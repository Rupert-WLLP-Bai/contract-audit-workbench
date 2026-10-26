import type {
  AnchorStatus,
  ClauseNode,
  ParsedDocumentMetadata,
  TextOffset,
} from "./types";
import { ApiError, requestJson } from "./http";
import { isRecord } from "./guards";
import { paths } from "./paths";

/**
 * Read-only projection of the frozen `case-report/v1` envelope. Values the
 * server omits stay null/empty; the report page never reconstructs a fact the
 * frozen report does not contain.
 */
export interface ReportCaseContext {
  id: string;
  title: string;
  contract_type: string;
  business_line: string;
  scenario: string;
}

export interface ReportRunInfo {
  id: string;
  case_id: string;
  state: string;
  model_ref: string | null;
  partial_reason: string | null;
  failure_message: string | null;
  created_at: string;
  updated_at: string;
}

export interface ReportAttemptInfo {
  id: string;
  run_id: string;
  ordinal: number;
  state: string;
  retry_of_attempt_id: string | null;
  accepted_at: string;
  started_at: string | null;
  finished_at: string | null;
  input_hash: string | null;
  output_hash: string | null;
  error_code: string | null;
  history_kind: string;
}

export interface ReportUsage {
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
  calls: number;
  unknown_calls: number;
  sources: string[];
}

export interface ReportExecutionEvent {
  seq: number;
  attempt_id: string;
  phase: string;
  event_type: string;
  status: string | null;
  at: string | null;
  duration_ms: number | null;
  error_code: string | null;
  model_ref: string | null;
}

export interface ReportExecutionInfo {
  schema_version: string;
  events: ReportExecutionEvent[];
  usage: ReportUsage | null;
  duration_ms: number | null;
  first_verified_finding_ms: number | null;
  queue_wait_ms: number | null;
}

export interface ReportDocument {
  id: string;
  case_id: string;
  role: string;
  filename: string;
  media_type: string;
  digest: string;
  size: number;
  origin: string;
  created_at: string;
  parsed_document: ParsedDocumentMetadata;
  text: string;
  clauses: ClauseNode[];
}

export interface ReportAnchor {
  id: string;
  document_id: string;
  parsed_document_id: string | null;
  normalized_digest: string | null;
  quote: string;
  status: AnchorStatus;
  page: number | null;
  text_offset: TextOffset | null;
  mismatch_reason: string | null;
}

export interface ReportSeveritySource {
  policy_id: string;
  policy_version: string;
  clause: string;
  reason: string;
}

export interface ReportFinding {
  id: string;
  origin: string;
  domain: string;
  rule_id: string | null;
  capability_id: string | null;
  object_ref: string | null;
  claim: string;
  relation: string;
  disposition_suggestion: string | null;
  severity: string | null;
  severity_source: ReportSeveritySource | null;
  version: number | null;
  anchors: ReportAnchor[];
  created_at: string;
}

export interface ReportDecision {
  id: string;
  finding_id: string;
  run_id: string;
  action: string;
  reason: string;
  actor_id: string;
  actor_name: string;
  old_disposition: string;
  new_disposition: string;
  created_at: string;
}

export interface ReportEvaluation {
  id: string;
  domain: string;
  rule_id: string | null;
  rule_version: string;
  capability_id: string | null;
  mandatory: boolean;
  relation: string;
  reason_code: string | null;
  reason: string;
  finding_id: string | null;
}

export interface ReportRulePack {
  availability: string;
  compiler_ref: string;
  manifest_hash: string;
  snapshot_sha256: string;
  snapshot_id: string | null;
  snapshot_version: string | null;
  rule_count: number | null;
  unavailable_reason: string | null;
}

export interface ReportAgentReview {
  status: string | null;
  mode: string | null;
  domain_count: number;
  gap_count: number;
}

export interface ReportDetail {
  schema_version: "case-report/v1";
  report_id: string;
  report_version: number;
  created_at: string;
  notice: string;
  case: ReportCaseContext;
  run: ReportRunInfo;
  attempt: ReportAttemptInfo;
  execution: ReportExecutionInfo;
  documents: ReportDocument[];
  findings: ReportFinding[];
  evaluations: ReportEvaluation[];
  decisions: ReportDecision[];
  rulepack: ReportRulePack;
  input_hash: string;
  output_hash: string;
  decision_snapshot_hash: string;
  agent_review: ReportAgentReview | null;
  agent_review_sha256: string | null;
}

function fail(message: string): never {
  throw new ApiError(`报告内容无效：${message}`, 500, "invalid_response", false, null);
}

