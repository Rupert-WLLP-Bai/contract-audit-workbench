package postgres

import (
	"context"
	"errors"

	clmcontract "contractaudit/internal/contract/model"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// DraftQueries reads only Contract-owned rows through the caller's authorized
// transaction. Approval and signed-case authorization remain outside this adapter.
type DraftQueries struct{ tx platform.Executor }

func NewDraftQueries(tx platform.Executor) *DraftQueries { return &DraftQueries{tx: tx} }

func (r *DraftQueries) LoadDraftDetailFacts(ctx context.Context, record clmcontract.Aggregate) (clmcontract.DraftDetailFacts, error) {
	var facts clmcontract.DraftDetailFacts
	loadRevision := func() error {
		if record.CurrentRevisionID == nil {
			return nil
		}
		err := r.tx.QueryRow(ctx, `SELECT snapshot FROM clm_contract_revisions
			WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
			record.TenantID, record.ContractID, *record.CurrentRevisionID).Scan(&facts.RevisionSnapshot)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return err
		default:
			facts.HasRevision = true
			return nil
		}
	}
	if record.CurrentRevisionID != nil && record.LifecycleState != "draft" {
		if err := loadRevision(); err != nil {
			return clmcontract.DraftDetailFacts{}, err
		}
	} else {
		err := r.tx.QueryRow(ctx, `SELECT content FROM clm_contract_drafts
			WHERE tenant_id=$1 AND contract_id=$2`, record.TenantID, record.ContractID).Scan(&facts.DraftContent)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return clmcontract.DraftDetailFacts{}, err
		default:
			facts.HasDraft = true
		}
		if !facts.HasDraft && record.CurrentRevisionID != nil {
			if err := loadRevision(); err != nil {
				return clmcontract.DraftDetailFacts{}, err
			}
		}
	}

	if record.AuthorityMode == "external" {
		var observation clmcontract.DraftSourceObservation
		err := r.tx.QueryRow(ctx, `SELECT source_observed_state,disposition,recorded_at
			FROM clm_contract_source_observations WHERE tenant_id=$1 AND contract_id=$2
			ORDER BY recorded_at DESC,observation_id DESC LIMIT 1`, record.TenantID, record.ContractID).
			Scan(&observation.State, &observation.Disposition, &observation.ObservedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return clmcontract.DraftDetailFacts{}, err
		default:
			facts.SourceObservation = &observation
		}
	}

	if record.AuthorityMode != "external" && record.SourceSystem != nil && record.ExternalContractID != nil && record.SourceVersion != nil {
		var legacy clmcontract.DraftLegacyImport
		err := r.tx.QueryRow(ctx, `SELECT approval_history_status, imported_at FROM clm_legacy_imports
			WHERE tenant_id=$1 AND contract_id=$2`, record.TenantID, record.ContractID).
			Scan(&legacy.ApprovalHistoryStatus, &legacy.ImportedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return clmcontract.DraftDetailFacts{}, err
		default:
			facts.LegacyImport = &legacy
		}
	}
	return facts, nil
}

func (r *DraftQueries) ListMyDraftRows(ctx context.Context, tenantID, actorID string, limit int) ([]clmcontract.DraftRecoveryRow, error) {
	rows, err := r.tx.Query(ctx, `SELECT c.tenant_id,c.contract_id,c.owner_org_id,c.authority_mode,c.contract_number,
		c.source_system,c.external_contract_id,c.source_version,c.lifecycle_state,c.validity_state,
		c.current_revision_id,c.row_version,c.created_by,c.created_at,c.updated_at,
		d.updated_at,d.content->'contract'->>'name',d.content->'contract'->>'amount',d.content->'contract'->>'currency'
		FROM clm_contracts c JOIN clm_contract_drafts d
			ON d.tenant_id=c.tenant_id AND d.contract_id=c.contract_id
		WHERE c.tenant_id=$1 AND c.created_by=$2 AND c.authority_mode='native'
			AND c.lifecycle_state='draft' AND c.validity_state='active'
		ORDER BY d.updated_at DESC, c.contract_id DESC LIMIT $3`, tenantID, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []clmcontract.DraftRecoveryRow{}
	for rows.Next() {
		var item clmcontract.DraftRecoveryRow
		var name *string
		if err := rows.Scan(&item.Record.TenantID, &item.Record.ContractID, &item.Record.OwnerOrgID,
			&item.Record.AuthorityMode, &item.Record.ContractNumber, &item.Record.SourceSystem,
			&item.Record.ExternalContractID, &item.Record.SourceVersion, &item.Record.LifecycleState,
			&item.Record.ValidityState, &item.Record.CurrentRevisionID, &item.Record.RowVersion,
			&item.Record.CreatedBy, &item.Record.CreatedAt, &item.Record.UpdatedAt,
			&item.UpdatedAt, &name, &item.Amount, &item.Currency); err != nil {
			return nil, err
		}
		if name != nil {
			item.Name = *name
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
