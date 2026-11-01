import { ApiError, errorFromResponse, jsonInit, requestJson } from "./http";
import { responseReader } from "./respond";

/**
 * Contract registration and resumable drafts.
 *
 * The draft body is a full replacement: a PUT deletes every field the client
 * omits, so the editor keeps the loaded content and only overwrites the values
 * it actually edits. Restricted supplier fields arrive masked for viewers
 * without manage authority, and such viewers never receive the write command.
 */

export interface ContractMoneyFields {
  amount?: string;
  currency?: string;
  settlement_mode?: string;
  prices_include_tax?: boolean;
}

export interface ContractBasics extends ContractMoneyFields {
  project_number?: string;
  project_type?: string;
  name?: string;
  number?: string;
  classification?: string;
  category?: string;
  grade?: string;
  drafting_party?: string;
  department_id?: string;
  handler_user_id?: string;
  handler_contact?: string;
  related_parties?: PartyRef[];
  is_ppp_project?: boolean | null;
  is_ndrc_engineering_project?: boolean | null;
  is_fixed_asset?: boolean | null;
  procurement_result_package?: ProcurementRef;
  procurement_method?: string;
  signing_date?: string;
  performance_start_date?: string;
  performance_end_date?: string;
  performance_location?: string;
  business_terms_verified?: boolean;
  performance_bond_required?: boolean | null;
  remark?: string;
}

export interface PartyReferenceSnapshot {
  binding_id: string;
  binding_version: number;
  case_role: string;
  binding_state: string;
  subject_id: string;
  subject_version: number;
  subject_payload_hash: string;
  verification_state: string;
  display_name: string;
  captured_at: string;
}

export interface PartyRef {
  party_id: string;
  role: string;
  name_snapshot: string;
  binding_id?: string;
  reference_snapshot?: PartyReferenceSnapshot;
}

export interface ProcurementSourceSnapshot {
  system: string;
  result_id: string;
  package_id: string;
  snapshot_ref: string;
  material_id: string;
  material_version_id: string;
  material_version: number;
  material_role: "procurement" | "award";
  sha256: string;
  captured_at: string;
}

export interface ProcurementRef {
  system: string;
  result_id: string;
  package_id: string;
  snapshot_ref: string;
  source_snapshot?: ProcurementSourceSnapshot;
}

export interface UnitRef {
  id: string;
  name: string;
}

export interface DraftBuyer {
  purchasing_unit?: UnitRef;
  contact_name?: string;
  contact_phone?: string;
}

export interface DraftSupplier {
  enterprise_type?: string;
  name?: string;
  credit_code?: string;
  enterprise_scale?: string;
  accepting_entity?: string;
  region?: string;
  address?: string;
  special_nature?: string[];
  payee_name?: string;
  bank_name?: string;
  bank_account?: string;
  contact_name?: string;
  contact_phone?: string;
  tax_identifier?: string;
}

export interface DraftItem {
  item_id: string;
  name?: string;
  catalog?: string;
  brand?: string;
  specification?: string;
  quantity?: string;
  unit?: string;
  unit_price?: string;
  budget_amount?: string;
  line_amount?: string;
  tax_rate?: string;
}

export interface DraftFunding {
  nature?: string;
  channel?: string;
  amount?: string;
  funding_year?: number;
  cost_center?: string;
  quota_number?: string;
}

export interface DraftPaymentPlan {
  sequence: number;
  is_advance?: boolean;
  description?: string;
  planned_date?: string;
  advance_amount?: string;
  unit_amount?: string;
  fiscal_amount?: string;
  amount?: string;
  ratio?: string;
  ratio_base?: string;
  condition?: PaymentCondition;
}

export interface PaymentCondition {
  type: string;
  evidence_requirements?: string[];
  due_rule?: DueRule;
  policy_ref?: string;
}

export interface DueRule {
  trigger: string;
  days_after?: number;
  calendar?: string;
  policy_ref?: string;
}

export interface DraftDeliveryPlan {
  sequence: number;
  item_id?: string;
  planned_date?: string;
  description?: string;
  quantity?: string;
}

export interface DraftAcceptancePlan {
  sequence: number;
  planned_date?: string;
  description?: string;
  criteria?: string;
  item_ids?: string[];
}

export interface MaterialRef {
  material_id: string;
  material_version_id: string;
  sha256: string;
  purpose: string;
}

export interface DraftContent {
  contract: ContractBasics;
  buyer?: DraftBuyer;
  supplier?: DraftSupplier;
  items?: DraftItem[];
  assets?: unknown[];
  funding?: DraftFunding[];
  payments?: DraftPaymentPlan[];
  delivery_plans?: DraftDeliveryPlan[];
  acceptance_plans?: DraftAcceptancePlan[];
  materials?: MaterialRef[];
}

