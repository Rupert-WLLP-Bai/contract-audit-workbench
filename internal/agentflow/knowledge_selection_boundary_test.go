package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Rejected frozen records must never reach the planner or be rewritten.
func TestRuntimeRejectsForgedKnowledgeSelection(t *testing.T) {
	tests := map[string]func(*Request){
		"algorithm":            func(r *Request) { r.KnowledgeSelection.Algorithm = "other" },
		"snapshot digest":      func(r *Request) { r.KnowledgeSelection.SnapshotSHA256 = "invalid" },
		"missing candidates":   func(r *Request) { r.KnowledgeSelection.Candidates = nil },
		"missing gaps":         func(r *Request) { r.KnowledgeSelection.Input.Gaps = nil },
		"missing evidence":     func(r *Request) { r.KnowledgeSelection.Input.Evidence = nil },
		"missing rounds":       func(r *Request) { r.KnowledgeSelection.PlannerRounds = nil },
		"previous round":       func(r *Request) { r.KnowledgeSelection.PlannerRounds = []KnowledgePlanningRound{{}} },
		"missing reads":        func(r *Request) { r.KnowledgeSelection.Reads = nil },
		"previous read":        func(r *Request) { r.KnowledgeSelection.Reads = []KnowledgeReadRecord{{}} },
		"business line length": func(r *Request) { r.KnowledgeSelection.Input.BusinessLine = strings.Repeat("a", 129) },
		"scenario encoding":    func(r *Request) { r.KnowledgeSelection.Input.Scenario = string([]byte{255}) },
		"gap question":         func(r *Request) { r.KnowledgeSelection.Input.Gaps[0].Question += "changed" },
		"evidence digest":      func(r *Request) { r.KnowledgeSelection.Input.Evidence[0].SHA256 = TextHash("other") },
		"evidence runes":       func(r *Request) { r.KnowledgeSelection.Input.Evidence[0].ScannedRunes++ },
		"reference limit":      func(r *Request) { r.KnowledgeSelection.Budget.ReferenceLimit++ },
		"field limit":          func(r *Request) { r.KnowledgeSelection.Budget.FieldBytes++ },
		"scan limit":           func(r *Request) { r.KnowledgeSelection.Budget.EvidenceRunesPerDocument++ },
		"negative budget":      func(r *Request) { r.KnowledgeSelection.Budget.ContentBytesLimit = -1 },
		"oversized budget":     func(r *Request) { r.KnowledgeSelection.Budget.ContentBytesLimit = MaxPlannerEvidenceBytes + 1 },
		"negative usage":       func(r *Request) { r.KnowledgeSelection.Budget.ContentBytesUsed = -1 },
		"overdrawn usage": func(r *Request) {
			r.KnowledgeSelection.Budget.ContentBytesUsed = r.KnowledgeSelection.Budget.ContentBytesLimit + 1
		},
		"read limit":           func(r *Request) { r.KnowledgeSelection.Budget.ReadCallsLimit++ },
		"read rune limit":      func(r *Request) { r.KnowledgeSelection.Budget.ReadMaxRunes++ },
		"read byte limit":      func(r *Request) { r.KnowledgeSelection.Budget.ReadMaxBytes++ },
		"previous reads":       func(r *Request) { r.KnowledgeSelection.Budget.PriorReadCalls++ },
		"previous reservation": func(r *Request) { r.KnowledgeSelection.Budget.PriorReadReservedBytes++ },
		"field length":         func(r *Request) { r.Knowledge[0].Summary = strings.Repeat("x", PlannerKnowledgeFieldBytes+1) },
		"unknown candidate":    func(r *Request) { r.KnowledgeSelection.Candidates[0].KnowledgeID = "other" },
		"duplicate candidate": func(r *Request) {
			r.KnowledgeSelection.Candidates = append(r.KnowledgeSelection.Candidates, r.KnowledgeSelection.Candidates[0])
		},
		"unknown reason":               func(r *Request) { r.KnowledgeSelection.Candidates[0].Reason = "other" },
		"negative score":               func(r *Request) { r.KnowledgeSelection.Candidates[0].Score = -1 },
		"required flag":                func(r *Request) { r.KnowledgeSelection.Candidates[0].Required = false },
		"candidate kind":               func(r *Request) { r.KnowledgeSelection.Candidates[0].Kind = "guidance" },
		"candidate decision":           func(r *Request) { r.KnowledgeSelection.Candidates[0].Decision = "omitted" },
		"candidate truncation":         func(r *Request) { r.KnowledgeSelection.Candidates[0].Truncated = true },
		"semantic flag":                func(r *Request) { r.KnowledgeSelection.Candidates[0].RequiresFactEvaluation = true },
		"required total":               func(r *Request) { r.KnowledgeSelection.Budget.RequiredCount++ },
		"optional total":               func(r *Request) { r.KnowledgeSelection.Budget.OptionalCandidates++ },
		"selected total":               func(r *Request) { r.KnowledgeSelection.Budget.OptionalSelected++ },
		"omitted total":                func(r *Request) { r.KnowledgeSelection.Budget.OptionalOmitted++ },
		"acceptance total":             func(r *Request) { r.KnowledgeSelection.Budget.AcceptanceOmitted++ },
		"acceptance flag":              func(r *Request) { r.KnowledgeSelection.Budget.AcceptanceTruncated = true },
		"content total":                func(r *Request) { r.KnowledgeSelection.Budget.ContentBytesUsed++ },
		"policy status":                func(r *Request) { r.KnowledgeSelection.PolicyStatus = "unknown" },
		"policy reason":                func(r *Request) { r.KnowledgeSelection.PolicyReason = "policy_missing" },
		"missing snapshot with policy": func(r *Request) { r.KnowledgeSelection.SnapshotSHA256 = "" },
	}
	for name, alter := range tests {
		t.Run(name, func(t *testing.T) {
			request := requestWithPolicyTrace(false)
			alter(&request)
			before, _ := json.Marshal(request)
			calls := 0
			planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) { calls++; return Plan{}, nil })
			report, err := runtimeFixture(t, planner, BaselineTools(), 1).Run(context.Background(), request)
			if !errors.Is(err, ErrInput) || calls != 0 || report.ToolCalls != 0 {
				t.Fatalf("forged trace reached execution: err=%v planner=%d tools=%d", err, calls, report.ToolCalls)
			}
			after, _ := json.Marshal(request)
			if string(after) != string(before) {
				t.Fatal("validation modified the frozen input")
			}
		})
	}
}

