package store

import (
	"context"
	"encoding/json"
	"errors"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// attemptOutcome is what a narrow terminal or cancellation transition publishes
// after its transaction commits.
type attemptOutcome struct {
	AttemptID     string
	RunID         string
	AttemptState  string
	FinishedAt    *string
	ErrorCode     *string
	OutputHash    *string
	RunState      string
	PartialReason string
	FailureReason string
	RunUpdatedAt  string
	RunJobState   string
	Events        []ExecutionEvent
	RunEvents     []Event
	Audit         *AuditRecord
}

// loadAttemptOutputScope reads only the rows the canonical output hash needs:
// one attempt plus that attempt's findings and evaluations. It never loads the
// whole store, so the hash keeps its exact existing definition without
// reproducing it in SQL.
func loadAttemptOutputScope(ctx context.Context, tx pgx.Tx, runID, attemptID string) (state, error) {
	var scope state
	row := tx.QueryRow(ctx, `SELECT id, run_id, ordinal, retry_of_attempt_id, state, accepted_at, started_at, finished_at, input_hash, output_hash, error_code, history_kind, actor_id, rule_pack_snapshot, rule_pack_hash, rule_pack_snapshot_digest, rule_pack_compiler_ref, rule_pack_id, rule_pack_version, rule_pack_pointer_revision, planner_configuration_id, planner_configuration
		FROM review_attempts WHERE id=$1 AND run_id=$2`, attemptID, runID)
	var attempt Attempt
	var plannerPayload []byte
	if err := row.Scan(&attempt.ID, &attempt.RunID, &attempt.Ordinal, &attempt.RetryOfAttemptID, &attempt.State, &attempt.AcceptedAt, &attempt.StartedAt, &attempt.FinishedAt, &attempt.InputHash, &attempt.OutputHash, &attempt.ErrorCode, &attempt.HistoryKind, &attempt.ActorID, &attempt.RulePackSnapshot, &attempt.RulePackHash, &attempt.RulePackSnapshotDigest, &attempt.RulePackCompilerRef,
		&attempt.RulePackID, &attempt.RulePackVersion, &attempt.RulePackPointerRevision, &attempt.PlannerConfigurationID, &plannerPayload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return state{}, errAttemptMissing
		}
		return state{}, err
	}
	if len(plannerPayload) > 0 {
		attempt.PlannerConfiguration = append(json.RawMessage(nil), plannerPayload...)
	}
	scope.Attempts = []Attempt{attempt}

	findings, err := tx.Query(ctx, `SELECT id, run_id, attempt_id, case_id, origin, domain, rule_id, capability_id, object_ref, claim, relation, disposition_suggestion, unverified_reason, version, severity, severity_source, anchors, created_at
		FROM findings WHERE attempt_id=$1 AND run_id=$2 ORDER BY seq`, attemptID, runID)
	if err != nil {
		return state{}, err
	}
	defer findings.Close()
	for findings.Next() {
		var item Finding
		var severitySourceJSON, anchorsJSON []byte
		if err := findings.Scan(&item.ID, &item.RunID, &item.AttemptID, &item.CaseID, &item.Origin, &item.Domain, &item.RuleID, &item.CapabilityID, &item.ObjectRef, &item.Claim, &item.Relation, &item.DispositionSuggestion, &item.UnverifiedReason, &item.Version, &item.Severity, &severitySourceJSON, &anchorsJSON, &item.CreatedAt); err != nil {
			return state{}, err
		}
		if len(severitySourceJSON) > 0 {
			var source FindingSeveritySource
			if err := json.Unmarshal(severitySourceJSON, &source); err != nil {
				return state{}, err
			}
			item.SeveritySource = &source
		}
		if err := json.Unmarshal(anchorsJSON, &item.Anchors); err != nil {
			return state{}, err
		}
		scope.Findings = append(scope.Findings, item)
	}
	if err := findings.Err(); err != nil {
		return state{}, err
	}

	evaluations, err := tx.Query(ctx, `SELECT id, run_id, attempt_id, case_id, domain, relation, rule_version, reason, finding_id, rule_id, capability_id, reason_code, fact_ids, object_refs, mandatory, evidence
		FROM constraint_evaluations WHERE attempt_id=$1 AND run_id=$2 ORDER BY seq`, attemptID, runID)
	if err != nil {
		return state{}, err
	}
	defer evaluations.Close()
	for evaluations.Next() {
		var item ConstraintEval
		var factIDs, objectRefs string
		var evidence []byte
		if err := evaluations.Scan(&item.ID, &item.RunID, &item.AttemptID, &item.CaseID, &item.Domain, &item.Relation, &item.RuleVersion, &item.Reason, &item.FindingID, &item.RuleID, &item.CapabilityID, &item.ReasonCode, &factIDs, &objectRefs, &item.Mandatory, &evidence); err != nil {
			return state{}, err
		}
		if err := json.Unmarshal([]byte(factIDs), &item.FactIDs); err != nil {
			return state{}, err
		}
		if err := json.Unmarshal([]byte(objectRefs), &item.ObjectRefs); err != nil {
			return state{}, err
		}
		if len(evidence) > 0 {
			if err := json.Unmarshal(evidence, &item.Evidence); err != nil {
				return state{}, err
			}
		}
		if len(item.Evidence) == 0 {
			item.Evidence = nil
		}
		scope.Evaluations = append(scope.Evaluations, item)
	}
	return scope, evaluations.Err()
}

