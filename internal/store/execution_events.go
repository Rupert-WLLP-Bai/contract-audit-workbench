package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
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
// Scope of the guarantee: this is a monotonic, gap-tolerant append per attempt.
// The unique (attempt_id, event_seq) key only stops two writers from storing the
// same sequence number; it is NOT exactly-once for a replayed logical event,
// because a replay is allocated the next sequence. The recorder calls this once
// per span event (internal/execution emits each event exactly once, no retry
// loop), so no stable event identity is needed on this path. The late-telemetry
// path (persistLateModelEvent) deliberately keeps its own (attempt, seq) check
// because it can replay a model completion after the attempt went terminal.
func (s *Store) appendExecutionEventFenced(ctx context.Context, runID, attemptID string, event ExecutionEvent) error {
	if ctx == nil || runID == "" || attemptID == "" || event.AttemptID != attemptID {
		return errExecutionEventScope
	}
	claim, ok := s.claimForAttempt(attemptID)
	if !ok || claim.JobID == "" || claim.AttemptID != attemptID || claim.RunID != runID {
		return errExecutionEventScope
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
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(event_seq),0)+1 FROM execution_events WHERE attempt_id=$1`, attemptID).Scan(&event.Seq); err != nil {
		return err
	}
	if err := insertExecutionEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.patchExecutionEventProjection(event)
	return nil
}

// patchExecutionEventProjection appends the committed event to the in-memory
// read model without cloning or rewriting any other entity; the event list is
// append-only and the sequence was allocated under the attempt row lock.
func (s *Store) patchExecutionEventProjection(event ExecutionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.st.ExecutionEvents {
		if existing.AttemptID == event.AttemptID && existing.Seq == event.Seq {
			return
		}
	}
	s.st.ExecutionEvents = append(s.st.ExecutionEvents, event)
}
