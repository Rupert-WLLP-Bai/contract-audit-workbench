package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type telecomScenarioFixture struct {
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	ExpectedRelations map[string]string `json:"expected_relations"`
	Fields            map[string]string `json:"fields"`
	Materials         []struct {
		Role     string `json:"role"`
		Filename string `json:"filename"`
		Text     string `json:"text"`
	} `json:"materials"`
	DraftOverride *struct {
		Filename string `json:"filename"`
		Text     string `json:"text"`
	} `json:"draft_override"`
}

type telecomDatasetFixture struct {
	DatasetID       string                   `json:"dataset_id"`
	Template        string                   `json:"template_id"`
	TemplateVersion string                   `json:"template_version"`
	Scenarios       []telecomScenarioFixture `json:"scenarios"`
}

func TestTelecomBusinessHTTPWorkflowFreezesReportsAndRejectsInsufficientClosure(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	dataset := readTelecomDataset(t)
	if dataset.DatasetID != "telecom-c1b-v1" || dataset.Template != "ent_telecom_dedicated_line_v1" || dataset.TemplateVersion != "1.0.0" {
		t.Fatalf("unexpected frozen telecom fixture identity: %+v", dataset)
	}
	compatible := telecomScenario(t, dataset, "compatible")
	conflict := telecomScenario(t, dataset, "conflict")
	insufficient := telecomScenario(t, dataset, "insufficient")
	templateID := requireTelecomTemplate(t, api, dataset.Template, dataset.TemplateVersion)

	caseID := createTelecomCase(t, api, "C1-B HTTP business workflow")
	scopeStatus, scope := api.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	if scopeStatus != http.StatusOK || scope["business_line"] != "ENT" || scope["scenario"] != "telecom_dedicated_line" {
		t.Fatalf("case business scope: status=%d detail=%v", scopeStatus, scope)
	}
	generation := generateTelecomDraft(t, api, caseID, templateID, conflict.Fields)
	requiredString(t, generation, "document_id")
	requiredString(t, generation, "digest")
	for _, material := range conflict.Materials {
		api.upload(t, caseID, material.Role, material.Filename, "text/plain", []byte(material.Text))
	}

	firstRunID, firstAttemptID, firstWorkspace := startTelecomReview(t, api, caseID)
	assertTelecomRelations(t, firstWorkspace, conflict.ExpectedRelations)
	bandwidth := evaluationForRule(t, firstWorkspace, "ENT-TECH-BW-001")
	if bandwidth["capability_id"] != "dedicated_line.minimum_bandwidth" {
		t.Fatalf("bandwidth evaluation lost its capability identity: %+v", bandwidth)
	}
	if got := stringSlice(t, bandwidth["object_refs"]); len(got) != 1 || got[0] != "LINE-001" {
		t.Fatalf("bandwidth evaluation object refs = %v", got)
	}
	findingID := requiredString(t, bandwidth, "finding_id")
	finding := findingForID(t, firstWorkspace, findingID)
	status, decision := api.do(t, http.MethodPost,
		fmt.Sprintf("/api/cases/%s/reviews/%s/findings/%s/decisions", caseID, firstRunID, findingID),
		map[string]any{
			"action": "confirm_risk", "reason": "采购和拟签合同的最低保障带宽数值不同。",
			"expected_version": int(finding["version"].(float64)),
		})
	if status != http.StatusNoContent {
		t.Fatalf("record human decision: status=%d body=%v", status, decision)
	}

	status, remediation := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/remediations", map[string]any{
		"run_id":      firstRunID,
		"attempt_id":  firstAttemptID,
		"finding_ids": []string{findingID},
		"assignee_id": "demo-admin",
		"due_at":      "2027-01-01T00:00:00Z",
		"measure":     "将修改稿最低保障带宽修订为采购要求，并按同一口径复核。",
	})
	if status != http.StatusCreated || remediation["status"] != "open" || remediation["version"] != float64(1) {
		t.Fatalf("create remediation: status=%d body=%v", status, remediation)
	}
	itemID := requiredString(t, remediation, "id")

	correctedDraft := "拟签合同\n" +
		"电路 LINE-001：最低保障带宽 100 Mbit/s。\n" +
		"电路 LINE-001：SLA 可用率 99.9%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。\n" +
		"电路 LINE-001：预付款比例 20%，预付款以合同总额为基础，含税。\n" +
		"电路 LINE-001：合同总额 100000 元，含税。"
	correctedID := api.upload(t, caseID, "draft_contract", "telecom-revised-draft.txt", "text/plain", []byte(correctedDraft))
	status, submitted := api.do(t, http.MethodPost,
		fmt.Sprintf("/api/cases/%s/remediations/%s/submit", caseID, itemID),
		map[string]any{
			"document_ids":     []string{correctedID},
			"reason":           "按采购条件修订带宽、SLA 和预付款条款。",
			"expected_version": 1,
		})
	if status != http.StatusOK || submitted["status"] != "submitted" || submitted["version"] != float64(2) {
		t.Fatalf("submit remediation: status=%d body=%v", status, submitted)
	}

	secondRunID, secondAttemptID, secondWorkspace := startTelecomReview(t, api, caseID)
	if secondRunID == firstRunID || secondAttemptID == firstAttemptID {
		t.Fatalf("modified draft reused frozen review identities: first=%s/%s second=%s/%s", firstRunID, firstAttemptID, secondRunID, secondAttemptID)
	}
	assertTelecomRelations(t, secondWorkspace, compatible.ExpectedRelations)
	if secondWorkspace["previous_run_id"] != firstRunID || secondWorkspace["revision_comparison_reason"] != nil {
		t.Fatalf("committed RulePack attempts were not comparable: previous=%v reason=%v", secondWorkspace["previous_run_id"], secondWorkspace["revision_comparison_reason"])
	}
	for ruleID := range conflict.ExpectedRelations {
		before := evaluationForRule(t, firstWorkspace, ruleID)
		previousID := requiredString(t, before, "finding_id")
		var revision map[string]any
		for _, value := range secondWorkspace["revisions"].([]any) {
			row := value.(map[string]any)
			if row["previous_finding_id"] == previousID {
				if revision != nil {
					t.Fatalf("duplicate revision for prior finding %s", previousID)
				}
				revision = row
			}
		}
		if revision == nil || revision["mark"] != "消除" || revision["relation"] != "compatible" || revision["finding_id"] != nil {
			t.Fatalf("compatible %s did not resolve its original risk: %v", ruleID, revision)
		}
		if previousID == findingID {
			prior, ok := revision["prior_decision"].(map[string]any)
			if !ok || prior["action"] != "confirm_risk" || prior["reason"] != "采购和拟签合同的最低保障带宽数值不同。" {
				t.Fatalf("revision lost the original human decision: %v", revision)
			}
		}
	}
	status, verified := api.do(t, http.MethodPost,
		fmt.Sprintf("/api/cases/%s/remediations/%s/verify", caseID, itemID),
		map[string]any{
			"run_id": secondRunID, "attempt_id": secondAttemptID,
			"reason": "新运行在相同电路和规则能力下确认修改稿满足要求。", "expected_version": 2,
		})
	if status != http.StatusOK || verified["status"] != "closed" || verified["version"] != float64(3) {
		t.Fatalf("verify compatible remediation: status=%d body=%v", status, verified)
	}

	firstReport := createTelecomReport(t, api, caseID, firstRunID, firstAttemptID)
	firstJSONPath := fmt.Sprintf("/api/cases/%s/reports/%s?format=json", caseID, firstReport["id"])
	firstHTMLPath := fmt.Sprintf("/api/cases/%s/reports/%s?format=html", caseID, firstReport["id"])
	firstJSON := readHTTPBody(t, api.client, api.base, http.MethodGet, firstJSONPath, nil)
	firstHTML := readHTTPBody(t, api.client, api.base, http.MethodGet, firstHTMLPath, nil)
	if !json.Valid(firstJSON.body) || !strings.Contains(strings.ToLower(string(firstHTML.body)), "<!doctype html>") {
		t.Fatalf("reports are not frozen JSON/HTML bytes: json=%q html=%q", firstJSON.body, firstHTML.body)
	}
	if !strings.Contains(firstJSON.header.Get("Content-Type"), "application/json") || !strings.Contains(firstHTML.header.Get("Content-Type"), "text/html") {
		t.Fatalf("report media types: JSON=%q HTML=%q", firstJSON.header.Get("Content-Type"), firstHTML.header.Get("Content-Type"))
	}
	if got := firstHTML.header.Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") || !strings.Contains(got, "sandbox") {
		t.Fatalf("HTML export CSP = %q", got)
	}
	secondReport := createTelecomReport(t, api, caseID, secondRunID, secondAttemptID)
	secondJSON := readHTTPBody(t, api.client, api.base, http.MethodGet,
		fmt.Sprintf("/api/cases/%s/reports/%s?format=json", caseID, secondReport["id"]), nil)
	if !json.Valid(secondJSON.body) {
		t.Fatalf("latest report is not JSON: %q", secondJSON.body)
	}
	if again := readHTTPBody(t, api.client, api.base, http.MethodGet, firstJSONPath, nil); !bytes.Equal(again.body, firstJSON.body) {
		t.Fatal("creating a later report changed the older frozen JSON bytes")
	}
	if again := readHTTPBody(t, api.client, api.base, http.MethodGet, firstHTMLPath, nil); !bytes.Equal(again.body, firstHTML.body) {
		t.Fatal("creating a later report changed the older frozen HTML bytes")
	}

	status, archived := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/archive", map[string]any{
		"report_id": secondReport["id"], "reason": "本次复审和处置记录已冻结归档。",
	})
	if status != http.StatusOK || archived["report_id"] != secondReport["id"] {
		t.Fatalf("archive case: status=%d body=%v", status, archived)
	}
	if afterArchive := readHTTPBody(t, api.client, api.base, http.MethodGet, firstJSONPath, nil); !bytes.Equal(afterArchive.body, firstJSON.body) {
		t.Fatal("archiving changed the older frozen report bytes")
	}
	status, rejected := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/generations", map[string]any{
		"template_id": templateID, "fields": conflict.Fields,
	})
	if status != http.StatusConflict {
		t.Fatalf("generation after archive status=%d body=%v; want 409", status, rejected)
	}
	unauthorized := requestHTTPBody(t, &http.Client{}, api.base, http.MethodGet, firstJSONPath, nil)
	if unauthorized.status != http.StatusUnauthorized {
		t.Fatalf("unauthorized report export status=%d body=%s; want 401", unauthorized.status, unauthorized.body)
	}

	insufficientCase := createTelecomCase(t, api, "C1-B HTTP insufficient evidence")
	generateTelecomDraft(t, api, insufficientCase, templateID, insufficient.Fields)
	for _, material := range insufficient.Materials {
		api.upload(t, insufficientCase, material.Role, material.Filename, "text/plain", []byte(material.Text))
	}
	if insufficient.DraftOverride == nil {
		t.Fatal("frozen insufficient scenario has no missing-period/wrong-circuit draft")
	}
	api.upload(t, insufficientCase, "draft_contract", insufficient.DraftOverride.Filename, "text/plain", []byte(insufficient.DraftOverride.Text))
	insufficientRunID, insufficientAttemptID, insufficientWorkspace := startTelecomReview(t, api, insufficientCase)
	assertTelecomRelations(t, insufficientWorkspace, insufficient.ExpectedRelations)
	advance := evaluationForRule(t, insufficientWorkspace, "ENT-COM-ADV-001")
	insufficientFindingID := requiredString(t, advance, "finding_id")
	status, blockedItem := api.do(t, http.MethodPost, "/api/cases/"+insufficientCase+"/remediations", map[string]any{
		"run_id": insufficientRunID, "attempt_id": insufficientAttemptID,
		"finding_ids": []string{insufficientFindingID}, "assignee_id": "demo-admin",
		"due_at": "2027-01-01T00:00:00Z", "measure": "补充经批准的预付款上限依据。",
	})
	if status != http.StatusCreated {
		t.Fatalf("create insufficient remediation: status=%d body=%v", status, blockedItem)
	}
	blockedItemID := requiredString(t, blockedItem, "id")
	newDraftID := api.upload(t, insufficientCase, "draft_contract", "telecom-payment-corrected.txt", "text/plain", []byte(correctedDraft))
	status, _ = api.do(t, http.MethodPost,
		fmt.Sprintf("/api/cases/%s/remediations/%s/submit", insufficientCase, blockedItemID),
		map[string]any{"document_ids": []string{newDraftID}, "reason": "拟签条款已回到 20%，尚待审批依据。", "expected_version": 1})
	if status != http.StatusOK {
		t.Fatalf("submit insufficient remediation status=%d", status)
	}
	insufficientNextRun, insufficientNextAttempt, nextWorkspace := startTelecomReview(t, api, insufficientCase)
	assertTelecomRelations(t, nextWorkspace, map[string]string{
		"ENT-TECH-BW-001":  "compatible",
		"ENT-TECH-SLA-001": "compatible",
		"ENT-COM-ADV-001":  "insufficient",
	})
	status, refusal := api.do(t, http.MethodPost,
		fmt.Sprintf("/api/cases/%s/remediations/%s/verify", insufficientCase, blockedItemID),
		map[string]any{
			"run_id": insufficientNextRun, "attempt_id": insufficientNextAttempt,
			"reason": "缺少审批依据，仍无法判断预付款是否在授权上限内。", "expected_version": 2,
		})
	if status != http.StatusConflict {
		t.Fatalf("insufficient evidence closed remediation: status=%d body=%v; want 409", status, refusal)
	}
	_, caseDetail := api.do(t, http.MethodGet, "/api/cases/"+insufficientCase, nil)
	remediations := caseDetail["remediations"].([]any)
	if len(remediations) != 1 || remediations[0].(map[string]any)["status"] != "reopened" {
		t.Fatalf("failed verification did not retain reopened history: %+v", remediations)
	}
}

