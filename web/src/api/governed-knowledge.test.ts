import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, setSessionIdentity } from "./http";
import { commandGovernedKnowledge, createGovernedKnowledge, getGovernedKnowledge, previewGovernedPolicy, type KnowledgeCommand, type KnowledgeVersion } from "./governed-knowledge";

const version: KnowledgeVersion = {
  ref: { knowledge_id: "policy/one", version_id: "v/hash", content_sha256: "a".repeat(64) },
  content: {
    knowledge_id: "policy/one", version: "1", kind: "policy", title: "制度", body: "带宽不低于100 Mbit/s", summary: "核验带宽",
    source_type: "reference", source_reference: "制度来源", scope: { business_line: "ENT", scenario: "line", contract_type: "service" },
    valid_from: "2026-01-01T00:00:00Z", valid_until: "2027-01-01T00:00:00Z", applicability: null, exclusions: [],
    binding: { pack_id: "pack", pack_version: "1", pack_content_hash: "b".repeat(64), pack_snapshot_digest: "c".repeat(64), compiler_ref: "governed-rulepack/v1", rule_ids: ["BW"], source_spans: [{ rule_id: "BW", start: 0, end: 15, quote: "带宽不低于100 Mbit/s" }], candidate_diff: "已审阅差异", validation_evidence: "校验记录" },
  },
  status: "submitted", revision: 2, cross_case_reviewed: false, active: false, pointer_revision: 4, created_by: "admin", created_at: "2026-01-01T00:00:00Z",
};
function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}
afterEach(() => { vi.unstubAllGlobals(); setSessionIdentity(null); });

describe("governed knowledge API", () => {
  it("reads exact escaped versions and rejects mismatched version or preview identities", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(json(version)).mockResolvedValueOnce(json({ ...version, ref: { ...version.ref, version_id: "other" } })).mockResolvedValueOnce(json({ ref: { ...version.ref, version_id: "other" }, extractor_ref: "extractor", comparisons: [], issues: [], semantic_only: [], approvable: false }));
    vi.stubGlobal("fetch", fetchMock);
    expect((await getGovernedKnowledge("policy/one", "v/hash")).content.applicability).toBeNull();
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/admin/governed-knowledge/policy%2Fone/versions/v%2Fhash");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ credentials: "include", cache: "no-store" });
    await expect(getGovernedKnowledge("policy/one", "v/hash")).rejects.toMatchObject({ code: "invalid_response" });
    await expect(previewGovernedPolicy("policy/one", "v/hash")).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("sends content and commands with their actual revisions, reason, digest and idempotency key", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(json(version, 201)).mockResolvedValueOnce(json({ ...version, status: "validated", revision: 3 }));
    vi.stubGlobal("fetch", fetchMock);
    await createGovernedKnowledge(version.content, "create-key");
    const command: KnowledgeCommand = { action: "validate", expected_revision: 2, expected_content_sha256: version.ref.content_sha256, expected_pointer_revision: 4, reason: "已核对规则", cross_case_reviewed: false };
    await commandGovernedKnowledge("policy/one", "v/hash", command, "command-key");
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ content: version.content });
    expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get("Idempotency-Key")).toBe("create-key");
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual(command);
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).get("Idempotency-Key")).toBe("command-key");
  });
  it("preserves server error status, code, retryability and details", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(json({ error: { code: "case_busy", message: "关联案件正在修改", retryable: true, details: { case_id: "case-1" } } }, 503)));
    const error = await getGovernedKnowledge("policy/one", "v/hash").catch((value: unknown) => value);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 503, code: "case_busy", message: "关联案件正在修改", retryable: true, details: { case_id: "case-1" } });
  });
});
