import {
  QueryClient,
  QueryClientProvider,
  onlineManager,
} from "@tanstack/react-query";
import { cleanup, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router";
import { setSessionIdentity } from "../api/http";
import { queryKeys } from "../api/queries";
import type { WorkbenchCase, WorkbenchSnapshot } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { ReviewEntryPage } from "./review-entry-page";

const at = "2026-10-21T06:00:00Z";
const clients: QueryClient[] = [];

function snapshot(): WorkbenchSnapshot {
  const reviewCase: WorkbenchCase = {
    id: "case/alpha",
    title: "专线合同审查",
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
    tasks: [],
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
  if (data) {
    client.setQueryData(
      queryKeys.workbench("week"),
      data,
      updatedAt === undefined ? undefined : { updatedAt },
    );
  }
  return client;
}

function renderEntry(client: QueryClient) {
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/reviews"]}>
        <ReviewEntryPage />
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
    expect(screen.getByText("尚无审查运行")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "进入案件" })).toHaveAttribute(
      "href",
      "/cases/case%2Fwithout-review",
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
