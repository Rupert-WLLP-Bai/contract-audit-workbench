// File metadata (manifest mirror):
// Purpose: Exercises current read authority, facts-only discriminator handling and immutable source/configuration validation for persisted facts projections.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/model"
)

type factsProjectionFixtureModel struct {
	*frozenOCRModel
	modelConfigurationID string
	factsConfigurationID string
}

type deadlineFactsFixtureModel struct {
	*factsProjectionFixtureModel
	deadline chan time.Time
}

func (m *deadlineFactsFixtureModel) ContractFacts(ctx context.Context, _ string, _ bool) (model.ContractFactsSuggestion, error) {
	deadline, _ := ctx.Deadline()
	m.deadline <- deadline
	return model.ContractFactsSuggestion{}, model.ErrRequest
}

func (m *factsProjectionFixtureModel) Name() string            { return "facts-chat" }
func (m *factsProjectionFixtureModel) ConfigurationID() string { return m.modelConfigurationID }
func (m *factsProjectionFixtureModel) ContractFactsConfigurationID() string {
	return m.factsConfigurationID
}
func (m *factsProjectionFixtureModel) ContractFacts(context.Context, string, bool) (model.ContractFactsSuggestion, error) {
	return model.ContractFactsSuggestion{}, model.ErrRequest
}

func TestLongFactsReviewRequiresValidFrozenDurableCapability(t *testing.T) {
	t.Setenv("AGENT_PLANNER_MODE", "deterministic")
	modelID := strings.Repeat("a", 64)
	planner, err := agentflow.ResolvePlannerConfigurationWithContractFacts(modelID, "facts-chat", model.ContractFactsConfigurationID(modelID, "facts-chat"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(planner)
	if err != nil {
		t.Fatal(err)
	}
	st := state{
		Cases: []Case{{ID: "case"}}, Runs: []Run{{ID: "run", CaseID: "case"}},
		Attempts:  []Attempt{{ID: "attempt", RunID: "run", PlannerConfigurationID: planner.ConfigurationID, PlannerConfiguration: payload}},
		Snapshots: []RunSnapshot{applyReviewScope(RunSnapshot{RunID: "run"}, ReviewScopeSingleContract)},
	}
	claim := JobClaim{JobID: "job", RunID: "run", AttemptID: "attempt"}
	if !longFactsReview(st, claim, "run", "attempt") {
		t.Fatal("valid frozen facts attempt did not receive the bounded allowance")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*state, *JobClaim)
	}{
		{"embedded", func(_ *state, c *JobClaim) { c.JobID = "" }},
		{"different attempt", func(_ *state, c *JobClaim) { c.AttemptID = "other" }},
		{"full review", func(s *state, _ *JobClaim) { s.Snapshots[0].RuleSetHash = "other" }},
		{"governed case", func(s *state, _ *JobClaim) {
			s.Cases[0].BusinessLine = "ENT"
			s.Cases[0].Scenario = "telecom_dedicated_line"
		}},
		{"missing capability", func(s *state, _ *JobClaim) { s.Attempts[0].PlannerConfiguration = nil }},
		{"changed identity", func(s *state, _ *JobClaim) { s.Attempts[0].PlannerConfigurationID = "old" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, err := st.clone()
			if err != nil {
				t.Fatal(err)
			}
			current := claim
			tc.mutate(&candidate, &current)
			if longFactsReview(candidate, current, "run", "attempt") {
				t.Fatal("unqualified attempt received the facts allowance")
			}
		})
	}
}

func TestReviewWorkContextPreservesParentDeadlineCancellationAndScope(t *testing.T) {
	started := time.Now()
	parent, cancel := context.WithDeadline(context.Background(), started.Add(2*time.Minute))
	defer cancel()
	preparation := model.WithDelegatedScope(parent, model.DelegatedScope{Kind: "execution", RunID: "run", AttemptID: "attempt"})
	ctx, stop := reviewWorkContext(parent, preparation, started, reviewExecutionLimit+model.DefaultRequestTimeout)
	defer stop()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(started.Add(2*time.Minute)) {
		t.Fatalf("parent deadline was not retained: %v", deadline)
	}
	scope, ok := model.DelegatedScopeFromContext(ctx)
	if !ok || scope.RunID != "run" || scope.AttemptID != "attempt" {
		t.Fatal("resolved execution scope was not retained")
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatalf("parent cancellation did not stop execution: %v", ctx.Err())
	}

	ctx, stop = reviewWorkContext(context.Background(), context.Background(), started, reviewExecutionLimit)
	defer stop()
	deadline, ok = ctx.Deadline()
	if !ok || !deadline.Equal(started.Add(reviewExecutionLimit)) {
		t.Fatalf("ordinary review deadline changed: %v", deadline)
	}
	preparation, cancelPreparation := context.WithCancel(context.Background())
	cancelPreparation()
	ctx, stop = reviewWorkContext(context.Background(), preparation, started, reviewExecutionLimit+model.DefaultRequestTimeout)
	defer stop()
	if ctx.Err() != context.Canceled {
		t.Fatal("cancelled preparation was revived")
	}
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	ctx, stop = reviewWorkContext(context.Background(), expired, started.Add(-reviewExecutionLimit), reviewExecutionLimit+model.DefaultRequestTimeout)
	defer stop()
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatal("expired preparation was revived")
	}
}

