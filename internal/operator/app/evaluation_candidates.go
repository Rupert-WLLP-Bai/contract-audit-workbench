package app

import (
	"sort"
	"strings"
	"time"

	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/operator/model"
)

type EvaluationCandidate[D executionmodel.OriginalDocument] struct {
	ObjectRef       string
	ServiceObjectID string
	Source          executionmodel.FrozenDocument[D]
	Reason          string
}
type ServiceObjectAtTime struct {
	Head        model.ServiceObjectHead
	Version     model.ServiceObjectVersion
	Association *model.ServiceObjectContractAssociation
}

func IsContractMaterial[D executionmodel.OriginalDocument](frozen executionmodel.FrozenDocument[D]) bool {
	return frozen.Material != nil &&
		((frozen.Material.Purpose == "primary_contract" && frozen.Document.OriginalProof().Role == "draft_contract") ||
			(frozen.Material.Purpose == "supplement" && frozen.Document.OriginalProof().Role == "attachment"))
}

func IsContractSource[D executionmodel.OriginalDocument](frozen executionmodel.FrozenDocument[D]) bool {
	return IsContractMaterial(frozen) && frozen.Ready && frozen.ParsedDocumentID != ""
}

func EvaluationCandidates[D executionmodel.OriginalDocument](documents []executionmodel.FrozenDocument[D], evaluations []executionmodel.ConstraintEval, runID, attemptID string) []EvaluationCandidate[D] {
	sources := make(map[string]executionmodel.FrozenDocument[D])
	unavailable := make([]EvaluationCandidate[D], 0)
	for _, frozen := range documents {
		if IsContractSource(frozen) {
			sources[frozen.Document.OriginalProof().ID+"\x00"+frozen.ParsedDocumentID] = frozen
		} else if IsContractMaterial(frozen) {
			unavailable = append(unavailable, EvaluationCandidate[D]{ObjectRef: "unresolved-source:" + frozen.Document.OriginalProof().ID, Reason: "contract_source_not_ready"})
		}
	}
	if len(sources) == 0 {
		return NormalizeEvaluationCandidates(unavailable)
	}
	byKey := make(map[string]EvaluationCandidate[D])
	referencedSources := make(map[string]bool)
	for _, evaluation := range evaluations {
		if evaluation.RunID != runID || evaluation.AttemptID != attemptID {
			continue
		}
		refs := uniqueSortedStrings(evaluation.ObjectRefs)
		if len(refs) == 0 {
			continue
		}
		evidenceSources := make(map[string]executionmodel.FrozenDocument[D])
		for _, evidence := range evaluation.Evidence {
			key := evidence.Ref.DocumentID + "\x00" + evidence.Ref.ParsedDocumentID
			if source, ok := sources[key]; ok {
				referencedSources[key] = true
				evidenceSources[key] = source
			}
		}
		selected := evidenceSources
		missingSourceEvidence := len(selected) == 0
		ambiguousSource := false
		if missingSourceEvidence {
			selected = sources
			ambiguousSource = len(sources) > 1
		}
		for _, ref := range refs {
			for _, source := range selected {
				key := ref + "\x00" + source.Document.OriginalProof().ID + "\x00" + source.ParsedDocumentID
				reason := ""
				if ambiguousSource {
					reason = "contract_object_source_ambiguous"
				}
				if missingSourceEvidence && !ambiguousSource {
					reason = "contract_object_source_evidence_missing"
				}
				byKey[key] = EvaluationCandidate[D]{ObjectRef: ref, Source: source, Reason: reason}
			}
		}
	}
	for key, source := range sources {
		if !referencedSources[key] {
			ref := "unresolved-source:" + source.Document.OriginalProof().ID
			byKey[ref+"\x00"+key] = EvaluationCandidate[D]{ObjectRef: ref, Source: source, Reason: "contract_object_candidates_missing"}
		}
	}
	refSources := make(map[string]map[string]struct{})
	for _, candidate := range byKey {
		key := candidate.Source.Document.OriginalProof().ID + "\x00" + candidate.Source.ParsedDocumentID
		if refSources[candidate.ObjectRef] == nil {
			refSources[candidate.ObjectRef] = make(map[string]struct{})
		}
		refSources[candidate.ObjectRef][key] = struct{}{}
	}
	for key, candidate := range byKey {
		if len(refSources[candidate.ObjectRef]) > 1 {
			candidate.Reason = "contract_object_source_ambiguous"
			byKey[key] = candidate
		}
	}
	out := append(make([]EvaluationCandidate[D], 0, len(byKey)+len(unavailable)), unavailable...)
	for _, candidate := range byKey {
		out = append(out, candidate)
	}
	return NormalizeEvaluationCandidates(out)
}

