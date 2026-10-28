package agentflow

import (
	"math"
	"slices"
	"sort"
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
	if err := validateKnowledgeAdvisory(record, request); err != nil {
		return err
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

var advisoryBands = []string{"relevant", "uncertain", "not_relevant", "unknown"}

var advisoryStatuses = []string{
	"completed", "unavailable", "not_applicable", "model_mismatch",
	"invalid_response", "budget_exhausted", "persistence_failed", "config_mismatch",
}

func validateKnowledgeAdvisory(record *KnowledgeSelectionRecord, request Request) error {
	advisory := record.Advisory
	type rank struct {
		detRank   int
		finalRank int
	}
	ranks := map[KnowledgeIdentity]rank{}
	optional := []KnowledgeCandidate{}
	for _, candidate := range record.Candidates {
		if !candidate.Required && candidate.Score > 0 && (candidate.Reason == "relevance" || candidate.Reason == "planner_budget") {
			optional = append(optional, candidate)
		}
	}
	sort.SliceStable(optional, func(i, j int) bool {
		if optional[i].Score != optional[j].Score {
			return optional[i].Score > optional[j].Score
		}
		left, right := optional[i], optional[j]
		if left.KnowledgeID != right.KnowledgeID {
			return left.KnowledgeID < right.KnowledgeID
		}
		if left.VersionID != right.VersionID {
			return left.VersionID < right.VersionID
		}
		return left.ContentSHA256 < right.ContentSHA256
	})
	for index, candidate := range optional {
		ranks[KnowledgeIdentity{KnowledgeID: candidate.KnowledgeID, VersionID: candidate.VersionID, ContentSHA256: candidate.ContentSHA256}] = rank{detRank: index + 1}
	}
	finalIndex := 0
	for _, candidate := range record.Candidates {
		if candidate.Required || candidate.Score <= 0 || (candidate.Reason != "relevance" && candidate.Reason != "planner_budget") {
			continue
		}
		finalIndex++
		key := KnowledgeIdentity{KnowledgeID: candidate.KnowledgeID, VersionID: candidate.VersionID, ContentSHA256: candidate.ContentSHA256}
		item := ranks[key]
		item.finalRank = finalIndex
		ranks[key] = item
	}
	if advisory == nil {
		for _, candidate := range record.Candidates {
			if candidate.Advisory != nil {
				return ErrInput
			}
		}
		return nil
	}
	if advisory.Feature != AdvisoryFeatureKnowledgeRerank ||
		(advisory.Mode != AdvisoryModeAdvisory && advisory.Mode != AdvisoryModeFixture) ||
		(advisory.Provider != AdvisoryProviderGateway && advisory.Provider != AdvisoryProviderFixture && advisory.Provider != AdvisoryProviderDirect) ||
		advisory.QuestionSetVersion != AdvisoryQuestionSetVersion || advisory.Egress != AdvisoryEgressMinimized ||
		!normalizedDigest.MatchString(advisory.ConfigurationID) ||
		!slices.Contains(advisoryStatuses, advisory.Status) || len(advisory.Candidates) > MaxAdvisoryMaxCandidates ||
		advisory.Candidates == nil || len(advisory.ErrorCode) > 80 || !utf8.ValidString(advisory.ErrorCode) ||
		(advisory.ErrorCode != "" && !capabilityID.MatchString(advisory.ErrorCode)) {
		return ErrInput
	}
	if advisory.ModelRequested != "" && !capabilityID.MatchString(advisory.ModelRequested) {
		return ErrInput
	}
	if advisory.ModelObserved != "" && !capabilityID.MatchString(advisory.ModelObserved) {
		return ErrInput
	}
	if advisory.RequestSHA256 != "" && !normalizedDigest.MatchString(advisory.RequestSHA256) {
		return ErrInput
	}
	if advisory.JudgmentID != "" && !normalizedDigest.MatchString(advisory.JudgmentID) {
		return ErrInput
	}
	if advisory.Status == "completed" {
		if advisory.ModelRequested == "" || advisory.ModelObserved != advisory.ModelRequested ||
			advisory.RequestSHA256 == "" || advisory.JudgmentID == "" {
			return ErrInput
		}
	} else {
		if advisory.ModelObserved != "" {
			return ErrInput
		}
		for _, item := range advisory.Candidates {
			if item.Probability != nil || item.Band != "unknown" {
				return ErrInput
			}
		}
	}
	byIdentity := map[KnowledgeIdentity]KnowledgeAdvisoryCandidate{}
	for _, item := range advisory.Candidates {
		key := KnowledgeIdentity{KnowledgeID: item.KnowledgeID, VersionID: item.VersionID, ContentSHA256: item.ContentSHA256}
		if !boundedIdentity(key.KnowledgeID) || !boundedIdentity(key.VersionID) || !normalizedDigest.MatchString(key.ContentSHA256) ||
			!capabilityID.MatchString(item.Alias) || !slices.Contains(advisoryBands, item.Band) ||
			item.RankBefore < 1 || item.RankAfter < 1 || item.RankBefore > len(optional) || item.RankAfter > len(optional) ||
			len(item.Reason) > 64 || !utf8.ValidString(item.Reason) {
			return ErrInput
		}
		if _, duplicate := byIdentity[key]; duplicate {
			return ErrInput
		}
		byIdentity[key] = item
	}
	advisoryCandidates := 0
	for _, candidate := range record.Candidates {
		if candidate.Advisory == nil {
			if _, ok := byIdentity[KnowledgeIdentity{KnowledgeID: candidate.KnowledgeID, VersionID: candidate.VersionID, ContentSHA256: candidate.ContentSHA256}]; ok {
				return ErrInput
			}
			continue
		}
		if candidate.Required || candidate.Score <= 0 ||
			!slices.Contains([]string{"guidance", "experience", "exception"}, candidate.Kind) ||
			(candidate.Reason != "relevance" && candidate.Reason != "planner_budget") {
			return ErrInput
		}
		key := KnowledgeIdentity{KnowledgeID: candidate.KnowledgeID, VersionID: candidate.VersionID, ContentSHA256: candidate.ContentSHA256}
		item, ok := byIdentity[key]
		if !ok {
			return ErrInput
		}
		det := ranks[key]
		if item.Alias != candidate.Advisory.Alias || item.Band != candidate.Advisory.Band ||
			item.RankBefore != candidate.Advisory.RankBefore || item.RankAfter != candidate.Advisory.RankAfter ||
			item.Probability != nil != (candidate.Advisory.Probability != nil) ||
			item.RankBefore != det.detRank || item.RankAfter != det.finalRank {
			return ErrInput
		}
		if item.Probability == nil && item.Band != "unknown" {
			return ErrInput
		}
		if item.Probability != nil {
			if math.IsNaN(*item.Probability) || math.IsInf(*item.Probability, 0) || *item.Probability < 0 || *item.Probability > 1 ||
				candidate.Advisory.Probability == nil || *candidate.Advisory.Probability != *item.Probability {
				return ErrInput
			}
		}
		advisoryCandidates++
	}
	if advisoryCandidates != len(advisory.Candidates) {
		return ErrInput
	}
	if advisory.Status == "completed" && advisoryCandidates == 0 {
		return ErrInput
	}
	if advisory.Status == "completed" {
		if advisory.Truncated && advisoryCandidates == 0 {
			return ErrInput
		}
		selected := []KnowledgeIdentity{}
		for _, candidate := range record.Candidates {
			if candidate.Decision != "selected" {
				continue
			}
			selected = append(selected, KnowledgeIdentity{
				KnowledgeID: candidate.KnowledgeID, VersionID: candidate.VersionID, ContentSHA256: candidate.ContentSHA256,
			})
		}
		if len(selected) != len(request.Knowledge) {
			return ErrInput
		}
		for index, ref := range request.Knowledge {
			if selected[index] != (KnowledgeIdentity{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID, ContentSHA256: ref.ContentSHA256}) {
				return ErrInput
			}
		}
	}
	return nil
}
