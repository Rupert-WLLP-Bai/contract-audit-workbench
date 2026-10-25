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
});
