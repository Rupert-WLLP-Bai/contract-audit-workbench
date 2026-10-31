import { enumValue, invalidResponse } from "./respond";
import { isRecord } from "./guards";
import type {
  BindExistingSubjectResult,
  CaseRoleBindingProjection,
  CaseRoleCreateResult,
  CaseSubjectsProjection,
  LegacySubjectProfile,
  MaskedSubjectIdentifier,
  SubjectAlias,
  SubjectChangeBasisRef,
  SubjectChangeDecisionResult,
  SubjectChangeDetail,
  SubjectChangeImpact,
  SubjectChangeProposalResult,
  SubjectChangeSummary,
  SubjectDetail,
  SubjectCaseLinksProjection,
  SubjectCaseLinkProjection,
  SubjectSourceSnapshotList,
  SubjectSourceSnapshotRecord,
  SubjectSourceSnapshotDetail,
  MaterialParsedTextPage,
  MaterialParsedDocumentList,
  MaterialParsedDocumentSummary,
  SubjectSourceDirectory,
  SubjectSourceDirectoryItem,
  SubjectIdentityClaim,
  SubjectHistoryProjection,
  SubjectPrincipal,
  SubjectSummary,
  SubjectSourceSnapshot,
  SubjectSourceVerificationResult,
  SubjectVersionProjection,
} from "./types";

const roles = ["customer", "supplier", "contractor", "service_provider", "other"] as const;
const bindingStates = ["active", "inactive", "pending_confirmation"] as const;
const verificationStates = ["pending_verification", "verified"] as const;
const aliasKinds = ["legal", "trade", "former", "other"] as const;
const subjectSources = ["user_asserted", "official_identifier", "authorized_correction"] as const;
const claimSources = ["user_asserted", "official_identifier", "authorized_correction", "synthetic_controlled_snapshot"] as const;
const verificationScopes = ["synthetic_controlled", "official_controlled", "ordinary"] as const;
const changeTargets = ["subject", "case_subject_role"] as const;
const changeStates = ["pending_decision", "approved", "rejected", "applied"] as const;

function fail(path: string): never {
  return invalidResponse(`主体数据无效：${path}`, 500);
}

function object(value: unknown, path: string): Record<string, unknown> {
  if (!isRecord(value)) return fail(path);
  return value;
}

function text(record: Record<string, unknown>, key: string, path: string): string {
  const value = record[key];
  if (typeof value !== "string" || value.trim() === "") return fail(`${path}.${key}`);
  return value;
}

function textAllowEmpty(record: Record<string, unknown>, key: string, path: string): string {
  const value = record[key];
  if (typeof value !== "string") return fail(`${path}.${key}`);
  return value;
}

function literal<T extends string>(record: Record<string, unknown>, key: string, expected: T, path: string): T {
  if (record[key] !== expected) return fail(`${path}.${key}`);
  return expected;
}

function nullableText(record: Record<string, unknown>, key: string, path: string): string | null {
  const value = record[key];
  if (value !== null && typeof value !== "string") return fail(`${path}.${key}`);
  return value;
}

function positiveInteger(record: Record<string, unknown>, key: string, path: string): number {
  const value = record[key];
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1) return fail(`${path}.${key}`);
  return value;
}

function nonNegativeInteger(record: Record<string, unknown>, key: string, path: string): number {
  const value = record[key];
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) return fail(`${path}.${key}`);
  return value;
}

function isoTime(record: Record<string, unknown>, key: string, path: string): string {
  const value = text(record, key, path);
  if (!/^\d{4}-\d{2}-\d{2}T.*(?:Z|[+-]\d{2}:\d{2})$/.test(value) || Number.isNaN(Date.parse(value)))
    return fail(`${path}.${key}`);
  return value;
}

function nullableIsoTime(record: Record<string, unknown>, key: string, path: string): string | null {
  const value = record[key];
  if (value === null) return null;
  if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}T.*(?:Z|[+-]\d{2}:\d{2})$/.test(value) || Number.isNaN(Date.parse(value)))
    return fail(`${path}.${key}`);
  return value;
}

function enumField<T extends string>(record: Record<string, unknown>, key: string, allowed: readonly T[], path: string): T {
  const value = enumValue(record[key], allowed);
  if (value === null) return fail(`${path}.${key}`);
  return value;
}

