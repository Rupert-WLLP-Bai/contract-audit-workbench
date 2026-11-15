// File metadata (manifest mirror):
// Purpose: Exercises the tenant configuration and money-policy admin routes over HTTP: catalog reads, preview/adoption revision guards, live control and rejected platform-operator actions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"

	"github.com/jackc/pgx/v5"
)

func provisionTenantRow(t *testing.T, st *store.Store, tenantID string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), "SET search_path TO "+pgx.Identifier{st.SchemaName()}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), `INSERT INTO identity_tenants(tenant_id,display_name,status) VALUES($1,$1,'active') ON CONFLICT DO NOTHING`, tenantID); err != nil {
		t.Fatal(err)
	}
}

func newTenantConfigAPI(t *testing.T) *testAPI {
	t.Helper()
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(scriptModels{})
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	if _, err := st.SaveLLMEndpoint(context.Background(), admin, store.LLMEndpointInput{
		Name: "测试端点", Protocol: "openai-compatible", BaseURL: "http://127.0.0.1:18090",
		Models: []string{"review-a", "ocr-b"}, ReviewModel: "review-a", OCRModel: "ocr-b",
	}); err != nil {
		t.Fatalf("save endpoint: %v", err)
	}
	provisionTenantRow(t, st, admin.TenantID)
	srv := httptest.NewServer(httpapi.Handler(st, httpapi.Options{}))
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &testAPI{base: srv.URL, client: &http.Client{Jar: jar}}
}

func tenantConfigSelectionFromView(t *testing.T, view map[string]any) map[string]any {
	t.Helper()
	catalog, ok := view["public_template_catalog"].([]any)
	if !ok || len(catalog) == 0 {
		t.Fatalf("tenant config view has no public template catalog: %v", view)
	}
	entry := catalog[0].(map[string]any)
	models, _ := view["model_catalog"].([]any)
	if len(models) == 0 {
		t.Fatalf("tenant config view has no model catalog: %v", view)
	}
	return map[string]any{
		"public_template_ref": map[string]any{"id": entry["id"], "version": entry["version"], "digest": entry["digest"]},
		"private_policy_refs": []any{},
		"model_catalog_ref":   models[0].(map[string]any)["id"],
		"credential_ref":      nil,
		"egress_mode":         "approved_only",
	}
}

