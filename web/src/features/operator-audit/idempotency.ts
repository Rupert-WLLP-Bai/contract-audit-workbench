// File metadata (manifest mirror):
// Purpose: Keeps stable idempotency keys per identical operator-audit operation and payload during a mounted session so retries cannot accidentally become new writes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-29
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { useRef } from "react";
import { newIdempotencyKey } from "../../api/idempotency";

/** Stable key for identical write inputs; a retry reuses the same key. */
export type IdempotencyKeyFor = (operation: string, input: unknown) => string;

export function useStableIdempotencyKey(): IdempotencyKeyFor {
  const keys = useRef(new Map<string, string>());
  return (operation, input) => {
    const identity = JSON.stringify([operation, input]);
    const existing = keys.current.get(identity);
    if (existing !== undefined) return existing;
    const key = newIdempotencyKey();
    keys.current.set(identity, key);
    return key;
  };
}
