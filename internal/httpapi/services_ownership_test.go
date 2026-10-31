package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"contractaudit/internal/httpapi"
)

func TestOperationOwnedByRouteOwnershipMatrix(t *testing.T) {
	tests := []struct {
		name, path, owner string
		methods           []string
	}{
		{"openapi", "/openapi.json", "core", []string{http.MethodGet}},
		{"session", "/api/session", "core", []string{http.MethodGet, http.MethodPost, http.MethodDelete}},
		{"settings", "/api/settings", "core", []string{http.MethodGet}},
		{"templates", "/api/templates", "document", []string{http.MethodGet}},
		{"cases", "/api/cases", "core", []string{http.MethodGet, http.MethodPost}},
		{"workbench", "/api/workbench", "core", []string{http.MethodGet}},
		{"knowledge-create", "/api/knowledge", "core", []string{http.MethodPost}},
		{"knowledge-item", "/api/knowledge/k", "core", []string{http.MethodGet}},
		{"knowledge-version", "/api/knowledge/k/versions/v", "core", []string{http.MethodGet}},
		{"admin-settings", "/api/admin/settings", "core", []string{http.MethodGet}},
		{"admin-health", "/api/admin/system-health", "core", []string{http.MethodGet}},
		{"governed-list", "/api/admin/governed-knowledge", "core", []string{http.MethodGet, http.MethodPost}},
		{"governed-version", "/api/admin/governed-knowledge/k/versions/v", "core", []string{http.MethodGet}},
		{"governed-command", "/api/admin/governed-knowledge/k/versions/v/commands", "core", []string{http.MethodPost}},
		{"governed-preview", "/api/admin/governed-knowledge/k/versions/v/preview", "core", []string{http.MethodGet}},
		{"governed-history", "/api/admin/governed-knowledge/k/history", "core", []string{http.MethodGet}},
		{"legal-source-import", "/api/admin/legal-sources/imports", "core", []string{http.MethodPost}},
		{"legal-source-admin-capture", "/api/admin/legal-sources/source/snapshots/capture", "core", []string{http.MethodGet}},
		{"company-policy-admin-status", "/api/admin/company-policy-sources", "core", []string{http.MethodGet}},
		{"rulepacks", "/api/admin/rulepacks", "core", []string{http.MethodGet, http.MethodPost}},
		{"rulepack-version", "/api/admin/rulepacks/p/versions/v", "core", []string{http.MethodGet, http.MethodPost, http.MethodPut}},
		{"rulepack-command", "/api/admin/rulepacks/p/versions/v/commands", "core", []string{http.MethodPost}},
		{"rulepack-rollback", "/api/admin/rulepacks/p/rollback", "core", []string{http.MethodPost}},
		{"rulepack-history", "/api/admin/rulepacks/p/history", "core", []string{http.MethodGet}},
		{"oidc-login", "/api/auth/oidc/login", "core", []string{http.MethodGet}},
		{"case-item", "/api/cases/c", "core", []string{http.MethodGet}},
		{"case-legal-search", "/api/cases/c/legal-knowledge", "core", []string{http.MethodGet}},
		{"case-legal-applicability-resolve", "/api/cases/c/legal-applicability-facts", "core", []string{http.MethodGet}},
		{"case-legal-applicability-history", "/api/cases/c/legal-applicability-facts/buyer_kind/history", "core", []string{http.MethodGet}},
		{"case-legal-applicability-record", "/api/cases/c/legal-applicability-facts/buyer_kind", "core", []string{http.MethodPut}},
		{"case-legal-version", "/api/cases/c/legal-knowledge/k/versions/v", "core", []string{http.MethodGet}},
		{"case-legal-capture", "/api/cases/c/legal-sources/snapshots/capture", "core", []string{http.MethodGet}},
		{"case-company-policy-status", "/api/cases/c/company-policy-sources", "core", []string{http.MethodGet}},
		{"review-readiness", "/api/cases/c/review-readiness", "execution", []string{http.MethodGet}},
		{"frozen-legal-citation", "/api/cases/c/runs/r/attempts/a/legal-citations/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "execution", []string{http.MethodGet}},
		{"frozen-legal-citation-openapi-template", "/api/cases/{caseID}/runs/{runID}/attempts/{attemptID}/legal-citations/{citationID}", "execution", []string{http.MethodGet}},
		{"case-subject-list-and-create", "/api/cases/c/subjects", "core", []string{http.MethodGet, http.MethodPost}},
		{"case-subject-bind-existing", "/api/cases/c/subjects/bind-existing", "core", []string{http.MethodPost}},
		{"subject-source-directory", "/api/cases/c/subject-sources", "core", []string{http.MethodGet}},
		{"subject-source-snapshot-list-create", "/api/cases/c/subject-source-snapshots", "core", []string{http.MethodGet, http.MethodPost}},
		{"subject-source-snapshot-detail", "/api/cases/c/subject-source-snapshots/ss", "core", []string{http.MethodGet}},
		{"subject-change-list-and-propose", "/api/cases/c/subject-changes", "core", []string{http.MethodGet, http.MethodPost}},
		{"subject-change-detail", "/api/cases/c/subject-changes/r", "core", []string{http.MethodGet}},
		{"subject-change-impact", "/api/cases/c/subject-changes/r/impact", "core", []string{http.MethodGet}},
		{"subject-change-decision", "/api/cases/c/subject-changes/r/decisions", "core", []string{http.MethodPost}},
		{"subject-list-and-create", "/api/subjects", "core", []string{http.MethodGet, http.MethodPost}},
		{"subject-detail", "/api/subjects/s", "core", []string{http.MethodGet}},
		{"subject-history", "/api/subjects/s/history", "core", []string{http.MethodGet}},
		{"subject-case-links", "/api/subjects/s/case-links", "core", []string{http.MethodGet}},
		{"experience-feedback", "/api/cases/c/experience-feedback", "core", []string{http.MethodPost}},
		{"experience-feedback-item", "/api/cases/c/experience-feedback/f", "core", []string{http.MethodGet}},
		{"experience-distill", "/api/cases/c/experience-feedback/f/distillations", "core", []string{http.MethodPost}},
		{"experience-distill-job", "/api/cases/c/experience-feedback/f/distillations/j", "core", []string{http.MethodGet}},
		{"experience-candidate", "/api/cases/c/experience-candidates/x/versions/v", "core", []string{http.MethodGet}},
		{"evidence-requests", "/api/cases/c/evidence-requests", "core", []string{http.MethodGet, http.MethodPost}},
		{"evidence-request", "/api/cases/c/evidence-requests/r", "core", []string{http.MethodGet}},
		{"evidence-history", "/api/cases/c/evidence-requests/r/history", "core", []string{http.MethodGet}},
		{"evidence-receipt", "/api/cases/c/evidence-requests/r/receipts", "core", []string{http.MethodPost}},
		{"evidence-verify", "/api/cases/c/evidence-requests/r/receipts/x/verify", "core", []string{http.MethodPost}},
		{"profile", "/api/cases/c/profile", "core", []string{http.MethodPut}},
		{"task-create", "/api/cases/c/tasks", "core", []string{http.MethodPost}},
		{"task-update", "/api/cases/c/tasks/t", "core", []string{http.MethodPut}},
		{"relation-create", "/api/cases/c/relations", "core", []string{http.MethodPost}},
		{"relation-delete", "/api/cases/c/relations/r", "core", []string{http.MethodDelete}},
		{"document-upload", "/api/cases/c/documents", "document", []string{http.MethodPost}},
		{"material-selection", "/api/cases/c/materials/selection", "document", []string{http.MethodPost}},
		{"material-version-parses", "/api/cases/c/materials/m/versions/1/parsed-documents", "document", []string{http.MethodGet}},
		{"material-version-parse-text", "/api/cases/c/materials/m/versions/1/parsed-documents/p", "document", []string{http.MethodGet}},
		{"document-original", "/api/cases/c/documents/d/original", "document", []string{http.MethodGet}},
		{"document-parse", "/api/cases/c/documents/d/parse", "document", []string{http.MethodPost}},
		{"object-binding", "/api/cases/c/object-bindings", "core", []string{http.MethodPost}},
		{"object-mapping-change-preview", "/api/cases/c/object-bindings/m/changes/preview", "core", []string{http.MethodPost}},
		{"object-mapping-change-proposal", "/api/cases/c/object-bindings/m/changes", "core", []string{http.MethodPost}},
		{"object-mapping-change-detail", "/api/cases/c/object-binding-changes/r", "core", []string{http.MethodGet}},
		{"object-mapping-change-decision", "/api/cases/c/object-binding-changes/r/decision", "core", []string{http.MethodPost}},
		{"service-object-list-create", "/api/cases/c/service-objects", "core", []string{http.MethodGet, http.MethodPost}},
		{"service-object-current-detail", "/api/cases/c/service-objects/o", "core", []string{http.MethodGet}},
		{"service-object-version-list", "/api/cases/c/service-objects/o/versions", "core", []string{http.MethodGet}},
		{"service-object-version-detail", "/api/cases/c/service-objects/o/versions/1", "core", []string{http.MethodGet}},
		{"service-object-version-detail-openapi-template", "/api/cases/{caseID}/service-objects/{objectID}/versions/{version}", "core", []string{http.MethodGet}},
		{"service-object-association-list-and-confirm", "/api/cases/c/service-objects/o/associations", "core", []string{http.MethodGet, http.MethodPost}},
		{"service-object-association-detail", "/api/cases/c/service-objects/o/associations/1", "core", []string{http.MethodGet}},
		{"service-object-association-detail-openapi-template", "/api/cases/{caseID}/service-objects/{objectID}/associations/{associationVersion}", "core", []string{http.MethodGet}},
		{"service-object-association-candidates", "/api/cases/c/service-objects/o/association-candidates", "core", []string{http.MethodGet}},
		{"external-job", "/api/cases/c/external-collection-jobs", "core", []string{http.MethodPost}},
		{"external-job-resume", "/api/cases/c/external-collection-jobs/col_0123456789abcdef", "core", []string{http.MethodGet}},
		{"external-job-resume-openapi-template", "/api/cases/{caseID}/external-collection-jobs/{collectionID}", "core", []string{http.MethodGet}},
		{"external-snapshot", "/api/cases/c/external-snapshots", "core", []string{http.MethodGet}},
		{"fulfillment-create", "/api/cases/c/fulfillment-evaluations", "core", []string{http.MethodPost}},
		{"fulfillment-item", "/api/cases/c/fulfillment-evaluations/e", "core", []string{http.MethodGet}},
		{"fulfillment-impact", "/api/cases/c/fulfillment-evaluations/e/impact", "core", []string{http.MethodGet}},
		{"generation", "/api/cases/c/generations", "document", []string{http.MethodPost}},
		{"review-create", "/api/cases/c/reviews", "execution", []string{http.MethodPost}},
		{"review-item", "/api/cases/c/reviews/r", "execution", []string{http.MethodGet}},
		{"review-events", "/api/cases/c/reviews/r/events", "execution", []string{http.MethodGet}},
		{"review-diagnostics", "/api/cases/c/reviews/r/diagnostics", "execution", []string{http.MethodGet}},
		{"review-retry", "/api/cases/c/reviews/r/retry", "execution", []string{http.MethodPost}},
		{"review-cancel", "/api/cases/c/reviews/r/cancel", "execution", []string{http.MethodPost}},
		{"review-decision", "/api/cases/c/reviews/r/findings/f/decisions", "core", []string{http.MethodPost}},
		{"member", "/api/cases/c/members", "core", []string{http.MethodPost}},
		{"archive", "/api/cases/c/archive", "core", []string{http.MethodPost}},
		{"remediation", "/api/cases/c/remediations", "core", []string{http.MethodPost}},
		{"remediation-submit", "/api/cases/c/remediations/r/submit", "core", []string{http.MethodPost}},
		{"remediation-verify", "/api/cases/c/remediations/r/verify", "core", []string{http.MethodPost}},
		{"report-create", "/api/cases/c/reports", "core", []string{http.MethodPost}},
		{"report-item", "/api/cases/c/reports/r", "core", []string{http.MethodGet}},
	}
	roles := []string{"core", "document", "execution"}
	methods := []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			allowed := make(map[string]bool, len(tc.methods))
			for _, method := range tc.methods {
				allowed[method] = true
			}
			allowed[http.MethodOptions] = true
			allowed[http.MethodHead] = allowed[http.MethodGet]
			for _, method := range methods {
				for _, role := range roles {
					want := role == tc.owner && allowed[method]
					if got := httpapi.OperationOwnedBy(role, method, tc.path); got != want {
						t.Errorf("OperationOwnedBy(%q, %q, %q) = %t, want %t", role, method, tc.path, got, want)
					}
				}
			}
		})
	}
}

