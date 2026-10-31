import { jsonInit, requestJson } from "../../api/http";
import { responseReader } from "../../api/respond";

const id = (value: string) => encodeURIComponent(value);
function page(path: string, cursor?: string | null, limit = 20): string {
  const query = new URLSearchParams({ limit: String(limit) });
  if (cursor) query.set("cursor", cursor);
  return `${path}?${query}`;
}
export const serviceObjectPaths = {
  list: (caseId: string, cursor?: string | null) => page(`/api/cases/${id(caseId)}/service-objects`, cursor),
  create: (caseId: string) => `/api/cases/${id(caseId)}/service-objects`,
  detail: (caseId: string, objectId: string) => `/api/cases/${id(caseId)}/service-objects/${id(objectId)}`,
  versions: (caseId: string, objectId: string, cursor?: string | null) => page(`/api/cases/${id(caseId)}/service-objects/${id(objectId)}/versions`, cursor),
  version: (caseId: string, objectId: string, version: number) => `/api/cases/${id(caseId)}/service-objects/${id(objectId)}/versions/${version}`,
  associations: (caseId: string, objectId: string, cursor?: string | null) => page(`/api/cases/${id(caseId)}/service-objects/${id(objectId)}/associations`, cursor),
  association: (caseId: string, objectId: string, version: number) => `/api/cases/${id(caseId)}/service-objects/${id(objectId)}/associations/${version}`,
  candidates: (caseId: string, objectId: string, source: AssociationSource) => {
    const query = new URLSearchParams({ ...source, material_version: String(source.material_version) });
    return `/api/cases/${id(caseId)}/service-objects/${id(objectId)}/association-candidates?${query}`;
  },
  confirmAssociation: (caseId: string, objectId: string) => `/api/cases/${id(caseId)}/service-objects/${id(objectId)}/associations`,
};

