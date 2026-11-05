// File metadata (manifest mirror):
// Purpose: Verifies filtering the current authorized loaded case collection before pagination and retaining case-ID links after an editable business-number change.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { expect, it, vi } from "vitest";
import type { CaseSummary } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { CasesPage } from "./cases-page";

const source = vi.hoisted(() => ({ items: [] as CaseSummary[] }));
vi.mock("../api/queries", () => ({
  useCases: () => ({ data: source.items, isLoading: false, isError: false }),
  useSession: () => ({ data: { role: "viewer" } }),
  useCreateCase: () => ({ mutate: vi.fn(), isPending: false }),
}));

it("filters the authorized loaded set before paging and keeps links on case ID after a number edit", async () => {
  source.items = Array.from({ length: 13 }, (_, index) => ({
    id: `case-${index}`, title: "同名案件", case_number: `BUS-${index}`,
    contract_type: index % 2 ? "framework" : "service", scenario: "telecom_dedicated_line", business_line: "ENT",
    document_count: 0, pending_decisions: 0, latest_run: null, updated_at: `2026-11-05T01:02:${String(index).padStart(2, "0")}Z`,
  }));
  source.items.push({ ...source.items[0]!, id: "case-other", title: "其他案件", case_number: "OTHER", scenario: "cloud_services" });
  const view = render(<MemoryRouter initialEntries={["/cases"]}><CasesPage /></MemoryRouter>);
  const user = userEvent.setup();
  expect(screen.getByRole("button", { name: "新建案件" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "新建案件" })).toHaveAttribute(
    "title",
    "当前角色仅可查看案件；新建案件需要管理员或经办人角色。",
  );
  expect(screen.getByText(/当前角色仅可查看案件/)).toBeVisible();
  const search = screen.getByRole("searchbox", { name: "搜索案件" });
  await user.type(search, "政企专线");
  expect(screen.getByText("显示 1–10 条，共 13 条")).toBeInTheDocument();
  await user.click(screen.getByTitle("2"));
  expect(screen.getByText("显示 11–13 条，共 13 条")).toBeInTheDocument();
  expect(within(screen.getByRole("table")).getAllByRole("link", { name: "同名案件" })).toHaveLength(3);
  await user.clear(search);
  await user.type(search, "BUS-0");
  const originalLink = screen.getByRole("link", { name: "同名案件" });
  expect(originalLink).toHaveAttribute("href", "/cases/case-0");
  source.items[0] = { ...source.items[0]!, case_number: "BUS-NEW" };
  view.rerender(<MemoryRouter initialEntries={["/cases?q=BUS-NEW"]}><CasesPage /></MemoryRouter>);
  await user.clear(screen.getByRole("searchbox", { name: "搜索案件" }));
  await user.type(screen.getByRole("searchbox", { name: "搜索案件" }), "BUS-NEW");
  expect(screen.getByRole("link", { name: "同名案件" })).toHaveAttribute("href", "/cases/case-0");
});
