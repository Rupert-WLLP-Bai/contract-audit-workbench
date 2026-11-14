// File metadata (manifest mirror):
// Purpose: Coordinates single-object binding, snapshot, frozen terminal proof, association and source selection before evaluation outside the write transaction.
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

	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type SingleEvaluationFacts[D executionmodel.OriginalDocument] interface {
	EvaluationFrozenFacts[D]
	SupplementSources[D]
	ReadBinding(context.Context, string) (model.OperatorObjectBinding, error)
	ReadSnapshot(context.Context, string) (model.OperatorSnapshotSet, error)
	ReadAssociation(context.Context, string, int) (model.ServiceObjectContractAssociation, error)
	RunConfiguration(string) AggregateConfiguration
}

func PrepareSingleFlow[D executionmodel.OriginalDocument](ctx context.Context, f SingleEvaluationFacts[D], id CreateIdentity, caseScope PreviewScope, grant string, input model.OperatorEvaluationInput, scope model.OperatorEvaluationScope, pack capabilitypack.Manifest, evaluator auditext.Evaluator, key string, clock EvaluationClock) (SingleWriteInput, error) {
	var out SingleWriteInput
	binding, err := f.ReadBinding(ctx, input.ObjectBindingVersion)
	if err != nil {
		return out, err
	}
	if err = ValidateSingleBinding(binding, input.BusinessAt); err != nil {
		return out, err
	}
	snapshot, err := f.ReadSnapshot(ctx, input.SnapshotSetID)
	if err != nil {
		return out, err
	}
	if err = ValidateSingleSnapshot(snapshot, binding, input.BusinessAt); err != nil {
		return out, err
	}
	run, attempt, frozenInput, _, err := ValidateEvaluationFrozen[D](ctx, f, caseScope, input.SourceReviewID, input.SourceAttemptID, false)
	if err != nil {
		return out, err
	}
	if run.CaseID != id.CaseID {
		return out, fault.Conflict("来源审查不属于当前案件")
	}
	if !FrozenHasContract(frozenInput.Documents, input.ContractVersionID) {
		return out, fault.Conflict("合同版本不在冻结输入中")
	}
	if binding.ServiceObjectID == "" || binding.ObjectVersion < 1 || binding.AssociationVersion < 1 {
		return out, fault.Conflict("历史对象映射尚未绑定已确认的服务对象，不能发起新的核验")
	}
	association, err := f.ReadAssociation(ctx, binding.ServiceObjectID, binding.AssociationVersion)
	if err != nil {
		return out, err
	}
	if !AssociationMatchesFrozen(association, binding, input, frozenInput.Documents) {
		return out, fault.Conflict("对象映射未固定到请求所选的合同材料、解析和审查尝试")
	}
	cutoff := *input.EvidenceCutoffAt
	if association.CreatedAt.After(cutoff) || association.ConfirmedAt.After(cutoff) || binding.CreatedAt.After(cutoff) || binding.ConfirmedAt.After(cutoff) {
		return out, fault.Conflict("对象关联或映射晚于证据截止时间")
	}
	rulePackVersion := attempt.RulePackVersion
	if rulePackVersion == "" {
		rulePackVersion = pack.RulePackRef.Version
	}
	if rulePackVersion != pack.RulePackRef.Version || (attempt.RulePackID != "" && attempt.RulePackID != pack.RulePackRef.ID) {
		return out, fault.Conflict("来源审查使用的规则包与能力包不一致")
	}
	purpose, role, err := SelectSingleSource(ctx, f, frozenInput.Documents, association, cutoff)
	if err != nil {
		return out, err
	}
	config := f.RunConfiguration(input.SourceReviewID)
	prepared, err := PrepareSingleEvaluation(ctx, evaluator, id, grant, config, input, scope, auditext.PackRef{ID: pack.PackID, Version: pack.Version}, rulePackVersion, binding, association, snapshot)
	if err != nil {
		return out, err
	}
	evaluationID, err := clock.NewEvaluationID()
	if err != nil {
		return out, err
	}
	return SingleWriteInput{Identity: id, Input: input, Scope: scope, Pack: auditext.PackRef{ID: pack.PackID, Version: pack.Version}, RulePackVersion: rulePackVersion, EvaluationID: evaluationID, Key: key, Purpose: purpose, Role: role, Binding: binding, Association: association, Snapshot: snapshot, Prepared: prepared}, nil
}
