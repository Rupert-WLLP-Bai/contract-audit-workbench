// File metadata (manifest mirror):
// Purpose: Selects precise excerpts from frozen sources, groups contractual commitments for reading and preserves exact text proof and Unicode coordinates.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-19
// Revision: r4
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

// Automatic reading highlights stay short. Longer text can still be searched and quoted.
const MAX_READING_QUOTE_LENGTH = 180;
const MIN_READING_QUOTE_LENGTH = 5;

export const KEY_CLAUSE_CATEGORIES = [
  {
    id: "payment",
    label: "付款",
    keywords: [
      "付款",
      "支付",
      "价款",
      "结算",
      "预付款",
      "尾款",
      "账款",
      "发票",
      "合同总价",
      "合同金额",
      "采购金额",
    ],
  },
  {
    id: "delivery_acceptance",
    label: "交付验收",
    keywords: ["交付", "交货", "验收", "签收", "交付物", "验收标准", "里程碑", "巡检"],
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
    id: "warranty",
    label: "质保售后",
    keywords: ["质保", "保修", "质量保证", "售后", "维修", "缺陷责任"],
  },
  {
    id: "dispute_resolution",
    label: "争议解决",
    keywords: ["争议解决", "争议", "仲裁", "诉讼", "管辖", "适用法律"],
  },
  {
    id: "confidentiality_data",
    label: "保密数据",
    keywords: ["保密", "商业秘密", "数据", "个人信息", "信息安全", "隐私", "网络安全"],
  },
] as const;

export type KeyClauseCategory = (typeof KEY_CLAUSE_CATEGORIES)[number]["id"];
export type KeyClauseKind = "structured" | "fragment";

export type ReadingGroup = "money" | "performance" | "rights";
export const READING_GROUPS: readonly { id: ReadingGroup; label: string }[] = [
  { id: "money", label: "金额与付款" },
  { id: "performance", label: "履约与期限" },
  { id: "rights", label: "权利与责任" },
];
export function readingGroupFor(
  candidate: Pick<KeyClauseCandidate, "categories" | "quote">,
): ReadingGroup {
  switch (candidate.categories[0]) {
    case "payment":
      return "money";
    case "term_termination":
      return /解除|终止/u.test(candidate.quote) ? "rights" : "performance";
    case "delivery_acceptance":
      // An acceptance condition attached to a payment is still a payment milestone.
      return candidate.categories.includes("payment") ? "money" : "performance";
    case "warranty":
      return "performance";
    default:
      return "rights";
  }
}

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
  allCandidates?: KeyClauseCandidate[];
  message: string;
  notice?: string;
}

interface RatedSentence {
  range: TextOffset;
  categories: readonly KeyClauseCategory[];
  primary: KeyClauseCategory;
  score: number;
  emphasized: boolean;
  key: string;
  source: ReviewDocument["clauses"][number] | undefined;
}

function parsedDocumentIdOf(document: ReviewDocument, scope: FrozenQuoteScope): string | null {
  const parsedDocumentId = document.parsed_document_id;
  if (!parsedDocumentId || parsedDocumentId.trim() === "") return null;
  const metadata = document.parsed_document;
  if (
    scope.sourceProofs?.some(
      (proof) =>
        proof.documentId === document.id &&
        proof.parsedDocumentId === parsedDocumentId &&
        proof.originalDigest === document.digest,
    )
  )
    return parsedDocumentId;
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

const PARTY =
  /甲方|乙方|丙方|采购[方人]|供应[方商]|委托[方人]|受托[方人]|承包[人方]|发包[人方]|服务方|双方|各方|任一方|任何一方|违约方/u;
const DIRECTION =
  /(?:应当|应于|应在|应自|应按|应向|应由|应[交提支履保承负赔退返履防采通约完享对给将妥指配]|须|必须|有权|不得|不应|不承担|不予|禁止|同意|承诺|负责|仅限|无权|可以|均可|可向|可要求)/u;
const QUANTIFIED_TERM =
  /[０-９0-9]+(?:[.,．，][０-９0-9]+)*\s*(?:个?月|个?自然日|个?工作日|天|日|年|小时|分钟|兆|％|%|万?元|亿?元|MB|GB|TB)|[零〇一二两三四五六七八九十百千]+(?:个?月|个?自然日|个?工作日|天|日|年|小时|分钟)|[零〇一二两三四五六七八九十壹贰叁肆伍陆柒捌玖拾佰仟百千万亿]+[元圆]/u;
const RECURRING_DUTY = /每(?:季度|月|年|周|日).{0,20}(?:提交|提供|巡检|检查|报告|履行)/u;
const TRIGGER =
  /之日起|之后|之前|收到|经.*确认|如遇|如果|若|未能|逾期|届满|期满|提前|自.*起|期限内|[前后](?:的)?[０-９0-9一二两三四五六七八九十百]/u;
const PLACEHOLDER =
  /\[\s*(?:待填|填写|待定|可选)[^\]]*\]|【\s*(?:待填|填写|待定|可选)[^】]*】|_{2,}|(?:待填(?:写)?|待补充|尚无具体约定|不作具体约定|无具体约定|本条不适用|本项不适用|XX\s*(?:天|日|元|个月|年))|^(?:术语解释|示例|例如|参考模板|填写说明|目录|关键词)[：:\s]/u;
