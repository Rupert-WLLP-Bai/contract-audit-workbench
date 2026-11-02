package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

const acceptanceOptionalKnowledgeLimit = 1024

// decodeGovernedKnowledge accepts only the canonical bytes and digest produced
// by the resolver. A nil column denotes a historical, pre-governance run.
func decodeGovernedKnowledge(raw []byte) (*ReviewKnowledgeSnapshot, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value ReviewKnowledgeSnapshot
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("invalid frozen governed knowledge")
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) || value.Encoding != "review-knowledge-snapshot/v1" ||
		!validKnowledgeHash(value.SnapshotSHA256) || !validKnowledgeHash(value.AuthorizationRevision) {
		return nil, errors.New("invalid frozen governed knowledge identity")
	}
	sealed, err := sealKnowledgeSnapshot(value)
	if err != nil || sealed.SnapshotSHA256 != value.SnapshotSHA256 {
		return nil, errors.New("frozen governed knowledge digest mismatch")
	}
	return &value, nil
}

func copyGovernedKnowledge(snapshot RunSnapshot) (*ReviewKnowledgeSnapshot, error) {
	if snapshot.GovernedKnowledge == nil {
		return nil, nil
	}
	raw, err := json.Marshal(snapshot.GovernedKnowledge)
	if err != nil {
		return nil, err
	}
	value, err := decodeGovernedKnowledge(raw)
	if err != nil {
		return nil, err
	}
	ref := value.RulePack
	if ref.PackID != snapshot.RulePackID || ref.Version != snapshot.RulePackVersion ||
		ref.ContentHash != snapshot.RulePackHash || ref.SnapshotDigest != snapshot.RulePackDigest ||
		ref.CompilerRef != snapshot.RulePackCompilerRef || ref.PointerRevision <= 0 || sha256Bytes(snapshot.RulePackSnapshot) != ref.SnapshotDigest {
		return nil, errors.New("frozen governed knowledge and rule pack differ")
	}
	return value, nil
}

// Every attempt in a governed run must carry the same immutable pack identity
// and pointer revision that its run's knowledge snapshot authorized.
func validateGovernedAttemptSnapshot(attempt Attempt, snapshot RunSnapshot) error {
	if snapshot.GovernedKnowledge == nil {
		return nil
	}
	ref := snapshot.GovernedKnowledge.RulePack
	if attempt.RunID != snapshot.RunID || attempt.RulePackID != ref.PackID || attempt.RulePackVersion != ref.Version ||
		attempt.RulePackHash != ref.ContentHash || attempt.RulePackSnapshotDigest != ref.SnapshotDigest ||
		attempt.RulePackCompilerRef != ref.CompilerRef || attempt.RulePackPointerRevision != ref.PointerRevision ||
		!bytes.Equal(attempt.RulePackSnapshot, snapshot.RulePackSnapshot) {
		return errors.New("attempt and frozen governed knowledge identities differ")
	}
	return nil
}

