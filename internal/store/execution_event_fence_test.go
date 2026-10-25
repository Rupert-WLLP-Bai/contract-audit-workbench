package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"contractaudit/internal/execution"
)

// TestExecutionEventAppendIsFencedAcrossInstances proves that two independent
// durable Execution stores preserve append order and reject stale generations.
func TestExecutionEventAppendIsFencedAcrossInstances(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	before := fixture.execution.snapshot().executionEvents(fixture.attemptID)
	if len(before) == 0 {
		t.Fatalf("acceptance event history is missing: %+v", before)
	}
	recorder := attemptRecorder{store: fixture.execution, attemptID: fixture.attemptID, runID: fixture.runID}
	event := execution.Event{NodeKind: "phase", Phase: "evidence", EventType: "node_completed", Status: "completed"}
	if err := recorder.RecordExecutionEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	events := fixture.execution.snapshot().executionEvents(fixture.attemptID)
	if len(events) != len(before)+1 || events[len(events)-1].Seq != before[len(before)-1].Seq+1 || events[len(events)-1].EventType != "node_completed" {
		t.Fatalf("the narrow append did not reach the local read model in order: before=%+v after=%+v", before, events)
	}
	reader := fixture.environment.openRole(t, "execution")
	durable := durableSnapshot(t, reader, fixture.claim).executionEvents(fixture.attemptID)
	if len(durable) != len(events) || durable[len(durable)-1].Seq != events[len(events)-1].Seq {
		t.Fatalf("event append was not durable for a second store: local=%+v durable=%+v", events, durable)
	}
	if err := reader.CancelReview(ctx, fixture.admin, fixture.caseID, fixture.runID); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordExecutionEvent(ctx, event); err == nil {
		t.Fatal("a cancelled attempt accepted further execution telemetry")
	}

	reader.UseModels(&frozenOCRModel{})
	reader.UseDocumentPreparer(fixture.document)
	if _, err := fixture.document.AddDocument(context.Background(), fixture.admin, fixture.caseID, "procurement", "supplement.txt", "text/plain", []byte("补充采购材料")); err != nil {
		t.Fatalf("document service could not change the review signature: %v", err)
	}
	second, err := reader.StartReviewAsync(ctx, fixture.admin, fixture.caseID)
	if err != nil {
		t.Fatal(err)
	}
	staleStore := fixture.environment.openRole(t, "execution")
	staleClaim, err := staleStore.ClaimNextJob(ctx, "stale-worker", time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	if staleClaim.JobID != second.JobID {
		t.Fatalf("stale instance claimed %q, want %q", staleClaim.JobID, second.JobID)
	}
	// Model expiry of the persisted retry delay as well as the lease; this
	// scenario exercises fencing after takeover, not immediate retry eligibility.
	if _, err := fixture.environment.harness.pool.Exec(ctx, `UPDATE worker_jobs SET ready_at=clock_timestamp()-interval '1 second' WHERE id=$1`, staleClaim.JobID); err != nil {
		t.Fatal(err)
	}
	takeoverStore := fixture.environment.openRole(t, "execution")
	takeoverClaim, err := takeoverStore.ClaimNextJob(ctx, "takeover-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if takeoverClaim.JobID != second.JobID || takeoverClaim.Generation != staleClaim.Generation+1 {
		t.Fatalf("takeover did not supersede the stale claim: stale=%+v takeover=%+v", staleClaim, takeoverClaim)
	}
	staleRecorder := attemptRecorder{store: staleStore, attemptID: staleClaim.AttemptID, runID: second.RunID}
	if err := staleRecorder.RecordExecutionEvent(ctx, event); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("a superseded instance appended an execution event: %v", err)
	}
	takeoverRecorder := attemptRecorder{store: takeoverStore, attemptID: takeoverClaim.AttemptID, runID: second.RunID}
	if err := takeoverRecorder.RecordExecutionEvent(ctx, event); err != nil {
		t.Fatalf("the takeover instance could not append: %v", err)
	}
	takeoverEvents := durableSnapshot(t, takeoverStore, takeoverClaim).executionEvents(takeoverClaim.AttemptID)
	if len(takeoverEvents) < 2 || takeoverEvents[len(takeoverEvents)-1].EventType != "node_completed" || takeoverEvents[len(takeoverEvents)-1].Seq != takeoverEvents[len(takeoverEvents)-2].Seq+1 {
		t.Fatalf("takeover event sequence is not visible or monotonic: %+v", takeoverEvents)
	}
	if err := (&Store{pool: takeoverStore.pool}).appendExecutionEventFenced(ctx, second.RunID, second.AttemptID, ExecutionEvent{
		AttemptID: second.AttemptID, NodeKind: "phase", EventType: "node_completed",
	}); err == nil {
		t.Fatal("an append without the durable claim was accepted")
	}
	if err := takeoverStore.CancelReview(ctx, fixture.admin, fixture.caseID, second.RunID); err != nil {
		t.Fatal(err)
	}
}
