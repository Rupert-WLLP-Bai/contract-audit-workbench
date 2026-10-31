package documentservice_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/documentclient"
	"contractaudit/internal/documentservice"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

const testWorkerCredential = "document-capacity-integration-worker-key-0001"

const testBusinessTime = "2026-06-01T12:00:00Z"

type capacityModel struct {
	entered chan int64
	release chan struct{}
	calls   atomic.Int64
	active  atomic.Int64
	maximum atomic.Int64
}

func newCapacityModel() *capacityModel {
	return &capacityModel{entered: make(chan int64, 16), release: make(chan struct{}, 16)}
}

func (m *capacityModel) releaseAll() {
	for range 16 {
		select {
		case m.release <- struct{}{}:
		default:
			return
		}
	}
}

func (*capacityModel) Name() string            { return "capacity-test" }
func (*capacityModel) ConfigurationID() string { return "capacity-test/v1" }

func (m *capacityModel) Transcribe(ctx context.Context, _ string, _ []byte) (string, error) {
	call := m.calls.Add(1)
	active := m.active.Add(1)
	defer m.active.Add(-1)
	for {
		maximum := m.maximum.Load()
		if active <= maximum || m.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	m.entered <- call
	select {
	case <-m.release:
		return "质保期为36个月", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (*capacityModel) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	return model.Warranty{Relation: "compatible", Disposition: "false_positive"}, nil
}

type blockingObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingObjectStore() *blockingObjectStore {
	return &blockingObjectStore{
		objects: make(map[string][]byte),
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (o *blockingObjectStore) Put(ctx context.Context, key string, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	o.objects[key] = append([]byte(nil), body...)
	o.mu.Unlock()
	return nil
}

func (o *blockingObjectStore) Get(ctx context.Context, key string) ([]byte, error) {
	select {
	case o.entered <- struct{}{}:
	default:
	}
	select {
	case <-o.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	o.mu.Lock()
	body, ok := o.objects[key]
	body = append([]byte(nil), body...)
	o.mu.Unlock()
	if !ok {
		return nil, errors.New("blocked object store key is missing")
	}
	return body, nil
}

func (o *blockingObjectStore) releaseGet() {
	o.once.Do(func() { close(o.release) })
}

type serviceFixture struct {
	core       *store.Store
	document   *store.Store
	worker     *store.Store
	admin      accounts.User
	token      string
	model      *capacityModel
	server     *httptest.Server
	httpClient *http.Client
}

type httpResult struct {
	status int
	body   []byte
	err    error
}

type observingPreparer struct {
	delegate store.DocumentPreparer
	started  chan store.PrepareDocumentRequest
	finished chan error
}

func (p *observingPreparer) PrepareAttemptDocument(ctx context.Context, request store.PrepareDocumentRequest) (store.FrozenDocument, error) {
	select {
	case p.started <- request:
	default:
	}
	frozen, err := p.delegate.PrepareAttemptDocument(ctx, request)
	select {
	case p.finished <- err:
	default:
	}
	return frozen, err
}

func newServiceFixture(t *testing.T, queueCapacity int, parseTimeout time.Duration) *serviceFixture {
	t.Helper()
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" {
		t.Skip("TEST_DATABASE_URL is required for the PostgreSQL-backed capacity regression")
	}
	core, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := core.Close(); err != nil {
			t.Errorf("close core store: %v", err)
		}
	})
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin is unavailable")
	}
	token, user, err := core.Login("demo-admin", "admin-demo")
	if err != nil || user.ID != admin.ID {
		t.Fatalf("create authenticated HTTP session: user=%+v err=%v", user, err)
	}
	model := newCapacityModel()
	core.UseModels(model)

	document, err := core.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := document.Close(); err != nil {
			t.Errorf("close document store: %v", err)
		}
	})
	if err := document.UseServiceOwner("document"); err != nil {
		t.Fatal(err)
	}
	document.UseModels(model)

	worker, err := core.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := worker.Close(); err != nil {
			t.Errorf("close worker store: %v", err)
		}
	})
	if err := worker.UseServiceOwner("worker"); err != nil {
		t.Fatal(err)
	}
	worker.UseModels(model)

	server, client := serveFixture(t, document, queueCapacity, parseTimeout)
	return &serviceFixture{
		core: core, document: document, worker: worker, admin: admin, token: token,
		model: model, server: server, httpClient: client,
	}
}

