import { enumValue, responseReader } from "./respond";
import type {
  AgentPlannerMode,
  AgentReview,
  AgentReviewAnchorStatus,
  AgentReviewCallStatus,
  AgentReviewCapabilityStatus,
  AgentReviewDomain,
  AgentReviewDomainStatus,
  AgentReviewEventKind,
  AgentReviewEvidenceVerification,
  AgentReviewAdvisoryBand,
  AgentReviewAdvisoryMode,
  AgentReviewAdvisoryProvider,
  AgentReviewAdvisoryStatus,
  AgentReviewGapStatus,
  AgentReviewKnowledgeAdvisory,
  AgentReviewKnowledgeAdvisoryCandidate,
  AgentReviewKnowledgeSelection,
  AgentReviewPlanStatus,
  AgentReviewRelation,
  AgentReviewRoundStatus,
  AgentReviewStatus,
} from "./types";

const { invalid, record, text, integer } = responseReader(
  (path) => `五域审查响应中的 ${path} 无法识别`,
);

function optionalRecord(
  value: unknown,
  path: string,
  required: readonly string[],
  optional: readonly string[],
): Record<string, unknown> {
  return record(value, path, [...required, ...optional], required);
}

function boundedInteger(
  value: unknown,
  path: string,
  minimum: number,
  maximum = Number.MAX_SAFE_INTEGER,
): number {
  const result = integer(value, path);
  return result >= minimum && result <= maximum ? result : invalid(path);
}

function strings(value: unknown, path: string): string[] {
  if (!Array.isArray(value)) return invalid(path);
  return value.map((item, index) => text(item, `${path}[${index}]`));
}

function nullableText(value: unknown, path: string): string | null {
  return value === null ? null : text(value, path);
}

function nullableInteger(value: unknown, path: string): number | null {
  return value === null ? null : integer(value, path);
}

function domain(value: unknown, path: string): AgentReviewDomain {
  return (
    enumValue(value, [
      "compliance",
      "commercial",
      "technical",
      "fulfillment",
      "counterparty",
    ]) ?? invalid(path)
  );
}

function mode(value: unknown, path: string): AgentPlannerMode {
  return (
    enumValue(value, [
      "fixed_chain",
      "deterministic_planner",
      "model_fixture",
      "model_live",
    ]) ?? invalid(path)
  );
}

function reviewStatus(value: unknown, path: string): AgentReviewStatus {
  return (
    enumValue(value, [
      "queued",
      "planning",
      "running",
      "completed",
      "partial",
      "blocked_live",
      "cancelled",
      "failed",
      "rejected",
    ]) ?? invalid(path)
  );
}

function relation(value: unknown, path: string): AgentReviewRelation | null {
  if (value === null) return null;
  return (
    enumValue<AgentReviewRelation>(value, [
      "compatible",
      "conflict",
      "insufficient",
      "not_applicable",
    ]) ?? invalid(path)
  );
}

function anchorStatus(value: unknown, path: string): AgentReviewAnchorStatus {
  return enumValue(value, ["located", "unlocatable"]) ?? invalid(path);
}

function sourceSnapshot(value: unknown): AgentReview["source_snapshot"] {
  if (value === null) return null;
  const item = record(value, "source_snapshot", [
    "document_ids",
    "parsed_document_ids",
    "parse_selection_hash",
    "parser_configuration_id",
  ]);
  return {
    document_ids: strings(item.document_ids, "source_snapshot.document_ids"),
    parsed_document_ids: strings(
      item.parsed_document_ids,
      "source_snapshot.parsed_document_ids",
    ),
    parse_selection_hash: text(
      item.parse_selection_hash,
      "source_snapshot.parse_selection_hash",
    ),
    parser_configuration_id: text(
      item.parser_configuration_id,
      "source_snapshot.parser_configuration_id",
    ),
  };
}

