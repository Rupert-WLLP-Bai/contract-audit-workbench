package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type BindingClock interface {
	Now() time.Time
	NewBindingID() (string, error)
	NewMappingID() (string, error)
}
type BindingRepository interface {
	ReadRepository
	BindingByKey(context.Context, CreateIdentity, string) (MappingBindingRow, bool, error)
	AppendInitialBinding(context.Context, CreateIdentity, string, model.OperatorObjectBindingRecord, []byte, []byte, []byte) error
	AppendInitialLineage(context.Context, CreateIdentity, model.OperatorObjectBinding) error
}
type PreparedBinding struct {
	Identity                              CreateIdentity
	Input                                 model.OperatorObjectBindingInput
	Key, BindingID, LegacySHA, RequestSHA string
	From, Until                           time.Time
}

func PrepareBinding(id CreateIdentity, input model.OperatorObjectBindingInput, key string, clock BindingClock) (PreparedBinding, error) {
	if !input.Confirmed {
		return PreparedBinding{}, fault.Invalid("必须显式确认合同对象与外部对象的映射")
	}
	if strings.TrimSpace(input.ContractObject.ObjectType) == "" || auditext.ValidateID(input.ContractObject.ObjectID) != nil || auditext.ValidateID(input.ExternalObject.ConnectorID) != nil || auditext.ValidateID(input.ExternalObject.ExternalObjectID) != nil {
		return PreparedBinding{}, fault.Invalid("合同对象或外部对象标识不合法")
	}
	if input.ContractObject.Label != nil && len(*input.ContractObject.Label) > 256 {
		return PreparedBinding{}, fault.Invalid("合同对象名称过长")
	}
	from, until := input.EffectiveFrom, input.EffectiveUntil
	if from.IsZero() {
		from = clock.Now().UTC()
	}
	if until.IsZero() {
		until = from.AddDate(1, 0, 0)
	}
	if !from.Before(until) {
		return PreparedBinding{}, fault.Invalid("映射有效区间不合法")
	}
	bindingID, err := clock.NewBindingID()
	if err != nil {
		return PreparedBinding{}, err
	}
	legacy, current, err := model.OperatorObjectBindingRequestSHA256(id.CaseID, input, from, until)
	if err != nil {
		return PreparedBinding{}, err
	}
	return PreparedBinding{Identity: id, Input: input, Key: key, BindingID: bindingID, LegacySHA: legacy, RequestSHA: current, From: from, Until: until}, nil
}
func ApplyBinding(ctx context.Context, repo BindingRepository, clock BindingClock, p PreparedBinding) (model.OperatorObjectBinding, error) {
	var out model.OperatorObjectBinding
	row, found, err := repo.BindingByKey(ctx, p.Identity, p.Key)
	if err != nil {
		return out, err
	}
	if found {
		record, err := DecodeBindingRecord(row, "对象映射幂等回执损坏")
		if err != nil {
			return model.OperatorObjectBinding{}, err
		}
		if record.RequestSHA256 != p.RequestSHA && !(p.Input.ServiceObjectID == "" && p.Input.ExpectedObjectVersion == 0 && p.Input.ExpectedAssociationVersion == 0 && record.RequestSHA256 == p.LegacySHA) {
			return model.OperatorObjectBinding{}, fault.Conflict("同一幂等键对应了不同的对象映射")
		}
		return record.Value, nil
	}
	in := p.Input
	if strings.TrimSpace(in.ServiceObjectID) == "" || in.ExpectedObjectVersion < 1 || in.ExpectedAssociationVersion < 1 {
		return out, fault.Conflict("新建对象映射必须绑定已确认的服务对象和合同关联版本")
	}
	rid := ReadIdentity{TenantID: p.Identity.TenantID, OrgID: p.Identity.OrgID, CaseID: p.Identity.CaseID}
	head, err := ReadServiceObjectHead(ctx, repo, rid, in.ServiceObjectID)
	if err != nil {
		return out, err
	}
	if head.CurrentVersion != in.ExpectedObjectVersion || head.CurrentAssociationVersion != in.ExpectedAssociationVersion {
		return out, fault.Conflict("服务对象身份或合同关联已变化，请刷新后重试")
	}
	identity, err := ReadServiceObjectVersionExact(ctx, repo, rid, head.ID, head.CurrentVersion)
	if err != nil {
		return out, err
	}
	assoc, err := ReadServiceObjectAssociationExact(ctx, repo, rid, head.ID, head.CurrentAssociationVersion)
	if err != nil {
		return out, err
	}
	if identity.State != model.ServiceObjectConfirmed || assoc.ObjectVersion != in.ExpectedObjectVersion || assoc.AssociationVersion != in.ExpectedAssociationVersion || assoc.ObjectRef != in.ContractObject.ObjectID {
		return out, fault.Conflict("合同对象与已确认的服务对象关联不一致")
	}
	mappingID, err := clock.NewMappingID()
	if err != nil {
		return out, err
	}
	contract, err := json.Marshal(in.ContractObject)
	if err != nil {
		return out, err
	}
	external, err := json.Marshal(in.ExternalObject)
	if err != nil {
		return out, err
	}
	now := clock.Now().UTC().Truncate(time.Microsecond)
	out = model.OperatorObjectBinding{CaseID: p.Identity.CaseID, BindingID: p.BindingID, Version: p.BindingID, Status: "confirmed", ContractObject: in.ContractObject, ExternalObject: in.ExternalObject, ConfirmedBy: p.Identity.ActorID, ConfirmedAt: now, EffectiveFrom: p.From, EffectiveUntil: p.Until, CreatedAt: now, MappingID: mappingID, MappingVersion: 1, ServiceObjectID: in.ServiceObjectID, ObjectVersion: in.ExpectedObjectVersion, AssociationVersion: in.ExpectedAssociationVersion}
	body, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	record := model.OperatorObjectBindingRecord{Value: out, RequestSHA256: p.RequestSHA}
	if err = repo.AppendInitialBinding(ctx, p.Identity, p.Key, record, contract, external, body); err != nil {
		return out, err
	}
	if err = repo.AppendInitialLineage(ctx, p.Identity, out); err != nil {
		return out, err
	}
	return out, nil
}

// DecodeBindingRecord retains scan fields absent from the stored response body.
func DecodeBindingRecord(row MappingBindingRow, receiptError string) (model.OperatorObjectBindingRecord, error) {
	record := model.OperatorObjectBindingRecord{Value: row.Payload, RequestSHA256: row.RequestSHA256}
	if len(row.ResponseBody) > 0 {
		if err := json.Unmarshal(row.ResponseBody, &record.Value); err != nil {
			return model.OperatorObjectBindingRecord{}, fault.New(500, "integrity_error", receiptError)
		}
	}
	return record, nil
}
func ReadBindingVersion(ctx context.Context, repo ReadRepository, id ReadIdentity, version string) (model.OperatorObjectBindingRecord, error) {
	row, found, err := repo.ReadMappingBinding(ctx, id, version)
	if err != nil {
		return model.OperatorObjectBindingRecord{}, err
	}
	if !found {
		return model.OperatorObjectBindingRecord{}, fault.NotFound("没有找到对象映射版本")
	}
	return DecodeBindingRecord(row, "对象映射响应回执损坏")
}
