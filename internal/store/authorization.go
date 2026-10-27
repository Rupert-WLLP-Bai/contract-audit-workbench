package store

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

func trustedUser(user accounts.User) (accounts.User, error) {
	current, ok := accounts.ByID(user.ID)
	if !ok || !current.Enabled {
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
	current, err := trustedUser(user)
	if err != nil {
		return state{}, err
	}
	return s.snapshotScoped(context.Background(), userScope(current))
}

func (s *Store) snapshotForClaim(claim JobClaim) (state, error) {
	return s.snapshotScoped(context.Background(), claim.scope())
}

func (s *Store) snapshotForAttempt(attemptID string) state {
	if claim, ok := s.claimForAttempt(attemptID); ok {
		st, err := s.snapshotForClaim(claim)
		if err == nil {
			return st
		}
		return state{}
	}
	return s.snapshot()
}

func (s *Store) snapshotScoped(ctx context.Context, scope dbScope) (state, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return state{}, err
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
	return dbScope{ActorID: user.ID, TenantID: accounts.Tenant(user), OrgID: user.OrgID, Role: "api"}
}

func (s *Store) updateAs(user accounts.User, fn func(*state) error) error {
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateState(context.Background(), userScope(current), "", fn)
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

func workerScope(job Job) dbScope {
	return dbScope{ActorID: job.ActorID, TenantID: job.TenantID, OrgID: job.OrgID, Role: "worker"}
}

func localDemoScope(actorID string) dbScope {
	return dbScope{ActorID: actorID, TenantID: accounts.LocalDemoTenant, OrgID: "demo-org", Role: "api"}
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

func (s *Store) snapshotForSession(ctx context.Context, token string) (state, error) {
	if token == "" {
		return state{}, fault.Unauthenticated("登录已失效或账号密码不正确")
	}
	return s.snapshotScoped(ctx, dbScope{ActorID: "session", SessionToken: token, Role: "api"})
}

func (s *Store) sessionFromDB(ctx context.Context, token string) (session, error) {
	if token == "" {
		return session{}, fault.Unauthenticated("登录已失效或账号密码不正确")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return session{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "session", SessionToken: token, Role: "api"}); err != nil {
		return session{}, err
	}
	var current session
	err = tx.QueryRow(ctx, `SELECT token, actor_id, issuer, subject, tenant_id, org_id, audience, expires_at
		FROM sessions WHERE token=$1`, token).Scan(&current.Token, &current.ActorID, &current.Issuer,
		&current.Subject, &current.TenantID, &current.OrgID, &current.Audience, &current.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return session{}, fault.Unauthenticated("登录已失效或账号密码不正确")
	}
	if err != nil {
		return session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return session{}, err
	}
	expires, err := time.Parse(timeLayout, current.ExpiresAt)
	if err != nil || !expires.After(time.Now().UTC()) {
		return session{}, fault.Unauthenticated("登录已失效或账号密码不正确")
	}
	user, ok := accounts.ByID(current.ActorID)
	if !ok || !user.Enabled || user.TenantID != current.TenantID || user.OrgID != current.OrgID ||
		user.Issuer != current.Issuer || user.Subject != current.Subject {
		return session{}, fault.Unauthenticated("登录身份已变更或账号不可用")
	}
	if err := validateAudienceUser(user, current.Audience); err != nil {
		return session{}, err
	}
	return current, nil
}
