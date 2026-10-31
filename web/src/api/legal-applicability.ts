import { jsonInit, requestJson } from "./http";
import { paths } from "./paths";
import { responseReader } from "./respond";
import type { MaterialAnchorBasisInput } from "./types";
import { isLegalApplicabilityControlledValue } from "../domain/legal-applicability";

export const legalApplicabilityDimensions = [
  "buyer_kind", "seller_kind", "buyer_enterprise_size", "seller_enterprise_size",
  "funding_kind", "procurement_scope", "project_kind", "contract_clause_kind", "transaction_kind",
] as const;
export type LegalApplicabilityDimension = typeof legalApplicabilityDimensions[number];
export type LegalApplicabilityStatus = "established" | "unknown";
export type LegalApplicabilityTemporalBasis = "current_state" | "historical_interval";
export type LegalApplicabilityUnknownReason =
  | "conflicting_evidence" | "uncertain_evidence" | "unsupported_basis" | "insufficient_evidence"
  | "expired_basis" | "stale_party_context" | "party_scope_ambiguous" | "historical_transition_basis_required";
export type LegalApplicabilityStoredUnknownReason = LegalApplicabilityUnknownReason | "";
export type LegalApplicabilityResolvedUnknownReason = LegalApplicabilityStoredUnknownReason | "missing_fact";

export interface LegalApplicabilityPartyPin {
  side: "buyer" | "seller";
  binding_id: string;
  binding_version: number;
  subject_id: string;
  subject_version: number;
  scope_note: string;
}

export interface LegalApplicabilityMaterialRef extends MaterialAnchorBasisInput {
  filename: string;
  quote: string;
}

export interface LegalApplicabilityFactVersion {
  fact_id: string;
  dimension: LegalApplicabilityDimension;
  version: number;
  status: LegalApplicabilityStatus;
  value: string;
  unknown_reason: LegalApplicabilityStoredUnknownReason;
  temporal_basis: LegalApplicabilityTemporalBasis;
  valid_from: string;
  valid_until: string | null;
  basis_obtained_at: string | null;
  basis_note: string;
  basis_refs: LegalApplicabilityMaterialRef[];
  basis_sha256: string;
  party: LegalApplicabilityPartyPin | null;
  created_at: string;
}

export interface LegalApplicabilityResolvedFact {
  dimension: LegalApplicabilityDimension;
  status: LegalApplicabilityStatus;
  value: string;
  unknown_reason: LegalApplicabilityResolvedUnknownReason;
  fact: LegalApplicabilityFactVersion | null;
}

export interface LegalApplicabilityFactSet {
  business_at: string;
  evidence_cutoff_at: string;
  facts: LegalApplicabilityResolvedFact[];
}

export interface LegalApplicabilityFactHistory {
  case_id: string;
  dimension: LegalApplicabilityDimension;
  current_version: number;
  items: LegalApplicabilityFactVersion[];
  next_cursor: string | null;
  can_manage: boolean;
}

export interface LegalApplicabilityFactInput {
  expected_version: number;
  dimension: LegalApplicabilityDimension;
  status: LegalApplicabilityStatus;
  value?: string;
  unknown_reason?: LegalApplicabilityUnknownReason;
  temporal_basis: LegalApplicabilityTemporalBasis;
  valid_from?: string;
  valid_until?: string;
  basis_note: string;
  basis_refs: MaterialAnchorBasisInput[];
  party?: LegalApplicabilityPartyPin;
}

const reader = responseReader((path) => `法律适用事实响应中的 ${path} 无法识别`);
const { object, text, integer, array } = reader;

function instant(value: unknown, path: string): string {
  const result = text(value, path);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(result) || Number.isNaN(Date.parse(result))) {
    throw new Error(`法律适用事实响应中的 ${path} 必须为UTC时间`);
  }
  return result;
}

function enumValue<const Values extends readonly string[]>(value: unknown, values: Values, path: string): Values[number] {
  if (typeof value !== "string" || !values.includes(value)) throw new Error(`法律适用事实响应中的 ${path} 枚举值无效`);
  return value;
}

function optionalEnumValue<const Values extends readonly string[]>(value: unknown, values: Values, path: string): Values[number] | "" {
  if (value === undefined || value === "") return "";
  return enumValue(value, values, path);
}

