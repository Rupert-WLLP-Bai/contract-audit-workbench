import type {
  ArchiveRecord,
  AuditEvent,
  BusinessAuditEvent,
  GenerationRecord,
  Remediation,
  ReportRecord,
  Template,
  ActivityItem,
  AcceptedReview,
  Actor,
  BoundingBox,
  CaseDetail,
  CaseSummary,
  ClauseNode,
  DocumentVersion,
  EvidenceAnchor,
  Execution,
  ExecutionAttempt,
  ExecutionEvent,
  ExecutionOperationalMetadata,
  ExecutionCallUsage,
  ExecutionUsage,
  Finding,
  FindingVerification,
  FindingCounts,
  HumanDecision,
  LLMEndpointView,
  LLMProbeResult,
  ModelCallTrace,
  ReviewDocument,
  ReviewRun,
  ReviewRunSummary,
  ReviewAttempts,
  ReviewDiagnostics,
  ReviewWorkspace,
  RunSnapshot,
  ConstraintEvaluation,
  RevisionComparison,
  OperatorAuditSettings,
  OperatorConnectorInfo,
  SettingsUser,
  SettingsView,
  TextOffset,
  ParsedDocumentMetadata,
  FindingSeveritySource,
  MaterialProjection,
  MaterialSelectionProjection,
  MaterialVersionProjection,
  UploadDocumentResult,
} from "./types";
import { parseFrozenLegalReviewProjection } from "./legal-citations";
import { isRecord } from "./guards";
import { invalidResponse } from "./respond";
import { parseAgentReview } from "./agent-review";
import { parseBusinessAgentReview } from "./business-agent-review";
function nonNegativeIntegerOrNull(value: unknown): number | null {
  const result = numberOrNull(value);
  return result !== null && Number.isInteger(result) && result >= 0
    ? result
    : null;
}

function requiredCount(record: Record<string, unknown>, key: string): number {
  const result = nonNegativeIntegerOrNull(record[key]);
  if (result === null) fail(`响应缺少有效的 ${key}`);
  return result;
}

function nullableString(
  record: Record<string, unknown>,
  key: string,
): string | null {
  const value = record[key];
  return typeof value === "string" ? value : null;
}

function stringArrayValue(value: unknown, key: string): string[] {
  if (!Array.isArray(value) || !value.every((item) => typeof item === "string"))
    fail(`${key}不是字符串数组`);
  return value as string[];
}

function stringArrayOrEmpty(
  record: Record<string, unknown>,
  key: string,
): string[] {
  const value = record[key];
  return value === undefined || value === null
    ? []
    : stringArrayValue(value, key);
}

function requiredBoolean(
  record: Record<string, unknown>,
  key: string,
): boolean {
  if (typeof record[key] !== "boolean") fail(`响应缺少有效的 ${key}`);
  return record[key] as boolean;
}

function requiredStringArray(
  record: Record<string, unknown>,
  key: string,
): string[] {
  return stringArrayValue(record[key], key);
}

function requiredNullableString(
  record: Record<string, unknown>,
  key: string,
): string | null {
  if (!Object.hasOwn(record, key)) fail(`响应缺少 ${key}`);
  const value = record[key];
  if (value !== null && typeof value !== "string")
    fail(`${key}不是字符串或null`);
  return value as string | null;
}

function optionalNullableString(
  record: Record<string, unknown>,
  key: string,
): string | null {
  if (!(key in record) || record[key] === null) return null;
  if (typeof record[key] !== "string") fail(`${key}不是字符串或null`);
  return record[key] as string;
}

function requiredPositiveInteger(
  record: Record<string, unknown>,
  key: string,
): number {
  const value = requiredCount(record, key);
  if (value < 1) fail(`${key}必须大于0`);
  return value;
}

function parseCallUsage(value: unknown): ExecutionCallUsage | null {
  if (value === null || value === undefined) return null;
  const record = recordOf(value, "event usage");
  return {
    source: requiredString(record, "source"),
    input_tokens: nonNegativeIntegerOrNull(record.input_tokens),
    output_tokens: nonNegativeIntegerOrNull(record.output_tokens),
    total_tokens: nonNegativeIntegerOrNull(record.total_tokens),
  };
}

function parseUsage(value: unknown): ExecutionUsage | null {
  if (value === null || value === undefined) return null;
  const record = recordOf(value, "usage");
  const subtotals = recordOf(record.known_subtotals, "known_subtotals");
  if (
    !Array.isArray(record.sources) ||
    !record.sources.every((source) => typeof source === "string")
  ) {
    fail("usage.sources不是字符串数组");
  }
  if (record.cost !== null) fail("usage.cost必须为null");
  return {
    input_tokens: nonNegativeIntegerOrNull(record.input_tokens),
    output_tokens: nonNegativeIntegerOrNull(record.output_tokens),
    total_tokens: nonNegativeIntegerOrNull(record.total_tokens),
    calls: requiredCount(record, "calls"),
    unknown_calls: requiredCount(record, "unknown_calls"),
    sources: record.sources as string[],
    known_subtotals: {
      input_tokens: nonNegativeIntegerOrNull(subtotals.input_tokens),
      output_tokens: nonNegativeIntegerOrNull(subtotals.output_tokens),
      total_tokens: nonNegativeIntegerOrNull(subtotals.total_tokens),
    },
    cost: null,
  };
}

