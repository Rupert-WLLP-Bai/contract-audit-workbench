/**
 * Test-only fixtures for the operator-audit panels.
 *
 * No production component or API module imports this file. Every fixture that
 * represents external data carries `dataClass: "synthetic"` so the UI cannot
 * present it as a live production record.
 */

import type {
  CapabilityPack,
  CheckResult,
  ExternalCollectionJob,
  ExternalObservation,
  FulfillmentComparison,
  ImpactPlan,
  ObjectBinding,
} from "./types";

export const dedicatedLinePack: CapabilityPack = {
  packId: "operator.dedicated_line",
  version: "1.0.0",
  displayName: "专线配置核验（合成示例）",
  scenario: "telecom_dedicated_line",
  status: "published",
  hostContractVersion: "auditext/v1",
  requiredMaterialRoles: ["draft_contract", "procurement"],
  rulePack: {
    id: "fixture.rulepack.dedicated_line",
    version: "1.0.0",
  },
  capabilities: [
    {
      id: "operator.oss.read_config",
      effect: "read",
      connectorRef: "fixture.oss",
    },
  ],
  checks: [
    {
      id: "line.configured_bandwidth",
      comparatorRef: "builtin.configured_bandwidth_at_least/v1",
      contractField: "minimum_bandwidth",
      externalField: "configured_bandwidth",
    },
  ],
  dataClass: "synthetic",
};

export const cloudServicesPack: CapabilityPack = {
  packId: "operator.cloud_services",
  version: "1.0.0",
  displayName: "云服务规则复用（合成示例）",
  scenario: "cloud_services",
  status: "published",
  hostContractVersion: "auditext/v1",
  requiredMaterialRoles: ["draft_contract", "procurement"],
  rulePack: {
    id: "fixture.rulepack.cloud_services",
    version: "1.0.0",
  },
  capabilities: [],
  checks: [
    {
      id: "cloud.existing_bandwidth",
      comparatorRef: "existing.minimum_bandwidth",
      contractField: "minimum_bandwidth",
      externalField: null,
    },
  ],
  dataClass: "synthetic",
};

export const capabilityPacks: CapabilityPack[] = [
  dedicatedLinePack,
  cloudServicesPack,
];

export const confirmedBinding: ObjectBinding = {
  id: "binding-1",
  version: "2",
  status: "confirmed",
  contractObject: {
    objectType: "contract_line",
    objectId: "line-1",
    label: "政企专线合同",
  },
  externalObject: {
    connectorId: "fixture.oss",
    externalObjectId: "fixture-line-001",
  },
  confirmedBy: "reviewer-1",
  confirmedAt: "2026-10-28T07:50:00Z",
  effectiveFrom: "2026-10-01T00:00:00Z",
  effectiveUntil: "2026-11-01T00:00:00Z",
};

export const ambiguousBinding: ObjectBinding = {
  id: "binding-2",
  version: "1",
  status: "ambiguous",
  contractObject: {
    objectType: "contract_line",
    objectId: "line-1",
    label: "政企专线合同",
  },
  externalObject: {
    connectorId: "fixture.oss",
    externalObjectId: null,
  },
  confirmedBy: null,
  confirmedAt: null,
  effectiveFrom: null,
  effectiveUntil: null,
};

export const availableObservation: ExternalObservation = {
  snapshotId: "fixture-snapshot-001",
  connectorId: "fixture.oss",
  sourceSystem: "fixture-oss-east",
  objectBindingVersion: "fixture-binding-v1",
  externalObjectId: "fixture-line-001",
  recordId: "fixture-record-001",
  recordVersion: "fixture-source-v1",
  observedAt: "2026-10-28T08:00:00Z",
  sourceUpdatedAt: "2026-10-28T07:55:00Z",
  effectiveFrom: "2026-10-01T00:00:00Z",
  effectiveUntil: "2026-11-01T00:00:00Z",
  availability: "available",
  reasonCode: "read_succeeded",
  payloadRef: "fixture://oss/record-001.json",
  fields: [
    {
      name: "configured_bandwidth",
      value: "80",
      unit: "Mbit/s",
      sourcePointer: "/serviceCharacteristic/0/value",
      semanticKind: "configured_value",
    },
  ],
  dataClass: "synthetic",
};

