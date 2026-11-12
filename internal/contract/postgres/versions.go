package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// VersionCommands writes Contract-owned revision state through the exact
// caller-owned transaction and embeds the existing receipt/aggregate port.
type VersionCommands struct {
	*DraftCommands
	tx platform.Executor
}

func NewVersionCommands(tx platform.Executor) *VersionCommands {
	return &VersionCommands{DraftCommands: NewDraftCommands(tx), tx: tx}
}

func (r *VersionCommands) LoadDraftContent(ctx context.Context, tenantID, contractID string) ([]byte, error) {
	var content []byte
	err := r.tx.QueryRow(ctx, `SELECT content FROM clm_contract_drafts WHERE tenant_id=$1 AND contract_id=$2`,
		tenantID, contractID).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fault.Conflict("合同草稿已不存在，无法冻结合同版本")
	}
	return content, err
}

func (r *VersionCommands) CanonicalizeSnapshot(ctx context.Context, raw []byte) ([]byte, error) {
	var canonical string
	if err := r.tx.QueryRow(ctx, `SELECT ($1::jsonb)::text`, string(raw)).Scan(&canonical); err != nil {
		return nil, err
	}
	return []byte(canonical), nil
}

func (r *VersionCommands) NextRevisionNumber(ctx context.Context, tenantID, contractID string) (int, error) {
	var revision int
	err := r.tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision_number),0)+1 FROM clm_contract_revisions
		WHERE tenant_id=$1 AND contract_id=$2`, tenantID, contractID).Scan(&revision)
	return revision, err
}

func (r *VersionCommands) InsertFrozenRevision(ctx context.Context, tenantID, contractID string,
	revision clmcontract.FrozenContractRevision) (clmcontract.FrozenContractRevision, error) {
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_revisions
		(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by,amounts_reconciled)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,true) RETURNING frozen_at`,
		tenantID, contractID, revision.RevisionID, revision.RevisionNumber, revision.Amount, revision.Currency,
		string(revision.Snapshot), revision.SnapshotDigest, revision.FrozenBy).Scan(&revision.FrozenAt)
	if err != nil {
		return clmcontract.FrozenContractRevision{}, uniqueViolation(err, "合同版本已被他人冻结，请刷新后确认")
	}
	return revision, nil
}

func (r *VersionCommands) InsertFrozenRevisionItems(ctx context.Context, tenantID, contractID, revisionID string,
	items []clmcontract.RevisionPlanItem) error {
	for _, item := range items {
		if _, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_revision_items
			(tenant_id,contract_id,contract_revision_id,item_id,planned_quantity,unit)
			VALUES ($1,$2,$3,$4,$5,$6)`, tenantID, contractID, revisionID,
			item.ItemID, item.PlannedQuantity, item.Unit); err != nil {
			return err
		}
	}
	return nil
}

func (r *VersionCommands) InsertFrozenMaterials(ctx context.Context, tenantID, contractID, revisionID string,
	materials []clmcontract.FrozenVersionMaterial) error {
	for _, material := range materials {
		if _, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_version_materials
			(tenant_id,contract_id,contract_revision_id,material_id,material_version_id,case_org_id,case_id,material_version,filename,digest,purpose)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenantID, contractID, revisionID,
			material.MaterialID, material.MaterialVersionID, material.CaseOrgID, material.CaseID,
			material.MaterialVersion, material.Filename, material.Digest, material.Purpose); err != nil {
			return err
		}
	}
	return nil
}

func (r *VersionCommands) InsertVersionSeal(ctx context.Context, tenantID, contractID, revisionID string,
	seal clmcontract.ContractVersionSeal) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_version_seals
		(tenant_id,contract_id,contract_revision_id,snapshot_digest,materials_digest,content_fingerprint,restricted_fingerprints)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)`, tenantID, contractID, revisionID,
		seal.SnapshotDigest, seal.MaterialsDigest, seal.ContentFingerprint, string(seal.RestrictedFingerprints))
	return err
}

