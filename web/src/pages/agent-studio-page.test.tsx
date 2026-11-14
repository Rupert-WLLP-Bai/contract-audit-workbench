// File metadata (manifest mirror):
// Purpose: Checks the read-only Agent Studio page: unknown diagnostics stay unobserved, the event list filters with exact loaded counts, swimlane node selection shows saved detail, invalid identities fail safely, and phase lanes preserve saved order.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-06
// Updated: 2026-11-12
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  Execution,
  ExecutionAttempt,
  ExecutionEvent,
  ReviewDiagnostics,
  ReviewWorkspace,
  RunSnapshot,
} from "../api/types";
import { tokenCount } from "../domain/execution-labels";
import { renderWithAntd as render } from "../test/antd";
import {
  AgentStudioPage,
  buildCapabilityGroups,
  buildStudioLanes,
  capabilitySummary,
  filterStudioEvents,
  plannerModeOf,
  planRoundsText,
} from "./agent-studio-page";

const mocks = vi.hoisted(() => ({
  useReview: vi.fn(),
  useReviewDiagnostics: vi.fn(),
  useCaseDetail: vi.fn(),
}));

vi.mock("../api/queries", () => ({
  useReview: mocks.useReview,
  useReviewDiagnostics: mocks.useReviewDiagnostics,
  useCaseDetail: mocks.useCaseDetail,
}));

const attempt: ExecutionAttempt = {
  id: "attempt-1",
  run_id: "run-1",
  ordinal: 1,
  retry_of_attempt_id: null,
  state: "completed",
  accepted_at: "2026-11-07T00:00:00.000Z",
  started_at: "2026-11-07T00:00:00.000Z",
  finished_at: "2026-11-07T00:00:03.000Z",
  parsed_document_ids: [],
  input_hash: null,
  output_hash: null,
  error_code: null,
  history_kind: "frozen",
};

function event(overrides: Partial<ExecutionEvent>): ExecutionEvent {
  return {
    seq: 1,
    attempt_id: "attempt-1",
    span_id: null,
    parent_span_id: null,
    node_kind: "lambda",
    phase: "input_preparation",
    event_type: "span_started",
    at: "2026-11-07T00:00:00.000Z",
    duration_ms: null,
    status: "running",
    error_code: null,
    model_ref: null,
    usage: null,
    trace: null,
    ...overrides,
  };
}

function execution(events: ExecutionEvent[]): Execution {
  return {
    schema_version: "audit-execution/v1",
    attempt,
    attempts: [attempt],
    events,
    agent_review: null,
    usage: {
      input_tokens: 100,
      output_tokens: 20,
      total_tokens: 120,
      calls: 1,
      unknown_calls: 0,
      sources: ["provider_response.usage"],
      known_subtotals: {
        input_tokens: 100,
        output_tokens: 20,
        total_tokens: 120,
      },
      cost: null,
    },
    all_attempts_usage: null,
    duration_ms: 3000,
    first_verified_finding_ms: null,
    queue_wait_ms: null,
  };
}

function workspace(): ReviewWorkspace {
  return {
    run: {
      id: "run-1",
      case_id: "case-1",
      state: "completed",
      updated_at: "2026-11-07T00:00:03.000Z",
      started_at: "2026-11-07T00:00:00.000Z",
      model_ref: null,
      partial_reason: null,
      failure_message: null,
    },
    documents: [],
    findings: [],
    review_attempts: { attempt, attempts: [attempt] },
    agent_review: null,
    execution: null,
    audit_events: [],
  } as ReviewWorkspace;
}

function diagnostics(executionValue: Execution | null): ReviewDiagnostics {
  return {
    case_id: "case-1",
    run_id: "run-1",
    execution: executionValue,
    snapshot: null,
    audit_events: [],
  };
}

