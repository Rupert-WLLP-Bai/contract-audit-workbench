import { cleanup, screen } from "@testing-library/react";
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

function snapshot(observedAt: string) {
  return {
    schema_version: "system-health/v1",
    observed_at: observedAt,
    mode: "kubernetes",
    source: "kubernetes",
    cluster: null,
    architecture: [],
    services: [
      {
        id: "core-api",
        name: "核心业务 API",
        category: "application",
        kind: "container",
        status: "ready",
        detail: "Pod 正在运行且所有容器已就绪。",
        target: "http://core-api:8081",
        depends_on: [],
        container: {
          id: "pod-a",
          name: "core-api-a",
          image: "backend:v1",
          state: "running",
          health: "healthy",
          started_at: "2026-10-27T10:00:00Z",
          restarts: 0,
        },
        probe: null,
        logical_id: "core-api",
        instance_id: "pod-a",
        namespace: "contract-audit",
        node_name: "node-a",
        workload: "core-api",
        observed_at: observedAt,
      },
      {
        id: "core-api/core-api-b",
        name: "core-api / core-api-b",
        category: "application",
        kind: "container",
        status: "ready",
        detail: "Pod 正在运行且所有容器已就绪。",
        target: "http://core-api:8081",
        depends_on: [],
        container: {
          id: "pod-b",
          name: "core-api-b",
          image: "backend:v1",
          state: "running",
          health: "healthy",
          started_at: "2026-10-27T10:00:00Z",
          restarts: 0,
        },
        probe: null,
        logical_id: "core-api",
        instance_id: "pod-b",
        namespace: "contract-audit",
        node_name: "node-b",
        workload: "core-api",
        observed_at: observedAt,
      },
    ],
    groups: [
      {
        id: "core-api",
        name: "核心业务 API",
        category: "application",
        status: "degraded",
        detail: "就绪 2/3；已观测 2 个实例；期望值来自 Kubernetes Deployment。",
        expected_instances: 3,
        observed_instances: 2,
        ready_instances: 2,
        expected_source: "deployment",
        observed_at: observedAt,
      },
    ],
  };
}

function open(value: unknown, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => json(value, status)),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <QueryClientProvider client={client}>
      <SystemHealthPage />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("system health page", () => {
  it("shows controller expectations separately from observed instances", async () => {
    const user = userEvent.setup();
    const now = new Date().toISOString();
    open(snapshot(now));
    expect(await screen.findByText("服务组与副本")).toBeInTheDocument();
    expect(screen.getByText("2 / 3")).toBeInTheDocument();
    expect(screen.getByText("Kubernetes Deployment")).toBeInTheDocument();
    expect(screen.getByText("降级")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "容器清单" }));
    expect(await screen.findByText("node-a")).toBeInTheDocument();
    expect(screen.getByText("node-b")).toBeInTheDocument();
    expect(screen.getByText("pod-a")).toBeInTheDocument();
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
    expect(screen.queryByText("服务组与副本")).not.toBeInTheDocument();
  });
});
