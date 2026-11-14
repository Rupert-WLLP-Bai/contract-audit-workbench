package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/operator/app"
	"contractaudit/internal/operator/model"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type Confirm struct{ tx platformpostgres.Executor }

func NewConfirm(tx platformpostgres.Executor) *Confirm { return &Confirm{tx: tx} }
func (p *Confirm) ReadConfirmReceipt(ctx context.Context, id app.CreateIdentity, key string) (app.Receipt, bool, error) {
	var v app.Receipt
	err := p.tx.QueryRow(ctx, `SELECT request_sha256,response_status,response_body FROM service_object_mutation_receipts WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND operation=$5 AND idempotency_key=$6`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, "confirm_association", key).Scan(&v.RequestSHA, &v.Status, &v.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}
func (p *Confirm) LockHead(ctx context.Context, id app.CreateIdentity, objectID string) (model.ServiceObjectHead, bool, error) {
	var h model.ServiceObjectHead
	err := p.tx.QueryRow(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0) FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 FOR UPDATE`, id.TenantID, id.OrgID, id.CaseID, objectID).Scan(&h.ID, &h.CurrentVersion, &h.CurrentAssociationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, false, nil
	}
	return h, err == nil, err
}
func (p *Confirm) HasMapping(ctx context.Context, id app.CreateIdentity, objectID string) (bool, error) {
	var found bool
	err := p.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM operator_mapping_lineages WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4)`, id.TenantID, id.OrgID, id.CaseID, objectID).Scan(&found)
	return found, err
}
func (p *Confirm) ReadConfirmVersion(ctx context.Context, id app.CreateIdentity, objectID string, number int) (app.VersionRow, error) {
	return NewReads(p.tx).ReadCurrentVersionRaw(ctx, app.ReadIdentity{TenantID: id.TenantID, OrgID: id.OrgID, CaseID: id.CaseID}, objectID, number)
}
func (p *Confirm) AppendVersion(ctx context.Context, id app.CreateIdentity, v model.ServiceObjectVersion) error {
	aliases, err := json.Marshal(v.Aliases)
	if err != nil {
		return err
	}
	basis := v.BasisRefs
	if len(basis) == 0 {
		basis = json.RawMessage(`[]`)
	}
	_, err = p.tx.Exec(ctx, `INSERT INTO service_object_versions
 (tenant_id,org_id,case_id,service_object_id,version,object_type,display_name,business_code,aliases,state,effective_from,change_decision_id,change_decision_outcome,basis_refs,basis_summary,payload_sha256,created_by,created_at)
 VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9::jsonb,$10,$11,NULLIF($12,''),CASE WHEN $12<>'' THEN 'approved' ELSE NULL END,$13::jsonb,NULLIF($14,''),$15,$16,$17)`, id.TenantID, id.OrgID, id.CaseID, v.ServiceObjectID, v.Version, v.ObjectType, v.DisplayName, v.BusinessCode, json.RawMessage(aliases), v.State, v.EffectiveFrom.UTC().Truncate(time.Microsecond), v.ChangeDecisionID, basis, v.BasisSummary, v.PayloadSHA256, id.ActorID, v.CreatedAt.UTC().Truncate(time.Microsecond))
	return err
}
func (p *Confirm) AppendAssociation(ctx context.Context, id app.CreateIdentity, v model.ServiceObjectContractAssociation) error {
	basis := v.BasisRefs
	if len(basis) == 0 {
		basis = json.RawMessage(`[]`)
	}
	_, err := p.tx.Exec(ctx, `INSERT INTO service_object_contract_associations
 (tenant_id,org_id,case_id,service_object_id,association_version,object_version,material_id,material_version,document_id,parsed_document_id,source_run_id,source_attempt_id,object_ref,facts_canonical_json,facts_sha256,basis_refs,identity_change_decision_id,identity_change_decision_outcome,payload_sha256,confirmed_by,confirmed_at,created_at)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb,NULLIF($17,''),CASE WHEN $17<>'' THEN 'approved' ELSE NULL END,$18,$19,$20,$21)`, id.TenantID, id.OrgID, id.CaseID, v.ServiceObjectID, v.AssociationVersion, v.ObjectVersion, v.MaterialID, v.MaterialVersion, v.DocumentID, v.ParsedDocumentID, v.SourceRunID, v.SourceAttemptID, v.ObjectRef, v.FactsCanonicalJSON, v.FactsSHA256, basis, v.IdentityChangeDecisionID, v.PayloadSHA256, v.ConfirmedBy, v.ConfirmedAt.UTC().Truncate(time.Microsecond), v.CreatedAt.UTC().Truncate(time.Microsecond))
	return err
}
func (p *Confirm) AdvanceHead(ctx context.Context, id app.CreateIdentity, h model.ServiceObjectHead, expected, assoc int) (bool, error) {
	tag, err := p.tx.Exec(ctx, `UPDATE service_objects SET current_version=$1,current_association_version=$2 WHERE tenant_id=$3 AND org_id=$4 AND case_id=$5 AND service_object_id=$6 AND current_version=$7 AND COALESCE(current_association_version,0)=$8`, h.CurrentVersion, h.CurrentAssociationVersion, id.TenantID, id.OrgID, id.CaseID, h.ID, expected, assoc)
	return tag.RowsAffected() == 1, err
}
func (p *Confirm) AppendConfirmReceipt(ctx context.Context, id app.CreateIdentity, key, hash string, body []byte, objectID string, now time.Time) error {
	_, err := p.tx.Exec(ctx, `INSERT INTO service_object_mutation_receipts (tenant_id,org_id,case_id,actor_id,operation,idempotency_key,request_sha256,response_status,response_body,entity_id,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, "confirm_association", key, hash, 201, body, objectID, now.UTC().Truncate(time.Microsecond))
	return err
}
