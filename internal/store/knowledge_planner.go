package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"contractaudit/internal/advisory"
	"contractaudit/internal/agentflow"
)

const (
	governedPlannerAlgorithm     = "chinese-bigram-label/v1"
	governedPlannerEvidenceRunes = 4096
)

// governedPlannerKnowledge operates only on the copied, reauthorized acceptance
// snapshot and the verified frozen review inputs. It never fetches newer versions.
// advisoryOutcome is one reusable or freshly persisted judgment batch. The
// store evaluator owns persistence; this planner only applies bands.
type advisoryOutcome struct {
	Result     advisory.KnowledgeResult
	JudgmentID string
	Cached     bool
}

type advisoryEvaluator interface {
	Evaluate(context.Context, advisory.KnowledgeRequest) (advisoryOutcome, error)
}

// governedPlannerKnowledge is the deterministic baseline. It is kept as the
// exact no-Jev entry point so tests and fallback paths can compare bytes.
func governedPlannerKnowledge(snapshot *ReviewKnowledgeSnapshot, gaps []agentflow.Gap, evidence []agentflow.Evidence, maxEvidenceBytes int) ([]agentflow.KnowledgeReference, *agentflow.KnowledgeSelectionRecord, error) {
	return governedPlannerKnowledgeWithAdvisory(context.Background(), snapshot, gaps, evidence, maxEvidenceBytes, nil, "", nil)
}

