// File metadata (manifest mirror):
// Purpose: Verifies inbox assignments and visible case work stay distinct, approval and intake actions work, and recent visits follow the authorized identity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r13
// Copyright: See LICENSE
// End file metadata.

import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithAntd as render } from "../test/antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import type { Actor, WorkbenchCase, WorkbenchSnapshot, WorkbenchTask } from "../api/types";
import type { ApprovalInboxItem } from "../api/approvals";
import type { EvidenceRequest } from "../api/evidence-workflow";
import { recordRecentCaseVisit } from "../features/home/recent-cases";
import { inboxRows } from "../features/home/inbox";
import { HomePage } from "./home-page";

const at = "2026-10-21T06:00:00Z";
const clients: QueryClient[] = [];

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function task(
  overrides: Partial<WorkbenchTask> & { can_complete?: boolean } = {},
): WorkbenchTask & { can_complete?: boolean } {
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

const actor: Actor = {
  ...session.actor,
  audience: "client",
  principal_id: session.principal_id,
  active_context: session.active_context,
  csrf_token: session.csrf_token,
  context_switch_enabled: false,
};

function evidence(overrides: Partial<EvidenceRequest> = {}): EvidenceRequest {
  return {
    id: "request-1",
    case_id: "case-1",
    task_id: "task-no-date",
    task_version: 1,
    assignee_id: "me",
    due_at: at,
    source_run_id: "run-1",
    source_attempt_id: "attempt-1",
    source_evaluation: null,
    source_gap: {
      gap_id: "gap-1",
      domain: "warranty",
      capability_id: "warranty.term",
      status: "open",
      reason_code: "required_source_or_field_missing",
    },
    question: "补充采购文件",
    material_role: "procurement",
    verification_condition: "核对原文",
    status: "open",
    version: 1,
    created_by: "me",
    created_at: at,
    updated_at: at,
    verification: null,
    ...overrides,
  };
}

const approval: ApprovalInboxItem = {
  view: "todo",
  contract_id: "contract-1",
  contract_name: "待会签合同",
  contract_number: null,
  contract_state: "in_review",
  contract_revision_id: "revision-1",
  approval_round_id: "round-1",
  round_number: 1,
  round_state: "in_review",
  round_started_at: at,
  round_ended_at: null,
  task_id: "approval-1",
  node_order: 1,
  node_code: null,
  role_code: null,
  assignee_subject_id: null,
  assignee_display: null,
  task_state: "active",
  task_row_version: 1,
  task_activated_at: at,
  task_decided_at: null,
  decision_id: null,
  decision_action: null,
  decision_actor_subject_id: null,
  decision_actor_display: null,
  decision_opinion: null,
  decision_decided_at: null,
  allowed_actions: ["approve"],
};

function open(
  options: {
    data?: WorkbenchSnapshot;
    deniedCase?: string;
    moreApprovals?: boolean;
    emptyApprovals?: boolean;
    deniedWorkbench?: boolean;
  } = {},
) {
  const workbench = options.data ?? snapshot();
  const paths: string[] = [];
  setSessionIdentity(actor);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://home.test");
      paths.push(url.pathname);
      if (url.pathname === "/api/session") return json(session);
      if (url.pathname === "/api/workbench")
        return options.deniedWorkbench ? json({}, 403) : json(workbench);
      if (url.pathname === "/api/intakes/pending") return json({ items: [], next_cursor: null });
      if (url.pathname === "/api/approval-tasks")
        return json({
          items: options.emptyApprovals ? [] : [approval, approval],
          next_cursor: options.moreApprovals ? "next" : null,
          has_more: options.moreApprovals ?? false,
        });
      const caseId = decodeURIComponent(url.pathname.split("/")[3] ?? "");
      if (url.pathname.endsWith("/evidence-requests")) {
        if (caseId === options.deniedCase) return json({}, 403);
        return json({
          items:
            caseId === "case-1"
              ? [
                  evidence(),
                  evidence(),
                  evidence({ id: "other-request", task_id: "other-task", assignee_id: "other" }),
                  evidence({
                    id: "received-request",
                    task_id: "received-task",
                    status: "received",
                  }),
                ]
              : [],
        });
      }
      return json({}, 404);
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <HomePage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { paths, user: userEvent.setup() };
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("Personal workbench home", () => {
  it("shows independently scoped rows and one action per item", async () => {
    const { user } = open();
    const assigned = await screen.findByRole("tab", { name: "指派给我 2" });
    expect(assigned).toHaveAttribute("aria-selected", "true");
    const table = screen.getByRole("table", { name: "指派给我" });
    for (const name of ["事项", "类型", "更新时间 / 截止", "操作"]) {
      expect(within(table).getByRole("columnheader", { name })).toBeVisible();
    }
    expect(within(table).getAllByRole("link")).toHaveLength(2);
    expect(within(table).queryByText("补充附件")).not.toBeInTheDocument();
    expect(within(table).queryByText("其他人的审查")).not.toBeInTheDocument();
    expect(within(table).getByRole("link", { name: "查看任务：确认质保条款" })).toHaveAttribute(
      "href",
      "/workbench/tasks?task=task-mine",
    );
    expect(within(table).getByRole("link", { name: "查看补件：补充采购文件" })).toHaveAttribute(
      "href",
      "/cases/case-1/reviews/run-1?attempt_id=attempt-1&work_kind=request&work_item=request-1&drawer=record&tab=supplement",
    );
    await user.click(screen.getByRole("tab", { name: "团队待处理 2" }));
    const team = within(screen.getByRole("table", { name: "团队待处理" }));
    expect(team.getAllByText("3 条线索待判断")).toHaveLength(2);
    expect(team.getByRole("link", { name: "去判断：设备维保合同" })).toHaveAttribute(
      "href",
      "/cases/case%2Fold/reviews/run%20old",
    );
    expect(team.getByRole("link", { name: "查看案件：专线采购合同" })).toHaveAttribute(
      "href",
      "/cases/case-1",
    );
    await user.click(screen.getByRole("tab", { name: "待我会签 1" }));
    expect(await screen.findByRole("link", { name: "查看会签：待会签合同" })).toHaveAttribute(
      "href",
      "/workbench/approvals?view=todo&task=approval-1",
    );
    await user.click(screen.getByRole("tab", { name: "整改待确认 1" }));
    expect(
      within(screen.getByRole("table", { name: "整改待确认" })).getByRole("link"),
    ).toHaveAttribute("href", "/cases/case%2Fold?work_kind=remediation&work_item=remediation-1");
    expect(screen.queryByText("可见案件待处理")).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "上次没传完" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "最近打开" })).not.toBeInTheDocument();
  });

  it("defaults to team if personal work is confirmed empty and keeps a manual selection", async () => {
    const data = snapshot();
    data.tasks = [];
    data.cases = [caseRecord({ id: "team-case", pending_decisions: 2 })];
    const { user } = open({ data, emptyApprovals: true });
    const team = await screen.findByRole("tab", { name: "团队待处理 1", selected: true });
    expect(team).toBeVisible();
    expect(screen.getByRole("table", { name: "团队待处理" })).toHaveTextContent("2 条线索待判断");
    await user.click(screen.getByRole("tab", { name: "指派给我 0" }));
    expect(screen.getByRole("tab", { name: "指派给我 0", selected: true })).toBeVisible();
    expect(screen.getByRole("table", { name: "指派给我" })).toHaveTextContent("暂无事项");
  });

  it("shows separate work for two evidence requests sharing one task, retaining exact attempts", () => {
    const data = snapshot();
    const scopedCases = data.cases.filter((item) => item.id === "case-1");
    const requests = [
      evidence(),
      evidence({ id: "request-2", source_run_id: "run-2", source_attempt_id: "attempt-2" }),
    ];
    const rows = inboxRows({ ...data, cases: scopedCases }, actor.id, requests, []);
    expect(rows.filter((row) => row.kind === "evidence").map((row) => row.href)).toEqual([
      "/cases/case-1/reviews/run-1?attempt_id=attempt-1&work_kind=request&work_item=request-1&drawer=record&tab=supplement",
      "/cases/case-1/reviews/run-2?attempt_id=attempt-2&work_kind=request&work_item=request-2&drawer=record&tab=supplement",
    ]);
    expect(rows).toHaveLength(3);
  });

  it("shows lower bounds on denied evidence and paginated approval reads", async () => {
    const { user } = open({ deniedCase: "case/old", moreApprovals: true });
    expect(await screen.findByText(/部分案件的补件未能读取/)).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "指派给我 至少 2" })).toBeVisible();
    const approvals = await screen.findByRole("tab", { name: "待我会签 至少 1" });
    await user.click(approvals);
    expect(screen.getByRole("link", { name: "查看全部待会签" })).toHaveAttribute(
      "href",
      "/workbench/approvals?view=todo",
    );
  });

  it("previews three actions in a dense inbox and keeps all tasks reachable", async () => {
    const data = snapshot();
    data.tasks = Array.from({ length: 5 }, (_, index) =>
      task({ id: `t-${index}`, title: `待办 ${index}` }),
    );
    const { user } = open({ data, emptyApprovals: true });
    await screen.findByRole("tab", { name: "指派给我 6" });
    const table = screen.getByRole("table");
    expect(within(table).getAllByRole("link")).toHaveLength(3);
    const all = screen.getByRole("button", { name: "查看全部待办" });
    await user.click(all);
    expect(within(table).getAllByRole("link")).toHaveLength(6);
    expect(within(table).getByRole("link", { name: "查看任务：待办 4" })).toHaveAttribute(
      "href",
      "/workbench/tasks?task=t-4",
    );
    await user.click(screen.getByRole("button", { name: "收起待办" }));
    expect(within(table).getAllByRole("link")).toHaveLength(3);
  });

  it("loads visible cases in batches and treats all unread cases as uncovered", async () => {
    const data = snapshot();
    data.cases = Array.from({ length: 9 }, (_, index) => caseRecord({ id: `batch-${index}` }));
    const { paths, user } = open({ data, emptyApprovals: true });
    const more = await screen.findByRole("button", { name: "核对更多案件" });
    await waitFor(() => expect(more).toBeEnabled());
    expect(paths.filter((path) => path.endsWith("/evidence-requests"))).toHaveLength(8);
    expect(screen.getByRole("tab", { name: "指派给我 未确认" })).toBeVisible();
    await user.click(more);
    await screen.findByRole("tab", { name: "指派给我 0" });
    expect(paths.filter((path) => path.endsWith("/evidence-requests"))).toHaveLength(9);
  });

  it("shows only authorized cases actually visited by the current browser identity", async () => {
    recordRecentCaseVisit(actor, "case-1");
    recordRecentCaseVisit(actor, "case-1");
    recordRecentCaseVisit(actor, "revoked-case");
    recordRecentCaseVisit(
      {
        ...actor,
        active_context: { ...session.active_context, membership_id: "membership-other" },
      },
      "case/old",
    );
    open();
    const recent = await screen.findByRole("region", { name: "最近打开" });
    expect(await within(recent).findByRole("link", { name: "专线采购合同" })).toHaveAttribute(
      "href",
      "/cases/case-1",
    );
    expect(within(recent).getAllByRole("link")).toHaveLength(1);
    expect(within(recent).queryByText("revoked-case")).not.toBeInTheDocument();
  });

  it("does not display unverified case data when the snapshot cannot be read", async () => {
    recordRecentCaseVisit(actor, "case-1");
    open({ deniedWorkbench: true, emptyApprovals: true });
    expect(await screen.findByText("任务和可见案件暂未加载。")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "指派给我 未确认" })).toBeVisible();
    expect(screen.queryByRole("region", { name: "最近打开" })).not.toBeInTheDocument();
  });

  it("reports exact zero when the visible-case scope is confirmed empty", async () => {
    const data = snapshot();
    data.cases = [];
    open({ data, emptyApprovals: true });
    expect(await screen.findByRole("tab", { name: "团队待处理 0" })).toBeVisible();
    expect(screen.getByRole("tab", { name: "指派给我 0", selected: true })).toBeVisible();
  });
});
