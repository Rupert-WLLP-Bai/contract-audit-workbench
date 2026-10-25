// Command gateway runs the edge reverse proxy.
//
// It holds no database identity and no business authority: it routes each
// operation to the one service that owns it, strips every internal header a
// caller could have forged, and presents that service's own credential. All
// user authorization still happens in the owning service.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"contractaudit/internal/gateway"
)

// shutdownTimeout drains in-flight requests after SIGTERM. The deployment
// manifests stop this container with a 20s grace period, so the drain budget
// stays below it.
const shutdownTimeout = 15 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	config, err := gateway.FromEnv()
	if err != nil {
		return err
	}
	edge, err := gateway.New(config)
	if err != nil {
		return err
	}
	if err := edge.Start(ctx); err != nil {
		edge.Close()
		return err
	}
	defer edge.Close()
	addr := env("ADDR", "127.0.0.1:8080")
	log.Printf("edge gateway listening on %s", addr)
	server := &http.Server{
		Addr: addr, Handler: edge.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		// No global ReadTimeout: an upload streams for as long as its own byte
		// and time bounds allow. No global WriteTimeout: a review event stream
		// stays open until the client or the upstream closes it. Both are
		// bounded per route inside the gateway.
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	return serve(ctx, server, shutdownTimeout)
}

// serve runs server until ctx is canceled or the listener fails, then drains
// in-flight requests within timeout. It mirrors the lifecycle of the other
// process entrypoints in this repository.
func serve(ctx context.Context, server *http.Server, timeout time.Duration) error {
	finished := make(chan error, 1)
	go func() { finished <- server.ListenAndServe() }()
	select {
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
