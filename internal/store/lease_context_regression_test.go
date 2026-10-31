package store

import (
	"context"
	"contractaudit/internal/accounts"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestSupersededExecutionContextCannotAdoptReplacementClaim(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	oldCtx := withExecutionClaim(ctx, fixture.claim)
	expireDurableClaim(t, fixture)
	replacement, err := fixture.execution.ClaimNextJob(ctx, "replacement-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim replacement generation: %v", err)
	}
	if replacement.JobID != fixture.jobID || replacement.AttemptID != fixture.attemptID ||
		replacement.Generation != fixture.claim.Generation+1 || replacement.WorkerID == fixture.claim.WorkerID {
		t.Fatalf("replacement did not supersede the original claim: old=%+v new=%+v", fixture.claim, replacement)
	}
	before := durableMutationSnapshot(t, fixture, replacement)

	workerMismatch := replacement
	workerMismatch.WorkerID = fixture.claim.WorkerID
	generationMismatch := replacement
	generationMismatch.Generation = fixture.claim.Generation
	for _, tc := range []struct {
		name  string
		claim JobClaim
	}{
		{name: "old worker and generation", claim: fixture.claim},
		{name: "old worker only", claim: workerMismatch},
		{name: "old generation only", claim: generationMismatch},
	} {
		bound := withExecutionClaim(ctx, tc.claim)
		if _, err := fixture.execution.snapshotForAttemptContext(bound, fixture.attemptID); !errors.Is(err, ErrLeaseLost) {
			t.Errorf("%s read adopted the replacement claim: %v", tc.name, err)
		}
		if _, err := fixture.execution.snapshotForAttemptMetadataContext(bound, fixture.attemptID); !errors.Is(err, ErrLeaseLost) {
			t.Errorf("%s metadata read adopted the replacement claim: %v", tc.name, err)
		}
		called := false
		err := fixture.execution.updateExecutionContext(bound, fixture.attemptID, func(*state) error {
			called = true
			return nil
		})
		if !errors.Is(err, ErrLeaseLost) || called {
			t.Errorf("%s mutation adopted the replacement claim: called=%t err=%v", tc.name, called, err)
		}
	}
	if err := fixture.execution.markAttemptStarted(oldCtx, fixture.runID, fixture.attemptID); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("superseded context started the replacement attempt: %v", err)
	}
	if err := fixture.execution.appendExecutionEventFenced(oldCtx, fixture.runID, fixture.attemptID, ExecutionEvent{
		AttemptID: fixture.attemptID, NodeKind: "phase", Phase: "evidence", EventType: "node_completed", At: stamp(), Status: "completed",
	}); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("superseded context appended a replacement event: %v", err)
	}
	oldWorkCtx, cancelOldWork := context.WithCancel(oldCtx)
	cancelOldWork()
	if err := fixture.execution.finishAttemptAfterExecution(ctx, oldWorkCtx, fixture.runID, fixture.attemptID, "failed", "", "execution_failed"); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("superseded cleanup settled the replacement attempt: %v", err)
	}
	after := durableMutationSnapshot(t, fixture, replacement)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("superseded context changed durable state: before=%+v after=%+v", before, after)
	}
	currentCtx := withExecutionClaim(ctx, replacement)
	if _, err := fixture.execution.snapshotForAttemptContext(currentCtx, fixture.attemptID); err != nil {
		t.Fatalf("current context cannot read its attempt: %v", err)
	}
	if err := fixture.execution.markAttemptStarted(currentCtx, fixture.runID, fixture.attemptID); err != nil {
		t.Fatalf("current context cannot start its attempt: %v", err)
	}
}

