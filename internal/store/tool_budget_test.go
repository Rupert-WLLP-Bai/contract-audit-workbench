package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func newFrozenToolBudgetFixture(t *testing.T) (durableFixture, bool) {
	t.Helper()
	fixture, ok := newDurableTelecomFixture(t)
	if !ok {
		return durableFixture{}, false
	}
	if err := fixture.execution.RunClaimedJob(context.Background(), fixture.claim); err != nil {
		t.Fatalf("finish telecom attempt for tool budget: %v", err)
	}
	return fixture, true
}

type toolBudgetScope struct {
	caseID    string
	runID     string
	attemptID string
}

func toolBudgetScopeFor(fixture durableFixture) toolBudgetScope {
	return toolBudgetScope{caseID: fixture.caseID, runID: fixture.runID, attemptID: fixture.attemptID}
}

func toolBudgetRequest(scope toolBudgetScope) ToolCallRequest {
	return ToolCallRequest{
		CaseID: scope.caseID, RunID: scope.runID, AttemptID: scope.attemptID,
		CapabilityID: "dedicated_line.minimum_bandwidth", ToolVersion: toolCallVersion,
		Deadline: time.Now().UTC().Add(50 * time.Second),
	}
}

func createFrozenToolBudgetAttempt(t *testing.T, fixture durableFixture, index int) toolBudgetScope {
	t.Helper()
	caseID, err := fixture.core.CreateCaseWithBusiness(fixture.admin, "工具预算跨案件测试", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create telecom case %d: %v", index, err)
	}
	if _, err := fixture.document.Generate(context.Background(), fixture.admin, caseID, telecomTemplateID, validTelecomTemplateFields()); err != nil {
		t.Fatalf("generate telecom draft %d: %v", index, err)
	}
	if _, err := fixture.document.AddDocument(context.Background(), fixture.admin, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件未提供合同阈值或审批依据。")); err != nil {
		t.Fatalf("add procurement document %d: %v", index, err)
	}
	accepted, err := fixture.execution.StartReviewAsyncAt(context.Background(), fixture.admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("accept telecom review %d: %v", index, err)
	}
	claim, err := fixture.execution.ClaimNextJob(context.Background(), "tool-budget-worker-"+time.Now().UTC().Format("150405.000000000"), time.Minute)
	if err != nil {
		t.Fatalf("claim telecom review %d: %v", index, err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed job %q, want %q", claim.JobID, accepted.JobID)
	}
	if err := fixture.execution.RunClaimedJob(context.Background(), claim); err != nil {
		t.Fatalf("freeze telecom attempt %d: %v", index, err)
	}
	return toolBudgetScope{caseID: caseID, runID: accepted.RunID, attemptID: accepted.AttemptID}
}

func requireToolBudgetFault(t *testing.T, err error, status int, code string) {
	t.Helper()
	var got *fault.Error
	if !errors.As(err, &got) || got.Status != status || got.Code != code {
		t.Fatalf("tool budget error = %v, want status=%d code=%q", err, status, code)
	}
}

func TestToolCallAttemptBudgetIsSharedAcrossPools(t *testing.T) {
	fixture, ok := newFrozenToolBudgetFixture(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	first := fixture.environment.openRole(t, "tool")
	second := fixture.environment.openRole(t, "tool")
	pools := []*Store{first, second}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	const requests = 40
	type result struct {
		callID string
		err    error
	}
	results := make(chan result, requests)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range requests {
		pool := pools[i%len(pools)]
		workers.Add(1)
		go func(pool *Store) {
			defer workers.Done()
			<-start
			callID, err := pool.ReserveToolCall(ctx, fixture.admin, toolBudgetRequest(toolBudgetScopeFor(fixture)))
			results <- result{callID: callID, err: err}
		}(pool)
	}
	close(start)
	workers.Wait()
	close(results)

	admitted, rejected := 0, 0
	for result := range results {
		if result.err == nil {
			if result.callID == "" {
				t.Fatal("successful reservation returned an empty call identity")
			}
			admitted++
			continue
		}
		requireToolBudgetFault(t, result.err, 429, "tool_quota_exceeded")
		rejected++
	}
	if admitted != toolCallAttemptLimit || rejected != requests-toolCallAttemptLimit {
		t.Fatalf("shared attempt budget admitted=%d rejected=%d, want %d and %d", admitted, rejected, toolCallAttemptLimit, requests-toolCallAttemptLimit)
	}
}

func TestToolCallTenantMinuteBudgetIsSharedAcrossPoolsAndAttempts(t *testing.T) {
	fixture, ok := newFrozenToolBudgetFixture(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	scopes := []toolBudgetScope{toolBudgetScopeFor(fixture)}
	for i := 1; i < 4; i++ {
		scopes = append(scopes, createFrozenToolBudgetAttempt(t, fixture, i))
	}
	pools := []*Store{
		fixture.environment.openRole(t, "tool"),
		fixture.environment.openRole(t, "tool"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()

	type request struct {
		poolIndex  int
		scopeIndex int
	}
	requests := make([]request, 0, toolCallTenantMinuteLimit+1)
	for scopeIndex := range scopes {
		count := toolCallAttemptLimit
		if scopeIndex == len(scopes)-1 {
			count = toolCallTenantMinuteLimit - toolCallAttemptLimit*(len(scopes)-1) + 1
		}
		for range count {
			requests = append(requests, request{poolIndex: len(requests) % len(pools), scopeIndex: scopeIndex})
		}
	}

	type result struct {
		scopeIndex int
		callID     string
		err        error
	}
	results := make(chan result, len(requests))
	start := make(chan struct{})
	var workers sync.WaitGroup
	const workerCount = 8
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		workers.Add(1)
		go func(offset int) {
			defer workers.Done()
			<-start
			for i := offset; i < len(requests); i += workerCount {
				req := requests[i]
				callID, err := pools[req.poolIndex].ReserveToolCall(ctx, fixture.admin, toolBudgetRequest(scopes[req.scopeIndex]))
				results <- result{scopeIndex: req.scopeIndex, callID: callID, err: err}
			}
		}(workerIndex)
	}
	close(start)
	workers.Wait()
	close(results)

	perAttempt := make([]int, len(scopes))
	admitted, rejected := 0, 0
	for result := range results {
		if result.err == nil {
			if result.callID == "" {
				t.Fatal("successful reservation returned an empty call identity")
			}
			admitted++
			perAttempt[result.scopeIndex]++
			continue
		}
		requireToolBudgetFault(t, result.err, 429, "tool_quota_exceeded")
		rejected++
	}
	if admitted != toolCallTenantMinuteLimit || rejected != 1 {
		t.Fatalf("shared tenant-minute budget admitted=%d rejected=%d, want %d and 1", admitted, rejected, toolCallTenantMinuteLimit)
	}
	for index, count := range perAttempt {
		if count > toolCallAttemptLimit {
			t.Fatalf("attempt %d consumed %d calls, above the per-attempt limit %d", index, count, toolCallAttemptLimit)
		}
	}
}

func TestToolCallSettlementIsTerminalAndRechecksScope(t *testing.T) {
	fixture, ok := newFrozenToolBudgetFixture(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	tool := fixture.environment.openRole(t, "tool")
	ctx := context.Background()
	scope := toolBudgetScopeFor(fixture)

	invalid := toolBudgetRequest(scope)
	invalid.CapabilityID = "unsupported.capability"
	_, err := tool.ReserveToolCall(ctx, fixture.admin, invalid)
	requireToolBudgetFault(t, err, 422, "invalid")

	stale := toolBudgetRequest(scope)
	stale.AttemptID = "stale-attempt"
	_, err = tool.ReserveToolCall(ctx, fixture.admin, stale)
	requireToolBudgetFault(t, err, 404, "not_found")

	viewer, found := accounts.ByID("demo-viewer")
	if !found {
		t.Fatal("demo viewer is unavailable")
	}
	_, err = tool.ReserveToolCall(ctx, viewer, toolBudgetRequest(scope))
	requireToolBudgetFault(t, err, 404, "not_found")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = tool.ReserveToolCall(cancelled, fixture.admin, toolBudgetRequest(scope))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission error = %v, want context.Canceled", err)
	}

	failedID, err := tool.ReserveToolCall(ctx, fixture.admin, toolBudgetRequest(scope))
	if err != nil || failedID == "" {
		t.Fatalf("reserve failed call: id=%q err=%v", failedID, err)
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, failedID, "completed", 0); err == nil {
		t.Fatal("completed settlement without output bytes was accepted")
	} else {
		requireToolBudgetFault(t, err, 422, "invalid")
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, failedID, "failed", 0); err != nil {
		t.Fatalf("settle failed call: %v", err)
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, failedID, "failed", 0); err == nil {
		t.Fatal("duplicate settlement was accepted")
	} else {
		requireToolBudgetFault(t, err, 409, "conflict")
	}

	completedID, err := tool.ReserveToolCall(ctx, fixture.admin, toolBudgetRequest(scope))
	if err != nil || completedID == "" {
		t.Fatalf("reserve completed call: id=%q err=%v", completedID, err)
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, completedID, "completed", 128); err != nil {
		t.Fatalf("settle completed call: %v", err)
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, completedID, "completed", 128); err == nil {
		t.Fatal("duplicate completed settlement was accepted")
	} else {
		requireToolBudgetFault(t, err, 409, "conflict")
	}

	tooLargeID, err := tool.ReserveToolCall(ctx, fixture.admin, toolBudgetRequest(scope))
	if err != nil || tooLargeID == "" {
		t.Fatalf("reserve bounded-output call: id=%q err=%v", tooLargeID, err)
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, tooLargeID, "completed", toolCallOutputLimit+1); err == nil {
		t.Fatal("settlement above the output limit was accepted")
	} else {
		requireToolBudgetFault(t, err, 413, "tool_output_too_large")
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, tooLargeID, "cancelled", 0); err != nil {
		t.Fatalf("settle cancelled call after invalid output report: %v", err)
	}
}

func TestToolCallCancellationSettlesReservedCallAndRejectsStaleScope(t *testing.T) {
	fixture, ok := newDurableTelecomFixture(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	gate := newHeldReviewModel(1)
	fixture.execution.UseModels(gate)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	workerDone := make(chan struct{})
	t.Cleanup(func() {
		gate.unblock()
		select {
		case <-workerDone:
		case <-time.After(10 * time.Second):
			t.Error("worker did not exit after cancellation test cleanup")
		}
	})
	go func() {
		defer close(workerDone)
		_ = fixture.execution.RunClaimedJob(ctx, fixture.claim)
	}()
	waitSignal(t, gate.entered, "telecom model before cancellation")

	tool := fixture.environment.openRole(t, "tool")
	scope := toolBudgetScopeFor(fixture)
	callID, err := tool.ReserveToolCall(ctx, fixture.admin, toolBudgetRequest(scope))
	if err != nil || callID == "" {
		t.Fatalf("reserve call during active frozen attempt: id=%q err=%v", callID, err)
	}
	if err := fixture.execution.CancelReview(ctx, fixture.admin, fixture.caseID, fixture.runID); err != nil {
		t.Fatalf("cancel active frozen attempt: %v", err)
	}
	if _, err := tool.ReserveToolCall(ctx, fixture.admin, toolBudgetRequest(scope)); err == nil {
		t.Fatal("cancelled attempt admitted a new tool call")
	} else {
		requireToolBudgetFault(t, err, 409, "conflict")
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, callID, "completed", 1); err == nil {
		t.Fatal("completed output was accepted after the frozen attempt was cancelled")
	} else {
		requireToolBudgetFault(t, err, 409, "conflict")
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, callID, "cancelled", 0); err != nil {
		t.Fatalf("cancelled reservation did not settle: %v", err)
	}
	if err := tool.FinishToolCall(ctx, fixture.admin, callID, "cancelled", 0); err == nil {
		t.Fatal("cancelled reservation was settled twice")
	} else {
		requireToolBudgetFault(t, err, 409, "conflict")
	}

	gate.unblock()
	select {
	case <-workerDone:
	case <-ctx.Done():
		t.Fatalf("worker did not exit after cancellation: %v", ctx.Err())
	}
}
