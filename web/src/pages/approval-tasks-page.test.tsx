import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApprovalTasksPage } from "./approval-tasks-page";
import { renderWithAntd as render } from "../test/antd";
import { setSessionIdentity } from "../api/http";

const at = "2026-11-01T02:30:00Z";

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const roundOne = {
  approval_round_id: "round-1",
  round_number: 1,
  contract_revision_id: "revision-1",
  state: "returned",
  workflow_template_id: "template-approval",
  workflow_template_version: "1.0.0",
  initiated_by: "actor-1",
  initiated_by_display: "审查员甲",
  started_at: at,
  ended_at: at,
  previous_round_id: null,
  row_version: 9,
};

const roundTwo = {
  ...roundOne,
  approval_round_id: "round-2",
  round_number: 2,
  contract_revision_id: "revision-2",
  state: "in_review",
  ended_at: null,
  previous_round_id: "round-1",
  row_version: 4,
};

function inboxItem(overrides: Record<string, unknown>) {
  return {
    contract_id: "contract-1",
    contract_name: "专线采购合同",
    contract_number: "HT-2026-001",
    contract_state: "in_approval",
    contract_revision_id: "revision-2",
    approval_round_id: "round-2",
    round_number: 2,
    round_state: "in_review",
    round_started_at: at,
    round_ended_at: null,
    task_id: "task-1",
    node_order: 1,
    node_code: "department_head",
    role_code: "department_head",
    assignee_subject_id: "actor-1",
    assignee_display: "审查员甲",
    task_state: "active",
    task_row_version: 3,
    task_activated_at: at,
    task_decided_at: null,
    decision_id: null,
    decision_action: null,
    decision_actor_subject_id: null,
    decision_actor_display: null,
    decision_opinion: null,
    decision_decided_at: null,
    allowed_actions: ["approve", "return"],
    ...overrides,
  };
}

const todoItem = inboxItem({ view: "todo" });
const doneItem = inboxItem({
  view: "done",
  contract_id: "contract-2",
  contract_name: "框架服务合同",
  contract_number: "HT-2026-002",
  task_id: "task-2",
  task_state: "approved",
  decision_id: "decision-2",
  decision_action: "approve",
  decision_actor_display: "审查员甲",
  decision_opinion: "同意签署",
  decision_decided_at: at,
  allowed_actions: [],
});
const initiatedItem = inboxItem({
  view: "initiated",
  contract_name: "已退回合同",
  round_state: "returned",
  round_ended_at: at,
  task_id: null,
  node_order: null,
  node_code: null,
  role_code: null,
  assignee_subject_id: null,
  assignee_display: null,
  task_state: null,
  task_row_version: null,
  task_activated_at: null,
  task_decided_at: null,
  allowed_actions: [],
});

function taskRoundDetail(overrides: Record<string, unknown> = {}) {
  return {
    contract_id: "contract-1",
    contract_name: "专线采购合同",
    contract_number: "HT-2026-001",
    contract_state: "in_approval",
    contract_row_version: 8,
    current_revision_id: "revision-2",
    round: roundTwo,
    tasks: [
      {
        task_id: "task-1",
        node_order: 1,
        node_code: "department_head",
        role_code: "department_head",
        assignee_subject_id: "actor-1",
        assignee_display: "审查员甲",
        state: "active",
        row_version: 3,
        activated_at: at,
        decided_at: null,
      },
      {
        task_id: "task-3",
        node_order: 2,
        node_code: "legal_counsel",
        role_code: "legal_counsel",
        assignee_subject_id: "actor-2",
        assignee_display: "法务乙",
        state: "pending",
        row_version: 1,
        activated_at: null,
        decided_at: null,
      },
    ],
    decisions: [],
    active_task_id: "task-1",
    risk_disposition_state: "unknown",
    risk_disposition_reason: "risk_disposition_not_configured",
    previous_round: roundOne,
    revision_changed: false,
    revision_diff: null,
    allowed_actions: ["approve", "return"],
    ...overrides,
  };
}

function initiatedRoundDetail() {
  return taskRoundDetail({
    round: { ...roundTwo, state: "returned", ended_at: at },
    active_task_id: null,
    allowed_actions: [],
    tasks: [
      {
        task_id: "task-1",
        node_order: 1,
        node_code: "department_head",
        role_code: "department_head",
        assignee_subject_id: "actor-1",
        assignee_display: "审查员甲",
        state: "returned",
        row_version: 4,
        activated_at: at,
        decided_at: at,
      },
    ],
    decisions: [
      {
        decision_id: "decision-1",
        task_id: "task-1",
        node_order: 1,
        node_code: "department_head",
        action: "return",
        actor_subject_id: "actor-1",
        actor_display: "审查员甲",
        opinion: "请补充付款节点。",
        decided_at: at,
      },
    ],
    revision_changed: true,
    revision_diff: {
      from_revision_number: 1,
      to_revision_number: 2,
      changed: true,
      fields: [
        {
          path: "contract.amount",
          category: "amount",
          classification: "internal",
          change: "changed",
          before: 100,
          after: 120,
        },
      ],
      restricted_fields: [],
      amount_terms: [],
    },
  });
}

