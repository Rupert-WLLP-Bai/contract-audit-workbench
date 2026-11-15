// File metadata (manifest mirror):
// Purpose: Exercises the rulepack fixture-suite validator, runner and version comparison with synthetic demo inputs, including malformed-suite rejection and lifecycle handling.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package rulepack

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func fixtureInputFromDemo(t *testing.T, mutate func(*FixtureInput)) FixtureInput {
	t.Helper()
	var request Request
	if err := json.Unmarshal(fixture(t, "demo-request.json"), &request); err != nil {
		t.Fatal(err)
	}
	input := FixtureInput{
		BusinessLine: request.BusinessLine,
		Scenario:     request.Scenario,
		ContractType: request.ContractType,
		At:           request.At.UTC().Format(time.RFC3339),
	}
	for _, source := range request.Sources {
		input.Sources = append(input.Sources, FixtureSource{
			ID: source.ID, Version: source.Version, Role: source.Role, Text: source.Text,
		})
	}
	for _, fact := range request.Facts {
		converted := FixtureFact{
			ID: fact.ID, RuleID: fact.RuleID, Dimension: fact.Dimension, ObjectRef: fact.ObjectRef,
			Value: fact.Value, Unit: fact.Unit, SourceRole: fact.SourceRole,
			MeasurementWindow: fact.MeasurementWindow, TimeZone: fact.TimeZone,
			Calendar: fact.Calendar, OutageDefinition: fact.OutageDefinition,
			Basis: fact.Basis, Applicable: fact.Applicable,
		}
		for _, citation := range fact.Citations {
			converted.Citations = append(converted.Citations, FixtureCitation{
				SourceID: citation.SourceID, SourceVersion: citation.SourceVersion,
				Start: citation.Start, End: citation.End, Quote: citation.Quote,
			})
		}
		input.Facts = append(input.Facts, converted)
	}
	if mutate != nil {
		mutate(&input)
	}
	return input
}

func fixtureSetFact(input *FixtureInput, factID, value string) {
	for i := range input.Facts {
		if input.Facts[i].ID == factID {
			input.Facts[i].Value = value
		}
	}
}

// fixtureSetFactText keeps the synthetic source, citation span and fact value
// consistent so the engine can support the value from the quoted evidence.
func fixtureSetFactText(input *FixtureInput, factID, value, text string) {
	for i := range input.Facts {
		if input.Facts[i].ID != factID {
			continue
		}
		input.Facts[i].Value = value
		for j := range input.Facts[i].Citations {
			input.Facts[i].Citations[j].Start = 0
			input.Facts[i].Citations[j].End = utf8.RuneCountInString(text)
			input.Facts[i].Citations[j].Quote = text
			for k := range input.Sources {
				if input.Sources[k].ID == input.Facts[i].Citations[j].SourceID {
					input.Sources[k].Text = text
				}
			}
		}
	}
}

func fixtureDropFact(input *FixtureInput, factID string) {
	facts := input.Facts[:0]
	for _, fact := range input.Facts {
		if fact.ID != factID {
			facts = append(facts, fact)
		}
	}
	input.Facts = facts
}

func fixtureExpectedFromReport(t *testing.T, report Report) []FixtureExpected {
	t.Helper()
	expected := make([]FixtureExpected, 0, len(report.Results))
	for _, result := range report.Results {
		item := FixtureExpected{
			RuleID: result.RuleID, RuleVersion: result.RuleVersion,
			Relation: result.Relation, ReasonCode: result.ReasonCode,
			Citations: []FixtureCitation{},
		}
		for _, citation := range result.Citations {
			item.Citations = append(item.Citations, FixtureCitation{
				SourceID: citation.SourceID, SourceVersion: citation.SourceVersion,
				Start: citation.Start, End: citation.End, Quote: citation.Quote,
			})
		}
		expected = append(expected, item)
	}
	return expected
}

func fixtureExpectedForInput(t *testing.T, pack *Pack, input FixtureInput) []FixtureExpected {
	t.Helper()
	executable, err := fixtureExecutable(pack)
	if err != nil {
		t.Fatal(err)
	}
	request, err := fixtureRequest(pack.content.TenantID, input)
	if err != nil {
		t.Fatal(err)
	}
	report, err := executable.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return fixtureExpectedFromReport(t, report)
}

