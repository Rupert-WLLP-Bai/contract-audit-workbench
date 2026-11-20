// File metadata (manifest mirror):
// Purpose: Checks registration and ledger workflows, saved filters and exports, and return to the authorized contract material list without losing its filters.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-20
// Revision: r6
// Copyright: See LICENSE
// End file metadata.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter } from "react-router";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  CONTRACT_LEDGER_COLUMNS,
  contractLedgerExportColumns,
  parseContractExport,
  type ContractLedgerFilter,
} from "../api/contracts";
import { setSessionIdentity } from "../api/http";
import { idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import { renderWithAntd as render } from "../test/antd";
import { ContractsPage } from "./contracts-page";
import { AdministrativeTechnicalDetails } from "../components/app-shell-preferences";
import labelCases from "./contracts-ledger-labels.fixture.json";

const source = vi.hoisted(() => ({
  ledgerFilter: vi.fn<(filter: ContractLedgerFilter) => void>(),
  rows: [] as Array<Record<string, unknown>>,
  drafts: [] as Array<Record<string, unknown>>,
  realSavedViews: false,
  actor: {
    id: "synthetic-admin",
    role: "admin",
    audience: "client",
    tenant_id: "tenant-1",
    org_id: "org-1",
    active_context: { context_version: "1" },
    csrf_token: "synthetic-csrf-token",
  },
}));

vi.mock("../api/contracts", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/contracts")>()),
  listMyContractDrafts: vi.fn(async () => source.drafts),
  listMyContractReminderInbox: vi.fn(async () => ({ items: [] })),
}));

vi.mock("../api/idempotency", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/idempotency")>();
  return { ...actual, pendingMutationKey: vi.fn(actual.pendingMutationKey) };
});

vi.mock("../api/queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/queries")>();
  return {
    ...actual,
    useSession: () => ({ data: source.actor }),
    useContractLedger: (filter: ContractLedgerFilter) => {
      source.ledgerFilter(filter);
      return {
        data: { pages: [{ items: source.rows }] },
        isLoading: false,
        isError: false,
        hasNextPage: false,
      };
    },
    useContractSavedViews: (scope: string, enabled: boolean) =>
      source.realSavedViews
        ? actual.useContractSavedViews(scope, enabled)
        : {
            data: [
              {
                view_id: "state-view",
                name: "状态对照",
                filter: {},
                row_version: 1,
                columns: ["contract_name", "authority_mode", "contract_state", "workflow_state"],
              },
            ],
          },
    useCreateContractSavedView: () =>
      source.realSavedViews ? actual.useCreateContractSavedView() : { mutateAsync: vi.fn() },
    useReplaceContractSavedView: () =>
      source.realSavedViews ? actual.useReplaceContractSavedView() : { mutateAsync: vi.fn() },
    useDeleteContractSavedView: () =>
      source.realSavedViews ? actual.useDeleteContractSavedView() : { mutateAsync: vi.fn() },
    useCases: () => ({ data: [], isSuccess: true, isError: false }),
    useCaseDetail: () => ({ data: undefined, isError: false }),
  };
});

function row(overrides: Record<string, string>) {
  return {
    contract_id: "11111111-1111-4111-8111-111111111111",
    name: "示例合同",
    number: null,
    authority_mode: "native",
    contract_state: "draft",
    workflow_state: "not_started",
    amount: null,
    currency: null,
    owner_org_id: "org-1",
    row_version: 1,
    created_at: "2026-11-05T00:00:00Z",
    updated_at: "2026-11-05T00:00:00Z",
    ...overrides,
  };
}

function renderPage(path = "/", entry?: ReactNode, showTechnical = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        {entry}
        {showTechnical ? (
          <AdministrativeTechnicalDetails>
            <ContractsPage />
          </AdministrativeTechnicalDetails>
        ) : (
          <ContractsPage />
        )}
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

it("uses the route text with the existing ledger query and clears a previous route filter", async () => {
  renderPage("/contracts?q=CGHT202603008", <Link to="/contracts">清空路由搜索</Link>);
  expect(source.ledgerFilter).toHaveBeenLastCalledWith({ text: "CGHT202603008" });
  await userEvent.click(screen.getByRole("link", { name: "清空路由搜索" }));
  await waitFor(() => expect(source.ledgerFilter).toHaveBeenLastCalledWith({ text: undefined }));
});

beforeEach(() => {
  source.rows = [row({})];
  source.drafts = [];
  source.realSavedViews = false;
  sessionStorage.clear();
  setSessionIdentity(source.actor);
  Object.defineProperty(URL, "createObjectURL", {
    configurable: true,
    value: vi.fn(() => "blob:synthetic"),
  });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: vi.fn() });
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
});

