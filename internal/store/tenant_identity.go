package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaName returns the schema selected for this Store. Offline administrative
// tooling and isolated integration fixtures use it to bind explicit identity
// crosswalk work to the same schema as the application connection.
func (s *Store) SchemaName() string {
	if s == nil {
		return ""
	}
	return s.schema
}

// ResolveVerifiedOIDC uses only the provider-verified issuer/subject and the
// trusted directory's enabled/profile record. Tenant authority is resolved by
// the database in the same transaction that creates a single-membership
// session or a multi-membership pending challenge.
func (s *Store) ResolveVerifiedOIDC(ctx context.Context, profile accounts.User, audience string) (accounts.OIDCLoginResult, error) {
	ctx = commandContext(ctx, profile)
	if s == nil || s.pool == nil || !profile.Enabled || profile.Issuer == "" || profile.Subject == "" ||
		(audience != "client" && audience != "admin") {
		return accounts.OIDCLoginResult{}, fault.Unauthenticated("登录身份不可用")
	}
	pendingRaw, err := randomIdentityToken("p_")
	if err != nil {
		return accounts.OIDCLoginResult{}, err
	}
	sessionRaw, err := newSessionToken()
	if err != nil {
		return accounts.OIDCLoginResult{}, err
	}
	pendingDigest := sha256.Sum256([]byte(pendingRaw))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT pending,principal_id,actor_id,issuer,subject,membership_id,tenant_id,tenant_name,
		jurisdiction_id,org_unit_id,org_unit_name,role_code,grants,principal_revision,tenant_revision,membership_revision
		FROM tenant_identity_oidc_login($1,$2,$3,$4,$5,$6)`, profile.Issuer, profile.Subject, audience,
		hex.EncodeToString(pendingDigest[:]), sessionTokenDigest(sessionRaw), time.Now().UTC().Add(7*24*time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		return accounts.OIDCLoginResult{}, mapIdentityError(err)
	}
	defer rows.Close()
	result := accounts.OIDCLoginResult{}
	for rows.Next() {
		var pending bool
		var principalID, actorID, issuer, subject, tenantID, tenantName, orgID, orgName, role string
		var membershipID, jurisdictionID *string
		var grantsJSON []byte
		var principalRevision, tenantRevision, membershipRevision int64
		if err := rows.Scan(&pending, &principalID, &actorID, &issuer, &subject, &membershipID,
			&tenantID, &tenantName, &jurisdictionID, &orgID, &orgName, &role, &grantsJSON,
			&principalRevision, &tenantRevision, &membershipRevision); err != nil {
			return accounts.OIDCLoginResult{}, err
		}
		if pending {
			result.PendingToken = pendingRaw
			choice := accounts.MembershipChoice{ID: *membershipID, TenantID: tenantID, TenantName: tenantName, OrgID: orgID, OrgName: orgName, Role: role}
			if jurisdictionID != nil {
				choice.Jurisdiction = *jurisdictionID
			}
			result.Choices = append(result.Choices, choice)
			continue
		}
		var grants []access.Grant
		if err := json.Unmarshal(grantsJSON, &grants); err != nil {
			return accounts.OIDCLoginResult{}, errors.New("database membership grants are invalid")
		}
		if actorID != profile.ID || issuer != profile.Issuer || subject != profile.Subject {
			return accounts.OIDCLoginResult{}, fault.Unauthenticated("目录身份与数据库映射不一致")
		}
		result.User = accounts.MembershipUser(actorID, profile.DisplayName, role, tenantID, orgID, issuer, subject, grants)
		result.User.PrincipalID, result.User.MembershipID = principalID, *membershipID
		result.SessionToken = sessionRaw
	}
	if err := rows.Err(); err != nil {
		return accounts.OIDCLoginResult{}, err
	}
	if result.PendingToken == "" && result.SessionToken == "" {
		return accounts.OIDCLoginResult{}, fault.Unauthenticated("没有可用的租户成员关系")
	}
	latestProfile, found := accounts.BySubject(profile.Issuer, profile.Subject)
	if !found || !latestProfile.Enabled || latestProfile.ID != profile.ID {
		return accounts.OIDCLoginResult{}, fault.Unauthenticated("目录身份已停用或映射已变更")
	}
	if result.SessionToken != "" {
		current, _, err := s.AuthenticateSession(ctx, result.SessionToken)
		if err != nil {
			return accounts.OIDCLoginResult{}, err
		}
		result.User = current
	}
	return result, nil
}

// ListPendingMemberships reveals only live choices belonging to the opaque
// pending token. The caller must never use this list as a reusable grant.
func (s *Store) ListPendingMemberships(ctx context.Context, token, audience string) ([]accounts.MembershipChoice, error) {
	digest := identityTokenDigest(token)
	if digest == "" || (audience != "client" && audience != "admin") {
		return nil, fault.Unauthenticated("登录选择已失效")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT actor_id,issuer,subject,membership_id,tenant_id,tenant_name,jurisdiction_id,org_unit_id,org_unit_name,role_code
		FROM tenant_identity_list_pending($1,$2)`, digest, audience)
	if err != nil {
		return nil, mapIdentityError(err)
	}
	defer rows.Close()
	choices := make([]accounts.MembershipChoice, 0)
	for rows.Next() {
		var actorID, issuer, subject, id, tenantID, tenantName, orgID, orgName, role string
		var jurisdictionID *string
		if err := rows.Scan(&actorID, &issuer, &subject, &id, &tenantID, &tenantName, &jurisdictionID, &orgID, &orgName, &role); err != nil {
			return nil, err
		}
		profile, ok := accounts.ByID(actorID)
		if !ok || !profile.Enabled || profile.Issuer != issuer || profile.Subject != subject {
			return nil, fault.Unauthenticated("登录身份已停用或映射已变更")
		}
		choice := accounts.MembershipChoice{ID: id, TenantID: tenantID, TenantName: tenantName, OrgID: orgID, OrgName: orgName, Role: role}
		if jurisdictionID != nil {
			choice.Jurisdiction = *jurisdictionID
		}
		choices = append(choices, choice)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(choices) == 0 {
		return nil, fault.Unauthenticated("登录选择已失效")
	}
	return choices, nil
}

