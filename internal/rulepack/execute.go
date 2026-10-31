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

// AuthorityGate is supplied only by the persisted review snapshot reader. A
// normative rule cannot run its comparator unless every normative source has
// an exact frozen gate with an applicable outcome.
type AuthorityGate struct {
	RuleID           string `json:"rule_id"`
	SourceID         string `json:"source_id"`
	SourceVersion    string `json:"source_version"`
	Outcome          string `json:"outcome"`
	Reason           string `json:"reason,omitempty"`
	ReferenceMissing bool   `json:"reference_missing,omitempty"`
}

type Request struct {
	Principal      access.Principal `json:"principal"`
	Resource       access.Resource  `json:"resource"`
	BusinessLine   string           `json:"business_line"`
	Scenario       string           `json:"scenario"`
	ContractType   string           `json:"contract_type"`
	At             time.Time        `json:"at"`
	Sources        []Source         `json:"sources"`
	Facts          []Fact           `json:"facts"`
	AuthorityGates []AuthorityGate  `json:"authority_gates,omitempty"`
}

type Result struct {
	RuleID                string     `json:"rule_id"`
	RuleVersion           string     `json:"rule_version"`
	Domain                string     `json:"domain"`
	CapabilityID          string     `json:"capability_id"`
	Mandatory             bool       `json:"mandatory"`
	Relation              Relation   `json:"relation"`
	ReasonCode            string     `json:"reason_code"`
	FactIDs               []string   `json:"fact_ids,omitempty"`
	Citations             []Citation `json:"citations,omitempty"`
	LegalAuthorityOutcome string     `json:"legal_authority_outcome,omitempty"`
	LegalAuthorityReason  string     `json:"legal_authority_reason,omitempty"`
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
	return p.execute(ctx, request, false)
}

// ExecuteLegacyFrozen preserves the evaluator semantics for a pre-AUTH03
// accepted attempt whose exact persisted snapshot has been validated by the
// store and contains no legal authority extension. Callers must not select this
// path from request or HTTP data; new acceptance snapshots must use Execute.
func (p *Pack) ExecuteLegacyFrozen(ctx context.Context, request Request) (Report, error) {
	if len(request.AuthorityGates) != 0 {
		return Report{}, fmt.Errorf("%w: legacy frozen execution cannot accept legal gates", ErrInvalidRequest)
	}
	return p.execute(ctx, request, true)
}

func (p *Pack) execute(ctx context.Context, request Request, legacyFrozen bool) (Report, error) {
	if err := validateExecutionRequest(p, ctx, request); err != nil {
		return Report{}, err
	}
	sources, err := indexSources(request)
	if err != nil {
		return Report{}, err
	}
	factsByRule, err := indexFacts(p.content.Rules, request.Facts)
	if err != nil {
		return Report{}, err
	}
	return executeRules(ctx, p, request, sources, factsByRule, legacyFrozen)
}

func validateExecutionRequest(p *Pack, ctx context.Context, request Request) error {
	if err := validateExecutionPack(p); err != nil {
		return err
	}
	if err := validateExecutionContext(ctx); err != nil {
		return err
	}
	if err := validateExecutionAccess(p, request); err != nil {
		return err
	}
	if err := validateExecutionScope(p, request); err != nil {
		return err
	}
	return ctx.Err()
}

func validateExecutionPack(p *Pack) error {
	if p == nil {
		return ErrInvalidManifest
	}
	if p.lifecycle.State != StatePublished {
		return ErrNotPublished
	}
	return nil
}

func validateExecutionContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidRequest)
	}
	return nil
}

func validateExecutionAccess(p *Pack, request Request) error {
	decision := access.Check(access.Review, request.Principal, request.Resource, time.Now())
	if !decision.Allowed {
		return fmt.Errorf("%w: %s", ErrUnauthorized, decision.Reason)
	}
	if request.Resource.TenantID != p.content.TenantID {
		return ErrTenantMismatch
	}
	return nil
}

