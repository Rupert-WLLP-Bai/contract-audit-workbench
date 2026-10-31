package httpapi_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"contractaudit/internal/llmendpoint"
)

func loginAdminAudience(t *testing.T, api *testAPI) {
	t.Helper()
	status, body := api.do(t, http.MethodPost, "/api/session", map[string]any{
		"username": "demo-admin", "password": "admin-demo", "audience": "admin",
	})
	if status != http.StatusOK {
		t.Fatalf("admin audience login: %d %v", status, body)
	}
}

func TestLLMEndpointAdminConfigurationKeepsTheCredentialServerSide(t *testing.T) {
	const providerKey = "{{SECRET_4vaahdme}}"
	t.Setenv("LLM_CONFIG_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("h", 32))))
	// The httptest providers are loopback addresses; the deployment allowlist
	// is the sanctioned way a private target stays reachable.
	t.Setenv(llmendpoint.AllowedHostsEnv, "127.0.0.1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+providerKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"id": "review-a"}, {"id": "ocr-b"},
		}})
	}))
	defer upstream.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "stored credential leaked to another endpoint", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "other-model"}}})
	}))
	defer other.Close()

	api := newAPI(t)
	if status, _ := api.do(t, http.MethodGet, "/api/admin/llm-endpoint", nil); status != http.StatusUnauthorized {
		t.Fatalf("anonymous endpoint read = %d", status)
	}
	if status, _ := api.do(t, http.MethodPost, "/api/session", map[string]any{
		"username": "demo-reviewer", "password": "reviewer-demo", "audience": "admin",
	}); status != http.StatusForbidden {
		t.Fatalf("non-admin admin-audience login = %d", status)
	}
	loginAdminAudience(t, api)

	status, empty := api.do(t, http.MethodGet, "/api/admin/llm-endpoint", nil)
	if status != http.StatusOK || empty["configured"] != false || empty["protocol"] != "openai-compatible" {
		t.Fatalf("empty endpoint view: %d %v", status, empty)
	}

	status, probed := api.do(t, http.MethodPost, "/api/admin/llm-endpoint/probe", map[string]any{
		"protocol": "openai-compatible", "base_url": upstream.URL, "api_key": providerKey,
	})
	if status != http.StatusOK || len(probed["models"].([]any)) != 2 {
		t.Fatalf("probe: %d %v", status, probed)
	}

	status, saved := api.do(t, http.MethodPut, "/api/admin/llm-endpoint", map[string]any{
		"name": "生产网关", "protocol": "openai-compatible", "base_url": upstream.URL,
		"models": []string{"review-a", "ocr-b"}, "api_key": providerKey,
	})
	if status != http.StatusOK || saved["configured"] != true || saved["has_api_key"] != true ||
		saved["review_model"] != "" || saved["ocr_model"] != "" || saved["revision"] != float64(1) {
		t.Fatalf("saved endpoint: %d %v", status, saved)
	}
	if _, exists := saved["api_key"]; exists {
		t.Fatal("endpoint response included an api_key field")
	}
	status, raw := api.bytes(t, http.MethodGet, "/api/admin/llm-endpoint", nil, "")
	if status != http.StatusOK || strings.Contains(string(raw), providerKey) ||
		strings.Contains(string(raw), "secret_ciphertext") {
		t.Fatalf("endpoint response leaked the credential: %s", raw)
	}

	status, selected := api.do(t, http.MethodPut, "/api/admin/llm-endpoint/models", map[string]any{
		"review_model": "review-a", "ocr_model": "ocr-b", "expected_revision": 1,
	})
	if status != http.StatusOK || selected["review_model"] != "review-a" ||
		selected["ocr_model"] != "ocr-b" || selected["revision"] != float64(2) {
		t.Fatalf("model selection: %d %v", status, selected)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/llm-endpoint/models", map[string]any{
		"review_model": "review-a", "ocr_model": "missing", "expected_revision": 2,
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("selection outside catalog = %d", status)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/llm-endpoint/models", map[string]any{
		"review_model": "ocr-b", "ocr_model": "review-a", "expected_revision": 1,
	}); status != http.StatusConflict {
		t.Fatalf("stale endpoint revision = %d", status)
	}

	status, settings := api.do(t, http.MethodGet, "/api/admin/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("settings read = %d %v", status, settings)
	}
	modelView := settings["model"].(map[string]any)
	if modelView["status"] != "connected" ||
		!strings.Contains(modelView["label"].(string), "review-a") ||
		!strings.Contains(modelView["label"].(string), "ocr-b") {
		t.Fatalf("settings did not probe the stored endpoint: %v", modelView)
	}

	// A blank probe key reuses the stored credential, and a save without a key
	// preserves it.
	if status, body := api.do(t, http.MethodPost, "/api/admin/llm-endpoint/probe", map[string]any{
		"protocol": "openai-compatible", "base_url": upstream.URL,
	}); status != http.StatusOK {
		t.Fatalf("stored-key probe: %d %v", status, body)
	}
	if status, body := api.do(t, http.MethodPost, "/api/admin/llm-endpoint/probe", map[string]any{
		"protocol": "openai-compatible", "base_url": other.URL,
	}); status != http.StatusOK {
		t.Fatalf("changed endpoint probe did not stay credential-free: %d %v", status, body)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/llm-endpoint", map[string]any{
		"name": "生产网关", "protocol": "openai-compatible", "base_url": other.URL,
		"models": []string{"review-a", "ocr-b"}, "expected_revision": 2,
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("credential moved to another endpoint without confirmation = %d", status)
	}
	if status, body := api.do(t, http.MethodPost, "/api/admin/llm-endpoint/probe", map[string]any{
		"protocol": "openai-compatible", "base_url": upstream.URL, "clear_api_key": true,
	}); status != http.StatusBadGateway {
		t.Fatalf("cleared credential was still used for probe: %d %v", status, body)
	}
	status, preserved := api.do(t, http.MethodPut, "/api/admin/llm-endpoint", map[string]any{
		"name": "生产网关", "protocol": "openai-compatible", "base_url": upstream.URL,
		"models": []string{"review-a", "ocr-b"}, "expected_revision": 2,
	})
	if status != http.StatusOK || preserved["has_api_key"] != true || preserved["revision"] != float64(3) {
		t.Fatalf("credential was not preserved: %d %v", status, preserved)
	}

	status, cleared := api.do(t, http.MethodPut, "/api/admin/llm-endpoint", map[string]any{
		"name": "生产网关", "protocol": "openai-compatible", "base_url": upstream.URL,
		"models": []string{"review-a", "ocr-b"}, "clear_api_key": true, "expected_revision": 3,
	})
	if status != http.StatusOK || cleared["has_api_key"] != false {
		t.Fatalf("credential was not cleared: %d %v", status, cleared)
	}
	if status, _ := api.do(t, http.MethodPost, "/api/admin/llm-endpoint/probe", map[string]any{
		"protocol": "openai-compatible", "base_url": upstream.URL,
	}); status != http.StatusBadGateway {
		t.Fatalf("probe without a credential reached the upstream: %d", status)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/llm-endpoint", map[string]any{
		"name": "生产网关", "protocol": "anthropic", "base_url": upstream.URL,
		"models": []string{"review-a", "ocr-b"},
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported protocol = %d", status)
	}
}
