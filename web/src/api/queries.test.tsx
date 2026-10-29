import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import * as api from "./endpoints";
import { ApiError } from "./http";
import {
  clearIdentityQueries,
  queryKeys,
  useCaseDetail,
  useCaseReviewHistory,
  useCreateRulePack,
  useKnowledge,
  useReview,
  useReviewDiagnostics,
  useRulePack,
  useRulePackHistory,
  useRulePacks,
  useSession,
  useTemplates,
  useTestRulePack,
} from "./queries";
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

function testClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

function testWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

function workspaceWithStates(
  attemptState: string,
  runState: string,
): ReviewWorkspace {
  const base = workspace(runState);
  const attempt = { ...base.review_attempts!.attempt, state: attemptState };
  return {
    ...base,
    review_attempts: { attempt, attempts: [attempt] },
  };
}

describe("identity cache cleanup", () => {
  it("removes every query except session-scoped ones", () => {
    const cancelQueries = vi.fn(async () => undefined);
    const removeQueries = vi.fn();
    const client = { cancelQueries, removeQueries } as unknown as QueryClient;
    clearIdentityQueries(client);
    expect(cancelQueries).toHaveBeenCalledTimes(1);
    expect(removeQueries).toHaveBeenCalledTimes(1);
    const filter = removeQueries.mock.calls[0]?.[0] as unknown as {
      predicate: (query: { queryKey: readonly unknown[] }) => boolean;
    };
    expect(filter.predicate({ queryKey: ["session", "client"] })).toBe(false);
    expect(filter.predicate({ queryKey: ["session", "admin"] })).toBe(false);
    expect(filter.predicate({ queryKey: ["cases"] })).toBe(true);
    expect(filter.predicate({ queryKey: ["settings", "client"] })).toBe(true);
  });
});

