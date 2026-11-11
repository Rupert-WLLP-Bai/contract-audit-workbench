// File metadata (manifest mirror):
// Purpose: Exercises the `telemetry` package behavior through TestHTTPHistogramCollectorPayload; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"

	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestHTTPHistogramUsesConfiguredSDKBuckets(t *testing.T) {
	requests := make(chan map[string]*metricpb.Metric, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- internalMetrics(decodeInternalMetricRequest(t, r.Body))
		writeInternalOTLPAck(w)
	}))
	defer collector.Close()
	exporter, err := New(Config{Endpoint: collector.URL, Service: "audit.edge", SampleRatio: 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, duration := range []int64{0, 1, 2, 3, 60001} {
		exporter.observeHTTP(httpObservation{method: "GET", route: "/health", status: 200, durationMS: duration})
	}
	exporter.Close()
	metrics := <-requests
	item := metrics["audit.http.duration_ms"]
	if item == nil || item.GetHistogram() == nil || item.GetUnit() != "ms" ||
		item.GetHistogram().GetAggregationTemporality() != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE ||
		len(item.GetHistogram().GetDataPoints()) != 1 {
		t.Fatalf("unexpected SDK histogram metadata: %+v", item)
	}
	point := item.GetHistogram().GetDataPoints()[0]
	counts := point.GetBucketCounts()
	if point.GetCount() != 5 || point.GetSum() != 60007 ||
		len(point.GetExplicitBounds()) != 15 || len(counts) != 16 ||
		point.GetExplicitBounds()[0] != 1 || point.GetExplicitBounds()[14] != 60000 ||
		counts[0] != 2 || counts[1] != 1 || counts[2] != 1 || counts[15] != 1 {
		t.Fatalf("incorrect histogram observations: %+v", point)
	}
	for _, count := range counts[3:15] {
		if count != 0 {
			t.Fatalf("unexpected intermediate bucket: %v", counts)
		}
	}
}
