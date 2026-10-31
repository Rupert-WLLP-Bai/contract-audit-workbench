// Package process holds the lifecycle wiring shared by the service
// entrypoints: signal-scoped contexts, graceful HTTP shutdown and the
// environment fallback rule.
package process

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
)

// Main runs fn and exits non-zero when it returns an error.
func Main(fn func() error) {
	if err := fn(); err != nil {
		log.Fatal(err)
	}
}

// SignalContext is canceled on SIGINT or SIGTERM.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Serve runs server until ctx is canceled or the listener fails, then stops
// accepting new requests and drains in-flight handlers within timeout.
//
// Serve owns the request lifetimes: it replaces server.BaseContext with a
// context that stays valid for the whole drain window, so a signal alone never
// cancels an in-flight request. Serve cancels that context when the drain
// completes (release) or when the window expires (force), then closes the
// connections the expired window left behind.
func Serve(ctx context.Context, server *http.Server, timeout time.Duration) error {
	base, release := context.WithCancel(context.Background())
	defer release()
	server.BaseContext = func(net.Listener) context.Context { return base }
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
			release()
			_ = server.Close()
			return err
		}
		if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// Env returns the trimmed environment value, or fallback when it is empty.
func Env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
