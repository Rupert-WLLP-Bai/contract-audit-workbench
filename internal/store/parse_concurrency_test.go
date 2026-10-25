package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/model"
)

type parseConcurrencyTranscription struct {
	ordinal int32
	release func()
}

type parseConcurrencyModel struct {
	calls      atomic.Int32
	entered    chan parseConcurrencyTranscription
	warranties chan string
	releasesMu sync.Mutex
	releases   []func()
}

func newParseConcurrencyModel() *parseConcurrencyModel {
	return &parseConcurrencyModel{
		entered:    make(chan parseConcurrencyTranscription, 8),
		warranties: make(chan string, 8),
	}
}

func (*parseConcurrencyModel) Name() string            { return "parse-concurrency-model" }
func (*parseConcurrencyModel) OCRName() string         { return "parse-concurrency-ocr" }
func (*parseConcurrencyModel) ConfigurationID() string { return "parse-concurrency-config/v1" }

func (m *parseConcurrencyModel) Warranty(_ context.Context, materials []model.Material) (model.Warranty, error) {
	for _, material := range materials {
		if material.Role == "draft_contract" {
			m.warranties <- material.Text
			break
		}
	}
	return model.Warranty{Relation: "compatible", Disposition: "false_positive"}, nil
}

func (m *parseConcurrencyModel) Transcribe(ctx context.Context, _ string, _ []byte) (string, error) {
	ordinal := m.calls.Add(1)
	blocked := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(blocked) }) }
	m.releasesMu.Lock()
	m.releases = append(m.releases, release)
	m.releasesMu.Unlock()
	m.entered <- parseConcurrencyTranscription{ordinal: ordinal, release: release}
	select {
	case <-blocked:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	switch ordinal {
	case 1:
		return "采购文件要求质保36个月。", nil
	default:
		return "采购文件要求质保48个月。", nil
	}
}

func (m *parseConcurrencyModel) unblockAll() {
	m.releasesMu.Lock()
	releases := append([]func(){}, m.releases...)
	m.releasesMu.Unlock()
	for _, release := range releases {
		release()
	}
}

type firstErrBarrierContext struct {
	context.Context
	checked     chan struct{}
	proceed     chan struct{}
	calls       atomic.Int32
	once        sync.Once
	proceedOnce sync.Once
}

func newFirstErrBarrierContext(ctx context.Context) *firstErrBarrierContext {
	return &firstErrBarrierContext{Context: ctx, checked: make(chan struct{}), proceed: make(chan struct{})}
}

func (c *firstErrBarrierContext) Err() error {
	if c.calls.Add(1) == 1 {
		c.once.Do(func() { close(c.checked) })
		<-c.proceed
		return nil
	}
	return c.Context.Err()
}

func (c *firstErrBarrierContext) continueAfterFirstCheck() {
	c.proceedOnce.Do(func() { close(c.proceed) })
}

type parseStartResult struct {
	runID string
	err   error
}

func TestConcurrentAcceptedRunsFreezeIndependentPendingParseSelections(t *testing.T) {
	assertIndependentPendingParseProducers(t, false)
}

func TestCompanionReparseDoesNotSharePendingParseProducer(t *testing.T) {
	assertIndependentPendingParseProducers(t, true)
}

