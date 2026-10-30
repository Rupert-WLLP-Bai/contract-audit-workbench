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
