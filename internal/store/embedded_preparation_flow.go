package store

import (
	"context"
	"errors"
	"reflect"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

type embeddedAdmissionKey struct{}

func withEmbeddedAdmission(ctx context.Context, user accounts.User, admitted bool) context.Context {
	if !admitted {
		return ctx
	}
	return context.WithValue(ctx, embeddedAdmissionKey{}, user)
}

// Freeze and exchange before any business stage or model observer starts.
func (s *Store) prepareAndPromoteEmbedded(ctx context.Context, original model.DelegatedScope, models Models) (context.Context, error) {
	user, ok := ctx.Value(embeddedAdmissionKey{}).(accounts.User)
	if !ok {
		return ctx, errAttemptAuthorizationRevoked
	}
	if _, err := s.prepareAttempt(ctx, original.RunID, original.AttemptID, models); err != nil {
		return ctx, err
	}
	next, err := s.promoteEmbeddedAuthority(ctx, user, original)
	if err != nil {
		return ctx, err
	}
	s.claimMu.Lock()
	current, ok := s.operationContexts[original.AttemptID]
	claim, claimed := s.claims[original.AttemptID]
	if !ok || !claimed || !reflect.DeepEqual(current, original) || !s.validEmbeddedPreparationScope(original, claim) {
		s.claimMu.Unlock()
		return ctx, errAttemptAuthorizationRevoked
	}
	s.operationContexts[original.AttemptID] = next
	s.claimMu.Unlock()
	return model.WithDelegatedScope(ctx, modelScope(next, models)), nil
}

func (s *Store) settleEmbeddedPreparation(parent, work context.Context, runID, attemptID string, cause error) {
	stateName, code := "failed", "preparation_failed"
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		stateName, code = "cancelled", "preparation_cancelled"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), attemptFinalizeTimeout)
	defer cancel()
	scope, ok := model.DelegatedScopeFromContext(work)
	if !ok {
		return
	}
	ctx = model.WithDelegatedScope(ctx, scope)
	if err := s.finishEmbeddedPreparation(ctx, runID, attemptID, stateName, code); err != nil {
		_ = s.settleExpiredEmbeddedPreparation(ctx, scope, stateName, code)
	}
}

// This private machine-fact path only settles the exact still-installed attempt.
// It cannot call a model, freeze input, publish results, or use generic mutations.
func (s *Store) settleExpiredEmbeddedPreparation(ctx context.Context, scope model.DelegatedScope, stateName, code string) error {
	if (stateName != "failed" && stateName != "cancelled") || !preparationCode(&code) {
		return errExecutionEventScope
	}
	s.claimMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.claimMu.Unlock()
		}
	}()
	claim, ok := s.claims[scope.AttemptID]
	current, installed := s.operationContexts[scope.AttemptID]
	if !ok || !installed || !reflect.DeepEqual(current, scope) || !s.validEmbeddedPreparationScope(scope, claim) {
		return errExecutionEventScope
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, claim.scope()); err != nil {
		return err
	}
	if err = scopedCaseLock(ctx, tx, claim.CaseID); err != nil {
		return err
	}
	if err = scopedAttemptLock(ctx, tx, scope.AttemptID); err != nil {
		return err
	}
	var version int64
	var currentState, actor string
	if err = tx.QueryRow(ctx, `SELECT state,entity_version,actor_id FROM review_attempts WHERE id=$1 AND run_id=$2 FOR UPDATE`, scope.AttemptID, scope.RunID).Scan(&currentState, &version, &actor); err != nil {
		return err
	}
	if !attemptActive(currentState) || actor != scope.ActorID {
		return errAttemptTerminal
	}
	var hasJob bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_jobs WHERE tenant_id=$1 AND org_id=$2 AND run_id=$3 AND attempt_id=$4)`, scope.TenantID, scope.OrgID, scope.RunID, scope.AttemptID).Scan(&hasJob); err != nil {
		return err
	}
	if hasJob {
		return errExecutionEventScope
	}
	previous, err := s.loadScopedAttemptState(ctx, tx, claim, scope.RunID, scope.AttemptID)
	if err != nil {
		return err
	}
	canonicalizeLoadedRows(&previous)
	previous.normalize()
	next, err := previous.clone()
	if err != nil {
		return err
	}
	if err = applyEmbeddedPreparationTerminal(&next, scope.RunID, scope.AttemptID, stateName, code); err != nil {
		return err
	}
	if err = validateEmbeddedPreparationDelta(previous, next, scope.RunID, scope.AttemptID); err != nil {
		return err
	}
	old, changed, err := stateDelta(previous, next)
	if err != nil {
		return err
	}
	if err = s.validateServiceDelta(old, changed); err != nil {
		return err
	}
	if err = validateAppendOnly(previous, next); err != nil {
		return err
	}
	if err = s.persist(ctx, tx, previous, next, &version); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.claimMu.Unlock()
	locked = false
	s.patchStateProjection(ctx, previous, old, changed)
	return nil
}
