// File metadata (manifest mirror):
// Purpose: Tests terminal attempt and frozen rule-pack boundaries and requires exact document, parse, digest and Unicode-span matches for every citation on a model risk.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-19
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"strings"
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/rulepack"
)

func TestFrozenDecisionEvidenceRequiresEveryExactCitation(t *testing.T) {
	ir := docir.Build("😀采购文件。")
	digest := ir.NormalizedDigest
	docs := []FrozenDocument[hashTestOriginal]{{Document: hashTestOriginal{ID: "procurement"},
		ParsedDocumentID: "saved-parse", Ready: true, Text: ir.NormalizedText, NormalizedHash: &digest, IR: ir}}
	anchor := Anchor{DocumentID: "procurement", ParsedDocumentID: "saved-parse", NormalizedDigest: digest,
		Status: docir.StatusLocated, Quote: "采购", Start: 1, End: 3}
	if !FrozenDecisionEvidenceMatches([]Anchor{anchor}, docs) || FrozenDecisionEvidenceMatches(nil, docs) {
		t.Fatal("expected an exact nonempty Unicode citation")
	}
	for name, change := range map[string]func(*Anchor){
		"unselected document": func(a *Anchor) { a.DocumentID = "different-document" },
		"unselected parse":    func(a *Anchor) { a.ParsedDocumentID = "different-parse" },
		"missing parse":       func(a *Anchor) { a.ParsedDocumentID = "" },
		"different digest":    func(a *Anchor) { a.NormalizedDigest = "different-digest" },
		"missing digest":      func(a *Anchor) { a.NormalizedDigest = "" },
		"wrong text":          func(a *Anchor) { a.Quote = "合同" },
		"out of range":        func(a *Anchor) { a.End = 100 },
		"unlocated":           func(a *Anchor) { a.Status = docir.StatusUnlocatable },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := anchor
			change(&invalid)
			if FrozenDecisionEvidenceMatches([]Anchor{anchor, invalid}, docs) {
				t.Fatal("a second invalid citation was accepted")
			}
		})
	}
}

func TestFrozenTerminalPredicates(t *testing.T) {
	stamp, hash := "finished", "saved"
	if !RunMatchesCase(Run{CaseID: "case"}, "case") || RunMatchesCase(Run{CaseID: "other"}, "case") {
		t.Fatal("case identity not respected")
	}
	attempt := Attempt{RunID: "run", State: "partial", FinishedAt: &stamp, HistoryKind: "frozen", InputHash: &hash, OutputHash: &hash}
	if !AttemptTerminalForRun(attempt, "run") || !HasFrozenAttemptHashes(attempt) {
		t.Fatal("valid frozen partial attempt rejected")
	}
	attempt.FinishedAt = nil
	if AttemptTerminalForRun(attempt, "run") {
		t.Fatal("unfinished attempt accepted")
	}
	attempt.OutputHash = nil
	if HasFrozenAttemptHashes(attempt) {
		t.Fatal("missing output hash accepted")
	}
}
func TestFrozenRulePackBaselineAndFailureOrder(t *testing.T) {
	scope := FrozenRulePackScope{TenantID: "tenant"}
	if _, err := FrozenRulePack(Attempt{RulePackHash: "invented"}, scope); err == nil || err.Error() != "missing rulepack compiler identity" {
		t.Fatalf("compiler must fail first: %v", err)
	}
	baseline := Attempt{RulePackCompilerRef: "compiler"}
	manifest, err := FrozenRulePack(baseline, scope)
	if err != nil || manifest.TenantID != "" || len(manifest.Rules) != 0 {
		t.Fatalf("legacy baseline rejected: %+v %v", manifest, err)
	}
	baseline.RulePackHash = "invented"
	if _, err := FrozenRulePack(baseline, scope); err == nil || !strings.Contains(err.Error(), "invented hashes") {
		t.Fatalf("invented baseline hash accepted: %v", err)
	}
	baseline.RulePackSnapshot = []byte(`{}`)
	baseline.RulePackSnapshotDigest = "wrong"
	if _, err := FrozenRulePack(baseline, scope); err == nil || err.Error() != "raw rulepack snapshot digest mismatch" {
		t.Fatalf("raw digest must precede parse: %v", err)
	}
}

func TestFrozenManifestRejectsCrossScopeRule(t *testing.T) {
	scope := FrozenRulePackScope{TenantID: "tenant", BusinessLine: "ENT", Scenario: "scenario", ContractType: "service"}
	manifest := rulepack.Manifest{TenantID: "tenant", Lifecycle: rulepack.Lifecycle{State: rulepack.StatePublished}, Rules: []rulepack.Rule{{TenantID: "tenant", BusinessScope: rulepack.BusinessScope{BusinessLine: "ENT", Scenario: "other", ContractType: "service"}}}}
	if err := validateFrozenManifestScope(manifest, scope); err == nil || err.Error() != "rulepack business scope mismatch" {
		t.Fatalf("cross-scope rule accepted: %v", err)
	}
	manifest.TenantID = "other"
	if err := validateFrozenManifestScope(manifest, scope); err == nil || err.Error() != "rulepack scope or lifecycle mismatch" {
		t.Fatalf("manifest tenant must precede rule scope: %v", err)
	}
}
