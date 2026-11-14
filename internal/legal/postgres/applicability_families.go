// File metadata (manifest mirror):
// Purpose: Streams the fixed Legal-owned applicability head/version projection through the caller transaction.
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
	"time"

	"contractaudit/internal/legal/app"
	"contractaudit/internal/legal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type familyCursor struct{ rows pgx.Rows }

func (p *Applicability) OpenFactFamilies(ctx context.Context, id app.RecordIdentity) (app.FactFamilyCursor, error) {
	rows, err := p.db.Query(ctx, `SELECT f.fact_id,f.dimension,f.current_version,v.version,v.status,COALESCE(v.value,''),COALESCE(v.unknown_reason,''),v.temporal_basis,v.valid_from,v.valid_until,v.basis_obtained_at,v.created_at,v.basis_note,v.basis_refs,v.basis_sha256,
 COALESCE(v.party_side,''),v.party_binding_id,v.party_binding_version,v.party_subject_id,v.party_subject_version,COALESCE(v.party_scope_note,'') FROM case_legal_applicability_facts f JOIN case_legal_applicability_fact_versions v USING (tenant_id,org_id,case_id,fact_id,dimension) WHERE f.tenant_id=$1 AND f.org_id=$2 AND f.case_id=$3 ORDER BY f.dimension,v.version`, id.TenantID, id.OrgID, id.CaseID)
	if err != nil {
		return nil, err
	}
	return &familyCursor{rows: rows}, nil
}
func (c *familyCursor) Close() { c.rows.Close() }
func (c *familyCursor) Next() (app.FactFamilyRow, bool, error) {
	if !c.rows.Next() {
		return app.FactFamilyRow{}, false, c.rows.Err()
	}
	var row app.FactFamilyRow
	fact := &row.Fact
	var until, obtained pgtype.Timestamptz
	var side, bindingID, subjectID, scopeNote pgtype.Text
	var bindingVersion, subjectVersion pgtype.Int8
	err := c.rows.Scan(&fact.FactID, &fact.Dimension, &row.Head, &fact.Version, &fact.Status, &fact.Value, &fact.UnknownReason, &fact.TemporalBasis, &fact.ValidFrom, &until, &obtained, &fact.CreatedAt, &fact.BasisNote, &fact.BasisRefs, &fact.BasisSHA256, &side, &bindingID, &bindingVersion, &subjectID, &subjectVersion, &scopeNote)
	if err != nil {
		return app.FactFamilyRow{}, false, err
	}
	if until.Valid {
		value := until.Time.UTC().Truncate(time.Microsecond)
		fact.ValidUntil = &value
	}
	if obtained.Valid {
		value := obtained.Time.UTC().Truncate(time.Microsecond)
		fact.BasisObtainedAt = &value
	}
	fact.Party = model.LegalApplicabilityPartyPin{Side: side.String, BindingID: bindingID.String, SubjectID: subjectID.String, ScopeNote: scopeNote.String}
	if bindingVersion.Valid {
		fact.Party.BindingVersion = bindingVersion.Int64
	}
	if subjectVersion.Valid {
		fact.Party.SubjectVersion = subjectVersion.Int64
	}
	return row, true, nil
}
