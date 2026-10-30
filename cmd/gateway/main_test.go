package main

import (
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunPreservesGatewayConfigurationAndLifecycle(t *testing.T) {
	configureGatewayEnvironment(t)

	t.Run("invalid limit is reported", func(t *testing.T) {
		configureGatewayEnvironment(t)
		t.Setenv("GATEWAY_MAX_INFLIGHT", "0")
		if err := run(); err == nil || err.Error() != "GATEWAY_MAX_INFLIGHT must be a positive integer" {
			t.Fatalf("run() error = %v, want bounded inflight error", err)
		}
	})

	t.Run("missing owner is refused", func(t *testing.T) {
		configureGatewayEnvironment(t)
		t.Setenv("CORE_URLS", "")
		if err := run(); err == nil || err.Error() != "no core upstream URL is configured" {
			t.Fatalf("run() error = %v, want missing core upstream error", err)
		}
	})

	t.Run("duplicate service credentials are refused", func(t *testing.T) {
		configureGatewayEnvironment(t)
		t.Setenv("GATEWAY_DOCUMENT_TOKEN", strings.Repeat("c", 40))
		if err := run(); err == nil || err.Error() != "GATEWAY_DOCUMENT_TOKEN must differ from GATEWAY_CORE_TOKEN" {
			t.Fatalf("run() error = %v, want duplicate service credential error", err)
		}
	})

	t.Run("listener failure is returned", func(t *testing.T) {
		configureGatewayEnvironment(t)
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

	t.Run("liveness is served and SIGINT drains the edge", func(t *testing.T) {
		configureGatewayEnvironment(t)
		addr := freeGatewayAddress(t)
		t.Setenv("ADDR", addr)
		done := make(chan error, 1)
		go func() { done <- run() }()
		waitForGatewayHealth(t, addr, done)
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
			t.Fatal("gateway did not drain after SIGINT")
		}
	})
}

func configureGatewayEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("CORE_URLS", "http://127.0.0.1:1")
	t.Setenv("DOCUMENT_URL", "http://127.0.0.1:2")
	t.Setenv("EXECUTION_URL", "http://127.0.0.1:3")
	t.Setenv("TOOL_URLS", "http://127.0.0.1:4")
	t.Setenv("GATEWAY_CORE_TOKEN", strings.Repeat("c", 40))
	t.Setenv("GATEWAY_DOCUMENT_TOKEN", strings.Repeat("d", 40))
	t.Setenv("GATEWAY_EXECUTION_TOKEN", strings.Repeat("e", 40))
	t.Setenv("GATEWAY_TOOL_TOKEN", strings.Repeat("t", 40))
	t.Setenv("GATEWAY_HEALTH_INTERVAL", "100ms")
	t.Setenv("GATEWAY_HEALTH_TIMEOUT", "50ms")
	t.Setenv("GATEWAY_HEALTH_FAILURE_THRESHOLD", "1")
	t.Setenv("GATEWAY_HEALTH_RECOVERY_THRESHOLD", "1")
	t.Setenv("GATEWAY_MAX_INFLIGHT", "")
	t.Setenv("ADDR", "")
}

func freeGatewayAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve gateway address")
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal("release gateway address")
	}
	return addr
}

func waitForGatewayHealth(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + addr + "/health/live")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("gateway liveness status=%d, want 200", response.StatusCode)
			}
			return
		}
		select {
		case runErr := <-done:
			t.Fatalf("gateway stopped before liveness: %v", runErr)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("gateway did not serve liveness within 10 seconds")
}
