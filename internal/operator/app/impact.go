package app

import (
	"context"
	"errors"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fault"
)

type ImpactEvaluation struct {
	ID     string
	Input  auditext.FrozenInput
	Checks []auditext.CheckResult
}

func PlanImpact(ctx context.Context, planner auditext.ImpactPlanner, mode auditext.ImpactMode, target, base ImpactEvaluation, baseFound bool) (auditext.ImpactPlan, error) {
	var plan auditext.ImpactPlan
	if !baseFound {
		checks := make([]auditext.ImpactCheck, 0, len(target.Checks))
		for _, check := range target.Checks {
			checks = append(checks, auditext.ImpactCheck{CheckID: check.CheckID, Action: auditext.ImpactRecompute, Reasons: []string{"baseline_missing"}})
		}
		plan = auditext.ImpactPlan{SchemaVersion: auditext.ImpactPlanSchema, BaseEvaluationID: "none", TargetEvaluationID: target.ID, Mode: mode, DependencyComplete: false, Fallback: auditext.FallbackFull, FallbackReason: "baseline_missing", ChangedInputs: []string{}, Checks: checks, BusinessResultSource: "full", DataClass: auditext.DataClassSynthetic}
	} else {
		var err error
		plan, err = planner.Plan(ctx, auditext.ImpactRequest{BaseEvaluationID: base.ID, TargetEvaluationID: target.ID, Base: base.Input, Target: target.Input, BaseChecks: base.Checks, TargetChecks: target.Checks, Mode: mode})
		if err != nil {
			if errors.Is(err, auditext.ErrUnauthorized) {
				return auditext.ImpactPlan{}, fault.Forbidden("当前授权不能读取影响计划")
			}
			return auditext.ImpactPlan{}, fault.Conflict("影响计划未完成")
		}
	}
	if err := plan.Validate(); err != nil {
		return auditext.ImpactPlan{}, fault.New(502, "impact_plan_invalid", "影响计划格式不合法")
	}
	return plan, nil
}
