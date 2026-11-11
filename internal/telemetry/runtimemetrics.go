// File metadata (manifest mirror):
// Purpose: RuntimeSnapshot is a bounded process-local pool snapshot and, only for the authorized worker, cross-tenant queue and outbox snapshots. Failure values are "role", "timeout",… Related `telemetry` operations include observe, metric, SetRuntimeMetricsSource, RecordQueueClaim.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-01
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package telemetry

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// RuntimeSnapshot is a bounded process-local pool snapshot and, only for the
// authorized worker, cross-tenant queue and outbox snapshots. Failure values
// are "role", "timeout", "database", or "not_started"; empty means success.
// QueueWaitAvailable is false when any queued job lacks a trustworthy PG
// readiness anchor; queue counts remain valid. OutboxAgeAvailable is false
// when a pending event has an invalid stored creation time. Outbox creation
// can use an application clock, so its age is subject to clock skew. Queue
// and outbox snapshots are repeated by workers and must not be summed.
type RuntimeSnapshot struct {
	PoolAvailable            bool
	PoolAcquired             int64
	PoolIdle                 int64
	PoolMax                  int64
	PoolAcquireCount         int64
	PoolEmptyAcquireCount    int64
	PoolCanceledAcquireCount int64
	PoolEmptyAcquireWaitMS   float64
	PoolAcquireDurationMS    float64

	QueueObserved      bool
	QueueFailure       string
	QueueWaitAvailable bool
	Queued             int64
	Ready              int64
	Running            int64
	OldestWaitMS       float64
	OldestReadyWaitMS  float64
	OutboxPending      int64
	OutboxFailure      string
	OutboxAgeAvailable bool
	OutboxOldestMS     float64

	CLMObserved              bool
	CLMAvailable             bool
	CLMFailure               string
	CLMPending               int64
	CLMPendingAgeAvailable   bool
	CLMOldestPendingMS       float64
	CLMApproved              CLMApprovalSnapshot
	CLMReturned              CLMApprovalSnapshot
	CLMWithdrawn             CLMApprovalSnapshot
	CLMTerminated            CLMApprovalSnapshot
	CLMAgedInReviewAvailable bool
	CLMAgedInReview          int64
}

// CLMApprovalSnapshot is a bounded, global summary for one fixed round outcome.
type CLMApprovalSnapshot struct {
	Count             int64
	DurationAvailable bool
	P50MS             float64
	P95MS             float64
}

// Millisecond bounds include long queues and executions; the last bucket is +Inf.
var queueDurationBounds = [...]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 120000, 300000, 900000, 1800000, 3600000}

type runtimeMetrics struct {
	owner *Exporter

	sourceMu sync.RWMutex
	source   func(context.Context) RuntimeSnapshot
	skip     atomic.Bool

	claims         metric.Int64Counter
	reclaims       metric.Int64Counter
	quotaReject    metric.Int64Counter
	queueWait      metric.Float64Histogram
	queueElapsed   metric.Float64Histogram
	queueFailure   metric.Int64ObservableCounter
	outboxFailure  metric.Int64ObservableCounter
	queueFailures  [4]atomic.Int64
	outboxFailures [4]atomic.Int64

	intGauge     map[string]metric.Int64ObservableGauge
	floatGauge   map[string]metric.Float64ObservableGauge
	intCounter   map[string]metric.Int64ObservableCounter
	floatCounter map[string]metric.Float64ObservableCounter
	registration metric.Registration
}

