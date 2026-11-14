// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “TrajectoryView”; representative assertions cover shows a chronological call timeline with expandable request and output, filters entries and reports when nothing matches, does not imply activity when the execution saved no events. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-11-12
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import { fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { parseAgentReview } from "../api/agent-review";
import type { Execution, ExecutionAttempt, ExecutionEvent, ModelCallTrace } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { TrajectoryView } from "./trajectory-view";

const attempt: ExecutionAttempt = {
  id: "attempt-1",
  run_id: "run-1",
  ordinal: 1,
  retry_of_attempt_id: null,
  state: "completed",
  accepted_at: "2026-10-30T00:00:00.000Z",
  started_at: "2026-10-30T00:00:00.000Z",
  finished_at: "2026-10-30T00:00:03.000Z",
  parsed_document_ids: [],
  input_hash: null,
  output_hash: null,
  error_code: null,
  history_kind: "frozen",
};

const trace: ModelCallTrace = {
  requested_model: "codex/gpt-6-luna",
  reported_model: "codex/gpt-6-luna",
  status: "completed",
  error_code: "",
  started_at: "2026-10-30T00:00:01.000Z",
  finished_at: "2026-10-30T00:00:02.000Z",
  request_sha256: "request-digest",
  request_bytes: 64,
  request_messages: [{ role: "user", content: "冻结原文", truncated: false }],
  request_truncated: false,
  response_sha256: "response-digest",
  response_bytes: 24,
  response_text: "模型输出内容",
  response_truncated: false,
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
    at: "2026-10-30T00:00:00.000Z",
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
      sources: [],
      known_subtotals: { input_tokens: 100, output_tokens: 20, total_tokens: 120 },
      cost: null,
    },
    all_attempts_usage: null,
    duration_ms: 3000,
    first_verified_finding_ms: null,
    queue_wait_ms: null,
  };
}

const events = [
  event({ span_id: "span-stage", event_type: "span_started" }),
  event({
    span_id: "span-stage",
    event_type: "node_completed",
    at: "2026-10-30T00:00:00.500Z",
    duration_ms: 500,
    status: "completed",
  }),
  event({
    seq: 3,
    span_id: "span-model",
    node_kind: "model_call",
    phase: "model_request",
    event_type: "span_started",
    at: "2026-10-30T00:00:01.000Z",
    model_ref: "codex/gpt-6-luna",
  }),
  event({
    seq: 4,
    span_id: "span-model",
    node_kind: "model_call",
    phase: "model_request",
    event_type: "model_completed",
    at: "2026-10-30T00:00:02.000Z",
    duration_ms: 1000,
    status: "completed",
    model_ref: "codex/gpt-6-luna",
    trace,
    usage: {
      source: "provider_response.usage",
      input_tokens: 100,
      output_tokens: 20,
      total_tokens: 120,
    },
  }),
];

function duplicateToolReview() {
  const call = (id: string, gap: string) => ({
    call_id: id,
    tool_id: "evidence.lookup",
    tool_version: "tool-v1",
    domain: "commercial",
    capability_id: "amount",
    gap_id: gap,
    status: "completed",
    reason_code: "verified_result",
    evidence_ids: [],
  });
  const toolEvent = (
    sequence: number,
    event_id: string,
    kind: "tool_started" | "tool_completed",
    callId: string,
    gap: string,
    at: string,
  ) => ({
    event_id,
    sequence,
    round: 1,
    kind,
    tool_id: "evidence.lookup",
    gap_id: gap,
    call_id: callId,
    domain: "commercial",
    code: kind === "tool_started" ? "read_action_started" : "validated_tool_result",
    occurred_at: at,
    duration_ms: kind === "tool_started" ? 0 : 100,
  });
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
    scope: [],
    domains: [],
    gaps: [],
    plan: {
      status: "completed",
      rejection_code: null,
      calls: [call("call-1", "gap-1"), call("call-2", "gap-2")],
      rounds: [],
    },
    evidence: [],
    usage: { planner_calls: 1, input_tokens: null, output_tokens: null, total_tokens: null },
    unresolved_gap_ids: [],
    events: [
      toolEvent(1, "tool-1-start", "tool_started", "call-1", "gap-1", "2026-10-30T00:00:01.000Z"),
      toolEvent(2, "tool-1-done", "tool_completed", "call-1", "gap-1", "2026-10-30T00:00:01.100Z"),
      toolEvent(3, "tool-2-start", "tool_started", "call-2", "gap-2", "2026-10-30T00:00:02.000Z"),
      toolEvent(4, "tool-2-done", "tool_completed", "call-2", "gap-2", "2026-10-30T00:00:02.100Z"),
    ],
  });
}

