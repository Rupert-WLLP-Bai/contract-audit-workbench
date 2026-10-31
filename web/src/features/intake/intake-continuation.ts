import { isRecord } from "../../api/guards";
import { idempotencyFingerprint } from "../../api/idempotency";
import { INTAKE_MAX_FILE_BYTES, type IntakeRequestIdentity, type IntakeResult, type IntakeState, type ServerIntakeContinuation } from "../../api/intakes";
import type { Actor } from "../../api/types";

/**
 * Actor+tenant+audience scoped continuation metadata for the homepage intake.
 * Only the idempotency key, canonical request identity, file SHA-256 and the
 * server receipt are stored. File bytes, cookies and tokens never are.
 */

export interface IntakeContinuationScope {
  audience: string;
  actor_id: string;
  tenant_id: string;
  org_id: string;
}


export interface IntakeReceipt {
  operation_id: string | null;
  case_id: string | null;
  state: IntakeState | null;
  material_id: string | null;
  material_version: number | null;
  document_id: string | null;
  error_code: string | null;
  retryable: boolean;
}

export interface IntakeContinuation {
  version: 1;
  scope: IntakeContinuationScope;
  idempotency_key: string;
  request: IntakeRequestIdentity;
  receipt: IntakeReceipt;
  updated_at: string;
}

export interface IntakeContinuationSelection {
  detail: ServerIntakeContinuation;
  error_code: string | null;
  archived: boolean;
}

const STORAGE_PREFIX = "caw:intake-continuation:";
const KEY_PATTERN = /^[\x21-\x7e]{1,200}$/;
const SHA256_PATTERN = /^[a-f0-9]{64}$/;

export function pendingReceipt(): IntakeReceipt {
  return {
    operation_id: null,
    case_id: null,
    state: null,
    material_id: null,
    material_version: null,
    document_id: null,
    error_code: null,
    retryable: false,
  };
}

export function receiptFromResult(result: IntakeResult): IntakeReceipt {
  return {
    operation_id: result.operation_id,
    case_id: result.case_id,
    state: result.state,
    material_id: result.material_id,
    material_version: result.material_version,
    document_id: result.document_id,
    error_code: result.error_code,
    retryable: result.retryable,
  };
}

export function scopeOf(actor: Actor): IntakeContinuationScope {
  return {
    audience: actor.audience,
    actor_id: actor.id,
    tenant_id: actor.tenant_id,
    org_id: actor.org_id,
  };
}

export function scopeMatches(
  scope: IntakeContinuationScope,
  actor: Actor,
): boolean {
  const current = scopeOf(actor);
  return (
    scope.audience === current.audience &&
    scope.actor_id === current.actor_id &&
    scope.tenant_id === current.tenant_id &&
    scope.org_id === current.org_id
  );
}

async function storageKey(
  scope: IntakeContinuationScope,
): Promise<string> {
  const fingerprint = await idempotencyFingerprint(
    [scope.audience, scope.tenant_id, scope.org_id, scope.actor_id].join(
      "\u0000",
    ),
  );
  return `${STORAGE_PREFIX}${fingerprint}`;
}

function textField(
  value: unknown,
  { allowEmpty = false }: { allowEmpty?: boolean } = {},
): string | null {
  if (typeof value !== "string") return null;
  if (!allowEmpty && value.trim() === "") return null;
  return value;
}

function nullableId(value: unknown): string | null | undefined {
  if (value === null) return null;
  const result = textField(value);
  return result === null ? undefined : result;
}

function parseReceipt(value: unknown): IntakeReceipt | null {
  if (!isRecord(value)) return null;
  const operationId = nullableId(value.operation_id);
  const caseId = nullableId(value.case_id);
  const materialId = nullableId(value.material_id);
  const documentId = nullableId(value.document_id);
  const errorCode = nullableId(value.error_code);
  if (
    operationId === undefined ||
    caseId === undefined ||
    materialId === undefined ||
    documentId === undefined ||
    errorCode === undefined
  )
    return null;
  const rawState = value.state;
  let state: IntakeState | null;
  if (rawState === null) state = null;
  else if (rawState === "upload_pending") state = "upload_pending";
  else if (rawState === "completed") state = "completed";
  else return null;
  const rawVersion = value.material_version;
  let version: number | null;
  if (rawVersion === null) version = null;
  else if (
    typeof rawVersion === "number" &&
    Number.isSafeInteger(rawVersion) &&
    rawVersion >= 1
  )
    version = rawVersion;
  else return null;
  const retryable = value.retryable;
  if (typeof retryable !== "boolean") return null;
  if (retryable !== (state === "upload_pending")) return null;
  if ((state === null) !== (operationId === null)) return null;
  if ((state === null) !== (caseId === null)) return null;
  if (state === "completed" && (materialId === null || documentId === null || version === null))
    return null;
  if (state === "upload_pending" && (materialId !== null || documentId !== null || version !== null))
    return null;
  return {
    operation_id: operationId,
    case_id: caseId,
    state,
    material_id: materialId,
    material_version: version,
    document_id: documentId,
    error_code: errorCode,
    retryable,
  };
}

