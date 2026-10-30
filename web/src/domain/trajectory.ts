import type {
  AgentReview,
  AgentReviewEvent,
  AgentReviewKnowledgeRead,
  AgentReviewObservation,
  Execution,
  ExecutionEvent,
  ModelCallTrace,
} from "../api/types";
import {
  eventStatusLabel,
  eventTypeLabel,
  phaseLabel,
  tokenCount,
} from "./execution-labels";

export type TrajectoryKind = "stage" | "model" | "tool" | "knowledge" | "event";

export interface TrajectoryField {
  label: string;
  value: string;
}

export interface TrajectorySection {
  label: string;
  text: string;
  code?: boolean;
}

export interface TrajectoryEntry {
  id: string;
  kind: TrajectoryKind;
  at: string | null;
  order: number;
  callID: string | null;
  durationMs: number | null;
  status: string;
  title: string;
  meta: string[];
  fields: TrajectoryField[];
  sections: TrajectorySection[];
  search: string;
}

export interface TrajectorySummary {
  entries: TrajectoryEntry[];
  modelCalls: number;
  toolCalls: number;
  knowledgeReads: number;
  stages: number;
  durationMs: number | null;
  inputTokens: number | null;
  outputTokens: number | null;
}

const AGENT_EVENT_LABELS: Record<string, string> = {
  planning: "规划请求",
  plan: "计划",
  plan_accepted: "计划已受理",
  plan_rejected: "计划被拒绝",
  planner_fallback: "规划回退",
  guard: "计划守卫",
  authorization: "授权检查",
  input: "输入观察",
  gap_unsupported: "能力未支持",
  evidence: "证据核验",
  budget: "预算终止",
  terminal: "运行终止",
  run_cancelled: "运行已取消",
  tool_call: "工具调用",
  tool_started: "工具调用开始",
  tool_observed: "工具返回",
  tool_completed: "工具调用完成",
  tool_failed: "工具调用失败",
  knowledge_read_started: "知识读取开始",
  knowledge_read_completed: "知识读取完成",
  knowledge_read_failed: "知识读取失败",
};

const FAILED_KINDS = new Set([
  "plan_rejected",
  "tool_failed",
  "knowledge_read_failed",
  "gap_unsupported",
  "run_cancelled",
]);
const RUNNING_KINDS = new Set(["tool_started", "knowledge_read_started", "planning"]);

export function agentEventLabel(kind: string): string {
  return AGENT_EVENT_LABELS[kind] ?? kind;
}

export function buildTrajectory(execution: Execution): TrajectorySummary {
  const entries = [
    ...spanEntries(execution.events),
    ...looseEntries(execution.events),
    ...agentReviewEntries(execution.agent_review),
  ].sort(compareEntries);
  return {
    entries,
    stages: entries.filter((entry) => entry.kind === "stage").length,
    modelCalls: entries.filter((entry) => entry.kind === "model").length,
    toolCalls: distinctCalls(entries, "tool"),
    knowledgeReads: distinctCalls(entries, "knowledge"),
    durationMs: execution.duration_ms,
    inputTokens: execution.usage?.input_tokens ?? null,
    outputTokens: execution.usage?.output_tokens ?? null,
  };
}

/** One tool call emits start, observed and completed records; count calls, not records. */
function distinctCalls(entries: TrajectoryEntry[], kind: TrajectoryKind): number {
  return new Set(
    entries
      .filter((entry) => entry.kind === kind)
      .map((entry) => entry.callID ?? entry.id),
  ).size;
}

function compareEntries(left: TrajectoryEntry, right: TrajectoryEntry): number {
  const leftTime = left.at === null ? null : Date.parse(left.at);
  const rightTime = right.at === null ? null : Date.parse(right.at);
  if (leftTime !== null && rightTime !== null && leftTime !== rightTime) {
    return leftTime - rightTime;
  }
  if (leftTime !== null && rightTime === null) return -1;
  if (leftTime === null && rightTime !== null) return 1;
  return left.order - right.order;
}

interface SpanGroup {
  spanId: string;
  start: ExecutionEvent | null;
  end: ExecutionEvent | null;
  inner: ExecutionEvent[];
}

