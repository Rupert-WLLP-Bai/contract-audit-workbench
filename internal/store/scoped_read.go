package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// scopedReadRows reuses the canonical row decoders with a fixed, parameterized
// predicate. These decoders issue unparameterized SELECTs ending in ORDER BY;
// fail closed if their SQL contract changes.
type scopedReadRows struct {
	tx		pgx.Tx
	predicate	string
	args		[]any
}

func (q scopedReadRows) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	order := strings.LastIndex(sql, " ORDER BY ")
	if len(args) != 0 || order < 0 || strings.Contains(sql, " UNION ") || strings.Contains(sql[:order], " WHERE ") {
		return nil, errors.New("unsupported scoped read query")
	}
	return q.tx.Query(ctx, sql[:order]+" WHERE "+q.predicate+sql[order:], q.args...)
}

func readRows(tx pgx.Tx, predicate string, args ...any) queryer {
	return scopedReadRows{tx: tx, predicate: predicate, args: args}
}

// beginCaseRead resolves the principal before reading the case and checks the
// current case grant before any case contents are fetched. The repeatable-read
// transaction keeps the authorization and the resulting projection consistent.
func (s *Store) beginCaseRead(ctx context.Context, user accounts.User, caseID string) (pgx.Tx, accounts.User, Case, error) {
	current, err := trustedUser(user)
	if err != nil {
		return nil, accounts.User{}, Case{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, accounts.User{}, Case{}, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, accounts.User{}, Case{}, err
	}
	cases, err := loadCases(ctx, readRows(tx, "id=$1", caseID))
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, accounts.User{}, Case{}, err
	}
	if len(cases) != 1 || !authorizeCase(cases[0], current, access.Read) {
		_ = tx.Rollback(ctx)
		return nil, accounts.User{}, Case{}, fault.NotFound("没有找到对应记录")
	}
	return tx, current, cases[0], nil
}

