// Command auditexecution runs the independently deployed execution service.
//
// The process owns the execution database identity and accepts review runs,
// retries, cancellations, and finding decisions. In durable mode it records a
// persistent task instead of running the review inline, so an elected Worker
// performs the attempt; GATEWAY_ROLE_TOKEN authenticates the edge gateway.
// The service never calls a model: only the Worker and the document service
// hold a model gateway credential.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"contractaudit/internal/bootstrap"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/outboxrelay"
)

// shutdownTimeout drains in-flight requests after SIGTERM. The deployment
// manifests stop this container with a 30s grace period, so the drain budget
// stays below it.
const shutdownTimeout = 25 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := bootstrap.OpenService(ctx, "execution")
	if err != nil {
		return err
	}
	defer app.Close()
	if !app.Durable {
		return errors.New("the execution service requires durable execution")
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).With(
		"service", "audit-execution", "service.instance.id", bootstrap.InstanceID(),
	))

	// The service filter rejects any operation the execution role does not own,
	// requires the edge credential, and mounts the unified probe surface, so a
	// direct request to this container fails the same way it would at the
	// gateway. The edge forwards the browser's Origin unchanged, so this hop
	// needs the same allowlist as the gateway or every browser write to a
	// review route is refused as an untrusted origin.
	service, err := httpapi.ServiceHandler(app.Store, httpapi.Options{
		CORSOrigins: origins(), Models: app.Models, Durable: app.Durable, SecureCookies: app.SecureCookies,
	}, "execution", strings.TrimSpace(os.Getenv("GATEWAY_ROLE_TOKEN")))
	if err != nil {
		return err
	}

	// Publish committed outbox rows to JetStream and mark them delivered only
	// after the broker stores them. Workers can still find jobs in PostgreSQL
	// when NATS_URL is unset.
	relayConfig, err := outboxrelay.FromEnv()
	if err != nil {
		return err
	}
	relayCtx, stopRelay := context.WithCancel(ctx)
	defer stopRelay()
	var relayDone <-chan error
	if relayConfig.Enabled() {
		// Serve keeps retrying the broker with bounded backoff, so a broker that
		// starts after this service still drains the pending backlog. The relay
		// gets its own cancel function so an early listener failure also stops
		// it, and shutdown waits for it before the deferred Store.Close runs.
		done := make(chan error, 1)
		relayDone = done
		go func() {
			dial := func(dialCtx context.Context, cfg outboxrelay.Config) (outboxrelay.Broker, error) {
				return outboxrelay.Dial(dialCtx, cfg)
			}
			done <- outboxrelay.Serve(relayCtx, app.Store, relayConfig, dial)
		}()
	} else {
		log.Printf("outbox relay disabled: NATS_URL is not configured; PostgreSQL scanning remains the recovery path")
	}

	addr := env("ADDR", "127.0.0.1:8083")
	slog.Info("execution service listening", "addr", addr)
	server := &http.Server{
		Addr: addr, Handler: service,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		// Review events stream over SSE; a global WriteTimeout would cut a valid
		// stream, and the request context ends when the process is asked to stop.
	}
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	serveErr := serve(ctx, server, shutdownTimeout)
	stopRelay()
	return errors.Join(serveErr, waitRelay(relayDone, shutdownTimeout))
}

// waitRelay bounds how long shutdown waits for the background publisher. The
// relay's normal cancellation is not an error; an unexpected non-context error
// or an unresponsive relay is surfaced after the HTTP server has drained.
func waitRelay(done <-chan error, timeout time.Duration) error {
	if done == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("outbox relay: %w", err)
	case <-timer.C:
		return errors.New("outbox relay did not stop within the shutdown timeout")
	}
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

// origins reads the extra browser origins the deployment allows, using the same
// convention as the core service: an empty CORS_ORIGIN keeps the handler's
// development defaults. The browser sends its Origin through the edge, so this
// hop must accept the same site origins or it would refuse writes the edge
// already authorized.
func origins() []string {
	extra := strings.TrimSpace(os.Getenv("CORS_ORIGIN"))
	if extra == "" {
		return nil
	}
	result := []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	for _, origin := range strings.Split(extra, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			result = append(result, origin)
		}
	}
	return result
}