afterEach(() => {
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

it("shows six ledger columns and applies field visibility without changing the draft state", async () => {
  renderPage();
  const user = userEvent.setup();
  const table = within(screen.getByRole("region", { name: "合同台账" })).getByRole("table");
  expect(
    within(table)
      .getAllByRole("columnheader")
      .map((cell) => cell.textContent),
  ).toEqual(["合同名称与编号", "对方主体", "合同金额", "状态", "会签", "更新时间"]);
  expect(within(table).getByText("草稿")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "字段" }));
  const fields = screen.getByRole("dialog", { name: "显示字段" });
  await user.click(within(fields).getByRole("checkbox", { name: "供应商" }));
  expect(within(table).queryByRole("columnheader", { name: "对方主体" })).not.toBeInTheDocument();
  expect(within(table).getByText("草稿")).toBeVisible();
});

it("opens the authorized ID from a data cell and keeps a keyboard action when identity columns are hidden", async () => {
  source.rows = [row({ supplier_name: "对方主体", amount: "74514600.00", currency: "CNY" })];
  const fetch = vi.fn(
    async (_url: RequestInfo | URL, _init?: RequestInit) =>
      new Response(JSON.stringify({ error: "forbidden" }), {
        status: 403,
        headers: { "Content-Type": "application/json" },
      }),
  );
  vi.stubGlobal("fetch", fetch);
  renderPage();
  const user = userEvent.setup();
  const table = within(screen.getByRole("region", { name: "合同台账" })).getByRole("table");
  expect(within(table).getByText("CNY 74,514,600.00")).toBeVisible();
  const amountCell = within(table).getByText("CNY 74,514,600.00").closest("td")!;
  expect(amountCell).toHaveClass("contracts-ledger-amount");
  await user.click(within(table).getByRole("cell", { name: "对方主体" }));
  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith(
      expect.stringContaining("/api/contracts/11111111-1111-4111-8111-111111111111"),
      expect.objectContaining({ method: "GET" }),
    ),
  );
  await user.click(screen.getByRole("button", { name: "字段" }));
  const fields = screen.getByRole("dialog", { name: "显示字段" });
  await user.click(within(fields).getByRole("checkbox", { name: "合同名称" }));
  await user.click(within(fields).getByRole("checkbox", { name: "合同编号" }));
  const action = within(table).getByRole("button", { name: "查看详情" });
  action.focus();
  await user.keyboard("{Enter}");
  await waitFor(() =>
    expect(
      fetch.mock.calls.filter(([url]) =>
        String(url).includes("/api/contracts/11111111-1111-4111-8111-111111111111"),
      ),
    ).toHaveLength(2),
  );
});

it("uses the submitted readable columns and obtains the CSV from the ledger routes", async () => {
  const exportID = "11111111-1111-4111-8111-111111111111";
  const csv = new Uint8Array([
    0xef,
    0xbb,
    0xbf,
    ...new TextEncoder().encode("合同名称,合同状态名称,合同状态代码\n示例合同,草稿,draft\n"),
  ]);
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === "/api/contracts/ledger/exports" && init?.method === "POST") {
      const payload = JSON.parse(String(init.body));
      return new Response(
        JSON.stringify({
          export_id: exportID,
          state: "ready",
          format: "csv",
          columns: payload.columns,
          row_count: 1,
          created_at: "2026-11-05T00:00:00Z",
          expires_at: "2026-11-06T00:00:00Z",
          download_available: true,
          replayed: false,
        }),
        { status: 201, headers: { "Content-Type": "application/json" } },
      );
    }
    if (
      String(url) === `/api/contracts/ledger/exports/${exportID}/download` &&
      init?.method === "GET"
    )
      return new Response(csv, {
        status: 200,
        headers: { "Content-Type": "text/csv; charset=utf-8" },
      });
    return new Response(JSON.stringify({ error: "not found" }), {
      status: 404,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fetch);
  renderPage();
  expect(await screen.findByRole("heading", { name: "合同台账", level: 1 })).toBeVisible();
  await userEvent.setup().click(screen.getByRole("button", { name: "导出 CSV" }));
  const exportCalls = () => fetch.mock.calls.filter(([, init]) => init?.method === "POST");
  await waitFor(() => expect(exportCalls()).toHaveLength(1));
  expect(String(exportCalls()[0]?.[0])).toBe("/api/contracts/ledger/exports");
  await within(screen.getByRole("region", { name: "合同台账" })).findByText("已导出 1 行");
  await waitFor(() =>
    expect(
      fetch.mock.calls.some(
        ([url]) => String(url) === `/api/contracts/ledger/exports/${exportID}/download`,
      ),
    ).toBe(true),
  );
  const request = exportCalls()[0]?.[1];
  expect(request).toBeDefined();
  const payload = JSON.parse(String(request!.body));
  expect(payload).toEqual({
    filter: {},
    columns: [
      "contract_name",
      "contract_number",
      "supplier_name",
      "amount",
      "currency",
      "contract_state_label",
      "contract_state_code",
      "workflow_state_label",
      "workflow_state_code",
      "updated_at",
    ],
    format: "csv",
  });
  expect(vi.mocked(pendingMutationKey).mock.calls[0]?.[3]).toEqual(payload);
  const pending = await vi.mocked(pendingMutationKey).mock.results[0]!.value;
  expect(new Headers(request!.headers).get("Idempotency-Key")).toBe(pending.key);
  expect(new Headers(request!.headers).get("X-Context-Version")).toBe("1");
  expect(new Headers(request!.headers).get("X-CSRF-Token")).toBe(source.actor.csrf_token);
  const blob = vi.mocked(URL.createObjectURL).mock.calls[0]?.[0] as Blob;
  expect(blob).toBeInstanceOf(Blob);
  expect(Array.from(new Uint8Array(await blob.arrayBuffer()))).toEqual(Array.from(csv));
  expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1);
});

