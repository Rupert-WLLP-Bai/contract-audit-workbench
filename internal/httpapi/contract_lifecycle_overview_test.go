// File metadata (manifest mirror):
// Purpose: Checks the real lifecycle HTTP response, OpenAPI null unions, private caching and contract read authorization using an authenticated draft fixture.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestContractLifecycleOverviewHTTPNullsAndAuthorization(t *testing.T) {
	api := clmDraftAPI(t)
	status, _ := api.do(t, http.MethodGet, "/api/contracts/00000000-0000-4000-8000-000000000001/lifecycle-overview", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous overview: %d", status)
	}
	api.login(t, "demo-clerk", "clerk-demo")
	status, created := clmJSONRequest(t, api, http.MethodPost, "/api/contracts", clmHeaders("lifecycle-http-create"), clmCreatePayload("履约概览草稿"))
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, created)
	}
	id := created["summary"].(map[string]any)["contract_id"].(string)
	path := "/api/contracts/" + id + "/lifecycle-overview"
	response, err := api.client.Get(api.base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("overview HTTP/cache: %d %q %v", response.StatusCode, response.Header.Get("Cache-Control"), body)
	}
	spec := openAPI(t)
	assertWireSchema(t, spec, "ContractLifecycleOverview", body)
	status, rejected := clmJSONRequest(t, api, http.MethodPost, "/api/contracts/"+id+"/acceptances/10000000-0000-4000-8000-000000000001/rectifications", clmHeaders("lifecycle-path-selector"), map[string]any{
		"expected_row_version": 1, "contract_revision_id": "20000000-0000-4000-8000-000000000001", "defect_id": "30000000-0000-4000-8000-000000000001", "description": "路径中的验收身份由服务端提供", "evidence": []any{}, "acceptance_id": "40000000-0000-4000-8000-000000000001",
	})
	if status != http.StatusUnprocessableEntity || rejected["error"].(map[string]any)["code"] != "invalid" {
		t.Fatalf("body acceptance selector was not explicitly rejected: %d %v", status, rejected)
	}
	if body["plans"] != nil || body["contract"].(map[string]any)["current_revision_id"] != nil {
		t.Fatalf("draft plans must remain unknown: %v", body)
	}
	// The wire contract must also accept a frozen-shaped empty projection;
	// emptiness is a representation, not proof that obligations are complete.
	body["contract"].(map[string]any)["current_revision_id"] = "10000000-0000-4000-8000-000000000001"
	body["plans"] = map[string]any{"delivery_plans": []any{}, "acceptance_plans": nil, "payments": nil, "assets": []any{}}
	body["payments"] = []any{}
	assertWireSchema(t, spec, "ContractLifecycleOverview", body)
	api.login(t, "demo-admin", "admin-demo")
	status, body = api.do(t, http.MethodGet, path, nil)
	if status != http.StatusNotFound {
		t.Fatalf("non-creator overview: %d %v", status, body)
	}
}