// governedPlannerKnowledgeWithAdvisory selects required policies first and
// optional candidates by the existing deterministic budget. A completed Jev
// batch may change only the optional ordering; it cannot change scores, the
// candidate set, required policies or the reference budget.
func governedPlannerKnowledgeWithAdvisory(ctx context.Context, snapshot *ReviewKnowledgeSnapshot, gaps []agentflow.Gap, evidence []agentflow.Evidence, maxEvidenceBytes int, advisoryConfiguration *agentflow.AdvisoryConfiguration, plannerConfigurationID string, evaluator advisoryEvaluator) ([]agentflow.KnowledgeReference, *agentflow.KnowledgeSelectionRecord, error) {
	record := &agentflow.KnowledgeSelectionRecord{
		Algorithm: governedPlannerAlgorithm, PolicyStatus: "unknown", PolicyReason: "policy_snapshot_missing",
		Input:      agentflow.KnowledgeSelectionInput{Gaps: []agentflow.KnowledgeInputGap{}, Evidence: []agentflow.KnowledgeInputEvidence{}},
		Candidates: []agentflow.KnowledgeCandidate{}, PlannerRounds: []agentflow.KnowledgePlanningRound{}, Reads: []agentflow.KnowledgeReadRecord{},
		Budget: agentflow.KnowledgeSelectionBudget{ReferenceLimit: agentflow.PlannerKnowledgeReferenceBudget,
			FieldBytes: agentflow.PlannerKnowledgeFieldBytes, EvidenceRunesPerDocument: governedPlannerEvidenceRunes,
			ReadCallsLimit: agentflow.KnowledgeReadMaxCalls, ReadMaxRunes: agentflow.KnowledgeReadMaxRunes, ReadMaxBytes: agentflow.KnowledgeReadMaxBytes},
	}
	if advisoryConfiguration != nil {
		record.Advisory = &agentflow.KnowledgeAdvisoryRecord{
			Mode: advisoryConfiguration.Mode, Feature: agentflow.AdvisoryFeatureKnowledgeRerank,
			Provider: advisoryConfiguration.Provider, ConfigurationID: plannerConfigurationID,
			ModelRequested: advisoryConfiguration.Model, QuestionSetVersion: advisoryConfiguration.QuestionSetVersion,
			Egress: advisoryConfiguration.Egress, Status: "not_applicable",
			Candidates: []agentflow.KnowledgeAdvisoryCandidate{},
		}
	}
	labels, query := map[string]bool{}, map[string]bool{}
	addLabel := func(value string) {
		for token := range governedWords(value) {
			labels[token] = true
			query[token] = true
		}
	}
	if snapshot != nil {
		record.SnapshotSHA256 = snapshot.SnapshotSHA256
		record.PolicyReason = "policy_missing"
		record.Input.BusinessLine, record.Input.Scenario, record.Input.ContractType = snapshot.Scope.BusinessLine, snapshot.Scope.Scenario, snapshot.Scope.ContractType
		addLabel(snapshot.Scope.BusinessLine)
		addLabel(snapshot.Scope.Scenario)
		addLabel(snapshot.Scope.ContractType)
	}
	for _, gap := range gaps {
		record.Input.Gaps = append(record.Input.Gaps, agentflow.KnowledgeInputGap{GapID: gap.ID, Domain: gap.Domain, Capability: gap.Capability, Question: gap.Question})
		addLabel(string(gap.Domain))
		addLabel(gap.Capability)
		for token := range governedWords(gap.Question) {
			query[token] = true
		}
	}
	sort.Slice(record.Input.Gaps, func(i, j int) bool { return record.Input.Gaps[i].GapID < record.Input.Gaps[j].GapID })
	for _, source := range evidence {
		maxEvidenceBytes -= len(source.Text)
		text, scanned, total := governedEvidencePrefix(source.Text, governedPlannerEvidenceRunes)
		record.Input.Evidence = append(record.Input.Evidence, agentflow.KnowledgeInputEvidence{EvidenceID: source.ID, Role: source.Role, SHA256: source.SHA256, ScannedRunes: scanned, TotalRunes: total})
		addLabel(source.Role)
		for token := range governedWords(text) {
			query[token] = true
		}
	}
	sort.Slice(record.Input.Evidence, func(i, j int) bool { return record.Input.Evidence[i].EvidenceID < record.Input.Evidence[j].EvidenceID })
	if maxEvidenceBytes < 0 {
		maxEvidenceBytes = 0
	}
	record.Budget.ContentBytesLimit = maxEvidenceBytes
	if snapshot == nil {
		return []agentflow.KnowledgeReference{}, record, nil
	}
	if len(snapshot.RequiredPolicies)+len(snapshot.OptionalCandidates)+len(snapshot.Excluded) > agentflow.MaxKnowledgeRecords {
		return nil, nil, errors.New("frozen governed knowledge exceeds the planner identity limit")
	}
	if snapshot.OptionalTruncated != (len(snapshot.Excluded) > 0) {
		return nil, nil, errors.New("frozen optional truncation is inconsistent")
	}
	record.Budget.RequiredCount = len(snapshot.RequiredPolicies)
	record.Budget.OptionalCandidates = len(snapshot.OptionalCandidates)
	record.Budget.AcceptanceOmitted = len(snapshot.Excluded)
	record.Budget.AcceptanceTruncated = snapshot.OptionalTruncated
	type ranked struct {
		selection GovernedKnowledgeSelection
		score     int
	}
	policies := make([]ranked, 0, len(snapshot.RequiredPolicies))
	optional := make([]ranked, 0, len(snapshot.OptionalCandidates))
	for _, item := range snapshot.RequiredPolicies {
		if !item.Required || item.Kind != "policy" {
			return nil, nil, errors.New("invalid required governed policy")
		}
		policies = append(policies, ranked{selection: item})
	}
	for _, item := range snapshot.OptionalCandidates {
		if item.Required || (item.Kind != "guidance" && item.Kind != "experience" && item.Kind != "exception") {
			return nil, nil, errors.New("invalid optional governed knowledge")
		}
		optional = append(optional, ranked{selection: item, score: governedRelevance(item, query, labels)})
	}
	lessIdentity := func(a, b KnowledgeRef) bool {
		if a.KnowledgeID != b.KnowledgeID {
			return a.KnowledgeID < b.KnowledgeID
		}
		if a.VersionID != b.VersionID {
			return a.VersionID < b.VersionID
		}
		return a.ContentSHA256 < b.ContentSHA256
	}
	sort.Slice(policies, func(i, j int) bool { return lessIdentity(policies[i].selection.Ref, policies[j].selection.Ref) })
	sort.Slice(optional, func(i, j int) bool {
		if optional[i].score != optional[j].score {
			return optional[i].score > optional[j].score
		}
		return lessIdentity(optional[i].selection.Ref, optional[j].selection.Ref)
	})
	deterministicRank := map[KnowledgeRef]int{}
	advisoryKeys := []KnowledgeRef{}
	for _, item := range optional {
		if item.score > 0 {
			deterministicRank[item.selection.Ref] = len(advisoryKeys) + 1
			advisoryKeys = append(advisoryKeys, item.selection.Ref)
		}
	}
	advisoryByKey := map[KnowledgeRef]agentflow.KnowledgeAdvisoryCandidate{}
	advisoryCompleted := false
	if record.Advisory != nil && len(advisoryKeys) > 0 && advisoryConfiguration.Supports(agentflow.AdvisoryFeatureKnowledgeRerank) {
		inputCandidates := make([]advisory.KnowledgeCandidate, 0, len(advisoryKeys))
		for _, key := range advisoryKeys {
			for _, item := range optional {
				if item.selection.Ref == key {
					inputCandidates = append(inputCandidates, advisory.KnowledgeCandidate{
						Kind: item.selection.Kind, Summary: item.selection.Summary,
						CheckAction: item.selection.CheckAction, MinimumEvidence: item.selection.MinimumEvidence,
					})
					break
				}
			}
		}
		scope := advisory.KnowledgeScope{BusinessLine: snapshot.Scope.BusinessLine, Scenario: snapshot.Scope.Scenario, ContractType: snapshot.Scope.ContractType}
		gapLabels := make([]advisory.KnowledgeGap, 0, len(gaps))
		for _, gap := range gaps {
			gapLabels = append(gapLabels, advisory.KnowledgeGap{Domain: string(gap.Domain), Capability: gap.Capability, Reason: gap.IncompleteReason})
		}
		request, buildErr := advisory.BuildKnowledgeRequest(*advisoryConfiguration, scope, gapLabels, inputCandidates)
		switch {
		case errors.Is(buildErr, advisory.ErrNotApplicable):
			record.Advisory.Status = "not_applicable"
		case buildErr != nil:
			record.Advisory.Status = "unavailable"
			record.Advisory.ErrorCode = "invalid_request"
		default:
			record.Advisory.RequestSHA256 = request.RequestSHA256
			record.Advisory.Truncated = request.Truncated
			record.Advisory.Status = "unavailable"
			if evaluator == nil {
				record.Advisory.ErrorCode = "provider_unavailable"
			} else if ctx != nil && ctx.Err() != nil {
				record.Advisory.ErrorCode = "cancelled"
			} else {
				outcome, evaluateErr := evaluator.Evaluate(ctx, request)
				if evaluateErr != nil {
					record.Advisory.ErrorCode = advisoryErrorCode(evaluateErr)
				} else if len(outcome.Result.Bands) != len(request.Candidates) {
					record.Advisory.ErrorCode = "invalid_response"
				} else {
					advisoryCompleted = true
					record.Advisory.Status = "completed"
					record.Advisory.ModelObserved = outcome.Result.Model
					record.Advisory.JudgmentID = outcome.JudgmentID
					for _, candidate := range request.Candidates {
						index, ok := advisoryCandidateIndex(candidate.Alias)
						if !ok || index >= len(advisoryKeys) {
							continue
						}
						value := outcome.Result.Probabilities[candidate.Alias]
						band := outcome.Result.Bands[candidate.Alias]
						advisoryByKey[advisoryKeys[index]] = agentflow.KnowledgeAdvisoryCandidate{
							KnowledgeID: advisoryKeys[index].KnowledgeID, VersionID: advisoryKeys[index].VersionID,
							ContentSHA256: advisoryKeys[index].ContentSHA256, Alias: candidate.Alias,
							Probability: &value, Band: string(band), RankBefore: deterministicRank[advisoryKeys[index]],
						}
					}
				}
			}
			// A failed or partially omitted batch still records the attempted
			// candidates as unknown; it must never look like a negative answer.
			for _, candidate := range request.Candidates {
				index, ok := advisoryCandidateIndex(candidate.Alias)
				if !ok || index >= len(advisoryKeys) {
					continue
				}
				if _, exists := advisoryByKey[advisoryKeys[index]]; exists {
					continue
				}
				advisoryByKey[advisoryKeys[index]] = agentflow.KnowledgeAdvisoryCandidate{
					KnowledgeID: advisoryKeys[index].KnowledgeID, VersionID: advisoryKeys[index].VersionID,
					ContentSHA256: advisoryKeys[index].ContentSHA256, Alias: candidate.Alias,
					Band: string(advisory.BandUnknown), RankBefore: deterministicRank[advisoryKeys[index]],
				}
			}
			for _, omission := range request.Omitted {
				index, ok := advisoryCandidateIndex(omission.Alias)
				if !ok || index >= len(advisoryKeys) {
					continue
				}
				advisoryByKey[advisoryKeys[index]] = agentflow.KnowledgeAdvisoryCandidate{
					KnowledgeID: advisoryKeys[index].KnowledgeID, VersionID: advisoryKeys[index].VersionID,
					ContentSHA256: advisoryKeys[index].ContentSHA256, Alias: omission.Alias,
					Band: string(advisory.BandUnknown), RankBefore: deterministicRank[advisoryKeys[index]],
					Reason: "advisory_" + omission.Reason,
				}
			}
		}
	}
	if advisoryCompleted {
		sort.SliceStable(optional, func(i, j int) bool {
			left, right := advisory.BandUnknown, advisory.BandUnknown
			if item, ok := advisoryByKey[optional[i].selection.Ref]; ok {
				left = advisory.Band(item.Band)
			}
			if item, ok := advisoryByKey[optional[j].selection.Ref]; ok {
				right = advisory.Band(item.Band)
			}
			return advisory.BandRank(left) < advisory.BandRank(right)
		})
	}
	refs := make([]agentflow.KnowledgeReference, 0, len(policies)+min(len(optional), agentflow.PlannerKnowledgeReferenceBudget))
	remaining := maxEvidenceBytes
	appendSelected := func(item ranked, reason string, advice *agentflow.KnowledgeAdvisoryCandidate) {
		ref := plannerKnowledgeReference(item.selection, &remaining)
		refs = append(refs, ref)
		record.Candidates = append(record.Candidates, agentflow.KnowledgeCandidate{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID,
			ContentSHA256: ref.ContentSHA256, Kind: ref.Kind, Required: ref.Required, Score: item.score,
			Decision: "selected", Reason: reason, Truncated: ref.Truncated, RequiresFactEvaluation: ref.RequiresFactEvaluation,
			Advisory: advice})
	}
	for _, item := range policies {
		appendSelected(item, "required_policy", nil)
	}
	if len(policies) > 0 {
		record.PolicyStatus, record.PolicyReason = "available", ""
	}
	for _, ref := range refs {
		if ref.RequiresFactEvaluation {
			record.PolicyStatus, record.PolicyReason = "unknown", "policy_semantic_verification_required"
		}
		if ref.Truncated {
			record.PolicyStatus, record.PolicyReason = "unknown", "policy_context_truncated"
			break
		}
	}
	finalRank := 0
	for _, item := range optional {
		if item.score > 0 {
			finalRank++
		}
		reason := "relevance"
		if item.score == 0 {
			reason = "no_relevance"
		} else if len(refs) >= agentflow.PlannerKnowledgeReferenceBudget {
			reason = "planner_budget"
		}
		var advice *agentflow.KnowledgeAdvisoryCandidate
		if value, ok := advisoryByKey[item.selection.Ref]; ok {
			value.RankAfter = finalRank
			copy := value
			advice = &copy
			advisoryByKey[item.selection.Ref] = value
		}
		if reason == "relevance" {
			appendSelected(item, reason, advice)
			record.Budget.OptionalSelected++
		} else {
			ref := item.selection.Ref
			record.Candidates = append(record.Candidates, agentflow.KnowledgeCandidate{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID,
				ContentSHA256: ref.ContentSHA256, Kind: item.selection.Kind, Score: item.score, Decision: "omitted", Reason: reason,
				RequiresFactEvaluation: item.selection.RequiresFactEvaluation, Advisory: advice})
			record.Budget.OptionalOmitted++
		}
	}
	if record.Advisory != nil && len(advisoryByKey) > 0 {
		for _, key := range advisoryKeys {
			if value, ok := advisoryByKey[key]; ok {
				record.Advisory.Candidates = append(record.Advisory.Candidates, value)
			}
		}
		sort.Slice(record.Advisory.Candidates, func(i, j int) bool {
			if record.Advisory.Candidates[i].RankAfter != record.Advisory.Candidates[j].RankAfter {
				return record.Advisory.Candidates[i].RankAfter < record.Advisory.Candidates[j].RankAfter
			}
			return record.Advisory.Candidates[i].KnowledgeID < record.Advisory.Candidates[j].KnowledgeID
		})
	}
	for _, excluded := range snapshot.Excluded {
		if excluded.Reason != "optional_budget" {
			return nil, nil, errors.New("unsupported frozen knowledge exclusion")
		}
		record.Candidates = append(record.Candidates, agentflow.KnowledgeCandidate{KnowledgeID: excluded.Ref.KnowledgeID, VersionID: excluded.Ref.VersionID,
			ContentSHA256: excluded.Ref.ContentSHA256, Decision: "omitted", Reason: "acceptance_optional_budget"})
	}
	record.Budget.ContentBytesUsed = maxEvidenceBytes - remaining
	return refs, record, nil
}

