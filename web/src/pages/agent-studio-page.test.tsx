// File metadata (manifest mirror):
// Purpose: Checks the read-only Agent Studio page: unknown diagnostics stay unobserved, the event list filters with exact loaded counts, swimlane node selection shows saved detail, invalid identities fail safely, and phase lanes preserve saved order.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-06
// Updated: 2026-11-06
// Revision: r1
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
} from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import {
  AgentStudioPage,
  buildStudioLanes,
  filterStudioEvents,
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
          <Route
            path="/cases/:caseId/reviews/:runId/agent"
            element={<AgentStudioPage />}
          />
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
    expect(
      screen.getByText(/不会用审查摘要反推事件、步骤或耗时/),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "能力关系" }));

    expect(
      screen.getByText("模型", { selector: ".studio-capability-title" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("工具与 MCP", { selector: ".studio-capability-title" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("规则与知识", { selector: ".studio-capability-title" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("输入证据", { selector: ".studio-capability-title" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("经验", { selector: ".studio-capability-title" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("未观测").length).toBeGreaterThanOrEqual(5);
    expect(screen.queryByText(/0 次真实调用/)).not.toBeInTheDocument();
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
        name: /模型请求 第 2 号事件 模型调用完成/,
      }),
    );

    expect(screen.getByRole("heading", { name: "节点详情" })).toBeInTheDocument();
    expect(screen.getAllByText("模型调用").length).toBeGreaterThan(0);
    expect(screen.getByText("provider_response.usage")).toBeInTheDocument();
    expect(screen.getByText("父 Span span-stage")).toBeInTheDocument();
    expect(screen.getByText("1.00 秒")).toBeInTheDocument();
  });

  it("handles a missing case or run identity without rendering execution data", () => {
    renderWithoutRoute();
    expect(screen.getByText("审查身份无法确认")).toBeInTheDocument();
    expect(
      screen.getByText("地址缺少案件或审查标识，请返回审查记录重新进入。"),
    ).toBeInTheDocument();
    expect(screen.queryByText("本次执行")).not.toBeInTheDocument();
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
    expect(lanes.map((lane) => lane.label)).toEqual([
      "输入准备",
      "模型请求",
      "custom_phase",
    ]);
    expect(lanes[1]?.events.map((item) => item.seq)).toEqual([2, 3]);
    expect(filterStudioEvents(shuffled, "custom_event").map((item) => item.seq)).toEqual([
      4,
    ]);
  });
});
