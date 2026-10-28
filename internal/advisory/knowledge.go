package advisory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"
)

const (
	maxScopeFieldBytes = 128
	maxGapReasonBytes  = 64
	maxStateGaps       = 64
	stateBudget        = jev.MaxStateBytes - 4*1024
)

var (
	labelPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

	urlPattern       = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`)
	emailPattern     = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	phonePattern     = regexp.MustCompile(`(?:\+?86[- ]?)?1[3-9][0-9]{9}`)
	datePattern      = regexp.MustCompile(`(?:19|20)[0-9]{2}\s*[-/.年]\s*(?:0?[1-9]|1[0-2])\s*[-/.月]\s*(?:0?[1-9]|[12][0-9]|3[01])\s*日?`)
	amountPattern    = regexp.MustCompile(`[0-9][0-9,]*(?:\.[0-9]+)?\s*(?:万元|亿元|万|亿)?\s*(?:元|美元)`)
	longDigitPattern = regexp.MustCompile(`\b[0-9]{6,}\b`)
	longTokenPattern = regexp.MustCompile(`\b[A-Za-z0-9_\-]{24,}\b`)
)

type knowledgeState struct {
	Feature            string                    `json:"feature"`
	QuestionSetVersion string                    `json:"question_set_version"`
	Scope              knowledgeScope            `json:"scope"`
	Gaps               []knowledgeStateGap       `json:"gaps"`
	Candidates         []knowledgeStateCandidate `json:"candidates"`
}

type knowledgeScope struct {
	BusinessLine string `json:"business_line,omitempty"`
	Scenario     string `json:"scenario,omitempty"`
	ContractType string `json:"contract_type,omitempty"`
}

type knowledgeStateGap struct {
	Domain     string `json:"domain"`
	Capability string `json:"capability"`
	Reason     string `json:"reason,omitempty"`
}

type knowledgeStateCandidate struct {
	Alias           string `json:"id"`
	Kind            string `json:"kind"`
	Summary         string `json:"summary,omitempty"`
	CheckAction     string `json:"check_action,omitempty"`
	MinimumEvidence string `json:"minimum_evidence,omitempty"`
}

type knowledgeQuestion struct {
	Alias        string `json:"alias"`
	QuestionID   string `json:"question_id"`
	Instructions string `json:"instructions"`
	True         string `json:"true"`
	False        string `json:"false"`
}

type knowledgeIdentity struct {
	Feature            string              `json:"feature"`
	QuestionSetVersion string              `json:"question_set_version"`
	Egress             string              `json:"egress"`
	Model              string              `json:"model"`
	State              string              `json:"state"`
	Questions          []knowledgeQuestion `json:"questions"`
	ThresholdHigh      float64             `json:"threshold_high"`
	ThresholdLow       float64             `json:"threshold_low"`
}

// BuildKnowledgeRequest turns deterministic optional candidates into one
// bounded, minimized System One batch. It never receives contract text: the
// scope, gap labels and governed descriptive fields are the entire egress.
func BuildKnowledgeRequest(configuration agentflow.AdvisoryConfiguration, scope KnowledgeScope, gaps []KnowledgeGap, candidates []KnowledgeCandidate) (KnowledgeRequest, error) {
	if err := configuration.Validate(); err != nil || !configuration.Supports(FeatureKnowledgeRerank) ||
		configuration.Egress != EgressMinimized {
		return KnowledgeRequest{}, fmt.Errorf("%w: frozen advisory configuration cannot build a request", ErrNotApplicable)
	}
	stateGaps := buildGaps(gaps)
	if len(stateGaps) == 0 {
		return KnowledgeRequest{}, ErrNotApplicable
	}
	stateScope := knowledgeScope{
		BusinessLine: minimizedField(scope.BusinessLine, maxScopeFieldBytes),
		Scenario:     minimizedField(scope.Scenario, maxScopeFieldBytes),
		ContractType: minimizedField(scope.ContractType, maxScopeFieldBytes),
	}
	included := make([]KnowledgeCandidate, 0, min(len(candidates), configuration.MaxCandidates))
	omitted := make([]KnowledgeOmission, 0)
	truncated := false
	for index, candidate := range candidates {
		alias := fmt.Sprintf("candidate_%d", index)
		if index >= configuration.MaxCandidates {
			truncated = true
			break
		}
		prepared, reason := minimizeCandidate(alias, candidate)
		if reason != "" {
			omitted = append(omitted, KnowledgeOmission{Alias: alias, Reason: reason})
			continue
		}
		trial := append(append([]KnowledgeCandidate(nil), included...), prepared)
		encoded, err := encodeState(stateScope, stateGaps, trial)
		if err != nil {
			return KnowledgeRequest{}, fmt.Errorf("%w: advisory state cannot be encoded", ErrNotApplicable)
		}
		if len(encoded) > stateBudget {
			omitted = append(omitted, KnowledgeOmission{Alias: alias, Reason: "state_budget"})
			truncated = true
			continue
		}
		included = trial
	}
	if len(included) == 0 {
		return KnowledgeRequest{}, ErrNotApplicable
	}
	state := mustEncodeState(stateScope, stateGaps, included)
	questions := make(map[string]jev.NoulQuestion, len(included))
	canonicalQuestions := make([]knowledgeQuestion, 0, len(included))
	for _, candidate := range included {
		questionID := candidate.Alias + "_relevance"
		instruction := "Does the governance item in `candidates` whose `id` is `" + candidate.Alias +
			"` directly regulate the capability and scenario named in `gaps`?"
		trueCriterion := "It directly states a check, requirement, or exception that applies to that capability in this business line and scenario."
		falseCriterion := "It addresses a different capability, only cites related material, or its stated scope excludes this contract."
		questions[questionID] = jev.NoulQuestion{
			Instructions: instruction,
			Criteria:     jev.NoulCriteria{True: trueCriterion, False: falseCriterion},
		}
		canonicalQuestions = append(canonicalQuestions, knowledgeQuestion{
			Alias: candidate.Alias, QuestionID: questionID, Instructions: instruction,
			True: trueCriterion, False: falseCriterion,
		})
	}
	sort.Slice(canonicalQuestions, func(i, j int) bool { return canonicalQuestions[i].Alias < canonicalQuestions[j].Alias })
	identity := knowledgeIdentity{
		Feature: FeatureKnowledgeRerank, QuestionSetVersion: QuestionSetVersion,
		Egress: EgressMinimized, Model: configuration.Model, State: state,
		Questions: canonicalQuestions, ThresholdHigh: configuration.ThresholdHigh,
		ThresholdLow: configuration.ThresholdLow,
	}
	encodedIdentity, err := json.Marshal(identity)
	if err != nil {
		return KnowledgeRequest{}, fmt.Errorf("%w: advisory request identity cannot be encoded", ErrNotApplicable)
	}
	return KnowledgeRequest{
		Feature: FeatureKnowledgeRerank, QuestionSetVersion: QuestionSetVersion,
		Egress: EgressMinimized, Model: configuration.Model, State: state,
		StateSHA256: digest(state), Questions: questions,
		Candidates: included, Omitted: omitted, Truncated: truncated,
		RequestSHA256: digestBytes(encodedIdentity),
	}, nil
}

