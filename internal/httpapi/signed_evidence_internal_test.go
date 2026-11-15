// File metadata (manifest mirror):
// Purpose: Exercises signed-evidence identity normalization and the readable PDF section/decision projection without a database.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"strings"
	"testing"
	"time"

	"contractaudit/internal/store"
)

func evidenceBundleFixture(cutoff time.Time) store.EvidenceBundleData {
	return store.EvidenceBundleData{
		TenantID:  "tenant-demo",
		CaseTitle: "专线采购审查",
		Record: store.ReportRecord{
			ID: "report-1", Version: 2, RunID: "run-1", AttemptID: "att-1",
			InputHash: "sha256:aa", OutputHash: "sha256:bb", DecisionSnapshotHash: "sha256:cc",
			CreatedAt: "2026-11-15T08:00:00Z",
		},
		HistoryCutoff: cutoff,
		Findings: []store.Finding{{
			ID: "finding-1", Relation: "conflict", Claim: "付款早于验收",
			Anchors: []store.Anchor{
				{DocumentID: "doc-1", ParsedDocumentID: "parsed-1", Start: 0, End: 3, Quote: "abc", Status: "located"},
				{DocumentID: "doc-2", Status: "unlocated", Reason: "原文未定位"},
			},
		}},
		Evaluations: []store.ConstraintEval{{
			ID: "evaluation-1", RuleID: "rule-a", Relation: "conflict", Reason: "低于阈值",
			Evidence: []store.EvaluationEvidence{{Ref: store.ExperienceEvidenceRef{
				DocumentID: "doc-1", ParsedDocumentID: "parsed-1", Start: 0, End: 3, Quote: "abc",
			}}},
		}},
		FrozenDecisions: []store.Decision{{ID: "decision-1", FindingID: "finding-1", ActorName: "审查员", Action: "confirm_risk", NewDisposition: "confirmed", CreatedAt: "2026-11-15T08:01:00Z", Reason: "证据充分"}},
		Decisions:       []store.Decision{{ID: "decision-2", FindingID: "finding-1", ActorName: "复核员", Action: "accept_residual", NewDisposition: "accepted_residual", CreatedAt: "2026-11-15T08:02:00Z", Reason: "接受残余风险"}},
		ManualFindings: []store.EvidenceManualFinding{{
			ID: "manual-1", ActorName: "复核员", Claim: "补充验收条件", CreatedAt: time.Date(2026, 11, 15, 8, 3, 0, 0, time.UTC),
			Evidence: store.ExperienceEvidenceRef{DocumentID: "doc-1", ParsedDocumentID: "parsed-1", Start: 0, End: 3, Quote: "abc"},
		}},
		Originals: []store.EvidenceOriginal{{
			Path: "originals/doc-1", DocumentID: "doc-1", ParsedDocumentID: "parsed-1", Filename: "contract.txt",
			MediaType: "text/plain", Size: 12, SHA256: "sha256:dd",
		}},
	}
}

