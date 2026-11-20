// File metadata (manifest mirror):
// Purpose: Checks contract desk filtering, distinct source versions, authorized frozen-document matches, case-material fallback and return through ordered results.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-19
// Updated: 2026-11-19
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider, useLocation } from "react-router";
import { beforeEach, expect, it, vi } from "vitest";
import { getCase, getReview, listMaterialParsedDocuments } from "../api/endpoints";
import type {
  Actor,
  CaseDetail,
  ExecutionAttempt,
  ReviewWorkspace,
  WorkbenchDocument,
  WorkbenchSnapshot,
} from "../api/types";
import { actorReadContextKey } from "../domain/actor-read-context";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import {
  ContractDeskPage,
  ContractDeskSources,
  contractDeskReturnHref,
} from "./contract-desk-page";

const api = vi.hoisted(() => ({ snapshot: null as WorkbenchSnapshot | null }));
const actor = {
  id: "reviewer",
  audience: "client",
  role: "reviewer",
  tenant_id: "tenant",
  org_id: "org",
} as Actor;
vi.mock("../api/endpoints", async (original) => ({
  ...(await original<typeof import("../api/endpoints")>()),
  getCase: vi.fn(),
  getReview: vi.fn(),
  listMaterialParsedDocuments: vi.fn(),
}));
vi.mock("../api/queries", async (original) => ({
  ...(await original<typeof import("../api/queries")>()),
  useSession: () => ({ data: actor, isSuccess: true, isFetching: false }),
  useWorkbench: (_period: string, scope: string) => ({
    data: scope ? api.snapshot : null,
    isSuccess: Boolean(scope),
    isPending: !scope,
    isFetching: false,
  }),
}));

const instant = "2026-11-18T08:30:00Z";
const caseRecord = {
  id: "case/a",
  title: "在网时长项目合作",
  contract_type: "generic",
  document_count: 2,
  pending_decisions: 0,
  latest_run: null,
  updated_at: instant,
  profile: {
    case_id: "case/a",
    number: "CASE-026",
    customer: "",
    supplier: "",
    contract_category: "",
    owner_id: "reviewer",
    due_at: null,
    version: 1,
    updated_at: instant,
  },
  severity: null,
  progress: null,
} satisfies WorkbenchSnapshot["cases"][number];

function document(id: string, overrides: Partial<WorkbenchDocument> = {}): WorkbenchDocument {
  return {
    id,
    case_id: caseRecord.id,
    case_title: caseRecord.title,
    role: "draft_contract",
    filename: "review-upload.docx",
    media_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    digest: "f".repeat(64),
    size: 104,
    parse_status: "ready",
    parse_error: null,
    created_at: instant,
    parsed_document: null,
    ...overrides,
  };
}

const run = {
  id: "run/one",
  case_id: caseRecord.id,
  case_title: caseRecord.title,
  state: "completed",
  created_at: instant,
  updated_at: instant,
  attempt_id: "attempt/one",
  started_at: instant,
  finished_at: instant,
  usage: null,
} satisfies WorkbenchSnapshot["runs"][number];

const attempt = {
  id: run.attempt_id,
  run_id: run.id,
  ordinal: 1,
  retry_of_attempt_id: null,
  state: "completed",
  accepted_at: instant,
  started_at: instant,
  finished_at: instant,
  parsed_document_ids: ["parse/one"],
  input_hash: null,
  output_hash: null,
  error_code: null,
  history_kind: "frozen",
} satisfies ExecutionAttempt;

function review(frozen: WorkbenchDocument): ReviewWorkspace {
  return {
    run: { ...run, model_ref: null, partial_reason: null, failure_message: null },
    documents: [
      {
        ...frozen,
        digest: "",
        parsed_document_id: "parse/one",
        text: "合成冻结段落。",
        clauses: [],
      },
    ],
    findings: [],
    review_attempts: { attempt, attempts: [attempt] },
    execution: null,
    audit_events: [],
  };
}

function caseDetailFor(item: WorkbenchDocument, parsedId = "parse/one"): CaseDetail {
  return {
    id: item.case_id,
    title: item.case_title,
    contract_type: "generic",
    created_at: instant,
    updated_at: instant,
    documents: [
      {
        ...item,
        parsed_document: {
          id: parsedId,
          document_id: item.id,
          original_digest: item.digest,
          parser_id: "test-parser",
          parser_version: "1",
          config_hash: "",
          normalization_version: "",
          normalized_digest: null,
          ir_digest: null,
          artifact_digest: "",
          created_at: instant,
          status: "ready",
        },
      },
    ],
    material_versions: [
      {
        document_id: item.id,
        material_id: "material/one",
        version: 2,
        previous_document_id: null,
        created_at: instant,
      },
    ],
    members: [],
    latest_run: null,
    finding_counts: null,
    activity: [],
    business_line: "",
    scenario: "",
    business_status: "active",
    generations: [],
    remediations: [],
    reports: [],
    archives: [],
    archived_at: null,
  };
}

