// File metadata (manifest mirror):
// Purpose: Reads Operator-owned service-object and exact mapping rows with separately preserved response bytes and stored request identities.
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

// Reads uses only the caller-owned transaction and Operator-owned rows.
type Reads struct{ tx platformpostgres.Executor }

func NewReads(tx platformpostgres.Executor) *Reads { return &Reads{tx: tx} }

func (p *Reads) ReadHead(ctx context.Context, id app.ReadIdentity, objectID string) (head model.ServiceObjectHead, found bool, err error) {
	err = p.tx.QueryRow(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0)
		FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4`,
		id.TenantID, id.OrgID, id.CaseID, objectID).Scan(&head.ID, &head.CurrentVersion, &head.CurrentAssociationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ServiceObjectHead{}, false, nil
	}
	return head, err == nil, err
}

func (p *Reads) ListHeads(ctx context.Context, id app.ReadIdentity, after string, limit int) ([]model.ServiceObjectHead, error) {
	rows, err := p.tx.Query(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0)
		FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id>$4
		ORDER BY service_object_id LIMIT $5`, id.TenantID, id.OrgID, id.CaseID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	heads := make([]model.ServiceObjectHead, 0)
	for rows.Next() {
		var head model.ServiceObjectHead
		if err := rows.Scan(&head.ID, &head.CurrentVersion, &head.CurrentAssociationVersion); err != nil {
			rows.Close()
			return nil, err
		}
		heads = append(heads, head)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	return heads, nil
}

func (p *Reads) ReadCurrentVersionRaw(ctx context.Context, id app.ReadIdentity, objectID string, number int) (app.VersionRow, error) {
	var row app.VersionRow
	err := p.tx.QueryRow(ctx, `SELECT service_object_id,version,object_type,display_name,COALESCE(business_code,''),aliases,state,effective_from,COALESCE(change_decision_id,''),basis_refs,COALESCE(basis_summary,''),payload_sha256,created_by,created_at
		FROM service_object_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND version=$5`,
		id.TenantID, id.OrgID, id.CaseID, objectID, number).Scan(&row.ServiceObjectID, &row.Version, &row.ObjectType, &row.DisplayName,
		&row.BusinessCode, &row.Aliases, &row.State, &row.EffectiveFrom, &row.ChangeDecisionID, &row.BasisRefs, &row.BasisSummary,
		&row.PayloadSHA256, &row.CreatedBy, &row.CreatedAt)
	return row, err
}

func (p *Reads) ReadVersionRaw(ctx context.Context, id app.ReadIdentity, objectID string, number int) (app.VersionRow, bool, error) {
	row, err := p.ReadCurrentVersionRaw(ctx, id, objectID, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.VersionRow{}, false, nil
	}
	return row, err == nil, err
}

func (p *Reads) ListVersionNumbers(ctx context.Context, id app.ReadIdentity, objectID string, before, limit int) ([]int, error) {
	rows, err := p.tx.Query(ctx, `SELECT version FROM service_object_versions
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND version<$5
		ORDER BY version DESC LIMIT $6`, id.TenantID, id.OrgID, id.CaseID, objectID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]int, 0, limit)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return nil, err
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	return versions, nil
}

func (p *Reads) StreamVersionRows(ctx context.Context, id app.ReadIdentity, objectID string) (app.VersionRows, error) {
	rows, err := p.tx.Query(ctx, `SELECT service_object_id,version,object_type,display_name,COALESCE(business_code,''),aliases,state,effective_from,COALESCE(change_decision_id,''),basis_refs,COALESCE(basis_summary,''),payload_sha256,created_by,created_at
		FROM service_object_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 ORDER BY version`,
		id.TenantID, id.OrgID, id.CaseID, objectID)
	if err != nil {
		return nil, err
	}
	return versionRows{rows: rows}, nil
}

func (p *Reads) ReadAssociationRaw(ctx context.Context, id app.ReadIdentity, objectID string, number int) (app.AssociationRow, bool, error) {
	var row app.AssociationRow
	err := p.tx.QueryRow(ctx, `SELECT service_object_id,association_version,object_version,material_id,material_version,document_id,parsed_document_id,source_run_id,source_attempt_id,object_ref,facts_canonical_json,facts_sha256,basis_refs,COALESCE(identity_change_decision_id,''),payload_sha256,confirmed_by,confirmed_at,created_at
		FROM service_object_contract_associations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND association_version=$5`,
		id.TenantID, id.OrgID, id.CaseID, objectID, number).Scan(&row.ServiceObjectID, &row.AssociationVersion, &row.ObjectVersion,
		&row.MaterialID, &row.MaterialVersion, &row.DocumentID, &row.ParsedDocumentID, &row.SourceRunID, &row.SourceAttemptID,
		&row.ObjectRef, &row.FactsCanonicalJSON, &row.FactsSHA256, &row.BasisRefs, &row.IdentityChangeDecisionID,
		&row.PayloadSHA256, &row.ConfirmedBy, &row.ConfirmedAt, &row.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.AssociationRow{}, false, nil
	}
	return row, err == nil, err
}

func (p *Reads) ListAssociationNumbers(ctx context.Context, id app.ReadIdentity, objectID string, before, limit int) ([]int, error) {
	rows, err := p.tx.Query(ctx, `SELECT association_version FROM service_object_contract_associations
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND association_version<$5
		ORDER BY association_version DESC LIMIT $6`, id.TenantID, id.OrgID, id.CaseID, objectID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]int, 0, limit)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return nil, err
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	return versions, nil
}

