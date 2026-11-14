package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/auditext"
	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

func ValidateSingleBinding(binding model.OperatorObjectBinding, at time.Time) error {
	if binding.Status != "confirmed" {
		return fault.Conflict("对象映射尚未确认")
	}
	if at.Before(binding.EffectiveFrom) || !at.Before(binding.EffectiveUntil) {
		return fault.Conflict("业务时间不在已确认对象映射的有效期间内")
	}
	return nil
}
func ValidateSingleSnapshot(snapshot model.OperatorSnapshotSet, binding model.OperatorObjectBinding, at time.Time) error {
	if snapshot.BindingVersion != binding.Version {
		return fault.Conflict("快照集不属于该对象映射")
	}
	if !snapshot.BusinessAt.Equal(at) {
		return fault.Conflict("快照集业务时间与核验请求不一致")
	}
	if len(snapshot.Observations) == 0 {
		return fault.Conflict("快照集没有可用观测")
	}
	return nil
}
func FrozenHasContract[D executionmodel.OriginalDocument](docs []executionmodel.FrozenDocument[D], id string) bool {
	for _, v := range docs {
		if v.Document.OriginalProof().ID == id && IsContractMaterial(v) {
			return true
		}
	}
	return false
}
func AssociationMatchesFrozen[D executionmodel.OriginalDocument](association model.ServiceObjectContractAssociation, binding model.OperatorObjectBinding, input model.OperatorEvaluationInput, docs []executionmodel.FrozenDocument[D]) bool {
	if association.ServiceObjectID != binding.ServiceObjectID || association.ObjectVersion != binding.ObjectVersion || association.AssociationVersion != binding.AssociationVersion || association.DocumentID != input.ContractVersionID || association.SourceRunID == "" || association.SourceAttemptID == "" || association.MaterialID == "" || association.MaterialVersion < 1 || association.ParsedDocumentID == "" {
		return false
	}
	for _, v := range docs {
		if v.Document.OriginalProof().ID != association.DocumentID || v.ParsedDocumentID != association.ParsedDocumentID || !IsContractMaterial(v) || v.Material.MaterialID != association.MaterialID || v.Material.Version != association.MaterialVersion || !v.Ready {
			continue
		}
		return true
	}
	return false
}
func SelectSingleSource[D executionmodel.OriginalDocument](ctx context.Context, f SupplementSources[D], docs []executionmodel.FrozenDocument[D], association model.ServiceObjectContractAssociation, cutoff time.Time) (string, string, error) {
	selected := make([]executionmodel.FrozenDocument[D], 0)
	for _, doc := range docs {
		if doc.Document.OriginalProof().ID == association.DocumentID && doc.ParsedDocumentID != "" && doc.ParsedDocumentID == association.ParsedDocumentID {
			selected = append(selected, doc)
		}
	}
	if len(selected) != 1 {
		return "", "", fault.Conflict("服务对象合同关联未固定到唯一的审查解析")
	}
	purpose := ""
	for _, doc := range docs {
		if doc.Document.OriginalProof().ID == association.DocumentID && doc.ParsedDocumentID == association.ParsedDocumentID && doc.Material != nil {
			a, e1 := time.Parse(time.RFC3339Nano, doc.Document.OriginalProof().CreatedAt)
			b, e2 := time.Parse(time.RFC3339Nano, doc.ParsedCreatedAt)
			if e1 != nil || e2 != nil || a.After(cutoff) || b.After(cutoff) {
				return "", "", fault.Conflict("合同或解析取得时间晚于证据截止时间，或无法核实")
			}
			purpose = doc.Material.Purpose
			break
		}
	}
	if _, err := f.Sources(ctx, selected[0]); err != nil {
		return "", "", fault.Conflict("冻结合同输入未通过完整性校验")
	}
	return purpose, selected[0].Document.OriginalProof().Role, nil
}

type SinglePrepared struct {
	Frozen                               auditext.FrozenInput
	Facts                                []auditext.Fact
	Checks                               []auditext.CheckResult
	MachineStatus, RequestSHA, ResultSHA string
	InputJSON, ResultJSON                []byte
}

