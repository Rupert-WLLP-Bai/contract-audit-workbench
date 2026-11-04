// File metadata (manifest mirror):
// Purpose: Projects saved execution events into swimlanes (phase lanes on one axis, chronological steps on the other) without inventing steps, skills or token values.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { Execution, ExecutionEvent } from "../api/types";
import { phaseLabel } from "./execution-labels";

export type SwimlaneKind = "model" | "tool" | "knowledge" | "stage" | "event";

export interface SwimlaneStep {
  id: string;
  seq: number;
  lane: string;
  kind: SwimlaneKind;
  title: string;
  tool: string | null;
  model: string | null;
  status: string;
  at: string | null;
  durationMs: number | null;
  spanId: string | null;
  parentSpanId: string | null;
  inputTokens: number | null;
  outputTokens: number | null;
  totalTokens: number | null;
  usageSource: string | null;
  usageKnown: boolean;
  errorCode: string | null;
}

export interface SwimlaneModel {
  lanes: { id: string; label: string; steps: SwimlaneStep[] }[];
  steps: SwimlaneStep[];
  skillEvents: number;
  reportedUsageSteps: number;
}

const NODE_KIND_LABELS: Record<string, string> = {
  model_call: "模型调用",
  tool_call: "工具调用",
  Chain: "链式节点",
  Lambda: "Lambda 节点",
  attempt: "尝试",
  agent_review: "智能体审查",
};

export function nodeKindLabel(kind: string): string {
  return NODE_KIND_LABELS[kind] ?? kind;
}

function stepKind(event: ExecutionEvent): SwimlaneKind {
  switch (event.node_kind) {
    case "model_call":
      return "model";
    case "tool_call":
      return "tool";
    case "agent_review":
      return event.event_type === "tool_evidence" ? "tool" : "knowledge";
    default:
      return event.event_type.startsWith("span_") || event.event_type.startsWith("node_")
        ? "stage"
        : "event";
  }
}

/** Only terminal/completed records carry the model's reported usage; equal
 *  start/end events for one span would otherwise double-count tokens. The
 *  view uses `usageKnown` rather than inferring tokens from event type. */
export function usageIsReported(event: ExecutionEvent): boolean {
  return event.usage !== null;
}

function stepTitle(event: ExecutionEvent, kind: SwimlaneKind): string {
  const label = nodeKindLabel(event.node_kind);
  if (kind === "model") return event.model_ref ? `模型 · ${event.model_ref}` : label;
  if (kind === "tool") return event.model_ref ? `工具 · ${event.model_ref}` : label;
  return label;
}

/**
 * Build lanes from the frozen `execution.events` array. One step is one saved
 * event (terminal records carry token usage); no synthetic skill steps are
 * created because the execution contract has no skill event.
 */
export function buildSwimlanes(execution: Execution): SwimlaneModel {
  const events = [...execution.events].sort((a, b) => a.seq - b.seq);
  const orderedLanes: string[] = [];
  const byLane = new Map<string, SwimlaneStep[]>();
  const steps: SwimlaneStep[] = [];
  let skillEvents = 0;
  let reportedUsageSteps = 0;

  for (const event of events) {
    const lane = event.phase || "unknown";
    if (!byLane.has(lane)) {
      byLane.set(lane, []);
      orderedLanes.push(lane);
    }
    const kind = stepKind(event);
    const usage = event.usage;
    if (usage) reportedUsageSteps += 1;
    if (/skill/i.test(event.event_type) || /skill/i.test(event.node_kind)) skillEvents += 1;
    const step: SwimlaneStep = {
      id: `event-${event.seq}`,
      seq: event.seq,
      lane,
      kind,
      title: stepTitle(event, kind),
      tool: kind === "tool" ? (event.model_ref ?? nodeKindLabel(event.node_kind)) : null,
      model: kind === "model" ? (event.trace?.reported_model || event.model_ref) ?? null : null,
      status: event.status ?? "unknown",
      at: event.at,
      durationMs: event.duration_ms,
      spanId: event.span_id,
      parentSpanId: event.parent_span_id,
      inputTokens: usage?.input_tokens ?? null,
      outputTokens: usage?.output_tokens ?? null,
      totalTokens: usage?.total_tokens ?? null,
      usageSource: usage?.source ?? null,
      usageKnown: usage !== null,
      errorCode: event.error_code,
    };
    byLane.get(lane)?.push(step);
    steps.push(step);
  }

  return {
    lanes: orderedLanes.map((id) => ({
      id,
      label: phaseLabel(id),
      steps: byLane.get(id) ?? [],
    })),
    steps,
    skillEvents,
    reportedUsageSteps,
  };
}
