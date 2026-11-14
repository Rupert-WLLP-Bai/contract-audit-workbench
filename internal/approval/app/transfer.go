// File metadata (manifest mirror):
// Purpose: Coordinates authorized receipt replay, live transfer target checks and append-only reassignment facts with contract event/outbox/write receipt.
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
	"contractaudit/internal/approval/model"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

const TransferRoute = "clm.approval_transfer"

type PreparedTransfer struct {
	Input                            model.TransferApprovalTaskInput
	TaskID, ToUserID, Reason, Digest string
}

func PrepareTransfer(input model.TransferApprovalTaskInput) (PreparedTransfer, error) {
	id, err := contract.NormalizeContractUUID(input.TaskID)
	if err != nil {
		return PreparedTransfer{}, err
	}
	target, reason := strings.TrimSpace(input.ToUserID), strings.TrimSpace(input.Reason)
	if target == "" || reason == "" || utf8.RuneCountInString(reason) > 1000 {
		return PreparedTransfer{}, fault.Invalid("转交必须填写目标账号与理由")
	}
	if input.ExpectedTaskRowVersion < 1 {
		return PreparedTransfer{}, fault.Invalid("转交必须携带当前任务 row_version")
	}
	if err = contract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedTransfer{}, err
	}
	digest, err := contract.RequestDigest(TransferRoute, "POST", "/api/approval-tasks/"+id+"/transfers", input.ExpectedTaskRowVersion, struct {
		ToUserID string `json:"to_user_id"`
		Reason   string `json:"reason"`
	}{target, reason})
	return PreparedTransfer{input, id, target, reason, digest}, err
}

type TransitionContractRepository interface {
	SubmissionContractRepository
	CASApprovalTransition(context.Context, string, contract.Aggregate, bool) (int64, error)
	LoadApprovalRevisionScope(context.Context, string, string, string) (contractapp.ApprovalRevisionScope, error)
}
type TransferRepository interface {
	LoadApprovalTask(context.Context, string, string) (model.ApprovalTaskRecord, error)
	LoadApprovalRoundForUpdate(context.Context, string, string, string) (model.ApprovalRoundRecord, error)
	LockApprovalTask(context.Context, string, string, string, string) (model.ApprovalTaskRecord, error)
	ApprovalRoleBindingCurrent(context.Context, string, string, string, string) (bool, error)
	FirstApprovalAssignee(context.Context, string, model.ApprovalTaskRecord) (string, error)
	InsertTransfer(context.Context, string, string, string, string, string, model.ApprovalTaskRecord, accounts.User) (time.Time, error)
	InsertTransferAssignment(context.Context, string, model.ApprovalTaskRecord, accounts.User) error
}

func Transfer(ctx context.Context, p PreparedTransfer, current accounts.User, contracts TransitionContractRepository, access SubmissionAccess, directory Directory, repo TransferRepository, events SubmissionEvents) (model.TransferApprovalTaskResult, error) {
	var empty model.TransferApprovalTaskResult
	tenant := accounts.Tenant(current)
	if err := contracts.LockWriteReceipt(ctx, tenant, current.ID, TransferRoute, p.Input.IdempotencyKey); err != nil {
		return empty, err
	}
	receipt, found, err := loadAuthorizedReceipt(ctx, contracts, access, tenant, current.ID, TransferRoute, p.Input.IdempotencyKey, p.Digest)
	if err != nil {
		return empty, err
	}
	if found {
		var result model.TransferApprovalTaskResult
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
	if !access.CanManage(record) || current.Role != "admin" {
		return empty, fault.Forbidden("只有流程管理员可以转交审批任务")
	}
	round, err := repo.LoadApprovalRoundForUpdate(ctx, tenant, task.ContractID, task.ApprovalRoundID)
	if err != nil {
		return empty, err
	}
	if round.State != "in_review" {
		return empty, fault.Conflict("审批任务已结束，不能转交")
	}
	task, err = repo.LockApprovalTask(ctx, tenant, task.ContractID, task.ApprovalRoundID, p.TaskID)
	if err != nil {
		return empty, err
	}
	if task.State != "active" {
		return empty, fault.Conflict("审批任务已结束，不能转交")
	}
	if task.RowVersion != p.Input.ExpectedTaskRowVersion {
		return empty, fault.Conflict("审批任务已变化，请刷新后重试")
	}
	target, ok := directory.LookupUser(p.ToUserID)
	if !ok || target.TenantID != tenant || !target.Enabled || target.ID == current.ID {
		return empty, fault.Invalid("目标账号不存在、未启用或不是本租户成员")
	}
	bound, err := repo.ApprovalRoleBindingCurrent(ctx, tenant, task.RoleCode, record.OwnerOrgID, target.ID)
	if err != nil {
		return empty, err
	}
	if !bound {
		return empty, fault.Invalid("目标账号没有该会签角色的有效绑定")
	}
	from, err := repo.FirstApprovalAssignee(ctx, tenant, task)
	if err != nil {
		return empty, err
	}
	transferID, err := contract.NewContractUUID()
	if err != nil {
		return empty, err
	}
	at, err := repo.InsertTransfer(ctx, tenant, transferID, current.ID, from, p.Reason, task, target)
	if err != nil {
		return empty, err
	}
	if err = repo.InsertTransferAssignment(ctx, tenant, task, target); err != nil {
		return empty, err
	}
	version, err := contracts.CASApprovalTransition(ctx, tenant, record, false)
	if err != nil {
		return empty, err
	}
	eventID, err := contract.NewContractUUID()
	if err != nil {
		return empty, err
	}
	h := sha256.Sum256([]byte(p.Reason))
	body, err := json.Marshal(map[string]any{"contract_id": task.ContractID, "approval_round_id": task.ApprovalRoundID, "approval_task_id": task.ApprovalTaskID, "transfer_id": transferID, "from_user_id": from, "to_user_id": target.ID, "actor_id": current.ID, "reason_digest": hex.EncodeToString(h[:])})
	if err != nil {
		return empty, err
	}
	if err = events.AppendContractEvent(ctx, tenant, eventID, task.ContractID, version, "contract.approval_transferred.v1", body); err != nil {
		return empty, err
	}
	if err = events.EnqueueContractEvent(ctx, tenant, eventID); err != nil {
		return empty, err
	}
	result := model.TransferApprovalTaskResult{TransferID: transferID, TaskID: task.ApprovalTaskID, FromUserID: from, ToUserID: target.ID, ActorID: current.ID, RowVersion: version, TransferredAt: at.UTC()}
	response, err := json.Marshal(result)
	if err != nil {
		return empty, err
	}
	if err = contracts.InsertWriteReceipt(ctx, tenant, current.ID, TransferRoute, p.Input.IdempotencyKey, p.Digest, task.ContractID, 200, response); err != nil {
		return empty, err
	}
	return result, nil
}
