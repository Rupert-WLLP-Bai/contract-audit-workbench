package store

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ActiveContext struct {
	MembershipID   *string `json:"membership_id"`
	TenantID       string  `json:"tenant_id"`
	TenantName     string  `json:"tenant_name"`
	OrgID          string  `json:"org_id"`
	OrgName        string  `json:"org_name"`
	ContextVersion string  `json:"context_version"`
}
type SessionContext struct {
	role              *string
	grants            []byte
	actor             string
	ActiveContext     ActiveContext `json:"active_context"`
	CSRFToken         string        `json:"csrf_token"`
	AuthorityRevision string        `json:"-"`
}

func ValidContextVersion(raw string) bool {
	v, e := strconv.ParseInt(raw, 10, 64)
	return e == nil && v > 0 && strconv.FormatInt(v, 10) == raw
}
func contextError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch {
		case pg.Code == "40001":
			return fault.New(409, "context_stale", "当前上下文已变更，请重新读取会话")
		case pg.Code == "22023" && strings.HasPrefix(pg.Message, "invalid_support"):
			return fault.Invalid("支援授权参数无效")
		case pg.Code == "28000":
			return fault.New(401, "session_invalid", "会话或当前成员授权已失效")
		case pg.Code == "42501" && pg.Message == "access_denied":
			return fault.New(403, "access_denied", "当前上下文没有该项授权")
		}
	}
	return err
}
func (s *Store) sessionContextTx(ctx context.Context, tx pgx.Tx, digest string) (SessionContext, error) {
	var c SessionContext
	var actor, tenant, org string
	var principal, membership, role *string
	var grants []byte
	var version int64
	err := tx.QueryRow(ctx, `SELECT actor_id,principal_id,membership_id,tenant_id,tenant_name,org_id,org_name,role_code,grants,context_version,csrf_token,authority_revision FROM tenant_context_info($1)`, digest).Scan(&actor, &principal, &membership, &tenant, &c.ActiveContext.TenantName, &org, &c.ActiveContext.OrgName, &role, &grants, &version, &c.CSRFToken, &c.AuthorityRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fault.New(401, "session_invalid", "会话或当前成员授权已失效")
	}
	if err != nil {
		return c, contextError(err)
	}
	c.role = role
	c.grants = append([]byte(nil), grants...)
	c.actor = actor
	c.ActiveContext.MembershipID = membership
	c.ActiveContext.TenantID = tenant
	c.ActiveContext.OrgID = org
	c.ActiveContext.ContextVersion = strconv.FormatInt(version, 10)
	return c, nil
}
func (s *Store) SessionContext(ctx context.Context, token string) (SessionContext, error) {
	user, _, err := s.AuthenticateSession(ctx, token)
	if err != nil {
		return SessionContext{}, fault.New(401, "session_invalid", "会话或当前成员授权已失效")
	}
	return UserSessionContext(user)
}
func UserSessionContext(user accounts.User) (SessionContext, error) {
	proof, ok := user.CommandAuthority()
	if !ok || !ValidContextVersion(proof.ContextVersion) || proof.CSRFToken == "" {
		return SessionContext{}, fault.New(401, "session_invalid", "会话上下文不可用")
	}
	var member *string
	if user.MembershipID != "" {
		v := user.MembershipID
		member = &v
	}
	return SessionContext{ActiveContext: ActiveContext{MembershipID: member, TenantID: user.TenantID, TenantName: proof.TenantName, OrgID: user.OrgID, OrgName: proof.OrgName, ContextVersion: proof.ContextVersion}, CSRFToken: proof.CSRFToken, AuthorityRevision: proof.Revision}, nil
}

func (s *Store) ListSessionContexts(ctx context.Context, token string) ([]accounts.MembershipChoice, error) {
	if _, _, err := s.AuthenticateSession(ctx, token); err != nil {
		return nil, fault.New(401, "session_invalid", "会话已失效")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	digest := sessionTokenDigest(token)
	if err = setLocalSessionScope(ctx, tx, digest); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,tenant_name,org_id,org_name,role FROM tenant_context_list($1)`, digest)
	if err != nil {
		return nil, contextError(err)
	}
	list := []accounts.MembershipChoice{}
	for rows.Next() {
		var c accounts.MembershipChoice
		if err = rows.Scan(&c.ID, &c.TenantID, &c.TenantName, &c.OrgID, &c.OrgName, &c.Role); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return list, tx.Commit(ctx)
}
func (s *Store) SwitchSessionContext(ctx context.Context, token, expected, membership string) error {
	if !ValidContextVersion(expected) || !canonicalUUID(membership) {
		return fault.Invalid("上下文版本或成员标识无效")
	}
	user, _, err := s.AuthenticateSession(ctx, token)
	if err != nil {
		return fault.New(401, "session_invalid", "会话已失效")
	}
	proof, err := s.SessionContext(ctx, token)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(user)); err != nil {
		return err
	}
	version, _ := strconv.ParseInt(expected, 10, 64)
	_, err = tx.Exec(ctx, `SELECT tenant_context_switch($1,$2,$3,$4::uuid)`, sessionTokenDigest(token), version, proof.AuthorityRevision, membership)
	if err != nil {
		return contextError(err)
	}
	return contextError(tx.Commit(ctx))
}

type commandProofKey struct{}
type commandProof struct {
	digest, version, revision, support string
	supportRead                        bool
	operation                          []byte
}

// BindCommandContext is only called after exact-session HTTP authentication and
// CSRF validation. Neither digest nor revision is accepted from browser JSON.
func BindCommandContext(ctx context.Context, token, version, revision string) context.Context {
	return context.WithValue(ctx, commandProofKey{}, commandProof{digest: sessionTokenDigest(token), version: version, revision: revision})
}

type commandUserKey struct{}

func commandContext(ctx context.Context, user accounts.User) context.Context {
	ctx = context.WithValue(ctx, commandUserKey{}, user)
	if existing, ok := ctx.Value(commandProofKey{}).(commandProof); ok && (existing.digest != "" || len(existing.operation) > 0) {
		return ctx
	}
	if p, ok := user.CommandAuthority(); ok {
		return context.WithValue(ctx, commandProofKey{}, commandProof{digest: p.SessionDigest, version: p.ContextVersion, revision: p.Revision, support: p.SupportID, operation: append([]byte(nil), p.OperationScope...)})
	}
	if user.HasResolvedMembership() {
		if _, delegated := model.DelegatedScopeFromContext(ctx); !delegated && operationTicket(ctx) == "" {
			return context.WithValue(ctx, commandProofKey{}, commandProof{})
		}
	}
	return ctx
}
func BindCommandUser(user accounts.User, token, version, revision string) accounts.User {
	return accounts.WithCommandAuthority(user, accounts.CommandAuthority{SessionDigest: sessionTokenDigest(token), ContextVersion: version, Revision: revision})
}
func contextProof(ctx context.Context) (commandProof, bool) {
	p, ok := ctx.Value(commandProofKey{}).(commandProof)
	return p, ok && (strings.HasPrefix(p.digest, "sha256:") || len(p.operation) > 0)
}

func canonicalUUID(raw string) bool {
	if len(raw) != 36 {
		return false
	}
	for i, ch := range raw {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
		} else if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}
