// File metadata (manifest mirror):
// Purpose: Exercises the `telemetry_test` package behavior through TestExporterPreservesTraceTreeWithoutProviderContent, TestExporterAcknowledgementFailureAccounting, TestExporterSlowCollectorCannotBlockProducerOrShutdown, TestExporterSamplingDoesNotExportRejectedAttempts, TestExporterMetricsRemainCumulativeAcrossBatches; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-11
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package telemetry_test

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/execution"
	"contractaudit/internal/telemetry"
	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func completedEvent(span, parent string) execution.Event {
	duration := int64(7)
	return execution.Event{
		SpanID: span, ParentSpanID: parent, NodeKind: "Lambda", Phase: "rulepack",
		EventType: "node_completed", At: time.Unix(1780000000, 123456789),
		DurationMS: &duration, Status: "completed",
	}
}

func TestExporterUsesOTLPSDKAndPreservesTraceAndMetricProjection(t *testing.T) {
	traceRequests := make(chan *tracecollectorpb.ExportTraceServiceRequest, 2)
	metricRequests := make(chan *metricspb.ExportMetricsServiceRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("unexpected OTLP request: %s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		switch r.URL.Path {
		case "/collector/v1/traces":
			traceRequests <- readTraceRequest(t, r.Body)
		case "/collector/v1/metrics":
			metricRequests <- readMetricRequest(t, r.Body)
		default:
			t.Errorf("unexpected OTLP endpoint %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeOTLPAck(w, r.URL.Path)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{
		Endpoint: server.URL + "/collector", Service: "audit.worker", InstanceID: "worker-7", SampleRatio: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	root := completedEvent("root", "att_one")
	root.ModelRef = "PRIVATE CONTRACT BODY"
	root.ErrorCode = "secret=PRIVATE KEY"
	root.Usage = &execution.Usage{Source: "PRIVATE PROVIDER RESPONSE"}
	exporter.Record("run_one", "att_one", root)
	exporter.Record("run_one", "att_one", completedEvent("child", "root"))
	exporter.Close()

	traceRequest := <-traceRequests
	spans := traces(traceRequest)
	if len(spans) != 2 {
		t.Fatalf("expected two exported spans, got %d", len(spans))
	}
	resources := traceRequest.GetResourceSpans()
	if len(resources) != 1 ||
		stringAttr(resources[0].GetResource().GetAttributes(), "service.name") != "audit.worker" ||
		stringAttr(resources[0].GetResource().GetAttributes(), "service.instance.id") != "worker-7" {
		t.Fatalf("resource identity was not projected: %+v", resources)
	}
	if stringAttr(spans[0].GetAttributes(), "audit.error_code") != "" ||
		stringAttr(spans[0].GetAttributes(), "audit.run_id") != "run_one" {
		t.Fatalf("metadata projection was not sanitized: %v", spans[0].GetAttributes())
	}
	if spans[0].GetTraceId() == nil || string(spans[0].GetTraceId()) != string(spans[1].GetTraceId()) ||
		len(spans[0].GetParentSpanId()) != 0 || string(spans[1].GetParentSpanId()) != string(spans[0].GetSpanId()) {
		t.Fatalf("OTLP span tree differs from durable event parentage: %+v", spans)
	}
	expectedTrace := sha256.Sum256([]byte("att_one"))
	if string(spans[0].GetTraceId()) != string(expectedTrace[:16]) ||
		binary.BigEndian.Uint64(spans[0].GetSpanId()) == 0 ||
		spans[0].GetKind().String() != "SPAN_KIND_INTERNAL" ||
		spans[0].GetStatus().GetCode().String() != "STATUS_CODE_OK" ||
		spans[0].GetEndTimeUnixNano()-spans[0].GetStartTimeUnixNano() != 7_000_000 {
		t.Fatalf("invalid OTLP identity, timestamp or enum: %+v", spans[0])
	}

	var metricRequest *metricspb.ExportMetricsServiceRequest
	select {
	case metricRequest = <-metricRequests:
	case <-time.After(3 * time.Second):
		t.Fatal("SDK did not export event metrics during shutdown")
	}
	items := metrics(metricRequest)
	events := items["audit.execution.events"].GetSum().GetDataPoints()
	duration := items["audit.execution.duration_ms"].GetGauge().GetDataPoints()
	if len(events) != 1 || events[0].GetAsInt() != 2 || len(duration) != 1 ||
		duration[0].GetAsInt() != 7 || metricStringAttr(events[0].GetAttributes(), "audit.phase") != "rulepack" {
		t.Fatalf("unexpected SDK execution metrics: %+v %+v", events, duration)
	}
	stats := exporter.Stats()
	if stats.Exported != 2 || stats.Dropped != 0 || stats.Unknown != 0 ||
		stats.MetricsExported == 0 || stats.MetricsUnknown != 0 || stats.FailedRequests != 0 {
		t.Fatalf("export acknowledgement accounting is incorrect: %+v", stats)
	}
}

func TestExporterCountsExactPartialAndUnconfirmedAcknowledgements(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		status      int
		partial     bool
	}{
		{name: "protobuf partial response", contentType: "application/x-protobuf", status: http.StatusOK, partial: true},
		{name: "unconfirmed response", contentType: "application/json", status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			var traceCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/traces" {
					traceCalls.Add(1)
					request := readTraceRequest(t, r.Body)
					if test.partial {
						w.Header().Set("Content-Type", test.contentType)
						w.WriteHeader(test.status)
						response, _ := proto.Marshal(&tracecollectorpb.ExportTraceServiceResponse{
							PartialSuccess: &tracecollectorpb.ExportTracePartialSuccess{
								RejectedSpans: 1, ErrorMessage: "private collector detail",
							},
						})
						_, _ = w.Write(response)
						if len(traces(request)) != 2 {
							t.Errorf("expected two spans in one SDK export")
						}
						return
					}
					w.Header().Set("Content-Type", test.contentType)
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, "{}")
					return
				}
				writeOTLPAck(w, r.URL.Path)
			}))
			defer server.Close()
			exporter, err := telemetry.New(telemetry.Config{
				Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			exporter.Record("run", "attempt", completedEvent("one", "attempt"))
			exporter.Record("run", "attempt", completedEvent("two", "attempt"))
			exporter.Close()
			stats := exporter.Stats()
			if traceCalls.Load() != 1 {
				t.Fatalf("SDK retried an export: calls=%d stats=%+v", traceCalls.Load(), stats)
			}
			if test.partial {
				if stats.Exported != 1 || stats.Dropped != 1 || stats.Unknown != 0 || stats.FailedRequests != 0 {
					t.Fatalf("partial response was not counted exactly: %+v", stats)
				}
			} else if stats.Exported != 0 || stats.Dropped != 0 || stats.Unknown != 2 || stats.FailedRequests != 1 {
				t.Fatalf("unconfirmed response was reported as successful: %+v", stats)
			}
		})
	}
}

