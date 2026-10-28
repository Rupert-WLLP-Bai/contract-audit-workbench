package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/decision"
	"contractaudit/internal/model"
)

func TestChatShadowDeniedDoesNotCallModel(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"observed-model","choices":[{"finish_reason":"stop","message":{"content":"{\"choice\":\"bandwidth\"}"}}]}`))
	}))
	defer server.Close()

	input := chatShadowTestInput(t, "denied")
	input.Principal.Enabled = false
	result, _ := chatShadow(context.Background(), model.New(model.Config{BaseURL: server.URL, ChatModel: "configured-model"}), input, "configured-model", time.Second)
	if result.Status != decision.StatusDenied || result.Event.Mode != decision.ModeShadow || result.Event.FailureReason != decision.FailureAuthorization || result.Event.Status != result.Status {
		t.Fatalf("unauthorized request did not remain denied: %+v", result)
	}
	requireNoAcceptedRules(t, result)
	if calls.Load() != 0 {
		t.Fatalf("unauthorized request reached the model: calls=%d", calls.Load())
	}
}

func TestChatShadowConcurrentIdentitiesStayIsolated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"observed-chat-model","choices":[{"finish_reason":"stop","message":{"content":"{\"choice\":\"bandwidth\"}"}}]}`))
	}))
	defer server.Close()
	client := model.New(model.Config{BaseURL: server.URL, ChatModel: "configured-model"})

	const count = 16
	type completed struct {
		input  decision.Input
		result decision.Result
		chat   *model.ChoiceResult
	}
	results := make(chan completed, count)
	for index := range count {
		suffix := "parallel-" + string(rune('a'+index))
		input := chatShadowTestInput(t, suffix)
		input.Principal.UserID += "-" + suffix
		input.Principal.TenantID += "-" + suffix
		input.Principal.Grants[0].CaseID += "-" + suffix
		input.Resource.TenantID = input.Principal.TenantID
		input.Resource.CaseID += "-" + suffix
		input.Resource.OwnerID = input.Principal.UserID
		go func(input decision.Input) {
			result, chat := chatShadow(context.Background(), client, input, "configured-model", 5*time.Second)
			results <- completed{input: input, result: result, chat: chat}
		}(input)
	}
	for range count {
		item := <-results
		if item.result.Event.Mode != decision.ModeShadow || item.result.Status != decision.StatusSuggested || item.result.Suggestion == nil || item.result.Suggestion.CandidateID != "bandwidth" {
			t.Fatalf("valid model response was not a suggestion: %+v", item.result)
		}
		requireNoAcceptedRules(t, item.result)
		if item.result.Event.RequestID != item.input.RequestID || item.result.Event.TraceID != item.input.TraceID ||
			item.result.Event.TenantID != item.input.Resource.TenantID || item.result.Event.CaseID != item.input.Resource.CaseID ||
			item.result.Event.ActorID != item.input.Principal.UserID {
			t.Fatalf("concurrent request identities crossed: input=%+v event=%+v", item.input, item.result.Event)
		}
		if item.chat == nil || item.chat.Choice != "bandwidth" || item.chat.ObservedModel != "observed-chat-model" || item.chat.Usage != nil {
			t.Fatalf("chat evidence lost its observed model or unknown usage: %+v", item.chat)
		}
	}
}

func TestChatShadowUnavailableResponsesRetainObservedUsage(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
		modelName  string
		wantReason string
	}{
		{
			name:       "service unavailable",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"model":"observed-503-model","usage":{"prompt_tokens":17,"completion_tokens":0},"error":{"message":"temporarily unavailable"}}`,
			modelName:  "observed-503-model", wantReason: "model_request_failed",
		},
		{
			name:       "incomplete generation",
			statusCode: http.StatusOK,
			body:       `{"model":"observed-incomplete-model","usage":{"prompt_tokens":23,"completion_tokens":0},"choices":[{"finish_reason":"length","message":{"content":"{\"choice\":\"bandwidth\"}"}}]}`,
			modelName:  "observed-incomplete-model", wantReason: decision.FailureInvalidAnswer,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			input := chatShadowTestInput(t, strings.ReplaceAll(test.name, " ", "-"))
			result, chat := chatShadow(context.Background(), model.New(model.Config{BaseURL: server.URL, ChatModel: "configured-model"}), input, "configured-model", time.Second)
			requireUnavailable(t, result)
			if result.Event.FailureReason != test.wantReason {
				t.Fatalf("failure reason = %q, want %q", result.Event.FailureReason, test.wantReason)
			}
			if chat == nil || chat.ObservedModel != test.modelName || chat.Usage == nil || chat.Usage.Source != "openai_response.usage" ||
				chat.Usage.InputTokens == nil || *chat.Usage.InputTokens == 0 ||
				chat.Usage.OutputTokens == nil || *chat.Usage.OutputTokens != 0 {
				t.Fatalf("observed failure usage or model was lost: %+v", chat)
			}
		})
	}
}

func TestChatShadowMalformedResponseIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"partial`))
	}))
	defer server.Close()

	input := chatShadowTestInput(t, "malformed")
	result, _ := chatShadow(context.Background(), model.New(model.Config{BaseURL: server.URL, ChatModel: "configured-model"}), input, "configured-model", time.Second)
	requireUnavailable(t, result)
	if result.Event.FailureReason != decision.FailureInvalidAnswer {
		t.Fatalf("failure reason = %q, want %q", result.Event.FailureReason, decision.FailureInvalidAnswer)
	}
}

