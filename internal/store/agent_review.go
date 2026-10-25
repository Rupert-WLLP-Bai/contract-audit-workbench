package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// AgentReviewRecord is one terminal agent-flow projection persisted for a live
// claim. It is Execution-owned: the durable worker writes it before the terminal
// transition and every read verifies its hash. A terminal attempt never accepts
// a new record, and the review hash joins the canonical attempt output hash.
type AgentReviewRecord struct {
	TenantID           string
	OrgID              string
	CaseID             string
	RunID              string
	AttemptID          string
	Generation         int64
	AttemptInputSHA256 string
	AgentReviewSHA256  string
	Review             []byte
}

func (record AgentReviewRecord) validate() error {
	if record.TenantID == "" || record.OrgID == "" || record.CaseID == "" || record.RunID == "" || record.AttemptID == "" {
		return errors.New("agent review record identity is incomplete")
	}
	if record.Generation <= 0 {
		return errors.New("agent review record generation is invalid")
	}
	if !isSHA256Hex(record.AttemptInputSHA256) || !isSHA256Hex(record.AgentReviewSHA256) {
		return errors.New("agent review record digest is invalid")
	}
	if len(record.Review) == 0 || !json.Valid(record.Review) {
		return errors.New("agent review record payload is not valid JSON")
	}
	var projection map[string]json.RawMessage
	if err := json.Unmarshal(record.Review, &projection); err != nil || projection == nil {
		return errors.New("agent review record payload is not a JSON object")
	}
	return nil
}

