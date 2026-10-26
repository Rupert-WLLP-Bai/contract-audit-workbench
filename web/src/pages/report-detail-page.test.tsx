import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { reportEnvelope } from "../test/report-fixture";
import { ReportDetailPage } from "./report-detail-page";

const clients: QueryClient[] = [];
const originalFetch = globalThis.fetch;

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function open(
  path = "/cases/case_1/reports/report_1",
  response: unknown | "forbidden" = reportEnvelope(),
) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (response === "forbidden")
      return json({ error: { message: "没有读取报告的权限" } }, 403);
    if (url.startsWith("/api/cases/case_1/reports/report_1?format=json"))
      return json(response);
    return json({ error: { message: "unexpected request" } }, 404);
  });
  globalThis.fetch = fetchMock as typeof fetch;
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  });
  clients.push(client);
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route
            path="/cases/:caseId/reports/:reportId"
            element={<ReportDetailPage />}
          />
          <Route
            path="/cases/:caseId/reviews/:runId"
            element={<div>审查工作区占位</div>}
          />
          <Route path="/cases/:caseId" element={<div>案件详情占位</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { fetchMock, user };
}

describe("ReportDetailPage", () => {
  it("renders the frozen report with only the exports the backend supports", async () => {
    open();
    await screen.findByRole("heading", { name: /政企专线服务合同/ });

    expect(screen.getByText("报告包含 1 项高风险发现")).toBeInTheDocument();
    expect(
      screen.getAllByText("预付款比例超过审批上限。").length,
    ).toBeGreaterThan(0);
    expect(
      screen.getAllByText("SLA 可用率低于采购依据。").length,
    ).toBeGreaterThan(0);

    const htmlLinks = screen.getAllByRole("link", { name: /导出 HTML/ });
    expect(htmlLinks.length).toBeGreaterThanOrEqual(1);
    expect(htmlLinks[0]).toHaveAttribute(
      "href",
      "/api/cases/case_1/reports/report_1?format=html",
    );
    expect(htmlLinks[0]).toHaveAttribute("target", "_blank");
    expect(
      screen.getAllByRole("link", { name: /导出 JSON/ })[0],
    ).toHaveAttribute(
      "href",
      "/api/cases/case_1/reports/report_1?format=json",
    );

    expect(screen.queryByRole("link", { name: /PDF/ })).not.toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: /Word/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /分享|发送给复核人/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/PDF、Word、分享链接或发送复核不在接口能力内/),
    ).toBeInTheDocument();
  });

  it("filters the finding list and opens the append-only decision history", async () => {
    const { user } = open();
    await screen.findByRole("heading", { name: /政企专线服务合同/ });
    await user.click(screen.getByRole("tab", { name: "问题清单" }));

    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "筛选风险等级" }),
      "高风险",
    );
    await waitFor(() =>
      expect(screen.getByText("当前显示 1 项")).toBeInTheDocument(),
    );
    expect(
      screen.getAllByText("预付款比例超过审批上限。").length,
    ).toBeGreaterThan(0);
    expect(
      screen.queryByText("SLA 可用率低于采购依据。"),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "查看详情" }));
    const drawer = await screen.findByRole("dialog");
    expect(
      within(drawer).getAllByText("预付款比例超过审批上限。").length,
    ).toBeGreaterThan(0);
    expect(within(drawer).getByText("人工处理记录")).toBeInTheDocument();
    expect(within(drawer).getByText("确认风险")).toBeInTheDocument();
    expect(
      within(drawer).getByText("采购材料上限为 30%，拟签合同为 40%。"),
    ).toBeInTheDocument();
    expect(
      within(drawer).getByText(/人工决定按时间追加保存/),
    ).toBeInTheDocument();

    await user.click(
      within(drawer).getByRole("button", { name: /在冻结原文中查看/ }),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("tab", { name: "原文依据" }),
      ).toHaveAttribute("aria-selected", "true"),
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("highlights only anchors that agree with the frozen text and supports reverse selection", async () => {
    const { user } = open(
      "/cases/case_1/reports/report_1?tab=sources&finding=finding_high",
    );
    await screen.findByRole("heading", { name: /政企专线服务合同/ });

    const mark = await screen.findByRole("button", {
      name: "查看发现：预付款比例超过审批上限。",
    });
    expect(mark.tagName).toBe("MARK");
    expect(mark).toHaveAttribute("data-finding-id", "finding_high");
    expect(mark).toHaveAttribute("data-severity", "high");
    expect(mark).toHaveTextContent("预付款比例 40%");
    expect(screen.getByText("1 / 1 条引用已定位")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /采购文件/ }));
    expect(
      await screen.findByText("1 / 2 条引用已定位"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("引用原文与冻结正文不一致", { exact: false }),
    ).toBeInTheDocument();

    await user.click(
      screen.getByRole("button", {
        name: "查看发现：预付款比例超过审批上限。",
      }),
    );
    const drawer = await screen.findByRole("dialog");
    expect(
      within(drawer).getByText("问题描述"),
    ).toBeInTheDocument();
    expect(
      within(drawer).getAllByText("预付款比例超过审批上限。").length,
    ).toBeGreaterThan(0);
  });

  it("shows the server denial without rendering cached report data", async () => {
    open("/cases/case_1/reports/report_1", "forbidden");
    expect(
      await screen.findByText("没有权限读取这个报告"),
    ).toBeInTheDocument();
    expect(screen.queryByText("政企专线服务合同")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: /导出 HTML/ }),
    ).not.toBeInTheDocument();
  });
});