export interface ContractSummary {
  contract_id: string;
  authority_mode: string;
  name: string;
  number: string | null;
  amount: string | null;
  currency: string | null;
  contract_state: string;
  workflow_state: string;
  payment_state?: string | null;
  row_version: number;
  created_at: string;
}

export interface ContractDraftSummary {
  contract_id: string;
  authority_mode: string;
  name: string;
  number: string | null;
  amount: string | null;
  currency: string | null;
  contract_state: string;
  workflow_state: string;
  row_version: number;
  created_at: string;
  updated_at?: string;
}

export interface SourceRef {
  system: string;
  id: string;
  source_version: string;
}

export interface ContractDetail {
  summary: ContractSummary;
  current_revision_id: string | null;
  draft?: DraftContent;
  active_round_id: string | null;
  allowed_commands: string[];
  source_ref?: SourceRef;
  source_observed_at?: string;
  warnings: string[];
}

export interface LegacyImportInput {
  source_ref: SourceRef;
  source_evidence: MaterialRef;
  snapshot: DraftContent;
  approval_history_status: "unknown" | "evidence_attached";
  historical_approval_materials: MaterialRef[];
  reason: string;
}

export interface ContractSubjectReference {
  party_id: string;
  role: string;
  name_snapshot: string;
  binding_id: string;
  binding_version: number;
  case_role: string;
  binding_state: string;
  subject_version: number;
  subject_payload_hash: string;
  verification_state: string;
  frozen: boolean;
}

export interface ContractMaterialReference {
  material_id: string;
  material_version_id: string;
  sha256: string;
  purpose: string;
  material_version: number;
  material_role: string;
  filename: string;
  media_type: string;
  frozen: boolean;
  original_path: string;
}

export interface ContractReferenceCheck {
  contract_id: string;
  source: "draft" | "revision";
  contract_revision_id: string | null;
  source_case_id: string;
  checked_at: string;
  subjects: ContractSubjectReference[];
  materials: ContractMaterialReference[];
  procurement_result?: ProcurementSourceSnapshot;
  warnings: string[];
}

/** Restricted supplier fields are sealed server-side; viewers see fixed masks. */
export const RESTRICTED_MASKS = {
  payee_name: "•••",
  bank_name: "••••",
  bank_account: "••••••••",
  tax_identifier: "••••••",
} as const;

export function isMaskedRestricted(value: string | undefined): boolean {
  if (value === undefined) return false;
  return Object.values(RESTRICTED_MASKS).includes(
    value as (typeof RESTRICTED_MASKS)[keyof typeof RESTRICTED_MASKS],
  );
}

const CONTRACTS_PATH = "/api/contracts";

const { object, record, text, optionalText, nullableText, integer, bool, array } =
  responseReader((path) => `合同响应中的 ${path} 无法识别`);

function parseSourceRef(value: unknown, path: string): SourceRef {
  const row = object(value, path);
  return {
    system: text(row.system, `${path}.system`),
    id: text(row.id, `${path}.id`),
    source_version: text(row.source_version, `${path}.source_version`),
  };
}

function parseMaterialRef(value: unknown, path: string): MaterialRef {
  const row = object(value, path);
  const sha = parseSha256(row.sha256, `${path}.sha256`);
  return {
    material_id: text(row.material_id, `${path}.material_id`),
    material_version_id: text(row.material_version_id, `${path}.material_version_id`),
    sha256: sha,
    purpose: text(row.purpose, `${path}.purpose`),
  };
}

function parseSha256(value: unknown, path: string): string {
  const sha = text(value, path);
  if (!/^[a-f0-9]{64}$/.test(sha))
    throw new Error(`合同响应中的 ${path} 不是 SHA-256 摘要`);
  return sha;
}

function parseBoolean(value: unknown, path: string): boolean {
  if (typeof value !== "boolean")
    throw new Error(`合同响应中的 ${path} 无法识别`);
  return value;
}

function parsePartyRef(value: unknown, path: string): PartyRef {
  const row = record(
    value,
    path,
    ["party_id", "role", "name_snapshot", "binding_id", "reference_snapshot"],
    ["party_id", "role", "name_snapshot"],
  );
  const party: PartyRef = {
    party_id: text(row.party_id, `${path}.party_id`),
    role: text(row.role, `${path}.role`),
    name_snapshot: text(row.name_snapshot, `${path}.name_snapshot`),
  };
  if (row.binding_id !== undefined)
    party.binding_id = text(row.binding_id, `${path}.binding_id`);
  if (row.reference_snapshot !== undefined && row.reference_snapshot !== null)
    party.reference_snapshot = parsePartyReferenceSnapshot(
      row.reference_snapshot,
      `${path}.reference_snapshot`,
    );
  return party;
}