function rulePack(value: unknown): AgentReview["rulepack"] {
  if (value === null) return null;
  const item = record(value, "rulepack", ["id", "version", "content_sha256"]);
  return {
    id: text(item.id, "rulepack.id"),
    version: text(item.version, "rulepack.version"),
    content_sha256: text(item.content_sha256, "rulepack.content_sha256"),
  };
}

const advisoryStatuses: readonly AgentReviewAdvisoryStatus[] = [
  "completed",
  "unavailable",
  "not_applicable",
  "model_mismatch",
  "invalid_response",
  "budget_exhausted",
  "persistence_failed",
  "config_mismatch",
];

const advisoryBands: readonly AgentReviewAdvisoryBand[] = [
  "relevant",
  "uncertain",
  "not_relevant",
  "unknown",
];

const advisoryModes: readonly AgentReviewAdvisoryMode[] = [
  "advisory",
  "fixture",
];

const advisoryProviders: readonly AgentReviewAdvisoryProvider[] = [
  "model-gateway",
  "local-fixture",
  "direct",
];

function probability(value: unknown, path: string): number | null {
  if (value === undefined) return null;
  if (
    typeof value !== "number" ||
    !Number.isFinite(value) ||
    value < 0 ||
    value > 1
  )
    return invalid(path);
  return value;
}

function advisoryCandidate(
  value: unknown,
  path: string,
): AgentReviewKnowledgeAdvisoryCandidate {
  const item = optionalRecord(
    value,
    path,
    [
      "knowledge_id",
      "version_id",
      "content_sha256",
      "alias",
      "band",
      "rank_before",
      "rank_after",
    ],
    ["probability", "reason"],
  );
  return {
    knowledge_id: text(item.knowledge_id, `${path}.knowledge_id`),
    version_id: text(item.version_id, `${path}.version_id`),
    content_sha256: text(item.content_sha256, `${path}.content_sha256`),
    alias: text(item.alias, `${path}.alias`),
    probability: probability(item.probability, `${path}.probability`),
    band: enumValue(item.band, advisoryBands) ?? invalid(`${path}.band`),
    rank_before: boundedInteger(item.rank_before, `${path}.rank_before`, 1),
    rank_after: boundedInteger(item.rank_after, `${path}.rank_after`, 1),
    reason: item.reason === undefined ? null : text(item.reason, `${path}.reason`),
  };
}

function advisoryEnvelope(
  value: unknown,
  path: string,
): Record<string, unknown> {
  const item = optionalRecord(
    value,
    path,
    [
      "mode",
      "feature",
      "provider",
      "configuration_id",
      "question_set_version",
      "egress",
      "status",
      "candidates",
    ],
    [
      "model_requested",
      "model_observed",
      "request_sha256",
      "judgment_id",
      "error_code",
      "truncated",
    ],
  );
  if (!Array.isArray(item.candidates)) return invalid(`${path}.candidates`);
  if (item.truncated !== undefined && typeof item.truncated !== "boolean")
    return invalid(`${path}.truncated`);
  if (item.feature !== "knowledge_rerank") return invalid(`${path}.feature`);
  if (item.egress !== "minimized") return invalid(`${path}.egress`);
  return item;
}

function optionalAdvisoryText(
  item: Record<string, unknown>,
  key: string,
  path: string,
): string | null {
  return item[key] === undefined ? null : text(item[key], `${path}.${key}`);
}

