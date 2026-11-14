package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type MappingRepository interface {
	ReadRepository
	ProposalReceipt(context.Context, CreateIdentity, string) ([]byte, string, bool, error)
	AppendProposal(context.Context, CreateIdentity, string, model.OperatorMappingChangeRecord, []byte, []byte, string, []byte) error
	RequestRaw(context.Context, ReadIdentity, string) (MappingRequestRow, bool, error)
	DecisionState(context.Context, ReadIdentity, string) (string, bool, error)
	DecisionRaw(context.Context, ReadIdentity, string) (model.OperatorMappingChangeDecision, bool, error)
}
type MappingRequestRow struct {
	Record                      model.OperatorMappingChangeRecord
	Before, After, Patch, Basis []byte
}
type MappingClock interface {
	Now() time.Time
	NewRequestID() (string, error)
}
type BasisFacts interface {
	VerifyBasis(context.Context, json.RawMessage) (json.RawMessage, error)
}
type MappingTuple struct {
	Head        model.OperatorMappingHead
	Version     model.OperatorMappingVersion
	Identity    model.ServiceObjectVersion
	Association model.ServiceObjectContractAssociation
}

func LoadMappingTuple(ctx context.Context, repo MappingRepository, id ReadIdentity, mappingID string) (MappingTuple, error) {
	var out MappingTuple
	head, found, err := repo.ReadMappingLineage(ctx, id, mappingID)
	out.Head = model.OperatorMappingHead{CaseID: head.CaseID, MappingID: head.MappingID, ServiceObjectID: head.ServiceObjectID, CurrentVersion: head.CurrentVersion, CurrentBindingVersion: head.CurrentBindingVersion, CreatedBy: head.CreatedBy, CreatedAt: head.CreatedAt, UpdatedAt: head.UpdatedAt}
	if err != nil {
		return out, err
	}
	if !found {
		return out, fault.NotFound("没有找到对象映射")
	}
	binding, err := ReadBindingVersion(ctx, repo, id, head.CurrentBindingVersion)
	if err != nil {
		return out, err
	}
	out.Version = model.OperatorMappingVersion{MappingID: head.MappingID, MappingVersion: head.CurrentVersion, ServiceObjectID: head.ServiceObjectID, ObjectVersion: binding.Value.ObjectVersion, AssociationVersion: binding.Value.AssociationVersion, Binding: binding.Value}
	out.Identity, err = ReadServiceObjectVersionExact(ctx, repo, id, head.ServiceObjectID, binding.Value.ObjectVersion)
	if err != nil {
		return out, err
	}
	out.Association, err = ReadServiceObjectAssociationExact(ctx, repo, id, head.ServiceObjectID, binding.Value.AssociationVersion)
	return out, err
}
func NormalizeMappingBasis(raw json.RawMessage) (json.RawMessage, error) {
	basis, err := model.CanonicalOperatorMappingRawJSON(raw)
	if err != nil || len(basis) == 0 || string(basis) == "null" {
		return nil, fault.Invalid("必须提供可验证的更正依据")
	}
	return basis, nil
}
func ValidateMappingBasis(ctx context.Context, facts BasisFacts, basis json.RawMessage, assoc model.ServiceObjectContractAssociation) error {
	verified, err := facts.VerifyBasis(ctx, basis)
	if err != nil {
		return fault.Invalid("更正依据必须引用授权案件中可见的冻结材料")
	}
	var refs []struct {
		MaterialID       string `json:"material_id"`
		MaterialVersion  int    `json:"material_version"`
		DocumentID       string `json:"document_id"`
		ParsedDocumentID string `json:"parsed_document_id"`
	}
	if json.Unmarshal(verified, &refs) != nil || len(refs) == 0 {
		return fault.Invalid("对象映射更正需要精确的材料依据")
	}
	for _, r := range refs {
		if r.MaterialID != assoc.MaterialID || r.MaterialVersion != assoc.MaterialVersion || r.DocumentID != assoc.DocumentID || r.ParsedDocumentID != assoc.ParsedDocumentID {
			return fault.Invalid("更正依据必须固定当前服务对象关联的材料版本")
		}
	}
	return nil
}
func mappingIdentityBasis(patch model.OperatorMappingChangePatch, basis json.RawMessage) error {
	if patch.Identity != nil {
		raw, err := model.CanonicalOperatorMappingRawJSON(patch.Identity.BasisRefs)
		if err != nil || !bytes.Equal(raw, basis) {
			return fault.Invalid("身份更正依据必须与已验证的材料依据一致")
		}
	}
	return nil
}
func MappingTarget[D executionmodel.OriginalDocument](ctx context.Context, repo MappingRepository, facts PreviewFacts[D], scope PreviewScope, actor string, clock MappingClock, tuple MappingTuple, input *model.ServiceObjectAssociationConfirmInput) (*model.ServiceObjectContractAssociation, *model.OperatorMappingAssociationAfter, error) {
	if input == nil {
		return nil, nil, nil
	}
	head, err := ReadServiceObjectHead(ctx, repo, ReadIdentity{TenantID: scope.TenantID, OrgID: scope.OrgID, CaseID: scope.CaseID}, tuple.Head.ServiceObjectID)
	if err != nil {
		return nil, nil, err
	}
	if head.CurrentVersion != tuple.Version.ObjectVersion || head.CurrentAssociationVersion != tuple.Version.AssociationVersion || input.ExpectedObjectVersion != head.CurrentVersion || input.ExpectedAssociationVersion != head.CurrentAssociationVersion || !input.Confirmed {
		return nil, nil, fault.Conflict("服务对象身份或关联版本已变化")
	}
	preview, err := Preview[D](ctx, facts, scope, head, input.Source)
	if err != nil {
		return nil, nil, err
	}
	var selected *model.ServiceObjectAssociationOption
	for i := range preview.Options {
		if preview.Options[i].ObjectRef == input.ObjectRef {
			selected = &preview.Options[i]
			break
		}
	}
	if selected == nil || selected.FactsSHA256 != input.FactsSHA256 || preview.CandidatesSHA256 != input.CandidatesSHA256 {
		return nil, nil, fault.Conflict("目标合同对象或冻结事实已变化，请重新预览")
	}
	assoc := model.ServiceObjectContractAssociation{MaterialID: preview.MaterialID, MaterialVersion: preview.MaterialVersion, DocumentID: preview.DocumentID, ParsedDocumentID: preview.ParsedDocumentID, SourceRunID: preview.RunID, SourceAttemptID: preview.AttemptID, ObjectRef: selected.ObjectRef, FactRefs: selected.Facts, BasisRefs: selected.BasisRefs}
	plan, err := model.PlanServiceObjectAssociation(head, tuple.Identity, model.ServiceObjectAssociationCandidate{ExpectedObjectVersion: input.ExpectedObjectVersion, ExpectedAssociationVersion: input.ExpectedAssociationVersion, Confirmed: true, Association: assoc}, actor, clock.Now().UTC())
	if err != nil || plan.ObjectVersion.Version != tuple.Identity.Version {
		return nil, nil, fault.Conflict("目标合同关联不能应用于当前服务对象")
	}
	assoc = plan.Association
	return &assoc, model.OperatorMappingAssociationAfterProjection(assoc), nil
}
func PreviewMapping[D executionmodel.OriginalDocument](ctx context.Context, repo MappingRepository, facts PreviewFacts[D], basisFacts BasisFacts, scope PreviewScope, actor, mappingID string, cas model.OperatorMappingChangeCAS, patch model.OperatorMappingChangePatch, basis json.RawMessage, clock MappingClock) (model.OperatorMappingChangePreview, error) {
	var out model.OperatorMappingChangePreview
	id := ReadIdentity{TenantID: scope.TenantID, OrgID: scope.OrgID, CaseID: scope.CaseID}
	tuple, err := LoadMappingTuple(ctx, repo, id, mappingID)
	if err != nil {
		return out, err
	}
	if !model.OperatorMappingCASMatches(tuple.Head, tuple.Version, cas) {
		return out, fault.Conflict("对象映射版本已变化，请刷新后重试")
	}
	if err = ValidateMappingBasis(ctx, basisFacts, basis, tuple.Association); err != nil {
		return out, err
	}
	patch, err = model.NormalizeOperatorMappingPatch(patch, tuple.Version.Binding)
	if err != nil {
		return out, err
	}
	if err = mappingIdentityBasis(patch, basis); err != nil {
		return out, err
	}
	plan, err := model.PlanOperatorMappingChange(tuple.Head, tuple.Version, cas, patch, "binding_preview", actor, clock.Now().UTC())
	if err != nil {
		return out, fault.Conflict("对象映射更正内容或版本不匹配")
	}
	_, target, err := MappingTarget[D](ctx, repo, facts, scope, actor, clock, tuple, patch.ContractAssociation)
	if err != nil {
		return out, err
	}
	if err = model.BindOperatorMappingAssociation(&plan, target); err != nil {
		return out, err
	}
	impact, err := model.OperatorMappingImpactWithAssociationSHA256(tuple.Head, tuple.Version, tuple.Identity, tuple.Association, target)
	if err != nil {
		return out, err
	}
	var identityAfter *model.ServiceObjectVersion
	if patch.Identity != nil {
		v, e := model.ProposedOperatorIdentityVersion(tuple.Head.ServiceObjectID, tuple.Identity.Version, *patch.Identity, patch.EffectiveFrom)
		if e != nil {
			return out, fault.Invalid("身份更正内容无效")
		}
		identityAfter = &v
	}
	return model.OperatorMappingChangePreview{MappingID: mappingID, ServiceObjectID: tuple.Head.ServiceObjectID, CAS: cas, Before: tuple.Version, After: plan.NextVersion, BeforeSHA256: plan.BeforeSHA256, AfterSHA256: plan.AfterSHA256, IdentityAfter: identityAfter, AssociationAfter: target, ImpactSHA256: impact, BasisRefs: append(json.RawMessage(nil), basis...)}, nil
}

