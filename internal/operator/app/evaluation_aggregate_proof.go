package app

import (
	"context"

	"contractaudit/internal/capabilitypack"
	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type AggregateProofFacts[D executionmodel.OriginalDocument] interface {
	EvaluationFrozenFacts[D]
	AggregateSources[D]
}

func PrepareAggregateProof[D executionmodel.OriginalDocument](ctx context.Context, f AggregateProofFacts[D], scope PreviewScope, input model.OperatorEvaluationInput, pack capabilitypack.Manifest) (executionmodel.Attempt, string, string, error) {
	var zero executionmodel.Attempt
	run, attempt, frozen, _, err := ValidateEvaluationFrozen[D](ctx, f, scope, input.SourceReviewID, input.SourceAttemptID, false)
	if err != nil {
		return zero, "", "", err
	}
	if run.CaseID != scope.CaseID || frozen.RunID != input.SourceReviewID || frozen.AttemptID != input.SourceAttemptID {
		return zero, "", "", fault.Conflict("来源审查不属于当前案件")
	}
	version := attempt.RulePackVersion
	if version == "" {
		version = pack.RulePackRef.Version
	}
	if version != pack.RulePackRef.Version || (attempt.RulePackID != "" && attempt.RulePackID != pack.RulePackRef.ID) {
		return zero, "", "", fault.Conflict("来源审查使用的规则包与能力包不一致")
	}
	if err = ValidateAggregateSources(ctx, f, frozen.Documents); err != nil {
		return zero, "", "", fault.Conflict("冻结合同来源未通过完整性校验")
	}
	sha, err := EvaluationRequestSHA(input)
	if err != nil {
		return zero, "", "", err
	}
	return attempt, version, sha, nil
}
func ValidateFreshAggregate[D executionmodel.OriginalDocument](ctx context.Context, f EvaluationFrozenFacts[D], scope PreviewScope, input model.OperatorEvaluationInput, pack capabilitypack.Manifest, outer executionmodel.Attempt) (executionmodel.AttemptInput[D], error) {
	_, attempt, frozen, _, err := ValidateEvaluationFrozen[D](ctx, f, scope, input.SourceReviewID, input.SourceAttemptID, true)
	if err != nil {
		return executionmodel.AttemptInput[D]{}, err
	}
	if *attempt.InputHash != *outer.InputHash || *attempt.OutputHash != *outer.OutputHash {
		return executionmodel.AttemptInput[D]{}, fault.Conflict("来源审查的冻结输入或输出已变化")
	}
	if attempt.RulePackID != "" && attempt.RulePackID != pack.RulePackRef.ID {
		return executionmodel.AttemptInput[D]{}, fault.Conflict("来源审查使用的规则包与能力包不一致")
	}
	return frozen, nil
}
