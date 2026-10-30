package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/bootstrap"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/config"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestProductionStartupRejectsDemoAccountsBeforeOpeningStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVICE_ROLE", "core")
	t.Setenv("APP_DATABASE_MODE", "application")
	t.Setenv("EXECUTION_MODE", "durable")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("CORE_DATABASE_URL", "postgres://user:secret@127.0.0.1:1/contract_audit_prod?sslmode=disable")
	t.Setenv("DEV_DATABASE_URL", "")
	t.Setenv("TEST_DATABASE_URL", "")
	t.Setenv("DATA_DIR", dir)
	err := run()
	if err == nil || !strings.Contains(err.Error(), "demo accounts") {
		t.Fatalf("production did not stop at identity boundary: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("storage was opened before identity validation: %v", err)
	}
}

func TestProductionRequiresExplicitServiceRole(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("DATA_DIR", dir)
	err := run()
	if err == nil || !strings.Contains(err.Error(), "SERVICE_ROLE") {
		t.Fatalf("production without SERVICE_ROLE was not refused: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("refused startup opened storage: %v", err)
	}
}

func TestUnknownServiceRoleIsRefused(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("SERVICE_ROLE", "document")
	err := run()
	if err == nil || !strings.Contains(err.Error(), "SERVICE_ROLE") {
		t.Fatalf("unknown service role was not refused: %v", err)
	}
}

func TestInvalidReadOnlySettingStopsBeforeOpeningStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("APP_ENV", "test")
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("TEST_DATABASE_URL", "postgres://user:secret@127.0.0.1:1/contract_audit_test?sslmode=disable")
	t.Setenv("DEV_DATABASE_URL", "")
	t.Setenv("PROD_DATABASE_URL", "")
	t.Setenv("DATA_DIR", dir)
	t.Setenv("APP_READ_ONLY", "enabled")
	if err := run(); err == nil || !strings.Contains(err.Error(), "APP_READ_ONLY") {
		t.Fatalf("invalid read-only setting was not refused before storage: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("invalid rollback setting opened storage: %v", err)
	}
}

func TestOriginsPreservesDefaultsAndConfiguredOrder(t *testing.T) {
	t.Setenv("CORS_ORIGIN", " \t")
	if got := origins(); got != nil {
		t.Fatalf("origins() with blank configuration = %#v, want nil", got)
	}

	t.Setenv("CORS_ORIGIN", " https://east.example , ,https://west.example,https://east.example ")
	want := []string{
		"http://localhost:5173", "http://127.0.0.1:5173",
		"https://east.example", "https://west.example", "https://east.example",
	}
	if got := origins(); !reflect.DeepEqual(got, want) {
		t.Fatalf("origins() = %#v, want %#v", got, want)
	}
}

func TestSplitListTrimsDropsEmptyEntriesAndPreservesDuplicates(t *testing.T) {
	if got := splitList(" \t, , "); got != nil {
		t.Fatalf("splitList() for blank items = %#v, want nil", got)
	}
	want := []string{"east", "west", "east", "central"}
	if got := splitList(" east, ,west, east ,\t, central "); !reflect.DeepEqual(got, want) {
		t.Fatalf("splitList() = %#v, want %#v", got, want)
	}
}

func TestListenReturnsTheListenerFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve an address for the listener failure test")
	}
	defer listener.Close()
	t.Setenv("ADDR", listener.Addr().String())

	if err := listen(context.Background(), http.NotFoundHandler()); err == nil {
		t.Fatal("listen() returned nil for an occupied address")
	}
}

