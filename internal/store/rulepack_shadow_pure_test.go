// File metadata (manifest mirror):
// Purpose: Exercises rule-shadow scope, sample and baseline validation, rule comparability, change classification and frozen request capture without a database.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"fmt"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/rulepack"
)

func shadowTestRule(id string) rulepack.Rule {
	return rulepack.Rule{
		RuleID: id, Version: "1", Domain: "contract", CapabilityID: "cap-1", Mandatory: true,
		BusinessScope: rulepack.BusinessScope{BusinessLine: "telecom", Scenario: "review", ContractType: "service"},
		TenantID:      "tenant-demo", Comparator: "threshold", Threshold: rulepack.Threshold{Value: "100", Unit: "Mbit/s"},
		RequiredFact:     rulepack.RequiredFact{Dimension: "bandwidth", SourceRoles: []string{"contract"}, Exclusions: []string{}},
		AuthoritySources: []rulepack.AuthoritySource{{ID: "doc-1", Version: "1", Kind: "contract"}},
	}
}

func shadowTestResult(id string, relation rulepack.Relation, reason string) rulepack.Result {
	return rulepack.Result{RuleID: id, RuleVersion: "1", Domain: "contract", CapabilityID: "cap-1", Mandatory: true, Relation: relation, ReasonCode: reason, FactIDs: []string{"fact-" + id}}
}

func TestShadowScopeMatchesFrozenBusinessScope(t *testing.T) {
	item := Case{TenantID: "tenant-demo", BusinessLine: "telecom", Scenario: "review", ContractType: "service"}
	rules := []rulepack.Rule{shadowTestRule("rule-a")}
	if !shadowScopeMatches(rules, item) {
		t.Fatal("matching business scope was rejected")
	}
	if shadowScopeMatches(nil, item) {
		t.Fatal("empty rule set matched")
	}
	cases := []struct {
		name  string
		rule  rulepack.Rule
		item  Case
		match bool
	}{
		{"tenant", rulepack.Rule{RuleID: "rule-a", TenantID: "tenant-other", BusinessScope: rules[0].BusinessScope}, item, false},
		{"line", rules[0], Case{TenantID: "tenant-demo", BusinessLine: "finance", Scenario: "review", ContractType: "service"}, false},
		{"scenario", rules[0], Case{TenantID: "tenant-demo", BusinessLine: "telecom", Scenario: "intake", ContractType: "service"}, false},
		{"contract type", rules[0], Case{TenantID: "tenant-demo", BusinessLine: "telecom", Scenario: "review", ContractType: "lease"}, false},
	}
	for _, check := range cases {
		if shadowScopeMatches([]rulepack.Rule{check.rule}, check.item) != check.match {
			t.Fatalf("case %s produced the wrong scope decision", check.name)
		}
	}
}

func TestValidateShadowSamplesBoundsAndDeduplicates(t *testing.T) {
	sample := func(index int) RuleShadowSampleRef {
		return RuleShadowSampleRef{CaseID: fmt.Sprintf("case_%02d", index), RunID: fmt.Sprintf("run_%02d", index), AttemptID: fmt.Sprintf("att_%02d", index)}
	}
	limit := make([]RuleShadowSampleRef, 0, shadowSampleLimit)
	for i := 0; i < shadowSampleLimit; i++ {
		limit = append(limit, sample(i))
	}
	if err := validateShadowSamples(limit); err != nil {
		t.Fatalf("valid sample list was rejected: %v", err)
	}
	if err := validateShadowSamples(nil); err == nil {
		t.Fatal("empty sample list was accepted")
	}
	if err := validateShadowSamples(append(append([]RuleShadowSampleRef{}, limit...), sample(99))); err == nil {
		t.Fatal("oversized sample list was accepted")
	}
	if err := validateShadowSamples([]RuleShadowSampleRef{limit[0], limit[0]}); err == nil {
		t.Fatal("duplicate sample was accepted")
	}
	invalid := []RuleShadowSampleRef{
		{CaseID: "", RunID: "run_01", AttemptID: "att_01"},
		{CaseID: "case/one", RunID: "run_01", AttemptID: "att_01"},
		{CaseID: "case_01", RunID: "run 01", AttemptID: "att_01"},
		{CaseID: "case_01", RunID: "run_01", AttemptID: "\x7f"},
	}
	for _, ref := range invalid {
		if err := validateShadowSamples([]RuleShadowSampleRef{ref}); err == nil {
			t.Fatalf("invalid sample %+v was accepted", ref)
		}
	}
}

