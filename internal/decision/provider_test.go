package decision

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/access"
)

func TestDisabledAndShadowKeepAcceptedRulesEmpty(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	disabled, err := NewProvider(Config{Mode: ModeDisabled})
	if err != nil {
		t.Fatal(err)
	}
	disabledResult := disabled.Evaluate(context.Background(), input)
	if disabledResult.Status != StatusDisabled || disabledResult.Suggestion != nil ||
		disabledResult.AcceptedRules == nil || len(disabledResult.AcceptedRules) != 0 ||
		disabledResult.Event.LatencyMS != 0 {
		t.Fatalf("disabled provider had an effect: %+v", disabledResult)
	}

	var calls atomic.Int32
	server := responseServer(t, input, choiceAnswer(input, "manual_review", .7), &calls)
	defer server.Close()
	shadow, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	shadowResult := shadow.Evaluate(context.Background(), input)
	if shadowResult.Status != StatusSuggested || shadowResult.Suggestion == nil ||
		shadowResult.AcceptedRules == nil || len(shadowResult.AcceptedRules) != 0 ||
		!reflect.DeepEqual(disabledResult.AcceptedRules, shadowResult.AcceptedRules) {
		t.Fatalf("shadow changed rule acceptance: disabled=%+v shadow=%+v", disabledResult, shadowResult)
	}
	if calls.Load() != 1 {
		t.Fatalf("shadow calls=%d, want 1", calls.Load())
	}
}

