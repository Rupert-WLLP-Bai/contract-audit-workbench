import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MemoryRouter } from "react-router";
import { parseAgentReview } from "../api/agent-review";
import { AgentReviewInspector } from "./agent-review-inspector";

function reviewWithUnsupportedCapability() {
  return parseAgentReview({
    schema_version: "five-domain-review/v1",
    attempt_id: "attempt-1",
    status: "partial",
    mode: "deterministic_planner",
    source_snapshot: null,
    rulepack: null,
    planner: {
      identity: "planner",
      configuration_id: "planner-config",
      model_configuration_id: null,
      prompt_version: "prompt-v1",
      prompt_sha256: "prompt-digest",
      schema_version: "plan-v1",
      runtime_version: "runtime-v1",
      tool_catalog_version: "tools-v1",
      fallback_used: false,
      fallback_code: null,
      limits: { max_rounds: 2, max_calls: 5, max_duration_ms: 1000, max_evidence_bytes: 4096 },
    },
    scope: [{ domain: "compliance", capability_ids: ["approval_authority", "unregistered.audit"], source_ids: ["doc-1"] }],
    domains: [{
      domain: "compliance",
      status: "partial",
      capabilities: [
        { capability_id: "approval_authority", status: "unknown", relation: null, reason_code: "source_fact_only_not_a_decision", evidence_ids: [], canonical_evaluation_ids: [] },
        { capability_id: "unregistered.audit", status: "unsupported", relation: null, reason_code: "capability_not_registered", evidence_ids: [], canonical_evaluation_ids: [] },
      ],
    }],
    gaps: [
      { gap_id: "gap-approval", domain: "compliance", capability_id: "approval_authority", status: "insufficient", reason_code: "source_fact_only_not_a_decision", evidence_ids: [] },
      { gap_id: "gap-unsupported", domain: "compliance", capability_id: "unregistered.audit", status: "unsupported", reason_code: "capability_not_registered", evidence_ids: [] },
    ],
    plan: { status: "partial", rejection_code: null, calls: [], rounds: [] },
    evidence: [],
    usage: { planner_calls: 1, input_tokens: null, output_tokens: null, total_tokens: null },
    unresolved_gap_ids: ["gap-approval", "gap-unsupported"],
    events: [],
  });
}

function reviewWithRuntimeCallStatuses() {
  const base = reviewWithUnsupportedCapability();
  const call = {
    call_id: "call-running",
    tool_id: "source.compliance",
    tool_version: "v1",
    domain: "compliance",
    capability_id: "approval_authority",
    gap_id: "gap-approval",
    status: "running",
    reason_code: null,
    evidence_ids: [],
  };
  return parseAgentReview({
    ...base,
    plan: {
      status: "partial",
      rejection_code: null,
      calls: [call, { ...call, call_id: "call-denied", status: "denied", reason_code: "tool_authority_denied" }],
      rounds: [
        { round: 1, status: "accepted", gap_ids: ["gap-approval"], calls: [call], new_evidence_ids: [], resolved_gap_ids: [], termination_code: null },
        { round: 2, status: "guard_rejected", gap_ids: ["gap-approval"], calls: [{ ...call, call_id: "call-denied", status: "denied", reason_code: "tool_authority_denied" }], new_evidence_ids: [], resolved_gap_ids: [], termination_code: "tool_authority_denied" },
      ],
    },
  });
}