function knowledgeAdvisory(
  value: unknown,
  path: string,
): AgentReviewKnowledgeAdvisory {
  const item = advisoryEnvelope(value, path);
  const candidates = item.candidates as unknown[];
  return {
    mode: enumValue(item.mode, advisoryModes) ?? invalid(`${path}.mode`),
    feature: "knowledge_rerank",
    provider:
      enumValue(item.provider, advisoryProviders) ??
      invalid(`${path}.provider`),
    configuration_id: text(item.configuration_id, `${path}.configuration_id`),
    model_requested: optionalAdvisoryText(item, "model_requested", path),
    model_observed: optionalAdvisoryText(item, "model_observed", path),
    question_set_version: text(
      item.question_set_version,
      `${path}.question_set_version`,
    ),
    egress: "minimized",
    request_sha256: optionalAdvisoryText(item, "request_sha256", path),
    judgment_id: optionalAdvisoryText(item, "judgment_id", path),
    status:
      enumValue(item.status, advisoryStatuses) ?? invalid(`${path}.status`),
    error_code: optionalAdvisoryText(item, "error_code", path),
    truncated: item.truncated === true,
    candidates: candidates.map((candidate, index) =>
      advisoryCandidate(candidate, `${path}.candidates[${index}]`),
    ),
  };
}

function knowledgeSelection(
  value: unknown,
): AgentReviewKnowledgeSelection | null {
  if (value === undefined || value === null) return null;
  const item = optionalRecord(
    value,
    "knowledge_selection",
    [
      "snapshot_sha256",
      "algorithm",
      "policy_status",
      "policy_reason",
      "input",
      "candidates",
      "budget",
      "planner_rounds",
      "reads",
    ],
    ["advisory"],
  );
  return {
    snapshot_sha256: text(
      item.snapshot_sha256,
      "knowledge_selection.snapshot_sha256",
    ),
    algorithm: text(item.algorithm, "knowledge_selection.algorithm"),
    policy_status: text(
      item.policy_status,
      "knowledge_selection.policy_status",
    ),
    policy_reason: text(
      item.policy_reason,
      "knowledge_selection.policy_reason",
    ),
    advisory:
      item.advisory === undefined
        ? null
        : knowledgeAdvisory(
            item.advisory,
            "knowledge_selection.advisory",
          ),
  };
}

function planner(value: unknown): AgentReview["planner"] {
  const item = record(value, "planner", [
    "identity",
    "configuration_id",
    "model_configuration_id",
    "prompt_version",
    "prompt_sha256",
    "schema_version",
    "runtime_version",
    "tool_catalog_version",
    "fallback_used",
    "fallback_code",
    "limits",
  ]);
  const limits = record(item.limits, "planner.limits", [
    "max_rounds",
    "max_calls",
    "max_duration_ms",
    "max_evidence_bytes",
  ]);
  if (typeof item.fallback_used !== "boolean")
    return invalid("planner.fallback_used");
  return {
    identity: text(item.identity, "planner.identity"),
    configuration_id: text(item.configuration_id, "planner.configuration_id"),
    model_configuration_id: nullableText(
      item.model_configuration_id,
      "planner.model_configuration_id",
    ),
    prompt_version: text(item.prompt_version, "planner.prompt_version"),
    prompt_sha256: text(item.prompt_sha256, "planner.prompt_sha256"),
    schema_version: text(item.schema_version, "planner.schema_version"),
    runtime_version: text(item.runtime_version, "planner.runtime_version"),
    tool_catalog_version: text(
      item.tool_catalog_version,
      "planner.tool_catalog_version",
    ),
    fallback_used: item.fallback_used,
    fallback_code: nullableText(item.fallback_code, "planner.fallback_code"),
    limits: {
      max_rounds: boundedInteger(
        limits.max_rounds,
        "planner.limits.max_rounds",
        1,
        3,
      ),
      max_calls: boundedInteger(
        limits.max_calls,
        "planner.limits.max_calls",
        1,
        16,
      ),
      max_duration_ms: boundedInteger(
        limits.max_duration_ms,
        "planner.limits.max_duration_ms",
        1000,
        120000,
      ),
      max_evidence_bytes: boundedInteger(
        limits.max_evidence_bytes,
        "planner.limits.max_evidence_bytes",
        1,
        8388608,
      ),
    },
  };
}

function scopeItem(value: unknown, path: string): AgentReview["scope"][number] {
  const item = record(value, path, ["domain", "capability_ids", "source_ids"]);
  return {
    domain: domain(item.domain, `${path}.domain`),
    capability_ids: strings(item.capability_ids, `${path}.capability_ids`),
    source_ids: strings(item.source_ids, `${path}.source_ids`),
  };
}

