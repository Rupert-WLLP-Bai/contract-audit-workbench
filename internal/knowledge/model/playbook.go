package model

import (
	"contractaudit/internal/fault"
)

// PlaybookTemplateRef identifies the exact standard template, not the origin
// of an input contract.
type PlaybookTemplateRef struct {
	TemplateID      string `json:"template_id"`
	TemplateVersion string `json:"template_version"`
	TemplateDigest  string `json:"template_digest"`
}
type PlaybookClause struct {
	RuleID              string `json:"rule_id"`
	StandardClause      string `json:"standard_clause"`
	AcceptableDeviation string `json:"acceptable_deviation"`
	HardConstraint      string `json:"hard_constraint"`
	ProposedWording     string `json:"proposed_wording"`
	EscalationCondition string `json:"escalation_condition"`
}
type PlaybookContent struct {
	Template PlaybookTemplateRef `json:"template"`
	Clauses  []PlaybookClause    `json:"clauses"`
}

func ValidatePlaybook(content GovernedKnowledgeContent) error {
	p := content.Playbook
	if p == nil || !validResourceID(p.Template.TemplateID) || !validResourceID(p.Template.TemplateVersion) || !ValidHash(p.Template.TemplateDigest) || len(p.Clauses) == 0 || len(p.Clauses) > 32 {
		return fault.Invalid("审查手册模板或条款无效")
	}
	rules := map[string]bool{}
	for _, id := range content.Binding.RuleIDs {
		rules[id] = true
	}
	sources := map[string]bool{}
	for _, span := range content.Binding.SourceSpans {
		sources[span.RuleID] = true
	}
	seen := map[string]bool{}
	for _, clause := range p.Clauses {
		if !rules[clause.RuleID] || !sources[clause.RuleID] || seen[clause.RuleID] || !ValidText(clause.StandardClause, 2000) || !ValidText(clause.AcceptableDeviation, 2000) || !ValidText(clause.HardConstraint, 2000) || !ValidText(clause.ProposedWording, 2000) || !ValidText(clause.EscalationCondition, 2000) {
			return fault.Invalid("审查手册条款必须对应唯一规则及正文依据")
		}
		seen[clause.RuleID] = true
	}
	return nil
}

// AdoptedPlaybook preserves the full approved content and exact reference.
type AdoptedPlaybook struct {
	Ref     KnowledgeRef             `json:"ref"`
	Content GovernedKnowledgeContent `json:"content"`
}
