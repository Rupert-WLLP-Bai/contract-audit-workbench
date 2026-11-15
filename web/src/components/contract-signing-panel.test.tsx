// File metadata (manifest mirror):
// Purpose: Checks signing registration read-only facts, transcription comparison results, manual confirmation gates, idempotent registration and supplement draft creation with server-resolved parties and revisions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import {
  checkContractSigning,
  createContractSupplement,
  listContractSigningRegistrations,
  listContractSupplements,
  registerContractSigning,
  type ContractDetail,
  type ContractSigningCheckResult,
  type ContractSigningRegistration,
  type ContractSupplement,
  type ContractSupplementResult,
  type DraftContent,
} from "../api/contracts";
import { ApiError } from "../api/http";
import {
  clearPendingMutationKey,
  idempotencyActorScope,
  pendingMutationKey,
} from "../api/idempotency";
import { useCaseDetail, useCases } from "../api/queries";
import type { Actor } from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { ContractSigningPanel } from "./contract-signing-panel";

vi.mock("../api/contracts", async (original) => ({
  ...(await original<typeof import("../api/contracts")>()),
  checkContractSigning: vi.fn(),
  registerContractSigning: vi.fn(),
  listContractSigningRegistrations: vi.fn(),
  listContractSupplements: vi.fn(),
  createContractSupplement: vi.fn(),
}));

vi.mock("../api/queries", () => ({
  useCases: vi.fn(),
  useCaseDetail: vi.fn(),
}));

vi.mock("../api/idempotency", () => ({
  idempotencyActorScope: vi.fn(() => ({
    actorId: "clerk-1",
    tenantId: "tenant-1",
    orgId: "org-1",
    audience: "client",
  })),
  pendingMutationKey: vi.fn(),
  clearPendingMutationKey: vi.fn(),
}));

const actor: Actor = {
  id: "clerk-1",
  display_name: "经办人",
  role: "clerk",
  tenant_id: "tenant-1",
  org_id: "org-1",
  audience: "client",
  principal_id: null,
  csrf_token: "synthetic-csrf",
  context_switch_enabled: false,
  active_context: {
    context_version: "1",
    membership_id: null,
    tenant_id: "tenant-1",
    tenant_name: "本地租户",
    org_id: "org-1",
    org_name: "本地组织",
  },
};

const onRefresh = vi.fn(async () => {});
const onOpenDraft = vi.fn();

const caseSummary = {
  id: "case-1",
  title: "服务器采购",
  contract_type: "procurement_ict",
  document_count: 1,
  pending_decisions: 0,
  latest_run: null,
  updated_at: "2026-11-01T00:00:00Z",
  business_line: "ict",
  scenario: "procurement",
  business_status: "active",
  case_number: null,
  customer: null,
  supplier: null,
  owner_name: null,
};

const caseDetail = {
  id: "case-1",
  title: "服务器采购",
  contract_type: "procurement_ict",
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  documents: [
    {
      id: "doc-1",
      role: "contract",
      filename: "evidence.pdf",
      media_type: "application/pdf",
      digest: "d".repeat(64),
      size: 10,
      parse_status: "ready",
      parse_error: null,
      created_at: "2026-11-01T00:00:00Z",
      parsed_document: null,
    },
  ],
  material_versions: [
    {
      material_id: "material-1",
      document_id: "doc-1",
      version: 2,
      previous_document_id: null,
      created_at: "2026-11-01T00:00:00Z",
    },
  ],
  members: [],
  latest_run: null,
  finding_counts: null,
  activity: [],
  business_line: "ict",
  scenario: "procurement",
  business_status: "active",
  generations: [],
  remediations: [],
  reports: [],
  archives: [],
  archived_at: null,
};

const signedMaterial = {
  case_id: "case-1",
  case_org_id: "org-1",
  digest: "d".repeat(64),
  filename: "evidence.pdf",
  material_id: "material-1",
  material_version: 2,
  material_version_id: "doc-1",
  purpose: "signed_contract" as const,
};

const evidenceRef = {
  material_id: "material-1",
  material_version_id: "doc-1",
  sha256: "d".repeat(64),
  purpose: "signed_contract" as const,
};

