import { isRecord } from "./guards";
import { enumValue, invalidResponse } from "./respond";
import { wireEnums } from "./wire.generated";
import {
  parseCaseSummary,
  parseExecutionUsage,
  parseReportRecord,
  parseRemediation,
} from "./parse";
import type {
  CaseProfile,
  CaseRelation,
  DocumentVersion,
  Knowledge,
  ParsedDocumentMetadata,
  Template,
  WorkbenchAuditEvent,
  WorkbenchCase,
  WorkbenchDashboard,
  WorkbenchDocument,
  WorkbenchMember,
  WorkbenchPeriod,
  WorkbenchRemediation,
  WorkbenchReport,
  WorkbenchRun,
  WorkbenchSnapshot,
  WorkbenchTask,
} from "./types";

function invalid(message: string): never {
  return invalidResponse(`工作台数据无效：${message}`, 500);
}

function object(value: unknown, name: string): Record<string, unknown> {
  if (!isRecord(value)) return invalid(`${name}不是对象`);
  return value;
}

function string(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  if (typeof value !== "string") return invalid(`${key}不是字符串`);
  return value;
}

function nullableString(
  record: Record<string, unknown>,
  key: string,
): string | null {
  const value = record[key];
  if (value !== null && typeof value !== "string")
    return invalid(`${key}不是字符串或null`);
  return value;
}

function count(record: Record<string, unknown>, key: string): number {
  const value = record[key];
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
    return invalid(`${key}不是非负整数`);
  return value;
}

function nullableDuration(
  record: Record<string, unknown>,
  key: string,
): number | null {
  const value = record[key];
  if (value === null) return null;
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
    return invalid(`${key}不是非负整数或null`);
  return value;
}

function isoTime(record: Record<string, unknown>, key: string): string {
  const value = string(record, key);
  if (
    !/^\d{4}-\d{2}-\d{2}T.*(?:Z|[+-]\d{2}:\d{2})$/.test(value) ||
    Number.isNaN(Date.parse(value))
  ) {
    return invalid(`${key}不是RFC3339时间`);
  }
  return value;
}

function array<T>(
  record: Record<string, unknown>,
  key: string,
  parse: (value: unknown) => T,
): T[] {
  const value = record[key];
  if (!Array.isArray(value)) return invalid(`${key}不是数组`);
  return value.map(parse);
}

function stringArray(record: Record<string, unknown>, key: string): string[] {
  const value = record[key];
  if (!Array.isArray(value) || !value.every((item) => typeof item === "string"))
    return invalid(`${key}不是字符串数组`);
  return value;
}

function profile(value: unknown): CaseProfile {
  const record = object(value, "案件资料");
  return {
    case_id: string(record, "case_id"),
    number: string(record, "number"),
    customer: string(record, "customer"),
    supplier: string(record, "supplier"),
    contract_category: string(record, "contract_category"),
    owner_id: string(record, "owner_id"),
    due_at: nullableString(record, "due_at"),
    version: count(record, "version"),
    updated_at: isoTime(record, "updated_at"),
  };
}

function task(value: unknown): WorkbenchTask {
  const record = object(value, "任务");
  const kind = enumValue(string(record, "kind"), wireEnums["WorkbenchTask.kind"]);
  const status = enumValue(string(record, "status"), wireEnums["WorkbenchTask.status"]);
  const priority = enumValue(string(record, "priority"), wireEnums["WorkbenchTask.priority"]);
  if (kind === null) return invalid("任务 kind 无效");
  if (status === null) return invalid("任务 status 无效");
  if (priority === null) return invalid("任务 priority 无效");
  return {
    id: string(record, "id"),
    case_id: string(record, "case_id"),
    title: string(record, "title"),
    kind,
    assignee_id: string(record, "assignee_id"),
    due_at: nullableString(record, "due_at"),
    status,
    priority,
    finding_id: nullableString(record, "finding_id"),
    version: count(record, "version"),
    created_by: nullableString(record, "created_by"),
    created_at: isoTime(record, "created_at"),
    updated_at: isoTime(record, "updated_at"),
  };
}

function relation(value: unknown): CaseRelation {
  const record = object(value, "案件关联");
  return {
    id: string(record, "id"),
    case_id: string(record, "case_id"),
    related_case_id: string(record, "related_case_id"),
    reason: string(record, "reason"),
    created_by: string(record, "created_by"),
    created_at: isoTime(record, "created_at"),
  };
}

