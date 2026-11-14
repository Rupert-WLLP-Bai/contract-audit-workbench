package postgres

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/operator/app"
	"contractaudit/internal/operator/model"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type Collection struct{ tx platformpostgres.Executor }

func NewCollection(tx platformpostgres.Executor) *Collection { return &Collection{tx: tx} }
func (p *Collection) LockClaim(ctx context.Context, id model.CollectionIdentity) (model.CollectionClaim, bool, error) {
	var c model.CollectionClaim
	err := p.tx.QueryRow(ctx, `SELECT collection_id,request_sha256,generation,lease_owner,lease_until,status,result_snapshot_set_id FROM operator_external_collection_claims WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5 FOR UPDATE`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, id.Key).Scan(&c.CollectionID, &c.RequestSHA256, &c.Generation, &c.Owner, &c.LeaseUntil, &c.Status, &c.ResultID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}
func (p *Collection) SnapshotByKey(ctx context.Context, id model.CollectionIdentity) (model.SnapshotRecord, bool, error) {
	var r model.SnapshotRecord
	v := &r.Value
	err := p.tx.QueryRow(ctx, `SELECT case_id,snapshot_set_id,binding_version,connector_id,external_object_id,source_system,business_at,status,reason_code,collected_by,collected_at,request_sha256 FROM operator_external_snapshot_sets WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, id.Key).Scan(&v.CaseID, &v.SnapshotSetID, &v.BindingVersion, &v.ConnectorID, &v.ExternalObjectID, &v.SourceSystem, &v.BusinessAt, &v.Status, &v.ReasonCode, &v.CollectedBy, &v.CollectedAt, &r.RequestSHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}
func (p *Collection) SnapshotByID(ctx context.Context, caseID, setID string) (model.OperatorSnapshotSet, error) {
	var v model.OperatorSnapshotSet
	err := p.tx.QueryRow(ctx, `SELECT case_id,snapshot_set_id,binding_version,connector_id,external_object_id,source_system,business_at,status,reason_code,collected_by,collected_at FROM operator_external_snapshot_sets WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND snapshot_set_id=$2`, caseID, setID).Scan(&v.CaseID, &v.SnapshotSetID, &v.BindingVersion, &v.ConnectorID, &v.ExternalObjectID, &v.SourceSystem, &v.BusinessAt, &v.Status, &v.ReasonCode, &v.CollectedBy, &v.CollectedAt)
	return v, err
}

type collectionRows struct{ pgx.Rows }

func (r collectionRows) ScanRaw() ([]byte, error) { var b []byte; err := r.Scan(&b); return b, err }
func (p *Collection) Observations(ctx context.Context, caseID, setID string) (app.ObservationCursor, error) {
	rows, err := p.tx.Query(ctx, `SELECT observation_json FROM operator_external_observations WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND snapshot_set_id=$2 ORDER BY snapshot_id`, caseID, setID)
	if err != nil {
		return nil, err
	}
	return collectionRows{rows}, nil
}
func (p *Collection) InsertLegacyClaim(ctx context.Context, t model.CollectionLeaseToken, setID string, body []byte) error {
	id := t.Identity
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_external_collection_claims (tenant_id,org_id,case_id,actor_id,idempotency_key,collection_id,request_sha256,generation,status,result_snapshot_set_id,response_status,response_body) VALUES ($1,$2,$3,$4,$5,$6,$7,1,'completed',$8,201,$9)`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, id.Key, t.CollectionID, t.RequestSHA256, setID, body)
	return err
}
func (p *Collection) InsertClaim(ctx context.Context, t model.CollectionLeaseToken) error {
	id := t.Identity
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_external_collection_claims (tenant_id,org_id,case_id,actor_id,idempotency_key,collection_id,request_sha256,generation,lease_owner,lease_until,status) VALUES ($1,$2,$3,$4,$5,$6,$7,1,$8,clock_timestamp()+interval '5 minutes','in_progress')`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, id.Key, t.CollectionID, t.RequestSHA256, t.Owner)
	return err
}
func (p *Collection) RenewClaim(ctx context.Context, t model.CollectionLeaseToken) error {
	id := t.Identity
	_, err := p.tx.Exec(ctx, `UPDATE operator_external_collection_claims SET generation=$6,lease_owner=$7,lease_until=clock_timestamp()+interval '5 minutes',status='in_progress',updated_at=clock_timestamp() WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, id.Key, t.Generation, t.Owner)
	return err
}

