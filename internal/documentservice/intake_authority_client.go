package documentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"contractaudit/internal/coreauthority"
	"contractaudit/internal/store"
)

const coreDocumentIntakeAuthorityPath = "/internal/v1/document/intake-authority"

type intakeAuthorityResolver interface {
	Resolve(context.Context, string, string) (store.DocumentIntakeAuthority, error)
}

type coreIntakeAuthorityClient struct {
	endpoint   string
	credential string
	http       *http.Client
}

func newCoreIntakeAuthorityClient(endpoint, caFile string, allowInsecureDev bool, appEnv, credential string, timeout time.Duration) (*coreIntakeAuthorityClient, error) {
	if len(credential) < 32 || timeout <= 0 {
		return nil, errors.New("Core intake authority requires a private credential and positive timeout")
	}
	url, client, err := coreauthority.NewTransport(endpoint, coreDocumentIntakeAuthorityPath, caFile, appEnv, allowInsecureDev, timeout)
	if err != nil {
		return nil, err
	}
	return &coreIntakeAuthorityClient{
		endpoint: url, credential: credential, http: client,
	}, nil
}

func (client *coreIntakeAuthorityClient) Resolve(ctx context.Context, ticket, bodySHA256 string) (store.DocumentIntakeAuthority, error) {
	if client == nil || ctx == nil || ticket == "" || len(bodySHA256) != sha256.Size*2 {
		return store.DocumentIntakeAuthority{}, errors.New("Core intake authority request is incomplete")
	}
	encoded, err := json.Marshal(struct {
		Ticket     string `json:"ticket"`
		BodySHA256 string `json:"body_sha256"`
	}{ticket, bodySHA256})
	if err != nil {
		return store.DocumentIntakeAuthority{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return store.DocumentIntakeAuthority{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Contract-Service-Key", client.credential)
	response, err := client.http.Do(request)
	if err != nil {
		return store.DocumentIntakeAuthority{}, fmt.Errorf("Core intake authority request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return store.DocumentIntakeAuthority{}, fmt.Errorf("Core intake authority returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		return store.DocumentIntakeAuthority{}, errors.New("Core intake authority response is unavailable")
	}
	var authority store.DocumentIntakeAuthority
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&authority); err != nil {
		return store.DocumentIntakeAuthority{}, errors.New("Core intake authority response is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return store.DocumentIntakeAuthority{}, errors.New("Core intake authority response contains trailing data")
	}
	if authority.ActorID == "" || authority.TenantID == "" || authority.OrgID == "" || authority.Role == "" ||
		authority.CaseID == "" || authority.OperationID == "" || authority.UploadKey == "" ||
		authority.Action != "create_primary_contract" || authority.BodySHA256 != bodySHA256 {
		return store.DocumentIntakeAuthority{}, errors.New("Core intake authority response does not match the upload")
	}
	decoded, err := hex.DecodeString(authority.BodySHA256)
	if err != nil || len(decoded) != sha256.Size {
		return store.DocumentIntakeAuthority{}, errors.New("Core intake authority digest is invalid")
	}
	return authority, nil
}