function Registration() {
  const { state } = useLocation();
  return (
    <ContractDeskSources
      active="registered"
      materialsHref={contractDeskReturnHref(state, actorReadContextKey(actor))}
    />
  );
}

function openPage(path = "/contract-desk?page=1") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter(
    [
      { path: "/contract-desk", element: <ContractDeskPage /> },
      { path: "/contracts", element: <Registration /> },
      { path: "/cases", element: <h1>案件</h1> },
      { path: "/cases/:caseId", element: <div>案件材料入口</div> },
      { path: "/cases/:caseId/reviews/:runId", element: <div>冻结原文入口</div> },
    ],
    { initialEntries: [path] },
  );
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

it("replaces bare legacy contract desk visits with the cases page", async () => {
  const router = openPage("/contract-desk");
  expect(await screen.findByRole("heading", { name: "案件" })).toBeVisible();
  expect(router.state.location.pathname).toBe("/cases");
  expect(router.state.historyAction).toBe("REPLACE");
});

beforeEach(() => {
  vi.resetAllMocks();
  api.snapshot = {
    schema_version: "workbench/v1",
    generated_at: instant,
    period: "week",
    dashboard: {
      visible_cases: 2,
      my_pending_tasks: 0,
      overdue_tasks: 0,
      pending_review_cases: 0,
      remediation_cases: 0,
      completed_reviews: 0,
      average_duration_ms: null,
      progress: [],
      risks: [],
    },
    cases: [caseRecord, { ...caseRecord, id: "case/b", title: "第二个案件", profile: null }],
    profiles: [],
    tasks: [
      {
        id: "task",
        case_id: caseRecord.id,
        title: "补充材料",
        kind: "decision",
        assignee_id: "reviewer",
        due_at: null,
        status: "open",
        priority: "medium",
        finding_id: null,
        version: 1,
        created_by: null,
        created_at: instant,
        updated_at: instant,
      },
    ],
    relations: [],
    knowledge: [],
    members: [
      {
        id: "reviewer",
        display_name: "张审核",
        role: "reviewer",
        tenant_id: "tenant",
        org_id: "org",
      },
    ],
    remediations: [],
    reports: [],
    documents: [
      document("document/a"),
      document("document/b", { case_id: "case/b" }),
      document("document/old", { filename: "review-previous.docx", superseded: true }),
      document("document/other", { filename: "procurement.pdf", role: "procurement" }),
    ],
    runs: [run],
    audit: [],
    templates: [],
    model: { name: "", ocr_name: "", status: "unconfigured", label: "" },
    tools: [],
  };
});

