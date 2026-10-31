import {
  cleanup,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import type {
  WorkbenchCase,
  WorkbenchDashboard,
  WorkbenchPeriod,
  WorkbenchSnapshot,
  WorkbenchTask,
  WorkbenchTaskInput,
  WorkbenchTaskUpdateInput,
} from "../api/types";
import { HomePage } from "./home-page";

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
    due_at: "2026-10-22T15:59:59Z",
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
    latest_run: null,
    updated_at: at,
    profile: null,
    severity: null,
    progress: null,
    business_line: "ENT",
    scenario: "telecom_dedicated_line",
    business_status: "active",
    ...overrides,
  };
}

function snapshot(): WorkbenchSnapshot {
  return {
    schema_version: "workbench/v1",
    generated_at: at,
    period: "week",
    dashboard: {
      visible_cases: 17,
      my_pending_tasks: 2,
      overdue_tasks: 3,
      pending_review_cases: 4,
      remediation_cases: 1,
      completed_reviews: 9,
      average_duration_ms: 1234,
      progress: [
        { status: "active", count: 4 },
        { status: "archived", count: 1 },
        { status: "待确认", count: 2 },
        { status: "", count: 0 },
      ],
      risks: [
        { severity: "high", count: 4 },
        { severity: "medium", count: 2 },
        { severity: "low", count: 2 },
        { severity: "info", count: 1 },
        { severity: "unrated", count: 1 },
      ],
    },
    cases: [
      caseRecord(),
      caseRecord({
        id: "case/old",
        title: "设备维保合同",
        updated_at: "2026-10-23T06:00:00Z",
        business_line: "",
        scenario: "",
        latest_run: { id: "run old", state: "completed", updated_at: at },
      }),
    ],
    tasks: [
      task({
        id: "task-no-date",
        title: "补充附件",
        due_at: null,
        priority: "low",
        kind: "general",
        finding_id: null,
      }),
      task({
        id: "task-other",
        title: "其他人的审查",
        assignee_id: "other",
        priority: "medium",
        due_at: "2026-10-20T15:59:59Z",
      }),
      task(),
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
      {
        id: "other",
        display_name: "陈复核",
        role: "reviewer",
        tenant_id: "tenant",
        org_id: "org",
      },
    ],
    remediations: [
      {
        id: "remediation-1",
        case_id: "case/old",
        case_title: "设备维保合同",
        run_id: "run old",
        attempt_id: "attempt-1",
        finding_ids: ["finding-1"],
        assignee_id: "missing-member",
        due_at: at,
        measure: "补充验收标准",
        status: "submitted",
        version: 1,
        document_ids: [],
        reason: "待复核",
        verification_run_id: null,
        verification_attempt_id: null,
        created_at: at,
        updated_at: at,
      },
    ],
    audit: [
      {
        id: "event-old",
        case_id: "case/old",
        case_title: null,
        actor_id: "me",
        actor_name: "周审查",
        action: "task.created",
        at,
        summary: "创建了协作任务",
      },
      {
        id: "event-new",
        case_id: null,
        case_title: null,
        actor_id: "other",
        actor_name: "陈复核",
        action: "policy.updated",
        at: "2026-10-23T06:00:00Z",
        summary: "更新了平台规则",
      },
    ],
    profiles: [],
    relations: [],
    knowledge: [],
    reports: [],
    documents: [],
    runs: [],
    templates: [],
    tools: [],
    model: {
      name: "",
      ocr_name: "",
      status: "unconfigured",
      label: "规则模式",
    },
  };
}

class HomeService {
  data = snapshot();
  requests: { method: string; path: string; body: unknown }[] = [];
  periodDashboards: Partial<
    Record<WorkbenchPeriod, Partial<WorkbenchDashboard>>
  > = {};
  snapshotFailures: number[] = [];
  createFailures: number[] = [];
  snapshotGate: ReturnType<typeof Promise.withResolvers<void>> | null = null;
  completionGate: ReturnType<typeof Promise.withResolvers<void>> | null = null;

