package main

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/decision"
	"contractaudit/internal/jev"
)

// jevShadow supplies a comparison route only. The mandatory rules execute in
// the next Eino node even when this call is denied, unavailable or cancelled.
// The hosted Jev endpoint receives the bounded state, so this path is reserved
// for synthetic benchmark states and must stay default-off.
func jevShadow(ctx context.Context, client *jev.Client, input decision.Input, timeout time.Duration) (result decision.Result, detail *jev.Answer) {
	configuredModel := ""
	if client != nil {
		configuredModel = client.Model()
	}
	result = decision.Result{Status: decision.StatusUnavailable, AcceptedRules: []string{}, Event: decision.DecisionEvent{
		RequestID: input.RequestID, DecisionID: input.DecisionID, RunID: input.RunID,
		AttemptID: input.AttemptID, TraceID: input.TraceID, TenantID: input.Resource.TenantID,
		CaseID: input.Resource.CaseID, ActorID: input.Principal.UserID, Capability: input.Capability,
		Mode: decision.ModeShadow, RulePackVersion: input.RulePackVersion, RulePackHash: input.RulePackHash,
		ModelName: configuredModel, ModelRevisionSource: "unknown", PolicyVersion: access.PolicyVersion,
		ConfidenceSource: "not_reported", Abstention: "not_configured",
	}}
	defer func() { result.Event.Status = result.Status }()
	if !access.Check(access.Review, input.Principal, input.Resource, time.Now()).Allowed {
		result.Status, result.Event.FailureReason = decision.StatusDenied, decision.FailureAuthorization
		return result, nil
	}
	if client == nil || timeout <= 0 || timeout > 30*time.Second || !decision.ValidInput(input) {
		result.Status, result.Event.FailureReason = decision.StatusInvalidInput, decision.FailureInvalidInput
		return result, nil
	}
	criteria := make(map[string]string, len(input.AllowedCandidates))
	for _, candidate := range input.AllowedCandidates {
		criteria[candidate.ID] = candidate.Description
	}
	if err := jev.ValidateInput(input.State, "review_focus", decision.ChoiceInstructions, criteria); err != nil {
		result.Status, result.Event.FailureReason = decision.StatusInvalidInput, decision.FailureInvalidInput
		return result, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	response, err := client.Choose(callCtx, input.State, "review_focus", decision.ChoiceInstructions, criteria)
	result.Event.LatencyMS = float64(time.Since(started).Nanoseconds()) / 1e6
	if err != nil {
		if errors.Is(err, jev.ErrInvalidInput) {
			result.Status, result.Event.FailureReason = decision.StatusInvalidInput, decision.FailureInvalidInput
			return result, nil
		}
		switch {
		case errors.Is(callCtx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
			result.Event.FailureReason = decision.FailureTimeout
		case errors.Is(callCtx.Err(), context.Canceled), errors.Is(err, context.Canceled):
			result.Event.FailureReason = decision.FailureCanceled
		case errors.Is(err, jev.ErrInvalidJSON):
			result.Event.FailureReason = decision.FailureInvalidJSON
		case errors.Is(err, jev.ErrInvalidProbabilities):
			result.Event.FailureReason = decision.FailureInvalidProbabilities
		case errors.Is(err, jev.ErrInvalidAnswer):
			result.Event.FailureReason = decision.FailureInvalidAnswer
		case errors.Is(err, jev.ErrOversizeResponse):
			result.Event.FailureReason = decision.FailureOversizeResponse
		case errors.Is(err, jev.ErrRedirect):
			result.Event.FailureReason = decision.FailureRedirect
		case errors.Is(err, jev.ErrHTTPStatus):
			result.Event.FailureReason = decision.FailureHTTPStatus
		default:
			result.Event.FailureReason = decision.FailureTransport
		}
		return result, nil
	}
	detail = &response
	result.Status = decision.StatusSuggested
	confidence := response.Confidence
	result.Suggestion = &decision.Suggestion{
		CandidateID: response.Choice, Probabilities: response.Probabilities,
		Confidence: &confidence, ConfidenceSource: response.ConfidenceSource,
		ResponseModel: response.Model,
	}
	result.Event.Recommendation = response.Choice
	result.Event.ResponseModel = response.Model
	result.Event.Confidence = &confidence
	result.Event.ConfidenceSource = response.ConfidenceSource
	return result, detail
}
