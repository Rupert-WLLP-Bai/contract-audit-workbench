import { ApiError } from "./http";
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
  AgentReviewGapStatus,
  AgentReviewPlanStatus,
  AgentReviewRelation,
  AgentReviewRoundStatus,
  AgentReviewStatus,
} from "./types";

function invalid(path: string): never {
  throw new ApiError(`五域审查响应中的 ${path} 无法识别`, 200, "invalid_response", false, null);
}

function record(value: unknown, path: string, keys: readonly string[]): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return invalid(path);
  const object = value as Record<string, unknown>;
  if (Object.keys(object).some((key) => !keys.includes(key)) || keys.some((key) => !Object.hasOwn(object, key))) return invalid(path);
  return object;
}

function text(value: unknown, path: string): string {
  return typeof value === "string" ? value : invalid(path);
}

function integer(value: unknown, path: string): number {
  return typeof value === "number" && Number.isSafeInteger(value) ? value : invalid(path);
}

function boundedInteger(value: unknown, path: string, minimum: number, maximum = Number.MAX_SAFE_INTEGER): number {
  const result = integer(value, path);
  return result >= minimum && result <= maximum ? result : invalid(path);
}

function enumValue<T extends string>(value: unknown, path: string, values: readonly T[]): T {
  return typeof value === "string" && (values as readonly string[]).includes(value) ? value as T : invalid(path);
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
  return enumValue(value, path, ["compliance", "commercial", "technical", "fulfillment", "counterparty"]);
}

function mode(value: unknown, path: string): AgentPlannerMode {
  return enumValue(value, path, ["fixed_chain", "deterministic_planner", "model_fixture", "model_live"]);
}

function reviewStatus(value: unknown, path: string): AgentReviewStatus {
  return enumValue(value, path, ["queued", "planning", "running", "completed", "partial", "blocked_live", "cancelled", "failed", "rejected"]);
}

function relation(value: unknown, path: string): AgentReviewRelation | null {
  return value === null ? null : enumValue<AgentReviewRelation>(value, path, ["compatible", "conflict", "insufficient", "not_applicable"]);
}

function anchorStatus(value: unknown, path: string): AgentReviewAnchorStatus {
  return enumValue(value, path, ["located", "unlocatable"]);
}

