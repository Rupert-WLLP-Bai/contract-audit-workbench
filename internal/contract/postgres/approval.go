package postgres

import (
	"context"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"
	"errors"
	"github.com/jackc/pgx/v5"
)

type Approval struct{ tx platform.Executor }

func NewApproval(tx platform.Executor) *Approval { return &Approval{tx: tx} }
func (r *Approval) CASSubmission(ctx context.Context, tenantID, contractID string, version int64, revisionID string) (int64, error) {
	var v int64
	err := r.tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1, updated_at=clock_timestamp() WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3 AND authority_mode='native' AND lifecycle_state='draft' AND validity_state='active' AND current_revision_id=$4 RETURNING row_version`, tenantID, contractID, version, revisionID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fault.Conflict("合同状态或版本已变化，请刷新后确认")
	}
	return v, err
}

type ApprovalFreeze struct {
	*VersionQueries
	commands *VersionCommands
}

func NewApprovalFreeze(tx platform.Executor) *ApprovalFreeze {
	return &ApprovalFreeze{VersionQueries: NewVersionQueries(tx), commands: NewVersionCommands(tx)}
}
func (r *ApprovalFreeze) LoadDraftContent(ctx context.Context, tenantID, contractID string) ([]byte, error) {
	return r.commands.LoadDraftContent(ctx, tenantID, contractID)
}

type ApprovalCommands struct {
	*DraftCommands
	*Approval
}

func NewApprovalCommands(tx platform.Executor) *ApprovalCommands {
	return &ApprovalCommands{DraftCommands: NewDraftCommands(tx), Approval: NewApproval(tx)}
}
