package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type contextPlanningModel struct {
	inputs []PlanInput
}

func (m *contextPlanningModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	var input PlanInput
	if len(messages) != 2 || json.Unmarshal([]byte(messages[1].Content), &input) != nil {
		return nil, errors.New("invalid planning input")
	}
	m.inputs = append(m.inputs, input)
	tool := "candidate.scope"
	if len(input.Observations) > 0 && len(input.VerifiedFacts) >= 2 &&
		input.Observations[0].ReasonCode == "candidate_requires_comparison" {
		tool = "source.technical"
	}
	body, err := json.Marshal(Plan{Calls: []Call{{ToolID: tool, Domain: Technical, GapID: "g-tech", Reason: "inspect source"}}})
	if err != nil {
		return nil, err
	}
	return schema.AssistantMessage(string(body), nil), nil
}

func scopeSourceRequest() (Request, Citation) {
	request := requestFixture()
	request.Gaps[0].Capability = "scope"
	request.Gaps[0].Mandatory = true
	request.Gaps[0].IncompleteReason = "awaiting_evidence"
	for i, value := range []string{"专线A", "专线B"} {
		item := NewEvidence(request.Evidence[i].ID, "v1", request.Evidence[i].Role, "线路范围："+value+"。", request.Case)
		item.DocumentID = "doc-" + item.ID
		item.ParsedDocumentID = "parsed-" + item.ID
		item.NormalizedDigest = TextHash(item.Text)
		request.Evidence[i] = item
	}
	item := request.Evidence[0]
	quote := "线路范围：专线A"
	return request, Citation{EvidenceID: item.ID, Version: item.Version, Quote: quote, Start: 0, End: len([]rune(quote)),
		DocumentID: item.DocumentID, ParsedDocumentID: item.ParsedDocumentID, NormalizedDigest: item.NormalizedDigest, Status: "located"}
}

