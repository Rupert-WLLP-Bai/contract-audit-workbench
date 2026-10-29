package process

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestEnvTrimsAndFallsBack(t *testing.T) {
	t.Setenv("PROCESS_TEST_VALUE", "  configured  ")
	if got := Env("PROCESS_TEST_VALUE", "fallback"); got != "configured" {
		t.Fatalf("Env() = %q, want %q", got, "configured")
	}
	t.Setenv("PROCESS_TEST_VALUE", "   ")
	if got := Env("PROCESS_TEST_VALUE", "fallback"); got != "fallback" {
		t.Fatalf("Env() = %q, want fallback", got)
	}
}

func TestServeDrainsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.NewServeMux()}
	finished := make(chan error, 1)
	go func() { finished <- Serve(ctx, server, time.Second) }()
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Serve() = %v, want nil after cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancellation")
	}
}

func TestServeReportsListenerFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	server := &http.Server{Addr: occupied.Addr().String()}
	if err := Serve(context.Background(), server, time.Second); err == nil {
		t.Fatal("Serve() = nil, want a listener failure")
	}
}
