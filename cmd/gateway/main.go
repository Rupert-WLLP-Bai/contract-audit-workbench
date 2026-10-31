// Command gateway runs the edge reverse proxy.
//
// It holds no database identity and no business authority: it routes each
// operation to the one service that owns it, strips every internal header a
// caller could have forged, and presents that service's own credential. All
// user authorization still happens in the owning service.
package main

import (
	"log"
	"net/http"
	"time"

	"contractaudit/internal/bootstrap"
	"contractaudit/internal/gateway"
	"contractaudit/internal/process"
	"contractaudit/internal/telemetry"
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
	exporter, err := bootstrap.HTTPExporter("gateway")
	if err != nil {
		return err
	}
	defer exporter.Close()
	addr := process.Env("ADDR", "127.0.0.1:8080")
	log.Printf("edge gateway listening on %s", addr)
	server := &http.Server{
		Addr: addr, Handler: telemetry.HTTPBoundary(exporter, "gateway", telemetry.HTTPOptions{ResolveRoute: gateway.RouteTemplate}, edge.Handler()),
		ReadHeaderTimeout: 5 * time.Second,
		// No global ReadTimeout: an upload streams for as long as its own byte
		// and time bounds allow. No global WriteTimeout: a review event stream
		// stays open until the client or the upstream closes it. Both are
		// bounded per route inside the gateway.
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	return process.Serve(ctx, server, shutdownTimeout)
}

// serve runs server until ctx is canceled or the listener fails, then drains
// in-flight requests within timeout. It mirrors the lifecycle of the other
// process entrypoints in this repository.
