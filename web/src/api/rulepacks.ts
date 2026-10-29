import { jsonInit, requestJson } from "./http";
import { paths } from "./paths";
import { enumValue, responseReader } from "./respond";

export type RulePackState = "draft" | "validated" | "approved" | "published";
export type RulePackHistoryAction =
  | "create"
  | "edit"
  | "validate"
  | "approve"
  | "publish"
  | "disable"
  | "rollback"
  | "test";
export type RulePackRelation =
  | "compatible"
  | "conflict"
  | "insufficient"
  | "not_applicable";
export type RulePackComparator =
  | "minimum_bandwidth"
  | "monthly_sla"
  | "advance_payment_ceiling";

export interface RulePackAuthoritySource {
  id: string;
  version: string;
  kind: string;
}

export interface RulePackThreshold {
  value: string;
  unit: string;
  basis?: string;
}

export interface RulePackRequiredFact {
  dimension: string;
  source_roles: string[];
  object_required: boolean;
  unit_family: string;
  measurement_window?: string;
  time_zone?: string;
  calendar?: string;
  outage_definition?: string;
  exclusions: string[] | null;
  basis?: string;
}

export interface RulePackRule {
  rule_id: string;
  version: string;
  domain: string;
  capability_id: string;
  mandatory: boolean;
  business_scope: {
    business_line: string;
    scenario: string;
    contract_type: string;
  };
  tenant_id: string;
  comparator: RulePackComparator;
  threshold: RulePackThreshold;
  required_fact: RulePackRequiredFact;
  authority_sources: RulePackAuthoritySource[];
}

export interface RulePackLifecycle {
  state: RulePackState;
  verification?: {
    manifest_hash: string;
    verifier_version: string;
    passed: boolean;
  };
  approval?: {
    manifest_hash: string;
    verification_hash: string;
    actor_id: string;
  };
  release?: {
    manifest_hash: string;
    approval_hash: string;
    release_hash: string;
  };
}

export interface RulePackManifest {
  schema_version: "rulepack/v1";
  id: string;
  version: string;
  tenant_id: string;
  effective_from: string;
  effective_until: string;
  rules: RulePackRule[];
  lifecycle: RulePackLifecycle;
}

export type RulePackManifestInput = Omit<RulePackManifest, "lifecycle"> & {
  lifecycle?: { state: "draft" };
};

export interface RulePackSummary {
  id: string;
  version: string;
  tenant_id: string;
  status: RulePackState;
  revision: number;
  content_hash: string;
  snapshot_sha256: string;
  effective_from: string;
  effective_until: string;
  authority_sources: string[];
  is_active: boolean;
  active_pointer_revision: number;
}

export interface RulePackDetail {
  pack: RulePackSummary;
  manifest: RulePackManifest;
}

export interface RulePackHistoryEntry {
  sequence: number;
  pack_id: string;
  version: string;
  action: RulePackHistoryAction;
  actor_id: string;
  content_hash: string;
  pointer_revision: number;
  created_at: string;
  previous_version: string | null;
  previous_active: boolean | null;
  resulting_version: string | null;
  resulting_active: boolean | null;
}

export interface RulePackTestSource {
  id: string;
  version: string;
  role: string;
  text: string;
}

export interface RulePackTestCitation {
  source_id: string;
  source_version: string;
  start: number;
  end: number;
  quote: string;
}

export interface RulePackReportCitation extends RulePackTestCitation {
  tenant_id: string;
  case_id: string;
  source_digest: string;
}

export interface RulePackTestFact {
  id: string;
  rule_id: string;
  dimension: string;
  object_ref: string;
  value: string;
  unit: string;
  source_role: string;
  measurement_window?: string;
  time_zone?: string;
  calendar?: string;
  outage_definition?: string;
  exclusions?: string[];
  basis?: string;
  applicable?: boolean;
  citations: RulePackTestCitation[];
}