const LEADING_EXCEPTION = /^(?:但是|但(?!愿)|除非|除.{0,24}外|惟|唯在)/u;
const INCOMPLETE = /[：:、，,；;]$/u;
// Numbering and list markers outside the parser's body offset can be dropped.
const SECTION_PREFIX =
  /^(?:(?:\p{Extended_Pictographic}|[•·▪])\s*|第[零〇一二两三四五六七八九十百０-９0-9]+[条章节][：:、]?\s*|[（(]?[０-９0-9]+[.．、)）]\s*)+$/u;
const CONTRACT_REFERENCE = /本合同|本协议|合作方|本项目/u;

const TOPIC_RULES: Record<KeyClauseCategory, { action: RegExp; specific: RegExp }> = {
  payment: {
    action: /支付|付款|付清|付讫|付至|结算|开具.*发票|退还.*[款费]|退还.*保证金|付款条件/u,
    specific:
      /(?:合同(?:总价|价款|金额)|采购金额|含税总价|结算总额|付款比例)[\s：:为计人民币]*(?:[０-９0-9]|[零〇一二两三四五六七八九十壹贰叁肆伍陆柒捌玖拾佰仟百千万亿]+(?:[万亿]?[元圆]))|(?:预付款|首付款|进度款|尾款|价款|款项)[^。！？]{0,20}(?:[０-９0-9]+[％%]|百分之[一二两三四五六七八九十百]+|[０-９0-9]+万?元)|(?:付款|价款|尾款|款项)[^。！？]{0,12}(?:[０-９0-9]+|[一二两三四五六七八九十百]+)(?:个?自然日|个?工作日|天|日|月)内[^。！？]{0,8}(?:支付|付款|付清)/u,
  },
  delivery_acceptance: {
    action: /交付|交货|交接|签收|验收|上线|供货|提交|巡检|整改|验收.*视为/u,
    specific:
      /(?:(?:交付|交货|竣工|验收)[^。！？]{0,30}(?:不得超过|不超过|\d+(?:个)?(?:工作日|天|日|月)|逾期视为))|(?:验收合格(?:后|之日|率))|(?:付款|确认|批准|签收)后[^。！？]{0,12}(?:交付|交货|履行)/u,
  },
  term_termination: {
    action: /终止|解除|续约|展期|生效|延长|失效|届满|到期/u,
    specific:
      /(?:签字|盖章|签订|签署).{0,20}(?:生效|起生效)|(?:有效期|期限).{0,12}(?:\d|[一二两三四五六七八九十]+(?:个?月|工作日|天|年))|(?:自\d{4}年)|(?:届满前[^。！？]{0,20}(?:自动续|书面通知))/u,
  },
  liability_compensation: {
    action: /承担|赔偿|补偿|支付|免责|负责|责任限制|违约金|免除|赔付|承担连带责任/u,
    specific:
      /(?:违约金|赔偿|责任上限)[^。！？]{0,24}(?:上限|以[^。！？]{1,20}为限|[０-９0-9]+[％%]|[０-９0-9]+万?元|实际损失)|(?:不(?:再)?承担(?:任何|连带|间接|直接)责任)/u,
  },
  warranty: {
    action: /保修|质保|保用|维修|修复|响应|更换|提供|保证|售后/u,
    specific:
      /(?:质保|保修|保用|售后|质量保证)[^。！？]{0,18}(?:[０-９0-9]+|[一二两三四五六七八九十]+)\s*(?:个?月|个?工作日|天|日|年|小时)/u,
  },
  dispute_resolution: {
    action: /提交.*仲裁|提起诉讼|提起仲裁|向.*法院.*起诉|有管辖权|管辖法院|适用.*法律/u,
    specific:
      /(?:协商不成|调解不成|任何一方|均可|应|由|向|提交|提请|所在地).{0,38}(?:仲裁委|仲裁院|仲裁机构|人民法院|有管辖权的法院)|(?:适用[^。！？]{1,18}(?:法律|法律法规))|(?:争议|纠纷)[^。！？]{0,14}(?:提交|提请|申请)[^。！？]{0,14}仲裁/u,
  },
  confidentiality_data: {
    action:
      /披露|保守秘密|保护|泄露|泄漏|销毁|删除|脱敏|加密|出境|仅限|保密义务|转交|提供给|转让|传输|返还/u,
    specific:
      /(?:未经.{0,40}(?:同意|许可).{0,18}(?:不得|不应|不可).{0,24}(?:披露|转交|转让|共享|公开))|(?:不得|不应).{0,10}(?:泄露|泄漏|外传|出境|擅自披露)|(?:依法).{0,6}(?:删除|销毁).{0,12}(?:数据|个人信息)/u,
  },
};
const ADDITIONAL_TOPIC: Partial<Record<KeyClauseCategory, RegExp>> = {
  payment: /含税总价|付款比例|付款条件/u,
  term_termination: /生效|签字.{0,12}盖章/u,
  dispute_resolution: /法院|起诉|诉至/u,
  confidentiality_data: /披露|泄露|泄漏|数据出境/u,
};
const SIMPLE_AMOUNT =
  /^(合同(?:总价|价款|金额)|采购金额)[\s：:为计人民币]*([０-９0-9.．,，]+)(万?元|亿元)(?:整)?[。.]?$/u;