func TestDurableContractFactsReceivesBoundedExtendedDeadline(t *testing.T) {
	fixture, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("isolated PostgreSQL and migrator required")
	}
	execution := fixture.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: fixture.core})
	execution.UseDocumentPreparer(fixture.document)
	modelID := strings.Repeat("a", 64)
	models := &deadlineFactsFixtureModel{factsProjectionFixtureModel: &factsProjectionFixtureModel{
		frozenOCRModel: &frozenOCRModel{}, modelConfigurationID: modelID,
		factsConfigurationID: model.ContractFactsConfigurationID(modelID, "facts-chat"),
	}, deadline: make(chan time.Time, 1)}
	execution.UseModels(models)
	ctx := WithAdmissionSessionToken(context.Background(), fixture.sessionToken)
	accepted, err := execution.StartReviewAsyncScope(ctx, fixture.admin, fixture.caseID, "", "", ReviewScopeSingleContract)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execution.ClaimNextJob(ctx, "facts-deadline-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim facts attempt: %+v err=%v", claim, err)
	}
	started := time.Now()
	if err := execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatal(err)
	}
	select {
	case deadline := <-models.deadline:
		if deadline.Before(started.Add(11*time.Minute)) || deadline.After(time.Now().Add(reviewExecutionLimit+model.DefaultRequestTimeout)) {
			t.Fatalf("facts deadline outside expected execution allowance: %v", deadline.Sub(started))
		}
	default:
		t.Fatal("facts model was not called")
	}
	var stepStatus, failureCode, attemptState string
	if err := fixture.environment.harness.pool.QueryRow(ctx, `SELECT c.status,c.failure_code,a.state FROM execution_agent_step_checkpoints c JOIN review_attempts a ON a.id=c.attempt_id WHERE c.attempt_id=$1 AND c.step_id='contract_facts/extract'`, claim.AttemptID).Scan(&stepStatus, &failureCode, &attemptState); err != nil {
		t.Fatal(err)
	}
	if stepStatus != "failed" || failureCode != "contract_facts_request_failed" || attemptState != "partial" {
		t.Fatalf("facts failure was not persisted under the live claim: status=%q code=%q attempt=%q", stepStatus, failureCode, attemptState)
	}
}

