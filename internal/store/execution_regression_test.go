package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/model"
)

type heldReviewModel struct {
	entered  chan struct{}
	release  chan struct{}
	returned chan struct{}
	once     sync.Once
	calls    atomic.Int32
}

func newHeldReviewModel(capacity int) *heldReviewModel {
	return &heldReviewModel{
		entered: make(chan struct{}, capacity), release: make(chan struct{}),
		returned: make(chan struct{}, capacity),
	}
}

func (m *heldReviewModel) Name() string            { return "held-model" }
func (m *heldReviewModel) ConfigurationID() string { return m.Name() }

func (m *heldReviewModel) Warranty(_ context.Context, _ []model.Material) (model.Warranty, error) {
	m.calls.Add(1)
	m.entered <- struct{}{}
	<-m.release
	m.returned <- struct{}{}
	return model.Warranty{Relation: "compatible", Disposition: "false_positive"}, nil
}

func (*heldReviewModel) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

func (m *heldReviewModel) unblock() { m.once.Do(func() { close(m.release) }) }

type frozenOCRModel struct {
	transcriptMu sync.RWMutex
	transcript   string
	transcribes  atomic.Int32
	completions  atomic.Int32
}

func (m *frozenOCRModel) Name() string            { return "frozen-chat-model" }
func (m *frozenOCRModel) OCRName() string         { return "frozen-ocr-model" }
func (m *frozenOCRModel) ConfigurationID() string { return m.Name() }
func (m *frozenOCRModel) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	m.completions.Add(1)
	return model.Warranty{Relation: "compatible", Disposition: "false_positive"}, nil
}
func (m *frozenOCRModel) Transcribe(context.Context, string, []byte) (string, error) {
	m.transcribes.Add(1)
	m.transcriptMu.RLock()
	defer m.transcriptMu.RUnlock()
	return m.transcript, nil
}
func (m *frozenOCRModel) setTranscript(value string) {
	m.transcriptMu.Lock()
	m.transcript = value
	m.transcriptMu.Unlock()
}

func TestRulesAreVisibleBeforeBlockedModelAndLateUsageCannotUndoCancel(t *testing.T) {
	st, admin := openExecutionStore(t)
	gate := newHeldReviewModel(1)
	t.Cleanup(gate.unblock)
	st.UseModels(gate)
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, gate.entered, "model entry")
	workspace := readWorkspace(t, st, admin, caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.State != "running" {
		t.Fatalf("attempt was not visibly running: %+v", workspace.Execution)
	}
	if len(workspace.Findings) == 0 || len(workspace.Evaluations) == 0 || workspace.Execution.FirstVerifiedFindingMS == nil {
		t.Fatalf("verified deterministic results were not published before the model returned: %+v", workspace)
	}
	if err := st.Decide(admin, caseID, runID, workspace.Findings[0].ID, "blocked-decision", "confirm_risk", "规则结果已核验", 1); err == nil {
		t.Fatal("in-flight attempt accepted a human decision")
	}

	attemptID := workspace.Execution.Attempt.ID
	recorder := attemptRecorder{store: st, attemptID: attemptID}
	_, finish := recorder.StartModelCall(context.Background(), "held-model")
	if err := st.CancelReview(context.Background(), admin, caseID, runID); err != nil {
		t.Fatal(err)
	}
	inputTokens, outputTokens, totalTokens := int64(17), int64(5), int64(22)
	finish(model.CallObservation{
		ModelRef: "provider-checkpoint-r7", Status: "completed",
		Usage: &model.TokenUsage{Source: "test-response.usage", InputTokens: &inputTokens, OutputTokens: &outputTokens, TotalTokens: &totalTokens},
	})
	cancelled := readWorkspace(t, st, admin, caseID, runID)
	if cancelled.Execution.Attempt.State != "cancelled" || cancelled.View.State != "cancelled" {
		t.Fatalf("late response overwrote cancellation: %+v", cancelled.Execution.Attempt)
	}
	if cancelled.Execution.Usage.Calls != 1 || cancelled.Execution.Usage.UnknownCalls != 0 || cancelled.Execution.Usage.InputTokens == nil || *cancelled.Execution.Usage.InputTokens != inputTokens {
		t.Fatalf("late physical call usage was not retained: %+v", cancelled.Execution.Usage)
	}
	if !hasModelEvent(cancelled.Execution.Events, "model_completed") {
		t.Fatalf("late model completion event missing: %+v", cancelled.Execution.Events)
	}
	for _, event := range cancelled.Execution.Events {
		if event.NodeKind == "model_call" && event.EventType == "model_completed" && (event.ModelRef == nil || *event.ModelRef != "provider-checkpoint-r7") {
			t.Fatalf("cancelled physical call lost the provider-reported model: %+v", event)
		}
	}
	assertStoreEventSequence(t, cancelled.Execution.Events)

	gate.unblock()
	waitSignal(t, gate.returned, "blocked model return")
	st.executionWG.Wait()
	final := readWorkspace(t, st, admin, caseID, runID)
	if final.Execution.Attempt.State != "cancelled" || hasOrigin(final.Findings, "model") {
		t.Fatalf("late model result changed cancelled output: state=%s findings=%+v", final.Execution.Attempt.State, final.Findings)
	}
	assertStoreEventSequence(t, final.Execution.Events)
}