function spanEntries(events: ExecutionEvent[]): TrajectoryEntry[] {
  const groups = new Map<string, SpanGroup>();
  for (const event of events) {
    if (event.span_id === null || event.node_kind === "agent_review") continue;
    const group = groups.get(event.span_id) ?? {
      spanId: event.span_id,
      start: null,
      end: null,
      inner: [],
    };
    if (event.event_type === "span_started") group.start = event;
    else if (isSpanEnd(event.event_type)) group.end = event;
    else group.inner.push(event);
    groups.set(event.span_id, group);
  }
  return [...groups.values()]
    .map((group, index) => spanEntry(group, index))
    .filter((entry): entry is TrajectoryEntry => entry !== null);
}

/**
 * Spanless records are the deterministic checks and attempt lifecycle events
 * that never opened an Eino span. Agent-review events are projected from the
 * agent review itself, so they are excluded here to avoid duplicates.
 */
function looseEntries(events: ExecutionEvent[]): TrajectoryEntry[] {
  return events
    .filter((event) => event.span_id === null && event.node_kind !== "agent_review")
    .map((event, index) => looseEntry(event, index));
}

function looseEntry(event: ExecutionEvent, index: number): TrajectoryEntry {
  const fields: TrajectoryField[] = [
    { label: "阶段", value: phaseLabel(event.phase) },
    { label: "节点", value: event.node_kind },
    { label: "状态", value: eventStatusLabel(event.status ?? "unknown") },
  ];
  if (event.error_code) fields.push({ label: "错误码", value: event.error_code });
  if (event.model_ref) fields.push({ label: "对象", value: event.model_ref });
  if (event.duration_ms !== null) fields.push({ label: "耗时", value: `${event.duration_ms} 毫秒` });
  return makeEntry({
    id: `event:${event.seq}`,
    kind: "event",
    at: event.at,
    order: 500 + index,
    durationMs: event.duration_ms,
    status: event.status ?? "unknown",
    title: `${phaseLabel(event.phase)} · ${eventTypeLabel(event.event_type)}`,
    meta: [event.node_kind, event.model_ref ?? ""].filter(Boolean),
    fields,
    sections: [],
  });
}

function isSpanEnd(eventType: string): boolean {
  return (
    eventType === "span_completed" ||
    eventType === "span_failed" ||
    eventType === "node_completed" ||
    eventType === "node_failed" ||
    eventType === "model_completed" ||
    eventType === "model_failed"
  );
}

function spanEntry(group: SpanGroup, index: number): TrajectoryEntry | null {
  const first = group.start ?? group.end ?? group.inner[0];
  if (first === undefined) return null;
  const last = group.end ?? group.inner.at(-1) ?? group.start ?? first;
  return first.node_kind === "model_call"
    ? modelEntry(group, first, last, index)
    : stageEntry(group, first, last, index);
}

function stageEntry(
  group: SpanGroup,
  first: ExecutionEvent,
  last: ExecutionEvent,
  index: number,
): TrajectoryEntry {
  const fields: TrajectoryField[] = [
    { label: "阶段", value: phaseLabel(first.phase) },
    { label: "节点", value: first.node_kind },
    { label: "状态", value: eventStatusLabel(last.status ?? first.status ?? "running") },
  ];
  if (last.error_code) fields.push({ label: "错误码", value: last.error_code });
  const sections = group.inner.map((event) => ({
    label: `${phaseLabel(event.phase)} · ${eventTypeLabel(event.event_type)}`,
    text: [
      `状态：${eventStatusLabel(event.status ?? "unknown")}`,
      event.error_code ? `错误码：${event.error_code}` : "",
      event.duration_ms !== null ? `耗时：${event.duration_ms} 毫秒` : "",
    ]
      .filter(Boolean)
      .join("；"),
  }));
  return makeEntry({
    id: `span:${group.spanId}`,
    kind: "stage",
    at: first.at,
    order: index,
    durationMs: last.duration_ms,
    status: last.status ?? first.status ?? "running",
    title: phaseLabel(first.phase),
    meta: [first.node_kind],
    fields,
    sections,
  });
}

