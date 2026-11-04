// File metadata (manifest mirror):
// Purpose: Checks immutable quote ranges, Unicode, layout whitespace, numerical boundaries, duplicate matches, OCR quality and headered or headerless TSV geometry.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, it } from "vitest";
import { locateSourceQuote, parseOCRWords, type SourceWord } from "./source-annotation";

const word = (text: string, x = 0.1): SourceWord => ({
  text, box: { x, y: 0.2, width: 0.2, height: 0.04 },
});
const frozen = "😀甲方应支付100万元。";
const quote = { id: "a", start: 6, end: 11, quote: "100万元", page: 2 };

describe("original page citation annotations", () => {
  it("maps an exact frozen Unicode citation to observed page regions", () => {
    const result = locateSourceQuote(frozen, quote, [word("100"), word("万元", 0.3)], 2);
    expect(result.kind).toBe("located");
    if (result.kind === "located") expect(result.boxes).toHaveLength(2);
  });

  it("allows whitespace and line wrapping without changing numbers or units", () => {
    expect(locateSourceQuote(frozen, quote, [word("100 万\n元")], 2).kind).toBe("located");
    expect(locateSourceQuote(frozen, quote, [word("1000万元")], 2).kind).toBe("not_found");
    expect(locateSourceQuote(frozen, quote, [word("1100万元")], 2).kind).toBe("not_found");
    expect(locateSourceQuote(frozen, quote, [word("-100万元")], 2).kind).toBe("not_found");
    expect(locateSourceQuote(frozen, quote, [word("100万圆")], 2).kind).toBe("not_found");
  });

  it("allows OCR colon and sentence-stop glyph variants while keeping frozen and numeric identity exact", () => {
    const text = "带宽：80 Mbps。";
    const citation = { id: "zh", quote: text, start: 0, end: text.length, page: 1 };
    expect(locateSourceQuote(text, citation, [word("带宽:80 Mbps.")], 1).kind).toBe("located");
    expect(locateSourceQuote(text, { ...citation, quote: "带宽:80 Mbps." }, [word("带宽:80 Mbps.")], 1).kind).toBe("unavailable");
    const number = "1。5万元";
    expect(locateSourceQuote(number, { ...citation, quote: number, end: number.length }, [word("1.5万元")], 1).kind).toBe("not_found");
  });

  it("keeps a weak layout colon as context without highlighting it or accepting weak numeric punctuation", () => {
    const text = "条款：付款";
    const result = locateSourceQuote(text, { id: "separator", quote: text, start: 0, end: text.length, page: 1 },
      [word("条款"), { ...word(":"), confidence: 50 }, word("付款")], 1);
    expect(result.kind).toBe("located");
    if (result.kind === "located") expect(result.boxes).toHaveLength(2);
    const ratio = "1：5";
    expect(locateSourceQuote(ratio, { id: "ratio", quote: ratio, start: 0, end: ratio.length, page: 1 },
      [word("1"), { ...word(":"), confidence: 50 }, word("5")], 1).kind).toBe("unavailable");
  });

  it("refuses a stale frozen range even when the page contains the quote", () => {
    expect(locateSourceQuote(frozen, { ...quote, start: 5 }, [word("100万元")], 2).kind).toBe("unavailable");
  });

  it("does not choose among repeated matches or locate on a different frozen page", () => {
    expect(locateSourceQuote(frozen, quote, [word("100万元"), word("100万元", 0.4)], 2).kind).toBe("ambiguous");
    expect(locateSourceQuote(frozen, quote, [word("100万元")], 1).kind).toBe("wrong_page");
  });

  it("preserves observed word boundaries around an English citation", () => {
    const text = "Bandwidth 80 Mbps";
    expect(locateSourceQuote(text, { id: "en", quote: text, start: 0, end: text.length, page: 1 },
      [word("Bandwidth"), word("80"), word("Mbps"), word("Warranty")], 1).kind).toBe("located");
  });

  it("refuses invalid geometry and low quality OCR regions", () => {
    expect(locateSourceQuote(frozen, quote, [{ ...word("100万元"), confidence: 20 }], 2).kind).toBe("unavailable");
    for (const x of [-0.1, 1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(locateSourceQuote(frozen, quote, [word("100万元", x)], 2).kind).toBe("unavailable");
    }
  });

  it("reads observed OCR word boxes as proportions of the rendered page", () => {
    const tsv = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
      "5\t1\t1\t1\t1\t1\t20\t40\t80\t10\t92\t100万元\n";
    expect(parseOCRWords(tsv, 200, 200)).toEqual([
      { text: "100万元", confidence: 92, box: { x: 0.1, y: 0.2, width: 0.4, height: 0.05 } },
    ]);
    expect(parseOCRWords("1\t1\t0\t0\t0\t0\t0\t0\t200\t200\t-1\t\n" + tsv.split("\n")[1], 200, 200))
      .toEqual(parseOCRWords(tsv, 200, 200));
    expect(parseOCRWords(tsv, 0, 200)).toEqual([]);
    expect(parseOCRWords("unrecognized output", 200, 200)).toEqual([]);
  });
});
