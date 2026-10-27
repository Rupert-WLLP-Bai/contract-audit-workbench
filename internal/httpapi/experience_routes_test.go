package httpapi_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type experienceAPIModel struct{ scriptModels }

// The external model is deterministic; the queue, persisted result, Core
// acceptance, source authorization and HTTP reads are the real implementation.
func (experienceAPIModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: `{"schema_version":"experience-candidate/v1","applicability":["付款发生在验收后"],"exclusions":["验收前付款"],"check_action":"核对验收日期与付款条款","minimum_evidence":["验收记录"],"counterexamples":["验收前付款仍需核查"],"valid_from":"2020-01-01T00:00:00Z","valid_until":"2100-01-01T00:00:00Z","policy_refs":[],"review_diff":""}`}, nil
}

type experienceGatewayTransport struct {
	key string
}

func (transport experienceGatewayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header.Set("X-Contract-Service-Key", transport.key)
	return http.DefaultTransport.RoundTrip(copy)
}

// The legacy in-process handler prepares a frozen review. Distillation runs
// through the actual Core service, which alone mounts result delivery.
func newExperienceAPI(t *testing.T) (*testAPI, *store.Store, string) {
	t.Helper()
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(experienceAPIModel{})
	st.UseExperienceResultAcceptor(st)
	legacy := httptest.NewServer(httpapi.Handler(st, httpapi.Options{}))
	t.Cleanup(legacy.Close)
	gatewayKey := strings.Repeat("g", 40)
	workerKey := strings.Repeat("w", 40)
	coreHandler, err := httpapi.ServiceHandler(st, httpapi.Options{Durable: true, ExperienceWorkerCredential: workerKey}, "core", gatewayKey)
	if err != nil {
		t.Fatal(err)
	}
	core := httptest.NewServer(coreHandler)
	t.Cleanup(core.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &testAPI{base: legacy.URL, client: &http.Client{Jar: jar, Transport: experienceGatewayTransport{key: gatewayKey}}}, st, core.URL
}

// Publish a complete policy before review acceptance so the feedback is tied
// to the immutable policy snapshot used by the review.
func publishExperiencePolicy(t *testing.T, api *testAPI) {
	t.Helper()
	adminLogin(t, api)
	status, body := api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{"manifest": governedHTTPManifest("1", "100")})
	if status != http.StatusCreated {
		t.Fatalf("create experience rule pack: %d %v", status, body)
	}
	packPath := "/api/admin/rulepacks/telecom-governed/versions/1"
	status, body = api.do(t, http.MethodPost, packPath+"/validate", map[string]any{"expected_revision": 1})
	if status != http.StatusOK {
		t.Fatalf("validate experience rule pack: %d %v", status, body)
	}
	pack := body["pack"].(map[string]any)
	status, body = api.do(t, http.MethodPost, packPath+"/approve", map[string]any{"expected_revision": pack["revision"], "expected_content_hash": pack["content_hash"]})
	if status != http.StatusOK {
		t.Fatalf("approve experience rule pack: %d %v", status, body)
	}
	pack = body["pack"].(map[string]any)
	status, body = api.do(t, http.MethodPost, packPath+"/publish", map[string]any{"expected_revision": pack["revision"], "expected_pointer_revision": 0})
	if status != http.StatusOK {
		t.Fatalf("publish experience rule pack: %d %v", status, body)
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
		"knowledge_id": "operator-policy", "version": "1", "kind": "policy", "title": "专线治理制度", "body": text, "summary": "核验带宽、可用率和预付款比例",
		"source_type": "reference", "source_reference": "制度来源记录", "scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
		"valid_from": "2020-01-01T00:00:00Z", "valid_until": "2100-01-01T00:00:00Z",
		"binding": map[string]any{"pack_id": "telecom-governed", "pack_version": "1", "pack_content_hash": pack["content_hash"], "pack_snapshot_digest": pack["snapshot_sha256"], "compiler_ref": "governed-rulepack/v1", "rule_ids": ruleIDs, "source_spans": spans, "candidate_diff": "规则差异已审阅", "validation_evidence": "规则包校验通过"},
	}
	base := "/api/admin/governed-knowledge"
	status, body = api.do(t, http.MethodPost, base, map[string]any{"content": content, "idempotency_key": "experience-policy-create"})
	if status != http.StatusCreated {
		t.Fatalf("create experience policy: %d %v", status, body)
	}
	ref := body["ref"].(map[string]any)
	commandPath := base + "/operator-policy/versions/" + ref["version_id"].(string) + "/commands"
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		status, body = api.do(t, http.MethodPost, commandPath, map[string]any{"action": action, "expected_revision": body["revision"], "expected_content_sha256": ref["content_sha256"], "expected_pointer_revision": body["pointer_revision"], "reason": "已核对政策依据", "idempotency_key": "experience-policy-" + action})
		if status != http.StatusOK {
			t.Fatalf("%s experience policy: %d %v", action, status, body)
		}
	}
}