func (r *VersionCommands) AdvanceCurrentRevision(ctx context.Context, record clmcontract.Aggregate,
	revisionID string) (clmcontract.Aggregate, error) {
	err := r.tx.QueryRow(ctx, `UPDATE clm_contracts SET current_revision_id=$3, row_version=row_version+1, updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$4
			AND authority_mode='native' AND lifecycle_state='draft' AND validity_state='active'
		RETURNING row_version,updated_at`, record.TenantID, record.ContractID, revisionID, record.RowVersion).
		Scan(&record.RowVersion, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.Aggregate{}, fault.Conflict("合同状态或版本已变化，请刷新后确认")
	}
	if err != nil {
		return clmcontract.Aggregate{}, err
	}
	return record, nil
}

func (r *VersionCommands) CreateLegacyAggregate(ctx context.Context, record clmcontract.Aggregate) (clmcontract.Aggregate, error) {
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contracts
		(tenant_id,contract_id,owner_org_id,authority_mode,contract_number,source_system,external_contract_id,source_version,lifecycle_state,row_version,created_by)
		VALUES ($1,$2,$3,'native',$4,$5,$6,$7,'registered',1,$8) RETURNING created_at`,
		record.TenantID, record.ContractID, record.OwnerOrgID, record.ContractNumber,
		record.SourceSystem, record.ExternalContractID, record.SourceVersion, record.CreatedBy).Scan(&record.CreatedAt)
	if err != nil {
		return clmcontract.Aggregate{}, uniqueViolation(err, "合同编号或来源合同已存在")
	}
	record.UpdatedAt = record.CreatedAt
	return record, nil
}

func (r *VersionCommands) InsertLegacyRevision(ctx context.Context, tenantID, contractID string,
	revision clmcontract.FrozenContractRevision) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_revisions
		(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by,amounts_reconciled)
		VALUES ($1,$2,$3,1,$4,$5,$6::jsonb,$7,$8,$9)`, tenantID, contractID,
		revision.RevisionID, revision.Amount, revision.Currency, string(revision.Snapshot),
		revision.SnapshotDigest, revision.FrozenBy, revision.AmountsReconciled)
	return err
}

func (r *VersionCommands) SetLegacyCurrentRevision(ctx context.Context, tenantID, contractID, revisionID string) error {
	_, err := r.tx.Exec(ctx, `UPDATE clm_contracts SET current_revision_id=$3, updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2`, tenantID, contractID, revisionID)
	return err
}