// SyncAgentReviewRecord persists one agent-review projection for a live claim in
// a single transaction. It takes the case and attempt advisory locks once, in the
// same order as every other attempt writer, so it can never deadlock against the
// terminal finalizer. A newer active generation replaces an unfinalized older
// row, the same generation is idempotent only for byte-identical JSON, and a
// terminal attempt refuses the write. The attempt's frozen input hash must match
// exactly, so a projection can never be attached to different frozen input.
func (s *Store) SyncAgentReviewRecord(ctx context.Context, claim JobClaim, record AgentReviewRecord) error {
	if ctx == nil {
		return errors.New("agent review record context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.validate(); err != nil {
		return err
	}
	if claim.JobID == "" || claim.Generation <= 0 {
		return errors.New("agent review record requires a durable claim")
	}
	if record.TenantID != claim.TenantID || record.OrgID != claim.OrgID || record.CaseID != claim.CaseID ||
		record.RunID != claim.RunID || record.AttemptID != claim.AttemptID || record.Generation != claim.Generation {
		return errors.New("agent review record identity does not match its claim")
	}
	if sha256Bytes(record.Review) != record.AgentReviewSHA256 {
		return errors.New("agent review record hash does not match its payload")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, claim.scope()); err != nil {
		return err
	}
	// Same lock order as finishAttemptNarrow: case key, then attempt key.
	if err := scopedCaseLock(ctx, tx, claim.CaseID); err != nil {
		return err
	}
	if err := scopedAttemptLock(ctx, tx, claim.AttemptID); err != nil {
		return err
	}
	var currentState string
	if err := tx.QueryRow(ctx, `SELECT state FROM review_attempts WHERE id=$1 AND run_id=$2 FOR UPDATE`,
		claim.AttemptID, claim.RunID).Scan(&currentState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errAttemptMissing
		}
		return err
	}
	if !attemptActive(currentState) {
		return fault.Conflict("终态尝试不能再写入智能审查记录")
	}
	if err := s.requireLiveClaim(ctx, tx, claim, claim.RunID, claim.AttemptID); err != nil {
		return err
	}
	var inputHash string
	if err := tx.QueryRow(ctx, `SELECT input_hash FROM review_attempt_inputs WHERE attempt_id=$1 AND run_id=$2`,
		claim.AttemptID, claim.RunID).Scan(&inputHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.Conflict("尝试缺少冻结输入身份，不能写入智能审查记录")
		}
		return err
	}
	if inputHash != record.AttemptInputSHA256 {
		return fault.Conflict("智能审查记录的冻结输入身份与尝试不一致")
	}

	var existingGeneration int64
	var existingDigest string
	var existingPayload []byte
	err = tx.QueryRow(ctx, `SELECT generation, agent_review_sha256, agent_review FROM execution_agent_reviews
		WHERE tenant_id=$1 AND org_id=$2 AND attempt_id=$3`,
		record.TenantID, record.OrgID, record.AttemptID).Scan(&existingGeneration, &existingDigest, &existingPayload)
	switch {
	case err == nil:
		if existingGeneration == record.Generation {
			if existingDigest != record.AgentReviewSHA256 || !bytesEqual(existingPayload, record.Review) {
				return fault.Conflict("同一代的智能审查记录已存在且内容不同")
			}
			return tx.Commit(ctx)
		}
		if existingGeneration > record.Generation {
			return fault.Conflict("已有更新的智能审查记录代次")
		}
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO execution_agent_reviews
		(tenant_id, org_id, case_id, run_id, attempt_id, generation, attempt_input_sha256, agent_review_sha256, agent_review)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (tenant_id, org_id, attempt_id) DO UPDATE SET generation=EXCLUDED.generation,
			attempt_input_sha256=EXCLUDED.attempt_input_sha256, agent_review_sha256=EXCLUDED.agent_review_sha256,
			agent_review=EXCLUDED.agent_review`,
		record.TenantID, record.OrgID, record.CaseID, record.RunID, record.AttemptID, record.Generation,
		record.AttemptInputSHA256, record.AgentReviewSHA256, record.Review)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict("智能审查记录代次冲突，请重试")
	}
	return tx.Commit(ctx)
}

// AgentReviewForAttempt returns the persisted projection of one attempt for an
// authorized case reader. It is null when the attempt has no agent-flow record,
// and an error when a stored row fails hash verification, so a corrupt row is
// never projected. It first checks the caller's case grant, then reads using the
// authorized case's owner organization under the tenant RLS policy.
func (s *Store) AgentReviewForAttempt(ctx context.Context, user accounts.User, caseID, runID, attemptID string) (json.RawMessage, bool, error) {
	if ctx == nil {
		return nil, false, errors.New("agent review context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return nil, false, err
	}
	st, err := s.snapshotScoped(ctx, dbScope{
		ActorID: current.ID, TenantID: accounts.Tenant(current), OrgID: current.OrgID,
		CaseID: caseID, Role: "api",
	})
	if err != nil {
		return nil, false, err
	}
	caseItem, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return nil, false, err
	}
	if caseItem.TenantID != accounts.Tenant(current) {
		return nil, false, errors.New("agent review case tenant does not match the trusted user")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{
		ActorID: current.ID, TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID,
		CaseID: caseItem.ID, Role: "api",
	}); err != nil {
		return nil, false, err
	}
	var digest string
	var payload []byte
	err = tx.QueryRow(ctx, `SELECT review.agent_review_sha256, review.agent_review
		FROM execution_agent_reviews AS review
		JOIN cases AS c ON c.tenant_id=review.tenant_id AND c.org_id=review.org_id AND c.id=review.case_id
		JOIN review_runs AS run ON run.tenant_id=c.tenant_id AND run.org_id=c.org_id AND run.case_id=c.id AND run.id=review.run_id
		JOIN review_attempts AS attempt ON attempt.tenant_id=run.tenant_id AND attempt.org_id=run.org_id AND attempt.run_id=run.id AND attempt.id=review.attempt_id
		WHERE c.id=$1 AND c.tenant_id=$4 AND c.org_id=$5 AND run.id=$2 AND attempt.id=$3`,
		caseItem.ID, runID, attemptID, caseItem.TenantID, caseItem.OwnerOrgID).Scan(&digest, &payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if sha256Bytes(payload) != digest {
		return nil, false, fmt.Errorf("agent review %q payload digest mismatch", attemptID)
	}
	return append(json.RawMessage(nil), payload...), true, nil
}

// agentReviewDigestForOutput returns the verified hash of the attempt's persisted
// projection, or empty when the attempt has none. It runs inside the caller's
// transaction so the terminal output hash sees exactly the committed row.
func agentReviewDigestForOutput(ctx context.Context, tx pgx.Tx, tenantID, orgID, attemptID string) (string, error) {
	var digest string
	var payload []byte
	err := tx.QueryRow(ctx, `SELECT agent_review_sha256, agent_review FROM execution_agent_reviews
		WHERE tenant_id=$1 AND org_id=$2 AND attempt_id=$3`, tenantID, orgID, attemptID).Scan(&digest, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if sha256Bytes(payload) != digest {
		return "", fmt.Errorf("agent review %q payload digest mismatch", attemptID)
	}
	return digest, nil
}

func bytesEqual(left, right []byte) bool {
	return string(left) == string(right)
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

// ErrFrozenPlannerAbsent means a legacy attempt has no planner identity or
// payload. Such attempts may continue through the historical deterministic
// baseline, but they do not acquire a new agent-planner stage.
var ErrFrozenPlannerAbsent = errors.New("frozen planner configuration is absent")

// ErrFrozenPlannerMismatch means a run's frozen planner payload does not
// reconstruct to the frozen configuration identity. A worker must fail closed on
// it rather than fall back to current process configuration.
var ErrFrozenPlannerMismatch = errors.New("frozen planner configuration does not match its identity")

// FrozenPlannerConfiguration reconstructs the accepted planner configuration
// from the immutable attempt payload. The worker must use the returned limits and
// prompt/tool identities instead of re-reading process environment, so an
// already accepted run keeps its approved behaviour. A payload that does not
// reconstruct to the frozen identity returns ErrFrozenPlannerMismatch; a legacy
// attempt with neither field returns ErrFrozenPlannerAbsent.
func FrozenPlannerConfiguration(attempt Attempt) (agentflow.PlannerConfiguration, error) {
	if attempt.PlannerConfigurationID == "" && len(attempt.PlannerConfiguration) == 0 {
		return agentflow.PlannerConfiguration{}, ErrFrozenPlannerAbsent
	}
	if attempt.PlannerConfigurationID == "" || len(attempt.PlannerConfiguration) == 0 {
		return agentflow.PlannerConfiguration{}, ErrFrozenPlannerMismatch
	}
	var configuration agentflow.PlannerConfiguration
	if err := json.Unmarshal(attempt.PlannerConfiguration, &configuration); err != nil {
		return agentflow.PlannerConfiguration{}, fmt.Errorf("%w: %v", ErrFrozenPlannerMismatch, err)
	}
	if configuration.ConfigurationID == "" || configuration.ConfigurationID != attempt.PlannerConfigurationID {
		return agentflow.PlannerConfiguration{}, ErrFrozenPlannerMismatch
	}
	if err := configuration.Validate(); err != nil {
		return agentflow.PlannerConfiguration{}, fmt.Errorf("%w: %v", ErrFrozenPlannerMismatch, err)
	}
	return configuration, nil
}