function object(value: unknown, name: string): Record<string, unknown> {
  if (!isRecord(value)) fail(`${name}不是对象`);
  return value;
}

function string(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  if (typeof value !== "string") fail(`${key}不是字符串`);
  return value;
}

function nullableString(
  record: Record<string, unknown>,
  key: string,
): string | null {
  const value = record[key];
  if (value === null || value === undefined) return null;
  if (typeof value !== "string") fail(`${key}不是字符串或null`);
  return value === "" ? null : value;
}

function optionalString(record: Record<string, unknown>, key: string): string {
  return nullableString(record, key) ?? "";
}

function integer(record: Record<string, unknown>, key: string): number {
  const value = record[key];
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
    fail(`${key}不是非负整数`);
  return value;
}

function nullableInteger(
  record: Record<string, unknown>,
  key: string,
): number | null {
  const value = record[key];
  if (value === null || value === undefined) return null;
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
    fail(`${key}不是非负整数或null`);
  return value;
}

function booleanOrFalse(record: Record<string, unknown>, key: string): boolean {
  const value = record[key];
  if (value === undefined || value === null) return false;
  if (typeof value !== "boolean") fail(`${key}不是布尔值`);
  return value;
}

function list<T>(
  record: Record<string, unknown>,
  key: string,
  parse: (value: unknown) => T,
): T[] {
  const value = record[key];
  if (!Array.isArray(value)) fail(`${key}不是数组`);
  return value.map(parse);
}

function optionalList<T>(
  record: Record<string, unknown>,
  key: string,
  parse: (value: unknown) => T,
): T[] {
  const value = record[key];
  if (value === null || value === undefined) return [];
  if (!Array.isArray(value)) fail(`${key}不是数组`);
  return value.map(parse);
}

function stringList(record: Record<string, unknown>, key: string): string[] {
  const value = record[key];
  if (value === null || value === undefined) return [];
  if (!Array.isArray(value) || !value.every((item) => typeof item === "string"))
    fail(`${key}不是字符串数组`);
  return value as string[];
}

function offset(value: unknown): TextOffset | null {
  if (!isRecord(value)) return null;
  const start = value.Start ?? value.start;
  const end = value.End ?? value.end;
  if (
    typeof start !== "number" ||
    typeof end !== "number" ||
    !Number.isSafeInteger(start) ||
    !Number.isSafeInteger(end) ||
    start < 0 ||
    end < start
  )
    return null;
  return { start, end };
}

function anchorStatus(value: string): AnchorStatus {
  return value === "located" ||
    value === "anchor_mismatch" ||
    value === "unlocatable" ||
    value === "missing"
    ? value
    : "missing";
}

function parseAnchor(value: unknown): ReportAnchor {
  const record = object(value, "证据锚点");
  const end = nullableInteger(record, "end");
  // The server omits a zero `start`; an explicitly reported end makes the
  // offset complete rather than missing.
  const start = nullableInteger(record, "start") ?? (end === null ? null : 0);
  const status = anchorStatus(string(record, "status"));
  return {
    id: string(record, "id"),
    document_id: string(record, "document_id"),
    parsed_document_id: nullableString(record, "parsed_document_id"),
    normalized_digest: nullableString(record, "normalized_digest"),
    quote: optionalString(record, "quote"),
    status,
    page: nullableInteger(record, "page"),
    text_offset:
      status === "located" && start !== null && end !== null
        ? { start, end }
        : null,
    mismatch_reason: nullableString(record, "reason"),
  };
}

function parseSeveritySource(value: unknown): ReportSeveritySource | null {
  if (!isRecord(value)) return null;
  return {
    policy_id: optionalString(value, "policy_id"),
    policy_version: optionalString(value, "policy_version"),
    clause: optionalString(value, "clause"),
    reason: optionalString(value, "reason"),
  };
}

function parseFinding(value: unknown): ReportFinding {
  const record = object(value, "发现项");
  return {
    id: string(record, "id"),
    origin: string(record, "origin"),
    domain: string(record, "domain"),
    rule_id: nullableString(record, "rule_id"),
    capability_id: nullableString(record, "capability_id"),
    object_ref: nullableString(record, "object_ref"),
    claim: string(record, "claim"),
    relation: string(record, "relation"),
    disposition_suggestion: nullableString(record, "disposition_suggestion"),
    severity: nullableString(record, "severity"),
    severity_source: parseSeveritySource(record.severity_source),
    version: nullableInteger(record, "version"),
    anchors: optionalList(record, "anchors", parseAnchor),
    created_at: optionalString(record, "created_at"),
  };
}

