import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MemoryRouter } from "react-router";
import { parseAgentReview } from "../api/agent-review";
import type { ExecutionEvent } from "../api/types";
import { AgentReviewInspector } from "./agent-review-inspector";
import { renderWithAntd as render } from "../test/antd";

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
      limits: {
        max_rounds: 2,
        max_calls: 5,
        max_duration_ms: 1000,
        max_evidence_bytes: 4096,
      },
    },
    scope: [
      {
        domain: "compliance",
        capability_ids: ["approval_authority", "unregistered.audit"],
        source_ids: ["doc-1"],
      },
    ],
    domains: [
      {
        domain: "compliance",
        status: "partial",
        capabilities: [
          {
            capability_id: "approval_authority",
            mandatory: false,
            status: "unknown",
            relation: null,
            reason_code: "source_fact_only_not_a_decision",
            evidence_ids: [],
            canonical_evaluation_ids: [],
          },
          {
            capability_id: "unregistered.audit",
            mandatory: false,
            status: "unsupported",
            relation: null,
            reason_code: "capability_not_registered",
            evidence_ids: [],
            canonical_evaluation_ids: [],
          },
        ],
      },
    ],
    gaps: [
      {
        gap_id: "gap-approval",
        domain: "compliance",
        capability_id: "approval_authority",
        mandatory: false,
        status: "insufficient",
        reason_code: "source_fact_only_not_a_decision",
        evidence_ids: [],
      },
      {
        gap_id: "gap-unsupported",
        domain: "compliance",
        capability_id: "unregistered.audit",
        mandatory: false,
        status: "unsupported",
        reason_code: "capability_not_registered",
        evidence_ids: [],
      },
    ],
    plan: { status: "partial", rejection_code: null, calls: [], rounds: [] },
    evidence: [],
    usage: {
      planner_calls: 1,
      input_tokens: null,
      output_tokens: null,
      total_tokens: null,
    },
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
      calls: [
        call,
        {
          ...call,
          call_id: "call-denied",
          status: "denied",
          reason_code: "tool_authority_denied",
        },
      ],
      rounds: [
        {
          round: 1,
          status: "accepted",
          gap_ids: ["gap-approval"],
          calls: [call],
          new_evidence_ids: [],
          resolved_gap_ids: [],
          termination_code: null,
        },
        {
          round: 2,
          status: "guard_rejected",
          gap_ids: ["gap-approval"],
          calls: [
            {
              ...call,
              call_id: "call-denied",
              status: "denied",
              reason_code: "tool_authority_denied",
            },
          ],
          new_evidence_ids: [],
          resolved_gap_ids: [],
          termination_code: "tool_authority_denied",
        },
      ],
    },
  });
}

