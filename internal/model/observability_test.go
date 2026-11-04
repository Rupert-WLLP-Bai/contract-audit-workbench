// File metadata (manifest mirror):
// Purpose: Exercises the `model` package behavior through TestWarrantyObservationRetainsUsageFromFailedAndIncompleteResponses, TestWarrantyObservationIdentifiesTimeoutAndUnknownUsage; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-04
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type observationSink struct {
	mu    sync.Mutex
	calls []CallObservation
}

func (s *observationSink) StartModelCall(ctx context.Context, _ string) (context.Context, func(CallObservation)) {
	return ctx, func(observation CallObservation) {
		s.mu.Lock()
		s.calls = append(s.calls, observation)
		s.mu.Unlock()
	}
}

func (s *observationSink) snapshot() []CallObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CallObservation(nil), s.calls...)
}

func TestWarrantyObservationRetainsUsageFromFailedAndIncompleteResponses(t *testing.T) {
	usage := map[string]any{"prompt_tokens": 17, "completion_tokens": 5, "total_tokens": 22}
	cases := []struct {
		name       string
		status     int
		body       any
		wantErr    error
		wantStatus string
		wantUsage  bool
	}{
		{
			name: "successful-response-with-unknown-usage", status: http.StatusOK,
			body: map[string]any{"model": "observed-model", "choices": []any{map[string]any{
				"finish_reason": "stop", "message": map[string]any{"content": `{"relation":"compatible","disposition":"false_positive"}`},
			}}}, wantStatus: "completed",
		},
		{
			name: "503-with-partial-usage-and-malformed-choices", status: http.StatusServiceUnavailable,
			body:    map[string]any{"model": "observed-model", "usage": usage, "choices": "invalid"},
			wantErr: ErrRequest, wantStatus: "failed", wantUsage: true,
		},
		{
			name: "incomplete-generation", status: http.StatusOK,
			body: map[string]any{"model": "observed-model", "usage": usage, "choices": []any{map[string]any{
				"finish_reason": "length", "message": map[string]any{"content": `{"relation":"compatible"}`},
			}}}, wantErr: ErrUnusable, wantStatus: "failed", wantUsage: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				if err := json.NewEncoder(w).Encode(test.body); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			defer server.Close()
			sink := &observationSink{}
			ctx := WithCallObserver(context.Background(), sink)
			_, err := New(Config{BaseURL: server.URL, ChatModel: "configured-model"}).Warranty(ctx, nil)
			if test.wantErr == nil && err != nil || test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("warranty error %v, want %v", err, test.wantErr)
			}
			calls := sink.snapshot()
			if len(calls) != 1 || calls[0].Status != test.wantStatus {
				t.Fatalf("call observations %+v", calls)
			}
			if calls[0].ModelRef != "observed-model" {
				t.Fatalf("configured routing alias hid the provider-reported model: %q", calls[0].ModelRef)
			}
			if test.wantUsage {
				if calls[0].Usage == nil || calls[0].Usage.InputTokens == nil || *calls[0].Usage.InputTokens != 17 || calls[0].Usage.OutputTokens == nil || *calls[0].Usage.OutputTokens != 5 {
					t.Fatalf("failed response usage was lost: %+v", calls[0].Usage)
				}
			} else if calls[0].Usage != nil {
				t.Fatalf("unknown usage was fabricated: %+v", calls[0].Usage)
			}
			trace := calls[0].Trace
			if trace == nil || trace.RequestedModel != "configured-model" || trace.RequestBytes == 0 || trace.ResponseBytes == 0 {
				t.Fatalf("physical call trace was not recorded: %+v", trace)
			}
			if trace.Status != test.wantStatus {
				t.Fatalf("trace status %q, want %q", trace.Status, test.wantStatus)
			}
		})
	}
}

func TestWarrantyObservationIdentifiesTimeoutAndUnknownUsage(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	sink := &observationSink{}
	ctx, cancel := context.WithTimeout(WithCallObserver(context.Background(), sink), 20*time.Millisecond)
	defer cancel()
	_, err := New(Config{BaseURL: server.URL, ChatModel: "configured-model"}).Warranty(ctx, nil)
	if !errors.Is(err, ErrRequest) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout identity was lost: %v", err)
	}
	calls := sink.snapshot()
	if len(calls) != 1 || calls[0].Status != "failed" || calls[0].ErrorCode != "timeout" || calls[0].Usage != nil {
		t.Fatalf("timeout invented usage or lost status: %+v", calls)
	}
	if calls[0].Trace == nil || calls[0].Trace.RequestBytes == 0 || calls[0].Trace.ResponseBytes != 0 || calls[0].Trace.Status != "failed" {
		t.Fatalf("timeout trace was not bounded or honest: %+v", calls[0].Trace)
	}
}

func TestProviderResponseDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, body, code string }{
		{"refusal", `{"usage":{"prompt_tokens":7,"completion_tokens":2},"choices":[{"finish_reason":"stop","message":{"content":null,"refusal":"declined"}}]}`, "provider_refusal"},
		{"refusal with content", `{"choices":[{"finish_reason":"stop","message":{"content":"answer","refusal":"declined"}}]}`, "provider_refusal"},
		{"truncated empty", `{"usage":{"prompt_tokens":7,"completion_tokens":2},"choices":[{"finish_reason":"length","message":{"content":""}}]}`, "response_truncated"},
		{"filtered empty", `{"choices":[{"finish_reason":"content_filter","message":{"content":null}}]}`, "content_filtered"},
		{"unknown finish", `{"choices":[{"finish_reason":"tool_calls","message":{"content":"text"}}]}`, "invalid_response"},
		{"malformed envelope", `not-json`, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := New(Config{BaseURL: "http://model.example", ChatModel: "model"})
			client.WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, tc.body), nil }))
			sink := &observationSink{}
			_, err := client.Warranty(WithCallObserver(context.Background(), sink), nil)
			if !errors.Is(err, ErrUnusable) {
				t.Fatalf("error = %v", err)
			}
			calls := sink.snapshot()
			if len(calls) != 1 || calls[0].ErrorCode != tc.code || calls[0].Trace == nil || calls[0].Trace.ErrorCode != tc.code {
				t.Fatalf("observation = %+v, want %s", calls, tc.code)
			}
			if tc.name == "refusal" || tc.name == "truncated empty" {
				usage := calls[0].Usage
				if usage == nil || usage.InputTokens == nil || *usage.InputTokens != 7 || usage.OutputTokens == nil || *usage.OutputTokens != 2 {
					t.Fatalf("provider usage lost: %+v", usage)
				}
			}
		})
	}
}

func TestUnknownRefusalShapesPreserveContent(t *testing.T) {
	for _, refusal := range []string{`null`, `false`, `{"reason":"unknown"}`} {
		body := `{"choices":[{"finish_reason":"stop","message":{"content":"normal transcription","refusal":` + refusal + `}}]}`
		client := New(Config{BaseURL: "http://model.example", OCRModel: "model"})
		client.WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil }))
		sink := &observationSink{}
		text, err := client.Transcribe(WithCallObserver(context.Background(), sink), "image/png", []byte("image"))
		calls := sink.snapshot()
		if err != nil || text != "normal transcription" || len(calls) != 1 || calls[0].Trace == nil || calls[0].Trace.ResponseText != text {
			t.Fatalf("text %q error %v observation %+v", text, err, calls)
		}
	}
}

func TestBusinessOutputDiagnostics(t *testing.T) {
	tests := []struct{ raw, code string }{
		{`{"relation":`, "model_json_malformed"},
		{`{"relation":"compatible"} {}`, "model_json_trailing"},
		{`{"relation":42}`, "model_json_fields"},
		{`{"unknown":true}`, "model_json_fields"},
		{`{"relation":"invalid"}`, "model_output_semantic"},
	}
	for _, tt := range tests {
		t.Run(tt.code+tt.raw, func(t *testing.T) {
			_, err := ParseWarranty(tt.raw)
			var coded interface{ ExecutionErrorCode() string }
			if !errors.Is(err, ErrUnusable) || !errors.As(err, &coded) || coded.ExecutionErrorCode() != tt.code {
				t.Fatalf("error=%v code=%v", err, coded)
			}
		})
	}
	for _, tt := range []struct{ raw, code string }{
		{`{"findings":`, "model_json_malformed"}, {`{"findings":[]} true`, "model_json_trailing"},
		{`{"findings":"wrong"}`, "model_json_fields"}, {`{"findings":null}`, "model_output_semantic"},
	} {
		_, err := ParseCloudContract(tt.raw)
		var coded interface{ ExecutionErrorCode() string }
		if !errors.Is(err, ErrUnusable) || !errors.As(err, &coded) || coded.ExecutionErrorCode() != tt.code {
			t.Fatalf("%q: %v", tt.raw, err)
		}
	}
}

func TestBusinessParseFailureKeepsPhysicalCallCompleted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": `{"relation":`}}}, "usage": map[string]any{"prompt_tokens": 17, "completion_tokens": 5, "total_tokens": 22}})
	}))
	defer server.Close()
	sink := &observationSink{}
	_, err := New(Config{BaseURL: server.URL, ChatModel: "model"}).Warranty(WithCallObserver(context.Background(), sink), nil)
	var coded interface{ ExecutionErrorCode() string }
	if !errors.Is(err, ErrUnusable) || !errors.As(err, &coded) || coded.ExecutionErrorCode() != "model_json_malformed" {
		t.Fatalf("error %v", err)
	}
	calls := sink.snapshot()
	if len(calls) != 1 || calls[0].Status != "completed" || calls[0].Usage == nil {
		t.Fatalf("physical calls %+v", calls)
	}
}

func TestStructuredGatewayRejectionObservation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		gateway  bool
		content  string
		status   int
		code     string
		disabled bool
	}{
		{"gateway structured", true, `{"code":"model_structured_request_rejected","error":"safe"}`, 502, "structured_request_rejected", false},
		{"gateway ordinary", true, `{"code":"model_provider_failed"}`, 502, "request_failed", false},
		{"direct forged", false, `{"code":"model_structured_request_rejected"}`, 502, "request_failed", false},
		{"wrong status", true, `{"code":"model_structured_request_rejected"}`, 401, "request_failed", false},
		{"disabled format", true, `{"code":"model_structured_request_rejected"}`, 502, "request_failed", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.content))
			}))
			defer server.Close()
			t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "true")
			if tt.disabled {
				t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "")
			}
			t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", server.URL)
			t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", "review")
			sink := &observationSink{}
			client := New(Config{BaseURL: server.URL, Gateway: tt.gateway, APIKey: "key", ChatModel: "review"})
			ctx := WithCallObserver(context.Background(), sink)
			if tt.gateway {
				ctx = WithDelegatedScope(ctx, DelegatedScope{Kind: "execution"})
			}
			_, err := client.Warranty(ctx, nil)
			if !errors.Is(err, ErrRequest) {
				t.Fatalf("error=%v", err)
			}
			calls := sink.snapshot()
			if len(calls) != 1 || calls[0].Status != "failed" || calls[0].ErrorCode != tt.code {
				t.Fatalf("observations=%+v", calls)
			}
		})
	}
}