const statuses = ["established", "unknown"] as const;
const temporalBases = ["current_state", "historical_interval"] as const;
const unknownReasons = ["conflicting_evidence", "uncertain_evidence", "unsupported_basis", "insufficient_evidence", "expired_basis", "stale_party_context", "party_scope_ambiguous", "historical_transition_basis_required"] as const;

function parseParty(value: unknown, path: string): LegalApplicabilityPartyPin | null {
  if (value === undefined || value === null) return null;
  const v = object(value, path);
  if (Object.keys(v).length === 0) return null;
  const bindingID = text(v.binding_id, `${path}.binding_id`);
  const subjectID = text(v.subject_id, `${path}.subject_id`);
  if (!bindingID.trim() || !subjectID.trim()) throw new Error(`法律适用事实响应中的 ${path} 主体固定引用无效`);
  return {
    side: enumValue(v.side, ["buyer", "seller"] as const, `${path}.side`),
    binding_id: bindingID,
    binding_version: integer(v.binding_version, `${path}.binding_version`, 1),
    subject_id: subjectID,
    subject_version: integer(v.subject_version, `${path}.subject_version`, 1),
    scope_note: text(v.scope_note, `${path}.scope_note`),
  };
}

function parseMaterialRef(value: unknown, path: string): LegalApplicabilityMaterialRef {
  const v = object(value, path);
  const start = integer(v.start, `${path}.start`, 0);
  const end = integer(v.end, `${path}.end`, 0);
  const quote = text(v.quote, `${path}.quote`);
  const digest = text(v.quote_digest, `${path}.quote_digest`);
  if (v.kind !== "material_anchor" || end <= start || [...quote].length !== end - start || !/^[a-f0-9]{64}$/i.test(digest)) {
    throw new Error(`法律适用事实响应中的 ${path} 原文锚点无效`);
  }
  return {
    kind: "material_anchor",
    material_id: text(v.material_id, `${path}.material_id`),
    material_version: integer(v.material_version, `${path}.material_version`, 1),
    document_id: text(v.document_id, `${path}.document_id`),
    parsed_document_id: text(v.parsed_document_id, `${path}.parsed_document_id`),
    start, end, quote_digest: digest,
    filename: text(v.filename, `${path}.filename`), quote,
  };
}

export function parseLegalApplicabilityFactVersion(value: unknown, path = "fact"): LegalApplicabilityFactVersion {
  const v = object(value, path);
  const dimension = enumValue(v.dimension, legalApplicabilityDimensions, `${path}.dimension`);
  const status = enumValue(v.status, statuses, `${path}.status`);
  const temporalBasis = enumValue(v.temporal_basis, temporalBases, `${path}.temporal_basis`);
  const valueText = v.value === undefined ? "" : text(v.value, `${path}.value`);
  const unknownReason = optionalEnumValue(v.unknown_reason, unknownReasons, `${path}.unknown_reason`);
  const validUntil = v.valid_until === undefined || v.valid_until === null ? null : instant(v.valid_until, `${path}.valid_until`);
  const basisObtainedAt = v.basis_obtained_at === undefined || v.basis_obtained_at === null ? null : instant(v.basis_obtained_at, `${path}.basis_obtained_at`);
  const refs = array(v.basis_refs, `${path}.basis_refs`, parseMaterialRef);
  const basisSHA = text(v.basis_sha256, `${path}.basis_sha256`);
  if (!/^[a-f0-9]{64}$/i.test(basisSHA)) throw new Error(`法律适用事实响应中的 ${path}.basis_sha256 无效`);
  if (
    (status === "established" && (valueText === "" || unknownReason !== "")) ||
    (status === "unknown" && (valueText !== "" || unknownReason === "")) ||
    (status === "established" && !isLegalApplicabilityControlledValue(dimension, valueText))
  ) {
    throw new Error(`法律适用事实响应中的 ${path} 状态和值不匹配`);
  }
  const party = parseParty(v.party, `${path}.party`);
  const expectedPartySide = dimension.startsWith("buyer_") ? "buyer" : dimension.startsWith("seller_") ? "seller" : null;
  if (status === "established" && expectedPartySide !== null && (!party || party.side !== expectedPartySide)) {
    throw new Error(`法律适用事实响应中的 ${path} 缺少匹配维度的主体固定引用`);
  }
  return {
    fact_id: text(v.fact_id, `${path}.fact_id`), dimension, version: integer(v.version, `${path}.version`, 1),
    status, value: valueText, unknown_reason: unknownReason,
    temporal_basis: temporalBasis, valid_from: instant(v.valid_from, `${path}.valid_from`), valid_until: validUntil,
    basis_obtained_at: basisObtainedAt, basis_note: text(v.basis_note, `${path}.basis_note`),
    basis_refs: refs, basis_sha256: basisSHA, party,
    created_at: instant(v.created_at, `${path}.created_at`),
  };
}