func buildGaps(gaps []KnowledgeGap) []knowledgeStateGap {
	seen := map[string]bool{}
	out := make([]knowledgeStateGap, 0, len(gaps))
	for _, gap := range gaps {
		domain := strings.TrimSpace(gap.Domain)
		capability := strings.TrimSpace(gap.Capability)
		if !labelPattern.MatchString(domain) || !labelPattern.MatchString(capability) {
			continue
		}
		reason := minimizedField(gap.Reason, maxGapReasonBytes)
		if reason != "" && !labelPattern.MatchString(reason) {
			reason = ""
		}
		key := domain + "\x00" + capability + "\x00" + reason
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, knowledgeStateGap{Domain: domain, Capability: capability, Reason: reason})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		if out[i].Capability != out[j].Capability {
			return out[i].Capability < out[j].Capability
		}
		return out[i].Reason < out[j].Reason
	})
	if len(out) > maxStateGaps {
		out = out[:maxStateGaps]
	}
	return out
}

func minimizeCandidate(alias string, candidate KnowledgeCandidate) (KnowledgeCandidate, string) {
	kind := strings.TrimSpace(candidate.Kind)
	if kind != "guidance" && kind != "experience" && kind != "exception" {
		return KnowledgeCandidate{}, "invalid_kind"
	}
	prepared := KnowledgeCandidate{
		Alias: alias, Kind: kind,
		Summary:         minimizedField(candidate.Summary, agentflow.PlannerKnowledgeFieldBytes),
		CheckAction:     minimizedField(candidate.CheckAction, agentflow.PlannerKnowledgeFieldBytes),
		MinimumEvidence: minimizedField(candidate.MinimumEvidence, agentflow.PlannerKnowledgeFieldBytes),
	}
	// The minimizedField helper returns an empty string both for an empty input
	// and for an oversized or invalid one. Distinguish the latter first so a
	// long field is not silently mistaken for an absent one.
	for _, field := range []string{candidate.Summary, candidate.CheckAction, candidate.MinimumEvidence} {
		if strings.TrimSpace(field) != "" && minimizedField(field, agentflow.PlannerKnowledgeFieldBytes) == "" {
			return KnowledgeCandidate{}, "field_too_long"
		}
	}
	if prepared.Summary == "" && prepared.CheckAction == "" && prepared.MinimumEvidence == "" {
		return KnowledgeCandidate{}, "empty"
	}
	return prepared, ""
}

