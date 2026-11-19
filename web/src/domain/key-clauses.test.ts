// File metadata (manifest mirror):
// Purpose: Checks exact contract highlight excerpts and Unicode offsets, category separation, payment table data and decisions to suppress ambiguous observations.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-19
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, it } from "vitest";
import type { ReviewDocument, TextOffset } from "../api/types";
import { frozenQuoteMatchesDocument } from "./frozen-quote";
import { keyClauseNavigationFor, readingGroupFor } from "./key-clauses";

const text = "😀第一条 付款条款：甲方支付预付款。第二条 保密数据条款：双方保护个人信息。";
function rangeOf(quote: string, source = text): TextOffset {
  const codePoints = Array.from(source);
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

    expect(result.allCandidates!).toHaveLength(2);
    expect(result.allCandidates![0]).toMatchObject({
      heading: "付款条款",
      quote: "付款条款：甲方支付预付款。",
      page: 3,
      categories: ["payment"],
      parsedDocumentId: "parsed-1",
    });
    expect(result.allCandidates![1]).toMatchObject({
      heading: "保密数据条款",
      quote: "保密数据条款：双方保护个人信息。",
      page: null,
      categories: ["confidentiality_data"],
    });
    expect(result.allCandidates![0]?.selection).toMatchObject({
      caseId: "case-1",
      runId: "run-1",
      attemptId: "attempt-1",
      documentId: "doc-1",
      parsedDocumentId: "parsed-1",
    });
  });

  it("keeps distinct amounts and removes an equivalent duplicate", () => {
    const source =
      "😀项目背景为网络建设。合同总价100万元。合同金额为100万元。采购金额为120万元。其他说明。";
    const document = documentOf({ text: source, clauses: [] });
    const result = keyClauseNavigationFor(scope, document);

    expect(result.candidates.map((candidate) => candidate.quote)).toEqual([
      "合同总价100万元。",
      "采购金额为120万元。",
    ]);
    const [candidate] = result.candidates;
    expect(candidate).toMatchObject({ quote: "合同总价100万元。", categories: ["payment"] });
    expect(frozenQuoteMatchesDocument(candidate?.selection ?? null, scope, document)).toBe(true);
  });

  it("keeps independent payment milestones while removing exact repeats", () => {
    const source =
      "验收通过并收到发票后10个工作日内支付首期款。年度服务期满并完成交接后15个工作日内支付尾款。验收通过并收到发票后10个工作日内支付首期款。";
    const result = keyClauseNavigationFor(scope, documentOf({ text: source, clauses: [] }));
    expect(result.candidates.map((candidate) => candidate.quote)).toEqual([
      "验收通过并收到发票后10个工作日内支付首期款。",
      "年度服务期满并完成交接后15个工作日内支付尾款。",
    ]);
  });

  it("uses the pinned table columns for amounts, preserves merged column numbers and excludes quantity", () => {
    const grid = [
      [
        [1, "名称"],
        [2, "数量"],
        [3, "单价"],
        [5, "含税金额（元）"],
      ],
      [
        [1, "支持服务"],
        [2, "999"],
        [3, "80.50"],
        [5, "241.50"],
      ],
      [
        [1, "合计"],
        [5, "241.50"],
      ],
    ] as const;
    let text = "😀采购项目。\n\n";
    const table_cells = grid.flatMap((row, rowIndex) => {
      if (rowIndex > 0) text += "\n";
      return row.map(([column, value], cellIndex) => {
        if (cellIndex > 0) text += "\t";
        const start = Array.from(text).length;
        text += value;
        return {
          table_id: "t-金",
          row: rowIndex + 1,
          column,
          text_offset: { start, end: start + Array.from(value).length },
        };
      });
    });
    const document = documentOf({ text, table_cells, clauses: [] });
    const result = keyClauseNavigationFor(scope, document);
    expect(result.candidates.map(({ quote, categories }) => ({ quote, categories }))).toEqual([
      { quote: "241.50", categories: ["payment"] },
    ]);
    expect(result.candidates[0]?.range.start).toBe(table_cells.at(-1)?.text_offset.start);
    expect(result.allCandidates?.map(({ quote }) => quote)).toEqual(["80.50", "241.50", "241.50"]);
    expect(result.candidates[0]?.sourceLabel).toBe("冻结表格单元格");
    for (const item of result.allCandidates ?? []) {
      expect(frozenQuoteMatchesDocument(item.selection, scope, document)).toBe(true);
    }
    const older = keyClauseNavigationFor(scope, documentOf({ text, clauses: [] }));
    expect(older.allCandidates?.some((item) => item.sourceLabel === "冻结表格单元格")).not.toBe(
      true,
    );
  });

  it("uses explicit deadline labels and units without assuming a bare number is a date or duration", () => {
    const text = "😀导语。\n\n合同履约期限\t36个月\n响应时间\t50\n数量\t9个工作日";
    const rows = [
      ["合同履约期限", "36个月"],
      ["响应时间", "50"],
      ["数量", "9个工作日"],
    ];
    const table_cells = rows.flatMap((row, r) =>
      row.map((value, c) => ({
        table_id: "t-1",
        row: r + 1,
        column: c + 1,
        text_offset: rangeOf(value, text),
      })),
    );
    const result = keyClauseNavigationFor(scope, documentOf({ text, table_cells, clauses: [] }));
    expect(result.candidates.map(({ quote, categories }) => ({ quote, categories }))).toEqual([
      { quote: "36个月", categories: ["term_termination"] },
    ]);
  });

  it("maps payment milestones, obligations and termination to the requested reading groups", () => {
    const source =
      "验收通过并收到发票后10个工作日内支付首期款。年度服务期满并完成交接后15个工作日内支付尾款。乙方应在10个工作日内交付服务成果。逾期超过15日，甲方有权解除合同。";
    const result = keyClauseNavigationFor(scope, documentOf({ text: source, clauses: [] }));
    expect(result.candidates.map((candidate) => readingGroupFor(candidate))).toEqual([
      "money",
      "money",
      "performance",
      "rights",
    ]);
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
            text_offset: { start: 4, end: Array.from(text).length + 1 },
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
    const candidate = result.allCandidates![0];

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

    expect(result.allCandidates!).toHaveLength(1);
    expect(result.allCandidates![0]).toMatchObject({
      kind: "fragment",
      heading: "正文片段/标题未识别",
      sourceLabel: "正文片段/标题未识别",
      quote,
      page: null,
      clauseId: "untitled",
    });
  });

  it("finds a whole short obligation inside a long structure without claiming its page", () => {
    const text = `${"背景资料".repeat(1400)}。乙方应支付尾款。`;
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
    const candidate = result.allCandidates![0];

    expect(candidate).toBeDefined();
    expect(candidate).toMatchObject({
      kind: "fragment",
      heading: "付款结算条款",
      sourceLabel: "正文片段/非完整条款",
      page: null,
      range: { end: Array.from(text).length },
    });
    expect(candidate?.quote).toBe("乙方应支付尾款。");
    expect(candidate?.range.start).toBeGreaterThan(0);
    expect(candidate?.quote).toBe(
      Array.from(text).slice(candidate?.range.start, candidate?.range.end).join(""),
    );
  });

  it("allows no suggestions for headings, form placeholders and deferred terms", () => {
    const text =
      "目录：付款条款、验收标准、保密数据。甲方应按【待填写】金额支付价款。" +
      "双方同意付款期限另行约定。项目背景提到数据。";
    expect(keyClauseNavigationFor(scope, documentOf({ text, clauses: [] })).candidates).toEqual([]);
  });

  it("keeps a following exception with its obligation and the original Unicode offsets", () => {
    const text = "😀前言。甲方应在收票后三十日内支付价款。\n但通知后仍有争议的款项暂缓支付。";
    const document = documentOf({ text, clauses: [] });
    const [item] = keyClauseNavigationFor(scope, document).candidates;
    expect(item?.quote).toBe("甲方应在收票后三十日内支付价款。\n但通知后仍有争议的款项暂缓支付。");
    expect(item?.range.start).toBe(Array.from("😀前言。").length);
    expect(frozenQuoteMatchesDocument(item?.selection ?? null, scope, document)).toBe(true);
  });

  it("covers explicit jurisdiction, warranties and disclosures alongside payment", () => {
    const text =
      "合同总价为人民币壹拾万元整。预付款金额为合同价款的30%。" +
      "双方协商不成时，任一方可向约定的有管辖权的法院起诉。质保三年。" +
      "未经甲方书面同意，乙方不得向第三方披露相关资料。";
    const document = documentOf({ text, clauses: [] });
    const { candidates } = keyClauseNavigationFor(scope, document);
    expect(candidates.map((candidate) => candidate.categories[0])).toEqual([
      "payment",
      "payment",
      "dispute_resolution",
      "warranty",
      "confidentiality_data",
    ]);
    expect(
      candidates.every(({ selection }) => frozenQuoteMatchesDocument(selection, scope, document)),
    ).toBe(true);
  });

  it("does not cut an overlong continuous condition into a misleading fragment", () => {
    const text = "甲方应支付预付款，" + "补充说明".repeat(80) + "，但仅限验收后。";
    expect(keyClauseNavigationFor(scope, documentOf({ text, clauses: [] })).candidates).toEqual([]);
  });
});

