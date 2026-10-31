import { paths } from "./paths";
import { requestJson } from "./http";
import { responseReader } from "./respond";

export interface FrozenLegalApplicabilityCondition {
  field: string;
  one_of: string[];
  outcome: "satisfied" | "missing" | "conflicting" | "unknown";
  missing_reason?: string;
}

export interface FrozenLegalDependencyGap {
  dimension?: string;
  kind: string;
  reason: string;
}

export interface FrozenLegalArticle {
  content_sha256: string;
  source_id: string;
  source_snapshot_id: string;
  norm_title: string;
  norm_version: string;
  article_number: number;
  article_label: string;
  document_type: string;
  canonical_full_article_body: string;
  article_text_sha256: string;
  quote_span: { start: number; end: number; quote: string };
  effective_from: string;
  effective_until?: string;
  source_provenance: {
    provenance: string;
    publisher: string;
    issuer: string;
    official_url: string;
    raw_response_sha256: string;
    captured_at: string;
  };
  status_provenance: {
    observed_status: string;
    registry_url: string;
    observed_at: string;
    response_sha256: string;
  };
}

export interface FrozenLegalAuthority {
  citation_id: string;
  rule_id: string;
  authority_source_id: string;
  authority_source_version: string;
  rulepack_id: string;
  rulepack_version: string;
  rulepack_content_sha256: string;
  outcome: "applicable" | "not_applicable" | "unknown";
  outcome_reason?: string;
  conditions: FrozenLegalApplicabilityCondition[];
  missing_dependencies: FrozenLegalDependencyGap[];
  legal_review_status: "NOT_RUN";
  reviewer: string | null;
  legacy_rule_gap?: boolean;
  article?: FrozenLegalArticle;
}

export interface FrozenLegalReviewProjection {
  run_id: string;
  attempt_id: string;
  evidence_cutoff_at: string;
  evidence_cutoff_mode: "acceptance_time" | "explicit";
  explicit_evidence_cutoff_at?: string;
  authorities: FrozenLegalAuthority[];
}

const { record, object, text, integer, array, nullableText, bool } = responseReader(
  (path) => `冻结法律依据响应中的 ${path} 无法识别`,
);
const digest = (value: unknown, path: string) => {
  const result = text(value, path);
  if (!/^[a-f0-9]{64}$/.test(result)) throw new Error(`冻结法律依据响应中的 ${path} 不是 SHA-256`);
  return result;
};
const date = (value: unknown, path: string) => {
  const result = text(value, path);
  const parsed = new Date(`${result}T00:00:00Z`);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(result) || Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== result) {
    throw new Error(`冻结法律依据响应中的 ${path} 不是有效日期`);
  }
  return result;
};
const instant = (value: unknown, path: string) => {
  const result = text(value, path);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(result) || Number.isNaN(Date.parse(result))) {
    throw new Error(`冻结法律依据响应中的 ${path} 不是 UTC 时间`);
  }
  return result;
};

function parseCondition(value: unknown, path: string): FrozenLegalApplicabilityCondition {
  const row = record(value, path, ["field", "one_of", "outcome", "fact", "missing_reason"], ["field", "one_of", "outcome"]);
  const outcome = text(row.outcome, `${path}.outcome`);
  if (!( ["satisfied", "missing", "conflicting", "unknown"] as string[]).includes(outcome)) throw new Error(`冻结法律依据响应中的 ${path}.outcome 无效`);
  if (row.fact !== undefined && row.fact !== null) object(row.fact, `${path}.fact`);
  return {
    field: text(row.field, `${path}.field`),
    one_of: array(row.one_of, `${path}.one_of`, (entry, itemPath) => text(entry, itemPath)),
    outcome: outcome as FrozenLegalApplicabilityCondition["outcome"],
    ...(row.missing_reason === undefined ? {} : { missing_reason: text(row.missing_reason, `${path}.missing_reason`) }),
  };
}

