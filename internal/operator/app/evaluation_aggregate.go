// File metadata (manifest mirror):
// Purpose: Evaluates ordered aggregate candidates against exact historical object, mapping, snapshot and source facts while preserving insufficient classifications.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"contractaudit/internal/auditext"
	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type EvaluationObjectRecord struct {
	Result      model.OperatorEvaluationObject
	FactsJSON   []byte
	FactsSHA256 string
}
type AggregateEvidenceFacts[D executionmodel.OriginalDocument] interface {
	AttemptDocuments(string) (string, []executionmodel.FrozenDocument[D], bool)
	VerifySource(context.Context, executionmodel.FrozenDocument[D]) error
}

func aggregateAssociationBeforeCutoff[D executionmodel.OriginalDocument](facts AggregateEvidenceFacts[D], association model.ServiceObjectContractAssociation, cutoff time.Time) bool {
	run, docs, found := facts.AttemptDocuments(association.SourceAttemptID)
	if !found || run != association.SourceRunID {
		return false
	}
	return AssociationEvidenceBeforeCutoff(docs, association, cutoff)
}
func EvaluateAggregateObjects[D executionmodel.OriginalDocument](ctx context.Context, repo AggregateRepository, facts AggregateEvidenceFacts[D], identity ReadIdentity, input model.OperatorEvaluationInput, scope model.OperatorEvaluationScope, candidates []EvaluationCandidate[D], objects []ServiceObjectAtTime, evaluator auditext.Evaluator, base auditext.FrozenInput) ([]EvaluationObjectRecord, error) {
	byRef := make(map[string][]ServiceObjectAtTime)
	byID := make(map[string]ServiceObjectAtTime, len(objects))
	for _, object := range objects {
		byID[object.Head.ID] = object
		if object.Association != nil {
			byRef[object.Association.ObjectRef] = append(byRef[object.Association.ObjectRef], object)
		}
	}
	out := make([]EvaluationObjectRecord, 0, len(candidates))
	for _, candidate := range candidates {
		factsRaw := []byte("[]")
		factsSum := sha256.Sum256(factsRaw)
		factsSHA := hex.EncodeToString(factsSum[:])
		result := model.OperatorEvaluationObject{ObjectRef: candidate.ObjectRef, Status: "unresolved", ReasonCode: candidate.Reason, FactsSHA256: factsSHA, FactRefs: []model.ServiceObjectFactRef{}, Facts: []auditext.Fact{}, Checks: []auditext.CheckResult{}}
		if candidate.Source.Document.OriginalProof().ID != "" && candidate.Source.Material != nil {
			result.Source = &model.OperatorEvaluationSource{RunID: input.SourceReviewID, AttemptID: input.SourceAttemptID, DocumentID: candidate.Source.Document.OriginalProof().ID,
				ParsedDocumentID: candidate.Source.ParsedDocumentID, MaterialID: candidate.Source.Material.MaterialID, MaterialVersion: candidate.Source.Material.Version,
				Purpose: candidate.Source.Material.Purpose, Role: candidate.Source.Document.OriginalProof().Role}
		}
		registered := byRef[candidate.ObjectRef]
		if candidate.ServiceObjectID != "" {
			registered = nil
			if object, ok := byID[candidate.ServiceObjectID]; ok {
				registered = append(registered, object)
			}
		}
		if len(registered) == 0 {
			if result.ReasonCode == "" {
				result.ReasonCode = "service_object_unregistered"
			}
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		if len(registered) > 1 {
			result.ReasonCode = "service_object_identity_ambiguous"
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		object := registered[0]
		id, ver := object.Head.ID, object.Version.Version
		result.ServiceObjectID, result.ObjectVersion = &id, &ver
		if object.Association != nil {
			assocVer := object.Association.AssociationVersion
			result.AssociationVersion = &assocVer
			result.AssociationRunID = object.Association.SourceRunID
			result.AssociationAttemptID = object.Association.SourceAttemptID
			if result.Source != nil {
				result.Source.AssociationRunID = object.Association.SourceRunID
				result.Source.AssociationAttemptID = object.Association.SourceAttemptID
			}
		}
		if object.Version.CreatedAt.After(*base.EvidenceCutoffAt) || object.Association != nil &&
			(object.Association.CreatedAt.After(*base.EvidenceCutoffAt) || object.Association.ConfirmedAt.After(*base.EvidenceCutoffAt)) {
			result.Status, result.ReasonCode = "insufficient", "identity_evidence_after_cutoff"
		}
		if object.Association != nil && result.Status != "insufficient" && !aggregateAssociationBeforeCutoff(facts, *object.Association, *base.EvidenceCutoffAt) {
			result.Status, result.ReasonCode = "insufficient", "contract_evidence_after_cutoff"
		}
		if object.Association != nil && result.Status != "insufficient" {
			facts, err := FactsForAssociation(*object.Association)
			if err != nil {
				return nil, err
			}
			factsRaw = append([]byte(nil), object.Association.FactsCanonicalJSON...)
			factsSHA = object.Association.FactsSHA256
			result.Facts, result.FactsSHA256 = facts, factsSHA
			result.FactRefs = append([]model.ServiceObjectFactRef{}, object.Association.FactRefs...)
		}
		if result.ReasonCode != "" {
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		if object.Version.State != model.ServiceObjectConfirmed || object.Association == nil {
			result.ReasonCode = "service_object_identity_unconfirmed"
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		if candidate.Source.Document.OriginalProof().ID == "" || candidate.Source.Document.OriginalProof().ID != object.Association.DocumentID || candidate.Source.ParsedDocumentID != object.Association.ParsedDocumentID ||
			candidate.Source.Material == nil || candidate.Source.Material.MaterialID != object.Association.MaterialID || candidate.Source.Material.Version != object.Association.MaterialVersion {
			result.ReasonCode = "service_object_source_requires_reassociation"
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		association := *object.Association
		createdAt, dateErr := time.Parse(time.RFC3339Nano, candidate.Source.Document.OriginalProof().CreatedAt)
		parsedAt, parseErr := time.Parse(time.RFC3339Nano, candidate.Source.ParsedCreatedAt)
		if dateErr != nil || parseErr != nil || createdAt.After(*base.EvidenceCutoffAt) || parsedAt.After(*base.EvidenceCutoffAt) {
			result.Status, result.ReasonCode = "insufficient", "contract_evidence_after_cutoff"
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		frozenFacts := result.Facts
		mappings, err := ReadApplicableMappings(ctx, repo, identity, object, input.BusinessAt, *base.EvidenceCutoffAt)
		if err != nil {
			return nil, err
		}
		if len(mappings) == 0 {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.ReasonCode = "operator_mapping_missing"
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		if len(mappings) > 1 {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.ReasonCode = "operator_mapping_ambiguous"
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		mapping := mappings[0]
		mappingID, mappingVersion, bindingVersion := mapping.MappingID, mapping.MappingVersion, mapping.Binding.Version
		result.MappingID = &mappingID
		result.MappingVersion = &mappingVersion
		result.BindingVersion = &bindingVersion
		snapshot, found, err := ReadAggregateSnapshot(ctx, repo, identity.CaseID, mapping.Binding.Version, input.BusinessAt, input.EvidenceCutoffAt)
		if err != nil {
			return nil, err
		}
		if !found {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.Status = "insufficient"
			result.ReasonCode = "external_snapshot_missing"
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		snapshotID := snapshot.SnapshotSetID
		result.SnapshotSetID = &snapshotID
		result.SnapshotCollectedAt = &snapshot.CollectedAt
		if err := facts.VerifySource(ctx, candidate.Source); err != nil {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.ReasonCode = "contract_source_integrity_error"
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		objectFrozen := base
		objectFrozen.ContractVersion = association.DocumentID
		objectFrozen.BindingStatus = auditext.BindingConfirmed
		objectFrozen.BindingVersion = mapping.Binding.Version
		objectFrozen.SnapshotSetID = snapshot.SnapshotSetID
		objectFrozen.ExternalObjectID = mapping.Binding.ExternalObject.ExternalObjectID
		objectFrozen.SourceSystem = snapshot.SourceSystem
		objectFrozen.Facts = frozenFacts
		if err := objectFrozen.Validate(); err != nil {
			return nil, fault.Conflict("对象核验冻结输入不完整")
		}
		checks, err := evaluator.Evaluate(ctx, objectFrozen, ObservationsForEvaluation(snapshot, input.BusinessAt, *base.EvidenceCutoffAt))
		if errors.Is(err, auditext.ErrUnauthorized) {
			return nil, fault.Forbidden("当前授权不能核验该案件")
		}
		if err != nil {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.Status = "insufficient"
			result.ReasonCode = "evaluation_unavailable"
			out = append(out, EvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		machineStatus := "completed"
		for _, check := range checks {
			if err := check.Validate(); err != nil {
				return nil, fault.New(502, "fulfillment_invalid", "核验器返回了不合法的结果")
			}
			if check.MachineStatus != auditext.MachineCompleted {
				machineStatus = "partial"
			}
		}
		obj, err := EvaluationObjectForAssociation(scope, input, mapping.Binding, association, snapshot, candidate.Source.Material.Purpose, candidate.Source.Document.OriginalProof().Role, frozenFacts, checks, machineStatus)
		if err != nil {
			return nil, err
		}
		obj.Source.RunID = input.SourceReviewID
		obj.Source.AttemptID = input.SourceAttemptID
		out = append(out, EvaluationObjectRecord{Result: obj, FactsJSON: factsRaw, FactsSHA256: factsSHA})
	}
	return out, nil
}
