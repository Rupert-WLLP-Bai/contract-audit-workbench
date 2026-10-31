package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func startedEvents(events []ExecutionEvent) []ExecutionEvent {
	var starts []ExecutionEvent
	for _, event := range events {
		if event.EventType == "attempt_started" {
			starts = append(starts, event)
		}
	}
	return starts
}

func TestAttemptStartedEventIsFencedAndGenerationBound(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	var before, after time.Time
	if err := fixture.environment.harness.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := fixture.execution.markAttemptStarted(ctx, fixture.runID, fixture.attemptID); err != nil {
		t.Fatalf("start live claim: %v", err)
	}
	if err := fixture.environment.harness.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	state := durableSnapshot(t, fixture.execution, fixture.claim)
	attempt, found := state.attemptByID(fixture.attemptID)
	starts := startedEvents(state.executionEvents(fixture.attemptID))
	if !found || attempt.State != "running" || attempt.StartedAt == nil || len(starts) != 1 {
		t.Fatalf("start state and lifecycle observation were not committed together: attempt=%+v events=%+v", attempt, starts)
	}
	first := starts[0]
	if first.Seq != 2 || first.At != *attempt.StartedAt || first.RecordedAt == nil || first.EventKey == nil || *first.EventKey != "attempt/started" ||
		first.Generation == nil || *first.Generation != fixture.claim.Generation || first.Status != "running" || first.NodeKind != "attempt" {
		t.Fatalf("start observation lacks the fenced identity or actual start: %+v", first)
	}
	if _, err := time.Parse(time.RFC3339Nano, first.At); err != nil {
		t.Fatalf("start occurrence is not an instant: %v", err)
	}
	recorded, err := time.Parse(time.RFC3339Nano, *first.RecordedAt)
	if err != nil || recorded.Before(before) || recorded.After(after) {
		t.Fatalf("start recording time was not read from PostgreSQL inside the transition: %q %v", *first.RecordedAt, err)
	}
	if err := fixture.execution.markAttemptStarted(ctx, fixture.runID, fixture.attemptID); err != nil {
		t.Fatalf("same generation replay: %v", err)
	}
	replayed := durableSnapshot(t, fixture.execution, fixture.claim)
	if got := startedEvents(replayed.executionEvents(fixture.attemptID)); !reflect.DeepEqual(got, starts) {
		t.Fatalf("same generation created or rewrote a start: before=%+v after=%+v", starts, got)
	}

	expireDurableClaim(t, fixture)
	takeoverStore := fixture.environment.openRole(t, "execution")
	takeover, err := takeoverStore.ClaimNextJob(ctx, "start-takeover", time.Minute)
	if err != nil || takeover.JobID != fixture.jobID || takeover.Generation != fixture.claim.Generation+1 {
		t.Fatalf("takeover claim: %+v %v", takeover, err)
	}
	if err := fixture.execution.markAttemptStarted(ctx, fixture.runID, fixture.attemptID); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale generation started after takeover: %v", err)
	}
	stale := durableSnapshot(t, takeoverStore, takeover)
	if got := startedEvents(stale.executionEvents(fixture.attemptID)); !reflect.DeepEqual(got, starts) {
		t.Fatalf("stale generation appended a start event: %+v", got)
	}
	if err := takeoverStore.markAttemptStarted(ctx, takeover.RunID, takeover.AttemptID); err != nil {
		t.Fatalf("takeover start: %v", err)
	}
	resumed := durableSnapshot(t, takeoverStore, takeover)
	resumedAttempt, found := resumed.attemptByID(takeover.AttemptID)
	got := startedEvents(resumed.executionEvents(takeover.AttemptID))
	if !found || resumedAttempt.StartedAt == nil || *resumedAttempt.StartedAt != *attempt.StartedAt || len(got) != 2 ||
		got[1].Generation == nil || *got[1].Generation != takeover.Generation || got[1].EventKey == nil || *got[1].EventKey != "attempt/started" || got[1].RecordedAt == nil {
		t.Fatalf("takeover lost the original start or its own generation: attempt=%+v events=%+v", resumedAttempt, got)
	}
	if err := takeoverStore.markAttemptStarted(ctx, takeover.RunID, takeover.AttemptID); err != nil {
		t.Fatalf("takeover replay: %v", err)
	}
	if again := startedEvents(durableSnapshot(t, takeoverStore, takeover).executionEvents(takeover.AttemptID)); !reflect.DeepEqual(again, got) {
		t.Fatalf("takeover replay duplicated its start: before=%+v after=%+v", got, again)
	}
}

func TestAttemptStartedEventFailureRollsBackState(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	if _, err := fixture.environment.harness.pool.Exec(ctx, `CREATE FUNCTION reject_attempt_started_test() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.event_type='attempt_started' THEN RAISE EXCEPTION 'forced start event failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_attempt_started_test BEFORE INSERT ON execution_events
		FOR EACH ROW EXECUTE FUNCTION reject_attempt_started_test()`); err != nil {
		t.Fatal(err)
	}
	if err := fixture.execution.markAttemptStarted(ctx, fixture.runID, fixture.attemptID); err == nil {
		t.Fatal("start succeeded when its lifecycle event could not be inserted")
	}
	state := durableSnapshot(t, fixture.execution, fixture.claim)
	attempt, found := state.attemptByID(fixture.attemptID)
	run, runFound := state.runByID(fixture.runID)
	if !found || !runFound || attempt.State != "accepted" || attempt.StartedAt != nil || run.State != "accepted" || len(startedEvents(state.executionEvents(fixture.attemptID))) != 0 {
		t.Fatalf("failed start event committed a partial transition: run=%+v attempt=%+v", run, attempt)
	}
}

func TestEmbeddedAttemptRecordsStartBeforeStages(t *testing.T) {
	st, admin := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.StartedAt == nil {
		t.Fatal("embedded attempt has no start")
	}
	starts := startedEvents(workspace.Execution.Events)
	if len(starts) != 1 || starts[0].Seq != 2 || starts[0].At != *workspace.Execution.Attempt.StartedAt || starts[0].RecordedAt == nil || starts[0].Generation != nil {
		t.Fatalf("embedded attempt start was missing or not ordered before stages: %+v", starts)
	}
}