function parseRequest(value: unknown): IntakeRequestIdentity | null {
  if (!isRecord(value)) return null;
  const title = textField(value.title);
  const contractType = textField(value.contract_type);
  const businessLine = textField(value.business_line, { allowEmpty: true });
  const scenario = textField(value.scenario);
  const fileName = textField(value.file_name);
  const mediaType = textField(value.media_type, { allowEmpty: true });
  const sha = textField(value.file_sha256);
  if (
    title === null ||
    contractType === null ||
    businessLine === null ||
    scenario === null ||
    fileName === null ||
    mediaType === null ||
    sha === null ||
    !SHA256_PATTERN.test(sha)
  )
    return null;
  const size = value.file_size;
  if (
    typeof size !== "number" ||
    !Number.isSafeInteger(size) ||
    size < 0 ||
    size > INTAKE_MAX_FILE_BYTES
  )
    return null;
  return {
    title,
    contract_type: contractType,
    business_line: businessLine,
    scenario,
    file_name: fileName,
    file_size: size,
    media_type: mediaType,
    file_sha256: sha,
  };
}

export function parseIntakeContinuation(
  raw: string,
  actor: Actor,
): IntakeContinuation | null {
  let value: unknown;
  try {
    value = JSON.parse(raw) as unknown;
  } catch {
    return null;
  }
  if (!isRecord(value) || value.version !== 1) return null;
  const scope = value.scope;
  if (!isRecord(scope)) return null;
  const audience = textField(scope.audience);
  const actorId = textField(scope.actor_id);
  const tenantId = textField(scope.tenant_id);
  const orgId = textField(scope.org_id);
  if (
    audience === null ||
    actorId === null ||
    tenantId === null ||
    orgId === null
  )
    return null;
  const parsedScope: IntakeContinuationScope = {
    audience,
    actor_id: actorId,
    tenant_id: tenantId,
    org_id: orgId,
  };
  if (!scopeMatches(parsedScope, actor)) return null;
  const idempotencyKey = textField(value.idempotency_key);
  if (idempotencyKey === null || !KEY_PATTERN.test(idempotencyKey))
    return null;
  const request = parseRequest(value.request);
  const receipt = parseReceipt(value.receipt);
  const updatedAt = textField(value.updated_at);
  if (
    request === null ||
    receipt === null ||
    updatedAt === null ||
    Number.isNaN(Date.parse(updatedAt))
  )
    return null;
  return {
    version: 1,
    scope: parsedScope,
    idempotency_key: idempotencyKey,
    request,
    receipt,
    updated_at: updatedAt,
  };
}

export async function readIntakeContinuation(
  actor: Actor,
): Promise<IntakeContinuation | null> {
  const key = await storageKey(scopeOf(actor));
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(key);
  } catch {
    return null;
  }
  if (raw === null) return null;
  const parsed = parseIntakeContinuation(raw, actor);
  if (parsed === null) {
    try {
      localStorage.removeItem(key);
    } catch {
      /* A blocked storage area keeps the unreadable value; nothing is sent. */
    }
  }
  return parsed;
}

export async function writeIntakeContinuation(
  continuation: IntakeContinuation,
): Promise<void> {
  const key = await storageKey(continuation.scope);
  try {
    localStorage.setItem(key, JSON.stringify(continuation));
  } catch {
    throw new Error(
      "浏览器无法保存续办记录，请允许本站使用本地存储后重试",
    );
  }
}

export async function clearIntakeContinuation(actor: Actor): Promise<void> {
  try {
    localStorage.removeItem(await storageKey(scopeOf(actor)));
  } catch {
    /* Clearing is best effort; the scoped record is never sent anywhere else. */
  }
}

export function withReceipt(
  continuation: IntakeContinuation,
  result: IntakeResult,
): IntakeContinuation {
  return {
    ...continuation,
    receipt: receiptFromResult(result),
    updated_at: new Date().toISOString(),
  };
}

/**
 * The server binds a continuation to its original filename, size and digest.
 * Browser MIME reporting may differ, so replay restores the stored media type.
 */
export function continuationFileProblem(
  continuation: IntakeContinuation,
  file: File,
  sha256: string,
): string | null {
  const expected = continuation.request;
  if (sha256 !== expected.file_sha256)
    return "所选文件的内容与最初提交的主合同不一致。请选择同一个文件。";
  if (file.name !== expected.file_name)
    return "所选文件的文件名与最初提交不一致。请选择同一个文件。";
  if (file.size !== expected.file_size)
    return "所选文件的大小与最初提交不一致。请选择同一个文件。";
  return null;
}

export function shortDigest(sha256: string): string {
  return sha256.slice(0, 12);
}
