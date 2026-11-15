// File metadata (manifest mirror):
// Purpose: Checks exact Unicode clause replacement, complete document preservation, and refusal of unverified or unconfirmed changes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package contract

import "testing"

func TestVerifyDerivedClauseTextUsesUnicodeOffsetsAndFullDocument(t *testing.T) {
	original := "甲方🙂付款三十日。尾文"
	evidence := ClauseEvidence{Start: 3, End: 8, Quote: "付款三十日"}
	derived, err := ReplaceClause(original, evidence, "付款六十日")
	if err != nil || derived != "甲方🙂付款六十日。尾文" {
		t.Fatalf("derived=%q err=%v", derived, err)
	}
	if err := VerifyDerivedClauseText(original, evidence, "付款六十日", derived); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDerivedClauseText(original, evidence, "付款六十日", derived+"附加内容"); err == nil {
		t.Fatal("accepted extra document content")
	}
	evidence.Start++
	if _, err := ReplaceClause(original, evidence, "付款六十日"); err == nil {
		t.Fatal("accepted mismatched Unicode position")
	}
}

func TestUnverifiedProposalCannotProduceRevision(t *testing.T) {
	proposal := ClauseProposal{Status: "pending", Version: 1, ProposedText: "替换"}
	if _, err := DerivedClauseText(proposal, 1, "原文", nil); err == nil {
		t.Fatal("unverified candidate produced derived text")
	}
	if err := ValidateClauseProposalDecision(ClauseProposalDecision{Action: "reject", ExpectedVersion: 1}); err == nil {
		t.Fatal("decision without explicit confirmation accepted")
	}
}
