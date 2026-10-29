import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Actor } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { AppShell } from "./app-shell";

const mocks = vi.hoisted(() => ({
  useSession: vi.fn(),
  useLogout: vi.fn(),
  useCaseDetail: vi.fn(),
}));

vi.mock("../api/queries", () => ({
  useSession: mocks.useSession,
  useLogout: mocks.useLogout,
  useCaseDetail: mocks.useCaseDetail,
}));

const mutateAsync = vi.fn<() => Promise<void>>();

const actor: Actor = {
  id: "actor-1",
  display_name: "审查员甲",
  role: "reviewer",
  tenant_id: "tenant-1",
  org_id: "demo-org",
  audience: "client",
};

function LocationEcho() {
  const location = useLocation();
  return (
    <output data-testid="location">
      {location.pathname}
      {location.search}
    </output>
  );
}

function renderShell(path = "/") {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/login" element={<LocationEcho />} />
        <Route element={<AppShell />}>
          <Route path="/cases" element={<LocationEcho />} />
          <Route path="*" element={<p>页面内容</p>} />
        </Route>
      </Routes>
    </MemoryRouter>,
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
    expect(screen.getByText("两江新区分公司")).toBeInTheDocument();
    expect(screen.getByText("页面内容")).toBeInTheDocument();
    expect(screen.getByText("跳到主要内容")).toBeInTheDocument();
  });

  it("shows an unknown identity while the session loads", () => {
    mocks.useSession.mockReturnValue({ data: undefined });
    renderShell();
    expect(screen.getByText("身份未知")).toBeInTheDocument();
    expect(screen.getByText("正在确认")).toBeInTheDocument();
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
    expect(within(nav).getByRole("link", { name: "案件" })).toHaveAttribute(
      "href",
      "/cases",
    );
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
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/cases?run=active",
      ),
    );
  });

  it("keeps the search box in sync with the URL", async () => {
    renderShell("/cases?q=基站");
    expect(screen.getByLabelText("全局搜索案件")).toHaveValue("基站");
  });

  it("marks the active navigation entry and review layout", () => {
    renderShell("/cases/case-1/reviews/run-1");
    expect(
      screen.getByRole("link", { name: "合同审查" }).closest("li"),
    ).toHaveClass("ant-menu-item-selected");
    expect(document.querySelector(".app")).toHaveClass("app--review");
  });

  it.each([
    ["/compare", "app--compare"],
    ["/api-docs", "app--api-docs"],
  ])("marks the %s layout class", (path, className) => {
    renderShell(path);
    expect(document.querySelector(".app")).toHaveClass(className);
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
    await user.click(screen.getByRole("button", { name: "展开侧栏" }));
    expect(screen.getByRole("button", { name: "收合侧栏" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });

  it("links rulepack governance to the admin base when configured", () => {
    vi.stubEnv("VITE_ADMIN_URL", "http://admin.example.test/");
    renderShell();
    expect(screen.getByRole("link", { name: "规则包治理" })).toHaveAttribute(
      "href",
      "http://admin.example.test/admin/rulepacks",
    );
    vi.unstubAllEnvs();
  });

  it("links rulepack governance locally by default", () => {
    renderShell();
    expect(screen.getByRole("link", { name: "规则包治理" })).toHaveAttribute(
      "href",
      "/admin/rulepacks",
    );
  });

  it("logs out to the login page", async () => {
    const user = userEvent.setup();
    renderShell("/cases");
    await user.click(screen.getByRole("button", { name: "退出" }));
    expect(mutateAsync).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent("/login"),
    );
  });

  it("disables logout while pending", () => {
    mocks.useLogout.mockReturnValue({ isPending: true, mutateAsync });
    renderShell();
    expect(screen.getByRole("button", { name: "退出" })).toBeDisabled();
  });
});
