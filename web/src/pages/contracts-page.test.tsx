import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { CONTRACT_LEDGER_COLUMNS, contractLedgerExportColumns, parseContractExport } from "../api/contracts";
import { setSessionIdentity } from "../api/http";
import { idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import { renderWithAntd as render } from "../test/antd";
import { ContractsPage } from "./contracts-page";
import labelCases from "./contracts-ledger-labels.fixture.json";

const source = vi.hoisted(() => ({
  rows: [] as Array<Record<string, unknown>>,
  drafts: [] as Array<Record<string, unknown>>,
  realSavedViews: false,
  actor: {
    id: "synthetic-admin", role: "admin", audience: "client", tenant_id: "tenant-1", org_id: "org-1",
    active_context: { context_version: "1" }, csrf_token: "synthetic-csrf-token",
  },
}));

vi.mock("../api/contracts", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/contracts")>(),
  listMyContractDrafts: vi.fn(async () => source.drafts),
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
    useContractLedger: () => ({ data: { pages: [{ items: source.rows }] }, isLoading: false, isError: false, hasNextPage: false }),
    useContractSavedViews: (scope: string, enabled: boolean) => source.realSavedViews
      ? actual.useContractSavedViews(scope, enabled)
      : { data: [{
          view_id: "state-view", name: "状态对照", filter: {}, row_version: 1,
          columns: ["contract_name", "authority_mode", "contract_state", "workflow_state"],
        }] },
    useCreateContractSavedView: () => source.realSavedViews ? actual.useCreateContractSavedView() : { mutateAsync: vi.fn() },
    useReplaceContractSavedView: () => source.realSavedViews ? actual.useReplaceContractSavedView() : { mutateAsync: vi.fn() },
    useDeleteContractSavedView: () => source.realSavedViews ? actual.useDeleteContractSavedView() : { mutateAsync: vi.fn() },
    useCases: () => ({ data: [], isSuccess: true, isError: false }),
    useCaseDetail: () => ({ data: undefined, isError: false }),
  };
});

function row(overrides: Record<string, string>) {
  return {
    contract_id: "11111111-1111-4111-8111-111111111111", name: "示例合同", number: null,
    authority_mode: "native", contract_state: "draft", workflow_state: "not_started",
    amount: null, currency: null, owner_org_id: "org-1", row_version: 1,
    created_at: "2026-11-05T00:00:00Z", updated_at: "2026-11-05T00:00:00Z", ...overrides,
  };
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><ContractsPage /></QueryClientProvider>);
}

beforeEach(() => {
  source.rows = [row({})];
  source.drafts = [];
  source.realSavedViews = false;
  sessionStorage.clear();
  setSessionIdentity(source.actor);
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: vi.fn(() => "blob:synthetic") });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: vi.fn() });
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
});