export type ServiceObjectType = "dedicated_line" | "service";
export type ServiceObjectState = "pending_confirmation" | "confirmed" | "inactive";
export interface ServiceObjectAlias { kind: string; value: string }
export interface ServiceObjectFact {
  id: string; rule_id: string; dimension: string; object_ref: string; value: string; unit: string; source_role: string;
  measurement_window?: string; time_zone?: string; calendar?: string; outage_definition?: string; exclusions?: string[]; basis?: string; applicable?: boolean;
  citations: Array<{ source_id: string; source_version: string; tenant_id: string; case_id: string; source_digest: string; start: number; end: number; quote: string }>;
}
export interface ServiceObjectFactRef { fact: ServiceObjectFact; document_id: string; parsed_document_id: string; canonical_fact_sha256: string }
export interface ServiceObjectVersion {
  service_object_id: string; version: number; object_type: ServiceObjectType; display_name: string; business_code: string;
  aliases: ServiceObjectAlias[]; state: ServiceObjectState; basis_refs: unknown[]; basis_summary: string; change_decision_id?: string;
  created_by: string; created_at: string; effective_from: string; payload_sha256: string;
}
export interface ServiceObjectAssociation {
  service_object_id: string; association_version: number; object_version: number; material_id: string; material_version: number;
  document_id: string; parsed_document_id: string; source_run_id: string; source_attempt_id: string; object_ref: string;
  facts: ServiceObjectFactRef[]; basis_refs: unknown[]; confirmed_by: string; confirmed_at: string; identity_change_decision_id?: string;
  facts_sha256: string; created_at: string; payload_sha256: string;
}
export interface ServiceObjectSummary {
  service_object_id: string; object_type: ServiceObjectType; state: ServiceObjectState; current_version: number;
  current_association_version: number; display_name: string; business_code: string; aliases: ServiceObjectAlias[];
  current_association: Omit<ServiceObjectAssociation, "service_object_id" | "facts" | "basis_refs" | "payload_sha256" | "created_at"> | null;
  can_manage: boolean; needs_reassociation: boolean;
}
export interface ServiceObjectList { items: ServiceObjectSummary[]; next_cursor: string | null; can_manage: boolean }
export interface ServiceObjectMappingPin { mapping_id: string; mapping_version: number; binding_version: string; object_version: number; association_version: number; external_object: { connector_id: string; external_object_id: string }; effective_from: string; effective_until: string }
export interface ServiceObjectDetail { service_object_id: string; current_version: number; identity: ServiceObjectVersion; association: ServiceObjectAssociation | null; mapping: ServiceObjectMappingPin | null; can_manage: boolean; needs_reassociation: boolean }
export interface ServiceObjectVersionPage { items: ServiceObjectVersion[]; next_cursor: string | null }
export interface ServiceObjectAssociationPage { items: ServiceObjectAssociation[]; next_cursor: string | null }
export interface AssociationSource { run_id: string; attempt_id: string; material_id: string; material_version: number; document_id: string; parsed_document_id: string }
export interface AssociationOption { object_ref: string; facts: ServiceObjectFactRef[]; facts_sha256: string; basis_refs: unknown[] }
export interface AssociationPreview extends AssociationSource {
  service_object_id: string; expected_object_version: number; expected_association_version: number; candidate_sha256: string; options: AssociationOption[];
}
export interface ServiceObjectMutationResult { service_object_id: string; version: number; association_version: number }
export interface ServiceObjectCreateInput { object_type: ServiceObjectType; display_name: string; business_code?: string; aliases: ServiceObjectAlias[]; basis_summary?: string }
export interface ServiceObjectAssociationConfirmation { confirmed: true; expected_object_version: number; expected_association_version: number; source: AssociationSource; object_ref: string; facts_sha256: string; candidate_sha256: string }
export interface MappingChangeCAS { expected_mapping_version: number; expected_binding_version: string; expected_object_version: number; expected_association_version: number }
export interface MappingChangeDraft { cas: MappingChangeCAS; patch: { kind: "replace_mapping"; external_object: ServiceObjectMappingPin["external_object"]; effective_from: string; effective_until: string; contract_association: ServiceObjectAssociationConfirmation }; basis_refs: Array<{ kind: "material_anchor"; material_id: string; material_version: number; document_id: string; parsed_document_id: string; start: number; end: number; quote_digest: string }> }
export interface MappingAssociationAfter { service_object_id: string; object_version: number; association_version: number; material_id: string; material_version: number; document_id: string; parsed_document_id: string; source_run_id: string; source_attempt_id: string; object_ref: string; facts: ServiceObjectFactRef[]; facts_sha256: string; basis_refs: unknown[] }
export interface MappingChangePreview { after_sha256: string; impact_sha256: string; association_after: MappingAssociationAfter }
export interface MappingChangeRequest { request_id: string; case_id: string; service_object_id: string; mapping_id: string; expected_mapping_version: number; expected_binding_version: string; expected_object_version: number; expected_association_version: number; after_sha256: string; impact_sha256: string; state: string; created_at: string; after: MappingAssociationAfter }
export interface MappingDecisionInput extends MappingChangeCAS { decision: "approve" | "reject"; expected_after_sha256: string; expected_impact_sha256: string; reason: string }