func TestContractFactsProjectionIsSeparatelyAuthorizedAndFrozen(t *testing.T) {
	st, admin := openExecutionStore(t)
	modelConfigurationID := strings.Repeat("a", 64)
	factsConfigurationID := model.ContractFactsConfigurationID(modelConfigurationID, "facts-chat")
	st.UseModels(&factsProjectionFixtureModel{
		frozenOCRModel: &frozenOCRModel{}, modelConfigurationID: modelConfigurationID,
		factsConfigurationID: factsConfigurationID,
	})
	caseID, err := st.CreateCase(admin, "单合同事实投影", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "contract.txt", "text/plain", []byte("甲方🌙公司；合同总价为100万元。")); err != nil {
		t.Fatal(err)
	}
	accepted, err := st.StartReviewAsyncScope(context.Background(), admin, caseID, "", "", ReviewScopeSingleContract)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(context.Background(), "facts-projection-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim accepted single-contract job: claim=%+v error=%v", claim, err)
	}
	docs, err := st.prepareAttempt(context.Background(), claim.RunID, claim.AttemptID, st.models)
	if err != nil {
		t.Fatal(err)
	}
	state := st.snapshot()
	input, ok := state.attemptInput(claim.AttemptID)
	if !ok {
		t.Fatal("facts attempt has no frozen input")
	}
	attempt, ok := state.attemptByID(claim.AttemptID)
	if !ok {
		t.Fatal("facts attempt identity is missing")
	}
	planner, err := FrozenPlannerConfiguration(attempt)
	if err != nil || planner.ContractFacts == nil {
		t.Fatalf("facts capability was not frozen at admission: %v", err)
	}
	source, primaryText, err := frozenContractFactsPrimary(input, docs)
	if err != nil {
		t.Fatal(err)
	}
	fields := []model.ContractFactsField{{
		Field: "parties", Status: "known", Value: "甲方",
		Citations: []model.ContractFactsCitation{{Quote: "甲方"}},
	}}
	for _, name := range []string{"contract_amount", "tax_treatment", "business_dates", "term", "payment_schedule", "subject_matter", "exceptions"} {
		fields = append(fields, model.ContractFactsField{Field: name, Status: "unknown", Value: "", Citations: []model.ContractFactsCitation{}})
	}
	projection, err := agentflow.BindContractFacts(
		claim.AttemptID, primaryText, primaryText, false, source, *planner.ContractFacts,
		model.ContractFactsSuggestion{Fields: fields},
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SyncAgentReviewRecord(context.Background(), claim, agentReviewRecord(claim, input.InputHash, claim.Generation, payload)); err != nil {
		t.Fatalf("persist facts-only projection: %v", err)
	}

	if got, present, err := st.AgentReviewForAttempt(context.Background(), admin, caseID, claim.RunID, claim.AttemptID); err != nil || present || len(got) != 0 {
		t.Fatalf("facts-only payload entered the five-domain projection: present=%v payload=%s error=%v", present, got, err)
	}
	got, present, err := st.ContractFactsForAttempt(context.Background(), admin, caseID, claim.RunID, claim.AttemptID)
	if err != nil || !present || string(got) != string(payload) {
		t.Fatalf("authorized facts projection was not byte-exact: present=%v error=%v", present, err)
	}
	forgedCitation := projection
	forgedCitation.Fields = append([]agentflow.ContractFact(nil), projection.Fields...)
	forgedCitation.Fields[0].Citations = append([]agentflow.ContractFactCitation(nil), projection.Fields[0].Citations...)
	forgedCitation.Fields[0].Citations[0].Start = 1
	forgedCitation.Fields[0].Citations[0].End = 3
	if err := forgedCitation.Validate(); err != nil {
		t.Fatalf("fixture forged citation should pass shape validation before frozen-source recheck: %v", err)
	}
	if err := st.validateContractFactsFrozenProjection(context.Background(), admin, caseID, claim.RunID, claim.AttemptID, forgedCitation); err == nil {
		t.Fatal("a structurally valid quote at the wrong frozen codepoint was accepted")
	}
	viewer, _ := accounts.ByID("demo-viewer")
	if _, _, err := st.ContractFactsForAttempt(context.Background(), viewer, caseID, claim.RunID, claim.AttemptID); err == nil {
		t.Fatal("a non-member read the facts-only projection")
	}
	workspace, err := st.Workspace(admin, caseID, claim.RunID, claim.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.ContractFacts) == 0 || workspace.Execution == nil || len(workspace.Execution.AgentReview) != 0 {
		t.Fatalf("workspace projection mixed facts with five-domain review: facts=%s execution=%+v", workspace.ContractFacts, workspace.Execution)
	}
}
