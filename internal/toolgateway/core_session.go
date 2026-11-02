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

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/coreauthority"
)

const coreSessionAuthorityPath = "/internal/v1/session-authority"

type SessionResolver interface {
	AuthenticateSession(context.Context, string) (accounts.User, string, error)
}

type coreSessionResolver struct {
	endpoint string
	http     *http.Client
}

type coreSessionProjection struct {
	Audience        string         `json:"audience"`
	MembershipBound bool           `json:"membership_bound"`
	ActorID         string         `json:"actor_id"`
	DisplayName     string         `json:"display_name"`
	Role            string         `json:"role"`
	TenantID        string         `json:"tenant_id"`
	OrgID           string         `json:"org_id"`
	Enabled         bool           `json:"enabled"`
	Issuer          string         `json:"issuer,omitempty"`
	Subject         string         `json:"subject,omitempty"`
	PrincipalID     string         `json:"principal_id,omitempty"`
	MembershipID    string         `json:"membership_id,omitempty"`
	Grants          []access.Grant `json:"grants"`
}

func NewCoreSessionResolver(origin, caFile string, allowInsecureDev bool, appEnv string, timeout time.Duration) (SessionResolver, error) {
	endpoint, client, err := coreauthority.NewTransport(origin, coreSessionAuthorityPath, caFile, appEnv, allowInsecureDev, timeout)
	if err != nil {
		return nil, err
	}
	return &coreSessionResolver{endpoint: endpoint, http: client}, nil
}

func (resolver *coreSessionResolver) AuthenticateSession(ctx context.Context, token string) (accounts.User, string, error) {
	if resolver == nil || ctx == nil || token == "" || len(token) > 4096 || strings.ContainsAny(token, " \t\r\n") {
		return accounts.User{}, "", errors.New("session token is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, resolver.endpoint, nil)
	if err != nil {
		return accounts.User{}, "", errors.New("Core session authority request is unavailable")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := resolver.http.Do(request)
	if err != nil {
		return accounts.User{}, "", fmt.Errorf("Core session authority request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return accounts.User{}, "", fmt.Errorf("Core session authority returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return accounts.User{}, "", errors.New("Core session authority response is unavailable")
	}
	var projection coreSessionProjection
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&projection); err != nil {
		return accounts.User{}, "", errors.New("Core session authority response is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return accounts.User{}, "", errors.New("Core session authority response contains trailing data")
	}
	if projection.Audience != "client" || !projection.Enabled || projection.ActorID == "" || projection.TenantID == "" ||
		projection.OrgID == "" || projection.Role == "" {
		return accounts.User{}, "", errors.New("Core session authority response lacks a valid client membership")
	}
	var user accounts.User
	if projection.MembershipBound {
		if projection.Issuer == "" || projection.Subject == "" || projection.PrincipalID == "" || projection.MembershipID == "" {
			return accounts.User{}, "", errors.New("Core session authority response has an incomplete membership binding")
		}
		user = accounts.MembershipUser(projection.ActorID, projection.DisplayName, projection.Role,
			projection.TenantID, projection.OrgID, projection.Issuer, projection.Subject, projection.Grants)
		user.PrincipalID, user.MembershipID = projection.PrincipalID, projection.MembershipID
	} else {
		if projection.Issuer != "" || projection.Subject != "" || projection.PrincipalID != "" || projection.MembershipID != "" {
			return accounts.User{}, "", errors.New("unbound Core session projection contains enterprise identity fields")
		}
		user = accounts.User{ID: projection.ActorID, DisplayName: projection.DisplayName, Role: projection.Role,
			TenantID: projection.TenantID, OrgID: projection.OrgID, Enabled: true, Grants: projection.Grants}
	}
	return user, projection.Audience, nil
}

type sessionResolverBackend struct {
	Backend
	resolver SessionResolver
}

func WithCoreSessionResolver(backend Backend, resolver SessionResolver) Backend {
	if backend == nil || resolver == nil {
		return backend
	}
	return sessionResolverBackend{Backend: backend, resolver: resolver}
}

func (backend sessionResolverBackend) AuthenticateSession(ctx context.Context, token string) (accounts.User, string, error) {
	return backend.resolver.AuthenticateSession(ctx, token)
}
