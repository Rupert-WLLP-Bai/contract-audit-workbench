import { jsonInit, requestJson } from "./http";
import { enumValue, responseReader } from "./respond";

export interface ShadowSampleRef { case_id: string; run_id: string; attempt_id: string }
export interface ShadowSampleOption extends ShadowSampleRef { finished_at: string; state: string }
export interface ShadowChange {
  rule_id: string; kind: "changed" | "removed" | "unavailable";
  base_relation: string; candidate_relation: string; base_reason: string; candidate_reason: string; detail: string;
}
export interface ShadowSampleResult extends ShadowSampleRef {
  case_authorized: boolean; status: "completed" | "partial" | "failed"; error_code: string; message: string;
  input_sha256: string; output_sha256: string; knowledge_sha256: string; rule_request_sha256: string;
  extractor_ref: string; base_pack_id: string; base_pack_version: string; base_pack_sha256: string;
  base_pack_snapshot_sha256: string; changes: ShadowChange[];
}
export interface ShadowReport {
  schema_version: "rulepack-shadow-report/v1"; id: string; created_at: string; created_by: string;
  candidate: { id: string; version: string; revision: number; content_sha256: string; snapshot_sha256: string };
  samples: ShadowSampleResult[]; completed: number; partial: number; failed: number;
}
export interface ShadowEnvelope { report: ShadowReport; report_sha256: string }
export interface ShadowSamples { items: ShadowSampleOption[]; selection_limit: number; scan_limit: number }

