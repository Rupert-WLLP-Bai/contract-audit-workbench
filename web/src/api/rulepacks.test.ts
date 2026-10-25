import { afterEach, describe, expect, it, vi } from "vitest";
import type { RulePackManifestInput } from "./rulepacks";
import {
  approveRulePack,
  createRulePack,
  disableRulePack,
  editRulePack,
  parseRulePackDetail,
  parseRulePackList,
  parseRulePackMutationResult,
  parseRulePackTestResponse,
  publishRulePack,
  rollbackRulePack,
  testRulePack,
  validateRulePack,
} from "./rulepacks";

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
});

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } });
}

function installFetch(impl: typeof fetch) {
  globalThis.fetch = impl;
}

function mockMutationFetch() {
  const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse({ pack: summary }));
  installFetch(fetchMock as typeof fetch);
  return fetchMock;
}

function initOf(fetchMock: ReturnType<typeof mockMutationFetch>, index: number): RequestInit {
  return fetchMock.mock.calls[index]?.[1] as RequestInit;
}

function requestPath(fetchMock: ReturnType<typeof mockMutationFetch>, index: number): string {
  return String(fetchMock.mock.calls[index]?.[0]);
}


const contentHash = "a".repeat(64);
const snapshotHash = "b".repeat(64);

const summary = {
  id: "telecom-core",
  version: "2",
  tenant_id: "tenant-a",
  status: "draft",
  revision: 3,
  content_hash: contentHash,
  snapshot_sha256: snapshotHash,
  effective_from: "2026-10-01T00:00:00Z",
  effective_until: "2027-01-01T00:00:00Z",
  authority_sources: ["internal-policy"],
  is_active: false,
  active_pointer_revision: 4,
};

const manifest = {
  schema_version: "rulepack/v1",
  id: "telecom-core",
  version: "2",
  tenant_id: "tenant-a",
  effective_from: "2026-10-01T00:00:00Z",
  effective_until: "2027-01-01T00:00:00Z",
  rules: [{
    rule_id: "LINE-BW-001",
    version: "1",
    domain: "technical",
    capability_id: "dedicated_line.minimum_bandwidth",
    mandatory: true,
    business_scope: { business_line: "ENT", scenario: "telecom_dedicated_line", contract_type: "service" },
    tenant_id: "tenant-a",
    comparator: "minimum_bandwidth",
    threshold: { value: "100", unit: "Mbit/s" },
    required_fact: { dimension: "bandwidth", source_roles: ["procurement"], object_required: true, unit_family: "bandwidth", exclusions: [] },
    authority_sources: [{ id: "internal-policy", version: "3", kind: "policy" }],
  }],
};

const manifestSnapshot = { ...manifest, lifecycle: { state: "draft" } };
const detail = { pack: summary, manifest: manifestSnapshot };

