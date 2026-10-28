package main

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/decision"
	"contractaudit/internal/model"
)

// chatShadow supplies the comparison route only. Mandatory rules execute in the
// next Eino node even when this call is denied, unavailable or cancelled.
func chatShadow(ctx context.Context, client *model.Client, input decision.Input, configuredModel string, timeout time.Duration) (result decision.Result, detail *model.ChoiceResult) {
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
	if timeout <= 0 || timeout > 30*time.Second || !decision.ValidInput(input) {
		result.Status, result.Event.FailureReason = decision.StatusInvalidInput, decision.FailureInvalidInput
		return result, nil
	}
	criteria := make(map[string]string, len(input.AllowedCandidates))
	for _, candidate := range input.AllowedCandidates {
		criteria[candidate.ID] = candidate.Description
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	response, err := client.RouteChoice(callCtx, input.State, criteria, decision.ChoiceInstructions)
	result.Event.LatencyMS = float64(time.Since(started).Nanoseconds()) / 1e6
	detail = &response
	result.Event.ResponseModel = response.ObservedModel
	if err != nil {
		switch {
		case errors.Is(callCtx.Err(), context.DeadlineExceeded):
			result.Event.FailureReason = decision.FailureTimeout
		case errors.Is(callCtx.Err(), context.Canceled):
			result.Event.FailureReason = decision.FailureCanceled
		case errors.Is(err, model.ErrUnusable):
			result.Event.FailureReason = decision.FailureInvalidAnswer
		default:
			result.Event.FailureReason = "model_request_failed"
		}
		return result, detail
	}
	result.Status = decision.StatusSuggested
	result.Suggestion = &decision.Suggestion{CandidateID: response.Choice, ConfidenceSource: "not_reported", ResponseModel: response.ObservedModel}
	result.Event.Recommendation = response.Choice
	return result, detail
}
