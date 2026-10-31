import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parseAssociationPreview, parseServiceObjectAssociationPage, parseServiceObjectDetail, parseServiceObjectList, parseServiceObjectMutationResult, parseServiceObjectVersionPage } from "./service-objects-api";
import { ServiceObjectPanel } from "./service-object-panel";
import { renderWithAntd as render } from "../../test/antd";

const mocks = vi.hoisted(() => ({
  list: vi.fn(), detail: vi.fn(), versions: vi.fn(), associations: vi.fn(), preview: vi.fn(), create: vi.fn(), confirm: vi.fn(), review: vi.fn(),
}));
vi.mock("./service-objects-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./service-objects-api")>();
  return { ...actual, listServiceObjects: mocks.list, getServiceObject: mocks.detail, listServiceObjectVersions: mocks.versions, listServiceObjectAssociations: mocks.associations, getAssociationCandidates: mocks.preview, createServiceObject: mocks.create, confirmServiceObjectAssociation: mocks.confirm };
});
vi.mock("../../api/endpoints", () => ({ getReview: mocks.review }));

function fixture(): Record<string, unknown> {
  return JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/contracts/service-objects/wire.json"), "utf8")) as Record<string, unknown>;
}
function caseDetail() {
  return {
    id: "case-1",
    title: "示例案件",
    contract_type: "service",
    created_at: "2026-10-31T08:00:00Z",
    updated_at: "2026-10-31T08:00:00Z",
    documents: [],
    materials: [{ material_id: "material-wire-2", role: "draft_contract" as const, purpose: "primary_contract" as const, legacy_status: "native" as const, legacy_selected: true, selected: true, current_document_id: "doc-wire-2", current_version: 3 }],
    material_versions: [{ material_id: "material-wire-2", version: 3, document_id: "doc-wire-2", previous_document_id: null, created_at: "2026-10-31T08:00:00Z" }],
    members: [],
    latest_run: null,
    finding_counts: null,
    activity: [],
    business_line: "",
    scenario: "",
    business_status: "",
    generations: [],
    remediations: [],
    reports: [],
    archives: [],
    archived_at: null,
  };
}
function setup(entry: string, options: { association?: boolean; pagedHistory?: boolean } = {}) {
  const raw = fixture();
  const listPage = parseServiceObjectList(raw.summary_page);
  const detailRaw = structuredClone(raw.detail) as Record<string, unknown>;
  if (!options.association) {
    detailRaw.association = null;
    detailRaw.current_version = 2;
    const identity = detailRaw.identity as Record<string, unknown>;
    identity.state = "pending_confirmation";
  }
  mocks.list.mockResolvedValue({ ...listPage, can_manage: true });
  mocks.detail.mockResolvedValue(parseServiceObjectDetail(detailRaw));
  const versionPage = structuredClone(raw.version_page) as { items: Record<string, unknown>[]; next_cursor: string | null };
  const assocPage = structuredClone(raw.association_page) as { items: Record<string, unknown>[]; next_cursor: string | null };
  if (options.pagedHistory) {
    versionPage.next_cursor = "versions-cursor";
    assocPage.next_cursor = "associations-cursor";
    assocPage.items[0] = { ...assocPage.items[0]!, association_version: 2 };
  }
  mocks.versions.mockImplementation(async (_caseId: string, _objectId: string, cursor: string | null) => {
    if (cursor === "versions-cursor") return { ...parseServiceObjectVersionPage(raw.version_page), items: [{ ...parseServiceObjectVersionPage(raw.version_page).items[0]!, version: 1, display_name: "专线历史名称" }], next_cursor: null };
    return parseServiceObjectVersionPage(versionPage);
  });
  mocks.associations.mockImplementation(async (_caseId: string, _objectId: string, cursor: string | null) => {
    if (cursor === "associations-cursor") return { ...parseServiceObjectAssociationPage(raw.association_page), items: [{ ...parseServiceObjectAssociationPage(raw.association_page).items[0]!, association_version: 1 }], next_cursor: null };
    return parseServiceObjectAssociationPage(assocPage);
  });
  const previewRaw = structuredClone(raw.preview) as Record<string, unknown>;
  previewRaw.expected_object_version = 2;
  previewRaw.expected_association_version = 0;
  mocks.preview.mockResolvedValue(parseAssociationPreview(previewRaw));
  mocks.create.mockResolvedValue(parseServiceObjectMutationResult(raw.mutation_result));
  mocks.confirm.mockResolvedValue(parseServiceObjectMutationResult(raw.mutation_result));
  mocks.review.mockResolvedValue({
    run: { id: "run-wire-2", case_id: "case-1" },
    review_attempts: {
      attempt: { id: "attempt-wire-2", run_id: "run-wire-2", ordinal: 2, state: "completed", accepted_at: "2026-10-31T08:00:00Z", started_at: null, finished_at: null, retry_of_attempt_id: null, parsed_document_ids: [], input_hash: null, output_hash: null, error_code: null, history_kind: "frozen" },
      attempts: [{ id: "attempt-wire-2", run_id: "run-wire-2", ordinal: 2, state: "completed", accepted_at: "2026-10-31T08:00:00Z", started_at: null, finished_at: null, retry_of_attempt_id: null, parsed_document_ids: [], input_hash: null, output_hash: null, error_code: null, history_kind: "frozen" }],
    },
    documents: [{ id: "doc-wire-2", role: "draft_contract", filename: "主合同.docx", parsed_document_id: "parse-wire-2" }],
  });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={[entry]}><ServiceObjectPanel
    caseId="case-1"
    initialRunId="run-wire-2"
    initialAttemptId="attempt-wire-2"
    caseDetail={caseDetail()}
  /></MemoryRouter></QueryClientProvider>);
}