func TestExperienceFeedbackDistillationAndCandidateOverHTTP(t *testing.T) {
	api, st, coreURL := newExperienceAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	publishExperiencePolicy(t, api)
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "经验来源", "contract_type": "service", "business_line": "ENT", "scenario": "telecom_dedicated_line"})
	if status != http.StatusCreated {
		t.Fatalf("create case: %d %v", status, created)
	}
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件要求电路 LINE-001 的最低带宽为100 Mbit/s，合同总价100万元。"))
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定电路 LINE-001 的最低带宽为80 Mbit/s，合同总价120万元。"))
	status, started := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{"business_at": "2026-02-03T01:05:06Z"})
	if status != http.StatusCreated {
		t.Fatalf("start review: %d %v", status, started)
	}
	runID := started["id"].(string)
	attemptID := started["attempt_id"].(string)
	reviewPath := "/api/cases/" + caseID + "/reviews/" + runID
	status, workspace := api.do(t, http.MethodGet, reviewPath, nil)
	if status != http.StatusOK {
		t.Fatalf("read review: %d %v", status, workspace)
	}
	assertWireSchema(t, openAPI(t), "Workspace", workspace)
	finding := workspace["findings"].([]any)[0].(map[string]any)
	findingID := finding["id"].(string)
	status, body := api.send(t, http.MethodPost, reviewPath+"/findings/"+findingID+"/decisions", map[string]any{
		"action": "confirm_risk", "reason": "核对原文存在期限差异", "expected_version": finding["version"],
	}, "experience-decision")
	if status != http.StatusNoContent {
		t.Fatalf("decide finding: %d %v", status, body)
	}
	_, workspace = api.do(t, http.MethodGet, reviewPath, nil)
	finding = workspace["findings"].([]any)[0].(map[string]any)
	decisions := finding["decisions"].([]any)
	status, diagnostics := api.do(t, http.MethodGet, reviewPath+"/diagnostics?attempt_id="+attemptID, nil)
	if status != http.StatusOK || diagnostics["case_id"] != caseID || diagnostics["run_id"] != runID {
		t.Fatalf("case-authorized diagnostics: %d %v", status, diagnostics)
	}
	assertWireSchema(t, openAPI(t), "ReviewDiagnostics", diagnostics)
	decisionID := decisions[0].(map[string]any)["id"].(string)
	doc := workspace["documents"].([]any)[0].(map[string]any)
	text := doc["text"].(string)
	ref := map[string]any{"document_id": doc["id"], "parsed_document_id": doc["parsed_document_id"], "quote": text, "start": 0, "end": len([]rune(text))}
	input := map[string]any{
		"run_id": runID, "attempt_id": attemptID, "finding_id": findingID, "decision_id": decisionID,
		"reason": "应结合证据复核差异", "evidence_refs": []any{ref}, "idempotency_key": "feedback-request",
	}
	legacyURL := api.base
	frozenSnapshot := workspace["snapshot"]
	api.base = coreURL
	feedbackPath := "/api/cases/" + caseID + "/experience-feedback"
	status, feedback := api.do(t, http.MethodPost, feedbackPath, input)
	if status != http.StatusCreated || feedback["applicant_id"] != "demo-admin" || feedback["decision_id"] != decisionID {
		t.Fatalf("submit feedback: %d %v", status, feedback)
	}
	assertWireSchema(t, openAPI(t), "ExperienceFeedback", feedback)
	returnedRef := feedback["evidence_refs"].([]any)[0].(map[string]any)
	if _, leaked := returnedRef["normalized_digest"]; leaked {
		t.Fatalf("technical evidence digest leaked: %v", returnedRef)
	}
	feedbackID := feedback["feedback_id"].(string)
	input["case_id"] = caseID
	status, replay := api.do(t, http.MethodPost, feedbackPath, input)
	if status != http.StatusCreated || !reflect.DeepEqual(feedback, replay) {
		t.Fatalf("feedback replay: %d %v", status, replay)
	}
	status, read := api.do(t, http.MethodGet, feedbackPath+"/"+feedbackID, nil)
	if status != http.StatusOK || !reflect.DeepEqual(feedback, read) {
		t.Fatalf("read feedback: %d %v", status, read)
	}
	badCase := map[string]any{"case_id": "other-case"}
	if status, _ = api.do(t, http.MethodPost, feedbackPath, badCase); status != http.StatusUnprocessableEntity {
		t.Fatalf("body/path case mismatch: %d", status)
	}
	if status, _ = api.do(t, http.MethodPost, feedbackPath, map[string]any{"case_id": nil}); status != http.StatusUnprocessableEntity {
		t.Fatalf("null body case identity accepted: %d", status)
	}
	spoofed := map[string]any{"applicant_id": "demo-reviewer"}
	if status, _ = api.do(t, http.MethodPost, feedbackPath, spoofed); status != http.StatusUnprocessableEntity {
		t.Fatalf("client-supplied applicant accepted: %d", status)
	}
	badRef := map[string]any{"document_id": doc["id"], "parsed_document_id": doc["parsed_document_id"], "quote": text, "start": 0, "end": len([]rune(text)), "normalized_digest": strings.Repeat("0", 64)}
	invalid := map[string]any{"run_id": runID, "attempt_id": attemptID, "finding_id": findingID, "decision_id": decisionID, "reason": "核对来源证据", "evidence_refs": []any{badRef}, "idempotency_key": "bad-digest"}
	if status, _ = api.do(t, http.MethodPost, feedbackPath, invalid); status != http.StatusUnprocessableEntity {
		t.Fatalf("incorrect frozen digest accepted: %d", status)
	}
	distill := feedbackPath + "/" + feedbackID + "/distillations"
	if status, _ = api.do(t, http.MethodPost, distill, map[string]any{"feedback_id": "other", "idempotency_key": "x"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("body/path feedback mismatch: %d", status)
	}
	if status, _ = api.do(t, http.MethodPost, "/api/cases/"+caseID+"/members", map[string]any{"actor_id": "demo-reviewer"}); status != http.StatusOK {
		t.Fatalf("add reviewer: %d", status)
	}
	api.login(t, "demo-reviewer", "reviewer-demo")
	if status, _ = api.do(t, http.MethodGet, feedbackPath+"/"+feedbackID, nil); status != http.StatusOK {
		t.Fatalf("authorized reviewer could not read feedback: %d", status)
	}
	if status, _ = api.do(t, http.MethodPost, distill, map[string]any{"idempotency_key": "other-applicant"}); status != http.StatusForbidden {
		t.Fatalf("non-applicant requested distillation: %d", status)
	}
	api.login(t, "demo-admin", "admin-demo")
	status, receipt := api.do(t, http.MethodPost, distill, map[string]any{"case_id": caseID, "feedback_id": feedbackID, "idempotency_key": "distill-request"})
	if status != http.StatusAccepted || receipt["job_id"] == "" {
		t.Fatalf("request distillation: %d %v", status, receipt)
	}
	assertWireSchema(t, openAPI(t), "ExperienceDistillationReceipt", receipt)
	jobID := receipt["job_id"].(string)
	status, replay = api.do(t, http.MethodPost, distill, map[string]any{"idempotency_key": "distill-request"})
	if status != http.StatusAccepted || !reflect.DeepEqual(receipt, replay) {
		t.Fatalf("distillation replay: %d %v", status, replay)
	}
	statusPath := distill + "/" + jobID
	status, pending := api.do(t, http.MethodGet, statusPath, nil)
	if status != http.StatusOK || pending["state"] != "queued" || pending["feedback_id"] != feedbackID {
		t.Fatalf("queued status: %d %v", status, pending)
	}
	assertWireSchema(t, openAPI(t), "ExperienceDistillationStatus", pending)
	if _, exists := pending["candidate_id"]; exists {
		t.Fatalf("queued job invented a candidate: %v", pending)
	}
	claim, err := st.ClaimNextJob(context.Background(), "experience-api-test-worker", time.Minute)
	if err != nil || claim.JobID != jobID {
		t.Fatalf("claim distillation: %+v %v", claim, err)
	}
	if err := st.RunClaimedJob(context.Background(), claim); err != nil {
		t.Fatalf("run distillation: %v", err)
	}
	status, done := api.do(t, http.MethodGet, statusPath, nil)
	if status != http.StatusOK || done["state"] != "completed" || done["candidate_id"] == "" || done["version_id"] == "" {
		t.Fatalf("completed status: %d %v", status, done)
	}
	assertWireSchema(t, openAPI(t), "ExperienceDistillationStatus", done)
	candidatePath := "/api/cases/" + caseID + "/experience-candidates/" + done["candidate_id"].(string) + "/versions/" + done["version_id"].(string)
	status, candidate := api.do(t, http.MethodGet, candidatePath, nil)
	if status != http.StatusOK || candidate["feedback_id"] != feedbackID || candidate["job_id"] != jobID {
		t.Fatalf("read candidate: %d %v", status, candidate)
	}
	assertWireSchema(t, openAPI(t), "ExperienceCandidate", candidate)
	if status, _ = api.do(t, http.MethodGet, feedbackPath+"/other/distillations/"+jobID, nil); status != http.StatusNotFound {
		t.Fatalf("job disclosed through another feedback: %d", status)
	}
	st.UseModels(scriptModels{})
	status, failedReceipt := api.do(t, http.MethodPost, distill, map[string]any{"idempotency_key": "distill-failure"})
	if status != http.StatusAccepted {
		t.Fatalf("request failing distillation: %d %v", status, failedReceipt)
	}
	failedJob := failedReceipt["job_id"].(string)
	claim, err = st.ClaimNextJob(context.Background(), "experience-api-failing-worker", time.Minute)
	if err != nil || claim.JobID != failedJob {
		t.Fatalf("claim failing distillation: %+v %v", claim, err)
	}
	if err = st.RunClaimedJob(context.Background(), claim); err == nil {
		t.Fatal("model without generation unexpectedly succeeded")
	}
	status, failed := api.do(t, http.MethodGet, distill+"/"+failedJob, nil)
	if status != http.StatusOK || failed["state"] != "failed" || failed["failure_code"] != "experience_model_unavailable" {
		t.Fatalf("failed distillation status: %d %v", status, failed)
	}
	if _, exists := failed["candidate_id"]; exists {
		t.Fatalf("failed job exposed a candidate: %v", failed)
	}
	assertWireSchema(t, openAPI(t), "ExperienceDistillationStatus", failed)
	api.base = legacyURL
	status, after := api.do(t, http.MethodGet, reviewPath, nil)
	if status != http.StatusOK || !reflect.DeepEqual(after["snapshot"], frozenSnapshot) || !reflect.DeepEqual(after["findings"].([]any)[0].(map[string]any)["decisions"], decisions) {
		t.Fatalf("feedback changed frozen review or decisions: %d %v", status, after)
	}
	api.base = coreURL
	api.login(t, "demo-otherorg-clerk", "otherorg-clerk-demo")
	for _, path := range []string{feedbackPath + "/" + feedbackID, statusPath, candidatePath} {
		if status, _ = api.do(t, http.MethodGet, path, nil); status != http.StatusNotFound {
			t.Fatalf("case access bypass on %s: %d", path, status)
		}
	}
	api.login(t, "demo-tenant2-admin", "tenant2-admin-demo")
	if status, _ = api.do(t, http.MethodGet, candidatePath, nil); status != http.StatusNotFound {
		t.Fatalf("foreign tenant candidate read: %d", status)
	}
}