it("saves, reapplies, updates and deletes a view through the ledger API", async () => {
  source.realSavedViews = true;
  const viewID = "22222222-2222-4222-8222-222222222222";
  const path = "/api/contracts/ledger/views";
  const at = "2026-11-05T00:00:00Z";
  let saved: {
    view_id: string;
    name: string;
    filter: unknown;
    columns: unknown;
    row_version: number;
    created_at: string;
    updated_at: string;
  } | null = null;
  const json = (value: unknown, status = 200) =>
    new Response(JSON.stringify(value), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const route = String(url);
    const headers = new Headers(init?.headers);
    if (
      init?.method !== "GET" &&
      (headers.get("X-Context-Version") !== "1" ||
        headers.get("X-CSRF-Token") !== source.actor.csrf_token)
    )
      return json({ error: "missing context" }, 403);
    if (route === path && init?.method === "GET") return json({ items: saved ? [saved] : [] });
    if (route === path && init?.method === "POST" && saved === null) {
      const body = JSON.parse(String(init.body));
      saved = { ...body, view_id: viewID, row_version: 1, created_at: at, updated_at: at };
      return json(saved, 201);
    }
    if (route === `${path}/${viewID}` && init?.method === "POST" && saved !== null) {
      const body = JSON.parse(String(init.body));
      if (body.expected_row_version !== saved.row_version) return json({ error: "stale" }, 409);
      saved = {
        ...saved,
        name: body.name,
        filter: body.filter,
        columns: body.columns,
        row_version: 2,
      };
      return json(saved);
    }
    if (
      route === `${path}/${viewID}?expected_row_version=2` &&
      init?.method === "DELETE" &&
      saved?.row_version === 2
    ) {
      saved = null;
      return json({ deleted: true });
    }
    return json({ error: "not found" }, 404);
  });
  vi.stubGlobal("fetch", fetch);
  renderPage();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "筛选" }));
  const filters = screen.getByRole("dialog", { name: "合同台账筛选" });
  await user.type(within(filters).getByLabelText("合同名称或编号"), "设备");
  await user.type(within(filters).getByLabelText("供应商"), "供应商甲");
  await user.click(within(filters).getByRole("button", { name: "查询" }));
  await user.click(screen.getByRole("button", { name: "视图" }));
  const name = screen.getByPlaceholderText("视图名称（最多 60 字）");
  await user.type(name, "设备视图");
  await user.click(screen.getByRole("button", { name: "保存视图" }));
  expect(await screen.findByText("视图已保存")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /筛选/ }));
  await user.click(
    within(screen.getByRole("dialog", { name: "合同台账筛选" })).getByRole("button", {
      name: "重置",
    }),
  );
  await user.click(screen.getByRole("button", { name: "视图" }));
  await user.click(
    within(screen.getByRole("dialog", { name: "已保存视图" })).getByRole("combobox", {
      name: "应用已保存视图",
    }),
  );
  await user.click(await screen.findByRole("option", { name: "设备视图" }));
  expect(screen.getByRole("searchbox", { name: "搜索合同" })).toHaveValue("设备");
  expect(source.ledgerFilter).toHaveBeenLastCalledWith({ text: "设备", supplier_name: "供应商甲" });
  expect(name).toHaveValue("设备视图");
  await user.clear(name);
  await user.type(name, "更新视图");
  await user.click(await screen.findByRole("button", { name: /更新视图/ }));
  expect(await screen.findByText("视图已更新")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "删除视图" }));
  await waitFor(() => expect(document.querySelector(".ant-modal-confirm")).not.toBeNull());
  const dialog = document.querySelector<HTMLElement>(".ant-modal-confirm")!;
  expect(dialog).toHaveTextContent("删除这个台账视图？");
  await user.click(within(dialog).getByRole("button", { name: "删除视图" }));
  await waitFor(() => expect(saved).toBeNull());
  const viewButton = screen.getByRole("button", { name: "视图" });
  if (viewButton.getAttribute("aria-expanded") !== "true") await user.click(viewButton);
  expect(await screen.findByText("视图已删除")).toBeInTheDocument();
  const writes = fetch.mock.calls.filter(([, init]) => init?.method !== "GET");
  expect(writes.map(([url, init]) => [String(url), init?.method])).toEqual([
    [path, "POST"],
    [`${path}/${viewID}`, "POST"],
    [`${path}/${viewID}?expected_row_version=2`, "DELETE"],
  ]);
  expect(JSON.parse(String(writes[0]![1]?.body))).toMatchObject({
    name: "设备视图",
    filter: { text: "设备", supplier_name: "供应商甲" },
  });
  expect(JSON.parse(String(writes[1]![1]?.body))).toMatchObject({
    name: "更新视图",
    filter: { text: "设备", supplier_name: "供应商甲" },
    expected_row_version: 1,
  });
  expect(saved).toBeNull();
});