const registration: ContractSigningRegistration = {
  approval_basis: "approved_round",
  approval_evidence_status: "approved_round",
  approval_round_id: "round-1",
  comparison_confirmed_at: "2026-11-04T00:00:00Z",
  comparison_confirmed_by: "clerk-1",
  comparison_method: "structured_signed_terms/v1",
  comparison_result: "human_transcription_confirmed",
  comparison_scope: "all_structured_business_fields_and_restricted_fields",
  contract_id: "contract-1",
  contract_revision_id: "revision-1",
  contract_state: "registered",
  parties: [
    { role: "buyer", name: "采购单位" },
    { role: "supplier", name: "供应商" },
  ],
  registered_at: "2026-11-04T01:00:00Z",
  registered_by: "clerk-1",
  registration_id: "registration-1",
  signature_evidence_status: "document_only",
  signed_material: signedMaterial,
  signing_date: "2026-11-03",
  verification_status: "human_transcription_confirmed",
  warnings: ["历史审批依据需人工复核"],
};

function checkResult(
  overrides: Partial<ContractSigningCheckResult> = {},
): ContractSigningCheckResult {
  return {
    approval_status: "approved",
    baseline_revision_id: "revision-1",
    can_register: true,
    comparison_method: "structured_signed_terms/v1",
    comparison_result: "transcription_matches_revision",
    contract_id: "contract-1",
    fields: [{ path: "contract.amount", change: "changed" }],
    restricted_fields: [
      { path: "contract.restricted.unknown", changed: null },
      { path: "contract.restricted.changed", changed: true },
      { path: "contract.restricted.same", changed: false },
    ],
    verification_status: "human_transcription_confirmed",
    warnings: ["签署材料摘要与冻结修订一致"],
    ...overrides,
  };
}

const childSupplement: ContractSupplement = {
  contract_state: "draft",
  linked_at: "2026-11-04T00:00:00Z",
  linked_by: "clerk-1",
  parent_contract_id: "contract-1",
  parent_revision_id: "revision-1",
  reason: "补充验收条款",
  relation_type: "supplementary_agreement",
  supplement_contract_id: "contract-child",
};

const parentSupplement: ContractSupplement = {
  contract_state: "performing",
  linked_at: "2026-11-03T00:00:00Z",
  linked_by: "clerk-1",
  parent_contract_id: "contract-parent",
  parent_revision_id: "revision-parent",
  reason: "变更交付计划",
  relation_type: "change_order",
  supplement_contract_id: "contract-1",
};

const draft: DraftContent = {
  contract: {},
  buyer: { purchasing_unit: { id: "unit-1", name: "采购单位" } },
  supplier: { name: "供应商" },
};

beforeEach(() => {
  vi.mocked(checkContractSigning).mockReset();
  vi.mocked(registerContractSigning).mockReset();
  vi.mocked(listContractSigningRegistrations).mockReset().mockResolvedValue([]);
  vi.mocked(listContractSupplements).mockReset().mockResolvedValue({ items: [], parent: null });
  vi.mocked(createContractSupplement).mockReset();
  vi.mocked(pendingMutationKey)
    .mockReset()
    .mockResolvedValue({ storageKey: "storage-1", key: "key-1" });
  vi.mocked(clearPendingMutationKey).mockReset();
  vi.mocked(idempotencyActorScope).mockClear();
  onRefresh.mockReset().mockResolvedValue(undefined);
  onOpenDraft.mockReset();
  vi.mocked(useCases).mockReturnValue({
    data: [caseSummary],
    isError: false,
    isSuccess: true,
    isLoading: false,
  } as never);
  vi.mocked(useCaseDetail).mockReturnValue({
    data: caseDetail,
    isError: false,
    isLoading: false,
  } as never);
});

function detail(overrides: Partial<ContractDetail> = {}): ContractDetail {
  return {
    summary: {
      contract_id: "contract-1",
      authority_mode: "native",
      name: "签署合同",
      number: "HT-1",
      amount: null,
      currency: null,
      contract_state: "registered",
      workflow_state: "completed",
      row_version: 3,
      created_at: "2026-11-01T00:00:00Z",
    },
    current_revision_id: "revision-1",
    active_round_id: null,
    allowed_commands: [],
    warnings: [],
    draft,
    ...overrides,
  };
}

