// File metadata (manifest mirror):
// Purpose: Reads exact Operator binding receipts and appends initial bindings and lineages through a non-owning transaction executor.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"
	"errors"

	"contractaudit/internal/operator/app"
	"contractaudit/internal/operator/model"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type Binding struct {
	*Reads
	tx platformpostgres.Executor
}

func NewBinding(tx platformpostgres.Executor) *Binding { return &Binding{Reads: NewReads(tx), tx: tx} }
func (p *Binding) BindingByKey(ctx context.Context, id app.CreateIdentity, key string) (app.MappingBindingRow, bool, error) {
	var row app.MappingBindingRow
	v := &row.Payload
	err := p.tx.QueryRow(ctx, `SELECT case_id,binding_id,binding_version,status,contract_object,external_object,source_system,confirmed_by,confirmed_at,effective_from,effective_until,created_at,request_sha256,COALESCE(mapping_id,''),COALESCE(mapping_version,0),COALESCE(service_object_id,''),COALESCE(object_version,0),COALESCE(association_version,0),response_body FROM operator_object_bindings WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, key).Scan(&v.CaseID, &v.BindingID, &v.Version, &v.Status, &v.ContractObject, &v.ExternalObject, &v.SourceSystem, &v.ConfirmedBy, &v.ConfirmedAt, &v.EffectiveFrom, &v.EffectiveUntil, &v.CreatedAt, &row.RequestSHA256, &v.MappingID, &v.MappingVersion, &v.ServiceObjectID, &v.ObjectVersion, &v.AssociationVersion, &row.ResponseBody)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, false, nil
	}
	return row, err == nil, err
}
func (p *Binding) AppendInitialBinding(ctx context.Context, id app.CreateIdentity, key string, record model.OperatorObjectBindingRecord, contract, external, body []byte) error {
	v := record.Value
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_object_bindings
 (tenant_id,org_id,case_id,binding_id,binding_version,status,contract_object,external_object,connector_id,external_object_id,source_system,confirmed_by,confirmed_at,effective_from,effective_until,actor_id,idempotency_key,request_sha256,created_at,mapping_id,mapping_version,service_object_id,object_version,association_version,response_status,response_body)
 VALUES ($1,$2,$3,$4,$4,'confirmed',$5,$6,$7,$8,'',$9,$10,$11,$12,$9,$13,$14,$10,$15,1,$16,$17,$18,201,$19)`, id.TenantID, id.OrgID, id.CaseID, v.BindingID, contract, external, v.ExternalObject.ConnectorID, v.ExternalObject.ExternalObjectID, id.ActorID, v.CreatedAt, v.EffectiveFrom, v.EffectiveUntil, key, record.RequestSHA256, v.MappingID, v.ServiceObjectID, v.ObjectVersion, v.AssociationVersion, body)
	return err
}
func (p *Binding) AppendInitialLineage(ctx context.Context, id app.CreateIdentity, v model.OperatorObjectBinding) error {
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_mapping_lineages (tenant_id,org_id,case_id,mapping_id,service_object_id,current_version,current_binding_version,created_by,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,1,$6,$7,$8,$8)`, id.TenantID, id.OrgID, id.CaseID, v.MappingID, v.ServiceObjectID, v.BindingID, id.ActorID, v.CreatedAt)
	return err
}
