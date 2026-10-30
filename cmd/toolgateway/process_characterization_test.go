package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
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

func TestRunRejectsInvalidServiceCredentials(t *testing.T) {
	configureToolGatewayEnvironment(t)
	t.Setenv("DATABASE_SCHEMA", "")
	setupToolGatewayCommandDatabase(t)

	t.Run("short edge credential", func(t *testing.T) {
		t.Setenv("GATEWAY_ROLE_TOKEN", "short")
		if err := run(); err == nil || err.Error() != "GATEWAY_ROLE_TOKEN must hold the edge service credential (at least 32 bytes)" {
			t.Fatalf("run() error = %v, want edge credential error", err)
		}
	})
	t.Run("short worker credential", func(t *testing.T) {
		configureToolGatewayEnvironment(t)
		t.Setenv("WORKER_TOOL_TOKEN", "short")
		if err := run(); err == nil || err.Error() != "WORKER_TOOL_TOKEN must hold the worker service credential (at least 32 bytes)" {
			t.Fatalf("run() error = %v, want worker credential error", err)
		}
	})
	t.Run("worker and edge credentials differ", func(t *testing.T) {
		configureToolGatewayEnvironment(t)
		t.Setenv("WORKER_TOOL_TOKEN", strings.Repeat("e", 40))
		t.Setenv("GATEWAY_ROLE_TOKEN", strings.Repeat("e", 40))
		if err := run(); err == nil || err.Error() != "WORKER_TOOL_TOKEN must differ from GATEWAY_ROLE_TOKEN" {
			t.Fatalf("run() error = %v, want distinct credential error", err)
		}
	})
}

func TestRunServesLivenessCorsAndCallerResponsesThenStops(t *testing.T) {
	configureToolGatewayEnvironment(t)
	t.Setenv("DATABASE_SCHEMA", "")
	setupToolGatewayCommandDatabase(t)
	t.Setenv("CORS_ORIGIN", "https://portal.example")
	addr := freeToolGatewayAddress(t)
	t.Setenv("ADDR", addr)
	done := make(chan error, 1)
	go func() { done <- run() }()
	waitForToolGatewayHealth(t, addr, done)

	client := &http.Client{Timeout: time.Second}
	preflight, err := http.NewRequest(http.MethodOptions, "http://"+addr+"/mcp", nil)
	if err != nil {
		t.Fatal("build allowed MCP preflight")
	}
	preflight.Header.Set("Origin", "https://portal.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "Content-Type, MCP-Protocol-Version, Mcp-Method, Mcp-Name")
	preflightResponse, err := client.Do(preflight)
	if err != nil {
		t.Fatal("send allowed MCP preflight")
	}
	_ = preflightResponse.Body.Close()
	if preflightResponse.StatusCode != http.StatusNoContent || preflightResponse.Header.Get("Access-Control-Allow-Origin") != "https://portal.example" {
		t.Fatalf("allowed preflight status=%d allow-origin=%q", preflightResponse.StatusCode, preflightResponse.Header.Get("Access-Control-Allow-Origin"))
	}

	preflight, err = http.NewRequest(http.MethodOptions, "http://"+addr+"/mcp", nil)
	if err != nil {
		t.Fatal("build disallowed MCP preflight")
	}
	preflight.Header.Set("Origin", "https://attacker.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflightResponse, err = client.Do(preflight)
	if err != nil {
		t.Fatal("send disallowed MCP preflight")
	}
	_ = preflightResponse.Body.Close()
	if preflightResponse.StatusCode != http.StatusForbidden || preflightResponse.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed preflight status=%d allow-origin=%q", preflightResponse.StatusCode, preflightResponse.Header.Get("Access-Control-Allow-Origin"))
	}

	unauthorized := workerRequest(t, "", "")
	unauthorized.URL = mustToolGatewayURL(t, "http://"+addr+"/mcp")
	unauthorized.RequestURI = ""
	unauthorized.Header.Set("Origin", "https://portal.example")
	unauthorizedResponse, err := client.Do(unauthorized)
	if err != nil {
		t.Fatal("send unauthenticated MCP request")
	}
	unauthorizedBody := readToolGatewayBody(t, unauthorizedResponse)
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized || unauthorizedBody != "{\"error\":\"service authentication required\",\"code\":\"service_unauthenticated\"}" {
		t.Fatalf("unauthorized MCP response status=%d body=%s", unauthorizedResponse.StatusCode, unauthorizedBody)
	}
	if unauthorizedResponse.Header.Get("Cache-Control") != "no-store" || unauthorizedResponse.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("caller refusal headers changed: %#v", unauthorizedResponse.Header)
	}

	publicRequest := workerRequest(t, edgeCredential, "")
	publicRequest.URL = mustToolGatewayURL(t, "http://"+addr+"/mcp")
	publicRequest.RequestURI = ""
	publicRequest.Header.Set("Origin", "https://portal.example")
	publicResponse, err := client.Do(publicRequest)
	if err != nil {
		t.Fatal("send public MCP request with edge credential")
	}
	publicBody := readToolGatewayBody(t, publicResponse)
	if publicResponse.StatusCode != http.StatusUnauthorized || !strings.Contains(publicBody, "Authentication is required or no longer valid.") || publicResponse.Header.Get("Access-Control-Allow-Origin") != "https://portal.example" {
		t.Fatalf("public caller did not reach the session/MCP branch: status=%d body=%s allow-origin=%q", publicResponse.StatusCode, publicBody, publicResponse.Header.Get("Access-Control-Allow-Origin"))
	}

	worker := workerRequest(t, workerCredential, jobContext)
	worker.URL = mustToolGatewayURL(t, "http://"+addr+"/mcp")
	worker.RequestURI = ""
	workerResponse, err := client.Do(worker)
	if err != nil {
		t.Fatal("send worker MCP request")
	}
	workerBody := readToolGatewayBody(t, workerResponse)
	if workerResponse.StatusCode != http.StatusConflict || !strings.Contains(workerBody, "The current job scope is unavailable.") {
		t.Fatalf("unclaimed worker scope status=%d body=%s", workerResponse.StatusCode, workerBody)
	}

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
		t.Fatal("tool gateway did not drain after SIGINT")
	}
}

