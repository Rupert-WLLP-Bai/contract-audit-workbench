// File metadata (manifest mirror):
// Purpose: Verifies personal home links, navigation, case return scoping, avatar actions and professional preference isolation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-11-20
// Revision: r9
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
import { browserIdentityKey } from "../domain/browser-identity";
import { actorReadContextKey } from "../domain/actor-read-context";

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
    <>
      <output data-testid="location">
        {location.pathname}
        {location.search}|{navigationType}
      </output>
      <output data-testid="route-state">{JSON.stringify(location.state)}</output>
    </>
  );
}

function renderShell(path = "/", state?: unknown) {
  const { pathname, search, hash } = new URL(path, "https://navigation.invalid");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = () => (
    <QueryClientProvider client={client}>
      <MemoryRouter
        initialEntries={[state === undefined ? path : { pathname, search, hash, state }]}
      >
        <Routes>
          <Route path="/login" element={<LocationEcho />} />
          <Route element={<AppShell />}>
            <Route path="/contract-desk" element={<LocationEcho />} />
            <Route path="/cases" element={<LocationEcho />} />
            <Route path="/cases/:caseId/reviews/:runId/agent" element={<LocationEcho />} />
            <Route path="*" element={<p>页面内容</p>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
  const view = render(tree());
  return () => view.rerender(tree());
}

function breadcrumbs(): HTMLElement {
  const nav = document.querySelector(".breadcrumbs");
  if (!(nav instanceof HTMLElement)) throw new Error("breadcrumbs not rendered");
  return nav;
}

beforeEach(() => {
  localStorage.clear();
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
  it("renders the home brand, case search and account menu without competition branding", async () => {
    renderShell();
    expect(screen.getByRole("link", { name: "合同审计工作台首页" })).toHaveAttribute("href", "/");
    expect(screen.queryByText(/黄桷树/)).not.toBeInTheDocument();
    expect(screen.getByLabelText("全局搜索案件")).toBeInTheDocument();
    expect(within(screen.getByLabelText("当前身份")).getByText("审查员甲")).toBeInTheDocument();
    expect(screen.queryByText("租户一")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "头像菜单" }));
    const profile = screen.getByRole("group", { name: "个人设置" });
    expect(within(profile).getByText("审查员")).toBeInTheDocument();
    expect(within(profile).getByText("租户一 · 演示组织")).toBeInTheDocument();
    expect(
      within(profile).queryByRole("combobox", { name: "切换工作上下文" }),
    ).not.toBeInTheDocument();
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
    ["/review", ["我的待办", "合同审查"]],
    ["/compare", ["我的待办", "版本对照"]],
    ["/cases", ["我的待办", "案件管理"]],
    ["/workbench/tasks", ["我的待办", "业务管理", "审查任务"]],
    ["/workbench/unknown", ["我的待办", "业务管理", "工作区"]],
    ["/api-docs", ["开发者", "API 接入"]],
    ["/elsewhere", ["开发者", "API 接入"]],
  ])("builds the breadcrumb for %s", (path, labels) => {
    renderShell(path);
    const nav = breadcrumbs();
    for (const label of labels) expect(nav).toHaveTextContent(label);
  });

  it("uses the case title and adds the review workspace crumb outside focused reading", () => {
    renderShell("/cases/case%20one/reviews/run-1/agent");
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

  it.each([true, false])(
    "restores an authorized case list breadcrumb (same identity: %s)",
    async (sameIdentity) => {
      const user = userEvent.setup();
      const scope = actorReadContextKey(actor);
      const returnTo = "/cases?view=mine&q=基站&selected=case-1";
      mocks.useSession.mockReturnValue({ data: actor, isSuccess: true, isFetching: false });
      renderShell("/cases/case-1", {
        returnTo,
        scrollY: 248,
        actorScope: sameIdentity ? scope : "identity-before-switch",
        selectedId: "case-1",
      });
      const link = within(breadcrumbs()).getByRole("link", { name: "案件" });
      if (sameIdentity)
        expect(link).toHaveAttribute("href", expect.stringContaining("selected=case-1"));
      else expect(link).toHaveAttribute("href", "/cases");
      await user.click(link);
      if (sameIdentity)
        expect(JSON.parse(screen.getByTestId("route-state").textContent!)).toEqual({
          restoreScrollY: 248,
          actorScope: scope,
        });
      else expect(screen.getByTestId("route-state")).toHaveTextContent("null");
    },
  );

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

  it("leaves the contract desk query to its own search", () => {
    renderShell("/contract-desk?q=园区");
    expect(screen.queryByLabelText("全局搜索案件")).not.toBeInTheDocument();
    expect(within(breadcrumbs()).getByRole("link", { name: "我的待办" })).toHaveAttribute(
      "href",
      "/",
    );
    expect(screen.getByTestId("location")).toHaveTextContent("/contract-desk?q=园区");
  });

  it("keeps the search box in sync with the URL", async () => {
    renderShell("/cases?q=基站");
    expect(screen.getByLabelText("全局搜索案件")).toHaveValue("基站");
  });

  it("keeps the full navigation available in a drawer on the review reading route", async () => {
    const user = userEvent.setup();
    renderShell("/cases/case-1/reviews/run-1");
    const rail = screen.getByRole("navigation", { name: "主导航" });
    expect(within(rail).getByRole("link", { name: "我的待办" })).toHaveAttribute("href", "/");
    expect(within(rail).getByRole("link", { name: "案件与材料" })).toHaveAttribute(
      "href",
      "/cases",
    );
    expect(screen.queryByLabelText("全局搜索案件")).not.toBeInTheDocument();
    expect(document.querySelector(".app")).toHaveClass("app--review");
    const trigger = screen.getByRole("button", { name: "打开主导航" });
    trigger.focus();
    await user.keyboard("{Enter}");
    const drawer = await screen.findByRole("dialog", { name: "主导航" });
    expect(screen.getAllByRole("navigation", { name: "主导航" })).toEqual([
      within(drawer).getByRole("navigation", { name: "主导航" }),
    ]);
    expect(within(drawer).getByRole("link", { name: "案件与材料" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    await user.keyboard("{Escape}");
    await waitFor(() => expect(trigger).toHaveFocus());
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
    expect(screen.getByRole("link", { name: "任务管理" })).toHaveClass("active");
  });

  it("keeps unknown module and external paths unselected but readable", () => {
    renderShell("/workbench/unknown");
    expect(
      within(screen.getByLabelText("主导航")).getByRole("link", { name: "合同清单" }),
    ).not.toHaveAttribute("aria-current", "page");
  });

  it("gives each navigation destination a single Tab stop and preserves arrow navigation", async () => {
    const user = userEvent.setup();
    renderShell("/cases");
    const nav = within(screen.getByRole("navigation", { name: "主导航" }));
    const home = nav.getByRole("link", { name: "我的待办" });
    const review = nav.getByRole("link", { name: "发起审查" });
    expect(nav.queryAllByRole("menuitem")).toHaveLength(0);
    expect(nav.getAllByRole("link", { name: "我的待办" })).toEqual([home]);
    expect(review).toHaveAttribute("href", "/review");
    home.focus();
    await user.tab();
    expect(review).toHaveFocus();
    await user.keyboard("{ArrowUp}");
    expect(home).toHaveFocus();
  });

  it("puts the inbox first and only shows professional links when enabled", async () => {
    const user = userEvent.setup();
    renderShell();
    const nav = within(screen.getByRole("navigation", { name: "主导航" }));
    expect(nav.getAllByRole("link")[0]).toHaveAttribute("href", "/");
    expect(nav.getByRole("link", { name: "我的待办" })).toHaveAttribute("aria-current", "page");
    expect(nav.getByRole("link", { name: "合同清单" })).toHaveAttribute("href", "/contract-desk");
    expect(nav.getByRole("link", { name: "案件与材料" })).toHaveAttribute("href", "/cases");
    await user.click(nav.getByRole("button", { name: "更多工具" }));
    expect(nav.queryByRole("link", { name: "Agent Studio" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "头像菜单" }));
    expect(screen.getByRole("switch", { name: "专业模式" })).not.toBeChecked();
    await user.click(screen.getByRole("switch", { name: "专业模式" }));
    expect(nav.getByRole("link", { name: "Agent Studio" })).toHaveAttribute(
      "href",
      "/workbench/executions",
    );
    expect(screen.getByText("审查员 · reviewer")).toBeInTheDocument();
    expect(nav.queryByRole("link", { name: "系统健康" })).not.toBeInTheDocument();
  });

  it("persists the preference for this identity only and restores it when returning", async () => {
    const user = userEvent.setup();
    const refresh = renderShell();
    await user.click(screen.getByRole("button", { name: "头像菜单" }));
    await user.click(screen.getByRole("switch", { name: "专业模式" }));
    const other = {
      ...actor,
      active_context: { ...actor.active_context, membership_id: "member-2" },
    };
    mocks.useSession.mockReturnValue({ data: other });
    refresh();
    expect(screen.getByRole("switch", { name: "专业模式" })).not.toBeChecked();
    expect(screen.queryByText("审查员 · reviewer")).not.toBeInTheDocument();
    mocks.useSession.mockReturnValue({
      data: {
        ...actor,
        active_context: { ...actor.active_context, context_version: "32" },
      },
    });
    refresh();
    expect(screen.getByRole("switch", { name: "专业模式" })).toBeChecked();
    expect(screen.getByText("审查员 · reviewer")).toBeInTheDocument();
  });

  it("shows the context selector only when the selected identity can switch to another", async () => {
    const user = userEvent.setup();
    const current = {
      id: actor.active_context.membership_id,
      tenant_name: actor.active_context.tenant_name,
      org_name: actor.active_context.org_name,
    };
    mocks.useSession.mockReturnValue({ data: { ...actor, context_switch_enabled: true } });
    mocks.useContextChoices.mockReturnValue({
      isSuccess: true,
      data: { context_switch_enabled: true, memberships: [current] },
    });
    const refresh = renderShell();
    await user.click(screen.getByRole("button", { name: "头像菜单" }));
    expect(screen.queryByRole("combobox", { name: "切换工作上下文" })).not.toBeInTheDocument();
    mocks.useContextChoices.mockReturnValue({
      isSuccess: true,
      data: {
        context_switch_enabled: true,
        memberships: [
          current,
          { id: "another-member", tenant_name: "租户二", org_name: "演示组织二" },
        ],
      },
    });
    refresh();
    expect(screen.getByRole("combobox", { name: "切换工作上下文" })).toBeInTheDocument();
  });

  it.each([
    ["/review", "发起审查", "/review"],
    ["/compare", "版本对照", "/compare"],
    ["/cases", "案件与材料", "/cases"],
    ["/support", "案件支援访问", "/support"],
    ["/contracts", "合同登记与履约", "/contracts"],
    ["/workbench/approvals", "合同会签", "/workbench/approvals"],
    ["/subjects", "主体台账", "/subjects"],
    ["/workbench/tasks", "任务管理", "/workbench/tasks"],
    ["/workbench/remediation", "整改中心", "/workbench/remediation"],
    ["/workbench/reports", "报告中心", "/workbench/reports"],
    ["/workbench/evidence", "证据中心", "/workbench/evidence"],
    ["/workbench/evidence-sla", "补证时效", "/workbench/evidence-sla"],
    ["/workbench/knowledge", "知识库", "/workbench/knowledge"],
    ["/", "我的待办", "/"],
    ["/contract-desk", "合同清单", "/contract-desk"],
    ["/workbench/templates", "模板管理", "/workbench/templates"],
    ["/workbench/organization", "组织与权限", "/workbench/organization"],
    ["/workbench/tools", "工具网关", "/workbench/tools"],
    ["/workbench/executions", "Agent Studio", "/workbench/executions"],
    ["/docs/", "使用手册", "/docs/"],
    ["/api-docs", "API 文档", "/api-docs"],
  ])("keeps %s reachable from its selected entry group", async (path, label, href) => {
    if (["/workbench/tools", "/workbench/executions", "/api-docs"].includes(path)) {
      localStorage.setItem("caw:professional-mode:" + browserIdentityKey(actor), "true");
    }
    renderShell(path);
    const nav = within(screen.getByRole("navigation", { name: "主导航" }));
    if (path === "/docs/") {
      await userEvent.click(nav.getByRole("button", { name: "知识与规则" }));
    }
    expect(nav.getByRole("link", { name: label })).toHaveAttribute("href", href);
  });

  it("keeps public references and read-only directories visible while hiding admin operations", async () => {
    const user = userEvent.setup();
    renderShell("/workbench/knowledge");
    const nav = within(screen.getByRole("navigation", { name: "主导航" }));
    expect(nav.getByRole("link", { name: "知识库" })).toHaveAttribute(
      "href",
      "/workbench/knowledge",
    );
    expect(nav.getByRole("link", { name: "模板管理" })).toHaveAttribute(
      "href",
      "/workbench/templates",
    );
    await user.click(nav.getByRole("button", { name: "更多工具" }));
    expect(nav.getByRole("link", { name: "使用手册" })).toHaveAttribute("href", "/docs/");
    expect(nav.queryByRole("link", { name: "API 文档" })).not.toBeInTheDocument();
    expect(nav.getByRole("link", { name: "组织与权限" })).toHaveAttribute(
      "href",
      "/workbench/organization",
    );
    expect(nav.queryByRole("link", { name: "工具网关" })).not.toBeInTheDocument();
    for (const label of ["系统设置", "系统健康", "审计日志", "规则包治理"]) {
      expect(nav.queryByRole("link", { name: label })).not.toBeInTheDocument();
    }
  });

  it("keeps an attempt-specific professional route intact while its view is disabled", async () => {
    const user = userEvent.setup();
    renderShell("/cases/case-1/reviews/run-1/agent?attempt=attempt-7");
    expect(screen.getByText("请在头像菜单中打开专业模式")).toBeInTheDocument();
    expect(screen.queryByTestId("location")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "头像菜单" }));
    await user.click(screen.getByRole("switch", { name: "专业模式" }));
    const nav = within(screen.getByLabelText("主导航"));
    if (!nav.queryByRole("link", { name: "Agent Studio" })) {
      await user.click(nav.getByRole("button", { name: "更多工具" }));
    }
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
    expect(screen.getByRole("link", { name: "合同会签" })).toHaveAttribute(
      "href",
      "/workbench/approvals",
    );
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

  it("shows operation links for an admin client actor without changing their destinations", async () => {
    mocks.useSession.mockReturnValue({ data: { ...actor, role: "admin" } });
    renderShell();
    const utility = within(screen.getByRole("navigation", { name: "主导航" }));
    await userEvent.click(utility.getByRole("button", { name: "更多工具" }));
    expect(utility.getByRole("link", { name: "系统设置" })).toHaveAttribute(
      "href",
      "/workbench/settings",
    );
    expect(utility.queryByRole("link", { name: "系统健康" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "头像菜单" }));
    await userEvent.click(screen.getByRole("switch", { name: "专业模式" }));
    expect(utility.getByRole("link", { name: "系统健康" })).toHaveAttribute(
      "href",
      "/workbench/health",
    );
    expect(utility.getByRole("link", { name: "审计日志" })).toHaveAttribute(
      "href",
      "/workbench/audit",
    );
  });

  it("logs out to the login page", async () => {
    const user = userEvent.setup();
    renderShell("/cases");
    await user.click(screen.getByRole("button", { name: "头像菜单" }));
    await user.click(screen.getByRole("button", { name: "退出" }));
    expect(mutateAsync).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/login|REPLACE"));
  });

  it("disables logout while pending", async () => {
    mocks.useLogout.mockReturnValue({ isPending: true, mutateAsync });
    renderShell();
    await userEvent.click(screen.getByRole("button", { name: "头像菜单" }));
    expect(screen.getByRole("button", { name: "退出" })).toBeDisabled();
  });
});