  fetch = async (
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> => {
    const url = new URL(String(input), "http://home.test");
    const method = init?.method ?? "GET";
    const body: unknown =
      typeof init?.body === "string" ? JSON.parse(init.body) : null;
    this.requests.push({ method, path: `${url.pathname}${url.search}`, body });
    if (url.pathname === "/api/session")
      return json({ audience: "client", actor: this.data.members[0] });
    if (url.pathname === "/api/intakes/pending")
      return json({ items: [], next_cursor: null });
    if (url.pathname === "/api/workbench") {
      if (this.snapshotGate) await this.snapshotGate.promise;
      const failure = this.snapshotFailures.shift();
      if (failure)
        return json(
          { error: { code: "unavailable", message: "快照服务暂不可用" } },
          failure,
        );
      const period = url.searchParams.get("period") as WorkbenchPeriod;
      return json({
        ...this.data,
        period,
        dashboard: { ...this.data.dashboard, ...this.periodDashboards[period] },
      });
    }
    const parts = url.pathname.split("/");
    if (parts[4] === "tasks" && method === "POST") {
      const failure = this.createFailures.shift();
      if (failure)
        return json(
          {
            error: {
              code: "invalid_task",
              message: "无法保存任务，请检查分派权限",
            },
          },
          failure,
        );
      const submitted = body as WorkbenchTaskInput;
      const saved = task({
        ...submitted,
        id: "task-created",
        case_id: decodeURIComponent(parts[3]!),
        version: 1,
      });
      this.data.tasks.push(saved);
      if (saved.assignee_id === "me") this.data.dashboard.my_pending_tasks += 1;
      return json(saved, 201);
    }
    if (parts[4] === "tasks" && method === "PUT") {
      if (this.completionGate) await this.completionGate.promise;
      const index = this.data.tasks.findIndex(
        (item) => item.id === decodeURIComponent(parts[5]!),
      );
      const previous = this.data.tasks[index];
      const submitted = body as WorkbenchTaskUpdateInput;
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
      const saved = {
        ...previous,
        ...submitted,
        version: previous.version + 1,
      };
      this.data.tasks[index] = saved;
      if (saved.assignee_id === "me") this.data.dashboard.my_pending_tasks -= 1;
      return json(saved);
    }
    return json(
      { error: { message: `未匹配的请求 ${method} ${url.pathname}` } },
      404,
    );
  };
}

function open(service = new HomeService()) {
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
      <MemoryRouter initialEntries={["/"]}>
        <HomePage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { service, user: userEvent.setup() };
}

function taskPanel() {
  return screen.getByRole("region", { name: "任务与待办" });
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("HomePage snapshot consumers", () => {
  it("uses server metrics, mutually exclusive business states and explicit risk levels", async () => {
    open();
    await screen.findByRole("heading", { name: "你好，周审查" });

    expect(
      within(screen.getByLabelText("工作台摘要"))
        .getAllByRole("definition")
        .map((item) => item.textContent),
    ).toEqual(["17", "2", "3", "4"]);
    expect(
      screen.getByRole("link", {
        name: "打开案件中心；未归档 4 项，已归档 1 项，待确认 2 项，未报告 0 项",
      }),
    ).toHaveAttribute("href", "/cases");
    expect(
      screen.getByText("状态统计 7 项；可见案件 17 项。"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/已完成审查 9 项；平均运行时长 1.2 秒/),
    ).toBeInTheDocument();
    for (const label of [
      "高风险 4，占 40%",
      "中风险 2，占 20%",
      "低风险 2，占 20%",
      "提示 1，占 10%",
      "未分级 1，占 10%",
    ]) {
      expect(screen.getByLabelText(label)).toBeInTheDocument();
    }
    const rows = within(screen.getByRole("table")).getAllByRole("row").slice(1);
    expect(
      within(rows[0]!).getByRole("link", { name: "设备维保合同" }),
    ).toHaveAttribute("href", "/cases/case%2Fold/reviews/run%20old");
    expect(within(rows[0]!).getByText("未设置")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("尚未审查")).toBeInTheDocument();
    expect(
      within(rows[1]!).getByText("ENT，telecom_dedicated_line"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /补充验收标准.*责任人 missing-member/ }),
    ).toHaveAttribute("href", "/cases/case%2Fold/reviews/run%20old");
    const audit = within(
      screen.getByRole("region", { name: "最近业务记录" }),
    ).getAllByRole("listitem");
    expect(audit[0]).toHaveTextContent(
      "平台级事件更新了平台规则陈复核，policy.updated",
    );
    expect(
      within(audit[1]!).getByRole("link", { name: "case/old" }),
    ).toHaveAttribute("href", "/cases/case%2Fold");
  });

  it("fetches each selected period and updates the displayed snapshot", async () => {
    const service = new HomeService();
    service.periodDashboards.day = {
      overdue_tasks: 0,
      completed_reviews: 1,
      average_duration_ms: null,
    };
    service.periodDashboards.month = {
      overdue_tasks: 11,
      completed_reviews: 23,
      average_duration_ms: 5000,
    };
    const { user } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "统计周期" }),
      "今日",
    );
    expect(
      await screen.findByText(/已完成审查 1 项；平均运行时长 未知/),
    ).toBeInTheDocument();
    expect(
      within(screen.getByLabelText("工作台摘要")).getAllByRole("definition")[2],
    ).toHaveTextContent("0");
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "统计周期" }),
      "本月",
    );
    expect(
      await screen.findByText(/已完成审查 23 项；平均运行时长 5.0 秒/),
    ).toBeInTheDocument();
    expect(
      within(screen.getByLabelText("工作台摘要")).getAllByRole("definition")[2],
    ).toHaveTextContent("11");
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "统计周期" }),
      "本周",
    );
    expect(
      await screen.findByText(/已完成审查 9 项；平均运行时长 1.2 秒/),
    ).toBeInTheDocument();
    expect(
      service.requests
        .filter((request) => request.path.startsWith("/api/workbench"))
        .map((request) => request.path)
        .slice(0, 3),
    ).toEqual([
      "/api/workbench?period=week",
      "/api/workbench?period=day",
      "/api/workbench?period=month",
    ]);
  });

  it.each([0, 3])(
    "distinguishes empty visibility from missing statistics with %i visible cases",
    async (visible) => {
      const service = new HomeService();
      service.data.dashboard = {
        ...service.data.dashboard,
        visible_cases: visible,
        my_pending_tasks: 0,
        average_duration_ms: null,
        progress: [],
        risks: [],
      };
      service.data.cases = visible === 0 ? [] : [caseRecord()];
      service.data.tasks = [];
      service.data.remediations = [];
      service.data.audit = [];
      open(service);
      await screen.findByRole("heading", { name: "你好，周审查" });
      const progress = screen.getByRole("region", { name: "审查进展" });
      expect(
        within(progress).getByText(
          visible === 0 ? "当前身份没有可见案件。" : "案件状态统计未报告。",
        ),
      ).toBeInTheDocument();
      expect(
        within(progress).getByRole("link", {
          name: "打开案件中心；案件状态分布未报告",
        }),
      ).toHaveTextContent(visible === 0 ? "0案件" : "—状态未报");
      expect(
        within(screen.getByRole("region", { name: "审查发现分布" })).getByText(
          "没有获取到这项统计数据。",
        ),
      ).toBeInTheDocument();
      expect(within(taskPanel()).getByRole("status")).toHaveTextContent(
        "当前身份没有未完成的分派任务。",
      );
      expect(screen.getByText("没有获取到整改记录。")).toBeInTheDocument();
      expect(screen.getByText("没有获取到审计记录。")).toBeInTheDocument();
      if (visible === 0)
        expect(screen.queryByRole("table")).not.toBeInTheDocument();
      else expect(screen.getByRole("table")).toBeInTheDocument();
    },
  );

  it("renders a zero risk denominator without claiming any risk percentage", async () => {
    const service = new HomeService();
    service.data.dashboard.progress = [{ status: "active", count: 0 }];
    service.data.dashboard.risks = [{ severity: "high", count: 0 }];
    open(service);
    expect(await screen.findByLabelText("高风险 0，占 0%")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "打开案件中心；未归档 0 项" }),
    ).toHaveTextContent("—状态未报");
  });

  it("shows loading, reports snapshot failures and retries the actual query", async () => {
    const service = new HomeService();
    service.snapshotGate = Promise.withResolvers<void>();
    service.snapshotFailures.push(503);
    const { user } = open(service);
    expect(screen.getByRole("status")).toHaveTextContent("正在加载工作台");
    service.snapshotGate.resolve();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "快照服务暂不可用",
    );
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(
      await screen.findByRole("heading", { name: "你好，周审查" }),
    ).toBeInTheDocument();
    expect(
      service.requests.filter(
        (request) => request.path === "/api/workbench?period=week",
      ),
    ).toHaveLength(2);
  });
});

