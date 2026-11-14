// File metadata (manifest mirror):
// Purpose: Exercises the `telemetry` package behavior through TestRuntimeCollectionFailureOmitsQueueValues; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-12
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestRuntimeCollectionFailureOmitsQueueValues(t *testing.T) {
	requests := make(chan map[string]*metricpb.Metric, 3)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- internalMetrics(decodeInternalMetricRequest(t, r.Body))
		writeInternalOTLPAck(w)
	}))
	defer collector.Close()
	exporter, err := New(Config{Endpoint: collector.URL, Service: "audit.worker", SampleRatio: 0})
	if err != nil {
		t.Fatal(err)
	}
	exporter.SetRuntimeMetricsSource(func(context.Context) RuntimeSnapshot {
		return RuntimeSnapshot{
			PoolAvailable: true, PoolEmptyAcquireCount: 3, PoolEmptyAcquireWaitMS: 12,
			PoolAcquireDurationMS: 40, QueueObserved: true, QueueFailure: "database",
			OutboxFailure: "database", Queued: 99, Ready: 98, OutboxPending: 97,
		}
	})
	exporter.flushMetrics(true)
	exporter.SetRuntimeMetricsSource(func(context.Context) RuntimeSnapshot {
		return RuntimeSnapshot{QueueObserved: true, Queued: 4, Ready: 1, OutboxPending: 3}
	})
	exporter.flushMetrics(true)
	exporter.Close()

	first := <-requests
	for _, name := range []string{"audit.queue.available", "audit.outbox.available"} {
		points := first[name].GetGauge().GetDataPoints()
		if len(points) != 1 || points[0].GetAsInt() != 0 {
			t.Fatalf("%s did not report unavailable: %+v", name, points)
		}
	}
	for _, name := range []string{
		"audit.queue.queued", "audit.queue.ready", "audit.queue.running",
		"audit.outbox.pending", "audit.outbox.oldest_pending_ms",
	} {
		if first[name] != nil {
			t.Fatalf("failed queue collection emitted %s", name)
		}
	}
	empty := first["audit.db.pool.empty_acquires"].GetSum().GetDataPoints()
	wait := first["audit.db.pool.empty_acquire_wait_ms"].GetSum().GetDataPoints()
	acquire := first["audit.db.pool.acquire_duration_ms"].GetSum().GetDataPoints()
	if len(empty) != 1 || empty[0].GetAsInt() != 3 ||
		len(wait) != 1 || wait[0].GetAsDouble() != 12 ||
		len(acquire) != 1 || acquire[0].GetAsDouble() != 40 {
		t.Fatalf("pool wait and acquire durations were not preserved: %+v %+v %+v", empty, wait, acquire)
	}
	failures := first["audit.queue.collection_failures"].GetSum().GetDataPoints()
	found := false
	for _, point := range failures {
		if internalMetricStringAttr(point.GetAttributes(), "reason") == "database" && point.GetAsInt() == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("database collection failure was not counted: %+v", failures)
	}

	second := <-requests
	for name, want := range map[string]int64{
		"audit.queue.available": 1, "audit.queue.queued": 4, "audit.queue.ready": 1,
		"audit.queue.wait_age_available": 0, "audit.outbox.available": 1,
		"audit.outbox.pending": 3, "audit.outbox.age_available": 0,
	} {
		item := second[name]
		points := item.GetGauge().GetDataPoints()
		if len(points) != 1 || points[0].GetAsInt() != want {
			t.Fatalf("%s: expected %d, got %+v", name, want, item)
		}
	}
	for _, name := range []string{
		"audit.queue.oldest_wait_ms", "audit.queue.oldest_ready_wait_ms",
		"audit.outbox.oldest_pending_ms",
	} {
		if second[name] != nil {
			t.Fatalf("missing age anchor emitted %s", name)
		}
	}
}
