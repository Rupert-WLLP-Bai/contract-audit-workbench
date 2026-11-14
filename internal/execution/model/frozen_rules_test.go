// File metadata (manifest mirror):
// Purpose: Pins terminal and frozen rule-pack failure ordering, historical baseline compatibility and tenant or business-scope rejection.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"strings"
	"testing"

	"contractaudit/internal/rulepack"
)

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