func TestListenWaitsForAnInflightRequestBeforeReturning(t *testing.T) {
	addr := freeServerAddress(t)
	t.Setenv("ADDR", addr)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listen(ctx, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusNoContent)
		}))
	}()
	stopped := false
	t.Cleanup(func() {
		cancel()
		releaseHandler()
		if !stopped {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("listener did not stop during cleanup")
			}
		}
	})

	waitForServerSocket(t, addr, done)
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + addr + "/inflight")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				err = errors.New("inflight response status was not 204")
			}
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case err := <-requestDone:
		t.Fatalf("inflight request failed before reaching the handler: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("inflight request did not reach the server")
	}
	cancel()
	select {
	case err := <-done:
		stopped = true
		t.Fatalf("listen() returned before its inflight request completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	releaseHandler()
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("inflight HTTP request after release: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("inflight HTTP request did not finish after release")
	}
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatalf("listen() after cancellation = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not finish graceful shutdown")
	}
}

func TestOperatorAuditConfigKeepsTheExtensionDefaultOff(t *testing.T) {
	for _, test := range []struct {
		enabledValue string
		wantLog      string
	}{
		{enabledValue: ""},
		{enabledValue: "false"},
		{enabledValue: "not-a-boolean", wantLog: "operator audit disabled: OPERATOR_AUDIT_ENABLED is not a boolean"},
	} {
		t.Run("enabled="+test.enabledValue, func(t *testing.T) {
			t.Setenv("OPERATOR_AUDIT_ENABLED", test.enabledValue)
			t.Setenv("EXTERNAL_EVIDENCE_MODE", "off")
			t.Setenv("EXTERNAL_EVIDENCE_CONNECTOR_ID", "invalid connector id")
			t.Setenv("EXTERNAL_EVIDENCE_BASE_URL", "://not a URL")
			var enabled bool
			var registry *capabilitypack.Registry
			var connectors map[string]auditext.ReadConnector
			var catalog []httpapi.OperatorConnectorInfo
			output := captureServerLog(t, func() {
				enabled, registry, connectors, catalog = operatorAuditConfig(config.ModeTest)
			})
			if enabled || registry != nil || connectors != nil || catalog != nil {
				t.Fatalf("disabled operator audit configuration = %t/%t/%d/%d", enabled, registry != nil, len(connectors), len(catalog))
			}
			wantLog := test.wantLog
			if wantLog != "" {
				wantLog += "\n"
			}
			if output != wantLog {
				t.Fatalf("default-off log = %q, want %q", output, wantLog)
			}
		})
	}
}

func TestOperatorAuditConfigStopsAtTheConfiguredEvidenceMode(t *testing.T) {
	t.Run("off keeps the registry but ignores connector settings", func(t *testing.T) {
		t.Setenv("OPERATOR_AUDIT_ENABLED", "true")
		t.Setenv("EXTERNAL_EVIDENCE_MODE", "off")
		t.Setenv("EXTERNAL_EVIDENCE_CONNECTOR_ID", "invalid connector id")
		t.Setenv("EXTERNAL_EVIDENCE_BASE_URL", "://not a URL")
		enabled, registry, connectors, catalog := operatorAuditConfig(config.ModeProduction)
		if !enabled || registry == nil || connectors == nil || len(connectors) != 0 || catalog != nil {
			t.Fatalf("off-mode operator audit config = %t/%t/%d/%d", enabled, registry != nil, len(connectors), len(catalog))
		}
	})

	for _, test := range []struct {
		name    string
		mode    string
		app     string
		file    string
		wantLog string
	}{
		{name: "fixture in production", mode: "fixture", app: config.ModeProduction, file: "../../testdata/operatoraudit/raw/oss-bandwidth.json", wantLog: `operator audit disabled: fixture external evidence is not allowed in "production"`},
		{name: "live remains disabled", mode: "live", app: config.ModeTest, wantLog: "operator audit disabled: live external evidence requires a separate approved data path"},
		{name: "unknown remains disabled", mode: "unexpected", app: config.ModeTest, wantLog: `operator audit disabled: unknown EXTERNAL_EVIDENCE_MODE "unexpected"`},
		{name: "missing fixture fails closed", mode: "fixture", app: config.ModeTest, file: "missing-fixture.json", wantLog: "operator audit disabled: fixture file: open missing-fixture.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OPERATOR_AUDIT_ENABLED", "true")
			t.Setenv("EXTERNAL_EVIDENCE_MODE", test.mode)
			t.Setenv("EXTERNAL_EVIDENCE_FIXTURE_FILE", test.file)
			t.Setenv("EXTERNAL_EVIDENCE_CONNECTOR_ID", "")
			t.Setenv("EXTERNAL_EVIDENCE_SOURCE_SYSTEM", "")
			t.Setenv("EXTERNAL_EVIDENCE_BASE_URL", "")
			t.Setenv("EXTERNAL_EVIDENCE_ALLOWED_OBJECTS", "")
			t.Setenv("EXTERNAL_EVIDENCE_PAYLOAD_PREFIX", "")
			var enabled bool
			var registry *capabilitypack.Registry
			var connectors map[string]auditext.ReadConnector
			var catalog []httpapi.OperatorConnectorInfo
			output := captureServerLog(t, func() {
				enabled, registry, connectors, catalog = operatorAuditConfig(test.app)
			})
			if enabled || registry != nil || connectors != nil || catalog != nil {
				t.Fatalf("disabled external evidence config = %t/%t/%d/%d", enabled, registry != nil, len(connectors), len(catalog))
			}
			if !strings.Contains(output, test.wantLog) {
				t.Fatalf("operator audit log %q, want reason %q", output, test.wantLog)
			}
			if strings.Contains(output, "operator audit disabled: external connector:") {
				t.Fatalf("operator audit emitted a generic connector error after the configured reason: %q", output)
			}
		})
	}
}

