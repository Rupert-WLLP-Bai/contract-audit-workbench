package model

import (
	"sort"
	"strings"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fault"
)

type OperatorEvaluationInput struct {
	PackID               string                   `json:"pack_id"`
	PackVersion          string                   `json:"pack_version"`
	SourceReviewID       string                   `json:"source_review_id"`
	SourceAttemptID      string                   `json:"source_attempt_id"`
	ContractVersionID    string                   `json:"contract_version_id"`
	ObjectBindingVersion string                   `json:"object_binding_version"`
	SnapshotSetID        string                   `json:"snapshot_set_id"`
	BusinessAt           time.Time                `json:"business_at"`
	EvidenceCutoffAt     *time.Time               `json:"evidence_cutoff_at,omitempty"`
	Scope                *OperatorEvaluationScope `json:"scope,omitempty"`
}

type OperatorEvaluationScope struct {
	Kind             string   `json:"kind"`
	ServiceObjectIDs []string `json:"service_object_ids,omitempty"`
}

type OperatorEvaluationCoverage struct {
	ScopeKind         string `json:"scope_kind"`
	CandidateCount    int    `json:"candidate_count"`
	EvaluatedCount    int    `json:"evaluated_count"`
	UnresolvedCount   int    `json:"unresolved_count"`
	InsufficientCount int    `json:"insufficient_count"`
	Status            string `json:"status"`
	ReasonCode        string `json:"reason_code,omitempty"`
}

type OperatorEvaluationObject struct {
	ObjectRef            string                    `json:"object_ref"`
	Source               *OperatorEvaluationSource `json:"source"`
	ServiceObjectID      *string                   `json:"service_object_id"`
	ObjectVersion        *int                      `json:"object_version"`
	AssociationVersion   *int                      `json:"association_version"`
	AssociationRunID     string                    `json:"association_run_id,omitempty"`
	AssociationAttemptID string                    `json:"association_attempt_id,omitempty"`
	MappingID            *string                   `json:"mapping_id"`
	MappingVersion       *int64                    `json:"mapping_version"`
	BindingVersion       *string                   `json:"binding_version"`
	SnapshotSetID        *string                   `json:"snapshot_set_id"`
	Status               string                    `json:"status"`
	ReasonCode           string                    `json:"reason_code"`
	FactsSHA256          string                    `json:"facts_sha256,omitempty"`
	FactRefs             []ServiceObjectFactRef    `json:"fact_refs"`
	SnapshotCollectedAt  *time.Time                `json:"snapshot_collected_at,omitempty"`
	Facts                []auditext.Fact           `json:"facts"`
	Checks               []auditext.CheckResult    `json:"checks"`
}

type OperatorEvaluationSource struct {
	RunID                string `json:"run_id"`
	AttemptID            string `json:"attempt_id"`
	AssociationRunID     string `json:"association_run_id,omitempty"`
	AssociationAttemptID string `json:"association_attempt_id,omitempty"`
	DocumentID           string `json:"document_id"`
	ParsedDocumentID     string `json:"parsed_document_id"`
	MaterialID           string `json:"material_id"`
	MaterialVersion      int    `json:"material_version"`
	Purpose              string `json:"purpose"`
	Role                 string `json:"role"`
}