it("searches real filenames and case titles, keeps same-name files separate, and restores filters across sources", async () => {
  const router = openPage();
  expect(screen.getAllByRole("link", { name: "查看材料：review-upload.docx" })).toHaveLength(2);
  expect(screen.queryByText("procurement.pdf")).not.toBeInTheDocument();
  expect(screen.queryByText("review-previous.docx")).not.toBeInTheDocument();
  expect(screen.getByText("张审核")).toBeInTheDocument();
  fireEvent.change(screen.getByRole("searchbox"), { target: { value: "CASE-026" } });
  expect(screen.getAllByRole("link", { name: "查看材料：review-upload.docx" })).toHaveLength(1);
  fireEvent.change(screen.getByRole("searchbox"), { target: { value: "在网时长" } });
  expect(screen.getAllByRole("link", { name: "查看材料：review-upload.docx" })).toHaveLength(1);
  await chooseOption(
    userEvent.setup(),
    screen.getByRole("combobox", { name: "材料状态" }),
    "已解析",
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "我有待办的案件" }));
  fireEvent.click(screen.getByRole("link", { name: "已登记合同" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/contracts"));
  fireEvent.click(screen.getByRole("link", { name: "审查材料" }));
  await waitFor(() => expect(screen.getByRole("searchbox")).toHaveValue("在网时长"));
  expect(new URLSearchParams(router.state.location.search).get("status")).toBe("ready");
  expect(new URLSearchParams(router.state.location.search).get("mine")).toBe("1");
  fireEvent.change(screen.getByRole("searchbox"), { target: { value: "review-upload.docx" } });
  expect(screen.getAllByRole("link", { name: "查看材料：review-upload.docx" })).toHaveLength(1);
  fireEvent.click(screen.getByRole("checkbox", { name: "我有待办的案件" }));
  expect(screen.getAllByRole("link", { name: "查看材料：review-upload.docx" })).toHaveLength(2);
  expect(getReview).not.toHaveBeenCalled();
});

it("opens only the matched frozen document and resolves adjacent positions across pages", async () => {
  api.snapshot!.documents = Array.from({ length: 12 }, (_, index) =>
    document(`document/${String(index).padStart(2, "0")}`, { filename: `upload-${index}.docx` }),
  );
  const selected = api.snapshot!.documents[10]!;
  vi.mocked(getReview).mockResolvedValue(review(selected));
  vi.mocked(getCase).mockResolvedValue(caseDetailFor(selected));
  const router = openPage("/contract-desk?q=upload&page=2");
  expect(screen.queryByText("upload-9.docx")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("link", { name: "查看材料：upload-10.docx" }));
  expect(await screen.findByText("冻结原文入口")).toBeInTheDocument();
  expect(getReview).toHaveBeenCalledExactlyOnceWith(
    caseRecord.id,
    run.id,
    run.attempt_id,
    expect.any(AbortSignal),
  );
  expect(getCase).toHaveBeenCalledExactlyOnceWith(caseRecord.id, expect.any(AbortSignal));
  expect(listMaterialParsedDocuments).not.toHaveBeenCalled();
  expect(router.state.location.pathname).toBe("/cases/case%2Fa/reviews/run%2Fone");
  expect(Object.fromEntries(new URLSearchParams(router.state.location.search))).toEqual({
    attempt_id: run.attempt_id,
    source_document: "document/10",
    review_section: "document",
  });
  expect(router.state.location.state).toEqual({
    contractDesk: {
      actorScope: actorReadContextKey(actor),
      returnTo: "/contract-desk?q=upload&page=2",
      documentId: "document/10",
      previousHref: "/contract-desk?q=upload&open_material=document%2F09",
      nextHref: "/contract-desk?q=upload&page=2&open_material=document%2F11",
    },
  });
});

it("uses the proper case materials when the attempt has another document or no attempt exists", async () => {
  vi.mocked(getReview).mockResolvedValue(review(document("different-document")));
  vi.mocked(getCase).mockResolvedValue(caseDetailFor(document("document/a")));
  const router = openPage("/contract-desk?q=在网时长");
  fireEvent.click(screen.getByRole("link", { name: "查看材料：review-upload.docx" }));
  expect(await screen.findByText("案件材料入口")).toBeInTheDocument();
  expect(router.state.location).toMatchObject({
    pathname: "/cases/case%2Fa",
    hash: "#case-material-source",
  });
  expect(new URLSearchParams(router.state.location.search).get("source_document")).toBe(
    "document/a",
  );
  expect(getReview).toHaveBeenCalledTimes(1);
  await act(() => router.navigate("/contract-desk?q=第二个案件"));
  fireEvent.click(await screen.findByRole("link", { name: "查看材料：review-upload.docx" }));
  expect(await screen.findByText("案件材料入口")).toBeInTheDocument();
  expect(router.state.location).toMatchObject({
    pathname: "/cases/case%2Fb",
    hash: "#case-material-source",
  });
  expect(getReview).toHaveBeenCalledTimes(1);
});

it("restores the historical filter from a share URL and keeps the exact version on case fallback", async () => {
  vi.mocked(getReview).mockResolvedValue(review(document("document/a")));
  vi.mocked(getCase).mockResolvedValue(caseDetailFor(document("document/old")));
  const router = openPage("/contract-desk?status=superseded&mine=1");
  expect(screen.queryByText("review-upload.docx")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("link", { name: "查看材料：review-previous.docx" }));
  expect(await screen.findByText("案件材料入口")).toBeInTheDocument();
  expect(new URLSearchParams(router.state.location.search).get("source_document")).toBe(
    "document/old",
  );
  expect(contractDeskReturnHref(router.state.location.state, actorReadContextKey(actor))).toBe(
    "/contract-desk?status=superseded&mine=1",
  );
});

it.each([
  { historyDigest: "f".repeat(64), expected: "冻结原文入口" },
  { historyDigest: "e".repeat(64), expected: "案件材料入口" },
])(
  "checks the pinned historical parse digest before opening ($expected)",
  async ({ historyDigest, expected }) => {
    const item = document("document/a");
    vi.mocked(getReview).mockResolvedValue(review(item));
    vi.mocked(getCase).mockResolvedValue(caseDetailFor(item, "parse/reparsed"));
    vi.mocked(listMaterialParsedDocuments).mockResolvedValue({
      material_id: "material/one",
      material_version: 2,
      document_id: item.id,
      items: [
        {
          parsed_document_id: "parse/one",
          created_at: instant,
          original_digest: historyDigest,
          normalized_digest: null,
          parser_id: "test-parser",
          parser_version: "1",
          status: "ready",
        },
      ],
    });
    openPage("/contract-desk?q=在网时长");
    fireEvent.click(screen.getByRole("link", { name: "查看材料：review-upload.docx" }));
    expect(await screen.findByText(expected)).toBeInTheDocument();
    expect(listMaterialParsedDocuments).toHaveBeenCalledExactlyOnceWith(
      caseRecord.id,
      "material/one",
      2,
      expect.any(AbortSignal),
    );
  },
);
