import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type {
  ConstraintEvaluation,
  Execution,
  ExecutionAttempt,
  ExecutionEvent,
  Finding,
} from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { ExecutionInspector } from "./execution-inspector";

const at = "2026-10-21T06:00:00Z";

function attempt(
  id: string,
  ordinal: number,
  state: string,
  overrides: Partial<ExecutionAttempt> = {},
): ExecutionAttempt {
  return {
    id,
    run_id: "run-1",
    ordinal,
    retry_of_attempt_id: null,
    state,
    accepted_at: at,
    started_at: at,
    finished_at: state === "running" ? null : at,
    parsed_document_ids: ["parsed-1"],
    input_hash: "input-hash",
    output_hash: "output-hash",
    error_code: null,
    history_kind: "frozen",
    ...overrides,
  };
}

function event(
  seq: number,
  overrides: Partial<ExecutionEvent> = {},
): ExecutionEvent {
  return {
    seq,
    attempt_id: "attempt-2",
    span_id: null,
    parent_span_id: null,
    node_kind: "node",
    phase: "acceptance",
    event_type: "attempt_accepted",
    at,
    duration_ms: null,
    status: "accepted",
    error_code: null,
    model_ref: null,
    usage: null,
    ...overrides,
  };
}

const events: ExecutionEvent[] = [
  event(1),
  event(2, {
    span_id: "span-1",
    phase: "deterministic_rules",
    event_type: "span_started",
    status: "running",
    node_kind: "span",
    duration_ms: 1500,
  }),
  event(3, {
    span_id: "span-2",
    parent_span_id: "span-1",
    phase: "model_request",
    event_type: "model_completed",
    status: "completed",
    node_kind: "call",
    duration_ms: 250,
    model_ref: "model-a",
    usage: {
      source: "gateway",
      input_tokens: 1000,
      output_tokens: null,
      total_tokens: 1000,
    },
  }),
  event(4, {
    span_id: "span-1",
    phase: "deterministic_rules",
    event_type: "span_completed",
    status: "completed",
    node_kind: "span",
    duration_ms: 1250,
  }),
  event(99, { attempt_id: "attempt-1", phase: "acceptance" }),
];

