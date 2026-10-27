package store

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"contractaudit/internal/agentflow"
)

const (
	governedPlannerAlgorithm     = "chinese-bigram-label/v1"
	governedPlannerEvidenceRunes = 4096
)

// governedPlannerKnowledge operates only on the copied, reauthorized acceptance
// snapshot and the verified frozen review inputs. It never fetches newer versions.
func governedPlannerKnowledge(snapshot *ReviewKnowledgeSnapshot, gaps []agentflow.Gap, evidence []agentflow.Evidence, maxEvidenceBytes int) ([]agentflow.KnowledgeReference, *agentflow.KnowledgeSelectionRecord, error) {
	record := &agentflow.KnowledgeSelectionRecord{
		Algorithm: governedPlannerAlgorithm, PolicyStatus: "unknown", PolicyReason: "policy_snapshot_missing",
		Input:      agentflow.KnowledgeSelectionInput{Gaps: []agentflow.KnowledgeInputGap{}, Evidence: []agentflow.KnowledgeInputEvidence{}},
		Candidates: []agentflow.KnowledgeCandidate{}, PlannerRounds: []agentflow.KnowledgePlanningRound{}, Reads: []agentflow.KnowledgeReadRecord{},
		Budget: agentflow.KnowledgeSelectionBudget{ReferenceLimit: agentflow.PlannerKnowledgeReferenceBudget,
			FieldBytes: agentflow.PlannerKnowledgeFieldBytes, EvidenceRunesPerDocument: governedPlannerEvidenceRunes,
			ReadCallsLimit: agentflow.KnowledgeReadMaxCalls, ReadMaxRunes: agentflow.KnowledgeReadMaxRunes, ReadMaxBytes: agentflow.KnowledgeReadMaxBytes},
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
	refs := make([]agentflow.KnowledgeReference, 0, len(policies)+min(len(optional), agentflow.PlannerKnowledgeReferenceBudget))
	remaining := maxEvidenceBytes
	appendSelected := func(item ranked, reason string) {
		ref := plannerKnowledgeReference(item.selection, &remaining)
		refs = append(refs, ref)
		record.Candidates = append(record.Candidates, agentflow.KnowledgeCandidate{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID,
			ContentSHA256: ref.ContentSHA256, Kind: ref.Kind, Required: ref.Required, Score: item.score,
			Decision: "selected", Reason: reason, Truncated: ref.Truncated, RequiresFactEvaluation: ref.RequiresFactEvaluation})
	}
	for _, item := range policies {
		appendSelected(item, "required_policy")
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
	for _, item := range optional {
		reason := "relevance"
		if item.score == 0 {
			reason = "no_relevance"
		} else if len(refs) >= agentflow.PlannerKnowledgeReferenceBudget {
			reason = "planner_budget"
		}
		if reason == "relevance" {
			appendSelected(item, reason)
			record.Budget.OptionalSelected++
		} else {
			ref := item.selection.Ref
			record.Candidates = append(record.Candidates, agentflow.KnowledgeCandidate{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID,
				ContentSHA256: ref.ContentSHA256, Kind: item.selection.Kind, Score: item.score, Decision: "omitted", Reason: reason, RequiresFactEvaluation: item.selection.RequiresFactEvaluation})
			record.Budget.OptionalOmitted++
		}
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
