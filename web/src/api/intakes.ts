import { requestJson } from "./http";
import { responseReader } from "./respond";

/**
 * Homepage primary-contract intake. One explicitly chosen operation creates a
 * case and registers exactly one initial primary contract; the server fixes
 * the add/draft_contract/primary_contract intent, so the client never sends
 * intent, role or purpose fields.
 */

export type IntakeState = "upload_pending" | "completed";

export interface IntakeResult {
  operation_id: string;
  case_id: string;
  state: IntakeState;
  material_id: string | null;
  material_version: number | null;
  document_id: string | null;
  retryable: boolean;
  error_code: string | null;
}

export interface IntakeRequestIdentity {
  title: string;
  contract_type: string;
  business_line: string;
  scenario: string;
  file_name: string;
  file_size: number;
  media_type: string;
  file_sha256: string;
}

export interface PendingIntake {
  operation_id: string;
  case_id: string;
  title: string;
  state: "upload_pending";
  file_name: string | null;
  file_size: number | null;
  created_at: string;
  updated_at: string;
  error_code: string | null;
  can_resume: boolean;
  can_archive: boolean;
  reason_code: string;
}

export interface PendingIntakesPage {
  items: PendingIntake[];
  next_cursor: string | null;
}

export interface ServerIntakeContinuation {
  operation_id: string;
  case_id: string;
  state: IntakeState;
  request: IntakeRequestIdentity | null;
  idempotency_key: string | null;
  can_resume: boolean;
  can_archive: boolean;
  reason_code: string;
}

export interface IntakeArchiveResult {
  operation_id: string;
  case_id: string;
  archived_at: string;
}

export interface IntakeCreateInput {
  title: string;
  contract_type: string;
  business_line: string;
  scenario: string;
  file: File;
}

/** Existing UploadMaterial limit; the client rejects larger files before hashing. */
export const INTAKE_MAX_FILE_BYTES = 33_554_432;

const INTAKES_PATH = "/api/intakes";

const { object, text, integer, bool, optionalText } = responseReader(
  (path) => `主合同上传响应中的 ${path} 无法识别`,
);

function invalid(path: string): never {
  throw new Error(`主合同上传响应中的 ${path} 无法识别`);
}

function optionalId(value: unknown, path: string): string | null {
  const result = optionalText(value, path);
  return result === "" ? null : result;
}

export function parseIntakeResult(
  value: unknown,
  path = "intake",
): IntakeResult {
  const record = object(value, path);
  const operationId = text(record.operation_id, `${path}.operation_id`);
  const caseId = text(record.case_id, `${path}.case_id`);
  if (operationId === "" || caseId === "") invalid(path);
  const rawState = record.state;
  const state: IntakeState =
    rawState === "completed"
      ? "completed"
      : rawState === "upload_pending"
        ? "upload_pending"
        : invalid(`${path}.state`);
  const retryable = bool(record.retryable, `${path}.retryable`);
  if (retryable !== (state === "upload_pending"))
    invalid(`${path}.retryable`);
  const materialId = optionalId(record.material_id, `${path}.material_id`);
  const documentId = optionalId(record.document_id, `${path}.document_id`);
  const materialVersion =
    record.material_version === undefined || record.material_version === null
      ? null
      : integer(record.material_version, `${path}.material_version`, 1);
  const errorCode = optionalId(record.error_code, `${path}.error_code`);
  if (
    state === "completed" &&
    (materialId === null || documentId === null || materialVersion === null)
  )
    invalid(`${path}.completed`);
  return {
    operation_id: operationId,
    case_id: caseId,
    state,
    material_id: materialId,
    material_version: materialVersion,
    document_id: documentId,
    retryable,
    error_code: errorCode,
  };
}

export function createIntake(
  input: IntakeCreateInput,
  idempotencyKey: string,
): Promise<IntakeResult> {
  const body = new FormData();
  body.set("title", input.title);
  body.set("contract_type", input.contract_type);
  body.set("business_line", input.business_line);
  body.set("scenario", input.scenario);
  body.set("file", input.file);
  return requestJson(
    INTAKES_PATH,
    {
      method: "POST",
      body,
      headers: { "Idempotency-Key": idempotencyKey },
    },
    parseIntakeResult,
  );
}

