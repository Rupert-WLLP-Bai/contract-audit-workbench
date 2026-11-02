package toolgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/coreauthority"
	"contractaudit/internal/operationauthority"
	"contractaudit/internal/store"
)

const coreToolClaimAuthorityPath = "/internal/v1/tool/claim-authority"

type coreClaimRequest = operationauthority.ClaimRequest
type coreClaimProjection = operationauthority.ClaimProjection

type CoreClaimResolver interface {
	AuthorizeToolResolution(context.Context, store.ToolInvocationContext) (store.ToolAuthority, error)
}

type coreClaimResolver struct {
	endpoint string
	http     *http.Client
	token    string
}

func NewCoreClaimResolver(origin, caFile string, allowInsecureDev bool, appEnv, token string, timeout time.Duration) (CoreClaimResolver, error) {
	if len(token) < 32 || strings.TrimSpace(token) != token {
		return nil, errors.New("TOOL_CORE_AUTHORITY_TOKEN must contain at least 32 non-whitespace bytes")
	}
	endpoint, client, err := coreauthority.NewTransport(origin, coreToolClaimAuthorityPath, caFile, appEnv, allowInsecureDev, timeout)
	if err != nil {
		return nil, err
	}
	return &coreClaimResolver{endpoint: endpoint, http: client, token: token}, nil
}

func (resolver *coreClaimResolver) AuthorizeToolResolution(ctx context.Context, in store.ToolInvocationContext) (store.ToolAuthority, error) {
	if resolver == nil || ctx == nil || in.TenantID == "" || in.OrgID == "" || in.JobID == "" || in.WorkerID == "" ||
		in.Generation <= 0 || in.CaseID == "" || in.RunID == "" || in.AttemptID == "" {
		return store.ToolAuthority{}, store.ErrToolScopeUnavailable
	}
	requestBody, err := json.Marshal(coreClaimRequest{TenantID: in.TenantID, OrgID: in.OrgID, JobID: in.JobID,
		WorkerID: in.WorkerID, Generation: in.Generation, CaseID: in.CaseID, RunID: in.RunID, AttemptID: in.AttemptID})
	if err != nil {
		return store.ToolAuthority{}, errors.New("Core claim authority request cannot be encoded")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, resolver.endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return store.ToolAuthority{}, errors.New("Core claim authority request is unavailable")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+resolver.token)
	response, err := resolver.http.Do(request)
	if err != nil {
		return store.ToolAuthority{}, fmt.Errorf("Core claim authority request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return store.ToolAuthority{}, store.ErrToolScopeUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return store.ToolAuthority{}, errors.New("Core claim authority response is unavailable")
	}
	var projection coreClaimProjection
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&projection); err != nil {
		return store.ToolAuthority{}, errors.New("Core claim authority response is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return store.ToolAuthority{}, errors.New("Core claim authority response contains trailing data")
	}
	if projection.JobID != in.JobID || projection.CaseID != in.CaseID || projection.RunID != in.RunID || projection.AttemptID != in.AttemptID ||
		projection.Audience != "worker_claim" || !projection.Enabled || projection.ActorID == "" || projection.TenantID != in.TenantID ||
		projection.OrgID != in.OrgID || projection.Role == "" {
		return store.ToolAuthority{}, errors.New("Core claim authority response does not match the requested live scope")
	}
	var user accounts.User
	if projection.UserOrgID == "" {
		return store.ToolAuthority{}, errors.New("Core claim actor organization unavailable")
	}
	if projection.MembershipBound {
		if projection.Issuer == "" || projection.Subject == "" || projection.PrincipalID == "" || projection.MembershipID == "" {
			return store.ToolAuthority{}, errors.New("Core claim authority response has incomplete membership fields")
		}
		user = accounts.MembershipUser(projection.ActorID, projection.DisplayName, projection.Role, projection.TenantID,
			projection.UserOrgID, projection.Issuer, projection.Subject, projection.Grants)
		user.PrincipalID, user.MembershipID = projection.PrincipalID, projection.MembershipID
	} else {
		if projection.Issuer != "" || projection.Subject != "" || projection.PrincipalID != "" || projection.MembershipID != "" {
			return store.ToolAuthority{}, errors.New("unbound Core claim contains enterprise identity fields")
		}
		user = accounts.User{ID: projection.ActorID, DisplayName: projection.DisplayName, Role: projection.Role,
			TenantID: projection.TenantID, OrgID: projection.UserOrgID, Enabled: true, Grants: projection.Grants}
	}
	return store.ToolAuthority{User: user, TenantID: projection.TenantID, OrgID: projection.OrgID,
		CaseID: projection.CaseID, RunID: projection.RunID, AttemptID: projection.AttemptID, JobID: projection.JobID}, nil
}

type coreClaimBackend struct {
	Backend
	resolver CoreClaimResolver
}

func WithCoreClaimResolver(backend Backend, resolver CoreClaimResolver) Backend {
	if backend == nil || resolver == nil {
		return backend
	}
	return coreClaimBackend{Backend: backend, resolver: resolver}
}

func (backend coreClaimBackend) AuthorizeToolResolution(ctx context.Context, request store.ToolInvocationContext) (store.ToolAuthority, error) {
	return backend.resolver.AuthorizeToolResolution(ctx, request)
}
