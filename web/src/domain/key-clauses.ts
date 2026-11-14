// File metadata (manifest mirror):
// Purpose: Derives exact frozen key clauses or Intl.Segmenter source fragments, revalidating original quote and document identities.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { ReviewDocument, TextOffset } from "../api/types";
import {
  createFrozenQuoteSelection,
  frozenQuoteMatchesDocument,
  type FrozenQuoteScope,
  type FrozenQuoteSelection,
} from "./frozen-quote";
import { createTextIndex, sliceByCodePoints, type CodePointTextIndex } from "./text";

const MAX_FROZEN_QUOTE_LENGTH = 4096;

export const KEY_CLAUSE_CATEGORIES = [
  {
    id: "payment",
    label: "付款",
    keywords: ["付款", "支付", "价款", "结算", "预付款", "尾款", "账款", "发票"],
  },
  {
    id: "delivery_acceptance",
    label: "交付验收",
    keywords: ["交付", "交货", "验收", "签收", "交付物", "验收标准", "里程碑"],
  },
  {
    id: "term_termination",
    label: "期限终止",
    keywords: ["期限", "有效期", "续约", "展期", "终止", "解除", "届满", "到期"],
  },
  {
    id: "liability_compensation",
    label: "责任赔偿",
    keywords: ["责任", "违约", "赔偿", "违约金", "损失", "免责", "责任限制"],
  },
  {
    id: "confidentiality_data",
    label: "保密数据",
    keywords: ["保密", "商业秘密", "数据", "个人信息", "信息安全", "隐私", "网络安全"],
  },
] as const;

export type KeyClauseCategory = (typeof KEY_CLAUSE_CATEGORIES)[number]["id"];
export type KeyClauseKind = "structured" | "fragment";

export interface KeyClauseCandidate {
  id: string;
  clauseId: string | null;
  documentId: string;
  parsedDocumentId: string;
  heading: string;
  sourceLabel: string;
  kind: KeyClauseKind;
  range: TextOffset;
  quote: string;
  page: number | null;
  categories: readonly KeyClauseCategory[];
  selection: FrozenQuoteSelection;
}

export interface KeyClauseNavigation {
  candidates: KeyClauseCandidate[];
  message: string;
  notice?: string;
}

interface FragmentPlan {
  range: TextOffset;
  clauseId: string | null;
  heading: string | null;
  sourceLabel: string;
}

function parsedDocumentIdOf(document: ReviewDocument): string | null {
  const parsedDocumentId = document.parsed_document_id;
  if (!parsedDocumentId || parsedDocumentId.trim() === "") return null;
  const metadata = document.parsed_document;
  if (
    !metadata ||
    metadata.id !== parsedDocumentId ||
    metadata.document_id !== document.id ||
    metadata.original_digest !== document.digest ||
    metadata.status !== "ready"
  )
    return null;
  return parsedDocumentId;
}

function unavailable(message: string): KeyClauseNavigation {
  return { candidates: [], message };
}

function categoryMatches(text: string): KeyClauseCategory[] {
  return KEY_CLAUSE_CATEGORIES.flatMap((category) =>
    category.keywords.some((keyword) => text.includes(keyword)) ? [category.id] : [],
  );
}

function candidateId(
  selection: FrozenQuoteSelection,
  clauseId: string | null,
  kind: KeyClauseKind,
): string {
  return JSON.stringify([
    selection.caseId,
    selection.runId,
    selection.attemptId,
    selection.documentId,
    selection.parsedDocumentId,
    clauseId,
    kind,
    selection.start,
    selection.end,
  ]);
}

function candidateFromSelection(
  selection: FrozenQuoteSelection,
  parsedDocumentId: string,
  categories: readonly KeyClauseCategory[],
  source: {
    clauseId: string | null;
    heading: string;
    sourceLabel: string;
    kind: KeyClauseKind;
    page: number | null;
  },
): KeyClauseCandidate {
  return {
    id: candidateId(selection, source.clauseId, source.kind),
    clauseId: source.clauseId,
    documentId: selection.documentId,
    parsedDocumentId,
    heading: source.heading,
    sourceLabel: source.sourceLabel,
    kind: source.kind,
    range: { start: selection.start, end: selection.end },
    quote: selection.quote,
    page: source.page,
    categories,
    selection,
  };
}

function codePointBoundaryAtUtf16(
  textIndex: CodePointTextIndex,
  utf16Offset: number,
): number | null {
  let lower = 0;
  let upper = textIndex.utf16Offsets.length - 1;
  while (lower <= upper) {
    const middle = Math.floor((lower + upper) / 2);
    const offset = textIndex.utf16Offsets[middle];
    if (offset === utf16Offset) return middle;
    if (offset === undefined || offset > utf16Offset) upper = middle - 1;
    else lower = middle + 1;
  }
  return null;
}

