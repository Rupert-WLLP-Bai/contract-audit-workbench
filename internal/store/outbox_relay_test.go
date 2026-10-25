package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestOutboxRelayClaimAckReleaseRecovery pins the D2-6 relay persistence
// contract against PostgreSQL: exactly one relay owns a pending event, a
// superseded owner cannot ACK or release it, retry reasons are bounded codes,
// backoff gates re-claiming, and the PG scan recovers whatever a dead relay left
// pending. The broker is never involved.
func TestOutboxRelayClaimAckReleaseRecovery(t *testing.T) {
	ctx := context.Background()
	setup, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	executionStore := setup.environment.openRole(t, "execution")
	executionStore.UseModels(&frozenOCRModel{})
	executionStore.UseDocumentPreparer(setup.document)
	drainTestOutbox(t, executionStore)
	accepted, err := executionStore.StartReviewAsync(ctx, setup.admin, setup.caseID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := executionStore.ClaimNextJob(ctx, "outbox-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed %q, want %q", claim.JobID, accepted.JobID)
	}
	fixture := durableFixture{environment: setup.environment, core: setup.core, document: setup.document, execution: executionStore,
		admin: setup.admin, actor: setup.admin, caseID: setup.caseID, runID: accepted.RunID,
		attemptID: accepted.AttemptID, jobID: accepted.JobID, claim: claim, dir: setup.dir}
	if pending := outboxPending(t, executionStore, accepted.JobID); pending != 1 {
		t.Fatalf("acceptance did not leave exactly one pending event for its job: pending=%d", pending)
	}
	first, err := executionStore.ClaimOutbox(ctx, "relay-a", 8, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Event.JobID != accepted.JobID || first[0].Token != 1 ||
		first[0].Event.State != "pending" || first[0].Event.Payload == "" {
		t.Fatalf("unexpected first claim: %+v", first)
	}
	if claimed, err := executionStore.ClaimOutbox(ctx, "relay-b", 8, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("a leased event was handed to a second relay: claims=%+v err=%v", claimed, err)
	}
	if err := executionStore.ReleaseOutbox(ctx, first[0], "broker_unavailable", time.Minute); err != nil {
		t.Fatal(err)
	}
	if claimed, err := executionStore.ClaimOutbox(ctx, "relay-b", 8, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("a backed-off event was claimable: claims=%+v err=%v", claimed, err)
	}
	if err := executionStore.ReleaseOutbox(ctx, first[0], "https://broker.internal/secret", 0); err == nil {
		t.Fatal("a release accepted an unbounded reason string")
	}
	resetOutboxBackoff(t, executionStore, first[0].Event.ID)
	short, err := executionStore.ClaimOutbox(ctx, "relay-a", 8, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(short) != 1 || short[0].Token != first[0].Token+1 {
		t.Fatalf("re-claim did not advance the fencing token: %+v", short)
	}
	time.Sleep(5 * time.Millisecond)
	if err := executionStore.AckOutbox(ctx, short[0]); !errors.Is(err, ErrOutboxClaimLost) {
		t.Fatalf("an expired lease could still be acknowledged: %v", err)
	}
	if err := executionStore.ReleaseOutbox(ctx, short[0], "expired_lease", 0); !errors.Is(err, ErrOutboxClaimLost) {
		t.Fatalf("an expired lease could still be released: %v", err)
	}
	takeover, err := executionStore.ClaimOutbox(ctx, "relay-b", 8, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(takeover) != 1 || takeover[0].Token != short[0].Token+1 || takeover[0].Owner != "relay-b" {
		t.Fatalf("an expired lease was not taken over: %+v", takeover)
	}
	if err := executionStore.AckOutbox(ctx, short[0]); !errors.Is(err, ErrOutboxClaimLost) {
		t.Fatalf("a superseded ACK was accepted: %v", err)
	}
	if err := executionStore.ReleaseOutbox(ctx, short[0], "stale_lease", 0); !errors.Is(err, ErrOutboxClaimLost) {
		t.Fatalf("a superseded release was accepted: %v", err)
	}
	if err := executionStore.AckOutbox(ctx, takeover[0]); err != nil {
		t.Fatalf("the current owner could not acknowledge: %v", err)
	}
	if pending := outboxPending(t, executionStore, accepted.JobID); pending != 0 {
		t.Fatalf("acknowledged event is still pending: pending=%d", pending)
	}
	if claimed, err := executionStore.ClaimOutbox(ctx, "relay-c", 8, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("a delivered event was claimable again: claims=%+v err=%v", claimed, err)
	}
	if _, err := executionStore.ClaimOutbox(ctx, "", 8, time.Minute); err == nil {
		t.Fatal("an empty relay owner was accepted")
	}
	if err := executionStore.RunClaimedJob(ctx, fixture.claim); err != nil {
		t.Fatalf("durable review job did not complete after the outbox test: %v", err)
	}
}

func drainTestOutbox(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	for {
		claims, err := store.ClaimOutbox(ctx, "test-preflight", 512, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if len(claims) == 0 {
			return
		}
		for _, claim := range claims {
			if err := store.AckOutbox(ctx, claim); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func resetOutboxBackoff(t *testing.T, store *Store, eventID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "relay-a", Role: "worker", WorkerID: "relay-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox_events SET next_attempt_at=to_timestamp(0) WHERE id=$1`, eventID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func outboxPending(t *testing.T, store *Store, jobID string) int {
	t.Helper()
	ctx := context.Background()
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "outbox-inspector", Role: "worker", WorkerID: "outbox-inspector"}); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE job_id=$1 AND state='pending'`, jobID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return pending
}