function parseGap(value: unknown, path: string): FrozenLegalDependencyGap {
  const row = record(value, path, ["dimension", "kind", "reason"], ["kind", "reason"]);
  return {
    ...(row.dimension === undefined ? {} : { dimension: text(row.dimension, `${path}.dimension`) }),
    kind: text(row.kind, `${path}.kind`),
    reason: text(row.reason, `${path}.reason`),
  };
}

function parseArticle(value: unknown, path: string): FrozenLegalArticle {
  const row = record(value, path, ["ref", "content_sha256", "source_id", "source_snapshot_id", "norm_title", "norm_version", "article_number", "article_label", "document_type", "canonical_full_article_body", "article_text_sha256", "quote_span", "effective_from", "effective_until", "source_provenance", "status_provenance"], ["ref", "content_sha256", "source_id", "source_snapshot_id", "norm_title", "norm_version", "article_number", "article_label", "document_type", "canonical_full_article_body", "article_text_sha256", "quote_span", "effective_from", "source_provenance", "status_provenance"]);
  const source = record(row.source_provenance, `${path}.source_provenance`, ["source_capture_snapshot_id", "provenance", "publisher", "issuer", "official_url", "raw_response_sha256", "extracted_full_text_sha256", "extractor_id", "extractor_version", "canonicalization_version", "captured_at", "response_status"]);
  const status = record(row.status_provenance, `${path}.status_provenance`, ["status_observation_capture_snapshot_id", "capture_raw_response_sha256", "status_observation_id", "observed_status", "status_registry_url", "observed_at", "response_sha256", "response_status"]);
  const quote = record(row.quote_span, `${path}.quote_span`, ["start", "end", "quote"]);
  if (integer(quote.end, `${path}.quote_span.end`, 0) < integer(quote.start, `${path}.quote_span.start`, 0)) throw new Error(`冻结法律依据响应中的 ${path}.quote_span 范围无效`);
  const ref = record(row.ref, `${path}.ref`, ["knowledge_id", "version_id", "content_sha256"]);
  if (digest(ref.content_sha256, `${path}.ref.content_sha256`) !== digest(row.content_sha256, `${path}.content_sha256`)) {
    throw new Error(`冻结法律依据响应中的 ${path}.ref 与条文摘要不一致`);
  }
  const officialUrl = text(source.official_url, `${path}.source_provenance.official_url`);
  if (!/^https?:\/\//.test(officialUrl)) throw new Error(`冻结法律依据响应中的 ${path}.source_provenance.official_url 无效`);
  return {
    content_sha256: digest(row.content_sha256, `${path}.content_sha256`),
    source_id: text(row.source_id, `${path}.source_id`),
    source_snapshot_id: text(row.source_snapshot_id, `${path}.source_snapshot_id`),
    norm_title: text(row.norm_title, `${path}.norm_title`),
    norm_version: text(row.norm_version, `${path}.norm_version`),
    article_number: integer(row.article_number, `${path}.article_number`, 1),
    article_label: text(row.article_label, `${path}.article_label`),
    document_type: text(row.document_type, `${path}.document_type`),
    canonical_full_article_body: text(row.canonical_full_article_body, `${path}.canonical_full_article_body`),
    article_text_sha256: digest(row.article_text_sha256, `${path}.article_text_sha256`),
    effective_from: date(row.effective_from, `${path}.effective_from`),
    ...(row.effective_until === undefined ? {} : { effective_until: text(row.effective_until, `${path}.effective_until`) }),
    quote_span: {
      start: integer(quote.start, `${path}.quote_span.start`, 0),
      end: integer(quote.end, `${path}.quote_span.end`, 0),
      quote: text(quote.quote, `${path}.quote_span.quote`),
    },
    source_provenance: {
      provenance: text(source.provenance, `${path}.source_provenance.provenance`),
      publisher: text(source.publisher, `${path}.source_provenance.publisher`),
      issuer: text(source.issuer, `${path}.source_provenance.issuer`),
      official_url: officialUrl,
      raw_response_sha256: digest(source.raw_response_sha256, `${path}.source_provenance.raw_response_sha256`),
      captured_at: instant(source.captured_at, `${path}.source_provenance.captured_at`),
    },
    status_provenance: {
      observed_status: text(status.observed_status, `${path}.status_provenance.observed_status`),
      registry_url: text(status.status_registry_url, `${path}.status_provenance.status_registry_url`),
      observed_at: instant(status.observed_at, `${path}.status_provenance.observed_at`),
      response_sha256: digest(status.response_sha256, `${path}.status_provenance.response_sha256`),
    },
  };
}

export function parseFrozenLegalAuthority(value: unknown, path = "authority"): FrozenLegalAuthority {
  const row = record(value, path, ["citation_id", "rule_id", "authority_source_id", "authority_source_version", "rulepack_id", "rulepack_version", "rulepack_content_sha256", "exact_reference", "article", "outcome", "outcome_reason", "conditions", "selected_fact_versions", "missing_dependencies", "legacy_rule_gap", "legal_review_status", "reviewer"], ["citation_id", "rule_id", "authority_source_id", "authority_source_version", "rulepack_id", "rulepack_version", "rulepack_content_sha256", "outcome", "conditions", "selected_fact_versions", "missing_dependencies", "legal_review_status", "reviewer"]);
  const citationId = digest(row.citation_id, `${path}.citation_id`);
  const outcome = text(row.outcome, `${path}.outcome`);
  if (!( ["applicable", "not_applicable", "unknown"] as string[]).includes(outcome)) throw new Error(`冻结法律依据响应中的 ${path}.outcome 无效`);
  const status = text(row.legal_review_status, `${path}.legal_review_status`);
  if (status !== "NOT_RUN") throw new Error(`冻结法律依据响应中的 ${path}.legal_review_status 不得伪称已人工审核`);
  const article = row.article === undefined || row.article === null ? undefined : parseArticle(row.article, `${path}.article`);
  const legacyGap = row.legacy_rule_gap === undefined ? false : bool(row.legacy_rule_gap, `${path}.legacy_rule_gap`);
  if (!article && !legacyGap) throw new Error(`冻结法律依据响应中的 ${path} 缺少精确条文且未标记为历史缺口`);
  if (row.exact_reference !== undefined && row.exact_reference !== null) {
    const ref = record(row.exact_reference, `${path}.exact_reference`, ["authority_source_id", "knowledge_id", "version_id", "content_sha256", "source_id", "source_snapshot_id", "norm_title", "norm_version", "article_number", "document_type", "source_sha256", "article_text_sha256", "effective_from", "effective_until", "quote_span", "applicability"]);
    digest(ref.content_sha256, `${path}.exact_reference.content_sha256`);
    digest(ref.source_sha256, `${path}.exact_reference.source_sha256`);
    digest(ref.article_text_sha256, `${path}.exact_reference.article_text_sha256`);
    integer(ref.article_number, `${path}.exact_reference.article_number`, 1);
    const span = record(ref.quote_span, `${path}.exact_reference.quote_span`, ["start", "end", "quote"]);
    const start = integer(span.start, `${path}.exact_reference.quote_span.start`, 0);
    const end = integer(span.end, `${path}.exact_reference.quote_span.end`, 0);
    if (end < start) throw new Error(`冻结法律依据响应中的 ${path}.exact_reference.quote_span 无效`);
    text(span.quote, `${path}.exact_reference.quote_span.quote`);
    const applicability = record(ref.applicability, `${path}.exact_reference.applicability`, ["all_of"]);
    array(applicability.all_of, `${path}.exact_reference.applicability.all_of`, (item, itemPath) => {
      const condition = record(item, itemPath, ["field", "one_of"]);
      return { field: text(condition.field, `${itemPath}.field`), one_of: array(condition.one_of, `${itemPath}.one_of`, (entry, entryPath) => text(entry, entryPath)) };
    });
    for (const key of ["authority_source_id", "knowledge_id", "version_id", "source_id", "source_snapshot_id", "norm_title", "norm_version", "document_type", "effective_from", "effective_until"]) text(ref[key], `${path}.exact_reference.${key}`);
  }
  array(row.selected_fact_versions, `${path}.selected_fact_versions`, (item, itemPath) => object(item, itemPath));
  if (row.reviewer !== null) throw new Error(`冻结法律依据响应中的 ${path}.reviewer 必须为空，因为状态是 NOT_RUN`);
  return {
    citation_id: citationId,
    rule_id: text(row.rule_id, `${path}.rule_id`),
    authority_source_id: text(row.authority_source_id, `${path}.authority_source_id`),
    authority_source_version: text(row.authority_source_version, `${path}.authority_source_version`),
    rulepack_id: text(row.rulepack_id, `${path}.rulepack_id`),
    rulepack_version: text(row.rulepack_version, `${path}.rulepack_version`),
    rulepack_content_sha256: digest(row.rulepack_content_sha256, `${path}.rulepack_content_sha256`),
    outcome: outcome as FrozenLegalAuthority["outcome"],
    ...(row.outcome_reason === undefined ? {} : { outcome_reason: text(row.outcome_reason, `${path}.outcome_reason`) }),
    conditions: array(row.conditions, `${path}.conditions`, parseCondition),
    missing_dependencies: array(row.missing_dependencies, `${path}.missing_dependencies`, parseGap),
    legal_review_status: "NOT_RUN",
    reviewer: nullableText(row.reviewer, `${path}.reviewer`),
    ...(row.legacy_rule_gap === undefined ? {} : { legacy_rule_gap: legacyGap }),
    ...(article ? { article } : {}),
  };
}

export function parseFrozenLegalReviewProjection(value: unknown): FrozenLegalReviewProjection {
  const row = record(value, "legal_authorities", ["run_id", "attempt_id", "evidence_cutoff_at", "evidence_cutoff_mode", "explicit_evidence_cutoff_at", "authorities"], ["run_id", "attempt_id", "evidence_cutoff_at", "evidence_cutoff_mode", "authorities"]);
  const mode = text(row.evidence_cutoff_mode, "legal_authorities.evidence_cutoff_mode");
  if (mode !== "acceptance_time" && mode !== "explicit") throw new Error("冻结法律依据响应中的 evidence_cutoff_mode 无效");
  const explicitAt = row.explicit_evidence_cutoff_at === undefined
    ? undefined
    : instant(row.explicit_evidence_cutoff_at, "legal_authorities.explicit_evidence_cutoff_at");
  if (mode === "explicit" && explicitAt === undefined) throw new Error("显式法律证据截止时点缺失");
  if (mode === "acceptance_time" && explicitAt !== undefined) throw new Error("受理时点快照不能包含显式截止时点");
  return {
    run_id: text(row.run_id, "legal_authorities.run_id"),
    attempt_id: text(row.attempt_id, "legal_authorities.attempt_id"),
    evidence_cutoff_at: instant(row.evidence_cutoff_at, "legal_authorities.evidence_cutoff_at"),
    evidence_cutoff_mode: mode,
    ...(explicitAt === undefined ? {} : { explicit_evidence_cutoff_at: explicitAt }),
    authorities: array(row.authorities, "legal_authorities.authorities", parseFrozenLegalAuthority),
  };
}

export function getFrozenLegalAuthority(caseId: string, runId: string, attemptId: string, citationId: string, signal?: AbortSignal) {
  return requestJson(
    paths.frozenLegalCitation(caseId, runId, attemptId, citationId),
    { method: "GET", signal },
    (value) => parseFrozenLegalAuthority(value, "authority"),
  );
}
