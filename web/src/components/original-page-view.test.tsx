// File metadata (manifest mirror):
// Purpose: Checks native and scanned-page overlays, immutable-source mismatch refusal, repeated-text handling, pagination and recognition cleanup.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import OriginalPageView from "./original-page-view";
import type { SourceWord } from "../domain/source-annotation";
import { OriginalReadError } from "../features/review/original-source";

const mock = vi.hoisted(() => ({ read: vi.fn(), verify: vi.fn(), open: vi.fn(), recognize: vi.fn(), dispose: vi.fn(), render: vi.fn() }));
vi.mock("../api/http", async (original) => ({ ...await original<typeof import("../api/http")>(), requestBytes: mock.read }));
vi.mock("../features/review/original-source", async (original) => ({
  ...await original<typeof import("../features/review/original-source")>(),
  originalKind: (name: string) => name.endsWith(".pdf") ? "pdf" : "image",
  verifyOriginalDigest: mock.verify, openOriginalSource: mock.open, recognizePageWords: mock.recognize,
}));

const words: SourceWord[] = [{ text: "100万元", box: { x: 0.1, y: 0.2, width: 0.3, height: 0.05 } }];
const props = {
  scope: { caseId: "case-1", contextKey: "actor-1:attempt-1", digests: new Map() },
  documentId: "doc-1", filename: "合同.pdf", digest: "a".repeat(64), text: "😀甲方应支付100万元。",
  quotes: [{ id: "a", start: 6, end: 11, quote: "100万元", page: 2 }],
};

beforeEach(() => {
  vi.clearAllMocks();
  mock.read.mockResolvedValue(new Uint8Array([1]));
  mock.verify.mockResolvedValue(undefined);
  mock.render.mockResolvedValue(words);
  mock.recognize.mockResolvedValue(words);
  mock.open.mockResolvedValue({ pages: 3, kind: "pdf", render: mock.render, dispose: mock.dispose });
});

describe("original page review", () => {
  it("opens the frozen original at the cited page and overlays observed native text regions", async () => {
    const view = render(<OriginalPageView {...props} />);
    await screen.findByText(/已标出当前页/);
    expect(mock.read.mock.calls[0]?.[0]).toBe("/api/cases/case-1/documents/doc-1/original");
    expect(mock.verify).toHaveBeenCalledWith(new Uint8Array([1]), props.digest);
    expect(mock.render.mock.calls[0]?.[0]).toBe(2);
    expect(view.container.querySelector(".original-quote-region")).toHaveStyle({ left: "10%", top: "20%" });
    expect(mock.recognize).not.toHaveBeenCalled();
  });

  it("locates a scanned page using its observed OCR regions", async () => {
    mock.render.mockResolvedValue([]);
    const view = render(<OriginalPageView {...props} />);
    await screen.findByText(/已标出当前页/);
    expect(mock.recognize).toHaveBeenCalledTimes(1);
    expect(view.container.querySelectorAll(".original-quote-region")).toHaveLength(1);
  });

  it("refuses another original and does not open a mismatched file", async () => {
    mock.verify.mockRejectedValue(new OriginalReadError("读取的原件与本次审查材料不一致，未显示标注"));
    const view = render(<OriginalPageView {...props} />);
    await screen.findByText(/原件与本次审查材料不一致/);
    expect(mock.open).not.toHaveBeenCalled();
    expect(view.container.querySelector(".original-quote-region")).toBeNull();
  });

  it("does not guess among repeated text and clears marks when turning to another page", async () => {
    mock.render.mockResolvedValue([...words, ...words]);
    const view = render(<OriginalPageView {...props} />);
    await screen.findByText(/有多处相同文字/);
    expect(mock.recognize).not.toHaveBeenCalled();
    expect(view.container.querySelector(".original-quote-region")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(mock.render.mock.calls.at(-1)?.[0]).toBe(3));
    expect(view.container.querySelector(".original-quote-region")).toBeNull();
  });

  it("cancels pending recognition and releases the original when leaving the reader", async () => {
    mock.render.mockResolvedValue([]);
    mock.recognize.mockImplementation(() => new Promise(() => undefined));
    const view = render(<OriginalPageView {...props} />);
    await waitFor(() => expect(mock.recognize).toHaveBeenCalled());
    const signal = mock.recognize.mock.calls[0]?.[1] as AbortSignal;
    view.unmount();
    expect(signal.aborted).toBe(true);
    expect(mock.dispose).toHaveBeenCalledTimes(1);
  });
});
