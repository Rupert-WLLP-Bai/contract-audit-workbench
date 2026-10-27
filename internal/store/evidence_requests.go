package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/docversion"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// EvidenceRequestSchemaSQL must be migrated after workbench, review and document
// tables and forbid_append_only. Core owns these tables; migration must also
// force tenant RLS and grant core INSERT/UPDATE on the projection and INSERT on
// the two append-only tables.
const EvidenceRequestSchemaSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_run_scope ON review_runs (tenant_id,org_id,case_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_attempt_scope ON review_attempts (tenant_id,org_id,run_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_evaluation_scope ON constraint_evaluations (tenant_id,org_id,case_id,run_id,attempt_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_document_scope ON documents (tenant_id,org_id,case_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_task_scope ON business_workbench_tasks (tenant_id,org_id,case_id,id);
CREATE TABLE IF NOT EXISTS evidence_requests (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, request_id text NOT NULL,
 task_id text NOT NULL, source_run_id text NOT NULL, source_attempt_id text NOT NULL, source_evaluation_id text, source_gap_id text,
 status text NOT NULL CHECK (status IN ('open','received','verified')),
 version integer NOT NULL CHECK (version > 0), record_json jsonb NOT NULL,
 CHECK ((source_evaluation_id IS NULL) <> (source_gap_id IS NULL)),
 PRIMARY KEY (tenant_id,org_id,case_id,request_id),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases (tenant_id,org_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,task_id) REFERENCES business_workbench_tasks (tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,source_run_id) REFERENCES review_runs (tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,source_run_id,source_attempt_id) REFERENCES review_attempts (tenant_id,org_id,run_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,source_run_id,source_attempt_id,source_evaluation_id)
   REFERENCES constraint_evaluations (tenant_id,org_id,case_id,run_id,attempt_id,id)
);
CREATE INDEX IF NOT EXISTS evidence_requests_case ON evidence_requests (tenant_id,org_id,case_id,request_id);
CREATE TABLE IF NOT EXISTS evidence_receipts (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, request_id text NOT NULL, receipt_id text NOT NULL,
 document_id text, record_json jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,request_id,receipt_id),
 FOREIGN KEY (tenant_id,org_id,case_id,request_id) REFERENCES evidence_requests (tenant_id,org_id,case_id,request_id),
 FOREIGN KEY (tenant_id,org_id,case_id,document_id) REFERENCES documents (tenant_id,org_id,case_id,id)
);
CREATE TABLE IF NOT EXISTS evidence_request_events (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, request_id text NOT NULL,
 version integer NOT NULL CHECK (version > 0), event_type text NOT NULL CHECK (event_type IN ('created','received','verified')),
 actor_id text NOT NULL, idempotency_key text NOT NULL, request_sha256 text NOT NULL,
 record_json jsonb NOT NULL, detail_json jsonb NOT NULL, occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,request_id,version),
 UNIQUE (tenant_id,org_id,case_id,actor_id,event_type,idempotency_key),
 FOREIGN KEY (tenant_id,org_id,case_id,request_id) REFERENCES evidence_requests (tenant_id,org_id,case_id,request_id)
);
CREATE OR REPLACE FUNCTION guard_evidence_request_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'evidence request cannot be deleted'; END IF;
 IF (NEW.tenant_id,NEW.org_id,NEW.case_id,NEW.request_id,NEW.task_id,NEW.source_run_id,NEW.source_attempt_id,NEW.source_evaluation_id,NEW.source_gap_id)
   IS DISTINCT FROM (OLD.tenant_id,OLD.org_id,OLD.case_id,OLD.request_id,OLD.task_id,OLD.source_run_id,OLD.source_attempt_id,OLD.source_evaluation_id,OLD.source_gap_id)
   OR NEW.version <> OLD.version + 1 OR OLD.status = 'verified'
   OR NEW.status NOT IN ('received','verified')
   OR (NEW.status = 'verified' AND OLD.status <> 'received')
   OR (NEW.status = 'verified' AND NOT (NEW.record_json ? 'verification'))
   OR (NEW.status = 'received' AND NEW.record_json ? 'verification')
   OR NEW.record_json->>'status' IS DISTINCT FROM NEW.status
   OR NEW.record_json->>'version' IS DISTINCT FROM NEW.version::text
   OR (NEW.record_json - ARRAY['status','version','updated_at','verification'])
      IS DISTINCT FROM (OLD.record_json - ARRAY['status','version','updated_at','verification']) THEN
  RAISE EXCEPTION 'invalid evidence request transition';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS evidence_requests_update_guard ON evidence_requests;
CREATE TRIGGER evidence_requests_update_guard BEFORE UPDATE OR DELETE ON evidence_requests FOR EACH ROW EXECUTE FUNCTION guard_evidence_request_update();
DROP TRIGGER IF EXISTS evidence_receipts_append_only ON evidence_receipts;
CREATE TRIGGER evidence_receipts_append_only BEFORE UPDATE OR DELETE ON evidence_receipts FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS evidence_request_events_append_only ON evidence_request_events;
CREATE TRIGGER evidence_request_events_append_only BEFORE UPDATE OR DELETE ON evidence_request_events FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
`

// EvidenceRequest freezes the identity of a missing check and the linked
// task's assignment at creation. Later task edits do not rewrite this record.
// Verification is an explicit human attestation, not an inferred model result.
type EvidenceRequest struct {
	ID                    string                       `json:"id"`
	CaseID                string                       `json:"case_id"`
	TaskID                string                       `json:"task_id"`
	TaskVersion           int                          `json:"task_version"`
	AssigneeID            string                       `json:"assignee_id"`
	DueAt                 string                       `json:"due_at"`
	SourceRunID           string                       `json:"source_run_id"`
	SourceAttemptID       string                       `json:"source_attempt_id"`
	SourceEvaluation      *ConstraintEval              `json:"source_evaluation,omitempty"`
	SourceGap             *agentflow.GapReport         `json:"source_gap,omitempty"`
	SourceInputHash       string                       `json:"source_input_hash"`
	SourceOutputHash      string                       `json:"source_output_hash"`
	Question              string                       `json:"question"`
	MaterialRole          string                       `json:"material_role,omitempty"`
	VerificationCondition string                       `json:"verification_condition"`
	Status                string                       `json:"status"`
	Version               int                          `json:"version"`
	CreatedBy             string                       `json:"created_by"`
	CreatedAt             time.Time                    `json:"created_at"`
	UpdatedAt             time.Time                    `json:"updated_at"`
	Verification          *EvidenceRequestVerification `json:"verification,omitempty"`
}

type EvidenceRequestInput struct {
	TaskID             string `json:"task_id"`
	TaskVersion        int    `json:"task_version"`
	SourceRunID        string `json:"source_run_id"`
	SourceAttemptID    string `json:"source_attempt_id"`
	SourceEvaluationID string `json:"source_evaluation_id,omitempty"`
	SourceGapID        string `json:"source_gap_id,omitempty"`
	Question           string `json:"question"`
	MaterialRole       string `json:"material_role,omitempty"`
	// A canonical evaluation requires "compatible" or "conflict"; an agent
	// gap requires "resolved". The question carries the explanatory text.
	VerificationCondition string `json:"verification_condition"`
	IdempotencyKey        string `json:"idempotency_key"`
}

type EvidenceReceiptInput struct {
	DocumentID      string `json:"document_id,omitempty"`
	Answer          string `json:"answer,omitempty"`
	ExpectedVersion int    `json:"expected_version"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type EvidenceReceipt struct {
	ID                            string    `json:"id"`
	RequestID                     string    `json:"request_id"`
	CaseID                        string    `json:"case_id"`
	DocumentID                    string    `json:"document_id,omitempty"`
	DocumentDigest                string    `json:"document_digest,omitempty"`
	Answer                        string    `json:"answer,omitempty"`
	SubmittedBy                   string    `json:"submitted_by"`
	CreatedAt                     time.Time `json:"created_at"`
	ReviewWatermarkRunSequence    int64     `json:"review_watermark_run_sequence"`
	ReviewWatermarkAttemptOrdinal int       `json:"review_watermark_attempt_ordinal"`
}

type EvidenceRequestVerifyInput struct {
	RunID           string                  `json:"run_id"`
	AttemptID       string                  `json:"attempt_id"`
	EvaluationID    string                  `json:"evaluation_id,omitempty"`
	GapID           string                  `json:"gap_id,omitempty"`
	ReceiptID       string                  `json:"receipt_id"`
	Evidence        []ExperienceEvidenceRef `json:"evidence"`
	Reason          string                  `json:"reason"`
	ExpectedVersion int                     `json:"expected_version"`
	IdempotencyKey  string                  `json:"idempotency_key"`
}

type EvidenceRequestVerification struct {
	RunID        string                  `json:"run_id"`
	AttemptID    string                  `json:"attempt_id"`
	EvaluationID string                  `json:"evaluation_id,omitempty"`
	Relation     string                  `json:"relation,omitempty"`
	GapID        string                  `json:"gap_id,omitempty"`
	GapStatus    string                  `json:"gap_status,omitempty"`
	InputHash    string                  `json:"input_hash"`
	OutputHash   string                  `json:"output_hash"`
	ReceiptID    string                  `json:"receipt_id"`
	Evidence     []ExperienceEvidenceRef `json:"evidence"`
	Reason       string                  `json:"reason"`
	VerifiedBy   string                  `json:"verified_by"`
	VerifiedAt   time.Time               `json:"verified_at"`
}

type EvidenceRequestEvent struct {
	Type       string           `json:"type"`
	ActorID    string           `json:"actor_id"`
	OccurredAt time.Time        `json:"occurred_at"`
	Request    EvidenceRequest  `json:"request"`
	Receipt    *EvidenceReceipt `json:"receipt,omitempty"`
}

type evidenceEventDetail struct {
	Receipt *EvidenceReceipt `json:"receipt,omitempty"`
}

func evidenceRequestKey(value string) bool {
	return experienceIdentity(value) && len(value) <= 80
}

func evidenceRequestRole(value string) bool {
	switch value {
	case "", "procurement", "award", "draft_contract", "attachment":
		return true
	default:
		return false
	}
}

func evidenceRequestHash(operation string, input any) (string, error) {
	raw, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Input     any    `json:"input"`
	}{Operation: operation, Input: input})
	if err != nil {
		return "", err
	}
	return sha256Bytes(raw), nil
}

