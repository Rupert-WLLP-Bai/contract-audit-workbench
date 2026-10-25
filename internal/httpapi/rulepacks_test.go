package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// adminLogin establishes the admin-audience session the governance routes
// require and returns the client-visible actor.
func adminLogin(t *testing.T, api *testAPI) {
	t.Helper()
	status, body := api.do(t, http.MethodPost, "/api/session", map[string]any{
		"username": "demo-admin", "password": "admin-demo", "audience": "admin",
	})
	if status != http.StatusOK {
		t.Fatalf("admin login %d %v", status, body)
	}
}

func governedHTTPManifest(version, bandwidth string) map[string]any {
	return map[string]any{
		"schema_version":  "rulepack/v1",
		"id":              "telecom-governed",
		"version":         version,
		"tenant_id":       "local-demo",
		"effective_from":  "2020-01-01T00:00:00Z",
		"effective_until": "2100-01-01T00:00:00Z",
		"rules": []any{
			map[string]any{
				"rule_id": "ENT-TECH-BW-001", "version": "1", "domain": "technical",
				"capability_id": "dedicated_line.minimum_bandwidth", "mandatory": true,
				"business_scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
				"tenant_id":      "local-demo", "comparator": "minimum_bandwidth",
				"threshold":         map[string]any{"value": bandwidth, "unit": "Mbit/s"},
				"required_fact":     map[string]any{"dimension": "minimum_bandwidth", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "bandwidth_decimal"},
				"authority_sources": []any{map[string]any{"id": "operator-policy", "version": "1", "kind": "internal_policy"}},
			},
			map[string]any{
				"rule_id": "ENT-TECH-SLA-001", "version": "1", "domain": "technical",
				"capability_id": "dedicated_line.monthly_availability", "mandatory": true,
				"business_scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
				"tenant_id":      "local-demo", "comparator": "monthly_sla",
				"threshold": map[string]any{"value": "99.9", "unit": "percent"},
				"required_fact": map[string]any{"dimension": "monthly_availability", "source_roles": []string{"draft_contract"}, "object_required": true,
					"unit_family": "percentage", "measurement_window": "calendar_month", "time_zone": "Asia/Shanghai",
					"calendar": "gregorian", "outage_definition": "all_service_unavailability", "exclusions": []string{}},
				"authority_sources": []any{map[string]any{"id": "operator-policy", "version": "1", "kind": "internal_policy"}},
			},
			map[string]any{
				"rule_id": "ENT-COM-ADV-001", "version": "1", "domain": "commercial",
				"capability_id": "payment.advance_ratio.ceiling", "mandatory": true,
				"business_scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
				"tenant_id":      "local-demo", "comparator": "advance_payment_ceiling",
				"threshold":         map[string]any{"value": "1/3", "unit": "ratio", "basis": "contract_total"},
				"required_fact":     map[string]any{"dimension": "advance_payment_ratio", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "ratio", "basis": "contract_total"},
				"authority_sources": []any{map[string]any{"id": "operator-policy", "version": "1", "kind": "internal_policy"}},
			},
		},
	}
}

// TestRulePackAdminRoutesRequireTrustedAdminSession proves every governance route
// rejects a missing session (401) and a client-audience session (401/403), so the
// platform admin role never becomes contract-body authority.
func TestRulePackAdminRoutesRequireTrustedAdminSession(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")

	for _, path := range []string{
		"/api/admin/rulepacks",
		"/api/admin/rulepacks/telecom-governed/versions/1",
		"/api/admin/rulepacks/telecom-governed/history",
	} {
		status, _ := api.do(t, http.MethodGet, path, nil)
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Fatalf("client session read %s status=%d, want 401/403", path, status)
		}
	}
	status, _ := api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{
		"manifest": governedHTTPManifest("1", "100"),
	})
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("client session create status=%d, want 401/403", status)
	}
}

