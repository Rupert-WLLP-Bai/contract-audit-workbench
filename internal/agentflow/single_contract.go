package agentflow

import (
	"context"

	"contractaudit/internal/engine"
)

const SingleContractPaymentCapability = "single_contract.payment_sum"

// SingleContractTools exposes only the published single-contract rule to the
// planner. The wider baseline tools are intentionally unavailable in this scope.
func SingleContractTools() []Tool { return []Tool{singleContractPaymentTool{}} }

type singleContractPaymentTool struct{}

func (singleContractPaymentTool) Spec() ToolSpec {
	return ToolSpec{ID: "single_contract.payment_sum", Domain: Commercial, Version: "1", Effect: "read", Capabilities: []string{SingleContractPaymentCapability}}
}

func (tool singleContractPaymentTool) Execute(ctx context.Context, input ToolInput) ([]Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.Gap.Capability != SingleContractPaymentCapability || input.Gap.Domain != Commercial {
		return nil, ErrInput
	}
	var primary *Evidence
	for i := range input.Evidence {
		if input.Evidence[i].Role == "draft_contract" {
			if primary != nil {
				return nil, ErrInput
			}
			primary = &input.Evidence[i]
		}
	}
	if primary == nil {
		return []Finding{{Domain: Commercial, Capability: SingleContractPaymentCapability, RuleID: engine.SingleContractPrecheckRule,
			Relation: engine.RelInsufficient, Summary: "主合同未解析或不可用，付款比例检查未执行。", ReasonCode: "baseline_evidence_insufficient", Citations: []Citation{}}}, nil
	}
	evaluation := engine.EvaluateSingleContractPaymentSum(engine.Source{ID: primary.ID, Role: primary.Role, ParseStatus: "ready", Text: primary.Text})
	return []Finding{{Domain: Commercial, Capability: SingleContractPaymentCapability, RuleID: engine.SingleContractPrecheckRule,
		Relation: evaluation.Relation, Summary: evaluation.Reason, ReasonCode: "baseline_evidence_insufficient",
		Citations: baselineCitations(evaluation, map[string]string{primary.ID: primary.Version})}}, nil
}
