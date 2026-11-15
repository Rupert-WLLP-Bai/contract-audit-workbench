// File metadata (manifest mirror):
// Purpose: Checks fixed-asset plan rendering, historical and redacted handover facts, version alignment guards, and the authorized manual handover flow that never writes back to ERP.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import {
  createContractAssetHandover,
  listContractAcceptances,
  listContractAssets,
  listContractDeliveries,
  type ContractAcceptance,
  type ContractAssetHandover,
  type ContractAssetList,
  type ContractDelivery,
  type ContractDetail,
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
import { ContractAssetsPanel } from "./contract-assets-panel";

vi.mock("../api/contracts", async (original) => ({
  ...(await original<typeof import("../api/contracts")>()),
  listContractAssets: vi.fn(),
  listContractDeliveries: vi.fn(),
  listContractAcceptances: vi.fn(),
  createContractAssetHandover: vi.fn(),
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

beforeEach(() => {
  vi.mocked(listContractAssets).mockReset();
  vi.mocked(listContractDeliveries).mockReset();
  vi.mocked(listContractAcceptances).mockReset();
  vi.mocked(createContractAssetHandover).mockReset();
  vi.mocked(pendingMutationKey)
    .mockReset()
    .mockResolvedValue({ storageKey: "storage-1", key: "key-1" });
  vi.mocked(clearPendingMutationKey).mockReset();
  vi.mocked(idempotencyActorScope).mockClear();
  onRefresh.mockReset().mockResolvedValue(undefined);
  vi.mocked(useCases).mockReturnValue({
    data: [caseSummary],
    isError: false,
    isSuccess: true,
  } as never);
  vi.mocked(useCaseDetail).mockReturnValue({
    data: caseDetail,
    isError: false,
  } as never);
});

function detail(overrides: Partial<ContractDetail> = {}): ContractDetail {
  return {
    summary: {
      contract_id: "contract-1",
      authority_mode: "native",
      name: "资产合同",
      number: "HT-1",
      amount: null,
      currency: null,
      contract_state: "performing",
      workflow_state: "completed",
      row_version: 3,
      created_at: "2026-11-01T00:00:00Z",
    },
    current_revision_id: "revision-1",
    active_round_id: null,
    allowed_commands: [],
    warnings: [],
    ...overrides,
  };
}

function assetReceipt(
  id: string,
  eventType: "receipt" | "reversal",
): ContractAssetHandover["receipts"][number] {
  return {
    asset_code: `AST-${id}`,
    contract_id: "contract-1",
    event_type: eventType,
    handover_id: "handover-1",
    occurred_at: "2026-11-10T03:00:00Z",
    payload_digest: "a".repeat(64),
    quantity: "1.000000",
    receipt_id: `receipt-${id}`,
    recorded_at: "2026-11-10T04:00:00Z",
    source_event_id: `event-${id}`,
    source_id: "erp",
    source_key_id: `key-${id}`,
    unit: "台",
  };
}

function handover(overrides: Partial<ContractAssetHandover> = {}): ContractAssetHandover {
  return {
    acceptance_id: "acceptance-1",
    asset_plan_id: "asset-1",
    assignment_verification: "verified",
    booking_status: "partial_booking",
    contract_id: "contract-1",
    contract_revision_id: "revision-1",
    contract_row_version: 3,
    custodian_directory_checked_at: "2026-11-09T00:00:00Z",
    custodian_directory_source: "directory",
    custodian_display_name: "张三",
    custodian_org_id: "org-9",
    custodian_user_id: "user-9",
    custodian_verification: "verified",
    delivery_id: "delivery-1",
    department_verification: "verified",
    evidence: [
      {
        case_id: "case-1",
        case_org_id: "org-1",
        digest: "d".repeat(64),
        filename: "evidence.pdf",
        material_id: "material-1",
        material_version: 2,
        material_version_id: "doc-1",
        purpose: "supporting",
      },
    ],
    handed_over_at: "2026-11-10T02:00:00Z",
    handover_id: "handover-1",
    item_id: "item-1",
    location: "一号仓库",
    quantity: "1.000000",
    receipts: [assetReceipt("1", "receipt")],
    recorded_at: "2026-11-10T02:30:00Z",
    recorded_by: "clerk-1",
    scope_end: "1",
    scope_start: "0",
    transfer_note: "现场交接",
    unit: "台",
    ...overrides,
  };
}

function assetPlan(
  overrides: Partial<ContractAssetList["items"][number]> = {},
): ContractAssetList["items"][number] {
  return {
    asset_plan_id: "asset-1",
    contract_revision_id: "revision-1",
    custodian_user_id: "user-9",
    department_code: "D-01",
    department_name: "信息中心",
    department_verification: "verified",
    fixed_asset_status: "true",
    handovers: [handover()],
    item_id: "item-1",
    name: "服务器",
    plan_status: "known",
    planned_quantity: "2.000000",
    unit: "台",
    ...overrides,
  };
}

function assetList(overrides: Partial<ContractAssetList> = {}): ContractAssetList {
  return {
    contract_id: "contract-1",
    fixed_asset_status: "true",
    items: [assetPlan()],
    row_version: 3,
    ...overrides,
  };
}

const delivery: ContractDelivery = {
  actual_at: "2026-11-05T00:00:00Z",
  batch_reference: "B-1",
  contract_id: "contract-1",
  contract_revision_id: "revision-1",
  delivery_id: "delivery-1",
  description: "首批交付",
  evidence: [],
  item_id: "item-1",
  item_name: "服务器",
  location: "一号仓库",
  quantity: "2.000000",
  recorded_at: "2026-11-05T01:00:00Z",
  recorded_by: "clerk-1",
  unit: "台",
};

const acceptance: ContractAcceptance = {
  acceptance_id: "acceptance-1",
  accepted_quantity: "1.000000",
  actual_at: "2026-11-06T00:00:00Z",
  contract_id: "contract-1",
  contract_revision_id: "revision-1",
  defects: [],
  delivery_id: "delivery-1",
  evidence: [],
  failed_quantity: "0.000000",
  inspector_note: "现场验收",
  item_id: "item-1",
  item_name: "服务器",
  quantity: "1.000000",
  recorded_at: "2026-11-06T01:00:00Z",
  recorded_by: "clerk-1",
  rectification_ids: [],
  rectifications: [],
  result: "passed",
  scope_end: "1.000000",
  scope_start: "0",
  standard: "符合合同约定",
  unit: "台",
};

function resolveReads(assets: ContractAssetList = assetList(), rowVersion = 3) {
  vi.mocked(listContractAssets).mockResolvedValue({ ...assets, row_version: assets.row_version });
  vi.mocked(listContractDeliveries).mockResolvedValue({
    items: [delivery, { ...delivery, delivery_id: "delivery-other", item_id: "item-2" }],
    row_version: rowVersion,
    summary: [],
  });
  vi.mocked(listContractAcceptances).mockResolvedValue({
    items: [
      acceptance,
      { ...acceptance, acceptance_id: "acceptance-failed", result: "failed" },
      { ...acceptance, acceptance_id: "acceptance-other", item_id: "item-2" },
    ],
    row_version: rowVersion,
    summary: [],
  });
}

function renderPanel(canManage = false, overrides: Partial<ContractDetail> = {}) {
  return render(
    <ContractAssetsPanel
      detail={detail(overrides)}
      actor={actor}
      canManage={canManage}
      onRefresh={onRefresh}
    />,
  );
}

function selectByPlaceholder(placeholder: string): HTMLElement {
  const select = screen.getByText(placeholder).closest(".ant-select");
  if (!select) throw new Error(`select ${placeholder} was not rendered`);
  return within(select as HTMLElement).getByRole("combobox");
}

async function fillHandoverForm(user: ReturnType<typeof userEvent.setup>) {
  await chooseOption(user, selectByPlaceholder("选择当前修订的已核对资产计划"), /服务器/);
  await chooseOption(user, selectByPlaceholder("选择同一修订和商品的实际交付事实"), /B-1/);
  await chooseOption(user, selectByPlaceholder("选择该交付的验收事实"), /已通过/);
  await chooseOption(user, selectByPlaceholder("选择有权读取的案件证据"), /服务器采购/);
  await chooseOption(user, selectByPlaceholder("选择证据材料版本"), /evidence\.pdf/);
  await user.type(screen.getByLabelText("移交范围起点"), "0");
  await user.type(screen.getByLabelText("移交范围终点"), "1");
  await user.type(screen.getByLabelText("资产移交地点"), "一号仓库");
  await user.type(screen.getByLabelText("资产移交说明"), "现场交接");
}

async function submitHandover(user: ReturnType<typeof userEvent.setup>) {
  const submit = screen.getByRole("button", { name: "追加人工移交事实" });
  await waitFor(() => expect(submit).toBeEnabled());
  await user.click(submit);
}

it("renders current and historical fixed-asset facts without granting write controls", async () => {
  resolveReads(
    assetList({
      items: [
        assetPlan({
          handovers: [
            handover(),
            handover({
              handover_id: "handover-2",
              custodian_verification: "unknown",
              booking_status: "booked",
              evidence_redacted: true,
              receipts: [assetReceipt("2", "reversal")],
            }),
            handover({
              handover_id: "handover-3",
              custodian_verification: "unknown",
              booking_status: "unknown",
              evidence: [],
              receipts: [],
            }),
          ],
        }),
        assetPlan({
          asset_plan_id: "asset-history",
          contract_revision_id: "revision-0",
          fixed_asset_status: "unknown",
          name: "",
          plan_status: "unknown",
          handovers: [
            handover({
              handover_id: "handover-history",
              contract_revision_id: "revision-0",
              booking_status: "pending_booking",
              evidence: [],
            }),
          ],
        }),
      ],
    }),
  );
  renderPanel(false);

  expect(await screen.findByText("冻结修订标记为固定资产")).toBeVisible();
  expect(screen.getByText(/计划数量：2\.000000 台 · 计划状态：已核对/)).toBeVisible();
  expect(screen.getByText(/部门计划值：信息中心（D-01） · 部门主数据核验：已核验/)).toBeVisible();
  expect(screen.getByText("计划保管人 ID：user-9 · 这不证明部门归属或移交授权。")).toBeVisible();
  expect(screen.getAllByText(/范围 \[0, 1\) ·/)[0]).toBeVisible();
  expect(screen.getAllByText("现场交接")[0]).toBeVisible();
  expect(
    screen.getByText("部门归属核验：已核验；目录用户核验：目录身份已核验；整体分配核验：已核验。"),
  ).toBeVisible();
  expect(screen.getByText(/目录中的当时用户：张三 · 组织 ID org-9 · 来源 directory/)).toBeVisible();
  expect(screen.getByText("ERP 回执记录部分入账")).toBeVisible();
  expect(screen.getByText("ERP 回执记录已入账")).toBeVisible();
  expect(screen.getByText("ERP 入账状态未知")).toBeVisible();
  expect(screen.getByText("ERP 入账回执")).toBeVisible();
  expect(screen.getByText("ERP 冲正回执")).toBeVisible();
  expect(screen.getByText(/资产代码 AST-1/)).toBeVisible();
  expect(screen.getByText("部分案件材料摘要因当前授权范围已隐藏。")).toBeVisible();
  expect(screen.getByText("没有可见的移交证据摘要。")).toBeVisible();
  expect(screen.getByText("还没有 ERP 入账回执。")).toBeVisible();
  expect(screen.getByText(/evidence\.pdf · 版本 2 · SHA-256/)).toBeVisible();
  expect(screen.getByText("历史冻结修订资产计划与记录")).toBeVisible();
  expect(screen.getByText("未命名资产计划 · 历史修订")).toBeInTheDocument();
  expect(screen.getByText("尚无 ERP 入账回执")).toBeInTheDocument();
  expect(
    screen.getByText("当前身份没有可用于写入人工移交事实的授权，仍可查看已授权合同范围内的事实。"),
  ).toBeVisible();
  expect(screen.queryByRole("button", { name: "追加人工移交事实" })).not.toBeInTheDocument();
});

it("labels unknown and not-applicable fixed-asset revisions and blocks missing prerequisites", async () => {
  vi.mocked(listContractAssets).mockResolvedValue(
    assetList({ fixed_asset_status: "not_applicable", items: [] }),
  );
  vi.mocked(listContractDeliveries).mockResolvedValue({ items: [], row_version: 3, summary: [] });
  vi.mocked(listContractAcceptances).mockResolvedValue({ items: [], row_version: 3, summary: [] });
  const view = renderPanel(true);
  expect(await screen.findByText("该冻结修订明确标记为非固定资产，不适用资产移交。")).toBeVisible();
  expect(screen.getByText("当前冻结修订没有可用的资产计划记录。")).toBeVisible();
  expect(screen.queryByRole("button", { name: "追加人工移交事实" })).not.toBeInTheDocument();

  vi.mocked(listContractAssets).mockResolvedValue(assetList({ fixed_asset_status: "unknown" }));
  view.rerender(
    <ContractAssetsPanel
      detail={detail({ current_revision_id: "revision-2" })}
      actor={actor}
      canManage
      onRefresh={onRefresh}
    />,
  );
  expect(
    await screen.findByText("固定资产属性未知；不能按非固定资产处理，也不能登记移交。"),
  ).toBeVisible();
  expect(screen.getByText("固定资产属性未知")).toBeVisible();

  const callsBeforeMissingRevision = vi.mocked(listContractAssets).mock.calls.length;
  view.rerender(
    <ContractAssetsPanel
      detail={detail({ current_revision_id: null })}
      actor={actor}
      canManage
      onRefresh={onRefresh}
    />,
  );
  expect(screen.getByText("当前没有合同冻结修订，资产计划不可用。")).toBeVisible();
  await waitFor(() =>
    expect(vi.mocked(listContractAssets).mock.calls).toHaveLength(callsBeforeMissingRevision),
  );
});

it("keeps facts read-only when the contract detail version is stale", async () => {
  resolveReads();
  renderPanel(true, { summary: { ...detail().summary, row_version: 4 } });
  expect(
    await screen.findByText("资产或履约事实版本与合同详情不同步，暂不允许提交。请刷新合同。"),
  ).toBeVisible();
  expect(screen.getByText("冻结修订标记为固定资产")).toBeVisible();
  expect(screen.getByRole("button", { name: "追加人工移交事实" })).toBeDisabled();
});

it("surfaces receipt read failures and mismatched projection versions", async () => {
  resolveReads();
  vi.mocked(listContractAssets).mockRejectedValue(
    new ApiError("资产事实读取失败", 500, "internal", false, null),
  );
  const view = renderPanel(true);
  expect(await screen.findByText("资产事实读取失败")).toBeVisible();
  expect(screen.queryByText("冻结修订标记为固定资产")).not.toBeInTheDocument();

  vi.mocked(listContractAssets).mockResolvedValue(assetList({ row_version: 2 }));
  view.rerender(
    <ContractAssetsPanel
      detail={detail({ current_revision_id: "revision-next" })}
      actor={actor}
      canManage
      onRefresh={onRefresh}
    />,
  );
  expect(await screen.findByText("请求没有完成")).toBeVisible();
  expect(screen.queryByText("冻结修订标记为固定资产")).not.toBeInTheDocument();
});

it("registers a manual handover with idempotency and refreshes the contract", async () => {
  const user = userEvent.setup();
  resolveReads();
  vi.mocked(createContractAssetHandover).mockResolvedValue(
    handover({ handover_id: "handover-new", contract_row_version: 4, quantity: "1.000000" }),
  );
  renderPanel(true);
  expect(await screen.findByText("冻结修订标记为固定资产")).toBeVisible();

  await fillHandoverForm(user);
  await submitHandover(user);

  expect(
    await screen.findByText("人工移交事实已登记；ERP 入账回执由可信来源单独记录。"),
  ).toBeVisible();
  expect(pendingMutationKey).toHaveBeenCalledWith(
    "clm-asset-handover-create",
    { actorId: "clerk-1", tenantId: "tenant-1", orgId: "org-1", audience: "client" },
    "contract-1:revision-1:asset-1:delivery-1:acceptance-1",
    expect.objectContaining({
      expected_row_version: 3,
      contract_revision_id: "revision-1",
      asset_plan_id: "asset-1",
      delivery_id: "delivery-1",
      acceptance_id: "acceptance-1",
      scope_start: "0",
      scope_end: "1",
      location: "一号仓库",
      transfer_note: "现场交接",
      evidence: [
        {
          material_id: "material-1",
          material_version_id: "doc-1",
          sha256: "d".repeat(64),
          purpose: "supporting",
        },
      ],
    }),
  );
  expect(createContractAssetHandover).toHaveBeenCalledWith(
    "contract-1",
    expect.objectContaining({
      handed_over_at: expect.stringMatching(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:00\.000Z$/),
    }),
    "key-1",
  );
  expect(clearPendingMutationKey).toHaveBeenCalledWith("storage-1");
  expect(onRefresh).toHaveBeenCalledTimes(1);
});

it("keeps the safety warning when the browser cannot clear the retry marker", async () => {
  const user = userEvent.setup();
  resolveReads();
  vi.mocked(createContractAssetHandover).mockResolvedValue(
    handover({ handover_id: "handover-new", contract_row_version: 4 }),
  );
  vi.mocked(clearPendingMutationKey).mockImplementationOnce(() => {
    throw new Error("storage unavailable");
  });
  renderPanel(true);
  await screen.findByText("冻结修订标记为固定资产");

  await fillHandoverForm(user);
  await submitHandover(user);

  expect(
    await screen.findByText("人工移交已登记；浏览器未能清理安全重试标识，请勿重复提交。"),
  ).toBeVisible();
  await waitFor(() => expect(onRefresh).toHaveBeenCalledTimes(1));
});

it("keeps the registered handover visible when the contract refresh fails", async () => {
  const user = userEvent.setup();
  resolveReads();
  vi.mocked(createContractAssetHandover).mockResolvedValue(
    handover({ handover_id: "handover-new", contract_row_version: 4 }),
  );
  onRefresh.mockRejectedValue(new ApiError("刷新失败", 500, "internal", false, null));
  renderPanel(true);
  await screen.findByText("冻结修订标记为固定资产");

  await fillHandoverForm(user);
  await submitHandover(user);

  expect(await screen.findByText("人工移交已登记，但合同版本刷新失败：刷新失败")).toBeVisible();
  expect(clearPendingMutationKey).toHaveBeenCalledWith("storage-1");
  expect(onRefresh).toHaveBeenCalledTimes(1);
});