func (e *Exporter) configureRuntimeMetrics() error {
	meter := e.metricProvider.Meter("contractaudit.runtime")
	r := &runtimeMetrics{
		owner: e, intGauge: make(map[string]metric.Int64ObservableGauge),
		floatGauge:   make(map[string]metric.Float64ObservableGauge),
		intCounter:   make(map[string]metric.Int64ObservableCounter),
		floatCounter: make(map[string]metric.Float64ObservableCounter),
	}
	var err error
	if r.claims, err = meter.Int64Counter("audit.queue.claims"); err != nil {
		return err
	}
	if r.reclaims, err = meter.Int64Counter("audit.queue.reclaims"); err != nil {
		return err
	}
	if r.quotaReject, err = meter.Int64Counter("audit.quota.rejections"); err != nil {
		return err
	}
	if r.queueWait, err = meter.Float64Histogram("audit.queue.wait_duration_ms", metric.WithUnit("ms")); err != nil {
		return err
	}
	if r.queueElapsed, err = meter.Float64Histogram("audit.queue.execution_duration_ms", metric.WithUnit("ms")); err != nil {
		return err
	}
	if r.queueFailure, err = meter.Int64ObservableCounter("audit.queue.collection_failures"); err != nil {
		return err
	}
	if r.outboxFailure, err = meter.Int64ObservableCounter("audit.outbox.collection_failures"); err != nil {
		return err
	}

	observables := []metric.Observable{r.queueFailure, r.outboxFailure}
	for _, name := range []string{
		"audit.db.pool.acquired", "audit.db.pool.idle", "audit.db.pool.max",
		"audit.queue.available", "audit.outbox.available",
		"audit.queue.queued", "audit.queue.ready", "audit.queue.running",
		"audit.queue.wait_age_available", "audit.outbox.pending", "audit.outbox.age_available",
		"audit.clm.available", "audit.clm.outbox.pending", "audit.clm.outbox.pending_age_available",
		"audit.clm.approval_rounds.completed", "audit.clm.approval_rounds.duration_available",
		"audit.clm.approval_rounds.aged_in_review_available", "audit.clm.approval_rounds.aged_in_review",
	} {
		instrument, instrumentErr := meter.Int64ObservableGauge(name)
		if instrumentErr != nil {
			return instrumentErr
		}
		r.intGauge[name] = instrument
		observables = append(observables, instrument)
	}
	for _, name := range []string{
		"audit.queue.oldest_wait_ms", "audit.queue.oldest_ready_wait_ms",
		"audit.outbox.oldest_pending_ms", "audit.clm.outbox.oldest_pending_ms",
		"audit.clm.approval_rounds.duration_p50_ms", "audit.clm.approval_rounds.duration_p95_ms",
	} {
		instrument, instrumentErr := meter.Float64ObservableGauge(name, metric.WithUnit("ms"))
		if instrumentErr != nil {
			return instrumentErr
		}
		r.floatGauge[name] = instrument
		observables = append(observables, instrument)
	}
	for _, name := range []string{
		"audit.db.pool.acquires", "audit.db.pool.empty_acquires", "audit.db.pool.canceled_acquires",
	} {
		instrument, instrumentErr := meter.Int64ObservableCounter(name)
		if instrumentErr != nil {
			return instrumentErr
		}
		r.intCounter[name] = instrument
		observables = append(observables, instrument)
	}
	for _, name := range []string{"audit.db.pool.empty_acquire_wait_ms", "audit.db.pool.acquire_duration_ms"} {
		instrument, instrumentErr := meter.Float64ObservableCounter(name, metric.WithUnit("ms"))
		if instrumentErr != nil {
			return instrumentErr
		}
		r.floatCounter[name] = instrument
		observables = append(observables, instrument)
	}
	r.registration, err = meter.RegisterCallback(r.collect, observables...)
	if err != nil {
		return err
	}
	e.runtime = r
	return nil
}

func (e *Exporter) SetRuntimeMetricsSource(source func(context.Context) RuntimeSnapshot) {
	if e == nil || e.runtime == nil {
		return
	}
	e.runtime.sourceMu.Lock()
	e.runtime.source = source
	e.runtime.sourceMu.Unlock()
}

func (e *Exporter) RecordQueueClaim(reclaimed bool, wait time.Duration) {
	if e == nil || e.runtime == nil {
		return
	}
	e.runtime.claims.Add(context.Background(), 1)
	if reclaimed {
		e.runtime.reclaims.Add(context.Background(), 1)
	}
	if wait >= 0 {
		e.runtime.queueWait.Record(context.Background(), float64(wait)/float64(time.Millisecond))
	}
}

