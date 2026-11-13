package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"contractaudit/internal/fault"
)

type GovernedRulePackRef struct {
	PackID          string `json:"pack_id"`
	Version         string `json:"version"`
	ContentHash     string `json:"content_sha256"`
	SnapshotDigest  string `json:"snapshot_sha256"`
	CompilerRef     string `json:"compiler_ref"`
	PointerRevision int64  `json:"pointer_revision"`
}
type GovernedKnowledgeSelection struct {
	Ref                    KnowledgeRef         `json:"ref"`
	Kind                   string               `json:"kind"`
	SourceType             string               `json:"source_type"`
	Scope                  KnowledgeScope       `json:"scope"`
	Required               bool                 `json:"required"`
	Summary                string               `json:"summary"`
	ValidFrom              string               `json:"valid_from"`
	ValidUntil             string               `json:"valid_until"`
	ExceptionBasis         string               `json:"exception_basis,omitempty"`
	PolicyRefs             []KnowledgeRef       `json:"policy_refs,omitempty"`
	Applicability          []KnowledgeCondition `json:"applicability"`
	Exclusions             []KnowledgeCondition `json:"exclusions"`
	ReviewedApplicability  []string             `json:"reviewed_applicability,omitempty"`
	ReviewedExclusions     []string             `json:"reviewed_exclusions,omitempty"`
	CheckAction            string               `json:"check_action,omitempty"`
	MinimumEvidence        string               `json:"minimum_evidence,omitempty"`
	Counterexamples        []string             `json:"counterexamples,omitempty"`
	SemanticOnly           []PolicyTextIssue    `json:"semantic_only,omitempty"`
	RequiresFactEvaluation bool                 `json:"requires_fact_evaluation,omitempty"`
	PointerRevision        int64                `json:"pointer_revision"`
	Binding                PolicyRuleBinding    `json:"binding"`
	SelectionReason        string               `json:"selection_reason"`
}
type GovernedKnowledgeExclusion struct {
	Ref    KnowledgeRef `json:"ref"`
	Reason string       `json:"reason"`
}

// ReviewKnowledgeSnapshot preserves the v1 wire shape while its Legal-owned
// authority values remain supplied by the Legal boundary.
type ReviewKnowledgeSnapshot[Authority any] struct {
	Encoding                 string                       `json:"encoding"`
	BusinessAt               string                       `json:"business_at"`
	Scope                    KnowledgeScope               `json:"scope"`
	AuthorizationRevision    string                       `json:"authorization_revision"`
	RulePack                 GovernedRulePackRef          `json:"rule_pack"`
	RequiredPolicies         []GovernedKnowledgeSelection `json:"required_policies"`
	OptionalCandidates       []GovernedKnowledgeSelection `json:"optional_candidates"`
	Excluded                 []GovernedKnowledgeExclusion `json:"excluded"`
	OptionalTruncated        bool                         `json:"optional_truncated"`
	SnapshotSHA256           string                       `json:"snapshot_sha256"`
	EvidenceCutoffAt         string                       `json:"evidence_cutoff_at,omitempty"`
	EvidenceCutoffMode       string                       `json:"evidence_cutoff_mode,omitempty"`
	ExplicitEvidenceCutoffAt string                       `json:"explicit_evidence_cutoff_at,omitempty"`
	LegalAuthorities         []Authority                  `json:"legal_authorities,omitempty"`
	LegalReuseFingerprint    string                       `json:"legal_reuse_fingerprint,omitempty"`
}

// SealReviewSnapshot hashes a snapshot after its Legal-owned validation and
// canonicalization have completed at the existing Legal boundary.
func SealReviewSnapshot[A any](snapshot ReviewKnowledgeSnapshot[A]) (ReviewKnowledgeSnapshot[A], error) {
	snapshot.SnapshotSHA256 = ""
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return ReviewKnowledgeSnapshot[A]{}, err
	}
	snapshot.SnapshotSHA256 = SHA256(raw)
	return snapshot, nil
}

// DecodeCanonicalReviewSnapshot checks the v1 encoding and exact stored bytes;
// the caller must still validate Legal values and compare the sealed digest.
func DecodeCanonicalReviewSnapshot[A any](raw []byte) (*ReviewKnowledgeSnapshot[A], error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value ReviewKnowledgeSnapshot[A]
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("invalid frozen governed knowledge")
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) || value.Encoding != "review-knowledge-snapshot/v1" || !ValidHash(value.SnapshotSHA256) || !ValidHash(value.AuthorizationRevision) {
		return nil, errors.New("invalid frozen governed knowledge identity")
	}
	return &value, nil
}

// SameReviewSnapshotIdentity compares the historical reuse identity after the
// caller has verified each non-empty Legal fingerprint against Legal values.
func SameReviewSnapshotIdentity[A any](a, b *ReviewKnowledgeSnapshot[A], legalA, legalB bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.LegalReuseFingerprint == "" && b.LegalReuseFingerprint == "" {
		return a.SnapshotSHA256 == b.SnapshotSHA256
	}
	if a.LegalReuseFingerprint == "" || b.LegalReuseFingerprint == "" || !legalA || !legalB || a.LegalReuseFingerprint != b.LegalReuseFingerprint {
		return false
	}
	left, right := *a, *b
	for _, s := range []*ReviewKnowledgeSnapshot[A]{&left, &right} {
		s.EvidenceCutoffAt = ""
		s.EvidenceCutoffMode = ""
		s.ExplicitEvidenceCutoffAt = ""
		s.LegalAuthorities = nil
		s.LegalReuseFingerprint = ""
		s.SnapshotSHA256 = ""
	}
	l, e1 := json.Marshal(left)
	r, e2 := json.Marshal(right)
	return e1 == nil && e2 == nil && bytes.Equal(l, r)
}

