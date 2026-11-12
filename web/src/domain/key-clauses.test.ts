import { describe, expect, it } from "vitest";
import type { ReviewDocument, TextOffset } from "../api/types";
import { frozenQuoteMatchesDocument } from "./frozen-quote";
import { keyClauseNavigationFor } from "./key-clauses";

const text = "😀第一条 付款条款：甲方支付预付款。第二条 保密数据条款：双方保护个人信息。";
const codePoints = Array.from(text);

function rangeOf(quote: string): TextOffset {
  const quotePoints = Array.from(quote);
  const start = codePoints.findIndex((_, index) =>
    quotePoints.every((character, quoteIndex) => codePoints[index + quoteIndex] === character),
  );
  return { start, end: start + quotePoints.length };
}

function documentOf(overrides: Partial<ReviewDocument> = {}): ReviewDocument {
  return {
    id: "doc-1",
    role: "contract",
    filename: "合同.txt",
    media_type: "text/plain",
    digest: "frozen-digest",
    size: 10,
    parse_status: "ready",
    parse_error: null,
    created_at: "2026-11-01T00:00:00Z",
    parsed_document: {
      id: "parsed-1",
      document_id: "doc-1",
      original_digest: "frozen-digest",
      parser_id: "parser",
      parser_version: "1",
      config_hash: "config",
      normalization_version: "v1",
      normalized_digest: "normalized",
      ir_digest: "ir",
      artifact_digest: "artifact",
      created_at: "2026-11-01T00:00:00Z",
      status: "ready",
    },
    parsed_document_id: "parsed-1",
    text,
    clauses: [
      {
        id: "clause-payment",
        heading: "付款条款",
        text_offset: rangeOf("付款条款：甲方支付预付款。"),
        page: 3,
      },
      {
        id: "clause-data",
        heading: "保密数据条款",
        text_offset: rangeOf("保密数据条款：双方保护个人信息。"),
        page: null,
      },
    ],
    ...overrides,
  };
}

const scope = {
  caseId: "case-1",
  runId: "run-1",
  attemptId: "attempt-1",
  isFrozenAttempt: true,
  parsedDocumentIds: ["parsed-1"],
};

describe("key clause navigation", () => {
  it("extracts only exact, selected-attempt clause spans and labels common categories", () => {
    const result = keyClauseNavigationFor(scope, documentOf());

    expect(result.candidates).toHaveLength(2);
    expect(result.candidates[0]).toMatchObject({
      heading: "付款条款",
      quote: "付款条款：甲方支付预付款。",
      page: 3,
      categories: ["payment"],
      parsedDocumentId: "parsed-1",
    });
    expect(result.candidates[1]).toMatchObject({
      heading: "保密数据条款",
      quote: "保密数据条款：双方保护个人信息。",
      page: null,
      categories: ["confidentiality_data"],
    });
    expect(result.candidates[0]?.selection).toMatchObject({
      caseId: "case-1",
      runId: "run-1",
      attemptId: "attempt-1",
      documentId: "doc-1",
      parsedDocumentId: "parsed-1",
    });
  });

  it("rejects a document parsed by a different attempt", () => {
    const result = keyClauseNavigationFor(
      { ...scope, parsedDocumentIds: ["parsed-other"] },
      documentOf(),
    );

    expect(result.candidates).toEqual([]);
    expect(result.message).toContain("不属于所选冻结尝试");
  });

  it("rejects parsed metadata whose identity does not match the selected document", () => {
    const result = keyClauseNavigationFor(
      scope,
      documentOf({
        parsed_document: {
          ...documentOf().parsed_document!,
          document_id: "doc-other",
        },
      }),
    );

    expect(result.candidates).toEqual([]);
    expect(result.message).toContain("不属于所选冻结尝试");
  });

  it("does not turn headings into candidates when their saved offsets are invalid", () => {
    const result = keyClauseNavigationFor(
      scope,
      documentOf({
        clauses: [
          {
            id: "bad",
            heading: "付款条款",
            text_offset: { start: 4, end: codePoints.length + 1 },
            page: 1,
          },
        ],
      }),
    );

    expect(result.candidates).toEqual([]);
    expect(result.message).toBe("当前材料中没有可定位的关键条款。");
  });

  it("does not generate navigation for an unfrozen attempt", () => {
    const result = keyClauseNavigationFor({ ...scope, isFrozenAttempt: false }, documentOf());

    expect(result.candidates).toEqual([]);
    expect(result.message).toContain("可核验的冻结审查尝试");
  });

  it("uses sentence fragments when no ClauseNodes exist and converts emoji/newline offsets to code points", () => {
    const fragmentText = "😀背景说明。\n甲方补充约定：乙方应在次日支付预付款。\n其他内容。";
    const expectedStart = Array.from(fragmentText.slice(0, fragmentText.indexOf("甲方"))).length;
    const document = documentOf({ text: fragmentText, clauses: [] });
    const result = keyClauseNavigationFor(scope, document);
    const candidate = result.candidates[0];

    expect(candidate).toBeDefined();
    expect(candidate).toMatchObject({
      kind: "fragment",
      heading: "正文片段/标题未识别",
      sourceLabel: "正文片段/标题未识别",
      page: null,
      range: { start: expectedStart },
    });
    expect(candidate?.quote).toContain("支付预付款");
    expect(candidate?.selection.quote).toBe(candidate?.quote);
    expect(
      Array.from(fragmentText).slice(candidate?.range.start, candidate?.range.end).join(""),
    ).toBe(candidate?.quote);
    expect(candidate?.selection.attemptId).toBe("attempt-1");
    expect(
      frozenQuoteMatchesDocument(
        candidate?.selection ?? null,
        { ...scope, attemptId: "attempt-switched" },
        document,
      ),
    ).toBe(false);
  });

  it("uses sentence fragments for an unheaded ClauseNode without claiming its page or structure", () => {
    const quote = "保密数据条款：双方保护个人信息。";
    const document = documentOf({
      clauses: [{ id: "untitled", heading: "  ", text_offset: rangeOf(quote), page: 7 }],
    });
    const result = keyClauseNavigationFor(scope, document);

    expect(result.candidates).toHaveLength(1);
    expect(result.candidates[0]).toMatchObject({
      kind: "fragment",
      heading: "正文片段/标题未识别",
      sourceLabel: "正文片段/标题未识别",
      quote,
      page: null,
      clauseId: "untitled",
    });
  });

  it("keeps a keyword at the end of an overlong clause in a bounded, exact fragment", () => {
    const text = `${"背景资料".repeat(1400)}乙方应支付尾款。`;
    const document = documentOf({
      text,
      clauses: [
        {
          id: "long-payment",
          heading: "付款结算条款",
          text_offset: { start: 0, end: Array.from(text).length },
          page: 4,
        },
      ],
    });
    const result = keyClauseNavigationFor(scope, document);
    const candidate = result.candidates[0];

    expect(candidate).toBeDefined();
    expect(candidate).toMatchObject({
      kind: "fragment",
      heading: "付款结算条款",
      sourceLabel: "正文片段/非完整条款",
      page: null,
      range: { end: Array.from(text).length },
    });
    expect(candidate?.quote.endsWith("乙方应支付尾款。")).toBe(true);
    expect(Array.from(candidate?.quote ?? "").length).toBeLessThanOrEqual(4096);
    expect(candidate?.range.start).toBeGreaterThan(0);
    expect(candidate?.quote).toBe(
      Array.from(text).slice(candidate?.range.start, candidate?.range.end).join(""),
    );
  });
});