function capability(
  value: unknown,
  path: string,
): AgentReview["domains"][number]["capabilities"][number] {
  const item = record(value, path, [
    "capability_id",
    "status",
    "relation",
    "reason_code",
    "evidence_ids",
    "canonical_evaluation_ids",
  ]);
  return {
    capability_id: text(item.capability_id, `${path}.capability_id`),
    status:
      enumValue<AgentReviewCapabilityStatus>(item.status, [
        "supported",
        "completed",
        "insufficient",
        "partial",
        "unknown",
        "unsupported",
        "not_applicable",
      ]) ?? invalid(`${path}.status`),
    relation: relation(item.relation, `${path}.relation`),
    reason_code: text(item.reason_code, `${path}.reason_code`),
    evidence_ids: strings(item.evidence_ids, `${path}.evidence_ids`),
    canonical_evaluation_ids: strings(
      item.canonical_evaluation_ids,
      `${path}.canonical_evaluation_ids`,
    ),
  };
}

function domainCoverage(
  value: unknown,
  path: string,
): AgentReview["domains"][number] {
  const item = record(value, path, ["domain", "status", "capabilities"]);
  if (!Array.isArray(item.capabilities)) return invalid(`${path}.capabilities`);
  return {
    domain: domain(item.domain, `${path}.domain`),
    status:
      enumValue<AgentReviewDomainStatus>(item.status, [
        "not_requested",
        "supported",
        "completed",
        "insufficient",
        "partial",
        "unknown",
        "unsupported",
        "not_applicable",
      ]) ?? invalid(`${path}.status`),
    capabilities: item.capabilities.map((entry, index) =>
      capability(entry, `${path}.capabilities[${index}]`),
    ),
  };
}

function gap(value: unknown, path: string): AgentReview["gaps"][number] {
  const item = record(value, path, [
    "gap_id",
    "domain",
    "capability_id",
    "status",
    "reason_code",
    "evidence_ids",
  ]);
  return {
    gap_id: text(item.gap_id, `${path}.gap_id`),
    domain: domain(item.domain, `${path}.domain`),
    capability_id: text(item.capability_id, `${path}.capability_id`),
    status:
      enumValue<AgentReviewGapStatus>(item.status, [
        "open",
        "resolved",
        "completed",
        "insufficient",
        "unsupported",
        "unknown",
      ]) ?? invalid(`${path}.status`),
    reason_code: text(item.reason_code, `${path}.reason_code`),
    evidence_ids: strings(item.evidence_ids, `${path}.evidence_ids`),
  };
}

function plannedCall(
  value: unknown,
  path: string,
): AgentReview["plan"]["calls"][number] {
  const item = record(value, path, [
    "call_id",
    "tool_id",
    "tool_version",
    "domain",
    "capability_id",
    "gap_id",
    "status",
    "reason_code",
    "evidence_ids",
  ]);
  return {
    call_id: text(item.call_id, `${path}.call_id`),
    tool_id: text(item.tool_id, `${path}.tool_id`),
    tool_version: text(item.tool_version, `${path}.tool_version`),
    domain: domain(item.domain, `${path}.domain`),
    capability_id: text(item.capability_id, `${path}.capability_id`),
    gap_id: text(item.gap_id, `${path}.gap_id`),
    status:
      enumValue<AgentReviewCallStatus>(item.status, [
        "planned",
        "running",
        "completed",
        "failed",
        "denied",
        "cancelled",
        "insufficient",
        "unsupported",
      ]) ?? invalid(`${path}.status`),
    reason_code: nullableText(item.reason_code, `${path}.reason_code`),
    evidence_ids: strings(item.evidence_ids, `${path}.evidence_ids`),
  };
}