// loadCaseSummaries loads only metadata needed for the case list and summary.
// In particular, SQL extracts the parse-error bit from a frozen input instead
// of transferring its text and IR to the application.
func loadCaseSummaries(ctx context.Context, tx pgx.Tx, cases []Case, withStatus bool) (state, error) {
	st := state{Cases: cases}
	if len(cases) == 0 {
		return st, nil
	}
	caseIDs := make([]string, 0, len(cases))
	for _, item := range cases {
		caseIDs = append(caseIDs, item.ID)
	}
	var err error
	if st.Documents, err = loadDocuments(ctx, readRows(tx, "case_id=ANY($1)", caseIDs)); err != nil {
		return state{}, err
	}
	if st.Runs, err = loadRuns(ctx, readRows(tx, "case_id=ANY($1)", caseIDs)); err != nil {
		return state{}, err
	}
	runIDs := make([]string, 0, len(st.Runs))
	for _, run := range st.Runs {
		runIDs = append(runIDs, run.ID)
	}
	if len(runIDs) != 0 && withStatus {
		rows, err := tx.Query(ctx, `SELECT id,run_id,ordinal,state,error_code,history_kind,accepted_at,started_at,finished_at FROM review_attempts WHERE run_id=ANY($1) ORDER BY run_id,ordinal`, runIDs)
		if err != nil {
			return state{}, err
		}
		for rows.Next() {
			var item Attempt
			if err := rows.Scan(&item.ID, &item.RunID, &item.Ordinal, &item.State, &item.ErrorCode, &item.HistoryKind, &item.AcceptedAt, &item.StartedAt, &item.FinishedAt); err != nil {
				rows.Close()
				return state{}, err
			}
			st.Attempts = append(st.Attempts, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return state{}, err
		}
		rows.Close()
		latestIDs := make([]string, 0, len(runIDs))
		for _, runID := range runIDs {
			if attempt, ok := st.latestAttempt(runID); ok {
				latestIDs = append(latestIDs, attempt.ID)
			}
		}
		if len(latestIDs) != 0 {
			rows, err = tx.Query(ctx, `SELECT attempt_id,run_id,EXISTS (
				SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(documents::jsonb)='null' THEN '[]'::jsonb ELSE documents::jsonb END) AS document
				WHERE COALESCE(document->>'parse_error','') <> ''
			) FROM review_attempt_inputs WHERE attempt_id=ANY($1)`, latestIDs)
			if err != nil {
				return state{}, err
			}
			for rows.Next() {
				var item AttemptInput
				var failed bool
				if err := rows.Scan(&item.AttemptID, &item.RunID, &failed); err != nil {
					rows.Close()
					return state{}, err
				}
				if failed {
					item.Documents = []FrozenDocument{{ParseError: "failed"}}
				}
				st.AttemptInputs = append(st.AttemptInputs, item)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return state{}, err
			}
			rows.Close()
		}
		rows, err = tx.Query(ctx, `SELECT id,run_id,attempt_id,case_id,relation,version,severity FROM findings WHERE case_id=ANY($1) ORDER BY seq`, caseIDs)
		if err != nil {
			return state{}, err
		}
		for rows.Next() {
			var item Finding
			if err := rows.Scan(&item.ID, &item.RunID, &item.AttemptID, &item.CaseID, &item.Relation, &item.Version, &item.Severity); err != nil {
				rows.Close()
				return state{}, err
			}
			st.Findings = append(st.Findings, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return state{}, err
		}
		rows.Close()
	}
	rows, err := tx.Query(ctx, `SELECT finding_id,case_id,created_at FROM decisions WHERE case_id=ANY($1) ORDER BY seq`, caseIDs)
	if err != nil {
		return state{}, err
	}
	for rows.Next() {
		var item Decision
		if err := rows.Scan(&item.FindingID, &item.CaseID, &item.CreatedAt); err != nil {
			rows.Close()
			return state{}, err
		}
		st.Decisions = append(st.Decisions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return state{}, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id,case_id,occurred_at,summary FROM activity WHERE case_id=ANY($1)
		UNION ALL SELECT id,case_id,occurred_at,summary FROM document_activity WHERE case_id=ANY($1)
		UNION ALL SELECT id,case_id,occurred_at,summary FROM execution_activity WHERE case_id=ANY($1)
		ORDER BY occurred_at,id`, caseIDs)
	if err != nil {
		return state{}, err
	}
	for rows.Next() {
		var item Activity
		if err := rows.Scan(&item.ID, &item.CaseID, &item.At, &item.Summary); err != nil {
			rows.Close()
			return state{}, err
		}
		st.Activity = append(st.Activity, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return state{}, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT p.document_id,MAX(p.created_at) FROM parsed_documents p JOIN documents d ON d.id=p.document_id
		WHERE d.case_id=ANY($1) GROUP BY p.document_id`, caseIDs)
	if err != nil {
		return state{}, err
	}
	for rows.Next() {
		var item ParsedDocument
		if err := rows.Scan(&item.DocumentID, &item.CreatedAt); err != nil {
			rows.Close()
			return state{}, err
		}
		st.ParsedDocuments = append(st.ParsedDocuments, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return state{}, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT case_id,created_at FROM business_generations WHERE case_id=ANY($1)`, caseIDs)
	if err != nil {
		return state{}, err
	}
	for rows.Next() {
		var item generationRecord
		if err := rows.Scan(&item.CaseID, &item.Record.CreatedAt); err != nil {
			rows.Close()
			return state{}, err
		}
		st.Generations = append(st.Generations, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return state{}, err
	}
	rows.Close()
	applyReadProjections(&st)
	return st, nil
}

func (s *Store) snapshotCaseList(ctx context.Context, user accounts.User) (state, error) {
	current, err := trustedUser(user)
	if err != nil {
		return state{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return state{}, err
	}
	cases, err := loadCases(ctx, tx)
	if err != nil {
		return state{}, err
	}
	visible := make([]Case, 0, len(cases))
	for _, item := range cases {
		if authorizeCase(item, current, access.Read) {
			visible = append(visible, item)
		}
	}
	st, err := loadCaseSummaries(ctx, tx, visible, true)
	if err != nil {
		return state{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return st, nil
}

func (s *Store) snapshotCaseDetail(ctx context.Context, user accounts.User, caseID string) (state, error) {
	tx, _, item, err := s.beginCaseRead(ctx, user, caseID)
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	st, err := loadCaseSummaries(ctx, tx, []Case{item}, true)
	if err != nil {
		return state{}, err
	}
	q := readRows(tx, "case_id=$1", caseID)
	if st.Materials, st.MaterialVersions, st.MaterialChains, err = loadMaterialState(ctx, q); err != nil {
		return state{}, err
	}
	if st.Generations, err = loadBusinessGenerations(ctx, readRows(tx, "g.case_id=$1", caseID)); err != nil {
		return state{}, err
	}
	if st.Remediations, err = loadBusinessRemediations(ctx, q); err != nil {
		return state{}, err
	}
	if st.Archives, err = loadBusinessArchives(ctx, q); err != nil {
		return state{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,org_id,case_id,run_id,attempt_id,version,input_hash,output_hash,decision_snapshot_hash,digest,created_at FROM business_reports WHERE case_id=$1 ORDER BY seq`, caseID)
	if err != nil {
		return state{}, err
	}
	for rows.Next() {
		var report frozenReport
		if err := rows.Scan(&report.Record.ID, &report.TenantID, &report.OrgID, &report.CaseID, &report.Record.RunID, &report.Record.AttemptID, &report.Record.Version, &report.Record.InputHash, &report.Record.OutputHash, &report.Record.DecisionSnapshotHash, &report.Record.Digest, &report.Record.CreatedAt); err != nil {
			rows.Close()
			return state{}, err
		}
		st.Reports = append(st.Reports, report)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return state{}, err
	}
	rows.Close()
	subjects, err := loadSubjectCaseState(ctx, tx, item.TenantID, item.OwnerOrgID, caseID)
	if err != nil {
		return state{}, err
	}
	st.Subjects, st.SubjectVersions = subjects.Subjects, subjects.SubjectVersions
	st.CaseSubjectRoles, st.CaseSubjectRoleVersions = subjects.CaseSubjectRoles, subjects.CaseSubjectRoleVersions
	st.SubjectChangeRequests, st.SubjectChangeDecisions = subjects.SubjectChangeRequests, subjects.SubjectChangeDecisions
	st.SubjectSourceSnapshots = subjects.SubjectSourceSnapshots
	if run, ok := st.latestRun(caseID); ok {
		if st.Snapshots, err = loadSnapshots(ctx, readRows(tx, "run_id=$1", run.ID)); err != nil {
			return state{}, err
		}
		if attempt, ok := st.latestAttempt(run.ID); ok {
			if st.AttemptParseSelections, err = loadAttemptParseSelections(ctx, readRows(tx, "attempt_id=$1", attempt.ID)); err != nil {
				return state{}, err
			}
		}
	}
	docIDs := make([]string, 0, len(st.Documents))
	for _, doc := range st.Documents {
		docIDs = append(docIDs, doc.ID)
	}
	st.ParsedDocuments = nil
	if len(docIDs) != 0 {
		st.ParsedDocuments, err = loadParsedDocuments(ctx, readRows(tx, `id IN (SELECT DISTINCT ON (document_id) id FROM parsed_documents WHERE document_id=ANY($1) ORDER BY document_id,selection_order DESC,seq ASC)`, docIDs), st.Documents, false)
		if err != nil {
			return state{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return st, nil
}

// interpretLoaded uses the canonical artifact already checked against its
// stored columns and document by loadParsedDocuments in the same transaction.
func interpretLoaded(st state, docs []Document) []Interpreted {
	out := make([]Interpreted, len(docs))
	for i, doc := range docs {
		parsed, ok := st.latestParsed(doc.ID, "")
		if !ok {
			out[i] = Interpreted{Document: doc, ParsePending: true}
		} else if err := verifyParsed(parsed, doc); err != nil {
			out[i] = Interpreted{Document: doc, ParseError: "已保存解析产物完整性校验失败：" + err.Error()}
		} else {
			out[i] = parsedInterpreted(doc, parsed)
		}
	}
	return out
}

// readAttemptState reads the immutable execution identity and its own events and
// results. Full input and parsed payloads are loaded only for consumers that
// need to prepare or verify evidence.
func (s *Store) readAttemptState(ctx context.Context, claim JobClaim, withInput bool) (state, error) {
	scope, attemptID := claim.scope(), claim.AttemptID
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return state{}, err
	}
	var st state
	if claim.JobID != "" {
		st.Jobs, err = loadJobs(ctx, readRows(tx, `id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND run_id=$5 AND attempt_id=$6
			AND actor_id=$7 AND worker_id=$8 AND generation=$9 AND state='running' AND lease_until > clock_timestamp()`,
			claim.JobID, claim.TenantID, claim.OrgID, claim.CaseID, claim.RunID, claim.AttemptID,
			claim.ActorID, claim.WorkerID, claim.Generation))
		if err != nil {
			return state{}, err
		}
		if len(st.Jobs) != 1 {
			return state{}, errLeaseLost
		}
	}
	if st.Attempts, err = loadAttempts(ctx, readRows(tx, "id=$1", attemptID)); err != nil {
		return state{}, err
	}
	if len(st.Attempts) != 1 {
		return state{}, errAttemptMissing
	}
	attempt := st.Attempts[0]
	if st.Runs, err = loadRuns(ctx, readRows(tx, "id=$1", attempt.RunID)); err != nil {
		return state{}, err
	}
	if len(st.Runs) != 1 || (scope.CaseID != "" && st.Runs[0].CaseID != scope.CaseID) {
		return state{}, errAttemptMissing
	}
	if st.Cases, err = loadCases(ctx, readRows(tx, "id=$1", st.Runs[0].CaseID)); err != nil {
		return state{}, err
	}
	if len(st.Cases) != 1 || (scope.TenantID != "" && st.Cases[0].TenantID != scope.TenantID) || (scope.OrgID != "" && st.Cases[0].OwnerOrgID != scope.OrgID) {
		return state{}, errExecutionEventScope
	}
	if st.Snapshots, err = loadSnapshots(ctx, readRows(tx, "run_id=$1", attempt.RunID)); err != nil {
		return state{}, err
	}
	if err := validateGovernedAttemptSnapshot(attempt, st.snapshotOf(attempt.RunID)); err != nil {
		return state{}, fmt.Errorf("load governed attempt %q: %w", attempt.ID, err)
	}
	if st.Findings, err = loadFindings(ctx, readRows(tx, "attempt_id=$1", attemptID)); err != nil {
		return state{}, err
	}
	if st.Evaluations, err = loadEvaluations(ctx, readRows(tx, "attempt_id=$1", attemptID)); err != nil {
		return state{}, err
	}
	if st.ExecutionEvents, err = loadExecutionEvents(ctx, readRows(tx, "attempt_id=$1", attemptID)); err != nil {
		return state{}, err
	}
	if withInput {
		// Retry recovery consumes the immediately preceding attempt's event
		// history and immutable identity, but not its input or parse payloads.
		if attempt.RetryOfAttemptID != nil {
			prior, err := loadAttempts(ctx, readRows(tx, "id=$1 AND run_id=$2", *attempt.RetryOfAttemptID, attempt.RunID))
			if err != nil {
				return state{}, err
			}
			if len(prior) != 0 {
				if err := validateGovernedAttemptSnapshot(prior[0], st.snapshotOf(attempt.RunID)); err != nil {
					return state{}, fmt.Errorf("load governed attempt %q: %w", prior[0].ID, err)
				}
				st.Attempts = append(st.Attempts, prior[0])
				events, err := loadExecutionEvents(ctx, readRows(tx, "attempt_id=$1", prior[0].ID))
				if err != nil {
					return state{}, err
				}
				st.ExecutionEvents = append(st.ExecutionEvents, events...)
			}
		}
		if st.AttemptInputs, err = loadAttemptInputs(ctx, readRows(tx, "attempt_id=$1", attemptID)); err != nil {
			return state{}, err
		}
		if st.AttemptParseSelections, err = loadAttemptParseSelections(ctx, readRows(tx, "attempt_id=$1", attemptID)); err != nil {
			return state{}, err
		}
		docIDs := append([]string(nil), st.snapshotOf(attempt.RunID).DocumentIDs...)
		parsedIDs := append([]string(nil), st.snapshotOf(attempt.RunID).ParsedDocumentIDs...)
		for _, selection := range st.AttemptParseSelections {
			docIDs = append(docIDs, selection.DocumentID)
			parsedIDs = append(parsedIDs, selection.ParsedDocumentID)
		}
		for _, input := range st.AttemptInputs {
			for _, doc := range input.Documents {
				docIDs = append(docIDs, doc.Document.ID)
				parsedIDs = append(parsedIDs, doc.ParsedDocumentID)
			}
		}
		if len(st.AttemptInputs) == 0 && len(docIDs) != 0 {
			if st.Materials, st.MaterialVersions, err = loadAttemptMaterialReferences(ctx, tx, st.Runs[0].CaseID, docIDs); err != nil {
				return state{}, err
			}
		}
		if len(docIDs) != 0 {
			if st.Documents, err = loadDocuments(ctx, readRows(tx, "case_id=$1 AND id=ANY($2)", st.Runs[0].CaseID, docIDs)); err != nil {
				return state{}, err
			}
		}
		if len(parsedIDs) != 0 && len(st.Documents) != 0 {
			authorizedDocIDs := make([]string, 0, len(st.Documents))
			for _, doc := range st.Documents {
				authorizedDocIDs = append(authorizedDocIDs, doc.ID)
			}
			if st.ParsedDocuments, err = loadParsedDocuments(ctx, readRows(tx, "id=ANY($1) AND document_id=ANY($2)", parsedIDs, authorizedDocIDs), st.Documents, false); err != nil {
				return state{}, err
			}
		}
	}
	canonicalizeLoadedRows(&st)
	st.normalize()
	if err := tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return st, nil
}

// unionReadRows filters the already-merged service timelines without changing
// their ordering or excluding either service's historical records.
type unionReadRows struct {
	tx		pgx.Tx
	predicate	string
	order		string
	args		[]any
}

func (q unionReadRows) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if len(args) != 0 || !strings.Contains(sql, " UNION ALL ") {
		return nil, errors.New("unsupported scoped timeline query")
	}
	return q.tx.Query(ctx, "SELECT * FROM ("+sql+") AS timeline WHERE "+q.predicate+" ORDER BY "+q.order, q.args...)
}

// loadInputMetadata retains parse identities for the attempt selector while
// keeping frozen text and IR on the database side for unselected attempts.
func loadInputMetadata(ctx context.Context, tx pgx.Tx, attemptIDs, excludeIDs []string) ([]AttemptInput, error) {
	rows, err := tx.Query(ctx, `SELECT attempt_id,run_id,input_hash,frozen_at,
		COALESCE((SELECT jsonb_agg(document - 'text' - 'ir' ORDER BY ord)
			FROM jsonb_array_elements(CASE WHEN jsonb_typeof(documents::jsonb)='null' THEN '[]'::jsonb ELSE documents::jsonb END)
			WITH ORDINALITY AS entry(document,ord)), '[]'::jsonb)::text
		FROM review_attempt_inputs WHERE attempt_id=ANY($1) AND NOT (attempt_id=ANY($2)) ORDER BY attempt_id`, attemptIDs, excludeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttemptInput
	for rows.Next() {
		var item AttemptInput
		var raw string
		if err := rows.Scan(&item.AttemptID, &item.RunID, &item.InputHash, &item.FrozenAt, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &item.Documents); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// snapshotWorkspace loads the selected attempt and the previous run needed for
// comparison. Other attempts contribute only selector and usage metadata.
func (s *Store) snapshotWorkspace(ctx context.Context, user accounts.User, caseID, runID, selectedID string) (state, error) {
	tx, _, item, err := s.beginCaseRead(ctx, user, caseID)
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	st, err := loadCaseSummaries(ctx, tx, []Case{item}, false)
	if err != nil {
		return state{}, err
	}
	run, ok := st.runByID(runID)
	if !ok || run.CaseID != caseID {
		return state{}, fault.NotFound("没有找到对应记录")
	}
	previousID := ""
	if previous, ok := st.previousRun(run); ok {
		previousID = previous.ID
	}
	st.Attempts, err = loadAttempts(ctx, readRows(tx, `run_id=$1 OR (run_id=$2 AND ordinal=(SELECT MAX(ordinal) FROM review_attempts WHERE run_id=$2))`, runID, previousID))
	if err != nil {
		return state{}, err
	}
	if selectedID == "" {
		if attempt, ok := st.latestAttempt(runID); ok {
			selectedID = attempt.ID
		}
	}
	fullAttemptIDs := make([]string, 0, 2)
	if attempt, ok := st.attemptByID(selectedID); ok && attempt.RunID == runID {
		fullAttemptIDs = append(fullAttemptIDs, attempt.ID)
	}
	if attempt, ok := st.latestAttempt(previousID); ok {
		fullAttemptIDs = append(fullAttemptIDs, attempt.ID)
	}
	runIDs := []string{runID}
	if previousID != "" {
		runIDs = append(runIDs, previousID)
	}
	if st.Snapshots, err = loadSnapshots(ctx, readRows(tx, "run_id=ANY($1)", runIDs)); err != nil {
		return state{}, err
	}
	for _, attempt := range st.Attempts {
		if err := validateGovernedAttemptSnapshot(attempt, st.snapshotOf(attempt.RunID)); err != nil {
			return state{}, fmt.Errorf("load governed attempt %q: %w", attempt.ID, err)
		}
	}
	allAttemptIDs := make([]string, 0, len(st.Attempts))
	for _, attempt := range st.Attempts {
		allAttemptIDs = append(allAttemptIDs, attempt.ID)
	}
	st.AttemptInputs = nil
	st.AttemptParseSelections = nil
	if len(fullAttemptIDs) != 0 {
		if st.AttemptInputs, err = loadAttemptInputs(ctx, readRows(tx, "attempt_id=ANY($1)", fullAttemptIDs)); err != nil {
			return state{}, err
		}
		if st.AttemptParseSelections, err = loadAttemptParseSelections(ctx, readRows(tx, "attempt_id=ANY($1)", fullAttemptIDs)); err != nil {
			return state{}, err
		}
	}
	if len(allAttemptIDs) != 0 {
		metadata, err := loadInputMetadata(ctx, tx, allAttemptIDs, fullAttemptIDs)
		if err != nil {
			return state{}, err
		}
		st.AttemptInputs = append(st.AttemptInputs, metadata...)
	}
	var allParsedIDs, fullParsedIDs []string
	for _, input := range st.AttemptInputs {
		full := false
		for _, id := range fullAttemptIDs {
			full = full || id == input.AttemptID
		}
		for _, doc := range input.Documents {
			if doc.ParsedDocumentID != "" {
				allParsedIDs = append(allParsedIDs, doc.ParsedDocumentID)
				if full {
					fullParsedIDs = append(fullParsedIDs, doc.ParsedDocumentID)
				}
			}
		}
	}
	for _, snapshot := range st.Snapshots {
		fullParsedIDs = append(fullParsedIDs, snapshot.ParsedDocumentIDs...)
	}
	for _, selection := range st.AttemptParseSelections {
		fullParsedIDs = append(fullParsedIDs, selection.ParsedDocumentID)
	}
	st.ParsedDocuments = nil
	if len(allParsedIDs) != 0 {
		rows, err := tx.Query(ctx, `SELECT p.id,p.document_id,p.artifact_digest FROM parsed_documents p JOIN documents d ON d.id=p.document_id WHERE p.id=ANY($1) AND d.case_id=$2`, allParsedIDs, caseID)
		if err != nil {
			return state{}, err
		}
		for rows.Next() {
			var parsed ParsedDocument
			if err := rows.Scan(&parsed.ID, &parsed.DocumentID, &parsed.ArtifactDigest); err != nil {
				rows.Close()
				return state{}, err
			}
			st.ParsedDocuments = append(st.ParsedDocuments, parsed)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return state{}, err
		}
		rows.Close()
	}
	if len(fullParsedIDs) != 0 {
		docIDs := make([]string, 0, len(st.Documents))
		for _, doc := range st.Documents {
			docIDs = append(docIDs, doc.ID)
		}
		parsed, err := loadParsedDocuments(ctx, readRows(tx, "id=ANY($1) AND document_id=ANY($2)", fullParsedIDs, docIDs), st.Documents, false)
		if err != nil {
			return state{}, err
		}
		st.ParsedDocuments = append(st.ParsedDocuments, parsed...)
	}
	st.ExecutionEvents = nil
	if len(allAttemptIDs) != 0 {
		if st.ExecutionEvents, err = loadExecutionEvents(ctx, readRows(tx, "attempt_id=ANY($1)", allAttemptIDs)); err != nil {
			return state{}, err
		}
	}
	if st.Findings, err = loadFindings(ctx, readRows(tx, "attempt_id=ANY($1) OR (attempt_id='' AND run_id=ANY($2))", fullAttemptIDs, runIDs)); err != nil {
		return state{}, err
	}
	if st.Evaluations, err = loadEvaluations(ctx, readRows(tx, "attempt_id=ANY($1) OR (attempt_id='' AND run_id=ANY($2))", fullAttemptIDs, runIDs)); err != nil {
		return state{}, err
	}
	findingIDs := make([]string, 0, len(st.Findings))
	for _, finding := range st.Findings {
		findingIDs = append(findingIDs, finding.ID)
	}
	st.Decisions = nil
	if len(findingIDs) != 0 {
		if st.Decisions, err = loadDecisions(ctx, readRows(tx, "finding_id=ANY($1)", findingIDs)); err != nil {
			return state{}, err
		}
	}
	if st.Audits, err = loadAudits(ctx, unionReadRows{tx: tx, predicate: "run_id=$1", order: "seq,event_id", args: []any{runID}}); err != nil {
		return state{}, err
	}
	applyReadProjections(&st)
	canonicalizeLoadedRows(&st)
	st.normalize()
	if err := tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return st, nil
}

func (s *Store) snapshotCaseOriginal(ctx context.Context, user accounts.User, caseID, docID string) (state, error) {
	tx, _, item, err := s.beginCaseRead(ctx, user, caseID)
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	documents, err := loadDocuments(ctx, readRows(tx, "case_id=$1 AND id=$2", caseID, docID))
	if err != nil {
		return state{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return state{Cases: []Case{item}, Documents: documents}, nil
}

func (s *Store) snapshotCaseReport(ctx context.Context, user accounts.User, caseID, reportID string) (state, error) {
	tx, current, item, err := s.beginCaseRead(ctx, user, caseID)
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireCaseAction(item, current, access.Export); err != nil {
		return state{}, err
	}
	reports, err := loadBusinessReports(ctx, readRows(tx, "case_id=$1 AND id=$2", caseID, reportID))
	if err != nil {
		return state{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return state{Cases: []Case{item}, Reports: reports}, nil
}

// loadReadinessCaseState retains the newest parse for every parser
// configuration so readiness can distinguish a current artifact from a stale
// one without loading unrelated cases or older versions of the same parser.
func loadReadinessCaseState(ctx context.Context, tx pgx.Tx, item Case) (state, error) {
	st := state{Cases: []Case{item}}
	var err error
	if st.Documents, err = loadDocuments(ctx, readRows(tx, "case_id=$1", item.ID)); err != nil {
		return state{}, err
	}
	if st.Materials, st.MaterialVersions, st.MaterialChains, err = loadMaterialState(ctx, readRows(tx, "case_id=$1", item.ID)); err != nil {
		return state{}, err
	}
	docIDs := make([]string, 0, len(st.Documents))
	for _, doc := range st.Documents {
		docIDs = append(docIDs, doc.ID)
	}
	if len(docIDs) != 0 {
		st.ParsedDocuments, err = loadParsedDocuments(ctx, readRows(tx, `id IN (SELECT DISTINCT ON (document_id,config_hash) id FROM parsed_documents WHERE document_id=ANY($1) ORDER BY document_id,config_hash,selection_order DESC,seq ASC)`, docIDs), st.Documents, false)
		if err != nil {
			return state{}, err
		}
	}
	return st, nil
}

// snapshotWorkbench assembles the dashboard from authorized case metadata.
// Frozen review text, report bodies and parsed payloads are not transferred.
func (s *Store) snapshotWorkbench(ctx context.Context, user accounts.User) (state, error) {
	current, err := trustedUser(user)
	if err != nil {
		return state{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return state{}, err
	}
	cases, err := loadCases(ctx, tx)
	if err != nil {
		return state{}, err
	}
	visible := make([]Case, 0, len(cases))
	caseIDs := make([]string, 0, len(cases))
	for _, item := range cases {
		if authorizeCase(item, current, access.Read) {
			visible = append(visible, item)
			caseIDs = append(caseIDs, item.ID)
		}
	}
	st, err := loadCaseSummaries(ctx, tx, visible, true)
	if err != nil {
		return state{}, err
	}
	q := readRows(tx, "case_id=ANY($1)", caseIDs)
	if st.Materials, st.MaterialVersions, st.MaterialChains, err = loadMaterialState(ctx, q); err != nil {
		return state{}, err
	}
	if st.Remediations, err = loadBusinessRemediations(ctx, q); err != nil {
		return state{}, err
	}
	if st.Profiles, err = loadWorkbenchProfiles(ctx, q); err != nil {
		return state{}, err
	}
	if st.Tasks, err = loadWorkbenchTasks(ctx, q); err != nil {
		return state{}, err
	}
	if st.Relations, err = loadWorkbenchRelations(ctx, q); err != nil {
		return state{}, err
	}
	if st.WorkbenchAudit, err = loadWorkbenchAudit(ctx, readRows(tx, "case_id IS NULL OR case_id=ANY($1)", caseIDs)); err != nil {
		return state{}, err
	}
	if st.Knowledge, err = loadKnowledge(ctx, tx); err != nil {
		return state{}, err
	}
	if st.Decisions, err = loadDecisions(ctx, q); err != nil {
		return state{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,org_id,case_id,run_id,attempt_id,version,input_hash,output_hash,decision_snapshot_hash,digest,created_at FROM business_reports WHERE case_id=ANY($1) ORDER BY seq`, caseIDs)
	if err != nil {
		return state{}, err
	}
	for rows.Next() {
		var report frozenReport
		if err := rows.Scan(&report.Record.ID, &report.TenantID, &report.OrgID, &report.CaseID, &report.Record.RunID, &report.Record.AttemptID, &report.Record.Version, &report.Record.InputHash, &report.Record.OutputHash, &report.Record.DecisionSnapshotHash, &report.Record.Digest, &report.Record.CreatedAt); err != nil {
			rows.Close()
			return state{}, err
		}
		st.Reports = append(st.Reports, report)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return state{}, err
	}
	rows.Close()
	// The latest row's identity is enough for the dashboard. A malformed or
	// digest-mismatched payload exposes no parse-error text, matching the
	// unverified placeholder used by lenient reads.
	rows, err = tx.Query(ctx, `SELECT DISTINCT ON (p.document_id)
		p.id,p.selection_order,p.document_id,p.original_digest,p.parser_id,p.parser_version,p.config_hash,
		p.normalization_version,COALESCE(p.normalized_digest,''),COALESCE(p.ir_digest,''),p.artifact_digest,p.created_at,p.status,
		CASE WHEN encode(sha256(convert_to(p.payload,'UTF8')),'hex')=p.artifact_digest AND pg_input_is_valid(p.payload,'jsonb')
		THEN COALESCE((p.payload::jsonb)->>'parse_error','') ELSE '' END
		FROM parsed_documents p JOIN documents d ON d.id=p.document_id WHERE d.case_id=ANY($1)
		ORDER BY p.document_id,p.selection_order DESC,p.seq ASC`, caseIDs)
	if err != nil {
		return state{}, err
	}
	st.ParsedDocuments = nil
	for rows.Next() {
		var parsed ParsedDocument
		if err := rows.Scan(&parsed.ID, &parsed.SelectionOrder, &parsed.DocumentID, &parsed.OriginalDigest, &parsed.ParserID, &parsed.ParserVersion, &parsed.ConfigHash, &parsed.NormalizationVersion, &parsed.NormalizedDigest, &parsed.IRDigest, &parsed.ArtifactDigest, &parsed.CreatedAt, &parsed.Status, &parsed.ParseError); err != nil {
			rows.Close()
			return state{}, err
		}
		st.ParsedDocuments = append(st.ParsedDocuments, parsed)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return state{}, err
	}
	rows.Close()
	latestIDs := make([]string, 0, len(st.Runs))
	for _, run := range st.Runs {
		if attempt, ok := st.latestAttempt(run.ID); ok {
			latestIDs = append(latestIDs, attempt.ID)
		}
	}
	if len(latestIDs) != 0 {
		rows, err = tx.Query(ctx, `SELECT attempt_id,event_seq,node_kind,event_type,usage FROM execution_events WHERE attempt_id=ANY($1) AND node_kind='model_call' AND event_type IN ('model_completed','model_failed') ORDER BY attempt_id,event_seq`, latestIDs)
		if err != nil {
			return state{}, err
		}
		for rows.Next() {
			var event ExecutionEvent
			var raw *string
			if err := rows.Scan(&event.AttemptID, &event.Seq, &event.NodeKind, &event.EventType, &raw); err != nil {
				rows.Close()
				return state{}, err
			}
			if raw != nil {
				var usage UsageObservation
				if err := json.Unmarshal([]byte(*raw), &usage); err != nil {
					rows.Close()
					return state{}, err
				}
				event.Usage = &usage
			}
			st.ExecutionEvents = append(st.ExecutionEvents, event)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return state{}, err
		}
		rows.Close()
	}
	applyReadProjections(&st)
	if err := tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return st, nil
}

// Preparation binds only the selected documents' material versions to the
// frozen input. Once an input exists its material identity is already frozen.
func loadAttemptMaterialReferences(ctx context.Context, tx pgx.Tx, caseID string, docIDs []string) ([]MaterialEntity, []MaterialVersion, error) {
	rows, err := tx.Query(ctx, `SELECT tenant_id,org_id,case_id,material_id,version,document_id,COALESCE(previous_document_id,''),created_by,created_at
		FROM material_versions WHERE case_id=$1 AND document_id=ANY($2) ORDER BY material_id,version`, caseID, docIDs)
	if err != nil {
		return nil, nil, err
	}
	var versions []MaterialVersion
	var materialIDs []string
	for rows.Next() {
		var item MaterialVersion
		if err := rows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.MaterialID, &item.Version, &item.DocumentID, &item.PreviousDocument, &item.CreatedBy, &item.CreatedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		versions = append(versions, item)
		materialIDs = append(materialIDs, item.MaterialID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	if len(materialIDs) == 0 {
		return nil, versions, nil
	}
	rows, err = tx.Query(ctx, `SELECT tenant_id,org_id,case_id,material_id,role,purpose,legacy_status,legacy_selected,current_document_id,current_version,created_by,created_at
		FROM material_entities WHERE case_id=$1 AND material_id=ANY($2) ORDER BY created_at,material_id`, caseID, materialIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var materials []MaterialEntity
	for rows.Next() {
		var item MaterialEntity
		if err := rows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.ID, &item.Role, &item.Purpose, &item.LegacyStatus, &item.LegacySelected, &item.CurrentDocumentID, &item.CurrentVersion, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, nil, err
		}
		materials = append(materials, item)
	}
	return materials, versions, rows.Err()
}
