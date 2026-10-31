package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func loadJobs(ctx context.Context, q queryer) ([]Job, error) {
	rows, err := q.Query(ctx, `SELECT id, tenant_id, org_id, case_id, COALESCE(run_id,''), COALESCE(attempt_id,''), actor_id, kind, state, generation, worker_id, lease_until, heartbeat_at, created_at, updated_at FROM worker_jobs ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var item Job
		var lease, heartbeat pgtype.Timestamptz
		if err := rows.Scan(&item.ID, &item.TenantID, &item.OrgID, &item.CaseID, &item.RunID, &item.AttemptID, &item.ActorID,
			&item.Kind, &item.State, &item.Generation, &item.WorkerID, &lease, &heartbeat, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if lease.Valid {
			item.LeaseUntil = &lease.Time
		}
		if heartbeat.Valid {
			item.Heartbeat = &heartbeat.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadOutbox(ctx context.Context, q queryer) ([]OutboxRecord, error) {
	rows, err := q.Query(ctx, `SELECT id, job_id, tenant_id, org_id, case_id, event_type, payload, state, created_at, delivered_at FROM outbox_events ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxRecord
	for rows.Next() {
		var item OutboxRecord
		if err := rows.Scan(&item.ID, &item.JobID, &item.TenantID, &item.OrgID, &item.CaseID, &item.EventType, &item.Payload, &item.State, &item.CreatedAt, &item.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func syncJobs(ctx context.Context, tx pgx.Tx, items []Job) error {
	for _, item := range items {
		tag, err := tx.Exec(ctx, `INSERT INTO worker_jobs
			(id, tenant_id, org_id, case_id, run_id, attempt_id, actor_id, kind, state, generation, worker_id, lease_until, heartbeat_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			ON CONFLICT (id) DO UPDATE SET state=EXCLUDED.state, generation=EXCLUDED.generation, worker_id=EXCLUDED.worker_id,
			lease_until=EXCLUDED.lease_until, heartbeat_at=EXCLUDED.heartbeat_at, updated_at=EXCLUDED.updated_at
			WHERE worker_jobs.generation=EXCLUDED.generation AND worker_jobs.worker_id IS NOT DISTINCT FROM EXCLUDED.worker_id`,
			item.ID, item.TenantID, item.OrgID, item.CaseID, item.RunID, item.AttemptID, item.ActorID, item.Kind, item.State,
			item.Generation, item.WorkerID, item.LeaseUntil, item.Heartbeat, item.CreatedAt, item.UpdatedAt)
		if err != nil {
			return err
		}
		// The conditional update is the generation and worker-identity CAS for an
		// existing job. Zero rows means the row was claimed, re-leased, or
		// cancelled by another process after this snapshot was read; failing the
		// whole transaction keeps in-memory state from diverging from the
		// authoritative row instead of silently reporting success.
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("%w: worker job %q generation or worker identity changed", errLeaseLost, item.ID)
		}
	}
	return nil
}

func syncOutbox(ctx context.Context, tx pgx.Tx, items []OutboxRecord) error {
	for _, item := range items {
		if !json.Valid([]byte(item.Payload)) {
			return errInvalidOutboxPayload
		}
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_events
			(id, job_id, tenant_id, org_id, case_id, event_type, payload, state, created_at, delivered_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			item.ID, item.JobID, item.TenantID, item.OrgID, item.CaseID, item.EventType, item.Payload, item.State, item.CreatedAt, item.DeliveredAt); err != nil {
			return err
		}
	}
	return nil
}

var errInvalidOutboxPayload = errors.New("outbox payload must be one JSON value")

func hasJobForRun(st state, runID string) bool {
	for _, job := range st.Jobs {
		if job.RunID == runID {
			return true
		}
	}
	return false
}

// ErrLeaseLost means a worker lease was superseded or expired.
var ErrLeaseLost = errors.New("worker job lease was lost")

var errLeaseLost = ErrLeaseLost

func (s *Store) claimForAttempt(attemptID string) (JobClaim, bool) {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	claim, ok := s.claims[attemptID]
	return claim, ok
}

func (s *Store) setClaim(claim JobClaim) {
	if claim.AttemptID == "" {
		return
	}
	s.claimMu.Lock()
	s.claims[claim.AttemptID] = claim
	s.claimMu.Unlock()
}

func (s *Store) clearClaim(claim JobClaim) {
	if claim.AttemptID == "" {
		return
	}
	s.claimMu.Lock()
	if current, ok := s.claims[claim.AttemptID]; ok && current.Generation == claim.Generation && current.WorkerID == claim.WorkerID {
		delete(s.claims, claim.AttemptID)
	}
	s.claimMu.Unlock()
}

func (s *Store) ClaimNextJob(ctx context.Context, workerID string, lease time.Duration) (JobClaim, error) {
	if workerID == "" || lease <= 0 {
		return JobClaim{}, errors.New("worker id and positive lease are required")
	}
	// Read-only pre-check: an idle poll must not open the claim transaction,
	// which takes the queue advisory lock and locks tenant rows even when no
	// work exists. The scheduler predicates stay authoritative — a missed
	// pre-check only costs one claim attempt, never a lost job.
	if work, err := s.hasClaimableJob(ctx, workerID); err != nil {
		return JobClaim{}, err
	} else if !work {
		return JobClaim{}, pgx.ErrNoRows
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return JobClaim{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalQueueScope(ctx, tx, workerID); err != nil {
		return JobClaim{}, err
	}
	claim, err := claimScheduledJob(ctx, tx, workerID, lease, s.executionQueuePolicy())
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return JobClaim{}, commitErr
		}
		return JobClaim{}, pgx.ErrNoRows
	}
	if err != nil {
		return JobClaim{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return JobClaim{}, err
	}
	s.setClaim(claim)
	return claim, nil
}

func (s *Store) HeartbeatJob(ctx context.Context, claim JobClaim, lease time.Duration) (JobClaim, error) {
	if lease <= 0 {
		return JobClaim{}, errors.New("positive lease is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return JobClaim{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, claim.scope()); err != nil {
		return JobClaim{}, err
	}
	var expiry pgtype.Timestamptz
	err = tx.QueryRow(ctx, `UPDATE worker_jobs SET
		ready_at=clock_timestamp()+($7::bigint*interval '1 microsecond')+greatest(ready_at-lease_until,interval '0'),
		lease_until=clock_timestamp()+($7::bigint*interval '1 microsecond'), heartbeat_at=clock_timestamp(), updated_at=$8
		WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND attempt_id IS NOT DISTINCT FROM NULLIF($4,'') AND worker_id=$5 AND generation=$6 AND state='running' AND lease_until > clock_timestamp()
		RETURNING lease_until`, claim.JobID, claim.TenantID, claim.OrgID, claim.AttemptID, claim.WorkerID, claim.Generation, lease.Microseconds(), stamp()).Scan(&expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobClaim{}, errLeaseLost
	}
	if err != nil {
		return JobClaim{}, err
	}
	if !expiry.Valid {
		return JobClaim{}, errLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return JobClaim{}, err
	}
	claim.LeaseUntil = expiry.Time
	s.setClaim(claim)
	return claim, nil
}

func (s *Store) RunClaimedJob(ctx context.Context, claim JobClaim) error {
	if claim.JobID == "" || claim.WorkerID == "" || claim.Generation <= 0 {
		return errors.New("incomplete worker claim")
	}
	if claim.Kind == "experience_distill" {
		if claim.RunID != "" || claim.AttemptID != "" {
			return errors.New("experience task cannot use a review attempt identity")
		}
		if claim.stopReason != "" {
			// A claim can be terminal because its deadline or attempts were exhausted.
			// Give it a bounded window to reconcile an already accepted result or
			// persist the terminal failure under its current lease.
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
		} else if !claim.deadline.IsZero() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, claim.deadline)
			defer cancel()
		}
		return s.runExperienceDistill(ctx, claim)
	}
	if claim.Kind != "review" || claim.RunID == "" || claim.AttemptID == "" {
		return errors.New("unsupported or incomplete worker job")
	}
	ctx = withExecutionClaim(ctx, claim)
	s.setClaim(claim)
	defer s.clearClaim(claim)
	if claim.stopReason != "" {
		settleCtx, cancel := context.WithTimeout(ctx, attemptFinalizeTimeout)
		err := s.finishAttempt(settleCtx, claim.RunID, claim.AttemptID, "failed", "", claim.stopReason)
		cancel()
		if err != nil && !errors.Is(err, errAttemptTerminal) {
			return err
		}
	} else {
		executionContext := ctx
		if !claim.deadline.IsZero() {
			var cancel context.CancelFunc
			executionContext, cancel = context.WithDeadline(ctx, claim.deadline)
			defer cancel()
		}
		s.executeAttemptContext(executionContext, claim.RunID, claim.AttemptID, s.currentModels())
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	finishCtx, cancel := context.WithTimeout(ctx, attemptFinalizeTimeout)
	defer cancel()
	st, err := s.snapshotScoped(finishCtx, claim.scope())
	if err != nil {
		return err
	}
	var attempt Attempt
	found := false
	for _, item := range st.Attempts {
		if item.ID == claim.AttemptID && item.RunID == claim.RunID {
			attempt, found = item, true
			break
		}
	}
	if !found {
		return errAttemptMissing
	}
	if attemptActive(attempt.State) {
		return errors.New("worker execution returned before the attempt became terminal")
	}
	if attempt.State != "completed" && attempt.State != "partial" && attempt.State != "failed" && attempt.State != "cancelled" {
		return errors.New("attempt has an unsupported terminal state")
	}
	return s.finishClaim(finishCtx, claim, attempt.State)
}

func (s *Store) finishClaim(ctx context.Context, claim JobClaim, state string) error {
	if state != "completed" && state != "partial" && state != "failed" && state != "cancelled" {
		return errors.New("invalid terminal job state")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, claim.scope()); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE worker_jobs SET state=$7, lease_until=NULL, updated_at=$8
		WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND attempt_id IS NOT DISTINCT FROM NULLIF($4,'') AND worker_id=$5 AND generation=$6 AND state='running' AND lease_until > clock_timestamp()`,
		claim.JobID, claim.TenantID, claim.OrgID, claim.AttemptID, claim.WorkerID, claim.Generation, state, stamp())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errLeaseLost
	}
	return tx.Commit(ctx)
}