func captureServerLog(t *testing.T, action func()) string {
	t.Helper()
	var output bytes.Buffer
	previousWriter, previousFlags := log.Writer(), log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	}()
	action()
	return output.String()
}

func TestDevelopmentCompatibilityUsesAppEnvironmentAllowList(t *testing.T) {
	for _, test := range []struct {
		app  string
		want bool
	}{
		{app: "", want: true}, {app: " DEVELOPMENT ", want: true}, {app: "Dev", want: true},
		{app: "test", want: true}, {app: "production", want: false}, {app: "staging", want: false},
	} {
		t.Run(test.app, func(t *testing.T) {
			t.Setenv("APP_ENV", test.app)
			if got := developmentCompatibility(); got != test.want {
				t.Fatalf("developmentCompatibility() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestServeLegacyReturnsBootstrapCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serveLegacy(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("serveLegacy(canceled context) = %v, want context.Canceled", err)
	}
}

func TestLogStartupReportsInstanceDatabaseAndModels(t *testing.T) {
	app := &bootstrap.Runtime{
		Models:    model.New(model.Config{ChatModel: "review-v7", OCRModel: "ocr-v3"}),
		Selection: config.Selection{Mode: "test", Database: config.Identity{Name: "audit_test", Host: "db.internal", Port: "5433"}},
	}
	output := captureServerLog(t, func() { logStartup(app) })
	for _, want := range []string{
		"instance ", fmt.Sprintf("pid %d", os.Getpid()),
		"database audit_test at db.internal:5433 (test)", "review model review-v7, ocr model ocr-v3",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("startup log %q does not contain %q", output, want)
		}
	}
}

func TestOptionsPreservesRuntimeFlagsAndKeepsAuditDefaultOff(t *testing.T) {
	t.Setenv("OPERATOR_AUDIT_ENABLED", "")
	t.Setenv("WORKER_CORE_TOKEN", " worker-token ")
	app := &bootstrap.Runtime{
		Models: model.New(model.Config{ChatModel: "review-v7"}), Durable: true, SecureCookies: true,
		Selection: config.Selection{Mode: config.ModeTest, Database: config.Identity{Name: "audit_test", Host: "localhost", Port: "5432"}},
	}
	got := options(app, []string{"https://app.example"})
	if got.Models != app.Models || !got.Durable || !got.SecureCookies || got.ExperienceWorkerCredential != "worker-token" {
		t.Fatalf("runtime options lost configured values: %+v", got)
	}
	if !reflect.DeepEqual(got.CORSOrigins, []string{"https://app.example"}) || got.SystemHealth == nil {
		t.Fatalf("runtime options origins/health = %#v/%v", got.CORSOrigins, got.SystemHealth)
	}
	if got.OperatorAuditEnabled || got.CapabilityPacks != nil || got.ExternalConnectors != nil || got.ExternalConnectorInfo != nil {
		t.Fatalf("operator audit was enabled without its explicit switch: %+v", got)
	}
}

func TestMainRejectsUnknownRoleInChildProcess(t *testing.T) {
	if os.Getenv("CAW_SERVER_MAIN_CHILD") == "1" {
		main()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"-test.run=^TestMainRejectsUnknownRoleInChildProcess$"}
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-test.gocoverdir=") {
			args = append(args, arg)
			break
		}
	}
	command := exec.CommandContext(ctx, os.Args[0], args...)
	command.Env = replaceServerTestEnvironment(os.Environ(), map[string]string{
		"CAW_SERVER_MAIN_CHILD": "1", "SERVICE_ROLE": "unexpected", "APP_ENV": "development",
	})
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exitError) || exitError.ExitCode() != 1 || !strings.Contains(string(output), `unknown SERVICE_ROLE "unexpected"`) {
		t.Fatalf("main() child err=%v output=%q, want unknown-role fatal", err, output)
	}
}