function trimCodePointRange(textIndex: CodePointTextIndex, range: TextOffset): TextOffset | null {
  let start = range.start;
  let end = range.end;
  while (start < end && /\s/u.test(textIndex.codePoints[start] ?? "")) start += 1;
  while (end > start && /\s/u.test(textIndex.codePoints[end - 1] ?? "")) end -= 1;
  return start < end ? { start, end } : null;
}

function sentenceRanges(textIndex: CodePointTextIndex): TextOffset[] | null {
  if (typeof Intl.Segmenter !== "function") return null;
  const segmenter = new Intl.Segmenter("zh", { granularity: "sentence" });
  const ranges: TextOffset[] = [];
  for (const segment of segmenter.segment(textIndex.text)) {
    const start = codePointBoundaryAtUtf16(textIndex, segment.index);
    const end = codePointBoundaryAtUtf16(textIndex, segment.index + segment.segment.length);
    if (start === null || end === null) continue;
    const trimmed = trimCodePointRange(textIndex, { start, end });
    if (trimmed) ranges.push(trimmed);
  }
  return ranges;
}

function intersectRange(left: TextOffset, right: TextOffset): TextOffset | null {
  const start = Math.max(left.start, right.start);
  const end = Math.min(left.end, right.end);
  return start < end ? { start, end } : null;
}

function fragmentRangesFor(sentences: readonly TextOffset[], plan: FragmentPlan): TextOffset[] {
  return sentences.flatMap((sentence) => {
    const intersection = intersectRange(sentence, plan.range);
    return intersection ? [intersection] : [];
  });
}

function keywordHits(textIndex: CodePointTextIndex, range: TextOffset): TextOffset[] {
  const utf16Start = textIndex.utf16Offsets[range.start];
  const utf16End = textIndex.utf16Offsets[range.end];
  if (utf16Start === undefined || utf16End === undefined) return [];
  const unique = new Map<string, TextOffset>();
  for (const category of KEY_CLAUSE_CATEGORIES) {
    for (const keyword of category.keywords) {
      for (
        let match = textIndex.text.indexOf(keyword, utf16Start);
        match >= 0 && match < utf16End;
        match = textIndex.text.indexOf(keyword, match + keyword.length)
      ) {
        const matchEnd = match + keyword.length;
        if (matchEnd > utf16End) continue;
        const start = codePointBoundaryAtUtf16(textIndex, match);
        const end = codePointBoundaryAtUtf16(textIndex, matchEnd);
        if (start === null || end === null || start < range.start || end > range.end) continue;
        unique.set(`${start}:${end}`, { start, end });
      }
    }
  }
  return [...unique.values()].sort(
    (left, right) => left.start - right.start || left.end - right.end,
  );
}

function boundedContextRanges(textIndex: CodePointTextIndex, sentence: TextOffset): TextOffset[] {
  const hits = keywordHits(textIndex, sentence);
  const windows = hits.map((hit) => {
    const maxLength = Math.min(MAX_FROZEN_QUOTE_LENGTH, sentence.end - sentence.start);
    const centeredStart = hit.start - Math.floor((maxLength - (hit.end - hit.start)) / 2);
    let start = Math.max(sentence.start, centeredStart);
    const end = Math.min(sentence.end, start + maxLength);
    if (end - start < maxLength) start = Math.max(sentence.start, end - maxLength);
    return { start, end };
  });
  const merged: TextOffset[] = [];
  for (const window of windows) {
    const previous = merged.at(-1);
    const mergedEnd = previous ? Math.max(previous.end, window.end) : window.end;
    if (
      previous &&
      window.start <= previous.end &&
      mergedEnd - previous.start <= MAX_FROZEN_QUOTE_LENGTH
    ) {
      previous.end = mergedEnd;
    } else {
      merged.push({ ...window });
    }
  }
  return merged;
}

function fragmentCandidatesFor(
  scope: FrozenQuoteScope,
  document: ReviewDocument,
  parsedDocumentId: string,
  textIndex: CodePointTextIndex,
  sentences: readonly TextOffset[],
  plans: readonly FragmentPlan[],
): KeyClauseCandidate[] {
  const candidates: KeyClauseCandidate[] = [];
  for (const plan of plans) {
    const sentenceFragments = fragmentRangesFor(sentences, plan);
    for (const sentence of sentenceFragments) {
      const length = sentence.end - sentence.start;
      const ranges =
        length <= MAX_FROZEN_QUOTE_LENGTH ? [sentence] : boundedContextRanges(textIndex, sentence);
      for (const range of ranges) {
        const quote = sliceByCodePoints(document.text!, range.start, range.end, textIndex);
        if (quote === null || quote.trim() === "") continue;
        const categories = categoryMatches(quote);
        if (categories.length === 0) continue;
        const selection = createFrozenQuoteSelection(scope, document, range, textIndex);
        if (
          !selection ||
          selection.quote !== quote ||
          !frozenQuoteMatchesDocument(selection, scope, document)
        )
          continue;
        candidates.push(
          candidateFromSelection(selection, parsedDocumentId, categories, {
            clauseId: plan.clauseId,
            heading: plan.heading ?? "正文片段/标题未识别",
            sourceLabel: plan.sourceLabel,
            kind: "fragment",
            page: null,
          }),
        );
      }
    }
  }
  return candidates;
}