function parseExecutionAttempt(value: unknown): ExecutionAttempt {
  const record = recordOf(value, "attempt");
  return {
    id: requiredString(record, "id"),
    run_id: requiredString(record, "run_id"),
    ordinal: requiredCount(record, "ordinal"),
    parsed_document_ids: stringArrayOrEmpty(record, "parsed_document_ids"),
    retry_of_attempt_id: nullableString(record, "retry_of_attempt_id"),
    state: requiredString(record, "state"),
    accepted_at: nullableString(record, "accepted_at"),
    started_at: nullableString(record, "started_at"),
    finished_at: nullableString(record, "finished_at"),
    input_hash: nullableString(record, "input_hash"),
    output_hash: nullableString(record, "output_hash"),
    error_code: nullableString(record, "error_code"),
    history_kind: requiredString(record, "history_kind"),
  };
}

function parseExecutionEvent(value: unknown): ExecutionEvent {
  const record = recordOf(value, "execution event");
  return {
    seq: requiredCount(record, "seq"),
    attempt_id: requiredString(record, "attempt_id"),
    span_id: nullableString(record, "span_id"),
    parent_span_id: nullableString(record, "parent_span_id"),
    node_kind: requiredString(record, "node_kind"),
    phase: requiredString(record, "phase"),
    event_type: requiredString(record, "event_type"),
    at: nullableString(record, "at"),
    duration_ms: nonNegativeIntegerOrNull(record.duration_ms),
    status: nullableString(record, "status"),
    error_code: nullableString(record, "error_code"),
    model_ref: nullableString(record, "model_ref"),
    usage: parseCallUsage(record.usage),
    trace: parseModelCallTrace(record.trace),
  };
}

/**
 * A malformed trace never hides the rest of the execution record: the
 * trajectory renders "not recorded" instead of failing the workspace.
 */
function parseModelCallTrace(value: unknown): ModelCallTrace | null {
  if (!isRecord(value)) return null;
  const messages = Array.isArray(value.request_messages)
    ? value.request_messages.flatMap((entry) => {
        if (!isRecord(entry) || typeof entry.role !== "string" || typeof entry.content !== "string")
          return [];
        return [{
          role: entry.role,
          content: entry.content,
          truncated: entry.truncated === true,
        }];
      })
    : [];
  return {
    requested_model: optionalString(value, "requested_model") ?? "",
    reported_model: optionalString(value, "reported_model") ?? "",
    status: optionalString(value, "status") ?? "",
    error_code: optionalString(value, "error_code") ?? "",
    started_at: optionalString(value, "started_at") ?? "",
    finished_at: optionalString(value, "finished_at") ?? "",
    request_sha256: optionalString(value, "request_sha256") ?? "",
    request_bytes: nonNegativeIntegerOrNull(value.request_bytes) ?? 0,
    request_messages: messages,
    request_truncated: value.request_truncated === true,
    response_sha256: optionalString(value, "response_sha256") ?? "",
    response_bytes: nonNegativeIntegerOrNull(value.response_bytes) ?? 0,
    response_text: optionalString(value, "response_text") ?? "",
    response_truncated: value.response_truncated === true,
  };
}

function fail(message: string): never {
  return invalidResponse(message, 500);
}

function recordOf(value: unknown, name: string): Record<string, unknown> {
  if (!isRecord(value)) fail(`${name}不是对象`);
  return value;
}

function requiredString(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  if (typeof value !== "string" || value.trim() === "") fail(`响应缺少 ${key}`);
  return value;
}
function requiredStringValue(
  record: Record<string, unknown>,
  key: string,
): string {
  const value = record[key];
  if (typeof value !== "string") fail(`响应缺少 ${key}`);
  return value;
}

function optionalString(
  record: Record<string, unknown>,
  key: string,
): string | null {
  const value = record[key];
  if (typeof value !== "string") return null;
  return value;
}

function numberOrNull(value: unknown): number | null {
  if (typeof value !== "number" || !Number.isFinite(value)) return null;
  return value;
}

function parseOffset(value: unknown): TextOffset | null {
  if (!isRecord(value)) return null;
  const start = numberOrNull(value.start);
  const end = numberOrNull(value.end);
  if (
    start === null ||
    end === null ||
    !Number.isInteger(start) ||
    !Number.isInteger(end) ||
    end <= start
  ) {
    return null;
  }
  return { start, end };
}

function numberRecord<K extends string>(
  value: unknown,
  keys: readonly K[],
): Record<K, number> | null {
  if (!isRecord(value)) return null;
  const result = {} as Record<K, number>;
  for (const key of keys) {
    const parsed = numberOrNull(value[key]);
    if (parsed === null) return null;
    result[key] = parsed;
  }
  return result;
}