function reviewWithApprovalValidity() {
  const base = reviewWithUnsupportedCapability();
  const evidenceIds = [
    "authority-valid-from",
    "authority-valid-until",
    "contract-reference-date",
  ];
  const capability = {
    capability_id: "approval_validity",
    mandatory: false,
    status: "partial",
    relation: "insufficient",
    reason_code: "approval_period_dates_unverified",
    evidence_ids: evidenceIds,
    canonical_evaluation_ids: [],
  };
  const identityCapabilities = [
    {
      capability_id: "signing_party",
      mandatory: false,
      status: "partial",
      relation: "insufficient",
      reason_code: "stable_party_role_linkage_missing",
      evidence_ids: [],
      canonical_evaluation_ids: [],
    },
    {
      capability_id: "payee",
      mandatory: false,
      status: "partial",
      relation: "insufficient",
      reason_code: "stable_party_role_linkage_missing",
      evidence_ids: [],
      canonical_evaluation_ids: [],
    },
  ];
  const citedDates = [
    {
      evidence_id: evidenceIds[0],
      semantic_type: "authority_valid_from",
      value: "2026-01-01",
      document_id: "doc-approval",
      quote: "有效期自2026年1月1日起",
    },
    {
      evidence_id: evidenceIds[1],
      semantic_type: "authority_valid_until",
      value: "2026-12-31",
      document_id: "doc-approval",
      quote: "有效期至2026年12月31日",
    },
    {
      evidence_id: evidenceIds[2],
      semantic_type: "contract_reference_date",
      value: "2026-06-01",
      document_id: "doc-contract",
      quote: "合同引用日期：2026年6月1日",
    },
  ];
  return parseAgentReview({
    ...base,
    scope: base.scope.map((item) =>
      item.domain === "compliance"
        ? {
            ...item,
            capability_ids: [
              ...item.capability_ids,
              "approval_validity",
              ...identityCapabilities.map((entry) => entry.capability_id),
            ],
          }
        : item,
    ),
    domains: base.domains.map((item) =>
      item.domain === "compliance"
        ? {
            ...item,
            capabilities: [
              ...item.capabilities,
              capability,
              ...identityCapabilities,
            ],
          }
        : item,
    ),
    gaps: [
      ...base.gaps,
      {
        gap_id: "gap-approval-validity",
        domain: "compliance",
        capability_id: "approval_validity",
        mandatory: false,
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
    unresolved_gap_ids: [
      ...base.unresolved_gap_ids,
      "gap-approval-validity",
      ...identityCapabilities.map((entry) => `gap-${entry.capability_id}`),
    ],
  });
}

describe("AgentReview Inspector", () => {
  it("explains supported expressions and keeps unknown capabilities unresolved", () => {
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={reviewWithUnsupportedCapability()}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByText(/从本次审查使用的材料中提取审批或授权原文/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/当前没有这一检查项目的说明/),
    ).toBeInTheDocument();
    const nextStepLinks = screen.getAllByRole("link", {
      name: "查看案件材料并补证后新建审查",
    });
    expect(nextStepLinks).toHaveLength(2);
    for (const link of nextStepLinks)
      expect(link).toHaveAttribute("href", "/cases/case-1#case-materials");
    expect(
      within(
        screen.getByRole("region", { name: "范围、支持表达与证据覆盖" }),
      ).getAllByText("unregistered.audit").length,
    ).toBeGreaterThan(0);
  });

  it("explains approval validity and shows each cited authority and contract date", () => {
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={reviewWithApprovalValidity()}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    const scope = within(
      screen.getByRole("region", { name: "范围、支持表达与证据覆盖" }),
    );
    expect(scope.getByText("approval_validity")).toBeInTheDocument();
    expect(scope.getByText(/不能用当前日期代替/)).toBeInTheDocument();
    expect(
      scope.getByText(/原因：approval_period_dates_unverified/),
    ).toBeInTheDocument();
    const citations = within(
      scope.getByRole("list", { name: "approval_validity 来源引用" }),
    );
    for (const evidenceId of [
      "authority-valid-from",
      "authority-valid-until",
      "contract-reference-date",
    ]) {
      expect(citations.getByText(evidenceId)).toBeInTheDocument();
    }
    for (const quote of [
      "有效期自2026年1月1日起",
      "有效期至2026年12月31日",
      "合同引用日期：2026年6月1日",
    ]) {
      expect(citations.getByText(quote)).toBeInTheDocument();
    }
    expect(scope.getByText(/名称相同不足以确认签署身份/)).toBeInTheDocument();
    expect(scope.getByText(/名称相同不足以确认收款资格/)).toBeInTheDocument();
    expect(
      scope.getAllByText(/stable_party_role_linkage_missing/),
    ).toHaveLength(2);
  });

  it("marks compatible completed capability results good only with verified located evidence", () => {
    const review = reviewWithApprovalValidity();
    const capability = review.domains[0]?.capabilities.find(
      (item) => item.capability_id === "approval_validity",
    );
    if (!capability) throw new Error("expected capability fixture");
    capability.status = "completed";
    capability.relation = "compatible";
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
  });

  it("keeps completion without verified evidence neutral and historical null IDs unrecorded", () => {
    const review = reviewWithUnsupportedCapability();
    const capability = review.domains[0]?.capabilities[0];
    if (!capability) throw new Error("expected capability fixture");
    capability.status = "completed";
    capability.relation = "compatible";
    review.plan.rounds = [
      {
        round: 1,
        status: "partial",
        gap_ids: [],
        calls: [],
        new_evidence_ids: null,
        resolved_gap_ids: null,
        termination_code: null,
      },
    ];
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByText(/新证据：未记录，\s*已解决缺口：未记录/),
    ).toBeInTheDocument();
  });

  it("keeps blocked and rejected lifecycle states distinct", () => {
    const review = reviewWithUnsupportedCapability();
    review.status = "blocked_live";
    review.plan.status = "guard_rejected";
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
  });

  it("labels pre-round events as preparation without inventing round one", () => {
    const review = reviewWithUnsupportedCapability();
    review.events = [
      {
        sequence: 1,
        round: 0,
        kind: "planning",
        call_id: null,
        domain: null,
        code: "plan_started",
        duration_ms: 0,
      },
    ];
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByText(/1:准备阶段:planning\/plan_started/),
    ).toBeInTheDocument();
  });

  it("keeps unresolved IDs actionable when the response omits gap details", () => {
    const review = parseAgentReview({
      ...reviewWithUnsupportedCapability(),
      gaps: [],
      unresolved_gap_ids: ["gap-without-details"],
    });
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByText(/未报告缺口详情，\s*gap-without-details/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/未提供领域、能力、原因或证据详情/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "查看案件材料并补证后新建审查" }),
    ).toHaveAttribute("href", "/cases/case-1#case-materials");
    expect(
      screen.queryByText("没有报告缺口记录；这不等同于合同没有风险。"),
    ).not.toBeInTheDocument();
  });

  it("preserves in-progress and denied call statuses from durable planner rounds", () => {
    const review = reviewWithRuntimeCallStatuses();
    expect(review.plan.calls.map((call) => call.status)).toEqual([
      "running",
      "denied",
    ]);
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(screen.getAllByText("running").length).toBeGreaterThan(0);
    expect(screen.getAllByText("denied").length).toBeGreaterThan(0);
  });

  it("does not project a review from a different attempt", () => {
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={reviewWithUnsupportedCapability()}
          caseId="case-1"
          attemptId="attempt-2"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByText(/检查记录所属的执行编号与当前选择的执行记录不一致/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/规划器模式/)).not.toBeInTheDocument();
  });

  it("shows ordering suggestions without turning them into conclusions", () => {
    const review = reviewWithUnsupportedCapability();
    review.knowledge_selection = {
      snapshot_sha256: "a".repeat(64),
      algorithm: "chinese-bigram-label/v1",
      policy_status: "available",
      policy_reason: "",
      advisory: {
        mode: "advisory",
        feature: "knowledge_rerank",
        provider: "model-gateway",
        configuration_id: "b".repeat(64),
        model_requested: "jev-1.14.0",
        model_observed: "jev-1.14.0",
        question_set_version: "knowledge-relevance/v1",
        egress: "minimized",
        request_sha256: "c".repeat(64),
        judgment_id: "d".repeat(64),
        status: "completed",
        error_code: null,
        truncated: false,
        candidates: [
          {
            knowledge_id: "guide-b",
            version_id: "v1",
            content_sha256: "e".repeat(64),
            alias: "candidate_0",
            probability: 0.9,
            band: "relevant",
            rank_before: 2,
            rank_after: 1,
            reason: null,
          },
          {
            knowledge_id: "guide-a",
            version_id: "v1",
            content_sha256: "f".repeat(64),
            alias: "candidate_1",
            probability: 0.1,
            band: "not_relevant",
            rank_before: 1,
            rank_after: 2,
            reason: null,
          },
        ],
      },
    };
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    const region = within(
      screen.getByRole("region", { name: "治理知识与排序建议" }),
    );
    expect(
      region.getByText(/Jev 建议排序，不改变结论/),
    ).toBeInTheDocument();
    expect(region.getByText("建议已生成")).toBeInTheDocument();
    expect(region.getByText("guide-b")).toBeInTheDocument();
    expect(
      region.getByText(/确定性排序第 2 位，建议排序第 1 位/),
    ).toBeInTheDocument();
    expect(region.getByText(/不是正确率，也不是风险结论/)).toBeInTheDocument();
  });

  it("shows the deterministic fallback when the advisory is unavailable", () => {
    const review = reviewWithUnsupportedCapability();
    review.knowledge_selection = {
      snapshot_sha256: "a".repeat(64),
      algorithm: "chinese-bigram-label/v1",
      policy_status: "unknown",
      policy_reason: "policy_missing",
      advisory: {
        mode: "advisory",
        feature: "knowledge_rerank",
        provider: "model-gateway",
        configuration_id: "b".repeat(64),
        model_requested: "jev-1.14.0",
        model_observed: null,
        question_set_version: "knowledge-relevance/v1",
        egress: "minimized",
        request_sha256: null,
        judgment_id: null,
        status: "unavailable",
        error_code: "provider_timeout",
        truncated: false,
        candidates: [],
      },
    };
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    const region = within(
      screen.getByRole("region", { name: "治理知识与排序建议" }),
    );
    expect(region.getByText("建议不可用")).toBeInTheDocument();
    expect(region.getByText("provider_timeout")).toBeInTheDocument();
    expect(
      region.getByText(/按确定性顺序回退/),
    ).toBeInTheDocument();
    expect(
      region.getByText(/排序保持确定性顺序/),
    ).toBeInTheDocument();
  });
});

