// File metadata (manifest mirror):
// Purpose: Checks manual-work filters, exact navigation callbacks and unknown or refreshing count feedback without asserting business completion.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-05
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useState } from "react";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithAntd as render } from "../test/antd";
import type { WorkFilter, WorkItem, WorkProjection, WorkTarget } from "../features/review/work-items";
import { ReviewWorkItems } from "./review-work-items";

const make = (category: WorkFilter, kind: WorkTarget["kind"], id: string, sourceRunId = "run-current"): WorkItem => ({
  key: id, category, title: `事项 ${id}`, origin: "来源", status: "待处理", verification: "尚未核验",
  sourceRunId, sourceAttemptId: sourceRunId === "run-current" ? "attempt-current" : "attempt-old",
  target: { kind, id, runId: sourceRunId, attemptId: "attempt-current" }, needsCheck: false,
});
const projection: WorkProjection = { frozenScope: true, sourcesKnown: true, agentSkipped: false, requestsKnown: true, caseKnown: true,
  items: [make("judgment", "finding", "f1"), make("evidence", "request", "r1", "run-old"), make("remediation", "remediation", "m1", "run-old")] };

function Harness({ onOpen, value = projection, error = null }: { onOpen: (target: WorkTarget) => void; value?: WorkProjection; error?: string | null }) {
  const [filter, setFilter] = useState<WorkFilter>("judgment");
  return <ReviewWorkItems projection={value} filter={filter} onFilter={setFilter} onOpen={onOpen} refreshing={false} error={error}
    canPrepareRequest currentRunId="run-current" currentAttemptId="attempt-current" currentOrdinal={2} />;
}

it("filters the three kinds and opens the exact target without exposing technical identities in the main text", async () => {
  const user = userEvent.setup();
  const open = vi.fn();
  render(<Harness onOpen={open} />);
  expect(screen.getByText("当前第 2 次执行", { exact: false })).toBeVisible();
  await user.click(screen.getByRole("button", { name: "查看判断与原文" }));
  expect(open).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "finding", id: "f1" }));
  await user.click(screen.getByRole("button", { name: "待补件：1" }));
  const list = screen.getByRole("list", { name: "待补件事项" });
  expect(within(list).getByText("其他来源执行", { exact: false })).toBeVisible();
  expect(within(list).getByText("run-old / attempt-old")).not.toBeVisible();
  await user.click(within(list).getByRole("button", { name: "查看补件请求" }));
  expect(open).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "request", id: "r1" }));
  await user.click(screen.getByRole("button", { name: "待整改：1" }));
  await user.click(screen.getByRole("button", { name: "查看整改记录" }));
  expect(open).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "remediation", id: "m1" }));
});

it("does not report an empty or complete queue when the latest refresh failed", () => {
  render(<Harness onOpen={vi.fn()} value={{ ...projection, items: [] }} error="补件请求读取失败" />);
  expect(screen.getByRole("alert")).toHaveTextContent("数量待重新核对");
  expect(screen.getByText("当前分类的状态尚未确认。")).toBeVisible();
  expect(screen.queryByText("当前分类没有未完成事项。")).not.toBeInTheDocument();
});