function parseBox(value: unknown): BoundingBox | null {
  return numberRecord(value, ["x", "y", "width", "height"]);
}

function parseRunSummary(value: unknown): ReviewRunSummary | null {
  if (value === null || value === undefined) return null;
  const record = recordOf(value, "latest_run");
  return {
    id: requiredString(record, "id"),
    state: optionalString(record, "state") ?? "unknown",
    updated_at: optionalString(record, "updated_at") ?? "",
  };
}

export function parseCaseSummary(value: unknown): CaseSummary {
  const record = recordOf(value, "case");
  const documentCount = numberOrNull(record.document_count);
  const pending = numberOrNull(record.pending_decisions);
  if (documentCount === null || pending === null) fail("案件列表缺少数量");
  return {
    id: requiredString(record, "id"),
    title: optionalString(record, "title") ?? "未命名案件",
    contract_type: optionalString(record, "contract_type") ?? "",
    document_count: documentCount,
    pending_decisions: pending,
    latest_run: parseRunSummary(record.latest_run),
    updated_at: optionalString(record, "updated_at") ?? "",
    business_line: optionalString(record, "business_line") ?? undefined,
    scenario: optionalString(record, "scenario") ?? undefined,
    business_status: optionalString(record, "business_status") ?? undefined,
    archived_at: nullableString(record, "archived_at"),
  };
}

function items<T>(
  value: unknown,
  recordName: string,
  listName: string,
  parse: (entry: unknown) => T,
): T[] {
  const record = recordOf(value, recordName);
  if (!Array.isArray(record.items)) fail(`${listName}缺少 items`);
  return record.items.map(parse);
}

function requiredStringRecord<K extends string>(
  value: unknown,
  recordName: string,
  keys: readonly K[],
): Record<"id" | K, string> {
  const record = recordOf(value, recordName);
  const result = {
    id: requiredString(record, "id"),
  } as Record<"id" | K, string>;
  for (const key of keys) result[key] = requiredString(record, key);
  return result;
}

function stringDefaults<K extends string>(
  record: Record<string, unknown>,
  defaults: Record<K, string>,
): Record<K, string> {
  const result = {} as Record<K, string>;
  for (const key of Object.keys(defaults) as K[])
    result[key] = optionalString(record, key) ?? defaults[key];
  return result;
}

export function parseCaseList(value: unknown): CaseSummary[] {
  return items(value, "案件列表", "案件列表", parseCaseSummary);
}

function nullableValueString(
  record: Record<string, unknown>,
  key: string,
): string | null {
  const value = nullableString(record, key);
  return value !== null && value.trim() !== "" ? value : null;
}

function parseParsedDocument(value: unknown): ParsedDocumentMetadata | null {
  if (value === null || value === undefined) return null;
  const record = recordOf(value, "parsed_document");
  const status = record.status;
  if (status !== "ready" && status !== "failed") fail("解析产物状态无效");
  return {
    id: requiredString(record, "id"),
    document_id: requiredString(record, "document_id"),
    original_digest: requiredString(record, "original_digest"),
    parser_id: requiredString(record, "parser_id"),
    parser_version: requiredString(record, "parser_version"),
    config_hash: requiredString(record, "config_hash"),
    normalization_version: requiredString(record, "normalization_version"),
    normalized_digest: nullableValueString(record, "normalized_digest"),
    ir_digest: nullableValueString(record, "ir_digest"),
    artifact_digest: requiredString(record, "artifact_digest"),
    created_at: requiredString(record, "created_at"),
    status,
  };
}

function parseDocument(value: unknown): DocumentVersion {
  const record = recordOf(value, "document");
  const size = numberOrNull(record.size);
  return {
    id: requiredString(record, "id"),
    role: optionalString(record, "role") ?? "",
    filename: optionalString(record, "filename") ?? "未命名文件",
    media_type: optionalString(record, "media_type") ?? "",
    digest: optionalString(record, "digest") ?? "",
    size: size ?? -1,
    parse_status: optionalString(record, "parse_status") ?? "unknown",
    parse_error: optionalString(record, "parse_error"),
    created_at: optionalString(record, "created_at") ?? "",
    parsed_document: parseParsedDocument(record.parsed_document),
    superseded: record.superseded === true,
  };
}

function recordList<T>(
  value: unknown,
  parse: (entry: unknown) => T,
): T[] {
  return Array.isArray(value) ? value.map(parse) : [];
}

function requiredRecordList<T>(
  value: unknown,
  field: string,
  parse: (entry: unknown) => T,
): T[] {
  if (!Array.isArray(value)) fail(`${field}不是数组`);
  return value.map(parse);
}

function stringList(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];
}

function parseActivity(value: unknown): ActivityItem | null {
  if (!isRecord(value)) return null;
  const id = optionalString(value, "id");
  const summary = optionalString(value, "summary");
  if (id === null || summary === null) return null;
  return { id, at: optionalString(value, "at") ?? "", summary };
}