func TestExperienceAndKnowledgeRoutesRequireSessions(t *testing.T) {
	api := newAPI(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/cases/case/experience-feedback"},
		{http.MethodGet, "/api/cases/case/experience-feedback/feedback"},
		{http.MethodPost, "/api/cases/case/experience-feedback/feedback/distillations"},
		{http.MethodGet, "/api/cases/case/experience-feedback/feedback/distillations/job"},
		{http.MethodGet, "/api/cases/case/experience-candidates/candidate/versions/version"},
		{http.MethodGet, "/api/admin/governed-knowledge"},
		{http.MethodPost, "/api/admin/governed-knowledge"},
		{http.MethodGet, "/api/admin/governed-knowledge/id/versions/version"},
		{http.MethodGet, "/api/admin/governed-knowledge/id/versions/version/preview"},
		{http.MethodPost, "/api/admin/governed-knowledge/id/versions/version/commands"},
		{http.MethodGet, "/api/admin/governed-knowledge/id/history"},
	} {
		var payload any
		if tc.method == http.MethodPost {
			payload = map[string]any{}
		}
		if status, _ := api.do(t, tc.method, tc.path, payload); status != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s returned %d", tc.method, tc.path, status)
		}
	}
}

func TestExperienceDistillationRequiresDeliveryConfiguration(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	status, body := api.do(t, http.MethodPost, "/api/cases/case/experience-feedback/feedback/distillations", map[string]any{"idempotency_key": "no-delivery"})
	if status != http.StatusServiceUnavailable || body["error"].(map[string]any)["code"] != "experience_delivery_unavailable" {
		t.Fatalf("missing result callback accepted a job: %d %v", status, body)
	}
}