function parseProcurementRef(value: unknown, path: string): ProcurementRef {
  const row = record(
    value,
    path,
    ["system", "result_id", "package_id", "snapshot_ref", "source_snapshot"],
    ["system", "result_id", "package_id", "snapshot_ref"],
  );
  const ref: ProcurementRef = {
    system: text(row.system, `${path}.system`),
    result_id: text(row.result_id, `${path}.result_id`),
    package_id: text(row.package_id, `${path}.package_id`),
    snapshot_ref: text(row.snapshot_ref, `${path}.snapshot_ref`),
  };
  if (row.source_snapshot !== undefined && row.source_snapshot !== null)
    ref.source_snapshot = parseProcurementSourceSnapshot(
      row.source_snapshot,
      `${path}.source_snapshot`,
    );
  return ref;
}

function parseDraft(value: unknown, path: string): DraftContent {
  const row = object(value, path);
  if (row.contract === undefined || row.contract === null)
    throw new Error(`合同响应中的 ${path}.contract 缺失`);
  const draft = row as unknown as DraftContent;
  const contractRow = object(row.contract, `${path}.contract`);
  if (Array.isArray(contractRow.related_parties)) {
    draft.contract.related_parties = contractRow.related_parties.map(
      (entry, index) =>
        parsePartyRef(entry, `${path}.contract.related_parties[${index}]`),
    );
  }
  if (
    contractRow.procurement_result_package !== undefined &&
    contractRow.procurement_result_package !== null
  ) {
    draft.contract.procurement_result_package = parseProcurementRef(
      contractRow.procurement_result_package,
      `${path}.contract.procurement_result_package`,
    );
  }
  if (Array.isArray(row.materials)) {
    draft.materials = row.materials.map((entry, index) =>
      parseMaterialRef(entry, `${path}.materials[${index}]`),
    );
  }
  return draft;
}

function parseSummary(value: unknown, path: string): ContractSummary {
  const row = object(value, path);
  return {
    contract_id: text(row.contract_id, `${path}.contract_id`),
    authority_mode: text(row.authority_mode, `${path}.authority_mode`),
    name: text(row.name, `${path}.name`),
    number: nullableText(row.number, `${path}.number`),
    amount: nullableText(row.amount, `${path}.amount`),
    currency: nullableText(row.currency, `${path}.currency`),
    contract_state: text(row.contract_state, `${path}.contract_state`),
    workflow_state: text(row.workflow_state, `${path}.workflow_state`),
    payment_state:
      row.payment_state === undefined
        ? undefined
        : nullableText(row.payment_state, `${path}.payment_state`),
    row_version: integer(row.row_version, `${path}.row_version`, 1),
    created_at: text(row.created_at, `${path}.created_at`),
  };
}

export function parseContractDetail(
  value: unknown,
  path = "contract",
): ContractDetail {
  const row = record(
    value,
    path,
    [
      "summary",
      "current_revision_id",
      "draft",
      "active_round_id",
      "allowed_commands",
      "source_ref",
      "source_observed_at",
      "warnings",
    ],
    ["summary", "current_revision_id", "active_round_id", "allowed_commands", "warnings"],
  );
  return {
    summary: parseSummary(row.summary, `${path}.summary`),
    current_revision_id: nullableText(
      row.current_revision_id,
      `${path}.current_revision_id`,
    ),
    draft:
      row.draft === undefined || row.draft === null
        ? undefined
        : parseDraft(row.draft, `${path}.draft`),
    active_round_id: nullableText(row.active_round_id, `${path}.active_round_id`),
    allowed_commands: array(
      row.allowed_commands,
      `${path}.allowed_commands`,
      (entry, entryPath) => text(entry, entryPath),
    ),
    source_ref:
      row.source_ref === undefined || row.source_ref === null
        ? undefined
        : parseSourceRef(row.source_ref, `${path}.source_ref`),
    source_observed_at: optionalText(
      row.source_observed_at,
      `${path}.source_observed_at`,
    ) ?? undefined,
    warnings: array(row.warnings, `${path}.warnings`, (entry, entryPath) =>
      text(entry, entryPath),
    ),
  };
}

