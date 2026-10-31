package telecom

import (
	"testing"

	"contractaudit/internal/rulepack"
)

func TestTelecomMissingAndBaselinePassesPreserveBlockedLegalAuthority(t *testing.T) {
	definitions := ruleDefinitions
	ruleID := definitions[0].id
	report := rulepack.Report{Results: []rulepack.Result{{
		RuleID: ruleID, Relation: rulepack.RelationInsufficient, ReasonCode: "legal_authority_unresolved",
		LegalAuthorityOutcome: "unknown", LegalAuthorityReason: "missing_fact",
	}}}
	appendMissingResults(&report, definitions, map[string]string{ruleID: "required_fact_missing"})
	applyBaselineChecks(&report, rulepack.Request{}, definitions, map[string]clause{
		ruleID: {fact: rulepack.Fact{Citations: []rulepack.Citation{{SourceID: "contractual-source", Quote: "不得覆盖"}}}},
	})
	if len(report.Results) != 1 {
		t.Fatalf("baseline processing duplicated the blocked result: %+v", report.Results)
	}
	result := report.Results[0]
	if result.Relation != rulepack.RelationInsufficient || result.ReasonCode != "legal_authority_unresolved" ||
		result.LegalAuthorityOutcome != "unknown" || result.LegalAuthorityReason != "missing_fact" || len(result.Citations) != 0 {
		t.Fatalf("contractual missing/baseline checks overrode frozen legal gate: %+v", result)
	}
}

func TestTelecomNotApplicableLegalGateRemainsNotApplicable(t *testing.T) {
	definitions := ruleDefinitions
	ruleID := definitions[1].id
	report := rulepack.Report{Results: []rulepack.Result{{
		RuleID: ruleID, Relation: rulepack.RelationNotApplicable, ReasonCode: "legal_authority_not_applicable",
		LegalAuthorityOutcome: "not_applicable",
	}}}
	appendMissingResults(&report, definitions, map[string]string{ruleID: "sla_annex_missing"})
	applyBaselineChecks(&report, rulepack.Request{}, definitions, map[string]clause{
		ruleID: {fact: rulepack.Fact{Citations: []rulepack.Citation{{SourceID: "contractual-source", Quote: "不得覆盖"}}}},
	})
	if len(report.Results) != 1 || report.Results[0].Relation != rulepack.RelationNotApplicable ||
		report.Results[0].ReasonCode != "legal_authority_not_applicable" || len(report.Results[0].Citations) != 0 {
		t.Fatalf("contractual post-processing overrode not-applicable authority: %+v", report.Results)
	}
}
