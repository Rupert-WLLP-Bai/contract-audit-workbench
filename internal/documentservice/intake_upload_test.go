package documentservice

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

const (
	testDocumentWorkerKey = "test-document-worker-credential-0001"
	testDocumentIntakeKey = "test-document-intake-credential-0001"
)

func newIntakeUploadFixture(t *testing.T) (*store.Store, string, *httptest.Server) {
	t.Helper()
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin identity is unavailable")
	}
	caseID, err := st.CreateCaseWithBusiness(admin, "内部上传建案", "service", "ENT", "cloud_services")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(st, Config{
		WorkerCredential: testDocumentWorkerKey, IntakeCredential: testDocumentIntakeKey,
		CPUConcurrency: 2, OCRConcurrency: 1, QueueCapacity: 8, ParseTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler(http.NotFoundHandler()))
	t.Cleanup(server.Close)
	return st, caseID, server
}

func intakeUploadPayload(t *testing.T, actor, tenant, org, caseID, key, filename string, content []byte) (string, []byte) {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	for name, value := range map[string]string{
		"actor_id": actor, "tenant_id": tenant, "org_id": org, "case_id": caseID, "idempotency_key": key,
	} {
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

func postInternalIntake(client *http.Client, serverURL, credential, contentType string, payload []byte) (int, []byte) {
	request, err := http.NewRequest(http.MethodPost, serverURL+"/internal/v1/intake-uploads", bytes.NewReader(payload))
	if err != nil {
		return 0, []byte(err.Error())
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Contract-Service-Key", credential)
	response, err := client.Do(request)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, []byte(err.Error())
	}
	return response.StatusCode, body
}

func decodeDocumentUpload(t *testing.T, body []byte) store.DocumentUploadResult {
	t.Helper()
	var result store.DocumentUploadResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("document intake response is not JSON: %v %s", err, body)
	}
	return result
}

func TestDocumentIntakeUploadAuthenticatesActorAndReplays(t *testing.T) {
	st, caseID, server := newIntakeUploadFixture(t)
	client := &http.Client{Timeout: 10 * time.Second}
	contentType, payload := intakeUploadPayload(t, "demo-admin", accounts.LocalDemoTenant, "demo-org", caseID,
		"internal-intake-replay", "main.txt", []byte("内部上传主合同。"))
	status, body := postInternalIntake(client, server.URL, testDocumentIntakeKey, contentType, payload)
	if status != http.StatusCreated {
		t.Fatalf("internal intake upload status=%d body=%s", status, body)
	}
	first := decodeDocumentUpload(t, body)
	if first.DocumentID == "" || first.MaterialID == "" || first.Version != 1 || first.Replayed {
		t.Fatalf("internal intake upload receipt drifted: %+v", first)
	}
	status, body = postInternalIntake(client, server.URL, testDocumentIntakeKey, contentType, payload)
	if status != http.StatusOK {
		t.Fatalf("internal intake replay status=%d body=%s", status, body)
	}
	replay := decodeDocumentUpload(t, body)
	if replay.DocumentID != first.DocumentID || replay.MaterialID != first.MaterialID || replay.Version != 1 || !replay.Replayed {
		t.Fatalf("internal intake replay drifted: first=%+v replay=%+v", first, replay)
	}

	changedType, changedPayload := intakeUploadPayload(t, "demo-admin", accounts.LocalDemoTenant, "demo-org", caseID,
		"internal-intake-replay", "main.txt", []byte("不同字节。"))
	status, body = postInternalIntake(client, server.URL, testDocumentIntakeKey, changedType, changedPayload)
	if status != http.StatusConflict {
		t.Fatalf("changed internal intake payload status=%d body=%s", status, body)
	}
	status, body = postInternalIntake(client, server.URL, testDocumentWorkerKey, contentType, payload)
	if status != http.StatusUnauthorized {
		t.Fatalf("worker credential must not authorize the dedicated intake entry: status=%d body=%s", status, body)
	}
	status, body = postInternalIntake(client, server.URL, testDocumentWorkerKey+":wrong", contentType, payload)
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong internal intake credential status=%d body=%s", status, body)
	}
	viewerType, viewerPayload := intakeUploadPayload(t, "demo-viewer", accounts.LocalDemoTenant, "demo-org", caseID,
		"internal-intake-viewer", "viewer.txt", []byte("只读角色不能上传。"))
	status, body = postInternalIntake(client, server.URL, testDocumentIntakeKey, viewerType, viewerPayload)
	if status != http.StatusForbidden {
		t.Fatalf("viewer internal intake upload status=%d body=%s", status, body)
	}
	foreignType, foreignPayload := intakeUploadPayload(t, "demo-admin", accounts.LocalDemoTenant2, "demo-org-2", caseID,
		"internal-intake-foreign", "foreign.txt", []byte("跨租户断言。"))
	status, body = postInternalIntake(client, server.URL, testDocumentIntakeKey, foreignType, foreignPayload)
	if status != http.StatusForbidden {
		t.Fatalf("foreign tenant assertion status=%d body=%s", status, body)
	}
	admin, _ := accounts.ByID("demo-admin")
	detail, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Materials) != 1 || len(detail.MaterialVersions) != 1 || detail.Materials[0].Purpose != "primary_contract" {
		t.Fatalf("internal intake did not produce exactly one primary material/version: %+v/%+v", detail.Materials, detail.MaterialVersions)
	}
}

func TestDocumentIntakeUploadConcurrentSameKeyCreatesOneMaterial(t *testing.T) {
	st, caseID, server := newIntakeUploadFixture(t)
	client := &http.Client{Timeout: 20 * time.Second}
	contentType, payload := intakeUploadPayload(t, "demo-admin", accounts.LocalDemoTenant, "demo-org", caseID,
		"internal-intake-concurrent", "concurrent.txt", []byte("并发内部上传主合同。"))
	const workers = 4
	results := make(chan struct {
		status int
		body   []byte
	}, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			status, body := postInternalIntake(client, server.URL, testDocumentIntakeKey, contentType, payload)
			results <- struct {
				status int
				body   []byte
			}{status: status, body: body}
		}()
	}
	group.Wait()
	close(results)
	var materialID, documentID string
	created := 0
	for result := range results {
		if result.status != http.StatusCreated && result.status != http.StatusOK {
			t.Fatalf("concurrent internal intake status=%d body=%s", result.status, result.body)
		}
		receipt := decodeDocumentUpload(t, result.body)
		if receipt.MaterialID == "" || receipt.DocumentID == "" || receipt.Version != 1 {
			t.Fatalf("concurrent internal intake receipt drifted: %+v", receipt)
		}
		if materialID == "" {
			materialID, documentID = receipt.MaterialID, receipt.DocumentID
		} else if materialID != receipt.MaterialID || documentID != receipt.DocumentID {
			t.Fatalf("concurrent internal intake created multiple materials: %s/%s vs %s/%s", materialID, documentID, receipt.MaterialID, receipt.DocumentID)
		}
		if !receipt.Replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("concurrent internal intake reported %d fresh creations, want 1", created)
	}
	admin, _ := accounts.ByID("demo-admin")
	detail, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Materials) != 1 || len(detail.MaterialVersions) != 1 {
		t.Fatalf("concurrent internal intake created %d materials/%d versions, want 1/1", len(detail.Materials), len(detail.MaterialVersions))
	}
}