func evidenceRequestWritable(s *Store) error {
	if s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能修改补充材料请求")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.Forbidden("当前服务不能修改补充材料请求")
	}
	return nil
}

func evidenceRequestByID(ctx context.Context, tx pgx.Tx, item Case, id string) (EvidenceRequest, error) {
	var raw []byte
	var version int
	var status string
	err := tx.QueryRow(ctx, `SELECT record_json,version,status FROM evidence_requests
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`, item.TenantID, item.OwnerOrgID, item.ID, id).Scan(&raw, &version, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return EvidenceRequest{}, fault.NotFound("没有找到对应补充材料请求")
	}
	if err != nil {
		return EvidenceRequest{}, err
	}
	var request EvidenceRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return EvidenceRequest{}, err
	}
	if request.ID != id || request.CaseID != item.ID || request.Version != version || request.Status != status {
		return EvidenceRequest{}, errors.New("stored evidence request identity is inconsistent")
	}
	return request, nil
}

func evidenceRequestReplay(ctx context.Context, tx pgx.Tx, item Case, actor, eventType, key, hash, requestID string) (EvidenceRequestEvent, bool, error) {
	var raw, detail []byte
	var storedHash, storedID string
	var event EvidenceRequestEvent
	err := tx.QueryRow(ctx, `SELECT request_id,request_sha256,record_json,detail_json,occurred_at FROM evidence_request_events
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND event_type=$5 AND idempotency_key=$6`,
		item.TenantID, item.OwnerOrgID, item.ID, actor, eventType, key).Scan(&storedID, &storedHash, &raw, &detail, &event.OccurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EvidenceRequestEvent{}, false, nil
	}
	if err != nil {
		return EvidenceRequestEvent{}, false, err
	}
	if storedHash != hash || requestID != "" && storedID != requestID {
		return EvidenceRequestEvent{}, false, fault.Conflict("幂等键已用于其他补充材料操作")
	}
	if err := json.Unmarshal(raw, &event.Request); err != nil {
		return EvidenceRequestEvent{}, false, err
	}
	var data evidenceEventDetail
	if err := json.Unmarshal(detail, &data); err != nil {
		return EvidenceRequestEvent{}, false, err
	}
	if event.Request.ID != storedID || event.Request.CaseID != item.ID {
		return EvidenceRequestEvent{}, false, errors.New("evidence request event identity is inconsistent")
	}
	event.Type, event.ActorID, event.Receipt = eventType, actor, data.Receipt
	return event, true, nil
}

func insertEvidenceEvent(ctx context.Context, tx pgx.Tx, item Case, actor, eventType, key, hash string, request EvidenceRequest, detail evidenceEventDetail) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO evidence_request_events
		(tenant_id,org_id,case_id,request_id,version,event_type,actor_id,idempotency_key,request_sha256,record_json,detail_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, item.TenantID, item.OwnerOrgID, item.ID,
		request.ID, request.Version, eventType, actor, key, hash, raw, data)
	return err
}