func TestShadowSendsBoundedChoiceAndRecordsVersionProvenance(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	var requestBody []byte
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("request target = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		requestBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(responseBody(t, input, choiceAnswer(input, "bandwidth", .8)))
	}))
	defer server.Close()
	provider, err := NewProvider(Config{
		Mode: ModeShadow, Endpoint: server.URL + "/v1",
		ModelRevision: ExpectedMultilingualRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	if result.Status != StatusSuggested || result.Suggestion == nil || result.Event.FailureReason != "" {
		t.Fatalf("decision = %+v", result)
	}
	if result.Suggestion.CandidateID != "bandwidth" || result.Suggestion.Confidence == nil ||
		*result.Suggestion.Confidence != .8 || result.Suggestion.ConfidenceSource != "answer_confidence" ||
		result.Suggestion.Probabilities["manual_review"] == 0 {
		t.Fatalf("raw recommendation evidence lost: %+v", result.Suggestion)
	}
	if result.Event.RequestID != input.RequestID || result.Event.DecisionID != input.DecisionID ||
		result.Event.RunID != input.RunID || result.Event.AttemptID != input.AttemptID ||
		result.Event.TraceID != input.TraceID || result.Event.RulePackHash != input.RulePackHash ||
		result.Event.ModelName != ModelAlias || result.Event.ModelRevision != ExpectedMultilingualRevision ||
		result.Event.ModelRevisionSource != "configured_expected_pin" ||
		result.Event.ObservedModelRevision != nil || result.Event.RoutedCheckpoint != ModelAlias ||
		result.Event.Confidence == nil || *result.Event.Confidence != .8 || result.Event.LatencyMS <= 0 {
		t.Fatalf("decision event missing provenance/correlation: %+v", result.Event)
	}
	var request map[string]any
	if err := json.Unmarshal(requestBody, &request); err != nil {
		t.Fatal(err)
	}
	if len(request) != 3 || request["model"] != ModelAlias || request["state"] != input.State {
		t.Fatalf("unexpected System One body: %s", requestBody)
	}
	questions := request["questions"].(map[string]any)
	if len(questions) != 1 {
		t.Fatalf("questions = %#v", questions)
	}
	question := questions["review_focus"].(map[string]any)
	if question["type"] != "choice" || question["instructions"] !=
		"根据材料选择唯一应优先核查的重点；多个同等问题、材料含糊或超出范围时选择 manual_review。只做分流，不判断合同结论。" {
		t.Fatalf("wrong fixed choice question: %#v", question)
	}
	criteria := question["criteria"].(map[string]any)
	if len(criteria) != len(input.AllowedCandidates) || criteria["manual_review"] == nil ||
		strings.Contains(string(requestBody), input.TraceID) || strings.Contains(string(requestBody), input.Resource.TenantID) {
		t.Fatalf("request leaked identity or omitted candidates: %s", requestBody)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d, want 1", calls.Load())
	}
}

func TestShadowLowConfidenceAbstainsAndKeepsSuggestion(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	server := responseServer(t, input, choiceAnswer(input, "manual_review", .55), nil)
	defer server.Close()
	threshold := .7
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1", ShadowThreshold: &threshold})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	if result.Status != StatusAbstained || result.Suggestion == nil || result.Suggestion.CandidateID != "manual_review" ||
		result.Event.Abstention != "abstained" || len(result.AcceptedRules) != 0 {
		t.Fatalf("low-confidence decision was not an explicit abstention: %+v", result)
	}
	if result.Event.ConfidenceThreshold == nil || *result.Event.ConfidenceThreshold != threshold {
		t.Fatalf("shadow threshold missing from event: %+v", result.Event)
	}
}

func TestAssistRequiresExplicitCapabilityPolicyAndThreshold(t *testing.T) {
	input := inputFixture(CapabilityRuleRecommendation)
	var calls atomic.Int32
	server := responseServer(t, input, choiceAnswer(input, "optional_payment", .9), &calls)
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeAssist, Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	if result.Status != StatusApprovalRequired || result.Event.FailureReason != FailureApprovalRequired ||
		len(result.AcceptedRules) != 0 || calls.Load() != 0 {
		t.Fatalf("unapproved assist was not rejected before HTTP: result=%+v calls=%d", result, calls.Load())
	}
}

func TestAssistOrdersAllAllowedRulesAndCannotChangeRelations(t *testing.T) {
	input := inputFixture(CapabilityRuleRecommendation)
	server := responseServer(t, input, choiceAnswer(input, "optional_sla", .9), nil)
	defer server.Close()
	provider, err := NewProvider(Config{
		Mode: ModeAssist, Endpoint: server.URL + "/v1",
		AssistApproval: &AssistApproval{Capability: CapabilityRuleRecommendation,
			PolicyVersion: access.PolicyVersion, MinConfidence: .8},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	want := []string{"optional_sla", "optional_payment", "optional_scope"}
	if result.Status != StatusOrdered || !reflect.DeepEqual(result.AcceptedRules, want) || result.Suggestion == nil {
		t.Fatalf("assist did not return a complete ordering: %+v", result)
	}
	if contains(result.AcceptedRules, "mandatory_payment") || contains(result.AcceptedRules, "mandatory_scope") {
		t.Fatalf("mandatory rule entered the model ordering: %v", result.AcceptedRules)
	}
	// Decision results contain only the suggestion/order overlay; relation and
	// evidence values remain owned by the deterministic rule executor.
	if result.Event.Recommendation != "optional_sla" || result.Event.Confidence == nil || *result.Event.Confidence != .9 {
		t.Fatalf("wrong recommendation event: %+v", result.Event)
	}
}

func TestAssistThresholdIsFrozenAtConstruction(t *testing.T) {
	input := inputFixture(CapabilityRuleRecommendation)
	server := responseServer(t, input, choiceAnswer(input, "optional_payment", .75), nil)
	defer server.Close()
	threshold := .8
	provider, err := NewProvider(Config{
		Mode: ModeAssist, Endpoint: server.URL + "/v1",
		AssistApproval: &AssistApproval{Capability: CapabilityRuleRecommendation,
			PolicyVersion: access.PolicyVersion, MinConfidence: threshold},
	})
	if err != nil {
		t.Fatal(err)
	}
	threshold = .5
	result := provider.Evaluate(context.Background(), input)
	if result.Status != StatusAbstained || result.Event.Abstention != "abstained" || len(result.AcceptedRules) != 0 {
		t.Fatalf("mutating caller threshold changed frozen approval: %+v", result)
	}
	if result.Event.ConfidenceThreshold == nil || *result.Event.ConfidenceThreshold != .8 {
		t.Fatalf("assist threshold missing from event: %+v", result.Event)
	}
}

func TestChoiceCannotEnterAssistMode(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	var calls atomic.Int32
	server := responseServer(t, input, choiceAnswer(input, "bandwidth", .9), &calls)
	defer server.Close()
	provider, err := NewProvider(Config{
		Mode: ModeAssist, Endpoint: server.URL + "/v1",
		AssistApproval: &AssistApproval{Capability: CapabilityChoice,
			PolicyVersion: access.PolicyVersion, MinConfidence: .5},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	if result.Status != StatusAssistUnsupported || len(result.AcceptedRules) != 0 || calls.Load() != 0 {
		t.Fatalf("generic classification entered assist: result=%+v calls=%d", result, calls.Load())
	}
}

func TestAuthorizationAndMandatoryCandidateGuardPreventSending(t *testing.T) {
	var calls atomic.Int32
	input := inputFixture(CapabilityRuleRecommendation)
	server := responseServer(t, input, choiceAnswer(input, "optional_payment", .9), &calls)
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}

	foreign := input
	foreign.Principal.TenantID = "tenant-b"
	result := provider.Evaluate(context.Background(), foreign)
	if result.Status != StatusDenied || calls.Load() != 0 {
		t.Fatalf("foreign tenant request was not denied before send: result=%+v calls=%d", result, calls.Load())
	}

	unauthorized := inputFixture(CapabilityRuleRecommendation)
	unauthorized.AllowedCandidates = append(unauthorized.AllowedCandidates,
		Candidate{ID: "mandatory_payment", Description: "mandatory rule"})
	result = provider.Evaluate(context.Background(), unauthorized)
	if result.Status != StatusInvalidInput || calls.Load() != 0 {
		t.Fatalf("mandatory rule candidate was sent: result=%+v calls=%d", result, calls.Load())
	}

	stalePolicy := inputFixture(CapabilityChoice)
	stalePolicy.PolicyVersion = "old-policy"
	result = provider.Evaluate(context.Background(), stalePolicy)
	if result.Status != StatusInvalidInput || calls.Load() != 0 {
		t.Fatalf("stale policy input was sent: result=%+v calls=%d", result, calls.Load())
	}
}

func TestUnknownCandidateResponseIsRejected(t *testing.T) {
	input := inputFixture(CapabilityRuleRecommendation)
	answer := choiceAnswer(input, "optional_payment", .9)
	answer["choice"] = "mandatory_payment"
	server := responseServer(t, input, answer, nil)
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	if result.Status != StatusUnavailable || result.Event.FailureReason != FailureInvalidAnswer ||
		result.Suggestion != nil || len(result.AcceptedRules) != 0 {
		t.Fatalf("unknown candidate was accepted: %+v", result)
	}
}

func TestMissingRevisionProvenanceRemainsUnknown(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	server := responseServer(t, input, choiceAnswer(input, "bandwidth", .8), nil)
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	if result.Event.ModelRevision != "" || result.Event.ModelRevisionSource != "unknown" ||
		result.Event.ObservedModelRevision != nil || result.Event.RoutedCheckpoint != ModelAlias {
		t.Fatalf("missing runtime provenance was fabricated: %+v", result.Event)
	}
}

func TestObserverReceivesEinoCorrelationEvent(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	server := responseServer(t, input, choiceAnswer(input, "bandwidth", .8), nil)
	defer server.Close()
	var observed DecisionEvent
	provider, err := NewProvider(Config{
		Mode: ModeShadow, Endpoint: server.URL + "/v1",
		Observer: ObserverFunc(func(_ context.Context, event DecisionEvent) { observed = event }),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	if observed.TraceID != input.TraceID || observed.RunID != input.RunID || observed.AttemptID != input.AttemptID ||
		observed.DecisionID != input.DecisionID || observed.Status != result.Status || observed.LatencyMS <= 0 {
		t.Fatalf("observer event does not correlate with decision: %+v", observed)
	}
}

func inputFixture(capability Capability) Input {
	input := Input{
		RequestID: "request_1", DecisionID: "decision_1", RunID: "run_1", AttemptID: "attempt_1",
		TraceID: "trace-001", Principal: access.Principal{UserID: "alice", TenantID: "tenant-a", Enabled: true,
			Grants: []access.Grant{{Role: "reviewer", Scope: "case", CaseID: "case-a"}}},
		Resource:   access.Resource{TenantID: "tenant-a", CaseID: "case-a", OrgID: "sales", OwnerID: "alice"},
		Capability: capability, State: "合同审核范围内的短摘要。", RulePackVersion: "decision-pack-v1",
		RulePackHash: strings.Repeat("a", 64), PolicyVersion: access.PolicyVersion,
	}
	if capability == CapabilityChoice {
		input.AllowedCandidates = []Candidate{
			{ID: "bandwidth", Description: "带宽承诺"},
			{ID: "monthly_sla", Description: "月度服务等级"},
			{ID: "advance_payment", Description: "预付款条件"},
			{ID: "manual_review", Description: "人工复核"},
		}
		return input
	}
	input.AllowedCandidates = []Candidate{
		{ID: "optional_payment", Description: "可选付款规则"},
		{ID: "optional_sla", Description: "可选服务等级规则"},
		{ID: "optional_scope", Description: "可选范围规则"},
	}
	input.MandatoryRuleIDs = []string{"mandatory_payment", "mandatory_scope"}
	return input
}

func choiceAnswer(input Input, choice string, confidence float64) map[string]any {
	probabilities := make(map[string]float64, len(input.AllowedCandidates))
	remaining := 1 - confidence
	otherCount := len(input.AllowedCandidates) - 1
	for _, candidate := range input.AllowedCandidates {
		if candidate.ID == choice {
			probabilities[candidate.ID] = confidence
		} else {
			probabilities[candidate.ID] = remaining / float64(otherCount)
		}
	}
	return map[string]any{
		"type": "choice", "choice": choice, "probabilities": probabilities,
		"confidence": .01, "answer_confidence": confidence,
	}
}

func responseBody(t *testing.T, input Input, answer map[string]any) []byte {
	t.Helper()
	questionID, _ := questionFor(input.Capability)
	body, err := json.Marshal(map[string]any{
		"model":   "laya-rl-agent",
		"answers": map[string]any{questionID: answer},
		"usage":   map[string]any{"input_tokens": 10, "output_tokens": 0},
		"routing": map[string]any{"model": ModelAlias},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func responseServer(t *testing.T, input Input, answer map[string]any, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls != nil {
			calls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(responseBody(t, input, answer))
	}))
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestMeasuredLatencyIncludesHTTPResponseWait(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write(responseBody(t, input, choiceAnswer(input, "bandwidth", .8)))
	}))
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	if result.Event.LatencyMS < 30 {
		t.Fatalf("latency %.2fms did not include delayed HTTP response", result.Event.LatencyMS)
	}
}

func TestProviderDefaultsDisabledAndRejectsInvalidConfiguration(t *testing.T) {
	provider, err := NewProvider(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if result := provider.Evaluate(context.Background(), inputFixture(CapabilityChoice)); result.Status != StatusDisabled {
		t.Fatalf("empty config did not default to disabled: %+v", result)
	}
	threshold := math.NaN()
	for name, config := range map[string]Config{
		"mode":      {Mode: Mode("enabled")},
		"revision":  {Mode: ModeDisabled, ModelRevision: "unreviewed"},
		"threshold": {Mode: ModeDisabled, ShadowThreshold: &threshold},
		"endpoint":  {Mode: ModeShadow, Endpoint: "http://example.com/v1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewProvider(config); err == nil {
				t.Fatalf("accepted invalid %s configuration", name)
			}
		})
	}
}

func TestInvalidInputShapesFailBeforeSending(t *testing.T) {
	base := inputFixture(CapabilityRuleRecommendation)
	var calls atomic.Int32
	server := responseServer(t, base, choiceAnswer(base, "optional_payment", .9), &calls)
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*Input){
		"missing request reference": func(in *Input) { in.RequestID = "" },
		"missing trace reference":   func(in *Input) { in.TraceID = "bad trace id" },
		"bad rulepack version":      func(in *Input) { in.RulePackVersion = "" },
		"bad rulepack hash":         func(in *Input) { in.RulePackHash = "not-a-hash" },
		"oversize state":            func(in *Input) { in.State = strings.Repeat("x", MaxStateBytes+1) },
		"invalid UTF-8 state":       func(in *Input) { in.State = string([]byte{0xff}) },
		"unsupported capability":    func(in *Input) { in.Capability = Capability("score") },
		"too many candidates": func(in *Input) {
			in.AllowedCandidates = append(in.AllowedCandidates, make([]Candidate, MaxCandidates)...)
		},
		"duplicate candidate":         func(in *Input) { in.AllowedCandidates[1].ID = in.AllowedCandidates[0].ID },
		"empty candidate description": func(in *Input) { in.AllowedCandidates[0].Description = " " },
		"oversize candidate description": func(in *Input) {
			in.AllowedCandidates[0].Description = strings.Repeat("x", MaxCandidateDescriptionBytes+1)
		},
		"duplicate mandatory id": func(in *Input) { in.MandatoryRuleIDs = append(in.MandatoryRuleIDs, in.MandatoryRuleIDs[0]) },
		"mandatory candidate": func(in *Input) {
			in.AllowedCandidates = append(in.AllowedCandidates, Candidate{ID: "mandatory_payment", Description: "mandatory"})
		},
	}
	choiceOnly := map[string]func(*Input){
		"missing manual review":        func(in *Input) { in.AllowedCandidates[3].ID = "fallback" },
		"choice carries mandatory ids": func(in *Input) { in.MandatoryRuleIDs = []string{"mandatory_payment"} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			input := inputFixture(CapabilityRuleRecommendation)
			mutate(&input)
			result := provider.Evaluate(context.Background(), input)
			if result.Status != StatusInvalidInput || calls.Load() != 0 {
				t.Fatalf("invalid input reached HTTP: result=%+v calls=%d", result, calls.Load())
			}
		})
	}
	for name, mutate := range choiceOnly {
		t.Run(name, func(t *testing.T) {
			input := inputFixture(CapabilityChoice)
			mutate(&input)
			result := provider.Evaluate(context.Background(), input)
			if result.Status != StatusInvalidInput || calls.Load() != 0 {
				t.Fatalf("invalid choice input reached HTTP: result=%+v calls=%d", result, calls.Load())
			}
		})
	}
}

func TestAssistApprovalMismatchNeverSends(t *testing.T) {
	input := inputFixture(CapabilityRuleRecommendation)
	var calls atomic.Int32
	server := responseServer(t, input, choiceAnswer(input, "optional_payment", .9), &calls)
	defer server.Close()
	approvals := map[string]*AssistApproval{
		"policy":     {Capability: CapabilityRuleRecommendation, PolicyVersion: "stale-policy", MinConfidence: .8},
		"capability": {Capability: CapabilityChoice, PolicyVersion: access.PolicyVersion, MinConfidence: .8},
		"threshold":  {Capability: CapabilityRuleRecommendation, PolicyVersion: access.PolicyVersion, MinConfidence: math.NaN()},
	}
	for name, approval := range approvals {
		t.Run(name, func(t *testing.T) {
			provider, err := NewProvider(Config{Mode: ModeAssist, Endpoint: server.URL + "/v1", AssistApproval: approval})
			if err != nil {
				t.Fatal(err)
			}
			result := provider.Evaluate(context.Background(), input)
			if result.Status != StatusApprovalRequired || result.Event.ConfidenceThreshold != nil || calls.Load() != 0 {
				t.Fatalf("mismatched approval reached HTTP: result=%+v calls=%d", result, calls.Load())
			}
		})
	}
}
