package decision

import (
	"strconv"
	"strings"
	"testing"

	"contractaudit/internal/access"
)

func validInputFixture() Input {
	return Input{
		RequestID: "request_1", DecisionID: "decision_1", RunID: "run_1", AttemptID: "attempt_1",
		TraceID: "trace-001",
		Principal: access.Principal{UserID: "alice", TenantID: "tenant-a", Enabled: true,
			Grants: []access.Grant{{Role: "reviewer", Scope: "case", CaseID: "case-a"}}},
		Resource:   access.Resource{TenantID: "tenant-a", CaseID: "case-a", OrgID: "sales", OwnerID: "alice"},
		Capability: CapabilityChoice, State: "合同审核范围内的短摘要。", RulePackVersion: "decision-pack-v1",
		RulePackHash: strings.Repeat("a", 64), PolicyVersion: access.PolicyVersion,
		AllowedCandidates: []Candidate{
			{ID: "bandwidth", Description: "带宽承诺"},
			{ID: "monthly_sla", Description: "月度服务等级"},
			{ID: "advance_payment", Description: "预付款条件"},
			{ID: "manual_review", Description: "人工复核"},
		},
	}
}

func TestValidInputAcceptsTheBoundedChoiceShape(t *testing.T) {
	if input := validInputFixture(); !ValidInput(input) {
		t.Fatalf("valid input was rejected: %+v", input)
	}
}

func TestValidInputAcceptsInclusiveByteAndCountLimits(t *testing.T) {
	input := validInputFixture()
	input.State = strings.Repeat("x", MaxStateBytes)
	input.AllowedCandidates = make([]Candidate, MaxCandidates)
	for index := range input.AllowedCandidates {
		suffix := strconv.Itoa(index)
		id := "candidate_" + strings.Repeat("a", MaxCandidateIDBytes-len("candidate_")-len(suffix)) + suffix
		if index == MaxCandidates-1 {
			id = "manual_review"
		}
		input.AllowedCandidates[index] = Candidate{
			ID: id, Description: strings.Repeat("d", MaxCandidateDescriptionBytes),
		}
	}
	if !ValidInput(input) {
		t.Fatal("inclusive maximum byte and candidate counts were rejected")
	}
}

func TestValidInputChecksShapeWithoutAuthorizingPrincipalOrResource(t *testing.T) {
	input := validInputFixture()
	input.Principal = access.Principal{}
	input.Resource = access.Resource{}
	if !ValidInput(input) {
		t.Fatal("valid bounded shape depended on authorization fields")
	}
}

func TestValidInputKeepsSingleByteDescriptionsAndRejectsDuplicatesAtAnyPosition(t *testing.T) {
	input := validInputFixture()
	input.AllowedCandidates[0].Description = "x"
	if !ValidInput(input) {
		t.Fatal("one-byte candidate description was rejected")
	}

	input.AllowedCandidates = []Candidate{
		{ID: "first", Description: "one"},
		{ID: "second", Description: "two"},
		{ID: "manual_review", Description: "review"},
	}
	if !validCandidates(input.AllowedCandidates) {
		t.Fatal("unique candidates were rejected")
	}
	input.AllowedCandidates[2].ID = "first"
	if validCandidates(input.AllowedCandidates) {
		t.Fatal("duplicate at final candidate position was accepted")
	}
}

func TestValidInputRejectsUnboundedOrAmbiguousShapes(t *testing.T) {
	for name, mutate := range map[string]func(*Input){
		"missing request":          func(input *Input) { input.RequestID = "" },
		"missing decision":         func(input *Input) { input.DecisionID = "" },
		"missing run":              func(input *Input) { input.RunID = "" },
		"missing attempt":          func(input *Input) { input.AttemptID = "" },
		"missing trace":            func(input *Input) { input.TraceID = "" },
		"trace with space":         func(input *Input) { input.TraceID = "bad trace" },
		"stale policy":             func(input *Input) { input.PolicyVersion = "stale" },
		"missing rulepack version": func(input *Input) { input.RulePackVersion = "" },
		"invalid rulepack hash":    func(input *Input) { input.RulePackHash = "not-a-hash" },
		"empty state":              func(input *Input) { input.State = "" },
		"oversize state":           func(input *Input) { input.State = strings.Repeat("x", MaxStateBytes+1) },
		"invalid utf8 state":       func(input *Input) { input.State = string([]byte{0xff}) },
		"unsupported capability":   func(input *Input) { input.Capability = Capability("score") },
		"single candidate":         func(input *Input) { input.AllowedCandidates = input.AllowedCandidates[:1] },
		"too many candidates": func(input *Input) {
			input.AllowedCandidates = append(input.AllowedCandidates, make([]Candidate, MaxCandidates)...)
		},
		"uppercase candidate id":      func(input *Input) { input.AllowedCandidates[0].ID = "Bandwidth" },
		"empty candidate description": func(input *Input) { input.AllowedCandidates[0].Description = " " },
		"oversize candidate":          func(input *Input) { input.AllowedCandidates[0].ID = strings.Repeat("x", MaxCandidateIDBytes+1) },
		"oversize description": func(input *Input) {
			input.AllowedCandidates[0].Description = strings.Repeat("x", MaxCandidateDescriptionBytes+1)
		},
		"invalid utf8 description": func(input *Input) { input.AllowedCandidates[0].Description = string([]byte{0xff}) },
		"duplicate candidate":      func(input *Input) { input.AllowedCandidates[1].ID = input.AllowedCandidates[0].ID },
		"missing manual review":    func(input *Input) { input.AllowedCandidates[3].ID = "fallback" },
	} {
		t.Run(name, func(t *testing.T) {
			input := validInputFixture()
			mutate(&input)
			if ValidInput(input) {
				t.Fatalf("invalid input was accepted: %+v", input)
			}
		})
	}
}

func TestDisabledResultKeepsCorrelationAndNoRules(t *testing.T) {
	input := validInputFixture()
	result := DisabledResult(input)
	if result.Status != StatusDisabled || result.Suggestion != nil || result.AcceptedRules == nil ||
		len(result.AcceptedRules) != 0 || result.Event.Status != StatusDisabled ||
		result.Event.Mode != ModeDisabled || result.Event.RequestID != input.RequestID ||
		result.Event.TraceID != input.TraceID || result.Event.TenantID != input.Resource.TenantID ||
		result.Event.CaseID != input.Resource.CaseID || result.Event.ActorID != input.Principal.UserID {
		t.Fatalf("disabled result = %+v", result)
	}
}
