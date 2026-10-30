import { describe, expect, it } from "vitest";
import { parseAgentReview } from "../api/agent-review";
import type {
  Execution,
  ExecutionAttempt,
  ExecutionEvent,
  ModelCallTrace,
} from "../api/types";
import { agentEventLabel, buildTrajectory } from "./trajectory";

const attempt: ExecutionAttempt = {
  id: "attempt-1",
  run_id: "run-1",
  ordinal: 1,
  retry_of_attempt_id: null,
  state: "completed",
  accepted_at: "2026-10-30T00:00:00.000Z",
  started_at: "2026-10-30T00:00:00.000Z",
  finished_at: "2026-10-30T00:00:05.000Z",
  parsed_document_ids: ["parsed-1"],
  input_hash: "input-hash",
  output_hash: "output-hash",
  error_code: null,
  history_kind: "frozen",
};

const trace: ModelCallTrace = {
  requested_model: "codex/gpt-6-luna",
  reported_model: "codex/gpt-6-luna",
  status: "completed",
  error_code: "",
  started_at: "2026-10-30T00:00:02.000Z",
  finished_at: "2026-10-30T00:00:02.900Z",
  request_sha256: "request-digest",
  request_bytes: 120,
  request_messages: [
    { role: "system", content: "审查规则", truncated: false },
    { role: "user", content: "采购合同原文", truncated: false },
  ],
  request_truncated: false,
  response_sha256: "response-digest",
  response_bytes: 40,
  response_text: "模型给出的审查建议",
  response_truncated: false,
};

const review = parseAgentReview({
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
    limits: {
      max_rounds: 2,
      max_calls: 5,
      max_duration_ms: 1000,
      max_evidence_bytes: 4096,
    },
  },
  scope: [],
  domains: [],
  gaps: [],
  plan: {
    status: "completed",
    rejection_code: null,
    calls: [
      {
        call_id: "call-1",
        tool_id: "evidence.lookup",
        tool_version: "tool-v1",
        domain: "commercial",
        capability_id: "amount",
        gap_id: "gap-1",
        status: "completed",
        reason_code: "verified_result",
        evidence_ids: ["e1"],
      },
    ],
    rounds: [],
  },
  evidence: [],
  usage: {
    planner_calls: 1,
    input_tokens: 100,
    output_tokens: 20,
    total_tokens: 120,
  },
  unresolved_gap_ids: [],
  events: [
    {
      event_id: "plan-1",
      sequence: 1,
      round: 1,
      kind: "plan",
      tool_id: "evidence.lookup",
      gap_id: "gap-1",
      call_id: "call-1",
      domain: "commercial",
      code: "plan_accepted",
      occurred_at: "2026-10-30T00:00:02.500Z",
      duration_ms: 0,
    },
    {
      event_id: "tool-start",
      sequence: 2,
      round: 1,
      kind: "tool_started",
      tool_id: "evidence.lookup",
      gap_id: "gap-1",
      call_id: "call-1",
      domain: "commercial",
      code: "read_action_started",
      occurred_at: "2026-10-30T00:00:03.100Z",
      duration_ms: 0,
    },
    {
      event_id: "tool-observed",
      sequence: 3,
      round: 1,
      kind: "tool_observed",
      tool_id: "evidence.lookup",
      gap_id: "gap-1",
      call_id: "call-1",
      domain: "commercial",
      code: "validated_tool_observation",
      occurred_at: "2026-10-30T00:00:03.200Z",
      duration_ms: 100,
    },
    {
      event_id: "tool-completed",
      sequence: 4,
      round: 1,
      kind: "tool_completed",
      tool_id: "evidence.lookup",
      gap_id: "gap-1",
      call_id: "call-1",
      domain: "commercial",
      code: "validated_tool_result",
      occurred_at: "2026-10-30T00:00:03.300Z",
      duration_ms: 200,
    },
    {
      event_id: "tool-failed",
      sequence: 5,
      round: 1,
      kind: "tool_failed",
      tool_id: "evidence.lookup",
      gap_id: "gap-2",
      call_id: "call-2",
      domain: "commercial",
      code: "tool_rejected",
      occurred_at: "2026-10-30T00:00:03.500Z",
      duration_ms: 10,
    },
    {
      event_id: "knowledge-1",
      sequence: 6,
      round: 1,
      kind: "knowledge_read_completed",
      tool_id: "knowledge.read",
      call_id: "read-1",
      domain: null,
      code: "frozen_knowledge_span_read",
      occurred_at: "2026-10-30T00:00:03.400Z",
      duration_ms: 12,
    },
  ],
  observations: [
    {
      round: 1,
      call_id: "call-1",
      gap_id: "gap-1",
      tool_id: "evidence.lookup",
      reported_relation: "conflict",
      reason_code: "verified_result",
      summary: "发现金额冲突",
      claim_status: "tool_reported_not_independently_verified",
      fact_ids: ["fact-1"],
      located_evidence_ids: ["e1"],
      truncated: false,
    },
  ],
  knowledge_reads: [
    {
      knowledge_id: "knowledge-1",
      version_id: "version-1",
      content_sha256: "content-digest",
      start: 0,
      end: 4,
      text_sha256: "text-digest",
      text: "知识文本片段",
    },
  ],
});

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

