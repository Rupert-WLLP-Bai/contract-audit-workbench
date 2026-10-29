package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
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
		"empty gaps":           func(r *Request) { r.KnowledgeSelection.Input.Gaps = []KnowledgeInputGap{} },
		"empty evidence":       func(r *Request) { r.KnowledgeSelection.Input.Evidence = []KnowledgeInputEvidence{} },
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
		"oversize field with matching total": func(r *Request) {
			r.Knowledge[0].Summary = strings.Repeat("x", PlannerKnowledgeFieldBytes+1)
			r.KnowledgeSelection.Budget.ContentBytesUsed = len(r.Knowledge[0].Summary)
		},
		"unknown candidate": func(r *Request) { r.KnowledgeSelection.Candidates[0].KnowledgeID = "other" },
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
		"policy check collides with user gap": func(r *Request) {
			r.Knowledge[0].Truncated, r.KnowledgeSelection.Candidates[0].Truncated = true, true
			r.KnowledgeSelection.PolicyStatus, r.KnowledgeSelection.PolicyReason = "unknown", "policy_context_truncated"
			id := "knowledge_policy_" + TextHash("policy\x00version\x00" + r.Knowledge[0].ContentSHA256)[:24]
			r.Gaps[0].ID, r.KnowledgeSelection.Input.Gaps[0].GapID = id, id
		},
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

func selectionWithoutPolicy() Request {
	request := requestWithPolicyTrace(false)
	request.Knowledge = nil
	trace := request.KnowledgeSelection
	trace.Candidates = []KnowledgeCandidate{}
	trace.PolicyStatus, trace.PolicyReason = "unknown", "policy_missing"
	trace.Budget.RequiredCount, trace.Budget.ContentBytesUsed = 0, 0
	return request
}

func omittedGuidance(id string) KnowledgeCandidate {
	return KnowledgeCandidate{KnowledgeID: id, VersionID: "v1", ContentSHA256: TextHash(id),
		Kind: "guidance", Decision: "omitted", Reason: "no_relevance"}
}

func TestSelectionTraceRejectsInvalidOmittedCandidates(t *testing.T) {
	tests := map[string]func(*Request){
		"invalid identity": func(r *Request) { r.KnowledgeSelection.Candidates[0].KnowledgeID = "" },
		"invalid version":  func(r *Request) { r.KnowledgeSelection.Candidates[0].VersionID = "" },
		"invalid digest":   func(r *Request) { r.KnowledgeSelection.Candidates[0].ContentSHA256 = "bad" },
		"duplicate omitted": func(r *Request) {
			r.KnowledgeSelection.Candidates = append(r.KnowledgeSelection.Candidates, r.KnowledgeSelection.Candidates[0])
			r.KnowledgeSelection.Budget.OptionalCandidates, r.KnowledgeSelection.Budget.OptionalOmitted = 2, 2
		},
		"missing snapshot": func(r *Request) {
			r.KnowledgeSelection.SnapshotSHA256, r.KnowledgeSelection.PolicyReason = "", "policy_snapshot_missing"
		},
	}
	for name, alter := range tests {
		t.Run(name, func(t *testing.T) {
			request := selectionWithoutPolicy()
			request.KnowledgeSelection.Candidates = []KnowledgeCandidate{omittedGuidance("optional")}
			request.KnowledgeSelection.Budget.OptionalCandidates, request.KnowledgeSelection.Budget.OptionalOmitted = 1, 1
			alter(&request)
			if err := validateKnowledgeSelection(request); !errors.Is(err, ErrInput) {
				t.Fatalf("invalid omitted candidate accepted: %v", err)
			}
		})
	}
}

func TestSelectionTraceAcceptsContentAndInputBoundaries(t *testing.T) {
	for _, limit := range []int{0, MaxPlannerEvidenceBytes} {
		request := selectionWithoutPolicy()
		request.KnowledgeSelection.Budget.ContentBytesLimit = limit
		request.KnowledgeSelection.Input.BusinessLine = strings.Repeat("x", 128)
		if err := validateKnowledgeSelection(request); err != nil {
			t.Fatalf("empty content with limit %d rejected: %v", limit, err)
		}
	}
	request := requestWithPolicyTrace(false)
	request.Knowledge[0].Summary = strings.Repeat("x", PlannerKnowledgeFieldBytes)
	request.KnowledgeSelection.Budget.ContentBytesUsed = PlannerKnowledgeFieldBytes
	request.KnowledgeSelection.Budget.ContentBytesLimit = PlannerKnowledgeFieldBytes
	if err := validateKnowledgeSelection(request); err != nil {
		t.Fatalf("exact field and content budget rejected: %v", err)
	}
	request.KnowledgeSelection.Candidates = append(request.KnowledgeSelection.Candidates,
		KnowledgeCandidate{KnowledgeID: "omitted", VersionID: "v1", ContentSHA256: TextHash("omitted"),
			Decision: "omitted", Reason: "acceptance_optional_budget"})
	request.KnowledgeSelection.Budget.AcceptanceOmitted, request.KnowledgeSelection.Budget.AcceptanceTruncated = 1, true
	if err := validateKnowledgeSelection(request); err != nil {
		t.Fatalf("one acceptance omission rejected: %v", err)
	}
}

