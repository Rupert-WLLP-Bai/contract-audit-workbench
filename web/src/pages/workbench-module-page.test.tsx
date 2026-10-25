import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import type {
  KnowledgeInput,
  WorkbenchDocument,
  WorkbenchRemediation,
  WorkbenchRun,
  WorkbenchSnapshot,
  WorkbenchTask,
  WorkbenchTaskInput,
  WorkbenchTaskUpdateInput,
} from "../api/types";
import { WorkbenchModulePage } from "./workbench-module-page";

const at = "2026-10-21T06:00:00Z";
const later = "2026-10-21T07:00:00Z";
const clients: QueryClient[] = [];
type User = ReturnType<typeof userEvent.setup>;

function task(overrides: Partial<WorkbenchTask> = {}): WorkbenchTask {
  return {
    id: "task/assigned", case_id: "case/alpha", title: "核实付款依据", kind: "decision",
    assignee_id: "reviewer-1", due_at: "2026-10-28T15:59:59Z", status: "open", priority: "high",
    finding_id: "finding/payment", version: 7, created_by: "reviewer-2", created_at: at, updated_at: at,
    ...overrides,
  };
}

function document(overrides: Partial<WorkbenchDocument> = {}): WorkbenchDocument {
  return {
    id: "document/source", case_id: "case/alpha", case_title: "专线采购案件", role: "draft_contract",
    filename: "采购原件.pdf", media_type: "application/pdf", digest: "original-sha256", size: 2048,
    parse_status: "ready", parse_error: null, created_at: at, superseded: false,
    parsed_document: {
      id: overrides.id ? `parsed/${overrides.id}` : "parsed/source", document_id: overrides.id ?? "document/source", original_digest: "original-sha256",
      parser_id: "pdf-parser", parser_version: "3.2.1", config_hash: "config-sha256",
      normalization_version: "normalization-v2", normalized_digest: "normalized-sha256",
      ir_digest: "ir-sha256", artifact_digest: "artifact-sha256", created_at: at, status: "ready",
    },
    ...overrides,
  };
}

function run(overrides: Partial<WorkbenchRun> = {}): WorkbenchRun {
  return {
    id: "run/current", case_id: "case/alpha", case_title: "专线采购案件", state: "completed",
    created_at: at, updated_at: later, attempt_id: "attempt/frozen-3", started_at: at, finished_at: later,
    usage: {
      input_tokens: 120, output_tokens: 30, total_tokens: 150, calls: 7, unknown_calls: 2,
      sources: ["provider", "gateway"], known_subtotals: { input_tokens: 120, output_tokens: 30, total_tokens: 150 },
      cost: null,
    },
    ...overrides,
  };
}

function remediation(overrides: Partial<WorkbenchRemediation> = {}): WorkbenchRemediation {
  return {
    id: "remediation/open", case_id: "case/alpha", case_title: "专线采购案件", run_id: "run/original",
    attempt_id: "attempt/original", finding_ids: ["finding/payment"], assignee_id: "reviewer-2",
    due_at: "2026-10-29T15:59:59Z", measure: "补充付款材料", status: "open", version: 4,
    document_ids: ["document/source", "not-in-authorized-snapshot"], reason: "缺少签章",
    verification_run_id: null, verification_attempt_id: null, created_at: at, updated_at: later,
    ...overrides,
  };
}

