package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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

func TestOriginsPreservesDefaultsAndConfiguredValues(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CORS_ORIGIN", " \t")
	if got := origins(); !equalExecutionOrigins(got, []string{"http://localhost:5173", "http://127.0.0.1:5173"}) {
		t.Fatalf("origins() with blank configuration = %#v", got)
	}
	t.Setenv("CORS_ORIGIN", " https://east.example , ,https://west.example  ")
	want := []string{"http://localhost:5173", "http://127.0.0.1:5173", "https://east.example", "https://west.example"}
	if got := origins(); !equalExecutionOrigins(got, want) {
		t.Fatalf("origins() = %#v, want %#v", got, want)
	}
}

func TestWaitRelayWaitsForBackgroundPublisher(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		done <- nil
	}()
	started := time.Now()
	if err := waitRelay(done, time.Second); err != nil {
		t.Fatalf("completed relay reported an error: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("waitRelay did not wait for the relay to complete: %s", elapsed)
	}
}

func TestWaitRelayTreatsCancellationAsNormalShutdown(t *testing.T) {
	done := make(chan error, 1)
	done <- context.Canceled
	if err := waitRelay(done, time.Second); err != nil {
		t.Fatalf("context cancellation surfaced as a shutdown failure: %v", err)
	}
}

func TestWaitRelaySurfacesUnexpectedFailure(t *testing.T) {
	done := make(chan error, 1)
	done <- errors.New("broker configuration rejected")
	err := waitRelay(done, time.Second)
	if err == nil || !strings.Contains(err.Error(), "outbox relay") {
		t.Fatalf("unexpected relay failure was swallowed: %v", err)
	}
}

func TestWaitRelayBoundsAnUnresponsiveRelay(t *testing.T) {
	done := make(chan error, 1)
	started := time.Now()
	err := waitRelay(done, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "shutdown timeout") {
		t.Fatalf("unresponsive relay was not bounded: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("relay timeout waited too long: %s", elapsed)
	}
}

func TestWaitRelayTreatsNilAndDeadlineAsNormalShutdown(t *testing.T) {
	for _, shutdownErr := range []error{nil, context.DeadlineExceeded} {
		done := make(chan error, 1)
		done <- shutdownErr
		if err := waitRelay(done, time.Second); err != nil {
			t.Fatalf("waitRelay(%v) = %v, want nil", shutdownErr, err)
		}
	}
}

func TestRunPreservesStartupAndShutdownBehavior(t *testing.T) {
	configureExecutionCommandEnvironment(t)
	t.Setenv("DATABASE_SCHEMA", "")
	setupExecutionCommandDatabase(t)

	t.Run("configured role mismatch", func(t *testing.T) {
		t.Setenv("SERVICE_ROLE", "document")
		if err := run(); err == nil || err.Error() != "configured service identity does not match executable" {
			t.Fatalf("run() error = %v, want configured role mismatch", err)
		}
	})

	t.Run("private gateway credential required", func(t *testing.T) {
		configureExecutionCommandEnvironment(t)
		t.Setenv("GATEWAY_ROLE_TOKEN", "short")
		if err := run(); err == nil || err.Error() != "a private service credential of at least 32 bytes is required" {
			t.Fatalf("run() error = %v, want service credential error", err)
		}
	})

	t.Run("invalid outbox setting is reported", func(t *testing.T) {
		configureExecutionCommandEnvironment(t)
		t.Setenv("OUTBOX_RELAY_BATCH", "0")
		if err := run(); err == nil || err.Error() != "OUTBOX_RELAY_BATCH must be an integer in 1..512" {
			t.Fatalf("run() error = %v, want bounded relay batch error", err)
		}
	})

	t.Run("listener failure is returned", func(t *testing.T) {
		configureExecutionCommandEnvironment(t)
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

	t.Run("liveness is served and SIGINT drains the service", func(t *testing.T) {
		configureExecutionCommandEnvironment(t)
		addr := freeExecutionAddress(t)
		t.Setenv("ADDR", addr)
		done := make(chan error, 1)
		go func() { done <- run() }()
		waitForExecutionHealth(t, addr, done)
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
			t.Fatal("execution service did not drain after SIGINT")
		}
	})
}

func configureExecutionCommandEnvironment(t *testing.T) {
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
	t.Setenv("GATEWAY_ROLE_TOKEN", strings.Repeat("g", 40))
	t.Setenv("CORS_ORIGIN", "")
	t.Setenv("NATS_URL", "")
	t.Setenv("OUTBOX_RELAY_BATCH", "")
}

func setupExecutionCommandDatabase(t *testing.T) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if baseDSN == "" || migratorDSN == "" {
		t.Fatal("execution command characterization requires TEST_DATABASE_URL and MIGRATOR_DATABASE_URL")
	}
	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("create isolated execution database identity")
	}
	suffix := hex.EncodeToString(nonce[:])
	schema := "it_cmd_execution_" + suffix
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
			t.Error("connect execution test database cleanup")
			return
		}
		defer conn.Close(cleanupCtx)
		if _, err := conn.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error("drop isolated execution test schema")
		}
		for _, role := range roles {
			if _, err := conn.Exec(cleanupCtx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("drop isolated execution test role")
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate isolated execution test database (%T)", err)
	}
	if err := store.MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("provision isolated execution test roles (%T)", err)
	}
}

func freeExecutionAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve execution service address")
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal("release execution service address")
	}
	return addr
}

func waitForExecutionHealth(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + addr + "/health/live")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("execution liveness status=%d, want 200", response.StatusCode)
			}
			return
		}
		select {
		case runErr := <-done:
			t.Fatalf("execution service stopped before liveness: %v", runErr)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("execution service did not serve liveness within 10 seconds")
}

func equalExecutionOrigins(got, want []string) bool {
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
