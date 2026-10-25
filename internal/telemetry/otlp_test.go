package telemetry_test

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/execution"
	"contractaudit/internal/telemetry"
)

func completedEvent(span, parent string) execution.Event {
	duration := int64(7)
	return execution.Event{SpanID: span, ParentSpanID: parent, NodeKind: "Lambda", Phase: "rulepack",
		EventType: "node_completed", At: time.Unix(1780000000, 123456789), DurationMS: &duration, Status: "completed"}
}

func TestExporterPreservesTraceTreeWithoutProviderContent(t *testing.T) {
	type captured struct {
		path string
		body []byte
	}
	bodies := make(chan captured, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" ||
			(r.URL.Path != "/collector/v1/traces" && r.URL.Path != "/collector/v1/metrics") {
			t.Errorf("invalid OTLP request: %s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		bodies <- captured{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL + "/collector", Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer exporter.Close()
	root := completedEvent("root", "att_one")
	root.ModelRef = "PRIVATE CONTRACT BODY"
	root.ErrorCode = "secret=PRIVATE KEY"
	root.Usage = &execution.Usage{Source: "PRIVATE PROVIDER RESPONSE"}
	exporter.Record("run_one", "att_one", root)
	exporter.Record("run_one", "att_one", completedEvent("child", "root"))
	exporter.Close()
	close(bodies)
	var spans []map[string]any
	var metricBodies [][]byte
	for body := range bodies {
		if strings.Contains(string(body.body), "PRIVATE") || strings.Contains(string(body.body), "tokens") {
			t.Fatalf("provider content escaped the metadata boundary: %s", body.body)
		}
		if body.path == "/collector/v1/metrics" {
			metricBodies = append(metricBodies, body.body)
			continue
		}
		var request map[string]any
		if err := json.Unmarshal(body.body, &request); err != nil {
			t.Fatal(err)
		}
		for _, rawResource := range request["resourceSpans"].([]any) {
			resource := rawResource.(map[string]any)
			for _, rawScope := range resource["scopeSpans"].([]any) {
				for _, rawSpan := range rawScope.(map[string]any)["spans"].([]any) {
					spans = append(spans, rawSpan.(map[string]any))
				}
			}
		}
	}
	if len(spans) != 2 || exporter.Stats().Exported != 2 || exporter.Stats().Dropped != 0 || exporter.Stats().MetricsExported != 2 || exporter.Stats().MetricsFailed != 0 {
		t.Fatalf("lost completed spans or execution metrics: spans=%v metrics=%d stats=%+v", spans, len(metricBodies), exporter.Stats())
	}
	var sawEvents, sawDuration bool
	for _, body := range metricBodies {
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		for _, rawResource := range request["resourceMetrics"].([]any) {
			resource := rawResource.(map[string]any)
			for _, rawScope := range resource["scopeMetrics"].([]any) {
				for _, rawMetric := range rawScope.(map[string]any)["metrics"].([]any) {
					item := rawMetric.(map[string]any)
					switch item["name"] {
					case "audit.execution.events":
						sawEvents = true
					case "audit.execution.duration_ms":
						sawDuration = true
					}
				}
			}
		}
	}
	if !sawEvents || !sawDuration {
		t.Fatalf("execution metrics were not produced: %s", metricBodies)
	}
	if spans[0]["traceId"] != spans[1]["traceId"] || spans[0]["parentSpanId"] != nil || spans[1]["parentSpanId"] != spans[0]["spanId"] {
		t.Fatalf("OTLP tree differs from execution parentage: %v", spans)
	}
	for _, span := range spans {
		trace, traceErr := hex.DecodeString(span["traceId"].(string))
		id, spanErr := hex.DecodeString(span["spanId"].(string))
		start, startErr := strconv.ParseInt(span["startTimeUnixNano"].(string), 10, 64)
		end, endErr := strconv.ParseInt(span["endTimeUnixNano"].(string), 10, 64)
		if traceErr != nil || spanErr != nil || len(trace) != 16 || len(id) != 8 ||
			startErr != nil || endErr != nil || end-start != 7_000_000 || span["kind"] != float64(1) || span["status"].(map[string]any)["code"] != float64(1) {
			t.Fatalf("invalid protocol identity, timestamp or enum: %v", span)
		}
	}
}

func TestExporterAcknowledgementFailureAccounting(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		exported   uint64
		failed     uint64
	}{
		{"success", `{}`, 200, 1, 0},
		{"partial_string", `{"partialSuccess":{"rejectedSpans":"1","errorMessage":"private collector detail"}}`, 200, 0, 0},
		{"partial_number", `{"partialSuccess":{"rejectedSpans":1}}`, 200, 0, 0},
		{"bad_count", `{"partialSuccess":{"rejectedSpans":"2"}}`, 200, 0, 1},
		{"null_ack", `null`, 200, 0, 1},
		{"malformed", `{`, 200, 0, 1},
		{"unavailable", `{"message":"private collector error"}`, 503, 0, 1},
		{"oversized", `{"detail":"` + strings.Repeat("x", 65536) + `"}`, 200, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
			if err != nil {
				t.Fatal(err)
			}
			exporter.Record("run", "attempt", completedEvent("span", "attempt"))
			exporter.Close()
			stats := exporter.Stats()
			wantCalls := int64(1)
			if test.failed == 0 {
				wantCalls = 2
			}
			if calls.Load() != wantCalls || stats.Exported != test.exported || stats.Dropped != 1-test.exported || stats.FailedRequests != test.failed {
				t.Fatalf("acknowledgement was misreported or retried: calls=%d stats=%+v", calls.Load(), stats)
			}
		})
	}
}

func TestExporterSlowCollectorCannotBlockProducerOrShutdown(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer exporter.Close()
	event := completedEvent("span", "attempt")
	exporter.Record("run", "attempt", event)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not receive the first span")
	}
	produced := make(chan struct{})
	go func() {
		for range 4096 {
			exporter.Record("run", "attempt", event)
		}
		close(produced)
	}()
	select {
	case <-produced:
	case <-time.After(time.Second):
		t.Fatal("a saturated exporter blocked business event publication")
	}
	if exporter.Stats().Dropped == 0 {
		t.Fatal("the bounded queue failed to account for overflow")
	}
	start := time.Now()
	exporter.Close()
	if time.Since(start) > 4*time.Second {
		t.Fatal("shutdown exceeded the collector-independent deadline")
	}
	exporter.Record("run", "attempt", event)
	stats := exporter.Stats()
	if stats.Exported != 0 || stats.Dropped != 4098 || stats.FailedRequests == 0 {
		t.Fatalf("lost spans were unaccounted after saturation/shutdown: %+v", stats)
	}
}

func TestExporterSamplingDoesNotExportRejectedAttempts(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 0})
	if err != nil {
		t.Fatal(err)
	}
	exporter.Record("run", "attempt", completedEvent("root", "attempt"))
	exporter.Record("run", "attempt", completedEvent("child", "root"))
	exporter.Close()
	if calls.Load() != 0 || exporter.Stats().SampledOut != 2 || exporter.Stats().Dropped != 0 {
		t.Fatalf("sampled-out attempt reached collector or became delivery loss: calls=%d stats=%+v", calls.Load(), exporter.Stats())
	}
}
