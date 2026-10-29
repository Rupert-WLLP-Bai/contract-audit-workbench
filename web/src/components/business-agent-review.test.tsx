import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { parseBusinessAgentReview } from "../api/business-agent-review";
import { renderWithAntd as render } from "../test/antd";
import { BusinessAgentReviewView } from "./business-agent-review";

const anchor = {
  id: "anchor-1",
  document_id: "doc-1",
  parsed_document_id: "parsed-1",
  quote: "质保36个月",
  status: "located",
  start: 0,
  end: 7,
  reason: null,
  page: null,
  element_id: null,
};
const checkedEvidence = {
  evidence_id: "checked",
  semantic_type: "warranty",
  value: "36",
  unit: "month",
  currency: null,
  verification: "typed_semantic_and_frozen_quote_verified",
  anchor,
};
const warranty = {
  capability_id: "warranty_months",
  mandatory: true,
  status: "insufficient",
  relation: "insufficient",
  reason_code: "required_source_or_field_missing",
  evidence_ids: ["checked", "unverified", "missing"],
  canonical_evaluation_ids: [],
};
const projection = {
  attempt_id: "attempt-1",
  status: "partial",
  rulepack: null,
  scope: [{ domain: "technical", capability_ids: ["warranty_months"], source_ids: ["doc-1"] }],
  domains: [{ domain: "technical", status: "partial", capabilities: [warranty] }],
  gaps: [{ gap_id: "gap-1", domain: "technical", capability_id: "warranty_months", mandatory: true, status: "open", reason_code: "required_source_or_field_missing", evidence_ids: [] }],
  evidence: [checkedEvidence, { ...checkedEvidence, evidence_id: "unverified", verification: "unknown", anchor: { ...anchor, quote: "未经核验原文" } }],
  unresolved_gap_ids: ["gap-1"],
};


