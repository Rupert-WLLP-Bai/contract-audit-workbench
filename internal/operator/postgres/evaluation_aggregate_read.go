package postgres

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/operator/app"
	"contractaudit/internal/operator/model"
	"github.com/jackc/pgx/v5"
)

type mappingPinRows struct{ pgx.Rows }

func (r mappingPinRows) ScanPin() (app.MappingPin, error) {
	var v app.MappingPin
	err := r.Scan(&v.BindingVersion, &v.MappingID, &v.MappingVersion)
	return v, err
}
func (p *Evaluation) MappingPins(ctx context.Context, id app.ReadIdentity, objectID string, at, cutoff time.Time) (app.MappingPinCursor, error) {
	rows, err := p.tx.Query(ctx, `SELECT binding_version,mapping_id,mapping_version FROM ( SELECT DISTINCT ON (mapping_id) binding_version,mapping_id,mapping_version,status FROM operator_object_bindings WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND effective_from<=$5 AND $5<effective_until AND created_at<=$6 AND confirmed_at<=$6 AND mapping_id IS NOT NULL ORDER BY mapping_id,mapping_version DESC ) selected WHERE status='confirmed' ORDER BY mapping_id`, id.TenantID, id.OrgID, id.CaseID, objectID, at, cutoff)
	if err != nil {
		return nil, err
	}
	return mappingPinRows{rows}, nil
}
func (p *Evaluation) SnapshotIDAt(ctx context.Context, caseID, bindingVersion string, businessAt, cutoff time.Time) (string, bool, error) {
	var id string
	err := p.tx.QueryRow(ctx, `SELECT snapshot_set_id FROM operator_external_snapshot_sets WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND binding_version=$2 AND business_at=$3 AND collected_at<=$4 AND created_at<=$4 ORDER BY collected_at DESC,snapshot_set_id LIMIT 1`, caseID, bindingVersion, businessAt, cutoff).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

type objectHeadRows struct{ pgx.Rows }

func (r objectHeadRows) ScanHead() (model.ServiceObjectHead, error) {
	var h model.ServiceObjectHead
	err := r.Scan(&h.ID, &h.CurrentVersion, &h.CurrentAssociationVersion)
	return h, err
}
func (p *Evaluation) ObjectHeads(ctx context.Context, id app.ReadIdentity) (app.ObjectHeadCursor, error) {
	rows, err := p.tx.Query(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0) FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 ORDER BY service_object_id`, id.TenantID, id.OrgID, id.CaseID)
	if err != nil {
		return nil, err
	}
	return objectHeadRows{rows}, nil
}
func (p *Evaluation) VersionAt(ctx context.Context, id app.ReadIdentity, objectID string, at, cutoff time.Time) (int, bool, error) {
	var n int
	err := p.tx.QueryRow(ctx, `SELECT version FROM service_object_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND effective_from<=$5 ORDER BY (created_at<=$6) DESC,effective_from DESC,version DESC LIMIT 1`, id.TenantID, id.OrgID, id.CaseID, objectID, at, cutoff).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return n, err == nil, err
}
func (p *Evaluation) AssociationAt(ctx context.Context, id app.ReadIdentity, objectID string, version int, cutoff time.Time) (int, bool, error) {
	var n int
	err := p.tx.QueryRow(ctx, `SELECT association_version FROM service_object_contract_associations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND object_version=$5 ORDER BY (created_at<=$6 AND confirmed_at<=$6) DESC,association_version DESC LIMIT 1`, id.TenantID, id.OrgID, id.CaseID, objectID, version, cutoff).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return n, err == nil, err
}
