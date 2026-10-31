import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getFrozenLegalAuthority, parseFrozenLegalAuthority, parseFrozenLegalReviewProjection } from "./legal-citations";
import { parseReviewWorkspace } from "./parse";

function fixture(): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/contracts/legal/frozen-legal-authorities.json"), "utf8")) as unknown;
}

function response(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}

afterEach(() => vi.unstubAllGlobals());

describe("frozen legal citation API", () => {
  it("parses the exact Go workspace projection and preserves NOT_RUN review state", () => {
    const projection = parseFrozenLegalReviewProjection(fixture());
    expect(projection).toMatchObject({ run_id: "run_wire_fixture", attempt_id: "attempt_wire_fixture", evidence_cutoff_mode: "acceptance_time" });
    expect(projection.authorities[0]).toMatchObject({
      citation_id: expect.stringMatching(/^[a-f0-9]{64}$/),
      outcome: "applicable",
      legal_review_status: "NOT_RUN",
      reviewer: null,
      missing_dependencies: [],
    });
    expect(projection.authorities[0]?.article?.norm_title).toBe("法律结构 fixture");
  });

  it("rejects a citation without a digest or a legal body unless it is a legacy gap", () => {
    const raw = fixture() as { authorities: unknown[] };
    const authority = raw.authorities[0] as Record<string, unknown>;
    expect(authority).toBeDefined();
    expect(() => parseFrozenLegalAuthority({ ...authority, citation_id: "not-a-digest" })).toThrow(/SHA-256/);
    expect(() => parseFrozenLegalAuthority({ ...authority, article: undefined })).toThrow(/缺少精确条文/);
    expect(parseFrozenLegalAuthority({ ...authority, article: undefined, legacy_rule_gap: true })).toMatchObject({ legacy_rule_gap: true });
    expect(() => parseFrozenLegalAuthority({ ...authority, legal_review_status: "approved" })).toThrow(/不得伪称已人工审核/);
  });

  it("reads only the requested case, run, attempt, and citation ID", async () => {
    const raw = fixture() as { run_id: string; attempt_id: string; authorities: unknown[] };
    const projection = parseFrozenLegalReviewProjection(raw);
    const authority = raw.authorities[0] as Record<string, unknown>;
    expect(authority).toBeDefined();
    const fetchMock = vi.fn().mockResolvedValue(response(authority));
    vi.stubGlobal("fetch", fetchMock);
    const citationId = projection.authorities[0]?.citation_id;
    expect(citationId).toBeDefined();
    const result = await getFrozenLegalAuthority("case/1", projection.run_id, projection.attempt_id, citationId!);
    expect(result.citation_id).toBe(citationId);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      `/api/cases/case%2F1/runs/${projection.run_id}/attempts/${projection.attempt_id}/legal-citations/${citationId}`,
    );
  });

  it("rejects a workspace citation projection when the exact selected attempt is absent", () => {
    const projection = fixture();
    expect(() => parseReviewWorkspace({
      run: { id: "run_wire_fixture", case_id: "case-wire", state: "completed" },
      audit_events: [],
      legal_authorities: projection,
    })).toThrow(/不属于当前审查及所选执行记录/);
  });
});
