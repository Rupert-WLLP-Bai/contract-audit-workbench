package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"contractaudit/internal/jev"
)

const systemOneServiceKey = "worker-model-service-key-0123456789"

func systemOneQuestions() map[string]jev.NoulQuestion {
	return map[string]jev.NoulQuestion{
		"candidate_0_relevance": {
			Instructions: "Does the item directly regulate the gap capability?",
			Criteria:     jev.NoulCriteria{True: "It directly applies.", False: "It addresses another capability."},
		},
	}
}

func systemOneScope() DelegatedScope {
	return DelegatedScope{
		Kind: "execution", TenantID: "tenant-a", OrgID: "org-a", CaseID: "case-a",
		ActorID: "worker-a", JobID: "job-a", RunID: "run-a", AttemptID: "attempt-a",
		WorkerID: "worker-1", Generation: 7,
	}
}

func systemOneClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(Config{
		BaseURL: server.URL, ChatModel: "chat-model", OCRModel: "ocr-model",
		APIKey: systemOneServiceKey, Gateway: true,
	})
}

func TestSystemOneSendsOnlyGatewayScopedHeadersAndParsesAnswers(t *testing.T) {
	scope := systemOneScope()
	questions := systemOneQuestions()
	client := systemOneClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/systemone" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Contract-Service-Key"); got != systemOneServiceKey {
			t.Errorf("service key header = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("client forwarded a provider authorization header: %q", got)
		}
		if got := r.Header.Get("X-Contract-Jev-Feature"); got != "knowledge_rerank" {
			t.Errorf("feature header = %q", got)
		}
		if got := r.Header.Get("X-Contract-Data-Classification"); got != "minimized" {
			t.Errorf("classification header = %q", got)
		}
		var observedScope DelegatedScope
		if err := json.Unmarshal([]byte(r.Header.Get("X-Audit-Execution-Scope")), &observedScope); err != nil || observedScope != scope {
			t.Errorf("scope header = %+v, %v", observedScope, err)
		}
		var request struct {
			Model     string `json:"model"`
			State     string `json:"state"`
			Questions map[string]struct {
				Type         string            `json:"type"`
				Instructions string            `json:"instructions"`
				Criteria     map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != "jev-1.14.0" || request.State != `{"feature":"knowledge_rerank"}` {
			t.Errorf("request identity = %q %q", request.Model, request.State)
		}
		item, ok := request.Questions["candidate_0_relevance"]
		if !ok || item.Type != "noul" || item.Instructions != questions["candidate_0_relevance"].Instructions ||
			item.Criteria["true"] != questions["candidate_0_relevance"].Criteria.True {
			t.Errorf("question payload = %+v", request.Questions)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "jev-1.14.0",
			"answers": map[string]any{"candidate_0_relevance": map[string]any{"type": "noul", "noul": 0.82}},
			"usage":   map[string]any{"input_tokens": 120, "output_tokens": 8},
		})
	})
	sink := &observationSink{}
	ctx := WithCallObserver(WithDelegatedScope(context.Background(), scope), sink)
	answers, meta, err := client.SystemOne(ctx, SystemOneRequest{
		Model: "jev-1.14.0", State: `{"feature":"knowledge_rerank"}`, Questions: questions,
		Feature: "knowledge_rerank", Egress: "minimized",
	})
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if meta.Model != "jev-1.14.0" || meta.Usage.InputTokens == nil || *meta.Usage.InputTokens != 120 ||
		meta.Usage.OutputTokens == nil || *meta.Usage.OutputTokens != 8 {
		t.Fatalf("meta = %+v", meta)
	}
	if answer, ok := answers["candidate_0_relevance"]; !ok || answer.Noul != 0.82 || answer.Model != "jev-1.14.0" {
		t.Fatalf("answers = %+v", answers)
	}
	calls := sink.snapshot()
	if len(calls) != 1 || calls[0].Status != "completed" || calls[0].ModelRef != "jev-1.14.0" ||
		calls[0].Usage == nil || calls[0].Usage.Source != "typesafe_response.usage" ||
		calls[0].Usage.InputTokens == nil || *calls[0].Usage.InputTokens != 120 {
		t.Fatalf("observations = %+v", calls)
	}
}

func TestSystemOneRejectsInputBeforeNetwork(t *testing.T) {
	called := false
	client := systemOneClient(t, func(http.ResponseWriter, *http.Request) { called = true })
	base := WithDelegatedScope(context.Background(), systemOneScope())
	tests := []struct {
		name    string
		ctx     context.Context
		request SystemOneRequest
	}{
		{
			name: "missing scope", ctx: context.Background(),
			request: SystemOneRequest{Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "minimized"},
		},
		{
			name: "missing feature", ctx: base,
			request: SystemOneRequest{Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(), Egress: "minimized"},
		},
		{
			name: "missing model", ctx: base,
			request: SystemOneRequest{State: `{"a":1}`, Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "minimized"},
		},
		{
			name: "empty state", ctx: base,
			request: SystemOneRequest{Model: "jev-1.14.0", State: "", Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "minimized"},
		},
		{
			name: "approved text egress is not implemented", ctx: base,
			request: SystemOneRequest{Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(), Feature: "knowledge_rerank", Egress: "approved_text"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := client.SystemOne(test.ctx, test.request); !errors.Is(err, ErrRequest) && !errors.Is(err, ErrUnusable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if called {
		t.Fatal("an invalid request reached the network")
	}
}

func TestSystemOneRejectsNonGatewayClient(t *testing.T) {
	client := New(Config{BaseURL: "https://example.invalid", ChatModel: "chat-model", APIKey: "provider-key"})
	ctx := WithDelegatedScope(context.Background(), systemOneScope())
	if _, _, err := client.SystemOne(ctx, SystemOneRequest{
		Model: "jev-1.14.0", State: `{"a":1}`, Questions: systemOneQuestions(),
		Feature: "knowledge_rerank", Egress: "minimized",
	}); !errors.Is(err, ErrRequest) {
		t.Fatalf("direct client called the gateway route: %v", err)
	}
}

func TestSystemOneRejectsModelDriftAndIncompleteAnswers(t *testing.T) {
	questions := systemOneQuestions()
	tests := []struct {
		name   string
		body   map[string]any
		status int
		want   error
	}{
		{
			name: "observed model drift", status: http.StatusOK,
			body: map[string]any{
				"model":   "jev-1.15.0",
				"answers": map[string]any{"candidate_0_relevance": map[string]any{"type": "noul", "noul": 0.8}},
			},
			want: ErrUnusable,
		},
		{
			name: "missing answer", status: http.StatusOK,
			body: map[string]any{"model": "jev-1.14.0", "answers": map[string]any{}},
			want: ErrUnusable,
		},
		{
			name: "out of range", status: http.StatusOK,
			body: map[string]any{
				"model":   "jev-1.14.0",
				"answers": map[string]any{"candidate_0_relevance": map[string]any{"type": "noul", "noul": 1.2}},
			},
			want: ErrUnusable,
		},
		{
			name: "gateway failure", status: http.StatusBadGateway,
			body: map[string]any{"error": "provider failed"},
			want: ErrRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := systemOneClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_ = json.NewEncoder(w).Encode(test.body)
			})
			ctx := WithDelegatedScope(context.Background(), systemOneScope())
			_, _, err := client.SystemOne(ctx, SystemOneRequest{
				Model: "jev-1.14.0", State: `{"a":1}`, Questions: questions,
				Feature: "knowledge_rerank", Egress: "minimized",
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}
}
