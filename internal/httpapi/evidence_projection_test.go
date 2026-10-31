package httpapi_test

import (
	"net/http"
	"testing"
	"time"
)

// A governed compatible result can close the request when its canonical fact
// cites the submitted frozen document. The public response must not expose the
// technical citation metadata used internally to prove that linkage.
func TestEvidenceHTTPCompatibleRulepackCitation(t *testing.T) {
	t.Setenv("AGENT_PLANNER_MODE", "deterministic")
	api, _, coreURL := newExperienceAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	publishExperiencePolicy(t, api)
	legacyURL := api.base
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "带宽材料核验", "contract_type": "service", "business_line": "ENT", "scenario": "telecom_dedicated_line",
	})
	if status != http.StatusCreated {
		t.Fatalf("create governed case: %d %v", status, created)
	}
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("政企专线采购依据\n电路 LINE-001：最低保障带宽 100 Mbit/s。"))
	initialDraftID := api.upload(t, caseID, "draft_contract", "initial.txt", "text/plain", []byte("拟签合同尚未写明电路带宽。"))
	status, source := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{
		"business_at": "2026-02-03T01:05:06.123456789Z",
	})
	if status != http.StatusCreated {
		t.Fatalf("start governed source review: %d %v", status, source)
	}
	sourceRun, sourceAttempt := source["id"].(string), source["attempt_id"].(string)
	status, sourceWorkspace := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+sourceRun, nil)
	if status != http.StatusOK {
		t.Fatalf("read governed source review: %d %v", status, sourceWorkspace)
	}
	missing := evaluationForRule(t, sourceWorkspace, "ENT-TECH-BW-001")
	if missing["relation"] != "insufficient" || missing["reason_code"] != "required_fact_missing" {
		t.Fatalf("source bandwidth check did not identify the missing draft fact: %v", missing)
	}
	status, task := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/tasks", map[string]any{
		"title": "补充最低带宽", "kind": "review", "assignee_id": "demo-admin",
		"due_at": time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339), "status": "open", "priority": "high",
	})
	if status != http.StatusCreated {
		t.Fatalf("create task: %d %v", status, task)
	}
	api.base = coreURL
	base := "/api/cases/" + caseID + "/evidence-requests"
	status, request := api.send(t, http.MethodPost, base, map[string]any{
		"task_id": task["id"], "task_version": task["version"], "source_run_id": sourceRun,
		"source_attempt_id": sourceAttempt, "source_evaluation_id": missing["id"],
		"question": "请提供电路的最低保障带宽", "material_role": "draft_contract", "verification_condition": "compatible",
	}, "governed-request")
	if status != http.StatusCreated {
		t.Fatalf("create governed request: %d %v", status, request)
	}
	assertWireSchema(t, openAPI(t), "EvidenceRequest", request)
	assertNoEvidenceDiagnostics(t, request)
	if _, leaked := request["source_evaluation"].(map[string]any)["evidence"]; leaked {
		t.Fatalf("source evaluation exposed canonical citation metadata: %v", request)
	}
	requestPath := base + "/" + request["id"].(string)
	materialID, currentDocumentID := api.materialForCurrentDocument(t, caseID, initialDraftID)
	api.base = legacyURL
	status, otherTime := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{
		"business_at": "2026-03-01T04:00:00Z",
	})
	if status != http.StatusCreated {
		t.Fatalf("start independent review at another business time: %d %v", status, otherTime)
	}
	status, sourceWorkspace = api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+sourceRun+"?attempt_id="+sourceAttempt, nil)
	if status != http.StatusOK {
		t.Fatalf("reopen the source attempt after another review: %d %v", status, sourceWorkspace)
	}
	frozenBusinessAt, ok := sourceWorkspace["snapshot"].(map[string]any)["business_at"].(string)
	if !ok || frozenBusinessAt == "" {
		t.Fatalf("source attempt did not provide its frozen business time: %v", sourceWorkspace["snapshot"])
	}
	documentID := api.replaceUpload(t, caseID, "draft_contract", "supplement.txt", "text/plain", []byte("拟签合同\n电路 LINE-001：最低保障带宽 100 Mbit/s。"), materialID, currentDocumentID)
	api.base = coreURL
	status, submitted := api.send(t, http.MethodPost, requestPath+"/receipts", map[string]any{
		"document_id": documentID, "expected_version": 1,
	}, "governed-receipt")
	if status != http.StatusCreated || submitted["request"].(map[string]any)["status"] != "received" {
		t.Fatalf("submit governed receipt: %d %v", status, submitted)
	}
	receiptID := submitted["receipt"].(map[string]any)["id"].(string)
	api.base = legacyURL
	status, later := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{
		"business_at": frozenBusinessAt,
	})
	if status != http.StatusCreated || later["attempt_id"] == sourceAttempt {
		t.Fatalf("start later governed review: %d %v", status, later)
	}
	laterRun := later["id"].(string)
	status, workspace := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+laterRun, nil)
	if status != http.StatusOK {
		t.Fatalf("read later governed review: %d %v", status, workspace)
	}
	observed := evaluationForRule(t, workspace, "ENT-TECH-BW-001")
	if observed["relation"] != "compatible" {
		t.Fatalf("later bandwidth check is not compatible: %v", observed)
	}
	var document map[string]any
	for _, raw := range workspace["documents"].([]any) {
		candidate := raw.(map[string]any)
		if candidate["id"] == documentID {
			document = candidate
			break
		}
	}
	if document == nil || document["parsed_document_id"] == nil || document["text"] == nil {
		t.Fatalf("later review did not include the submitted frozen document: %v", workspace["documents"])
	}
	text := document["text"].(string)
	api.base = coreURL
	status, verified := api.send(t, http.MethodPost, requestPath+"/receipts/"+receiptID+"/verify", map[string]any{
		"run_id": laterRun, "attempt_id": later["attempt_id"], "evaluation_id": observed["id"],
		"evidence": []any{map[string]any{"document_id": documentID, "parsed_document_id": document["parsed_document_id"],
			"quote": text, "start": 0, "end": len([]rune(text))}},
		"reason": "已核对材料及带宽检查", "expected_version": 2,
	}, "governed-verify")
	if status != http.StatusOK || verified["status"] != "verified" || verified["version"] != float64(3) {
		t.Fatalf("compatible result with cited receipt did not verify: %d %v", status, verified)
	}
	verification := verified["verification"].(map[string]any)
	if verification["relation"] != "compatible" || verification["receipt_id"] != receiptID || verification["evaluation_id"] != observed["id"] {
		t.Fatalf("verification omitted its actual result or receipt: %v", verification)
	}
	assertWireSchema(t, openAPI(t), "EvidenceRequest", verified)
	assertNoEvidenceDiagnostics(t, verified)
	status, history := api.do(t, http.MethodGet, requestPath+"/history", nil)
	if status != http.StatusOK || len(history["items"].([]any)) != 3 {
		t.Fatalf("governed request history: %d %v", status, history)
	}
	assertWireSchema(t, openAPI(t), "EvidenceRequestHistory", history)
	assertNoEvidenceDiagnostics(t, history)
}
