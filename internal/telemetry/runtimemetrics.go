package telemetry

import (
	"context"
	"strconv"
	"time"
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

type runtimeHistogram struct {
	count   uint64
	sum     float64
	buckets [len(queueDurationBounds) + 1]uint64
}

func (h *runtimeHistogram) observe(value time.Duration) {
	if value < 0 {
		return
	}
	milliseconds := float64(value) / float64(time.Millisecond)
	h.count++
	h.sum += milliseconds
	bucket := 0
	for bucket < len(queueDurationBounds) && milliseconds > queueDurationBounds[bucket] {
		bucket++
	}
	h.buckets[bucket]++
}

func (h *runtimeHistogram) metric(name, start, now string) metric {
	buckets := make([]string, len(h.buckets))
	for i, count := range h.buckets {
		buckets[i] = strconv.FormatUint(count, 10)
	}
	return metric{Name: name, Unit: "ms", Histogram: &histogramMetric{AggregationTemporality: 2, DataPoints: []histogramPoint{{
		Start: start, Time: now, Count: strconv.FormatUint(h.count, 10), Sum: h.sum,
		BucketCounts: buckets, ExplicitBounds: queueDurationBounds[:],
	}}}}
}

// SetRuntimeMetricsSource installs the sole runtime sampler. It is called only
// from the exporter's periodic delivery loop, with a short, cancellable context.
// Install it during assembly, before serving traffic.
func (e *Exporter) SetRuntimeMetricsSource(source func(context.Context) RuntimeSnapshot) {
	if e == nil {
		return
	}
	e.runtimeMu.Lock()
	e.runtimeSource = source
	e.runtimeMu.Unlock()
}

// RecordQueueClaim counts a committed claim and its PostgreSQL-clock wait.
// Negative waits are omitted when the source timestamp cannot be established.
// A reclaim is also a claim.
// The interval can include capacity delays and does not assert that the job
// stayed claimable throughout the interval.
func (e *Exporter) RecordQueueClaim(reclaimed bool, wait time.Duration) {
	if e == nil {
		return
	}
	e.runtimeMu.Lock()
	e.queueClaims.Add(1)
	if reclaimed {
		e.queueReclaims.Add(1)
	}
	e.queueWait.observe(wait)
	e.runtimeMu.Unlock()
}

// RecordQueueCompletion records a committed terminal transition's elapsed
// monotonic wall time, measured only in the process that held the claim.
func (e *Exporter) RecordQueueCompletion(elapsed time.Duration) {
	if e == nil {
		return
	}
	e.runtimeMu.Lock()
	e.queueExecution.observe(elapsed)
	e.runtimeMu.Unlock()
}

// RecordQuotaRejection accepts only the fixed rejection categories below.
func (e *Exporter) RecordQuotaRejection(reason string) {
	if e == nil {
		return
	}
	switch reason {
	case "queue_capacity":
		e.quotaQueue.Add(1)
	case "model_budget":
		e.quotaModel.Add(1)
	case "tool_budget":
		e.quotaTool.Add(1)
	}
}

func (e *Exporter) collectRuntimeMetrics(collect bool) []metric {
	e.runtimeMu.Lock()
	source := e.runtimeSource
	e.runtimeMu.Unlock()
	if source == nil {
		return nil
	}

	var snapshot RuntimeSnapshot
	if collect {
		ctx, cancel := context.WithTimeout(e.sampleCtx, time.Second)
		snapshot = source(ctx)
		cancel()
		if snapshot.QueueObserved && snapshot.QueueFailure != "" {
			e.queueErrors[queueFailureIndex(snapshot.QueueFailure)].Add(1)
		}
		if snapshot.QueueObserved && snapshot.OutboxFailure != "" {
			e.outboxErrors[queueFailureIndex(snapshot.OutboxFailure)].Add(1)
		}
	}
	e.runtimeMu.Lock()
	claims, reclaims := e.queueClaims.Load(), e.queueReclaims.Load()
	wait, execution := e.queueWait, e.queueExecution
	e.runtimeMu.Unlock()
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	counter := func(name string, value uint64) metric {
		return metric{Name: name, Sum: &sumMetric{AggregationTemporality: 2, IsMonotonic: true,
			DataPoints: []numberPoint{{Start: e.metricStart, Time: now, AsInt: strconv.FormatUint(value, 10)}}}}
	}
	counterPoint := func(reason string, value uint64) numberPoint {
		return numberPoint{Attributes: []attribute{stringAttribute("reason", reason)}, Start: e.metricStart,
			Time: now, AsInt: strconv.FormatUint(value, 10)}
	}
	metrics := []metric{
		counter("audit.queue.claims", claims),
		counter("audit.queue.reclaims", reclaims),
		{Name: "audit.quota.rejections", Sum: &sumMetric{AggregationTemporality: 2, IsMonotonic: true, DataPoints: []numberPoint{
			counterPoint("queue_capacity", e.quotaQueue.Load()),
			counterPoint("model_budget", e.quotaModel.Load()),
			counterPoint("tool_budget", e.quotaTool.Load()),
		}}},
	}
	if wait.count > 0 {
		metrics = append(metrics, wait.metric("audit.queue.wait_duration_ms", e.metricStart, now))
	}
	if execution.count > 0 {
		metrics = append(metrics, execution.metric("audit.queue.execution_duration_ms", e.metricStart, now))
	}
	if !collect {
		return metrics
	}
	gaugeInt := func(name string, value int64) metric {
		return metric{Name: name, Gauge: &gaugeMetric{DataPoints: []numberPoint{{Time: now, AsInt: strconv.FormatInt(value, 10)}}}}
	}
	gaugeDuration := func(name string, value float64) metric {
		return metric{Name: name, Unit: "ms", Gauge: &gaugeMetric{DataPoints: []numberPoint{{Time: now, AsDouble: &value}}}}
	}
	durationCounter := func(name string, value float64) metric {
		return metric{Name: name, Unit: "ms", Sum: &sumMetric{AggregationTemporality: 2, IsMonotonic: true,
			DataPoints: []numberPoint{{Start: e.metricStart, Time: now, AsDouble: &value}}}}
	}
	if snapshot.PoolAvailable {
		metrics = append(metrics,
			gaugeInt("audit.db.pool.acquired", snapshot.PoolAcquired),
			gaugeInt("audit.db.pool.idle", snapshot.PoolIdle),
			gaugeInt("audit.db.pool.max", snapshot.PoolMax),
			counter("audit.db.pool.acquires", uint64(snapshot.PoolAcquireCount)),
			counter("audit.db.pool.empty_acquires", uint64(snapshot.PoolEmptyAcquireCount)),
			counter("audit.db.pool.canceled_acquires", uint64(snapshot.PoolCanceledAcquireCount)),
			durationCounter("audit.db.pool.empty_acquire_wait_ms", snapshot.PoolEmptyAcquireWaitMS),
			durationCounter("audit.db.pool.acquire_duration_ms", snapshot.PoolAcquireDurationMS),
		)
	}
	if snapshot.QueueObserved {
		availability := func(ok bool) int64 {
			if ok {
				return 1
			}
			return 0
		}
		failures := func(name string, role, timeout, database, notStarted uint64) metric {
			return metric{Name: name, Sum: &sumMetric{AggregationTemporality: 2, IsMonotonic: true, DataPoints: []numberPoint{
				counterPoint("role", role), counterPoint("timeout", timeout),
				counterPoint("database", database), counterPoint("not_started", notStarted),
			}}}
		}
		queueUp, outboxUp := snapshot.QueueFailure == "", snapshot.OutboxFailure == ""
		metrics = append(metrics,
			gaugeInt("audit.queue.available", availability(queueUp)),
			gaugeInt("audit.outbox.available", availability(outboxUp)),
			failures("audit.queue.collection_failures", e.queueErrors[0].Load(), e.queueErrors[1].Load(), e.queueErrors[2].Load(), e.queueErrors[3].Load()),
			failures("audit.outbox.collection_failures", e.outboxErrors[0].Load(), e.outboxErrors[1].Load(), e.outboxErrors[2].Load(), e.outboxErrors[3].Load()),
		)
		if queueUp {
			metrics = append(metrics,
				gaugeInt("audit.queue.queued", snapshot.Queued),
				gaugeInt("audit.queue.ready", snapshot.Ready),
				gaugeInt("audit.queue.running", snapshot.Running),
				gaugeInt("audit.queue.wait_age_available", availability(snapshot.QueueWaitAvailable)),
			)
			if snapshot.QueueWaitAvailable {
				metrics = append(metrics,
					gaugeDuration("audit.queue.oldest_wait_ms", snapshot.OldestWaitMS),
					gaugeDuration("audit.queue.oldest_ready_wait_ms", snapshot.OldestReadyWaitMS),
				)
			}
		}
		if outboxUp {
			metrics = append(metrics,
				gaugeInt("audit.outbox.pending", snapshot.OutboxPending),
				gaugeInt("audit.outbox.age_available", availability(snapshot.OutboxAgeAvailable)),
			)
			if snapshot.OutboxAgeAvailable {
				metrics = append(metrics, gaugeDuration("audit.outbox.oldest_pending_ms", snapshot.OutboxOldestMS))
			}
		}
	}
	if snapshot.CLMObserved {
		available := int64(0)
		if snapshot.CLMAvailable {
			available = 1
		}
		metrics = append(metrics, gaugeInt("audit.clm.available", available))
		if snapshot.CLMAvailable {
			ageAvailable := int64(0)
			if snapshot.CLMPendingAgeAvailable {
				ageAvailable = 1
			}
			metrics = append(metrics,
				gaugeInt("audit.clm.outbox.pending", snapshot.CLMPending),
				gaugeInt("audit.clm.outbox.pending_age_available", ageAvailable),
			)
			if snapshot.CLMPendingAgeAvailable {
				metrics = append(metrics, gaugeDuration("audit.clm.outbox.oldest_pending_ms", snapshot.CLMOldestPendingMS))
			}
			for _, outcome := range []struct {
				name    string
				latency CLMApprovalSnapshot
			}{
				{"approved", snapshot.CLMApproved},
				{"returned", snapshot.CLMReturned},
				{"withdrawn", snapshot.CLMWithdrawn},
				{"terminated", snapshot.CLMTerminated},
			} {
				outcomeCount := numberPoint{Attributes: []attribute{stringAttribute("outcome", outcome.name)}, Time: now,
					AsInt: strconv.FormatInt(outcome.latency.Count, 10)}
				durationAvailable := int64(0)
				if outcome.latency.DurationAvailable {
					durationAvailable = 1
				}
				availabilityPoint := numberPoint{Attributes: []attribute{stringAttribute("outcome", outcome.name)}, Time: now,
					AsInt: strconv.FormatInt(durationAvailable, 10)}
				metrics = append(metrics,
					metric{Name: "audit.clm.approval_rounds.completed", Gauge: &gaugeMetric{DataPoints: []numberPoint{outcomeCount}}},
					metric{Name: "audit.clm.approval_rounds.duration_available", Gauge: &gaugeMetric{DataPoints: []numberPoint{availabilityPoint}}},
				)
				if outcome.latency.DurationAvailable {
					p50, p95 := outcome.latency.P50MS, outcome.latency.P95MS
					metrics = append(metrics,
						metric{Name: "audit.clm.approval_rounds.duration_p50_ms", Unit: "ms", Gauge: &gaugeMetric{DataPoints: []numberPoint{{Attributes: []attribute{stringAttribute("outcome", outcome.name)}, Time: now, AsDouble: &p50}}}},
						metric{Name: "audit.clm.approval_rounds.duration_p95_ms", Unit: "ms", Gauge: &gaugeMetric{DataPoints: []numberPoint{{Attributes: []attribute{stringAttribute("outcome", outcome.name)}, Time: now, AsDouble: &p95}}}},
					)
				}
			}
			agedAvailable := int64(0)
			if snapshot.CLMAgedInReviewAvailable {
				agedAvailable = 1
			}
			metrics = append(metrics, gaugeInt("audit.clm.approval_rounds.aged_in_review_available", agedAvailable))
			if snapshot.CLMAgedInReviewAvailable {
				metrics = append(metrics, gaugeInt("audit.clm.approval_rounds.aged_in_review", snapshot.CLMAgedInReview))
			}
		}
	}
	return metrics
}

func queueFailureIndex(reason string) int {
	switch reason {
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
