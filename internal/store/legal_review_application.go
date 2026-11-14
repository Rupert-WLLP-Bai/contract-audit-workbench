package store

import (
	"context"
	"errors"

	"contractaudit/internal/accounts"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"
	knowledgePostgres "contractaudit/internal/knowledge/postgres"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
	legalPostgres "contractaudit/internal/legal/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
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
	return legalApp.LoadFactFamilies(ctx, legalPostgres.NewApplicability(platformpostgres.Bind(facts.tx)), legalApp.RecordIdentity{TenantID: accounts.Tenant(facts.user), OrgID: caseItem.OwnerOrgID, CaseID: facts.item.ID})
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
