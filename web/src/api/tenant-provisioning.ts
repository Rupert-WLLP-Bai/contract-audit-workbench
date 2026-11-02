import { useQuery } from "@tanstack/react-query";
import { jsonInit, requestJson, type CommandContext } from "./http";
import { enumValue, responseReader } from "./respond";

export type ProvisionKind = "provision" | "deactivate" | "reactivate";
export type ProvisionStatus = "pending" | "in_progress" | "reconciliation_required" | "provision_failed" | "completed";
export type ProvisionStepName = "identity" | "tenant_config" | "runtime_ready" | "drain" | "usage_reconcile" | "completed";
export type ProvisionStepState = "pending" | "complete" | "external_unknown" | "failed" | "reconciled";
export type ProvisionReconciliation = "confirmed_complete" | "confirmed_not_applied" | "requires_operator";
export interface TenantProvisionStep {
  name: ProvisionStepName;
  state: ProvisionStepState;
  receipt_sha256: string | null;
  updated_at: string | null;
}
export interface TenantProvisionOperation {
  operation_id: string;
  tenant_id: string;
  kind: ProvisionKind;
  status: ProvisionStatus;
  current_step: ProvisionStepName;
  payload_sha256: string;
  revision: string;
  reconciliation_required: boolean;
  steps: TenantProvisionStep[];
  created_at: string;
  updated_at: string;
}
export interface TenantProvisionOperationsView {
  schema_version: string;
  operations: TenantProvisionOperation[];
  next_cursor: string | null;
}
export interface TenantProvisionInput {
  idempotency_key: string;
  kind: ProvisionKind;
  tenant_id: string;
  tenant_name: string;
  jurisdiction_id: string;
  admin_principal_id: string;
  admin_org_unit_id: string;
  admin_org_unit_name: string;
  tenant_config_version: string;
}
export interface TenantProvisionOperationResult {
  schema_version: string;
  operation: TenantProvisionOperation;
}
export interface TenantProvisionStepInput {
  expected_revision: string;
  state: Exclude<ProvisionStepState, "pending">;
  receipt_sha256: string | null;
  reconciliation: ProvisionReconciliation | "";
}

const { object, text, bool, array, nullableText, invalid } = responseReader((path) => `租户生命周期响应中的 ${path} 无法识别`);
const KINDS = ["provision", "deactivate", "reactivate"] as const;
const STATUSES = ["pending", "in_progress", "reconciliation_required", "provision_failed", "completed"] as const;
const STEP_NAMES = ["identity", "tenant_config", "runtime_ready", "drain", "usage_reconcile", "completed"] as const;
const STEP_STATES = ["pending", "complete", "external_unknown", "failed", "reconciled"] as const;
const RECONCILIATIONS = ["confirmed_complete", "confirmed_not_applied", "requires_operator"] as const;
function canonicalRevision(value: unknown, path: string): string {
  const result = text(value, path);
  return /^(0|[1-9][0-9]*)$/.test(result) ? result : invalid(path);
}
function timestamp(value: string, path: string): string {
  return Number.isFinite(Date.parse(value)) ? value : invalid(path);
}
function digest(value: string, path: string): string {
  return /^[0-9a-f]{64}$/.test(value) ? value : invalid(path);
}
function parseProvisionStep(value: unknown, path: string): TenantProvisionStep {
  const item = object(value, path);
  const name = enumValue(item.name, STEP_NAMES) ?? invalid(`${path}.name`);
  const state = enumValue(item.state, STEP_STATES) ?? invalid(`${path}.state`);
  const receipt = nullableText(item.receipt_sha256, `${path}.receipt_sha256`);
  const updated = nullableText(item.updated_at, `${path}.updated_at`);
  return {
    name, state,
    receipt_sha256: receipt === null ? null : digest(receipt, `${path}.receipt_sha256`),
    updated_at: updated === null ? null : timestamp(updated, `${path}.updated_at`),
  };
}
export function parseTenantProvisionOperation(value: unknown): TenantProvisionOperation {
  const item = object(value, "operation");
  const kind = enumValue(item.kind, KINDS) ?? invalid("operation.kind");
  const status = enumValue(item.status, STATUSES) ?? invalid("operation.status");
  const currentStep = enumValue(item.current_step, STEP_NAMES) ?? invalid("operation.current_step");
  const result: TenantProvisionOperation = {
    operation_id: text(item.operation_id, "operation.operation_id"),
    tenant_id: text(item.tenant_id, "operation.tenant_id"),
    kind, status, current_step: currentStep,
    payload_sha256: digest(text(item.payload_sha256, "operation.payload_sha256"), "operation.payload_sha256"),
    revision: canonicalRevision(item.revision, "operation.revision"),
    reconciliation_required: bool(item.reconciliation_required, "operation.reconciliation_required"),
    steps: array(item.steps, "operation.steps", parseProvisionStep),
    created_at: timestamp(text(item.created_at, "operation.created_at"), "operation.created_at"),
    updated_at: timestamp(text(item.updated_at, "operation.updated_at"), "operation.updated_at"),
  };
  if (!result.operation_id || !result.tenant_id) return invalid("operation.identity");
  return result;
}
function parseOperationsView(value: unknown): TenantProvisionOperationsView {
  const item = object(value, "operations_view");
  const schemaVersion = text(item.schema_version, "schema_version");
  if (schemaVersion !== "tenant-provision-operations/v1") return invalid("schema_version");
  return {
    schema_version: schemaVersion,
    operations: array(item.operations, "operations", (entry) => parseTenantProvisionOperation(entry)),
    next_cursor: nullableText(item.next_cursor, "next_cursor"),
  };
}
function parseOperationResult(value: unknown): TenantProvisionOperationResult {
  const item = object(value, "operation_result");
  const schemaVersion = text(item.schema_version, "schema_version");
  if (schemaVersion !== "tenant-provision-operation/v1") return invalid("schema_version");
  return { schema_version: schemaVersion, operation: parseTenantProvisionOperation(item.operation) };
}
const BASE = "/api/admin/platform/tenant-provision-operations";
export function listTenantProvisionOperations(cursor?: string | null, signal?: AbortSignal) {
  const query = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
  return requestJson(`${BASE}${query}`, { method: "GET", signal }, parseOperationsView);
}
export function getTenantProvisionOperation(operationId: string, signal?: AbortSignal) {
  return requestJson(`${BASE}/${encodeURIComponent(operationId)}`, { method: "GET", signal }, (value) => parseOperationResult(value).operation);
}
export function createTenantProvisionOperation(input: TenantProvisionInput, commandContext: CommandContext) {
  return requestJson(BASE, {
    ...jsonInit("POST", input, { "Idempotency-Key": input.idempotency_key }),
    commandContext,
  }, parseOperationResult);
}
export function recordTenantProvisionStep(operationId: string, input: TenantProvisionStepInput, commandContext: CommandContext) {
  return requestJson(`${BASE}/${encodeURIComponent(operationId)}/step`, {
    ...jsonInit("POST", input), commandContext,
  }, parseOperationResult);
}
export function useTenantProvisionOperations(cursor: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ["tenant-provision-operations", "platform", cursor ?? ""],
    queryFn: ({ signal }) => listTenantProvisionOperations(cursor, signal),
    enabled,
    retry: false,
  });
}
export function useTenantProvisionOperation(operationId: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ["tenant-provision-operation", operationId ?? ""],
    queryFn: ({ signal }) => getTenantProvisionOperation(operationId!, signal),
    enabled: enabled && operationId !== null,
    retry: false,
  });
}