func TestAttemptReadsAndCleanupRecheckDatabaseLease(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	oldCtx := withExecutionClaim(ctx, fixture.claim)
	if _, err := fixture.execution.snapshotForAttemptContext(oldCtx, fixture.attemptID); err != nil {
		t.Fatalf("live claim cannot read its attempt: %v", err)
	}
	expireDurableClaim(t, fixture)
	takeoverStore := fixture.environment.openRole(t, "execution")
	takeover, err := takeoverStore.ClaimNextJob(ctx, "independent-takeover", time.Minute)
	if err != nil {
		t.Fatalf("claim independent takeover: %v", err)
	}
	if takeover.JobID != fixture.jobID || takeover.Generation != fixture.claim.Generation+1 {
		t.Fatalf("independent takeover did not advance the generation: old=%+v new=%+v", fixture.claim, takeover)
	}
	before := durableMutationSnapshot(t, fixture, takeover)
	if _, err := fixture.execution.snapshotForAttemptContext(oldCtx, fixture.attemptID); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("expired database claim still read full attempt input: %v", err)
	}
	if _, err := fixture.execution.snapshotForAttemptMetadataContext(oldCtx, fixture.attemptID); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("expired database claim still read attempt metadata: %v", err)
	}
	cancelled, cancel := context.WithCancel(oldCtx)
	cancel()
	if err := fixture.execution.finishAttemptAfterExecution(ctx, cancelled, fixture.runID, fixture.attemptID, "failed", "", "execution_failed"); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("expired database claim settled a replacement attempt: %v", err)
	}
	after := durableMutationSnapshot(t, fixture, takeover)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("expired claim changed the replacement attempt: before=%+v after=%+v", before, after)
	}
	if _, err := takeoverStore.snapshotForAttemptContext(withExecutionClaim(ctx, takeover), fixture.attemptID); err != nil {
		t.Fatalf("independent takeover cannot read its attempt: %v", err)
	}
}

func TestAttemptAccessWithoutClaimDoesNotUseDemoTenant(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	bound := withExecutionClaim(ctx, fixture.claim)
	if _, err := fixture.execution.snapshotForAttemptContext(bound, fixture.attemptID); err != nil {
		t.Fatalf("live claim cannot read its attempt: %v", err)
	}
	before := durableMutationSnapshot(t, fixture, fixture.claim)
	fixture.execution.clearClaim(fixture.claim)
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "unbound", ctx: ctx, want: errExecutionEventScope},
		{name: "previously bound", ctx: bound, want: ErrLeaseLost},
	} {
		if _, err := fixture.execution.snapshotForAttemptContext(tc.ctx, fixture.attemptID); !errors.Is(err, tc.want) {
			t.Errorf("%s full attempt read without a claim: got %v, want %v", tc.name, err, tc.want)
		}
		if _, err := fixture.execution.snapshotForAttemptMetadataContext(tc.ctx, fixture.attemptID); !errors.Is(err, tc.want) {
			t.Errorf("%s metadata read without a claim: got %v, want %v", tc.name, err, tc.want)
		}
		called := false
		err := fixture.execution.updateExecutionContext(tc.ctx, fixture.attemptID, func(*state) error {
			called = true
			return nil
		})
		if !errors.Is(err, tc.want) || called {
			t.Errorf("%s mutation without a claim: called=%t err=%v, want %v", tc.name, called, err, tc.want)
		}
	}
	after := durableMutationSnapshot(t, fixture, fixture.claim)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("claimless access changed durable state: before=%+v after=%+v", before, after)
	}
	fixture.execution.setClaim(fixture.claim)
	if _, err := fixture.execution.snapshotForAttemptContext(bound, fixture.attemptID); err != nil {
		t.Fatalf("restored live claim cannot read its attempt: %v", err)
	}
}

