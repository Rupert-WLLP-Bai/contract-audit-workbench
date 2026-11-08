// File metadata (manifest mirror):
// Purpose: Checks review entry rendering, exact run/attempt navigation, actor-assigned task filtering, recent-run filtering and ordering, URL-backed tabs/search, CTA destination, empty state, summary, and refresh/error states.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-11-08
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import {
  QueryClient,
  QueryClientProvider,
  onlineManager,
} from "@tanstack/react-query";
import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, useLocation } from "react-router";
import { setSessionIdentity } from "../api/http";
import { queryKeys } from "../api/queries";
import type { Actor, WorkbenchCase, WorkbenchSnapshot } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { ReviewEntryPage } from "./review-entry-page";

const at = "2026-10-21T06:00:00Z";
const clients: QueryClient[] = [];

function snapshot(): WorkbenchSnapshot {
  const reviewCase: WorkbenchCase = {
    id: "case/alpha",
    title: "专线合同审查",
    case_number: "BW-2026-09",
    business_line: "ENT",
    business_status: "active",
    contract_type: "service",
    document_count: 2,
    pending_decisions: 1,
    latest_run: null,
    updated_at: at,
    profile: null,
    severity: null,
    progress: 50,
  };
  const noRunCase: WorkbenchCase = {
    ...reviewCase,
    id: "case/without-review",
    title: "暂无运行案件",
    case_number: undefined,
    business_status: undefined,
    document_count: 0,
    pending_decisions: 0,
    progress: 0,
  };
  return {
    schema_version: "workbench/v1",
    generated_at: at,
    period: "week",
    dashboard: {
      visible_cases: 2,
      my_pending_tasks: 0,
      overdue_tasks: 0,
      pending_review_cases: 1,
      remediation_cases: 0,
      completed_reviews: 2,
      average_duration_ms: null,
      progress: [],
      risks: [],
    },
    cases: [reviewCase, noRunCase],
    profiles: [],
    tasks: [
      {
        id: "task-1",
        case_id: reviewCase.id,
        assignee_id: "actor-1",
        created_at: at,
        created_by: "actor-1",
        due_at: null,
        finding_id: null,
        kind: "decision",
        priority: "medium",
        status: "open",
        title: "核对审查决定",
        updated_at: at,
        version: 1,
      },
      {
        id: "task-other-actor",
        case_id: noRunCase.id,
        assignee_id: "actor-2",
        created_at: at,
        created_by: "actor-2",
        due_at: null,
        finding_id: null,
        kind: "decision",
        priority: "medium",
        status: "open",
        title: "其他成员待办",
        updated_at: at,
        version: 1,
      },
    ],
    relations: [],
    knowledge: [],
    members: [],
    remediations: [],
    reports: [],
    documents: [],
    runs: [
      {
        id: "run-old",
        case_id: reviewCase.id,
        case_title: reviewCase.title,
        state: "completed",
        created_at: at,
        updated_at: at,
        attempt_id: "attempt-old",
        started_at: at,
        finished_at: at,
        usage: null,
      },
      {
        id: "run-new",
        case_id: reviewCase.id,
        case_title: reviewCase.title,
        state: "partial",
        created_at: "2026-10-22T06:00:00Z",
        updated_at: "2026-10-22T06:00:00Z",
        attempt_id: "attempt/new",
        started_at: "2026-10-22T06:00:00Z",
        finished_at: "2026-10-22T06:01:00Z",
        usage: null,
      },
    ],
    audit: [],
    templates: [],
    model: {
      name: "",
      ocr_name: "",
      status: "unconfigured",
      label: "规则模式",
    },
    tools: [],
  };
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
  onlineManager.setOnline(true);
});

function makeClient(data?: WorkbenchSnapshot, updatedAt?: number) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  clients.push(client);
  const actor: Actor = {
    id: "actor-1",
    display_name: "审查员甲",
    role: "reviewer",
    tenant_id: "tenant-1",
    org_id: "org-1",
    audience: "client",
    principal_id: "principal-1",
    active_context: {
      membership_id: "membership-1",
      context_version: "1",
      tenant_id: "tenant-1",
      tenant_name: "租户甲",
      org_id: "org-1",
      org_name: "组织甲",
    },
    csrf_token: "test-token",
    context_switch_enabled: false,
  };
  client.setQueryData(queryKeys.session, actor);
  if (data) {
    client.setQueryData(
      queryKeys.workbench("week"),
      data,
      updatedAt === undefined ? undefined : { updatedAt },
    );
  }
  return client;
}

function LocationSearch() {
  const location = useLocation();
  return <output data-testid="location-search">{location.search}</output>;
}