func sameKnowledgeSnapshotIdentity(a, b *ReviewKnowledgeSnapshot) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.LegalReuseFingerprint == "" && b.LegalReuseFingerprint == "" {
		return a.SnapshotSHA256 == b.SnapshotSHA256
	}
	if a.LegalReuseFingerprint == "" || b.LegalReuseFingerprint == "" ||
		!validLegalReuseFingerprint(a) || !validLegalReuseFingerprint(b) ||
		a.LegalReuseFingerprint != b.LegalReuseFingerprint {
		return false
	}
	// Acceptance-time cutoffs are sampled for each candidate snapshot. Reuse
	// ignores that timestamp only after comparing every prior governance input.
	// The exact selected legal dependencies remain covered by the fingerprint.
	left, right := *a, *b
	for _, snapshot := range []*ReviewKnowledgeSnapshot{&left, &right} {
		snapshot.EvidenceCutoffAt = ""
		snapshot.EvidenceCutoffMode = ""
		snapshot.ExplicitEvidenceCutoffAt = ""
		snapshot.LegalAuthorities = nil
		snapshot.LegalReuseFingerprint = ""
		snapshot.SnapshotSHA256 = ""
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

// revalidateGovernedAttemptTx runs only with the schema lock followed by the
// case and attempt locks. It derives actor, case, and frozen snapshot from the
// database, not from a caller-supplied tenant or snapshot.
func (s *Store) revalidateGovernedAttemptTx(ctx context.Context, tx pgx.Tx, runID, attemptID, caseID string) error {
	var actorID, storedCase string
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT a.actor_id, r.case_id, s.governed_knowledge
		FROM review_attempts a JOIN review_runs r ON r.id=a.run_id AND r.tenant_id=a.tenant_id AND r.org_id=a.org_id
		JOIN review_run_snapshots s ON s.run_id=r.id AND s.tenant_id=r.tenant_id AND s.org_id=r.org_id
		WHERE a.id=$1 AND a.run_id=$2`, attemptID, runID).Scan(&actorID, &storedCase, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return errAttemptMissing
	}
	if err != nil {
		return err
	}
	if storedCase != caseID {
		return errExecutionEventScope
	}
	var actor accounts.User
	if fenced, ok := tx.(*contextTx); ok && fenced.user.ID != "" {
		actor = fenced.user
	} else {
		actor, err = s.authorityForAttempt(ctx, runID, attemptID)
	}
	ok := err == nil
	if !ok {
		actor, ok = accounts.ByID(actorID)
		ok = ok && actor.Issuer == "" && actor.Subject == ""
	}
	if !ok || !actor.Enabled || !accounts.CanStart(actor.Role) {
		return errAttemptAuthorizationRevoked
	}
	current, err := trustedUser(actor)
	if err != nil {
		return err
	}
	item, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		if isCloudServicesCase(item) {
			return fault.Conflict("云服务审查缺少本次执行保存的制度记录")
		}
		return nil
	}
	frozen, err := decodeGovernedKnowledge(raw)
	if err != nil {
		return err
	}
	return ValidateFrozenGovernedKnowledgeSnapshotTx(ctx, tx, current, caseID, *frozen)
}

// updateAuthorizedGovernedExecution rechecks the initiating actor and frozen
// governed knowledge in the same locked transaction that adopts results. The
// durable path also keeps the existing lease fence. Legacy non-cloud runs still
// use the normal actor authorization check.
func (s *Store) updateAuthorizedGovernedExecution(ctx context.Context, runID, attemptID string, fn func(*state) error) error {
	claim, hasClaim := s.claimForAttempt(attemptID)
	if executionScopeRequiresClaim(ctx) && (!hasClaim || !claimMatchesExecutionScope(ctx, claim)) {
		return errLeaseLost
	}
	if hasClaim && claim.RunID == runID && claim.AttemptID == attemptID {
		return s.scopedGovernedAttemptMutation(ctx, runID, attemptID, func(st *state) error {
			if _, err := s.authorizeAttemptExecution(*st, runID, attemptID); err != nil {
				return err
			}
			return fn(st)
		})
	}
	seen, err := s.snapshotForAttemptMetadataContext(ctx, attemptID)
	if err != nil {
		return err
	}
	run, ok := seen.runByID(runID)
	if !ok {
		return errAttemptMissing
	}
	attempt, ok := seen.attemptByID(attemptID)
	if !ok || attempt.RunID != runID {
		return errAttemptMissing
	}
	actor, err := s.authorityForAttempt(ctx, runID, attemptID)
	ok = err == nil
	if !ok {
		actor, ok = accounts.ByID(attempt.ActorID)
		ok = ok && actor.Issuer == "" && actor.Subject == ""
	}
	if !ok || !actor.Enabled {
		return errAttemptAuthorizationRevoked
	}
	scope := userScope(actor)
	scope.CaseID = run.CaseID
	if err := s.lockExecutionContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	return s.updateStateWith(ctx, scope, attemptID, func(tx pgx.Tx) error {
		return s.revalidateGovernedAttemptTx(ctx, tx, runID, attemptID, run.CaseID)
	}, func(st *state) error {
		if _, err := s.authorizeAttemptExecution(*st, runID, attemptID); err != nil {
			return err
		}
		return fn(st)
	})
}