const { record, object, text, integer, bool, array, nullableText } = responseReader((path) => `服务对象响应中的 ${path} 无法识别`);
// The store serves optional text fields (basis_summary, business_code) as
// present-but-empty strings, so they are required keys that allow "".
const stringField = (value: unknown, path: string): string => {
  if (typeof value !== "string") throw new Error(`服务对象响应中的 ${path} 无法识别`);
  return value;
};
const sha = (value: unknown, path: string) => { const result = text(value, path); if (!/^[a-f0-9]{64}$/.test(result)) throw new Error(`服务对象响应中的 ${path} 不是 SHA-256`); return result; };
const instant = (value: unknown, path: string) => {
  const result = text(value, path);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-](?:0\d|1\d|2[0-3]):[0-5]\d)$/.test(result) || Number.isNaN(Date.parse(result))) {
    throw new Error(`服务对象响应中的 ${path} 不是 RFC 3339 时间`);
  }
  return result;
};
function enumField<T extends string>(value: unknown, values: readonly T[], path: string): T { const result = text(value, path); if (!(values as readonly string[]).includes(result)) throw new Error(`服务对象响应中的 ${path} 值无效`); return result as T; }
function parseAlias(value: unknown, path: string): ServiceObjectAlias { const row = record(value, path, ["kind", "value"]); return { kind: text(row.kind, `${path}.kind`), value: text(row.value, `${path}.value`) }; }
function parseFact(value: unknown, path: string): ServiceObjectFact {
  const keys = ["id", "rule_id", "dimension", "object_ref", "value", "unit", "source_role", "measurement_window", "time_zone", "calendar", "outage_definition", "exclusions", "basis", "applicable", "citations"];
  const row = record(value, path, keys, ["id", "rule_id", "dimension", "object_ref", "value", "unit", "source_role", "citations"]);
  const stringOpt = (key: string) => row[key] === undefined ? undefined : text(row[key], `${path}.${key}`);
  const citations = array(row.citations, `${path}.citations`, (item, itemPath) => {
    const c = record(item, itemPath, ["source_id", "source_version", "tenant_id", "case_id", "source_digest", "start", "end", "quote"]);
    const start = integer(c.start, `${itemPath}.start`, 0); const end = integer(c.end, `${itemPath}.end`, 0);
    if (end < start) throw new Error(`服务对象响应中的 ${itemPath} 引用跨度无效`);
    return { source_id: text(c.source_id, `${itemPath}.source_id`), source_version: text(c.source_version, `${itemPath}.source_version`), tenant_id: text(c.tenant_id, `${itemPath}.tenant_id`), case_id: text(c.case_id, `${itemPath}.case_id`), source_digest: sha(c.source_digest, `${itemPath}.source_digest`), start, end, quote: text(c.quote, `${itemPath}.quote`) };
  });
  return { id: text(row.id, `${path}.id`), rule_id: text(row.rule_id, `${path}.rule_id`), dimension: text(row.dimension, `${path}.dimension`), object_ref: text(row.object_ref, `${path}.object_ref`), value: text(row.value, `${path}.value`), unit: text(row.unit, `${path}.unit`), source_role: text(row.source_role, `${path}.source_role`), ...(stringOpt("measurement_window") === undefined ? {} : { measurement_window: stringOpt("measurement_window") }), ...(stringOpt("time_zone") === undefined ? {} : { time_zone: stringOpt("time_zone") }), ...(stringOpt("calendar") === undefined ? {} : { calendar: stringOpt("calendar") }), ...(stringOpt("outage_definition") === undefined ? {} : { outage_definition: stringOpt("outage_definition") }), ...(row.exclusions === undefined ? {} : { exclusions: array(row.exclusions, `${path}.exclusions`, (x, p) => text(x, p)) }), ...(stringOpt("basis") === undefined ? {} : { basis: stringOpt("basis") }), ...(row.applicable === undefined ? {} : { applicable: bool(row.applicable, `${path}.applicable`) }), citations };
}
function parseFactRef(value: unknown, path: string): ServiceObjectFactRef { const row = record(value, path, ["fact", "document_id", "parsed_document_id", "canonical_fact_sha256"]); return { fact: parseFact(row.fact, `${path}.fact`), document_id: text(row.document_id, `${path}.document_id`), parsed_document_id: text(row.parsed_document_id, `${path}.parsed_document_id`), canonical_fact_sha256: sha(row.canonical_fact_sha256, `${path}.canonical_fact_sha256`) }; }
function parseBasisRefs(value: unknown, path: string): unknown[] { return array(value, path, (item, itemPath) => object(item, itemPath)); }
export function parseServiceObjectVersion(value: unknown, path = "version"): ServiceObjectVersion {
  const row = record(value, path, ["service_object_id", "version", "object_type", "display_name", "business_code", "aliases", "state", "basis_refs", "basis_summary", "change_decision_id", "created_by", "created_at", "effective_from", "payload_sha256"], ["service_object_id", "version", "object_type", "display_name", "business_code", "aliases", "state", "basis_refs", "basis_summary", "created_by", "created_at", "effective_from", "payload_sha256"]);
  return { service_object_id: text(row.service_object_id, `${path}.service_object_id`), version: integer(row.version, `${path}.version`, 1), object_type: enumField(row.object_type, ["dedicated_line", "service"], `${path}.object_type`), display_name: text(row.display_name, `${path}.display_name`), business_code: stringField(row.business_code, `${path}.business_code`), aliases: array(row.aliases, `${path}.aliases`, parseAlias), state: enumField(row.state, ["pending_confirmation", "confirmed", "inactive"], `${path}.state`), basis_refs: parseBasisRefs(row.basis_refs, `${path}.basis_refs`), basis_summary: stringField(row.basis_summary, `${path}.basis_summary`), ...(row.change_decision_id === undefined ? {} : { change_decision_id: text(row.change_decision_id, `${path}.change_decision_id`) }), created_by: text(row.created_by, `${path}.created_by`), created_at: instant(row.created_at, `${path}.created_at`), effective_from: instant(row.effective_from, `${path}.effective_from`), payload_sha256: sha(row.payload_sha256, `${path}.payload_sha256`) };
}
export function parseServiceObjectAssociation(value: unknown, path = "association"): ServiceObjectAssociation {
  const row = record(value, path, ["service_object_id", "association_version", "object_version", "material_id", "material_version", "document_id", "parsed_document_id", "source_run_id", "source_attempt_id", "object_ref", "facts", "basis_refs", "confirmed_by", "confirmed_at", "identity_change_decision_id", "facts_sha256", "created_at", "payload_sha256"], ["service_object_id", "association_version", "object_version", "material_id", "material_version", "document_id", "parsed_document_id", "source_run_id", "source_attempt_id", "object_ref", "facts", "basis_refs", "confirmed_by", "confirmed_at", "facts_sha256", "created_at", "payload_sha256"]);
  return { service_object_id: text(row.service_object_id, `${path}.service_object_id`), association_version: integer(row.association_version, `${path}.association_version`, 1), object_version: integer(row.object_version, `${path}.object_version`, 1), material_id: text(row.material_id, `${path}.material_id`), material_version: integer(row.material_version, `${path}.material_version`, 1), document_id: text(row.document_id, `${path}.document_id`), parsed_document_id: text(row.parsed_document_id, `${path}.parsed_document_id`), source_run_id: text(row.source_run_id, `${path}.source_run_id`), source_attempt_id: text(row.source_attempt_id, `${path}.source_attempt_id`), object_ref: text(row.object_ref, `${path}.object_ref`), facts: array(row.facts, `${path}.facts`, parseFactRef), basis_refs: parseBasisRefs(row.basis_refs, `${path}.basis_refs`), confirmed_by: text(row.confirmed_by, `${path}.confirmed_by`), confirmed_at: instant(row.confirmed_at, `${path}.confirmed_at`), ...(row.identity_change_decision_id === undefined ? {} : { identity_change_decision_id: text(row.identity_change_decision_id, `${path}.identity_change_decision_id`) }), facts_sha256: sha(row.facts_sha256, `${path}.facts_sha256`), created_at: instant(row.created_at, `${path}.created_at`), payload_sha256: sha(row.payload_sha256, `${path}.payload_sha256`) };
}
function parseAssociationPin(value: unknown, path: string): ServiceObjectSummary["current_association"] {
  if (value === null) return null;
  const row = record(value, path, ["association_version", "object_version", "material_id", "material_version", "document_id", "parsed_document_id", "source_run_id", "source_attempt_id", "object_ref", "facts_sha256", "confirmed_by", "confirmed_at"]);
  return { association_version: integer(row.association_version, `${path}.association_version`, 1), object_version: integer(row.object_version, `${path}.object_version`, 1), material_id: text(row.material_id, `${path}.material_id`), material_version: integer(row.material_version, `${path}.material_version`, 1), document_id: text(row.document_id, `${path}.document_id`), parsed_document_id: text(row.parsed_document_id, `${path}.parsed_document_id`), source_run_id: text(row.source_run_id, `${path}.source_run_id`), source_attempt_id: text(row.source_attempt_id, `${path}.source_attempt_id`), object_ref: text(row.object_ref, `${path}.object_ref`), facts_sha256: sha(row.facts_sha256, `${path}.facts_sha256`), confirmed_by: text(row.confirmed_by, `${path}.confirmed_by`), confirmed_at: instant(row.confirmed_at, `${path}.confirmed_at`) };
}
function parseSummary(value: unknown, path: string): ServiceObjectSummary {
  const row = record(value, path, ["service_object_id", "object_type", "state", "current_version", "current_association_version", "display_name", "business_code", "aliases", "current_association", "can_manage", "needs_reassociation"]);
  return { service_object_id: text(row.service_object_id, `${path}.service_object_id`), object_type: enumField(row.object_type, ["dedicated_line", "service"], `${path}.object_type`), state: enumField(row.state, ["pending_confirmation", "confirmed", "inactive"], `${path}.state`), current_version: integer(row.current_version, `${path}.current_version`, 1), current_association_version: integer(row.current_association_version, `${path}.current_association_version`, 0), display_name: text(row.display_name, `${path}.display_name`), business_code: stringField(row.business_code, `${path}.business_code`), aliases: array(row.aliases, `${path}.aliases`, parseAlias), current_association: parseAssociationPin(row.current_association, `${path}.current_association`), can_manage: bool(row.can_manage, `${path}.can_manage`), needs_reassociation: bool(row.needs_reassociation, `${path}.needs_reassociation`) };
}
function pageCursor(value: unknown, path: string): string | null { return nullableText(value, path); }
export function parseServiceObjectList(value: unknown): ServiceObjectList {
  const row = record(value, "service_objects", ["items", "next_cursor", "can_manage"]);
  const items = array(row.items, "service_objects.items", parseSummary);
  const canManage = bool(row.can_manage, "service_objects.can_manage");
  if (items.some((item) => item.can_manage !== canManage)) {
    throw new Error("服务对象列表权限与项目权限不一致");
  }
  return { items, next_cursor: pageCursor(row.next_cursor, "service_objects.next_cursor"), can_manage: canManage };
}
function parseMappingPin(value: unknown): ServiceObjectMappingPin | null {
  if (value === null) return null;
  const row = record(value, "service_object.mapping", ["mapping_id", "mapping_version", "binding_version", "object_version", "association_version", "external_object", "effective_from", "effective_until"]);
  const external = record(row.external_object, "service_object.mapping.external_object", ["connector_id", "external_object_id"]);
  return { mapping_id: text(row.mapping_id, "mapping_id"), mapping_version: integer(row.mapping_version, "mapping_version", 1), binding_version: text(row.binding_version, "binding_version"), object_version: integer(row.object_version, "object_version", 1), association_version: integer(row.association_version, "association_version", 1), external_object: { connector_id: text(external.connector_id, "connector_id"), external_object_id: text(external.external_object_id, "external_object_id") }, effective_from: text(row.effective_from, "effective_from"), effective_until: text(row.effective_until, "effective_until") };
}
export function parseServiceObjectDetail(value: unknown): ServiceObjectDetail {
  const row = record(value, "service_object", ["service_object_id", "current_version", "identity", "association", "mapping", "can_manage", "needs_reassociation"]);
  return { service_object_id: text(row.service_object_id, "service_object.service_object_id"), current_version: integer(row.current_version, "service_object.current_version", 1), identity: parseServiceObjectVersion(row.identity, "service_object.identity"), association: row.association === null ? null : parseServiceObjectAssociation(row.association, "service_object.association"), mapping: parseMappingPin(row.mapping), can_manage: bool(row.can_manage, "service_object.can_manage"), needs_reassociation: bool(row.needs_reassociation, "service_object.needs_reassociation") };
}
export function parseServiceObjectVersionPage(value: unknown): ServiceObjectVersionPage { const row = record(value, "service_object_versions", ["items", "next_cursor"]); return { items: array(row.items, "service_object_versions.items", parseServiceObjectVersion), next_cursor: pageCursor(row.next_cursor, "service_object_versions.next_cursor") }; }
export function parseServiceObjectAssociationPage(value: unknown): ServiceObjectAssociationPage { const row = record(value, "service_object_associations", ["items", "next_cursor"]); return { items: array(row.items, "service_object_associations.items", parseServiceObjectAssociation), next_cursor: pageCursor(row.next_cursor, "service_object_associations.next_cursor") }; }
export function parseAssociationPreview(value: unknown): AssociationPreview {
  const row = record(value, "association_preview", ["service_object_id", "expected_object_version", "expected_association_version", "run_id", "attempt_id", "material_id", "material_version", "document_id", "parsed_document_id", "candidate_sha256", "options"]);
  return { service_object_id: text(row.service_object_id, "association_preview.service_object_id"), expected_object_version: integer(row.expected_object_version, "association_preview.expected_object_version", 1), expected_association_version: integer(row.expected_association_version, "association_preview.expected_association_version", 0), run_id: text(row.run_id, "association_preview.run_id"), attempt_id: text(row.attempt_id, "association_preview.attempt_id"), material_id: text(row.material_id, "association_preview.material_id"), material_version: integer(row.material_version, "association_preview.material_version", 1), document_id: text(row.document_id, "association_preview.document_id"), parsed_document_id: text(row.parsed_document_id, "association_preview.parsed_document_id"), candidate_sha256: sha(row.candidate_sha256, "association_preview.candidate_sha256"), options: array(row.options, "association_preview.options", (value, path) => { const item = record(value, path, ["object_ref", "facts", "facts_sha256", "basis_refs"]); return { object_ref: text(item.object_ref, `${path}.object_ref`), facts: array(item.facts, `${path}.facts`, parseFactRef), facts_sha256: sha(item.facts_sha256, `${path}.facts_sha256`), basis_refs: parseBasisRefs(item.basis_refs, `${path}.basis_refs`) }; }) };
}
export function parseServiceObjectMutationResult(value: unknown): ServiceObjectMutationResult { const row = record(value, "mutation_result", ["service_object_id", "version", "association_version"]); return { service_object_id: text(row.service_object_id, "mutation_result.service_object_id"), version: integer(row.version, "mutation_result.version", 1), association_version: integer(row.association_version, "mutation_result.association_version", 0) }; }

