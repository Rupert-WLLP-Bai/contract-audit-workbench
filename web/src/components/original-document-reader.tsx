// File metadata (manifest mirror):
// Purpose: Provides attempt-scoped original-reading identity and switches between original annotations and frozen text without changing evidence or decisions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-07
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { createContext, lazy, Suspense, useEffect, useRef, useState, type ReactNode } from "react";
import { Button } from "antd";
import type { SourceQuote } from "../domain/source-annotation";
import "./original-document-reader.css";

export interface OriginalReadScope {
  caseId: string;
  contextKey: string;
  digests: ReadonlyMap<string, string>;
}

export const OriginalSourceContext = createContext<OriginalReadScope | null>(null);

export function supportsOriginalPreview(filename: string): boolean {
  return /\.(?:pdf|png|jpe?g|docx)$/i.test(filename);
}

const OriginalPageView = lazy(() => import("./original-page-view"));

export function OriginalDocumentReader({
  scope,
  documentId,
  filename,
  digest,
  text,
  quotes,
  keyClause = null,
  focusRange = null,
  onClearKeyClause,
  children,
}: {
  scope: OriginalReadScope;
  documentId: string;
  filename: string;
  digest: string;
  text: string;
  quotes: readonly SourceQuote[];
  keyClause?: {
    id: string;
    heading: string;
    sourceLabel: string;
    quote: string;
    page: number | null;
  } | null;
  focusRange?: { start: number; end: number } | null;
  onClearKeyClause?: () => void;
  children: ReactNode;
}) {
  const noPage = quotes.length > 0 && quotes.every((quote) => quote.page === null);
  const quoteIdentity = quotes.map((quote) => quote.id).join("\u0000");
  const [mode, setMode] = useState<"original" | "text">(() => (noPage ? "text" : "original"));
  const reader = useRef<HTMLElement>(null);
  const effectiveMode = keyClause ? "text" : mode;
  useEffect(() => {
    if (noPage) setMode("text");
  }, [quoteIdentity, noPage]);
  useEffect(() => {
    if (focusRange) setMode("text");
  }, [focusRange?.start, focusRange?.end]);
  useEffect(() => {
    if (effectiveMode !== "text") return;
    const mark = focusRange
      ? reader.current?.querySelector<HTMLElement>(
          `[data-active-source='true'] [data-offset='${focusRange.start}']`,
        )
      : (reader.current?.querySelector<HTMLElement>(
          "[data-active-source='true'] .hit-key-clause[data-selected-key-clause='true']",
        ) ??
        reader.current?.querySelector<HTMLElement>("[data-active-source='true'] .hit-evidence"));
    const pane = mark?.closest<HTMLElement>(".col-doc");
    if (mark && pane && pane.scrollHeight > pane.clientHeight) {
      pane.scrollTop +=
        mark.getBoundingClientRect().top - pane.getBoundingClientRect().top - pane.clientHeight / 2;
    }
  }, [effectiveMode, focusRange?.start, keyClause?.id, quoteIdentity]);
  return (
    <section
      ref={reader}
      className="original-document-reader"
      aria-label={`${filename}原件与文字对照`}
    >
      <div className="original-reader-modes" role="group" aria-label="原文显示方式">
        <Button
          aria-pressed={effectiveMode === "original"}
          disabled={Boolean(keyClause && !onClearKeyClause)}
          onClick={() => {
            if (keyClause) onClearKeyClause?.();
            setMode("original");
          }}
        >
          原件标注
        </Button>
        <Button aria-pressed={effectiveMode === "text"} onClick={() => setMode("text")}>
          文字原文
        </Button>
      </div>
      {keyClause ? (
        <div className="original-reader-key-clause" aria-label="关键条款冻结文字位置">
          <p role="status">
            关键条款“{keyClause.heading}”已在冻结文字中定位（{keyClause.sourceLabel}）；
            {keyClause.page === null
              ? "原件页码未记录，只定位冻结文字。"
              : `ClauseNode 记录的起始页为第 ${keyClause.page} 页。`}
          </p>
          <blockquote>{keyClause.quote}</blockquote>
        </div>
      ) : null}
      {focusRange && effectiveMode === "text" && !keyClause ? (
        <p className="meta" role="status">
          已按引用位置打开冻结文字；请结合上下文核对这条事实建议。
        </p>
      ) : null}
      {effectiveMode === "text" && keyClause === null && noPage ? (
        <p className="meta">引用未记录原件页码，已显示冻结文字中的位置；可切换原件自行核对页码。</p>
      ) : null}
      {effectiveMode === "text" ? (
        children
      ) : (
        <Suspense fallback={<p role="status">正在加载原件阅读器…</p>}>
          <OriginalPageView
            key={`${scope.contextKey}:${scope.caseId}:${documentId}:${digest}`}
            scope={scope}
            documentId={documentId}
            filename={filename}
            digest={digest}
            text={text}
            quotes={quotes}
          />
        </Suspense>
      )}
    </section>
  );
}
