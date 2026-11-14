// File metadata (manifest mirror):
// Purpose: Builds aggregate candidates and frozen inputs, evaluates within the existing caller transaction and appends all ordered results before constructing the response.
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
	"encoding/json"
	"time"

	"contractaudit/internal/auditext"
	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type AggregateRunRepository interface {
	AggregateRepository
	AggregateObjectRepository
	EvaluationWriter
}
type AggregateRunFacts[D executionmodel.OriginalDocument] interface {
	AggregateEvidenceFacts[D]
	SupplementSources[D]
}
type EvaluationClock interface {
	Now() time.Time
	NewEvaluationID() (string, error)
}
type AggregateConfiguration struct{ Model, Planner, Parser string }

func RunAggregateAfterProof[D executionmodel.OriginalDocument](ctx context.Context, repo AggregateRunRepository, facts AggregateRunFacts[D], id CreateIdentity, scope PreviewScope, grantVersion string, docs []executionmodel.FrozenDocument[D], evaluations []executionmodel.ConstraintEval, config AggregateConfiguration, input model.OperatorEvaluationInput, selected model.OperatorEvaluationScope, pack auditext.PackRef, rulePackVersion string, evaluator auditext.Evaluator, key, requestSHA string, clock EvaluationClock) (model.OperatorEvaluation, error) {
	var out model.OperatorEvaluation
	candidates := EvaluationCandidates(docs, evaluations, input.SourceReviewID, input.SourceAttemptID)
	supplements, err := SupplementCandidates(ctx, facts, scope, docs)
	if err != nil {
		return out, err
	}
	candidates = MergeSupplementCandidates(candidates, supplements)
	rid := ReadIdentity{TenantID: id.TenantID, OrgID: id.OrgID, CaseID: id.CaseID}
	objects, err := ReadServiceObjectsAt(ctx, repo, rid, input.BusinessAt, *input.EvidenceCutoffAt)
	if err != nil {
		return out, err
	}
	candidates = NormalizeEvaluationCandidates(UnionRegisteredServiceObjects(candidates, objects, selected))
	if len(candidates) > 100 {
		return out, fault.TooLarge("本次整案核验候选对象超过100个")
	}
	if config.Model == "" {
		config.Model = "not_used"
	}
	if config.Planner == "" {
		config.Planner = "not_used"
	}
	if config.Parser == "" {
		config.Parser = "not_used"
	}
	frozen := auditext.FrozenInput{Scope: auditext.AuthorizedScope{TenantID: id.TenantID, CaseID: id.CaseID, ActorID: id.ActorID, GrantVersion: grantVersion}, Pack: pack, RulePackVersion: rulePackVersion, ModelConfigurationID: config.Model, PlannerConfigurationID: config.Planner, ParserConfigurationID: config.Parser, BusinessAt: input.BusinessAt, EvidenceCutoffAt: input.EvidenceCutoffAt, EvaluationScope: &auditext.EvaluationScope{Kind: selected.Kind, ServiceObjectIDs: append([]string(nil), selected.ServiceObjectIDs...)}, Facts: []auditext.Fact{}}
	if err = frozen.Validate(); err != nil {
		return out, fault.Conflict("整案核验冻结输入不完整")
	}
	records, err := EvaluateAggregateObjects(ctx, repo, facts, rid, input, selected, candidates, objects, evaluator, frozen)
	if err != nil {
		return out, err
	}
	publicObjects := make([]model.OperatorEvaluationObject, 0, len(records))
	for _, record := range records {
		publicObjects = append(publicObjects, record.Result)
	}
	coverage := model.SummarizeEvaluationObjects(selected.Kind, publicObjects)
	status := "completed"
	if coverage.Status != "complete" {
		status = "partial"
	}
	checks := []auditext.CheckResult{}
	checksJSON, err := json.Marshal(checks)
	if err != nil {
		return out, err
	}
	objectsJSON, err := json.Marshal(publicObjects)
	if err != nil {
		return out, err
	}
	resultSum := sha256.Sum256(objectsJSON)
	resultSHA := hex.EncodeToString(resultSum[:])
	inputJSON, err := json.Marshal(frozen)
	if err != nil {
		return out, err
	}
	evaluationID, err := clock.NewEvaluationID()
	if err != nil {
		return out, err
	}
	now := clock.Now().UTC().Truncate(time.Microsecond)
	value := model.OperatorEvaluation{CaseID: id.CaseID, EvaluationID: evaluationID, PackID: pack.ID, PackVersion: pack.Version, SourceReviewID: input.SourceReviewID, SourceAttemptID: input.SourceAttemptID, RulePackVersion: rulePackVersion, BusinessAt: input.BusinessAt, MachineStatus: status, ResultSHA256: resultSHA, CreatedAt: now}
	if err = repo.AppendEvaluation(ctx, EvaluationWrite{Identity: id, Value: value, Key: key, RequestSHA: requestSHA, InputJSON: inputJSON, ResultJSON: checksJSON, Aggregate: true}); err != nil {
		return out, err
	}
	for i, record := range records {
		if err = WriteEvaluationObject(ctx, repo, id, evaluationID, i+1, record.Result, record.FactsJSON, record.FactsSHA256); err != nil {
			return out, err
		}
	}
	out = model.OperatorEvaluation{CaseID: id.CaseID, EvaluationID: evaluationID, PackID: pack.ID, PackVersion: pack.Version, SourceReviewID: input.SourceReviewID, SourceAttemptID: input.SourceAttemptID, RulePackVersion: rulePackVersion, BusinessAt: input.BusinessAt.UTC(), EvidenceCutoffAt: input.EvidenceCutoffAt, MachineStatus: status, Checks: checks, CreatedAt: now, Input: frozen, ResultSHA256: resultSHA, Scope: &selected, Coverage: &coverage, Objects: publicObjects}
	return out, nil
}
