// File metadata (manifest mirror):
// Purpose: CLM-08 advances a submitted round one active task at a time. Decisions are append-only evidence; the round and task state updates happen in the same transaction, and a newer round… Related `store` operations include DecideApprovalTask, approvalDecisionWorkflowState, finishApprovalDecision, loadApprovalTask.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-02
// Revision: r2
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
	platformpg "contractaudit/internal/platform/postgres"
)

// CLM-08 advances a submitted round one active task at a time. Decisions are
// append-only evidence; the round and task state updates happen in the same
// transaction, and a newer round never rewrites an older round's decisions.
const (
	clmDecisionApprove = approvalmodel.DecisionApprove
	clmDecisionReturn  = approvalmodel.DecisionReturn
)

type DecideApprovalTaskInput = approvalmodel.DecideApprovalTaskInput
type ApprovalDecisionResult = approvalmodel.ApprovalDecisionResult
type clmApprovalRoundRecord = approvalmodel.ApprovalRoundRecord
type clmApprovalTaskRecord = approvalmodel.ApprovalTaskRecord

// DecideApprovalTask appends one approve/return decision, then advances the
// sequential round or closes it. The contract advisory lock is always taken
// before the round and task row locks; two concurrent decisions therefore
// produce one success and one 409, never two answers for the same node.
func (s *Store) DecideApprovalTask(ctx context.Context, user accounts.User, input DecideApprovalTaskInput) (ApprovalDecisionResult, error) {
	ctx = commandContext(ctx, user)
	prepared, err := approvalapp.PrepareDecision(input)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}

	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能提交审批决定", nil)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	defer rollbackCLM(tx)
	executor := platformpg.Bind(tx)
	result, err := approvalapp.Decide(ctx, prepared, current, s.approvalKeyring, contractpg.NewApprovalCommands(executor), decisionAccess(tx, current), approvalpg.NewDecision(executor), storeVersionEventAdapter{tx: tx})
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalDecisionResult{}, err
	}
	return result, nil
}
