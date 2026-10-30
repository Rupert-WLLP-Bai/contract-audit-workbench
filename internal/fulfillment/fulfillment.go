// Package fulfillment implements deterministic, evidence-bound fulfillment
// checks for the operator audit extension.
//
// The dedicated-line configured-bandwidth check compares a contract minimum
// held in a frozen evaluation input with one authorized external snapshot
// observation. Contract text and external snapshots stay distinct evidence
// kinds: an API response is never turned into a document citation, and a
// configuration value never stands in for a measured throughput result.
package fulfillment

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"contractaudit/internal/auditext"
)

const (
	// CheckConfiguredBandwidth is the only deterministic check supported by
	// this package.
	CheckConfiguredBandwidth = "line.configured_bandwidth"
	// ContractFieldMinimumBandwidth is the frozen contract fact name.
	ContractFieldMinimumBandwidth = "minimum_bandwidth"
	// ExternalFieldConfiguredBandwidth is the normalized external field name.
	ExternalFieldConfiguredBandwidth = "configured_bandwidth"
	// ComparatorConfiguredBandwidthAtLeast pins the comparator semantics.
	ComparatorConfiguredBandwidthAtLeast = "builtin.configured_bandwidth_at_least/v1"

	packDedicatedLine       = "operator.dedicated_line"
	semanticConfiguredValue = "configured_value"

	reasonContractFactMissing          = "contract_fact_missing"
	reasonContractFactUncertain        = "contract_fact_uncertain"
	reasonProvenOutOfScope             = "proven_out_of_scope"
	reasonContractEvidenceMissing      = "contract_evidence_missing"
	reasonEvidenceFieldMismatch        = "evidence_field_mismatch"
	reasonMeasurementSemanticsMismatch = "measurement_semantics_mismatch"
	reasonObjectBindingMissing         = "object_binding_missing"
	reasonObjectBindingAmbiguous       = "object_binding_ambiguous"
	reasonObservationMissing           = "observation_missing"
	reasonObservationAmbiguous         = "observation_ambiguous"
	reasonSourceStale                  = "source_stale"
	reasonSourceUnavailable            = "source_unavailable"
	reasonExternalObjectMismatch       = "external_object_mismatch"
	reasonObjectBindingMismatch        = "object_binding_mismatch"
	reasonSourceSystemMismatch         = "source_system_mismatch"
	reasonObservationTimeMismatch      = "observation_time_mismatch"
	reasonEffectiveTimeMismatch        = "effective_time_mismatch"
	reasonRequiredFieldMissing         = "required_field_missing"
	reasonRequiredFieldInvalid         = "required_field_invalid"
	reasonConfiguredBelowContract      = "configured_bandwidth_below_contract"
	reasonConfiguredMeetsContract      = "configured_bandwidth_meets_contract"

	limitationText = "差异只表明配置记录与合同要求不一致，不证明实际吞吐或赔偿责任。"

	maxEvidence = 16
)

// decimalValueRE is stricter than big.Rat.SetString: it rejects fractions and
// exponent notation so only an exact non-negative decimal is compared.
var decimalValueRE = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

// Evaluator runs the dedicated-line configured-bandwidth comparison. It is a
// pure deterministic check and never consults a model or external service.
type Evaluator struct{}

var _ auditext.Evaluator = (*Evaluator)(nil)

// New returns a stateless deterministic evaluator.
func New() *Evaluator { return &Evaluator{} }

// Evaluate implements auditext.Evaluator for the dedicated-line pack.
//
// Authorization failures return auditext.ErrUnauthorized without a business
// result. Malformed contract inputs return the validation error. Every emitted
// check result passes auditext.CheckResult.Validate and keeps
// RequiresHumanReview set: a configured-value conflict is never an automatic
// risk confirmation.
func (e *Evaluator) Evaluate(ctx context.Context, input auditext.FrozenInput, observations []auditext.Observation) ([]auditext.CheckResult, error) {
	if err := validateEvaluationRequest(ctx, input, observations); err != nil {
		return nil, err
	}
	fact, contractEvidence, results, err := evaluateContractFact(input)
	if evaluationFinished(results, err) {
		return results, err
	}
	observation, results, err := selectEvaluationObservation(input, observations, contractEvidence)
	if evaluationFinished(results, err) {
		return results, err
	}
	recordVersion := observationRecordVersion(observation)
	results, err = evaluateObservationAvailability(input, observation, contractEvidence, recordVersion)
	if evaluationFinished(results, err) {
		return results, err
	}
	results, err = validateObservationBinding(input, observation, contractEvidence, recordVersion)
	if evaluationFinished(results, err) {
		return results, err
	}
	return evaluateConfiguredBandwidth(input, fact, observation, contractEvidence, recordVersion)
}