function knowledge(value: unknown): Knowledge {
  const record = object(value, "知识条目");
  const sourceType = string(record, "source_type");
  if (sourceType !== "synthetic_policy" && sourceType !== "reference")
    return invalid("知识来源类型无效");
  const versionId = string(record, "version_id");
  if (!versionId) return invalid("知识版本标识为空");
  return {
    id: string(record, "id"),
    version_id: versionId,
    title: string(record, "title"),
    version: string(record, "version"),
    body: string(record, "body"),
    source_type: sourceType,
    effective_at: nullableString(record, "effective_at"),
    created_by: string(record, "created_by"),
    created_at: isoTime(record, "created_at"),
  };
}

function member(value: unknown): WorkbenchMember {
  const record = object(value, "成员");
  return {
    id: string(record, "id"),
    display_name: string(record, "display_name"),
    role: string(record, "role"),
    tenant_id: string(record, "tenant_id"),
    org_id: string(record, "org_id"),
  };
}

function hasCaseEnvelope(record: Record<string, unknown>): boolean {
  return (
    Object.hasOwn(record, "profile") &&
    Object.hasOwn(record, "severity") &&
    Object.hasOwn(record, "progress") &&
    Object.hasOwn(record, "latest_run")
  );
}

function isCaseProgress(value: unknown): boolean {
  return (
    value === null ||
    (typeof value === "number" &&
      Number.isFinite(value) &&
      value >= 0 &&
      value <= 100)
  );
}

function workbenchCase(value: unknown): WorkbenchCase {
  const record = object(value, "案件");
  if (!hasCaseEnvelope(record))
    return invalid("案件缺少profile、severity、progress或latest_run");
  const severity = nullableString(record, "severity");
  const progress = record.progress;
  if (!isCaseProgress(progress)) return invalid("案件 progress 无效");
  const parsed = parseCaseSummary(value);
  return {
    ...parsed,
    profile: record.profile === null ? null : profile(record.profile),
    severity,
    progress: progress as number | null,
  };
}

function dashboard(value: unknown): WorkbenchDashboard {
  const record = object(value, "dashboard");
  const progress = array(record, "progress", (entry) => {
    const item = object(entry, "进展统计");
    return { status: string(item, "status"), count: count(item, "count") };
  });
  const risks = array<WorkbenchDashboard["risks"][number]>(
    record,
    "risks",
    (entry) => {
      const item = object(entry, "风险统计");
      const severity = string(item, "severity");
      if (
        severity !== "high" &&
        severity !== "medium" &&
        severity !== "low" &&
        severity !== "info" &&
        severity !== "unrated"
      ) {
        return invalid("风险统计 severity 无效");
      }
      return { severity, count: count(item, "count") };
    },
  );
  return {
    visible_cases: count(record, "visible_cases"),
    my_pending_tasks: count(record, "my_pending_tasks"),
    overdue_tasks: count(record, "overdue_tasks"),
    pending_review_cases: count(record, "pending_review_cases"),
    remediation_cases: count(record, "remediation_cases"),
    completed_reviews: count(record, "completed_reviews"),
    average_duration_ms: nullableDuration(record, "average_duration_ms"),
    progress,
    risks,
  };
}

function parsedDocument(value: unknown): ParsedDocumentMetadata | null {
  if (value === null) return null;
  const record = object(value, "解析产物");
  const status = string(record, "status");
  if (status !== "ready" && status !== "failed")
    return invalid("解析产物 status 无效");
  return {
    id: string(record, "id"),
    document_id: string(record, "document_id"),
    original_digest: string(record, "original_digest"),
    parser_id: string(record, "parser_id"),
    parser_version: string(record, "parser_version"),
    config_hash: string(record, "config_hash"),
    normalization_version: string(record, "normalization_version"),
    normalized_digest: nullableString(record, "normalized_digest"),
    ir_digest: nullableString(record, "ir_digest"),
    artifact_digest: string(record, "artifact_digest"),
    created_at: isoTime(record, "created_at"),
    status,
  };
}

function document(value: unknown): DocumentVersion {
  const record = object(value, "材料");
  const size = record.size;
  if (typeof size !== "number" || !Number.isSafeInteger(size) || size < 0)
    return invalid("材料 size 无效");
  const superseded = record.superseded;
  if (superseded !== undefined && typeof superseded !== "boolean")
    return invalid("材料 superseded 无效");
  return {
    id: string(record, "id"),
    role: string(record, "role"),
    filename: string(record, "filename"),
    media_type: string(record, "media_type"),
    digest: string(record, "digest"),
    size,
    parse_status: string(record, "parse_status"),
    parse_error: nullableString(record, "parse_error"),
    created_at: isoTime(record, "created_at"),
    parsed_document: parsedDocument(record.parsed_document),
    ...(superseded === undefined ? {} : { superseded }),
  };
}

