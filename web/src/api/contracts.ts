import { jsonInit, requestJson } from "./http";
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

export interface PartyRef {
  party_id: string;
  role: string;
  name_snapshot: string;
}

export interface ProcurementRef {
  system: string;
  result_id: string;
  package_id: string;
  snapshot_ref: string;
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

const { object, record, text, optionalText, nullableText, integer, array } =
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
  const sha = text(row.sha256, `${path}.sha256`);
  if (!/^[a-f0-9]{64}$/.test(sha))
    throw new Error(`合同响应中的 ${path}.sha256 不是 SHA-256 摘要`);
  return {
    material_id: text(row.material_id, `${path}.material_id`),
    material_version_id: text(row.material_version_id, `${path}.material_version_id`),
    sha256: sha,
    purpose: text(row.purpose, `${path}.purpose`),
  };
}

function parseDraft(value: unknown, path: string): DraftContent {
  const row = object(value, path);
  if (row.contract === undefined || row.contract === null)
    throw new Error(`合同响应中的 ${path}.contract 缺失`);
  const draft = row as unknown as DraftContent;
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
