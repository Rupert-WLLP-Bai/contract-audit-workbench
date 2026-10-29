package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/access"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type plannerFunc func(context.Context, PlanInput) (Plan, error)

func (plannerFunc) Name() string                                           { return "test-planner" }
func (p plannerFunc) Plan(ctx context.Context, in PlanInput) (Plan, error) { return p(ctx, in) }

type toolFunc struct {
	spec ToolSpec
	run  func(context.Context, ToolInput) ([]Finding, error)
}

func (tool toolFunc) Spec() ToolSpec { return tool.spec }
func (tool toolFunc) Execute(ctx context.Context, in ToolInput) ([]Finding, error) {
	return tool.run(ctx, in)
}

func requestFixture() Request {
	resource := access.Resource{TenantID: "branch-a", CaseID: "case-a", OrgID: "enterprise", OwnerID: "alice"}
	return Request{RequestID: "demo-1", Principal: access.Principal{UserID: "alice", TenantID: "branch-a", Enabled: true,
		Grants: []access.Grant{{Role: "reviewer", Scope: "case", CaseID: "case-a"}}},
		Case: resource, Scope: []Domain{Technical},
		Gaps: []Gap{{ID: "g-tech", Domain: Technical, Capability: "warranty_months", Question: "质保承诺是否降低"}},
		Evidence: []Evidence{
			NewEvidence("req", "v1", "procurement", "设备质保期为36个月。", resource),
			NewEvidence("draft", "v1", "draft_contract", "设备质保期为24个月。", resource),
		}}
}