function booleanField(record: Record<string, unknown>, key: string, path: string): boolean {
  const value = record[key];
  if (typeof value !== "boolean") return fail(`${path}.${key}`);
  return value;
}

function array<T>(record: Record<string, unknown>, key: string, path: string, parse: (value: unknown, path: string) => T): T[] {
  const value = record[key];
  if (!Array.isArray(value)) return fail(`${path}.${key}`);
  return value.map((item, index) => parse(item, `${path}.${key}[${index}]`));
}

function principal(value: unknown, path: string): SubjectPrincipal {
  const record = object(value, path);
  return {
    user_id: text(record, "user_id", path),
    // The fixed local demonstration directory carries no SSO identity; only
    // enterprise directory records must have issuer/subject, so both stay
    // required keys that may be empty strings.
    issuer: textAllowEmpty(record, "issuer", path),
    subject: textAllowEmpty(record, "subject", path),
  };
}

function alias(value: unknown, path: string): SubjectAlias {
  const record = object(value, path);
  return {
    value: text(record, "value", path),
    kind: enumField(record, "kind", aliasKinds, path),
    ...(record.source_note === undefined ? {} : { source_note: text(record, "source_note", path) }),
  };
}

function identifier(value: unknown, path: string, requireMasked: true): MaskedSubjectIdentifier;
function identifier(value: unknown, path: string, requireMasked: false, allowFullValue?: boolean): SubjectIdentityClaim;
function identifier(value: unknown, path: string, requireMasked: boolean, allowFullValue = false): MaskedSubjectIdentifier | SubjectIdentityClaim {
  const record = object(value, path);
  if (record.namespace !== "cn.gov.samr.uscc" || record.identifier_type !== "unified_social_credit_code")
    return fail(`${path}.namespace/identifier_type`);
  const masked = record.masked_value;
  const raw = record.value;
  if (masked !== undefined && (typeof masked !== "string" || !/^[0-9A-Z]{4}•{10}[0-9A-Z]{4}$/.test(masked)))
    return fail(`${path}.masked_value`);
  if (raw !== undefined && (typeof raw !== "string" || !/^[0-9A-Z]{18}$/.test(raw)))
    return fail(`${path}.value`);
  if (requireMasked && typeof masked !== "string") return fail(`${path}.masked_value`);
  if ((requireMasked || !allowFullValue) && raw !== undefined) return fail(`${path}.value`);
  if (!requireMasked && typeof masked !== "string" && typeof raw !== "string") return fail(`${path}.value/masked_value`);
  return {
    namespace: "cn.gov.samr.uscc",
    identifier_type: "unified_social_credit_code",
    ...(typeof raw === "string" ? { value: raw } : {}),
    ...(typeof masked === "string" ? { masked_value: masked } : {}),
    ...(record.verification_scope === undefined ? {} : { verification_scope: enumField(record, "verification_scope", verificationScopes, path) }),
    verification_state: enumField(record, "verification_state", verificationStates, path),
    source_kind: enumField(record, "source_kind", claimSources, path),
    source_time: nullableIsoTime(record, "source_time", path),
    payload_hash: hash(record, "payload_hash", path),
  };
}

function hash(record: Record<string, unknown>, key: string, path: string): string {
  const value = text(record, key, path);
  if (!/^[0-9a-f]{64}$/.test(value)) return fail(`${path}.${key}`);
  return value;
}

function principalAliases(record: Record<string, unknown>, path: string): SubjectAlias[] {
  return array(record, "aliases", path, alias);
}

export function parseSubjectSummary(value: unknown): SubjectSummary {
  const path = "subject";
  const record = object(value, path);
  const verification = record.verification_state === "inactive"
    ? "inactive"
    : enumField(record, "verification_state", verificationStates, path);
  return {
    subject_id: text(record, "subject_id", path),
    current_version: positiveInteger(record, "current_version", path),
    display_name: text(record, "display_name", path),
    legal_name: nullableText(record, "legal_name", path),
    verification_state: verification,
    ...(record.verification_scope === undefined ? {} : { verification_scope: enumField(record, "verification_scope", verificationScopes, path) }),
    aliases: principalAliases(record, path),
    created_at: isoTime(record, "created_at", path),
    created_by: principal(record.created_by, `${path}.created_by`),
    identifiers: array(record, "identifiers", path, (item, itemPath) => identifier(item, itemPath, true)),
  };
}