func validateExecutionScope(p *Pack, request Request) error {
	if request.At.IsZero() || !validIdentifier(request.BusinessLine) || !validIdentifier(request.Scenario) || !validIdentifier(request.ContractType) {
		return fmt.Errorf("%w: business scope and evaluation time are required", ErrInvalidRequest)
	}
	from, _ := time.Parse(time.RFC3339Nano, p.content.EffectiveFrom)
	until, _ := time.Parse(time.RFC3339Nano, p.content.EffectiveUntil)
	if request.At.Before(from) || !request.At.Before(until) {
		return ErrNotEffective
	}
	return nil
}

func indexFacts(rules []Rule, facts []Fact) (map[string][]Fact, error) {
	rulesByID := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		rulesByID[rule.RuleID] = struct{}{}
	}
	factsByRule := make(map[string][]Fact)
	factIDs := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		if !validIdentifier(fact.ID) || !validIdentifier(fact.RuleID) {
			return nil, fmt.Errorf("%w: fact identity is required", ErrInvalidRequest)
		}
		if _, exists := factIDs[fact.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate fact id %q", ErrInvalidRequest, fact.ID)
		}
		factIDs[fact.ID] = struct{}{}
		if _, exists := rulesByID[fact.RuleID]; !exists {
			return nil, fmt.Errorf("%w: fact references unknown rule %q", ErrInvalidRequest, fact.RuleID)
		}
		factsByRule[fact.RuleID] = append(factsByRule[fact.RuleID], fact)
	}
	return factsByRule, nil
}

func executeRules(ctx context.Context, p *Pack, request Request, sources map[sourceKey]Source, factsByRule map[string][]Fact, legacyFrozen bool) (Report, error) {
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
		var gate AuthorityGate
		var required bool
		if !legacyFrozen {
			var err error
			gate, required, err = evaluateRuleAuthorityGate(rule, request.AuthorityGates)
			if err != nil {
				return Report{}, err
			}
			if required && gate.Outcome != "applicable" {
				result := ruleResult(rule)
				result.LegalAuthorityOutcome = gate.Outcome
				result.LegalAuthorityReason = gate.Reason
				if gate.Outcome == "not_applicable" {
					result.Relation, result.ReasonCode = RelationNotApplicable, "legal_authority_not_applicable"
				} else {
					result.Relation, result.ReasonCode = RelationInsufficient, "legal_authority_unresolved"
				}
				report.Results = append(report.Results, result)
				continue
			}
		}
		facts := factsByRule[rule.RuleID]
		result, err := evaluateRule(rule, facts, sources, request)
		if err != nil {
			return Report{}, err
		}
		if required {
			result.LegalAuthorityOutcome = gate.Outcome
		}
		report.Results = append(report.Results, result)
	}
	return report, nil
}

func evaluateRuleAuthorityGate(rule Rule, gates []AuthorityGate) (AuthorityGate, bool, error) {
	var normative []AuthoritySource
	for _, source := range rule.AuthoritySources {
		if source.Kind == "normative" {
			normative = append(normative, source)
		}
	}
	if len(normative) == 0 {
		return AuthorityGate{}, false, nil
	}
	sort.Slice(normative, func(i, j int) bool {
		if normative[i].ID != normative[j].ID {
			return normative[i].ID < normative[j].ID
		}
		return normative[i].Version < normative[j].Version
	})
	combined := AuthorityGate{RuleID: rule.RuleID, Outcome: "applicable"}
	unknown := false
	notApplicable := false
	missingFreeze := false
	explicitMissingReference := false
	var unknownReason string
	for _, source := range normative {
		var match *AuthorityGate
		for index := range gates {
			gate := &gates[index]
			if gate.RuleID == rule.RuleID && gate.SourceID == source.ID && gate.SourceVersion == source.Version {
				if match != nil {
					return AuthorityGate{}, true, fmt.Errorf("%w: duplicate legal authority gate", ErrInvalidRequest)
				}
				match = gate
			}
		}
		if match == nil {
			missingFreeze = true
			continue
		}
		if match.ReferenceMissing {
			if match.Outcome != "unknown" {
				return AuthorityGate{}, true, fmt.Errorf("%w: missing-reference gate must be unknown", ErrInvalidRequest)
			}
			explicitMissingReference = true
			continue
		}
		switch match.Outcome {
		case "applicable":
		case "not_applicable":
			notApplicable = true
		case "unknown":
			unknown = true
			if unknownReason == "" {
				unknownReason = match.Reason
				if unknownReason == "" {
					unknownReason = "legal_authority_unknown"
				}
			}
		default:
			return AuthorityGate{}, true, fmt.Errorf("%w: invalid legal authority outcome", ErrInvalidRequest)
		}
	}
	// A missing exact reference is an authority gap, not a fact-level outcome,
	// so it remains unknown even if another bound source is not applicable.
	if missingFreeze || explicitMissingReference {
		reason := "legal_authority_missing_freeze"
		if explicitMissingReference {
			reason = "legal_reference_missing"
		}
		combined.Outcome, combined.Reason, combined.ReferenceMissing = "unknown", reason, true
	} else if notApplicable {
		combined.Outcome, combined.Reason = "not_applicable", "legal_authority_not_applicable"
	} else if unknown {
		combined.Outcome, combined.Reason = "unknown", unknownReason
	}
	return combined, true, nil
}

