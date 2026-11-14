package store

import (
	"sort"

	executionmodel "contractaudit/internal/execution/model"
)

type Run = executionmodel.Run
type Attempt = executionmodel.Attempt
type ReviewScopeIdentity = executionmodel.ReviewScopeIdentity
type AttemptParseSelection = executionmodel.AttemptParseSelection
type FrozenMaterialSelection = executionmodel.FrozenMaterialSelection
type FrozenDocument = executionmodel.FrozenDocument[Document]
type AttemptInput = executionmodel.AttemptInput[Document]
type RunSnapshot = executionmodel.RunSnapshot[PartyContextItem, ReviewKnowledgeSnapshot, ServiceObjectReviewContext]
type Anchor = executionmodel.Anchor
type EvaluationEvidenceRef = executionmodel.EvaluationEvidenceRef
type ExperienceEvidenceRef = executionmodel.EvaluationEvidenceRef
type EvaluationEvidence = executionmodel.EvaluationEvidence
type ConstraintEval = executionmodel.ConstraintEval
type FindingSeveritySource = executionmodel.FindingSeveritySource
type Finding = executionmodel.Finding

// OriginalProof returns only immutable original-file facts used by frozen
// execution checks. It performs no I/O or authorization and does not alter the
// serialized Document value.
func (d Document) OriginalProof() executionmodel.OriginalDocumentProof {
	return executionmodel.OriginalDocumentProof{
		ID: d.ID, Digest: d.Digest, TenantID: d.TenantID, OrgID: d.OrgID,
		CaseID: d.CaseID, Role: d.Role, CreatedAt: d.CreatedAt,
	}
}

func inputHashVersion(snapshot RunSnapshot) int {
	return executionmodel.InputHashVersion(snapshot)
}

func frozenInputHash(snapshot RunSnapshot, docs []FrozenDocument) (string, error) {
	snapshot = scopeSnapshot(snapshot)
	// Preserve the old helper's observable in-place ordering of these shared
	// snapshot slices before the pure model computes from its own copy.
	sort.Strings(snapshot.DocumentIDs)
	sort.Strings(snapshot.ParsedDocumentIDs)
	return executionmodel.FrozenInputHash(snapshot, docs)
}

func selectionHash(docs []FrozenDocument) string {
	return executionmodel.SelectionHash(docs)
}

func frozenConfigHash(docs []FrozenDocument) string {
	return executionmodel.FrozenConfigHash(docs)
}

func frozenInputMatchesSnapshot(snapshot RunSnapshot, docs []FrozenDocument) bool {
	return executionmodel.FrozenInputMatchesSnapshot(snapshot, docs)
}

func attemptSelectionsMatchFrozen(selections []AttemptParseSelection, docs []FrozenDocument) bool {
	return executionmodel.AttemptSelectionsMatchFrozen(selections, docs)
}

func attemptOutputHash(st state, runID, attemptID, agentReviewDigest string) (string, error) {
	findings := st.findingsForAttempt(runID, attemptID)
	attempt, _ := st.attemptByID(attemptID)
	evaluations := st.evaluationsForAttempt(runID, attemptID)
	return executionmodel.AttemptOutputHash(findings, evaluations,
		attempt.RulePackHash, attempt.RulePackSnapshotDigest, attempt.RulePackCompilerRef, agentReviewDigest)
}
