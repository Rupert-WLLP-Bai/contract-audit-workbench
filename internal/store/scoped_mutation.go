package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// loadScopedAttemptState loads only the records a durable attempt mutation can
// touch: the case, run, run snapshot, attempt, that attempt's execution events,
// findings and evaluations, the run's events and the run's audit chain. Callbacks
// therefore see a bounded state in the same shape as the whole-state writer, and
// nothing else in the store is read or cloned.
func (s *Store) loadScopedAttemptState(ctx context.Context, tx pgx.Tx, claim JobClaim, runID, attemptID string) (state, error) {
	var out state
	var run Run
	var runTenant, runOrg string
	if err := tx.QueryRow(ctx, `SELECT id, tenant_id, org_id, case_id, state, model_ref, partial_reason, failure_message, created_at, updated_at
		FROM review_runs WHERE id=$1`, runID).
		Scan(&run.ID, &runTenant, &runOrg, &run.CaseID, &run.State, &run.ModelRef, &run.PartialReason, &run.FailureMessage, &run.CreatedAt, &run.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return state{}, errAttemptMissing
		}
		return state{}, err
	}
	if run.CaseID != claim.CaseID || runTenant != claim.TenantID || runOrg != claim.OrgID {
		return state{}, errExecutionEventScope
	}
	out.Runs = []Run{run}
	var caseItem Case
	var memberJSON string
	if err := tx.QueryRow(ctx, `SELECT id, tenant_id, org_id, business_line, scenario, business_status, archived_at, title, contract_type, owner_id, member_ids, created_at, updated_at
		FROM cases WHERE id=$1 AND tenant_id=$2 AND org_id=$3`, run.CaseID, claim.TenantID, claim.OrgID).
		Scan(&caseItem.ID, &caseItem.TenantID, &caseItem.OwnerOrgID, &caseItem.BusinessLine, &caseItem.Scenario, &caseItem.BusinessStatus, &caseItem.ArchivedAt, &caseItem.Title, &caseItem.ContractType, &caseItem.OwnerID, &memberJSON, &caseItem.CreatedAt, &caseItem.UpdatedAt); err != nil {
		return state{}, err
	}
	if err := json.Unmarshal([]byte(memberJSON), &caseItem.MemberIDs); err != nil {
		return state{}, err
	}
	out.Cases = []Case{caseItem}

	scope, err := loadAttemptOutputScope(ctx, tx, runID, attemptID)
	if err != nil {
		return state{}, err
	}
	out.Attempts = scope.Attempts
	out.Findings = scope.Findings
	out.Evaluations = scope.Evaluations

	execEvents, err := tx.Query(ctx, `SELECT attempt_id, event_seq, span_id, parent_span_id, node_kind, phase, event_type, occurred_at, duration_ms, status, error_code, model_ref, usage
		FROM execution_events WHERE attempt_id=$1 ORDER BY event_seq`, attemptID)
	if err != nil {
		return state{}, err
	}
	defer execEvents.Close()
	for execEvents.Next() {
		var item ExecutionEvent
		var spanID, parentID, errorCode, modelRef, usageJSON *string
		var duration *int64
		if err := execEvents.Scan(&item.AttemptID, &item.Seq, &spanID, &parentID, &item.NodeKind, &item.Phase, &item.EventType, &item.At, &duration, &item.Status, &errorCode, &modelRef, &usageJSON); err != nil {
			return state{}, err
		}
		item.SpanID, item.ParentSpanID, item.ErrorCode, item.ModelRef, item.DurationMS = spanID, parentID, errorCode, modelRef, duration
		if usageJSON != nil {
			var usage UsageObservation
			if err := json.Unmarshal([]byte(*usageJSON), &usage); err != nil {
				return state{}, err
			}
			item.Usage = &usage
		}
		out.ExecutionEvents = append(out.ExecutionEvents, item)
	}
	if err := execEvents.Err(); err != nil {
		return state{}, err
	}

	runEvents, err := tx.Query(ctx, `SELECT run_id, event_seq, event_type, state FROM `+runEventTableFor(s.serviceOwner)+` WHERE run_id=$1 ORDER BY event_seq`, runID)
	if err != nil {
		return state{}, err
	}
	defer runEvents.Close()
	for runEvents.Next() {
		var item Event
		if err := runEvents.Scan(&item.RunID, &item.Seq, &item.Type, &item.State); err != nil {
			return state{}, err
		}
		out.Events = append(out.Events, item)
	}
	if err := runEvents.Err(); err != nil {
		return state{}, err
	}

	chain, err := s.loadRunAuditChain(ctx, tx, runID)
	if err != nil {
		return state{}, err
	}
	out.Audits = chain.Audits

	// Snapshot of the run: the freeze and preparation callbacks read and write it.
	var snapshot RunSnapshot
	var documentIDs, parsedDocumentIDs string
	var packSnapshot, plannerPayload, policySnapshot []byte
	if err := tx.QueryRow(ctx, `SELECT run_id, document_ids, rule_set_hash, playbook_hash, model_ref, comparator_ref, model_configuration_id, parsed_document_ids, parse_selection_hash, parser_configuration_id, rule_pack_compiler_ref, rule_pack_id, rule_pack_version, rule_pack_hash, rule_pack_digest, rule_pack_snapshot, planner_configuration_id, planner_configuration, cloud_policy_snapshot, cloud_policy_digest
		FROM review_run_snapshots WHERE run_id=$1`, runID).
		Scan(&snapshot.RunID, &documentIDs, &snapshot.RuleSetHash, &snapshot.PlaybookHash, &snapshot.ModelRef, &snapshot.ComparatorRef,
			&snapshot.ModelConfigurationID, &parsedDocumentIDs, &snapshot.ParseSelectionHash, &snapshot.ParserConfigurationID, &snapshot.RulePackCompilerRef,
			&snapshot.RulePackID, &snapshot.RulePackVersion, &snapshot.RulePackHash, &snapshot.RulePackDigest, &packSnapshot, &snapshot.PlannerConfigurationID, &plannerPayload,
			&policySnapshot, &snapshot.CloudPolicyDigest); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return state{}, err
		}
	} else {
		if err := json.Unmarshal([]byte(documentIDs), &snapshot.DocumentIDs); err != nil {
			return state{}, err
		}
		if err := json.Unmarshal([]byte(parsedDocumentIDs), &snapshot.ParsedDocumentIDs); err != nil {
			return state{}, err
		}
		if len(packSnapshot) > 0 {
			snapshot.RulePackSnapshot = append([]byte(nil), packSnapshot...)
		}
		if len(plannerPayload) > 0 {
			snapshot.PlannerConfiguration = append(json.RawMessage(nil), plannerPayload...)
		}
		if len(policySnapshot) > 0 {
			snapshot.CloudPolicySnapshot = append(json.RawMessage(nil), policySnapshot...)
		}
		out.Snapshots = []RunSnapshot{snapshot}
	}

	// Parse selections and the frozen attempt input of this attempt.
	selectionRows, err := tx.Query(ctx, `SELECT attempt_id, run_id, document_id, original_digest, parsed_document_id, selection_order, parser_id, parser_version, config_hash, normalization_version, artifact_digest
		FROM review_attempt_parse_selections WHERE attempt_id=$1 AND run_id=$2 ORDER BY selection_order`, attemptID, runID)
	if err != nil {
		return state{}, err
	}
	defer selectionRows.Close()
	for selectionRows.Next() {
		var item AttemptParseSelection
		if err := selectionRows.Scan(&item.AttemptID, &item.RunID, &item.DocumentID, &item.OriginalDigest, &item.ParsedDocumentID,
			&item.SelectionOrder, &item.ParserID, &item.ParserVersion, &item.ConfigHash, &item.NormalizationVersion, &item.ArtifactDigest); err != nil {
			return state{}, err
		}
		out.AttemptParseSelections = append(out.AttemptParseSelections, item)
	}
	if err := selectionRows.Err(); err != nil {
		return state{}, err
	}

	var input AttemptInput
	var inputDocuments string
	if err := tx.QueryRow(ctx, `SELECT attempt_id, run_id, input_hash, frozen_at, documents FROM review_attempt_inputs WHERE attempt_id=$1 AND run_id=$2`, attemptID, runID).
		Scan(&input.AttemptID, &input.RunID, &input.InputHash, &input.FrozenAt, &inputDocuments); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return state{}, err
		}
	} else {
		if err := json.Unmarshal([]byte(inputDocuments), &input.Documents); err != nil {
			return state{}, err
		}
		out.AttemptInputs = []AttemptInput{input}
	}

	// The case's documents and the parsed artifacts this attempt selected.
	caseDocuments, err := tx.Query(ctx, `SELECT id, tenant_id, org_id, case_id, role, filename, media_type, digest, size, origin, storage_backend, created_at
		FROM documents WHERE case_id=$1 AND tenant_id=$2 AND org_id=$3 ORDER BY seq`, run.CaseID, claim.TenantID, claim.OrgID)
	if err != nil {
		return state{}, err
	}
	defer caseDocuments.Close()
	for caseDocuments.Next() {
		var item Document
		if err := caseDocuments.Scan(&item.ID, &item.TenantID, &item.OrgID, &item.CaseID, &item.Role, &item.Filename, &item.MediaType,
			&item.Digest, &item.Size, &item.Origin, &item.StorageBackend, &item.CreatedAt); err != nil {
			return state{}, err
		}
		out.Documents = append(out.Documents, item)
	}
	if err := caseDocuments.Err(); err != nil {
		return state{}, err
	}

	parsedRows, err := tx.Query(ctx, `SELECT parsed.payload FROM review_attempt_parse_selections AS selection
		JOIN parsed_documents AS parsed ON parsed.id=selection.parsed_document_id
		WHERE selection.attempt_id=$1 AND selection.run_id=$2 ORDER BY selection.selection_order`, attemptID, runID)
	if err != nil {
		return state{}, err
	}
	defer parsedRows.Close()
	for parsedRows.Next() {
		var payload string
		if err := parsedRows.Scan(&payload); err != nil {
			return state{}, err
		}
		parsed, err := parsePayload(payload)
		if err != nil {
			return state{}, err
		}
		out.ParsedDocuments = append(out.ParsedDocuments, parsed)
	}
	if err := parsedRows.Err(); err != nil {
		return state{}, err
	}
	return out, nil
}

