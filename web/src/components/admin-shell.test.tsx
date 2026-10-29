import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
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
}));

const admin: Actor = {
  id: "actor-1",
  display_name: "管理员甲",
  role: "admin",
  tenant_id: "tenant-1",
  org_id: "demo-org",
  audience: "admin",
};

function LocationEcho() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

function renderShell(path = "/admin/settings") {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/admin/login" element={<LocationEcho />} />
        <Route path="/admin" element={<AdminShell />}>
          <Route path="*" element={<p>管理内容</p>} />
        </Route>
      </Routes>
    </MemoryRouter>,
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
      screen.getByText(
        "当前管理会话没有管理角色。案件访问权限由业务工作台单独授权。",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("管理导航")).not.toBeInTheDocument();
    expect(screen.queryByText("管理内容")).not.toBeInTheDocument();
  });

  it("shows the administration identity, organization and read-only disclaimer", () => {
    renderShell();
    expect(screen.getByLabelText("管理工作台")).toHaveAttribute(
      "href",
      "/admin/settings",
    );
    expect(screen.getByText("合同智能审计工作台")).toBeInTheDocument();
    expect(screen.getByText("两江新区分公司")).toBeInTheDocument();
    expect(
      screen.getByText("此工作区不授予案件正文访问权限。"),
    ).toBeInTheDocument();
    const who = screen.getByLabelText("当前管理身份");
    expect(within(who).getByText("管理员甲")).toBeInTheDocument();
    expect(within(who).getByText("管理员")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "规则库" }),
    ).toHaveAttribute("href", "/admin/rulepacks");
    expect(
      screen.getByRole("link", { name: "知识治理" }),
    ).toHaveAttribute("href", "/admin/governed-knowledge");
    expect(
      screen.getByRole("link", { name: "基础设施与中间件" }),
    ).toHaveAttribute("href", "/admin/infrastructure");
    expect(screen.getByText("管理内容")).toBeInTheDocument();
    expect(screen.getByText(/按当前管理身份授权/)).toBeInTheDocument();
  });

  it("shows an unknown identity while the session is still loading", () => {
    session = { data: undefined };
    renderShell();
    expect(screen.getByText("身份未知")).toBeInTheDocument();
    expect(screen.getByText("正在确认")).toBeInTheDocument();
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
    expect(screen.getByRole("link", { name: label })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("navigates to the admin login after logging out", async () => {
    const user = userEvent.setup();
    renderShell();
    await user.click(screen.getByRole("button", { name: "退出" }));
    expect(mutateAsync).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent("/admin/login"),
    );
  });

  it("disables the logout action while it is pending", () => {
    logoutPending = true;
    renderShell();
    expect(screen.getByRole("button", { name: "退出" })).toBeDisabled();
  });
});
