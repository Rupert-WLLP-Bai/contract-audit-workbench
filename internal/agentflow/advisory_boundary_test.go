package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestAdvisoryFeatureConfigurationFreezesOnlySupportedFeatures(t *testing.T) {
	clearAdvisoryEnv(t)
	t.Setenv("AGENT_PLANNER_MODE", "deterministic")
	fallback, err := ResolvePlannerConfiguration("")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_JEV_MODE", "advisory")
	t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
	canonical, err := ResolvePlannerConfiguration("")
	if err != nil || canonical.Advisory == nil {
		t.Fatalf("canonical advisory configuration: %+v / %v", canonical, err)
	}
	for _, test := range []struct {
		name     string
		features string
		enabled  bool
	}{
		{"normalized duplicates", " KNOWLEDGE_RERANK, ,knowledge_rerank, ", true},
		{"empty explicit list", " , , ", false},
		{"supported followed by unknown", "knowledge_rerank,citation_support", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TYPESAFE_JEV_FEATURES", test.features)
			configuration, err := ResolvePlannerConfiguration("")
			if err != nil || configuration.Validate() != nil {
				t.Fatalf("advisory configuration blocked review acceptance: %+v / %v", configuration, err)
			}
			if test.enabled {
				if configuration.ConfigurationID != canonical.ConfigurationID || configuration.Advisory == nil ||
					len(configuration.Advisory.Features) != 1 || !configuration.Advisory.Supports(AdvisoryFeatureKnowledgeRerank) {
					t.Fatalf("equivalent feature lists changed the frozen identity: %+v", configuration)
				}
			} else if configuration.Advisory != nil || configuration.ConfigurationID != fallback.ConfigurationID {
				t.Fatalf("unsupported feature configuration did not use deterministic fallback: %+v", configuration)
			}
			if configuration.Advisory.Supports("citation_support") {
				t.Fatal("an unsupported feature gained authority")
			}
		})
	}
}

// This model records the actual context the runtime supplies on each round.
// The first inspection is deliberately inconclusive so the next model call
// must retain the policy, the mandatory check and the previous observation.
type boundaryPlanningModel struct {
	inputs      []PlanInput
	secondUsage *schema.TokenUsage
	failSecond  bool
}

func (m *boundaryPlanningModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	var input PlanInput
	if len(messages) != 2 || json.Unmarshal([]byte(messages[1].Content), &input) != nil {
		return nil, errors.New("invalid planning input")
	}
	m.inputs = append(m.inputs, input)
	toolID := "inspect.warranty"
	usage := &schema.TokenUsage{PromptTokens: 11, CompletionTokens: 4, TotalTokens: 15}
	if len(m.inputs) > 1 {
		if m.failSecond {
			return nil, errors.New("model unavailable")
		}
		toolID, usage = "baseline.technical", m.secondUsage
	}
	body, err := json.Marshal(Plan{Calls: []Call{{ToolID: toolID, Domain: Technical, GapID: "g-tech", Reason: "check the frozen warranty evidence"}}})
	if err != nil {
		return nil, err
	}
	message := schema.AssistantMessage(string(body), nil)
	if usage != nil {
		message.ResponseMeta = &schema.ResponseMeta{Usage: usage}
	}
	return message, nil
}

func TestModelPlanningKeepsPolicyAndReportsOnlyCompleteUsage(t *testing.T) {
	for _, test := range []struct {
		name        string
		secondUsage *schema.TokenUsage
		failSecond  bool
		knownUsage  bool
	}{
		{"two measured rounds", &schema.TokenUsage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}, false, true},
		{"missing second measurement", nil, false, false},
		{"invalid second measurement", &schema.TokenUsage{PromptTokens: -1, CompletionTokens: 3, TotalTokens: 2}, false, false},
		{"second call falls back", nil, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := requestWithPolicyTrace(false)
			request.Gaps[0].Mandatory = true
			before, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			inspection := toolFunc{
				spec: ToolSpec{ID: "inspect.warranty", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"warranty_months"}},
				run: func(context.Context, ToolInput) ([]Finding, error) {
					return []Finding{{Domain: Technical, Capability: "warranty_months", RuleID: "inspect.warranty", Relation: "insufficient",
						Summary: "The sources still require a deterministic comparison.", ReasonCode: "comparison_required"}}, nil
				},
			}
			chat := &boundaryPlanningModel{secondUsage: test.secondUsage, failSecond: test.failSecond}
			tools := append([]Tool{inspection}, BaselineTools()...)
			report, err := runtimeFixture(t, ModelPlanner{Model: chat, Identity: "usage-fixture"}, tools, 2).Run(context.Background(), request)
			wantStatus := "completed"
			if test.failSecond {
				wantStatus = "partial"
			}
			if err != nil || report.Status != wantStatus || report.ToolCalls != 2 || report.Projection == nil || len(chat.inputs) != 2 {
				t.Fatalf("two-round review did not complete: %+v / %v", report, err)
			}
			if report.FallbackUsed != test.failSecond || report.Projection.Gaps[0].Status != "resolved" || !report.Projection.Gaps[0].Mandatory {
				t.Fatalf("model availability changed the mandatory check: %+v", report.Projection)
			}
			for round, input := range chat.inputs {
				if len(input.Knowledge) != 1 || !input.Knowledge[0].Required || input.Knowledge[0].KnowledgeID != "policy" ||
					input.KnowledgePolicyStatus != "available" || len(input.MandatoryChecks) != 1 || input.MandatoryChecks[0].GapID != "g-tech" {
					t.Fatalf("round %d lost required policy or check: %+v", round+1, input)
				}
			}
			if len(chat.inputs[1].Observations) != 1 || chat.inputs[1].Observations[0].ReasonCode != "comparison_required" ||
				chat.inputs[1].Observations[0].ClaimStatus != "tool_reported_not_independently_verified" {
				t.Fatalf("second round lost the inconclusive observation: %+v", chat.inputs[1].Observations)
			}
			trace := report.Projection.KnowledgeSelection
			if trace == nil || len(trace.PlannerRounds) != 2 {
				t.Fatalf("planning delivery was not recorded: %+v", trace)
			}
			wantOutcome := "returned"
			if test.failSecond {
				wantOutcome = "failed"
			}
			if trace.PlannerRounds[1].Outcome != wantOutcome || len(trace.PlannerRounds[1].ReferencesProvided) != 1 ||
				trace.PlannerRounds[1].ReferencesProvided[0].KnowledgeID != "policy" {
				t.Fatalf("model failure hid policy delivery: %+v", trace.PlannerRounds[1])
			}
			usage := report.Projection.Usage
			if usage.PlannerCalls != 2 {
				t.Fatalf("usage omitted an attempted model call: %+v", usage)
			}
			if test.knownUsage {
				if usage.InputTokens == nil || usage.OutputTokens == nil || usage.TotalTokens == nil ||
					*usage.InputTokens != 18 || *usage.OutputTokens != 7 || *usage.TotalTokens != 25 {
					t.Fatalf("usage did not sum both measured rounds: %+v", usage)
				}
			} else if usage.InputTokens != nil || usage.OutputTokens != nil || usage.TotalTokens != nil {
				t.Fatalf("partial measurements were reported as complete usage: %+v", usage)
			}
			after, err := json.Marshal(request)
			if err != nil || string(after) != string(before) || report.InputSHA256 != TextHash(string(before)) {
				t.Fatalf("execution changed the frozen caller input: %v", err)
			}
		})
	}
}