func evidenceTask(ctx context.Context, tx pgx.Tx, item Case, taskID string) (WorkbenchTask, error) {
	var raw []byte
	var version int
	err := tx.QueryRow(ctx, `SELECT record_json,version FROM business_workbench_tasks
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND id=$4`, item.TenantID, item.OwnerOrgID, item.ID, taskID).Scan(&raw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkbenchTask{}, fault.NotFound("没有找到对应任务")
	}
	if err != nil {
		return WorkbenchTask{}, err
	}
	var task WorkbenchTask
	if err := json.Unmarshal(raw, &task); err != nil {
		return WorkbenchTask{}, err
	}
	if task.ID != taskID || task.CaseID != item.ID || task.Version != version {
		return WorkbenchTask{}, errors.New("stored task identity is inconsistent")
	}
	return task, nil
}

// evidenceAttempt verifies the same frozen input, selected artifacts and output
// hash used for reports, but reads the verified agent-review digest in this
// transaction so authorization and provenance cannot race the mutation.
func (s *Store) evidenceAttempt(ctx context.Context, tx pgx.Tx, item Case, runID, attemptID string) (state, Attempt, AttemptInput, error) {
	claim := JobClaim{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID}
	st, err := s.loadScopedAttemptState(ctx, tx, claim, runID, attemptID)
	if errors.Is(err, errAttemptMissing) || errors.Is(err, errExecutionEventScope) {
		return state{}, Attempt{}, AttemptInput{}, fault.Conflict("没有可核验的同案件审查尝试")
	}
	if err != nil {
		return state{}, Attempt{}, AttemptInput{}, err
	}
	attempt, ok := st.attemptByID(attemptID)
	if !ok || attempt.RunID != runID || attempt.HistoryKind != "frozen" || attempt.FinishedAt == nil ||
		(attempt.State != "completed" && attempt.State != "partial") || attempt.InputHash == nil || *attempt.InputHash == "" ||
		attempt.OutputHash == nil || *attempt.OutputHash == "" {
		return state{}, Attempt{}, AttemptInput{}, fault.Conflict("审查尝试没有冻结的终态输入和输出")
	}
	input, ok := st.attemptInput(attemptID)
	if !ok || input.RunID != runID || input.InputHash != *attempt.InputHash {
		return state{}, Attempt{}, AttemptInput{}, fault.Conflict("冻结审查输入身份不一致")
	}
	snapshot := st.snapshotOf(runID)
	inputHash, err := frozenInputHash(snapshot, input.Documents)
	if err != nil || inputHash != input.InputHash || !frozenInputMatchesSnapshot(snapshot, input.Documents) ||
		!attemptSelectionsMatchFrozen(st.parseSelectionsForAttempt(attemptID), input.Documents) || s.verifyFrozenDocuments(st, input.Documents) != nil {
		return state{}, Attempt{}, AttemptInput{}, fault.Conflict("冻结审查输入或材料校验失败")
	}
	digest, err := agentReviewDigestForOutput(ctx, tx, item.TenantID, item.OwnerOrgID, attemptID)
	if err != nil {
		return state{}, Attempt{}, AttemptInput{}, fault.Conflict("审查输出证据校验失败")
	}
	outputHash, err := attemptOutputHash(st, runID, attemptID, digest)
	if err != nil || outputHash != *attempt.OutputHash {
		return state{}, Attempt{}, AttemptInput{}, fault.Conflict("冻结审查输出摘要不一致")
	}
	if attempt.RulePackCompilerRef != "" || len(attempt.RulePackSnapshot) != 0 || attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" {
		fullCase, ok := st.caseByID(item.ID)
		if !ok {
			return state{}, Attempt{}, AttemptInput{}, fault.Conflict("审查案件身份不完整")
		}
		if _, err := validateRemediationRulePack(attempt, fullCase); err != nil {
			return state{}, Attempt{}, AttemptInput{}, fault.Conflict("冻结规则包身份无效")
		}
	} else if attempt.RulePackID != "" || attempt.RulePackVersion != "" || attempt.RulePackPointerRevision != 0 {
		return state{}, Attempt{}, AttemptInput{}, fault.Conflict("冻结规则包身份不完整")
	}
	return st, attempt, input, nil
}

func evidenceEvaluation(st state, caseID, runID, attemptID, id string) (ConstraintEval, bool) {
	var result ConstraintEval
	count := 0
	for _, evaluation := range st.evaluationsForAttempt(runID, attemptID) {
		if evaluation.ID == id && evaluation.CaseID == caseID {
			result, count = evaluation, count+1
		}
	}
	// Empty optional slices disappear when the immutable JSON snapshot is
	// persisted, so normalize them before returning the first response too.
	result.FactIDs = emptyStringsToNil(result.FactIDs)
	result.ObjectRefs = emptyStringsToNil(result.ObjectRefs)
	if len(result.Evidence) == 0 {
		result.Evidence = nil
	}
	return result, count == 1
}

// Missing evidence is an observed insufficient/unknown result, not the absence
// of a row or a caller-supplied name for a check.
func evidenceMissingEvaluation(evaluation ConstraintEval) bool {
	return evaluation.ID != "" && evaluation.Domain != "" && evaluation.RuleVersion != "" &&
		(evaluation.Relation == "insufficient" || evaluation.Relation == "unknown")
}

func sameEvidenceCheck(before, after ConstraintEval) bool {
	if before.Domain == "" || before.RuleVersion == "" || before.Domain != after.Domain ||
		before.RuleVersion != after.RuleVersion || before.RuleID != after.RuleID || before.CapabilityID != after.CapabilityID {
		return false
	}
	// Some missing checks have no object identity until evidence is supplied.
	// In that case the later check must still be unique and the authorized
	// reviewer must explicitly attest the condition using the located receipt.
	return len(before.ObjectRefs) == 0 || sameStringSet(before.ObjectRefs, after.ObjectRefs)
}

func matchingEvidenceEvaluation(st state, source ConstraintEval, caseID, runID, attemptID, evaluationID string) (ConstraintEval, bool) {
	var result ConstraintEval
	count := 0
	for _, evaluation := range st.evaluationsForAttempt(runID, attemptID) {
		if evaluation.CaseID == caseID && sameEvidenceCheck(source, evaluation) {
			result, count = evaluation, count+1
		}
	}
	return result, count == 1 && result.ID == evaluationID
}

