package bootstrap

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/llmendpoint"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func TestApplyStoredLLMEndpointKeepsGatewayCredentialSeparate(t *testing.T) {
	t.Setenv(llmendpoint.EncryptionKeyEnv, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("c", 32))))
	sealed, err := llmendpoint.Seal("provider-key")
	if err != nil {
		t.Fatal(err)
	}
	settings := store.LLMEndpointSettings{
		Name: "平台端点", Protocol: llmendpoint.ProtocolOpenAICompatible, BaseURL: "https://llm.example.com/v1",
		Models: []string{"review-stored", "ocr-stored"}, ReviewModel: "review-stored", OCRModel: "ocr-stored",
		SecretCiphertext: sealed, Revision: 3,
	}
	base := model.Config{
		BaseURL: "http://model-gateway", ProviderURL: "https://env.example.com/v1",
		Gateway: true, ChatModel: "env-review", OCRModel: "env-ocr", APIKey: "service-caller-token",
	}

	worker := applyStoredLLMEndpoint(base, "worker", settings)
	if worker.BaseURL != base.BaseURL || worker.APIKey != base.APIKey || !worker.Gateway {
		t.Fatalf("worker gateway identity changed: %+v", worker)
	}
	if worker.ProviderURL != settings.BaseURL || worker.ChatModel != settings.ReviewModel || worker.OCRModel != settings.OCRModel {
		t.Fatalf("worker upstream identity = %+v", worker)
	}

	direct := applyStoredLLMEndpoint(model.Config{
		BaseURL: "https://env.example.com/v1", ProviderURL: "https://env.example.com/v1",
		ChatModel: "env-review", OCRModel: "env-ocr", APIKey: "env-key",
	}, "", settings)
	if direct.BaseURL != settings.BaseURL || direct.APIKey != "provider-key" || direct.Gateway {
		t.Fatalf("single process did not adopt the stored endpoint: %+v", direct)
	}

	core := applyStoredLLMEndpoint(model.Config{
		BaseURL: "https://env.example.com/v1", ProviderURL: "https://env.example.com/v1",
		ChatModel: "env-review", OCRModel: "env-ocr", APIKey: "env-key",
	}, "core", settings)
	if core.BaseURL != settings.BaseURL || core.APIKey != "" {
		t.Fatalf("core retained a provider credential or endpoint: %+v", core)
	}

	inactive := settings
	inactive.OCRModel = ""
	if got := applyStoredLLMEndpoint(base, "worker", inactive); got != base {
		t.Fatalf("incomplete endpoint changed the deployment config: %+v", got)
	}
}

func TestLoadStoredLLMEndpointWithoutDatabaseKeepsEnvironmentIdentity(t *testing.T) {
	base := model.Config{ProviderURL: "https://env.example.com/v1", ChatModel: "env-review", OCRModel: "env-ocr"}
	got, revision, storedDirect := loadStoredLLMEndpoint(t.Context(), &store.Store{}, "worker", base)
	if got != base || revision != 0 || storedDirect {
		t.Fatalf("loaded %+v revision %d direct %t", got, revision, storedDirect)
	}
}

// A stored endpoint whose credential cannot be opened must not be paired with
// the deployment credential: the new address never received that key.
func TestApplyStoredLLMEndpointFallsBackWholeWithoutUsableCredential(t *testing.T) {
	t.Setenv(llmendpoint.EncryptionKeyEnv, "")
	sealed, err := llmendpoint.Seal("provider-key")
	if err != nil {
		// Without the environment key sealing already fails; use opaque bytes.
		sealed = []byte("sealed-with-another-key")
	}
	base := model.Config{
		BaseURL: "https://env.example.com/v1", ProviderURL: "https://env.example.com/v1",
		ChatModel: "env-review", OCRModel: "env-ocr", APIKey: "env-key",
	}
	settings := store.LLMEndpointSettings{
		Name: "平台端点", Protocol: llmendpoint.ProtocolOpenAICompatible, BaseURL: "https://llm.example.com/v1",
		Models: []string{"review-stored", "ocr-stored"}, ReviewModel: "review-stored", OCRModel: "ocr-stored",
		SecretCiphertext: sealed, Revision: 4,
	}
	if got := applyStoredLLMEndpoint(base, "", settings); got != base {
		t.Fatalf("single-process client paired a new endpoint with the deployment credential: %+v", got)
	}
}

type endpointWatchTestStore struct {
	mu       sync.Mutex
	endpoint store.LLMEndpointSettings
	database bool
	updates  chan store.Models
}

func (s *endpointWatchTestStore) LLMEndpoint(context.Context) (store.LLMEndpointSettings, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endpoint, true, nil
}

func (s *endpointWatchTestStore) UseModels(models store.Models) { s.updates <- models }
func (s *endpointWatchTestStore) HasDatabase() bool             { return s.database }

func TestWatcherAppliesRevisionChangedDuringStartup(t *testing.T) {
	first := store.LLMEndpointSettings{Protocol: llmendpoint.ProtocolOpenAICompatible, BaseURL: "https://first.example/v1", Models: []string{"chat-one", "ocr-one"}, ReviewModel: "chat-one", OCRModel: "ocr-one", Revision: 1}
	second := store.LLMEndpointSettings{Protocol: llmendpoint.ProtocolOpenAICompatible, BaseURL: "https://second.example/v1", Models: []string{"chat-two", "ocr-two"}, ReviewModel: "chat-two", OCRModel: "ocr-two", Revision: 2}
	st := &endpointWatchTestStore{endpoint: first, database: true, updates: make(chan store.Models, 2)}
	settings := runtimeSettings{modelConfig: model.Config{Gateway: true, BaseURL: "http://gateway/v1"}}
	config, revision, storedDirect := configuredLLMEndpoint(t.Context(), st, settings, "worker")
	if config.ProviderURL != first.BaseURL || revision != first.Revision || storedDirect {
		t.Fatalf("initial endpoint = %+v, revision = %d, direct = %t", config, revision, storedDirect)
	}
	// An administrator saves another revision after the client is built but
	// before the watcher starts. It must not treat that revision as applied.
	st.mu.Lock()
	st.endpoint = second
	st.mu.Unlock()
	stop, done := watchConfiguredLLMEndpoint(t.Context(), st, settings, "worker", revision)
	defer stop()
	select {
	case got := <-st.updates:
		want := model.New(applyStoredLLMEndpoint(settings.modelConfig, "worker", second))
		if got.ConfigurationID() != want.ConfigurationID() {
			t.Fatalf("watcher installed identity %q, want %q", got.ConfigurationID(), want.ConfigurationID())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watcher missed the revision saved during startup")
	}
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop after cancellation")
	}
}

func TestWatcherDisabledForReadOnlyAndNoDatabase(t *testing.T) {
	for _, tc := range []struct {
		name               string
		readOnly, database bool
	}{
		{"read-only", true, true}, {"no-database", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &endpointWatchTestStore{database: tc.database}
			stop, done := watchConfiguredLLMEndpoint(t.Context(), st, runtimeSettings{readOnly: tc.readOnly}, "worker", 1)
			if stop != nil || done != nil {
				t.Fatal("disabled watcher was started")
			}
		})
	}
}