function parseActivityItems(value: unknown): ActivityItem[] {
  if (!Array.isArray(value)) return [];
  return value
    .map(parseActivity)
    .filter((item): item is ActivityItem => item !== null);
}

function parseCounts(value: unknown): FindingCounts | null {
  return numberRecord(value, ["total", "undecided", "conflict", "insufficient"]);
}

function hasCaseRecordArrays(record: Record<string, unknown>): boolean {
  return (
    Array.isArray(record.members) &&
    Array.isArray(record.generations) &&
    Array.isArray(record.remediations) &&
    Array.isArray(record.reports) &&
    Array.isArray(record.archives)
  );
}

function parseMembers(value: unknown[]): CaseDetail["members"] {
  return value.map((entry) => {
    const member = recordOf(entry, "案件成员");
    return {
      id: requiredStringValue(member, "id"),
      display_name: requiredStringValue(member, "display_name"),
      role: requiredStringValue(member, "role"),
      tenant_id: requiredStringValue(member, "tenant_id"),
      org_id: requiredStringValue(member, "org_id"),
    };
  });
}

const materialPurposes = new Set([
  "primary_contract",
  "supplement",
  "procurement",
  "award",
  "acceptance",
  "invoice",
  "correspondence",
  "other",
]);

function parseMaterialProjection(value: unknown): MaterialProjection {
  const record = recordOf(value, "material");
  const role = requiredString(record, "role");
  const purpose = requiredString(record, "purpose");
  const legacyStatus = requiredString(record, "legacy_status");
  if (
    role !== "procurement" &&
    role !== "award" &&
    role !== "draft_contract" &&
    role !== "attachment"
  ) fail("material.role 无效");
  if (!materialPurposes.has(purpose)) fail("material.purpose 无效");
  if (
    legacyStatus !== "native" &&
    legacyStatus !== "pending_confirmation" &&
    legacyStatus !== "confirmed"
  ) fail("material.legacy_status 无效");
  return {
    material_id: requiredString(record, "material_id"),
    role,
    purpose: purpose as MaterialProjection["purpose"],
    legacy_status: legacyStatus,
    legacy_selected: requiredBoolean(record, "legacy_selected"),
    selected: requiredBoolean(record, "selected"),
    current_document_id: requiredString(record, "current_document_id"),
    current_version: requiredPositiveInteger(record, "current_version"),
  };
}

function parseMaterialVersionProjection(value: unknown): MaterialVersionProjection {
  const record = recordOf(value, "material version");
  return {
    material_id: requiredString(record, "material_id"),
    version: requiredPositiveInteger(record, "version"),
    document_id: requiredString(record, "document_id"),
    previous_document_id: optionalNullableString(record, "previous_document_id"),
    created_at: requiredString(record, "created_at"),
  };
}

export function parseMaterialSelectionProjection(value: unknown): MaterialSelectionProjection {
  const record = recordOf(value, "material_selection");
  const status = requiredString(record, "status");
  if (
    status !== "pending_confirmation" &&
    status !== "confirmed" &&
    status !== "not_applicable"
  ) fail("material_selection.status 无效");
  return {
    version: requiredCount(record, "version"),
    status,
    document_ids: stringArrayValue(record.document_ids, "material_selection.document_ids"),
    ...(Object.hasOwn(record, "candidate_document_ids")
      ? { candidate_document_ids: stringArrayValue(record.candidate_document_ids, "material_selection.candidate_document_ids") }
      : {}),
    primary_material_id: optionalNullableString(record, "primary_material_id"),
  };
}

export function parseUploadDocumentResult(value: unknown): UploadDocumentResult {
  const record = recordOf(value, "uploaded document");
  return {
    id: requiredString(record, "id"),
    material_id: requiredString(record, "material_id"),
    version: requiredPositiveInteger(record, "version"),
    replayed: requiredBoolean(record, "replayed"),
  };
}

export function parseCaseDetail(value: unknown): CaseDetail {
  const record = recordOf(value, "case");
  if (!hasCaseRecordArrays(record)) fail("案件缺少业务记录数组");
  const materialProjectionKeys = ["materials", "material_versions", "material_selection"];
  const projectionPresence = materialProjectionKeys.map((key) => Object.hasOwn(record, key));
  if (projectionPresence.some(Boolean) && !projectionPresence.every(Boolean))
    fail("案件材料投影字段不完整");
  return {
    id: requiredString(record, "id"),
    title: optionalString(record, "title") ?? "未命名案件",
    contract_type: optionalString(record, "contract_type") ?? "",
    created_at: optionalString(record, "created_at") ?? "",
    updated_at: optionalString(record, "updated_at") ?? "",
    documents: recordList(record.documents, parseDocument),
    ...(projectionPresence.every(Boolean)
      ? {
          materials: requiredRecordList(record.materials, "materials", parseMaterialProjection),
          material_versions: requiredRecordList(record.material_versions, "material_versions", parseMaterialVersionProjection),
          material_selection: parseMaterialSelectionProjection(record.material_selection),
        }
      : {}),
    members: parseMembers(record.members as unknown[]),
    latest_run: parseRunSummary(record.latest_run),
    finding_counts: parseCounts(record.finding_counts),
    activity: parseActivityItems(record.activity),
    revision_pending: record.revision_pending === true,
    configuration_pending: record.configuration_pending === true,
    ...(typeof record.subject_context_changed_after_latest_run === "boolean"
      ? { subject_context_changed_after_latest_run: record.subject_context_changed_after_latest_run }
      : {}),
    business_line: requiredStringValue(record, "business_line"),
    scenario: requiredStringValue(record, "scenario"),
    business_status: requiredStringValue(record, "business_status"),
    generations: (record.generations as unknown[]).map(parseGenerationRecord),
    remediations: (record.remediations as unknown[]).map(parseRemediation),
    reports: (record.reports as unknown[]).map(parseReportRecord),
    archives: (record.archives as unknown[]).map(parseArchiveRecord),
    archived_at: requiredNullableString(record, "archived_at"),
  };
}

