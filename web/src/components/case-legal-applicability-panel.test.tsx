import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { parseLegalApplicabilityFactVersion } from "../api/legal-applicability";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { CaseLegalApplicabilityPanel } from "./case-legal-applicability-panel";

const mocks = vi.hoisted(() => ({
  getFacts: vi.fn(), getHistory: vi.fn(), recordFact: vi.fn(),
  useSession: vi.fn(), useCaseDetail: vi.fn(), useCaseSubjects: vi.fn(),
  useMaterialParsedDocuments: vi.fn(), useMaterialParsedTextPage: vi.fn(),
  pendingMutationKey: vi.fn(), clearPendingMutationKey: vi.fn(),
}));

vi.mock("../api/legal-applicability", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/legal-applicability")>();
  return { ...actual, getLegalApplicabilityFacts: mocks.getFacts, getLegalApplicabilityFactHistory: mocks.getHistory, recordLegalApplicabilityFact: mocks.recordFact };
});
vi.mock("../api/queries", () => ({
  useSession: mocks.useSession, useCaseDetail: mocks.useCaseDetail, useCaseSubjects: mocks.useCaseSubjects,
  useMaterialParsedDocuments: mocks.useMaterialParsedDocuments, useMaterialParsedTextPage: mocks.useMaterialParsedTextPage,
}));
vi.mock("../api/idempotency", () => ({
  idempotencyActorScope: () => ({ actorId: "actor-1", tenantId: "tenant-1", orgId: "org-1", audience: "client" }),
  pendingMutationKey: mocks.pendingMutationKey, clearPendingMutationKey: mocks.clearPendingMutationKey,
}));

function fixture(name: string): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/contracts/legal", name), "utf8")) as unknown;
}

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}><CaseLegalApplicabilityPanel caseId="case-wire-fixture" /></QueryClientProvider>);
}

beforeEach(() => {
  mocks.getFacts.mockResolvedValue(fixture("applicability-fact-set.json"));
  mocks.getHistory.mockImplementation(async (caseId: string, dimension: string, cursor?: string | null) => {
    const history = fixture("applicability-history.json") as { case_id: string; dimension: string; items: Array<Record<string, unknown>>; current_version: number; can_manage: boolean; next_cursor: string | null };
    if (dimension === "buyer_kind" && cursor) return {
      ...history, case_id: caseId, current_version: 3,
      items: [{ ...history.items[0], fact_id: "laf_wire_buyer_kind_v1", version: 1 }], next_cursor: null,
    };
    if (dimension === "buyer_kind") return { ...history, case_id: caseId, current_version: 3 };
    return { ...history, case_id: caseId, dimension, items: [], current_version: 0, next_cursor: null };
  });
  mocks.recordFact.mockResolvedValue(parseLegalApplicabilityFactVersion(fixture("applicability-record-success.json")));
  mocks.useSession.mockReturnValue({ data: { id: "actor-1", tenant_id: "tenant-1", org_id: "org-1", audience: "client" } });
  mocks.useCaseDetail.mockReturnValue({ data: { materials: [], material_versions: [] }, isError: false, isLoading: false, refetch: vi.fn() });
  mocks.useCaseSubjects.mockReturnValue({ data: { items: [], legacy_profile: null, can_manage: true }, isError: false, refetch: vi.fn() });
  mocks.useMaterialParsedDocuments.mockReturnValue({ data: { items: [] }, isError: false, refetch: vi.fn() });
  mocks.useMaterialParsedTextPage.mockReturnValue({ data: undefined, isError: false, refetch: vi.fn() });
  mocks.pendingMutationKey.mockResolvedValue({ storageKey: "safe-slot", key: "stable-test-key" });
  mocks.clearPendingMutationKey.mockReset();
});
afterEach(() => { vi.clearAllMocks(); vi.unstubAllEnvs(); });

