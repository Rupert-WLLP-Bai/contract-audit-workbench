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

const telecomReviewBusinessAt = "2026-02-03T01:05:06Z"

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
	publishTelecomBusinessPolicy(t, api)
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
	correctedID := api.replaceUpload(t, caseID, "draft_contract", "telecom-revised-draft.txt", "text/plain", []byte(correctedDraft), requiredString(t, generation, "material_id"), requiredString(t, generation, "document_id"))
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
	insufficientGeneration := generateTelecomDraft(t, api, insufficientCase, templateID, insufficient.Fields)
	for _, material := range insufficient.Materials {
		if material.Role == "draft_contract" {
			api.replaceUpload(t, insufficientCase, material.Role, material.Filename, "text/plain", []byte(material.Text), requiredString(t, insufficientGeneration, "material_id"), requiredString(t, insufficientGeneration, "document_id"))
		} else {
			api.upload(t, insufficientCase, material.Role, material.Filename, "text/plain", []byte(material.Text))
		}
	}
	if insufficient.DraftOverride == nil {
		t.Fatal("frozen insufficient scenario has no missing-period/wrong-circuit draft")
	}
	currentMaterial, currentDocument := api.materialForDocument(t, insufficientCase, requiredString(t, insufficientGeneration, "material_id"))
	api.replaceUpload(t, insufficientCase, "draft_contract", insufficient.DraftOverride.Filename, "text/plain", []byte(insufficient.DraftOverride.Text), currentMaterial, currentDocument)
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
	currentMaterial, currentDocument = api.materialForDocument(t, insufficientCase, currentMaterial)
	newDraftID := api.replaceUpload(t, insufficientCase, "draft_contract", "telecom-payment-corrected.txt", "text/plain", []byte(correctedDraft), currentMaterial, currentDocument)
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

func TestGeneratedMaterialHTTPIdempotencyAndExplicitReplacement(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	caseID := createTelecomCase(t, api, "生成材料幂等与显式替换")
	dataset := readTelecomDataset(t)
	templateID := requireTelecomTemplate(t, api, dataset.Template, dataset.TemplateVersion)
	fields := telecomScenario(t, dataset, "compatible").Fields
	path := "/api/cases/" + caseID + "/generations"
	status, first := api.send(t, http.MethodPost, path, map[string]any{"template_id": templateID, "fields": fields}, "generation-add-key")
	if status != http.StatusCreated || first["replayed"] != false || first["version"] != float64(1) {
		t.Fatalf("create generated material: status=%d body=%v", status, first)
	}
	materialID, documentID := requiredString(t, first, "material_id"), requiredString(t, first, "document_id")
	status, replay := api.send(t, http.MethodPost, path, map[string]any{"template_id": templateID, "fields": fields}, "generation-add-key")
	if status != http.StatusOK || replay["replayed"] != true || replay["material_id"] != materialID || replay["document_id"] != documentID || replay["version"] != float64(1) {
		t.Fatalf("replay generated material: status=%d first=%v replay=%v", status, first, replay)
	}
	changed := map[string]string{}
	for key, value := range fields {
		changed[key] = value
	}
	changed["bandwidth_mbps"] = "200"
	status, conflict := api.send(t, http.MethodPost, path, map[string]any{"template_id": templateID, "fields": changed}, "generation-add-key")
	if status != http.StatusConflict {
		t.Fatalf("changed payload with same key should conflict: status=%d body=%v", status, conflict)
	}
	status, replaced := api.send(t, http.MethodPost, path, map[string]any{
		"template_id": templateID, "fields": changed, "intent": "replace", "material_id": materialID, "replaces_document_id": documentID,
	}, "generation-replace-key")
	if status != http.StatusCreated || replaced["material_id"] != materialID || replaced["version"] != float64(2) || replaced["replayed"] != false {
		t.Fatalf("explicitly replace generated material: status=%d body=%v", status, replaced)
	}
	status, secondConflict := api.send(t, http.MethodPost, path, map[string]any{"template_id": templateID, "fields": fields}, "generation-add-second-key")
	errorBody, _ := secondConflict["error"].(map[string]any)
	actions, _ := errorBody["next_actions"].([]any)
	if status != http.StatusConflict || errorBody["code"] != "primary_contract_exists" || len(actions) != 2 || actions[0] != "separate_case" || actions[1] != "explicit_replace" {
		t.Fatalf("default add to existing primary should provide explicit next actions: status=%d body=%v", status, secondConflict)
	}
}