function execution(overrides: Partial<Execution>): Execution {
  return {
    schema_version: "audit-execution/v1",
    attempt,
    attempts: [attempt],
    events: [
      event({ seq: 1, span_id: "span-stage", event_type: "span_started" }),
      event({
        seq: 2,
        span_id: "span-stage",
        event_type: "node_completed",
        at: "2026-10-30T00:00:00.900Z",
        duration_ms: 900,
        status: "completed",
      }),
      event({
        seq: 3,
        span_id: "span-model",
        node_kind: "model_call",
        phase: "model_request",
        event_type: "span_started",
        at: "2026-10-30T00:00:02.000Z",
        model_ref: "codex/gpt-6-luna",
      }),
      event({
        seq: 4,
        span_id: "span-model",
        node_kind: "model_call",
        phase: "model_request",
        event_type: "model_completed",
        at: "2026-10-30T00:00:02.900Z",
        duration_ms: 900,
        status: "completed",
        model_ref: "codex/gpt-6-luna",
        usage: {
          source: "provider_response.usage",
          input_tokens: 100,
          output_tokens: 20,
          total_tokens: 120,
        },
        trace,
      }),
    ],
    agent_review: review,
    usage: {
      input_tokens: 100,
      output_tokens: 20,
      total_tokens: 120,
      calls: 1,
      unknown_calls: 0,
      sources: ["provider_response.usage"],
      known_subtotals: { input_tokens: 100, output_tokens: 20, total_tokens: 120 },
      cost: null,
    },
    all_attempts_usage: null,
    duration_ms: 5000,
    first_verified_finding_ms: null,
    queue_wait_ms: null,
    ...overrides,
  };
}

describe("buildTrajectory", () => {
  it("merges stages, model traces, tool returns and knowledge reads in time order", () => {
    const trajectory = buildTrajectory(execution({}));
    expect(trajectory).toMatchObject({
      stages: 1,
      modelCalls: 1,
      toolCalls: 2,
      knowledgeReads: 1,
      durationMs: 5000,
      inputTokens: 100,
      outputTokens: 20,
    });
    expect(trajectory.entries.map((entry) => entry.kind)).toEqual([
      "stage",
      "model",
      "event",
      "tool",
      "tool",
      "tool",
      "knowledge",
      "tool",
    ]);

    const model = trajectory.entries.find((entry) => entry.kind === "model");
    expect(model?.title).toBe("模型调用 · codex/gpt-6-luna");
    expect(model?.fields).toContainEqual({ label: "请求摘要", value: "request-digest" });
    expect(model?.sections).toContainEqual({
      label: "模型输出",
      text: "模型给出的审查建议",
      code: true,
    });
    expect(model?.sections).toContainEqual({
      label: "请求 · user",
      text: "采购合同原文",
      code: true,
    });

    const observed = trajectory.entries.find((entry) => entry.id === "agent:tool-observed");
    expect(observed?.sections).toContainEqual({ label: "工具返回", text: "发现金额冲突" });
    expect(observed?.fields).toContainEqual({ label: "工具", value: "evidence.lookup" });
    expect(observed?.fields).toContainEqual({ label: "Fact ID", value: "fact-1" });

    const failed = trajectory.entries.find((entry) => entry.id === "agent:tool-failed");
    expect(failed?.status).toBe("failed");
    expect(failed?.callID).toBe("call-2");
    expect(failed?.fields).toContainEqual({ label: "能力缺口", value: "gap-2" });

    const knowledge = trajectory.entries.find((entry) => entry.kind === "knowledge");
    expect(knowledge?.sections).toContainEqual({ label: "读取的知识文本", text: "知识文本片段" });
    expect(knowledge?.fields).toContainEqual({ label: "知识", value: "knowledge-1" });
    expect(knowledge?.search).toContain("知识文本片段");
  });

  it("shows deterministic checks that never opened a span", () => {
    const trajectory = buildTrajectory(
      execution({
        events: [
          event({
            seq: 9,
            node_kind: "tool_call",
            phase: "evidence_tool",
            event_type: "tool_evidence",
            status: "no_statement",
            model_ref: "dedicated_line.minimum_bandwidth",
            error_code: "tool_evidence_no_statement",
          }),
        ],
        agent_review: null,
      }),
    );
    const [entry] = trajectory.entries;
    expect(entry?.kind).toBe("event");
    expect(entry?.title).toBe("证据工具 · 证据工具结果");
    expect(entry?.meta).toEqual([
      "tool_call",
      "dedicated_line.minimum_bandwidth",
    ]);
    expect(entry?.fields).toContainEqual({
      label: "错误码",
      value: "tool_evidence_no_statement",
    });
  });

  it("states when model output and timestamps were not recorded", () => {
    const trajectory = buildTrajectory(
      execution({
        events: [
          event({
            seq: 1,
            span_id: "span-model",
            node_kind: "model_call",
            phase: "model_request",
            event_type: "span_started",
            at: null,
            status: "running",
            model_ref: "configured-model",
          }),
        ],
        agent_review: null,
        usage: null,
        duration_ms: null,
      }),
    );
    const [entry] = trajectory.entries;
    expect(entry?.kind).toBe("model");
    expect(entry?.at).toBeNull();
    expect(entry?.status).toBe("running");
    expect(entry?.meta).toContain("Token 未报告");
    expect(entry?.sections).toContainEqual({
      label: "请求与输出",
      text: "本次调用没有保存请求或输出内容；只能看到模型标识、状态和用量。",
    });
  });

  it("returns an empty trajectory instead of inventing activity", () => {
    const trajectory = buildTrajectory(
      execution({ events: [], agent_review: null, usage: null, duration_ms: null }),
    );
    expect(trajectory.entries).toEqual([]);
    expect(trajectory).toMatchObject({
      stages: 0,
      modelCalls: 0,
      toolCalls: 0,
      knowledgeReads: 0,
      durationMs: null,
      inputTokens: null,
      outputTokens: null,
    });
  });

  it("keeps unknown event labels visible", () => {
    expect(agentEventLabel("new_runtime_event")).toBe("new_runtime_event");
    expect(agentEventLabel("tool_observed")).toBe("工具返回");
  });
});
