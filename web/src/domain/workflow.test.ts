// File metadata (manifest mirror):
// Purpose: Checks run and attempt choice identity survives appended or reordered history and missing attempt timestamps are not replaced with a parent run update time.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, it } from "vitest";
import { parseReviewWorkspace } from "../api/parse";
import { reviewChoiceForKey, reviewChoiceKey, reviewChoiceLabel, reviewChoices } from "./workflow";

function entry(runId: string, attemptId: string, accepted: string | null) {
  const attempt = { id: attemptId, run_id: runId, ordinal: 2, state: "completed", accepted_at: accepted,
    started_at: "2026-11-05T01:02:10Z", finished_at: "2026-11-05T01:03:11Z", history_kind: "frozen" };
  return { runId, workspace: parseReviewWorkspace({
    run: { id: runId, case_id: "case-1", state: "completed", updated_at: "2026-11-06T01:00:00Z" },
    review_attempts: { attempt, attempts: [attempt] }, documents: [], findings: [], audit_events: [],
    execution: null, snapshot: null, evaluations: [], revisions: [], previous_run_id: null,
  }) };
}

describe("stable review choices", () => {
  it("retains both identities and the same label after adding and reordering history", () => {
    const old = entry("run-old", "att-old", "2026-11-05T01:02:03Z");
    const other = entry("run-other", "att-other", "2026-11-05T01:02:04Z");
    const key = reviewChoiceKey("run-old", "att-old");
    const before = reviewChoiceForKey(reviewChoices([old]), key)!;
    const after = reviewChoiceForKey(reviewChoices([other, old]), key)!;
    expect(after.runId).toBe("run-old");
    expect(after.attempt.id).toBe("att-old");
    expect(reviewChoiceLabel(after)).toBe(reviewChoiceLabel(before));
    expect(reviewChoiceLabel(after)).toContain("受理时间 2026/11/05 09:02:03");
    expect(reviewChoiceLabel(after)).toContain("执行开始 2026/11/05 09:02:10");
    expect(reviewChoiceLabel(after)).toContain("完成时间 2026/11/05 09:03:11");
    expect(reviewChoiceLabel(after)).toContain("审查更新时间 2026/11/06 09:00:00");
    expect(reviewChoiceLabel(after)).not.toContain("审查记录 1");
  });

  it("does not replace missing attempt times with the run update time", () => {
    const choice = reviewChoices([entry("run-old", "att-old", null)])[0]!;
    expect(reviewChoiceLabel(choice)).toContain("受理时间 未记录");
  });
});
