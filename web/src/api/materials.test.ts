import { afterEach, describe, expect, it, vi } from "vitest";
import { confirmLegacyMaterialSelection, createGeneration, uploadDocument } from "./endpoints";

function jsonResponse(value: unknown, status = 201): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => vi.restoreAllMocks());

describe("material API contract", () => {
  it("sends an explicit add intent and parses the server material/version identity", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ id: "doc-new", material_id: "mat-new", version: 1, replayed: false }),
    );
    const inputFile = new File(["body"], "supplement.txt", { type: "text/plain" });
    await expect(uploadDocument("case/one", {
      file: inputFile,
      role: "attachment",
      purpose: "supplement",
      intent: "add",
      idempotency_key: "retry-key-1",
    })).resolves.toEqual({ id: "doc-new", material_id: "mat-new", version: 1, replayed: false });
    const [url, init] = fetch.mock.calls[0]!;
    expect(String(url)).toBe("/api/cases/case%2Fone/documents");
    expect(new Headers(init?.headers).get("Idempotency-Key")).toBe("retry-key-1");
    const body = init?.body as FormData;
    expect(body.get("intent")).toBe("add");
    expect(body.get("role")).toBe("attachment");
    expect(body.get("purpose")).toBe("supplement");
    expect(body.get("file")).toBe(inputFile);
    expect(body.has("material_id")).toBe(false);
    expect(body.has("replaces_document_id")).toBe(false);
  });

  it("sends only a chosen material and current-document CAS target for replacement", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ id: "doc-v2", material_id: "mat-1", version: 2, replayed: false }),
    );
    await uploadDocument("case-1", {
      file: new File(["updated"], "contract.txt", { type: "text/plain" }),
      role: "draft_contract",
      intent: "replace",
      material_id: "mat-1",
      replaces_document_id: "doc-v1",
      idempotency_key: "cas-key",
    });
    const init = fetch.mock.calls[0]![1];
    const body = init?.body as FormData;
    expect(body.get("intent")).toBe("replace");
    expect(body.get("material_id")).toBe("mat-1");
    expect(body.get("replaces_document_id")).toBe("doc-v1");
    expect(new Headers(init?.headers).get("Idempotency-Key")).toBe("cas-key");
  });

  it("confirms an explicit selection using the live route and selection version in the body", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ version: 4, status: "confirmed", document_ids: ["doc-a", "doc-b"], candidate_document_ids: [], primary_material_id: null }, 200),
    );
    const result = await confirmLegacyMaterialSelection("case-1", { version: 4, document_ids: ["doc-a", "doc-b"] });
    expect(result).toMatchObject({ status: "confirmed", document_ids: ["doc-a", "doc-b"] });
    const [url, init] = fetch.mock.calls[0]!;
    expect(String(url)).toBe("/api/cases/case-1/materials/selection");
    expect(JSON.parse(String(init?.body))).toEqual({ version: 4, document_ids: ["doc-a", "doc-b"] });
    expect(new Headers(init?.headers).has("If-Match")).toBe(false);
    expect(new Headers(init?.headers).has("Idempotency-Key")).toBe(false);
  });

  it("sends explicit template replacement and the stable key in the request header", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ id: "generation-1", document_id: "doc-v2", template_id: "template-1", template_version: "2", digest: "digest", created_at: "2026-10-31T00:00:00Z" }),
    );
    await createGeneration("case-1", {
      template_id: "template-1",
      fields: { title: "服务合同" },
      intent: "replace",
      material_id: "mat-primary",
      replaces_document_id: "doc-v1",
    }, "generation-key");
    const init = fetch.mock.calls[0]![1];
    expect(JSON.parse(String(init?.body))).toMatchObject({
      intent: "replace", material_id: "mat-primary", replaces_document_id: "doc-v1",
    });
    expect(new Headers(init?.headers).get("Idempotency-Key")).toBe("generation-key");
  });
});
