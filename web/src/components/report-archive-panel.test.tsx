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
      .getByRole("button", { name: "生成报告" })
      .closest("form")!;
    expect(within(form).queryByText("输入摘要")).not.toBeInTheDocument();
    expect(within(form).getByText("合同正文.txt")).toBeInTheDocument();
    expect(form).not.toHaveTextContent("run-1");
    expect(form).not.toHaveTextContent("attempt-2");
    expect(form).not.toHaveTextContent("document-1");
    await waitFor(() =>
      expect(within(form).getByRole("button", { name: "生成报告" })).toBeEnabled(),
    );
    await user.click(within(form).getByRole("button", { name: "生成报告" }));
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
    const panel = screen.getByRole("heading", { name: "审查报告与归档" }).closest("section")!;
    expect(within(panel).getByText("报告版本 3")).toBeInTheDocument();
    expect(within(panel).getByText("审批完成")).toBeInTheDocument();
    expect(within(panel).getByRole("link", { name: "查看 HTML 报告" })).toHaveAttribute(
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
      screen.getByText("没有可用于生成报告的执行记录。较早的审查如果没有保存当时使用的材料版本记录，不能用于生成报告。"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "生成报告" })).toBeDisabled();
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
    expect(screen.getByRole("button", { name: "生成报告" })).toBeDisabled();
  });
});

function renderArchive(options: {
  detail?: CaseDetail;
  history?: { runId: string; workspace: ReturnType<typeof parseReviewWorkspace> }[];
  historyLoading?: boolean;
  historyError?: unknown;
  canCreateReport?: boolean;
  canArchive?: boolean;
  canReadReports?: boolean;
  archived?: boolean;
  reviewResponse?: () => Response | Promise<Response>;
  reportResponse?: () => Response | Promise<Response>;
  archiveResponse?: () => Response | Promise<Response>;
} = {}) {
  const requests: { url: URL; init?: RequestInit }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://workflow.test");
      requests.push({ url, init });
      if (url.pathname === "/api/cases/case-1/reviews/run-1") {
        return (options.reviewResponse ?? (() => Response.json(publicWorkspace())))();
      }
      if (url.pathname === "/api/cases/case-1/reports" && init?.method === "POST") {
        return (
          options.reportResponse ??
          (() =>
            Response.json({
              id: "report-2",
              version: 2,
              run_id: "run-1",
              attempt_id: "attempt-2",
              input_hash: "input",
              output_hash: "output",
              decision_snapshot_hash: "decisions",
              digest: "digest",
              created_at: at,
            }))
        )();
      }
      if (url.pathname === "/api/cases/case-1/archive" && init?.method === "POST") {
        return (
          options.archiveResponse ??
          (() =>
            Response.json({
              id: "archive-1",
              case_id: "case-1",
              report_id: "report-1",
              reason: "审批完成",
              archived_at: at,
            }))
        )();
      }
      return Response.json({ error: { message: "Unexpected request" } }, { status: 404 });
    }),
  );
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  const caseDetail = options.detail ?? detail;
  const view = render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <ReportArchivePanel
          caseId="case-1"
          detail={caseDetail}
          history={
            options.history ?? [
              { runId: "run-1", workspace: parseReviewWorkspace(publicWorkspace()) },
            ]
          }
          historyLoading={options.historyLoading ?? false}
          historyError={options.historyError ?? null}
          canCreateReport={options.canCreateReport ?? true}
          canArchive={options.canArchive ?? true}
          canReadReports={options.canReadReports ?? true}
          archived={options.archived ?? false}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...view, requests, user: userEvent.setup() };
}

const reportRecord = {
  id: "report-1",
  version: 4,
  run_id: "run-1",
  attempt_id: "attempt-2",
  input_hash: "input-private",
  output_hash: "output-private",
  decision_snapshot_hash: "decisions-private",
  digest: "digest-private",
  created_at: at,
};

const archiveRecord = {
  id: "archive-1",
  case_id: "case-1",
  report_id: "report-1",
  reason: "审批完成",
  archived_at: at,
};