function planRound(
  value: unknown,
  path: string,
): AgentReview["plan"]["rounds"][number] {
  const item = record(value, path, [
    "round",
    "status",
    "gap_ids",
    "calls",
    "new_evidence_ids",
    "resolved_gap_ids",
    "termination_code",
  ]);
  if (!Array.isArray(item.calls)) return invalid(`${path}.calls`);
  return {
    round: boundedInteger(item.round, `${path}.round`, 1),
    status:
      enumValue<AgentReviewRoundStatus>(item.status, [
        "accepted",
        "guard_rejected",
        "completed",
        "partial",
        "no_progress",
        "budget_exhausted",
        "cancelled",
        "failed",
      ]) ?? invalid(`${path}.status`),
    gap_ids: strings(item.gap_ids, `${path}.gap_ids`),
    calls: item.calls.map((call, index) =>
      plannedCall(call, `${path}.calls[${index}]`),
    ),
    new_evidence_ids:
      item.new_evidence_ids === null
        ? null
        : strings(item.new_evidence_ids, `${path}.new_evidence_ids`),
    resolved_gap_ids:
      item.resolved_gap_ids === null
        ? null
        : strings(item.resolved_gap_ids, `${path}.resolved_gap_ids`),
    termination_code: nullableText(
      item.termination_code,
      `${path}.termination_code`,
    ),
  };
}

function plan(value: unknown): AgentReview["plan"] {
  const item = record(value, "plan", [
    "status",
    "rejection_code",
    "calls",
    "rounds",
  ]);
  const status =
    enumValue<AgentReviewPlanStatus>(item.status, [
      "not_planned",
      "accepted",
      "guard_rejected",
      "executing",
      "completed",
      "partial",
      "fallback",
    ]) ?? invalid("plan.status");
  if (!Array.isArray(item.calls) || !Array.isArray(item.rounds))
    return invalid("plan.calls/rounds");
  return {
    status,
    rejection_code: nullableText(item.rejection_code, "plan.rejection_code"),
    calls: item.calls.map((call, index) =>
      plannedCall(call, `plan.calls[${index}]`),
    ),
    rounds: item.rounds.map((round, index) =>
      planRound(round, `plan.rounds[${index}]`),
    ),
  };
}

function evidence(
  value: unknown,
  path: string,
): AgentReview["evidence"][number] {
  const item = record(value, path, [
    "evidence_id",
    "semantic_type",
    "value",
    "unit",
    "currency",
    "verification",
    "anchor",
  ]);
  const anchor = record(item.anchor, `${path}.anchor`, [
    "id",
    "document_id",
    "parsed_document_id",
    "normalized_digest",
    "quote",
    "status",
    "start",
    "end",
    "reason",
    "page",
    "element_id",
  ]);
  const start = boundedInteger(anchor.start, `${path}.anchor.start`, 0);
  const end = boundedInteger(anchor.end, `${path}.anchor.end`, 0);
  if (end <= start) return invalid(`${path}.anchor.end`);
  return {
    evidence_id: text(item.evidence_id, `${path}.evidence_id`),
    semantic_type: text(item.semantic_type, `${path}.semantic_type`),
    value: text(item.value, `${path}.value`),
    unit: nullableText(item.unit, `${path}.unit`),
    currency: nullableText(item.currency, `${path}.currency`),
    verification:
      enumValue<AgentReviewEvidenceVerification>(item.verification, [
        "verified",
        "typed_semantic_and_frozen_quote_verified",
        "unknown",
        "rejected",
      ]) ?? invalid(`${path}.verification`),
    anchor: {
      id: text(anchor.id, `${path}.anchor.id`),
      document_id: text(anchor.document_id, `${path}.anchor.document_id`),
      parsed_document_id: text(
        anchor.parsed_document_id,
        `${path}.anchor.parsed_document_id`,
      ),
      normalized_digest: text(
        anchor.normalized_digest,
        `${path}.anchor.normalized_digest`,
      ),
      quote: text(anchor.quote, `${path}.anchor.quote`),
      status: anchorStatus(anchor.status, `${path}.anchor.status`),
      start,
      end,
      reason: nullableText(anchor.reason, `${path}.anchor.reason`),
      page: nullableInteger(anchor.page, `${path}.anchor.page`),
      element_id: nullableText(anchor.element_id, `${path}.anchor.element_id`),
    },
  };
}

