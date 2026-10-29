import { jsonInit, requestJson } from "./http";
import { responseReader } from "./respond";

export interface EvidenceSourceEvaluation {
  id: string;
  domain: string;
  rule_version: string;
  rule_id: string;
  capability_id: string;
  object_refs: string[];
  relation: string;
  reason: string;
}

export interface EvidenceSourceGap {
  gap_id: string;
  domain: string;
  capability_id: string;
  status: string;
  reason_code: string;
}

export interface EvidenceQuote {
  document_id: string;
  parsed_document_id: string;
  quote: string;
  start: number;
  end: number;
}

export interface EvidenceVerification {
  run_id: string;
  attempt_id: string;
  receipt_id: string;
  evaluation_id: string | null;
  relation: string | null;
  gap_id: string | null;
  gap_status: string | null;
  evidence: EvidenceQuote[];
  reason: string;
  verified_by: string;
  verified_at: string;
}

export interface EvidenceRequest {
  id: string;
  case_id: string;
  task_id: string;
  task_version: number;
  assignee_id: string;
  due_at: string;
  source_run_id: string;
  source_attempt_id: string;
  source_evaluation: EvidenceSourceEvaluation | null;
  source_gap: EvidenceSourceGap | null;
  question: string;
  material_role: string | null;
  verification_condition: string;
  status: string;
  version: number;
  created_by: string;
  created_at: string;
  updated_at: string;
  verification: EvidenceVerification | null;
}

export interface EvidenceReceipt {
  id: string;
  request_id: string;
  case_id: string;
  document_id: string | null;
  answer: string | null;
  submitted_by: string;
  created_at: string;
}

export interface EvidenceRequestEvent {
  type: string;
  actor_id: string;
  occurred_at: string;
  request: EvidenceRequest;
  receipt: EvidenceReceipt | null;
}

export interface CreateEvidenceRequestInput {
  task_id: string;
  task_version: number;
  source_run_id: string;
  source_attempt_id: string;
  source_evaluation_id?: string;
  source_gap_id?: string;
  question: string;
  material_role?: string;
  verification_condition: "compatible" | "conflict" | "resolved";
}

export interface CreateEvidenceReceiptInput {
  document_id?: string;
  answer?: string;
  expected_version: number;
}

export interface VerifyEvidenceRequestInput {
  run_id: string;
  attempt_id: string;
  evaluation_id?: string;
  gap_id?: string;
  evidence: EvidenceQuote[];
  reason: string;
  expected_version: number;
}

const { invalid, object, text, optionalText, array, integer: safeInteger } =
  responseReader((field) => `补充材料响应中的 ${field} 无法识别`);
const integer = (value: unknown, field: string, minimum = 1): number =>
  safeInteger(value, field, minimum);

function parseQuote(value: unknown): EvidenceQuote {
  const item = object(value, "evidence");
  return {
    document_id: text(item.document_id, "document_id"),
    parsed_document_id: text(item.parsed_document_id, "parsed_document_id"),
    quote: text(item.quote, "quote"),
    start: integer(item.start, "start", 0),
    end: integer(item.end, "end"),
  };
}

function parseVerification(value: unknown): EvidenceVerification | null {
  if (value === undefined || value === null) return null;
  const item = object(value, "verification");
  return {
    run_id: text(item.run_id, "verification.run_id"),
    attempt_id: text(item.attempt_id, "verification.attempt_id"),
    receipt_id: text(item.receipt_id, "verification.receipt_id"),
    evaluation_id: optionalText(item.evaluation_id, "verification.evaluation_id"),
    relation: optionalText(item.relation, "verification.relation"),
    gap_id: optionalText(item.gap_id, "verification.gap_id"),
    gap_status: optionalText(item.gap_status, "verification.gap_status"),
    evidence: array(item.evidence, "verification.evidence", parseQuote),
    reason: text(item.reason, "verification.reason"),
    verified_by: text(item.verified_by, "verification.verified_by"),
    verified_at: text(item.verified_at, "verification.verified_at"),
  };
}