type PreparedProposal struct {
	Identity                   CreateIdentity
	MappingID, Key, RequestSHA string
	Input                      model.OperatorMappingChangeCreateInput
}

func PrepareProposal(id CreateIdentity, mappingID, key string, input model.OperatorMappingChangeCreateInput) (PreparedProposal, error) {
	id.CaseID = strings.TrimSpace(id.CaseID)
	mappingID = strings.TrimSpace(mappingID)
	basis, err := NormalizeMappingBasis(input.BasisRefs)
	if err != nil {
		return PreparedProposal{}, err
	}
	input.BasisRefs = basis
	input.Patch, err = model.NormalizeOperatorMappingPatch(input.Patch, model.OperatorObjectBinding{})
	if err != nil {
		return PreparedProposal{}, fault.Invalid("对象映射更正内容无效")
	}
	if !model.ValidOperatorDigest(input.ImpactSHA256) {
		return PreparedProposal{}, fault.Invalid("影响预览摘要无效")
	}
	hash, err := model.OperatorMappingChangeCreateRequestSHA256(id.CaseID, mappingID, input)
	if err != nil {
		return PreparedProposal{}, err
	}
	return PreparedProposal{Identity: id, MappingID: mappingID, Key: key, RequestSHA: hash, Input: input}, nil
}
func ProposeMapping[D executionmodel.OriginalDocument](ctx context.Context, repo MappingRepository, facts PreviewFacts[D], basisFacts BasisFacts, scope PreviewScope, principal model.TrustedPrincipalProjection, clock MappingClock, p PreparedProposal) (model.OperatorMappingChangeRequest, bool, error) {
	var result model.OperatorMappingChangeRequest
	body, hash, found, err := repo.ProposalReceipt(ctx, p.Identity, p.Key)
	if err != nil {
		return result, false, err
	}
	if found {
		if hash != p.RequestSHA {
			return result, false, fault.Conflict("同一幂等键对应了不同的映射更正")
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return result, false, fault.New(500, "integrity_error", "对象映射更正幂等回执损坏")
		}
		return result, true, nil
	}
	id := ReadIdentity{TenantID: p.Identity.TenantID, OrgID: p.Identity.OrgID, CaseID: p.Identity.CaseID}
	tuple, err := LoadMappingTuple(ctx, repo, id, p.MappingID)
	if err != nil {
		return result, false, err
	}
	if !model.OperatorMappingCASMatches(tuple.Head, tuple.Version, p.Input.CAS) {
		return result, false, fault.Conflict("对象映射版本已变化，请刷新后重试")
	}
	_, target, err := MappingTarget[D](ctx, repo, facts, scope, principal.UserID, clock, tuple, p.Input.Patch.ContractAssociation)
	if err != nil {
		return result, false, err
	}
	impact, err := model.OperatorMappingImpactWithAssociationSHA256(tuple.Head, tuple.Version, tuple.Identity, tuple.Association, target)
	if err != nil {
		return result, false, err
	}
	if impact != p.Input.ImpactSHA256 {
		return result, false, fault.Conflict("对象映射影响预览已变化，请重新查看")
	}
	if err = ValidateMappingBasis(ctx, basisFacts, p.Input.BasisRefs, tuple.Association); err != nil {
		return result, false, err
	}
	if err = mappingIdentityBasis(p.Input.Patch, p.Input.BasisRefs); err != nil {
		return result, false, err
	}
	plan, err := model.PlanOperatorMappingChange(tuple.Head, tuple.Version, p.Input.CAS, p.Input.Patch, "binding_proposal", principal.UserID, clock.Now().UTC())
	if err != nil {
		return result, false, fault.Conflict("对象映射更正内容或版本不匹配")
	}
	if err = model.BindOperatorMappingAssociation(&plan, target); err != nil {
		return result, false, err
	}
	patchJSON, err := json.Marshal(p.Input.Patch)
	if err != nil {
		return result, false, err
	}
	before, err := json.Marshal(model.CanonicalOperatorMappingState(tuple.Version))
	if err != nil {
		return result, false, err
	}
	var after []byte
	if target != nil {
		after, err = json.Marshal(model.OperatorMappingApprovedAfter{Mapping: model.CanonicalOperatorMappingState(plan.NextVersion), Association: target})
	} else if p.Input.Patch.Identity == nil {
		after, err = json.Marshal(model.CanonicalOperatorMappingState(plan.NextVersion))
	} else {
		v, e := model.ProposedOperatorIdentityVersion(tuple.Head.ServiceObjectID, tuple.Identity.Version, *p.Input.Patch.Identity, p.Input.Patch.EffectiveFrom)
		if e != nil {
			return result, false, fault.Invalid("身份更正内容无效")
		}
		after, err = json.Marshal(model.OperatorMappingApprovedAfter{Mapping: model.CanonicalOperatorMappingState(plan.NextVersion), Identity: model.OperatorIdentityAfterProjection(v)})
	}
	if err != nil {
		return result, false, err
	}
	sum := sha256.Sum256(patchJSON)
	requestID, err := clock.NewRequestID()
	if err != nil {
		return result, false, err
	}
	now := clock.Now().UTC().Truncate(time.Microsecond)
	result = model.OperatorMappingChangeRequest{RequestID: requestID, CaseID: p.Identity.CaseID, MappingID: p.MappingID, ServiceObjectID: tuple.Head.ServiceObjectID, Kind: p.Input.Patch.Kind, ExpectedMappingVersion: p.Input.CAS.ExpectedMappingVersion, ExpectedBindingVersion: p.Input.CAS.ExpectedBindingVersion, ExpectedObjectVersion: p.Input.CAS.ExpectedObjectVersion, ExpectedAssociationVersion: p.Input.CAS.ExpectedAssociationVersion, Before: before, BeforeSHA256: plan.BeforeSHA256, After: after, AfterSHA256: plan.AfterSHA256, Patch: p.Input.Patch, PatchSHA256: hex.EncodeToString(sum[:]), BasisRefs: append(json.RawMessage(nil), p.Input.BasisRefs...), ImpactSHA256: p.Input.ImpactSHA256, State: "pending_decision", ProposedBy: principal, CreatedAt: now}
	response, err := json.Marshal(result)
	if err != nil {
		return result, false, err
	}
	err = repo.AppendProposal(ctx, p.Identity, p.Key, model.OperatorMappingChangeRecord{Value: result, RequestSHA256: p.RequestSHA}, patchJSON, after, plan.PatchSHA256, response)
	return result, false, err
}
func ReadMappingRequest(ctx context.Context, repo MappingRepository, id ReadIdentity, requestID string) (model.OperatorMappingChangeRecord, error) {
	row, found, err := repo.RequestRaw(ctx, id, requestID)
	if err != nil {
		return row.Record, err
	}
	if !found {
		return row.Record, fault.NotFound("没有找到对象映射更正")
	}
	v := &row.Record.Value
	v.Before = append(json.RawMessage(nil), row.Before...)
	v.After = append(json.RawMessage(nil), row.After...)
	v.BasisRefs = append(json.RawMessage(nil), row.Basis...)
	if json.Unmarshal(row.Patch, &v.Patch) != nil || json.Unmarshal(v.Before, new(map[string]any)) != nil || json.Unmarshal(v.After, new(map[string]any)) != nil {
		return model.OperatorMappingChangeRecord{}, fault.New(500, "integrity_error", "对象映射更正记录损坏")
	}
	state, found, err := repo.DecisionState(ctx, id, requestID)
	if err != nil {
		return model.OperatorMappingChangeRecord{}, err
	}
	if found {
		v.State = state
	} else {
		v.State = "pending_decision"
	}
	return row.Record, nil
}
func GetMappingChange(ctx context.Context, repo MappingRepository, id ReadIdentity, requestID string) (model.OperatorMappingChangeRequest, *model.OperatorMappingChangeDecision, error) {
	record, err := ReadMappingRequest(ctx, repo, id, requestID)
	if err != nil {
		return record.Value, nil, err
	}
	result := record.Value
	decision, found, err := repo.DecisionRaw(ctx, id, requestID)
	if err != nil {
		return result, nil, err
	}
	if !found {
		return result, nil, nil
	}
	result.State = decision.Decision
	return result, &decision, nil
}
