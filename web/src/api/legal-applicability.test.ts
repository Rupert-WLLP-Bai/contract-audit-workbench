import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getLegalApplicabilityFactHistory, getLegalApplicabilityFacts, parseLegalApplicabilityFactHistory, parseLegalApplicabilityFactSet, parseLegalApplicabilityFactVersion, recordLegalApplicabilityFact, type LegalApplicabilityFactInput } from "./legal-applicability";

function fixture(name: string): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/contracts/legal", name), "utf8")) as unknown;
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

afterEach(() => vi.unstubAllGlobals());

describe("legal applicability fact API", () => {
  it("parses Go-marshaled fact resolution and append-only version fixtures", () => {
    const facts = parseLegalApplicabilityFactSet(fixture("applicability-fact-set.json"));
    const current = facts.facts.find((item) => item.dimension === "buyer_kind");
    expect(facts).toMatchObject({ business_at: "2025-05-20T12:00:00Z", evidence_cutoff_at: "2026-10-31T00:00:00.123456Z" });
    expect(current?.fact).toMatchObject({ dimension: "buyer_kind", status: "established", value: "enterprise", version: 2 });
    expect(current?.fact?.unknown_reason).toBe("");
    expect(parseLegalApplicabilityFactVersion(fixture("applicability-record-success.json"))).toMatchObject({
      dimension: "buyer_kind", version: 1, temporal_basis: "historical_interval",
    });
    expect(parseLegalApplicabilityFactSet(fixture("applicability-fact-set.json")).facts.find((item) => item.dimension === "project_kind")?.fact?.party).toBeNull();
    expect(() => parseLegalApplicabilityFactSet({ ...fixture("applicability-fact-set.json") as object, business_at: "2025-05-20T12:00:00+08:00" })).toThrow(/UTC/);
  });

  it("rejects contradictory resolutions, uncontrolled established values, and missing party pins", () => {
    type RawFact = Record<string, unknown>;
    type RawResolved = { dimension: string; status: string; value?: string; unknown_reason?: string; fact?: RawFact | null };
    const source = fixture("applicability-fact-set.json");

    const unknownWithValue = structuredClone(source) as { facts: RawResolved[] };
    unknownWithValue.facts[2]!.value = "enterprise";
    expect(() => parseLegalApplicabilityFactSet(unknownWithValue)).toThrow(/状态字段不匹配/);

    const establishedWithReason = structuredClone(source) as { facts: RawResolved[] };
    establishedWithReason.facts[0]!.unknown_reason = "missing_fact";
    expect(() => parseLegalApplicabilityFactSet(establishedWithReason)).toThrow(/状态字段不匹配/);

    const mismatchedDimension = structuredClone(source) as { facts: RawResolved[] };
    const mismatchedFact = mismatchedDimension.facts[0]!.fact!;
    mismatchedDimension.facts[0]!.fact = {
      ...mismatchedFact,
      dimension: "seller_kind",
      party: { ...(mismatchedFact.party as Record<string, unknown>), side: "seller" },
    };
    expect(() => parseLegalApplicabilityFactSet(mismatchedDimension)).toThrow(/状态字段不匹配/);

    const uncontrolledValue = structuredClone(fixture("applicability-record-success.json")) as RawFact;
    uncontrolledValue.value = "corporation";
    expect(() => parseLegalApplicabilityFactVersion(uncontrolledValue)).toThrow(/状态和值不匹配/);

    const noPartyPin = structuredClone(fixture("applicability-record-success.json")) as RawFact;
    noPartyPin.party = {};
    expect(() => parseLegalApplicabilityFactVersion(noPartyPin)).toThrow(/主体固定引用/);
  });

  it("parses paged immutable history and its authoritative CAS head from the Go fixture", async () => {
    const raw = fixture("applicability-history.json");
    const history = parseLegalApplicabilityFactHistory(raw);
    expect(history).toMatchObject({ case_id: "case_wire_fixture", dimension: "buyer_kind", current_version: 2, can_manage: true });
    expect(history.items[0]).toMatchObject({ version: 2, fact_id: "laf_wire_buyer_kind" });
    expect(history.next_cursor).toBe("cursor_wire_fixture_opaque");
    expect(() => parseLegalApplicabilityFactHistory({ ...raw as object, can_manage: "true" })).toThrow(/can_manage/);
    const fetchMock = vi.fn().mockResolvedValue(json(raw));
    vi.stubGlobal("fetch", fetchMock);
    const result = await getLegalApplicabilityFactHistory("case/1", "buyer_kind", "cursor/+", new AbortController().signal);
    expect(result.current_version).toBe(2);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/cases/case%2F1/legal-applicability-facts/buyer_kind/history?cursor=cursor%2F%2B&limit=20");
  });

  it("reads the selected business-time resolution and writes with the server CAS and idempotency contract", async () => {
    const factSet = fixture("applicability-fact-set.json");
    const saved = fixture("applicability-record-success.json");
    const fetchMock = vi.fn().mockResolvedValueOnce(json(factSet)).mockResolvedValueOnce(json(saved, 201));
    vi.stubGlobal("fetch", fetchMock);
    const result = await getLegalApplicabilityFacts("case/1", "2025-05-20T12:00:00Z", "2026-10-31T00:00:00Z");
    expect(result.facts).toHaveLength(3);
    const input: LegalApplicabilityFactInput = {
      expected_version: 0, dimension: "buyer_kind", status: "established", value: "enterprise",
      temporal_basis: "historical_interval", valid_from: "2025-05-01T00:00:00+08:00", valid_until: "2025-06-01T00:00:00+08:00",
      basis_note: "fixture-only evidence; not a legal quotation", basis_refs: [{
        kind: "material_anchor", material_id: "material_fixture", material_version: 1,
        document_id: "document_fixture", parsed_document_id: "parsed_fixture", start: 0, end: 21,
        quote_digest: "c6eeda9036a5d24d30d7e95e66b0876c5853ff91298b7d4093446fa3f6d53626",
      }],
      party: { side: "buyer", binding_id: "binding_fixture", binding_version: 2, subject_id: "subject_fixture", subject_version: 3, scope_note: "fixture buyer role" },
    };
    const created = await recordLegalApplicabilityFact("case/1", input, "stable-fact-key");
    expect(created.version).toBe(1);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/cases/case%2F1/legal-applicability-facts?business_at=2025-05-20T12%3A00%3A00Z&evidence_cutoff_at=2026-10-31T00%3A00%3A00Z");
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe("/api/cases/case%2F1/legal-applicability-facts/buyer_kind");
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).get("Idempotency-Key")).toBe("stable-fact-key");
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toMatchObject({ ...input, idempotency_key: "stable-fact-key" });
  });
});
