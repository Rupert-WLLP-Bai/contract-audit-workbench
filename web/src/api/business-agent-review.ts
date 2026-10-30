import { responseReader } from "./respond";

export interface BusinessAgentScope {
  domain: string;
  capability_ids: string[] | null;
  source_ids: string[] | null;
}

export interface BusinessAgentCapability {
  capability_id: string;
  mandatory: boolean;
  status: string;
  relation: string | null;
  reason_code: string;
  evidence_ids: string[] | null;
  canonical_evaluation_ids: string[] | null;
}

export interface BusinessAgentDomain {
  domain: string;
  status: string;
  capabilities: BusinessAgentCapability[] | null;
}

export interface BusinessAgentGap {
  gap_id: string;
  domain: string;
  capability_id: string;
  mandatory: boolean;
  status: string;
  reason_code: string;
  evidence_ids: string[] | null;
}

export interface BusinessAgentEvidence {
  evidence_id: string;
  semantic_type: string;
  value: string;
  unit: string | null;
  currency: string | null;
  verification: string;
  anchor: {
    id: string;
    document_id: string;
    parsed_document_id: string;
    quote: string;
    status: string;
    start: number;
    end: number;
    reason: string | null;
    page: number | null;
    element_id: string | null;
  };
}

export interface BusinessAgentObservation {
  round: number;
  call_id: string;
  gap_id: string;
  tool_id: string;
  reported_relation: string;
  reason_code: string | null;
  summary: string;
  claim_status: string;
  fact_ids: string[] | null;
  located_evidence_ids: string[] | null;
  truncated: boolean;
}

export interface BusinessAgentReview {
  attempt_id: string | null;
  status: string | null;
  rulepack: { id: string; version: string } | null;
  scope: BusinessAgentScope[] | null;
  domains: BusinessAgentDomain[] | null;
  gaps: BusinessAgentGap[] | null;
  evidence: BusinessAgentEvidence[] | null;
  unresolved_gap_ids: string[] | null;
  observations: BusinessAgentObservation[] | null;
}

const { invalid, record, text, integer, nullableText, nullableArray } =
  responseReader((path) => `业务审查响应中的 ${path} 无法识别`);

function scope(value: unknown, path: string): BusinessAgentScope {
  const item = record(value, path, ["domain", "capability_ids", "source_ids"]);
  return {
    domain: text(item.domain, `${path}.domain`),
    capability_ids: nullableArray(item.capability_ids, `${path}.capability_ids`, text),
    source_ids: nullableArray(item.source_ids, `${path}.source_ids`, text),
  };
}

function capability(value: unknown, path: string): BusinessAgentCapability {
  const item = record(value, path, ["capability_id", "mandatory", "status", "relation", "reason_code", "evidence_ids", "canonical_evaluation_ids"]);
  return {
    capability_id: text(item.capability_id, `${path}.capability_id`),
    mandatory: typeof item.mandatory === "boolean" ? item.mandatory : invalid(`${path}.mandatory`),
    status: text(item.status, `${path}.status`),
    relation: nullableText(item.relation, `${path}.relation`),
    reason_code: text(item.reason_code, `${path}.reason_code`),
    evidence_ids: nullableArray(item.evidence_ids, `${path}.evidence_ids`, text),
    canonical_evaluation_ids: nullableArray(item.canonical_evaluation_ids, `${path}.canonical_evaluation_ids`, text),
  };
}

function domain(value: unknown, path: string): BusinessAgentDomain {
  const item = record(value, path, ["domain", "status", "capabilities"]);
  return {
    domain: text(item.domain, `${path}.domain`),
    status: text(item.status, `${path}.status`),
    capabilities: nullableArray(item.capabilities, `${path}.capabilities`, capability),
  };
}

function gap(value: unknown, path: string): BusinessAgentGap {
  const item = record(value, path, ["gap_id", "domain", "capability_id", "mandatory", "status", "reason_code", "evidence_ids"]);
  return {
    gap_id: text(item.gap_id, `${path}.gap_id`),
    domain: text(item.domain, `${path}.domain`),
    capability_id: text(item.capability_id, `${path}.capability_id`),
    mandatory: typeof item.mandatory === "boolean" ? item.mandatory : invalid(`${path}.mandatory`),
    status: text(item.status, `${path}.status`),
    reason_code: text(item.reason_code, `${path}.reason_code`),
    evidence_ids: nullableArray(item.evidence_ids, `${path}.evidence_ids`, text),
  };
}

