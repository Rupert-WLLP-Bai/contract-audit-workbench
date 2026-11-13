// File metadata (manifest mirror):
// Purpose: FrozenLegalAuthority records one normative rule source and the exact legal article and applicability evidence selected when a review was accepted. A nil ExactReference is… Related `store` operations include prepareLegalKnowledgeReuse, frozenLegalCitationID, validLegalReuseFingerprint, hasLegalReviewSnapshot.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	knowledgeApp "contractaudit/internal/knowledge/app"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
	"contractaudit/internal/rulepack"
	"github.com/jackc/pgx/v5"
)

type FrozenLegalAuthority = legalModel.FrozenLegalAuthority
type FrozenLegalArticle = legalModel.FrozenLegalArticle
type FrozenLegalSourceProvenance = legalModel.FrozenLegalSourceProvenance
type FrozenLegalStatusProvenance = legalModel.FrozenLegalStatusProvenance
type FrozenLegalApplicabilityCondition = legalModel.FrozenLegalApplicabilityCondition
type FrozenLegalDependencyGap = legalModel.FrozenLegalDependencyGap
type LegalApplicabilityEvaluation = legalModel.LegalApplicabilityEvaluation

func prepareLegalKnowledgeReuse(snapshot *ReviewKnowledgeSnapshot) error {
	if snapshot == nil {
		return errors.New("nil governed knowledge snapshot")
	}
	fields := legalModel.LegalReuseFields{EvidenceCutoffAt: snapshot.EvidenceCutoffAt, EvidenceCutoffMode: snapshot.EvidenceCutoffMode, ExplicitEvidenceCutoffAt: snapshot.ExplicitEvidenceCutoffAt, LegalReuseFingerprint: snapshot.LegalReuseFingerprint, LegalAuthorities: snapshot.LegalAuthorities}
	err := legalModel.PrepareLegalReuse(&fields)
	snapshot.EvidenceCutoffAt, snapshot.EvidenceCutoffMode, snapshot.ExplicitEvidenceCutoffAt, snapshot.LegalReuseFingerprint, snapshot.LegalAuthorities = fields.EvidenceCutoffAt, fields.EvidenceCutoffMode, fields.ExplicitEvidenceCutoffAt, fields.LegalReuseFingerprint, fields.LegalAuthorities
	return err
}
func frozenLegalCitationID(a FrozenLegalAuthority) (string, error) {
	return legalModel.FrozenLegalCitationID(a)
}
func validLegalReuseFingerprint(snapshot *ReviewKnowledgeSnapshot) bool {
	if snapshot == nil || snapshot.LegalReuseFingerprint == "" {
		return false
	}
	copy := *snapshot
	if err := prepareLegalKnowledgeReuse(&copy); err != nil {
		return false
	}
	return copy.LegalReuseFingerprint == snapshot.LegalReuseFingerprint
}

func hasLegalReviewSnapshot(snapshot *ReviewKnowledgeSnapshot) bool {
	return snapshot != nil && (len(snapshot.LegalAuthorities) > 0 || snapshot.EvidenceCutoffAt != "" || snapshot.EvidenceCutoffMode != "" ||
		snapshot.ExplicitEvidenceCutoffAt != "" || snapshot.LegalReuseFingerprint != "" || snapshot.LegalAuthorities != nil)
}

func hasNormativeRulePackAuthority(pack *rulepack.Pack) bool {
	if pack == nil {
		return false
	}
	for _, rule := range pack.Manifest().Rules {
		for _, source := range rule.AuthoritySources {
			if source.Kind == "normative" {
				return true
			}
		}
	}
	return false
}

// legacyFrozenAuthoritySemantics is only true for a governed pack and exact
// accepted run snapshot that predates the AUTH03 extension. New reviews with
// unbound normative rules carry explicit legal authority gap entries.
func legacyFrozenAuthoritySemantics(snapshot RunSnapshot, pack *rulepack.Pack) bool {
	return snapshot.RunID != "" && snapshot.GovernedKnowledge != nil && !hasLegalReviewSnapshot(snapshot.GovernedKnowledge) &&
		hasNormativeRulePackAuthority(pack)
}

func computeLegalReuseFingerprint(mode, explicit string, authorities []FrozenLegalAuthority) (string, error) {
	return legalModel.ComputeLegalReuseFingerprint(mode, explicit, authorities)
}
func validateFrozenLegalAuthorities(a []FrozenLegalAuthority) error {
	return legalModel.ValidateFrozenLegalAuthorities(a)
}
func validateFrozenLegalArticle(a FrozenLegalAuthority) error {
	return legalModel.ValidateFrozenLegalArticle(a)
}
func evaluateLegalAuthorityApplicability(refs []rulepack.LegalAuthorityReference, facts LegalApplicabilityFactSet) (LegalApplicabilityEvaluation, error) {
	return legalModel.EvaluateLegalAuthorityApplicability(refs, facts)
}

