// File metadata (manifest mirror):
// Purpose: Verifies subject list columns, page-scoped status filtering and the authorized new-subject drawer with its draft and identity boundaries.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, MemoryRouter, RouterProvider } from "react-router";
import { DirtyLeaveGuardProvider } from "../components/dirty-leave-guard";
import { describe, expect, it, vi } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import { SubjectsPage } from "./subjects-page";

const mocks = vi.hoisted(() => ({
  actor: {
    id: "actor",
    role: "admin",
    audience: "client",
    tenant_id: "tenant",
    org_id: "org",
    principal_id: "principal",
    active_context: { tenant_id: "tenant", org_id: "org", membership_id: "member" },
  },
  listeners: new Set<() => void>(),
  create: vi.fn(),
  calls: [] as Array<[string, string | null]>,
}));
vi.mock("../api/queries", async () => {
  const { useSyncExternalStore } = await import("react");
  return {
    useSession: () => ({
      data: useSyncExternalStore(
        (listener) => {
          mocks.listeners.add(listener);
          return () => {
            mocks.listeners.delete(listener);
          };
        },
        () => mocks.actor,
      ),
    }),
    useCreateSubject: () => ({ isPending: false, mutateAsync: mocks.create }),
    useSubjects: (query: string, cursor: string | null) => {
      mocks.calls.push([query, cursor]);
      const second = cursor === "next";
      return {
        isLoading: false,
        isFetching: false,
        isError: false,
        data: {
          items: [
            {
              subject_id: second ? "b" : "a",
              display_name: second ? "已核验主体" : "待核验主体",
              legal_name: null,
              aliases: [],
              identifiers: [],
              verification_state: second ? "verified" : "pending_verification",
              verification_scope: "ordinary",
              created_at: "2026-11-20T00:00:00Z",
            },
          ],
          next_cursor: second ? null : "next",
        },
      };
    },
  };
});

describe("subject list", () => {
  it("filters only this page and preserves server pagination", async () => {
    render(
      <MemoryRouter>
        <SubjectsPage />
      </MemoryRouter>,
    );
    const table = screen.getByRole("table");
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((cell) => cell.textContent),
    ).toEqual(["主体名称", "统一社会信用代码", "核验状态", "登记时间"]);
    expect(screen.queryByText("本页主体")).not.toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox", { name: "本页核验状态" }));
    await user.click(await screen.findByRole("option", { name: "本页已核验" }));
    expect(screen.getByText("本页没有符合条件的主体")).toBeVisible();
    expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "下一页" }));
    expect(await screen.findByRole("link", { name: "已核验主体" })).toBeVisible();
    expect(mocks.calls.some(([query, cursor]) => query === "" && cursor === "next")).toBe(true);
    expect(screen.getByRole("button", { name: "上一页" })).toBeEnabled();
  });
  it("keeps a closed drawer draft and guards leaving the page", async () => {
    mocks.create.mockClear();
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <DirtyLeaveGuardProvider>
            <SubjectsPage />
          </DirtyLeaveGuardProvider>
        ),
      },
      { path: "/subjects/:id", element: <p>详情页</p> },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "新建主体" }));
    const name = await screen.findByLabelText("主体名称");
    await user.type(name, "暂存草稿");
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "新建主体" }));
    expect(await screen.findByDisplayValue("暂存草稿")).toBeVisible();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("link", { name: "待核验主体" }));
    expect(await screen.findByText("主体草稿尚未登记。离开后输入将丢失。")).toBeVisible();
    expect(mocks.create).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "留在本页" }));
    mocks.actor = { ...mocks.actor, id: "other-actor" };
    act(() => {
      for (const listener of mocks.listeners) listener();
    });
    await user.click(screen.getByRole("button", { name: "新建主体" }));
    expect(await screen.findByLabelText("主体名称")).toHaveValue("");
  });
});
