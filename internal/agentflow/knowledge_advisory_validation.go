package agentflow

import (
	"cmp"
	"slices"
	"unicode/utf8"
)

func validateKnowledgeAdvisory(record *KnowledgeSelectionRecord, request Request) error {
	advisory := record.Advisory
	if advisory == nil {
		for _, candidate := range record.Candidates {
			if candidate.Advisory != nil {
				return ErrInput
			}
		}
		return nil
	}
	if !validAdvisoryMetadata(advisory) {
		return ErrInput
	}
	ranks, optionalCount := advisoryCandidateRanks(record.Candidates)
	if err := validateAdvisoryCandidates(record.Candidates, advisory, ranks, optionalCount); err != nil {
		return err
	}
	return validateCompletedAdvisoryOrder(record, advisory, request)
}

type advisoryCandidateRank struct {
	detRank, finalRank int
}

func advisoryRankedCandidate(candidate KnowledgeCandidate) bool {
	return !candidate.Required && candidate.Score > 0 &&
		slices.Contains([]string{"relevance", "planner_budget"}, candidate.Reason)
}

func compareAdvisoryCandidates(left, right KnowledgeCandidate) int {
	return cmp.Or(cmp.Compare(right.Score, left.Score), cmp.Compare(left.KnowledgeID, right.KnowledgeID),
		cmp.Compare(left.VersionID, right.VersionID), cmp.Compare(left.ContentSHA256, right.ContentSHA256))
}

func advisoryCandidateRanks(candidates []KnowledgeCandidate) (map[KnowledgeIdentity]advisoryCandidateRank, int) {
	ranks := map[KnowledgeIdentity]advisoryCandidateRank{}
	optional := []KnowledgeCandidate{}
	for _, candidate := range candidates {
		if advisoryRankedCandidate(candidate) {
			optional = append(optional, candidate)
			ranks[candidateKnowledgeIdentity(candidate)] = advisoryCandidateRank{finalRank: len(optional)}
		}
	}
	slices.SortStableFunc(optional, compareAdvisoryCandidates)
	for index, candidate := range optional {
		key := candidateKnowledgeIdentity(candidate)
		item := ranks[key]
		item.detRank = index + 1
		ranks[key] = item
	}
	return ranks, len(optional)
}

func validAdvisoryMetadata(advisory *KnowledgeAdvisoryRecord) bool {
	return validAdvisoryDescriptor(advisory) && validAdvisoryEnvelope(advisory) &&
		validAdvisoryOptionalIdentities(advisory) && validAdvisoryCompletion(advisory)
}

func validAdvisoryDescriptor(advisory *KnowledgeAdvisoryRecord) bool {
	return advisory.Feature == AdvisoryFeatureKnowledgeRerank &&
		slices.Contains([]string{AdvisoryModeAdvisory, AdvisoryModeFixture}, advisory.Mode) &&
		slices.Contains([]string{AdvisoryProviderGateway, AdvisoryProviderFixture, AdvisoryProviderDirect}, advisory.Provider) &&
		advisory.QuestionSetVersion == AdvisoryQuestionSetVersion && advisory.Egress == AdvisoryEgressMinimized &&
		normalizedDigest.MatchString(advisory.ConfigurationID)
}

func validAdvisoryEnvelope(advisory *KnowledgeAdvisoryRecord) bool {
	return slices.Contains(advisoryStatuses, advisory.Status) && advisory.Candidates != nil &&
		len(advisory.Candidates) <= MaxAdvisoryMaxCandidates && optionalAdvisoryID(advisory.ErrorCode)
}

func optionalAdvisoryID(value string) bool {
	return value == "" || capabilityID.MatchString(value)
}

func validAdvisoryOptionalIdentities(advisory *KnowledgeAdvisoryRecord) bool {
	return optionalAdvisoryID(advisory.ModelRequested) &&
		optionalKnowledgeDigest(advisory.RequestSHA256) && optionalKnowledgeDigest(advisory.JudgmentID)
}

func validAdvisoryCompletion(advisory *KnowledgeAdvisoryRecord) bool {
	if advisory.Status == "completed" {
		return advisory.ModelRequested != "" && advisory.ModelObserved == advisory.ModelRequested &&
			advisory.RequestSHA256 != "" && advisory.JudgmentID != ""
	}
	return advisory.ModelObserved == "" && failedAdvisoryHasNoJudgments(advisory.Candidates)
}