describe("business agent review statuses and edge cases", () => {
  function minimal(status: string | null) {
    return parseBusinessAgentReview({
      ...projection,
      status,
      scope: null,
      domains: null,
      gaps: null,
      evidence: null,
      unresolved_gap_ids: null,
    });
  }

  it.each([
    ["failed", "执行失败", "ant-tag-error"],
    ["rejected", "未被接受", "ant-tag-error"],
    ["queued", "等待执行", "ant-tag-processing"],
    ["planning", "正在规划", "ant-tag-processing"],
    ["running", "执行中", "ant-tag-processing"],
    ["completed", "已完成", "ant-tag-default"],
    ["supported", "已支持", "ant-tag-default"],
    ["not_applicable", "不适用", "ant-tag-default"],
    ["not_requested", "未要求", "ant-tag-default"],
    ["partial", "部分完成", "ant-tag-warning"],
    ["blocked_live", "未能完成", "ant-tag-warning"],
    ["cancelled", "已取消", "ant-tag-warning"],
    ["insufficient", "证据不足", "ant-tag-warning"],
    ["open", "未解决", "ant-tag-warning"],
    ["unknown", "状态未知", "ant-tag-warning"],
  ])("colors the %s review status as %s", (status, label, tone) => {
    render(<BusinessAgentReviewView review={minimal(status)} />);
    expect(screen.getByText(label).className).toContain(tone);
  });

  it("keeps a missing status neutral but attention-toned", () => {
    render(<BusinessAgentReviewView review={minimal(null)} />);
    expect(screen.getByText("状态未知").className).toContain("ant-tag-warning");
  });

  it("keeps scope-only domains and missing capabilities unknown", () => {
    render(
      <BusinessAgentReviewView
        review={parseBusinessAgentReview({
          ...projection,
          scope: [
            { domain: "commercial", capability_ids: [], source_ids: null },
            {
              domain: "technical",
              capability_ids: ["warranty_months", "unregistered.cap"],
              source_ids: null,
            },
          ],
          domains: [{ domain: "technical", status: "completed", capabilities: [warranty] }],
          gaps: [{ gap_id: "g-empty", domain: "technical", capability_id: "warranty_months", mandatory: false, status: "open", reason_code: "required_source_or_field_missing", evidence_ids: [] }],
          evidence: [],
          unresolved_gap_ids: [],
        })}
      />,
    );
    expect(
      screen.getByText("该领域已列入范围，检查结果和必查状态未知。"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/范围中的项目：/)).not.toBeInTheDocument();
    expect(screen.getByText("是否必查：未知。")).toBeInTheDocument();
    expect(screen.getByText("未识别审查项目")).toBeInTheDocument();
    expect(screen.getByText("缺少必要材料或字段。")).toBeInTheDocument();
    expect(
      screen.getByText("部分项目没有原文依据，不能确认这些项目已经核验。"),
    ).toBeInTheDocument();
  });

  it("distinguishes gap resolution states and missing gap details", () => {
    render(
      <BusinessAgentReviewView
        review={parseBusinessAgentReview({
          ...projection,
          domains: [],
          scope: null,
          evidence: [],
          gaps: [
            { gap_id: "g-resolved", domain: "technical", capability_id: "warranty_months", mandatory: false, status: "resolved", reason_code: "required_source_or_field_missing", evidence_ids: ["checked"] },
            { gap_id: "g-completed", domain: "technical", capability_id: "warranty_months", mandatory: true, status: "completed", reason_code: "required_source_or_field_missing", evidence_ids: ["checked"] },
            { gap_id: "g-open", domain: "technical", capability_id: "warranty_months", mandatory: false, status: "open", reason_code: "required_source_or_field_missing", evidence_ids: ["checked"] },
          ],
          unresolved_gap_ids: ["g-resolved", "g-missing"],
        })}
      />,
    );
    expect(screen.getByText("未解决（状态需核对）")).toBeInTheDocument();
    expect(screen.getByText("已处理，解决状态待核对")).toBeInTheDocument();
    expect(screen.getByText("未解决")).toBeInTheDocument();
    expect(
      screen.getByText("有 1 项未解决缺口缺少详情，请进一步核对。"),
    ).toBeInTheDocument();
    expect(screen.getByText("必查项目；缺少必要材料或字段。")).toBeInTheDocument();
  });

  it("says when the unresolved gap list itself is unavailable", () => {
    render(
      <BusinessAgentReviewView
        review={parseBusinessAgentReview({
          ...projection,
          domains: [],
          scope: null,
          evidence: [],
          gaps: [{ gap_id: "g-1", domain: "technical", capability_id: "warranty_months", mandatory: false, status: "open", reason_code: "required_source_or_field_missing", evidence_ids: ["checked"] }],
          unresolved_gap_ids: null,
        })}
      />,
    );
    expect(
      screen.getByText("缺口的解决清单不可用，请核对。"),
    ).toBeInTheDocument();
  });

  it("renders each citation state and separates unreferenced evidence", () => {
    const citation = (id: string, verification: string, overrides: Record<string, unknown> = {}) => ({
      evidence_id: id,
      semantic_type: "warranty",
      value: "36",
      unit: "month",
      currency: null,
      verification,
      anchor: { ...anchor, ...overrides },
    });
    render(
      <BusinessAgentReviewView
        review={parseBusinessAgentReview({
          ...projection,
          scope: [{ domain: "technical", capability_ids: ["warranty_months"], source_ids: null }],
          domains: [{
            domain: "technical",
            status: "partial",
            capabilities: [{
              ...warranty,
              evidence_ids: ["rejected", "unverified", "incomplete", "paged", "page-zero", "missing-detail"],
            }],
          }],
          gaps: [],
          unresolved_gap_ids: [],
          evidence: [
            citation("rejected", "rejected"),
            citation("unverified", "unknown"),
            citation("incomplete", "verified", { parsed_document_id: "" }),
            citation("paged", "verified", { page: 3 }),
            citation("page-zero", "verified", { page: 0 }),
            citation("other", "verified", { quote: "未被引用的原文" }),
          ],
        })}
      />,
    );
    expect(screen.getByText("引用未通过核验。")).toBeInTheDocument();
    expect(screen.getByText("引用尚未核验。")).toBeInTheDocument();
    expect(screen.getByText("核验引用的信息不完整，需进一步核对。")).toBeInTheDocument();
    expect(screen.getByText("来源材料中的引用已核验，第 3 页")).toBeInTheDocument();
    expect(screen.getByText("关联引用详情不可用。")).toBeInTheDocument();
    expect(screen.getByText("其他来源引用")).toBeInTheDocument();
    expect(screen.getByText("未被引用的原文")).toBeInTheDocument();
  });

  it("marks empty capability cards for every reported domain", () => {
    render(
      <BusinessAgentReviewView
        review={parseBusinessAgentReview({
          ...projection,
          scope: [{ domain: "technical", capability_ids: [], source_ids: null }],
          domains: [
            { domain: "technical", status: "completed", capabilities: [] },
            { domain: "commercial", status: "completed", capabilities: [] },
          ],
          gaps: [],
          evidence: [],
          unresolved_gap_ids: [],
        })}
      />,
    );
    expect(screen.getAllByText("暂无检查明细。")).toHaveLength(2);
  });
});