func scopeMatches(scope BusinessScope, request Request) bool {
	return scope.BusinessLine == request.BusinessLine && scope.Scenario == request.Scenario && scope.ContractType == request.ContractType
}

func indexSources(request Request) (map[sourceKey]Source, error) {
	sources := make(map[sourceKey]Source, len(request.Sources))
	for _, source := range request.Sources {
		key := sourceKey{id: source.ID, version: source.Version}
		if err := validateSourceIndexEntry(request, source, key, sources); err != nil {
			return nil, err
		}
		sources[key] = source
	}
	return sources, nil
}

func validateSourceIndexEntry(request Request, source Source, key sourceKey, sources map[sourceKey]Source) error {
	if !validSourceIdentity(source) {
		return fmt.Errorf("%w: malformed source identity", ErrIdentity)
	}
	if !sourceWithinRequest(source, request) {
		return fmt.Errorf("%w: source is outside the authorized case", ErrIdentity)
	}
	if source.Digest != digestHex([]byte(source.Text)) {
		return fmt.Errorf("%w: source digest does not match text", ErrIdentity)
	}
	if _, exists := sources[key]; exists {
		return fmt.Errorf("%w: duplicate source identity", ErrIdentity)
	}
	return nil
}

func validSourceIdentity(source Source) bool {
	return validIdentifier(source.ID) && validIdentifier(source.Version) && validIdentifier(source.Role) &&
		source.TenantID != "" && source.CaseID != "" && utf8.ValidString(source.Text)
}

func sourceWithinRequest(source Source, request Request) bool {
	return source.TenantID == request.Resource.TenantID && source.CaseID == request.Resource.CaseID
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
	result := ruleResult(rule)
	if len(facts) == 0 {
		result.Relation, result.ReasonCode = RelationInsufficient, "required_fact_missing"
		return result, nil
	}
	citations, roleMismatch, err := validateRuleFacts(facts, sources, request)
	if err != nil {
		return Result{}, err
	}
	if roleMismatch {
		return insufficientFactsResult(result, "source_role_mismatch", facts, citations), nil
	}
	if len(facts) > 1 {
		return insufficientFactsResult(result, "required_fact_ambiguous", facts, citations), nil
	}
	return evaluateSingleFact(result, rule, facts[0], citations)
}

func ruleResult(rule Rule) Result {
	return Result{RuleID: rule.RuleID, RuleVersion: rule.Version, Domain: rule.Domain,
		CapabilityID: rule.CapabilityID, Mandatory: rule.Mandatory}
}

func validateRuleFacts(facts []Fact, sources map[sourceKey]Source, request Request) ([]Citation, bool, error) {
	citations := make([]Citation, 0)
	for _, fact := range facts {
		factCitations, rolesMatch, err := validateFactEvidence(fact, sources, request)
		if err != nil {
			return nil, false, err
		}
		citations = append(citations, factCitations...)
		if !rolesMatch {
			return citations, true, nil
		}
	}
	return citations, false, nil
}

