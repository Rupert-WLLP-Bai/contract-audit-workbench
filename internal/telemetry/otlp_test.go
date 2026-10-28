package telemetry_test

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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
	if len(spans) != 2 || exporter.Stats().Exported != 2 || exporter.Stats().Dropped != 0 || exporter.Stats().MetricsExported != uint64(2*len(metricBodies)) || exporter.Stats().MetricsFailed != 0 {
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

type metricPoint struct {
	Attributes []struct {
		Key   string `json:"key"`
		Value struct {
			String string `json:"stringValue"`
		} `json:"value"`
	} `json:"attributes"`
	Start string `json:"startTimeUnixNano"`
	Time  string `json:"timeUnixNano"`
	Value string `json:"asInt"`
}

type capturedMetric struct {
	Name string `json:"name"`
	Sum  *struct {
		Temporality int           `json:"aggregationTemporality"`
		Monotonic   bool          `json:"isMonotonic"`
		Points      []metricPoint `json:"dataPoints"`
	} `json:"sum"`
	Gauge *struct {
		Points []metricPoint `json:"dataPoints"`
	} `json:"gauge"`
}

func decodeMetrics(t *testing.T, body []byte) map[string]capturedMetric {
	t.Helper()
	var payload struct {
		Resources []struct {
			Scopes []struct {
				Metrics []capturedMetric `json:"metrics"`
			} `json:"scopeMetrics"`
		} `json:"resourceMetrics"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	metrics := make(map[string]capturedMetric)
	for _, resource := range payload.Resources {
		for _, scope := range resource.Scopes {
			for _, metric := range scope.Metrics {
				metrics[metric.Name] = metric
			}
		}
	}
	if len(metrics) != 2 {
		t.Fatalf("expected event and duration metrics: %s", body)
	}
	return metrics
}

func receiveMetrics(t *testing.T, bodies <-chan []byte) []byte {
	t.Helper()
	select {
	case body := <-bodies:
		return body
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not receive execution metrics")
		return nil
	}
}

func metricPointKey(point metricPoint) string {
	var key strings.Builder
	for _, attr := range point.Attributes {
		key.WriteString(attr.Key)
		key.WriteByte(0)
		key.WriteString(attr.Value.String)
		key.WriteByte(0)
	}
	return key.String()
}

func TestExporterMetricsRemainCumulativeAcrossBatches(t *testing.T) {
	bodies := make(chan []byte, 2)
	release := make(chan struct{})
	var unblock sync.Once
	var metricCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/metrics" {
			body, _ := io.ReadAll(r.Body)
			bodies <- body
			if metricCalls.Add(1) == 1 {
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer exporter.Close()
	defer unblock.Do(func() { close(release) })
	first := completedEvent("first", "attempt")
	exporter.Record("run", "attempt", first)
	firstMetrics := decodeMetrics(t, receiveMetrics(t, bodies))
	later := completedEvent("later", "attempt")
	later.At = first.At.Add(10 * time.Second)
	duration := int64(11)
	later.DurationMS = &duration
	exporter.Record("run", "attempt", later)
	older := completedEvent("older", "attempt")
	older.At = first.At.Add(5 * time.Second)
	exporter.Record("run", "attempt", older)
	failed := completedEvent("failed", "attempt")
	failed.At = first.At.Add(8 * time.Second)
	failed.Status = "failed"
	exporter.Record("run", "attempt", failed)
	unblock.Do(func() { close(release) })
	exporter.Close()
	secondMetrics := decodeMetrics(t, receiveMetrics(t, bodies))
	firstSum := firstMetrics["audit.execution.events"].Sum
	secondSum := secondMetrics["audit.execution.events"].Sum
	if firstSum == nil || secondSum == nil || firstSum.Temporality != 2 || secondSum.Temporality != 2 ||
		!firstSum.Monotonic || !secondSum.Monotonic || len(firstSum.Points) != 1 || len(secondSum.Points) != 2 {
		t.Fatalf("events must contain one cumulative point per series: first=%+v second=%+v", firstSum, secondSum)
	}
	firstPoint := firstSum.Points[0]
	firstStart, startErr := strconv.ParseInt(firstPoint.Start, 10, 64)
	if startErr != nil || firstStart > first.At.UnixNano() || firstPoint.Value != "1" {
		t.Fatalf("invalid first cumulative point: %+v", firstPoint)
	}
	points := make(map[string]metricPoint)
	for _, point := range secondSum.Points {
		key := metricPointKey(point)
		if _, exists := points[key]; exists {
			t.Fatalf("duplicate series in one metric batch: %+v", secondSum.Points)
		}
		points[key] = point
	}
	continued, ok := points[metricPointKey(firstPoint)]
	if !ok || continued.Value != "3" || continued.Start != firstPoint.Start {
		t.Fatalf("event counter reset across separated batches: first=%+v next=%+v", firstPoint, continued)
	}
	continuedTime, timeErr := strconv.ParseInt(continued.Time, 10, 64)
	if timeErr != nil || continuedTime < later.At.UnixNano() {
		t.Fatalf("out-of-order events moved the cumulative timestamp backwards: %+v", continued)
	}
	for key, point := range points {
		if key != metricPointKey(firstPoint) && point.Value != "1" {
			t.Fatalf("different statuses shared a counter: %+v", secondSum.Points)
		}
	}
	gauge := secondMetrics["audit.execution.duration_ms"].Gauge
	if gauge == nil || len(gauge.Points) != 2 {
		t.Fatalf("duration gauge must contain one point per series: %+v", gauge)
	}
	for _, point := range gauge.Points {
		if metricPointKey(point) == metricPointKey(firstPoint) && point.Value != "11" {
			t.Fatalf("duration gauge did not retain the latest event: %+v", point)
		}
	}
	if stats := exporter.Stats(); stats.Exported != 4 || stats.Dropped != 0 || stats.MetricsExported != 6 || stats.MetricsFailed != 0 {
		t.Fatalf("metric accounting must count emitted data points: %+v", stats)
	}
}

func TestExporterMetricsAcknowledgementAccounting(t *testing.T) {
	for _, test := range []struct {
		name, body, contentType string
		status                  int
		exported, failed        uint64
	}{
		{"success", `{}`, "application/json", 200, 2, 0},
		{"warning", `{"partialSuccess":{"rejectedDataPoints":"0","errorMessage":"collector warning"}}`, "application/json", 200, 2, 0},
		{"partial_string", `{"partialSuccess":{"rejectedDataPoints":"1"}}`, "application/json", 200, 1, 1},
		{"partial_number", `{"partialSuccess":{"rejectedDataPoints":1}}`, "application/json", 200, 1, 1},
		{"all_rejected", `{"partialSuccess":{"rejectedDataPoints":"2"}}`, "application/json", 200, 0, 2},
		{"too_many", `{"partialSuccess":{"rejectedDataPoints":"3"}}`, "application/json", 200, 0, 2},
		{"negative", `{"partialSuccess":{"rejectedDataPoints":"-1"}}`, "application/json", 200, 0, 2},
		{"fraction", `{"partialSuccess":{"rejectedDataPoints":0.5}}`, "application/json", 200, 0, 2},
		{"overflow", `{"partialSuccess":{"rejectedDataPoints":"9223372036854775808"}}`, "application/json", 200, 0, 2},
		{"invalid_string", `{"partialSuccess":{"rejectedDataPoints":"unknown"}}`, "application/json", 200, 0, 2},
		{"invalid_type", `{"partialSuccess":{"rejectedDataPoints":true}}`, "application/json", 200, 0, 2},
		{"invalid_partial", `{"partialSuccess":[]}`, "application/json", 200, 0, 2},
		{"null_ack", `null`, "application/json", 200, 0, 2},
		{"array_ack", `[]`, "application/json", 200, 0, 2},
		{"malformed", `{`, "application/json", 200, 0, 2},
		{"trailing_json", `{} {}`, "application/json", 200, 0, 2},
		{"wrong_content_type", `{}`, "text/plain", 200, 0, 2},
		{"unavailable", `{}`, "application/json", 503, 0, 2},
		{"oversized", `{"detail":"` + strings.Repeat("x", 65536) + `"}`, "application/json", 200, 0, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var metricCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/metrics" {
					metricCalls.Add(1)
					w.Header().Set("Content-Type", test.contentType)
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, test.body)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{}`)
			}))
			defer server.Close()
			exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
			if err != nil {
				t.Fatal(err)
			}
			exporter.Record("run", "attempt", completedEvent("span", "attempt"))
			exporter.Close()
			stats := exporter.Stats()
			if metricCalls.Load() != 1 || stats.MetricsExported != test.exported || stats.MetricsFailed != test.failed ||
				stats.Exported != 1 || stats.Dropped != 0 || stats.FailedRequests != 0 {
				t.Fatalf("metric acknowledgement was misreported or retried: calls=%d stats=%+v", metricCalls.Load(), stats)
			}
		})
	}
}

