package store

import (
	"context"
	"sort"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	operatorApp "contractaudit/internal/operator/app"
	"contractaudit/internal/rulepack"
	"github.com/jackc/pgx/v5"
)

type operatorPreviewFacts struct {
	store *Store
	tx    pgx.Tx
	state state
	item  Case
	user  accounts.User
}

var _ operatorApp.PreviewFacts[Document] = operatorPreviewFacts{}

func (f operatorPreviewFacts) Run(id string) (Run, bool)            { return f.state.runByID(id) }
func (f operatorPreviewFacts) Attempt(id string) (Attempt, bool)    { return f.state.attemptByID(id) }
func (f operatorPreviewFacts) Input(id string) (AttemptInput, bool) { return f.state.attemptInput(id) }
func (f operatorPreviewFacts) SnapshotForHash(id string) operatorApp.PreviewSnapshot {
	snapshot := scopeSnapshot(f.state.snapshotOf(id))
	sort.Strings(snapshot.DocumentIDs)
	sort.Strings(snapshot.ParsedDocumentIDs)
	return snapshot
}
func (f operatorPreviewFacts) Selections(id string) []AttemptParseSelection {
	return f.state.parseSelectionsForAttempt(id)
}
func (f operatorPreviewFacts) VerifyDocuments(_ context.Context, docs []FrozenDocument) error {
	return f.store.verifyFrozenDocuments(f.state, docs)
}
func (f operatorPreviewFacts) AgentDigest(ctx context.Context, id string) (string, error) {
	return agentReviewDigestForOutput(ctx, f.tx, f.item.TenantID, f.item.OwnerOrgID, id)
}
func (f operatorPreviewFacts) OutputRows(runID, attemptID string) ([]Finding, []ConstraintEval) {
	return f.state.findingsForAttempt(runID, attemptID), f.state.evaluationsForAttempt(runID, attemptID)
}
func (f operatorPreviewFacts) HasMaterial(id string, version int, documentID string) bool {
	for _, v := range f.state.MaterialVersions {
		if v.TenantID == f.item.TenantID && v.OrgID == f.item.OwnerOrgID && v.CaseID == f.item.ID && v.MaterialID == id && v.Version == version && v.DocumentID == documentID {
			return true
		}
	}
	return false
}
func (f operatorPreviewFacts) TelecomSource(_ context.Context, doc FrozenDocument) (rulepack.Source, error) {
	sources, err := telecomSources(interpretedFromFrozen([]FrozenDocument{doc}))
	if err != nil {
		return rulepack.Source{}, err
	}
	return sources[0], nil
}
func (operatorPreviewFacts) Now() time.Time { return time.Now() }

func (f operatorPreviewFacts) ExtractionAuthorization() (access.Principal, access.Resource) {
	return bindLocalDemoCaseGrant(f.item, f.user, accounts.Principal(f.user)), access.Resource{TenantID: f.item.TenantID, OrgID: f.item.OwnerOrgID, CaseID: f.item.ID, OwnerID: f.item.OwnerID}
}