func TestExporterProjectsStatusParentageAndSanitizedMetadata(t *testing.T) {
	traceRequests := make(chan *tracecollectorpb.ExportTraceServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			traceRequests <- readTraceRequest(t, r.Body)
		}
		writeOTLPAck(w, r.URL.Path)
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
		{SpanID: "failed", ParentSpanID: "parent-span", NodeKind: "FailedNode", Phase: "bad phase!", At: base, DurationMS: &duration, Status: "failed", ErrorCode: "private=value"},
		{SpanID: "cancelled", NodeKind: "CancelledNode", Phase: "phase.three", At: base, DurationMS: &duration, Status: "cancelled"},
		{SpanID: "unknown", NodeKind: "UnknownNode", Phase: "phase-four", At: base, DurationMS: &duration, Status: "running"},
	}
	for _, event := range events {
		exporter.Record("run/one", "attempt_one", event)
	}
	exporter.Close()
	spans := traces(<-traceRequests)
	byKind := make(map[string]*tracepb.Span)
	for _, span := range spans {
		byKind[stringAttr(span.GetAttributes(), "audit.node_kind")] = span
	}
	for kind, status := range map[string]int32{
		"CompletedNode": 1, "FailedNode": 2, "CancelledNode": 2, "UnknownNode": 0,
	} {
		span := byKind[kind]
		if span == nil || int32(span.GetStatus().GetCode()) != status {
			t.Fatalf("status projection for %s: %+v", kind, span)
		}
	}
	if len(byKind["CompletedNode"].GetParentSpanId()) != 0 ||
		len(byKind["FailedNode"].GetParentSpanId()) != 8 ||
		len(byKind["CancelledNode"].GetParentSpanId()) != 0 ||
		byKind["FailedNode"].GetName() != "" ||
		byKind["CompletedNode"].GetName() != "phase_one" ||
		stringAttr(byKind["FailedNode"].GetAttributes(), "audit.error_code") != "" ||
		stringAttr(byKind["FailedNode"].GetAttributes(), "audit.run_id") != "" {
		t.Fatalf("parentage or sanitized metadata changed: %+v", byKind)
	}
}

func TestExporterProducerDoesNotWaitForCollector(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		writeOTLPAck(w, r.URL.Path)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{
		Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatal("collector latency blocked durable event publication")
	}
	start := time.Now()
	exporter.Close()
	if time.Since(start) > 4*time.Second || exporter.Stats().Unknown == 0 {
		t.Fatalf("shutdown or unknown accounting is incorrect: elapsed=%s stats=%+v", time.Since(start), exporter.Stats())
	}
	close(release)
	exporter.Record("run", "attempt", event)
	if exporter.Stats().Dropped != 1 {
		t.Fatalf("closed exporter did not account for a local rejection: %+v", exporter.Stats())
	}
}