function parseDecision(value: unknown): ReportDecision {
  const record = object(value, "人工决定");
  return {
    id: string(record, "id"),
    finding_id: string(record, "finding_id"),
    run_id: string(record, "run_id"),
    action: string(record, "action"),
    reason: string(record, "reason"),
    actor_id: optionalString(record, "actor_id"),
    actor_name: optionalString(record, "actor_name"),
    old_disposition: optionalString(record, "old_disposition"),
    new_disposition: optionalString(record, "new_disposition"),
    created_at: string(record, "created_at"),
  };
}

function parseEvaluation(value: unknown): ReportEvaluation {
  const record = object(value, "规则评估");
  return {
    id: string(record, "id"),
    domain: string(record, "domain"),
    rule_id: nullableString(record, "rule_id"),
    rule_version: string(record, "rule_version"),
    capability_id: nullableString(record, "capability_id"),
    mandatory: booleanOrFalse(record, "mandatory"),
    relation: string(record, "relation"),
    reason_code: nullableString(record, "reason_code"),
    reason: string(record, "reason"),
    finding_id: nullableString(record, "finding_id"),
  };
}

function parseExecutionEvent(value: unknown): ReportExecutionEvent {
  const record = object(value, "执行事件");
  return {
    seq: integer(record, "seq"),
    attempt_id: string(record, "attempt_id"),
    phase: string(record, "phase"),
    event_type: string(record, "event_type"),
    status: nullableString(record, "status"),
    at: nullableString(record, "at"),
    duration_ms: nullableInteger(record, "duration_ms"),
    error_code: nullableString(record, "error_code"),
    model_ref: nullableString(record, "model_ref"),
  };
}

function parseUsage(value: unknown): ReportUsage | null {
  if (!isRecord(value)) return null;
  return {
    input_tokens: nullableInteger(value, "input_tokens"),
    output_tokens: nullableInteger(value, "output_tokens"),
    total_tokens: nullableInteger(value, "total_tokens"),
    calls: integer(value, "calls"),
    unknown_calls: integer(value, "unknown_calls"),
    sources: stringList(value, "sources"),
  };
}

function parseExecution(value: unknown): ReportExecutionInfo {
  const record = object(value, "执行信息");
  return {
    schema_version: optionalString(record, "schema_version"),
    events: optionalList(record, "events", parseExecutionEvent),
    usage: parseUsage(record.usage),
    duration_ms: nullableInteger(record, "duration_ms"),
    first_verified_finding_ms: nullableInteger(
      record,
      "first_verified_finding_ms",
    ),
    queue_wait_ms: nullableInteger(record, "queue_wait_ms"),
  };
}

function parseRun(value: unknown): ReportRunInfo {
  const record = object(value, "审查运行");
  return {
    id: string(record, "id"),
    case_id: optionalString(record, "case_id"),
    state: string(record, "state"),
    model_ref: nullableString(record, "model_ref"),
    partial_reason: nullableString(record, "partial_reason"),
    failure_message: nullableString(record, "failure_message"),
    created_at: optionalString(record, "created_at"),
    updated_at: optionalString(record, "updated_at"),
  };
}

function parseAttempt(value: unknown): ReportAttemptInfo {
  const record = object(value, "审查尝试");
  return {
    id: string(record, "id"),
    run_id: string(record, "run_id"),
    ordinal: integer(record, "ordinal"),
    state: string(record, "state"),
    retry_of_attempt_id: nullableString(record, "retry_of_attempt_id"),
    accepted_at: optionalString(record, "accepted_at"),
    started_at: nullableString(record, "started_at"),
    finished_at: nullableString(record, "finished_at"),
    input_hash: nullableString(record, "input_hash"),
    output_hash: nullableString(record, "output_hash"),
    error_code: nullableString(record, "error_code"),
    history_kind: optionalString(record, "history_kind"),
  };
}

function parseParsedMetadata(
  value: unknown,
  documentId: string,
): ParsedDocumentMetadata {
  const record = object(value, "解析产物");
  const parsedDocumentId = string(record, "id");
  const parsedDocumentDocumentId = string(record, "document_id");
  if (parsedDocumentDocumentId !== documentId)
    fail("解析产物与材料身份不一致");
  return {
    id: parsedDocumentId,
    document_id: parsedDocumentDocumentId,
    original_digest: optionalString(record, "original_digest"),
    parser_id: optionalString(record, "parser_id"),
    parser_version: optionalString(record, "parser_version"),
    config_hash: optionalString(record, "config_hash"),
    normalization_version: optionalString(record, "normalization_version"),
    normalized_digest: nullableString(record, "normalized_digest"),
    ir_digest: nullableString(record, "ir_digest"),
    artifact_digest: optionalString(record, "artifact_digest"),
    created_at: optionalString(record, "created_at"),
    status: string(record, "status") === "ready" ? "ready" : "failed",
  };
}