describe("RulePack API parsers", () => {
  it("preserves non-SLA null exclusions and rejects missing SLA arrays or invalid elements", () => {
    const withExclusions = (exclusions: unknown, comparator = "minimum_bandwidth") => ({
      ...detail,
      manifest: { ...manifestSnapshot, rules: [{ ...manifest.rules[0], comparator, required_fact: { ...manifest.rules[0]!.required_fact, exclusions } }] },
    });
    expect(parseRulePackDetail(withExclusions(null)).manifest.rules[0]?.required_fact.exclusions).toBeNull();
    expect(parseRulePackDetail(withExclusions([], "monthly_sla")).manifest.rules[0]?.required_fact.exclusions).toEqual([]);
    expect(() => parseRulePackDetail(withExclusions(null, "monthly_sla"))).toThrow(/exclusions/);
    expect(() => parseRulePackDetail(withExclusions([7]))).toThrow(/exclusions/);
  });

  it("accepts the documented list and detail envelopes", () => {
    expect(parseRulePackList({ items: [summary] })).toEqual([summary]);
    expect(parseRulePackDetail(detail)).toEqual(detail);
  });

  it("accepts the documented summary response for pointer mutations", () => {
    expect(parseRulePackMutationResult({ pack: summary })).toEqual(summary);
  });

  it("rejects unknown fields and lifecycle states", () => {
    expect(() => parseRulePackList({ items: [{ ...summary, server_only: true }] })).toThrow(/list.items\[0\]/);
    expect(() => parseRulePackDetail({ ...detail, unexpected: true })).toThrow(/detail/);
    expect(() => parseRulePackList({ items: [{ ...summary, revision: 0 }] })).toThrow(/revision/);
    expect(() => parseRulePackList({ items: [{ ...summary, content_hash: "not-a-sha256" }] })).toThrow(/content_hash/);
    const changed = structuredClone(detail);
    changed.manifest.lifecycle.state = "retired";
    expect(() => parseRulePackDetail(changed)).toThrow(/lifecycle.state/);
  });

  it("rejects an unknown sample-test relation rather than dropping it", () => {
    expect(() => parseRulePackTestResponse({
      revision: 3,
      content_hash: contentHash,
      report: {
        pack_id: "telecom-core",
        pack_version: "2",
        pack_hash: contentHash,
        relations: ["probably_ok"],
        results: [],
      },
    })).toThrow(/relations\[0\]/);
  });
  it("publishes and rolls back with pointer CAS and idempotency keys", async () => {
    const fetchMock = mockMutationFetch();
    await publishRulePack("telecom-core", "2", 3, 4, "publish-operation");
    await rollbackRulePack("telecom-core", "1", 5, "rollback-operation");

    expect(requestPath(fetchMock, 0)).toBe("/api/admin/rulepacks/telecom-core/versions/2/publish");
    expect(initOf(fetchMock, 0).method).toBe("POST");
    expect(JSON.parse(String(initOf(fetchMock, 0).body))).toEqual({ expected_revision: 3, expected_pointer_revision: 4 });
    expect((initOf(fetchMock, 0).headers as Record<string, string>)["Idempotency-Key"]).toBe("publish-operation");

    expect(requestPath(fetchMock, 1)).toBe("/api/admin/rulepacks/telecom-core/rollback");
    expect(JSON.parse(String(initOf(fetchMock, 1).body))).toEqual({ version: "1", expected_pointer_revision: 5 });
    expect((initOf(fetchMock, 1).headers as Record<string, string>)["Idempotency-Key"]).toBe("rollback-operation");
  });

  it("sends five-field sample citations and parses fully source-bound report citations", async () => {
    const responseCitation = {
      source_id: "source-1",
      source_version: "1",
      tenant_id: "tenant-a",
      case_id: "sample-case",
      source_digest: "c".repeat(64),
      start: 0,
      end: 15,
      quote: "服务带宽为100 Mbit/s",
    };
    const response = {
      revision: 3,
      content_hash: contentHash,
      report: {
        pack_id: "telecom-core",
        pack_version: "2",
        pack_hash: contentHash,
        relations: ["compatible"],
        results: [{
          rule_id: "LINE-BW-001",
          rule_version: "1",
          domain: "technical",
          capability_id: "dedicated_line.minimum_bandwidth",
          mandatory: true,
          relation: "compatible",
          reason_code: "verified_fact",
          fact_ids: ["fact-1"],
          citations: [responseCitation],
        }],
      },
    };
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse(response));
    installFetch(fetchMock as typeof fetch);
    const requestCitation = {
      source_id: "source-1",
      source_version: "1",
      start: 0,
      end: 15,
      quote: "服务带宽为100 Mbit/s",
    };
    const sample = {
      expected_revision: 3,
      business_line: "ENT",
      scenario: "telecom_dedicated_line",
      contract_type: "service",
      at: "2026-10-23T12:00:00Z",
      sources: [{ id: "source-1", version: "1", role: "procurement", text: "服务带宽为100 Mbit/s" }],
      facts: [{ id: "fact-1", rule_id: "LINE-BW-001", dimension: "bandwidth", object_ref: "LINE-001", value: "100", unit: "Mbit/s", source_role: "procurement", citations: [requestCitation] }],
    };

    expect(await testRulePack("telecom-core", "2", sample)).toEqual(response);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/admin/rulepacks/telecom-core/versions/2/test");
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual(sample);
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body)).facts[0].citations[0]).toEqual(requestCitation);
  });
  it("sends tenant-scoped drafts and row-CAS lifecycle actions", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse(detail));
    installFetch(fetchMock as typeof fetch);
    const input = manifest as RulePackManifestInput;

    await createRulePack(input);
    await editRulePack("telecom-core", "2", 3, input);
    await validateRulePack("telecom-core", "2", 4);
    await approveRulePack("telecom-core", "2", 5, contentHash);
    await disableRulePack("telecom-core", "2", 6, 7);

    expect(requestPath(fetchMock, 0)).toBe("/api/admin/rulepacks");
    expect(initOf(fetchMock, 0).method).toBe("POST");
    expect(JSON.parse(String(initOf(fetchMock, 0).body))).toEqual({ manifest: input });
    expect(requestPath(fetchMock, 1)).toBe("/api/admin/rulepacks/telecom-core/versions/2");
    expect(initOf(fetchMock, 1).method).toBe("PUT");
    expect(JSON.parse(String(initOf(fetchMock, 1).body))).toEqual({ expected_revision: 3, manifest: input });
    expect(requestPath(fetchMock, 2)).toBe("/api/admin/rulepacks/telecom-core/versions/2/validate");
    expect(JSON.parse(String(initOf(fetchMock, 2).body))).toEqual({ expected_revision: 4 });
    expect(requestPath(fetchMock, 3)).toBe("/api/admin/rulepacks/telecom-core/versions/2/approve");
    expect(JSON.parse(String(initOf(fetchMock, 3).body))).toEqual({ expected_revision: 5, expected_content_hash: contentHash });
    expect(requestPath(fetchMock, 4)).toBe("/api/admin/rulepacks/telecom-core/versions/2/disable");
    expect(JSON.parse(String(initOf(fetchMock, 4).body))).toEqual({ expected_revision: 6, expected_pointer_revision: 7 });
  });
});