export function parseSubjectVersionProjection(value: unknown, allowFullIdentifiers = false): SubjectVersionProjection {
  const path = "subject version";
  const record = object(value, path);
  return {
    subject_id: text(record, "subject_id", path),
    version: positiveInteger(record, "version", path),
    display_name: text(record, "display_name", path),
    legal_name: nullableText(record, "legal_name", path),
    verification_state: enumField(record, "verification_state", verificationStates, path),
    ...(record.verification_scope === undefined ? {} : { verification_scope: enumField(record, "verification_scope", verificationScopes, path) }),
    source_kind: enumField(record, "source_kind", subjectSources, path),
    basis_summary: text(record, "basis_summary", path),
    payload_hash: hash(record, "payload_hash", path),
    identity_claims: array(record, "identity_claims", path, (item, itemPath) => identifier(item, itemPath, false, allowFullIdentifiers)),
    aliases: principalAliases(record, path),
    created_by: principal(record.created_by, `${path}.created_by`),
    created_at: isoTime(record, "created_at", path),
  };
}

export function parseSubjectList(value: unknown): { items: SubjectSummary[]; next_cursor: string | null } {
  const record = object(value, "subject list");
  return {
    items: array(record, "items", "subject list", (item) => parseSubjectSummary(item)),
    next_cursor: nullableText(record, "next_cursor", "subject list"),
  };
}

export function parseSubjectDetail(value: unknown): SubjectDetail {
  const path = "subject detail";
  const record = object(value, path);
  return {
    subject_id: text(record, "subject_id", path),
    current_version: positiveInteger(record, "current_version", path),
    current: parseSubjectVersionProjection(record.current),
    identifiers: array(record, "identifiers", path, (item, itemPath) => identifier(item, itemPath, true)),
  };
}

export function parseCaseRoleBinding(value: unknown, allowFullIdentifiers = false): CaseRoleBindingProjection {
  const path = "case role binding";
  const record = object(value, path);
  return {
    binding_id: text(record, "binding_id", path),
    binding_version: positiveInteger(record, "binding_version", path),
    case_id: text(record, "case_id", path),
    role: enumField(record, "role", roles, path),
    binding_state: enumField(record, "binding_state", bindingStates, path),
    source_kind: enumField(record, "source_kind", ["user_asserted", "legacy_label", "authorized_correction"] as const, path),
    legacy_label: nullableText(record, "legacy_label", path),
    created_by: principal(record.created_by, `${path}.created_by`),
    subject: parseSubjectVersionProjection(record.subject, allowFullIdentifiers),
    created_at: isoTime(record, "created_at", path),
  };
}

export function parseCaseSubjects(value: unknown): CaseSubjectsProjection {
  const record = object(value, "case subjects");
  const legacy = record.legacy_profile;
  let legacyProfile: LegacySubjectProfile | null = null;
  if (legacy !== null) {
    const profile = object(legacy, "legacy profile");
    legacyProfile = {
      version: positiveInteger(profile, "version", "legacy profile"),
      customer: nullableText(profile, "customer", "legacy profile"),
      supplier: nullableText(profile, "supplier", "legacy profile"),
    };
  }
  return {
    items: array(record, "items", "case subjects", (item) => parseCaseRoleBinding(item)),
    legacy_profile: legacyProfile,
    can_manage: record.can_manage === undefined ? false : booleanField(record, "can_manage", "case subjects"),
  };
}

export function parseSubjectCaseLinks(value: unknown): SubjectCaseLinksProjection {
  const record = object(value, "subject case links");
  const items = array(record, "items", "subject case links", (item, path): SubjectCaseLinkProjection => {
    const link = object(item, path);
    return {
      ...parseCaseRoleBinding(link),
      case_title: text(link, "case_title", path),
      business_status: text(link, "business_status", path),
    };
  });
  const visibleCount = nonNegativeInteger(record, "visible_count", "subject case links");
  if (visibleCount !== items.length) return fail("subject case links.visible_count");
  return { items, visible_count: visibleCount, next_cursor: nullableText(record, "next_cursor", "subject case links") };
}

