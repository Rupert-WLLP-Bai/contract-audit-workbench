package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/execution"
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

	execEvents, err := tx.Query(ctx, `SELECT attempt_id, event_seq, span_id, parent_span_id, node_kind, phase, event_type, occurred_at, duration_ms, status, error_code, model_ref, usage, trace, recorded_at, event_key, generation
		FROM execution_events WHERE attempt_id=$1 ORDER BY event_seq`, attemptID)
	if err != nil {
		return state{}, err
	}
	defer execEvents.Close()
	for execEvents.Next() {
		var item ExecutionEvent
		var spanID, parentID, errorCode, modelRef, usageJSON, traceJSON *string
		var duration *int64
		if err := execEvents.Scan(&item.AttemptID, &item.Seq, &spanID, &parentID, &item.NodeKind, &item.Phase, &item.EventType, &item.At, &duration, &item.Status, &errorCode, &modelRef, &usageJSON, &traceJSON, &item.RecordedAt, &item.EventKey, &item.Generation); err != nil {
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
		if traceJSON != nil {
			var trace execution.ModelCallTrace
			if err := json.Unmarshal([]byte(*traceJSON), &trace); err != nil {
				return state{}, err
			}
			item.Trace = &trace
		}
		out.ExecutionEvents = append(out.ExecutionEvents, item)
	}
	if err := execEvents.Err(); err != nil {
		return state{}, err
	}

	runEvents, err := tx.Query(ctx, `SELECT * FROM (
		(SELECT run_id,event_seq,event_type,state FROM run_events WHERE run_id=$1 ORDER BY event_seq DESC LIMIT 1)
		UNION ALL
		(SELECT run_id,event_seq,event_type,state FROM business_run_events WHERE run_id=$1 ORDER BY event_seq DESC LIMIT 1)
	) heads ORDER BY event_seq DESC LIMIT 1`, runID)
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
	var packSnapshot, plannerPayload, policySnapshot, knowledge, partyContext, serviceObjectContext []byte
	if err := tx.QueryRow(ctx, `SELECT run_id, document_ids, rule_set_hash, playbook_hash, model_ref, comparator_ref, model_configuration_id, parsed_document_ids, parse_selection_hash, parser_configuration_id, rule_pack_compiler_ref, rule_pack_id, rule_pack_version, rule_pack_hash, rule_pack_digest, rule_pack_snapshot, planner_configuration_id, planner_configuration, cloud_policy_snapshot, cloud_policy_digest, governed_knowledge, party_context, party_context_hash, service_object_context, service_object_context_hash
		FROM review_run_snapshots WHERE run_id=$1`, runID).
		Scan(&snapshot.RunID, &documentIDs, &snapshot.RuleSetHash, &snapshot.PlaybookHash, &snapshot.ModelRef, &snapshot.ComparatorRef,
			&snapshot.ModelConfigurationID, &parsedDocumentIDs, &snapshot.ParseSelectionHash, &snapshot.ParserConfigurationID, &snapshot.RulePackCompilerRef,
			&snapshot.RulePackID, &snapshot.RulePackVersion, &snapshot.RulePackHash, &snapshot.RulePackDigest, &packSnapshot, &snapshot.PlannerConfigurationID, &plannerPayload,
			&policySnapshot, &snapshot.CloudPolicyDigest, &knowledge, &partyContext, &snapshot.PartyContextHash,
			&serviceObjectContext, &snapshot.ServiceObjectContextHash); err != nil {
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
		snapshot.PartyContext, err = decodePartyContext(partyContext, snapshot.PartyContextHash)
		if err != nil {
			return state{}, fmt.Errorf("invalid party context snapshot for run %s: %w", snapshot.RunID, err)
		}
		snapshot.ServiceObjectContext, err = decodeServiceObjectReviewContext(serviceObjectContext, snapshot.ServiceObjectContextHash)
		if err != nil {
			return state{}, fmt.Errorf("invalid service object context snapshot for run %s: %w", snapshot.RunID, err)
		}
		snapshot.GovernedKnowledge, err = decodeGovernedKnowledge(knowledge)
		if err != nil {
			return state{}, err
		}
		if _, err := copyGovernedKnowledge(snapshot); err != nil {
			return state{}, err
		}
		out.Snapshots = []RunSnapshot{snapshot}
	}
	for _, attempt := range out.Attempts {
		if err := validateGovernedAttemptSnapshot(attempt, snapshot); err != nil {
			return state{}, err
		}
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

	parsedRows, err := tx.Query(ctx, `SELECT parsed.id, parsed.selection_order, parsed.document_id, parsed.original_digest, parsed.parser_id, parsed.parser_version, parsed.config_hash, parsed.normalization_version, parsed.normalized_digest, parsed.ir_digest, parsed.artifact_digest, parsed.created_at, parsed.status, parsed.payload
		FROM review_attempt_parse_selections AS selection
		JOIN parsed_documents AS parsed ON parsed.id=selection.parsed_document_id
		WHERE selection.attempt_id=$1 AND selection.run_id=$2 ORDER BY selection.selection_order`, attemptID, runID)
	if err != nil {
		return state{}, err
	}
	out.ParsedDocuments, err = decodeScopedParsedRows(parsedRows, out.Documents)
	if err != nil {
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
	return s.scopedAttemptMutationWithGovernance(ctx, runID, attemptID, false, fn)
}

func (s *Store) scopedGovernedAttemptMutation(ctx context.Context, runID, attemptID string, fn func(*state) error) error {
	return s.scopedAttemptMutationWithGovernance(ctx, runID, attemptID, true, fn)
}

func (s *Store) scopedAttemptMutationWithGovernance(ctx context.Context, runID, attemptID string, governance bool, fn func(*state) error) error {
	return s.scopedAttemptMutationWithTx(ctx, runID, attemptID, governance, func(_ pgx.Tx, st *state) error {
		return fn(st)
	})
}

func (s *Store) scopedAttemptMutationWithTx(ctx context.Context, runID, attemptID string, governance bool, fn func(pgx.Tx, *state) error) error {
	claim, ok := s.claimForAttempt(attemptID)
	if !ok || claim.CaseID == "" || claim.AttemptID != attemptID || claim.RunID != runID {
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
	if governance {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
			return err
		}
	}
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
	if claim.JobID != "" {
		if err := s.requireLiveClaim(ctx, tx, claim, runID, attemptID); err != nil {
			return err
		}
		if err := s.reauthorizeAttempt(ctx, tx, runID, attemptID); err != nil {
			return err
		}
	}
	previous, err := s.loadScopedAttemptState(ctx, tx, claim, runID, attemptID)
	if err != nil {
		return err
	}
	if governance {
		if err := revalidateGovernedAttemptTx(ctx, tx, runID, attemptID, claim.CaseID); err != nil {
			return err
		}
	}
	next, err := previous.clone()
	if err != nil {
		return err
	}
	if err := fn(tx, &next); err != nil {
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
	s.patchStateProjection(ctx, previous, old, changed)
	return nil
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

// loadScopedCaseState loads the case, its documents and parsed artifacts,
// activity and runs. History heads are loaded separately by the writer.
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
	materialRows, err := tx.Query(ctx, `SELECT tenant_id,org_id,case_id,material_id,role,purpose,legacy_status,legacy_selected,current_document_id,current_version,created_by,created_at
		FROM material_entities WHERE case_id=$1 AND tenant_id=$2 AND org_id=$3 ORDER BY created_at,material_id`, caseID, caseItem.TenantID, caseItem.OwnerOrgID)
	if err != nil {
		return state{}, err
	}
	for materialRows.Next() {
		var item MaterialEntity
		if err := materialRows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.ID, &item.Role, &item.Purpose, &item.LegacyStatus, &item.LegacySelected, &item.CurrentDocumentID, &item.CurrentVersion, &item.CreatedBy, &item.CreatedAt); err != nil {
			materialRows.Close()
			return state{}, err
		}
		out.Materials = append(out.Materials, item)
	}
	if err := materialRows.Err(); err != nil {
		materialRows.Close()
		return state{}, err
	}
	materialRows.Close()
	versionRows, err := tx.Query(ctx, `SELECT tenant_id,org_id,case_id,material_id,version,document_id,COALESCE(previous_document_id,''),created_by,created_at
		FROM material_versions WHERE case_id=$1 AND tenant_id=$2 AND org_id=$3 ORDER BY material_id,version`, caseID, caseItem.TenantID, caseItem.OwnerOrgID)
	if err != nil {
		return state{}, err
	}
	for versionRows.Next() {
		var item MaterialVersion
		if err := versionRows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.MaterialID, &item.Version, &item.DocumentID, &item.PreviousDocument, &item.CreatedBy, &item.CreatedAt); err != nil {
			versionRows.Close()
			return state{}, err
		}
		out.MaterialVersions = append(out.MaterialVersions, item)
	}
	if err := versionRows.Err(); err != nil {
		versionRows.Close()
		return state{}, err
	}
	versionRows.Close()
	var chain CaseMaterialChain
	var primary, legacy *string
	var selected string
	if err := tx.QueryRow(ctx, `SELECT tenant_id,org_id,case_id,primary_material_id,legacy_selected_document_id,legacy_selection_status,selection_version,selected_document_ids,created_at,updated_at FROM case_contract_chains WHERE case_id=$1 AND tenant_id=$2 AND org_id=$3`, caseID, caseItem.TenantID, caseItem.OwnerOrgID).Scan(&chain.TenantID, &chain.OrgID, &chain.CaseID, &primary, &legacy, &chain.LegacySelectionStatus, &chain.SelectionVersion, &selected, &chain.CreatedAt, &chain.UpdatedAt); err == nil {
		chain.PrimaryMaterialID, chain.LegacySelectedDocumentID = primary, legacy
		if err := json.Unmarshal([]byte(selected), &chain.SelectedDocumentIDs); err != nil {
			return state{}, err
		}
		out.MaterialChains = append(out.MaterialChains, chain)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return state{}, err
	}
	uploadRows, err := tx.Query(ctx, `SELECT tenant_id,org_id,case_id,actor_id,idempotency_key,request_hash,document_id,material_id,created_at FROM document_upload_idempotency WHERE case_id=$1 AND tenant_id=$2 AND org_id=$3`, caseID, caseItem.TenantID, caseItem.OwnerOrgID)
	if err != nil {
		return state{}, err
	}
	for uploadRows.Next() {
		var item UploadIdempotencyRecord
		if err := uploadRows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.ActorID, &item.Key, &item.RequestHash, &item.DocumentID, &item.MaterialID, &item.CreatedAt); err != nil {
			uploadRows.Close()
			return state{}, err
		}
		out.UploadIdempotency = append(out.UploadIdempotency, item)
	}
	if err := uploadRows.Err(); err != nil {
		uploadRows.Close()
		return state{}, err
	}
	uploadRows.Close()
	if len(documentIDs) > 0 {
		parsedRows, err := tx.Query(ctx, `SELECT id, selection_order, document_id, original_digest, parser_id, parser_version, config_hash, normalization_version, normalized_digest, ir_digest, artifact_digest, created_at, status, payload FROM parsed_documents WHERE document_id = ANY($1) ORDER BY seq`, documentIDs)
		if err != nil {
			return state{}, err
		}
		out.ParsedDocuments, err = decodeScopedParsedRows(parsedRows, out.Documents)
		if err != nil {
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
	for runRows.Next() {
		var item Run
		if err := runRows.Scan(&item.ID, &item.CaseID, &item.State, &item.ModelRef, &item.PartialReason, &item.FailureMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return state{}, err
		}
		out.Runs = append(out.Runs, item)
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
		if err := validateFrozenReport(item); err != nil {
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
	subjectState, err := loadSubjectCaseState(ctx, tx, caseItem.TenantID, caseItem.OwnerOrgID, caseID)
	if err != nil {
		return state{}, err
	}
	out.Subjects = subjectState.Subjects
	out.SubjectVersions = subjectState.SubjectVersions
	out.CaseSubjectRoles = subjectState.CaseSubjectRoles
	out.CaseSubjectRoleVersions = subjectState.CaseSubjectRoleVersions
	out.SubjectChangeRequests = subjectState.SubjectChangeRequests
	out.SubjectChangeDecisions = subjectState.SubjectChangeDecisions
	out.SubjectSourceSnapshots = subjectState.SubjectSourceSnapshots
	canonicalizeLoadedRows(&out)
	return out, nil
}

// scopedCaseMutation is the case-level adapter: the shared case key, a bounded
// case load, the same validation and delta persistence as the whole-state
// writer, and a projection patch limited to the bounded slices. The transaction
// scope comes from the trusted caller, exactly like the whole-state writer's.
func (s *Store) scopedCaseMutation(ctx context.Context, user accounts.User, caseID string, fn func(*state) error) error {
	return s.scopedCaseWrite(ctx, user, caseID, "", "", false, false, nil, fn)
}

// scopedCaseWrite optionally includes the case's review and business state and
// preserves the governance, decision-key and attempt fences when required.
func (s *Store) scopedCaseWrite(ctx context.Context, user accounts.User, caseID, attemptID, decisionKey string, full, governance bool, hook func(pgx.Tx) error, fn func(*state) error) (result error) {
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
	if governance {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
			return err
		}
	}
	scope := userScope(current)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return err
	}
	if decisionKey != "" {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':decision:' || jsonb_build_array($1::text,$2::text)::text,0))`, current.ID, decisionKey); err != nil {
			return err
		}
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return err
	}
	var expectedAttemptVersion *int64
	if attemptID != "" {
		if err := scopedAttemptLock(ctx, tx, attemptID); err != nil {
			return err
		}
		var currentState string
		var version int64
		if err := tx.QueryRow(ctx, `SELECT state, entity_version FROM review_attempts WHERE id=$1 AND run_id IN (SELECT id FROM review_runs WHERE case_id=$2) FOR UPDATE`, attemptID, caseID).Scan(&currentState, &version); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errAttemptMissing
			}
			return err
		}
		if !attemptActive(currentState) {
			return errAttemptTerminal
		}
		expectedAttemptVersion = &version
	}
	var previous state
	if full {
		previous, err = s.loadScopedCaseWriteState(ctx, tx, caseID, current.ID, decisionKey)
	} else {
		previous, err = s.loadScopedCaseState(ctx, tx, caseID)
	}
	if err != nil {
		return err
	}
	caseItem, ok := previous.caseByID(caseID)
	if !ok || caseItem.TenantID != scope.TenantID {
		return fault.NotFound("没有找到对应记录")
	}
	if hook != nil {
		if err := hook(tx); err != nil {
			return err
		}
	}
	scope.OrgID = caseItem.OwnerOrgID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return err
	}
	if err := loadWriteHistory(ctx, tx, &previous, []string{caseID}, attemptID); err != nil {
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
	if err := s.persist(ctx, tx, previous, next, expectedAttemptVersion); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.patchStateProjection(ctx, previous, old, changed)
	return nil
}
