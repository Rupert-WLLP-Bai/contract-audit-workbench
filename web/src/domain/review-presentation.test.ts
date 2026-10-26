import type {
  ExecutionEvent,
  Finding,
  ReviewDocument,
} from "../api/types";
import {
  decisionSummary,
  domainLabel,
  findingHighlights,
  findingLocation,
  firstClauseOffsetForPage,
  reportedPages,
  reviewStage,
  severityRank,
  summarizeFindings,
  verificationLabel,
} from "./review-presentation";

function finding(overrides: Partial<Finding> = {}): Finding {
  return {
    id: "f1",
    origin: "ai",
    domain: "质保",
    claim: "质保期限不一致",
    relation: "conflict",
    confidence: "medium",
    verification_status: "unverified",
    evidence: [],
    unverified_reason: null,
    version: 1,
    severity: "high",
    severity_source: null,
    decisions: [],
    ...overrides,
  };
}

function event(seq: number, phase: string): ExecutionEvent {
  return {
    seq,
    attempt_id: "a1",
    span_id: null,
    parent_span_id: null,
    node_kind: "phase",
    phase,
    event_type: "node_started",
    at: null,
    duration_ms: null,
    status: "running",
    error_code: null,
    model_ref: null,
    usage: null,
  };
}

function document(overrides: Partial<ReviewDocument> = {}): ReviewDocument {
  return {
    id: "d1",
    role: "draft_contract",
    filename: "拟签合同.txt",
    media_type: "text/plain",
    digest: "digest",
    size: 10,
    parse_status: "ready",
    parse_error: null,
    created_at: "2026-10-21T00:00:00Z",
    parsed_document: null,
    text: "质保期为二十四个月。",
    clauses: [],
    ...overrides,
  };
}

