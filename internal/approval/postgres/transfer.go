// File metadata (manifest mirror):
// Purpose: Appends Approval transfer and assignment history in the caller-owned transaction.
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
	"contractaudit/internal/accounts"
	"contractaudit/internal/approval/model"
	"contractaudit/internal/fault"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"time"
)

func (r *Decision) InsertTransfer(ctx context.Context, tenantID, transferID, actorID, fromID, reason string, task model.ApprovalTaskRecord, target accounts.User) (time.Time, error) {
	var at time.Time
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_approval_task_transfers (tenant_id,contract_id,approval_round_id,transfer_id,approval_task_id,from_user_id,to_user_id,to_org_id,reason,actor_id,from_task_row_version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING transferred_at`, tenantID, task.ContractID, task.ApprovalRoundID, transferID, task.ApprovalTaskID, fromID, target.ID, target.OrgID, reason, actorID, task.RowVersion).Scan(&at)
	if err != nil {
		var e *pgconn.PgError
		if errors.As(err, &e) && e.Code == "23505" {
			return at, fault.Conflict("该审批任务已经转交")
		}
	}
	return at, err
}
func (r *Decision) InsertTransferAssignment(ctx context.Context, tenantID string, task model.ApprovalTaskRecord, target accounts.User) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_approval_task_assignments (tenant_id,contract_id,approval_round_id,approval_task_id,user_id,display_name,role_code,binding_org_scope) VALUES ($1,$2,$3,$4,$5,$6,$7,'') ON CONFLICT DO NOTHING`, tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID, target.ID, target.DisplayName, task.RoleCode)
	return err
}
