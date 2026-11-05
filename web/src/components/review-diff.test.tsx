// File metadata (manifest mirror):
// Purpose: Verifies frozen review comparison presents Chinese business scope and check labels while keeping exact per-side technical identities and source evidence references available for inspection.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-05
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { parseReviewWorkspace } from "../api/parse";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { ReviewDiff } from "./review-diff";

const api = vi.hoisted(() => ({ getReview: vi.fn(), getReviewHistoryPage: vi.fn() }));
vi.mock("../api/endpoints", () => api);

function workspace(runId: string) {
  const attempt = { id: `${runId}-attempt`, run_id: runId, ordinal: 1, state: "completed", history_kind: "frozen",
    finished_at: "2026-11-01T00:00:00Z", parsed_document_ids: ["parse-1"], frozen_result_available: true,
    input_hash: "input-secret", output_hash: "output-secret" };
  const result = parseReviewWorkspace({
    run: { id: runId, case_id: "case-1", state: "completed" },
    review_attempts: { attempt, attempts: [attempt] },
    snapshot: { document_ids: ["doc-1"], rule_set_hash: "rule-hash", review_scope: { id: "full_review", rule_set_id: "rules-secret", rule_set_version: "1", rule_set_hash: "rule-hash" } },
    documents: [{ id: "doc-1", role: "draft_contract", filename: "甲方有限公司合同.txt", parse_status: "ready", parsed_document_id: "parse-1", text: "原文：付款比例 30%。", clauses: [] }],
    evaluations: [{ id: `${runId}-evaluation`, domain: "technical", rule_id: "rule-secret", rule_version: "1", capability_id: "dedicated_line.minimum_bandwidth", relation: "insufficient", reason: "需要核对原文" }],
    findings: [], audit_events: [], execution: null,
  });
  result.agent_review = { attempt_id: attempt.id, status: "partial", rulepack: null,
    scope: [{ domain: "technical", capability_ids: ["dedicated_line.minimum_bandwidth", "unknown·"], source_ids: ["evidence-secret"] }],
    domains: null, evidence: null, observations: null, unresolved_gap_ids: [], gaps: [] };
  return result;
}

it("shows frozen scope and business names with exact identities behind technical details", async () => {
  const source = workspace("run-a");
  const target = workspace("run-b");
  api.getReviewHistoryPage.mockImplementation(async (_caseId: string, runId: string) => runId === "run-b"
    ? { entry: { runId, workspace: target }, nextRunId: "run-a", cycleDetected: false }
    : { entry: { runId, workspace: source }, nextRunId: null, cycleDetected: false });
  api.getReview.mockImplementation(async (_caseId: string, runId: string) => runId === "run-a" ? source : target);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter><ReviewDiff caseId="case-1" latestRunId="run-b" actorScope="test" /></MemoryRouter></QueryClientProvider>);
  const user = userEvent.setup();
  await user.click(screen.getByText("复审差异：选择两个冻结执行记录"));
  expect(await screen.findByText(/默认先读取最新审查记录/)).toBeInTheDocument();
  expect(api.getReviewHistoryPage).toHaveBeenCalledTimes(1);
  await user.click(await screen.findByRole("button", { name: "读取更早审查记录" }));
  expect(api.getReviewHistoryPage).toHaveBeenCalledTimes(2);
  await chooseOption(user, await screen.findByRole("combobox", { name: "来源审查与执行" }), /审查标识 run-a/);
  const previousListboxes = new Set(screen.queryAllByRole("listbox", { hidden: true }));
  await user.click(screen.getByRole("combobox", { name: "目标审查与执行" }));
  const targetListbox = await waitFor(() => {
    const opened = screen.getAllByRole("listbox").filter((element) => !previousListboxes.has(element));
    expect(opened).toHaveLength(1);
    return opened[0]!;
  });
  await user.click(await within(targetListbox).findByRole("option", { name: /审查标识 run-b/ }));
  const context = await screen.findByRole("region", { name: "比较前提" });
  const side = within(context).getByRole("region", { name: "来源身份与依据" });
  expect(within(side).getByText(/完整审查；技术与服务：最低保障带宽、审查项目待核对；来源 1 项/)).toBeVisible();
  expect(within(side).getByText("甲方有限公司合同.txt")).toBeVisible();
  expect(within(side).getByText(/evidence-secret/)).not.toBeVisible();
  expect(within(side).getByText(/rule-secret/)).not.toBeVisible();
  expect(within(side).getByText(/input-secret/)).not.toBeVisible();
  expect(api.getReview).toHaveBeenCalledWith("case-1", "run-a", "run-a-attempt", expect.any(AbortSignal));
  expect(api.getReview).toHaveBeenCalledWith("case-1", "run-b", "run-b-attempt", expect.any(AbortSignal));
});

it("keeps the latest review selectable when an older page fails", async () => {
  const target = workspace("run-b");
  api.getReviewHistoryPage.mockReset();
  api.getReviewHistoryPage
    .mockResolvedValueOnce({
      entry: { runId: "run-b", workspace: target },
      nextRunId: "run-a",
      cycleDetected: false,
    })
    .mockRejectedValueOnce(new Error("更早记录不可读取"));
  api.getReview.mockReset();
  api.getReview.mockResolvedValue(target);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter><ReviewDiff caseId="case-1" latestRunId="run-b" actorScope="test" /></MemoryRouter></QueryClientProvider>);
  const user = userEvent.setup();
  await user.click(screen.getByText("复审差异：选择两个冻结执行记录"));
  await user.click(await screen.findByRole("button", { name: "读取更早审查记录" }));
  expect(await screen.findByText(/更早的审查记录没有加载/)).toBeInTheDocument();
  expect(screen.queryByText(/审查历史无法读取/)).not.toBeInTheDocument();
  expect(screen.getByRole("combobox", { name: "来源审查与执行" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "重试读取更早记录" })).toBeInTheDocument();
});
