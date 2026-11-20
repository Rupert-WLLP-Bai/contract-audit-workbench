// File metadata (manifest mirror):
// Purpose: Verifies URL-backed case views, filtering the current authorized loaded collection before pagination, and retaining case-ID links after an editable business-number change.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-20
// Revision: r5
// Copyright: See LICENSE
// End file metadata.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { MemoryRouter } from "react-router";
import { expect, it, vi } from "vitest";
import type { CaseSummary } from "../api/types";
import { chooseOption, renderWithAntd } from "../test/antd";
import { CasesPage } from "./cases-page";

const source = vi.hoisted(() => ({
  items: [] as CaseSummary[],
  role: "viewer",
  profiles: [] as { case_id: string; owner_id: string }[],
  tasks: [] as { case_id: string; assignee_id: string; status: string }[],
  supplements: new Map<string, { case_id: string; assignee_id: string; status: string }[]>(),
}));
vi.mock("../api/evidence-workflow", () => ({
  evidenceRequestsKey: (id: string) => ["evidence", id],
  listEvidenceRequests: async (id: string) => source.supplements.get(id) ?? [],
}));

function render(element: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithAntd(element, {
    wrapper: ({ children }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  });
}
vi.mock("../api/queries", () => ({
  useCases: () => ({
    data: source.items,
    isSuccess: true,
    fetchStatus: "idle",
    isLoading: false,
    isError: false,
  }),
  useSession: () => ({
    isSuccess: true,
    fetchStatus: "idle",
    data: { id: "actor", role: source.role, display_name: "审查员" },
  }),
  useWorkbench: () => ({
    isSuccess: true,
    data: {
      cases: source.items,
      tasks: source.tasks,
      profiles: source.profiles,
      runs: [],
      reports: [],
      documents: [],
      remediations: [],
    },
  }),
  useCreateCase: () => ({ mutate: vi.fn(), isPending: false }),
}));

it("filters the authorized loaded set before paging and keeps links on case ID after a number edit", async () => {
  source.role = "viewer";
  source.items = Array.from({ length: 13 }, (_, index) => ({
    id: `case-${index}`,
    title: "同名案件",
    case_number: `BUS-${index}`,
    contract_type: index % 2 ? "framework" : "service",
    scenario: "telecom_dedicated_line",
    business_line: "ENT",
    document_count: 0,
    pending_decisions: 0,
    latest_run: null,
    updated_at: `2026-11-05T01:02:${String(index).padStart(2, "0")}Z`,
  }));
  source.items.push({
    ...source.items[0]!,
    id: "case-other",
    title: "其他案件",
    case_number: "OTHER",
    scenario: "cloud_services",
  });
  const view = render(
    <MemoryRouter initialEntries={["/cases"]}>
      <CasesPage />
    </MemoryRouter>,
  );
  const user = userEvent.setup();
  expect(screen.getByRole("button", { name: "新建空白案件" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "新建空白案件" })).toHaveAttribute(
    "title",
    "当前角色仅可查看案件；新建案件需要管理员或经办人角色。",
  );
  const search = screen.getByRole("searchbox", { name: "搜索案件" });
  await user.type(search, "政企专线");
  expect(screen.getByText("显示 1–10 条，共 13 条")).toBeInTheDocument();
  await user.click(screen.getByTitle("2"));
  expect(screen.getByText("显示 11–13 条，共 13 条")).toBeInTheDocument();
  expect(
    within(screen.getByRole("table")).getAllByRole("link", { name: "查看材料：同名案件" }),
  ).toHaveLength(3);
  await user.clear(search);
  await user.type(search, "BUS-0");
  const originalLink = screen.getByRole("link", { name: "查看材料：同名案件" });
  expect(originalLink).toHaveAttribute("href", "/cases/case-0#case-materials");
  source.items[0] = { ...source.items[0]!, case_number: "BUS-NEW" };
  view.rerender(
    <MemoryRouter initialEntries={["/cases?q=BUS-NEW"]}>
      <CasesPage />
    </MemoryRouter>,
  );
  await user.clear(screen.getByRole("searchbox", { name: "搜索案件" }));
  await user.type(screen.getByRole("searchbox", { name: "搜索案件" }), "BUS-NEW");
  expect(screen.getByRole("link", { name: "查看材料：同名案件" })).toHaveAttribute(
    "href",
    "/cases/case-0#case-materials",
  );
});

it("names the create dialog once from the modal title", async () => {
  source.role = "admin";
  source.items = [
    {
      id: "case-1",
      title: "已有案件",
      case_number: "BUS-1",
      contract_type: "service",
      scenario: "telecom_dedicated_line",
      business_line: "ENT",
      document_count: 0,
      pending_decisions: 0,
      latest_run: null,
      updated_at: "2026-11-08T01:02:00Z",
    },
  ];
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={["/cases"]}>
      <CasesPage />
    </MemoryRouter>,
  );
  await user.click(screen.getByRole("button", { name: "新建空白案件" }));
  const dialog = screen.getByRole("dialog");
  expect(dialog.querySelector(".ant-modal-title")).toHaveTextContent("新建空白案件");
  expect(dialog.textContent?.match(/新建空白案件/g)).toEqual(["新建空白案件"]);
  expect(within(dialog).queryByRole("heading", { name: "新建空白案件" })).not.toBeInTheDocument();
  expect(within(dialog).getByLabelText("案件名称")).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "自定义规则范围编码" })).toBeInTheDocument();
  expect(within(dialog).getByRole("combobox", { name: "业务场景" })).toBeInTheDocument();
  expect(within(dialog).getByRole("combobox", { name: "合同类型" })).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "取消" })).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "创建" })).toBeInTheDocument();
});