func TestExporterExecutionMetricsRemainCumulative(t *testing.T) {
	metricRequests := make(chan *metricspb.ExportMetricsServiceRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/metrics" {
			metricRequests <- readMetricRequest(t, r.Body)
		}
		writeOTLPAck(w, r.URL.Path)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	first := completedEvent("first", "attempt")
	exporter.Record("run", "attempt", first)
	exporter.flushMetrics(true)
	firstMetrics := metrics(<-metricRequests)
	firstPoints := firstMetrics["audit.execution.events"].GetSum().GetDataPoints()
	if len(firstPoints) != 1 || firstPoints[0].GetAsInt() != 1 ||
		firstMetrics["audit.execution.events"].GetSum().GetAggregationTemporality().String() != "AGGREGATION_TEMPORALITY_CUMULATIVE" {
		t.Fatalf("first event counter was not cumulative: %+v", firstMetrics)
	}

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
	exporter.flushMetrics(true)
	secondMetrics := metrics(<-metricRequests)
	points := secondMetrics["audit.execution.events"].GetSum().GetDataPoints()
	var completedCount, failedCount int64
	for _, point := range points {
		switch metricStringAttr(point.GetAttributes(), "audit.status") {
		case "completed":
			completedCount = point.GetAsInt()
		case "failed":
			failedCount = point.GetAsInt()
		}
	}
	durationPoints := secondMetrics["audit.execution.duration_ms"].GetGauge().GetDataPoints()
	var latestCompleted float64
	for _, point := range durationPoints {
		if metricStringAttr(point.GetAttributes(), "audit.status") == "completed" {
			latestCompleted = float64(point.GetAsInt())
		}
	}
	if completedCount != 3 || failedCount != 1 || latestCompleted != 11 {
		t.Fatalf("cumulative events or latest duration changed: counts=%d/%d duration=%v", completedCount, failedCount, latestCompleted)
	}
	exporter.Close()
}

func TestExporterBoundsExecutionMetricSeriesWithoutLosingEvents(t *testing.T) {
	metricRequests := make(chan *metricspb.ExportMetricsServiceRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/metrics" {
			metricRequests <- readMetricRequest(t, r.Body)
		}
		writeOTLPAck(w, r.URL.Path)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	phases := []string{
		"audit_review_execution_v1", "eino_component", "input_preparation", "deterministic_rules",
		"model_review", "citation_verification", "single_contract_rules", "agent_review",
		"authorized_input", "fact_extraction", "tool_evidence", "rulepack", "evidence_guard",
		"persistence", "verified_finding", "model_request", "evidence_tool", "agent_review_result",
		"acceptance", "execution", "terminal_persistence", "cancellation", "restart_recovery",
		"authorization", "input", "gap_unsupported",
	}
	kinds := []string{
		"attempt", "phase", "model_call", "citation_verification", "agent_review", "tool_call",
		"eino", "Unknown", "Graph", "Workflow", "Chain", "Passthrough",
	}
	var count int
	for _, phase := range phases {
		for _, kind := range kinds {
			if count == 300 {
				break
			}
			event := completedEvent("event_"+strconv.Itoa(count), "attempt")
			event.Phase, event.NodeKind = phase, kind
			exporter.Record("run", "attempt", event)
			count++
		}
		if count == 300 {
			break
		}
	}
	exporter.Close()
	request := <-metricRequests
	item := metrics(request)["audit.execution.events"]
	points := item.GetSum().GetDataPoints()
	if len(points) > 256 {
		t.Fatalf("execution dimensions exceeded the cardinality bound: %d", len(points))
	}
	var total int64
	for _, point := range points {
		total += point.GetAsInt()
	}
	if len(points) == 0 || total != 300 {
		t.Fatalf("bounded dimensions lost durable event counts: series=%d total=%d", len(points), total)
	}
}

func TestExporterSamplingAndClosedRecord(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeOTLPAck(w, r.URL.Path)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{
		Endpoint: server.URL, Service: "audit.worker", SampleRatio: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	exporter.Record("run", "attempt", completedEvent("root", "attempt"))
	exporter.Record("run", "attempt", completedEvent("child", "root"))
	exporter.Close()
	if calls.Load() != 0 || exporter.Stats().SampledOut != 2 || exporter.Stats().Dropped != 0 {
		t.Fatalf("sampled-out events reached the collector: calls=%d stats=%+v", calls.Load(), exporter.Stats())
	}
	closed, err := telemetry.New(telemetry.Config{
		Endpoint: server.URL, Service: "audit.worker", SampleRatio: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	closed.Record("run", "attempt", completedEvent("after-close", "attempt"))
	if closed.Stats().Dropped != 1 || closed.Stats().Unknown != 0 {
		t.Fatalf("record after close was not resolved as a local drop: %+v", closed.Stats())
	}
}
