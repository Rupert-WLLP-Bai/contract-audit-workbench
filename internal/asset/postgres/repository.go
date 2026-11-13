package postgres

import (
	"context"
	asset "contractaudit/internal/asset/app"
	platform "contractaudit/internal/platform/postgres"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type Repository struct{ tx platform.Executor }

func NewRepository(tx platform.Executor) *Repository { return &Repository{tx: tx} }

func (r *Repository) LoadProjectedPlans(ctx context.Context, tenant, contract, revision string) (map[string]asset.ProjectedPlan, error) {
	rows, err := r.tx.Query(ctx, `SELECT asset_plan_id::text,item_id::text,planned_quantity::text,unit,department_code,department_name,custodian_user_id FROM clm_contract_revision_asset_plans WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 ORDER BY asset_plan_id`, tenant, contract, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := map[string]asset.ProjectedPlan{}
	for rows.Next() {
		var plan asset.ProjectedPlan
		if err = rows.Scan(&plan.AssetPlanID, &plan.ItemID, &plan.Quantity, &plan.Unit, &plan.DepartmentCode, &plan.DepartmentName, &plan.CustodianUser); err != nil {
			return nil, err
		}
		plans[plan.AssetPlanID] = plan
	}
	return plans, rows.Err()
}

func (r *Repository) ListReceipts(ctx context.Context, tenant, contract, handover string) ([]asset.ContractAssetReceipt, error) {
	rows, err := r.tx.Query(ctx, `SELECT receipt_id::text,source_id,source_key_id,source_event_id,event_type,COALESCE(original_source_event_id,''),asset_code,quantity::text,unit,occurred_at,recorded_at,payload_digest FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND contract_id=$2 AND handover_id=$3 ORDER BY occurred_at,receipt_id`, tenant, contract, handover)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []asset.ContractAssetReceipt{}
	for rows.Next() {
		var r asset.ContractAssetReceipt
		r.ContractID = contract
		r.HandoverID = handover
		if err = rows.Scan(&r.ReceiptID, &r.SourceID, &r.SourceKeyID, &r.SourceEventID, &r.EventType, &r.OriginalSourceEventID, &r.AssetCode, &r.Quantity, &r.Unit, &r.OccurredAt, &r.RecordedAt, &r.PayloadDigest); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r *Repository) LoadReceipt(ctx context.Context, tenant, source, event string) (asset.ContractAssetReceipt, error) {
	var receipt asset.ContractAssetReceipt
	err := r.tx.QueryRow(ctx, `SELECT receipt_id::text,contract_id::text,handover_id::text,source_id,source_key_id,source_event_id,event_type,COALESCE(original_source_event_id,''),asset_code,quantity::text,unit,occurred_at,recorded_at,payload_digest FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND source_id=$2 AND source_event_id=$3`, tenant, source, event).Scan(&receipt.ReceiptID, &receipt.ContractID, &receipt.HandoverID, &receipt.SourceID, &receipt.SourceKeyID, &receipt.SourceEventID, &receipt.EventType, &receipt.OriginalSourceEventID, &receipt.AssetCode, &receipt.Quantity, &receipt.Unit, &receipt.OccurredAt, &receipt.RecordedAt, &receipt.PayloadDigest)
	return receipt, err
}

func (r *Repository) LoadPlan(ctx context.Context, tenant, contract, revision, planID string) (asset.ProjectedPlan, bool, error) {
	var plan asset.ProjectedPlan
	err := r.tx.QueryRow(ctx, `SELECT item_id::text,planned_quantity::text,unit,department_code,department_name,custodian_user_id
 FROM clm_contract_revision_asset_plans WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND asset_plan_id=$4`, tenant, contract, revision, planID).
		Scan(&plan.ItemID, &plan.Quantity, &plan.Unit, &plan.DepartmentCode, &plan.DepartmentName, &plan.CustodianUser)
	plan.AssetPlanID = planID
	if errors.Is(err, pgx.ErrNoRows) {
		return asset.ProjectedPlan{}, false, nil
	}
	return plan, err == nil, err
}

func (r *Repository) LockSourceEvent(ctx context.Context, tenant, source, event string) error {
	raw, err := json.Marshal([]string{tenant, source, event})
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	_, err = r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-asset-event:' || $1,0))`, hex.EncodeToString(hash[:]))
	return err
}

func (r *Repository) HasOverlap(ctx context.Context, tenant, contract, revision, item, delivery, start, end string) (bool, error) {
	var overlap bool
	err := r.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_asset_handovers
 WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4 AND delivery_id=$5
 AND scope_start < $7::numeric AND scope_end > $6::numeric)`, tenant, contract, revision, item, delivery, start, end).Scan(&overlap)
	return overlap, err
}
func (r *Repository) AllocatedQuantity(ctx context.Context, tenant, contract, revision, plan string) (string, error) {
	var allocated string
	err := r.tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0)::text FROM clm_contract_asset_handovers
 WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND asset_plan_id=$4`, tenant, contract, revision, plan).Scan(&allocated)
	return allocated, err
}
func (r *Repository) InsertHandover(ctx context.Context, tenant, custodianTenant string, h asset.ContractAssetHandover, actor string, evidence []byte) (time.Time, error) {
	var recordedAt time.Time
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_asset_handovers
 (tenant_id,contract_id,contract_revision_id,handover_id,asset_plan_id,delivery_id,item_id,acceptance_id,scope_start,scope_end,unit,handed_over_at,location,transfer_note,evidence_snapshot,department_code_claim,department_name_claim,department_verification,custodian_user_id,custodian_display_name_snapshot,custodian_tenant_id_snapshot,custodian_org_id_snapshot,custodian_directory_source,custodian_directory_revision,custodian_directory_checked_at,custodian_verification,recorded_by,contract_row_version)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::numeric,$10::numeric,$11,$12,$13,$14,$15::jsonb,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)
 RETURNING recorded_at`, tenant, h.ContractID, h.ContractRevisionID, h.HandoverID, h.AssetPlanID, h.DeliveryID, h.ItemID, h.AcceptanceID, h.ScopeStart, h.ScopeEnd, h.Unit, h.HandedOverAt, h.Location, h.TransferNote, string(evidence), h.DepartmentCodeClaim, h.DepartmentNameClaim, h.DepartmentVerification, h.CustodianUserID, h.CustodianDisplayName, custodianTenant, h.CustodianOrgID, h.CustodianDirectorySource, h.CustodianDirectoryRevision, h.CustodianDirectoryChecked, h.CustodianVerification, actor, h.ContractRowVersion).Scan(&recordedAt)
	return recordedAt, err
}

func (r *Repository) LoadReceiptDigest(ctx context.Context, tenant, source, event string) (string, bool, error) {
	var digest string
	err := r.tx.QueryRow(ctx, `SELECT payload_digest FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND source_id=$2 AND source_event_id=$3`, tenant, source, event).Scan(&digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return digest, err == nil, err
}
func (r *Repository) LoadReceiptHandover(ctx context.Context, tenant, contract, handover string) (asset.ReceiptHandover, bool, error) {
	var h asset.ReceiptHandover
	err := r.tx.QueryRow(ctx, `SELECT contract_revision_id::text,asset_plan_id::text,item_id::text,unit,quantity::text FROM clm_contract_asset_handovers WHERE tenant_id=$1 AND contract_id=$2 AND handover_id=$3`, tenant, contract, handover).Scan(&h.RevisionID, &h.AssetPlanID, &h.ItemID, &h.Unit, &h.Quantity)
	if errors.Is(err, pgx.ErrNoRows) {
		return asset.ReceiptHandover{}, false, nil
	}
	return h, err == nil, err
}
func (r *Repository) SumReceiptReversals(ctx context.Context, tenant, source, event string) (string, error) {
	var total string
	err := r.tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0)::text FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND source_id=$2 AND original_source_event_id=$3 AND event_type='reversal'`, tenant, source, event).Scan(&total)
	return total, err
}
func (r *Repository) NetHandoverReceipts(ctx context.Context, tenant, contract, handover string) (string, error) {
	var net string
	err := r.tx.QueryRow(ctx, `SELECT COALESCE(sum(CASE WHEN event_type='receipt' THEN quantity ELSE -quantity END),0)::text FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND contract_id=$2 AND handover_id=$3`, tenant, contract, handover).Scan(&net)
	return net, err
}
func (r *Repository) InsertReceipt(ctx context.Context, tenant string, receipt asset.ContractAssetReceipt) (time.Time, error) {
	var recorded time.Time
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_asset_receipts(tenant_id,contract_id,handover_id,receipt_id,source_id,source_key_id,source_event_id,event_type,original_source_event_id,asset_code,quantity,unit,occurred_at,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11::numeric,$12,$13,$14) RETURNING recorded_at`, tenant, receipt.ContractID, receipt.HandoverID, receipt.ReceiptID, receipt.SourceID, receipt.SourceKeyID, receipt.SourceEventID, receipt.EventType, receipt.OriginalSourceEventID, receipt.AssetCode, receipt.Quantity, receipt.Unit, receipt.OccurredAt, receipt.PayloadDigest).Scan(&recorded)
	return recorded, err
}

func (r *Repository) ListHandovers(ctx context.Context, tenant, contract, revision, plan string) ([]asset.HandoverRow, error) {
	rows, err := r.tx.Query(ctx, `SELECT handover_id::text,delivery_id::text,item_id::text,acceptance_id::text,scope_start::text,scope_end::text,quantity::text,unit,handed_over_at,location,transfer_note,evidence_snapshot,department_code_claim,department_name_claim,department_verification,custodian_user_id,custodian_display_name_snapshot,custodian_org_id_snapshot,custodian_directory_source,custodian_directory_revision,custodian_directory_checked_at,custodian_verification,recorded_by,recorded_at,contract_row_version FROM clm_contract_asset_handovers WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND asset_plan_id=$4 ORDER BY recorded_at,handover_id`, tenant, contract, revision, plan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	loaded := []asset.HandoverRow{}
	for rows.Next() {
		var h asset.ContractAssetHandover
		var evidence []byte
		if err = rows.Scan(&h.HandoverID, &h.DeliveryID, &h.ItemID, &h.AcceptanceID, &h.ScopeStart, &h.ScopeEnd, &h.Quantity, &h.Unit, &h.HandedOverAt, &h.Location, &h.TransferNote, &evidence, &h.DepartmentCodeClaim, &h.DepartmentNameClaim, &h.DepartmentVerification, &h.CustodianUserID, &h.CustodianDisplayName, &h.CustodianOrgID, &h.CustodianDirectorySource, &h.CustodianDirectoryRevision, &h.CustodianDirectoryChecked, &h.CustodianVerification, &h.RecordedBy, &h.RecordedAt, &h.ContractRowVersion); err != nil {
			return nil, err
		}
		loaded = append(loaded, asset.HandoverRow{Handover: h, Evidence: evidence})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return loaded, nil
}
