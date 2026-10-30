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

func TestMatchesFrozenChecksModelAndProviderIdentity(t *testing.T) {
	upstream := upstreamConfig{ProviderURL: "https://provider.example/v1", ChatModel: "review-v2", OCRModel: "ocr-v2"}
	currentID := model.ConfigurationIDFor(upstream.ProviderURL, upstream.ChatModel, upstream.OCRModel)
	tests := []struct {
		name  string
		scope model.DelegatedScope
		want  bool
	}{
		{"current configuration", model.DelegatedScope{ConfigurationID: currentID, ProviderURL: upstream.ProviderURL}, true},
		{"changed review model at same address", model.DelegatedScope{ConfigurationID: model.ConfigurationIDFor(upstream.ProviderURL, "review-v1", upstream.OCRModel), ProviderURL: upstream.ProviderURL}, false},
		{"changed OCR model at same address", model.DelegatedScope{ConfigurationID: model.ConfigurationIDFor(upstream.ProviderURL, upstream.ChatModel, "ocr-v1"), ProviderURL: upstream.ProviderURL}, false},
		{"forged address with current identity", model.DelegatedScope{ConfigurationID: currentID, ProviderURL: "https://other.example/v1"}, false},
		{"identity without address", model.DelegatedScope{ConfigurationID: currentID}, true},
		{"missing identity retains compatibility", model.DelegatedScope{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := upstream.matchesFrozen(tc.scope); got != tc.want {
				t.Fatalf("matchesFrozen() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGatewayRejectsSameProviderChangedModelConfiguration(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"answer"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()
	config := gatewayTestConfig("openai-compatible", provider.URL+"/v1")
	_, gateway := startGatewayServer(t, st, config)
	caseID, docs := createGatewayCase(t, st, admin, "same provider changed model")
	client := &http.Client{Timeout: 5 * time.Second}
	scope := frozenDocumentScope(admin, caseID, docs[0], model.ConfigurationIDFor(config.UpstreamBaseURL, "previous-review-model", config.OCRModel))
	scope.ProviderURL = config.UpstreamBaseURL
	request := func() (*http.Response, []byte, error) {
		return gatewayChatRequest(context.Background(), client, gateway.URL+"/v1/chat/completions", []string{config.DocumentCredential}, encodedGatewayScope(t, scope), config.OCRModel, 8)
	}
	response, body, err := request()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict || gatewayErrorCode(t, body) != "model_configuration_changed" || calls.Load() != 0 {
		t.Fatalf("changed model: status=%d body=%s provider calls=%d", response.StatusCode, body, calls.Load())
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 1 || usage[0].ErrorCode != "model_configuration_changed" || !usage[0].BudgetReleased || usage[0].ChargedTokens != 0 {
		t.Fatalf("changed model did not release its budget: %+v", usage)
	}
	assertNoActiveGatewayLeases(t, active)

	scope.ConfigurationID = model.ConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)
	response, body, err = request()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("matching configuration: status=%d body=%s provider calls=%d", response.StatusCode, body, calls.Load())
	}
}
