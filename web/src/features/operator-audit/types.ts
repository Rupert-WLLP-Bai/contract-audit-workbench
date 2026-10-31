/**
 * Local view models for the operator-audit extension (D line).
 *
 * These types describe only what the panels need to render. They are a client
 * projection of the draft `auditext` contract, not a copy of the Go package.
 * Optional server fields stay `null` so the UI can say "未报告" instead of
 * inventing a zero, an empty success, or a business conclusion.
 *
 * None of these types are exported from the shared `src/api/types.ts`.
 */

/** Deterministic business relation. Unknown values never become a conclusion. */
export type Relation =
  | "compatible"
  | "conflict"
  | "insufficient"
  | "not_applicable"
  | "unknown";

/** Execution status, kept separate from the business relation. */
export type MachineStatus =
  | "completed"
  | "partial"
  | "failed"
  | "cancelled"
  | "unknown";

/** Whether an external observation can be used as evidence. */
export type Availability =
  | "available"
  | "unavailable"
  | "stale"
  | "rejected"
  | "unknown";

/** Human-confirmed mapping state. Missing and ambiguous stay evidence-insufficient. */
export type BindingStatus = "confirmed" | "missing" | "ambiguous" | "unknown";

export type ImpactMode = "explain" | "shadow" | "unknown";
export type ImpactAction = "recompute" | "reuse_candidate" | "deny" | "unknown";
export type ImpactFallback = "none" | "full" | "deny" | "unknown";

export interface PackRef {
  id: string;
  version: string | null;
}

export interface CapabilityPackCapability {
  id: string;
  effect: string | null;
  connectorRef: string | null;
}

export interface CapabilityPackCheck {
  id: string;
  comparatorRef: string | null;
  contractField: string | null;
  externalField: string | null;
}

export interface CapabilityPack {
  packId: string;
  version: string | null;
  displayName: string | null;
  scenario: string | null;
  /** Catalog lifecycle status when the server reports one; null means unknown. */
  status: string | null;
  hostContractVersion: string | null;
  requiredMaterialRoles: string[];
  rulePack: PackRef | null;
  capabilities: CapabilityPackCapability[];
  checks: CapabilityPackCheck[];
  dataClass: string | null;
}

export interface ContractObjectRef {
  objectType: string | null;
  objectId: string | null;
  label: string | null;
}

export interface ExternalObjectRef {
  connectorId: string | null;
  externalObjectId: string | null;
}

/** A versioned, human-confirmed contract object to external object mapping. */
export interface ObjectBinding {
  id: string;
  version: string | null;
  status: BindingStatus;
  contractObject: ContractObjectRef;
  externalObject: ExternalObjectRef;
  confirmedBy: string | null;
  confirmedAt: string | null;
  effectiveFrom: string | null;
  effectiveUntil: string | null;
}

/**
 * Server-bound confirmation request. `confirmed: true` must be set only after a
 * human selected both objects explicitly; the client never sends a guessed pair.
 */
export interface CreateObjectBindingInput {
  contract_object: {
    object_type: string;
    object_id: string;
    label: string | null;
  };
  external_object: {
    connector_id: string;
    external_object_id: string;
  };
  service_object_id: string;
  expected_object_version: number;
  expected_association_version: number;
  effective_from: string | null;
  effective_until: string | null;
  confirmed: true;
}

/** A collection job reference. Acceptance is not evidence and not a conclusion. */
export interface ExternalCollectionJob {
  jobId: string;
  state: string | null;
  connectorId: string | null;
  externalObjectId: string | null;
  acceptedAt: string | null;
}

export interface CreateExternalCollectionJobInput {
  connector_id: string;
  external_object_id: string;
  object_binding_version: string;
  business_at: string;
}

export interface ExternalField {
  name: string;
  value: string | null;
  unit: string | null;
  sourcePointer: string | null;
  semanticKind: string | null;
}

/** Immutable normalized snapshot metadata plus its normalized fields. */
export interface ExternalObservation {
  snapshotId: string;
  connectorId: string | null;
  sourceSystem: string | null;
  objectBindingVersion: string | null;
  externalObjectId: string | null;
  recordId: string | null;
  recordVersion: string | null;
  observedAt: string | null;
  sourceUpdatedAt: string | null;
  effectiveFrom: string | null;
  effectiveUntil: string | null;
  availability: Availability;
  reasonCode: string | null;
  payloadRef: string | null;
  fields: ExternalField[];
  dataClass: string | null;
  snapshotSetId: string | null;
}