func fixtureCaseFor(t *testing.T, pack *Pack, id, title, kind string, input FixtureInput) FixtureCase {
	t.Helper()
	return FixtureCase{ID: id, Title: title, Kind: kind, Input: input,
		Expected: fixtureExpectedForInput(t, pack, input)}
}

func fixtureDemoSuite(t *testing.T) *FixtureSuite {
	t.Helper()
	pack := demoDraft(t)
	positive := fixtureInputFromDemo(t, func(input *FixtureInput) {
		fixtureSetFactText(input, "fact-bandwidth", "200", "专线甲最低带宽承诺为200 Mbit/s。")
		fixtureSetFactText(input, "fact-advance", "0.05", "专线甲合同总额为预付款计算基础，预付款比例为0.05。")
	})
	negative := fixtureInputFromDemo(t, nil)
	insufficient := fixtureInputFromDemo(t, func(input *FixtureInput) {
		fixtureDropFact(input, "fact-sla")
	})
	notApplicable := fixtureInputFromDemo(t, func(input *FixtureInput) {
		fixtureSetFactText(input, "fact-bandwidth", "200", "专线甲最低带宽承诺为200 Mbit/s。")
		fixtureSetFactText(input, "fact-advance", "0.05", "专线甲合同总额为预付款计算基础，预付款比例为0.05。")
	})
	return &FixtureSuite{
		SchemaVersion: fixtureSchemaVersion,
		ID:            "line-demo-fixtures",
		PackID:        pack.content.ID,
		PackVersion:   pack.content.Version,
		DataClass:     "synthetic",
		Cases: []FixtureCase{
			fixtureCaseFor(t, pack, "case-positive", "合规样本", "positive", positive),
			fixtureCaseFor(t, pack, "case-negative", "冲突样本", "negative", negative),
			fixtureCaseFor(t, pack, "case-insufficient", "信息不足样本", "insufficient", insufficient),
			fixtureCaseFor(t, pack, "case-not-applicable", "不适用样本", "positive", notApplicable),
		},
	}
}