function workbenchDocument(value: unknown): WorkbenchDocument {
  const record = object(value, "材料记录");
  return {
    ...document(value),
    case_id: string(record, "case_id"),
    case_title: string(record, "case_title"),
  };
}

function remediation(value: unknown): WorkbenchRemediation {
  const record = object(value, "整改记录");
  return {
    ...parseRemediation(value),
    case_title: string(record, "case_title"),
  };
}

function report(value: unknown): WorkbenchReport {
  const record = object(value, "报告记录");
  return {
    ...parseReportRecord(value),
    case_id: string(record, "case_id"),
    case_title: string(record, "case_title"),
  };
}

function run(value: unknown): WorkbenchRun {
  const record = object(value, "审查运行");
  return {
    id: string(record, "id"),
    case_id: string(record, "case_id"),
    case_title: string(record, "case_title"),
    state: string(record, "state"),
    created_at: isoTime(record, "created_at"),
    updated_at: isoTime(record, "updated_at"),
    attempt_id: nullableString(record, "attempt_id"),
    started_at: nullableString(record, "started_at"),
    finished_at: nullableString(record, "finished_at"),
    usage: parseExecutionUsage(record.usage),
  };
}

function auditEvent(value: unknown): WorkbenchAuditEvent {
  const record = object(value, "审计事件");
  return {
    id: string(record, "id"),
    case_id: nullableString(record, "case_id"),
    case_title: nullableString(record, "case_title"),
    actor_id: string(record, "actor_id"),
    actor_name: string(record, "actor_name"),
    action: string(record, "action"),
    at: isoTime(record, "at"),
    summary: string(record, "summary"),
  };
}

function template(value: unknown): Template {
  const record = object(value, "模板");
  const fields = array(record, "fields", (fieldValue) => {
    const field = object(fieldValue, "模板字段");
    if (typeof field.required !== "boolean")
      return invalid("模板字段 required 无效");
    return {
      key: string(field, "key"),
      label: string(field, "label"),
      required: field.required,
      type: string(field, "type"),
    };
  });
  return {
    id: string(record, "id"),
    name: string(record, "name"),
    version: string(record, "version"),
    digest: string(record, "digest"),
    fields,
  };
}

export function parseWorkbenchSnapshot(
  value: unknown,
  requestedPeriod: WorkbenchPeriod,
): WorkbenchSnapshot {
  const record = object(value, "响应");
  if (record.schema_version !== "workbench/v1")
    return invalid("schema_version 不受支持");
  const period = string(record, "period");
  if (period !== "day" && period !== "week" && period !== "month")
    return invalid("period 无效");
  if (period !== requestedPeriod) return invalid("响应 period 与请求不一致");
  const modelRecord = object(record.model, "模型配置");
  const tools = array(record, "tools", (toolValue) => {
    const item = object(toolValue, "工具");
    return {
      id: string(item, "id"),
      name: string(item, "name"),
      description: string(item, "description"),
      capabilities: stringArray(item, "capabilities"),
      calls: count(item, "calls"),
      last_status: nullableString(item, "last_status"),
    };
  });
  return {
    schema_version: "workbench/v1",
    generated_at: isoTime(record, "generated_at"),
    period,
    dashboard: dashboard(record.dashboard),
    cases: array(record, "cases", workbenchCase),
    profiles: array(record, "profiles", profile),
    tasks: array(record, "tasks", task),
    relations: array(record, "relations", relation),
    knowledge: array(record, "knowledge", knowledge),
    members: array(record, "members", member),
    remediations: array(record, "remediations", remediation),
    reports: array(record, "reports", report),
    documents: array(record, "documents", workbenchDocument),
    runs: array(record, "runs", run),
    audit: array(record, "audit", auditEvent),
    templates: array(record, "templates", template),
    model: {
      name: string(modelRecord, "name"),
      ocr_name: string(modelRecord, "ocr_name"),
      status: (() => {
        const status = string(modelRecord, "status");
        if (status !== "configured" && status !== "unconfigured")
          return invalid("model.status 无效");
        return status;
      })(),
      label: string(modelRecord, "label"),
    },
    tools,
  };
}

export function parseCaseProfile(value: unknown): CaseProfile {
  return profile(value);
}

export function parseWorkbenchTask(value: unknown): WorkbenchTask {
  return task(value);
}

export function parseCaseRelation(value: unknown): CaseRelation {
  return relation(value);
}

export function parseKnowledge(value: unknown): Knowledge {
  return knowledge(value);
}
