import { parseReportDetail, type ReportDetail } from "../api/reports";
import {
  dispositionLabel,
  latestFindingDecision,
  reportConclusion,
  reportDomainCounts,
  reportFindingLocation,
  reportPagePath,
  reportSourceHighlights,
  reportSummary,
} from "./report";

const at = "2026-10-25T11:54:44.284Z";

function reportFixture(): ReportDetail {
  return parseReportDetail({
    schema_version: "case-report/v1",
    report_id: "report_1",
    report_version: 1,
    created_at: at,
    notice: "",
    case: {
      id: "case/1",
      title: "专线合同",
      contract_type: "service",
      business_line: "ENT",
      scenario: "telecom_dedicated_line",
    },
    run: {
      id: "run_1",
      case_id: "case/1",
      state: "partial",
      model_ref: null,
      created_at: at,
      updated_at: at,
    },
    attempt: {
      id: "attempt_1",
      run_id: "run_1",
      ordinal: 1,
      state: "partial",
      retry_of_attempt_id: null,
      accepted_at: at,
      started_at: at,
      finished_at: at,
      input_hash: "in",
      output_hash: "out",
      error_code: null,
      history_kind: "frozen",
    },
    execution: {
      schema_version: "audit-execution/v1",
      events: [],
      usage: null,
      duration_ms: 1000,
      first_verified_finding_ms: null,
      queue_wait_ms: null,
    },
    documents: [
      {
        document: {
          id: "doc_1",
          role: "draft_contract",
          filename: "draft.txt",
          media_type: "text/plain",
          digest: "digest",
          size: 20,
          origin: "upload",
          created_at: at,
        },
        parsed: {
          id: "parsed_1",
          document_id: "doc_1",
          original_digest: "digest",
          parser_id: "text",
          parser_version: "1",
          config_hash: "config",
          normalization_version: "1",
          normalized_digest: "normalized",
          ir_digest: null,
          artifact_digest: "artifact",
          created_at: at,
          status: "ready",
          text: "预付款比例 40%。质保期二十四个月。",
          ir: { Clauses: [] },
        },
      },
    ],
    findings: [
      {
        id: "finding_high",
        origin: "rule",
        domain: "payment",
        rule_id: "RULE-1",
        claim: "预付款比例超过上限。",
        relation: "conflict",
        disposition_suggestion: "confirm_risk",
        severity: "high",
        version: 1,
        anchors: [
          {
            id: "anchor_1",
            document_id: "doc_1",
            parsed_document_id: "parsed_1",
            normalized_digest: "normalized",
            quote: "预付款比例 40%",
            status: "located",
            start: 0,
            end: 9,
          },
        ],
        created_at: at,
      },
      {
        id: "finding_unrated",
        origin: "rule",
        domain: "payment",
        rule_id: "RULE-2",
        claim: "质保期限未报告。",
        relation: "insufficient",
        disposition_suggestion: "insufficient_evidence",
        severity: null,
        version: 1,
        anchors: [
          {
            id: "anchor_2",
            document_id: "doc_1",
            parsed_document_id: "parsed_1",
            normalized_digest: "normalized",
            quote: "质保期二十四个月",
            status: "anchor_mismatch",
            reason: "引用原文与当前正文不一致",
          },
        ],
        created_at: at,
      },
    ],
    evaluations: [],
    decisions: [
      {
        id: "decision_1",
        finding_id: "finding_high",
        run_id: "run_1",
        action: "confirm_risk",
        reason: "确认为风险",
        actor_id: "demo",
        actor_name: "演示管理员",
        old_disposition: "pending",
        new_disposition: "confirmed",
        created_at: at,
      },
    ],
    rulepack: {
      availability: "frozen",
      compiler_ref: "compiler",
      manifest_hash: "manifest",
      snapshot_sha256: "snapshot",
      snapshot: null,
    },
    input_hash: "in",
    output_hash: "out",
    decision_snapshot_hash: "decisions",
    agent_review: null,
  });
}

describe("report presentation", () => {
  it("counts frozen findings, decisions and only resolvable anchors", () => {
    const report = reportFixture();
    const summary = reportSummary(report);
    expect(summary).toMatchObject({
      total: 2,
      high: 1,
      unrated: 1,
      decided: 1,
      undecided: 1,
      conflicts: 1,
      insufficient: 1,
      locatedAnchors: 1,
      totalAnchors: 2,
    });
    expect(reportConclusion(summary)).toMatchObject({
      tone: "error",
      title: "报告包含 1 项高风险发现",
    });
    expect(latestFindingDecision(report, "finding_high")?.action).toBe(
      "confirm_risk",
    );
    expect(latestFindingDecision(report, "finding_unrated")).toBeNull();
  });

  it("keeps mismatched quotes as snapshots and never marks them as located text", () => {
    const report = reportFixture();
    const document = report.documents[0]!;
    const highlights = reportSourceHighlights(report, document, null);
    expect(highlights).toHaveLength(1);
    expect(highlights[0]).toMatchObject({
      findingId: "finding_high",
      severity: "high",
      start: 0,
      end: 9,
    });
    expect(reportFindingLocation(report, report.findings[1]!)).toBe(
      "原文位置未报告",
    );
  });

  it("reports domains without inventing labels or ordering", () => {
    expect(reportDomainCounts(reportFixture().findings)).toEqual([
      { domain: "payment", label: "付款", count: 2 },
    ]);
    expect(dispositionLabel("confirm_risk")).toBe("建议确认风险");
    expect(dispositionLabel("unexpected")).toBe(
      "处置建议未识别（unexpected）",
    );
    expect(dispositionLabel(null)).toBe("服务未返回处置建议");
  });

  it("encodes report deep links", () => {
    expect(reportPagePath("case/1", "report/1")).toBe(
      "/cases/case%2F1/reports/report%2F1",
    );
  });
});
