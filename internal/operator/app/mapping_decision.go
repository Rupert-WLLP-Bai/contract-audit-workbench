// File metadata (manifest mirror):
// Purpose: Coordinates independent mapping decisions, exact proposal and impact revalidation, decision receipts and ordered immutable appends or CAS through owned ports.
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
	"context"
	"encoding/json"
	"strings"
	"time"

	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

// PreparedMappingDecision contains the normalized input and its idempotency digest.
type PreparedMappingDecision struct {
	Input         model.OperatorMappingChangeDecisionInput
	Status        string
	RequestSHA256 string
}

func PrepareMappingDecision(caseID, requestID string, input model.OperatorMappingChangeDecisionInput) (PreparedMappingDecision, error) {
	input.Decision = strings.TrimSpace(input.Decision)
	input.Reason = strings.TrimSpace(input.Reason)
	if (input.Decision != "approve" && input.Decision != "reject") || input.Reason == "" || len(input.Reason) > 2000 ||
		input.ExpectedMappingVersion < 1 || input.ExpectedBindingVersion == "" || input.ExpectedObjectVersion < 1 || input.ExpectedAssociationVersion < 1 ||
		!model.ValidOperatorDigest(input.ExpectedAfterSHA256) || !model.ValidOperatorDigest(input.ExpectedImpactSHA256) {
		return PreparedMappingDecision{}, fault.Invalid("决定内容或预期版本无效")
	}
	status := "rejected"
	if input.Decision == "approve" {
		status = "approved"
	}
	digest, err := model.OperatorMappingChangeDecisionRequestSHA256(caseID, requestID, input)
	if err != nil {
		return PreparedMappingDecision{}, err
	}
	return PreparedMappingDecision{Input: input, Status: status, RequestSHA256: digest}, nil
}

// DecisionRepository is bound to the existing authorized transaction.
type DecisionRepository interface {
	MappingRepository
	DecisionReceipt(context.Context, CreateIdentity, string) ([]byte, string, bool, error)
	AppendDecision(context.Context, CreateIdentity, DecisionWrite) error
	AppendVersion(context.Context, CreateIdentity, model.ServiceObjectVersion) error
	AppendAssociation(context.Context, CreateIdentity, model.ServiceObjectContractAssociation) error
	AppendApprovedBinding(context.Context, CreateIdentity, ApprovedBindingWrite) error
	AdvanceObjectBoth(context.Context, CreateIdentity, model.ServiceObjectHead, int, int) (bool, error)
	AdvanceAssociation(context.Context, CreateIdentity, string, int, int, int) (bool, error)
	AdvanceLineage(context.Context, CreateIdentity, string, int64, string, int64, string, time.Time) (bool, error)
}
type DecisionWrite struct {
	Request                                      model.OperatorMappingChangeRecord
	CAS                                          model.OperatorMappingChangeCAS
	DecisionID, Status, Reason, Key, DecisionSHA string
	Actor                                        model.TrustedPrincipalProjection
	Body                                         []byte
	Now                                          time.Time
}
type ApprovedBindingWrite struct {
	Binding         model.OperatorObjectBinding
	DecisionID, SHA string
	Body            []byte
	Now             time.Time
}
type DecisionClock interface {
	MappingClock
	NewDecisionID() (string, error)
	NewBindingID() (string, error)
}

