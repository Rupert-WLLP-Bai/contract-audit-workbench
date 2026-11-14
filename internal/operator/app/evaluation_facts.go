// File metadata (manifest mirror):
// Purpose: Converts exact frozen association facts and quantities, derives cutoff-safe observation copies and classifies public per-object results.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"bytes"
	"math/big"
	"strings"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

func FactsForAssociation(association model.ServiceObjectContractAssociation) ([]auditext.Fact, error) {
	canonical, digest, err := model.CanonicalServiceObjectFacts(association.FactRefs)
	if err != nil || len(association.FactsCanonicalJSON) == 0 || !bytes.Equal(canonical, association.FactsCanonicalJSON) ||
		digest != association.FactsSHA256 || !model.ValidServiceObjectSHA256(association.FactsSHA256) {
		return nil, fault.Conflict("服务对象合同关联事实未通过完整性校验")
	}
	if err := model.ValidateServiceObjectFactRefs(association); err != nil {
		return nil, fault.Conflict("服务对象合同关联事实与材料解析不一致")
	}
	out := make([]auditext.Fact, 0, len(association.FactRefs))
	for _, reference := range association.FactRefs {
		fact := reference.Fact
		evidence := make([]auditext.EvidenceRef, 0, len(fact.Citations))
		for _, citation := range fact.Citations {
			if citation.SourceID != association.DocumentID || citation.SourceVersion != association.ParsedDocumentID {
				return nil, fault.Conflict("服务对象合同关联引用了不同的冻结材料或解析")
			}
			evidence = append(evidence, auditext.EvidenceRef{
				Kind: auditext.EvidenceDocument, DocumentVersionID: citation.SourceID,
				ParsedVersionID: citation.SourceVersion, Start: citation.Start, End: citation.End,
				Quote: citation.Quote, SourceDigest: citation.SourceDigest,
			})
		}
		var applicable *bool
		outOfScope := false
		if fact.Applicable != nil {
			value := *fact.Applicable
			applicable = &value
			outOfScope = !value
		}
		semanticKind := ""
		if fact.Dimension == "minimum_bandwidth" {
			semanticKind = "configured_value"
		}
		quantity := auditext.ExactQuantity{Value: fact.Value, Unit: fact.Unit}
		if !outOfScope && strings.ContainsRune(quantity.Value, '/') {
			rational, ok := new(big.Rat).SetString(quantity.Value)
			if !ok || rational.Sign() < 0 {
				return nil, fault.Conflict("服务对象合同关联事实的精确数值不合法")
			}
			precision, exact := rational.FloatPrec()
			if !exact || precision > 64 {
				return nil, fault.Conflict("服务对象合同关联事实不能无损表示为精确十进制")
			}
			quantity.Value = rational.FloatString(precision)
			if auditext.ValidateQuantity(quantity) != nil {
				return nil, fault.Conflict("服务对象合同关联事实的精确数值不合法")
			}
		}
		out = append(out, auditext.Fact{
			Name: fact.Dimension, Value: quantity,
			Evidence: evidence, OutOfScope: outOfScope, SemanticKind: semanticKind,
			FactID: fact.ID, RuleID: fact.RuleID, Dimension: fact.Dimension, ObjectRef: fact.ObjectRef,
			Applicable: applicable, Basis: fact.Basis, MeasurementWindow: fact.MeasurementWindow,
			TimeZone: fact.TimeZone, Calendar: fact.Calendar, OutageDefinition: fact.OutageDefinition,
			Exclusions: append([]string(nil), fact.Exclusions...),
		})
	}
	return out, nil
}