describe("TrajectoryView", () => {
  async function showList() {
    const radio = screen.queryByRole("radio", { name: "列表" });
    await userEvent.click(radio ?? screen.getByText("列表"));
  }

  it("shows merged calls with reported usage in the execution diagram", async () => {
    render(<TrajectoryView execution={execution(events)} />);
    expect(screen.getByRole("region", { name: "Agent 执行路径泳道图" })).toBeInTheDocument();
    expect(screen.getByText(/2 个调用 \/ 阶段/)).toBeInTheDocument();
    await userEvent.click(screen.getByText("按顺序选择节点（键盘可用）"));
    expect(
      screen.getByRole("button", { name: /模型 · codex\/gpt-6-luna · #4/ }),
    ).toBeInTheDocument();
  });

  it("opens a merged call with span and reported token details", async () => {
    render(<TrajectoryView execution={execution(events)} />);
    await userEvent.click(screen.getByText("按顺序选择节点（键盘可用）"));
    await userEvent.click(screen.getByRole("button", { name: /模型 · codex\/gpt-6-luna · #4/ }));
    expect(
      screen.getByText(/输入 100 · 输出 20 · 合计 120 · 来源 provider_response.usage/),
    ).toBeInTheDocument();
    expect(screen.getByText(/Span span-model/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /#3/ })).toBeInTheDocument();
  });

  it("shows a chronological call timeline with expandable request and output", async () => {
    render(<TrajectoryView execution={execution(events)} />);
    await showList();
    expect(screen.getByRole("heading", { name: "运行轨迹" })).toBeInTheDocument();
    expect(screen.getByText("2 条记录")).toBeInTheDocument();
    expect(screen.getByText("3.00 秒")).toBeInTheDocument();

    const model = screen.getByRole("button", { name: /模型调用 · codex\/gpt-6-luna/ });
    expect(model).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByText("模型输出内容")).not.toBeVisible();

    await userEvent.click(model);
    expect(model).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("模型输出内容")).toBeVisible();
    expect(screen.getByText("request-digest")).toBeVisible();

    await userEvent.click(screen.getByRole("button", { name: "展开全部" }));
    expect(screen.getByText("模型输出内容")).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "收起全部" }));
    expect(screen.getByText("模型输出内容")).not.toBeVisible();
  });

  it("switches from the swimlane to the list when a search starts", () => {
    render(<TrajectoryView execution={execution(events)} />);
    expect(screen.getByRole("region", { name: "Agent 执行路径泳道图" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "泳道图" })).toBeChecked();
    expect(screen.getByText(/泳道图始终展示全部步骤；搜索只筛选列表/)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("搜索轨迹"), { target: { value: "模型" } });

    expect(screen.getByRole("radio", { name: "列表" })).toBeChecked();
    expect(screen.queryByRole("region", { name: "Agent 执行路径泳道图" })).not.toBeInTheDocument();
    expect(screen.getByText("已按“模型”筛选出 1 条记录。")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /模型调用 · codex\/gpt-6-luna/ }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /输入准备/ })).not.toBeInTheDocument();
  });

  it("keeps the list view and all entries after the search is cleared", () => {
    render(<TrajectoryView execution={execution(events)} />);
    const search = screen.getByLabelText("搜索轨迹");
    fireEvent.change(search, { target: { value: "模型" } });
    expect(screen.getByText("已按“模型”筛选出 1 条记录。")).toBeInTheDocument();

    fireEvent.change(search, { target: { value: "" } });

    expect(screen.getByRole("radio", { name: "列表" })).toBeChecked();
    expect(screen.queryByRole("region", { name: "Agent 执行路径泳道图" })).not.toBeInTheDocument();
    expect(screen.queryByText(/筛选出/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /输入准备/ })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /模型调用 · codex\/gpt-6-luna/ }),
    ).toBeInTheDocument();
  });

  it("shows the existing empty message when a search from the swimlane matches nothing", () => {
    render(<TrajectoryView execution={execution(events)} />);
    fireEvent.change(screen.getByLabelText("搜索轨迹"), {
      target: { value: "不存在的内容" },
    });

    expect(screen.getByRole("radio", { name: "列表" })).toBeChecked();
    expect(screen.getByText("已按“不存在的内容”筛选出 0 条记录。")).toBeInTheDocument();
    expect(screen.getByText("没有匹配的轨迹记录。")).toBeInTheDocument();
  });

  it("keeps the search input when switching between the swimlane and the list", async () => {
    render(<TrajectoryView execution={execution(events)} />);
    const search = screen.getByLabelText("搜索轨迹");
    fireEvent.change(search, { target: { value: "模型" } });
    expect(screen.getByRole("radio", { name: "列表" })).toBeChecked();

    await userEvent.click(screen.getByRole("radio", { name: "泳道图" }));

    expect(search).toHaveValue("模型");
    expect(screen.getByRole("region", { name: "Agent 执行路径泳道图" })).toBeInTheDocument();
    expect(screen.getByText(/当前搜索“模型”仍保留，切回列表可查看匹配记录/)).toBeInTheDocument();
    expect(screen.queryByText(/已按“模型”筛选出/)).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "列表" }));

    expect(search).toHaveValue("模型");
    expect(screen.getByText("已按“模型”筛选出 1 条记录。")).toBeInTheDocument();
  });

  it("keeps duplicate tool steps distinct and read-only when filtering", () => {
    render(
      <TrajectoryView execution={{ ...execution([]), agent_review: duplicateToolReview() }} />,
    );
    const summary = screen.getByLabelText("轨迹汇总");
    expect(within(summary).getByText("2")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("搜索轨迹"), {
      target: { value: "evidence.lookup" },
    });

    expect(screen.getByText("已按“evidence.lookup”筛选出 4 条记录。")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /evidence\.lookup/ })).toHaveLength(4);
    expect(screen.queryByRole("button", { name: /保存|编辑|删除/ })).not.toBeInTheDocument();
  });

  it("filters entries and reports when nothing matches", async () => {
    render(<TrajectoryView execution={execution(events)} />);
    await showList();
    const search = screen.getByLabelText("搜索轨迹");
    fireEvent.change(search, { target: { value: "模型" } });
    expect(screen.getByText("已按“模型”筛选出 1 条记录。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /输入准备/ })).not.toBeInTheDocument();

    fireEvent.change(search, { target: { value: "不存在的内容" } });
    expect(screen.getByText("没有匹配的轨迹记录。")).toBeInTheDocument();
  });

  it("does not imply activity when the execution saved no events", async () => {
    render(<TrajectoryView execution={execution([])} />);
    expect(screen.getByText("本次执行没有保存可展示的步骤。")).toBeInTheDocument();
    await showList();
    expect(screen.getByText("本次执行没有保存可展示的轨迹记录。")).toBeInTheDocument();
  });
});
