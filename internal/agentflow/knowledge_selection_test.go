package agentflow

import (
	"context"
	"errors"
	"testing"
	"time"
	"unicode/utf8"
)

func requestWithPolicyTrace(truncated bool) Request {
	request := requestFixture()
	policy := KnowledgeReference{KnowledgeID: "policy", VersionID: "version", ContentSHA256: TextHash("policy content"), Kind: "policy", Required: true, Summary: "政策摘要", Truncated: truncated}
	request.Knowledge = []KnowledgeReference{policy}
	trace := &KnowledgeSelectionRecord{SnapshotSHA256: TextHash("snapshot"), Algorithm: "chinese-bigram-label/v1", PolicyStatus: "available",
		Input: KnowledgeSelectionInput{Gaps: []KnowledgeInputGap{}, Evidence: []KnowledgeInputEvidence{}},
		Candidates: []KnowledgeCandidate{{KnowledgeID: policy.KnowledgeID, VersionID: policy.VersionID, ContentSHA256: policy.ContentSHA256,
			Kind: policy.Kind, Required: true, Decision: "selected", Reason: "required_policy", Truncated: truncated}},
		Budget: KnowledgeSelectionBudget{ReferenceLimit: PlannerKnowledgeReferenceBudget, FieldBytes: PlannerKnowledgeFieldBytes,
			EvidenceRunesPerDocument: 4096, RequiredCount: 1, ContentBytesLimit: 1000, ContentBytesUsed: len(policy.Summary),
			ReadCallsLimit: KnowledgeReadMaxCalls, ReadMaxRunes: KnowledgeReadMaxRunes, ReadMaxBytes: KnowledgeReadMaxBytes},
		PlannerRounds: []KnowledgePlanningRound{}, Reads: []KnowledgeReadRecord{}}
	if truncated {
		trace.PolicyStatus, trace.PolicyReason = "unknown", "policy_context_truncated"
	}
	for _, gap := range request.Gaps {
		trace.Input.Gaps = append(trace.Input.Gaps, KnowledgeInputGap{GapID: gap.ID, Domain: gap.Domain, Capability: gap.Capability, Question: gap.Question})
	}
	for _, item := range request.Evidence {
		count := utf8.RuneCountInString(item.Text)
		trace.Input.Evidence = append(trace.Input.Evidence, KnowledgeInputEvidence{EvidenceID: item.ID, Role: item.Role, SHA256: item.SHA256, TotalRunes: count, ScannedRunes: min(count, 4096)})
	}
	request.KnowledgeSelection = trace
	return request
}

func TestTruncatedPolicyRemainsUnknownEvenWhenContractCheckResolves(t *testing.T) {
	request := requestWithPolicyTrace(true)
	var input PlanInput
	planner := plannerFunc(func(ctx context.Context, value PlanInput) (Plan, error) {
		input = value
		return (ScopePlanner{}).Plan(ctx, value)
	})
	report, err := runtimeFixture(t, planner, BaselineTools(), 1).Run(context.Background(), request)
	if err != nil || report.Status != "partial" || report.Projection == nil || report.Projection.Gaps[0].Status != "resolved" {
		t.Fatalf("contract check obscured the incomplete policy: %+v %v", report, err)
	}
	trace := report.Projection.KnowledgeSelection
	if trace == nil || trace.PolicyStatus != "unknown" || trace.PolicyReason != "policy_context_truncated" ||
		len(input.Knowledge) != 1 || !input.Knowledge[0].Truncated || input.KnowledgePolicyStatus != "unknown" ||
		input.KnowledgePolicyReason != "policy_context_truncated" || len(trace.PlannerRounds) != 1 ||
		len(trace.PlannerRounds[0].ReferencesProvided) != 1 || trace.PlannerRounds[0].Outcome != "returned" {
		t.Fatalf("truncation or observed delivery was lost: input=%+v trace=%+v", input, trace)
	}
	if len(request.KnowledgeSelection.PlannerRounds) != 0 {
		t.Fatal("execution mutated its caller's trace")
	}
}

func TestPlannerAuthorizationRunsBeforeEveryRound(t *testing.T) {
	request := requestWithPolicyTrace(false)
	calls, authorizations, toolCalls := 0, 0, 0
	planner := plannerFunc(func(_ context.Context, input PlanInput) (Plan, error) {
		calls++
		return Plan{Calls: []Call{{ToolID: "inspection", Domain: Technical, GapID: input.Gaps[0].ID, Reason: "inspect"}}}, nil
	})
	tool := toolFunc{spec: ToolSpec{ID: "inspection", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"warranty_months"}},
		run: func(context.Context, ToolInput) ([]Finding, error) {
			toolCalls++
			return []Finding{{Domain: Technical, Capability: "warranty_months", RuleID: "inspection", Relation: "insufficient", Summary: "more evidence needed", ReasonCode: "more_evidence_needed"}}, nil
		}}
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{tool}, MaxCalls: 2, MaxRounds: 2, Timeout: time.Second,
		ToolTimeout: 100 * time.Millisecond, AuthorizePlanning: func(context.Context) error {
			authorizations++
			if authorizations == 2 {
				return errors.New("withdrawn")
			}
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Run(context.Background(), request)
	if !errors.Is(err, ErrPlanningAuthorization) || authorizations != 2 || calls != 1 || toolCalls != 1 || report.Projection == nil ||
		len(report.Projection.KnowledgeSelection.PlannerRounds) != 1 {
		t.Fatalf("withdrawal did not stop the next planner round: auth=%d plans=%d tools=%d report=%+v err=%v", authorizations, calls, toolCalls, report, err)
	}
}

func TestSelectionTraceCannotHidePolicyTruncation(t *testing.T) {
	request := requestWithPolicyTrace(true)
	request.KnowledgeSelection.PolicyStatus = "available"
	request.KnowledgeSelection.PolicyReason = ""
	called := false
	planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) { called = true; return Plan{}, nil })
	report, err := runtimeFixture(t, planner, BaselineTools(), 1).Run(context.Background(), request)
	if !errors.Is(err, ErrInput) || called || report.ToolCalls != 0 {
		t.Fatalf("forged complete policy trace was accepted: %+v %v", report, err)
	}
}

func TestMissingPolicyAndOmissionsAreVisibleToPlanner(t *testing.T) {
	request := requestWithPolicyTrace(false)
	request.Knowledge = nil
	trace := request.KnowledgeSelection
	trace.PolicyStatus, trace.PolicyReason = "unknown", "policy_missing"
	trace.Candidates = []KnowledgeCandidate{{KnowledgeID: "omitted", VersionID: "v1", ContentSHA256: TextHash("omitted"), Kind: "guidance", Decision: "omitted", Reason: "no_relevance"}}
	trace.Budget.RequiredCount, trace.Budget.OptionalCandidates, trace.Budget.OptionalOmitted, trace.Budget.ContentBytesUsed = 0, 1, 1, 0
	var input PlanInput
	planner := plannerFunc(func(ctx context.Context, value PlanInput) (Plan, error) {
		input = value
		return (ScopePlanner{}).Plan(ctx, value)
	})
	report, err := runtimeFixture(t, planner, BaselineTools(), 1).Run(context.Background(), request)
	if err != nil || report.Status != "partial" || input.KnowledgePolicyStatus != "unknown" ||
		input.KnowledgePolicyReason != "policy_missing" || input.Truncation.KnowledgeOmitted != 1 || len(input.Knowledge) != 0 {
		t.Fatalf("missing policy or omitted guidance disappeared: input=%+v report=%+v err=%v", input, report, err)
	}
}