func insufficientFactsResult(result Result, reason string, facts []Fact, citations []Citation) Result {
	result.Relation, result.ReasonCode = RelationInsufficient, reason
	result.FactIDs, result.Citations = factIDsAndCitations(facts, citations)
	return result
}

func evaluateSingleFact(result Result, rule Rule, fact Fact, citations []Citation) (Result, error) {
	result.FactIDs, result.Citations = []string{fact.ID}, citations
	if evaluateFactPrerequisites(&result, rule, fact) {
		return result, nil
	}
	return evaluateFactValue(result, rule, fact)
}

func evaluateFactPrerequisites(result *Result, rule Rule, fact Fact) bool {
	if len(fact.Citations) == 0 {
		result.Relation, result.ReasonCode = RelationInsufficient, "evidence_missing"
		return true
	}
	if !containsString(rule.RequiredFact.SourceRoles, fact.SourceRole) {
		result.Relation, result.ReasonCode = RelationInsufficient, "source_role_not_allowed"
		return true
	}
	if evaluateFactApplicability(result, rule, fact) {
		return true
	}
	return evaluateFactMetadata(result, rule, fact)
}

func evaluateFactApplicability(result *Result, rule Rule, fact Fact) bool {
	if fact.Applicable == nil || *fact.Applicable {
		return false
	}
	if !evidenceProvesInapplicable(rule, fact) {
		result.Relation, result.ReasonCode = RelationInsufficient, "applicability_not_proven_for_rule_and_object"
	} else {
		result.Relation, result.ReasonCode = RelationNotApplicable, "applicability_proven_false"
	}
	return true
}

func evaluateFactMetadata(result *Result, rule Rule, fact Fact) bool {
	if fact.Dimension != rule.RequiredFact.Dimension || fact.ObjectRef == "" || fact.SourceRole == "" || fact.Value == "" {
		result.Relation, result.ReasonCode = RelationInsufficient, "required_fact_metadata_missing"
		return true
	}
	if rule.RequiredFact.ObjectRequired && !evidenceContains(fact.Citations, fact.ObjectRef) {
		result.Relation, result.ReasonCode = RelationInsufficient, "object_not_supported_by_evidence"
		return true
	}
	if !factMetadataMatches(rule, fact) {
		result.Relation, result.ReasonCode = RelationInsufficient, "fact_metadata_mismatch"
		return true
	}
	return false
}

func evaluateFactValue(result Result, rule Rule, fact Fact) (Result, error) {
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
	if err := compareFactValue(&result, rule, fact, value, threshold); err != nil {
		return Result{}, err
	}
	return result, nil
}

func compareFactValue(result *Result, rule Rule, fact Fact, value, threshold *big.Rat) error {
	switch rule.Comparator {
	case ComparatorMinimumBandwidth:
		return compareBandwidth(result, fact, rule, value, threshold)
	case ComparatorMonthlySLA:
		return compareMonthlySLA(result, fact, value, threshold)
	case ComparatorAdvancePaymentCeiling:
		return compareAdvancePayment(result, fact, value, threshold)
	default:
		return fmt.Errorf("%w: unsupported comparator %q", ErrInvalidManifest, rule.Comparator)
	}
}

func compareBandwidth(result *Result, fact Fact, rule Rule, value, threshold *big.Rat) error {
	actual := bandwidthMbit(value, fact.Unit)
	expected := bandwidthMbit(threshold, rule.Threshold.Unit)
	if actual.Cmp(expected) >= 0 {
		result.Relation, result.ReasonCode = RelationCompatible, "minimum_bandwidth_met"
	} else {
		result.Relation, result.ReasonCode = RelationConflict, "minimum_bandwidth_below_threshold"
	}
	return nil
}

func compareMonthlySLA(result *Result, fact Fact, value, threshold *big.Rat) error {
	if fact.Unit != "percent" || value.Cmp(big.NewRat(100, 1)) > 0 {
		result.Relation, result.ReasonCode = RelationInsufficient, "fact_unit_or_range_invalid"
	} else if value.Cmp(threshold) >= 0 {
		result.Relation, result.ReasonCode = RelationCompatible, "monthly_availability_met"
	} else {
		result.Relation, result.ReasonCode = RelationConflict, "monthly_availability_below_threshold"
	}
	return nil
}

