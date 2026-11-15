// File metadata (manifest mirror):
// Purpose: Checks tenant-lifecycle operation listing, controlled creation with idempotency, step receipt validation, reconciliation, stale-context recovery, pagination and terminal states.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { Modal } from "antd";
import type { PropsWithChildren } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import type {
  TenantProvisionOperation,
  TenantProvisionOperationsView,
} from "../api/tenant-provisioning";
import type { Actor } from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { TenantLifecyclePage } from "./tenant-lifecycle-page";

const mocks = vi.hoisted(() => ({
  useSession: vi.fn(),
  useTenantProvisionOperations: vi.fn(),
  useTenantProvisionOperation: vi.fn(),
  createTenantProvisionOperation: vi.fn(),
  recordTenantProvisionStep: vi.fn(),
  pendingMutationKey: vi.fn(),
  clearPendingMutationKey: vi.fn(),
  idempotencyActorScope: vi.fn(),
  revalidateSessionContext: vi.fn(),
  clearIdentityQueries: vi.fn(),
  sessionFor: vi.fn((audience: string) => ["session", audience]),
  captureCommandContext: vi.fn(),
  pageRefetch: vi.fn(),
  detailRefetch: vi.fn(),
}));

vi.mock("../api/tenant-provisioning", async (original) => ({
  ...(await original<typeof import("../api/tenant-provisioning")>()),
  createTenantProvisionOperation: mocks.createTenantProvisionOperation,
  recordTenantProvisionStep: mocks.recordTenantProvisionStep,
  useTenantProvisionOperation: mocks.useTenantProvisionOperation,
  useTenantProvisionOperations: mocks.useTenantProvisionOperations,
}));
vi.mock("../api/queries", () => ({
  useSession: mocks.useSession,
  queryKeys: { sessionFor: mocks.sessionFor },
  revalidateSessionContext: mocks.revalidateSessionContext,
  clearIdentityQueries: mocks.clearIdentityQueries,
}));
vi.mock("../api/idempotency", () => ({
  idempotencyActorScope: mocks.idempotencyActorScope,
  pendingMutationKey: mocks.pendingMutationKey,
  clearPendingMutationKey: mocks.clearPendingMutationKey,
}));
vi.mock("../api/http", async (original) => ({
  ...(await original<typeof import("../api/http")>()),
  captureCommandContext: mocks.captureCommandContext,
}));

const actor: Actor = {
  id: "admin-1",
  display_name: "平台管理员",
  role: "operator",
  tenant_id: "tenant-1",
  org_id: "org-1",
  audience: "admin",
  principal_id: "principal-1",
  csrf_token: "synthetic-csrf",
  context_switch_enabled: false,
  active_context: {
    context_version: "7",
    membership_id: null,
    tenant_id: "tenant-1",
    tenant_name: "平台租户",
    org_id: "org-1",
    org_name: "平台组织",
  },
};
const actorScope = {
  actorId: "admin-1",
  tenantId: "tenant-1",
  orgId: "org-1",
  audience: "admin",
  contextVersion: "7",
  sessionIdentity:
    "admin-1\u0000tenant-1\u0000org-1\u0000principal-1\u0000operator\u0000admin\u0000synthetic-csrf\u0000token",
};
const commandContext = {
  contextVersion: "7",
  csrfToken: "csrf-1",
  identity: "admin-1",
  epoch: 3,
};
const receiptHash = "b".repeat(64);
const submittedHash = "c".repeat(64);

function operation(overrides: Partial<TenantProvisionOperation> = {}): TenantProvisionOperation {
  return {
    operation_id: "op-1",
    tenant_id: "tenant-1",
    kind: "provision",
    status: "in_progress",
    current_step: "identity",
    payload_sha256: "a".repeat(64),
    revision: "3",
    reconciliation_required: false,
    steps: [
      { name: "identity", state: "pending", receipt_sha256: null, updated_at: null },
      {
        name: "tenant_config",
        state: "complete",
        receipt_sha256: receiptHash,
        updated_at: "2026-11-15T00:00:00Z",
      },
    ],
    created_at: "2026-11-15T00:00:00Z",
    updated_at: "2026-11-15T00:00:00Z",
    ...overrides,
  };
}