func TestSelectionTraceCandidateAndReferenceLimits(t *testing.T) {
	for _, count := range []int{MaxKnowledgeRecords, MaxKnowledgeRecords + 1} {
		request := selectionWithoutPolicy()
		for i := 0; i < count; i++ {
			request.KnowledgeSelection.Candidates = append(request.KnowledgeSelection.Candidates, omittedGuidance(fmt.Sprintf("optional-%d", i)))
		}
		request.KnowledgeSelection.Budget.OptionalCandidates, request.KnowledgeSelection.Budget.OptionalOmitted = count, count
		if err := validateKnowledgeSelection(request); (err == nil) != (count <= MaxKnowledgeRecords) {
			t.Fatalf("candidate count %d: %v", count, err)
		}
	}
	for _, count := range []int{1, PlannerKnowledgeReferenceBudget, PlannerKnowledgeReferenceBudget + 1} {
		for _, required := range []bool{false, true} {
			request := requestWithPolicyTrace(false)
			for i := 1; i < count; i++ {
				id := fmt.Sprintf("reference-%d", i)
				ref := KnowledgeReference{KnowledgeID: id, VersionID: "v1", ContentSHA256: TextHash(id), Kind: "guidance", Required: required}
				candidate := KnowledgeCandidate{KnowledgeID: id, VersionID: "v1", ContentSHA256: ref.ContentSHA256,
					Kind: ref.Kind, Required: required, Decision: "selected", Reason: "relevance", Score: 1}
				if required {
					ref.Kind, candidate.Kind, candidate.Reason = "policy", "policy", "required_policy"
					request.KnowledgeSelection.Budget.RequiredCount++
				} else {
					request.KnowledgeSelection.Budget.OptionalCandidates++
					request.KnowledgeSelection.Budget.OptionalSelected++
				}
				request.Knowledge = append(request.Knowledge, ref)
				request.KnowledgeSelection.Candidates = append(request.KnowledgeSelection.Candidates, candidate)
			}
			wantValid := required || count <= PlannerKnowledgeReferenceBudget
			if err := validateKnowledgeSelection(request); (err == nil) != wantValid {
				t.Fatalf("reference count %d required=%v: %v", count, required, err)
			}
		}
	}
}

func TestRuntimeDeliversExactPolicyChecks(t *testing.T) {
	for _, state := range []string{"available", "missing", "truncated", "semantic", "both"} {
		t.Run(state, func(t *testing.T) {
			request := requestWithPolicyTrace(false)
			want := []MandatoryCheck{}
			switch state {
			case "missing":
				request = selectionWithoutPolicy()
				want = append(want, MandatoryCheck{GapID: "knowledge_policy_missing", Domain: Compliance,
					Capability: "governed_policy_context", Status: "unknown", Reason: "policy_missing"})
			case "truncated", "semantic", "both":
				ref := &request.Knowledge[0]
				ref.Truncated, ref.RequiresFactEvaluation = state != "semantic", state != "truncated"
				request.KnowledgeSelection.Candidates[0].Truncated = ref.Truncated
				request.KnowledgeSelection.Candidates[0].RequiresFactEvaluation = ref.RequiresFactEvaluation
				reason, capability := "policy_semantic_verification_required", "governed_policy_semantics"
				if ref.Truncated {
					reason, capability = "policy_context_truncated", "governed_policy_context"
				}
				request.KnowledgeSelection.PolicyStatus, request.KnowledgeSelection.PolicyReason = "unknown", reason
				want = append(want, MandatoryCheck{GapID: "knowledge_policy_" + TextHash("policy\x00version\x00" + ref.ContentSHA256)[:24],
					Domain: Compliance, Capability: capability, Status: "unknown", Reason: reason})
				// Complete required policies and optional semantic context do not create mandatory gaps.
				request.Knowledge = append(request.Knowledge,
					KnowledgeReference{KnowledgeID: "complete", VersionID: "v1", ContentSHA256: TextHash("complete"), Kind: "policy", Required: true},
					KnowledgeReference{KnowledgeID: "optional", VersionID: "v1", ContentSHA256: TextHash("optional"), Kind: "guidance", RequiresFactEvaluation: true})
				request.KnowledgeSelection.Candidates = append(request.KnowledgeSelection.Candidates,
					KnowledgeCandidate{KnowledgeID: "complete", VersionID: "v1", ContentSHA256: TextHash("complete"), Kind: "policy", Required: true, Decision: "selected", Reason: "required_policy"},
					KnowledgeCandidate{KnowledgeID: "optional", VersionID: "v1", ContentSHA256: TextHash("optional"), Kind: "guidance", Decision: "selected", Reason: "relevance", Score: 1, RequiresFactEvaluation: true})
				request.KnowledgeSelection.Budget.RequiredCount, request.KnowledgeSelection.Budget.OptionalCandidates, request.KnowledgeSelection.Budget.OptionalSelected = 2, 1, 1
			}
			request.Scope = append(request.Scope, Compliance)
			var got []MandatoryCheck
			planner := plannerFunc(func(ctx context.Context, input PlanInput) (Plan, error) {
				got = slices.DeleteFunc(slices.Clone(input.MandatoryChecks), func(check MandatoryCheck) bool { return !strings.HasPrefix(check.GapID, "knowledge_policy_") })
				return (ScopePlanner{}).Plan(ctx, input)
			})
			_, err := runtimeFixture(t, planner, BaselineTools(), 1).Run(context.Background(), request)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("policy checks: got=%+v want=%+v err=%v", got, want, err)
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