const DEFERRED = /(?:另行|后续|届时)(?:商定|协商|确定|议定|约定)|(?:尚待|待双方)确定/u;

function duplicateKey(text: string): string {
  const normalized = text
    .normalize("NFKC")
    .replace(/\s+/gu, "")
    .replace(/[。！？.!?]+$/gu, "");
  const amount = SIMPLE_AMOUNT.exec(normalized);
  if (!amount?.[1] || !amount[2] || !amount[3]) return normalized;
  const kind = amount[1].startsWith("合同") ? "合同" : "采购";
  return `${kind}金额${amount[2]}${amount[3]}`;
}

function rankSentence(
  text: string,
): Pick<RatedSentence, "categories" | "primary" | "score"> | null {
  if (PLACEHOLDER.test(text) || INCOMPLETE.test(text) || LEADING_EXCEPTION.test(text)) return null;
  const party = PARTY.test(text);
  const directive = DIRECTION.test(text);
  const hasNumber = QUANTIFIED_TERM.test(text);
  const trigger = TRIGGER.test(text);
  const contractRef = CONTRACT_REFERENCE.test(text);

  const ranked = KEY_CLAUSE_CATEGORIES.flatMap((category) => {
    const rule = TOPIC_RULES[category.id];
    if (
      !category.keywords.some((word) => text.includes(word)) &&
      !ADDITIONAL_TOPIC[category.id]?.test(text)
    )
      return [];
    const specific = rule.specific.test(text);
    const action = rule.action.test(text);
    if (!specific && !(action && (directive || party || (hasNumber && trigger)))) return [];
    if (!specific && !hasNumber && DEFERRED.test(text)) return [];
    if (
      !specific &&
      category.id === "confidentiality_data" &&
      text.includes("数据") &&
      !/保密|披露|个人信息|泄露|泄漏|销毁|删除|脱敏|加密|出境|转交|提供给|传输|转让/u.test(text)
    )
      return [];
    const score =
      5 * Number(specific) +
      2 * Number(action) +
      2 * Number(directive) +
      3 * Number(party) +
      2 * Number(hasNumber) +
      Number(trigger) +
      Number(contractRef);
    return [{ id: category.id, score }];
  }).sort((left, right) => right.score - left.score);
  const strongest = ranked[0];
  return strongest && strongest.score >= 5
    ? {
        categories: ranked.map((item) => item.id),
        primary: strongest.id,
        score: strongest.score,
      }
    : null;
}

function includesRange(outer: TextOffset, inner: TextOffset): boolean {
  return outer.start <= inner.start && outer.end >= inner.end;
}

function overlapsRange(left: TextOffset, right: TextOffset): boolean {
  return left.start < right.end && right.start < left.end;
}

