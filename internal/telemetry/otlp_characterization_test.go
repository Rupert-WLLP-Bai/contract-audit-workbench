package telemetry_test

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/execution"
	"contractaudit/internal/telemetry"
)

func TestExporterNewValidatesEndpointRatioAndIdentity(t *testing.T) {
	endpointTests := []struct {
		name     string
		endpoint string
	}{
		{"relative endpoint", "collector.example"},
		{"unsupported scheme", "ftp://collector.example"},
		{"endpoint credentials", "http://operator:secret@collector.example"},
		{"endpoint query", "http://collector.example?tenant=1"},
		{"endpoint fragment", "http://collector.example#fragment"},
		{"missing host", "http:///collector"},
		{"parse failure", "https://collector.example/%zz"},
	}
	for _, test := range endpointTests {
		t.Run(test.name, func(t *testing.T) {
			exporter, err := telemetry.New(telemetry.Config{Endpoint: test.endpoint, Service: "audit.worker", SampleRatio: 1})
			if err == nil || exporter != nil || err.Error() != "OTLP endpoint must be an HTTP(S) collector base URL without credentials, query or fragment" {
				t.Fatalf("invalid endpoint accepted: %v", err)
			}
		})
	}
	for _, ratio := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.01, 1.01} {
		exporter, err := telemetry.New(telemetry.Config{Endpoint: "http://collector.example", Service: "audit.worker", SampleRatio: ratio})
		if err == nil || exporter != nil || err.Error() != "OTLP sampling ratio must be finite and between zero and one" {
			t.Fatalf("invalid ratio %v accepted: %v", ratio, err)
		}
	}
	for _, service := range []string{"", "audit worker", strings.Repeat("a", 129)} {
		exporter, err := telemetry.New(telemetry.Config{Endpoint: "http://collector.example", Service: service, SampleRatio: 1})
		if err == nil || exporter != nil || err.Error() != "OTLP service identity is required" {
			t.Fatalf("invalid service %q accepted: %v", service, err)
		}
	}
	exporter, err := telemetry.New(telemetry.Config{Endpoint: "   ", Service: "audit.worker", SampleRatio: 1})
	if err != nil || exporter != nil {
		t.Fatalf("empty endpoint must disable the exporter: %v %v", exporter, err)
	}
}

