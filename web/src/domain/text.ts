import type {
  EvidenceAnchor,
  ParsedDocumentMetadata,
  TextOffset,
} from "../api/types";

export type ResolvedAnchor =
  | { kind: "highlight"; start: number; end: number; page: number | null }
  | { kind: "snapshot"; quote: string; reason: string; page: number | null }
  | { kind: "unavailable"; reason: string };

const PARSE_MISMATCH = "证据绑定的解析产物与当前正文不一致，未在正文中高亮。";
const MISMATCH = "定位坐标与当前版本不匹配，未跳转到高亮位置。";
const MISSING = "找不到对应来源，不能把这条引用当成已验证证据。";
const NO_TEXT =
  "正文尚未返回。下面只显示保存下来的引用快照，没有在文档中定位。";
const QUOTE_MISMATCH = "引用原文与当前正文不一致，未跳转到高亮位置。";
const BBOX_ONLY = "只记录了版面坐标，当前视图不能据此高亮正文。";
const NO_LOCATION = "这条证据没有可用的原文位置。";

export function codePoints(text: string): string[] {
  return Array.from(text);
}

export interface CodePointTextIndex {
  readonly text: string;
  readonly codePoints: readonly string[];
  readonly utf16Offsets: readonly number[];
  readonly lowercaseCodePoints: readonly string[];
}

export function createTextIndex(text: string): CodePointTextIndex {
  const chars = codePoints(text);
  const utf16Offsets = new Array<number>(chars.length + 1);
  let utf16Offset = 0;
  for (let index = 0; index < chars.length; index += 1) {
    utf16Offsets[index] = utf16Offset;
    utf16Offset += chars[index]?.length ?? 0;
  }
  utf16Offsets[chars.length] = utf16Offset;
  return {
    text,
    codePoints: chars,
    utf16Offsets,
    lowercaseCodePoints: chars.map((char) => char.toLowerCase()),
  };
}

export function sliceByCodePoints(
  text: string,
  start: number,
  end: number,
  textIndex?: CodePointTextIndex,
): string | null {
  if (
    !Number.isInteger(start) ||
    !Number.isInteger(end) ||
    start < 0 ||
    start >= end
  ) {
    return null;
  }
  const index = textIndex?.text === text ? textIndex : null;
  const chars = index?.codePoints ?? codePoints(text);
  if (end > chars.length) return null;
  if (index) {
    return text.slice(index.utf16Offsets[start], index.utf16Offsets[end]);
  }
  return chars.slice(start, end).join("");
}

/** A located offset is trusted only when the quoted text is that span. */
export function offsetAgrees(hit: string, quote: string): boolean {
  return quote !== "" && hit === quote;
}

function snapshotOrUnavailable(
  quote: string,
  reason: string,
  page: number | null,
): ResolvedAnchor {
  if (quote.trim() === "") return { kind: "unavailable", reason };
  return { kind: "snapshot", quote, reason, page };
}

/**
 * Located anchors highlight a code-point span. Mismatch, missing, or a quote that
 * no longer matches the span stay on the saved quote and never jump elsewhere.
 */
export function resolveAnchor(
  text: string | null,
  anchor: EvidenceAnchor,
  parsedDocument: ParsedDocumentMetadata | null = null,
  publicParsedDocumentId: string | null = null,
  textIndex?: CodePointTextIndex,
): ResolvedAnchor {
  const page = anchor.page;
  if (anchor.status === "missing") {
    return snapshotOrUnavailable(
      anchor.quote,
      anchor.mismatch_reason ?? MISSING,
      null,
    );
  }
  if (anchor.status === "anchor_mismatch" || anchor.status === "unlocatable") {
    return snapshotOrUnavailable(
      anchor.quote,
      anchor.mismatch_reason ?? MISMATCH,
      null,
    );
  }
  const hasParseProvenance =
    anchor.parsed_document_id !== null || anchor.normalized_digest !== null;
  const publicParseMatches =
    parsedDocument === null &&
    publicParsedDocumentId !== null &&
    anchor.parsed_document_id === publicParsedDocumentId &&
    anchor.normalized_digest === null;
  if (
    hasParseProvenance && !publicParseMatches &&
    (parsedDocument === null ||
      parsedDocument.status !== "ready" ||
      parsedDocument.document_id !== anchor.document_version_id ||
      (anchor.parsed_document_id !== null &&
        parsedDocument.id !== anchor.parsed_document_id) ||
      (anchor.normalized_digest !== null &&
        parsedDocument.normalized_digest !== anchor.normalized_digest))
  ) {
    return snapshotOrUnavailable(anchor.quote, PARSE_MISMATCH, null);
  }
  if (text !== null && anchor.text_offset) {
    const hit = sliceByCodePoints(
      text,
      anchor.text_offset.start,
      anchor.text_offset.end,
      textIndex,
    );
    if (hit !== null && offsetAgrees(hit, anchor.quote)) {
      return {
        kind: "highlight",
        start: anchor.text_offset.start,
        end: anchor.text_offset.end,
        page,
      };
    }
    return snapshotOrUnavailable(anchor.quote, QUOTE_MISMATCH, null);
  }
  if (anchor.quote.trim() !== "") {
    return {
      kind: "snapshot",
      quote: anchor.quote,
      reason: text === null ? NO_TEXT : BBOX_ONLY,
      page: null,
    };
  }
  if (anchor.bbox) return { kind: "unavailable", reason: BBOX_ONLY };
  return { kind: "unavailable", reason: NO_LOCATION };
}