// TestRulePackAdminLifecycleOverHTTP drives the real Admin API: create, read,
// validate, approve, publish, list, history and sample test, then verifies the
// published pointer is visible and the sample report is synthetic-only.
func TestRulePackAdminLifecycleOverHTTP(t *testing.T) {
	api := newAPI(t)
	adminLogin(t, api)

	status, created := api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{
		"manifest": governedHTTPManifest("1", "100"),
	})
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", status, created)
	}
	pack := created["pack"].(map[string]any)
	if pack["id"] != "telecom-governed" || pack["status"] != "draft" || pack["revision"] != float64(1) {
		t.Fatalf("created pack %v", pack)
	}
	if _, ok := created["manifest"].(map[string]any); !ok {
		t.Fatalf("create response omitted manifest: %v", created)
	}

	base := "/api/admin/rulepacks/telecom-governed/versions/1"
	status, validated := api.do(t, http.MethodPost, base+"/validate", map[string]any{"expected_revision": 1})
	if status != http.StatusOK || validated["pack"].(map[string]any)["status"] != "validated" {
		t.Fatalf("validate status=%d body=%v", status, validated)
	}
	revision := int(validated["pack"].(map[string]any)["revision"].(float64))
	contentHash := validated["pack"].(map[string]any)["content_hash"].(string)

	// A stale revision is refused.
	status, _ = api.do(t, http.MethodPost, base+"/approve", map[string]any{
		"expected_revision": revision - 1, "expected_content_hash": contentHash,
	})
	if status != http.StatusConflict {
		t.Fatalf("stale approve status=%d, want 409", status)
	}
	status, approved := api.do(t, http.MethodPost, base+"/approve", map[string]any{
		"expected_revision": revision, "expected_content_hash": contentHash,
	})
	if status != http.StatusOK || approved["pack"].(map[string]any)["status"] != "approved" {
		t.Fatalf("approve status=%d body=%v", status, approved)
	}
	revision = int(approved["pack"].(map[string]any)["revision"].(float64))

	// A stale pointer revision is refused before publish.
	status, _ = api.do(t, http.MethodPost, base+"/publish", map[string]any{
		"expected_revision": revision, "expected_pointer_revision": 9,
	})
	if status != http.StatusConflict {
		t.Fatalf("stale pointer publish status=%d, want 409", status)
	}
	status, published := api.do(t, http.MethodPost, base+"/publish", map[string]any{
		"expected_revision": revision, "expected_pointer_revision": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("publish status=%d body=%v", status, published)
	}
	pack = published["pack"].(map[string]any)
	if pack["status"] != "published" || pack["is_active"] != true || pack["active_pointer_revision"] != float64(1) {
		t.Fatalf("published pack %v", pack)
	}
	revision = int(pack["revision"].(float64))

	// List shows the active pointer.
	status, list := api.do(t, http.MethodGet, "/api/admin/rulepacks", nil)
	if status != http.StatusOK {
		t.Fatalf("list status=%d", status)
	}
	items := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list items %d", len(items))
	}
	if _, hasTopLevel := list["active_pointer_revision"]; hasTopLevel {
		t.Fatal("list must not expose a top-level pointer revision")
	}

	// History is append-only and actor-bound.
	status, history := api.do(t, http.MethodGet, "/api/admin/rulepacks/telecom-governed/history", nil)
	if status != http.StatusOK {
		t.Fatalf("history status=%d", status)
	}
	entries := history["items"].([]any)
	if len(entries) != 4 {
		t.Fatalf("history entries %d, want 4", len(entries))
	}
	if entries[0].(map[string]any)["actor_id"] != "demo-admin" {
		t.Fatalf("history actor %v", entries[0])
	}
	assertWireSchema(t, openAPI(t), "RulePackHistory", history)
	for _, raw := range entries {
		entry := raw.(map[string]any)
		for _, key := range []string{"previous_version", "previous_active", "resulting_version", "resulting_active"} {
			if _, ok := entry[key]; !ok {
				t.Fatalf("history omitted %s: %v", key, entry)
			}
		}
		if entry["action"] != "publish" {
			if entry["previous_version"] != nil || entry["previous_active"] != nil || entry["resulting_version"] != nil || entry["resulting_active"] != nil {
				t.Fatalf("non-pointer history invented a pointer image: %v", entry)
			}
			continue
		}
		if entry["previous_version"] != nil || entry["previous_active"] != nil || entry["resulting_version"] != "1" || entry["resulting_active"] != true {
			t.Fatalf("first publish pointer image = %v", entry)
		}
	}

	// Sample test runs a bounded synthetic execution.
	status, sample := api.do(t, http.MethodPost, base+"/test", map[string]any{
		"expected_revision": revision,
		"business_line":     "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service",
		"at":      "2026-06-01T00:00:00Z",
		"sources": []any{map[string]any{"id": "draft", "version": "1", "role": "draft_contract", "text": "电路 LINE-001：最低保障带宽 150 Mbit/s。"}},
		"facts": []any{map[string]any{
			"id": "fact-bw", "rule_id": "ENT-TECH-BW-001", "dimension": "minimum_bandwidth",
			"object_ref": "LINE-001", "value": "150", "unit": "Mbit/s", "source_role": "draft_contract",
			"citations": []any{map[string]any{"source_id": "draft", "source_version": "1", "start": 0, "end": 30, "quote": "电路 LINE-001：最低保障带宽 150 Mbit/s。"}},
		}},
	})
	if status != http.StatusOK {
		t.Fatalf("sample test status=%d body=%v", status, sample)
	}
	report := sample["report"].(map[string]any)
	if report["pack_id"] != "telecom-governed" {
		t.Fatalf("sample report %v", report)
	}

	// Disable clears the pointer but keeps the published version readable.
	status, disabled := api.do(t, http.MethodPost, base+"/disable", map[string]any{
		"expected_revision": revision, "expected_pointer_revision": 1,
	})
	if status != http.StatusOK {
		t.Fatalf("disable status=%d body=%v", status, disabled)
	}
	if disabled["pack"].(map[string]any)["is_active"] != false ||
		disabled["pack"].(map[string]any)["status"] != "published" {
		t.Fatalf("disabled pack %v", disabled["pack"])
	}
	status, detail := api.do(t, http.MethodGet, base, nil)
	if status != http.StatusOK || detail["pack"].(map[string]any)["status"] != "published" {
		t.Fatalf("published bytes were not readable after disable: %d %v", status, detail)
	}
}