func runtimeFixture(t *testing.T, planner Planner, tools []Tool, maxCalls int) *Runtime {
	t.Helper()
	r, err := New(context.Background(), Config{Planner: planner, Tools: tools, MaxCalls: maxCalls,
		Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEinoScopePlanReusesRulesAndPreservesInput(t *testing.T) {
	request := requestFixture()
	before, _ := json.Marshal(request)
	r := runtimeFixture(t, ScopePlanner{}, BaselineTools(), 5)
	report, err := r.Run(context.Background(), request)
	if err != nil || report.Status != "completed" || report.ToolCalls != 1 || report.FallbackUsed {
		t.Fatalf("run = %+v / %v", report, err)
	}
	if len(report.Domains) != 5 || report.Domains[2].Findings[0].Relation != "conflict" ||
		report.Domains[0].Status != "not_requested" || len(report.UnresolvedGapIDs) != 0 {
		t.Fatalf("domain results = %+v", report.Domains)
	}
	if report.InputSHA256 != TextHash(string(before)) || len(report.Events) != 5 {
		t.Fatalf("missing provenance: %+v", report)
	}
	if report.Projection == nil || report.Projection.Gaps[0].Status != "resolved" ||
		report.Projection.Domains[2].Capabilities[0].Status != "completed" {
		t.Fatalf("verified conflict was not projected as a resolved check: %+v", report.Projection)
	}
	for i, event := range report.Events {
		if event.Sequence != i+1 {
			t.Fatal("event sequence is not monotonic")
		}
	}
	after, _ := json.Marshal(request)
	if string(before) != string(after) {
		t.Fatal("input was mutated")
	}
}

func TestBaselineInsufficientResultRemainsAValidObservation(t *testing.T) {
	request := requestFixture()
	request.Evidence = request.Evidence[:1]
	var inputs []PlanInput
	planner := plannerFunc(func(ctx context.Context, input PlanInput) (Plan, error) {
		inputs = append(inputs, input)
		return (ScopePlanner{}).Plan(ctx, input)
	})
	report, err := runtimeFixture(t, planner, BaselineTools(), 2).Run(context.Background(), request)
	if err != nil || report.Status != "partial" || report.ToolCalls != 1 ||
		!slices.Contains(report.UnresolvedGapIDs, "g-tech") || len(report.Domains[2].Findings) != 1 || len(inputs) != 2 {
		t.Fatalf("insufficient baseline result was not retained: %+v / %v", report, err)
	}
	finding := report.Domains[2].Findings[0]
	wantReason := "仅有一份独立材料写明质保月数，无法完成跨文件比对，这条比对是信息不足。"
	if finding.Relation != "insufficient" || finding.ReasonCode != "baseline_evidence_insufficient" ||
		finding.Summary != wantReason || len(finding.Citations) != 1 || finding.Citations[0].EvidenceID != "req" ||
		finding.Citations[0].Quote == "" || report.Projection == nil ||
		report.Projection.Gaps[0].Status != "insufficient" || report.Projection.Gaps[0].ReasonCode != finding.ReasonCode {
		t.Fatalf("baseline finding lost its reason or citation: %+v", finding)
	}
	if len(inputs[1].Observations) != 1 || inputs[1].Observations[0].Relation != "insufficient" ||
		inputs[1].Observations[0].ReasonCode != finding.ReasonCode || inputs[1].Observations[0].Summary != wantReason {
		t.Fatalf("later planning did not receive the valid observation: %+v", inputs[1].Observations)
	}
	for _, event := range report.Events {
		if event.Kind == "tool_failed" || event.Code == "tool_error_or_unverified_result" {
			t.Fatalf("insufficient result was recorded as a tool error: %+v", report.Events)
		}
	}
}

func TestAllFiveDomainsKeepMissingAuthorityVisible(t *testing.T) {
	request := requestFixture()
	request.Scope = Domains()
	request.Gaps = nil
	for _, domain := range Domains() {
		request.Gaps = append(request.Gaps, Gap{ID: "gap-" + string(domain), Domain: domain, Capability: BaselineCapability(domain), Question: "检查受支持的范围"})
	}
	report, err := runtimeFixture(t, ScopePlanner{}, BaselineTools(), 5).Run(context.Background(), request)
	if err != nil || report.ToolCalls != 5 || report.Status != "partial" || len(report.UnresolvedGapIDs) != 2 {
		t.Fatalf("missing authority did not remain visibly partial: %+v %v", report, err)
	}
	for _, index := range []int{0, 4} {
		if report.Domains[index].Findings[0].Relation != "insufficient" {
			t.Fatal("missing authority became a pass")
		}
	}
	if !slices.Contains(report.UnresolvedGapIDs, "gap-compliance") || !slices.Contains(report.UnresolvedGapIDs, "gap-counterparty") {
		t.Fatal("unresolved authority not reported")
	}
	encoded, err := json.Marshal(report.Projection)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Plan struct {
			Rounds []struct {
				NewEvidenceIDs []string `json:"new_evidence_ids"`
				ResolvedGapIDs []string `json:"resolved_gap_ids"`
			} `json:"rounds"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, round := range wire.Plan.Rounds {
		if round.NewEvidenceIDs == nil || round.ResolvedGapIDs == nil {
			t.Fatalf("missing-authority round serialized null collections: %s", encoded)
		}
	}
}

func TestPlannerCannotObserveCallerMutationAfterSnapshot(t *testing.T) {
	request := requestFixture()
	before, _ := json.Marshal(request)
	planner := plannerFunc(func(ctx context.Context, input PlanInput) (Plan, error) {
		// Deterministic mutation in the same goroutine, after Run has frozen its
		// input. This reveals JSON decoders accidentally reusing caller slices.
		request.Evidence[0].Text = "设备质保期为24个月。"
		request.Evidence[0].SHA256 = TextHash(request.Evidence[0].Text)
		request.Principal.Grants[0].Role = "viewer"
		request.Gaps[0].Question = "changed"
		return (ScopePlanner{}).Plan(ctx, input)
	})
	report, err := runtimeFixture(t, planner, BaselineTools(), 5).Run(context.Background(), request)
	if err != nil || report.Status != "completed" || report.ToolCalls != 1 ||
		report.Domains[2].Findings[0].Relation != "conflict" ||
		report.InputSHA256 != TextHash(string(before)) {
		t.Fatalf("snapshot changed after validation: %+v / %v", report, err)
	}
}

func TestAuthorizationPrecedesPlannerAndTools(t *testing.T) {
	var calls atomic.Int32
	planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) { calls.Add(1); return Plan{}, nil })
	r := runtimeFixture(t, planner, BaselineTools(), 5)
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Principal.TenantID = "branch-b" },
		func(r *Request) { r.Principal.Grants[0].Role = "viewer" },
		func(r *Request) { r.Principal.Enabled = false },
	} {
		request := requestFixture()
		mutate(&request)
		report, err := r.Run(context.Background(), request)
		if !errors.Is(err, ErrDenied) || report.ToolCalls != 0 || report.Status != "rejected" {
			t.Fatalf("%+v %v", report, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized identity reached planner")
	}
}

func TestInvalidSnapshotsRejectedBeforePlanning(t *testing.T) {
	cases := map[string]func(*Request){
		"duplicate scope":         func(r *Request) { r.Scope = []Domain{Technical, Technical} },
		"unknown scope":           func(r *Request) { r.Scope = []Domain{"external"} },
		"empty scope":             func(r *Request) { r.Scope = nil },
		"empty request id":        func(r *Request) { r.RequestID = "" },
		"no gaps":                 func(r *Request) { r.Gaps = nil },
		"duplicate gap":           func(r *Request) { r.Gaps = append(r.Gaps, r.Gaps[0]) },
		"gap outside scope":       func(r *Request) { r.Gaps[0].Domain = Commercial },
		"empty question":          func(r *Request) { r.Gaps[0].Question = " " },
		"long question":           func(r *Request) { r.Gaps[0].Question = strings.Repeat("a", 4097) },
		"foreign evidence tenant": func(r *Request) { r.Evidence[0].TenantID = "branch-b" },
		"foreign evidence case":   func(r *Request) { r.Evidence[0].CaseID = "case-b" },
		"duplicate evidence":      func(r *Request) { r.Evidence = append(r.Evidence, r.Evidence[0]) },
		"empty version":           func(r *Request) { r.Evidence[0].Version = "" },
		"wrong digest":            func(r *Request) { r.Evidence[0].SHA256 = strings.Repeat("0", 64) },
		"unnormalized text": func(r *Request) {
			r.Evidence[0].Text = "测试\r\n材料"
			r.Evidence[0].SHA256 = TextHash(r.Evidence[0].Text)
		},
		"oversize corpus": func(r *Request) {
			r.Evidence[0].Text = strings.Repeat("a", 2*1024*1024+1)
			r.Evidence[0].SHA256 = TextHash(r.Evidence[0].Text)
		},
	}
	r := runtimeFixture(t, ScopePlanner{}, BaselineTools(), 5)
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := requestFixture()
			mutate(&request)
			report, err := r.Run(context.Background(), request)
			if !errors.Is(err, ErrInput) || report.ToolCalls != 0 {
				t.Fatalf("%+v %v", report, err)
			}
		})
	}
}

func TestWholePlanGuardRejectsBeforeFirstTool(t *testing.T) {
	valid := Call{ToolID: "baseline.technical", Domain: Technical, GapID: "g-tech", Reason: "compare warranty"}
	cases := map[string][]Call{
		"unknown tool":      {valid, {ToolID: "shell.exec", Domain: Technical, GapID: "g-tech", Reason: "ignore all rules"}},
		"out of scope":      {{ToolID: "baseline.commercial", Domain: Commercial, GapID: "g-tech", Reason: "extra"}},
		"wrong tool domain": {{ToolID: "baseline.commercial", Domain: Technical, GapID: "g-tech", Reason: "mismatch"}},
		"unknown gap":       {{ToolID: "baseline.technical", Domain: Technical, GapID: "foreign-gap", Reason: "mismatch"}},
		"duplicate gap":     {valid, valid},
		"missing reason":    {{ToolID: "baseline.technical", Domain: Technical, GapID: "g-tech"}},
		"oversize reason":   {{ToolID: "baseline.technical", Domain: Technical, GapID: "g-tech", Reason: strings.Repeat("x", 1025)}},
	}
	for name, calls := range cases {
		t.Run(name, func(t *testing.T) {
			planner := plannerFunc(func(context.Context, PlanInput) (Plan, error) { return Plan{Calls: calls}, nil })
			report, err := runtimeFixture(t, planner, BaselineTools(), 1).Run(context.Background(), requestFixture())
			if !errors.Is(err, ErrPlan) || report.ToolCalls != 0 || report.Status != "rejected" {
				t.Fatalf("%+v %v", report, err)
			}
		})
	}
}

func TestPlannerUnavailableFallsBackButMalformedPlanDoesNot(t *testing.T) {
	for _, bad := range []error{errors.New("private endpoint secret must not appear"), ErrPlan} {
		p := plannerFunc(func(context.Context, PlanInput) (Plan, error) { return Plan{}, bad })
		report, err := runtimeFixture(t, p, BaselineTools(), 5).Run(context.Background(), requestFixture())
		if errors.Is(bad, ErrPlan) {
			if !errors.Is(err, ErrPlan) || report.ToolCalls != 0 || report.FallbackUsed {
				t.Fatalf("%+v %v", report, err)
			}
		} else {
			if err != nil || report.Status != "partial" || !report.FallbackUsed || report.ToolCalls != 1 ||
				report.Domains[2].Findings[0].Relation != "conflict" {
				t.Fatalf("%+v %v", report, err)
			}
		}
		b, _ := json.Marshal(report)
		if strings.Contains(string(b), "private endpoint secret") {
			t.Fatal("raw adapter error leaked")
		}
	}
}

func TestEmptyPlanCannotBecomeCompleted(t *testing.T) {
	p := plannerFunc(func(context.Context, PlanInput) (Plan, error) { return Plan{Calls: []Call{}}, nil })
	report, err := runtimeFixture(t, p, BaselineTools(), 5).Run(context.Background(), requestFixture())
	if err != nil || report.Status != "partial" || len(report.UnresolvedGapIDs) != 1 {
		t.Fatalf("%+v %v", report, err)
	}
}

func TestToolFailureOrForgedEvidenceCannotPass(t *testing.T) {
	cases := []string{"error", "forged_quote", "foreign_id", "wrong_version", "negative_offset", "empty_citations", "wrong_domain", "wrong_relation", "empty_result", "timeout"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			tool := toolFunc{spec: ToolSpec{ID: "test", Domain: Technical, Version: "1", Effect: "read", Capabilities: []string{"warranty_months"}}, run: func(ctx context.Context, in ToolInput) ([]Finding, error) {
				if name == "error" {
					return nil, errors.New("private connection error")
				}
				if name == "timeout" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if name == "empty_result" {
					return nil, nil
				}
				e := in.Evidence[0]
				f := Finding{Domain: Technical, Capability: "warranty_months", RuleID: "test", Relation: "conflict", Summary: "candidate", Citations: []Citation{{EvidenceID: e.ID, Version: e.Version, Quote: e.Text, Start: 0, End: len([]rune(e.Text))}}}
				switch name {
				case "forged_quote":
					f.Citations[0].Quote = "不存在的引用"
				case "foreign_id":
					f.Citations[0].EvidenceID = "another-case"
				case "wrong_version":
					f.Citations[0].Version = "v2"
				case "negative_offset":
					f.Citations[0].Start = -1
				case "empty_citations":
					f.Citations = nil
				case "wrong_domain":
					f.Domain = Commercial
				case "wrong_relation":
					f.Relation = "approved"
				}
				return []Finding{f}, nil
			}}
			r := runtimeFixture(t, ScopePlanner{}, []Tool{tool}, 1)
			report, err := r.Run(context.Background(), requestFixture())
			if err != nil || report.Status != "partial" || len(report.Domains[2].Findings) != 0 || report.ToolCalls != 1 {
				t.Fatalf("%+v %v", report, err)
			}
		})
	}
}

func TestCancellationAndGrantExpiry(t *testing.T) {
	r := runtimeFixture(t, ScopePlanner{}, BaselineTools(), 5)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := r.Run(ctx, requestFixture())
	if !errors.Is(err, context.Canceled) || report.Status != "cancelled" || report.ToolCalls != 0 {
		t.Fatalf("%+v %v", report, err)
	}
	now := time.Now()
	var clockCalls int
	request := requestFixture()
	request.Principal.Grants[0].ExpiresAt = now.Add(time.Second)
	r, err = New(context.Background(), Config{Planner: ScopePlanner{}, Tools: BaselineTools(), MaxCalls: 5, Timeout: time.Second, ToolTimeout: time.Millisecond * 100,
		Clock: func() time.Time {
			clockCalls++
			if clockCalls == 1 {
				return now
			}
			return now.Add(2 * time.Second)
		}})
	if err != nil {
		t.Fatal(err)
	}
	report, err = r.Run(context.Background(), request)
	if !errors.Is(err, ErrDenied) || report.ToolCalls != 0 {
		t.Fatalf("%+v %v", report, err)
	}
}

func TestRuntimeConcurrentInvocationsDoNotShareReports(t *testing.T) {
	r := runtimeFixture(t, ScopePlanner{}, BaselineTools(), 5)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			report, err := r.Run(context.Background(), requestFixture())
			if err != nil || report.ToolCalls != 1 {
				t.Errorf("%+v %v", report, err)
			}
		}()
	}
	wg.Wait()
}

func TestInvalidRuntimeConfigurations(t *testing.T) {
	base := Config{Planner: ScopePlanner{}, Tools: BaselineTools(), MaxCalls: 5, Timeout: time.Second, ToolTimeout: time.Millisecond * 100}
	mutations := []func(*Config){
		func(c *Config) { c.Planner = nil }, func(c *Config) { c.MaxCalls = -1 }, func(c *Config) { c.MaxCalls = 17 },
		func(c *Config) { c.Timeout = -1 }, func(c *Config) { c.ToolTimeout = 2 * time.Second }, func(c *Config) { c.Tools = nil },
		func(c *Config) { c.Tools = []Tool{nil} }, func(c *Config) { c.Tools = append(c.Tools, c.Tools[0]) },
		func(c *Config) {
			c.Tools = []Tool{toolFunc{spec: ToolSpec{ID: "write", Domain: Technical, Version: "1", Effect: "write"}}}
		},
	}
	for _, mutate := range mutations {
		c := base
		mutate(&c)
		if _, err := New(context.Background(), c); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}

func TestNewAppliesLimitsFrozenInPlannerConfiguration(t *testing.T) {
	for _, name := range []string{"AGENT_PLANNER_MODE", "AGENT_PLANNER_MAX_ROUNDS", "AGENT_PLANNER_MAX_CALLS", "AGENT_PLANNER_TIMEOUT", "AGENT_PLANNER_MAX_EVIDENCE_BYTES"} {
		t.Setenv(name, "")
	}
	configuration, err := ResolvePlannerConfiguration("")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(context.Background(), Config{Planner: ScopePlanner{}, Tools: append(BaselineTools(), SourceFactTools()...), PlannerConfiguration: configuration})
	if err != nil {
		t.Fatalf("New rejected frozen planner limits: %v", err)
	}
	if runtime.config.MaxCalls != configuration.Limits.MaxCalls || runtime.config.MaxRounds != configuration.Limits.MaxRounds ||
		runtime.config.Timeout != time.Duration(configuration.Limits.MaxDurationMS)*time.Millisecond ||
		runtime.config.MaxEvidenceBytes != configuration.Limits.MaxEvidenceBytes {
		t.Fatalf("runtime limits=%+v, frozen=%+v", runtime.config, configuration.Limits)
	}
}

func TestPlannerConfigurationRejectsStaleImplementationIdentity(t *testing.T) {
	for _, name := range []string{"AGENT_PLANNER_MODE", "AGENT_PLANNER_MAX_ROUNDS", "AGENT_PLANNER_MAX_CALLS", "AGENT_PLANNER_TIMEOUT", "AGENT_PLANNER_MAX_EVIDENCE_BYTES"} {
		t.Setenv(name, "")
	}
	configuration, err := ResolvePlannerConfiguration("")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*PlannerConfiguration)
	}{
		{name: "runtime", mutate: func(value *PlannerConfiguration) { value.RuntimeVersion = "older-runtime" }},
		{name: "prompt version", mutate: func(value *PlannerConfiguration) { value.PromptVersion = "older-prompt" }},
		{name: "prompt digest", mutate: func(value *PlannerConfiguration) { value.PromptSHA256 = strings.Repeat("0", 64) }},
		{name: "schema", mutate: func(value *PlannerConfiguration) { value.SchemaVersion = "older-schema" }},
		{name: "tool catalog", mutate: func(value *PlannerConfiguration) { value.ToolCatalogVersion = "older-catalog" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stale := configuration
			tc.mutate(&stale)
			identity, err := plannerConfigurationID(stale)
			if err != nil {
				t.Fatal(err)
			}
			stale.ConfigurationID = identity
			if err := stale.Validate(); err == nil {
				t.Fatal("accepted a self-consistent configuration for a stale implementation")
			}
		})
	}
}

type chatFixture struct {
	output     string
	err        error
	nilMessage bool
	input      []*schema.Message
}

func (c *chatFixture) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	c.input = messages
	if c.err != nil {
		return nil, c.err
	}
	if c.nilMessage {
		return nil, nil
	}
	return schema.AssistantMessage(c.output, nil), nil
}
func (*chatFixture) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("not used by bounded planner")
}

func TestEinoModelPlannerStrictDTOAndNoIdentityInPrompt(t *testing.T) {
	client := &chatFixture{output: `{"calls":[{"tool_id":"baseline.technical","domain":"technical","gap_id":"g-tech","reason":"check declared warranty"}]}`}
	report, err := runtimeFixture(t, ModelPlanner{Model: client, Identity: "fixture-model-v1"}, BaselineTools(), 5).Run(context.Background(), requestFixture())
	if err != nil || report.Planner != "fixture-model-v1" || report.ToolCalls != 1 {
		t.Fatalf("%+v %v", report, err)
	}
	if len(client.input) != 2 || client.input[0].Role != schema.System {
		t.Fatal("missing instruction boundary")
	}
	var plannerInput PlanInput
	if err := json.Unmarshal([]byte(client.input[1].Content), &plannerInput); err != nil || plannerInput.RemainingCalls != 5 {
		t.Fatalf("planner did not receive call budget: %+v, %v", plannerInput, err)
	}
	for _, text := range []string{"branch-a", "alice", "设备质保期", "tenant_id", "grants"} {
		if strings.Contains(client.input[1].Content, text) {
			t.Fatalf("unexpected identity/raw evidence in planning DTO: %s", text)
		}
	}
	for _, raw := range []string{"null", "[]", "{}", `{"calls":null}`, `{"calls":[],"tenant_id":"forged"}`, `{"calls":[]} {}`, strings.Repeat("a", 32769)} {
		if _, err := ParsePlan(raw); !errors.Is(err, ErrPlan) {
			t.Fatalf("accepted invalid plan %q", raw[:min(len(raw), 80)])
		}
	}
	if p, err := ParsePlan(`{"calls":[]}`); err != nil || len(p.Calls) != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	for _, planner := range []ModelPlanner{{}, {Model: client}, {Model: &chatFixture{err: errors.New("secret")}, Identity: "m"}, {Model: &chatFixture{nilMessage: true}, Identity: "m"}} {
		if _, err := planner.Plan(context.Background(), PlanInput{}); err == nil {
			t.Fatal("unconfigured/unavailable model accepted")
		}
	}
}

func TestScopePlannerRespectsRemainingCallBudget(t *testing.T) {
	input := PlanInput{
		Scope: []Domain{Compliance, Commercial},
		Gaps: []Gap{
			{ID: "g-compliance", Domain: Compliance, Capability: "amount"},
			{ID: "g-commercial", Domain: Commercial, Capability: "amount"},
			{ID: "g-second", Domain: Compliance, Capability: "party"},
		},
		Tools: []ToolSpec{
			{ID: "source.compliance", Domain: Compliance, Capabilities: []string{"amount", "party"}},
			{ID: "source.commercial", Domain: Commercial, Capabilities: []string{"amount"}},
		},
		RemainingCalls: 2,
	}
	plan, err := (ScopePlanner{}).Plan(context.Background(), input)
	if err != nil || len(plan.Calls) != 2 || plan.Calls[0].GapID != "g-compliance" || plan.Calls[1].GapID != "g-commercial" {
		t.Fatalf("bounded deterministic plan = %+v, %v", plan, err)
	}
	input.RemainingCalls = 0
	plan, err = (ScopePlanner{}).Plan(context.Background(), input)
	if err != nil || len(plan.Calls) != 0 {
		t.Fatalf("zero-budget deterministic plan = %+v, %v", plan, err)
	}
}
func TestRuntimeUsesVerifiedNewEvidenceAcrossPlannerRounds(t *testing.T) {
	request := requestFixture()
	request.Gaps[0].Capability = "scope"
	for index, value := range []string{"专线A", "专线B"} {
		evidence := NewEvidence(request.Evidence[index].ID, "v1", request.Evidence[index].Role,
			"线路范围："+value+"。", request.Case)
		evidence.DocumentID = "doc-" + evidence.ID
		evidence.ParsedDocumentID = "parsed-" + evidence.ID
		evidence.NormalizedDigest = TextHash(evidence.Text)
		request.Evidence[index] = evidence
	}
	first := toolFunc{spec: ToolSpec{ID: "candidate.scope", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"scope"}},
		run: func(_ context.Context, input ToolInput) ([]Finding, error) {
			evidence := input.Evidence[0]
			citation := Citation{EvidenceID: evidence.ID, Version: evidence.Version, Quote: "线路范围：专线A", Start: 0,
				End: len([]rune("线路范围：专线A")), DocumentID: evidence.DocumentID,
				ParsedDocumentID: evidence.ParsedDocumentID, NormalizedDigest: evidence.NormalizedDigest, Status: "located"}
			return []Finding{{Domain: Technical, Capability: "scope", RuleID: "candidate.scope", Relation: "insufficient",
				Summary: "verified source candidate requires comparison", ReasonCode: "candidate_requires_comparison",
				Facts:     []SemanticFact{{ID: "fact-scope-candidate", Type: "scope", Value: "专线A", Citation: citation}},
				Citations: []Citation{citation}}}, nil
		}}
	var priorFactSeen atomic.Bool
	second := toolFunc{spec: ToolSpec{ID: "compare.scope", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"scope"}},
		run: func(_ context.Context, input ToolInput) ([]Finding, error) {
			if len(input.PriorFindings) != 1 || len(input.PriorFindings[0].Facts) != 1 ||
				input.PriorFindings[0].Facts[0].ID != "fact-scope-candidate" {
				return nil, errors.New("newly verified candidate was not passed to the comparison tool")
			}
			priorFactSeen.Store(true)
			citations := make([]Citation, 0, len(input.Evidence))
			for _, evidence := range input.Evidence {
				citations = append(citations, Citation{EvidenceID: evidence.ID, Version: evidence.Version,
					Quote: evidence.Text, Start: 0, End: len([]rune(evidence.Text))})
			}
			return []Finding{{Domain: Technical, Capability: "scope", RuleID: "compare.scope", Relation: "conflict",
				Summary: "the two frozen scope values differ", Citations: citations, Facts: []SemanticFact{}}}, nil
		}}
	runtime, err := New(context.Background(), Config{Planner: ScopePlanner{}, Tools: []Tool{first, second},
		MaxCalls: 2, MaxRounds: 2, Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Run(context.Background(), request)
	if err != nil || report.Status != "partial" || report.ToolCalls != 2 || len(report.Rounds) != 2 ||
		len(report.Rounds[0].Calls) != 1 || len(report.Rounds[1].Calls) != 1 ||
		report.Rounds[0].Calls[0].ToolID != "candidate.scope" || report.Rounds[1].Calls[0].ToolID != "compare.scope" ||
		!slices.Contains(report.Rounds[0].NewEvidenceIDs, "fact-scope-candidate") || !priorFactSeen.Load() ||
		report.Projection == nil || len(report.Projection.Evidence) != 1 || report.Projection.Evidence[0].Value != "专线A" ||
		report.Projection.Gaps[0].Status != "unknown" || report.Projection.Gaps[0].ReasonCode != "claim_requires_semantic_verification" {
		t.Fatalf("verified source candidate did not progress through both rounds: %+v / %v", report, err)
	}
}

func TestRuntimeStopsAfterNoProgressAndHonorsRoundAndCallBudgets(t *testing.T) {
	noProgressTool := toolFunc{spec: ToolSpec{ID: "source.missing", Domain: Technical, Version: "v1", Effect: "read",
		Capabilities: []string{"warranty_months"}}, run: func(context.Context, ToolInput) ([]Finding, error) {
		return []Finding{{Domain: Technical, Capability: "warranty_months", RuleID: "source.missing",
			Relation: "insufficient", Summary: "no supported source field was found",
			ReasonCode: "required_source_or_field_missing", Citations: []Citation{}, Facts: []SemanticFact{}}}, nil
	}}
	noProgress, err := New(context.Background(), Config{Planner: ScopePlanner{}, Tools: []Tool{noProgressTool},
		MaxCalls: 4, MaxRounds: 3, Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	noProgressReport, err := noProgress.Run(context.Background(), requestFixture())
	if err != nil || noProgressReport.Status != "partial" || noProgressReport.ToolCalls != 1 || len(noProgressReport.Rounds) != 2 ||
		noProgressReport.Rounds[1].Status != "no_progress" || noProgressReport.Rounds[1].TerminationCode == nil ||
		*noProgressReport.Rounds[1].TerminationCode != "no_new_evidence_or_gap_change" {
		t.Fatalf("planner did not stop after its no-progress round: %+v / %v", noProgressReport, err)
	}

	makeTool := func(id string, domain Domain, capability string) Tool {
		return toolFunc{spec: ToolSpec{ID: id, Domain: domain, Version: "v1", Effect: "read", Capabilities: []string{capability}},
			run: func(context.Context, ToolInput) ([]Finding, error) {
				return []Finding{{Domain: domain, Capability: capability, RuleID: id, Relation: "insufficient",
					Summary: "the frozen source is insufficient", ReasonCode: "required_source_or_field_missing",
					Citations: []Citation{}, Facts: []SemanticFact{}}}, nil
			}}
	}
	tools := []Tool{
		makeTool("source.compliance", Compliance, "approval_authority"),
		makeTool("source.fulfillment", Fulfillment, "milestone"),
		makeTool("source.counterparty", Counterparty, "subject_id"),
	}
	request := requestFixture()
	request.Scope = []Domain{Compliance, Fulfillment, Counterparty}
	request.Gaps = []Gap{
		{ID: "gap-compliance", Domain: Compliance, Capability: "approval_authority", Question: "approved authority"},
		{ID: "gap-fulfillment", Domain: Fulfillment, Capability: "milestone", Question: "acceptance milestone"},
		{ID: "gap-counterparty", Domain: Counterparty, Capability: "subject_id", Question: "stable party identity"},
	}
	oneCallPerRound := plannerFunc(func(_ context.Context, input PlanInput) (Plan, error) {
		if input.RemainingCalls <= 0 {
			return Plan{Calls: []Call{}}, nil
		}
		for _, gap := range input.Gaps {
			for _, tool := range input.Tools {
				if tool.Domain != gap.Domain || !slices.Contains(tool.Capabilities, gap.Capability) {
					continue
				}
				called := false
				for _, prior := range input.PreviouslyCalled {
					if prior.GapID == gap.ID && prior.ToolID == tool.ID && !prior.NewEvidenceAvailable {
						called = true
						break
					}
				}
				if !called {
					return Plan{Calls: []Call{{ToolID: tool.ID, Domain: gap.Domain, GapID: gap.ID,
						Reason: "one bounded read call for the next unresolved gap"}}}, nil
				}
			}
		}
		return Plan{Calls: []Call{}}, nil
	})
	for _, test := range []struct {
		name, termination, lastStatus              string
		maxCalls, maxRounds, wantCalls, wantRounds int
	}{
		{name: "round limit", termination: "max_rounds_reached", lastStatus: "partial", maxCalls: 5, maxRounds: 2, wantCalls: 2, wantRounds: 2},
		{name: "cumulative call limit", termination: "max_calls_reached", lastStatus: "budget_exhausted", maxCalls: 1, maxRounds: 3, wantCalls: 1, wantRounds: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := New(context.Background(), Config{Planner: oneCallPerRound, Tools: tools,
				MaxCalls: test.maxCalls, MaxRounds: test.maxRounds, Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			report, err := runtime.Run(context.Background(), request)
			if len(report.Rounds) == 0 {
				t.Fatalf("runtime produced no budget round: %+v / %v", report, err)
			}
			last := report.Rounds[len(report.Rounds)-1]
			if err != nil || report.Status != "partial" || report.ToolCalls != test.wantCalls || len(report.Rounds) != test.wantRounds ||
				last.Status != test.lastStatus || last.TerminationCode == nil || *last.TerminationCode != test.termination ||
				len(report.UnresolvedGapIDs) != 3 {
				t.Fatalf("runtime exceeded or misreported its cumulative budget: %+v / %v", report, err)
			}
		})
	}
}

func TestRuntimeUsesOneTimeDeadlineAndRejectsEvidenceOverBudgetBeforePlanning(t *testing.T) {
	var plannerCalls atomic.Int32
	planner := plannerFunc(func(ctx context.Context, _ PlanInput) (Plan, error) {
		call := plannerCalls.Add(1)
		if call == 1 {
			return Plan{Calls: []Call{{ToolID: "source.missing", Domain: Technical, GapID: "g-tech", Reason: "bounded"}}}, nil
		}
		<-ctx.Done()
		return Plan{}, ctx.Err()
	})
	tool := toolFunc{spec: ToolSpec{ID: "source.missing", Domain: Technical, Version: "v1", Effect: "read",
		Capabilities: []string{"warranty_months"}}, run: func(context.Context, ToolInput) ([]Finding, error) {
		return []Finding{{Domain: Technical, Capability: "warranty_months", RuleID: "source.missing",
			Relation: "insufficient", Summary: "source field missing", ReasonCode: "required_source_or_field_missing",
			Citations: []Citation{}, Facts: []SemanticFact{}}}, nil
	}}
	runtime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{tool}, MaxCalls: 2, MaxRounds: 2,
		Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Run(context.Background(), requestFixture())
	if !errors.Is(err, context.DeadlineExceeded) || report.Status != "cancelled" || report.ToolCalls != 1 ||
		plannerCalls.Load() != 2 || len(report.Rounds) != 2 || report.Rounds[1].Status != "cancelled" {
		t.Fatalf("rounds did not share the runtime deadline: %+v / %v", report, err)
	}

	plannerCalls.Store(0)
	tinyEvidenceRuntime, err := New(context.Background(), Config{Planner: planner, Tools: []Tool{tool}, MaxCalls: 2, MaxRounds: 2,
		MaxEvidenceBytes: 1, Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	evidenceReport, err := tinyEvidenceRuntime.Run(context.Background(), requestFixture())
	if !errors.Is(err, ErrInput) || evidenceReport.ToolCalls != 0 || plannerCalls.Load() != 0 || len(evidenceReport.Events) == 0 ||
		evidenceReport.Events[0].Code != "input_budget_exceeded" {
		t.Fatalf("evidence over budget reached the planner or tools: %+v / %v", evidenceReport, err)
	}
}

func TestPlannerConfigurationSeparatesFixtureLiveAndEnforcesUpperBounds(t *testing.T) {
	for _, name := range []string{"AGENT_PLANNER_MODE", "AGENT_PLANNER_MAX_ROUNDS", "AGENT_PLANNER_MAX_CALLS", "AGENT_PLANNER_TIMEOUT", "AGENT_PLANNER_MAX_EVIDENCE_BYTES"} {
		t.Setenv(name, "")
	}
	for _, test := range []struct {
		mode, want    string
		modelRequired bool
	}{
		{mode: "fixed_chain", want: ModeFixedChain},
		{mode: "deterministic", want: ModeDeterministic},
		{mode: "fixture", want: ModeModelFixture, modelRequired: true},
		{mode: "live", want: ModeModelLive, modelRequired: true},
	} {
		t.Run(test.mode, func(t *testing.T) {
			t.Setenv("AGENT_PLANNER_MODE", test.mode)
			if _, err := ResolvePlannerConfiguration(""); test.modelRequired && err == nil {
				t.Fatal("model-backed planner accepted an absent model configuration identity")
			} else if !test.modelRequired && err != nil {
				t.Fatalf("deterministic planner required a model identity: %v", err)
			}
			configuration, err := ResolvePlannerConfiguration("model-config-sha256")
			if err != nil || configuration.Mode != test.want || configuration.ConfigurationID == "" {
				t.Fatalf("planner configuration = %+v, %v", configuration, err)
			}
			if test.modelRequired && configuration.ModelConfigurationID != "model-config-sha256" {
				t.Fatalf("model planner lost its frozen model identity: %+v", configuration)
			}
		})
	}
	t.Setenv("AGENT_PLANNER_MODE", "fixture")
	fixture, err := ResolvePlannerConfiguration("model-config-sha256")
	if err != nil {
		t.Fatalf("resolve fixture planner: %v", err)
	}
	t.Setenv("AGENT_PLANNER_MODE", "live")
	live, err := ResolvePlannerConfiguration("model-config-sha256")
	if err != nil || fixture.ConfigurationID == live.ConfigurationID || fixture.Mode == live.Mode {
		t.Fatalf("fixture and live planner identities were conflated: fixture=%+v live=%+v err=%v", fixture, live, err)
	}
	for _, setting := range []struct{ name, value string }{
		{"AGENT_PLANNER_MAX_ROUNDS", "4"}, {"AGENT_PLANNER_MAX_CALLS", "17"},
		{"AGENT_PLANNER_TIMEOUT", "500ms"}, {"AGENT_PLANNER_MAX_EVIDENCE_BYTES", "8388609"},
	} {
		t.Run(setting.name, func(t *testing.T) {
			t.Setenv("AGENT_PLANNER_MODE", "deterministic")
			t.Setenv(setting.name, setting.value)
			if _, err := ResolvePlannerConfiguration(""); err == nil {
				t.Fatalf("accepted out-of-bounds %s=%s", setting.name, setting.value)
			}
		})
	}
}

func TestPlanProjectionStatusUsesPublishedEnum(t *testing.T) {
	round := []PlanRound{{Round: 1}}
	tests := []struct {
		name          string
		status        string
		rounds        []PlanRound
		fallbackUsed  bool
		guardRejected bool
		want          string
	}{
		{name: "not planned", status: "rejected", want: "not_planned"},
		{name: "guard rejected", status: "rejected", rounds: round, guardRejected: true, want: "guard_rejected"},
		{name: "fallback", status: "partial", rounds: round, fallbackUsed: true, want: "fallback"},
		{name: "completed", status: "completed", rounds: round, want: "completed"},
		{name: "in progress", status: "running", rounds: round, want: "executing"},
		{name: "cancelled", status: "cancelled", rounds: round, want: "partial"},
		{name: "partial", status: "partial", rounds: round, want: "partial"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := planProjectionStatus(test.status, test.rounds, test.fallbackUsed, test.guardRejected); got != test.want {
				t.Fatalf("plan status = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCanonicalCoverageRetainsEveryCheckAndCannotHideMissingEvidence(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "insufficient first", true: "insufficient last"}[reverse], func(t *testing.T) {
			request := requestFixture()
			request.Scope = []Domain{Technical, Commercial}
			request.CanonicalEvaluations = []CanonicalEvaluation{
				{ID: "bandwidth", Domain: Technical, CapabilityID: "dedicated_line.bandwidth", Mandatory: true, Relation: "insufficient", ReasonCode: "required_fact_missing", EvidenceIDs: []string{"req"}},
				{ID: "availability", Domain: Technical, CapabilityID: "dedicated_line.monthly_availability", Mandatory: true, Relation: "compatible", ReasonCode: "minimum_met", EvidenceIDs: []string{"draft", "req"}},
				{ID: "advance", Domain: Commercial, CapabilityID: "payment.advance_ratio.ceiling", Mandatory: true, Relation: "conflict", ReasonCode: "ceiling_exceeded", EvidenceIDs: []string{"draft"}},
			}
			if reverse {
				slices.Reverse(request.CanonicalEvaluations)
			}
			var plannerInput PlanInput
			planner := plannerFunc(func(ctx context.Context, input PlanInput) (Plan, error) {
				plannerInput = input
				return (ScopePlanner{}).Plan(ctx, input)
			})
			report, err := runtimeFixture(t, planner, BaselineTools(), 5).Run(context.Background(), request)
			if err != nil || report.Projection == nil {
				t.Fatalf("canonical review: %+v, %v", report, err)
			}
			projection := report.Projection
			if report.Status != "partial" || projection.Status != "partial" || projection.Domains[2].Status != "partial" || projection.Domains[1].Status != "completed" {
				t.Fatalf("a completed check hid insufficient canonical evidence: %+v", projection)
			}
			if len(projection.Domains[2].Capabilities) != 3 || len(projection.Domains[1].Capabilities) != 1 || len(projection.Gaps) != 1 || projection.Gaps[0].Status != "resolved" {
				t.Fatalf("canonical checks were dropped or created executable gaps: %+v", projection)
			}
			for _, evaluation := range request.CanonicalEvaluations {
				found := false
				for _, domain := range projection.Domains {
					for _, capability := range domain.Capabilities {
						if capability.CapabilityID == evaluation.CapabilityID && capability.Mandatory && slices.Contains(capability.CanonicalEvaluationIDs, evaluation.ID) && capability.Relation != nil && *capability.Relation == evaluation.Relation && slices.Equal(capability.EvidenceIDs, evaluation.EvidenceIDs) {
							found = true
						}
					}
				}
				if !found {
					t.Fatalf("canonical identity, relation, or evidence lost: %+v", evaluation)
				}
				if !slices.ContainsFunc(plannerInput.MandatoryChecks, func(check MandatoryCheck) bool {
					return check.GapID == evaluation.ID && check.Domain == evaluation.Domain && check.Capability == evaluation.CapabilityID
				}) {
					t.Fatalf("mandatory canonical check missing from planner context: %+v", evaluation)
				}
			}
			if report.ToolCalls != 1 || len(report.Plan.Calls) != 1 || report.Plan.Calls[0].GapID != "g-tech" {
				t.Fatalf("canonical projection expanded executable scope: %+v", report.Plan)
			}
			for _, scope := range projection.Scope {
				for _, evaluation := range request.CanonicalEvaluations {
					if evaluation.Domain == scope.Domain && !slices.Contains(scope.CapabilityIDs, evaluation.CapabilityID) {
						t.Fatalf("canonical capability missing from declared coverage: %+v", scope)
					}
				}
			}
		})
	}
}

func TestOptionalCanonicalCheckIsNotMadeMandatory(t *testing.T) {
	request := requestFixture()
	request.CanonicalEvaluations = []CanonicalEvaluation{{ID: "optional-check", Domain: Technical,
		CapabilityID: "optional.guidance", Relation: "insufficient", ReasonCode: "required_fact_missing", EvidenceIDs: []string{"req"}}}
	var plannerInput PlanInput
	planner := plannerFunc(func(ctx context.Context, input PlanInput) (Plan, error) {
		plannerInput = input
		return (ScopePlanner{}).Plan(ctx, input)
	})
	report, err := runtimeFixture(t, planner, BaselineTools(), 2).Run(context.Background(), request)
	if err != nil || report.Projection == nil {
		t.Fatalf("optional canonical check: %+v / %v", report, err)
	}
	var found bool
	for _, capability := range report.Projection.Domains[2].Capabilities {
		if slices.Contains(capability.CanonicalEvaluationIDs, "optional-check") {
			found = true
			if capability.Mandatory || capability.Status != "insufficient" || capability.Relation == nil || *capability.Relation != "insufficient" {
				t.Fatalf("optional check lost its original status: %+v", capability)
			}
		}
	}
	if !found || slices.ContainsFunc(plannerInput.MandatoryChecks, func(check MandatoryCheck) bool { return check.GapID == "optional-check" }) {
		t.Fatalf("optional check missing or incorrectly mandatory: %+v / %+v", report.Projection, plannerInput.MandatoryChecks)
	}
}

func TestCanonicalProjectionDoesNotExpandTrustedScope(t *testing.T) {
	request := requestFixture()
	request.CanonicalEvaluations = []CanonicalEvaluation{{ID: "outside-scope", Domain: Compliance,
		CapabilityID: "approval_authority", Relation: "compatible", ReasonCode: "authority_present"}}
	report, err := runtimeFixture(t, ScopePlanner{}, BaselineTools(), 5).Run(context.Background(), request)
	if err != nil || report.Projection == nil {
		t.Fatalf("review: %+v, %v", report, err)
	}
	projection := report.Projection
	if len(projection.Scope) != 1 || projection.Scope[0].Domain != Technical ||
		projection.Domains[0].Status != "not_requested" || len(projection.Domains[0].Capabilities) != 0 || report.ToolCalls != 1 {
		t.Fatalf("canonical metadata expanded trusted scope: %+v", projection)
	}
}
