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
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/bootstrap"
	"contractaudit/internal/config"
	"contractaudit/internal/coreauthority"
	"contractaudit/internal/documentclient"
	"contractaudit/internal/documentservice"
	"contractaudit/internal/fault"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
	"contractaudit/internal/toolgateway"
	"github.com/jackc/pgx/v5"
)

const documentWorkerKey = "document-worker-test-private-key-1234567890"

type serviceEnvironment struct {
	ctx                               context.Context
	db                                *pgx.Conn
	schema                            string
	runtimes                          []*bootstrap.Runtime
	core, document, execution, worker *bootstrap.Runtime
	model, tool                       *bootstrap.Runtime
	documentURL                       string
	coreAuthorityURL                  string
	actor                             accounts.User
	intakeUploader                    store.CaseIntakeUploader
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
		"CORE_MODEL_AUTHORITY_TOKEN": strings.Repeat("u", 40), "WORKER_CORE_TOKEN": strings.Repeat("w", 40), "TOOL_CORE_AUTHORITY_TOKEN": strings.Repeat("q", 40),
		"TOOL_GATEWAY_URL":    modelServer.URL,
		"CORE_DOCUMENT_TOKEN": "core-document-test-private-token-1234567890",
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
	coreAuthorityHandler, err := httpapi.ServiceHandler(env.core.Store, httpapi.Options{
		Durable: true, DocumentIntakeCredential: os.Getenv("CORE_DOCUMENT_TOKEN"),
		ExperienceWorkerCredential: os.Getenv("WORKER_CORE_TOKEN"), ToolClaimAuthorityCredential: os.Getenv("TOOL_CORE_AUTHORITY_TOKEN"), ModelAuthorityCredential: os.Getenv("CORE_MODEL_AUTHORITY_TOKEN"),
	}, "core", strings.Repeat("g", 40))
	if err != nil {
		t.Fatal(err)
	}
	coreAuthorityServer := httptest.NewServer(coreAuthorityHandler)
	t.Cleanup(coreAuthorityServer.Close)
	env.coreAuthorityURL = coreAuthorityServer.URL
	t.Setenv("CORE_AUTHORITY_URL", coreAuthorityServer.URL)
	t.Setenv("CORE_SERVICE_URL", coreAuthorityServer.URL)
	t.Setenv("CORE_AUTHORITY_ALLOW_INSECURE_DEV", "true")
	t.Setenv("APP_ENV", "test")
	env.document = env.open(t, "document")
	env.execution = env.open(t, "execution")
	env.model = env.open(t, "model")
	env.tool = env.open(t, "tool")
	document, err := documentservice.New(env.document.Store, documentservice.Config{
		WorkerCredential: documentWorkerKey, IntakeCredential: os.Getenv("CORE_DOCUMENT_TOKEN"),
		CoreAuthorityURL: coreAuthorityServer.URL, AllowInsecureDev: true, AppEnv: "test",
		CPUConcurrency: 1, OCRConcurrency: 1, QueueCapacity: 1, ParseTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(document.Handler(http.NotFoundHandler()))
	t.Cleanup(server.Close)
	env.documentURL = server.URL
	t.Setenv("DOCUMENT_SERVICE_URL", server.URL)
	uploader, err := documentclient.NewIntakeUploader(server.URL, os.Getenv("CORE_DOCUMENT_TOKEN"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	env.intakeUploader = uploader
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
	key := strings.ToUpper(role) + "_DATABASE_URL"
	if role == "worker" {
		key = "EXECUTION_DATABASE_URL"
	}
	cfg, err := pgx.ParseConfig(os.Getenv(key))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(env.ctx, os.Getenv(key))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(env.ctx)
	var current, session string
	var super, bypass bool
	if err = conn.QueryRow(env.ctx, `SELECT session_user,current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&session, &current, &super, &bypass); err != nil || session != cfg.User || current != cfg.User || super || bypass {
		t.Fatalf("actual %s consumer role proof failed: %v", role, err)
	}
	if _, err = conn.Exec(env.ctx, `SELECT principal_id FROM `+pgx.Identifier{env.schema, "identity_principals"}.Sanitize()); err == nil {
		t.Fatalf("%s read identity table directly", role)
	}
	if role != "core" {
		if _, err = conn.Exec(env.ctx, `SELECT session_digest FROM `+pgx.Identifier{env.schema, "operation_authority_tickets"}.Sanitize()); err == nil {
			t.Fatalf("%s read private operation ticket credentials", role)
		}
	}
	return runtime
}

func (env *serviceEnvironment) reviewClaim(t *testing.T, ocrDraft bool) (string, store.JobAcceptance, store.JobClaim, store.PrepareDocumentRequest) {
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
	filename, media, body := "draft.png", "image/png", imageBytes.Bytes()
	if !ocrDraft {
		filename, media, body = "draft.txt", "text/plain", []byte("合同约定质保期为36个月。")
	}
	docID, err := env.document.Store.AddDocument(context.Background(), env.actor, caseID, "draft_contract", filename, media, body)
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, err := env.core.Store.EstablishSession(env.actor, "client")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := env.execution.Store.StartReviewAsync(store.WithAdmissionSessionToken(env.ctx, sessionToken), env.actor, caseID)
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

func TestDistributedDocumentIntakeRedeemsCoreTicket(t *testing.T) {
	env := newServiceEnvironment(t, http.NotFoundHandler())
	token, err := env.core.Store.EstablishSession(env.actor, "client")
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.Handler(env.core.Store, httpapi.Options{IntakeUploader: env.intakeUploader}))
	defer api.Close()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	for name, value := range map[string]string{"title": "Cross-process authority ticket", "contract_type": "service", "business_line": "ENT", "scenario": "cloud_services"} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	disposition := mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": "ticket-contract.txt"})
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {disposition}, "Content-Type": {"text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("opaque ticket cross-process body")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, api.URL+"/api/intakes", &payload)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Idempotency-Key", "cross-process-ticket")
	request.Header.Set("Origin", api.URL)
	request.AddCookie(&http.Cookie{Name: "session", Value: token})
	response, err := api.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("distributed intake status=%d body=%s", response.StatusCode, body)
	}
	var receipt struct {
		OperationID string `json:"operation_id"`
		CaseID      string `json:"case_id"`
		State       string `json:"state"`
		DocumentID  string `json:"document_id"`
		MaterialID  string `json:"material_id"`
	}
	if err := json.Unmarshal(body, &receipt); err != nil || receipt.State != "completed" || receipt.DocumentID == "" || receipt.MaterialID == "" {
		t.Fatalf("distributed intake receipt=%+v err=%v body=%s", receipt, err, body)
	}
	operation, err := env.core.Store.GetCaseIntake(env.ctx, env.actor, receipt.OperationID)
	if err != nil || operation.Status != "completed" || operation.CaseID != receipt.CaseID {
		t.Fatalf("Core receipt state=%+v err=%v", operation, err)
	}
}

func TestToolGatewaySessionAuthorityPreservesDatabaseMembershipOverHTTP(t *testing.T) {
	env := newServiceEnvironment(t, http.NotFoundHandler())
	issuer := "https://identity.example/tool-gateway-test"
	profile := accounts.User{ID: "remote-session-actor", DisplayName: "Directory display", Role: "admin",
		TenantID: "directory-tenant", OrgID: "directory-org", Enabled: true, Issuer: issuer, Subject: "remote-subject",
		Grants: []access.Grant{{Role: "viewer", Scope: "tenant"}}}
	directoryPath := t.TempDir() + "/directory.json"
	directoryBytes, err := json.Marshal(map[string]any{"users": []accounts.User{profile}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directoryPath, directoryBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directoryPath); err != nil {
		t.Fatal(err)
	}
	defer accounts.ConfigureDirectory("")
	profile, ok := accounts.BySubject(issuer, "remote-subject")
	if !ok {
		t.Fatal("test directory did not resolve the remote session identity")
	}
	adminDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	adminConfig, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	crosswalk := store.IdentityCrosswalk{
		Version:    1,
		Tenants:    []store.IdentityTenantMap{{ID: "database-tenant", DisplayName: "Database tenant", Status: "active"}},
		OrgUnits:   []store.IdentityOrgUnitMap{{TenantID: "database-tenant", ID: "database-org", DisplayName: "Database org", Status: "active"}},
		Principals: []store.IdentityPrincipalMap{{ID: "71111111-1111-4111-8111-111111111111", ActorID: profile.ID, Issuer: issuer, Subject: profile.Subject, Status: "active"}},
		Memberships: []store.IdentityMembershipMap{{ID: "72222222-2222-4222-8222-222222222222", ActorID: profile.ID,
			TenantID: "database-tenant", OrgUnitID: "database-org", RoleCode: "reviewer",
			Grants: []access.Grant{{Role: "reviewer", Scope: "tenant"}}, Status: "active"}},
	}
	if _, err := store.ImportIdentityCrosswalk(env.ctx, adminConfig.ConnString(), env.schema, adminConfig.Database, crosswalk, true); err != nil {
		t.Fatalf("apply exact test membership crosswalk: %v", err)
	}
	login, err := env.core.Store.ResolveVerifiedOIDC(env.ctx, profile, "client")
	if err != nil || login.SessionToken == "" || !login.User.HasResolvedMembership() {
		t.Fatalf("resolve verified OIDC login: result=%+v err=%v", login, err)
	}
	resolver, err := toolgateway.NewCoreSessionResolver(env.coreAuthorityURL, "", true, "test", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resolved, audience, err := resolver.AuthenticateSession(env.ctx, login.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	if audience != "client" || !resolved.HasResolvedMembership() || resolved.PrincipalID != crosswalk.Principals[0].ID ||
		resolved.MembershipID != crosswalk.Memberships[0].ID || resolved.TenantID != "database-tenant" ||
		resolved.OrgID != "database-org" || resolved.Role != "reviewer" || len(resolved.Grants) != 1 || resolved.Grants[0].Role != "reviewer" {
		t.Fatalf("HTTP session projection lost or replaced database authority: audience=%q user=%+v", audience, resolved)
	}
}

func TestToolGatewayClaimAuthorityRechecksLiveFrozenJobThroughCoreHTTP(t *testing.T) {
	modelStarted := make(chan struct{}, 1)
	releaseModel := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseModel) })
	env := newServiceEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case modelStarted <- struct{}{}:
		default:
		}
		select {
		case <-releaseModel:
		case <-r.Context().Done():
		}
		http.Error(w, "controlled test model unavailable", http.StatusServiceUnavailable)
	}))
	_, _, claim, _ := env.reviewClaim(t, false)
	workerEndpoint, workerHTTP, err := coreauthority.NewTransport(env.coreAuthorityURL, "/internal/v1/worker/claim-authority", "", "test", true, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	workerBody, err := json.Marshal(map[string]any{"tenant_id": claim.TenantID, "org_id": claim.OrgID, "job_id": claim.JobID,
		"worker_id": claim.WorkerID, "generation": claim.Generation, "case_id": claim.CaseID, "run_id": claim.RunID, "attempt_id": claim.AttemptID})
	if err != nil {
		t.Fatal(err)
	}
	workerRequest, err := http.NewRequestWithContext(env.ctx, http.MethodPost, workerEndpoint, bytes.NewReader(workerBody))
	if err != nil {
		t.Fatal(err)
	}
	workerRequest.Header.Set("Content-Type", "application/json")
	workerRequest.Header.Set("Authorization", "Bearer "+os.Getenv("WORKER_CORE_TOKEN"))
	workerResponse, err := workerHTTP.Do(workerRequest)
	if err != nil {
		t.Fatal(err)
	}
	workerResponseBody, _ := io.ReadAll(workerResponse.Body)
	workerResponse.Body.Close()
	if workerResponse.StatusCode != http.StatusOK {
		t.Fatalf("Core refused live worker-start claim: status=%d body=%s", workerResponse.StatusCode, workerResponseBody)
	}
	workerDone := make(chan error, 1)
	workerJoined := make(chan struct{})
	runCtx, cancelWorker := context.WithCancel(env.ctx)
	go func() { defer close(workerJoined); workerDone <- env.worker.Store.RunClaimedJob(runCtx, claim) }()
	t.Cleanup(func() {
		cancelWorker()
		select {
		case <-workerJoined:
		case <-time.After(6 * time.Second):
			t.Error("worker did not join after cancellation")
		}
	})
	select {
	case <-modelStarted:
	case runErr := <-workerDone:
		var stateName, errorCode string
		_ = env.db.QueryRow(env.ctx, `SELECT state,COALESCE(error_code,'') FROM `+pgx.Identifier{env.schema, "review_attempts"}.Sanitize()+` WHERE id=$1`, claim.AttemptID).Scan(&stateName, &errorCode)
		t.Fatalf("worker exited before model dispatch: err=%v attempt=%s code=%s", runErr, stateName, errorCode)
	case <-time.After(10 * time.Second):
		t.Fatal("review attempt never reached the controlled model call")
	}
	resolver, err := toolgateway.NewCoreClaimResolver(env.coreAuthorityURL, "", true, "test", os.Getenv("TOOL_CORE_AUTHORITY_TOKEN"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := store.ToolInvocationContext{TenantID: claim.TenantID, OrgID: claim.OrgID, JobID: claim.JobID,
		WorkerID: claim.WorkerID, Generation: claim.Generation, CaseID: claim.CaseID, RunID: claim.RunID, AttemptID: claim.AttemptID}
	for i := 0; i < 2; i++ {
		authority, err := resolver.AuthorizeToolResolution(env.ctx, request)
		if err != nil || authority.User.ID != claim.ActorID || authority.TenantID != claim.TenantID || authority.CaseID != claim.CaseID {
			t.Fatalf("Core claim authority pass %d=%+v err=%v", i+1, authority, err)
		}
	}
	releaseOnce.Do(func() { close(releaseModel) })
	if err := <-workerDone; err != nil && !strings.Contains(err.Error(), "controlled test model unavailable") {
		t.Fatalf("finish controlled worker attempt: %v", err)
	}
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
	caseID, accepted, claim, request := env.reviewClaim(t, true)
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
	caseID, accepted, claim, request := env.reviewClaim(t, true)
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
	caseID, accepted, _, valid := env.reviewClaim(t, true)
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
