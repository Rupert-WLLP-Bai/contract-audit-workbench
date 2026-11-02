package documentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

const (
	testDocumentWorkerKey = "test-document-worker-credential-0001"
	testDocumentIntakeKey = "test-document-intake-credential-0001"
)

type staticIntakeAuthority struct {
	entries map[string]store.DocumentIntakeAuthority
}

func (resolver *staticIntakeAuthority) Resolve(_ context.Context, ticket, digest string) (store.DocumentIntakeAuthority, error) {
	authority, ok := resolver.entries[ticket]
	if !ok || authority.BodySHA256 != digest {
		return store.DocumentIntakeAuthority{}, errors.New("ticket authority mismatch")
	}
	return authority, nil
}

func newIntakeUploadFixture(t *testing.T) (*store.Store, string, *staticIntakeAuthority, *httptest.Server) {
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
	resolver := &staticIntakeAuthority{entries: map[string]store.DocumentIntakeAuthority{}}
	service, err := New(st, Config{
		WorkerCredential: testDocumentWorkerKey, IntakeCredential: testDocumentIntakeKey,
		CPUConcurrency: 2, OCRConcurrency: 1, QueueCapacity: 8, ParseTimeout: 15 * time.Second,
		intakeAuthority: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler(http.NotFoundHandler()))
	t.Cleanup(server.Close)
	return st, caseID, resolver, server
}

func registerIntakeAuthority(resolver *staticIntakeAuthority, ticket, actor, tenant, org, caseID, key, filename, content string, role string) {
	digest := sha256.Sum256([]byte(content))
	resolver.entries[ticket] = store.DocumentIntakeAuthority{
		ActorID: actor, TenantID: tenant, OrgID: org, Role: role,
		Issuer: "https://identity.example/test", Subject: actor, CaseID: caseID,
		OperationID: "intake-test", UploadKey: key, Filename: filename, MediaType: "text/plain",
		BodySHA256: hex.EncodeToString(digest[:]), Action: "create_primary_contract",
		Grants: []access.Grant{{Role: "case_owner", Scope: "case", CaseID: caseID}},
	}
}

func intakeUploadPayload(t *testing.T, ticket, caseID, filename string, content []byte) (string, []byte) {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	for name, value := range map[string]string{
		"authority_ticket": ticket, "case_id": caseID,
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
	st, caseID, resolver, server := newIntakeUploadFixture(t)
	client := &http.Client{Timeout: 10 * time.Second}
	ticket := "iat_" + strings.Repeat("a", 64)
	content := []byte("内部上传主合同。")
	registerIntakeAuthority(resolver, ticket, "demo-admin", accounts.LocalDemoTenant, "demo-org", caseID, "internal-intake-replay", "main.txt", string(content), "admin")
	contentType, payload := intakeUploadPayload(t, ticket, caseID, "main.txt", content)
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

	changedType, changedPayload := intakeUploadPayload(t, ticket, caseID, "main.txt", []byte("不同字节。"))
	status, body = postInternalIntake(client, server.URL, testDocumentIntakeKey, changedType, changedPayload)
	if status != http.StatusForbidden {
		t.Fatalf("changed internal intake payload was not rejected by ticket binding: status=%d body=%s", status, body)
	}
	status, body = postInternalIntake(client, server.URL, testDocumentWorkerKey, contentType, payload)
	if status != http.StatusUnauthorized {
		t.Fatalf("worker credential must not authorize the dedicated intake entry: status=%d body=%s", status, body)
	}
	status, body = postInternalIntake(client, server.URL, testDocumentWorkerKey+":wrong", contentType, payload)
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong internal intake credential status=%d body=%s", status, body)
	}
	viewerTicket := "iat_" + strings.Repeat("b", 64)
	viewerContent := "只读角色不能上传。"
	registerIntakeAuthority(resolver, viewerTicket, "demo-viewer", accounts.LocalDemoTenant, "demo-org", caseID, "internal-intake-viewer", "viewer.txt", viewerContent, "viewer")
	viewerType, viewerPayload := intakeUploadPayload(t, viewerTicket, caseID, "viewer.txt", []byte(viewerContent))
	status, body = postInternalIntake(client, server.URL, testDocumentIntakeKey, viewerType, viewerPayload)
	if status != http.StatusForbidden {
		t.Fatalf("viewer internal intake upload status=%d body=%s", status, body)
	}
	foreignTicket := "iat_" + strings.Repeat("c", 64)
	foreignContent := "跨租户断言。"
	registerIntakeAuthority(resolver, foreignTicket, "demo-admin", accounts.LocalDemoTenant2, "demo-org-2", caseID, "internal-intake-foreign", "foreign.txt", foreignContent, "admin")
	foreignType, foreignPayload := intakeUploadPayload(t, foreignTicket, caseID, "foreign.txt", []byte(foreignContent))
	status, body = postInternalIntake(client, server.URL, testDocumentIntakeKey, foreignType, foreignPayload)
	if status != http.StatusNotFound {
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
	st, caseID, resolver, server := newIntakeUploadFixture(t)
	client := &http.Client{Timeout: 20 * time.Second}
	ticket := "iat_" + strings.Repeat("d", 64)
	content := []byte("并发内部上传主合同。")
	registerIntakeAuthority(resolver, ticket, "demo-admin", accounts.LocalDemoTenant, "demo-org", caseID, "internal-intake-concurrent", "concurrent.txt", string(content), "admin")
	contentType, payload := intakeUploadPayload(t, ticket, caseID, "concurrent.txt", content)
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
