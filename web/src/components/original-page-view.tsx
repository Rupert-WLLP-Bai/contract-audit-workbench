// File metadata (manifest mirror):
// Purpose: Reads and verifies immutable originals, renders one page at a time, displays per-quote location status and reuses an identity/page scoped bounded OCR word cache while cancelling obsolete render and recognition tasks.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-12
// Revision: r5
// Copyright: See LICENSE
// End file metadata.

import { useEffect, useMemo, useRef, useState } from "react";
import { Button, Select } from "antd";
import { ApiError, requestBytes, requestDerivedPreview } from "../api/http";
import { paths } from "../api/paths";
import {
  locateSourceQuote,
  type SourceLocation,
  type SourceQuote,
  type SourceWord,
} from "../domain/source-annotation";
import {
  openOriginalSource,
  OriginalReadError,
  originalKind,
  recognizePageWords,
  verifyOriginalDigest,
  type OriginalSource,
} from "../features/review/original-source";
import type { OriginalReadScope } from "./original-document-reader";

interface Props {
  scope: OriginalReadScope;
  documentId: string;
  filename: string;
  digest: string;
  text: string;
  quotes: readonly SourceQuote[];
}

function readError(error: unknown): string {
  return error instanceof OriginalReadError || error instanceof ApiError
    ? error.message
    : "原件暂时无法显示，请切换文字原文或下载原件核对";
}

const OCR_CACHE_LIMIT = 16;
const OCR_CACHE = new Map<string, SourceWord[]>();

function ocrCacheKeyOf(
  scope: OriginalReadScope,
  documentId: string,
  digest: string,
  page: number,
): string {
  return JSON.stringify([scope.contextKey, scope.caseId, documentId, digest, page]);
}

function readOCRCache(key: string): SourceWord[] | null {
  const value = OCR_CACHE.get(key);
  if (!value) return null;
  // Refresh LRU position without changing the immutable cached words.
  OCR_CACHE.delete(key);
  OCR_CACHE.set(key, value);
  return value;
}

function writeOCRCache(key: string, words: SourceWord[]): void {
  OCR_CACHE.delete(key);
  OCR_CACHE.set(key, words);
  while (OCR_CACHE.size > OCR_CACHE_LIMIT) {
    const oldest = OCR_CACHE.keys().next().value;
    if (oldest === undefined) break;
    OCR_CACHE.delete(oldest);
  }
}

/** Test-only reset; production cache remains bounded by OCR_CACHE_LIMIT. */
export function clearOriginalOCRCache(): void {
  OCR_CACHE.clear();
}