func TestOperatorAuditFixtureConnectorInBoundedChild(t *testing.T) {
	if os.Getenv("CAW_SERVER_OPERATOR_AUDIT_CHILD") == "1" {
		enabled, registry, connectors, catalog := operatorAuditConfig(config.ModeTest)
		if !enabled || registry == nil || len(connectors) != 1 || len(catalog) != 1 {
			t.Fatalf("fixture operator audit config = %t/%t/%d/%d", enabled, registry != nil, len(connectors), len(catalog))
		}
		if catalog[0].ConnectorID != "fixture.oss" || catalog[0].SourceSystem != "fixture-oss-east" || !reflect.DeepEqual(catalog[0].AllowedObjects, []string{"fixture-line-001"}) {
			t.Fatalf("fixture operator connector catalog = %+v", catalog)
		}
		observation, err := connectors["fixture.oss"].Read(context.Background(), auditext.ReadRequest{
			Scope: auditext.AuthorizedScope{
				TenantID: "fixture-tenant-a", CaseID: "fixture-case-001", ActorID: "fixture-actor-q", GrantVersion: "fixture-grant-v1",
			},
			ConnectorID: "fixture.oss", BindingVersion: "fixture-binding-v1", ExternalObjectID: "fixture-line-001",
			BusinessAt: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
		})
		if err != nil || observation.Availability != auditext.AvailabilityAvailable || observation.ReasonCode != "read_succeeded" {
			t.Fatalf("fixture read availability=%s reason=%s err=%v", observation.Availability, observation.ReasonCode, err)
		}
		field, ok := observation.FieldByName("configured_bandwidth")
		if !ok || field.Value != "80" || field.Unit != "Mbit/s" || field.SourcePointer != "/serviceCharacteristic/1/value" {
			t.Fatalf("fixture bandwidth field = %+v found=%t", field, ok)
		}

		t.Setenv("EXTERNAL_EVIDENCE_CONNECTOR_ID", "invalid connector id")
		var connectorFailureLog string
		connectorFailureLog = captureServerLog(t, func() {
			enabled, registry, connectors, catalog = operatorAuditConfig(config.ModeTest)
		})
		if enabled || registry != nil || connectors != nil || catalog != nil {
			t.Fatalf("invalid fixture connector config = %t/%t/%d/%d", enabled, registry != nil, len(connectors), len(catalog))
		}
		if !strings.Contains(connectorFailureLog, "operator audit disabled: external connector:") {
			t.Fatalf("invalid connector log lost its connector error: %q", connectorFailureLog)
		}
		return
	}

	childCtx, cancelChild := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelChild()
	childArgs := []string{"-test.run=^TestOperatorAuditFixtureConnectorInBoundedChild$"}
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-test.gocoverdir=") {
			childArgs = append(childArgs, arg)
			break
		}
	}
	command := exec.CommandContext(childCtx, os.Args[0], childArgs...)
	command.Env = replaceServerTestEnvironment(os.Environ(), map[string]string{
		"CAW_SERVER_OPERATOR_AUDIT_CHILD":   "1",
		"OPERATOR_AUDIT_ENABLED":            "true",
		"EXTERNAL_EVIDENCE_MODE":            "fixture",
		"EXTERNAL_EVIDENCE_FIXTURE_FILE":    "../../testdata/operatoraudit/raw/oss-bandwidth.json",
		"EXTERNAL_EVIDENCE_ALLOWED_OBJECTS": "",
		"EXTERNAL_EVIDENCE_BASE_URL":        "://not a URL",
		"EXTERNAL_EVIDENCE_CONNECTOR_ID":    "",
		"EXTERNAL_EVIDENCE_SOURCE_SYSTEM":   "",
		"EXTERNAL_EVIDENCE_PAYLOAD_PREFIX":  "",
	})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bounded fixture connector process failed: %v: %s", err, output)
	}
}

