import { describe, expect, it } from "vitest";
import type { ExecutionAttempt, ReviewAttempts } from "../api/types";
import { retryAttemptIsConfirmedLatest } from "./review-retry-identity";

function attempt(id: string, ordinal: number, runId = "run"): ExecutionAttempt {
  return {
    id,
    run_id: runId,
    ordinal,
    retry_of_attempt_id: null,
    state: "partial",
    accepted_at: null,
    started_at: null,
    finished_at: null,
    parsed_document_ids: [],
    input_hash: null,
    output_hash: null,
    error_code: null,
    history_kind: "frozen",
  };
}

describe("retry attempt identity", () => {
  const old = attempt("old", 1);
  const latest = attempt("latest", 2);
  const execution: ReviewAttempts = { attempt: latest, attempts: [old, latest] };
  it("accepts only an identified latest attempt from the complete list", () => {
    expect(retryAttemptIsConfirmedLatest(execution, "run")).toBe(true);
    expect(retryAttemptIsConfirmedLatest({ ...execution, attempt: old }, "run")).toBe(false);
    expect(retryAttemptIsConfirmedLatest(null, "run")).toBe(false);
    expect(retryAttemptIsConfirmedLatest({ ...execution, attempts: [] }, "run")).toBe(false);
    expect(retryAttemptIsConfirmedLatest({ ...execution, attempts: [old] }, "run")).toBe(false);
  });
  it("fails closed for duplicate or inconsistent identities", () => {
    expect(
      retryAttemptIsConfirmedLatest({ ...execution, attempts: [old, latest, latest] }, "run"),
    ).toBe(false);
    expect(
      retryAttemptIsConfirmedLatest(
        { ...execution, attempts: [old, attempt("other", 2), latest] },
        "run",
      ),
    ).toBe(false);
    expect(
      retryAttemptIsConfirmedLatest(
        { ...execution, attempts: [old, attempt("latest", 2, "other")] },
        "run",
      ),
    ).toBe(false);
    expect(
      retryAttemptIsConfirmedLatest({ ...execution, attempt: { ...latest, ordinal: 3 } }, "run"),
    ).toBe(false);
  });
});
