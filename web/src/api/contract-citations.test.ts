import { describe, expect, it } from "vitest";
import { parseFrozenContractCitation } from "./contract-citations";

const DIGEST = "a".repeat(64);
const TEXT = "😀甲方应支付100万元。";

function payload(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    case_id: "case-1",
    run_id: "run-1",
    attempt_id: "attempt-1",
    evidence_id: "fact_0123456789abcdef01234567",
    document_id: "doc-1",
    parsed_document_id: "parsed-1",
    filename: "合同.txt",
    original_digest: DIGEST,
    parse_digest: "b".repeat(64),
    start: 6,
    end: 11,
    quote: "100万元",
    text: TEXT,
    material_id: null,
    material_version: null,
    ...overrides,
  };
}

describe("frozen contract citation parsing", () => {
  it("reads a Unicode code-point range that starts after an emoji", () => {
    const citation = parseFrozenContractCitation(payload());
    expect(citation.start).toBe(6);
    expect(citation.end).toBe(11);
    expect(Array.from(citation.text).slice(citation.start, citation.end).join("")).toBe("100万元");
    expect(citation.material_id).toBeNull();
    expect(citation.material_version).toBeNull();
  });

  it("rejects a range that does not reproduce its own quote", () => {
    expect(() => parseFrozenContractCitation(payload({ start: 5 }))).toThrow(/区间与引用原文不一致/);
    expect(() => parseFrozenContractCitation(payload({ quote: "999万元" }))).toThrow(/区间与引用原文不一致/);
    expect(() => parseFrozenContractCitation(payload({ end: 99 }))).toThrow(/区间无效/);
  });

  it("rejects malformed digests, unknown fields and half a material identity", () => {
    expect(() => parseFrozenContractCitation(payload({ original_digest: "short" }))).toThrow(/不是 SHA-256/);
    expect(() => parseFrozenContractCitation(payload({ extra: true }))).toThrow(/无法识别/);
    expect(() =>
      parseFrozenContractCitation(payload({ material_id: "material-1", material_version: null })),
    ).toThrow(/材料身份不完整/);
    expect(() =>
      parseFrozenContractCitation(payload({ material_id: null, material_version: 2 })),
    ).toThrow(/材料身份不完整/);
  });

  it("keeps a complete material version when the server resolved one", () => {
    const citation = parseFrozenContractCitation(
      payload({ material_id: "material-1", material_version: 2 }),
    );
    expect(citation.material_id).toBe("material-1");
    expect(citation.material_version).toBe(2);
  });
});
