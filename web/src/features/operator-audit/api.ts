/**
 * Injected client for the draft operator-audit endpoints.
 *
 * Paths follow the draft contract in `contracts/openapi-delta.yaml`. They are
 * additive paths only; this module never imports or changes the shared API
 * layer beyond `requestJson` / `jsonInit` / `ApiError`.
 *
 * Parsers are intentionally tolerant about optional leaf fields: a missing or
 * wrongly typed value becomes `null` / `unknown` so the UI says "未报告"
 * instead of fabricating `0`, `false`, or a business relation. Identity fields
 * that a list row needs are still required, otherwise the response is invalid.
 */

import { ApiError, jsonInit, requestJson } from "../../api/http";
import type {
  Availability,
  BindingStatus,
  CapabilityPack,
  CheckResult,
  CreateExternalCollectionJobInput,
  CreateObjectBindingInput,
  EvidenceRef,
  ExternalCollectionJob,
  ExternalField,
  ExternalObservation,
  FulfillmentEvaluation,
  FulfillmentEvaluationInput,
  FulfillmentEvaluationRef,
  ImpactAction,
  ImpactCheck,
  ImpactFallback,
  ImpactMode,
  ImpactPlan,
  MachineStatus,
  ObjectBinding,
  PackRef,
  Relation,
} from "./types";

export interface OperatorAuditClient {
  listCapabilityPacks(signal?: AbortSignal): Promise<CapabilityPack[]>;
  createObjectBinding(
    caseId: string,
    input: CreateObjectBindingInput,
    idempotencyKey: string,
  ): Promise<ObjectBinding>;
  createExternalCollectionJob(
    caseId: string,
    input: CreateExternalCollectionJobInput,
    idempotencyKey: string,
  ): Promise<ExternalCollectionJob>;
  listExternalSnapshots(
    caseId: string,
    signal?: AbortSignal,
  ): Promise<ExternalObservation[]>;
  createFulfillmentEvaluation(
    caseId: string,
    input: FulfillmentEvaluationInput,
    idempotencyKey: string,
  ): Promise<FulfillmentEvaluationRef>;
  getFulfillmentEvaluation(
    caseId: string,
    evaluationId: string,
    signal?: AbortSignal,
  ): Promise<FulfillmentEvaluation>;
  getImpactPlan(
    caseId: string,
    evaluationId: string,
    signal?: AbortSignal,
  ): Promise<ImpactPlan>;
}

function invalid(field: string): never {
  throw new ApiError(
    `外部审查响应中的 ${field} 无法识别`,
    200,
    "invalid_response",
    false,
    null,
  );
}

function asRecord(
  value: unknown,
  field: string,
): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value))
    return invalid(field);
  return value as Record<string, unknown>;
}

/** Accept both `{ [key]: value }` and the bare object for single resources. */
function unwrap(
  value: unknown,
  key: string,
  field: string,
): Record<string, unknown> {
  const item = asRecord(value, field);
  const nested = item[key];
  if (nested === undefined || nested === null) return item;
  return asRecord(nested, `${field}.${key}`);
}

/** Empty strings are missing values, not empty conclusions. */
function optionalString(value: unknown): string | null {
  if (typeof value !== "string") return null;
  return value.trim() === "" ? null : value;
}

function requiredString(value: unknown, field: string): string {
  const text = optionalString(value);
  if (text === null) return invalid(field);
  return text;
}

function optionalNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function optionalBoolean(value: unknown): boolean | null {
  return typeof value === "boolean" ? value : null;
}

function stringList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter(
    (entry): entry is string =>
      typeof entry === "string" && entry.trim() !== "",
  );
}

function recordList(value: unknown, field: string): unknown[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) return invalid(field);
  return value;
}

function listItems(value: unknown, field: string): unknown[] {
  const items = Array.isArray(value) ? value : asRecord(value, field).items;
  if (!Array.isArray(items)) return invalid(field);
  return items;
}

