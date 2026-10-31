package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/execution"
)

// These regressions need the shared PostgreSQL test environment (B owns it) and
// are written to prove the D2 transition guarantees on the four frozen hot
// entities: sessions, review_attempts (+ appended execution_events),
// worker_jobs and cases.

// TestLateModelEventCannotRewriteTerminalOrDecidedState exercises the durable
// Execution writer after a separate API cancellation and preserves prior decisions.
func TestLateModelEventCannotRewriteTerminalOrDecidedState(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	if err := fixture.execution.RunClaimedJob(ctx, fixture.claim); err != nil {
		t.Fatalf("first durable attempt failed: %v", err)
	}
	firstState := durableSnapshot(t, fixture.execution, fixture.claim)
	firstFindings := firstState.findingsForAttempt(fixture.runID, fixture.attemptID)
	if len(firstFindings) == 0 {
		t.Fatalf("first durable attempt did not produce findings: %+v", firstState.Findings)
	}
	finding := firstFindings[0]
	if err := fixture.core.Decide(fixture.admin, fixture.caseID, fixture.runID, finding.ID, "late-event-decision", "confirm_risk", "按冻结原文确认", finding.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.document.AddDocument(context.Background(), fixture.admin, fixture.caseID, "procurement", "supplement.txt", "text/plain", []byte("补充采购材料")); err != nil {
		t.Fatalf("document service could not change the review signature: %v", err)
	}
	accepted, err := fixture.execution.StartReviewAsync(ctx, fixture.admin, fixture.caseID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := fixture.execution.ClaimNextJob(ctx, "late-event-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed %q, want %q", claim.JobID, accepted.JobID)
	}
	api := fixture.environment.openRole(t, "execution")
	if err := api.CancelReview(ctx, fixture.admin, fixture.caseID, accepted.RunID); err != nil {
		t.Fatal(err)
	}
	before := durableSnapshot(t, api, claim)
	firstBeforeLateEvent := before.findingsForAttempt(fixture.runID, fixture.attemptID)
	beforeDecisionCount, beforeFindingCount := len(before.Decisions), len(before.Findings)
	if err := fixture.execution.persistLateModelEvent(ctx, accepted.AttemptID, ExecutionEvent{
		AttemptID: accepted.AttemptID, NodeKind: "model_call", EventType: "model_completed",
		At: stamp(), Status: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	after := durableSnapshot(t, api, claim)
	attempt, found := after.attemptByID(accepted.AttemptID)
	if !found || attempt.State != "cancelled" || attempt.ErrorCode == nil || *attempt.ErrorCode != "cancelled_by_user" {
		t.Fatalf("the late event changed the terminal attempt: %+v", attempt)
	}
	if run, found := after.runByID(accepted.RunID); !found || run.State != "cancelled" {
		t.Fatalf("the late event changed the run state: %+v", run)
	}
	if len(after.Decisions) != beforeDecisionCount || len(after.Findings) != beforeFindingCount ||
		len(after.findingsForAttempt(accepted.RunID, accepted.AttemptID)) != 0 || len(after.evaluationsForAttempt(accepted.RunID, accepted.AttemptID)) != 0 {
		t.Fatalf("the late event rewrote decisions or official findings: decisions=%d/%d findings=%d/%d", len(before.Decisions), len(after.Decisions), beforeFindingCount, len(after.Findings))
	}
	events := after.executionEvents(accepted.AttemptID)
	recorded := false
	for _, event := range events {
		if event.EventType == "model_completed" {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("late model telemetry was not appended: %+v", events)
	}
	firstAfter := after.findingsForAttempt(fixture.runID, fixture.attemptID)
	if !reflect.DeepEqual(firstBeforeLateEvent, firstAfter) {
		t.Fatalf("late telemetry changed the earlier attempt's finding history: before=%+v after=%+v", firstBeforeLateEvent, firstAfter)
	}
}

// TestCrossInstanceCancelBeatsWorkerCompletion cancels through a second durable
// Execution store and proves the worker's stale claim cannot publish afterward.
func TestCrossInstanceCancelBeatsWorkerCompletion(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	api := fixture.environment.openRole(t, "execution")
	if err := api.CancelReview(ctx, fixture.admin, fixture.caseID, fixture.runID); err != nil {
		t.Fatal(err)
	}
	attempt, found := durableSnapshot(t, api, fixture.claim).attemptByID(fixture.attemptID)
	if !found || attempt.State != "cancelled" || attempt.ErrorCode == nil || *attempt.ErrorCode != "cancelled_by_user" {
		t.Fatalf("cancel did not terminate the durable attempt: %+v", attempt)
	}
	if _, err := fixture.execution.HeartbeatJob(ctx, fixture.claim, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("cancelled claim kept its lease: %v", err)
	}
	if err := fixture.execution.RunClaimedJob(ctx, fixture.claim); err == nil {
		t.Fatal("a cancelled attempt was completed by its worker")
	}
	after := durableSnapshot(t, api, fixture.claim)
	finished, found := after.attemptByID(fixture.attemptID)
	if !found || finished.State != "cancelled" || finished.ErrorCode == nil || *finished.ErrorCode != "cancelled_by_user" {
		t.Fatalf("worker completion overwrote the cancellation: %+v", finished)
	}
	if _, ok := after.attemptInput(fixture.attemptID); ok {
		t.Fatal("worker completion froze official input after cancellation")
	}
	if len(after.findingsForAttempt(fixture.runID, fixture.attemptID)) != 0 || len(after.evaluationsForAttempt(fixture.runID, fixture.attemptID)) != 0 {
		t.Fatal("worker completion published official results after cancellation")
	}
}

// TestForeignScopeCannotCommitOfficialResults proves the durable writer refuses
// an execution mutation whose run does not match the installed claim.
func TestForeignScopeCannotCommitOfficialResults(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	err := fixture.execution.updateAuthorizedExecution(ctx, "run_foreign", fixture.attemptID, func(st *state) error {
		return nil
	})
	if err == nil {
		t.Fatal("an execution commit for a foreign run was accepted")
	}
	snapshot := durableSnapshot(t, fixture.execution, fixture.claim)
	attempt, found := snapshot.attemptByID(fixture.attemptID)
	if !found || attempt.RunID != fixture.runID || attempt.State != "accepted" || attempt.FinishedAt != nil || attempt.OutputHash != nil {
		t.Fatalf("the foreign write changed the live attempt: %+v", attempt)
	}
	if _, found := snapshot.attemptInput(fixture.attemptID); found || len(snapshot.findingsForAttempt(fixture.runID, fixture.attemptID)) != 0 ||
		len(snapshot.evaluationsForAttempt(fixture.runID, fixture.attemptID)) != 0 {
		t.Fatal("the foreign write published official input or results")
	}
}

// TestExecutionEventAppendKeepsSequenceMonotonic pins append ordering across
// two independent durable Execution stores writing one attempt.
func TestExecutionEventAppendKeepsSequenceMonotonic(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	secondStore := fixture.environment.openRole(t, "execution")
	secondStore.setClaim(fixture.claim)
	appended := []execution.Event{
		{NodeKind: "phase", Phase: "evidence", EventType: "node_started", Status: "running"},
		{NodeKind: "phase", Phase: "evidence", EventType: "node_completed", Status: "completed"},
	}
	writers := []*Store{fixture.execution, secondStore}
	for i, event := range appended {
		recorder := attemptRecorder{store: writers[i], attemptID: fixture.attemptID, runID: fixture.runID}
		if err := recorder.RecordExecutionEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events := durableSnapshot(t, secondStore, fixture.claim).executionEvents(fixture.attemptID)
	if len(events) < len(appended)+1 || events[len(events)-2].EventType != appended[0].EventType || events[len(events)-1].EventType != appended[1].EventType {
		t.Fatalf("events from both durable writers were not persisted: %+v", events)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Seq != events[i-1].Seq+1 {
			t.Fatalf("execution event sequence is not contiguous: %+v", events)
		}
	}
	if err := secondStore.CancelReview(ctx, fixture.admin, fixture.caseID, fixture.runID); err != nil {
		t.Fatal(err)
	}
}
