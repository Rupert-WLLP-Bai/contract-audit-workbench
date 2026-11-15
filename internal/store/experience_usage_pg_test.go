// File metadata (manifest mirror):
// Purpose: Exercises governed experience usage against a real published experience candidate: option discovery before decision, append-only recording, idempotent replay, conflict and role rejection, and outcome metrics after the human decision.
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
	"time"

	"contractaudit/internal/accounts"
)

func publishedExperienceUsageCandidate(t *testing.T) (*Store, accounts.User, GovernedKnowledgeVersion, GovernedKnowledgeVersion, time.Time) {
	t.Helper()
	st, admin, feedbackInput, _, pack, policy := feedbackGovernedFixture(t, "false_positive")
	ctx := context.Background()
	feedback, err := st.SubmitExperienceFeedback(ctx, admin, feedbackInput)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	candidateContent := candidateContentForFeedback(t, st, admin, feedback, now)
	delivery := createFeedbackDelivery(t, st, admin, feedback,
		feedbackDistillInput(t, st, admin, feedback, "usage-candidate", ""), candidateContent, now.Add(time.Hour))
	candidate, err := acceptDelivery(st, admin, feedback, delivery)
	if err != nil {
		t.Fatal(err)
	}
	content := knowledgeFixtureContent(pack, "payment-experience-usage", "1", "experience")
	content.SourceType, content.SourceCaseID = "human_feedback", feedback.CaseID
	content.CandidateID, content.CandidateVersionID, content.CandidateSHA256 = candidate.CandidateID, candidate.VersionID, candidate.ContentSHA256
	content.CandidateReviewDiff = "去除原案件身份信息并保留验收前付款反例"
	content.ReviewedApplicability = []string{"验收完成后支付"}
	content.ReviewedExclusions = []string{"验收前支付"}
	content.CheckAction, content.MinimumEvidence = "核对本案验收及付款日期", "验收记录及付款条款"
	content.Counterexamples = []string{"验收前付款仍需要提示风险"}
	content.PolicyRefs = []KnowledgeRef{policy.Ref}
	content.ValidFrom, content.ValidUntil = candidateContent.ValidFrom, candidateContent.ValidUntil
	version := publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, content, "usage-experience"), "usage-experience")
	return st, admin, version, policy, now
}

func startUsageReview(t *testing.T, st *Store, admin accounts.User, caseID, businessAt string) (string, Workspace) {
	t.Helper()
	for role, body := range map[string]string{
		"procurement":    "采购文件：电路 LINE-001：最低保障带宽 200 Mbit/s。",
		"draft_contract": "拟签合同：电路 LINE-001：最低保障带宽 200 Mbit/s。",
	} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	runID, err := st.StartReviewAt(context.Background(), admin, caseID, businessAt)
	if err != nil {
		t.Fatal(err)
	}
	ws := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if len(ws.Findings) == 0 || ws.Execution == nil || ws.Execution.Attempt == nil {
		t.Fatalf("usage review produced no finding or frozen attempt: %+v", ws)
	}
	return runID, ws
}

