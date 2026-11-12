// File metadata (manifest mirror):
// Purpose: Exercises the `telemetry` package behavior through TestHTTPBoundaryValidatesIdentityAndContinuesTrace, TestHTTPBoundaryRecoversAndExportsSanitizedRED; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-11
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package telemetry

import (
	"bytes"
	"encoding/hex"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestHTTPBoundaryValidatesIdentityAndContinuesTrace(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const parentID = "00f067aa0ba902b7"
	for _, tc := range []struct {
		name, requestID, traceparent string
		valid                        bool
	}{
		{"valid", "request-123", "00-" + traceID + "-" + parentID + "-01", true},
		{"untrusted", "private/request", "00-00000000000000000000000000000000-" + parentID + "-01", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got Boundary
			handler := HTTPBoundary(nil, "edge", HTTPOptions{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var ok bool
				got, ok = BoundaryFromContext(r.Context())
				if !ok {
					t.Fatal("request reached handler without boundary identity")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "http://example.test/cases/42", nil)
			req.Header.Set("X-Request-Id", tc.requestID)
			req.Header.Set("Traceparent", tc.traceparent)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusNoContent || recorder.Header().Get("X-Request-Id") != got.RequestID || got.RequestID == "" {
				t.Fatalf("response lost request identity: status=%d response=%q context=%q", recorder.Code, recorder.Header().Get("X-Request-Id"), got.RequestID)
			}
			if tc.valid {
				if got.RequestID != tc.requestID || !got.RemoteParent || hex.EncodeToString(got.TraceID[:]) != traceID || hex.EncodeToString(got.ParentSpanID[:]) != parentID {
					t.Fatalf("valid parent was not continued: %+v", got)
				}
				if got.Traceparent() == tc.traceparent || !strings.HasPrefix(got.Traceparent(), "00-"+traceID+"-") {
					t.Fatalf("server did not issue a child span: %q", got.Traceparent())
				}
			} else if got.RequestID == tc.requestID || got.RemoteParent || hex.EncodeToString(got.TraceID[:]) == strings.Repeat("0", 32) {
				t.Fatalf("invalid client identity survived validation: %+v", got)
			}
		})
	}
}

func TestHTTPBoundaryRecoversAndExportsSanitizedRED(t *testing.T) {
	previousWriter, previousFlags := log.Writer(), log.Flags()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(previousWriter); log.SetFlags(previousFlags) })
	payloads := make(chan map[string]*metricpb.Metric, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/metrics" {
			t.Errorf("unexpected collector request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		payloads <- internalMetrics(decodeInternalMetricRequest(t, r.Body))
		writeInternalOTLPAck(w)
	}))
	defer collector.Close()
	exporter, err := New(Config{Endpoint: collector.URL, Service: "audit.edge", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cases/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("panic") == "1" {
			panic("SECRET-PANIC-CONTENT")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := HTTPBoundary(exporter, "edge", HTTPOptions{}, mux)
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"?token=SECRET-QUERY", http.StatusNoContent},
		{"?panic=1&token=SECRET-QUERY", http.StatusInternalServerError},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://example.test/cases/SECRET-CASE"+tc.query, strings.NewReader("SECRET-BODY"))
		req.Header.Set("Authorization", "Bearer SECRET-CREDENTIAL")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != tc.want || (tc.want == http.StatusInternalServerError && strings.Contains(recorder.Body.String(), "SECRET")) {
			t.Fatalf("unexpected response status/body: %d %q", recorder.Code, recorder.Body.String())
		}
	}
	exporter.Close()
	metrics := <-payloads
	if bytes.Contains([]byte(logs.String()), []byte("SECRET")) {
		t.Fatalf("request or panic content leaked into logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "http_panic") || !strings.Contains(logs.String(), "status=500") {
		t.Fatalf("recovered panic or error status missing from logs: %s", logs.String())
	}
	statuses := map[string]int64{}
	requests := metrics["audit.http.requests"].GetSum().GetDataPoints()
	for _, point := range requests {
		attrs := point.GetAttributes()
		if internalMetricStringAttr(attrs, "http.request.method") != "GET" ||
			internalMetricStringAttr(attrs, "http.route") != "/cases/{id}" {
			t.Fatalf("unexpected metric labels: %+v", attrs)
		}
		if len(attrs) != 3 {
			t.Fatalf("unexpected request counter labels: %+v", attrs)
		}
		statuses[internalMetricStringAttr(attrs, "http.response.status_code")] = point.GetAsInt()
	}
	duration := metrics["audit.http.duration_ms"].GetHistogram().GetDataPoints()
	if len(statuses) != 2 || statuses["204"] != 1 || statuses["500"] != 1 ||
		len(duration) != 1 || duration[0].GetCount() != 2 ||
		internalMetricStringAttr(duration[0].GetAttributes(), "http.route") != "/cases/{id}" {
		t.Fatalf("RED metrics did not distinguish success and panic: statuses=%v durations=%+v", statuses, duration)
	}
}
