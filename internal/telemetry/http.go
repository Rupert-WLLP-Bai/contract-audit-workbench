package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// The HTTP boundary is uniform across every process in this repository: one
// middleware layer that assigns a bounded request identity, parses and republishes
// W3C trace context, recovers handler panics, writes one structured access log
// line per exchange, and feeds the RED metric series (rate, errors, duration).
//
// The boundary never reads request or response bodies and never logs the URL
// query, credentials, contract content, or any business identifier: the route
// attribute is the matched route template, so the metric series stay bounded and
// every durable audit trail remains the Store's own business records.

const (
	requestIDHeader    = "X-Request-Id"
	traceparentHeader  = "Traceparent"
	maxRequestIDLength = 64
	maxRouteLength     = 128
	// httpFlushInterval bounds how long RED aggregates wait for a metrics-only
	// send when no execution event wakes the delivery loop first.
	httpFlushInterval = 15 * time.Second
	// maxPanicStackBytes bounds the recovered stack written for a handler panic.
	maxPanicStackBytes = 8 << 10
)

// HTTPOptions configures one boundary for one service.
type HTTPOptions struct {
	// ResolveRoute returns the route template recorded for the exchange. The
	// default reads the pattern http.ServeMux matched. A proxy that routes by
	// path shape supplies its own bounded template. The result is sanitized to
	// the metric attribute alphabet; an unusable result is recorded as
	// "unmatched".
	ResolveRoute func(r *http.Request) string
}

// boundaryKey carries the per-request boundary identities through the context.
type boundaryKey struct{}

// Boundary is the request identity and W3C trace context one exchange carried.
type Boundary struct {
	RequestID string
	TraceID   [16]byte
	SpanID    [8]byte
	// ParentSpanID is the span identity the caller propagated, zero when the
	// exchange started a new trace.
	ParentSpanID [8]byte
	// RemoteParent reports that a valid inbound traceparent was present.
	RemoteParent bool
}

// BoundaryFromContext returns the boundary context one request carried.
func BoundaryFromContext(ctx context.Context) (Boundary, bool) {
	if ctx == nil {
		return Boundary{}, false
	}
	boundary, ok := ctx.Value(boundaryKey{}).(Boundary)
	return boundary, ok
}

// Traceparent renders the W3C header for this context. A proxy reuses the
// server span identity so the next hop parents onto it directly.
func (b Boundary) Traceparent() string {
	return "00-" + hex.EncodeToString(b.TraceID[:]) + "-" + hex.EncodeToString(b.SpanID[:]) + "-01"
}

// TraceparentHeaderName is the canonical W3C trace context header.
const TraceparentHeaderName = traceparentHeader

// HTTPBoundary wraps next with the uniform HTTP boundary. A nil exporter keeps
// every behavior except metric export, so a deployment without a collector
// still logs access, bounds request IDs, propagates W3C context, and recovers.
func HTTPBoundary(exporter *Exporter, service string, opt HTTPOptions, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		boundary := boundaryFor(r)
		w.Header().Set(requestIDHeader, boundary.RequestID)
		served := r.WithContext(context.WithValue(r.Context(), boundaryKey{}, boundary))
		recorder := &responseRecorder{ResponseWriter: w}
		defer func() {
			recovered := recover()
			if recovered != nil && !isAbortPanic(recovered) {
				logPanic(service, boundary, routeFor(served, opt), recovered)
				if !recorder.wroteHeader {
					recorder.Header().Set("Content-Type", "application/json; charset=utf-8")
					recorder.Header().Set("Cache-Control", "no-store")
					recorder.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(recorder, `{"error":"internal_error","code":"internal_error"}`)
				}
			}
			method := httpMethodAttribute(r.Method)
			route := routeFor(served, opt)
			// A non-abort panic with nothing written already wrote the 500 above.
			// An abort panic means the connection ended without any response, so
			// the exchange is logged with status 0 and counted in no status
			// bucket instead of being recorded as a success.
			status := recorder.responseStatus()
			if recovered != nil && isAbortPanic(recovered) {
				status = 0
			}
			durationMS := time.Since(start).Milliseconds()
			exporter.observeHTTP(httpObservation{method: method, route: route, status: status, durationMS: durationMS})
			logAccess(service, boundary, method, route, status, durationMS, recorder.bytes)
			if recovered != nil && isAbortPanic(recovered) {
				panic(recovered)
			}
		}()
		next.ServeHTTP(recorder, served)
	})
}

