// File metadata (manifest mirror):
// Purpose: Checks app-shell identity and breadcrumb behavior plus ordinary/admin role navigation visibility without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-11-12
// Revision: r6
// Copyright: See LICENSE
// End file metadata.

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation, useNavigationType } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Actor } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { AppShell } from "./app-shell";

const mocks = vi.hoisted(() => ({
  useSession: vi.fn(),
  useLogout: vi.fn(),
  useCaseDetail: vi.fn(),
  useContextChoices: vi.fn(),
  useSwitchContext: vi.fn(),
}));

vi.mock("../api/queries", () => ({
  useSession: mocks.useSession,
  useLogout: mocks.useLogout,
  useCaseDetail: mocks.useCaseDetail,
  useContextChoices: mocks.useContextChoices,
  useSwitchContext: mocks.useSwitchContext,
}));

const mutateAsync = vi.fn<() => Promise<void>>();

const actor: Actor = {
  id: "actor-1",
  display_name: "审查员甲",
  role: "reviewer",
  tenant_id: "tenant-1",
  org_id: "demo-org",
  audience: "client",
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
      {location.pathname}
      {location.search}|{navigationType}
    </output>
  );
}

function renderShell(path = "/") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/login" element={<LocationEcho />} />
          <Route element={<AppShell />}>
            <Route path="/cases" element={<LocationEcho />} />
            <Route path="/cases/:caseId/reviews/:runId/agent" element={<LocationEcho />} />
            <Route path="*" element={<p>页面内容</p>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function breadcrumbs(): HTMLElement {
  const nav = document.querySelector(".breadcrumbs");
  if (!(nav instanceof HTMLElement)) throw new Error("breadcrumbs not rendered");
  return nav;
}

beforeEach(() => {
  mocks.useSession.mockReturnValue({ data: actor });
  mocks.useLogout.mockReturnValue({
    isPending: false,
    mutateAsync,
  });
  mocks.useCaseDetail.mockReturnValue({ data: { title: "案件甲" } });
  mocks.useContextChoices.mockReturnValue({
    data: { context_switch_enabled: false, memberships: [] },
  });
  mocks.useSwitchContext.mockReturnValue({ isPending: false, mutateAsync: vi.fn() });
  mutateAsync.mockReset();
  mutateAsync.mockResolvedValue(undefined);
});

describe("app shell", () => {
  it("renders the competition brand, search and current identity", () => {
    renderShell();
    expect(
      screen.getByRole("link", {
        name: "第一届黄桷树AI智能体开发大赛，合同智能审计工作台首页",
      }),
    ).toHaveAttribute("href", "/");
    expect(screen.getByLabelText("全局搜索案件")).toBeInTheDocument();
    const who = screen.getByLabelText("当前身份");
    expect(within(who).getByText("审查员甲")).toBeInTheDocument();
    expect(within(who).getByText("审查员")).toBeInTheDocument();
    expect(within(who).getByText("审")).toHaveClass("ant-avatar-string");
    expect(screen.getByText("租户一")).toBeInTheDocument();
    expect(screen.getByText("页面内容")).toBeInTheDocument();
    expect(screen.getByText("跳到主要内容")).toBeInTheDocument();
  });

  it("shows the district roster name for a previous demo person label", () => {
    mocks.useSession.mockReturnValue({
      data: { ...actor, id: "demo-admin", display_name: "演示管理员", role: "admin" },
    });
    renderShell();
    const who = screen.getByLabelText("当前身份");
    expect(within(who).getByText("James Carter")).toBeInTheDocument();
    expect(within(who).queryByText("演示管理员")).not.toBeInTheDocument();
  });

  it("shows an unknown identity while the session loads", () => {
    mocks.useSession.mockReturnValue({ data: undefined });
    renderShell();
    expect(screen.getByText("身份未知")).toBeInTheDocument();
    expect(screen.queryByText("租户一")).not.toBeInTheDocument();
  });

  it.each([
    ["/", ["工作台"]],
    ["/review", ["工作台", "合同审查"]],
    ["/compare", ["工作台", "版本对照"]],
    ["/cases", ["工作台", "案件管理"]],
    ["/workbench/tasks", ["工作台", "业务管理", "审查任务"]],
    ["/workbench/unknown", ["工作台", "业务管理", "工作区"]],
    ["/api-docs", ["开发者", "API 接入"]],
    ["/elsewhere", ["开发者", "API 接入"]],
  ])("builds the breadcrumb for %s", (path, labels) => {
    renderShell(path);
    const nav = breadcrumbs();
    for (const label of labels) expect(nav).toHaveTextContent(label);
  });

  it("uses the case title and adds the review workspace crumb", () => {
    renderShell("/cases/case%20one/reviews/run-1");
    expect(mocks.useCaseDetail).toHaveBeenCalledWith("case one");
    const nav = breadcrumbs();
    expect(nav).toHaveTextContent("案件");
    expect(nav).toHaveTextContent("案件甲");
    expect(nav).toHaveTextContent("审查工作区");
    expect(within(nav).getByRole("link", { name: "案件" })).toHaveAttribute("href", "/cases");
    expect(within(nav).getByRole("link", { name: "案件甲" })).toHaveAttribute(
      "href",
      "/cases/case%20one",
    );
  });

  it("falls back to a generic case crumb while the detail loads", () => {
    mocks.useCaseDetail.mockReturnValue({ data: undefined });
    renderShell("/cases/case-1");
    expect(breadcrumbs()).toHaveTextContent("案件");
    expect(breadcrumbs()).not.toHaveTextContent("审查工作区");
    expect(breadcrumbs()).not.toHaveTextContent("案件甲");
  });

  it("searches cases and clears the selection and page parameters", async () => {
    const user = userEvent.setup();
    renderShell("/cases?run=needs_human&selected=c1&page=2");
    const search = screen.getByLabelText("全局搜索案件");
    await user.clear(search);
    await user.type(search, "  基站  ");
    await user.click(screen.getByRole("button", { name: "搜索" }));
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/cases?run=needs_human&q=%E5%9F%BA%E7%AB%99",
      ),
    );
  });

  it("removes the query when the search is cleared", async () => {
    const user = userEvent.setup();
    renderShell("/cases?q=old&run=active");
    const search = screen.getByLabelText("全局搜索案件");
    await user.clear(search);
    await user.click(screen.getByRole("button", { name: "搜索" }));
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent("/cases?run=active"),
    );
  });

  it("keeps the search box in sync with the URL", async () => {
    renderShell("/cases?q=基站");
    expect(screen.getByLabelText("全局搜索案件")).toHaveValue("基站");
  });

  it("marks the active navigation entry and review layout", () => {
    renderShell("/cases/case-1/reviews/run-1");
    expect(screen.getByRole("link", { name: "案件" })).toHaveAttribute("aria-current", "page");
    expect(document.querySelector(".app")).toHaveClass("app--review");
  });

  it.each([
    ["/compare", "app--compare"],
    ["/api-docs", "app--api-docs"],
  ])("marks the %s layout class", (path, className) => {
    renderShell(path);
    expect(document.querySelector(".app")).toHaveClass(className);
  });

  it("selects the parent navigation entry for nested module routes", () => {
    renderShell("/workbench/tasks/123");
    expect(screen.getByRole("link", { name: "审查任务" })).toHaveClass("active");
  });

  it("keeps unknown module and external paths unselected but readable", () => {
    renderShell("/workbench/unknown");
    expect(
      within(screen.getByLabelText("主导航")).getByRole("link", { name: "工作台" }),
    ).not.toHaveAttribute("aria-current", "page");
  });

  it("shows five named main entry points and sends Agent Studio to the saved execution list", () => {
    renderShell();
    const nav = within(screen.getByLabelText("主导航"));
    const mainEntries = nav.getByRole("list", { name: "五个主入口" });
    expect(within(mainEntries).getAllByRole("link")).toHaveLength(5);
    expect(nav.getByRole("link", { name: "工作台" })).toHaveAttribute("href", "/");
    expect(nav.getByRole("link", { name: "案件" })).toHaveAttribute("href", "/cases");
    expect(nav.getByRole("link", { name: "合同台账" })).toHaveAttribute("href", "/contracts");
    expect(nav.getByRole("link", { name: "知识与规则" })).toHaveAttribute(
      "href",
      "/workbench/knowledge",
    );
    expect(nav.getByRole("link", { name: "Agent Studio" })).toHaveAttribute(
      "href",
      "/workbench/executions",
    );
  });

  it.each([
    ["/review", "合同审查", "/review"],
    ["/compare", "版本对照", "/compare"],
    ["/cases", "案件", "/cases"],
    ["/support", "案件支援访问", "/support"],
    ["/contracts", "合同台账", "/contracts"],
    ["/workbench/approvals", "合同会签", "/workbench/approvals"],
    ["/subjects", "主体台账", "/subjects"],
    ["/workbench/tasks", "审查任务", "/workbench/tasks"],
    ["/workbench/remediation", "整改中心", "/workbench/remediation"],
    ["/workbench/reports", "报告中心", "/workbench/reports"],
    ["/workbench/evidence", "证据中心", "/workbench/evidence"],
    ["/workbench/evidence-sla", "补证时效", "/workbench/evidence-sla"],
    ["/workbench/knowledge", "知识与规则", "/workbench/knowledge"],
    ["/workbench/templates", "模板管理", "/workbench/templates"],
    ["/workbench/organization", "组织与权限", "/workbench/organization"],
    ["/workbench/tools", "工具网关", "/workbench/tools"],
    ["/workbench/executions", "Agent Studio", "/workbench/executions"],
    ["/docs/", "使用手册", "/docs/"],
    ["/api-docs", "API 文档", "/api-docs"],
  ])("keeps %s reachable from its selected entry group", (path, label, href) => {
    renderShell(path);
    expect(
      screen.getByLabelText("主导航").querySelector(`a[aria-label="${label}"]`),
    ).toHaveAttribute("href", href);
  });

  it("keeps public references and read-only directories visible while hiding admin operations", () => {
    renderShell("/workbench/knowledge");
    const nav = within(screen.getByLabelText("主导航"));
    expect(nav.getByRole("link", { name: "知识与规则" })).toHaveAttribute(
      "href",
      "/workbench/knowledge",
    );
    expect(nav.getByRole("link", { name: "模板管理" })).toHaveAttribute(
      "href",
      "/workbench/templates",
    );
    expect(nav.queryByRole("link", { name: "规则包治理" })).not.toBeInTheDocument();
    const utility = within(
      screen.getByRole("heading", { name: "只读目录与文档" }).closest("section") as HTMLElement,
    );
    expect(utility.getByRole("link", { name: "使用手册" })).toHaveAttribute("href", "/docs/");
    expect(utility.getByRole("link", { name: "API 文档" })).toHaveAttribute("href", "/api-docs");
    expect(utility.getByRole("link", { name: "组织与权限" })).toHaveAttribute(
      "href",
      "/workbench/organization",
    );
    expect(utility.getByRole("link", { name: "工具网关" })).toHaveAttribute(
      "href",
      "/workbench/tools",
    );
    for (const label of ["系统设置", "系统健康", "审计日志"]) {
      expect(utility.queryByRole("link", { name: label })).not.toBeInTheDocument();
    }
  });

  it("selects the Agent Studio entry for an attempt-specific detail route without rewriting its query", () => {
    renderShell("/cases/case-1/reviews/run-1/agent?attempt=attempt-7");
    const nav = within(screen.getByLabelText("主导航"));
    expect(nav.getByRole("link", { name: "Agent Studio" })).toHaveAttribute("aria-current", "page");
    expect(nav.getByRole("link", { name: "Agent Studio" })).toHaveAttribute(
      "href",
      "/workbench/executions",
    );
    expect(screen.getByTestId("location")).toHaveTextContent(
      "/cases/case-1/reviews/run-1/agent?attempt=attempt-7|POP",
    );
  });

  it("keeps the main landmark programmatically focusable", () => {
    renderShell();
    expect(document.querySelector("#main")).toHaveAttribute("tabindex", "-1");
  });

  it("collapses and expands the side navigation", async () => {
    const user = userEvent.setup();
    renderShell();
    await user.click(screen.getByRole("button", { name: "收合侧栏" }));
    expect(screen.getByRole("button", { name: "展开侧栏" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(document.querySelector(".app")).toHaveClass("app--nav-collapsed");
    expect(screen.getByRole("link", { name: "合同审查" })).toHaveAttribute("href", "/review");
    const mainNavigation = within(screen.getByRole("list", { name: "五个主入口" }));
    const mainEntries = mainNavigation.getAllByRole("link");
    mainEntries[0]!.focus();
    await user.tab();
    expect(document.activeElement).toBe(mainEntries[1]);
    await user.click(screen.getByRole("button", { name: "展开侧栏" }));
    expect(screen.getByRole("button", { name: "收合侧栏" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });

  it("links rulepack governance to the admin base when configured", () => {
    vi.stubEnv("VITE_ADMIN_URL", "http://admin.example.test/");
    mocks.useSession.mockReturnValue({ data: { ...actor, role: "admin" } });
    renderShell("/workbench/knowledge");
    expect(screen.getByRole("link", { name: "规则包治理" })).toHaveAttribute(
      "href",
      "http://admin.example.test/admin/rulepacks",
    );
    vi.unstubAllEnvs();
  });

  it("links rulepack governance locally by default", () => {
    mocks.useSession.mockReturnValue({ data: { ...actor, role: "admin" } });
    renderShell("/workbench/knowledge");
    expect(screen.getByRole("link", { name: "规则包治理" })).toHaveAttribute(
      "href",
      "/admin/rulepacks",
    );
  });

  it("shows operation links for an admin client actor without changing their destinations", () => {
    mocks.useSession.mockReturnValue({ data: { ...actor, role: "admin" } });
    renderShell();
    const utility = within(
      screen.getByRole("heading", { name: "管理与文档" }).closest("section") as HTMLElement,
    );
    expect(utility.getByRole("link", { name: "系统设置" })).toHaveAttribute(
      "href",
      "/workbench/settings",
    );
    expect(utility.getByRole("link", { name: "系统健康" })).toHaveAttribute(
      "href",
      "/workbench/health",
    );
    expect(utility.getByRole("link", { name: "审计日志" })).toHaveAttribute(
      "href",
      "/workbench/audit",
    );
  });

  it("keeps the handbook and API reference in the read-only links for ordinary roles", () => {
    renderShell();
    const group = screen.getByRole("heading", { name: "只读目录与文档" }).closest("section");
    expect(group).not.toBeNull();
    expect(within(group as HTMLElement).getByRole("link", { name: "使用手册" })).toHaveAttribute(
      "href",
      "/docs/",
    );
    expect(within(group as HTMLElement).getByRole("link", { name: "API 文档" })).toHaveAttribute(
      "href",
      "/api-docs",
    );
  });

  it("logs out to the login page", async () => {
    const user = userEvent.setup();
    renderShell("/cases");
    await user.click(screen.getByRole("button", { name: "退出" }));
    expect(mutateAsync).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/login|REPLACE"));
  });

  it("disables logout while pending", () => {
    mocks.useLogout.mockReturnValue({ isPending: true, mutateAsync });
    renderShell();
    expect(screen.getByRole("button", { name: "退出" })).toBeDisabled();
  });
});