func TestChatShadowTimeoutAndCancellationAreUnavailable(t *testing.T) {
	for _, test := range []struct {
		name       string
		cancel     bool
		wantReason string
	}{
		{name: "timeout", wantReason: decision.FailureTimeout},
		{name: "cancellation", cancel: true, wantReason: decision.FailureCanceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runStalledChatShadow(t, test.cancel)
			requireUnavailable(t, result)
			if result.Event.FailureReason != test.wantReason {
				t.Fatalf("failure reason = %q, want %q", result.Event.FailureReason, test.wantReason)
			}
		})
	}
}

func runStalledChatShadow(t *testing.T, cancelRequest bool) decision.Result {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := chatShadowTestInput(t, "stalled")
	resultChannel := make(chan decision.Result, 1)
	go func() {
		result, _ := chatShadow(ctx, model.New(model.Config{BaseURL: server.URL, ChatModel: "configured-model"}), input, "configured-model", 150*time.Millisecond)
		resultChannel <- result
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("model request did not reach the stalled test server")
	}
	if cancelRequest {
		cancel()
	}
	select {
	case result := <-resultChannel:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("chat shadow did not finish after timeout or cancellation")
		return decision.Result{}
	}
}

func chatShadowTestInput(t *testing.T, suffix string) decision.Input {
	t.Helper()
	body, err := os.ReadFile("../../internal/rulepack/testdata/demo-request.json")
	if err != nil {
		t.Fatalf("read demo request fixture: %v", err)
	}
	var identity struct {
		Principal access.Principal `json:"principal"`
		Resource  access.Resource  `json:"resource"`
	}
	if err := json.Unmarshal(body, &identity); err != nil {
		t.Fatalf("decode demo request identity: %v", err)
	}
	return decision.Input{
		RequestID: "request-" + suffix, DecisionID: "decision-" + suffix, RunID: "run-" + suffix,
		AttemptID: "attempt-" + suffix, TraceID: "trace-" + suffix,
		Principal: identity.Principal, Resource: identity.Resource, Capability: decision.CapabilityChoice,
		State: "合成审核摘要 " + suffix,
		AllowedCandidates: []decision.Candidate{
			{ID: "bandwidth", Description: "专线承诺带宽"},
			{ID: "monthly_sla", Description: "月度服务等级"},
			{ID: "advance_payment", Description: "预付款条件"},
			{ID: "manual_review", Description: "人工复核"},
		},
		RulePackVersion: "demo-rulepack-v1", RulePackHash: strings.Repeat("a", 64), PolicyVersion: access.PolicyVersion,
	}
}

func requireUnavailable(t *testing.T, result decision.Result) {
	t.Helper()
	if result.Status != decision.StatusUnavailable || result.Suggestion != nil || result.Event.Mode != decision.ModeShadow || result.Event.Status != decision.StatusUnavailable || result.Event.FailureReason == "" {
		t.Fatalf("failed model response produced an unsafe decision: %+v", result)
	}
	requireNoAcceptedRules(t, result)
}

func requireNoAcceptedRules(t *testing.T, result decision.Result) {
	t.Helper()
	if result.AcceptedRules == nil || len(result.AcceptedRules) != 0 {
		t.Fatalf("shadow result accepted deterministic rules: %#v", result.AcceptedRules)
	}
}