func (p *Reads) StreamAssociationRows(ctx context.Context, id app.ReadIdentity, objectID string) (app.AssociationRows, error) {
	rows, err := p.tx.Query(ctx, `SELECT service_object_id,association_version,object_version,material_id,material_version,document_id,parsed_document_id,source_run_id,source_attempt_id,object_ref,facts_canonical_json,facts_sha256,basis_refs,COALESCE(identity_change_decision_id,''),payload_sha256,confirmed_by,confirmed_at,created_at
		FROM service_object_contract_associations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 ORDER BY association_version`,
		id.TenantID, id.OrgID, id.CaseID, objectID)
	if err != nil {
		return nil, err
	}
	return associationRows{rows: rows}, nil
}

func (p *Reads) FindMappingID(ctx context.Context, id app.ReadIdentity, objectID string) (string, bool, error) {
	var mappingID string
	err := p.tx.QueryRow(ctx, `SELECT mapping_id FROM operator_mapping_lineages
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4`,
		id.TenantID, id.OrgID, id.CaseID, objectID).Scan(&mappingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return mappingID, err == nil, err
}

func (p *Reads) ReadMappingLineage(ctx context.Context, id app.ReadIdentity, mappingID string) (app.MappingLineageRow, bool, error) {
	var row app.MappingLineageRow
	err := p.tx.QueryRow(ctx, `SELECT case_id,mapping_id,service_object_id,current_version,current_binding_version,created_by,created_at,updated_at
		FROM operator_mapping_lineages WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND mapping_id=$4`,
		id.TenantID, id.OrgID, id.CaseID, mappingID).Scan(&row.CaseID, &row.MappingID, &row.ServiceObjectID, &row.CurrentVersion,
		&row.CurrentBindingVersion, &row.CreatedBy, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.MappingLineageRow{}, false, nil
	}
	return row, err == nil, err
}

func (p *Reads) ReadMappingBinding(ctx context.Context, id app.ReadIdentity, bindingVersion string) (app.MappingBindingRow, bool, error) {
	var row app.MappingBindingRow
	err := p.tx.QueryRow(ctx, `SELECT case_id,binding_id,binding_version,status,contract_object,external_object,
		source_system,confirmed_by,confirmed_at,effective_from,effective_until,created_at,request_sha256,
		COALESCE(mapping_id,''),COALESCE(mapping_version,0),COALESCE(service_object_id,''),COALESCE(object_version,0),COALESCE(association_version,0),response_body
		FROM operator_object_bindings WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND binding_version=$4`,
		id.TenantID, id.OrgID, id.CaseID, bindingVersion).Scan(&row.Payload.CaseID, &row.Payload.BindingID, &row.Payload.Version,
		&row.Payload.Status, &row.Payload.ContractObject, &row.Payload.ExternalObject, &row.Payload.SourceSystem,
		&row.Payload.ConfirmedBy, &row.Payload.ConfirmedAt, &row.Payload.EffectiveFrom, &row.Payload.EffectiveUntil,
		&row.Payload.CreatedAt, &row.RequestSHA256, &row.Payload.MappingID, &row.Payload.MappingVersion,
		&row.Payload.ServiceObjectID, &row.Payload.ObjectVersion, &row.Payload.AssociationVersion, &row.ResponseBody)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.MappingBindingRow{}, false, nil
	}
	return row, err == nil, err
}

type versionRows struct{ rows pgx.Rows }

func (r versionRows) Next() bool { return r.rows.Next() }
func (r versionRows) Err() error { return r.rows.Err() }
func (r versionRows) Close()     { r.rows.Close() }
func (r versionRows) Read() (app.VersionRow, error) {
	var row app.VersionRow
	err := r.rows.Scan(&row.ServiceObjectID, &row.Version, &row.ObjectType, &row.DisplayName, &row.BusinessCode,
		&row.Aliases, &row.State, &row.EffectiveFrom, &row.ChangeDecisionID, &row.BasisRefs, &row.BasisSummary,
		&row.PayloadSHA256, &row.CreatedBy, &row.CreatedAt)
	return row, err
}

type associationRows struct{ rows pgx.Rows }

func (r associationRows) Next() bool { return r.rows.Next() }
func (r associationRows) Err() error { return r.rows.Err() }
func (r associationRows) Close()     { r.rows.Close() }
func (r associationRows) Read() (app.AssociationRow, error) {
	var row app.AssociationRow
	err := r.rows.Scan(&row.ServiceObjectID, &row.AssociationVersion, &row.ObjectVersion, &row.MaterialID, &row.MaterialVersion,
		&row.DocumentID, &row.ParsedDocumentID, &row.SourceRunID, &row.SourceAttemptID, &row.ObjectRef, &row.FactsCanonicalJSON,
		&row.FactsSHA256, &row.BasisRefs, &row.IdentityChangeDecisionID, &row.PayloadSHA256, &row.ConfirmedBy, &row.ConfirmedAt, &row.CreatedAt)
	return row, err
}

var _ app.ReadRepository = (*Reads)(nil)

// ListAllContextHeads preserves the acceptance query's complete read before its limit check.
func (p *Reads) ListAllContextHeads(ctx context.Context, id app.ReadIdentity) ([]model.ServiceObjectHead, error) {
	rows, err := p.tx.Query(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0)
  FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 ORDER BY service_object_id`, id.TenantID, id.OrgID, id.CaseID)
	if err != nil {
		return nil, err
	}
	var heads []model.ServiceObjectHead
	for rows.Next() {
		var h model.ServiceObjectHead
		if err := rows.Scan(&h.ID, &h.CurrentVersion, &h.CurrentAssociationVersion); err != nil {
			rows.Close()
			return nil, err
		}
		heads = append(heads, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return heads, nil
}