func TestTelecomRevisionKeepsMissingClausesUnknown(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	dataset := readTelecomDataset(t)
	conflict := telecomScenario(t, dataset, "conflict")
	templateID := requireTelecomTemplate(t, api, dataset.Template, dataset.TemplateVersion)
	caseID := createTelecomCase(t, api, "RulePack revision transitions")
	generateTelecomDraft(t, api, caseID, templateID, conflict.Fields)
	for _, material := range conflict.Materials {
		api.upload(t, caseID, material.Role, material.Filename, "text/plain", []byte(material.Text))
	}
	_, _, previous := startTelecomReview(t, api, caseID)
	const unchanged = "电路 LINE-001：SLA 可用率 99.9%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。\n" +
		"电路 LINE-001：预付款比例 20%，预付款以合同总额为基础，含税。\n" +
		"电路 LINE-001：合同总额 100000 元，含税。"
	for _, step := range []struct {
		name, bandwidth, relation, mark, revisionRelation string
	}{
		{"changed_conflict", "电路 LINE-001：最低保障带宽 75 Mbit/s。\n", "conflict", "持续", "conflict"},
		{"deleted_clause", "", "insufficient", "未能判断", "insufficient"},
		{"restored_requirement", "电路 LINE-001：最低保障带宽 100 Mbit/s。\n", "compatible", "未能判断", "insufficient"},
		{"new_conflict", "电路 LINE-001：最低保障带宽 50 Mbit/s。\n", "conflict", "新增", "conflict"},
	} {
		t.Run(step.name, func(t *testing.T) {
			priorEvaluation := evaluationForRule(t, previous, "ENT-TECH-BW-001")
			priorFindingID := priorEvaluation["finding_id"]
			// A missing clause has no verified object identity. A later compatible
			// result cannot retroactively assign that old risk to its own object.
			if step.name == "restored_requirement" && len(stringSlice(t, priorEvaluation["object_refs"])) != 0 {
				t.Fatalf("missing-clause risk unexpectedly acquired an object identity: %v", priorEvaluation)
			}
			api.upload(t, caseID, "draft_contract", step.name+".txt", "text/plain", []byte(step.bandwidth+unchanged))
			_, _, current := startTelecomReview(t, api, caseID)
			evaluation := evaluationForRule(t, current, "ENT-TECH-BW-001")
			if evaluation["relation"] != step.relation || current["revision_comparison_reason"] != nil {
				t.Fatalf("unexpected frozen comparison: evaluation=%v reason=%v", evaluation, current["revision_comparison_reason"])
			}
			var revision map[string]any
			for _, value := range current["revisions"].([]any) {
				row := value.(map[string]any)
				matches := priorFindingID != nil && row["previous_finding_id"] == priorFindingID
				if priorFindingID == nil {
					matches = row["finding_id"] == evaluation["finding_id"]
				}
				if matches {
					if revision != nil {
						t.Fatalf("ambiguous revision for bandwidth: %v and %v", revision, row)
					}
					revision = row
				}
			}
			if revision == nil || revision["mark"] != step.mark || revision["relation"] != step.revisionRelation || revision["previous_finding_id"] != priorFindingID {
				t.Fatalf("wrong bandwidth revision: got=%v want=%s/%s previous=%v", revision, step.mark, step.revisionRelation, priorFindingID)
			}
			previous = current
		})
		if t.Failed() {
			break
		}
	}
}