func plannerKnowledgeReference(item GovernedKnowledgeSelection, remaining *int) agentflow.KnowledgeReference {
	applicability, _ := json.Marshal(struct {
		Scope      KnowledgeScope       `json:"scope"`
		ValidFrom  string               `json:"valid_from"`
		ValidUntil string               `json:"valid_until"`
		Conditions []KnowledgeCondition `json:"conditions"`
		Reviewed   []string             `json:"reviewed"`
	}{item.Scope, item.ValidFrom, item.ValidUntil, item.Applicability, item.ReviewedApplicability})
	exclusions, _ := json.Marshal(struct {
		Conditions     []KnowledgeCondition `json:"conditions"`
		Reviewed       []string             `json:"reviewed"`
		ExceptionBasis string               `json:"exception_basis,omitempty"`
	}{item.Exclusions, item.ReviewedExclusions, item.ExceptionBasis})
	counterexamples, _ := json.Marshal(item.Counterexamples)
	semanticChecks := ""
	if len(item.SemanticOnly) > 0 {
		encoded, _ := json.Marshal(item.SemanticOnly)
		semanticChecks = string(encoded)
	}
	sourceSpans := ""
	if len(item.Binding.SourceSpans) > 0 {
		encoded, _ := json.Marshal(item.Binding.SourceSpans)
		sourceSpans = string(encoded)
	}
	ref := agentflow.KnowledgeReference{KnowledgeID: item.Ref.KnowledgeID, VersionID: item.Ref.VersionID, ContentSHA256: item.Ref.ContentSHA256,
		Kind: item.Kind, Required: item.Required, RequiresFactEvaluation: item.RequiresFactEvaluation}
	fields := []struct {
		dst   *string
		value string
	}{
		{&ref.Summary, item.Summary}, {&ref.Applicability, string(applicability)}, {&ref.Exclusions, string(exclusions)},
		{&ref.CheckAction, item.CheckAction}, {&ref.MinimumEvidence, item.MinimumEvidence}, {&ref.Counterexamples, string(counterexamples)},
		{&ref.SemanticChecks, semanticChecks}, {&ref.SourceSpans, sourceSpans},
	}
	for _, field := range fields {
		limit := min(agentflow.PlannerKnowledgeFieldBytes, *remaining)
		end := min(len(field.value), limit)
		for end > 0 && !utf8.ValidString(field.value[:end]) {
			end--
		}
		*field.dst = field.value[:end]
		*remaining -= end
		if end != len(field.value) {
			ref.Truncated = true
		}
	}
	return ref
}