func compareAdvancePayment(result *Result, fact Fact, value, threshold *big.Rat) error {
	if fact.Unit != "ratio" || value.Cmp(big.NewRat(1, 1)) > 0 {
		result.Relation, result.ReasonCode = RelationInsufficient, "fact_unit_or_range_invalid"
	} else if value.Cmp(threshold) <= 0 {
		result.Relation, result.ReasonCode = RelationCompatible, "advance_payment_within_ceiling"
	} else {
		result.Relation, result.ReasonCode = RelationConflict, "advance_payment_exceeds_ceiling"
	}
	return nil
}

func validateFactEvidence(fact Fact, sources map[sourceKey]Source, request Request) ([]Citation, bool, error) {
	if len(fact.Citations) == 0 {
		return nil, true, nil
	}
	citations := make([]Citation, 0, len(fact.Citations))
	rolesMatch := true
	for _, citation := range fact.Citations {
		roleMatches, err := validateCitation(fact, citation, sources, request)
		if err != nil {
			return nil, false, err
		}
		if !roleMatches {
			rolesMatch = false
		}
		citations = append(citations, citation)
	}
	return sortCitations(citations), rolesMatch, nil
}

func validateCitation(fact Fact, citation Citation, sources map[sourceKey]Source, request Request) (bool, error) {
	source, exists := sources[sourceKey{id: citation.SourceID, version: citation.SourceVersion}]
	if !exists {
		return false, fmt.Errorf("%w: citation source does not exist", ErrIdentity)
	}
	if !citationMatchesSource(citation, source, request) {
		return false, fmt.Errorf("%w: citation identity does not match frozen source", ErrIdentity)
	}
	if !validCitationSpan(citation) {
		return false, fmt.Errorf("%w: citation span is invalid", ErrIdentity)
	}
	if !citationQuoteMatches(citation, source) {
		return false, fmt.Errorf("%w: code-point quote does not match source text", ErrIdentity)
	}
	return source.Role == fact.SourceRole, nil
}

func citationMatchesSource(citation Citation, source Source, request Request) bool {
	return citation.TenantID == request.Resource.TenantID && citation.CaseID == request.Resource.CaseID &&
		citation.TenantID == source.TenantID && citation.CaseID == source.CaseID && citation.SourceDigest == source.Digest
}

func validCitationSpan(citation Citation) bool {
	return citation.Start >= 0 && citation.End > citation.Start && citation.Quote != ""
}

func citationQuoteMatches(citation Citation, source Source) bool {
	runes := []rune(source.Text)
	return citation.End <= len(runes) && string(runes[citation.Start:citation.End]) == citation.Quote
}

func sortCitations(citations []Citation) []Citation {
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
	return citations
}

func factMetadataMatches(rule Rule, fact Fact) bool {
	switch rule.Comparator {
	case ComparatorMinimumBandwidth:
		return bandwidthMetadataMatches(fact)
	case ComparatorMonthlySLA:
		return slaMetadataMatches(rule.RequiredFact, fact)
	case ComparatorAdvancePaymentCeiling:
		return advanceMetadataMatches(rule, fact)
	default:
		return false
	}
}

func bandwidthMetadataMatches(fact Fact) bool {
	return fact.Unit == "Mbit/s" || fact.Unit == "Gbit/s"
}

func slaMetadataMatches(required RequiredFact, fact Fact) bool {
	return fact.Unit == "percent" && fact.MeasurementWindow == required.MeasurementWindow &&
		fact.TimeZone == required.TimeZone && fact.Calendar == required.Calendar &&
		fact.OutageDefinition == required.OutageDefinition && sameStrings(fact.Exclusions, required.Exclusions)
}

func advanceMetadataMatches(rule Rule, fact Fact) bool {
	return fact.Unit == "ratio" && fact.Basis == rule.Threshold.Basis && fact.Basis == rule.RequiredFact.Basis
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
