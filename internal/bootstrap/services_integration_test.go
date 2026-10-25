package bootstrap_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/bootstrap"
	"contractaudit/internal/config"
	"contractaudit/internal/documentclient"
	"contractaudit/internal/documentservice"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
)

const documentWorkerKey = "document-worker-test-private-key-1234567890"

type serviceEnvironment struct {
	ctx                               context.Context
	db                                *pgx.Conn
	schema                            string
	runtimes                          []*bootstrap.Runtime
	core, document, execution, worker *bootstrap.Runtime
	documentURL                       string
	actor                             accounts.User
}

func newServiceEnvironment(t *testing.T, upstream http.Handler) *serviceEnvironment {
	t.Helper()
	base, migrator := os.Getenv("TEST_DATABASE_URL"), os.Getenv("MIGRATOR_DATABASE_URL")
	if base == "" || migrator == "" {
		t.Skip("isolated PostgreSQL and migrator are required")
	}
	if _, err := config.Select(config.Environ{AppEnv: config.ModeTest, TestDatabaseURL: base}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(nonce[:])
	env := &serviceEnvironment{ctx: ctx, schema: "it_services_" + suffix}
	var err error
	env.db, err = pgx.Connect(ctx, migrator)
	if err != nil {
		t.Fatal(err)
	}
	roles := make([]string, 0, 5)
	t.Cleanup(func() {
		for i := len(env.runtimes) - 1; i >= 0; i-- {
			if err := env.runtimes[i].Close(); err != nil {
				t.Error(err)
			}
		}
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := env.db.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{env.schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
		for _, role := range roles {
			if _, err := env.db.Exec(cleanup, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error(err)
			}
		}
		_ = env.db.Close(cleanup)
		_ = accounts.ConfigureDirectory("")
	})
	for _, key := range []string{"SERVICE_ROLE", "APP_DATABASE_MODE", "EXECUTION_MODE", "APP_READ_ONLY", "COOKIE_SECURE", "IDENTITY_DIRECTORY_FILE", "OIDC_ISSUER", "S3_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT", "EXECUTION_QUEUE_POLICY"} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_SCHEMA", env.schema)
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("TEST_DATABASE_URL", base)
	t.Setenv("MIGRATOR_DATABASE_URL", migrator)
	modelServer := httptest.NewServer(upstream)
	t.Cleanup(modelServer.Close)
	for key, value := range map[string]string{
		"LLM_BASE_URL": modelServer.URL + "/v1", "MODEL_GATEWAY_URL": modelServer.URL + "/v1",
		"LLM_MODEL": "controlled-chat", "OCR_MODEL": "controlled-ocr", "LLM_API_KEY": "",
		"WORKER_MODEL_TOKEN": strings.Repeat("m", 32), "DOCUMENT_MODEL_TOKEN": strings.Repeat("d", 32),
		"WORKER_DOCUMENT_TOKEN": documentWorkerKey, "WORKER_TOOL_TOKEN": strings.Repeat("t", 32),
		"TOOL_GATEWAY_URL": modelServer.URL,
	} {
		t.Setenv(key, value)
	}
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		role := "it_" + service + "_" + suffix
		parsed, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		parsed.User = url.UserPassword(role, strings.Repeat(suffix, 2))
		t.Setenv(strings.ToUpper(service)+"_DATABASE_URL", parsed.String())
		roles = append(roles, role)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateServiceRoles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(""); err != nil {
		t.Fatal(err)
	}
	var found bool
	env.actor, found = accounts.ByID("demo-admin")
	if !found {
		t.Fatal("synthetic actor unavailable")
	}
	env.core = env.open(t, "core")
	env.document = env.open(t, "document")
	env.execution = env.open(t, "execution")
	document, err := documentservice.New(env.document.Store, documentservice.Config{
		WorkerCredential: documentWorkerKey, CPUConcurrency: 1, OCRConcurrency: 1, QueueCapacity: 1, ParseTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(document.Handler(http.NotFoundHandler()))
	t.Cleanup(server.Close)
	env.documentURL = server.URL
	t.Setenv("DOCUMENT_SERVICE_URL", server.URL)
	env.worker = env.open(t, "worker")
	return env
}

func (env *serviceEnvironment) open(t *testing.T, role string) *bootstrap.Runtime {
	t.Helper()
	runtime, err := bootstrap.OpenService(env.ctx, role)
	if err != nil {
		t.Fatalf("open %s service: %v", role, err)
	}
	env.runtimes = append(env.runtimes, runtime)
	return runtime
}

func (env *serviceEnvironment) imageClaim(t *testing.T) (string, store.JobAcceptance, store.JobClaim, store.PrepareDocumentRequest) {
	t.Helper()
	caseID, err := env.core.Store.CreateCase(env.actor, "Document service boundary", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.document.Store.AddDocument(context.Background(), env.actor, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件要求质保期为36个月。")); err != nil {
		t.Fatal(err)
	}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	docID, err := env.document.Store.AddDocument(context.Background(), env.actor, caseID, "draft_contract", "draft.png", "image/png", imageBytes.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := env.execution.Store.StartReviewAsync(env.ctx, env.actor, caseID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := env.worker.Store.ClaimNextJob(env.ctx, "document-boundary-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed unrelated job %s", claim.JobID)
	}
	var selectionJSON []byte
	query := "SELECT row_to_json(p) FROM " + pgx.Identifier{env.schema, "review_attempt_parse_selections"}.Sanitize() + " p WHERE attempt_id=$1 AND document_id=$2"
	if err := env.db.QueryRow(env.ctx, query, accepted.AttemptID, docID).Scan(&selectionJSON); err != nil {
		t.Fatal(err)
	}
	var selection store.AttemptParseSelection
	if err := json.Unmarshal(selectionJSON, &selection); err != nil {
		t.Fatal(err)
	}
	request := store.PrepareDocumentRequest{TenantID: claim.TenantID, OrgID: claim.OrgID, JobID: claim.JobID, WorkerID: claim.WorkerID, Generation: claim.Generation,
		RunID: claim.RunID, AttemptID: claim.AttemptID, DocumentID: docID, Selection: selection}
	return caseID, accepted, claim, request
}

func (env *serviceEnvironment) startClaim(t *testing.T, claim store.JobClaim) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(env.ctx)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- env.worker.Store.RunClaimedJob(ctx, claim)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	})
	return cancel, done
}

func TestIndependentDocumentPreparationPreservesFrozenIdentityAndRuleFailure(t *testing.T) {
	var calls atomic.Int32
	chatEntered, releaseChat := make(chan struct{}, 1), make(chan struct{})
	defer func() {
		select {
		case <-releaseChat:
		default:
			close(releaseChat)
		}
	}()
	env := newServiceEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Model == "controlled-ocr" {
			calls.Add(1)
		} else {
			select {
			case chatEntered <- struct{}{}:
			default:
			}
			select {
			case <-releaseChat:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"拟签合同约定质保期为24个月。"}}]}`)
	}))
	caseID, accepted, claim, request := env.imageClaim(t)
	_, done := env.startClaim(t, claim)
	select {
	case <-chatEntered:
	case err := <-done:
		t.Fatalf("worker finished before model analysis: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach model analysis")
	}
	client, err := documentclient.New(env.documentURL, documentWorkerKey, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := client.PrepareAttemptDocument(env.ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !frozen.Ready || !frozen.FromOCR || frozen.Text != "拟签合同约定质保期为24个月。" || frozen.ParsedDocumentID != request.Selection.ParsedDocumentID || frozen.OriginalHash != request.Selection.OriginalDigest {
		t.Fatalf("prepared document did not preserve original, selected parse and OCR text: %+v", frozen)
	}
	reused, err := client.PrepareAttemptDocument(env.ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if reused.ParsedArtifactDigest != frozen.ParsedArtifactDigest || calls.Load() != 1 {
		t.Fatal("reusing a frozen parse changed its artifact or called OCR again")
	}
	foreign := request
	foreign.Generation++
	if _, err := client.PrepareAttemptDocument(env.ctx, foreign); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("stale generation was not refused: %v", err)
	}
	close(releaseChat)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not finish after model response")
	}
	workspace, err := env.execution.Store.Workspace(env.actor, caseID, accepted.RunID, accepted.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Execution == nil || workspace.Execution.Attempt.State != "partial" {
		t.Fatalf("unusable model output must remain partial: %+v", workspace.Execution)
	}
	foundConflict := false
	for _, finding := range workspace.Findings {
		if finding.Origin == "rule" && finding.Relation == "conflict" {
			for _, anchor := range finding.Anchors {
				if anchor.DocumentID == request.DocumentID && strings.Contains(anchor.Quote, "24") {
					foundConflict = true
				}
			}
		}
	}
	if !foundConflict {
		t.Fatalf("independent parsing lost the original warranty conflict: %+v", workspace.Findings)
	}
	if _, err := client.PrepareAttemptDocument(env.ctx, request); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("finished claim still delegated preparation: %v", err)
	}
	t.Setenv("APP_READ_ONLY", "true")
	readOnly := env.open(t, "core")
	if _, err := readOnly.Store.GetCase(env.actor, caseID); err != nil {
		t.Fatalf("read-only runtime lost history: %v", err)
	}
	_, err = readOnly.Store.CreateCase(env.actor, "forbidden write", "procurement_ict")
	var failure *fault.Error
	if !errors.As(err, &failure) || failure.Code != "read_only" {
		t.Fatalf("read-only runtime accepted a business write: %v", err)
	}
}

func TestDocumentCapacityAndCancellationDoNotPublishLateOCR(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	env := newServiceEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"拟签合同约定质保24个月。"}}]}`)
	}))
	caseID, accepted, claim, request := env.imageClaim(t)
	client, err := documentclient.New(env.documentURL, documentWorkerKey, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := env.startClaim(t, claim)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("OCR did not reach the controlled upstream")
	}
	if _, err := client.PrepareAttemptDocument(env.ctx, request); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("full document capacity was not rejected: %v", err)
	}
	if err := env.execution.Store.CancelReview(env.ctx, env.actor, caseID, accepted.RunID); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation did not reach HTTP client: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled preparation did not exit")
	}
	workspace, err := env.execution.Store.Workspace(env.actor, caseID, accepted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Execution == nil || workspace.Execution.Attempt.State != "cancelled" {
		t.Fatalf("late OCR changed canceled attempt: %+v", workspace.Execution)
	}
	if _, err := client.PrepareAttemptDocument(env.ctx, request); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("canceled claim still delegated work: %v", err)
	}
}