function renderPanel(overrides: Partial<ContractDetail> = {}) {
  return render(
    <ContractSigningPanel
      detail={detail(overrides)}
      actor={actor}
      onRefresh={onRefresh}
      onOpenDraft={onOpenDraft}
    />,
  );
}

function selectByPlaceholder(placeholder: string): HTMLElement {
  const select = screen.getByText(placeholder).closest(".ant-select");
  if (!select) throw new Error(`select ${placeholder} was not rendered`);
  return within(select as HTMLElement).getByRole("combobox");
}

async function selectEvidence(user: ReturnType<typeof userEvent.setup>) {
  await chooseOption(user, selectByPlaceholder("选择有权读取的案件"), /服务器采购/);
  await chooseOption(user, selectByPlaceholder("选择已授权材料版本"), /evidence\.pdf/);
}

async function prepareCheck(user: ReturnType<typeof userEvent.setup>) {
  await selectEvidence(user);
  await user.click(screen.getByRole("checkbox"));
  const checkButton = screen.getByRole("button", { name: "核对转录与冻结修订" });
  await waitFor(() => expect(checkButton).toBeEnabled());
  await user.click(checkButton);
}

it("shows an existing signing registration as a read-only fact with the original link", async () => {
  vi.mocked(listContractSigningRegistrations).mockResolvedValue([registration]);
  renderPanel();

  expect(await screen.findByText("已有签署登记事实（只读）")).toBeVisible();
  expect(screen.getByText("签署日期：2026-11-03；登记修订：revision-1")).toBeVisible();
  expect(screen.getByText("签署主体：buyer：采购单位；supplier：供应商")).toBeVisible();
  expect(screen.getByRole("link", { name: "evidence.pdf" })).toHaveAttribute(
    "href",
    "/api/cases/case-1/documents/doc-1/original",
  );
  expect(screen.getByText(/人工核对：clerk-1 · 2026-11-04T00:00:00Z/)).toBeVisible();
  expect(screen.getByText(/电子签名\/印章验真状态：未验证/)).toBeVisible();
  expect(screen.getByText(/审批依据：approved_round/)).toBeVisible();
  expect(screen.getByText("历史审批依据需人工复核")).toBeVisible();
  expect(screen.queryByText("请人工对照原件核对结构化字段。")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "核对转录与冻结修订" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "从当前不可变修订创建补充协议草稿" })).toBeEnabled();
});

it("renders parent and child supplement relations and opens their drafts", async () => {
  const user = userEvent.setup();
  vi.mocked(listContractSupplements).mockResolvedValue({
    items: [childSupplement],
    parent: parentSupplement,
  });
  renderPanel();

  expect(await screen.findByText("补充协议关系")).toBeVisible();
  expect(screen.getByText("变更单")).toBeVisible();
  expect(screen.getByText("补充协议")).toBeVisible();
  expect(screen.getByText(/父合同 contract-parent · 父修订 revision-parent/)).toBeVisible();
  expect(screen.getByText(/子合同 contract-child · 父修订 revision-1/)).toBeVisible();
  await user.click(screen.getByRole("button", { name: "打开父合同" }));
  expect(onOpenDraft).toHaveBeenCalledWith("contract-parent");
  await user.click(screen.getByRole("button", { name: "打开草稿" }));
  expect(onOpenDraft).toHaveBeenCalledWith("contract-child");
});