export function parseContractDraftList(value: unknown): ContractDraftSummary[] {
  const row = object(value, "contracts");
  return array(row.items, "contracts.items", (entry, path) => {
    const draft = record(
      entry,
      path,
      [
        "contract_id",
        "authority_mode",
        "name",
        "number",
        "amount",
        "currency",
        "contract_state",
        "workflow_state",
        "row_version",
        "created_at",
        "updated_at",
      ],
      ["contract_id", "name", "contract_state", "workflow_state", "row_version", "created_at"],
    );
    return {
      contract_id: text(draft.contract_id, `${path}.contract_id`),
      authority_mode: text(draft.authority_mode, `${path}.authority_mode`),
      name: text(draft.name, `${path}.name`),
      number: nullableText(draft.number, `${path}.number`),
      amount: nullableText(draft.amount, `${path}.amount`),
      currency: nullableText(draft.currency, `${path}.currency`),
      contract_state: text(draft.contract_state, `${path}.contract_state`),
      workflow_state: text(draft.workflow_state, `${path}.workflow_state`),
      row_version: integer(draft.row_version, `${path}.row_version`, 1),
      created_at: text(draft.created_at, `${path}.created_at`),
      updated_at: optionalText(draft.updated_at, `${path}.updated_at`) ?? undefined,
    };
  });
}

function parsePartyReferenceSnapshot(
  value: unknown,
  path: string,
): PartyReferenceSnapshot {
  const row = record(
    value,
    path,
    [
      "binding_id",
      "binding_version",
      "case_role",
      "binding_state",
      "subject_id",
      "subject_version",
      "subject_payload_hash",
      "verification_state",
      "display_name",
      "captured_at",
    ],
    [
      "binding_id",
      "binding_version",
      "case_role",
      "binding_state",
      "subject_id",
      "subject_version",
      "subject_payload_hash",
      "verification_state",
      "display_name",
      "captured_at",
    ],
  );
  return {
    binding_id: text(row.binding_id, `${path}.binding_id`),
    binding_version: integer(row.binding_version, `${path}.binding_version`, 1),
    case_role: text(row.case_role, `${path}.case_role`),
    binding_state: text(row.binding_state, `${path}.binding_state`),
    subject_id: text(row.subject_id, `${path}.subject_id`),
    subject_version: integer(row.subject_version, `${path}.subject_version`, 1),
    subject_payload_hash: parseSha256(
      row.subject_payload_hash,
      `${path}.subject_payload_hash`,
    ),
    verification_state: text(row.verification_state, `${path}.verification_state`),
    display_name: text(row.display_name, `${path}.display_name`),
    captured_at: text(row.captured_at, `${path}.captured_at`),
  };
}

function parseProcurementSourceSnapshot(
  value: unknown,
  path: string,
): ProcurementSourceSnapshot {
  const row = record(
    value,
    path,
    [
      "system",
      "result_id",
      "package_id",
      "snapshot_ref",
      "material_id",
      "material_version_id",
      "material_version",
      "material_role",
      "sha256",
      "captured_at",
    ],
    [
      "system",
      "result_id",
      "package_id",
      "snapshot_ref",
      "material_id",
      "material_version_id",
      "material_version",
      "material_role",
      "sha256",
      "captured_at",
    ],
  );
  const role = text(row.material_role, `${path}.material_role`);
  if (role !== "procurement" && role !== "award")
    throw new Error(`合同响应中的 ${path}.material_role 不是采购或中标材料`);
  return {
    system: text(row.system, `${path}.system`),
    result_id: text(row.result_id, `${path}.result_id`),
    package_id: text(row.package_id, `${path}.package_id`),
    snapshot_ref: text(row.snapshot_ref, `${path}.snapshot_ref`),
    material_id: text(row.material_id, `${path}.material_id`),
    material_version_id: text(
      row.material_version_id,
      `${path}.material_version_id`,
    ),
    material_version: integer(row.material_version, `${path}.material_version`, 1),
    material_role: role,
    sha256: parseSha256(row.sha256, `${path}.sha256`),
    captured_at: text(row.captured_at, `${path}.captured_at`),
  };
}

function parseSubjectReference(
  value: unknown,
  path: string,
): ContractSubjectReference {
  const row = record(
    value,
    path,
    [
      "party_id",
      "role",
      "name_snapshot",
      "binding_id",
      "binding_version",
      "case_role",
      "binding_state",
      "subject_version",
      "subject_payload_hash",
      "verification_state",
      "frozen",
    ],
    [
      "party_id",
      "role",
      "name_snapshot",
      "binding_id",
      "binding_version",
      "case_role",
      "binding_state",
      "subject_version",
      "subject_payload_hash",
      "verification_state",
      "frozen",
    ],
  );
  return {
    party_id: text(row.party_id, `${path}.party_id`),
    role: text(row.role, `${path}.role`),
    name_snapshot: text(row.name_snapshot, `${path}.name_snapshot`),
    binding_id: text(row.binding_id, `${path}.binding_id`),
    binding_version: integer(row.binding_version, `${path}.binding_version`, 1),
    case_role: text(row.case_role, `${path}.case_role`),
    binding_state: text(row.binding_state, `${path}.binding_state`),
    subject_version: integer(row.subject_version, `${path}.subject_version`, 1),
    subject_payload_hash: parseSha256(
      row.subject_payload_hash,
      `${path}.subject_payload_hash`,
    ),
    verification_state: text(row.verification_state, `${path}.verification_state`),
    frozen: parseBoolean(row.frozen, `${path}.frozen`),
  };
}