function reviewWithApprovalValidity() {
  const base = reviewWithUnsupportedCapability();
  const evidenceIds = ["authority-valid-from", "authority-valid-until", "contract-reference-date"];
  const capability = {
    capability_id: "approval_validity",
    status: "partial",
    relation: "insufficient",
    reason_code: "approval_period_dates_unverified",
    evidence_ids: evidenceIds,
    canonical_evaluation_ids: [],
  };
  const identityCapabilities = [
    { capability_id: "signing_party", status: "partial", relation: "insufficient", reason_code: "stable_party_role_linkage_missing", evidence_ids: [], canonical_evaluation_ids: [] },
    { capability_id: "payee", status: "partial", relation: "insufficient", reason_code: "stable_party_role_linkage_missing", evidence_ids: [], canonical_evaluation_ids: [] },
  ];
  const citedDates = [
    { evidence_id: evidenceIds[0], semantic_type: "authority_valid_from", value: "2026-01-01", document_id: "doc-approval", quote: "有效期自2026年1月1日起" },
    { evidence_id: evidenceIds[1], semantic_type: "authority_valid_until", value: "2026-12-31", document_id: "doc-approval", quote: "有效期至2026年12月31日" },
    { evidence_id: evidenceIds[2], semantic_type: "contract_reference_date", value: "2026-06-01", document_id: "doc-contract", quote: "合同引用日期：2026年6月1日" },
  ];
  return parseAgentReview({
    ...base,
    scope: base.scope.map((item) => item.domain === "compliance"
      ? { ...item, capability_ids: [...item.capability_ids, "approval_validity", ...identityCapabilities.map((entry) => entry.capability_id)] }
      : item),
    domains: base.domains.map((item) => item.domain === "compliance"
      ? { ...item, capabilities: [...item.capabilities, capability, ...identityCapabilities] }
      : item),
    gaps: [
      ...base.gaps,
      {
        gap_id: "gap-approval-validity",
        domain: "compliance",
        capability_id: "approval_validity",
        status: "insufficient",
        reason_code: capability.reason_code,
        evidence_ids: evidenceIds,
      },
      ...identityCapabilities.map((entry) => ({
        gap_id: `gap-${entry.capability_id}`,
        domain: "compliance",
        capability_id: entry.capability_id,
        status: "insufficient",
        reason_code: entry.reason_code,
        evidence_ids: [],
      })),
    ],
    evidence: citedDates.map((item, index) => ({
      evidence_id: item.evidence_id,
      semantic_type: item.semantic_type,
      value: item.value,
      unit: null,
      currency: null,
      verification: "verified",
      anchor: {
        id: `anchor-${index}`,
        document_id: item.document_id,
        parsed_document_id: `parsed-${item.document_id}`,
        normalized_digest: `digest-${item.document_id}`,
        quote: item.quote,
        status: "located",
        start: 0,
        end: Array.from(item.quote).length,
        reason: null,
        page: null,
        element_id: null,
      },
    })),
    unresolved_gap_ids: [...base.unresolved_gap_ids, "gap-approval-validity", ...identityCapabilities.map((entry) => `gap-${entry.capability_id}`)],
  });
}

