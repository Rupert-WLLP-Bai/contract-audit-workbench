package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"contractaudit/internal/fault"
	"contractaudit/internal/model"
)

type countingOCRModel struct {
	configuration string
	transcript    string
	calls         atomic.Int32
}

func (m *countingOCRModel) Name() string            { return "counting-model" }
func (m *countingOCRModel) OCRName() string         { return "counting-ocr" }
func (m *countingOCRModel) ConfigurationID() string { return m.configuration }
func (m *countingOCRModel) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	return model.Warranty{Relation: "compatible", Disposition: "false_positive"}, nil
}
func (m *countingOCRModel) Transcribe(context.Context, string, []byte) (string, error) {
	m.calls.Add(1)
	return m.transcript, nil
}

type gatedOCRModel struct {
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
	transcribes atomic.Int32
}

func newGatedOCRModel() *gatedOCRModel {
	return &gatedOCRModel{entered: make(chan struct{}, 1), release: make(chan struct{})}
}
func (*gatedOCRModel) Name() string            { return "gated-model" }
func (*gatedOCRModel) OCRName() string         { return "gated-ocr" }
func (*gatedOCRModel) ConfigurationID() string { return "gated-config/v1" }
func (m *gatedOCRModel) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	m.entered <- struct{}{}
	<-m.release
	return model.Warranty{Relation: "compatible", Disposition: "false_positive"}, nil
}
func (m *gatedOCRModel) Transcribe(context.Context, string, []byte) (string, error) {
	if m.transcribes.Add(1) == 1 {
		return "图片原始质保24个月。", nil
	}
	return "重解析质保60个月。", nil
}
func (m *gatedOCRModel) unblock() { m.once.Do(func() { close(m.release) }) }

func TestParsedArtifactReparseAppendsAndChangesRunSelection(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID := createExecutionCase(t, st, admin)
	initial, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Documents) != 2 || initial.Documents[0].ParsedDocument == nil {
		t.Fatalf("initial parsed metadata missing: %+v", initial.Documents)
	}
	doc := initial.Documents[0]
	original := initial.Documents[0].ParsedDocument
	firstRunID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedID, err := st.StartReview(context.Background(), admin, caseID); err != nil || repeatedID != firstRunID {
		t.Fatalf("same parse selection was not idempotent: run=%q err=%v", repeatedID, err)
	}
	waitTerminalWorkspace(t, st, admin, caseID, firstRunID)

	if err := st.Reparse(context.Background(), admin, caseID, doc.Document.ID); err != nil {
		t.Fatal(err)
	}
	after, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	var reparsed *ParsedDocument
	for _, item := range after.Documents {
		if item.Document.ID == doc.Document.ID {
			reparsed = item.ParsedDocument
		}
	}
	if reparsed == nil || reparsed.ID == original.ID || reparsed.ArtifactDigest == original.ArtifactDigest ||
		reparsed.NormalizedDigest != original.NormalizedDigest || reparsed.Text != original.Text {
		t.Fatalf("explicit same-text reparse did not append an equivalent new artifact: old=%+v new=%+v", original, reparsed)
	}
	if !after.RevisionPending {
		t.Fatal("case did not become revision-pending after parse selection changed")
	}
	secondRunID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if secondRunID == firstRunID {
		t.Fatal("new parse selection reused the previous run")
	}
	latest := st.snapshot()
	if !contains(latest.snapshotOf(secondRunID).ParsedDocumentIDs, reparsed.ID) {
		t.Fatalf("new run snapshot omitted selected parse %q", reparsed.ID)
	}
	oldWorkspace := readWorkspace(t, st, admin, caseID, firstRunID)
	if got := parsedDocumentID(oldWorkspace.Documents, doc.Document.ID); got != original.ID {
		t.Fatalf("old attempt moved to a later parse: got %q want %q", got, original.ID)
	}
	if attempt, ok := latest.latestAttempt(firstRunID); !ok {
		t.Fatal("first run attempt missing")
	} else if projected := attemptParsedDocumentIDs(latest, attempt); !contains(projected, original.ID) || contains(projected, reparsed.ID) {
		t.Fatalf("attempt projection did not retain its exact parse refs: %v", projected)
	}
}