function parseMaterialReference(
  value: unknown,
  path: string,
): ContractMaterialReference {
  const row = record(
    value,
    path,
    [
      "material_id",
      "material_version_id",
      "sha256",
      "purpose",
      "material_version",
      "material_role",
      "filename",
      "media_type",
      "frozen",
      "original_path",
    ],
    [
      "material_id",
      "material_version_id",
      "sha256",
      "purpose",
      "material_version",
      "material_role",
      "filename",
      "media_type",
      "frozen",
      "original_path",
    ],
  );
  return {
    material_id: text(row.material_id, `${path}.material_id`),
    material_version_id: text(
      row.material_version_id,
      `${path}.material_version_id`,
    ),
    sha256: parseSha256(row.sha256, `${path}.sha256`),
    purpose: text(row.purpose, `${path}.purpose`),
    material_version: integer(row.material_version, `${path}.material_version`, 1),
    material_role: text(row.material_role, `${path}.material_role`),
    filename: text(row.filename, `${path}.filename`),
    media_type: text(row.media_type, `${path}.media_type`),
    frozen: parseBoolean(row.frozen, `${path}.frozen`),
    original_path: text(row.original_path, `${path}.original_path`),
  };
}

export function parseContractReferenceCheck(
  value: unknown,
): ContractReferenceCheck {
  const row = record(
    value,
    "contract_reference_check",
    [
      "contract_id",
      "source",
      "contract_revision_id",
      "source_case_id",
      "checked_at",
      "subjects",
      "materials",
      "procurement_result",
      "warnings",
    ],
    [
      "contract_id",
      "source",
      "contract_revision_id",
      "source_case_id",
      "checked_at",
      "subjects",
      "materials",
      "warnings",
    ],
  );
  const source = text(row.source, "contract_reference_check.source");
  if (source !== "draft" && source !== "revision")
    throw new Error("合同响应中的 contract_reference_check.source 无法识别");
  return {
    contract_id: text(row.contract_id, "contract_reference_check.contract_id"),
    source,
    contract_revision_id: nullableText(
      row.contract_revision_id,
      "contract_reference_check.contract_revision_id",
    ),
    source_case_id: text(
      row.source_case_id,
      "contract_reference_check.source_case_id",
    ),
    checked_at: text(row.checked_at, "contract_reference_check.checked_at"),
    subjects: array(
      row.subjects,
      "contract_reference_check.subjects",
      (entry, path) => parseSubjectReference(entry, path),
    ),
    materials: array(
      row.materials,
      "contract_reference_check.materials",
      (entry, path) => parseMaterialReference(entry, path),
    ),
    procurement_result:
      row.procurement_result === undefined || row.procurement_result === null
        ? undefined
        : parseProcurementSourceSnapshot(
            row.procurement_result,
            "contract_reference_check.procurement_result",
          ),
    warnings: array(
      row.warnings,
      "contract_reference_check.warnings",
      (entry, path) => text(entry, path),
    ),
  };
}

export function emptyDraft(): DraftContent {
  return { contract: {} };
}

export function createContractDraft(
  draft: DraftContent,
  idempotencyKey: string,
): Promise<ContractDetail> {
  return requestJson(
    CONTRACTS_PATH,
    jsonInit(
      "POST",
      { expected_row_version: 0, authority_mode: "native", draft },
      { "Idempotency-Key": idempotencyKey },
    ),
    (value) => parseContractDetail(value),
  );
}

export function listMyContractDrafts(
  signal?: AbortSignal,
): Promise<ContractDraftSummary[]> {
  return requestJson(
    `${CONTRACTS_PATH}/drafts?limit=50`,
    { method: "GET", signal },
    parseContractDraftList,
  );
}

export function getContract(
  contractId: string,
  signal?: AbortSignal,
): Promise<ContractDetail> {
  return requestJson(
    `${CONTRACTS_PATH}/${encodeURIComponent(contractId)}`,
    { method: "GET", signal },
    (value) => parseContractDetail(value),
  );
}

