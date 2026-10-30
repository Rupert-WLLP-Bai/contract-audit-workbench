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

func TestRunPreservesStartupErrorsAndServesBeforeSignalShutdown(t *testing.T) {
	configureModelGatewayEnvironment(t)
	setupModelCommandDatabase(t)

	t.Run("configured role mismatch fails before database access", func(t *testing.T) {
		t.Setenv("SERVICE_ROLE", "document")
		err := run()
		if err == nil || err.Error() != "configured service identity does not match executable" {
			t.Fatalf("run() error = %v, want configured role mismatch", err)
		}
	})

	t.Run("invalid environment override is reported", func(t *testing.T) {
		t.Setenv("SERVICE_ROLE", "")
		t.Setenv("MODEL_MAX_CONCURRENCY", "0")
		err := run()
		if err == nil || err.Error() != "MODEL_MAX_CONCURRENCY is outside its supported integer range" {
			t.Fatalf("run() error = %v, want bounded concurrency error", err)
		}
	})

	t.Run("fixture mode requires explicit OCR content", func(t *testing.T) {
		t.Setenv("MODEL_MAX_CONCURRENCY", "")
		t.Setenv("MODEL_GATEWAY_FIXTURE_OCR_CONTENT", "")
		err := run()
		if err == nil || err.Error() != "local model fixture OCR content must be explicitly configured" {
			t.Fatalf("run() error = %v, want fixture OCR content error", err)
		}
	})

	t.Run("listener failure is returned", func(t *testing.T) {
		configureModelGatewayService(t)
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
		configureModelGatewayService(t)
		addr := freeModelAddress(t)
		t.Setenv("ADDR", addr)
		done := make(chan error, 1)
		go func() { done <- run() }()
		waitForModelHealth(t, addr, done)
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
			t.Fatal("model gateway did not drain after SIGINT")
		}
	})
}

func configureModelGatewayEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_DATABASE_MODE", "application")
	t.Setenv("EXECUTION_MODE", "durable")
	t.Setenv("APP_READ_ONLY", "false")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("DATA_DIR", t.TempDir())
	configureModelGatewayService(t)
}

func configureModelGatewayService(t *testing.T) {
	t.Helper()
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("MODEL_GATEWAY_MODE", "fixture")
	t.Setenv("MODEL_GATEWAY_FIXTURE_OCR_CONTENT", "fixture OCR evidence")
	t.Setenv("LLM_MODEL", "fixture-chat")
	t.Setenv("OCR_MODEL", "fixture-ocr")
	t.Setenv("WORKER_MODEL_TOKEN", strings.Repeat("w", 40))
	t.Setenv("DOCUMENT_MODEL_TOKEN", strings.Repeat("d", 40))
	t.Setenv("MODEL_MAX_CONCURRENCY", "")
	t.Setenv("ADDR", "")
}

func setupModelCommandDatabase(t *testing.T) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if baseDSN == "" || migratorDSN == "" {
		t.Fatal("model command characterization requires TEST_DATABASE_URL and MIGRATOR_DATABASE_URL")
	}
	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("create isolated model database identity")
	}
	suffix := hex.EncodeToString(nonce[:])
	schema := "it_cmd_model_" + suffix
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
			t.Error("connect model test database cleanup")
			return
		}
		defer conn.Close(cleanupCtx)
		if _, err := conn.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error("drop isolated model test schema")
		}
		for _, role := range roles {
			if _, err := conn.Exec(cleanupCtx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("drop isolated model test role")
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate isolated model test database (%T)", err)
	}
	if err := store.MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("provision isolated model test roles (%T)", err)
	}
}

func freeModelAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve model gateway address")
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal("release model gateway address")
	}
	return addr
}

func waitForModelHealth(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/health/live", nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode != http.StatusOK {
					t.Fatalf("model liveness status=%d, want 200", response.StatusCode)
				}
				return
			}
		}
		select {
		case runErr := <-done:
			t.Fatalf("model gateway stopped before liveness: %v", runErr)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("model gateway did not serve liveness within 10 seconds")
}
