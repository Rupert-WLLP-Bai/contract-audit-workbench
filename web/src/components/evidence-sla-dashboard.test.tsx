// File metadata (manifest mirror):
// Purpose: Tests evidence SLA dashboard request selection, source links, and access-denied handling across cases.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-08
// Updated: 2026-11-08
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listEvidenceHistory, listEvidenceRequests, type EvidenceRequest } from "../api/evidence-workflow";
import { renderWithAntd as render } from "../test/antd";
import { EvidenceSLADashboard } from "./evidence-sla-dashboard";

vi.mock("../api/evidence-workflow", () => ({
  listEvidenceRequests: vi.fn(async (caseId: string) => [request(caseId)]),
  listEvidenceHistory: vi.fn(async () => []),
}));

function request(caseId: string): EvidenceRequest {
  return {
    id: "same", case_id: caseId, task_id: "task", task_version: 1, assignee_id: `owner-${caseId}`,
    due_at: "", source_run_id: `run-${caseId}`, source_attempt_id: `attempt-${caseId}`,
    source_evaluation: null, source_gap: null, question: `Question ${caseId}`, material_role: null,
    verification_condition: "check", status: "open", version: 1, created_by: "creator",
    created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", verification: null,
  };
}

afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("EvidenceSLADashboard", () => {
  it("selects the exact case and request and retains its source link", async () => {
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<QueryClientProvider client={client}><MemoryRouter><EvidenceSLADashboard cases={[{ id: "a", title: "Case A" }, { id: "b", title: "Case B" }]} /></MemoryRouter></QueryClientProvider>);
    const first = await screen.findByRole("button", { name: "查看请求详情：Case A，a，same" });
    await user.click(first);
    let detail = screen.getByRole("complementary", { name: "请求详情" });
    expect(within(detail).getByText("owner-a")).toBeInTheDocument();
    expect(within(detail).getByRole("link", { name: "查看原始审查" }).getAttribute("href")).toContain("run-a");
    expect(within(detail).getByRole("link", { name: "查看原始审查" }).getAttribute("href")).toContain("attempt-a");
    await user.click(screen.getByRole("button", { name: "查看请求详情：Case B，b，same" }));
    detail = screen.getByRole("complementary", { name: "请求详情" });
    expect(within(detail).getByText("owner-b")).toBeInTheDocument();
    expect(within(detail).queryByText("owner-a")).not.toBeInTheDocument();
    expect(first).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByText("未完成请求").nextElementSibling).toHaveTextContent("2");
    view.rerender(<QueryClientProvider client={client}><MemoryRouter><EvidenceSLADashboard cases={[{ id: "a", title: "Case A" }]} /></MemoryRouter></QueryClientProvider>);
    expect(within(screen.getByRole("complementary", { name: "请求详情" })).queryByText("owner-b")).not.toBeInTheDocument();
    client.clear();
  });

  it("shows server history and hides stale detail if history access fails", async () => {
    const user = userEvent.setup();
    const item = request("a");
    vi.mocked(listEvidenceHistory).mockResolvedValueOnce([{ type: "receipt_recorded", actor_id: "actor", occurred_at: item.created_at, request: item, receipt: { id: "receipt", request_id: "same", case_id: "a", document_id: "doc-real", answer: null, submitted_by: "actor", created_at: item.created_at } }]);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><MemoryRouter><EvidenceSLADashboard cases={[{ id: "a", title: "Case A" }]} /></MemoryRouter></QueryClientProvider>);
    await user.click(await screen.findByRole("button", { name: "查看请求详情：Case A，a，same" }));
    expect(await screen.findByText("材料 ID：doc-real")).toBeInTheDocument();
    vi.mocked(listEvidenceHistory).mockRejectedValueOnce(new Error("revoked"));
    await client.refetchQueries({ queryKey: ["evidence-requests", "a", "same", "history"] });
    expect(await screen.findByRole("alert")).toHaveTextContent("请求历史不可用");
    expect(screen.queryByText("材料 ID：doc-real")).not.toBeInTheDocument();
    client.clear();
  });

  it("keeps the empty and request error states", async () => {
    vi.mocked(listEvidenceRequests).mockResolvedValueOnce([]);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<QueryClientProvider client={client}><MemoryRouter><EvidenceSLADashboard cases={[{ id: "a", title: "Case A" }]} /></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByText("当前授权案件范围内没有补充材料请求。")).toBeInTheDocument();
    vi.mocked(listEvidenceRequests).mockRejectedValueOnce(new Error("revoked"));
    await client.refetchQueries({ queryKey: ["evidence-requests", "a"] });
    expect(await screen.findByText("补充材料请求无法完整读取")).toBeInTheDocument();
    view.unmount();
    client.clear();
  });
});