export function parseSubjectHistory(value: unknown): SubjectHistoryProjection {
  const record = object(value, "subject history");
  const events = array(record, "events", "subject history", (value, path) => {
    const event = object(value, path);
    return {
      event_id: text(event, "event_id", path),
      kind: enumField(event, "kind", ["proposal_created", "decision_approved", "decision_rejected", "correction_applied"] as const, path),
      request_id: text(event, "request_id", path),
      case_id: text(event, "case_id", path),
      actor: principal(event.actor, `${path}.actor`),
      created_at: isoTime(event, "created_at", path),
    };
  });
  return {
    versions: array(record, "versions", "subject history", (item) => parseSubjectVersionProjection(item)),
    events,
  };
}

export function parseCaseRoleCreateResult(value: unknown): CaseRoleCreateResult {
  const path = "case role create";
  const record = object(value, path);
  return {
    binding_id: text(record, "binding_id", path),
    binding_version: positiveInteger(record, "binding_version", path),
    subject_id: text(record, "subject_id", path),
    subject_version: positiveInteger(record, "subject_version", path),
    role: enumField(record, "role", roles, path),
    display_name: text(record, "display_name", path),
    verification_state: enumField(record, "verification_state", verificationStates, path),
    source_kind: enumField(record, "source_kind", ["user_asserted", "legacy_label"] as const, path),
    legacy_label: nullableText(record, "legacy_label", path),
    binding_state: enumField(record, "binding_state", ["active", "pending_confirmation"] as const, path),
  };
}

export function parseBindExistingSubjectResult(value: unknown): BindExistingSubjectResult {
  const path = "bind existing subject";
  const record = object(value, path);
  if (record.binding_state !== "active" || record.source_kind !== "user_asserted") return fail(path);
  return {
    binding_id: text(record, "binding_id", path),
    binding_version: positiveInteger(record, "binding_version", path),
    subject_id: text(record, "subject_id", path),
    subject_version: positiveInteger(record, "subject_version", path),
    role: enumField(record, "role", roles, path),
    display_name: text(record, "display_name", path),
    verification_state: enumField(record, "verification_state", verificationStates, path),
    binding_state: "active",
    created_at: isoTime(record, "created_at", path),
    source_kind: "user_asserted",
  };
}

export function parseSubjectCreateResult(value: unknown): SubjectSummary {
  const path = "subject create";
  const record = object(value, path);
  const current = parseSubjectVersionProjection(record.subject_version);
  const currentVersion = positiveInteger(record, "current_version", path);
  if (current.subject_id !== text(record, "subject_id", path) || current.version !== currentVersion)
    return fail(path);
  return {
    subject_id: current.subject_id,
    current_version: currentVersion,
    display_name: current.display_name,
    legal_name: current.legal_name,
    verification_state: current.verification_state,
    aliases: current.aliases,
    created_at: isoTime(record, "created_at", path),
    created_by: current.created_by,
    identifiers: current.identity_claims.filter((claim): claim is MaskedSubjectIdentifier => typeof claim.masked_value === "string"),
  };
}

function summary(value: unknown): SubjectChangeSummary {
  const path = "subject change summary";
  const record = object(value, path);
  return {
    request_id: text(record, "request_id", path),
    target_kind: enumField(record, "target_kind", changeTargets, path),
    state: enumField(record, "state", changeStates, path),
    target_subject_id: text(record, "target_subject_id", path),
    target_binding_id: nullableText(record, "target_binding_id", path),
    expected_subject_version: positiveInteger(record, "expected_subject_version", path),
    expected_binding_version: record.expected_binding_version === null ? null : positiveInteger(record, "expected_binding_version", path),
    payload_hash: hash(record, "payload_hash", path),
    proposed_by: principal(record.proposed_by, `${path}.proposed_by`),
    proposed_by_display_name: text(record, "proposed_by_display_name", path),
    can_decide: booleanField(record, "can_decide", path),
    decision_blocked_reason: nullableText(record, "decision_blocked_reason", path),
    created_at: isoTime(record, "created_at", path),
  };
}