func TestExporterMetricsBoundsSeriesWithoutLosingEvents(t *testing.T) {
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/metrics" {
			body, _ := io.ReadAll(r.Body)
			bodies <- body
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer exporter.Close()
	counts := make(map[string]int64)
	for i := range 1024 {
		event := completedEvent("span", "attempt")
		event.Phase = "phase_" + strconv.Itoa(i)
		exporter.Record("run", "attempt", event)
		metrics := decodeMetrics(t, receiveMetrics(t, bodies))
		sum := metrics["audit.execution.events"].Sum
		if sum == nil || len(sum.Points) != 1 {
			t.Fatalf("each batch must export its one active series: %+v", sum)
		}
		point := sum.Points[0]
		count, err := strconv.ParseInt(point.Value, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		counts[metricPointKey(point)] = count
	}
	exporter.Close()
	var total int64
	for _, count := range counts {
		total += count
	}
	if len(counts) > 256 || total != 1024 || exporter.Stats().Dropped != 0 || exporter.Stats().MetricsFailed != 0 {
		t.Fatalf("metric cardinality must stay bounded without losing events: series=%d total=%d stats=%+v", len(counts), total, exporter.Stats())
	}
}

func TestExporterResourceIdentifiesServiceInstance(t *testing.T) {
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
	exporter, err := telemetry.New(telemetry.Config{
		Endpoint: server.URL, Service: "audit.worker", InstanceID: "worker-7", SampleRatio: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	exporter.Record("run", "attempt", completedEvent("root", "attempt"))
	var body []byte
	select {
	case body = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("trace export did not arrive")
	}
	exporter.Close()
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
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.ResourceSpans) != 1 {
		t.Fatalf("resource spans missing: %s", body)
	}
	attributes := map[string]string{}
	for _, item := range request.ResourceSpans[0].Resource.Attributes {
		attributes[item.Key] = item.Value.String
	}
	if attributes["service.name"] != "audit.worker" || attributes["service.instance.id"] != "worker-7" {
		t.Fatalf("replica identity missing from OTLP resource: %v", attributes)
	}
}
