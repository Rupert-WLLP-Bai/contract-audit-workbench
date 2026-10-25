package httpapi

import (
	"context"
	"net/http"
	"time"
)

// HealthPaths is the complete set of probe paths this repository serves. Every
// binary mounts exactly this surface through MountHealth, and the deployment
// manifest conformance check asserts that each declared probe path belongs to
// this set. That keeps a manifest from pointing at a path no binary answers,
// which is how the core-api / tool-gateway / audit-worker probe mismatches arose.
var HealthPaths = []string{"/health", "/health/live", "/ready", "/health/ready"}

// readinessTimeout bounds the dependency check a readiness probe performs. A
// probe must stay cheap and local: it never calls a paid model, an OCR vendor,
// or any other billed external service.
const readinessTimeout = 2 * time.Second

// MountHealth registers the unified health surface on mux.
//
// Liveness (/health, /health/live) reports only that this process is running and
// serving HTTP. It deliberately checks no dependency, because a database or
// object-store outage must not make the orchestrator restart an otherwise
// healthy process.
//
// Readiness (/ready, /health/ready) reports whether this replica can serve its
// own traffic right now. ready is the service's own bounded check; a nil ready
// means the service has no readiness dependency beyond process liveness.
//
// Idle is not unhealthy: readiness never inspects queue depth or worker
// activity, so an idle worker that can still accept new work reports ready.
// Probe responses carry no error detail, so a probe never leaks a DSN, a
// credential, or a vendor error message.
func MountHealth(mux *http.ServeMux, ready func(context.Context) error) {
	live := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	readiness := func(w http.ResponseWriter, r *http.Request) {
		if ready == nil {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()
		if err := ready(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	mux.HandleFunc("GET /health", live)
	mux.HandleFunc("GET /health/live", live)
	mux.HandleFunc("GET /ready", readiness)
	mux.HandleFunc("GET /health/ready", readiness)
}
