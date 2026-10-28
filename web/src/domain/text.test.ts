import {
  evidenceSourceMatches,
  findTextMatches,
  offsetAgrees,
  resolveAnchor,
  segmentText,
  sliceByCodePoints,
} from "./text";
import type { EvidenceAnchor, ParsedDocumentMetadata } from "../api/types";

function anchor(overrides: Partial<EvidenceAnchor>): EvidenceAnchor {
  return {
    id: "e1",
    document_version_id: "d1",
    parsed_document_id: null,
    normalized_digest: null,
    quote: "",
    status: "located",
    page: null,
    text_offset: null,
    bbox: null,
    mismatch_reason: null,
    ...overrides,
  };
}

function parsedDocument(
  overrides: Partial<ParsedDocumentMetadata> = {},
): ParsedDocumentMetadata {
  return {
    id: "pd1",
    document_id: "d1",
    original_digest: "original-hash",
    parser_id: "text",
    parser_version: "1",
    config_hash: "config-hash",
    normalization_version: "unicode-v1",
    normalized_digest: "text-hash",
    ir_digest: null,
    artifact_digest: "artifact-hash",
    created_at: "2026-10-21T01:00:00Z",
    status: "ready",
    ...overrides,
  };
}

describe("text offsets", () => {
  it("slices by unicode code point, not utf-16 unit", () => {
    expect(sliceByCodePoints("A😀B", 1, 2)).toBe("😀");
    expect(sliceByCodePoints("A😀B", 1, 4)).toBeNull();
  });

  it("highlights only when the quote is the coordinate slice", () => {
    expect(offsetAgrees("abcdefghijklmnopqrstuvwxyz", "abc")).toBe(false);
    expect(offsetAgrees("三十六个月", "采购文件约定质保期为三十六个月")).toBe(
      false,
    );
    expect(offsetAgrees("三十六个月", "三十六个月")).toBe(true);
  });

  it("drops page and offset when the quote cannot be anchored", () => {
    const resolved = resolveAnchor(
      "质保期为三十六个月。",
      anchor({
        status: "unlocatable",
        quote: "质保99个月",
        page: 4,
        text_offset: { start: 0, end: 4 },
        bbox: { x: 0, y: 0, width: 10, height: 10 },
        mismatch_reason: "引用与坐标切片不一致，不能编造页码或框。",
      }),
    );
    expect(resolved).toEqual({
      kind: "snapshot",
      quote: "质保99个月",
      reason: "引用与坐标切片不一致，不能编造页码或框。",
      page: null,
    });
  });

  it("keeps a mismatched anchor on the saved quote", () => {
    const text = "质保期为三十六个月。";
    const resolved = resolveAnchor(
      text,
      anchor({
        status: "anchor_mismatch",
        quote: "二十四个月",
        text_offset: { start: 4, end: 9 },
        mismatch_reason: "坐标已失效",
      }),
    );
    expect(resolved.kind).toBe("snapshot");
    if (resolved.kind === "snapshot")
      expect(resolved.reason).toBe("坐标已失效");
  });

  it("refuses to highlight when the quote disagrees with the offset", () => {
    const text = "质保期为三十六个月。";
    const resolved = resolveAnchor(
      text,
      anchor({
        status: "located",
        quote: "二十四个月",
        text_offset: { start: 4, end: 9 },
      }),
    );
    expect(resolved.kind).toBe("snapshot");
  });

  it("keeps an exact legacy anchor locatable when provenance was never recorded", () => {
    const text = "质保期为三十六个月。";
    const resolved = resolveAnchor(
      text,
      anchor({
        status: "located",
        quote: "三十六个月",
        text_offset: { start: 4, end: 9 },
        page: 3,
      }),
    );
    expect(resolved).toEqual({ kind: "highlight", start: 4, end: 9, page: 3 });
  });

  it("refuses a same-document anchor from a different parsed document", () => {
    const resolved = resolveAnchor(
      "质保期为三十六个月。",
      anchor({
        quote: "三十六个月",
        text_offset: { start: 4, end: 9 },
        parsed_document_id: "pd-old",
        normalized_digest: "text-old",
      }),
      parsedDocument({ id: "pd-new", normalized_digest: "text-new" }),
    );
    expect(resolved.kind).toBe("snapshot");
    if (resolved.kind === "snapshot")
      expect(resolved.reason).toContain("解析产物与当前正文不一致");
  });

  it("requires a ready parse and a matching supplied text digest", () => {
    const oldSource = anchor({
      quote: "三十六个月",
      text_offset: { start: 4, end: 9 },
      parsed_document_id: "pd1",
      normalized_digest: "text-hash",
    });
    const text = "质保期为三十六个月。";
    expect(
      resolveAnchor(text, oldSource, parsedDocument({ status: "failed" })).kind,
    ).toBe("snapshot");
    expect(
      resolveAnchor(
        text,
        oldSource,
        parsedDocument({ normalized_digest: "another-text-hash" }),
      ).kind,
    ).toBe("snapshot");
  });

  it("selects the evidence document by parsed identity when original IDs repeat", () => {
    const frozen = anchor({
      parsed_document_id: "pd-old",
      normalized_digest: "text-old",
    });
    expect(
      evidenceSourceMatches(
        frozen,
        "d1",
        parsedDocument({ id: "pd-old", normalized_digest: "text-old" }),
      ),
    ).toBe(true);
    expect(
      evidenceSourceMatches(
        frozen,
        "d1",
        parsedDocument({ id: "pd-new", normalized_digest: "text-new" }),
      ),
    ).toBe(false);
  });

  it("matches a public frozen parse without inventing diagnostic metadata", () => {
    const frozen = anchor({
      parsed_document_id: "pd1",
      quote: "三十六个月",
      text_offset: { start: 4, end: 9 },
    });
    const text = "质保期为三十六个月。";
    expect(evidenceSourceMatches(frozen, "d1", null, "pd1")).toBe(true);
    expect(evidenceSourceMatches(frozen, "d1", null, "pd2")).toBe(false);
    expect(evidenceSourceMatches(frozen, "d2", null, "pd1")).toBe(false);
    expect(resolveAnchor(text, frozen, null, "pd1").kind).toBe("highlight");
    expect(resolveAnchor(text, frozen, null, "pd2").kind).toBe("snapshot");
    expect(resolveAnchor(text, { ...frozen, normalized_digest: "withheld" }, null, "pd1").kind).toBe("snapshot");
  });

  it("treats a missing status as unavailable evidence", () => {
    const resolved = resolveAnchor(
      "正文",
      anchor({ status: "missing", quote: "" }),
    );
    expect(resolved.kind).toBe("unavailable");
  });
});

describe("segmentText and search", () => {
  it("marks the evidence span and keeps a clause boundary", () => {
    const segments = segmentText("质保期为三十六个月。", { start: 4, end: 9 }, [
      4,
    ]);
    const hit = segments.find((segment) => segment.highlighted === "evidence");
    expect(hit?.text).toBe("三十六个月");
    expect(hit?.anchor).toBe(true);
  });

  it("finds case-insensitive matches without splitting a code point", () => {
    expect(findTextMatches("ABC abc", "ab")).toEqual([
      { start: 0, end: 2 },
      { start: 4, end: 6 },
    ]);
  });
});