func TestShadowBaselineMatchesFrozenEvaluations(t *testing.T) {
	citation := rulepack.Citation{SourceID: "doc-1", SourceVersion: "1", SourceDigest: "sha256:aa", Start: 0, End: 3, Quote: "abc"}
	baseline := rulepack.Report{Results: []rulepack.Result{shadowTestResult("rule-a", rulepack.RelationCompatible, "within_threshold")}}
	baseline.Results[0].Citations = []rulepack.Citation{citation}
	evidence := EvaluationEvidence{FactID: "fact-rule-a", Ref: ExperienceEvidenceRef{DocumentID: citation.SourceID, ParsedDocumentID: citation.SourceVersion, NormalizedDigest: citation.SourceDigest, Quote: citation.Quote, Start: citation.Start, End: citation.End}}
	evaluation := ConstraintEval{
		RuleID: "rule-a", RuleVersion: "1", CapabilityID: "cap-1", Domain: "contract", Mandatory: true,
		Relation: string(rulepack.RelationCompatible), ReasonCode: "within_threshold",
		FactIDs: []string{"fact-rule-a"}, Evidence: []EvaluationEvidence{evidence},
	}
	if !shadowBaselineMatches([]ConstraintEval{evaluation}, baseline) {
		t.Fatal("matching frozen evaluation and baseline did not compare equal")
	}
	mutations := []struct {
		name   string
		mutate func(*ConstraintEval)
	}{
		{"missing rule id", func(item *ConstraintEval) { item.RuleID = "" }},
		{"version", func(item *ConstraintEval) { item.RuleVersion = "2" }},
		{"capability", func(item *ConstraintEval) { item.CapabilityID = "cap-2" }},
		{"domain", func(item *ConstraintEval) { item.Domain = "other" }},
		{"mandatory", func(item *ConstraintEval) { item.Mandatory = false }},
		{"relation", func(item *ConstraintEval) { item.Relation = string(rulepack.RelationConflict) }},
		{"reason", func(item *ConstraintEval) { item.ReasonCode = "other" }},
		{"facts", func(item *ConstraintEval) { item.FactIDs = []string{"fact-other"} }},
		{"evidence quote", func(item *ConstraintEval) { item.Evidence[0].Ref.Quote = "xyz" }},
		{"extra distinct evidence", func(item *ConstraintEval) {
			item.Evidence = append(item.Evidence, EvaluationEvidence{Ref: ExperienceEvidenceRef{DocumentID: citation.SourceID, ParsedDocumentID: citation.SourceVersion, NormalizedDigest: citation.SourceDigest, Quote: "xyz", Start: 0, End: 3}})
		}},
	}
	for _, check := range mutations {
		item := evaluation
		item.Evidence = append([]EvaluationEvidence{}, evaluation.Evidence...)
		check.mutate(&item)
		if shadowBaselineMatches([]ConstraintEval{item}, baseline) {
			t.Fatalf("evaluation with %s mismatch compared equal", check.name)
		}
	}
	if shadowBaselineMatches(nil, baseline) {
		t.Fatal("missing evaluations compared equal to a baseline")
	}
	if shadowBaselineMatches([]ConstraintEval{evaluation, evaluation}, baseline) {
		t.Fatal("duplicate rule evaluations compared equal")
	}
	if shadowBaselineMatches([]ConstraintEval{evaluation}, rulepack.Report{}) {
		t.Fatal("empty baseline compared equal to a populated evaluation")
	}
}