function event(value: unknown, path: string): AgentReview["events"][number] {
  const item = record(value, path, [
    "sequence",
    "round",
    "kind",
    "call_id",
    "domain",
    "code",
    "duration_ms",
  ]);
  return {
    sequence: boundedInteger(item.sequence, `${path}.sequence`, 1),
    round: boundedInteger(item.round, `${path}.round`, 0, 3),
    kind:
      enumValue<AgentReviewEventKind>(item.kind, [
        "planning",
        "guard",
        "tool_call",
        "evidence",
        "budget",
        "terminal",
        "authorization",
        "input",
        "gap_unsupported",
        "plan_rejected",
        "planner_fallback",
        "plan",
        "plan_accepted",
        "tool_failed",
        "tool_completed",
      ]) ?? invalid(`${path}.kind`),
    call_id: nullableText(item.call_id, `${path}.call_id`),
    domain: item.domain === null ? null : domain(item.domain, `${path}.domain`),
    code: text(item.code, `${path}.code`),
    duration_ms: boundedInteger(item.duration_ms, `${path}.duration_ms`, 0),
  };
}

export function parseAgentReview(value: unknown): AgentReview {
  const item = optionalRecord(
    value,
    "agent_review",
    [
      "schema_version",
      "attempt_id",
      "status",
      "mode",
      "source_snapshot",
      "rulepack",
      "planner",
      "scope",
      "domains",
      "gaps",
      "plan",
      "evidence",
      "usage",
      "unresolved_gap_ids",
      "events",
    ],
    ["knowledge_selection"],
  );
  if (item.schema_version !== "five-domain-review/v1")
    return invalid("schema_version");
  const usage = record(item.usage, "usage", [
    "planner_calls",
    "input_tokens",
    "output_tokens",
    "total_tokens",
  ]);
  if (
    !Array.isArray(item.scope) ||
    !Array.isArray(item.domains) ||
    !Array.isArray(item.gaps) ||
    !Array.isArray(item.evidence) ||
    !Array.isArray(item.events)
  )
    return invalid("collection fields");
  return {
    schema_version: "five-domain-review/v1",
    attempt_id: text(item.attempt_id, "attempt_id"),
    status: reviewStatus(item.status, "status"),
    mode: mode(item.mode, "mode"),
    source_snapshot: sourceSnapshot(item.source_snapshot),
    rulepack: rulePack(item.rulepack),
    knowledge_selection: knowledgeSelection(item.knowledge_selection),
    planner: planner(item.planner),
    scope: item.scope.map((entry, index) =>
      scopeItem(entry, `scope[${index}]`),
    ),
    domains: item.domains.map((entry, index) =>
      domainCoverage(entry, `domains[${index}]`),
    ),
    gaps: item.gaps.map((entry, index) => gap(entry, `gaps[${index}]`)),
    plan: plan(item.plan),
    evidence: item.evidence.map((entry, index) =>
      evidence(entry, `evidence[${index}]`),
    ),
    usage: {
      planner_calls: boundedInteger(
        usage.planner_calls,
        "usage.planner_calls",
        0,
      ),
      input_tokens: nullableInteger(usage.input_tokens, "usage.input_tokens"),
      output_tokens: nullableInteger(
        usage.output_tokens,
        "usage.output_tokens",
      ),
      total_tokens: nullableInteger(usage.total_tokens, "usage.total_tokens"),
    },
    unresolved_gap_ids: strings(item.unresolved_gap_ids, "unresolved_gap_ids"),
    events: item.events.map((entry, index) => event(entry, `events[${index}]`)),
  };
}
