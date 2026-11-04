// File metadata (manifest mirror):
// Purpose: Maps exact frozen Unicode quote ranges to observed native or OCR page regions, rejecting ambiguous text, numerical changes and unreliable geometry.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { sliceByCodePoints } from "./text";

export interface SourceBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface SourceWord {
  text: string;
  box: SourceBox;
  /** OCR transcription quality, never a risk confidence score. */
  confidence?: number;
}

export interface SourceQuote {
  id: string;
  quote: string;
  start: number;
  end: number;
  page: number | null;
}

export type SourceLocation =
  | { kind: "located"; boxes: SourceBox[] }
  | { kind: "wrong_page" | "not_found" | "ambiguous" | "unavailable" };

function validBox(box: SourceBox): boolean {
  return Object.values(box).every(Number.isFinite) && box.x >= 0 && box.y >= 0 &&
    box.width > 0 && box.height > 0 && box.x + box.width <= 1.000001 && box.y + box.height <= 1.000001;
}

const compact = (text: string) => text.replace(/\s/gu, "");

/** OCR punctuation glyphs vary; this never changes the frozen quote itself. */
function comparableGlyphs(text: string): string {
  const chars = Array.from(text);
  return chars.map((char, index) => {
    if (char === "：") return ":";
    if (char === "。" && !/[0-9]/.test(chars[index - 1] ?? "") && !/[0-9]/.test(chars[index + 1] ?? "")) return ".";
    return char;
  }).join("");
}

/** Presentation-only geometry. Never changes an evidence status or its frozen offsets. */
export function locateSourceQuote(
  frozenText: string,
  citation: SourceQuote,
  words: readonly SourceWord[],
  page: number,
): SourceLocation {
  if (citation.page !== null && citation.page !== page) return { kind: "wrong_page" };
  if (!citation.quote || sliceByCodePoints(frozenText, citation.start, citation.end) !== citation.quote)
    return { kind: "unavailable" };
  const needle = comparableGlyphs(compact(citation.quote));
  if (Array.from(needle).length < 2) return { kind: "unavailable" };
  let text = "";
  const wordAt: number[] = [];
  const boundaries = new Set<number>();
  for (const [index, word] of words.entries()) {
    boundaries.add(text.length);
    let offset = text.length;
    for (const character of word.text) {
      if (/\s/u.test(character)) boundaries.add(offset);
      else offset += character.length;
    }
    const part = compact(word.text);
    text += part;
    boundaries.add(text.length);
    for (let at = 0; at < part.length; at += 1) wordAt.push(index);
  }
  const comparableText = comparableGlyphs(text);
  const matches: number[] = [];
  for (let at = comparableText.indexOf(needle); at >= 0; at = comparableText.indexOf(needle, at + 1)) {
    const previous = text[at - 1] ?? "";
    const following = text[at + needle.length] ?? "";
    if (/^[0-9]/.test(needle) && /[0-9.,+\-−。．]/.test(previous)) continue;
    if (/[0-9]$/.test(needle) && /[0-9.,]/.test(following)) continue;
    if (/^[A-Za-z]/.test(needle) && /[A-Za-z0-9_]/.test(previous) && !boundaries.has(at)) continue;
    if (/[A-Za-z]$/.test(needle) && /[A-Za-z0-9_]/.test(following) && !boundaries.has(at + needle.length)) continue;
    matches.push(at);
    if (matches.length > 1) return { kind: "ambiguous" };
  }
  const start = matches[0];
  if (start === undefined) return { kind: "not_found" };
  const indices = [...new Set(wordAt.slice(start, start + needle.length))];
  if (!indices.length || indices.length > 512) return { kind: "unavailable" };
  const matched = indices.flatMap((index) => words[index] ? [words[index]!] : []);
  const canOmitSeparator = !/[0-9][:：]|[:：][0-9]/.test(compact(citation.quote));
  const layoutSeparator = (word: SourceWord) => canOmitSeparator && /^[:：]$/.test(word.text.trim());
  if (matched.some((word) => !validBox(word.box) ||
    (word.confidence !== undefined && (!Number.isFinite(word.confidence) || word.confidence < 0 ||
      (word.confidence < 70 && !layoutSeparator(word))))))
    return { kind: "unavailable" };
  // An isolated layout colon can have a poor OCR score despite clear adjacent
  // words. Keep it as matching context, omit its box, and retain strict quality
  // checks for words, numbers, units and number-adjacent punctuation.
  const boxes = matched.filter((word) => !layoutSeparator(word)).map((word) => word.box);
  return boxes.length ? { kind: "located", boxes } : { kind: "unavailable" };
}

/** Tesseract word-level TSV coordinates are observed pixels, converted to page proportions. */
export function parseOCRWords(tsv: string, width: number, height: number): SourceWord[] {
  if (!(width > 0 && height > 0) || !Number.isFinite(width + height)) return [];
  const rows = tsv.trim().split(/\r?\n/);
  const header = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext";
  if (rows[0] === header) rows.shift();
  else {
    // Tesseract.js returns the TSV body without the CLI's header row.
    const first = rows[0]?.split("\t") ?? [];
    if (first.length < 12 || first[0] !== "1" ||
      first.slice(0, 11).some((value) => value === "" || !Number.isFinite(Number(value)))) return [];
  }
  return rows.slice(0, 100_000).flatMap((row): SourceWord[] => {
    const cells = row.split("\t");
    if (cells.length < 12 || cells[0] !== "5") return [];
    const text = cells.slice(11).join("\t").trim();
    const box = { x: Number(cells[6]) / width, y: Number(cells[7]) / height,
      width: Number(cells[8]) / width, height: Number(cells[9]) / height };
    const confidence = Number(cells[10]);
    if (!text || !validBox(box) || !Number.isFinite(confidence)) return [];
    return [{ text, box, confidence }];
  });
}
