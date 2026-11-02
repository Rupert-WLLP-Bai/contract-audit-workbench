package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

type SupportGrant struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	OrgID       string    `json:"org_id"`
	CaseID      string    `json:"case_id"`
	PrincipalID string    `json:"principal_id"`
	Actions     []string  `json:"actions"`
	Reason      string    `json:"reason"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedBy   string    `json:"created_by"`
	ApprovedBy  *string   `json:"approved_by"`
	Status      string    `json:"status"`
	Revision    string    `json:"revision"`
}
type SupportGrantRequest struct {
	PrincipalID string    `json:"principal_id"`
	Actions     []string  `json:"actions"`
	Reason      string    `json:"reason"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type SupportCaseView struct {
	GrantID        string `json:"grant_id"`
	CaseID         string `json:"case_id"`
	TenantID       string `json:"tenant_id"`
	OrgID          string `json:"org_id"`
	Title          string `json:"title"`
	ContractType   string `json:"contract_type"`
	BusinessStatus string `json:"business_status"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	ReadOnly       bool   `json:"read_only"`
}

func (s *Store) ListSupportGrants(ctx context.Context, user accounts.User) ([]SupportGrant, error) {
	ctx = commandContext(ctx, user)
	proof, ok := contextProof(ctx)
	if !ok {
		return nil, fault.New(401, "session_invalid", "会话上下文不可用")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = setLocalSessionScope(ctx, tx, proof.digest); err != nil {
		return nil, err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT tenant_support_list($1)`, proof.digest).Scan(&raw); err != nil {
		return nil, contextError(err)
	}
	var result []SupportGrant
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, tx.Commit(ctx)
}
func (s *Store) RequestSupportGrant(ctx context.Context, user accounts.User, caseID string, input SupportGrantRequest) (SupportGrant, error) {
	if !canonicalUUID(input.PrincipalID) || len(input.Actions) != 1 || input.Actions[0] != "case.read" {
		return SupportGrant{}, fault.Invalid("仅支持对精确人员的只读案件支援")
	}
	return s.supportCommand(ctx, user, "request", "", caseID, input.PrincipalID, "", input)
}
func (s *Store) ChangeSupportGrant(ctx context.Context, user accounts.User, id, action, expected string) (SupportGrant, error) {
	if !canonicalUUID(id) || !ValidContextVersion(expected) || (action != "approve" && action != "revoke") {
		return SupportGrant{}, fault.Invalid("支援授权命令无效")
	}
	return s.supportCommand(ctx, user, action, id, "", "", expected, SupportGrantRequest{})
}
func (s *Store) supportCommand(ctx context.Context, user accounts.User, action, id, caseID, principal, expected string, input SupportGrantRequest) (SupportGrant, error) {
	ctx = commandContext(ctx, user)
	proof, ok := contextProof(ctx)
	if !ok {
		return SupportGrant{}, fault.New(401, "session_invalid", "会话上下文不可用")
	}
	// Include the exact grant in the first fence, before any domain lock.
	proof.support = id
	ctx = context.WithValue(ctx, commandProofKey{}, proof)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return SupportGrant{}, err
	}
	defer tx.Rollback(ctx)
	var grantID, targetID, expires, revision any
	if id != "" {
		grantID = id
	}
	if principal != "" {
		targetID = principal
	}
	if !input.ExpiresAt.IsZero() {
		expires = input.ExpiresAt
	}
	if expected != "" {
		revision, _ = strconv.ParseInt(expected, 10, 64)
	}
	version, _ := strconv.ParseInt(proof.version, 10, 64)
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT tenant_support_command($1,$2,$3,$4,$5::uuid,$6,$7::uuid,$8::text[],$9,$10::timestamptz,$11::bigint)`, proof.digest, version, proof.revision, action, grantID, caseID, targetID, input.Actions, input.Reason, expires, revision).Scan(&raw)
	if err != nil {
		return SupportGrant{}, contextError(err)
	}
	var result SupportGrant
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (s *Store) ReadSupportCase(ctx context.Context, user accounts.User, id string) (SupportCaseView, error) {
	if !canonicalUUID(id) {
		return SupportCaseView{}, fault.New(403, "access_denied", "支援授权不可用")
	}
	ctx = commandContext(ctx, user)
	proof, ok := contextProof(ctx)
	if !ok {
		return SupportCaseView{}, fault.New(401, "session_invalid", "会话上下文不可用")
	}
	proof.support = id
	proof.supportRead = true
	ctx = context.WithValue(ctx, commandProofKey{}, proof)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return SupportCaseView{}, err
	}
	defer tx.Rollback(ctx)
	version, _ := strconv.ParseInt(proof.version, 10, 64)
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT tenant_support_read($1,$2,$3,$4::uuid)`, proof.digest, version, proof.revision, id).Scan(&raw); err != nil {
		return SupportCaseView{}, contextError(err)
	}
	var result SupportCaseView
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
