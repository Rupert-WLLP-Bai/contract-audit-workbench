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
    expect(screen.getByText(/来源引用核验不等于风险已确认或合同合规/)).toBeInTheDocument();
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
    expect(screen.getByText("部分项目暂无引用，不能据此确认已核验。")).toBeInTheDocument();
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
    expect(screen.getByText("本次暂无五域结果，不能据此推断已覆盖。")).toBeInTheDocument();
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
    expect(screen.getByText("本次暂无领域、缺口和引用信息，不能据此推断已覆盖。")).toBeInTheDocument();
    expect(screen.getByText("状态未知")).toBeInTheDocument();
    expect(screen.queryByText(/未报告/)).not.toBeInTheDocument();
  });
});
