package telemetry

import (
	"io"
	"net/http"
	"testing"

	metriccollectorpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func writeInternalOTLPAck(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/x-protobuf")
	body, _ := proto.Marshal(&metriccollectorpb.ExportMetricsServiceResponse{})
	_, _ = w.Write(body)
}

func decodeInternalMetricRequest(t *testing.T, reader io.Reader) *metriccollectorpb.ExportMetricsServiceRequest {
	t.Helper()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	request := new(metriccollectorpb.ExportMetricsServiceRequest)
	if err := proto.Unmarshal(data, request); err != nil {
		t.Fatal(err)
	}
	return request
}

func internalMetrics(request *metriccollectorpb.ExportMetricsServiceRequest) map[string]*metricpb.Metric {
	result := make(map[string]*metricpb.Metric)
	for _, resource := range request.GetResourceMetrics() {
		for _, scope := range resource.GetScopeMetrics() {
			for _, item := range scope.GetMetrics() {
				result[item.GetName()] = item
			}
		}
	}
	return result
}

func internalMetricStringAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, attr := range attrs {
		if attr.GetKey() == key {
			return attr.GetValue().GetStringValue()
		}
	}
	return ""
}

func internalMetricPointCount(item *metricpb.Metric) int {
	if item == nil {
		return 0
	}
	switch data := item.GetData().(type) {
	case *metricpb.Metric_Sum:
		return len(data.Sum.GetDataPoints())
	case *metricpb.Metric_Gauge:
		return len(data.Gauge.GetDataPoints())
	case *metricpb.Metric_Histogram:
		return len(data.Histogram.GetDataPoints())
	default:
		return 0
	}
}
