// File metadata (manifest mirror):
// Purpose: 实现 `TestEmbeddedGatewayConfigurationAndCredentialRouting` 等单进程服务装配与集成入口，连接已配置的服务组件和权限边界。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package bootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"contractaudit/internal/model"
)

func TestEmbeddedGatewayConfigurationAndCredentialRouting(t *testing.T) {
	for _, key := range []string{"LLM_BASE_URL", "LLM_API_KEY", "MODEL_GATEWAY_URL", "WORKER_MODEL_TOKEN", "DOCUMENT_MODEL_TOKEN", "CORE_MODEL_AUTHORITY_TOKEN"} {
		t.Setenv(key, "")
	}
	if cfg, err := serviceModelConfig(""); err != nil || cfg.Gateway {
		t.Fatalf("offline startup: gateway=%v err=%v", cfg.Gateway, err)
	}
	t.Setenv("LLM_BASE_URL", "https://provider.invalid/v1")
	if _, err := serviceModelConfig(""); err == nil {
		t.Fatal("direct provider silently accepted")
	}
	t.Setenv("MODEL_GATEWAY_URL", "http://gateway.invalid/v1")
	if _, err := serviceModelConfig(""); err == nil {
		t.Fatal("partial gateway accepted")
	}
	worker := strings.Repeat("w", 32)
	document := strings.Repeat("d", 32)
	t.Setenv("WORKER_MODEL_TOKEN", worker)
	t.Setenv("DOCUMENT_MODEL_TOKEN", document)
	t.Setenv("CORE_MODEL_AUTHORITY_TOKEN", strings.Repeat("c", 32))
	cfg, err := serviceModelConfig("")
	if err != nil || !cfg.Gateway || cfg.ProviderURL != "https://provider.invalid/v1" || cfg.APIKey != worker {
		t.Fatalf("invalid gateway config: err=%v", err)
	}
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("X-Contract-Service-Key"))
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"text"}}]}`)
	}))
	defer server.Close()
	cfg.BaseURL = server.URL
	m := embeddedModelsFor(cfg, "", newModelClient(cfg, false))
	reviewCtx := model.WithDelegatedScope(context.Background(), model.DelegatedScope{Kind: "execution", Action: "embedded_review", AdmissionRevision: "r"})
	_, _ = m.Warranty(reviewCtx, nil)
	ocrCtx := model.WithDelegatedScope(context.Background(), model.DelegatedScope{Kind: "document", Action: "embedded_prepare_parse"})
	_, _ = m.Transcribe(ocrCtx, "image/png", model.SampleWarrantyPNG)
	if len(got) != 2 || got[0] != worker || got[1] != document {
		t.Fatalf("wrong caller routing: %d calls", len(got))
	}
	if m.ConfigurationID() != model.New(cfg).ConfigurationID() {
		t.Fatal("worker identity changed")
	}
	t.Setenv("DOCUMENT_MODEL_TOKEN", worker)
	if _, err := serviceModelConfig(""); err == nil {
		t.Fatal("shared credentials accepted")
	}
}
