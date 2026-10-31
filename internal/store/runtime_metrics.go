package store

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/telemetry"
	"github.com/jackc/pgx/v5"
)

// runtimeMetrics is called by the existing exporter loop, never by a request.
// Only the worker process can collect global queue metadata: it must have both
// its real claim-loop identity and the database role validated for execution.
func (s *Store) runtimeMetrics(ctx context.Context) telemetry.RuntimeSnapshot {
	var result telemetry.RuntimeSnapshot
	if s.pool == nil {
		return result
	}
	stats := s.pool.Stat()
	result.PoolAvailable = true
	result.PoolAcquired = int64(stats.AcquiredConns())
	result.PoolIdle = int64(stats.IdleConns())
	result.PoolMax = int64(stats.MaxConns())
	result.PoolAcquireCount = stats.AcquireCount()
	result.PoolEmptyAcquireCount = stats.EmptyAcquireCount()
	result.PoolCanceledAcquireCount = stats.CanceledAcquireCount()
	// AcquireDuration includes all successful acquisitions, including connection
	// construction. EmptyAcquireWaitTime is the actual empty-pool wait duration.
	result.PoolAcquireDurationMS = float64(stats.AcquireDuration()) / float64(time.Millisecond)
	result.PoolEmptyAcquireWaitMS = float64(stats.EmptyAcquireWaitTime()) / float64(time.Millisecond)
	if s.serviceOwner != "worker" {
		return result
	}
	result.QueueObserved = true
	workerID, ok := s.metricWorkerID.Load().(string)
	if !ok {
		result.QueueFailure, result.OutboxFailure = "not_started", "not_started"
		return result
	}
	result.QueueFailure = runtimeCollectionFailure(ctx, s.readRuntimeQueue(ctx, workerID, &result))
	result.OutboxFailure = runtimeCollectionFailure(ctx, s.readRuntimeOutbox(ctx, workerID, &result))
	return result
}

var errRuntimeRole = errors.New("worker execution database role unavailable")

func runtimeCollectionFailure(ctx context.Context, err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errRuntimeRole):
		return "role"
	case ctx.Err() != nil:
		return "timeout"
	default:
		return "database"
	}
}

