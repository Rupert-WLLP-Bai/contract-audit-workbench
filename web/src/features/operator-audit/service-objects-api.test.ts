import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  confirmServiceObjectAssociation,
  createServiceObject,
  parseAssociationPreview,
  parseServiceObjectAssociationPage,
  parseServiceObjectDetail,
  parseServiceObjectList,
  parseServiceObjectMutationResult,
  parseServiceObjectVersionPage,
  serviceObjectPaths,
} from "./service-objects-api";

function fixture(): Record<string, unknown> {
  return JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/contracts/service-objects/wire.json"), "utf8")) as Record<string, unknown>;
}

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

afterEach(() => vi.unstubAllGlobals());

describe("service object API", () => {
  it("parses Go-marshaled list, detail, preview, version, and association projections", () => {
    const raw = fixture();
    expect(parseServiceObjectList(raw.summary_page)).toMatchObject({
      items: [{ service_object_id: "svc-wire-001", current_version: 2, can_manage: true }],
      next_cursor: null,
    });
    expect(parseServiceObjectDetail(raw.detail)).toMatchObject({
      service_object_id: "svc-wire-001",
      association: { association_version: 1, material_id: "material-wire-1", document_id: "doc-wire-1" },
    });
    expect(parseAssociationPreview(raw.preview)).toMatchObject({
      service_object_id: "svc-wire-001",
      expected_object_version: 2,
      expected_association_version: 1,
      options: [{ object_ref: "LINE-A", facts: [{ fact: { dimension: "minimum_bandwidth" } }] }],
    });
    expect(parseServiceObjectVersionPage(raw.version_page).items[0]?.version).toBe(2);
    expect(parseServiceObjectAssociationPage(raw.association_page).items[0]?.association_version).toBe(1);
    expect(parseServiceObjectMutationResult(raw.mutation_result)).toEqual({ service_object_id: "svc-wire-001", version: 2, association_version: 1 });
  });

  it("rejects invalid digest, lifecycle state, and permission values", () => {
    const raw = fixture();
    const list = raw.summary_page as { items: Array<Record<string, unknown>>; next_cursor: null };
    expect(() => parseServiceObjectList({ ...raw.summary_page as object, items: [{ ...list.items[0], can_manage: "true" }] })).toThrow(/can_manage/);
    expect(() => parseServiceObjectList({ ...raw.summary_page as object, can_manage: false })).toThrow(/权限与项目权限不一致/);
    expect(() => parseServiceObjectList({ ...raw.summary_page as object, items: [{ ...list.items[0], state: "probably_confirmed" }] })).toThrow(/state 值无效/);
    const detail = raw.detail as { association: Record<string, unknown> };
    expect(() => parseServiceObjectDetail({ ...raw.detail as object, association: { ...detail.association, facts_sha256: "bad" } })).toThrow(/SHA-256/);
  });

  it("keeps all service-object routes case and object scoped", () => {
    const source = { run_id: "run/1", attempt_id: "attempt 1", material_id: "mat/1", material_version: 3, document_id: "doc/1", parsed_document_id: "parse/1" };
    expect(serviceObjectPaths.list("case/1")).toBe("/api/cases/case%2F1/service-objects?limit=20");
    expect(serviceObjectPaths.detail("case/1", "svc/1")).toBe("/api/cases/case%2F1/service-objects/svc%2F1");
    expect(serviceObjectPaths.candidates("case/1", "svc/1", source)).toContain("run_id=run%2F1");
    expect(serviceObjectPaths.candidates("case/1", "svc/1", source)).toContain("parsed_document_id=parse%2F1");
  });

  it("creates and confirms with exact server CAS data and idempotency headers", async () => {
    const raw = fixture();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response(raw.mutation_result, 201))
      .mockResolvedValueOnce(response(raw.mutation_result, 201));
    vi.stubGlobal("fetch", fetchMock);
    const created = await createServiceObject("case/1", {
      object_type: "dedicated_line", display_name: "专线 A", aliases: [{ kind: "circuit", value: "LINE-A" }],
    }, "create-key");
    const confirmed = await confirmServiceObjectAssociation("case/1", "svc-wire-001", {
      confirmed: true,
      expected_object_version: 1,
      expected_association_version: 0,
      source: { run_id: "run-1", attempt_id: "attempt-1", material_id: "mat-1", material_version: 1, document_id: "doc-1", parsed_document_id: "parse-1" },
      object_ref: "LINE-A", facts_sha256: "d".repeat(64), candidate_sha256: "f".repeat(64),
    }, "confirm-key");
    expect(created.service_object_id).toBe("svc-wire-001");
    expect(confirmed.association_version).toBe(1);
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toEqual([
      "/api/cases/case%2F1/service-objects",
      "/api/cases/case%2F1/service-objects/svc-wire-001/associations",
    ]);
    expect(fetchMock.mock.calls[0]?.[1]?.headers).toMatchObject({ "Idempotency-Key": "create-key" });
    expect(fetchMock.mock.calls[1]?.[1]?.headers).toMatchObject({ "Idempotency-Key": "confirm-key" });
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toMatchObject({
      expected_object_version: 1, expected_association_version: 0, object_ref: "LINE-A", confirmed: true,
    });
  });
});
