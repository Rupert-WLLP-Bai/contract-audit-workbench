package rulepack

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
)

var ErrInvalidRequest = errors.New("invalid rulepack request")

type Source struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	TenantID string `json:"tenant_id"`
	CaseID   string `json:"case_id"`
	Role     string `json:"role"`
	Digest   string `json:"digest"`
	Text     string `json:"text"`
}

type Citation struct {
	SourceID      string `json:"source_id"`
	SourceVersion string `json:"source_version"`
	TenantID      string `json:"tenant_id"`
	CaseID        string `json:"case_id"`
	SourceDigest  string `json:"source_digest"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	Quote         string `json:"quote"`
}

type Fact struct {
	ID                string     `json:"id"`
	RuleID            string     `json:"rule_id"`
	Dimension         string     `json:"dimension"`
	ObjectRef         string     `json:"object_ref"`
	Value             string     `json:"value"`
	Unit              string     `json:"unit"`
	SourceRole        string     `json:"source_role"`
	MeasurementWindow string     `json:"measurement_window,omitempty"`
	TimeZone          string     `json:"time_zone,omitempty"`
	Calendar          string     `json:"calendar,omitempty"`
	OutageDefinition  string     `json:"outage_definition,omitempty"`
	Exclusions        []string   `json:"exclusions,omitempty"`
	Basis             string     `json:"basis,omitempty"`
	Applicable        *bool      `json:"applicable,omitempty"`
	Citations         []Citation `json:"citations"`
}

type Request struct {
	Principal    access.Principal `json:"principal"`
	Resource     access.Resource  `json:"resource"`
	BusinessLine string           `json:"business_line"`
	Scenario     string           `json:"scenario"`
	ContractType string           `json:"contract_type"`
	At           time.Time        `json:"at"`
	Sources      []Source         `json:"sources"`
	Facts        []Fact           `json:"facts"`
}

type Result struct {
	RuleID       string     `json:"rule_id"`
	RuleVersion  string     `json:"rule_version"`
	Domain       string     `json:"domain"`
	CapabilityID string     `json:"capability_id"`
	Mandatory    bool       `json:"mandatory"`
	Relation     Relation   `json:"relation"`
	ReasonCode   string     `json:"reason_code"`
	FactIDs      []string   `json:"fact_ids,omitempty"`
	Citations    []Citation `json:"citations,omitempty"`
}

type Report struct {
	PackID      string     `json:"pack_id"`
	PackVersion string     `json:"pack_version"`
	PackHash    string     `json:"pack_hash"`
	Relations   []Relation `json:"relations"`
	Results     []Result   `json:"results"`
}

type sourceKey struct{ id, version string }

// Execute checks case.review authorization before inspecting facts, then runs every
// rule whose exact business scope matches the request. Model output is not an input.
func (p *Pack) Execute(ctx context.Context, request Request) (Report, error) {
	if p == nil {
		return Report{}, ErrInvalidManifest
	}
	if p.lifecycle.State != StatePublished {
		return Report{}, ErrNotPublished
	}
	if ctx == nil {
		return Report{}, fmt.Errorf("%w: nil context", ErrInvalidRequest)
	}
	decision := access.Check(access.Review, request.Principal, request.Resource, time.Now())
	if !decision.Allowed {
		return Report{}, fmt.Errorf("%w: %s", ErrUnauthorized, decision.Reason)
	}
	if request.Resource.TenantID != p.content.TenantID {
		return Report{}, ErrTenantMismatch
	}
	if request.At.IsZero() || !validIdentifier(request.BusinessLine) || !validIdentifier(request.Scenario) || !validIdentifier(request.ContractType) {
		return Report{}, fmt.Errorf("%w: business scope and evaluation time are required", ErrInvalidRequest)
	}
	from, _ := time.Parse(time.RFC3339Nano, p.content.EffectiveFrom)
	until, _ := time.Parse(time.RFC3339Nano, p.content.EffectiveUntil)
	if request.At.Before(from) || !request.At.Before(until) {
		return Report{}, ErrNotEffective
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	sources, err := indexSources(request)
	if err != nil {
		return Report{}, err
	}
	rulesByID := make(map[string]struct{}, len(p.content.Rules))
	factsByRule := make(map[string][]Fact)
	factIDs := make(map[string]struct{}, len(request.Facts))
	for _, rule := range p.content.Rules {
		rulesByID[rule.RuleID] = struct{}{}
	}
	for _, fact := range request.Facts {
		if !validIdentifier(fact.ID) || !validIdentifier(fact.RuleID) {
			return Report{}, fmt.Errorf("%w: fact identity is required", ErrInvalidRequest)
		}
		if _, exists := factIDs[fact.ID]; exists {
			return Report{}, fmt.Errorf("%w: duplicate fact id %q", ErrInvalidRequest, fact.ID)
		}
		factIDs[fact.ID] = struct{}{}
		if _, exists := rulesByID[fact.RuleID]; !exists {
			return Report{}, fmt.Errorf("%w: fact references unknown rule %q", ErrInvalidRequest, fact.RuleID)
		}
		factsByRule[fact.RuleID] = append(factsByRule[fact.RuleID], fact)
	}

	report := Report{
		PackID:      p.content.ID,
		PackVersion: p.content.Version,
		PackHash:    p.hash,
		Relations:   []Relation{RelationCompatible, RelationConflict, RelationInsufficient, RelationNotApplicable},
		Results:     make([]Result, 0, len(p.content.Rules)),
	}
	for _, rule := range p.content.Rules {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		if !scopeMatches(rule.BusinessScope, request) {
			continue
		}
		facts := factsByRule[rule.RuleID]
		result, err := evaluateRule(rule, facts, sources, request)
		if err != nil {
			return Report{}, err
		}
		report.Results = append(report.Results, result)
	}
	return report, nil
}

func scopeMatches(scope BusinessScope, request Request) bool {
	return scope.BusinessLine == request.BusinessLine && scope.Scenario == request.Scenario && scope.ContractType == request.ContractType
}

func indexSources(request Request) (map[sourceKey]Source, error) {
	sources := make(map[sourceKey]Source, len(request.Sources))
	for _, source := range request.Sources {
		if !validIdentifier(source.ID) || !validIdentifier(source.Version) || !validIdentifier(source.Role) || source.TenantID == "" || source.CaseID == "" || !utf8.ValidString(source.Text) {
			return nil, fmt.Errorf("%w: malformed source identity", ErrIdentity)
		}
		if source.TenantID != request.Resource.TenantID || source.CaseID != request.Resource.CaseID {
			return nil, fmt.Errorf("%w: source is outside the authorized case", ErrIdentity)
		}
		if source.Digest != digestHex([]byte(source.Text)) {
			return nil, fmt.Errorf("%w: source digest does not match text", ErrIdentity)
		}
		key := sourceKey{id: source.ID, version: source.Version}
		if _, exists := sources[key]; exists {
			return nil, fmt.Errorf("%w: duplicate source identity", ErrIdentity)
		}
		sources[key] = source
	}
	return sources, nil
}

// ValidateSources verifies source identities, digests, case scope and uniqueness.
// Callers must establish authorization before inspecting supplied source content.
func ValidateSources(request Request) error {
	_, err := indexSources(request)
	return err
}

func evaluateRule(rule Rule, facts []Fact, sources map[sourceKey]Source, request Request) (Result, error) {
	facts = append([]Fact(nil), facts...)
	sort.Slice(facts, func(i, j int) bool { return facts[i].ID < facts[j].ID })
	result := Result{
		RuleID:       rule.RuleID,
		RuleVersion:  rule.Version,
		Domain:       rule.Domain,
		CapabilityID: rule.CapabilityID,
		Mandatory:    rule.Mandatory,
	}
	if len(facts) == 0 {
		result.Relation, result.ReasonCode = RelationInsufficient, "required_fact_missing"
		return result, nil
	}
	allCitations := make([]Citation, 0)
	for _, fact := range facts {
		citations, rolesMatch, err := validateFactEvidence(fact, sources, request)
		if err != nil {
			return Result{}, err
		}
		allCitations = append(allCitations, citations...)
		if !rolesMatch {
			result.Relation, result.ReasonCode = RelationInsufficient, "source_role_mismatch"
			result.FactIDs, result.Citations = factIDsAndCitations(facts, allCitations)
			return result, nil
		}
	}
	if len(facts) > 1 {
		result.Relation, result.ReasonCode = RelationInsufficient, "required_fact_ambiguous"
		result.FactIDs, result.Citations = factIDsAndCitations(facts, allCitations)
		return result, nil
	}
	fact := facts[0]
	result.FactIDs, result.Citations = []string{fact.ID}, allCitations
	if len(fact.Citations) == 0 {
		result.Relation, result.ReasonCode = RelationInsufficient, "evidence_missing"
		return result, nil
	}
	if !containsString(rule.RequiredFact.SourceRoles, fact.SourceRole) {
		result.Relation, result.ReasonCode = RelationInsufficient, "source_role_not_allowed"
		return result, nil
	}
	if fact.Applicable != nil && !*fact.Applicable {
		if !evidenceProvesInapplicable(rule, fact) {
			result.Relation, result.ReasonCode = RelationInsufficient, "applicability_not_proven_for_rule_and_object"
			return result, nil
		}
		result.Relation, result.ReasonCode = RelationNotApplicable, "applicability_proven_false"
		return result, nil
	}
	if fact.Dimension != rule.RequiredFact.Dimension || fact.ObjectRef == "" || fact.SourceRole == "" || fact.Value == "" {
		result.Relation, result.ReasonCode = RelationInsufficient, "required_fact_metadata_missing"
		return result, nil
	}
	if rule.RequiredFact.ObjectRequired && !evidenceContains(fact.Citations, fact.ObjectRef) {
		result.Relation, result.ReasonCode = RelationInsufficient, "object_not_supported_by_evidence"
		return result, nil
	}
	if !factMetadataMatches(rule, fact) {
		result.Relation, result.ReasonCode = RelationInsufficient, "fact_metadata_mismatch"
		return result, nil
	}
	value, err := parseRational(fact.Value)
	if err != nil {
		result.Relation, result.ReasonCode = RelationInsufficient, "fact_value_invalid"
		return result, nil
	}
	if !EvidenceSupportsValue(rule, fact) {
		result.Relation, result.ReasonCode = RelationInsufficient, "evidence_does_not_support_fact"
		return result, nil
	}
	threshold, err := parseThresholdRational(rule.Threshold.Value)
	if err != nil {
		return Result{}, fmt.Errorf("%w: invalid threshold for rule %q: %v", ErrInvalidManifest, rule.RuleID, err)
	}
	switch rule.Comparator {
	case ComparatorMinimumBandwidth:
		value = bandwidthMbit(value, fact.Unit)
		threshold = bandwidthMbit(threshold, rule.Threshold.Unit)
		if value.Cmp(threshold) >= 0 {
			result.Relation, result.ReasonCode = RelationCompatible, "minimum_bandwidth_met"
		} else {
			result.Relation, result.ReasonCode = RelationConflict, "minimum_bandwidth_below_threshold"
		}
	case ComparatorMonthlySLA:
		if fact.Unit != "percent" || value.Cmp(big.NewRat(100, 1)) > 0 {
			result.Relation, result.ReasonCode = RelationInsufficient, "fact_unit_or_range_invalid"
		} else if value.Cmp(threshold) >= 0 {
			result.Relation, result.ReasonCode = RelationCompatible, "monthly_availability_met"
		} else {
			result.Relation, result.ReasonCode = RelationConflict, "monthly_availability_below_threshold"
		}
	case ComparatorAdvancePaymentCeiling:
		if fact.Unit != "ratio" || value.Cmp(big.NewRat(1, 1)) > 0 {
			result.Relation, result.ReasonCode = RelationInsufficient, "fact_unit_or_range_invalid"
		} else if value.Cmp(threshold) <= 0 {
			result.Relation, result.ReasonCode = RelationCompatible, "advance_payment_within_ceiling"
		} else {
			result.Relation, result.ReasonCode = RelationConflict, "advance_payment_exceeds_ceiling"
		}
	default:
		return Result{}, fmt.Errorf("%w: unsupported comparator %q", ErrInvalidManifest, rule.Comparator)
	}
	return result, nil
}

func validateFactEvidence(fact Fact, sources map[sourceKey]Source, request Request) ([]Citation, bool, error) {
	if len(fact.Citations) == 0 {
		return nil, true, nil
	}
	citations := make([]Citation, 0, len(fact.Citations))
	rolesMatch := true
	for _, citation := range fact.Citations {
		source, exists := sources[sourceKey{id: citation.SourceID, version: citation.SourceVersion}]
		if !exists {
			return nil, false, fmt.Errorf("%w: citation source does not exist", ErrIdentity)
		}
		if citation.TenantID != request.Resource.TenantID || citation.CaseID != request.Resource.CaseID || citation.TenantID != source.TenantID || citation.CaseID != source.CaseID || citation.SourceDigest != source.Digest {
			return nil, false, fmt.Errorf("%w: citation identity does not match frozen source", ErrIdentity)
		}
		if citation.Start < 0 || citation.End <= citation.Start || citation.Quote == "" {
			return nil, false, fmt.Errorf("%w: citation span is invalid", ErrIdentity)
		}
		runes := []rune(source.Text)
		if citation.End > len(runes) || string(runes[citation.Start:citation.End]) != citation.Quote {
			return nil, false, fmt.Errorf("%w: code-point quote does not match source text", ErrIdentity)
		}
		if source.Role != fact.SourceRole {
			rolesMatch = false
		}
		citations = append(citations, citation)
	}
	sort.Slice(citations, func(i, j int) bool {
		a, b := citations[i], citations[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.SourceVersion != b.SourceVersion {
			return a.SourceVersion < b.SourceVersion
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End < b.End
		}
		return a.Quote < b.Quote
	})
	return citations, rolesMatch, nil
}

func factMetadataMatches(rule Rule, fact Fact) bool {
	required := rule.RequiredFact
	switch rule.Comparator {
	case ComparatorMinimumBandwidth:
		return fact.Unit == "Mbit/s" || fact.Unit == "Gbit/s"
	case ComparatorMonthlySLA:
		return fact.Unit == "percent" && fact.MeasurementWindow == required.MeasurementWindow && fact.TimeZone == required.TimeZone && fact.Calendar == required.Calendar && fact.OutageDefinition == required.OutageDefinition && sameStrings(fact.Exclusions, required.Exclusions)
	case ComparatorAdvancePaymentCeiling:
		return fact.Unit == "ratio" && fact.Basis == rule.Threshold.Basis && fact.Basis == required.Basis
	default:
		return false
	}
}

func evidenceContains(citations []Citation, value string) bool {
	return value != "" && strings.Contains(joinedQuotes(citations), value)
}

func joinedQuotes(citations []Citation) string {
	var builder strings.Builder
	for _, citation := range citations {
		builder.WriteString(citation.Quote)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func containsAny(value string, choices ...string) bool {
	for _, choice := range choices {
		if strings.Contains(value, strings.ToLower(choice)) {
			return true
		}
	}
	return false
}

func factIDsAndCitations(facts []Fact, citations []Citation) ([]string, []Citation) {
	ids := make([]string, 0, len(facts))
	for _, fact := range facts {
		ids = append(ids, fact.ID)
	}
	return ids, citations
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func bandwidthMbit(value *big.Rat, unit string) *big.Rat {
	converted := new(big.Rat).Set(value)
	if unit == "Gbit/s" {
		converted.Mul(converted, big.NewRat(1000, 1))
	}
	return converted
}