func DecideMapping[D executionmodel.OriginalDocument](ctx context.Context, repo DecisionRepository, facts PreviewFacts[D], basis BasisFacts, scope PreviewScope, actor model.TrustedPrincipalProjection, clock DecisionClock, id CreateIdentity, requestID, key string, prepared PreparedMappingDecision) (model.OperatorMappingChangeDecisionResult, bool, error) {
	var result model.OperatorMappingChangeDecisionResult
	body, hash, found, err := repo.DecisionReceipt(ctx, id, key)
	if err != nil {
		return result, false, err
	}
	if found {
		if hash != prepared.RequestSHA256 {
			return result, false, fault.Conflict("同一幂等键对应了不同的映射决定")
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return result, false, fault.New(500, "integrity_error", "对象映射决定回执损坏")
		}
		return result, true, nil
	}
	rid := ReadIdentity{TenantID: id.TenantID, OrgID: id.OrgID, CaseID: id.CaseID}
	record, err := ReadMappingRequest(ctx, repo, rid, requestID)
	if err != nil {
		return result, false, err
	}
	request := record.Value
	_, found, err = repo.DecisionState(ctx, rid, requestID)
	if err != nil {
		return result, false, err
	}
	if found {
		return result, false, fault.Conflict("该对象映射更正已由其他决定完成")
	}
	if !model.OperatorMappingDecisionIndependent(request, actor) {
		return result, false, fault.Forbidden("更正提出人不能批准或拒绝同一更正")
	}
	tuple, err := LoadMappingTuple(ctx, repo, rid, request.MappingID)
	if err != nil {
		return result, false, err
	}
	cas := model.OperatorMappingChangeCAS{ExpectedMappingVersion: request.ExpectedMappingVersion, ExpectedBindingVersion: request.ExpectedBindingVersion, ExpectedObjectVersion: request.ExpectedObjectVersion, ExpectedAssociationVersion: request.ExpectedAssociationVersion}
	target, targetAfter, err := MappingTarget[D](ctx, repo, facts, scope, actor.UserID, clock, tuple, request.Patch.ContractAssociation)
	if err != nil {
		return result, false, err
	}
	impact, err := model.OperatorMappingImpactWithAssociationSHA256(tuple.Head, tuple.Version, tuple.Identity, tuple.Association, targetAfter)
	if err != nil {
		return result, false, err
	}
	if !model.OperatorMappingCASMatches(tuple.Head, tuple.Version, cas) || request.AfterSHA256 != prepared.Input.ExpectedAfterSHA256 || request.ImpactSHA256 != prepared.Input.ExpectedImpactSHA256 || impact != request.ImpactSHA256 {
		return result, false, fault.Conflict("对象映射版本或影响范围已变化，请重新查看")
	}
	if err = ValidateMappingBasis(ctx, basis, request.BasisRefs, tuple.Association); err != nil {
		return result, false, err
	}
	if request.Patch.Identity != nil {
		a, e := model.CanonicalOperatorMappingRawJSON(request.Patch.Identity.BasisRefs)
		b, e2 := model.CanonicalOperatorMappingRawJSON(request.BasisRefs)
		if e != nil || e2 != nil || !bytes.Equal(a, b) {
			return result, false, fault.Conflict("身份更正依据与提案不一致")
		}
	}
	plan, err := model.PlanOperatorMappingChange(tuple.Head, tuple.Version, cas, request.Patch, "binding_proposal", request.ProposedBy.UserID, clock.Now().UTC())
	if err != nil {
		return result, false, fault.Conflict("对象映射更正摘要与提案内容不一致")
	}
	if err = model.BindOperatorMappingAssociation(&plan, targetAfter); err != nil || plan.AfterSHA256 != request.AfterSHA256 {
		return result, false, fault.Conflict("对象映射更正摘要与提案内容不一致")
	}
	if targetAfter != nil {
		var saved model.OperatorMappingApprovedAfter
		if json.Unmarshal(request.After, &saved) != nil {
			return result, false, fault.Conflict("目标合同关联提案记录损坏")
		}
		digest, e := model.OperatorMappingApprovedDigest(saved.Mapping, saved.Identity, saved.Association)
		if e != nil || digest != request.AfterSHA256 {
			return result, false, fault.Conflict("目标合同关联提案摘要不一致")
		}
	}
	decisionID, err := clock.NewDecisionID()
	if err != nil {
		return result, false, err
	}
	now := clock.Now().UTC().Truncate(time.Microsecond)
	bindingID := tuple.Version.Binding.Version
	mappingVersion := tuple.Head.CurrentVersion
	if prepared.Status == "approved" {
		bindingID, err = clock.NewBindingID()
		if err != nil {
			return result, false, err
		}
		mappingVersion++
	}
	result = model.OperatorMappingChangeDecisionResult{RequestID: requestID, DecisionID: decisionID, Decision: prepared.Status, MappingID: request.MappingID, MappingVersion: mappingVersion, BindingVersion: bindingID}
	response, err := json.Marshal(result)
	if err != nil {
		return result, false, err
	}
	err = repo.AppendDecision(ctx, id, DecisionWrite{Request: record, CAS: cas, DecisionID: decisionID, Status: prepared.Status, Reason: prepared.Input.Reason, Key: key, DecisionSHA: prepared.RequestSHA256, Actor: actor, Body: response, Now: now})
	if err != nil {
		return result, false, err
	}
	if prepared.Status != "approved" {
		return result, false, nil
	}
	plan, err = model.PlanOperatorMappingChange(tuple.Head, tuple.Version, cas, request.Patch, bindingID, actor.UserID, now)
	if err != nil {
		return result, false, fault.Conflict("映射更正内容已失效")
	}
	if err = model.BindOperatorMappingAssociation(&plan, targetAfter); err != nil {
		return result, false, err
	}
	binding := plan.NextVersion.Binding
	objectVersion, associationVersion := request.ExpectedObjectVersion, request.ExpectedAssociationVersion
	if target != nil {
		target.ConfirmedBy, target.ConfirmedAt, target.CreatedAt = actor.UserID, now, now
		target.IdentityChangeDecisionID = decisionID
		target.PayloadSHA256, err = model.CanonicalServiceObjectAssociationSHA256(*target)
		if err != nil {
			return result, false, err
		}
		if err = repo.AppendAssociation(ctx, id, *target); err != nil {
			return result, false, err
		}
		associationVersion = target.AssociationVersion
	}
	if request.Patch.Identity != nil {
		proposed, e := model.ProposedOperatorIdentityVersion(request.ServiceObjectID, tuple.Identity.Version, *request.Patch.Identity, request.Patch.EffectiveFrom)
		if e != nil {
			return result, false, fault.Conflict("身份更正内容已失效")
		}
		approval := verifiedServiceObjectIdentityChange{decisionID: decisionID, expectedObjectVersion: request.ExpectedObjectVersion, expectedAssociation: request.ExpectedAssociationVersion, beforePayloadSHA256: tuple.Identity.PayloadSHA256, afterPayloadSHA256: proposed.PayloadSHA256, effectiveFrom: proposed.EffectiveFrom, basisRefs: append(json.RawMessage(nil), proposed.BasisRefs...), validatedByDecisionSvc: true}
		identityPlan, e := planServiceObjectIdentityChange(model.ServiceObjectHead{ID: request.ServiceObjectID, CurrentVersion: tuple.Identity.Version, CurrentAssociationVersion: request.ExpectedAssociationVersion}, tuple.Identity, tuple.Association, proposed, approval, actor.UserID, now)
		if e != nil {
			return result, false, fault.Conflict("服务对象身份版本已变化")
		}
		identityPlan.ObjectVersion.ChangeDecisionID = decisionID
		if e = repo.AppendVersion(ctx, id, identityPlan.ObjectVersion); e != nil {
			return result, false, e
		}
		identityPlan.Association.IdentityChangeDecisionID = decisionID
		identityPlan.Association.PayloadSHA256, e = model.CanonicalServiceObjectAssociationSHA256(identityPlan.Association)
		if e != nil {
			return result, false, e
		}
		if e = repo.AppendAssociation(ctx, id, identityPlan.Association); e != nil {
			return result, false, e
		}
		ok, e := repo.AdvanceObjectBoth(ctx, id, identityPlan.Head, request.ExpectedObjectVersion, request.ExpectedAssociationVersion)
		if e != nil {
			return result, false, e
		}
		if !ok {
			return result, false, fault.Conflict("服务对象身份版本已被并发更新")
		}
		objectVersion, associationVersion = identityPlan.ObjectVersion.Version, identityPlan.Association.AssociationVersion
	}
	binding.MappingID, binding.MappingVersion = request.MappingID, plan.NextVersion.MappingVersion
	binding.ServiceObjectID, binding.ObjectVersion, binding.AssociationVersion = request.ServiceObjectID, objectVersion, associationVersion
	bindingJSON, err := json.Marshal(binding)
	if err != nil {
		return result, false, err
	}
	if err = repo.AppendApprovedBinding(ctx, id, ApprovedBindingWrite{Binding: binding, DecisionID: decisionID, SHA: plan.AfterSHA256, Body: bindingJSON, Now: now}); err != nil {
		return result, false, err
	}
	if target != nil {
		ok, e := repo.AdvanceAssociation(ctx, id, request.ServiceObjectID, associationVersion, request.ExpectedObjectVersion, request.ExpectedAssociationVersion)
		if e != nil {
			return result, false, e
		}
		if !ok {
			return result, false, fault.Conflict("服务对象合同关联已被并发更新")
		}
	}
	ok, err := repo.AdvanceLineage(ctx, id, request.MappingID, mappingVersion, bindingID, cas.ExpectedMappingVersion, cas.ExpectedBindingVersion, now)
	if err != nil {
		return result, false, err
	}
	if !ok {
		return result, false, fault.Conflict("对象映射版本已被并发更新")
	}
	return result, false, nil
}
