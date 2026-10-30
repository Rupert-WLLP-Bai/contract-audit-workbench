package agentflow

import (
	"context"
	"contractaudit/internal/engine"
	"slices"
)

// BaselineCapability names the exact limited comparator family, not the entire
// domain. The application must resolve this typed identifier; Question is only
// display context and cannot widen a tool's supported scope.
func BaselineCapability(domain Domain) string {
	switch domain {
	case Compliance:
		return "authorization_basis"
	case Commercial:
		return "commercial_baseline"
	case Technical:
		return "warranty_months"
	case Fulfillment:
		return "acceptance_schedule"
	case Counterparty:
		return "counterparty_record"
	default:
		return ""
	}
}

// BaselineTools reuses existing deterministic comparators in three operator
// views. Governance and counterparty tools explicitly return missing evidence.
// They do not manufacture legal opinions or call an external registry.
func BaselineTools() []Tool {
	return []Tool{baselineTool{Compliance}, baselineTool{Commercial}, baselineTool{Technical},
		baselineTool{Fulfillment}, baselineTool{Counterparty}}
}

type baselineTool struct{ domain Domain }

func (tool baselineTool) Spec() ToolSpec {
	return ToolSpec{ID: "baseline." + string(tool.domain), Domain: tool.domain, Version: "v1", Effect: "read",
		Capabilities: []string{BaselineCapability(tool.domain)}}
}

func (tool baselineTool) Execute(ctx context.Context, input ToolInput) ([]Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !baselineCapabilityAllowed(tool, input) {
		return nil, ErrInput
	}
	if tool.domain == Compliance || tool.domain == Counterparty {
		return []Finding{missingBaselineFinding(tool, input.Gap.Capability)}, nil
	}
	evaluations := evaluateBaseline(tool.domain, input.Evidence)
	return baselineFindings(tool, input, evaluations), nil
}

func baselineCapabilityAllowed(tool baselineTool, input ToolInput) bool {
	return slices.Contains(tool.Spec().Capabilities, input.Gap.Capability)
}

func missingBaselineFinding(tool baselineTool, capability string) Finding {
	reason, reasonCode := "未接入经授权且有版本的法律/制度/业务审批依据；保留缺证，不推断合规。", "required_authority_source_missing"
	if tool.domain == Counterparty {
		reason, reasonCode = "未接入经授权的主体/关联数据源；未发起外部查询，不推断资质或关联关系。", "authorized_counterparty_source_missing"
	}
	return Finding{Domain: tool.domain, Capability: capability, RuleID: tool.Spec().ID, Relation: "insufficient",
		Summary: reason, ReasonCode: reasonCode, Citations: []Citation{}}
}

func evaluateBaseline(domain Domain, evidence []Evidence) []engine.Evaluation {
	var docs []engine.Source
	for _, item := range evidence {
		docs = append(docs, engine.Source{ID: item.ID, Role: item.Role, Text: item.Text, ParseStatus: "ready"})
	}
	switch domain {
	case Commercial:
		return []engine.Evaluation{engine.EvaluatePaymentSum(docs), engine.EvaluatePaymentCondition(docs), engine.EvaluateAmount(docs)}
	case Technical:
		return []engine.Evaluation{engine.Evaluate(docs)}
	case Fulfillment:
		return []engine.Evaluation{engine.EvaluateSchedule(docs)}
	}
	return nil
}

func baselineFindings(tool baselineTool, input ToolInput, evaluations []engine.Evaluation) []Finding {
	out := make([]Finding, 0, len(evaluations))
	for _, evaluation := range evaluations {
		out = append(out, baselineFinding(tool, input, evaluation))
	}
	return out
}

func baselineFinding(tool baselineTool, input ToolInput, evaluation engine.Evaluation) Finding {
	finding := Finding{Domain: tool.domain, Capability: input.Gap.Capability, RuleID: evaluation.RuleVersion,
		Relation: evaluation.Relation, Summary: evaluation.Reason, Citations: []Citation{}}
	finding.ReasonCode = baselineReasonCode(evaluation.Relation)
	versions := make(map[string]string, len(input.Evidence))
	for _, evidence := range input.Evidence {
		versions[evidence.ID] = evidence.Version
	}
	finding.Citations = baselineCitations(evaluation, versions)
	return finding
}

func baselineReasonCode(relation string) string {
	switch relation {
	case engine.RelInsufficient:
		return "baseline_evidence_insufficient"
	case "unsupported":
		return "baseline_evidence_unsupported"
	case "unknown":
		return "baseline_evidence_unknown"
	default:
		return ""
	}
}

func baselineCitations(evaluation engine.Evaluation, versions map[string]string) []Citation {
	anchors := append([]engine.Anchor{}, evaluation.Uncertain...)
	for _, fact := range evaluation.Facts {
		anchors = append(anchors, fact.Anchor)
	}
	seen := map[Citation]bool{}
	citations := make([]Citation, 0, len(anchors))
	for _, anchor := range anchors {
		if anchor.Status != "located" {
			continue
		}
		citation := Citation{EvidenceID: anchor.DocumentID, Version: versions[anchor.DocumentID], Quote: anchor.Quote, Start: anchor.Start, End: anchor.End}
		if !seen[citation] {
			seen[citation] = true
			citations = append(citations, citation)
		}
	}
	return citations
}