func serveFixture(t *testing.T, document *store.Store, queueCapacity int, parseTimeout time.Duration) (*httptest.Server, *http.Client) {
	t.Helper()
	service, err := documentservice.New(document, documentservice.Config{WorkerCredential: testWorkerCredential, IntakeCredential: "core-intake-test-private-credential-0000000000", CPUConcurrency: 1,
		OCRConcurrency: 1,
		QueueCapacity:  queueCapacity,
		ParseTimeout:   parseTimeout})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler(httpapi.Handler(document, httpapi.Options{})))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 20 * time.Second
	return server, client
}

func TestUploadPreservesLargeOriginalAndRejectsOversize(t *testing.T) {
	f := newServiceFixture(t, 2, 2*time.Minute)
	caseID, err := f.core.CreateCaseWithBusiness(f.admin, "大文件上传", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	original := make([]byte, 32<<20)
	copy(original, "%PDF-1.4\n")
	copy(original[len(original)-6:], "%%EOF\n")
	id, result := f.upload(t, caseID, "attachment", "large.pdf", "application/pdf", original)
	if result.err != nil || result.status != http.StatusCreated {
		t.Fatalf("large upload: status=%d err=%v body=%s", result.status, result.err, result.body)
	}
	_, got, err := f.document.Original(f.admin, caseID, id)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("original differs: got %d bytes, err=%v", len(got), err)
	}
	_, result = f.upload(t, caseID, "attachment", "oversize.pdf", "application/pdf", make([]byte, (32<<20)+1))
	if result.err != nil || result.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize file: status=%d err=%v body=%s", result.status, result.err, result.body)
	}
	_, result = f.upload(t, caseID, "attachment", "oversize-request.pdf", "application/pdf", make([]byte, 33<<20))
	if result.err != nil || result.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize multipart: status=%d err=%v body=%s", result.status, result.err, result.body)
	}
}