func MergeSupplementCandidates[D executionmodel.OriginalDocument](candidates, supplements []EvaluationCandidate[D]) []EvaluationCandidate[D] {
	identified := make(map[string]bool)
	for _, candidate := range supplements {
		identified[candidate.Source.Document.OriginalProof().ID] = true
	}
	merged := make([]EvaluationCandidate[D], 0, len(candidates)+len(supplements))
	for _, candidate := range candidates {
		if candidate.Reason == "contract_object_candidates_missing" && identified[candidate.Source.Document.OriginalProof().ID] {
			continue
		}
		merged = append(merged, candidate)
	}
	merged = append(merged, supplements...)
	byRef := make(map[string]map[string]bool)
	for _, candidate := range merged {
		if candidate.Source.Document.OriginalProof().ID == "" {
			continue
		}
		if byRef[candidate.ObjectRef] == nil {
			byRef[candidate.ObjectRef] = make(map[string]bool)
		}
		byRef[candidate.ObjectRef][candidate.Source.Document.OriginalProof().ID+"\x00"+candidate.Source.ParsedDocumentID] = true
	}
	for i := range merged {
		if len(byRef[merged[i].ObjectRef]) > 1 {
			merged[i].Reason = "contract_object_source_ambiguous"
		}
	}
	return merged
}

func NormalizeEvaluationCandidates[D executionmodel.OriginalDocument](candidates []EvaluationCandidate[D]) []EvaluationCandidate[D] {
	byKey := make(map[string]EvaluationCandidate[D], len(candidates))
	for _, candidate := range candidates {
		key := candidate.ObjectRef + "\x00" + candidate.ServiceObjectID
		if candidate.Source.Document.OriginalProof().ID != "" {
			key += "\x00" + candidate.Source.Document.OriginalProof().ID + "\x00" + candidate.Source.ParsedDocumentID
		}
		if previous, ok := byKey[key]; ok && previous.Reason != "" {
			candidate.Reason = previous.Reason
		}
		byKey[key] = candidate
	}
	out := make([]EvaluationCandidate[D], 0, len(byKey))
	for _, candidate := range byKey {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ObjectRef != out[j].ObjectRef {
			return out[i].ObjectRef < out[j].ObjectRef
		}
		if out[i].ServiceObjectID != out[j].ServiceObjectID {
			return out[i].ServiceObjectID < out[j].ServiceObjectID
		}
		if out[i].Source.Document.OriginalProof().ID != out[j].Source.Document.OriginalProof().ID {
			return out[i].Source.Document.OriginalProof().ID < out[j].Source.Document.OriginalProof().ID
		}
		return out[i].Source.ParsedDocumentID < out[j].Source.ParsedDocumentID
	})
	return out
}

func UnionRegisteredServiceObjects[D executionmodel.OriginalDocument](candidates []EvaluationCandidate[D], objects []ServiceObjectAtTime, scope model.OperatorEvaluationScope) []EvaluationCandidate[D] {
	selected := make(map[string]bool, len(scope.ServiceObjectIDs))
	for _, id := range scope.ServiceObjectIDs {
		selected[id] = true
	}
	byRef := make(map[string][]ServiceObjectAtTime)
	for _, object := range objects {
		if object.Association != nil {
			byRef[object.Association.ObjectRef] = append(byRef[object.Association.ObjectRef], object)
		}
	}
	covered := make(map[string]bool)
	out := make([]EvaluationCandidate[D], 0, len(candidates)+len(objects))
	for _, candidate := range candidates {
		matches := byRef[candidate.ObjectRef]
		if len(matches) == 0 && scope.Kind == "whole_case" {
			out = append(out, candidate)
		}
		for _, object := range matches {
			if scope.Kind == "explicit_subset" && !selected[object.Head.ID] {
				continue
			}
			row := candidate
			row.ServiceObjectID = object.Head.ID
			if len(matches) > 1 {
				row.Reason = "service_object_identity_ambiguous"
			}
			out = append(out, row)
			covered[object.Head.ID] = true
		}
	}
	for _, object := range objects {
		id := object.Head.ID
		if (scope.Kind == "explicit_subset" && !selected[id]) || covered[id] {
			continue
		}
		ref := "service_object:" + id
		reason := "service_object_contract_association_missing"
		if object.Association != nil {
			ref = object.Association.ObjectRef
			reason = "service_object_source_requires_reassociation"
		}
		if object.Version.State != model.ServiceObjectConfirmed {
			reason = "service_object_identity_unconfirmed"
		}
		out = append(out, EvaluationCandidate[D]{ObjectRef: ref, ServiceObjectID: id, Reason: reason})
		covered[id] = true
	}
	for _, id := range scope.ServiceObjectIDs {
		if !covered[id] {
			out = append(out, EvaluationCandidate[D]{ObjectRef: "service_object:" + id, ServiceObjectID: id, Reason: "service_object_source_unresolved"})
		}
	}
	return out
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func AssociationEvidenceBeforeCutoff[D executionmodel.OriginalDocument](documents []executionmodel.FrozenDocument[D], association model.ServiceObjectContractAssociation, cutoff time.Time) bool {
	for _, frozen := range documents {
		if !IsContractSource(frozen) || frozen.Document.OriginalProof().ID != association.DocumentID || frozen.ParsedDocumentID != association.ParsedDocumentID || frozen.Material.MaterialID != association.MaterialID || frozen.Material.Version != association.MaterialVersion {
			continue
		}
		originalAt, originalErr := time.Parse(time.RFC3339Nano, frozen.Document.OriginalProof().CreatedAt)
		parsedAt, parsedErr := time.Parse(time.RFC3339Nano, frozen.ParsedCreatedAt)
		return originalErr == nil && parsedErr == nil && !originalAt.After(cutoff) && !parsedAt.After(cutoff)
	}
	return false
}
