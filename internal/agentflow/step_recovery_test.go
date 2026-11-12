// File metadata (manifest mirror):
// Purpose: Pins successful Agent step replay across repeated takeovers and checked elapsed-budget restoration from retry-attempt history.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package agentflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/execution/steps"
)

type observedPlannerFunc func(context.Context, PlanInput) (Plan, PlannerObservation, error)

func (observedPlannerFunc) Name() string { return "observed-step-planner" }

func (planner observedPlannerFunc) Plan(ctx context.Context, input PlanInput) (Plan, error) {
	plan, _, err := planner.PlanObserved(ctx, input)
	return plan, err
}

func (planner observedPlannerFunc) PlanObserved(ctx context.Context, input PlanInput) (Plan, PlannerObservation, error) {
	return planner(ctx, input)
}

type memoryStepJournal struct {
	generation int64
	records    map[string]steps.Record
	finishErr  error
}

func newMemoryStepJournal() *memoryStepJournal {
	return &memoryStepJournal{generation: 1, records: map[string]steps.Record{}}
}

func (journal *memoryStepJournal) RecoverySummary(context.Context) (steps.RecoverySummary, error) {
	var summary steps.RecoverySummary
	for _, record := range journal.records {
		if record.Identity.Kind == "knowledge_read" {
			summary.KnowledgeReadCount++
		}
		if record.Status == steps.StatusSucceeded || record.Status == steps.StatusFailed {
			summary.ElapsedMS += record.ElapsedMS
		}
	}
	return summary, nil
}

func (journal *memoryStepJournal) Begin(_ context.Context, identity steps.Identity) (steps.Record, bool, error) {
	if err := identity.Validate(); err != nil {
		return steps.Record{}, false, err
	}
	if record, exists := journal.records[identity.StepID]; exists {
		if record.Identity != identity {
			return steps.Record{}, false, steps.ErrIdentityMismatch
		}
		if record.Status == steps.StatusStarted {
			record.Scope.Generation = journal.generation
			record.Status = steps.StatusAmbiguous
			record.FailureCode = "prior_generation_result_unresolved"
			journal.records[identity.StepID] = record
		}
		return record, false, nil
	}
	record := steps.Record{Scope: steps.Scope{TenantID: "tenant-a", OrgID: "org-a", CaseID: "case-a", RunID: "run-a",
		AttemptID: "attempt-a", Generation: journal.generation, AttemptInputSHA256: strings.Repeat("a", 64)},
		Identity: identity, Status: steps.StatusStarted, StartedAt: time.Now().UTC(), CallsConsumed: 1,
		Usage: steps.Usage{Status: steps.UsageUnknown}}
	journal.records[identity.StepID] = record
	return record, true, nil
}

func (journal *memoryStepJournal) Finish(_ context.Context, identity steps.Identity, outcome steps.Outcome) error {
	if journal.finishErr != nil {
		return journal.finishErr
	}
	record, exists := journal.records[identity.StepID]
	if !exists || record.Identity != identity || record.Scope.Generation != journal.generation || record.Status != steps.StatusStarted {
		return steps.ErrIdentityMismatch
	}
	if err := outcome.Validate(); err != nil {
		return err
	}
	record.Status = outcome.Status
	record.Payload = append([]byte(nil), outcome.Payload...)
	if len(outcome.Payload) > 0 {
		record.OutputSHA256 = steps.OutputDigest(outcome.Payload)
	}
	record.FailureCode = outcome.FailureCode
	record.ObservedAt = &outcome.ObservedAt
	record.ElapsedMS = outcome.ElapsedMS
	record.ElapsedKnown = true
	record.CallsConsumed = outcome.CallsConsumed
	record.Usage = outcome.Usage
	journal.records[identity.StepID] = record
	return nil
}

