// File metadata (manifest mirror):
// Purpose: Loads bounded immutable PDF and image originals, extracts observed text geometry and recognizes current-page positions locally without writing audit state.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { SourceWord } from "../../domain/source-annotation";
import { parseOCRWords } from "../../domain/source-annotation";
import type { Worker } from "tesseract.js";

export type OriginalKind = "pdf" | "image";
export class OriginalReadError extends Error {}
export interface OriginalSource {
  pages: number;
  kind: OriginalKind;
  render: (page: number, canvas: HTMLCanvasElement, signal: AbortSignal) => Promise<SourceWord[]>;
  dispose: () => void;
}

export function originalKind(filename: string): OriginalKind | null {
  if (/\.pdf$/i.test(filename)) return "pdf";
  return /\.(?:png|jpe?g)$/i.test(filename) ? "image" : null;
}

const assets = () => new URL(`${import.meta.env.BASE_URL}document-reader/`, globalThis.location?.origin ?? "http://localhost").href;

function checkActive(signal: AbortSignal): void { signal.throwIfAborted(); }

function abortable<T>(work: Promise<T>, signal: AbortSignal, cancel: () => void = () => undefined): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const abort = () => {
      signal.removeEventListener("abort", abort);
      cancel(); reject(signal.reason ?? new DOMException("Aborted", "AbortError"));
    };
    signal.addEventListener("abort", abort, { once: true });
    if (signal.aborted) abort();
    work.then((value) => { signal.removeEventListener("abort", abort); resolve(value); },
      (error: unknown) => { signal.removeEventListener("abort", abort); reject(error); });
  });
}

export async function verifyOriginalDigest(bytes: Uint8Array<ArrayBuffer>, digest: string): Promise<void> {
  if (!/^[0-9a-f]{64}$/.test(digest)) throw new OriginalReadError("缺少原件版本信息，暂不能显示原件标注");
  const hash = await crypto.subtle.digest("SHA-256", bytes);
  const actual = [...new Uint8Array(hash)].map((value) => value.toString(16).padStart(2, "0")).join("");
  if (actual !== digest) throw new OriginalReadError("读取的原件与本次审查材料不一致，未显示标注");
}

export async function openOriginalSource(
  bytes: Uint8Array<ArrayBuffer>, kind: OriginalKind, signal: AbortSignal,
): Promise<OriginalSource> {
  checkActive(signal);
  if (kind === "image") return openImage(bytes, signal);
  if (new TextDecoder().decode(bytes.slice(0, 5)) !== "%PDF-") throw new OriginalReadError("原件不是可读取的 PDF 文件");
  const pdfjs = await abortable(import("pdfjs-dist/legacy/build/pdf.mjs"), signal);
  checkActive(signal);
  pdfjs.GlobalWorkerOptions.workerSrc = `${assets()}pdf/pdf.worker.min.js`;
  const loading = pdfjs.getDocument({
    data: bytes, cMapUrl: `${assets()}pdf/cmaps/`, cMapPacked: true,
    standardFontDataUrl: `${assets()}pdf/fonts/`, wasmUrl: `${assets()}pdf/wasm/`,
    isEvalSupported: false, enableXfa: false,
  });
  let disposed = false;
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    void loading.destroy().catch(() => undefined);
  };
  signal.addEventListener("abort", dispose, { once: true });
  try {
    const pdf = await loading.promise;
    checkActive(signal);
    if (pdf.numPages > 128) throw new OriginalReadError("原件页数超过页面预览范围，请下载后核对");
    return {
      pages: pdf.numPages, kind,
      dispose: () => { signal.removeEventListener("abort", dispose); dispose(); },
      async render(pageNumber, canvas, renderSignal) {
        checkActive(signal);
        checkActive(renderSignal);
        const page = await pdf.getPage(pageNumber);
        checkActive(signal);
        checkActive(renderSignal);
        const natural = page.getViewport({ scale: 1 });
        const scale = Math.min(2, 2400 / Math.max(natural.width, natural.height));
        const viewport = page.getViewport({ scale });
        if (!(viewport.width > 0 && viewport.height > 0)) throw new OriginalReadError("原件页面尺寸无法读取");
        canvas.width = Math.ceil(viewport.width);
        canvas.height = Math.ceil(viewport.height);
        const context = canvas.getContext("2d");
        if (!context) throw new OriginalReadError("当前浏览器无法显示原件，请切换文字原文或下载核对");
        const task = page.render({ canvas, canvasContext: context, viewport });
        const cancel = () => task.cancel();
        renderSignal.addEventListener("abort", cancel, { once: true });
        try { await task.promise; } finally { renderSignal.removeEventListener("abort", cancel); }
        checkActive(signal);
        checkActive(renderSignal);
        const content = await page.getTextContent();
        checkActive(signal);
        checkActive(renderSignal);
        return content.items.flatMap((item): SourceWord[] => {
          if (!("str" in item) || !item.str.trim()) return [];
          const transform = pdfjs.Util.transform(viewport.transform, item.transform);
          let angle = Math.atan2(transform[1]!, transform[0]!);
          const style = content.styles[item.fontName];
          if (style?.vertical) angle += Math.PI / 2;
          const height = Math.hypot(transform[2]!, transform[3]!);
          const width = (style?.vertical ? item.height : item.width) * scale;
          const ascent = (style?.ascent ?? (style?.descent === undefined ? 1 : 1 + style.descent)) * height;
          const x = transform[4]! + ascent * Math.sin(angle);
          const y = transform[5]! - ascent * Math.cos(angle);
          const corners = [[x, y], [x + width * Math.cos(angle), y + width * Math.sin(angle)],
            [x - height * Math.sin(angle), y + height * Math.cos(angle)],
            [x + width * Math.cos(angle) - height * Math.sin(angle), y + width * Math.sin(angle) + height * Math.cos(angle)]];
          const left = Math.max(0, Math.min(...corners.map((point) => point[0]!)));
          const top = Math.max(0, Math.min(...corners.map((point) => point[1]!)));
          const right = Math.min(canvas.width, Math.max(...corners.map((point) => point[0]!)));
          const bottom = Math.min(canvas.height, Math.max(...corners.map((point) => point[1]!)));
          if (right <= left || bottom <= top) return [];
          return [{ text: item.str, box: { x: left / canvas.width, y: top / canvas.height,
            width: (right - left) / canvas.width, height: (bottom - top) / canvas.height } }];
        });
      },
    };
  } catch (error) {
    signal.removeEventListener("abort", dispose);
    dispose();
    throw error;
  }
}