// boundaryFor validates the inbound identities and starts the server span. The
// request ID is accepted only when it is short and made of safe printable
// characters; anything else is replaced by a generated identity so a forged or
// oversized value can never reach a log line, a response header, or a downstream
// service.
func boundaryFor(r *http.Request) Boundary {
	boundary := Boundary{RequestID: sanitizeRequestID(r.Header.Get(requestIDHeader))}
	if boundary.RequestID == "" {
		boundary.RequestID = generateRequestID()
	}
	if trace, ok := parseTraceparent(r.Header.Get(traceparentHeader)); ok {
		boundary.TraceID = trace.traceID
		boundary.ParentSpanID = trace.spanID
		boundary.RemoteParent = trace.recorded
	} else {
		_, _ = rand.Read(boundary.TraceID[:])
	}
	_, _ = rand.Read(boundary.SpanID[:])
	return boundary
}

func sanitizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxRequestIDLength {
		return ""
	}
	for i := range len(value) {
		c := rune(value[i])
		if !isASCIILetter(c) && !isASCIIDigit(c) && c != '-' && c != '_' && c != '.' {
			return ""
		}
	}
	return value
}

func generateRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Crypto failure is a process-wide emergency; the boundary still answers
		// with a stable fallback identity rather than panicking the exchange.
		return "req-fallback"
	}
	return hex.EncodeToString(buf[:])
}

// NewRequestID generates a bounded request identity for an outbound service
// call made outside any HTTP boundary, so the receiving boundary can correlate
// the hop in its access log.
func NewRequestID() string { return generateRequestID() }

type inboundTrace struct {
	traceID [16]byte
	spanID  [8]byte
	// recorded mirrors the W3C recorded flag: the caller intends the trace to
	// be exported.
	recorded bool
}

// parseTraceparent accepts exactly the W3C 00 version form. Later versions may
// extend the value, so anything else is treated as absent and a fresh trace
// starts rather than forwarding an unparsed identity.
func parseTraceparent(value string) (inboundTrace, bool) {
	parts := strings.Split(value, "-")
	if len(parts) != 4 || parts[0] != "00" {
		return inboundTrace{}, false
	}
	traceBytes, err := hex.DecodeString(parts[1])
	if err != nil || len(traceBytes) != 16 || allZero(traceBytes) {
		return inboundTrace{}, false
	}
	spanBytes, err := hex.DecodeString(parts[2])
	if err != nil || len(spanBytes) != 8 || allZero(spanBytes) {
		return inboundTrace{}, false
	}
	flagBytes, err := hex.DecodeString(parts[3])
	if err != nil || len(flagBytes) != 1 {
		return inboundTrace{}, false
	}
	var trace inboundTrace
	copy(trace.traceID[:], traceBytes)
	copy(trace.spanID[:], spanBytes)
	trace.recorded = flagBytes[0]&0x01 == 0x01
	return trace, true
}

func allZero(value []byte) bool {
	for _, b := range value {
		if b != 0 {
			return false
		}
	}
	return true
}

// AttemptTraceparent renders a W3C trace context for one outbound worker call.
// The trace identity is the same deterministic identity the execution event
// exporter derives from the attempt, so an attempt and its model, tool, and
// document calls land in one trace.
func AttemptTraceparent(attemptID string) string {
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		return ""
	}
	trace := traceSeed(attemptID)
	var span [8]byte
	if _, err := rand.Read(span[:]); err != nil {
		copy(span[:], trace[8:])
	}
	return "00-" + hex.EncodeToString(trace[:]) + "-" + hex.EncodeToString(span[:]) + "-01"
}

