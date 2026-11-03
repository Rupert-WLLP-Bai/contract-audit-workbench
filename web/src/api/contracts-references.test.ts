// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “parseContractReferenceCheck”; representative assertions cover keeps the resolved chain and original endpoint path, rejects a non-procurement source role, rejects unknown response properties. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-03
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { afterEach, describe, expect, it, vi } from "vitest";
import { getContractLifecycleOverview, parseContractReferenceCheck } from "./contracts";

const sha = "a".repeat(64);

function referenceCheck(): any {
  return {
    contract_id: "0f7d5c1e-4a2b-4c3d-8e9f-0123456789ab",
    source: "revision",
    contract_revision_id: "1f7d5c1e-4a2b-4c3d-8e9f-0123456789ab",
    source_case_id: "case-1",
    checked_at: "2026-11-01T12:00:00Z",
    subjects: [
      {
        party_id: "subj-1",
        role: "buyer",
        name_snapshot: "示例采购方",
        binding_id: "bind-1",
        binding_version: 1,
        case_role: "customer",
        binding_state: "active",
        subject_version: 2,
        subject_payload_hash: sha,
        verification_state: "verified",
        frozen: true,
      },
    ],
    materials: [
      {
        material_id: "mat-1",
        material_version_id: "doc-1",
        sha256: sha,
        purpose: "supporting",
        material_version: 3,
        material_role: "award",
        filename: "award.txt",
        media_type: "text/plain",
        frozen: true,
        original_path: "/api/cases/case-1/documents/doc-1/original",
      },
    ],
    procurement_result: {
      system: "erp-procurement",
      result_id: "PR-1",
      package_id: "PKG-1",
      snapshot_ref: "doc-1",
      material_id: "mat-1",
      material_version_id: "doc-1",
      material_version: 3,
      material_role: "award",
      sha256: sha,
      captured_at: "2026-11-01T12:00:00Z",
    },
    warnings: ["subject_pending_verification"],
  };
}

describe("parseContractReferenceCheck", () => {
  it("keeps the resolved chain and original endpoint path", () => {
    const parsed = parseContractReferenceCheck(referenceCheck());
    expect(parsed.source).toBe("revision");
    expect(parsed.subjects[0]?.binding_version).toBe(1);
    expect(parsed.materials[0]?.original_path).toBe(
      "/api/cases/case-1/documents/doc-1/original",
    );
    expect(parsed.procurement_result?.material_role).toBe("award");
  });

  it("rejects a non-procurement source role", () => {
    const value = referenceCheck();
    value.procurement_result.material_role = "attachment";
    expect(() => parseContractReferenceCheck(value)).toThrow();
  });

  it("rejects unknown response properties", () => {
    const value = { ...referenceCheck(), extra: true };
    expect(() => parseContractReferenceCheck(value)).toThrow();
  });
});

const lifecycleContractId = "0f7d5c1e-4a2b-4c3d-8e9f-0123456789ab";

function lifecycleOverview(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    contract: {
      contract_id: lifecycleContractId,
      current_revision_id: null,
      row_version: 1,
      authority_mode: "external",
    },
    observed_at: "2026-11-03T01:02:03Z",
    completeness: {
      plans: "unknown", deliveries: "known", acceptances: "known", payments: "known",
      assets: "known", disputes: "unknown", warranty: "unknown", other_obligations: "unknown",
    },
    source_observed_at: { contract_recorded_at: null, finance: null, erp: null },
    plans: null,
    deliveries: { items: [], summary: [], row_version: 1 },
    acceptances: { items: [], summary: [], row_version: 1 },
    payments: null,
    assets: { contract_id: lifecycleContractId, fixed_asset_status: "unknown", items: [], row_version: 1 },
    ...overrides,
  };
}

afterEach(() => vi.unstubAllGlobals());

describe("getContractLifecycleOverview response", () => {
  it.each(["different_contract", "null_plan_item"])("rejects an inconsistent %s projection", async (variant) => {
    const wrongId = "ffffffff-ffff-4fff-8fff-ffffffffffff";
    const body = variant === "different_contract"
      ? lifecycleOverview({
          contract: { contract_id: wrongId, current_revision_id: null, row_version: 1, authority_mode: "native" },
          assets: { contract_id: wrongId, fixed_asset_status: "unknown", items: [], row_version: 1 },
        })
      : lifecycleOverview({ plans: { delivery_plans: [null], acceptance_plans: null, payments: null, assets: null } });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body), {
      status: 200, headers: { "Content-Type": "application/json" },
    })));
    await expect(getContractLifecycleOverview(lifecycleContractId)).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("accepts a minimal response with null plans and payment facts and empty known lists", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(lifecycleOverview()), {
      status: 200, headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await getContractLifecycleOverview(lifecycleContractId);
    expect(result.plans).toBeNull();
    expect(result.payments).toBeNull();
    expect(result.deliveries.items).toEqual([]);
    expect(result.completeness.deliveries).toBe("known");
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(`/api/contracts/${lifecycleContractId}/lifecycle-overview`);
  });

  it("rejects an invalid completeness value or malformed nullable plan shape", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(lifecycleOverview({
      completeness: {
        plans: "complete", deliveries: "known", acceptances: "known", payments: "known",
        assets: "known", disputes: "unknown", warranty: "unknown", other_obligations: "unknown",
      },
    })), { status: 200, headers: { "Content-Type": "application/json" } })));
    await expect(getContractLifecycleOverview(lifecycleContractId)).rejects.toMatchObject({ code: "invalid_response", status: 200 });

    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(lifecycleOverview({
      plans: { delivery_plans: "empty", acceptance_plans: null, payments: null, assets: null },
    })), { status: 200, headers: { "Content-Type": "application/json" } })));
    await expect(getContractLifecycleOverview(lifecycleContractId)).rejects.toMatchObject({ code: "invalid_response", status: 200 });
  });
});