// The persisted projection is included in the already-checked attempt output
// hash. Check its own input binding and exact bytes before using a gap.
func evidenceAgentReview(ctx context.Context, tx pgx.Tx, item Case, runID, attemptID, inputHash string) (agentflow.AgentReview, error) {
	var raw []byte
	var digest, storedInput string
	err := tx.QueryRow(ctx, `SELECT agent_review,agent_review_sha256,attempt_input_sha256 FROM execution_agent_reviews
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND run_id=$4 AND attempt_id=$5`,
		item.TenantID, item.OwnerOrgID, item.ID, runID, attemptID).Scan(&raw, &digest, &storedInput)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentflow.AgentReview{}, fault.Conflict("审查尝试没有实际的智能审查缺失检查")
	}
	if err != nil {
		return agentflow.AgentReview{}, err
	}
	if storedInput != inputHash || sha256Bytes(raw) != digest {
		return agentflow.AgentReview{}, fault.Conflict("智能审查缺失检查的冻结身份无效")
	}
	var review agentflow.AgentReview
	if err := json.Unmarshal(raw, &review); err != nil || review.SchemaVersion != agentflow.AgentReviewSchemaVersion || review.AttemptID != attemptID {
		return agentflow.AgentReview{}, fault.Conflict("智能审查缺失检查的格式或身份无效")
	}
	return review, nil
}

func evidenceGap(review agentflow.AgentReview, id string) (agentflow.GapReport, bool) {
	var result agentflow.GapReport
	count := 0
	for _, gap := range review.Gaps {
		if gap.GapID == id {
			result, count = gap, count+1
		}
	}
	return result, count == 1 && result.Domain != "" && result.Capability != ""
}

func evidenceMissingGap(gap agentflow.GapReport) bool {
	return gap.GapID != "" && gap.Domain != "" && gap.Capability != "" &&
		(gap.Status == "insufficient" || gap.Status == "unknown" || gap.Status == "unsupported")
}

func sameEvidenceGap(before, after agentflow.GapReport) bool {
	return before.GapID != "" && before.GapID == after.GapID && before.Domain != "" &&
		before.Domain == after.Domain && before.Capability != "" && before.Capability == after.Capability
}

func evidenceResolvedGap(review agentflow.AgentReview, gap agentflow.GapReport) bool {
	if gap.Status != "resolved" {
		return false
	}
	for _, id := range review.UnresolvedGapIDs {
		if id == gap.GapID {
			return false
		}
	}
	count := 0
	valid := false
	for _, domain := range review.Domains {
		if domain.Domain != gap.Domain {
			continue
		}
		for _, capability := range domain.Capabilities {
			if capability.CapabilityID != gap.Capability {
				continue
			}
			count++
			valid = capability.Status == "completed" && capability.Relation != nil &&
				(*capability.Relation == "compatible" || *capability.Relation == "conflict")
		}
	}
	return count == 1 && valid
}

// CreateEvidenceRequest binds an existing task and an actually missing frozen
// check. The request's deadline and assignee are copied from that task.
func (s *Store) CreateEvidenceRequest(ctx context.Context, user accounts.User, caseID string, input EvidenceRequestInput) (EvidenceRequest, error) {
	if err := evidenceRequestWritable(s); err != nil {
		return EvidenceRequest{}, err
	}
	input.Question, input.VerificationCondition = strings.TrimSpace(input.Question), strings.TrimSpace(input.VerificationCondition)
	input.MaterialRole = strings.TrimSpace(input.MaterialRole)
	if !experienceIdentity(input.TaskID) || input.TaskVersion < 1 || !experienceIdentity(input.SourceRunID) ||
		!experienceIdentity(input.SourceAttemptID) || (input.SourceEvaluationID == "") == (input.SourceGapID == "") ||
		input.SourceEvaluationID != "" && !experienceIdentity(input.SourceEvaluationID) || input.SourceGapID != "" && !experienceIdentity(input.SourceGapID) ||
		!validBusinessText(input.Question, 2000) ||
		(input.SourceEvaluationID != "" && input.VerificationCondition != "compatible" && input.VerificationCondition != "conflict") ||
		(input.SourceGapID != "" && input.VerificationCondition != "resolved") ||
		!evidenceRequestRole(input.MaterialRole) || !evidenceRequestKey(input.IdempotencyKey) {
		return EvidenceRequest{}, fault.Invalid("补充材料请求缺少有效任务、缺失检查、问题、核验条件或幂等键")
	}
	hash, err := evidenceRequestHash("created", input)
	if err != nil {
		return EvidenceRequest{}, err
	}
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Manage)
	if err != nil {
		return EvidenceRequest{}, err
	}
	defer tx.Rollback(context.Background())
	if err := businessWritable(item); err != nil {
		return EvidenceRequest{}, err
	}
	prior, found, err := evidenceRequestReplay(ctx, tx, item, current.ID, "created", input.IdempotencyKey, hash, "")
	if err != nil {
		return EvidenceRequest{}, err
	}
	if found {
		if err := tx.Commit(ctx); err != nil {
			return EvidenceRequest{}, err
		}
		return prior.Request, nil
	}
	task, err := evidenceTask(ctx, tx, item, input.TaskID)
	if err != nil {
		return EvidenceRequest{}, err
	}
	if task.Version != input.TaskVersion || task.Status == "completed" {
		return EvidenceRequest{}, fault.Conflict("任务状态或版本已变化")
	}
	if task.DueAt == nil || !experienceIdentity(task.AssigneeID) || !enabledTenantUser(task.AssigneeID, item.TenantID) || !isCaseMember(item, task.AssigneeID) {
		return EvidenceRequest{}, fault.Invalid("任务必须有有效案件成员责任人和期限")
	}
	if _, err := time.Parse(time.RFC3339Nano, *task.DueAt); err != nil {
		return EvidenceRequest{}, fault.Invalid("任务期限无效")
	}
	st, attempt, _, err := s.evidenceAttempt(ctx, tx, item, input.SourceRunID, input.SourceAttemptID)
	if err != nil {
		return EvidenceRequest{}, err
	}
	var evaluation ConstraintEval
	var sourceEvaluation *ConstraintEval
	var gap *agentflow.GapReport
	var evaluationID, gapID *string
	if input.SourceEvaluationID != "" {
		var ok bool
		evaluation, ok = evidenceEvaluation(st, item.ID, input.SourceRunID, input.SourceAttemptID, input.SourceEvaluationID)
		if !ok || !evidenceMissingEvaluation(evaluation) {
			return EvidenceRequest{}, fault.Invalid("来源检查不是该尝试中缺少证据的检查")
		}
		if task.FindingID != nil && *task.FindingID != evaluation.FindingID {
			return EvidenceRequest{}, fault.Invalid("任务关联的风险与缺失检查不一致")
		}
		sourceEvaluation, evaluationID = &evaluation, &input.SourceEvaluationID
	} else {
		review, err := evidenceAgentReview(ctx, tx, item, input.SourceRunID, input.SourceAttemptID, *attempt.InputHash)
		if err != nil {
			return EvidenceRequest{}, err
		}
		observed, ok := evidenceGap(review, input.SourceGapID)
		if !ok || !evidenceMissingGap(observed) {
			return EvidenceRequest{}, fault.Invalid("来源智能审查缺口不是未解决的实际检查")
		}
		if task.FindingID != nil {
			matched := false
			for _, finding := range st.findingsForAttempt(input.SourceRunID, input.SourceAttemptID) {
				if finding.ID == *task.FindingID && finding.Domain == string(observed.Domain) && finding.CapabilityID == observed.Capability {
					matched = true
				}
			}
			if !matched {
				return EvidenceRequest{}, fault.Invalid("任务关联的风险与缺失检查不一致")
			}
		}
		gap, gapID = &observed, &input.SourceGapID
	}
	id, err := newID("ereq_")
	if err != nil {
		return EvidenceRequest{}, err
	}
	now := time.Now().UTC()
	request := EvidenceRequest{ID: id, CaseID: item.ID, TaskID: task.ID, TaskVersion: task.Version,
		AssigneeID: task.AssigneeID, DueAt: *task.DueAt, SourceRunID: input.SourceRunID, SourceAttemptID: input.SourceAttemptID,
		SourceEvaluation: sourceEvaluation, SourceGap: gap, SourceInputHash: *attempt.InputHash, SourceOutputHash: *attempt.OutputHash,
		Question: input.Question, MaterialRole: input.MaterialRole, VerificationCondition: input.VerificationCondition,
		Status: "open", Version: 1, CreatedBy: current.ID, CreatedAt: now, UpdatedAt: now}
	raw, err := json.Marshal(request)
	if err != nil {
		return EvidenceRequest{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO evidence_requests
		(tenant_id,org_id,case_id,request_id,task_id,source_run_id,source_attempt_id,source_evaluation_id,source_gap_id,status,version,record_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, item.TenantID, item.OwnerOrgID, item.ID, id,
		task.ID, input.SourceRunID, input.SourceAttemptID, evaluationID, gapID, request.Status, request.Version, raw)
	if err != nil {
		return EvidenceRequest{}, err
	}
	if err := insertEvidenceEvent(ctx, tx, item, current.ID, "created", input.IdempotencyKey, hash, request, evidenceEventDetail{}); err != nil {
		return EvidenceRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EvidenceRequest{}, err
	}
	return request, nil
}

func (s *Store) GetEvidenceRequest(ctx context.Context, user accounts.User, caseID, requestID string) (EvidenceRequest, error) {
	if !experienceIdentity(requestID) {
		return EvidenceRequest{}, fault.NotFound("没有找到对应补充材料请求")
	}
	tx, _, item, err := s.beginExperienceCase(ctx, user, caseID, access.Read)
	if err != nil {
		return EvidenceRequest{}, err
	}
	defer tx.Rollback(context.Background())
	request, err := evidenceRequestByID(ctx, tx, item, requestID)
	if err != nil {
		return EvidenceRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EvidenceRequest{}, err
	}
	return request, nil
}

func (s *Store) ListEvidenceRequests(ctx context.Context, user accounts.User, caseID string) ([]EvidenceRequest, error) {
	tx, _, item, err := s.beginExperienceCase(ctx, user, caseID, access.Read)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT request_id,version,status,record_json FROM evidence_requests
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 ORDER BY request_id`, item.TenantID, item.OwnerOrgID, item.ID)
	if err != nil {
		return nil, err
	}
	out := []EvidenceRequest{}
	for rows.Next() {
		var id, status string
		var version int
		var raw []byte
		if err := rows.Scan(&id, &version, &status, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var request EvidenceRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			rows.Close()
			return nil, err
		}
		if request.ID != id || request.CaseID != item.ID || request.Version != version || request.Status != status {
			rows.Close()
			return nil, errors.New("stored evidence request identity is inconsistent")
		}
		out = append(out, request)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func updateEvidenceRequest(ctx context.Context, tx pgx.Tx, item Case, previous int, request EvidenceRequest) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE evidence_requests SET version=$1,status=$2,record_json=$3
		WHERE tenant_id=$4 AND org_id=$5 AND case_id=$6 AND request_id=$7 AND version=$8`,
		request.Version, request.Status, raw, item.TenantID, item.OwnerOrgID, item.ID, request.ID, previous)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict("补充材料请求版本已变化")
	}
	return nil
}