function operationsView(
  operations: TenantProvisionOperation[],
  next: string | null = null,
): TenantProvisionOperationsView {
  return {
    schema_version: "tenant-provision-operations/v1",
    operations,
    next_cursor: next,
  };
}

function sessionState(overrides: Record<string, unknown> = {}) {
  return {
    data: actor,
    isLoading: false,
    isPending: false,
    isSuccess: true,
    isError: false,
    error: null,
    ...overrides,
  };
}

function pageState(data: TenantProvisionOperationsView | undefined) {
  return {
    data,
    isLoading: false,
    isError: false,
    error: null,
    refetch: mocks.pageRefetch,
  };
}

function detailState(data: TenantProvisionOperation | undefined) {
  return {
    data,
    isLoading: false,
    isError: false,
    error: null,
    refetch: mocks.detailRefetch,
  };
}

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Provider({ children }: PropsWithChildren) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  }
  const view = render(<TenantLifecyclePage />, { wrapper: Provider });
  return { queryClient, ...view };
}

/** Find the newest confirmation modal carrying the given title. */
async function openConfirm(title: string): Promise<HTMLElement> {
  return await waitFor(() => {
    const nodes = screen.queryAllByText(title);
    const modal = nodes[nodes.length - 1]?.closest(".ant-modal");
    if (!modal) throw new Error(`confirm dialog ${title} was not rendered`);
    return modal as HTMLElement;
  });
}

async function confirm(user: UserEvent, title: string, okText = "确认记录") {
  const dialog = await openConfirm(title);
  await user.click(within(dialog).getByRole("button", { name: okText }));
  await removeDialog(dialog);
}

/** Drop a confirmation portal so the page is reachable again while its leave animation stalls. */
async function removeDialog(dialog: HTMLElement) {
  await waitFor(() => {
    const root = dialog.closest(".ant-modal-root");
    if (root) root.remove();
  });
}

async function selectOperation(user: UserEvent) {
  await user.click(screen.getByRole("button", { name: "查看回执" }));
  await screen.findByRole("heading", { level: 2, name: "操作详情" });
}

beforeEach(() => {
  Modal.destroyAll();
  document.querySelectorAll(".ant-modal-root").forEach((node) => node.remove());
  mocks.useSession.mockReset().mockReturnValue(sessionState());
  mocks.useTenantProvisionOperations
    .mockReset()
    .mockReturnValue(pageState(operationsView([operation()])));
  mocks.useTenantProvisionOperation.mockReset().mockReturnValue(detailState(undefined));
  mocks.createTenantProvisionOperation.mockReset();
  mocks.recordTenantProvisionStep.mockReset();
  mocks.pendingMutationKey.mockReset().mockResolvedValue({ storageKey: "storage-1", key: "key-1" });
  mocks.clearPendingMutationKey.mockReset();
  mocks.idempotencyActorScope.mockReset().mockReturnValue(actorScope);
  mocks.revalidateSessionContext.mockReset().mockResolvedValue(undefined);
  mocks.clearIdentityQueries.mockReset();
  mocks.sessionFor.mockReset().mockImplementation((audience: string) => ["session", audience]);
  mocks.captureCommandContext.mockReset().mockReturnValue(commandContext);
  mocks.pageRefetch.mockReset();
  mocks.detailRefetch.mockReset().mockResolvedValue(undefined);
});