export const staleObservation: ExternalObservation = {
  ...availableObservation,
  snapshotId: "fixture-snapshot-stale",
  recordId: "fixture-record-stale",
  observedAt: "2026-09-15T08:00:00Z",
  sourceUpdatedAt: null,
  effectiveFrom: "2026-09-01T00:00:00Z",
  effectiveUntil: "2026-10-20T00:00:00Z",
  availability: "stale",
  reasonCode: "observation_expired",
  fields: [],
};

export const unavailableObservation: ExternalObservation = {
  ...availableObservation,
  snapshotId: "fixture-snapshot-unavailable",
  recordId: "fixture-record-unavailable",
  availability: "unavailable",
  reasonCode: "source_unreachable",
  payloadRef: null,
  fields: [],
};

export const rejectedObservation: ExternalObservation = {
  ...availableObservation,
  snapshotId: "fixture-snapshot-rejected",
  recordId: "fixture-record-rejected",
  availability: "rejected",
  reasonCode: "authorization_revoked",
  payloadRef: null,
  fields: [],
};

export const collectionJobs: ExternalCollectionJob[] = [
  {
    jobId: "job-running",
    state: "running",
    connectorId: "fixture.oss",
    externalObjectId: "fixture-line-001",
    acceptedAt: "2026-10-28T07:58:00Z",
  },
  {
    jobId: "job-denied",
    state: "denied",
    connectorId: "fixture.oss",
    externalObjectId: "fixture-line-001",
    acceptedAt: "2026-10-28T07:59:00Z",
  },
];

export const conflictCheck: CheckResult = {
  checkId: "line.configured_bandwidth",
  relation: "conflict",
  reasonCode: "configured_bandwidth_below_contract",
  machineStatus: "completed",
  evidence: [
    {
      kind: "document",
      documentVersionId: "fixture-doc-v1",
      parsedVersionId: "fixture-parse-v1",
      start: 0,
      end: 24,
      quote: "合成示例：本线路最低保障带宽100Mbit/s。",
    },
    {
      kind: "external_snapshot",
      snapshotId: "fixture-snapshot-001",
      recordId: "fixture-record-001",
      fieldPointer: "/serviceCharacteristic/0/value",
      displayValue: "80 Mbit/s",
      observedAt: "2026-10-28T08:00:00Z",
    },
  ],
  requiresHumanReview: true,
  limitations: [
    "只表明合成配置记录与合同要求不一致，不证明实际吞吐或赔偿责任。",
  ],
};

export const compatibleCheck: CheckResult = {
  checkId: "line.monthly_availability",
  relation: "compatible",
  reasonCode: "configured_availability_meets_contract",
  machineStatus: "completed",
  evidence: [
    {
      kind: "document",
      documentVersionId: "fixture-doc-v1",
      parsedVersionId: "fixture-parse-v1",
      start: 25,
      end: 48,
      quote: "合成示例：月可用率不低于99.9%。",
    },
  ],
  requiresHumanReview: false,
  limitations: [],
};

export const fulfillmentComparisons: FulfillmentComparison[] = [
  {
    check: conflictCheck,
    contractValue: { value: "100", unit: "Mbit/s" },
    configuredValue: { value: "80", unit: "Mbit/s" },
  },
  {
    check: compatibleCheck,
    contractValue: { value: "99.9", unit: "%" },
    configuredValue: { value: "99.95", unit: "%" },
  },
];

export const impactPlan: ImpactPlan = {
  schemaVersion: "impact-plan/v1",
  baseEvaluationId: "fixture-eval-v1",
  targetEvaluationId: "fixture-eval-v2",
  mode: "shadow",
  dependencyComplete: true,
  fallback: "none",
  fallbackReason: "complete_declared_dependencies",
  changedInputs: ["external.configured_bandwidth"],
  checks: [
    {
      checkId: "line.configured_bandwidth",
      action: "recompute",
      reasons: ["dependent_field_changed"],
    },
    {
      checkId: "line.existing_advance_payment",
      action: "reuse_candidate",
      reasons: ["unchanged_complete_dependencies"],
    },
    {
      checkId: "line.authorization_basis",
      action: "deny",
      reasons: ["authorization_revoked"],
    },
  ],
  businessResultSource: "full",
  dataClass: "synthetic",
};
