package main

import (
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestMainServesHealthAndStopsOnInterrupt(t *testing.T) {
	for key, value := range map[string]string{
		"KUBERNETES_SERVICE_HOST":         "",
		"KUBERNETES_SERVICE_PORT":         "",
		"SYSTEM_HEALTH_PROJECT":           "cmd-health-test",
		"SYSTEM_HEALTH_EXPECTED_REPLICAS": "core-api=1,audit-worker=1",
		"DOCKER_SOCKET":                   "",
		"SYSTEM_HEALTH_DATABASE_URL":      "",
		"LLM_MODEL":                       "",
		"LLM_BASE_URL":                    "",
		"OCR_MODEL":                       "",
		"MINERU_URL":                      "",
	} {
		t.Setenv(key, value)
	}
	listener, err := net.Listen("tcp", ":8092")
	if err != nil {
		t.Fatal("health monitor port 8092 is unavailable for startup characterization")
	}
	if err := listener.Close(); err != nil {
		t.Fatal("release health monitor port")
	}

	done := make(chan struct{})
	go func() {
		main()
		close(done)
	}()
	waitForHealthMonitor(t, done)
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal("find process for interrupt signal")
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		t.Fatal("send process interrupt")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("health monitor did not shut down after SIGINT")
	}
}

func waitForHealthMonitor(t *testing.T, done <-chan struct{}) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://127.0.0.1:8092/health")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("health monitor liveness status=%d, want 200", response.StatusCode)
			}
			return
		}
		select {
		case <-done:
			t.Fatal("health monitor exited before serving liveness")
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("health monitor did not serve liveness within 5 seconds")
}