export interface RulePackTestInput {
  expected_revision: number;
  business_line: string;
  scenario: string;
  contract_type: string;
  at: string;
  sources: RulePackTestSource[];
  facts: RulePackTestFact[];
}

export interface RulePackTestResult {
  rule_id: string;
  rule_version: string;
  domain: string;
  capability_id: string;
  mandatory: boolean;
  relation: RulePackRelation;
  reason_code: string;
  fact_ids?: string[];
  citations?: RulePackReportCitation[];
}

export interface RulePackTestReport {
  pack_id: string;
  pack_version: string;
  pack_hash: string;
  relations: RulePackRelation[];
  results: RulePackTestResult[];
}

export interface RulePackTestResponse {
  revision: number;
  content_hash: string;
  report: RulePackTestReport;
}

export type RulePackMutationResult = RulePackDetail | RulePackSummary;

const {
  invalid: fail,
  record: object,
  text: string,
  bool: boolean,
  array,
  nullableText: nullableString,
  nullableBool: nullableBoolean,
  integer: safeInteger,
} = responseReader((path) => `RulePack 响应中的 ${path} 无法识别`);
const integer = (value: unknown, path: string): number =>
  safeInteger(value, path, 0);
function nonEmptyString(value: unknown, path: string): string {
  const parsed = string(value, path);
  return parsed.length > 0 ? parsed : fail(path);
}

function sha256(value: unknown, path: string): string {
  const parsed = string(value, path);
  return /^[0-9a-f]{64}$/.test(parsed) ? parsed : fail(path);
}


function positiveInteger(value: unknown, path: string): number {
  const parsed = integer(value, path);
  return parsed > 0 ? parsed : fail(path);
}

function optionalString(
  record: Record<string, unknown>,
  key: string,
  path: string,
): string | undefined {
  return Object.hasOwn(record, key)
    ? string(record[key], `${path}.${key}`)
    : undefined;
}

function parseState(value: unknown, path: string): RulePackState {
  return (
    enumValue<RulePackState>(value, [
      "draft",
      "validated",
      "approved",
      "published",
    ]) ?? fail(path)
  );
}

function parseRelation(value: unknown, path: string): RulePackRelation {
  return (
    enumValue<RulePackRelation>(value, [
      "compatible",
      "conflict",
      "insufficient",
      "not_applicable",
    ]) ?? fail(path)
  );
}
function parseHistoryAction(
  value: unknown,
  path: string,
): RulePackHistoryAction {
  return (
    enumValue<RulePackHistoryAction>(value, [
      "create",
      "edit",
      "validate",
      "approve",
      "publish",
      "disable",
      "rollback",
      "test",
    ]) ?? fail(path)
  );
}

function parseAuthoritySource(
  value: unknown,
  path: string,
): RulePackAuthoritySource {
  const record = object(value, path, ["id", "version", "kind"]);
  return {
    id: string(record.id, `${path}.id`),
    version: string(record.version, `${path}.version`),
    kind: string(record.kind, `${path}.kind`),
  };
}

function parseSummary(value: unknown, path = "pack"): RulePackSummary {
  const keys = [
    "id",
    "version",
    "tenant_id",
    "status",
    "revision",
    "content_hash",
    "snapshot_sha256",
    "effective_from",
    "effective_until",
    "authority_sources",
    "is_active",
    "active_pointer_revision",
  ] as const;
  const record = object(value, path, keys);
  return {
    id: nonEmptyString(record.id, `${path}.id`),
    version: nonEmptyString(record.version, `${path}.version`),
    tenant_id: nonEmptyString(record.tenant_id, `${path}.tenant_id`),
    status: parseState(record.status, `${path}.status`),
    revision: positiveInteger(record.revision, `${path}.revision`),
    content_hash: sha256(record.content_hash, `${path}.content_hash`),
    snapshot_sha256: sha256(record.snapshot_sha256, `${path}.snapshot_sha256`),
    effective_from: string(record.effective_from, `${path}.effective_from`),
    effective_until: string(record.effective_until, `${path}.effective_until`),
    authority_sources: array(
      record.authority_sources,
      `${path}.authority_sources`,
      string,
    ),
    is_active: boolean(record.is_active, `${path}.is_active`),
    active_pointer_revision: integer(
      record.active_pointer_revision,
      `${path}.active_pointer_revision`,
    ),
  };
}