describe("AgentReview Inspector tones and missing details", () => {
  function renderReview(
    review = reviewWithUnsupportedCapability(),
    executionEvents: ExecutionEvent[] = [],
  ) {
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={executionEvents}
        />
      </MemoryRouter>,
    );
  }

  it("colors failed reviews as errors and completed reviews neutrally", () => {
    const failed = reviewWithUnsupportedCapability();
    failed.status = "failed";
    const view = render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={failed}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("failed").className).toContain("ant-tag-error");
    view.unmount();
    const completed = reviewWithUnsupportedCapability();
    completed.status = "completed";
    renderReview(completed);
    expect(screen.getByText("completed").className).toContain("ant-tag-default");
  });

  it("keeps an absent review and a mismatched attempt explicit", () => {
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={null}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByText(/本次执行没有保存五个方面的检查计划/),
    ).toBeInTheDocument();
  });

  it("marks a compatible verified capability good, conflicts bad and unknown relations attention", () => {
    const review = reviewWithApprovalValidity();
    const capability = review.domains[0]?.capabilities.find(
      (item) => item.capability_id === "approval_validity",
    )!;
    capability.status = "completed";
    capability.relation = "compatible";
    const view = render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("符合").className).toContain("ant-tag-success");
    view.unmount();
    capability.relation = "conflict";
    capability.status = "supported";
    const conflictView = render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("冲突").className).toContain("ant-tag-error");
    conflictView.unmount();
    capability.relation = null;
    renderReview(review);
    expect(screen.getByText("supported").className).toContain(
      "ant-tag-warning",
    );
  });

  it("reports an unreported citation detail without inventing evidence", () => {
    const review = reviewWithApprovalValidity();
    const domain = review.domains[0]!;
    domain.capabilities.push({
      capability_id: "ghost_capability",
      mandatory: false,
      status: "supported",
      relation: "compatible",
      reason_code: "",
      evidence_ids: ["ghost-evidence"],
      canonical_evaluation_ids: [],
    });
    review.scope[0]!.capability_ids.push("ghost_capability");
    renderReview(review);
    const region = within(
      screen.getByRole("region", { name: "范围、支持表达与证据覆盖" }),
    );
    expect(region.getByText("ghost-evidence")).toBeInTheDocument();
    expect(region.getByText(/来源引用详情未报告/)).toBeInTheDocument();
  });

  it("lists observed model references and usage sources from execution events", () => {
    renderReview(reviewWithUnsupportedCapability(), [
      { model_ref: "model-a", usage: { source: "gateway" } },
      { model_ref: "model-a", usage: { source: "cache" } },
      { model_ref: null, usage: null },
    ] as ExecutionEvent[]);
    expect(screen.getByText("model-a")).toBeInTheDocument();
    expect(screen.getByText("gateway、cache")).toBeInTheDocument();
  });

  it("marks a failed advisory as attention-toned and keeps deterministic fallback", () => {
    const review = reviewWithUnsupportedCapability();
    review.knowledge_selection = {
      snapshot_sha256: "a".repeat(64),
      algorithm: "chinese-bigram-label/v1",
      policy_status: "unknown",
      policy_reason: "policy_missing",
      advisory: {
        mode: "fixture",
        feature: "knowledge_rerank",
        provider: "local-fixture",
        configuration_id: "b".repeat(64),
        model_requested: null,
        model_observed: "observed",
        question_set_version: "knowledge-relevance/v1",
        egress: "minimized",
        request_sha256: null,
        judgment_id: null,
        status: "invalid_response",
        error_code: null,
        truncated: true,
        candidates: [
          {
            knowledge_id: "guide-c",
            version_id: "v2",
            content_sha256: "c".repeat(64),
            alias: "candidate_0",
            probability: null,
            band: "unknown",
            rank_before: 3,
            rank_after: 3,
            reason: "no_signal",
          },
        ],
      },
    };
    renderReview(review);
    const region = within(
      screen.getByRole("region", { name: "治理知识与排序建议" }),
    );
    expect(region.getByText("响应无效").className).toContain("ant-tag-warning");
    expect(region.getByText("本地 fixture， 本地 fixture")).toBeInTheDocument();
    expect(region.getByText(/候选过多，本次仅评估了部分可选知识/)).toBeInTheDocument();
    expect(region.getByText(/建议概率 未返回/)).toBeInTheDocument();
    expect(region.getByText(/原因 no_signal/)).toBeInTheDocument();
    expect(region.getByText(/该失败本身不构成审查结论/)).toBeInTheDocument();
  });
});

