// File metadata (manifest mirror):
// Purpose: Bounds concurrent evidence reads and checks cancellation before dispatching queued reads.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

const maximumConcurrentEvidenceReads = 8;
let activeEvidenceReads = 0;
const queuedEvidenceReads: (() => void)[] = [];

export async function withEvidenceReadSlot<T>(
  signal: AbortSignal,
  read: () => Promise<T>,
): Promise<T> {
  if (activeEvidenceReads >= maximumConcurrentEvidenceReads) {
    await new Promise<void>((resolve) => queuedEvidenceReads.push(resolve));
  } else {
    activeEvidenceReads++;
  }
  try {
    signal.throwIfAborted();
    return await read();
  } finally {
    const next = queuedEvidenceReads.shift();
    if (next) next();
    else activeEvidenceReads--;
  }
}
