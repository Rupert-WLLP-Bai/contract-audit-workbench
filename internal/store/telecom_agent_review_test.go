// File metadata (manifest mirror):
// Purpose: Exercises frozen telecom planner configuration, retry takeover history/budget reconstruction, rulepack identity, and partial Agent review outcomes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-12
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/telecom"
)

func frozenPlannerTestConfiguration(t *testing.T) agentflow.PlannerConfiguration {
	t.Helper()
	t.Setenv("AGENT_PLANNER_MODE", "deterministic")
	t.Setenv("AGENT_PLANNER_MAX_ROUNDS", "2")
	t.Setenv("AGENT_PLANNER_MAX_CALLS", "6")
	t.Setenv("AGENT_PLANNER_TIMEOUT", "15s")
	t.Setenv("AGENT_PLANNER_MAX_EVIDENCE_BYTES", "2097152")
	configuration, err := agentflow.ResolvePlannerConfiguration("")
	if err != nil {
		t.Fatal(err)
	}
	return configuration
}

func TestFrozenTelecomPlannerConfigurationLegacyAbsence(t *testing.T) {
	configuration, required, err := frozenTelecomPlannerConfiguration(Attempt{})
	if err != nil || required || configuration.ConfigurationID != "" {
		t.Fatalf("legacy configuration = %+v required=%v err=%v", configuration, required, err)
	}
}

func TestFrozenTelecomPlannerConfigurationRejectsIncompleteIdentity(t *testing.T) {
	configuration := frozenPlannerTestConfiguration(t)
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	for name, attempt := range map[string]Attempt{
		"identity without payload": {PlannerConfigurationID: configuration.ConfigurationID},
		"payload without identity": {PlannerConfiguration: payload},
	} {
		t.Run(name, func(t *testing.T) {
			_, required, err := frozenTelecomPlannerConfiguration(attempt)
			if !required || !errors.Is(err, ErrFrozenPlannerMismatch) {
				t.Fatalf("incomplete identity required=%v err=%v", required, err)
			}
		})
	}
}