// minimizedField applies the allowlisted redactions before the byte cap. It
// returns empty for invalid or oversized input; the caller omits the candidate
// rather than sending a half sentence that Jev cannot judge.
func minimizedField(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return ""
	}
	value = strings.Join(strings.Fields(value), " ")
	value = urlPattern.ReplaceAllString(value, "[url]")
	value = emailPattern.ReplaceAllString(value, "[email]")
	value = phonePattern.ReplaceAllString(value, "[phone]")
	value = datePattern.ReplaceAllString(value, "[date]")
	value = amountPattern.ReplaceAllString(value, "[amount]")
	value = longTokenPattern.ReplaceAllString(value, "[identifier]")
	value = longDigitPattern.ReplaceAllString(value, "[number]")
	if len(value) > limit {
		return ""
	}
	return value
}

func encodeState(scope knowledgeScope, gaps []knowledgeStateGap, candidates []KnowledgeCandidate) ([]byte, error) {
	state := knowledgeState{
		Feature: FeatureKnowledgeRerank, QuestionSetVersion: QuestionSetVersion,
		Scope: scope, Gaps: gaps,
		Candidates: make([]knowledgeStateCandidate, 0, len(candidates)),
	}
	for _, candidate := range candidates {
		state.Candidates = append(state.Candidates, knowledgeStateCandidate{
			Alias: candidate.Alias, Kind: candidate.Kind, Summary: candidate.Summary,
			CheckAction: candidate.CheckAction, MinimumEvidence: candidate.MinimumEvidence,
		})
	}
	return json.Marshal(state)
}

func mustEncodeState(scope knowledgeScope, gaps []knowledgeStateGap, candidates []KnowledgeCandidate) string {
	encoded, err := encodeState(scope, gaps, candidates)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// ValidateRequestShape is the pure boundary used by the gateway before it
// spends a provider call. It rejects a body that is not one minimized batch of
// bounded Noul questions.
func ValidateRequestShape(request ProviderRequest) error {
	if request.Feature != FeatureKnowledgeRerank || request.Egress != EgressMinimized {
		return errors.New("advisory feature or classification is unsupported")
	}
	if request.Model == "" || len(request.State) > jev.MaxStateBytes {
		return errors.New("advisory model or state is invalid")
	}
	return jev.ValidateNoulInput(request.State, request.Questions)
}
