package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/bootstrap"
	"contractaudit/internal/documentservice"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestWorkerConfigurationHelpersPreserveDefaultsAndValidation(t *testing.T) {
	t.Setenv("HEALTH_ADDR", " 127.0.0.1:8181 ")
	if got, err := requiredEnv("HEALTH_ADDR"); err != nil || got != "127.0.0.1:8181" {
		t.Fatalf("requiredEnv() = %q, %v", got, err)
	}
	t.Setenv("HEALTH_ADDR", " \t ")
	if _, err := requiredEnv("HEALTH_ADDR"); err == nil || err.Error() != "HEALTH_ADDR is required to expose the worker health surface" {
		t.Fatalf("requiredEnv() error = %v, want required address error", err)
	}

	t.Setenv("WORKER_ID", "  worker-characterization  ")
	if got, err := configuredWorkerID(); err != nil || got != "worker-characterization" {
		t.Fatalf("configuredWorkerID() = %q, %v", got, err)
	}
	t.Setenv("WORKER_ID", " ")
	host, err := os.Hostname()
	if err != nil {
		t.Fatal("read host name for default worker identity")
	}
	wantWorkerID := host + "-" + strconv.Itoa(os.Getpid())
	if got, err := configuredWorkerID(); err != nil || got != wantWorkerID {
		t.Fatalf("configuredWorkerID() = %q, %v, want %q", got, err, wantWorkerID)
	}

	t.Setenv("WORKER_DURATION_TEST", " ")
	if got, err := durationEnv("WORKER_DURATION_TEST", 30*time.Second); err != nil || got != 30*time.Second {
		t.Fatalf("durationEnv() default = %s, %v", got, err)
	}
	t.Setenv("WORKER_DURATION_TEST", " 125ms ")
	if got, err := durationEnv("WORKER_DURATION_TEST", time.Second); err != nil || got != 125*time.Millisecond {
		t.Fatalf("durationEnv() configured = %s, %v", got, err)
	}
	for _, invalid := range []string{"invalid", "0s", "-1s"} {
		t.Setenv("WORKER_DURATION_TEST", invalid)
		if _, err := durationEnv("WORKER_DURATION_TEST", time.Second); err == nil || err.Error() != "WORKER_DURATION_TEST must be a positive Go duration" {
			t.Fatalf("durationEnv(%q) error = %v", invalid, err)
		}
	}
}

