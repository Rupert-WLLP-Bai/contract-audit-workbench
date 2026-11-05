// File metadata (manifest mirror):
// Purpose: Checks saved-view and export client requests against the registered ledger route contract, including methods, encoded identities, payloads and valid command contexts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-05
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  checkContractReferences, createContractExport, createContractSavedView, deleteContractSavedView, downloadContractExport,
  listContractLedger, listContractSavedViews, replaceContractSavedView, type ContractExportRequest, type ContractLedgerColumn,
} from "./contracts";
import { captureCommandContext, setSessionIdentity, type CommandContext } from "./http";
import { idempotencyActorScope, pendingMutationKey } from "./idempotency";

const actor = {
  id: "synthetic-admin", role: "admin", audience: "client", tenant_id: "tenant-1", org_id: "org-1",
  active_context: { context_version: "17" }, csrf_token: "synthetic-csrf-token",
};
const viewID = "11111111-1111-4111-8111-111111111111";
const exportID = "22222222-2222-4222-8222-222222222222";
const at = "2026-11-05T00:00:00Z";
const filter = { text: "设备采购", contract_state: "draft" };
const columns: ContractLedgerColumn[] = ["contract_name", "contract_state"];
const view = { view_id: viewID, name: "设备视图", filter, columns, row_version: 7, created_at: at, updated_at: at };
const exportPayload: ContractExportRequest = {
  filter, columns: ["contract_name", "contract_state_label", "contract_state_code"], format: "csv",
};
const exported = {
  export_id: exportID, state: "ready", format: "csv", columns: exportPayload.columns, row_count: 1,
  created_at: at, expires_at: "2026-11-06T00:00:00Z", download_available: true, replayed: false,
};
const csv = new Uint8Array([0xef, 0xbb, 0xbf, ...new TextEncoder().encode("合同名称,合同状态名称,合同状态代码\n设备合同,草稿,draft\n")]);

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

beforeEach(() => {
  sessionStorage.clear();
  setSessionIdentity(null);
  setSessionIdentity(actor);
});

