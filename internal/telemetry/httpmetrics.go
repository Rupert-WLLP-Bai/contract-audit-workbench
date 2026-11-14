// File metadata (manifest mirror):
// Purpose: Records bounded HTTP RED observations in official SDK counter and histogram instruments while preserving route, status and millisecond bucket semantics.
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
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// HTTP dimensions are deliberately bounded: methods use a fixed vocabulary,
// route templates are sanitized, and the allocation guard caps each instrument.
const httpSeriesCapacity = 256

// Explicit inclusive upper bounds in milliseconds. The SDK adds the +Inf bucket.
var httpDurationBounds = [...]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000}

type httpObservation struct {
	method     string
	route      string
	status     int
	durationMS int64
}

type httpCounterKey struct {
	method string
	route  string
	status int
}

func httpDurationKey(o httpObservation) [2]string {
	return [2]string{o.method, o.route}
}

func (e *Exporter) configureHTTPMetrics() error {
	meter := e.metricProvider.Meter("contractaudit.http")
	var err error
	e.httpRequests, err = meter.Int64Counter("audit.http.requests")
	if err != nil {
		return err
	}
	e.httpDurations, err = meter.Int64Histogram("audit.http.duration_ms", metric.WithUnit("ms"))
	return err
}

// observeHTTP records one completed exchange. It never waits for the collector;
// the bounded mutex only protects the finite per-process series allocation.
func (e *Exporter) observeHTTP(o httpObservation) {
	if e == nil || o.status < 100 || o.status > 599 || o.durationMS < 0 {
		return
	}
	if o.method = httpMethodAttribute(o.method); o.method == "" {
		return
	}
	if o.route = sanitizeRoute(o.route); o.route == "" {
		o.route = "unmatched"
	}

	e.httpMu.Lock()
	defer e.httpMu.Unlock()
	requestKey := httpCounterKey{method: o.method, route: o.route, status: o.status}
	if _, ok := e.httpRequestSet[requestKey]; !ok {
		if len(e.httpRequestSet) >= httpSeriesCapacity {
			e.httpDropped.Add(1)
			return
		}
		e.httpRequestSet[requestKey] = struct{}{}
	}
	durationKey := httpDurationKey(o)
	if _, ok := e.httpDurationSet[durationKey]; !ok {
		if len(e.httpDurationSet) >= httpSeriesCapacity {
			e.httpDropped.Add(1)
			return
		}
		e.httpDurationSet[durationKey] = struct{}{}
	}
	attrs := []attribute.KeyValue{
		attribute.String("http.request.method", o.method),
		attribute.String("http.route", o.route),
	}
	e.httpRequests.Add(context.Background(), 1, metric.WithAttributes(append(attrs,
		attribute.String("http.response.status_code", strconv.Itoa(o.status)),
	)...))
	e.httpDurations.Record(context.Background(), o.durationMS, metric.WithAttributes(attrs...))
}

// flushMetrics remains a small lifecycle hook for package consumers that need
// to request SDK collection. PeriodicReader owns the collection and export.
func (e *Exporter) flushMetrics(collectRuntime bool) {
	if e == nil || e.metricProvider == nil {
		return
	}
	if collectRuntime && e.runtime != nil {
		e.runtime.skip.Store(false)
	} else if e.runtime != nil {
		e.runtime.skip.Store(true)
	}
	if e.runtime != nil {
		defer e.runtime.skip.Store(false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	if err := e.metricProvider.ForceFlush(ctx); err != nil {
		e.metricFailed.Add(1)
	}
}
