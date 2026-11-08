// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “system health page”; representative assertions cover the client and admin information architecture, real top-fact projection with missing observations kept as 未报告, controller expectations separated from observed instances, stale observations, read-only diagnostics and denied-session isolation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-11-08
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import { SystemHealthPage } from "./system-health-page";

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

type ServiceOverrides = {
  id: string;
  name: string;
  category?: string;
  kind?: string;
  status?: string;
  detail?: string;
  target?: string;
  depends_on?: string[];
  container?: {
    id: string;
    name: string;
    image: string;
    state: string;
    health: string;
    started_at: string;
    restarts: number;
  } | null;
  logical_id?: string;
  instance_id?: string;
  namespace?: string;
  node_name?: string;
  workload?: string;
  observed_at?: string;
};

function service(overrides: ServiceOverrides) {
  return {
    category: "application",
    kind: "container",
    status: "ready",
    detail: "",
    target: "",
    depends_on: [],
    container: null,
    probe: null,
    ...overrides,
  };
}

function container(name: string, state = "running", health = "healthy") {
  return {
    id: `${name}-id`,
    name,
    image: "example:v1",
    state,
    health,
    started_at: "2026-10-27T10:00:00Z",
    restarts: 0,
  };
}

type GroupOverrides = {
  id: string;
  name: string;
  status: string;
  detail: string;
  expected_instances: number;
  observed_instances: number;
  ready_instances: number;
  expected_source: "deployment" | "config" | "catalog" | "unavailable";
  category?: string;
  observed_at?: string;
};

function group(overrides: GroupOverrides) {
  return { category: "application", ...overrides };
}

function snapshot(observedAt: string) {
  return {
    schema_version: "system-health/v1",
    observed_at: observedAt,
    mode: "kubernetes",
    source: "kubernetes",
    cluster: null,
    architecture: [],
    services: [
      service({
        id: "core-api",
        name: "核心业务 API",
        status: "ready",
        detail: "Pod 正在运行且所有容器已就绪。",
        target: "http://core-api:8081",
        depends_on: ["db", "nats"],
        container: container("core-api-a"),
        logical_id: "core-api",
        instance_id: "pod-a",
        namespace: "contract-audit",
        node_name: "node-a",
        workload: "core-api",
        observed_at: observedAt,
      }),
      service({
        id: "core-api/core-api-b",
        name: "core-api / core-api-b",
        status: "ready",
        detail: "Pod 正在运行且所有容器已就绪。",
        target: "http://core-api:8081",
        depends_on: ["db", "nats"],
        container: container("core-api-b"),
        logical_id: "core-api",
        instance_id: "pod-b",
        namespace: "contract-audit",
        node_name: "node-b",
        workload: "core-api",
        observed_at: observedAt,
      }),
      service({
        id: "audit-worker",
        name: "审查 Worker",
        status: "unknown",
        detail: "容器运行中，未配置 Docker 健康检查。",
        target: "http://audit-worker:8086",
        depends_on: ["db", "nats", "document-service"],
        container: container("audit-worker-1", "running", ""),
        logical_id: "audit-worker",
        instance_id: "pod-worker",
        observed_at: observedAt,
      }),
      service({
        id: "db",
        name: "PostgreSQL 业务库",
        category: "middleware",
        status: "ready",
        detail: "数据库连接可用。",
        target: "db:5432",
        container: container("postgres-1"),
        logical_id: "db",
        instance_id: "pod-db",
        observed_at: observedAt,
      }),
      service({
        id: "document-service",
        name: "文档服务",
        status: "ready",
        detail: "文档服务就绪。",
        target: "http://document-service:8082",
        depends_on: ["db", "rustfs"],
        container: container("document-1"),
        logical_id: "document-service",
        instance_id: "pod-document",
        observed_at: observedAt,
      }),
      service({
        id: "nats",
        name: "NATS JetStream",
        category: "middleware",
        status: "ready",
        detail: "NATS 就绪。",
        target: "nats://nats:4222",
        container: container("nats-1"),
        logical_id: "nats",
        instance_id: "pod-nats",
        observed_at: observedAt,
      }),
    ],
    groups: [
      group({
        id: "core-api",
        name: "核心业务 API",
        status: "degraded",
        detail: "就绪 2/3；已观测 2 个实例；期望值来自 Kubernetes Deployment。",
        expected_instances: 3,
        observed_instances: 2,
        ready_instances: 2,
        expected_source: "deployment",
        observed_at: observedAt,
      }),
      group({
        id: "audit-worker",
        name: "审查 Worker",
        status: "unknown",
        detail: "容器运行中，未配置 Docker 健康检查。",
        expected_instances: 1,
        observed_instances: 1,
        ready_instances: 0,
        expected_source: "config",
        observed_at: observedAt,
      }),
      group({
        id: "db",
        name: "PostgreSQL 业务库",
        status: "ready",
        detail: "就绪 1/1；已观测 1 个实例；期望值来自内置服务清单。",
        expected_instances: 1,
        observed_instances: 1,
        ready_instances: 1,
        expected_source: "catalog",
        category: "middleware",
        observed_at: observedAt,
      }),
      group({
        id: "document-service",
        name: "文档服务",
        status: "ready",
        detail: "就绪 1/1；已观测 1 个实例；期望值来自内置服务清单。",
        expected_instances: 1,
        observed_instances: 1,
        ready_instances: 1,
        expected_source: "catalog",
        observed_at: observedAt,
      }),
      group({
        id: "nats",
        name: "NATS JetStream",
        status: "ready",
        detail: "就绪 1/1；已观测 1 个实例；期望值来自内置服务清单。",
        expected_instances: 1,
        observed_instances: 1,
        ready_instances: 1,
        expected_source: "catalog",
        category: "middleware",
        observed_at: observedAt,
      }),
    ],
  };
}