function parseThreshold(value: unknown, path: string): RulePackThreshold {
  const record = object(
    value,
    path,
    ["value", "unit", "basis"],
    ["value", "unit"],
  );
  const basis = optionalString(record, "basis", path);
  return {
    value: string(record.value, `${path}.value`),
    unit: string(record.unit, `${path}.unit`),
    ...(basis === undefined ? {} : { basis }),
  };
}

function parseRequiredFact(value: unknown, path: string): RulePackRequiredFact {
  const keys = [
    "dimension",
    "source_roles",
    "object_required",
    "unit_family",
    "measurement_window",
    "time_zone",
    "calendar",
    "outage_definition",
    "exclusions",
    "basis",
  ] as const;
  const record = object(value, path, keys, [
    "dimension",
    "source_roles",
    "object_required",
    "unit_family",
    "exclusions",
  ]);
  const optional = (key: string) => optionalString(record, key, path);
  return {
    dimension: string(record.dimension, `${path}.dimension`),
    source_roles: array(record.source_roles, `${path}.source_roles`, string),
    object_required: boolean(record.object_required, `${path}.object_required`),
    unit_family: string(record.unit_family, `${path}.unit_family`),
    exclusions:
      record.exclusions === null
        ? null
        : array(record.exclusions, `${path}.exclusions`, string),
    ...(optional("measurement_window") === undefined
      ? {}
      : { measurement_window: optional("measurement_window") }),
    ...(optional("time_zone") === undefined
      ? {}
      : { time_zone: optional("time_zone") }),
    ...(optional("calendar") === undefined
      ? {}
      : { calendar: optional("calendar") }),
    ...(optional("outage_definition") === undefined
      ? {}
      : { outage_definition: optional("outage_definition") }),
    ...(optional("basis") === undefined ? {} : { basis: optional("basis") }),
  };
}

function parseRule(value: unknown, path: string): RulePackRule {
  const keys = [
    "rule_id",
    "version",
    "domain",
    "capability_id",
    "mandatory",
    "business_scope",
    "tenant_id",
    "comparator",
    "threshold",
    "required_fact",
    "authority_sources",
  ] as const;
  const record = object(value, path, keys);
  const scope = object(record.business_scope, `${path}.business_scope`, [
    "business_line",
    "scenario",
    "contract_type",
  ]);
  const comparator = string(record.comparator, `${path}.comparator`);
  if (
    comparator !== "minimum_bandwidth" &&
    comparator !== "monthly_sla" &&
    comparator !== "advance_payment_ceiling"
  )
    return fail(`${path}.comparator`);
  const requiredFact = parseRequiredFact(
    record.required_fact,
    `${path}.required_fact`,
  );
  if (comparator === "monthly_sla" && requiredFact.exclusions === null)
    return fail(`${path}.required_fact.exclusions`);
  return {
    rule_id: string(record.rule_id, `${path}.rule_id`),
    version: string(record.version, `${path}.version`),
    domain: string(record.domain, `${path}.domain`),
    capability_id: string(record.capability_id, `${path}.capability_id`),
    mandatory: boolean(record.mandatory, `${path}.mandatory`),
    business_scope: {
      business_line: string(
        scope.business_line,
        `${path}.business_scope.business_line`,
      ),
      scenario: string(scope.scenario, `${path}.business_scope.scenario`),
      contract_type: string(
        scope.contract_type,
        `${path}.business_scope.contract_type`,
      ),
    },
    tenant_id: string(record.tenant_id, `${path}.tenant_id`),
    comparator,
    threshold: parseThreshold(record.threshold, `${path}.threshold`),
    required_fact: requiredFact,
    authority_sources: array(
      record.authority_sources,
      `${path}.authority_sources`,
      parseAuthoritySource,
    ),
  };
}

