// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “case legal applicability panel”; representative assertions cover sends the chosen business and evidence times as UTC from explicit Beijing inputs, keeps facts readable but removes write controls when server says the case is read-only, shows immutable paged history and writes with the server head, not the selected business-time version. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-08
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { parseLegalApplicabilityFactVersion } from "../api/legal-applicability";
import { ApiError } from "../api/http";
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
  mocks.getFacts.mockImplementation(async (_caseId: string, businessAt: string, cutoff: string) => ({ ...(fixture("applicability-fact-set.json") as object), business_at: businessAt, evidence_cutoff_at: cutoff }));
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
  it("preserves edited facts when a background refetch returns", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    render(<QueryClientProvider client={client}><CaseLegalApplicabilityPanel caseId="case-wire-fixture" /></QueryClientProvider>);
    const input = await screen.findByLabelText("采购方主体类型依据说明");
    fireEvent.change(input, { target: { value: "unsaved edit" } });
    const changed = fixture("applicability-fact-set.json") as { facts: Array<{ fact: { basis_note: string } | null }> };
    if (changed.facts[0]?.fact) changed.facts[0].fact.basis_note = "server update";
    mocks.getFacts.mockImplementation(async (_caseId: string, businessAt: string, cutoff: string) => ({ ...changed, business_at: businessAt, evidence_cutoff_at: cutoff }));
    await client.invalidateQueries({ queryKey: ["cases", "case-wire-fixture", "legal-applicability-facts"] });
    await waitFor(() => expect(mocks.getFacts.mock.calls.length).toBeGreaterThan(1));
    expect(input).toHaveValue("unsaved edit");
  });

  it("restores the initial draft after returning to the displayed times", async () => {
    const user = userEvent.setup();
    renderPanel();
    const input = await screen.findByLabelText("采购方主体类型依据说明");
    const business = screen.getByLabelText("法律判断业务时点（北京时间）") as HTMLInputElement;
    const cutoff = screen.getByLabelText("法律事实证据截止时点（北京时间）") as HTMLInputElement;
    const initialBusiness = business.value;
    const initialCutoff = cutoff.value;
    fireEvent.change(input, { target: { value: "initial draft" } });
    fireEvent.change(business, { target: { value: "2025-05-20T20:00" } });
    await user.click(screen.getByRole("button", { name: "读取该时点的事实" }));
    await screen.findByLabelText("采购方主体类型依据说明");
    fireEvent.change(business, { target: { value: initialBusiness } });
    fireEvent.change(cutoff, { target: { value: initialCutoff } });
    await user.click(screen.getByRole("button", { name: "读取该时点的事实" }));
    await waitFor(() => expect(screen.getByLabelText("采购方主体类型依据说明")).toHaveValue("initial draft"));
  });

  it("restores independent drafts for exact business and evidence time snapshots", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByLabelText("采购方主体类型依据说明");
    const business = screen.getByLabelText("法律判断业务时点（北京时间）");
    const cutoff = screen.getByLabelText("法律事实证据截止时点（北京时间）");
    const selectTime = async (businessAt: string, evidenceAt: string) => {
      fireEvent.change(business, { target: { value: businessAt } });
      fireEvent.change(cutoff, { target: { value: evidenceAt } });
      await user.click(screen.getByRole("button", { name: "读取该时点的事实" }));
      await waitFor(() => expect(mocks.getFacts).toHaveBeenLastCalledWith("case-wire-fixture", expect.any(String), expect.any(String), expect.any(AbortSignal)));
      await screen.findByLabelText("采购方主体类型依据说明");
    };
    await selectTime("2025-05-20T20:00", "2025-05-21T20:00");
    fireEvent.change(screen.getByLabelText("采购方主体类型依据说明"), { target: { value: "draft A" } });
    await selectTime("2025-05-20T20:00", "2025-05-22T20:00");
    expect(screen.getByLabelText("采购方主体类型依据说明")).not.toHaveValue("draft A");
    fireEvent.change(screen.getByLabelText("采购方主体类型依据说明"), { target: { value: "draft B" } });
    await selectTime("2025-05-20T20:00", "2025-05-21T20:00");
    await waitFor(() => expect(screen.getByLabelText("采购方主体类型依据说明")).toHaveValue("draft A"));
    await selectTime("2025-05-20T20:00", "2025-05-22T20:00");
    await waitFor(() => expect(screen.getByLabelText("采购方主体类型依据说明")).toHaveValue("draft B"));
  });

  it("restores the selected evidence anchor only for its time snapshot", async () => {
    const user = userEvent.setup();
    mocks.useMaterialParsedTextPage.mockReturnValue({ data: { material_id: "m1", material_version: 1, document_id: "d1", parsed_document_id: "p1", filename: "evidence.txt", offset: 0, text: "selected evidence", total_runes: 17, next_offset: null }, isError: false });
    renderPanel();
    const text = await screen.findByLabelText("法律适用事实依据原文，可选择引用");
    const range = document.createRange();
    range.setStart(text.firstChild!, 0);
    range.setEnd(text.firstChild!, 8);
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
    fireEvent.mouseUp(text);
    expect(screen.getByText("selected", { selector: "blockquote" })).toBeInTheDocument();
    const business = screen.getByLabelText("法律判断业务时点（北京时间）") as HTMLInputElement;
    const initial = business.value;
    fireEvent.change(business, { target: { value: "2025-05-20T20:00" } });
    await user.click(screen.getByRole("button", { name: "读取该时点的事实" }));
    expect(screen.queryByText("selected", { selector: "blockquote" })).not.toBeInTheDocument();
    fireEvent.change(business, { target: { value: initial } });
    await user.click(screen.getByRole("button", { name: "读取该时点的事实" }));
    expect(screen.getByText("selected", { selector: "blockquote" })).toBeInTheDocument();
  });

  it("discards drafts when the same actor changes role", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    const panel = render(<QueryClientProvider client={client}><CaseLegalApplicabilityPanel caseId="case-wire-fixture" /></QueryClientProvider>);
    fireEvent.change(await screen.findByLabelText("采购方主体类型依据说明"), { target: { value: "old role draft" } });
    mocks.useSession.mockReturnValue({ data: { id: "actor-1", tenant_id: "tenant-1", org_id: "org-1", audience: "client", role: "reviewer" } });
    panel.rerender(<QueryClientProvider client={client}><CaseLegalApplicabilityPanel caseId="case-wire-fixture" /></QueryClientProvider>);
    await waitFor(() => expect(screen.getByLabelText("采购方主体类型依据说明")).not.toHaveValue("old role draft"));
  });

  it.each(["session", "case", "subjects", "facts", "history", "parsedDocuments", "parsedPage"])("hides protected data when %s is forbidden", async (source) => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    const panel = render(<QueryClientProvider client={client}><CaseLegalApplicabilityPanel caseId="case-wire-fixture" /></QueryClientProvider>);
    expect(await screen.findByLabelText("采购方主体类型依据说明")).toBeInTheDocument();
    const error = new ApiError("denied", 403, "forbidden", false, null);
    if (source === "session") mocks.useSession.mockReturnValue({ data: { id: "actor-1" }, error, isError: true });
    if (source === "case") mocks.useCaseDetail.mockReturnValue({ data: { materials: [], material_versions: [] }, error, isError: true });
    if (source === "subjects") mocks.useCaseSubjects.mockReturnValue({ data: { items: [], can_manage: true }, error, isError: true });
    if (source === "parsedDocuments") mocks.useMaterialParsedDocuments.mockReturnValue({ data: { items: [] }, error, isError: true });
    if (source === "parsedPage") mocks.useMaterialParsedTextPage.mockReturnValue({ data: { text: "stale text" }, error, isError: true });
    if (source === "facts") mocks.getFacts.mockRejectedValue(error);
    if (source === "history") mocks.getHistory.mockRejectedValue(error);
    if (source === "facts") await client.invalidateQueries({ queryKey: ["cases", "case-wire-fixture", "legal-applicability-facts"] });
    else if (source === "history") await client.invalidateQueries({ queryKey: ["cases", "case-wire-fixture", "actor-1", "legal-applicability-history"] });
    else panel.rerender(<QueryClientProvider client={client}><CaseLegalApplicabilityPanel caseId="case-wire-fixture" /></QueryClientProvider>);
    expect(await screen.findByText("当前无权查看法律适用事实")).toBeInTheDocument();
    expect(screen.queryByLabelText("采购方主体类型依据说明")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "追加事实版本" })).not.toBeInTheDocument();
  });

  it("clears drafts when a save is forbidden", async () => {
    const user = userEvent.setup();
    const facts = fixture("applicability-fact-set.json") as { facts: Array<{ dimension: string; fact: unknown }> };
    const buyer = facts.facts.find((item) => item.dimension === "buyer_kind");
    if (buyer) buyer.fact = null;
    mocks.getFacts.mockImplementation(async (_caseId: string, businessAt: string, cutoff: string) => ({ ...facts, business_at: businessAt, evidence_cutoff_at: cutoff }));
    mocks.recordFact.mockRejectedValue(new ApiError("denied", 403, "forbidden", false, null));
    renderPanel();
    const input = await screen.findByLabelText("采购方主体类型依据说明");
    fireEvent.change(input, { target: { value: "restricted draft" } });
    await user.click(within(screen.getByRole("region", { name: "采购方主体类型" })).getByRole("button", { name: "追加事实版本" }));
    expect(await screen.findByText("当前无权查看法律适用事实")).toBeInTheDocument();
    expect(screen.queryByLabelText("采购方主体类型依据说明")).not.toBeInTheDocument();
  });

  it("ignores a save completing after a time change", async () => {
    const user = userEvent.setup();
    const facts = fixture("applicability-fact-set.json") as { facts: Array<{ dimension: string; fact: unknown }> };
    const buyer = facts.facts.find((item) => item.dimension === "buyer_kind");
    if (buyer) buyer.fact = null;
    mocks.getFacts.mockImplementation(async (_caseId: string, businessAt: string, cutoff: string) => ({ ...facts, business_at: businessAt, evidence_cutoff_at: cutoff }));
    let finish!: (value: unknown) => void;
    mocks.recordFact.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    renderPanel();
    const input = await screen.findByLabelText("采购方主体类型依据说明");
    fireEvent.change(input, { target: { value: "draft before save" } });
    await user.click(within(screen.getByRole("region", { name: "采购方主体类型" })).getByRole("button", { name: "追加事实版本" }));
    await waitFor(() => expect(mocks.recordFact).toHaveBeenCalled());
    fireEvent.change(screen.getByLabelText("法律判断业务时点（北京时间）"), { target: { value: "2025-05-20T20:00" } });
    await user.click(screen.getByRole("button", { name: "读取该时点的事实" }));
    finish(parseLegalApplicabilityFactVersion(fixture("applicability-record-success.json")));
    await waitFor(() => expect(screen.getByLabelText("采购方主体类型依据说明")).not.toHaveValue("draft before save"));
    expect(screen.queryByText(/已追加为第/)).not.toBeInTheDocument();
    await waitFor(() => expect(mocks.clearPendingMutationKey).toHaveBeenCalledWith("safe-slot"));
    await waitFor(() => expect(mocks.getHistory.mock.calls.length).toBeGreaterThan(9));
  });

  it("settles a completed save without invalidating a replacement actor's caches", async () => {
    const user = userEvent.setup();
    const facts = fixture("applicability-fact-set.json") as { facts: Array<{ dimension: string; fact: unknown }> };
    const buyer = facts.facts.find((item) => item.dimension === "buyer_kind");
    if (buyer) buyer.fact = null;
    mocks.getFacts.mockImplementation(async (_caseId: string, businessAt: string, cutoff: string) => ({ ...facts, business_at: businessAt, evidence_cutoff_at: cutoff }));
    let finish!: (value: unknown) => void;
    mocks.recordFact.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    const invalidate = vi.spyOn(client, "invalidateQueries");
    const panel = render(<QueryClientProvider client={client}><CaseLegalApplicabilityPanel caseId="case-wire-fixture" /></QueryClientProvider>);
    fireEvent.change(await screen.findByLabelText("采购方主体类型依据说明"), { target: { value: "old actor draft" } });
    await user.click(within(screen.getByRole("region", { name: "采购方主体类型" })).getByRole("button", { name: "追加事实版本" }));
    await waitFor(() => expect(mocks.recordFact).toHaveBeenCalled());
    mocks.useSession.mockReturnValue({ data: { id: "actor-2", tenant_id: "tenant-1", org_id: "org-1", audience: "client" } });
    panel.rerender(<QueryClientProvider client={client}><CaseLegalApplicabilityPanel caseId="case-wire-fixture" /></QueryClientProvider>);
    finish(parseLegalApplicabilityFactVersion(fixture("applicability-record-success.json")));
    await waitFor(() => expect(mocks.clearPendingMutationKey).toHaveBeenCalledWith("safe-slot"));
    expect(invalidate).not.toHaveBeenCalled();
    expect(screen.queryByText(/已追加为第/)).not.toBeInTheDocument();
  });

  it("rejects fact responses for a different time", async () => {
    mocks.getFacts.mockResolvedValue(fixture("applicability-fact-set.json"));
    renderPanel();
    expect(await screen.findByText("法律适用事实暂不可用")).toBeInTheDocument();
    expect(screen.queryByLabelText("采购方主体类型依据说明")).not.toBeInTheDocument();
  });

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
    mocks.getFacts.mockImplementation(async (_caseId: string, businessAt: string, cutoff: string) => ({ ...facts, business_at: businessAt, evidence_cutoff_at: cutoff }));
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
