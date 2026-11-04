// File metadata (manifest mirror):
// Purpose: Checks cancellation while the local OCR worker is loading or recognizing, settling the reader promptly and discarding late workers before they receive original pixels.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import { recognizePageWords } from "./original-source";

const create = vi.hoisted(() => vi.fn());
vi.mock("tesseract.js", async (original) => ({ ...await original<typeof import("tesseract.js")>(), createWorker: create }));
beforeEach(() => vi.clearAllMocks());

describe("current-page recognition cancellation", () => {
  it("settles promptly if recognition is cancelled while the local worker is still loading", async () => {
    const terminate = vi.fn(async () => undefined);
    const recognize = vi.fn();
    let ready!: (worker: { terminate: typeof terminate; recognize: typeof recognize }) => void;
    create.mockImplementation(() => new Promise((resolve) => { ready = resolve; }));
    const controller = new AbortController();
    const pending = recognizePageWords(document.createElement("canvas"), controller.signal, () => undefined);
    await waitFor(() => expect(create).toHaveBeenCalled());
    controller.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    ready({ terminate, recognize });
    await waitFor(() => expect(terminate).toHaveBeenCalled());
    expect(recognize).not.toHaveBeenCalled();
  });

  it("settles promptly even when a terminated recognition job does not reject its own promise", async () => {
    const terminate = vi.fn(async () => undefined);
    const recognize = vi.fn(() => new Promise(() => undefined));
    create.mockResolvedValue({ terminate, recognize, setParameters: vi.fn(async () => undefined) });
    const controller = new AbortController();
    const pending = recognizePageWords(document.createElement("canvas"), controller.signal, () => undefined);
    await waitFor(() => expect(recognize).toHaveBeenCalled());
    controller.abort(new Error("当前页识别超时"));
    await expect(pending).rejects.toThrow("当前页识别超时");
    expect(terminate).toHaveBeenCalled();
  });
});
