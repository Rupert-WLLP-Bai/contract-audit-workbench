import { describe, expect, it } from "vitest";
import { parseContractReferenceCheck } from "./contracts";

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
