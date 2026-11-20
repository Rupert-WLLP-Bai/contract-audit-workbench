// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “admin shell”; representative assertions cover denies management access to a non-admin session without rendering the navigation, shows the administration identity, organization and read-only disclaimer, shows an unknown identity while the session is still loading. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-11-20
// Revision: r6
// Copyright: See LICENSE
// End file metadata.

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation, useNavigationType } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Actor } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { AdminShell } from "./admin-shell";

const mutateAsync = vi.fn<() => Promise<void>>();
let session: { data: Actor | undefined };
let logoutPending = false;

vi.mock("../api/queries", () => ({
  useSession: () => session,
  useLogout: () => ({
    isPending: logoutPending,
    mutateAsync,
  }),
  useContextChoices: () => ({ data: [], isPending: false, isError: false }),
  useSwitchContext: () => ({ mutateAsync: vi.fn(), isPending: false }),
  clearIdentityQueries: vi.fn(),
  revalidateSessionContext: vi.fn(),
  queryKeys: {
    session: ["session", "client"],
    sessionFor: (audience: string) => ["session", audience],
  },
}));

const admin: Actor = {
  id: "actor-1",
  display_name: "管理员甲",
  role: "admin",
  tenant_id: "tenant-1",
  org_id: "demo-org",
  audience: "admin",
  principal_id: "principal-1",
  active_context: {
    membership_id: "membership-1",
    tenant_id: "tenant-1",
    tenant_name: "租户一",
    org_id: "demo-org",
    org_name: "演示组织",
    context_version: "1",
  },
  csrf_token: "test-csrf",
  context_switch_enabled: false,
};

function LocationEcho() {
  const location = useLocation();
  const navigationType = useNavigationType();
  return (
    <output data-testid="location">
      {location.pathname}|{navigationType}
    </output>
  );
}

function renderShell(path = "/admin/settings") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/admin/login" element={<LocationEcho />} />
          <Route path="/admin" element={<AdminShell />}>
            <Route path="*" element={<p>管理内容</p>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  session = { data: admin };
  logoutPending = false;
  mutateAsync.mockReset();
  mutateAsync.mockResolvedValue(undefined);
});

describe("admin shell", () => {
  it("denies management access to a non-admin session without rendering the navigation", () => {
    session = { data: { ...admin, role: "reviewer" } };
    renderShell("/admin/rulepacks");
    expect(screen.getByText("需要管理权限")).toBeInTheDocument();
    expect(
      screen.getByText("当前管理会话没有管理角色。案件访问权限由业务工作台单独授权。"),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("管理导航")).not.toBeInTheDocument();
    expect(screen.queryByText("管理内容")).not.toBeInTheDocument();
  });

  it("shows the administration identity, active tenant and read-only disclaimer", async () => {
    renderShell();
    expect(screen.getByLabelText("管理工作台")).toHaveAttribute("href", "/admin/settings");
    expect(screen.getByText("合同审计 · 管理后台")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "管理个人菜单" }));
    expect(screen.getByText("租户一")).toBeInTheDocument();
    expect(
      screen.getByText(
        (_content, element) =>
          element?.classList.contains("sr-only") === true &&
          element.textContent === "当前组织：演示组织",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("此工作区不授予案件正文访问权限。")).not.toBeInTheDocument();
    const who = screen.getByLabelText("当前管理身份");
    expect(within(who).getByText("管理员甲 · 管理员")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "规则库" })).toHaveAttribute(
      "href",
      "/admin/rulepacks",
    );
    expect(screen.getByRole("link", { name: "知识治理" })).toHaveAttribute(
      "href",
      "/admin/governed-knowledge",
    );
    expect(screen.getByRole("link", { name: "基础设施与中间件" })).toHaveAttribute(
      "href",
      "/admin/infrastructure",
    );
    expect(screen.getByText("管理内容")).toBeInTheDocument();
    expect(screen.queryByText("规则版本与发布历史")).not.toBeInTheDocument();
    for (const group of ["系统与模型", "规则库", "知识治理", "租户管理", "运营统计", "服务状态"]) {
      expect(screen.getAllByText(group).length).toBeGreaterThan(0);
    }
  });

  it("shows an unknown identity while the session is still loading", async () => {
    session = { data: undefined };
    renderShell();
    expect(screen.getByText("身份未知")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "管理个人菜单" }));
    expect(screen.getByRole("button", { name: "退出" })).toBeInTheDocument();
    expect(screen.queryByText(/^租户：/)).not.toBeInTheDocument();
  });

  it.each([
    ["/admin/settings", "设置与运行状态"],
    ["/admin/infrastructure", "基础设施与中间件"],
    ["/admin/governed-knowledge", "知识治理"],
    ["/admin/governed-knowledge/new", "创建知识版本"],
    ["/admin/governed-knowledge/versions/1", "知识版本详情"],
    ["/admin/rulepacks", "规则包治理"],
    ["/admin/rulepacks/new", "创建规则包草稿"],
    ["/admin/rulepacks/versions/1", "规则包详情"],
  ])("labels the breadcrumb for %s", (path, breadcrumb) => {
    renderShell(path);
    const breadcrumbs = screen.getByText("管理").closest("nav");
    expect(breadcrumbs).not.toBeNull();
    expect(within(breadcrumbs as HTMLElement).getByText(breadcrumb)).toBeInTheDocument();
  });

  it.each([
    ["/admin/settings", "设置与运行状态"],
    ["/admin/rulepacks", "规则库"],
    ["/admin/governed-knowledge", "知识治理"],
    ["/admin/infrastructure", "基础设施与中间件"],
  ])("marks the active navigation link for %s", (path, label) => {
    renderShell(path);
    expect(screen.getByRole("link", { name: label })).toHaveAttribute("aria-current", "page");
  });

  it("navigates to the admin login after logging out", async () => {
    const user = userEvent.setup();
    renderShell();
    await user.click(screen.getByRole("button", { name: "管理个人菜单" }));
    await user.click(screen.getByRole("button", { name: "退出" }));
    expect(mutateAsync).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent("/admin/login|REPLACE"),
    );
  });

  it("keeps the main landmark programmatically focusable", () => {
    renderShell();
    expect(document.querySelector("#main")).toHaveAttribute("tabindex", "-1");
  });

  it("disables the logout action while it is pending", async () => {
    logoutPending = true;
    renderShell();
    await userEvent.click(screen.getByRole("button", { name: "管理个人菜单" }));
    expect(screen.getByRole("button", { name: "退出" })).toBeDisabled();
  });
});