describe("frozen source proof", () => {
  it("binds current and historical parse proofs to the same frozen document and original digest", async () => {
    const { frozenSourceProof } = await import("./frozen-source-proof");
    const digest = "a".repeat(64);
    const frozen = documentOf({ digest: "", parsed_document: null });
    const current = documentOf({
      digest,
      parsed_document: {
        ...documentOf().parsed_document!,
        original_digest: digest,
      },
    });
    const detail = {
      id: "case-1",
      documents: [current],
      material_versions: [{ document_id: "doc-1", material_id: "material-1", version: 1 }],
    } as unknown as import("../api/types").CaseDetail;
    const proof = frozenSourceProof(detail, frozen, ["parsed-1"], undefined);
    expect(proof).toEqual({
      documentId: "doc-1",
      parsedDocumentId: "parsed-1",
      originalDigest: digest,
    });
    const provedDocument = { ...frozen, digest };
    expect(
      keyClauseNavigationFor({ ...scope, sourceProofs: [proof!] }, provedDocument).allCandidates,
    ).toHaveLength(2);
    expect(
      keyClauseNavigationFor({ ...scope, sourceProofs: [] }, provedDocument).candidates,
    ).toHaveLength(0);
    expect(frozenSourceProof(detail, frozen, ["other"], undefined)).toBeNull();
    expect(
      frozenSourceProof(detail, { ...frozen, digest: "b".repeat(64) }, ["parsed-1"], undefined),
    ).toBeNull();
    for (const metadata of [
      { ...current.parsed_document!, id: "other-parse" },
      { ...current.parsed_document!, document_id: "other-document" },
      { ...current.parsed_document!, original_digest: "b".repeat(64) },
      { ...current.parsed_document!, status: "failed" as const },
    ]) {
      expect(
        frozenSourceProof(
          detail,
          { ...frozen, parsed_document: metadata },
          ["parsed-1"],
          undefined,
        ),
      ).toBeNull();
    }
    const oldDetail = {
      ...detail,
      documents: [
        {
          ...current,
          parsed_document: { ...current.parsed_document!, id: "parsed-new" },
        },
      ],
    };
    const history = {
      material_id: "material-1",
      material_version: 1,
      document_id: "doc-1",
      items: [
        {
          parsed_document_id: "parsed-1",
          status: "ready" as const,
          parser_id: "p",
          parser_version: "1",
          created_at: "",
          original_digest: digest,
          normalized_digest: null,
        },
      ],
    };
    expect(frozenSourceProof(oldDetail, frozen, ["parsed-1"], undefined)).toBeNull();
    expect(frozenSourceProof(oldDetail, frozen, ["parsed-1"], history)).toEqual(proof);
    expect(
      frozenSourceProof(oldDetail, frozen, ["parsed-1"], {
        ...history,
        document_id: "other",
      }),
    ).toBeNull();
    expect(
      frozenSourceProof(oldDetail, frozen, ["parsed-1"], {
        ...history,
        items: [{ ...history.items[0]!, original_digest: "b".repeat(64) }],
      }),
    ).toBeNull();
  });
});