/**
 * Finds navigation-only candidates in a selected attempt's frozen document.
 * Keyword matches are labels for finding text, never risk findings or evidence.
 */
export function keyClauseNavigationFor(
  scope: FrozenQuoteScope | null,
  document: ReviewDocument | null,
): KeyClauseNavigation {
  if (
    !scope ||
    !scope.isFrozenAttempt ||
    !scope.caseId.trim() ||
    !scope.runId.trim() ||
    !scope.attemptId?.trim()
  ) {
    return unavailable("当前未选择可核验的冻结审查尝试，无法生成条款定位。");
  }
  if (!document) return unavailable("当前没有选中的审查材料。");
  const parsedDocumentId = parsedDocumentIdOf(document);
  if (!parsedDocumentId || !scope.parsedDocumentIds.includes(parsedDocumentId)) {
    return unavailable("当前材料不属于所选冻结尝试的解析版本，无法生成条款定位。");
  }
  if (document.parse_status !== "ready" || document.text === null || document.text === "") {
    return unavailable("当前冻结材料没有可定位正文。");
  }

  const textIndex = createTextIndex(document.text);
  const candidates: KeyClauseCandidate[] = [];
  const fragmentPlans: FragmentPlan[] = [];
  for (const clause of document.clauses) {
    const exactQuote = sliceByCodePoints(
      document.text,
      clause.text_offset.start,
      clause.text_offset.end,
      textIndex,
    );
    if (exactQuote === null || exactQuote.trim() === "") continue;
    const heading = clause.heading.trim();
    const exactRangeLength = clause.text_offset.end - clause.text_offset.start;
    if (!heading || exactRangeLength > MAX_FROZEN_QUOTE_LENGTH) {
      fragmentPlans.push({
        range: clause.text_offset,
        clauseId: clause.id || null,
        heading: heading || null,
        sourceLabel: heading ? "正文片段/非完整条款" : "正文片段/标题未识别",
      });
      continue;
    }
    const selection = createFrozenQuoteSelection(scope, document, clause.text_offset, textIndex);
    if (
      !selection ||
      selection.quote !== exactQuote ||
      !frozenQuoteMatchesDocument(selection, scope, document)
    )
      continue;
    const categories = categoryMatches(`${heading}\n${selection.quote}`);
    if (categories.length === 0) continue;
    candidates.push(
      candidateFromSelection(selection, parsedDocumentId, categories, {
        clauseId: clause.id,
        heading,
        sourceLabel: "结构化条款",
        kind: "structured",
        page:
          clause.page !== null && Number.isInteger(clause.page) && clause.page > 0
            ? clause.page
            : null,
      }),
    );
  }

  if (document.clauses.length === 0) {
    fragmentPlans.push({
      range: { start: 0, end: textIndex.codePoints.length },
      clauseId: null,
      heading: null,
      sourceLabel: "正文片段/标题未识别",
    });
  }

  if (fragmentPlans.length > 0) {
    const sentences = sentenceRanges(textIndex);
    if (sentences === null) {
      if (candidates.length === 0) {
        return unavailable("当前浏览器不支持原生句段定位，因此没有可定位的关键条款。");
      }
      return {
        candidates: candidates.sort((left, right) => left.range.start - right.range.start),
        message: "",
        notice: "当前浏览器不支持原生句段定位，未生成无标题或超长条款的正文片段。",
      };
    }
    candidates.push(
      ...fragmentCandidatesFor(
        scope,
        document,
        parsedDocumentId,
        textIndex,
        sentences,
        fragmentPlans,
      ),
    );
  }

  candidates.sort(
    (left, right) => left.range.start - right.range.start || left.range.end - right.range.end,
  );
  return candidates.length > 0
    ? { candidates, message: "" }
    : unavailable("当前材料中没有可定位的关键条款。");
}

export function keyClauseCategoryLabel(category: KeyClauseCategory): string {
  return KEY_CLAUSE_CATEGORIES.find((item) => item.id === category)?.label ?? category;
}