func TestModelPlannerReceivesBoundedFrozenContextAndRealPriorObservation(t *testing.T) {
	request, citation := scopeSourceRequest()
	page := 99
	citation.Page, citation.ElementID = &page, "unverified-element"
	request.VerifiedFacts = []VerifiedFact{{GapID: "g-tech", Domain: Technical,
		Fact: SemanticFact{ID: "fact-initial", Type: "scope", Value: "专线A", Citation: citation}}}
	for i := range 35 {
		request.Knowledge = append(request.Knowledge, KnowledgeReference{KnowledgeID: fmt.Sprintf("knowledge-%02d", i),
			VersionID: "v1", ContentSHA256: TextHash(fmt.Sprint(i)), Kind: "experience", RequiresFactEvaluation: true, Summary: "inspection method"})
	}
	request.Knowledge[34].Required = true
	request.Knowledge[34].Kind = "policy"
	request.Knowledge[34].Summary = strings.Repeat("中文", 150)
	request.Knowledge[34].Applicability = "enterprise contracts"
	request.Knowledge[34].Exclusions = "exclude consumer contracts"
	request.CanonicalEvaluations = []CanonicalEvaluation{{ID: "required-rule", Domain: Technical,
		CapabilityID: "dedicated_line.bandwidth", Mandatory: true, Relation: "insufficient", ReasonCode: "required_fact_missing"}}
	candidate := toolFunc{spec: ToolSpec{ID: "candidate.scope", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"scope"}},
		run: func(_ context.Context, _ ToolInput) ([]Finding, error) {
			return []Finding{{Domain: Technical, Capability: "scope", RuleID: "candidate.scope", Relation: "insufficient",
				Summary: "The first source contains a scope value; comparison remains open.", ReasonCode: "candidate_requires_comparison",
				Facts: []SemanticFact{{ID: "fact-round", Type: "scope", Value: "专线A", Citation: citation}}, Citations: []Citation{citation}}}, nil
		}}
	chat := &contextPlanningModel{}
	tools := append([]Tool{candidate}, SourceFactTools()...)
	runtime, err := New(context.Background(), Config{Planner: ModelPlanner{Model: chat, Identity: "fixture"}, Tools: tools,
		MaxCalls: 2, MaxRounds: 2, Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Run(context.Background(), request)
	if err != nil || report.ToolCalls != 2 || len(chat.inputs) != 2 || len(report.Rounds) != 2 {
		t.Fatalf("planning did not consume the preceding result: %+v / %v", report, err)
	}
	first, second := chat.inputs[0], chat.inputs[1]
	if len(first.Knowledge) != maxPlannerReferences || first.Knowledge[0].KnowledgeID != "knowledge-34" ||
		!first.Knowledge[0].Truncated || first.Knowledge[0].Applicability != "enterprise contracts" ||
		first.Knowledge[0].Exclusions != "exclude consumer contracts" || !utf8.ValidString(first.Knowledge[0].Summary) ||
		first.Truncation.KnowledgeOmitted != 3 || !first.Truncation.FieldsTruncated {
		t.Fatalf("frozen references were not bounded and marked: %+v", first)
	}
	if len(first.VerifiedFacts) != 1 || first.VerifiedFacts[0].FactID != "fact-initial" || len(first.Observations) != 0 ||
		len(first.MandatoryChecks) != 2 || first.MandatoryChecks[0].Reason != "awaiting_evidence" {
		t.Fatalf("initial verified facts or mandatory checks missing: %+v", first)
	}
	if len(second.Observations) != 1 || second.Observations[0].Summary != "The first source contains a scope value; comparison remains open." ||
		second.Observations[0].ClaimStatus != "tool_reported_not_independently_verified" ||
		!slices.Contains(second.Observations[0].FactIDs, "fact-round") || len(second.VerifiedFacts) != 2 ||
		report.Rounds[1].Calls[0].ToolID != "source.technical" {
		t.Fatalf("the model did not select a second action from validated observations: %+v", second)
	}
	if report.Status != "partial" || report.Projection == nil || report.Projection.Gaps[0].Status != "resolved" {
		t.Fatalf("an insufficient mandatory canonical check must remain visible: %+v", report)
	}
	for _, evidence := range report.Projection.Evidence {
		if evidence.Anchor.Page != nil || evidence.Anchor.ElementID != nil {
			t.Fatalf("unverified original-page location was projected: %+v", evidence.Anchor)
		}
	}
}

func TestMandatoryChecklistSurvivesWorksetAndBudgetLimits(t *testing.T) {
	request := requestFixture()
	request.Gaps = nil
	for i := range 20 {
		request.Gaps = append(request.Gaps, Gap{ID: fmt.Sprintf("gap-%02d", i), Domain: Technical,
			Capability: "warranty_months", Question: "check warranty", Mandatory: i >= 2})
	}
	var input PlanInput
	planner := plannerFunc(func(ctx context.Context, supplied PlanInput) (Plan, error) {
		input = supplied
		return (ScopePlanner{}).Plan(ctx, supplied)
	})
	tool := toolFunc{spec: ToolSpec{ID: "missing", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"warranty_months"}},
		run: func(context.Context, ToolInput) ([]Finding, error) {
			return []Finding{{Domain: Technical, Capability: "warranty_months", RuleID: "missing", Relation: "insufficient",
				Summary: "missing evidence", ReasonCode: "required_evidence_missing"}}, nil
		}}
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{tool}, MaxCalls: 1, MaxRounds: 1,
		Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Run(context.Background(), request)
	if err != nil || report.Status != "partial" || report.ToolCalls != 1 || len(report.UnresolvedGapIDs) != 20 ||
		len(input.Gaps) != 16 || len(input.MandatoryChecks) != 18 || input.Gaps[0].ID != "gap-02" ||
		len(report.Projection.Gaps) != 20 || report.Projection.Gaps[19].ReasonCode != "max_calls_reached" ||
		!report.Projection.Gaps[19].Mandatory {
		t.Fatalf("mandatory checks were lost or incorrectly completed: %+v / input=%+v / %v", report, input, err)
	}
}

