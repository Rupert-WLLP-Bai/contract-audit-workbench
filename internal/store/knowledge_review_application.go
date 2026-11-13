package store

import (
	"context"

	"contractaudit/internal/accounts"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"

	"github.com/jackc/pgx/v5"
)

// reviewKnowledgeFacts binds the existing ordinary review actor and current
// transaction; it does not grant administrative authority.
type reviewKnowledgeFacts struct {
	governedKnowledgeFacts
	actor     accounts.User
	item      Case
	lockCases bool
}

func newReviewKnowledgeFacts(tx pgx.Tx, actor accounts.User, item Case, lockCases bool) reviewKnowledgeFacts {
	return reviewKnowledgeFacts{governedKnowledgeFacts: governedKnowledgeFacts{tx: tx, admin: actor}, actor: actor, item: item, lockCases: lockCases}
}
func (f reviewKnowledgeFacts) AuthorizeReadSource(ctx context.Context, c knowledgeModel.GovernedKnowledgeContent) error {
	return authorizeKnowledgeSourceWithCaseLocks(ctx, f.tx, f.actor, c, f.lockCases)
}
func (f reviewKnowledgeFacts) LoadReadCandidate(ctx context.Context, c knowledgeModel.GovernedKnowledgeContent) (knowledgeApp.ExperienceCandidateFacts, error) {
	return f.loadExperienceCandidate(ctx, c, f.lockCases)
}
func (f reviewKnowledgeFacts) AuthorizeReadAncestors(ctx context.Context, c knowledgeApp.ExperienceCandidateFacts) error {
	return authorizeExperienceAncestorsWithCaseLocks(ctx, f.tx, f.actor, Case{ID: c.CaseID, TenantID: c.TenantID}, c.CandidateID, c.BaseVersionID, f.lockCases)
}
func (f reviewKnowledgeFacts) AuthorizationRevision(context.Context) (string, error) {
	return knowledgeAuthorizationRevision(f.actor, f.item)
}

var _ knowledgeApp.ReviewFacts = reviewKnowledgeFacts{}