func TestStepJournalRecoversKnowledgeAndToolAcrossTwoTakeovers(t *testing.T) {
	request := requestWithPolicyTrace(false)
	policy := request.Knowledge[0]
	span := KnowledgeReadRequest{KnowledgeID: policy.KnowledgeID, VersionID: policy.VersionID,
		ContentSHA256: policy.ContentSHA256, Start: 0, End: 2}
	journal := newMemoryStepJournal()
	plannerCalls, toolCalls, knowledgeRevalidations := 0, 0, 0
	tool := toolFunc{spec: ToolSpec{ID: "step.inspect", Domain: Technical, Version: "v1", Effect: "read",
		Capabilities: []string{"warranty_months"}}, run: func(context.Context, ToolInput) ([]Finding, error) {
		toolCalls++
		return []Finding{{Domain: Technical, Capability: "warranty_months", RuleID: "step.inspect", Relation: "insufficient",
			Summary: "frozen source did not establish the value", ReasonCode: "required_source_or_field_missing",
			Citations: []Citation{}, Facts: []SemanticFact{}}}, nil
	}}
	planner := observedPlannerFunc(func(_ context.Context, input PlanInput) (Plan, PlannerObservation, error) {
		plannerCalls++
		if input.KnowledgeReadBudget == nil || input.KnowledgeReadBudget.Remaining != 2-plannerCalls+1 {
			return Plan{}, PlannerObservation{}, fmt.Errorf("recovered knowledge budget differs: %+v", input.KnowledgeReadBudget)
		}
		usage := PlannerObservation{InputTokens: int64Pointer(7), OutputTokens: int64Pointer(3), TotalTokens: int64Pointer(10)}
		if plannerCalls == 1 {
			return Plan{Calls: []Call{}, KnowledgeReads: []KnowledgeReadRequest{span}}, usage, nil
		}
		if len(input.KnowledgeObservations) != 1 || input.KnowledgeObservations[0].Text != "依据" || input.RemainingCalls != 1 {
			return Plan{}, PlannerObservation{}, fmt.Errorf("second planner input did not replay the complete prior observation and budget: %+v", input)
		}
		return Plan{Calls: []Call{{ToolID: "step.inspect", Domain: Technical, GapID: request.Gaps[0].ID, Reason: "inspect frozen source"}}}, usage, nil
	})
	var events []Event
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{tool}, MaxCalls: 2, MaxRounds: 2,
		Timeout: time.Second, ToolTimeout: 100 * time.Millisecond, StepJournal: journal,
		EventSink: func(_ context.Context, event Event) error { events = append(events, event); return nil },
		ReadKnowledge: func(_ context.Context, current KnowledgeReadRequest) (string, error) {
			knowledgeRevalidations++
			if current != span {
				return "", errors.New("checkpoint reauthorization changed the frozen span")
			}
			return "依据", nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	for takeover := int64(0); takeover < 3; takeover++ {
		if takeover > 0 {
			journal.generation++
			request.PriorKnowledgeReadCount = 1
			request.PriorEventCount = len(events)
			request.KnowledgeSelection.Budget.PriorReadCalls = 1
			request.KnowledgeSelection.Budget.PriorReadReservedBytes = KnowledgeReadMaxBytes
		}
		report, runErr := runtime.Run(context.Background(), request)
		if runErr != nil || report.ToolCalls != 2 || len(report.Rounds) != 2 || report.Projection == nil ||
			report.Projection.Usage.PlannerCalls != 2 || report.Projection.Usage.InputTokens == nil || *report.Projection.Usage.InputTokens != 14 ||
			report.Projection.Usage.OutputTokens == nil || *report.Projection.Usage.OutputTokens != 6 ||
			report.Projection.Usage.TotalTokens == nil || *report.Projection.Usage.TotalTokens != 20 {
			t.Fatalf("run %d did not rebuild the same successful state: report=%+v err=%v", takeover, report, runErr)
		}
	}
	if plannerCalls != 2 || toolCalls != 1 || knowledgeRevalidations != 3 {
		t.Fatalf("same-attempt recovery repeated physical work: planner=%d tools=%d authorized span reads=%d", plannerCalls, toolCalls, knowledgeRevalidations)
	}
	startedReads, reusedReads, reusedTools := 0, 0, 0
	var firstReadObservedAt, firstToolObservedAt time.Time
	plannerObservedAt := map[int]time.Time{}
	for _, record := range journal.records {
		if record.Identity.Kind == "planner" && record.ObservedAt != nil {
			plannerObservedAt[record.Identity.Round] = *record.ObservedAt
		}
	}
	for _, event := range events {
		switch event.Kind {
		case "knowledge_read_started":
			startedReads++
		case "knowledge_read_completed":
			firstReadObservedAt = event.OccurredAt
		case "tool_observed":
			firstToolObservedAt = event.OccurredAt
		case "planner_reused":
			if expected := plannerObservedAt[event.Round]; !event.OccurredAt.Equal(expected) {
				t.Fatalf("reused planner observation changed its original time: got %s want %s", event.OccurredAt, expected)
			}
		case "knowledge_read_reused":
			reusedReads++
			if !event.OccurredAt.Equal(firstReadObservedAt) {
				t.Fatalf("reused knowledge observation changed its original time: got %s want %s", event.OccurredAt, firstReadObservedAt)
			}
		case "tool_reused":
			reusedTools++
			if !event.OccurredAt.Equal(firstToolObservedAt) {
				t.Fatalf("reused tool observation changed its original time: got %s want %s", event.OccurredAt, firstToolObservedAt)
			}
		}
	}
	if startedReads != 1 || reusedReads != 2 || reusedTools != 2 {
		t.Fatalf("recovery did not preserve budget and emit reuse facts: starts=%d reads=%d tools=%d", startedReads, reusedReads, reusedTools)
	}
	for index, event := range events {
		if event.Sequence != index+1 {
			t.Fatalf("same-attempt recovery lost the persisted event sequence budget at index %d: sequence=%d", index, event.Sequence)
		}
	}
}

func int64Pointer(value int64) *int64 { return &value }

func TestRecoveredElapsedBudgetAddsPriorAttemptAndCurrentAttemptMaximum(t *testing.T) {
	priorAttempt := int64(900)
	currentEventElapsed := int64(35)
	got, err := recoveredElapsedBudget(&priorAttempt, &currentEventElapsed, 52)
	if err != nil || got == nil || *got != 952 {
		t.Fatalf("retry takeover elapsed budget = %v err=%v, want prior baseline plus max current history", got, err)
	}
	got, err = recoveredElapsedBudget(&priorAttempt, &currentEventElapsed, 20)
	if err != nil || got == nil || *got != 935 {
		t.Fatalf("current event history was not retained when it exceeded checkpoints: %v err=%v", got, err)
	}
	negative := int64(-1)
	if _, err := recoveredElapsedBudget(&priorAttempt, &negative, 0); !errors.Is(err, steps.ErrCorrupt) {
		t.Fatalf("negative current-attempt elapsed was accepted: %v", err)
	}
	maxInt64 := int64(1<<63 - 1)
	overflow := int64(1)
	if _, err := recoveredElapsedBudget(&maxInt64, &overflow, 0); !errors.Is(err, steps.ErrCorrupt) {
		t.Fatalf("overflowing accumulated elapsed budget was accepted: %v", err)
	}
}

func TestStepJournalIdentityMismatchDoesNotRepeatPlanner(t *testing.T) {
	request := requestFixture()
	journal := newMemoryStepJournal()
	plannerCalls := 0
	planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) {
		plannerCalls++
		return Plan{Calls: []Call{}}, nil
	})
	tool := toolFunc{spec: ToolSpec{ID: "versioned.read", Domain: Technical, Version: "v1", Effect: "read",
		Capabilities: []string{"warranty_months"}}, run: func(context.Context, ToolInput) ([]Finding, error) { return nil, nil }}
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{tool}, MaxCalls: 1, MaxRounds: 1,
		Timeout: time.Second, ToolTimeout: 100 * time.Millisecond, StepJournal: journal})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = runtime.Run(context.Background(), request)
	journal.generation++
	changedTool := tool
	changedTool.spec.Version = "v2"
	changedRuntime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{changedTool}, MaxCalls: 1, MaxRounds: 1,
		Timeout: time.Second, ToolTimeout: 100 * time.Millisecond, StepJournal: journal})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changedRuntime.Run(context.Background(), request); !errors.Is(err, steps.ErrIdentityMismatch) || plannerCalls != 1 {
		t.Fatalf("changed tool catalog repeated a stored planner step: calls=%d err=%v", plannerCalls, err)
	}
}

