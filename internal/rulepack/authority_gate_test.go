package rulepack

import (
	"context"
	"testing"
)

func TestNormativeRuleRequiresFrozenAuthorityGateBeforeComparator(t *testing.T) {
	pack := demoPublished(t)
	rule := &pack.content.Rules[0]
	rule.AuthoritySources = []AuthoritySource{{ID: "civil-code", Version: "2021", Kind: "normative"}}

	request := demoRequest(t)
	withoutGate, err := pack.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	blocked := resultFor(t, withoutGate, rule.RuleID)
	if blocked.Relation != RelationInsufficient || blocked.ReasonCode != "legal_authority_unresolved" ||
		blocked.LegalAuthorityOutcome != "unknown" || len(blocked.Citations) != 0 || len(blocked.FactIDs) != 0 {
		t.Fatalf("normative rule executed without a frozen legal gate: %+v", blocked)
	}
	legacy, err := pack.ExecuteLegacyFrozen(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	legacyResult := resultFor(t, legacy, rule.RuleID)
	if legacyResult.ReasonCode == "legal_authority_unresolved" || legacyResult.LegalAuthorityOutcome != "" {
		t.Fatalf("explicit legacy-frozen evaluation did not preserve the pre-AUTH03 evaluator semantics: %+v", legacyResult)
	}
	request.AuthorityGates = []AuthorityGate{{RuleID: rule.RuleID, SourceID: "civil-code", SourceVersion: "2021", Outcome: "applicable"}}
	if _, err := pack.ExecuteLegacyFrozen(context.Background(), request); err == nil {
		t.Fatal("legacy-frozen evaluation accepted gates that could select its compatibility path")
	}

	applicable, err := pack.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	allowed := resultFor(t, applicable, rule.RuleID)
	if allowed.LegalAuthorityOutcome != "applicable" || allowed.Relation == RelationInsufficient {
		t.Fatalf("applicable frozen authority did not permit the original comparator: %+v", allowed)
	}

	request.AuthorityGates[0].Outcome = "unknown"
	request.AuthorityGates[0].Reason = "missing_fact"
	unknown, err := pack.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	unknownResult := resultFor(t, unknown, rule.RuleID)
	if unknownResult.Relation != RelationInsufficient || unknownResult.ReasonCode != "legal_authority_unresolved" ||
		unknownResult.LegalAuthorityReason != "missing_fact" || len(unknownResult.Citations) != 0 {
		t.Fatalf("unknown applicability was not gated before comparator/citations: %+v", unknownResult)
	}

	request.AuthorityGates[0].Outcome = "not_applicable"
	notApplicable, err := pack.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	notApplicableResult := resultFor(t, notApplicable, rule.RuleID)
	if notApplicableResult.Relation != RelationNotApplicable || notApplicableResult.ReasonCode != "legal_authority_not_applicable" ||
		notApplicableResult.LegalAuthorityOutcome != "not_applicable" || len(notApplicableResult.Citations) != 0 {
		t.Fatalf("not-applicable authority reached comparator: %+v", notApplicableResult)
	}
}

func TestNormativeRuleCombinesEverySourceAsAllOf(t *testing.T) {
	pack := demoPublished(t)
	rule := &pack.content.Rules[0]
	rule.AuthoritySources = []AuthoritySource{
		{ID: "civil-code", Version: "2021", Kind: "normative"},
		{ID: "procurement-law", Version: "2015", Kind: "normative"},
	}
	request := demoRequest(t)
	request.AuthorityGates = []AuthorityGate{
		{RuleID: rule.RuleID, SourceID: "civil-code", SourceVersion: "2021", Outcome: "applicable"},
		{RuleID: rule.RuleID, SourceID: "procurement-law", SourceVersion: "2015", Outcome: "unknown", Reason: "source_status_unverified"},
	}
	report, err := pack.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result := resultFor(t, report, rule.RuleID)
	if result.Relation != RelationInsufficient || result.LegalAuthorityOutcome != "unknown" || result.LegalAuthorityReason != "source_status_unverified" {
		t.Fatalf("missing one normative source did not block the all_of rule: %+v", result)
	}
	request.AuthorityGates[1].Outcome = "not_applicable"
	report, err = pack.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result = resultFor(t, report, rule.RuleID)
	if result.Relation != RelationNotApplicable {
		t.Fatalf("one failed all_of authority condition did not yield not_applicable: %+v", result)
	}
}

func TestAuthorityGateOutcomeIsIndependentOfSourceAndGateOrder(t *testing.T) {
	sources := []AuthoritySource{
		{ID: "source-a", Version: "1", Kind: "normative"},
		{ID: "source-b", Version: "1", Kind: "normative"},
		{ID: "source-c", Version: "1", Kind: "normative"},
	}
	gates := []AuthorityGate{
		{RuleID: "rule-order", SourceID: "source-a", SourceVersion: "1", Outcome: "unknown", Reason: "missing_fact"},
		{RuleID: "rule-order", SourceID: "source-b", SourceVersion: "1", Outcome: "not_applicable"},
		{RuleID: "rule-order", SourceID: "source-c", SourceVersion: "1", Outcome: "applicable"},
	}
	for _, sourceOrder := range permutations3(sources) {
		for _, gateOrder := range permutations3(gates) {
			rule := Rule{RuleID: "rule-order", AuthoritySources: sourceOrder}
			got, required, err := evaluateRuleAuthorityGate(rule, gateOrder)
			if err != nil || !required || got.Outcome != "not_applicable" {
				t.Fatalf("fact-level all_of outcome depends on order: sources=%+v gates=%+v result=%+v required=%v err=%v", sourceOrder, gateOrder, got, required, err)
			}
		}
	}

	for _, sourceOrder := range permutations2(sources[:2]) {
		for _, gateOrder := range permutations2([]AuthorityGate{
			{RuleID: "rule-order", SourceID: "source-a", SourceVersion: "1", Outcome: "not_applicable"},
		}) {
			got, required, err := evaluateRuleAuthorityGate(Rule{RuleID: "rule-order", AuthoritySources: sourceOrder}, gateOrder)
			if err != nil || !required || got.Outcome != "unknown" || got.Reason != "legal_authority_missing_freeze" {
				t.Fatalf("missing exact source was hidden by a not-applicable source: sources=%+v gates=%+v result=%+v err=%v", sourceOrder, gateOrder, got, err)
			}
		}
	}
	for _, sourceOrder := range permutations2(sources[:2]) {
		gates := []AuthorityGate{
			{RuleID: "rule-order", SourceID: "source-a", SourceVersion: "1", Outcome: "not_applicable"},
			{RuleID: "rule-order", SourceID: "source-b", SourceVersion: "1", Outcome: "unknown", Reason: "legal_reference_missing", ReferenceMissing: true},
		}
		for _, gateOrder := range permutations2(gates) {
			got, required, err := evaluateRuleAuthorityGate(Rule{RuleID: "rule-order", AuthoritySources: sourceOrder}, gateOrder)
			if err != nil || !required || got.Outcome != "unknown" || !got.ReferenceMissing || got.Reason != "legal_reference_missing" {
				t.Fatalf("explicit missing reference was treated as a fact-level not-applicable result: sources=%+v gates=%+v result=%+v err=%v", sourceOrder, gateOrder, got, err)
			}
		}
	}
}

func permutations2[T any](items []T) [][]T {
	if len(items) != 2 {
		return [][]T{append([]T(nil), items...)}
	}
	return [][]T{{items[0], items[1]}, {items[1], items[0]}}
}

func permutations3[T any](items []T) [][]T {
	if len(items) != 3 {
		return [][]T{append([]T(nil), items...)}
	}
	return [][]T{
		{items[0], items[1], items[2]}, {items[0], items[2], items[1]},
		{items[1], items[0], items[2]}, {items[1], items[2], items[0]},
		{items[2], items[0], items[1]}, {items[2], items[1], items[0]},
	}
}
