// File metadata (manifest mirror):
// Purpose: Provides attempt-scoped original-reading identity and switches between original annotations and frozen text without changing evidence or decisions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { createContext, lazy, Suspense, useState, type ReactNode } from "react";
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
  return /\.(?:pdf|png|jpe?g)$/i.test(filename);
}

const OriginalPageView = lazy(() => import("./original-page-view"));

export function OriginalDocumentReader({
  scope, documentId, filename, digest, text, quotes, children,
}: {
  scope: OriginalReadScope;
  documentId: string;
  filename: string;
  digest: string;
  text: string;
  quotes: readonly SourceQuote[];
  children: ReactNode;
}) {
  const [mode, setMode] = useState<"original" | "text">("original");
  return (
    <section className="original-document-reader" aria-label={`${filename}原件与文字对照`}>
      <div className="original-reader-modes" role="group" aria-label="原文显示方式">
        <Button aria-pressed={mode === "original"} onClick={() => setMode("original")}>原件标注</Button>
        <Button aria-pressed={mode === "text"} onClick={() => setMode("text")}>文字原文</Button>
      </div>
      {mode === "text" ? children : (
        <Suspense fallback={<p role="status">正在加载原件阅读器…</p>}>
          <OriginalPageView
            key={`${scope.contextKey}:${scope.caseId}:${documentId}:${digest}`}
            scope={scope} documentId={documentId} filename={filename} digest={digest} text={text} quotes={quotes}
          />
        </Suspense>
      )}
    </section>
  );
}