function snapshot(): WorkbenchSnapshot {
  return {
    schema_version: "workbench/v1", generated_at: later, period: "week",
    dashboard: {
      visible_cases: 2, my_pending_tasks: 1, overdue_tasks: 0, pending_review_cases: 1,
      remediation_cases: 1, completed_reviews: 1, average_duration_ms: null, progress: [], risks: [],
    },
    cases: [
      {
        id: "case/alpha", title: "专线采购案件", contract_type: "procurement_ict", document_count: 2,
        pending_decisions: 1, latest_run: { id: "run/current", state: "completed", updated_at: later },
        updated_at: later, business_line: "ENT", scenario: "telecom_dedicated_line", business_status: "active",
        archived_at: null, severity: "high", progress: 80,
        profile: {
          case_id: "case/alpha", number: "ENT-026", customer: "甲方", supplier: "乙方",
          contract_category: "专线", owner_id: "reviewer-2", due_at: null, version: 2, updated_at: at,
        },
      },
      {
        id: "case-empty", title: "未启动案件", contract_type: "procurement_ict", document_count: 1,
        pending_decisions: 0, latest_run: null, updated_at: at, archived_at: null,
        profile: null, severity: null, progress: null,
      },
    ],
    profiles: [], relations: [],
    tasks: [
      task(),
      task({ id: "task-created", title: "我创建的复核", assignee_id: "reviewer-2", created_by: "reviewer-1", status: "in_progress", priority: "low" }),
      task({ id: "task-completed", title: "历史已办任务", assignee_id: "reviewer-2", created_by: null, status: "completed", due_at: null }),
    ],
    knowledge: [
      { id: "knowledge-policy", title: "采购安全制度", version: "2.0.0", body: "签约前必须完成安全性评估。SPECIAL-CLAUSE 必须留存。", source_type: "synthetic_policy", effective_at: null, created_by: "reviewer-1", created_at: at },
      { id: "knowledge-reference", title: "外部参考资料", version: "2026-10", body: "供应商应提供资质材料。", source_type: "reference", effective_at: at, created_by: "former-member", created_at: later },
    ],
    members: [
      { id: "reviewer-1", display_name: "审查员甲", role: "reviewer", tenant_id: "tenant-public", org_id: "org-alpha" },
      { id: "reviewer-2", display_name: "管理员乙", role: "admin", tenant_id: "tenant-public", org_id: "org-beta" },
      { id: "reviewer-idle", display_name: "未活动成员", role: "reviewer", tenant_id: "tenant-public", org_id: "org-beta" },
    ],
    remediations: [
      remediation(),
      remediation({ id: "remediation-reopened", measure: "重新整改付款条款", status: "reopened" }),
      remediation({ id: "remediation-submitted", measure: "等待复核签章", status: "submitted", verification_run_id: "run/verification", verification_attempt_id: "attempt/verification" }),
      remediation({ id: "remediation-closed", measure: "已关闭签章整改", status: "closed" }),
    ],
    reports: [
      { id: "report/frozen-2", case_id: "case/alpha", case_title: "专线采购案件", version: 2, run_id: "run/report-2", attempt_id: "attempt/report-2", input_hash: "input-v2-sha256", output_hash: "output-v2-sha256", decision_snapshot_hash: "decision-v2-sha256", digest: "report-v2-sha256", created_at: later },
      { id: "report/frozen-1", case_id: "case/alpha", case_title: "专线采购案件", version: 1, run_id: "run/report-1", attempt_id: "attempt/report-1", input_hash: "input-v1-sha256", output_hash: "output-v1-sha256", decision_snapshot_hash: "decision-v1-sha256", digest: "report-v1-sha256", created_at: at },
      { id: "report-single", case_id: "case-empty", case_title: "独立单版案件", version: 1, run_id: "run-single", attempt_id: "attempt-single", input_hash: "input-single", output_hash: "output-single", decision_snapshot_hash: "decision-single", digest: "digest-single", created_at: at },
    ],
    documents: [
      document(),
      document({ id: "document/mismatch", filename: "来源不一致.txt", digest: "replaced-original-sha256", parse_status: "failed" }),
      document({ id: "document-unparsed", case_id: "case-empty", case_title: "未启动案件", filename: "尚未解析.txt", parsed_document: null, parse_status: "pending", size: 0 }),
    ],
    runs: [run(), run({ id: "run-no-usage", case_id: "case-empty", case_title: "失败且无用量的案件", state: "failed", usage: null, attempt_id: null, started_at: null, finished_at: null })],
    audit: [
      { id: "audit-case-1", case_id: "case/alpha", case_title: "专线采购案件", actor_id: "reviewer-1", actor_name: "审查员甲", action: "task.created", at, summary: "创建付款核对任务" },
      { id: "audit-case-2", case_id: "case/alpha", case_title: "专线采购案件", actor_id: "reviewer-2", actor_name: "管理员乙", action: "remediation.submitted", at: "2026-10-21T05:00:00Z", summary: "提交签章材料" },
      { id: "audit-platform", case_id: null, case_title: null, actor_id: "reviewer-2", actor_name: "管理员乙", action: "rulepack.published", at, summary: "发布运营商规则包" },
      { id: "audit-mine-platform", case_id: null, case_title: null, actor_id: "reviewer-1", actor_name: "审查员甲", action: "knowledge.created", at: later, summary: "维护安全制度" },
      { id: "audit-detached", case_id: "case-not-in-snapshot", case_title: null, actor_id: "reviewer-2", actor_name: "管理员乙", action: "case.updated", at, summary: "更新旧案件资料" },
    ],
    templates: [{ id: "template/line", name: "专线服务模板", version: "3.0.0", digest: "template-sha256", fields: [
      { key: "supplier", label: "供应商", type: "text", required: true },
      { key: "note", label: "备注", type: "textarea", required: false },
    ] }],
    model: { name: "model/configured", ocr_name: "ocr/model", status: "configured", label: "已登记审查模型" },
    tools: [
      { id: "tool/read-only", name: "授权证据检索", description: "读取案件内证据", capabilities: ["search", "read"], calls: 19, last_status: "ready" },
      { id: "tool/no-data", name: "尚未使用工具", description: "只返回登记信息", capabilities: [], calls: 0, last_status: null },
    ],
    health: [
      { id: "database", name: "业务数据库", status: "ready", detail: "读写检查完成", observed_at: at },
      { id: "notifications", name: "任务通知", status: "unavailable", detail: "通知服务不可用", observed_at: later },
      { id: "objects", name: "材料对象存储", status: "unknown", detail: "没有返回探测结果", observed_at: at },
    ],
  };
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

// Only HTTP is replaced: response parsing, query invalidation and all page
// projections run against the same stateful service fixture.
class WorkbenchService {
  snapshot = snapshot();
  responseSnapshot: unknown = undefined;
  actorId = "reviewer-1";
  requests: { key: string; body: unknown }[] = [];
  failures = new Map<string, { status: number; message: string }[]>();
  taskVersions = new Map<string, number>();

  fail(key: string, status: number, message: string) {
    this.failures.set(key, [...(this.failures.get(key) ?? []), { status, message }]);
  }

  handle = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = String(input);
    const method = init?.method ?? "GET";
    const key = `${method} ${path}`;
    const body: unknown = typeof init?.body === "string" ? JSON.parse(init.body) : null;
    this.requests.push({ key, body });
    const failure = this.failures.get(key)?.shift();
    if (failure) return json({ error: { code: failure.status === 409 ? "conflict" : "request_failed", message: failure.message, retryable: false } }, failure.status);
    if (key === "GET /api/session") return json({
      audience: "client", actor: { id: this.actorId, display_name: "审查员甲", role: "reviewer", tenant_id: "tenant-public", org_id: "org-alpha" },
    });
    if (key === "GET /api/workbench?period=week") return json(this.responseSnapshot ?? this.snapshot);
    if (key === "GET /api/settings") return json({ model: { status: "disabled", label: "服务端规则模式" }, queue: { depth: null, oldest_wait_ms: null }, redaction_policy: "案件正文按授权脱敏", users: [] });
    const parts = new URL(path, "http://workbench.test").pathname.split("/").map(decodeURIComponent);
    if (method === "POST" && parts[2] === "cases" && parts[4] === "tasks") {
      const submitted = body as WorkbenchTaskInput;
      if (!this.snapshot.cases.some((item) => item.id === parts[3]) || !this.snapshot.members.some((item) => item.id === submitted.assignee_id)) return json({ error: "案件或责任人不可见" }, 403);
      const created = task({ ...submitted, id: "task/server-created", case_id: parts[3], version: 1, created_by: this.actorId, updated_at: later });
      this.snapshot.tasks.push(created);
      return json(created, 201);
    }
    if (method === "PUT" && parts[2] === "cases" && parts[4] === "tasks") {
      const submitted = body as WorkbenchTaskUpdateInput;
      const index = this.snapshot.tasks.findIndex((item) => item.id === parts[5] && item.case_id === parts[3]);
      const current = this.snapshot.tasks[index];
      if (!current) return json({ error: "任务不存在" }, 404);
      const version = this.taskVersions.get(current.id) ?? current.version;
      if (submitted.expected_version !== version) return json({ error: { code: "conflict", message: "该任务版本已更新，请刷新后重试" } }, 409);
      const { expected_version: _expectedVersion, ...changes } = submitted;
      const updated = { ...current, ...changes, version: version + 1, updated_at: later };
      this.snapshot.tasks[index] = updated;
      return json(updated);
    }
    if (key === "POST /api/knowledge") {
      const submitted = body as KnowledgeInput;
      if (!submitted.title.trim() || !submitted.version.trim() || !submitted.body.trim()) return json({ error: "标题、版本与全文不能为空" }, 422);
      if (this.snapshot.knowledge.some((item) => item.id === submitted.id)) return json({ error: "知识条目 ID 已存在" }, 409);
      const created = { ...submitted, id: submitted.id ?? "knowledge-server-generated", created_by: this.actorId, created_at: later };
      this.snapshot.knowledge.push(created);
      return json(created, 201);
    }
    return json({ error: `没有测试路由：${key}` }, 404);
  };
}

function open(module: string, service = new WorkbenchService()) {
  vi.stubGlobal("fetch", vi.fn(service.handle));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  clients.push(client);
  const view = render(<QueryClientProvider client={client}>
    <MemoryRouter initialEntries={[`/workbench/${module}`]}>
      <Routes><Route path="/workbench/:module" element={<WorkbenchModulePage />} /></Routes>
    </MemoryRouter>
  </QueryClientProvider>);
  return { ...view, service, user: userEvent.setup() };
}