function parseLifecycle(value: unknown, path: string): RulePackLifecycle {
  const record = object(
    value,
    path,
    ["state", "verification", "approval", "release"],
    ["state"],
  );
  const result: RulePackLifecycle = {
    state: parseState(record.state, `${path}.state`),
  };
  if (Object.hasOwn(record, "verification")) {
    const verification = object(record.verification, `${path}.verification`, [
      "manifest_hash",
      "verifier_version",
      "passed",
    ]);
    result.verification = {
      manifest_hash: string(
        verification.manifest_hash,
        `${path}.verification.manifest_hash`,
      ),
      verifier_version: string(
        verification.verifier_version,
        `${path}.verification.verifier_version`,
      ),
      passed: boolean(verification.passed, `${path}.verification.passed`),
    };
  }
  if (Object.hasOwn(record, "approval")) {
    const approval = object(record.approval, `${path}.approval`, [
      "manifest_hash",
      "verification_hash",
      "actor_id",
    ]);
    result.approval = {
      manifest_hash: string(
        approval.manifest_hash,
        `${path}.approval.manifest_hash`,
      ),
      verification_hash: string(
        approval.verification_hash,
        `${path}.approval.verification_hash`,
      ),
      actor_id: string(approval.actor_id, `${path}.approval.actor_id`),
    };
  }
  if (Object.hasOwn(record, "release")) {
    const release = object(record.release, `${path}.release`, [
      "manifest_hash",
      "approval_hash",
      "release_hash",
    ]);
    result.release = {
      manifest_hash: string(
        release.manifest_hash,
        `${path}.release.manifest_hash`,
      ),
      approval_hash: string(
        release.approval_hash,
        `${path}.release.approval_hash`,
      ),
      release_hash: string(
        release.release_hash,
        `${path}.release.release_hash`,
      ),
    };
  }
  return result;
}

export function parseRulePackManifest(
  value: unknown,
  path = "manifest",
): RulePackManifest {
  const keys = [
    "schema_version",
    "id",
    "version",
    "tenant_id",
    "effective_from",
    "effective_until",
    "rules",
    "lifecycle",
  ] as const;
  const record = object(value, path, keys);
  if (record.schema_version !== "rulepack/v1")
    return fail(`${path}.schema_version`);
  return {
    schema_version: "rulepack/v1",
    id: nonEmptyString(record.id, `${path}.id`),
    version: nonEmptyString(record.version, `${path}.version`),
    tenant_id: nonEmptyString(record.tenant_id, `${path}.tenant_id`),
    effective_from: string(record.effective_from, `${path}.effective_from`),
    effective_until: string(record.effective_until, `${path}.effective_until`),
    rules: array(record.rules, `${path}.rules`, parseRule),
    lifecycle: parseLifecycle(record.lifecycle, `${path}.lifecycle`),
  };
}

export function parseRulePackDetail(value: unknown): RulePackDetail {
  const record = object(value, "detail", ["pack", "manifest"]);
  return {
    pack: parseSummary(record.pack),
    manifest: parseRulePackManifest(record.manifest),
  };
}

export function parseRulePackMutationResult(
  value: unknown,
): RulePackMutationResult {
  const record = object(value, "mutation", ["pack", "manifest"], ["pack"]);
  return Object.hasOwn(record, "manifest")
    ? parseRulePackDetail(record)
    : parseSummary(record.pack);
}

