// File metadata (manifest mirror):
// Purpose: Exercises manual finding input validation, frozen-evidence append, idempotent replay, conflict detection, exact-attempt listing and role denial.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
)

func TestManualFindingAppendsFrozenEvidenceAndListsExactAttempt(t *testing.T) {
	st, user, feedback, _ := feedbackFixture(t, "accept_residual")
	ctx := context.Background()
	ref := feedback.EvidenceRefs[0]
	input := ManualFindingInput{
		CaseID: feedback.CaseID, RunID: feedback.RunID, AttemptID: feedback.AttemptID,
		Claim: "  人工补充：付款需先完成验收  ", Severity: "medium",
		Evidence: ref, IdempotencyKey: "manual-finding-key-1",
	}
	first, err := st.CreateManualFinding(ctx, user, input)
	if err != nil {
		t.Fatalf("create manual finding: %v", err)
	}
	if first.ID == "" || first.CaseID != feedback.CaseID || first.RunID != feedback.RunID ||
		first.AttemptID != feedback.AttemptID || first.ActorID != user.ID ||
		first.Claim != "人工补充：付款需先完成验收" || first.Severity != "medium" ||
		first.Evidence.DocumentID != ref.DocumentID || first.Evidence.NormalizedDigest == "" {
		t.Fatalf("stored manual finding lost identity: %+v", first)
	}

	replayed, err := st.CreateManualFinding(ctx, user, input)
	if err != nil {
		t.Fatalf("replay manual finding: %v", err)
	}
	if replayed.ID != first.ID || !replayed.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("idempotent replay returned a different record: %+v", replayed)
	}

	conflict := input
	conflict.Claim = "另一个不同的人工发现"
	_, err = st.CreateManualFinding(ctx, user, conflict)
	requireFeedbackFault(t, err, 409)

	listed, err := st.ListManualFindings(ctx, user, feedback.CaseID, feedback.RunID, feedback.AttemptID)
	if err != nil {
		t.Fatalf("list manual findings: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != first.ID || listed[0].Claim != first.Claim {
		t.Fatalf("listed manual findings differ from the append: %+v", listed)
	}

	unknownAttempt := input
	unknownAttempt.IdempotencyKey = "manual-finding-key-2"
	unknownAttempt.AttemptID = "att_00000000000000ff"
	_, err = st.CreateManualFinding(ctx, user, unknownAttempt)
	requireFeedbackFault(t, err, 404)

	mismatched := input
	mismatched.IdempotencyKey = "manual-finding-key-3"
	mismatched.Evidence.Quote += "x"
	_, err = st.CreateManualFinding(ctx, user, mismatched)
	requireFeedbackFault(t, err, 422)

	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("demo viewer missing")
	}
	if err := st.AddMember(user, feedback.CaseID, viewer.ID); err != nil {
		t.Fatalf("add viewer member: %v", err)
	}
	viewerInput := input
	viewerInput.IdempotencyKey = "manual-finding-key-4"
	_, err = st.CreateManualFinding(ctx, viewer, viewerInput)
	requireFeedbackFault(t, err, 403)

	if _, err := st.ListManualFindings(ctx, user, feedback.CaseID, feedback.RunID, "bad id"); err == nil {
		t.Fatal("malformed attempt identity was listed")
	}
	if _, err := st.ListManualFindings(ctx, user, feedback.CaseID, "run_00000000000000ff", feedback.AttemptID); err == nil {
		t.Fatal("unknown run was listed")
	}
}

func TestManualFindingInputValidationRejectsInvalidEvidence(t *testing.T) {
	valid := ManualFindingInput{
		CaseID: "case_0123456789abcdef", RunID: "run_0123456789abcdef",
		AttemptID: "att_0123456789abcdef", Claim: "人工发现", Severity: "high",
		Evidence: ExperienceEvidenceRef{
			DocumentID: "doc_0123456789abcdef", ParsedDocumentID: "parsed_0123456789abcdef",
			Start: 0, End: 4, Quote: "人工发现",
		},
		IdempotencyKey: "manual-key",
	}
	check := func(name string, mutate func(*ManualFindingInput), wantError bool) {
		input := valid
		if mutate != nil {
			mutate(&input)
		}
		err := validateManualFindingInput(&input)
		if wantError && err == nil {
			t.Fatalf("%s was accepted", name)
		}
		if !wantError && err != nil {
			t.Fatalf("%s was rejected: %v", name, err)
		}
	}
	check("valid", nil, false)
	check("trimmed claim", func(input *ManualFindingInput) { input.Claim = "  人工发现  " }, false)
	check("empty claim", func(input *ManualFindingInput) { input.Claim = " " }, true)
	check("short claim", func(input *ManualFindingInput) { input.Claim = "短" }, true)
	check("long claim", func(input *ManualFindingInput) { input.Claim = strings.Repeat("长", 1001) }, true)
	check("invalid severity", func(input *ManualFindingInput) { input.Severity = "critical" }, true)
	check("empty quote", func(input *ManualFindingInput) { input.Evidence.Quote = "" }, true)
	check("negative start", func(input *ManualFindingInput) { input.Evidence.Start = -1 }, true)
	check("empty span", func(input *ManualFindingInput) { input.Evidence.End = input.Evidence.Start }, true)
	check("span mismatch", func(input *ManualFindingInput) { input.Evidence.End++ }, true)
	check("invalid digest", func(input *ManualFindingInput) { input.Evidence.NormalizedDigest = "not-a-digest" }, true)
	check("missing case", func(input *ManualFindingInput) { input.CaseID = "" }, true)
	check("missing document", func(input *ManualFindingInput) { input.Evidence.DocumentID = "" }, true)
	check("long key", func(input *ManualFindingInput) { input.IdempotencyKey = strings.Repeat("k", 81) }, true)
	check("invalid digest format", func(input *ManualFindingInput) { input.Evidence.NormalizedDigest = "sha256:xyz" }, true)
}
