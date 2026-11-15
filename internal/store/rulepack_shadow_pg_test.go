// File metadata (manifest mirror):
// Purpose: Exercises the PostgreSQL-backed rule shadow workflow over a governed acceptance review: sample discovery, candidate replay, append-only report persistence and scoped reads.
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
	"testing"
)

func shadowSampleForAttempt(samples []RuleShadowSampleOption, ref RuleShadowSampleRef) bool {
	for _, sample := range samples {
		if sample.RuleShadowSampleRef == ref {
			return true
		}
	}
	return false
}

func TestRuleShadowReportReplaysAGovernedAttempt(t *testing.T) {
	st, owner, feedback, _, pack, _ := feedbackGovernedFixture(t, "accept_residual")
	ctx := context.Background()
	ref := RuleShadowSampleRef{CaseID: feedback.CaseID, RunID: feedback.RunID, AttemptID: feedback.AttemptID}

	samples, err := st.RuleShadowSamples(ctx, owner, pack.Pack.ID, pack.Pack.Version)
	if err != nil {
		t.Fatalf("list shadow samples: %v", err)
	}
	if !shadowSampleForAttempt(samples, ref) {
		t.Fatalf("governed attempt is not among the visible shadow samples: %+v", samples)
	}

	envelope, err := st.CreateRuleShadowReport(ctx, owner, RuleShadowInput{
		PackID: pack.Pack.ID, Version: pack.Pack.Version,
		ExpectedRevision: pack.Pack.Revision, ExpectedContentHash: pack.Pack.ContentHash,
		Samples: []RuleShadowSampleRef{ref},
	})
	if err != nil {
		t.Fatalf("create shadow report: %v", err)
	}
	if envelope.Report.SchemaVersion != "rulepack-shadow-report/v1" || envelope.ReportSHA256 == "" ||
		envelope.Report.Candidate.ID != pack.Pack.ID || envelope.Report.Candidate.ContentSHA256 != pack.Pack.ContentHash {
		t.Fatalf("shadow report lost its candidate identity: %+v %q", envelope.Report, envelope.ReportSHA256)
	}
	if envelope.Report.Completed != 1 || envelope.Report.Partial != 0 || envelope.Report.Failed != 0 || len(envelope.Report.Samples) != 1 {
		t.Fatalf("shadow report counters are %+v", envelope.Report)
	}
	sample := envelope.Report.Samples[0]
	if sample.Status != "completed" || !sample.CaseAuthorized || sample.InputSHA256 == "" || sample.OutputSHA256 == "" ||
		sample.KnowledgeSHA256 == "" || sample.RuleRequestSHA256 == "" || sample.ExtractorRef == "" {
		t.Fatalf("shadow sample was not replayed: %+v", sample)
	}
	if sample.BasePackID != pack.Pack.ID || sample.BasePackVersion != pack.Pack.Version ||
		sample.BasePackSHA256 != pack.Pack.ContentHash || sample.BasePackSnapshotSHA256 != pack.Pack.SnapshotSHA256 {
		t.Fatalf("shadow sample lost its frozen baseline: %+v", sample)
	}

	read, err := st.RuleShadowReports(ctx, owner, pack.Pack.ID, pack.Pack.Version, envelope.Report.ID)
	if err != nil {
		t.Fatalf("read shadow report: %v", err)
	}
	if len(read) != 1 || read[0].Report.ID != envelope.Report.ID || read[0].ReportSHA256 != envelope.ReportSHA256 {
		t.Fatalf("shadow report read back as %+v", read)
	}
	listed, err := st.RuleShadowReports(ctx, owner, pack.Pack.ID, pack.Pack.Version, "")
	if err != nil || len(listed) == 0 || listed[0].Report.ID != envelope.Report.ID {
		t.Fatalf("shadow report list: %+v (%v)", listed, err)
	}
	if _, err := st.RuleShadowReports(ctx, owner, pack.Pack.ID, pack.Pack.Version, "shadow_missing"); err == nil {
		t.Fatal("missing shadow report was returned")
	} else {
		requireFeedbackFault(t, err, 404)
	}
}

func TestRuleShadowReportRejectsStaleCandidateAndUnauthorizedSamples(t *testing.T) {
	st, owner, feedback, _, pack, _ := feedbackGovernedFixture(t, "accept_residual")
	ctx := context.Background()
	ref := RuleShadowSampleRef{CaseID: feedback.CaseID, RunID: feedback.RunID, AttemptID: feedback.AttemptID}
	input := RuleShadowInput{
		PackID: pack.Pack.ID, Version: pack.Pack.Version,
		ExpectedRevision: pack.Pack.Revision, ExpectedContentHash: pack.Pack.ContentHash,
		Samples: []RuleShadowSampleRef{ref},
	}
	stale := input
	stale.ExpectedContentHash = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := st.CreateRuleShadowReport(ctx, owner, stale); err == nil {
		t.Fatal("stale candidate hash was accepted")
	} else {
		requireFeedbackFault(t, err, 409)
	}
	if _, err := st.CreateRuleShadowReport(ctx, owner, RuleShadowInput{
		PackID: pack.Pack.ID, Version: pack.Pack.Version,
		ExpectedRevision: pack.Pack.Revision, ExpectedContentHash: pack.Pack.ContentHash,
	}); err == nil {
		t.Fatal("empty sample list was accepted")
	} else {
		requireFeedbackFault(t, err, 422)
	}
	missing := input
	missing.Samples = []RuleShadowSampleRef{{CaseID: "case_missing", RunID: feedback.RunID, AttemptID: feedback.AttemptID}}
	if _, err := st.CreateRuleShadowReport(ctx, owner, missing); err == nil {
		t.Fatal("unauthorized sample was accepted")
	} else {
		requireFeedbackFault(t, err, 404)
	}
}