func TestRequiredReferencesAreNeverSilentlyDropped(t *testing.T) {
	request := requestFixture()
	for i := range maxPlannerReferences + 1 {
		request.Knowledge = append(request.Knowledge, KnowledgeReference{KnowledgeID: fmt.Sprintf("k-%d", i), VersionID: "v1",
			ContentSHA256: TextHash(fmt.Sprint(i)), Kind: "policy", Required: true})
	}
	request.Knowledge = append(request.Knowledge, KnowledgeReference{KnowledgeID: "optional", VersionID: "v1",
		ContentSHA256: TextHash("optional"), Kind: "guidance"})
	var received PlanInput
	planner := plannerFunc(func(ctx context.Context, input PlanInput) (Plan, error) {
		received = input
		return (ScopePlanner{}).Plan(ctx, input)
	})
	report, err := runtimeFixture(t, planner, BaselineTools(), 1).Run(context.Background(), request)
	if err != nil || report.ToolCalls != 1 || len(received.Knowledge) != maxPlannerReferences+1 || received.Truncation.KnowledgeOmitted != 1 {
		t.Fatalf("required policies were dropped or an optional reference exceeded the budget: %+v / %v", received, err)
	}
	for _, ref := range received.Knowledge {
		if !ref.Required || ref.Kind != "policy" {
			t.Fatalf("unexpected reference: %+v", ref)
		}
	}
}

func TestUnverifiedQuoteDoesNotEstablishClaim(t *testing.T) {
	request := requestFixture()
	request.Gaps[0].Mandatory = true
	tool := toolFunc{spec: ToolSpec{ID: "claim", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"warranty_months"}},
		run: func(_ context.Context, in ToolInput) ([]Finding, error) {
			e := in.Evidence[0]
			page := 3
			return []Finding{{Domain: Technical, Capability: "warranty_months", RuleID: "claim", Relation: "compatible",
				Summary:   "A located quote is asserted to prove the wrong conclusion.",
				Citations: []Citation{{EvidenceID: e.ID, Version: e.Version, Quote: e.Text, Start: 0, End: len([]rune(e.Text)), Page: &page, ElementID: "not-proven"}}}}, nil
		}}
	report, err := runtimeFixture(t, ScopePlanner{}, []Tool{tool}, 1).Run(context.Background(), request)
	if err != nil || report.Status != "partial" || len(report.UnresolvedGapIDs) != 1 ||
		report.Domains[2].Findings[0].Relation != "unknown" ||
		report.Projection.Gaps[0].ReasonCode != "claim_requires_semantic_verification" ||
		report.Domains[2].Findings[0].Citations[0].Page != nil || report.Domains[2].Findings[0].Citations[0].ElementID != "" {
		t.Fatalf("located quote became semantic proof: %+v / %v", report, err)
	}
}

func TestInvalidSuppliedFactIsRejectedBeforePlanning(t *testing.T) {
	request, citation := scopeSourceRequest()
	request.VerifiedFacts = []VerifiedFact{{GapID: "g-tech", Domain: Technical,
		Fact: SemanticFact{ID: "fact-false", Type: "scope", Value: "专线B", Citation: citation}}}
	called := false
	planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) { called = true; return Plan{}, nil })
	report, err := runtimeFixture(t, planner, SourceFactTools(), 1).Run(context.Background(), request)
	if !errors.Is(err, ErrInput) || called || report.ToolCalls != 0 {
		t.Fatalf("an unverified typed fact reached the planner: %+v / %v", report, err)
	}
}

func TestCanonicalResultReuseRequiresFrozenEvidence(t *testing.T) {
	for _, hasEvidence := range []bool{true, false} {
		request := requestFixture()
		request.Gaps[0].Mandatory = true
		canonical := CanonicalEvaluation{ID: "rule-1", Domain: Technical, CapabilityID: "warranty_months",
			Mandatory: true, Relation: "conflict", ReasonCode: "warranty_reduced"}
		if hasEvidence {
			canonical.EvidenceIDs = []string{"req", "draft"}
		}
		request.CanonicalEvaluations = []CanonicalEvaluation{canonical}
		report, err := runtimeFixture(t, ScopePlanner{}, BaselineTools(), 1).Run(context.Background(), request)
		if err != nil || report.Projection == nil {
			t.Fatalf("canonical review: %+v / %v", report, err)
		}
		if hasEvidence {
			if report.ToolCalls != 0 || report.Status != "completed" || report.Projection.Gaps[0].Status != "resolved" {
				t.Fatalf("verified canonical result was redundantly evaluated: %+v", report)
			}
		} else if report.ToolCalls != 1 || report.Status != "partial" || report.Projection.Gaps[0].Status != "unknown" ||
			report.Projection.Gaps[0].ReasonCode != "canonical_evidence_unavailable" || report.Projection.Domains[2].Capabilities[0].Relation != nil {
			t.Fatalf("missing canonical evidence was treated as proof: %+v", report)
		}
	}
}

