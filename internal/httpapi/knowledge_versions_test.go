package httpapi_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/httpapi"
)

func TestKnowledgeVersionRoutesPreserveExactTenantScopedContent(t *testing.T) {
	api := newAPI(t)
	base := "/api/knowledge/versioned-policy"
	status, body := api.do(t, http.MethodGet, base+"/versions/invalid", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous exact version read: status=%d body=%v", status, body)
	}
	api.login(t, "demo-admin", "admin-demo")

	firstInput := map[string]any{
		"id": "versioned-policy", "title": "采购制度", "version": "1.0", "body": "第一版正文。",
		"source_type": "reference", "effective_at": nil,
	}
	status, first := api.do(t, http.MethodPost, "/api/knowledge", firstInput)
	if status != http.StatusCreated {
		t.Fatalf("create first version: status=%d body=%v", status, first)
	}
	firstID, ok := first["version_id"].(string)
	if !ok || len(firstID) != 64 || strings.Trim(firstID, "0123456789abcdef") != "" {
		t.Fatalf("first version identity is not lowercase SHA-256: %v", first)
	}
	assertWireSchema(t, openAPI(t), "Knowledge", first)

	// Legacy latest uses millisecond timestamps, so separate the two creations.
	time.Sleep(2 * time.Millisecond)
	secondInput := map[string]any{
		"id": "versioned-policy", "title": "采购制度", "version": "2.0", "body": "第二版正文。",
		"source_type": "reference", "effective_at": nil,
	}
	status, second := api.do(t, http.MethodPost, "/api/knowledge", secondInput)
	secondID, ok := second["version_id"].(string)
	if status != http.StatusCreated || !ok || len(secondID) != 64 || secondID == firstID {
		t.Fatalf("create second version: status=%d body=%v", status, second)
	}
	assertWireSchema(t, openAPI(t), "Knowledge", second)

	for _, expected := range []map[string]any{first, second} {
		id := expected["version_id"].(string)
		status, actual := api.do(t, http.MethodGet, base+"/versions/"+id, nil)
		if status != http.StatusOK || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("exact read %s: status=%d got=%v want=%v", id, status, actual, expected)
		}
		assertWireSchema(t, openAPI(t), "Knowledge", actual)
	}
	status, latest := api.do(t, http.MethodGet, base, nil)
	if status != http.StatusOK || !reflect.DeepEqual(latest, second) {
		t.Fatalf("legacy latest read: status=%d body=%v", status, latest)
	}
	status, duplicate := api.do(t, http.MethodPost, "/api/knowledge", firstInput)
	if status != http.StatusCreated || !reflect.DeepEqual(duplicate, first) {
		t.Fatalf("recreate older version: status=%d body=%v", status, duplicate)
	}
	for _, invalid := range []string{"invalid", "2.0", strings.ToUpper(firstID), strings.Repeat("0", 64)} {
		status, body = api.do(t, http.MethodGet, base+"/versions/"+invalid, nil)
		if status != http.StatusNotFound || body["error"].(map[string]any)["code"] != "not_found" {
			t.Fatalf("invalid version %q fell back or leaked: status=%d body=%v", invalid, status, body)
		}
	}
	status, body = api.do(t, http.MethodGet, "/api/knowledge/other-policy/versions/"+firstID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("version from another knowledge ID was returned: status=%d body=%v", status, body)
	}

	status, snapshot := api.do(t, http.MethodGet, "/api/workbench?period=week", nil)
	if status != http.StatusOK {
		t.Fatalf("workbench: status=%d body=%v", status, snapshot)
	}
	assertWireSchema(t, openAPI(t), "Workbench", snapshot)
	items := snapshot["knowledge"].([]any)
	seen := map[string]map[string]any{}
	for _, item := range items {
		entry := item.(map[string]any)
		seen[entry["version_id"].(string)] = entry
	}
	if len(items) != 2 || !reflect.DeepEqual(seen[firstID], first) || !reflect.DeepEqual(seen[secondID], second) {
		t.Fatalf("workbench versions disagree with exact reads: %v", items)
	}

	api.login(t, "demo-otherorg-clerk", "otherorg-clerk-demo")
	status, sameTenant := api.do(t, http.MethodGet, base+"/versions/"+firstID, nil)
	if status != http.StatusOK || !reflect.DeepEqual(sameTenant, first) {
		t.Fatalf("same-tenant organization lost shared knowledge: status=%d body=%v", status, sameTenant)
	}
	api.login(t, "demo-tenant2-admin", "tenant2-admin-demo")
	status, body = api.do(t, http.MethodGet, base+"/versions/"+firstID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("foreign tenant read another tenant's version: status=%d body=%v", status, body)
	}
	status, body = api.do(t, http.MethodGet, base+"/versions/"+firstID+"?tenant_id=local-demo", nil)
	if status != http.StatusNotFound {
		t.Fatalf("client-supplied tenant changed exact read scope: status=%d body=%v", status, body)
	}
	status, foreign := api.do(t, http.MethodPost, "/api/knowledge", firstInput)
	foreignID, ok := foreign["version_id"].(string)
	if status != http.StatusCreated || !ok || foreignID == firstID {
		t.Fatalf("foreign tenant version identity: status=%d body=%v", status, foreign)
	}
	status, foreignRead := api.do(t, http.MethodGet, base+"/versions/"+foreignID, nil)
	if status != http.StatusOK || !reflect.DeepEqual(foreignRead, foreign) {
		t.Fatalf("foreign tenant could not read its own version: status=%d body=%v", status, foreignRead)
	}
	status, foreignSnapshot := api.do(t, http.MethodGet, "/api/workbench?period=week", nil)
	if status != http.StatusOK {
		t.Fatalf("foreign workbench: status=%d body=%v", status, foreignSnapshot)
	}
	foreignItems := foreignSnapshot["knowledge"].([]any)
	if len(foreignItems) != 1 || !reflect.DeepEqual(foreignItems[0], foreign) {
		t.Fatalf("foreign workbench leaked knowledge: %v", foreignItems)
	}
	status, body = api.do(t, http.MethodGet, base+"/versions/"+secondID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("foreign tenant fell back to its own version: status=%d body=%v", status, body)
	}
	api.login(t, "demo-admin", "admin-demo")
	status, body = api.do(t, http.MethodGet, base+"/versions/"+foreignID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("foreign version was visible in the original tenant: status=%d body=%v", status, body)
	}
}

func TestKnowledgeVersionRouteIsOwnedOnlyByCore(t *testing.T) {
	path := "/api/knowledge/knowledge-1/versions/abcdef"
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		for _, role := range []string{"core", "document", "execution"} {
			if got := httpapi.OperationOwnedBy(role, method, path); got != (role == "core") {
				t.Errorf("%s %s ownership for %s = %t", method, path, role, got)
			}
		}
	}
	for _, path := range []string{
		"/api/knowledge/knowledge-1/versions",
		"/api/knowledge/knowledge-1/other/abcdef",
		"/api/knowledge/knowledge-1/versions/abcdef/extra",
	} {
		if httpapi.OperationOwnedBy("core", http.MethodGet, path) {
			t.Errorf("unexpected knowledge route accepted: %s", path)
		}
	}
	if httpapi.OperationOwnedBy("core", http.MethodPost, "/api/knowledge/knowledge-1/versions/abcdef") {
		t.Error("exact knowledge route accepted POST")
	}
}
