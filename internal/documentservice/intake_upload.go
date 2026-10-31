package documentservice

import (
	"context"
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
	actorID, ok := internalIntakeField(r, "actor_id")
	if !ok || actorID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake actor is required"})
		return
	}
	tenantID, ok := internalIntakeField(r, "tenant_id")
	if !ok || tenantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake tenant is required"})
		return
	}
	orgID, ok := internalIntakeField(r, "org_id")
	if !ok || orgID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake organization is required"})
		return
	}
	caseID, ok := internalIntakeField(r, "case_id")
	if !ok || caseID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake case is required"})
		return
	}
	idempotencyKey, ok := internalIntakeField(r, "idempotency_key")
	if !ok || idempotencyKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intake idempotency key is required"})
		return
	}
	directoryUser, ok := accounts.ByID(actorID)
	if !ok || !directoryUser.Enabled || directoryUser.TenantID != tenantID || directoryUser.OrgID != orgID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "current trusted intake actor is unavailable"})
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
	media := http.DetectContentType(body)
	user := accounts.User{ID: actorID, TenantID: tenantID, OrgID: orgID}
	var result store.DocumentUploadResult
	err = s.withPublicParse(r.Context(), func(ctx context.Context) (string, error) {
		return s.store.DocumentUploadKind(ctx, user, caseID, filename, media)
	}, func(ctx context.Context) error {
		var uploadErr error
		result, uploadErr = s.store.UploadMaterial(ctx, user, caseID, "draft_contract", filename, media, body,
			store.MaterialUploadOptions{Intent: "add", Purpose: "primary_contract", IdempotencyKey: idempotencyKey})
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
