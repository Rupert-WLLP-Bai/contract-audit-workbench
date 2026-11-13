package model

import (
	"encoding/json"
	"strings"
	"time"
)

type LegalApplicabilityPartyPin struct {
	Side           string `json:"side,omitempty"`
	BindingID      string `json:"binding_id,omitempty"`
	BindingVersion int64  `json:"binding_version,omitempty"`
	SubjectID      string `json:"subject_id,omitempty"`
	SubjectVersion int64  `json:"subject_version,omitempty"`
	ScopeNote      string `json:"scope_note,omitempty"`
}

// LegalApplicabilityResolvedFact keeps the immutable authored version separate
// from the answer derived for one business time and evidence cutoff.
type LegalApplicabilityResolvedFact struct {
	Dimension     string                         `json:"dimension"`
	Status        string                         `json:"status"`
	Value         string                         `json:"value,omitempty"`
	UnknownReason string                         `json:"unknown_reason,omitempty"`
	Fact          *LegalApplicabilityFactVersion `json:"fact,omitempty"`
}

type LegalApplicabilityFactSet struct {
	BusinessAt       time.Time                        `json:"business_at"`
	EvidenceCutoffAt time.Time                        `json:"evidence_cutoff_at"`
	Facts            []LegalApplicabilityResolvedFact `json:"facts"`
}

// LegalApplicabilityFactVersion is an immutable, server-recorded fact version.
// Value is populated only for established facts; unknown versions carry a
// controlled UnknownReason and still participate in temporal selection.
type LegalApplicabilityFactVersion struct {
	TenantID        string                     `json:"tenant_id,omitempty"`
	OrgID           string                     `json:"org_id,omitempty"`
	CaseID          string                     `json:"case_id,omitempty"`
	FactID          string                     `json:"fact_id"`
	Dimension       string                     `json:"dimension"`
	Version         int64                      `json:"version"`
	Status          string                     `json:"status"`
	Value           string                     `json:"value,omitempty"`
	UnknownReason   string                     `json:"unknown_reason,omitempty"`
	TemporalBasis   string                     `json:"temporal_basis"`
	ValidFrom       time.Time                  `json:"valid_from"`
	ValidUntil      *time.Time                 `json:"valid_until,omitempty"`
	BasisObtainedAt *time.Time                 `json:"basis_obtained_at,omitempty"`
	BasisNote       string                     `json:"basis_note"`
	BasisRefs       json.RawMessage            `json:"basis_refs"`
	BasisSHA256     string                     `json:"basis_sha256"`
	Party           LegalApplicabilityPartyPin `json:"party,omitempty"`
	RequestDigest   string                     `json:"request_digest,omitempty"`
	IdempotencyKey  string                     `json:"idempotency_key,omitempty"`
	ActorUserID     string                     `json:"actor_user_id,omitempty"`
	ActorIssuer     string                     `json:"actor_issuer,omitempty"`
	ActorSubject    string                     `json:"actor_subject,omitempty"`
	CreatedAt       time.Time                  `json:"created_at"`
}

type LegalApplicabilitySelection struct {
	Version       LegalApplicabilityFactVersion
	UnknownReason string
}

// SelectLegalApplicabilityFactVersion selects by business validity first, not
// by append order. Evidence cutoff uses acquisition time; a current-state fact
// also requires its server observation point to be within the cutoff.
func SelectLegalApplicabilityFactVersion(currentVersion int64, versions []LegalApplicabilityFactVersion, businessAt, factCutoffAt time.Time) LegalApplicabilitySelection {
	var selected *LegalApplicabilityFactVersion
	for i := range versions {
		candidate := &versions[i]
		if candidate.Version <= 0 || candidate.Version > currentVersion ||
			(candidate.BasisObtainedAt != nil && candidate.BasisObtainedAt.After(factCutoffAt)) || candidate.ValidFrom.After(businessAt) ||
			(candidate.TemporalBasis == "current_state" && candidate.ValidFrom.After(factCutoffAt)) {
			continue
		}
		if selected == nil || candidate.ValidFrom.After(selected.ValidFrom) ||
			(candidate.ValidFrom.Equal(selected.ValidFrom) && candidate.Version > selected.Version) {
			selected = candidate
		}
	}
	if selected == nil {
		return LegalApplicabilitySelection{UnknownReason: "missing_fact"}
	}
	result := LegalApplicabilitySelection{Version: *selected}
	if selected.ValidUntil != nil && !businessAt.Before(*selected.ValidUntil) {
		result.UnknownReason = "expired_basis"
		return result
	}
	if selected.Status != "established" {
		result.UnknownReason = selected.UnknownReason
		if result.UnknownReason == "" {
			result.UnknownReason = "insufficient_evidence"
		}
		return result
	}
	if selected.TemporalBasis == "current_state" && selected.BasisObtainedAt != nil && businessAt.Before(*selected.BasisObtainedAt) {
		result.UnknownReason = "missing_fact"
	}
	return result
}

// PartyBindingFacts are the exact current Subject binding and subject head.
type PartyBindingFacts struct {
	BindingVersion, SubjectVersion, CurrentSubjectVersion int64
	SubjectID, Role, State                                string
}

func PartyPinStale(pin LegalApplicabilityPartyPin, facts PartyBindingFacts) bool {
	return facts.BindingVersion != pin.BindingVersion || facts.SubjectID != pin.SubjectID || facts.SubjectVersion != pin.SubjectVersion || facts.CurrentSubjectVersion != pin.SubjectVersion || facts.State != "active" || !PartyRoleMatches(pin.Side, facts.Role)
}
func PartyScopeAmbiguous(pin LegalApplicabilityPartyPin, activeCount int) bool {
	return activeCount > 1 && strings.TrimSpace(pin.ScopeNote) == ""
}
func PartyRoleMatches(side, role string) bool {
	if side == "buyer" {
		return role == "customer"
	}
	return side == "seller" && (role == "supplier" || role == "contractor" || role == "service_provider")
}
