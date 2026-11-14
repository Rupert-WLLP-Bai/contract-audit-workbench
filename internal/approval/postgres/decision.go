// File metadata (manifest mirror):
// Purpose: Reads and locks Approval-owned task/round facts, appends decisions and performs task/round state CAS in the caller transaction.
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
	"contractaudit/internal/approval"
	"contractaudit/internal/approval/model"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"time"
)

type Decision struct{ tx platform.Executor }

func NewDecision(tx platform.Executor) *Decision { return &Decision{tx: tx} }

func (r *Decision) LoadApprovalTask(ctx context.Context, tenantID, taskID string) (model.ApprovalTaskRecord, error) {
	var task model.ApprovalTaskRecord
	err := r.tx.QueryRow(ctx, `SELECT approval_task_id,contract_id,approval_round_id,node_code,node_order,
			title,role_code,is_final,state,row_version,activated_at
		FROM clm_approval_tasks WHERE tenant_id=$1 AND approval_task_id=$2`,
		tenantID, taskID).Scan(&task.ApprovalTaskID, &task.ContractID, &task.ApprovalRoundID,
		&task.NodeCode, &task.NodeOrder, &task.Title, &task.RoleCode, &task.Final,
		&task.State, &task.RowVersion, &task.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ApprovalTaskRecord{}, fault.NotFound("没有找到对应审批任务")
	}
	return task, err
}

func (r *Decision) LockApprovalTask(ctx context.Context, tenantID, contractID, roundID, taskID string) (model.ApprovalTaskRecord, error) {
	var task model.ApprovalTaskRecord
	err := r.tx.QueryRow(ctx, `SELECT approval_task_id,contract_id,approval_round_id,node_code,node_order,
			title,role_code,is_final,state,row_version,activated_at
		FROM clm_approval_tasks
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
		FOR UPDATE`, tenantID, contractID, roundID, taskID).Scan(&task.ApprovalTaskID, &task.ContractID,
		&task.ApprovalRoundID, &task.NodeCode, &task.NodeOrder, &task.Title, &task.RoleCode,
		&task.Final, &task.State, &task.RowVersion, &task.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ApprovalTaskRecord{}, fault.NotFound("没有找到对应审批任务")
	}
	return task, err
}

func (r *Decision) LoadApprovalRoundForUpdate(ctx context.Context, tenantID, contractID, roundID string) (model.ApprovalRoundRecord, error) {
	var round model.ApprovalRoundRecord
	err := r.tx.QueryRow(ctx, `SELECT approval_round_id,contract_id,contract_revision_id,round_number,state,
			workflow_template_id,workflow_template_version,COALESCE(routing_digest,''),row_version,
			started_at,ended_at,initiated_by
		FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 FOR UPDATE`,
		tenantID, contractID, roundID).Scan(&round.ApprovalRoundID, &round.ContractID,
		&round.ContractRevisionID, &round.RoundNumber, &round.State, &round.WorkflowTemplateID,
		&round.WorkflowTemplateVersion, &round.RoutingDigest, &round.RowVersion,
		&round.StartedAt, &round.EndedAt, &round.InitiatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ApprovalRoundRecord{}, fault.NotFound("没有找到对应会签轮次")
	}
	return round, err
}

func (r *Decision) ApprovalTaskAssignedTo(ctx context.Context, tenantID string, task model.ApprovalTaskRecord, userID string) (bool, error) {
	var assigned bool
	err := r.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM clm_approval_task_assignments
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4 AND user_id=$5)`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID, userID).Scan(&assigned)
	return assigned, err
}

// approvalRoleBindingCurrent applies the live role-binding check at decision
// time. The frozen assignment proves who was routed at submission; this proves
// the operator has not since revoked that role for the contract organization.
func (r *Decision) ApprovalRoleBindingCurrent(ctx context.Context, tenantID, roleCode, ownerOrgID, userID string) (bool, error) {
	var bound bool
	err := r.tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM clm_workflow_role_bindings binding
		JOIN clm_workflow_roles role ON role.tenant_id=binding.tenant_id AND role.role_code=binding.role_code
		WHERE binding.tenant_id=$1 AND binding.role_code=$2 AND binding.user_id=$3
			AND (binding.org_scope='' OR binding.org_scope=$4))`,
		tenantID, roleCode, userID, ownerOrgID).Scan(&bound)
	return bound, err
}