func TestEvidenceIdentityNormalizesHistoryAndRejectsInvalidTimestamps(t *testing.T) {
	cutoff := time.Date(2026, 11, 15, 8, 4, 5, 6, time.FixedZone("CST", 8*3600))
	data := evidenceBundleFixture(cutoff)
	identity, err := evidenceIdentity("case-1", data)
	if err != nil {
		t.Fatal(err)
	}
	if identity.CaseID != "case-1" || identity.TenantID != "tenant-demo" || identity.ReportID != "report-1" || identity.ReportVersion != 2 ||
		identity.RunID != "run-1" || identity.AttemptID != "att-1" || identity.InputSHA256 != "sha256:aa" || identity.OutputSHA256 != "sha256:bb" ||
		identity.DecisionSnapshotSHA256 != "sha256:cc" || identity.HistoryCutoff != cutoff.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("identity lost report fields: %+v", identity)
	}
	if len(identity.Decisions) != 1 || identity.Decisions[0].ID != "decision-2" || identity.Decisions[0].CreatedAt != "2026-11-15T08:02:00Z" {
		t.Fatalf("decision history was not normalized: %+v", identity.Decisions)
	}
	if len(identity.ManualFindings) != 1 || identity.ManualFindings[0].ID != "manual-1" || identity.ManualFindings[0].CreatedAt != "2026-11-15T08:03:00Z" {
		t.Fatalf("manual finding history was not normalized: %+v", identity.ManualFindings)
	}
	broken := data
	broken.Decisions = []store.Decision{{ID: "decision-bad", CreatedAt: "not-a-time"}}
	if _, err := evidenceIdentity("case-1", broken); err == nil {
		t.Fatal("invalid decision timestamp was accepted")
	}
	if _, err := evidenceIdentity("case-1", evidenceBundleFixture(cutoff)); err != nil {
		t.Fatalf("empty history was rejected: %v", err)
	}
	empty := data
	empty.Decisions = nil
	empty.ManualFindings = nil
	identity, err = evidenceIdentity("case-1", empty)
	if err != nil || identity.Decisions == nil || identity.ManualFindings == nil || len(identity.Decisions) != 0 || len(identity.ManualFindings) != 0 {
		t.Fatalf("empty history was not encoded as an empty list: %+v %v", identity, err)
	}
}

func TestEvidencePDFSectionsProjectEverySignedRecord(t *testing.T) {
	cutoff := time.Date(2026, 11, 15, 8, 4, 5, 0, time.UTC)
	sections := evidencePDFSections("case-1", evidenceBundleFixture(cutoff))
	if len(sections) != 5 {
		t.Fatalf("expected five sections, got %d", len(sections))
	}
	titles := []string{"审查证据包", "冻结原件", "报告冻结的检查与引用", "报告创建时冻结的人工决定", "导出时可见的追加记录"}
	for i, want := range titles {
		if sections[i].Title != want {
			t.Fatalf("section %d title is %q, want %q", i, sections[i].Title, want)
		}
	}
	joined := make([]string, 0)
	for _, section := range sections {
		joined = append(joined, section.Lines...)
	}
	text := strings.Join(joined, "\n")
	for _, want := range []string{
		"案件：专线采购审查；编号：case-1",
		"报告 report-1，版本 2，创建于 2026-11-15T08:00:00Z",
		"冻结发现 1 项，评估 1 项，报告决定 1 条，原件 1 份",
		"contract.txt；材料 doc-1；解析 parsed-1；12 字节；SHA-256 sha256:dd",
		"检查 evaluation-1；规则 rule-a；结果 conflict；低于阈值",
		"发现 finding-1；结果 conflict；付款早于验收",
		"引用未定位：原文未定位",
		"数据库记录截止时刻：2026-11-15T08:04:05Z",
		"人工发现 manual-1；复核员；2026-11-15T08:03:00Z；补充验收条件",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("PDF projection is missing %q:\n%s", want, text)
		}
	}
	empty := evidencePDFSections("case-2", store.EvidenceBundleData{HistoryCutoff: cutoff})
	if len(empty) != 5 || len(empty[1].Lines) != 0 || len(empty[2].Lines) != 0 || empty[3].Lines[0] != "没有已记录的人工决定。" {
		t.Fatalf("empty evidence bundle projection is %+v", empty)
	}
}

func TestDecisionPDFLinesJoinEveryDecisionField(t *testing.T) {
	if got := decisionPDFLines(nil); len(got) != 1 || got[0] != "没有已记录的人工决定。" {
		t.Fatalf("empty decisions produced %+v", got)
	}
	lines := decisionPDFLines([]store.Decision{{
		ID: "decision-1", FindingID: "finding-1", ActorName: "审查员", Action: "confirm_risk",
		NewDisposition: "confirmed", CreatedAt: "2026-11-15T08:01:00Z", Reason: "证据充分",
	}})
	want := "决定 decision-1；发现 finding-1；处理人 审查员；动作 confirm_risk；结论 confirmed；时间 2026-11-15T08:01:00Z；理由 证据充分"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("decision line is %q", lines)
	}
}