func traceSeed(attemptID string) [16]byte {
	sum := sha256.Sum256([]byte(attemptID))
	var trace [16]byte
	copy(trace[:], sum[:16])
	return trace
}

// routeFor resolves the recorded route template and keeps it within the metric
// attribute alphabet. A template is server-defined, so any rejected character
// means the resolver produced something unusable and the series must not grow.
func routeFor(r *http.Request, opt HTTPOptions) string {
	route := "unmatched"
	if opt.ResolveRoute != nil {
		route = opt.ResolveRoute(r)
	} else if r.Pattern != "" {
		route = patternRoute(r.Pattern)
	}
	route = sanitizeRoute(route)
	if route == "" {
		return "unmatched"
	}
	return route
}

// patternRoute drops the method prefix http.ServeMux adds to its patterns, so
// the recorded template stays method-independent.
func patternRoute(pattern string) string {
	if method, rest, found := strings.Cut(pattern, " "); found && isKnownMethod(method) {
		return rest
	}
	return pattern
}

func sanitizeRoute(route string) string {
	if route == "" || len(route) > maxRouteLength {
		return ""
	}
	for i := range len(route) {
		c := route[i]
		if c < 0x21 || c > 0x7e || c == '"' {
			return ""
		}
	}
	return route
}

func isKnownMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// httpMethodAttribute bounds the method attribute set: the request line method
// is client-controlled text, so an unknown token must not become a metric series.
func httpMethodAttribute(method string) string {
	if isKnownMethod(method) {
		return method
	}
	return "OTHER"
}

func isAbortPanic(recovered any) bool {
	err, ok := recovered.(error)
	return ok && errors.Is(err, http.ErrAbortHandler)
}

// logPanic records a recovered handler panic without the panic value: a panic
// message can quote request or contract content, so only its type and the
// bounded stack of function names are written.
func logPanic(service string, boundary Boundary, route string, recovered any) {
	stack := debug.Stack()
	if len(stack) > maxPanicStackBytes {
		stack = stack[:maxPanicStackBytes]
	}
	log.Printf("http_panic service=%s route=%s request_id=%s trace_id=%s panic_type=%T stack=%q",
		service, route, boundary.RequestID, hex.EncodeToString(boundary.TraceID[:]), recovered, stack)
}

// logAccess writes the one structured line every HTTP exchange produces. The
// fields are bounded identities and counts only: no path, no query, no
// credential, no user agent, no body size beyond the response byte count.
func logAccess(service string, boundary Boundary, method, route string, status int, durationMS int64, bytes int64) {
	log.Printf("http_request service=%s method=%s route=%s status=%d duration_ms=%d request_id=%s trace_id=%s bytes=%d",
		service, method, route, status, durationMS, boundary.RequestID, hex.EncodeToString(boundary.TraceID[:]), bytes)
}

// responseRecorder captures the exchange outcome for RED and the access log
// while preserving streaming behavior: Flush forwards, and Unwrap keeps
// http.ResponseController deadlines and per-route controller features working.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

// Flush forwards an explicit stream flush. A first flush before any header
// means the handler started a stream, which commits a 200 response.
func (w *responseRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		if !w.wroteHeader {
			w.status = http.StatusOK
			w.wroteHeader = true
		}
		flusher.Flush()
	}
}

// FlushError forwards a stream flush with its failure. http.ResponseController
// prefers FlushError over Flush when both exist, so an SSE handler keeps
// observing the underlying write failure instead of a swallowed nil.
func (w *responseRecorder) FlushError() error {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap exposes the underlying writer so http.ResponseController reaches the
// real deadlines, flushes, and hijack support of the server connection.
func (w *responseRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseRecorder) responseStatus() int {
	if !w.wroteHeader || w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