func TestStepJournalHistoryMismatchStopsWithExplicitPartialOutcome(t *testing.T) {
	request := requestWithPolicyTrace(false)
	request.PriorKnowledgeReadCount = 1
	request.KnowledgeSelection.Budget.PriorReadCalls = 1
	request.KnowledgeSelection.Budget.PriorReadReservedBytes = KnowledgeReadMaxBytes
	plannerCalls := 0
	planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) {
		plannerCalls++
		return Plan{Calls: []Call{}}, nil
	})
	tool := toolFunc{spec: ToolSpec{ID: "mismatch.read", Domain: Technical, Version: "v1", Effect: "read",
		Capabilities: []string{"warranty_months"}}, run: func(context.Context, ToolInput) ([]Finding, error) { return nil, nil }}
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{tool}, MaxCalls: 1, MaxRounds: 1,
		Timeout: time.Second, ToolTimeout: 100 * time.Millisecond, StepJournal: newMemoryStepJournal()})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Run(context.Background(), request)
	if !errors.Is(err, steps.ErrAmbiguous) || plannerCalls != 0 || report.Status != "partial" ||
		report.Projection == nil || report.Projection.Status != "partial" || report.ToolCalls != 1 {
		t.Fatalf("checkpoint/event mismatch did not stop with a persistable partial projection: report=%+v planner calls=%d err=%v", report, plannerCalls, err)
	}
}