func readTelecomDataset(t *testing.T) telecomDatasetFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "telecom-c1b-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dataset telecomDatasetFixture
	if err := json.Unmarshal(raw, &dataset); err != nil {
		t.Fatal(err)
	}
	return dataset
}

func telecomScenario(t *testing.T, dataset telecomDatasetFixture, id string) telecomScenarioFixture {
	t.Helper()
	for _, scenario := range dataset.Scenarios {
		if scenario.ID == id {
			return scenario
		}
	}
	t.Fatalf("frozen telecom scenario %q is missing", id)
	return telecomScenarioFixture{}
}

func requireTelecomTemplate(t *testing.T, api *testAPI, expectedID, expectedVersion string) string {
	t.Helper()
	status, response := api.do(t, http.MethodGet, "/api/templates", nil)
	if status != http.StatusOK {
		t.Fatalf("list templates: status=%d body=%v", status, response)
	}
	for _, raw := range response["items"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == expectedID && item["version"] == expectedVersion {
			return expectedID
		}
	}
	t.Fatalf("frozen template %s version %s is not public", expectedID, expectedVersion)
	return ""
}

func createTelecomCase(t *testing.T, api *testAPI, title string) string {
	t.Helper()
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": title, "contract_type": "service", "business_line": "ENT", "scenario": "telecom_dedicated_line",
	})
	if status != http.StatusCreated {
		t.Fatalf("create telecom case: status=%d body=%v", status, created)
	}
	return requiredString(t, created, "id")
}