describe("report archive permissions and states", () => {
  it("explains missing report permission without hiding history", () => {
    renderArchive({ canCreateReport: false });
    expect(
      screen.getByText("当前身份没有已定义的报告生成权限。"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "生成报告" })).not.toBeInTheDocument();
    expect(screen.getByText("报告历史")).toBeInTheDocument();
  });

  it("explains an archived case and shows the archive time", () => {
    renderArchive({
      archived: true,
      detail: { ...detail, archived_at: at },
    });
    expect(screen.getAllByText(/已归档/).length).toBeGreaterThan(0);
    expect(
      screen.getByText(/归档后的案件保留授权读取/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "生成报告" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "归档案件" })).not.toBeInTheDocument();
  });

  it("shows an archived badge without a time and a missing archive record note", () => {
    renderArchive({ archived: true, detail: { ...detail, archived_at: null } });
    expect(screen.getAllByText(/时间未报告/).length).toBeGreaterThan(0);
    expect(screen.getByText(/案件已归档，归档时间/)).toBeInTheDocument();
    expect(screen.getAllByText(/未报告/).length).toBeGreaterThan(0);
  });

  it("reports history loading, errors and empty choices", () => {
    const view = renderArchive({ historyLoading: true });
    expect(
      screen.getByText("正在读取可用于生成报告的审查和执行记录。"),
    ).toBeInTheDocument();
    view.unmount();
    renderArchive({ historyError: new Error("boom") });
    expect(screen.getByRole("alert")).toHaveTextContent(
      "运行历史没有加载：请求没有完成",
    );
  });

  it("explains the absence of usable attempts", () => {
    renderArchive({ history: [] });
    expect(
      screen.getByText(/没有可用于生成报告的执行记录/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "生成报告" })).toBeDisabled();
  });

  it("reports selected review loading and errors", async () => {
    let resolveReview!: (response: Response) => void;
    const pending = new Promise<Response>((resolve) => {
      resolveReview = resolve;
    });
    const view = renderArchive({ reviewResponse: () => pending });
    expect(screen.getByText("正在读取所选执行记录。")).toBeInTheDocument();
    resolveReview(Response.json(publicWorkspace()));
    await screen.findByText("第 2 次");
    view.unmount();
    renderArchive({
      reviewResponse: () =>
        Response.json(
          { error: { message: "读取失败", code: "failed" } },
          { status: 500 },
        ),
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "所选尝试没有加载：读取失败",
    );
  });

  it("renders the selected review without an attempt as unavailable", async () => {
    const workspace = { ...publicWorkspace(), review_attempts: null };
    renderArchive({ reviewResponse: () => Response.json(workspace) });
    expect(await screen.findByText("未返回")).toBeInTheDocument();
    expect(screen.getByText("未返回")).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: "审查工作区" }),
    ).not.toBeInTheDocument();
  });
});

describe("report creation and archiving actions", () => {
  it("marks a pending report and focuses a failed creation", async () => {
    let resolveReport!: (response: Response) => void;
    const pending = new Promise<Response>((resolve) => {
      resolveReport = resolve;
    });
    const view = renderArchive({ reportResponse: () => pending });
    const user = userEvent.setup();
    const generate = await screen.findByRole("button", { name: "生成报告" });
    await waitFor(() => expect(generate).toBeEnabled());
    await user.click(generate);
    expect(
      await screen.findByRole("button", { name: "正在生成报告" }),
    ).toBeDisabled();
    resolveReport(
      Response.json({ error: { message: "生成失败", code: "failed" } }, { status: 500 }),
    );
    const error = await screen.findByText("生成失败");
    await waitFor(() => expect(error).toHaveFocus());
    view.unmount();
  });

  it("shows the created report version", async () => {
    const { user } = renderArchive();
    const generate = await screen.findByRole("button", { name: "生成报告" });
    await waitFor(() => expect(generate).toBeEnabled());
    await user.click(generate);
    expect(await screen.findByText("报告已创建，版本 2。")).toBeInTheDocument();
  });

  it("archives with the selected report and reason, then clears the reason", async () => {
    const { requests, user } = renderArchive({
      detail: { ...detail, reports: [reportRecord] },
    });
    await user.type(screen.getByLabelText("归档理由"), "审批完成");
    await user.click(screen.getByRole("button", { name: "归档案件" }));
    expect(await screen.findByRole("status")).toHaveTextContent(/已保存归档记录/);
    const request = requests.find(({ url }) => url.pathname.endsWith("/archive"))!;
    expect(JSON.parse(String(request.init?.body))).toEqual({
      report_id: "report-1",
      reason: "审批完成",
    });
    expect(screen.getByLabelText("归档理由")).toHaveValue("");
  });

  it("disables archiving without a report or reason and explains the missing report", () => {
    renderArchive({ detail: { ...detail, reports: [] } });
    expect(
      screen.getByText("先生成实际报告版本，再执行归档。"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "归档案件" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "归档所依据的报告" })).toBeDisabled();
  });

  it("focuses an archive failure and keeps the reason", async () => {
    const { user } = renderArchive({
      detail: { ...detail, reports: [reportRecord] },
      archiveResponse: () =>
        Response.json(
          { error: { message: "归档失败", code: "failed" } },
          { status: 500 },
        ),
    });
    await user.type(screen.getByLabelText("归档理由"), "审批完成");
    await user.click(screen.getByRole("button", { name: "归档案件" }));
    const error = await screen.findByRole("alert");
    expect(error).toHaveTextContent("归档失败");
    await waitFor(() => expect(error).toHaveFocus());
    expect(screen.getByLabelText("归档理由")).toHaveValue("审批完成");
  });

  it("renders archive history with the report version and a fallback", () => {
    const view = renderArchive({
      detail: {
        ...detail,
        reports: [reportRecord],
        archives: [archiveRecord, { ...archiveRecord, id: "archive-2", report_id: "other-report" }],
      },
    });
    expect(screen.getAllByText("归档记录").length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("4").length).toBeGreaterThan(0);
    expect(screen.getAllByText("未返回").length).toBeGreaterThan(0);
    view.unmount();
    renderArchive({ detail });
    expect(screen.getByText("案件尚未归档。")).toBeInTheDocument();
  });

  it("keeps a non-terminal attempt from enabling a report", async () => {
    renderArchive({
      reviewResponse: () => Response.json(publicWorkspace("attempt-2", "running")),
    });
    expect(
      await screen.findByText(/所选尝试仍在执行，终态生成报告操作尚不可用/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "生成报告" })).toBeDisabled();
  });
});