export function checkContractReferences(
  contractId: string,
  caseId?: string,
  signal?: AbortSignal,
): Promise<ContractReferenceCheck> {
  const query = caseId === undefined ? "" : `?case_id=${encodeURIComponent(caseId)}`;
  return requestJson(
    `${CONTRACTS_PATH}/${encodeURIComponent(contractId)}/reference-checks${query}`,
    { method: "GET", signal },
    parseContractReferenceCheck,
  );
}

export function replaceContractDraft(
  contractId: string,
  expectedRowVersion: number,
  draft: DraftContent,
  idempotencyKey: string,
): Promise<ContractDetail> {
  return requestJson(
    `${CONTRACTS_PATH}/${encodeURIComponent(contractId)}/draft`,
    jsonInit(
      "PUT",
      { expected_row_version: expectedRowVersion, replacement_draft: draft },
      { "Idempotency-Key": idempotencyKey },
    ),
    (value) => parseContractDetail(value),
  );
}

export function importLegacyContract(
  input: LegacyImportInput,
  idempotencyKey: string,
): Promise<ContractDetail> {
  return requestJson(
    `${CONTRACTS_PATH}/legacy-imports`,
    jsonInit(
      "POST",
      {
        expected_row_version: 0,
        authority_mode: "native",
        source_ref: input.source_ref,
        source_evidence: parseMaterialRefForSend(input.source_evidence),
        snapshot: input.snapshot,
        approval_history_status: input.approval_history_status,
        historical_approval_materials: input.historical_approval_materials.map(
          parseMaterialRefForSend,
        ),
        reason: input.reason,
      },
      { "Idempotency-Key": idempotencyKey },
    ),
    (value) => parseContractDetail(value),
  );
}

function parseMaterialRefForSend(ref: MaterialRef): MaterialRef {
  return {
    material_id: ref.material_id,
    material_version_id: ref.material_version_id,
    sha256: ref.sha256,
    purpose: ref.purpose,
  };
}

/**
 * Contract ledger, personal saved views and frozen CSV exports.
 *
 * The filter shape is shared by all three so a saved view can be replayed as a
 * live query and an export can freeze exactly what the list showed. The cursor
 * is opaque and must be returned unchanged.
 */

export const CONTRACT_LEDGER_COLUMNS = [
  { key: "contract_number", label: "合同编号" },
  { key: "contract_name", label: "合同名称" },
  { key: "authority_mode", label: "权威模式" },
  { key: "supplier_name", label: "供应商" },
  { key: "amount", label: "金额" },
  { key: "currency", label: "币种" },
  { key: "department_id", label: "申请部门" },
  { key: "project_type", label: "项目类型" },
  { key: "procurement_method", label: "采购方式" },
  { key: "contract_state", label: "合同状态" },
  { key: "workflow_state", label: "会签状态" },
  { key: "signing_date", label: "签订日期" },
  { key: "source_system", label: "来源系统" },
  { key: "created_at", label: "创建时间" },
  { key: "updated_at", label: "更新时间" },
] as const;

export type ContractLedgerColumn =
  (typeof CONTRACT_LEDGER_COLUMNS)[number]["key"];

export const CONTRACT_LEDGER_DEFAULT_COLUMNS: ContractLedgerColumn[] = [
  "contract_number",
  "contract_name",
  "supplier_name",
  "amount",
  "currency",
  "department_id",
  "contract_state",
  "workflow_state",
  "updated_at",
];

export interface ContractLedgerFilter {
  text?: string;
  supplier_name?: string;
  department_id?: string;
  contract_state?: string;
  workflow_state?: string;
  authority_mode?: string;
  validity_state?: string;
  project_type?: string;
  procurement_method?: string;
  amount_min?: string;
  amount_max?: string;
  signing_date_from?: string;
  signing_date_to?: string;
  created_from?: string;
  created_to?: string;
}

export interface ContractLedgerItem {
  contract_id: string;
  authority_mode: string;
  name: string;
  number: string | null;
  amount: string | null;
  currency: string | null;
  contract_state: string;
  workflow_state: string;
  supplier_name?: string | null;
  department_id?: string | null;
  project_type?: string | null;
  procurement_method?: string | null;
  signing_date?: string | null;
  source_system?: string | null;
  owner_org_id: string;
  row_version: number;
  created_at: string;
  updated_at: string;
}

export interface ContractLedgerPage {
  items: ContractLedgerItem[];
  next_cursor: string | null;
  has_more: boolean;
}