// finishAttemptNarrow commits one attempt's terminal transition under the shared
// case and attempt keys, the attempt-row version CAS and the claim/job fence. The
// canonical output hash is derived in Go from a bounded attempt scope, so the
// persisted value is identical to the whole-state writer's.
func (s *Store) finishAttemptNarrow(ctx context.Context, runID, attemptID, stateName, partialReason, errorCode string) error {
	claim, ok := s.claimForAttempt(attemptID)
	if !ok || claim.JobID == "" || claim.AttemptID != attemptID || claim.RunID != runID {
		return errExecutionEventScope
	}
	if !claimMatchesExecutionScope(ctx, claim) {
		return errLeaseLost
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, claim.scope()); err != nil {
		return err
	}
	if err := scopedCaseLock(ctx, tx, claim.CaseID); err != nil {
		return err
	}
	if err := scopedAttemptLock(ctx, tx, attemptID); err != nil {
		return err
	}
	var currentState string
	var version int64
	if err := tx.QueryRow(ctx, `SELECT state, entity_version FROM review_attempts WHERE id=$1 AND run_id=$2 FOR UPDATE`, attemptID, runID).Scan(&currentState, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errAttemptMissing
		}
		return err
	}
	if !attemptActive(currentState) {
		return errAttemptTerminal
	}
	if err := s.requireLiveClaim(ctx, tx, claim, runID, attemptID); err != nil {
		return err
	}
	// A live claim is not authority: re-authorize the attempt's actor and the
	// case grant under the same locks, so a membership revocation or archive that
	// committed before this transition refuses it (parity with the old
	// authorizeAttemptExecution path inside updateAuthorizedExecution).
	if err := s.reauthorizeAttempt(ctx, tx, runID, attemptID); err != nil {
		if !errors.Is(err, errAttemptAuthorizationRevoked) {
			return err
		}
		// Parity with the whole-state writer: a revoked attempt is still
		// finalized, as cancelled with an explicit reason, instead of being left
		// running with a live claim.
		stateName, partialReason, errorCode = "cancelled", "", "authorization_revoked"
	}
	scope, err := loadAttemptOutputScope(ctx, tx, runID, attemptID)
	if err != nil {
		return err
	}
	reviewDigest, err := agentReviewDigestForOutput(ctx, tx, claim.TenantID, claim.OrgID, attemptID)
	if err != nil {
		return err
	}
	outputHash, err := attemptOutputHash(scope, runID, attemptID, reviewDigest)
	if err != nil {
		return err
	}
	findings := scope.findingsForAttempt(runID, attemptID)
	finishedAt := stamp()
	runState := stateName
	if stateName == "completed" && len(findings) > 0 {
		runState = "awaiting_human"
	}
	failureMessage := ""
	if stateName == "failed" {
		failureMessage = "这次运行未能完成。"
	}
	var errorCodeValue any
	if errorCode != "" {
		errorCodeValue = errorCode
	}
	tag, err := tx.Exec(ctx, `UPDATE review_attempts SET state=$3, finished_at=$4, error_code=$5, output_hash=$6, entity_version=entity_version+1
		WHERE id=$1 AND run_id=$2 AND entity_version=$7`, attemptID, runID, stateName, finishedAt, errorCodeValue, outputHash, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errAttemptTerminal
	}
	tag, err = tx.Exec(ctx, `UPDATE review_runs SET state=$2, partial_reason=$3, failure_message=$4, updated_at=$5
		WHERE id=$1 AND tenant_id=$6 AND org_id=$7`, runID, runState, partialReason, failureMessage, finishedAt, claim.TenantID, claim.OrgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("run identity changed during the attempt transition")
	}
	outcome := attemptOutcome{
		AttemptID: attemptID, RunID: runID, AttemptState: stateName, RunState: runState,
		PartialReason: partialReason, FailureReason: failureMessage, RunUpdatedAt: finishedAt,
		OutputHash: &outputHash,
	}
	finished := finishedAt
	outcome.FinishedAt = &finished
	if errorCode != "" {
		code := errorCode
		outcome.ErrorCode = &code
	}
	var eventSeq int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(event_seq),0)+1 FROM execution_events WHERE attempt_id=$1`, attemptID).Scan(&eventSeq); err != nil {
		return err
	}
	var spanID *string
	if attemptID != "" {
		spanID = &attemptID
	}
	outcome.Events = append(outcome.Events, ExecutionEvent{
		AttemptID: attemptID, Seq: eventSeq, SpanID: spanID, NodeKind: "attempt", Phase: "terminal_persistence",
		EventType: "attempt_finished", At: finishedAt, Status: stateName, ErrorCode: outcome.ErrorCode,
	})
	if err := insertExecutionEvent(ctx, tx, outcome.Events[0]); err != nil {
		return err
	}
	runEvent := Event{RunID: runID, Type: "state", State: runState}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(event_seq),0)+1 FROM `+runEventTableFor(s.serviceOwner)+` WHERE run_id=$1`, runID).Scan(&runEvent.Seq); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO `+runEventTableFor(s.serviceOwner)+` (run_id, event_seq, event_type, state) VALUES ($1,$2,$3,$4)`,
		runEvent.RunID, runEvent.Seq, runEvent.Type, runEvent.State); err != nil {
		return err
	}
	outcome.RunEvents = append(outcome.RunEvents, runEvent)
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.patchAttemptOutcomeProjection(ctx, outcome)
	return nil
}

// reauthorizeAttempt re-checks the attempt actor and the case Review grant with
// bounded reads inside the transition transaction. A revoked membership, a
// disabled actor or an archived case refuses the transaction.
func (s *Store) reauthorizeAttempt(ctx context.Context, tx pgx.Tx, runID, attemptID string) error {
	var actorID, caseOwnerID, memberJSON, businessStatus, caseTenant, caseOrg, caseID string
	var archivedAt *string
	if err := tx.QueryRow(ctx, `SELECT a.actor_id, c.owner_id, c.member_ids, c.business_status, c.archived_at, c.tenant_id, c.org_id, c.id
		FROM review_attempts AS a
		JOIN review_runs AS r ON r.tenant_id=a.tenant_id AND r.org_id=a.org_id AND r.id=a.run_id
		JOIN cases AS c ON c.tenant_id=r.tenant_id AND c.org_id=r.org_id AND c.id=r.case_id
		WHERE a.id=$1 AND a.run_id=$2`, attemptID, runID).
		Scan(&actorID, &caseOwnerID, &memberJSON, &businessStatus, &archivedAt, &caseTenant, &caseOrg, &caseID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errAttemptMissing
		}
		return err
	}
	var members []string
	if err := json.Unmarshal([]byte(memberJSON), &members); err != nil {
		return errAttemptAuthorizationRevoked
	}
	user, err := trustedUser(accounts.User{ID: actorID})
	if err != nil {
		return errAttemptAuthorizationRevoked
	}
	caseItem := Case{ID: caseID, TenantID: caseTenant, OwnerOrgID: caseOrg, OwnerID: caseOwnerID, MemberIDs: members,
		BusinessStatus: businessStatus, ArchivedAt: archivedAt}
	if err := businessWritable(caseItem); err != nil {
		return errAttemptAuthorizationRevoked
	}
	if !authorizeCase(caseItem, user, access.Review) {
		return errAttemptAuthorizationRevoked
	}
	return nil
}

// loadRunAuditChain reads the authoritative audit chain of one run from its
// routed table, so a narrow writer chains exactly like the whole-state writer:
// the previous hash comes from PostgreSQL, never from a possibly stale memory.
func (s *Store) loadRunAuditChain(ctx context.Context, tx pgx.Tx, runID string) (state, error) {
	rows, err := tx.Query(ctx, `SELECT event_id, run_id, actor, seq, event_type, prev_hash, hash, correlation_ids FROM `+
		auditTableFor(s.serviceOwner)+` WHERE run_id=$1 ORDER BY seq`, runID)
	if err != nil {
		return state{}, err
	}
	defer rows.Close()
	var chain state
	for rows.Next() {
		var item AuditRecord
		var correlation string
		if err := rows.Scan(&item.EventID, &item.RunID, &item.Actor, &item.Seq, &item.EventType, &item.PrevHash, &item.Hash, &correlation); err != nil {
			return state{}, err
		}
		if err := json.Unmarshal([]byte(correlation), &item.CorrelationIDs); err != nil {
			return state{}, err
		}
		chain.Audits = append(chain.Audits, item)
	}
	return chain, rows.Err()
}

// requireLiveClaim re-checks the worker job under the attempt lock: the same
// worker, generation and case/run/attempt binding with a live lease.
func (s *Store) requireLiveClaim(ctx context.Context, tx pgx.Tx, claim JobClaim, runID, attemptID string) error {
	var jobID string
	if err := tx.QueryRow(ctx, `SELECT id FROM worker_jobs
		WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND run_id=$5 AND attempt_id=$6
			AND worker_id=$7 AND generation=$8 AND state='running' AND lease_until > clock_timestamp()
		FOR UPDATE`,
		claim.JobID, claim.TenantID, claim.OrgID, claim.CaseID, runID, attemptID, claim.WorkerID, claim.Generation).Scan(&jobID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errLeaseLost
		}
		return err
	}
	return nil
}

// patchAttemptOutcomeProjection publishes the committed transition to the read
// model. Only the affected attempt, run and event slices are touched.
func (s *Store) patchAttemptOutcomeProjection(ctx context.Context, outcome attemptOutcome) {
	if err := s.lockExecutionContext(ctx); err != nil {
		return
	}
	defer s.mu.Unlock()
	for i := range s.st.Attempts {
		if s.st.Attempts[i].ID == outcome.AttemptID {
			s.st.Attempts[i].State = outcome.AttemptState
			s.st.Attempts[i].FinishedAt = outcome.FinishedAt
			s.st.Attempts[i].ErrorCode = outcome.ErrorCode
			if outcome.OutputHash != nil {
				value := *outcome.OutputHash
				s.st.Attempts[i].OutputHash = &value
			}
		}
	}
	for i := range s.st.Runs {
		if s.st.Runs[i].ID == outcome.RunID {
			s.st.Runs[i].State = outcome.RunState
			s.st.Runs[i].PartialReason = outcome.PartialReason
			s.st.Runs[i].FailureMessage = outcome.FailureReason
			s.st.Runs[i].UpdatedAt = outcome.RunUpdatedAt
		}
	}
	for _, event := range outcome.Events {
		duplicate := false
		for _, existing := range s.st.ExecutionEvents {
			if existing.AttemptID == event.AttemptID && existing.Seq == event.Seq {
				duplicate = true
				break
			}
		}
		if !duplicate {
			s.st.ExecutionEvents = append(s.st.ExecutionEvents, event)
		}
	}
	for _, event := range outcome.RunEvents {
		duplicate := false
		for _, existing := range s.st.Events {
			if existing.RunID == event.RunID && existing.Seq == event.Seq {
				duplicate = true
				break
			}
		}
		if !duplicate {
			s.st.Events = append(s.st.Events, event)
		}
	}
	if outcome.Audit != nil {
		duplicate := false
		for _, existing := range s.st.Audits {
			if existing.EventID == outcome.Audit.EventID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			s.st.Audits = append(s.st.Audits, *outcome.Audit)
		}
	}
}

// cancelAttemptNarrow cancels one live attempt from an authenticated API caller
// that may share no process with the worker: the trusted user is re-resolved, the
// case row and its Manage/Review grant plus the archived guard are checked inside
// the case lock, the run/attempt binding is read fresh, and any durable job row
// for the attempt loses its lease. No local worker claim is required.
func (s *Store) cancelAttemptNarrow(ctx context.Context, user accounts.User, caseID, runID, attemptID string) error {
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	scope := userScope(current)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return err
	}
	var caseTenant, caseOrg, caseOwner, businessLine, scenario, businessStatus, title, contractType, memberJSON string
	var archivedAt *string
	if err := tx.QueryRow(ctx, `SELECT tenant_id, org_id, owner_id, business_line, scenario, business_status, archived_at, title, contract_type, member_ids
		FROM cases WHERE id=$1`, caseID).
		Scan(&caseTenant, &caseOrg, &caseOwner, &businessLine, &scenario, &businessStatus, &archivedAt, &title, &contractType, &memberJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.NotFound("没有找到对应记录")
		}
		return err
	}
	var members []string
	if err := json.Unmarshal([]byte(memberJSON), &members); err != nil {
		return err
	}
	caseItem := Case{ID: caseID, TenantID: caseTenant, OwnerOrgID: caseOrg, OwnerID: caseOwner,
		BusinessLine: businessLine, Scenario: scenario, BusinessStatus: businessStatus, ArchivedAt: archivedAt,
		Title: title, ContractType: contractType, MemberIDs: members}
	if err := businessWritable(caseItem); err != nil {
		return err
	}
	if err := requireCaseAction(caseItem, current, access.Review); err != nil {
		return err
	}
	if err := scopedAttemptLock(ctx, tx, attemptID); err != nil {
		return err
	}
	var attemptRunID, currentState string
	var version int64
	if err := tx.QueryRow(ctx, `SELECT run_id, state, entity_version FROM review_attempts WHERE id=$1 FOR UPDATE`, attemptID).
		Scan(&attemptRunID, &currentState, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.NotFound("没有找到对应记录")
		}
		return err
	}
	if attemptRunID != runID {
		return fault.NotFound("没有找到对应记录")
	}
	if !attemptActive(currentState) {
		return errAttemptTerminal
	}
	var runCaseID string
	if err := tx.QueryRow(ctx, `SELECT case_id FROM review_runs WHERE id=$1 AND tenant_id=$2 AND org_id=$3 FOR UPDATE`,
		runID, caseTenant, caseOrg).Scan(&runCaseID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.NotFound("没有找到对应记录")
		}
		return err
	}
	if runCaseID != caseID {
		return fault.NotFound("没有找到对应记录")
	}
	var latestRunID string
	if err := tx.QueryRow(ctx, `SELECT id FROM review_runs WHERE case_id=$1 ORDER BY seq DESC LIMIT 1`, caseID).Scan(&latestRunID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.NotFound("没有找到对应记录")
		}
		return err
	}
	if latestRunID != runID {
		return fault.NotFound("没有找到对应记录")
	}
	var latestAttemptID string
	if err := tx.QueryRow(ctx, `SELECT id FROM review_attempts WHERE run_id=$1 ORDER BY ordinal DESC LIMIT 1`, runID).Scan(&latestAttemptID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.NotFound("没有找到对应记录")
		}
		return err
	}
	if latestAttemptID != attemptID {
		return fault.NotFound("没有找到对应记录")
	}
	finishedAt := stamp()
	errorCode := "cancelled_by_user"
	tag, err := tx.Exec(ctx, `UPDATE review_attempts SET state='cancelled', finished_at=$3, error_code=$4, entity_version=entity_version+1
		WHERE id=$1 AND run_id=$2 AND entity_version=$5 AND state IN ('accepted','running')`, attemptID, runID, finishedAt, errorCode, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errAttemptTerminal
	}
	tag, err = tx.Exec(ctx, `UPDATE review_runs SET state='cancelled', updated_at=$2 WHERE id=$1 AND tenant_id=$3 AND org_id=$4`,
		runID, finishedAt, caseTenant, caseOrg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("run identity changed during the attempt cancellation")
	}
	var jobID, jobState string
	jobErr := tx.QueryRow(ctx, `SELECT id, state FROM worker_jobs WHERE attempt_id=$1 FOR UPDATE`, attemptID).Scan(&jobID, &jobState)
	if jobErr != nil && !errors.Is(jobErr, pgx.ErrNoRows) {
		return jobErr
	}
	if jobErr == nil && (jobState == "running" || jobState == "queued") {
		tag, err = tx.Exec(ctx, `UPDATE worker_jobs SET state='cancelled', lease_until=NULL, updated_at=$2 WHERE id=$1 AND state IN ('running','queued')`,
			jobID, finishedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errLeaseLost
		}
	}
	outcome := attemptOutcome{
		AttemptID: attemptID, RunID: runID, AttemptState: "cancelled", RunState: "cancelled",
		FinishedAt: &finishedAt, ErrorCode: &errorCode, RunUpdatedAt: finishedAt,
	}
	var spanID *string
	if attemptID != "" {
		spanID = &attemptID
	}
	var eventSeq int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(event_seq),0)+1 FROM execution_events WHERE attempt_id=$1`, attemptID).Scan(&eventSeq); err != nil {
		return err
	}
	cancelled := ExecutionEvent{
		AttemptID: attemptID, Seq: eventSeq, SpanID: spanID, NodeKind: "attempt", Phase: "cancellation",
		EventType: "attempt_cancelled", At: finishedAt, Status: "cancelled", ErrorCode: &errorCode,
	}
	if err := insertExecutionEvent(ctx, tx, cancelled); err != nil {
		return err
	}
	outcome.Events = append(outcome.Events, cancelled)
	runEvent := Event{RunID: runID, Type: "state", State: "cancelled"}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(event_seq),0)+1 FROM `+runEventTableFor(s.serviceOwner)+` WHERE run_id=$1`, runID).Scan(&runEvent.Seq); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO `+runEventTableFor(s.serviceOwner)+` (run_id, event_seq, event_type, state) VALUES ($1,$2,$3,$4)`,
		runEvent.RunID, runEvent.Seq, runEvent.Type, runEvent.State); err != nil {
		return err
	}
	outcome.RunEvents = append(outcome.RunEvents, runEvent)
	// Cancellation is not allowed to commit without its audit row: the append and
	// the routed insert share this transaction, so any failure rolls the whole
	// cancellation back.
	chain, err := s.loadRunAuditChain(ctx, tx, runID)
	if err != nil {
		return err
	}
	if err := appendAudit(&chain, runID, current.ID, "review_cancelled", []string{caseID}); err != nil {
		return err
	}
	added := chain.Audits[len(chain.Audits)-1]
	if err := syncAudits(ctx, tx, []AuditRecord{added}, s.serviceOwner); err != nil {
		return err
	}
	outcome.Audit = &added
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.patchAttemptOutcomeProjection(ctx, outcome)
	return nil
}