async function openImage(bytes: Uint8Array<ArrayBuffer>, signal: AbortSignal): Promise<OriginalSource> {
  const png = bytes[0] === 137 && bytes[1] === 80 && bytes[2] === 78 && bytes[3] === 71;
  const jpeg = bytes[0] === 255 && bytes[1] === 216 && bytes[2] === 255;
  if (!png && !jpeg) throw new OriginalReadError("原件不是可读取的 PNG 或 JPEG 扫描件");
  const url = URL.createObjectURL(new Blob([bytes], { type: png ? "image/png" : "image/jpeg" }));
  const image = new Image();
  image.src = url;
  try {
    await abortable(image.decode(), signal);
    checkActive(signal);
    if (image.naturalWidth * image.naturalHeight > 40_000_000) throw new OriginalReadError("扫描件尺寸超过页面预览范围，请下载后核对");
  } catch (error) { URL.revokeObjectURL(url); throw error; }
  return {
    pages: 1, kind: "image", dispose: () => { URL.revokeObjectURL(url); image.src = ""; },
    async render(page, canvas, renderSignal) {
      checkActive(signal);
      checkActive(renderSignal);
      if (page !== 1) throw new OriginalReadError("扫描件没有所选页面");
      const scale = Math.min(1, 2400 / Math.max(image.naturalWidth, image.naturalHeight));
      canvas.width = Math.ceil(image.naturalWidth * scale);
      canvas.height = Math.ceil(image.naturalHeight * scale);
      const context = canvas.getContext("2d");
      if (!context) throw new OriginalReadError("当前浏览器无法显示扫描件");
      context.drawImage(image, 0, 0, canvas.width, canvas.height);
      return [];
    },
  };
}

/** Local page geometry only: no model gateway call and no audit-state write. */
export async function configureOriginalOCR(worker: Pick<Worker, "setParameters">): Promise<void> {
  const { PSM } = await import("tesseract.js");
  // Contract pages contain separate paragraphs and tables; the library's
  // single-block default merges these regions and harms Chinese recognition.
  await worker.setParameters({ tessedit_pageseg_mode: PSM.AUTO });
}

export async function recognizePageWords(
  canvas: HTMLCanvasElement, signal: AbortSignal, progress: (value: number) => void,
): Promise<SourceWord[]> {
  const { createWorker } = await abortable(import("tesseract.js"), signal);
  checkActive(signal);
  const state: { worker: Awaited<ReturnType<typeof createWorker>> | null } = { worker: null };
  const work = (async () => {
    const worker = await createWorker(["chi_sim", "eng"], 1, {
      workerPath: `${assets()}ocr/worker.min.js`, corePath: `${assets()}ocr/core`,
      langPath: `${assets()}ocr/lang`, cacheMethod: "none", workerBlobURL: false,
      logger: (message) => { if (!signal.aborted && message.status === "recognizing text") progress(message.progress); },
    });
    state.worker = worker;
    if (signal.aborted) { await worker.terminate(); checkActive(signal); }
    await configureOriginalOCR(worker);
    checkActive(signal);
    const { data } = await worker.recognize(canvas, {}, { tsv: true, text: false });
    checkActive(signal);
    return parseOCRWords(data.tsv ?? "", canvas.width, canvas.height);
  })();
  try {
    return await abortable(work, signal, () => { void state.worker?.terminate(); });
  } finally {
    // The worker factory may resolve after cancellation. Its continuation above
    // releases that late worker before any original pixels are sent to it.
    await state.worker?.terminate();
  }
}
