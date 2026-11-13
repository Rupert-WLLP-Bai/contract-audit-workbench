package postgres

import (
	"context"
	"errors"
	"time"

	fulfillmentapp "contractaudit/internal/fulfillment/app"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// Repository persists only Fulfillment-owned facts through the caller's
// transaction-bound executor. It never reads Contract revision tables.
type Repository struct{ tx platform.Executor }

func NewRepository(tx platform.Executor) *Repository { return &Repository{tx: tx} }

func (r *Repository) SumDelivered(ctx context.Context, tenantID, contractID, revisionID, itemID string) (string, error) {
	var total string
	err := r.tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0)::text FROM clm_contract_deliveries
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4`,
		tenantID, contractID, revisionID, itemID).Scan(&total)
	return total, err
}

func (r *Repository) SumPlanDelivery(ctx context.Context, tenantID, contractID, revisionID, itemID string, sequence int) (string, error) {
	var total string
	err := r.tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0)::text FROM clm_contract_deliveries
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4 AND delivery_plan_sequence=$5`,
		tenantID, contractID, revisionID, itemID, sequence).Scan(&total)
	return total, err
}

func (r *Repository) InsertDelivery(ctx context.Context, item fulfillmentapp.DeliveryInsert) (time.Time, error) {
	var recordedAt time.Time
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_deliveries
		(tenant_id,contract_id,contract_revision_id,delivery_id,delivery_plan_sequence,item_id,batch_reference,quantity,unit,actual_at,location,description,evidence_snapshot,recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::numeric,$9,$10,$11,$12,$13::jsonb,$14) RETURNING recorded_at`,
		item.TenantID, item.ContractID, item.ContractRevisionID, item.DeliveryID, item.DeliveryPlanSequence,
		item.ItemID, item.BatchReference, item.Quantity, item.Unit, item.ActualAt, item.Location, item.Description,
		string(item.EvidenceSnapshot), item.RecordedBy).Scan(&recordedAt)
	return recordedAt, err
}

func (r *Repository) LoadDelivery(ctx context.Context, tenantID, contractID, revisionID, deliveryID string) (fulfillmentapp.DeliveryFact, bool, error) {
	var fact fulfillmentapp.DeliveryFact
	err := r.tx.QueryRow(ctx, `SELECT delivery_id::text,contract_revision_id::text,delivery_plan_sequence,item_id::text,
		batch_reference,quantity::text,unit,actual_at,location,description,evidence_snapshot,recorded_by,recorded_at
		FROM clm_contract_deliveries
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND delivery_id=$4`,
		tenantID, contractID, revisionID, deliveryID).Scan(&fact.DeliveryID, &fact.ContractRevisionID,
		&fact.DeliveryPlanSequence, &fact.ItemID, &fact.BatchReference, &fact.Quantity, &fact.Unit,
		&fact.ActualAt, &fact.Location, &fact.Description, &fact.EvidenceSnapshot, &fact.RecordedBy, &fact.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fulfillmentapp.DeliveryFact{}, false, nil
	}
	return fact, err == nil, err
}

func (r *Repository) HasOverlappingRootAcceptance(ctx context.Context, tenantID, contractID, deliveryID, start, end string) (bool, error) {
	var overlaps bool
	err := r.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_acceptances
		WHERE tenant_id=$1 AND contract_id=$2 AND delivery_id=$3 AND reinspection_of IS NULL
		AND numrange(scope_start,scope_end,'[)') && numrange($4::numeric,$5::numeric,'[)'))`,
		tenantID, contractID, deliveryID, start, end).Scan(&overlaps)
	return overlaps, err
}

func (r *Repository) LoadAcceptance(ctx context.Context, tenantID, contractID, acceptanceID string) (fulfillmentapp.AcceptanceFact, bool, error) {
	var fact fulfillmentapp.AcceptanceFact
	err := r.tx.QueryRow(ctx, `SELECT acceptance_id::text,contract_revision_id::text,delivery_id::text,item_id::text,
		acceptance_plan_sequence,scope_start::text,scope_end::text,accepted_quantity::text,result,
		COALESCE(reinspection_of::text,''),actual_at,standard,inspector_note,evidence_snapshot,recorded_by,recorded_at
		FROM clm_contract_acceptances WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3`,
		tenantID, contractID, acceptanceID).Scan(&fact.AcceptanceID, &fact.ContractRevisionID, &fact.DeliveryID,
		&fact.ItemID, &fact.AcceptancePlanSequence, &fact.ScopeStart, &fact.ScopeEnd, &fact.AcceptedQuantity,
		&fact.Result, &fact.ReinspectionOf, &fact.ActualAt, &fact.Standard, &fact.InspectorNote,
		&fact.EvidenceSnapshot, &fact.RecordedBy, &fact.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fulfillmentapp.AcceptanceFact{}, false, nil
	}
	return fact, err == nil, err
}