const locatedFinding: Finding = {
  id: "f1",
  origin: "rule",
  domain: "履约",
  claim: "已定位线索",
  relation: "conflict",
  confidence: null,
  verification_status: "verified",
  evidence: [
    {
      id: "a1",
      document_version_id: "doc-1",
      parsed_document_id: null,
      normalized_digest: null,
      quote: "期限",
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
};

const pendingFinding: Finding = {
  ...locatedFinding,
  id: "f2",
  claim: "待定位线索",
  verification_status: "unverified",
  unverified_reason: "引用未核对",
  evidence: [
    { ...locatedFinding.evidence[0]!, id: "a2", status: "anchor_mismatch" },
  ],
};

const unresolvedFinding: Finding = {
  ...locatedFinding,
  id: "f3",
  claim: "无引用线索",
  verification_status: "unknown",
  evidence: [],
};

const evaluations: ConstraintEvaluation[] = [
  {
    id: "e1",
    domain: "履约",
    relation: "insufficient",
    rule_version: "1",
    reason: "缺少验收记录",
    rule_id: "rule-1",
    capability_id: "cap-1",
    object_refs: [],
    finding_id: null,
    reason_code: "missing",
    fact_ids: [],
  },
  {
    id: "e2",
    domain: "商务",
    relation: "conflict",
    rule_version: "1",
    reason: "金额不一致",
    rule_id: "rule-2",
    capability_id: "cap-2",
    object_refs: [],
    finding_id: null,
    reason_code: "mismatch",
    fact_ids: [],
  },
];

function execution(overrides: Partial<Execution> = {}): Execution {
  return {
    schema_version: "audit-execution/v1",
    attempt: attempt("attempt-2", 2, "running"),
    attempts: [attempt("attempt-1", 1, "failed"), attempt("attempt-2", 2, "running")],
    events,
    agent_review: null,
    usage: {
      input_tokens: 1500,
      output_tokens: 500,
      total_tokens: 2000,
      calls: 2,
      unknown_calls: 1,
      sources: ["gateway"],
      known_subtotals: {
        input_tokens: 1000,
        output_tokens: null,
        total_tokens: 1000,
      },
      cost: null,
    },
    all_attempts_usage: {
      input_tokens: null,
      output_tokens: null,
      total_tokens: null,
      calls: 0,
      unknown_calls: 0,
      sources: [],
      known_subtotals: {
        input_tokens: null,
        output_tokens: null,
        total_tokens: null,
      },
      cost: null,
    },
    duration_ms: null,
    first_verified_finding_ms: 500,
    queue_wait_ms: null,
    ...overrides,
  };
}

function renderInspector(
  overrides: Partial<Parameters<typeof ExecutionInspector>[0]> = {},
) {
  const onSelectFinding = vi.fn();
  const onSelectAttempt = vi.fn();
  const onCancel = vi.fn();
  const view = render(
    <MemoryRouter>
      <ExecutionInspector
        execution={execution()}
        caseId="case-1"
        findings={[locatedFinding, pendingFinding, unresolvedFinding]}
        evaluations={evaluations}
        selectedFindingId={null}
        onSelectFinding={onSelectFinding}
        onSelectAttempt={onSelectAttempt}
        canCancel={false}
        cancelling={false}
        cancelError={null}
        onCancel={onCancel}
        {...overrides}
      />
    </MemoryRouter>,
  );
  return { ...view, onSelectFinding, onSelectAttempt, onCancel };
}

describe("execution inspector", () => {
  it("summarizes the attempt, phase, duration, events and usage", () => {
    renderInspector();
    expect(
      screen.getByText(/第 2 次执行，最近保存的阶段 确定性规则审查/),
    ).toBeInTheDocument();
    expect(screen.getAllByText(/已定位线索/).length).toBeGreaterThan(0);
    expect(
      screen.getAllByText("运行中，尚无终态时长").length,
    ).toBeGreaterThan(0);
    expect(screen.getByText("4 条")).toBeInTheDocument();
    expect(screen.getByText("2 次")).toBeInTheDocument();
    expect(
      screen.getByText("第 2 次尝试状态：审查中。"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(
        "较早的审查没有保存当时使用的材料版本记录，无法补回当时的执行过程。",
      ),
    ).not.toBeInTheDocument();
  });

  it("shows the legacy warning and completion duration for a frozen terminal attempt", () => {
    const result = execution({
      duration_ms: 5000,
      attempt: attempt("attempt-2", 2, "completed", {
        history_kind: "legacy_unfrozen",
      }),
    });
    renderInspector({ execution: result });
    expect(
      screen.getByText(
        "较早的审查没有保存当时使用的材料版本记录，无法补回当时的执行过程。",
      ),
    ).toBeInTheDocument();
    expect(screen.getAllByText("5.00 秒").length).toBeGreaterThan(0);
  });

  it("lists attempt options newest first and reports selection", async () => {
    const user = userEvent.setup();
    const { onSelectAttempt } = renderInspector();
    const select = screen.getByRole("combobox", { name: "查看执行记录" });
    await user.click(select);
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual([
      "第 2 次，审查中，最新",
      "第 1 次，失败",
    ]);
    await user.click(options[1]!);
    expect(onSelectAttempt.mock.calls[0]?.[0]).toBe("attempt-1");
    expect(
      document
        .querySelector("[data-attempt-id='attempt-2']")
        ?.getAttribute("data-parsed-document-ids"),
    ).toBe(JSON.stringify(["parsed-1"]));
  });

  it("renders a timeline with ordered events and selects an event detail", async () => {
    const user = userEvent.setup();
    renderInspector();
    expect(screen.getByText("实际执行事件")).toBeInTheDocument();
    expect(screen.getByText("4 条事件，仅显示第 2 次执行保存的记录")).toBeInTheDocument();
    const target = screen.getByRole("button", {
      name: /序号 3（#3），模型请求，模型调用完成，已完成/,
    });
    expect(
      screen.getByRole("button", {
        name: /序号 1（#1），受理，执行已受理，已受理/,
      }),
    ).toHaveAttribute("aria-current", "true");
    await user.click(target);
    expect(screen.getByText("模型调用完成，已完成")).toBeInTheDocument();
    expect(screen.getAllByText("250 毫秒").length).toBeGreaterThan(0);
    await user.click(
      screen.getByRole("button", {
        name: /序号 2（#2），确定性规则审查，开始，进行中/,
      }),
    );
    const detail = screen.getByText("序号").closest("dl")!;
    expect(within(detail).getByText("2")).toBeInTheDocument();
    expect(within(detail).getByText(/确定性规则审查/)).toBeInTheDocument();
    expect(within(detail).getByText(/span-1/)).toBeInTheDocument();
    expect(within(detail).getAllByText(/未报告/).length).toBeGreaterThan(0);
  });

  it("shows event diagnostics for unknown codes and call usage", () => {
    const unknown = event(7, {
      attempt_id: "attempt-2",
      phase: "custom_phase",
      event_type: "custom_event",
      status: "custom_status",
      node_kind: "custom_node",
      error_code: "E-1",
      model_ref: "model-x",
      duration_ms: null,
      at: "not-a-date",
      span_id: null,
      parent_span_id: null,
      usage: { source: "cache", input_tokens: null, output_tokens: 5, total_tokens: null },
    });
    renderInspector({ execution: execution({ events: [unknown] }) });
    expect(screen.getByText("custom_phase")).toBeInTheDocument();
    expect(screen.getAllByText("custom_event").length).toBeGreaterThan(0);
    expect(screen.getAllByText("custom_status").length).toBeGreaterThan(0);
    expect(screen.getByText("custom_node")).toBeInTheDocument();
    expect(screen.getByText("E-1")).toBeInTheDocument();
    expect(screen.getByText("model-x")).toBeInTheDocument();
    expect(screen.getAllByText("not-a-date").length).toBeGreaterThan(0);
    expect(screen.getAllByText("未报告").length).toBeGreaterThan(0);
    expect(screen.getByText("cache")).toBeInTheDocument();
    expect(screen.getAllByText("未知").length).toBeGreaterThan(0);
  });

  it("renders the span tree with child nodes and a no-events state", async () => {
    const user = userEvent.setup();
    const view = renderInspector();
    await user.click(screen.getByRole("button", { name: "事件树" }));
    expect(
      screen.getByText(/确定性规则审查，span/),
    ).toBeInTheDocument();
    expect(screen.getByText(/，span span-2/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: /序号 1（#1），受理，执行已受理，已受理/,
      }),
    ).toBeInTheDocument();
    view.unmount();
    renderInspector({ execution: execution({ events: [] }) });
    expect(screen.getByText("本次执行尚无已保存的事件。")).toBeInTheDocument();
    expect(screen.getByText("没有可查看的事件详情。")).toBeInTheDocument();
  });

  it("lists located citations, pending citations and evaluation gaps", async () => {
    const user = userEvent.setup();
    const { onSelectFinding } = renderInspector();
    expect(
      screen.getAllByText("已定位线索").length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText("待定位线索").length).toBeGreaterThan(0);
    expect(screen.getByText(/引用未核对/)).toBeInTheDocument();
    expect(screen.getByText("引用尚未全部定位")).toBeInTheDocument();
    expect(screen.getByText("履约，缺少验收记录")).toBeInTheDocument();
    expect(screen.queryByText("商务，金额不一致")).not.toBeInTheDocument();
    await user.click(
      screen.getAllByRole("button", { name: "待定位线索" })[0]!,
    );
    expect(onSelectFinding).toHaveBeenCalledWith("f2");
  });

  it("says when no citations, gaps or located findings were reported", () => {
    renderInspector({
      findings: [],
      evaluations: [],
    });
    expect(screen.getByText("当前尝试没有全部引用已定位的线索。")).toBeInTheDocument();
    expect(
      screen.getByText("没有已报告的引用定位或规则缺口。"),
    ).toBeInTheDocument();
  });

  it("renders attempt summary dates, hashes, parse ids and usage details", () => {
    renderInspector();
    expect(screen.getAllByText("input-hash").length).toBeGreaterThan(0);
    expect(screen.getAllByText("output-hash").length).toBeGreaterThan(0);
    expect(screen.getAllByText("parsed-1").length).toBeGreaterThan(0);
    expect(screen.getAllByText("无已报告错误代码").length).toBeGreaterThan(0);
    expect(screen.getAllByText("输入 Token").length).toBeGreaterThan(0);
    expect(screen.getAllByText("1,500").length).toBeGreaterThan(0);
    expect(
      screen.getAllByText("未计算（没有版本化价格表）").length,
    ).toBeGreaterThan(0);
    expect(screen.getByText("未报告调用用量。")).toBeInTheDocument();
    expect(screen.queryByText("第一")).not.toBeInTheDocument();
    expect(screen.getAllByText("未知").length).toBeGreaterThan(0);

  });

  it("reports unavailable attempt timestamps and empty parse ids", () => {
    renderInspector({
      execution: execution({
        attempt: attempt("attempt-2", 2, "completed", {
          accepted_at: null,
          started_at: null,
          finished_at: null,
          input_hash: null,
          output_hash: null,
          parsed_document_ids: [],
          error_code: "failure-code",
        }),
      }),
    });
    expect(screen.getAllByText("未记录").length).toBeGreaterThan(0);
    expect(screen.getByText("未记录或尚未准备")).toBeInTheDocument();
    expect(screen.getByText("failure-code")).toBeInTheDocument();
  });

  it("offers cancellation, pending state and the cancellation error", async () => {
    const user = userEvent.setup();
    const { onCancel } = renderInspector({
      canCancel: true,
      cancelError: "取消失败",
      cancelling: false,
    });
    expect(screen.getByRole("alert")).toHaveTextContent("取消失败");
    await user.click(screen.getByRole("button", { name: "取消本次执行" }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("marks a pending cancellation without duplicate clicks", () => {
    renderInspector({ canCancel: true, cancelling: true });
    expect(screen.getByRole("button", { name: "正在取消" })).toBeDisabled();
  });

  it("embeds the agent review inspector for the selected attempt", () => {
    renderInspector();
    expect(
      screen.getByText(/本次执行没有保存五个方面的检查计划/),
    ).toBeInTheDocument();
  });
});