const events = [
  event({
    seq: 1,
    span_id: "span-stage",
    phase: "input_preparation",
    event_type: "span_started",
    at: "2026-11-07T00:00:00.000Z",
    status: "running",
  }),
  event({
    seq: 2,
    span_id: "span-model",
    parent_span_id: "span-stage",
    node_kind: "model_call",
    phase: "model_request",
    event_type: "model_completed",
    at: "2026-11-07T00:00:01.000Z",
    duration_ms: 1000,
    status: "completed",
    model_ref: "codex/gpt-6-luna",
    usage: {
      source: "provider_response.usage",
      input_tokens: 100,
      output_tokens: 20,
      total_tokens: 120,
    },
  }),
  event({
    seq: 3,
    span_id: "span-tool",
    parent_span_id: "span-stage",
    node_kind: "tool_call",
    phase: "evidence_tool",
    event_type: "tool_evidence",
    at: "2026-11-07T00:00:02.000Z",
    duration_ms: 300,
    status: "completed",
    model_ref: "evidence.lookup",
  }),
];

function query<T>(data: T, overrides: Record<string, unknown> = {}) {
  return {
    data,
    isLoading: false,
    isSuccess: true,
    isError: false,
    isFetching: false,
    error: null,
    refetch: vi.fn(),
    ...overrides,
  };
}

