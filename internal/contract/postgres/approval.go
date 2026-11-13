package postgres

import (
	"context"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
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

func (r *Approval) LoadApprovalRevisionScope(ctx context.Context, tenantID, contractID, revisionID string) (contractapp.ApprovalRevisionScope, error) {
	var scope contractapp.ApprovalRevisionScope
	err := r.tx.QueryRow(ctx, `SELECT revision.revision_number, revision.amount::text, revision.currency, seal.snapshot_digest FROM clm_contract_revisions revision JOIN clm_contract_version_seals seal ON seal.tenant_id=revision.tenant_id AND seal.contract_id=revision.contract_id AND seal.contract_revision_id=revision.contract_revision_id WHERE revision.tenant_id=$1 AND revision.contract_id=$2 AND revision.contract_revision_id=$3`, tenantID, contractID, revisionID).Scan(&scope.RevisionNumber, &scope.Amount, &scope.Currency, &scope.SnapshotDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return scope, fault.Conflict("该版本没有可核验的冻结封印")
	}
	return scope, err
}
func (r *Approval) CASApprovalTransition(ctx context.Context, tenantID string, record contract.Aggregate, complete bool) (int64, error) {
	var version int64
	var err error
	if !complete {
		err = r.tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1, updated_at=clock_timestamp() WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3 AND lifecycle_state='draft' AND validity_state='active' RETURNING row_version`, tenantID, record.ContractID, record.RowVersion).Scan(&version)
	} else {
		err = r.tx.QueryRow(ctx, `UPDATE clm_contracts SET lifecycle_state=$4, row_version=row_version+1, updated_at=clock_timestamp() WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3 AND lifecycle_state='draft' AND validity_state='active' RETURNING row_version`, tenantID, record.ContractID, record.RowVersion, "ready_to_sign").Scan(&version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fault.Conflict("合同状态或版本已变化，请刷新后确认")
	}
	return version, err
}