function modelEntry(
  group: SpanGroup,
  first: ExecutionEvent,
  last: ExecutionEvent,
  index: number,
): TrajectoryEntry {
  const trace = last.trace ?? first.trace ?? null;
  const usage = last.usage ?? first.usage;
  const model = trace?.reported_model || trace?.requested_model || last.model_ref || first.model_ref || "模型";
  const fields = modelFields(trace, last, usage);
  const sections = traceSections(trace);
  return makeEntry({
    id: `span:${group.spanId}`,
    kind: "model",
    at: first.at,
    order: index,
    durationMs: last.duration_ms,
    status: last.status ?? first.status ?? "running",
    title: `模型调用 · ${model}`,
    meta: [
      model,
      usage ? `输入 ${tokenCount(usage.input_tokens)} / 输出 ${tokenCount(usage.output_tokens)} Token` : "Token 未报告",
    ],
    fields,
    sections,
  });
}

function modelFields(
  trace: ModelCallTrace | null,
  event: ExecutionEvent,
  usage: ExecutionEvent["usage"],
): TrajectoryField[] {
  const fields: TrajectoryField[] = [];
  if (trace) {
    fields.push(
      { label: "请求模型", value: trace.requested_model || "未记录" },
      { label: "返回模型", value: trace.reported_model || event.model_ref || "未记录" },
      { label: "状态", value: eventStatusLabel(trace.status || event.status || "unknown") },
    );
    if (trace.error_code) fields.push({ label: "错误码", value: trace.error_code });
    fields.push(
      { label: "请求字节", value: String(trace.request_bytes) },
      { label: "请求摘要", value: trace.request_sha256 || "未记录" },
      { label: "响应字节", value: String(trace.response_bytes) },
      { label: "响应摘要", value: trace.response_sha256 || "未记录" },
      { label: "开始", value: trace.started_at || "未记录" },
      { label: "结束", value: trace.finished_at || "未记录" },
    );
  } else {
    fields.push({ label: "状态", value: eventStatusLabel(event.status ?? "unknown") });
    if (event.error_code) fields.push({ label: "错误码", value: event.error_code });
  }
  if (usage) {
    fields.push(
      { label: "输入 Token", value: tokenCount(usage.input_tokens) },
      { label: "输出 Token", value: tokenCount(usage.output_tokens) },
      { label: "总 Token", value: tokenCount(usage.total_tokens) },
      { label: "用量来源", value: usage.source || "未记录" },
    );
  }
  return fields;
}

function traceSections(trace: ModelCallTrace | null): TrajectorySection[] {
  if (!trace) {
    return [
      {
        label: "请求与输出",
        text: "本次调用没有保存请求或输出内容；只能看到模型标识、状态和用量。",
      },
    ];
  }
  const sections: TrajectorySection[] = trace.request_messages.map((message) => ({
    label: `请求 · ${message.role}${message.truncated ? "（已截断）" : ""}`,
    text: message.content || "内容为空",
    code: message.role === "system" || message.role === "user",
  }));
  if (trace.response_text) {
    sections.push({
      label: `模型输出${trace.response_truncated ? "（已截断）" : ""}`,
      text: trace.response_text,
      code: true,
    });
  } else {
    sections.push({ label: "模型输出", text: "模型没有返回可记录的文本。" });
  }
  return sections;
}

function agentReviewEntries(review: AgentReview | null): TrajectoryEntry[] {
  if (review === null || review.events.length === 0) return [];
  const observations = new Map<string, AgentReviewObservation>();
  for (const observation of review.observations ?? []) {
    observations.set(observation.call_id, observation);
  }
  const calls = new Map(review.plan.calls.map((call) => [call.call_id, call]));
  const reads = review.knowledge_reads ?? [];
  let readIndex = 0;
  return review.events.map((event, index) => {
    const kind = agentKind(event.kind);
    const read = event.kind === "knowledge_read_completed" ? (reads[readIndex++] ?? null) : null;
    const observation =
      event.kind === "tool_observed" || event.kind === "tool_failed"
        ? observations.get(event.call_id ?? "")
        : undefined;
    return agentEntry(event, index, kind, observation, calls, read);
  });
}

function agentKind(kind: string): TrajectoryKind {
  if (kind.startsWith("tool_") || kind === "tool_call") return "tool";
  if (kind.startsWith("knowledge_read_")) return "knowledge";
  return "event";
}

