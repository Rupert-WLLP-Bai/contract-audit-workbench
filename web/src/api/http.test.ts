import { getKnowledge, getReviewDiagnostics, startReview, submitDecision } from "./endpoints";
import {
  ApiError,
  bindIdempotencyCommandContext,
  captureCommandContext,
  errorFromResponse,
  errorMessage,
  isConflict,
  isForbidden,
  isUnauthenticated,
  jsonInit,
  parseErrorPayload,
  requestJson,
  setSessionIdentity,
  statusLabel,
} from "./http";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const originalFetch = globalThis.fetch;

function installFetch(fetchMock: unknown) {
  globalThis.fetch = fetchMock as typeof fetch;
}

function mockFetch(
  impl: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>,
) {
  return vi.fn(impl);
}

describe("http client", () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it("reads the error envelope and keeps conflict details", async () => {
    const payload = {
      error: {
        code: "conflict",
        message: "版本冲突",
        retryable: false,
        details: { finding_id: "f1" },
      },
    };
    expect(parseErrorPayload(payload).code).toBe("conflict");
    installFetch(mockFetch(async () => jsonResponse(409, payload)));
    const error = await requestJson(
      "/api/cases",
      { method: "GET" },
      () => null,
    ).catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({
      status: 409,
      code: "conflict",
      message: "版本冲突",
      details: { finding_id: "f1" },
    });
  });

  it("turns a network failure into a retryable error and sends the session cookie", async () => {
    const fetchMock = mockFetch(async () => {
      throw new TypeError("offline");
    });
    installFetch(fetchMock);
    await expect(
      requestJson("/api/session", { method: "GET" }, () => null),
    ).rejects.toMatchObject({
      status: 0,
      code: "network",
      retryable: true,
    });
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({
      credentials: "include",
    });
  });

  it("rejects a knowledge response for a different requested version", async () => {
    installFetch(
      mockFetch(async () =>
        jsonResponse(200, {
          id: "policy",
          version_id: "version-2",
          title: "制度",
          version: "2",
          body: "新版本正文",
          source_type: "reference",
          effective_at: null,
          created_by: "reviewer",
          created_at: "2026-10-27T00:00:00Z",
        }),
      ),
    );
    await expect(getKnowledge("policy", "version-1")).rejects.toMatchObject({
      code: "invalid_response",
      retryable: false,
    });
  });

  it("submits the chosen governed instant and omits it for ungoverned starts", async () => {
    setSessionIdentity({ id: "reviewer", role: "reviewer", active_context: { context_version: "1" }, csrf_token: "csrf" });
    const fetchMock = mockFetch(async () => jsonResponse(202, { id: "r1", attempt_id: "a1" }));
    installFetch(fetchMock);
    await startReview("case", { business_at: "2026-10-21T08:00:00.000Z" });
    await startReview("case");
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ business_at: "2026-10-21T08:00:00.000Z" });
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({});
  });

  it("rejects diagnostics belonging to another case or run", async () => {
    installFetch(mockFetch(async () => jsonResponse(200, {
      case_id: "other", run_id: "r1", execution: null, snapshot: null, audit_events: [],
    })));
    await expect(getReviewDiagnostics("c1", "r1")).rejects.toMatchObject({ code: "invalid_response" });
  });

  it("reads every documented error payload shape", () => {
    expect(parseErrorPayload(null)).toEqual({
      message: null,
      code: null,
      retryable: null,
      details: null,
    });
    expect(parseErrorPayload("text")).toEqual({
      message: null,
      code: null,
      retryable: null,
      details: null,
    });
    expect(parseErrorPayload({ error: "直接消息" })).toEqual({
      message: "直接消息",
      code: null,
      retryable: null,
      details: null,
    });
    expect(
      parseErrorPayload({
        message: "顶层消息",
        code: "top",
        retryable: true,
        details: { a: 1 },
      }),
    ).toEqual({
      message: "顶层消息",
      code: "top",
      retryable: true,
      details: { a: 1 },
    });
    expect(
      parseErrorPayload({
        error: "直接消息",
        message: "顶层消息",
        error_code: "ignored",
        error_details: "ignored",
      }),
    ).toMatchObject({ message: "顶层消息" });
    expect(
      parseErrorPayload({
        error: {
          message: "嵌套消息",
          code: "nested",
          retryable: false,
          details: null,
        },
      }),
    ).toEqual({
      message: "嵌套消息",
      code: "nested",
      retryable: false,
      details: null,
    });
    expect(parseErrorPayload({ error: { message: 7 } })).toEqual({
      message: null,
      code: null,
      retryable: null,
      details: null,
    });
    expect(
      parseErrorPayload({ error: { message: "x".repeat(400) } }).message,
    ).toHaveLength(300);
    expect(parseErrorPayload({ code: 7, retryable: "yes" })).toEqual({
      message: null,
      code: null,
      retryable: null,
      details: null,
    });
    expect(
      parseErrorPayload({ error: "直接消息", message: "" }).message,
    ).toBe("");
    expect(parseErrorPayload({ details: false }).details).toBe(false);
    expect(parseErrorPayload({ details: 0 }).details).toBe(0);
  });

  it("builds default errors per HTTP status and prefers the payload", async () => {
    const statuses = [
      [401, "unauthenticated", "登录已失效或账号密码不正确"],
      [403, "forbidden", "没有访问该内容的权限"],
      [404, "not_found", "没有找到对应记录"],
      [409, "conflict", "该事项已被他人更新，请按最新内容重新确认"],
      [418, "request_failed", "审查服务暂时不能完成这个操作"],
    ] as const;
    for (const [status, code, message] of statuses) {
      const error = await errorFromResponse(
        new Response("not json", { status }),
      );
      expect(error).toMatchObject({
        status,
        code,
        message,
        retryable: false,
        details: null,
      });
    }
    const network = await errorFromResponse(new Response("", { status: 0 }));
    expect(network).toMatchObject({
      status: 0,
      code: "network",
      message: "无法连接审查服务",
    });
    const blank = await errorFromResponse(
      Response.json(
        { error: { message: "   ", code: "custom", retryable: true, details: [1] } },
        { status: 409 },
      ),
    );
    expect(blank).toMatchObject({
      message: "该事项已被他人更新，请按最新内容重新确认",
      code: "custom",
      retryable: true,
      details: [1],
    });
    const emptyCode = await errorFromResponse(
      Response.json({ error: { code: "" } }, { status: 404 }),
    );
    expect(emptyCode.code).toBe("");
  });

  it("classifies errors without throwing on unknown values", () => {
    expect(isUnauthenticated(new ApiError("x", 401, "other", false, null))).toBe(
      true,
    );
    expect(isUnauthenticated(new ApiError("x", 400, "unauthenticated", false, null))).toBe(
      true,
    );
    expect(isUnauthenticated(new Error("x"))).toBe(false);
    expect(isUnauthenticated({ status: 401 })).toBe(false);
    expect(isUnauthenticated({ code: "unauthenticated" })).toBe(false);
    expect(isConflict(new ApiError("x", 409, "other", false, null))).toBe(true);
    expect(isConflict(new ApiError("x", 400, "conflict", false, null))).toBe(true);
    expect(isConflict(null)).toBe(false);
    expect(isForbidden(new ApiError("x", 403, "other", false, null))).toBe(true);
    expect(isForbidden(new ApiError("x", 400, "forbidden", false, null))).toBe(true);
    expect(isForbidden({ status: 403 })).toBe(false);
    expect(errorMessage(new ApiError("消息", 500, "code", false, null))).toBe(
      "消息",
    );
    expect(errorMessage(new Error("消息"))).toBe("请求没有完成");
    expect(statusLabel(0)).toBe("无法连接服务");
    expect(statusLabel(500)).toBe("HTTP 500");
  });

  it("tracks session identity changes and builds JSON inits", () => {
    setSessionIdentity(null);
    expect(setSessionIdentity(null)).toBe(false);
    expect(setSessionIdentity({ id: "u1", role: "reviewer" })).toBe(true);
    expect(setSessionIdentity({ id: "u1", role: "reviewer" })).toBe(false);
    expect(
      setSessionIdentity({
        id: "u1",
        role: "reviewer",
        audience: "admin",
        tenant_id: "t1",
        org_id: "o1",
      }),
    ).toBe(true);
    setSessionIdentity(null);
    expect(jsonInit("POST", { a: 1 })).toEqual({
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: '{"a":1}',
    });
    expect(jsonInit("GET")).toEqual({
      method: "GET",
      headers: {},
      body: null,
    });
    expect(jsonInit("PUT", undefined, { Accept: "text/plain" })).toEqual({
      method: "PUT",
      headers: { Accept: "text/plain" },
      body: null,
    });
  });

  it("passes headers, 204 and empty bodies through requestJson", async () => {
    const fetchMock = mockFetch(async () => new Response(null, { status: 204 }));
    installFetch(fetchMock);
    await expect(
      requestJson("/api/cases", { method: "GET", headers: { "X-Trace": "1" } }, (value) => value),
    ).resolves.toBeNull();
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({
      credentials: "include",
      cache: "no-store",
      headers: { Accept: "application/json", "X-Trace": "1" },
    });
    installFetch(mockFetch(async () => new Response("   ", { status: 200 })));
    await expect(requestJson("/api/cases", {}, (value) => value)).resolves.toBeNull();
  });

  it("rethrows abort errors but converts unknown fetch failures", async () => {
    installFetch(
      mockFetch(async () => {
        throw new DOMException("aborted", "AbortError");
      }),
    );
    await expect(
      requestJson("/api/cases", {}, () => null),
    ).rejects.toMatchObject({ name: "AbortError" });
    installFetch(
      mockFetch(async () => {
        throw new Error("socket");
      }),
    );
    await expect(
      requestJson("/api/cases", {}, () => null),
    ).rejects.toMatchObject({ status: 0, code: "network", retryable: true });
  });

  it("reports unrecognisable response content and keeps parser errors", async () => {
    installFetch(mockFetch(async () => new Response("{", { status: 200 })));
    await expect(
      requestJson("/api/cases", {}, () => null),
    ).rejects.toMatchObject({
      status: 200,
      code: "invalid_response",
      retryable: false,
    });
    installFetch(mockFetch(async () => jsonResponse(200, { a: 1 })));
    const parserError = new ApiError("自定义", 200, "invalid_response", false, null);
    await expect(
      requestJson("/api/cases", {}, () => {
        throw parserError;
      }),
    ).rejects.toBe(parserError);
  });

  it("aborts a non-session request when the identity changes during fetch", async () => {
    setSessionIdentity({ id: "u1", role: "reviewer" });
    installFetch(
      mockFetch(async () => {
        setSessionIdentity({ id: "u2", role: "reviewer" });
        return jsonResponse(200, {});
      }),
    );
    await expect(
      requestJson("/api/cases", {}, () => null),
    ).rejects.toMatchObject({ name: "AbortError" });
    setSessionIdentity({ id: "u3", role: "reviewer" });
    const fakeResponse = {
      ok: true,
      status: 200,
      text: async () => {
        setSessionIdentity({ id: "u4", role: "reviewer" });
        return "{}";
      },
      json: async () => ({}),
    } as Response;
    installFetch(mockFetch(async () => fakeResponse));
    await expect(
      requestJson("/api/cases", {}, () => null),
    ).rejects.toMatchObject({ name: "AbortError" });
    setSessionIdentity({ id: "u5", role: "reviewer" });
    installFetch(
      mockFetch(async () => {
        setSessionIdentity({ id: "u6", role: "reviewer" });
        return jsonResponse(200, { ok: true });
      }),
    );
    await expect(
      requestJson("/api/session", {}, (value) => value),
    ).resolves.toEqual({ ok: true });
    setSessionIdentity(null);
  });

  it("rejects a delayed idempotent dispatch after a version-1 identity switch", async () => {
    const actorA = {
      id: "actor-a", role: "clerk", audience: "client", tenant_id: "tenant-a", org_id: "org-a",
      principal_id: "principal-a", active_context: { context_version: "1" }, csrf_token: "csrf-a",
    };
    const actorB = {
      ...actorA, id: "actor-b", tenant_id: "tenant-b", org_id: "org-b", principal_id: "principal-b", csrf_token: "csrf-b",
    };
    setSessionIdentity(actorA);
    const originalContext = captureCommandContext(actorA);
    const key = "delayed-command-key";
    bindIdempotencyCommandContext(key, originalContext);

    const fetchMock = mockFetch(async () => jsonResponse(200, { ok: true }));
    installFetch(fetchMock);
    setSessionIdentity(actorB);

    await expect(requestJson(
      "/api/cases/case-1/materials",
      { method: "POST", headers: { "Idempotency-Key": key }, body: "{\"old\":true}" },
      (value) => value,
    )).rejects.toMatchObject({ name: "AbortError" });
    expect(fetchMock).not.toHaveBeenCalled();
    setSessionIdentity(null);
  });

  it("posts a decision with the expected version and an idempotency key", async () => {
    setSessionIdentity({ id: "reviewer", role: "reviewer", active_context: { context_version: "1" }, csrf_token: "csrf" });
    const fetchMock = mockFetch(
      async () => new Response(null, { status: 204 }),
    );
    installFetch(fetchMock);
    await submitDecision("case", "run", "finding", {
      action: "false_positive",
      reason: "条款一致",
      expectedVersion: 3,
    });
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit;
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "/api/cases/case/reviews/run/findings/finding/decisions",
    );
    expect(JSON.parse(String(init.body))).toEqual({
      action: "false_positive",
      reason: "条款一致",
      expected_version: 3,
    });
    const headers = init.headers as Record<string, string>;
    expect(headers["Idempotency-Key"]).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
    );
  });
});
