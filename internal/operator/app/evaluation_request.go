package app

import (
	"strings"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

func PrepareEvaluationRequest(key string, input model.OperatorEvaluationInput, pack capabilitypack.Manifest, evaluator auditext.Evaluator, clock CollectionClock) (string, model.OperatorEvaluationInput, model.OperatorEvaluationScope, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", input, model.OperatorEvaluationScope{}, fault.Invalid("缺少幂等键")
	}
	if evaluator == nil {
		return "", input, model.OperatorEvaluationScope{}, fault.New(503, "fulfillment_unavailable", "履约核验器未配置")
	}
	scope, err := model.NormalizeEvaluationScope(input)
	if err != nil {
		return "", input, scope, err
	}
	input.Scope = &scope
	if input.PackID != pack.PackID || input.PackVersion != pack.Version {
		return "", input, scope, fault.Conflict("能力包版本与请求不一致")
	}
	if input.BusinessAt.IsZero() {
		return "", input, scope, fault.Invalid("业务时间不合法")
	}
	input.BusinessAt = input.BusinessAt.UTC().Truncate(time.Microsecond)
	cutoff := input.BusinessAt.UTC().Truncate(time.Microsecond)
	if input.EvidenceCutoffAt != nil {
		cutoff = input.EvidenceCutoffAt.UTC().Truncate(time.Microsecond)
	}
	input.EvidenceCutoffAt = &cutoff
	if cutoff.After(clock.Now().UTC()) {
		return "", input, scope, fault.Invalid("证据截止时间不能晚于当前受理时间")
	}
	return key, input, scope, nil
}
func EvaluationRequestSHA(input model.OperatorEvaluationInput) (string, error) {
	return collectionHash(input)
}
func ValidateEvaluationScenario(scenario string, pack capabilitypack.Manifest) error {
	if scenario != pack.Scenario {
		return fault.Conflict("能力包场景与案件不一致")
	}
	return nil
}
func UseAggregateEvaluation(scope model.OperatorEvaluationScope, input model.OperatorEvaluationInput) bool {
	return scope.Kind == "whole_case" || input.ObjectBindingVersion == "" || input.SnapshotSetID == "" || input.ContractVersionID == ""
}
func CheckInitialEvaluationReplay(record model.EvaluationRecord, requestSHA string) (model.OperatorEvaluation, error) {
	if record.RequestSHA256 != requestSHA {
		return model.OperatorEvaluation{}, fault.Conflict("同一幂等键对应了不同的核验请求")
	}
	return record.Value, nil
}
func CheckSingleEvaluationReplay(record model.EvaluationRecord, requestSHA, resultSHA string) (model.OperatorEvaluation, error) {
	if record.RequestSHA256 != requestSHA || record.Value.ResultSHA256 != resultSHA {
		return model.OperatorEvaluation{}, fault.Conflict("同一幂等键对应了不同的核验结果")
	}
	return record.Value, nil
}
func CheckAggregateEvaluationReplay(record model.EvaluationRecord, requestSHA string) (model.OperatorEvaluation, error) {
	if record.RequestSHA256 != requestSHA {
		return model.OperatorEvaluation{}, fault.Conflict("同一幂等键对应了不同的核验请求")
	}
	return record.Value, nil
}
