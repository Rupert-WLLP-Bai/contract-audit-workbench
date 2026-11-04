// File metadata (manifest mirror):
// Purpose: Derives selectable run/attempt choices from persisted review history and keys a choice by both IDs so the UI cannot switch to a different frozen attempt implicitly.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import type {
  ExecutionAttempt,
  ReviewHistoryEntry,
  ReviewWorkspace,
} from "../api/types";
import { runLabel } from "./review";
import { preciseTime } from "./time";

export interface ReviewChoice {
  key: string;
  runId: string;
  attempt: ExecutionAttempt;
  workspace: ReviewWorkspace;
}

export function reviewChoiceKey(runId: string, attemptId: string): string {
  return new URLSearchParams([
    ["run_id", runId],
    ["attempt_id", attemptId],
  ]).toString();
}

export function reviewChoiceLabel(choice: ReviewChoice): string {
  return `${reviewChoiceShortLabel(choice)} · 执行开始 ${preciseTime(choice.attempt.started_at)} · 完成时间 ${preciseTime(choice.attempt.finished_at)} · 审查更新时间 ${preciseTime(choice.workspace.run.updated_at)} · 审查标识 ${choice.runId} · 执行标识 ${choice.attempt.id}`;
}

export function reviewChoiceShortLabel(choice: ReviewChoice): string {
  return `受理时间 ${preciseTime(choice.attempt.accepted_at)} · 第 ${choice.attempt.ordinal} 次尝试 · ${runLabel(choice.attempt.state)}`;
}

export function reviewChoices(history: ReviewHistoryEntry[]): ReviewChoice[] {
  return history
    .flatMap((entry, runOrder) => {
      const reviewAttempts = entry.workspace.review_attempts;
      if (!reviewAttempts) return [];
      const attempts = new Map(
        reviewAttempts.attempts.map((attempt) => [attempt.id, attempt]),
      );
      attempts.set(reviewAttempts.attempt.id, reviewAttempts.attempt);
      return Array.from(attempts.values(), (attempt) => ({
        key: reviewChoiceKey(entry.runId, attempt.id),
        runId: entry.runId,
        attempt,
        workspace: entry.workspace,
        runOrder,
      }));
    })
    .sort(
      (left, right) =>
        left.runOrder - right.runOrder ||
        right.attempt.ordinal - left.attempt.ordinal,
    )
    .map(({ runOrder: _runOrder, ...choice }) => choice);
}

export function reviewChoiceForKey(
  choices: ReviewChoice[],
  key: string,
): ReviewChoice | null {
  return choices.find((choice) => choice.key === key) ?? null;
}