type ReviewCaseFacts struct{ CaseID, OrgID, BusinessLine, Scenario, ContractType, BusinessStatus string }

func (c ReviewCaseFacts) value(field string) string {
	switch field {
	case "org_id":
		return c.OrgID
	case "case_id":
		return c.CaseID
	case "business_line":
		return c.BusinessLine
	case "scenario":
		return c.Scenario
	case "contract_type":
		return c.ContractType
	case "business_status":
		return c.BusinessStatus
	}
	return ""
}
func KnowledgeApplies(content GovernedKnowledgeContent, item ReviewCaseFacts, at time.Time) (bool, error) {
	if item.OrgID == "" || item.CaseID == "" || item.BusinessLine == "" || item.Scenario == "" || item.ContractType == "" {
		return false, fault.Conflict("案件适用范围不完整")
	}
	scope := content.Scope
	if scope.OrgID != "" && scope.OrgID != item.OrgID || scope.CaseID != "" && scope.CaseID != item.CaseID || scope.BusinessLine != item.BusinessLine || scope.Scenario != item.Scenario || scope.ContractType != item.ContractType {
		return false, nil
	}
	from, err := ParseBusinessTime(content.ValidFrom)
	if err != nil {
		return false, fault.Conflict("制度有效期无效")
	}
	until, err := ParseBusinessTime(content.ValidUntil)
	if err != nil {
		return false, fault.Conflict("制度有效期无效")
	}
	if at.Before(from) || !at.Before(until) {
		return false, nil
	}
	for _, condition := range content.Applicability {
		value := item.value(condition.Field)
		if value == "" {
			return false, fault.Conflict("案件缺少判断制度适用性所需的信息")
		}
		if value != condition.Equals {
			return false, nil
		}
	}
	for _, condition := range content.Exclusions {
		value := item.value(condition.Field)
		if value == "" {
			return false, fault.Conflict("案件缺少判断制度排除条件所需的信息")
		}
		if value == condition.Equals {
			return false, nil
		}
	}
	return true, nil
}
func KnowledgeSelection(v GovernedKnowledgeVersion) GovernedKnowledgeSelection {
	var semanticOnly []PolicyTextIssue
	if v.Content.Kind == "policy" {
		_, _, statements := ExtractGovernedPolicyThresholds(v.Content.Body)
		if len(statements) != 0 {
			semanticOnly = statements
		}
	}
	return GovernedKnowledgeSelection{Ref: v.Ref, Kind: v.Content.Kind, SourceType: v.Content.SourceType, Scope: v.Content.Scope, Required: v.Content.Kind == "policy", Summary: v.Content.Summary, ValidFrom: v.Content.ValidFrom, ValidUntil: v.Content.ValidUntil, ExceptionBasis: v.Content.ExceptionBasis, PolicyRefs: v.Content.PolicyRefs, Applicability: v.Content.Applicability, Exclusions: v.Content.Exclusions, ReviewedApplicability: v.Content.ReviewedApplicability, ReviewedExclusions: v.Content.ReviewedExclusions, CheckAction: v.Content.CheckAction, MinimumEvidence: v.Content.MinimumEvidence, Counterexamples: v.Content.Counterexamples, SemanticOnly: semanticOnly, RequiresFactEvaluation: v.Content.Kind == "experience" || v.Content.Kind == "exception" || len(semanticOnly) > 0, PointerRevision: v.PointerRevision, Binding: v.Content.Binding, SelectionReason: "published, authorized, in scope and effective at business time"}
}

// SortReviewCandidates preserves the resolver's knowledge-ID ordering in place.
func SortReviewCandidates(policies, optional []GovernedKnowledgeVersion) {
	sort.Slice(policies, func(i, j int) bool { return policies[i].Ref.KnowledgeID < policies[j].Ref.KnowledgeID })
	sort.Slice(optional, func(i, j int) bool { return optional[i].Ref.KnowledgeID < optional[j].Ref.KnowledgeID })
}

// AddReviewSelections preserves the resolver's optional budget and exclusion order.
// The caller validates the limit and initializes the destination slices.
func AddReviewSelections[A any](snapshot *ReviewKnowledgeSnapshot[A], policies, optional []GovernedKnowledgeVersion, limit int) {
	for _, v := range policies {
		snapshot.RequiredPolicies = append(snapshot.RequiredPolicies, KnowledgeSelection(v))
	}
	for i, v := range optional {
		if i < limit {
			snapshot.OptionalCandidates = append(snapshot.OptionalCandidates, KnowledgeSelection(v))
		} else {
			snapshot.OptionalTruncated = true
			snapshot.Excluded = append(snapshot.Excluded, GovernedKnowledgeExclusion{Ref: v.Ref, Reason: "optional_budget"})
		}
	}
}
