package httpapi_test

import (
	"net/http"
	"testing"
)

// The operator-audit extension is absent from the default review path. Until an
// operator enables it, every extension route returns a stable 503 instead of
// exposing a clickable write surface or a half-configured connector.
func TestOperatorAuditRoutesAreDisabledByDefault(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	for _, request := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/capability-packs", nil},
		{http.MethodPost, "/api/cases/case-1/object-bindings", map[string]any{}},
		{http.MethodPost, "/api/cases/case-1/external-collection-jobs", map[string]any{}},
		{http.MethodGet, "/api/cases/case-1/external-snapshots", nil},
		{http.MethodPost, "/api/cases/case-1/fulfillment-evaluations", map[string]any{}},
		{http.MethodGet, "/api/cases/case-1/fulfillment-evaluations/eval-1", nil},
		{http.MethodGet, "/api/cases/case-1/fulfillment-evaluations/eval-1/impact", nil},
	} {
		status, body := api.do(t, request.method, request.path, request.body)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("%s %s = %d %v, want 503 before the feature is enabled", request.method, request.path, status, body)
		}
		if errorBody, ok := body["error"].(map[string]any); !ok || errorBody["code"] != "operator_audit_disabled" {
			t.Fatalf("%s %s error = %v, want operator_audit_disabled", request.method, request.path, body)
		}
	}
}