func evaluationFinished(results []auditext.CheckResult, err error) bool {
	return results != nil || err != nil
}

func validateEvaluationRequest(ctx context.Context, input auditext.FrozenInput, observations []auditext.Observation) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", auditext.ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.Pack.ID != packDedicatedLine {
		return auditext.ErrUnsupported
	}
	if err := input.Scope.Validate(); err != nil {
		return auditext.ErrUnauthorized
	}
	if err := input.Validate(); err != nil {
		return err
	}
	for _, observation := range observations {
		if observation.TenantID != input.Scope.TenantID || observation.CaseID != input.Scope.CaseID {
			return auditext.ErrUnauthorized
		}
	}
	return nil
}

func evaluateContractFact(input auditext.FrozenInput) (auditext.Fact, []auditext.EvidenceRef, []auditext.CheckResult, error) {
	fact, found := input.FactByName(ContractFieldMinimumBandwidth)
	if !found {
		results, err := newResult(input, auditext.RelationInsufficient, reasonContractFactMissing, nil, false, "")
		return auditext.Fact{}, nil, results, err
	}
	contractEvidence := append([]auditext.EvidenceRef(nil), fact.Evidence...)

	// Contract-side checks stay ordered from "no fact" to "fact not usable".
	if fact.Uncertain {
		results, err := newResult(input, auditext.RelationInsufficient, reasonContractFactUncertain, contractEvidence, false, "")
		return auditext.Fact{}, nil, results, err
	}
	if fact.OutOfScope {
		results, err := newResult(input, auditext.RelationNotApplicable, reasonProvenOutOfScope, contractEvidence, false, "")
		return auditext.Fact{}, nil, results, err
	}
	if len(fact.Evidence) == 0 {
		results, err := newResult(input, auditext.RelationInsufficient, reasonContractEvidenceMissing, nil, false, "")
		return auditext.Fact{}, nil, results, err
	}
	if !documentEvidenceContains(fact.Evidence, fact.Value.Value) {
		results, err := newResult(input, auditext.RelationInsufficient, reasonEvidenceFieldMismatch, contractEvidence, false, "")
		return auditext.Fact{}, nil, results, err
	}
	if fact.SemanticKind != semanticConfiguredValue {
		results, err := newResult(input, auditext.RelationInsufficient, reasonMeasurementSemanticsMismatch, contractEvidence, false, "")
		return auditext.Fact{}, nil, results, err
	}
	return fact, contractEvidence, nil, nil
}

func selectEvaluationObservation(input auditext.FrozenInput, observations []auditext.Observation, evidence []auditext.EvidenceRef) (auditext.Observation, []auditext.CheckResult, error) {
	switch input.BindingStatus {
	case auditext.BindingMissing:
		results, err := newResult(input, auditext.RelationInsufficient, reasonObjectBindingMissing, evidence, false, "")
		return auditext.Observation{}, results, err
	case auditext.BindingAmbiguous:
		results, err := newResult(input, auditext.RelationInsufficient, reasonObjectBindingAmbiguous, evidence, false, "")
		return auditext.Observation{}, results, err
	}
	observation, selectionReason := selectObservation(input, observations)
	if selectionReason != "" {
		results, err := newResult(input, auditext.RelationInsufficient, selectionReason, evidence, false, "")
		return auditext.Observation{}, results, err
	}
	return observation, nil, nil
}

func observationRecordVersion(observation auditext.Observation) string {
	if observation.RecordVersion != nil {
		return *observation.RecordVersion
	}
	return ""
}

