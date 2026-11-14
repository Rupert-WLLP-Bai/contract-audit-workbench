// File metadata (manifest mirror):
// Purpose: Pins temporal applicability selection, unknown outcomes, party role matching, and exact Subject pin freshness.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"strings"
	"testing"
	"time"
)

func TestPartyPinRequiresExactSubjectAndScope(t *testing.T) {
	pin := LegalApplicabilityPartyPin{Side: "seller", BindingID: "b", BindingVersion: 2, SubjectID: "s", SubjectVersion: 3}
	facts := PartyBindingFacts{BindingVersion: 2, SubjectVersion: 3, CurrentSubjectVersion: 3, SubjectID: "s", Role: "supplier", State: "active"}
	if PartyPinStale(pin, facts) || !PartyScopeAmbiguous(pin, 2) {
		t.Fatal("fresh seller or ambiguity was misclassified")
	}
	pin.ScopeNote = "指定本主体"
	if PartyScopeAmbiguous(pin, 2) {
		t.Fatal("explicit scope still ambiguous")
	}
	facts.CurrentSubjectVersion = 4
	if !PartyPinStale(pin, facts) {
		t.Fatal("changed subject version remained fresh")
	}
	if !PartyRoleMatches("buyer", "customer") || PartyRoleMatches("buyer", "supplier") || !PartyRoleMatches("seller", "service_provider") {
		t.Fatal("party role mapping changed")
	}
}
func TestFactSelectionPinsBusinessTimeAndCutoff(t *testing.T) {
	jan := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	june := jan.AddDate(0, 5, 0)
	dec := jan.AddDate(1, 0, 0)
	acquired := june.AddDate(0, 1, 0)
	rows := []LegalApplicabilityFactVersion{{Version: 1, Status: "established", Value: "old", TemporalBasis: "historical_interval", ValidFrom: jan, ValidUntil: &june}, {Version: 2, Status: "established", Value: "new", TemporalBasis: "historical_interval", ValidFrom: june, ValidUntil: &dec, BasisObtainedAt: &acquired}}
	if got := SelectLegalApplicabilityFactVersion(2, rows, june, june); got.Version.Version != 1 || got.UnknownReason != "expired_basis" {
		t.Fatalf("before evidence=%+v", got)
	}
	if got := SelectLegalApplicabilityFactVersion(2, rows, june, acquired); got.Version.Version != 2 || got.UnknownReason != "" {
		t.Fatalf("after evidence=%+v", got)
	}
}
func TestLegalReuseRejectsAlteredCitationAndPreservesExplicitCutoff(t *testing.T) {
	authority := FrozenLegalAuthority{RuleID: "r", AuthoritySourceID: "law", AuthoritySourceVersion: "1", RulePackID: "pack", RulePackVersion: "1", RulePackContentSHA256: strings.Repeat("a", 64), LegacyRuleGap: true, Outcome: "unknown", OutcomeReason: "legal_reference_missing", LegalReviewStatus: "NOT_RUN"}
	fields := LegalReuseFields{EvidenceCutoffAt: "2026-01-01T00:00:00Z", EvidenceCutoffMode: "explicit", ExplicitEvidenceCutoffAt: "2026-01-01T00:00:00Z", LegalAuthorities: []FrozenLegalAuthority{authority}}
	if err := PrepareLegalReuse(&fields); err != nil || fields.LegalReuseFingerprint == "" || fields.LegalAuthorities[0].CitationID == "" {
		t.Fatalf("fields=%+v err=%v", fields, err)
	}
	fields.LegalAuthorities[0].CitationID = strings.Repeat("0", 64)
	if err := PrepareLegalReuse(&fields); err == nil {
		t.Fatal("forged citation accepted")
	}
}