func (p *Collection) LockCompletion(ctx context.Context, t model.CollectionLeaseToken) (app.CompletionClaim, error) {
	id := t.Identity
	var c app.CompletionClaim
	err := p.tx.QueryRow(ctx, `SELECT generation,lease_owner,status,COALESCE(lease_until > clock_timestamp(),false) FROM operator_external_collection_claims WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5 FOR UPDATE`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, id.Key).Scan(&c.Generation, &c.Owner, &c.Status, &c.LeaseValid)
	return c, err
}
func (p *Collection) InsertSnapshot(ctx context.Context, t model.CollectionLeaseToken, v model.OperatorSnapshotSet, setSHA string) error {
	id := t.Identity
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_external_snapshot_sets (tenant_id,org_id,case_id,snapshot_set_id,binding_version,connector_id,external_object_id,source_system,business_at,status,reason_code,collected_by,collected_at,observation_count,set_sha256,actor_id,idempotency_key,request_sha256,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,1,$14,$12,$15,$16,$13)`, id.TenantID, id.OrgID, id.CaseID, v.SnapshotSetID, v.BindingVersion, v.ConnectorID, v.ExternalObjectID, v.SourceSystem, v.BusinessAt, v.Status, v.ReasonCode, id.ActorID, v.CollectedAt, setSHA, id.Key, t.RequestSHA256)
	return err
}
func (p *Collection) InsertObservation(ctx context.Context, t model.CollectionLeaseToken, setID string, o app.PreparedObservation, now time.Time) error {
	id := t.Identity
	v := o.Value
	var recordVersion, payloadRef, sourceUpdated any
	if v.RecordVersion != nil {
		recordVersion = *v.RecordVersion
	}
	if v.PayloadRef != nil {
		payloadRef = *v.PayloadRef
	}
	if v.SourceUpdatedAt != nil {
		sourceUpdated = *v.SourceUpdatedAt
	}
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_external_observations (tenant_id,org_id,case_id,snapshot_set_id,snapshot_id,record_id,record_version,observed_at,source_updated_at,effective_from,effective_until,availability,reason_code,payload_ref,fields,data_class,observation_json,observation_sha256,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, id.TenantID, id.OrgID, id.CaseID, setID, v.SnapshotID, v.RecordID, recordVersion, v.ObservedAt, sourceUpdated, v.EffectiveFrom, v.EffectiveUntil, string(v.Availability), v.ReasonCode, payloadRef, o.Fields, v.DataClass, o.JSON, o.SHA, now)
	return err
}
func (p *Collection) CompleteClaim(ctx context.Context, t model.CollectionLeaseToken, setID string, body []byte, now time.Time) (bool, error) {
	id := t.Identity
	tag, err := p.tx.Exec(ctx, `UPDATE operator_external_collection_claims SET status='completed',lease_owner=NULL,lease_until=NULL,result_snapshot_set_id=$6,response_status=201,response_body=$7,updated_at=$8 WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5 AND generation=$9 AND lease_owner=$10 AND status='in_progress' AND lease_until>clock_timestamp()`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, id.Key, setID, body, now, t.Generation, t.Owner)
	return tag.RowsAffected() == 1, err
}
func (p *Collection) ReleaseClaim(ctx context.Context, t model.CollectionLeaseToken) error {
	id := t.Identity
	_, err := p.tx.Exec(ctx, `UPDATE operator_external_collection_claims SET status='retryable',lease_owner=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5 AND generation=$6 AND lease_owner=$7 AND status='in_progress'`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, id.Key, t.Generation, t.Owner)
	return err
}

func (p *Collection) RawBinding(ctx context.Context, caseID, version string) (model.OperatorObjectBinding, error) {
	var v model.OperatorObjectBinding
	var sha string
	err := p.tx.QueryRow(ctx, `SELECT case_id,binding_id,binding_version,status,contract_object,external_object,source_system,confirmed_by,confirmed_at,effective_from,effective_until,created_at,request_sha256,COALESCE(mapping_id,''),COALESCE(mapping_version,0),COALESCE(service_object_id,''),COALESCE(object_version,0),COALESCE(association_version,0) FROM operator_object_bindings WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND binding_version=$2`, caseID, version).Scan(&v.CaseID, &v.BindingID, &v.Version, &v.Status, &v.ContractObject, &v.ExternalObject, &v.SourceSystem, &v.ConfirmedBy, &v.ConfirmedAt, &v.EffectiveFrom, &v.EffectiveUntil, &v.CreatedAt, &sha, &v.MappingID, &v.MappingVersion, &v.ServiceObjectID, &v.ObjectVersion, &v.AssociationVersion)
	return v, err
}

type snapshotRows struct{ pgx.Rows }

func (r snapshotRows) ScanSnapshot() (model.OperatorSnapshotSet, error) {
	var v model.OperatorSnapshotSet
	err := r.Scan(&v.CaseID, &v.SnapshotSetID, &v.BindingVersion, &v.ConnectorID, &v.ExternalObjectID, &v.SourceSystem, &v.BusinessAt, &v.Status, &v.ReasonCode, &v.CollectedBy, &v.CollectedAt)
	return v, err
}
func (p *Collection) ListSnapshots(ctx context.Context, caseID string) (app.SnapshotCursor, error) {
	rows, err := p.tx.Query(ctx, `SELECT case_id,snapshot_set_id,binding_version,connector_id,external_object_id,source_system,business_at,status,reason_code,collected_by,collected_at FROM operator_external_snapshot_sets WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 ORDER BY created_at DESC, snapshot_set_id`, caseID)
	if err != nil {
		return nil, err
	}
	return snapshotRows{rows}, nil
}
func (p *Collection) ReadJob(ctx context.Context, id model.CollectionIdentity, selector string, byKey bool) (app.CollectionJobRow, error) {
	var row app.CollectionJobRow
	var query string
	args := []any{id.TenantID, id.OrgID, id.CaseID, selector}
	if byKey {
		query = `SELECT collection_id,status,lease_until,result_snapshot_set_id,COALESCE(lease_until <= clock_timestamp(),false) FROM operator_external_collection_claims WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`
		args = append(args[:3], id.ActorID, selector)
	} else {
		query = `SELECT collection_id,status,lease_until,result_snapshot_set_id,COALESCE(lease_until <= clock_timestamp(),false) FROM operator_external_collection_claims WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND collection_id=$4`
	}
	err := p.tx.QueryRow(ctx, query, args...).Scan(&row.Job.CollectionID, &row.Job.State, &row.LeaseUntil, &row.ResultID, &row.Expired)
	return row, err
}
