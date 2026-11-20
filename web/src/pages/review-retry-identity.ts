// File metadata (manifest mirror):
// Purpose: Verifies that the selected review attempt is the uniquely identified latest attempt before allowing a retry.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { ReviewAttempts } from "../api/types";

/** A retry can target only an explicitly returned, uniquely identified latest attempt. */
export function retryAttemptIsConfirmedLatest(
  execution: ReviewAttempts | null | undefined,
  runId: string,
): boolean {
  const selected = execution?.attempt;
  const attempts = execution?.attempts;
  if (!selected || !runId || selected.run_id !== runId || !selected.id || !attempts?.length)
    return false;
  const ids = new Set<string>();
  const ordinals = new Set<number>();
  for (const attempt of attempts) {
    if (
      !attempt.id ||
      attempt.run_id !== runId ||
      !Number.isInteger(attempt.ordinal) ||
      attempt.ordinal < 1 ||
      ids.has(attempt.id) ||
      ordinals.has(attempt.ordinal)
    )
      return false;
    ids.add(attempt.id);
    ordinals.add(attempt.ordinal);
  }
  const matching = attempts.find((attempt) => attempt.id === selected.id);
  return Boolean(
    matching &&
    matching.ordinal === selected.ordinal &&
    attempts.every((attempt) => attempt.ordinal <= selected.ordinal),
  );
}
