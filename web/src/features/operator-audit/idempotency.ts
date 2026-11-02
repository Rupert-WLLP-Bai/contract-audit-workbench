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