async function list(title: string) {
  return within(await screen.findByRole("region", { name: `${title}记录` }));
}

function details() {
  return within(screen.getByRole("complementary", { name: "所选记录详情" }));
}

function expectField(label: string, value: string) {
  expect(details().getByText(label, { selector: "dt" }).nextElementSibling).toHaveTextContent(value);
}

async function detailTab(user: User, name: string) {
  await user.click(within(screen.getByRole("tablist", { name: "记录详情分类" })).getByRole("tab", { name }));
}

async function category(user: User, title: string, name: string) {
  await user.click(within(screen.getByRole("tablist", { name: `${title}分类` })).getByRole("tab", { name }));
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("workbench tasks", () => {
  it("uses assignee and creator IDs for task tabs and resolves authorized case details", async () => {
    const { user } = open("tasks");
    const rows = await list("审查任务");
    expect(await rows.findByRole("button", { name: /核实付款依据/ })).toHaveAttribute("aria-pressed", "true");
    expect(rows.queryByRole("button", { name: /我创建的复核/ })).not.toBeInTheDocument();
    expect(rows.queryByRole("button", { name: /历史已办任务/ })).not.toBeInTheDocument();
    expectField("责任人", "审查员甲");
    expectField("优先级", "high");
    expectField("版本", "7");
    expect(details().getByRole("link", { name: "打开关联记录" })).toHaveAttribute("href", "/cases/case%2Falpha/reviews/run%2Fcurrent");
    await detailTab(user, "处理记录");
    expect(details().getByText(/创建付款核对任务/)).toBeInTheDocument();
    expect(details().getByText(/提交签章材料/)).toBeInTheDocument();
    expect(details().queryByText(/维护安全制度/)).not.toBeInTheDocument();
    await detailTab(user, "关联材料");
    expect(details().getByRole("link", { name: "读取 采购原件.pdf" })).toHaveAttribute("href", "/api/cases/case%2Falpha/documents/document%2Fsource/original");
    expect(details().queryByRole("link", { name: "读取 尚未解析.txt" })).not.toBeInTheDocument();
    await detailTab(user, "协作成员");
    expectField("当前责任人", "审查员甲");
    expectField("案件负责人", "管理员乙");

    await category(user, "审查任务", "我发起的");
    expect(screen.getByRole("status")).toHaveTextContent("服务端创建人 ID");
    expect(rows.getByRole("button", { name: /我创建的复核/ })).toBeInTheDocument();
    expect(rows.queryByRole("button", { name: /核实付款依据|历史已办任务/ })).not.toBeInTheDocument();
    await category(user, "审查任务", "已完成");
    expect(rows.getByRole("button", { name: /历史已办任务/ })).toBeInTheDocument();
    expect(details().queryByRole("button", { name: "标记任务完成" })).not.toBeInTheDocument();
    await user.type(screen.getByRole("searchbox"), "does-not-exist");
    expect(rows.getByText("没有符合此分类或搜索词的记录。")).toBeInTheDocument();
    expect(details().getByText("选择一条记录查看服务端返回的详情。")).toBeInTheDocument();
    expect(details().queryByRole("link")).not.toBeInTheDocument();
  });

  it("creates a dated task and moves its persisted completion to the completed tab", async () => {
    const service = new WorkbenchService();
    service.snapshot.tasks = [];
    const { user } = open("tasks", service);
    const rows = await list("审查任务");
    expect(rows.getByText("快照没有返回记录。")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "分派任务" }));
    expect(screen.getByRole("button", { name: "保存任务" })).toBeDisabled();
    await user.selectOptions(screen.getByLabelText("关联案件"), "case/alpha");
    await user.selectOptions(screen.getByLabelText("责任人"), "reviewer-1");
    await user.type(screen.getByLabelText("任务名称"), "  核对新合同  ");
    fireEvent.change(screen.getByLabelText("截止日期"), { target: { value: "2026-10-28" } });
    await user.click(screen.getByRole("button", { name: "保存任务" }));

    expect(await rows.findByRole("button", { name: /核对新合同/ })).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByLabelText("任务名称")).not.toBeInTheDocument());
    expectField("任务 ID", "task/server-created");
    expectField("截止时间", "2026-10-28T23:59:59+08:00");
    expectField("版本", "1");
    expect(service.requests.find((item) => item.key === "POST /api/cases/case%2Falpha/tasks")?.body).toEqual({
      title: "核对新合同", kind: "general", assignee_id: "reviewer-1", due_at: "2026-10-28T23:59:59+08:00",
      status: "open", priority: "medium", finding_id: null,
    });
    await user.click(details().getByRole("button", { name: "标记任务完成" }));
    await waitFor(() => expect(rows.queryByRole("button", { name: /核对新合同/ })).not.toBeInTheDocument());
    await category(user, "审查任务", "已完成");
    expect(await rows.findByRole("button", { name: /核对新合同/ })).toBeInTheDocument();
    expectField("状态", "completed");
    expectField("版本", "2");
    expect(details().queryByRole("button", { name: "标记任务完成" })).not.toBeInTheDocument();
    expect(service.requests.find((item) => item.key === "PUT /api/cases/case%2Falpha/tasks/task%2Fserver-created")?.body).toEqual({
      title: "核对新合同", kind: "general", assignee_id: "reviewer-1", due_at: "2026-10-28T23:59:59+08:00",
      status: "completed", priority: "medium", finding_id: null, expected_version: 1,
    });
  });

  it("retains task input after an authorization failure and accepts a task without a deadline", async () => {
    const service = new WorkbenchService();
    service.snapshot.tasks = [];
    service.fail("POST /api/cases/case-empty/tasks", 403, "该案件不允许分派任务");
    const { user } = open("tasks", service);
    await list("审查任务");
    await user.click(screen.getByRole("button", { name: "分派任务" }));
    await user.selectOptions(screen.getByLabelText("关联案件"), "case-empty");
    await user.selectOptions(screen.getByLabelText("责任人"), "reviewer-1");
    await user.type(screen.getByLabelText("任务名称"), "无截止日期的核对");
    await user.click(screen.getByRole("button", { name: "保存任务" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("该案件不允许分派任务");
    expect(screen.getByLabelText("任务名称")).toHaveValue("无截止日期的核对");
    expect(screen.getByLabelText("关联案件")).toHaveValue("case-empty");
    expect(screen.getByRole("button", { name: "保存任务" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "保存任务" }));
    expect(await details().findByRole("heading", { name: "无截止日期的核对" })).toBeInTheDocument();
    expectField("截止时间", "未报告");
    expect(details().getByRole("link", { name: "打开关联记录" })).toHaveAttribute("href", "/cases/case-empty");
    expect(service.requests.filter((item) => item.key === "POST /api/cases/case-empty/tasks").map((item) => item.body)).toEqual([
      { title: "无截止日期的核对", kind: "general", assignee_id: "reviewer-1", due_at: null, status: "open", priority: "medium", finding_id: null },
      { title: "无截止日期的核对", kind: "general", assignee_id: "reviewer-1", due_at: null, status: "open", priority: "medium", finding_id: null },
    ]);
  });

  it("submits the displayed task version and keeps the open task visible on a concurrent update", async () => {
    const service = new WorkbenchService();
    service.taskVersions.set("task/assigned", 8);
    const { user } = open("tasks", service);
    const rows = await list("审查任务");
    await rows.findByRole("button", { name: /核实付款依据/ });
    await user.click(details().getByRole("button", { name: "标记任务完成" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("该任务版本已更新，请刷新后重试");
    expectField("状态", "open");
    expectField("版本", "7");
    expect(rows.getByRole("button", { name: /核实付款依据/ })).toBeInTheDocument();
    expect(details().getByRole("button", { name: "标记任务完成" })).toBeEnabled();
    expect(service.requests.find((item) => item.key === "PUT /api/cases/case%2Falpha/tasks/task%2Fassigned")?.body).toEqual({
      title: "核实付款依据", kind: "decision", assignee_id: "reviewer-1", due_at: "2026-10-28T15:59:59Z",
      status: "completed", priority: "high", finding_id: "finding/payment", expected_version: 7,
    });
    await category(user, "审查任务", "已完成");
    expect(rows.queryByRole("button", { name: /核实付款依据/ })).not.toBeInTheDocument();
  });

  it("preserves raw identifiers and reports absent case material and ownership", async () => {
    const service = new WorkbenchService();
    service.snapshot.tasks = [task({ case_id: "case-missing", assignee_id: "former-assignee", created_by: "reviewer-1" })];
    const { user } = open("tasks", service);
    await list("审查任务");
    await category(user, "审查任务", "我发起的");
    expectField("责任人", "former-assignee");
    expect(details().getByRole("link", { name: "打开关联记录" })).toHaveAttribute("href", "/cases/case-missing");
    await detailTab(user, "处理记录");
    expect(details().getByText("快照没有返回相关案件的业务审计事件。")).toBeInTheDocument();
    await detailTab(user, "关联材料");
    expect(details().getByText("案件快照没有返回材料。")).toBeInTheDocument();
    expect(details().getAllByRole("link")).toHaveLength(1);
    await detailTab(user, "协作成员");
    expectField("当前责任人", "former-assignee");
    expectField("案件负责人", "未报告");
  });
});

describe("workbench knowledge", () => {
  it("searches saved body text only after a retrieval query and preserves source and version details", async () => {
    const { user } = open("knowledge");
    const rows = await list("知识库");
    expectField("维护人", "审查员甲");
    expectField("生效时间", "未报告");
    expect(details().getByText(/SPECIAL-CLAUSE 必须留存/)).toBeInTheDocument();
    await category(user, "知识库", "知识条目");
    await detailTab(user, "条目与引用");
    expectField("条目 ID", "knowledge-policy");
    expectField("版本", "2.0.0");
    await category(user, "知识库", "检索测试");
    expect(rows.getByText("输入检索词，按当前快照搜索已保存的制度标题与正文。")).toBeInTheDocument();
    expect(details().queryByRole("heading")).not.toBeInTheDocument();
    await user.type(screen.getByRole("searchbox"), "  special-clause  ");
    expect(rows.getByRole("button", { name: /采购安全制度/ })).toBeInTheDocument();
    expect(rows.queryByRole("button", { name: /外部参考资料/ })).not.toBeInTheDocument();
    await detailTab(user, "适用范围");
    expectField("来源类型", "synthetic_policy");
    expect(details().getByText("快照没有返回额外适用范围字段。")).toBeInTheDocument();
    await user.clear(screen.getByRole("searchbox"));
    await category(user, "知识库", "文档库");
    await user.click(rows.getByRole("button", { name: /外部参考资料/ }));
    await detailTab(user, "文档内容");
    expectField("维护人", "former-member");
    expectField("生效时间", at);
    await detailTab(user, "版本记录");
    expectField("当前版本", "2026-10");
    expectField("创建者", "former-member");
    expect(details().queryByRole("link")).not.toBeInTheDocument();
  });

  it("persists a trimmed reference entry with its stable ID and local effective time", async () => {
    const { user, service } = open("knowledge");
    const rows = await list("知识库");
    await user.click(screen.getByRole("button", { name: "新增知识" }));
    expect(screen.getByRole("button", { name: "保存知识条目" })).toBeDisabled();
    await user.type(screen.getByLabelText("稳定 ID（可选）"), "  knowledge/reference-3  ");
    await user.type(screen.getByLabelText("标题", { exact: true }), "  新签约依据  ");
    await user.clear(screen.getByLabelText("版本", { exact: true }));
    await user.type(screen.getByLabelText("版本", { exact: true }), "  3.1.0  ");
    await user.selectOptions(screen.getByLabelText("来源类型"), "reference");
    fireEvent.change(screen.getByLabelText("生效时间"), { target: { value: "2026-10-22T09:30" } });
    await user.type(screen.getByLabelText("全文"), "  合同签约前应取得审查意见。  ");
    await user.click(screen.getByRole("button", { name: "保存知识条目" }));
    await waitFor(() => expect(screen.queryByLabelText("全文")).not.toBeInTheDocument());
    await user.click(await rows.findByRole("button", { name: /新签约依据/ }));
    expectField("知识条目", "knowledge/reference-3");
    expectField("版本", "3.1.0");
    expectField("来源", "reference");
    expect(details().getByText("合同签约前应取得审查意见。")).toBeInTheDocument();
    expectField("生效时间", new Date(2026, 9, 22, 9, 30).toISOString());
    expect(service.requests.find((item) => item.key === "POST /api/knowledge")?.body).toEqual({
      id: "knowledge/reference-3", title: "新签约依据", version: "3.1.0", body: "合同签约前应取得审查意见。",
      source_type: "reference", effective_at: new Date(2026, 9, 22, 9, 30).toISOString(),
    });
  });

  it("rejects whitespace-only content, retains duplicate-ID errors and saves with generated identity", async () => {
    const { user, service } = open("knowledge");
    const rows = await list("知识库");
    await user.click(screen.getByRole("button", { name: "新增知识" }));
    await user.type(screen.getByLabelText("标题", { exact: true }), "   ");
    await user.type(screen.getByLabelText("全文"), "   ");
    expect(screen.getByRole("button", { name: "保存知识条目" })).toBeDisabled();
    expect(service.requests.filter((item) => item.key === "POST /api/knowledge")).toHaveLength(0);
    await user.type(screen.getByLabelText("标题", { exact: true }), "新增合成制度");
    await user.type(screen.getByLabelText("全文"), "保存真实制度正文");
    await user.type(screen.getByLabelText("稳定 ID（可选）"), "knowledge-policy");
    await user.clear(screen.getByLabelText("版本", { exact: true }));
    await user.type(screen.getByLabelText("版本", { exact: true }), "   ");
    await user.click(screen.getByRole("button", { name: "保存知识条目" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("标题、版本与全文不能为空");
    expect(screen.getByLabelText("全文")).toHaveValue("   保存真实制度正文");
    await user.clear(screen.getByLabelText("版本", { exact: true }));
    await user.type(screen.getByLabelText("版本", { exact: true }), "1.0.0");
    await user.click(screen.getByRole("button", { name: "保存知识条目" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("知识条目 ID 已存在");
    expect(screen.getByLabelText("稳定 ID（可选）")).toHaveValue("knowledge-policy");
    await user.clear(screen.getByLabelText("稳定 ID（可选）"));
    await user.click(screen.getByRole("button", { name: "保存知识条目" }));
    await waitFor(() => expect(screen.queryByLabelText("全文")).not.toBeInTheDocument());
    await user.click(await rows.findByRole("button", { name: /新增合成制度/ }));
    expectField("知识条目", "knowledge-server-generated");
    expectField("来源", "synthetic_policy");
    expectField("生效时间", "未报告");
    expect(service.requests.filter((item) => item.key === "POST /api/knowledge").at(-1)?.body).toEqual({
      title: "新增合成制度", version: "1.0.0", body: "保存真实制度正文", source_type: "synthetic_policy", effective_at: null,
    });
    await user.click(screen.getByRole("button", { name: "新增知识" }));
    await user.type(screen.getByLabelText("标题", { exact: true }), "未保存的草稿");
    await user.click(screen.getByRole("button", { name: "取消" }));
    expect(screen.queryByLabelText("全文")).not.toBeInTheDocument();
    expect(rows.queryByRole("button", { name: /未保存的草稿/ })).not.toBeInTheDocument();
    expect(service.requests.filter((item) => item.key === "POST /api/knowledge")).toHaveLength(3);
  });
});

describe("workbench remediation and frozen artifacts", () => {
  it("routes remediation status groups to their real materials, original run and verification run", async () => {
    const { user } = open("remediation");
    const rows = await list("整改中心");
    expect(rows.getByRole("button", { name: /补充付款材料/ })).toBeInTheDocument();
    expect(rows.getByRole("button", { name: /重新整改付款条款/ })).toBeInTheDocument();
    expect(rows.queryByRole("button", { name: /等待复核签章|已关闭签章整改/ })).not.toBeInTheDocument();
    expectField("责任人", "管理员乙");
    expectField("最近理由", "缺少签章");
    expectField("版本", "4");
    expect(details().getByRole("link", { name: "进入案件整改与复核" })).toHaveAttribute("href", "/cases/case%2Falpha");
    await detailTab(user, "材料与版本");
    expectField("采购原件.pdf", "draft_contract · ready · original-sha256");
    expect(details().getByRole("link", { name: "读取 采购原件.pdf" })).toHaveAttribute("href", "/api/cases/case%2Falpha/documents/document%2Fsource/original");
    expect(details().queryByRole("link", { name: "读取 来源不一致.txt" })).not.toBeInTheDocument();
    expect(details().queryByText(/not-in-authorized-snapshot/)).not.toBeInTheDocument();
    await detailTab(user, "复核记录");
    expectField("复核运行", "未报告");
    expect(details().getByRole("link", { name: "打开原始审查" })).toHaveAttribute("href", "/cases/case%2Falpha/reviews/run%2Foriginal");
    expect(details().queryByRole("link", { name: "打开复核审查" })).not.toBeInTheDocument();
    await category(user, "整改中心", "待复核");
    expect(rows.getAllByRole("button")).toHaveLength(1);
    expect(rows.getByRole("button", { name: /等待复核签章/ })).toBeInTheDocument();
    expectField("复核尝试", "attempt/verification");
    expectField("原始尝试", "attempt/original");
    expect(details().getByRole("link", { name: "打开复核审查" })).toHaveAttribute("href", "/cases/case%2Falpha/reviews/run%2Fverification");
    await detailTab(user, "操作历史");
    expect(details().getByText(/提交签章材料/)).toBeInTheDocument();
    await category(user, "整改中心", "已关闭");
    expect(rows.getAllByRole("button")).toHaveLength(1);
    expect(rows.getByRole("button", { name: /已关闭签章整改/ })).toBeInTheDocument();
  });

  it("keeps missing remediation materials, member identities and audit history explicit", async () => {
    const service = new WorkbenchService();
    service.snapshot.remediations = [remediation({ case_id: "case-empty", document_ids: [], assignee_id: "former-assignee", reason: "", due_at: "" })];
    const { user } = open("remediation", service);
    await list("整改中心");
    expectField("责任人", "former-assignee");
    expectField("截止时间", "未报告");
    expectField("最近理由", "未报告");
    await detailTab(user, "材料与版本");
    expect(details().getByText("整改记录未关联快照中的材料。")).toBeInTheDocument();
    await detailTab(user, "操作历史");
    expect(details().getByText("快照没有返回相关案件的审计历史。")).toBeInTheDocument();
  });

  it("compares only cases with multiple reports and exposes each frozen version and digest", async () => {
    const { user } = open("reports");
    const rows = await list("报告中心");
    expect(rows.getAllByRole("button")).toHaveLength(3);
    expectField("报告 ID", "report/frozen-2");
    const frozen = details().getByRole("link", { name: "打开冻结报告" });
    expect(frozen).toHaveAttribute("href", "/api/cases/case%2Falpha/reports/report%2Ffrozen-2?format=html");
    expect(frozen).toHaveAttribute("target", "_blank");
    expect(frozen).toHaveAttribute("rel", "noreferrer");
    expect(details().getByRole("link", { name: "读取冻结 JSON" })).toHaveAttribute("href", "/api/cases/case%2Falpha/reports/report%2Ffrozen-2?format=json");
    await detailTab(user, "发现与决定");
    expectField("运行 ID", "run/report-2");
    expectField("尝试 ID", "attempt/report-2");
    await detailTab(user, "版本与来源");
    expectField("报告版本", "2");
    expectField("输入摘要", "input-v2-sha256");
    expectField("输出摘要", "output-v2-sha256");
    expectField("决定快照摘要", "decision-v2-sha256");
    expectField("报告摘要", "report-v2-sha256");
    await category(user, "报告中心", "版本对比");
    expect(rows.getAllByRole("button")).toHaveLength(2);
    expect(rows.queryByRole("button", { name: /独立单版案件/ })).not.toBeInTheDocument();
    await user.type(screen.getByRole("searchbox"), "  INPUT-V1-SHA256  ");
    expect(rows.getAllByRole("button")).toHaveLength(1);
    expectField("报告版本", "1");
    expectField("报告摘要", "report-v1-sha256");
    await user.clear(screen.getByRole("searchbox"));
    await category(user, "报告中心", "导出记录");
    await user.click(rows.getByRole("button", { name: /独立单版案件/ }));
    await detailTab(user, "导出记录");
    expectField("运行 ID", "run-single");
    expect(details().getByRole("link", { name: "读取冻结 HTML" })).toHaveAttribute("href", "/api/cases/case-empty/reports/report-single?format=html");
  });

  it("links evidence originals, parser identity and citations to actual document and run IDs", async () => {
    const { user } = open("evidence");
    await list("证据中心");
    expectField("文件名", "采购原件.pdf");
    expectField("字节数", "2048");
    expect(details().getByRole("link", { name: "读取材料原件" })).toHaveAttribute("href", "/api/cases/case%2Falpha/documents/document%2Fsource/original");
    expect(details().getByRole("link", { name: "读取原件" })).toHaveAttribute("target", "_blank");
    await category(user, "证据中心", "引用片段");
    expect(screen.getByRole("status")).toHaveTextContent("指定 run/attempt");
    await detailTab(user, "引用片段");
    expectField("解析产物", "parsed/source");
    expect(details().getByRole("link", { name: "查看审查引用" })).toHaveAttribute("href", "/cases/case%2Falpha/reviews/run%2Fcurrent");
    await detailTab(user, "来源与身份");
    expectField("材料 ID", "document/source");
    expectField("原件摘要", "original-sha256");
    expectField("解析器", "pdf-parser");
    expectField("解析版本", "3.2.1");
    await detailTab(user, "使用记录");
    expectField("最新运行", "run/current");
    expectField("最新状态", "completed");
    expect(details().getByRole("link", { name: "打开最近审查" })).toHaveAttribute("href", "/cases/case%2Falpha/reviews/run%2Fcurrent");
  });

  it("distinguishes matching, mismatched and unavailable persisted evidence integrity", async () => {
    const { user } = open("evidence");
    const rows = await list("证据中心");
    await category(user, "证据中心", "完整性校验");
    expect(screen.queryByRole("tablist", { name: "记录详情分类" })).not.toBeInTheDocument();
    expectField("校验结果", "原件摘要与解析来源一致");
    expectField("解析产物 ID", "parsed/source");
    expectField("归一化内容摘要", "normalized-sha256");
    await user.click(rows.getByRole("button", { name: /来源不一致.txt/ }));
    expectField("校验结果", "原件摘要与解析来源不一致");
    expectField("原件摘要", "replaced-original-sha256");
    expectField("解析来源原件摘要", "original-sha256");
    await user.click(rows.getByRole("button", { name: /尚未解析.txt/ }));
    expectField("校验结果", "未生成解析产物");
    expectField("解析产物 ID", "未报告");
    expectField("归一化内容摘要", "未报告");
    await category(user, "证据中心", "证据材料");
    await user.click(rows.getByRole("button", { name: /尚未解析.txt/ }));
    expectField("字节数", "0");
    await detailTab(user, "引用片段");
    expectField("解析产物", "未报告");
    expect(details().queryByRole("link", { name: "查看审查引用" })).not.toBeInTheDocument();
    expect(details().getByRole("link", { name: "读取原件" })).toHaveAttribute("href", "/api/cases/case-empty/documents/document-unparsed/original");
    await detailTab(user, "来源与身份");
    expectField("解析器", "未报告");
    expectField("解析版本", "未报告");
    await detailTab(user, "使用记录");
    expect(details().getByText("案件尚无快照所示的审查运行。")).toBeInTheDocument();
    expect(details().queryByRole("link", { name: "打开最近审查" })).not.toBeInTheDocument();
  });
});

describe("workbench execution and platform projections", () => {
  it("renders reported run usage and preserves missing usage instead of inventing zeroes", async () => {
    const { user } = open("executions");
    const rows = await list("执行轨迹");
    expectField("运行 ID", "run/current");
    expectField("尝试 ID", "attempt/frozen-3");
    expectField("开始时间", at);
    expect(details().getByRole("link", { name: "打开完整执行工作区" })).toHaveAttribute("href", "/cases/case%2Falpha/reviews/run%2Fcurrent");
    await category(user, "执行轨迹", "模型调用");
    expect(screen.getByRole("status")).toHaveTextContent("运行级统计");
    await detailTab(user, "调用时间轴");
    expectField("实际调用数", "7");
    expectField("用量未知调用数", "2");
    expectField("已报告来源", "provider、gateway");
    expect(details().getByRole("link", { name: "查看执行事件" })).toHaveAttribute("href", "/cases/case%2Falpha/reviews/run%2Fcurrent");
    await detailTab(user, "输入与输出");
    expectField("输入 tokens", "120");
    expectField("输出 tokens", "30");
    expectField("总 tokens", "150");
    expectField("成本", "未报告");
    await category(user, "执行轨迹", "规则执行");
    expect(screen.getByRole("status")).toHaveTextContent("规则执行结果记录在具体运行中");
    await user.type(screen.getByRole("searchbox"), " FAILED ");
    expect(rows.getAllByRole("button")).toHaveLength(1);
    expectField("输入 tokens", "未报告");
    expectField("输出 tokens", "未报告");
    expectField("总 tokens", "未报告");
    await detailTab(user, "调用时间轴");
    expectField("实际调用数", "未报告");
    expectField("用量未知调用数", "未报告");
    expectField("已报告来源", "未报告");
    await detailTab(user, "运行概览");
    expectField("尝试 ID", "未报告");
    expectField("开始时间", "未报告");
    expectField("结束时间", "未报告");
    await detailTab(user, "执行日志");
    expectField("状态", "failed");
    expect(details().getByRole("link", { name: "查看运行记录" })).toHaveAttribute("href", "/cases/case-empty/reviews/run-no-usage");
  });

  it("reports configured model usage only from returned runs and limits changes to platform audit", async () => {
    const { user } = open("models");
    await list("模型服务");
    expect(details().getByRole("heading", { name: "已登记审查模型" })).toBeInTheDocument();
    expectField("模型名", "model/configured");
    expectField("OCR 模型", "ocr/model");
    expectField("配置状态", "已配置");
    expect(details().queryByRole("link")).not.toBeInTheDocument();
    await category(user, "模型服务", "调用记录");
    await detailTab(user, "调用统计");
    expectField("快照运行记录数", "2");
    expectField("有用量汇总的运行", "1");
    expectField("已报告调用总数", "7");
    expect(details().getByText(/费用保持未知/)).toBeInTheDocument();
    await category(user, "模型服务", "预算配置");
    await detailTab(user, "能力边界");
    expectField("聊天模型", "model/configured");
    await detailTab(user, "变更记录");
    expect(details().getByText("发布运营商规则包")).toBeInTheDocument();
    expect(details().getByText("维护安全制度")).toBeInTheDocument();
    expect(details().queryByText(/创建付款核对任务|提交签章材料/)).not.toBeInTheDocument();
    expect(details().getByRole("link", { name: "打开业务审计" })).toHaveAttribute("href", "/workbench/audit");
  });

  it("uses the model name when unlabeled and leaves aggregate usage unreported when every run lacks it", async () => {
    const service = new WorkbenchService();
    service.snapshot.model = { name: "unconfigured-model", label: "", ocr_name: "", status: "unconfigured" };
    service.snapshot.runs = [run({ usage: null })];
    service.snapshot.audit = [];
    const { user } = open("models", service);
    await list("模型服务");
    expect(details().getByRole("heading", { name: "unconfigured-model" })).toBeInTheDocument();
    expectField("配置状态", "未配置");
    expectField("OCR 模型", "未报告");
    expectField("显示标签", "未报告");
    await detailTab(user, "调用统计");
    expectField("有用量汇总的运行", "0");
    expectField("已报告调用总数", "未报告");
    await detailTab(user, "变更记录");
    expect(details().queryByRole("definition")).not.toBeInTheDocument();
  });

  it("keeps registered tool capabilities, call counts and unreported status distinct from private payloads", async () => {
    const { user } = open("tools");
    const rows = await list("工具网关");
    expectField("工具 ID", "tool/read-only");
    expectField("最近状态", "ready");
    await detailTab(user, "输入与输出");
    expectField("能力", "search、read");
    expect(details().getByText("快照未包含单次调用的输入输出正文。")).toBeInTheDocument();
    await category(user, "工具网关", "权限策略");
    await detailTab(user, "权限与预算");
    expectField("登记能力", "search、read");
    expect(details().getByText("权限与预算不在公开工具快照中返回。")).toBeInTheDocument();
    await category(user, "工具网关", "调用记录");
    await detailTab(user, "调用记录");
    expectField("已统计调用", "19");
    await user.click(rows.getByRole("button", { name: /尚未使用工具/ }));
    expectField("已统计调用", "0");
    expectField("最近状态", "未报告");
    await detailTab(user, "输入与输出");
    expectField("能力", "未报告");
    await detailTab(user, "权限与预算");
    expectField("登记能力", "未报告");
    await detailTab(user, "工具信息");
    expectField("工具 ID", "tool/no-data");
    expect(details().queryByRole("link")).not.toBeInTheDocument();
  });

  it("filters current unavailable and unknown health observations and refreshes resolved observations", async () => {
    const { user, service } = open("health");
    const rows = await list("系统健康");
    expectField("服务 ID", "database");
    expectField("观察时间", at);
    await detailTab(user, "运行指标");
    expectField("报告", "读写检查完成");
    await category(user, "系统健康", "依赖拓扑");
    expect(screen.getByRole("status")).toHaveTextContent("没有依赖关系边");
    await detailTab(user, "依赖服务");
    expectField("服务 ID", "database");
    expect(details().getByText(/不推断拓扑/)).toBeInTheDocument();
    await category(user, "系统健康", "告警记录");
    expect(rows.getAllByRole("button")).toHaveLength(2);
    expect(rows.queryByRole("button", { name: /业务数据库/ })).not.toBeInTheDocument();
    expect(rows.getByRole("button", { name: /任务通知/ })).toBeInTheDocument();
    expect(rows.getByRole("button", { name: /材料对象存储/ })).toBeInTheDocument();
    await user.click(rows.getByRole("button", { name: /材料对象存储/ }));
    await detailTab(user, "事件日志");
    expectField("当前状态", "unknown");
    expectField("观察说明", "没有返回探测结果");
    expect(details().getByText(/不是历史告警时间序列/)).toBeInTheDocument();
    service.snapshot.health = service.snapshot.health.map((item): WorkbenchSnapshot["health"][number] => ({ ...item, status: "ready", detail: "服务已恢复" }));
    await user.click(screen.getByRole("button", { name: "刷新快照" }));
    await waitFor(() => expect(rows.queryByRole("button")).not.toBeInTheDocument());
    expect(rows.getByText("没有符合此分类或搜索词的记录。")).toBeInTheDocument();
    expect(details().queryByRole("heading")).not.toBeInTheDocument();
    expect(service.requests.filter((item) => item.key === "GET /api/workbench?period=week")).toHaveLength(2);
  });
});

describe("workbench public directory, templates and audit", () => {
  it("shows public member identity without inferring case ACL and sorts only that actor audit events", async () => {
    const { user } = open("organization");
    const rows = await list("组织与权限");
    expectField("身份 ID", "reviewer-1");
    expectField("租户 ID", "tenant-public");
    expectField("组织 ID", "org-alpha");
    await category(user, "组织与权限", "角色权限");
    expect(screen.getByRole("status")).toHaveTextContent("资源授权仍由服务端逐次判断");
    await detailTab(user, "角色与权限");
    expectField("角色", "reviewer");
    expectField("组织", "org-alpha");
    await detailTab(user, "案件授权");
    expect(details().getByText(/不能从当前用户的可见案件推断此身份的授权范围/)).toBeInTheDocument();
    expect(details().queryByText("专线采购案件")).not.toBeInTheDocument();
    await detailTab(user, "活动记录");
    expect(details().getAllByText(/维护安全制度|创建付款核对任务/).map((item) => item.textContent)).toEqual([
      "平台级事件 · 维护安全制度", "专线采购案件 · 创建付款核对任务",
    ]);
    expect(details().queryByText(/提交签章材料|发布运营商规则包/)).not.toBeInTheDocument();
    expect(details().getByRole("link", { name: "查看审计事件" })).toHaveAttribute("href", "/workbench/audit");
    await user.click(rows.getByRole("button", { name: /未活动成员/ }));
    expect(details().getByText("快照没有返回关联到此身份的业务审计事件。")).toBeInTheDocument();
    expect(details().queryByRole("link")).not.toBeInTheDocument();
    await category(user, "组织与权限", "组织架构");
    expect(screen.getByRole("status")).toHaveTextContent("没有上下级组织关系");
    await user.type(screen.getByRole("searchbox"), "org-beta");
    expect(rows.getAllByRole("button")).toHaveLength(2);
    expect(rows.queryByRole("button", { name: /审查员甲/ })).not.toBeInTheDocument();
  });

  it("renders returned template field definitions and current version without fabricating generated documents", async () => {
    const { user } = open("templates");
    await list("模板管理");
    expectField("ID", "template/line");
    expectField("版本", "3.0.0");
    expectField("摘要", "template-sha256");
    expect(details().queryByRole("link")).not.toBeInTheDocument();
    await category(user, "模板管理", "字段配置");
    await detailTab(user, "字段定义");
    expectField("供应商", "supplier · text · 必填");
    expectField("备注", "note · textarea · 选填");
    await category(user, "模板管理", "生成记录");
    await detailTab(user, "使用记录");
    expectField("当前身份可见案件", "2");
    expect(details().getByText(/不包含模板级生成日志/)).toBeInTheDocument();
    expect(details().getByRole("link", { name: "打开案件中心查看生成记录" })).toHaveAttribute("href", "/cases");
    await detailTab(user, "版本记录");
    expectField("当前版本", "3.0.0");
    expectField("内容摘要", "template-sha256");
  });

  it("keeps an empty template definition empty", async () => {
    const service = new WorkbenchService();
    service.snapshot.templates = [{ id: "template-empty", name: "空字段模板", version: "1.0.0", digest: "empty-sha256", fields: [] }];
    const { user } = open("templates", service);
    await list("模板管理");
    await detailTab(user, "字段定义");
    expect(details().getByRole("heading", { name: "空字段模板" })).toBeInTheDocument();
    expect(details().queryByRole("definition")).not.toBeInTheDocument();
    expect(details().queryByRole("link")).not.toBeInTheDocument();
  });

  it("separates case, platform and current-actor audit projections and never treats them as access logs", async () => {
    const { user } = open("audit");
    const rows = await list("审计日志");
    expectField("事件 ID", "audit-case-1");
    expectField("资源范围", "专线采购案件");
    expect(details().getByRole("link", { name: "打开关联记录" })).toHaveAttribute("href", "/cases/case%2Falpha");
    await detailTab(user, "变更内容");
    expectField("动作", "task.created");
    expectField("摘要", "创建付款核对任务");
    await detailTab(user, "来源上下文");
    expectField("操作人 ID", "reviewer-1");
    await detailTab(user, "关联事件");
    expect(details().getByText(/创建付款核对任务/)).toBeInTheDocument();
    expect(details().getByText(/提交签章材料/)).toBeInTheDocument();
    expect(details().queryByText(/维护安全制度|发布运营商规则包/)).not.toBeInTheDocument();
    await user.click(rows.getByRole("button", { name: /rulepack.published/ }));
    expect(details().getByText("平台级事件没有案件关联。")).toBeInTheDocument();
    expect(details().queryByRole("link")).not.toBeInTheDocument();
    await detailTab(user, "事件详情");
    expectField("资源范围", "平台级事件");
    await user.click(rows.getByRole("button", { name: /case.updated/ }));
    expectField("资源范围", "case-not-in-snapshot");
    expect(details().getByRole("link", { name: "打开关联记录" })).toHaveAttribute("href", "/cases/case-not-in-snapshot");
    await detailTab(user, "关联事件");
    expect(details().getByText("快照没有返回该案件的其他审计事件。")).toBeInTheDocument();
    await category(user, "审计日志", "操作日志");
    expect(screen.getByRole("status")).toHaveTextContent("当前身份 actor_id");
    expect(rows.getAllByRole("button")).toHaveLength(2);
    expect(rows.getByRole("button", { name: /task.created/ })).toBeInTheDocument();
    expect(rows.getByRole("button", { name: /knowledge.created/ })).toBeInTheDocument();
    expect(rows.queryByRole("button", { name: /rulepack.published|remediation.submitted|case.updated/ })).not.toBeInTheDocument();
    await category(user, "审计日志", "访问记录");
    expect(screen.getByRole("status")).toHaveTextContent("HTTP 访问日志不在工作台业务审计快照中");
    expect(rows.queryByRole("button")).not.toBeInTheDocument();
    expect(details().queryByRole("heading")).not.toBeInTheDocument();
  });
});

describe("workbench snapshot boundaries", () => {
  it("retries an unavailable snapshot and renders only the authorized response", async () => {
    const service = new WorkbenchService();
    service.fail("GET /api/workbench?period=week", 503, "持久业务快照暂时不可用");
    service.snapshot.documents = [document({ id: "authorized-document", filename: "授权材料.pdf" })];
    const { user } = open("evidence", service);
    expect(await screen.findByRole("alert")).toHaveTextContent("持久业务快照暂时不可用");
    expect(screen.getByRole("heading", { name: "证据中心没有加载" })).toBeInTheDocument();
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    const rows = await list("证据中心");
    expect(rows.getAllByRole("button")).toHaveLength(1);
    expect(details().getByRole("heading", { name: "授权材料.pdf" })).toBeInTheDocument();
    expect(details().getByRole("link", { name: "读取材料原件" })).toHaveAttribute("href", "/api/cases/case%2Falpha/documents/authorized-document/original");
    expect(service.requests.filter((item) => item.key === "GET /api/workbench?period=week")).toHaveLength(2);
  });

  it("shows a server denial without exposing any cached fixture records", async () => {
    const service = new WorkbenchService();
    service.fail("GET /api/workbench?period=week", 403, "没有读取工作台快照的权限");
    open("reports", service);
    expect(await screen.findByRole("alert")).toHaveTextContent("没有读取工作台快照的权限");
    expect(screen.queryByRole("link", { name: "打开冻结报告" })).not.toBeInTheDocument();
    expect(screen.queryByText("专线采购案件")).not.toBeInTheDocument();
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();
  });

  it("rejects an invalid parsed-document projection and can recover with a valid snapshot", async () => {
    const service = new WorkbenchService();
    service.responseSnapshot = { ...service.snapshot, documents: [{ ...document(), parsed_document: undefined }] };
    const { user } = open("evidence", service);
    expect(await screen.findByRole("alert")).toHaveTextContent("工作台快照无效：解析产物不是对象");
    expect(screen.queryByRole("link", { name: "读取材料原件" })).not.toBeInTheDocument();
    service.responseSnapshot = undefined;
    await user.click(screen.getByRole("button", { name: "重试" }));
    await list("证据中心");
    expectField("文件名", "采购原件.pdf");
  });

  it("does not retain a selected record when a refreshed snapshot no longer contains it", async () => {
    const { user, service } = open("reports");
    await list("报告中心");
    expect(details().getByRole("link", { name: "打开冻结报告" })).toBeInTheDocument();
    service.snapshot.reports = [];
    await user.click(screen.getByRole("button", { name: "刷新快照" }));
    await waitFor(() => expect(details().queryByRole("link")).not.toBeInTheDocument());
    expect(screen.getByText("快照没有返回记录。")).toBeInTheDocument();
    expect(details().getByText("选择一条记录查看服务端返回的详情。")).toBeInTheDocument();
  });

  it("rejects prototype property names as module routes without fetching business data", () => {
    const { service } = open("constructor");
    expect(screen.getByRole("heading", { name: "没有这个工作区页面" })).toBeInTheDocument();
    expect(service.requests).toHaveLength(0);
  });

  it("routes settings to client settings and never loads a workbench projection", async () => {
    const { service } = open("settings");
    expect(await screen.findByRole("heading", { name: "设置" })).toBeInTheDocument();
    expect(screen.getByText(/服务端规则模式/)).toBeInTheDocument();
    expect(screen.getByText("案件正文按授权脱敏")).toBeInTheDocument();
    expect(service.requests.some((item) => item.key === "GET /api/settings")).toBe(true);
    expect(service.requests.some((item) => item.key.startsWith("GET /api/workbench"))).toBe(false);
    expect(service.requests.some((item) => item.key.includes("audience=admin"))).toBe(false);
  });
});