function parseHistoryEntry(value: unknown, path: string): RulePackHistoryEntry {
  const keys = [
    "sequence",
    "pack_id",
    "version",
    "action",
    "actor_id",
    "content_hash",
    "pointer_revision",
    "created_at",
    "previous_version",
    "previous_active",
    "resulting_version",
    "resulting_active",
  ] as const;
  const record = object(value, path, keys);
  return {
    sequence: positiveInteger(record.sequence, `${path}.sequence`),
    pack_id: string(record.pack_id, `${path}.pack_id`),
    version: string(record.version, `${path}.version`),
    action: parseHistoryAction(record.action, `${path}.action`),
    actor_id: string(record.actor_id, `${path}.actor_id`),
    content_hash: sha256(record.content_hash, `${path}.content_hash`),
    pointer_revision: integer(
      record.pointer_revision,
      `${path}.pointer_revision`,
    ),
    created_at: string(record.created_at, `${path}.created_at`),
    previous_version: nullableString(
      record.previous_version,
      `${path}.previous_version`,
    ),
    previous_active: nullableBoolean(
      record.previous_active,
      `${path}.previous_active`,
    ),
    resulting_version: nullableString(
      record.resulting_version,
      `${path}.resulting_version`,
    ),
    resulting_active: nullableBoolean(
      record.resulting_active,
      `${path}.resulting_active`,
    ),
  };
}

function list<T>(
  value: unknown,
  name: string,
  parse: (entry: unknown, path: string) => T,
): T[] {
  const record = object(value, name, ["items"]);
  return array(record.items, `${name}.items`, parse);
}

export function parseRulePackList(value: unknown): RulePackSummary[] {
  return list(value, "list", parseSummary);
}

export function parseRulePackHistory(value: unknown): RulePackHistoryEntry[] {
  return list(value, "history", parseHistoryEntry);
}

function parseReportCitation(
  value: unknown,
  path: string,
): RulePackReportCitation {
  const record = object(value, path, [
    "source_id",
    "source_version",
    "tenant_id",
    "case_id",
    "source_digest",
    "start",
    "end",
    "quote",
  ]);
  return {
    source_id: nonEmptyString(record.source_id, `${path}.source_id`),
    source_version: nonEmptyString(
      record.source_version,
      `${path}.source_version`,
    ),
    tenant_id: nonEmptyString(record.tenant_id, `${path}.tenant_id`),
    case_id: nonEmptyString(record.case_id, `${path}.case_id`),
    source_digest: sha256(record.source_digest, `${path}.source_digest`),
    start: integer(record.start, `${path}.start`),
    end: integer(record.end, `${path}.end`),
    quote: string(record.quote, `${path}.quote`),
  };
}

function parseTestResult(value: unknown, path: string): RulePackTestResult {
  const keys = [
    "rule_id",
    "rule_version",
    "domain",
    "capability_id",
    "mandatory",
    "relation",
    "reason_code",
    "fact_ids",
    "citations",
  ] as const;
  const record = object(value, path, keys, [
    "rule_id",
    "rule_version",
    "domain",
    "capability_id",
    "mandatory",
    "relation",
    "reason_code",
  ]);
  return {
    rule_id: string(record.rule_id, `${path}.rule_id`),
    rule_version: string(record.rule_version, `${path}.rule_version`),
    domain: string(record.domain, `${path}.domain`),
    capability_id: string(record.capability_id, `${path}.capability_id`),
    mandatory: boolean(record.mandatory, `${path}.mandatory`),
    relation: parseRelation(record.relation, `${path}.relation`),
    reason_code: string(record.reason_code, `${path}.reason_code`),
    ...(Object.hasOwn(record, "fact_ids")
      ? { fact_ids: array(record.fact_ids, `${path}.fact_ids`, string) }
      : {}),
    ...(Object.hasOwn(record, "citations")
      ? {
          citations: array(
            record.citations,
            `${path}.citations`,
            parseReportCitation,
          ),
        }
      : {}),
  };
}

