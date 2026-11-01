import { jsonInit, requestJson } from "../../../api/http";
import { responseReader } from "../../../api/respond";

export type ManualFindingSeverity = "high" | "medium" | "low" | "info";
export interface ManualFindingEvidence {
  document_id: string;
  parsed_document_id: string;
  normalized_digest: string;
  start: number;
  end: number;
  quote: string;
}
export interface ManualFinding {
  id: string;
  case_id: string;
  run_id: string;
  attempt_id: string;
  actor_id: string;
  actor_name: string;
  claim: string;
  severity: ManualFindingSeverity;
  evidence: ManualFindingEvidence;
  created_at: string;
}
export interface ManualFindingInput {
  claim: string;
  severity: ManualFindingSeverity;
  evidence: Omit<ManualFindingEvidence, "normalized_digest"> & { normalized_digest?: string };
}

const { invalid, object, text, integer, array } = responseReader(
  (field) => `人工发现响应中的 ${field} 无法识别`,
);

function identity(value: unknown, field: string): string {
  const result = text(value, field);
  return result && result.trim() === result ? result : invalid(field);
}

function parseManualFinding(value: unknown, field = "item"): ManualFinding {
  const item = object(value, field);
  const source = object(item.evidence, `${field}.evidence`);
  const severity = text(item.severity, `${field}.severity`);
  if (severity !== "high" && severity !== "medium" && severity !== "low" && severity !== "info") return invalid(`${field}.severity`);
  const start = integer(source.start, `${field}.evidence.start`, 0);
  const end = integer(source.end, `${field}.evidence.end`, 1);
  const quote = text(source.quote, `${field}.evidence.quote`);
  const digest = text(source.normalized_digest, `${field}.evidence.normalized_digest`);
  if (end <= start || Array.from(quote).length !== end - start || !/^[0-9a-f]{64}$/.test(digest)) return invalid(`${field}.evidence`);
  return {
    id: identity(item.id, `${field}.id`),
    case_id: identity(item.case_id, `${field}.case_id`),
    run_id: identity(item.run_id, `${field}.run_id`),
    attempt_id: identity(item.attempt_id, `${field}.attempt_id`),
    actor_id: identity(item.actor_id, `${field}.actor_id`),
    actor_name: identity(item.actor_name, `${field}.actor_name`),
    claim: text(item.claim, `${field}.claim`),
    severity,
    evidence: {
      document_id: identity(source.document_id, `${field}.evidence.document_id`),
      parsed_document_id: identity(source.parsed_document_id, `${field}.evidence.parsed_document_id`),
      normalized_digest: digest,
      start, end, quote,
    },
    created_at: text(item.created_at, `${field}.created_at`),
  };
}

function path(caseId: string, runId: string, attemptId: string): string {
  return `/api/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(runId)}/attempts/${encodeURIComponent(attemptId)}/manual-findings`;
}

function checkScope(item: ManualFinding, caseId: string, runId: string, attemptId: string): ManualFinding {
  if (item.case_id !== caseId || item.run_id !== runId || item.attempt_id !== attemptId) return invalid("来源身份");
  return item;
}

export function listManualFindings(caseId: string, runId: string, attemptId: string, signal?: AbortSignal): Promise<ManualFinding[]> {
  return requestJson(path(caseId, runId, attemptId), { method: "GET", signal }, (value) => {
    const payload = object(value, "list");
    return array(payload.items, "items", (entry, field) => checkScope(parseManualFinding(entry, field), caseId, runId, attemptId));
  });
}

export function createManualFinding(caseId: string, runId: string, attemptId: string, input: ManualFindingInput, idempotencyKey: string): Promise<ManualFinding> {
  return requestJson(path(caseId, runId, attemptId), jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }), (value) => {
    const item = checkScope(parseManualFinding(value), caseId, runId, attemptId);
    const actual = item.evidence;
    const expected = input.evidence;
    if (item.claim !== input.claim.trim() || item.severity !== input.severity ||
      actual.document_id !== expected.document_id || actual.parsed_document_id !== expected.parsed_document_id ||
      actual.quote !== expected.quote || actual.start !== expected.start || actual.end !== expected.end ||
      (expected.normalized_digest !== undefined && actual.normalized_digest !== expected.normalized_digest)) return invalid("记录内容");
    return item;
  });
}
