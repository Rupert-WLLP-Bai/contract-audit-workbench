package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/jev"
)

// SystemOne keeps the same failure taxonomy for every provider outcome and
// reports the matching observation error code. The response is never turned
// into answers when it is unreadable, oversized, invalid, or model-drifted.
func TestSystemOneMapsFailureModesAndObservationCodes(t *testing.T) {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		ctx      func(context.Context) context.Context
		wantErr  error
		wantCode string
	}{
		{
			name: "unreadable response body",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Errorf("hijacking is not supported by the test server")
					return
				}
				connection, buffered, err := hijacker.Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				defer connection.Close()
				_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 128\r\n\r\n{")
				_ = buffered.Flush()
			},
			wantErr: ErrRequest, wantCode: "request_failed",
		},
		{
			name: "oversized response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(strings.Repeat(" ", maxSystemOneResponseBytes+1)))
			},
			wantErr: ErrUnusable, wantCode: "invalid_response",
		},
		{
			name: "invalid response document",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("not json"))
			},
			wantErr: ErrUnusable, wantCode: "invalid_response",
		},
		{
			name: "observed model drift",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				writeJSONResponse(t, w, http.StatusOK, map[string]any{
					"model": "jev-9.9.9",
					"answers": map[string]any{
						"candidate_0_relevance": map[string]any{"type": "noul", "noul": 0.8},
					},
				})
			},
			wantErr: ErrUnusable, wantCode: "invalid_response",
		},
		{
			name: "gateway failure",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				writeJSONResponse(t, w, http.StatusBadGateway, map[string]any{"error": "provider failed"})
			},
			wantErr: ErrRequest, wantCode: "request_failed",
		},
		{
			name: "cancelled request",
			handler: func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			},
			ctx: func(parent context.Context) context.Context {
				ctx, cancel := context.WithCancel(parent)
				cancel()
				return ctx
			},
			wantErr: ErrRequest, wantCode: "cancelled",
		},
		{
			name: "timed out request",
			handler: func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			},
			ctx: func(parent context.Context) context.Context {
				ctx, cancel := context.WithTimeout(parent, 20*time.Millisecond)
				t.Cleanup(cancel)
				return ctx
			},
			wantErr: ErrRequest, wantCode: "timeout",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := systemOneClient(t, test.handler)
			sink := &observationSink{}
			ctx := WithCallObserver(WithDelegatedScope(context.Background(), systemOneScope()), sink)
			if test.ctx != nil {
				ctx = WithCallObserver(test.ctx(WithDelegatedScope(context.Background(), systemOneScope())), sink)
			}
			_, _, err := client.SystemOne(ctx, SystemOneRequest{
				Model: "jev-1.14.0", State: `{"feature":"knowledge_rerank"}`, Questions: systemOneQuestions(),
				Feature: "knowledge_rerank", Egress: "minimized",
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error %v, want %v", err, test.wantErr)
			}
			calls := sink.snapshot()
			if len(calls) != 1 || calls[0].Status != "failed" || calls[0].ErrorCode != test.wantCode {
				t.Fatalf("observations %+v, want code %q", calls, test.wantCode)
			}
		})
	}
}

func TestSystemOneRejectsOversizedEncodedRequest(t *testing.T) {
	called := false
	client := systemOneClient(t, func(http.ResponseWriter, *http.Request) { called = true })
	questions := make(map[string]jev.NoulQuestion, 16)
	for i := 0; i < 16; i++ {
		questions[fmt.Sprintf("candidate_%d_relevance", i)] = jev.NoulQuestion{
			Instructions: strings.Repeat("i", 2048),
			Criteria:     jev.NoulCriteria{True: strings.Repeat("t", 512), False: strings.Repeat("f", 512)},
		}
	}
	ctx := WithDelegatedScope(context.Background(), systemOneScope())
	_, _, err := client.SystemOne(ctx, SystemOneRequest{
		Model: "jev-1.14.0", State: strings.Repeat("x", 32*1024), Questions: questions,
		Feature: "knowledge_rerank", Egress: "minimized",
	})
	if !errors.Is(err, ErrUnusable) || called {
		t.Fatalf("oversized request: err=%v network=%v", err, called)
	}
}