const reader = responseReader((path) => `影子对比响应中的 ${path} 无法识别`);
const refKeys = ["case_id", "run_id", "attempt_id"] as const;
const resultKeys = [...refKeys, "case_authorized", "status", "error_code", "message", "input_sha256", "output_sha256", "knowledge_sha256", "rule_request_sha256", "extractor_ref", "base_pack_id", "base_pack_version", "base_pack_sha256", "base_pack_snapshot_sha256", "changes"] as const;
const changeKeys = ["rule_id", "kind", "base_relation", "candidate_relation", "base_reason", "candidate_reason", "detail"] as const;
function text(row: Record<string, unknown>, key: string, path: string): string { return reader.text(row[key], `${path}.${key}`); }
function digest(value: unknown, path: string, optional = false): string {
  const result = reader.text(value, path);
  if ((optional && result === "") || /^[0-9a-f]{64}$/.test(result)) return result;
  return reader.invalid(path);
}
function ref(row: Record<string, unknown>, path: string): ShadowSampleRef {
  return { case_id: text(row, "case_id", path), run_id: text(row, "run_id", path), attempt_id: text(row, "attempt_id", path) };
}
function option(value: unknown, path: string): ShadowSampleOption {
  const row = reader.record(value, path, [...refKeys, "finished_at", "state"]);
  return { ...ref(row, path), finished_at: text(row, "finished_at", path), state: text(row, "state", path) };
}
function change(value: unknown, path: string): ShadowChange {
  const row = reader.record(value, path, changeKeys);
  const kind = enumValue(row.kind, ["changed", "removed", "unavailable"] as const) ?? reader.invalid(`${path}.kind`);
  return { rule_id: text(row, "rule_id", path), kind, base_relation: text(row, "base_relation", path), candidate_relation: text(row, "candidate_relation", path), base_reason: text(row, "base_reason", path), candidate_reason: text(row, "candidate_reason", path), detail: text(row, "detail", path) };
}
function result(value: unknown, path: string): ShadowSampleResult {
  const row = reader.record(value, path, resultKeys);
  const status = enumValue(row.status, ["completed", "partial", "failed"] as const) ?? reader.invalid(`${path}.status`);
  return { ...ref(row, path), case_authorized: reader.bool(row.case_authorized, `${path}.case_authorized`), status,
    error_code: text(row, "error_code", path), message: text(row, "message", path),
    input_sha256: digest(row.input_sha256, `${path}.input_sha256`, true), output_sha256: digest(row.output_sha256, `${path}.output_sha256`, true),
    knowledge_sha256: digest(row.knowledge_sha256, `${path}.knowledge_sha256`, true), rule_request_sha256: digest(row.rule_request_sha256, `${path}.rule_request_sha256`, true),
    extractor_ref: text(row, "extractor_ref", path), base_pack_id: text(row, "base_pack_id", path), base_pack_version: text(row, "base_pack_version", path),
    base_pack_sha256: digest(row.base_pack_sha256, `${path}.base_pack_sha256`, true), base_pack_snapshot_sha256: digest(row.base_pack_snapshot_sha256, `${path}.base_pack_snapshot_sha256`, true),
    changes: reader.array(row.changes, `${path}.changes`, change) };
}
function parseEnvelope(value: unknown, path = "response"): ShadowEnvelope {
  const envelope = reader.record(value, path, ["report", "report_sha256"]);
  const row = reader.record(envelope.report, `${path}.report`, ["schema_version", "id", "created_at", "created_by", "candidate", "samples", "completed", "partial", "failed"]);
  if (row.schema_version !== "rulepack-shadow-report/v1") return reader.invalid(`${path}.report.schema_version`);
  const candidatePath = `${path}.report.candidate`;
  const candidate = reader.record(row.candidate, candidatePath, ["id", "version", "revision", "content_sha256", "snapshot_sha256"]);
  const samples = reader.array(row.samples, `${path}.report.samples`, result);
  const completed = reader.integer(row.completed, `${path}.report.completed`, 0);
  const partial = reader.integer(row.partial, `${path}.report.partial`, 0);
  const failed = reader.integer(row.failed, `${path}.report.failed`, 0);
  if (completed !== samples.filter((item) => item.status === "completed").length || partial !== samples.filter((item) => item.status === "partial").length || failed !== samples.filter((item) => item.status === "failed").length) return reader.invalid(`${path}.report.samples`);
  return { report_sha256: digest(envelope.report_sha256, `${path}.report_sha256`), report: {
    schema_version: "rulepack-shadow-report/v1", id: text(row, "id", `${path}.report`), created_at: text(row, "created_at", `${path}.report`), created_by: text(row, "created_by", `${path}.report`),
    candidate: { id: text(candidate, "id", candidatePath), version: text(candidate, "version", candidatePath), revision: reader.integer(candidate.revision, `${candidatePath}.revision`, 1), content_sha256: digest(candidate.content_sha256, `${candidatePath}.content_sha256`), snapshot_sha256: digest(candidate.snapshot_sha256, `${candidatePath}.snapshot_sha256`) },
    samples, completed, partial, failed,
  } };
}
function base(pack: string, version: string): string { return `/api/admin/rulepacks/${encodeURIComponent(pack)}/versions/${encodeURIComponent(version)}/shadow`; }
export function listShadowSamples(pack: string, version: string, signal?: AbortSignal): Promise<ShadowSamples> {
  return requestJson(`${base(pack, version)}/samples`, { signal }, (value) => {
    const row = reader.record(value, "response", ["items", "selection_limit", "scan_limit"]);
    return { items: reader.array(row.items, "response.items", option), selection_limit: reader.integer(row.selection_limit, "response.selection_limit", 1), scan_limit: reader.integer(row.scan_limit, "response.scan_limit", 1) };
  });
}
export function createShadowReport(pack: string, version: string, revision: number, hash: string, samples: ShadowSampleRef[]): Promise<ShadowEnvelope> {
  return requestJson(base(pack, version), jsonInit("POST", { expected_revision: revision, expected_content_hash: hash, samples }), parseEnvelope);
}
export function listShadowReports(pack: string, version: string, signal?: AbortSignal): Promise<ShadowEnvelope[]> {
  return requestJson(`${base(pack, version)}/reports`, { signal }, (value) => {
    const row = reader.record(value, "response", ["items"]);
    return reader.array(row.items, "response.items", parseEnvelope);
  });
}
export function getShadowReport(pack: string, version: string, reportID: string, signal?: AbortSignal): Promise<ShadowEnvelope> {
  return requestJson(`${base(pack, version)}/reports/${encodeURIComponent(reportID)}`, { signal }, parseEnvelope);
}