export function parseEvidenceRequest(value: unknown): EvidenceRequest {
  const item = object(value, "request");
  const evaluation = item.source_evaluation === undefined || item.source_evaluation === null
    ? null : object(item.source_evaluation, "source_evaluation");
  const gap = item.source_gap === undefined || item.source_gap === null
    ? null : object(item.source_gap, "source_gap");
  if ((evaluation === null) === (gap === null)) return invalid("来源检查");
  const sourceEvaluation = evaluation === null ? null : {
    id: text(evaluation.id, "source_evaluation.id"),
    domain: text(evaluation.domain, "source_evaluation.domain"),
    rule_version: text(evaluation.rule_version, "source_evaluation.rule_version"),
    rule_id: optionalText(evaluation.rule_id, "source_evaluation.rule_id") ?? "",
    capability_id: optionalText(evaluation.capability_id, "source_evaluation.capability_id") ?? "",
    object_refs: evaluation.object_refs === undefined || evaluation.object_refs === null ? [] :
      array(evaluation.object_refs, "source_evaluation.object_refs", (entry) => text(entry, "object_ref")),
    relation: text(evaluation.relation, "source_evaluation.relation"),
    reason: text(evaluation.reason, "source_evaluation.reason"),
  };
  const sourceGap = gap === null ? null : {
    gap_id: text(gap.gap_id, "source_gap.gap_id"),
    domain: text(gap.domain, "source_gap.domain"),
    capability_id: text(gap.capability_id, "source_gap.capability_id"),
    status: text(gap.status, "source_gap.status"),
    reason_code: text(gap.reason_code, "source_gap.reason_code"),
  };
  const status = text(item.status, "status");
  const verification = parseVerification(item.verification);
  if ((status === "verified" && verification === null) ||
    ((status === "open" || status === "received") && verification !== null)) return invalid("verification");
  return {
    id: text(item.id, "id"),
    case_id: text(item.case_id, "case_id"),
    task_id: text(item.task_id, "task_id"),
    task_version: integer(item.task_version, "task_version"),
    assignee_id: text(item.assignee_id, "assignee_id"),
    due_at: text(item.due_at, "due_at"),
    source_run_id: text(item.source_run_id, "source_run_id"),
    source_attempt_id: text(item.source_attempt_id, "source_attempt_id"),
    source_evaluation: sourceEvaluation,
    source_gap: sourceGap,
    question: text(item.question, "question"),
    material_role: optionalText(item.material_role, "material_role"),
    verification_condition: text(item.verification_condition, "verification_condition"),
    status,
    version: integer(item.version, "version"),
    created_by: text(item.created_by, "created_by"),
    created_at: text(item.created_at, "created_at"),
    updated_at: text(item.updated_at, "updated_at"),
    verification,
  };
}

function parseReceipt(value: unknown): EvidenceReceipt {
  const item = object(value, "receipt");
  const documentId = optionalText(item.document_id, "receipt.document_id");
  const answer = optionalText(item.answer, "receipt.answer");
  if ((!documentId) === (!answer)) return invalid("receipt 内容");
  return {
    id: text(item.id, "receipt.id"),
    request_id: text(item.request_id, "receipt.request_id"),
    case_id: text(item.case_id, "receipt.case_id"),
    document_id: documentId,
    answer,
    submitted_by: text(item.submitted_by, "receipt.submitted_by"),
    created_at: text(item.created_at, "receipt.created_at"),
  };
}

function parseEvent(value: unknown): EvidenceRequestEvent {
  const item = object(value, "history event");
  return {
    type: text(item.type, "event.type"),
    actor_id: text(item.actor_id, "event.actor_id"),
    occurred_at: text(item.occurred_at, "event.occurred_at"),
    request: parseEvidenceRequest(item.request),
    receipt: item.receipt === undefined || item.receipt === null ? null : parseReceipt(item.receipt),
  };
}


const base = (caseId: string) => `/api/cases/${encodeURIComponent(caseId)}/evidence-requests`;
const requestPath = (caseId: string, requestId: string) => `${base(caseId)}/${encodeURIComponent(requestId)}`;

export function listEvidenceRequests(caseId: string, signal?: AbortSignal) {
  return requestJson(base(caseId), { method: "GET", signal }, (value) => array(object(value, "list").items, "items", parseEvidenceRequest));
}

export function listEvidenceHistory(caseId: string, requestId: string, signal?: AbortSignal) {
  return requestJson(`${requestPath(caseId, requestId)}/history`, { method: "GET", signal }, (value) => array(object(value, "history").items, "items", parseEvent));
}

export function createEvidenceRequest(caseId: string, input: CreateEvidenceRequestInput, key: string) {
  return requestJson(base(caseId), jsonInit("POST", input, { "Idempotency-Key": key }), parseEvidenceRequest);
}

export function createEvidenceReceipt(caseId: string, requestId: string, input: CreateEvidenceReceiptInput, key: string) {
  return requestJson(`${requestPath(caseId, requestId)}/receipts`, jsonInit("POST", input, { "Idempotency-Key": key }), (value) => {
    const item = object(value, "receipt result");
    return { request: parseEvidenceRequest(item.request), receipt: parseReceipt(item.receipt) };
  });
}

export function verifyEvidenceRequest(caseId: string, requestId: string, receiptId: string, input: VerifyEvidenceRequestInput, key: string) {
  return requestJson(`${requestPath(caseId, requestId)}/receipts/${encodeURIComponent(receiptId)}/verify`, jsonInit("POST", input, { "Idempotency-Key": key }), parseEvidenceRequest);
}

export function uploadEvidenceDocument(caseId: string, file: File, role: string) {
  const body = new FormData();
  body.set("file", file);
  body.set("role", role);
  return requestJson(`/api/cases/${encodeURIComponent(caseId)}/documents`, { method: "POST", body }, (value) => {
    const id = text(object(value, "uploaded document").id, "uploaded document.id");
    if (!id) return invalid("uploaded document.id");
    return { id };
  });
}