export default function OriginalPageView({
  scope,
  documentId,
  filename,
  digest,
  text,
  quotes,
}: Props) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [source, setSource] = useState<OriginalSource | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  const firstPage = quotes.find((quote) => quote.page !== null)?.page ?? 1;
  const [page, setPage] = useState(firstPage);
  const [zoom, setZoom] = useState(100);
  const [rendered, setRendered] = useState<{ page: number; words: SourceWord[] } | null>(null);
  const [ocr, setOCR] = useState<{ page: number; words: SourceWord[] } | null>(null);
  const [recognizing, setRecognizing] = useState(false);
  const [progress, setProgress] = useState(0);
  const [manualPage, setManualPage] = useState<number | null>(null);
  const [ocrError, setOCRError] = useState<string | null>(null);
  const scroll = useRef<HTMLDivElement>(null);
  const ocrCacheKey = ocrCacheKeyOf(scope, documentId, digest, page);

  useEffect(() => {
    setPage(firstPage);
    setManualPage(null);
  }, [firstPage]);

  useEffect(() => {
    const controller = new AbortController();
    const timer = window.setTimeout(
      () => controller.abort(new Error("原件读取超时，请重试")),
      90_000,
    );
    let opened: OriginalSource | null = null;
    setError(null);
    setSource(null);
    setRendered(null);
    setOCR(null);
    setOCRError(null);
    void (async () => {
      const isDOCX = /\.docx$/i.test(filename);
      let bytes: Uint8Array<ArrayBuffer>;
      if (isDOCX) {
        const derived = await requestDerivedPreview(
          paths.documentDerivedPreview(scope.caseId, documentId),
          controller.signal,
        );
        if (derived.originalDigest !== digest)
          throw new OriginalReadError("派生预览与本次审查冻结原件不一致，未显示标注");
        bytes = derived.bytes;
      } else {
        bytes = await requestBytes(
          paths.documentOriginal(scope.caseId, documentId),
          controller.signal,
        );
        await verifyOriginalDigest(bytes, digest);
      }
      controller.signal.throwIfAborted();
      const kind = isDOCX ? "pdf" : originalKind(filename);
      if (!kind) throw new OriginalReadError("该文件暂不支持原件标注，请查看文字原文");
      opened = await openOriginalSource(bytes, kind, controller.signal);
      controller.signal.throwIfAborted();
      setSource(opened);
      window.clearTimeout(timer);
    })().catch((caught: unknown) => {
      if (!controller.signal.aborted) setError(readError(caught));
      else if (
        controller.signal.reason instanceof Error &&
        controller.signal.reason.name !== "AbortError"
      )
        setError(controller.signal.reason.message);
    });
    return () => {
      controller.abort();
      window.clearTimeout(timer);
      opened?.dispose();
    };
  }, [scope.caseId, scope.contextKey, documentId, filename, digest, retry]);

  useEffect(() => {
    if (!source || !canvas.current) return;
    const controller = new AbortController();
    setRendered(null);
    setOCR(null);
    setOCRError(null);
    setError(null);
    if (page < 1 || page > source.pages) {
      setError("引用记录的页码不在这份原件中，请切换文字原文核对");
      return;
    }
    const target = canvas.current;
    void source
      .render(page, target, controller.signal)
      .then((words) => {
        if (!controller.signal.aborted) setRendered({ page, words });
      })
      .catch((caught: unknown) => {
        if (!controller.signal.aborted) setError(readError(caught));
      });
    return () => controller.abort();
  }, [source, page]);

  const canLocate =
    source?.kind === "image" || manualPage === page || quotes.some((quote) => quote.page === page);
  const nativeLocations = useMemo(
    () =>
      rendered?.page === page && canLocate
        ? quotes.map((quote) => locateSourceQuote(text, quote, rendered.words, page))
        : [],
    [rendered, page, canLocate, quotes, text],
  );
  const needOCR = nativeLocations.some((location) => location.kind === "not_found");

  useEffect(() => {
    if (!needOCR || !rendered || rendered.page !== page || !canvas.current || ocr?.page === page)
      return;
    const cached = readOCRCache(ocrCacheKey);
    if (cached) {
      setOCR({ page, words: cached });
      setOCRError(null);
      setRecognizing(false);
      return;
    }
    const controller = new AbortController();
    let active = true;
    const timer = window.setTimeout(
      () => controller.abort(new Error("当前页识别超时，可切换文字原文核对")),
      90_000,
    );
    setRecognizing(true);
    setProgress(0);
    setOCRError(null);
    void recognizePageWords(canvas.current, controller.signal, setProgress)
      .then((words) => {
        if (!controller.signal.aborted) {
          setOCR({ page, words });
          writeOCRCache(ocrCacheKey, words);
        }
      })
      .catch((caught: unknown) => {
        if (active && !controller.signal.aborted)
          setOCRError("当前页文字位置识别失败，请切换文字原文或下载原件核对。");
        else if (
          active &&
          controller.signal.reason instanceof Error &&
          controller.signal.reason.name !== "AbortError"
        )
          setOCRError(controller.signal.reason.message);
        // Aborting on a page or scope change must not leave an old result visible.
        void caught;
      })
      .finally(() => {
        window.clearTimeout(timer);
        if (active) setRecognizing(false);
      });
    return () => {
      active = false;
      controller.abort();
      window.clearTimeout(timer);
      setRecognizing(false);
    };
  }, [needOCR, rendered, page, ocr?.page, ocrCacheKey]);

  const locations = nativeLocations.map((location, index) =>
    location.kind === "not_found" && ocr?.page === page && quotes[index]
      ? locateSourceQuote(text, quotes[index]!, ocr.words, page)
      : location,
  );
  const boxes = locations.flatMap((location) =>
    location.kind === "located" ? location.boxes : [],
  );
  const locatedCount = locations.filter((location) => location.kind === "located").length;
  const locationLabel = (location: SourceLocation | undefined) => {
    if (!location)
      return quotes.length > 0 && quotePageOutsideCurrent() ? "未在当前页" : "等待原件渲染";
    switch (location.kind) {
      case "located":
        return "已标注";
      case "ambiguous":
        return "重复文字，需人工核对";
      case "wrong_page":
        return "页码不匹配";
      case "not_found":
        return "未匹配，需核对原文";
      case "unavailable":
        return "文字或定位质量不足，需切换文字原文";
    }
  };
  function quotePageOutsideCurrent(): boolean {
    return quotes.some((quote) => quote.page !== null && quote.page !== page);
  }
  const highlightKey = boxes.map((box) => `${box.x}:${box.y}:${box.width}:${box.height}`).join(";");
  useEffect(() => {
    const container = scroll.current;
    const region = container?.querySelector<HTMLElement>(".original-quote-region");
    if (!container || !region) return;
    container.scrollTo({
      top: Math.max(0, region.offsetTop - (container.clientHeight - region.offsetHeight) / 2),
      left: Math.max(0, region.offsetLeft - (container.clientWidth - region.offsetWidth) / 2),
    });
    const pane = container.closest<HTMLElement>(".col-doc");
    if (pane && pane.scrollHeight > pane.clientHeight) {
      pane.scrollTop +=
        region.getBoundingClientRect().top -
        pane.getBoundingClientRect().top -
        pane.clientHeight / 2;
    }
  }, [highlightKey, page, zoom]);

  return (
    <div className="original-page-view">
      <p className="original-page-filename">
        <strong>{filename}</strong>
      </p>
      {/\.docx$/i.test(filename) ? (
        <p className="meta">
          DOCX 版式预览为服务端受控转换的只读 PDF；原始合同字节仍以 SHA-256 绑定。
        </p>
      ) : null}
      {source ? (
        <div className="original-reader-toolbar" aria-label="原件翻页与缩放">
          <Button onClick={() => setPage(Math.max(1, page - 1))} disabled={page <= 1}>
            上一页
          </Button>
          <Select
            aria-label="原件页码"
            value={page}
            onChange={(value) => {
              setPage(value);
              setManualPage(null);
            }}
            options={Array.from({ length: source.pages }, (_, index) => ({
              value: index + 1,
              label: `第 ${index + 1} 页 / 共 ${source.pages} 页`,
            }))}
          />
          <Button
            onClick={() => setPage(Math.min(source.pages, page + 1))}
            disabled={page >= source.pages}
          >
            下一页
          </Button>
          <Button
            aria-label="缩小原件"
            onClick={() => setZoom(Math.max(50, zoom - 25))}
            disabled={zoom <= 50}
          >
            缩小
          </Button>
          <span>{zoom}%</span>
          <Button
            aria-label="放大原件"
            onClick={() => setZoom(Math.min(250, zoom + 25))}
            disabled={zoom >= 250}
          >
            放大
          </Button>
        </div>
      ) : null}
      {rendered && canLocate && quotes.length > 0 ? (
        <section className="original-locate-summary" aria-label="引用定位统计">
          <p role="status">
            当前页已标注 {locatedCount} 条，待核对 {quotes.length - locatedCount} 条
          </p>
          <ul>
            {quotes.map((quote, index) => (
              <li key={quote.id}>
                第 {index + 1} 处引用 · {quote.page === null ? "未知页码" : `第 ${quote.page} 页`} ·{" "}
                {locationLabel(locations[index])}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {error ? (
        <div role="alert">
          <p>{error}</p>
          <Button onClick={() => setRetry((value) => value + 1)}>重新读取原件</Button>
        </div>
      ) : null}
      {!source && !error ? <p role="status">正在读取本次审查使用的原件…</p> : null}
      {source && !rendered && !error ? <p role="status">正在显示第 {page} 页…</p> : null}
      {recognizing ? (
        <p role="status">正在定位当前页中的引用文字… {Math.round(progress * 100)}%</p>
      ) : null}
      {ocrError ? <p role="alert">{ocrError}</p> : null}
      {rendered && !canLocate && quotes.length > 0 ? (
        <p>
          请核对原件页码。<Button onClick={() => setManualPage(page)}>在本页查找引用</Button>
        </p>
      ) : null}
      {boxes.length > 0 ? (
        <p role="status">已标出当前页中与引用一致的文字区域，请对照原件核对。</p>
      ) : null}
      {rendered &&
      canLocate &&
      !recognizing &&
      !ocrError &&
      quotes.length > 0 &&
      boxes.length === 0 ? (
        <p className="callout">
          {locations.some((location) => location.kind === "ambiguous")
            ? "当前页有多处相同文字，未自动选择位置。请结合上下文人工核对。"
            : "当前页未能可靠定位这条引用。请核对页码，或切换文字原文查看冻结引用。"}
        </p>
      ) : null}
      {quotes.length === 0 ? (
        <p className="meta">当前没有可用于原件标注的冻结引用，可切换文字原文核对。</p>
      ) : null}
      <div
        ref={scroll}
        className="original-page-scroll"
        tabIndex={0}
        role="region"
        aria-label="原件页面与引用标注"
      >
        <div
          className="original-page-surface"
          style={{
            width: `${zoom}%`,
            visibility: source && rendered?.page === page && !error ? "visible" : "hidden",
          }}
        >
          <canvas ref={canvas} aria-label={`${filename}，第 ${page} 页原件`} />
          {boxes.map((box, index) => (
            <span
              key={`${page}:${index}`}
              className="original-quote-region"
              aria-hidden="true"
              style={{
                left: `${box.x * 100}%`,
                top: `${box.y * 100}%`,
                width: `${box.width * 100}%`,
                height: `${box.height * 100}%`,
              }}
            />
          ))}
        </div>
      </div>
      {quotes.length > 0 ? (
        <div className="original-reader-quotes">
          <strong>对照引用</strong>
          {quotes.map((quote, index) => (
            <div key={quote.id}>
              {quote.page !== null ? (
                <Button type="link" onClick={() => setPage(quote.page!)}>
                  定位第 {index + 1} 处引用（第 {quote.page} 页）
                </Button>
              ) : null}
              <blockquote>{quote.quote}</blockquote>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}
