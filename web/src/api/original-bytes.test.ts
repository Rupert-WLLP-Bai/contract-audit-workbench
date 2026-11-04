// File metadata (manifest mirror):
// Purpose: Checks authenticated bounded original reads, rejects authorization errors and discards binary responses after a session change.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { afterEach, describe, expect, it, vi } from "vitest";
import { invalidateSessionIdentity, requestBytes } from "./http";

afterEach(() => vi.unstubAllGlobals());

describe("authorized original file reads", () => {
  it("uses the session cookie, disables caching and bounds the response body", async () => {
    const fetch = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(new Uint8Array([1, 2, 3])));
    vi.stubGlobal("fetch", fetch);
    const controller = new AbortController();
    expect(await requestBytes("/api/cases/c/documents/d/original", controller.signal)).toEqual(new Uint8Array([1, 2, 3]));
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({ credentials: "include", cache: "no-store", signal: controller.signal });
    await expect(requestBytes("/original", controller.signal, 2)).rejects.toMatchObject({ code: "original_too_large" });
  });

  it("discards a response after the effective session changes", async () => {
    let resolve!: (response: Response) => void;
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((done) => { resolve = done; })));
    const pending = requestBytes("/original", new AbortController().signal);
    invalidateSessionIdentity();
    resolve(new Response(new Uint8Array([1])));
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
  });

  it("keeps the server authorization error instead of rendering it as an original", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: { message: "没有案件权限", code: "forbidden" } }), { status: 403 })));
    await expect(requestBytes("/original", new AbortController().signal)).rejects.toMatchObject({ status: 403, message: "没有案件权限" });
  });
});
