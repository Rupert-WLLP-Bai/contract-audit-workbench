package store

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"
	knowledgePostgres "contractaudit/internal/knowledge/postgres"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ legalApp.FreezeFacts = (*legalReviewFacts)(nil)

type legalReviewFacts struct {
	tx        pgx.Tx
	user      accounts.User
	item      Case
	knowledge reviewKnowledgeFacts
}

func newLegalReviewFacts(tx pgx.Tx, user accounts.User, item Case, lockCases bool) *legalReviewFacts {
	return &legalReviewFacts{tx: tx, user: user, item: item, knowledge: newReviewKnowledgeFacts(tx, user, item, lockCases)}
}
func (facts *legalReviewFacts) LoadKnowledgeVersion(ctx context.Context, id, version string) (knowledgeModel.GovernedKnowledgeVersion, error) {
	repo := knowledgePostgres.NewGovernance(platformpostgres.Bind(facts.tx))
	v, err := knowledgeApp.LoadExactVersion(ctx, repo, facts.knowledge, accounts.Tenant(facts.user), id, version)
	return v.Version, err
}
func (facts *legalReviewFacts) LoadSourceCapture(ctx context.Context, id string) (legalModel.LegalSourceCapture, error) {
	return loadLegalSourceCaptureTx(ctx, facts.tx, accounts.Tenant(facts.user), id)
}
func (facts *legalReviewFacts) LoadFactFamilies(ctx context.Context) ([]legalApp.FactFamily, error) {
	caseItem, err := loadKnowledgeCase(ctx, facts.tx, facts.user, facts.item.ID)
	if err != nil {
		return nil, err
	}
	facts.item = caseItem
	if err := setLocalScope(ctx, facts.tx, func() dbScope { scope := userScope(facts.user); scope.CaseID = facts.item.ID; return scope }()); err != nil {
		return nil, err
	}
	rows, err := facts.tx.Query(ctx, `SELECT f.fact_id,f.dimension,f.current_version,v.version,v.status,COALESCE(v.value,''),COALESCE(v.unknown_reason,''),v.temporal_basis,v.valid_from,v.valid_until,v.basis_obtained_at,v.created_at,v.basis_note,v.basis_refs,v.basis_sha256,
		COALESCE(v.party_side,''),v.party_binding_id,v.party_binding_version,v.party_subject_id,v.party_subject_version,COALESCE(v.party_scope_note,'')
		FROM case_legal_applicability_facts f JOIN case_legal_applicability_fact_versions v USING (tenant_id,org_id,case_id,fact_id,dimension)
		WHERE f.tenant_id=$1 AND f.org_id=$2 AND f.case_id=$3 ORDER BY f.dimension,v.version`, accounts.Tenant(facts.user), caseItem.OwnerOrgID, facts.item.ID)
	if err != nil {
		return nil, err
	}
	type factFamily struct {
		head     int64
		versions []LegalApplicabilityFactVersion
	}
	families := make(map[string]*factFamily)
	for rows.Next() {
		var fact LegalApplicabilityFactVersion
		var head int64
		var until, obtained pgtype.Timestamptz
		var side, bindingID, subjectID, scopeNote pgtype.Text
		var bindingVersion, subjectVersion pgtype.Int8
		if err := rows.Scan(&fact.FactID, &fact.Dimension, &head, &fact.Version, &fact.Status, &fact.Value, &fact.UnknownReason,
			&fact.TemporalBasis, &fact.ValidFrom, &until, &obtained, &fact.CreatedAt, &fact.BasisNote, &fact.BasisRefs, &fact.BasisSHA256,
			&side, &bindingID, &bindingVersion, &subjectID, &subjectVersion, &scopeNote); err != nil {
			rows.Close()
			return nil, err
		}
		if until.Valid {
			value := until.Time.UTC().Truncate(time.Microsecond)
			fact.ValidUntil = &value
		}
		if obtained.Valid {
			value := obtained.Time.UTC().Truncate(time.Microsecond)
			fact.BasisObtainedAt = &value
		}
		fact.Party = LegalApplicabilityPartyPin{Side: side.String, BindingID: bindingID.String, SubjectID: subjectID.String, ScopeNote: scopeNote.String}
		if bindingVersion.Valid {
			fact.Party.BindingVersion = bindingVersion.Int64
		}
		if subjectVersion.Valid {
			fact.Party.SubjectVersion = subjectVersion.Int64
		}
		canonicalBasis, err := verifyLegalApplicabilityBasisManifest(fact.BasisRefs, fact.BasisSHA256)
		if err != nil {
			rows.Close()
			return nil, fault.Conflict("法律适用事实材料依据摘要不匹配")
		}
		fact.BasisRefs = canonicalBasis
		fact.ValidFrom = fact.ValidFrom.UTC().Truncate(time.Microsecond)
		fact.CreatedAt = fact.CreatedAt.UTC().Truncate(time.Microsecond)
		fact.TenantID, fact.OrgID, fact.CaseID = accounts.Tenant(facts.user), caseItem.OwnerOrgID, facts.item.ID
		family := families[fact.Dimension]
		if family == nil {
			family = &factFamily{head: head}
			families[fact.Dimension] = family
		}
		if family.head != head {
			rows.Close()
			return nil, errors.New("legal applicability head changed within snapshot")
		}
		family.versions = append(family.versions, fact)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := make([]legalApp.FactFamily, 0, len(families))
	for dimension, family := range families {
		out = append(out, legalApp.FactFamily{Dimension: dimension, Head: family.head, Versions: family.versions})
	}
	return out, nil
}

func (facts *legalReviewFacts) LoadSubjectBinding(ctx context.Context, pin legalModel.LegalApplicabilityPartyPin) (legalApp.SubjectBinding, error) {
	var out legalApp.SubjectBinding
	err := facts.tx.QueryRow(ctx, `SELECT h.current_version,v.subject_id,v.subject_version,v.role,v.binding_state FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.binding_id=$4`, accounts.Tenant(facts.user), facts.item.OwnerOrgID, facts.item.ID, pin.BindingID).Scan(&out.BindingVersion, &out.SubjectID, &out.SubjectVersion, &out.Role, &out.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Found = true
	return out, nil
}
func (facts *legalReviewFacts) LoadSubjectVersion(ctx context.Context, id string) (int64, bool, error) {
	var v int64
	err := facts.tx.QueryRow(ctx, `SELECT current_version FROM subjects WHERE tenant_id=$1 AND subject_id=$2`, accounts.Tenant(facts.user), id).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return v, err == nil, err
}
func (facts *legalReviewFacts) CountActiveRole(ctx context.Context, role string) (int, error) {
	var count int
	err := facts.tx.QueryRow(ctx, `SELECT count(*) FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.current_version=v.version AND v.role=$4 AND v.binding_state='active'`, accounts.Tenant(facts.user), facts.item.OwnerOrgID, facts.item.ID, role).Scan(&count)
	return count, err
}
