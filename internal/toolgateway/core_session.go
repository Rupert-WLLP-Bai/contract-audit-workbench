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
	"contractaudit/internal/fault"
	"contractaudit/internal/operationauthority"
	"contractaudit/internal/store"
)

const coreSessionAuthorityPath = "/internal/v1/session-authority"

type SessionResolver interface {
	AuthenticateSession(context.Context, string) (accounts.User, string, error)
}

type coreSessionResolver struct {
	endpoint string
	http     *http.Client
}

type coreSessionProjection = operationauthority.SessionProjection

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
		if response.StatusCode == 401 {
			return accounts.User{}, "", fault.New(401, "session_invalid", "会话已失效")
		}
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
	if projection.ContextVersion != "" {
		if !store.ValidContextVersion(projection.ContextVersion) || projection.CSRFToken == "" || projection.AuthorityRevision == "" {
			return accounts.User{}, "", errors.New("Core session context metadata unavailable")
		}
		user = store.BindCommandUser(user, token, projection.ContextVersion, projection.AuthorityRevision)
		proof, _ := user.CommandAuthority()
		proof.CSRFToken = projection.CSRFToken
		proof.TenantName = projection.TenantName
		proof.OrgName = projection.OrgName
		user = accounts.WithCommandAuthority(user, proof)
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