func TestRouteSegmentsDecodesExactlyOnceAndRejectsAmbiguousPaths(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"/api/cases/case%2D1", []string{"api", "cases", "case-1"}},
		{"/api/cases/%E4%B8%AD%E6%96%87", []string{"api", "cases", "中文"}},
		{"/api/cases/%252F", []string{"api", "cases", "%2F"}},
		{"/api/cases/a%20b", []string{"api", "cases", "a b"}},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got, ok := httpapi.RouteSegments(tc.path)
			if !ok || len(got) != len(tc.want) {
				t.Fatalf("RouteSegments(%q) = %q, %t; want %q, true", tc.path, got, ok, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("RouteSegments(%q) = %q, want %q", tc.path, got, tc.want)
				}
			}
		})
	}
	for _, path := range []string{
		"api/cases/c", "/", "/api/", "/api//cases", "/api/./cases", "/api/../cases",
		"/api/%2e/cases", "/api/%2E%2E/cases", "/api/cases%2Fsecret", "/api/cases%5csecret",
		"/api/%", "/api/%GG", "/api/a%00b", "/api/a%1Fb", "/api/a%7Fb", "/api/cases/",
	} {
		t.Run("reject-"+path, func(t *testing.T) {
			if got, ok := httpapi.RouteSegments(path); ok || got != nil {
				t.Fatalf("RouteSegments(%q) = %q, %t; want nil, false", path, got, ok)
			}
		})
	}
}