function basisRef(value: unknown, path: string): SubjectChangeBasisRef {
  const record = object(value, path);
  if (record.kind === "material_anchor") {
    const start = nonNegativeInteger(record, "start", path);
    const end = positiveInteger(record, "end", path);
    if (end <= start) return fail(`${path}.end`);
    return {
      kind: "material_anchor",
      material_id: text(record, "material_id", path),
      material_version: positiveInteger(record, "material_version", path),
      document_id: text(record, "document_id", path),
      parsed_document_id: text(record, "parsed_document_id", path),
      start,
      end,
      quote_digest: hash(record, "quote_digest", path),
      filename: text(record, "filename", path),
      quote: text(record, "quote", path),
    };
  }
  if (record.kind === "controlled_source") {
    if (record.source_catalog_id !== "synthetic.uscc.demo" || record.source_scope !== "synthetic_controlled")
      return fail(`${path}.source_catalog_id/source_scope`);
    return {
      kind: "controlled_source",
      snapshot_id: text(record, "snapshot_id", path),
      source_catalog_id: "synthetic.uscc.demo",
      source_version: positiveInteger(record, "source_version", path),
      source_digest: hash(record, "source_digest", path),
      source_scope: "synthetic_controlled",
      captured_at: isoTime(record, "captured_at", path),
      source_as_of: isoTime(record, "source_as_of", path),
      valid_from: isoTime(record, "valid_from", path),
      valid_until: nullableIsoTime(record, "valid_until", path),
    };
  }
  return fail(`${path}.kind`);
}

export function parseSubjectSourceSnapshot(value: unknown): SubjectSourceSnapshot {
  const path = "subject source snapshot";
  const record = object(value, path);
  if (record.source_catalog_id !== "synthetic.uscc.demo" || record.source_scope !== "synthetic_controlled")
    return fail(path);
  return {
    snapshot_id: text(record, "snapshot_id", path),
    source_catalog_id: "synthetic.uscc.demo",
    source_version: positiveInteger(record, "source_version", path),
    source_digest: hash(record, "source_digest", path),
    captured_at: isoTime(record, "captured_at", path),
    source_scope: "synthetic_controlled",
    record_count: nonNegativeInteger(record, "record_count", path),
    label: text(record, "label", path),
  };
}

function parseSubjectSourceSnapshotRecord(value: unknown): SubjectSourceSnapshotRecord {
  const path = "subject source snapshot record";
  const record = object(value, path);
  return {
    snapshot_id: text(record, "snapshot_id", path),
    source_catalog_id: literal(record, "source_catalog_id", "synthetic.uscc.demo", path),
    source_version: positiveInteger(record, "source_version", path),
    source_digest: hash(record, "source_digest", path),
    source_scope: literal(record, "source_scope", "synthetic_controlled", path),
    captured_at: isoTime(record, "captured_at", path),
    record_count: nonNegativeInteger(record, "record_count", path),
    label: text(record, "label", path),
  };
}

export function parseSubjectSourceSnapshotDetail(value: unknown): SubjectSourceSnapshotDetail {
  const path = "subject source snapshot detail";
  const record = object(value, path);
  return { ...parseSubjectSourceSnapshotRecord(record), case_id: text(record, "case_id", path) };
}

export function parseSubjectSourceSnapshotList(value: unknown): SubjectSourceSnapshotList {
  const record = object(value, "subject source snapshot list");
  return {
    items: array(record, "items", "subject source snapshot list", (item) => parseSubjectSourceSnapshotRecord(item)),
    next_cursor: nullableText(record, "next_cursor", "subject source snapshot list"),
  };
}

function parseSubjectSourceDirectoryItem(value: unknown): SubjectSourceDirectoryItem {
  const path = "subject source directory item";
  const record = object(value, path);
  return {
    source_id: literal(record, "source_id", "synthetic.uscc.demo", path),
    source_version: positiveInteger(record, "source_version", path),
    display_name: text(record, "display_name", path),
    verification_scope: literal(record, "verification_scope", "synthetic_controlled", path),
  };
}

export function parseSubjectSourceDirectory(value: unknown): SubjectSourceDirectory {
  const record = object(value, "subject source directory");
  return {
    items: array(record, "items", "subject source directory", (item) => parseSubjectSourceDirectoryItem(item)),
  };
}