func TestShadowRuleFactsComparableCoversEveryDefinitionField(t *testing.T) {
	base := shadowTestRule("rule-a")
	if !shadowRuleFactsComparable(base, base) {
		t.Fatal("identical rule definitions compared unequal")
	}
	cases := []struct {
		name   string
		mutate func(*rulepack.Rule)
	}{
		{"comparator", func(rule *rulepack.Rule) { rule.Comparator = "other" }},
		{"domain", func(rule *rulepack.Rule) { rule.Domain = "other" }},
		{"capability", func(rule *rulepack.Rule) { rule.CapabilityID = "cap-2" }},
		{"scope", func(rule *rulepack.Rule) { rule.BusinessScope.Scenario = "other" }},
		{"required fact", func(rule *rulepack.Rule) { rule.RequiredFact.Dimension = "other" }},
		{"authority sources", func(rule *rulepack.Rule) { rule.AuthoritySources = nil }},
		{"legal authorities", func(rule *rulepack.Rule) {
			rule.LegalAuthorities = []rulepack.LegalAuthorityReference{{KnowledgeID: "knowledge-1"}}
		}},
	}
	for _, check := range cases {
		candidate := base
		candidate.RequiredFact.SourceRoles = []string{"contract"}
		candidate.RequiredFact.Exclusions = []string{}
		candidate.AuthoritySources = append([]rulepack.AuthoritySource{}, base.AuthoritySources...)
		check.mutate(&candidate)
		if shadowRuleFactsComparable(base, candidate) {
			t.Fatalf("rule with %s change compared comparable", check.name)
		}
	}
}

func TestCompareShadowResultsReportsEveryChangeClass(t *testing.T) {
	base := rulepack.Manifest{Rules: []rulepack.Rule{shadowTestRule("rule-a"), shadowTestRule("rule-b"), shadowTestRule("rule-c"), shadowTestRule("rule-d")}}
	candidate := rulepack.Manifest{Rules: []rulepack.Rule{shadowTestRule("rule-a"), shadowTestRule("rule-c"), shadowTestRule("rule-d"), shadowTestRule("rule-e")}}
	candidate.Rules[1].Comparator = "changed-comparator"
	before := rulepack.Report{Results: []rulepack.Result{
		shadowTestResult("rule-a", rulepack.RelationCompatible, "within_threshold"),
		shadowTestResult("rule-b", rulepack.RelationConflict, "above_threshold"),
		shadowTestResult("rule-c", rulepack.RelationCompatible, "within_threshold"),
		shadowTestResult("rule-d", rulepack.RelationCompatible, "within_threshold"),
	}}
	after := rulepack.Report{Results: []rulepack.Result{
		shadowTestResult("rule-a", rulepack.RelationCompatible, "within_threshold"),
		shadowTestResult("rule-c", rulepack.RelationCompatible, "within_threshold"),
		shadowTestResult("rule-d", rulepack.RelationConflict, "above_threshold"),
		shadowTestResult("rule-e", rulepack.RelationCompatible, "within_threshold"),
	}}
	changes, status := compareShadowResults(base, candidate, before, after)
	if status != "partial" {
		t.Fatalf("mixed changes reported status %q", status)
	}
	if len(changes) != 4 {
		t.Fatalf("expected four changes, got %+v", changes)
	}
	want := map[string]string{"rule-b": "removed", "rule-c": "unavailable", "rule-d": "changed", "rule-e": "unavailable"}
	for _, change := range changes {
		if want[change.RuleID] != change.Kind {
			t.Fatalf("change %s reported kind %q, want %q", change.RuleID, change.Kind, want[change.RuleID])
		}
		if change.RuleID == "rule-d" {
			if change.Detail != "" || change.BaseRelation != string(rulepack.RelationCompatible) || change.CandidateRelation != string(rulepack.RelationConflict) {
				t.Fatalf("relation change lost its before/after identity: %+v", change)
			}
			continue
		}
		if change.Detail == "" {
			t.Fatalf("change %s is missing an explanation", change.RuleID)
		}
	}
	if changes[0].RuleID != "rule-b" || changes[1].RuleID != "rule-c" || changes[2].RuleID != "rule-d" || changes[3].RuleID != "rule-e" {
		t.Fatalf("changes are not sorted by rule id: %+v", changes)
	}

	versionBase := rulepack.Manifest{Rules: []rulepack.Rule{shadowTestRule("rule-a")}}
	versionCandidate := rulepack.Manifest{Rules: []rulepack.Rule{shadowTestRule("rule-a")}}
	versionCandidate.Rules[0].Version = "2"
	versionAfter := shadowTestResult("rule-a", rulepack.RelationCompatible, "within_threshold")
	versionAfter.RuleVersion = "2"
	versionChanges, versionStatus := compareShadowResults(versionBase, versionCandidate,
		rulepack.Report{Results: []rulepack.Result{shadowTestResult("rule-a", rulepack.RelationCompatible, "within_threshold")}},
		rulepack.Report{Results: []rulepack.Result{versionAfter}})
	if versionStatus != "completed" || len(versionChanges) != 1 || versionChanges[0].Kind != "changed" || versionChanges[0].Detail != "规则版本、强制属性、适用依据或引用身份发生变化" {
		t.Fatalf("version-only change was classified as %+v (%s)", versionChanges, versionStatus)
	}

	unchanged, unchangedStatus := compareShadowResults(base, base, before, before)
	if len(unchanged) != 0 || unchangedStatus != "completed" {
		t.Fatalf("identical reports produced changes: %+v (%s)", unchanged, unchangedStatus)
	}

	result := RuleShadowSampleResult{}
	shadowFailure(&result, "code-1", "message")
	if result.Status != "failed" || result.ErrorCode != "code-1" || result.Message != "message" {
		t.Fatalf("shadowFailure did not mark the result: %+v", result)
	}
}