describe("TenantLifecyclePage", () => {
  it("renders listed operations and the selected step receipts", async () => {
    const user = userEvent.setup();
    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(id === "op-1" ? operation() : undefined),
    );
    renderPage();

    expect(screen.getByRole("heading", { level: 1, name: "租户生命周期管理" })).toBeVisible();
    expect(screen.getByText(/未知结果不得盲目重试/)).toBeVisible();
    const summary = screen.getByLabelText("操作记录分页摘要");
    expect(within(summary).getByText("1")).toBeVisible();
    expect(within(summary).getByText("无")).toBeVisible();
    const listed = within(screen.getByRole("table"));
    expect(listed.getByText("tenant-1")).toBeVisible();
    expect(listed.getByText("op-1")).toBeVisible();
    expect(listed.getByText("开通")).toBeVisible();
    expect(listed.getByText("身份绑定")).toBeVisible();
    expect(listed.getByText("进行中")).toBeVisible();
    expect(screen.getByText("选择一条操作查看步骤回执和当前 revision。")).toBeVisible();

    await selectOperation(user);

    expect(mocks.useTenantProvisionOperation).toHaveBeenCalledWith("op-1", true);
    expect(screen.getByText("操作 ID")).toBeVisible();
    expect(screen.getByText("payload SHA-256")).toBeVisible();
    expect(screen.getByText("a".repeat(64))).toBeVisible();
    expect(screen.getByRole("heading", { name: "记录当前步骤：身份绑定" })).toBeVisible();
    expect(screen.getByText("已记录回执")).toBeVisible();
    expect(screen.getByText("未记录")).toBeVisible();
    expect(screen.getByText(receiptHash)).toBeVisible();
    expect(screen.getByRole("button", { name: "确认步骤已完成" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "记录外部结果未知" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "记录步骤失败" })).toBeEnabled();
  });

  it("registers a provision operation after confirmation with an idempotency key", async () => {
    const user = userEvent.setup();
    const created = operation({ operation_id: "op-new", tenant_id: "tenant-9" });
    mocks.createTenantProvisionOperation.mockResolvedValue({
      schema_version: "tenant-provision-operation/v1",
      operation: created,
    });
    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(id === "op-new" ? created : undefined),
    );
    renderPage();

    await user.type(screen.getByLabelText("租户标识"), "tenant-9");
    await user.type(screen.getByLabelText("租户名称"), "北区租户");
    await user.type(screen.getByLabelText("辖区标识"), "cn-north");
    await user.type(screen.getByLabelText("已有管理员主体标识"), "principal-9");
    await user.type(screen.getByLabelText("管理员组织单元标识"), "org-9");
    await user.type(screen.getByLabelText("管理员组织单元名称"), "北区组织");
    await user.type(screen.getByLabelText("已发布租户配置版本"), "config-3");
    await user.click(screen.getByRole("button", { name: "登记开通操作" }));

    const dialog = await openConfirm("确认登记开通操作？");
    expect(
      within(dialog).getByText(
        /将为租户 tenant-9 登记开通操作。管理员主体与租户配置由服务端重新验证；不会创建密码或身份凭据。/,
      ),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "确认登记" }));
    await removeDialog(dialog);

    await waitFor(() => expect(mocks.createTenantProvisionOperation).toHaveBeenCalledTimes(1));
    expect(mocks.captureCommandContext).toHaveBeenCalledWith(actor);
    expect(mocks.idempotencyActorScope).toHaveBeenCalledWith(actor);
    expect(mocks.pendingMutationKey).toHaveBeenCalledWith(
      "tenant-provision-operation",
      actorScope,
      "tenant-9",
      expect.objectContaining({
        kind: "provision",
        tenant_id: "tenant-9",
        tenant_name: "北区租户",
        jurisdiction_id: "cn-north",
        admin_principal_id: "principal-9",
        admin_org_unit_id: "org-9",
        admin_org_unit_name: "北区组织",
        tenant_config_version: "config-3",
      }),
      commandContext,
    );
    expect(mocks.createTenantProvisionOperation).toHaveBeenCalledWith(
      expect.objectContaining({
        idempotency_key: "key-1",
        kind: "provision",
        tenant_id: "tenant-9",
        tenant_name: "北区租户",
      }),
      commandContext,
    );
    expect(await screen.findByText("已登记开通操作 op-new。")).toBeVisible();
    await waitFor(() => expect(mocks.clearPendingMutationKey).toHaveBeenCalledWith("storage-1"));
    expect(await screen.findByRole("heading", { level: 2, name: "操作详情" })).toBeVisible();
    expect(screen.getByText("op-new")).toBeVisible();
  });

  it("registers a deactivation with cleared setup fields and keeps the history warning", async () => {
    const user = userEvent.setup();
    mocks.createTenantProvisionOperation.mockResolvedValue({
      schema_version: "tenant-provision-operation/v1",
      operation: operation({ operation_id: "op-off", tenant_id: "tenant-2", kind: "deactivate" }),
    });
    renderPage();

    await chooseOption(user, screen.getByRole("combobox"), "停用");
    expect(screen.getByRole("button", { name: "登记停用操作" })).toBeEnabled();
    await user.type(screen.getByLabelText("租户标识"), "tenant-2");
    await user.click(screen.getByRole("button", { name: "登记停用操作" }));

    const dialog = await openConfirm("确认登记停用操作？");
    expect(
      within(dialog).getByText(
        "将为租户 tenant-2 登记停用并进入在途用量核对。操作不会删除历史记录。",
      ),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "确认登记" }));
    await removeDialog(dialog);

    await waitFor(() => expect(mocks.createTenantProvisionOperation).toHaveBeenCalledTimes(1));
    expect(mocks.pendingMutationKey).toHaveBeenCalledWith(
      "tenant-provision-operation",
      actorScope,
      "tenant-2",
      expect.objectContaining({
        kind: "deactivate",
        tenant_id: "tenant-2",
        tenant_name: "",
        jurisdiction_id: "",
        admin_principal_id: "",
        admin_org_unit_id: "",
        admin_org_unit_name: "",
        tenant_config_version: "",
      }),
      commandContext,
    );
    expect(mocks.createTenantProvisionOperation).toHaveBeenCalledWith(
      expect.objectContaining({
        idempotency_key: "key-1",
        kind: "deactivate",
        tenant_id: "tenant-2",
        tenant_name: "",
        tenant_config_version: "",
      }),
      commandContext,
    );
    expect(await screen.findByText("已登记停用操作 op-off。")).toBeVisible();
  });

  it("renders loading, unauthenticated, failed and missing admin sessions", async () => {
    mocks.useSession.mockReturnValue(
      sessionState({ data: undefined, isLoading: true, isPending: true, isSuccess: false }),
    );
    const view = renderPage();
    expect(screen.getByRole("heading", { level: 1, name: "正在确认平台操作权限" })).toBeVisible();

    mocks.useSession.mockReturnValue(
      sessionState({
        data: undefined,
        isSuccess: false,
        isError: true,
        error: new ApiError("登录已过期", 401, "unauthenticated", false, null),
      }),
    );
    view.rerender(<TenantLifecyclePage />);
    expect(screen.getByRole("heading", { name: "无法确认管理身份" })).toBeVisible();
    expect(screen.getByText("登录已过期")).toBeVisible();

    mocks.useSession.mockReturnValue(
      sessionState({
        data: undefined,
        isSuccess: false,
        isError: true,
        error: new ApiError("会话服务不可用", 503, "unavailable", true, null),
      }),
    );
    view.rerender(<TenantLifecyclePage />);
    expect(screen.getByText("会话服务不可用")).toBeVisible();

    mocks.useSession.mockReturnValue(sessionState({ data: null, isSuccess: false }));
    view.rerender(<TenantLifecyclePage />);
    expect(screen.getByText("请求没有完成")).toBeVisible();
  });

  it("shows operation loading, forbidden and retryable page failures", async () => {
    const user = userEvent.setup();
    mocks.useTenantProvisionOperations.mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
      error: null,
      refetch: mocks.pageRefetch,
    });
    const view = renderPage();
    expect(screen.getByRole("heading", { name: "正在读取租户生命周期操作" })).toBeVisible();

    mocks.useTenantProvisionOperations.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError("无权限", 403, "forbidden", false, null),
      refetch: mocks.pageRefetch,
    });
    view.rerender(<TenantLifecyclePage />);
    expect(screen.getByRole("heading", { name: "没有平台租户生命周期权限" })).toBeVisible();
    expect(
      screen.getByText(/平台授权由服务端根据当前 actor 与数据库 principal 双重判定/),
    ).toBeVisible();

    mocks.useTenantProvisionOperations.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError("数据库不可用", 503, "unavailable", true, null),
      refetch: mocks.pageRefetch,
    });
    view.rerender(<TenantLifecyclePage />);
    expect(screen.getByRole("heading", { name: "租户生命周期操作不可用" })).toBeVisible();
    expect(screen.getByText("数据库不可用")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(mocks.pageRefetch).toHaveBeenCalledTimes(1);
  });

  it("moves through operation cursors and refreshes the current page", async () => {
    const user = userEvent.setup();
    const first = operationsView([operation({ operation_id: "op-1" })], "cursor-2");
    const second = operationsView([operation({ operation_id: "op-2" })], null);
    mocks.useTenantProvisionOperations.mockImplementation((cursor: string | null) =>
      pageState(cursor === "cursor-2" ? second : first),
    );
    renderPage();

    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    expect(screen.getByText("有")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() =>
      expect(mocks.useTenantProvisionOperations).toHaveBeenCalledWith("cursor-2", true),
    );
    expect(await screen.findByText("op-2")).toBeVisible();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    expect(screen.getByText("无")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "上一页" }));
    await waitFor(() =>
      expect(mocks.useTenantProvisionOperations).toHaveBeenCalledWith(null, true),
    );
    expect(await screen.findByText("op-1")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "刷新操作" }));
    expect(mocks.pageRefetch).toHaveBeenCalledTimes(1);
  });

  it("clears the selected operation when the admin identity changes", async () => {
    const user = userEvent.setup();
    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(id === "op-1" ? operation() : undefined),
    );
    const view = renderPage();
    await selectOperation(user);
    expect(screen.getByText("操作 ID")).toBeVisible();

    mocks.useSession.mockReturnValue(sessionState({ data: { ...actor, id: "admin-2" } }));
    view.rerender(<TenantLifecyclePage />);
    expect(await screen.findByText("选择一条操作查看步骤回执和当前 revision。")).toBeVisible();
    expect(screen.queryByRole("heading", { level: 2, name: "操作详情" })).not.toBeInTheDocument();
  });

  it("keeps detail loading, forbidden and failed reads bounded with retry", async () => {
    const user = userEvent.setup();
    mocks.useTenantProvisionOperation.mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
      error: null,
      refetch: mocks.detailRefetch,
    });
    const view = renderPage();
    await user.click(screen.getByRole("button", { name: "查看回执" }));
    expect(screen.getByText("操作详情")).toBeVisible();

    mocks.useTenantProvisionOperation.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError("无权查看", 403, "forbidden", false, null),
      refetch: mocks.detailRefetch,
    });
    view.rerender(<TenantLifecyclePage />);
    expect(await screen.findByText("当前身份无权查看该操作。")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "重新读取" }));
    expect(mocks.detailRefetch).toHaveBeenCalledTimes(1);

    mocks.useTenantProvisionOperation.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError("读取详情失败", 500, "internal", false, null),
      refetch: mocks.detailRefetch,
    });
    view.rerender(<TenantLifecyclePage />);
    expect(await screen.findByText("读取详情失败")).toBeVisible();
  });

  it("validates step receipts before recording a completed step", async () => {
    const user = userEvent.setup();
    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(id === "op-1" ? operation() : undefined),
    );
    mocks.recordTenantProvisionStep.mockResolvedValue({
      schema_version: "tenant-provision-operation/v1",
      operation: operation({ revision: "4" }),
    });
    renderPage();
    await selectOperation(user);

    const receipt = screen.getByLabelText("步骤回执 SHA-256（完成/核对时必填）");
    await user.click(screen.getByRole("button", { name: "确认步骤已完成" }));
    await confirm(user, "确认提交步骤回执？");
    expect(
      await screen.findByText("完成步骤或核对外部结果时必须提供有效的回执 SHA-256。"),
    ).toBeVisible();
    expect(mocks.recordTenantProvisionStep).not.toHaveBeenCalled();

    await user.type(receipt, "ABC");
    await user.click(screen.getByRole("button", { name: "确认步骤已完成" }));
    await confirm(user, "确认提交步骤回执？");
    expect(await screen.findByText("回执 SHA-256 必须是 64 位小写十六进制字符串。")).toBeVisible();
    expect(mocks.recordTenantProvisionStep).not.toHaveBeenCalled();

    await user.clear(receipt);
    await user.type(receipt, submittedHash);
    await user.click(screen.getByRole("button", { name: "确认步骤已完成" }));
    const dialog = await openConfirm("确认提交步骤回执？");
    expect(within(dialog).getByText("此操作只记录人工确认的步骤回执。")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "确认记录" }));
    await removeDialog(dialog);

    await waitFor(() =>
      expect(mocks.recordTenantProvisionStep).toHaveBeenCalledWith(
        "op-1",
        {
          expected_revision: "3",
          state: "complete",
          receipt_sha256: submittedHash,
          reconciliation: "",
        },
        commandContext,
      ),
    );
    expect(await screen.findByText("步骤回执已提交，页面将刷新。")).toBeVisible();
    await waitFor(() =>
      expect(screen.getByLabelText("步骤回执 SHA-256（完成/核对时必填）")).toHaveValue(""),
    );
  });

  it("records external-unknown and failed steps without an external receipt", async () => {
    const user = userEvent.setup();
    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(id === "op-1" ? operation() : undefined),
    );
    mocks.recordTenantProvisionStep.mockResolvedValue({
      schema_version: "tenant-provision-operation/v1",
      operation: operation(),
    });
    renderPage();
    await selectOperation(user);

    await user.click(screen.getByRole("button", { name: "记录外部结果未知" }));
    let dialog = await openConfirm("确认记录外部结果未知？");
    expect(
      within(dialog).getByText("服务会阻止推进后续步骤。请勿将未知状态当作失败后直接重试。"),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "确认记录" }));
    await removeDialog(dialog);
    await waitFor(() =>
      expect(mocks.recordTenantProvisionStep).toHaveBeenCalledWith(
        "op-1",
        expect.objectContaining({
          state: "external_unknown",
          receipt_sha256: null,
          reconciliation: "",
        }),
        commandContext,
      ),
    );

    expect(await screen.findByText("步骤回执已提交，页面将刷新。")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "记录步骤失败" }));
    dialog = await openConfirm("确认提交步骤回执？");
    expect(within(dialog).getByText("此操作只记录人工确认的步骤回执。")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "确认记录" }));
    await waitFor(() =>
      expect(mocks.recordTenantProvisionStep).toHaveBeenLastCalledWith(
        "op-1",
        expect.objectContaining({
          state: "failed",
          receipt_sha256: null,
          reconciliation: "",
        }),
        commandContext,
      ),
    );
  });

  it("requires a reconciliation decision and external receipt before completing the review", async () => {
    const user = userEvent.setup();
    const pendingReconciliation = operation({
      status: "reconciliation_required",
      reconciliation_required: true,
      steps: [
        { name: "identity", state: "external_unknown", receipt_sha256: null, updated_at: null },
      ],
    });
    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(id === "op-1" ? pendingReconciliation : undefined),
    );
    mocks.recordTenantProvisionStep.mockResolvedValue({
      schema_version: "tenant-provision-operation/v1",
      operation: operation(),
    });
    renderPage();
    await selectOperation(user);

    expect(screen.getByText("外部操作结果待核对")).toBeVisible();
    expect(screen.getByText(/不要直接重放可能已经执行的动作/)).toBeVisible();
    const editor = screen.getByRole("region", { name: "记录当前步骤" });
    const submit = within(editor).getByRole("button", { name: "提交核对结果" });
    expect(submit).toBeDisabled();
    await chooseOption(user, within(editor).getByRole("combobox"), "已确认外部操作完成");
    expect(submit).toBeEnabled();

    await user.click(submit);
    await confirm(user, "确认已核对外部结果？");
    expect(
      await screen.findByText("完成步骤或核对外部结果时必须提供有效的回执 SHA-256。"),
    ).toBeVisible();
    expect(mocks.recordTenantProvisionStep).not.toHaveBeenCalled();

    await user.type(within(editor).getByLabelText("外部核对回执 SHA-256（必填）"), submittedHash);
    await user.click(submit);
    const dialog = await openConfirm("确认已核对外部结果？");
    expect(
      within(dialog).getByText(
        "请确认这是从外部权威处核对的结果。页面只写回核对回执，不会重新执行外部操作。",
      ),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "确认记录" }));
    await removeDialog(dialog);

    await waitFor(() =>
      expect(mocks.recordTenantProvisionStep).toHaveBeenCalledWith(
        "op-1",
        {
          expected_revision: "3",
          state: "reconciled",
          receipt_sha256: submittedHash,
          reconciliation: "confirmed_complete",
        },
        commandContext,
      ),
    );
  });

  it("refreshes the operation after revision conflicts and stale command contexts", async () => {
    const user = userEvent.setup();
    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(id === "op-1" ? operation() : undefined),
    );
    mocks.recordTenantProvisionStep
      .mockRejectedValueOnce(
        new ApiError("操作版本已变化", 409, "tenant_operation_revision_conflict", false, null),
      )
      .mockRejectedValueOnce(new ApiError("上下文已过期", 409, "context_stale", false, null))
      .mockRejectedValueOnce(new ApiError("上下文已过期", 409, "context_stale", false, null));
    mocks.revalidateSessionContext
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error("刷新失败"));
    const { queryClient } = renderPage();
    await selectOperation(user);

    const receipt = screen.getByLabelText("步骤回执 SHA-256（完成/核对时必填）");
    await user.type(receipt, submittedHash);
    await user.click(screen.getByRole("button", { name: "确认步骤已完成" }));
    await confirm(user, "确认提交步骤回执？");
    expect(await screen.findByText("操作版本已变化")).toBeVisible();
    await waitFor(() => expect(mocks.detailRefetch).toHaveBeenCalledTimes(1));
    expect(screen.getByLabelText("步骤回执 SHA-256（完成/核对时必填）")).toHaveValue("");

    await user.click(screen.getByRole("button", { name: "记录外部结果未知" }));
    await confirm(user, "确认记录外部结果未知？");
    expect(await screen.findByText("上下文已过期")).toBeVisible();
    await waitFor(() => expect(mocks.detailRefetch).toHaveBeenCalledTimes(2));
    expect(mocks.revalidateSessionContext).toHaveBeenCalledWith(queryClient, "admin");

    await user.click(screen.getByRole("button", { name: "记录外部结果未知" }));
    await confirm(user, "确认记录外部结果未知？");
    await waitFor(() => expect(mocks.detailRefetch).toHaveBeenCalledTimes(3));
    expect(mocks.clearIdentityQueries).toHaveBeenCalledWith(queryClient);
    expect(mocks.sessionFor).toHaveBeenCalledWith("admin");
  });

  it("clears the step draft when the command context is no longer current", async () => {
    const user = userEvent.setup();
    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(id === "op-1" ? operation() : undefined),
    );
    mocks.captureCommandContext.mockImplementationOnce(() => {
      throw new DOMException("Session changed", "AbortError");
    });
    renderPage();
    await selectOperation(user);

    const receipt = screen.getByLabelText("步骤回执 SHA-256（完成/核对时必填）");
    await user.type(receipt, submittedHash);
    await user.click(screen.getByRole("button", { name: "确认步骤已完成" }));
    await confirm(user, "确认提交步骤回执？");

    expect(await screen.findByText("请求没有完成")).toBeVisible();
    expect(mocks.recordTenantProvisionStep).not.toHaveBeenCalled();
    expect(screen.getByLabelText("步骤回执 SHA-256（完成/核对时必填）")).toHaveValue("");
    await waitFor(() => expect(mocks.revalidateSessionContext).toHaveBeenCalled());
  });

  it("renders completed and failed operations as terminal records", async () => {
    const user = userEvent.setup();
    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(
        id === "op-1"
          ? operation({
              status: "completed",
              current_step: "completed",
              steps: [
                {
                  name: "completed",
                  state: "complete",
                  receipt_sha256: receiptHash,
                  updated_at: null,
                },
              ],
            })
          : undefined,
      ),
    );
    const view = renderPage();
    await selectOperation(user);
    expect(screen.getByText("操作已完成。归档和删除不属于本页的恢复动作。")).toBeVisible();
    expect(screen.queryByRole("region", { name: "记录当前步骤" })).not.toBeInTheDocument();

    mocks.useTenantProvisionOperation.mockImplementation((id: string | null) =>
      detailState(
        id === "op-1"
          ? operation({ status: "provision_failed", current_step: "identity" })
          : undefined,
      ),
    );
    view.rerender(<TenantLifecyclePage />);
    expect(await screen.findByText("当前操作已终止")).toBeVisible();
    expect(screen.getByText(/失败操作不能重放/)).toBeVisible();
    expect(screen.queryByRole("region", { name: "记录当前步骤" })).not.toBeInTheDocument();
  });

  it("surfaces create permission denial and resets the form when the admin context goes stale", async () => {
    const user = userEvent.setup();
    mocks.createTenantProvisionOperation
      .mockRejectedValueOnce(new ApiError("没有平台权限", 403, "forbidden", false, null))
      .mockRejectedValueOnce(new ApiError("上下文已过期", 409, "context_stale", false, null));
    renderPage();

    await user.type(screen.getByLabelText("租户标识"), "tenant-8");
    await user.type(screen.getByLabelText("租户名称"), "南区租户");
    await user.type(screen.getByLabelText("辖区标识"), "cn-south");
    await user.type(screen.getByLabelText("已有管理员主体标识"), "principal-8");
    await user.type(screen.getByLabelText("管理员组织单元标识"), "org-8");
    await user.type(screen.getByLabelText("管理员组织单元名称"), "南区组织");
    await user.type(screen.getByLabelText("已发布租户配置版本"), "config-8");
    await user.click(screen.getByRole("button", { name: "登记开通操作" }));
    await confirm(user, "确认登记开通操作？", "确认登记");

    expect(await screen.findByText("没有平台权限")).toBeVisible();
    expect(screen.getByText("当前身份没有平台租户生命周期操作权限。")).toBeVisible();
    expect(mocks.clearPendingMutationKey).not.toHaveBeenCalled();

    await waitFor(() => expect(screen.getByRole("button", { name: /登记开通操作/ })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: /登记开通操作/ }));
    await confirm(user, "确认登记开通操作？", "确认登记");

    expect(await screen.findByText("上下文已过期")).toBeVisible();
    await waitFor(() => expect(screen.getByLabelText("租户标识")).toHaveValue(""));
    expect(screen.getByLabelText("租户名称")).toHaveValue("");
    expect(mocks.clearPendingMutationKey).toHaveBeenCalledWith("storage-1");
    expect(mocks.revalidateSessionContext).toHaveBeenCalledWith(expect.anything(), "admin");
  });
});