func TestTelecomRevisionKeepsMissingClausesUnknown(t *testing.T) {
	api := newAPI(t)
	publishTelecomBusinessPolicy(t, api)
	api.login(t, "demo-admin", "admin-demo")
	dataset := readTelecomDataset(t)
	conflict := telecomScenario(t, dataset, "conflict")
	templateID := requireTelecomTemplate(t, api, dataset.Template, dataset.TemplateVersion)
	caseID := createTelecomCase(t, api, "RulePack revision transitions")
	revisionGeneration := generateTelecomDraft(t, api, caseID, templateID, conflict.Fields)
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
			materialID, currentDocumentID := api.materialForDocument(t, caseID, requiredString(t, revisionGeneration, "material_id"))
			api.replaceUpload(t, caseID, "draft_contract", step.name+".txt", "text/plain", []byte(step.bandwidth+unchanged), materialID, currentDocumentID)
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

// Publish real synthetic governance through the Admin API before exercising the
// dedicated-line business workflow with a client-audience session.
func publishTelecomBusinessPolicy(t *testing.T, api *testAPI) {
	t.Helper()
	adminLogin(t, api)
	manifest := governedHTTPManifest("1", "100")
	for _, raw := range manifest["rules"].([]any) {
		rule := raw.(map[string]any)
		rule["authority_sources"] = []any{map[string]any{"id": "telecom-business-demo-policy", "version": "1", "kind": "synthetic_demo"}}
	}
	status, body := api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{"manifest": manifest})
	if status != http.StatusCreated {
		t.Fatalf("create synthetic telecom pack: %d %v", status, body)
	}
	packPath := "/api/admin/rulepacks/telecom-governed/versions/1"
	pack := body["pack"].(map[string]any)
	status, body = api.do(t, http.MethodPost, packPath+"/validate", map[string]any{"expected_revision": pack["revision"]})
	if status != http.StatusOK {
		t.Fatalf("validate synthetic telecom pack: %d %v", status, body)
	}
	pack = body["pack"].(map[string]any)
	status, body = api.do(t, http.MethodPost, packPath+"/approve", map[string]any{"expected_revision": pack["revision"], "expected_content_hash": pack["content_hash"]})
	if status != http.StatusOK {
		t.Fatalf("approve synthetic telecom pack: %d %v", status, body)
	}
	pack = body["pack"].(map[string]any)
	status, body = api.do(t, http.MethodPost, packPath+"/publish", map[string]any{"expected_revision": pack["revision"], "expected_pointer_revision": pack["active_pointer_revision"]})
	if status != http.StatusOK {
		t.Fatalf("publish synthetic telecom pack: %d %v", status, body)
	}
	pack = body["pack"].(map[string]any)
	var text string
	var ruleIDs []string
	var spans []any
	for _, rule := range []struct{ id, quote, suffix string }{
		{"ENT-TECH-BW-001", "带宽不低于100 Mbit/s", ""},
		{"ENT-TECH-SLA-001", "月可用率不低于99.9%", "，统计周期为自然月，时区Asia/Shanghai，公历gregorian，故障口径all_service_unavailability，无排除项"},
		{"ENT-COM-ADV-001", "预付款比例不超过1/3", "，预付款以合同总额为基础"},
	} {
		start := len([]rune(text))
		text += rule.quote + rule.suffix + "。"
		ruleIDs = append(ruleIDs, rule.id)
		spans = append(spans, map[string]any{"rule_id": rule.id, "start": start, "end": start + len([]rune(rule.quote)), "quote": rule.quote})
	}
	content := map[string]any{
		"knowledge_id": "telecom-business-demo-policy", "version": "1", "kind": "policy", "title": "专线合成治理制度",
		"body": text, "summary": "核验带宽、可用率和预付款比例", "source_type": "synthetic_policy", "source_reference": "合成业务测试制度",
		"scope":      map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
		"valid_from": "2020-01-01T00:00:00Z", "valid_until": "2100-01-01T00:00:00Z",
		"binding": map[string]any{"pack_id": "telecom-governed", "pack_version": "1", "pack_content_hash": pack["content_hash"], "pack_snapshot_digest": pack["snapshot_sha256"], "compiler_ref": "governed-rulepack/v1", "rule_ids": ruleIDs, "source_spans": spans, "candidate_diff": "规则差异已审阅", "validation_evidence": "规则包校验通过"},
	}
	base := "/api/admin/governed-knowledge"
	status, body = api.do(t, http.MethodPost, base, map[string]any{"content": content, "idempotency_key": "telecom-business-policy-create"})
	if status != http.StatusCreated {
		t.Fatalf("create synthetic telecom policy: %d %v", status, body)
	}
	ref := body["ref"].(map[string]any)
	commandPath := base + "/telecom-business-demo-policy/versions/" + ref["version_id"].(string) + "/commands"
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		status, body = api.do(t, http.MethodPost, commandPath, map[string]any{"action": action, "expected_revision": body["revision"], "expected_content_sha256": ref["content_sha256"], "expected_pointer_revision": body["pointer_revision"], "reason": "已核对合成制度依据", "idempotency_key": "telecom-business-policy-" + action})
		if status != http.StatusOK {
			t.Fatalf("%s synthetic telecom policy: %d %v", action, status, body)
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
	status, accepted := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{"business_at": telecomReviewBusinessAt})
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
	reviewAttempts, _ := workspace["review_attempts"].(map[string]any)
	selected, _ := reviewAttempts["attempt"].(map[string]any)
	workspaceAttemptID, _ := selected["id"].(string)
	if attemptID == "" {
		attemptID = workspaceAttemptID
	}
	if attemptID == "" || workspaceAttemptID != attemptID {
		t.Fatalf("review %s has inconsistent frozen attempt identity: accepted=%q workspace=%q", runID, attemptID, workspaceAttemptID)
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
