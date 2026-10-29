package agentflow

import (
	"slices"
	"unicode/utf8"
)

// A validation owns its counters and consumes only its private reference map.
// The request and its frozen selection record are never modified.
type knowledgeSelectionValidation struct {
	request                                                Request
	record                                                 *KnowledgeSelectionRecord
	selected                                               map[KnowledgeIdentity]KnowledgeReference
	contentBytes, required, optionalSelected               int
	optionalCandidates, optionalOmitted, acceptanceOmitted int
	policyTruncated, policyNeedsVerification               bool
}

func validateSelectionRecord(request Request) error {
	if request.KnowledgeSelection == nil {
		return nil
	}
	v := knowledgeSelectionValidation{request: request, record: request.KnowledgeSelection,
		selected: make(map[KnowledgeIdentity]KnowledgeReference, len(request.Knowledge))}
	checks := []func() bool{v.validShape, v.validInput, v.validLimits, v.collectReferences,
		v.validCandidates, v.validAdvisory, v.validTotals, v.validPolicy, v.validPolicyGaps}
	for _, check := range checks {
		if !check() {
			return ErrInput
		}
	}
	return nil
}

func (v *knowledgeSelectionValidation) validShape() bool {
	r := v.record
	return r.Algorithm == "chinese-bigram-label/v1" && r.Candidates != nil &&
		len(r.Candidates) <= MaxKnowledgeRecords && r.Input.Gaps != nil && r.Input.Evidence != nil &&
		validInitialKnowledgeTrace(r) && optionalKnowledgeDigest(r.SnapshotSHA256)
}

func validInitialKnowledgeTrace(r *KnowledgeSelectionRecord) bool {
	return r.PlannerRounds != nil && len(r.PlannerRounds) == 0 && r.Reads != nil && len(r.Reads) == 0
}

func optionalKnowledgeDigest(value string) bool {
	return value == "" || normalizedDigest.MatchString(value)
}

func (v *knowledgeSelectionValidation) validInput() bool {
	input := v.record.Input
	for _, value := range []string{input.BusinessLine, input.Scenario, input.ContractType} {
		if len(value) > 128 || !utf8.ValidString(value) {
			return false
		}
	}
	return v.validGaps() && v.validEvidence()
}

func (v *knowledgeSelectionValidation) validGaps() bool {
	if len(v.record.Input.Gaps) != len(v.request.Gaps) {
		return false
	}
	gaps := make(map[string]Gap, len(v.request.Gaps))
	for _, gap := range v.request.Gaps {
		gaps[gap.ID] = gap
	}
	for _, input := range v.record.Input.Gaps {
		gap, ok := gaps[input.GapID]
		if !ok || input != (KnowledgeInputGap{GapID: gap.ID, Domain: gap.Domain, Capability: gap.Capability, Question: gap.Question}) {
			return false
		}
		delete(gaps, input.GapID)
	}
	return true
}

func (v *knowledgeSelectionValidation) validEvidence() bool {
	if len(v.record.Input.Evidence) != len(v.request.Evidence) {
		return false
	}
	evidence := make(map[string]Evidence, len(v.request.Evidence))
	for _, item := range v.request.Evidence {
		evidence[item.ID] = item
	}
	for _, input := range v.record.Input.Evidence {
		item, ok := evidence[input.EvidenceID]
		total := utf8.RuneCountInString(item.Text)
		expected := KnowledgeInputEvidence{EvidenceID: item.ID, Role: item.Role, SHA256: item.SHA256,
			TotalRunes: total, ScannedRunes: min(total, v.record.Budget.EvidenceRunesPerDocument)}
		if !ok || input != expected {
			return false
		}
		delete(evidence, input.EvidenceID)
	}
	return true
}

func (v *knowledgeSelectionValidation) validLimits() bool {
	b := v.record.Budget
	expected := b
	expected.ReferenceLimit = PlannerKnowledgeReferenceBudget
	expected.FieldBytes = PlannerKnowledgeFieldBytes
	expected.EvidenceRunesPerDocument = 4096
	expected.ReadCallsLimit = KnowledgeReadMaxCalls
	expected.ReadMaxRunes = KnowledgeReadMaxRunes
	expected.ReadMaxBytes = KnowledgeReadMaxBytes
	expected.PriorReadCalls = v.request.PriorKnowledgeReadCount
	expected.PriorReadReservedBytes = b.PriorReadCalls * KnowledgeReadMaxBytes
	return b == expected && validKnowledgeContentBudget(b) && b.PriorReadCalls >= 0 && b.PriorReadCalls <= KnowledgeReadMaxCalls
}

func validKnowledgeContentBudget(b KnowledgeSelectionBudget) bool {
	return b.ContentBytesLimit >= 0 && b.ContentBytesLimit <= MaxPlannerEvidenceBytes &&
		b.ContentBytesUsed >= 0 && b.ContentBytesUsed <= b.ContentBytesLimit
}

func (v *knowledgeSelectionValidation) collectReferences() bool {
	for _, ref := range v.request.Knowledge {
		v.selected[referenceKnowledgeIdentity(ref)] = ref
		if ref.Required {
			v.required++
			v.policyTruncated = v.policyTruncated || ref.Truncated
			v.policyNeedsVerification = v.policyNeedsVerification || ref.RequiresFactEvaluation
		} else {
			v.optionalSelected++
		}
		if !v.collectReferenceContent(ref) {
			return false
		}
	}
	return true
}

