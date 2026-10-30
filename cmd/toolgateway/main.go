// Command toolgateway runs the independently deployed, read-only tool gateway.
//
// The process owns the tool database identity. It serves the MCP profile at
// /mcp, which the edge gateway reaches with GATEWAY_ROLE_TOKEN, and it keeps the
// ordinary session, audience, and case authorization for every tool call.
// Probes are unauthenticated; the MCP route is not.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"contractaudit/internal/bootstrap"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/process"
	"contractaudit/internal/toolgateway"
)

// shutdownTimeout drains in-flight requests after SIGTERM. The deployment
// manifests stop this container with a 20s grace period, so the drain budget
// stays below it.
const shutdownTimeout = 15 * time.Second

func main() {
	process.Main(run)
}

func run() error {
	ctx, stop := process.SignalContext()
	defer stop()

	app, err := bootstrap.OpenService(ctx, "tool")
	if err != nil {
		return err
	}
	defer app.Close()

	credential := strings.TrimSpace(os.Getenv("GATEWAY_ROLE_TOKEN"))
	if len(credential) < 32 {
		return errors.New("GATEWAY_ROLE_TOKEN must hold the edge service credential (at least 32 bytes)")
	}
	workerCredential := strings.TrimSpace(os.Getenv("WORKER_TOOL_TOKEN"))
	if len(workerCredential) < 32 {
		return errors.New("WORKER_TOOL_TOKEN must hold the worker service credential (at least 32 bytes)")
	}
	if workerCredential == credential {
		return errors.New("WORKER_TOOL_TOKEN must differ from GATEWAY_ROLE_TOKEN")
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler(app.Store, configuredOrigins(), credential, workerCredential))
	httpapi.MountHealth(mux, func(ctx context.Context) error { return app.Store.Ready(ctx) })

	addr := process.Env("ADDR", "127.0.0.1:8090")
	host, _ := os.Hostname()
	log.Printf("tool gateway instance %s pid %d listening on %s", host, os.Getpid(), addr)
	server := &http.Server{
		Addr: addr, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// A tool call carries its own deadline (60s); the write timeout only has
		// to stay above it.
		WriteTimeout:   70 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	return process.Serve(ctx, server, shutdownTimeout)
}

// mcpHandler assembles the MCP endpoint: caller authentication first, then the
// MCP profile handler with its worker claim branch.
func mcpHandler(backend toolgateway.Backend, allowedOrigins []string, edgeToken, workerToken string) http.Handler {
	return requireCaller(edgeToken, workerToken, toolgateway.NewHandler(backend, toolgateway.Options{
		AllowedOrigins:   allowedOrigins,
		WorkerCredential: workerToken,
	}))
}

// requireCaller authenticates the two trusted caller branches before the MCP
// handler runs. A durable worker must present exactly one job context and the
// worker credential; the public path must present the edge credential and no job
// context at all. The edge strips every X-Contract-* header from external
// traffic, so a browser can reach neither branch, and a worker credential can
// never open the public/session paths because those carry no job context.
func requireCaller(edgeToken, workerToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			// CORS preflight carries no custom headers; the MCP handler still
			// enforces its origin allowlist for it.
			next.ServeHTTP(w, r)
			return
		}
		contexts := r.Header.Values("X-Contract-Job-Context")
		if len(contexts) > 1 || (len(contexts) == 1 && contexts[0] == "") {
			writeUnauthorized(w)
			return
		}
		expected := edgeToken
		if len(contexts) == 1 {
			expected = workerToken
		}
		values := r.Header.Values("X-Contract-Service-Key")
		if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(expected)) != 1 {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"service authentication required","code":"service_unauthenticated"}`))
}

func configuredOrigins() []string {
	configured := strings.TrimSpace(os.Getenv("CORS_ORIGIN"))
	if configured == "" {
		return nil
	}
	origins := []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	for _, origin := range strings.Split(configured, ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

// serve runs server until ctx is canceled or the listener fails, then drains
// in-flight requests within timeout. It mirrors the lifecycle of the other
// process entrypoints in this repository.