func TestWaitForUsesTimerWakeAndCancellation(t *testing.T) {
	t.Run("timer", func(t *testing.T) {
		if err := waitFor(context.Background(), time.Millisecond, nil); err != nil {
			t.Fatalf("waitFor timer = %v, want nil", err)
		}
	})
	t.Run("wake", func(t *testing.T) {
		wake := make(chan struct{}, 1)
		wake <- struct{}{}
		if err := waitFor(context.Background(), time.Hour, wake); err != nil {
			t.Fatalf("waitFor wake = %v, want nil", err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := waitFor(ctx, time.Hour, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("waitFor canceled context = %v, want context.Canceled", err)
		}
	})
}

func TestClaimOutcomeLogsExpectedAndUnexpectedFailures(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	claim := store.JobClaim{JobID: "job-1", RunID: "run-1", AttemptID: "attempt-1", Generation: 3}

	if unexpectedWorkerError(nil) || unexpectedWorkerError(context.Canceled) || unexpectedWorkerError(store.ErrLeaseLost) {
		t.Fatal("nil, cancellation, and lost lease should be expected worker outcomes")
	}
	if !unexpectedWorkerError(errors.New("unexpected run failure")) {
		t.Fatal("unexpected worker errors must be logged")
	}
	logClaimOutcome(claim, errors.New("run failed"), errors.New("heartbeat failed"))
	if !strings.Contains(output.String(), `msg="review job failed"`) || !strings.Contains(output.String(), `msg="review job heartbeat failed"`) {
		t.Fatalf("unexpected worker failures lost their operator log entries: %s", output.String())
	}

	output.Reset()
	logClaimOutcome(claim, context.Canceled, store.ErrLeaseLost)
	if !strings.Contains(output.String(), `msg="review job finished"`) || strings.Contains(output.String(), `msg="review job failed"`) || strings.Contains(output.String(), `msg="review job heartbeat failed"`) {
		t.Fatalf("expected cancellation/lost-lease logging changed: %s", output.String())
	}
}

func TestWorkerRunValidatesConfigAndDrainsIdlePostgresQueue(t *testing.T) {
	t.Setenv("HEALTH_ADDR", "")
	if _, err := requiredEnv("HEALTH_ADDR"); err == nil || err.Error() != "HEALTH_ADDR is required to expose the worker health surface" {
		t.Fatalf("run environment helper error = %v", err)
	}
	env := newWorkerCommandEnvironment(t, http.NotFoundHandler())
	if err := claimJobs(env.ctx, env.worker.Store, "", time.Second, time.Millisecond, time.Millisecond, nil); err == nil || err.Error() != "worker id and positive lease are required" {
		t.Fatalf("claimJobs invalid worker ID error = %v", err)
	}
	idleCtx, cancelIdle := context.WithCancel(env.ctx)
	idleDone := make(chan error, 1)
	idleCancel := time.AfterFunc(250*time.Millisecond, cancelIdle)
	defer idleCancel.Stop()
	go func() {
		idleDone <- claimJobs(idleCtx, env.worker.Store, "worker-empty-queue", time.Second, time.Millisecond, 5*time.Millisecond, nil)
	}()
	select {
	case err := <-idleDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("empty-queue claim loop after cancel = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("claim loop did not leave PostgreSQL no-row wait after cancellation")
	}

	t.Run("heartbeat rejects stale PostgreSQL generation and cancels job context", func(t *testing.T) {
		jobCtx, cancelJob := context.WithCancel(context.Background())
		claim := store.JobClaim{JobID: "missing-job", TenantID: env.actor.TenantID, OrgID: env.actor.OrgID,
			CaseID: "missing-case", RunID: "missing-run", AttemptID: "missing-attempt", ActorID: env.actor.ID,
			WorkerID: "stale-worker", Generation: 1}
		err := renewLease(jobCtx, cancelJob, env.worker.Store, claim, time.Second, time.Millisecond)
		if !errors.Is(err, store.ErrLeaseLost) || !errors.Is(jobCtx.Err(), context.Canceled) {
			t.Fatalf("renewLease error=%v job context=%v, want ErrLeaseLost and cancellation", err, jobCtx.Err())
		}
	})

	t.Run("claimed job starts and joins heartbeat before parent cancellation", func(t *testing.T) {
		accepted := env.createReview(t)
		entered := make(chan struct{}, 1)
		env.worker.Store.UseModels(blockingWorkerModel{entered: entered})
		claimCtx, cancelClaim := context.WithCancel(env.ctx)
		done := make(chan error, 1)
		workerID := "worker-claim-heartbeat-test"
		lease, heartbeat := 5*time.Second, 25*time.Millisecond
		go func() {
			done <- claimJobs(claimCtx, env.worker.Store, workerID, lease, heartbeat, 10*time.Millisecond, nil)
		}()
		select {
		case <-entered:
		case err := <-done:
			t.Fatalf("claim loop stopped before model stage: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("PostgreSQL worker claim did not reach the blocked model call")
		}
		initial := waitForInitialWorkerHeartbeat(t, env, accepted.JobID, workerID)
		last := waitForWorkerHeartbeatChange(t, env, accepted.JobID, workerID, initial, 2*time.Second)
		cancelClaim()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("claimJobs after parent cancellation = %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("claimJobs did not join the canceled job and heartbeat")
		}
		time.Sleep(2 * heartbeat)
		_, _, _, current, err := env.workerJobState(accepted.JobID)
		if err != nil || !current.Valid || !current.Time.Equal(last) {
			t.Fatalf("heartbeat changed after claimJobs returned: valid=%t err=%v", current.Valid, err)
		}
	})

	t.Run("invalid lease duration", func(t *testing.T) {
		configureWorkerProcessEnvironment(t)
		t.Setenv("WORKER_LEASE_DURATION", "bad")
		if err := run(); err == nil || err.Error() != "WORKER_LEASE_DURATION must be a positive Go duration" {
			t.Fatalf("run() error = %v, want lease duration error", err)
		}
	})

	t.Run("poll interval validation", func(t *testing.T) {
		configureWorkerProcessEnvironment(t)
		t.Setenv("WORKER_POLL_INTERVAL", "later")
		if err := run(); err == nil || err.Error() != "WORKER_POLL_INTERVAL must be a positive Go duration" {
			t.Fatalf("run() error = %v, want poll interval error", err)
		}
	})

	t.Run("idle worker is live and ready before SIGINT", func(t *testing.T) {
		configureWorkerProcessEnvironment(t)
		healthAddr := freeWorkerAddress(t)
		t.Setenv("HEALTH_ADDR", healthAddr)
		t.Setenv("WORKER_ID", "worker-idle-pg")
		t.Setenv("WORKER_POLL_INTERVAL", "10ms")
		done := make(chan error, 1)
		go func() { done <- run() }()
		waitForWorkerStatus(t, healthAddr, "/health/live", done, http.StatusOK)
		waitForWorkerStatus(t, healthAddr, "/ready", done, http.StatusOK)
		proc, err := os.FindProcess(os.Getpid())
		if err != nil {
			t.Fatal("find process for interrupt signal")
		}
		if err := proc.Signal(os.Interrupt); err != nil {
			t.Fatal("send process interrupt")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("run() after SIGINT = %v, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not drain after SIGINT")
		}
	})

}

func configureWorkerProcessEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_DATABASE_MODE", "application")
	t.Setenv("EXECUTION_MODE", "durable")
	t.Setenv("APP_READ_ONLY", "false")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("IDENTITY_DIRECTORY_FILE", "")
	t.Setenv("S3_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("HEALTH_ADDR", "127.0.0.1:0")
	t.Setenv("WORKER_ID", "worker-test")
	t.Setenv("WORKER_LEASE_DURATION", "")
	t.Setenv("WORKER_HEARTBEAT_INTERVAL", "")
	t.Setenv("WORKER_POLL_INTERVAL", "")
	t.Setenv("OUTBOX_RELAY_BATCH", "")
	t.Setenv("NATS_URL", "")
	t.Setenv("MODEL_GATEWAY_URL", "http://127.0.0.1:1/v1")
	t.Setenv("LLM_BASE_URL", "http://127.0.0.1:1/v1")
	t.Setenv("LLM_MODEL", "worker-test-chat")
	t.Setenv("OCR_MODEL", "worker-test-ocr")
	t.Setenv("WORKER_MODEL_TOKEN", strings.Repeat("m", 40))
	t.Setenv("DOCUMENT_MODEL_TOKEN", strings.Repeat("d", 40))
	t.Setenv("DOCUMENT_SERVICE_URL", "http://127.0.0.1:1")
	t.Setenv("WORKER_DOCUMENT_TOKEN", strings.Repeat("w", 40))
	t.Setenv("TOOL_GATEWAY_URL", "http://127.0.0.1:1")
	t.Setenv("WORKER_TOOL_TOKEN", strings.Repeat("t", 40))
}

type workerCommandEnvironment struct {
	ctx       context.Context
	cancel    context.CancelFunc
	db        *pgx.Conn
	schema    string
	roles     []string
	modelHTTP *httptest.Server
	docHTTP   *httptest.Server
	runtimes  []*bootstrap.Runtime
	core      *bootstrap.Runtime
	document  *bootstrap.Runtime
	execution *bootstrap.Runtime
	worker    *bootstrap.Runtime
	actor     accounts.User
}

func newWorkerCommandEnvironment(t *testing.T, modelHandler http.Handler) *workerCommandEnvironment {
	t.Helper()
	configureWorkerProcessEnvironment(t)
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if baseDSN == "" || migratorDSN == "" {
		t.Fatal("worker command characterization requires TEST_DATABASE_URL and MIGRATOR_DATABASE_URL")
	}
	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("create isolated worker database identity")
	}
	suffix := hex.EncodeToString(nonce[:])
	env := &workerCommandEnvironment{schema: "it_cmd_worker_" + suffix}
	env.ctx, env.cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	t.Setenv("TEST_DATABASE_URL", baseDSN)
	t.Setenv("MIGRATOR_DATABASE_URL", migratorDSN)
	t.Setenv("DATABASE_SCHEMA", env.schema)
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		role := "it_cmd_" + service + "_" + suffix
		parsed, err := url.Parse(baseDSN)
		if err != nil {
			t.Fatal("parse isolated test database URL")
		}
		parsed.User = url.UserPassword(role, strings.Repeat(suffix, 4))
		t.Setenv(strings.ToUpper(service)+"_DATABASE_URL", parsed.String())
		env.roles = append(env.roles, role)
	}
	db, err := pgx.Connect(env.ctx, migratorDSN)
	if err != nil {
		t.Fatal("connect isolated worker database migrator")
	}
	env.db = db
	if modelHandler == nil {
		modelHandler = http.NotFoundHandler()
	}
	env.modelHTTP = httptest.NewServer(modelHandler)
	t.Setenv("LLM_BASE_URL", env.modelHTTP.URL+"/v1")
	t.Setenv("MODEL_GATEWAY_URL", env.modelHTTP.URL+"/v1")
	t.Setenv("TOOL_GATEWAY_URL", env.modelHTTP.URL)
	t.Cleanup(func() { env.close(migratorDSN) })
	if err := store.Migrate(env.ctx); err != nil {
		t.Fatalf("migrate isolated worker database (%T)", err)
	}
	if err := store.MigrateServiceRoles(env.ctx); err != nil {
		t.Fatalf("provision isolated worker database roles (%T)", err)
	}
	if err := accounts.ConfigureDirectory(""); err != nil {
		t.Fatal("configure worker test identity directory")
	}
	var found bool
	env.actor, found = accounts.ByID("demo-admin")
	if !found {
		t.Fatal("worker test actor is unavailable")
	}
	env.core = env.open(t, "core")
	env.document = env.open(t, "document")
	env.execution = env.open(t, "execution")
	service, err := documentservice.New(env.document.Store, documentservice.Config{
		WorkerCredential: os.Getenv("WORKER_DOCUMENT_TOKEN"), CPUConcurrency: 1, OCRConcurrency: 1, QueueCapacity: 4, ParseTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create document test service (%T)", err)
	}
	env.docHTTP = httptest.NewServer(service.Handler(http.NotFoundHandler()))
	t.Setenv("DOCUMENT_SERVICE_URL", env.docHTTP.URL)
	env.worker = env.open(t, "worker")
	return env
}

func (env *workerCommandEnvironment) open(t *testing.T, role string) *bootstrap.Runtime {
	t.Helper()
	runtime, err := bootstrap.OpenService(env.ctx, role)
	if err != nil {
		t.Fatalf("open %s worker test runtime (%T)", role, err)
	}
	env.runtimes = append(env.runtimes, runtime)
	return runtime
}

func (env *workerCommandEnvironment) close(migratorDSN string) {
	env.cancel()
	for index := len(env.runtimes) - 1; index >= 0; index-- {
		_ = env.runtimes[index].Close()
	}
	if env.docHTTP != nil {
		env.docHTTP.Close()
	}
	if env.modelHTTP != nil {
		env.modelHTTP.Close()
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if env.db != nil {
		_, _ = env.db.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{env.schema}.Sanitize()+" CASCADE")
		for _, role := range env.roles {
			_, _ = env.db.Exec(cleanupCtx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
		}
		_ = env.db.Close(cleanupCtx)
	} else {
		if conn, err := pgx.Connect(cleanupCtx, migratorDSN); err == nil {
			_, _ = conn.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{env.schema}.Sanitize()+" CASCADE")
			for _, role := range env.roles {
				_, _ = conn.Exec(cleanupCtx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
			}
			_ = conn.Close(cleanupCtx)
		}
	}
	_ = accounts.ConfigureDirectory("")
}

func (env *workerCommandEnvironment) createReview(t *testing.T) store.JobAcceptance {
	t.Helper()
	caseID, err := env.core.Store.CreateCase(env.actor, "Worker lifecycle test", "procurement_ict")
	if err != nil {
		t.Fatalf("create worker test case (%T)", err)
	}
	for _, document := range []struct{ role, filename, body string }{
		{"procurement", "requirements.txt", "采购文件要求质保期为36个月。"},
		{"draft_contract", "draft.txt", "拟签合同约定质保期为24个月。"},
	} {
		if _, err := env.document.Store.AddDocument(env.ctx, env.actor, caseID, document.role, document.filename, "text/plain", []byte(document.body)); err != nil {
			t.Fatalf("add worker test document (%T)", err)
		}
	}
	accepted, err := env.execution.Store.StartReviewAsync(env.ctx, env.actor, caseID)
	if err != nil {
		t.Fatalf("accept worker test review (%T)", err)
	}
	return accepted
}

func (env *workerCommandEnvironment) workerJobState(jobID string) (string, string, int64, pgtype.Timestamptz, error) {
	query := "SELECT state,worker_id,generation,heartbeat_at FROM " + pgx.Identifier{env.schema, "worker_jobs"}.Sanitize() + " WHERE id=$1"
	var state, workerID string
	var generation int64
	var heartbeat pgtype.Timestamptz
	err := env.db.QueryRow(env.ctx, query, jobID).Scan(&state, &workerID, &generation, &heartbeat)
	return state, workerID, generation, heartbeat, err
}

type blockingWorkerModel struct{ entered chan struct{} }

func (blockingWorkerModel) Name() string            { return "worker-test-model" }
func (blockingWorkerModel) ConfigurationID() string { return "worker-test-configuration" }
func (m blockingWorkerModel) Warranty(ctx context.Context, _ []model.Material) (model.Warranty, error) {
	select {
	case m.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return model.Warranty{}, ctx.Err()
}
func (blockingWorkerModel) Transcribe(context.Context, string, []byte) (string, error) {
	return "", errors.New("unexpected OCR request in text-only worker test")
}

func waitForWorkerStatus(t *testing.T, addr, path string, done <-chan error, status int) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + addr + path)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != status {
				t.Fatalf("worker liveness status=%d, want %d", response.StatusCode, status)
			}
			return
		}
		select {
		case runErr := <-done:
			t.Fatalf("worker stopped before health response: %v", runErr)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("worker health server did not respond within 10 seconds")
}

func freeWorkerAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve worker health address")
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal("release worker health address")
	}
	return addr
}

func waitForWorkerHeartbeatChange(t *testing.T, env *workerCommandEnvironment, jobID, workerID string, initial time.Time, timeout time.Duration) time.Time {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, currentWorker, generation, heartbeat, err := env.workerJobState(jobID)
		if err != nil {
			t.Fatalf("read worker job lease state (%T)", err)
		}
		if state != "running" || currentWorker != workerID || generation < 1 {
			t.Fatalf("worker claim identity changed: state=%s worker=%s generation=%d", state, currentWorker, generation)
		}
		if heartbeat.Valid && heartbeat.Time.After(initial) {
			return heartbeat.Time
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("PostgreSQL heartbeat did not advance while the worker job was active")
	return time.Time{}
}

func waitForInitialWorkerHeartbeat(t *testing.T, env *workerCommandEnvironment, jobID, workerID string) time.Time {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state, currentWorker, generation, heartbeat, err := env.workerJobState(jobID)
		if err != nil {
			t.Fatalf("read worker claim state (%T)", err)
		}
		if state == "running" && currentWorker == workerID && generation > 0 && heartbeat.Valid {
			return heartbeat.Time
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("PostgreSQL did not record the worker claim and initial lease heartbeat")
	return time.Time{}
}
