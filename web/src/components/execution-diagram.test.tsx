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
import { ExecutionDiagram } from "./execution-diagram";

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
