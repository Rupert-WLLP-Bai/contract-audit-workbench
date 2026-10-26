import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import type { CaseSummary, WorkbenchSnapshot } from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { CasesPage } from "./cases-page";

const at = "2026-10-21T06:00:00Z";
const clients: QueryClient[] = [];

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function summary(overrides: Partial<CaseSummary> = {}): CaseSummary {
  return {
    id: "c1",
    title: "高风险专线",
    contract_type: "procurement_ict",
    document_count: 2,
    pending_decisions: 1,
    latest_run: { id: "r1", state: "running", updated_at: at },
    updated_at: at,
    business_line: "ENT",
    scenario: "telecom_dedicated_line",
    business_status: "active",
    ...overrides,
  };
}

function profile(caseId: string, owner = "me") {
  return {
    case_id: caseId,
    number: `HT-${caseId}`,
    customer: "示例客户",
    supplier: "示例供应商",
    contract_category: "service",
    owner_id: owner,
    due_at: null,
    version: 1,
    updated_at: at,
  };
}

function workbenchCase(
  base: CaseSummary,
  overrides: {
    severity?: string | null;
    owner?: string;
    profile?: ReturnType<typeof profile> | null;
  } = {},
) {
  return {
    ...base,
    profile:
      overrides.profile === undefined
        ? profile(base.id, overrides.owner)
        : overrides.profile,
    severity:
      overrides.severity === undefined ? null : overrides.severity,
    progress: 40,
  };
}

function snapshot(cases: CaseSummary[]): WorkbenchSnapshot {
  return {
    schema_version: "workbench/v1",
    generated_at: at,
    period: "week",
    dashboard: {
      visible_cases: cases.length,
      my_pending_tasks: 1,
      overdue_tasks: 1,
      pending_review_cases: 1,
      remediation_cases: 0,
      completed_reviews: 1,
      average_duration_ms: 60_000,
      progress: [],
      risks: [
        { severity: "high", count: 1 },
        { severity: "medium", count: 1 },
        { severity: "low", count: 0 },
        { severity: "info", count: 0 },
        { severity: "unrated", count: 1 },
      ],
    },
    cases: [
      workbenchCase(cases[0]!, { severity: "high", owner: "me" }),
      workbenchCase(cases[1]!, { severity: "medium", owner: "other" }),
      workbenchCase(cases[2]!, {
        severity: null,
        profile: null,
      }),
    ],
    profiles: [],
    tasks: [
      {
        id: "task-1",
        case_id: "c1",
        title: "确认质保条款",
        kind: "decision",
        assignee_id: "me",
        due_at: "2026-10-20T15:59:59Z",
        status: "open",
        priority: "high",
        finding_id: null,
        version: 1,
        created_at: at,
        created_by: "me",
        updated_at: at,
      },
    ],
    relations: [],
    knowledge: [],
    members: [
      {
        id: "me",
        display_name: "王审查",
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
    remediations: [],
    reports: [],
    documents: [],
    runs: [],
    audit: [],
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

const CASES: CaseSummary[] = [
  summary(),
  summary({
    id: "c2",
    title: "已完成合同",
    latest_run: { id: "r2", state: "completed", updated_at: at },
    contract_type: "service",
    business_status: "archived",
  }),
  summary({
    id: "c3",
    title: "未评级合同",
    latest_run: null,
    contract_type: "network_engineering",
    pending_decisions: 0,
  }),
];

class CasesService {
  cases = CASES;
  workbench = snapshot(CASES);
  failWorkbench = false;
  requests: { method: string; path: string }[] = [];

  fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input), "http://cases.test");
    const method = init?.method ?? "GET";
    this.requests.push({ method, path: `${url.pathname}${url.search}` });
    if (url.pathname === "/api/session")
      return json({
        audience: "client",
        actor: { ...this.workbench.members[0], role: "admin" },
      });
    if (url.pathname === "/api/cases" && method === "GET")
      return json({ items: this.cases });
    if (url.pathname === "/api/workbench") {
      if (this.failWorkbench)
        return json(
          { error: { code: "unavailable", message: "快照服务暂不可用" } },
          503,
        );
      return json({
        ...this.workbench,
        period: url.searchParams.get("period") ?? "week",
      });
    }
    return json(
      { error: { message: `未匹配的请求 ${method} ${url.pathname}` } },
      404,
    );
  };
}

function open(service = new CasesService(), path = "/cases") {
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
        <CasesPage />
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

describe("CasesPage", () => {
  it("renders real case KPIs, risk/owner enrichment, tabs, filters and the side rail", async () => {
    const { user } = open();
    await screen.findByRole("heading", { name: "案件中心", level: 1 });

    const kpis = screen.getByLabelText("案件概况");
    expect(within(kpis).getByLabelText("全部案件")).toHaveTextContent("3");
    expect(within(kpis).getByLabelText("待审查")).toHaveTextContent("1");
    expect(within(kpis).getByLabelText("审查中")).toHaveTextContent("1");
    expect(within(kpis).getByLabelText("已完成")).toHaveTextContent("1");

    expect(screen.getByRole("tab", { name: "全部 (3)" })).toBeInTheDocument();
    const highRisk = screen.getByRole("link", { name: "高风险专线" });
    const highRow = highRisk.closest("tr")!;
    expect(within(highRow).getByText("高风险")).toBeInTheDocument();
    expect(within(highRow).getByText("王审查")).toBeInTheDocument();
    expect(
      within(screen.getByRole("link", { name: "未评级合同" }).closest("tr")!)
        .getByText("尚未审查"),
    ).toBeInTheDocument();

    expect(
      screen.getByRole("img", {
        name: /案件风险分布：高风险 1 件，中风险 1 件，低风险 0 件，提示 0 件，未评级 1 件/,
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "确认质保条款" }),
    ).toHaveAttribute("href", "/cases/c1");
    expect(
      screen.getByRole("link", { name: /新建案件/ }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "审查中 (1)" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("link", { name: "已完成合同" }),
      ).not.toBeInTheDocument(),
    );
    expect(screen.getByRole("link", { name: "高风险专线" })).toBeInTheDocument();

    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "风险等级" }),
      "中风险",
    );
    expect(
      await screen.findByText("没有匹配的案件"),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "重置筛选" }));
    expect(
      await screen.findByRole("link", { name: "已完成合同" }),
    ).toBeInTheDocument();
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "筛选合同类型" }),
      "服务合同",
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("link", { name: "高风险专线" }),
      ).not.toBeInTheDocument(),
    );
    expect(screen.getByRole("link", { name: "已完成合同" })).toBeInTheDocument();
  });

  it("keeps the case list readable when the workbench snapshot fails", async () => {
    const service = new CasesService();
    service.failWorkbench = true;
    open(service);
    await screen.findByRole("heading", { name: "案件中心", level: 1 });

    expect(
      screen.getByText("风险、负责人和右侧统计暂未加载"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "高风险专线" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("未报告").length).toBeGreaterThan(0);
    expect(screen.getByRole("combobox", { name: "风险等级" })).toBeDisabled();
    expect(screen.getByText("风险分布没有加载")).toBeInTheDocument();
    expect(screen.getByText("没有未完成任务")).toBeInTheDocument();
  });

  it("opens the create dialog from a deep link without bypassing permissions", async () => {
    open(new CasesService(), "/cases?new=1");
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByRole("heading", { name: "新建案件" }),
    ).toBeInTheDocument();
    expect(within(dialog).getByLabelText("案件标题")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "创建" })).toBeInTheDocument();
  });
});