// ConsumePendingMembership atomically validates the selected membership,
// consumes the challenge and writes its audience-bound session.
func (s *Store) ConsumePendingMembership(ctx context.Context, token, membershipID, audience string) (accounts.OIDCLoginResult, error) {
	digest := identityTokenDigest(token)
	if digest == "" || membershipID == "" || (audience != "client" && audience != "admin") {
		return accounts.OIDCLoginResult{}, fault.Unauthenticated("登录选择已失效")
	}
	sessionRaw, err := newSessionToken()
	if err != nil {
		return accounts.OIDCLoginResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result accounts.OIDCLoginResult
	var principalID, actorID, issuer, subject, tenantID, tenantName, orgID, orgName, role string
	var membership string
	var jurisdictionID *string
	var grantsJSON []byte
	var principalRevision, tenantRevision, membershipRevision int64
	err = s.pool.QueryRow(ctx, `SELECT principal_id,actor_id,issuer,subject,membership_id,tenant_id,tenant_name,jurisdiction_id,
		org_unit_id,org_unit_name,role_code,grants,principal_revision,tenant_revision,membership_revision
		FROM tenant_identity_consume_pending($1,$2::uuid,$3,$4,$5)`, digest, membershipID,
		sessionTokenDigest(sessionRaw), audience, time.Now().UTC().Add(7*24*time.Hour).Format(time.RFC3339Nano)).Scan(
		&principalID, &actorID, &issuer, &subject, &membership, &tenantID, &tenantName, &jurisdictionID,
		&orgID, &orgName, &role, &grantsJSON, &principalRevision, &tenantRevision, &membershipRevision)
	if err != nil {
		return result, mapIdentityError(err)
	}
	profile, ok := accounts.ByID(actorID)
	if !ok || !profile.Enabled || profile.ID != actorID || profile.Issuer != issuer || profile.Subject != subject {
		return result, fault.Unauthenticated("目录身份已停用或映射已变更")
	}
	var grants []access.Grant
	if err := json.Unmarshal(grantsJSON, &grants); err != nil {
		return result, fmt.Errorf("database membership grants are invalid")
	}
	result.SessionToken = sessionRaw
	result.User = accounts.MembershipUser(actorID, profile.DisplayName, role, tenantID, orgID, issuer, subject, grants)
	result.User.PrincipalID, result.User.MembershipID = principalID, membership
	current, _, err := s.AuthenticateSession(ctx, result.SessionToken)
	if err != nil {
		return accounts.OIDCLoginResult{}, err
	}
	result.User = current
	return result, nil
}

func randomIdentityToken(prefix string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func identityTokenDigest(token string) string {
	if len(token) < 32 || len(token) > 128 {
		return ""
	}
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func mapIdentityError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "28000" || pgErr.Code == "22023" || pgErr.Code == "23505") {
		return fault.Unauthenticated("登录身份不可用")
	}
	return err
}
