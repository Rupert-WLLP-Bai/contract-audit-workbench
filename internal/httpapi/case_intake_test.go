package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"

	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
)

func newIntakeStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newIntakeAPI(t *testing.T, st *store.Store, uploader store.CaseIntakeUploader) *testAPI {
	t.Helper()
	st.UseModels(scriptModels{})
	server := httptest.NewServer(httpapi.Handler(st, httpapi.Options{IntakeUploader: uploader}))
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &testAPI{base: server.URL, client: &http.Client{Jar: jar}}
}

func intakeForm(t *testing.T, fields map[string]string, filename string, content []byte) (string, []byte) {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	disposition := mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": filename})
	if disposition == "" {
		t.Fatal("filename cannot be encoded")
	}
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {disposition},
		"Content-Type":        {"text/plain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return writer.FormDataContentType(), payload.Bytes()
}

type intakeHTTPResult struct {
	status int
	body   []byte
	err    error
}

func postIntakeBytes(client *http.Client, base, key, contentType string, payload []byte) intakeHTTPResult {
	request, err := http.NewRequest(http.MethodPost, base+"/api/intakes", bytes.NewReader(payload))
	if err != nil {
		return intakeHTTPResult{err: err}
	}
	request.Header.Set("Origin", request.URL.Scheme+"://"+request.URL.Host)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", key)
	response, err := client.Do(request)
	if err != nil {
		return intakeHTTPResult{err: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return intakeHTTPResult{err: err}
	}
	return intakeHTTPResult{status: response.StatusCode, body: body}
}

func intakeJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("intake response is not JSON: %v %s", err, body)
	}
	return out
}

func intakeFields(title string) map[string]string {
	return map[string]string{"title": title, "contract_type": "service", "business_line": "ENT", "scenario": "cloud_services"}
}

func TestCaseIntakeHTTPFreezesAndReplaysExactReceipt(t *testing.T) {
	st := newIntakeStore(t)
	api := newIntakeAPI(t, st, store.NewDirectCaseIntakeUploader(st))
	api.login(t, "demo-admin", "admin-demo")
	contentType, payload := intakeForm(t, intakeFields("端到端主合同接收"), "main.txt", []byte("主合同第一版。"))
	const key = "http-intake-freeze-1"
	first := postIntakeBytes(api.client, api.base, key, contentType, payload)
	if first.err != nil || first.status != http.StatusCreated {
		t.Fatalf("first intake status=%d err=%v body=%s", first.status, first.err, first.body)
	}
	receipt := intakeJSON(t, first.body)
	if len(receipt) != 7 {
		t.Fatalf("completed receipt has an unexpected shape: %+v", receipt)
	}
	operationID := requiredString(t, receipt, "operation_id")
	caseID := requiredString(t, receipt, "case_id")
	materialID := requiredString(t, receipt, "material_id")
	documentID := requiredString(t, receipt, "document_id")
	if receipt["state"] != "completed" || receipt["material_version"] != float64(1) || receipt["retryable"] != false {
		t.Fatalf("completed receipt values drifted: %+v", receipt)
	}
	if !strings.HasPrefix(operationID, "intake_") || !strings.HasPrefix(caseID, "case_") ||
		!strings.HasPrefix(materialID, "mat_") || !strings.HasPrefix(documentID, "doc_") {
		t.Fatalf("completed receipt identifiers are not stable prefixes: %+v", receipt)
	}

	replay := postIntakeBytes(api.client, api.base, key, contentType, payload)
	if replay.err != nil || replay.status != http.StatusCreated || !bytes.Equal(replay.body, first.body) {
		t.Fatalf("same-key replay did not return the frozen 201 bytes: status=%d err=%v body=%s", replay.status, replay.err, replay.body)
	}
	status, byID := api.bytes(t, http.MethodGet, "/api/intakes/"+operationID, nil, "")
	if status != http.StatusOK || !bytes.Equal(byID, first.body) {
		t.Fatalf("GET by operation id status=%d body=%s", status, byID)
	}
	status, byKey := api.bytes(t, http.MethodGet, "/api/intakes?key="+url.QueryEscape(key), nil, "")
	if status != http.StatusOK || !bytes.Equal(byKey, first.body) {
		t.Fatalf("GET by key status=%d body=%s", status, byKey)
	}
	status, missing := api.bytes(t, http.MethodGet, "/api/intakes", nil, "")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("GET without key status=%d body=%s", status, missing)
	}
	status, unknown := api.bytes(t, http.MethodGet, "/api/intakes?key=unknown-intake-key", nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("GET with unknown key status=%d body=%s", status, unknown)
	}

	changedFileType, changedFilePayload := intakeForm(t, intakeFields("端到端主合同接收"), "main.txt", []byte("换了内容。"))
	changedFile := postIntakeBytes(api.client, api.base, key, changedFileType, changedFilePayload)
	if changedFile.err != nil || changedFile.status != http.StatusConflict {
		t.Fatalf("changed file under the same key status=%d err=%v body=%s", changedFile.status, changedFile.err, changedFile.body)
	}
	changedMetadataType, changedMetadataPayload := intakeForm(t, intakeFields("改了标题"), "main.txt", []byte("主合同第一版。"))
	changedMetadata := postIntakeBytes(api.client, api.base, key, changedMetadataType, changedMetadataPayload)
	if changedMetadata.err != nil || changedMetadata.status != http.StatusConflict {
		t.Fatalf("changed metadata under the same key status=%d err=%v body=%s", changedMetadata.status, changedMetadata.err, changedMetadata.body)
	}

	status, replaced := api.uploadRequest(t, caseID, "draft_contract", "main-v2.txt", "text/plain", []byte("主合同第二版。"), "replace", materialID, documentID, "http-intake-replace-1")
	if status != http.StatusCreated || replaced["version"] != float64(2) {
		t.Fatalf("later replacement status=%d body=%+v", status, replaced)
	}
	status, afterReplacement := api.bytes(t, http.MethodGet, "/api/intakes/"+operationID, nil, "")
	if status != http.StatusOK || !bytes.Equal(afterReplacement, first.body) {
		t.Fatalf("receipt changed after a later material replacement: status=%d body=%s", status, afterReplacement)
	}
}

