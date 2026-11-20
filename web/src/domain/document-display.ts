import { createTextIndex } from "./text";

type DisplaySpan = { start: number; end: number; kind: "hidden" | "image" | "heading" };
export function displaySpans(text: string): DisplaySpan[] {
  const spans: DisplaySpan[] = [];
  const offsets = createTextIndex(text).utf16Offsets;
  const positions = new Map(offsets.map((offset, index) => [offset, index]));
  const point = (offset: number) => positions.get(offset)!;
  for (const match of text.matchAll(/^(#{1,6})[ \t]+([^\n]*)/gm)) {
    const start = match.index,
      prefixEnd = start + match[0].length - match[2]!.length;
    spans.push(
      { start: point(start), end: point(prefixEnd), kind: "hidden" },
      { start: point(prefixEnd), end: point(start + match[0].length), kind: "heading" },
    );
  }
  for (const match of text.matchAll(/!\[[^\]\n]*\]\([^\n)]*\)/g))
    spans.push({
      start: point(match.index),
      end: point(match.index + match[0].length),
      kind: "image",
    });
  for (const match of text.matchAll(/(?:\.{3,}|…{2,}|[·．。]{3,})(?=\s*\d+\s*$)/gm))
    spans.push({
      start: point(match.index),
      end: point(match.index + match[0].length),
      kind: "hidden",
    });
  return spans;
}
