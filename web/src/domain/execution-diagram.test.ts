// File metadata (manifest mirror):
// Purpose: Checks attempt and span grouping of recorded execution events and preserves unknown parent and status information.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, it } from "vitest";
import type { ExecutionEvent } from "../api/types";
import { buildExecutionDiagram, isEinoToolEvent } from "./execution-diagram";

function event(seq: number, span: string | null, overrides: Partial<ExecutionEvent> = {}): ExecutionEvent {
  return { seq, attempt_id: "a", span_id: span, parent_span_id: null, node_kind: "model_call", phase: "model_review", event_type: "span_started", at: null, duration_ms: null, status: "running", error_code: null, model_ref: "same-model", usage: null, ...overrides };
}

describe("execution diagram projection", () => {
  it("merges only the same attempt and span, retaining original event sequence and reported usage", () => {
    const result = buildExecutionDiagram([
      event(3, "one", { event_type: "node_completed", duration_ms: 25, usage: { input_tokens: 7, output_tokens: 3, total_tokens: 10, source: "provider" } }),
      event(1, "one"), event(2, "two"), event(4, "one", { attempt_id: "b" }), event(5, null), event(6, null),
    ]);
    expect(result).toHaveLength(5);
    expect(result[0]?.events.map((item) => item.seq)).toEqual([1, 3]);
    expect(result[0]?.event.usage?.total_tokens).toBe(10);
    expect(result[1]?.event.usage).toBeNull();
  });

  it("omits framework and call wrappers while keeping the underlying calls", () => {
    const result = buildExecutionDiagram([
      event(1, "root", { node_kind: "attempt" }),
      event(2, "chain", { node_kind: "Chain", parent_span_id: "root" }),
      event(3, "stage", { node_kind: "Lambda", phase: "tool_evidence", parent_span_id: "chain" }),
      event(4, "tool", { node_kind: "tool_call", parent_span_id: "stage", model_ref: "evidence.lookup" }),
      event(5, "tool", { node_kind: "tool_call", parent_span_id: "stage", model_ref: "evidence.lookup", event_type: "finding_verified" }),
    ]);
    expect(result.map((node) => node.kind)).toEqual(["tool"]);
    expect(result[0]?.title).toBe("evidence.lookup");
    expect(result[0]?.parentId).toBeNull();
    expect(result[0]?.events.map((item) => item.seq)).toEqual([4, 5]);
  });

  it("keeps substantive parents and excludes unscoped ledger events from the diagram", () => {
    const result = buildExecutionDiagram([
      event(1, "stage", { node_kind: "Lambda", phase: "persistence" }),
      event(2, "tool", { node_kind: "tool_call", parent_span_id: "stage" }),
      event(3, null, { node_kind: "agent_review", event_type: "agent_review_event" }),
    ]);
    expect(result).toHaveLength(2);
    expect(result[1]?.parentId).toBe(result[0]?.id);
  });
  it("labels Eino only for v4 tool events with a matching recorded runtime", () => {
    const tool = event(1, "call", { node_kind: "tool_call", event_type: "agent_review_event" });
    expect(isEinoToolEvent(tool, "operator-flow-v4/eino-0.9.22")).toBe(true);
    expect(isEinoToolEvent(tool, "operator-flow-v3/eino-0.9.22")).toBe(false);
    expect(isEinoToolEvent(tool, null)).toBe(false);
    expect(isEinoToolEvent({ ...tool, event_type: "tool_evidence" }, "operator-flow-v4/eino-0.9.22")).toBe(false);
    expect(isEinoToolEvent({ ...tool, node_kind: "agent_review" }, "operator-flow-v4/eino-0.9.22")).toBe(false);
  });

});
