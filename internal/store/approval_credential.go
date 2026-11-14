// File metadata (manifest mirror):
// Purpose: ApprovalCredentialClaims is the non-secret projection returned with a credential. The token itself is the only bearer value; these claims tell the page which task, revision and… Related `store` operations include loadApprovalRevisionScope, IssueApprovalCredential, verifyApprovalCredential, loadApprovalRound.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-13
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"contractaudit/internal/accounts"
	approvalapp "contractaudit/internal/approval/app"
	approvalmodel "contractaudit/internal/approval/model"
	approvalpg "contractaudit/internal/approval/postgres"
	contractpg "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	platformpg "contractaudit/internal/platform/postgres"
)

type ApprovalCredentialClaims = approvalmodel.ApprovalCredentialClaims
type ApprovalCredentialResult = approvalmodel.ApprovalCredentialResult
type TransferApprovalTaskInput = approvalmodel.TransferApprovalTaskInput
type TransferApprovalTaskResult = approvalmodel.TransferApprovalTaskResult

// IssueApprovalCredential binds the current subject, role, organization, task,
// frozen revision and both row versions into a short-lived token. Every fact is
// resolved server-side; the request carries only the task id.
func (s *Store) IssueApprovalCredential(ctx context.Context, user accounts.User, taskID string) (ApprovalCredentialResult, error) {
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(taskID)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	if s.approvalKeyring == nil {
		return ApprovalCredentialResult{}, fault.New(503, "approval_credentials_disabled", "审批内部凭证未启用")
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	defer rollbackCLM(tx)
	executor := platformpg.Bind(tx)
	result, err := approvalapp.IssueCredential(ctx, current, id, s.approvalKeyring, approvalpg.NewDecision(executor), contractpg.NewApprovalCommands(executor), approvalCredentialAccess{user: current})
	if err != nil {
		return ApprovalCredentialResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ApprovalCredentialResult{}, err
	}
	return result, nil
}

// TransferApprovalTask appends one reassignment fact and one assignment row for
// the new approver. The original task and frozen routing snapshot are not
// rewritten. A second transfer of the same task conflicts.
func (s *Store) TransferApprovalTask(ctx context.Context, user accounts.User, input TransferApprovalTaskInput) (TransferApprovalTaskResult, error) {
	ctx = commandContext(ctx, user)
	prepared, err := approvalapp.PrepareTransfer(input)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}

	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能转交审批任务", nil)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	defer rollbackCLM(tx)
	executor := platformpg.Bind(tx)
	result, err := approvalapp.Transfer(ctx, prepared, current, contractpg.NewApprovalCommands(executor), approvalSubmissionAccess{tx: tx, user: current}, approvalDirectory{}, approvalpg.NewDecision(executor), storeVersionEventAdapter{tx: tx})
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return TransferApprovalTaskResult{}, err
	}
	return result, nil
}