function parseAnchor(value: unknown): EvidenceAnchor {
  const record = recordOf(value, "evidence");
  const status = record.status;
  return {
    id: requiredString(record, "id"),
    document_version_id: optionalString(record, "document_version_id") ?? "",
    parsed_document_id: nullableValueString(record, "parsed_document_id"),
    normalized_digest: nullableValueString(record, "normalized_digest"),
    quote: optionalString(record, "quote") ?? "",
    status:
      status === "located" ||
      status === "anchor_mismatch" ||
      status === "unlocatable" ||
      status === "missing"
        ? status
        : "missing",
    page: numberOrNull(record.page),
    text_offset: parseOffset(record.text_offset),
    bbox: parseBox(record.bbox),
    mismatch_reason: optionalString(record, "mismatch_reason"),
  };
}

function parseDecision(value: unknown): HumanDecision {
  const record = recordOf(value, "decision");
  return {
    id: requiredString(record, "id"),
    ...stringDefaults(record, {
      action: "",
      reason: "",
      actor: "未知人员",
      decided_at: "",
    }),
  };
}

function parseFindingSeveritySource(
  value: unknown,
): FindingSeveritySource | null {
  if (value === null || value === undefined) return null;
  const record = recordOf(value, "finding severity source");
  return {
    policy_id: requiredString(record, "policy_id"),
    policy_version: requiredString(record, "policy_version"),
    clause: requiredString(record, "clause"),
    reason: requiredString(record, "reason"),
  };
}

export function parseFinding(value: unknown): Finding {
  const record = recordOf(value, "finding");
  const version = numberOrNull(record.version);
  const verification: FindingVerification =
    typeof record.unverified_reason === "string"
      ? "unverified"
      : "unknown";
  return {
    id: requiredString(record, "id"),
    origin: optionalString(record, "origin") ?? "unknown",
    domain: optionalString(record, "domain") ?? "",
    claim: optionalString(record, "claim") ?? "",
    relation: optionalString(record, "relation") ?? "unknown",
    confidence: optionalString(record, "confidence"),
    verification_status: verification,
    evidence: recordList(record.evidence, parseAnchor),
    unverified_reason: optionalString(record, "unverified_reason"),
    version: version !== null && Number.isInteger(version) ? version : null,
    severity: optionalNullableString(record, "severity"),
    severity_source: parseFindingSeveritySource(record.severity_source),
    decisions: recordList(record.decisions, parseDecision),
  };
}

function parseClause(value: unknown): ClauseNode | null {
  if (!isRecord(value)) return null;
  const id = optionalString(value, "id");
  const heading = optionalString(value, "heading");
  const textOffset = parseOffset(value.text_offset);
  if (id === null || heading === null || textOffset === null) return null;
  return {
    id,
    heading,
    text_offset: textOffset,
    page: numberOrNull(value.page),
  };
}

function parseReviewDocument(value: unknown): ReviewDocument {
  const record = recordOf(value, "document");
  return {
    ...parseDocument(value),
    parsed_document_id: optionalString(record, "parsed_document_id"),
    text: optionalString(record, "text"),
    clauses: Array.isArray(record.clauses)
      ? record.clauses
          .map(parseClause)
          .filter((item): item is ClauseNode => item !== null)
      : [],
  };
}

function parseReviewRun(value: unknown): ReviewRun {
  const record = recordOf(value, "run");
  const summary = parseRunSummary(value);
  if (summary === null) fail("审查运行缺少 id");
  return {
    ...summary,
    case_id: optionalString(record, "case_id") ?? "",
    started_at: optionalString(record, "started_at"),
    model_ref: optionalString(record, "model_ref"),
    partial_reason: optionalString(record, "partial_reason"),
    failure_message: optionalString(record, "failure_message"),
  };
}