func (r *Decision) LockNextPendingApprovalTask(ctx context.Context, tenantID string, current model.ApprovalTaskRecord) (*model.ApprovalTaskRecord, error) {
	var task model.ApprovalTaskRecord
	err := r.tx.QueryRow(ctx, `SELECT approval_task_id,contract_id,approval_round_id,node_code,node_order,
			title,role_code,is_final,state,row_version,activated_at
		FROM clm_approval_tasks
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3
			AND state='pending' AND node_order > $4
		ORDER BY node_order LIMIT 1 FOR UPDATE`,
		tenantID, current.ContractID, current.ApprovalRoundID, current.NodeOrder).Scan(
		&task.ApprovalTaskID, &task.ContractID, &task.ApprovalRoundID, &task.NodeCode,
		&task.NodeOrder, &task.Title, &task.RoleCode, &task.Final, &task.State,
		&task.RowVersion, &task.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *Decision) CloseApprovalTask(ctx context.Context, tenantID string, task model.ApprovalTaskRecord, state, actorID string) error {
	tag, err := r.tx.Exec(ctx, `UPDATE clm_approval_tasks
		SET state=$5, row_version=row_version+1, decided_at=clock_timestamp(), decided_by=$6
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
			AND state='active' AND row_version=$7`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID,
		state, actorID, task.RowVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict("审批任务已变化，请刷新待办后重试")
	}
	return nil
}

func (r *Decision) ActivateApprovalTask(ctx context.Context, tenantID string, task *model.ApprovalTaskRecord) error {
	tag, err := r.tx.Exec(ctx, `UPDATE clm_approval_tasks
		SET state='active', row_version=row_version+1, activated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
			AND state='pending' AND row_version=$5`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID, task.RowVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict("下一审批任务已变化，请刷新待办后重试")
	}
	task.State = "active"
	task.RowVersion++
	return nil
}

func (r *Decision) CancelPendingApprovalTasks(ctx context.Context, tenantID string, task model.ApprovalTaskRecord, actorID string) error {
	_, err := r.tx.Exec(ctx, `UPDATE clm_approval_tasks
		SET state='cancelled', row_version=row_version+1, decided_at=clock_timestamp(), decided_by=$5
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id<>$4
			AND state='pending'`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID, actorID)
	return err
}

func (r *Decision) LoadApprovalTaskSummary(ctx context.Context, tenantID string, task model.ApprovalTaskRecord) (model.ApprovalTaskSummary, error) {
	rows, err := r.tx.Query(ctx, `SELECT user_id FROM clm_approval_task_assignments
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
		ORDER BY user_id`, tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID)
	if err != nil {
		return model.ApprovalTaskSummary{}, err
	}
	defer rows.Close()
	assignees := []string{}
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return model.ApprovalTaskSummary{}, err
		}
		assignees = append(assignees, userID)
	}
	if err := rows.Err(); err != nil {
		return model.ApprovalTaskSummary{}, err
	}
	return approvalTaskSummary(task, assignees), nil
}

func approvalTaskSummary(task model.ApprovalTaskRecord, assignees []string) model.ApprovalTaskSummary {
	if assignees == nil {
		assignees = []string{}
	}
	return model.ApprovalTaskSummary{
		ApprovalTaskID: task.ApprovalTaskID, NodeCode: task.NodeCode, NodeOrder: task.NodeOrder,
		Title: task.Title, RoleCode: task.RoleCode, Final: task.Final, State: task.State,
		AssigneeIDs: assignees,
	}
}

func (r *Decision) InsertDecision(ctx context.Context, tenantID, decisionID, actorID, decision, comment, workflowState, credentialDigest string, task model.ApprovalTaskRecord, round model.ApprovalRoundRecord, claims *approval.Claims) error {
	var actorOrgID, credentialID, keyID, digest, snapshot, amount, currency, contractVersion any
	var issued, expires any
	if claims != nil {
		actorOrgID = claims.OrgID
		credentialID = claims.CredentialID
		keyID = claims.KeyID
		digest = credentialDigest
		issued = time.Unix(claims.IssuedAt, 0).UTC()
		expires = time.Unix(claims.ExpiresAt, 0).UTC()
		snapshot = claims.SnapshotDigest
		amount = claims.AmountLimit
		currency = claims.Currency
		contractVersion = claims.ContractRowVersion
	}
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_approval_decisions (tenant_id,decision_id,contract_id,approval_round_id,approval_task_id,contract_revision_id,node_code,node_order,role_code,decision,comment,decided_by,round_row_version_after,workflow_state_after,actor_org_id,credential_id,credential_key_id,credential_digest,credential_issued_at,credential_expires_at,snapshot_digest,amount_limit,currency,contract_row_version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22::text::numeric,$23,$24)`, tenantID, decisionID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID, round.ContractRevisionID, task.NodeCode, task.NodeOrder, task.RoleCode, decision, comment, actorID, round.RowVersion+1, workflowState, actorOrgID, credentialID, keyID, digest, issued, expires, snapshot, amount, currency, contractVersion)
	if err != nil {
		var e *pgconn.PgError
		if errors.As(err, &e) && e.Code == "23505" {
			return fault.Conflict("该审批节点已有决定")
		}
	}
	return err
}
func (r *Decision) UpdateRound(ctx context.Context, tenantID string, task model.ApprovalTaskRecord, version int64, state string) (int64, *time.Time, error) {
	var v int64
	var ended time.Time
	var err error
	if state == "in_review" {
		err = r.tx.QueryRow(ctx, `UPDATE clm_approval_rounds SET row_version=row_version+1 WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND state='in_review' AND row_version=$4 RETURNING row_version`, tenantID, task.ContractID, task.ApprovalRoundID, version).Scan(&v)
	} else {
		err = r.tx.QueryRow(ctx, `UPDATE clm_approval_rounds SET state=$5, row_version=row_version+1, ended_at=clock_timestamp() WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND state='in_review' AND row_version=$4 RETURNING ended_at,row_version`, tenantID, task.ContractID, task.ApprovalRoundID, version, state).Scan(&ended, &v)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, fault.Conflict("审批轮次已变化，请刷新待办后重试")
	}
	if err != nil {
		return 0, nil, err
	}
	if state == "in_review" {
		return v, nil, nil
	}
	return v, &ended, nil
}