func TestParsedArtifactConfigurationAndDocumentIdentityAreIsolated(t *testing.T) {
	st, admin := openExecutionStore(t)
	firstModel := &countingOCRModel{configuration: "provider-config/v1", transcript: "采购文件要求质保36个月。"}
	st.UseModels(firstModel)
	caseID, err := st.CreateCase(admin, "相同原件登记", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	image := []byte("identical image bytes")
	firstID, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "same.png", "image/png", image)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "same.png", "image/png", image)
	if err != nil {
		t.Fatal(err)
	}
	firstRunID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	waitTerminalWorkspace(t, st, admin, caseID, firstRunID)
	if firstModel.calls.Load() != 2 {
		t.Fatalf("same bytes registered under two document IDs shared parser output: calls=%d", firstModel.calls.Load())
	}
	firstState := st.snapshot()
	firstArtifacts := []ParsedDocument{}
	for _, parsed := range firstState.ParsedDocuments {
		if parsed.DocumentID == firstID || parsed.DocumentID == secondID {
			firstArtifacts = append(firstArtifacts, parsed)
		}
	}
	if len(firstArtifacts) != 2 || firstArtifacts[0].DocumentID == firstArtifacts[1].DocumentID || firstArtifacts[0].ID == firstArtifacts[1].ID {
		t.Fatalf("same-byte originals do not have distinct artifacts: %+v", firstArtifacts)
	}
	if repeatedID, err := st.StartReview(context.Background(), admin, caseID); err != nil || repeatedID != firstRunID {
		t.Fatalf("unchanged parser selection was not idempotent: run=%q err=%v", repeatedID, err)
	}
	if firstModel.calls.Load() != 2 {
		t.Fatalf("idempotent start repeated OCR: calls=%d", firstModel.calls.Load())
	}

	secondModel := &countingOCRModel{configuration: "provider-config/v2", transcript: "采购文件要求质保48个月。"}
	st.UseModels(secondModel)
	secondRunID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	waitTerminalWorkspace(t, st, admin, caseID, secondRunID)
	if secondRunID == firstRunID || secondModel.calls.Load() != 2 {
		t.Fatalf("parser configuration change reused a run or output: first=%s second=%s OCR=%d", firstRunID, secondRunID, secondModel.calls.Load())
	}
	secondState := st.snapshot()
	for _, docID := range []string{firstID, secondID} {
		old, oldOK := firstState.latestParsed(docID, "")
		newer, newOK := secondState.latestParsed(docID, "")
		if !oldOK || !newOK || old.ID == newer.ID || old.ConfigHash == newer.ConfigHash || old.ArtifactDigest == newer.ArtifactDigest {
			t.Fatalf("configuration change did not append an isolated artifact for %s", docID)
		}
	}
	beforeReads := secondModel.calls.Load()
	if _, err := st.GetCase(admin, caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Workspace(admin, caseID, secondRunID); err != nil {
		t.Fatal(err)
	}
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopened.UseModels(secondModel)
	if _, err := reopened.GetCase(admin, caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Workspace(admin, caseID, secondRunID); err != nil {
		t.Fatal(err)
	}
	if secondModel.calls.Load() != beforeReads {
		t.Fatalf("historical or case reads called OCR: before=%d after=%d", beforeReads, secondModel.calls.Load())
	}
	st.UseReadOnly(true)
	if _, err := st.StartReview(context.Background(), admin, caseID); err == nil {
		t.Fatal("read-only start was accepted")
	}
	if _, err := st.GetCase(admin, caseID); err != nil {
		t.Fatal(err)
	}
	if secondModel.calls.Load() != beforeReads {
		t.Fatalf("read-only access caused implicit OCR: before=%d after=%d", beforeReads, secondModel.calls.Load())
	}
}

func TestAcceptedAttemptRemainsPinnedAcrossLaterReparse(t *testing.T) {
	st, admin := openExecutionStore(t)
	model := newGatedOCRModel()
	t.Cleanup(model.unblock)
	st.UseModels(model)
	caseID, err := st.CreateCase(admin, "受理后重解析", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	imageID, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "quote.png", "image/png", []byte("image"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。")); err != nil {
		t.Fatal(err)
	}
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, model.entered, "accepted attempt model execution")
	before := st.snapshot()
	attempt, ok := before.latestAttempt(runID)
	if !ok {
		t.Fatal("accepted attempt missing")
	}
	input, ok := before.attemptInput(attempt.ID)
	if !ok {
		t.Fatal("accepted frozen input missing")
	}
	oldParsedID := parsedDocumentIDFromFrozen(input.Documents, imageID)
	if oldParsedID == "" {
		t.Fatal("accepted input did not retain its image parse reference")
	}
	if err := st.Reparse(context.Background(), admin, caseID, imageID); err != nil {
		t.Fatal(err)
	}
	after := st.snapshot()
	latestParsed, ok := after.latestParsed(imageID, "")
	if !ok || latestParsed.ID == oldParsedID || latestParsed.Text != "重解析质保60个月。" {
		t.Fatalf("explicit reparse did not append the new source version: %+v", latestParsed)
	}
	workspace := readWorkspace(t, st, admin, caseID, runID)
	if got := parsedDocumentID(workspace.Documents, imageID); got != oldParsedID || findDocumentText(workspace.Documents, imageID) != "图片原始质保24个月。" {
		t.Fatalf("accepted attempt changed after reparse: id=%q text=%q", got, findDocumentText(workspace.Documents, imageID))
	}
	if got := attemptParsedDocumentIDs(after, attempt); !contains(got, oldParsedID) || contains(got, latestParsed.ID) {
		t.Fatalf("attempt API refs followed the later parse: %v", got)
	}
	model.unblock()
	waitTerminalWorkspace(t, st, admin, caseID, runID)
	newRunID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if newRunID == runID {
		t.Fatal("start after reparse reused the accepted run")
	}
}

func TestFailedParsedArtifactsHaveNoTextOrLocationData(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID, err := st.CreateCase(admin, "失败解析", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "empty.txt", "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	// PDF and image uploads stay pending until an explicit reparse. A corrupt
	// PDF must then persist a failed artifact with no text or coordinates.
	pdfID, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "unsupported.pdf", "application/pdf", []byte("pdf bytes"))
	if err != nil {
		t.Fatal(err)
	}
	imageID, err := st.AddDocument(context.Background(), admin, caseID, "attachment", "unavailable.png", "image/png", model.SampleWarrantyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reparse(context.Background(), admin, caseID, pdfID); err != nil {
		t.Fatal(err)
	}
	if err := st.Reparse(context.Background(), admin, caseID, imageID); err != nil {
		t.Fatal(err)
	}
	detail, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Documents) != 3 {
		t.Fatalf("expected all failed artifacts: %+v", detail.Documents)
	}
	for _, item := range detail.Documents {
		parsed := item.ParsedDocument
		if parsed == nil || parsed.Status != "failed" || parsed.NormalizedDigest != "" || parsed.IRDigest != "" ||
			parsed.Text != "" || len(parsed.IR.Blocks) != 0 || len(parsed.IR.Pages) != 0 || item.Ready || item.Text != "" {
			t.Fatalf("failed parse fabricated output: %+v", item)
		}
	}
}

func TestParsedArtifactRewriteIsRejectedAndCorruptionIsDetected(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if workspace.Execution.Attempt == nil {
		t.Fatal("attempt missing")
	}
	attemptID := workspace.Execution.Attempt.ID
	stateBefore := st.snapshot()
	inputIndex, selectionIndex := -1, -1
	for i := range stateBefore.AttemptInputs {
		if stateBefore.AttemptInputs[i].AttemptID == attemptID {
			inputIndex = i
			break
		}
	}
	for i := range stateBefore.AttemptParseSelections {
		if stateBefore.AttemptParseSelections[i].AttemptID == attemptID {
			selectionIndex = i
			break
		}
	}
	if inputIndex < 0 || selectionIndex < 0 {
		t.Fatal("frozen input or parse selection missing")
	}
	if err := st.update(func(next *state) error {
		next.AttemptInputs[inputIndex].Documents[0].Text += "rewritten"
		return nil
	}); err == nil {
		t.Fatal("in-memory frozen input rewrite was accepted")
	}
	if err := st.update(func(next *state) error {
		next.AttemptParseSelections[selectionIndex].ArtifactDigest += "rewritten"
		return nil
	}); err == nil {
		t.Fatal("in-memory parse selection rewrite was accepted")
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE review_attempt_inputs SET input_hash = input_hash WHERE attempt_id = $1`, attemptID); err == nil {
		t.Fatal("database append-only trigger allowed a frozen input rewrite")
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE review_attempt_parse_selections SET artifact_digest = artifact_digest WHERE attempt_id = $1`, attemptID); err == nil {
		t.Fatal("database append-only trigger allowed a parse selection rewrite")
	}
	parsed := st.snapshot().ParsedDocuments[0]
	if err := st.update(func(next *state) error {
		next.ParsedDocuments[0].Text += "rewritten"
		return nil
	}); err == nil {
		t.Fatal("in-memory parsed artifact rewrite was accepted")
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE parsed_documents SET payload = payload WHERE id = $1`, parsed.ID); err == nil {
		t.Fatal("database append-only trigger allowed a parsed artifact rewrite")
	}
	var payload string
	if err := st.pool.QueryRow(context.Background(), `SELECT payload FROM parsed_documents WHERE id = $1`, parsed.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	corrupt := strings.Replace(payload, parsed.Text, "tampered text", 1)
	if corrupt == payload {
		t.Fatal("test payload did not contain the artifact text")
	}
	if _, err := st.pool.Exec(context.Background(), `ALTER TABLE parsed_documents DISABLE TRIGGER parsed_documents_append_only`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE parsed_documents SET payload = $2 WHERE id = $1`, parsed.ID, corrupt); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(context.Background(), `ALTER TABLE parsed_documents ENABLE TRIGGER parsed_documents_append_only`); err != nil {
		t.Fatal(err)
	}
	detail, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range detail.Documents {
		if item.Document.ID == parsed.DocumentID && (item.ParsedDocument != nil || item.Text != "" || item.ParseError == "") {
			t.Fatalf("corrupt persisted artifact was exposed: %+v", item)
		}
	}
	if _, err := st.Reopen(); err == nil {
		t.Fatal("reopen accepted a corrupted parsed artifact")
	}
}

func TestLegacyAttemptMigrationIsRepeatableAndRetryIsRejected(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	attempt := workspace.Execution.Attempt
	if attempt == nil {
		t.Fatal("attempt missing")
	}
	if len(workspace.Findings) == 0 {
		t.Fatal("migration fixture has no finding to decide")
	}
	finding := workspace.Findings[0]
	if err := st.Decide(admin, caseID, runID, finding.ID, "g5-decision-key", "confirm_risk", "保留的历史决定", finding.Version); err != nil {
		t.Fatal(err)
	}
	beforeMigration := st.snapshot()
	input, ok := st.snapshot().attemptInput(attempt.ID)
	if !ok {
		t.Fatal("attempt input missing")
	}
	originalBodies := make([][]byte, len(input.Documents))
	for i, document := range input.Documents {
		_, body, err := st.Original(admin, caseID, document.Document.ID)
		if err != nil {
			t.Fatal(err)
		}
		originalBodies[i] = body
	}
	legacyHash := "g5-input-hash-preserved"
	for i := range input.Documents {
		input.Documents[i].ParsedDocumentID = ""
		input.Documents[i].ParsedArtifactDigest = ""
		input.Documents[i].ConfigHash = ""
		input.Documents[i].ParserID = ""
		input.Documents[i].ParserVersion = ""
		input.Documents[i].NormalizationVersion = ""
		input.Documents[i].IRDigest = ""
		input.Documents[i].ParsedCreatedAt = ""
		input.Documents[i].ParsedStatus = ""
		input.Documents[i].SelectionOrder = 0
	}
	legacyJSON, err := json.Marshal(input.Documents)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(context.Background(), `ALTER TABLE review_attempt_inputs DISABLE TRIGGER review_attempt_inputs_append_only`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE review_attempt_inputs SET input_hash = $2, documents = $3 WHERE attempt_id = $1`, attempt.ID, legacyHash, string(legacyJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(context.Background(), `ALTER TABLE review_attempt_inputs ENABLE TRIGGER review_attempt_inputs_append_only`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE review_attempts SET history_kind = 'frozen' WHERE id = $1`, attempt.ID); err != nil {
		t.Fatal(err)
	}

	for i := range beforeMigration.Snapshots {
		beforeMigration.Snapshots[i].ParsedDocumentIDs = nil
		beforeMigration.Snapshots[i].ParseSelectionHash = ""
		beforeMigration.Snapshots[i].ParserConfigurationID = ""
	}
	beforeMigration.AttemptParseSelections = []AttemptParseSelection{}
	const rowCountQuery = `SELECT (SELECT count(*) FROM cases), (SELECT count(*) FROM documents), (SELECT count(*) FROM review_runs),
		(SELECT count(*) FROM review_attempts), (SELECT count(*) FROM review_attempt_inputs), (SELECT count(*) FROM execution_events),
		(SELECT count(*) FROM findings), (SELECT count(*) FROM constraint_evaluations), (SELECT count(*) FROM decisions),
		(SELECT count(*) FROM run_events), (SELECT count(*) FROM activity), (SELECT count(*) FROM idempotency),
		(SELECT count(*) FROM review_run_snapshots), (SELECT count(*) FROM audit_events)`
	beforeCounts := make([]int64, 14)
	beforeDest := make([]any, len(beforeCounts))
	for i := range beforeCounts {
		beforeDest[i] = &beforeCounts[i]
	}
	if err := st.pool.QueryRow(context.Background(), rowCountQuery).Scan(beforeDest...); err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-parse schema only inside this isolated test schema. The
	// current P3 tables are empty here, but their exact tuple FK closes over
	// parse selections, so remove that additive closure explicitly before
	// dropping the legacy parse tables. This intentionally avoids CASCADE: the
	// subsequent reopen must recreate every current constraint from migrations.
	for _, table := range []string{
		"operator_fulfillment_evaluation_objects",
		"operator_mapping_change_decisions", "operator_mapping_change_requests", "operator_mapping_lineages",
		"service_object_contract_associations", "service_object_versions", "service_object_mutation_receipts", "service_objects",
		"experience_finding_usage", "rule_pack_shadow_reports", "review_rulepack_inputs",
		"manual_finding_records", "execution_write_receipts", "core_write_receipts",
		"clm_contracts", "clm_contract_revisions", "clm_contract_cases", "clm_approval_rounds", "clm_business_events", "clm_outbox_delivery",
		"clm_contract_drafts", "clm_contract_write_receipts", "clm_legacy_imports",
		"case_intake_requests", "case_intake_archives",
	} {
		var count int64
		if err := st.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("legacy migration fixture unexpectedly has %d rows in %s", count, table)
		}
	}
	rows, err := st.pool.Query(context.Background(), `
		SELECT rel.relname, con.conname FROM pg_constraint con
		JOIN pg_class rel ON rel.oid=con.conrelid
		WHERE con.contype='f' AND con.conrelid IN (
			'operator_fulfillment_evaluation_objects'::regclass,
			'operator_object_bindings'::regclass, 'operator_mapping_lineages'::regclass,
			'operator_mapping_change_requests'::regclass, 'operator_mapping_change_decisions'::regclass,
			'service_objects'::regclass, 'service_object_versions'::regclass,
			'service_object_contract_associations'::regclass, 'service_object_mutation_receipts'::regclass)
		ORDER BY rel.relname, con.conname`)
	if err != nil {
		t.Fatal(err)
	}
	type namedForeignKey struct{ table, name string }
	var expectedP3ForeignKeys []namedForeignKey
	for rows.Next() {
		var key namedForeignKey
		if err := rows.Scan(&key.table, &key.name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		expectedP3ForeignKeys = append(expectedP3ForeignKeys, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(expectedP3ForeignKeys) == 0 {
		t.Fatal("current fixture has no P3 foreign keys to restore")
	}
	legacyTx, err := st.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	legacyStatements := []string{
		`DROP TABLE clm_contract_drafts, clm_contract_write_receipts, clm_legacy_imports, clm_outbox_delivery, clm_business_events, clm_approval_rounds, clm_contract_cases, clm_contract_revisions, clm_contracts`,
		`DROP TABLE case_intake_requests, case_intake_archives`,
		`DROP INDEX case_intake_pending_owner`,
		`DROP TABLE experience_finding_usage, rule_pack_shadow_reports, review_rulepack_inputs, manual_finding_records, execution_write_receipts, core_write_receipts`,
		`DROP TABLE operator_fulfillment_evaluation_objects`,
		`DROP TABLE schema_migrations`,
		`ALTER TABLE operator_object_bindings DROP CONSTRAINT IF EXISTS operator_binding_mapping_lineage_fk, DROP CONSTRAINT IF EXISTS operator_binding_service_association_fk, DROP CONSTRAINT IF EXISTS operator_binding_mapping_decision_fk, DROP CONSTRAINT IF EXISTS operator_binding_mapping_pin_null_group, DROP CONSTRAINT IF EXISTS operator_binding_decision_outcome_pair`,
		`ALTER TABLE service_objects DROP CONSTRAINT IF EXISTS service_objects_current_association_fk, DROP CONSTRAINT IF EXISTS service_objects_current_version_fk`,
		`ALTER TABLE service_object_versions DROP CONSTRAINT IF EXISTS service_object_version_mapping_decision_fk, DROP CONSTRAINT IF EXISTS service_object_version_decision_outcome_pair`,
		`ALTER TABLE service_object_contract_associations DROP CONSTRAINT IF EXISTS service_object_association_mapping_decision_fk, DROP CONSTRAINT IF EXISTS service_object_association_decision_outcome_pair`,
		`DROP TABLE operator_mapping_change_decisions`,
		`DROP TABLE operator_mapping_change_requests`,
		`DROP TABLE operator_mapping_lineages`,
		`DROP TABLE service_object_contract_associations`,
		`DROP TABLE service_object_versions`,
		`DROP TABLE service_object_mutation_receipts`,
		`DROP TABLE service_objects`,
		`ALTER TABLE operator_object_bindings DROP COLUMN mapping_id, DROP COLUMN mapping_version, DROP COLUMN service_object_id, DROP COLUMN object_version, DROP COLUMN association_version, DROP COLUMN mapping_change_decision_id, DROP COLUMN mapping_change_decision, DROP COLUMN response_status, DROP COLUMN response_body`,
		`DROP TABLE review_attempt_parse_selections`,
		`DROP TABLE parsed_documents`,
		`DROP SEQUENCE parsed_selection_order_seq`,
		`ALTER TABLE review_run_snapshots DROP COLUMN parsed_document_ids, DROP COLUMN parse_selection_hash, DROP COLUMN parser_configuration_id`,
	}
	for _, statement := range legacyStatements {
		if _, err := legacyTx.Exec(context.Background(), statement); err != nil {
			_ = legacyTx.Rollback(context.Background())
			t.Fatalf("construct isolated pre-parse schema with %q: %v", statement, err)
		}
	}
	if err := legacyTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	first, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	for _, key := range expectedP3ForeignKeys {
		var restored bool
		if err := first.pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname=$1 AND contype='f' AND conrelid=to_regclass($2))`, key.name, key.table).Scan(&restored); err != nil {
			t.Fatal(err)
		}
		if !restored {
			t.Errorf("current additive FK %s.%s was not restored after legacy migration", key.table, key.name)
		}
	}
	afterCounts := make([]int64, 14)
	afterDest := make([]any, len(afterCounts))
	for i := range afterCounts {
		afterDest[i] = &afterCounts[i]
	}
	if err := first.pool.QueryRow(context.Background(), rowCountQuery).Scan(afterDest...); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeCounts, afterCounts) {
		t.Fatalf("legacy migration changed business row counts: before=%v after=%v", beforeCounts, afterCounts)
	}
	migrated, ok := first.snapshot().attemptInput(attempt.ID)
	if !ok || migrated.InputHash != legacyHash || len(migrated.Documents) != len(input.Documents) {
		t.Fatalf("legacy frozen input was not preserved: %+v", migrated)
	}
	detail, err := first.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range detail.Documents {
		if document.ParsedDocument != nil {
			t.Fatalf("legacy document received fabricated parse metadata: %+v", document.ParsedDocument)
		}
	}
	for i := range input.Documents {
		if migrated.Documents[i].Text != input.Documents[i].Text || migrated.Documents[i].OriginalHash != input.Documents[i].OriginalHash ||
			migrated.Documents[i].NormalizedHash == nil || input.Documents[i].NormalizedHash == nil || *migrated.Documents[i].NormalizedHash != *input.Documents[i].NormalizedHash {
			t.Fatalf("legacy inline text or hash changed at %d", i)
		}
		_, body, err := first.Original(admin, caseID, input.Documents[i].Document.ID)
		if err != nil || !reflect.DeepEqual(body, originalBodies[i]) {
			t.Fatalf("legacy original bytes changed at %d: %v", i, err)
		}
	}
	legacyAttempt, ok := first.snapshot().attemptByID(attempt.ID)
	if !ok || legacyAttempt.HistoryKind != "legacy_unfrozen" {
		t.Fatalf("legacy provenance was not classified: %+v", legacyAttempt)
	}
	if got := attemptParsedDocumentIDs(first.snapshot(), legacyAttempt); len(got) != 0 {
		t.Fatalf("legacy attempt fabricated parsed refs: %v", got)
	}
	afterMigration := first.snapshot()
	beforeAttempt, beforeAttemptOK := beforeMigration.attemptByID(attempt.ID)
	if !beforeAttemptOK || beforeAttempt.OutputHash == nil || legacyAttempt.OutputHash == nil || *beforeAttempt.OutputHash != *legacyAttempt.OutputHash {
		t.Fatal("legacy migration changed the terminal output identity")
	}
	for _, check := range []struct {
		name          string
		before, after any
	}{
		{"runs", beforeMigration.Runs, afterMigration.Runs},
		{"findings", beforeMigration.Findings, afterMigration.Findings},
		{"evaluations", beforeMigration.Evaluations, afterMigration.Evaluations},
		{"decisions", beforeMigration.Decisions, afterMigration.Decisions},
		{"execution events", beforeMigration.ExecutionEvents, afterMigration.ExecutionEvents},
		{"run events", beforeMigration.Events, afterMigration.Events},
		{"snapshots", beforeMigration.Snapshots, afterMigration.Snapshots},
		{"audit events", beforeMigration.Audits, afterMigration.Audits},
		{"parse selections", beforeMigration.AttemptParseSelections, afterMigration.AttemptParseSelections},
	} {
		if !reflect.DeepEqual(check.before, check.after) {
			t.Fatalf("legacy migration changed %s: before=%#v after=%#v", check.name, check.before, check.after)
		}
	}
	legacyWorkspace, err := first.Workspace(admin, caseID, runID, attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range input.Documents {
		if legacyWorkspace.Documents[i].Text != input.Documents[i].Text {
			t.Fatalf("legacy workspace text changed at %d", i)
		}
	}
	if err := first.Retry(context.Background(), admin, caseID, runID); err == nil {
		t.Fatal("legacy attempt was accepted for unsafe retry")
	}
	second, err := first.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	secondCounts := make([]int64, len(afterCounts))
	secondDest := make([]any, len(secondCounts))
	for i := range secondCounts {
		secondDest[i] = &secondCounts[i]
	}
	if err := second.pool.QueryRow(context.Background(), rowCountQuery).Scan(secondDest...); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterCounts, secondCounts) {
		t.Fatalf("repeated legacy migration changed business row counts: first=%v second=%v", afterCounts, secondCounts)
	}
	again, ok := second.snapshot().attemptInput(attempt.ID)
	againAttempt, attemptOK := second.snapshot().attemptByID(attempt.ID)
	if !ok || !attemptOK || again.InputHash != legacyHash || againAttempt.HistoryKind != "legacy_unfrozen" || len(again.Documents) != len(input.Documents) {
		t.Fatalf("repeated migration changed legacy state: input=%+v attempt=%+v", again, againAttempt)
	}
}

func TestReadOnlyStorePreservesBusinessStateAndAllowsSessions(t *testing.T) {
	st, admin := openExecutionStore(t)
	parser := &countingOCRModel{configuration: "readonly-ocr/v1", transcript: "只读时不应转写。"}
	st.UseModels(parser)
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if len(workspace.Findings) == 0 {
		t.Fatal("fixture did not produce a finding")
	}
	pendingImageID, err := st.AddDocument(context.Background(), admin, caseID, "attachment", "pending.png", "image/png", model.SampleWarrantyPNG)
	if err != nil {
		t.Fatal(err)
	}
	docID := pendingImageID
	before := st.snapshot()
	st.UseReadOnly(true)
	assertReadOnly := func(operation string, err error) {
		t.Helper()
		var apiErr *fault.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "read_only" {
			t.Fatalf("%s did not fail as read_only: %v", operation, err)
		}
	}
	_, err = st.CreateCase(admin, "拒绝新增", "procurement_ict")
	assertReadOnly("CreateCase", err)
	assertReadOnly("AddMember", st.AddMember(admin, caseID, "demo-clerk"))
	_, err = st.AddDocument(context.Background(), admin, caseID, "attachment", "new.txt", "text/plain", []byte("new document"))
	assertReadOnly("AddDocument", err)
	assertReadOnly("Reparse", st.Reparse(context.Background(), admin, caseID, docID))
	_, err = st.StartReview(context.Background(), admin, caseID)
	assertReadOnly("StartReview", err)
	assertReadOnly("Retry", st.Retry(context.Background(), admin, caseID, runID))
	assertReadOnly("CancelReview", st.CancelReview(context.Background(), admin, caseID, runID))
	finding := workspace.Findings[0]
	assertReadOnly("Decide", st.Decide(admin, caseID, runID, finding.ID, "readonly-key", "approve", "人工复核", finding.Version))

	token, user, err := st.Login("demo-admin", "admin-demo")
	if err != nil || user.ID != admin.ID {
		t.Fatalf("read-only login failed: user=%+v err=%v", user, err)
	}
	if err := st.Logout(token); err != nil {
		t.Fatalf("read-only logout failed: %v", err)
	}
	detail, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	pendingFound := false
	for _, document := range detail.Documents {
		if document.Document.ID == pendingImageID {
			pendingFound = document.ParsePending && document.ParsedDocument == nil
		}
	}
	if !pendingFound {
		t.Fatal("read-only case read did not preserve the pending parse state")
	}
	if _, err := st.Workspace(admin, caseID, runID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Original(admin, caseID, docID); err != nil {
		t.Fatal(err)
	}
	if got := parser.calls.Load(); got != 0 {
		t.Fatalf("read-only history, case, or original reads invoked OCR %d times", got)
	}
	after := st.snapshot()
	if !reflect.DeepEqual(before.Cases, after.Cases) || !reflect.DeepEqual(before.Documents, after.Documents) ||
		!reflect.DeepEqual(before.ParsedDocuments, after.ParsedDocuments) || !reflect.DeepEqual(before.Runs, after.Runs) ||
		!reflect.DeepEqual(before.Attempts, after.Attempts) || !reflect.DeepEqual(before.AttemptInputs, after.AttemptInputs) ||
		!reflect.DeepEqual(before.AttemptParseSelections, after.AttemptParseSelections) ||
		!reflect.DeepEqual(before.ExecutionEvents, after.ExecutionEvents) || !reflect.DeepEqual(before.Findings, after.Findings) ||
		!reflect.DeepEqual(before.Evaluations, after.Evaluations) || !reflect.DeepEqual(before.Decisions, after.Decisions) ||
		!reflect.DeepEqual(before.Events, after.Events) || !reflect.DeepEqual(before.Activity, after.Activity) ||
		!reflect.DeepEqual(before.Idempotency, after.Idempotency) || !reflect.DeepEqual(before.Snapshots, after.Snapshots) ||
		!reflect.DeepEqual(before.Audits, after.Audits) {
		t.Fatal("read-only actions changed business state")
	}
}

func parsedDocumentIDFromFrozen(docs []FrozenDocument, documentID string) string {
	for _, frozen := range docs {
		if frozen.Document.ID == documentID {
			return frozen.ParsedDocumentID
		}
	}
	return ""
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func TestParsedDocumentReferencesProjectFromAttemptInput(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil {
		t.Fatal("execution projection missing")
	}
	attempt := *workspace.Execution.Attempt
	input, ok := st.snapshot().attemptInput(attempt.ID)
	if !ok {
		t.Fatal("attempt input missing")
	}
	want := make([]string, 0, len(input.Documents))
	for _, frozen := range input.Documents {
		want = append(want, frozen.ParsedDocumentID)
	}
	if !reflect.DeepEqual(attempt.ParsedDocumentIDs, want) {
		t.Fatalf("attempt refs do not come from its frozen input: got %v want %v", attempt.ParsedDocumentIDs, want)
	}
	if len(workspace.Execution.Attempts) != 1 || !reflect.DeepEqual(workspace.Execution.Attempts[0].ParsedDocumentIDs, want) {
		t.Fatalf("attempt history omitted frozen parse refs: %+v", workspace.Execution.Attempts)
	}
}

func TestLegacyOCRCacheIsPreservedButIgnored(t *testing.T) {
	st, admin := openExecutionStore(t)
	model := &countingOCRModel{configuration: "cache-test/v1", transcript: "最新转写质保36个月。"}
	st.UseModels(model)
	caseID, err := st.CreateCase(admin, "旧缓存隔离", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	imageID, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "quote.png", "image/png", []byte("image bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft.txt", "text/plain", []byte("合同约定质保24个月。")); err != nil {
		t.Fatal(err)
	}
	snapshot := st.snapshot()
	var doc Document
	ok := false
	for _, candidate := range snapshot.Documents {
		if candidate.ID == imageID {
			doc, ok = candidate, true
			break
		}
	}
	if !ok {
		t.Fatal("image document missing")
	}
	cachePath := filepath.Join(st.dir, "ocr", doc.Digest+".json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacyCache := []byte(`{"text":"过期缓存质保999个月。"}`)
	if err := os.WriteFile(cachePath, legacyCache, 0o600); err != nil {
		t.Fatal(err)
	}
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if got := findDocumentText(workspace.Documents, imageID); got != model.transcript {
		t.Fatalf("attempt imported legacy OCR cache: got %q want %q", got, model.transcript)
	}
	if model.calls.Load() != 1 {
		t.Fatalf("pinned parse did not transcribe exactly once: %d", model.calls.Load())
	}
	gotCache, err := os.ReadFile(cachePath)
	if err != nil || string(gotCache) != string(legacyCache) {
		t.Fatalf("legacy OCR cache changed: bytes=%q err=%v", gotCache, err)
	}
}
