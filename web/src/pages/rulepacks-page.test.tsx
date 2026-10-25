import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router";
import { AdminApp } from "../admin-app";
import { setSessionIdentity } from "../api/http";
import { renderWithAntd as render } from "../test/antd";

const clients: QueryClient[] = [];
const requests: string[] = [];

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function open(listStatus: number) {
  requests.length = 0;
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    requests.push(path);
    if (path === "/api/session?audience=admin") {
      return json({
        audience: "admin",
        actor: {
          id: "admin-test",
          display_name: "测试管理员",
          role: "admin",
          tenant_id: "tenant-test",
          org_id: "org-test",
        },
      });
    }
    if (path === "/api/admin/rulepacks") {
      return json(
        {
          error: {
            code: listStatus === 403 ? "forbidden" : "service_unavailable",
            message: "规则包列表请求未完成",
            retryable: false,
          },
        },
        listStatus,
      );
    }
    return json(
      { error: { code: "not_found", message: "未找到测试路由" } },
      404,
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  const view = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/admin/rulepacks"]}>
        <AdminApp />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...view, user: userEvent.setup() };
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  requests.length = 0;
  vi.unstubAllGlobals();
});

describe("RulePack list authorization feedback", () => {
  it("does not offer a futile retry after the server denies access", async () => {
    open(403);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "当前管理身份无权查看规则包",
    );
    expect(
      screen.queryByRole("button", { name: "重试" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "返回管理首页" })).toHaveAttribute(
      "href",
      "/admin/settings",
    );
    expect(requests).toContain("/api/admin/rulepacks");
  });

  it("keeps retry for a transient service failure", async () => {
    const { user } = open(503);

    expect(
      await screen.findByRole("heading", { name: "规则包列表没有加载" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() =>
      expect(
        requests.filter((path) => path === "/api/admin/rulepacks"),
      ).toHaveLength(2),
    );
  });
});
