// File metadata (manifest mirror):
// Purpose: Persists and reads Legal-owned fact heads and immutable versions with exact replay, row locks, and head CAS.
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
	"time"

	"contractaudit/internal/legal/app"
	"contractaudit/internal/legal/model"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Applicability struct{ db platformpostgres.Executor }

var _ app.RecordRepository = (*Applicability)(nil)
var _ app.FactFamilyReader = (*Applicability)(nil)
var _ app.HistoryRepository = (*Applicability)(nil)

func NewApplicability(db platformpostgres.Executor) *Applicability { return &Applicability{db: db} }
func (p *Applicability) FindVersionByActorKey(ctx context.Context, id app.RecordIdentity, dimension, key string) (int64, bool, error) {
	var version int64
	err := p.db.QueryRow(ctx, `SELECT version FROM case_legal_applicability_fact_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 AND actor_user_id=$5 AND idempotency_key=$6`, id.TenantID, id.OrgID, id.CaseID, dimension, id.ActorID, key).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return version, err == nil, err
}
func (p *Applicability) LockHead(ctx context.Context, id app.RecordIdentity, dimension string) (string, int64, error) {
	var factID string
	var version int64
	err := p.db.QueryRow(ctx, `SELECT fact_id,current_version FROM case_legal_applicability_facts WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 FOR UPDATE`, id.TenantID, id.OrgID, id.CaseID, dimension).Scan(&factID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, nil
	}
	return factID, version, err
}
func (p *Applicability) InsertHead(ctx context.Context, id app.RecordIdentity, dimension, factID string, now time.Time) error {
	_, err := p.db.Exec(ctx, `INSERT INTO case_legal_applicability_facts (tenant_id,org_id,case_id,fact_id,dimension,current_version,created_by_user_id,created_by_issuer,created_by_subject,created_at) VALUES ($1,$2,$3,$4,$5,1,$6,$7,$8,$9)`, id.TenantID, id.OrgID, id.CaseID, factID, dimension, id.ActorID, id.Issuer, id.Subject, now)
	return err
}
func (p *Applicability) AdvanceHead(ctx context.Context, id app.RecordIdentity, factID string, old, next int64) (bool, error) {
	tag, err := p.db.Exec(ctx, `UPDATE case_legal_applicability_facts SET current_version=$1 WHERE tenant_id=$2 AND org_id=$3 AND case_id=$4 AND fact_id=$5 AND current_version=$6`, next, id.TenantID, id.OrgID, id.CaseID, factID, old)
	return err == nil && tag.RowsAffected() == 1, err
}
func (p *Applicability) AppendVersion(ctx context.Context, fact model.LegalApplicabilityFactVersion) error {
	var until any
	if fact.ValidUntil != nil {
		until = *fact.ValidUntil
	}
	var obtained any
	if fact.BasisObtainedAt != nil {
		obtained = *fact.BasisObtainedAt
	}
	party := fact.Party
	var side, bindingID, subjectID, scopeNote any
	var bindingVersion, subjectVersion any
	if party.BindingID != "" {
		side, bindingID, bindingVersion, subjectID, subjectVersion, scopeNote = party.Side, party.BindingID, party.BindingVersion, party.SubjectID, party.SubjectVersion, party.ScopeNote
	}
	_, err := p.db.Exec(ctx, `INSERT INTO case_legal_applicability_fact_versions
		(tenant_id,org_id,case_id,fact_id,version,dimension,status,value,unknown_reason,temporal_basis,valid_from,valid_until,basis_note,basis_refs,basis_sha256,basis_obtained_at,
		 party_side,party_binding_id,party_binding_version,party_subject_id,party_subject_version,party_scope_note,request_digest,idempotency_key,actor_user_id,actor_issuer,actor_subject,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13,$14::jsonb,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)`,
		fact.TenantID, fact.OrgID, fact.CaseID, fact.FactID, fact.Version, fact.Dimension, fact.Status, fact.Value, fact.UnknownReason,
		fact.TemporalBasis, fact.ValidFrom, until, fact.BasisNote, string(fact.BasisRefs), fact.BasisSHA256, obtained,
		side, bindingID, bindingVersion, subjectID, subjectVersion, scopeNote, fact.RequestDigest, fact.IdempotencyKey,
		fact.ActorUserID, fact.ActorIssuer, fact.ActorSubject, fact.CreatedAt)
	return err
}

func (p *Applicability) ReadVersion(ctx context.Context, id app.RecordIdentity, dimension string, version int64) (model.LegalApplicabilityFactVersion, error) {
	var fact model.LegalApplicabilityFactVersion
	var until, obtained pgtype.Timestamptz
	var side, bindingID, subjectID, scopeNote pgtype.Text
	var bindingVersion, subjectVersion pgtype.Int8
	err := p.db.QueryRow(ctx, `SELECT fact_id,version,dimension,status,COALESCE(value,''),COALESCE(unknown_reason,''),temporal_basis,valid_from,valid_until,basis_note,basis_refs,basis_sha256,basis_obtained_at,
		COALESCE(party_side,''),party_binding_id,party_binding_version,party_subject_id,party_subject_version,COALESCE(party_scope_note,''),request_digest,idempotency_key,actor_user_id,actor_issuer,actor_subject,created_at
		FROM case_legal_applicability_fact_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 AND version=$5`,
		id.TenantID, id.OrgID, id.CaseID, dimension, version).Scan(&fact.FactID, &fact.Version, &fact.Dimension, &fact.Status, &fact.Value, &fact.UnknownReason, &fact.TemporalBasis,
		&fact.ValidFrom, &until, &fact.BasisNote, &fact.BasisRefs, &fact.BasisSHA256, &obtained,
		&side, &bindingID, &bindingVersion, &subjectID, &subjectVersion, &scopeNote, &fact.RequestDigest, &fact.IdempotencyKey, &fact.ActorUserID, &fact.ActorIssuer, &fact.ActorSubject, &fact.CreatedAt)
	if err != nil {
		return model.LegalApplicabilityFactVersion{}, err
	}
	fact.ValidFrom = fact.ValidFrom.UTC().Truncate(time.Microsecond)
	fact.CreatedAt = fact.CreatedAt.UTC().Truncate(time.Microsecond)
	fact.TenantID, fact.OrgID, fact.CaseID = id.TenantID, id.OrgID, id.CaseID
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
	return fact, nil
}