function rateSentences(
  document: ReviewDocument,
  index: CodePointTextIndex,
  ranges: readonly TextOffset[],
): RatedSentence[] {
  // Intersections with a malformed or misaligned structure never become quotes.
  const sources = document.clauses
    .filter(
      ({ text_offset }) =>
        Number.isInteger(text_offset.start) &&
        Number.isInteger(text_offset.end) &&
        text_offset.start >= 0 &&
        text_offset.end > text_offset.start &&
        text_offset.end <= index.codePoints.length,
    )
    .sort((left, right) => {
      const leftLength = left.text_offset.end - left.text_offset.start;
      const rightLength = right.text_offset.end - right.text_offset.start;
      return leftLength - rightLength || left.text_offset.start - right.text_offset.start;
    });
  const rated: RatedSentence[] = [];
  for (let indexOfRange = 0; indexOfRange < ranges.length; indexOfRange += 1) {
    const first = ranges[indexOfRange]!;
    let range = first;
    // A sentence that supplies an exception belongs with the preceding obligation.
    for (let next = ranges[indexOfRange + 1]; next; next = ranges[indexOfRange + 1]) {
      const text = sliceByCodePoints(index.text, next.start, next.end, index);
      if (text === null || !LEADING_EXCEPTION.test(text)) break;
      range = { start: first.start, end: next.end };
      indexOfRange += 1;
    }
    let source = sources.find(({ text_offset }) => includesRange(text_offset, range));
    if (document.clauses.length > 0 && !source) {
      source = sources.find(({ text_offset: node }) => {
        if (node.start <= range.start || node.start >= range.end || node.end < range.end) {
          return false;
        }
        const prefix = sliceByCodePoints(index.text, range.start, node.start, index);
        return prefix !== null && SECTION_PREFIX.test(prefix.trim());
      });
      if (!source) continue;
      range = { start: source.text_offset.start, end: range.end };
    }
    const length = range.end - range.start;
    if (length < MIN_READING_QUOTE_LENGTH || length > MAX_READING_QUOTE_LENGTH) continue;
    const text = sliceByCodePoints(index.text, range.start, range.end, index);
    if (text === null) continue;
    const fit = rankSentence(text);
    if (fit) {
      const specific = TOPIC_RULES[fit.primary].specific.test(text);
      const quantified =
        QUANTIFIED_TERM.test(text) &&
        (TRIGGER.test(text) || PARTY.test(text) || DIRECTION.test(text));
      rated.push({
        ...fit,
        emphasized: specific || quantified || RECURRING_DUTY.test(text),
        range,
        key: duplicateKey(text),
        source,
      });
    }
  }
  return rated;
}

function selectSentences(rated: RatedSentence[]): RatedSentence[] {
  const candidates = rated.sort(
    (left, right) =>
      right.score - left.score ||
      left.range.end - left.range.start - (right.range.end - right.range.start) ||
      left.range.start - right.range.start,
  );
  const selected: RatedSentence[] = [];
  const seenQuotes = new Set<string>();

  for (const candidate of candidates) {
    if (
      seenQuotes.has(candidate.key) ||
      selected.some(({ range }) => overlapsRange(range, candidate.range))
    )
      continue;
    selected.push(candidate);
    seenQuotes.add(candidate.key);
  }
  return selected.sort((left, right) => left.range.start - right.range.start);
}

/** Selects navigation-only excerpts in the current frozen source. */
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
  const parsedDocumentId = parsedDocumentIdOf(document, scope);
  if (!parsedDocumentId || !scope.parsedDocumentIds.includes(parsedDocumentId)) {
    return unavailable(
      scope.sourceFailureMessages?.[document.id] ??
        "当前材料不属于所选冻结尝试的解析版本，无法生成条款定位。",
    );
  }
  if (document.parse_status !== "ready" || document.text === null || document.text === "") {
    return unavailable("当前冻结材料没有可定位正文。");
  }

  const index = createTextIndex(document.text);
  const sentences = sentenceRanges(index);
  if (sentences === null) {
    return unavailable("当前浏览器不支持原生句段定位，因此没有可定位的关键条款。");
  }
  const picked = selectSentences(rateSentences(document, index, sentences));
  const candidates: KeyClauseCandidate[] = [];
  const allCandidates: KeyClauseCandidate[] = [];
  for (const { range, categories, source, emphasized } of picked) {
    const selection = createFrozenQuoteSelection(scope, document, range, index);
    if (!selection || !frozenQuoteMatchesDocument(selection, scope, document, index)) continue;

    const heading = source?.heading.trim() || null;
    const wholeNode = heading && source && includesRange(range, source.text_offset);
    const originalPage = source?.page;
    // The parser's page belongs to its original start. A later excerpt has no proven page.
    const page =
      wholeNode &&
      typeof originalPage === "number" &&
      Number.isInteger(originalPage) &&
      originalPage > 0
        ? originalPage
        : null;
    const candidate = candidateFromSelection(selection, parsedDocumentId, categories, {
      clauseId: source?.id || null,
      heading: heading ?? "正文片段/标题未识别",
      sourceLabel: wholeNode
        ? "结构化条款"
        : heading
          ? "正文片段/非完整条款"
          : "正文片段/标题未识别",
      kind: wholeNode ? "structured" : "fragment",
      page,
    });
    allCandidates.push(candidate);
    if (emphasized) candidates.push(candidate);
  }
  return allCandidates.length > 0
    ? {
        candidates,
        allCandidates,
        message: candidates.length ? "" : "当前没有重点，可显示更多条款。",
      }
    : unavailable("当前材料中没有可定位的关键条款。");
}

export function keyClauseCategoryLabel(category: KeyClauseCategory): string {
  return KEY_CLAUSE_CATEGORIES.find((item) => item.id === category)?.label ?? category;
}