function relation(value: unknown): Relation {
  switch (value) {
    case "compatible":
    case "conflict":
    case "insufficient":
    case "not_applicable":
      return value;
    default:
      return "unknown";
  }
}

function machineStatus(value: unknown): MachineStatus {
  switch (value) {
    case "completed":
    case "partial":
    case "failed":
    case "cancelled":
      return value;
    default:
      return "unknown";
  }
}

function availability(value: unknown): Availability {
  switch (value) {
    case "available":
    case "unavailable":
    case "stale":
    case "rejected":
      return value;
    default:
      return "unknown";
  }
}

function bindingStatus(value: unknown): BindingStatus {
  switch (value) {
    case "confirmed":
    case "missing":
    case "ambiguous":
      return value;
    default:
      return "unknown";
  }
}

function impactMode(value: unknown): ImpactMode {
  return value === "explain" || value === "shadow" ? value : "unknown";
}

function impactAction(value: unknown): ImpactAction {
  switch (value) {
    case "recompute":
    case "reuse_candidate":
    case "deny":
      return value;
    default:
      return "unknown";
  }
}

function impactFallback(value: unknown): ImpactFallback {
  switch (value) {
    case "none":
    case "full":
    case "deny":
      return value;
    default:
      return "unknown";
  }
}

export function parsePackRef(value: unknown): PackRef | null {
  if (value === undefined || value === null) return null;
  const item = asRecord(value, "pack ref");
  const id = optionalString(item.id);
  if (id === null) return null;
  return { id, version: optionalString(item.version) };
}

export function parseCapabilityPack(value: unknown): CapabilityPack {
  const item = asRecord(value, "capability pack");
  const capabilities = recordList(item.capabilities, "capabilities").map(
    (entry) => {
      const capability = asRecord(entry, "capability");
      return {
        id: requiredString(capability.id, "capability.id"),
        effect: optionalString(capability.effect),
        connectorRef: optionalString(capability.connector_ref),
      };
    },
  );
  const checks = recordList(item.checks, "checks").map((entry) => {
    const check = asRecord(entry, "capability check");
    return {
      id: requiredString(check.id, "check.id"),
      comparatorRef: optionalString(check.comparator_ref),
      contractField: optionalString(check.contract_field),
      externalField: optionalString(check.external_field),
    };
  });
  return {
    packId: requiredString(item.pack_id, "pack_id"),
    version: optionalString(item.version),
    displayName: optionalString(item.display_name),
    scenario: optionalString(item.scenario),
    status: optionalString(item.status),
    hostContractVersion: optionalString(item.host_contract_version),
    requiredMaterialRoles: stringList(item.required_material_roles),
    rulePack: parsePackRef(item.rule_pack_ref),
    capabilities,
    checks,
    dataClass: optionalString(item.data_class),
  };
}

export function parseCapabilityPackList(value: unknown): CapabilityPack[] {
  return listItems(value, "capability packs").map(parseCapabilityPack);
}

export function parseObjectBinding(value: unknown): ObjectBinding {
  const item = unwrap(value, "binding", "object binding");
  const contract =
    item.contract_object === undefined || item.contract_object === null
      ? null
      : asRecord(item.contract_object, "contract_object");
  const external =
    item.external_object === undefined || item.external_object === null
      ? null
      : asRecord(item.external_object, "external_object");
  return {
    id: requiredString(item.binding_id ?? item.id, "binding_id"),
    version: optionalString(item.version),
    status: bindingStatus(item.status ?? item.binding_status),
    contractObject: {
      objectType: contract ? optionalString(contract.object_type) : null,
      objectId: contract ? optionalString(contract.object_id) : null,
      label: contract ? optionalString(contract.label) : null,
    },
    externalObject: {
      connectorId: external ? optionalString(external.connector_id) : null,
      externalObjectId: external
        ? optionalString(external.external_object_id)
        : null,
    },
    confirmedBy: optionalString(item.confirmed_by),
    confirmedAt: optionalString(item.confirmed_at),
    effectiveFrom: optionalString(item.effective_from),
    effectiveUntil: optionalString(item.effective_until),
  };
}

