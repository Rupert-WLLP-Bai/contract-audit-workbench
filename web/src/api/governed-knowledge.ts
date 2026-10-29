import { jsonInit, requestJson } from "./http";
import { enumValue, responseReader } from "./respond";

export type KnowledgeKind = "policy" | "guidance" | "exception" | "experience";
export type KnowledgeAction = "submit" | "validate" | "approve" | "publish" | "withdraw" | "rollback";

export interface KnowledgeRef {
  knowledge_id: string;
  version_id: string;
  content_sha256: string;
}
export interface KnowledgeScope {
  org_id?: string;
  case_id?: string;
  business_line: string;
  scenario: string;
  contract_type: string;
}
export interface KnowledgeCondition { field: string; equals: string }
export interface KnowledgeSpan { rule_id: string; start: number; end: number; quote: string }
export interface KnowledgeBinding {
  pack_id: string;
  pack_version: string;
  pack_content_hash: string;
  pack_snapshot_digest: string;
  compiler_ref: string;
  extraction_compiler_ref?: string;
  rule_ids: string[] | null;
  source_spans: KnowledgeSpan[] | null;
  candidate_diff: string;
  validation_evidence: string;
}
export interface KnowledgeContent {
  encoding?: string;
  knowledge_id: string;
  version: string;
  kind: KnowledgeKind;
  title: string;
  body: string;
  summary: string;
  source_type: string;
  source_reference: string;
  source_case_id?: string;
  source_org_id?: string;
  source_feedback_id?: string;
  candidate_id?: string;
  candidate_version_id?: string;
  candidate_content_sha256?: string;
  candidate_review_diff?: string;
  reviewed_applicability?: string[];
  reviewed_exclusions?: string[];
  scope: KnowledgeScope;
  valid_from: string;
  valid_until: string;
  applicability: KnowledgeCondition[] | null;
  exclusions: KnowledgeCondition[] | null;
  binding: KnowledgeBinding;
  exception_basis?: string;
  check_action?: string;
  minimum_evidence?: string;
  counterexamples?: string[];
  policy_refs?: KnowledgeRef[];
}
export interface KnowledgeVersion {
  ref: KnowledgeRef;
  content: KnowledgeContent;
  status: string;
  revision: number;
  approved_digest?: string;
  approval_actor_id?: string;
  approval_reason?: string;
  cross_case_reviewed: boolean;
  active: boolean;
  pointer_revision: number;
  created_by: string;
  created_at: string;
}
export interface KnowledgeSummary {
  ref: KnowledgeRef;
  version: string;
  title: string;
  kind: KnowledgeKind;
  source_type: string;
  scope: KnowledgeScope;
  valid_from: string;
  valid_until: string;
  status: string;
  revision: number;
  pointer_revision: number;
  active: boolean;
}
export interface KnowledgeHistoryEntry {
  sequence: number;
  ref: KnowledgeRef;
  action: string;
  revision: number;
  pointer_revision: number;
  actor_id: string;
  reason: string;
  previous_version_id?: string;
  previous_active: boolean;
  resulting_version_id?: string;
  resulting_active: boolean;
  occurred_at: string;
}
export interface PolicyTextIssue { start: number; end: number; quote: string; reason: string }
export interface PolicyComparison {
  rule_id: string;
  comparator: string;
  candidate?: { comparator: string; value: string; unit: string; basis?: string; start: number; end: number; quote: string };
  rule_threshold: { value: string; unit: string; basis?: string };
  status: string;
  reason?: string;
}
export interface PolicyPreview {
  ref: KnowledgeRef;
  extractor_ref: string;
  comparisons: PolicyComparison[];
  issues: PolicyTextIssue[];
  semantic_only: PolicyTextIssue[];
  approvable: boolean;
}
export interface KnowledgeCommand {
  action: KnowledgeAction;
  expected_revision: number;
  expected_content_sha256: string;
  expected_pointer_revision: number;
  reason: string;
  cross_case_reviewed: boolean;
}

