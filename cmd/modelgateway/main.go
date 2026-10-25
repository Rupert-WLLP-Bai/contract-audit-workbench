// Command modelgateway runs the independently deployed model gateway.
//
// The process owns the model database identity (model call leases, usage, and
// quota). It is the only component that holds an upstream model credential:
// the Worker and the document service call it with their own private caller
// tokens (WORKER_MODEL_TOKEN / DOCUMENT_MODEL_TOKEN) and a delegated scope, and
// the gateway records and limits every model call before dispatch.
//
// Two explicit profiles exist, and neither silently degrades into the other:
//
//   - MODEL_GATEWAY_MODE=openai-compatible proxies LLM_BASE_URL.
//   - MODEL_GATEWAY_MODE=fixture answers deterministically from
//     MODEL_GATEWAY_FIXTURE_CHAT_CONTENT / MODEL_GATEWAY_FIXTURE_OCR_CONTENT
//     and is refused outside development and test.
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

	"contractaudit/internal/bootstrap"
	"contractaudit/internal/modelgateway"
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

	app, err := bootstrap.OpenService(ctx, "model")
	if err != nil {
		return err
	}
	defer app.Close()

	config, err := modelgateway.FromEnv()
	if err != nil {
		return err
	}
	service, err := modelgateway.New(app.Store, config)
	if err != nil {
		return err
	}

	addr := env("ADDR", "127.0.0.1:8084")
	log.Printf("model gateway listening on %s (mode %s, chat %s, ocr %s)", addr, config.Mode, config.ChatModel, config.OCRModel)
	server := &http.Server{
		Addr: addr, Handler: service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		// A completion may legitimately take as long as MODEL_REQUEST_TIMEOUT;
		// a global WriteTimeout would cut a valid call, and the request context
		// ends when the process is asked to stop.
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
