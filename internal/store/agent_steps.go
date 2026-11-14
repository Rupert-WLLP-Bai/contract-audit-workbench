// File metadata (manifest mirror):
// Purpose: Supplies the narrow authorized Store transaction bridge for Execution checkpoints with current claim, case, attempt and governed input revalidation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"

	executionpostgres "contractaudit/internal/execution/postgres"
	"contractaudit/internal/execution/steps"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

type agentStepJournal struct {
	store     *Store
	claim     JobClaim
	inputHash string
	recorder  attemptRecorder
}

func newAgentStepJournal(store *Store, claim JobClaim, inputHash string, recorder attemptRecorder) *agentStepJournal {
	return &agentStepJournal{store: store, claim: claim, inputHash: inputHash, recorder: recorder}
}

func (journal *agentStepJournal) RecoverySummary(ctx context.Context) (steps.RecoverySummary, error) {
	var summary steps.RecoverySummary
	err := journal.withAuthorizedTransaction(ctx, func(tx pgx.Tx, scope steps.Scope) error {
		var err error
		summary, err = executionpostgres.NewStepCheckpoints(platformpostgres.Bind(tx)).RecoverySummary(ctx, scope)
		return err
	})
	if err != nil {
		return steps.RecoverySummary{}, wrapAgentStepPersistenceError(err)
	}
	return summary, nil
}

func (journal *agentStepJournal) Begin(ctx context.Context, identity steps.Identity) (steps.Record, bool, error) {
	var record steps.Record
	var execute bool
	err := journal.withAuthorizedTransaction(ctx, func(tx pgx.Tx, scope steps.Scope) error {
		var err error
		record, execute, err = executionpostgres.NewStepCheckpoints(platformpostgres.Bind(tx)).Begin(ctx, scope, identity)
		return err
	})
	if err != nil {
		return steps.Record{}, false, wrapAgentStepPersistenceError(err)
	}
	return record, execute, nil
}

func (journal *agentStepJournal) Finish(ctx context.Context, identity steps.Identity, outcome steps.Outcome) error {
	return wrapAgentStepPersistenceError(journal.withAuthorizedTransaction(ctx, func(tx pgx.Tx, scope steps.Scope) error {
		return executionpostgres.NewStepCheckpoints(platformpostgres.Bind(tx)).Finish(ctx, scope, identity, outcome)
	}))
}

func (journal *agentStepJournal) withAuthorizedTransaction(ctx context.Context, write func(pgx.Tx, steps.Scope) error) error {
	if ctx == nil {
		return errors.New("Agent step checkpoint context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if journal == nil || journal.store == nil || journal.claim.JobID == "" || journal.claim.Generation < 1 ||
		journal.claim.RunID == "" || journal.claim.AttemptID == "" || !isSHA256Hex(journal.inputHash) {
		return errExecutionEventScope
	}
	if err := journal.recorder.err(); err != nil {
		return err
	}
	current, ok := journal.store.claimForAttempt(journal.claim.AttemptID)
	if !ok || !sameAgentStepClaim(journal.claim, current) || !claimMatchesExecutionScope(ctx, current) {
		return errLeaseLost
	}
	tx, err := journal.store.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Governed knowledge withdrawal/publication uses this outer lock as well.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		return err
	}
	if err := setLocalScope(ctx, tx, current.scope()); err != nil {
		return err
	}
	if err := scopedCaseLock(ctx, tx, current.CaseID); err != nil {
		return err
	}
	if err := scopedAttemptLock(ctx, tx, current.AttemptID); err != nil {
		return err
	}
	var currentState string
	var currentCaseID string
	if err := tx.QueryRow(ctx, `SELECT a.state,r.case_id FROM review_attempts a
		JOIN review_runs r ON r.id=a.run_id AND r.tenant_id=a.tenant_id AND r.org_id=a.org_id
		WHERE a.id=$1 AND a.run_id=$2 FOR UPDATE OF a`,
		current.AttemptID, current.RunID).Scan(&currentState, &currentCaseID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errAttemptMissing
		}
		return err
	}
	if currentCaseID != current.CaseID {
		return errExecutionEventScope
	}
	if !attemptActive(currentState) {
		return errAttemptTerminal
	}
	if err := journal.store.requireLiveClaim(ctx, tx, current, current.RunID, current.AttemptID); err != nil {
		return err
	}
	if err := journal.store.reauthorizeAttempt(ctx, tx, current.RunID, current.AttemptID); err != nil {
		return err
	}
	if err := journal.store.revalidateGovernedAttemptTx(ctx, tx, current.RunID, current.AttemptID, current.CaseID); err != nil {
		return err
	}
	var inputHash string
	if err := tx.QueryRow(ctx, `SELECT input_hash FROM review_attempt_inputs WHERE attempt_id=$1 AND run_id=$2`,
		current.AttemptID, current.RunID).Scan(&inputHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.Join(steps.ErrIdentityMismatch, errors.New("frozen attempt input is missing"))
		}
		return err
	}
	if inputHash != journal.inputHash {
		return steps.ErrIdentityMismatch
	}
	scope := steps.Scope{TenantID: current.TenantID, OrgID: current.OrgID, CaseID: current.CaseID,
		RunID: current.RunID, AttemptID: current.AttemptID, Generation: current.Generation, AttemptInputSHA256: inputHash}
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := write(tx, scope); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func sameAgentStepClaim(left, right JobClaim) bool {
	return left.JobID == right.JobID && left.TenantID == right.TenantID && left.OrgID == right.OrgID &&
		left.CaseID == right.CaseID && left.RunID == right.RunID && left.AttemptID == right.AttemptID &&
		left.WorkerID == right.WorkerID && left.Generation == right.Generation && left.TenantLiveEpoch == right.TenantLiveEpoch
}

func wrapAgentStepPersistenceError(err error) error {
	if err == nil || errors.Is(err, steps.ErrPersistence) {
		return err
	}
	for _, expected := range []error{context.Canceled, context.DeadlineExceeded, errLeaseLost, errAttemptTerminal,
		errAttemptAuthorizationRevoked, errAttemptMissing, errExecutionEventScope} {
		if errors.Is(err, expected) {
			return err
		}
	}
	return errors.Join(steps.ErrPersistence, err)
}