function agentEntry(
  event: AgentReviewEvent,
  index: number,
  kind: TrajectoryKind,
  observation: AgentReviewObservation | undefined,
  calls: Map<string, AgentReview["plan"]["calls"][number]>,
  read: AgentReviewKnowledgeRead | null,
): TrajectoryEntry {
  const call = event.call_id === null ? undefined : calls.get(event.call_id);
  const title = agentTitle(event, kind, call?.tool_id);
  const fields: TrajectoryField[] = [
    { label: "轮次", value: String(event.round) },
    { label: "事件", value: event.kind },
  ];
  if (event.tool_id ?? call?.tool_id) fields.push({ label: "工具", value: event.tool_id ?? call?.tool_id ?? "" });
  if (event.gap_id ?? call?.gap_id) fields.push({ label: "能力缺口", value: event.gap_id ?? call?.gap_id ?? "" });
  if (event.call_id) fields.push({ label: "调用", value: event.call_id });
  if (event.domain) fields.push({ label: "域", value: event.domain });
  fields.push({ label: "代码", value: event.code });
  if (event.duration_ms > 0) fields.push({ label: "耗时", value: `${event.duration_ms} 毫秒` });
  if (call) {
    fields.push(
      { label: "工具版本", value: call.tool_version },
      { label: "能力", value: call.capability_id },
      { label: "计划状态", value: call.status },
    );
    if (call.reason_code) fields.push({ label: "计划原因", value: call.reason_code });
  }
  if (observation) {
    fields.push(
      { label: "报告关系", value: observation.reported_relation },
      { label: "声明状态", value: observation.claim_status },
    );
    if (observation.reason_code) fields.push({ label: "观察原因", value: observation.reason_code });
    if (observation.fact_ids.length > 0) fields.push({ label: "Fact ID", value: observation.fact_ids.join("、") });
    if (observation.located_evidence_ids.length > 0) {
      fields.push({ label: "证据 ID", value: observation.located_evidence_ids.join("、") });
    }
    if (observation.truncated) fields.push({ label: "观察", value: "已截断" });
  }
  if (read) {
    fields.push(
      { label: "知识", value: read.knowledge_id },
      { label: "版本", value: read.version_id },
      { label: "区间", value: `${read.start}–${read.end}` },
      { label: "文本摘要", value: read.text_sha256 },
    );
  }
  const sections: TrajectorySection[] = [];
  if (observation) sections.push({ label: "工具返回", text: observation.summary });
  if (read?.text) sections.push({ label: "读取的知识文本", text: read.text });
  return makeEntry({
    id: `agent:${event.event_id ?? event.sequence}`,
    kind,
    at: event.occurred_at ?? null,
    order: 1000 + index,
    callID: event.call_id ?? call?.call_id ?? null,
    durationMs: event.duration_ms,
    status: agentStatus(event.kind),
    title,
    meta: [
      event.call_id ? `调用 ${event.call_id}` : "",
      event.tool_id ?? call?.tool_id ?? "",
      event.gap_id ?? call?.gap_id ?? "",
    ].filter(Boolean),
    fields,
    sections,
  });
}

function agentTitle(
  event: AgentReviewEvent,
  kind: TrajectoryKind,
  toolID: string | undefined,
): string {
  const label = agentEventLabel(event.kind);
  if (kind === "tool") return toolID ? `${label} · ${toolID}` : label;
  if (kind === "knowledge") return label;
  if (event.kind === "planning" || event.kind === "plan" || event.kind === "plan_accepted") {
    return label;
  }
  return label;
}

function agentStatus(kind: string): string {
  if (FAILED_KINDS.has(kind)) return "failed";
  if (RUNNING_KINDS.has(kind)) return "running";
  return "completed";
}

function makeEntry(input: {
  id: string;
  kind: TrajectoryKind;
  at: string | null;
  order: number;
  callID?: string | null;
  durationMs: number | null;
  status: string;
  title: string;
  meta: string[];
  fields: TrajectoryField[];
  sections: TrajectorySection[];
}): TrajectoryEntry {
  const search = [
    input.title,
    ...input.meta,
    ...input.fields.flatMap((field) => [field.label, field.value]),
    ...input.sections.flatMap((section) => [section.label, section.text]),
  ]
    .join("\n")
    .toLocaleLowerCase("zh-CN");
  return { ...input, callID: input.callID ?? null, search };
}