function parseTestReport(value: unknown, path: string): RulePackTestReport {
  const record = object(value, path, [
    "pack_id",
    "pack_version",
    "pack_hash",
    "relations",
    "results",
  ]);
  return {
    pack_id: string(record.pack_id, `${path}.pack_id`),
    pack_version: string(record.pack_version, `${path}.pack_version`),
    pack_hash: string(record.pack_hash, `${path}.pack_hash`),
    relations: array(record.relations, `${path}.relations`, parseRelation),
    results: array(record.results, `${path}.results`, parseTestResult),
  };
}

export function parseRulePackTestResponse(
  value: unknown,
): RulePackTestResponse {
  const record = object(value, "test", ["revision", "content_hash", "report"]);
  return {
    revision: positiveInteger(record.revision, "test.revision"),
    content_hash: sha256(record.content_hash, "test.content_hash"),
    report: parseTestReport(record.report, "test.report"),
  };
}

export function listRulePacks(signal?: AbortSignal) {
  return requestJson(
    paths.rulePacks,
    { method: "GET", signal },
    parseRulePackList,
  );
}

export function getRulePack(
  packId: string,
  version: string,
  signal?: AbortSignal,
) {
  return requestJson(
    paths.rulePack(packId, version),
    { method: "GET", signal },
    parseRulePackDetail,
  );
}

export function getRulePackHistory(packId: string, signal?: AbortSignal) {
  return requestJson(
    paths.rulePackHistory(packId),
    { method: "GET", signal },
    parseRulePackHistory,
  );
}

export function createRulePack(manifest: RulePackManifestInput) {
  return requestJson(
    paths.rulePacks,
    jsonInit("POST", { manifest }),
    parseRulePackDetail,
  );
}

export function editRulePack(
  packId: string,
  version: string,
  expectedRevision: number,
  manifest: RulePackManifestInput,
) {
  return requestJson(
    paths.rulePack(packId, version),
    jsonInit("PUT", { expected_revision: expectedRevision, manifest }),
    parseRulePackDetail,
  );
}

export function validateRulePack(
  packId: string,
  version: string,
  expectedRevision: number,
) {
  return requestJson(
    paths.rulePackAction(packId, version, "validate"),
    jsonInit("POST", { expected_revision: expectedRevision }),
    parseRulePackDetail,
  );
}

export function approveRulePack(
  packId: string,
  version: string,
  expectedRevision: number,
  expectedContentHash: string,
) {
  return requestJson(
    paths.rulePackAction(packId, version, "approve"),
    jsonInit("POST", {
      expected_revision: expectedRevision,
      expected_content_hash: expectedContentHash,
    }),
    parseRulePackDetail,
  );
}

export function publishRulePack(
  packId: string,
  version: string,
  expectedRevision: number,
  expectedPointerRevision: number,
  idempotencyKey: string,
) {
  return requestJson(
    paths.rulePackAction(packId, version, "publish"),
    jsonInit(
      "POST",
      {
        expected_revision: expectedRevision,
        expected_pointer_revision: expectedPointerRevision,
      },
      { "Idempotency-Key": idempotencyKey },
    ),
    parseRulePackMutationResult,
  );
}

export function disableRulePack(
  packId: string,
  version: string,
  expectedRevision: number,
  expectedPointerRevision: number,
) {
  return requestJson(
    paths.rulePackAction(packId, version, "disable"),
    jsonInit("POST", {
      expected_revision: expectedRevision,
      expected_pointer_revision: expectedPointerRevision,
    }),
    parseRulePackMutationResult,
  );
}

export function rollbackRulePack(
  packId: string,
  version: string,
  expectedPointerRevision: number,
  idempotencyKey: string,
) {
  return requestJson(
    paths.rulePackRollback(packId),
    jsonInit(
      "POST",
      { version, expected_pointer_revision: expectedPointerRevision },
      { "Idempotency-Key": idempotencyKey },
    ),
    parseRulePackMutationResult,
  );
}

export function testRulePack(
  packId: string,
  version: string,
  input: RulePackTestInput,
) {
  return requestJson(
    paths.rulePackAction(packId, version, "test"),
    jsonInit("POST", input),
    parseRulePackTestResponse,
  );
}