it("expands all selected display fields in place within the twenty-column request limit", () => {
  const columns = contractLedgerExportColumns(CONTRACT_LEDGER_COLUMNS.map((column) => column.key));
  expect(columns).toEqual([
    "contract_number",
    "contract_name",
    "authority_mode_label",
    "authority_mode_code",
    "supplier_name",
    "amount",
    "currency",
    "department_id",
    "project_type",
    "procurement_method",
    "contract_state_label",
    "contract_state_code",
    "workflow_state_label",
    "workflow_state_code",
    "signing_date",
    "source_system",
    "created_at",
    "updated_at",
  ]);
  expect(columns).toHaveLength(18);
  const response = {
    export_id: "11111111-1111-4111-8111-111111111111",
    state: "ready",
    format: "csv",
    columns,
    row_count: 1,
    created_at: "2026-11-05T00:00:00Z",
    expires_at: "2026-11-06T00:00:00Z",
    download_available: true,
    replayed: false,
  };
  expect(parseContractExport(response).columns).toEqual(columns);
  expect(() => parseContractExport({ ...response, columns: ["unknown_column"] })).toThrow();
});

it("keeps an old pending request distinct from the complete new payload and reuses an identical retry", async () => {
  const actor = idempotencyActorScope(source.actor);
  const legacy = { filter: {}, columns: ["contract_name"] };
  const current = { ...legacy, format: "csv" };
  const oldPending = await pendingMutationKey("clm-contract-export", actor, "ledger", legacy);
  const newPending = await pendingMutationKey("clm-contract-export", actor, "ledger", current);
  const retry = await pendingMutationKey("clm-contract-export", actor, "ledger", current);
  expect(newPending.key).not.toBe(oldPending.key);
  expect(retry).toEqual(newPending);
});

it("shows ledger labels and discloses unknown codes only in copyable technical details", async () => {
  // Inject directly at the display boundary; the OpenAPI enum contract stays strict.
  source.rows = labelCases.map((item, index) =>
    row({
      contract_id: String(index),
      name: item.name,
      authority_mode: item.authority,
      contract_state: item.state,
      workflow_state: item.workflow,
    }),
  );
  renderPage("/", undefined, true);
  const user = userEvent.setup();
  const writeText = vi.spyOn(navigator.clipboard, "writeText");
  await user.click(screen.getByRole("button", { name: "视图" }));
  await user.click(
    within(screen.getByRole("dialog", { name: "已保存视图" })).getByRole("combobox", {
      name: "应用已保存视图",
    }),
  );
  await user.click(await screen.findByRole("option", { name: "状态对照" }));
  const table = within(screen.getByRole("region", { name: "合同台账" })).getByRole("table");
  for (const item of labelCases) {
    const cells = within(within(table).getByText(item.name).closest("tr")!).getAllByRole("cell");
    for (const [index, label, code, detail] of [
      [1, item.authorityLabel, item.authority, "来源类型技术详情"],
      [2, item.stateLabel, item.state, "合同状态技术详情"],
      [3, item.workflowLabel, item.workflow, "会签状态技术详情"],
    ] as const) {
      const cell = cells[index]!;
      expect(within(cell).getByText(label)).toBeVisible();
      if (label !== "待核对") {
        expect(within(cell).queryByText(detail)).not.toBeInTheDocument();
        continue;
      }
      const fullCode = within(cell).getByText(code);
      expect(fullCode).not.toBeVisible();
      await user.click(within(cell).getByText(detail));
      expect(fullCode).toBeVisible();
      await user.click(within(cell).getByRole("button", { name: "复制完整值" }));
      await waitFor(() => expect(writeText).toHaveBeenCalledWith(code));
    }
  }
});