func TestReviewExecutionCapacityIsBounded(t *testing.T) {
	st, admin := openExecutionStore(t)
	gate := newHeldReviewModel(2)
	t.Cleanup(gate.unblock)
	st.UseModels(gate)
	caseIDs := []string{createExecutionCase(t, st, admin), createExecutionCase(t, st, admin), createExecutionCase(t, st, admin)}
	runIDs := make([]string, 2)
	for i := 0; i < 2; i++ {
		var err error
		runIDs[i], err = st.StartReview(context.Background(), admin, caseIDs[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	waitSignal(t, gate.entered, "first model slot")
	waitSignal(t, gate.entered, "second model slot")
	if _, err := st.StartReview(context.Background(), admin, caseIDs[2]); err == nil {
		t.Fatal("review beyond the configured in-process capacity was accepted")
	} else {
		var serviceError *fault.Error
		if !errors.As(err, &serviceError) || serviceError.Status != 503 || serviceError.Code != "execution_capacity" {
			t.Fatalf("capacity refusal was not an explicit 503: %v", err)
		}
	}
	metadata, err := st.OperationalMetadata(admin)
	if err != nil {
		t.Fatal(err)
	}
	if metadata["total_attempts"] != 2 || metadata["running_attempts"] != 2 || metadata["queue_depth"] != nil {
		t.Fatalf("operational metadata misreported the bounded executor: %+v", metadata)
	}
	gate.unblock()
	for i := 0; i < 2; i++ {
		waitSignal(t, gate.returned, "model slot release")
	}
	for i := range runIDs {
		finished := waitTerminalWorkspace(t, st, admin, caseIDs[i], runIDs[i])
		if finished.Execution.Attempt.State != "completed" {
			t.Fatalf("attempt did not complete after a slot was released: %+v", finished.Execution.Attempt)
		}
	}
	st.executionWG.Wait()
}

func TestRetryAndReopenUseFrozenOCRWithoutHistoricalCalls(t *testing.T) {
	st, admin := openExecutionStore(t)
	models := &frozenOCRModel{transcript: "采购文件要求质保36个月。合同总价100万元。"}
	st.UseModels(models)
	caseID, err := st.CreateCase(admin, "冻结转写历史", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	imageID, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.png", "image/png", model.SampleWarrantyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。合同总价120万元。")); err != nil {
		t.Fatal(err)
	}
	firstRunID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	first := waitTerminalWorkspace(t, st, admin, caseID, firstRunID)
	if models.transcribes.Load() != 1 || models.completions.Load() != 1 {
		t.Fatalf("initial OCR/model calls %d/%d", models.transcribes.Load(), models.completions.Load())
	}
	firstAttempt := first.Execution.Attempt.ID
	frozenText := findDocumentText(first.Documents, imageID)
	if frozenText != "采购文件要求质保36个月。合同总价100万元。" || !strings.Contains(frozenText, "36个月") {
		t.Fatalf("OCR output was not frozen: %q", frozenText)
	}
	frozenInput, ok := st.snapshot().attemptInput(firstAttempt)
	if !ok || frozenInput.InputHash == "" {
		t.Fatal("attempt input was not durably frozen")
	}
	imageIdentityFound := false
	for _, document := range frozenInput.Documents {
		if document.Document.ID != imageID {
			continue
		}
		imageIdentityFound = document.OriginalHash == document.Document.Digest &&
			document.NormalizedHash != nil && *document.NormalizedHash == document.IR.NormalizedDigest &&
			strings.Contains(document.ParserRef, "ocr:frozen-ocr-model")
	}
	if !imageIdentityFound {
		t.Fatalf("frozen OCR parser/original/normalized identities are incomplete: %+v", frozenInput.Documents)
	}
	if len(first.Findings) == 0 {
		t.Fatal("expected deterministic findings before retry")
	}
	findingID := first.Findings[0].ID
	if err := st.Decide(admin, caseID, firstRunID, findingID, "frozen-decision", "confirm_risk", "按冻结原文确认", 1); err != nil {
		t.Fatal(err)
	}
	oldEvents := append([]ExecutionEvent(nil), first.Execution.Events...)
	oldOutputHash := first.Execution.Attempt.OutputHash

	models.setTranscript("采购文件改版转写为24个月。")
	if err := st.Reparse(context.Background(), admin, caseID, imageID); err != nil {
		t.Fatal(err)
	}
	transcribesAfterReparse := models.transcribes.Load()
	if transcribesAfterReparse != 2 {
		t.Fatalf("reparse did not exercise the changed OCR output: %d", transcribesAfterReparse)
	}
	if err := st.Retry(context.Background(), admin, caseID, firstRunID); err != nil {
		t.Fatal(err)
	}
	latest := waitTerminalWorkspace(t, st, admin, caseID, firstRunID)
	if latest.Execution.Attempt.ID == firstAttempt || len(latest.Execution.Attempts) != 2 {
		t.Fatalf("retry did not append a new attempt: %+v", latest.Execution)
	}
	if models.transcribes.Load() != transcribesAfterReparse || models.completions.Load() != 2 {
		t.Fatalf("retry re-ran OCR or missed model execution: OCR=%d model=%d", models.transcribes.Load(), models.completions.Load())
	}
	if latest.Execution.Attempt.InputHash == nil || first.Execution.Attempt.InputHash == nil || *latest.Execution.Attempt.InputHash != *first.Execution.Attempt.InputHash {
		t.Fatalf("retry did not preserve frozen input identity: first=%+v latest=%+v", first.Execution.Attempt, latest.Execution.Attempt)
	}
	old, err := st.Workspace(admin, caseID, firstRunID, firstAttempt)
	if err != nil {
		t.Fatal(err)
	}
	if findDocumentText(old.Documents, imageID) != frozenText || len(old.Execution.Events) != len(oldEvents) || !equalEvents(old.Execution.Events, oldEvents) || !equalOptionalString(old.Execution.Attempt.OutputHash, oldOutputHash) {
		t.Fatalf("retry rewrote the original attempt: %+v", old.Execution)
	}
	if decisions := mustDecisionsFor(t, st, admin, findingID); len(decisions) != 1 || decisions[0].Action != "confirm_risk" {
		t.Fatalf("retry removed the original decision: %+v", decisions)
	}

	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopened.UseModels(models)
	ocrBeforeHistoryRead, modelBeforeHistoryRead := models.transcribes.Load(), models.completions.Load()
	historical, err := reopened.Workspace(admin, caseID, firstRunID, firstAttempt)
	if err != nil {
		t.Fatal(err)
	}
	if findDocumentText(historical.Documents, imageID) != frozenText {
		t.Fatalf("reopened history changed frozen OCR text: %q", findDocumentText(historical.Documents, imageID))
	}
	if models.transcribes.Load() != ocrBeforeHistoryRead || models.completions.Load() != modelBeforeHistoryRead {
		t.Fatalf("historical read called OCR/model: OCR %d->%d model %d->%d", ocrBeforeHistoryRead, models.transcribes.Load(), modelBeforeHistoryRead, models.completions.Load())
	}
	assertStoreEventSequence(t, historical.Execution.Events)
	st.executionWG.Wait()
}

func TestReopenMarksUnfinishedAttemptInterrupted(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID := createExecutionCase(t, st, admin)
	seen := st.snapshot()
	documents := currentDocuments(seen.documents(caseID))
	documentSet := documentIDs(documents)
	signature := st.currentSignature(documentSet)
	runID, err := newID("run_")
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := newID("att_")
	if err != nil {
		t.Fatal(err)
	}
	acceptedAt := stamp()
	err = st.update(func(next *state) error {
		next.Runs = append(next.Runs, Run{ID: runID, CaseID: caseID, State: "accepted", ModelRef: signature.ModelRef, CreatedAt: acceptedAt, UpdatedAt: acceptedAt})
		if err := sealSnapshot(next, runID, signature); err != nil {
			return err
		}
		attempt := Attempt{ID: attemptID, RunID: runID, Ordinal: 1, State: "accepted", AcceptedAt: acceptedAt, HistoryKind: "frozen"}
		next.Attempts = append(next.Attempts, attempt)
		next.ExecutionEvents = append(next.ExecutionEvents, acceptedEvent(attempt))
		appendRunEvent(next, runID, "accepted")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	workspace := readWorkspace(t, reopened, admin, caseID, runID)
	if workspace.Execution.Attempt.State != "interrupted" || workspace.View.State != "interrupted" || workspace.Execution.Attempt.ErrorCode == nil || *workspace.Execution.Attempt.ErrorCode != "process_interrupted" {
		t.Fatalf("restart did not terminally mark the unfinished attempt: %+v", workspace.Execution.Attempt)
	}
	assertStoreEventSequence(t, workspace.Execution.Events)
}
func TestExecutionPersistenceFailureCannotFinishSuccessfully(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID := createExecutionCase(t, st, admin)
	if _, err := st.pool.Exec(context.Background(), `CREATE SEQUENCE fail_one_execution_event_seq`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(context.Background(), `CREATE FUNCTION fail_one_execution_event() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.event_seq > 1 AND nextval('fail_one_execution_event_seq') = 1 THEN
				RAISE EXCEPTION 'injected execution event persistence failure';
			END IF;
			RETURN NEW;
		END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(context.Background(), `CREATE TRIGGER fail_one_execution_event BEFORE INSERT ON execution_events FOR EACH ROW EXECUTE FUNCTION fail_one_execution_event()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_one_execution_event ON execution_events`)
		_, _ = st.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS fail_one_execution_event()`)
		_, _ = st.pool.Exec(context.Background(), `DROP SEQUENCE IF EXISTS fail_one_execution_event_seq`)
	})
	models := &frozenOCRModel{transcript: "ignored"}
	st.UseModels(models)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if workspace.Execution.Attempt.State == "completed" || workspace.View.State == "awaiting_human" {
		t.Fatalf("review reported success despite an injected business-event write failure: %+v", workspace.Execution.Attempt)
	}
	if workspace.Execution.Attempt.State != "failed" {
		t.Fatalf("durable persistence failure did not become terminal failure: %+v", workspace.Execution.Attempt)
	}
	st.executionWG.Wait()
}

func TestAdminAloneCannotReadCaseAndMixedAttemptIDsAreHidden(t *testing.T) {
	st, admin := openExecutionStore(t)
	clerk, ok := accounts.ByID("demo-clerk")
	if !ok {
		t.Fatal("demo clerk missing")
	}
	privateCase, err := st.CreateCase(clerk, "非成员管理员不可读", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	addExecutionDocuments(t, st, clerk, privateCase)
	privateRun, err := st.StartReview(context.Background(), clerk, privateCase)
	if err != nil {
		t.Fatal(err)
	}
	privateWorkspace := readWorkspace(t, st, clerk, privateCase, privateRun)
	if _, err := st.Workspace(admin, privateCase, privateRun); err == nil {
		t.Fatal("administrator without case membership read case content")
	}
	publicCase := createExecutionCase(t, st, admin)
	publicRun, err := st.StartReview(context.Background(), admin, publicCase)
	if err != nil {
		t.Fatal(err)
	}
	publicWorkspace := readWorkspace(t, st, admin, publicCase, publicRun)
	privateAttemptID := privateWorkspace.Execution.Attempt.ID
	publicAttemptID := publicWorkspace.Execution.Attempt.ID
	if _, err := st.Workspace(admin, publicCase, privateRun); err == nil {
		t.Fatal("run id from another case was accepted")
	}
	if _, err := st.Workspace(admin, publicCase, publicRun, privateAttemptID); err == nil {
		t.Fatal("attempt id from another run was accepted")
	}
	if _, err := st.Workspace(clerk, privateCase, privateRun, publicAttemptID); err == nil {
		t.Fatal("attempt id from another case was accepted")
	}
	if len(privateWorkspace.Documents) < 2 || len(publicWorkspace.Documents) < 2 {
		t.Fatal("test setup did not include both case bodies")
	}
}

func openExecutionStore(t *testing.T) (*Store, accounts.User) {
	t.Helper()
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	return st, admin
}

func createExecutionCase(t *testing.T, st *Store, owner accounts.User) string {
	t.Helper()
	caseID, err := st.CreateCase(owner, "执行回归", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	addExecutionDocuments(t, st, owner, caseID)
	return caseID
}

func addExecutionDocuments(t *testing.T, st *Store, owner accounts.User, caseID string) {
	t.Helper()
	for role, body := range map[string]string{
		"procurement":    "采购文件要求质保36个月。合同总价为100万元。",
		"draft_contract": "拟签合同约定质保24个月。合同总价为120万元。",
	} {
		if _, err := st.AddDocument(context.Background(), owner, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
}

func readWorkspace(t *testing.T, st *Store, user accounts.User, caseID, runID string) Workspace {
	t.Helper()
	workspace, err := st.Workspace(user, caseID, runID)
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

func waitTerminalWorkspace(t *testing.T, st *Store, user accounts.User, caseID, runID string) Workspace {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		workspace := readWorkspace(t, st, user, caseID, runID)
		if workspace.Execution != nil && workspace.Execution.Attempt != nil && !attemptActive(workspace.Execution.Attempt.State) {
			return workspace
		}
		select {
		case <-deadline.C:
			t.Fatalf("attempt in run %s did not reach a terminal state", runID)
		case <-ticker.C:
		}
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func findDocumentText(docs []Interpreted, id string) string {
	for _, doc := range docs {
		if doc.Document.ID == id {
			return doc.Text
		}
	}
	return ""
}

func hasOrigin(findings []Finding, origin string) bool {
	for _, finding := range findings {
		if finding.Origin == origin {
			return true
		}
	}
	return false
}

func hasModelEvent(events []ExecutionEvent, kind string) bool {
	for _, event := range events {
		if event.NodeKind == "model_call" && event.EventType == kind {
			return true
		}
	}
	return false
}

func assertStoreEventSequence(t *testing.T, events []ExecutionEvent) {
	t.Helper()
	for i, event := range events {
		if event.Seq != i+1 {
			t.Fatalf("execution sequence is not contiguous: index=%d sequence=%d type=%s", i, event.Seq, event.EventType)
		}
	}
}

func equalEvents(a, b []ExecutionEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Seq != b[i].Seq || a[i].AttemptID != b[i].AttemptID || a[i].EventType != b[i].EventType || a[i].SpanID == nil != (b[i].SpanID == nil) {
			return false
		}
		if a[i].SpanID != nil && *a[i].SpanID != *b[i].SpanID {
			return false
		}
	}
	return true
}

func equalOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func TestUsageSummaryKeepsUnknownTotalsNullableAndKnownSubtotals(t *testing.T) {
	inputFirst, inputSecond := int64(11), int64(5)
	outputSecond, totalSecond := int64(7), int64(12)
	events := []ExecutionEvent{
		{AttemptID: "attempt-1", NodeKind: "model_call", EventType: "model_failed", Usage: &UsageObservation{Source: "provider-a", InputTokens: &inputFirst}},
		{AttemptID: "attempt-2", NodeKind: "model_call", EventType: "model_failed", Usage: &UsageObservation{Source: "provider-b"}},
		{AttemptID: "attempt-2", NodeKind: "model_call", EventType: "model_completed", Usage: &UsageObservation{
			Source: "provider-c", InputTokens: &inputSecond, OutputTokens: &outputSecond, TotalTokens: &totalSecond,
		}},
	}
	selected := usageSummary(events, []string{"attempt-2"})
	if selected.Calls != 2 || selected.UnknownCalls != 1 || selected.InputTokens != nil || selected.OutputTokens != nil || selected.TotalTokens != nil {
		t.Fatalf("partially unknown attempt totals were reported as complete: %+v", selected)
	}
	if selected.KnownSubtotals.InputTokens == nil || *selected.KnownSubtotals.InputTokens != inputSecond ||
		selected.KnownSubtotals.OutputTokens == nil || *selected.KnownSubtotals.OutputTokens != outputSecond ||
		selected.KnownSubtotals.TotalTokens == nil || *selected.KnownSubtotals.TotalTokens != totalSecond {
		t.Fatalf("known subtotals were discarded: %+v", selected.KnownSubtotals)
	}
	if len(selected.Sources) != 2 || selected.Sources[0] != "provider-b" || selected.Sources[1] != "provider-c" || selected.Cost != nil {
		t.Fatalf("source provenance or unknown cost changed: %+v", selected)
	}
	all := usageSummary(events, nil)
	if all.Calls != 3 || all.UnknownCalls != 1 || all.InputTokens != nil || all.OutputTokens != nil || all.TotalTokens != nil ||
		all.KnownSubtotals.InputTokens == nil || *all.KnownSubtotals.InputTokens != inputFirst+inputSecond {
		t.Fatalf("all-attempt usage was not aggregated conservatively: %+v", all)
	}
}

func TestRevisionDoesNotResolveBeforeRulePublication(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID := createExecutionCase(t, st, admin)
	oldID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	old := waitTerminalWorkspace(t, st, admin, caseID, oldID)
	if len(old.Findings) == 0 {
		t.Fatal("fixture has no previous conflict")
	}
	if _, err := replaceCurrentPrimaryForTest(t, st, admin, caseID, "revised.txt", []byte("拟签合同约定质保36个月。合同总价为100万元。")); err != nil {
		t.Fatal(err)
	}
	runID, err := newID("run_")
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := newID("att_")
	if err != nil {
		t.Fatal(err)
	}
	// The durable admission boundary exists before the worker publishes rules.
	err = st.update(func(next *state) error {
		now := stamp()
		next.Runs = append(next.Runs, Run{ID: runID, CaseID: caseID, State: "accepted", CreatedAt: now, UpdatedAt: now})
		if err := sealSnapshot(next, runID, st.currentSignature(documentIDs(currentDocuments(next.documents(caseID))))); err != nil {
			return err
		}
		attempt := Attempt{ID: attemptID, RunID: runID, Ordinal: 1, State: "accepted", AcceptedAt: now, HistoryKind: "frozen"}
		next.Attempts = append(next.Attempts, attempt)
		next.ExecutionEvents = append(next.ExecutionEvents, acceptedEvent(attempt))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	pending := readWorkspace(t, st, admin, caseID, runID)
	if pending.PreviousRunID != oldID || len(pending.Revisions) != 0 || pending.RevisionComparisonReason == "" {
		t.Fatalf("unexecuted revision claimed risk changes: %+v", pending)
	}
	if err := st.CancelReview(context.Background(), admin, caseID, runID); err != nil {
		t.Fatal(err)
	}
	cancelled := readWorkspace(t, st, admin, caseID, runID)
	if len(cancelled.Revisions) != 0 || cancelled.RevisionComparisonReason == "" {
		t.Fatalf("cancellation before rules claimed resolved risks: %+v", cancelled.Revisions)
	}
}

func TestOutputDigestSurvivesDecisionAndDetectsRuleMutation(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	before := waitTerminalWorkspace(t, st, admin, caseID, runID)
	attempt := before.Execution.Attempt
	if attempt.OutputHash == nil {
		t.Fatal("terminal output has no digest")
	}
	if err := st.Decide(admin, caseID, runID, before.Findings[0].ID, "hash-decision", "confirm_risk", "已核验原文冲突", 1); err != nil {
		t.Fatal(err)
	}
	current := st.snapshot()
	actual, err := attemptOutputHash(current, runID, attempt.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if actual != *attempt.OutputHash {
		t.Fatalf("human decision invalidated frozen rule output: %s != %s", actual, *attempt.OutputHash)
	}
	for i := range current.Findings {
		if current.Findings[i].AttemptID == attempt.ID {
			current.Findings[i].Claim += "篡改"
			break
		}
	}
	tampered, err := attemptOutputHash(current, runID, attempt.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if tampered == actual {
		t.Fatal("immutable rule output mutation did not change its digest")
	}
}

type successiveFailureModel struct{ calls atomic.Int32 }

func (*successiveFailureModel) Name() string              { return "fixed-failure-identity" }
func (m *successiveFailureModel) ConfigurationID() string { return m.Name() }
func (m *successiveFailureModel) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	if m.calls.Add(1) == 1 {
		return model.Warranty{}, model.ErrUnusable
	}
	return model.Warranty{}, model.ErrRequest
}
func (*successiveFailureModel) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

func TestHistoricalAttemptFailureReasonDoesNotFollowRetry(t *testing.T) {
	st, admin := openExecutionStore(t)
	st.UseModels(&successiveFailureModel{})
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	first := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if first.View.State != "partial" || first.View.PartialReason == "" {
		t.Fatal("first failure not retained")
	}
	if err := st.Retry(context.Background(), admin, caseID, runID); err != nil {
		t.Fatal(err)
	}
	second := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if second.View.PartialReason == first.View.PartialReason {
		t.Fatal("different failure causes were not distinguished")
	}
	history, err := st.Workspace(admin, caseID, runID, first.Execution.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if history.View.PartialReason != first.View.PartialReason {
		t.Fatalf("retry replaced previous failure explanation: %q -> %q", first.View.PartialReason, history.View.PartialReason)
	}
}
