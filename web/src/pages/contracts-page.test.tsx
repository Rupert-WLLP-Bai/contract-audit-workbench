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

vi.mock("../api/queries", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/queries")>(),
  useSession: () => ({ data: source.actor }),
  useContractLedger: () => ({ data: { pages: [{ items: source.rows }] }, isLoading: false, isError: false, hasNextPage: false }),
  useContractSavedViews: () => ({ data: [{
    view_id: "state-view", name: "状态对照", filter: {}, row_version: 1,
    columns: ["contract_name", "authority_mode", "contract_state", "workflow_state"],
  }] }),
  useCreateContractSavedView: () => ({ mutateAsync: vi.fn() }),
  useReplaceContractSavedView: () => ({ mutateAsync: vi.fn() }),
  useDeleteContractSavedView: () => ({ mutateAsync: vi.fn() }),
  useCases: () => ({ data: [], isSuccess: true, isError: false }),
  useCaseDetail: () => ({ data: undefined, isError: false }),
}));

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

it("uses the submitted readable columns and format for the pending request fingerprint", async () => {
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === "/api/contracts/ledger/exports" && init?.method === "POST") {
      const payload = JSON.parse(String(init.body));
      return new Response(JSON.stringify({
        export_id: "11111111-1111-4111-8111-111111111111", state: "ready", format: "csv",
        columns: payload.columns, row_count: 1, created_at: "2026-11-05T00:00:00Z",
        expires_at: "2026-11-06T00:00:00Z", download_available: true, replayed: false,
      }), { status: 201, headers: { "Content-Type": "application/json" } });
    }
    if (String(url).startsWith("/api/contracts/ledger/exports/") && init?.method === "GET")
      return new Response("synthetic csv", { status: 200, headers: { "Content-Type": "text/csv" } });
    return new Response(JSON.stringify({ error: "not found" }), { status: 404, headers: { "Content-Type": "application/json" } });
  });
  vi.stubGlobal("fetch", fetch);
  renderPage();
  await userEvent.setup().click(screen.getByRole("button", { name: "导出 CSV" }));
  await waitFor(() => expect(fetch).toHaveBeenCalled());
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
