package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

func trustedUser(user accounts.User) (accounts.User, error) {
	if user.HasResolvedMembership() {
		if user.ID == "" || !user.Enabled || user.TenantID == "" || user.OrgID == "" || user.Role == "" ||
			user.Issuer == "" || user.Subject == "" {
			return accounts.User{}, fault.Unauthenticated("登录身份已变更或账号不可用")
		}
		return user, nil
	}
	current, ok := accounts.ByID(user.ID)
	if !ok || !current.Enabled || current.TenantID == "" {
		return accounts.User{}, fault.Unauthenticated("登录已失效或账号不可用")
	}
	if (user.TenantID != "" && user.TenantID != current.TenantID) ||
		(user.OrgID != "" && user.OrgID != current.OrgID) ||
		(user.Role != "" && user.Role != current.Role) ||
		(user.Issuer != "" && user.Issuer != current.Issuer) ||
		(user.Subject != "" && user.Subject != current.Subject) {
		return accounts.User{}, fault.Unauthenticated("登录身份已变更")
	}
	return current, nil
}

func (s *Store) snapshotAs(user accounts.User) (state, error) {
	return s.snapshotAsContext(context.Background(), user)
}

func (s *Store) snapshotAsContext(ctx context.Context, user accounts.User) (state, error) {
	if err := ctx.Err(); err != nil {
		return state{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return state{}, err
	}
	return s.snapshotScoped(ctx, userScope(current))
}

func (s *Store) snapshotForAttemptContext(ctx context.Context, attemptID string) (state, error) {
	return s.snapshotAttemptRead(ctx, attemptID, true)
}

func (s *Store) snapshotForAttemptMetadataContext(ctx context.Context, attemptID string) (state, error) {
	return s.snapshotAttemptRead(ctx, attemptID, false)
}

func (s *Store) snapshotAttemptRead(ctx context.Context, attemptID string, withInput bool) (state, error) {
	if err := ctx.Err(); err != nil {
		return state{}, err
	}
	claim, ok := s.claimForAttempt(attemptID)
	if executionScopeRequiresClaim(ctx) && (!ok || !claimMatchesExecutionScope(ctx, claim)) {
		return state{}, errLeaseLost
	}
	if !ok || claim.AttemptID != attemptID {
		return state{}, errExecutionEventScope
	}
	st, err := s.readAttemptState(ctx, claim, withInput)
	if err != nil {
		return state{}, err
	}
	if claim.RunID != "" && (len(st.Runs) != 1 || st.Runs[0].ID != claim.RunID) {
		return state{}, errExecutionEventScope
	}
	return st, nil
}

func (s *Store) snapshotScoped(ctx context.Context, scope dbScope) (state, error) {
	return s.snapshotScopedClaim(ctx, scope, nil)
}

func (s *Store) snapshotScopedClaim(ctx context.Context, scope dbScope, claim *JobClaim) (state, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return state{}, err
	}
	if claim != nil && claim.JobID != "" {
		var live bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM worker_jobs
			WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND run_id=$5 AND attempt_id=$6
				AND actor_id=$7 AND worker_id=$8 AND generation=$9 AND state='running' AND lease_until > clock_timestamp())`,
			claim.JobID, claim.TenantID, claim.OrgID, claim.CaseID, claim.RunID, claim.AttemptID,
			claim.ActorID, claim.WorkerID, claim.Generation).Scan(&live)
		if err != nil {
			return state{}, err
		}
		if !live {
			return state{}, errLeaseLost
		}
	}
	// Per-request reads degrade a corrupted artifact to a per-document parse
	// failure instead of failing the whole request; startup and writes stay strict.
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return state{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return st, nil
}

func userScope(user accounts.User) dbScope {
	return dbScope{ActorID: user.ID, TenantID: user.TenantID, OrgID: user.OrgID, Role: "api"}
}

func (s *Store) updateAs(user accounts.User, fn func(*state) error) error {
	return s.updateAsContext(context.Background(), user, fn)
}

func (s *Store) updateAsContext(ctx context.Context, user accounts.User, fn func(*state) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateState(ctx, userScope(current), "", fn)
}

// bindLocalDemoCaseGrant binds an unbound case-scope grant for a local demo
// identity to the exact case it is a member of, reproducing membership-scoped
// content authorization. Enterprise identities are returned unchanged, so this
// never widens a real grant. Every access.Check on case content must go through the
// returned principal, because callers such as rulepack.Execute re-check it.
func bindLocalDemoCaseGrant(item Case, current accounts.User, principal access.Principal) access.Principal {
	if current.Issuer != "" || item.TenantID != current.TenantID ||
		(current.TenantID != accounts.LocalDemoTenant && current.TenantID != accounts.LocalDemoTenant2) {
		return principal
	}
	for _, memberID := range item.MemberIDs {
		if memberID != current.ID {
			continue
		}
		for i := range principal.Grants {
			if principal.Grants[i].Scope == "case" && principal.Grants[i].CaseID == "" {
				principal.Grants[i].CaseID = item.ID
			}
		}
		break
	}
	return principal
}

func authorizeCase(item Case, user accounts.User, action access.Action) bool {
	current, err := trustedUser(user)
	if err != nil {
		return false
	}
	principal := bindLocalDemoCaseGrant(item, current, accounts.Principal(current))
	resource := access.Resource{TenantID: item.TenantID, CaseID: item.ID, OrgID: item.OwnerOrgID, OwnerID: item.OwnerID}
	return access.Check(action, principal, resource, time.Now().UTC()).Allowed
}

func caseReadAllowed(item Case, user accounts.User) bool {
	return authorizeCase(item, user, access.Read)
}

func validateAudienceUser(user accounts.User, audience string) error {
	switch audience {
	case "client":
		return nil
	case "admin":
		if user.Role != "admin" {
			return fault.Forbidden("当前身份不能建立管理会话")
		}
		return nil
	default:
		return fault.Unauthenticated("会话 audience 无效")
	}
}

func (s *Store) sessionFromDB(ctx context.Context, token string) (session, accounts.User, error) {
	if token == "" {
		return session{}, accounts.User{}, fault.Unauthenticated("登录已失效或账号密码不正确")
	}
	return s.sessionFromDigest(ctx, sessionTokenDigest(token))
}

func (s *Store) sessionFromDigest(ctx context.Context, digest string) (session, accounts.User, error) {
	if len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return session{}, accounts.User{}, fault.Unauthenticated("登录已失效或账号密码不正确")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return session{}, accounts.User{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalSessionScope(ctx, tx, digest); err != nil {
		return session{}, accounts.User{}, err
	}
	readSession := func() (session, error) {
		var current session
		err := tx.QueryRow(ctx, `SELECT token, actor_id, issuer, subject, tenant_id, org_id, audience, expires_at, principal_id, membership_id
		FROM sessions WHERE token=$1`, digest).Scan(&current.Token, &current.ActorID, &current.Issuer,
			&current.Subject, &current.TenantID, &current.OrgID, &current.Audience, &current.ExpiresAt,
			&current.PrincipalID, &current.MembershipID)
		return current, err
	}
	current, err := readSession()
	if errors.Is(err, pgx.ErrNoRows) {
		return session{}, accounts.User{}, fault.Unauthenticated("登录已失效或账号密码不正确")
	}
	if err != nil {
		return session{}, accounts.User{}, err
	}
	if current.PrincipalID != nil || current.MembershipID != nil {
		// The first token-scoped read only classifies this immutable session as
		// membership-backed. Wait for controlled identity changes before the
		// authoritative reread, which runs as a fresh READ COMMITTED statement.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, s.schema+":tenant-identity"); err != nil {
			return session{}, accounts.User{}, err
		}
		current, err = readSession()
		if errors.Is(err, pgx.ErrNoRows) {
			return session{}, accounts.User{}, fault.Unauthenticated("登录已失效或账号密码不正确")
		}
		if err != nil {
			return session{}, accounts.User{}, err
		}
	}
	expires, err := time.Parse(timeLayout, current.ExpiresAt)
	if err != nil || !expires.After(time.Now().UTC()) {
		return session{}, accounts.User{}, fault.Unauthenticated("登录已失效或账号密码不正确")
	}
	var user accounts.User
	if current.PrincipalID != nil || current.MembershipID != nil {
		if current.PrincipalID == nil || current.MembershipID == nil || current.Issuer == "" || current.Subject == "" {
			return session{}, accounts.User{}, fault.Unauthenticated("登录身份已变更或账号不可用")
		}
		var principalID, actorID, issuer, subject, audience, membershipID, tenantID, tenantName, orgID, orgName, role string
		var jurisdictionID *string
		var grantsJSON []byte
		var expiresAt string
		err := tx.QueryRow(ctx, `SELECT principal_id,actor_id,issuer,subject,audience,membership_id,tenant_id,tenant_name,
			jurisdiction_id,org_unit_id,org_unit_name,role_code,grants,expires_at
			FROM tenant_identity_session_user($1)`, digest).Scan(&principalID, &actorID, &issuer, &subject, &audience,
			&membershipID, &tenantID, &tenantName, &jurisdictionID, &orgID, &orgName, &role, &grantsJSON, &expiresAt)
		if err != nil || principalID != *current.PrincipalID || audience != current.Audience || actorID != current.ActorID || issuer != current.Issuer || subject != current.Subject ||
			membershipID != *current.MembershipID || tenantID != current.TenantID || orgID != current.OrgID {
			return session{}, accounts.User{}, fault.Unauthenticated("登录身份已变更或账号不可用")
		}
		profile, ok := accounts.ByID(actorID)
		if !ok || !profile.Enabled || profile.Issuer != issuer || profile.Subject != subject {
			return session{}, accounts.User{}, fault.Unauthenticated("登录身份已变更或账号不可用")
		}
		var grants []access.Grant
		if err := json.Unmarshal(grantsJSON, &grants); err != nil {
			return session{}, accounts.User{}, fault.Unauthenticated("登录身份已变更或账号不可用")
		}
		user = accounts.MembershipUser(actorID, profile.DisplayName, role, tenantID, orgID, issuer, subject, grants)
		user.PrincipalID, user.MembershipID = principalID, membershipID
	} else {
		// A pre-membership enterprise session must never fall back to the
		// directory's tenant or grants. Legacy local demonstration sessions are
		// retained only for the non-enterprise path.
		if current.Issuer != "" || current.Subject != "" {
			return session{}, accounts.User{}, fault.Unauthenticated("请重新登录以刷新成员身份")
		}
		var ok bool
		user, ok = accounts.ByID(current.ActorID)
		if !ok || !user.Enabled || user.TenantID != current.TenantID || user.OrgID != current.OrgID {
			return session{}, accounts.User{}, fault.Unauthenticated("登录身份已变更或账号不可用")
		}
	}
	if err := validateAudienceUser(user, current.Audience); err != nil {
		return session{}, accounts.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return session{}, accounts.User{}, err
	}
	return current, user, nil
}