it("filters the visible case set by assigned owner and archived state", async () => {
  source.role = "viewer";
  source.items = [
    {
      id: "mine-open",
      title: "我负责的案件",
      case_number: "BUS-MINE",
      contract_type: "service",
      scenario: "telecom_dedicated_line",
      business_line: "ENT",
      owner_name: "审查员",
      archived_at: null,
      document_count: 0,
      pending_decisions: 0,
      latest_run: null,
      updated_at: "2026-11-08T01:02:00Z",
    },
    {
      id: "other-open",
      title: "他人负责的案件",
      case_number: "BUS-OTHER",
      contract_type: "service",
      scenario: "telecom_dedicated_line",
      business_line: "ENT",
      owner_name: "其他人",
      archived_at: null,
      document_count: 0,
      pending_decisions: 0,
      latest_run: null,
      updated_at: "2026-11-08T01:03:00Z",
    },
    {
      id: "mine-archived",
      title: "已归档案件",
      case_number: "BUS-ARCHIVED",
      contract_type: "service",
      scenario: "telecom_dedicated_line",
      business_line: "ENT",
      owner_name: "其他人",
      archived_at: "2026-11-07T01:02:00Z",
      document_count: 0,
      pending_decisions: 0,
      latest_run: null,
      updated_at: "2026-11-07T01:02:00Z",
    },
  ];
  source.items[1]!.owner_name = "审查员";
  source.items[1]!.pending_decisions = 5;
  source.items.push(
    { ...source.items[0]!, id: "task", title: "我的分派任务" },
    { ...source.items[0]!, id: "supplement", title: "我的待答复补件" },
    { ...source.items[0]!, id: "closed-supplement", title: "已答复的补件" },
  );
  source.profiles = [{ case_id: "mine-open", owner_id: "actor" }];
  source.tasks = [{ case_id: "task", assignee_id: "actor", status: "in_progress" }];
  source.supplements = new Map([
    ["supplement", [{ case_id: "supplement", assignee_id: "actor", status: "open" }]],
    [
      "closed-supplement",
      [{ case_id: "closed-supplement", assignee_id: "actor", status: "responded" }],
    ],
  ]);
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={["/cases"]}>
      <CasesPage />
    </MemoryRouter>,
  );

  await user.click(screen.getByRole("tab", { name: "待我处理" }));
  expect(await screen.findByRole("link", { name: /我的待答复补件/ })).toBeInTheDocument();
  const assignedTable = within(screen.getByRole("table"));
  expect(assignedTable.getByRole("link", { name: /：我负责的案件$/ })).toBeInTheDocument();
  expect(assignedTable.queryByRole("link", { name: /：他人负责的案件$/ })).not.toBeInTheDocument();
  expect(assignedTable.queryByRole("link", { name: /：已归档案件$/ })).not.toBeInTheDocument();

  expect(assignedTable.getByRole("link", { name: /我的分派任务/ })).toBeInTheDocument();
  expect(assignedTable.queryByRole("link", { name: /已答复的补件/ })).not.toBeInTheDocument();

  await user.click(screen.getByRole("tab", { name: "已归档" }));
  const archivedTable = within(screen.getByRole("table"));
  expect(archivedTable.getByRole("link", { name: /：已归档案件$/ })).toBeInTheDocument();
  expect(archivedTable.queryByRole("link", { name: /：我负责的案件$/ })).not.toBeInTheDocument();
  expect(archivedTable.queryByRole("link", { name: /：他人负责的案件$/ })).not.toBeInTheDocument();
});

it("orders recent reviews by the dated run and keeps filters behind the popover", async () => {
  source.items = [
    {
      id: "last-edited",
      title: "较晚编辑",
      contract_type: "framework",
      document_count: 0,
      pending_decisions: 0,
      updated_at: "2026-11-18T01:03:00Z",
      latest_run: { id: "older", state: "partial", updated_at: "2026-11-01T01:00:00Z" },
    },
    {
      id: "last-reviewed",
      title: "最近审查的案件",
      contract_type: "service",
      document_count: 0,
      pending_decisions: 0,
      updated_at: "2026-11-15T01:03:00Z",
      latest_run: { id: "newer", state: "completed", updated_at: "2026-11-13T01:00:00Z" },
    },
    {
      id: "unreviewed",
      title: "未运行案件",
      contract_type: "service",
      document_count: 0,
      pending_decisions: 0,
      updated_at: "2026-11-19T01:03:00Z",
      latest_run: null,
    },
  ];
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={["/cases?sort=title&direction=asc"]}>
      <CasesPage />
    </MemoryRouter>,
  );
  await user.click(screen.getByRole("tab", { name: "最近审查" }));
  const table = within(screen.getByRole("table"));
  expect(
    table.getAllByRole("link").map((link) => link.getAttribute("aria-label")?.split("：")[1]),
  ).toEqual(["最近审查的案件", "较晚编辑"]);
  expect(screen.queryByRole("combobox", { name: "合同类型" })).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "状态与类型" }));
  await chooseOption(user, screen.getByRole("combobox", { name: "合同类型" }), "服务合同");
  expect(table.getAllByRole("link")).toHaveLength(1);
  expect(table.getByRole("link", { name: /最近审查的案件/ })).toBeVisible();
});