func TestServeCoreKeepsHealthPublicAndBusinessRoutesDefaultDeny(t *testing.T) {
	configureServerCommandEnvironment(t)
	setupServerCommandDatabase(t)
	addr := freeServerAddress(t)
	t.Setenv("ADDR", addr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveCore(ctx) }()
	stopped := false
	t.Cleanup(func() {
		cancel()
		if !stopped {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("core service did not stop during test cleanup")
			}
		}
	})
	waitForServerStatus(t, addr, "/health/live", done, http.StatusOK)

	response, err := http.Get("http://" + addr + "/openapi.json")
	if err != nil {
		t.Fatal("request OpenAPI without a service key")
	}
	var unauthorized struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&unauthorized)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || decodeErr != nil || unauthorized.Error != "service authentication required" || unauthorized.Code != "service_unauthenticated" {
		t.Fatalf("unauthenticated core OpenAPI status=%d body=%+v decodeErr=%v", response.StatusCode, unauthorized, decodeErr)
	}

	request, err := http.NewRequest(http.MethodGet, "http://"+addr+"/openapi.json", nil)
	if err != nil {
		t.Fatal("create authenticated OpenAPI request")
	}
	request.Header.Set("X-Contract-Service-Key", strings.Repeat("g", 40))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal("request OpenAPI with the configured service key")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated core OpenAPI status=%d, want 200", response.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatalf("serveCore() after cancellation = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("core service did not drain after cancellation")
	}
}

func configureServerCommandEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SERVICE_ROLE", "APP_ENV", "APP_DATABASE_MODE", "EXECUTION_MODE", "APP_READ_ONLY", "COOKIE_SECURE",
		"DATA_DIR", "ADDR", "DATABASE_SCHEMA", "IDENTITY_DIRECTORY_FILE", "CORS_ORIGIN",
		"OPERATOR_AUDIT_ENABLED", "EXTERNAL_EVIDENCE_MODE", "EXTERNAL_EVIDENCE_CONNECTOR_ID", "EXTERNAL_EVIDENCE_SOURCE_SYSTEM",
		"EXTERNAL_EVIDENCE_BASE_URL", "EXTERNAL_EVIDENCE_ALLOWED_OBJECTS", "EXTERNAL_EVIDENCE_PAYLOAD_PREFIX", "EXTERNAL_EVIDENCE_FIXTURE_FILE",
		"GATEWAY_ROLE_TOKEN", "WORKER_CORE_TOKEN", "MODEL_GATEWAY_URL", "DOCUMENT_SERVICE_URL", "WORKER_DOCUMENT_TOKEN",
		"TOOL_GATEWAY_URL", "WORKER_TOOL_TOKEN", "WORKER_MODEL_TOKEN", "DOCUMENT_MODEL_TOKEN", "LLM_BASE_URL", "LLM_MODEL", "OCR_MODEL",
		"DEV_DATABASE_URL", "PROD_DATABASE_URL", "CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL",
		"EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL", "OTEL_EXPORTER_OTLP_ENDPOINT", "S3_ENDPOINT",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", config.ModeTest)
	t.Setenv("APP_DATABASE_MODE", "application")
	t.Setenv("EXECUTION_MODE", "durable")
	t.Setenv("APP_READ_ONLY", "false")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("SERVICE_ROLE", "core")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("GATEWAY_ROLE_TOKEN", strings.Repeat("g", 40))
	t.Setenv("WORKER_CORE_TOKEN", strings.Repeat("c", 40))
	t.Setenv("LLM_BASE_URL", "http://127.0.0.1:1/v1")
	t.Setenv("LLM_MODEL", "server-core-test-chat")
	t.Setenv("OCR_MODEL", "server-core-test-ocr")
}