describe("query hook gates", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("does not retry the session query and keys it by audience", async () => {
    const getSession = vi
      .spyOn(api, "getSession")
      .mockResolvedValue({} as never);
    const client = testClient();
    const { unmount } = renderHook(() => useSession("admin"), {
      wrapper: testWrapper(client),
    });
    await waitFor(() => expect(getSession).toHaveBeenCalledTimes(1));
    expect(
      client.getQueryCache().find({ queryKey: queryKeys.sessionFor("admin") })
        ?.options.retry,
    ).toBe(false);
    unmount();
    client.clear();
  });

  it("loads templates by default", async () => {
    const listTemplates = vi
      .spyOn(api, "listTemplates")
      .mockResolvedValue([]);
    const client = testClient();
    const { unmount } = renderHook(() => useTemplates(), {
      wrapper: testWrapper(client),
    });
    await waitFor(() => expect(listTemplates).toHaveBeenCalledTimes(1));
    unmount();
    client.clear();
  });

  it("loads knowledge only when both ids are present", async () => {
    const getKnowledge = vi
      .spyOn(api, "getKnowledge")
      .mockResolvedValue({} as never);
    const client = testClient();
    const { rerender, unmount } = renderHook(
      ({ knowledgeId, versionId }) => useKnowledge(knowledgeId, versionId),
      {
        wrapper: testWrapper(client),
        initialProps: { knowledgeId: "k1", versionId: "v1" },
      },
    );
    await waitFor(() => expect(getKnowledge).toHaveBeenCalledTimes(1));
    rerender({ knowledgeId: "", versionId: "v1" });
    await act(async () => { await Promise.resolve(); });
    expect(getKnowledge).toHaveBeenCalledTimes(1);
    rerender({ knowledgeId: "k1", versionId: "" });
    await act(async () => { await Promise.resolve(); });
    expect(getKnowledge).toHaveBeenCalledTimes(1);
    unmount();
    client.clear();
  });

  it("loads a case detail only for a non-empty id", async () => {
    const getCase = vi.spyOn(api, "getCase").mockResolvedValue({} as never);
    const client = testClient();
    const { rerender, unmount } = renderHook(
      ({ caseId }) => useCaseDetail(caseId),
      { wrapper: testWrapper(client), initialProps: { caseId: "c1" } },
    );
    await waitFor(() => expect(getCase).toHaveBeenCalledTimes(1));
    rerender({ caseId: "" });
    await act(async () => { await Promise.resolve(); });
    expect(getCase).toHaveBeenCalledTimes(1);
    unmount();
    client.clear();
  });

  it("loads review history only with a case and latest run", async () => {
    const getReviewHistory = vi
      .spyOn(api, "getReviewHistory")
      .mockResolvedValue({} as never);
    const client = testClient();
    const { rerender, unmount } = renderHook(
      ({ caseId, latestRunId }) => useCaseReviewHistory(caseId, latestRunId),
      {
        wrapper: testWrapper(client),
        initialProps: { caseId: "c1", latestRunId: "r1" as string | null },
      },
    );
    await waitFor(() => expect(getReviewHistory).toHaveBeenCalledTimes(1));
    rerender({ caseId: "", latestRunId: "r1" });
    await act(async () => { await Promise.resolve(); });
    expect(getReviewHistory).toHaveBeenCalledTimes(1);
    rerender({ caseId: "c1", latestRunId: null });
    await act(async () => { await Promise.resolve(); });
    expect(getReviewHistory).toHaveBeenCalledTimes(1);
    unmount();
    client.clear();
  });

  it("does not load a review without both ids", async () => {
    const getReview = vi
      .spyOn(api, "getReview")
      .mockResolvedValue(workspace("running"));
    const client = testClient();
    const { rerender, unmount } = renderHook(
      ({ caseId, runId }) => useReview(caseId, runId),
      {
        wrapper: testWrapper(client),
        initialProps: { caseId: "", runId: "r1" },
      },
    );
    await act(async () => { await Promise.resolve(); });
    expect(getReview).not.toHaveBeenCalled();
    rerender({ caseId: "c1", runId: "" });
    await act(async () => { await Promise.resolve(); });
    expect(getReview).not.toHaveBeenCalled();
    unmount();
    client.clear();
  });

  it("loads diagnostics only when enabled and both ids are present", async () => {
    const getReviewDiagnostics = vi
      .spyOn(api, "getReviewDiagnostics")
      .mockResolvedValue({} as never);
    const client = testClient();
    const { rerender, unmount } = renderHook(
      ({ caseId, runId, enabled }) =>
        useReviewDiagnostics(caseId, runId, null, enabled),
      {
        wrapper: testWrapper(client),
        initialProps: { caseId: "c1", runId: "r1", enabled: true },
      },
    );
    await waitFor(() => expect(getReviewDiagnostics).toHaveBeenCalledTimes(1));
    expect(
      client
        .getQueryCache()
        .find({ queryKey: queryKeys.reviewDiagnostics("c1", "r1", null) })
        ?.options.retry,
    ).toBe(false);
    rerender({ caseId: "c1", runId: "r1", enabled: false });
    await act(async () => { await Promise.resolve(); });
    expect(getReviewDiagnostics).toHaveBeenCalledTimes(1);
    rerender({ caseId: "", runId: "r1", enabled: true });
    await act(async () => { await Promise.resolve(); });
    expect(getReviewDiagnostics).toHaveBeenCalledTimes(1);
    rerender({ caseId: "c1", runId: "", enabled: true });
    await act(async () => { await Promise.resolve(); });
    expect(getReviewDiagnostics).toHaveBeenCalledTimes(1);
    unmount();
    client.clear();
  });

  it("gates rule pack queries on their identifiers without retrying", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 500 }));
    const client = testClient();
    const packs = renderHook(() => useRulePacks(), {
      wrapper: testWrapper(client),
    });
    await act(async () => { await Promise.resolve(); });
    expect(
      client.getQueryCache().find({ queryKey: queryKeys.rulePackList })?.options
        .retry,
    ).toBe(false);
    packs.unmount();

    const pack = renderHook(
      ({ packId, version }) => useRulePack(packId, version),
      {
        wrapper: testWrapper(client),
        initialProps: { packId: "p1", version: "v1" },
      },
    );
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const calls = fetchMock.mock.calls.length;
    pack.rerender({ packId: "", version: "v1" });
    await act(async () => { await Promise.resolve(); });
    expect(fetchMock).toHaveBeenCalledTimes(calls);
    pack.rerender({ packId: "p1", version: "" });
    await act(async () => { await Promise.resolve(); });
    expect(fetchMock).toHaveBeenCalledTimes(calls);
    expect(
      client.getQueryCache().find({ queryKey: queryKeys.rulePack("p1", "v1") })
        ?.options.retry,
    ).toBe(false);
    pack.unmount();

    const history = renderHook(
      ({ packId }) => useRulePackHistory(packId),
      {
        wrapper: testWrapper(client),
        initialProps: { packId: "p1" },
      },
    );
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const historyCalls = fetchMock.mock.calls.length;
    history.rerender({ packId: "" });
    await act(async () => { await Promise.resolve(); });
    expect(fetchMock).toHaveBeenCalledTimes(historyCalls);
    expect(
      client.getQueryCache().find({ queryKey: queryKeys.rulePackHistory("p1") })
        ?.options.retry,
    ).toBe(false);
    history.unmount();
    client.clear();
  });

  it("does not retry rule pack mutations", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));
    const client = testClient();
    const create = renderHook(() => useCreateRulePack(), {
      wrapper: testWrapper(client),
    });
    act(() => {
      create.result.current.mutate({} as never);
    });
    const createEntry = client.getMutationCache().getAll()[0];
    client.clear();
    expect(createEntry?.options.retry).toBe(false);
    create.unmount();

    const test = renderHook(() => useTestRulePack("p1", "v1"), {
      wrapper: testWrapper(client),
    });
    act(() => {
      test.result.current.mutate({} as never);
    });
    const testEntry = client.getMutationCache().getAll()[0];
    client.clear();
    expect(testEntry?.options.retry).toBe(false);
    test.unmount();
    await act(async () => { await Promise.resolve(); });
    expect(fetchMock).toHaveBeenCalled();
  });
});

