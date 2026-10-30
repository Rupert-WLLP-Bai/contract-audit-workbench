package bootstrap

import (
	"encoding/base64"
	"strings"
	"testing"

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
	got, revision := loadStoredLLMEndpoint(t.Context(), &store.Store{}, "worker", base)
	if got != base || revision != 0 {
		t.Fatalf("loaded %+v revision %d", got, revision)
	}
}
