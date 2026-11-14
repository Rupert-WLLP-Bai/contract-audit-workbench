// File metadata (manifest mirror):
// Purpose: Pins v1 snapshot bytes and hashes, legacy nil values, Legal reuse identity, applicability intervals, and optional selection limits.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type testLegalAuthority struct {
	RuleID string `json:"rule_id"`
}

func TestReviewSnapshotV1CanonicalShapeAndLegacyNil(t *testing.T) {
	snapshot := ReviewKnowledgeSnapshot[testLegalAuthority]{Encoding: "review-knowledge-snapshot/v1", BusinessAt: "2026-01-01T00:00:00Z", Scope: KnowledgeScope{BusinessLine: "ENT", Scenario: "line", ContractType: "service"}, AuthorizationRevision: strings.Repeat("a", 64), RequiredPolicies: []GovernedKnowledgeSelection{}, OptionalCandidates: []GovernedKnowledgeSelection{}, Excluded: []GovernedKnowledgeExclusion{}}
	const prefix = `{"encoding":"review-knowledge-snapshot/v1","business_at":"2026-01-01T00:00:00Z","scope":{"business_line":"ENT","scenario":"line","contract_type":"service"},"authorization_revision":"`
	const suffix = `","rule_pack":{"pack_id":"","version":"","content_sha256":"","snapshot_sha256":"","compiler_ref":"","pointer_revision":0},"required_policies":[],"optional_candidates":[],"excluded":[],"optional_truncated":false,"snapshot_sha256":""}`
	expectedBytes := prefix + strings.Repeat("a", 64) + suffix
	sealed, err := SealReviewSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.SnapshotSHA256 != SHA256([]byte(expectedBytes)) {
		t.Fatalf("canonical v1 hash differs: %s", sealed.SnapshotSHA256)
	}
	raw, err := json.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCanonicalReviewSnapshot[testLegalAuthority](raw)
	if err != nil || decoded == nil || decoded.SnapshotSHA256 != sealed.SnapshotSHA256 {
		t.Fatalf("decode=%+v err=%v", decoded, err)
	}
	if _, err = DecodeCanonicalReviewSnapshot[testLegalAuthority](append([]byte(" "), raw...)); err == nil {
		t.Fatal("non-canonical bytes accepted")
	}
	legacy, err := DecodeCanonicalReviewSnapshot[testLegalAuthority](nil)
	if err != nil || legacy != nil {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	snapshot.RequiredPolicies = nil
	nilSealed, err := SealReviewSnapshot(snapshot)
	if err != nil || nilSealed.SnapshotSHA256 == sealed.SnapshotSHA256 {
		t.Fatal("nil and empty arrays collapsed")
	}
}
func TestReviewSnapshotLegalIdentityUsesValidatedFingerprintAndOtherFields(t *testing.T) {
	a := ReviewKnowledgeSnapshot[testLegalAuthority]{BusinessAt: "2026-01-01", LegalReuseFingerprint: "fingerprint", EvidenceCutoffMode: "acceptance_time", EvidenceCutoffAt: "first", LegalAuthorities: []testLegalAuthority{{RuleID: "a"}}, SnapshotSHA256: "one"}
	b := a
	b.EvidenceCutoffAt = "second"
	b.SnapshotSHA256 = "two"
	if !SameReviewSnapshotIdentity(&a, &b, true, true) || SameReviewSnapshotIdentity(&a, &b, false, true) {
		t.Fatal("legal fingerprint validation/reuse differs")
	}
	b.BusinessAt = "different"
	if SameReviewSnapshotIdentity(&a, &b, true, true) {
		t.Fatal("changed business time reused")
	}
	b = a
	b.LegalReuseFingerprint = "other"
	if SameReviewSnapshotIdentity(&a, &b, true, true) {
		t.Fatal("changed fingerprint reused")
	}
}
func TestKnowledgeAppliesHalfOpenAndMissingCaseFact(t *testing.T) {
	content := GovernedKnowledgeContent{Scope: KnowledgeScope{BusinessLine: "ENT", Scenario: "line", ContractType: "service"}, ValidFrom: "2026-01-01T00:00:00Z", ValidUntil: "2027-01-01T00:00:00Z", Applicability: []KnowledgeCondition{{Field: "business_status", Equals: "open"}}}
	item := ReviewCaseFacts{OrgID: "org", CaseID: "case", BusinessLine: "ENT", Scenario: "line", ContractType: "service", BusinessStatus: "open"}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if ok, err := KnowledgeApplies(content, item, at); err != nil || !ok {
		t.Fatalf("begin: %v %v", ok, err)
	}
	if ok, err := KnowledgeApplies(content, item, at.AddDate(1, 0, 0)); err != nil || ok {
		t.Fatalf("end: %v %v", ok, err)
	}
	item.BusinessStatus = ""
	if _, err := KnowledgeApplies(content, item, at); err == nil {
		t.Fatal("missing condition fact accepted")
	}
}

func TestReviewSelectionsSortAndLimitKeepsMandatory(t *testing.T) {
	makeVersion := func(id, kind string) GovernedKnowledgeVersion {
		return GovernedKnowledgeVersion{Ref: KnowledgeRef{KnowledgeID: id}, Content: GovernedKnowledgeContent{Kind: kind}}
	}
	policies := []GovernedKnowledgeVersion{makeVersion("z", "policy"), makeVersion("a", "policy")}
	optional := []GovernedKnowledgeVersion{makeVersion("z", "guidance"), makeVersion("a", "guidance")}
	SortReviewCandidates(policies, optional)
	snapshot := ReviewKnowledgeSnapshot[testLegalAuthority]{RequiredPolicies: []GovernedKnowledgeSelection{}, OptionalCandidates: []GovernedKnowledgeSelection{}, Excluded: []GovernedKnowledgeExclusion{}}
	AddReviewSelections(&snapshot, policies, optional, 1)
	if len(snapshot.RequiredPolicies) != 2 || snapshot.RequiredPolicies[0].Ref.KnowledgeID != "a" || len(snapshot.OptionalCandidates) != 1 || snapshot.OptionalCandidates[0].Ref.KnowledgeID != "a" || len(snapshot.Excluded) != 1 || snapshot.Excluded[0].Ref.KnowledgeID != "z" || snapshot.Excluded[0].Reason != "optional_budget" || !snapshot.OptionalTruncated {
		t.Fatalf("selection=%+v", snapshot)
	}
}
