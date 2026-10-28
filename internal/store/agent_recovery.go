package store

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
)

// applyPriorAttemptRecovery restores only evidence the store actually holds:
// prior execution-event count, elapsed time, and failed tool calls from the
// previous attempt's persisted projection. Successful tool outputs are not
// reconstructed because the projection does not retain their findings; those
// gaps stay open and are re-executed instead of being trusted by inference.
func (s *Store) applyPriorAttemptRecovery(ctx context.Context, st state, actor accounts.User, caseItem Case, attempt Attempt, tools []agentflow.Tool, request *agentflow.Request) error {
	if attempt.RetryOfAttemptID == nil || attempt.InputHash == nil {
		return nil
	}
	prior, ok := st.attemptByID(*attempt.RetryOfAttemptID)
	if !ok || prior.RunID != attempt.RunID || prior.InputHash == nil || *prior.InputHash != *attempt.InputHash || attemptActive(prior.State) {
		return nil
	}
	events := st.executionEvents(prior.ID)
	count, elapsed := priorAttemptUsage(events, prior)
	var observations []agentflow.PriorToolResult
	raw, found, err := s.AgentReviewForAttempt(ctx, actor, caseItem.ID, attempt.RunID, prior.ID)
	if err != nil {
		return err
	}
	if found {
		var review agentflow.AgentReview
		if err := json.Unmarshal(raw, &review); err != nil {
			return err
		}
		if review.AttemptID == prior.ID {
			observations = priorFailureObservations(review, tools, telecomAgentReviewGaps())
			if len(observations) > 0 && count < priorMinimumEvents(observations) {
				observations = nil
			}
		}
		if count == 0 && len(review.Events) > 0 {
			count = min(len(review.Events), 1024)
		}
	}
	request.PriorObservations = observations
	if count > 0 {
		request.PriorEventCount = count
		request.PriorElapsedMS = elapsed
	}
	return nil
}

func priorAttemptUsage(events []ExecutionEvent, prior Attempt) (int, *int64) {
	count := min(len(events), 1024)
	total := int64(0)
	for _, event := range events {
		if event.DurationMS != nil && *event.DurationMS > 0 {
			total += *event.DurationMS
		}
	}
	if total == 0 && prior.StartedAt != nil && prior.FinishedAt != nil {
		if started, err := time.Parse(time.RFC3339Nano, *prior.StartedAt); err == nil {
			if finished, err := time.Parse(time.RFC3339Nano, *prior.FinishedAt); err == nil && finished.After(started) {
				total = finished.Sub(started).Milliseconds()
			}
		}
	}
	return count, &total
}

func priorFailureObservations(review agentflow.AgentReview, tools []agentflow.Tool, gaps []agentflow.Gap) []agentflow.PriorToolResult {
	toolVersions := make(map[string]string, len(tools))
	for _, tool := range tools {
		spec := tool.Spec()
		toolVersions[spec.ID] = spec.Version
	}
	allowedGaps := make(map[string]agentflow.Gap, len(gaps))
	for _, gap := range gaps {
		allowedGaps[gap.ID] = gap
	}
	seen := map[string]bool{}
	observations := make([]agentflow.PriorToolResult, 0, agentflow.DefaultPlannerCalls)
	lastRound := 0
	for _, round := range review.Plan.Rounds {
		for _, call := range round.Calls {
			if call.ReasonCode == nil || *call.ReasonCode == "" || call.Status == "completed" {
				continue
			}
			gap, gapOK := allowedGaps[call.GapID]
			version, toolOK := toolVersions[call.ToolID]
			roundNumber := round.Round
			if roundNumber < 1 || !gapOK || !toolOK || version != call.ToolVersion || gap.Capability != call.CapabilityID || roundNumber < lastRound || roundNumber > lastRound+1 {
				continue
			}
			key := call.GapID + "\x00" + call.ToolID
			if seen[key] {
				continue
			}
			seen[key] = true
			lastRound = roundNumber
			observations = append(observations, agentflow.PriorToolResult{
				Round: roundNumber, ToolID: call.ToolID, ToolVersion: call.ToolVersion,
				GapID: call.GapID, FailureCode: *call.ReasonCode,
			})
			if len(observations) == agentflow.DefaultPlannerCalls {
				return observations
			}
		}
	}
	return observations
}

func priorMinimumEvents(observations []agentflow.PriorToolResult) int {
	minimum, last := 0, 0
	for _, observation := range observations {
		if observation.Round != last {
			minimum += 2
		}
		minimum += 2
		last = observation.Round
	}
	return minimum
}
