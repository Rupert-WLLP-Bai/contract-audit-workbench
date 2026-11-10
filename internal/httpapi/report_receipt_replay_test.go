// File metadata (manifest mirror):
// Purpose: Verifies report creation idempotency through the authenticated HTTP route across logout and login.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-10
// Updated: 2026-11-10
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"net/http"
	"reflect"
	"testing"
)

func TestReportReceiptReplayAfterLoginHTTP(t *testing.T) {
	api := newAPI(t)
	publishTelecomBusinessPolicy(t, api)
	actorA := api.login(t, "demo-admin", "admin-demo")
	dataset := readTelecomDataset(t)
	conflict := telecomScenario(t, dataset, "conflict")
	templateID := requireTelecomTemplate(t, api, dataset.Template, dataset.TemplateVersion)
	caseID := createTelecomCase(t, api, "跨登录报告回执 HTTP 检查")
	generateTelecomDraft(t, api, caseID, templateID, conflict.Fields)
	for _, material := range conflict.Materials {
		api.upload(t, caseID, material.Role, material.Filename, "text/plain", []byte(material.Text))
	}
	runID, attemptID, _ := startTelecomReview(t, api, caseID)
	const key = "report-http-lost-response"
	path := "/api/cases/" + caseID + "/reports"
	status, original := api.send(t, http.MethodPost, path, map[string]any{"run_id": runID, "attempt_id": attemptID}, key)
	if status != http.StatusCreated {
		t.Fatalf("create report over HTTP: status=%d body=%v", status, original)
	}
	originalJSON := readHTTPBody(t, api.client, api.base, http.MethodGet, path+"/"+requiredString(t, original, "id")+"?format=json", nil)
	originalHTML := readHTTPBody(t, api.client, api.base, http.MethodGet, path+"/"+requiredString(t, original, "id")+"?format=html", nil)
	if status, _ := api.do(t, http.MethodDelete, "/api/session", nil); status != http.StatusNoContent {
		t.Fatalf("logout session A: status=%d", status)
	}
	actorB := api.login(t, "demo-admin", "admin-demo")
	if actorA["id"] != actorB["id"] || actorA["tenant_id"] != actorB["tenant_id"] || actorA["org_id"] != actorB["org_id"] {
		t.Fatalf("session B did not resolve the original user context: A=%v B=%v", actorA, actorB)
	}
	status, replay := api.send(t, http.MethodPost, path, map[string]any{"run_id": runID, "attempt_id": attemptID}, key)
	if status != http.StatusCreated || !reflect.DeepEqual(replay, original) {
		t.Fatalf("same-key HTTP replay must return original report: status=%d original=%v replay=%v", status, original, replay)
	}
	if again := readHTTPBody(t, api.client, api.base, http.MethodGet, path+"/"+requiredString(t, original, "id")+"?format=json", nil); string(again.body) != string(originalJSON.body) {
		t.Fatal("HTTP replay changed original JSON report bytes")
	}
	if again := readHTTPBody(t, api.client, api.base, http.MethodGet, path+"/"+requiredString(t, original, "id")+"?format=html", nil); string(again.body) != string(originalHTML.body) {
		t.Fatal("HTTP replay changed original HTML report bytes")
	}
	status, conflictResponse := api.send(t, http.MethodPost, path, map[string]any{"run_id": runID, "attempt_id": attemptID + "-different"}, key)
	if status != http.StatusConflict {
		t.Fatalf("same key with changed attempt must conflict over HTTP: status=%d body=%v", status, conflictResponse)
	}
}