function parseExecution(value: unknown): Execution | null {
  if (value === null || value === undefined) return null;
  const record = recordOf(value, "execution");
  if (record.schema_version !== "audit-execution/v1")
    fail("不支持的执行记录版本");
  const attempt = parseExecutionAttempt(record.attempt);
  const attempts = Array.isArray(record.attempts)
    ? record.attempts.map(parseExecutionAttempt)
    : [attempt];
  const events = Array.isArray(record.events)
    ? record.events.map(parseExecutionEvent)
    : [];
  return {
    schema_version: "audit-execution/v1",
    attempt,
    attempts,
    events,
    agent_review:
      record.agent_review === null || record.agent_review === undefined
        ? null
        : parseAgentReview(record.agent_review),
    usage: parseUsage(record.usage),
    all_attempts_usage: parseUsage(record.all_attempts_usage),
    duration_ms: nonNegativeIntegerOrNull(record.duration_ms),
    first_verified_finding_ms: nonNegativeIntegerOrNull(
      record.first_verified_finding_ms,
    ),
    queue_wait_ms: nonNegativeIntegerOrNull(record.queue_wait_ms),
  };
}
function parseReviewAttempts(value: unknown): ReviewAttempts | null {
  if (value === null || value === undefined) return null;
  const record = recordOf(value, "review_attempts");
  if (!Array.isArray(record.attempts)) fail("审查尝试缺少 attempts");
  if (record.attempt === null) return null;
  return {
    attempt: parseExecutionAttempt(record.attempt),
    attempts: record.attempts.map(parseExecutionAttempt),
  };
}

export function parseReviewDiagnostics(value: unknown): ReviewDiagnostics {
  const record = recordOf(value, "review diagnostics");
  if (!Array.isArray(record.audit_events)) fail("审查诊断缺少 audit_events");
  return {
    case_id: requiredString(record, "case_id"),
    run_id: requiredString(record, "run_id"),
    execution: parseExecution(record.execution),
    snapshot: parseSnapshot(record.snapshot),
    audit_events: record.audit_events.map(parseAuditEvent),
  };
}

export function parseReviewWorkspace(value: unknown): ReviewWorkspace {
  const record = recordOf(value, "review");
  if (!Array.isArray(record.audit_events)) fail("审查工作区缺少 audit_events");
  const run = parseReviewRun(record.run);
  const reviewAttempts = parseReviewAttempts(record.review_attempts);
  const legalAuthorities = record.legal_authorities === undefined
    ? undefined
    : parseFrozenLegalReviewProjection(record.legal_authorities);
  if (legalAuthorities && (legalAuthorities.run_id !== run.id ||
      legalAuthorities.attempt_id !== reviewAttempts?.attempt?.id)) {
    fail("冻结法律依据不属于当前审查及所选执行记录");
  }
  return {
    run,
    documents: Array.isArray(record.documents)
      ? record.documents.map(parseReviewDocument)
      : [],
    findings: Array.isArray(record.findings)
      ? record.findings.map(parseFinding)
      : [],
    review_attempts: reviewAttempts,
    agent_review: parseBusinessAgentReview(record.agent_review),
    execution: parseExecution(record.execution),
    snapshot: parseSnapshot(record.snapshot),
    evaluations: Array.isArray(record.evaluations)
      ? record.evaluations.map(parseEvaluation)
      : [],
    audit_events: record.audit_events.map(parseAuditEvent),
    business_events: Array.isArray(record.business_events)
      ? record.business_events.map(parseBusinessAuditEvent)
      : [],
    previous_run_id: optionalString(record, "previous_run_id"),
    revisions: Array.isArray(record.revisions)
      ? record.revisions.map(parseRevision)
      : [],
    revision_comparison_reason: optionalString(
      record,
      "revision_comparison_reason",
    ),
    ...(legalAuthorities === undefined ? {} : { legal_authorities: legalAuthorities }),
  };
}

function parseSnapshot(value: unknown): RunSnapshot | null {
  if (!isRecord(value)) return null;
  return {
    document_ids: Array.isArray(value.document_ids)
      ? value.document_ids.filter((id): id is string => typeof id === "string")
      : [],
    rule_set_hash: optionalString(value, "rule_set_hash") ?? "",
    playbook_hash: optionalString(value, "playbook_hash") ?? "",
    model_ref: optionalString(value, "model_ref"),
    comparator_ref: optionalString(value, "comparator_ref"),
  };
}

function parseEvaluation(value: unknown): ConstraintEvaluation {
  const row = recordOf(value, "evaluation");
  return {
    id: requiredString(row, "id"),
    domain: optionalString(row, "domain") ?? "",
    relation: optionalString(row, "relation") ?? "unknown",
    rule_version: optionalString(row, "rule_version") ?? "",
    reason: optionalString(row, "reason") ?? "",
    rule_id: optionalString(row, "rule_id") ?? "",
    capability_id: optionalString(row, "capability_id") ?? "",
    object_refs: stringList(row.object_refs),
    finding_id: optionalString(row, "finding_id"),
    reason_code: optionalString(row, "reason_code") ?? "",
    fact_ids: stringList(row.fact_ids),
  };
}

