// File metadata (manifest mirror):
// Purpose: Checks pending organization membership selection: server-provided choices, submit success and expiry, load failure retry and audience-specific login links.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import {
  listPendingMemberships,
  selectPendingMembership,
  type MembershipChoice,
} from "../api/pending-memberships";
import { renderWithAntd as render } from "../test/antd";
import { PendingMembershipPage } from "./pending-membership-page";

vi.mock("../api/pending-memberships", () => ({
  listPendingMemberships: vi.fn(),
  selectPendingMembership: vi.fn(),
}));

const memberships: MembershipChoice[] = [
  {
    id: "membership-1",
    tenant_id: "tenant-1",
    tenant_name: "本地租户",
    org_id: "org-1",
    org_name: "合同部",
    role: "clerk",
  },
  {
    id: "membership-2",
    tenant_id: "tenant-1",
    tenant_name: "本地租户",
    org_id: "org-2",
    org_name: "采购部",
    role: "reviewer",
  },
];

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

it("submits the selected server-provided organization identity", async () => {
  const user = userEvent.setup();
  vi.mocked(listPendingMemberships).mockResolvedValue({ memberships });
  vi.mocked(selectPendingMembership).mockResolvedValue(null);
  const replace = vi.fn();
  vi.stubGlobal("location", { ...window.location, replace });
  render(<PendingMembershipPage audience="client" />);
  expect(screen.getByRole("status")).toHaveTextContent("正在读取可用组织身份");
  expect(await screen.findByRole("radio", { name: /合同部/ })).toBeVisible();
  const submit = screen.getByRole("button", { name: "进入工作台" });
  expect(submit).toBeDisabled();
  await user.click(screen.getByRole("radio", { name: /采购部/ }));
  expect(submit).toBeEnabled();
  await user.click(submit);
  await waitFor(() =>
    expect(selectPendingMembership).toHaveBeenCalledWith("client", "membership-2"),
  );
  expect(replace).toHaveBeenCalledWith("/");
  expect(screen.getByRole("link", { name: "返回登录页面" })).toHaveAttribute("href", "/login");
});

it("reports an expired challenge and links back to the audience login", async () => {
  vi.mocked(listPendingMemberships).mockResolvedValue({ memberships: [] });
  render(<PendingMembershipPage audience="admin" />);
  expect(await screen.findByText("本次组织登录已过期或没有可用身份，请重新登录。")).toBeVisible();
  expect(screen.getByRole("link", { name: "重新登录" })).toHaveAttribute(
    "href",
    "/api/auth/oidc/login?audience=admin",
  );
  expect(screen.getByRole("link", { name: "返回登录页面" })).toHaveAttribute(
    "href",
    "/admin/login",
  );
  expect(screen.queryByRole("radio")).not.toBeInTheDocument();
});

it("retries a failed identity read without exposing transport details", async () => {
  const user = userEvent.setup();
  vi.mocked(listPendingMemberships)
    .mockRejectedValueOnce(new ApiError("network down", 0, "network", true, null))
    .mockResolvedValueOnce({ memberships });
  render(<PendingMembershipPage audience="client" />);
  expect(await screen.findByText("暂时无法连接身份服务，请检查网络后重试。")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "重试" }));
  expect(await screen.findByRole("radio", { name: /合同部/ })).toBeVisible();
  expect(listPendingMemberships).toHaveBeenCalledTimes(2);
});

it("keeps the page recoverable when the server rejects the selection", async () => {
  const user = userEvent.setup();
  vi.mocked(listPendingMemberships)
    .mockResolvedValueOnce({ memberships })
    .mockResolvedValueOnce({ memberships: [] });
  vi.mocked(selectPendingMembership).mockRejectedValue(
    new ApiError("conflict", 409, "conflict", false, null),
  );
  render(<PendingMembershipPage audience="client" />);
  await user.click(await screen.findByRole("radio", { name: /合同部/ }));
  await user.click(screen.getByRole("button", { name: "进入工作台" }));
  expect(await screen.findByText("身份服务暂时无法完成请求，请稍后重试。")).toBeVisible();
  expect(screen.queryByRole("radio")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "重新读取身份" }));
  expect(await screen.findByText("本次组织登录已过期或没有可用身份，请重新登录。")).toBeVisible();
});

it("treats a forbidden selection as an expired challenge", async () => {
  const user = userEvent.setup();
  vi.mocked(listPendingMemberships).mockResolvedValue({ memberships });
  vi.mocked(selectPendingMembership).mockRejectedValue(
    new ApiError("forbidden", 403, "forbidden", false, null),
  );
  render(<PendingMembershipPage audience="client" />);
  await user.click(await screen.findByRole("radio", { name: /合同部/ }));
  await user.click(screen.getByRole("button", { name: "进入工作台" }));
  expect(await screen.findByText("本次组织登录已过期或没有可用身份，请重新登录。")).toBeVisible();
  expect(screen.queryByText("没有完成身份选择")).not.toBeInTheDocument();
});
