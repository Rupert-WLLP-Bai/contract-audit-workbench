package decision

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
)

var (
	referencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	candidatePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,79}$`)
	ruleIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,79}$`)
	ruleHashPattern  = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	revisionPattern  = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
)

func newProvider(config Config) (*Provider, error) {
	if config.Mode == "" {
		config.Mode = ModeDisabled
	}
	switch config.Mode {
	case ModeDisabled, ModeShadow, ModeAssist:
	default:
		return nil, errors.New("invalid decision mode")
	}
	if config.ModelRevision != "" {
		if !revisionPattern.MatchString(config.ModelRevision) {
			return nil, errors.New("invalid expected Laya model revision")
		}
		config.ModelRevision = strings.ToLower(config.ModelRevision)
	}
	if config.ShadowThreshold != nil {
		if !validThreshold(*config.ShadowThreshold) {
			return nil, errors.New("invalid shadow confidence threshold")
		}
		threshold := *config.ShadowThreshold
		config.ShadowThreshold = &threshold
	}
	if config.AssistApproval != nil {
		approval := *config.AssistApproval
		config.AssistApproval = &approval
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	provider := &Provider{config: config}
	if config.Mode != ModeDisabled {
		adapter, err := NewSystemOneAdapter(config.Endpoint, config.Timeout, config.BearerToken)
		if err != nil {
			return nil, err
		}
		provider.adapter = adapter
	}
	return provider, nil
}

func (p *Provider) evaluate(ctx context.Context, input Input) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	result := Result{Status: StatusUnavailable, AcceptedRules: []string{}}
	event := p.event(input)
	finish := func(status Status, failure string) Result {
		result.Status = status
		event.Status = status
		event.FailureReason = failure
		result.Event = event
		if p.config.Observer != nil {
			p.config.Observer.ObserveDecision(ctx, event)
		}
		return result
	}

	if p.config.Mode == ModeDisabled {
		return finish(StatusDisabled, FailureNone)
	}
	policy := access.Check(access.Review, input.Principal, input.Resource, p.config.Clock())
	event.PolicyVersion = policy.PolicyVersion
	if !policy.Allowed {
		return finish(StatusDenied, FailureAuthorization)
	}
	if !validInput(input) {
		return finish(StatusInvalidInput, FailureInvalidInput)
	}
	if p.config.Mode == ModeAssist {
		if input.Capability == CapabilityChoice {
			return finish(StatusAssistUnsupported, FailureAssistUnsupported)
		}
		if !approvalMatches(p.config.AssistApproval, input) {
			return finish(StatusApprovalRequired, FailureApprovalRequired)
		}
	}
	if err := ctx.Err(); err != nil {
		if err == context.DeadlineExceeded {
			return finish(StatusUnavailable, FailureTimeout)
		}
		return finish(StatusUnavailable, FailureCanceled)
	}
	if p.adapter == nil {
		return finish(StatusUnavailable, FailureTransport)
	}

	suggestion, latency, failure := p.adapter.query(ctx, input)
	event.LatencyMS = float64(latency) / float64(time.Millisecond)
	if failure != FailureNone || suggestion == nil {
		if failure == FailureNone {
			failure = FailureInvalidAnswer
		}
		return finish(StatusUnavailable, failure)
	}
	result.Suggestion = suggestion
	event.ResponseModel = suggestion.ResponseModel
	event.RoutedCheckpoint = suggestion.RoutedCheckpoint
	event.Recommendation = suggestion.CandidateID
	event.Confidence = copyFloat(suggestion.Confidence)
	event.ConfidenceSource = suggestion.ConfidenceSource

	threshold, hasThreshold := p.threshold(input)
	abstained := suggestion.UpstreamAbstained ||
		(hasThreshold && suggestion.Confidence != nil && *suggestion.Confidence < threshold)
	if abstained {
		event.Abstention = "abstained"
		return finish(StatusAbstained, FailureNone)
	}
	if p.config.Mode == ModeShadow {
		if hasThreshold {
			event.Abstention = "not_abstained"
		} else if suggestion.UpstreamAbstained {
			event.Abstention = "abstained"
		} else {
			event.Abstention = "not_configured"
		}
		return finish(StatusSuggested, FailureNone)
	}

	// Assist only changes the order of the existing allowed non-mandatory rules.
	// The full list is returned so callers cannot interpret the suggestion as a
	// filter that skips deterministic rules.
	result.AcceptedRules = reorderedRules(input.AllowedCandidates, suggestion.CandidateID)
	event.Abstention = "not_abstained"
	return finish(StatusOrdered, FailureNone)
}