describe("AgentReview Inspector rich configuration and evidence", () => {
  it("renders snapshot, rulepack, fallback and every evidence field", () => {
    const review = reviewWithUnsupportedCapability();
    review.planner.fallback_used = true;
    review.planner.fallback_code = null;
    review.planner.model_configuration_id = "model-config-1";
    review.source_snapshot = {
      document_ids: [],
      parsed_document_ids: ["parsed-1"],
      parse_selection_hash: "selection-hash",
      parser_configuration_id: "parser-config-1",
    };
    review.rulepack = { id: "pack-1", version: "9", content_sha256: "pack-sha" };
    review.scope.push({
      domain: "commercial",
      capability_ids: [],
      source_ids: [],
    });
    review.domains[0]!.capabilities.push({
      capability_id: "amount",
      mandatory: false,
      status: "supported",
      relation: "compatible",
      reason_code: "",
      evidence_ids: ["rich", "unlocated", "empty-quote"],
      canonical_evaluation_ids: [],
    });
    review.evidence = [
      {
        evidence_id: "rich",
        semantic_type: "amount",
        value: "100",
        unit: "CNY",
        currency: "人民币",
        verification: "verified",
        anchor: {
          id: "a-1",
          document_id: "doc-1",
          parsed_document_id: "parsed-1",
          normalized_digest: "norm-1",
          quote: "100元",
          status: "located",
          start: 0,
          end: 4,
          reason: null,
          page: 3,
          element_id: "el-1",
        },
      },
      {
        evidence_id: "unlocated",
        semantic_type: "amount",
        value: "50",
        unit: null,
        currency: null,
        verification: "unknown",
        anchor: {
          id: "a-2",
          document_id: "doc-1",
          parsed_document_id: "parsed-1",
          normalized_digest: "norm-1",
          quote: "",
          status: "unlocatable",
          start: 0,
          end: 0,
          reason: null,
          page: null,
          element_id: null,
        },
      },
      {
        evidence_id: "empty-quote",
        semantic_type: "amount",
        value: "20",
        unit: null,
        currency: null,
        verification: "verified",
        anchor: {
          id: "a-3",
          document_id: "doc-1",
          parsed_document_id: "parsed-1",
          normalized_digest: "norm-1",
          quote: "",
          status: "located",
          start: 0,
          end: 0,
          reason: null,
          page: null,
          element_id: null,
        },
      },
    ];
    render(
      <MemoryRouter>
        <AgentReviewInspector
          agentReview={review}
          caseId="case-1"
          attemptId="attempt-1"
          executionEvents={[]}
        />
      </MemoryRouter>,
    );
    expect(screen.getAllByText(/无已报告 ID/).length).toBeGreaterThan(0);
    expect(screen.getByText("selection-hash")).toBeInTheDocument();
    expect(screen.getByText(/pack-1 v9/)).toBeInTheDocument();
    expect(screen.getByText("已回退：原因未报告")).toBeInTheDocument();
    expect(screen.getByText("model-config-1")).toBeInTheDocument();
    expect(screen.getAllByText("未报告").length).toBeGreaterThan(0);
    expect(screen.getAllByText("没有报告具体能力结果。").length).toBeGreaterThan(0);
    expect(screen.getByText("amount: 100 CNY，人民币")).toBeInTheDocument();
    expect(screen.getByText(/页 3，元素 el-1/)).toBeInTheDocument();
    expect(screen.getByText("引用未定位：未提供原因")).toBeInTheDocument();
    expect(screen.getByText("没有报告原文引用。")).toBeInTheDocument();
  });
});