func TestCaseIntakeHTTPConcurrentSameKeyCreatesOneCaseAndPrimary(t *testing.T) {
	st := newIntakeStore(t)
	api := newIntakeAPI(t, st, store.NewDirectCaseIntakeUploader(st))
	api.login(t, "demo-admin", "admin-demo")
	contentType, payload := intakeForm(t, intakeFields("并发幂等建案"), "concurrent.txt", []byte("并发请求使用同一份主合同。"))
	const key = "http-intake-concurrent-1"
	const workers = 4
	results := make(chan intakeHTTPResult, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- postIntakeBytes(api.client, api.base, key, contentType, payload)
		}()
	}
	group.Wait()
	close(results)
	var frozen []byte
	for result := range results {
		if result.err != nil || result.status != http.StatusCreated {
			t.Fatalf("concurrent intake status=%d err=%v body=%s", result.status, result.err, result.body)
		}
		if frozen == nil {
			frozen = result.body
			continue
		}
		if !bytes.Equal(frozen, result.body) {
			t.Fatalf("concurrent completions returned different receipts:\n%s\n%s", frozen, result.body)
		}
	}
	receipt := intakeJSON(t, frozen)
	caseID := requiredString(t, receipt, "case_id")

	status, list := api.do(t, http.MethodGet, "/api/cases", nil)
	if status != http.StatusOK {
		t.Fatalf("list cases status=%d body=%+v", status, list)
	}
	matches := 0
	for _, raw := range list["items"].([]any) {
		item := raw.(map[string]any)
		if item["title"] == "并发幂等建案" {
			matches++
			if item["id"] != caseID {
				t.Fatalf("concurrent intake created a second case: %+v", item)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("concurrent intake created %d cases, want 1", matches)
	}
	status, detail := api.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	if status != http.StatusOK {
		t.Fatalf("case detail status=%d body=%+v", status, detail)
	}
	materials, _ := detail["materials"].([]any)
	if len(materials) != 1 {
		t.Fatalf("concurrent intake created %d materials, want 1: %+v", len(materials), materials)
	}
}

type intakeUploadThenFail struct {
	delegate store.CaseIntakeUploader
}

func (uploader intakeUploadThenFail) UploadPrimaryContract(ctx context.Context, caseID string, request store.PrimaryContractUploadRequest) (store.DocumentUploadResult, error) {
	if uploader.delegate != nil {
		_, _ = uploader.delegate.UploadPrimaryContract(ctx, caseID, request)
	}
	return store.DocumentUploadResult{}, errors.New("simulated lost response after the document commit")
}

func TestCaseIntakeHTTPRecoversLostResponseWithoutAnotherCase(t *testing.T) {
	st := newIntakeStore(t)
	pendingAPI := newIntakeAPI(t, st, intakeUploadThenFail{delegate: store.NewDirectCaseIntakeUploader(st)})
	pendingAPI.login(t, "demo-admin", "admin-demo")
	contentType, payload := intakeForm(t, intakeFields("丢响应后恢复"), "lost.txt", []byte("上传已提交但响应丢失。"))
	const key = "http-intake-lost-response-1"
	lost := postIntakeBytes(pendingAPI.client, pendingAPI.base, key, contentType, payload)
	if lost.err != nil || lost.status != http.StatusAccepted {
		t.Fatalf("lost-response intake status=%d err=%v body=%s", lost.status, lost.err, lost.body)
	}
	pending := intakeJSON(t, lost.body)
	operationID := requiredString(t, pending, "operation_id")
	caseID := requiredString(t, pending, "case_id")
	if pending["state"] != "upload_pending" || pending["retryable"] != true || pending["error_code"] != "document_upload_unavailable" {
		t.Fatalf("pending response leaked an unexpected shape: %+v", pending)
	}
	status, recoveredPending := pendingAPI.bytes(t, http.MethodGet, "/api/intakes?key="+url.QueryEscape(key), nil, "")
	if status != http.StatusOK || !bytes.Equal(recoveredPending, lost.body) {
		t.Fatalf("pending recovery status=%d body=%s", status, recoveredPending)
	}

	retryAPI := newIntakeAPI(t, st, store.NewDirectCaseIntakeUploader(st))
	retryAPI.login(t, "demo-admin", "admin-demo")
	retried := postIntakeBytes(retryAPI.client, retryAPI.base, key, contentType, payload)
	if retried.err != nil || retried.status != http.StatusCreated {
		t.Fatalf("retry after lost response status=%d err=%v body=%s", retried.status, retried.err, retried.body)
	}
	completed := intakeJSON(t, retried.body)
	if completed["operation_id"] != operationID || completed["case_id"] != caseID ||
		completed["state"] != "completed" || completed["material_version"] != float64(1) {
		t.Fatalf("retry did not complete the original operation: %+v", completed)
	}
	status, list := retryAPI.do(t, http.MethodGet, "/api/cases", nil)
	if status != http.StatusOK {
		t.Fatalf("list cases status=%d body=%+v", status, list)
	}
	matches := 0
	for _, raw := range list["items"].([]any) {
		if raw.(map[string]any)["title"] == "丢响应后恢复" {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("lost-response recovery created %d cases, want 1", matches)
	}
	status, detail := retryAPI.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	if status != http.StatusOK {
		t.Fatalf("case detail status=%d body=%+v", status, detail)
	}
	if materials, _ := detail["materials"].([]any); len(materials) != 1 {
		t.Fatalf("lost-response recovery created %d materials, want 1: %+v", len(materials), materials)
	}
}

func TestCaseIntakeHTTPRechecksIdentityAndPermission(t *testing.T) {
	st := newIntakeStore(t)
	api := newIntakeAPI(t, st, store.NewDirectCaseIntakeUploader(st))
	api.login(t, "demo-admin", "admin-demo")
	contentType, payload := intakeForm(t, intakeFields("权限复核建案"), "auth.txt", []byte("只允许当前经办继续读取。"))
	const key = "http-intake-auth-1"
	created := postIntakeBytes(api.client, api.base, key, contentType, payload)
	if created.err != nil || created.status != http.StatusCreated {
		t.Fatalf("create status=%d err=%v body=%s", created.status, created.err, created.body)
	}
	receipt := intakeJSON(t, created.body)
	operationID := requiredString(t, receipt, "operation_id")
	casesPath := "/api/intakes?key=" + url.QueryEscape(key)

	api.login(t, "demo-reviewer", "reviewer-demo")
	status, body := api.bytes(t, http.MethodGet, casesPath, nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("non-member reviewer recovered another actor's operation: status=%d body=%s", status, body)
	}
	status, body = api.bytes(t, http.MethodGet, "/api/intakes/"+operationID, nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("non-member reviewer read the operation by id: status=%d body=%s", status, body)
	}

	api.login(t, "demo-otherorg-clerk", "otherorg-clerk-demo")
	status, body = api.bytes(t, http.MethodGet, casesPath, nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("another organization recovered the operation: status=%d body=%s", status, body)
	}

	api.login(t, "demo-viewer", "viewer-demo")
	viewerType, viewerPayload := intakeForm(t, intakeFields("只读角色不能建案"), "viewer.txt", []byte("不应创建。"))
	viewer := postIntakeBytes(api.client, api.base, "http-intake-auth-viewer", viewerType, viewerPayload)
	if viewer.err != nil || viewer.status != http.StatusForbidden {
		t.Fatalf("viewer create status=%d err=%v body=%s", viewer.status, viewer.err, viewer.body)
	}

	api.login(t, "demo-admin", "admin-demo")
	missingKey := postIntakeBytes(api.client, api.base, "", contentType, payload)
	if missingKey.err != nil || missingKey.status != http.StatusUnprocessableEntity {
		t.Fatalf("missing idempotency key status=%d err=%v body=%s", missingKey.status, missingKey.err, missingKey.body)
	}
	request, err := http.NewRequest(http.MethodPost, api.base+"/api/intakes", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", request.URL.Scheme+"://"+request.URL.Host)
	request.Header.Set("Content-Type", contentType)
	request.Header.Add("Idempotency-Key", key)
	request.Header.Add("Idempotency-Key", key)
	response, err := api.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate idempotency header status=%d body=%s", response.StatusCode, duplicate)
	}
	intentType, intentPayload := intakeForm(t, map[string]string{
		"title": "错误意图", "contract_type": "service", "business_line": "ENT", "scenario": "cloud_services", "intent": "replace",
	}, "intent.txt", []byte("不应创建。"))
	wrongIntent := postIntakeBytes(api.client, api.base, "http-intake-auth-intent", intentType, intentPayload)
	if wrongIntent.err != nil || wrongIntent.status != http.StatusUnprocessableEntity {
		t.Fatalf("non-fixed intent status=%d err=%v body=%s", wrongIntent.status, wrongIntent.err, wrongIntent.body)
	}

	st.UseReadOnly(true)
	readOnlyType, readOnlyPayload := intakeForm(t, intakeFields("只读模式建案"), "readonly.txt", []byte("不应创建。"))
	readOnly := postIntakeBytes(api.client, api.base, "http-intake-auth-readonly", readOnlyType, readOnlyPayload)
	if readOnly.err != nil || readOnly.status != http.StatusServiceUnavailable {
		t.Fatalf("read-only create status=%d err=%v body=%s", readOnly.status, readOnly.err, readOnly.body)
	}
	status, body = api.bytes(t, http.MethodGet, casesPath, nil, "")
	if status != http.StatusOK || !bytes.Equal(body, created.body) {
		t.Fatalf("read-only recovery lost the frozen receipt: status=%d body=%s", status, body)
	}
}

func TestCaseIntakeHTTPResumesAcrossSessionsAndArchivesPersonalPending(t *testing.T) {
	st := newIntakeStore(t)
	firstDevice := newIntakeAPI(t, st, nil)
	firstDevice.login(t, "demo-admin", "admin-demo")
	firstFile := []byte("第一份原始主合同。")
	firstType, firstPayload := intakeForm(t, intakeFields("跨设备第一项"), "first.txt", firstFile)
	secondType, secondPayload := intakeForm(t, intakeFields("跨设备第二项"), "second.txt", []byte("第二份原始主合同。"))
	first := postIntakeBytes(firstDevice.client, firstDevice.base, "multi-device-first", firstType, firstPayload)
	second := postIntakeBytes(firstDevice.client, firstDevice.base, "multi-device-second", secondType, secondPayload)
	if first.err != nil || second.err != nil || first.status != http.StatusAccepted || second.status != http.StatusAccepted {
		t.Fatalf("create pending first=%+v second=%+v", first, second)
	}
	firstReceipt, secondReceipt := intakeJSON(t, first.body), intakeJSON(t, second.body)
	firstID, firstCase := requiredString(t, firstReceipt, "operation_id"), requiredString(t, firstReceipt, "case_id")
	secondID, secondCase := requiredString(t, secondReceipt, "operation_id"), requiredString(t, secondReceipt, "case_id")
	status, body := firstDevice.bytes(t, http.MethodPost, "/api/cases/"+firstCase+"/members", strings.NewReader(`{"actor_id":"demo-reviewer"}`), "application/json")
	if status != http.StatusOK {
		t.Fatalf("grant case access: status=%d body=%s", status, body)
	}

	// A fresh cookie jar has no browser continuation and must authenticate again.
	secondDevice := newIntakeAPI(t, st, store.NewDirectCaseIntakeUploader(st))
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/intakes/pending", nil, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous pending list: status=%d body=%s", status, body)
	}
	secondDevice.login(t, "demo-reviewer", "reviewer-demo")
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/cases/"+firstCase, nil, "")
	if status != http.StatusOK {
		t.Fatalf("member cannot read case fixture: status=%d body=%s", status, body)
	}
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/intakes/"+firstID+"/continuation", nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("case membership exposed another actor's replay identity: status=%d body=%s", status, body)
	}
	status, body = secondDevice.bytes(t, http.MethodPost, "/api/intakes/"+firstID+"/archive", strings.NewReader(`{}`), "application/json")
	if status != http.StatusNotFound {
		t.Fatalf("member archived another actor's command: status=%d body=%s", status, body)
	}

	secondDevice.login(t, "demo-admin", "admin-demo")
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/intakes/pending?limit=1", nil, "")
	var page store.PendingCaseIntakesPage
	if status != http.StatusOK || json.Unmarshal(body, &page) != nil || len(page.Items) != 1 || page.Items[0].OperationID != secondID || page.NextCursor == nil {
		t.Fatalf("fresh session first pending page: status=%d body=%s", status, body)
	}
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/intakes/pending?limit=1&cursor="+url.QueryEscape(*page.NextCursor), nil, "")
	if status != http.StatusOK || json.Unmarshal(body, &page) != nil || len(page.Items) != 1 || page.Items[0].OperationID != firstID || page.NextCursor != nil {
		t.Fatalf("second pending page duplicated or lost command: status=%d body=%s", status, body)
	}
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/intakes/"+firstID+"/continuation", nil, "")
	var continuation store.CaseIntakeContinuation
	if status != http.StatusOK || json.Unmarshal(body, &continuation) != nil || !continuation.CanResume || continuation.IdempotencyKey == nil || *continuation.IdempotencyKey != "multi-device-first" || continuation.Request == nil {
		t.Fatalf("fresh session lost replay identity: status=%d body=%s", status, body)
	}
	if continuation.Request.Title != "跨设备第一项" || continuation.Request.FileName != "first.txt" || continuation.Request.FileSize != int64(len(firstFile)) || continuation.CaseID != firstCase {
		t.Fatalf("restored another request: %+v", continuation)
	}
	wrongType, wrongPayload := intakeForm(t, intakeFields("跨设备第一项"), "first.txt", []byte("内容已发生变化。"))
	wrong := postIntakeBytes(secondDevice.client, secondDevice.base, *continuation.IdempotencyKey, wrongType, wrongPayload)
	if wrong.err != nil || wrong.status != http.StatusConflict {
		t.Fatalf("different file resumed old command: status=%d err=%v body=%s", wrong.status, wrong.err, wrong.body)
	}
	completed := postIntakeBytes(secondDevice.client, secondDevice.base, *continuation.IdempotencyKey, firstType, firstPayload)
	if completed.err != nil || completed.status != http.StatusCreated || requiredString(t, intakeJSON(t, completed.body), "case_id") != firstCase {
		t.Fatalf("resume created another case: status=%d err=%v body=%s", completed.status, completed.err, completed.body)
	}
	status, archived := secondDevice.bytes(t, http.MethodPost, "/api/intakes/"+secondID+"/archive", strings.NewReader(`{}`), "application/json")
	if status != http.StatusOK || requiredString(t, intakeJSON(t, archived), "case_id") != secondCase {
		t.Fatalf("archive pending: status=%d body=%s", status, archived)
	}
	status, body = secondDevice.bytes(t, http.MethodPost, "/api/intakes/"+secondID+"/archive", strings.NewReader(`{}`), "application/json")
	if status != http.StatusOK || !bytes.Equal(body, archived) {
		t.Fatalf("archive fact changed on replay: status=%d body=%s want=%s", status, body, archived)
	}
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/intakes/"+secondID, nil, "")
	if status != http.StatusConflict || !bytes.Contains(body, []byte(`"code":"intake_archived"`)) {
		t.Fatalf("archived command advertised pending retry: status=%d body=%s", status, body)
	}
	retried := postIntakeBytes(secondDevice.client, secondDevice.base, "multi-device-second", secondType, secondPayload)
	if retried.err != nil || retried.status != http.StatusConflict {
		t.Fatalf("archived command restarted: status=%d err=%v body=%s", retried.status, retried.err, retried.body)
	}
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/cases/"+secondCase, nil, "")
	if status != http.StatusOK {
		t.Fatalf("personal archive removed original case: status=%d body=%s", status, body)
	}
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/intakes/pending", nil, "")
	if status != http.StatusOK || json.Unmarshal(body, &page) != nil || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("completed or archived commands remain pending: status=%d body=%s", status, body)
	}
	status, body = secondDevice.bytes(t, http.MethodGet, "/api/intakes/"+firstID, nil, "")
	if status != http.StatusOK || !bytes.Equal(body, completed.body) {
		t.Fatalf("completed receipt changed: status=%d body=%s want=%s", status, body, completed.body)
	}
}