func TestSystemOneRejectsInvalidNoulInputBeforeNetwork(t *testing.T) {
	var called bool
	client := systemOneClient(t, func(http.ResponseWriter, *http.Request) { called = true })
	ctx := WithDelegatedScope(context.Background(), systemOneScope())
	tests := []struct {
		name    string
		request SystemOneRequest
	}{
		{"empty state", SystemOneRequest{Model: "jev-1.14.0", Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "minimized"}},
		{"empty questions", SystemOneRequest{Model: "jev-1.14.0", State: `{"a":1}`, Feature: "knowledge_rerank", Egress: "minimized"}},
		{"invalid question criteria", SystemOneRequest{
			Model: "jev-1.14.0", State: `{"a":1}`, Feature: "knowledge_rerank", Egress: "minimized",
			Questions: map[string]jev.NoulQuestion{"candidate_0_relevance": {Instructions: "x", Criteria: jev.NoulCriteria{True: "", False: ""}}},
		}},
		{"missing model", SystemOneRequest{State: `{"a":1}`, Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "minimized"}},
		{"missing feature", SystemOneRequest{Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(), Egress: "minimized"}},
		{"missing classification", SystemOneRequest{Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(), Feature: "knowledge_rerank"}},
		{"approved text egress", SystemOneRequest{
			Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "approved_text",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := client.SystemOne(ctx, test.request); !errors.Is(err, ErrUnusable) && !errors.Is(err, ErrRequest) {
				t.Fatalf("invalid System One request accepted: %v", err)
			}
		})
	}
	if called {
		t.Fatal("an invalid System One request reached the network")
	}
}

func TestSystemOneTrimsThePinnedModelAndReportsCompletion(t *testing.T) {
	var observed string
	client := systemOneClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readRequestJSON(t, r)
		observed, _ = body["model"].(string)
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"model":   "jev-1.14.0",
			"answers": map[string]any{"candidate_0_relevance": map[string]any{"type": "noul", "noul": 0.82}},
			"usage":   map[string]any{"input_tokens": 5, "output_tokens": 3},
		})
	})
	sink := &observationSink{}
	ctx := WithCallObserver(WithDelegatedScope(context.Background(), systemOneScope()), sink)
	answers, meta, err := client.SystemOne(ctx, SystemOneRequest{
		Model: " jev-1.14.0 ", State: `{"feature":"knowledge_rerank"}`, Questions: systemOneQuestions(),
		Feature: "knowledge_rerank", Egress: "minimized",
	})
	if err != nil || meta.Model != "jev-1.14.0" || observed != "jev-1.14.0" || len(answers) != 1 {
		t.Fatalf("System One completion answers=%+v meta=%+v model=%q err=%v", answers, meta, observed, err)
	}
	calls := sink.snapshot()
	if len(calls) != 1 || calls[0].Status != "completed" || calls[0].ModelRef != "jev-1.14.0" ||
		calls[0].Usage == nil || calls[0].Usage.TotalTokens == nil || *calls[0].Usage.TotalTokens != 8 {
		t.Fatalf("completion observation %+v", calls)
	}
}

func TestSystemOneRejectsDirectClientBeforeNetwork(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, ChatModel: "chat", APIKey: "provider-key"})
	ctx := WithDelegatedScope(context.Background(), systemOneScope())
	_, _, err := client.SystemOne(ctx, SystemOneRequest{
		Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "minimized",
	})
	if !errors.Is(err, ErrRequest) || calls != 0 {
		t.Fatalf("direct client called the gateway route: err=%v calls=%d", err, calls)
	}
}

func TestSystemOneBuildsTheBoundaryHeadersFromTheRequest(t *testing.T) {
	client := systemOneClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Contract-Jev-Feature"); got != "knowledge_rerank" {
			t.Errorf("feature header %q", got)
		}
		if got := r.Header.Get("X-Contract-Data-Classification"); got != "minimized" {
			t.Errorf("classification header %q", got)
		}
		if got := r.Header.Get("X-Contract-Service-Key"); got != systemOneServiceKey {
			t.Errorf("service key header %q", got)
		}
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"model":   "jev-1.14.0",
			"answers": map[string]any{"candidate_0_relevance": map[string]any{"type": "noul", "noul": 0.5}},
		})
	})
	ctx := WithDelegatedScope(context.Background(), systemOneScope())
	if _, _, err := client.SystemOne(ctx, SystemOneRequest{
		Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "minimized",
	}); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
}

func TestSystemOneResponseWithoutUsageLeavesCountersUnknown(t *testing.T) {
	client := systemOneClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"model":   "jev-1.14.0",
			"answers": map[string]any{"candidate_0_relevance": map[string]any{"type": "noul", "noul": 0.5}},
		})
	})
	ctx := WithDelegatedScope(context.Background(), systemOneScope())
	_, meta, err := client.SystemOne(ctx, SystemOneRequest{
		Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "minimized",
	})
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if meta.Usage.InputTokens != nil || meta.Usage.OutputTokens != nil {
		t.Fatalf("usage counters were invented: %+v", meta.Usage)
	}
	if got := tokenUsageFromJev(meta.Usage); got != nil {
		t.Fatalf("partial usage was promoted: %+v", got)
	}
	onlyInput := int64(4)
	got := tokenUsageFromJev(jev.Usage{InputTokens: &onlyInput})
	if got == nil || got.TotalTokens != nil || got.Source != "typesafe_response.usage" {
		t.Fatalf("partial usage mapping %+v", got)
	}
}
