// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “HomePage snapshot consumers, HomePage persistent task workflow”; representative assertions cover uses server metrics, mutually exclusive business states and explicit risk levels, fetches each selected period and updates the displayed snapshot, renders a zero risk denominator without claiming any risk percentage. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-04
// Revision: r5
// Copyright: See LICENSE
// End file metadata.

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
  CaseDetail,
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

function task(overrides: Partial<WorkbenchTask> & { can_complete?: boolean } = {}): WorkbenchTask & { can_complete?: boolean } {
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
    can_complete: true,
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
    can_create_task: true,
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
        can_create_task: false,
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

const session = {
  audience: "client",
  actor: {
    id: "me",
    display_name: "周审查",
    role: "reviewer",
    tenant_id: "tenant",
    org_id: "org",
  },
  active_context: {
    membership_id: "membership-me",
    tenant_id: "tenant",
    tenant_name: "租户",
    org_id: "org",
    org_name: "组织",
    context_version: "1",
  },
  principal_id: "principal-me",
  csrf_token: "test-csrf",
  context_switch_enabled: false,
};

class HomeService {
  data = snapshot();
  requests: { method: string; path: string; body: unknown }[] = [];
  periodDashboards: Partial<
    Record<WorkbenchPeriod, Partial<WorkbenchDashboard>>
  > = {};
  snapshotFailures: number[] = [];
  capabilityUnsupported = false;
  completionFailures: number[] = [];
  revokeCreateCapabilityOnFailure = false;
  sessionGate: ReturnType<typeof Promise.withResolvers<void>> | null = null;
  caseDetail: CaseDetail | null = null;
  readinessResponse: unknown = null;
  readinessFailure: number | null = null;
  readinessGate: ReturnType<typeof Promise.withResolvers<void>> | null = null;
  caseDetailGate: ReturnType<typeof Promise.withResolvers<void>> | null = null;
  reviewGate: ReturnType<typeof Promise.withResolvers<void>> | null = null;
  reviewResponse: unknown = null;
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
    if (url.pathname === "/api/session") {
      if (this.sessionGate) await this.sessionGate.promise;
      return json(session);
    }
    if (url.pathname === "/api/intakes/pending")
      return json({ items: [], next_cursor: null });
    if (url.pathname === "/api/workbench") {
      if (this.snapshotGate) await this.snapshotGate.promise;
      if (url.searchParams.has("task_capabilities") && this.capabilityUnsupported)
        return json({ error: { message: "旧服务不支持任务能力字段" } }, 422);
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
    if (parts[2] === "cases" && parts.length === 4 && method === "GET") {
      if (this.caseDetailGate) await this.caseDetailGate.promise;
      return this.caseDetail ? json(this.caseDetail) : json({ error: { message: "未匹配的请求" } }, 404);
    }
    if (parts[2] === "cases" && parts[4] === "review-readiness" && method === "GET") {
      if (this.readinessGate) await this.readinessGate.promise;
      if (this.readinessFailure) return json({ error: { message: "准备状态不可用" } }, this.readinessFailure);
      return json(this.readinessResponse);
    }
    if (parts[2] === "cases" && parts[4] === "reviews" && method === "GET") {
      if (this.reviewGate) await this.reviewGate.promise;
      if (this.reviewResponse) return json(this.reviewResponse);
      return json({ error: { message: "审查历史已撤权" } }, 403);
    }
    if (parts[4] === "tasks" && method === "POST") {
      const failure = this.createFailures.shift();
      if (failure)
        if (this.revokeCreateCapabilityOnFailure) {
          const caseId = decodeURIComponent(parts[3]!);
          const targetCase = this.data.cases.find((item) => item.id === caseId);
          if (targetCase) targetCase.can_create_task = false;
        }
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
      const failure = this.completionFailures.shift();
      if (failure) {
        const taskToRevoke = this.data.tasks.find((item) => item.id === decodeURIComponent(parts[5]!));
        if (taskToRevoke) taskToRevoke.can_complete = false;
        return json({ error: { message: "任务完成权限已变化" } }, failure);
      }
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
  setSessionIdentity({
    id: session.actor.id,
    role: session.actor.role,
    audience: session.audience,
    tenant_id: session.actor.tenant_id,
    org_id: session.actor.org_id,
    principal_id: session.principal_id,
    active_context: session.active_context,
    csrf_token: session.csrf_token,
  });
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
  return { service, client, user: userEvent.setup() };
}

function caseDetail(caseId = "case-1", updatedAt = at): CaseDetail {
  return {
    id: caseId,
    title: "专线采购合同",
    contract_type: "procurement_ict",
    created_at: at,
    updated_at: updatedAt,
    documents: [],
    materials: [],
    material_versions: [],
    material_selection: { version: 1, status: "not_applicable", document_ids: [], candidate_document_ids: [], primary_material_id: null },
    latest_run: null,
    finding_counts: null,
    activity: [],
    revision_pending: false,
    configuration_pending: false,
    subject_context_changed_after_latest_run: false,
    members: [],
    business_line: "ENT",
    scenario: "telecom_dedicated_line",
    business_status: "active",
    generations: [],
    remediations: [],
    reports: [],
    archives: [],
    archived_at: null,
  };
}

function readiness(blocker = "required_materials_missing", message = "缺少主合同") {
  return {
    case_id: "case-1",
    revision: "a".repeat(64),
    business_at: null,
    can_start: false,
    material_selection_version: 1,
    materials: [],
    scopes: [{
      id: "full_review",
      status: "blocked",
      can_start: false,
      rule_pack: null,
      rule_set: null,
      checks: [],
      blockers: [{ code: blocker, status: "blocked", message, missing_dependencies: [], next_action: "补齐后重试" }],
    }],
  };
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
    const service = new HomeService();
    open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });

    const caseWork = screen.getByRole("region", { name: "按案件处理" });
    expect(within(caseWork).getByText(/确认质保条款.*处理中/)).toBeInTheDocument();
    expect(within(caseWork).getAllByText(/截止/).length).toBeGreaterThan(0);
    expect(within(caseWork).getByRole("link", { name: "专线采购合同" })).toHaveAttribute(
      "href",
      "/cases/case-1",
    );
    expect(within(caseWork).getByText(/实际下一步及可执行动作以案件页服务端信息为准/)).toBeInTheDocument();

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
    expect(within(rows[0]!).getByText("业务范围未设置")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("尚未审查")).toBeInTheDocument();
    expect(
      within(rows[1]!).getByText("政企业务 / 政企专线"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /补充验收标准.*责任人 missing-member/ }),
    ).toHaveAttribute("href", "/cases/case%2Fold/reviews/run%20old");
    const audit = within(
      screen.getByRole("region", { name: "最近业务记录" }),
    ).getAllByRole("listitem");
    expect(audit[0]).toHaveTextContent(
      "平台级事件更新了平台规则陈复核，更新策略",
    );
    expect(
      within(audit[1]!).getByRole("link", { name: "未命名案件" }),
    ).toHaveAttribute("href", "/cases/case%2Fold");
    const technical = within(audit[1]!).getByText("案件标识");
    expect(technical.closest("details")).toHaveTextContent("case/old");
  });

  it("shows a profile deadline without describing the case as high risk", async () => {
    const service = new HomeService();
    service.data.cases = [
      caseRecord({
        id: "case-due-only",
        title: "仅有案件期限",
        profile: {
          case_id: "case-due-only",
          contract_category: "采购",
          customer: "客户",
          due_at: "2026-11-20T00:00:00Z",
          number: "C-1",
          owner_id: "me",
          supplier: "供应商",
          updated_at: at,
          version: 1,
        },
      }),
    ];
    service.data.tasks = [];
    service.data.remediations = [];
    open(service);

    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    const dueOnlyCase = within(caseWork)
      .getByRole("link", { name: "仅有案件期限" })
      .closest("li");
    expect(dueOnlyCase).toHaveTextContent("案件期限");
    expect(dueOnlyCase).not.toHaveTextContent("高风险");
  });

  it("keeps an unreviewed case with no task or deadline available for next-step review", async () => {
    const service = new HomeService();
    service.data.cases = [caseRecord({ title: "尚未审查且无期限" })];
    service.data.tasks = [];
    service.data.remediations = [];
    service.caseDetail = { ...caseDetail(), title: "尚未审查且无期限" };
    service.readinessResponse = readiness();
    const { user } = open(service);

    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    const caseRow = within(caseWork).getByRole("link", { name: "尚未审查且无期限" }).closest("li");
    expect(caseRow).not.toHaveTextContent("尚未审查且无期限 ·");
    expect(within(caseRow!).getByRole("button", { name: "查看实际下一步" })).toBeInTheDocument();
    await user.click(within(caseRow!).getByRole("button", { name: "查看实际下一步" }));
    expect(await within(caseRow!).findByText("缺少主合同")).toBeInTheDocument();
  });

  it("loads next-step details only for the case the user expands and keeps write actions out of the workbench", async () => {
    const service = new HomeService();
    const { user } = open(service);
    const caseWork = await screen.findByRole("region", { name: "按案件处理" });

    expect(service.requests.filter((request) => request.path.includes("/review-readiness"))).toHaveLength(0);
    const targetCase = within(caseWork).getByRole("link", { name: "专线采购合同" }).closest("li");
    expect(targetCase).not.toBeNull();
    await user.click(within(targetCase!).getByRole("button", { name: "查看实际下一步" }));

    await screen.findByRole("heading", { name: "案件下一步不可用" });
    expect(service.requests.filter((request) => request.path === "/api/cases/case-1")).toHaveLength(1);
    expect(service.requests.filter((request) => request.path.includes("/review-readiness"))).toHaveLength(0);
    expect(screen.queryByRole("button", { name: "开始审查" })).not.toBeInTheDocument();
  });

  it("shows server readiness links with the selected case route", async () => {
    const service = new HomeService();
    service.caseDetail = caseDetail();
    service.readinessResponse = readiness();
    const { user } = open(service);
    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    const targetCase = within(caseWork).getByRole("link", { name: "专线采购合同" }).closest("li");

    await user.click(within(targetCase!).getByRole("button", { name: "查看实际下一步" }));

    const continueLink = await screen.findByRole("link", { name: "转到材料集合" });
    expect(continueLink).toHaveAttribute("href", "/cases/case-1#case-materials");
    expect(service.requests.filter((request) => request.path === "/api/cases/case-1")).toHaveLength(1);
    expect(service.requests.filter((request) => request.path.startsWith("/api/cases/case-1/review-readiness"))).toHaveLength(1);
  });

  it("shows the same limited-precheck outcome and material next step on the home entry", async () => {
    const service = new HomeService();
    service.caseDetail = caseDetail();
    service.caseDetail.latest_run = { id: "run-1", state: "partial", updated_at: at };
    service.readinessResponse = readiness("required_materials_missing", "缺少采购材料");
    const scope = { id: "single_contract_precheck", rule_set_id: "single-contract-precheck", rule_set_version: "1", rule_set_hash: "51676a95611b6e1bf4c30fc50be82e1d493528f34b89f3ece9dbb5aa6c291596" };
    const attempt = { id: "attempt-1", run_id: "run-1", ordinal: 1, state: "partial", history_kind: "frozen", parsed_document_ids: ["parse-1"], review_scope: scope,
      outcome: { reason: "scope_limited", agent_stage: "failed", retry_cause: "none" } };
    service.reviewResponse = { run: { id: "run-1", case_id: "case-1", state: "partial" },
      review_attempts: { attempt, attempts: [attempt] }, snapshot: { document_ids: ["doc-1"], review_scope: scope },
      documents: [{ id: "doc-1", role: "draft_contract", filename: "合同.txt", parse_status: "ready", parsed_document_id: "parse-1" }],
      findings: [{ id: "finding-1", decisions: [], evidence: [] }], evaluations: [{ id: "eval-1", domain: "payment_sum", relation: "insufficient", rule_id: "single-payment-sum-v1", rule_version: "single-payment-sum-v1", capability_id: "single_contract.payment_sum", reason: "需人工核对" }], execution: null, audit_events: [] };
    const { user } = open(service);
    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    const target = within(caseWork).getByRole("link", { name: "专线采购合同" }).closest("li")!;
    await user.click(within(target).getByRole("button", { name: "查看实际下一步" }));
    const summary = await within(target).findByRole("region", { name: "审查结果、范围与人工进度" });
    expect(summary).toHaveTextContent("单合同预审已结束");
    expect(summary).toHaveTextContent("智能审查执行失败");
    expect(summary).toHaveTextContent("1 项尚无人工判断");
    expect(within(target).getByRole("link", { name: "转到材料集合" })).toHaveAttribute("href", "/cases/case-1#case-materials");
    expect(within(target).queryByRole("button", { name: "开始完整审查" })).not.toBeInTheDocument();
  });

  it("fails closed when the case detail response belongs to another case", async () => {
    const service = new HomeService();
    service.caseDetail = caseDetail("case-2");
    service.caseDetail.latest_run = { id: "wrong-run", state: "completed", updated_at: at };
    service.readinessResponse = readiness();
    const { user } = open(service);
    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    const targetCase = within(caseWork).getByRole("link", { name: "专线采购合同" }).closest("li");

    await user.click(within(targetCase!).getByRole("button", { name: "查看实际下一步" }));

    expect(await screen.findByRole("heading", { name: "案件身份不匹配" })).toBeInTheDocument();
    expect(service.requests.filter((request) => request.path.startsWith("/api/cases/case-1/review-readiness"))).toHaveLength(0);
    expect(service.requests.filter((request) => request.path === "/api/cases/case-1/reviews/wrong-run")).toHaveLength(0);
  });

  it("refreshes readiness when case material identity changes and does not keep the previous projection while loading", async () => {
    const service = new HomeService();
    const originalCase = caseDetail();
    originalCase.documents = [{
      id: "doc-1",
      role: "primary_contract",
      filename: "contract.pdf",
      media_type: "application/pdf",
      digest: "b".repeat(64),
      size: 1,
      parse_status: "pending",
      parse_error: null,
      created_at: at,
      parsed_document: null,
    }];
    service.caseDetail = originalCase;
    service.readinessResponse = readiness("required_materials_missing", "旧材料缺项");
    const { user, client } = open(service);
    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    const targetCase = within(caseWork).getByRole("link", { name: "专线采购合同" }).closest("li");
    await user.click(within(targetCase!).getByRole("button", { name: "查看实际下一步" }));
    expect(await screen.findByText("旧材料缺项")).toBeInTheDocument();

    service.caseDetail = {
      ...originalCase,
      material_selection: { ...originalCase.material_selection!, version: 2 },
      documents: originalCase.documents.map((document) => ({ ...document, parse_status: "completed" })),
    };
    service.readinessResponse = readiness("required_materials_missing", "新材料缺项");
    service.caseDetailGate = Promise.withResolvers<void>();
    service.readinessGate = Promise.withResolvers<void>();
    const invalidation = client.invalidateQueries({ queryKey: ["cases", "case-1"], exact: true });
    await screen.findByRole("heading", { name: "正在读取案件下一步" });
    expect(screen.queryByText("旧材料缺项")).not.toBeInTheDocument();
    service.caseDetailGate.resolve();
    service.caseDetailGate = null;
    await invalidation;

    await screen.findByRole("heading", { name: "等待服务端返回最新准备清单" });
    expect(screen.queryByText("旧材料缺项")).not.toBeInTheDocument();
    expect(service.requests.filter((request) => request.path.startsWith("/api/cases/case-1/review-readiness"))).toHaveLength(2);
    service.readinessGate.resolve();
    service.readinessGate = null;
    expect(await screen.findByText("新材料缺项")).toBeInTheDocument();
  });

  it("clears a previously ready state when readiness is revoked", async () => {
    const service = new HomeService();
    service.caseDetail = caseDetail();
    const ready = readiness();
    ready.can_start = true;
    ready.scopes[0]!.status = "ready";
    ready.scopes[0]!.can_start = true;
    ready.scopes[0]!.blockers = [];
    service.readinessResponse = ready;
    const { user, client } = open(service);
    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    const targetCase = within(caseWork).getByRole("link", { name: "专线采购合同" }).closest("li");
    await user.click(within(targetCase!).getByRole("button", { name: "查看实际下一步" }));
    await screen.findByRole("heading", { name: "可以开始首次审查" });
    const continueLink = screen.getByRole("link", { name: "打开案件继续" });
    expect(continueLink).toHaveAttribute("href", "/cases/case-1#case-next-step-title");
    expect(screen.queryByRole("link", { name: "开始审查" })).not.toBeInTheDocument();

    service.readinessFailure = 403;
    await client.invalidateQueries({ queryKey: ["cases", "case-1", "review-readiness"] });

    expect(await screen.findByRole("heading", { name: "无法确认是否可以开始审查" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "可以开始首次审查" })).not.toBeInTheDocument();
  });

  it("drops the selected next step when role or active context version changes", async () => {
    const service = new HomeService();
    service.caseDetail = caseDetail();
    service.readinessResponse = readiness();
    const { user, client } = open(service);
    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    const targetCase = within(caseWork).getByRole("link", { name: "专线采购合同" }).closest("li");
    await user.click(within(targetCase!).getByRole("button", { name: "查看实际下一步" }));
    expect(await screen.findByText("缺少主合同")).toBeInTheDocument();

    service.snapshotGate = Promise.withResolvers<void>();
    client.setQueryData(["session", "client"], {
      ...session.actor,
      audience: session.audience,
      role: "viewer",
      active_context: { ...session.active_context, context_version: "2" },
      principal_id: session.principal_id,
      csrf_token: session.csrf_token,
      context_switch_enabled: session.context_switch_enabled,
    });

    expect(await screen.findByRole("heading", { name: "正在加载工作台" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "标记完成" })).not.toBeInTheDocument();
    service.snapshotGate.resolve();
    service.snapshotGate = null;
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: "正在加载工作台" })).not.toBeInTheDocument(),
    );
    expect(screen.queryByText("缺少主合同")).not.toBeInTheDocument();
  });

  it("hides cached remediation history while it is revalidated and after revocation", async () => {
    const service = new HomeService();
    const detail = caseDetail();
    detail.latest_run = { id: "run-1", state: "completed", updated_at: at };
    detail.finding_counts = { conflict: 0, insufficient: 0, total: 1, undecided: 0 };
    detail.remediations = [{
      id: "remediation-1",
      case_id: "case-1",
      run_id: "run-1",
      attempt_id: "attempt-1",
      finding_ids: ["finding-1"],
      assignee_id: "me",
      due_at: at,
      measure: "补充整改材料",
      status: "submitted",
      version: 1,
      document_ids: [],
      reason: "",
      verification_run_id: null,
      verification_attempt_id: null,
      created_at: at,
      updated_at: at,
    }];
    service.caseDetail = detail;
    service.reviewGate = Promise.withResolvers<void>();
    const { user, client } = open(service);
    client.setQueryData(["cases", "case-1", "review-history", "run-1"], []);
    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    const targetCase = within(caseWork).getByRole("link", { name: "专线采购合同" }).closest("li");
    await user.click(within(targetCase!).getByRole("button", { name: "查看实际下一步" }));

    expect(await screen.findByRole("heading", { name: "等待审查与执行历史" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "经办已提交修改稿，等待复核" })).not.toBeInTheDocument();
    service.reviewGate.resolve();
    expect(await screen.findByRole("heading", { name: "审查历史没有加载" })).toBeInTheDocument();
  });

  it("shows a scoped empty state and labels unknown severity as unknown", async () => {
    const empty = new HomeService();
    empty.data.cases = [];
    empty.data.tasks = [];
    empty.data.remediations = [];
    open(empty);
    expect(await screen.findByText("当前无可见案件。")).toBeInTheDocument();
    cleanup();

    const unknown = new HomeService();
    unknown.data.cases[0]!.severity = "future_state";
    open(unknown);
    const caseWork = await screen.findByRole("region", { name: "按案件处理" });
    expect(await within(caseWork).findByText("风险级别未知")).toBeInTheDocument();
    expect(within(caseWork).queryByText(/future_state/)).not.toBeInTheDocument();
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
      "/api/workbench?period=week&task_capabilities=1",
      "/api/workbench?period=day&task_capabilities=1",
      "/api/workbench?period=month&task_capabilities=1",
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
        (request) => request.path === "/api/workbench?period=week&task_capabilities=1",
      ),
    ).toHaveLength(2);
  });

  it("hides the cached workbench while the session is being revalidated", async () => {
    const service = new HomeService();
    const { client } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    expect(screen.getByRole("region", { name: "按案件处理" })).toBeInTheDocument();
    expect(within(taskPanel()).getByRole("button", { name: "新建任务" })).toBeInTheDocument();
    expect(within(taskPanel()).getAllByRole("button", { name: "标记完成" }).length).toBeGreaterThan(0);

    service.sessionGate = Promise.withResolvers<void>();
    const revalidation = client.invalidateQueries({ queryKey: ["session", "client"], exact: true });

    expect(await screen.findByRole("heading", { name: "正在加载工作台" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "按案件处理" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("工作台摘要")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "新建任务" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "标记完成" })).not.toBeInTheDocument();

    service.sessionGate.resolve();
    service.sessionGate = null;
    await revalidation;
    expect(await screen.findByRole("heading", { name: "你好，周审查" })).toBeInTheDocument();
  });

  it("hides the cached workbench while its snapshot is being refreshed", async () => {
    const service = new HomeService();
    const { client } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    expect(screen.getByRole("region", { name: "按案件处理" })).toBeInTheDocument();
    expect(within(taskPanel()).getAllByRole("button", { name: "标记完成" }).length).toBeGreaterThan(0);

    service.snapshotGate = Promise.withResolvers<void>();
    const refresh = client.invalidateQueries({ queryKey: ["workbench"] });
    expect(await screen.findByRole("heading", { name: "正在加载工作台" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "按案件处理" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("工作台摘要")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "新建任务" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "标记完成" })).not.toBeInTheDocument();

    service.snapshotGate.resolve();
    service.snapshotGate = null;
    await refresh;
    expect(await screen.findByRole("heading", { name: "你好，周审查" })).toBeInTheDocument();
  });
});

