// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “frozen material span offsets”; representative assertions cover converts browser UTF-16 selection offsets to Unicode codepoint offsets, rejects empty, reversed and out-of-bounds selections. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, it } from "vitest";
import { runeSpanFromUTF16 } from "./material-anchor";

describe("frozen material span offsets", () => {
  it("converts browser UTF-16 selection offsets to Unicode codepoint offsets", () => {
    expect(runeSpanFromUTF16("甲😀乙", 1, 3)).toEqual({ start: 1, end: 2, quote: "😀" });
    expect(runeSpanFromUTF16("甲😀乙", 0, 4)).toEqual({ start: 0, end: 3, quote: "甲😀乙" });
  });

  it("rejects empty, reversed and out-of-bounds selections", () => {
    expect(runeSpanFromUTF16("abc", 1, 1)).toBeNull();
    expect(runeSpanFromUTF16("abc", 2, 1)).toBeNull();
    expect(runeSpanFromUTF16("abc", 0, 4)).toBeNull();
  });
});