export function parseMaterialParsedTextPage(value: unknown): MaterialParsedTextPage {
  const path = "material parsed text page";
  const record = object(value, path);
  const nextOffset = record.next_offset === null ? null : nonNegativeInteger(record, "next_offset", path);
  const offset = nonNegativeInteger(record, "offset", path);
  const totalRunes = nonNegativeInteger(record, "total_runes", path);
  if (nextOffset !== null && nextOffset <= offset) return fail(`${path}.next_offset`);
  const pageText = textAllowEmpty(record, "text", path);
  const pageEnd = offset + [...pageText].length;
  if (pageEnd > totalRunes || (nextOffset !== null && nextOffset !== pageEnd) || (nextOffset === null && pageEnd !== totalRunes))
    return fail(`${path}.text_range`);
  return {
    material_id: text(record, "material_id", path),
    material_version: positiveInteger(record, "material_version", path),
    document_id: text(record, "document_id", path),
    parsed_document_id: text(record, "parsed_document_id", path),
    filename: text(record, "filename", path),
    original_digest: hash(record, "original_digest", path),
    parse_digest: hash(record, "parse_digest", path),
    offset,
    total_runes: totalRunes,
    text: pageText,
    next_offset: nextOffset,
  };
}

function parseMaterialParsedDocumentSummary(value: unknown): MaterialParsedDocumentSummary {
  const path = "material parsed document summary";
  const record = object(value, path);
  return {
    parsed_document_id: text(record, "parsed_document_id", path),
    status: enumField(record, "status", ["ready", "failed"] as const, path),
    parser_id: text(record, "parser_id", path),
    parser_version: text(record, "parser_version", path),
    created_at: isoTime(record, "created_at", path),
    original_digest: hash(record, "original_digest", path),
    normalized_digest: record.normalized_digest === null ? null : hash(record, "normalized_digest", path),
  };
}

export function parseMaterialParsedDocumentList(value: unknown): MaterialParsedDocumentList {
  const path = "material parsed document list";
  const record = object(value, path);
  return {
    material_id: text(record, "material_id", path),
    material_version: positiveInteger(record, "material_version", path),
    document_id: text(record, "document_id", path),
    items: array(record, "items", path, parseMaterialParsedDocumentSummary),
  };
}

export function parseSubjectSourceVerificationResult(value: unknown): SubjectSourceVerificationResult {
  const path = "subject source verification";
  const record = object(value, path);
  if (record.source_catalog_id !== "synthetic.uscc.demo" || record.source_scope !== "synthetic_controlled")
    return fail(path);
  const match = enumField(record, "match_outcome", ["exact_match", "no_match", "ambiguous"] as const, path);
  const state = match === "exact_match" ? "pending_decision" : "not_proposed";
  if (record.state !== state) return fail(`${path}.state`);
  const requestId = nullableText(record, "request_id", path);
  if ((match === "exact_match") !== (requestId !== null)) return fail(`${path}.request_id`);
  let matchedRecord: SubjectSourceVerificationResult["matched_record"] = null;
  if (match === "exact_match") {
    const matched = object(record.matched_record, `${path}.matched_record`);
    matchedRecord = {
      display_name: text(matched, "display_name", `${path}.matched_record`),
      masked_identifier: text(matched, "masked_identifier", `${path}.matched_record`),
    };
    if (!/^[0-9A-Z]{4}•{10}[0-9A-Z]{4}$/.test(matchedRecord.masked_identifier)) return fail(`${path}.matched_record.masked_identifier`);
  } else if (record.matched_record !== null) {
    return fail(`${path}.matched_record`);
  }
  return {
    request_id: requestId,
    snapshot_id: text(record, "snapshot_id", path),
    source_catalog_id: "synthetic.uscc.demo",
    source_version: positiveInteger(record, "source_version", path),
    source_digest: hash(record, "source_digest", path),
    source_scope: "synthetic_controlled",
    source_as_of: nullableIsoTime(record, "source_as_of", path),
    valid_from: nullableIsoTime(record, "valid_from", path),
    valid_until: nullableIsoTime(record, "valid_until", path),
    match_outcome: match,
    state,
    matched_record: matchedRecord,
  };
}