const {
  invalid,
  object: obj,
  text,
  bool,
  array,
  nullableArray,
  optionalArray,
  maybeText: optionalText,
  integer: safeInteger,
} = responseReader((path) => `知识治理响应中的 ${path} 无法识别`);
const integer = (value: unknown, path: string): number =>
  safeInteger(value, path, 0);
function kind(value: unknown, path: string): KnowledgeKind {
  return (
    enumValue<KnowledgeKind>(value, [
      "policy",
      "guidance",
      "exception",
      "experience",
    ]) ?? invalid(path)
  );
}
function ref(value: unknown, path: string): KnowledgeRef {
  const v = obj(value, path);
  return { knowledge_id: text(v.knowledge_id, `${path}.knowledge_id`), version_id: text(v.version_id, `${path}.version_id`), content_sha256: text(v.content_sha256, `${path}.content_sha256`) };
}
function scope(value: unknown, path: string): KnowledgeScope {
  const v = obj(value, path);
  return { org_id: optionalText(v.org_id, `${path}.org_id`), case_id: optionalText(v.case_id, `${path}.case_id`), business_line: text(v.business_line, `${path}.business_line`), scenario: text(v.scenario, `${path}.scenario`), contract_type: text(v.contract_type, `${path}.contract_type`) };
}
function condition(value: unknown, path: string): KnowledgeCondition {
  const v = obj(value, path);
  return { field: text(v.field, `${path}.field`), equals: text(v.equals, `${path}.equals`) };
}
function span(value: unknown, path: string): KnowledgeSpan {
  const v = obj(value, path);
  return { rule_id: text(v.rule_id, `${path}.rule_id`), start: integer(v.start, `${path}.start`), end: integer(v.end, `${path}.end`), quote: text(v.quote, `${path}.quote`) };
}
function binding(value: unknown, path: string): KnowledgeBinding {
  const v = obj(value, path);
  return { pack_id: text(v.pack_id, `${path}.pack_id`), pack_version: text(v.pack_version, `${path}.pack_version`), pack_content_hash: text(v.pack_content_hash, `${path}.pack_content_hash`), pack_snapshot_digest: text(v.pack_snapshot_digest, `${path}.pack_snapshot_digest`), compiler_ref: text(v.compiler_ref, `${path}.compiler_ref`), extraction_compiler_ref: optionalText(v.extraction_compiler_ref, `${path}.extraction_compiler_ref`), rule_ids: nullableArray(v.rule_ids, `${path}.rule_ids`, text), source_spans: nullableArray(v.source_spans, `${path}.source_spans`, span), candidate_diff: text(v.candidate_diff, `${path}.candidate_diff`), validation_evidence: text(v.validation_evidence, `${path}.validation_evidence`) };
}
function content(value: unknown, path: string): KnowledgeContent {
  const v = obj(value, path);
  return {
    encoding: optionalText(v.encoding, `${path}.encoding`), knowledge_id: text(v.knowledge_id, `${path}.knowledge_id`), version: text(v.version, `${path}.version`), kind: kind(v.kind, `${path}.kind`), title: text(v.title, `${path}.title`), body: text(v.body, `${path}.body`), summary: text(v.summary, `${path}.summary`), source_type: text(v.source_type, `${path}.source_type`), source_reference: text(v.source_reference, `${path}.source_reference`),
    source_case_id: optionalText(v.source_case_id, `${path}.source_case_id`), source_org_id: optionalText(v.source_org_id, `${path}.source_org_id`), source_feedback_id: optionalText(v.source_feedback_id, `${path}.source_feedback_id`), candidate_id: optionalText(v.candidate_id, `${path}.candidate_id`), candidate_version_id: optionalText(v.candidate_version_id, `${path}.candidate_version_id`), candidate_content_sha256: optionalText(v.candidate_content_sha256, `${path}.candidate_content_sha256`), candidate_review_diff: optionalText(v.candidate_review_diff, `${path}.candidate_review_diff`), reviewed_applicability: optionalArray(v.reviewed_applicability, `${path}.reviewed_applicability`, text), reviewed_exclusions: optionalArray(v.reviewed_exclusions, `${path}.reviewed_exclusions`, text),
    scope: scope(v.scope, `${path}.scope`), valid_from: text(v.valid_from, `${path}.valid_from`), valid_until: text(v.valid_until, `${path}.valid_until`), applicability: nullableArray(v.applicability, `${path}.applicability`, condition), exclusions: nullableArray(v.exclusions, `${path}.exclusions`, condition), binding: binding(v.binding, `${path}.binding`), exception_basis: optionalText(v.exception_basis, `${path}.exception_basis`), check_action: optionalText(v.check_action, `${path}.check_action`), minimum_evidence: optionalText(v.minimum_evidence, `${path}.minimum_evidence`), counterexamples: optionalArray(v.counterexamples, `${path}.counterexamples`, text), policy_refs: optionalArray(v.policy_refs, `${path}.policy_refs`, ref),
  };
}
export function parseKnowledgeVersion(value: unknown): KnowledgeVersion {
  const v = obj(value, "version");
  return { ref: ref(v.ref, "version.ref"), content: content(v.content, "version.content"), status: text(v.status, "version.status"), revision: integer(v.revision, "version.revision"), approved_digest: optionalText(v.approved_digest, "version.approved_digest"), approval_actor_id: optionalText(v.approval_actor_id, "version.approval_actor_id"), approval_reason: optionalText(v.approval_reason, "version.approval_reason"), cross_case_reviewed: bool(v.cross_case_reviewed, "version.cross_case_reviewed"), active: bool(v.active, "version.active"), pointer_revision: integer(v.pointer_revision, "version.pointer_revision"), created_by: text(v.created_by, "version.created_by"), created_at: text(v.created_at, "version.created_at") };
}
function summary(value: unknown, path: string): KnowledgeSummary {
  const v = obj(value, path);
  return { ref: ref(v.ref, `${path}.ref`), version: text(v.version, `${path}.version`), title: text(v.title, `${path}.title`), kind: kind(v.kind, `${path}.kind`), source_type: text(v.source_type, `${path}.source_type`), scope: scope(v.scope, `${path}.scope`), valid_from: text(v.valid_from, `${path}.valid_from`), valid_until: text(v.valid_until, `${path}.valid_until`), status: text(v.status, `${path}.status`), revision: integer(v.revision, `${path}.revision`), pointer_revision: integer(v.pointer_revision, `${path}.pointer_revision`), active: bool(v.active, `${path}.active`) };
}
function history(value: unknown, path: string): KnowledgeHistoryEntry {
  const v = obj(value, path);
  return { sequence: integer(v.sequence, `${path}.sequence`), ref: ref(v.ref, `${path}.ref`), action: text(v.action, `${path}.action`), revision: integer(v.revision, `${path}.revision`), pointer_revision: integer(v.pointer_revision, `${path}.pointer_revision`), actor_id: text(v.actor_id, `${path}.actor_id`), reason: text(v.reason, `${path}.reason`), previous_version_id: optionalText(v.previous_version_id, `${path}.previous_version_id`), previous_active: bool(v.previous_active, `${path}.previous_active`), resulting_version_id: optionalText(v.resulting_version_id, `${path}.resulting_version_id`), resulting_active: bool(v.resulting_active, `${path}.resulting_active`), occurred_at: text(v.occurred_at, `${path}.occurred_at`) };
}
function issue(value: unknown, path: string): PolicyTextIssue {
  const v = obj(value, path);
  return { start: integer(v.start, `${path}.start`), end: integer(v.end, `${path}.end`), quote: text(v.quote, `${path}.quote`), reason: text(v.reason, `${path}.reason`) };
}
function threshold(value: unknown, path: string) {
  const v = obj(value, path);
  return { value: text(v.value, `${path}.value`), unit: text(v.unit, `${path}.unit`), basis: optionalText(v.basis, `${path}.basis`) };
}
function comparison(value: unknown, path: string): PolicyComparison {
  const v = obj(value, path);
  const c = v.candidate === undefined ? undefined : obj(v.candidate, `${path}.candidate`);
  return { rule_id: text(v.rule_id, `${path}.rule_id`), comparator: text(v.comparator, `${path}.comparator`), candidate: c === undefined ? undefined : { comparator: text(c.comparator, `${path}.candidate.comparator`), ...threshold(c, `${path}.candidate`), start: integer(c.start, `${path}.candidate.start`), end: integer(c.end, `${path}.candidate.end`), quote: text(c.quote, `${path}.candidate.quote`) }, rule_threshold: threshold(v.rule_threshold, `${path}.rule_threshold`), status: text(v.status, `${path}.status`), reason: optionalText(v.reason, `${path}.reason`) };
}
export function parsePolicyPreview(value: unknown): PolicyPreview {
  const v = obj(value, "preview");
  return { ref: ref(v.ref, "preview.ref"), extractor_ref: text(v.extractor_ref, "preview.extractor_ref"), comparisons: array(v.comparisons, "preview.comparisons", comparison), issues: array(v.issues, "preview.issues", issue), semantic_only: array(v.semantic_only, "preview.semantic_only", issue), approvable: bool(v.approvable, "preview.approvable") };
}

