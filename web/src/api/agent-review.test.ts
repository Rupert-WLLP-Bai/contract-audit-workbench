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

  it("reads advisory optionals, statuses and provider variants", () => {
    const advisory = (overrides: Record<string, unknown> = {}) => ({
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
          mode: "fixture",
          feature: "knowledge_rerank",
          provider: "local-fixture",
          configuration_id: "b".repeat(64),
          model_requested: "jev-1",
          model_observed: "jev-1",
          question_set_version: "v1",
          egress: "minimized",
          request_sha256: "c".repeat(64),
          judgment_id: "d".repeat(64),
          status: "completed",
          error_code: "EGRESS",
          truncated: true,
          candidates: [
            {
              knowledge_id: "k1",
              version_id: "v1",
              content_sha256: "e".repeat(64),
              alias: "candidate_0",
              probability: 0,
              band: "not_relevant",
              rank_before: 1,
              rank_after: 2,
              reason: "低相关",
            },
          ],
          ...overrides,
        },
      },
    });
    expect(
      parseAgentReview(advisory()).knowledge_selection?.advisory,
    ).toMatchObject({
      mode: "fixture",
      provider: "local-fixture",
      model_requested: "jev-1",
      model_observed: "jev-1",
      request_sha256: "c".repeat(64),
      judgment_id: "d".repeat(64),
      error_code: "EGRESS",
      truncated: true,
      candidates: [
        {
          probability: 0,
          band: "not_relevant",
          reason: "低相关",
          rank_before: 1,
          rank_after: 2,
        },
      ],
    });
    expect(
      parseAgentReview(advisory({ truncated: undefined })).knowledge_selection
        ?.advisory?.truncated,
    ).toBe(false);
    expect(
      parseAgentReview(
        advisory({
          mode: "advisory",
          provider: "direct",
          status: "unavailable",
          model_requested: undefined,
          model_observed: undefined,
          request_sha256: undefined,
          judgment_id: undefined,
          error_code: undefined,
          candidates: [],
        }),
      ).knowledge_selection?.advisory,
    ).toMatchObject({
      model_requested: null,
      model_observed: null,
      request_sha256: null,
      judgment_id: null,
      error_code: null,
      status: "unavailable",
    });
  });

  it.each([
    ["candidates", { candidates: {} }, /knowledge_selection\.advisory\.candidates/],
    ["truncated", { truncated: "yes" }, /knowledge_selection\.advisory\.truncated/],
    ["feature", { feature: "other" }, /knowledge_selection\.advisory\.feature/],
    ["egress", { egress: "full" }, /knowledge_selection\.advisory\.egress/],
    ["mode", { mode: "unknown" }, /knowledge_selection\.advisory\.mode/],
    ["provider", { provider: "unknown" }, /knowledge_selection\.advisory\.provider/],
    ["status", { status: "unknown" }, /knowledge_selection\.advisory\.status/],
    [
      "candidate band",
      {
        candidates: [
          {
            knowledge_id: "k1",
            version_id: "v1",
            content_sha256: "e".repeat(64),
            alias: "candidate_0",
            band: "unknown_band",
            rank_before: 1,
            rank_after: 2,
          },
        ],
      },
      /knowledge_selection\.advisory\.candidates\[0\]\.band/,
    ],
    [
      "candidate rank",
      {
        candidates: [
          {
            knowledge_id: "k1",
            version_id: "v1",
            content_sha256: "e".repeat(64),
            alias: "candidate_0",
            band: "relevant",
            rank_before: 0,
            rank_after: 2,
          },
        ],
      },
      /knowledge_selection\.advisory\.candidates\[0\]\.rank_before/,
    ],
    [
      "candidate reason",
      {
        candidates: [
          {
            knowledge_id: "k1",
            version_id: "v1",
            content_sha256: "e".repeat(64),
            alias: "candidate_0",
            band: "relevant",
            rank_before: 1,
            rank_after: 2,
            reason: 7,
          },
        ],
      },
      /knowledge_selection\.advisory\.candidates\[0\]\.reason/,
    ],
  ])("rejects an invalid advisory %s", (_label, overrides, pattern) => {
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
          question_set_version: "v1",
          egress: "minimized",
          status: "completed",
          candidates: [],
          ...overrides,
        },
      },
    };
    expect(() => parseAgentReview(review)).toThrow(pattern);
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

function reviewWithAdvisoryCandidate(candidate: Record<string, unknown>) {
  return {
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
        question_set_version: "v1",
        egress: "minimized",
        status: "completed",
        candidates: [
          {
            knowledge_id: "k1",
            version_id: "v1",
            content_sha256: "e".repeat(64),
            alias: "candidate_0",
            band: "relevant",
            rank_before: 1,
            rank_after: 1,
            ...candidate,
          },
        ],
      },
    },
  };
}

function reviewWithAnchor(anchor: Record<string, unknown>) {
  return {
    ...structuredClone(validReview),
    evidence: [
      {
        evidence_id: "e1",
        semantic_type: "amount",
        value: "1",
        unit: null,
        currency: null,
        verification: "verified",
        anchor: {
          id: "a1",
          document_id: "d1",
          parsed_document_id: "pd1",
          normalized_digest: "digest",
          quote: "q",
          status: "located",
          start: 0,
          end: 1,
          reason: null,
          page: null,
          element_id: null,
          ...anchor,
        },
      },
    ],
  };
}