func failedAdvisoryHasNoJudgments(candidates []KnowledgeAdvisoryCandidate) bool {
	for _, item := range candidates {
		if item.Probability != nil || item.Band != "unknown" {
			return false
		}
	}
	return true
}

func advisoryKnowledgeIdentity(item KnowledgeAdvisoryCandidate) KnowledgeIdentity {
	return KnowledgeIdentity{KnowledgeID: item.KnowledgeID, VersionID: item.VersionID, ContentSHA256: item.ContentSHA256}
}

func validAdvisoryCandidateFields(item KnowledgeAdvisoryCandidate, count int) bool {
	return validKnowledgeIdentity(advisoryKnowledgeIdentity(item)) && capabilityID.MatchString(item.Alias) &&
		slices.Contains(advisoryBands, item.Band) && validAdvisoryRankBounds(item, count) &&
		len(item.Reason) <= 64 && utf8.ValidString(item.Reason)
}

func validAdvisoryRankBounds(item KnowledgeAdvisoryCandidate, count int) bool {
	return item.RankBefore >= 1 && item.RankAfter >= 1 && item.RankBefore <= count && item.RankAfter <= count
}

func indexAdvisoryCandidates(items []KnowledgeAdvisoryCandidate, count int) (map[KnowledgeIdentity]KnowledgeAdvisoryCandidate, error) {
	indexed := make(map[KnowledgeIdentity]KnowledgeAdvisoryCandidate, len(items))
	for _, item := range items {
		key := advisoryKnowledgeIdentity(item)
		if !validAdvisoryCandidateFields(item, count) {
			return nil, ErrInput
		}
		if _, duplicate := indexed[key]; duplicate {
			return nil, ErrInput
		}
		indexed[key] = item
	}
	return indexed, nil
}

func validateAdvisoryCandidates(candidates []KnowledgeCandidate, advisory *KnowledgeAdvisoryRecord, ranks map[KnowledgeIdentity]advisoryCandidateRank, optionalCount int) error {
	indexed, err := indexAdvisoryCandidates(advisory.Candidates, optionalCount)
	if err != nil {
		return err
	}
	matched := 0
	for _, candidate := range candidates {
		key := candidateKnowledgeIdentity(candidate)
		item, exists := indexed[key]
		if !matchesCandidateAdvisory(candidate, item, exists, ranks[key]) {
			return ErrInput
		}
		if candidate.Advisory != nil {
			matched++
		}
	}
	if matched != len(advisory.Candidates) || (advisory.Status == "completed" && matched == 0) {
		return ErrInput
	}
	return nil
}

func matchesCandidateAdvisory(candidate KnowledgeCandidate, item KnowledgeAdvisoryCandidate, exists bool, rank advisoryCandidateRank) bool {
	if candidate.Advisory == nil {
		return !exists
	}
	return exists && advisoryRankedCandidate(candidate) &&
		slices.Contains([]string{"guidance", "experience", "exception"}, candidate.Kind) &&
		matchingAdvisoryMetadata(item, *candidate.Advisory, rank) &&
		matchingAdvisoryProbability(item, *candidate.Advisory)
}

func matchingAdvisoryMetadata(item, attached KnowledgeAdvisoryCandidate, rank advisoryCandidateRank) bool {
	return item.Alias == attached.Alias && item.Band == attached.Band && item.RankBefore == attached.RankBefore &&
		item.RankAfter == attached.RankAfter && item.RankBefore == rank.detRank && item.RankAfter == rank.finalRank
}

func matchingAdvisoryProbability(item, attached KnowledgeAdvisoryCandidate) bool {
	if item.Probability == nil {
		return attached.Probability == nil && item.Band == "unknown"
	}
	if attached.Probability == nil {
		return false
	}
	value := *item.Probability
	// The interval rejects both infinities and NaN as well as out-of-range values.
	return value >= 0 && value <= 1 && *attached.Probability == value
}

func validateCompletedAdvisoryOrder(record *KnowledgeSelectionRecord, advisory *KnowledgeAdvisoryRecord, request Request) error {
	if advisory.Status != "completed" {
		return nil
	}
	selected := []KnowledgeIdentity{}
	for _, candidate := range record.Candidates {
		if candidate.Decision == "selected" {
			selected = append(selected, candidateKnowledgeIdentity(candidate))
		}
	}
	if len(selected) != len(request.Knowledge) {
		return ErrInput
	}
	for index, ref := range request.Knowledge {
		if selected[index] != referenceKnowledgeIdentity(ref) {
			return ErrInput
		}
	}
	return nil
}
