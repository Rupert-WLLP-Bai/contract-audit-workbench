// File metadata (manifest mirror):
// Purpose: Checks execution diagram selection and unknown observations while mocking the browser drawing engine.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ExecutionEvent } from "../api/types";

const mocks = vi.hoisted(() => ({ setProperties: vi.fn() }));
vi.mock("@logicflow/core", () => ({
  default: class {
    register() {}
    setTheme() {}
    on() {}
    render() {}
    fitView() {}
    resize() {}
    destroy() {}
    setProperties = mocks.setProperties;
  },
  RectNode: class {},
  RectNodeModel: class {},
}));
import { ExecutionDiagram, diagramNodeStatus } from "./execution-diagram";
import { buildExecutionDiagram } from "../domain/execution-diagram";

const events: ExecutionEvent[] = Array.from({ length: 7 }, (_, index) => ({
  seq: index + 1,
  attempt_id: "a",
  span_id: `span-${index + 1}`,
  parent_span_id: null,
  node_kind: "Lambda",
  phase: "fact_extraction",
  event_type: "node_completed",
  at: null,
  duration_ms: 1,
  status: "completed",
  error_code: null,
  model_ref: null,
  usage: null,
}));

describe("execution diagram selection", () => {
  beforeEach(() => mocks.setProperties.mockClear());
  it("labels framework closure without claiming an agent or fact result", () => {
    const base = events[0]!;
    const records: ExecutionEvent[] = [
      {
        ...base,
        seq: 8,
        attempt_id: "a",
        span_id: "facts",
        phase: "single_contract_facts",
        status: "running",
        event_type: "span_started",
      },
      {
        ...base,
        seq: 9,
        attempt_id: "a",
        span_id: "facts",
        phase: "single_contract_facts",
        status: "completed",
      },
      {
        ...base,
        seq: 10,
        attempt_id: "a",
        span_id: "agent",
        phase: "agent_review",
        status: "running",
        event_type: "span_started",
      },
      {
        ...base,
        seq: 11,
        attempt_id: "a",
        span_id: null,
        node_kind: "agent_review",
        phase: "agent_review_result",
        status: "skipped",
        event_type: "agent_review_event",
        error_code: "agent_review_unavailable",
      },
      {
        ...base,
        seq: 12,
        attempt_id: "a",
        span_id: "agent",
        phase: "agent_review",
        status: "completed",
      },
      {
        ...base,
        seq: 13,
        attempt_id: "other",
        span_id: "other",
        phase: "agent_review_result",
        status: "completed",
      },
    ];
    const nodes = buildExecutionDiagram(records);
    const agent = nodes.find(
      (n) => n.event.attempt_id === "a" && n.event.phase === "agent_review",
    )!;
    const facts = nodes.find((n) => n.event.phase === "single_contract_facts")!;
    expect(agent.title).toBe("智能体审查框架步骤");
    expect(diagramNodeStatus(agent)).toBe("阶段已结束");
    expect(agent.events.map((e) => e.seq)).toEqual([10, 12]);
    expect(facts.title).toBe("合同事实处理阶段");
    expect(diagramNodeStatus(facts)).toBe("阶段已结束");
    expect(buildExecutionDiagram([])).toEqual([]);
    const ordinary = buildExecutionDiagram([
      { ...base, phase: "input_preparation", status: "completed" },
    ])[0]!;
    expect(diagramNodeStatus(ordinary)).toBe("已完成");
    render(<ExecutionDiagram events={records} selectedSeq={null} onSelectSeq={vi.fn()} />);
    fireEvent.click(screen.getByText("按顺序选择节点（键盘可用）"));
    expect(screen.getByRole("button", { name: /智能体审查框架步骤.*阶段已结束/ })).toBeVisible();
  });

  it("opens the selected segment, allows manual paging, and restores highlight after rebuilding", () => {
    const onSelectSeq = vi.fn();
    const view = render(
      <ExecutionDiagram events={events} selectedSeq={6} onSelectSeq={onSelectSeq} />,
    );
    expect(screen.getByText(/第 2 \/ 2 段/)).toBeInTheDocument();
    expect(mocks.setProperties).toHaveBeenCalledWith(JSON.stringify(["a", "span", "span-6"]), {
      selected: true,
    });
    fireEvent.click(screen.getByRole("button", { name: "上一段" }));
    expect(screen.getByText(/第 1 \/ 2 段/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "下一段" }));
    expect(screen.getByText(/第 2 \/ 2 段/)).toBeInTheDocument();
    expect(mocks.setProperties).toHaveBeenLastCalledWith(JSON.stringify(["a", "span", "span-7"]), {
      selected: false,
    });
    expect(mocks.setProperties).toHaveBeenCalledWith(JSON.stringify(["a", "span", "span-6"]), {
      selected: true,
    });
    view.rerender(<ExecutionDiagram events={events} selectedSeq={2} onSelectSeq={onSelectSeq} />);
    expect(screen.getByText(/第 1 \/ 2 段/)).toBeInTheDocument();
  });
});
