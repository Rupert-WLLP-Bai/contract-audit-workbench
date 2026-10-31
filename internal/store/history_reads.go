package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

// Retention is unchanged: PostgreSQL keeps all terminal jobs, delivered
// notifications, execution events and audit records. Before considering an
// archive or partition, measure table and index growth, state and age
// distributions, queue lag, query latency and buffers under tenant RLS,
// vacuum/WAL pressure, and backup/restore time. Audit rows have no timestamp;
// a run's creation time is only a proxy, not each audit entry's age. Any future
// proposal must keep attempt identities, frozen evidence, audit hashes,
// authorization and replay available throughout migration and recovery.

const jobSelectSQL = `SELECT id, tenant_id, org_id, case_id, COALESCE(run_id,''), COALESCE(attempt_id,''), actor_id, kind, state, generation, worker_id, lease_until, heartbeat_at, created_at, updated_at FROM worker_jobs`

func scanJobs(rows pgx.Rows) ([]Job, error) {
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

// loadWriteHistory loads only the chain heads needed to append to the locked
// cases. Full timelines stay in PostgreSQL and are read explicitly by run or
// attempt when a user opens them.
func loadWriteHistory(ctx context.Context, tx pgx.Tx, st *state, caseIDs []string, attemptID string) error {
	if len(caseIDs) != 0 {
		audits, err := tx.Query(ctx, `SELECT h.event_id,h.run_id,h.actor,h.seq,h.event_type,h.prev_hash,h.hash,h.correlation_ids
			FROM review_runs r CROSS JOIN LATERAL (
				SELECT * FROM (
					(SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM audit_events WHERE run_id=r.id ORDER BY seq DESC,event_id DESC LIMIT 1)
					UNION ALL
					(SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM business_audit_events WHERE run_id=r.id ORDER BY seq DESC,event_id DESC LIMIT 1)
				) candidates ORDER BY seq DESC,event_id DESC LIMIT 1
			) h WHERE r.case_id=ANY($1) ORDER BY h.seq,h.event_id`, caseIDs)
		if err != nil {
			return err
		}
		for audits.Next() {
			var item AuditRecord
			var correlation string
			if err := audits.Scan(&item.EventID, &item.RunID, &item.Actor, &item.Seq, &item.EventType, &item.PrevHash, &item.Hash, &correlation); err != nil {
				audits.Close()
				return err
			}
			if err := json.Unmarshal([]byte(correlation), &item.CorrelationIDs); err != nil {
				audits.Close()
				return err
			}
			st.Audits = append(st.Audits, item)
		}
		if err := audits.Err(); err != nil {
			audits.Close()
			return err
		}
		audits.Close()

		events, err := tx.Query(ctx, `SELECT h.run_id,h.event_seq,h.event_type,h.state
			FROM review_runs r CROSS JOIN LATERAL (
				SELECT * FROM (
					(SELECT run_id,event_seq,event_type,state FROM run_events WHERE run_id=r.id ORDER BY event_seq DESC LIMIT 1)
					UNION ALL
					(SELECT run_id,event_seq,event_type,state FROM business_run_events WHERE run_id=r.id ORDER BY event_seq DESC LIMIT 1)
				) candidates ORDER BY event_seq DESC LIMIT 1
			) h WHERE r.case_id=ANY($1) ORDER BY h.run_id,h.event_seq`, caseIDs)
		if err != nil {
			return err
		}
		for events.Next() {
			var item Event
			if err := events.Scan(&item.RunID, &item.Seq, &item.Type, &item.State); err != nil {
				events.Close()
				return err
			}
			st.Events = append(st.Events, item)
		}
		if err := events.Err(); err != nil {
			events.Close()
			return err
		}
		events.Close()
	}
	if attemptID != "" {
		var err error
		st.ExecutionEvents, err = loadExecutionEvents(ctx, readRows(tx, "attempt_id=$1", attemptID))
		if err != nil {
			return err
		}
	}
	return nil
}

// jobAcceptanceForRun also finds a job that finished between acceptance and
// this read. It never scans terminal jobs belonging to other attempts.
func (s *Store) jobAcceptanceForRun(ctx context.Context, user accounts.User, caseID, runID, missingAttempt, missingJob string) (JobAcceptance, error) {
	tx, _, _, err := s.beginCaseRead(ctx, user, caseID)
	if err != nil {
		return JobAcceptance{}, err
	}
	defer tx.Rollback(ctx)
	var result JobAcceptance
	result.RunID = runID
	err = tx.QueryRow(ctx, `SELECT a.id,COALESCE(j.id,'') FROM review_attempts a
		JOIN review_runs r ON r.id=a.run_id AND r.tenant_id=a.tenant_id AND r.org_id=a.org_id
		LEFT JOIN worker_jobs j ON j.attempt_id=a.id AND j.run_id=r.id AND j.case_id=r.case_id AND j.tenant_id=r.tenant_id AND j.org_id=r.org_id
		WHERE r.id=$1 AND r.case_id=$2 ORDER BY a.ordinal DESC LIMIT 1`, runID, caseID).Scan(&result.AttemptID, &result.JobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobAcceptance{}, fault.Conflict(missingAttempt)
	}
	if err != nil {
		return JobAcceptance{}, err
	}
	if result.JobID == "" {
		return JobAcceptance{}, fault.Conflict(missingJob)
	}
	if err := tx.Commit(ctx); err != nil {
		return JobAcceptance{}, err
	}
	return result, nil
}

// snapshotReportState keeps the selected attempt's immutable event timeline in
// the same repeatable-read snapshot used to freeze a report.
func (s *Store) snapshotReportState(ctx context.Context, user accounts.User, caseID, runID, attemptID string) (state, error) {
	current, err := trustedUser(user)
	if err != nil {
		return state{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return state{}, err
	}
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return state{}, err
	}
	caseItem, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return state{}, err
	}
	if err := businessWritable(caseItem); err != nil {
		return state{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Export); err != nil {
		return state{}, err
	}
	run, runOK := st.runByID(runID)
	attempt, attemptOK := st.attemptByID(attemptID)
	if runOK && attemptOK && run.CaseID == caseID && attempt.RunID == runID {
		st.ExecutionEvents, err = loadExecutionEvents(ctx, readRows(tx, "attempt_id=$1", attemptID))
		if err != nil {
			return state{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return st, nil
}

// operationalHistoryCounts computes the existing tenant-scoped counters in SQL
// without materializing every historical event or its trace payload.
func (s *Store) operationalHistoryCounts(ctx context.Context, user accounts.User) (map[string]any, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		return nil, err
	}
	var total, running, completed, partial, failed, cancelled int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*),
		COUNT(*) FILTER (WHERE state IN ('accepted','running')),
		COUNT(*) FILTER (WHERE state='completed'), COUNT(*) FILTER (WHERE state='partial'),
		COUNT(*) FILTER (WHERE state IN ('failed','interrupted')), COUNT(*) FILTER (WHERE state='cancelled')
		FROM review_attempts`).Scan(&total, &running, &completed, &partial, &failed, &cancelled); err != nil {
		return nil, err
	}
	var calls, unknown int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE usage IS NULL OR
		(usage::jsonb->>'input_tokens' IS NULL AND usage::jsonb->>'output_tokens' IS NULL AND usage::jsonb->>'total_tokens' IS NULL))
		FROM execution_events WHERE node_kind='model_call' AND event_type IN ('model_completed','model_failed')`).Scan(&calls, &unknown); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{
		"total_attempts": total, "running_attempts": running, "completed_attempts": completed,
		"partial_attempts": partial, "failed_attempts": failed, "cancelled_attempts": cancelled,
		"model_calls": calls, "unknown_usage_calls": unknown, "queue_depth": nil,
	}, nil
}