describe("AgentReview Inspector", () => {
  it("explains supported expressions and keeps unknown capabilities unresolved", () => {
    render(<MemoryRouter><AgentReviewInspector agentReview={reviewWithUnsupportedCapability()} caseId="case-1" attemptId="attempt-1" executionEvents={[]} /></MemoryRouter>);
    expect(screen.getByText(/从冻结材料抽取审批或授权原文/)).toBeInTheDocument();
    expect(screen.getByText(/当前界面没有此能力的支持说明/)).toBeInTheDocument();
    expect(screen.getByText("能力不支持")).toHaveClass("status-attention");
    expect(document.querySelector(".status-good")).toBeNull();
    const nextStepLinks = screen.getAllByRole("link", { name: "查看案件材料并补证后新建审查" });
    expect(nextStepLinks).toHaveLength(2);
    for (const link of nextStepLinks) expect(link).toHaveAttribute("href", "/cases/case-1#case-materials");
    expect(within(screen.getByRole("region", { name: "范围、支持表达与证据覆盖" })).getAllByText("unregistered.audit").length).toBeGreaterThan(0);
  });

  it("explains approval validity and shows each cited authority and contract date", () => {
    render(<MemoryRouter><AgentReviewInspector agentReview={reviewWithApprovalValidity()} caseId="case-1" attemptId="attempt-1" executionEvents={[]} /></MemoryRouter>);
    const scope = within(screen.getByRole("region", { name: "范围、支持表达与证据覆盖" }));
    expect(scope.getByText("approval_validity")).toBeInTheDocument();
    expect(scope.getByText(/不使用当前日期补值/)).toBeInTheDocument();
    expect(scope.getByText(/原因：approval_period_dates_unverified/)).toBeInTheDocument();
    const citations = within(scope.getByRole("list", { name: "approval_validity 来源引用" }));
    for (const evidenceId of ["authority-valid-from", "authority-valid-until", "contract-reference-date"]) {
      expect(citations.getByText(evidenceId)).toBeInTheDocument();
    }
    for (const quote of ["有效期自2026年1月1日起", "有效期至2026年12月31日", "合同引用日期：2026年6月1日"]) {
      expect(citations.getByText(quote)).toBeInTheDocument();
    }
    expect(scope.getByText(/名称相同不足以确认签署身份/)).toBeInTheDocument();
    expect(scope.getByText(/名称相同不足以确认收款资格/)).toBeInTheDocument();
    expect(scope.getAllByText(/stable_party_role_linkage_missing/)).toHaveLength(2);
  });

  it("marks compatible completed capability results good only with verified located evidence", () => {
    const review = reviewWithApprovalValidity();
    const capability = review.domains[0]?.capabilities.find((item) => item.capability_id === "approval_validity");
    if (!capability) throw new Error("expected capability fixture");
    capability.status = "completed";
    capability.relation = "compatible";
    render(<MemoryRouter><AgentReviewInspector agentReview={review} caseId="case-1" attemptId="attempt-1" executionEvents={[]} /></MemoryRouter>);
    expect(screen.getByText("符合")).toHaveClass("status-good");
    expect(screen.getByText("能力不支持")).toHaveClass("status-attention");
  });

  it("keeps completion without verified evidence neutral and historical null IDs unrecorded", () => {
    const review = reviewWithUnsupportedCapability();
    const capability = review.domains[0]?.capabilities[0];
    if (!capability) throw new Error("expected capability fixture");
    capability.status = "completed";
    capability.relation = "compatible";
    review.plan.rounds = [{ round: 1, status: "partial", gap_ids: [], calls: [], new_evidence_ids: null, resolved_gap_ids: null, termination_code: null }];
    render(<MemoryRouter><AgentReviewInspector agentReview={review} caseId="case-1" attemptId="attempt-1" executionEvents={[]} /></MemoryRouter>);
    expect(screen.getByText("符合")).not.toHaveClass("status-good");
    expect(screen.getByText(/新证据：未记录 · 已解决缺口：未记录/)).toBeInTheDocument();
  });

  it("keeps blocked and rejected lifecycle states distinct", () => {
    const review = reviewWithUnsupportedCapability();
    review.status = "blocked_live";
    review.plan.status = "guard_rejected";
    render(<MemoryRouter><AgentReviewInspector agentReview={review} caseId="case-1" attemptId="attempt-1" executionEvents={[]} /></MemoryRouter>);
    expect(screen.getByText("blocked_live")).toHaveClass("status-attention");
    expect(screen.getByText("guard_rejected")).toHaveClass("status-bad");
  });

  it("labels pre-round events as preparation without inventing round one", () => {
    const review = reviewWithUnsupportedCapability();
    review.events = [{ sequence: 1, round: 0, kind: "planning", call_id: null, domain: null, code: "plan_started", duration_ms: 0 }];
    render(<MemoryRouter><AgentReviewInspector agentReview={review} caseId="case-1" attemptId="attempt-1" executionEvents={[]} /></MemoryRouter>);
    expect(screen.getByText(/1:准备阶段:planning\/plan_started/)).toBeInTheDocument();
  });

  it("keeps unresolved IDs actionable when the response omits gap details", () => {
    const review = parseAgentReview({
      ...reviewWithUnsupportedCapability(),
      gaps: [],
      unresolved_gap_ids: ["gap-without-details"],
    });
    render(<MemoryRouter><AgentReviewInspector agentReview={review} caseId="case-1" attemptId="attempt-1" executionEvents={[]} /></MemoryRouter>);
    expect(screen.getByText("未报告缺口详情 · gap-without-details")).toBeInTheDocument();
    expect(screen.getByText(/未提供领域、能力、原因或证据详情/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "查看案件材料并补证后新建审查" })).toHaveAttribute("href", "/cases/case-1#case-materials");
    expect(screen.queryByText("没有报告缺口记录；这不等同于合同没有风险。")).not.toBeInTheDocument();
  });

  it("preserves in-progress and denied call statuses from durable planner rounds", () => {
    const review = reviewWithRuntimeCallStatuses();
    expect(review.plan.calls.map((call) => call.status)).toEqual(["running", "denied"]);
    render(<MemoryRouter><AgentReviewInspector agentReview={review} caseId="case-1" attemptId="attempt-1" executionEvents={[]} /></MemoryRouter>);
    expect(screen.getAllByText("running").length).toBeGreaterThan(0);
    expect(document.querySelector(".status-bad")?.textContent).toBe("denied");
    expect(document.querySelector(".status-good")).toBeNull();
  });

  it("does not project a review from a different attempt", () => {
    render(<MemoryRouter><AgentReviewInspector agentReview={reviewWithUnsupportedCapability()} caseId="case-1" attemptId="attempt-2" executionEvents={[]} /></MemoryRouter>);
    expect(screen.getByText(/attempt ID 与当前执行快照不一致/)).toBeInTheDocument();
    expect(screen.queryByText(/规划器模式/)).not.toBeInTheDocument();
  });
});
