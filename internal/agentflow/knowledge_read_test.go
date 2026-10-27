package agentflow

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProgressiveKnowledgeReadReachesNextPlanningRound(t *testing.T) {
	request := requestWithPolicyTrace(false)
	policy := request.Knowledge[0]
	span := KnowledgeReadRequest{KnowledgeID: policy.KnowledgeID, VersionID: policy.VersionID, ContentSHA256: policy.ContentSHA256, Start: 0, End: 2}
	var inputs []PlanInput
	planner := plannerFunc(func(ctx context.Context, input PlanInput) (Plan, error) {
		inputs = append(inputs, input)
		if len(inputs) == 1 {
			return Plan{Calls: []Call{}, KnowledgeReads: []KnowledgeReadRequest{span}}, nil
		}
		return (ScopePlanner{}).Plan(ctx, input)
	})
	reads := 0
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: BaselineTools(), MaxCalls: 2, MaxRounds: 2,
		Timeout: time.Second, ToolTimeout: 100 * time.Millisecond, ReadKnowledge: func(_ context.Context, received KnowledgeReadRequest) (string, error) {
			reads++
			if received != span {
				return "", errors.New("wrong span")
			}
			return "依据", nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Run(context.Background(), request)
	if err != nil || reads != 1 || len(inputs) != 2 || report.ToolCalls != 2 || report.Projection == nil {
		t.Fatalf("progressive read did not execute: reads=%d inputs=%d report=%+v err=%v", reads, len(inputs), report, err)
	}
	if inputs[0].KnowledgeReadBudget == nil || inputs[0].KnowledgeReadBudget.Remaining != 2 || len(inputs[0].KnowledgeObservations) != 0 ||
		len(inputs[1].KnowledgeObservations) != 1 || inputs[1].KnowledgeObservations[0].Text != "依据" ||
		inputs[1].KnowledgeObservations[0].TextSHA256 != TextHash("依据") || inputs[1].KnowledgeReadBudget.Remaining != 1 {
		t.Fatalf("span was not offered as bounded source context: %+v", inputs)
	}
	trace := report.Projection.KnowledgeSelection
	if len(trace.Reads) != 1 || trace.Reads[0].Status != "completed" || trace.Reads[0].TextSHA256 != TextHash("依据") ||
		len(trace.PlannerRounds) != 2 || len(trace.PlannerRounds[0].ReadObservationsProvided) != 0 ||
		len(trace.PlannerRounds[1].ReadObservationsProvided) != 1 || trace.PlannerRounds[1].ReadObservationsProvided[0].TextSHA256 != TextHash("依据") {
		t.Fatalf("read or subsequent delivery trace is inaccurate: %+v", trace)
	}
	for _, fact := range report.Projection.Evidence {
		if fact.Value == "依据" {
			t.Fatal("policy text was promoted to a contract fact")
		}
	}
}

func TestKnowledgeReadRejectsUnselectedOversizedAndExhaustedRequests(t *testing.T) {
	for _, name := range []string{"foreign", "oversized", "exhausted", "disabled"} {
		t.Run(name, func(t *testing.T) {
			request := requestWithPolicyTrace(false)
			policy := request.Knowledge[0]
			span := KnowledgeReadRequest{KnowledgeID: policy.KnowledgeID, VersionID: policy.VersionID, ContentSHA256: policy.ContentSHA256, Start: 0, End: 1}
			if name == "foreign" {
				span.ContentSHA256 = TextHash("another version")
			}
			if name == "oversized" {
				span.End = KnowledgeReadMaxRunes + 1
			}
			if name == "exhausted" {
				request.PriorKnowledgeReadCount = KnowledgeReadMaxCalls
				request.KnowledgeSelection.Budget.PriorReadCalls = KnowledgeReadMaxCalls
				request.KnowledgeSelection.Budget.PriorReadReservedBytes = KnowledgeReadMaxCalls * KnowledgeReadMaxBytes
			}
			planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) {
				return Plan{Calls: []Call{{ToolID: "baseline.technical", Domain: Technical, GapID: "g-tech", Reason: "check"}}, KnowledgeReads: []KnowledgeReadRequest{span}}, nil
			})
			calls := 0
			config := Config{Planner: planner, Tools: BaselineTools(), MaxCalls: 8, MaxRounds: 2, Timeout: time.Second, ToolTimeout: 100 * time.Millisecond}
			if name != "disabled" {
				config.ReadKnowledge = func(context.Context, KnowledgeReadRequest) (string, error) { calls++; return "x", nil }
			}
			runtime, err := New(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			report, err := runtime.Run(context.Background(), request)
			if !errors.Is(err, ErrPlan) || calls != 0 || report.ToolCalls != request.PriorKnowledgeReadCount {
				t.Fatalf("unsafe read or paired tool executed: calls=%d report=%+v err=%v", calls, report, err)
			}
		})
	}
}

func TestKnowledgeReadFailureStopsBeforeToolsOrNextRound(t *testing.T) {
	request := requestWithPolicyTrace(false)
	policy := request.Knowledge[0]
	span := KnowledgeReadRequest{KnowledgeID: policy.KnowledgeID, VersionID: policy.VersionID, ContentSHA256: policy.ContentSHA256, Start: 0, End: 1}
	plans := 0
	planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) {
		plans++
		return Plan{Calls: []Call{}, KnowledgeReads: []KnowledgeReadRequest{span}}, nil
	})
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: BaselineTools(), MaxCalls: 2, MaxRounds: 2, Timeout: time.Second,
		ToolTimeout: 100 * time.Millisecond, ReadKnowledge: func(context.Context, KnowledgeReadRequest) (string, error) { return "", errors.New("withdrawn") }})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Run(context.Background(), request)
	if !errors.Is(err, ErrKnowledgeRead) || plans != 1 || report.ToolCalls != 1 || len(report.Projection.KnowledgeSelection.Reads) != 1 ||
		report.Projection.KnowledgeSelection.Reads[0].Status != "failed" || report.Projection.KnowledgeSelection.Reads[0].TextSHA256 != "" {
		t.Fatalf("failed read was promoted or execution continued: %+v / %v", report, err)
	}
}
