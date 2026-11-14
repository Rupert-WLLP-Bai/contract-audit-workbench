// File metadata (manifest mirror):
// Purpose: Coordinates approval decision validation, credential checks, authorized replay, ordered locking, state changes and atomic contract event/outbox/write receipt.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"contractaudit/internal/accounts"
	"contractaudit/internal/approval"
	"contractaudit/internal/approval/model"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

const DecisionRoute = "clm.approval_decide"

type PreparedDecision struct {
	Input          model.DecideApprovalTaskInput
	TaskID, Digest string
}

func PrepareDecision(input model.DecideApprovalTaskInput) (PreparedDecision, error) {
	id, err := contract.NormalizeContractUUID(input.TaskID)
	if err != nil {
		return PreparedDecision{}, err
	}
	input.Decision = strings.TrimSpace(input.Decision)
	input.Comment = strings.TrimSpace(input.Comment)
	if input.Decision != model.DecisionApprove && input.Decision != model.DecisionReturn {
		return PreparedDecision{}, fault.Invalid("decision 必须是 approve 或 return")
	}
	if input.Decision == model.DecisionReturn && input.Comment == "" {
		return PreparedDecision{}, fault.Invalid("退回必须填写理由")
	}
	if utf8.RuneCountInString(input.Comment) > 2000 {
		return PreparedDecision{}, fault.Invalid("审批意见不能超过 2000 个字符")
	}
	if input.ExpectedRoundRowVersion < 1 {
		return PreparedDecision{}, fault.Invalid("审批决定必须携带当前轮次 row_version")
	}
	if err = contract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedDecision{}, err
	}
	digest, err := contract.RequestDigest(DecisionRoute, "POST", "/api/approval-tasks/"+id+"/decisions", input.ExpectedRoundRowVersion, struct {
		Decision string `json:"decision"`
		Comment  string `json:"comment"`
	}{input.Decision, input.Comment})
	return PreparedDecision{input, id, digest}, err
}

type DecisionRepository interface {
	TransferRepository
	ApprovalTaskAssignedTo(context.Context, string, model.ApprovalTaskRecord, string) (bool, error)
	LatestApprovalTransfer(context.Context, string, model.ApprovalTaskRecord) (string, bool, error)
	LockNextPendingApprovalTask(context.Context, string, model.ApprovalTaskRecord) (*model.ApprovalTaskRecord, error)
	InsertDecision(context.Context, string, string, string, string, string, string, string, model.ApprovalTaskRecord, model.ApprovalRoundRecord, *approval.Claims) error
	CloseApprovalTask(context.Context, string, model.ApprovalTaskRecord, string, string) error
	CancelPendingApprovalTasks(context.Context, string, model.ApprovalTaskRecord, string) error
	ActivateApprovalTask(context.Context, string, *model.ApprovalTaskRecord) error
	LoadApprovalTaskSummary(context.Context, string, model.ApprovalTaskRecord) (model.ApprovalTaskSummary, error)
	UpdateRound(context.Context, string, model.ApprovalTaskRecord, int64, string) (int64, *time.Time, error)
}
type DecisionAccess interface {
	CanRead(context.Context, contract.Aggregate) bool
	CanDecide(contract.Aggregate) bool
}