// TestRulePackAdminRejectsForeignTenantAndUnknownFields proves the strict body
// contract: a manifest for another tenant and an unknown field are both refused.
func TestRulePackAdminRejectsForeignTenantAndUnknownFields(t *testing.T) {
	api := newAPI(t)
	adminLogin(t, api)

	foreign := governedHTTPManifest("1", "100")
	foreign["tenant_id"] = "other-tenant"
	status, _ := api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{"manifest": foreign})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("foreign tenant status=%d, want 422", status)
	}

	status, _ = api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{
		"manifest":    governedHTTPManifest("1", "100"),
		"approved_by": "attacker",
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown field status=%d, want 422", status)
	}

	// A client-supplied lifecycle attestation is refused.
	withLifecycle := governedHTTPManifest("1", "100")
	withLifecycle["lifecycle"] = map[string]any{"state": "published"}
	status, body := api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{"manifest": withLifecycle})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("client lifecycle status=%d body=%v, want 422", status, body)
	}
}

// TestRulePackOpenAPIDocumentsOnlyImplementedRoutes proves the contract lists the
// governance routes it actually serves and keeps the bounded request schemas.
func TestRulePackOpenAPIDocumentsOnlyImplementedRoutes(t *testing.T) {
	api := newAPI(t)
	status, raw := api.bytes(t, http.MethodGet, "/openapi.json", nil, "")
	if status != http.StatusOK {
		t.Fatalf("openapi status=%d", status)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	for _, path := range []string{
		"/api/admin/rulepacks",
		"/api/admin/rulepacks/{packID}/versions/{version}",
		"/api/admin/rulepacks/{packID}/versions/{version}/validate",
		"/api/admin/rulepacks/{packID}/versions/{version}/approve",
		"/api/admin/rulepacks/{packID}/versions/{version}/publish",
		"/api/admin/rulepacks/{packID}/versions/{version}/disable",
		"/api/admin/rulepacks/{packID}/versions/{version}/test",
		"/api/admin/rulepacks/{packID}/rollback",
		"/api/admin/rulepacks/{packID}/history",
	} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("spec is missing implemented route %s", path)
		}
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	summary := schemas["RulePackSummary"].(map[string]any)
	if summary["additionalProperties"] != false {
		t.Fatal("RulePackSummary must be a strict object")
	}
	props := summary["properties"].(map[string]any)
	for _, field := range []string{"id", "version", "tenant_id", "status", "revision", "content_hash",
		"snapshot_sha256", "effective_from", "effective_until", "authority_sources", "is_active", "active_pointer_revision"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("RulePackSummary is missing %s", field)
		}
	}
	if _, ok := props["approved_by"]; ok {
		t.Fatal("RulePackSummary must not accept a client approval identity")
	}
	// The response snapshot must carry lifecycle attestations, while the create
	// request manifest stays draft-only and cannot submit them.
	detail := schemas["RulePackDetail"].(map[string]any)
	detailProps := detail["properties"].(map[string]any)
	manifestRef := detailProps["manifest"].(map[string]any)["$ref"].(string)
	if manifestRef != "#/components/schemas/RulePackManifestSnapshot" {
		t.Fatalf("detail.manifest must reference the persisted snapshot, got %s", manifestRef)
	}
	snapshotSchema := schemas["RulePackManifestSnapshot"].(map[string]any)
	snapshotProps := snapshotSchema["properties"].(map[string]any)
	if _, ok := snapshotProps["lifecycle"]; !ok {
		t.Fatal("the response snapshot schema must include lifecycle")
	}
	requestManifest := schemas["RulePackManifestInput"].(map[string]any)
	if _, ok := requestManifest["properties"].(map[string]any)["lifecycle"]; ok {
		t.Fatal("the request manifest must not accept lifecycle attestations")
	}
	// The sample-test report must be the concrete deterministic report, not an
	// unconstrained object.
	testResult := schemas["RulePackTestResult"].(map[string]any)
	reportRef := testResult["properties"].(map[string]any)["report"].(map[string]any)["$ref"]
	if reportRef != "#/components/schemas/RulePackReport" {
		t.Fatalf("sample report must reference RulePackReport, got %v", reportRef)
	}
	testRequest := schemas["RulePackTestRequest"].(map[string]any)
	testProps := testRequest["properties"].(map[string]any)
	if _, ok := testProps["principal"]; ok {
		t.Fatal("sample test must not accept a client principal")
	}
	for _, forbidden := range []string{"tenant_id", "case_id", "grant"} {
		if _, ok := testProps[forbidden]; ok {
			t.Fatalf("sample test must not accept %s", forbidden)
		}
	}
	if !strings.Contains(string(raw), "合成样例") {
		t.Fatal("sample test must be documented as a synthetic sample")
	}
}