function renderPage(path = "/cases/case-1/reviews/run-1/agent") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/cases/:caseId/reviews/:runId/agent" element={<AgentStudioPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function renderWithoutRoute() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AgentStudioPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("AgentStudioPage", () => {
  beforeEach(() => {
    mocks.useReview.mockReturnValue(query(workspace()));
    mocks.useReviewDiagnostics.mockReturnValue(query(diagnostics(null)));
    mocks.useCaseDetail.mockReturnValue(
      query({ id: "case-1", title: "采购合同审查", documents: [] }),
    );
  });

  it("shows unobserved states without fabricating execution or capability records", async () => {
    renderPage();
    expect(screen.getByText("未观测到本次执行")).toBeInTheDocument();
    expect(screen.getByText(/不会用审查摘要反推事件、步骤或耗时/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "能力关系" }));

    expect(screen.getByText("模型", { selector: ".studio-capability-title" })).toBeInTheDocument();
    expect(
      screen.getByText("工具与 MCP", { selector: ".studio-capability-title" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("规则与知识", { selector: ".studio-capability-title" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("输入证据", { selector: ".studio-capability-title" }),
    ).toBeInTheDocument();
    expect(screen.getByText("经验", { selector: ".studio-capability-title" })).toBeInTheDocument();
    expect(screen.getAllByText("未观测").length).toBeGreaterThanOrEqual(5);
    expect(screen.queryByText(/0 次真实调用/)).not.toBeInTheDocument();
  });

  it("keeps missing token usage unknown instead of displaying zero", () => {
    const value = execution([]);
    value.usage = null;
    mocks.useReviewDiagnostics.mockReturnValue(query(diagnostics(value)));
    renderPage();

    const tokenTotal = screen.getByText("Token 合计").parentElement;
    expect(tokenTotal).not.toBeNull();
    expect(tokenTotal).toHaveTextContent("未知");
    expect(tokenCount(null)).toBe("未知");
  });

  it("filters the loaded event list and reports matched versus loaded counts", async () => {
    mocks.useReviewDiagnostics.mockReturnValue(query(diagnostics(execution(events))));
    renderPage();

    await userEvent.click(screen.getByRole("radio", { name: "事件列表" }));
    expect(screen.getByText("当前匹配 3 / 已加载 3")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("搜索事件"), {
      target: { value: "模型" },
    });
    expect(screen.getByText("当前匹配 1 / 已加载 3")).toBeInTheDocument();
    expect(screen.getByText("模型调用完成")).toBeInTheDocument();
    expect(screen.queryByText("开始")).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("搜索事件"), {
      target: { value: "不存在的内容" },
    });
    expect(screen.getByText("当前匹配 0 / 已加载 3")).toBeInTheDocument();
    expect(screen.getByText("没有匹配的事件。")).toBeInTheDocument();
  });

  it("opens a swimlane node detail with saved status, span and token usage", async () => {
    mocks.useReviewDiagnostics.mockReturnValue(query(diagnostics(execution(events))));
    renderPage();

    expect(screen.getByRole("radio", { name: "本次执行" })).toBeChecked();
    await userEvent.click(
      screen.getByRole("button", {
        name: /模型 · codex\/gpt-6-luna · #2/,
      }),
    );

    expect(screen.getByRole("heading", { name: "节点详情" })).toBeInTheDocument();
    expect(screen.getAllByText("模型调用").length).toBeGreaterThan(0);
    expect(screen.getByText("provider_response.usage")).toBeInTheDocument();
    expect(screen.getByText("父 Span span-stage")).toBeInTheDocument();
    expect(screen.getByText("1.00 秒")).toBeInTheDocument();
  });

  it("shows the Eino invocation only when the recorded runtime identifies v4", async () => {
    const tool = event({
      seq: 5,
      span_id: "tool-5",
      node_kind: "tool_call",
      event_type: "agent_review_event",
      status: "completed",
      error_code: "validated_tool_result",
      model_ref: "evidence.lookup",
    });
    const snapshot: RunSnapshot = {
      document_ids: [],
      rule_set_hash: "",
      playbook_hash: "",
      model_ref: null,
      comparator_ref: null,
      planner: {
        mode: "deterministic_planner",
        configuration_id: "config",
        model_configuration_id: null,
        prompt_version: "v1",
        schema_version: "v1",
        runtime_version: "operator-flow-v4/eino-0.9.22",
        tool_catalog_version: "v1",
        max_rounds: 1,
      },
    };
    mocks.useReviewDiagnostics.mockReturnValue(
      query({ ...diagnostics(execution([tool])), snapshot }),
    );
    renderPage();
    await userEvent.click(screen.getByText("按顺序选择节点（键盘可用）"));
    await userEvent.click(screen.getByRole("button", { name: /工具 · evidence.lookup · #5/ }));
    expect(screen.getByText("Eino InvokableTool")).toBeInTheDocument();
    expect(screen.getByText("结果代码")).toBeInTheDocument();
    expect(screen.getByText("validated_tool_result")).toBeInTheDocument();
    expect(screen.queryByText("错误代码")).not.toBeInTheDocument();
  });

  it("handles a missing case or run identity without rendering execution data", () => {
    renderWithoutRoute();
    expect(screen.getByText("审查身份无法确认")).toBeInTheDocument();
    expect(
      screen.getByText("地址缺少案件或审查标识，请返回审查记录重新进入。"),
    ).toBeInTheDocument();
    expect(screen.queryByText("本次执行")).not.toBeInTheDocument();
  });

  it("projects frozen planner, rule pack, knowledge and anchored findings without inventing tools", async () => {
    const snapshot: RunSnapshot = {
      document_ids: ["doc-1", "doc-2", "doc-3", "doc-4"],
      parsed_document_ids: ["parse-1"],
      parse_selection_hash: "parse-hash",
      parser_configuration_id: "parser-1",
      rule_set_hash: "rules",
      playbook_hash: "playbook",
      model_ref: "panda-local/cqmoma/cqcm/deepseek-v4-flash",
      comparator_ref: null,
      planner: {
        mode: "deterministic_planner",
        configuration_id: "planner-id",
        model_configuration_id: null,
        prompt_version: "scope-tool-selection/v5",
        schema_version: "agent-plan/v4",
        runtime_version: "operator-flow-v3/eino-0.9.22",
        tool_catalog_version: "catalog",
        max_rounds: 2,
      },
      governed_knowledge: {
        snapshot_sha256: "knowledge-sha",
        rulepack: { id: "telecom-governed", version: "1", content_sha256: "pack-sha" },
        selections: [
          {
            knowledge_id: "telecom-business-demo-policy",
            version_id: "v1",
            kind: "policy",
            selection_reason: "published, authorized, in scope and effective at business time",
            required: true,
          },
        ],
      },
    };
    const modelExecution = execution([
      event({
        seq: 23,
        node_kind: "model_call",
        phase: "model_request",
        event_type: "model_completed",
        status: "completed",
      }),
    ]);
    modelExecution.attempt = { ...attempt, parsed_document_ids: ["parse-1"] };
    const groups = buildCapabilityGroups({
      execution: modelExecution,
      agentReview: null,
      businessReview: null,
      snapshot,
      documentCount: 4,
      diagnosticsLoaded: true,
      findings: [{ id: "f1", claim: "预付款冲突", origin: "rule", anchorCount: 1 }],
    });
    const byKey = Object.fromEntries(groups.map((group) => [group.key, group]));
    expect(plannerModeOf(undefined, modelExecution, snapshot)).toBe("deterministic_planner");
    expect(planRoundsText(null, "deterministic_planner")).toBe("未执行");
    expect(capabilitySummary(byKey.model!)).toBe("3 / 3 项来源已观测");
    expect(byKey.model?.facts.find((item) => item.key === "planner-model")?.value).toBe(
      "已记录规划配置，没有规划模型",
    );
    expect(capabilitySummary(byKey.tools!)).toBe("本组来源均未观测");
    expect(byKey.tools?.facts.map((item) => item.value)).toEqual([
      "没有计划调用",
      "没有工具观测",
      "没有工具调用事件",
      "没有 MCP 连接",
    ]);
    expect(capabilitySummary(byKey.rules!)).toBe("2 / 4 项来源已观测");
    expect(byKey.rules?.facts.find((item) => item.key === "plan-rounds")?.value).toBe("未执行");
    expect(byKey.rules?.facts.find((item) => item.key === "knowledge-reads")?.value).toBe(
      "没有知识读取记录",
    );
    expect(capabilitySummary(byKey.evidence!)).toBe("4 / 4 项来源已观测");
    expect(capabilitySummary(byKey.experience!)).toBe("本组来源均未观测");

    mocks.useReview.mockReturnValue(
      query({
        ...workspace(),
        documents: ["doc-1", "doc-2", "doc-3", "doc-4"].map((id) => ({
          id,
          role: "draft_contract",
          filename: id,
          media_type: "text/plain",
          digest: "digest",
          size: 1,
          parse_status: "ready",
          parse_error: null,
          created_at: "2026-11-07T00:00:00Z",
          parsed_document: null,
          parsed_document_id: null,
          text: null,
          clauses: [],
        })),
        findings: [
          {
            id: "f1",
            origin: "rule",
            domain: "commercial",
            claim: "预付款冲突",
            relation: "conflict",
            confidence: null,
            verification_status: "verified",
            evidence: [
              {
                id: "anchor-1",
                document_version_id: "doc-1",
                parsed_document_id: null,
                normalized_digest: null,
                quote: "预付款",
                status: "located",
                page: null,
                text_offset: null,
                bbox: null,
                mismatch_reason: null,
              },
            ],
            unverified_reason: null,
            version: 1,
            severity: null,
            severity_source: null,
            decisions: [],
          },
        ],
      } as ReviewWorkspace),
    );
    mocks.useReviewDiagnostics.mockReturnValue(query({ ...diagnostics(modelExecution), snapshot }));
    renderPage();
    await userEvent.click(screen.getByRole("radio", { name: "能力关系" }));
    expect(screen.getAllByText("确定性规划").length).toBeGreaterThan(0);
    expect(screen.getAllByText("未执行").length).toBeGreaterThan(0);
    expect(screen.getByText("规则与模型审查已完成")).toBeInTheDocument();
    expect(screen.getByText("2 / 4 项来源已观测")).toBeInTheDocument();
    expect(screen.getAllByText("本组来源均未观测")).toHaveLength(2);
  });

  it("groups lanes by actual phase and keeps the saved event order", () => {
    const shuffled = [
      event({
        seq: 3,
        phase: "model_request",
        event_type: "model_completed",
        status: "completed",
      }),
      event({
        seq: 1,
        phase: "input_preparation",
        event_type: "span_started",
        status: "running",
      }),
      event({
        seq: 2,
        phase: "model_request",
        event_type: "model_completed",
        status: "completed",
      }),
      event({
        seq: 4,
        phase: "custom_phase",
        event_type: "custom_event",
        status: "completed",
      }),
    ];
    const lanes = buildStudioLanes(shuffled);
    expect(lanes.map((lane) => lane.label)).toEqual(["输入准备", "模型请求", "custom_phase"]);
    expect(lanes[1]?.events.map((item) => item.seq)).toEqual([2, 3]);
    expect(filterStudioEvents(shuffled, "custom_event").map((item) => item.seq)).toEqual([4]);
  });
});