it("requires valid transcription JSON and an explicit confirmation before checking", async () => {
  const user = userEvent.setup();
  vi.mocked(checkContractSigning).mockResolvedValue(checkResult());
  renderPanel();

  const transcription = screen.getByLabelText("签署原件结构化转录（人工逐项核对）");
  fireEvent.change(transcription, { target: { value: "[]" } });
  expect(screen.getByText("需要包含 contract 对象的合同结构化内容")).toBeVisible();
  expect(screen.getByRole("checkbox")).toBeDisabled();
  expect(screen.getByRole("button", { name: "核对转录与冻结修订" })).toBeDisabled();

  fireEvent.change(transcription, { target: { value: JSON.stringify(draft, null, 2) } });
  await selectEvidence(user);
  await user.click(screen.getByRole("checkbox"));
  await user.click(screen.getByRole("button", { name: "核对转录与冻结修订" }));

  expect(
    await screen.findByText(
      /审批历史：approved；结构化比较：transcription_matches_revision；登记许可：可登记/,
    ),
  ).toBeVisible();
  expect(screen.getByText(/人工转录确认状态：human_transcription_confirmed/)).toBeVisible();
  expect(screen.getByText("contract.amount：changed")).toBeVisible();
  expect(screen.getByText("contract.restricted.unknown：无法核验")).toBeVisible();
  expect(screen.getByText("contract.restricted.changed：有差异")).toBeVisible();
  expect(screen.getByText("contract.restricted.same：无差异")).toBeVisible();
  expect(screen.getByText("签署材料摘要与冻结修订一致")).toBeVisible();
  expect(screen.getByRole("button", { name: /追加签署登记/ })).toBeVisible();
  expect(checkContractSigning).toHaveBeenCalledWith(
    "contract-1",
    expect.objectContaining({
      baseline_revision_id: "revision-1",
      signed_evidence: evidenceRef,
      signed_content: draft,
      manual_comparison: {
        confirmed: true,
        signed_material_digest: "d".repeat(64),
        contract_revision_id: "revision-1",
        scope: "all_structured_business_fields_and_restricted_fields",
      },
    }),
  );
  expect(vi.mocked(checkContractSigning).mock.calls[0]![1]).not.toHaveProperty(
    "legacy_approval_basis",
  );
});

it("registers the signing with derived parties, the manual basis and a refresh", async () => {
  const user = userEvent.setup();
  vi.mocked(checkContractSigning).mockResolvedValue(checkResult());
  vi.mocked(registerContractSigning).mockResolvedValue(registration);
  renderPanel();

  await user.type(
    screen.getByLabelText("存量审批历史未知时的人工依据（仅限真实导入来源）"),
    "  经办人核对纸质审批单  ",
  );
  fireEvent.change(screen.getByLabelText("签署日期"), { target: { value: "2026-11-03" } });
  await prepareCheck(user);
  await screen.findByRole("button", { name: /追加签署登记/ });
  await user.click(screen.getByRole("button", { name: /追加签署登记/ }));

  expect(await screen.findByText("签署登记已追加；合同原文和既有审批事实未修改。")).toBeVisible();
  expect(pendingMutationKey).toHaveBeenCalledWith(
    "contract-signing-register",
    { actorId: "clerk-1", tenantId: "tenant-1", orgId: "org-1", audience: "client" },
    "contract-1",
    expect.objectContaining({
      expected_row_version: 3,
      baseline_revision_id: "revision-1",
      signing_date: "2026-11-03",
      signed_evidence: evidenceRef,
      legacy_approval_basis: "经办人核对纸质审批单",
      parties: [
        { role: "buyer", name: "采购单位" },
        { role: "supplier", name: "供应商" },
      ],
      signature_evidence_status: "document_only",
    }),
  );
  expect(registerContractSigning).toHaveBeenCalledWith(
    "contract-1",
    expect.objectContaining({ signing_date: "2026-11-03" }),
    "key-1",
  );
  expect(clearPendingMutationKey).toHaveBeenCalledWith("storage-1");
  expect(onRefresh).toHaveBeenCalledTimes(1);
  expect(await screen.findByText("已有签署登记事实（只读）")).toBeVisible();
  expect(checkContractSigning).toHaveBeenCalledWith(
    "contract-1",
    expect.objectContaining({ legacy_approval_basis: "经办人核对纸质审批单" }),
  );
});

it("refuses registration when the transcription carries no party names", async () => {
  const user = userEvent.setup();
  vi.mocked(checkContractSigning).mockResolvedValue(checkResult());
  renderPanel({ draft: { contract: {} } });

  fireEvent.change(screen.getByLabelText("签署日期"), { target: { value: "2026-11-03" } });
  await prepareCheck(user);
  await screen.findByRole("button", { name: /追加签署登记/ });
  await user.click(screen.getByRole("button", { name: /追加签署登记/ }));

  expect(await screen.findByText("签署内容中没有可登记的采购方或供应方主体名称")).toBeVisible();
  expect(registerContractSigning).not.toHaveBeenCalled();
});

