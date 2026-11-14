// File metadata (manifest mirror):
// Purpose: Projects immutable execution observations into attempt-bound diagram nodes without inventing runtime events.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { ExecutionEvent } from "../api/types";
import { phaseLabel } from "./execution-labels";

export interface ExecutionDiagramNode {
  id: string;
  events: ExecutionEvent[];
  event: ExecutionEvent;
  kind: "model" | "tool" | "stage";
  title: string;
  parentId: string | null;
}

/** Project saved events by attempt and span. Events without a span stay separate. */
export function buildExecutionDiagram(events: ExecutionEvent[]): ExecutionDiagramNode[] {
  const groups = new Map<string, ExecutionEvent[]>();
  for (const event of [...events].sort((a, b) => a.seq - b.seq)) {
    const id = JSON.stringify([
      event.attempt_id,
      event.span_id === null ? "event" : "span",
      event.span_id ?? event.seq,
    ]);
    const group = groups.get(id);
    if (group) group.push(event);
    else groups.set(id, [event]);
  }
  const all = [...groups.entries()].map(([id, records]) => {
    const ordered = [...records].reverse();
    // Prefer the record containing actual usage/trace, then a terminal record.
    const event =
      ordered.find((item) => item.usage || item.trace) ??
      ordered.find((item) => item.duration_ms !== null) ??
      ordered[0]!;
    const kind: ExecutionDiagramNode["kind"] = records.some(
      (item) => item.node_kind === "model_call",
    )
      ? "model"
      : records.some((item) => item.node_kind === "tool_call")
        ? "tool"
        : "stage";
    const name = ordered.find((item) => item.trace?.reported_model || item.model_ref);
    const title =
      kind === "model"
        ? name?.trace?.reported_model || name?.model_ref || "模型调用"
        : kind === "tool"
          ? name?.model_ref || "工具调用"
          : phaseLabel(event.phase || "unknown");
    return { id, events: records, event, kind, title, parentId: null as string | null };
  });
  const byId = new Map(all.map((node) => [node.id, node]));
  const callParents = new Set(
    all
      .filter((node) => node.kind !== "stage")
      .map((node) => {
        const parent = node.events.find((event) => event.parent_span_id)?.parent_span_id;
        return parent ? JSON.stringify([node.event.attempt_id, "span", parent]) : "";
      }),
  );
  const visible = all.filter((node) => {
    if (node.events.every((event) => ["chain", "attempt"].includes(event.node_kind.toLowerCase())))
      return false;
    // Unscoped ledger events are available in the event list, not invented calls.
    if (node.kind === "stage" && node.events.every((event) => event.span_id === null)) return false;
    // A Lambda that immediately wraps a real model/tool call adds no distinct call.
    if (
      callParents.has(node.id) &&
      node.events.every((event) => event.node_kind.toLowerCase() === "lambda") &&
      /model|tool/.test(node.event.phase.toLowerCase())
    )
      return false;
    return true;
  });
  const visibleIds = new Set(visible.map((node) => node.id));
  for (const node of visible) {
    let parent = node.events.find((event) => event.parent_span_id)?.parent_span_id ?? null;
    const visited = new Set([node.id]);
    while (parent) {
      const parentId = JSON.stringify([node.event.attempt_id, "span", parent]);
      if (visited.has(parentId)) break;
      visited.add(parentId);
      const ancestor = byId.get(parentId);
      if (!ancestor) break;
      if (visibleIds.has(parentId)) {
        node.parentId = parentId;
        break;
      }
      parent = ancestor.events.find((event) => event.parent_span_id)?.parent_span_id ?? null;
    }
  }
  return visible;
}

/** The frozen runtime and event kind jointly identify the v4 Eino invocation. */
export function isEinoToolEvent(
  event: ExecutionEvent,
  runtimeVersion: string | null | undefined,
): boolean {
  return (
    event.node_kind === "tool_call" &&
    event.event_type === "agent_review_event" &&
    /^operator-flow-v4(?:\/|$)/.test(runtimeVersion ?? "")
  );
}
