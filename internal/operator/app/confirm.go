package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type ConfirmPrepared struct {
	Identity                  CreateIdentity
	ObjectID, Key, RequestSHA string
	Input                     model.ServiceObjectAssociationConfirmInput
}
type ConfirmRepository interface {
	ReadConfirmReceipt(context.Context, CreateIdentity, string) (Receipt, bool, error)
	LockHead(context.Context, CreateIdentity, string) (model.ServiceObjectHead, bool, error)
	HasMapping(context.Context, CreateIdentity, string) (bool, error)
	ReadConfirmVersion(context.Context, CreateIdentity, string, int) (VersionRow, error)
	AppendVersion(context.Context, CreateIdentity, model.ServiceObjectVersion) error
	AppendAssociation(context.Context, CreateIdentity, model.ServiceObjectContractAssociation) error
	AdvanceHead(context.Context, CreateIdentity, model.ServiceObjectHead, int, int) (bool, error)
	AppendConfirmReceipt(context.Context, CreateIdentity, string, string, []byte, string, time.Time) error
}

func PrepareConfirm(ctx context.Context, repo ConfirmRepository, id CreateIdentity, objectID, key string, input model.ServiceObjectAssociationConfirmInput) (ConfirmPrepared, model.ServiceObjectMutationResult, bool, error) {
	var empty model.ServiceObjectMutationResult
	objectID = strings.TrimSpace(objectID)
	input.ObjectRef = strings.TrimSpace(input.ObjectRef)
	input.Source.RunID = strings.TrimSpace(input.Source.RunID)
	input.Source.AttemptID = strings.TrimSpace(input.Source.AttemptID)
	input.Source.MaterialID = strings.TrimSpace(input.Source.MaterialID)
	input.Source.DocumentID = strings.TrimSpace(input.Source.DocumentID)
	input.Source.ParsedDocumentID = strings.TrimSpace(input.Source.ParsedDocumentID)
	if objectID == "" || !input.Confirmed || input.ExpectedObjectVersion < 1 || input.ExpectedAssociationVersion < 0 || input.ObjectRef == "" || !model.ValidServiceObjectSHA256(input.FactsSHA256) || !model.ValidServiceObjectSHA256(input.CandidatesSHA256) {
		return ConfirmPrepared{}, empty, false, fault.Invalid("服务对象关联版本或证据标识无效")
	}
	payload := struct {
		ServiceObjectID string                                     `json:"service_object_id"`
		Input           model.ServiceObjectAssociationConfirmInput `json:"input"`
	}{objectID, input}
	hash, err := ServiceObjectRequestSHA256(id, "confirm_association", payload)
	if err != nil {
		return ConfirmPrepared{}, empty, false, err
	}
	receipt, found, err := repo.ReadConfirmReceipt(ctx, id, key)
	if err != nil {
		return ConfirmPrepared{}, empty, false, err
	}
	if found {
		result, err := decodeServiceObjectReceipt(receipt, hash)
		if err != nil {
			return ConfirmPrepared{}, empty, false, err
		}
		return ConfirmPrepared{}, result, true, nil
	}

	return ConfirmPrepared{Identity: id, ObjectID: objectID, Key: key, RequestSHA: hash, Input: input}, empty, false, nil
}
func ConfirmAfterMiss[D executionmodel.OriginalDocument](ctx context.Context, repo ConfirmRepository, facts PreviewFacts[D], scope PreviewScope, p ConfirmPrepared) (model.ServiceObjectMutationResult, error) {
	var empty model.ServiceObjectMutationResult
	head, found, err := repo.LockHead(ctx, p.Identity, p.ObjectID)
	if err != nil {
		return empty, err
	}
	if !found {
		return empty, fault.NotFound("没有找到服务对象")
	}
	if head.CurrentAssociationVersion > 0 {
		exists, err := repo.HasMapping(ctx, p.Identity, p.ObjectID)
		if err != nil {
			return empty, err
		}
		if exists {
			return empty, fault.Conflict("已映射对象的合同关联变更须通过映射异人审批流程")
		}
	}
	row, err := repo.ReadConfirmVersion(ctx, p.Identity, head.ID, head.CurrentVersion)
	if err != nil {
		return empty, err
	}
	current, err := decodeVersionRow(row)
	if err != nil {
		return empty, err
	}
	preview, err := Preview[D](ctx, facts, scope, head, p.Input.Source)
	if err != nil {
		return empty, err
	}
	var option *model.ServiceObjectAssociationOption
	for i := range preview.Options {
		if preview.Options[i].ObjectRef == p.Input.ObjectRef {
			option = &preview.Options[i]
			break
		}
	}
	if option == nil || option.FactsSHA256 != p.Input.FactsSHA256 || preview.CandidatesSHA256 != p.Input.CandidatesSHA256 {
		return empty, fault.Conflict("合同对象候选已变化，请重新预览")
	}
	association := model.ServiceObjectContractAssociation{MaterialID: preview.MaterialID, MaterialVersion: preview.MaterialVersion, DocumentID: preview.DocumentID, ParsedDocumentID: preview.ParsedDocumentID, SourceRunID: preview.RunID, SourceAttemptID: preview.AttemptID, ObjectRef: option.ObjectRef, FactRefs: option.Facts, BasisRefs: option.BasisRefs}
	plan, err := model.PlanServiceObjectAssociation(head, current, model.ServiceObjectAssociationCandidate{ExpectedObjectVersion: p.Input.ExpectedObjectVersion, ExpectedAssociationVersion: p.Input.ExpectedAssociationVersion, Confirmed: true, Association: association}, p.Identity.ActorID, facts.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return empty, fault.Conflict("服务对象身份或关联已变化，请刷新后重试")
	}
	if plan.ObjectVersion.Version != current.Version {
		plan.ObjectVersion.PayloadSHA256, err = model.CanonicalServiceObjectIdentitySHA256(plan.ObjectVersion)
		if err != nil {
			return empty, err
		}
		if err = repo.AppendVersion(ctx, p.Identity, plan.ObjectVersion); err != nil {
			return empty, err
		}
	}
	plan.Association.PayloadSHA256, err = model.CanonicalServiceObjectAssociationSHA256(plan.Association)
	if err != nil {
		return empty, err
	}
	if err = repo.AppendAssociation(ctx, p.Identity, plan.Association); err != nil {
		return empty, err
	}
	advanced, err := repo.AdvanceHead(ctx, p.Identity, plan.Head, p.Input.ExpectedObjectVersion, p.Input.ExpectedAssociationVersion)
	if err != nil {
		return empty, err
	}
	if !advanced {
		return empty, fault.Conflict("服务对象身份或关联已变化，请刷新后重试")
	}
	result := model.ServiceObjectMutationResult{ServiceObjectID: p.ObjectID, Version: plan.Head.CurrentVersion, AssociationVersion: plan.Head.CurrentAssociationVersion}
	body, err := json.Marshal(result)
	if err != nil {
		return empty, err
	}
	if err = repo.AppendConfirmReceipt(ctx, p.Identity, p.Key, p.RequestSHA, body, p.ObjectID, facts.Now().UTC().Truncate(time.Microsecond)); err != nil {
		return empty, err
	}
	return result, nil
}