func (r *Repository) ListAcceptanceDefects(ctx context.Context, tenantID, contractID, acceptanceID string) ([]fulfillmentapp.DefectFact, error) {
	rows, err := r.tx.Query(ctx, `SELECT defect_id::text,COALESCE(prior_defect_id::text,''),description,responsible,
		remediation_due::text,failed_quantity::text,recorded_by,recorded_at
		FROM clm_contract_acceptance_defects
		WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 ORDER BY defect_id`, tenantID, contractID, acceptanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := []fulfillmentapp.DefectFact{}
	for rows.Next() {
		var fact fulfillmentapp.DefectFact
		if err := rows.Scan(&fact.DefectID, &fact.PriorDefectID, &fact.Description, &fact.Responsible,
			&fact.RemediationDue, &fact.FailedQuantity, &fact.RecordedBy, &fact.RecordedAt); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (r *Repository) ListAcceptanceRectifications(ctx context.Context, tenantID, contractID, acceptanceID string) ([]fulfillmentapp.RectificationFact, error) {
	rows, err := r.tx.Query(ctx, `SELECT rectification_id::text,contract_revision_id::text,acceptance_id::text,
		defect_id::text,description,evidence_snapshot,recorded_by,recorded_at
		FROM clm_contract_rectifications WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3
		ORDER BY recorded_at,rectification_id`, tenantID, contractID, acceptanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := []fulfillmentapp.RectificationFact{}
	for rows.Next() {
		var fact fulfillmentapp.RectificationFact
		if err := rows.Scan(&fact.RectificationID, &fact.ContractRevisionID, &fact.AcceptanceID,
			&fact.DefectID, &fact.Description, &fact.EvidenceSnapshot, &fact.RecordedBy, &fact.RecordedAt); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (r *Repository) HasReinspection(ctx context.Context, tenantID, contractID, acceptanceID string) (bool, error) {
	var exists bool
	err := r.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_acceptances
		WHERE tenant_id=$1 AND contract_id=$2 AND reinspection_of=$3)`, tenantID, contractID, acceptanceID).Scan(&exists)
	return exists, err
}

func (r *Repository) HasDefect(ctx context.Context, tenantID, contractID, acceptanceID, defectID string) (bool, error) {
	var exists bool
	err := r.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_acceptance_defects
		WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 AND defect_id=$4)`,
		tenantID, contractID, acceptanceID, defectID).Scan(&exists)
	return exists, err
}

func (r *Repository) InsertAcceptance(ctx context.Context, item fulfillmentapp.AcceptanceInsert) (time.Time, error) {
	var recordedAt time.Time
	var parent any
	if item.ReinspectionOf != "" {
		parent = item.ReinspectionOf
	}
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_acceptances
		(tenant_id,contract_id,contract_revision_id,acceptance_id,delivery_id,item_id,acceptance_plan_sequence,scope_start,scope_end,accepted_quantity,result,reinspection_of,actual_at,standard,inspector_note,evidence_snapshot,recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::numeric,$9::numeric,$10::numeric,$11,$12,$13,$14,$15,$16::jsonb,$17) RETURNING recorded_at`,
		item.TenantID, item.ContractID, item.ContractRevisionID, item.AcceptanceID, item.DeliveryID, item.ItemID,
		item.PlanSequence, item.ScopeStart, item.ScopeEnd, item.AcceptedQuantity, item.Result, parent, item.ActualAt,
		item.Standard, item.InspectorNote, string(item.EvidenceSnapshot), item.RecordedBy).Scan(&recordedAt)
	return recordedAt, err
}

func (r *Repository) LoadRectification(ctx context.Context, tenantID, contractID, rectificationID string) (fulfillmentapp.RectificationFact, bool, error) {
	var fact fulfillmentapp.RectificationFact
	err := r.tx.QueryRow(ctx, `SELECT rectification_id::text,contract_revision_id::text,acceptance_id::text,
		defect_id::text,description,evidence_snapshot,recorded_by,recorded_at
		FROM clm_contract_rectifications WHERE tenant_id=$1 AND contract_id=$2 AND rectification_id=$3`,
		tenantID, contractID, rectificationID).Scan(&fact.RectificationID, &fact.ContractRevisionID,
		&fact.AcceptanceID, &fact.DefectID, &fact.Description, &fact.EvidenceSnapshot, &fact.RecordedBy, &fact.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fulfillmentapp.RectificationFact{}, false, nil
	}
	return fact, err == nil, err
}

func (r *Repository) InsertAcceptanceRectification(ctx context.Context, item fulfillmentapp.AcceptanceRectificationInsert) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_acceptance_rectifications
		(tenant_id,contract_id,acceptance_id,contract_revision_id,delivery_id,item_id,rectification_id,defect_acceptance_id,defect_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, item.TenantID, item.ContractID, item.AcceptanceID,
		item.RevisionID, item.DeliveryID, item.ItemID, item.RectificationID, item.DefectAcceptanceID, item.DefectID)
	return err
}

func (r *Repository) InsertDefect(ctx context.Context, item fulfillmentapp.DefectInsert) (time.Time, error) {
	var recordedAt time.Time
	var priorAcceptance, priorDefect any
	if item.PriorAcceptanceID != "" {
		priorAcceptance, priorDefect = item.PriorAcceptanceID, item.PriorDefectID
	}
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_acceptance_defects
		(tenant_id,contract_id,contract_revision_id,acceptance_id,defect_id,prior_acceptance_id,prior_defect_id,description,responsible,remediation_due,failed_quantity,recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::date,$11::numeric,$12) RETURNING recorded_at`,
		item.TenantID, item.ContractID, item.ContractRevisionID, item.AcceptanceID, item.DefectID,
		priorAcceptance, priorDefect, item.Description, item.Responsible, item.RemediationDue,
		item.FailedQuantity, item.RecordedBy).Scan(&recordedAt)
	return recordedAt, err
}

func (r *Repository) LoadAcceptanceResult(ctx context.Context, tenantID, contractID, acceptanceID, revisionID string) (string, bool, error) {
	var result string
	err := r.tx.QueryRow(ctx, `SELECT result FROM clm_contract_acceptances
		WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 AND contract_revision_id=$4`,
		tenantID, contractID, acceptanceID, revisionID).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return result, err == nil, err
}

func (r *Repository) InsertRectification(ctx context.Context, item fulfillmentapp.RectificationInsert) (time.Time, error) {
	var recordedAt time.Time
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_rectifications
		(tenant_id,contract_id,contract_revision_id,rectification_id,acceptance_id,defect_id,description,evidence_snapshot,recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9) RETURNING recorded_at`,
		item.TenantID, item.ContractID, item.ContractRevisionID, item.RectificationID, item.AcceptanceID,
		item.DefectID, item.Description, string(item.EvidenceSnapshot), item.RecordedBy).Scan(&recordedAt)
	return recordedAt, err
}

func (r *Repository) ListDeliveries(ctx context.Context, tenantID, contractID string) ([]fulfillmentapp.DeliveryFact, error) {
	rows, err := r.tx.Query(ctx, `SELECT delivery_id::text,contract_revision_id::text,delivery_plan_sequence,item_id::text,
		batch_reference,quantity::text,unit,actual_at,location,description,evidence_snapshot,recorded_by,recorded_at
		FROM clm_contract_deliveries WHERE tenant_id=$1 AND contract_id=$2
		ORDER BY actual_at,delivery_id`, tenantID, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := []fulfillmentapp.DeliveryFact{}
	for rows.Next() {
		var fact fulfillmentapp.DeliveryFact
		if err := rows.Scan(&fact.DeliveryID, &fact.ContractRevisionID, &fact.DeliveryPlanSequence,
			&fact.ItemID, &fact.BatchReference, &fact.Quantity, &fact.Unit, &fact.ActualAt,
			&fact.Location, &fact.Description, &fact.EvidenceSnapshot, &fact.RecordedBy, &fact.RecordedAt); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (r *Repository) ListAcceptances(ctx context.Context, tenantID, contractID string) ([]fulfillmentapp.AcceptanceFact, error) {
	rows, err := r.tx.Query(ctx, `SELECT acceptance_id::text,contract_revision_id::text,delivery_id::text,item_id::text,
		acceptance_plan_sequence,scope_start::text,scope_end::text,accepted_quantity::text,result,
		COALESCE(reinspection_of::text,''),actual_at,standard,inspector_note,evidence_snapshot,recorded_by,recorded_at
		FROM clm_contract_acceptances WHERE tenant_id=$1 AND contract_id=$2
		ORDER BY actual_at,acceptance_id`, tenantID, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := []fulfillmentapp.AcceptanceFact{}
	for rows.Next() {
		var fact fulfillmentapp.AcceptanceFact
		if err := rows.Scan(&fact.AcceptanceID, &fact.ContractRevisionID, &fact.DeliveryID, &fact.ItemID,
			&fact.AcceptancePlanSequence, &fact.ScopeStart, &fact.ScopeEnd, &fact.AcceptedQuantity,
			&fact.Result, &fact.ReinspectionOf, &fact.ActualAt, &fact.Standard, &fact.InspectorNote,
			&fact.EvidenceSnapshot, &fact.RecordedBy, &fact.RecordedAt); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (r *Repository) ListAcceptanceRectificationIDs(ctx context.Context, tenantID, contractID, acceptanceID string) ([]string, error) {
	rows, err := r.tx.Query(ctx, `SELECT rectification_id::text FROM clm_contract_acceptance_rectifications
		WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 ORDER BY rectification_id`, tenantID, contractID, acceptanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SumAcceptedQuantity returns the original exact numeric aggregate for Payment.
func (r *Repository) SumAcceptedQuantity(ctx context.Context, tenantID, contractID, revisionID, itemID string) (string, error) {
	var quantity string
	err := r.tx.QueryRow(ctx, `SELECT COALESCE(sum(accepted_quantity),0)::text FROM clm_contract_acceptances WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4`, tenantID, contractID, revisionID, itemID).Scan(&quantity)
	return quantity, err
}