func roundSummary(r model.ApprovalRoundRecord) model.ApprovalRoundSummary {
	return model.ApprovalRoundSummary{ApprovalRoundID: r.ApprovalRoundID, ContractRevisionID: r.ContractRevisionID, RoundNumber: r.RoundNumber, State: r.State, WorkflowTemplateID: r.WorkflowTemplateID, WorkflowTemplateVersion: r.WorkflowTemplateVersion, RoutingDigest: r.RoutingDigest, RowVersion: r.RowVersion, StartedAt: r.StartedAt, EndedAt: r.EndedAt}
}
func Decide(ctx context.Context, p PreparedDecision, current accounts.User, keyring *approval.Keyring, contracts TransitionContractRepository, access DecisionAccess, repo DecisionRepository, events SubmissionEvents) (model.ApprovalDecisionResult, error) {
	var empty model.ApprovalDecisionResult
	tenant := accounts.Tenant(current)
	if keyring != nil {
		claims, err := keyring.Verify(p.Input.Credential)
		if err != nil {
			return empty, fault.Forbidden("审批身份凭证无效或已过期")
		}
		if claims.TenantID != tenant || claims.SubjectID != current.ID || claims.TaskID != p.TaskID {
			return empty, fault.Forbidden("审批身份凭证与当前任务、版本或范围不匹配")
		}
	}
	if err := contracts.LockWriteReceipt(ctx, tenant, current.ID, DecisionRoute, p.Input.IdempotencyKey); err != nil {
		return empty, err
	}
	receipt, found, err := loadAuthorizedReceipt(ctx, contracts, access, tenant, current.ID, DecisionRoute, p.Input.IdempotencyKey, p.Digest)
	if err != nil {
		return empty, err
	}
	if found {
		var result model.ApprovalDecisionResult
		if err = json.Unmarshal(receipt.Response, &result); err != nil {
			return empty, err
		}
		return result, nil
	}
	task, err := repo.LoadApprovalTask(ctx, tenant, p.TaskID)
	if err != nil {
		return empty, err
	}
	if err = contracts.LockContract(ctx, task.ContractID); err != nil {
		return empty, err
	}
	record, err := contracts.LoadContract(ctx, tenant, task.ContractID)
	if err != nil {
		return empty, err
	}
	if !access.CanRead(ctx, record) {
		return empty, fault.NotFound("没有找到对应记录")
	}
	if record.AuthorityMode != "native" {
		return empty, fault.Forbidden("外部权威合同不能在本地提交审批决定")
	}
	if !access.CanDecide(record) {
		return empty, fault.Forbidden("当前授权不能审批此合同")
	}
	if !accounts.CanDecide(current.Role) {
		return empty, fault.Forbidden("当前角色不能审批合同")
	}
	if record.ValidityState != "active" || record.LifecycleState != "draft" {
		return empty, fault.Conflict("合同当前状态不允许继续会签")
	}
	round, err := repo.LoadApprovalRoundForUpdate(ctx, tenant, task.ContractID, task.ApprovalRoundID)
	if err != nil {
		return empty, err
	}
	if round.State != "in_review" {
		return empty, fault.Conflict("会签轮次已结束")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != round.ContractRevisionID {
		return empty, fault.Conflict("审批轮次引用的不是当前冻结版本")
	}
	if round.RowVersion != p.Input.ExpectedRoundRowVersion {
		return empty, fault.Conflict("审批轮次已变化，请刷新待办后重试")
	}
	task, err = repo.LockApprovalTask(ctx, tenant, task.ContractID, task.ApprovalRoundID, p.TaskID)
	if err != nil {
		return empty, err
	}
	if task.State != "active" {
		return empty, fault.Conflict("审批任务已处理或不是当前待办节点")
	}
	assigned, err := repo.ApprovalTaskAssignedTo(ctx, tenant, task, current.ID)
	if err != nil {
		return empty, err
	}
	if !assigned {
		return empty, fault.Forbidden("当前账号不是该会签节点的审批人")
	}
	if !task.Final && round.InitiatedBy == current.ID {
		return empty, fault.Forbidden("发起人不能审批自己送审的独立会签节点")
	}
	bound, err := repo.ApprovalRoleBindingCurrent(ctx, tenant, task.RoleCode, record.OwnerOrgID, current.ID)
	if err != nil {
		return empty, err
	}
	if !bound {
		return empty, fault.Forbidden("会签角色绑定已撤销，不能继续审批")
	}
	target, transferred, err := repo.LatestApprovalTransfer(ctx, tenant, task)
	if err != nil {
		return empty, err
	}
	if transferred && target != current.ID {
		return empty, fault.Forbidden("审批任务已转交，原受理人不能再作决定")
	}
	scope, err := contracts.LoadApprovalRevisionScope(ctx, tenant, task.ContractID, round.ContractRevisionID)
	if err != nil {
		return empty, err
	}
	claims, err := VerifyCredential(keyring, current, task, record, round, scope, p.Input.Credential)
	if err != nil {
		return empty, err
	}
	next, err := repo.LockNextPendingApprovalTask(ctx, tenant, task)
	if err != nil {
		return empty, err
	}
	workflow, err := model.DecisionWorkflowState(task, next != nil, p.Input.Decision)
	if err != nil {
		return empty, err
	}
	decisionID, err := contract.NewContractUUID()
	if err != nil {
		return empty, err
	}
	credentialHash := sha256.Sum256([]byte(p.Input.Credential))
	if err = repo.InsertDecision(ctx, tenant, decisionID, current.ID, p.Input.Decision, p.Input.Comment, workflow, hex.EncodeToString(credentialHash[:]), task, round, claims); err != nil {
		return empty, err
	}
	state := "approved"
	if p.Input.Decision == model.DecisionReturn {
		state = "returned"
	}
	if err = repo.CloseApprovalTask(ctx, tenant, task, state, current.ID); err != nil {
		return empty, err
	}
	task.State = state
	if state == "returned" {
		if err = repo.CancelPendingApprovalTasks(ctx, tenant, task, current.ID); err != nil {
			return empty, err
		}
	}
	var taskSummary model.ApprovalTaskSummary
	if state == "approved" {
		taskSummary, err = repo.LoadApprovalTaskSummary(ctx, tenant, task)
		if err != nil {
			return empty, err
		}
	}
	roundState := state
	if state == "approved" && next != nil {
		if err = repo.ActivateApprovalTask(ctx, tenant, next); err != nil {
			return empty, err
		}
		roundState = "in_review"
	}
	roundVersion, ended, err := repo.UpdateRound(ctx, tenant, task, round.RowVersion, roundState)
	if err != nil {
		return empty, err
	}
	version, err := contracts.CASApprovalTransition(ctx, tenant, record, roundState == "approved")
	if err != nil {
		return empty, err
	}
	var nextSummary *model.ApprovalTaskSummary
	if next != nil && state == "approved" {
		v, err := repo.LoadApprovalTaskSummary(ctx, tenant, *next)
		if err != nil {
			return empty, err
		}
		nextSummary = &v
	}
	eventType := "contract.approval_completed.v1"
	if state == "returned" {
		eventType = "contract.approval_returned.v1"
	} else if next != nil {
		eventType = "contract.approval_advanced.v1"
	}
	commentHash := sha256.Sum256([]byte(p.Input.Comment))
	payload := map[string]any{"contract_id": task.ContractID, "approval_round_id": task.ApprovalRoundID, "approval_task_id": task.ApprovalTaskID, "decision_id": decisionID, "contract_revision_id": round.ContractRevisionID, "node_code": task.NodeCode, "node_order": task.NodeOrder, "comment_digest": hex.EncodeToString(commentHash[:]), "workflow_state": workflow}
	if next != nil && state == "approved" {
		payload["next_approval_task_id"] = next.ApprovalTaskID
		payload["next_node_order"] = next.NodeOrder
	}
	eventID, err := contract.NewContractUUID()
	if err != nil {
		return empty, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return empty, err
	}
	if err = events.AppendContractEvent(ctx, tenant, eventID, task.ContractID, version, eventType, body); err != nil {
		return empty, err
	}
	if err = events.EnqueueContractEvent(ctx, tenant, eventID); err != nil {
		return empty, err
	}
	if state == "returned" {
		taskSummary, err = repo.LoadApprovalTaskSummary(ctx, tenant, task)
		if err != nil {
			return empty, err
		}
	}
	round.RowVersion = roundVersion
	if roundState != "in_review" {
		round.State = roundState
		round.EndedAt = ended
	}
	contractState := record.LifecycleState
	if roundState == "approved" {
		contractState = "ready_to_sign"
	}
	result := model.ApprovalDecisionResult{DecisionID: decisionID, Decision: p.Input.Decision, Round: roundSummary(round), Task: taskSummary, NextTask: nextSummary, ContractState: contractState, WorkflowState: workflow, RowVersion: version}
	raw, err := json.Marshal(result)
	if err != nil {
		return empty, err
	}
	if err = contracts.InsertWriteReceipt(ctx, tenant, current.ID, DecisionRoute, p.Input.IdempotencyKey, p.Digest, task.ContractID, 201, raw); err != nil {
		return empty, err
	}
	return result, nil
}