func PrepareSingleEvaluation(ctx context.Context, evaluator auditext.Evaluator, id CreateIdentity, grant string, config AggregateConfiguration, input model.OperatorEvaluationInput, scope model.OperatorEvaluationScope, pack auditext.PackRef, rulePackVersion string, binding model.OperatorObjectBinding, association model.ServiceObjectContractAssociation, snapshot model.OperatorSnapshotSet) (SinglePrepared, error) {
	var out SinglePrepared
	facts, err := FactsForAssociation(association)
	if err != nil {
		return out, err
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
	frozen := auditext.FrozenInput{Scope: auditext.AuthorizedScope{TenantID: id.TenantID, CaseID: id.CaseID, ActorID: id.ActorID, GrantVersion: grant}, Pack: pack, ContractVersion: input.ContractVersionID, RulePackVersion: rulePackVersion, BindingStatus: auditext.BindingConfirmed, BindingVersion: binding.Version, SnapshotSetID: snapshot.SnapshotSetID, ExternalObjectID: binding.ExternalObject.ExternalObjectID, SourceSystem: snapshot.SourceSystem, ModelConfigurationID: config.Model, PlannerConfigurationID: config.Planner, ParserConfigurationID: config.Parser, BusinessAt: input.BusinessAt, EvidenceCutoffAt: input.EvidenceCutoffAt, Facts: facts, EvaluationScope: &auditext.EvaluationScope{Kind: scope.Kind, ServiceObjectIDs: append([]string(nil), scope.ServiceObjectIDs...)}}
	if err = frozen.Validate(); err != nil {
		return out, fault.Conflict("冻结核验输入不完整")
	}
	checks, err := evaluator.Evaluate(ctx, frozen, ObservationsForEvaluation(snapshot, input.BusinessAt, *input.EvidenceCutoffAt))
	if err != nil {
		if errors.Is(err, auditext.ErrUnauthorized) {
			return out, fault.Forbidden("当前授权不能核验该案件")
		}
		return out, fault.Conflict("确定性核验未完成")
	}
	if len(checks) == 0 {
		return out, fault.New(502, "fulfillment_empty", "核验器没有返回检查结果")
	}
	status := "completed"
	for _, check := range checks {
		if check.Validate() != nil {
			return out, fault.New(502, "fulfillment_invalid", "核验器返回了不合法的结果")
		}
		if check.MachineStatus != auditext.MachineCompleted {
			status = "partial"
		}
	}
	requestSHA, err := collectionHash(input)
	if err != nil {
		return out, err
	}
	inputJSON, err := json.Marshal(frozen)
	if err != nil {
		return out, err
	}
	resultJSON, err := json.Marshal(checks)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(resultJSON)
	return SinglePrepared{Frozen: frozen, Facts: facts, Checks: checks, MachineStatus: status, RequestSHA: requestSHA, ResultSHA: hex.EncodeToString(sum[:]), InputJSON: inputJSON, ResultJSON: resultJSON}, nil
}

type SingleWriteInput struct {
	Identity                                          CreateIdentity
	Input                                             model.OperatorEvaluationInput
	Scope                                             model.OperatorEvaluationScope
	Pack                                              auditext.PackRef
	RulePackVersion, EvaluationID, Key, Purpose, Role string
	Binding                                           model.OperatorObjectBinding
	Association                                       model.ServiceObjectContractAssociation
	Snapshot                                          model.OperatorSnapshotSet
	Prepared                                          SinglePrepared
}

func WriteSingleAfterReplay(ctx context.Context, repo EvaluationWriter, w SingleWriteInput, clock CollectionClock) (model.OperatorEvaluation, error) {
	var out model.OperatorEvaluation
	p := w.Prepared
	now := clock.Now().UTC().Truncate(time.Microsecond)
	v := model.OperatorEvaluation{CaseID: w.Identity.CaseID, EvaluationID: w.EvaluationID, PackID: w.Pack.ID, PackVersion: w.Pack.Version, SourceReviewID: w.Input.SourceReviewID, SourceAttemptID: w.Input.SourceAttemptID, ContractVersionID: w.Input.ContractVersionID, RulePackVersion: w.RulePackVersion, ObjectBindingVersion: w.Binding.Version, SnapshotSetID: w.Snapshot.SnapshotSetID, BusinessAt: w.Input.BusinessAt, MachineStatus: p.MachineStatus, ResultSHA256: p.ResultSHA, CreatedAt: now}
	if err := repo.AppendEvaluation(ctx, EvaluationWrite{Identity: w.Identity, Value: v, Key: w.Key, RequestSHA: p.RequestSHA, InputJSON: p.InputJSON, ResultJSON: p.ResultJSON}); err != nil {
		return out, err
	}
	if err := WriteEvaluationChecks(ctx, repo, w.Identity, w.EvaluationID, p.Checks, now); err != nil {
		return out, err
	}
	out = model.OperatorEvaluation{CaseID: w.Identity.CaseID, EvaluationID: w.EvaluationID, PackID: w.Pack.ID, PackVersion: w.Pack.Version, SourceReviewID: w.Input.SourceReviewID, SourceAttemptID: w.Input.SourceAttemptID, ContractVersionID: w.Input.ContractVersionID, RulePackVersion: w.RulePackVersion, ObjectBindingVersion: w.Binding.Version, SnapshotSetID: w.Snapshot.SnapshotSetID, BusinessAt: w.Input.BusinessAt, EvidenceCutoffAt: w.Input.EvidenceCutoffAt, MachineStatus: p.MachineStatus, Checks: p.Checks, CreatedAt: now, Input: p.Frozen, ResultSHA256: p.ResultSHA, Scope: &w.Scope}
	object, err := EvaluationObjectForAssociation(w.Scope, w.Input, w.Binding, w.Association, w.Snapshot, w.Purpose, w.Role, p.Facts, p.Checks, p.MachineStatus)
	if err != nil {
		return out, err
	}
	out.Objects = []model.OperatorEvaluationObject{object}
	coverage := model.SummarizeEvaluationObjects(w.Scope.Kind, out.Objects)
	out.Coverage = &coverage
	if err = WriteEvaluationObject(ctx, repo, w.Identity, w.EvaluationID, 1, object, w.Association.FactsCanonicalJSON, w.Association.FactsSHA256); err != nil {
		return out, err
	}
	return out, nil
}
