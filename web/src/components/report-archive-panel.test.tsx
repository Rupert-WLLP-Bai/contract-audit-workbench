import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import { parseReviewWorkspace } from "../api/parse";
import type { CaseDetail } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { ReportArchivePanel } from "./report-archive-panel";

const at = "2026-10-21T06:00:00Z";
const clients: QueryClient[] = [];
const detail: CaseDetail = {
  id: "case-1",
  title: "合同",
  contract_type: "service",
  created_at: at,
  updated_at: at,
  documents: [],
  members: [],
  latest_run: null,
  finding_counts: null,
  activity: [],
  business_line: "ENT",
  scenario: "service",
  business_status: "active",
  generations: [],
  remediations: [],
  reports: [],
  archives: [],
  archived_at: null,
};

function publicAttempt(id: string, ordinal: number, state: string) {
  return {
    id,
    run_id: "run-1",
    ordinal,
    retry_of_attempt_id: ordinal === 1 ? null : "attempt-1",
    state,
    accepted_at: at,
    started_at: at,
    finished_at: state === "running" ? null : at,
    parsed_document_ids: [],
    history_kind: "frozen",
  };
}

function publicWorkspace(selectedId = "attempt-2", state = "completed") {
  const first = publicAttempt(
    "attempt-1",
    1,
    selectedId === "attempt-1" ? state : "failed",
  );
  const second = publicAttempt(
    "attempt-2",
    2,
    selectedId === "attempt-2" ? state : "completed",
  );
  return {
    run: {
      id: "run-1",
      case_id: "case-1",
      state,
      updated_at: at,
      started_at: at,
    },
    documents: [
      {
        id: "document-1",
        role: "draft_contract",
        filename: "合同正文.txt",
        parsed_document: null,
        parsed_document_id: null,
        text: null,
        clauses: [],
      },
    ],
    findings: [],
    review_attempts: {
      attempt: selectedId === "attempt-1" ? first : second,
      attempts: [first, second],
    },
    execution: null,
    snapshot: { document_ids: ["document-1"] },
    evaluations: [],
    audit_events: [],
    business_events: [],
  };
}

