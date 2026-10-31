package store

import (
	"context"
	"errors"

	"contractaudit/internal/accounts"
	"github.com/jackc/pgx/v5"
)

// loadScopedCaseWriteState extends the existing bounded case scope with the
// review and business rows read by the ordinary case mutation callbacks.
func (s *Store) loadScopedCaseWriteState(ctx context.Context, tx pgx.Tx, caseID, actorID, decisionKey string) (state, error) {
	st, err := s.loadScopedCaseState(ctx, tx, caseID)
	if err != nil {
		return state{}, err
	}
	if st.Attempts, err = loadAttempts(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if st.AttemptInputs, err = loadAttemptInputs(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if st.AttemptParseSelections, err = loadAttemptParseSelections(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if st.Findings, err = loadFindings(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if st.Evaluations, err = loadEvaluations(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if st.Decisions, err = loadDecisions(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if st.Activity, err = loadActivity(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if st.Snapshots, err = loadSnapshots(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	for _, attempt := range st.Attempts {
		if err := validateGovernedAttemptSnapshot(attempt, st.snapshotOf(attempt.RunID)); err != nil {
			return state{}, err
		}
	}
	if st.Jobs, err = loadJobs(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if !ownedTable(s.serviceOwner, "business_generations") {
		if st.Generations, err = loadBusinessGenerations(ctx, tx, caseID); err != nil {
			return state{}, err
		}
	}
	if st.Remediations, err = loadBusinessRemediations(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if st.Profiles, err = loadWorkbenchProfiles(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	if st.Tasks, err = loadWorkbenchTasks(ctx, tx, caseID); err != nil {
		return state{}, err
	}
	rows, err := tx.Query(ctx, `SELECT tenant_id,actor_user_id,operation,target_id,idempotency_key,payload_hash,response,http_status,created_at
		FROM subject_write_idempotency WHERE tenant_id=$1 AND actor_user_id=$2 AND target_id=$3 ORDER BY created_at`, st.Cases[0].TenantID, actorID, caseID)
	if err != nil {
		return state{}, err
	}
	for rows.Next() {
		var item SubjectIdempotencyRecord
		if err := rows.Scan(&item.TenantID, &item.ActorUserID, &item.Operation, &item.TargetID, &item.Key, &item.PayloadHash, &item.Response, &item.HTTPStatus, &item.CreatedAt); err != nil {
			rows.Close()
			return state{}, err
		}
		st.SubjectIdempotency = append(st.SubjectIdempotency, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return state{}, err
	}
	rows.Close()
	if decisionKey != "" {
		var item Idem
		err := tx.QueryRow(ctx, `SELECT actor_id, idem_key, finding_id, decision_id, action, reason, expected_version
			FROM idempotency WHERE actor_id=$1 AND idem_key=$2`, actorID, decisionKey).
			Scan(&item.ActorID, &item.Key, &item.FindingID, &item.DecisionID, &item.Action, &item.Reason, &item.ExpectedVersion)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return state{}, err
		}
		if err == nil {
			st.Idempotency = []Idem{item}
		}
	}
	canonicalizeLoadedRows(&st)
	st.normalize()
	applyReadProjections(&st)
	return st, nil
}

// scopedCreateCaseWrite locks a newly allocated case, or the case returned by a
// receipt lookup, and commits creation and any receipt in the same transaction.
func (s *Store) scopedCreateCaseWrite(ctx context.Context, user accounts.User, caseID string, lookup func(pgx.Tx) (string, error), fn func(*state) error) error {
	if ctx == nil {
		return errors.New("case creation context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	if caseID == "" {
		return errors.New("case creation requires an identity")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	scope := userScope(current)
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return err
	}
	replayID := ""
	if lookup != nil {
		replayID, err = lookup(tx)
		if err != nil {
			return err
		}
	}
	lockedID := caseID
	if replayID != "" {
		lockedID = replayID
	}
	if err := scopedCaseLock(ctx, tx, lockedID); err != nil {
		return err
	}
	var previous state
	if replayID != "" {
		item, err := loadKnowledgeCase(ctx, tx, current, replayID)
		if err != nil {
			return err
		}
		previous.Cases = []Case{item}
		scope.OrgID = item.OwnerOrgID
		if err := setLocalScope(ctx, tx, scope); err != nil {
			return err
		}
	}
	next, err := previous.clone()
	if err != nil {
		return err
	}
	if err := fn(&next); err != nil {
		return err
	}
	if err := validateAppendOnly(previous, next); err != nil {
		return err
	}
	removed, changed, err := stateDelta(previous, next)
	if err != nil {
		return err
	}
	if err := s.persist(ctx, tx, previous, next, nil); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.patchStateProjection(ctx, previous, removed, changed)
	return nil
}
