package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimeCollectionFailureOmitsQueueValues(t *testing.T) {
	payloads := make(chan []byte, 3)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		payloads <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer collector.Close()
	exporter, err := New(Config{Endpoint: collector.URL, Service: "audit.worker", SampleRatio: 0})
	if err != nil {
		t.Fatal(err)
	}
	exporter.SetRuntimeMetricsSource(func(context.Context) RuntimeSnapshot {
		return RuntimeSnapshot{PoolAvailable: true, PoolEmptyAcquireCount: 3, PoolEmptyAcquireWaitMS: 12,
			PoolAcquireDurationMS: 40, QueueObserved: true, QueueFailure: "database", OutboxFailure: "database", Queued: 99, Ready: 98, OutboxPending: 97}
	})
	exporter.flushMetrics(true)
	exporter.SetRuntimeMetricsSource(func(context.Context) RuntimeSnapshot {
		return RuntimeSnapshot{QueueObserved: true, Queued: 4, Ready: 1, OutboxPending: 3}
	})
	exporter.flushMetrics(true)
	exporter.Close()
	var payload metricsRequest
	select {
	case body := <-payloads:
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("collector received no runtime metrics")
	}
	metrics := map[string]metric{}
	for _, scope := range payload.ResourceMetrics[0].ScopeMetrics {
		for _, item := range scope.Metrics {
			metrics[item.Name] = item
		}
	}
	for _, name := range []string{"audit.queue.available", "audit.outbox.available"} {
		item := metrics[name]
		if item.Gauge == nil || len(item.Gauge.DataPoints) != 1 || item.Gauge.DataPoints[0].AsInt != "0" {
			t.Fatalf("%s did not report unavailable: %+v", name, item)
		}
	}
	for _, name := range []string{"audit.queue.queued", "audit.queue.ready", "audit.queue.running", "audit.outbox.pending", "audit.outbox.oldest_pending_ms"} {
		if _, ok := metrics[name]; ok {
			t.Fatalf("failed queue collection emitted %s", name)
		}
	}
	empty := metrics["audit.db.pool.empty_acquires"].Sum
	wait := metrics["audit.db.pool.empty_acquire_wait_ms"].Sum
	acquire := metrics["audit.db.pool.acquire_duration_ms"].Sum
	if empty == nil || len(empty.DataPoints) != 1 || empty.DataPoints[0].AsInt != "3" ||
		wait == nil || len(wait.DataPoints) != 1 || wait.DataPoints[0].AsDouble == nil || *wait.DataPoints[0].AsDouble != 12 ||
		acquire == nil || len(acquire.DataPoints) != 1 || acquire.DataPoints[0].AsDouble == nil || *acquire.DataPoints[0].AsDouble != 40 {
		t.Fatalf("pool wait and acquire durations were not preserved: %+v %+v %+v", empty, wait, acquire)
	}
	failures := metrics["audit.queue.collection_failures"].Sum
	if failures == nil {
		t.Fatal("missing bounded collection failure counter")
	}
	found := false
	for _, point := range failures.DataPoints {
		if attributeValue(point.Attributes, "reason") == "database" && point.AsInt == "1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("database collection failure was not counted: %+v", failures.DataPoints)
	}
	var partial metricsRequest
	if err := json.Unmarshal(<-payloads, &partial); err != nil {
		t.Fatal(err)
	}
	metrics = map[string]metric{}
	for _, scope := range partial.ResourceMetrics[0].ScopeMetrics {
		for _, item := range scope.Metrics {
			metrics[item.Name] = item
		}
	}
	for name, want := range map[string]string{
		"audit.queue.available": "1", "audit.queue.queued": "4", "audit.queue.ready": "1",
		"audit.queue.wait_age_available": "0", "audit.outbox.available": "1",
		"audit.outbox.pending": "3", "audit.outbox.age_available": "0",
	} {
		item := metrics[name]
		if item.Gauge == nil || len(item.Gauge.DataPoints) != 1 || item.Gauge.DataPoints[0].AsInt != want {
			t.Fatalf("%s: expected %s, got %+v", name, want, item)
		}
	}
	for _, name := range []string{"audit.queue.oldest_wait_ms", "audit.queue.oldest_ready_wait_ms", "audit.outbox.oldest_pending_ms"} {
		if _, ok := metrics[name]; ok {
			t.Fatalf("missing age anchor emitted %s", name)
		}
	}
}