func TestGovernedKnowledgeLifecycleAndAuthorizationOverHTTP(t *testing.T) {
	api := newAPI(t)
	path := "/api/admin/governed-knowledge"
	if status, _ := api.do(t, http.MethodGet, path, nil); status != http.StatusUnauthorized {
		t.Fatalf("anonymous knowledge read: %d", status)
	}
	api.login(t, "demo-admin", "admin-demo")
	if status, _ := api.do(t, http.MethodGet, path, nil); status != http.StatusUnauthorized {
		t.Fatalf("client audience read administrator surface: %d", status)
	}
	if status, _ := api.do(t, http.MethodGet, path+"/id/versions/version/preview", nil); status != http.StatusUnauthorized {
		t.Fatalf("client audience accessed policy preview: %d", status)
	}
	adminLogin(t, api)
	status, createdPack := api.do(t, http.MethodPost, "/api/admin/rulepacks", map[string]any{"manifest": governedHTTPManifest("1", "100")})
	if status != http.StatusCreated {
		t.Fatalf("create pack: %d %v", status, createdPack)
	}
	packPath := "/api/admin/rulepacks/telecom-governed/versions/1"
	status, validated := api.do(t, http.MethodPost, packPath+"/validate", map[string]any{"expected_revision": 1})
	if status != http.StatusOK {
		t.Fatalf("validate pack: %d %v", status, validated)
	}
	pack := validated["pack"].(map[string]any)
	status, approved := api.do(t, http.MethodPost, packPath+"/approve", map[string]any{"expected_revision": pack["revision"], "expected_content_hash": pack["content_hash"]})
	if status != http.StatusOK {
		t.Fatalf("approve pack: %d %v", status, approved)
	}
	pack = approved["pack"].(map[string]any)
	status, published := api.do(t, http.MethodPost, packPath+"/publish", map[string]any{"expected_revision": pack["revision"], "expected_pointer_revision": 0})
	if status != http.StatusOK {
		t.Fatalf("publish pack: %d %v", status, published)
	}
	pack = published["pack"].(map[string]any)
	content := map[string]any{
		"knowledge_id": "operator-policy", "version": "1", "kind": "policy", "title": "专线带宽制度", "body": "带宽不低于100 Mbit/s。", "summary": "核对专线最低保障带宽",
		"source_type": "reference", "source_reference": "制度来源记录", "scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
		"valid_from": "2020-01-01T00:00:00Z", "valid_until": "2100-01-01T00:00:00Z",
		"binding": map[string]any{"pack_id": "telecom-governed", "pack_version": "1", "pack_content_hash": pack["content_hash"], "pack_snapshot_digest": pack["snapshot_sha256"], "compiler_ref": "governed-rulepack/v1", "rule_ids": []string{"ENT-TECH-BW-001"}, "source_spans": []any{map[string]any{"rule_id": "ENT-TECH-BW-001", "start": 0, "end": 15, "quote": "带宽不低于100 Mbit/s"}}, "candidate_diff": "规则差异已审阅", "validation_evidence": "规则包校验通过"},
	}
	request := map[string]any{"content": content, "idempotency_key": "policy-create"}
	status, draft := api.do(t, http.MethodPost, path, request)
	if status != http.StatusCreated || draft["status"] != "draft" || draft["created_by"] != "demo-admin" {
		t.Fatalf("create policy: %d %v", status, draft)
	}
	assertWireSchema(t, openAPI(t), "GovernedKnowledgeVersion", draft)
	status, replay := api.do(t, http.MethodPost, path, request)
	if status != http.StatusCreated || !reflect.DeepEqual(replay, draft) {
		t.Fatalf("create replay: %d %v", status, replay)
	}
	if status, _ = api.do(t, http.MethodPost, path, map[string]any{"content": content, "idempotency_key": "forged", "actor_id": "demo-tenant2-admin"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("spoofed governance actor accepted: %d", status)
	}
	ref := draft["ref"].(map[string]any)
	versionPath := path + "/operator-policy/versions/" + ref["version_id"].(string)
	status, exact := api.do(t, http.MethodGet, versionPath, nil)
	if status != http.StatusOK || !reflect.DeepEqual(exact, draft) {
		t.Fatalf("read draft: %d %v", status, exact)
	}
	status, list := api.do(t, http.MethodGet, path, nil)
	if status != http.StatusOK || len(list["items"].([]any)) != 1 {
		t.Fatalf("list policy metadata: %d %v", status, list)
	}
	assertWireSchema(t, openAPI(t), "GovernedKnowledgeList", list)
	if _, leaked := list["items"].([]any)[0].(map[string]any)["content"]; leaked {
		t.Fatal("list leaked policy content")
	}
	status, preview := api.do(t, http.MethodGet, versionPath+"/preview", nil)
	if status != http.StatusOK || preview["approvable"] != true || !reflect.DeepEqual(preview["ref"], ref) {
		t.Fatalf("matching policy preview: %d %v", status, preview)
	}
	assertWireSchema(t, openAPI(t), "GovernedPolicyPreview", preview)
	comparisons := preview["comparisons"].([]any)
	if len(comparisons) != 1 {
		t.Fatalf("policy preview comparisons: %v", comparisons)
	}
	match := comparisons[0].(map[string]any)
	candidate := match["candidate"].(map[string]any)
	threshold := match["rule_threshold"].(map[string]any)
	if match["status"] != "match" || candidate["value"] != "100" || candidate["unit"] != "Mbit/s" || candidate["quote"] != "带宽不低于100 Mbit/s" || candidate["start"] != float64(0) || candidate["end"] != float64(15) || threshold["value"] != "100" {
		t.Fatalf("incorrect matching threshold preview: %v", match)
	}
	for _, tc := range []struct{ id, body, quote, want string }{
		{"preview-difference", "带宽不低于200 Mbit/s。", "带宽不低于200 Mbit/s", "different"},
		{"preview-ambiguous", "带宽不低于100 Mbit/s。带宽不低于200 Mbit/s。", "带宽不低于100 Mbit/s", "ambiguous"},
	} {
		other := make(map[string]any, len(content))
		for key, value := range content {
			other[key] = value
		}
		binding := make(map[string]any)
		for key, value := range content["binding"].(map[string]any) {
			binding[key] = value
		}
		other["knowledge_id"], other["body"], other["binding"] = tc.id, tc.body, binding
		binding["source_spans"] = []any{map[string]any{"rule_id": "ENT-TECH-BW-001", "start": 0, "end": len([]rune(tc.quote)), "quote": tc.quote}}
		status, otherDraft := api.do(t, http.MethodPost, path, map[string]any{"content": other, "idempotency_key": tc.id})
		if status != http.StatusCreated {
			t.Fatalf("create %s: %d %v", tc.id, status, otherDraft)
		}
		otherRef := otherDraft["ref"].(map[string]any)
		status, otherPreview := api.do(t, http.MethodGet, path+"/"+tc.id+"/versions/"+otherRef["version_id"].(string)+"/preview", nil)
		if status != http.StatusOK || otherPreview["approvable"] != false {
			t.Fatalf("preview %s: %d %v", tc.id, status, otherPreview)
		}
		assertWireSchema(t, openAPI(t), "GovernedPolicyPreview", otherPreview)
		rows := otherPreview["comparisons"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["status"] != tc.want {
			t.Fatalf("preview %s comparison: %v", tc.id, rows)
		}
	}
	cmdPath := versionPath + "/commands"
	baseCommand := map[string]any{"knowledge_id": "operator-policy", "version_id": ref["version_id"], "action": "submit", "expected_revision": draft["revision"], "expected_content_sha256": ref["content_sha256"], "reason": "已核对制度依据", "idempotency_key": "policy-submit"}
	mismatch := map[string]any{"knowledge_id": "other-policy", "action": "submit"}
	if status, _ = api.do(t, http.MethodPost, cmdPath, mismatch); status != http.StatusUnprocessableEntity {
		t.Fatalf("body/path knowledge mismatch: %d", status)
	}
	mismatch = map[string]any{"version_id": "other-version", "action": "submit"}
	if status, _ = api.do(t, http.MethodPost, cmdPath, mismatch); status != http.StatusUnprocessableEntity {
		t.Fatalf("body/path version mismatch: %d", status)
	}
	if status, _ = api.send(t, http.MethodPost, cmdPath, baseCommand, "different-header-key"); status != http.StatusUnprocessableEntity {
		t.Fatalf("conflicting idempotency sources accepted: %d", status)
	}
	status, current := api.do(t, http.MethodPost, cmdPath, baseCommand)
	if status != http.StatusOK || current["status"] != "submitted" {
		t.Fatalf("submit policy: %d %v", status, current)
	}
	status, replay = api.do(t, http.MethodPost, cmdPath, baseCommand)
	if status != http.StatusOK || !reflect.DeepEqual(current, replay) {
		t.Fatalf("command replay: %d %v", status, replay)
	}
	stale := map[string]any{"action": "validate", "expected_revision": 1, "expected_content_sha256": ref["content_sha256"], "reason": "审查依据", "idempotency_key": "stale"}
	if status, _ = api.do(t, http.MethodPost, cmdPath, stale); status != http.StatusConflict {
		t.Fatalf("stale command: %d", status)
	}
	for _, action := range []string{"validate", "approve", "publish"} {
		command := map[string]any{"action": action, "expected_revision": current["revision"], "expected_content_sha256": ref["content_sha256"], "expected_pointer_revision": current["pointer_revision"], "reason": "已核对制度依据", "idempotency_key": "policy-" + action}
		status, current = api.do(t, http.MethodPost, cmdPath, command)
		if status != http.StatusOK {
			t.Fatalf("%s policy: %d %v", action, status, current)
		}
	}
	if current["status"] != "published" || current["active"] != true || current["approval_actor_id"] != "demo-admin" || current["approved_digest"] != ref["content_sha256"] {
		t.Fatalf("published policy was not bound to approved actor and content: %v", current)
	}
	assertWireSchema(t, openAPI(t), "GovernedKnowledgeVersion", current)
	status, history := api.do(t, http.MethodGet, path+"/operator-policy/history", nil)
	if status != http.StatusOK || len(history["items"].([]any)) != 5 {
		t.Fatalf("policy history: %d %v", status, history)
	}
	assertWireSchema(t, openAPI(t), "GovernedKnowledgeHistory", history)
	entries := history["items"].([]any)
	if entries[4].(map[string]any)["action"] != "publish" || entries[4].(map[string]any)["actor_id"] != "demo-admin" {
		t.Fatalf("history attribution: %v", entries)
	}
	status, _ = api.do(t, http.MethodPost, "/api/session", map[string]any{"username": "demo-tenant2-admin", "password": "tenant2-" + "admin-demo", "audience": "admin"})
	if status != http.StatusOK {
		t.Fatalf("foreign admin login: %d", status)
	}
	if status, _ = api.do(t, http.MethodGet, versionPath, nil); status != http.StatusNotFound {
		t.Fatalf("foreign tenant read policy: %d", status)
	}
	if status, _ = api.do(t, http.MethodGet, versionPath+"/preview", nil); status != http.StatusNotFound {
		t.Fatalf("foreign tenant read policy preview: %d", status)
	}
	status, list = api.do(t, http.MethodGet, path, nil)
	if status != http.StatusOK || len(list["items"].([]any)) != 0 {
		t.Fatalf("foreign tenant list: %d %v", status, list)
	}
}

func TestExperienceAndGovernedKnowledgeRoutesAreCoreOwned(t *testing.T) {
	paths := []struct{ method, path string }{
		{http.MethodPost, "/api/cases/case/experience-feedback"},
		{http.MethodGet, "/api/cases/case/experience-feedback/feedback"},
		{http.MethodPost, "/api/cases/case/experience-feedback/feedback/distillations"},
		{http.MethodGet, "/api/cases/case/experience-feedback/feedback/distillations/job"},
		{http.MethodGet, "/api/cases/case/experience-candidates/candidate/versions/version"},
		{http.MethodGet, "/api/admin/governed-knowledge"},
		{http.MethodPost, "/api/admin/governed-knowledge"},
		{http.MethodGet, "/api/admin/governed-knowledge/id/versions/version"},
		{http.MethodGet, "/api/admin/governed-knowledge/id/versions/version/preview"},
		{http.MethodPost, "/api/admin/governed-knowledge/id/versions/version/commands"},
		{http.MethodGet, "/api/admin/governed-knowledge/id/history"},
	}
	for _, tc := range paths {
		for _, role := range []string{"core", "document", "execution"} {
			if got := httpapi.OperationOwnedBy(role, tc.method, tc.path); got != (role == "core") {
				t.Errorf("%s %s owner %s = %v", tc.method, tc.path, role, got)
			}
			if got := httpapi.OperationOwnedBy(role, http.MethodOptions, tc.path); got != (role == "core") {
				t.Errorf("OPTIONS %s owner %s = %v", tc.path, role, got)
			}
			if tc.method == http.MethodGet && httpapi.OperationOwnedBy(role, http.MethodHead, tc.path) != (role == "core") {
				t.Errorf("HEAD %s owner %s", tc.path, role)
			}
		}
	}
	for _, path := range []string{
		"/api/cases/case/experience-feedback/feedback/distillations/job/extra",
		"/api/cases/case/experience-candidates/id/versions",
		"/api/admin/governed-knowledge/id/versions/version/commands/extra",
	} {
		if httpapi.OperationOwnedBy("core", http.MethodGet, path) || httpapi.OperationOwnedBy("core", http.MethodPost, path) {
			t.Errorf("unexpected owned path %s", path)
		}
	}
}