// scopedAttemptMutation is the shared adapter for durable attempt mutations that
// are not plain event appends: it loads the bounded attempt scope, runs the same
// callback the whole-state writer would, validates the append-only invariants,
// persists the changed owned rows through the existing delta/persist path inside
// the caller's transaction, and then patches only the slices the scope contains.
func (s *Store) scopedAttemptMutation(ctx context.Context, runID, attemptID string, fn func(*state) error) error {
	claim, ok := s.claimForAttempt(attemptID)
	if !ok || claim.JobID == "" || claim.AttemptID != attemptID || claim.RunID != runID {
		return errExecutionEventScope
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
	var attemptVersion int64
	if err := tx.QueryRow(ctx, `SELECT state, entity_version FROM review_attempts WHERE id=$1 AND run_id=$2 FOR UPDATE`, attemptID, runID).Scan(&currentState, &attemptVersion); err != nil {
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
	if err := s.reauthorizeAttempt(ctx, tx, runID, attemptID); err != nil {
		return err
	}
	previous, err := s.loadScopedAttemptState(ctx, tx, claim, runID, attemptID)
	if err != nil {
		return err
	}
	next, err := previous.clone()
	if err != nil {
		return err
	}
	if err := fn(&next); err != nil {
		return err
	}
	old, changed, err := stateDelta(previous, next)
	if err != nil {
		return err
	}
	if err := s.validateServiceDelta(old, changed); err != nil {
		return err
	}
	if err := validateAppendOnly(previous, next); err != nil {
		return err
	}
	if err := s.persist(ctx, tx, previous, next, &attemptVersion); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.patchScopedAttemptProjection(ctx, changed)
	return nil
}

func (s *Store) patchScopedAttemptProjection(ctx context.Context, changed state) {
	if !s.mu.TryLock() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if s.mu.TryLock() {
				break
			}
		}
	}
	if ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	defer s.mu.Unlock()

	for _, item := range changed.Cases {
		for i := range s.st.Cases {
			if s.st.Cases[i].ID == item.ID {
				s.st.Cases[i] = item
			}
		}
	}
	for _, item := range changed.Runs {
		for i := range s.st.Runs {
			if s.st.Runs[i].ID == item.ID {
				s.st.Runs[i] = item
			}
		}
	}
	for _, item := range changed.Attempts {
		for i := range s.st.Attempts {
			if s.st.Attempts[i].ID == item.ID {
				s.st.Attempts[i] = item
			}
		}
	}
	s.st.ExecutionEvents = mergeExecutionEventSlice(s.st.ExecutionEvents, changed.ExecutionEvents)
	s.st.Events = mergeRunEventSlice(s.st.Events, changed.Events)
	s.st.Findings = mergeFindingSlice(s.st.Findings, changed.Findings)
	s.st.Evaluations = mergeEvaluationSlice(s.st.Evaluations, changed.Evaluations)
	s.st.Audits = mergeAuditSlice(s.st.Audits, changed.Audits)
	for _, item := range changed.Reports {
		replaced := false
		for i := range s.st.Reports {
			if s.st.Reports[i].Record.ID == item.Record.ID {
				s.st.Reports[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			s.st.Reports = append(s.st.Reports, item)
		}
	}
	for _, item := range changed.Archives {
		duplicate := false
		for _, current := range s.st.Archives {
			if current.Record.ID == item.Record.ID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			s.st.Archives = append(s.st.Archives, item)
		}
	}
	for _, item := range changed.Documents {
		replaced := false
		for i := range s.st.Documents {
			if s.st.Documents[i].ID == item.ID {
				s.st.Documents[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			s.st.Documents = append(s.st.Documents, item)
		}
	}
	for _, item := range changed.ParsedDocuments {
		replaced := false
		for i := range s.st.ParsedDocuments {
			if s.st.ParsedDocuments[i].ID == item.ID {
				s.st.ParsedDocuments[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			s.st.ParsedDocuments = append(s.st.ParsedDocuments, item)
		}
	}
	for _, item := range changed.Generations {
		duplicate := false
		for _, current := range s.st.Generations {
			if current.Record.ID == item.Record.ID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			s.st.Generations = append(s.st.Generations, item)
		}
	}

}

func mergeExecutionEventSlice(existing, added []ExecutionEvent) []ExecutionEvent {
	for _, item := range added {
		duplicate := false
		for _, current := range existing {
			if current.AttemptID == item.AttemptID && current.Seq == item.Seq {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, item)
		}
	}
	return existing
}

func mergeRunEventSlice(existing, added []Event) []Event {
	for _, item := range added {
		duplicate := false
		for _, current := range existing {
			if current.RunID == item.RunID && current.Seq == item.Seq {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, item)
		}
	}
	return existing
}

func mergeFindingSlice(existing, added []Finding) []Finding {
	for _, item := range added {
		replaced := false
		for i := range existing {
			if existing[i].ID == item.ID {
				existing[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, item)
		}
	}
	return existing
}

func mergeEvaluationSlice(existing, added []ConstraintEval) []ConstraintEval {
	for _, item := range added {
		replaced := false
		for i := range existing {
			if existing[i].ID == item.ID {
				existing[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, item)
		}
	}
	return existing
}

func mergeAuditSlice(existing, added []AuditRecord) []AuditRecord {
	for _, item := range added {
		duplicate := false
		for _, current := range existing {
			if current.EventID == item.EventID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, item)
		}
	}
	return existing
}

// loadScopedCaseState loads only the records a case-level mutation can touch:
// the case row, its documents and their parsed artifacts, its activity, its runs
// and the audit chains of those runs.
func (s *Store) loadScopedCaseState(ctx context.Context, tx pgx.Tx, caseID string) (state, error) {
	var out state
	var caseItem Case
	var memberJSON string
	if err := tx.QueryRow(ctx, `SELECT id, tenant_id, org_id, business_line, scenario, business_status, archived_at, title, contract_type, owner_id, member_ids, created_at, updated_at
		FROM cases WHERE id=$1`, caseID).
		Scan(&caseItem.ID, &caseItem.TenantID, &caseItem.OwnerOrgID, &caseItem.BusinessLine, &caseItem.Scenario, &caseItem.BusinessStatus,
			&caseItem.ArchivedAt, &caseItem.Title, &caseItem.ContractType, &caseItem.OwnerID, &memberJSON, &caseItem.CreatedAt, &caseItem.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return state{}, fault.NotFound("没有找到对应记录")
		}
		return state{}, err
	}
	if err := json.Unmarshal([]byte(memberJSON), &caseItem.MemberIDs); err != nil {
		return state{}, err
	}
	out.Cases = []Case{caseItem}

	documentRows, err := tx.Query(ctx, `SELECT id, tenant_id, org_id, case_id, role, filename, media_type, digest, size, origin, storage_backend, created_at
		FROM documents WHERE case_id=$1 AND tenant_id=$2 AND org_id=$3 ORDER BY seq`, caseID, caseItem.TenantID, caseItem.OwnerOrgID)
	if err != nil {
		return state{}, err
	}
	defer documentRows.Close()
	var documentIDs []string
	for documentRows.Next() {
		var item Document
		if err := documentRows.Scan(&item.ID, &item.TenantID, &item.OrgID, &item.CaseID, &item.Role, &item.Filename, &item.MediaType,
			&item.Digest, &item.Size, &item.Origin, &item.StorageBackend, &item.CreatedAt); err != nil {
			return state{}, err
		}
		out.Documents = append(out.Documents, item)
		documentIDs = append(documentIDs, item.ID)
	}
	if err := documentRows.Err(); err != nil {
		return state{}, err
	}
	if len(documentIDs) > 0 {
		parsedRows, err := tx.Query(ctx, `SELECT payload FROM parsed_documents WHERE document_id = ANY($1) ORDER BY seq`, documentIDs)
		if err != nil {
			return state{}, err
		}
		defer parsedRows.Close()
		for parsedRows.Next() {
			var payload string
			if err := parsedRows.Scan(&payload); err != nil {
				return state{}, err
			}
			parsed, err := parsePayload(payload)
			if err != nil {
				return state{}, err
			}
			out.ParsedDocuments = append(out.ParsedDocuments, parsed)
		}
		if err := parsedRows.Err(); err != nil {
			return state{}, err
		}
	}
	if ownedTable(s.serviceOwner, "business_generations") {
		generationRows, err := tx.Query(ctx, `SELECT g.id, g.tenant_id, g.org_id, g.case_id, g.document_id, g.template_id, g.template_version,
			g.digest, d.digest, g.created_at FROM business_generations g JOIN documents d
			ON d.tenant_id=g.tenant_id AND d.org_id=g.org_id AND d.case_id=g.case_id AND d.id=g.document_id
			WHERE g.case_id=$1 AND g.tenant_id=$2 AND g.org_id=$3 ORDER BY g.seq`, caseID, caseItem.TenantID, caseItem.OwnerOrgID)
		if err != nil {
			return state{}, err
		}
		defer generationRows.Close()
		for generationRows.Next() {
			var item generationRecord
			var documentDigest string
			if err := generationRows.Scan(&item.Record.ID, &item.TenantID, &item.OrgID, &item.CaseID,
				&item.Record.DocumentID, &item.Record.TemplateID, &item.Record.TemplateVersion,
				&item.Record.Digest, &documentDigest, &item.Record.CreatedAt); err != nil {
				return state{}, err
			}
			if item.Record.Digest != documentDigest {
				return state{}, errors.New("business generation document digest mismatch")
			}
			out.Generations = append(out.Generations, item)
		}
		if err := generationRows.Err(); err != nil {
			return state{}, err
		}
	}

	activityRows, err := tx.Query(ctx, `SELECT id, case_id, occurred_at, summary FROM `+activityTableFor(s.serviceOwner)+` WHERE case_id=$1 ORDER BY seq`, caseID)
	if err != nil {
		return state{}, err
	}
	defer activityRows.Close()
	for activityRows.Next() {
		var item Activity
		if err := activityRows.Scan(&item.ID, &item.CaseID, &item.At, &item.Summary); err != nil {
			return state{}, err
		}
		out.Activity = append(out.Activity, item)
	}
	if err := activityRows.Err(); err != nil {
		return state{}, err
	}
	runRows, err := tx.Query(ctx, `SELECT id, case_id, state, model_ref, partial_reason, failure_message, created_at, updated_at FROM review_runs WHERE case_id=$1 ORDER BY seq`, caseID)
	if err != nil {
		return state{}, err
	}
	defer runRows.Close()
	var runIDs []string
	for runRows.Next() {
		var item Run
		if err := runRows.Scan(&item.ID, &item.CaseID, &item.State, &item.ModelRef, &item.PartialReason, &item.FailureMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return state{}, err
		}
		out.Runs = append(out.Runs, item)
		runIDs = append(runIDs, item.ID)
	}
	if err := runRows.Err(); err != nil {
		return state{}, err
	}
	reportRows, err := tx.Query(ctx, `SELECT id, tenant_id, org_id, case_id, run_id, attempt_id, version, input_hash, output_hash, decision_snapshot_hash, digest, created_at, json_bytes, html_bytes
		FROM business_reports WHERE case_id=$1 ORDER BY seq`, caseID)
	if err != nil {
		return state{}, err
	}
	defer reportRows.Close()
	for reportRows.Next() {
		var item frozenReport
		if err := reportRows.Scan(&item.Record.ID, &item.TenantID, &item.OrgID, &item.CaseID, &item.Record.RunID, &item.Record.AttemptID,
			&item.Record.Version, &item.Record.InputHash, &item.Record.OutputHash, &item.Record.DecisionSnapshotHash,
			&item.Record.Digest, &item.Record.CreatedAt, &item.JSONBytes, &item.HTMLBytes); err != nil {
			return state{}, err
		}
		out.Reports = append(out.Reports, item)
	}
	if err := reportRows.Err(); err != nil {
		return state{}, err
	}
	archiveRows, err := tx.Query(ctx, `SELECT id, tenant_id, org_id, case_id, report_id, actor_id, reason, archived_at FROM business_archives WHERE case_id=$1 ORDER BY seq`, caseID)
	if err != nil {
		return state{}, err
	}
	defer archiveRows.Close()
	for archiveRows.Next() {
		var item archiveRecord
		if err := archiveRows.Scan(&item.Record.ID, &item.TenantID, &item.OrgID, &item.Record.CaseID, &item.Record.ReportID, &item.ActorID,
			&item.Record.Reason, &item.Record.ArchivedAt); err != nil {
			return state{}, err
		}
		out.Archives = append(out.Archives, item)
	}
	if err := archiveRows.Err(); err != nil {
		return state{}, err
	}
	if len(runIDs) > 0 {
		auditRows, err := tx.Query(ctx, `SELECT event_id, run_id, actor, seq, event_type, prev_hash, hash, correlation_ids FROM `+auditTableFor(s.serviceOwner)+` WHERE run_id = ANY($1) ORDER BY seq`, runIDs)
		if err != nil {
			return state{}, err
		}
		defer auditRows.Close()
		for auditRows.Next() {
			var item AuditRecord
			var correlation string
			if err := auditRows.Scan(&item.EventID, &item.RunID, &item.Actor, &item.Seq, &item.EventType, &item.PrevHash, &item.Hash, &correlation); err != nil {
				return state{}, err
			}
			if err := json.Unmarshal([]byte(correlation), &item.CorrelationIDs); err != nil {
				return state{}, err
			}
			out.Audits = append(out.Audits, item)
		}
		if err := auditRows.Err(); err != nil {
			return state{}, err
		}
	}
	return out, nil
}

// scopedCaseMutation is the case-level adapter: the shared case key, a bounded
// case load, the same validation and delta persistence as the whole-state
// writer, and a projection patch limited to the bounded slices. The transaction
// scope comes from the trusted caller, exactly like the whole-state writer's.
func (s *Store) scopedCaseMutation(ctx context.Context, user accounts.User, caseID string, fn func(*state) error) (result error) {
	if ctx == nil {
		return errors.New("case mutation context is nil")
	}
	defer func() {
		if result != nil && ctx.Err() != nil {
			result = ctx.Err()
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	scope := userScope(current)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return err
	}
	previous, err := s.loadScopedCaseState(ctx, tx, caseID)
	if err != nil {
		return err
	}
	caseItem, ok := previous.caseByID(caseID)
	if !ok || caseItem.TenantID != scope.TenantID {
		return fault.NotFound("没有找到对应记录")
	}
	scope.OrgID = caseItem.OwnerOrgID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return err
	}
	next, err := previous.clone()
	if err != nil {
		return err
	}
	if err := fn(&next); err != nil {
		return err
	}
	old, changed, err := stateDelta(previous, next)
	if err != nil {
		return err
	}
	if err := validateAppendOnly(previous, next); err != nil {
		return err
	}
	if err := s.validateServiceDelta(old, changed); err != nil {
		return err
	}
	if err := s.persist(ctx, tx, previous, next, nil); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.patchScopedAttemptProjection(ctx, changed)
	return nil
}