function open(
  response: unknown = publicWorkspace(),
  historyWorkspace = parseReviewWorkspace(publicWorkspace()),
  caseDetail: CaseDetail = detail,
) {
  const requests: { url: URL; init?: RequestInit }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://workflow.test");
      requests.push({ url, init });
      if (
        url.pathname === "/api/cases/case-1/reviews/run-1" &&
        url.searchParams.get("attempt_id") === "attempt-2"
      ) {
        return Response.json(response);
      }
      if (
        url.pathname === "/api/cases/case-1/reports" &&
        init?.method === "POST"
      ) {
        return Response.json({
          id: "report-1",
          version: 1,
          run_id: "run-1",
          attempt_id: "attempt-2",
          input_hash: "report-input",
          output_hash: "report-output",
          decision_snapshot_hash: "report-decisions",
          digest: "report-digest",
          created_at: at,
        });
      }
      return Response.json(
        { error: { message: "Unexpected request" } },
        { status: 404 },
      );
    }),
  );
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <ReportArchivePanel
          caseId="case-1"
          detail={caseDetail}
          history={[{ runId: "run-1", workspace: historyWorkspace }]}
          historyLoading={false}
          historyError={null}
          canCreateReport
          canArchive={false}
          canReadReports
          archived={false}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { requests, user: userEvent.setup() };
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("report creation with public attempt history", () => {
  it("creates a report for the selected terminal attempt without diagnostics", async () => {
    const { requests, user } = open();
    expect(
      await screen.findByText("第 2 次"),
    ).toBeInTheDocument();
    const form = screen
      .getByRole("button", { name: "生成冻结报告" })
      .closest("form")!;
    expect(within(form).queryByText("输入摘要")).not.toBeInTheDocument();
    expect(within(form).getByText("合同正文.txt")).toBeInTheDocument();
    expect(form).not.toHaveTextContent("run-1");
    expect(form).not.toHaveTextContent("attempt-2");
    expect(form).not.toHaveTextContent("document-1");
    await waitFor(() =>
      expect(within(form).getByRole("button", { name: "生成冻结报告" })).toBeEnabled(),
    );
    await user.click(within(form).getByRole("button", { name: "生成冻结报告" }));
    await waitFor(() =>
      expect(requests.some(({ init }) => init?.method === "POST")).toBe(true),
    );
    const request = requests.find(({ init }) => init?.method === "POST")!;
    expect(request.url.pathname).toBe("/api/cases/case-1/reports");
    expect(JSON.parse(String(request.init?.body))).toEqual({
      run_id: "run-1",
      attempt_id: "attempt-2",
    });
    expect(await screen.findByText("报告已创建，版本 1。")).toBeInTheDocument();
  });

  it("shows report and archive versions without technical identifiers or hashes", () => {
    open(publicWorkspace(), parseReviewWorkspace(publicWorkspace()), {
      ...detail,
      reports: [{
        id: "report-private",
        version: 3,
        run_id: "run-private",
        attempt_id: "attempt-private",
        input_hash: "input-private",
        output_hash: "output-private",
        decision_snapshot_hash: "decision-private",
        digest: "digest-private",
        created_at: at,
      }],
      archives: [{
        id: "archive-private",
        case_id: "case-private",
        report_id: "report-private",
        reason: "审批完成",
        archived_at: at,
      }],
    });
    const panel = screen.getByRole("heading", { name: "冻结报告与归档" }).closest("section")!;
    expect(within(panel).getByText("报告版本 3")).toBeInTheDocument();
    expect(within(panel).getByText("审批完成")).toBeInTheDocument();
    expect(within(panel).getByRole("link", { name: "读取冻结 HTML" })).toHaveAttribute(
      "href",
      "/api/cases/case-1/reports/report-private?format=html",
    );
    for (const hidden of [
      "report-private", "run-private", "attempt-private", "input-private",
      "output-private", "decision-private", "digest-private", "archive-private", "case-private",
    ]) expect(panel).not.toHaveTextContent(hidden);
  });

  it("does not infer a selectable attempt from diagnostics alone", () => {
    const historyWorkspace = parseReviewWorkspace({
      ...publicWorkspace(),
      review_attempts: null,
      execution: {
        schema_version: "audit-execution/v1",
        attempt: publicAttempt("attempt-2", 2, "completed"),
        attempts: [publicAttempt("attempt-2", 2, "completed")],
        events: [],
      },
    });
    const { requests } = open(publicWorkspace(), historyWorkspace);
    expect(
      screen.getByText("没有可报告的冻结尝试；服务未提供的 legacy 输入不会被补造。"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "生成冻结报告" })).toBeDisabled();
    expect(requests).toHaveLength(0);
  });

  it.each(["attempt-1", "attempt-2"])(
    "does not display diagnostic hashes embedded in the business response for %s",
    async (diagnosticAttemptId) => {
      const diagnosticAttempt = {
        ...publicAttempt(diagnosticAttemptId, diagnosticAttemptId === "attempt-1" ? 1 : 2, "completed"),
        input_hash: "private-input",
        output_hash: "private-output",
      };
      const { requests } = open({
        ...publicWorkspace(),
        execution: {
          schema_version: "audit-execution/v1",
          attempt: diagnosticAttempt,
          attempts: [diagnosticAttempt],
          events: [],
        },
      });
      expect(await screen.findByText("第 2 次")).toBeInTheDocument();
      expect(screen.queryByText("输入摘要")).not.toBeInTheDocument();
      expect(screen.queryByText("private-input")).not.toBeInTheDocument();
      expect(screen.queryByText("private-output")).not.toBeInTheDocument();
      expect(screen.getByRole("link", { name: "审查工作区" })).toHaveAttribute(
        "href",
        "/cases/case-1/reviews/run-1?attempt_id=attempt-2",
      );
      expect(requests.some(({ url }) => url.pathname.endsWith("/diagnostics"))).toBe(false);
    },
  );

  it.each([
    ["a different attempt", publicWorkspace("attempt-1")],
    ["an unfinished attempt", publicWorkspace("attempt-2", "running")],
  ] as const)("blocks a report for %s", async (_description, response) => {
    open(response);
    const ordinal = response.review_attempts.attempt.ordinal;
    expect(await screen.findByText(`第 ${ordinal} 次`)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "生成冻结报告" })).toBeDisabled();
  });
});
