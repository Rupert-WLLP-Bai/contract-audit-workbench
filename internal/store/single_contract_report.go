package store

import (
	"contractaudit/internal/engine"
	"contractaudit/internal/fault"
)

// validateSingleContractReport preserves the ordinary input/output and object
// integrity checks when an ungoverned precheck has no governed RulePack.
func (s *Store) validateSingleContractReport(st state, item Case, runID, attemptID string) (Run, Attempt, AttemptInput, error) {
	invalid := func() (Run, Attempt, AttemptInput, error) {
		return Run{}, Attempt{}, AttemptInput{}, fault.Conflict("单合同预审的冻结输入、规则映射或结果未通过校验")
	}
	run, attempt, err := terminalRunAndAttempt(st, item, runID, attemptID)
	if err != nil || requireFrozenAttemptHashes(attempt) != nil || isGovernedReviewCase(item) ||
		attempt.RulePackCompilerRef != "" || attempt.RulePackID != "" || attempt.RulePackVersion != "" ||
		attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" || len(attempt.RulePackSnapshot) != 0 {
		return invalid()
	}
	snapshot := st.snapshotOf(runID)
	if !isSingleContractSnapshot(snapshot) {
		return invalid()
	}
	if _, err := engine.PublishedSingleContractMapping(snapshot.RuleSetHash); err != nil {
		return invalid()
	}
	input, ok := st.attemptInput(attemptID)
	if !ok || input.RunID != runID || attempt.InputHash == nil || input.InputHash != *attempt.InputHash ||
		!frozenAttemptInputMatches(st, runID, attemptID, input) || s.verifyFrozenDocuments(st, input.Documents) != nil {
		return invalid()
	}
	primaryCount := 0
	for _, doc := range input.Documents {
		if doc.Document.Role == "draft_contract" {
			primaryCount++
		}
	}
	evaluations := st.evaluationsForAttempt(runID, attemptID)
	if primaryCount != 1 || len(evaluations) != 1 || evaluations[0].RuleVersion != engine.SingleContractPrecheckRule || evaluations[0].Domain != "payment_sum" {
		return invalid()
	}
	digest, err := s.agentReviewDigestFor(item.TenantID, item.OwnerOrgID, attemptID)
	if err != nil || !frozenAttemptOutputMatches(st, runID, attemptID, attempt, digest) {
		return invalid()
	}
	return run, attempt, input, nil
}