func (v *knowledgeSelectionValidation) collectReferenceContent(ref KnowledgeReference) bool {
	for _, value := range []string{ref.Summary, ref.Applicability, ref.Exclusions, ref.CheckAction,
		ref.MinimumEvidence, ref.Counterexamples, ref.SemanticChecks, ref.SourceSpans} {
		v.contentBytes += len(value)
		if len(value) > PlannerKnowledgeFieldBytes {
			return false
		}
	}
	return true
}

func referenceKnowledgeIdentity(ref KnowledgeReference) KnowledgeIdentity {
	return KnowledgeIdentity{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID, ContentSHA256: ref.ContentSHA256}
}

func candidateKnowledgeIdentity(candidate KnowledgeCandidate) KnowledgeIdentity {
	return KnowledgeIdentity{KnowledgeID: candidate.KnowledgeID, VersionID: candidate.VersionID, ContentSHA256: candidate.ContentSHA256}
}

func validKnowledgeIdentity(key KnowledgeIdentity) bool {
	return boundedIdentity(key.KnowledgeID) && boundedIdentity(key.VersionID) && normalizedDigest.MatchString(key.ContentSHA256)
}

type knowledgeCandidateRule struct {
	required                      bool
	decision                      string
	kinds                         []string
	minimumScore                  int
	zeroScore, untruncated        bool
	optional, omitted, acceptance bool
}

var knowledgeCandidateRules = map[string]knowledgeCandidateRule{
	"required_policy":            {required: true, decision: "selected", kinds: []string{"policy"}},
	"relevance":                  {decision: "selected", kinds: []string{"guidance", "experience", "exception"}, minimumScore: 1, optional: true},
	"no_relevance":               {decision: "omitted", kinds: []string{"guidance", "experience", "exception"}, zeroScore: true, untruncated: true, optional: true, omitted: true},
	"planner_budget":             {decision: "omitted", kinds: []string{"guidance", "experience", "exception"}, minimumScore: 1, untruncated: true, optional: true, omitted: true},
	"acceptance_optional_budget": {decision: "omitted", kinds: []string{""}, zeroScore: true, untruncated: true, acceptance: true},
}

func (rule knowledgeCandidateRule) matches(candidate KnowledgeCandidate) bool {
	return candidate.Required == rule.required && candidate.Decision == rule.decision &&
		slices.Contains(rule.kinds, candidate.Kind) && rule.validScore(candidate.Score) &&
		(!rule.untruncated || !candidate.Truncated)
}

func (rule knowledgeCandidateRule) validScore(score int) bool {
	if rule.zeroScore {
		return score == 0
	}
	return score >= rule.minimumScore
}

func (v *knowledgeSelectionValidation) validCandidates() bool {
	seen := make(map[KnowledgeIdentity]bool, len(v.record.Candidates))
	for _, candidate := range v.record.Candidates {
		key := candidateKnowledgeIdentity(candidate)
		if !validKnowledgeIdentity(key) || seen[key] || !v.matchCandidate(candidate, key) {
			return false
		}
		seen[key] = true
	}
	return true
}

func (v *knowledgeSelectionValidation) matchCandidate(candidate KnowledgeCandidate, key KnowledgeIdentity) bool {
	rule, ok := knowledgeCandidateRules[candidate.Reason]
	if rule.optional {
		v.optionalCandidates++
	}
	if rule.omitted {
		v.optionalOmitted++
	}
	if rule.acceptance {
		v.acceptanceOmitted++
	}
	return ok && rule.matches(candidate) && v.matchReference(candidate, key)
}

func (v *knowledgeSelectionValidation) matchReference(candidate KnowledgeCandidate, key KnowledgeIdentity) bool {
	ref, ok := v.selected[key]
	if candidate.Decision != "selected" {
		return !ok
	}
	delete(v.selected, key)
	return ok && candidateMatchesReference(candidate, ref)
}

func candidateMatchesReference(candidate KnowledgeCandidate, ref KnowledgeReference) bool {
	return ref.Kind == candidate.Kind && ref.Required == candidate.Required &&
		ref.Truncated == candidate.Truncated && ref.RequiresFactEvaluation == candidate.RequiresFactEvaluation
}

func (v *knowledgeSelectionValidation) validAdvisory() bool {
	return validateKnowledgeAdvisory(v.record, v.request) == nil
}

func (v *knowledgeSelectionValidation) validTotals() bool {
	b := v.record.Budget
	expected := b
	expected.RequiredCount = v.required
	expected.OptionalCandidates = v.optionalCandidates
	expected.OptionalSelected = v.optionalSelected
	expected.OptionalOmitted = v.optionalOmitted
	expected.AcceptanceOmitted = v.acceptanceOmitted
	expected.AcceptanceTruncated = v.acceptanceOmitted > 0
	expected.ContentBytesUsed = v.contentBytes
	return len(v.selected) == 0 && b == expected &&
		(v.optionalSelected == 0 || len(v.request.Knowledge) <= PlannerKnowledgeReferenceBudget)
}

func (v *knowledgeSelectionValidation) validPolicy() bool {
	status, reason := "unknown", ""
	switch {
	case v.record.SnapshotSHA256 == "":
		if v.required != 0 || len(v.record.Candidates) != 0 {
			return false
		}
		reason = "policy_snapshot_missing"
	case v.required == 0:
		reason = "policy_missing"
	case v.policyTruncated:
		reason = "policy_context_truncated"
	case v.policyNeedsVerification:
		reason = "policy_semantic_verification_required"
	default:
		status = "available"
	}
	return v.record.PolicyStatus == status && v.record.PolicyReason == reason
}

func (v *knowledgeSelectionValidation) validPolicyGaps() bool {
	for _, check := range knowledgePolicyChecks(v.record) {
		for _, gap := range v.request.Gaps {
			if check.GapID == gap.ID {
				return false
			}
		}
	}
	return true
}