it("keeps an unknown draft state behind technical details outside the draft button", async () => {
  // This deliberately bypasses the strict wire contract to exercise the display fallback.
  source.drafts = [row({ name: "状态待核对草稿", contract_state: "future_draft_state" })];
  renderPage("/", undefined, true);
  await userEvent.setup().click(screen.getByRole("tab", { name: "我的草稿" }));
  const draft = (await screen.findByText("状态待核对草稿")).closest("li")!;
  const button = within(draft).getByRole("button", { name: /状态待核对草稿/ });
  expect(button).toHaveTextContent("待核对");
  expect(button).not.toHaveTextContent("future_draft_state");
  const fullCode = within(draft).getByText("future_draft_state");
  expect(fullCode).not.toBeVisible();
  const detail = within(draft).getByText("合同状态技术详情");
  expect(button).not.toContainElement(detail);
  await userEvent.setup().click(detail);
  expect(fullCode).toBeVisible();
});

it("preserves the contract editor draft when switching workspace tabs", async () => {
  renderPage();
  const user = userEvent.setup();
  const ledgerTab = screen.getByRole("tab", { name: "全部合同" });
  ledgerTab.focus();
  await user.keyboard("{ArrowRight}");
  const draftsTab = screen.getByRole("tab", { name: /草稿/ });
  expect(draftsTab).toHaveFocus();
  await user.keyboard("{Enter}");
  expect(draftsTab).toHaveAttribute("aria-selected", "true");
  const name = screen.getByLabelText("合同名称");
  await user.type(name, "切换分区仍保留的草稿名称");
  draftsTab.focus();
  await user.keyboard("{ArrowLeft}");
  expect(ledgerTab).toHaveFocus();
  await user.keyboard("{Enter}");
  expect(ledgerTab).toHaveAttribute("aria-selected", "true");
  expect(name).toHaveValue("切换分区仍保留的草稿名称");
  ledgerTab.focus();
  await user.keyboard("{ArrowRight}");
  expect(draftsTab).toHaveFocus();
  await user.keyboard("{Enter}");
  expect(draftsTab).toHaveAttribute("aria-selected", "true");
  expect(name).toBeVisible();
  expect(name).toHaveValue("切换分区仍保留的草稿名称");
});

it("shows one primary action while editing a new draft", async () => {
  renderPage();
  const user = userEvent.setup();
  await user.click(screen.getByRole("tab", { name: "我的草稿" }));
  await user.type(screen.getByLabelText("合同名称"), "需要保存的草稿");
  const save = screen.getByRole("button", { name: "保存草稿" });
  const create = screen.getByRole("button", { name: "新建合同草稿" });
  expect(save).toHaveClass("ant-btn-primary");
  expect(create).not.toHaveClass("ant-btn-primary");
});

it("keeps one draft while switching among the four contract form sections", async () => {
  renderPage();
  const user = userEvent.setup();
  await user.click(screen.getByRole("tab", { name: /草稿/ }));
  const name = await screen.findByLabelText("合同名称");
  await user.type(name, "四分区共享草稿");
  await user.click(screen.getByRole("button", { name: "相关方与标的" }));
  expect(screen.getByRole("button", { name: "相关方与标的" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  expect(name).not.toBeVisible();
  await user.click(screen.getByRole("button", { name: "资金与付款" }));
  expect(name).not.toBeVisible();
  await user.click(screen.getByRole("button", { name: "材料与核对" }));
  expect(name).not.toBeVisible();
  await user.click(screen.getByRole("button", { name: "合同基本信息" }));
  expect(name).toBeVisible();
  expect(name).toHaveValue("四分区共享草稿");
});