describe("HomePage persistent task workflow", () => {
  it.each([
    ["authorized case", true, false, true],
    ["denied case", false, false, false],
    ["missing field", undefined, false, false],
    ["legacy service", undefined, true, false],
  ])("offers task creation only for authorized cases (%s)", async (_label, canCreate, unsupported, visible) => {
    const service = new HomeService();
    service.capabilityUnsupported = unsupported;
    if (canCreate === undefined) {
      for (const item of service.data.cases) delete item.can_create_task;
    } else if (!canCreate) {
      for (const item of service.data.cases) item.can_create_task = false;
    } else {
      service.data.cases[0]!.can_create_task = true;
    }
    const { user } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });

    if (!visible) {
      expect(screen.queryByRole("button", { name: "新建任务" })).not.toBeInTheDocument();
      expect(screen.queryByRole("combobox", { name: "关联案件" })).not.toBeInTheDocument();
      return;
    }

    await user.click(screen.getByRole("button", { name: "新建任务" }));
    await user.click(screen.getByRole("combobox", { name: "关联案件" }));
    expect(screen.getByRole("option", { name: "专线采购合同" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "设备维保合同" })).not.toBeInTheDocument();
  });

  it("refreshes task creation capability after a refusal and closes the revoked form", async () => {
    const service = new HomeService();
    service.createFailures = [403];
    service.revokeCreateCapabilityOnFailure = true;
    const { user } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    await user.click(screen.getByRole("button", { name: "新建任务" }));
    await chooseOption(user, screen.getByRole("combobox", { name: "关联案件" }), "专线采购合同");
    await chooseOption(user, screen.getByRole("combobox", { name: "责任人" }), "周审查，reviewer");
    await user.type(screen.getByRole("textbox", { name: "任务名称" }), "受限任务");
    await user.click(screen.getByRole("button", { name: "创建并保存任务" }));

    expect(await screen.findByText("无法保存任务，请检查分派权限")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("button", { name: "新建任务" })).not.toBeInTheDocument());
    expect(screen.queryByRole("combobox", { name: "关联案件" })).not.toBeInTheDocument();
    expect(service.requests.filter((request) => request.path.startsWith("/api/workbench"))).toHaveLength(2);
    expect(service.requests.filter((request) => request.path === "/api/session")).toHaveLength(2);
  });

  it("clears the selected task case when the active identity context changes", async () => {
    const service = new HomeService();
    const { user, client } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    await user.click(screen.getByRole("button", { name: "新建任务" }));
    await chooseOption(user, screen.getByRole("combobox", { name: "关联案件" }), "专线采购合同");
    await user.type(screen.getByRole("textbox", { name: "任务名称" }), "旧上下文任务");

    service.snapshotGate = Promise.withResolvers<void>();
    client.setQueryData(["session", "client"], {
      ...session.actor,
      audience: session.audience,
      role: "viewer",
      active_context: { ...session.active_context, context_version: "2" },
      principal_id: session.principal_id,
      csrf_token: session.csrf_token,
      context_switch_enabled: session.context_switch_enabled,
    });

    expect(await screen.findByRole("heading", { name: "正在加载工作台" })).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "关联案件" })).not.toBeInTheDocument();
    service.snapshotGate.resolve();
    service.snapshotGate = null;
    await screen.findByRole("heading", { name: "你好，周审查" });
    expect(screen.queryByRole("combobox", { name: "关联案件" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "任务名称" })).not.toBeInTheDocument();
  });

  it.each([
    ["authorized", true, false, true],
    ["denied", false, false, false],
    ["missing capability", undefined, false, false],
    ["legacy server", undefined, true, false],
  ])("offers task creation only for an affirmative case capability (%s)", async (_label, canCreate, unsupported, visible) => {
    const service = new HomeService();
    service.capabilityUnsupported = unsupported;
    if (canCreate === undefined) delete service.data.cases[0]!.can_create_task;
    else service.data.cases[0]!.can_create_task = canCreate;
    const { user } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    if (visible) {
      expect(screen.getByRole("button", { name: "新建任务" })).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "新建任务" }));
      const caseSelect = screen.getByRole("combobox", { name: "关联案件" });
      await user.click(caseSelect);
      expect(screen.getByRole("option", { name: "专线采购合同" })).toBeInTheDocument();
      expect(screen.queryByRole("option", { name: "设备维保合同" })).not.toBeInTheDocument();
    } else {
      expect(screen.queryByRole("button", { name: "新建任务" })).not.toBeInTheDocument();
      expect(screen.queryByRole("combobox", { name: "关联案件" })).not.toBeInTheDocument();
    }
  });

  it("refreshes capabilities and clears task creation after the selected case is revoked", async () => {
    const service = new HomeService();
    service.createFailures = [403];
    service.revokeCreateCapabilityOnFailure = true;
    const { user } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    await user.click(screen.getByRole("button", { name: "新建任务" }));
    await chooseOption(user, screen.getByRole("combobox", { name: "关联案件" }), "专线采购合同");
    await chooseOption(user, screen.getByRole("combobox", { name: "责任人" }), "周审查，reviewer");
    await user.type(screen.getByRole("textbox", { name: "任务名称" }), "受限任务");
    await user.click(screen.getByRole("button", { name: "创建并保存任务" }));

    expect(await screen.findByText("无法保存任务，请检查分派权限")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("button", { name: "新建任务" })).not.toBeInTheDocument());
    expect(screen.queryByRole("combobox", { name: "关联案件" })).not.toBeInTheDocument();
    expect(service.requests.filter((request) => request.path.startsWith("/api/workbench"))).toHaveLength(2);
    expect(service.requests.filter((request) => request.path === "/api/session")).toHaveLength(2);
  });

  it.each([
    ["granted", true, false, true],
    ["denied", false, false, false],
    ["missing", undefined, false, false],
    ["unsupported server", undefined, true, false],
  ])("shows completion only when the server snapshot grants it (%s)", async (_label, canComplete, unsupported, visible) => {
    const service = new HomeService();
    service.capabilityUnsupported = unsupported;
    const targetTask = task();
    if (canComplete === undefined) delete targetTask.can_complete;
    else targetTask.can_complete = canComplete;
    service.data.tasks = [targetTask];
    service.data.cases = [caseRecord()];
    service.data.members = [];
    open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    const panel = within(taskPanel());
    expect(panel.getByRole("link", { name: "确认质保条款" })).toHaveAttribute("href", "/cases/case-1");
    if (visible) {
      expect(panel.getByRole("button", { name: "标记完成" })).toBeInTheDocument();
    } else {
      expect(panel.queryByRole("button", { name: "标记完成" })).not.toBeInTheDocument();
    }
  });

  it("refreshes authorization after a completion refusal and hides the revoked action", async () => {
    const service = new HomeService();
    service.completionFailures = [403];
    service.data.tasks = [task()];
    service.data.cases = [caseRecord()];
    const { user } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    const workbenchReadsBeforeCompletion = service.requests.filter((request) => request.path.startsWith("/api/workbench")).length;
    await user.click(within(taskPanel()).getByRole("button", { name: "标记完成" }));

    expect(await screen.findByText("任务完成权限已变化")).toBeInTheDocument();
    await waitFor(() => expect(within(taskPanel()).queryByRole("button", { name: "标记完成" })).not.toBeInTheDocument());
    expect(service.requests.filter((request) => request.path.startsWith("/api/workbench"))).toHaveLength(workbenchReadsBeforeCompletion + 1);
    expect(service.requests.filter((request) => request.path === "/api/session")).toHaveLength(2);
  });

  it("does not reuse a granted action when authorization snapshot refresh fails", async () => {
    const service = new HomeService();
    service.completionFailures = [409];
    service.data.tasks = [task()];
    service.data.cases = [caseRecord()];
    const { user } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    service.snapshotFailures.push(503);
    await user.click(within(taskPanel()).getByRole("button", { name: "标记完成" }));

    expect(await screen.findByRole("heading", { name: "工作台数据没有加载" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "标记完成" })).not.toBeInTheDocument();
  });

  it("does not carry completion capability across an identity or context switch", async () => {
    const service = new HomeService();
    service.data.tasks = [task()];
    service.data.cases = [caseRecord()];
    const { client } = open(service);
    await screen.findByRole("heading", { name: "你好，周审查" });
    expect(within(taskPanel()).getByRole("button", { name: "标记完成" })).toBeInTheDocument();

    service.data.tasks[0]!.can_complete = false;
    service.snapshotGate = Promise.withResolvers<void>();
    client.setQueryData(["session", "client"], {
      ...session.actor,
      audience: session.audience,
      role: "viewer",
      active_context: { ...session.active_context, context_version: "2" },
      principal_id: session.principal_id,
      csrf_token: session.csrf_token,
      context_switch_enabled: session.context_switch_enabled,
    });

    expect(await screen.findByRole("heading", { name: "正在加载工作台" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "标记完成" })).not.toBeInTheDocument();
    service.snapshotGate.resolve();
    service.snapshotGate = null;
    await screen.findByRole("heading", { name: "你好，周审查" });
    expect(screen.queryByRole("button", { name: "标记完成" })).not.toBeInTheDocument();
  });

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
          (request) => request.path === "/api/workbench?period=week&task_capabilities=1",
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