class ApprovalService {
  calls: {
    method: string;
    path: string;
    view: string | null;
    body: string | null;
    idempotencyKey: string | null;
  }[] = [];
  conflictsRemaining = 1;

  handle = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = init?.method ?? "GET";
    const headers = new Headers(init?.headers);
    this.calls.push({
      method,
      path: url.pathname,
      view: url.searchParams.get("view"),
      body: typeof init?.body === "string" ? init.body : null,
      idempotencyKey: headers.get("Idempotency-Key"),
    });

    if (url.pathname === "/api/session") {
      return json({
        audience: "client",
        actor: {
          id: "actor-1",
          display_name: "审查员甲",
          role: "reviewer",
          tenant_id: "tenant-1",
          org_id: "org-1",
        },
      });
    }
    if (url.pathname === "/api/approval-tasks" && method === "GET") {
      const view = url.searchParams.get("view");
      return json({
        items:
          view === "done"
            ? [doneItem]
            : view === "initiated"
              ? [initiatedItem]
              : [todoItem],
        next_cursor: null,
        has_more: false,
      });
    }
    if (url.pathname === "/api/approval-tasks/task-1" && method === "GET") {
      return json({ selected_task_id: "task-1", round_detail: taskRoundDetail() });
    }
    if (
      url.pathname === "/api/contracts/contract-1/approval-rounds" &&
      method === "GET"
    ) {
      return json({
        items: [
          {
            round: { ...roundTwo, state: "returned", ended_at: at },
            active_task: null,
            task_count: 1,
            decision_count: 1,
          },
          {
            round: roundOne,
            active_task: null,
            task_count: 1,
            decision_count: 1,
          },
        ],
        next_cursor: null,
        has_more: false,
      });
    }
    if (
      url.pathname === "/api/contracts/contract-1/approval-rounds/round-2" &&
      method === "GET"
    ) {
      return json(initiatedRoundDetail());
    }
    if (
      url.pathname === "/api/contracts/contract-1/approval-rounds/round-1" &&
      method === "GET"
    ) {
      return json({
        ...initiatedRoundDetail(),
        round: roundOne,
        previous_round: null,
        revision_changed: false,
        revision_diff: null,
      });
    }
    if (
      url.pathname === "/api/approval-tasks/task-1/decisions" &&
      method === "POST"
    ) {
      if (this.conflictsRemaining > 0) {
        this.conflictsRemaining -= 1;
        return json(
          { error: { code: "conflict", message: "任务已被他人处理" } },
          409,
        );
      }
      return json({
        decision_id: "decision-3",
        decision: "approve",
        round: {
          approval_round_id: "round-2",
          contract_revision_id: "revision-2",
          round_number: 2,
          state: "in_review",
          row_version: 4,
        },
        task: {
          approval_task_id: "task-1",
          node_order: 1,
          state: "approved",
        },
        next_task: null,
        contract_state: "in_approval",
        workflow_state: "approved",
        row_version: 5,
      });
    }
    return json({ error: { code: "not_found", message: "没有找到记录" } }, 404);
  };
}

const clients: QueryClient[] = [];

function open(search = "?view=todo", service = new ApprovalService()) {
  vi.stubGlobal("fetch", vi.fn(service.handle));
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 300_000 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  const router = createMemoryRouter(
    [{ path: "/workbench/approvals", element: <ApprovalTasksPage /> }],
    { initialEntries: [`/workbench/approvals${search}`] },
  );
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { user: userEvent.setup(), router, client, service };
}

