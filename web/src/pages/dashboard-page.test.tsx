import {
  cleanup,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import type {
  WorkbenchCase,
  WorkbenchPeriod,
  WorkbenchSnapshot,
  WorkbenchTask,
  WorkbenchTaskUpdateInput,
} from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { DashboardPage } from "./dashboard-page";

const at = "2026-10-21T06:00:00Z";
const clients: QueryClient[] = [];

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function task(overrides: Partial<WorkbenchTask> = {}): WorkbenchTask {
  return {
    id: "task-mine",
    case_id: "case-1",
    title: "确认质保条款",
    kind: "decision",
    assignee_id: "me",
    due_at: "2026-10-20T15:59:59Z",
    status: "in_progress",
    priority: "high",
    finding_id: "finding-1",
    version: 7,
    created_at: at,
    created_by: "me",
    updated_at: at,
    ...overrides,
  };
}

function caseRecord(overrides: Partial<WorkbenchCase> = {}): WorkbenchCase {
  return {
    id: "case-1",
    title: "专线采购合同",
    contract_type: "procurement_ict",
    document_count: 2,
    pending_decisions: 3,
    latest_run: { id: "run-1", state: "completed", updated_at: at },
    updated_at: at,
    profile: {
      case_id: "case-1",
      number: "HT-001",
      customer: "示例政企客户",
      supplier: "示例供应商",
      contract_category: "service",
      owner_id: "me",
      due_at: null,
      version: 1,
      updated_at: at,
    },
    severity: "high",
    progress: 80,
    business_line: "ENT",
    scenario: "telecom_dedicated_line",
    business_status: "active",
    ...overrides,
  };
}

function snapshot(overrides: Partial<WorkbenchSnapshot> = {}): WorkbenchSnapshot {
  return {
    schema_version: "workbench/v1",
    generated_at: at,
    period: "week",
    dashboard: {
      visible_cases: 2,
      my_pending_tasks: 2,
      overdue_tasks: 1,
      pending_review_cases: 3,
      remediation_cases: 1,
      completed_reviews: 7,
      average_duration_ms: 125_000,
      progress: [{ status: "active", count: 2 }],
      risks: [
        { severity: "high", count: 2 },
        { severity: "medium", count: 1 },
        { severity: "low", count: 1 },
        { severity: "info", count: 0 },
        { severity: "unrated", count: 1 },
      ],
    },
    cases: [
      caseRecord(),
      caseRecord({
        id: "case/old",
        title: "设备维保合同",
        contract_type: "service",
        updated_at: "2026-10-20T06:00:00Z",
        latest_run: null,
        severity: null,
        profile: null,
      }),
    ],
    tasks: [
      task(),
      task({
        id: "task-low",
        title: "补充附件",
        due_at: null,
        priority: "low",
        kind: "general",
      }),
      task({ id: "task-other", title: "其他人的审查", assignee_id: "other" }),
      task({ id: "task-done", title: "已保存的完成事项", status: "completed" }),
    ],
    members: [
      {
        id: "me",
        display_name: "周审查",
        role: "reviewer",
        tenant_id: "tenant",
        org_id: "org",
      },
    ],
    runs: [
      {
        id: "run-1",
        case_id: "case-1",
        case_title: "专线采购合同",
        state: "completed",
        created_at: at,
        updated_at: at,
        attempt_id: "attempt-1",
        started_at: "2026-10-21T05:00:00Z",
        finished_at: "2026-10-21T05:30:00Z",
        usage: null,
      },
    ],
    profiles: [],
    relations: [],
    knowledge: [],
    remediations: [],
    reports: [],
    documents: [],
    audit: [],
    templates: [],
    tools: [],
    model: {
      name: "",
      ocr_name: "",
      status: "unconfigured",
      label: "规则模式",
    },
    ...overrides,
  };
}

class DashboardService {
  data = snapshot();
  requests: { method: string; path: string; body: unknown }[] = [];
  snapshotStatus: number | null = null;
  private failure: number | null = null;

  failNext(status: number) {
    this.failure = status;
  }

  fetch = async (
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> => {
    const url = new URL(String(input), "http://dashboard.test");
    const method = init?.method ?? "GET";
    const body: unknown =
      typeof init?.body === "string" ? JSON.parse(init.body) : null;
    this.requests.push({ method, path: `${url.pathname}${url.search}`, body });
    if (url.pathname === "/api/session")
      return json({ audience: "client", actor: this.data.members[0] });
    if (url.pathname === "/api/workbench") {
      const failure = this.failure;
      this.failure = null;
      if (failure)
        return json(
          {
            error: {
              code: failure === 403 ? "forbidden" : "unavailable",
              message:
                failure === 403
                  ? "没有案件授权"
                  : "快照服务暂不可用",
            },
          },
          failure,
        );
      const period = (url.searchParams.get("period") ??
        "week") as WorkbenchPeriod;
      return json({
        ...this.data,
        period,
        dashboard: {
          ...this.data.dashboard,
          ...(period === "day"
            ? { completed_reviews: 1, average_duration_ms: null, overdue_tasks: 0 }
            : {}),
        },
      });
    }
    const parts = url.pathname.split("/");
    if (parts[4] === "tasks" && method === "PUT") {
      const submitted = body as WorkbenchTaskUpdateInput;
      const index = this.data.tasks.findIndex(
        (item) => item.id === decodeURIComponent(parts[5]!),
      );
      const previous = this.data.tasks[index];
      if (!previous || submitted.expected_version !== previous.version) {
        return json(
          {
            error: {
              code: "conflict",
              message: "任务已被其他人更新，请读取最新版本",
            },
          },
          409,
        );
      }
      this.data.tasks[index] = {
        ...previous,
        ...submitted,
        version: previous.version + 1,
      } as WorkbenchTask;
      this.data.dashboard.my_pending_tasks -= 1;
      return json(this.data.tasks[index]);
    }
    return json(
      { error: { message: `未匹配的请求 ${method} ${url.pathname}` } },
      404,
    );
  };
}

function open(service = new DashboardService(), path = "/dashboard") {
  vi.stubGlobal("fetch", vi.fn(service.fetch));
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <DashboardPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { service, user: userEvent.setup() };
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("DashboardPage", () => {
  it("renders real KPIs, trend, risk findings and recent cases with scopes", async () => {
    open();
    await screen.findByRole("heading", { name: "你好，周审查" });

    const kpis = screen.getByLabelText("工作台关键指标");
    expect(within(kpis).getByLabelText("待处理审查")).toHaveTextContent("3");
    expect(within(kpis).getByLabelText("高风险合同")).toHaveTextContent("1");
    expect(within(kpis).getByLabelText("已完成审查")).toHaveTextContent("7");
    expect(within(kpis).getByLabelText("平均审查时长")).toHaveTextContent(
      "2 分钟",
    );
    expect(
      within(kpis).getAllByText("已完成与平均时长按本周统计"),
    ).toHaveLength(2);

    expect(
      screen.getByText(/当前有 2 项我的待办。可见范围内另有 1 项任务逾期。/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/当前有 1 个高风险案件需要优先关注。/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("img", {
        name: "最新审查结论发现项分布：高风险 2 项，中风险 1 项，低风险 1 项，提示 0 项，未评级 1 项",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("img", { name: /近 14 天审查数量/ }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/按最新审查结论中的发现项分级统计，不是合同数量/),
    ).toBeInTheDocument();

    const recent = screen.getByRole("region", { name: "最近案件" });
    expect(
      within(recent).getByRole("link", { name: "设备维保合同" }),
    ).toHaveAttribute("href", "/cases/case%2Fold");
    expect(
      within(recent).getByRole("link", { name: "专线采购合同" }),
    ).toHaveAttribute("href", "/cases/case-1/reviews/run-1");
    expect(
      within(recent).getByText("示例政企客户 / 示例供应商"),
    ).toBeInTheDocument();
    expect(within(recent).getByText("未评级")).toBeInTheDocument();
    expect(within(recent).getByText("尚未审查")).toBeInTheDocument();

    expect(
      screen.getByText(/待处理审查与高风险合同为当前状态/),
    ).toBeInTheDocument();
    expect(screen.getByText(/趋势为近 14 天全部可见运行/)).toBeInTheDocument();
  });

  it("requests the selected period and reflects its real snapshot values", async () => {
    const { service, user } = open();
    await screen.findByRole("heading", { name: "你好，周审查" });

    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "统计周期" }),
      "今日",
    );
    await waitFor(() =>
      expect(
        within(screen.getByLabelText("已完成审查")).getByText("1"),
      ).toBeInTheDocument(),
    );
    expect(
      within(screen.getByLabelText("平均审查时长")).getByText("未报告"),
    ).toBeInTheDocument();
    expect(
      service.requests.some((request) => request.path === "/api/workbench?period=day"),
    ).toBe(true);
    expect(
      screen.getByText(/当前周期没有可计算的已完成审查/),
    ).toBeInTheDocument();
  });

  it("completes a real task with the expected version and refreshes the snapshot", async () => {
    const { service, user } = open();
    await screen.findByRole("heading", { name: "你好，周审查" });

    await user.click(
      screen.getByRole("button", { name: "将 确认质保条款 标记完成" }),
    );
    await waitFor(() =>
      expect(
        service.requests.some(
          (request) =>
            request.method === "PUT" &&
            request.path === "/api/cases/case-1/tasks/task-mine",
        ),
      ).toBe(true),
    );
    const update = service.requests.find(
      (request) => request.method === "PUT",
    );
    expect(update?.body).toMatchObject({
      status: "completed",
      expected_version: 7,
    });
    await waitFor(() =>
      expect(
        screen.queryByRole("link", { name: "确认质保条款" }),
      ).not.toBeInTheDocument(),
    );
  });

  it("shows loading, server error, forbidden and empty states honestly", async () => {
    const loading = new DashboardService();
    const gate = Promise.withResolvers<void>();
    const original = loading.fetch;
    loading.fetch = async (input, init) => {
      if (String(input).includes("/api/workbench")) await gate.promise;
      return original(input, init);
    };
    open(loading);
    expect(
      screen.getByText("正在加载工作台快照"),
    ).toBeInTheDocument();
    gate.resolve();
    await screen.findByRole("heading", { name: "你好，周审查" });
    cleanup();
    for (const client of clients.splice(0)) client.clear();
    vi.unstubAllGlobals();

    const failed = new DashboardService();
    failed.failNext(503);
    const first = open(failed);
    expect(
      await screen.findByText("工作台快照没有加载"),
    ).toBeInTheDocument();
    expect(screen.getByText("快照服务暂不可用")).toBeInTheDocument();
    await first.user.click(screen.getByRole("button", { name: "重试" }));
    await screen.findByRole("heading", { name: "你好，周审查" });
    cleanup();
    for (const client of clients.splice(0)) client.clear();
    vi.unstubAllGlobals();

    const forbidden = new DashboardService();
    forbidden.failNext(403);
    open(forbidden);
    expect(
      await screen.findByText("没有权限查看工作台"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/当前身份没有可见的案件范围/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "重试" })).not.toBeInTheDocument();
    cleanup();
    for (const client of clients.splice(0)) client.clear();
    vi.unstubAllGlobals();

    const empty = new DashboardService();
    empty.data = snapshot({
      cases: [],
      tasks: [],
      runs: [],
      dashboard: {
        ...empty.data.dashboard,
        pending_review_cases: 0,
        completed_reviews: 0,
        average_duration_ms: null,
        overdue_tasks: 0,
        my_pending_tasks: 0,
        visible_cases: 0,
        risks: [
          { severity: "high", count: 0 },
          { severity: "medium", count: 0 },
          { severity: "low", count: 0 },
          { severity: "info", count: 0 },
          { severity: "unrated", count: 0 },
        ],
      },
    });
    open(empty);
    await screen.findByRole("heading", { name: "你好，周审查" });
    expect(screen.getByText("当前身份没有可见案件")).toBeInTheDocument();
    expect(screen.getByText("没有未完成的分派任务")).toBeInTheDocument();
    expect(screen.getByText("还没有已报告的发现项")).toBeInTheDocument();
    expect(
      screen.getByText("近 14 天还没有可统计的审查运行"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/当前可见案件中没有高风险等级。/),
    ).toBeInTheDocument();
  });
});