function parseRevision(value: unknown): RevisionComparison {
  const row = recordOf(value, "revision");
  return {
    mark: optionalString(row, "mark") ?? "未分类",
    finding_id: optionalString(row, "finding_id"),
    previous_finding_id: optionalString(row, "previous_finding_id"),
    domain: optionalString(row, "domain") ?? "",
    relation: optionalString(row, "relation") ?? "unknown",
    claim: optionalString(row, "claim") ?? "",
    prior_decision:
      row.prior_decision == null ? null : parseDecision(row.prior_decision),
  };
}

export function parseActor(value: unknown): Actor {
  const record = recordOf(value, "session");
  const actor = isRecord(record.actor) ? record.actor : record;
  const audience = requiredString(record, "audience");
  if (audience !== "client" && audience !== "admin") fail("会话 audience 无效");
  return {
    id: requiredString(actor, "id"),
    display_name:
      optionalString(actor, "display_name") ?? requiredString(actor, "id"),
    role: optionalString(actor, "role") ?? "",
    tenant_id: requiredString(actor, "tenant_id"),
    org_id: requiredString(actor, "org_id"),
    audience,
  };
}

function parseTemplate(value: unknown): Template {
  const record = recordOf(value, "template");
  if (!Array.isArray(record.fields)) fail("模板缺少 fields");
  return {
    id: requiredString(record, "id"),
    name: requiredString(record, "name"),
    version: requiredString(record, "version"),
    digest: requiredString(record, "digest"),
    fields: record.fields.map((field) => {
      const item = recordOf(field, "template field");
      return {
        key: requiredString(item, "key"),
        label: requiredString(item, "label"),
        required: requiredBoolean(item, "required"),
        type: requiredString(item, "type"),
      };
    }),
  };
}

export function parseTemplateList(value: unknown): Template[] {
  return items(value, "templates", "模板列表", parseTemplate);
}

export function parseGenerationRecord(value: unknown): GenerationRecord {
  const parsed = requiredStringRecord(value, "generation", [
    "document_id",
    "template_id",
    "template_version",
    "digest",
    "created_at",
  ]);
  const record = recordOf(value, "generation");
  const materialId = optionalString(record, "material_id");
  const version = record.version === undefined ? null : numberOrNull(record.version);
  if (record.version !== undefined && (version === null || !Number.isInteger(version) || version < 1))
    fail("generation.version 无效");
  if (record.replayed !== undefined && typeof record.replayed !== "boolean")
    fail("generation.replayed 无效");
  return {
    ...parsed,
    ...(materialId === null ? {} : { material_id: materialId }),
    ...(version === null ? {} : { version }),
    ...(record.replayed === undefined ? {} : { replayed: record.replayed as boolean }),
  };
}

export function parseRemediation(value: unknown): Remediation {
  const record = recordOf(value, "remediation");
  const status = requiredString(record, "status");
  if (!["open", "submitted", "closed", "reopened"].includes(status))
    fail("整改状态无效");
  return {
    id: requiredString(record, "id"),
    case_id: requiredString(record, "case_id"),
    run_id: requiredString(record, "run_id"),
    attempt_id: requiredString(record, "attempt_id"),
    finding_ids: requiredStringArray(record, "finding_ids"),
    assignee_id: optionalString(record, "assignee_id") ?? "",
    due_at: optionalString(record, "due_at") ?? "",
    measure: requiredString(record, "measure"),
    status,
    version: requiredPositiveInteger(record, "version"),
    document_ids: requiredStringArray(record, "document_ids"),
    reason: optionalString(record, "reason") ?? "",
    verification_run_id: optionalNullableString(record, "verification_run_id"),
    verification_attempt_id: optionalNullableString(
      record,
      "verification_attempt_id",
    ),
    created_at: requiredString(record, "created_at"),
    updated_at: requiredString(record, "updated_at"),
  };
}

export function parseReportRecord(value: unknown): ReportRecord {
  const record = recordOf(value, "report");
  return {
    id: requiredString(record, "id"),
    version: requiredPositiveInteger(record, "version"),
    run_id: requiredString(record, "run_id"),
    attempt_id: requiredString(record, "attempt_id"),
    input_hash: requiredString(record, "input_hash"),
    output_hash: requiredString(record, "output_hash"),
    decision_snapshot_hash: requiredString(record, "decision_snapshot_hash"),
    digest: requiredString(record, "digest"),
    created_at: requiredString(record, "created_at"),
  };
}

export function parseArchiveRecord(value: unknown): ArchiveRecord {
  return requiredStringRecord(value, "archive", [
    "case_id",
    "report_id",
    "reason",
    "archived_at",
  ]);
}

function parseBusinessAuditEvent(value: unknown): BusinessAuditEvent {
  const record = recordOf(value, "business audit event");
  return {
    event_id: requiredString(record, "event_id"),
    run_id: requiredString(record, "run_id"),
    actor: requiredString(record, "actor"),
    seq: requiredPositiveInteger(record, "seq"),
    event_type: requiredString(record, "event_type"),
    correlation_ids: stringArrayOrEmpty(record, "correlation_ids"),
  };
}