func NormalizeEvaluationScope(input OperatorEvaluationInput) (OperatorEvaluationScope, error) {
	scope := OperatorEvaluationScope{Kind: "whole_case"}
	if input.Scope != nil {
		scope = *input.Scope
	}
	if len(scope.ServiceObjectIDs) > 100 {
		return OperatorEvaluationScope{}, fault.TooLarge("单次核验最多包含100个服务对象")
	}
	scope.Kind = strings.TrimSpace(scope.Kind)
	ids := make([]string, 0, len(scope.ServiceObjectIDs))
	seen := make(map[string]struct{}, len(scope.ServiceObjectIDs))
	for _, raw := range scope.ServiceObjectIDs {
		id := strings.TrimSpace(raw)
		if auditext.ValidateID(id) != nil {
			return OperatorEvaluationScope{}, fault.Invalid("核验范围内的服务对象标识无效")
		}
		if _, exists := seen[id]; exists {
			return OperatorEvaluationScope{}, fault.Invalid("核验范围不能重复选择服务对象")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	scope.ServiceObjectIDs = ids
	switch scope.Kind {
	case "whole_case":
		if len(ids) != 0 {
			return OperatorEvaluationScope{}, fault.Invalid("整案核验不能携带单独的服务对象列表")
		}
		if input.ObjectBindingVersion != "" || input.SnapshotSetID != "" || input.ContractVersionID != "" {
			return OperatorEvaluationScope{}, fault.Invalid("整案核验由服务端解析对象与证据，不能只提交单个对象映射")
		}
	case "explicit_subset":
		if len(ids) == 0 {
			return OperatorEvaluationScope{}, fault.Invalid("明确范围至少需要一个服务对象")
		}
		hasLegacyPins := input.ObjectBindingVersion != "" || input.SnapshotSetID != "" || input.ContractVersionID != ""
		if hasLegacyPins && (len(ids) != 1 || input.ObjectBindingVersion == "" || input.SnapshotSetID == "" || input.ContractVersionID == "") {
			return OperatorEvaluationScope{}, fault.Invalid("单对象兼容映射必须同时提供来源合同、映射和快照")
		}
	default:
		return OperatorEvaluationScope{}, fault.Invalid("核验范围类型无效")
	}
	return scope, nil
}

func SummarizeEvaluationObjects(scopeKind string, objects []OperatorEvaluationObject) OperatorEvaluationCoverage {
	coverage := OperatorEvaluationCoverage{ScopeKind: scopeKind, CandidateCount: len(objects)}
	if len(objects) == 0 {
		coverage.Status = "partial"
		coverage.ReasonCode = "no_candidates"
		return coverage
	}
	for _, object := range objects {
		switch object.Status {
		case "evaluated", "not_applicable":
			coverage.EvaluatedCount++
		case "unresolved":
			coverage.UnresolvedCount++
		case "insufficient":
			coverage.InsufficientCount++
		default:
			// Unknown persisted outcomes cannot be interpreted as complete.
			coverage.UnresolvedCount++
			if coverage.ReasonCode == "" {
				coverage.ReasonCode = "unknown_object_status"
			}
		}
	}
	coverage.Status = "complete"
	if coverage.UnresolvedCount > 0 || coverage.InsufficientCount > 0 {
		coverage.Status = "partial"
	}
	return coverage
}

type OperatorEvaluation struct {
	CaseID               string                      `json:"case_id"`
	EvaluationID         string                      `json:"evaluation_id"`
	PackID               string                      `json:"pack_id"`
	PackVersion          string                      `json:"pack_version"`
	SourceReviewID       string                      `json:"source_review_id"`
	SourceAttemptID      string                      `json:"source_attempt_id"`
	ContractVersionID    string                      `json:"contract_version_id,omitempty"`
	RulePackVersion      string                      `json:"rule_pack_version"`
	ObjectBindingVersion string                      `json:"object_binding_version,omitempty"`
	SnapshotSetID        string                      `json:"snapshot_set_id,omitempty"`
	BusinessAt           time.Time                   `json:"business_at"`
	EvidenceCutoffAt     *time.Time                  `json:"evidence_cutoff_at,omitempty"`
	MachineStatus        string                      `json:"machine_status"`
	Checks               []auditext.CheckResult      `json:"checks"`
	CreatedAt            time.Time                   `json:"created_at"`
	Input                auditext.FrozenInput        `json:"-"`
	ResultSHA256         string                      `json:"-"`
	Scope                *OperatorEvaluationScope    `json:"scope,omitempty"`
	Coverage             *OperatorEvaluationCoverage `json:"coverage,omitempty"`
	Objects              []OperatorEvaluationObject  `json:"objects,omitempty"`
}

type EvaluationRecord struct {
	Value         OperatorEvaluation `json:"-"`
	RequestSHA256 string             `json:"-"`
}