func generateTelecomDraft(t *testing.T, api *testAPI, caseID, templateID string, fields map[string]string) map[string]any {
	t.Helper()
	status, generation := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/generations", map[string]any{
		"template_id": templateID, "fields": fields,
	})
	if status != http.StatusCreated || generation["template_id"] != templateID {
		t.Fatalf("generate telecom draft: status=%d body=%v", status, generation)
	}
	return generation
}

func startTelecomReview(t *testing.T, api *testAPI, caseID string) (string, string, map[string]any) {
	t.Helper()
	status, accepted := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("start telecom review: status=%d body=%v", status, accepted)
	}
	runID := requiredString(t, accepted, "id")
	attemptID, _ := accepted["attempt_id"].(string)
	path := fmt.Sprintf("/api/cases/%s/reviews/%s", caseID, runID)
	if attemptID != "" {
		path += "?attempt_id=" + attemptID
	}
	status, workspace := api.do(t, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("read telecom workspace: status=%d body=%v", status, workspace)
	}
	if attemptID == "" {
		execution, _ := workspace["execution"].(map[string]any)
		attempt, _ := execution["attempt"].(map[string]any)
		attemptID, _ = attempt["id"].(string)
	}
	if attemptID == "" {
		t.Fatalf("review %s has no frozen attempt identity", runID)
	}
	return runID, attemptID, workspace
}