func (e *Exporter) RecordQueueCompletion(elapsed time.Duration) {
	if e == nil || e.runtime == nil || elapsed < 0 {
		return
	}
	e.runtime.queueElapsed.Record(context.Background(), float64(elapsed)/float64(time.Millisecond))
}

func (e *Exporter) RecordQuotaRejection(reason string) {
	if e == nil || e.runtime == nil {
		return
	}
	switch reason {
	case "queue_capacity", "model_budget", "tool_budget":
		e.runtime.quotaReject.Add(context.Background(), 1, metric.WithAttributes(attribute.String("reason", reason)))
	}
}

func (r *runtimeMetrics) collect(ctx context.Context, observer metric.Observer) error {
	if r.owner.closing.Load() || r.skip.Load() {
		return nil
	}
	r.sourceMu.RLock()
	source := r.source
	r.sourceMu.RUnlock()
	if source == nil {
		return nil
	}
	sampleCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	snapshot := source(sampleCtx)

	if snapshot.QueueObserved && snapshot.QueueFailure != "" {
		r.queueFailures[queueFailureIndex(snapshot.QueueFailure)].Add(1)
	}
	if snapshot.QueueObserved && snapshot.OutboxFailure != "" {
		r.outboxFailures[queueFailureIndex(snapshot.OutboxFailure)].Add(1)
	}

	if snapshot.PoolAvailable {
		r.observeInt(observer, "audit.db.pool.acquired", snapshot.PoolAcquired)
		r.observeInt(observer, "audit.db.pool.idle", snapshot.PoolIdle)
		r.observeInt(observer, "audit.db.pool.max", snapshot.PoolMax)
		r.observeIntCounter(observer, "audit.db.pool.acquires", snapshot.PoolAcquireCount)
		r.observeIntCounter(observer, "audit.db.pool.empty_acquires", snapshot.PoolEmptyAcquireCount)
		r.observeIntCounter(observer, "audit.db.pool.canceled_acquires", snapshot.PoolCanceledAcquireCount)
		r.observeFloatCounter(observer, "audit.db.pool.empty_acquire_wait_ms", snapshot.PoolEmptyAcquireWaitMS)
		r.observeFloatCounter(observer, "audit.db.pool.acquire_duration_ms", snapshot.PoolAcquireDurationMS)
	}
	if snapshot.QueueObserved {
		for i, reason := range [4]string{"role", "timeout", "database", "not_started"} {
			attrs := metric.WithAttributes(attribute.String("reason", reason))
			observer.ObserveInt64(r.queueFailure, r.queueFailures[i].Load(), attrs)
			observer.ObserveInt64(r.outboxFailure, r.outboxFailures[i].Load(), attrs)
		}
		queueUp, outboxUp := snapshot.QueueFailure == "", snapshot.OutboxFailure == ""
		r.observeInt(observer, "audit.queue.available", boolInt(queueUp))
		r.observeInt(observer, "audit.outbox.available", boolInt(outboxUp))
		if queueUp {
			r.observeInt(observer, "audit.queue.queued", snapshot.Queued)
			r.observeInt(observer, "audit.queue.ready", snapshot.Ready)
			r.observeInt(observer, "audit.queue.running", snapshot.Running)
			r.observeInt(observer, "audit.queue.wait_age_available", boolInt(snapshot.QueueWaitAvailable))
			if snapshot.QueueWaitAvailable {
				r.observeFloat(observer, "audit.queue.oldest_wait_ms", snapshot.OldestWaitMS)
				r.observeFloat(observer, "audit.queue.oldest_ready_wait_ms", snapshot.OldestReadyWaitMS)
			}
		}
		if outboxUp {
			r.observeInt(observer, "audit.outbox.pending", snapshot.OutboxPending)
			r.observeInt(observer, "audit.outbox.age_available", boolInt(snapshot.OutboxAgeAvailable))
			if snapshot.OutboxAgeAvailable {
				r.observeFloat(observer, "audit.outbox.oldest_pending_ms", snapshot.OutboxOldestMS)
			}
		}
	}
	if snapshot.CLMObserved {
		r.observeInt(observer, "audit.clm.available", boolInt(snapshot.CLMAvailable))
		if snapshot.CLMAvailable {
			r.observeInt(observer, "audit.clm.outbox.pending", snapshot.CLMPending)
			r.observeInt(observer, "audit.clm.outbox.pending_age_available", boolInt(snapshot.CLMPendingAgeAvailable))
			if snapshot.CLMPendingAgeAvailable {
				r.observeFloat(observer, "audit.clm.outbox.oldest_pending_ms", snapshot.CLMOldestPendingMS)
			}
			for _, outcome := range []struct {
				name    string
				latency CLMApprovalSnapshot
			}{
				{"approved", snapshot.CLMApproved}, {"returned", snapshot.CLMReturned},
				{"withdrawn", snapshot.CLMWithdrawn}, {"terminated", snapshot.CLMTerminated},
			} {
				attrs := metric.WithAttributes(attribute.String("outcome", outcome.name))
				r.observeIntWith(observer, "audit.clm.approval_rounds.completed", outcome.latency.Count, attrs)
				r.observeIntWith(observer, "audit.clm.approval_rounds.duration_available",
					boolInt(outcome.latency.DurationAvailable), attrs)
				if outcome.latency.DurationAvailable {
					r.observeFloatWith(observer, "audit.clm.approval_rounds.duration_p50_ms", outcome.latency.P50MS, attrs)
					r.observeFloatWith(observer, "audit.clm.approval_rounds.duration_p95_ms", outcome.latency.P95MS, attrs)
				}
			}
			r.observeInt(observer, "audit.clm.approval_rounds.aged_in_review_available",
				boolInt(snapshot.CLMAgedInReviewAvailable))
			if snapshot.CLMAgedInReviewAvailable {
				r.observeInt(observer, "audit.clm.approval_rounds.aged_in_review", snapshot.CLMAgedInReview)
			}
		}
	}
	return nil
}

