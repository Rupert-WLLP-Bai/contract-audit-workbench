// File metadata (manifest mirror):
// Purpose: Reads Approval round, original assignment and latest transfer facts for credential and decision flows.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"
	"contractaudit/internal/approval/model"
	"contractaudit/internal/fault"
	"errors"
	"github.com/jackc/pgx/v5"
)

func (r *Decision) LoadApprovalRound(ctx context.Context, tenantID, contractID, roundID string) (model.ApprovalRoundRecord, error) {
	var round model.ApprovalRoundRecord
	err := r.tx.QueryRow(ctx, `SELECT approval_round_id,contract_id,contract_revision_id,round_number,state,
			workflow_template_id,workflow_template_version,COALESCE(routing_digest,''),row_version,
			started_at,ended_at,initiated_by
		FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3`,
		tenantID, contractID, roundID).Scan(&round.ApprovalRoundID, &round.ContractID,
		&round.ContractRevisionID, &round.RoundNumber, &round.State, &round.WorkflowTemplateID,
		&round.WorkflowTemplateVersion, &round.RoutingDigest, &round.RowVersion,
		&round.StartedAt, &round.EndedAt, &round.InitiatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ApprovalRoundRecord{}, fault.NotFound("没有找到对应会签轮次")
	}
	return round, err
}

func (r *Decision) FirstApprovalAssignee(ctx context.Context, tenantID string, task model.ApprovalTaskRecord) (string, error) {
	var userID string
	err := r.tx.QueryRow(ctx, `SELECT user_id FROM clm_approval_task_assignments
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
		ORDER BY user_id LIMIT 1`, tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID).
		Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fault.Conflict("审批任务没有受理人，不能转交")
	}
	return userID, err
}

// latestApprovalTransfer returns the transfer target for a task. A transferred
// task can only be decided by the latest target; the original assignees remain
// in the append-only assignment table as history but lose the live right.
func (r *Decision) LatestApprovalTransfer(ctx context.Context, tenantID string, task model.ApprovalTaskRecord) (string, bool, error) {
	var toUserID string
	err := r.tx.QueryRow(ctx, `SELECT to_user_id FROM clm_approval_task_transfers
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID).Scan(&toUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return toUserID, true, nil
}
