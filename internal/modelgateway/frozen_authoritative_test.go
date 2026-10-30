package modelgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/model"
)

type frozenBarrier struct {
	*gatewayBarrierModels
	identity string
}

func (m *frozenBarrier) ConfigurationID() string { return m.identity }

func TestChatUsesServerFrozenIdentityDespiteOmittedOrForgedScope(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()
	config := gatewayTestConfig("openai-compatible", provider.URL+"/v1")
	_, gateway := startGatewayServer(t, st, config)
	release := make(chan struct{})
	models := &frozenBarrier{gatewayBarrierModels: &gatewayBarrierModels{entered: make(chan model.DelegatedScope, 1), release: release}, identity: model.ConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)}
	st.UseModels(models)
	caseID, _ := createGatewayCase(t, st, admin, "authoritative frozen model")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_, err := st.StartReviewAsync(ctx, admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(ctx, "frozen-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = st.RunClaimedJob(ctx, claim) }()
	defer func() { close(release); <-done }()
	var scope model.DelegatedScope
	select {
	case scope = <-models.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	client := &http.Client{Timeout: 5 * time.Second}
	request := func(scope model.DelegatedScope) (int, []byte) {
		t.Helper()
		response, body, err := gatewayChatRequest(ctx, client, gateway.URL+"/v1/chat/completions", []string{config.WorkerCredential}, encodedGatewayScope(t, scope), config.ChatModel, 8)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}
	scope.ConfigurationID = ""
	scope.ProviderURL = ""
	status, body := request(scope)
	if status != http.StatusOK {
		t.Fatalf("correct frozen provider with omitted declaration: %d %s", status, body)
	}
	// Change the server-selected upstream without altering the frozen snapshot.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer other.Close()
	otherConfig := gatewayTestConfig("openai-compatible", other.URL+"/v1")
	_, otherGateway := startGatewayServer(t, st, otherConfig)
	for _, declared := range []string{"", model.ConfigurationIDFor(otherConfig.UpstreamBaseURL, otherConfig.ChatModel, otherConfig.OCRModel)} {
		scope.ConfigurationID = declared
		response, body, err := gatewayChatRequest(ctx, client, otherGateway.URL+"/v1/chat/completions", []string{otherConfig.WorkerCredential}, encodedGatewayScope(t, scope), otherConfig.ChatModel, 8)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusConflict || gatewayErrorCode(t, body) != "model_configuration_changed" {
			t.Fatalf("forged declaration %q: %d %s", declared, response.StatusCode, body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("wrong provider dispatched; calls=%d", calls.Load())
	}
	scope.CaseID = "foreign-case"
	response, body, err := gatewayChatRequest(ctx, client, otherGateway.URL+"/v1/chat/completions", []string{otherConfig.WorkerCredential}, encodedGatewayScope(t, scope), otherConfig.ChatModel, 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthorized request: %d %s", response.StatusCode, body)
	}
}