afterEach(() => {
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

it("uses the submitted readable columns and obtains the CSV from the ledger routes", async () => {
  const exportID = "11111111-1111-4111-8111-111111111111";
  const csv = new Uint8Array([0xef, 0xbb, 0xbf, ...new TextEncoder().encode("合同名称,合同状态名称,合同状态代码\n示例合同,草稿,draft\n")]);
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === "/api/contracts/ledger/exports" && init?.method === "POST") {
      const payload = JSON.parse(String(init.body));
      return new Response(JSON.stringify({
        export_id: exportID, state: "ready", format: "csv",
        columns: payload.columns, row_count: 1, created_at: "2026-11-05T00:00:00Z",
        expires_at: "2026-11-06T00:00:00Z", download_available: true, replayed: false,
      }), { status: 201, headers: { "Content-Type": "application/json" } });
    }
    if (String(url) === `/api/contracts/ledger/exports/${exportID}/download` && init?.method === "GET")
      return new Response(csv, { status: 200, headers: { "Content-Type": "text/csv; charset=utf-8" } });
    return new Response(JSON.stringify({ error: "not found" }), { status: 404, headers: { "Content-Type": "application/json" } });
  });
  vi.stubGlobal("fetch", fetch);
  renderPage();
  await userEvent.setup().click(screen.getByRole("button", { name: "导出 CSV" }));
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  expect(String(fetch.mock.calls[0]?.[0])).toBe("/api/contracts/ledger/exports");
  await screen.findByText("已导出 1 行");
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(String(fetch.mock.calls[1]?.[0])).toBe(`/api/contracts/ledger/exports/${exportID}/download`);
  const request = fetch.mock.calls.find(([, init]) => init?.method === "POST")?.[1];
  expect(request).toBeDefined();
  const payload = JSON.parse(String(request!.body));
  expect(payload).toEqual({
    filter: {},
    columns: ["contract_number", "contract_name", "supplier_name", "amount", "currency", "department_id",
      "contract_state_label", "contract_state_code", "workflow_state_label", "workflow_state_code", "updated_at"],
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
  let saved: { view_id: string; name: string; filter: unknown; columns: unknown; row_version: number; created_at: string; updated_at: string } | null = null;
  const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const route = String(url);
    const headers = new Headers(init?.headers);
    if (init?.method !== "GET" && (headers.get("X-Context-Version") !== "1" || headers.get("X-CSRF-Token") !== source.actor.csrf_token))
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
      saved = { ...saved, name: body.name, filter: body.filter, columns: body.columns, row_version: 2 };
      return json(saved);
    }
    if (route === `${path}/${viewID}?expected_row_version=2` && init?.method === "DELETE" && saved?.row_version === 2) {
      saved = null;
      return json({ deleted: true });
    }
    return json({ error: "not found" }, 404);
  });
  vi.stubGlobal("fetch", fetch);
  renderPage();
  const user = userEvent.setup();
  const search = screen.getByPlaceholderText("名称、服务端编号或内容编号");
  const name = screen.getByPlaceholderText("视图名称（最多 60 字）");
  await user.type(search, "设备");
  await user.click(screen.getByRole("button", { name: "查询" }));
  await user.type(name, "设备视图");
  await user.click(screen.getByRole("button", { name: "保存视图" }));
  expect(await screen.findByText("视图已保存")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "重置" }));
  await user.click(screen.getByText("应用已保存视图"));
  await user.click(await screen.findByRole("option", { name: "设备视图" }));
  expect(search).toHaveValue("设备");
  expect(name).toHaveValue("设备视图");
  await user.clear(name);
  await user.type(name, "更新视图");
  await user.click(await screen.findByRole("button", { name: /更新视图/ }));
  expect(await screen.findByText("视图已更新")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "删除视图" }));
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("button", { name: "删除视图" }));
  expect(await screen.findByText("视图已删除")).toBeInTheDocument();
  const writes = fetch.mock.calls.filter(([, init]) => init?.method !== "GET");
  expect(writes.map(([url, init]) => [String(url), init?.method])).toEqual([
    [path, "POST"], [`${path}/${viewID}`, "POST"], [`${path}/${viewID}?expected_row_version=2`, "DELETE"],
  ]);
  expect(JSON.parse(String(writes[0]![1]?.body))).toMatchObject({ name: "设备视图", filter: { text: "设备" } });
  expect(JSON.parse(String(writes[1]![1]?.body))).toMatchObject({ name: "更新视图", filter: { text: "设备" }, expected_row_version: 1 });
  expect(saved).toBeNull();
});

it("expands all selected display fields in place within the twenty-column request limit", () => {
  const columns = contractLedgerExportColumns(CONTRACT_LEDGER_COLUMNS.map((column) => column.key));
  expect(columns).toEqual([
    "contract_number", "contract_name", "authority_mode_label", "authority_mode_code", "supplier_name", "amount", "currency",
    "department_id", "project_type", "procurement_method", "contract_state_label", "contract_state_code",
    "workflow_state_label", "workflow_state_code", "signing_date", "source_system", "created_at", "updated_at",
  ]);
  expect(columns).toHaveLength(18);
  const response = {
    export_id: "11111111-1111-4111-8111-111111111111", state: "ready", format: "csv", columns,
    row_count: 1, created_at: "2026-11-05T00:00:00Z", expires_at: "2026-11-06T00:00:00Z",
    download_available: true, replayed: false,
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
  source.rows = labelCases.map((item, index) => row({
    contract_id: String(index), name: item.name,
    authority_mode: item.authority, contract_state: item.state, workflow_state: item.workflow,
  }));
  renderPage();
  const user = userEvent.setup();
  const writeText = vi.spyOn(navigator.clipboard, "writeText");
  await user.click(screen.getByText("应用已保存视图"));
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
  renderPage();
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