// waitForLeaseBlock observes PostgreSQL's actual lock wait before cancellation.
func waitForLeaseBlock(t *testing.T, store *Store, holderPID int32, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		select {
		case err := <-done:
			t.Fatalf("worker returned before reaching the held lock: %v", err)
		case <-ctx.Done():
			t.Fatalf("worker did not reach the held lock: %v", ctx.Err())
		default:
		}
		var blocked bool
		err := store.pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND $1::integer=ANY(pg_blocking_pids(pid))
		)`, holderPID).Scan(&blocked)
		if err != nil {
			t.Fatalf("observe worker lock wait: %v", err)
		}
		if blocked {
			return
		}
	}
}

func TestCancelledWorkerLeavesNoResultWhileWaitingForCaseLock(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	before := durableMutationSnapshot(t, fixture, fixture.claim)
	versionBefore := durableAttemptVersion(t, fixture.execution, fixture.claim)
	lockTx, err := fixture.environment.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			if err := lockTx.Rollback(context.Background()); err != nil {
				t.Errorf("release held case lock: %v", err)
			}
		}
	}()
	var holderPID int32
	if err := lockTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if err := scopedCaseLock(ctx, lockTx, fixture.caseID); err != nil {
		t.Fatalf("hold case lock: %v", err)
	}
	workerBase, stopWorker := context.WithTimeout(ctx, 20*time.Second)
	defer stopWorker()
	workerCtx, cancelWorker := context.WithCancel(withExecutionClaim(workerBase, fixture.claim))
	defer cancelWorker()
	done := make(chan error, 1)
	go func() {
		done <- fixture.execution.markAttemptStarted(workerCtx, fixture.runID, fixture.attemptID)
	}()
	waitForLeaseBlock(t, fixture.environment.harness, holderPID, done)
	cancelWorker()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled lock wait returned %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled worker remained blocked on the case lock")
	}
	if err := lockTx.Rollback(ctx); err != nil {
		t.Fatalf("release held case lock: %v", err)
	}
	locked = false
	after := durableMutationSnapshot(t, fixture, fixture.claim)
	if !reflect.DeepEqual(after, before) || durableAttemptVersion(t, fixture.execution, fixture.claim) != versionBefore {
		t.Fatalf("cancelled lock wait changed the attempt or its results: before=%+v after=%+v", before, after)
	}
	if err := fixture.execution.markAttemptStarted(withExecutionClaim(ctx, fixture.claim), fixture.runID, fixture.attemptID); err != nil {
		t.Fatalf("live worker could not start after lock release: %v", err)
	}
}

func TestCancelledWorkCanSettleOnlyWithLiveParentAndLease(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	bound := withExecutionClaim(ctx, fixture.claim)
	if err := fixture.execution.markAttemptStarted(bound, fixture.runID, fixture.attemptID); err != nil {
		t.Fatalf("start live attempt: %v", err)
	}
	before := durableMutationSnapshot(t, fixture, fixture.claim)
	workCtx, cancelWork := context.WithCancel(bound)
	cancelWork()
	parent, cancelParent := context.WithCancel(ctx)
	cancelParent()
	if err := fixture.execution.finishAttemptAfterExecution(parent, workCtx, fixture.runID, fixture.attemptID, "failed", "", "execution_failed"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled parent allowed settlement: %v", err)
	}
	if after := durableMutationSnapshot(t, fixture, fixture.claim); !reflect.DeepEqual(after, before) {
		t.Fatalf("cancelled parent changed the attempt: before=%+v after=%+v", before, after)
	}
	if err := fixture.execution.finishAttemptAfterExecution(ctx, workCtx, fixture.runID, fixture.attemptID, "failed", "", "execution_failed"); err != nil {
		t.Fatalf("live parent and claim could not settle cancelled work: %v", err)
	}
	settled := durableMutationSnapshot(t, fixture, fixture.claim)
	if settled.Attempt.State != "failed" || settled.Attempt.FinishedAt == nil || settled.Attempt.ErrorCode == nil || *settled.Attempt.ErrorCode != "execution_failed" {
		t.Fatalf("bounded settlement did not persist the failure: %+v", settled.Attempt)
	}
}

func TestNarrowAttemptReadPreservesFrozenParseAndInitiatingActor(t *testing.T) {
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer fixture is unavailable")
	}
	fixture, ok := newDurableFixtureAs(t, reviewer)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := withExecutionClaim(context.Background(), fixture.claim)
	if fixture.claim.ActorID != fixture.actor.ID {
		t.Fatalf("claim actor %q, want %q", fixture.claim.ActorID, fixture.actor.ID)
	}
	if err := fixture.execution.markAttemptStarted(ctx, fixture.runID, fixture.attemptID); err != nil {
		t.Fatalf("reviewer could not start its attempt: %v", err)
	}
	before, err := fixture.execution.snapshotForAttemptContext(ctx, fixture.attemptID)
	if err != nil {
		t.Fatalf("read accepted attempt: %v", err)
	}
	attempt, found := before.attemptByID(fixture.attemptID)
	if !found || attempt.ActorID != fixture.actor.ID {
		t.Fatalf("narrow read lost the initiating actor: %+v", attempt)
	}
	actor, err := authorizeAttemptExecution(before, fixture.runID, fixture.attemptID)
	if err != nil || actor.ID != fixture.actor.ID {
		t.Fatalf("narrow read cannot authorize the initiating reviewer: actor=%+v err=%v", actor, err)
	}
	selections := before.parseSelectionsForAttempt(fixture.attemptID)
	if len(selections) != 2 {
		t.Fatalf("narrow read lost accepted parse selections: %+v", selections)
	}
	prepared, err := fixture.execution.prepareAttempt(ctx, fixture.runID, fixture.attemptID, fixture.execution.currentModels())
	if err != nil {
		t.Fatalf("prepare accepted attempt: %v", err)
	}
	wantText := map[string]string{
		"procurement":    "采购文件要求质保36个月。合同总价为100万元。",
		"draft_contract": "拟签合同约定质保24个月。合同总价为120万元。",
	}
	if len(prepared) != len(wantText) {
		t.Fatalf("prepared %d documents, want %d", len(prepared), len(wantText))
	}
	for _, doc := range prepared {
		want, exists := wantText[doc.Document.Role]
		if !exists || !doc.Ready || doc.Text != want {
			t.Fatalf("prepared document did not retain its accepted source: %+v", doc)
		}
		delete(wantText, doc.Document.Role)
	}
	if len(wantText) != 0 {
		t.Fatalf("accepted documents were omitted: %v", wantText)
	}
	frozenState, err := fixture.execution.snapshotForAttemptContext(ctx, fixture.attemptID)
	if err != nil {
		t.Fatalf("read frozen attempt: %v", err)
	}
	input, found := frozenState.attemptInput(fixture.attemptID)
	if !found || input.RunID != fixture.runID || len(input.Documents) != 2 || !attemptSelectionsMatchFrozen(selections, input.Documents) {
		t.Fatalf("narrow read lost the frozen input or parse identity: %+v", input)
	}
	for _, doc := range input.Documents {
		parsed, exists := frozenState.parsedByID(doc.ParsedDocumentID)
		if !exists || parsed.DocumentID != doc.Document.ID || parsed.ArtifactDigest != doc.ParsedArtifactDigest || parsed.Text != doc.Text {
			t.Fatalf("selected parsed artifact is absent or differs from frozen input: frozen=%+v parsed=%+v", doc, parsed)
		}
	}
	if err := fixture.document.Reparse(context.Background(), fixture.admin, fixture.caseID, input.Documents[0].Document.ID); err != nil {
		t.Fatalf("append a later document parse: %v", err)
	}
	full := durableSnapshot(t, fixture.execution, fixture.claim)
	latest, found := full.latestParsed(input.Documents[0].Document.ID, "")
	if !found || latest.ID == input.Documents[0].ParsedDocumentID {
		t.Fatalf("later reparse did not produce a distinct artifact: %+v", latest)
	}
	recovered, err := fixture.execution.prepareAttempt(ctx, fixture.runID, fixture.attemptID, fixture.execution.currentModels())
	if err != nil || len(recovered) != len(input.Documents) {
		t.Fatalf("attempt preparation did not reuse the frozen parse: after=%+v err=%v", recovered, err)
	}
	remaining := make(map[string]FrozenDocument, len(input.Documents))
	for _, doc := range input.Documents {
		remaining[doc.Document.ID] = doc
	}
	for _, doc := range recovered {
		frozen, exists := remaining[doc.Document.ID]
		if !exists || !doc.Ready || doc.Text != frozen.Text || doc.ParsedDocument == nil ||
			doc.ParsedDocument.ID != frozen.ParsedDocumentID || doc.ParsedDocument.ArtifactDigest != frozen.ParsedArtifactDigest {
			t.Fatalf("recovered document differs from its frozen parse: recovered=%+v frozen=%+v", doc, frozen)
		}
		delete(remaining, doc.Document.ID)
	}
	if len(remaining) != 0 {
		t.Fatalf("recovery omitted frozen documents: %+v", remaining)
	}
	after, err := fixture.execution.snapshotForAttemptContext(ctx, fixture.attemptID)
	if err != nil {
		t.Fatalf("read attempt after reparse: %v", err)
	}
	afterAttempt, found := after.attemptByID(fixture.attemptID)
	afterInput, hasInput := after.attemptInput(fixture.attemptID)
	if !found || afterAttempt.ActorID != fixture.actor.ID || !hasInput || !reflect.DeepEqual(afterInput, input) ||
		!reflect.DeepEqual(after.parseSelectionsForAttempt(fixture.attemptID), selections) {
		t.Fatalf("later parse changed the actor or frozen input: attempt=%+v input=%+v", afterAttempt, afterInput)
	}
}

func TestCaseWriteDoesNotBlockOtherCaseOrDiscardProjection(t *testing.T) {
	fixture, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	caseB, err := fixture.core.CreateCase(fixture.admin, "并发写入的独立案件", "procurement_ict")
	if err != nil {
		t.Fatalf("create second case: %v", err)
	}
	const memberID = "demo-reviewer"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	aDone := make(chan error, 1)
	bDone := make(chan error, 1)
	aFinished, bStarted, bFinished := false, false, false
	defer func() {
		unblock()
		cancel()
		if !aFinished {
			select {
			case err := <-aDone:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("case A cleanup: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("case A writer did not exit")
			}
		}
		if bStarted && !bFinished {
			select {
			case err := <-bDone:
				if err != nil {
					t.Errorf("case B cleanup: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("case B writer did not exit")
			}
		}
	}()
	go func() {
		aDone <- fixture.core.scopedCaseMutation(ctx, fixture.admin, fixture.caseID, func(st *state) error {
			for i := range st.Cases {
				if st.Cases[i].ID == fixture.caseID {
					st.Cases[i].MemberIDs = append(st.Cases[i].MemberIDs, memberID)
					st.Cases[i].UpdatedAt = stamp()
					close(entered)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
			return errors.New("case A missing from its mutation scope")
		})
	}()
	select {
	case <-entered:
	case err := <-aDone:
		aFinished = true
		t.Fatalf("case A failed before acquiring its lock: %v", err)
	case <-ctx.Done():
		t.Fatalf("case A did not acquire its lock: %v", ctx.Err())
	}
	bStarted = true
	go func() { bDone <- fixture.core.AddMember(fixture.admin, caseB, memberID) }()
	select {
	case err := <-bDone:
		bFinished = true
		if err != nil {
			t.Fatalf("case B write failed while case A held its lock: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("case B write waited for the unrelated case A lock")
	}
	unblock()
	select {
	case err := <-aDone:
		aFinished = true
		if err != nil {
			t.Fatalf("case A write failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("case A write did not complete: %v", ctx.Err())
	}
	fixture.core.mu.Lock()
	cachedA, hasA := fixture.core.st.caseByID(fixture.caseID)
	cachedB, hasB := fixture.core.st.caseByID(caseB)
	fixture.core.mu.Unlock()
	if !hasA || !hasB || !slices.Contains(cachedA.MemberIDs, memberID) || !slices.Contains(cachedB.MemberIDs, memberID) {
		t.Fatalf("case projection discarded a committed write: A=%+v B=%+v", cachedA, cachedB)
	}
	for _, caseID := range []string{fixture.caseID, caseB} {
		detail, err := fixture.core.GetCase(fixture.admin, caseID)
		if err != nil || !slices.Contains(detail.Case.MemberIDs, memberID) {
			t.Fatalf("case %q did not retain its committed member: detail=%+v err=%v", caseID, detail, err)
		}
	}
}
