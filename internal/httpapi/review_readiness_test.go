// File metadata (manifest mirror):
// Purpose: Checks the readiness API and single-contract precheck admission, verifying independent payment evidence reaches coverage without creating findings.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-19
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
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
	if !ok || len(scopes) != 2 {
		t.Fatalf("both published review scopes should be returned: %+v", readiness["scopes"])
	}
	scope, ok := scopes[0].(map[string]any)
	if !ok || scope["id"] != "full_review" || scope["can_start"] != true {
		t.Fatalf("full-review scope must preserve legacy business-time policy: %+v", scopes[0])
	}
	blockers, ok := scope["blockers"].([]any)
	if !ok || len(blockers) != 0 {
		t.Fatalf("legacy case without objects must not acquire new blockers: %+v", scope["blockers"])
	}
	single, ok := scopes[1].(map[string]any)
	if !ok || single["id"] != "single_contract_precheck" || single["can_start"] != true || single["rule_set"] == nil {
		t.Fatalf("single-contract scope is not startable with its published mapping: %+v", scopes[1])
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
	if !ok || len(viewerScopes) != 2 {
		t.Fatalf("viewer lost the readiness scope: %+v", viewerReadiness["scopes"])
	}
	viewerScope, ok := viewerScopes[0].(map[string]any)
	viewerBlockers, blockersOK := viewerScope["blockers"].([]any)
	if !ok || !blockersOK || viewerScope["can_start"] != false || !readinessHasBlocker(viewerBlockers, "review_start_role_required") {
		t.Fatalf("viewer readiness must explain the start-role boundary: %+v", viewerScopes[0])
	}
	viewerSingle, ok := viewerScopes[1].(map[string]any)
	viewerSingleBlockers, blockersOK := viewerSingle["blockers"].([]any)
	if !ok || !blockersOK || viewerSingle["can_start"] != false || !readinessHasBlocker(viewerSingleBlockers, "review_start_role_required") {
		t.Fatalf("single-contract readiness must preserve the start-role boundary: %+v", viewerScopes[1])
	}
}

func TestSingleContractV2ReviewPresentsExactEvidenceAsCoverage(t *testing.T) {
	api := newAPIWith(t, nil, nil)
	api.login(t, "demo-admin", "admin-demo")
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "合成付款覆盖", "contract_type": "service",
	})
	if status != http.StatusCreated {
		t.Fatalf("create status %d", status)
	}
	casePath := "/api/cases/" + created["id"].(string)
	primary := api.upload(t, created["id"].(string), "draft_contract", "synthetic.txt", "text/plain",
		[]byte("😀费用按实际使用量结算。收到发票后十个工作日内支付。首期款比例为20%。尾款比例为80%。税率6%。"))
	status, started := api.do(t, http.MethodPost, casePath+"/reviews", map[string]any{"scope": "single_contract_precheck"})
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("precheck admission status %d: %v", status, started)
	}
	deadline := time.Now().Add(8 * time.Second)
	var ws map[string]any
	for time.Now().Before(deadline) {
		status, ws = api.do(t, http.MethodGet, casePath+"/reviews/"+started["id"].(string), nil)
		if status != http.StatusOK {
			t.Fatalf("workspace status %d", status)
		}
		state := ws["run"].(map[string]any)["state"]
		if state == "partial" {
			break
		}
		if state == "failed" || state == "interrupted" {
			t.Fatalf("precheck failed before producing coverage: %v", state)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if ws == nil || ws["run"].(map[string]any)["state"] != "partial" {
		t.Fatal("limited precheck did not finish")
	}
	if len(ws["findings"].([]any)) != 0 {
		t.Fatal("coverage observations entered the risk queue")
	}
	evaluations := ws["evaluations"].([]any)
	if len(evaluations) != 1 {
		t.Fatalf("coverage evaluation count %d", len(evaluations))
	}
	evaluation := evaluations[0].(map[string]any)
	if evaluation["rule_id"] != "single-payment-terms-v2" || evaluation["reason_code"] != "payment_terms_observed" ||
		evaluation["finding_id"] != nil || strings.Contains(evaluation["reason"].(string), "合计") {
		t.Fatal("coverage identity or classification was not preserved")
	}
	evidence := evaluation["evidence"].([]any)
	if len(evidence) != 4 {
		t.Fatalf("separate source observations were not preserved: %d", len(evidence))
	}
	for _, item := range evidence {
		anchor := item.(map[string]any)
		if anchor["document_version_id"] != primary || anchor["status"] != "located" || len(anchor["observation_labels"].([]any)) == 0 {
			t.Fatal("missing exact source coordinates or observation labels")
		}
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
