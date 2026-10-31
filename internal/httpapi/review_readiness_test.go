package httpapi_test

import (
	"net/http"
	"testing"
)

func TestReviewReadinessListsCurrentMaterialsAndPreservesLegacyTimePolicy(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "准备清单验收", "contract_type": "procurement_ict",
	})
	if status != http.StatusCreated {
		t.Fatalf("create case: status=%d body=%v", status, created)
	}
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "采购文件.txt", "text/plain", []byte("采购文件原文"))
	api.upload(t, caseID, "draft_contract", "拟签合同.txt", "text/plain", []byte("拟签合同原文"))

	status, readiness := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/review-readiness", nil)
	if status != http.StatusOK {
		t.Fatalf("read readiness without a business instant: status=%d body=%v", status, readiness)
	}
	if _, exists := readiness["business_at"]; exists {
		t.Fatalf("server substituted a business time that the user did not provide: %+v", readiness)
	}
	if readiness["case_id"] != caseID || readiness["can_start"] != true {
		t.Fatalf("legacy readiness must preserve admission without a business instant: %+v", readiness)
	}
	materials, ok := readiness["materials"].([]any)
	if !ok || len(materials) != 2 {
		t.Fatalf("readiness did not return the current two-material collection: %+v", readiness["materials"])
	}
	scopes, ok := readiness["scopes"].([]any)
	if !ok || len(scopes) != 1 {
		t.Fatalf("only the supported full-review scope should be returned: %+v", readiness["scopes"])
	}
	scope, ok := scopes[0].(map[string]any)
	if !ok || scope["id"] != "full_review" || scope["can_start"] != true {
		t.Fatalf("full-review scope must preserve legacy business-time policy: %+v", scopes[0])
	}
	blockers, ok := scope["blockers"].([]any)
	if !ok || len(blockers) != 0 {
		t.Fatalf("legacy case without objects must not acquire new blockers: %+v", scope["blockers"])
	}

	status, ready := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/review-readiness?business_at=2026-10-31T10:00:00Z", nil)
	if status != http.StatusOK || ready["can_start"] != true {
		t.Fatalf("existing ungoverned two-material flow should remain startable with an explicit business time: status=%d body=%+v", status, ready)
	}
	status, member := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/members", map[string]any{"actor_id": "demo-viewer"})
	if status != http.StatusOK || member["id"] != "demo-viewer" {
		t.Fatalf("add a read-only case member: status=%d body=%+v", status, member)
	}
	api.login(t, "demo-viewer", "viewer-demo")
	status, viewerReadiness := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/review-readiness?business_at=2026-10-31T10:00:00Z", nil)
	if status != http.StatusOK || viewerReadiness["can_start"] != false {
		t.Fatalf("viewer should read readiness but must not receive start permission: status=%d body=%+v", status, viewerReadiness)
	}
	viewerScopes, ok := viewerReadiness["scopes"].([]any)
	if !ok || len(viewerScopes) != 1 {
		t.Fatalf("viewer lost the readiness scope: %+v", viewerReadiness["scopes"])
	}
	viewerScope, ok := viewerScopes[0].(map[string]any)
	viewerBlockers, blockersOK := viewerScope["blockers"].([]any)
	if !ok || !blockersOK || viewerScope["can_start"] != false || !readinessHasBlocker(viewerBlockers, "review_start_role_required") {
		t.Fatalf("viewer readiness must explain the start-role boundary: %+v", viewerScopes[0])
	}
}

func readinessHasBlocker(blockers []any, code string) bool {
	for _, item := range blockers {
		blocker, ok := item.(map[string]any)
		if ok && blocker["code"] == code {
			return true
		}
	}
	return false
}
