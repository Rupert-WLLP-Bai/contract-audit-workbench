package agentflow

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUnsupportedBandwidthGapCannotBeClosedByWarranty(t *testing.T) {
	request := requestFixture()
	request.Gaps[0].Capability = "bandwidth"
	request.Gaps[0].Question = "带宽最低100 Mbit/s，拟签50 Mbit/s，是否违反承诺？"
	for i := range request.Evidence {
		request.Evidence[i].Text = "设备质保期为36个月。"
		request.Evidence[i].SHA256 = TextHash(request.Evidence[i].Text)
	}
	report, err := runtimeFixture(t, ScopePlanner{}, BaselineTools(), 5).Run(context.Background(), request)
	if err != nil || report.Status != "partial" || report.ToolCalls != 0 ||
		!slices.Contains(report.UnresolvedGapIDs, "g-tech") || len(report.Domains[2].Findings) != 0 {
		t.Fatalf("unsupported gap incorrectly resolved: %+v / %v", report, err)
	}
	plannerCalled := false
	forced := plannerFunc(func(context.Context, PlanInput) (Plan, error) {
		plannerCalled = true
		return Plan{Calls: []Call{{ToolID: "baseline.technical", Domain: Technical, GapID: "g-tech", Reason: "force wrong capability"}}}, nil
	})
	report, err = runtimeFixture(t, forced, BaselineTools(), 5).Run(context.Background(), request)
	if err != nil || plannerCalled || report.Status != "partial" || report.ToolCalls != 0 ||
		len(report.Rounds) != 0 || !slices.Contains(report.UnresolvedGapIDs, "g-tech") || len(report.Events) != 1 ||
		report.Events[0].Kind != "gap_unsupported" || report.Events[0].Code != "capability_not_registered" {
		t.Fatalf("unsupported capability reached planning or closed the gap: %+v / %v", report, err)
	}
}

func TestEvidenceValidationHasCitationAndByteBudgets(t *testing.T) {
	evidence := requestFixture().Evidence[:1]
	base := Finding{Domain: Technical, Capability: "warranty_months", RuleID: "test", Relation: "conflict", Summary: "bounded",
		Citations: []Citation{{EvidenceID: evidence[0].ID, Version: evidence[0].Version, Quote: "设", Start: 0, End: 1}}}
	if err := validateFindings(context.Background(), []Finding{base}, Technical, "warranty_months", evidence); err != nil {
		t.Fatal(err)
	}
	t.Run("per finding", func(t *testing.T) {
		f := base
		f.Citations = slices.Repeat(f.Citations, maxCitationsPerFinding+1)
		if !errors.Is(validateFindings(context.Background(), []Finding{f}, Technical, "warranty_months", evidence), ErrEvidence) {
			t.Fatal("unbounded per-finding citations accepted")
		}
	})
	t.Run("total", func(t *testing.T) {
		f := base
		f.Citations = slices.Repeat(f.Citations, maxCitationsPerFinding)
		if !errors.Is(validateFindings(context.Background(), slices.Repeat([]Finding{f}, 9), Technical, "warranty_months", evidence), ErrEvidence) {
			t.Fatal("unbounded total citations accepted")
		}
	})
	t.Run("quote bytes", func(t *testing.T) {
		ev := evidence[0]
		ev.Text = strings.Repeat("a", maxQuoteBytes+1)
		f := base
		f.Citations = []Citation{{EvidenceID: ev.ID, Version: ev.Version, Quote: ev.Text, Start: 0, End: len(ev.Text)}}
		if !errors.Is(validateFindings(context.Background(), []Finding{f}, Technical, "warranty_months", []Evidence{ev}), ErrEvidence) {
			t.Fatal("unbounded quote bytes accepted")
		}
	})
	t.Run("wrong capability", func(t *testing.T) {
		if !errors.Is(validateFindings(context.Background(), []Finding{base}, Technical, "bandwidth", evidence), ErrEvidence) {
			t.Fatal("unrelated capability accepted")
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(validateFindings(ctx, []Finding{base}, Technical, "warranty_months", evidence), context.Canceled) {
		t.Fatal("cancelled validation accepted")
	}
}

func TestOverallDeadlineOnFinalToolKeepsUnresolvedGap(t *testing.T) {
	tool := toolFunc{spec: ToolSpec{ID: "slow", Version: "1", Domain: Technical, Effect: "read", Capabilities: []string{"warranty_months"}},
		run: func(ctx context.Context, _ ToolInput) ([]Finding, error) { <-ctx.Done(); return nil, ctx.Err() }}
	r, err := New(context.Background(), Config{Planner: ScopePlanner{}, Tools: []Tool{tool}, MaxCalls: 1,
		Timeout: time.Second, ToolTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	report, err := r.Run(context.Background(), requestFixture())
	if !errors.Is(err, context.DeadlineExceeded) || report.Status != "cancelled" || len(report.UnresolvedGapIDs) != 1 {
		t.Fatalf("cancelled final tool looked complete: %+v / %v", report, err)
	}
}

func TestReturnedEvidenceSharesOneBudgetAcrossRounds(t *testing.T) {
	for _, test := range []struct {
		name        string
		budget      int
		wantCalls   int
		wantChecks  int
		wantPartial bool
	}{
		{name: "no remaining evidence allowance", budget: 64, wantCalls: 1, wantPartial: true},
		{name: "second round exceeds cumulative allowance", budget: 220, wantCalls: 2, wantChecks: 1, wantPartial: true},
		{name: "both rounds fit", budget: 1024, wantCalls: 2, wantChecks: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := requestFixture()
			request.Gaps = []Gap{
				{ID: "scope-first", Domain: Technical, Capability: "scope", Question: "核对首项范围"},
				{ID: "scope-second", Domain: Technical, Capability: "scope", Question: "核对第二项范围"},
			}
			request.Evidence = []Evidence{
				sourceEvidence("req", "procurement", "适用范围：专线A。"),
				sourceEvidence("draft", "draft_contract", "适用范围：专线A。"),
			}
			for i := range request.Evidence {
				request.Evidence[i].TenantID = request.Case.TenantID
			}
			planner := plannerFunc(func(_ context.Context, input PlanInput) (Plan, error) {
				return Plan{Calls: []Call{{ToolID: "source.technical", Domain: Technical, GapID: input.Gaps[0].ID, Reason: "核对冻结材料范围"}}}, nil
			})
			runtime, err := New(context.Background(), Config{Planner: planner, Tools: SourceFactTools(), MaxCalls: 2, MaxRounds: 2,
				MaxEvidenceBytes: test.budget, Timeout: time.Second, ToolTimeout: 100 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			report, err := runtime.Run(context.Background(), request)
			if err != nil || report.ToolCalls != test.wantCalls || len(report.Projection.Gaps)-len(report.UnresolvedGapIDs) != test.wantChecks {
				t.Fatalf("returned evidence budget outcome: %+v, %v", report, err)
			}
			if (report.Status == "partial") != test.wantPartial {
				t.Fatalf("budget failure became a completed review: %+v", report)
			}
			if test.wantPartial {
				found := false
				for _, event := range report.Events {
					found = found || event.Code == "evidence_budget_exhausted"
				}
				if !found {
					t.Fatalf("missing cumulative evidence budget outcome: %+v", report.Events)
				}
			}
			if test.wantChecks == 0 && len(report.Projection.Evidence) != 0 {
				t.Fatalf("over-budget facts were partially retained: %+v", report.Projection.Evidence)
			}
		})
	}
}
