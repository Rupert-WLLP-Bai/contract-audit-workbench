// File metadata (manifest mirror):
// Purpose: Checks checkpoint persistence, lease generation fencing, cross-tenant RLS, revoked access, terminal attempts and immutable frozen input bindings.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	executionpostgres "contractaudit/internal/execution/postgres"
	"contractaudit/internal/execution/steps"
	"contractaudit/internal/model"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

type revokedAgentStepAuthority struct{}

func (revokedAgentStepAuthority) ResolveOperation(context.Context, model.DelegatedScope) (accounts.User, error) {
	return accounts.User{}, errAttemptAuthorizationRevoked
}

func TestAgentStepCheckpointTakeoverReuseAndFences(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	claimCtx := withExecutionClaim(ctx, fixture.claim)
	if err := fixture.execution.markAttemptStarted(claimCtx, fixture.runID, fixture.attemptID); err != nil {
		t.Fatalf("start durable attempt: %v", err)
	}
	if _, err := fixture.execution.prepareAttempt(claimCtx, fixture.runID, fixture.attemptID, &frozenOCRModel{}); err != nil {
		t.Fatalf("freeze durable attempt input: %v", err)
	}
	state, err := fixture.execution.snapshotForAttemptContext(claimCtx, fixture.attemptID)
	if err != nil {
		t.Fatalf("read frozen attempt: %v", err)
	}
	input, found := state.attemptInput(fixture.attemptID)
	if !found || input.InputHash == "" {
		t.Fatal("durable attempt input was not frozen")
	}
	recorder := attemptRecorder{store: fixture.execution, runID: fixture.runID, attemptID: fixture.attemptID, failure: &recorderFailure{}}
	journal := newAgentStepJournal(fixture.execution, fixture.claim, input.InputHash, recorder)
	identity := steps.Identity{
		StepID: "tool/test-read", Kind: "tool", Round: 1, RuntimeVersion: "agent-runtime-test",
		PlannerID: "planner-test", PlannerConfigurationID: "planner-config-test", ToolCatalogVersion: "catalog-test",
		ToolCatalogSHA256: strings.Repeat("a", 64), ToolID: "test.read", ToolVersion: "v1",
		ToolConfigurationSHA256: strings.Repeat("b", 64), InputSHA256: strings.Repeat("c", 64),
	}
	started, execute, err := journal.Begin(claimCtx, identity)
	if err != nil || !execute || started.Status != steps.StatusStarted {
		t.Fatalf("begin first generation step: record=%+v execute=%v err=%v", started, execute, err)
	}
	observedAt := time.Now().UTC()
	outcome := steps.Outcome{Status: steps.StatusSucceeded, Payload: []byte(`{"finding":"bounded"}`),
		ObservedAt: observedAt, ElapsedMS: 17, CallsConsumed: 1, Usage: steps.Usage{Status: steps.UsageUnknown}}
	if err := journal.Finish(claimCtx, identity, outcome); err != nil {
		t.Fatalf("persist first generation step: %v", err)
	}
	if err := fixture.execution.HeartbeatJob(ctx, fixture.claim, time.Minute); err != nil {
		t.Fatalf("heartbeat before takeover: %v", err)
	}
	expireDurableClaim(t, fixture)
	takeover, err := fixture.execution.ClaimNextJob(ctx, "agent-step-takeover", time.Minute)
	if err != nil || takeover.Generation != fixture.claim.Generation+1 {
		t.Fatalf("take over durable Agent attempt: claim=%+v err=%v", takeover, err)
	}
	takeoverCtx := withExecutionClaim(ctx, takeover)
	takeoverRecorder := attemptRecorder{store: fixture.execution, runID: fixture.runID, attemptID: fixture.attemptID, failure: &recorderFailure{}}
	takeoverJournal := newAgentStepJournal(fixture.execution, takeover, input.InputHash, takeoverRecorder)
	summary, err := takeoverJournal.RecoverySummary(takeoverCtx)
	if err != nil || summary.ElapsedMS != outcome.ElapsedMS || summary.KnowledgeReadCount != 0 {
		t.Fatalf("read recovery summary: %+v err=%v", summary, err)
	}
	reused, execute, err := takeoverJournal.Begin(takeoverCtx, identity)
	if err != nil || execute || reused.Status != steps.StatusSucceeded || reused.Scope.Generation != fixture.claim.Generation ||
		!reused.ElapsedKnown || reused.Usage.Status != steps.UsageUnknown || string(reused.Payload) != string(outcome.Payload) ||
		reused.ObservedAt == nil || !reused.ObservedAt.Equal(observedAt) {
		t.Fatalf("takeover did not reuse the immutable original step: record=%+v execute=%v err=%v", reused, execute, err)
	}
	if _, err := journal.RecoverySummary(claimCtx); !errors.Is(err, errLeaseLost) {
		t.Fatalf("old generation read persisted step data after takeover: %v", err)
	}
	changedIdentity := identity
	changedIdentity.InputSHA256 = strings.Repeat("d", 64)
	if _, _, err := takeoverJournal.Begin(takeoverCtx, changedIdentity); !errors.Is(err, steps.ErrIdentityMismatch) {
		t.Fatalf("changed step input replayed an existing result: %v", err)
	}
	crossTenantTx, err := fixture.execution.beginContextTx(takeoverCtx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin cross-tenant checkpoint fence transaction: %v", err)
	}
	defer crossTenantTx.Rollback(takeoverCtx)
	if err := setLocalScope(takeoverCtx, crossTenantTx, takeover.scope()); err != nil {
		t.Fatalf("set current worker scope for cross-tenant checkpoint fence: %v", err)
	}
	crossTenantScope := steps.Scope{TenantID: fixture.claim.TenantID + "-other", OrgID: fixture.claim.OrgID,
		CaseID: fixture.caseID, RunID: fixture.runID, AttemptID: fixture.attemptID,
		Generation: takeover.Generation, AttemptInputSHA256: input.InputHash}
	crossTenantIdentity := identity
	crossTenantIdentity.StepID = "tool/cross-tenant"
	if _, _, err := executionpostgres.NewStepCheckpoints(platformpostgres.Bind(crossTenantTx)).Begin(takeoverCtx, crossTenantScope, crossTenantIdentity); err == nil {
		t.Fatal("execution role wrote a checkpoint under a different tenant scope")
	}
	wrongGenerationTx, err := fixture.execution.beginContextTx(takeoverCtx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin wrong-generation checkpoint fence transaction: %v", err)
	}
	defer wrongGenerationTx.Rollback(takeoverCtx)
	if err := setLocalScope(takeoverCtx, wrongGenerationTx, takeover.scope()); err != nil {
		t.Fatalf("set current worker scope for wrong-generation checkpoint fence: %v", err)
	}
	wrongGenerationScope := steps.Scope{TenantID: fixture.claim.TenantID, OrgID: fixture.claim.OrgID,
		CaseID: fixture.caseID, RunID: fixture.runID, AttemptID: fixture.attemptID,
		Generation: takeover.Generation + 1, AttemptInputSHA256: input.InputHash}
	wrongGenerationIdentity := identity
	wrongGenerationIdentity.StepID = "tool/wrong-generation"
	if _, _, err := executionpostgres.NewStepCheckpoints(platformpostgres.Bind(wrongGenerationTx)).Begin(takeoverCtx, wrongGenerationScope, wrongGenerationIdentity); err == nil {
		t.Fatal("execution role wrote a checkpoint for a generation other than the active claim")
	}
	fixture.execution.UseOperationAuthority(revokedAgentStepAuthority{})
	if _, _, err := takeoverJournal.Begin(takeoverCtx, identity); !errors.Is(err, errAttemptAuthorizationRevoked) {
		t.Fatalf("revoked attempt authority replayed a successful step: %v", err)
	}
	fixture.execution.UseOperationAuthority(durableTestOperationAuthority{core: fixture.core})
	if err := fixture.execution.finishAttemptAfterExecution(ctx, takeoverCtx, fixture.runID, fixture.attemptID, "partial", "checkpoint fence test", "checkpoint_fence_test"); err != nil {
		t.Fatalf("finish attempt for terminal fence: %v", err)
	}
	if _, _, err := takeoverJournal.Begin(takeoverCtx, steps.Identity{
		StepID: "tool/after-terminal", Kind: "tool", Round: 1, RuntimeVersion: "agent-runtime-test",
		PlannerID: "planner-test", PlannerConfigurationID: "planner-config-test", ToolCatalogVersion: "catalog-test",
		ToolCatalogSHA256: strings.Repeat("a", 64), ToolID: "test.read", ToolVersion: "v1",
		ToolConfigurationSHA256: strings.Repeat("b", 64), InputSHA256: strings.Repeat("d", 64),
	}); !errors.Is(err, errAttemptTerminal) {
		t.Fatalf("terminal attempt accepted a new Agent step: %v", err)
	}
}