function renderEntry(client: QueryClient, initialEntry = "/review") {
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <ReviewEntryPage />
        <LocationSearch />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("审查入口", () => {
  it("opens the newest run with its exact frozen attempt id", () => {
    const client = makeClient(snapshot());
    renderEntry(client);

    expect(screen.getByRole("link", { name: "打开审查" })).toHaveAttribute(
      "href",
      "/cases/case%2Falpha/reviews/run-new?attempt_id=attempt%2Fnew",
    );
    expect(screen.getByText("尚未发起")).toBeInTheDocument();
    expect(screen.getByText("业务编号：BW-2026-09")).toBeInTheDocument();
    expect(screen.getByText("进行中")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "发起审查" })).toHaveAttribute(
      "href",
      "/cases",
    );
    expect(screen.getByRole("link", { name: "进入案件" })).toHaveAttribute(
      "href",
      "/cases/case%2Fwithout-review",
    );
    const summary = within(
      screen.getByRole("list", { name: "审查工作区摘要" }),
    );
    expect(summary.getByText("可见案件").closest("li")).toHaveAttribute(
      "data-state",
      "filled",
    );
    expect(summary.getByText("本周运行").closest("li")).toHaveAttribute(
      "data-state",
      "filled",
    );
    expect(summary.getByText("待人工处理").closest("li")).toHaveAttribute(
      "data-state",
      "open",
    );
    expect(screen.getByRole("combobox")).toBeInTheDocument();
  });

  it("filters to tasks assigned to the actor and keeps the selected view in the URL", () => {
    const client = makeClient(snapshot());
    renderEntry(client, "/review?view=mine");

    expect(screen.getByRole("tab", { name: "待我处理" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByRole("row", { name: /专线合同审查/ })).toBeInTheDocument();
    expect(screen.queryByText("暂无运行案件")).not.toBeInTheDocument();
    expect(screen.getByTestId("location-search")).toHaveTextContent("view=mine");
  });

  it("filters recent reviews and sorts cases by the newest persisted run", () => {
    const client = makeClient(snapshot());
    renderEntry(client, "/review?view=recent");

    expect(screen.getByRole("tab", { name: "最近审查" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByRole("row", { name: /专线合同审查/ })).toBeInTheDocument();
    expect(screen.queryByText("暂无运行案件")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "打开审查" })).toHaveAttribute(
      "href",
      "/cases/case%2Falpha/reviews/run-new?attempt_id=attempt%2Fnew",
    );
  });

  it("searches visible cases and updates the URL without dropping the view", () => {
    const client = makeClient(snapshot());
    renderEntry(client, "/review?view=all");

    fireEvent.change(screen.getByRole("textbox", { name: "搜索案件" }), {
      target: { value: "without-review" },
    });

    expect(screen.getByRole("row", { name: /暂无运行案件/ })).toBeInTheDocument();
    expect(screen.queryByText("专线合同审查")).not.toBeInTheDocument();
    expect(screen.getByTestId("location-search")).toHaveTextContent(
      "view=all&q=without-review",
    );
  });

  it("shows the empty state when no cases are visible", () => {
    const data = snapshot();
    const client = makeClient({
      ...data,
      cases: [],
      runs: [],
      dashboard: {
        ...data.dashboard,
        visible_cases: 0,
        pending_review_cases: 0,
      },
    });
    renderEntry(client);

    expect(
      screen.getByRole("heading", { name: "当前身份没有可访问案件" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.getAllByText("0", { selector: "strong" })).toHaveLength(3);
    const summary = within(
      screen.getByRole("list", { name: "审查工作区摘要" }),
    );
    expect(summary.getByText("可见案件").closest("li")).toHaveAttribute(
      "data-state",
      "empty",
    );
    expect(summary.getByText("本周运行").closest("li")).toHaveAttribute(
      "data-state",
      "empty",
    );
    expect(summary.getByText("待人工处理").closest("li")).toHaveAttribute(
      "data-state",
      "filled",
    );
  });

  it("marks one visible case and one run as filled", () => {
    const data = snapshot();
    const client = makeClient({
      ...data,
      cases: [data.cases[0]!],
      runs: [data.runs[0]!],
      dashboard: {
        ...data.dashboard,
        visible_cases: 1,
        pending_review_cases: 0,
      },
    });
    renderEntry(client);

    const summary = within(
      screen.getByRole("list", { name: "审查工作区摘要" }),
    );
    expect(summary.getByText("可见案件").closest("li")).toHaveAttribute(
      "data-state",
      "filled",
    );
    expect(summary.getByText("本周运行").closest("li")).toHaveAttribute(
      "data-state",
      "filled",
    );
  });

  it("shows a refresh error when a stale workbench snapshot fails to reload", async () => {
    const client = makeClient(snapshot(), Date.now() - 60_000);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        Response.json(
          { error: { message: "工作区刷新暂不可用" } },
          { status: 503 },
        ),
      ),
    );
    renderEntry(client);

    expect(
      await screen.findByRole("heading", { name: "审查工作区没有加载" }),
    ).toBeInTheDocument();
    expect(screen.getByText("工作区刷新暂不可用")).toBeInTheDocument();
    expect(screen.queryByText("专线合同审查")).not.toBeInTheDocument();
  });

  it("shows the unloaded state when an offline workbench query has no cache", async () => {
    onlineManager.setOnline(false);
    const client = makeClient();
    renderEntry(client);

    expect(
      await screen.findByRole("heading", { name: "审查工作区没有加载" }),
    ).toBeInTheDocument();
    expect(screen.getByText("请求没有完成")).toBeInTheDocument();
    expect(screen.queryByText("合同审查")).not.toBeInTheDocument();
  });
});