func TestRuntimePreservesIncompletePolicyReasons(t *testing.T) {
	tests := []struct {
		name, reason string
		alter        func(*Request)
	}{
		{"missing snapshot", "policy_snapshot_missing", func(r *Request) {
			r.Knowledge = nil
			r.KnowledgeSelection.Candidates = []KnowledgeCandidate{}
			r.KnowledgeSelection.Budget.RequiredCount = 0
			r.KnowledgeSelection.Budget.ContentBytesUsed = 0
			r.KnowledgeSelection.SnapshotSHA256 = ""
		}},
		{"missing policy", "policy_missing", func(r *Request) {
			r.Knowledge = nil
			r.KnowledgeSelection.Candidates = []KnowledgeCandidate{}
			r.KnowledgeSelection.Budget.RequiredCount = 0
			r.KnowledgeSelection.Budget.ContentBytesUsed = 0
		}},
		{"semantic verification", "policy_semantic_verification_required", func(r *Request) {
			r.Knowledge[0].RequiresFactEvaluation = true
			r.KnowledgeSelection.Candidates[0].RequiresFactEvaluation = true
		}},
		{"truncation takes precedence", "policy_context_truncated", func(r *Request) {
			r.Knowledge[0].RequiresFactEvaluation, r.Knowledge[0].Truncated = true, true
			r.KnowledgeSelection.Candidates[0].RequiresFactEvaluation, r.KnowledgeSelection.Candidates[0].Truncated = true, true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := requestWithPolicyTrace(false)
			test.alter(&request)
			request.KnowledgeSelection.PolicyStatus, request.KnowledgeSelection.PolicyReason = "unknown", test.reason
			before, _ := json.Marshal(request)
			var received PlanInput
			planner := plannerFunc(func(ctx context.Context, input PlanInput) (Plan, error) {
				received = input
				return (ScopePlanner{}).Plan(ctx, input)
			})
			report, err := runtimeFixture(t, planner, BaselineTools(), 1).Run(context.Background(), request)
			if err != nil || report.Status != "partial" || received.KnowledgePolicyStatus != "unknown" || received.KnowledgePolicyReason != test.reason {
				t.Fatalf("incomplete policy lost: err=%v status=%s input=%+v", err, report.Status, received)
			}
			after, _ := json.Marshal(request)
			if string(after) != string(before) {
				t.Fatal("execution modified the caller's trace")
			}
		})
	}
}