export function parseExternalCollectionJob(
  value: unknown,
): ExternalCollectionJob {
  const item = unwrap(value, "job", "external collection job");
  return {
    jobId: requiredString(item.job_id ?? item.id, "job_id"),
    state: optionalString(item.state ?? item.status),
    connectorId: optionalString(item.connector_id),
    externalObjectId: optionalString(item.external_object_id),
    acceptedAt: optionalString(item.accepted_at ?? item.created_at),
  };
}

export function parseExternalField(value: unknown): ExternalField {
  const item = asRecord(value, "external field");
  return {
    name: requiredString(item.name, "field.name"),
    value: optionalString(item.value),
    unit: optionalString(item.unit),
    sourcePointer: optionalString(item.source_pointer),
    semanticKind: optionalString(item.semantic_kind),
  };
}

export function parseExternalObservation(value: unknown): ExternalObservation {
  const item = asRecord(value, "external observation");
  return {
    snapshotId: requiredString(item.snapshot_id, "snapshot_id"),
    connectorId: optionalString(item.connector_id),
    sourceSystem: optionalString(item.source_system),
    objectBindingVersion: optionalString(item.object_binding_version),
    externalObjectId: optionalString(item.external_object_id),
    recordId: optionalString(item.record_id),
    recordVersion: optionalString(item.record_version),
    observedAt: optionalString(item.observed_at),
    sourceUpdatedAt: optionalString(item.source_updated_at),
    effectiveFrom: optionalString(item.effective_from),
    effectiveUntil: optionalString(item.effective_until),
    availability: availability(item.availability),
    reasonCode: optionalString(item.reason_code),
    payloadRef: optionalString(item.payload_ref),
    fields: recordList(item.fields, "fields").map(parseExternalField),
    dataClass: optionalString(item.data_class),
  };
}

export function parseExternalObservationList(
  value: unknown,
): ExternalObservation[] {
  return listItems(value, "external snapshots").map(parseExternalObservation);
}

export function parseEvidence(value: unknown): EvidenceRef {
  const item = asRecord(value, "evidence");
  if (item.kind === "document") {
    return {
      kind: "document",
      documentVersionId: optionalString(item.document_version_id),
      parsedVersionId: optionalString(item.parsed_version_id),
      start: optionalNumber(item.start),
      end: optionalNumber(item.end),
      quote: optionalString(item.quote),
    };
  }
  if (item.kind === "external_snapshot") {
    return {
      kind: "external_snapshot",
      snapshotId: optionalString(item.snapshot_id),
      recordId: optionalString(item.record_id),
      fieldPointer: optionalString(item.field_pointer),
      displayValue: optionalString(item.display_value),
      observedAt: optionalString(item.observed_at),
    };
  }
  return { kind: "unknown", rawKind: optionalString(item.kind) };
}

export function parseCheckResult(value: unknown): CheckResult {
  const item = asRecord(value, "check result");
  return {
    checkId: requiredString(item.check_id, "check_id"),
    relation: relation(item.relation),
    reasonCode: optionalString(item.reason_code),
    machineStatus: machineStatus(item.machine_status),
    evidence: recordList(item.evidence, "evidence").map(parseEvidence),
    requiresHumanReview: optionalBoolean(item.requires_human_review),
    limitations: stringList(item.limitations),
  };
}

export function parseFulfillmentEvaluationRef(
  value: unknown,
): FulfillmentEvaluationRef {
  const item = unwrap(value, "evaluation", "fulfillment evaluation ref");
  return {
    evaluationId: requiredString(
      item.evaluation_id ?? item.id,
      "evaluation_id",
    ),
    jobId: optionalString(item.job_id),
  };
}

