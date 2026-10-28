package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/decision"
	"contractaudit/internal/jev"
)

const jevTestKey = "apikey_test_000000000000000000000000000000"

func jevTestClient(t *testing.T, handler http.HandlerFunc) *jev.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := jev.New(jev.Config{BaseURL: server.URL, APIKey: jevTestKey, Model: "jev-test", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("new jev client: %v", err)
	}
	return client
}

func jevResponseBody(choice string, probabilities map[string]float64, confidence float64) string {
	body, err := json.Marshal(map[string]any{
		"model": "jev-1.14.0",
		"answers": map[string]any{
			"review_focus": map[string]any{
				"type": "choice", "choice": choice, "confidence": confidence, "probabilities": probabilities,
			},
		},
		"usage": map[string]any{"input_tokens": 128, "output_tokens": 12},
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func jevProbabilities() map[string]float64 {
	return map[string]float64{"bandwidth": 0.91, "monthly_sla": 0.05, "advance_payment": 0.02, "manual_review": 0.02}
}

func TestJevShadowSendsBoundedChoiceAndKeepsSuggestionOnly(t *testing.T) {
	client := jevTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer "+jevTestKey {
			t.Errorf("request = %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Model     string `json:"model"`
			State     string `json:"state"`
			Questions map[string]struct {
				Type     string            `json:"type"`
				Criteria map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "jev-test" || !strings.Contains(body.State, "合成审核摘要") ||
			body.Questions["review_focus"].Type != "choice" || len(body.Questions["review_focus"].Criteria) != 4 {
			t.Errorf("request body = %+v", body)
		}
		_, _ = w.Write([]byte(jevResponseBody("bandwidth", jevProbabilities(), 0.91)))
	})
	input := chatShadowTestInput(t, "jev-success")
	result, detail := jevShadow(context.Background(), client, input, time.Second)
	if result.Status != decision.StatusSuggested || result.Suggestion == nil {
		t.Fatalf("result = %+v", result)
	}
	suggestion := result.Suggestion
	if suggestion.CandidateID != "bandwidth" || suggestion.Confidence == nil || *suggestion.Confidence != 0.91 ||
		suggestion.ConfidenceSource != "answer_confidence" || suggestion.ResponseModel != "jev-1.14.0" ||
		suggestion.Probabilities["bandwidth"] != 0.91 {
		t.Fatalf("suggestion = %+v", suggestion)
	}
	if result.Event.Mode != decision.ModeShadow || result.Event.Status != decision.StatusSuggested ||
		result.Event.Recommendation != "bandwidth" || result.Event.ResponseModel != "jev-1.14.0" ||
		result.Event.Confidence == nil || *result.Event.Confidence != 0.91 ||
		result.Event.ModelName != "jev-test" || result.Event.Abstention != "not_configured" || result.Event.LatencyMS <= 0 {
		t.Fatalf("event = %+v", result.Event)
	}
	requireNoAcceptedRules(t, result)
	if detail == nil || detail.Usage.InputTokens == nil || *detail.Usage.InputTokens != 128 ||
		detail.Usage.OutputTokens == nil || *detail.Usage.OutputTokens != 12 {
		t.Fatalf("detail = %+v", detail)
	}
	encoded, _ := json.Marshal(struct {
		Result decision.Result `json:"result"`
		Detail *jev.Answer     `json:"detail"`
	}{result, detail})
	if strings.Contains(string(encoded), jevTestKey) {
		t.Fatalf("result leaked the API key: %s", encoded)
	}
}

func TestJevShadowDeniedDoesNotCallModel(t *testing.T) {
	var calls atomic.Int32
	client := jevTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(jevResponseBody("bandwidth", jevProbabilities(), 0.91)))
	})
	input := chatShadowTestInput(t, "jev-denied")
	input.Principal.Enabled = false
	result, detail := jevShadow(context.Background(), client, input, time.Second)
	if result.Status != decision.StatusDenied || result.Event.Status != decision.StatusDenied ||
		result.Event.FailureReason != decision.FailureAuthorization || detail != nil {
		t.Fatalf("denied result = %+v detail=%+v", result, detail)
	}
	requireNoAcceptedRules(t, result)
	if calls.Load() != 0 {
		t.Fatalf("denied request reached the model: calls=%d", calls.Load())
	}
}

func TestJevShadowRejectsInvalidInputBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	client := jevTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(jevResponseBody("bandwidth", jevProbabilities(), 0.91)))
	})
	cases := []struct {
		name   string
		mutate func(*decision.Input)
	}{
		{name: "oversize state", mutate: func(input *decision.Input) { input.State = strings.Repeat("x", jev.MaxStateBytes+1) }},
		{name: "missing manual review", mutate: func(input *decision.Input) {
			input.AllowedCandidates = input.AllowedCandidates[:len(input.AllowedCandidates)-1]
		}},
		{name: "wrong capability", mutate: func(input *decision.Input) { input.Capability = decision.Capability("unsupported") }},
		{name: "wrong policy", mutate: func(input *decision.Input) { input.PolicyVersion = "other-policy" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			input := chatShadowTestInput(t, "jev-invalid")
			test.mutate(&input)
			result, detail := jevShadow(context.Background(), client, input, time.Second)
			if result.Status != decision.StatusInvalidInput || result.Event.Status != decision.StatusInvalidInput ||
				result.Event.FailureReason != decision.FailureInvalidInput || detail != nil {
				t.Fatalf("invalid result = %+v detail=%+v", result, detail)
			}
			requireNoAcceptedRules(t, result)
		})
	}
	result, _ := jevShadow(context.Background(), nil, chatShadowTestInput(t, "jev-no-client"), time.Second)
	if result.Status != decision.StatusInvalidInput || result.Event.FailureReason != decision.FailureInvalidInput {
		t.Fatalf("nil client result = %+v", result)
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid input reached the model: calls=%d", calls.Load())
	}
}

func TestJevShadowMapsUpstreamFailures(t *testing.T) {
	valid := jevResponseBody("bandwidth", jevProbabilities(), 0.91)
	notAllowed := jevResponseBody("other", jevProbabilities(), 0.91)
	badSum := jevResponseBody("bandwidth", map[string]float64{"bandwidth": 0.2, "monthly_sla": 0.1, "advance_payment": 0.1, "manual_review": 0.1}, 0.2)
	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantReason string
	}{
		{name: "upstream status", status: http.StatusBadGateway, body: "{}", wantReason: decision.FailureHTTPStatus},
		{name: "redirect", status: http.StatusFound, body: "", wantReason: decision.FailureRedirect},
		{name: "malformed json", status: http.StatusOK, body: "{", wantReason: decision.FailureInvalidJSON},
		{name: "invalid answer", status: http.StatusOK, body: notAllowed, wantReason: decision.FailureInvalidAnswer},
		{name: "invalid probabilities", status: http.StatusOK, body: badSum, wantReason: decision.FailureInvalidProbabilities},
		{name: "oversize response", status: http.StatusOK, body: strings.Repeat("x", jev.MaxResponseBytes+1), wantReason: decision.FailureOversizeResponse},
		{name: "valid", status: http.StatusOK, body: valid, wantReason: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := jevTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			result, detail := jevShadow(context.Background(), client, chatShadowTestInput(t, "jev-failure"), time.Second)
			if test.wantReason == "" {
				if result.Status != decision.StatusSuggested || detail == nil {
					t.Fatalf("valid result = %+v detail=%+v", result, detail)
				}
				return
			}
			requireUnavailable(t, result)
			if result.Event.FailureReason != test.wantReason || detail != nil {
				t.Fatalf("failure result = %+v detail=%+v", result, detail)
			}
		})
	}
}

func TestJevShadowMapsTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		defer close(release)
		client := jevTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		})
		resultChannel := make(chan decision.Result, 1)
		go func() {
			result, _ := jevShadow(context.Background(), client, chatShadowTestInput(t, "jev-timeout"), 50*time.Millisecond)
			resultChannel <- result
		}()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("request did not reach the test server")
		}
		select {
		case result := <-resultChannel:
			requireUnavailable(t, result)
			if result.Event.FailureReason != decision.FailureTimeout {
				t.Fatalf("failure reason = %q, want %q", result.Event.FailureReason, decision.FailureTimeout)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout did not return")
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		client := jevTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(jevResponseBody("bandwidth", jevProbabilities(), 0.91)))
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, _ := jevShadow(ctx, client, chatShadowTestInput(t, "jev-canceled"), time.Second)
		requireUnavailable(t, result)
		if result.Event.FailureReason != decision.FailureCanceled {
			t.Fatalf("failure reason = %q, want %q", result.Event.FailureReason, decision.FailureCanceled)
		}
	})
}