func TestEventSinkIsSynchronousAndStopsAtPersistenceFailure(t *testing.T) {
	for _, failKind := range []string{"", "plan_accepted", "tool_started", "tool_observed", "tool_completed"} {
		t.Run(map[string]string{"": "success", "plan_accepted": "plan", "tool_started": "start", "tool_observed": "observation", "tool_completed": "completion"}[failKind], func(t *testing.T) {
			var events []Event
			calls := 0
			writeErr := errors.New("fenced write rejected")
			clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			tool := toolFunc{spec: ToolSpec{ID: "observe", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"warranty_months"}},
				run: func(context.Context, ToolInput) ([]Finding, error) {
					calls++
					if len(events) == 0 || events[len(events)-1].Kind != "tool_started" {
						t.Fatal("tool ran before its start was persisted")
					}
					return []Finding{{Domain: Technical, Capability: "warranty_months", RuleID: "observe", Relation: "insufficient",
						Summary: "missing evidence", ReasonCode: "required_evidence_missing"}}, nil
				}}
			runtime, err := New(context.Background(), Config{Planner: ScopePlanner{}, Tools: []Tool{tool}, MaxCalls: 1, MaxRounds: 1,
				Timeout: time.Second, ToolTimeout: 100 * time.Millisecond,
				Clock: func() time.Time { clock = clock.Add(time.Millisecond); return clock },
				EventSink: func(_ context.Context, event Event) error {
					events = append(events, event)
					if event.Kind == failKind {
						return writeErr
					}
					return nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			report, err := runtime.Run(context.Background(), requestFixture())
			if failKind == "" {
				if err != nil || calls != 1 || len(events) != 5 || report.Status != "partial" {
					t.Fatalf("events=%+v report=%+v err=%v", events, report, err)
				}
				want := []string{"plan", "plan_accepted", "tool_started", "tool_observed", "tool_completed"}
				for i, event := range events {
					if event.Kind != want[i] || event.Sequence != i+1 || event.EventID == "" || event.OccurredAt.IsZero() ||
						(i > 0 && event.OccurredAt.Before(events[i-1].OccurredAt)) {
						t.Fatalf("event occurrence or order is incorrect: %+v", events)
					}
				}
			} else {
				if !errors.Is(err, ErrEventSink) || !errors.Is(err, writeErr) || events[len(events)-1].Kind != failKind || report.Status == "completed" {
					t.Fatalf("sink error was lost or action continued: events=%+v report=%+v err=%v", events, report, err)
				}
				if (failKind == "plan_accepted" || failKind == "tool_started") && calls != 0 {
					t.Fatalf("tool ran after rejected event: %d", calls)
				}
				if (failKind == "tool_observed" || failKind == "tool_completed") && calls != 1 {
					t.Fatalf("wrong call count: %d", calls)
				}
			}
		})
	}
}

func TestTrustedPriorToolResultsRetainBudgetsAndEventOrder(t *testing.T) {
	request, citation := scopeSourceRequest()
	elapsed := int64(1)
	request.PriorElapsedMS = &elapsed
	request.PriorEventCount = 5
	request.PriorObservations = []PriorToolResult{{Round: 1, ToolID: "candidate.scope", ToolVersion: "v1", GapID: "g-tech",
		Findings: []Finding{{Domain: Technical, Capability: "scope", RuleID: "candidate.scope", Relation: "insufficient",
			Summary: "First source value found.", ReasonCode: "candidate_requires_comparison",
			Facts: []SemanticFact{{ID: "fact-prior", Type: "scope", Value: "专线A", Citation: citation}}, Citations: []Citation{citation}}}}}
	before, _ := json.Marshal(request)
	first := toolFunc{spec: ToolSpec{ID: "candidate.scope", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"scope"}},
		run: func(context.Context, ToolInput) ([]Finding, error) {
			return nil, errors.New("prior call must not repeat")
		}}
	var supplied PlanInput
	planner := plannerFunc(func(_ context.Context, input PlanInput) (Plan, error) {
		supplied = input
		return Plan{Calls: []Call{{ToolID: "source.technical", Domain: Technical, GapID: "g-tech", Reason: "compare both sources"}}}, nil
	})
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: append([]Tool{first}, SourceFactTools()...),
		MaxCalls: 2, MaxRounds: 2, Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Run(context.Background(), request)
	if err != nil || report.Status != "completed" || report.ToolCalls != 2 || len(report.Rounds) != 1 || report.Rounds[0].Round != 2 ||
		len(supplied.Observations) != 1 || supplied.Observations[0].Round != 1 || supplied.Observations[0].Summary != "First source value found." ||
		len(supplied.VerifiedFacts) != 1 || supplied.VerifiedFacts[0].FactID != "fact-prior" || supplied.RemainingCalls != 1 ||
		len(supplied.PreviouslyCalled) != 1 || len(report.Events) != 5 || report.Events[0].Sequence != 6 {
		t.Fatalf("prior result, budget or event sequence was lost: %+v / input=%+v / %v", report, supplied, err)
	}
	after, _ := json.Marshal(request)
	if string(before) != string(after) {
		t.Fatal("prior results were mutated")
	}
}

func TestPriorResultsFailClosedAndCannotResetLimits(t *testing.T) {
	for _, scenario := range []string{"bad_quote", "bad_tool_version", "missing_elapsed", "bad_event_count", "calls_exhausted", "time_exhausted"} {
		t.Run(scenario, func(t *testing.T) {
			request, citation := scopeSourceRequest()
			elapsed := int64(1)
			request.PriorElapsedMS, request.PriorEventCount = &elapsed, 5
			request.PriorObservations = []PriorToolResult{{Round: 1, ToolID: "candidate.scope", ToolVersion: "v1", GapID: "g-tech",
				Findings: []Finding{{Domain: Technical, Capability: "scope", RuleID: "candidate.scope", Relation: "insufficient",
					Summary: "Prior candidate", ReasonCode: "candidate_requires_comparison", Citations: []Citation{citation}}}}}
			maxCalls := 2
			switch scenario {
			case "bad_quote":
				request.PriorObservations[0].Findings[0].Citations[0].Quote = "forged"
			case "bad_tool_version":
				request.PriorObservations[0].ToolVersion = "v2"
			case "missing_elapsed":
				request.PriorElapsedMS = nil
			case "bad_event_count":
				request.PriorEventCount = 2
			case "calls_exhausted":
				maxCalls = 1
			case "time_exhausted":
				elapsed = 1000
			}
			called := false
			planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) { called = true; return Plan{}, nil })
			tool := toolFunc{spec: ToolSpec{ID: "candidate.scope", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"scope"}},
				run: func(context.Context, ToolInput) ([]Finding, error) { t.Fatal("unexpected tool call"); return nil, nil }}
			runtime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{tool}, MaxCalls: maxCalls, MaxRounds: 2,
				Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			report, err := runtime.Run(context.Background(), request)
			if scenario == "calls_exhausted" || scenario == "time_exhausted" {
				if err != nil || called || report.ToolCalls != 1 || report.Status != "partial" || len(report.Events) != 0 {
					t.Fatalf("prior budget was reset or events were replayed: %+v / %v", report, err)
				}
			} else if !errors.Is(err, ErrInput) || called || report.ToolCalls != 0 {
				t.Fatalf("invalid prior output reached the planner: %+v / %v", report, err)
			}
		})
	}
}