function open(
  value: unknown,
  status = 200,
  audience: "client" | "admin" = "client",
) {
  const fetchMock = vi.fn(
    async (_input: RequestInfo | URL, _init?: RequestInit) => json(value, status),
  );
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <QueryClientProvider client={client}>
      <SystemHealthPage audience={audience} />
    </QueryClientProvider>,
  );
  return fetchMock;
}

function tabNames(): string[] {
  return screen.getAllByRole("tab").map((tab) => tab.textContent ?? "");
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("system health page", () => {
  it("uses the client information architecture and derives top facts from reported fields", async () => {
    const now = new Date().toISOString();
    const fetchMock = open(snapshot(now));
    expect(await screen.findByText("服务概览")).toBeInTheDocument();
    expect(tabNames()).toEqual(["服务概览", "依赖与容量", "近期异常"]);
    expect(screen.queryByRole("tab", { name: "服务健康" })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/system-health");

    const facts = screen.getByRole("list", { name: "健康概览关键事实" });
    expect(within(facts).getByRole("listitem", { name: "Core" })).toBeInTheDocument();
    expect(within(facts).getByRole("listitem", { name: "Worker" })).toBeInTheDocument();
    expect(
      within(facts).getByRole("listitem", { name: "对象存储" }),
    ).toBeInTheDocument();
    expect(
      within(facts).getByRole("listitem", { name: "事件连接" }),
    ).toBeInTheDocument();
    expect(within(facts).queryByText("数据库")).not.toBeInTheDocument();

    const core = within(facts).getByRole("listitem", { name: "Core" });
    expect(within(core).getAllByText("降级")).toHaveLength(2);
    const worker = within(facts).getByRole("listitem", { name: "Worker" });
    expect(within(worker).getAllByText("运行中")).toHaveLength(2);
    const storage = within(facts).getByRole("listitem", { name: "对象存储" });
    expect(within(storage).getAllByText("未报告").length).toBeGreaterThanOrEqual(2);
    const events = within(facts).getByRole("listitem", { name: "事件连接" });
    expect(within(events).getAllByText("正常")).toHaveLength(2);
  });

  it("keeps controller expectations separately from observed instances in the capacity view", async () => {
    const user = userEvent.setup();
    const now = new Date().toISOString();
    open(snapshot(now));
    await screen.findByText("服务概览");
    await user.click(screen.getByRole("tab", { name: "依赖与容量" }));
    expect(await screen.findByText("服务组与副本")).toBeInTheDocument();
    expect(screen.getByText("2 / 3")).toBeInTheDocument();
    expect(screen.getByText("Kubernetes Deployment")).toBeInTheDocument();
    expect(screen.getAllByText("降级").length).toBeGreaterThan(0);
    await user.click(screen.getByText("展开容器清单"));
    expect(await screen.findByText("node-a")).toBeInTheDocument();
    expect(screen.getByText("node-b")).toBeInTheDocument();
    expect(screen.getByText("pod-a")).toBeInTheDocument();
  });

  it("routes the diagnostic entry to the exception view without issuing a write", async () => {
    const user = userEvent.setup();
    const now = new Date().toISOString();
    const fetchMock = open(snapshot(now));
    await screen.findByText("服务概览");
    await user.click(screen.getByRole("button", { name: "查看诊断" }));
    expect(screen.getByRole("tab", { name: "近期异常" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect((await screen.findAllByText("对象存储未报告")).length).toBeGreaterThan(0);
    expect(fetchMock.mock.calls.every((call) => call[1]?.method === "GET")).toBe(
      true,
    );
  });

  it("uses the admin information architecture and admin endpoint without leaking client tabs", async () => {
    const now = new Date().toISOString();
    const fetchMock = open(snapshot(now), 200, "admin");
    expect(
      await screen.findByRole("heading", { level: 1, name: "基础设施" }),
    ).toBeInTheDocument();
    expect(tabNames()).toEqual(["服务健康", "存储与网络", "容量与事件"]);
    expect(screen.queryByRole("tab", { name: "服务概览" })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/admin/system-health");

    const facts = screen.getByRole("list", { name: "健康概览关键事实" });
    expect(within(facts).getByRole("listitem", { name: "数据库" })).toBeInTheDocument();
    expect(within(facts).queryByText("事件连接")).not.toBeInTheDocument();
  });

  it("marks an old observation as expired instead of current normal", async () => {
    open(snapshot("2026-01-01T00:00:00Z"));
    expect(await screen.findByText("观测已过期")).toBeInTheDocument();
    expect(screen.getByText(/上次观测距现在/)).toBeInTheDocument();
  });

  it("does not keep a cached snapshot after the session is denied", async () => {
    open(
      {
        error: { code: "forbidden", message: "没有访问该内容的权限" },
      },
      403,
    );
    expect(
      await screen.findByText("没有访问系统健康的权限"),
    ).toBeInTheDocument();
    expect(screen.queryByText("服务概览")).not.toBeInTheDocument();
    expect(screen.queryByText("服务组与副本")).not.toBeInTheDocument();
  });
});
