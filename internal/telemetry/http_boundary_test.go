package telemetry

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

type httpMetricPoint struct {
	Attributes []struct {
		Key   string `json:"key"`
		Value struct {
			String string `json:"stringValue"`
		} `json:"value"`
	} `json:"attributes"`
	Value string `json:"asInt"`
	Count string `json:"count"`
}

func TestHTTPBoundaryRecoversAndExportsSanitizedRED(t *testing.T) {
	previousWriter, previousFlags := log.Writer(), log.Flags()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(previousWriter); log.SetFlags(previousFlags) })
	payloads := make(chan []byte, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/metrics" {
			t.Errorf("unexpected collector request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		payloads <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer collector.Close()
	exporter, err := New(Config{Endpoint: collector.URL, Service: "audit.edge", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer exporter.Close()
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
	var payload []byte
	select {
	case payload = <-payloads:
	default:
		t.Fatal("HTTP-only exporter did not flush metrics on shutdown")
	}
	for _, secret := range []string{"SECRET-CASE", "SECRET-QUERY", "SECRET-BODY", "SECRET-CREDENTIAL", "SECRET-PANIC-CONTENT"} {
		if strings.Contains(logs.String(), secret) || bytes.Contains(payload, []byte(secret)) {
			t.Fatalf("request or panic content leaked into telemetry: %s", secret)
		}
	}
	if !strings.Contains(logs.String(), "http_panic") || !strings.Contains(logs.String(), "status=500") {
		t.Fatalf("recovered panic or error status missing from logs: %s", logs.String())
	}
	var decoded struct {
		Resources []struct {
			Scopes []struct {
				Metrics []struct {
					Name string `json:"name"`
					Sum  *struct {
						Points []httpMetricPoint `json:"dataPoints"`
					} `json:"sum"`
					Histogram *struct {
						Points []httpMetricPoint `json:"dataPoints"`
					} `json:"histogram"`
				} `json:"metrics"`
			} `json:"scopeMetrics"`
		} `json:"resourceMetrics"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	durations := 0
	for _, resource := range decoded.Resources {
		for _, scope := range resource.Scopes {
			for _, metric := range scope.Metrics {
				var points []httpMetricPoint
				switch metric.Name {
				case "audit.http.requests":
					if metric.Sum == nil {
						t.Fatal("HTTP request metric has no counter")
					}
					points = metric.Sum.Points
				case "audit.http.duration_ms":
					if metric.Histogram == nil {
						t.Fatal("HTTP duration metric has no histogram")
					}
					points = metric.Histogram.Points
				default:
					continue
				}
				for _, point := range points {
					attrs := map[string]string{}
					for _, attr := range point.Attributes {
						attrs[attr.Key] = attr.Value.String
					}
					if attrs["http.request.method"] != "GET" || attrs["http.route"] != "/cases/{id}" {
						t.Fatalf("unexpected metric labels: %+v", attrs)
					}
					if metric.Name == "audit.http.requests" {
						if len(attrs) != 3 {
							t.Fatalf("unexpected counter labels: %+v", attrs)
						}
						statuses[attrs["http.response.status_code"]] = point.Value
					} else {
						if len(attrs) != 2 || point.Count != "2" {
							t.Fatalf("unexpected duration labels: %+v", attrs)
						}
						durations++
					}
				}
			}
		}
	}
	if len(statuses) != 2 || statuses["204"] != "1" || statuses["500"] != "1" || durations != 1 {
		t.Fatalf("RED metrics did not distinguish success and panic: statuses=%v durations=%d", statuses, durations)
	}
}
