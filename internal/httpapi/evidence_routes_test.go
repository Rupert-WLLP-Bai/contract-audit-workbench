package httpapi_test

import (
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
)

// Preparation uses the ordinary review handler. Evidence operations go through
// the independently authenticated Core service and its real service router.
func newEvidenceAPI(t *testing.T) (*testAPI, string, string) {
	t.Helper()
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	legacy := httptest.NewServer(httpapi.Handler(st, httpapi.Options{}))
	t.Cleanup(legacy.Close)
	gatewayKey := strings.Repeat("e", 40)
	coreHandler, err := httpapi.ServiceHandler(st, httpapi.Options{Durable: true}, "core", gatewayKey)
	if err != nil {
		t.Fatal(err)
	}
	core := httptest.NewServer(coreHandler)
	t.Cleanup(core.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	api := &testAPI{base: core.URL, client: &http.Client{Jar: jar, Transport: experienceGatewayTransport{key: gatewayKey}}}
	return api, legacy.URL, core.URL
}

func requireEvidenceHTTPError(t *testing.T, status, expected int, body map[string]any) {
	t.Helper()
	if status != expected {
		t.Fatalf("HTTP status = %d, want %d: %v", status, expected, body)
	}
	errorBody, ok := body["error"].(map[string]any)
	codes := map[int]string{http.StatusUnauthorized: "unauthenticated", http.StatusForbidden: "forbidden",
		http.StatusNotFound: "not_found", http.StatusConflict: "conflict", http.StatusUnprocessableEntity: "invalid"}
	message, hasMessage := errorBody["message"].(string)
	if !ok || errorBody["code"] != codes[expected] || !hasMessage || message == "" {
		t.Fatalf("wrong API error for status %d: %v", expected, body)
	}
}

func assertNoEvidenceDiagnostics(t *testing.T, value any) {
	t.Helper()
	private := map[string]bool{
		"source_input_hash": true, "source_output_hash": true, "input_hash": true, "output_hash": true,
		"document_digest": true, "normalized_digest": true, "review_watermark_run_sequence": true,
		"review_watermark_attempt_ordinal": true,
	}
	var visit func(any)
	visit = func(current any) {
		switch current := current.(type) {
		case map[string]any:
			for key, child := range current {
				if private[key] {
					t.Fatalf("business evidence response exposed %s: %v", key, current)
				}
				visit(child)
			}
		case []any:
			for _, child := range current {
				visit(child)
			}
		}
	}
	visit(value)
}

func evidenceEvaluation(t *testing.T, workspace map[string]any, domain, relation string) map[string]any {
	t.Helper()
	for _, raw := range workspace["evaluations"].([]any) {
		item := raw.(map[string]any)
		if item["domain"] == domain && item["relation"] == relation {
			return item
		}
	}
	t.Fatalf("no %s evaluation with relation %s: %v", domain, relation, workspace["evaluations"])
	return nil
}

func TestEvidenceRequestHTTPCaseAuthorizationAndFrozenLifecycle(t *testing.T) {
	api, legacyURL, coreURL := newEvidenceAPI(t)
	status, body := api.do(t, http.MethodGet, "/api/cases/no-case/evidence-requests", nil)
	requireEvidenceHTTPError(t, status, http.StatusUnauthorized, body)

	api.base = legacyURL
	api.login(t, "demo-admin", "admin-demo")
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "补充材料请求 API", "contract_type": "procurement_ict",
	})
	if status != http.StatusCreated {
		t.Fatalf("create case: %d %v", status, created)
	}
	caseID := created["id"].(string)
	for _, actor := range []string{"demo-reviewer", "demo-clerk", "demo-viewer"} {
		status, body = api.do(t, http.MethodPost, "/api/cases/"+caseID+"/members", map[string]any{"actor_id": actor})
		if status != http.StatusOK {
			t.Fatalf("add member %s: %d %v", actor, status, body)
		}
	}
	status, otherCase := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "另一个案件", "contract_type": "procurement_ict",
	})
	if status != http.StatusCreated {
		t.Fatalf("create other case: %d %v", status, otherCase)
	}
	otherCaseID := otherCase["id"].(string)
	api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件要求质保36个月。"))
	api.upload(t, caseID, "draft_contract", "initial.txt", "text/plain", []byte("拟签合同未写明质保期限。"))
	status, source := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("start source review: %d %v", status, source)
	}
	sourceRun, sourceAttempt := source["id"].(string), source["attempt_id"].(string)
	status, sourceWorkspace := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+sourceRun, nil)
	if status != http.StatusOK {
		t.Fatalf("read source review: %d %v", status, sourceWorkspace)
	}
	missing := evidenceEvaluation(t, sourceWorkspace, "warranty", "insufficient")
	due := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	status, task := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/tasks", map[string]any{
		"title": "补充质保条款", "kind": "review", "assignee_id": "demo-reviewer", "due_at": due,
		"status": "open", "priority": "high",
	})
	if status != http.StatusCreated {
		t.Fatalf("create assigned task: %d %v", status, task)
	}
	api.base = coreURL
	base := "/api/cases/" + caseID + "/evidence-requests"
	create := map[string]any{
		"task_id": task["id"], "task_version": task["version"], "source_run_id": sourceRun,
		"source_attempt_id": sourceAttempt, "source_evaluation_id": missing["id"],
		"question": "请补充拟签合同中的质保期限", "material_role": "draft_contract", "verification_condition": "conflict",
	}

	for name, change := range map[string]func(map[string]any){
		"different case":         func(in map[string]any) { in["case_id"] = otherCaseID },
		"null case":              func(in map[string]any) { in["case_id"] = nil },
		"client actor":           func(in map[string]any) { in["actor_id"] = "demo-admin" },
		"client tenant":          func(in map[string]any) { in["tenant_id"] = "another-tenant" },
		"unstructured condition": func(in map[string]any) { in["verification_condition"] = "请核对" },
	} {
		invalid := maps.Clone(create)
		change(invalid)
		status, body = api.send(t, http.MethodPost, base, invalid, "invalid-create")
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d body %v", name, status, body)
		}
	}
	status, body = api.send(t, http.MethodPost, base, create, "")
	requireEvidenceHTTPError(t, status, http.StatusUnprocessableEntity, body)
	conflictingHeader := maps.Clone(create)
	conflictingHeader["idempotency_key"] = "body-key"
	status, body = api.send(t, http.MethodPost, base, conflictingHeader, "header-key")
	requireEvidenceHTTPError(t, status, http.StatusUnprocessableEntity, body)
	staleTask := maps.Clone(create)
	staleTask["task_version"] = 2
	status, body = api.send(t, http.MethodPost, base, staleTask, "stale-task")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)

	status, request := api.send(t, http.MethodPost, base, create, "create-request")
	if status != http.StatusCreated || request["status"] != "open" || request["version"] != float64(1) ||
		request["case_id"] != caseID || request["assignee_id"] != "demo-reviewer" || request["created_by"] != "demo-admin" ||
		request["source_evaluation"].(map[string]any)["id"] != missing["id"] {
		t.Fatalf("create request: %d %v", status, request)
	}
	assertWireSchema(t, openAPI(t), "EvidenceRequest", request)
	assertNoEvidenceDiagnostics(t, request)
	requestID := request["id"].(string)
	requestPath := base + "/" + requestID
	createWithCase := maps.Clone(create)
	createWithCase["case_id"] = caseID
	createWithCase["idempotency_key"] = "create-request"
	status, replay := api.send(t, http.MethodPost, base, createWithCase, "create-request")
	if status != http.StatusCreated || !reflect.DeepEqual(request, replay) {
		t.Fatalf("create replay changed receipt: %d %v", status, replay)
	}
	changedCreate := maps.Clone(create)
	changedCreate["question"] = "另一个问题"
	status, body = api.send(t, http.MethodPost, base, changedCreate, "create-request")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)

	secondCreate := maps.Clone(create)
	secondCreate["verification_condition"] = "compatible"
	status, second := api.send(t, http.MethodPost, base, secondCreate, "second-request")
	if status != http.StatusCreated || second["id"] == requestID {
		t.Fatalf("create second request: %d %v", status, second)
	}
	secondID := second["id"].(string)
	secondPath := base + "/" + secondID
	status, list := api.do(t, http.MethodGet, base, nil)
	if status != http.StatusOK || len(list["items"].([]any)) != 2 {
		t.Fatalf("list requests: %d %v", status, list)
	}
	assertWireSchema(t, openAPI(t), "EvidenceRequestList", list)
	status, read := api.do(t, http.MethodGet, requestPath, nil)
	if status != http.StatusOK || !reflect.DeepEqual(read, request) {
		t.Fatalf("get request: %d %v", status, read)
	}
	status, history := api.do(t, http.MethodGet, requestPath+"/history", nil)
	if status != http.StatusOK || len(history["items"].([]any)) != 1 {
		t.Fatalf("initial history: %d %v", status, history)
	}
	status, body = api.do(t, http.MethodGet, "/api/cases/"+otherCaseID+"/evidence-requests/"+requestID, nil)
	requireEvidenceHTTPError(t, status, http.StatusNotFound, body)

	api.login(t, "demo-viewer", "viewer-demo")
	if status, body = api.do(t, http.MethodGet, requestPath, nil); status != http.StatusOK {
		t.Fatalf("case viewer cannot read request: %d %v", status, body)
	}
	if status, body = api.do(t, http.MethodGet, requestPath+"/history", nil); status != http.StatusOK {
		t.Fatalf("case viewer cannot read history: %d %v", status, body)
	}
	status, body = api.send(t, http.MethodPost, base, create, "viewer-create")
	requireEvidenceHTTPError(t, status, http.StatusForbidden, body)
	status, body = api.send(t, http.MethodPost, requestPath+"/receipts", map[string]any{
		"answer": "质保24个月", "expected_version": 1,
	}, "viewer-submit")
	requireEvidenceHTTPError(t, status, http.StatusForbidden, body)
	api.login(t, "demo-otherorg-clerk", "otherorg-clerk-demo")
	status, body = api.do(t, http.MethodGet, requestPath, nil)
	requireEvidenceHTTPError(t, status, http.StatusNotFound, body)
	status, body = api.send(t, http.MethodPost, base, create, "other-org-create")
	requireEvidenceHTTPError(t, status, http.StatusNotFound, body)
	api.login(t, "demo-tenant2-admin", "tenant2-admin-demo")
	status, body = api.do(t, http.MethodGet, requestPath+"/history", nil)
	requireEvidenceHTTPError(t, status, http.StatusNotFound, body)
	status, body = api.send(t, http.MethodPost, requestPath+"/receipts", map[string]any{
		"answer": "跨租户提交", "expected_version": 1,
	}, "other-tenant-submit")
	requireEvidenceHTTPError(t, status, http.StatusNotFound, body)

	api.login(t, "demo-admin", "admin-demo")
	api.base = legacyURL
	documentID := api.upload(t, caseID, "draft_contract", "supplement.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	api.base = coreURL
	status, secondReceiptResult := api.send(t, http.MethodPost, secondPath+"/receipts", map[string]any{
		"answer": "另一份提交", "expected_version": 1,
	}, "second-receipt")
	if status != http.StatusCreated {
		t.Fatalf("second request receipt: %d %v", status, secondReceiptResult)
	}
	secondReceiptID := secondReceiptResult["receipt"].(map[string]any)["id"].(string)

	api.login(t, "demo-reviewer", "reviewer-demo")
	receiptInput := map[string]any{"case_id": caseID, "request_id": requestID, "document_id": documentID, "expected_version": 1}
	status, received := api.send(t, http.MethodPost, requestPath+"/receipts", receiptInput, "receipt-request")
	if status != http.StatusCreated {
		t.Fatalf("assigned reviewer submitted receipt: %d %v", status, received)
	}
	receivedRequest, receipt := received["request"].(map[string]any), received["receipt"].(map[string]any)
	if receivedRequest["status"] != "received" || receivedRequest["version"] != float64(2) ||
		receivedRequest["verification"] != nil || receipt["request_id"] != requestID ||
		receipt["case_id"] != caseID || receipt["document_id"] != documentID || receipt["submitted_by"] != "demo-reviewer" {
		t.Fatalf("receipt must not imply verification: %v", received)
	}
	assertWireSchema(t, openAPI(t), "EvidenceReceiptResult", received)
	assertNoEvidenceDiagnostics(t, received)
	receiptID := receipt["id"].(string)
	status, replay = api.send(t, http.MethodPost, requestPath+"/receipts", receiptInput, "receipt-request")
	if status != http.StatusCreated || !reflect.DeepEqual(received, replay) {
		t.Fatalf("receipt replay: %d %v", status, replay)
	}
	changedReceipt := maps.Clone(receiptInput)
	changedReceipt["document_id"] = "another-document"
	status, body = api.send(t, http.MethodPost, requestPath+"/receipts", changedReceipt, "receipt-request")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	status, body = api.send(t, http.MethodPost, requestPath+"/receipts", receiptInput, "stale-receipt")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	for _, field := range []string{"case_id", "request_id"} {
		invalid := maps.Clone(receiptInput)
		invalid[field] = "different-id"
		status, body = api.send(t, http.MethodPost, requestPath+"/receipts", invalid, "wrong-identity")
		requireEvidenceHTTPError(t, status, http.StatusUnprocessableEntity, body)
	}

	api.login(t, "demo-admin", "admin-demo")
	api.base = legacyURL
	status, later := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated || later["attempt_id"] == sourceAttempt {
		t.Fatalf("start later review: %d %v", status, later)
	}
	laterRun, laterAttempt := later["id"].(string), later["attempt_id"].(string)
	status, laterWorkspace := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+laterRun, nil)
	if status != http.StatusOK {
		t.Fatalf("read later review: %d %v", status, laterWorkspace)
	}
	observed := evidenceEvaluation(t, laterWorkspace, "warranty", "conflict")
	for _, field := range []string{"rule_id", "rule_version", "capability_id"} {
		if missing[field] != observed[field] {
			t.Fatalf("later evaluation changed %s: source=%v later=%v", field, missing, observed)
		}
	}
	var doc map[string]any
	for _, raw := range laterWorkspace["documents"].([]any) {
		candidate := raw.(map[string]any)
		if candidate["id"] == documentID {
			doc = candidate
			break
		}
	}
	if doc == nil || doc["parsed_document_id"] == nil || doc["text"] == nil {
		t.Fatalf("later review omitted the submitted parsed document: %v", laterWorkspace["documents"])
	}
	text := doc["text"].(string)
	verify := map[string]any{
		"run_id": laterRun, "attempt_id": laterAttempt, "evaluation_id": observed["id"],
		"evidence": []any{map[string]any{"document_id": documentID, "parsed_document_id": doc["parsed_document_id"],
			"quote": text, "start": 0, "end": len([]rune(text))}},
		"reason": "核对提交材料与实际复核结果", "expected_version": 2,
	}
	api.base = coreURL
	verifyPath := requestPath + "/receipts/" + receiptID + "/verify"

	api.login(t, "demo-clerk", "clerk-demo")
	status, body = api.send(t, http.MethodPost, verifyPath, verify, "clerk-verify")
	requireEvidenceHTTPError(t, status, http.StatusForbidden, body)
	api.login(t, "demo-viewer", "viewer-demo")
	status, body = api.send(t, http.MethodPost, verifyPath, verify, "viewer-verify")
	requireEvidenceHTTPError(t, status, http.StatusForbidden, body)
	api.login(t, "demo-reviewer", "reviewer-demo")
	for _, field := range []string{"case_id", "request_id", "receipt_id"} {
		invalid := maps.Clone(verify)
		invalid[field] = "different-id"
		status, body = api.send(t, http.MethodPost, verifyPath, invalid, "wrong-identity")
		requireEvidenceHTTPError(t, status, http.StatusUnprocessableEntity, body)
	}
	status, body = api.send(t, http.MethodPost, requestPath+"/receipts/"+secondReceiptID+"/verify", verify, "wrong-receipt")
	requireEvidenceHTTPError(t, status, http.StatusNotFound, body)
	staleVerify := maps.Clone(verify)
	staleVerify["expected_version"] = 1
	status, body = api.send(t, http.MethodPost, verifyPath, staleVerify, "stale-verify")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	oldAttempt := maps.Clone(verify)
	oldAttempt["run_id"], oldAttempt["attempt_id"] = sourceRun, sourceAttempt
	status, body = api.send(t, http.MethodPost, verifyPath, oldAttempt, "old-attempt")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	forged := maps.Clone(verify)
	forged["evidence"] = []any{map[string]any{"document_id": documentID, "parsed_document_id": doc["parsed_document_id"],
		"quote": "并不存在的文字", "start": 0, "end": len([]rune("并不存在的文字"))}}
	status, body = api.send(t, http.MethodPost, verifyPath, forged, "forged-quote")
	requireEvidenceHTTPError(t, status, http.StatusUnprocessableEntity, body)
	forgedDigest := maps.Clone(verify)
	forgedDigest["evidence"] = []any{map[string]any{"document_id": documentID, "parsed_document_id": doc["parsed_document_id"],
		"normalized_digest": strings.Repeat("0", 64), "quote": text, "start": 0, "end": len([]rune(text))}}
	status, body = api.send(t, http.MethodPost, verifyPath, forgedDigest, "forged-digest")
	requireEvidenceHTTPError(t, status, http.StatusUnprocessableEntity, body)
	status, body = api.send(t, http.MethodPost, secondPath+"/receipts/"+secondReceiptID+"/verify", verify, "unmet-condition")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	status, secondRead := api.do(t, http.MethodGet, secondPath, nil)
	if status != http.StatusOK || secondRead["status"] != "received" || secondRead["version"] != float64(2) || secondRead["verification"] != nil {
		t.Fatalf("unmet verification condition changed request: %d %v", status, secondRead)
	}
	status, read = api.do(t, http.MethodGet, requestPath, nil)
	if status != http.StatusOK || read["status"] != "received" || read["version"] != float64(2) || read["verification"] != nil {
		t.Fatalf("failed verification changed request: %d %v", status, read)
	}

	verify["case_id"], verify["request_id"], verify["receipt_id"] = caseID, requestID, receiptID
	status, verified := api.send(t, http.MethodPost, verifyPath, verify, "verify-request")
	if status != http.StatusOK || verified["status"] != "verified" || verified["version"] != float64(3) {
		t.Fatalf("authorized verification: %d %v", status, verified)
	}
	verification := verified["verification"].(map[string]any)
	if verification["receipt_id"] != receiptID || verification["run_id"] != laterRun ||
		verification["attempt_id"] != laterAttempt || verification["evaluation_id"] != observed["id"] ||
		verification["verified_by"] != "demo-reviewer" || verification["relation"] != "conflict" {
		t.Fatalf("verification lost its observed identity: %v", verification)
	}
	assertWireSchema(t, openAPI(t), "EvidenceRequest", verified)
	assertNoEvidenceDiagnostics(t, verified)
	status, replay = api.send(t, http.MethodPost, verifyPath, verify, "verify-request")
	if status != http.StatusOK || !reflect.DeepEqual(verified, replay) {
		t.Fatalf("verification replay: %d %v", status, replay)
	}
	changedVerify := maps.Clone(verify)
	changedVerify["reason"] = "替换理由"
	status, body = api.send(t, http.MethodPost, verifyPath, changedVerify, "verify-request")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	otherReceiptReplay := maps.Clone(verify)
	delete(otherReceiptReplay, "receipt_id")
	status, body = api.send(t, http.MethodPost, requestPath+"/receipts/"+secondReceiptID+"/verify", otherReceiptReplay, "verify-request")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	otherRequestReplay := maps.Clone(verify)
	delete(otherRequestReplay, "request_id")
	status, body = api.send(t, http.MethodPost, secondPath+"/receipts/"+receiptID+"/verify", otherRequestReplay, "verify-request")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	status, body = api.send(t, http.MethodPost, verifyPath, verify, "new-verify-key")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	status, replay = api.send(t, http.MethodPost, requestPath+"/receipts", receiptInput, "receipt-request")
	if status != http.StatusCreated || !reflect.DeepEqual(received, replay) {
		t.Fatalf("later verification changed receipt replay: %d %v", status, replay)
	}
	status, history = api.do(t, http.MethodGet, requestPath+"/history", nil)
	if status != http.StatusOK || len(history["items"].([]any)) != 3 {
		t.Fatalf("history: %d %v", status, history)
	}
	events := history["items"].([]any)
	for i, expected := range []string{"created", "received", "verified"} {
		event := events[i].(map[string]any)
		if event["type"] != expected || event["request"].(map[string]any)["version"] != float64(i+1) {
			t.Fatalf("history event %d: %v", i, event)
		}
	}
	if !reflect.DeepEqual(events[1].(map[string]any)["receipt"], receipt) {
		t.Fatalf("history lost exact receipt: %v", history)
	}
	assertWireSchema(t, openAPI(t), "EvidenceRequestHistory", history)
	assertNoEvidenceDiagnostics(t, history)

	// A matching result cannot close a request if the stored check does not
	// independently identify the submitted material as its evidence.
	api.login(t, "demo-admin", "admin-demo")
	api.base = legacyURL
	compatibleDocID := api.upload(t, caseID, "draft_contract", "compatible.txt", "text/plain", []byte("拟签合同约定质保36个月。"))
	api.base = coreURL
	status, compatibleReceipt := api.send(t, http.MethodPost, secondPath+"/receipts", map[string]any{
		"document_id": compatibleDocID, "expected_version": 2,
	}, "compatible-receipt")
	if status != http.StatusCreated || compatibleReceipt["request"].(map[string]any)["version"] != float64(3) {
		t.Fatalf("submit compatible document: %d %v", status, compatibleReceipt)
	}
	compatibleReceiptID := compatibleReceipt["receipt"].(map[string]any)["id"].(string)
	api.base = legacyURL
	status, compatibleRun := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("start compatible review: %d %v", status, compatibleRun)
	}
	compatibleRunID := compatibleRun["id"].(string)
	status, compatibleWorkspace := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+compatibleRunID, nil)
	if status != http.StatusOK {
		t.Fatalf("read compatible review: %d %v", status, compatibleWorkspace)
	}
	compatibleCheck := evidenceEvaluation(t, compatibleWorkspace, "warranty", "compatible")
	var compatibleDoc map[string]any
	for _, raw := range compatibleWorkspace["documents"].([]any) {
		candidate := raw.(map[string]any)
		if candidate["id"] == compatibleDocID {
			compatibleDoc = candidate
			break
		}
	}
	if compatibleDoc == nil || compatibleDoc["parsed_document_id"] == nil || compatibleDoc["text"] == nil {
		t.Fatalf("compatible review omitted submitted parsed document: %v", compatibleWorkspace["documents"])
	}
	compatibleText := compatibleDoc["text"].(string)
	api.base = coreURL
	api.login(t, "demo-reviewer", "reviewer-demo")
	status, body = api.send(t, http.MethodPost, secondPath+"/receipts/"+compatibleReceiptID+"/verify", map[string]any{
		"run_id": compatibleRunID, "attempt_id": compatibleRun["attempt_id"], "evaluation_id": compatibleCheck["id"],
		"evidence": []any{map[string]any{"document_id": compatibleDocID, "parsed_document_id": compatibleDoc["parsed_document_id"],
			"quote": compatibleText, "start": 0, "end": len([]rune(compatibleText))}},
		"reason": "核对补充材料与复核结果", "expected_version": 3,
	}, "compatible-without-provenance")
	requireEvidenceHTTPError(t, status, http.StatusConflict, body)
	status, secondRead = api.do(t, http.MethodGet, secondPath, nil)
	if status != http.StatusOK || secondRead["status"] != "received" || secondRead["version"] != float64(3) || secondRead["verification"] != nil {
		t.Fatalf("unlinked compatible result closed request: %d %v", status, secondRead)
	}
}
