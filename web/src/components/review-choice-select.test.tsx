// File metadata (manifest mirror):
// Purpose: Checks frozen review choices display their own attempt times and retain exact run and attempt identity after history changes or reorders.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { parseReviewWorkspace } from "../api/parse";
import { reviewChoiceKey, reviewChoices } from "../domain/workflow";
import { renderWithAntd as render } from "../test/antd";
import { ReviewChoiceSelect } from "./review-choice-select";

function history(runId: string, id: string, accepted: string) {
  const attempt = { id, run_id: runId, ordinal: 1, state: "completed", accepted_at: accepted,
    started_at: "2026-11-05T01:02:10Z", finished_at: "2026-11-05T01:03:10Z", history_kind: "frozen" };
  return { runId, workspace: parseReviewWorkspace({ run: { id: runId, case_id: "case-1", state: "completed", updated_at: "2026-11-06T01:00:00Z" },
    review_attempts: { attempt, attempts: [attempt] }, documents: [], findings: [], audit_events: [], execution: null, snapshot: null, evaluations: [], revisions: [] }) };
}

it("shows attempt times and stable identities without changing selection after history reorders", async () => {
  const original = history("run-old", "att-old", "2026-11-05T01:02:03Z");
  const added = history("run-new", "att-new", "2026-11-05T01:02:04Z");
  const onChange = vi.fn();
  const key = reviewChoiceKey("run-old", "att-old");
  const view = render(<ReviewChoiceSelect label="选择审查" choices={reviewChoices([original])} value={key} onChange={onChange} />);
  expect(screen.getByText(/受理时间 2026\/11\/05 09:02:03/)).toBeVisible();
  expect(screen.getByText(/执行开始 2026\/11\/05 09:02:10/)).toBeVisible();
  expect(screen.getByTitle("run-old")).not.toBeVisible();
  view.rerender(<ReviewChoiceSelect label="选择审查" choices={reviewChoices([added, original])} value={key} onChange={onChange} />);
  expect(screen.getByText(/受理时间 2026\/11\/05 09:02:03/)).toBeVisible();
  expect(onChange).not.toHaveBeenCalled();
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox", { name: "选择审查" }));
  const option = screen.getByRole("option", { name: /审查标识 run-new.*执行标识 att-new/ });
  await user.click(option);
  expect(onChange).toHaveBeenCalledWith(reviewChoiceKey("run-new", "att-new"), expect.anything());
});

it("distinguishes invalid timestamps and exposes their exact raw values for copying", async () => {
  const original = history("run-invalid", "att-invalid", "invalid-accepted-原值");
  original.workspace.review_attempts!.attempt.started_at = "invalid-start-原值";
  const choice = reviewChoices([original]);
  render(<ReviewChoiceSelect label="选择审查" choices={choice} value={choice[0]!.key} onChange={() => {}} />);
  expect(screen.getAllByText(/时间格式异常/).length).toBeGreaterThan(0);
  const user = userEvent.setup();
  const detail = screen.getByText("受理时间原始值").closest("details")!;
  await user.click(screen.getByText("受理时间原始值"));
  expect(screen.getByText("invalid-accepted-原值")).toBeVisible();
  expect(within(detail).getByRole("button", { name: "复制完整值" })).toBeVisible();
});