describe("review presentation", () => {
  it("maps terminal run states without claiming an unreported stage", () => {
    expect(reviewStage("awaiting_human").label).toBe("待人工复核");
    expect(reviewStage("completed").tone).toBe("good");
    expect(
      reviewStage("partial", [], { partial_reason: "图片未转写" }).detail,
    ).toBe("图片未转写");
    expect(
      reviewStage("failed", [], { failure_message: "模型超时" }).detail,
    ).toBe("模型超时");
    expect(reviewStage("partial").tone).toBe("pending");
    expect(reviewStage("failed").label).toBe("审查失败");
    expect(reviewStage("cancelled").tone).toBe("neutral");
    expect(reviewStage("interrupted").label).toBe("审查已中断");
    expect(reviewStage("queued").label).toBe("等待开始审查");
  });

  it("uses the latest execution phase only for running states", () => {
    expect(reviewStage("running", [event(1, "rulepack")]).label).toBe(
      "正在加载审查依据",
    );
    expect(
      reviewStage("running", [event(1, "rules"), event(2, "evidence")]).label,
    ).toBe("正在核验依据");
    expect(
      reviewStage("running", [event(1, "verified_finding")]).label,
    ).toBe("正在核验依据");
    expect(reviewStage("running", [event(1, "model_request")]).label).toBe(
      "正在识别风险",
    );
    expect(
      reviewStage("running", [event(1, "agent_review_result")]).label,
    ).toBe("正在识别风险");
    expect(
      reviewStage("running", [event(1, "terminal_persistence")]).label,
    ).toBe("正在保存审查结果");
    expect(reviewStage("running", [event(1, "cancellation")]).label).toBe(
      "正在取消审查",
    );
    expect(reviewStage("running", [event(1, "acceptance")]).label).toBe(
      "正在准备审查",
    );
    expect(reviewStage("running", [event(1, "custom_phase")]).label).toBe(
      "正在进行审查",
    );
    expect(reviewStage("running").label).toBe("正在进行审查");
    expect(reviewStage("mystery").label).toBe("状态未识别");
    expect(reviewStage("running", [event(1, "model")]).label).toBe(
      "正在识别风险",
    );
    expect(reviewStage("running", [event(1, "agent_review")]).label).toBe(
      "正在识别风险",
    );
  });

  it("summarizes real findings without treating undecided as verified", () => {
    const summary = summarizeFindings([
      finding(),
      finding({ id: "f2", severity: "medium", relation: "insufficient" }),
      finding({
        id: "f3",
        severity: "low",
        verification_status: "verified",
        decisions: [
          {
            id: "d1",
            action: "false_positive",
            reason: "示例",
            actor: "审查员",
            decided_at: "2026-10-21T00:00:00Z",
          },
        ],
      }),
      finding({ id: "f4", severity: "info" }),
      finding({ id: "f5", severity: null }),
    ]);
    expect(summary).toMatchObject({
      total: 5,
      high: 1,
      medium: 1,
      low: 1,
      info: 1,
      unrated: 1,
      undecided: 4,
      decided: 1,
      conflicts: 4,
      insufficient: 1,
      verified: 1,
    });
    expect(severityRank("high")).toBe(0);
    expect(severityRank(null)).toBe(4);
    expect(severityRank("info")).toBe(3);
  });

  it("shows a source page only when the quote still resolves to frozen text", () => {
    const anchor = {
      id: "e1",
      document_version_id: "d1",
      parsed_document_id: null,
      normalized_digest: null,
      quote: "二十四个月",
      status: "located" as const,
      page: 2,
      text_offset: { start: 4, end: 9 },
      bbox: null,
      mismatch_reason: null,
    };
    expect(findingLocation(finding({ evidence: [anchor] }), [document()])).toBe(
      "第 2 页 · 拟签合同",
    );
    expect(
      findingLocation(
        finding({
          evidence: [{ ...anchor, quote: "三十六个月" }],
        }),
        [document()],
      ),
    ).toBe("记录页码 2 · 未能定位");
    expect(
      findingLocation(
        finding({ evidence: [{ ...anchor, page: null }] }),
        [document()],
      ),
    ).toBe("拟签合同");
    expect(
      findingLocation(
        finding({
          evidence: [
            {
              ...anchor,
              status: "anchor_mismatch",
              page: null,
              text_offset: null,
            },
          ],
        }),
        [document()],
      ),
    ).toBe("原文位置未报告");
    expect(findingLocation(finding(), [document()])).toBe("未附原文位置");
    expect(
      findingLocation(finding({ evidence: [anchor] }), [
        document({ text: null }),
      ]),
    ).toBe("记录页码 2 · 未能定位");
  });

  it("only marks spans that still resolve against the frozen document", () => {
    const located = {
      id: "e1",
      document_version_id: "d1",
      parsed_document_id: null,
      normalized_digest: null,
      quote: "二十四个月",
      status: "located" as const,
      page: 2,
      text_offset: { start: 4, end: 9 },
      bbox: null,
      mismatch_reason: null,
    };
    const source = document({
      clauses: [
        { id: "c1", heading: "质保", text_offset: { start: 0, end: 4 }, page: 2 },
        { id: "c2", heading: "付款", text_offset: { start: 9, end: 10 }, page: 5 },
      ],
    });
    const highlights = findingHighlights(
      [
        finding({ evidence: [located] }),
        finding({
          id: "f2",
          claim: "付款依据不足",
          evidence: [{ ...located, id: "e2", quote: "三十六个月" }],
        }),
      ],
      source,
      "f2",
    );
    expect(highlights).toEqual([
      {
        findingId: "f1",
        claim: "质保期限不一致",
        severity: "high",
        active: false,
        start: 4,
        end: 9,
      },
    ]);
    expect(reportedPages(source)).toEqual([2, 5]);
    expect(firstClauseOffsetForPage(source, 5)).toBe(9);
    expect(firstClauseOffsetForPage(source, 8)).toBeNull();
    expect(reportedPages(document({ text: null, clauses: [] }))).toEqual([]);
    expect(
      findingHighlights([finding({ evidence: [located] })], document({ text: null }), "f1"),
    ).toEqual([]);
  });

  it("labels verification and decision state", () => {
    expect(domainLabel("warranty")).toBe("质量保证");
    expect(domainLabel("SLA")).toBe("服务水平");
    expect(domainLabel("custom-domain")).toBe("custom-domain");
    expect(domainLabel("")).toBeNull();
    expect(domainLabel(null)).toBeNull();
    expect(verificationLabel("verified")).toBe("已核验");
    expect(verificationLabel("unverified")).toBe("未核验");
    expect(verificationLabel("unknown")).toBe("核验状态未报告");
    expect(decisionSummary(finding()).pending).toBe(true);
    expect(
      decisionSummary(
        finding({
          decisions: [
            {
              id: "d1",
              action: "confirm_risk",
              reason: "示例",
              actor: "审查员",
              decided_at: "2026-10-21T00:00:00Z",
            },
          ],
        }),
      ).pending,
    ).toBe(false);
  });
});
