package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

func queueTestActors(t *testing.T) []accounts.User {
	t.Helper()
	admin, _ := accounts.ByID("demo-admin")
	users := []accounts.User{admin, admin}
	for i := range users {
		users[i].ID = fmt.Sprintf("queue-actor-%d", i)
		users[i].TenantID = fmt.Sprintf("queue-tenant-%d", i)
		users[i].OrgID = "queue-org"
		users[i].Issuer = "https://queue.invalid"
		users[i].Subject = users[i].ID
		users[i].Grants = []access.Grant{{Role: "case_owner", Scope: "self"}}
	}
	body, err := json.Marshal(map[string]any{"users": users})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "directory.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Error(err)
		}
	})
	return users
}

func queueTestAcceptance(t *testing.T, core, document, execution *Store, user accounts.User) (string, JobAcceptance, error) {
	t.Helper()
	caseID, err := core.CreateCaseWithBusiness(user, "队列公平调度", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := document.Generate(context.Background(), user, caseID, telecomTemplateID, validTelecomTemplateFields()); err != nil {
		t.Fatal(err)
	}
	if _, err := document.AddDocument(context.Background(), user, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件缺少阈值，审查须明确缺证。")); err != nil {
		t.Fatal(err)
	}
	accepted, err := execution.StartReviewAsync(context.Background(), user, caseID)
	return caseID, accepted, err
}

func TestTenantFairnessAcrossWorkers(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	users := queueTestActors(t)
	core, document := environment.openRole(t, "core"), environment.openRole(t, "document")
	workers := []*Store{environment.openRole(t, "execution"), environment.openRole(t, "execution")}
	policy := DefaultQueuePolicy()
	policy.GlobalInFlight, policy.TenantInFlight = 1, 1
	policy.Weights = map[string]int{users[0].TenantID: 2, users[1].TenantID: 1}
	for _, worker := range workers {
		if err := worker.UseQueuePolicy(policy); err != nil {
			t.Fatal(err)
		}
	}
	for i, n := range []int{6, 3} {
		for range n {
			if _, _, err := queueTestAcceptance(t, core, document, workers[0], users[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx := context.Background()
	for i, want := range []int{0, 0, 1, 0, 0, 1, 0, 0, 1} {
		if i == 3 { // A new connection/process model must preserve the shared round.
			workers[0] = environment.openRole(t, "execution")
			if err := workers[0].UseQueuePolicy(policy); err != nil {
				t.Fatal(err)
			}
		}
		worker := workers[i%2]
		claim, err := worker.ClaimNextJob(ctx, fmt.Sprintf("fair-%d", i), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if claim.TenantID != users[want].TenantID {
			t.Fatalf("turn %d: got %s want %s", i, claim.TenantID, users[want].TenantID)
		}
		if _, err := workers[(i+1)%2].ClaimNextJob(ctx, "over-cap", time.Minute); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("shared cap exceeded: %v", err)
		}
		if err := worker.CancelReview(ctx, users[want], claim.CaseID, claim.RunID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueueTenantCapacitySkipsSaturatedTenant(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	users := queueTestActors(t)
	core, document := environment.openRole(t, "core"), environment.openRole(t, "document")
	workers := []*Store{environment.openRole(t, "execution"), environment.openRole(t, "execution")}
	policy := DefaultQueuePolicy()
	policy.GlobalInFlight = 2
	policy.TenantInFlight = 1
	for _, worker := range workers {
		if err := worker.UseQueuePolicy(policy); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range users {
		for range 2 {
			if _, _, err := queueTestAcceptance(t, core, document, workers[0], user); err != nil {
				t.Fatal(err)
			}
		}
	}
	start := make(chan struct{})
	type result struct {
		claim JobClaim
		err   error
	}
	results := make(chan result, 4)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := workers[i%2].ClaimNextJob(context.Background(), fmt.Sprintf("capacity-%d", i), time.Minute)
			results <- result{claim, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	seen := map[string]int{}
	for result := range results {
		if errors.Is(result.err, pgx.ErrNoRows) {
			continue
		}
		if result.err != nil {
			t.Fatal(result.err)
		}
		seen[result.claim.TenantID]++
	}
	if seen[users[0].TenantID] != 1 || seen[users[1].TenantID] != 1 {
		t.Fatalf("shared reservations violated tenant/global caps: %+v", seen)
	}
}

func TestQueueFullRollsBackAcceptanceAndKeepsIdempotency(t *testing.T) {
	setup, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	execution := setup.environment.openRole(t, "execution")
	policy := DefaultQueuePolicy()
	policy.GlobalInFlight = 1
	policy.TenantInFlight = 1
	policy.GlobalPending = 1
	policy.TenantPending = 1
	if err := execution.UseQueuePolicy(policy); err != nil {
		t.Fatal(err)
	}
	accepted, err := execution.StartReviewAsync(context.Background(), setup.admin, setup.caseID)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := execution.StartReviewAsync(context.Background(), setup.admin, setup.caseID)
	if err != nil || repeated != accepted {
		t.Fatalf("full queue broke idempotent acceptance: %+v %v", repeated, err)
	}
	caseID, _, err := queueTestAcceptance(t, setup.core, setup.document, execution, setup.admin)
	var failure *fault.Error
	if !errors.As(err, &failure) || failure.Code != "execution_capacity" {
		t.Fatalf("full queue admitted task: %v", err)
	}
	var runs, jobs, outbox int
	if err := setup.environment.harness.pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM review_runs WHERE case_id=$1),
		(SELECT count(*) FROM worker_jobs WHERE case_id=$1),
		(SELECT count(*) FROM outbox_events WHERE case_id=$1)`, caseID).Scan(&runs, &jobs, &outbox); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || jobs != 0 || outbox != 0 {
		t.Fatalf("partial acceptance survived rollback: runs=%d jobs=%d outbox=%d", runs, jobs, outbox)
	}
	other := setup.environment.openRole(t, "execution")
	if _, err := other.ClaimNextJob(context.Background(), "mismatched", time.Minute); !errors.As(err, &failure) || failure.Code != "queue_policy_mismatch" {
		t.Fatalf("different replica policy was accepted: %v", err)
	}
}

func TestQueueBackoffAndExhaustionNeverExecuteAgain(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	ctx := context.Background()
	if _, err := fixture.environment.harness.pool.Exec(ctx, `UPDATE worker_jobs SET lease_until=clock_timestamp()-interval '1 second',ready_at=clock_timestamp()+interval '1 hour' WHERE id=$1`, fixture.jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.execution.ClaimNextJob(ctx, "too-early", time.Minute); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("backoff bypassed: %v", err)
	}
	if _, err := fixture.environment.harness.pool.Exec(ctx, `UPDATE worker_jobs SET ready_at=clock_timestamp()-interval '1 second',claim_count=max_claims WHERE id=$1`, fixture.jobID); err != nil {
		t.Fatal(err)
	}
	claim, err := fixture.execution.ClaimNextJob(ctx, "cleanup", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatal(err)
	}
	st := durableSnapshot(t, fixture.execution, claim)
	attempt, found := st.attemptByID(claim.AttemptID)
	if !found || attempt.State != "failed" || attempt.ErrorCode == nil || *attempt.ErrorCode != "execution_claims_exhausted" {
		t.Fatalf("exhausted attempt did not fail durably: %+v", attempt)
	}
	if _, found := st.attemptInput(claim.AttemptID); found {
		t.Fatal("exhausted job prepared input again")
	}
	for _, event := range st.executionEvents(claim.AttemptID) {
		if event.EventType == "attempt_started" || event.NodeKind == "model_call" || event.NodeKind == "tool_call" {
			t.Fatalf("exhausted job executed: %+v", event)
		}
	}
	if _, err := fixture.execution.ClaimNextJob(ctx, "after-terminal", time.Minute); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("terminal job reclaimed: %v", err)
	}
}