func TestPlannerCheckpointUsageKeepsUnavailableModelCountersUnknown(t *testing.T) {
	if got := plannerStepUsage(PlannerObservation{}, false, true); got.Status != steps.UsageUnknown ||
		got.InputTokens != nil || got.OutputTokens != nil || got.TotalTokens != nil {
		t.Fatalf("missing model usage counters were represented as known: %+v", got)
	}
	if got := plannerStepUsage(PlannerObservation{}, false, false); got.Status != steps.UsageNotApplicable {
		t.Fatalf("deterministic planner usage should be not applicable: %+v", got)
	}
}

func TestStepPersistenceFailureStopsAndOldStartedStepBecomesAmbiguous(t *testing.T) {
	request := requestFixture()
	journal := newMemoryStepJournal()
	journal.finishErr = errors.New("checkpoint transaction did not commit")
	plannerCalls, toolCalls := 0, 0
	tool := toolFunc{spec: ToolSpec{ID: "must.not.run", Domain: Technical, Version: "v1", Effect: "read",
		Capabilities: []string{"warranty_months"}}, run: func(context.Context, ToolInput) ([]Finding, error) {
		toolCalls++
		return nil, nil
	}}
	planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) {
		plannerCalls++
		return Plan{Calls: []Call{{ToolID: "must.not.run", Domain: Technical, GapID: request.Gaps[0].ID, Reason: "bounded"}}}, nil
	})
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{tool}, MaxCalls: 1, MaxRounds: 1,
		Timeout: time.Second, ToolTimeout: 100 * time.Millisecond, StepJournal: journal})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Run(context.Background(), request); !errors.Is(err, steps.ErrPersistence) || plannerCalls != 1 || toolCalls != 0 {
		t.Fatalf("failed planner persistence continued to tools: planners=%d tools=%d err=%v", plannerCalls, toolCalls, err)
	}
	journal.generation++
	report, err := runtime.Run(context.Background(), request)
	if !errors.Is(err, steps.ErrAmbiguous) || report.Status != "partial" || plannerCalls != 1 || toolCalls != 0 {
		t.Fatalf("unresolved prior planner call was repeated or hidden: report=%+v planners=%d tools=%d err=%v", report, plannerCalls, toolCalls, err)
	}
}
