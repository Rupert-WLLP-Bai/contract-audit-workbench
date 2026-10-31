package documentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"contractaudit/internal/store"
)

const (
	maxIntakeUploadBytes    = 32 << 20
	maxIntakeResponseBytes  = 1 << 20
	intakeUploadEndpoint    = "/internal/v1/intake-uploads"
	intakeUploadDefaultType = "application/octet-stream"
)

// IntakeUploader calls the document service's dedicated authenticated intake
// upload entry. It is a separate client from the read-only DocumentPreparer;
// the worker preparation credential is never used to impersonate a user.
type IntakeUploader struct {
	endpoint   string
	credential string
	http       *http.Client
}

// NewIntakeUploader creates a bounded client for the document-service internal
// intake upload endpoint.
func NewIntakeUploader(endpoint, credential string, timeout time.Duration) (*IntakeUploader, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || !isHTTPOrHTTPS(parsed.Scheme) ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("document-service endpoint must be an HTTP(S) origin or base path")
	}
	if len(credential) < 32 {
		return nil, errors.New("document-service credential must contain at least 32 bytes")
	}
	if timeout <= 0 {
		return nil, errors.New("document-service timeout must be positive")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + intakeUploadEndpoint
	parsed.RawPath = ""
	return &IntakeUploader{
		endpoint: parsed.String(), credential: credential,
		http: &http.Client{Timeout: timeout},
	}, nil
}

// UploadPrimaryContract uploads exactly one initial primary contract under the
// server-derived idempotency key and returns Document's stable receipt.
func (c *IntakeUploader) UploadPrimaryContract(ctx context.Context, caseID string, request store.PrimaryContractUploadRequest) (store.DocumentUploadResult, error) {
	if ctx == nil {
		return store.DocumentUploadResult{}, errors.New("document intake upload context is required")
	}
	if err := validateIntakeUploadRequest(caseID, request); err != nil {
		return store.DocumentUploadResult{}, err
	}
	media := strings.TrimSpace(request.MediaType)
	if media == "" {
		media = intakeUploadDefaultType
	}
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("actor_id", request.ActorID); err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("encode intake actor: %w", err)
	}
	if err := writer.WriteField("tenant_id", request.TenantID); err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("encode intake tenant: %w", err)
	}
	if err := writer.WriteField("org_id", request.OrgID); err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("encode intake organization: %w", err)
	}
	if err := writer.WriteField("case_id", caseID); err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("encode intake case: %w", err)
	}
	if err := writer.WriteField("idempotency_key", request.IdempotencyKey); err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("encode intake idempotency key: %w", err)
	}
	disposition := mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": request.Filename})
	if disposition == "" {
		return store.DocumentUploadResult{}, errors.New("intake filename cannot be encoded")
	}
	_, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {disposition},
		"Content-Type":        {media},
	})
	if err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("encode intake file part: %w", err)
	}
	// Keep only the multipart framing in memory; the file is already held by
	// Core and is read directly between the headers and the closing boundary.
	headerLength := payload.Len()
	if err := writer.Close(); err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("encode intake upload: %w", err)
	}
	header, trailer := payload.Bytes()[:headerLength], payload.Bytes()[headerLength:]
	newBody := func() io.Reader {
		return io.MultiReader(bytes.NewReader(header), bytes.NewReader(request.Body), bytes.NewReader(trailer))
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, newBody())
	if err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("create intake upload request: %w", err)
	}
	httpRequest.ContentLength = int64(len(header)) + int64(len(request.Body)) + int64(len(trailer))
	httpRequest.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(newBody()), nil }
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("X-Contract-Service-Key", c.credential)
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("document intake upload request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxIntakeResponseBytes+1))
	if err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("read document intake upload response: %w", err)
	}
	if len(body) > maxIntakeResponseBytes {
		return store.DocumentUploadResult{}, errors.New("document intake upload response exceeds the size limit")
	}
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return store.DocumentUploadResult{}, fmt.Errorf("document intake upload returned HTTP %d", response.StatusCode)
	}
	var result store.DocumentUploadResult
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return store.DocumentUploadResult{}, fmt.Errorf("decode document intake upload response: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return store.DocumentUploadResult{}, errors.New("document intake upload response contains trailing data")
	}
	if !validIntakeUploadID(result.DocumentID) || !validIntakeUploadID(result.MaterialID) || result.Version != 1 {
		return store.DocumentUploadResult{}, errors.New("document intake upload response lacks a primary material receipt")
	}
	return result, nil
}

func validateIntakeUploadRequest(caseID string, request store.PrimaryContractUploadRequest) error {
	if !validIntakeUploadID(request.ActorID) || request.TenantID == "" || request.OrgID == "" || !validIntakeUploadID(caseID) {
		return errors.New("document intake upload identity is incomplete")
	}
	if request.Filename == "" || strings.ContainsAny(request.Filename, "\r\n\x00") {
		return errors.New("document intake upload filename is invalid")
	}
	if len(request.Body) > maxIntakeUploadBytes {
		return errors.New("document intake upload file exceeds 32 MiB")
	}
	if key := request.IdempotencyKey; len(key) < 1 || len(key) > 200 {
		return errors.New("document intake upload idempotency key is invalid")
	} else {
		for _, char := range key {
			if char < 0x21 || char > 0x7e {
				return errors.New("document intake upload idempotency key is invalid")
			}
		}
	}
	if strings.ContainsAny(request.MediaType, "\r\n") {
		return errors.New("document intake upload media type is invalid")
	}
	return nil
}

func validIntakeUploadID(value string) bool {
	if value == "" || len(value) > 120 {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f || char == '/' || char == '\\' {
			return false
		}
	}
	return true
}

var _ store.CaseIntakeUploader = (*IntakeUploader)(nil)