func TestExporterNewJoinsCollectorPathsAndFallsBackInstanceIdentity(t *testing.T) {
	type captured struct {
		path string
		body []byte
	}
	bodies := make(chan captured, 2)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		bodies <- captured{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{
		Endpoint: server.URL + "/collector/", Service: "audit.worker", InstanceID: "invalid instance!", SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("New made a network request at startup")
	}
	exporter.Record("run", "attempt", completedEvent("span", "attempt"))
	exporter.Close()
	close(bodies)
	var sawTraces bool
	for body := range bodies {
		if body.path == "/collector/v1/traces" {
			sawTraces = true
			var request struct {
				ResourceSpans []struct {
					Resource struct {
						Attributes []struct {
							Key   string `json:"key"`
							Value struct {
								String string `json:"stringValue"`
							} `json:"value"`
						} `json:"attributes"`
					} `json:"resource"`
				} `json:"resourceSpans"`
			}
			if err := json.Unmarshal(body.body, &request); err != nil {
				t.Fatal(err)
			}
			attributes := map[string]string{}
			for _, item := range request.ResourceSpans[0].Resource.Attributes {
				attributes[item.Key] = item.Value.String
			}
			if attributes["service.name"] != "audit.worker" || attributes["service.instance.id"] != "audit.worker" {
				t.Fatalf("instance identity fallback %v", attributes)
			}
		}
	}
	if !sawTraces {
		t.Fatal("trace export did not use the joined collector path")
	}
}

func TestExporterRecordRejectsIncompleteEventsAndNilReceivers(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	duration := int64(5)
	negative := int64(-1)
	for _, event := range []execution.Event{
		{SpanID: "span", At: time.Unix(1780000000, 0)},
		{SpanID: "span", At: time.Unix(1780000000, 0), DurationMS: &negative},
		{SpanID: "span", DurationMS: &duration},
		{At: time.Unix(1780000000, 0), DurationMS: &duration},
	} {
		exporter.Record("run", "attempt", event)
	}
	exporter.Close()
	if stats := exporter.Stats(); calls.Load() != 0 || stats.Exported != 0 || stats.Dropped != 0 {
		t.Fatalf("invalid events reached the collector: calls=%d stats=%+v", calls.Load(), stats)
	}

	var nilExporter *telemetry.Exporter
	nilExporter.Record("run", "attempt", completedEvent("span", "attempt"))
	if stats := nilExporter.Stats(); stats != (telemetry.Stats{}) {
		t.Fatalf("nil exporter stats %+v", stats)
	}
	nilExporter.Close()

	closed, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	closed.Record("run", "attempt", completedEvent("span", "attempt"))
	if stats := closed.Stats(); stats.Dropped != 1 {
		t.Fatalf("record after close was not counted: %+v", stats)
	}
}

// The span projection is metadata only: status enum, parentage and sanitized
// attribute values are pinned, and the export carries nothing from the model.
func TestExporterRecordProjectsStatusParentageAndSanitizedMetadata(t *testing.T) {
	bodies := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			body, _ := io.ReadAll(r.Body)
			bodies <- body
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	duration := int64(9)
	base := time.Unix(1780000000, 500000000)
	events := []execution.Event{
		{SpanID: "completed", ParentSpanID: "attempt_one", NodeKind: "CompletedNode", Phase: "phase_one", At: base, DurationMS: &duration, Status: "completed"},
		{SpanID: "failed", ParentSpanID: "parent-span", NodeKind: "FailedNode", Phase: "bad phase!", At: base, DurationMS: &duration, Status: "failed"},
		{SpanID: "cancelled", NodeKind: "CancelledNode", Phase: "phase.three", At: base, DurationMS: &duration, Status: "cancelled"},
		{SpanID: "unknown", NodeKind: "UnknownNode", Phase: "phase-four", At: base, DurationMS: &duration, Status: "running"},
	}
	for _, event := range events {
		exporter.Record("run/one", "attempt_one", event)
	}
	exporter.Close()
	close(bodies)
	type capturedSpan struct {
		Name   string `json:"name"`
		Parent string `json:"parentSpanId"`
		Status struct {
			Code int `json:"code"`
		} `json:"status"`
		Attributes []struct {
			Key   string `json:"key"`
			Value struct {
				String string `json:"stringValue"`
			} `json:"value"`
		} `json:"attributes"`
	}
	spans := map[string]capturedSpan{}
	for body := range bodies {
		var request struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []capturedSpan `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					attributes := map[string]string{}
					for _, item := range span.Attributes {
						attributes[item.Key] = item.Value.String
					}
					spans[attributes["audit.node_kind"]] = span
				}
			}
		}
	}
	wantStatus := map[string]int{"CompletedNode": 1, "FailedNode": 2, "CancelledNode": 2, "UnknownNode": 0}
	for kind, code := range wantStatus {
		span, ok := spans[kind]
		if !ok || span.Status.Code != code {
			t.Fatalf("status projection for %s: %+v", kind, span)
		}
	}
	if spans["CompletedNode"].Parent != "" {
		t.Fatalf("attempt parent span leaked: %+v", spans["CompletedNode"])
	}
	if spans["FailedNode"].Parent == "" || spans["CancelledNode"].Parent != "" {
		t.Fatalf("parent span projection is wrong: %+v %+v", spans["FailedNode"], spans["CancelledNode"])
	}
	if spans["FailedNode"].Name != "" {
		t.Fatalf("invalid phase was exported as a name: %+v", spans["FailedNode"])
	}
	if spans["CompletedNode"].Name != "phase_one" || spans["CancelledNode"].Name != "phase.three" {
		t.Fatalf("valid phase name lost: %+v %+v", spans["CompletedNode"], spans["CancelledNode"])
	}
	for _, span := range spans {
		for _, item := range span.Attributes {
			if item.Key == "audit.run_id" && item.Value.String != "" {
				t.Fatalf("invalid run identity was exported: %+v", item)
			}
			if strings.Contains(item.Value.String, "PRIVATE") {
				t.Fatalf("provider content reached the export: %+v", item)
			}
		}
	}
}

func TestExporterRecordRespectsRatioBoundary(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	for _, ratio := range []float64{0, 1} {
		exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: ratio})
		if err != nil {
			t.Fatal(err)
		}
		exporter.Record("run", "attempt", completedEvent("span", "attempt"))
		exporter.Close()
		stats := exporter.Stats()
		if ratio == 0 && (stats.SampledOut != 1 || stats.Exported != 0) {
			t.Fatalf("ratio zero exported a span: %+v", stats)
		}
		if ratio == 1 && (stats.Exported != 1 || stats.SampledOut != 0) {
			t.Fatalf("ratio one dropped a span: %+v", stats)
		}
	}
}

func TestExporterMetadataAcceptsOnlyBoundedIdentifierCharacters(t *testing.T) {
	bodies := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			body, _ := io.ReadAll(r.Body)
			bodies <- body
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", InstanceID: "instance_1.2-3", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	exporter.Record("run_1.2-3", "attempt_1.2-3", completedEvent("span", "attempt"))
	exporter.Close()
	var body []byte
	select {
	case body = <-bodies:
	case <-time.After(3 * time.Second):
		t.Fatal("trace export did not arrive")
	}
	var request struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []struct {
					Key   string `json:"key"`
					Value struct {
						String string `json:"stringValue"`
					} `json:"value"`
				} `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []struct {
					Attributes []struct {
						Key   string `json:"key"`
						Value struct {
							String string `json:"stringValue"`
						} `json:"value"`
					} `json:"attributes"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	attributes := map[string]string{}
	for _, item := range request.ResourceSpans[0].Resource.Attributes {
		attributes[item.Key] = item.Value.String
	}
	if attributes["service.instance.id"] != "instance_1.2-3" {
		t.Fatalf("valid instance identity rejected: %v", attributes)
	}
	spanAttributes := map[string]string{}
	for _, item := range request.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes {
		spanAttributes[item.Key] = item.Value.String
	}
	if spanAttributes["audit.run_id"] != "run_1.2-3" || spanAttributes["audit.attempt_id"] != "attempt_1.2-3" {
		t.Fatalf("valid metadata rejected: %v", spanAttributes)
	}
}
