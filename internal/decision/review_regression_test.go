package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReviewedRuleIDsPreserveRulePackIdentity(t *testing.T) {
	input := inputFixture(CapabilityRuleRecommendation)
	input.AllowedCandidates = []Candidate{{ID: "ENT-TECH-OPT-001", Description: "核对可选技术约定"}, {ID: "ENT-COM-OPT-001", Description: "核对可选商务约定"}}
	input.MandatoryRuleIDs = []string{"ENT-TECH-BW-001"}
	var calls atomic.Int32
	server := responseServer(t, input, choiceAnswer(input, "ENT-COM-OPT-001", .8), &calls)
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeAssist, Endpoint: server.URL + "/v1", AssistApproval: &AssistApproval{
		Capability: CapabilityRuleRecommendation, PolicyVersion: input.PolicyVersion, MinConfidence: .7,
	}})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	if result.Status != StatusOrdered || calls.Load() != 1 || len(result.AcceptedRules) != 2 ||
		result.AcceptedRules[0] != "ENT-COM-OPT-001" || result.AcceptedRules[1] != "ENT-TECH-OPT-001" ||
		contains(result.AcceptedRules, "ENT-TECH-BW-001") {
		t.Fatalf("RulePack identity was rejected or mandatory rule entered ordering: %+v", result)
	}
}

func TestReviewedSuccessResponseCannotEchoBearerToken(t *testing.T) {
	const token = "private-server-key"
	input := inputFixture(CapabilityChoice)
	var wire map[string]any
	if err := json.Unmarshal(responseBody(t, input, choiceAnswer(input, "bandwidth", .8)), &wire); err != nil {
		t.Fatal(err)
	}
	wire["model"] = token
	body, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	server := rawResponseServer(string(body))
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1", BearerToken: token})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Evaluate(context.Background(), input)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnavailable || result.Event.FailureReason != FailureInvalidAnswer || bytes.Contains(encoded, []byte(token)) {
		t.Fatalf("success response leaked its bearer credential: %s", encoded)
	}
}

func TestReviewedAdapterAuthorizesAndValidatesDirectCalls(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	var calls atomic.Int32
	server := responseServer(t, input, choiceAnswer(input, "bandwidth", .8), &calls)
	defer server.Close()
	adapter, err := NewSystemOneAdapter(server.URL+"/v1", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	denied := input
	denied.Resource.TenantID = "foreign-tenant"
	if result := adapter.Query(context.Background(), denied); result.FailureReason != FailureAuthorization {
		t.Fatalf("direct adapter bypassed authorization: %+v", result)
	}
	invalid := input
	invalid.AllowedCandidates = nil
	if result := adapter.Query(context.Background(), invalid); result.FailureReason != FailureInvalidInput || calls.Load() != 0 {
		t.Fatalf("direct adapter sent an invalid request: %+v calls=%d", result, calls.Load())
	}
	result := adapter.Query(nil, input)
	if result.FailureReason != "" || result.Suggestion == nil || result.Suggestion.CandidateID != "bandwidth" || calls.Load() != 1 {
		t.Fatalf("valid direct adapter query failed: %+v", result)
	}
}

func TestReviewedLocalhostDialingUsesLoopback(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	var calls atomic.Int32
	server := responseServer(t, input, choiceAnswer(input, "bandwidth", .8), &calls)
	defer server.Close()
	endpoint := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	result := runShadowAt(t, endpoint+"/v1", input, 0)
	if result.Status != StatusSuggested || calls.Load() != 1 {
		t.Fatalf("localhost did not resolve to the bounded loopback service: %+v", result)
	}
}

func TestReviewedProbabilityValuesMustBeJSONNumbers(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	answer := choiceAnswer(input, "bandwidth", .8)
	probabilities := map[string]any{}
	for id, probability := range answer["probabilities"].(map[string]float64) {
		probabilities[id] = probability
	}
	probabilities["bandwidth"] = "0.8"
	answer["probabilities"] = probabilities
	server := rawResponseServer(string(responseBody(t, input, answer)))
	defer server.Close()
	result := runShadowAt(t, server.URL+"/v1", input, 0)
	if result.Event.FailureReason != FailureInvalidProbabilities || result.Suggestion != nil {
		t.Fatalf("string probability accepted as a numeric probability: %+v", result)
	}
}

func TestReviewedUnavailableServiceFallsBackWithoutLeakingResponse(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "sensitive upstream details", http.StatusServiceUnavailable)
	}))
	endpoint := server.URL + "/v1"
	result := runShadowAt(t, endpoint, input, 0)
	if result.Status != StatusUnavailable || result.Event.FailureReason != FailureHTTPStatus || len(result.AcceptedRules) != 0 {
		t.Fatalf("503 did not preserve suggestion-only fallback: %+v", result)
	}
	server.Close()
	result = runShadowAt(t, endpoint, input, 0)
	if result.Status != StatusUnavailable || result.Event.FailureReason != FailureTransport || result.Suggestion != nil || len(result.AcceptedRules) != 0 {
		t.Fatalf("unavailable service produced a usable recommendation: %+v", result)
	}
}
