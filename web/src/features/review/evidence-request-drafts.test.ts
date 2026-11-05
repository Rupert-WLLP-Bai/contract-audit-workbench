// File metadata (manifest mirror):
// Purpose: Verifies evidence creation draft isolation, bounded retention and invalidation so late operations cannot revive cleared or replaced checkpoints.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-05
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { afterEach, describe, expect, it, vi } from "vitest";
import {
  activateEvidenceDraft, beginEvidenceOperation, clearAllEvidenceDrafts, clearRejectedEvidenceOperation, evidenceDraftScope,
  evidenceOperationCurrent, hasEvidenceDraft, interruptEvidenceDraft, readEvidenceDraft, updateEvidenceDraft,
  updateEvidenceOperation,
} from "./evidence-request-drafts";

const scope = evidenceDraftScope("actor-context", "case-1", "run-1", "attempt-1")!;
afterEach(() => { clearAllEvidenceDrafts(); vi.useRealTimers(); });

describe("supplement creation checkpoints", () => {
  it("isolates actor, case, attempt and explicit source identities", () => {
    activateEvidenceDraft(scope, "evaluation:one");
    updateEvidenceDraft(scope, (draft) => ({ ...draft, question: "问题一", dirty: true }));
    activateEvidenceDraft(scope, "evaluation:two");
    expect(readEvidenceDraft(scope).question).toBe("");
    expect(hasEvidenceDraft(scope)).toBe(true);
    updateEvidenceDraft(scope, (draft) => ({ ...draft, source: "evaluation:two", question: "问题二", dirty: true }));
    activateEvidenceDraft(scope, "evaluation:one");
    expect(readEvidenceDraft(scope).question).toBe("问题一");
    for (const other of [
      evidenceDraftScope("another-actor", "case-1", "run-1", "attempt-1"),
      evidenceDraftScope("actor-context", "case-2", "run-1", "attempt-1"),
      evidenceDraftScope("actor-context", "case-1", "run-1", "attempt-2"),
    ]) expect(readEvidenceDraft(other).question).toBe("");
  });

  it("allows only the original interrupted intent to record a late task without continuing", () => {
    activateEvidenceDraft(scope, "evaluation:one");
    updateEvidenceDraft(scope, (draft) => ({ ...draft, question: "待补" }));
    const operation = beginEvidenceOperation(scope, "task-pending");
    interruptEvidenceDraft(scope);
    expect(evidenceOperationCurrent(scope, operation)).toBe(false);
    expect(updateEvidenceOperation(scope, operation, (draft) => ({ ...draft, phase: "linked", needsVerification: true,
      task: { id: "accepted", caseId: "case-1", version: 2, assigneeId: "owner" } }))).toBe(true);
    expect(readEvidenceDraft(scope)).toMatchObject({ interrupted: true, needsVerification: true, task: { id: "accepted" } });
  });

  it("records the same interrupted source's late accepted task without changing another source", () => {
    activateEvidenceDraft(scope, "evaluation:one");
    const operation = beginEvidenceOperation(scope, "task-pending");
    activateEvidenceDraft(scope, "evaluation:two");
    updateEvidenceDraft(scope, (draft) => ({ ...draft, question: "来源二" }));
    expect(evidenceOperationCurrent(scope, operation, "evaluation:one")).toBe(false);
    expect(updateEvidenceOperation(scope, operation, (draft) => ({ ...draft, phase: "linked", needsVerification: true,
      task: { id: "accepted", caseId: "case-1", version: 1, assigneeId: "owner" } }), "evaluation:one")).toBe(true);
    expect(readEvidenceDraft(scope).question).toBe("来源二");
    activateEvidenceDraft(scope, "evaluation:one");
    expect(readEvidenceDraft(scope)).toMatchObject({ interrupted: true, needsVerification: true, task: { id: "accepted" } });
  });

  it("clears only the rejected actor, source and operation, never another draft or replacement", () => {
    activateEvidenceDraft(scope, "evaluation:one");
    const rejected = beginEvidenceOperation(scope, "request-pending");
    updateEvidenceDraft(scope, (draft) => ({ ...draft, question: "旧来源", requestKey: "old-key" }));
    activateEvidenceDraft(scope, "evaluation:two");
    updateEvidenceDraft(scope, (draft) => ({ ...draft, question: "其他来源", dirty: true }));
    const other = evidenceDraftScope("other-actor", "case-1", "run-1", "attempt-1")!;
    activateEvidenceDraft(other, "evaluation:one");
    const otherOperation = beginEvidenceOperation(other, "request-pending");
    updateEvidenceDraft(other, (draft) => ({ ...draft, question: "其他身份", requestKey: "other-key" }));
    expect(clearRejectedEvidenceOperation(scope, "evaluation:one", rejected)).toBe(true);
    expect(readEvidenceDraft(scope, "evaluation:one").question).toBe("");
    expect(readEvidenceDraft(scope).question).toBe("其他来源");
    expect(readEvidenceDraft(other)).toMatchObject({ operation: otherOperation, question: "其他身份", requestKey: "other-key" });
    activateEvidenceDraft(scope, "evaluation:one");
    const earlier = beginEvidenceOperation(scope, "request-pending");
    const replacement = beginEvidenceOperation(scope, "request-pending");
    updateEvidenceDraft(scope, (draft) => ({ ...draft, question: "新办理", requestKey: "new-key" }));
    expect(clearRejectedEvidenceOperation(scope, "evaluation:one", earlier)).toBe(false);
    expect(readEvidenceDraft(scope)).toMatchObject({ operation: replacement, question: "新办理", requestKey: "new-key" });
    expect(readEvidenceDraft(other).question).toBe("其他身份");
  });

  it("cannot revive cleared, replaced or expired operations", () => {
    vi.useFakeTimers();
    activateEvidenceDraft(scope, "evaluation:one");
    const cleared = beginEvidenceOperation(scope, "task-pending");
    clearAllEvidenceDrafts();
    const replacement = beginEvidenceOperation(scope, "task-pending");
    updateEvidenceDraft(scope, (draft) => ({ ...draft, requestKey: "expired-key" }));
    expect(updateEvidenceOperation(scope, cleared, (draft) => ({ ...draft, question: "旧数据" }))).toBe(false);
    vi.advanceTimersByTime(60 * 60 * 1000 + 1);
    expect(updateEvidenceOperation(scope, replacement, (draft) => ({ ...draft, question: "过期数据" }))).toBe(false);
    expect(clearRejectedEvidenceOperation(scope, "", replacement)).toBe(true);
    expect(readEvidenceDraft(scope).question).toBe("");
  });
});
