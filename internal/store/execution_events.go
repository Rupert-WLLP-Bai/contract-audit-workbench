package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

// errExecutionEventScope reports that an execution event was offered without the
// current durable claim, attempt or run binding.
var errExecutionEventScope = errors.New("execution events require the current durable claim")

// appendExecutionEventFenced appends one execution event under per-entity
// constraints instead of the whole-state writer: the attempt row is locked and
// must still be active, the worker job must still be running with this worker,
// generation and a live lease, and the sequence is allocated from the attempt's
// own event rows under that lock.
//
// Keyed runtime events are idempotent within one attempt and claim generation.
// A replay with different contents is rejected; unkeyed observations retain the
// existing monotonic append semantics.
func (s *Store) appendExecutionEventFenced(ctx context.Context, runID, attemptID string, event ExecutionEvent) error {
	if ctx == nil || runID == "" || attemptID == "" || event.AttemptID != attemptID {
		return errExecutionEventScope
	}
	claim, ok := s.claimForAttempt(attemptID)
	if !ok || claim.JobID == "" || claim.AttemptID != attemptID || claim.RunID != runID {
		return errExecutionEventScope
	}
	if !claimMatchesExecutionScope(ctx, claim) {
		return errLeaseLost
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, claim.scope()); err != nil {
		return err
	}
	if err := scopedAttemptLock(ctx, tx, attemptID); err != nil {
		return err
	}
	var stateName string
	if err := tx.QueryRow(ctx, `SELECT state FROM review_attempts WHERE id=$1 AND run_id=$2 FOR UPDATE`, attemptID, runID).Scan(&stateName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errAttemptMissing
		}
		return err
	}
	if !attemptActive(stateName) {
		return errAttemptTerminal
	}
	var jobID string
	if err := tx.QueryRow(ctx, `SELECT id FROM worker_jobs
		WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND run_id=$5 AND attempt_id=$6
			AND worker_id=$7 AND generation=$8 AND state='running' AND lease_until > clock_timestamp()
		FOR UPDATE`,
		claim.JobID, claim.TenantID, claim.OrgID, claim.CaseID, runID, attemptID, claim.WorkerID, claim.Generation).Scan(&jobID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errLeaseLost
		}
		return err
	}
	if event.EventKey != nil {
		if strings.TrimSpace(*event.EventKey) != *event.EventKey || len(*event.EventKey) == 0 || len(*event.EventKey) > 160 {
			return errors.New("invalid execution event identity")
		}
		generation := claim.Generation
		event.Generation = &generation
		var usage any
		if event.Usage != nil {
			usage, err = jsonText(event.Usage)
			if err != nil {
				return err
			}
		}
		var matches bool
		err = tx.QueryRow(ctx, `SELECT span_id IS NOT DISTINCT FROM $4 AND parent_span_id IS NOT DISTINCT FROM $5
			AND node_kind=$6 AND phase=$7 AND event_type=$8 AND occurred_at=$9
			AND duration_ms IS NOT DISTINCT FROM $10 AND status=$11 AND error_code IS NOT DISTINCT FROM $12
			AND model_ref IS NOT DISTINCT FROM $13 AND usage IS NOT DISTINCT FROM $14
			FROM execution_events WHERE attempt_id=$1 AND generation=$2 AND event_key=$3`,
			attemptID, generation, *event.EventKey, event.SpanID, event.ParentSpanID, event.NodeKind, event.Phase,
			event.EventType, event.At, event.DurationMS, event.Status, event.ErrorCode, event.ModelRef, usage).Scan(&matches)
		if err == nil {
			if !matches {
				return errors.New("execution event identity was reused with different contents")
			}
			return tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	var recordedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&recordedAt); err != nil {
		return err
	}
	formattedAt := recordedAt.UTC().Format(time.RFC3339Nano)
	event.RecordedAt = &formattedAt
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(event_seq),0)+1 FROM execution_events WHERE attempt_id=$1`, attemptID).Scan(&event.Seq); err != nil {
		return err
	}
	if err := insertExecutionEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.patchExecutionEventProjection(ctx, event)
	return nil
}

// patchExecutionEventProjection appends the committed event to the in-memory
// read model without cloning or rewriting any other entity; the event list is
// append-only and the sequence was allocated under the attempt row lock.
func (s *Store) patchExecutionEventProjection(ctx context.Context, event ExecutionEvent) {
	if err := s.lockExecutionContext(ctx); err != nil {
		return
	}
	defer s.mu.Unlock()
	for _, existing := range s.st.ExecutionEvents {
		if existing.AttemptID == event.AttemptID && existing.Seq == event.Seq {
			return
		}
	}
	s.st.ExecutionEvents = append(s.st.ExecutionEvents, event)
}
