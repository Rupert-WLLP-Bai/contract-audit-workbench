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