func TestFrozenTelecomPlannerConfigurationUsesAcceptedPayload(t *testing.T) {
	configuration := frozenPlannerTestConfiguration(t)
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	attempt := Attempt{PlannerConfigurationID: configuration.ConfigurationID, PlannerConfiguration: payload}
	t.Setenv("AGENT_PLANNER_MAX_CALLS", "1")
	got, required, err := frozenTelecomPlannerConfiguration(attempt)
	if err != nil || !required || got.Limits.MaxCalls != 6 || got.ConfigurationID != configuration.ConfigurationID {
		t.Fatalf("accepted configuration = %+v required=%v err=%v", got, required, err)
	}

	corruptConfiguration := configuration
	corruptConfiguration.Limits.MaxCalls++
	corrupt, err := json.Marshal(corruptConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	attempt.PlannerConfiguration = corrupt
	if _, _, err := frozenTelecomPlannerConfiguration(attempt); !errors.Is(err, ErrFrozenPlannerMismatch) {
		t.Fatalf("corrupt payload error = %v", err)
	}
}

func TestAgentReviewRulePackIdentityRejectsIncompleteCompilerMetadata(t *testing.T) {
	for name, attempt := range map[string]Attempt{
		"synthetic without a compiled pack":     {RulePackCompilerRef: telecom.CompilerRef},
		"historical v3 without a compiled pack": {RulePackCompilerRef: telecom.HistoricalCompilerRef},
		"historical v4 without a compiled pack": {RulePackCompilerRef: "telecom-contract-compiler/v4"},
		"legacy absence":                        {},
	} {
		t.Run(name, func(t *testing.T) {
			identity, err := agentReviewRulePackIdentity(attempt, Case{})
			if err != nil || identity != nil {
				t.Fatalf("identity=%+v err=%v", identity, err)
			}
		})
	}
	for name, attempt := range map[string]Attempt{
		"synthetic governed fields":     {RulePackCompilerRef: telecom.CompilerRef, RulePackID: "telecom-contractual"},
		"synthetic hash without bytes":  {RulePackCompilerRef: telecom.CompilerRef, RulePackHash: "content-hash"},
		"frozen bytes without compiler": {RulePackSnapshot: []byte(`{}`)},
		"unknown compiler":              {RulePackCompilerRef: "unknown-compiler/v1"},
		"retired compiler":              {RulePackCompilerRef: "telecom-contract-compiler/v2"},
		"incomplete governed identity":  {RulePackCompilerRef: governedCompilerMarker},
	} {
		t.Run(name, func(t *testing.T) {
			if identity, err := agentReviewRulePackIdentity(attempt, Case{}); err == nil || identity != nil {
				t.Fatalf("accepted identity=%+v err=%v", identity, err)
			}
		})
	}
}

func TestAgentReviewKnowledgeContextBudgetPreservesSameAttemptTakeoverInput(t *testing.T) {
	const maxEvidenceBytes = 2 << 20
	priorReadCalls := 1
	takeoverBudget, takeoverReserved := agentReviewKnowledgeContextBudget(maxEvidenceBytes, priorReadCalls, true)
	if takeoverBudget != maxEvidenceBytes || takeoverReserved != agentflow.KnowledgeReadMaxBytes {
		t.Fatalf("same-attempt takeover changed the first-generation knowledge selection budget: context=%d reserved=%d",
			takeoverBudget, takeoverReserved)
	}
	retryBudget, retryReserved := agentReviewKnowledgeContextBudget(maxEvidenceBytes, priorReadCalls, false)
	if retryBudget != maxEvidenceBytes-agentflow.KnowledgeReadMaxBytes || retryReserved != takeoverReserved {
		t.Fatalf("new attempt did not retain prior context reservation policy: context=%d reserved=%d",
			retryBudget, retryReserved)
	}
}

func TestRetryAttemptTakeoverAddsCurrentEventsAfterPriorAttemptBaseline(t *testing.T) {
	baseElapsed := int64(700)
	request := agentflow.Request{PriorEventCount: 11, PriorElapsedMS: &baseElapsed}
	firstDuration, secondDuration, thirdDuration := int64(5), int64(0), int64(9)
	events := []ExecutionEvent{
		{Seq: 12, DurationMS: &firstDuration},
		{Seq: 13, DurationMS: &secondDuration},
		{Seq: 14, DurationMS: &thirdDuration},
	}
	if err := addCurrentAttemptRecoveryHistory(&request, events); err != nil {
		t.Fatalf("merge retry attempt takeover history: %v", err)
	}
	if request.PriorEventCount != 14 || request.PriorElapsedMS == nil || *request.PriorElapsedMS != baseElapsed ||
		request.CurrentAttemptPriorElapsedMS == nil || *request.CurrentAttemptPriorElapsedMS != 14 {
		t.Fatalf("retry takeover overwrote its prior attempt baseline or lost current history: %+v", request)
	}

	request.PriorEventCount = 1023
	request.CurrentAttemptPriorElapsedMS = nil
	if err := addCurrentAttemptRecoveryHistory(&request, events); err == nil || request.PriorEventCount != 1023 || request.CurrentAttemptPriorElapsedMS != nil {
		t.Fatalf("overflowing event sequence was clamped or partially applied: request=%+v err=%v", request, err)
	}

	request.PriorEventCount = 0
	maxDuration, extraDuration := int64(1<<63-1), int64(1)
	if err := addCurrentAttemptRecoveryHistory(&request, []ExecutionEvent{
		{DurationMS: &maxDuration}, {DurationMS: &extraDuration},
	}); err == nil || request.PriorEventCount != 0 || request.CurrentAttemptPriorElapsedMS != nil {
		t.Fatalf("overflowing current-attempt elapsed was accepted or partially applied: request=%+v err=%v", request, err)
	}
}

func TestAgentReviewPlannerFailurePreservesRuleFindingsAsPartial(t *testing.T) {
	t.Setenv("AGENT_PLANNER_MODE", "fixture")
	st, claim, caseID, _ := agentReviewFixture(t)
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	if err := st.RunClaimedJob(context.Background(), claim); err != nil {
		t.Fatalf("run claimed job: %v", err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, claim.RunID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil ||
		workspace.Execution.Attempt.State != "partial" || workspace.View.State != "partial" || len(workspace.Findings) == 0 {
		t.Fatalf("planner failure did not preserve canonical findings as partial: state=%+v findings=%d", workspace.View, len(workspace.Findings))
	}
	if workspace.Execution.Attempt.ErrorCode == nil || *workspace.Execution.Attempt.ErrorCode != "agent_review_failed" {
		t.Fatalf("planner failure category = %v, want agent_review_failed", workspace.Execution.Attempt.ErrorCode)
	}
	var failedOutcome bool
	for _, event := range workspace.Execution.Events {
		if event.Phase == "agent_review_result" && event.EventType == "agent_review_event" &&
			event.Status == "partial" && event.ErrorCode != nil && *event.ErrorCode == "agent_review_failed" {
			failedOutcome = true
			break
		}
	}
	if !failedOutcome {
		t.Fatal("planner failure category was not retained in execution events")
	}
}

func TestTelecomEmbeddedParseFailureCannotBecomeCompleted(t *testing.T) {
	st, admin := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID, err := st.CreateCaseWithBusiness(admin, "专线解析不完整", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	for role, body := range map[string]string{
		"procurement":    "采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s，合同总额 100 万元。",
		"draft_contract": "拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s，合同总额 100 万元。",
	} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	failedID, err := st.AddDocument(context.Background(), admin, caseID, "attachment", "unsupported.pdf", "application/pdf", []byte("unsupported PDF input"))
	if err != nil {
		t.Fatal(err)
	}
	runID, err := st.StartReviewAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.AgentReview != nil {
		t.Fatalf("test did not exercise the embedded path without an AgentReview: %+v", workspace.Execution)
	}
	attempt := workspace.Execution.Attempt
	if attempt.State != "partial" || attempt.ErrorCode == nil || *attempt.ErrorCode != "document_parse_incomplete" || workspace.View.State != "partial" {
		t.Fatalf("failed parse became a complete telecom review: %+v", attempt)
	}
	if len(workspace.Evaluations) != 3 || len(workspace.Documents) != 3 {
		t.Fatalf("parse failure discarded canonical results or frozen documents: %+v", workspace)
	}
	failed := false
	for _, document := range workspace.Documents {
		if document.Document.ID == failedID && !document.Ready && document.ParsedDocument != nil && document.ParsedDocument.Status == "failed" {
			failed = true
		}
	}
	if !failed {
		t.Fatal("failed parse artifact was not preserved in the frozen workspace")
	}
}
