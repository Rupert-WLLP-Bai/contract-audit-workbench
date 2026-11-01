package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"contractaudit/internal/telemetry"
	"github.com/jackc/pgx/v5/pgconn"
)

const clmMetricsQueryTimeout = 2 * time.Second

// CLMApprovalLatency is a current scalar summary over retained completed
// approval rounds of one fixed outcome. Percentiles are available only when
// the sample is non-empty and all contributing intervals are valid.
type CLMApprovalLatency struct {
	Count             int64
	DurationAvailable bool
	P50MS             float64
	P95MS             float64
}

// CLMMetricsSnapshot contains global, low-cardinality operational scalars.
// It never carries tenant, contract, actor, or event payload data.
type CLMMetricsSnapshot struct {
	Available             bool
	Failure               string
	CapturedAt            time.Time
	Pending               int64
	PendingAgeAvailable   bool
	OldestPendingMS       float64
	Approved              CLMApprovalLatency
	Returned              CLMApprovalLatency
	Withdrawn             CLMApprovalLatency
	Terminated            CLMApprovalLatency
	AgedInReviewAvailable bool
	AgedInReview          int64
}

// RefreshCLMMetrics replaces the Store's process-local scalar snapshot from
// one fixed-dimension PostgreSQL aggregate. A nil threshold means the
// aged-in-review proxy is unknown. It does not mean zero age or zero blocked
// rounds. The database remains authoritative; wakeups only call this refresh.
func (s *Store) RefreshCLMMetrics(ctx context.Context, agedInReviewAfter *time.Duration) error {
	if s == nil || s.pool == nil || ctx == nil {
		return errors.New("CLM metrics require a database and context")
	}
	if agedInReviewAfter != nil && (*agedInReviewAfter <= 0 || *agedInReviewAfter > 365*24*time.Hour) {
		return errors.New("CLM aged-in-review threshold must be in (0, 365 days]")
	}
	queryCtx, cancel := context.WithTimeout(ctx, clmMetricsQueryTimeout)
	defer cancel()
	var snapshot CLMMetricsSnapshot
	var approved, returned, withdrawn, terminated clmApprovalMetricsRow
	var thresholdUS *int64
	var agedInReview *int64
	var oldestPendingMS *float64
	if agedInReviewAfter != nil {
		value := agedInReviewAfter.Microseconds()
		if value <= 0 {
			return errors.New("CLM aged-in-review threshold must be at least one microsecond")
		}
		thresholdUS = &value
	}
	err := s.pool.QueryRow(queryCtx, `SELECT captured_at,pending_count,pending_age_available,oldest_pending_ms,
		approved_count,approved_valid,approved_p50_ms,approved_p95_ms,
		returned_count,returned_valid,returned_p50_ms,returned_p95_ms,
		withdrawn_count,withdrawn_valid,withdrawn_p50_ms,withdrawn_p95_ms,
		terminated_count,terminated_valid,terminated_p50_ms,terminated_p95_ms,
		aged_in_review_count
		FROM clm_metrics_snapshot($1::bigint)`, thresholdUS).Scan(
		&snapshot.CapturedAt, &snapshot.Pending, &snapshot.PendingAgeAvailable, &oldestPendingMS,
		&approved.Count, &approved.Valid, &approved.P50, &approved.P95,
		&returned.Count, &returned.Valid, &returned.P50, &returned.P95,
		&withdrawn.Count, &withdrawn.Valid, &withdrawn.P50, &withdrawn.P95,
		&terminated.Count, &terminated.Valid, &terminated.P50, &terminated.P95,
		&agedInReview)
	if err != nil {
		failure := clmMetricsFailure(queryCtx, err)
		snapshot = CLMMetricsSnapshot{Failure: failure}
		s.clmMetrics.Store(&snapshot)
		return fmt.Errorf("refresh CLM metric snapshot: %w", err)
	}
	snapshot.Available = true
	snapshot.PendingAgeAvailable = snapshot.PendingAgeAvailable && snapshot.Pending > 0 && oldestPendingMS != nil
	if snapshot.PendingAgeAvailable {
		snapshot.OldestPendingMS = *oldestPendingMS
	}
	snapshot.Approved = approved.snapshot()
	snapshot.Returned = returned.snapshot()
	snapshot.Withdrawn = withdrawn.snapshot()
	snapshot.Terminated = terminated.snapshot()
	if agedInReview != nil {
		snapshot.AgedInReview = *agedInReview
		snapshot.AgedInReviewAvailable = thresholdUS != nil
	}
	s.clmMetrics.Store(&snapshot)
	return nil
}

// CLMMetricsSnapshot returns the last atomic global metric snapshot. Before
// the first successful refresh it reports not_started; it never performs I/O.
func (s *Store) CLMMetricsSnapshot() CLMMetricsSnapshot {
	if s == nil {
		return CLMMetricsSnapshot{Failure: "not_started"}
	}
	snapshot := s.clmMetrics.Load()
	if snapshot == nil {
		return CLMMetricsSnapshot{Failure: "not_started"}
	}
	return *snapshot
}

type clmApprovalMetricsRow struct {
	Count int64
	Valid bool
	P50   *float64
	P95   *float64
}

func (row clmApprovalMetricsRow) snapshot() CLMApprovalLatency {
	result := CLMApprovalLatency{Count: row.Count, DurationAvailable: row.Valid && row.Count > 0}
	if result.DurationAvailable && row.P50 != nil && row.P95 != nil {
		result.P50MS, result.P95MS = *row.P50, *row.P95
	} else {
		result.DurationAvailable = false
	}
	return result
}

func clmMetricsFailure(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "timeout"
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42501" {
		return "role"
	}
	return "database"
}

func (s *Store) applyCLMMetricsRuntimeSnapshot(out *telemetry.RuntimeSnapshot) {
	snapshot := s.CLMMetricsSnapshot()
	out.CLMObserved = true
	out.CLMAvailable = snapshot.Available
	out.CLMFailure = snapshot.Failure
	if !snapshot.Available {
		return
	}
	out.CLMPending = snapshot.Pending
	out.CLMPendingAgeAvailable = snapshot.PendingAgeAvailable
	out.CLMOldestPendingMS = snapshot.OldestPendingMS
	out.CLMApproved = telemetry.CLMApprovalSnapshot{Count: snapshot.Approved.Count, DurationAvailable: snapshot.Approved.DurationAvailable, P50MS: snapshot.Approved.P50MS, P95MS: snapshot.Approved.P95MS}
	out.CLMReturned = telemetry.CLMApprovalSnapshot{Count: snapshot.Returned.Count, DurationAvailable: snapshot.Returned.DurationAvailable, P50MS: snapshot.Returned.P50MS, P95MS: snapshot.Returned.P95MS}
	out.CLMWithdrawn = telemetry.CLMApprovalSnapshot{Count: snapshot.Withdrawn.Count, DurationAvailable: snapshot.Withdrawn.DurationAvailable, P50MS: snapshot.Withdrawn.P50MS, P95MS: snapshot.Withdrawn.P95MS}
	out.CLMTerminated = telemetry.CLMApprovalSnapshot{Count: snapshot.Terminated.Count, DurationAvailable: snapshot.Terminated.DurationAvailable, P50MS: snapshot.Terminated.P50MS, P95MS: snapshot.Terminated.P95MS}
	out.CLMAgedInReviewAvailable = snapshot.AgedInReviewAvailable
	out.CLMAgedInReview = snapshot.AgedInReview
}
