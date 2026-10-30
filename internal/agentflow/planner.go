package agentflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// FixedChainPlanner selects the first registered tool for every requested gap.
// It is the non-model baseline used to compare deterministic and model plans.
type FixedChainPlanner struct{}

func (FixedChainPlanner) Name() string { return "fixed-chain-v1" }

func (FixedChainPlanner) Plan(ctx context.Context, input PlanInput) (Plan, error) {
	return (ScopePlanner{}).Plan(ctx, input)
}

// ScopePlanner is an explicit deterministic baseline, not an LLM.
type ScopePlanner struct{}

func (ScopePlanner) Name() string { return "deterministic-scope-v1" }

func (ScopePlanner) Plan(_ context.Context, input PlanInput) (Plan, error) {
	plan := Plan{Calls: []Call{}}
	called := make(map[string]bool, len(input.PreviouslyCalled))
	for _, prior := range input.PreviouslyCalled {
		called[prior.GapID+"\x00"+prior.ToolID] = !prior.NewEvidenceAvailable
	}
	if input.RemainingCalls <= 0 {
		return plan, nil
	}
	appendScopePlanCalls(&plan, input, called, true)
	appendScopePlanCalls(&plan, input, called, false)
	return plan, nil
}

func appendScopePlanCalls(plan *Plan, input PlanInput, called map[string]bool, mandatory bool) {
	for _, gap := range input.Gaps {
		if gap.Mandatory != mandatory {
			continue
		}
		if len(plan.Calls) >= input.RemainingCalls {
			break
		}
		if tool, exists := firstToolForGap(input.Tools, called, gap); exists {
			plan.Calls = append(plan.Calls, Call{ToolID: tool.ID, Domain: gap.Domain, GapID: gap.ID,
				Reason: "registered tool for a requested evidence gap"})
		}
	}
}

func firstToolForGap(tools []ToolSpec, called map[string]bool, gap Gap) (ToolSpec, bool) {
	for _, tool := range tools {
		if tool.Domain == gap.Domain && slices.Contains(tool.Capabilities, gap.Capability) && !called[gap.ID+"\x00"+tool.ID] {
			return tool, true
		}
	}
	return ToolSpec{}, false
}

// PlannerChatModel is the Generate-only Eino surface required by bounded
// planning. The model gateway client implements it without exposing Stream.
type PlannerChatModel interface {
	Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error)
}

type PlannerObservation struct {
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
}

type ObservedPlanner interface {
	PlanObserved(context.Context, PlanInput) (Plan, PlannerObservation, error)
}

// ModelPlanner accepts an Eino-compatible ChatModel supplied by application
// wiring. Its prompt selects tools only and cannot return evidence or findings.
type ModelPlanner struct {
	Model    PlannerChatModel
	Identity string
}

func (p ModelPlanner) Name() string { return p.Identity }

func (p ModelPlanner) Plan(ctx context.Context, input PlanInput) (Plan, error) {
	plan, _, err := p.PlanObserved(ctx, input)
	return plan, err
}

func (p ModelPlanner) PlanObserved(ctx context.Context, input PlanInput) (Plan, PlannerObservation, error) {
	if p.Model == nil || strings.TrimSpace(p.Identity) == "" {
		return Plan{}, PlannerObservation{}, errPlannerUnavailable
	}
	body, err := marshalPlannerInput(input)
	if err != nil {
		return Plan{}, PlannerObservation{}, err
	}
	message, err := p.generatePlanMessage(ctx, body)
	if err != nil {
		return Plan{}, PlannerObservation{}, err
	}
	if message == nil {
		return Plan{}, PlannerObservation{}, errPlannerUnavailable
	}
	plan, err := ParsePlan(message.Content)
	if err != nil {
		return Plan{}, PlannerObservation{}, err
	}
	return plan, plannerObservation(message), nil
}

func marshalPlannerInput(input PlanInput) ([]byte, error) { return json.Marshal(input) }

func (p ModelPlanner) generatePlanMessage(ctx context.Context, body []byte) (*schema.Message, error) {
	message, err := p.Model.Generate(ctx, []*schema.Message{schema.SystemMessage(plannerSystemPrompt), schema.UserMessage(string(body))})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errPlannerUnavailable
	}
	return message, nil
}

func plannerObservation(message *schema.Message) PlannerObservation {
	if message.ResponseMeta == nil || message.ResponseMeta.Usage == nil {
		return PlannerObservation{}
	}
	inputTokens := int64(message.ResponseMeta.Usage.PromptTokens)
	outputTokens := int64(message.ResponseMeta.Usage.CompletionTokens)
	totalTokens := int64(message.ResponseMeta.Usage.TotalTokens)
	return PlannerObservation{InputTokens: &inputTokens, OutputTokens: &outputTokens, TotalTokens: &totalTokens}
}

var errPlannerUnavailable = errors.New("planner model unavailable")

func ParsePlan(text string) (Plan, error) {
	if len(text) > 32*1024 {
		return Plan{}, ErrPlan
	}
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return Plan{}, ErrPlan
	}
	decoder := json.NewDecoder(bytes.NewBufferString(trimmed))
	decoder.DisallowUnknownFields()
	var plan Plan
	if err := decoder.Decode(&plan); err != nil || plan.Calls == nil {
		return Plan{}, ErrPlan
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		return Plan{}, ErrPlan
	}
	return plan, nil
}