function impactCases(value: unknown, path: string) {
  return array({ items: value }, "items", path, (item, itemPath) => {
    const record = object(item, itemPath);
    return {
      case_id: text(record, "case_id", itemPath),
      case_title: text(record, "case_title", itemPath),
      business_status: text(record, "business_status", itemPath),
    };
  });
}

function targetProjection(value: unknown, targetKind: SubjectChangeSummary["target_kind"]) {
  return targetKind === "subject"
    ? parseSubjectVersionProjection(value, true)
    : parseCaseRoleBinding(value, true);
}

export function parseSubjectChangeList(value: unknown): { items: SubjectChangeSummary[]; next_cursor: string | null } {
  const record = object(value, "subject changes");
  return {
    items: array(record, "items", "subject changes", summary),
    next_cursor: nullableText(record, "next_cursor", "subject changes"),
  };
}

export function parseSubjectChangeProposalResult(value: unknown): SubjectChangeProposalResult {
  const path = "subject change proposal";
  const record = object(value, path);
  return {
    request_id: text(record, "request_id", path),
    target_kind: enumField(record, "target_kind", changeTargets, path),
    expected_subject_version: positiveInteger(record, "expected_subject_version", path),
    expected_binding_version: record.expected_binding_version === null ? null : positiveInteger(record, "expected_binding_version", path),
    payload_hash: hash(record, "payload_hash", path),
    state: record.state === "pending_decision" ? "pending_decision" : fail(`${path}.state`),
    proposed_by_display_name: text(record, "proposed_by_display_name", path),
    can_decide: booleanField(record, "can_decide", path),
    decision_blocked_reason: nullableText(record, "decision_blocked_reason", path),
  };
}

export function parseSubjectChangeDetail(value: unknown): SubjectChangeDetail {
  const path = "subject change detail";
  const record = object(value, path);
  const subjectSummary = summary(record.summary);
  const proposedBy = text(record, "proposed_by_display_name", path);
  const canDecide = booleanField(record, "can_decide", path);
  const blockedReason = nullableText(record, "decision_blocked_reason", path);
  if (subjectSummary.proposed_by_display_name !== proposedBy || subjectSummary.can_decide !== canDecide || subjectSummary.decision_blocked_reason !== blockedReason)
    return fail(`${path}.decision_capability`);
  return {
    summary: subjectSummary,
    proposed_by_display_name: proposedBy,
    can_decide: canDecide,
    decision_blocked_reason: blockedReason,
    before: targetProjection(record.before, subjectSummary.target_kind),
    after: targetProjection(record.after, subjectSummary.target_kind),
    basis_note: textAllowEmpty(record, "basis_note", path),
    basis_refs: array(record, "basis_refs", path, basisRef),
  };
}

export function parseSubjectChangeImpact(
  value: unknown,
  targetKind: SubjectChangeSummary["target_kind"],
): SubjectChangeImpact {
  const path = "subject change impact";
  const record = object(value, path);
  return {
    request_id: text(record, "request_id", path),
    proposed_by_display_name: text(record, "proposed_by_display_name", path),
    can_decide: booleanField(record, "can_decide", path),
    decision_blocked_reason: nullableText(record, "decision_blocked_reason", path),
    target_version: positiveInteger(record, "target_version", path),
    payload_hash: hash(record, "payload_hash", path),
    before: targetProjection(record.before, targetKind),
    after: targetProjection(record.after, targetKind),
    basis_refs: array(record, "basis_refs", path, basisRef),
    impact_etag: text(record, "impact_etag", path),
    visible_affected_count: nonNegativeInteger(record, "visible_affected_count", path),
    authorized_impact_cases: impactCases(record.authorized_impact_cases, `${path}.authorized_impact_cases`),
  };
}

export function parseSubjectChangeDecisionResult(value: unknown): SubjectChangeDecisionResult {
  const path = "subject change decision";
  const record = object(value, path);
  const decision = enumField(record, "decision", ["approved", "rejected"] as const, path);
  const applied = record.applied_subject_version === null
    ? null
    : positiveInteger(record, "applied_subject_version", path);
  return {
    request_id: text(record, "request_id", path),
    decision,
    applied_subject_version: applied,
    binding_versions_appended: nonNegativeInteger(record, "binding_versions_appended", path),
  };
}