export function listServiceObjects(caseId: string, cursor?: string | null, signal?: AbortSignal) { return requestJson(serviceObjectPaths.list(caseId, cursor), { method: "GET", signal }, parseServiceObjectList); }
export function createServiceObject(caseId: string, input: ServiceObjectCreateInput, key: string) { return requestJson(serviceObjectPaths.create(caseId), jsonInit("POST", input, { "Idempotency-Key": key }), parseServiceObjectMutationResult); }
export function getServiceObject(caseId: string, objectId: string, signal?: AbortSignal) { return requestJson(serviceObjectPaths.detail(caseId, objectId), { method: "GET", signal }, parseServiceObjectDetail); }
export function listServiceObjectVersions(caseId: string, objectId: string, cursor?: string | null, signal?: AbortSignal) { return requestJson(serviceObjectPaths.versions(caseId, objectId, cursor), { method: "GET", signal }, parseServiceObjectVersionPage); }
export function getServiceObjectVersion(caseId: string, objectId: string, version: number, signal?: AbortSignal) { return requestJson(serviceObjectPaths.version(caseId, objectId, version), { method: "GET", signal }, (value) => parseServiceObjectVersion(value)); }
export function listServiceObjectAssociations(caseId: string, objectId: string, cursor?: string | null, signal?: AbortSignal) { return requestJson(serviceObjectPaths.associations(caseId, objectId, cursor), { method: "GET", signal }, parseServiceObjectAssociationPage); }
export function getServiceObjectAssociation(caseId: string, objectId: string, version: number, signal?: AbortSignal) { return requestJson(serviceObjectPaths.association(caseId, objectId, version), { method: "GET", signal }, (value) => parseServiceObjectAssociation(value)); }
export function getAssociationCandidates(caseId: string, objectId: string, source: AssociationSource, signal?: AbortSignal) { return requestJson(serviceObjectPaths.candidates(caseId, objectId, source), { method: "GET", signal }, parseAssociationPreview); }
export function confirmServiceObjectAssociation(caseId: string, objectId: string, input: ServiceObjectAssociationConfirmation, key: string) { return requestJson(serviceObjectPaths.confirmAssociation(caseId, objectId), jsonInit("POST", input, { "Idempotency-Key": key }), parseServiceObjectMutationResult); }