func assertIndependentPendingParseProducers(t *testing.T, reparseCompanion bool) {
	t.Helper()
	st, admin := openExecutionStore(t)
	models := newParseConcurrencyModel()
	t.Cleanup(models.unblockAll)
	st.UseModels(models)

	caseID, err := st.CreateCase(admin, "并发受理冻结解析选择", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	imageID, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.png", "image/png", []byte("purchase image"))
	if err != nil {
		t.Fatal(err)
	}
	draftID, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft-v1.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	if err != nil {
		t.Fatal(err)
	}

	runA, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedID, err := st.StartReview(context.Background(), admin, caseID); err != nil || repeatedID != runA {
		t.Fatalf("identical pending start was not idempotent: run=%q err=%v", repeatedID, err)
	}
	firstOCR := waitParseConcurrencyTranscription(t, models.entered, "first accepted run OCR")
	if firstOCR.ordinal != 1 {
		t.Fatalf("first OCR invocation ordinal = %d, want 1", firstOCR.ordinal)
	}

	expectedDraftText := "拟签合同约定质保12个月。"
	if reparseCompanion {
		expectedDraftText = "拟签合同约定质保24个月。"
		if err := st.Reparse(context.Background(), admin, caseID, draftID); err != nil {
			t.Fatal(err)
		}
	} else if _, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft-v2.txt", "text/plain", []byte(expectedDraftText)); err != nil {
		t.Fatal(err)
	}
	runB, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if runB == runA {
		t.Fatal("changed material or parse reused the earlier run")
	}
	secondOCR := waitParseConcurrencyTranscription(t, models.entered, "replacement run OCR")
	if secondOCR.ordinal != 2 {
		t.Fatalf("second OCR invocation ordinal = %d, want 2", secondOCR.ordinal)
	}

	beforeRelease := st.snapshot()
	selectionA := selectedParseID(beforeRelease, runA, imageID)
	selectionB := selectedParseID(beforeRelease, runB, imageID)
	firstIDWasIndependent := selectionA != "" && selectionB != "" && selectionA != selectionB

	// Complete the newer selection while the older producer is still held.
	secondOCR.release()
	select {
	case draftText := <-models.warranties:
		if draftText != expectedDraftText {
			t.Fatalf("newer run reached Warranty with draft text %q", draftText)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("newer run did not finish parsing while the older OCR call was held")
	}
	newerPublished := st.snapshot()
	newerArtifact, newerExists := newerPublished.parsedByID(selectionB)
	newerWasCurrent := false
	if newerExists {
		latest, ok := newerPublished.latestParsed(imageID, "")
		newerWasCurrent = ok && latest.ID == selectionB && newerArtifact.ID == selectionB
	}

	firstOCR.release()
	waitParseConcurrencyWorkers(t, st)
	final := st.snapshot()
	latest, latestOK := final.latestParsed(imageID, "")
	if !firstIDWasIndependent {
		t.Errorf("different accepted runs did not reserve independent parse IDs: A=%q B=%q", selectionA, selectionB)
	}
	if !newerWasCurrent || !latestOK || latest.ID != selectionB {
		t.Errorf("older completion replaced the newer selection: before=%q after=%q want=%q", selectionB, latest.ID, selectionB)
	}

	for label, runID := range map[string]string{"A": runA, "B": runB} {
		workspace := readWorkspace(t, st, admin, caseID, runID)
		if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.State == "failed" || workspace.Execution.Attempt.State == "cancelled" {
			t.Errorf("run %s did not retain a usable attempt: %+v", label, workspace.Execution)
			continue
		}
		if !hasOrigin(workspace.Findings, "rule") {
			t.Errorf("run %s lost its deterministic findings: %+v", label, workspace.Findings)
		}
		attempt := workspace.Execution.Attempt
		input, ok := final.attemptInput(attempt.ID)
		if !ok {
			t.Errorf("run %s did not freeze its attempt input", label)
			continue
		}
		frozenID := parsedDocumentIDFromFrozen(input.Documents, imageID)
		selectedID := selectedParseID(final, runID, imageID)
		artifact, exists := final.parsedByID(frozenID)
		if frozenID == "" || frozenID != selectedID || !exists || artifact.ID != frozenID {
			t.Errorf("run %s did not preserve its exact image artifact reference: frozen=%q selection=%q exists=%v", label, frozenID, selectedID, exists)
		}
	}
}

func TestReparseOCRDoesNotBlockUnrelatedStartOrCancelledRequests(t *testing.T) {
	st, admin := openExecutionStore(t)
	models := newParseConcurrencyModel()
	st.UseModels(models)

	reparseCase, err := st.CreateCase(admin, "显式重解析中的案件", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	imageID, err := st.AddDocument(context.Background(), admin, reparseCase, "procurement", "purchase.png", "image/png", []byte("reparse image"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, reparseCase, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。")); err != nil {
		t.Fatal(err)
	}
	readyCase, err := st.CreateCase(admin, "独立文本案件", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, readyCase, "procurement", "purchase.txt", "text/plain", []byte("采购文件要求质保36个月。")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, readyCase, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。")); err != nil {
		t.Fatal(err)
	}

	reparseDone := make(chan struct{})
	var reparseErr error
	go func() {
		reparseErr = st.Reparse(context.Background(), admin, reparseCase, imageID)
		close(reparseDone)
	}()
	t.Cleanup(func() {
		models.unblockAll()
		select {
		case <-reparseDone:
		case <-time.After(10 * time.Second):
			t.Error("explicit reparse did not finish during cleanup")
		}
	})
	waitParseConcurrencyTranscription(t, models.entered, "explicit reparse OCR")

	cancelBase, cancel := context.WithCancel(context.Background())
	cancelledContext := newFirstErrBarrierContext(cancelBase)
	cancelledDone := make(chan parseStartResult, 1)
	cancelledFinished := make(chan struct{})
	go func() {
		defer close(cancelledFinished)
		runID, err := st.StartReview(cancelledContext, admin, readyCase)
		cancelledDone <- parseStartResult{runID: runID, err: err}
	}()
	normalContext := newFirstErrBarrierContext(context.Background())
	normalDone := make(chan parseStartResult, 1)
	normalFinished := make(chan struct{})
	go func() {
		defer close(normalFinished)
		runID, err := st.StartReview(normalContext, admin, readyCase)
		normalDone <- parseStartResult{runID: runID, err: err}
	}()
	t.Cleanup(func() {
		cancel()
		models.unblockAll()
		cancelledContext.continueAfterFirstCheck()
		normalContext.continueAfterFirstCheck()
		for _, finished := range []<-chan struct{}{cancelledFinished, normalFinished} {
			select {
			case <-finished:
			case <-time.After(10 * time.Second):
				t.Error("start request did not finish during cleanup")
			}
		}
	})

	waitParseConcurrencySignal(t, cancelledContext.checked, "cancelled request first context check")
	waitParseConcurrencySignal(t, normalContext.checked, "ready-text request first context check")
	cancel()
	cancelledContext.continueAfterFirstCheck()
	normalContext.continueAfterFirstCheck()

	acceptedBeforeRelease, cancelledBeforeRelease := false, false
	var acceptedResult, cancelledResult parseStartResult
	deadline := time.NewTimer(10 * time.Second)
collectBeforeOCRRelease:
	for !acceptedBeforeRelease || !cancelledBeforeRelease {
		select {
		case acceptedResult = <-normalDone:
			acceptedBeforeRelease = true
		case cancelledResult = <-cancelledDone:
			cancelledBeforeRelease = true
		case <-deadline.C:
			break collectBeforeOCRRelease
		}
	}
	deadline.Stop()
	models.unblockAll()
	waitParseConcurrencySignal(t, reparseDone, "explicit reparse completion")
	if !acceptedBeforeRelease {
		acceptedResult = waitParseStartResult(t, normalDone, "ready-text start after OCR release")
	}
	if !cancelledBeforeRelease {
		cancelledResult = waitParseStartResult(t, cancelledDone, "cancelled start after OCR release")
	}
	waitParseConcurrencyWorkers(t, st)

	if reparseErr != nil {
		t.Errorf("explicit reparse failed: %v", reparseErr)
	}
	if !acceptedBeforeRelease || acceptedResult.err != nil || acceptedResult.runID == "" {
		t.Errorf("ready-text run was not accepted before unrelated OCR release: result=%+v beforeRelease=%v", acceptedResult, acceptedBeforeRelease)
	}
	if !cancelledBeforeRelease || !errors.Is(cancelledResult.err, context.Canceled) {
		t.Errorf("cancelled request waited for unrelated OCR: result=%+v beforeRelease=%v", cancelledResult, cancelledBeforeRelease)
	}
}

func waitParseConcurrencyTranscription(t *testing.T, calls <-chan parseConcurrencyTranscription, what string) parseConcurrencyTranscription {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return parseConcurrencyTranscription{}
	}
}

func waitParseConcurrencySignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitParseConcurrencyWorkers(t *testing.T, st *Store) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		st.executionWG.Wait()
		close(done)
	}()
	waitParseConcurrencySignal(t, done, "review workers")
}

func waitParseStartResult(t *testing.T, result <-chan parseStartResult, what string) parseStartResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return parseStartResult{}
	}
}

func selectedParseID(st state, runID, documentID string) string {
	for _, attempt := range st.Attempts {
		if attempt.RunID != runID {
			continue
		}
		for _, selection := range st.parseSelectionsForAttempt(attempt.ID) {
			if selection.DocumentID == documentID {
				return selection.ParsedDocumentID
			}
		}
	}
	return ""
}