func configureToolGatewayEnvironment(t *testing.T) {
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
	t.Setenv("GATEWAY_ROLE_TOKEN", edgeCredential)
	t.Setenv("WORKER_TOOL_TOKEN", workerCredential)
	t.Setenv("CORS_ORIGIN", "")
	t.Setenv("ADDR", "")
}

func setupToolGatewayCommandDatabase(t *testing.T) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if baseDSN == "" || migratorDSN == "" {
		t.Fatal("tool gateway characterization requires TEST_DATABASE_URL and MIGRATOR_DATABASE_URL")
	}
	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("create isolated tool database identity")
	}
	suffix := hex.EncodeToString(nonce[:])
	schema := "it_cmd_tool_" + suffix
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
			t.Error("connect tool test database cleanup")
			return
		}
		defer conn.Close(cleanupCtx)
		if _, err := conn.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error("drop isolated tool test schema")
		}
		for _, role := range roles {
			if _, err := conn.Exec(cleanupCtx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("drop isolated tool test role")
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate isolated tool test database (%T)", err)
	}
	if err := store.MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("provision isolated tool test roles (%T)", err)
	}
}

func freeToolGatewayAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve tool gateway address")
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal("release tool gateway address")
	}
	return addr
}

func waitForToolGatewayHealth(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + addr + "/health/live")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("tool gateway liveness status=%d, want 200", response.StatusCode)
			}
			return
		}
		select {
		case runErr := <-done:
			t.Fatalf("tool gateway stopped before liveness: %v", runErr)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("tool gateway did not serve liveness within 10 seconds")
}

func mustToolGatewayURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal("parse tool gateway test URL")
	}
	return parsed
}

func readToolGatewayBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("read tool gateway response")
	}
	return string(body)
}
