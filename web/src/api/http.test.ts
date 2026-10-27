import { getKnowledge, submitDecision } from "./endpoints";
import { ApiError, parseErrorPayload, requestJson } from "./http";

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

  it("posts a decision with the expected version and an idempotency key", async () => {
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
