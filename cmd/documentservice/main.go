// Command documentservice runs the independently deployed document service.
//
// The process owns the document database identity. It serves the public
// document operations the edge gateway routes to it, and it exposes the
// internal prepare endpoint the Worker calls:
//
//   - GATEWAY_ROLE_TOKEN authenticates the edge gateway on the public routes.
//   - WORKER_DOCUMENT_TOKEN authenticates the Worker on /internal/v1/prepare.
//
// Neither service credential replaces the session, audience, and case
// authorization the request handler performs for every user operation.
package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"contractaudit/internal/bootstrap"
	"contractaudit/internal/config"
	"contractaudit/internal/documentservice"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/process"
)

// shutdownTimeout drains in-flight requests after SIGTERM. The deployment
// manifests stop this container with a 45s grace period, so the drain budget
// stays below it.
const shutdownTimeout = 30 * time.Second

func main() {
	process.Main(run)
}

func run() error {
	ctx, stop := process.SignalContext()
	defer stop()

	app, err := bootstrap.OpenService(ctx, "document")
	if err != nil {
		return err
	}
	defer app.Close()
	if !app.Durable {
		return errors.New("the document service requires durable execution")
	}

	serviceConfig, err := documentservice.FromEnv()
	if err != nil {
		return err
	}
	service, err := documentservice.New(app.Store, serviceConfig)
	if err != nil {
		return err
	}

	// The service filter rejects any operation the document role does not own
	// and requires the edge credential, so a direct request to this container
	// fails the same way it would at the gateway. The edge forwards the
	// browser's Origin unchanged, so this hop needs the same allowlist as the
	// gateway: otherwise every browser write through the edge is refused as an
	// untrusted origin even though the edge accepted it.
	public, err := httpapi.ServiceHandler(app.Store, httpapi.Options{
		CORSOrigins: origins(), Models: app.Models, Durable: app.Durable, SecureCookies: app.SecureCookies,
	}, "document", strings.TrimSpace(os.Getenv("GATEWAY_ROLE_TOKEN")))
	if err != nil {
		return err
	}

	addr := process.Env("ADDR", "127.0.0.1:8082")
	log.Printf("document service listening on %s", addr)
	// Every readiness alias must report the same dependency set. The internal
	// handler answers /health/live and /health/ready itself, so the unified
	// surface is mounted in front of it: /health and /ready then check the
	// database and the object store exactly like /health/ready, and no alias
	// reports ready while the object store is unreachable.
	mux := http.NewServeMux()
	httpapi.MountHealth(mux, app.Store.DocumentReady)
	mux.Handle("/", service.Handler(public))
	server := &http.Server{
		Addr:    addr,
		Handler: app.Boundary("document", mux),
		// Upload and parse requests are streamed and bounded inside the handler
		// (MaxBytesReader plus the per-request parse deadline), so no global
		// ReadTimeout or WriteTimeout may cut a valid long request.
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	return process.Serve(ctx, server, shutdownTimeout)
}

// serve runs server until ctx is canceled or the listener fails, then drains
// in-flight requests within timeout. It mirrors the lifecycle of the other
// process entrypoints in this repository.
// origins applies the same browser origin policy as the core service.
func origins() []string {
	return config.BrowserOrigins(os.Getenv("CORS_ORIGIN"), os.Getenv("APP_ENV"))
}
