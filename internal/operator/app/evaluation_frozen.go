package app

import (
	"context"

	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
)

type EvaluationFrozenFacts[D executionmodel.OriginalDocument] interface {
	Run(string) (executionmodel.Run, bool)
	Attempt(string) (executionmodel.Attempt, bool)
	Input(string) (executionmodel.AttemptInput[D], bool)
	SnapshotForHash(string) PreviewSnapshot
	Selections(string) []executionmodel.AttemptParseSelection
	VerifyDocuments(context.Context, []executionmodel.FrozenDocument[D]) error
	AgentDigest(context.Context, string) (string, error)
	OutputRows(string, string) ([]executionmodel.Finding, []executionmodel.ConstraintEval)
}

func ValidateEvaluationFrozen[D executionmodel.OriginalDocument](ctx context.Context, f EvaluationFrozenFacts[D], scope PreviewScope, runID, attemptID string, inside bool) (executionmodel.Run, executionmodel.Attempt, executionmodel.AttemptInput[D], rulepack.Manifest, error) {
	var zr executionmodel.Run
	var za executionmodel.Attempt
	var zi executionmodel.AttemptInput[D]
	var zm rulepack.Manifest
	run, ok := f.Run(runID)
	if !ok || !executionmodel.RunMatchesCase(run, scope.CaseID) {
		return zr, za, zi, zm, fault.Conflict("执行记录不存在，或不属于当前案件")
	}
	attempt, ok := f.Attempt(attemptID)
	if !ok || !executionmodel.AttemptTerminalForRun(attempt, runID) {
		return zr, za, zi, zm, fault.Conflict("这次执行尚未形成可比较的最终证据")
	}
	if !executionmodel.HasFrozenAttemptHashes(attempt) {
		return zr, za, zi, zm, fault.Conflict("之前的执行记录缺少保存的输入或输出编号")
	}
	input, ok := f.Input(attemptID)
	if !ok || input.RunID != runID || input.AttemptID != attemptID || input.InputHash != *attempt.InputHash {
		if inside {
			return zr, za, zi, zm, fault.Conflict("来源审查的冻结输入未通过校验")
		}
		return zr, za, zi, zm, fault.Conflict("这次执行保存的输入不存在或编号不匹配")
	}
	snapshot := f.SnapshotForHash(runID)
	hash, err := executionmodel.FrozenInputHash(snapshot, input.Documents)
	if err != nil || hash != input.InputHash || !executionmodel.FrozenInputMatchesSnapshot(snapshot, input.Documents) || !executionmodel.AttemptSelectionsMatchFrozen(f.Selections(attemptID), input.Documents) {
		if inside {
			return zr, za, zi, zm, fault.Conflict("来源审查的冻结输入未通过校验")
		}
		return zr, za, zi, zm, fault.Conflict("这次执行保存的输入未通过完整性校验")
	}
	if err = f.VerifyDocuments(ctx, input.Documents); err != nil {
		if inside {
			return zr, za, zi, zm, fault.Conflict("来源审查的冻结材料未通过校验")
		}
		return zr, za, zi, zm, fault.Conflict("这次执行使用的原件或解析结果编号未通过校验")
	}
	digest, err := f.AgentDigest(ctx, attemptID)
	if err != nil {
		if inside {
			return zr, za, zi, zm, fault.Conflict("来源审查的冻结输出未通过校验")
		}
		return zr, za, zi, zm, fault.Conflict("这次执行的智能审查记录未通过校验")
	}
	findings, evaluations := f.OutputRows(runID, attemptID)
	hash, err = executionmodel.AttemptOutputHash(findings, evaluations, attempt.RulePackHash, attempt.RulePackSnapshotDigest, attempt.RulePackCompilerRef, digest)
	if err != nil || hash != *attempt.OutputHash {
		if inside {
			return zr, za, zi, zm, fault.Conflict("来源审查的冻结输出未通过校验")
		}
		return zr, za, zi, zm, fault.Conflict("这次执行的规则结果摘要未通过校验")
	}
	manifest, err := executionmodel.FrozenRulePack(attempt, executionmodel.FrozenRulePackScope{TenantID: scope.TenantID, BusinessLine: scope.BusinessLine, Scenario: scope.Scenario, ContractType: scope.ContractType})
	if err != nil {
		if inside {
			return zr, za, zi, zm, fault.Conflict("来源审查缺少有效的冻结规则包")
		}
		return zr, za, zi, zm, fault.Conflict("之前的执行记录缺少有效的规则包记录")
	}
	return run, attempt, input, manifest, nil
}
