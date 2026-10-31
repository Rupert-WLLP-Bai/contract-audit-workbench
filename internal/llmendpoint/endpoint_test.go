package llmendpoint

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeKeepsRoleSelectionsOptionalAndValidatesMembership(t *testing.T) {
	config, err := Normalize(" 生产网关 ", "", "https://llm.example.com/v1/", []string{"review", "ocr", "review"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if config.Name != "生产网关" || config.Protocol != ProtocolOpenAICompatible ||
		config.BaseURL != "https://llm.example.com/v1" || len(config.Models) != 2 {
		t.Fatalf("normalized config = %+v", config)
	}
	if config.Active() {
		t.Fatal("an endpoint without both role selections is active")
	}
	config, err = Normalize("网关", ProtocolOpenAICompatible, "https://llm.example.com/v1", []string{"review", "ocr"}, "review", "ocr")
	if err != nil || !config.Active() {
		t.Fatalf("complete config: %+v %v", config, err)
	}
	for _, tc := range []struct {
		name     string
		protocol string
		baseURL  string
		models   []string
		review   string
		ocr      string
	}{
		{"missing-name", "", "https://llm.example.com", []string{"m"}, "", ""},
		{"unsupported-protocol", "anthropic", "https://llm.example.com", []string{"m"}, "", ""},
		{"userinfo", "", "https://user@llm.example.com", []string{"m"}, "", ""},
		{"query", "", "https://llm.example.com/v1?key=leak", []string{"m"}, "", ""},
		{"empty-models", "", "https://llm.example.com", nil, "", ""},
		{"role-outside-list", "", "https://llm.example.com", []string{"m"}, "other", ""},
		{"model-with-space", "", "https://llm.example.com", []string{"two words"}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "网关"
			if tc.name == "missing-name" {
				name = " "
			}
			if _, err := Normalize(name, tc.protocol, tc.baseURL, tc.models, tc.review, tc.ocr); err == nil {
				t.Fatal("invalid endpoint was accepted")
			}
		})
	}
}

func TestProbeReadsCatalogWithoutLeakingTheCredential(t *testing.T) {
	const apiKey = "sk-probe-secret"
	// httptest endpoints are loopback addresses; the deployment allowlist is
	// the sanctioned way a private target stays reachable.
	t.Setenv(AllowedHostsEnv, "127.0.0.1")
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		authorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "review-b"}, {"id": "review-a"}, {"id": "review-a"}},
		})
	}))
	defer server.Close()

	result, err := Probe(context.Background(), "", server.URL+"/v1/", apiKey)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer "+apiKey {
		t.Fatalf("authorization = %q", authorization)
	}
	if strings.Join(result.Models, ",") != "review-a,review-b" {
		t.Fatalf("catalog = %v", result.Models)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream rejected "+apiKey, http.StatusUnauthorized)
	}))
	defer failing.Close()
	_, err = Probe(context.Background(), ProtocolOpenAICompatible, failing.URL, apiKey)
	if err == nil || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("probe error leaked the credential: %v", err)
	}
}

func TestProbeAcceptsOllamaStyleCatalog(t *testing.T) {
	t.Setenv(AllowedHostsEnv, "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "qwen3"}}})
	}))
	defer server.Close()
	result, err := Probe(context.Background(), ProtocolOpenAICompatible, server.URL, "")
	if err != nil || len(result.Models) != 1 || result.Models[0] != "qwen3" {
		t.Fatalf("probe result = %+v, %v", result, err)
	}
}

func TestSealAndOpenRequireTheDeploymentKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	t.Setenv(EncryptionKeyEnv, key)
	sealed, err := Seal("sk-provider")
	if err != nil {
		t.Fatal(err)
	}
	if string(sealed) == "sk-provider" || len(sealed) == 0 {
		t.Fatal("credential was not sealed")
	}
	opened, err := Open(sealed)
	if err != nil || opened != "sk-provider" {
		t.Fatalf("open = %q, %v", opened, err)
	}
	empty, err := Seal("  ")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty key = %v, %v", empty, err)
	}
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 1
	if _, err := Open(tampered); err == nil {
		t.Fatal("tampered credential was opened")
	}

	t.Setenv(EncryptionKeyEnv, "")
	if _, err := Seal("sk-provider"); !errors.Is(err, ErrEncryptionKey) {
		t.Fatalf("missing key error = %v", err)
	}
	if _, err := Open(sealed); !errors.Is(err, ErrEncryptionKey) {
		t.Fatalf("open without key error = %v", err)
	}
}
