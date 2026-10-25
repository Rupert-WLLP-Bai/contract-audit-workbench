package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRouteChoiceRejectsUnknownOrAmbiguousAnswers(t *testing.T) {
	criteria := map[string]string{"bandwidth": "带宽证据", "manual_review": "人工明确"}
	for _, answer := range []string{
		`{"choice":"skip_rules"}`, `{"choice":"bandwidth","relation":"compatible"}`,
		`{"choice":"bandwidth"} {"choice":"manual_review"}`, `null`,
		`{"choice":"bandwidth","choice":"manual_review"}`, `{"Choice":"bandwidth"}`,
		`{"choice":"bandwidth","Choice":"manual_review"}`, `{"choice":`, `{"choice":false}`,
	} {
		t.Run(answer, func(t *testing.T) {
			server := routingResponseServer(t, answer, nil)
			defer server.Close()
			client := New(Config{BaseURL: server.URL, ChatModel: "route-model"})
			if result, err := client.RouteChoice(context.Background(), "待核对材料", criteria, "选择核查重点"); !errors.Is(err, ErrUnusable) || result.Choice != "" {
				t.Fatalf("untrusted model answer became a route: %+v err=%v", result, err)
			}
		})
	}
}

func TestRouteChoicePreservesKnownAndUnknownUsage(t *testing.T) {
	cases := []struct {
		name  string
		usage any
		known bool
		input int64
	}{
		{"observed", map[string]any{"prompt_tokens": 120, "completion_tokens": 8, "total_tokens": 128}, true, 120},
		{"explicit-zero", map[string]any{"prompt_tokens": 0, "completion_tokens": 0}, true, 0},
		{"absent", nil, false, 0},
		{"invalid-negative", map[string]any{"prompt_tokens": -1}, false, 0},
		{"invalid-type", map[string]any{"prompt_tokens": "120"}, false, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := routingResponseServer(t, `{"choice":"manual_review"}`, test.usage)
			defer server.Close()
			result, err := New(Config{BaseURL: server.URL, ChatModel: "route-model"}).RouteChoice(context.Background(), "含糊材料", map[string]string{"bandwidth": "带宽", "manual_review": "人工"}, "选择重点")
			if err != nil || result.Choice != "manual_review" || result.ObservedModel != "observed-provider-model" {
				t.Fatalf("valid route lost: %+v err=%v", result, err)
			}
			if test.known {
				if result.Usage == nil || result.Usage.Source != "openai_response.usage" || result.Usage.InputTokens == nil || *result.Usage.InputTokens != test.input {
					t.Fatalf("observed usage lost its provenance/value: %+v", result.Usage)
				}
			} else if result.Usage != nil {
				t.Fatalf("unknown usage was invented: %+v", result.Usage)
			}
		})
	}
}

func TestRouteChoiceRejectsOversizeBeforeNetworkAndReturnsHTTPFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, ChatModel: "route-model"})
	criteria := map[string]string{"bandwidth": "带宽", "manual_review": "人工"}
	if _, err := client.RouteChoice(context.Background(), strings.Repeat("x", 32*1024+1), criteria, "选择重点"); !errors.Is(err, ErrUnusable) || calls.Load() != 0 {
		t.Fatalf("oversized state reached upstream: %v calls=%d", err, calls.Load())
	}
	result, err := client.RouteChoice(context.Background(), "待审", criteria, "选择重点")
	if !errors.Is(err, ErrRequest) || result.Choice != "" || calls.Load() != 1 {
		t.Fatalf("503 became a usable recommendation: %+v err=%v calls=%d", result, err, calls.Load())
	}
}

func TestRouteChoiceRejectsOversizedAnswer(t *testing.T) {
	server := routingResponseServer(t, strings.Repeat(" ", 1025)+`{"choice":"bandwidth"}`, nil)
	defer server.Close()
	result, err := New(Config{BaseURL: server.URL, ChatModel: "route-model"}).RouteChoice(
		context.Background(), "待核对材料", map[string]string{"bandwidth": "带宽", "manual_review": "人工"}, "选择重点")
	if !errors.Is(err, ErrUnusable) || result.Choice != "" {
		t.Fatalf("oversized answer became a route: %+v err=%v", result, err)
	}
}

func routingResponseServer(t *testing.T, answer string, usage any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := map[string]any{"model": "observed-provider-model", "choices": []any{map[string]any{
			"finish_reason": "stop", "message": map[string]any{"content": answer},
		}}}
		if usage != nil {
			body["usage"] = usage
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}