it("surfaces load, case-material and comparison failures without stale results", async () => {
  vi.mocked(listContractSigningRegistrations).mockRejectedValue(
    new ApiError("签署事实读取失败", 500, "internal", false, null),
  );
  vi.mocked(useCaseDetail).mockReturnValue({
    data: undefined,
    isError: true,
    isLoading: false,
  } as never);
  const view = renderPanel();
  expect(await screen.findByText("签署事实读取失败")).toBeVisible();
  expect(
    screen.getByText("无法读取该案件材料；材料元数据已隐藏。请检查案件读取授权后重试。"),
  ).toBeVisible();
  expect(selectByPlaceholder("先选择案件")).toBeDisabled();

  vi.mocked(listContractSigningRegistrations).mockResolvedValue([]);
  vi.mocked(useCaseDetail).mockReturnValue({
    data: caseDetail,
    isError: false,
    isLoading: false,
  } as never);
  vi.mocked(checkContractSigning).mockRejectedValue(
    new ApiError("签署核对失败", 409, "conflict", false, null),
  );
  view.rerender(
    <ContractSigningPanel
      detail={detail({ summary: { ...detail().summary, contract_id: "contract-2" } })}
      actor={actor}
      onRefresh={onRefresh}
      onOpenDraft={onOpenDraft}
    />,
  );
  const user = userEvent.setup();
  await prepareCheck(user);
  expect(await screen.findByText("签署核对失败")).toBeVisible();
  expect(screen.queryByText(/登记许可：可登记/)).not.toBeInTheDocument();
});

it("creates a supplement draft from the frozen revision and opens it", async () => {
  const user = userEvent.setup();
  const supplementResult = {
    contract: {
      ...detail(),
      summary: { ...detail().summary, contract_id: "contract-new" },
    },
    supplement: { ...childSupplement, supplement_contract_id: "contract-new" },
  } as unknown as ContractSupplementResult;
  vi.mocked(createContractSupplement).mockResolvedValue(supplementResult);
  renderPanel();
  await screen.findByText("签署登记与补充协议");

  await user.click(screen.getByRole("button", { name: "从当前不可变修订创建补充协议草稿" }));
  const dialog = await screen.findByRole("dialog");
  await chooseOption(user, within(dialog).getByRole("combobox"), /变更单/);
  await user.type(within(dialog).getByLabelText("创建理由"), "补充验收条款");
  await user.click(within(dialog).getByRole("button", { name: "创建草稿" }));

  await waitFor(() => expect(createContractSupplement).toHaveBeenCalledTimes(1));
  expect(pendingMutationKey).toHaveBeenCalledWith(
    "contract-supplement-create",
    { actorId: "clerk-1", tenantId: "tenant-1", orgId: "org-1", audience: "client" },
    "contract-1",
    expect.objectContaining({
      expected_row_version: 3,
      parent_revision_id: "revision-1",
      relation_type: "change_order",
      reason: "补充验收条款",
      draft,
    }),
  );
  expect(createContractSupplement).toHaveBeenCalledWith(
    "contract-1",
    expect.objectContaining({ relation_type: "change_order", reason: "补充验收条款" }),
    "key-1",
  );
  expect(clearPendingMutationKey).toHaveBeenCalledWith("storage-1");
  expect(onRefresh).toHaveBeenCalledTimes(1);
  expect(onOpenDraft).toHaveBeenCalledWith("contract-new");
  expect(screen.getByText(/子合同 contract-new · 父修订 revision-1/)).toBeVisible();
});

it("rejects an invalid supplement draft before calling the server", async () => {
  const user = userEvent.setup();
  renderPanel();
  await screen.findByText("签署登记与补充协议");

  await user.click(screen.getByRole("button", { name: "从当前不可变修订创建补充协议草稿" }));
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("button", { name: "创建草稿" }));
  expect(await screen.findByText("请填写理由并提供包含 contract 的结构化草稿")).toBeVisible();

  fireEvent.change(within(dialog).getByLabelText("补充协议结构化草稿"), { target: { value: "{" } });
  await user.type(within(dialog).getByLabelText("创建理由"), "补充验收条款");
  await user.click(within(dialog).getByRole("button", { name: "创建草稿" }));
  expect(await screen.findByText("补充协议草稿 JSON 格式无效")).toBeVisible();
  expect(createContractSupplement).not.toHaveBeenCalled();
});