function sourceSnapshot(value: unknown): AgentReview["source_snapshot"] {
  if (value === null) return null;
  const item = record(value, "source_snapshot", ["document_ids", "parsed_document_ids", "parse_selection_hash", "parser_configuration_id"]);
  return {
    document_ids: strings(item.document_ids, "source_snapshot.document_ids"),
    parsed_document_ids: strings(item.parsed_document_ids, "source_snapshot.parsed_document_ids"),
    parse_selection_hash: text(item.parse_selection_hash, "source_snapshot.parse_selection_hash"),
    parser_configuration_id: text(item.parser_configuration_id, "source_snapshot.parser_configuration_id"),
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

function planner(value: unknown): AgentReview["planner"] {
  const item = record(value, "planner", [
    "identity", "configuration_id", "model_configuration_id", "prompt_version", "prompt_sha256",
    "schema_version", "runtime_version", "tool_catalog_version", "fallback_used", "fallback_code", "limits",
  ]);
  const limits = record(item.limits, "planner.limits", ["max_rounds", "max_calls", "max_duration_ms", "max_evidence_bytes"]);
  if (typeof item.fallback_used !== "boolean") return invalid("planner.fallback_used");
  return {
    identity: text(item.identity, "planner.identity"),
    configuration_id: text(item.configuration_id, "planner.configuration_id"),
    model_configuration_id: nullableText(item.model_configuration_id, "planner.model_configuration_id"),
    prompt_version: text(item.prompt_version, "planner.prompt_version"),
    prompt_sha256: text(item.prompt_sha256, "planner.prompt_sha256"),
    schema_version: text(item.schema_version, "planner.schema_version"),
    runtime_version: text(item.runtime_version, "planner.runtime_version"),
    tool_catalog_version: text(item.tool_catalog_version, "planner.tool_catalog_version"),
    fallback_used: item.fallback_used,
    fallback_code: nullableText(item.fallback_code, "planner.fallback_code"),
    limits: {
      max_rounds: boundedInteger(limits.max_rounds, "planner.limits.max_rounds", 1, 3),
      max_calls: boundedInteger(limits.max_calls, "planner.limits.max_calls", 1, 16),
      max_duration_ms: boundedInteger(limits.max_duration_ms, "planner.limits.max_duration_ms", 1000, 120000),
      max_evidence_bytes: boundedInteger(limits.max_evidence_bytes, "planner.limits.max_evidence_bytes", 1, 8388608),
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

function capability(value: unknown, path: string): AgentReview["domains"][number]["capabilities"][number] {
  const item = record(value, path, ["capability_id", "status", "relation", "reason_code", "evidence_ids", "canonical_evaluation_ids"]);
  return {
    capability_id: text(item.capability_id, `${path}.capability_id`),
    status: enumValue<AgentReviewCapabilityStatus>(item.status, `${path}.status`, ["supported", "completed", "insufficient", "partial", "unknown", "unsupported", "not_applicable"]),
    relation: relation(item.relation, `${path}.relation`),
    reason_code: text(item.reason_code, `${path}.reason_code`),
    evidence_ids: strings(item.evidence_ids, `${path}.evidence_ids`),
    canonical_evaluation_ids: strings(item.canonical_evaluation_ids, `${path}.canonical_evaluation_ids`),
  };
}

function domainCoverage(value: unknown, path: string): AgentReview["domains"][number] {
  const item = record(value, path, ["domain", "status", "capabilities"]);
  if (!Array.isArray(item.capabilities)) return invalid(`${path}.capabilities`);
  return {
    domain: domain(item.domain, `${path}.domain`),
    status: enumValue<AgentReviewDomainStatus>(item.status, `${path}.status`, ["not_requested", "supported", "completed", "insufficient", "partial", "unknown", "unsupported", "not_applicable"]),
    capabilities: item.capabilities.map((entry, index) => capability(entry, `${path}.capabilities[${index}]`)),
  };
}

function gap(value: unknown, path: string): AgentReview["gaps"][number] {
  const item = record(value, path, ["gap_id", "domain", "capability_id", "status", "reason_code", "evidence_ids"]);
  return {
    gap_id: text(item.gap_id, `${path}.gap_id`),
    domain: domain(item.domain, `${path}.domain`),
    capability_id: text(item.capability_id, `${path}.capability_id`),
    status: enumValue<AgentReviewGapStatus>(item.status, `${path}.status`, ["open", "resolved", "completed", "insufficient", "unsupported", "unknown"]),
    reason_code: text(item.reason_code, `${path}.reason_code`),
    evidence_ids: strings(item.evidence_ids, `${path}.evidence_ids`),
  };
}

function plannedCall(value: unknown, path: string): AgentReview["plan"]["calls"][number] {
  const item = record(value, path, ["call_id", "tool_id", "tool_version", "domain", "capability_id", "gap_id", "status", "reason_code", "evidence_ids"]);
  return {
    call_id: text(item.call_id, `${path}.call_id`),
    tool_id: text(item.tool_id, `${path}.tool_id`),
    tool_version: text(item.tool_version, `${path}.tool_version`),
    domain: domain(item.domain, `${path}.domain`),
    capability_id: text(item.capability_id, `${path}.capability_id`),
    gap_id: text(item.gap_id, `${path}.gap_id`),
    status: enumValue<AgentReviewCallStatus>(item.status, `${path}.status`, ["planned", "running", "completed", "failed", "denied", "cancelled", "insufficient", "unsupported"]),
    reason_code: nullableText(item.reason_code, `${path}.reason_code`),
    evidence_ids: strings(item.evidence_ids, `${path}.evidence_ids`),
  };
}

function planRound(value: unknown, path: string): AgentReview["plan"]["rounds"][number] {
  const item = record(value, path, ["round", "status", "gap_ids", "calls", "new_evidence_ids", "resolved_gap_ids", "termination_code"]);
  if (!Array.isArray(item.calls)) return invalid(`${path}.calls`);
  return {
    round: boundedInteger(item.round, `${path}.round`, 1),
    status: enumValue<AgentReviewRoundStatus>(item.status, `${path}.status`, ["accepted", "guard_rejected", "completed", "partial", "no_progress", "budget_exhausted", "cancelled", "failed"]),
    gap_ids: strings(item.gap_ids, `${path}.gap_ids`),
    calls: item.calls.map((call, index) => plannedCall(call, `${path}.calls[${index}]`)),
    new_evidence_ids: item.new_evidence_ids === null ? null : strings(item.new_evidence_ids, `${path}.new_evidence_ids`),
    resolved_gap_ids: item.resolved_gap_ids === null ? null : strings(item.resolved_gap_ids, `${path}.resolved_gap_ids`),
    termination_code: nullableText(item.termination_code, `${path}.termination_code`),
  };
}

function plan(value: unknown): AgentReview["plan"] {
  const item = record(value, "plan", ["status", "rejection_code", "calls", "rounds"]);
  const status = enumValue<AgentReviewPlanStatus>(item.status, "plan.status", ["not_planned", "accepted", "guard_rejected", "executing", "completed", "partial", "fallback"]);
  if (!Array.isArray(item.calls) || !Array.isArray(item.rounds)) return invalid("plan.calls/rounds");
  return {
    status,
    rejection_code: nullableText(item.rejection_code, "plan.rejection_code"),
    calls: item.calls.map((call, index) => plannedCall(call, `plan.calls[${index}]`)),
    rounds: item.rounds.map((round, index) => planRound(round, `plan.rounds[${index}]`)),
  };
}

function evidence(value: unknown, path: string): AgentReview["evidence"][number] {
  const item = record(value, path, ["evidence_id", "semantic_type", "value", "unit", "currency", "verification", "anchor"]);
  const anchor = record(item.anchor, `${path}.anchor`, ["id", "document_id", "parsed_document_id", "normalized_digest", "quote", "status", "start", "end", "reason", "page", "element_id"]);
  const start = boundedInteger(anchor.start, `${path}.anchor.start`, 0);
  const end = boundedInteger(anchor.end, `${path}.anchor.end`, 0);
  if (end <= start) return invalid(`${path}.anchor.end`);
  return {
    evidence_id: text(item.evidence_id, `${path}.evidence_id`),
    semantic_type: text(item.semantic_type, `${path}.semantic_type`),
    value: text(item.value, `${path}.value`),
    unit: nullableText(item.unit, `${path}.unit`),
    currency: nullableText(item.currency, `${path}.currency`),
    verification: enumValue<AgentReviewEvidenceVerification>(item.verification, `${path}.verification`, ["verified", "typed_semantic_and_frozen_quote_verified", "unknown", "rejected"]),
    anchor: {
      id: text(anchor.id, `${path}.anchor.id`),
      document_id: text(anchor.document_id, `${path}.anchor.document_id`),
      parsed_document_id: text(anchor.parsed_document_id, `${path}.anchor.parsed_document_id`),
      normalized_digest: text(anchor.normalized_digest, `${path}.anchor.normalized_digest`),
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
  const item = record(value, path, ["sequence", "round", "kind", "call_id", "domain", "code", "duration_ms"]);
  return {
    sequence: boundedInteger(item.sequence, `${path}.sequence`, 1),
    round: boundedInteger(item.round, `${path}.round`, 0, 3),
    kind: enumValue<AgentReviewEventKind>(item.kind, `${path}.kind`, ["planning", "guard", "tool_call", "evidence", "budget", "terminal", "authorization", "input", "gap_unsupported", "plan_rejected", "planner_fallback", "plan", "plan_accepted", "tool_failed", "tool_completed"]),
    call_id: nullableText(item.call_id, `${path}.call_id`),
    domain: item.domain === null ? null : domain(item.domain, `${path}.domain`),
    code: text(item.code, `${path}.code`),
    duration_ms: boundedInteger(item.duration_ms, `${path}.duration_ms`, 0),
  };
}

export function parseAgentReview(value: unknown): AgentReview {
  const item = record(value, "agent_review", [
    "schema_version", "attempt_id", "status", "mode", "source_snapshot", "rulepack", "planner", "scope",
    "domains", "gaps", "plan", "evidence", "usage", "unresolved_gap_ids", "events",
  ]);
  if (item.schema_version !== "five-domain-review/v1") return invalid("schema_version");
  const usage = record(item.usage, "usage", ["planner_calls", "input_tokens", "output_tokens", "total_tokens"]);
  if (!Array.isArray(item.scope) || !Array.isArray(item.domains) || !Array.isArray(item.gaps) || !Array.isArray(item.evidence) || !Array.isArray(item.events)) return invalid("collection fields");
  return {
    schema_version: "five-domain-review/v1",
    attempt_id: text(item.attempt_id, "attempt_id"),
    status: reviewStatus(item.status, "status"),
    mode: mode(item.mode, "mode"),
    source_snapshot: sourceSnapshot(item.source_snapshot),
    rulepack: rulePack(item.rulepack),
    planner: planner(item.planner),
    scope: item.scope.map((entry, index) => scopeItem(entry, `scope[${index}]`)),
    domains: item.domains.map((entry, index) => domainCoverage(entry, `domains[${index}]`)),
    gaps: item.gaps.map((entry, index) => gap(entry, `gaps[${index}]`)),
    plan: plan(item.plan),
    evidence: item.evidence.map((entry, index) => evidence(entry, `evidence[${index}]`)),
    usage: {
      planner_calls: boundedInteger(usage.planner_calls, "usage.planner_calls", 0),
      input_tokens: nullableInteger(usage.input_tokens, "usage.input_tokens"),
      output_tokens: nullableInteger(usage.output_tokens, "usage.output_tokens"),
      total_tokens: nullableInteger(usage.total_tokens, "usage.total_tokens"),
    },
    unresolved_gap_ids: strings(item.unresolved_gap_ids, "unresolved_gap_ids"),
    events: item.events.map((entry, index) => event(entry, `events[${index}]`)),
  };
}