// TestRulePackPublishAndRollbackReplayByIdempotencyKey proves the HTTP layer
// forwards Idempotency-Key so an identical publish/rollback retry replays the
// original result instead of re-executing and conflicting.
func TestRulePackPublishAndRollbackReplayByIdempotencyKey(t *testing.T) {
	api := newAPI(t)
	adminLogin(t, api)
	if status, _ := api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{
		"manifest": governedHTTPManifest("1", "100"),
	}); status != http.StatusCreated {
		t.Fatalf("create %d", status)
	}
	base := "/api/admin/rulepacks/telecom-governed/versions/1"
	_, validated := api.do(t, http.MethodPost, base+"/validate", map[string]any{"expected_revision": 1})
	revision := int(validated["pack"].(map[string]any)["revision"].(float64))
	hash := validated["pack"].(map[string]any)["content_hash"].(string)
	_, approved := api.do(t, http.MethodPost, base+"/approve", map[string]any{
		"expected_revision": revision, "expected_content_hash": hash,
	})
	revision = int(approved["pack"].(map[string]any)["revision"].(float64))

	publishBody := map[string]any{"expected_revision": revision, "expected_pointer_revision": 0}
	status, first := api.send(t, http.MethodPost, base+"/publish", publishBody, "publish-1")
	if status != http.StatusOK {
		t.Fatalf("first publish %d %v", status, first)
	}
	status, replay := api.send(t, http.MethodPost, base+"/publish", publishBody, "publish-1")
	if status != http.StatusOK {
		t.Fatalf("identical publish replay was refused: %d %v", status, replay)
	}
	if replay["pack"].(map[string]any)["active_pointer_revision"] != first["pack"].(map[string]any)["active_pointer_revision"] {
		t.Fatal("publish replay returned a different pointer revision")
	}

	// Publish v2 so a rollback back to v1 is a real pointer move.
	if status, _ := api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{
		"manifest": governedHTTPManifest("2", "200"),
	}); status != http.StatusCreated {
		t.Fatalf("create v2 %d", status)
	}
	baseV2 := "/api/admin/rulepacks/telecom-governed/versions/2"
	_, v2Validated := api.do(t, http.MethodPost, baseV2+"/validate", map[string]any{"expected_revision": 1})
	v2Revision := int(v2Validated["pack"].(map[string]any)["revision"].(float64))
	v2Hash := v2Validated["pack"].(map[string]any)["content_hash"].(string)
	_, v2Approved := api.do(t, http.MethodPost, baseV2+"/approve", map[string]any{
		"expected_revision": v2Revision, "expected_content_hash": v2Hash,
	})
	v2Revision = int(v2Approved["pack"].(map[string]any)["revision"].(float64))
	if status, body := api.do(t, http.MethodPost, baseV2+"/publish", map[string]any{
		"expected_revision": v2Revision, "expected_pointer_revision": 1,
	}); status != http.StatusOK {
		t.Fatalf("publish v2 %d %v", status, body)
	}

	rollbackBody := map[string]any{"version": "1", "expected_pointer_revision": 2}
	status, rollback := api.send(t, http.MethodPost, "/api/admin/rulepacks/telecom-governed/rollback", rollbackBody, "rollback-1")
	if status != http.StatusOK {
		t.Fatalf("rollback %d %v", status, rollback)
	}
	status, rollbackReplay := api.send(t, http.MethodPost, "/api/admin/rulepacks/telecom-governed/rollback", rollbackBody, "rollback-1")
	if status != http.StatusOK {
		t.Fatalf("identical rollback replay was refused: %d %v", status, rollbackReplay)
	}
}
