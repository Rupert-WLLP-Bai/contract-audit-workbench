package httpapi_test

import (
	"net/http"
	"testing"
)

func TestWorkbenchRoutesExposeCASCreatorsRelationsAndPlatformAudit(t *testing.T) {
	api := newAPI(t)
	actor := api.login(t, "demo-admin", "admin-demo")
	actorID := actor["id"].(string)

	status, firstCase := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "工作台HTTP案件", "contract_type": "procurement_ict"})
	if status != http.StatusCreated {
		t.Fatalf("create case: status=%d body=%v", status, firstCase)
	}
	caseID := firstCase["id"].(string)
	status, profile := api.do(t, http.MethodPut, "/api/cases/"+caseID+"/profile", map[string]any{
		"number": "WB-HTTP-001", "customer": "采购方", "supplier": "服务商", "contract_category": "云服务",
		"owner_id": actorID, "due_at": nil, "expected_version": 0,
	})
	if status != http.StatusOK || profile["version"] != float64(1) {
		t.Fatalf("create profile: status=%d body=%v", status, profile)
	}
	status, task := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/tasks", map[string]any{
		"title": "复核采购制度", "kind": "review", "assignee_id": actorID, "due_at": nil,
		"status": "open", "priority": "high", "finding_id": nil,
	})
	if status != http.StatusCreated || task["created_by"] != actorID || task["version"] != float64(1) {
		t.Fatalf("create task: status=%d body=%v", status, task)
	}
	taskID := task["id"].(string)
	status, updated := api.do(t, http.MethodPut, "/api/cases/"+caseID+"/tasks/"+taskID, map[string]any{
		"title": "复核采购制度", "kind": "review", "assignee_id": actorID, "due_at": nil,
		"status": "in_progress", "priority": "high", "finding_id": nil, "expected_version": 1,
	})
	if status != http.StatusOK || updated["version"] != float64(2) || updated["created_by"] != actorID {
		t.Fatalf("update task: status=%d body=%v", status, updated)
	}
	status, stale := api.do(t, http.MethodPut, "/api/cases/"+caseID+"/tasks/"+taskID, map[string]any{
		"title": "过期更新", "kind": "review", "assignee_id": actorID, "due_at": nil,
		"status": "completed", "priority": "high", "finding_id": nil, "expected_version": 1,
	})
	if status != http.StatusConflict {
		t.Fatalf("stale task update status=%d body=%v", status, stale)
	}

	status, secondCase := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "关联HTTP案件", "contract_type": "procurement_ict"})
	if status != http.StatusCreated {
		t.Fatalf("create related case: status=%d body=%v", status, secondCase)
	}
	status, relation := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/relations", map[string]any{
		"related_case_id": secondCase["id"], "reason": "同一采购项目",
	})
	if status != http.StatusCreated {
		t.Fatalf("create relation: status=%d body=%v", status, relation)
	}

	status, knowledge := api.do(t, http.MethodPost, "/api/knowledge", map[string]any{
		"id": "http-cloud-policy", "title": "云服务制度", "version": "1.0.0", "body": "合成制度正文。",
		"source_type": "synthetic_policy", "effective_at": nil,
	})
	if status != http.StatusCreated {
		t.Fatalf("create knowledge: status=%d body=%v", status, knowledge)
	}
	status, readKnowledge := api.do(t, http.MethodGet, "/api/knowledge/http-cloud-policy", nil)
	if status != http.StatusOK || readKnowledge["body"] != knowledge["body"] {
		t.Fatalf("read knowledge: status=%d body=%v", status, readKnowledge)
	}

	status, snapshot := api.do(t, http.MethodGet, "/api/workbench?period=week", nil)
	if status != http.StatusOK || len(snapshot["profiles"].([]any)) != 1 || len(snapshot["tasks"].([]any)) != 1 ||
		len(snapshot["relations"].([]any)) != 1 || len(snapshot["knowledge"].([]any)) != 1 {
		t.Fatalf("workbench snapshot: status=%d body=%v", status, snapshot)
	}
	platformAuditFound := false
	for _, raw := range snapshot["audit"].([]any) {
		event := raw.(map[string]any)
		if event["action"] == "knowledge.created" {
			platformAuditFound = true
			if event["case_id"] != nil || event["case_title"] != nil {
				t.Fatalf("platform audit fabricated a case link: %v", event)
			}
		}
	}
	if !platformAuditFound {
		t.Fatal("knowledge audit missing from workbench snapshot")
	}
}
