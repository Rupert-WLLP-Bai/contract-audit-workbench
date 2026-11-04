// File metadata (manifest mirror):
// Purpose: Uses synthetic native PDFs, scanned PDFs and PNG fixtures to check actual original-byte verification, rendering and OCR geometry with pinned local resources.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// @vitest-environment node
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, copyFile, symlink, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createCanvas, DOMMatrix, ImageData, Path2D } from "@napi-rs/canvas";
import { createWorker } from "tesseract.js";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";
import { locateSourceQuote, parseOCRWords } from "../../domain/source-annotation";
import { configureOriginalOCR, openOriginalSource, verifyOriginalDigest, type OriginalSource } from "./original-source";

const require = createRequire(import.meta.url);
const fixtures = resolve(process.cwd(), "../internal/documentparse/testdata/pdf");
let assets: string;

beforeAll(async () => {
  assets = await mkdtemp(join(tmpdir(), "caw-original-reader-"));
  const pdf = dirname(require.resolve("pdfjs-dist/package.json"));
  await mkdir(join(assets, "document-reader/pdf"), { recursive: true });
  await mkdir(join(assets, "document-reader/ocr/lang"), { recursive: true });
  await writeFile(join(assets, "package.json"), '{"type":"module"}');
  await copyFile(join(pdf, "legacy/build/pdf.worker.min.mjs"), join(assets, "document-reader/pdf/pdf.worker.min.js"));
  for (const [target, source] of [["cmaps", "cmaps"], ["fonts", "standard_fonts"], ["wasm", "wasm"]])
    await symlink(join(pdf, source!), join(assets, `document-reader/pdf/${target}`));
  for (const language of ["eng", "chi_sim"]) {
    const data = dirname(require.resolve(`@tesseract.js-data/${language}/package.json`));
    await copyFile(join(data, `4.0.0_best_int/${language}.traineddata.gz`), join(assets, `document-reader/ocr/lang/${language}.traineddata.gz`));
  }
  vi.stubEnv("BASE_URL", pathToFileURL(`${assets}/`).href);
  vi.stubGlobal("DOMMatrix", DOMMatrix);
  vi.stubGlobal("ImageData", ImageData);
  vi.stubGlobal("Path2D", Path2D);
});

afterAll(async () => {
  vi.unstubAllEnvs(); vi.unstubAllGlobals();
  if (assets) await rm(assets, { recursive: true, force: true });
});

describe("original PDF and scan adapters with synthetic files", () => {
  it("checks original bytes before rendering, including a mismatched immutable source", async () => {
    const bytes = new Uint8Array(await readFile(join(fixtures, "single-native.pdf")));
    const digest = createHash("sha256").update(bytes).digest("hex");
    await expect(verifyOriginalDigest(bytes, digest)).resolves.toBeUndefined();
    await expect(verifyOriginalDigest(bytes, "a".repeat(64))).rejects.toThrow(/原件与本次审查材料不一致/);
  });

  it("renders a real native PDF and locates its Chinese quote using the observed text geometry", async () => {
    const controller = new AbortController();
    const bytes = new Uint8Array(await readFile(join(fixtures, "single-native.pdf")));
    const source = await openOriginalSource(bytes, "pdf", controller.signal);
    try {
      const canvas = createCanvas(1, 1);
      const words = await source.render(1, canvas as unknown as HTMLCanvasElement, controller.signal);
      const quote = "合成单页合同：质保36个月。";
      const location = locateSourceQuote(quote, { id: "native", quote, start: 0, end: Array.from(quote).length, page: 1 }, words, 1);
      expect(location.kind).toBe("located");
      expect(canvas.width).toBeGreaterThan(100);
      expect(source.pages).toBe(1);
    } finally { source.dispose(); }
  });

  it("locates scanned PDF and standalone PNG regions with the shipped Chinese/English OCR data", async () => {
    const worker = await createWorker(["chi_sim", "eng"], 1, { langPath: join(assets, "document-reader/ocr/lang"), cacheMethod: "none" });
    await configureOriginalOCR(worker);
    const controller = new AbortController();
    const bytes = new Uint8Array(await readFile(join(fixtures, "scanned-chinese-table.pdf")));
    let source: OriginalSource | null = null;
    try {
      source = await openOriginalSource(bytes, "pdf", controller.signal);
      const canvas = createCanvas(1, 1);
      expect(await source.render(1, canvas as unknown as HTMLCanvasElement, controller.signal)).toHaveLength(0);
      const images = [canvas.toBuffer("image/png"), await readFile(join(fixtures, "scan-source.png"))];
      for (const image of images) {
        const { data } = await worker.recognize(image, {}, { tsv: true, text: false });
        // PNG and rendered-PDF sizes differ; decode the observed image dimensions.
        const { loadImage } = await import("@napi-rs/canvas");
        const decoded = await loadImage(image);
        const words = parseOCRWords(data.tsv ?? "", decoded.width, decoded.height);
        for (const quote of ["Bandwidth 80 Mbps", "带宽80 Mbps。", "扫描条款：带宽80 Mbps。"]) {
          expect(locateSourceQuote(quote, { id: "scan", quote, start: 0, end: quote.length, page: 1 }, words, 1).kind,
            JSON.stringify({ quote, words })).toBe("located");
        }
      }
    } finally { await worker.terminate(); source?.dispose(); }
  }, 30_000);

  it("does not let a page cancelled during loading draw over the next page", async () => {
    const bytes = new Uint8Array(await readFile(join(fixtures, "single-native.pdf")));
    const source = await openOriginalSource(bytes, "pdf", new AbortController().signal);
    const frame = new AbortController();
    const canvas = createCanvas(1, 1);
    try {
      const rendering = source.render(1, canvas as unknown as HTMLCanvasElement, frame.signal);
      frame.abort();
      await expect(rendering).rejects.toMatchObject({ name: "AbortError" });
      expect(canvas.width).toBe(1);
    } finally { source.dispose(); }
  });
});