export interface DocumentEvidence {
  kind: "document";
  documentVersionId: string | null;
  parsedVersionId: string | null;
  start: number | null;
  end: number | null;
  quote: string | null;
}

export interface ExternalSnapshotEvidence {
  kind: "external_snapshot";
  snapshotId: string | null;
  recordId: string | null;
  fieldPointer: string | null;
  displayValue: string | null;
  observedAt: string | null;
}

export interface UnknownEvidence {
  kind: "unknown";
  rawKind: string | null;
}

/**
 * Evidence is a closed union: a document citation and an API snapshot citation
 * are rendered in separate sections and never converted into each other.
 */
export type EvidenceRef =
  | DocumentEvidence
  | ExternalSnapshotEvidence
  | UnknownEvidence;

export interface CheckResult {
  checkId: string;
  relation: Relation;
  reasonCode: string | null;
  machineStatus: MachineStatus;
  evidence: EvidenceRef[];
  /** Null when the server omitted it; never rendered as "no review needed". */
  requiresHumanReview: boolean | null;
  limitations: string[];
}

/** Exact quantity kept as a lexical string; no floating point conversion. */
export interface QuantityValue {
  value: string | null;
  unit: string | null;
}

/**
 * UI projection for one fulfillment check: the machine result plus the two
 * compared quantities. Human decisions stay in the existing decision flow.
 */
export interface FulfillmentComparison {
  check: CheckResult;
  contractValue: QuantityValue | null;
  configuredValue: QuantityValue | null;
}

export interface FulfillmentEvaluationInput {
  pack_id: string;
  pack_version: string;
  source_review_id: string;
  source_attempt_id: string;
  contract_version_id?: string;
  object_binding_version?: string;
  snapshot_set_id?: string;
  business_at: string;
  evidence_cutoff_at?: string;
  scope?: { kind: "whole_case" | "explicit_subset"; service_object_ids?: string[] };
}

export interface FulfillmentEvaluationRef {
  evaluationId: string;
  jobId: string | null;
}

export interface ContractFact {
  name: string;
  value: { value: string | null; unit: string | null } | null;
  uncertain: boolean;
  outOfScope: boolean;
  semanticKind: string | null;
}

export interface FulfillmentObject {
  objectRef: string;
  serviceObjectId: string | null;
  objectVersion: number | null;
  associationVersion: number | null;
  associationRunId: string | null;
  associationAttemptId: string | null;
  mappingId: string | null;
  mappingVersion: number | null;
  bindingVersion: string | null;
  snapshotSetId: string | null;
  snapshotCollectedAt: string | null;
  status: string | null;
  reasonCode: string | null;
  factsSha256: string | null;
  factRefs: unknown[];
  facts: ContractFact[];
  checks: CheckResult[];
  source: {
    runId: string | null;
    attemptId: string | null;
    associationRunId: string | null;
    associationAttemptId: string | null;
    materialId: string | null;
    materialVersion: number | null;
    documentId: string | null;
    parsedDocumentId: string | null;
    purpose: string | null;
  } | null;
}

export interface FulfillmentCoverage {
  scopeKind: string | null;
  candidateCount: number | null;
  evaluatedCount: number | null;
  unresolvedCount: number | null;
  insufficientCount: number | null;
  status: string | null;
  reasonCode: string | null;
}

export interface FulfillmentEvaluation {
  evaluationId: string;
  sourceReviewId?: string | null;
  sourceAttemptId?: string | null;
  pack: PackRef | null;
  contractVersion: string | null;
  rulePackVersion: string | null;
  objectBindingVersion: string | null;
  snapshotSetId: string | null;
  businessAt: string | null;
  evidenceCutoffAt?: string | null;
  scope?: { kind: string | null; serviceObjectIds: string[] } | null;
  coverage?: FulfillmentCoverage | null;
  objects?: FulfillmentObject[];
  checks: CheckResult[];
  contractFacts: ContractFact[];
  dataClass: string | null;
}

export interface ImpactCheck {
  checkId: string;
  action: ImpactAction;
  reasons: string[];
}

/** Explain or shadow plan. It never supersedes the full business result. */
export interface ImpactPlan {
  schemaVersion: string | null;
  baseEvaluationId: string | null;
  targetEvaluationId: string | null;
  mode: ImpactMode;
  dependencyComplete: boolean | null;
  fallback: ImpactFallback;
  fallbackReason: string | null;
  changedInputs: string[];
  checks: ImpactCheck[];
  businessResultSource: string | null;
  dataClass: string | null;
}
