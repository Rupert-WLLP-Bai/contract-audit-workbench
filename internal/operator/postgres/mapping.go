// File metadata (manifest mirror):
// Purpose: Reads Operator change proposals or decisions and appends proposal receipts through the original caller-bound executor.
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
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/operator/app"
	"contractaudit/internal/operator/model"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type Mapping struct {
	*Reads
	tx platformpostgres.Executor
}

func NewMapping(tx platformpostgres.Executor) *Mapping { return &Mapping{Reads: NewReads(tx), tx: tx} }
func (p *Mapping) ProposalReceipt(ctx context.Context, id app.CreateIdentity, key string) ([]byte, string, bool, error) {
	var body []byte
	var hash string
	err := p.tx.QueryRow(ctx, `SELECT response_body,request_sha256 FROM operator_mapping_change_requests WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, key).Scan(&body, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", false, nil
	}
	return body, hash, err == nil, err
}
func (p *Mapping) AppendProposal(ctx context.Context, id app.CreateIdentity, key string, record model.OperatorMappingChangeRecord, patchJSON, afterJSON []byte, patchSHA string, response []byte) error {
	v := record.Value
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_mapping_change_requests
 (tenant_id,org_id,case_id,request_id,mapping_id,service_object_id,change_kind,expected_mapping_version,expected_binding_version,expected_object_version,expected_association_version,before_json,before_sha256,after_json,after_sha256,patch_json,patch_sha256,basis_refs,impact_sha256,proposer_user_id,proposer_issuer,proposer_subject,actor_id,idempotency_key,request_sha256,response_status,response_body,created_at)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14::jsonb,$15,$16::jsonb,$17,$18::jsonb,$19,$20,$21,$22,$20,$23,$24,201,$25,$26)`, id.TenantID, id.OrgID, id.CaseID, v.RequestID, v.MappingID, v.ServiceObjectID, v.Kind, v.ExpectedMappingVersion, v.ExpectedBindingVersion, v.ExpectedObjectVersion, v.ExpectedAssociationVersion, []byte(v.Before), v.BeforeSHA256, afterJSON, v.AfterSHA256, patchJSON, patchSHA, v.BasisRefs, v.ImpactSHA256, v.ProposedBy.UserID, v.ProposedBy.Issuer, v.ProposedBy.Subject, key, record.RequestSHA256, response, v.CreatedAt)
	return err
}
func (p *Mapping) RequestRaw(ctx context.Context, id app.ReadIdentity, requestID string) (app.MappingRequestRow, bool, error) {
	var row app.MappingRequestRow
	v := &row.Record.Value
	err := p.tx.QueryRow(ctx, `SELECT request_id,case_id,mapping_id,service_object_id,change_kind,expected_mapping_version,expected_binding_version,expected_object_version,expected_association_version,before_json,before_sha256,after_json,after_sha256,patch_json,patch_sha256,basis_refs,impact_sha256,proposer_user_id,proposer_issuer,proposer_subject,request_sha256,created_at FROM operator_mapping_change_requests WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`, id.TenantID, id.OrgID, id.CaseID, requestID).Scan(&v.RequestID, &v.CaseID, &v.MappingID, &v.ServiceObjectID, &v.Kind, &v.ExpectedMappingVersion, &v.ExpectedBindingVersion, &v.ExpectedObjectVersion, &v.ExpectedAssociationVersion, &row.Before, &v.BeforeSHA256, &row.After, &v.AfterSHA256, &row.Patch, &v.PatchSHA256, &row.Basis, &v.ImpactSHA256, &v.ProposedBy.UserID, &v.ProposedBy.Issuer, &v.ProposedBy.Subject, &row.Record.RequestSHA256, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, false, nil
	}
	return row, err == nil, err
}
func (p *Mapping) DecisionState(ctx context.Context, id app.ReadIdentity, requestID string) (string, bool, error) {
	var state string
	err := p.tx.QueryRow(ctx, `SELECT decision FROM operator_mapping_change_decisions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`, id.TenantID, id.OrgID, id.CaseID, requestID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return state, err == nil, err
}
func (p *Mapping) DecisionRaw(ctx context.Context, id app.ReadIdentity, requestID string) (model.OperatorMappingChangeDecision, bool, error) {
	var v model.OperatorMappingChangeDecision
	err := p.tx.QueryRow(ctx, `SELECT decision_id,request_id,decision,reason,decider_user_id,decider_issuer,decider_subject,created_at FROM operator_mapping_change_decisions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`, id.TenantID, id.OrgID, id.CaseID, requestID).Scan(&v.DecisionID, &v.RequestID, &v.Decision, &v.Reason, &v.DecidedBy.UserID, &v.DecidedBy.Issuer, &v.DecidedBy.Subject, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}

func (p *Mapping) DecisionReceipt(ctx context.Context, id app.CreateIdentity, key string) ([]byte, string, bool, error) {
	var body []byte
	var hash string
	err := p.tx.QueryRow(ctx, `SELECT response_body,decision_request_sha256 FROM operator_mapping_change_decisions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, key).Scan(&body, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", false, nil
	}
	return body, hash, err == nil, err
}
func (p *Mapping) AppendDecision(ctx context.Context, id app.CreateIdentity, w app.DecisionWrite) error {
	r := w.Request.Value
	c := w.CAS
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_mapping_change_decisions (tenant_id,org_id,case_id,request_id,decision_id,mapping_id,service_object_id,expected_mapping_version,expected_binding_version,expected_object_version,expected_association_version,decision,reason,decider_user_id,decider_issuer,decider_subject,request_sha256,actor_id,idempotency_key,decision_request_sha256,response_status,response_body,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$14,$18,$19,201,$20,$21)`, id.TenantID, id.OrgID, id.CaseID, r.RequestID, w.DecisionID, r.MappingID, r.ServiceObjectID, c.ExpectedMappingVersion, c.ExpectedBindingVersion, c.ExpectedObjectVersion, c.ExpectedAssociationVersion, w.Status, w.Reason, w.Actor.UserID, w.Actor.Issuer, w.Actor.Subject, w.Request.RequestSHA256, w.Key, w.DecisionSHA, w.Body, w.Now)
	return err
}
func (p *Mapping) AppendVersion(ctx context.Context, id app.CreateIdentity, v model.ServiceObjectVersion) error {
	return NewConfirm(p.tx).AppendVersion(ctx, id, v)
}
func (p *Mapping) AppendAssociation(ctx context.Context, id app.CreateIdentity, v model.ServiceObjectContractAssociation) error {
	return NewConfirm(p.tx).AppendAssociation(ctx, id, v)
}
func (p *Mapping) AdvanceObjectBoth(ctx context.Context, id app.CreateIdentity, h model.ServiceObjectHead, expected, assoc int) (bool, error) {
	tag, err := p.tx.Exec(ctx, `UPDATE service_objects SET current_version=$1,current_association_version=$2 WHERE tenant_id=$3 AND org_id=$4 AND case_id=$5 AND service_object_id=$6 AND current_version=$7 AND current_association_version=$8`, h.CurrentVersion, h.CurrentAssociationVersion, id.TenantID, id.OrgID, id.CaseID, h.ID, expected, assoc)
	return tag.RowsAffected() == 1, err
}
func (p *Mapping) AppendApprovedBinding(ctx context.Context, id app.CreateIdentity, w app.ApprovedBindingWrite) error {
	v := w.Binding
	contract, _ := json.Marshal(v.ContractObject)
	external, _ := json.Marshal(v.ExternalObject)
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_object_bindings (tenant_id,org_id,case_id,binding_id,binding_version,status,contract_object,external_object,connector_id,external_object_id,source_system,confirmed_by,confirmed_at,effective_from,effective_until,actor_id,idempotency_key,request_sha256,created_at,mapping_id,mapping_version,service_object_id,object_version,association_version,mapping_change_decision_id,mapping_change_decision,response_status,response_body) VALUES ($1,$2,$3,$4,$4,$5,$6::jsonb,$7::jsonb,$8,$9,$10,$11,$12,$13,$14,$11,$15,$16,$12,$17,$18,$19,$20,$21,$22,'approved',201,$23)`, id.TenantID, id.OrgID, id.CaseID, v.BindingID, v.Status, contract, external, v.ExternalObject.ConnectorID, v.ExternalObject.ExternalObjectID, v.SourceSystem, id.ActorID, w.Now, v.EffectiveFrom, v.EffectiveUntil, "mapping-change:"+w.DecisionID, w.SHA, v.MappingID, v.MappingVersion, v.ServiceObjectID, v.ObjectVersion, v.AssociationVersion, w.DecisionID, w.Body)
	return err
}
func (p *Mapping) AdvanceAssociation(ctx context.Context, id app.CreateIdentity, objectID string, next, expected, assoc int) (bool, error) {
	tag, err := p.tx.Exec(ctx, `UPDATE service_objects SET current_association_version=$1 WHERE tenant_id=$2 AND org_id=$3 AND case_id=$4 AND service_object_id=$5 AND current_version=$6 AND current_association_version=$7`, next, id.TenantID, id.OrgID, id.CaseID, objectID, expected, assoc)
	return tag.RowsAffected() == 1, err
}
func (p *Mapping) AdvanceLineage(ctx context.Context, id app.CreateIdentity, mappingID string, next int64, bindingID string, expected int64, expectedBinding string, now time.Time) (bool, error) {
	tag, err := p.tx.Exec(ctx, `UPDATE operator_mapping_lineages SET current_version=$1,current_binding_version=$2,updated_at=$3 WHERE tenant_id=$4 AND org_id=$5 AND case_id=$6 AND mapping_id=$7 AND current_version=$8 AND current_binding_version=$9`, next, bindingID, now, id.TenantID, id.OrgID, id.CaseID, mappingID, expected, expectedBinding)
	return tag.RowsAffected() == 1, err
}