/** Recovers a lost first response for the current tenant+actor and key. */
export function getIntakeByKey(
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<IntakeResult> {
  const params = new URLSearchParams({ key: idempotencyKey });
  return requestJson(
    `${INTAKES_PATH}?${params.toString()}`,
    { method: "GET", signal },
    parseIntakeResult,
  );
}

export function getIntake(
  operationId: string,
  signal?: AbortSignal,
): Promise<IntakeResult> {
  return requestJson(
    `${INTAKES_PATH}/${encodeURIComponent(operationId)}`,
    { method: "GET", signal },
    parseIntakeResult,
  );
}

export function parseIntakeRequestIdentity(
  value: unknown,
  path = "intake.request",
): IntakeRequestIdentity {
  const record = object(value, path);
  const identity: IntakeRequestIdentity = {
    title: text(record.title, `${path}.title`),
    contract_type: text(record.contract_type, `${path}.contract_type`),
    business_line: text(record.business_line, `${path}.business_line`),
    scenario: text(record.scenario, `${path}.scenario`),
    file_name: text(record.file_name, `${path}.file_name`),
    file_size: integer(record.file_size, `${path}.file_size`, 0),
    media_type: text(record.media_type, `${path}.media_type`),
    file_sha256: text(record.file_sha256, `${path}.file_sha256`),
  };
  for (const field of ["title", "contract_type", "business_line", "scenario", "file_name"] as const) {
    if (identity[field].trim() === "") invalid(`${path}.${field}`);
  }
  if (identity.file_size > INTAKE_MAX_FILE_BYTES || !/^[a-f0-9]{64}$/.test(identity.file_sha256)) invalid(path);
  return identity;
}

function requiredId(record: Record<string, unknown>, field: string, path: string): string {
  const result = text(record[field], `${path}.${field}`);
  if (result.trim() === "") invalid(`${path}.${field}`);
  return result;
}

function parsePendingIntake(value: unknown, path: string): PendingIntake {
  const record = object(value, path);
  if (record.state !== "upload_pending") invalid(`${path}.state`);
  const fileSize = record.file_size === null
    ? null
    : integer(record.file_size, `${path}.file_size`, 0);
  if (fileSize !== null && fileSize > INTAKE_MAX_FILE_BYTES) invalid(`${path}.file_size`);
  return {
    operation_id: requiredId(record, "operation_id", path),
    case_id: requiredId(record, "case_id", path),
    title: requiredId(record, "title", path),
    state: "upload_pending",
    file_name: optionalId(record.file_name, `${path}.file_name`),
    file_size: fileSize,
    created_at: text(record.created_at, `${path}.created_at`),
    updated_at: text(record.updated_at, `${path}.updated_at`),
    error_code: optionalId(record.error_code, `${path}.error_code`),
    can_resume: bool(record.can_resume, `${path}.can_resume`),
    can_archive: bool(record.can_archive, `${path}.can_archive`),
    reason_code: text(record.reason_code, `${path}.reason_code`),
  };
}

function parsePendingIntakesPage(value: unknown): PendingIntakesPage {
  const record = object(value, "pending_intakes");
  if (!Array.isArray(record.items)) invalid("pending_intakes.items");
  return {
    items: record.items.map((item, index) => parsePendingIntake(item, `pending_intakes.items[${index}]`)),
    next_cursor: optionalId(record.next_cursor, "pending_intakes.next_cursor"),
  };
}

function parseServerIntakeContinuation(value: unknown): ServerIntakeContinuation {
  const path = "intake_continuation";
  const record = object(value, path);
  if (record.state !== "upload_pending" && record.state !== "completed") invalid(`${path}.state`);
  const request = record.request === null ? null : parseIntakeRequestIdentity(record.request, `${path}.request`);
  const key = optionalId(record.idempotency_key, `${path}.idempotency_key`);
  const canResume = bool(record.can_resume, `${path}.can_resume`);
  const reasonCode = text(record.reason_code, `${path}.reason_code`);
  if (key !== null && !/^[\x21-\x7e]{1,200}$/.test(key)) invalid(`${path}.idempotency_key`);
  if (canResume && (record.state !== "upload_pending" || request === null || key === null || reasonCode !== "")) invalid(path);
  if (!canResume && key !== null) invalid(`${path}.idempotency_key`);
  return {
    operation_id: requiredId(record, "operation_id", path),
    case_id: requiredId(record, "case_id", path),
    state: record.state,
    request,
    idempotency_key: key,
    can_resume: canResume,
    can_archive: bool(record.can_archive, `${path}.can_archive`),
    reason_code: reasonCode,
  };
}

function parseIntakeArchiveResult(value: unknown): IntakeArchiveResult {
  const path = "intake_archive";
  const record = object(value, path);
  return {
    operation_id: requiredId(record, "operation_id", path),
    case_id: requiredId(record, "case_id", path),
    archived_at: text(record.archived_at, `${path}.archived_at`),
  };
}

export function listPendingIntakes(cursor: string | null, signal?: AbortSignal): Promise<PendingIntakesPage> {
  const params = new URLSearchParams({ limit: "10" });
  if (cursor !== null) params.set("cursor", cursor);
  return requestJson(`${INTAKES_PATH}/pending?${params}`, { method: "GET", signal }, parsePendingIntakesPage);
}

export function getIntakeContinuation(operationId: string, signal?: AbortSignal): Promise<ServerIntakeContinuation> {
  return requestJson(`${INTAKES_PATH}/${encodeURIComponent(operationId)}/continuation`, { method: "GET", signal }, parseServerIntakeContinuation);
}

export function archiveIntake(operationId: string): Promise<IntakeArchiveResult> {
  return requestJson(`${INTAKES_PATH}/${encodeURIComponent(operationId)}/archive`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  }, parseIntakeArchiveResult);
}