type serverCommandDatabase struct {
	db     *pgx.Conn
	schema string
	roles  []string
}

func setupServerCommandDatabase(t *testing.T) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if baseDSN == "" || migratorDSN == "" {
		t.Fatal("server command characterization requires TEST_DATABASE_URL and MIGRATOR_DATABASE_URL")
	}
	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("create isolated server database identity")
	}
	suffix := hex.EncodeToString(nonce[:])
	env := &serverCommandDatabase{schema: "it_cmd_server_" + suffix}
	t.Setenv("TEST_DATABASE_URL", baseDSN)
	t.Setenv("MIGRATOR_DATABASE_URL", migratorDSN)
	t.Setenv("DATABASE_SCHEMA", env.schema)
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		role := "it_cmd_server_" + service + "_" + suffix
		parsed, err := url.Parse(baseDSN)
		if err != nil {
			t.Fatal("parse server test database URL")
		}
		parsed.User = url.UserPassword(role, strings.Repeat(suffix, 4))
		t.Setenv(strings.ToUpper(service)+"_DATABASE_URL", parsed.String())
		env.roles = append(env.roles, role)
	}
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
	db, err := pgx.Connect(cleanupCtx, migratorDSN)
	cancelCleanup()
	if err != nil {
		t.Fatal("connect isolated server migration database")
	}
	env.db = db
	t.Cleanup(func() { env.close(t) })
	migrateCtx, cancelMigration := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelMigration()
	if err := store.Migrate(migrateCtx); err != nil {
		t.Fatalf("migrate isolated server test schema (%T)", err)
	}
	if err := store.MigrateServiceRoles(migrateCtx); err != nil {
		t.Fatalf("provision isolated server test roles (%T)", err)
	}
}

func (env *serverCommandDatabase) close(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if env.db == nil {
		return
	}
	if _, err := env.db.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{env.schema}.Sanitize()+" CASCADE"); err != nil {
		t.Error("drop isolated server test schema")
	}
	for _, role := range env.roles {
		if _, err := env.db.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error("drop isolated server test role")
		}
	}
	if err := env.db.Close(ctx); err != nil {
		t.Error("close isolated server migration connection")
	}
}

func freeServerAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve an address for the server command test")
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal("release the server command test address")
	}
	return addr
}

func waitForServerSocket(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		select {
		case runErr := <-done:
			t.Fatalf("server stopped before binding %s: %v", addr, runErr)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server did not bind %s within 3 seconds", addr)
}

func waitForServerStatus(t *testing.T, addr, path string, done <-chan error, status int) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + addr + path)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != status {
				t.Fatalf("server status for %s=%d, want %d", path, response.StatusCode, status)
			}
			return
		}
		select {
		case runErr := <-done:
			t.Fatalf("server stopped before %s responded: %v", path, runErr)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not respond at %s within 10 seconds", path)
}

func replaceServerTestEnvironment(environment []string, replacements map[string]string) []string {
	filtered := make([]string, 0, len(environment)+len(replacements))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := replacements[name]; !replaced {
			filtered = append(filtered, entry)
		}
	}
	for name, value := range replacements {
		filtered = append(filtered, name+"="+value)
	}
	return filtered
}