func (r *VersionCommands) InsertContractCaseLink(ctx context.Context, tenantID, contractID, caseOrgID, caseID, actorID string) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_cases (tenant_id,contract_id,case_org_id,case_id,linked_by)
		VALUES ($1,$2,$3,$4,$5)`, tenantID, contractID, caseOrgID, caseID, actorID)
	return err
}

func (r *VersionCommands) InsertLegacyImport(ctx context.Context, tenantID, contractID string,
	input clmcontract.LegacyImportInput, facts clmcontract.LegacyImportFacts, revisionID, actorID string) (time.Time, error) {
	historicalJSON, err := json.Marshal(facts.Historical)
	if err != nil {
		return time.Time{}, err
	}
	var importedAt time.Time
	err = r.tx.QueryRow(ctx, `INSERT INTO clm_legacy_imports
		(tenant_id,contract_id,contract_revision_id,source_system,source_id,source_version,
		 case_org_id,case_id,material_id,material_version,material_digest,
		 approval_history_status,historical_approval_materials,reason,imported_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb,$14,$15)
		RETURNING imported_at`, tenantID, contractID, revisionID,
		input.SourceRef.System, input.SourceRef.ID, input.SourceRef.SourceVersion,
		facts.SourceEvidence.CaseOrgID, facts.SourceEvidence.CaseID, facts.SourceEvidence.MaterialID,
		facts.SourceEvidence.MaterialVersion, facts.SourceEvidence.Digest, input.ApprovalHistoryStatus,
		string(historicalJSON), input.Reason, actorID).Scan(&importedAt)
	return importedAt, err
}

type VersionQueries struct{ tx platform.Executor }

func NewVersionQueries(tx platform.Executor) *VersionQueries { return &VersionQueries{tx: tx} }

func (r *VersionQueries) ListVersionSummaries(ctx context.Context, tenantID, contractID string) ([]clmcontract.ContractVersionSummary, error) {
	rows, err := r.tx.Query(ctx, `SELECT r.contract_revision_id,r.revision_number,r.amount,r.currency,r.snapshot_digest,
			r.amounts_reconciled,r.frozen_by,r.frozen_at,
			COALESCE(s.materials_digest,''),COALESCE(s.content_fingerprint,''),
			(SELECT count(*) FROM clm_contract_version_materials m
			 WHERE m.tenant_id=r.tenant_id AND m.contract_id=r.contract_id AND m.contract_revision_id=r.contract_revision_id)
		FROM clm_contract_revisions r
		LEFT JOIN clm_contract_version_seals s
			ON s.tenant_id=r.tenant_id AND s.contract_id=r.contract_id AND s.contract_revision_id=r.contract_revision_id
		WHERE r.tenant_id=$1 AND r.contract_id=$2 ORDER BY r.revision_number DESC`, tenantID, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []clmcontract.ContractVersionSummary{}
	for rows.Next() {
		var item clmcontract.ContractVersionSummary
		if err := rows.Scan(&item.ContractRevisionID, &item.RevisionNumber, &item.Amount, &item.Currency,
			&item.SnapshotDigest, &item.AmountsReconciled, &item.FrozenBy, &item.FrozenAt,
			&item.MaterialsDigest, &item.ContentFingerprint, &item.MaterialCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *VersionQueries) LoadFrozenRevision(ctx context.Context, tenantID, contractID, revisionID string) (clmcontract.FrozenContractRevision, error) {
	var frozen clmcontract.FrozenContractRevision
	err := r.tx.QueryRow(ctx, `SELECT contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,
			amounts_reconciled,frozen_by,frozen_at
		FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
		tenantID, contractID, revisionID).Scan(&frozen.RevisionID, &frozen.RevisionNumber, &frozen.Amount,
		&frozen.Currency, &frozen.Snapshot, &frozen.SnapshotDigest, &frozen.AmountsReconciled,
		&frozen.FrozenBy, &frozen.FrozenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.FrozenContractRevision{}, fault.NotFound("没有找到对应的冻结版本")
	}
	return frozen, err
}

func (r *VersionQueries) LoadVersionSeal(ctx context.Context, tenantID, contractID, revisionID string) (*clmcontract.ContractVersionSeal, error) {
	var seal clmcontract.ContractVersionSeal
	err := r.tx.QueryRow(ctx, `SELECT snapshot_digest,materials_digest,content_fingerprint,restricted_fingerprints
		FROM clm_contract_version_seals WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
		tenantID, contractID, revisionID).Scan(&seal.SnapshotDigest, &seal.MaterialsDigest,
		&seal.ContentFingerprint, &seal.RestrictedFingerprints)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &seal, nil
}

func (r *VersionQueries) LoadVersionMaterials(ctx context.Context, tenantID, contractID, revisionID string) ([]clmcontract.FrozenVersionMaterial, error) {
	rows, err := r.tx.Query(ctx, `SELECT material_id,material_version_id,case_org_id,case_id,material_version,filename,digest,purpose
		FROM clm_contract_version_materials WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3
		ORDER BY material_id`, tenantID, contractID, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	materials := []clmcontract.FrozenVersionMaterial{}
	for rows.Next() {
		var material clmcontract.FrozenVersionMaterial
		if err := rows.Scan(&material.MaterialID, &material.MaterialVersionID, &material.CaseOrgID,
			&material.CaseID, &material.MaterialVersion, &material.Filename, &material.Digest,
			&material.Purpose); err != nil {
			return nil, err
		}
		materials = append(materials, material)
	}
	return materials, rows.Err()
}

func uniqueViolation(err error, message string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fault.Conflict(message)
	}
	return err
}
