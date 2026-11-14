// File metadata (manifest mirror):
// Purpose: Adapts current Case, Knowledge, Legal capture and staged Subject pin facts for Legal review freezing without changing transaction ownership.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"

	"contractaudit/internal/accounts"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"
	knowledgePostgres "contractaudit/internal/knowledge/postgres"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
	legalPostgres "contractaudit/internal/legal/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	subjectPostgres "contractaudit/internal/subject/postgres"
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
	v, err := subjectPostgres.ReadCurrentBinding(ctx, platformpostgres.Bind(facts.tx), accounts.Tenant(facts.user), facts.item.OwnerOrgID, facts.item.ID, pin.BindingID)
	return legalApp.SubjectBinding{BindingVersion: v.HeadVersion, SubjectID: v.SubjectID, SubjectVersion: v.SubjectVersion, Role: v.Role, State: v.State, Found: v.Found}, err
}
func (facts *legalReviewFacts) LoadSubjectVersion(ctx context.Context, id string) (int64, bool, error) {
	return subjectPostgres.ReadSubjectCurrentVersion(ctx, platformpostgres.Bind(facts.tx), accounts.Tenant(facts.user), id)
}
func (facts *legalReviewFacts) CountActiveRole(ctx context.Context, role string) (int, error) {
	return subjectPostgres.CountActiveRole(ctx, platformpostgres.Bind(facts.tx), accounts.Tenant(facts.user), facts.item.OwnerOrgID, facts.item.ID, role)
}