// SubmitEvidenceReceipt records a pre-existing uploaded document or an answer.
// Neither makes the request verified. The current task assignee or a case
// manager may submit, subject to current case review access.
func (s *Store) SubmitEvidenceReceipt(ctx context.Context, user accounts.User, caseID, requestID string, input EvidenceReceiptInput) (EvidenceRequest, EvidenceReceipt, error) {
	if err := evidenceRequestWritable(s); err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	input.Answer = strings.TrimSpace(input.Answer)
	if !experienceIdentity(requestID) || input.ExpectedVersion < 1 || !evidenceRequestKey(input.IdempotencyKey) ||
		(input.DocumentID == "") == (input.Answer == "") || input.DocumentID != "" && !experienceIdentity(input.DocumentID) ||
		input.Answer != "" && !validBusinessText(input.Answer, 4000) {
		return EvidenceRequest{}, EvidenceReceipt{}, fault.Invalid("提交需要一个材料或答复、预期版本和幂等键")
	}
	hash, err := evidenceRequestHash("received", input)
	if err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Review)
	if err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	defer tx.Rollback(context.Background())
	if err := businessWritable(item); err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	request, err := evidenceRequestByID(ctx, tx, item, requestID)
	if err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	task, err := evidenceTask(ctx, tx, item, request.TaskID)
	if err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	if current.ID != task.AssigneeID && !authorizeCase(item, current, access.Manage) {
		return EvidenceRequest{}, EvidenceReceipt{}, fault.Forbidden("只有当前任务责任人或案件管理人可以提交")
	}
	prior, found, err := evidenceRequestReplay(ctx, tx, item, current.ID, "received", input.IdempotencyKey, hash, requestID)
	if err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	if found {
		if prior.Receipt == nil {
			return EvidenceRequest{}, EvidenceReceipt{}, errors.New("receipt event has no receipt")
		}
		if err := tx.Commit(ctx); err != nil {
			return EvidenceRequest{}, EvidenceReceipt{}, err
		}
		return prior.Request, *prior.Receipt, nil
	}
	if request.Version != input.ExpectedVersion || request.Status == "verified" {
		return EvidenceRequest{}, EvidenceReceipt{}, fault.Conflict("补充材料请求状态或版本已变化")
	}
	var digest string
	if input.DocumentID != "" {
		var role string
		err := tx.QueryRow(ctx, `SELECT role,digest FROM documents WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND id=$4`,
			item.TenantID, item.OwnerOrgID, item.ID, input.DocumentID).Scan(&role, &digest)
		if errors.Is(err, pgx.ErrNoRows) {
			return EvidenceRequest{}, EvidenceReceipt{}, fault.Invalid("提交材料不属于当前案件")
		}
		if err != nil {
			return EvidenceRequest{}, EvidenceReceipt{}, err
		}
		if request.MaterialRole != "" && role != request.MaterialRole {
			return EvidenceRequest{}, EvidenceReceipt{}, fault.Invalid("提交材料角色与请求不一致")
		}
		if _, err := docversion.ParseDigest(digest); err != nil {
			return EvidenceRequest{}, EvidenceReceipt{}, fault.Conflict("提交材料缺少有效原件摘要")
		}
	}
	id, err := newID("ereceipt_")
	if err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	// Case-locked review acceptance and receipt insertion share this sequence
	// fence. It proves the later attempt was accepted after this receipt even
	// when application and database clocks disagree or have coarse precision.
	var runSequence int64
	var attemptOrdinal int
	err = tx.QueryRow(ctx, `SELECT r.seq,COALESCE(MAX(a.ordinal),0) FROM review_runs r
		LEFT JOIN review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 GROUP BY r.seq ORDER BY r.seq DESC LIMIT 1`,
		item.TenantID, item.OwnerOrgID, item.ID).Scan(&runSequence, &attemptOrdinal)
	if err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&createdAt); err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	receipt := EvidenceReceipt{ID: id, RequestID: request.ID, CaseID: item.ID, DocumentID: input.DocumentID,
		DocumentDigest: digest, Answer: input.Answer, SubmittedBy: current.ID, CreatedAt: createdAt.UTC(),
		ReviewWatermarkRunSequence: runSequence, ReviewWatermarkAttemptOrdinal: attemptOrdinal}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	var documentID *string
	if input.DocumentID != "" {
		documentID = &input.DocumentID
	}
	_, err = tx.Exec(ctx, `INSERT INTO evidence_receipts (tenant_id,org_id,case_id,request_id,receipt_id,document_id,record_json,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, item.TenantID, item.OwnerOrgID, item.ID, request.ID, id, documentID, raw, receipt.CreatedAt)
	if err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	request.Status, request.Version, request.UpdatedAt = "received", request.Version+1, receipt.CreatedAt
	if err := updateEvidenceRequest(ctx, tx, item, input.ExpectedVersion, request); err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	if err := insertEvidenceEvent(ctx, tx, item, current.ID, "received", input.IdempotencyKey, hash, request, evidenceEventDetail{Receipt: &receipt}); err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EvidenceRequest{}, EvidenceReceipt{}, err
	}
	return request, receipt, nil
}

func evidenceReceiptByID(ctx context.Context, tx pgx.Tx, item Case, requestID, receiptID string) (EvidenceReceipt, error) {
	var raw []byte
	var createdAt time.Time
	err := tx.QueryRow(ctx, `SELECT record_json,created_at FROM evidence_receipts
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4 AND receipt_id=$5`,
		item.TenantID, item.OwnerOrgID, item.ID, requestID, receiptID).Scan(&raw, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EvidenceReceipt{}, fault.NotFound("没有找到对应提交记录")
	}
	if err != nil {
		return EvidenceReceipt{}, err
	}
	var receipt EvidenceReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return EvidenceReceipt{}, err
	}
	if receipt.ID != receiptID || receipt.RequestID != requestID || receipt.CaseID != item.ID || !receipt.CreatedAt.Equal(createdAt) ||
		receipt.ReviewWatermarkRunSequence < 1 || receipt.ReviewWatermarkAttemptOrdinal < 0 {
		return EvidenceReceipt{}, errors.New("stored evidence receipt identity is inconsistent")
	}
	return receipt, nil
}

func receiptMatchesEvidence(request EvidenceRequest, receipt EvidenceReceipt, documents []FrozenDocument, ref ExperienceEvidenceRef) bool {
	for _, document := range documents {
		if document.Document.ID != ref.DocumentID || request.MaterialRole != "" && document.Document.Role != request.MaterialRole {
			continue
		}
		if receipt.DocumentID != "" && document.Document.ID == receipt.DocumentID && document.Document.Digest == receipt.DocumentDigest ||
			receipt.Answer != "" && strings.Contains(ref.Quote, receipt.Answer) {
			return true
		}
	}
	return false
}

func evidenceRefCovers(outer, inner ExperienceEvidenceRef) bool {
	if outer.DocumentID != inner.DocumentID || outer.ParsedDocumentID != inner.ParsedDocumentID ||
		outer.NormalizedDigest != inner.NormalizedDigest || outer.Start > inner.Start || outer.End < inner.End || inner.End <= inner.Start {
		return false
	}
	text := []rune(outer.Quote)
	start, end := inner.Start-outer.Start, inner.End-outer.Start
	return start >= 0 && end <= len(text) && string(text[start:end]) == inner.Quote
}

// A rule result is usable only if its own frozen fact citation or finding
// identifies the submitted material. A quotation merely present elsewhere in
// the attempt is not proof that the selected check used it.
func observedEvaluationReceipt(ctx context.Context, tx pgx.Tx, item Case, st state, input AttemptInput,
	request EvidenceRequest, receipt EvidenceReceipt, evaluation ConstraintEval, supplied []ExperienceEvidenceRef) (bool, error) {
	for _, observation := range evaluation.Evidence {
		linked := observation.FactID == ""
		for _, factID := range evaluation.FactIDs {
			if factID != "" && factID == observation.FactID {
				linked = true
				break
			}
		}
		if !linked || !receiptMatchesEvidence(request, receipt, input.Documents, observation.Ref) {
			continue
		}
		for _, ref := range supplied {
			if !evidenceRefCovers(ref, observation.Ref) {
				continue
			}
			if err := verifyFeedbackEvidence(ctx, tx, item, evaluation.RunID, evaluation.AttemptID, input.Documents, observation.Ref); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	if evaluation.FindingID == "" {
		return false, nil
	}
	for _, finding := range st.findingsForAttempt(evaluation.RunID, evaluation.AttemptID) {
		if finding.ID != evaluation.FindingID || finding.CaseID != item.ID || finding.Origin != "rule" ||
			finding.Domain != evaluation.Domain || finding.Relation != evaluation.Relation {
			continue
		}
		for _, anchor := range finding.Anchors {
			if anchor.Status != "located" || anchor.DocumentID == "" || anchor.ParsedDocumentID == "" || anchor.NormalizedDigest == "" ||
				anchor.Quote == "" || anchor.Start < 0 || anchor.End <= anchor.Start {
				continue
			}
			actual := ExperienceEvidenceRef{DocumentID: anchor.DocumentID, ParsedDocumentID: anchor.ParsedDocumentID,
				NormalizedDigest: anchor.NormalizedDigest, Quote: anchor.Quote, Start: anchor.Start, End: anchor.End}
			if !receiptMatchesEvidence(request, receipt, input.Documents, actual) {
				continue
			}
			for _, ref := range supplied {
				if !evidenceRefCovers(ref, actual) {
					continue
				}
				if err := verifyFeedbackEvidence(ctx, tx, item, evaluation.RunID, evaluation.AttemptID, input.Documents, actual); err != nil {
					return false, err
				}
				return true, nil
			}
		}
	}
	return false, nil
}

// Agent gap evidence IDs include the document identities actually cited by the
// verified check. A typed fact may instead be referenced by its own identity;
// in that case its located anchor must be covered by the supplied evidence.
func observedGapReceipt(request EvidenceRequest, receipt EvidenceReceipt, input AttemptInput, gap agentflow.GapReport,
	review agentflow.AgentReview, supplied []ExperienceEvidenceRef) bool {
	for _, id := range gap.EvidenceIDs {
		for _, ref := range supplied {
			if id == ref.DocumentID && receiptMatchesEvidence(request, receipt, input.Documents, ref) {
				return true
			}
		}
		for _, fact := range review.Evidence {
			anchor := fact.Anchor
			if fact.EvidenceID != id || fact.Verification != "typed_semantic_and_frozen_quote_verified" || anchor.Status != "located" {
				continue
			}
			actual := ExperienceEvidenceRef{DocumentID: anchor.DocumentID, ParsedDocumentID: anchor.ParsedDocumentID,
				NormalizedDigest: anchor.NormalizedDigest, Quote: anchor.Quote, Start: anchor.Start, End: anchor.End}
			if !receiptMatchesEvidence(request, receipt, input.Documents, actual) {
				continue
			}
			for _, ref := range supplied {
				if evidenceRefCovers(ref, actual) {
					return true
				}
			}
		}
	}
	return false
}

// VerifyEvidenceRequest records an authorized attestation only after the later
// frozen review actually produced the requested condition and identified the
// receipt as evidence for that check. Unknown, insufficient, missing or
// unlinked observations remain unresolved.
func (s *Store) VerifyEvidenceRequest(ctx context.Context, user accounts.User, caseID, requestID string, input EvidenceRequestVerifyInput) (EvidenceRequest, error) {
	if err := evidenceRequestWritable(s); err != nil {
		return EvidenceRequest{}, err
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if !experienceIdentity(requestID) || !experienceIdentity(input.RunID) || !experienceIdentity(input.AttemptID) ||
		(input.EvaluationID == "") == (input.GapID == "") || input.EvaluationID != "" && !experienceIdentity(input.EvaluationID) ||
		input.GapID != "" && !experienceIdentity(input.GapID) || !experienceIdentity(input.ReceiptID) || input.ExpectedVersion < 1 ||
		!evidenceRequestKey(input.IdempotencyKey) || !validBusinessText(input.Reason, 2000) || len(input.Evidence) == 0 || len(input.Evidence) > 32 {
		return EvidenceRequest{}, fault.Invalid("核验需要新尝试、实际检查、提交记录、理由和冻结证据")
	}
	for _, ref := range input.Evidence {
		if !experienceIdentity(ref.DocumentID) || !experienceIdentity(ref.ParsedDocumentID) || ref.Quote == "" ||
			runeCount(ref.Quote) > 4096 || ref.Start < 0 || ref.End <= ref.Start {
			return EvidenceRequest{}, fault.Invalid("核验证据定位不完整")
		}
		if ref.NormalizedDigest != "" {
			if _, err := docversion.ParseDigest(ref.NormalizedDigest); err != nil {
				return EvidenceRequest{}, fault.Invalid("核验证据摘要无效")
			}
		}
	}
	hash, err := evidenceRequestHash("verified", input)
	if err != nil {
		return EvidenceRequest{}, err
	}
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Decide)
	if err != nil {
		return EvidenceRequest{}, err
	}
	defer tx.Rollback(context.Background())
	if !accounts.CanDecide(current.Role) {
		return EvidenceRequest{}, fault.Forbidden("当前角色不能核验补充材料")
	}
	if err := businessWritable(item); err != nil {
		return EvidenceRequest{}, err
	}
	prior, found, err := evidenceRequestReplay(ctx, tx, item, current.ID, "verified", input.IdempotencyKey, hash, requestID)
	if err != nil {
		return EvidenceRequest{}, err
	}
	if found {
		if err := tx.Commit(ctx); err != nil {
			return EvidenceRequest{}, err
		}
		return prior.Request, nil
	}
	request, err := evidenceRequestByID(ctx, tx, item, requestID)
	if err != nil {
		return EvidenceRequest{}, err
	}
	if request.Version != input.ExpectedVersion || request.Status != "received" {
		return EvidenceRequest{}, fault.Conflict("补充材料请求状态或版本已变化")
	}
	receipt, err := evidenceReceiptByID(ctx, tx, item, requestID, input.ReceiptID)
	if err != nil {
		return EvidenceRequest{}, err
	}
	if input.AttemptID == request.SourceAttemptID {
		return EvidenceRequest{}, fault.Conflict("必须使用提交之后的新审查尝试")
	}
	sourceState, sourceAttempt, _, err := s.evidenceAttempt(ctx, tx, item, request.SourceRunID, request.SourceAttemptID)
	if err != nil {
		return EvidenceRequest{}, err
	}
	if *sourceAttempt.InputHash != request.SourceInputHash || *sourceAttempt.OutputHash != request.SourceOutputHash {
		return EvidenceRequest{}, fault.Conflict("原始审查冻结身份不一致")
	}
	var source ConstraintEval
	var sourceReview agentflow.AgentReview
	if request.SourceGap == nil {
		if request.SourceEvaluation == nil {
			return EvidenceRequest{}, fault.Conflict("原始缺失检查身份不完整")
		}
		var ok bool
		source, ok = evidenceEvaluation(sourceState, item.ID, request.SourceRunID, request.SourceAttemptID, request.SourceEvaluation.ID)
		if input.EvaluationID == "" || !ok || !evidenceMissingEvaluation(source) {
			return EvidenceRequest{}, fault.Conflict("原始缺失检查不一致")
		}
	} else {
		if input.GapID == "" {
			return EvidenceRequest{}, fault.Conflict("核验必须使用同一智能审查缺口")
		}
		sourceReview, err = evidenceAgentReview(ctx, tx, item, request.SourceRunID, request.SourceAttemptID, *sourceAttempt.InputHash)
		if err != nil {
			return EvidenceRequest{}, err
		}
		observed, ok := evidenceGap(sourceReview, request.SourceGap.GapID)
		if !ok || !evidenceMissingGap(observed) || !sameEvidenceGap(*request.SourceGap, observed) {
			return EvidenceRequest{}, fault.Conflict("原始智能审查缺口不一致")
		}
	}
	laterState, laterAttempt, laterInput, err := s.evidenceAttempt(ctx, tx, item, input.RunID, input.AttemptID)
	if err != nil {
		return EvidenceRequest{}, err
	}
	var laterRunSequence int64
	err = tx.QueryRow(ctx, `SELECT seq FROM review_runs WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND id=$4`,
		item.TenantID, item.OwnerOrgID, item.ID, input.RunID).Scan(&laterRunSequence)
	if err != nil {
		return EvidenceRequest{}, err
	}
	if laterRunSequence < receipt.ReviewWatermarkRunSequence || laterRunSequence == receipt.ReviewWatermarkRunSequence &&
		laterAttempt.Ordinal <= receipt.ReviewWatermarkAttemptOrdinal {
		return EvidenceRequest{}, fault.Conflict("复核尝试必须在材料提交之后受理")
	}
	before, after := sourceState.snapshotOf(request.SourceRunID), laterState.snapshotOf(input.RunID)
	if before.RuleSetHash != after.RuleSetHash || before.PlaybookHash != after.PlaybookHash || before.ComparatorRef != after.ComparatorRef ||
		!sameRunRulePackIdentity(before, after) || sourceAttempt.RulePackCompilerRef != laterAttempt.RulePackCompilerRef {
		return EvidenceRequest{}, fault.Conflict("复核尝试的检查规则不可比较")
	}
	var evaluation ConstraintEval
	var observedGap agentflow.GapReport
	var laterReview agentflow.AgentReview
	if request.SourceGap == nil {
		var ok bool
		evaluation, ok = matchingEvidenceEvaluation(laterState, source, item.ID, input.RunID, input.AttemptID, input.EvaluationID)
		if !ok {
			return EvidenceRequest{}, fault.Conflict("复核尝试没有唯一匹配的实际检查")
		}
		if (request.VerificationCondition != "compatible" && request.VerificationCondition != "conflict") || evaluation.Relation != request.VerificationCondition {
			return EvidenceRequest{}, fault.Conflict("复核检查未达到请求的核验条件")
		}
	} else {
		if !samePlannerConfiguration(before, after) {
			return EvidenceRequest{}, fault.Conflict("复核尝试的智能审查配置不可比较")
		}
		laterReview, err = evidenceAgentReview(ctx, tx, item, input.RunID, input.AttemptID, *laterAttempt.InputHash)
		if err != nil {
			return EvidenceRequest{}, err
		}
		if sourceReview.Planner.RuntimeVersion != laterReview.Planner.RuntimeVersion ||
			sourceReview.Planner.ToolCatalogVersion != laterReview.Planner.ToolCatalogVersion ||
			sourceReview.Planner.SchemaVersion != laterReview.Planner.SchemaVersion {
			return EvidenceRequest{}, fault.Conflict("复核尝试的智能审查能力不可比较")
		}
		var ok bool
		observedGap, ok = evidenceGap(laterReview, input.GapID)
		if !ok || !sameEvidenceGap(*request.SourceGap, observedGap) ||
			request.VerificationCondition != "resolved" || !evidenceResolvedGap(laterReview, observedGap) {
			return EvidenceRequest{}, fault.Conflict("复核尝试没有唯一匹配的实际缺口检查")
		}
	}
	evidence := append([]ExperienceEvidenceRef(nil), input.Evidence...)
	for i := range evidence {
		ref := &evidence[i]
		if ref.NormalizedDigest == "" {
			for _, document := range laterInput.Documents {
				if document.Document.ID == ref.DocumentID && document.ParsedDocumentID == ref.ParsedDocumentID && document.NormalizedHash != nil {
					ref.NormalizedDigest = *document.NormalizedHash
				}
			}
			if ref.NormalizedDigest == "" {
				return EvidenceRequest{}, fault.Invalid("核验证据不属于复核尝试的冻结解析")
			}
		}
		if err := verifyFeedbackEvidence(ctx, tx, item, input.RunID, input.AttemptID, laterInput.Documents, *ref); err != nil {
			return EvidenceRequest{}, err
		}
	}
	matchedReceipt := false
	if request.SourceGap == nil {
		matchedReceipt, err = observedEvaluationReceipt(ctx, tx, item, laterState, laterInput, request, receipt, evaluation, evidence)
		if err != nil {
			return EvidenceRequest{}, err
		}
	} else {
		matchedReceipt = observedGapReceipt(request, receipt, laterInput, observedGap, laterReview, evidence)
	}
	if !matchedReceipt {
		return EvidenceRequest{}, fault.Conflict("复核检查没有使用本次提交的可定位材料或答复")
	}
	now := time.Now().UTC()
	request.Status, request.Version, request.UpdatedAt = "verified", request.Version+1, now
	request.Verification = &EvidenceRequestVerification{RunID: input.RunID, AttemptID: input.AttemptID, EvaluationID: evaluation.ID,
		Relation: evaluation.Relation, GapID: observedGap.GapID, GapStatus: observedGap.Status,
		InputHash: *laterAttempt.InputHash, OutputHash: *laterAttempt.OutputHash,
		ReceiptID: receipt.ID, Evidence: evidence, Reason: input.Reason,
		VerifiedBy: current.ID, VerifiedAt: now}
	if err := updateEvidenceRequest(ctx, tx, item, input.ExpectedVersion, request); err != nil {
		return EvidenceRequest{}, err
	}
	if err := insertEvidenceEvent(ctx, tx, item, current.ID, "verified", input.IdempotencyKey, hash, request, evidenceEventDetail{}); err != nil {
		return EvidenceRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EvidenceRequest{}, err
	}
	return request, nil
}

// ListEvidenceRequestHistory includes the original request and every receipt or
// human verification snapshot; no subsequent review mutates prior history.
func (s *Store) ListEvidenceRequestHistory(ctx context.Context, user accounts.User, caseID, requestID string) ([]EvidenceRequestEvent, error) {
	if !experienceIdentity(requestID) {
		return nil, fault.NotFound("没有找到对应补充材料请求")
	}
	tx, _, item, err := s.beginExperienceCase(ctx, user, caseID, access.Read)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err := evidenceRequestByID(ctx, tx, item, requestID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT version,event_type,actor_id,record_json,detail_json,occurred_at FROM evidence_request_events
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4 ORDER BY version`, item.TenantID, item.OwnerOrgID, item.ID, requestID)
	if err != nil {
		return nil, err
	}
	out := []EvidenceRequestEvent{}
	for rows.Next() {
		var event EvidenceRequestEvent
		var version int
		var raw, detail []byte
		if err := rows.Scan(&version, &event.Type, &event.ActorID, &raw, &detail, &event.OccurredAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(raw, &event.Request); err != nil {
			rows.Close()
			return nil, err
		}
		var data evidenceEventDetail
		if err := json.Unmarshal(detail, &data); err != nil {
			rows.Close()
			return nil, err
		}
		if event.Request.ID != requestID || event.Request.CaseID != item.ID || event.Request.Version != version {
			rows.Close()
			return nil, fmt.Errorf("evidence request history revision %d has inconsistent identity", version)
		}
		event.Receipt = data.Receipt
		out = append(out, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
