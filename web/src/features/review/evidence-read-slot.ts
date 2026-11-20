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