function parseAuditEvent(value: unknown): AuditEvent {
  const record = recordOf(value, "audit event");
  if (typeof record.prev_hash !== "string") fail("audit event prev_hash 无效");
  return {
    event_id: requiredString(record, "event_id"),
    run_id: requiredString(record, "run_id"),
    actor: requiredString(record, "actor"),
    seq: requiredPositiveInteger(record, "seq"),
    event_type: requiredString(record, "event_type"),
    prev_hash: record.prev_hash,
    hash: requiredString(record, "hash"),
    correlation_ids: requiredStringArray(record, "correlation_ids"),
  };
}

function parseSettingsUser(value: unknown): SettingsUser | null {
  if (!isRecord(value)) return null;
  const id = optionalString(value, "id");
  if (id === null) return null;
  return {
    id,
    display_name: optionalString(value, "display_name") ?? id,
    role: optionalString(value, "role") ?? "",
  };
}

function parseOperationalMetadata(
  value: unknown,
): ExecutionOperationalMetadata | null {
  if (value === null || value === undefined) return null;
  const record = recordOf(value, "execution settings");
  return {
    total_attempts: requiredCount(record, "total_attempts"),
    running_attempts: requiredCount(record, "running_attempts"),
    completed_attempts: requiredCount(record, "completed_attempts"),
    partial_attempts: requiredCount(record, "partial_attempts"),
    failed_attempts: requiredCount(record, "failed_attempts"),
    cancelled_attempts: requiredCount(record, "cancelled_attempts"),
    model_calls: requiredCount(record, "model_calls"),
    unknown_usage_calls: requiredCount(record, "unknown_usage_calls"),
    queue_depth: nonNegativeIntegerOrNull(record.queue_depth),
  };
}
function parseOperatorConnector(value: unknown): OperatorConnectorInfo | null {
  if (!isRecord(value)) return null;
  const connectorId = optionalString(value, "connector_id");
  if (connectorId === null) return null;
  return {
    connector_id: connectorId,
    source_system: optionalString(value, "source_system"),
    allowed_objects: Array.isArray(value.allowed_objects)
      ? value.allowed_objects.filter(
          (item): item is string => typeof item === "string" && item !== "",
        )
      : [],
  };
}

function parseOperatorAuditSettings(
  value: unknown,
): OperatorAuditSettings | null {
  if (!isRecord(value)) return null;
  return {
    enabled: value.enabled === true,
    connectors: Array.isArray(value.connectors)
      ? value.connectors
          .map(parseOperatorConnector)
          .filter((item): item is OperatorConnectorInfo => item !== null)
      : [],
  };
}

export function parseSettings(value: unknown): SettingsView {
  const record = isRecord(value) ? value : {};
  const model = isRecord(record.model) ? record.model : {};
  const queue = isRecord(record.queue) ? record.queue : {};
  return {
    model: {
      status: optionalString(model, "status") ?? "unknown",
      label: optionalString(model, "label") ?? "未报告",
    },
    queue: {
      depth: numberOrNull(queue.depth),
      oldest_wait_ms: numberOrNull(queue.oldest_wait_ms),
    },
    redaction_policy: optionalString(record, "redaction_policy") ?? "",
    users: Array.isArray(record.users)
      ? record.users
          .map(parseSettingsUser)
          .filter((item): item is SettingsUser => item !== null)
      : [],
    execution: parseOperationalMetadata(record.execution),
    operator_audit: parseOperatorAuditSettings(record.operator_audit),
  };
}

export function parseLLMEndpoint(value: unknown): LLMEndpointView {
  const record = recordOf(value, "LLM endpoint");
  return {
    configured: requiredBoolean(record, "configured"),
    name: requiredStringValue(record, "name"),
    protocol: requiredStringValue(record, "protocol"),
    base_url: requiredStringValue(record, "base_url"),
    models: requiredStringArray(record, "models"),
    review_model: requiredStringValue(record, "review_model"),
    ocr_model: requiredStringValue(record, "ocr_model"),
    has_api_key: requiredBoolean(record, "has_api_key"),
    revision: requiredCount(record, "revision"),
    updated_at: requiredStringValue(record, "updated_at"),
    updated_by: requiredStringValue(record, "updated_by"),
  };
}

export function parseLLMProbe(value: unknown): LLMProbeResult {
  const record = recordOf(value, "LLM probe result");
  return {
    models: requiredStringArray(record, "models"),
    latency_ms: requiredCount(record, "latency_ms"),
  };
}

export function parseId(value: unknown): string {
  if (
    !isRecord(value) ||
    typeof value.id !== "string" ||
    value.id.trim() === ""
  ) {
    fail("服务没有返回编号");
  }
  return value.id;
}
export function parseAcceptedReview(value: unknown): AcceptedReview {
  const record = recordOf(value, "review acceptance");
  return {
    id: requiredString(record, "id"),
    attempt_id: requiredString(record, "attempt_id"),
    job_id: optionalString(record, "job_id") ?? undefined,
  };
}

export function parseEmpty(_value: unknown): null {
  return null;
}
export function parseExecutionUsage(value: unknown): ExecutionUsage | null {
  return parseUsage(value);
}
