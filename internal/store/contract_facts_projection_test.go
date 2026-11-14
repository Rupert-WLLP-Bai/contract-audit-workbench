// File metadata (manifest mirror):
// Purpose: Exercises current read authority, facts-only discriminator handling and immutable source/configuration validation for persisted facts projections.
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

func (m *factsProjectionFixtureModel) Name() string            { return "facts-chat" }
func (m *factsProjectionFixtureModel) ConfigurationID() string { return m.modelConfigurationID }
func (m *factsProjectionFixtureModel) ContractFactsConfigurationID() string {
	return m.factsConfigurationID
}
func (m *factsProjectionFixtureModel) ContractFacts(context.Context, string, bool) (model.ContractFactsSuggestion, error) {
	return model.ContractFactsSuggestion{}, model.ErrRequest
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
