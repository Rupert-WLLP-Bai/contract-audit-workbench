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
	"log"
	"net"
	"net/http"
	"time"

	"contractaudit/internal/bootstrap"
	"contractaudit/internal/modelgateway"
	"contractaudit/internal/process"
)

// shutdownTimeout drains in-flight requests after SIGTERM. The deployment
// manifests stop this container with a 30s grace period, so the drain budget
// stays below it.
const shutdownTimeout = 25 * time.Second

func main() {
	process.Main(run)
}

func run() error {
	ctx, stop := process.SignalContext()
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

	addr := process.Env("ADDR", "127.0.0.1:8084")
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
	return process.Serve(ctx, server, shutdownTimeout)
}

// serve runs server until ctx is canceled or the listener fails, then drains
// in-flight requests within timeout. It mirrors the lifecycle of the other
// process entrypoints in this repository.