// rulepackAuthorityGates converts the exact per-reference result in an
// accepted snapshot into the conservative all_of gate consumed by RulePack.
func rulepackAuthorityGates(snapshot *ReviewKnowledgeSnapshot) ([]rulepack.AuthorityGate, error) {
	if snapshot == nil || len(snapshot.LegalAuthorities) == 0 {
		return nil, nil
	}
	if !validLegalReuseFingerprint(snapshot) {
		return nil, errors.New("frozen legal authority snapshot seal is invalid")
	}
	type key struct{ ruleID, sourceID, sourceVersion string }
	type aggregate struct {
		gate             rulepack.AuthorityGate
		unknown          bool
		unknownWhy       string
		notApplicable    bool
		referenceMissing bool
	}
	byKey := make(map[key]*aggregate)
	for _, authority := range snapshot.LegalAuthorities {
		k := key{authority.RuleID, authority.AuthoritySourceID, authority.AuthoritySourceVersion}
		item := byKey[k]
		if item == nil {
			item = &aggregate{gate: rulepack.AuthorityGate{RuleID: k.ruleID, SourceID: k.sourceID, SourceVersion: k.sourceVersion, Outcome: "applicable"}}
			byKey[k] = item
		}
		if authority.LegacyRuleGap {
			item.referenceMissing = true
			item.unknownWhy = "legal_reference_missing"
			continue
		}
		switch authority.Outcome {
		case "applicable":
		case "not_applicable":
			item.notApplicable = true
		case "unknown":
			item.unknown = true
			reason := authority.OutcomeReason
			if reason == "" {
				reason = "legal_authority_unknown"
			}
			if item.unknownWhy == "" || reason < item.unknownWhy {
				item.unknownWhy = reason
			}
		default:
			return nil, errors.New("frozen legal authority has an invalid outcome")
		}
	}
	keys := make([]key, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ruleID != keys[j].ruleID {
			return keys[i].ruleID < keys[j].ruleID
		}
		if keys[i].sourceID != keys[j].sourceID {
			return keys[i].sourceID < keys[j].sourceID
		}
		return keys[i].sourceVersion < keys[j].sourceVersion
	})
	out := make([]rulepack.AuthorityGate, 0, len(keys))
	for _, k := range keys {
		item := byKey[k]
		if item.referenceMissing {
			item.gate.Outcome = "unknown"
			item.gate.Reason = "legal_reference_missing"
			item.gate.ReferenceMissing = true
		} else if item.notApplicable {
			item.gate.Outcome = "not_applicable"
			item.gate.Reason = "legal_authority_not_applicable"
		} else if item.unknown {
			item.gate.Outcome = "unknown"
			item.gate.Reason = item.unknownWhy
			if item.gate.Reason == "" {
				item.gate.Reason = "legal_authority_unknown"
			}
		}
		out = append(out, item.gate)
	}
	return out, nil
}

func validateFrozenLegalAuthorityCapturesTx(ctx context.Context, tx pgx.Tx, tenant string, authorities []FrozenLegalAuthority) error {
	return legalApp.ValidateFrozenCaptures(ctx, newLegalReviewFacts(tx, accounts.User{TenantID: tenant}, Case{}, false), authorities)
}

// ReadFrozenLegalAuthority returns only a reference present in the specified
// accepted run and attempt. It does not consult current rule pointers, current
// facts, or latest captures, so later changes cannot rewrite old citations.
func (s *Store) ReadFrozenLegalAuthority(ctx context.Context, user accounts.User, caseID, runID, attemptID, citationID string) (FrozenLegalAuthority, error) {
	ctx = commandContext(ctx, user)
	if ctx == nil || s == nil || s.pool == nil {
		return FrozenLegalAuthority{}, fault.New(503, "governance_unavailable", "制度数据库不可用")
	}
	caseID, runID, attemptID = strings.TrimSpace(caseID), strings.TrimSpace(runID), strings.TrimSpace(attemptID)
	if caseID == "" || runID == "" || attemptID == "" || !isLegalCitationID(citationID) {
		return FrozenLegalAuthority{}, fault.Invalid("读取冻结法律依据需要完整的审查、执行和citation_id")
	}
	current, err := trustedUser(user)
	if err != nil {
		return FrozenLegalAuthority{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return FrozenLegalAuthority{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		return FrozenLegalAuthority{}, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return FrozenLegalAuthority{}, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return FrozenLegalAuthority{}, err
	}
	item, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return FrozenLegalAuthority{}, err
	}
	if !authorizeCase(item, current, access.Read) {
		return FrozenLegalAuthority{}, fault.NotFound("没有找到对应记录")
	}
	var storedCase string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT r.case_id,s.governed_knowledge
		FROM review_runs r JOIN review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		JOIN review_run_snapshots s ON s.run_id=r.id
		WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5`,
		item.TenantID, item.OwnerOrgID, caseID, runID, attemptID).Scan(&storedCase, &raw)
	if errors.Is(err, pgx.ErrNoRows) || storedCase != caseID {
		return FrozenLegalAuthority{}, fault.NotFound("没有找到对应的冻结审查依据")
	}
	if err != nil {
		return FrozenLegalAuthority{}, err
	}
	frozen, err := decodeGovernedKnowledge(raw)
	if err != nil || frozen == nil {
		return FrozenLegalAuthority{}, fault.Conflict("所选审查没有有效的冻结法律依据")
	}
	selected, err := knowledgeApp.SelectFrozenLegalCitation(*frozen, citationID)
	if err != nil {
		return FrozenLegalAuthority{}, err
	}
	if err := legalApp.ValidateFrozenCaptures(ctx, newLegalReviewFacts(tx, current, item, false), []FrozenLegalAuthority{selected}); err != nil {
		return FrozenLegalAuthority{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return FrozenLegalAuthority{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FrozenLegalAuthority{}, err
	}
	return selected, nil
}

func isLegalCitationID(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func parseCanonicalLegalCutoff(raw string) (string, error) {
	return legalModel.ParseCanonicalLegalCutoff(raw)
}

func canonicalizeLegalCutoffInput(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	at, err := parseLegalFactTime(raw)
	if err != nil {
		return "", errors.New("legal evidence cutoff must be RFC3339")
	}
	return at.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano), nil
}