afterEach(() => {
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("contract ledger API routes", () => {
  const cases: Array<{
    name: string;
    path: string;
    method: string;
    body?: unknown;
    response: () => Response;
    invoke: (key: string, context: CommandContext) => Promise<unknown>;
  }> = [
    { name: "list views", path: "/api/contracts/ledger/views", method: "GET", response: () => json({ items: [view] }),
      invoke: () => listContractSavedViews() },
    { name: "create view", path: "/api/contracts/ledger/views", method: "POST", body: { name: view.name, filter, columns },
      response: () => json(view, 201), invoke: () => createContractSavedView(view.name, filter, columns) },
    { name: "replace view", path: `/api/contracts/ledger/views/${viewID}`, method: "POST",
      body: { expected_row_version: 7, name: view.name, filter, columns }, response: () => json({ ...view, row_version: 8 }),
      invoke: () => replaceContractSavedView(viewID, 7, view.name, filter, columns) },
    { name: "delete view", path: `/api/contracts/ledger/views/${viewID}?expected_row_version=7`, method: "DELETE",
      response: () => json({ deleted: true }), invoke: () => deleteContractSavedView(viewID, 7) },
    { name: "create export", path: "/api/contracts/ledger/exports", method: "POST", body: exportPayload,
      response: () => json(exported, 201), invoke: (key) => createContractExport(exportPayload, key) },
    { name: "download export", path: `/api/contracts/ledger/exports/${exportID}/download`, method: "GET",
      response: () => new Response(csv, { status: 200, headers: { "Content-Type": "text/csv; charset=utf-8" } }),
      invoke: (_, context) => downloadContractExport(exportID, context) },
  ];

  it.each(cases)("sends $name to the registered method and URL", async (testCase) => {
    const context = captureCommandContext(actor);
    const pending = await pendingMutationKey("clm-contract-export", idempotencyActorScope(actor), "ledger", exportPayload, context);
    const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) =>
      String(url) === testCase.path && init?.method === testCase.method ? testCase.response() : json({ error: "not found" }, 404));
    vi.stubGlobal("fetch", fetch);
    const result = await testCase.invoke(pending.key, context).then((value) => ({ value }), (error: unknown) => ({ error }));
    // Assert an actual network call before inspecting its route: a session preflight failure cannot pass.
    expect(fetch).toHaveBeenCalledTimes(1);
    const [url, init] = fetch.mock.calls[0]!;
    expect(String(url)).toBe(testCase.path);
    expect(init?.method).toBe(testCase.method);
    expect(init?.credentials).toBe("include");
    expect(init?.cache).toBe("no-store");
    const headers = new Headers(init?.headers);
    if (testCase.method !== "GET" || testCase.name === "download export") {
      expect(headers.get("X-Context-Version")).toBe(actor.active_context.context_version);
      expect(headers.get("X-CSRF-Token")).toBe(actor.csrf_token);
    }
    if (testCase.body !== undefined) {
      expect(headers.get("Content-Type")).toContain("application/json");
      expect(JSON.parse(String(init?.body))).toEqual(testCase.body);
    }
    if (testCase.name === "create export") expect(headers.get("Idempotency-Key")).toBe(pending.key);
    expect(result).toHaveProperty("value");
    if (testCase.name === "list views") expect("value" in result && result.value).toEqual([view]);
    if (testCase.name === "create view") expect("value" in result && result.value).toEqual(view);
    if (testCase.name === "replace view") expect("value" in result && result.value).toEqual({ ...view, row_version: 8 });
    if (testCase.name === "create export") expect("value" in result && result.value).toEqual(exported);
    if (testCase.name === "download export" && "value" in result) {
      expect(Array.from(new Uint8Array(await (result.value as Blob).arrayBuffer()))).toEqual(Array.from(csv));
    }
  });

  it("encodes identifiers before requesting and rejects a non-existent view and export", async () => {
    const fetch = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => json({ error: "not found" }, 404));
    vi.stubGlobal("fetch", fetch);
    await expect(replaceContractSavedView("id/中?#", 7, view.name, filter, columns)).rejects.toMatchObject({ status: 404 });
    await expect(downloadContractExport("id/中?#", captureCommandContext(actor))).rejects.toMatchObject({ status: 404 });
    expect(String(fetch.mock.calls[0]?.[0])).toBe("/api/contracts/ledger/views/id%2F%E4%B8%AD%3F%23");
    expect(String(fetch.mock.calls[1]?.[0])).toBe("/api/contracts/ledger/exports/id%2F%E4%B8%AD%3F%23/download");
  });

  it("keeps the ledger list cursor and reference-check query on their existing routes", async () => {
    const ledgerURL = "/api/contracts?text=%E8%AE%BE%E5%A4%87+%2F%3F&contract_state=draft&cursor=cursor%2B%2F%3D&limit=25";
    const referenceURL = `/api/contracts/${viewID}/reference-checks?case_id=%E6%A1%88%E4%BB%B6%20%2F%3F`;
    const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      if (String(url) === ledgerURL && init?.method === "GET") return json({ items: [], next_cursor: null, has_more: false });
      if (String(url) === referenceURL && init?.method === "GET") return json({ error: "not found" }, 404);
      return json({ error: "wrong path" }, 500);
    });
    vi.stubGlobal("fetch", fetch);
    await expect(listContractLedger({ text: "设备 /?", contract_state: "draft" }, "cursor+/=", 25))
      .resolves.toEqual({ items: [], next_cursor: null, has_more: false });
    await expect(checkContractReferences(viewID, "案件 /?")).rejects.toMatchObject({ status: 404 });
    expect(fetch.mock.calls.map(([url]) => String(url))).toEqual([ledgerURL, referenceURL]);
  });

  it("rejects an expired or revoked export without returning CSV", async () => {
    const path = `/api/contracts/ledger/exports/${exportID}/download`;
    const fetch = vi.fn(async (url: RequestInfo | URL) => String(url) === path ? json({ error: "not found" }, 404) : json({ error: "wrong path" }, 500));
    vi.stubGlobal("fetch", fetch);
    await expect(downloadContractExport(exportID, captureCommandContext(actor))).rejects.toMatchObject({ status: 404 });
    expect(String(fetch.mock.calls[0]?.[0])).toBe(path);
  });

  it("does not send an unbound idempotency key or a stale-context request", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    await expect(createContractExport(exportPayload, "unbound-idempotency-key")).rejects.toMatchObject({ name: "AbortError" });
    const context = captureCommandContext(actor);
    const pending = await pendingMutationKey("clm-contract-export", idempotencyActorScope(actor), "ledger", exportPayload, context);
    setSessionIdentity({ ...actor, id: "another-user" });
    await expect(createContractExport(exportPayload, pending.key)).rejects.toMatchObject({ name: "AbortError" });
    await expect(downloadContractExport(exportID, context)).rejects.toMatchObject({ name: "AbortError" });
    expect(fetch).not.toHaveBeenCalled();
  });

  it("discards an in-flight download when the context changes", async () => {
    let finish!: (response: Response) => void;
    const fetch = vi.fn((_url: RequestInfo | URL, _init?: RequestInit) => new Promise<Response>((resolve) => { finish = resolve; }));
    vi.stubGlobal("fetch", fetch);
    const download = downloadContractExport(exportID, captureCommandContext(actor));
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(String(fetch.mock.calls[0]?.[0])).toBe(`/api/contracts/ledger/exports/${exportID}/download`);
    setSessionIdentity({ ...actor, active_context: { context_version: "18" } });
    finish(new Response(csv, { status: 200 }));
    await expect(download).rejects.toMatchObject({ name: "AbortError" });
  });
});