func ObservationsForEvaluation(snapshot model.OperatorSnapshotSet, businessAt, cutoff time.Time) []auditext.Observation {
	observations := append([]auditext.Observation(nil), snapshot.Observations...)
	businessAt = businessAt.UTC().Truncate(time.Microsecond)
	cutoff = cutoff.UTC().Truncate(time.Microsecond)
	for i := range observations {
		observation := &observations[i]
		if observation.Availability != auditext.AvailabilityAvailable {
			continue
		}
		if snapshot.CollectedAt.After(cutoff) || observation.ObservedAt.After(cutoff) {
			operatorMarkObservationInsufficient(observation, "evidence_after_cutoff")
			continue
		}
		proof := observation.TemporalProof
		if proof == nil {
			operatorMarkObservationInsufficient(observation, "historical_proof_missing")
			continue
		}
		if proof.SourceAsOf == nil || proof.SourceAsOf.After(cutoff) {
			operatorMarkObservationInsufficient(observation, "temporal_proof_unavailable")
			continue
		}
		switch proof.Kind {
		case auditext.TemporalProofHistoricalInterval:
			if proof.ValidFrom == nil || proof.ValidUntil == nil ||
				proof.ValidFrom.After(businessAt) || !businessAt.Before(*proof.ValidUntil) ||
				!proof.ValidFrom.Equal(observation.EffectiveFrom) || !proof.ValidUntil.Equal(observation.EffectiveUntil) {
				operatorMarkObservationInsufficient(observation, "historical_proof_unavailable")
			}
		case auditext.TemporalProofCurrentState:
			if !proof.SourceAsOf.Equal(businessAt) {
				operatorMarkObservationInsufficient(observation, "historical_proof_missing")
			}
		default:
			operatorMarkObservationInsufficient(observation, "temporal_proof_unavailable")
		}
	}
	return observations
}

func operatorMarkObservationInsufficient(observation *auditext.Observation, reason string) {
	observation.Availability = auditext.AvailabilityRejected
	observation.ReasonCode = reason
	observation.PayloadRef = nil
	observation.Fields = nil
	// The derived evaluation view does not carry a proof that was insufficient
	// for this business instant. The immutable collected snapshot is untouched.
	observation.TemporalProof = nil
}

func EvaluationObjectForAssociation(scope model.OperatorEvaluationScope, input model.OperatorEvaluationInput, binding model.OperatorObjectBinding,
	association model.ServiceObjectContractAssociation, snapshot model.OperatorSnapshotSet, purpose, role string,
	facts []auditext.Fact, checks []auditext.CheckResult, machineStatus string) (model.OperatorEvaluationObject, error) {
	inScope := scope.Kind == "whole_case"
	for _, id := range scope.ServiceObjectIDs {
		if id == binding.ServiceObjectID {
			inScope = true
		}
	}
	if (scope.Kind != "whole_case" && !inScope) ||
		purpose == "" || role == "" || association.DocumentID == "" || association.ParsedDocumentID == "" {
		return model.OperatorEvaluationObject{}, fault.Conflict("单对象结果未固定到明确的服务对象与合同来源")
	}
	serviceObjectID := binding.ServiceObjectID
	objectVersion := binding.ObjectVersion
	associationVersion := binding.AssociationVersion
	mappingID := binding.MappingID
	mappingVersion := binding.MappingVersion
	bindingVersion := binding.Version
	snapshotSetID := snapshot.SnapshotSetID
	status := "evaluated"
	reason := ""
	allNotApplicable := len(checks) > 0
	for _, check := range checks {
		if check.Relation != auditext.RelationNotApplicable {
			allNotApplicable = false
		}
		if check.Relation == auditext.RelationInsufficient {
			status = "insufficient"
			if reason == "" {
				reason = check.ReasonCode
			}
		}
	}
	if allNotApplicable {
		status = "not_applicable"
	}
	if machineStatus != "completed" || len(checks) == 0 || len(facts) == 0 {
		status = "insufficient"
		if reason == "" {
			reason = "evaluation_partial"
		}
	}

	return model.OperatorEvaluationObject{
		ObjectRef: association.ObjectRef,
		Source: &model.OperatorEvaluationSource{RunID: input.SourceReviewID, AttemptID: input.SourceAttemptID,
			DocumentID: association.DocumentID, ParsedDocumentID: association.ParsedDocumentID,
			MaterialID: association.MaterialID, MaterialVersion: association.MaterialVersion, Purpose: purpose, Role: role,
			AssociationRunID: association.SourceRunID, AssociationAttemptID: association.SourceAttemptID},
		ServiceObjectID: &serviceObjectID, ObjectVersion: &objectVersion, AssociationVersion: &associationVersion,
		AssociationRunID: association.SourceRunID, AssociationAttemptID: association.SourceAttemptID,
		MappingID: &mappingID, MappingVersion: &mappingVersion, BindingVersion: &bindingVersion, SnapshotSetID: &snapshotSetID,
		SnapshotCollectedAt: &snapshot.CollectedAt, Status: status, ReasonCode: reason, FactsSHA256: association.FactsSHA256,
		FactRefs: append([]model.ServiceObjectFactRef{}, association.FactRefs...),
		Facts:    append([]auditext.Fact{}, facts...), Checks: append([]auditext.CheckResult{}, checks...),
	}, nil
}