func TestOperationOwnedByFailsClosedForUnknownRolesMethodsAndPathShapes(t *testing.T) {
	for _, tc := range []struct{ role, method, path string }{
		{"", http.MethodGet, "/api/cases/c"},
		{"root", http.MethodGet, "/api/cases/c"},
		{"core", http.MethodPatch, "/api/cases/c"},
		{"core", "TRACE", "/api/cases/c"},
		{"core", http.MethodGet, "/api/cases/c/unknown"},
		{"execution", http.MethodPost, "/api/cases/c/reviews/r/diagnostics"},
		{"document", http.MethodGet, "/api/cases/c/documents/d/original/extra"},
		{"core", http.MethodPost, "/api/cases/c/materials/selection"},
		{"document", http.MethodPut, "/api/cases/c/materials/selection"},
		{"document", http.MethodPost, "/api/cases/c/materials/unknown"},
		{"execution", http.MethodGet, "/api/cases/c/materials/m/versions/1/parsed-documents"},
		{"document", http.MethodPost, "/api/cases/c/materials/m/versions/1/parsed-documents"},
		{"core", http.MethodDelete, "/api/cases/c/service-objects"},
		{"core", http.MethodPost, "/api/cases/c/service-objects/o/versions"},
		{"core", http.MethodGet, "/api/cases/c/service-objects/o/versions/latest"},
		{"document", http.MethodGet, "/api/cases/c/service-objects/o"},
		{"execution", http.MethodGet, "/api/cases/c/service-objects/o/associations"},
		{"core", http.MethodPost, "/api/cases/c/service-objects/o/association-candidates"},
		{"core", http.MethodPatch, "/api/cases/c/service-objects/o/associations/1"},
		{"core", http.MethodGet, "/api/cases/c/object-bindings/m/changes"},
		{"core", http.MethodDelete, "/api/cases/c/object-bindings/m/changes/preview"},
		{"core", http.MethodGet, "/api/cases/c/object-binding-changes/r/decision"},
		{"document", http.MethodPost, "/api/cases/c/object-binding-changes/r/decision"},
		{"execution", http.MethodGet, "/api/cases/c/runs/r/attempts/a/legal-citations/not-a-digest"},
		{"core", http.MethodGet, "/api/cases/c/external-collection-jobs/raw-key"},
		{"execution", http.MethodGet, "/api/cases/{caseID}/runs/{runID}/attempts/{attemptID}/legal-citations/{citationID}/extra"},
		{"execution", http.MethodGet, "/api/cases/{caseID}/runs/{runID}/attempts/{attemptID}/legal-citations/{otherID}"},
		{"core", http.MethodGet, "/api/cases/c/runs/r/attempts/a/legal-citations/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"core", http.MethodGet, "/api/%2563ases/c"},
		{"core", http.MethodGet, "/api/cases/c/"},
	} {
		if httpapi.OperationOwnedBy(tc.role, tc.method, tc.path) {
			t.Errorf("OperationOwnedBy(%q, %q, %q) unexpectedly allowed", tc.role, tc.method, tc.path)
		}
	}
}

func TestServiceHandlerValidatesConfigurationAndScopesRequests(t *testing.T) {
	credential := strings.Repeat("g", 40)
	worker := strings.Repeat("w", 40)
	for _, tc := range []struct {
		name       string
		role       string
		credential string
		options    httpapi.Options
		wantError  bool
	}{
		{"unknown role", "unknown", credential, httpapi.Options{Durable: true}, true},
		{"short credential", "core", "short", httpapi.Options{Durable: true}, true},
		{"minimum credential length", "core", strings.Repeat("m", 32), httpapi.Options{Durable: true}, false},
		{"non durable", "core", credential, httpapi.Options{}, true},
		{"short worker credential", "core", credential, httpapi.Options{Durable: true, ExperienceWorkerCredential: "short"}, true},
		{"duplicate worker credential", "core", credential, httpapi.Options{Durable: true, ExperienceWorkerCredential: credential}, true},
		{"document role ignores worker delivery", "document", credential, httpapi.Options{Durable: true, ExperienceWorkerCredential: "short"}, false},
		{"distinct worker credential", "core", credential, httpapi.Options{Durable: true, ExperienceWorkerCredential: worker}, false},
		{"minimum worker credential length", "core", credential, httpapi.Options{Durable: true, ExperienceWorkerCredential: strings.Repeat("w", 32)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := httpapi.ServiceHandler(nil, tc.options, tc.role, tc.credential)
			if (err != nil) != tc.wantError {
				t.Fatalf("ServiceHandler error = %v, wantError %t", err, tc.wantError)
			}
			if err != nil {
				return
			}
			if handler == nil {
				t.Fatal("ServiceHandler returned a nil handler")
			}
			if tc.role != "core" || tc.name != "distinct worker credential" {
				return
			}
			for _, request := range []struct {
				path, method, key string
				wantStatus        int
			}{
				{"/api/cases/c", http.MethodGet, "", http.StatusUnauthorized},
				{"/api/templates", http.MethodGet, "", http.StatusNotFound},
				{"/not-owned", http.MethodGet, "", http.StatusNotFound},
				{"/openapi.json", http.MethodGet, credential, http.StatusOK},
				{"/internal/v1/experience-results", http.MethodPost, worker, http.StatusUnprocessableEntity},
				{"/internal/v1/experience-results", http.MethodPost, credential, http.StatusUnauthorized},
			} {
				req := httptest.NewRequest(request.method, request.path, nil)
				if request.key != "" {
					req.Header.Set("X-Contract-Service-Key", request.key)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != request.wantStatus {
					t.Errorf("%s %s status = %d, want %d", request.method, request.path, rec.Code, request.wantStatus)
				}
			}
		})
	}
}