func (f *serviceFixture) newCaseWithDocuments(t *testing.T) (string, string) {
	t.Helper()
	caseID, err := f.core.CreateCaseWithBusiness(f.admin, "文档服务解析容量回归", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	imageID, result := f.upload(t, caseID, "procurement", "scan.png", "text/plain", model.SampleWarrantyPNG)
	if result.status != http.StatusCreated {
		t.Fatalf("upload image: status=%d body=%s err=%v", result.status, result.body, result.err)
	}
	_, result = f.upload(t, caseID, "award", "award.txt", "image/png", []byte("中标文件未提供付款与验收约定。"))
	if result.status != http.StatusCreated {
		t.Fatalf("upload text: status=%d body=%s err=%v", result.status, result.body, result.err)
	}
	document, _, err := f.document.Original(f.admin, caseID, imageID)
	if err != nil {
		t.Fatalf("read uploaded image metadata: %v", err)
	}
	if document.MediaType != "image/png" {
		t.Fatalf("image type trusted multipart metadata instead of bytes: got %q", document.MediaType)
	}
	if got := f.model.calls.Load(); got != 0 {
		t.Fatalf("image upload ran OCR before an explicit parse: calls=%d", got)
	}
	return caseID, imageID
}

func (f *serviceFixture) upload(t *testing.T, caseID, role, filename, partMedia string, body []byte) (string, httpResult) {
	t.Helper()
	contentType, payload := multipartUploadPayload(t, role, filename, partMedia, body)
	result := f.do(http.MethodPost, "/api/cases/"+caseID+"/documents", contentType, payload)
	var response struct {
		ID string `json:"id"`
	}
	if result.err == nil && result.status == http.StatusCreated {
		if err := json.Unmarshal(result.body, &response); err != nil {
			t.Fatalf("decode upload response: %v", err)
		}
	}
	return response.ID, result
}

func multipartUploadPayload(t *testing.T, role, filename, partMedia string, body []byte) (string, []byte) {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("role", role); err != nil {
		t.Fatal(err)
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": filename}))
	header.Set("Content-Type", partMedia)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return writer.FormDataContentType(), payload.Bytes()
}

func (f *serviceFixture) do(method, path, contentType string, body []byte) httpResult {
	return f.doAs(f.token, method, path, contentType, body)
}

func (f *serviceFixture) doAs(token, method, path, contentType string, body []byte) httpResult {
	req, err := http.NewRequest(method, f.server.URL+path, bytes.NewReader(body))
	if err != nil {
		return httpResult{err: err}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	response, err := f.httpClient.Do(req)
	if err != nil {
		return httpResult{err: err}
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	return httpResult{status: response.StatusCode, body: responseBody, err: err}
}

func (f *serviceFixture) reparseAsync(caseID, documentID string) <-chan httpResult {
	result := make(chan httpResult, 1)
	go func() {
		result <- f.do(http.MethodPost, "/api/cases/"+caseID+"/documents/"+documentID+"/parse", "application/json", []byte(`{}`))
	}()
	return result
}

// Publish the sample rule pack and its synthetic policy through the real
// governance lifecycles before starting a governed review.
func (f *serviceFixture) publishGovernance(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile("../rulepack/testdata/demo.json")
	if err != nil {
		t.Fatalf("read sample rule pack: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode sample rule pack: %v", err)
	}
	manifest["tenant_id"] = accounts.Tenant(f.admin)
	rules, ok := manifest["rules"].([]any)
	if !ok || len(rules) == 0 {
		t.Fatal("sample rule pack has no rules")
	}
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			t.Fatal("sample rule pack contains an invalid rule")
		}
		rule["tenant_id"] = accounts.Tenant(f.admin)
		rule["business_scope"] = map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"}
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode sample rule pack: %v", err)
	}
	pack, err := f.core.CreateRulePack(f.admin, raw)
	if err != nil {
		t.Fatalf("create capacity rule pack: %v", err)
	}
	input := store.GovernanceInput{PackID: pack.Pack.ID, Version: pack.Pack.Version, ExpectedRevision: pack.Pack.Revision}
	pack, err = f.core.ValidateRulePackVersion(f.admin, input)
	if err != nil {
		t.Fatalf("validate capacity rule pack: %v", err)
	}
	input.ExpectedRevision, input.ExpectedContentHash = pack.Pack.Revision, pack.Pack.ContentHash
	pack, err = f.core.ApproveRulePackVersion(f.admin, input)
	if err != nil {
		t.Fatalf("approve capacity rule pack: %v", err)
	}
	input.ExpectedRevision, input.ExpectedPointerRevision = pack.Pack.Revision, pack.Pack.ActivePointerRevision
	pack, err = f.core.PublishRulePackVersion(f.admin, input)
	if err != nil {
		t.Fatalf("publish capacity rule pack: %v", err)
	}

	var body string
	var ruleIDs []string
	var spans []store.KnowledgeSourceSpan
	for _, rule := range []struct{ id, quote, suffix string }{
		{"ENT-TECH-BW-001", "带宽不低于100 Mbit/s", ""},
		{"ENT-TECH-SLA-001", "月可用率不低于99.9%", "，统计周期为自然月，时区Asia/Shanghai，公历gregorian，故障口径all_service_unavailability，无排除项"},
		{"ENT-COM-ADV-001", "预付款比例不超过1/3", "，预付款以合同总额为基础"},
	} {
		start := len([]rune(body))
		body += rule.quote + rule.suffix + "。"
		ruleIDs = append(ruleIDs, rule.id)
		spans = append(spans, store.KnowledgeSourceSpan{RuleID: rule.id, Start: start, End: start + len([]rune(rule.quote)), Quote: rule.quote})
	}
	policy, err := f.core.CreateGovernedKnowledge(f.admin, store.CreateGovernedKnowledgeInput{
		IdempotencyKey: "capacity-policy-create",
		Content: store.GovernedKnowledgeContent{
			KnowledgeID: "demo-synthetic-policy", Version: "1", Kind: "policy", Title: "容量测试制度", Body: body,
			Summary: "核验带宽、可用率和预付款比例", SourceType: "synthetic_policy", SourceReference: "容量回归合成制度",
			Scope:     store.KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
			ValidFrom: pack.Pack.EffectiveFrom, ValidUntil: pack.Pack.EffectiveUntil,
			Binding: store.PolicyRuleBinding{
				PackID: pack.Pack.ID, PackVersion: pack.Pack.Version, PackContentHash: pack.Pack.ContentHash,
				PackSnapshotDigest: pack.Pack.SnapshotSHA256, CompilerRef: "governed-rulepack/v1",
				RuleIDs: ruleIDs, SourceSpans: spans, CandidateDiff: "规则差异已审阅", ValidationEvidence: "规则包校验通过",
			},
		},
	})
	if err != nil {
		t.Fatalf("create capacity policy: %v", err)
	}
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		policy, err = f.core.GovernedKnowledgeCommand(f.admin, store.GovernedKnowledgeCommandInput{
			KnowledgeID: policy.Ref.KnowledgeID, VersionID: policy.Ref.VersionID, Action: action,
			ExpectedRevision: policy.Revision, ExpectedContentSHA256: policy.Ref.ContentSHA256,
			ExpectedPointerRevision: policy.PointerRevision, Reason: "已核对合成制度依据", IdempotencyKey: "capacity-policy-" + action,
		})
		if err != nil {
			t.Fatalf("%s capacity policy: %v", action, err)
		}
	}
}

func (f *serviceFixture) startWorker(t *testing.T, caseID string) (*observingPreparer, <-chan error, context.CancelFunc) {
	t.Helper()
	if _, err := f.core.StartReviewAsyncAt(context.Background(), f.admin, caseID, testBusinessTime); err != nil {
		t.Fatalf("accept durable review: %v", err)
	}
	claim, err := f.worker.ClaimNextJob(context.Background(), "capacity-regression-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim durable review job: %v", err)
	}
	client, err := documentclient.New(f.server.URL, testWorkerCredential, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	preparer := &observingPreparer{
		delegate: client,
		started:  make(chan store.PrepareDocumentRequest, 4),
		finished: make(chan error, 8),
	}
	f.worker.UseDocumentPreparer(preparer)
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	done := make(chan error, 1)
	go func() { done <- f.worker.RunClaimedJob(ctx, claim) }()
	return preparer, done, cancel
}

func waitHTTP(t *testing.T, result <-chan httpResult) httpResult {
	t.Helper()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("HTTP request failed: %v", got.err)
		}
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for HTTP request")
		return httpResult{}
	}
}