function buttonWithText(scope: HTMLElement, label: string): HTMLElement {
  const button = within(scope)
    .getAllByRole("button")
    .find((candidate) => candidate.textContent === label);
  if (!button) throw new Error(`没有找到按钮：${label}`);
  return button;
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("approval tasks page", () => {
  it("switches between todo, done and initiated views from the server", async () => {
    const { user, service } = open();
    const todo = await screen.findByRole("region", { name: "我的待办" });
    expect(await within(todo).findByText("专线采购合同")).toBeInTheDocument();
    expect(within(todo).getByText("部门负责人")).toBeInTheDocument();
    expect(screen.getByText(/已加载/)).toHaveTextContent("已加载 1 项");
    expect(
      service.calls.some(
        (call) => call.path === "/api/approval-tasks" && call.view === "todo",
      ),
    ).toBe(true);

    await user.click(screen.getByRole("tab", { name: "我已审批" }));
    const done = await screen.findByRole("region", { name: "我已审批" });
    expect(await within(done).findByText("框架服务合同")).toBeInTheDocument();
    expect(within(done).getByText("通过")).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "我已审批" }),
    ).toHaveAttribute("aria-selected", "true");
    expect(service.calls.some((call) =>
      call.path === "/api/approval-tasks" && call.view === "done",
    )).toBe(true);

    await user.click(screen.getByRole("tab", { name: "我发起的" }));
    const initiated = await screen.findByRole("region", { name: "我发起的" });
    expect(await within(initiated).findByText("已退回合同")).toBeInTheDocument();
    expect(within(initiated).getByText("已退回")).toBeInTheDocument();
    expect(service.calls.some((call) =>
      call.path === "/api/approval-tasks" && call.view === "initiated",
    )).toBe(true);
  });

  it("keeps a failed decision unapproved and retries with the same idempotency key", async () => {
    const { user, service } = open();
    const todo = await screen.findByRole("region", { name: "我的待办" });
    await within(todo).findByText("专线采购合同");
    await user.click(within(todo).getByRole("button", { name: /查看/ }));

    const panel = await screen.findByRole("complementary", { name: "会签详情" });
    expect((await within(panel).findAllByText(/部门负责人/)).length).toBeGreaterThan(0);
    expect(within(panel).getByText(/风险处置未配置|未配置，不能视为已接受风险/)).toBeInTheDocument();

    await user.click(within(panel).getByRole("button", { name: "通过" }));
    expect(await within(panel).findByText("请填写审批意见或退回理由。")).toBeInTheDocument();
    expect(service.calls.filter((call) => call.method === "POST")).toHaveLength(0);

    await user.type(
      within(panel).getByRole("textbox", { name: "审批意见或退回理由" }),
      "同意进入法务节点",
    );
    await user.click(within(panel).getByRole("button", { name: "通过" }));

    expect(
      await within(panel).findByText(/当前任务或轮次已经变化/),
    ).toBeInTheDocument();
    expect(within(panel).queryByText(/服务端已确认/)).not.toBeInTheDocument();
    const posts = service.calls.filter((call) => call.method === "POST");
    expect(posts).toHaveLength(1);
    expect(posts[0]?.path).toBe("/api/approval-tasks/task-1/decisions");
    expect(posts[0]?.idempotencyKey).not.toBeNull();
    expect(JSON.parse(posts[0]?.body ?? "{}")).toMatchObject({
      decision: "approve",
      comment: "同意进入法务节点",
      expected_round_row_version: 4,
    });

    await waitFor(() =>
      expect(
        within(panel).getByText(/当前任务或轮次已经变化/),
      ).toBeInTheDocument(),
    );
    await user.click(buttonWithText(panel, "通过"));
    expect(
      await within(panel).findByText(/服务端已确认/),
    ).toBeInTheDocument();
    const retried = service.calls.filter((call) => call.method === "POST");
    expect(retried).toHaveLength(2);
    expect(retried[1]?.idempotencyKey).toBe(retried[0]?.idempotencyKey);
    expect(
      within(panel).getByText(/第 2 轮 会签中，当前节点 1 为已通过/),
    ).toBeInTheDocument();
  });

  it("shows historical rounds read-only and never offers a decision on them", async () => {
    const { user, service } = open("?view=initiated");
    const initiated = await screen.findByRole("region", { name: "我发起的" });
    await within(initiated).findByText("已退回合同");
    await user.click(within(initiated).getByRole("button", { name: /查看/ }));

    const panel = await screen.findByRole("complementary", { name: "会签详情" });
    expect(await within(panel).findByText("请补充付款节点。")).toBeInTheDocument();
    expect(within(panel).getByText(/未配置，不能视为已接受风险/)).toBeInTheDocument();
    expect(within(panel).queryByRole("button", { name: "通过" })).not.toBeInTheDocument();
    expect(
      within(panel).queryByRole("button", { name: "退回修改" }),
    ).not.toBeInTheDocument();

    const history = within(await screen.findByRole("region", { name: "轮次历史" }));
    expect(await history.findByRole("button", { name: /第 2 轮/ })).toBeInTheDocument();
    expect(history.getByRole("button", { name: /第 1 轮/ })).toBeInTheDocument();
    expect(
      history.getAllByText(/1\/1 个节点已决定/).length,
    ).toBeGreaterThan(0);

    await user.click(history.getByRole("button", { name: /第 1 轮/ }));
    await waitFor(() =>
      expect(
        service.calls.some(
          (call) =>
            call.path === "/api/contracts/contract-1/approval-rounds/round-1",
        ),
      ).toBe(true),
    );
    const updatedPanel = await screen.findByRole("complementary", {
      name: "会签详情",
    });
    await waitFor(() =>
      expect(
        within(updatedPanel).queryByRole("button", { name: "通过" }),
      ).not.toBeInTheDocument(),
    );
    expect(within(updatedPanel).getByText("首轮会签，没有上一轮可比较。")).toBeInTheDocument();
  });
});
