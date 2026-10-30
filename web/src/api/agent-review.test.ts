import { describe, expect, it } from "vitest";
import { parseAgentReview } from "./agent-review";

const validReview = {
  schema_version: "five-domain-review/v1",
  attempt_id: "attempt-1",
  status: "completed",
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
  plan: { status: "completed", rejection_code: null, calls: [], rounds: [] },
  evidence: [],
  usage: {
    planner_calls: 1,
    input_tokens: null,
    output_tokens: null,
    total_tokens: null,
  },
  unresolved_gap_ids: [],
  events: [
    {
      sequence: 4,
      round: 2,
      kind: "tool_call",
      call_id: "call-1",
      domain: "technical",
      code: "verified_frozen_source_facts",
      duration_ms: 8,
    },
  ],
};

describe("AgentReview API parser", () => {
  it("reads finite runtime statuses and preserves unrecorded historical round IDs", () => {
    const review = {
      ...validReview,
      domains: [
        {
          domain: "technical",
          status: "completed",
          capabilities: [
            {
              capability_id: "scope",
              mandatory: true,
              status: "insufficient",
              relation: null,
              reason_code: "required_source_or_field_missing",
              evidence_ids: [],
              canonical_evaluation_ids: [],
            },
          ],
        },
      ],
      gaps: [
        {
          gap_id: "gap-scope",
          domain: "technical",
          capability_id: "scope",
          status: "completed",
          reason_code: "verified_result",
          evidence_ids: [],
        },
      ],
      plan: {
        ...validReview.plan,
        rounds: [
          {
            round: 1,
            status: "partial",
            gap_ids: ["gap-scope"],
            calls: [],
            new_evidence_ids: null,
            resolved_gap_ids: [],
            termination_code: null,
          },
        ],
      },
      events: [{ ...validReview.events[0]!, kind: "plan_accepted" }],
    };
    expect(
      parseAgentReview(review).plan.rounds[0]?.new_evidence_ids,
    ).toBeNull();
    expect(parseAgentReview(review).domains[0]?.capabilities[0]?.status).toBe(
      "insufficient",
    );
    expect(parseAgentReview(review).domains[0]?.capabilities[0]?.mandatory).toBe(
      true,
    );
    expect(() =>
      parseAgentReview({
        ...review,
        domains: [
          {
            ...review.domains[0],
            capabilities: [{ ...review.domains[0]!.capabilities[0], mandatory: "yes" }],
          },
        ],
      }),
    ).toThrow(/mandatory/);
    expect(() =>
      parseAgentReview({
        ...review,
        domains: [{ ...review.domains[0], status: "probably_safe" }],
      }),
    ).toThrow(/domains\[0\].status/);
    expect(() =>
      parseAgentReview({
        ...review,
        plan: {
          ...review.plan,
          rounds: [{ ...review.plan.rounds[0], new_evidence_ids: [17] }],
        },
      }),
    ).toThrow(/new_evidence_ids/);
    expect(() =>
      parseAgentReview({
        ...review,
        events: [{ ...review.events[0], kind: "invented_event" }],
      }),
    ).toThrow(/events\[0\].kind/);
  });

  it("parses runtime tool events, returned observations and frozen knowledge reads", () => {
    const review = parseAgentReview({
      ...validReview,
      events: [
        {
          event_id: "event-1",
          sequence: 1,
          round: 1,
          kind: "tool_observed",
          tool_id: "evidence.lookup",
          gap_id: "gap-1",
          call_id: "call-1",
          domain: "commercial",
          code: "validated_tool_observation",
          occurred_at: "2026-10-30T00:00:01.000Z",
          duration_ms: 12,
        },
        {
          ...validReview.events[0],
          event_id: "event-2",
          sequence: 2,
          kind: "knowledge_read_completed",
          tool_id: "knowledge.read",
          gap_id: "gap-1",
          occurred_at: "2026-10-30T00:00:02.000Z",
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
          summary: "工具报告冲突",
          claim_status: "tool_reported_not_independently_verified",
          fact_ids: ["fact-1"],
          located_evidence_ids: ["evidence-1"],
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
          text: "知识文本",
        },
      ],
    });
    expect(review.events[0]).toMatchObject({
      event_id: "event-1",
      kind: "tool_observed",
      tool_id: "evidence.lookup",
      gap_id: "gap-1",
      occurred_at: "2026-10-30T00:00:01.000Z",
    });
    expect(review.events[1]?.kind).toBe("knowledge_read_completed");
    expect(review.observations?.[0]).toMatchObject({
      summary: "工具报告冲突",
      fact_ids: ["fact-1"],
      reason_code: "verified_result",
    });
    expect(review.knowledge_reads?.[0]?.text).toBe("知识文本");
  });

  it("preserves the runtime planner round on events", () => {
    expect(parseAgentReview(validReview).events[0]).toMatchObject({
      sequence: 4,
      round: 2,
      kind: "tool_call",
    });
  });

  it("accepts round zero for events before planner execution", () => {
    const review = structuredClone(validReview);
    review.events[0]!.round = 0;
    expect(parseAgentReview(review).events[0]?.round).toBe(0);
  });

  it("rejects event rounds above the configured OpenAPI maximum", () => {
    const review = structuredClone(validReview);
    review.events[0]!.round = 4;
    expect(() => parseAgentReview(review)).toThrow(/events\[0\]\.round/);
  });

  it("keeps root lifecycle and plan status enums distinct", () => {
    const review = structuredClone(validReview);
    review.status = "blocked_live";
    review.plan.status = "guard_rejected";
    expect(parseAgentReview(review)).toMatchObject({
      status: "blocked_live",
      plan: { status: "guard_rejected" },
    });
  });

  it("rejects an out-of-range planner limit and undocumented event fields", () => {
    const review = structuredClone(validReview);
    review.planner.limits.max_rounds = 4;
    expect(() => parseAgentReview(review)).toThrow(
      /planner\.limits\.max_rounds/,
    );

    const withUnknownField = structuredClone(validReview);
    (
      withUnknownField.events[0] as (typeof withUnknownField.events)[0] & {
        provider_note?: string;
      }
    ).provider_note = "unreviewed";
    expect(() => parseAgentReview(withUnknownField)).toThrow(/events\[0\]/);
  });

  it("rejects an event without its round identity", () => {
    const review = structuredClone(validReview);
    delete (review.events[0] as Partial<(typeof review.events)[0]>).round;
    expect(() => parseAgentReview(review)).toThrow(/events\[0\]/);
  });

  it("reads knowledge advisory suggestions without presenting them as conclusions", () => {
    const review = {
      ...structuredClone(validReview),
      knowledge_selection: {
        snapshot_sha256: "a".repeat(64),
        algorithm: "chinese-bigram-label/v1",
        policy_status: "available",
        policy_reason: "",
        input: {},
        candidates: [],
        budget: {},
        planner_rounds: [],
        reads: [],
        advisory: {
          mode: "advisory",
          feature: "knowledge_rerank",
          provider: "model-gateway",
          configuration_id: "b".repeat(64),
          model_requested: "jev-1.14.0",
          model_observed: "jev-1.14.0",
          question_set_version: "knowledge-relevance/v1",
          egress: "minimized",
          request_sha256: "c".repeat(64),
          judgment_id: "d".repeat(64),
          status: "completed",
          truncated: false,
          candidates: [
            {
              knowledge_id: "guide-a",
              version_id: "v1",
              content_sha256: "e".repeat(64),
              alias: "candidate_0",
              probability: 0.82,
              band: "relevant",
              rank_before: 2,
              rank_after: 1,
            },
            {
              knowledge_id: "guide-b",
              version_id: "v1",
              content_sha256: "f".repeat(64),
              alias: "candidate_1",
              band: "unknown",
              rank_before: 1,
              rank_after: 2,
            },
          ],
        },
      },
    };
    const parsed = parseAgentReview(review);
    expect(parsed.knowledge_selection?.advisory).toMatchObject({
      status: "completed",
      model_observed: "jev-1.14.0",
      candidates: [
        {
          knowledge_id: "guide-a",
          probability: 0.82,
          band: "relevant",
          rank_before: 2,
          rank_after: 1,
          reason: null,
        },
        { knowledge_id: "guide-b", probability: null, band: "unknown" },
      ],
    });
    expect(parsed.knowledge_selection?.advisory?.error_code).toBeNull();
    expect(parsed.knowledge_selection?.policy_status).toBe("available");
  });

  it("keeps old reviews without knowledge selection compatible", () => {
    expect(parseAgentReview(validReview).knowledge_selection).toBeNull();
  });

  it("rejects unknown advisory fields and out-of-range probabilities", () => {
    const review = {
      ...structuredClone(validReview),
      knowledge_selection: {
        snapshot_sha256: "a".repeat(64),
        algorithm: "chinese-bigram-label/v1",
        policy_status: "available",
        policy_reason: "",
        input: {},
        candidates: [],
        budget: {},
        planner_rounds: [],
        reads: [],
        advisory: {
          mode: "advisory",
          feature: "knowledge_rerank",
          provider: "model-gateway",
          configuration_id: "b".repeat(64),
          question_set_version: "knowledge-relevance/v1",
          egress: "minimized",
          status: "completed",
          candidates: [] as unknown[],
        },
      },
    };
    expect(() => parseAgentReview(review)).not.toThrow();
    const withUnknown = structuredClone(review);
    (
      withUnknown.knowledge_selection.advisory as typeof withUnknown.knowledge_selection.advisory & {
        provider_note?: string;
      }
    ).provider_note = "unreviewed";
    expect(() => parseAgentReview(withUnknown)).toThrow(
      /knowledge_selection\.advisory/,
    );
    const withProbability = structuredClone(review);
    withProbability.knowledge_selection.advisory.candidates = [
      {
        knowledge_id: "guide-a",
        version_id: "v1",
        content_sha256: "e".repeat(64),
        alias: "candidate_0",
        probability: 1.2,
        band: "relevant",
        rank_before: 1,
        rank_after: 1,
      },
    ];
    expect(() => parseAgentReview(withProbability)).toThrow(
      /candidates\[0\]\.probability/,
    );
  });
});