export function parseLegalApplicabilityFactSet(value: unknown): LegalApplicabilityFactSet {
  const v = object(value, "fact set");
  const facts = array(v.facts, "fact set.facts", (item, path): LegalApplicabilityResolvedFact => {
    const row = object(item, path);
    const dimension = enumValue(row.dimension, legalApplicabilityDimensions, `${path}.dimension`);
    const status = enumValue(row.status, statuses, `${path}.status`);
    const valueText = row.value === undefined ? "" : text(row.value, `${path}.value`);
    const unknownReason = optionalEnumValue(row.unknown_reason, [...unknownReasons, "missing_fact"] as const, `${path}.unknown_reason`);
    const fact = row.fact === undefined || row.fact === null ? null : parseLegalApplicabilityFactVersion(row.fact, `${path}.fact`);
    if (
      (status === "established" && (valueText === "" || unknownReason !== "" || !isLegalApplicabilityControlledValue(dimension, valueText))) ||
      (status === "unknown" && (valueText !== "" || unknownReason === "")) ||
      (fact !== null && fact.dimension !== dimension)
    ) throw new Error(`法律适用事实响应中的 ${path} 状态字段不匹配`);
    return {
      dimension, status, value: valueText, unknown_reason: unknownReason,
      fact,
    };
  });
  if (new Set(facts.map((fact) => fact.dimension)).size !== facts.length) throw new Error("法律适用事实响应中包含重复维度");
  return {
    business_at: instant(v.business_at, "fact set.business_at"),
    evidence_cutoff_at: instant(v.evidence_cutoff_at, "fact set.evidence_cutoff_at"),
    facts,
  };
}

export function parseLegalApplicabilityFactHistory(value: unknown): LegalApplicabilityFactHistory {
  const v = object(value, "fact history");
  const dimension = enumValue(v.dimension, legalApplicabilityDimensions, "fact history.dimension");
  if (typeof v.can_manage !== "boolean") throw new Error("法律适用事实历史中的 can_manage 无效");
  const cursor = v.next_cursor === undefined || v.next_cursor === null ? null : text(v.next_cursor, "fact history.next_cursor");
  const items = array(v.items, "fact history.items", parseLegalApplicabilityFactVersion);
  const currentVersion = integer(v.current_version, "fact history.current_version", 0);
  if (items.some((item) => item.dimension !== dimension || item.version > currentVersion) || new Set(items.map((item) => item.version)).size !== items.length) {
    throw new Error("法律适用事实历史中的版本与维度不匹配");
  }
  return {
    case_id: text(v.case_id, "fact history.case_id"), dimension,
    current_version: currentVersion, items, next_cursor: cursor, can_manage: v.can_manage,
  };
}

export function getLegalApplicabilityFacts(caseId: string, businessAt: string, evidenceCutoffAt: string, signal?: AbortSignal) {
  return requestJson(paths.caseLegalApplicabilityFacts(caseId, businessAt, evidenceCutoffAt), { method: "GET", signal }, parseLegalApplicabilityFactSet);
}

export function getLegalApplicabilityFactHistory(caseId: string, dimension: LegalApplicabilityDimension, cursor?: string | null, signal?: AbortSignal) {
  return requestJson(paths.caseLegalApplicabilityFactHistory(caseId, dimension, cursor, 20), { method: "GET", signal }, parseLegalApplicabilityFactHistory);
}

export function recordLegalApplicabilityFact(caseId: string, input: LegalApplicabilityFactInput, idempotencyKey: string) {
  return requestJson(
    paths.caseLegalApplicabilityFact(caseId, input.dimension),
    jsonInit("PUT", { ...input, idempotency_key: idempotencyKey }, { "Idempotency-Key": idempotencyKey }),
    parseLegalApplicabilityFactVersion,
  );
}