func evaluateObservationAvailability(input auditext.FrozenInput, observation auditext.Observation, evidence []auditext.EvidenceRef, recordVersion string) ([]auditext.CheckResult, error) {
	// Unusable observations surface their availability before fields are read.
	switch observation.Availability {
	case auditext.AvailabilityAvailable:
		return nil, nil
	case auditext.AvailabilityStale:
		return newResult(input, auditext.RelationInsufficient, reasonSourceStale, evidence, false, recordVersion)
	case auditext.AvailabilityUnavailable:
		return newResult(input, auditext.RelationInsufficient, reasonSourceUnavailable, evidence, false, recordVersion)
	case auditext.AvailabilityRejected:
		if auditext.ValidateID(observation.ReasonCode) != nil {
			return nil, fmt.Errorf("%w: rejected observation reason", auditext.ErrInvalidRequest)
		}
		return newResult(input, auditext.RelationInsufficient, observation.ReasonCode, evidence, false, recordVersion)
	default:
		return nil, fmt.Errorf("%w: observation availability", auditext.ErrInvalidRequest)
	}
}

func validateObservationBinding(input auditext.FrozenInput, observation auditext.Observation, evidence []auditext.EvidenceRef, recordVersion string) ([]auditext.CheckResult, error) {
	if observation.ExternalObjectID != input.ExternalObjectID {
		return newResult(input, auditext.RelationInsufficient, reasonExternalObjectMismatch, evidence, false, "")
	}
	if observation.ObjectBindingVersion != input.BindingVersion {
		return newResult(input, auditext.RelationInsufficient, reasonObjectBindingMismatch, evidence, false, recordVersion)
	}
	if observation.SourceSystem != input.SourceSystem {
		return newResult(input, auditext.RelationInsufficient, reasonSourceSystemMismatch, evidence, false, recordVersion)
	}
	if observation.ObservedAt.After(input.BusinessAt) {
		return newResult(input, auditext.RelationInsufficient, reasonObservationTimeMismatch, evidence, false, recordVersion)
	}
	if !observation.EffectiveAt(input.BusinessAt) {
		return newResult(input, auditext.RelationInsufficient, reasonEffectiveTimeMismatch, evidence, false, recordVersion)
	}
	return nil, nil
}

func evaluateConfiguredBandwidth(input auditext.FrozenInput, fact auditext.Fact, observation auditext.Observation, evidence []auditext.EvidenceRef, recordVersion string) ([]auditext.CheckResult, error) {
	field, fieldFound := observation.FieldByName(ExternalFieldConfiguredBandwidth)
	if !fieldFound {
		return newResult(input, auditext.RelationInsufficient, reasonRequiredFieldMissing, evidence, false, recordVersion)
	}
	if field.SemanticKind != semanticConfiguredValue {
		return newResult(input, auditext.RelationInsufficient, reasonMeasurementSemanticsMismatch, evidence, true, recordVersion)
	}
	configured, ok := bandwidthInMbit(field.Value, field.Unit)
	if !ok {
		return newResult(input, auditext.RelationInsufficient, reasonRequiredFieldInvalid, evidence, true, recordVersion)
	}
	minimum, ok := bandwidthInMbit(fact.Value.Value, fact.Value.Unit)
	if !ok {
		return newResult(input, auditext.RelationInsufficient, reasonRequiredFieldInvalid, evidence, true, recordVersion)
	}

	resultEvidence := withSnapshotEvidence(evidence, observation, field)
	if configured.Cmp(minimum) < 0 {
		return newResult(input, auditext.RelationConflict, reasonConfiguredBelowContract, resultEvidence, true, recordVersion)
	}
	return newResult(input, auditext.RelationCompatible, reasonConfiguredMeetsContract, resultEvidence, true, recordVersion)
}

// selectObservation requires exactly one observation bound to the frozen
// external object. A single observation bound elsewhere is surfaced as an
// object mismatch so callers can distinguish "nothing collected" from
// "collected the wrong record".
func selectObservation(input auditext.FrozenInput, observations []auditext.Observation) (auditext.Observation, string) {
	matches := make([]auditext.Observation, 0, 1)
	for _, observation := range observations {
		if observation.ExternalObjectID == input.ExternalObjectID {
			matches = append(matches, observation)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], ""
	case len(matches) > 1:
		return auditext.Observation{}, reasonObservationAmbiguous
	case len(observations) == 0:
		return auditext.Observation{}, reasonObservationMissing
	case len(observations) == 1:
		// Exactly one candidate was supplied, but for another object. The
		// explicit mismatch check below reports the precise reason.
		return observations[0], ""
	default:
		return auditext.Observation{}, reasonObservationAmbiguous
	}
}