func TestTenantConfigPreviewAdoptAndLiveControlOverHTTP(t *testing.T) {
	api := newTenantConfigAPI(t)
	loginAdminAudience(t, api)

	status, view := api.do(t, http.MethodGet, "/api/admin/tenant-config", nil)
	if status != http.StatusOK || view["schema_version"] != "tenant-config/v1" || view["revision"] != "0" {
		t.Fatalf("tenant config view: %d %v", status, view)
	}
	selection := tenantConfigSelectionFromView(t, view)

	status, preview := api.do(t, http.MethodPost, "/api/admin/tenant-config/preview", map[string]any{
		"expected_revision": "0", "selection": selection,
	})
	if status != http.StatusOK || preview["valid"] != true || len(preview["diff"].([]any)) != 5 {
		t.Fatalf("tenant config preview: %d %v", status, preview)
	}
	if status, _ := api.do(t, http.MethodPost, "/api/admin/tenant-config/preview", map[string]any{
		"expected_revision": "0", "selection": map[string]any{"egress_mode": "cloud"},
	}); status != http.StatusOK {
		t.Fatalf("invalid preview must still report validation problems: %d", status)
	}
	status, invalidPreview := api.do(t, http.MethodPost, "/api/admin/tenant-config/preview", map[string]any{
		"expected_revision": "0", "selection": map[string]any{"egress_mode": "cloud"},
	})
	if status != http.StatusOK || invalidPreview["valid"] != false || len(invalidPreview["errors"].([]any)) == 0 {
		t.Fatalf("invalid selection preview: %d %v", status, invalidPreview)
	}
	if status, _ := api.do(t, http.MethodPost, "/api/admin/tenant-config/preview", map[string]any{
		"expected_revision": "9", "selection": selection,
	}); status != http.StatusConflict {
		t.Fatalf("stale preview = %d", status)
	}
	if status, _ := api.do(t, http.MethodPost, "/api/admin/tenant-config/preview", map[string]any{
		"expected_revision": "bad", "selection": selection,
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("malformed preview revision = %d", status)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/tenant-config/adoption", map[string]any{
		"expected_revision": "0", "selection": map[string]any{"egress_mode": "cloud"},
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("adoption of an invalid selection = %d", status)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/tenant-config/adoption", map[string]any{
		"expected_revision": "bad", "selection": selection,
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("malformed adoption revision = %d", status)
	}

	status, adopted := api.do(t, http.MethodPut, "/api/admin/tenant-config/adoption", map[string]any{
		"expected_revision": "0", "selection": selection,
	})
	if status != http.StatusOK || adopted["revision"] != "1" {
		t.Fatalf("tenant config adoption: %d %v", status, adopted)
	}
	config, ok := adopted["adopted_config"].(map[string]any)
	if !ok || config["version_id"] == "" || config["digest"] == "" || config["egress_mode"] != "approved_only" {
		t.Fatalf("adopted config lost identity: %v", adopted)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/tenant-config/adoption", map[string]any{
		"expected_revision": "0", "selection": selection,
	}); status != http.StatusConflict {
		t.Fatalf("stale adoption = %d", status)
	}

	status, current := api.do(t, http.MethodGet, "/api/admin/tenant-config", nil)
	if status != http.StatusOK || current["revision"] != "1" {
		t.Fatalf("adopted view: %d %v", status, current)
	}
	if value, ok := current["current"].(map[string]any); !ok || value["version_id"] == "" || len(value["public_template"].(map[string]any)) == 0 {
		t.Fatalf("adopted view lost the current config: %v", current)
	}

	status, preview = api.do(t, http.MethodPost, "/api/admin/tenant-config/preview", map[string]any{
		"expected_revision": "1", "selection": selection,
	})
	if status != http.StatusOK || preview["valid"] != true || len(preview["diff"].([]any)) != 0 {
		t.Fatalf("unchanged preview after adoption: %d %v", status, preview)
	}

	live, ok := view["live_control"].(map[string]any)
	if !ok {
		t.Fatalf("view lost live control: %v", view)
	}
	status, control := api.do(t, http.MethodPut, "/api/admin/tenant-config/live-control", map[string]any{
		"expected_revision": live["revision"].(string), "enabled": false,
	})
	if status != http.StatusOK || control["enabled"] != false {
		t.Fatalf("live control: %d %v", status, control)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/tenant-config/live-control", map[string]any{
		"expected_revision": live["revision"].(string), "enabled": true,
	}); status != http.StatusConflict {
		t.Fatalf("stale live control = %d", status)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/tenant-config/live-control", map[string]any{
		"expected_revision": "bad", "enabled": true,
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("malformed live control revision = %d", status)
	}
}

func TestTenantAndPlatformMoneyPolicyOverHTTP(t *testing.T) {
	api := newTenantConfigAPI(t)
	loginAdminAudience(t, api)

	status, policy := api.do(t, http.MethodGet, "/api/admin/tenant-cost-policy", nil)
	if status != http.StatusOK || policy["revision"] != "0" || policy["configured"] != false {
		t.Fatalf("empty tenant cost policy: %d %v", status, policy)
	}
	status, updated := api.do(t, http.MethodPut, "/api/admin/tenant-cost-policy", map[string]any{
		"expected_revision": "0", "day_limit_minor": "1000000", "month_limit_minor": "5000000",
	})
	if status != http.StatusOK || updated["revision"] != "1" || updated["configured"] != true {
		t.Fatalf("tenant cost limits: %d %v", status, updated)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/tenant-cost-policy", map[string]any{
		"expected_revision": "0", "day_limit_minor": "2000000",
	}); status != http.StatusConflict {
		t.Fatalf("stale tenant cost limits = %d", status)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/tenant-cost-policy", map[string]any{
		"expected_revision": "1", "day_limit_minor": "1.5",
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid minor units = %d", status)
	}
	status, read := api.do(t, http.MethodGet, "/api/admin/tenant-cost-policy", nil)
	if status != http.StatusOK || read["revision"] != "1" || read["day_limit_minor"] != "1000000" {
		t.Fatalf("updated tenant cost policy: %d %v", status, read)
	}
	if status, _ := api.do(t, http.MethodPost, "/api/admin/tenant-cost-adjustments", map[string]any{
		"lease_id": "", "kind": "adjustment", "amount_minor": "1", "idempotency_key": "key", "reason": "short",
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid cost adjustment = %d", status)
	}
	if status, _ := api.do(t, http.MethodGet, "/api/admin/platform/model-money-policy", nil); status != http.StatusForbidden {
		t.Fatalf("tenant admin platform policy read = %d", status)
	}
	if status, _ := api.do(t, http.MethodPut, "/api/admin/platform/model-money-policy", map[string]any{
		"expected_revision": "0", "prices": []any{},
	}); status != http.StatusForbidden {
		t.Fatalf("tenant admin platform policy write = %d", status)
	}
}
