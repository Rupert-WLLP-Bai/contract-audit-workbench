import type { ExecutionAttempt, ReviewHistoryEntry, ReviewWorkspace } from "../api/types";

export interface ReviewChoice {
  key: string;
  runId: string;
  attempt: ExecutionAttempt;
  workspace: ReviewWorkspace;
  runOrder: number;
}

export function reviewChoiceKey(runId: string, attemptId: string): string {
  return new URLSearchParams([ ["run_id", runId], ["attempt_id", attemptId] ]).toString();
}

export function reviewChoices(history: ReviewHistoryEntry[]): ReviewChoice[] {
  return history.flatMap((entry, runOrder) => {
    const execution = entry.workspace.execution;
    if (!execution) return [];
    const attempts = new Map(execution.attempts.map((attempt) => [attempt.id, attempt]));
    attempts.set(execution.attempt.id, execution.attempt);
    return Array.from(attempts.values(), (attempt) => ({
      key: reviewChoiceKey(entry.runId, attempt.id),
      runId: entry.runId,
      attempt,
      workspace: entry.workspace,
      runOrder,
    }));
  }).sort((left, right) => left.runOrder - right.runOrder || right.attempt.ordinal - left.attempt.ordinal);
}

export function reviewChoiceForKey(choices: ReviewChoice[], key: string): ReviewChoice | null {
  return choices.find((choice) => choice.key === key) ?? null;
}