afterEach(() => vi.clearAllMocks());

describe("service object panel", () => {
  it("restores the selected object from the URL and confirms its first frozen fact association with server versions", async () => {
    const raw = fixture();
    setup("/cases/case-1/operator-audit?service_object_id=svc-wire-001&run_id=run-wire-2&attempt_id=attempt-wire-2");
    expect(await screen.findByRole("heading", { name: "专线 A · 身份第 2 版" })).toBeInTheDocument();
    fireEvent.change(await screen.findByLabelText("冻结服务对象候选"), { target: { value: "LINE-A" } });
    expect(await screen.findByText("最低保障带宽为100 Mbit/s")).toBeInTheDocument();
    expect(mocks.detail).toHaveBeenCalledWith("case-1", "svc-wire-001", expect.any(AbortSignal));
    expect(mocks.preview).toHaveBeenCalledWith("case-1", "svc-wire-001", expect.objectContaining({
      run_id: "run-wire-2", attempt_id: "attempt-wire-2", material_id: "material-wire-2", material_version: 3, document_id: "doc-wire-2", parsed_document_id: "parse-wire-2",
    }), expect.any(AbortSignal));

    fireEvent.click(screen.getByRole("checkbox", { name: /我已核对/ }));
    fireEvent.click(screen.getByRole("button", { name: "确认首个冻结事实关联" }));
    await waitFor(() => expect(mocks.confirm).toHaveBeenCalledWith("case-1", "svc-wire-001", expect.objectContaining({
      confirmed: true, expected_object_version: 2, expected_association_version: 0,
      source: expect.objectContaining({ run_id: "run-wire-2", attempt_id: "attempt-wire-2", material_id: "material-wire-2", material_version: 3, document_id: "doc-wire-2", parsed_document_id: "parse-wire-2" }),
      object_ref: "LINE-A", facts_sha256: "d".repeat(64), candidate_sha256: "f".repeat(64),
    }), expect.any(String)));
    expect(screen.getByRole("status")).toHaveTextContent("首个事实关联已确认");
    expect(raw.mutation_result).toBeDefined();
  });

  it("paginates immutable identity and association histories", async () => {
    setup("/cases/case-1/operator-audit?service_object_id=svc-wire-001", { association: true, pagedHistory: true });
    expect(await screen.findByRole("heading", { name: "专线 A · 身份第 2 版" })).toBeInTheDocument();
    fireEvent.click(screen.getByText("身份版本历史"));
    fireEvent.click(screen.getByRole("button", { name: "读取更早身份版本" }));
    expect(await screen.findByText(/专线历史名称/)).toBeInTheDocument();
    fireEvent.click(screen.getByText("冻结事实关联历史"));
    fireEvent.click(screen.getByRole("button", { name: "读取更早事实关联" }));
    await waitFor(() => expect(mocks.associations).toHaveBeenCalledWith("case-1", "svc-wire-001", "associations-cursor", expect.any(AbortSignal)));
  });

  it("does not open registration controls when the empty list is not manageable", async () => {
    mocks.list.mockResolvedValue({ items: [], next_cursor: null, can_manage: false });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    render(<QueryClientProvider client={queryClient}><MemoryRouter><ServiceObjectPanel caseId="case-1" initialRunId="" initialAttemptId={null} caseDetail={undefined} /></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByText("本案尚未登记服务对象。")).toBeInTheDocument();
    expect(screen.getByText(/服务端没有返回空列表写入权限/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "登记待确认服务对象" })).not.toBeInTheDocument();
  });
});
