import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import * as api from "./endpoints";
import { ApiError } from "./http";
import { queryKeys, useReview } from "./queries";
import type { ReviewWorkspace } from "./types";

function workspace(state: string): ReviewWorkspace {
  const attempt = {
    id: "a1", run_id: "r1", ordinal: 1, retry_of_attempt_id: null,
    state, accepted_at: "2026-10-21T06:00:00Z", started_at: null,
    finished_at: null, parsed_document_ids: [], input_hash: null,
    output_hash: null, error_code: null, history_kind: "frozen",
  };
  return {
    run: { id: "r1", case_id: "c1", state, updated_at: "2026-10-21T06:00:00Z", started_at: null, model_ref: null, partial_reason: null, failure_message: null },
    documents: [], findings: [], review_attempts: { attempt, attempts: [attempt] },
    execution: null, audit_events: [],
  };
}

describe("review polling", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("backs off unchanged catch-up reads, slows while live, and stops after a terminal refresh", async () => {
    vi.useFakeTimers();
    let response = workspace("running");
    const getReview = vi.spyOn(api, "getReview").mockImplementation(async () => structuredClone(response));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { rerender, unmount } = renderHook(
      ({ live }) => useReview("c1", "r1", null, live),
      { wrapper, initialProps: { live: false } },
    );
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getReview).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview).toHaveBeenCalledTimes(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(3_000); });
    expect(getReview).toHaveBeenCalledTimes(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(1_001); });
    expect(getReview).toHaveBeenCalledTimes(3);
    await act(async () => { await vi.advanceTimersByTimeAsync(8_000); });
    expect(getReview).toHaveBeenCalledTimes(4);

    rerender({ live: true });
    await act(async () => { await vi.advanceTimersByTimeAsync(29_999); });
    expect(getReview).toHaveBeenCalledTimes(4);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getReview).toHaveBeenCalledTimes(5);

    rerender({ live: false });
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview).toHaveBeenCalledTimes(6);
    response = workspace("awaiting_human");
    await act(async () => {
      await client.invalidateQueries({ queryKey: queryKeys.review("c1", "r1") });
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(getReview).toHaveBeenCalledTimes(7);
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(getReview).toHaveBeenCalledTimes(7);
    unmount();
    client.clear();
  });

  it("stops catch-up polling after a forbidden workspace read", async () => {
    vi.useFakeTimers();
    const getReview = vi.spyOn(api, "getReview")
      .mockResolvedValueOnce(workspace("running"))
      .mockRejectedValue(new ApiError("访问已撤销", 403, "forbidden", false, null));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { unmount } = renderHook(() => useReview("c1", "r1"), { wrapper });
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview).toHaveBeenCalledTimes(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(getReview).toHaveBeenCalledTimes(2);
    unmount();
    client.clear();
  });
});