func TestExperienceUsageOptionsRecordingAndMetricsFollowTheExactVersion(t *testing.T) {
	st, admin, experience, policy, now := publishedExperienceUsageCandidate(t)
	ctx := context.Background()
	targetCase := governanceTestCase(t, st, admin)
	runID, ws := startUsageReview(t, st, admin, targetCase, now.Format(time.RFC3339Nano))
	finding := ws.Findings[0]
	input := ExperienceUsageInput{
		CaseID: targetCase, RunID: runID, AttemptID: ws.Execution.Attempt.ID, FindingID: finding.ID,
		Ref: experience.Ref, ExpectedAction: "confirm_risk",
	}

	options, err := st.ListExperienceUsageOptions(ctx, admin, input)
	if err != nil {
		t.Fatalf("list usage options: %v", err)
	}
	if !options.CanRecord || len(options.Items) != 1 || options.Items[0].Ref != experience.Ref || options.Items[0].Usage != nil ||
		options.Items[0].Body == "" || options.Items[0].Summary == "" || len(options.Items[0].ReviewedExclusions) == 0 {
		t.Fatalf("usage options lost the frozen boundary: %+v", options)
	}

	usage, err := st.RecordExperienceUsage(ctx, admin, input)
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if usage.UsageSHA256 == "" || usage.SnapshotSHA256 == "" || usage.InputSHA256 == "" ||
		usage.RecordedBy != admin.ID || usage.CaseID != targetCase || usage.Ref != experience.Ref {
		t.Fatalf("usage lost its frozen identity: %+v", usage)
	}
	replayed, err := st.RecordExperienceUsage(ctx, admin, input)
	if err != nil || replayed.UsageSHA256 != usage.UsageSHA256 || !replayed.RecordedAt.Equal(usage.RecordedAt) {
		t.Fatalf("usage replay: %+v err=%v", replayed, err)
	}
	conflicting := input
	conflicting.ExpectedAction = "false_positive"
	if _, err := st.RecordExperienceUsage(ctx, admin, conflicting); err == nil {
		t.Fatal("another direction was accepted for the same usage")
	} else {
		requireFeedbackFault(t, err, 409)
	}
	invalidAction := input
	invalidAction.ExpectedAction = "maybe"
	if _, err := st.RecordExperienceUsage(ctx, admin, invalidAction); err == nil {
		t.Fatal("invalid expected action was accepted")
	} else {
		requireFeedbackFault(t, err, 422)
	}
	unknownRef := input
	unknownRef.Ref.VersionID = strings.Repeat("a", 64)
	if _, err := st.RecordExperienceUsage(ctx, admin, unknownRef); err == nil {
		t.Fatal("unknown experience version was accepted")
	} else {
		requireFeedbackFault(t, err, 404)
	}
	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("viewer missing")
	}
	if _, err := st.RecordExperienceUsage(ctx, viewer, input); err == nil {
		t.Fatal("viewer recorded experience usage")
	} else {
		requireFeedbackFault(t, err, 403)
	}

	metrics, err := st.ExperienceMetricsForVersion(ctx, admin, experience.Ref.KnowledgeID, experience.Ref.VersionID)
	if err != nil {
		t.Fatalf("usage metrics: %v", err)
	}
	if !metrics.HasBeenPublished || metrics.UsageCount != 1 || metrics.PendingCount != 1 || metrics.AdoptedCount != 0 ||
		len(metrics.Samples) != 1 || metrics.Samples[0].Outcome != "pending" || metrics.Samples[0].UsageSHA256 != usage.UsageSHA256 {
		t.Fatalf("pending metrics are %+v", metrics)
	}

	if err := st.Decide(admin, targetCase, runID, finding.ID, "usage-decision", "confirm_risk", "确认该风险", finding.Version); err != nil {
		t.Fatalf("decide: %v", err)
	}
	metrics, err = st.ExperienceMetricsForVersion(ctx, admin, experience.Ref.KnowledgeID, experience.Ref.VersionID)
	if err != nil {
		t.Fatalf("decided metrics: %v", err)
	}
	if metrics.UsageCount != 1 || metrics.PendingCount != 0 || metrics.AdoptedCount != 1 || metrics.OverturnedCount != 0 ||
		len(metrics.Samples) != 1 || metrics.Samples[0].Outcome != "adopted" || metrics.Samples[0].DecisionID == "" ||
		metrics.Samples[0].DecisionAction != "confirm_risk" || metrics.Samples[0].DecisionDisposition != "confirmed" {
		t.Fatalf("adopted metrics are %+v", metrics)
	}
	options, err = st.ListExperienceUsageOptions(ctx, admin, input)
	if err != nil || options.CanRecord || len(options.Items) != 1 || options.Items[0].Usage == nil ||
		options.Items[0].Usage.UsageSHA256 != usage.UsageSHA256 {
		t.Fatalf("decided options are %+v (%v)", options, err)
	}

	for name, check := range map[string]func() error{
		"malformed knowledge": func() error {
			_, err := st.ExperienceMetricsForVersion(ctx, admin, "bad", experience.Ref.VersionID)
			return err
		},
		"malformed version": func() error {
			_, err := st.ExperienceMetricsForVersion(ctx, admin, experience.Ref.KnowledgeID, "bad")
			return err
		},
		"policy is not experience": func() error {
			_, err := st.ExperienceMetricsForVersion(ctx, admin, policy.Ref.KnowledgeID, policy.Ref.VersionID)
			return err
		},
	} {
		if err := check(); err == nil {
			t.Fatalf("%s metrics query succeeded", name)
		} else {
			requireFeedbackFault(t, err, 404)
		}
	}
	if _, err := st.ListExperienceUsageOptions(ctx, admin, ExperienceUsageInput{}); err == nil {
		t.Fatal("incomplete usage identity was accepted")
	} else {
		requireFeedbackFault(t, err, 422)
	}
}