export function parseFulfillmentEvaluation(
  value: unknown,
): FulfillmentEvaluation {
  const item = unwrap(value, "evaluation", "fulfillment evaluation");
  return {
    evaluationId: requiredString(
      item.evaluation_id ?? item.id,
      "evaluation_id",
    ),
    pack: parsePackRef(item.pack_ref ?? item.pack),
    contractVersion: optionalString(
      item.contract_version ?? item.contract_version_id,
    ),
    rulePackVersion: optionalString(item.rule_pack_version),
    objectBindingVersion: optionalString(item.object_binding_version),
    snapshotSetId: optionalString(item.snapshot_set_id),
    businessAt: optionalString(item.business_at),
    checks: recordList(item.checks, "checks").map(parseCheckResult),
    dataClass: optionalString(item.data_class),
  };
}

function parseImpactCheck(value: unknown): ImpactCheck {
  const item = asRecord(value, "impact check");
  return {
    checkId: requiredString(item.check_id, "check_id"),
    action: impactAction(item.action),
    reasons: stringList(item.reasons),
  };
}

export function parseImpactPlan(value: unknown): ImpactPlan {
  const item = unwrap(value, "impact", "impact plan");
  return {
    schemaVersion: optionalString(item.schema_version),
    baseEvaluationId: optionalString(item.base_evaluation_id),
    targetEvaluationId: optionalString(item.target_evaluation_id),
    mode: impactMode(item.mode),
    dependencyComplete: optionalBoolean(item.dependency_complete),
    fallback: impactFallback(item.fallback),
    fallbackReason: optionalString(item.fallback_reason),
    changedInputs: stringList(item.changed_inputs),
    checks: recordList(item.checks, "checks").map(parseImpactCheck),
    businessResultSource: optionalString(item.business_result_source),
    dataClass: optionalString(item.data_class),
  };
}

function idempotencyHeaders(key: string): Record<string, string> {
  if (typeof key !== "string" || key.trim() === "") {
    throw new ApiError(
      "写请求缺少幂等键，未发送请求",
      0,
      "invalid_request",
      false,
      null,
    );
  }
  return { "Idempotency-Key": key };
}

function casePath(caseId: string, suffix: string): string {
  return `/api/cases/${encodeURIComponent(caseId)}/${suffix}`;
}

export function createHttpOperatorAuditClient(): OperatorAuditClient {
  return {
    listCapabilityPacks(signal) {
      return requestJson(
        "/api/capability-packs",
        { method: "GET", signal },
        parseCapabilityPackList,
      );
    },
    async createObjectBinding(caseId, input, idempotencyKey) {
      return requestJson(
        casePath(caseId, "object-bindings"),
        jsonInit("POST", input, idempotencyHeaders(idempotencyKey)),
        parseObjectBinding,
      );
    },
    async createExternalCollectionJob(caseId, input, idempotencyKey) {
      return requestJson(
        casePath(caseId, "external-collection-jobs"),
        jsonInit("POST", input, idempotencyHeaders(idempotencyKey)),
        parseExternalCollectionJob,
      );
    },
    listExternalSnapshots(caseId, signal) {
      return requestJson(
        casePath(caseId, "external-snapshots"),
        { method: "GET", signal },
        parseExternalObservationList,
      );
    },
    async createFulfillmentEvaluation(caseId, input, idempotencyKey) {
      return requestJson(
        casePath(caseId, "fulfillment-evaluations"),
        jsonInit("POST", input, idempotencyHeaders(idempotencyKey)),
        parseFulfillmentEvaluationRef,
      );
    },
    getFulfillmentEvaluation(caseId, evaluationId, signal) {
      return requestJson(
        casePath(
          caseId,
          `fulfillment-evaluations/${encodeURIComponent(evaluationId)}`,
        ),
        { method: "GET", signal },
        parseFulfillmentEvaluation,
      );
    },
    getImpactPlan(caseId, evaluationId, signal) {
      return requestJson(
        casePath(
          caseId,
          `fulfillment-evaluations/${encodeURIComponent(evaluationId)}/impact`,
        ),
        { method: "GET", signal },
        parseImpactPlan,
      );
    },
  };
}

export const defaultOperatorAuditClient = createHttpOperatorAuditClient();
