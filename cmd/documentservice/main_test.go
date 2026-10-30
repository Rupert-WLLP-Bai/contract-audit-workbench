package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestOriginsPreservesDefaultsAndTrimsConfiguredValues(t *testing.T) {
	t.Setenv("CORS_ORIGIN", " \t")
	if got := origins(); got != nil {
		t.Fatalf("origins() with blank configuration = %#v, want nil", got)
	}

	t.Setenv("CORS_ORIGIN", " https://east.example , ,https://west.example  ")
	want := []string{
		"http://localhost:5173", "http://127.0.0.1:5173",
		"https://east.example", "https://west.example",
	}
	if got := origins(); !equalStrings(got, want) {
		t.Fatalf("origins() = %#v, want %#v", got, want)
	}
}

func TestRunPreservesStartupValidationAndServesBeforeSignalShutdown(t *testing.T) {
	configureDocumentCommandEnvironment(t)
	setupDocumentCommandDatabase(t)

	t.Run("configured role mismatch fails before startup", func(t *testing.T) {
		t.Setenv("SERVICE_ROLE", "model")
		err := run()
		if err == nil || err.Error() != "configured service identity does not match executable" {
			t.Fatalf("run() error = %v, want configured role mismatch", err)
		}
	})

	t.Run("invalid document configuration fails before serving", func(t *testing.T) {
		configureDocumentApplicationMode(t)
		t.Setenv("DOCUMENT_CPU_CONCURRENCY", "0")
		err := run()
		if err == nil || err.Error() != "DOCUMENT_CPU_CONCURRENCY must be an integer in 1..1024" {
			t.Fatalf("run() error = %v, want bounded CPU concurrency error", err)
		}
	})

	t.Run("missing worker credential is refused", func(t *testing.T) {
		configureDocumentApplicationMode(t)
		t.Setenv("DOCUMENT_CPU_CONCURRENCY", "")
		t.Setenv("WORKER_DOCUMENT_TOKEN", "")
		err := run()
		if err == nil || err.Error() != "document service requires a Store, private worker credential, and positive bounded capacities" {
			t.Fatalf("run() error = %v, want document service credential error", err)
		}
	})

	t.Run("invalid gateway credential is refused", func(t *testing.T) {
		configureDocumentApplicationMode(t)
		t.Setenv("WORKER_DOCUMENT_TOKEN", strings.Repeat("w", 40))
		t.Setenv("GATEWAY_ROLE_TOKEN", "short")
		err := run()
		if err == nil || err.Error() != "a private service credential of at least 32 bytes is required" {
			t.Fatalf("run() error = %v, want service credential error", err)
		}
	})

	t.Run("listener failure is returned", func(t *testing.T) {
		configureDocumentApplicationMode(t)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal("reserve listener for failure characterization")
		}
		defer listener.Close()
		t.Setenv("ADDR", listener.Addr().String())
		if err := run(); err == nil {
			t.Fatal("run() returned nil for an occupied listener")
		}
	})

	t.Run("liveness is served and SIGINT drains the process", func(t *testing.T) {
		configureDocumentApplicationMode(t)
		addr := freeDocumentAddress(t)
		t.Setenv("ADDR", addr)
		done := make(chan error, 1)
		go func() { done <- run() }()
		waitForDocumentHealth(t, addr, done)
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
			t.Fatal("document service did not drain after SIGINT")
		}
	})
}

func configureDocumentCommandEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_DATABASE_MODE", "application")
	t.Setenv("EXECUTION_MODE", "durable")
	t.Setenv("APP_READ_ONLY", "false")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("ADDR", "")
	t.Setenv("DATABASE_SCHEMA", "")
	t.Setenv("IDENTITY_DIRECTORY_FILE", "")
	t.Setenv("S3_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("CORS_ORIGIN", "")
	t.Setenv("MODEL_GATEWAY_URL", "http://127.0.0.1:1")
	t.Setenv("DOCUMENT_MODEL_TOKEN", strings.Repeat("d", 40))
	t.Setenv("WORKER_DOCUMENT_TOKEN", strings.Repeat("w", 40))
	t.Setenv("GATEWAY_ROLE_TOKEN", strings.Repeat("g", 40))
}

func configureDocumentApplicationMode(t *testing.T) {
	t.Helper()
	t.Setenv("APP_DATABASE_MODE", "application")
	t.Setenv("EXECUTION_MODE", "durable")
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("WORKER_DOCUMENT_TOKEN", strings.Repeat("w", 40))
	t.Setenv("GATEWAY_ROLE_TOKEN", strings.Repeat("g", 40))
}

func setupDocumentCommandDatabase(t *testing.T) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if baseDSN == "" || migratorDSN == "" {
		t.Fatal("document command characterization requires TEST_DATABASE_URL and MIGRATOR_DATABASE_URL")
	}

	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("create isolated document database identity")
	}
	suffix := hex.EncodeToString(nonce[:])
	schema := "it_cmd_document_" + suffix
	roles := make([]string, 0, 5)
	t.Setenv("TEST_DATABASE_URL", baseDSN)
	t.Setenv("MIGRATOR_DATABASE_URL", migratorDSN)
	t.Setenv("DATABASE_SCHEMA", schema)
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		role := "it_cmd_" + service + "_" + suffix
		parsed, err := url.Parse(baseDSN)
		if err != nil {
			t.Fatal("parse isolated test database URL")
		}
		parsed.User = url.UserPassword(role, strings.Repeat(suffix, 4))
		t.Setenv(strings.ToUpper(service)+"_DATABASE_URL", parsed.String())
		roles = append(roles, role)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, err := pgx.Connect(cleanupCtx, migratorDSN)
		if err != nil {
			t.Error("connect document test database cleanup")
			return
		}
		defer conn.Close(cleanupCtx)
		if _, err := conn.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error("drop isolated document test schema")
		}
		for _, role := range roles {
			if _, err := conn.Exec(cleanupCtx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("drop isolated document test role")
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate isolated document test database (%T)", err)
	}
	if err := store.MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("provision isolated document test roles (%T)", err)
	}
}

func freeDocumentAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve document service address")
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal("release document service address")
	}
	return addr
}

func waitForDocumentHealth(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + addr + "/health/live")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("document liveness status=%d, want 200", response.StatusCode)
			}
			return
		}
		select {
		case runErr := <-done:
			t.Fatalf("document service stopped before liveness: %v", runErr)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("document service did not serve liveness within 10 seconds")
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
