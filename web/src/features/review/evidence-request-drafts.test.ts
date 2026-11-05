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
  activateEvidenceDraft, beginEvidenceOperation, clearAllEvidenceDrafts, evidenceDraftScope,
  evidenceOperationCurrent, hasEvidenceDraft, interruptEvidenceDraft, readEvidenceDraft, updateEvidenceDraft,
  updateEvidenceOperation,
} from "./evidence-request-drafts";

const scope = evidenceDraftScope("actor-context", "case-1", "run-1", "attempt-1")!;
afterEach(() => { clearAllEvidenceDrafts(); vi.useRealTimers(); });

describe("supplement creation checkpoints", () => {
  it("isolates actor, case, attempt and explicit source identities", () => {
    updateEvidenceDraft(scope, (draft) => ({ ...draft, source: "evaluation:one", question: "问题一", dirty: true }));
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
    updateEvidenceDraft(scope, (draft) => ({ ...draft, source: "evaluation:one", question: "待补" }));
    const operation = beginEvidenceOperation(scope, "task-pending");
    interruptEvidenceDraft(scope);
    expect(evidenceOperationCurrent(scope, operation)).toBe(false);
    expect(updateEvidenceOperation(scope, operation, (draft) => ({ ...draft, phase: "linked", needsVerification: true,
      task: { id: "accepted", caseId: "case-1", version: 2, assigneeId: "owner" } }))).toBe(true);
    expect(readEvidenceDraft(scope)).toMatchObject({ interrupted: true, needsVerification: true, task: { id: "accepted" } });
  });

  it("cannot revive cleared, replaced or expired operations", () => {
    vi.useFakeTimers();
    updateEvidenceDraft(scope, (draft) => ({ ...draft, source: "evaluation:one" }));
    const cleared = beginEvidenceOperation(scope, "task-pending");
    clearAllEvidenceDrafts();
    const replacement = beginEvidenceOperation(scope, "task-pending");
    expect(updateEvidenceOperation(scope, cleared, (draft) => ({ ...draft, question: "旧数据" }))).toBe(false);
    vi.advanceTimersByTime(60 * 60 * 1000 + 1);
    expect(updateEvidenceOperation(scope, replacement, (draft) => ({ ...draft, question: "过期数据" }))).toBe(false);
    expect(readEvidenceDraft(scope).question).toBe("");
  });
});
