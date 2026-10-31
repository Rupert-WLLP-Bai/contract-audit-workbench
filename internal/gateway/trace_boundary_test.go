package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/telemetry"
)

func TestGatewayContinuesValidatedTraceAcrossServiceBoundary(t *testing.T) {
	type observed struct {
		boundary telemetry.Boundary
		header   http.Header
	}
	upstreamRequests := make(chan observed, 2)
	upstream := httptest.NewServer(telemetry.HTTPBoundary(nil, "core", telemetry.HTTPOptions{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		boundary, ok := telemetry.BoundaryFromContext(r.Context())
		if !ok {
			t.Error("core request has no telemetry boundary")
		}
		upstreamRequests <- observed{boundary: boundary, header: r.Header.Clone()}
		w.WriteHeader(http.StatusNoContent)
	})))
	defer upstream.Close()

	config := baseConfig()
	config.CoreURLs = []string{upstream.URL}
	config.DocumentURL = upstream.URL
	config.ExecutionURL = upstream.URL
	config.ToolURLs = []string{upstream.URL}
	config.HealthInterval = minHealthInterval
	config.HealthTimeout = minHealthTimeout
	edge, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := edge.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer edge.Close()
	edgeRequests := make(chan telemetry.Boundary, 2)
	proxy := edge.Handler()
	server := httptest.NewServer(telemetry.HTTPBoundary(nil, "edge", telemetry.HTTPOptions{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/cases" {
			boundary, ok := telemetry.BoundaryFromContext(r.Context())
			if !ok {
				t.Error("edge request has no telemetry boundary")
			}
			edgeRequests <- boundary
		}
		proxy.ServeHTTP(w, r)
	})))
	defer server.Close()
	waitGatewayReady(t, server)

	for _, tc := range []struct {
		name, id, trace string
		valid           bool
	}{
		{"valid", "trace-request-42", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", true},
		{"untrusted", "forged/request", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, server.URL+"/api/cases", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Request-Id", tc.id)
			req.Header.Set("Traceparent", tc.trace)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("proxy returned %d", response.StatusCode)
			}
			var edgeBoundary telemetry.Boundary
			var got observed
			select {
			case edgeBoundary = <-edgeRequests:
			case <-time.After(3 * time.Second):
				t.Fatal("missing edge observation")
			}
			select {
			case got = <-upstreamRequests:
			case <-time.After(3 * time.Second):
				t.Fatal("missing core observation")
			}
			if got.boundary.RequestID != edgeBoundary.RequestID || got.header.Get("X-Request-Id") != edgeBoundary.RequestID ||
				got.boundary.TraceID != edgeBoundary.TraceID || got.boundary.ParentSpanID != edgeBoundary.SpanID ||
				got.header.Get("Traceparent") != edgeBoundary.Traceparent() {
				t.Fatalf("proxy did not continue the edge identity: edge=%+v core=%+v", edgeBoundary, got.boundary)
			}
			ids := response.Header.Values("X-Request-Id")
			if len(ids) != 1 || ids[0] != edgeBoundary.RequestID {
				t.Fatalf("client received duplicate or inconsistent request identities: %v", ids)
			}
			if tc.valid {
				if edgeBoundary.RequestID != tc.id || !edgeBoundary.RemoteParent || !strings.HasPrefix(edgeBoundary.Traceparent(), "00-4bf92f3577b34da6a3ce929d0e0e4736-") {
					t.Fatalf("valid inbound identity was not continued: %+v", edgeBoundary)
				}
			} else if edgeBoundary.RequestID == tc.id || edgeBoundary.RemoteParent {
				t.Fatalf("untrusted inbound identity was propagated: %+v", edgeBoundary)
			}
		})
	}
}
