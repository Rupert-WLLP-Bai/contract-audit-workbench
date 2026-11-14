package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type ClauseProposals struct{ tx platform.Executor }

func NewClauseProposals(tx platform.Executor) *ClauseProposals { return &ClauseProposals{tx: tx} }

func (r *ClauseProposals) LockReceipt(ctx context.Context, tenant, actor, route, key string) error {
	raw, err := json.Marshal([]string{tenant, actor, route, key})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	_, err = r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clause-receipt:' || $1,0))`, hex.EncodeToString(digest[:]))
	return err
}
func (r *ClauseProposals) LoadReceipt(ctx context.Context, tenant, actor, route, key string) (string, []byte, bool, error) {
	var digest string
	var response []byte
	err := r.tx.QueryRow(ctx, `SELECT request_sha256,response_json FROM clm_clause_proposal_receipts WHERE tenant_id=$1 AND actor_id=$2 AND route_scope=$3 AND idempotency_key=$4`, tenant, actor, route, key).Scan(&digest, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, nil
	}
	return digest, response, err == nil, err
}
func (r *ClauseProposals) SaveReceipt(ctx context.Context, tenant, actor, route, key, digest string, response []byte) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_clause_proposal_receipts(tenant_id,actor_id,route_scope,idempotency_key,request_sha256,response_json) VALUES($1,$2,$3,$4,$5,$6)`, tenant, actor, route, key, digest, response)
	return err
}
func (r *ClauseProposals) LoadProposal(ctx context.Context, tenant, org, caseID, id string, lock bool) (contract.ClauseProposal, error) {
	query := `SELECT proposal_json FROM clm_clause_proposals WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND id=$4`
	// The caller holds the case advisory transaction lock for decision writes.
	_ = lock
	var raw []byte
	if err := r.tx.QueryRow(ctx, query, tenant, org, caseID, id).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contract.ClauseProposal{}, fault.NotFound("没有找到对应记录")
		}
		return contract.ClauseProposal{}, err
	}
	var proposal contract.ClauseProposal
	if err := json.Unmarshal(raw, &proposal); err != nil {
		return contract.ClauseProposal{}, err
	}
	rows, err := r.tx.Query(ctx, `SELECT decision_json FROM clm_clause_proposal_decisions WHERE tenant_id=$1 AND org_id=$2 AND proposal_id=$3 ORDER BY version`, tenant, org, id)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	defer rows.Close()
	proposal.History = []contract.ClauseProposalHistory{}
	for rows.Next() {
		var value []byte
		var history contract.ClauseProposalHistory
		if err := rows.Scan(&value); err != nil {
			return contract.ClauseProposal{}, err
		}
		if err := json.Unmarshal(value, &history); err != nil {
			return contract.ClauseProposal{}, err
		}
		proposal.History = append(proposal.History, history)
		proposal.Version = history.Version
		proposal.Status = history.Action
	}
	if err := rows.Err(); err != nil {
		return contract.ClauseProposal{}, err
	}
	return proposal, nil
}
func (r *ClauseProposals) AppendDecision(ctx context.Context, tenant, org, id string, history contract.ClauseProposalHistory, expected int64) error {
	raw, err := json.Marshal(history)
	if err != nil {
		return err
	}
	tag, err := r.tx.Exec(ctx, `INSERT INTO clm_clause_proposal_decisions(tenant_id,org_id,proposal_id,version,decision_json)
 SELECT $1,$2,$3,$4,$5 WHERE $4=$6+1 AND COALESCE((SELECT max(version) FROM clm_clause_proposal_decisions WHERE tenant_id=$1 AND proposal_id=$3),1)=$6`, tenant, org, id, history.Version, raw, expected)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict("条款建议已被处理或版本已变化")
	}
	return nil
}

func (r *ClauseProposals) Create(ctx context.Context, tenant, org, actor, key, digest string, proposal contract.ClauseProposal) error {
	raw, err := json.Marshal(proposal)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `INSERT INTO clm_clause_proposals(tenant_id,org_id,case_id,id,run_id,attempt_id,actor_id,idempotency_key,request_sha256,proposal_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, tenant, org, proposal.CaseID, proposal.ID, proposal.RunID, proposal.AttemptID, actor, key, digest, raw)
	return err
}
func (r *ClauseProposals) FindCreate(ctx context.Context, tenant, actor, key string) (string, string, bool, error) {
	var id, digest string
	err := r.tx.QueryRow(ctx, `SELECT id,request_sha256 FROM clm_clause_proposals WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, tenant, actor, key).Scan(&id, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	return id, digest, err == nil, err
}
func (r *ClauseProposals) ListIDs(ctx context.Context, tenant, org, caseID string) ([]string, error) {
	rows, err := r.tx.Query(ctx, `SELECT id FROM clm_clause_proposals WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 ORDER BY created_at,id`, tenant, org, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *ClauseProposals) LoadCreated(ctx context.Context, tenant, org, caseID, id string) (contract.ClauseProposal, error) {
	var raw []byte
	err := r.tx.QueryRow(ctx, `SELECT proposal_json FROM clm_clause_proposals WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND id=$4`, tenant, org, caseID, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.ClauseProposal{}, fault.NotFound("没有找到对应记录")
	}
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	var proposal contract.ClauseProposal
	err = json.Unmarshal(raw, &proposal)
	return proposal, err
}