// Han bigrams give deterministic word-like overlap without an external segmenter;
// ASCII labels are matched as case-folded words. Scores are not confidence.
func governedWords(text string) map[string]bool {
	out := map[string]bool{}
	var previous rune
	var ascii strings.Builder
	flush := func() {
		if ascii.Len() > 0 {
			out[ascii.String()] = true
			ascii.Reset()
		}
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			flush()
			if previous != 0 {
				out[string([]rune{previous, r})] = true
			}
			previous = r
		} else {
			previous = 0
			if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
				ascii.WriteRune(unicode.ToLower(r))
			} else {
				flush()
			}
		}
	}
	flush()
	return out
}

func governedRelevance(item GovernedKnowledgeSelection, query, labels map[string]bool) int {
	words := map[string]bool{}
	values := []string{item.Summary, item.CheckAction, item.MinimumEvidence}
	values = append(values, item.ReviewedApplicability...)
	values = append(values, item.ReviewedExclusions...)
	values = append(values, item.Counterexamples...)
	for _, value := range values {
		for word := range governedWords(value) {
			words[word] = true
		}
	}
	score := 0
	for word := range words {
		if query[word] {
			score++
			if labels[word] {
				score += 4
			}
		}
	}
	return score
}

func governedEvidencePrefix(text string, limit int) (string, int, int) {
	total := utf8.RuneCountInString(text)
	if total <= limit {
		return text, total, total
	}
	count := 0
	for index := range text {
		if count == limit {
			return text[:index], count, total
		}
		count++
	}
	return text, total, total
}

func governedKnowledgeSpan(body string, start, end int) (string, error) {
	if !utf8.ValidString(body) || start < 0 || end <= start || end-start > agentflow.KnowledgeReadMaxRunes {
		return "", errors.New("invalid governed knowledge span")
	}
	first, last, offset := -1, -1, 0
	for index := range body {
		if offset == start {
			first = index
		}
		if offset == end {
			last = index
			break
		}
		offset++
	}
	if last < 0 && offset == end {
		last = len(body)
	}
	if first < 0 || last < first || last-first > agentflow.KnowledgeReadMaxBytes {
		return "", errors.New("governed knowledge span exceeds the source or byte limit")
	}
	return body[first:last], nil
}

func advisoryCandidateIndex(alias string) (int, bool) {
	const prefix = "candidate_"
	if !strings.HasPrefix(alias, prefix) {
		return 0, false
	}
	index, err := strconv.Atoi(strings.TrimPrefix(alias, prefix))
	return index, err == nil && index >= 0
}