func fixtureManifestWithSuite(t *testing.T, suite *FixtureSuite, adjust func(map[string]any)) *Pack {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(fixture(t, "demo.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if suite != nil {
		raw, err := json.Marshal(suite)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		manifest["fixture_suite"] = decoded
	}
	if adjust != nil {
		adjust(manifest)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

func TestFixtureSuiteRunsEveryDeclaredCaseAndComparesVersions(t *testing.T) {
	suite := fixtureDemoSuite(t)
	pack := fixtureManifestWithSuite(t, suite, nil)
	run, err := RunFixtureSuite(context.Background(), pack)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Passed {
		t.Fatalf("fixture run failed: %+v", run.Cases)
	}
	if run.SuiteID != suite.ID || run.PackID != pack.content.ID || run.PackVersion != pack.content.Version || run.PackHash != pack.Hash() {
		t.Fatalf("fixture run identity mismatch: %+v", run)
	}
	if len(run.Cases) != len(suite.Cases) {
		t.Fatalf("fixture run reports %d cases, want %d", len(run.Cases), len(suite.Cases))
	}
	for _, item := range run.Cases {
		if !item.Passed || len(item.Failures) != 0 || item.Report == nil {
			t.Fatalf("fixture case %s did not pass cleanly: %+v", item.ID, item)
		}
	}

	baseline := fixtureManifestWithSuite(t, nil, nil)
	comparison, err := CompareFixtureSuite(context.Background(), pack, baseline, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Changes) != 0 || !comparison.Baseline.Passed {
		t.Fatalf("identical baseline reported changes: %+v", comparison)
	}

	relaxed := fixtureManifestWithSuite(t, nil, func(manifest map[string]any) {
		rules := manifest["rules"].([]any)
		for _, raw := range rules {
			rule := raw.(map[string]any)
			if rule["rule_id"] == "ENT-TECH-BW-001" {
				rule["threshold"].(map[string]any)["value"] = "40"
			}
		}
	})
	changed, err := CompareFixtureSuite(context.Background(), pack, relaxed, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Changes) == 0 {
		t.Fatal("relaxed baseline did not report a relation change")
	}
	for _, change := range changed.Changes {
		if change.Before == nil || change.After == nil {
			t.Fatalf("expected before/after results for %s: %+v", change.CaseID, change)
		}
	}

	manifest := pack.Manifest()
	if manifest.FixtureSuite == nil || len(manifest.FixtureSuite.Cases) != len(suite.Cases) {
		t.Fatalf("manifest copy lost the fixture suite: %+v", manifest.FixtureSuite)
	}
	manifest.FixtureSuite.Cases[0].Input.Sources[0].Text = "mutated"
	if suite.Cases[0].Input.Sources[0].Text == "mutated" {
		t.Fatal("manifest copy shares fixture source storage with the pack")
	}
}

func TestFixtureSuiteCoversLifecycleStatesAndContextCancellation(t *testing.T) {
	pack := fixtureManifestWithSuite(t, fixtureDemoSuite(t), nil)
	validated, err := pack.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunFixtureSuite(context.Background(), validated); err != nil {
		t.Fatalf("validated pack did not produce an executable fixture run: %v", err)
	}
	approved, err := validated.Approve("fixture-approver", validated.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunFixtureSuite(context.Background(), approved); err != nil {
		t.Fatalf("approved pack did not produce an executable fixture run: %v", err)
	}
	published, err := approved.Publish()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunFixtureSuite(context.Background(), published); err != nil {
		t.Fatalf("published pack did not produce an executable fixture run: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunFixtureSuite(cancelled, published); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context returned %v", err)
	}
	if _, err := RunFixtureSuite(context.Background(), nil); err == nil {
		t.Fatal("nil pack returned no error")
	}
	if _, err := RunFixtureSuite(context.Background(), fixtureManifestWithSuite(t, nil, nil)); err == nil {
		t.Fatal("pack without fixture suite returned no error")
	}
}

func TestFixtureSuiteComparisonRejectsIdentityMismatches(t *testing.T) {
	pack := fixtureManifestWithSuite(t, fixtureDemoSuite(t), nil)
	run, err := RunFixtureSuite(context.Background(), pack)
	if err != nil {
		t.Fatal(err)
	}
	baseline := fixtureManifestWithSuite(t, nil, nil)
	if _, err := CompareFixtureSuite(context.Background(), nil, baseline, run); err == nil {
		t.Fatal("nil candidate returned no error")
	}
	if _, err := CompareFixtureSuite(context.Background(), pack, nil, run); err == nil {
		t.Fatal("nil baseline returned no error")
	}
	otherPack := fixtureManifestWithSuite(t, nil, func(manifest map[string]any) {
		manifest["id"] = "line-other"
	})
	if _, err := CompareFixtureSuite(context.Background(), pack, otherPack, run); err == nil {
		t.Fatal("pack identity mismatch returned no error")
	}
	tampered := run
	tampered.PackHash = strings.Repeat("0", 64)
	if _, err := CompareFixtureSuite(context.Background(), pack, baseline, tampered); err == nil {
		t.Fatal("tampered candidate hash returned no error")
	}
	short := run
	short.Cases = run.Cases[:1]
	if _, err := CompareFixtureSuite(context.Background(), pack, baseline, short); err == nil {
		t.Fatal("short candidate run returned no error")
	}
	renamed := run
	renamed.Cases = append([]FixtureCaseResult(nil), run.Cases...)
	renamed.Cases[0].ID = "case-renamed"
	if _, err := CompareFixtureSuite(context.Background(), pack, baseline, renamed); err == nil {
		t.Fatal("case mismatch returned no error")
	}
	withoutReport := run
	withoutReport.Cases = append([]FixtureCaseResult(nil), run.Cases...)
	withoutReport.Cases[0].Report = nil
	withoutReport.Cases[0].Failures = []string{"execution: synthetic failure"}
	if comparison, err := CompareFixtureSuite(context.Background(), pack, baseline, withoutReport); err != nil || len(comparison.Changes) == 0 || comparison.Changes[0].AfterError == "" {
		t.Fatalf("report-less case did not surface an error change: %+v %v", comparison, err)
	}
}

func TestFixtureSuiteValidationRejectsMalformedSuites(t *testing.T) {
	pack := fixtureManifestWithSuite(t, fixtureDemoSuite(t), nil)
	base := pack.content
	valid := *base.FixtureSuite

	copySuite := func(mutate func(*FixtureSuite)) *FixtureSuite {
		raw, err := json.Marshal(&valid)
		if err != nil {
			t.Fatal(err)
		}
		var copied FixtureSuite
		if err := json.Unmarshal(raw, &copied); err != nil {
			t.Fatal(err)
		}
		mutate(&copied)
		return &copied
	}
	content := func(suite *FixtureSuite) manifestContent {
		next := base
		next.FixtureSuite = suite
		return next
	}

	checks := []struct {
		name  string
		suite *FixtureSuite
	}{
		{"schema version", copySuite(func(suite *FixtureSuite) { suite.SchemaVersion = "v0" })},
		{"invalid id", copySuite(func(suite *FixtureSuite) { suite.ID = "bad id" })},
		{"pack id", copySuite(func(suite *FixtureSuite) { suite.PackID = "line-other" })},
		{"pack version", copySuite(func(suite *FixtureSuite) { suite.PackVersion = "2" })},
		{"data class", copySuite(func(suite *FixtureSuite) { suite.DataClass = "production" })},
		{"too few cases", copySuite(func(suite *FixtureSuite) { suite.Cases = suite.Cases[:2] })},
		{"duplicate case", copySuite(func(suite *FixtureSuite) { suite.Cases[1].ID = suite.Cases[0].ID })},
		{"empty title", copySuite(func(suite *FixtureSuite) { suite.Cases[0].Title = "" })},
		{"invalid kind", copySuite(func(suite *FixtureSuite) { suite.Cases[0].Kind = "unknown" })},
		{"missing kinds", copySuite(func(suite *FixtureSuite) {
			for i := range suite.Cases {
				suite.Cases[i].Kind = "positive"
				for j := range suite.Cases[i].Expected {
					if suite.Cases[i].Expected[j].Relation == RelationConflict || suite.Cases[i].Expected[j].Relation == RelationInsufficient {
						suite.Cases[i].Expected[j].Relation = RelationCompatible
						if suite.Cases[i].Expected[j].Citations == nil {
							suite.Cases[i].Expected[j].Citations = []FixtureCitation{}
						}
					}
				}
			}
		})},
		{"bad scope", copySuite(func(suite *FixtureSuite) { suite.Cases[0].Input.BusinessLine = "bad line" })},
		{"out of window", copySuite(func(suite *FixtureSuite) { suite.Cases[0].Input.At = "2028-01-01T00:00:00Z" })},
		{"no sources", copySuite(func(suite *FixtureSuite) { suite.Cases[0].Input.Sources = nil })},
		{"duplicate source", copySuite(func(suite *FixtureSuite) {
			suite.Cases[0].Input.Sources = append(suite.Cases[0].Input.Sources, suite.Cases[0].Input.Sources[0])
		})},
		{"duplicate fact", copySuite(func(suite *FixtureSuite) {
			suite.Cases[0].Input.Facts = append(suite.Cases[0].Input.Facts, suite.Cases[0].Input.Facts[0])
		})},
		{"expected count", copySuite(func(suite *FixtureSuite) { suite.Cases[0].Expected = suite.Cases[0].Expected[:2] })},
		{"unknown expected rule", copySuite(func(suite *FixtureSuite) { suite.Cases[0].Expected[0].RuleID = "ENT-UNKNOWN" })},
		{"invalid relation", copySuite(func(suite *FixtureSuite) { suite.Cases[0].Expected[0].Relation = "unknown" })},
		{"missing citations", copySuite(func(suite *FixtureSuite) {
			for i := range suite.Cases[0].Expected {
				if suite.Cases[0].Expected[i].Relation == RelationCompatible {
					suite.Cases[0].Expected[i].Citations = nil
				}
			}
		})},
		{"unknown citation source", copySuite(func(suite *FixtureSuite) {
			for i := range suite.Cases[0].Expected {
				if len(suite.Cases[0].Expected[i].Citations) > 0 {
					suite.Cases[0].Expected[i].Citations[0].SourceID = "doc-unknown"
				}
			}
		})},
		{"kind mismatch", copySuite(func(suite *FixtureSuite) {
			suite.Cases[0].Kind = "insufficient"
			for i := range suite.Cases[0].Expected {
				if suite.Cases[0].Expected[i].Relation == RelationInsufficient {
					suite.Cases[0].Expected[i].Relation = RelationCompatible
					if suite.Cases[0].Expected[i].Citations == nil {
						suite.Cases[0].Expected[i].Citations = []FixtureCitation{}
					}
				}
			}
		})},
	}
	if err := validateFixtureSuite(content(nil)); err != nil {
		t.Fatalf("absent fixture suite returned %v", err)
	}
	for _, check := range checks {
		if err := validateFixtureSuite(content(check.suite)); err == nil {
			t.Fatalf("fixture suite with %s was accepted", check.name)
		}
	}
	if err := validateFixtureSuite(content(copySuite(func(suite *FixtureSuite) {}))); err != nil {
		t.Fatalf("valid suite copy returned %v", err)
	}
}

func TestFixtureFailuresReportEveryMismatchClass(t *testing.T) {
	expected := []FixtureExpected{
		{RuleID: "rule-a", RuleVersion: "1", Relation: RelationCompatible, ReasonCode: "within_threshold"},
		{RuleID: "rule-b", RuleVersion: "1", Relation: RelationConflict, ReasonCode: "above_threshold",
			Citations: []FixtureCitation{{SourceID: "doc", SourceVersion: "1", Start: 0, End: 3, Quote: "abc"}}},
	}
	actual := []Result{
		{RuleID: "rule-a", RuleVersion: "2", Relation: RelationConflict, ReasonCode: "other",
			Citations: []Citation{{SourceID: "doc", SourceVersion: "1", Start: 0, End: 3, Quote: "xyz"}}},
		{RuleID: "rule-extra", RuleVersion: "1", Relation: RelationCompatible, ReasonCode: "within_threshold"},
	}
	failures := fixtureFailures(expected, actual)
	if len(failures) != 6 {
		t.Fatalf("expected six failures, got %+v", failures)
	}
	if len(fixtureFailures(nil, nil)) != 0 {
		t.Fatal("empty expectations reported failures")
	}
	if fixtureCitationsEqual(expected[1].Citations, actual[0].Citations) {
		t.Fatal("differing citation quotes compared equal")
	}
	if fixtureCitationsEqual(expected[1].Citations, []Citation{{SourceID: "doc", SourceVersion: "1", Start: 0, End: 3, Quote: "abc", TenantID: "t"}}) != true {
		t.Fatal("same citation identities compared unequal across evidence fields")
	}
}

func TestFixtureSuiteRejectsUnusablePacksDirectly(t *testing.T) {
	if _, err := fixtureExecutable(nil); err == nil {
		t.Fatal("nil pack was executable")
	}
	if _, err := fixtureExecutable(&Pack{lifecycle: Lifecycle{State: "rejected"}}); err == nil {
		t.Fatal("rejected lifecycle state was executable")
	}
	pack := fixtureManifestWithSuite(t, fixtureDemoSuite(t), nil)
	if _, err := runFixtureCases(nil, pack, pack.content.FixtureSuite); err == nil {
		t.Fatal("nil context was accepted")
	}
	broken := &Pack{content: manifestContent{ID: "line-demo", Version: "1", FixtureSuite: &FixtureSuite{ID: "suite-1"}}}
	if _, err := RunFixtureSuite(context.Background(), broken); err == nil {
		t.Fatal("invalid embedded suite was accepted by the runner")
	}
}

func TestFixtureCitationComparisonSortsEveryKey(t *testing.T) {
	got := []Citation{
		{SourceID: "b", SourceVersion: "1", Start: 0, End: 2, Quote: "b"},
		{SourceID: "a", SourceVersion: "2", Start: 0, End: 2, Quote: "a2"},
		{SourceID: "a", SourceVersion: "1", Start: 3, End: 4, Quote: "a1-late"},
		{SourceID: "a", SourceVersion: "1", Start: 1, End: 3, Quote: "a1-long"},
		{SourceID: "a", SourceVersion: "1", Start: 1, End: 2, Quote: "a1-z"},
		{SourceID: "a", SourceVersion: "1", Start: 1, End: 2, Quote: "a1-a"},
	}
	want := make([]FixtureCitation, 0, len(got))
	for i := len(got) - 1; i >= 0; i-- {
		want = append(want, FixtureCitation{SourceID: got[i].SourceID, SourceVersion: got[i].SourceVersion,
			Start: got[i].Start, End: got[i].End, Quote: got[i].Quote})
	}
	if !fixtureCitationsEqual(want, got) {
		t.Fatal("same citations in different orders compared unequal")
	}
}
