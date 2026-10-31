package store

import (
	"context"
	"errors"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

var errAttemptAuthorizationRevoked = errors.New("attempt authorization was revoked")

// authorizeAttemptExecution resolves current grants for the frozen initiating actor.
// It applies to active execution; historical reads use their requesting user's grants.
func authorizeAttemptExecution(st state, runID, attemptID string) (accounts.User, error) {
	attempt, ok := st.attemptByID(attemptID)
	if !ok || attempt.RunID != runID || !attemptActive(attempt.State) {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	actorID := attempt.ActorID
	for _, job := range st.Jobs {
		if job.RunID == runID && job.AttemptID == attemptID {
			if actorID != "" && actorID != job.ActorID {
				return accounts.User{}, errAttemptAuthorizationRevoked
			}
			actorID = job.ActorID
			break
		}
	}
	user, ok := accounts.ByID(actorID)
	if !ok || !user.Enabled || !accounts.CanStart(user.Role) {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	for _, run := range st.Runs {
		if run.ID != runID {
			continue
		}
		item, exists := st.caseByID(run.CaseID)
		if !exists || businessWritable(item) != nil || !authorizeCase(item, user, access.Review) {
			return accounts.User{}, errAttemptAuthorizationRevoked
		}
		return user, nil
	}
	return accounts.User{}, errAttemptAuthorizationRevoked
}

func (s *Store) updateAuthorizedExecution(ctx context.Context, runID, attemptID string, fn func(*state) error) error {
	return s.updateExecutionContext(ctx, attemptID, func(st *state) error {
		if _, err := authorizeAttemptExecution(*st, runID, attemptID); err != nil {
			return err
		}
		return fn(st)
	})
}

func (s *Store) writableCase(st *state, user accounts.User, caseID string, action access.Action) (*Case, error) {
	item, err := s.visibleCase(st, user, caseID)
	if err != nil {
		return nil, err
	}
	if !authorizeCase(item, user, action) {
		return nil, fault.Forbidden("当前授权不能执行此案件操作")
	}
	if err := businessWritable(item); err != nil {
		return nil, err
	}
	for i := range st.Cases {
		if st.Cases[i].ID == caseID {
			return &st.Cases[i], nil
		}
	}
	return nil, fault.NotFound("没有找到对应记录")
}

// updateCaseAsWith is updateCaseAs with a transaction hook that runs after the
// case lock and before the state snapshot. Acceptance uses it to select the
// governed active pack inside the same transaction that freezes it, so the
// selection and the freeze are one linearised operation.
func (s *Store) updateCaseAsWith(user accounts.User, caseID string, hook func(pgx.Tx) error, fn func(*state) error) error {
	return s.scopedCaseWrite(context.Background(), user, caseID, "", "", true, true, hook, fn)
}

func (s *Store) updateCaseAs(user accounts.User, caseID string, fn func(*state) error) error {
	return s.scopedCaseWrite(context.Background(), user, caseID, "", "", true, false, nil, fn)
}

func (s *Store) updateCaseAsContext(ctx context.Context, user accounts.User, caseID string, fn func(*state) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	scope := userScope(current)
	scope.CaseID = caseID
	s.mu.Lock()
	defer s.mu.Unlock()
	// updateState takes the case-scoped advisory key before loading state, so this
	// general writer and the narrow case writers share one ordering.
	return s.updateState(ctx, scope, "", fn)
}

// updateSubjectCasesAs preserves the writer's schema-wide lock and then takes
// subject locks before sorted case locks. Subject corrections use it when one
// immutable subject version fans out to active bindings in several cases.
func (s *Store) updateSubjectCasesAs(ctx context.Context, user accounts.User, subjectIDs, caseIDs []string, fn func(*state) error) error {
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	scope := userScope(current)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateStateWithLocks(ctx, scope, "", subjectIDs, caseIDs, nil, fn)
}

// updateCaseAttemptAs is updateCaseAs with the attempt row locked for the whole
// transaction. Cancellation uses it so it cannot interleave with a worker phase
// write or a late model event for the same attempt: both take the same row lock.
func (s *Store) updateCaseAttemptAs(user accounts.User, caseID, attemptID string, fn func(*state) error) error {
	return s.scopedCaseWrite(context.Background(), user, caseID, attemptID, "", true, false, nil, fn)
}
