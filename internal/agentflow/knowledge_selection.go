package agentflow

// validateKnowledgeSelection prevents a trace from claiming that a different
// set of references or a complete policy context was supplied to the planner.
func validateKnowledgeSelection(request Request) error {
	return validateSelectionRecord(request)
}

// These checks cannot be discharged by numeric comparators or by retrieving a
// policy span. They remain unknown until a semantic verifier actually runs.
func knowledgePolicyChecks(record *KnowledgeSelectionRecord) []MandatoryCheck {
	if record == nil || record.PolicyStatus == "available" {
		return nil
	}
	if record.Budget.RequiredCount == 0 {
		return []MandatoryCheck{{GapID: "knowledge_policy_missing", Domain: Compliance, Capability: "governed_policy_context", Status: "unknown", Reason: record.PolicyReason}}
	}
	checks := []MandatoryCheck{}
	for _, candidate := range record.Candidates {
		if !candidate.Required || (!candidate.Truncated && !candidate.RequiresFactEvaluation) {
			continue
		}
		reason, capability := "policy_semantic_verification_required", "governed_policy_semantics"
		if candidate.Truncated {
			reason, capability = "policy_context_truncated", "governed_policy_context"
		}
		identity := candidate.KnowledgeID + "\x00" + candidate.VersionID + "\x00" + candidate.ContentSHA256
		checks = append(checks, MandatoryCheck{GapID: "knowledge_policy_" + TextHash(identity)[:24], Domain: Compliance,
			Capability: capability, Status: "unknown", Reason: reason})
	}
	return checks
}

var advisoryBands = []string{"relevant", "uncertain", "not_relevant", "unknown"}

var advisoryStatuses = []string{
	"completed", "unavailable", "not_applicable", "model_mismatch",
	"invalid_response", "budget_exhausted", "persistence_failed", "config_mismatch",
}