describe("business agent review", () => {
  it("shows required checks, gaps and verified quotations without treating location as confirmation", () => {
    render(<BusinessAgentReviewView review={parseBusinessAgentReview(projection)} />);
    expect(screen.getAllByText("技术与服务").length).toBeGreaterThan(0);
    expect(screen.getByText("质保期限")).toBeInTheDocument();
    expect(screen.getByText("必查项目；比较结果：证据不足。")).toBeInTheDocument();
    expect(screen.getByText("未解决")).toBeInTheDocument();
    expect(screen.getByText("来源材料中的引用已核验")).toBeInTheDocument();
    expect(screen.getByText("质保36个月")).toBeInTheDocument();
    expect(screen.getByText("引用尚未核验。")).toBeInTheDocument();
    expect(screen.getByText("关联引用详情不可用。")).toBeInTheDocument();
    expect(screen.queryByText("未经核验原文")).not.toBeInTheDocument();
    expect(screen.queryByText(/doc-1|parsed-1|gap-1/)).not.toBeInTheDocument();
    expect(screen.getByText(/找到原文依据，并不代表风险已确认或合同已合规/)).toBeInTheDocument();
  });

  it("keeps missing and unknown results separate from success", () => {
    const review = parseBusinessAgentReview({
      ...projection,
      status: null,
      scope: [
        { domain: "commercial", capability_ids: ["amount"], source_ids: null },
        { domain: "technical", capability_ids: ["warranty_months", "authorization_basis", "internal.secret_code"], source_ids: null },
      ],
      domains: [{ domain: "technical", status: "constructor", capabilities: [{ ...warranty, status: "unknown", relation: null, reason_code: "private-hash", evidence_ids: null }] }],
      gaps: null,
      evidence: null,
      unresolved_gap_ids: null,
    });
    render(<BusinessAgentReviewView review={review} />);
    expect(screen.getAllByText("状态未知").length).toBeGreaterThan(0);
    expect(screen.getByText("必查项目；比较结果未知。")).toBeInTheDocument();
    expect(screen.getByText("部分项目没有原文依据，不能确认这些项目已经核验。")).toBeInTheDocument();
    expect(screen.getByText("该领域已列入范围，检查结果和必查状态未知。")).toBeInTheDocument();
    expect(screen.getAllByText("是否必查：未知。")).toHaveLength(2);
    expect(screen.getByText("授权依据")).toBeInTheDocument();
    expect(screen.getByText("未识别审查项目")).toBeInTheDocument();
    expect(screen.queryByText(/internal.secret_code|private-hash/)).not.toBeInTheDocument();
    expect(screen.getByText("暂无缺口资料；这不等于不存在风险。")).toBeInTheDocument();
    expect(screen.queryByText("已通过")).not.toBeInTheDocument();
  });

  it("does not display a quotation when a claimed verification lacks a usable location", () => {
    const review = parseBusinessAgentReview({
      ...projection,
      evidence: [{ ...checkedEvidence, anchor: { ...anchor, parsed_document_id: "", quote: "位置不完整的原文" } }],
    });
    render(<BusinessAgentReviewView review={review} />);
    expect(screen.getByText("核验引用的信息不完整，需进一步核对。")).toBeInTheDocument();
    expect(screen.queryByText("位置不完整的原文")).not.toBeInTheDocument();
  });

  it("explains an absent review without claiming coverage", () => {
    render(<BusinessAgentReviewView review={null} />);
    expect(screen.getByText("本次没有返回五个方面的审查结果，不能因此认为已经全部检查。")).toBeInTheDocument();
  });

  it("uses one concise empty state when the projection contains no details", () => {
    const review = parseBusinessAgentReview({
      ...projection,
      status: null,
      scope: null,
      domains: null,
      gaps: null,
      evidence: null,
      unresolved_gap_ids: null,
    });
    render(<BusinessAgentReviewView review={review} />);
    expect(screen.getByText("本次没有返回检查范围、待补充事项和原文依据，不能因此认为已经完成检查。")).toBeInTheDocument();
    expect(screen.getByText("状态未知")).toBeInTheDocument();
    expect(screen.queryByText(/未报告/)).not.toBeInTheDocument();
  });
});