// bandwidthInMbit converts an exact decimal bandwidth to Mbit/s. Only the two
// units declared by the contract are accepted; Gbit/s scales by exactly 1000.
func bandwidthInMbit(value, unit string) (*big.Rat, bool) {
	if value == "" || len(value) > 64 || !decimalValueRE.MatchString(value) {
		return nil, false
	}
	quantity, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, false
	}
	switch unit {
	case "Mbit/s":
		return quantity, true
	case "Gbit/s":
		return new(big.Rat).Mul(quantity, big.NewRat(1000, 1)), true
	default:
		return nil, false
	}
}

func documentEvidenceContains(evidence []auditext.EvidenceRef, value string) bool {
	if value == "" {
		return false
	}
	for _, item := range evidence {
		if item.Kind == auditext.EvidenceDocument && strings.Contains(item.Quote, value) {
			return true
		}
	}
	return false
}

// withSnapshotEvidence appends real snapshot coordinates and keeps the result
// within the evidence limit. No document quote or page is synthesized here.
func withSnapshotEvidence(contract []auditext.EvidenceRef, observation auditext.Observation, field auditext.Field) []auditext.EvidenceRef {
	limit := maxEvidence - 1
	if len(contract) > limit {
		contract = contract[:limit]
	}
	evidence := make([]auditext.EvidenceRef, 0, len(contract)+1)
	evidence = append(evidence, contract...)
	evidence = append(evidence, auditext.EvidenceRef{
		Kind:         auditext.EvidenceExternalSnapshot,
		SnapshotID:   observation.SnapshotID,
		RecordID:     observation.RecordID,
		FieldPointer: field.SourcePointer,
		DisplayValue: field.Value + " " + field.Unit,
		ObservedAt:   observation.ObservedAt,
	})
	return evidence
}

// dependencySet declares positive and negative reads. The set stays complete
// even when a field or mapping is absent: "not found" is itself a dependency
// that a later addition must be able to invalidate.
func dependencySet(input auditext.FrozenInput, fieldPresent bool, recordVersion string) auditext.DependencySet {
	return auditext.DependencySet{
		Complete: true,
		Items: []auditext.Dependency{
			{
				Kind:    auditext.DependencyField,
				Ref:     "external.configured_bandwidth",
				Version: recordVersion,
				Present: fieldPresent,
			},
			{
				Kind:    auditext.DependencyRecordSet,
				Ref:     "external.records",
				Version: input.SnapshotSetID,
				Present: true,
			},
			{
				Kind:    auditext.DependencyMapping,
				Ref:     "object.binding",
				Version: input.BindingVersion,
				Present: input.BindingStatus == auditext.BindingConfirmed,
			},
			{
				Kind:    auditext.DependencyRulePack,
				Ref:     "rule_pack",
				Version: input.RulePackVersion,
				Present: true,
			},
			{
				Kind:    auditext.DependencyCapabilityPack,
				Ref:     "capability_pack",
				Version: input.Pack.Version,
				Present: true,
			},
			{
				Kind:    auditext.DependencyDocument,
				Ref:     input.ContractVersion,
				Present: true,
			},
			{
				Kind:    auditext.DependencyParser,
				Ref:     "parser",
				Version: input.ParserConfigurationID,
				Present: true,
			},
			{
				Kind:    auditext.DependencyModel,
				Ref:     "model",
				Version: input.ModelConfigurationID,
				Present: true,
			},
			{
				Kind:    auditext.DependencyPolicy,
				Ref:     "policy",
				Version: input.PlannerConfigurationID,
				Present: true,
			},
		},
	}
}

func newResult(input auditext.FrozenInput, relation auditext.Relation, reason string, evidence []auditext.EvidenceRef, fieldPresent bool, recordVersion string) ([]auditext.CheckResult, error) {
	if evidence == nil {
		evidence = []auditext.EvidenceRef{}
	}
	result := auditext.CheckResult{
		CheckID:             CheckConfiguredBandwidth,
		Relation:            relation,
		ReasonCode:          reason,
		MachineStatus:       auditext.MachineCompleted,
		Evidence:            evidence,
		RequiresHumanReview: true,
		Limitations:         []string{limitationText},
		Dependencies:        dependencySet(input, fieldPresent, recordVersion),
	}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return []auditext.CheckResult{result}, nil
}
