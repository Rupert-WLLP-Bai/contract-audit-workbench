// File metadata (manifest mirror):
// Purpose: Checks direct and modal intake behavior, successful and failed file submissions, cancellation and guarded draft navigation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-11-20
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import type { Actor } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { DirtyLeaveGuard, DirtyLeaveGuardProvider } from "../components/dirty-leave-guard";
import { ReviewIntakeDialog } from "../features/intake/review-intake-dialog";
import { ReviewEntryPage } from "./review-entry-page";

const actor: Actor = {
  id: "clerk-1",
  display_name: "周登记",
  role: "clerk",
  tenant_id: "tenant-1",
  org_id: "org-1",
  audience: "client",
  principal_id: "principal-1",
  active_context: {
    membership_id: "membership-1",
    context_version: "1",
    tenant_id: "tenant-1",
    tenant_name: "租户甲",
    org_id: "org-1",
    org_name: "组织甲",
  },
  csrf_token: "test-token",
  context_switch_enabled: false,
};

vi.mock("../api/queries", () => ({
  useSession: () => ({ isSuccess: true, data: actor }),
}));

function response(value: unknown, status = 201) {
  return Response.json(value, { status });
}

function open({ modal = false, procurementFails = false } = {}) {
  const onClose = vi.fn();
  const writes: { path: string; key: string | null; form: FormData }[] = [];
  let fail = procurementFails;
  setSessionIdentity(actor);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://test").pathname;
      if (init?.method === "POST" && init.body instanceof FormData) {
        writes.push({
          path,
          key: new Headers(init.headers).get("Idempotency-Key"),
          form: init.body,
        });
      }
      if (path === "/api/intakes")
        return response({
          operation_id: "intake-new",
          case_id: "case-new",
          document_id: "doc-main",
          material_id: "mat-main",
          material_version: 1,
          state: "completed",
          retryable: false,
        });
      if (path === "/api/cases/case-new/documents") {
        if (fail) {
          fail = false;
          return response({ error: { code: "unavailable", message: "暂时无法接收采购文件" } }, 503);
        }
        return response({ id: "doc-proc", material_id: "mat-proc", version: 1, replayed: false });
      }
      throw new Error(`Unexpected request ${path}`);
    }),
  );
  const router = createMemoryRouter(
    [
      {
        path: "/review",
        element: modal ? <ReviewIntakeDialog open onClose={onClose} /> : <ReviewEntryPage />,
      },
      { path: "/cases", element: <h1>案件</h1> },
      { path: "/cases/:id", element: <h1>新建案件准备</h1> },
    ],
    { initialEntries: ["/review"] },
  );
  render(<RouterProvider router={router} />);
  return { router, writes, onClose, user: userEvent.setup() };
}

function sample(name: string) {
  return new File(["工程验收夹具"], name, { type: "text/plain" });
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  sessionStorage.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

it("reuses the contract intake for direct review, with drag upload and one create action", async () => {
  const { router, user, writes } = open();
  expect(screen.getByRole("heading", { name: "审一份合同" })).toBeVisible();
  const choose = await screen.findByRole("button", { name: "拖入拟签合同，或选择文件" });
  fireEvent.drop(choose.parentElement!, { dataTransfer: { files: [sample("拟签合同.txt")] } });
  expect(screen.getByRole("textbox", { name: "案件名称" })).toHaveValue("拟签合同");
  expect(screen.getByLabelText("业务场景")).toBeVisible();
  expect(screen.getByLabelText("合同类型")).toBeVisible();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "开始建档" }));
  expect(await screen.findByRole("heading", { name: "新建案件准备" })).toBeVisible();
  expect(router.state.location.pathname).toBe("/cases/case-new");
  expect(writes.map((entry) => entry.path)).toEqual(["/api/intakes"]);
  expect(writes[0]?.key).toBeTruthy();
  expect(writes[0]?.form.get("title")).toBe("拟签合同");
});

it("keeps the established case and retries optional procurement with the same idempotency key", async () => {
  const { onClose, user, writes } = open({ modal: true, procurementFails: true });
  const dialog = await screen.findByRole("dialog", { name: "审一份合同" });
  await user.upload(await within(dialog).findByLabelText("选择主合同文件"), sample("主合同.txt"));
  await user.upload(within(dialog).getByLabelText("选择采购文件"), sample("采购要求.txt"));
  await user.click(within(dialog).getByRole("button", { name: "开始建档" }));
  expect(await within(dialog).findByText("采购文件未能上传")).toBeInTheDocument();
  expect(onClose).not.toHaveBeenCalled();
  await user.click(within(dialog).getByRole("button", { name: "重试采购文件" }));
  expect(await screen.findByRole("heading", { name: "新建案件准备" })).toBeVisible();
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  expect(writes.map((entry) => entry.path)).toEqual([
    "/api/intakes",
    "/api/cases/case-new/documents",
    "/api/cases/case-new/documents",
  ]);
  expect(writes[1]?.form.get("role")).toBe("procurement");
  expect(writes[1]?.form.get("intent")).toBe("add");
  expect(writes[1]?.key).toBeTruthy();
  expect(writes[1]?.key).toBe(writes[2]?.key);
  expect(writes[1]?.key).not.toBe(writes[0]?.key);
});

it("confirms before discarding a populated route form and does not start intake", async () => {
  const { user, writes } = open();
  await user.upload(await screen.findByLabelText("选择主合同文件"), sample("准备中的合同.txt"));
  await user.click(screen.getByRole("button", { name: "取消" }));
  const confirmation = await screen.findByRole("dialog");
  expect(confirmation).toHaveTextContent("放弃未提交的材料");
  await user.click(within(confirmation).getByRole("button", { name: "放弃并返回" }));
  expect(await screen.findByRole("heading", { name: "案件" })).toBeVisible();
  expect(writes).toEqual([]);
});

it.each([false, true])(
  "keeps drafts on Back until departure is confirmed (case draft: %s)",
  async (caseDirty) => {
    const onClose = vi.fn();
    setSessionIdentity(actor);
    const router = createMemoryRouter(
      [
        { path: "/cases", element: <h1>案件</h1> },
        {
          path: "/cases/contract",
          element: (
            <DirtyLeaveGuardProvider>
              <h1>现有案件</h1>
              <DirtyLeaveGuard dirty={caseDirty} />
              <ReviewIntakeDialog open onClose={onClose} />
            </DirtyLeaveGuardProvider>
          ),
        },
      ],
      { initialEntries: ["/cases", "/cases/contract"] },
    );
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();
    const intake = await screen.findByRole("dialog", { name: "审一份合同" });
    if (!caseDirty) {
      await user.upload(
        await within(intake).findByLabelText("选择主合同文件"),
        sample("待提交合同.txt"),
      );
    }
    await act(async () => router.navigate(-1));
    const stay = await screen.findByRole("button", { name: "留在本页" });
    expect(stay.closest('[role="dialog"]')).toHaveTextContent(
      caseDirty ? "当前页面有未提交的草稿" : "尚未提交的文件",
    );
    await user.click(stay);
    expect(router.state.location.pathname).toBe("/cases/contract");
    expect(onClose).not.toHaveBeenCalled();
    if (!caseDirty)
      expect(within(intake).getByRole("textbox", { name: "案件名称" })).toHaveValue("待提交合同");

    await act(async () => router.navigate(-1));
    await user.click(await screen.findByRole("button", { name: "离开" }));
    expect(await screen.findByRole("heading", { name: "案件" })).toBeVisible();
    expect(onClose).toHaveBeenCalledTimes(caseDirty ? 0 : 1);
  },
);