function parseMappingAssociation(value: unknown, path: string): MappingAssociationAfter {
  const row = object(value, path);
  return { service_object_id: text(row.service_object_id, `${path}.service_object_id`), object_version: integer(row.object_version, `${path}.object_version`, 1), association_version: integer(row.association_version, `${path}.association_version`, 1), material_id: text(row.material_id, `${path}.material_id`), material_version: integer(row.material_version, `${path}.material_version`, 1), document_id: text(row.document_id, `${path}.document_id`), parsed_document_id: text(row.parsed_document_id, `${path}.parsed_document_id`), source_run_id: text(row.source_run_id, `${path}.source_run_id`), source_attempt_id: text(row.source_attempt_id, `${path}.source_attempt_id`), object_ref: text(row.object_ref, `${path}.object_ref`), facts: array(row.facts, `${path}.facts`, parseFactRef), facts_sha256: sha(row.facts_sha256, `${path}.facts_sha256`), basis_refs: array(row.basis_refs, `${path}.basis_refs`, (item) => item) };
}
function parseMappingChange(value: unknown): MappingChangeRequest {
  const row = object(value, "mapping_change");
  const after = object(row.after, "mapping_change.after");
  return { request_id: text(row.request_id, "request_id"), case_id: text(row.case_id, "case_id"), service_object_id: text(row.service_object_id, "service_object_id"), mapping_id: text(row.mapping_id, "mapping_id"), expected_mapping_version: integer(row.expected_mapping_version, "expected_mapping_version", 1), expected_binding_version: text(row.expected_binding_version, "expected_binding_version"), expected_object_version: integer(row.expected_object_version, "expected_object_version", 1), expected_association_version: integer(row.expected_association_version, "expected_association_version", 1), after_sha256: sha(row.after_sha256, "after_sha256"), impact_sha256: sha(row.impact_sha256, "impact_sha256"), state: text(row.state, "state"), created_at: text(row.created_at, "created_at"), after: parseMappingAssociation(after.association, "mapping_change.after.association") };
}
export function previewMappingAssociation(caseId: string, mappingId: string, draft: MappingChangeDraft) {
  return requestJson(`/api/cases/${id(caseId)}/object-bindings/${id(mappingId)}/changes/preview`, jsonInit("POST", draft), (value): MappingChangePreview => { const row = object(value, "mapping_preview"); return { after_sha256: sha(row.after_sha256, "after_sha256"), impact_sha256: sha(row.impact_sha256, "impact_sha256"), association_after: parseMappingAssociation(row.association_after, "mapping_preview.association_after") }; });
}
export function proposeMappingAssociation(caseId: string, mappingId: string, draft: MappingChangeDraft, preview: MappingChangePreview, key: string) {
  return requestJson(`/api/cases/${id(caseId)}/object-bindings/${id(mappingId)}/changes`, jsonInit("POST", { ...draft, impact_sha256: preview.impact_sha256 }, { "Idempotency-Key": key }), parseMappingChange);
}
export function getMappingAssociationChange(caseId: string, requestId: string, signal?: AbortSignal) {
  return requestJson(`/api/cases/${id(caseId)}/object-binding-changes/${id(requestId)}`, { method: "GET", signal }, (value) => parseMappingChange(object(value, "mapping_change_response").request));
}
export function decideMappingAssociation(caseId: string, requestId: string, input: MappingDecisionInput, key: string) {
  return requestJson(`/api/cases/${id(caseId)}/object-binding-changes/${id(requestId)}/decision`, jsonInit("POST", input, { "Idempotency-Key": key }), (value) => { const row = object(value, "mapping_decision"); return { decision: text(row.decision, "mapping_decision.decision") }; });
}