function parseClause(value: unknown): ClauseNode | null {
  if (!isRecord(value)) return null;
  const range = offset(value);
  const heading = value.Heading ?? value.heading;
  if (range === null || typeof heading !== "string") return null;
  const id = value.ID ?? value.id;
  const page = value.Page ?? value.page;
  return {
    id: typeof id === "string" ? id : "",
    heading,
    text_offset: range,
    page:
      typeof page === "number" && Number.isSafeInteger(page) && page > 0
        ? page
        : null,
  };
}

function parseDocument(value: unknown): ReportDocument {
  const wrapper = object(value, "报告材料");
  const document = object(wrapper.document, "材料");
  const parsed = object(wrapper.parsed, "解析正文");
  const id = string(document, "id");
  const metadata = parseParsedMetadata(parsed, id);
  const ir = isRecord(parsed.ir) ? parsed.ir : null;
  const clauses = ir
    ? optionalList(
        { clauses: ir.Clauses ?? ir.clauses },
        "clauses",
        parseClause,
      ).filter((item): item is ClauseNode => item !== null)
    : [];
  return {
    id,
    case_id: optionalString(document, "case_id"),
    role: string(document, "role"),
    filename: string(document, "filename"),
    media_type: optionalString(document, "media_type"),
    digest: optionalString(document, "digest"),
    size: integer(document, "size"),
    origin: optionalString(document, "origin"),
    created_at: optionalString(document, "created_at"),
    parsed_document: metadata,
    text: string(parsed, "text"),
    clauses,
  };
}

function parseRulePack(value: unknown): ReportRulePack {
  const record = object(value, "规则包");
  const snapshot = isRecord(record.snapshot) ? record.snapshot : null;
  const rules = snapshot?.rules;
  return {
    availability: string(record, "availability"),
    compiler_ref: optionalString(record, "compiler_ref"),
    manifest_hash: optionalString(record, "manifest_hash"),
    snapshot_sha256: optionalString(record, "snapshot_sha256"),
    snapshot_id: snapshot ? nullableString(snapshot, "id") : null,
    snapshot_version: snapshot ? nullableString(snapshot, "version") : null,
    rule_count: Array.isArray(rules) ? rules.length : null,
    unavailable_reason: nullableString(record, "unavailable_reason"),
  };
}

function parseAgentReview(value: unknown): ReportAgentReview | null {
  if (!isRecord(value)) return null;
  return {
    status: nullableString(value, "status"),
    mode: nullableString(value, "mode"),
    domain_count: Array.isArray(value.domains) ? value.domains.length : 0,
    gap_count: Array.isArray(value.gaps) ? value.gaps.length : 0,
  };
}

export function parseReportDetail(value: unknown): ReportDetail {
  const record = object(value, "报告");
  if (record.schema_version !== "case-report/v1")
    fail("schema_version不是case-report/v1");
  const caseRecord = object(record.case, "案件");
  return {
    schema_version: "case-report/v1",
    report_id: string(record, "report_id"),
    report_version: integer(record, "report_version"),
    created_at: string(record, "created_at"),
    notice: optionalString(record, "notice"),
    case: {
      id: string(caseRecord, "id"),
      title: string(caseRecord, "title"),
      contract_type: string(caseRecord, "contract_type"),
      business_line: optionalString(caseRecord, "business_line"),
      scenario: optionalString(caseRecord, "scenario"),
    },
    run: parseRun(record.run),
    attempt: parseAttempt(record.attempt),
    execution: parseExecution(record.execution),
    documents: list(record, "documents", parseDocument),
    findings: list(record, "findings", parseFinding),
    evaluations: optionalList(record, "evaluations", parseEvaluation),
    decisions: optionalList(record, "decisions", parseDecision),
    rulepack: parseRulePack(record.rulepack),
    input_hash: optionalString(record, "input_hash"),
    output_hash: optionalString(record, "output_hash"),
    decision_snapshot_hash: optionalString(record, "decision_snapshot_hash"),
    agent_review: parseAgentReview(record.agent_review),
    agent_review_sha256: nullableString(record, "agent_review_sha256"),
  };
}

export function getReport(
  caseId: string,
  reportId: string,
  signal?: AbortSignal,
) {
  return requestJson(
    paths.report(caseId, reportId, "json"),
    { method: "GET", signal },
    parseReportDetail,
  );
}