func TestFreezeRuleRequestBindsEveryFrozenField(t *testing.T) {
	request := rulepack.Request{
		Principal:    access.Principal{UserID: "user-1", TenantID: "tenant-demo"},
		Resource:     access.Resource{TenantID: "tenant-demo", CaseID: "case-1"},
		BusinessLine: "telecom", Scenario: "review", ContractType: "service",
		At:             time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC),
		Sources:        []rulepack.Source{{ID: "doc-1", Version: "1", Role: "contract", Text: "source text"}},
		Facts:          []rulepack.Fact{{ID: "fact-1", RuleID: "rule-a", Dimension: "bandwidth", Value: "200"}},
		AuthorityGates: []rulepack.AuthorityGate{{RuleID: "rule-a", SourceID: "doc-1", Outcome: "pass"}},
	}
	frozen := freezeRuleRequest(request)
	request.Facts[0].Value = "mutated"
	request.Sources[0].Text = "mutated"
	request.AuthorityGates[0].Outcome = "mutated"
	if frozen.Encoding != "rulepack-request/v1" || frozen.Facts[0].Value != "200" || frozen.Sources[0].Text != "source text" || frozen.AuthorityGates[0].Outcome != "pass" {
		t.Fatalf("frozen request shares storage or lost fields: %+v", frozen)
	}
	bound := frozen.bind(request)
	if bound.Principal.UserID != "user-1" || bound.Resource.CaseID != "case-1" {
		t.Fatal("binding a frozen request dropped the principal or resource")
	}
	if bound.BusinessLine != "telecom" || bound.Scenario != "review" || bound.ContractType != "service" || !bound.At.Equal(time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)) ||
		bound.Facts[0].Value != "200" || bound.Sources[0].Text != "source text" || bound.AuthorityGates[0].Outcome != "pass" {
		t.Fatalf("binding did not restore the frozen fields: %+v", bound)
	}
}