func (r *runtimeMetrics) observeInt(observer metric.Observer, name string, value int64) {
	if instrument := r.intGauge[name]; instrument != nil {
		observer.ObserveInt64(instrument, value)
	}
}

func (r *runtimeMetrics) observeIntWith(observer metric.Observer, name string, value int64, options metric.ObserveOption) {
	if instrument := r.intGauge[name]; instrument != nil {
		observer.ObserveInt64(instrument, value, options)
	}
}

func (r *runtimeMetrics) observeFloat(observer metric.Observer, name string, value float64) {
	if instrument := r.floatGauge[name]; instrument != nil {
		observer.ObserveFloat64(instrument, value)
	}
}

func (r *runtimeMetrics) observeFloatWith(observer metric.Observer, name string, value float64, options metric.ObserveOption) {
	if instrument := r.floatGauge[name]; instrument != nil {
		observer.ObserveFloat64(instrument, value, options)
	}
}

func (r *runtimeMetrics) observeIntCounter(observer metric.Observer, name string, value int64) {
	if instrument := r.intCounter[name]; instrument != nil {
		observer.ObserveInt64(instrument, value)
	}
}

func (r *runtimeMetrics) observeFloatCounter(observer metric.Observer, name string, value float64) {
	if instrument := r.floatCounter[name]; instrument != nil {
		observer.ObserveFloat64(instrument, value)
	}
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func failureReason(reason string) string {
	switch reason {
	case "role", "timeout", "not_started":
		return reason
	default:
		return "database"
	}
}

func queueFailureIndex(reason string) int {
	switch failureReason(reason) {
	case "role":
		return 0
	case "timeout":
		return 1
	case "not_started":
		return 3
	default:
		return 2
	}
}
