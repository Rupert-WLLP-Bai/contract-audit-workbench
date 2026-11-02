package documentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

const (
	internalIntakeBodyLimit = 33 << 20
	internalIntakeFileLimit = 32 << 20
)

// intakeUpload is the document-owned, authenticated internal entry Core calls
// after its intake transaction commits. It re-resolves the asserted trusted
// actor and rechecks current case management authority before writing.
func (s *Service) intakeUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !validPrepareCredential(r.Header.Values("X-Contract-Service-Key"), s.config.IntakeCredential) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service authentication required", "code": "service_unauthenticated"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, internalIntakeBodyLimit)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "too large") || errors.Is(err, io.ErrUnexpectedEOF) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "intake upload exceeds the size limit"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid intake upload request"})
		return
	}
	defer r.MultipartForm.RemoveAll()
	if len(r.MultipartForm.File) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exactly one intake file is required"})
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake file is required"})
		return
	}
	caseID, ok := internalIntakeField(r, "case_id")
	if !ok || caseID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake case is required"})
		return
	}
	ticket, ok := internalIntakeField(r, "authority_ticket")
	if !ok || ticket == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake authority is required"})
		return
	}
	if s.intakeAuthority == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "trusted Core intake authority is unavailable"})
		return
	}
	file, err := files[0].Open()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake file cannot be read"})
		return
	}
	defer file.Close()
	if files[0].Size > internalIntakeFileLimit {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "intake file exceeds 32 MiB"})
		return
	}
	// The multipart parser has measured the complete file part already.
	body := make([]byte, int(files[0].Size))
	if _, err := io.ReadFull(file, body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake file cannot be read"})
		return
	}
	filename := files[0].Filename
	if filename == "" || strings.ContainsAny(filename, "\r\n\x00/\\") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake filename is invalid"})
		return
	}
	for _, char := range filename {
		if char < 0x20 || char == 0x7f {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake filename is invalid"})
			return
		}
	}
	if s.intakeAuthority == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "trusted Core intake authority is unavailable"})
		return
	}
	bodyDigest := sha256.Sum256(body)
	bodySHA256 := hex.EncodeToString(bodyDigest[:])
	authority, err := s.intakeAuthority.Resolve(r.Context(), ticket, bodySHA256)
	if err != nil || authority.CaseID != caseID || authority.Filename != filename ||
		authority.BodySHA256 != bodySHA256 || authority.Action != "create_primary_contract" ||
		files[0].Header.Get("Content-Type") != authority.MediaType {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "current trusted intake authority is unavailable"})
		return
	}
	user := accounts.MembershipUser(authority.ActorID, "", authority.Role, authority.TenantID, authority.OrgID,
		authority.Issuer, authority.Subject, authority.Grants)
	user.PrincipalID, user.MembershipID = authority.PrincipalID, authority.MembershipID
	if authority.PrincipalID == "" {
		user = accounts.User{ID: authority.ActorID, Role: authority.Role, TenantID: authority.TenantID, OrgID: authority.OrgID, Enabled: true, Grants: authority.Grants}
	}
	media := authority.MediaType
	var result store.DocumentUploadResult
	err = s.withPublicParse(store.WithOperationTicket(r.Context(), ticket), func(ctx context.Context) (string, error) {
		return s.store.DocumentUploadKind(ctx, user, caseID, filename, media)
	}, func(ctx context.Context) error {
		var uploadErr error
		result, uploadErr = s.store.UploadMaterial(ctx, user, caseID, "draft_contract", filename, media, body,
			store.MaterialUploadOptions{Intent: "add", Purpose: "primary_contract", IdempotencyKey: authority.UploadKey})
		return uploadErr
	})
	if err != nil {
		writeIntakeUploadFailure(w, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func internalIntakeField(r *http.Request, name string) (string, bool) {
	values := r.MultipartForm.Value[name]
	if len(values) == 0 {
		return "", true
	}
	for _, value := range values[1:] {
		if value != values[0] {
			return "", false
		}
	}
	return values[0], true
}

func writeIntakeUploadFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, errParseCapacityFull) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "document intake capacity is full"})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "document intake upload timed out"})
		return
	}
	var failure *fault.Error
	if errors.As(err, &failure) {
		writeJSON(w, failure.Status, map[string]any{"error": map[string]any{
			"code": failure.Code, "message": failure.Message, "retryable": failure.Retryable,
		}})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "document intake upload failed"})
}