export interface ContractSavedView {
  view_id: string;
  name: string;
  filter: ContractLedgerFilter;
  columns: ContractLedgerColumn[];
  row_version: number;
  created_at: string;
  updated_at: string;
}

export interface ContractExport {
  export_id: string;
  state: "ready";
  format: "csv";
  columns: ContractLedgerColumn[];
  row_count: number;
  created_at: string;
  expires_at: string;
  download_available: boolean;
  replayed: boolean;
}

const LEDGER_FILTER_KEYS = [
  "text",
  "supplier_name",
  "department_id",
  "contract_state",
  "workflow_state",
  "authority_mode",
  "validity_state",
  "project_type",
  "procurement_method",
  "amount_min",
  "amount_max",
  "signing_date_from",
  "signing_date_to",
  "created_from",
  "created_to",
] as const;

function ledgerColumn(value: unknown, path: string): ContractLedgerColumn {
  const candidate = text(value, path);
  if (!CONTRACT_LEDGER_COLUMNS.some((column) => column.key === candidate))
    throw new Error(`合同响应中的 ${path} 不是支持的台账列`);
  return candidate as ContractLedgerColumn;
}

function parseLedgerFilter(value: unknown, path: string): ContractLedgerFilter {
  const row = record(value, path, LEDGER_FILTER_KEYS, []);
  const filter: ContractLedgerFilter = {};
  const read = (key: keyof ContractLedgerFilter) => {
    const entry = row[key];
    if (entry === undefined || entry === null) return;
    filter[key] = text(entry, `${path}.${key}`);
  };
  for (const key of LEDGER_FILTER_KEYS) read(key);
  return filter;
}

function parseLedgerItem(value: unknown, path: string): ContractLedgerItem {
  const item = record(
    value,
    path,
    [
      "contract_id",
      "authority_mode",
      "name",
      "number",
      "amount",
      "currency",
      "contract_state",
      "workflow_state",
      "supplier_name",
      "department_id",
      "project_type",
      "procurement_method",
      "signing_date",
      "source_system",
      "owner_org_id",
      "row_version",
      "created_at",
      "updated_at",
    ],
    [
      "contract_id",
      "authority_mode",
      "name",
      "number",
      "amount",
      "currency",
      "contract_state",
      "workflow_state",
      "owner_org_id",
      "row_version",
      "created_at",
      "updated_at",
    ],
  );
  const optional = (key: string) =>
    item[key] === undefined ? undefined : nullableText(item[key], `${path}.${key}`);
  return {
    contract_id: text(item.contract_id, `${path}.contract_id`),
    authority_mode: text(item.authority_mode, `${path}.authority_mode`),
    name: text(item.name, `${path}.name`),
    number: nullableText(item.number, `${path}.number`),
    amount: nullableText(item.amount, `${path}.amount`),
    currency: nullableText(item.currency, `${path}.currency`),
    contract_state: text(item.contract_state, `${path}.contract_state`),
    workflow_state: text(item.workflow_state, `${path}.workflow_state`),
    supplier_name: optional("supplier_name"),
    department_id: optional("department_id"),
    project_type: optional("project_type"),
    procurement_method: optional("procurement_method"),
    signing_date: optional("signing_date"),
    source_system: optional("source_system"),
    owner_org_id: text(item.owner_org_id, `${path}.owner_org_id`),
    row_version: integer(item.row_version, `${path}.row_version`, 1),
    created_at: text(item.created_at, `${path}.created_at`),
    updated_at: text(item.updated_at, `${path}.updated_at`),
  };
}

export function parseContractLedgerPage(value: unknown): ContractLedgerPage {
  const page = record(
    value,
    "contracts",
    ["items", "next_cursor", "has_more"],
    ["items", "next_cursor", "has_more"],
  );
  return {
    items: array(page.items, "contracts.items", parseLedgerItem),
    next_cursor: nullableText(page.next_cursor, "contracts.next_cursor"),
    has_more: bool(page.has_more, "contracts.has_more"),
  };
}

function parseContractSavedView(value: unknown, path: string): ContractSavedView {
  const view = record(
    value,
    path,
    ["view_id", "name", "filter", "columns", "row_version", "created_at", "updated_at"],
  );
  return {
    view_id: text(view.view_id, `${path}.view_id`),
    name: text(view.name, `${path}.name`),
    filter: parseLedgerFilter(view.filter, `${path}.filter`),
    columns: array(view.columns, `${path}.columns`, ledgerColumn),
    row_version: integer(view.row_version, `${path}.row_version`, 1),
    created_at: text(view.created_at, `${path}.created_at`),
    updated_at: text(view.updated_at, `${path}.updated_at`),
  };
}

