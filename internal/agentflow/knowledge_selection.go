package agentflow

import (
	"slices"
	"unicode/utf8"
)

// validateKnowledgeSelection prevents a trace from claiming that a different
// set of references or a complete policy context was supplied to the planner.
func validateKnowledgeSelection(request Request) error {
	record := request.KnowledgeSelection
	if record == nil {
		return nil
	}
	if record.Algorithm != "chinese-bigram-label/v1" || len(record.Candidates) > MaxKnowledgeRecords ||
		record.Candidates == nil || record.Input.Gaps == nil || record.Input.Evidence == nil || record.PlannerRounds == nil || len(record.PlannerRounds) != 0 || record.Reads == nil || len(record.Reads) != 0 ||
		len(record.Input.Gaps) != len(request.Gaps) || len(record.Input.Evidence) != len(request.Evidence) ||
		(record.SnapshotSHA256 != "" && !normalizedDigest.MatchString(record.SnapshotSHA256)) {
		return ErrInput
	}
	for _, value := range []string{record.Input.BusinessLine, record.Input.Scenario, record.Input.ContractType} {
		if len(value) > 128 || !utf8.ValidString(value) {
			return ErrInput
		}
	}
	gaps := make(map[string]Gap, len(request.Gaps))
	for _, gap := range request.Gaps {
		gaps[gap.ID] = gap
	}
	for _, input := range record.Input.Gaps {
		gap, ok := gaps[input.GapID]
		if !ok || gap.Domain != input.Domain || gap.Capability != input.Capability || gap.Question != input.Question {
			return ErrInput
		}
		delete(gaps, input.GapID)
	}
	evidence := make(map[string]Evidence, len(request.Evidence))
	for _, item := range request.Evidence {
		evidence[item.ID] = item
	}
	for _, input := range record.Input.Evidence {
		item, ok := evidence[input.EvidenceID]
		total := utf8.RuneCountInString(item.Text)
		if !ok || item.Role != input.Role || item.SHA256 != input.SHA256 || input.TotalRunes != total || input.ScannedRunes != min(total, record.Budget.EvidenceRunesPerDocument) {
			return ErrInput
		}
		delete(evidence, input.EvidenceID)
	}
	budget := record.Budget
	if budget.ReferenceLimit != PlannerKnowledgeReferenceBudget || budget.FieldBytes != PlannerKnowledgeFieldBytes ||
		budget.EvidenceRunesPerDocument != 4096 || budget.ContentBytesLimit < 0 || budget.ContentBytesLimit > MaxPlannerEvidenceBytes ||
		budget.ContentBytesUsed < 0 || budget.ContentBytesUsed > budget.ContentBytesLimit ||
		budget.ReadCallsLimit != KnowledgeReadMaxCalls || budget.ReadMaxRunes != KnowledgeReadMaxRunes || budget.ReadMaxBytes != KnowledgeReadMaxBytes ||
		budget.PriorReadCalls != request.PriorKnowledgeReadCount || budget.PriorReadCalls < 0 || budget.PriorReadCalls > KnowledgeReadMaxCalls ||
		budget.PriorReadReservedBytes != budget.PriorReadCalls*KnowledgeReadMaxBytes {
		return ErrInput
	}
	selected := make(map[KnowledgeIdentity]KnowledgeReference, len(request.Knowledge))
	contentBytes, requiredCount, optionalSelected := 0, 0, 0
	policyTruncated, policyNeedsVerification := false, false
	for _, ref := range request.Knowledge {
		key := KnowledgeIdentity{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID, ContentSHA256: ref.ContentSHA256}
		selected[key] = ref
		for _, value := range []string{ref.Summary, ref.Applicability, ref.Exclusions, ref.CheckAction, ref.MinimumEvidence, ref.Counterexamples, ref.SemanticChecks, ref.SourceSpans} {
			if len(value) > PlannerKnowledgeFieldBytes {
				return ErrInput
			}
			contentBytes += len(value)
		}
		if ref.Required {
			requiredCount++
			policyTruncated = policyTruncated || ref.Truncated
			policyNeedsVerification = policyNeedsVerification || ref.RequiresFactEvaluation
		} else {
			optionalSelected++
		}
	}
	optionalCandidates, optionalOmitted, acceptanceOmitted := 0, 0, 0
	seen := map[KnowledgeIdentity]bool{}
	for _, candidate := range record.Candidates {
		key := KnowledgeIdentity{KnowledgeID: candidate.KnowledgeID, VersionID: candidate.VersionID, ContentSHA256: candidate.ContentSHA256}
		if !boundedIdentity(key.KnowledgeID) || !boundedIdentity(key.VersionID) || !normalizedDigest.MatchString(key.ContentSHA256) || seen[key] || candidate.Score < 0 {
			return ErrInput
		}
		seen[key] = true
		switch candidate.Reason {
		case "required_policy":
			if !candidate.Required || candidate.Kind != "policy" || candidate.Decision != "selected" {
				return ErrInput
			}
		case "relevance":
			if candidate.Required || candidate.Score == 0 || candidate.Decision != "selected" || !slices.Contains([]string{"guidance", "experience", "exception"}, candidate.Kind) {
				return ErrInput
			}
			optionalCandidates++
		case "no_relevance", "planner_budget":
			if candidate.Required || candidate.Decision != "omitted" || candidate.Truncated || !slices.Contains([]string{"guidance", "experience", "exception"}, candidate.Kind) ||
				(candidate.Reason == "no_relevance" && candidate.Score != 0) || (candidate.Reason == "planner_budget" && candidate.Score == 0) {
				return ErrInput
			}
			optionalCandidates++
			optionalOmitted++
		case "acceptance_optional_budget":
			if candidate.Required || candidate.Kind != "" || candidate.Score != 0 || candidate.Truncated || candidate.Decision != "omitted" {
				return ErrInput
			}
			acceptanceOmitted++
		default:
			return ErrInput
		}
		if candidate.Decision == "selected" {
			ref, ok := selected[key]
			if !ok || ref.Kind != candidate.Kind || ref.Required != candidate.Required || ref.Truncated != candidate.Truncated || ref.RequiresFactEvaluation != candidate.RequiresFactEvaluation {
				return ErrInput
			}
			delete(selected, key)
		} else if _, ok := selected[key]; ok {
			return ErrInput
		}
	}
	if len(selected) != 0 || budget.RequiredCount != requiredCount || budget.OptionalCandidates != optionalCandidates ||
		budget.OptionalSelected != optionalSelected || budget.OptionalOmitted != optionalOmitted || budget.AcceptanceOmitted != acceptanceOmitted ||
		budget.AcceptanceTruncated != (acceptanceOmitted > 0) || budget.ContentBytesUsed != contentBytes ||
		(optionalSelected > 0 && len(request.Knowledge) > PlannerKnowledgeReferenceBudget) {
		return ErrInput
	}
	switch {
	case record.SnapshotSHA256 == "":
		if requiredCount != 0 || len(record.Candidates) != 0 || record.PolicyStatus != "unknown" || record.PolicyReason != "policy_snapshot_missing" {
			return ErrInput
		}
	case requiredCount == 0:
		if record.PolicyStatus != "unknown" || record.PolicyReason != "policy_missing" {
			return ErrInput
		}
	case policyTruncated:
		if record.PolicyStatus != "unknown" || record.PolicyReason != "policy_context_truncated" {
			return ErrInput
		}
	case policyNeedsVerification:
		if record.PolicyStatus != "unknown" || record.PolicyReason != "policy_semantic_verification_required" {
			return ErrInput
		}
	default:
		if record.PolicyStatus != "available" || record.PolicyReason != "" {
			return ErrInput
		}
	}
	for _, check := range knowledgePolicyChecks(record) {
		for _, gap := range request.Gaps {
			if check.GapID == gap.ID {
				return ErrInput
			}
		}
	}
	return nil
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