export function evidenceSourceMatches(
  anchor: EvidenceAnchor,
  documentId: string,
  parsedDocument: ParsedDocumentMetadata | null,
  publicParsedDocumentId: string | null = null,
): boolean {
  if (documentId !== anchor.document_version_id) return false;
  if (anchor.parsed_document_id === null && anchor.normalized_digest === null)
    return true;
  if (parsedDocument === null) {
    return publicParsedDocumentId !== null &&
      anchor.parsed_document_id === publicParsedDocumentId &&
      anchor.normalized_digest === null;
  }
  if (parsedDocument.document_id !== documentId) return false;
  return (
    (anchor.parsed_document_id === null ||
      parsedDocument.id === anchor.parsed_document_id) &&
    (anchor.normalized_digest === null ||
      parsedDocument.normalized_digest === anchor.normalized_digest)
  );
}

export interface TextSegment {
  text: string;
  start: number;
  highlighted: "evidence" | "find" | null;
  anchor: boolean;
}

export function segmentText(
  text: string,
  highlight: TextOffset | null,
  anchorStarts: number[],
  findRange: TextOffset | null = null,
  textIndex?: CodePointTextIndex,
): TextSegment[] {
  const codePointIndex = textIndex?.text === text ? textIndex : null;
  const chars = codePointIndex?.codePoints ?? codePoints(text);
  const breaks = new Set<number>([0, chars.length]);
  const addRange = (range: TextOffset | null) => {
    if (
      !range ||
      range.start < 0 ||
      range.end > chars.length ||
      range.start >= range.end
    )
      return;
    breaks.add(range.start);
    breaks.add(range.end);
  };
  addRange(highlight);
  addRange(findRange);
  for (const start of anchorStarts) {
    if (start >= 0 && start < chars.length) breaks.add(start);
  }
  const anchorStartSet = new Set(anchorStarts);
  const points = [...breaks].sort((left, right) => left - right);
  const segments: TextSegment[] = [];
  for (let index = 0; index < points.length - 1; index += 1) {
    const start = points[index];
    const end = points[index + 1];
    if (start === undefined || end === undefined || start === end) continue;
    const inEvidence = Boolean(
      highlight && start >= highlight.start && end <= highlight.end,
    );
    const inFind = Boolean(
      findRange && start >= findRange.start && end <= findRange.end,
    );
    segments.push({
      text: codePointIndex
        ? text.slice(
            codePointIndex.utf16Offsets[start],
            codePointIndex.utf16Offsets[end],
          )
        : chars.slice(start, end).join(""),
      start,
      highlighted: inEvidence ? "evidence" : inFind ? "find" : null,
      anchor: anchorStartSet.has(start),
    });
  }
  return segments;
}

export function findTextMatches(
  text: string,
  query: string,
  textIndex?: CodePointTextIndex,
): TextOffset[] {
  const needle = Array.from(query.trim().toLowerCase());
  if (needle.length === 0) return [];
  const index = textIndex?.text === text ? textIndex : null;
  const chars =
    index?.lowercaseCodePoints ??
    codePoints(text).map((char) => char.toLowerCase());
  const prefix = new Array<number>(needle.length).fill(0);
  for (
    let position = 1, matched = 0;
    position < needle.length;
    position += 1
  ) {
    while (matched > 0 && needle[position] !== needle[matched]) {
      matched = prefix[matched - 1] ?? 0;
    }
    if (needle[position] === needle[matched]) matched += 1;
    prefix[position] = matched;
  }

  const matches: TextOffset[] = [];
  let matched = 0;
  for (let position = 0; position < chars.length; position += 1) {
    const char = chars[position];
    while (matched > 0 && char !== needle[matched]) {
      matched = prefix[matched - 1] ?? 0;
    }
    if (char === needle[matched]) matched += 1;
    if (matched === needle.length) {
      const end = position + 1;
      matches.push({ start: end - needle.length, end });
      matched = 0;
    }
  }
  return matches;
}