func waitParseCall(t *testing.T, calls <-chan int64) int64 {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for OCR parser")
		return 0
	}
}

func waitWorkerResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for worker job")
		return errors.New("worker job timed out")
	}
}

func TestPublicAndDelegatedDocumentWorkShareQueueAndLeaveReadsAvailable(t *testing.T) {
	f := newServiceFixture(t, 1, 10*time.Second)
	t.Cleanup(f.model.releaseAll)
	caseID, imageID := f.newCaseWithDocuments(t)
	f.publishGovernance(t)

	public := f.reparseAsync(caseID, imageID)
	if call := waitParseCall(t, f.model.entered); call != 1 {
		t.Fatalf("first OCR call number=%d, want 1", call)
	}

	read := f.do(http.MethodGet, "/api/cases/"+caseID, "", nil)
	if read.err != nil || read.status != http.StatusOK {
		t.Fatalf("read while parse queue is full: status=%d body=%s err=%v", read.status, read.body, read.err)
	}
	_, upload := f.upload(t, caseID, "attachment", "extra.txt", "image/png", []byte("额外材料"))
	if upload.err != nil || upload.status != http.StatusServiceUnavailable {
		t.Fatalf("public upload did not share the full queue: status=%d body=%s err=%v", upload.status, upload.body, upload.err)
	}
	generation := f.do(http.MethodPost, "/api/cases/"+caseID+"/generations", "application/json", []byte(`{"template_id":"ent_telecom_dedicated_line_v1","fields":{"circuit_id":"LINE-001","bandwidth_mbps":"100","sla_percent":"99.9","sla_period":"calendar_month","sla_exclusions":"none","amount_cny":"100000","tax_basis":"inclusive","prepayment_percent":"20","customer":"甲方","supplier":"乙方"}}`))
	if generation.err != nil || generation.status != http.StatusServiceUnavailable {
		t.Fatalf("public generation did not share the full queue: status=%d body=%s err=%v", generation.status, generation.body, generation.err)
	}

	preparer, workerDone, cancelWorker := f.startWorker(t, caseID)
	defer cancelWorker()
	select {
	case <-preparer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not request delegated document preparation")
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case err := <-preparer.finished:
			if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
				continue
			}
			goto delegationRejected
		case <-deadline:
			t.Fatal("delegated preparation did not observe the shared-queue HTTP 503")
		}
	}