const base = "/api/admin/governed-knowledge";
function versionPath(knowledgeId: string, versionId: string): string {
  return `${base}/${encodeURIComponent(knowledgeId)}/versions/${encodeURIComponent(versionId)}`;
}
export function listGovernedKnowledge(signal?: AbortSignal): Promise<KnowledgeSummary[]> {
  return requestJson(base, { method: "GET", signal }, (value) => array(obj(value, "list").items, "list.items", summary));
}
export function getGovernedKnowledge(knowledgeId: string, versionId: string, signal?: AbortSignal): Promise<KnowledgeVersion> {
  return requestJson(versionPath(knowledgeId, versionId), { method: "GET", signal }, (value) => {
    const version = parseKnowledgeVersion(value);
    if (version.ref.knowledge_id !== knowledgeId || version.ref.version_id !== versionId || version.content.knowledge_id !== knowledgeId) return invalid("version.ref");
    return version;
  });
}
export function getKnowledgeHistory(knowledgeId: string, signal?: AbortSignal): Promise<KnowledgeHistoryEntry[]> {
  return requestJson(`${base}/${encodeURIComponent(knowledgeId)}/history`, { method: "GET", signal }, (value) => {
    const entries = array(obj(value, "history").items, "history.items", history);
    if (entries.some((entry) => entry.ref.knowledge_id !== knowledgeId)) return invalid("history.ref");
    return entries;
  });
}
export function previewGovernedPolicy(knowledgeId: string, versionId: string, signal?: AbortSignal): Promise<PolicyPreview> {
  return requestJson(`${versionPath(knowledgeId, versionId)}/preview`, { method: "GET", signal }, (value) => {
    const preview = parsePolicyPreview(value);
    if (preview.ref.knowledge_id !== knowledgeId || preview.ref.version_id !== versionId) return invalid("preview.ref");
    return preview;
  });
}
export function createGovernedKnowledge(content: KnowledgeContent, idempotencyKey: string): Promise<KnowledgeVersion> {
  return requestJson(base, jsonInit("POST", { content }, { "Idempotency-Key": idempotencyKey }), (value) => {
    const created = parseKnowledgeVersion(value);
    if (created.ref.knowledge_id !== content.knowledge_id || created.content.knowledge_id !== content.knowledge_id || created.content.version !== content.version || created.content.kind !== content.kind) return invalid("created.ref");
    return created;
  });
}
export function commandGovernedKnowledge(knowledgeId: string, versionId: string, command: KnowledgeCommand, idempotencyKey: string): Promise<KnowledgeVersion> {
  return requestJson(`${versionPath(knowledgeId, versionId)}/commands`, jsonInit("POST", command, { "Idempotency-Key": idempotencyKey }), (value) => {
    const version = parseKnowledgeVersion(value);
    if (version.ref.knowledge_id !== knowledgeId || version.ref.version_id !== versionId || version.ref.content_sha256 !== command.expected_content_sha256) return invalid("command.ref");
    return version;
  });
}