describe("case legal applicability panel", () => {
  it("sends the chosen business and evidence times as UTC from explicit Beijing inputs", async () => {
    vi.stubEnv("TZ", "America/Los_Angeles");
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => expect(mocks.getFacts).toHaveBeenCalled());
    fireEvent.change(screen.getByLabelText("法律判断业务时点（北京时间）"), { target: { value: "2025-05-20T20:00" } });
    fireEvent.change(screen.getByLabelText("法律事实证据截止时点（北京时间）"), { target: { value: "2026-10-31T08:00" } });
    await user.click(screen.getByRole("button", { name: "读取该时点的事实" }));
    await waitFor(() => expect(mocks.getFacts).toHaveBeenLastCalledWith(
      "case-wire-fixture", "2025-05-20T12:00:00.000Z", "2026-10-31T00:00:00.000Z", expect.any(AbortSignal),
    ));
    expect(await screen.findByRole("heading", { name: "采购方主体类型" })).toBeInTheDocument();
    expect(await screen.findByText(/已记录第 2 版/)).toBeInTheDocument();
  });

  it("keeps facts readable but removes write controls when server says the case is read-only", async () => {
    mocks.useCaseSubjects.mockReturnValue({ data: { items: [], legacy_profile: null, can_manage: false }, isError: false, refetch: vi.fn() });
    renderPanel();
    expect(await screen.findByText("当前案件为只读状态，可以查看事实，不能记录新版本。")).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "采购方主体类型" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "追加事实版本" })).not.toBeInTheDocument();
  });

  it("shows immutable paged history and writes with the server head, not the selected business-time version", async () => {
    const user = userEvent.setup();
    const facts = fixture("applicability-fact-set.json") as { facts: Array<{ dimension: string; status: string; value: string; unknown_reason?: string; fact: unknown }> };
    const buyer = facts.facts.find((item) => item.dimension === "buyer_kind");
    if (buyer) Object.assign(buyer, { status: "unknown", value: "", unknown_reason: "missing_fact", fact: null });
    mocks.getFacts.mockResolvedValue(facts);
    renderPanel();
    expect(await screen.findByText("版本历史（当前写入头 v3）")).toBeInTheDocument();
    expect(await screen.findByText(/第 2 版 · 企业/)).toBeInTheDocument();
    const section = screen.getByRole("region", { name: "采购方主体类型" });
    await user.click(within(section).getByRole("button", { name: "下一页历史" }));
    expect(await within(section).findByText(/第 1 版 · 企业/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("采购方主体类型依据说明"), "由本案材料核对，待确认具体性质。");
    await user.click(within(section).getByRole("button", { name: "追加事实版本" }));
    await waitFor(() => expect(mocks.recordFact).toHaveBeenCalled());
    expect(mocks.recordFact.mock.calls[0]?.[1]).toMatchObject({ dimension: "buyer_kind", expected_version: 3, status: "unknown" });
  });

  it("explains when the case has no fixed material versions and a dimension has no fact history", async () => {
    renderPanel();
    expect(await screen.findByText("本案暂无可引用的固定材料版本。")).toBeInTheDocument();
    const heading = await screen.findByRole("heading", { name: "供应方主体类型" });
    const section = heading.closest("section");
    if (!section) throw new Error("供应方主体类型事实区未渲染");
    const history = within(section).getByLabelText("供应方主体类型历史版本");
    expect(await within(history).findByText("当前没有已记录的事实版本。")).toBeInTheDocument();
  });

  it("explains when the selected fixed material version has no parse records", async () => {
    mocks.useCaseDetail.mockReturnValue({
      data: {
        materials: [{ material_id: "material-contract", role: "draft_contract", purpose: "primary_contract" }],
        material_versions: [{ material_id: "material-contract", version: 1 }],
      },
      isError: false,
      isLoading: false,
      refetch: vi.fn(),
    });
    const user = userEvent.setup();
    renderPanel();
    await chooseOption(user, screen.getByRole("combobox", { name: "选择事实依据的材料版本" }), /拟签合同.*主合同.*v1/);
    expect(await screen.findByText("所选材料版本还没有固定解析记录。")).toBeInTheDocument();
  });
});