function parseContractSavedViewList(value: unknown): ContractSavedView[] {
  const row = object(value, "contracts");
  return array(row.items, "contracts.items", parseContractSavedView);
}

export function parseContractExport(value: unknown): ContractExport {
  const item = record(value, "contract", [
    "export_id",
    "state",
    "format",
    "columns",
    "row_count",
    "created_at",
    "expires_at",
    "download_available",
    "replayed",
  ]);
  const state = text(item.state, "contract.state");
  const format = text(item.format, "contract.format");
  if (state !== "ready" || format !== "csv")
    throw new Error("合同导出响应的状态或格式无法识别");
  return {
    export_id: text(item.export_id, "contract.export_id"),
    state,
    format,
    columns: array(item.columns, "contract.columns", ledgerColumn),
    row_count: integer(item.row_count, "contract.row_count", 0),
    created_at: text(item.created_at, "contract.created_at"),
    expires_at: text(item.expires_at, "contract.expires_at"),
    download_available: bool(item.download_available, "contract.download_available"),
    replayed: bool(item.replayed, "contract.replayed"),
  };
}

export function contractLedgerQueryString(
  filter: ContractLedgerFilter,
  cursor: string | null = null,
  limit = 25,
): string {
  const params = new URLSearchParams();
  for (const key of LEDGER_FILTER_KEYS) {
    const value = filter[key];
    if (value !== undefined && value !== "") params.set(key, value);
  }
  if (cursor !== null && cursor !== "") params.set("cursor", cursor);
  params.set("limit", String(limit));
  return params.toString();
}

export function listContractLedger(
  filter: ContractLedgerFilter,
  cursor: string | null = null,
  limit = 25,
  signal?: AbortSignal,
): Promise<ContractLedgerPage> {
  return requestJson(
    `${CONTRACTS_PATH}?${contractLedgerQueryString(filter, cursor, limit)}`,
    { method: "GET", signal },
    parseContractLedgerPage,
  );
}

export function listContractSavedViews(
  signal?: AbortSignal,
): Promise<ContractSavedView[]> {
  return requestJson(
    `${CONTRACTS_PATH}/views`,
    { method: "GET", signal },
    parseContractSavedViewList,
  );
}

export function createContractSavedView(
  name: string,
  filter: ContractLedgerFilter,
  columns: ContractLedgerColumn[],
): Promise<ContractSavedView> {
  return requestJson(
    `${CONTRACTS_PATH}/views`,
    jsonInit("POST", { name, filter, columns }),
    (value) => parseContractSavedView(value, "contract"),
  );
}

export function replaceContractSavedView(
  viewId: string,
  expectedRowVersion: number,
  name: string,
  filter: ContractLedgerFilter,
  columns: ContractLedgerColumn[],
): Promise<ContractSavedView> {
  return requestJson(
    `${CONTRACTS_PATH}/views/${encodeURIComponent(viewId)}`,
    jsonInit("POST", {
      expected_row_version: expectedRowVersion,
      name,
      filter,
      columns,
    }),
    (value) => parseContractSavedView(value, "contract"),
  );
}

export function deleteContractSavedView(
  viewId: string,
  expectedRowVersion: number,
): Promise<void> {
  return requestJson(
    `${CONTRACTS_PATH}/views/${encodeURIComponent(viewId)}?expected_row_version=${expectedRowVersion}`,
    { method: "DELETE" },
    (value) => {
      const result = object(value, "contract");
      if (!bool(result.deleted, "contract.deleted"))
        throw new Error("合同视图删除响应无法识别");
    },
  );
}

export function createContractExport(
  filter: ContractLedgerFilter,
  columns: ContractLedgerColumn[],
  idempotencyKey: string,
): Promise<ContractExport> {
  return requestJson(
    `${CONTRACTS_PATH}/exports`,
    jsonInit(
      "POST",
      { filter, columns, format: "csv" },
      { "Idempotency-Key": idempotencyKey },
    ),
    parseContractExport,
  );
}

/** Downloads are byte streams, not JSON, so they bypass requestJson. */
export async function downloadContractExport(exportId: string): Promise<Blob> {
  let response: Response;
  try {
    response = await fetch(
      `${CONTRACTS_PATH}/exports/${encodeURIComponent(exportId)}/download`,
      {
        method: "GET",
        credentials: "include",
        cache: "no-store",
        headers: { Accept: "text/csv" },
      },
    );
  } catch (error) {
    if (error instanceof Error && error.name === "AbortError") throw error;
    throw new ApiError("无法连接审查服务", 0, "network", true, null);
  }
  if (!response.ok) throw await errorFromResponse(response);
  return response.blob();
}
