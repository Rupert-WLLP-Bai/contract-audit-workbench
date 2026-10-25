package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestStaleGenerationCompletionIsRejected exercises the durable Execution role
// from two independent stores and verifies that a superseded worker publishes
// no input, results, or terminal state.
func TestStaleGenerationCompletionIsRejected(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	takeoverStore := fixture.environment.openRole(t, "execution")
	takeoverStore.UseDocumentPreparer(fixture.document)
	takeoverStore.UseModels(&frozenOCRModel{})
	versionBefore := durableAttemptVersion(t, fixture.execution, fixture.claim)
	expireDurableClaim(t, fixture)
	takeover, err := takeoverStore.ClaimNextJob(ctx, "takeover-worker", time.Minute)
	if err != nil {
		t.Fatalf("takeover claim failed: %v", err)
	}
	if takeover.JobID != fixture.jobID || takeover.WorkerID != "takeover-worker" || takeover.Generation != fixture.claim.Generation+1 {
		t.Fatalf("takeover did not supersede the stale claim: stale=%+v takeover=%+v", fixture.claim, takeover)
	}
	if _, err := fixture.execution.HeartbeatJob(ctx, fixture.claim, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("superseded heartbeat was accepted: %v", err)
	}
	if err := fixture.execution.RunClaimedJob(ctx, fixture.claim); err == nil {
		t.Fatal("superseded worker completed the job")
	}
	full := durableSnapshot(t, takeoverStore, takeover)
	attempt, ok := full.attemptByID(takeover.AttemptID)
	if !ok || attempt.State != "accepted" || attempt.StartedAt != nil || attempt.FinishedAt != nil || attempt.OutputHash != nil {
		t.Fatalf("the stale worker changed the authoritative attempt: %+v", attempt)
	}
	if _, ok := full.attemptInput(takeover.AttemptID); ok {
		t.Fatal("the stale worker froze official input")
	}
	if len(full.findingsForAttempt(takeover.RunID, takeover.AttemptID)) != 0 || len(full.evaluationsForAttempt(takeover.RunID, takeover.AttemptID)) != 0 {
		t.Fatal("the stale worker published official results")
	}
	for _, event := range full.executionEvents(takeover.AttemptID) {
		if event.EventType == "attempt_finished" || event.EventType == "attempt_started" {
			t.Fatalf("the stale worker published a lifecycle event: %+v", event)
		}
	}
	versionAfter := durableAttemptVersion(t, takeoverStore, takeover)
	if versionAfter != versionBefore {
		t.Fatalf("stale generation changed the attempt version: before=%d after=%d", versionBefore, versionAfter)
	}
	var job Job
	for _, candidate := range full.Jobs {
		if candidate.ID == takeover.JobID {
			job = candidate
			break
		}
	}
	if job.State != "running" || job.Generation != takeover.Generation || job.WorkerID != takeover.WorkerID {
		t.Fatalf("stale execution changed the takeover job: %+v", job)
	}
	if err := takeoverStore.RunClaimedJob(ctx, takeover); err != nil {
		t.Fatalf("current generation could not publish the attempt: %v", err)
	}
	finishedState := durableSnapshot(t, takeoverStore, takeover)
	finished, ok := finishedState.attemptByID(takeover.AttemptID)
	if !ok || finished.State != "completed" || finished.InputHash == nil || finished.OutputHash == nil {
		t.Fatalf("current generation did not publish the frozen terminal result: %+v", finished)
	}
	want, err := attemptOutputHash(finishedState, takeover.RunID, takeover.AttemptID, "")
	if err != nil || want != *finished.OutputHash {
		t.Fatalf("current generation output hash mismatch: got=%v want=%q err=%v", finished.OutputHash, want, err)
	}
}
