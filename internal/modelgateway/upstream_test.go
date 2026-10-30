package modelgateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/llmendpoint"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func TestUpstreamAllowsOnlySelectedRoleModels(t *testing.T) {
	upstream := upstreamConfig{ChatModel: "review-a", OCRModel: "ocr-b"}
	for _, requested := range []string{"review-a", "ocr-b", "panda-local/review-a", " panda-local/ocr-b "} {
		if !upstream.allows(requested) {
			t.Fatalf("%q was rejected", requested)
		}
	}
	for _, requested := range []string{"", "env-review", "review-a/other"} {
		if upstream.allows(requested) {
			t.Fatalf("%q was allowed", requested)
		}
	}
}

func TestUpstreamForUsesStaticConfigWithoutDatabase(t *testing.T) {
	service := &Service{config: Config{
		Mode: "openai-compatible", UpstreamBaseURL: "https://env.example.com/v1",
		UpstreamAPIKey: "env-key", ChatModel: "env-review", OCRModel: "env-ocr",
	}}
	upstream, err := service.upstreamFor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if upstream.BaseURL != "https://env.example.com/v1" || upstream.APIKey != "env-key" ||
		upstream.ChatModel != "env-review" || upstream.OCRModel != "env-ocr" || upstream.Revision != 0 {
		t.Fatalf("static upstream = %+v", upstream)
	}
}

func TestStoredEndpointDrivesGatewayCatalogAndDispatch(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("g", 32)))
	t.Setenv(llmendpoint.EncryptionKeyEnv, key)
	const providerKey = "{{SECRET_tuyqeziy}}"
	sealed, err := llmendpoint.Seal(providerKey)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := st.SaveLLMEndpoint(context.Background(), admin, store.LLMEndpointInput{
		Name: "平台端点", Protocol: llmendpoint.ProtocolOpenAICompatible, BaseURL: "https://stored.example.com/v1",
		Models: []string{"stored-review", "stored-ocr"}, ReviewModel: "stored-review", OCRModel: "stored-ocr",
		SecretCiphertext: sealed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Revision != 1 {
		t.Fatalf("saved revision = %d", settings.Revision)
	}

	var mu sync.Mutex
	var authorization, chatModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorization = r.Header.Get("Authorization")
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+providerKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
				{"id": "stored-review"}, {"id": "stored-ocr"}, {"id": "unused-model"},
			}})
		case "/chat/completions":
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			chatModel = body.Model
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": body.Model,
				"usage": map[string]int{"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	_, err = st.SaveLLMEndpoint(context.Background(), admin, store.LLMEndpointInput{
		Name: "平台端点", Protocol: llmendpoint.ProtocolOpenAICompatible, BaseURL: upstream.URL,
		Models: []string{"stored-review", "stored-ocr"}, ReviewModel: "stored-review", OCRModel: "stored-ocr",
		PreserveSecret: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var staticCalls int
	staticUpstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		staticCalls++
	}))
	defer staticUpstream.Close()
	config := gatewayTestConfig("openai-compatible", staticUpstream.URL)
	config.ChatModel = "env-review"
	config.OCRModel = "env-ocr"
	config.UpstreamAPIKey = "env-key"
	service, _ := startGatewayServer(t, st, config)

	resolved, err := service.upstreamFor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.BaseURL != upstream.URL || resolved.ChatModel != "stored-review" ||
		resolved.OCRModel != "stored-ocr" || resolved.APIKey != providerKey || resolved.Revision != 2 {
		t.Fatalf("resolved upstream = %+v", resolved)
	}
	ids, err := service.fetchCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "stored-review,stored-ocr,unused-model" {
		t.Fatalf("catalog = %v", ids)
	}
	response, status, usage, requestErr, _, _ := service.dispatch(context.Background(), "stored-review", []byte(`{"model":"stored-review","max_tokens":8}`))
	if requestErr != nil || status != http.StatusOK || usage == nil || usage.TotalTokens == nil || *usage.TotalTokens != 6 {
		t.Fatalf("dispatch status=%d usage=%+v err=%v body=%s", status, usage, requestErr, response)
	}
	mu.Lock()
	if authorization != "Bearer "+providerKey || chatModel != "stored-review" {
		t.Fatalf("upstream saw authorization %q model %q", authorization, chatModel)
	}
	mu.Unlock()
	if !resolved.allows("stored-review") || resolved.allows("env-review") {
		t.Fatal("stored endpoint did not replace the environment model allow-list")
	}
	if staticCalls != 0 {
		t.Fatalf("static environment upstream received %d calls", staticCalls)
	}

	// An endpoint saved without role selections is registered but does not
	// replace the deployment identity.
	if _, err := st.SaveLLMEndpoint(context.Background(), admin, store.LLMEndpointInput{
		Name: "不完整端点", Protocol: llmendpoint.ProtocolOpenAICompatible, BaseURL: upstream.URL,
		Models: []string{"stored-review", "stored-ocr"}, PreserveSecret: true,
	}); err != nil {
		t.Fatal(err)
	}
	service.upstreamMu.Lock()
	service.upstreamAt = time.Time{}
	service.upstreamMu.Unlock()
	inactive, err := service.upstreamFor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inactive.ChatModel != "env-review" || inactive.BaseURL != staticUpstream.URL {
		t.Fatalf("incomplete endpoint was treated as active: %+v", inactive)
	}

	// A stored credential that this process cannot decrypt is an error rather
	// than a silent route change.
	t.Setenv(llmendpoint.EncryptionKeyEnv, "")
	service.upstreamMu.Lock()
	service.upstreamAt = time.Time{}
	service.upstreamMu.Unlock()
	if _, err := st.SaveLLMEndpoint(context.Background(), admin, store.LLMEndpointInput{
		Name: "平台端点", Protocol: llmendpoint.ProtocolOpenAICompatible, BaseURL: upstream.URL,
		Models: []string{"stored-review", "stored-ocr"}, ReviewModel: "stored-review", OCRModel: "stored-ocr",
		PreserveSecret: true,
	}); err != nil {
		t.Fatal(err)
	}
	service.upstreamMu.Lock()
	service.upstreamAt = time.Time{}
	service.upstreamMu.Unlock()
	if _, err := service.upstreamFor(context.Background()); err == nil {
		t.Fatal("gateway silently fell back after losing the credential key")
	}
}

func TestStoredEndpointModelIDsMatchGatewayIDs(t *testing.T) {
	if model.GatewayID("panda-local/stored-review") != "stored-review" {
		t.Fatal("gateway id normalization changed")
	}
}
