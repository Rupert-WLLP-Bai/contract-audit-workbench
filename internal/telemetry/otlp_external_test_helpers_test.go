package telemetry_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricdatapb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func writeOTLPAck(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/x-protobuf")
	var body []byte
	if strings.HasSuffix(path, "/v1/traces") {
		body, _ = proto.Marshal(&tracecollectorpb.ExportTraceServiceResponse{})
	} else {
		body, _ = proto.Marshal(&metricspb.ExportMetricsServiceResponse{})
	}
	_, _ = w.Write(body)
}

func readTraceRequest(t *testing.T, r io.Reader) *tracecollectorpb.ExportTraceServiceRequest {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	request := new(tracecollectorpb.ExportTraceServiceRequest)
	if err := proto.Unmarshal(data, request); err != nil {
		t.Fatal(err)
	}
	return request
}

func readMetricRequest(t *testing.T, r io.Reader) *metricspb.ExportMetricsServiceRequest {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	request := new(metricspb.ExportMetricsServiceRequest)
	if err := proto.Unmarshal(data, request); err != nil {
		t.Fatal(err)
	}
	return request
}

func traces(request *tracecollectorpb.ExportTraceServiceRequest) []*tracepb.Span {
	var spans []*tracepb.Span
	for _, resource := range request.GetResourceSpans() {
		for _, scope := range resource.GetScopeSpans() {
			spans = append(spans, scope.GetSpans()...)
		}
	}
	return spans
}

func metrics(request *metricspb.ExportMetricsServiceRequest) map[string]*metricdatapb.Metric {
	result := make(map[string]*metricdatapb.Metric)
	for _, resource := range request.GetResourceMetrics() {
		for _, scope := range resource.GetScopeMetrics() {
			for _, item := range scope.GetMetrics() {
				result[item.GetName()] = item
			}
		}
	}
	return result
}

func stringAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, attr := range attrs {
		if attr.GetKey() == key {
			return attr.GetValue().GetStringValue()
		}
	}
	return ""
}

func metricStringAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, attr := range attrs {
		if attr.GetKey() == key {
			return attr.GetValue().GetStringValue()
		}
	}
	return ""
}