// parseQueuePGTime is used only for experience tasks, whose existing enqueue
// function records clock_timestamp()::text inside PostgreSQL. A nonstandard or
// invalid encoding loses just the observation, never the claim.
func parseQueuePGTime(value string) (time.Time, bool) {
	for _, layout := range [...]string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999Z07", "2006-01-02 15:04:05.999999999Z0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func (s *Store) beginRuntimeQueueRead(ctx context.Context, workerID string) (pgx.Tx, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	var executionRole bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM service_database_roles WHERE role_name=current_user AND service='execution')`).Scan(&executionRole); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	if !executionRole {
		_ = tx.Rollback(ctx)
		return nil, errRuntimeRole
	}
	if err := setLocalQueueScope(ctx, tx, workerID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func (s *Store) readRuntimeQueue(ctx context.Context, workerID string, out *telemetry.RuntimeSnapshot) error {
	tx, err := s.beginRuntimeQueueRead(ctx, workerID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	cfg := s.executionQueuePolicy()
	// This snapshot uses the scheduler's job eligibility and active-lease
	// predicates. Ready is capped by remaining tenant and global slots. Locks
	// and concurrent commits can change the result before an actual claim.
	// Legacy queued reviews with an epoch ready_at lack a PG-clock wait anchor;
	// count them normally, but do not report a partial maximum as the oldest age.
	err = tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS now),
		jobs AS MATERIALIZED (
			SELECT tenant_id,state,ready_at,lease_until,deadline_at,
				CASE WHEN state='queued' AND ready_at>to_timestamp(0) AND isfinite(ready_at) THEN ready_at
					WHEN state='queued' AND ready_at=to_timestamp(0) AND kind='experience_distill' AND pg_input_is_valid(created_at,'timestamp with time zone')
						THEN CASE WHEN isfinite(created_at::timestamptz) THEN created_at::timestamptz END
					WHEN state='running' THEN GREATEST(lease_until,LEAST(ready_at,deadline_at)) END AS wait_since
			FROM worker_jobs WHERE state IN ('queued','running')
		), tenant_stats AS (
			SELECT tenant_id,
				count(*) FILTER (WHERE state='queued') AS queued,
				count(*) FILTER (WHERE state='running' AND lease_until>instant.now) AS running,
				count(*) FILTER (WHERE (state='queued' AND ready_at<=instant.now) OR
					(state='running' AND lease_until<=instant.now AND (ready_at<=instant.now OR deadline_at<=instant.now))) AS eligible,
				count(*) FILTER (WHERE state='queued' AND wait_since IS NULL) AS missing_wait,
				min(wait_since) FILTER (WHERE state='queued' OR (state='running' AND lease_until<=instant.now)) AS oldest_queued,
				min(wait_since) FILTER (WHERE (state='queued' AND ready_at<=instant.now) OR
					(state='running' AND lease_until<=instant.now AND (ready_at<=instant.now OR deadline_at<=instant.now))) AS oldest_ready
			FROM jobs CROSS JOIN instant GROUP BY tenant_id
		), totals AS (
			SELECT COALESCE(sum(queued),0)::bigint AS queued,
				COALESCE(sum(running),0)::bigint AS running,
				COALESCE(sum(LEAST(eligible,GREATEST($2::bigint-running,0))),0)::bigint AS eligible,
				COALESCE(sum(missing_wait),0)=0 AS wait_available,
				min(oldest_queued) AS oldest_queued,
				min(oldest_ready) FILTER (WHERE running<$2::bigint) AS oldest_ready
			FROM tenant_stats
		), policy AS (
			SELECT EXISTS (SELECT 1 FROM execution_queue_cursor WHERE tenant_id='__scheduler__' AND policy_hash<>$3)
				AND EXISTS (SELECT 1 FROM jobs) AS mismatch
		)
		SELECT totals.queued,totals.running,
			CASE WHEN policy.mismatch THEN 0 ELSE LEAST(totals.eligible,GREATEST($1::bigint-totals.running,0)) END,
			totals.wait_available,
			COALESCE(GREATEST(EXTRACT(EPOCH FROM (instant.now-totals.oldest_queued))*1000,0),0)::double precision,
			CASE WHEN policy.mismatch OR totals.running >= $1::bigint OR totals.eligible=0 THEN 0
				ELSE COALESCE(GREATEST(EXTRACT(EPOCH FROM (instant.now-totals.oldest_ready))*1000,0),0) END::double precision
		FROM totals CROSS JOIN policy CROSS JOIN instant`, cfg.GlobalInFlight, cfg.TenantInFlight, cfg.hash).
		Scan(&out.Queued, &out.Running, &out.Ready, &out.QueueWaitAvailable, &out.OldestWaitMS, &out.OldestReadyWaitMS)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) readRuntimeOutbox(ctx context.Context, workerID string, out *telemetry.RuntimeSnapshot) error {
	tx, err := s.beginRuntimeQueueRead(ctx, workerID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Existing outbox rows store their creation timestamp as text. Invalid dates
	// make age unavailable without hiding the authoritative pending count.
	// Earlier review events may carry an application-clock timestamp; this age
	// therefore reflects the recorded timestamp and can include clock skew.
	err = tx.QueryRow(ctx, `WITH pending AS MATERIALIZED (
		SELECT CASE WHEN pg_input_is_valid(created_at,'timestamp with time zone')
			THEN CASE WHEN isfinite(created_at::timestamptz) THEN created_at::timestamptz END END AS created
		FROM outbox_events WHERE state='pending'
	)
	SELECT count(*),count(*) FILTER (WHERE created IS NULL)=0,
		COALESCE(GREATEST(EXTRACT(EPOCH FROM (clock_timestamp()-min(created)))*1000,0),0)::double precision
	FROM pending`).Scan(&out.OutboxPending, &out.OutboxAgeAvailable, &out.OutboxOldestMS)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
