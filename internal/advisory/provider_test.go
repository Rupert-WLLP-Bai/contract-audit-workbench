package advisory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"
	"contractaudit/internal/model"
)

const providerTestKey = "typesafe_test_key_01234567890123456789"

func TestDirectProviderUsesPinnedJevIdentityAndReturnsObservedModel(t *testing.T) {
	request := builtRequest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+providerTestKey {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var payload struct {
			Model     string                     `json:"model"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode provider payload: %v", err)
		}
		if payload.Model != request.Model || len(payload.Questions) != len(request.Questions) {
			t.Errorf("provider payload identity = model %q question count %d", payload.Model, len(payload.Questions))
		}
		answers := map[string]any{}
		for id := range request.Questions {
			answers[id] = map[string]any{"type": "noul", "noul": 0.82}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "answers": answers, "usage": map[string]int{"input_tokens": 17}})
	}))
	defer server.Close()

	provider, err := NewDirectProvider(jev.Config{BaseURL: server.URL, APIKey: providerTestKey, Model: request.Model})
	if err != nil {
		t.Fatalf("create direct provider: %v", err)
	}
	if provider.ProviderIdentity() != agentflow.AdvisoryProviderDirect {
		t.Fatalf("provider identity = %q", provider.ProviderIdentity())
	}
	response, err := provider.AskNouls(context.Background(), toProviderRequest(request))
	if err != nil {
		t.Fatalf("direct provider call: %v", err)
	}
	if response.Model != request.Model || len(response.Answers) != len(request.Questions) || response.Answers["candidate_0_relevance"].Noul != 0.82 {
		t.Fatalf("provider response = %+v", response)
	}
}

func TestDirectProviderRejectsUnavailableMismatchedAndInvalidCalls(t *testing.T) {
	request := builtRequest(t)
	var provider *DirectProvider
	if _, err := provider.AskNouls(context.Background(), toProviderRequest(request)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil provider error = %v", err)
	}
	if _, err := NewDirectProvider(jev.Config{APIKey: "short"}); err == nil {
		t.Fatal("invalid Jev configuration was accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"jev-1.14.0","answers":{},"usage":{}}`))
	}))
	defer server.Close()
	provider, err := NewDirectProvider(jev.Config{BaseURL: server.URL, APIKey: providerTestKey, Model: request.Model})
	if err != nil {
		t.Fatal(err)
	}
	badModel := toProviderRequest(request)
	badModel.Model = "jev-other"
	if _, err := provider.AskNouls(context.Background(), badModel); !errors.Is(err, ErrModelMismatch) {
		t.Fatalf("model mismatch error = %v", err)
	}
	badShape := toProviderRequest(request)
	badShape.Egress = agentflow.AdvisoryEgressApprovedText
	if _, err := provider.AskNouls(context.Background(), badShape); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid request shape error = %v", err)
	}
	badShape.Egress = EgressMinimized
	if _, err := provider.AskNouls(context.Background(), badShape); err == nil {
		t.Fatal("missing upstream answers were accepted")
	}
}

func TestGatewayProviderUsesModelGatewayBoundary(t *testing.T) {
	request := builtRequest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/systemone" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Contract-Service-Key") != providerTestKey ||
			r.Header.Get("X-Contract-Jev-Feature") != FeatureKnowledgeRerank ||
			r.Header.Get("X-Contract-Data-Classification") != EgressMinimized || r.Header.Get("X-Audit-Execution-Scope") == "" {
			t.Errorf("gateway boundary headers are incomplete: %+v", r.Header)
		}
		answers := map[string]any{}
		for id := range request.Questions {
			answers[id] = map[string]any{"type": "noul", "noul": 0.56}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "answers": answers, "usage": map[string]int{"output_tokens": 4}})
	}))
	defer server.Close()
	client := model.New(model.Config{BaseURL: server.URL, Gateway: true, APIKey: providerTestKey})
	provider, err := NewGatewayProvider(client)
	if err != nil {
		t.Fatal(err)
	}
	if provider.ProviderIdentity() != agentflow.AdvisoryProviderGateway {
		t.Fatalf("provider identity = %q", provider.ProviderIdentity())
	}
	ctx := model.WithDelegatedScope(context.Background(), model.DelegatedScope{
		Kind: "review", TenantID: "tenant_1", OrgID: "org_1", CaseID: "case_1", ActorID: "actor_1",
	})
	response, err := provider.AskNouls(ctx, toProviderRequest(request))
	if err != nil {
		t.Fatalf("gateway provider call: %v", err)
	}
	if response.Model != request.Model || len(response.Answers) != len(request.Questions) || response.Answers["candidate_0_relevance"].Noul != 0.56 {
		t.Fatalf("gateway response = %+v", response)
	}
}

func TestGatewayProviderRejectsMissingClientAndBadShape(t *testing.T) {
	if _, err := NewGatewayProvider(nil); err == nil {
		t.Fatal("nil gateway client was accepted")
	}
	request := builtRequest(t)
	var provider *GatewayProvider
	if _, err := provider.AskNouls(context.Background(), toProviderRequest(request)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil gateway provider error = %v", err)
	}
	provider, err := NewGatewayProvider(model.New(model.Config{Gateway: true, BaseURL: "http://127.0.0.1:1", APIKey: providerTestKey}))
	if err != nil {
		t.Fatal(err)
	}
	badShape := toProviderRequest(request)
	badShape.Feature = "unknown"
	if _, err := provider.AskNouls(context.Background(), badShape); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid gateway request shape error = %v", err)
	}
}

func toProviderRequest(request KnowledgeRequest) ProviderRequest {
	return ProviderRequest{
		Feature: request.Feature, Egress: request.Egress, Model: request.Model,
		State: request.State, Questions: request.Questions,
	}
}