func (p *Provider) event(input Input) DecisionEvent {
	revision, source := p.config.ModelRevision, "configured_expected_pin"
	if revision == "" {
		source = "unknown"
	}
	var confidenceThreshold *float64
	if threshold, ok := p.threshold(input); ok {
		confidenceThreshold = &threshold
	}
	return DecisionEvent{
		RequestID: safeReference(input.RequestID), DecisionID: safeReference(input.DecisionID),
		RunID: safeReference(input.RunID), AttemptID: safeReference(input.AttemptID),
		TraceID: safeReference(input.TraceID), TenantID: safeReference(input.Resource.TenantID),
		CaseID: safeReference(input.Resource.CaseID), ActorID: safeReference(input.Principal.UserID),
		Capability: safeCapability(input.Capability), Mode: p.config.Mode, Status: StatusUnavailable,
		RulePackVersion: safeReference(input.RulePackVersion),
		RulePackHash:    safeHash(input.RulePackHash), ModelName: ModelAlias,
		ModelRevision: revision, ModelRevisionSource: source,
		ObservedModelRevision: nil, ResponseModel: "", RoutedCheckpoint: "",
		Recommendation: "", Confidence: nil, ConfidenceSource: "",
		ConfidenceThreshold: confidenceThreshold,
		Abstention:          "not_evaluated", LatencyMS: 0, FailureReason: FailureNone,
		PolicyVersion: access.PolicyVersion,
	}
}

func (p *Provider) threshold(input Input) (float64, bool) {
	if p.config.Mode == ModeAssist && approvalMatches(p.config.AssistApproval, input) {
		return p.config.AssistApproval.MinConfidence, true
	}
	if p.config.Mode == ModeShadow && p.config.ShadowThreshold != nil &&
		(input.Capability == CapabilityChoice || input.Capability == CapabilityRuleRecommendation) {
		return *p.config.ShadowThreshold, true
	}
	return 0, false
}

func validInput(input Input) bool {
	if !validReference(input.RequestID) || !validReference(input.DecisionID) ||
		!validReference(input.RunID) || !validReference(input.AttemptID) ||
		!validReference(input.TraceID) || input.PolicyVersion != access.PolicyVersion ||
		!validReference(input.RulePackVersion) || !ruleHashPattern.MatchString(input.RulePackHash) ||
		len(input.State) > MaxStateBytes || !utf8.ValidString(input.State) {
		return false
	}
	if input.Capability != CapabilityChoice && input.Capability != CapabilityRuleRecommendation {
		return false
	}
	if len(input.AllowedCandidates) < 2 || len(input.AllowedCandidates) > MaxCandidates ||
		len(input.MandatoryRuleIDs) > MaxCandidates {
		return false
	}
	seen := make(map[string]struct{}, len(input.AllowedCandidates))
	idPattern := candidatePattern
	if input.Capability == CapabilityRuleRecommendation {
		idPattern = ruleIDPattern
	}
	for _, candidate := range input.AllowedCandidates {
		if !idPattern.MatchString(candidate.ID) || len(candidate.ID) > MaxCandidateIDBytes ||
			len(candidate.Description) == 0 || len(candidate.Description) > MaxCandidateDescriptionBytes ||
			!utf8.ValidString(candidate.Description) || strings.TrimSpace(candidate.Description) == "" {
			return false
		}
		if _, duplicate := seen[candidate.ID]; duplicate {
			return false
		}
		seen[candidate.ID] = struct{}{}
	}
	if input.Capability == CapabilityChoice {
		if _, ok := seen["manual_review"]; !ok || len(input.MandatoryRuleIDs) != 0 {
			return false
		}
		return true
	}
	mandatory := make(map[string]struct{}, len(input.MandatoryRuleIDs))
	for _, id := range input.MandatoryRuleIDs {
		if !ruleIDPattern.MatchString(id) {
			return false
		}
		if _, duplicate := mandatory[id]; duplicate {
			return false
		}
		mandatory[id] = struct{}{}
		if _, allowed := seen[id]; allowed {
			return false
		}
	}
	return true
}

func approvalMatches(approval *AssistApproval, input Input) bool {
	return approval != nil && approval.Capability == CapabilityRuleRecommendation &&
		approval.Capability == input.Capability && approval.PolicyVersion == access.PolicyVersion &&
		approval.PolicyVersion == input.PolicyVersion && validThreshold(approval.MinConfidence)
}

func validThreshold(threshold float64) bool {
	return !math.IsNaN(threshold) && !math.IsInf(threshold, 0) && threshold >= 0 && threshold <= 1
}

func reorderedRules(candidates []Candidate, first string) []string {
	ordered := make([]string, 0, len(candidates))
	ordered = append(ordered, first)
	for _, candidate := range candidates {
		if candidate.ID != first {
			ordered = append(ordered, candidate.ID)
		}
	}
	return ordered
}

func safeReference(value string) string {
	if validReference(value) {
		return value
	}
	return ""
}

func validReference(value string) bool {
	return referencePattern.MatchString(value)
}

func safeHash(value string) string {
	if ruleHashPattern.MatchString(value) {
		return strings.ToLower(value)
	}
	return ""
}

func copyFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func safeCapability(value Capability) Capability {
	if value == CapabilityChoice || value == CapabilityRuleRecommendation {
		return value
	}
	return ""
}
