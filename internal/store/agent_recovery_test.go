package store

import (
	"testing"

	"contractaudit/internal/agentflow"
)

func TestPriorFailureObservationsRestoreOnlyPersistedFailures(t *testing.T) {
	gaps := telecomAgentReviewGaps()
	if len(gaps) == 0 {
		t.Fatal("telecom gaps missing")
	}
	gap := gaps[0]
	tools := append(agentflow.BaselineTools(), agentflow.SourceFactTools()...)
	var toolID, toolVersion, completedTool string
	for _, tool := range tools {
		spec := tool.Spec()
		if spec.Domain != gap.Domain {
			continue
		}
		for _, capability := range spec.Capabilities {
			if capability == gap.Capability {
				toolID, toolVersion = spec.ID, spec.Version
			}
		}
		if toolID == "" && len(spec.Capabilities) > 0 {
			completedTool = spec.ID
		}
	}
	if toolID == "" {
		t.Fatal("no tool matches the first telecom gap")
	}
	review := agentflow.AgentReview{
		Plan: agentflow.AgentPlan{Rounds: []agentflow.PlanRound{{
			Round: 1,
			Calls: []agentflow.PlannedCall{
				{ToolID: toolID, ToolVersion: toolVersion, GapID: gap.ID, CapabilityID: gap.Capability, Status: "failed", ReasonCode: stringPointer("tool_error_or_unverified_result")},
				{ToolID: completedTool, ToolVersion: "v1", GapID: gap.ID, CapabilityID: gap.Capability, Status: "completed"},
				{ToolID: "unknown.tool", ToolVersion: "v1", GapID: gap.ID, CapabilityID: gap.Capability, Status: "failed", ReasonCode: stringPointer("unknown_tool")},
			},
		}}},
	}
	observations := priorFailureObservations(review, tools, gaps)
	if len(observations) != 1 || observations[0].ToolID != toolID || observations[0].GapID != gap.ID ||
		observations[0].FailureCode != "tool_error_or_unverified_result" || observations[0].Round != 1 {
		t.Fatalf("untrusted prior outputs were not filtered: %+v", observations)
	}
	if priorMinimumEvents(observations) != 4 {
		t.Fatalf("minimum event accounting changed: %d", priorMinimumEvents(observations))
	}
}