function evidence(value: unknown, path: string): BusinessAgentEvidence {
  const item = record(value, path, ["evidence_id", "semantic_type", "value", "unit", "currency", "verification", "anchor"]);
  const anchor = record(item.anchor, `${path}.anchor`, ["id", "document_id", "parsed_document_id", "quote", "status", "start", "end", "reason", "page", "element_id"]);
  return {
    evidence_id: text(item.evidence_id, `${path}.evidence_id`),
    semantic_type: text(item.semantic_type, `${path}.semantic_type`),
    value: text(item.value, `${path}.value`),
    unit: nullableText(item.unit, `${path}.unit`),
    currency: nullableText(item.currency, `${path}.currency`),
    verification: text(item.verification, `${path}.verification`),
    anchor: {
      id: text(anchor.id, `${path}.anchor.id`),
      document_id: text(anchor.document_id, `${path}.anchor.document_id`),
      parsed_document_id: text(anchor.parsed_document_id, `${path}.anchor.parsed_document_id`),
      quote: text(anchor.quote, `${path}.anchor.quote`),
      status: text(anchor.status, `${path}.anchor.status`),
      start: integer(anchor.start, `${path}.anchor.start`),
      end: integer(anchor.end, `${path}.anchor.end`),
      reason: nullableText(anchor.reason, `${path}.anchor.reason`),
      page: anchor.page === null ? null : integer(anchor.page, `${path}.anchor.page`),
      element_id: nullableText(anchor.element_id, `${path}.anchor.element_id`),
    },
  };
}

function observation(value: unknown, path: string): BusinessAgentObservation {
  const item = record(value, path, [
    "round",
    "call_id",
    "gap_id",
    "tool_id",
    "reported_relation",
    "reason_code",
    "summary",
    "claim_status",
    "fact_ids",
    "located_evidence_ids",
    "truncated",
  ]);
  return {
    round: integer(item.round, `${path}.round`, 0),
    call_id: text(item.call_id, `${path}.call_id`),
    gap_id: text(item.gap_id, `${path}.gap_id`),
    tool_id: text(item.tool_id, `${path}.tool_id`),
    reported_relation: text(item.reported_relation, `${path}.reported_relation`),
    reason_code: nullableText(item.reason_code, `${path}.reason_code`),
    summary: text(item.summary, `${path}.summary`),
    claim_status: text(item.claim_status, `${path}.claim_status`),
    fact_ids: nullableArray(item.fact_ids, `${path}.fact_ids`, text),
    located_evidence_ids: nullableArray(
      item.located_evidence_ids,
      `${path}.located_evidence_ids`,
      text,
    ),
    truncated: typeof item.truncated === "boolean" ? item.truncated : invalid(`${path}.truncated`),
  };
}

export function parseBusinessAgentReview(value: unknown): BusinessAgentReview | null {
  if (value === null || value === undefined) return null;
  const item = record(value, "agent_review", ["attempt_id", "status", "rulepack", "scope", "domains", "gaps", "evidence", "unresolved_gap_ids", "observations"]);
  const pack = item.rulepack === null ? null : record(item.rulepack, "agent_review.rulepack", ["id", "version"]);
  return {
    attempt_id: nullableText(item.attempt_id, "agent_review.attempt_id"),
    status: nullableText(item.status, "agent_review.status"),
    rulepack: pack === null ? null : {
      id: text(pack.id, "agent_review.rulepack.id"),
      version: text(pack.version, "agent_review.rulepack.version"),
    },
    scope: nullableArray(item.scope, "agent_review.scope", scope),
    domains: nullableArray(item.domains, "agent_review.domains", domain),
    gaps: nullableArray(item.gaps, "agent_review.gaps", gap),
    evidence: nullableArray(item.evidence, "agent_review.evidence", evidence),
    unresolved_gap_ids: nullableArray(item.unresolved_gap_ids, "agent_review.unresolved_gap_ids", text),
    observations: nullableArray(
      item.observations,
      "agent_review.observations",
      observation,
    ),
  };
}