function reviewWithRound(round: Record<string, unknown>) {
  const review = structuredClone(validReview);
  review.plan.rounds = [
    {
      round: 1,
      status: "accepted",
      gap_ids: [],
      calls: [],
      new_evidence_ids: null,
      resolved_gap_ids: null,
      termination_code: null,
      ...round,
    },
  ] as typeof review.plan.rounds;
  return review;
}

describe("AgentReview boundary values", () => {
  it("accepts bounded planner limits at their maxima", () => {
    const review = structuredClone(validReview);
    review.planner.limits.max_rounds = 3;
    review.planner.limits.max_calls = 16;
    review.planner.limits.max_duration_ms = 120000;
    review.planner.limits.max_evidence_bytes = 8388608;
    expect(parseAgentReview(review).planner.limits).toEqual({
      max_rounds: 3,
      max_calls: 16,
      max_duration_ms: 120000,
      max_evidence_bytes: 8388608,
    });
  });

  it.each(["max_rounds", "max_calls", "max_evidence_bytes"])(
    "rejects planner limit %s below its minimum",
    (key) => {
      const review = structuredClone(validReview);
      (review.planner.limits as unknown as Record<string, number>)[key] = 0;
      expect(() => parseAgentReview(review)).toThrow(
        new RegExp(`planner\\.limits\\.${key}`),
      );
    },
  );

  it("accepts a probability of one and rejects NaN and negative values", () => {
    expect(
      parseAgentReview(reviewWithAdvisoryCandidate({ probability: 1 }))
        .knowledge_selection?.advisory?.candidates[0]?.probability,
    ).toBe(1);
    for (const probability of [Number.NaN, -0.5]) {
      expect(() =>
        parseAgentReview(reviewWithAdvisoryCandidate({ probability })),
      ).toThrow(/candidates\[0\]\.probability/);
    }
  });

  it("rejects a candidate rank after below one", () => {
    expect(() =>
      parseAgentReview(reviewWithAdvisoryCandidate({ rank_after: 0 })),
    ).toThrow(/candidates\[0\]\.rank_after/);
  });

  it("rejects a planning round below one", () => {
    expect(() => parseAgentReview(reviewWithRound({ round: 0 }))).toThrow(
      /plan\.rounds\[0\]\.round/,
    );
  });

  it("keeps resolved gap ids null or parses the list", () => {
    expect(
      parseAgentReview(reviewWithRound({ resolved_gap_ids: ["g1"] })).plan
        .rounds[0]?.resolved_gap_ids,
    ).toEqual(["g1"]);
    expect(
      parseAgentReview(reviewWithRound({ resolved_gap_ids: null })).plan
        .rounds[0]?.resolved_gap_ids,
    ).toBeNull();
  });

  it("rejects a plan whose calls or rounds are not arrays", () => {
    for (const key of ["calls", "rounds"]) {
      const review = structuredClone(validReview);
      (review.plan as unknown as Record<string, unknown>)[key] = "none";
      expect(() => parseAgentReview(review)).toThrow(/plan\.calls\/rounds/);
    }
  });

  it("accepts a zero-based anchor span and rejects an empty one", () => {
    expect(
      parseAgentReview(reviewWithAnchor({ start: 0, end: 1 })).evidence[0]
        ?.anchor,
    ).toMatchObject({ start: 0, end: 1 });
    expect(() =>
      parseAgentReview(reviewWithAnchor({ start: 0, end: 0 })),
    ).toThrow(/anchor\.end/);
  });

  it("enforces event sequence and accepts a zero duration", () => {
    const zero = structuredClone(validReview);
    zero.events[0]!.duration_ms = 0;
    expect(parseAgentReview(zero).events[0]?.duration_ms).toBe(0);
    const badSequence = structuredClone(validReview);
    badSequence.events[0]!.sequence = 0;
    expect(() => parseAgentReview(badSequence)).toThrow(
      /events\[0\]\.sequence/,
    );
  });

  it("keeps a nullable event domain", () => {
    expect(
      parseAgentReview(structuredClone(validReview)).events[0]?.domain,
    ).toBe("technical");
    const withNull = structuredClone(validReview);
    (withNull.events[0] as unknown as Record<string, unknown>).domain = null;
    expect(parseAgentReview(withNull).events[0]?.domain).toBeNull();
  });

  it.each(["scope", "domains", "gaps", "evidence", "events"])(
    "rejects non-array collection field %s",
    (key) => {
      const review: Record<string, unknown> = structuredClone(validReview);
      review[key] = "none";
      expect(() => parseAgentReview(review)).toThrow(/collection fields/);
    },
  );

  it("accepts zero planner calls", () => {
    const review = structuredClone(validReview);
    review.usage.planner_calls = 0;
    expect(parseAgentReview(review).usage.planner_calls).toBe(0);
  });
});