describe("HomePage persistent task workflow", () => {
  it("filters by the signed-in assignee and completion, and orders deadlines before undated tasks", async () => {
    const { user } = open();
    await screen.findByRole("heading", { name: "你好，周审查" });
    const panel = within(taskPanel());
    expect(
      panel
        .getAllByRole("listitem")
        .map((item) => within(item).getByRole("link").textContent),
    ).toEqual(["确认质保条款", "补充附件"]);
    expect(panel.getByText("高优先级")).toBeInTheDocument();
    expect(panel.getByText("低优先级")).toBeInTheDocument();
    expect(panel.getByText("未设期限")).toBeInTheDocument();
    await user.click(panel.getByRole("tab", { name: /全部未完成/ }));
    expect(
      panel
        .getAllByRole("listitem")
        .map((item) => within(item).getByRole("link").textContent),
    ).toEqual(["其他人的审查", "确认质保条款", "补充附件"]);
    expect(
      panel.getByText("专线采购合同，陈复核，审查判断"),
    ).toBeInTheDocument();
    expect(panel.getByText("中优先级")).toBeInTheDocument();
    await user.click(panel.getByRole("tab", { name: /^已完成/ }));
    expect(
      panel.getByRole("link", { name: "已保存的完成事项" }),
    ).toBeInTheDocument();
    expect(
      panel.queryByRole("button", { name: "标记完成" }),
    ).not.toBeInTheDocument();
    expect(
      panel.queryByRole("link", { name: "确认质保条款" }),
    ).not.toBeInTheDocument();
    await user.click(panel.getByRole("tab", { name: /我的待办/ }));
    expect(panel.getByRole("tab", { name: /我的待办/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(panel.getByRole("link", { name: "确认质保条款" })).toHaveAttribute(
      "href",
      "/cases/case-1",
    );
  });

  it.each(["2026-11-05", ""])(
    "persists a trimmed task with deadline %j and refreshes the list",
    async (due) => {
      const { user, service } = open();
      await screen.findByRole("heading", { name: "你好，周审查" });
      await user.click(screen.getByRole("button", { name: "新建任务" }));
      expect(
        screen.getByRole("button", { name: "创建并保存任务" }),
      ).toBeDisabled();
      await chooseOption(
        user,
        screen.getByRole("combobox", { name: "关联案件" }),
        "专线采购合同",
      );
      await user.type(
        screen.getByRole("textbox", { name: "任务名称" }),
        "  核实发票附件  ",
      );
      await chooseOption(
        user,
        screen.getByRole("combobox", { name: "责任人" }),
        "周审查，reviewer",
      );
      if (due)
        fireEvent.change(screen.getByLabelText("截止日期"), {
          target: { value: due },
        });
      await user.click(screen.getByRole("button", { name: "创建并保存任务" }));
      expect(
        await screen.findByRole("link", { name: "核实发票附件" }),
      ).toHaveAttribute("href", "/cases/case-1");
      await waitFor(() =>
        expect(
          screen.queryByRole("textbox", { name: "任务名称" }),
        ).not.toBeInTheDocument(),
      );
      expect(
        service.requests.find((request) => request.method === "POST"),
      ).toEqual({
        method: "POST",
        path: "/api/cases/case-1/tasks",
        body: {
          title: "核实发票附件",
          kind: "general",
          assignee_id: "me",
          due_at: due ? "2026-11-05T23:59:59+08:00" : null,
          status: "open",
          priority: "medium",
          finding_id: null,
        },
      });
      expect(
        service.requests.filter(
          (request) => request.path === "/api/workbench?period=week",
        ),
      ).toHaveLength(2);
      await user.click(screen.getByRole("button", { name: "新建任务" }));
      expect(screen.getByRole("textbox", { name: "任务名称" })).toHaveValue("");
      expect(screen.getByLabelText("截止日期")).toHaveValue("");
      await user.click(screen.getByRole("button", { name: "收起" }));
      expect(
        screen.queryByRole("textbox", { name: "任务名称" }),
      ).not.toBeInTheDocument();
    },
  );

  it("keeps failed creation input available for correction and a successful retry", async () => {
    const service = new HomeService();
    service.createFailures.push(403);
    const { user } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    await user.click(screen.getByRole("button", { name: "新建任务" }));
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "关联案件" }),
      "专线采购合同",
    );
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "责任人" }),
      "周审查，reviewer",
    );
    await user.type(
      screen.getByRole("textbox", { name: "任务名称" }),
      "重新核对权限",
    );
    await user.click(screen.getByRole("button", { name: "创建并保存任务" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "无法保存任务，请检查分派权限",
    );
    expect(screen.getByRole("textbox", { name: "任务名称" })).toHaveValue(
      "重新核对权限",
    );
    expect(
      screen.queryByRole("link", { name: "重新核对权限" }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "创建并保存任务" }));
    expect(
      await screen.findByRole("link", { name: "重新核对权限" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByRole("alert")).not.toBeInTheDocument(),
    );
  });

  it("completes the chosen task with its version and refreshes both task filters", async () => {
    const service = new HomeService();
    service.completionGate = Promise.withResolvers<void>();
    const { user } = open(service);
    const link = await screen.findByRole("link", { name: "确认质保条款" });
    await user.click(
      within(link.closest("li")!).getByRole("button", { name: "标记完成" }),
    );
    expect(
      await screen.findByRole("button", { name: "正在保存" }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: "标记完成" })).toBeEnabled();
    service.completionGate.resolve();
    await waitFor(() =>
      expect(
        screen.queryByRole("link", { name: "确认质保条款" }),
      ).not.toBeInTheDocument(),
    );
    expect(
      service.requests.find((request) => request.method === "PUT"),
    ).toEqual({
      method: "PUT",
      path: "/api/cases/case-1/tasks/task-mine",
      body: {
        title: "确认质保条款",
        kind: "decision",
        assignee_id: "me",
        due_at: "2026-10-22T15:59:59Z",
        status: "completed",
        priority: "high",
        finding_id: "finding-1",
        expected_version: 7,
      },
    });
    await user.click(within(taskPanel()).getByRole("tab", { name: /^已完成/ }));
    const completed = screen
      .getByRole("link", { name: "确认质保条款" })
      .closest("li")!;
    expect(within(completed).getByText("已完成")).toBeInTheDocument();
    expect(within(completed).queryByRole("button")).not.toBeInTheDocument();
  });

  it("preserves a conflicted task and submits the refreshed version on the next completion", async () => {
    const { user, service } = open();
    const link = await screen.findByRole("link", { name: "确认质保条款" });
    service.data.tasks.find((item) => item.id === "task-mine")!.version = 8;
    await user.click(
      within(link.closest("li")!).getByRole("button", { name: "标记完成" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "任务已被其他人更新，请读取最新版本",
    );
    expect(
      screen.getByRole("link", { name: "确认质保条款" }),
    ).toBeInTheDocument();
    expect(
      service.requests.filter((request) => request.method === "PUT"),
    ).toHaveLength(1);
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "统计周期" }),
      "今日",
    );
    const refreshed = await screen.findByRole("link", { name: "确认质保条款" });
    await user.click(
      within(refreshed.closest("li")!).getByRole("button", {
        name: "标记完成",
      }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("link", { name: "确认质保条款" }),
      ).not.toBeInTheDocument(),
    );
    expect(
      service.requests
        .filter((request) => request.method === "PUT")
        .map(
          (request) =>
            (request.body as WorkbenchTaskUpdateInput).expected_version,
        ),
    ).toEqual([7, 8]);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("limits the dashboard task list and preserves unresolved case and member identities", async () => {
    const service = new HomeService();
    service.data.tasks = Array.from({ length: 10 }, (_, index) =>
      task({
        id: `extra-${index}`,
        title: `协作事项 ${index}`,
        case_id: "unlisted-case",
        assignee_id: "unlisted-member",
        due_at: `2026-11-${String(index + 1).padStart(2, "0")}T00:00:00Z`,
      }),
    );
    const { user } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    await user.click(
      within(taskPanel()).getByRole("tab", { name: /全部未完成/ }),
    );
    expect(within(taskPanel()).getAllByRole("listitem")).toHaveLength(8);
    expect(screen.getByRole("link", { name: "协作事项 0" })).toHaveAttribute(
      "href",
      "/cases/unlisted-case",
    );
    expect(
      screen.getAllByText("unlisted-case，unlisted-member，审查判断"),
    ).toHaveLength(8);
    expect(
      screen.queryByRole("link", { name: "协作事项 8" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "查看全部任务" }),
    ).toHaveAttribute("href", "/workbench/tasks");
    await user.click(within(taskPanel()).getByRole("tab", { name: /^已完成/ }));
    expect(within(taskPanel()).getByRole("status")).toHaveTextContent(
      "没有符合条件的任务记录。",
    );
  });
});