delegationRejected:
	_ = waitWorkerResult(t, workerDone)

	f.model.release <- struct{}{}
	if result := waitHTTP(t, public); result.status != http.StatusOK {
		t.Fatalf("public reparse failed after OCR release: status=%d body=%s", result.status, result.body)
	}

	followup := f.reparseAsync(caseID, imageID)
	if call := waitParseCall(t, f.model.entered); call != 2 {
		t.Fatalf("follow-up OCR call number=%d, want 2", call)
	}
	f.model.release <- struct{}{}
	if result := waitHTTP(t, followup); result.status != http.StatusOK {
		t.Fatalf("capacity was not released after concurrent work: status=%d body=%s", result.status, result.body)
	}
}

func TestDelegatedPrepareWithoutCurrentJobAuthorityReturnsDenied(t *testing.T) {
	f := newServiceFixture(t, 1, 10*time.Second)
	request := store.PrepareDocumentRequest{
		TenantID: accounts.Tenant(f.admin), OrgID: f.admin.OrgID,
		JobID: "missing-delegated-job", WorkerID: "stale-worker", Generation: 1,
		RunID: "missing-run", AttemptID: "missing-attempt", DocumentID: "missing-document",
		Selection: store.AttemptParseSelection{
			AttemptID: "missing-attempt", RunID: "missing-run", DocumentID: "missing-document",
			ParsedDocumentID: "missing-parsed-document",
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/internal/v1/prepare", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Contract-Service-Key", testWorkerCredential)
	response, err := f.httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || !strings.Contains(string(responseBody), "current delegated preparation authority is unavailable") {
		t.Fatalf("missing claim authority answered %d %q", response.StatusCode, responseBody)
	}
	if strings.Contains(string(responseBody), "missing-delegated-job") || strings.Contains(string(responseBody), "tenant-a") {
		t.Fatalf("authority failure exposed request identity: %s", responseBody)
	}
}

func TestPublicAndDelegatedOCRUseOneSharedOCRPermit(t *testing.T) {
	f := newServiceFixture(t, 4, 10*time.Second)
	t.Cleanup(f.model.releaseAll)
	caseID, imageID := f.newCaseWithDocuments(t)
	f.publishGovernance(t)

	public := f.reparseAsync(caseID, imageID)
	if call := waitParseCall(t, f.model.entered); call != 1 {
		t.Fatalf("first OCR call number=%d, want 1", call)
	}
	preparer, workerDone, cancelWorker := f.startWorker(t, caseID)
	defer cancelWorker()
	select {
	case <-preparer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not reach delegated preparation")
	}
	select {
	case call := <-f.model.entered:
		t.Fatalf("delegated OCR entered while public OCR held the only permit: call=%d", call)
	case <-time.After(250 * time.Millisecond):
	}
	if maximum := f.model.maximum.Load(); maximum != 1 {
		t.Fatalf("concurrent OCR calls exceeded the configured permit: maximum=%d", maximum)
	}

	f.model.release <- struct{}{}
	if result := waitHTTP(t, public); result.status != http.StatusOK {
		t.Fatalf("public reparse failed: status=%d body=%s", result.status, result.body)
	}
	if call := waitParseCall(t, f.model.entered); call != 2 {
		t.Fatalf("delegated OCR call number=%d, want 2", call)
	}
	f.model.release <- struct{}{}
	select {
	case err := <-preparer.finished:
		if err != nil {
			t.Fatalf("delegated preparation failed after permit release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("delegated preparation did not finish after OCR release")
	}
	_ = waitWorkerResult(t, workerDone)
	if maximum := f.model.maximum.Load(); maximum != 1 {
		t.Fatalf("shared OCR permit allowed parallel parser calls: maximum=%d", maximum)
	}
}

func TestPublicParseTimeoutPersistsParserFailure(t *testing.T) {
	// Upload preparation performs database writes but does not run OCR. Give
	// that setup its ordinary budget, then exercise the short parse deadline.
	f := newServiceFixture(t, 2, 2*time.Minute)
	caseID, imageID := f.newCaseWithDocuments(t)
	f.server.Close()
	f.server, f.httpClient = serveFixture(t, f.document, 2, 350*time.Millisecond)

	result := waitHTTP(t, f.reparseAsync(caseID, imageID))
	if result.status != http.StatusGatewayTimeout {
		t.Fatalf("timed-out reparse status=%d body=%s, want 504", result.status, result.body)
	}
	if call := waitParseCall(t, f.model.entered); call != 1 {
		t.Fatalf("timeout test OCR call number=%d, want 1", call)
	}
	if active := f.model.active.Load(); active != 0 {
		t.Fatalf("timed-out parser remained active: %d", active)
	}
	detail, err := f.core.GetCase(f.admin, caseID)
	if err != nil {
		t.Fatalf("read case after parser timeout: %v", err)
	}
	for _, document := range detail.Documents {
		if document.Document.ID != imageID {
			continue
		}
		if document.ParsedDocument == nil || document.ParsedDocument.Status != "failed" || !strings.Contains(document.ParseError, "deadline exceeded") {
			t.Fatalf("timeout did not persist truthful parser failure: parsed=%+v error=%q", document.ParsedDocument, document.ParseError)
		}
		return
	}
	t.Fatal("timed-out image was absent from case detail")
}

func TestUnauthorizedPublicRequestsRejectBeforeCPUAndFreeCapacity(t *testing.T) {
	f := newServiceFixture(t, 3, 10*time.Second)
	t.Cleanup(f.model.releaseAll)
	caseID, _ := f.newCaseWithDocuments(t)
	f.publishGovernance(t)

	objects := newBlockingObjectStore()
	f.document.UseObjectStore(objects)
	t.Cleanup(objects.releaseGet)
	blockerType, blockerBody := multipartUploadPayload(t, "attachment", "cpu-block.txt", "text/plain", []byte("CPU parser gate blocker."))
	blockerUpload := make(chan httpResult, 1)
	go func() {
		blockerUpload <- f.do(http.MethodPost, "/api/cases/"+caseID+"/documents", blockerType, blockerBody)
	}()
	select {
	case <-objects.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("public text upload did not reach the blocked CPU parse")
	}

	viewerToken, viewer, err := f.core.Login("demo-viewer", "viewer-demo")
	if err != nil || viewer.ID != "demo-viewer" {
		t.Fatalf("authenticate viewer: user=%+v err=%v", viewer, err)
	}
	clerkToken, clerk, err := f.core.Login("demo-clerk", "clerk-demo")
	if err != nil || clerk.ID != "demo-clerk" {
		t.Fatalf("authenticate upload-capable user: user=%+v err=%v", clerk, err)
	}

	viewerType, viewerBody := multipartUploadPayload(t, "attachment", "viewer.txt", "text/plain", []byte("not authorized"))
	clerkType, clerkBody := multipartUploadPayload(t, "attachment", "clerk.txt", "text/plain", []byte("no case grant"))
	responses := make(chan struct {
		name   string
		result httpResult
	}, 3)
	go func() {
		responses <- struct {
			name   string
			result httpResult
		}{"viewer upload", f.doAs(viewerToken, http.MethodPost, "/api/cases/"+caseID+"/documents", viewerType, viewerBody)}
	}()
	go func() {
		responses <- struct {
			name   string
			result httpResult
		}{"clerk upload", f.doAs(clerkToken, http.MethodPost, "/api/cases/"+caseID+"/documents", clerkType, clerkBody)}
	}()
	go func() {
		responses <- struct {
			name   string
			result httpResult
		}{"clerk generation", f.doAs(clerkToken, http.MethodPost, "/api/cases/"+caseID+"/generations", "application/json", []byte(`{"template_id":"ent_telecom_dedicated_line_v1","fields":{}}`))}
	}()
	wantStatus := map[string]int{
		"viewer upload":    http.StatusForbidden,
		"clerk upload":     http.StatusNotFound,
		"clerk generation": http.StatusNotFound,
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for range 3 {
		select {
		case response := <-responses:
			if response.result.err != nil {
				t.Fatalf("%s failed: %v", response.name, response.result.err)
			}
			if response.result.status != wantStatus[response.name] {
				t.Fatalf("%s status=%d body=%s, want %d before CPU capacity is released", response.name, response.result.status, response.result.body, wantStatus[response.name])
			}
		case <-deadline.C:
			t.Fatal("unauthorized public document request waited on the occupied CPU parser")
		}
	}

	if _, result := f.upload(t, caseID, "attachment", "free.png", "text/plain", model.SampleWarrantyPNG); result.err != nil || result.status != http.StatusCreated {
		t.Fatalf("authorized image upload lost queue capacity while CPU parsing was occupied: status=%d body=%s err=%v", result.status, result.body, result.err)
	}
	if calls := f.model.calls.Load(); calls != 0 {
		t.Fatalf("image upload ran OCR before delegated preparation: calls=%d", calls)
	}

	preparer, workerDone, cancelWorker := f.startWorker(t, caseID)
	defer cancelWorker()
	select {
	case <-preparer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not request delegated document preparation")
	}
	if call := waitParseCall(t, f.model.entered); call != 1 {
		t.Fatalf("delegated OCR call number=%d, want 1", call)
	}
	select {
	case <-objects.release:
		t.Fatal("CPU blocker was released before delegated OCR entered")
	default:
	}
	f.model.releaseAll()
	select {
	case err := <-preparer.finished:
		if err != nil {
			t.Fatalf("delegated OCR failed while CPU capacity was occupied: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("delegated OCR did not complete after releasing its model")
	}
	objects.releaseGet()
	if result := waitHTTP(t, blockerUpload); result.status != http.StatusCreated {
		t.Fatalf("CPU blocker upload did not finish after release: status=%d body=%s", result.status, result.body)
	}
	if err := waitWorkerResult(t, workerDone); err != nil {
		t.Fatalf("worker failed after delegated OCR: %v", err)
	}
	generation := f.do(http.MethodPost, "/api/cases/"+caseID+"/generations", "application/json", []byte(`{"template_id":"ent_telecom_dedicated_line_v1","fields":{"circuit_id":"LINE-001","bandwidth_mbps":"100","sla_percent":"99.9","sla_period":"calendar_month","sla_exclusions":"none","amount_cny":"100000","tax_basis":"inclusive","prepayment_percent":"20","customer":"甲方","supplier":"乙方"}}`))
	if generation.err != nil || generation.status != http.StatusCreated {
		t.Fatalf("authorized contract draft generation failed after CPU capacity became available: status=%d body=%s err=%v", generation.status, generation.body, generation.err)
	}
}
