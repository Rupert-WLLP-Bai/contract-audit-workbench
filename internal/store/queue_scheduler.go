package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// claimScheduledJob runs under the deployment queue lock. The job lease itself
// reserves one execution slot; expired leases cannot renew or commit results.
func claimScheduledJob(ctx context.Context, tx pgx.Tx, worker string, lease time.Duration, cfg *queueConfiguration) (JobClaim, error) {
	cursor, err := lockQueue(ctx, tx, cfg)
	if err != nil {
		return JobClaim{}, err
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM worker_jobs WHERE state='running' AND lease_until>clock_timestamp()`).Scan(&active); err != nil {
		return JobClaim{}, err
	}
	if active >= cfg.GlobalInFlight {
		return JobClaim{}, pgx.ErrNoRows
	}
	// Additive migration enrolls pre-policy jobs without recreating their history.
	if _, err := tx.Exec(ctx, `INSERT INTO execution_queue_tenants(tenant_id) SELECT DISTINCT tenant_id FROM worker_jobs WHERE state IN ('queued','running') ON CONFLICT DO NOTHING`); err != nil {
		return JobClaim{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE execution_queue_tenants t SET deficit=0 WHERE deficit<>0 AND NOT EXISTS (
		SELECT 1 FROM worker_jobs j WHERE j.tenant_id=t.tenant_id AND (j.state='queued' OR (j.state='running' AND j.lease_until<=clock_timestamp())))`); err != nil {
		return JobClaim{}, err
	}
	var tenants int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM execution_queue_tenants`).Scan(&tenants); err != nil {
		return JobClaim{}, err
	}
	for range tenants {
		var tenant string
		var deficit int
		err := tx.QueryRow(ctx, `SELECT t.tenant_id,t.deficit FROM execution_queue_tenants t
			WHERE EXISTS (SELECT 1 FROM worker_jobs j WHERE j.tenant_id=t.tenant_id AND
				((j.state='queued' AND j.ready_at<=clock_timestamp()) OR
				(j.state='running' AND j.lease_until<=clock_timestamp() AND (j.ready_at<=clock_timestamp() OR j.deadline_at<=clock_timestamp()))))
			AND (SELECT count(*) FROM worker_jobs a WHERE a.tenant_id=t.tenant_id AND a.state='running' AND a.lease_until>clock_timestamp())<$2
			ORDER BY CASE WHEN t.tenant_id=$1 AND t.deficit>0 THEN 0 WHEN t.tenant_id>$1 THEN 1 ELSE 2 END,t.tenant_id
			LIMIT 1 FOR UPDATE OF t SKIP LOCKED`, cursor, cfg.TenantInFlight).Scan(&tenant, &deficit)
		if err != nil {
			return JobClaim{}, err
		}
		var claim JobClaim
		var count, maxClaims int
		var expired bool
		err = tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,case_id,COALESCE(run_id,''),COALESCE(attempt_id,''),actor_id,kind,claim_count,max_claims,deadline_at,deadline_at<=clock_timestamp()
			FROM worker_jobs WHERE tenant_id=$1 AND
			((state='queued' AND ready_at<=clock_timestamp()) OR
			(state='running' AND lease_until<=clock_timestamp() AND (ready_at<=clock_timestamp() OR deadline_at<=clock_timestamp())))
			ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, tenant).Scan(
			&claim.JobID, &claim.TenantID, &claim.OrgID, &claim.CaseID, &claim.RunID, &claim.AttemptID, &claim.ActorID, &claim.Kind,
			&count, &maxClaims, &claim.deadline, &expired)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return JobClaim{}, err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// The deficit reset only bookkeeps credit the tenant no longer has
			// work to spend; guarding it keeps an idle claim write-free.
			if _, err := tx.Exec(ctx, `UPDATE execution_queue_tenants SET deficit=0 WHERE tenant_id=$1 AND deficit<>0`, tenant); err != nil {
				return JobClaim{}, err
			}
			cursor = tenant
			continue
		}
		if expired {
			claim.stopReason = "execution_deadline_exceeded"
		} else if count >= maxClaims {
			claim.stopReason = "execution_claims_exhausted"
		}
		if deficit == 0 {
			deficit = cfg.Weights[tenant]
			if deficit == 0 {
				deficit = 1
			}
		}
		deficit--
		if _, err := tx.Exec(ctx, `UPDATE execution_queue_tenants SET deficit=$2 WHERE tenant_id=$1`, tenant, deficit); err != nil {
			return JobClaim{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE execution_queue_cursor SET last_tenant=$1 WHERE tenant_id='__scheduler__'`, tenant); err != nil {
			return JobClaim{}, err
		}
		delay := queueRetryDelay(cfg, claim.JobID, count+1)
		err = tx.QueryRow(ctx, `UPDATE worker_jobs SET state='running',generation=generation+1,claim_count=claim_count+1,worker_id=$2,
			lease_until=clock_timestamp()+($3::bigint*interval '1 microsecond'),heartbeat_at=clock_timestamp(),
			ready_at=clock_timestamp()+(($3::bigint+$4::bigint)*interval '1 microsecond'),updated_at=$5
			WHERE id=$1 RETURNING generation,lease_until`, claim.JobID, worker, lease.Microseconds(), delay.Microseconds(), stamp()).Scan(&claim.Generation, &claim.LeaseUntil)
		if err != nil {
			return JobClaim{}, err
		}
		claim.WorkerID = worker
		return claim, nil
	}
	return JobClaim{}, pgx.ErrNoRows
}

// hasClaimableJob reports whether any job could currently be claimed. It is a
// read-only pre-check so an idle claim loop does not open the scheduling
// transaction, which takes the queue lock and rewrites tenant bookkeeping on
// every poll even when no work exists. The scheduler's own predicates stay
// authoritative: a true result can still end in no claim, and a false result
// cannot hide claimable work because it uses the same job-level eligibility.
func (s *Store) hasClaimableJob(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{Role: "worker"}); err != nil {
		return false, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_jobs WHERE
		(state='queued' AND ready_at<=clock_timestamp()) OR
		(state='running' AND lease_until<=clock_timestamp() AND (ready_at<=clock_timestamp() OR deadline_at<=clock_timestamp())))`).Scan(&exists); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return exists, nil
}

func queueRetryDelay(cfg *queueConfiguration, id string, count int) time.Duration {
	delay := int64(cfg.BackoffBaseMS)
	for i := 1; i < count && delay < int64(cfg.BackoffMaxMS); i++ {
		delay = min(delay*2, int64(cfg.BackoffMaxMS))
	}
	// Stable bounded jitter avoids a synchronized retry wave after a process loss.
	var hash uint64 = 14695981039346656037
	for i := 0; i < len(id); i++ {
		hash = (hash ^ uint64(id[i])) * 1099511628211
	}
	delay = min(delay+int64(hash%uint64(max(delay/4, 1))), int64(cfg.BackoffMaxMS))
	return time.Duration(delay) * time.Millisecond
}
