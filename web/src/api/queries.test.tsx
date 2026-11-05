// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “review polling, identity cache cleanup”; representative assertions cover backs off unchanged catch-up reads, slows while live, and stops after a terminal refresh, stops catch-up polling after a forbidden workspace read, stops catch-up polling after the session expires. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-11-05
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import * as api from "./endpoints";
import { ApiError, setSessionIdentity } from "./http";
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
    const { rerender, unmount } = renderHook(() => useReview("c1", "r1"), { wrapper });
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview).toHaveBeenCalledTimes(2);
    const query = client.getQueryCache().find({
      queryKey: queryKeys.review("c1", "r1"),
    });
    const interval = query?.observers[0]?.options.refetchInterval;
    expect(interval).toBeTypeOf("function");
    if (query && typeof interval === "function") {
      expect(interval(query)).toBe(false);
    }
    rerender();
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(getReview).toHaveBeenCalledTimes(2);
    unmount();
    client.clear();
  });

  it("stops catch-up polling after the session expires", async () => {
    vi.useFakeTimers();
    const getReview = vi.spyOn(api, "getReview")
      .mockResolvedValueOnce(workspace("running"))
      .mockRejectedValue(new ApiError("登录状态已失效", 401, "unauthenticated", false, null));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { rerender, unmount } = renderHook(() => useReview("c1", "r1"), { wrapper });
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
    expect(getReview).toHaveBeenCalledTimes(2);
    rerender();
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

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "Content-Type": "application/json" },
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
    setSessionIdentity(null);
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

  it("loads the latest review first and older records only on demand", async () => {
    const getHistoryPage = vi
      .spyOn(api, "getReviewHistoryPage")
      .mockImplementation(async (_caseId, runId) => ({
        entry: { runId, workspace: workspace("completed") },
        nextRunId: runId === "r1" ? "r0" : null,
        cycleDetected: false,
      }));
    const client = testClient();
    const { result, rerender, unmount } = renderHook(
      ({ caseId, latestRunId }) => useCaseReviewHistory(caseId, latestRunId),
      {
        wrapper: testWrapper(client),
        initialProps: { caseId: "c1", latestRunId: "r1" as string | null },
      },
    );
    await waitFor(() => expect(getHistoryPage).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(result.current.data).toHaveLength(1));
    expect(result.current.data?.map((entry) => entry.runId)).toEqual(["r1"]);
    expect(result.current.hasOlder).toBe(true);
    expect(result.current.isFetching).toBe(false);
    expect(getHistoryPage).toHaveBeenCalledWith("c1", "r1", [], expect.any(AbortSignal));

    await act(async () => { await result.current.fetchOlder(); });
    expect(getHistoryPage).toHaveBeenCalledTimes(2);
    expect(getHistoryPage).toHaveBeenLastCalledWith(
      "c1",
      "r0",
      ["r1"],
      expect.any(AbortSignal),
    );
    await waitFor(() =>
      expect(result.current.data?.map((entry) => entry.runId)).toEqual([
        "r1",
        "r0",
      ]),
    );
    expect(result.current.hasOlder).toBe(false);
    expect(result.current.cycleDetected).toBe(false);

    rerender({ caseId: "", latestRunId: "r1" });
    await act(async () => { await Promise.resolve(); });
    expect(getHistoryPage).toHaveBeenCalledTimes(2);
    rerender({ caseId: "c1", latestRunId: null });
    await act(async () => { await Promise.resolve(); });
    expect(getHistoryPage).toHaveBeenCalledTimes(2);
    unmount();
    client.clear();
  });

  it("stops paging at a cycle and keeps the loaded records", async () => {
    const getHistoryPage = vi
      .spyOn(api, "getReviewHistoryPage")
      .mockResolvedValue({
        entry: { runId: "r1", workspace: workspace("completed") },
        nextRunId: "r1",
        cycleDetected: true,
      });
    const client = testClient();
    const { result, unmount } = renderHook(
      () => useCaseReviewHistory("c1", "r1"),
      { wrapper: testWrapper(client) },
    );
    await waitFor(() => expect(result.current.data).toHaveLength(1));
    expect(result.current.cycleDetected).toBe(true);
    expect(result.current.hasOlder).toBe(false);
    await act(async () => { await result.current.fetchOlder(); });
    expect(getHistoryPage).toHaveBeenCalledTimes(1);
    unmount();
    client.clear();
  });

  it("keeps the latest record when an older page fails", async () => {
    const getHistoryPage = vi
      .spyOn(api, "getReviewHistoryPage")
      .mockResolvedValueOnce({
        entry: { runId: "r1", workspace: workspace("completed") },
        nextRunId: "r0",
        cycleDetected: false,
      })
      .mockRejectedValueOnce(
        new ApiError("更早的审查记录不可读取", 500, "unavailable", true, null),
      );
    const client = testClient();
    const { result, unmount } = renderHook(
      () => useCaseReviewHistory("c1", "r1"),
      { wrapper: testWrapper(client) },
    );
    await waitFor(() => expect(result.current.data).toHaveLength(1));
    await act(async () => {
      await result.current.fetchOlder().catch(() => undefined);
    });
    await waitFor(() => expect(result.current.olderError).not.toBeNull());
    expect(result.current.isError).toBe(false);
    expect(result.current.data?.map((entry) => entry.runId)).toEqual(["r1"]);
    expect(result.current.hasOlder).toBe(true);
    expect(result.current.isLoadingOlder).toBe(false);
    expect(getHistoryPage).toHaveBeenCalledTimes(2);
    unmount();
    client.clear();
  });

  it("validates page identity and detects a self-referencing history", async () => {
    const bodies: Record<string, unknown> = {
      r1: {
        run: { id: "r1", case_id: "c1" },
        audit_events: [],
        previous_run_id: "r0",
      },
      r0: {
        run: { id: "r0", case_id: "c1" },
        audit_events: [],
        previous_run_id: null,
      },
      rc: {
        run: { id: "rc", case_id: "c1" },
        audit_events: [],
        previous_run_id: "rc",
      },
      bad: {
        run: { id: "bad", case_id: "other" },
        audit_events: [],
        previous_run_id: null,
      },
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const runId = new URL(String(input), "http://workflow.test").pathname
          .split("/")
          .at(-1)!;
        return Response.json(bodies[runId]);
      }),
    );
    try {
      const latest = await api.getReviewHistoryPage("c1", "r1", []);
      expect(latest.nextRunId).toBe("r0");
      expect(latest.cycleDetected).toBe(false);
      const older = await api.getReviewHistoryPage("c1", "r0", ["r1"]);
      expect(older.nextRunId).toBeNull();
      expect(older.cycleDetected).toBe(false);
      const cycle = await api.getReviewHistoryPage("c1", "rc", []);
      expect(cycle.cycleDetected).toBe(true);
      expect(cycle.nextRunId).toBeNull();
      await expect(api.getReviewHistoryPage("c1", "bad", [])).rejects.toThrow(
        "审查历史中的案件或审查身份不一致",
      );
      await expect(api.getReviewHistory("c1", "rc")).rejects.toThrow(
        "运行历史包含循环引用",
      );
    } finally {
      vi.unstubAllGlobals();
    }
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
    const actor = {
      id: "rulepack-admin",
      role: "admin",
      audience: "admin",
      tenant_id: "tenant-1",
      org_id: "org-1",
      active_context: { context_version: "7" },
      csrf_token: "rulepack-csrf-token",
    };
    setSessionIdentity(actor);

    const created = {
      pack: {
        id: "pack-1",
        version: "v1",
        tenant_id: "tenant-1",
        status: "draft",
        revision: 1,
        content_hash: "a".repeat(64),
        snapshot_sha256: "b".repeat(64),
        effective_from: "2026-01-01",
        effective_until: "2026-12-31",
        authority_sources: [],
        is_active: false,
        active_pointer_revision: 0,
      },
      manifest: {
        schema_version: "rulepack/v1",
        id: "pack-1",
        version: "v1",
        tenant_id: "tenant-1",
        effective_from: "2026-01-01",
        effective_until: "2026-12-31",
        rules: [],
        lifecycle: { state: "draft" },
      },
    };
    const tested = {
      revision: 1,
      content_hash: "c".repeat(64),
      report: {
        pack_id: "pack-1",
        pack_version: "v1",
        pack_hash: "pack-hash",
        relations: [],
        results: [],
      },
    };
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(jsonResponse(created))
      .mockResolvedValueOnce(jsonResponse(tested));
    const client = testClient();

    const create = renderHook(() => useCreateRulePack(), {
      wrapper: testWrapper(client),
    });
    act(() => {
      create.result.current.mutate({} as never);
    });
    await waitFor(() => expect(create.result.current.isSuccess).toBe(true));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [createUrl, createInit] = fetchMock.mock.calls[0]!;
    expect(String(createUrl)).toBe("/api/admin/rulepacks");
    expect(createInit?.method).toBe("POST");
    const createHeaders = new Headers(createInit?.headers);
    expect(createHeaders.get("X-Context-Version")).toBe(
      actor.active_context.context_version,
    );
    expect(createHeaders.get("X-CSRF-Token")).toBe(actor.csrf_token);
    const createEntry = client.getMutationCache().getAll()[0];
    expect(createEntry?.options.retry).toBe(false);
    create.unmount();
    client.clear();

    const test = renderHook(() => useTestRulePack("pack-1", "v1"), {
      wrapper: testWrapper(client),
    });
    act(() => {
      test.result.current.mutate({} as never);
    });
    await waitFor(() => expect(test.result.current.isSuccess).toBe(true));
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [testUrl, testInit] = fetchMock.mock.calls[1]!;
    expect(String(testUrl)).toBe("/api/admin/rulepacks/pack-1/versions/v1/test");
    expect(testInit?.method).toBe("POST");
    const testHeaders = new Headers(testInit?.headers);
    expect(testHeaders.get("X-Context-Version")).toBe(
      actor.active_context.context_version,
    );
    expect(testHeaders.get("X-CSRF-Token")).toBe(actor.csrf_token);
    const testEntry = client.getMutationCache().getAll()[0];
    expect(testEntry?.options.retry).toBe(false);
    test.unmount();
    client.clear();
  });

  it("sends an expected failing rule pack mutation exactly once", async () => {
    const actor = {
      id: "rulepack-admin",
      role: "admin",
      audience: "admin",
      tenant_id: "tenant-1",
      org_id: "org-1",
      active_context: { context_version: "7" },
      csrf_token: "rulepack-csrf-token",
    };
    setSessionIdentity(actor);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ code: "conflict", message: "版本已变化" }), {
        status: 409,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const client = testClient();
    const create = renderHook(() => useCreateRulePack(), {
      wrapper: testWrapper(client),
    });
    act(() => {
      create.result.current.mutate({} as never);
    });
    await waitFor(() => expect(create.result.current.isError).toBe(true));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(String(url)).toBe("/api/admin/rulepacks");
    expect(init?.method).toBe("POST");
    const headers = new Headers(init?.headers);
    expect(headers.get("X-Context-Version")).toBe(
      actor.active_context.context_version,
    );
    expect(headers.get("X-CSRF-Token")).toBe(actor.csrf_token);
    expect(create.result.current.error).toBeInstanceOf(ApiError);
    expect((create.result.current.error as ApiError).status).toBe(409);
    const entry = client.getMutationCache().getAll()[0];
    expect(entry?.options.retry).toBe(false);
    create.unmount();
    client.clear();
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
    const query = client.getQueryCache().find({
      queryKey: queryKeys.review("c1", "r1"),
    });
    const interval = query?.observers[0]?.options.refetchInterval;
    expect(interval).toBeTypeOf("function");
    if (query && typeof interval === "function") {
      expect(interval(query)).toBe(false);
    }
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

  it("starts the new attempt scope at the base interval", async () => {
    vi.useFakeTimers();
    const getReview = vi.spyOn(api, "getReview")
      .mockImplementation(async () => structuredClone(workspace("running")));
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
    });
    const nextScope = queryKeys.review("c1", "r1", "next");
    const cached = workspace("running");
    client.setQueryData(nextScope, cached);
    const { rerender, unmount } = renderHook(
      ({ attemptId }: { attemptId: string | null }) =>
        useReview("c1", "r1", attemptId),
      { wrapper: testWrapper(client), initialProps: { attemptId: "current" } },
    );
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getReview).toHaveBeenCalledTimes(1);
    rerender({ attemptId: "next" });
    const query = client.getQueryCache().find({ queryKey: nextScope });
    expect(query).toBeDefined();
    if (!query) throw new Error("next attempt query was not created");
    const data = query.state.data;
    if (!data) throw new Error("next attempt query data was not cached");
    client.setQueryData(nextScope, data);
    const interval = query.observers[0]?.options.refetchInterval;
    expect(interval).toBeTypeOf("function");
    if (typeof interval !== "function") throw new Error("polling interval missing");
    expect(interval(query)).toBe(4_000);
    unmount();
    client.clear();
  });

  it("keeps the base delay when an initialData query changes scope", async () => {
    vi.useFakeTimers();
    const initial = workspace("running");
    const client = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          staleTime: Number.POSITIVE_INFINITY,
          initialData: initial,
        },
      },
    });
    const { rerender, unmount } = renderHook(
      ({ attemptId }: { attemptId: string }) =>
        useReview("c1", "r1", attemptId),
      { wrapper: testWrapper(client), initialProps: { attemptId: "current" } },
    );
    const currentKey = queryKeys.review("c1", "r1", "current");
    const currentQuery = client.getQueryCache().find({ queryKey: currentKey });
    expect(currentQuery?.state.dataUpdateCount).toBe(0);

    rerender({ attemptId: "next" });
    const nextKey = queryKeys.review("c1", "r1", "next");
    const nextQuery = client.getQueryCache().find({ queryKey: nextKey });
    expect(nextQuery).toBeDefined();
    if (!nextQuery) throw new Error("initialData query was not created");
    expect(nextQuery.state.dataUpdateCount).toBe(0);
    const nextInterval = nextQuery.observers[0]?.options.refetchInterval;
    expect(nextInterval).toBeTypeOf("function");
    if (typeof nextInterval !== "function") throw new Error("polling interval missing");
    expect(nextInterval(nextQuery)).toBe(2_000);

    client.setQueryData(nextKey, nextQuery.state.data);
    expect(nextQuery.state.dataUpdateCount).toBe(1);
    expect(nextInterval(nextQuery)).toBe(2_000);
    unmount();
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

  it("resets the catch-up delay after an unchanged read during a live stream", async () => {
    vi.useFakeTimers();
    const getReview = vi.spyOn(api, "getReview")
      .mockImplementation(async () => structuredClone(workspace("running")));
    const client = testClient();
    const { rerender, unmount } = renderHook(
      ({ live }) => useReview("c1", "r1", null, live),
      { wrapper: testWrapper(client), initialProps: { live: false } },
    );
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getReview).toHaveBeenCalledTimes(1);
    const query = client.getQueryCache().find({
      queryKey: queryKeys.review("c1", "r1"),
    });
    expect(query).toBeDefined();
    if (!query) throw new Error("review query was not created");

    rerender({ live: true });
    const liveInterval = query.observers[0]?.options.refetchInterval;
    expect(liveInterval).toBeTypeOf("function");
    if (typeof liveInterval !== "function") throw new Error("live polling interval missing");
    expect(liveInterval(query)).toBe(30_000);

    query.state = {
      ...query.state,
      dataUpdateCount: query.state.dataUpdateCount + 1,
    };
    rerender({ live: false });
    const catchUpInterval = query.observers[0]?.options.refetchInterval;
    expect(catchUpInterval).toBeTypeOf("function");
    if (typeof catchUpInterval !== "function") throw new Error("catch-up interval missing");
    expect(catchUpInterval(query)).toBe(2_000);
    unmount();
    client.clear();
  });
});