func assertTelecomRelations(t *testing.T, workspace map[string]any, expected map[string]string) {
	t.Helper()
	actual := map[string]string{}
	for _, raw := range workspace["evaluations"].([]any) {
		item := raw.(map[string]any)
		ruleID, _ := item["rule_id"].(string)
		if ruleID != "" {
			actual[ruleID] = item["relation"].(string)
		}
	}
	for ruleID, relation := range expected {
		if actual[ruleID] != relation {
			t.Fatalf("RulePack result %s=%q, want %q; evaluations=%+v", ruleID, actual[ruleID], relation, workspace["evaluations"])
		}
	}
}

func evaluationForRule(t *testing.T, workspace map[string]any, ruleID string) map[string]any {
	t.Helper()
	for _, raw := range workspace["evaluations"].([]any) {
		item := raw.(map[string]any)
		if item["rule_id"] == ruleID {
			return item
		}
	}
	t.Fatalf("evaluation for %s missing: %+v", ruleID, workspace["evaluations"])
	return nil
}

func findingForID(t *testing.T, workspace map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range workspace["findings"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == id {
			return item
		}
	}
	t.Fatalf("finding %s missing from workspace", id)
	return nil
}

func stringSlice(t *testing.T, value any) []string {
	t.Helper()
	rows, ok := value.([]any)
	if !ok {
		t.Fatalf("value %T is not a JSON array", value)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		text, ok := row.(string)
		if !ok {
			t.Fatalf("array item %T is not a string", row)
		}
		out = append(out, text)
	}
	return out
}

func requiredString(t *testing.T, values map[string]any, key string) string {
	t.Helper()
	value, ok := values[key].(string)
	if !ok || value == "" {
		t.Fatalf("%s is absent from response: %+v", key, values)
	}
	return value
}

func createTelecomReport(t *testing.T, api *testAPI, caseID, runID, attemptID string) map[string]any {
	t.Helper()
	status, report := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reports", map[string]any{
		"run_id": runID, "attempt_id": attemptID,
	})
	if status != http.StatusCreated {
		t.Fatalf("create frozen report: status=%d body=%v", status, report)
	}
	return report
}

type httpBodyResult struct {
	status int
	header http.Header
	body   []byte
}

func readHTTPBody(t *testing.T, client *http.Client, base, method, path string, payload any) httpBodyResult {
	t.Helper()
	result := requestHTTPBody(t, client, base, method, path, payload)
	if result.status != http.StatusOK {
		t.Fatalf("%s %s: status=%d body=%s", method, path, result.status, result.body)
	}
	return result
}

func requestHTTPBody(t *testing.T, client *http.Client, base, method, path string, payload any) httpBodyResult {
	t.Helper()
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return httpBodyResult{status: response.StatusCode, header: response.Header.Clone(), body: data}
}
