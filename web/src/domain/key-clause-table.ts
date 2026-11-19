import type { ReviewTableCell, TextOffset } from "../api/types";
import { sliceByCodePoints, type CodePointTextIndex } from "./text";

type TermKind = "total" | "amount" | "unitPrice" | "deadline" | "warranty";
type Term = { kind: TermKind; heading: string; hasTimeUnit: boolean };
type Cell = ReviewTableCell & { text: string };
export type TableValue = {
  range: TextOffset;
  heading: string;
  key: string;
  category: "payment" | "term_termination" | "warranty";
  emphasized: boolean;
};

const MONEY_LABEL =
  /^(?:人民币|合同|采购|服务|项目|报价|应付|含税|不含税|未税|税前|税后|价税|合计){0,5}(合同总价|总价|总额|金额|价款|单价|小计|合计|总计|费用)(?:\([^()]{1,16}\))?[:：]?$/u;
const MONEY_TOTAL =
  /^(?:(?:含税|不含税|未税|税前|税后|价税|人民币|金额){0,4}(?:合计|总计|小计)|(?:合计|总计|小计)金额)(?:\([^()]{1,16}\))?[:：]?$/u;
const TIME_LABEL =
  /^(?:(?:合同|项目|服务|履约|交付|交货|供货|验收|整改|交接|响应|完成){1,2}(?:期限|有效期|周期|时间)|有效期|履约期|服务期|质保(?:期|期限|时间)|保修(?:期|期限|时间))(?:\([^()]{1,12}\))?[:：]?$/u;
const TIME_UNIT = /\((?:个?月|个?(?:自然|工作)?日|天|年|小时|分钟)\)/u;
const MONEY_VALUE = /^(?:人民币|¥)?[-+]?\d{1,16}(?:,\d{3})*(?:\.\d{1,6})?(?:元|万元|亿元)?$/u;
const TIME_VALUE =
  /^(?:[0-9零〇一二两三四五六七八九十百千]+(?:个?月|个?(?:自然|工作)?日|天|年|小时|分钟)(?:内|以内|以上)?|(?:自)?\d{4}(?:[.\-/]\d{1,2}[.\-/]\d{1,2}|年\d{1,2}月\d{1,2}日)(?:[至到–—~]\d{4}(?:[.\-/]\d{1,2}[.\-/]\d{1,2}|年\d{1,2}月\d{1,2}日))?)$/u;

function compact(text: string): string {
  return text.normalize("NFKC").replace(/\s+/gu, "");
}

function termOf(text: string): Term | undefined {
  if (text.length > 40 || /[\r\n\t]/u.test(text)) return undefined;
  const value = compact(text);
  const amount = MONEY_LABEL.exec(value);
  if (amount) {
    const kind =
      amount[1] === "单价"
        ? "unitPrice"
        : /总价|总额|小计|合计|总计/u.test(amount[1] ?? "")
          ? "total"
          : "amount";
    return { kind, heading: text, hasTimeUnit: false };
  }
  if (!TIME_LABEL.test(value)) return undefined;
  return {
    kind: /质保|保修/u.test(value) ? "warranty" : "deadline",
    heading: text,
    hasTimeUnit: TIME_UNIT.test(value),
  };
}

function valueMatches(text: string, term: Term): boolean {
  if (text.length > 56 || /[\r\n\t]/u.test(text)) return false;
  const value = compact(text);
  if (term.kind === "deadline" || term.kind === "warranty")
    return TIME_VALUE.test(value) || (term.hasTimeUnit && /^\d{1,5}(?:\.\d{1,2})?$/u.test(value));
  return MONEY_VALUE.test(value);
}

function tableRows(cells: ReviewTableCell[], index: CodePointTextIndex): Cell[][] {
  const rows = new Map<number, Cell[]>();
  cells.sort((left, right) => left.row - right.row || left.column - right.column);
  let previousEnd = 0;
  for (const cell of cells) {
    const { column, row, text_offset: range } = cell;
    const line = rows.get(row) ?? [];
    if (
      !Number.isSafeInteger(row) ||
      row < 1 ||
      !Number.isSafeInteger(column) ||
      column < 1 ||
      range.start < previousEnd ||
      line.at(-1)?.column === column
    )
      return [];
    const text = sliceByCodePoints(index.text, range.start, range.end, index);
    if (!text?.trim()) return [];
    line.push({ ...cell, text });
    rows.set(row, line);
    previousEnd = range.end;
  }
  return [...rows.values()];
}

function identifiedValues(rows: Cell[][]): TableValue[] {
  const firstGridRow = rows.find((row) => row.length > 1);
  const headings = new Map(firstGridRow?.map(({ column, text }) => [column, termOf(text)]));
  const out: TableValue[] = [];
  for (const row of rows) {
    const [label] = row;
    if (!label) continue;
    const rowTerm = row.length === 2 ? termOf(label.text) : undefined;
    const aggregate = label.text.length <= 40 && MONEY_TOTAL.test(compact(label.text));
    for (const cell of row.slice(1)) {
      const headerTerm = row !== firstGridRow ? headings.get(cell.column) : undefined;
      const term = headerTerm ?? rowTerm;
      if (!term || !valueMatches(cell.text, term)) continue;
      const category =
        term.kind === "warranty"
          ? "warranty"
          : term.kind === "deadline"
            ? "term_termination"
            : "payment";
      const namedRow =
        headerTerm && label.text.length <= 32 && !MONEY_VALUE.test(compact(label.text));
      out.push({
        range: cell.text_offset,
        heading: namedRow ? `${label.text} · ${term.heading}` : term.heading,
        key: JSON.stringify([cell.table_id, cell.row, cell.column]),
        category,
        emphasized:
          category !== "payment" ||
          (term.kind !== "unitPrice" && (aggregate || Boolean(rowTerm) || term.kind === "total")),
      });
    }
  }
  return out;
}

/** Classify cells from exact frozen coordinates. Sparse and merged grid columns are kept. */
export function frozenTableValues(
  cells: readonly ReviewTableCell[] | undefined,
  index: CodePointTextIndex,
): TableValue[] {
  if (!cells?.length || cells.length > 4096) return [];
  const tables = new Map<string, ReviewTableCell[]>();
  for (const cell of cells) {
    const group = tables.get(cell.table_id) ?? [];
    group.push(cell);
    tables.set(cell.table_id, group);
  }
  return [...tables.values()].flatMap((table) => identifiedValues(tableRows(table, index)));
}