func TestDocumentProtocolRejectsUntrustedPreparationBeforeOCR(t *testing.T) {
	var calls atomic.Int32
	env := newServiceEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	for _, item := range []struct {
		name, method, body string
		keys               []string
		status             int
	}{
		{"wrong-method", "GET", "", nil, 405},
		{"missing-credential", "POST", `{}`, nil, 401},
		{"duplicate-credential", "POST", `{}`, []string{documentWorkerKey, documentWorkerKey}, 401},
		{"unknown-field", "POST", `{"shell":"forbidden"}`, []string{documentWorkerKey}, 400},
		{"trailing-object", "POST", `{} {}`, []string{documentWorkerKey}, 400},
		{"oversized-body", "POST", `{"document_id":"` + strings.Repeat("x", (1<<20)+1) + `"}`, []string{documentWorkerKey}, 400},
		{"no-current-claim", "POST", `{}`, []string{documentWorkerKey}, 403},
	} {
		t.Run(item.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(env.ctx, item.method, env.documentURL+"/internal/v1/prepare", strings.NewReader(item.body))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range item.keys {
				req.Header.Add("X-Contract-Service-Key", key)
			}
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != item.status {
				t.Fatalf("status=%d want=%d", response.StatusCode, item.status)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("rejected delegated requests reached OCR")
	}
}

func TestDelegatedPreparationRejectsForgedClaimFieldsAndRevokedReview(t *testing.T) {
	var calls atomic.Int32
	env := newServiceEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	caseID, accepted, _, valid := env.imageClaim(t)
	client, err := documentclient.New(env.documentURL, documentWorkerKey, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*store.PrepareDocumentRequest)
	}{
		{"foreign tenant", func(r *store.PrepareDocumentRequest) { r.TenantID = "tenant-foreign" }},
		{"foreign organization", func(r *store.PrepareDocumentRequest) { r.OrgID = "org-foreign" }},
		{"foreign job", func(r *store.PrepareDocumentRequest) { r.JobID = "job-foreign" }},
		{"foreign worker", func(r *store.PrepareDocumentRequest) { r.WorkerID = "worker-foreign" }},
		{"stale generation", func(r *store.PrepareDocumentRequest) { r.Generation++ }},
		{"foreign run", func(r *store.PrepareDocumentRequest) { r.RunID = "run-foreign" }},
		{"foreign attempt", func(r *store.PrepareDocumentRequest) { r.AttemptID = "attempt-foreign" }},
		{"foreign document", func(r *store.PrepareDocumentRequest) { r.DocumentID = "document-foreign" }},
		{"foreign parse selection", func(r *store.PrepareDocumentRequest) { r.Selection.ParsedDocumentID = "parse-foreign" }},
		{"changed parser identity", func(r *store.PrepareDocumentRequest) { r.Selection.ParserVersion = "untrusted-parser" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if _, err := client.PrepareAttemptDocument(env.ctx, request); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
				t.Fatalf("forged delegated identity was not refused with 403: %v", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("forged delegated claims reached OCR %d times", calls.Load())
	}
	if err := env.execution.Store.CancelReview(env.ctx, env.actor, caseID, accepted.RunID); err != nil {
		t.Fatalf("revoke review authority: %v", err)
	}
	if _, err := client.PrepareAttemptDocument(env.ctx, valid); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("revoked review still delegated document preparation: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("revoked delegated claim reached OCR %d times", calls.Load())
	}
}
