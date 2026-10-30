import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type {
  Execution,
  ExecutionAttempt,
  ExecutionEvent,
  ModelCallTrace,
} from "../api/types";
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

describe("TrajectoryView", () => {
  it("shows a chronological call timeline with expandable request and output", async () => {
    render(<TrajectoryView execution={execution(events)} />);
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

  it("filters entries and reports when nothing matches", () => {
    render(<TrajectoryView execution={execution(events)} />);
    const search = screen.getByLabelText("搜索轨迹");
    fireEvent.change(search, { target: { value: "模型" } });
    expect(screen.getByText("已按“模型”筛选出 1 条记录。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /输入准备/ })).not.toBeInTheDocument();

    fireEvent.change(search, { target: { value: "不存在的内容" } });
    expect(screen.getByText("没有匹配的轨迹记录。")).toBeInTheDocument();
  });

  it("does not imply activity when the execution saved no events", () => {
    render(<TrajectoryView execution={execution([])} />);
    expect(
      screen.getByText("本次执行没有保存可展示的轨迹记录。"),
    ).toBeInTheDocument();
  });
});