describe("review state polling", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("falls back to the run state when review attempts are null", async () => {
    vi.useFakeTimers();
    const data: ReviewWorkspace = { ...workspace("running"), review_attempts: null };
    const getReview = vi
      .spyOn(api, "getReview")
      .mockImplementation(async () => structuredClone(data));
    const client = testClient();
    const { unmount } = renderHook(() => useReview("c1", "r1"), {
      wrapper: testWrapper(client),
    });
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getReview).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview).toHaveBeenCalledTimes(2);
    unmount();
    client.clear();
  });

  it("does not poll when the attempt state is empty", async () => {
    vi.useFakeTimers();
    const data = workspaceWithStates("", "running");
    const getReview = vi
      .spyOn(api, "getReview")
      .mockImplementation(async () => structuredClone(data));
    const client = testClient();
    const { unmount } = renderHook(() => useReview("c1", "r1"), {
      wrapper: testWrapper(client),
    });
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getReview).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(getReview).toHaveBeenCalledTimes(1);
    unmount();
    client.clear();
  });

  it("does not poll before the first workspace response", async () => {
    vi.useFakeTimers();
    let resolve: (value: ReviewWorkspace) => void = () => {};
    const pending = new Promise<ReviewWorkspace>((settle) => {
      resolve = settle;
    });
    const getReview = vi.spyOn(api, "getReview").mockReturnValue(pending);
    const client = testClient();
    const { unmount } = renderHook(() => useReview("c1", "r1"), {
      wrapper: testWrapper(client),
    });
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(getReview).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolve(workspace("running"));
      await vi.advanceTimersByTimeAsync(1);
    });
    unmount();
    client.clear();
  });

  it("resets polling state when the attempt scope changes", async () => {
    vi.useFakeTimers();
    const getReview = vi
      .spyOn(api, "getReview")
      .mockImplementation(async () => structuredClone(workspace("running")));
    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: Number.POSITIVE_INFINITY },
      },
    });
    const wrapper = testWrapper(client);
    const emptyAttempt = renderHook(
      ({ attemptId }: { attemptId: string | null }) =>
        useReview("c1", "r1", attemptId),
      { wrapper, initialProps: { attemptId: "" as string | null } },
    );
    const nullAttempt = renderHook(
      () => useReview("c1", "r1", null),
      { wrapper },
    );
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    await act(async () => { await vi.advanceTimersByTimeAsync(4_000); });
    nullAttempt.unmount();
    const before = getReview.mock.calls.length;
    emptyAttempt.rerender({ attemptId: null });
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview.mock.calls.length).toBeGreaterThan(before);
    emptyAttempt.unmount();
    client.clear();
  });

  it("restarts the backoff after a live stream ends", async () => {
    vi.useFakeTimers();
    const getReview = vi
      .spyOn(api, "getReview")
      .mockImplementation(async () => structuredClone(workspace("running")));
    const client = testClient();
    const { rerender, unmount } = renderHook(
      ({ live }) => useReview("c1", "r1", null, live),
      { wrapper: testWrapper(client), initialProps: { live: false } },
    );
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getReview).toHaveBeenCalledTimes(1);
    rerender({ live: true });
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(getReview).toHaveBeenCalledTimes(2);
    rerender({ live: false });
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview).toHaveBeenCalledTimes(3);
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview).toHaveBeenCalledTimes(3);
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview).toHaveBeenCalledTimes(4);
    unmount();
    client.clear();
  });
});
