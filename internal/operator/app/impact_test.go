package app

import (
	"context"
	"testing"

	"contractaudit/internal/auditext"
)

type unexpectedImpactPlanner struct{}

func (unexpectedImpactPlanner) Plan(context.Context, auditext.ImpactRequest) (auditext.ImpactPlan, error) {
	panic("baseline-less path must not invoke planner")
}
func TestImpactWithoutBaselineRecomputesEveryCheck(t *testing.T) {
	target := ImpactEvaluation{ID: "eval-target", Checks: []auditext.CheckResult{{CheckID: "check-one"}, {CheckID: "check-two"}}}
	plan, err := PlanImpact(context.Background(), unexpectedImpactPlanner{}, auditext.ImpactExplain, target, ImpactEvaluation{}, false)
	if err != nil || plan.Fallback != auditext.FallbackFull || len(plan.Checks) != 2 || plan.Checks[0].Action != auditext.ImpactRecompute || len(plan.Checks[0].Reasons) != 1 || plan.Checks[0].Reasons[0] != "baseline_missing" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}
