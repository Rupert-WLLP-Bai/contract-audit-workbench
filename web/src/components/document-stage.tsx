// File metadata (manifest mirror):
// Purpose: Presents the selected review attempt’s materials through text and original-file readers, with responsive two-document comparison, synchronized clause topics and precise citation navigation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r11
// Copyright: See LICENSE
// End file metadata.

import { findingTitle } from "../domain/finding-presentation";
import { Tabs } from "antd";
import { Collapse } from "antd";
import { Button, Input, Select } from "antd";
import {
  memo,
  createContext,
  useContext,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
  type ComponentProps,
} from "react";
import { Link } from "react-router";
import type { CaseReturnState } from "./section-navigation";
import { TechnicalDetail } from "./technical-detail";
import { caseSelectPresentation } from "./case-select-presentation";
import type { ContractFactsCitation, ContractFactsProjection } from "../api/contract-facts";
import type {
  CaseRelationInput,
  EvidenceAnchor,
  Finding,
  MaterialVersionProjection,
  ReviewDocument,
} from "../api/types";
import type { ReviewLayout } from "../domain/review";
import {
  actorRoleLabel,
  caseHref,
  formatDateTime,
  parseLabel,
  parseTone,
  roleLabel,
} from "../domain/review";
import {
  createTextIndex,
  evidenceSourceMatches,
  findTextMatches,
  resolveAnchor,
  segmentText,
  sliceByCodePoints,
  type CodePointTextIndex,
  type ResolvedAnchor,
  type TextSegment,
} from "../domain/text";
import { ParsedDocumentSummary } from "./parsed-document-summary";
import {
  createFrozenQuoteSelection,
  frozenQuoteMatchesDocument,
  type FrozenQuoteScope,
  type FrozenQuoteSelection,
} from "../domain/frozen-quote";
import {
  keyClauseNavigationFor,
  readingGroupFor,
  READING_GROUPS,
  type ReadingGroup,
  type KeyClauseCandidate,
  type KeyClauseNavigation,
} from "../domain/key-clauses";
import { StatusText } from "./status-text";
import {
  OriginalDocumentReader,
  OriginalSourceContext,
  OpenOriginalContext,
  supportsOriginalPreview,
} from "./original-document-reader";
import { businessScopeLabel } from "../domain/business-labels";
import { KeyClauseNavigator } from "./key-clause-navigator";
import "./document-stage-immersive.css";

export interface ReviewCaseContext {
  caseId: string;
  title: string;
  number: string | null;
  customer: string | null;
  supplier: string | null;
  contractType: string;
  businessLine: string;
  scenario: string;
  status: string;
  ownerId: string | null;
  ownerName: string | null;
  dueAt: string | null;
  collaborators: {
    id: string;
    name: string;
    role: string;
    responsibilities: string[];
  }[];
  relatedCases: {
    relationId: string;
    id: string;
    title: string;
    reason: string;
    createdAt: string;
    runId: string | null;
  }[];
  availableCases: { id: string; title: string }[];
}

function documentKey(document: ReviewDocument): string {
  return `${document.id}\u0000${document.parsed_document?.id ?? document.parsed_document_id ?? "legacy"}`;
}

function documentLabel(document: ReviewDocument, documents: ReviewDocument[]): string {
  const sameVersion = documents.filter((item) => item.id === document.id);
  return sameVersion.length > 1
    ? `${roleLabel(document.role)}，来源 ${sameVersion.findIndex((item) => documentKey(item) === documentKey(document)) + 1}`
    : roleLabel(document.role);
}

function documentForAnchor(
  anchor: EvidenceAnchor,
  documents: ReviewDocument[],
): ReviewDocument | null {
  return (
    documents.find((document) =>
      evidenceSourceMatches(
        anchor,
        document.id,
        document.parsed_document,
        document.parsed_document_id,
      ),
    ) ?? null
  );
}

function documentForContractFactsCitation(
  citation: ContractFactsCitation | null,
  documents: ReviewDocument[],
  scope: ContractFactsLocationScope | null,
): ReviewDocument | null {
  if (
    !citation ||
    !scope ||
    scope.historyKind !== "frozen" ||
    !scope.caseId ||
    !scope.runId ||
    scope.projection.attempt_id !== scope.attemptId ||
    !scope.parsedDocumentIds.includes(citation.parsed_document_id)
  )
    return null;
  const source = scope.projection.source;
  if (
    source.document_id !== citation.document_id ||
    source.parsed_document_id !== citation.parsed_document_id ||
    source.original_digest !== citation.original_digest ||
    source.normalized_digest !== citation.normalized_digest ||
    citation.end > scope.projection.input_codepoints ||
    !scope.projection.fields.some((field) =>
      field.citations.some(
        (item) =>
          item.document_id === citation.document_id &&
          item.parsed_document_id === citation.parsed_document_id &&
          item.original_digest === citation.original_digest &&
          item.normalized_digest === citation.normalized_digest &&
          item.quote === citation.quote &&
          item.start === citation.start &&
          item.end === citation.end,
      ),
    )
  )
    return null;
  return (
    documents.find((document) => {
      return (
        document.id === citation.document_id &&
        document.role === "draft_contract" &&
        document.parsed_document_id === citation.parsed_document_id &&
        scope.parsedDocumentIds.includes(document.parsed_document_id) &&
        document.parse_status === "ready" &&
        document.text !== null &&
        sliceByCodePoints(document.text, citation.start, citation.end) === citation.quote
      );
    }) ?? null
  );
}

export interface ContractFactsLocationScope {
  caseId: string;
  runId: string;
  attemptId: string;
  historyKind: string;
  parsedDocumentIds: readonly string[];
  projection: ContractFactsProjection;
}

function anchorsFor(document: ReviewDocument, finding: Finding | null): EvidenceAnchor[] {
  return finding?.evidence.filter((anchor) => anchor.document_version_id === document.id) ?? [];
}

function codePointBoundary(index: CodePointTextIndex, utf16Offset: number): number | null {
  let lower = 0;
  let upper = index.utf16Offsets.length - 1;
  while (lower <= upper) {
    const middle = Math.floor((lower + upper) / 2);
    const offset = index.utf16Offsets[middle];
    if (offset === utf16Offset) return middle;
    if (offset === undefined || offset > utf16Offset) upper = middle - 1;
    else lower = middle + 1;
  }
  return null;
}

function textOffsetWithin(container: HTMLElement, node: Node, offset: number): number | null {
  if (!container.contains(node)) return null;
  const prefix = window.document.createRange();
  prefix.selectNodeContents(container);
  try {
    prefix.setEnd(node, offset);
  } catch {
    return null;
  }
  return prefix.toString().length;
}

function publishQuoteSelection(
  container: HTMLElement,
  source: ReviewDocument,
  scope: FrozenQuoteScope | null,
  providedTextIndex: CodePointTextIndex | null,
  onQuoteSelectionChange: ((selection: FrozenQuoteSelection | null) => void) | undefined,
) {
  if (!onQuoteSelectionChange) return;
  const selection = window.getSelection();
  if (
    !selection ||
    selection.isCollapsed ||
    selection.rangeCount !== 1 ||
    source.text === null ||
    container.textContent !== source.text ||
    !selection.anchorNode ||
    !selection.focusNode ||
    !container.contains(selection.anchorNode) ||
    !container.contains(selection.focusNode)
  ) {
    onQuoteSelectionChange(null);
    return;
  }
  const range = selection.getRangeAt(0);
  if (
    [...container.querySelectorAll("[data-projection-hidden]")].some((node) =>
      range.intersectsNode(node),
    )
  ) {
    onQuoteSelectionChange(null);
    return;
  }
  const index =
    providedTextIndex?.text === source.text ? providedTextIndex : createTextIndex(source.text);
  const startUtf16 = textOffsetWithin(container, range.startContainer, range.startOffset);
  const endUtf16 = textOffsetWithin(container, range.endContainer, range.endOffset);
  const start = startUtf16 === null ? null : codePointBoundary(index, startUtf16);
  const end = endUtf16 === null ? null : codePointBoundary(index, endUtf16);
  if (start === null || end === null || start >= end) {
    onQuoteSelectionChange(null);
    return;
  }
  const frozen = createFrozenQuoteSelection(scope, source, { start, end }, index);
  onQuoteSelectionChange(frozen?.quote === range.toString() ? frozen : null);
}

function DocPane({
  document,
  activeSource,
  children,
}: {
  document: ReviewDocument;
  activeSource: boolean;
  children: ReactNode;
}) {
  return (
    <div
      className="doc-pane"
      data-document-version-id={document.id}
      data-parsed-document-id={document.parsed_document?.id ?? document.parsed_document_id ?? ""}
      data-active-source={activeSource ? "true" : "false"}
    >
      {children}
    </div>
  );
}

function DiagnosticsCollapse({
  document,
  showDiagnostics,
}: {
  document: ReviewDocument;
  showDiagnostics: boolean;
}) {
  if (!showDiagnostics) return null;
  return (
    <Collapse
      className="doc-source-details"
      items={[
        {
          key: "content",
          label: "解析来源与摘要",
          children: (
            <>
              <ParsedDocumentSummary document={document} />
            </>
          ),
        },
      ]}
    />
  );
}

const ClauseContext = createContext<{
  document: ReviewDocument | null;
  navigation: KeyClauseNavigation | null;
  ranges: readonly { start: number; end: number; group: ReadingGroup }[];
  group: ReadingGroup | "all";
  showAll: boolean;
  findRange: { start: number; end: number } | null;
  selected: KeyClauseCandidate | null;
}>({
  document: null,
  navigation: null,
  ranges: [],
  group: "all",
  showAll: false,
  findRange: null,
  selected: null,
});

function keyRangeAt(
  ranges: readonly { start: number; end: number; group: ReadingGroup }[],
  offset: number,
) {
  let lower = 0;
  let upper = ranges.length;
  while (lower < upper) {
    const middle = Math.floor((lower + upper) / 2);
    if (ranges[middle]!.end <= offset) lower = middle + 1;
    else upper = middle;
  }
  return ranges[lower];
}

interface FindingMarker {
  start: number;
  end: number;
  number: number;
  findingId: string;
  relation: string;
}
const FindingMarkerContext = createContext<{
  markers: FindingMarker[];
  onSelect: (id: string) => void;
} | null>(null);

type DisplaySpan = { start: number; end: number; kind: "hidden" | "image" | "heading" };
function displaySpans(text: string): DisplaySpan[] {
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
const DocSegments = memo(function DocSegments({ segments }: { segments: TextSegment[] }) {
  const { findRange, selected: keyClause, ranges: keyRanges } = useContext(ClauseContext);
  const markerContext = useContext(FindingMarkerContext);
  const openOriginal = useContext(OpenOriginalContext);
  const projected = useMemo(() => {
    const spans = displaySpans(segments.map((segment) => segment.text).join(""));
    return segments.flatMap((segment) => {
      const chars = Array.from(segment.text),
        end = segment.start + chars.length;
      const points = [
        ...new Set([
          segment.start,
          end,
          ...spans
            .flatMap((span) => [span.start, span.end])
            .filter((point) => point > segment.start && point < end),
        ]),
      ].sort((a, b) => a - b);
      return points.slice(0, -1).map((start, i) => {
        const stop = points[i + 1]!;
        const display =
          spans.find(
            (span) => start >= span.start && stop <= span.end && span.kind !== "heading",
          ) ?? spans.find((span) => start >= span.start && stop <= span.end);
        return {
          ...segment,
          start,
          text: chars.slice(start - segment.start, stop - segment.start).join(""),
          display,
        };
      });
    });
  }, [segments]);
  return (
    <>
      {projected.map((segment) => {
        const segmentEnd = segment.start + Array.from(segment.text).length;
        const range = keyRangeAt(keyRanges, segment.start);
        const inKeyClause = Boolean(
          range && segment.start >= range.start && segmentEnd <= range.end,
        );
        const selected =
          keyClause !== null &&
          segment.start >= keyClause.range.start &&
          segmentEnd <= keyClause.range.end;
        const selectedAttribute = selected ? { "data-selected-key-clause": "true" } : {};
        const marker = markerContext?.markers.find((item) => item.start === segment.start);
        if (segment.display?.kind === "hidden" || segment.display?.kind === "image")
          return (
            <span key={segment.start} data-offset={segment.start}>
              <span
                data-finding-number={marker?.number}
                data-finding-relation={marker?.relation}
                className={marker ? "hit-evidence" : undefined}
                role={marker ? "button" : undefined}
                tabIndex={marker ? 0 : undefined}
                aria-label={marker ? `查看线索 ${marker.number}` : undefined}
                onClick={marker ? () => markerContext?.onSelect(marker.findingId) : undefined}
                onKeyDown={
                  marker
                    ? (event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          markerContext?.onSelect(marker.findingId);
                        }
                      }
                    : undefined
                }
              >
                <span data-projection-hidden="true" style={{ display: "none" }}>
                  {segment.text}
                </span>
              </span>
              {segment.display.kind === "image" &&
                segment.start === segment.display.start &&
                (openOriginal ? (
                  <button
                    type="button"
                    className="doc-image-reference"
                    aria-label="查看原件中的图片"
                    onClick={openOriginal}
                  />
                ) : (
                  <span className="doc-image-reference" aria-label="图片" />
                ))}
            </span>
          );
        if (segment.highlighted === "evidence") {
          return (
            <mark
              key={segment.start}
              data-offset={segment.start}
              data-finding-number={marker?.number}
              data-finding-relation={marker?.relation}
              role={marker ? "button" : undefined}
              tabIndex={marker ? 0 : undefined}
              aria-label={marker ? `查看线索 ${marker.number}` : undefined}
              onClick={marker ? () => markerContext?.onSelect(marker.findingId) : undefined}
              onKeyDown={
                marker
                  ? (event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        markerContext?.onSelect(marker.findingId);
                      }
                    }
                  : undefined
              }
              {...selectedAttribute}
              className={`hit-evidence${segment.display?.kind === "heading" ? " doc-projection-heading" : ""}${inKeyClause ? " key-clause-overlap" : ""}${selected ? " key-clause-selected" : ""}${findRange && segment.start >= findRange.start && segmentEnd <= findRange.end ? " evidence-find" : ""}`}
            >
              {segment.text}
            </mark>
          );
        }
        if (inKeyClause) {
          return (
            <mark
              key={segment.start}
              data-offset={segment.start}
              {...selectedAttribute}
              className={`hit-key-clause${segment.display?.kind === "heading" ? " doc-projection-heading" : ""} reading-${range!.group}${segment.highlighted === "find" ? " hit-find" : ""}${selected ? " key-clause-selected" : ""}`}
            >
              {segment.text}
            </mark>
          );
        }
        return (
          <span
            key={segment.start}
            data-offset={
              segment.anchor || segment.highlighted === "find" ? segment.start : undefined
            }
            className={`${segment.highlighted === "find" ? "hit-find" : ""}${segment.display?.kind === "heading" ? " doc-projection-heading" : ""}`}
          >
            {segment.text}
          </span>
        );
      })}
    </>
  );
});

type LocatedEvidenceAnchor = {
  anchor: EvidenceAnchor;
  resolved: Extract<ResolvedAnchor, { kind: "highlight" }>;
};

function EvidenceNotes({ resolved }: { resolved: ResolvedAnchor[] }) {
  return (
    <>
      {resolved.map((item, index) =>
        item.kind === "snapshot" ? (
          <SnapshotCallout key={`snapshot-${index}`} resolved={item} />
        ) : item.kind === "unavailable" ? (
          <p key={`unavailable-${index}`} className="callout">
            {item.reason}
          </p>
        ) : null,
      )}
    </>
  );
}

function EvidenceAnchorSwitch({
  anchors,
  selectedId,
  onSelect,
}: {
  anchors: LocatedEvidenceAnchor[];
  selectedId: string;
  onSelect: (anchor: LocatedEvidenceAnchor) => void;
}) {
  if (anchors.length < 2) return null;
  return (
    <div className="evidence-anchor-switch" role="group" aria-label="多个引用位置">
      <span>引用位置</span>
      {anchors.map((item, index) => {
        const excerpt = Array.from(item.anchor.quote).slice(0, 36).join("");
        return (
          <Button
            key={`${item.anchor.id}:${item.resolved.start}`}
            htmlType="button"
            aria-label={`定位第 ${index + 1} 处引用：${item.anchor.quote}`}
            aria-pressed={selectedId === item.anchor.id}
            onClick={() => onSelect(item)}
          >
            {`第 ${index + 1} 处：${excerpt}${Array.from(item.anchor.quote).length > 36 ? "…" : ""}`}
          </Button>
        );
      })}
    </div>
  );
}

function SelectableDocText({
  document,
  scope,
  textIndex,
  segments,
  onQuoteSelectionChange,
}: {
  document: ReviewDocument;
  scope: FrozenQuoteScope | null;
  textIndex: CodePointTextIndex | null;
  segments: TextSegment[];
  onQuoteSelectionChange?: (selection: FrozenQuoteSelection | null) => void;
}) {
  if (document.text === null) return <p>这份材料还没有可阅读的正文。</p>;
  return (
    <>
      <div
        className="doc-text"
        tabIndex={0}
        aria-label={`${document.filename}原文`}
        onMouseUp={(event) =>
          publishQuoteSelection(
            event.currentTarget,
            document,
            scope,
            textIndex,
            onQuoteSelectionChange,
          )
        }
        onKeyUp={(event) =>
          publishQuoteSelection(
            event.currentTarget,
            document,
            scope,
            textIndex,
            onQuoteSelectionChange,
          )
        }
      >
        <DocSegments segments={segments} />
      </div>
    </>
  );
}

function FailedParseBody({
  document,
  resolved,
  showDiagnostics,
  activeSource,
}: {
  document: ReviewDocument;
  resolved: ResolvedAnchor[];
  showDiagnostics: boolean;
  activeSource: boolean;
}) {
  return (
    <DocPane document={document} activeSource={activeSource}>
      <header className="doc-pane-head">
        <strong title={document.filename}>{document.filename}</strong>
        <StatusText tone="bad">解析失败</StatusText>
        <TechnicalDetail
          label="材料状态编码"
          value={JSON.stringify({ role: document.role, parse_status: document.parse_status })}
        />
      </header>
      <DiagnosticsCollapse document={document} showDiagnostics={showDiagnostics} />
      <p className="callout">这份材料解析失败，请先处理解析问题再继续审查。</p>
      {document.parse_error ? <p>{document.parse_error}</p> : null}
      <EvidenceNotes resolved={resolved} />
    </DocPane>
  );
}

function HighlightBody({
  document,
  resolved,
  notes,
  anchors,
  selectedAnchorId,
  onSelectAnchor,
  segments,
  showDiagnostics,
  activeSource,
  scope,
  textIndex,
  onQuoteSelectionChange,
}: {
  document: ReviewDocument;
  resolved: Extract<ResolvedAnchor, { kind: "highlight" }>[];
  notes: ResolvedAnchor[];
  anchors: LocatedEvidenceAnchor[];
  selectedAnchorId: string;
  onSelectAnchor: (anchor: LocatedEvidenceAnchor) => void;
  segments: TextSegment[];
  showDiagnostics: boolean;
  activeSource: boolean;
  scope: FrozenQuoteScope | null;
  textIndex: CodePointTextIndex | null;
  onQuoteSelectionChange?: (selection: FrozenQuoteSelection | null) => void;
}) {
  const pages = [...new Set(resolved.flatMap((item) => (item.page === null ? [] : [item.page])))];
  return (
    <DocPane document={document} activeSource={activeSource}>
      <header className="doc-pane-head">
        <strong title={document.filename}>{document.filename}</strong>
        <span>{roleLabel(document.role)}</span>
        {pages.map((page) => (
          <span key={page}>第 {page} 页</span>
        ))}
        <TechnicalDetail
          label="材料状态编码"
          value={JSON.stringify({ role: document.role, parse_status: document.parse_status })}
        />
      </header>
      <DiagnosticsCollapse document={document} showDiagnostics={showDiagnostics} />
      {activeSource ? (
        <EvidenceAnchorSwitch
          anchors={anchors}
          selectedId={selectedAnchorId}
          onSelect={onSelectAnchor}
        />
      ) : null}
      <EvidenceNotes resolved={notes} />
      <SelectableDocText
        document={document}
        scope={scope}
        textIndex={textIndex}
        segments={segments}
        onQuoteSelectionChange={onQuoteSelectionChange}
      />
    </DocPane>
  );
}

function SnapshotCallout({
  resolved,
}: {
  resolved: Extract<ResolvedAnchor, { kind: "snapshot" }>;
}) {
  return (
    <div className="callout">
      <p>{resolved.reason}</p>
      {resolved.page !== null ? <p>记录中的页码：第 {resolved.page} 页</p> : null}
      <blockquote>{resolved.quote}</blockquote>
    </div>
  );
}

function EmptyOrText({
  hasResolved,
  document,
  scope,
  textIndex,
  segments,
  onQuoteSelectionChange,
}: {
  hasResolved: boolean;
  document: ReviewDocument;
  scope: FrozenQuoteScope | null;
  textIndex: CodePointTextIndex | null;
  segments: TextSegment[];
  onQuoteSelectionChange?: (selection: FrozenQuoteSelection | null) => void;
}) {
  if (hasResolved && document.text === null) return null;
  return (
    <SelectableDocText
      document={document}
      scope={scope}
      textIndex={textIndex}
      segments={segments}
      onQuoteSelectionChange={onQuoteSelectionChange}
    />
  );
}

function PlainBody({
  document,
  resolved,
  showDiagnostics,
  segments,
  activeSource,
  scope,
  textIndex,
  onQuoteSelectionChange,
}: {
  document: ReviewDocument;
  resolved: ResolvedAnchor[];
  showDiagnostics: boolean;
  segments: TextSegment[];
  activeSource: boolean;
  scope: FrozenQuoteScope | null;
  textIndex: CodePointTextIndex | null;
  onQuoteSelectionChange?: (selection: FrozenQuoteSelection | null) => void;
}) {
  return (
    <DocPane document={document} activeSource={activeSource}>
      <header className="doc-pane-head">
        <strong title={document.filename}>{document.filename}</strong>
        <span>{roleLabel(document.role)}</span>
        <StatusText tone={parseTone(document.parse_status)}>
          {parseLabel(document.parse_status)}
        </StatusText>
        <TechnicalDetail
          label="材料状态编码"
          value={JSON.stringify({ role: document.role, parse_status: document.parse_status })}
        />
      </header>
      <DiagnosticsCollapse document={document} showDiagnostics={showDiagnostics} />
      <EvidenceNotes resolved={resolved} />
      <EmptyOrText
        hasResolved={resolved.length > 0}
        document={document}
        scope={scope}
        textIndex={textIndex}
        segments={segments}
        onQuoteSelectionChange={onQuoteSelectionChange}
      />
    </DocPane>
  );
}

const TextDocumentBody = memo(function TextDocumentBody({
  document,
  finding,
  findRange,
  activeSource,
  showDiagnostics,
  textIndex: providedTextIndex,
  quoteScope,
  onQuoteSelectionChange,
}: {
  document: ReviewDocument;
  finding: Finding | null;
  findRange: { start: number; end: number } | null;
  activeSource: boolean;
  showDiagnostics: boolean;
  textIndex: CodePointTextIndex | null;
  quoteScope: FrozenQuoteScope | null;
  focusRange?: { start: number; end: number } | null;
  onClearKeyClause?: () => void;
  onQuoteSelectionChange?: (selection: FrozenQuoteSelection | null) => void;
  immersive?: boolean;
}) {
  const textIndex = useMemo(
    () => providedTextIndex ?? (document.text === null ? null : createTextIndex(document.text)),
    [
      document,
      document.parsed_document,
      document.parsed_document_id,
      document.text,
      providedTextIndex,
    ],
  );
  const { ranges: keyRanges, selected: keyClause } = useContext(ClauseContext);
  const markerContext = useContext(FindingMarkerContext);
  const anchors = useMemo(() => anchorsFor(document, finding), [document, finding]);
  const resolved = useMemo(
    () =>
      anchors.map((anchor) =>
        resolveAnchor(
          document.text,
          anchor,
          document.parsed_document,
          document.parsed_document_id,
          textIndex ?? undefined,
        ),
      ),
    [anchors, document, textIndex],
  );
  const locatedAnchors = useMemo(
    () =>
      resolved.flatMap((item, index): LocatedEvidenceAnchor[] =>
        item.kind === "highlight" && anchors[index]
          ? [{ anchor: anchors[index]!, resolved: item }]
          : [],
      ),
    [anchors, resolved],
  );
  const [selectedAnchorId, setSelectedAnchorId] = useState("");
  const currentAnchorId =
    locatedAnchors.find((item) => item.anchor.id === selectedAnchorId)?.anchor.id ??
    locatedAnchors[0]?.anchor.id ??
    "";
  const clauseStarts = useMemo(
    () => document.clauses.map((clause) => clause.text_offset.start),
    [document, document.clauses],
  );
  const segments = useMemo(() => {
    if (document.text === null || document.parse_status === "failed") return null;
    const highlights =
      markerContext?.markers ??
      locatedAnchors.map(({ resolved: item }) => ({
        start: item.start,
        end: item.end,
      }));
    const clauseOffsets = clauseStarts.concat(
      keyRanges.flatMap((range) => [range.start, range.end]),
    );
    if (keyClause) clauseOffsets.push(keyClause.range.start, keyClause.range.end);
    return segmentText(
      document.text,
      highlights,
      highlights.length > 0
        ? clauseOffsets.concat(highlights.map((item) => item.start))
        : clauseOffsets,
      findRange,
      textIndex ?? undefined,
    );
  }, [
    clauseStarts,
    document,
    findRange,
    keyClause,
    keyRanges,
    locatedAnchors,
    markerContext,
    textIndex,
  ]);
  function selectAnchor(anchor: LocatedEvidenceAnchor) {
    setSelectedAnchorId(anchor.anchor.id);
    focusAnchorOffset(document, anchor.resolved.start);
  }

  if (document.parse_status === "failed") {
    return (
      <FailedParseBody
        document={document}
        resolved={resolved}
        showDiagnostics={showDiagnostics}
        activeSource={activeSource}
      />
    );
  }

  if (
    (locatedAnchors.length > 0 || (markerContext?.markers.length ?? 0) > 0) &&
    document.text !== null
  ) {
    return (
      <HighlightBody
        document={document}
        resolved={locatedAnchors.map((item) => item.resolved)}
        notes={resolved.filter((item) => item.kind !== "highlight")}
        anchors={locatedAnchors}
        selectedAnchorId={currentAnchorId}
        onSelectAnchor={selectAnchor}
        segments={segments ?? []}
        showDiagnostics={showDiagnostics}
        activeSource={activeSource}
        scope={quoteScope}
        textIndex={textIndex}
        onQuoteSelectionChange={onQuoteSelectionChange}
      />
    );
  }

  return (
    <PlainBody
      document={document}
      resolved={resolved}
      showDiagnostics={showDiagnostics}
      segments={segments ?? []}
      activeSource={activeSource}
      scope={quoteScope}
      textIndex={textIndex}
      onQuoteSelectionChange={onQuoteSelectionChange}
    />
  );
});

const DocumentBody = memo(function DocumentBody(props: ComponentProps<typeof TextDocumentBody>) {
  const scope = useContext(OriginalSourceContext);
  const document = props.document;
  const quotes = useMemo(
    () =>
      anchorsFor(document, props.finding).flatMap((anchor) => {
        const resolved = resolveAnchor(
          document.text,
          anchor,
          document.parsed_document,
          document.parsed_document_id,
          props.textIndex ?? undefined,
        );
        return resolved.kind === "highlight"
          ? [
              {
                id: anchor.id,
                quote: anchor.quote,
                start: resolved.start,
                end: resolved.end,
                page: resolved.page,
              },
            ]
          : [];
      }),
    [document, props.finding, props.textIndex],
  );
  const parentClauses = useContext(ClauseContext);
  const keyClause = parentClauses.document === document ? parentClauses.selected : null;
  const navigation = useMemo(() => {
    if (parentClauses.document === document && parentClauses.navigation)
      return parentClauses.navigation;
    const all = keyClauseNavigationFor(props.quoteScope, document);
    const available = parentClauses.showAll
      ? (all.allCandidates ?? all.candidates)
      : all.candidates;
    const candidates =
      parentClauses.group === "all"
        ? available
        : available.filter((candidate) => readingGroupFor(candidate) === parentClauses.group);
    return {
      ...all,
      candidates,
      message: candidates.length ? "" : all.message || "当前分类没有匹配位置。",
    };
  }, [
    parentClauses.document,
    parentClauses.navigation,
    parentClauses.group,
    parentClauses.showAll,
    props.quoteScope,
    document,
  ]);
  const keyRanges = useMemo(() => {
    const ranges: { start: number; end: number; group: ReadingGroup }[] = [];
    for (const candidate of navigation.candidates) {
      const previous = ranges.at(-1);
      if (previous && candidate.range.start < previous.end)
        previous.end = Math.max(previous.end, candidate.range.end);
      else ranges.push({ ...candidate.range, group: readingGroupFor(candidate) });
    }
    return ranges;
  }, [navigation]);
  const textBody = (
    <ClauseContext.Provider
      value={{
        document,
        navigation,
        ranges: keyRanges,
        group: parentClauses.group,
        showAll: parentClauses.showAll,
        findRange: props.findRange,
        selected: keyClause,
      }}
    >
      <TextDocumentBody {...props} />
    </ClauseContext.Provider>
  );
  const digest = scope?.digests.get(document.id);
  if (
    !scope ||
    !props.quoteScope ||
    scope.caseId !== props.quoteScope.caseId ||
    !digest ||
    digest !== document.digest ||
    !props.quoteScope.isFrozenAttempt ||
    !document.parsed_document_id ||
    !props.quoteScope.parsedDocumentIds.includes(document.parsed_document_id) ||
    !supportsOriginalPreview(document.filename)
  )
    return textBody;
  return (
    <OriginalDocumentReader
      key={`${document.id}:${document.parsed_document_id}`}
      scope={scope}
      documentId={document.id}
      filename={document.filename}
      digest={digest}
      text={document.text ?? ""}
      quotes={quotes}
      hasKeyClauses={keyRanges.length > 0}
      keyClause={
        keyClause
          ? {
              id: keyClause.id,
              heading: keyClause.heading,
              sourceLabel: keyClause.sourceLabel,
              quote: keyClause.quote,
              page: keyClause.page,
            }
          : null
      }
      focusRange={props.focusRange ?? props.findRange ?? null}
      onClearKeyClause={props.onClearKeyClause}
      immersive={props.immersive}
    >
      {textBody}
    </OriginalDocumentReader>
  );
});

function ownerLabel(context: ReviewCaseContext): string {
  return context.ownerName || (context.ownerId ? "姓名未返回" : "未分配");
}

function businessScope(context: ReviewCaseContext): string {
  return businessScopeLabel(context.businessLine, context.scenario);
}

function deadlineLabel(context: ReviewCaseContext): string {
  return `${context.dueAt ? formatDateTime(context.dueAt) : "未设期限"} ，${context.status || "未报告"}`;
}

function CaseContextHeader({
  context,
  caseReturnState,
}: {
  context: ReviewCaseContext;
  caseReturnState?: CaseReturnState;
}) {
  return (
    <>
      <header className="review-case-context-head">
        <div>
          <p>案件上下文</p>
          <h2>{context.title}</h2>
          {context.number ? <span>{context.number}</span> : null}
        </div>
        <Link
          className="review-case-open"
          to={`/cases/${encodeURIComponent(context.caseId)}`}
          state={caseReturnState}
        >
          案件详情
        </Link>
      </header>
      <dl className="review-case-context-meta">
        <div>
          <dt>客户</dt>
          <dd>{context.customer || "未设置"}</dd>
        </div>
        <div>
          <dt>供应商</dt>
          <dd>{context.supplier || "未设置"}</dd>
        </div>
        <div>
          <dt>合同类型</dt>
          <dd>{context.contractType || "未报告"}</dd>
        </div>
        <div>
          <dt>业务线 / 场景</dt>
          <dd>{businessScope(context)}</dd>
        </div>
        <div>
          <dt>负责人</dt>
          <dd>{ownerLabel(context)}</dd>
        </div>
        <div>
          <dt>期限 / 状态</dt>
          <dd>{deadlineLabel(context)}</dd>
        </div>
      </dl>
    </>
  );
}

function ContextTabs({
  context,
  leftTab,
  onTab,
}: {
  context: ReviewCaseContext;
  leftTab: string;
  onTab: (tab: "材料" | "审查成员" | "相关案件") => void;
}) {
  return (
    <Tabs
      activeKey={leftTab}
      onChange={(key) => {
        const next = (["材料", "审查成员", "相关案件"] as const).find((label) => label === key);
        if (next) onTab(next);
      }}
      items={[
        { key: "材料", label: "材料" },
        {
          key: "审查成员",
          label: `审查成员 ${context.collaborators.length}`,
        },
        {
          key: "相关案件",
          label: `相关案件 ${context.relatedCases.length}`,
        },
      ]}
    />
  );
}

function MaterialTabs({
  documents,
  activeKey,
  onChange,
}: {
  documents: ReviewDocument[];
  activeKey: string;
  onChange: (key: string) => void;
}) {
  return (
    <Tabs
      activeKey={activeKey}
      onChange={onChange}
      items={documents.map((item) => ({
        key: documentKey(item),
        label: (
          <span
            aria-label={documentLabel(item, documents)}
            title={item.filename}
            data-document-version-id={item.id}
            data-parsed-document-id={item.parsed_document?.id ?? item.parsed_document_id ?? ""}
          >
            {documentLabel(item, documents)}
          </span>
        ),
      }))}
    />
  );
}

function MaterialSearch({
  query,
  resultQuery,
  onQuery,
  matches,
  currentMatchIndex,
  onPrevious,
  onNext,
}: {
  query: string;
  resultQuery: string;
  onQuery: (value: string) => void;
  matches: { start: number; end: number }[];
  currentMatchIndex: number;
  onPrevious: () => void;
  onNext: () => void;
}) {
  return (
    <>
      <label className="field">
        <span>文内搜索</span>
        <Input
          aria-label="文内搜索"
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="条款或正文"
        />
      </label>
      <p className="meta">
        {resultQuery.trim() === "" ? "输入后在当前材料中查找" : `正文匹配 ${matches.length} 处`}
      </p>
      {matches.length > 0 ? (
        <div className="filter-row" role="group" aria-label="搜索匹配项导航">
          <Button htmlType="button" type="default" aria-label="搜索上一处" onClick={onPrevious}>
            上一处
          </Button>
          <span className="meta" role="status" aria-live="polite">
            第 {currentMatchIndex + 1} / {matches.length} 处
          </span>
          <Button htmlType="button" type="default" aria-label="搜索下一处" onClick={onNext}>
            下一处
          </Button>
        </div>
      ) : null}
    </>
  );
}

const ClauseList = memo(function ClauseList({
  clauses,
  onFocus,
}: {
  clauses: ReviewDocument["clauses"];
  onFocus: (offset: number) => void;
}) {
  return (
    <>
      <h3>条款</h3>
      {clauses.length === 0 ? (
        <p className="meta">这份材料没有可在原文中找到的已保存条款。</p>
      ) : null}
      <ul className="clause-list">
        {clauses.map((clause) => (
          <li key={clause.id}>
            <Button
              aria-label={`${clause.page !== null ? `起始页第 ${clause.page} 页，` : ""}${clause.heading}`}
              htmlType="button"
              onClick={() => onFocus(clause.text_offset.start)}
            >
              {clause.page !== null ? `起始页第 ${clause.page} 页，` : ""}
              {clause.heading}
            </Button>
          </li>
        ))}
      </ul>
    </>
  );
});

const RelatedFindings = memo(function RelatedFindings({
  findings,
  selectedFindingId,
  onSelectFinding,
}: {
  findings: Finding[];
  selectedFindingId: string | null;
  onSelectFinding: (findingId: string) => void;
}) {
  return (
    <>
      <h3>引用该材料的线索</h3>
      {findings.length === 0 ? (
        <p className="meta">当前材料没有关联到本次运行的线索。</p>
      ) : (
        <ul className="clause-list">
          {findings.map((item) => (
            <li key={item.id}>
              <Button
                aria-label={"查看线索：" + findingTitle(item)}
                htmlType="button"
                aria-pressed={selectedFindingId === item.id}
                onClick={() => onSelectFinding(item.id)}
              >
                查看线索：{findingTitle(item)}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </>
  );
});

function MaterialPane({
  documents,
  active,
  query,
  resultQuery,
  matches,
  currentMatchIndex,
  clauses,
  keyClauseNavigation,
  selectedKeyClauseId,
  relatedFindings,
  selectedFindingId,
  onActiveKey,
  onQuery,
  onPreviousMatch,
  onNextMatch,
  onFocusOffset,
  onSelectKeyClause,
  onSelectFinding,
}: {
  documents: ReviewDocument[];
  active: ReviewDocument | null;
  query: string;
  resultQuery: string;
  matches: { start: number; end: number }[];
  currentMatchIndex: number;
  clauses: ReviewDocument["clauses"];
  keyClauseNavigation: KeyClauseNavigation;
  selectedKeyClauseId: string | null;
  relatedFindings: Finding[];
  selectedFindingId: string | null;
  onActiveKey: (key: string) => void;
  onQuery: (value: string) => void;
  onPreviousMatch: () => void;
  onNextMatch: () => void;
  onFocusOffset: (offset: number) => void;
  onSelectKeyClause: (candidate: KeyClauseCandidate) => void;
  onSelectFinding: (findingId: string) => void;
}) {
  return (
    <section className="review-material-context" aria-label="本次运行材料">
      <h2 className="sr-only">材料</h2>
      {documents.length === 0 ? (
        <p className="meta">本次运行没有带上材料。</p>
      ) : (
        <MaterialTabs
          documents={documents}
          activeKey={active ? documentKey(active) : ""}
          onChange={onActiveKey}
        />
      )}
      {documents.length > 0 ? (
        <>
          <MaterialSearch
            query={query}
            resultQuery={resultQuery}
            onQuery={onQuery}
            matches={matches}
            currentMatchIndex={currentMatchIndex}
            onPrevious={onPreviousMatch}
            onNext={onNextMatch}
          />
          <ClauseList clauses={clauses} onFocus={onFocusOffset} />
          <KeyClauseNavigator
            navigation={keyClauseNavigation}
            selectedId={selectedKeyClauseId}
            onSelect={onSelectKeyClause}
          />
          <RelatedFindings
            findings={relatedFindings}
            selectedFindingId={selectedFindingId}
            onSelectFinding={onSelectFinding}
          />
        </>
      ) : null}
    </section>
  );
}

function CollaboratorsPane({ context }: { context: ReviewCaseContext }) {
  return (
    <section className="review-collaborators" aria-label="案件协作人员">
      <h2>审查成员</h2>
      <p className="meta">当前案件已授权的成员及实际分工。</p>
      {context.collaborators.length === 0 ? (
        <p className="meta">当前没有可显示的案件成员。</p>
      ) : (
        <ul>
          {context.collaborators.map((member) => (
            <li key={member.id}>
              <strong>{member.name}</strong>
              <span>{actorRoleLabel(member.role)}</span>
              <small>{member.responsibilities.join("，")}</small>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function RelationForm({
  availableCases,
  relatedCaseId,
  relationReason,
  pending,
  error,
  onRelatedCase,
  onReason,
  onSubmit,
}: {
  availableCases: { id: string; title: string }[];
  relatedCaseId: string;
  relationReason: string;
  pending: boolean;
  error: string | null;
  onRelatedCase: (value: string) => void;
  onReason: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  if (availableCases.length === 0) {
    return <p className="meta">当前身份可见范围内没有可新增的案件。</p>;
  }
  return (
    <form className="review-relation-form" onSubmit={onSubmit}>
      <label>
        关联可见案件
        <Select
          aria-label="关联可见案件"
          {...caseSelectPresentation}
          showSearch={{ optionFilterProp: "label" }}
          value={relatedCaseId || undefined}
          onChange={onRelatedCase}
          options={[
            { label: "选择案件", value: "" },
            ...availableCases.map((item) => ({
              label: item.title,
              value: item.id,
            })),
          ]}
        />
      </label>
      <label>
        关联原因
        <Input
          required
          maxLength={500}
          value={relationReason}
          onChange={(event) => onReason(event.target.value)}
          placeholder="填写业务关联依据"
        />
      </label>
      {error ? <p role="alert">{error}</p> : null}
      <Button
        aria-label={String(pending ? "正在保存" : "建立关联")}
        type="primary"
        htmlType="submit"
        disabled={pending || relatedCaseId === "" || relationReason.trim() === ""}
      >
        {pending ? "正在保存" : "建立关联"}
      </Button>
    </form>
  );
}

function RelatedCasesPane({
  context,
  canEditRelations,
  relationPending,
  relationError,
  relatedCaseId,
  relationReason,
  onCreateRelation,
  onDeleteRelation,
  onRelatedCase,
  onReason,
  onSubmitRelation,
}: {
  context: ReviewCaseContext;
  canEditRelations: boolean;
  relationPending: boolean;
  relationError: string | null;
  relatedCaseId: string;
  relationReason: string;
  onCreateRelation?: (input: CaseRelationInput) => Promise<void>;
  onDeleteRelation?: (relationId: string) => void;
  onRelatedCase: (value: string) => void;
  onReason: (value: string) => void;
  onSubmitRelation: (event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <section className="review-related-cases" aria-label="关联案件">
      <h2>
        相关案件 <span>{context.relatedCases.length}</span>
      </h2>
      {context.relatedCases.length === 0 ? (
        <p className="meta">当前案件没有已保存的关系。</p>
      ) : (
        <ul>
          {context.relatedCases.map((relation) => (
            <li key={relation.relationId}>
              <div>
                <Link to={caseHref(relation.id, relation.runId)}>{relation.title}</Link>
                <small>{formatDateTime(relation.createdAt)}</small>
                <p>{relation.reason}</p>
              </div>
              {canEditRelations && onDeleteRelation ? (
                <Button
                  aria-label={"解除关联"}
                  htmlType="button"
                  type="text"
                  disabled={relationPending}
                  onClick={() => onDeleteRelation(relation.relationId)}
                >
                  解除关联
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {canEditRelations && onCreateRelation ? (
        <RelationForm
          availableCases={context.availableCases}
          relatedCaseId={relatedCaseId}
          relationReason={relationReason}
          pending={relationPending}
          error={relationError}
          onRelatedCase={onRelatedCase}
          onReason={onReason}
          onSubmit={onSubmitRelation}
        />
      ) : (
        <p className="meta">当前案件关系为只读。</p>
      )}
    </section>
  );
}

function DocumentToolbar({
  layout,
  evidenceCount,
  activeIsEvidence,
  compareSources,
  onCompare,
}: {
  layout: ReviewLayout;
  evidenceCount: number;
  activeIsEvidence: boolean;
  compareSources: boolean;
  onCompare: (compare: boolean) => void;
}) {
  const offered = layout === "split" && evidenceCount >= 2 && activeIsEvidence;
  return (
    <header className="document-toolbar">
      <h2>合同与证据原文</h2>
      {offered ? (
        <div className="filter-row" role="group" aria-label="正文显示模式">
          <Button
            aria-label={"单篇阅读"}
            htmlType="button"
            aria-pressed={!compareSources}
            onClick={() => onCompare(false)}
          >
            单篇阅读
          </Button>
          <Button
            aria-label={"双来源对照"}
            htmlType="button"
            aria-pressed={compareSources}
            onClick={() => onCompare(true)}
          >
            双来源对照
          </Button>
        </div>
      ) : null}
    </header>
  );
}

function SourceSwitch({
  evidenceDocs,
  documents,
  active,
  onSelect,
}: {
  evidenceDocs: ReviewDocument[];
  documents: ReviewDocument[];
  active: ReviewDocument | null;
  onSelect: (key: string) => void;
}) {
  return (
    <div className="source-switch" role="group" aria-label="证据来源">
      {evidenceDocs.map((item) => (
        <Button
          aria-label={String(documentLabel(item, documents))}
          key={documentKey(item)}
          htmlType="button"
          aria-pressed={active !== null && documentKey(active) === documentKey(item)}
          data-document-version-id={item.id}
          data-parsed-document-id={item.parsed_document?.id ?? item.parsed_document_id ?? ""}
          onClick={() => onSelect(documentKey(item))}
        >
          {documentLabel(item, documents)}
        </Button>
      ))}
    </div>
  );
}

function DocumentView({
  documents,
  finding,
  active,
  textIndex,
  currentMatch,
  showDiagnostics,
  split,
  evidenceDocs,
  quoteScope,
  focusRange,
  onClearKeyClause,
  onQuoteSelectionChange,
}: {
  documents: ReviewDocument[];
  finding: Finding | null;
  active: ReviewDocument | null;
  textIndex: CodePointTextIndex | null;
  currentMatch: { start: number; end: number } | null;
  showDiagnostics: boolean;
  split: boolean;
  evidenceDocs: ReviewDocument[];
  quoteScope: FrozenQuoteScope | null;
  focusRange: { start: number; end: number } | null;
  onClearKeyClause?: () => void;
  onQuoteSelectionChange?: (selection: FrozenQuoteSelection | null) => void;
}) {
  if (documents.length === 0) return <p>这次运行没有带上材料。</p>;
  if (split) {
    return (
      <div className="document-split">
        {evidenceDocs.map((item) => (
          <DocumentBody
            key={documentKey(item)}
            document={item}
            finding={finding}
            findRange={item === active ? currentMatch : null}
            textIndex={item === active ? textIndex : null}
            showDiagnostics={showDiagnostics}
            activeSource={item === active}
            quoteScope={quoteScope}
            focusRange={item === active ? focusRange : null}
            onClearKeyClause={onClearKeyClause}
            onQuoteSelectionChange={onQuoteSelectionChange}
          />
        ))}
      </div>
    );
  }
  return active ? (
    <DocumentBody
      document={active}
      finding={finding}
      findRange={currentMatch}
      textIndex={textIndex}
      showDiagnostics={showDiagnostics}
      activeSource
      quoteScope={quoteScope}
      focusRange={focusRange}
      onClearKeyClause={onClearKeyClause}
      onQuoteSelectionChange={onQuoteSelectionChange}
    />
  ) : null;
}

function initialDocumentOf(
  finding: Finding | null,
  documents: ReviewDocument[],
): ReviewDocument | null {
  return (
    finding?.evidence
      .map((anchor) => documentForAnchor(anchor, documents))
      .find((item) => item !== null) ??
    documents[0] ??
    null
  );
}

function activeDocumentOf(documents: ReviewDocument[], activeKey: string): ReviewDocument | null {
  return documents.find((document) => documentKey(document) === activeKey) ?? documents[0] ?? null;
}

function evidenceDocumentsOf(
  finding: Finding | null,
  documents: ReviewDocument[],
): ReviewDocument[] {
  if (!finding) return [];
  const unique = new Map<string, ReviewDocument>();
  for (const anchor of finding.evidence) {
    const document = documentForAnchor(anchor, documents);
    if (document) unique.set(documentKey(document), document);
  }
  return [...unique.values()].slice(0, 2);
}

function evidenceSourceOf(
  finding: Finding | null,
  documents: ReviewDocument[],
): ReviewDocument | null {
  if (!finding) return null;
  return (
    finding.evidence
      .map((anchor) => documentForAnchor(anchor, documents))
      .find((item) => item !== null) ?? null
  );
}

function relatedFindingsOf(active: ReviewDocument | null, findings: Finding[]): Finding[] {
  if (active === null) return [];
  return findings.filter((item) =>
    item.evidence.some((anchor) => anchor.document_version_id === active.id),
  );
}

function visibleClausesOf(active: ReviewDocument | null, query: string): ReviewDocument["clauses"] {
  return (
    active?.clauses.filter(
      (clause) => query.trim() === "" || clause.heading.includes(query.trim()),
    ) ?? []
  );
}

function useEvidenceAutoSwitch(
  finding: Finding | null,
  active: ReviewDocument | null,
  documents: ReviewDocument[],
  setActiveKey: (key: string) => void,
  factCitationActive: boolean,
) {
  useEffect(() => {
    if (
      factCitationActive ||
      !finding ||
      !active ||
      finding.evidence.some((anchor) => anchor.document_version_id === active.id)
    )
      return;
    const source = evidenceSourceOf(finding, documents);
    if (source) setActiveKey(documentKey(source));
  }, [active, documents, factCitationActive, finding, setActiveKey]);
}

function useScrollToEvidence(finding: Finding | null, active: ReviewDocument | null) {
  useEffect(() => {
    if (!finding || !active) return;
    const mark = document.querySelector('.col-doc [data-active-source="true"] .hit-evidence');
    const pane = mark?.closest<HTMLElement>(".col-doc");
    if (mark instanceof HTMLElement && pane && pane.scrollHeight > pane.clientHeight) {
      const markRect = mark.getBoundingClientRect();
      const paneRect = pane.getBoundingClientRect();
      // Move only the document pane. Scrolling every ancestor would overwrite the
      // page's restored position when opening a finding or switching materials.
      pane.scrollTop += markRect.top - paneRect.top - pane.clientHeight / 2;
    }
  }, [finding, active]);
}

function isActiveEvidence(active: ReviewDocument | null, evidenceDocs: ReviewDocument[]): boolean {
  return active !== null && evidenceDocs.some((item) => documentKey(item) === documentKey(active));
}

function shouldSplit(
  compareSources: boolean,
  layout: ReviewLayout,
  evidenceCount: number,
  activeIsEvidence: boolean,
): boolean {
  return compareSources && layout === "split" && evidenceCount >= 2 && activeIsEvidence;
}

function currentMatchOf(
  matches: { start: number; end: number }[],
  matchIndex: number,
): { start: number; end: number } | null {
  return matches[matchIndex] ?? null;
}

function relationIsSubmittable(
  onCreateRelation: ((input: CaseRelationInput) => Promise<void>) | undefined,
  relatedCaseId: string,
  relationReason: string,
): boolean {
  return onCreateRelation !== undefined && relatedCaseId !== "" && relationReason.trim() !== "";
}

function focusOffsetNode(offset: number, source: ReviewDocument | null) {
  if (source === null) return;
  const parsedDocumentId = source.parsed_document?.id ?? source.parsed_document_id ?? "";
  const candidates = Array.from(
    window.document.querySelectorAll<HTMLElement>(".doc-pane [data-offset]"),
  ).filter((node) => {
    if (node.dataset.offset !== String(offset)) return false;
    const pane = node.closest<HTMLElement>(".doc-pane");
    return (
      pane?.dataset.documentVersionId === source.id &&
      pane.dataset.parsedDocumentId === parsedDocumentId
    );
  });
  const node =
    candidates.find(
      (candidate) => candidate.closest<HTMLElement>(".doc-pane")?.dataset.activeSource === "true",
    ) ?? candidates[0];
  if (node && typeof node.scrollIntoView === "function") {
    node.scrollIntoView({ block: "center" });
  }
}
function focusAnchorOffset(source: ReviewDocument, offset: number) {
  focusOffsetNode(offset, source);
}

function EvidenceAside({
  caseContext,
  caseReturnState,
  leftTab,
  onTab,
  materials,
  collaborators,
  relatedCases,
}: {
  caseContext: ReviewCaseContext | null;
  caseReturnState?: CaseReturnState;
  leftTab: "材料" | "审查成员" | "相关案件";
  onTab: (tab: "材料" | "审查成员" | "相关案件") => void;
  materials: React.ReactNode;
  collaborators: React.ReactNode;
  relatedCases: React.ReactNode;
}) {
  return (
    <aside className="col-nav">
      {caseContext ? (
        <>
          <CaseContextHeader context={caseContext} caseReturnState={caseReturnState} />
          <ContextTabs context={caseContext} leftTab={leftTab} onTab={onTab} />
        </>
      ) : null}
      {leftTab === "材料" ? materials : null}
      {caseContext && leftTab === "审查成员" ? collaborators : null}
      {caseContext && leftTab === "相关案件" ? relatedCases : null}
    </aside>
  );
}

function KeyClauseQuickNavigation({
  onSelect,
  onGroup,
  onToggleAll,
  hasMore,
}: {
  onSelect: (candidate: KeyClauseCandidate) => void;
  onGroup: (group: ReadingGroup | "all") => void;
  onToggleAll: () => void;
  hasMore: boolean;
}) {
  const { navigation, selected, group, showAll } = useContext(ClauseContext);
  const candidates = navigation?.candidates ?? [];
  const selectedIndex = candidates.findIndex((candidate) => candidate.id === selected?.id);
  function move(direction: -1 | 1) {
    if (!candidates.length) return;
    const index =
      selectedIndex < 0
        ? direction === 1
          ? 0
          : candidates.length - 1
        : (selectedIndex + direction + candidates.length) % candidates.length;
    const candidate = candidates[index];
    if (candidate) onSelect(candidate);
  }
  return (
    <div className="key-clause-quick-nav" role="group" aria-label="重点位置导航">
      <div className="reading-category-filters" role="group" aria-label="阅读分类">
        <button type="button" aria-pressed={group === "all"} onClick={() => onGroup("all")}>
          全部
        </button>
        {READING_GROUPS.map((item) => (
          <button
            key={item.id}
            type="button"
            className={`reading-filter-${item.id}`}
            aria-pressed={group === item.id}
            onClick={() => onGroup(item.id)}
          >
            {item.label}
          </button>
        ))}
      </div>
      {hasMore ? (
        <Button htmlType="button" aria-pressed={showAll} onClick={onToggleAll}>
          {showAll ? "只看重点" : "显示更多条款"}
        </Button>
      ) : null}
      <span role="status">
        {candidates.length === 0
          ? navigation?.message || "当前分类没有匹配位置。"
          : selectedIndex < 0
            ? `阅读重点 · ${candidates.length} 处`
            : `第 ${selectedIndex + 1} / ${candidates.length} 处`}
      </span>
      <Button
        htmlType="button"
        disabled={!candidates.length}
        onClick={() => {
          const first = candidates[0];
          if (first) onSelect(first);
        }}
      >
        查看重点
      </Button>
      <Button
        disabled={!candidates.length}
        aria-label="上一个重点"
        htmlType="button"
        onClick={() => move(-1)}
      >
        上一处
      </Button>
      <Button
        disabled={!candidates.length}
        aria-label="下一个重点"
        htmlType="button"
        onClick={() => move(1)}
      >
        下一处
      </Button>
    </div>
  );
}

function DocumentColumn({
  documents,
  finding,
  active,
  textIndex,
  currentMatch,
  showDiagnostics,
  split,
  evidenceDocs,
  layout,
  activeIsEvidence,
  compareSources,
  onCompare,
  onActiveKey,
  quoteScope,
  focusRange,
  onClearKeyClause,
  onSelectKeyClause,
  onReadingGroup,
  onToggleAll,
  hasMoreCandidates,
  onQuoteSelectionChange,
}: {
  documents: ReviewDocument[];
  finding: Finding | null;
  active: ReviewDocument | null;
  textIndex: CodePointTextIndex | null;
  currentMatch: { start: number; end: number } | null;
  showDiagnostics: boolean;
  split: boolean;
  evidenceDocs: ReviewDocument[];
  layout: ReviewLayout;
  activeIsEvidence: boolean;
  compareSources: boolean;
  onCompare: (compare: boolean) => void;
  onActiveKey: (key: string) => void;
  quoteScope: FrozenQuoteScope | null;
  focusRange: { start: number; end: number } | null;
  onClearKeyClause?: () => void;
  onSelectKeyClause: (candidate: KeyClauseCandidate) => void;
  onReadingGroup: (group: ReadingGroup | "all") => void;
  onToggleAll: () => void;
  hasMoreCandidates: boolean;
  onQuoteSelectionChange?: (selection: FrozenQuoteSelection | null) => void;
}) {
  return (
    <section className="col-doc" aria-label="原文">
      <div className="reader-toolbar">
        <DocumentToolbar
          layout={layout}
          evidenceCount={evidenceDocs.length}
          activeIsEvidence={activeIsEvidence}
          compareSources={compareSources}
          onCompare={onCompare}
        />
        <KeyClauseQuickNavigation
          onSelect={onSelectKeyClause}
          onGroup={onReadingGroup}
          onToggleAll={onToggleAll}
          hasMore={hasMoreCandidates}
        />
      </div>
      {!split && evidenceDocs.length >= 2 ? (
        <SourceSwitch
          evidenceDocs={evidenceDocs}
          documents={documents}
          active={active}
          onSelect={onActiveKey}
        />
      ) : null}
      <DocumentView
        documents={documents}
        finding={finding}
        active={active}
        textIndex={textIndex}
        currentMatch={currentMatch}
        showDiagnostics={showDiagnostics}
        split={split}
        evidenceDocs={evidenceDocs}
        quoteScope={quoteScope}
        focusRange={focusRange}
        onClearKeyClause={onClearKeyClause}
        onQuoteSelectionChange={onQuoteSelectionChange}
      />
    </section>
  );
}

export function ReviewCaseContextPanel({
  context,
  caseReturnState,
  canEditRelations = false,
  relationPending = false,
  relationError = null,
  onCreateRelation,
  onDeleteRelation,
}: {
  context: ReviewCaseContext;
  caseReturnState?: CaseReturnState;
  canEditRelations?: boolean;
  relationPending?: boolean;
  relationError?: string | null;
  onCreateRelation?: (input: CaseRelationInput) => Promise<void>;
  onDeleteRelation?: (relationId: string) => void;
}) {
  const [relatedCaseId, setRelatedCaseId] = useState("");
  const [relationReason, setRelationReason] = useState("");
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!relationIsSubmittable(onCreateRelation, relatedCaseId, relationReason)) return;
    void onCreateRelation!({ related_case_id: relatedCaseId, reason: relationReason.trim() }).then(
      () => {
        setRelatedCaseId("");
        setRelationReason("");
      },
      () => undefined,
    );
  }
  return (
    <section className="review-case-context-panel" aria-label="案件上下文">
      <CaseContextHeader context={context} caseReturnState={caseReturnState} />
      <CollaboratorsPane context={context} />
      <RelatedCasesPane
        context={context}
        canEditRelations={canEditRelations}
        relationPending={relationPending}
        relationError={relationError}
        relatedCaseId={relatedCaseId}
        relationReason={relationReason}
        onCreateRelation={onCreateRelation}
        onDeleteRelation={onDeleteRelation}
        onRelatedCase={setRelatedCaseId}
        onReason={setRelationReason}
        onSubmitRelation={submit}
      />
    </section>
  );
}

type ImmersiveTopic =
  | "all"
  | "warranty"
  | "payment"
  | "delivery_acceptance"
  | "liability_compensation"
  | "dispute_resolution";
const IMMERSIVE_TOPICS: { id: ImmersiveTopic; label: string }[] = [
  { id: "all", label: "全部条款" },
  { id: "warranty", label: "质保" },
  { id: "payment", label: "付款" },
  { id: "delivery_acceptance", label: "验收" },
  { id: "liability_compensation", label: "违约责任" },
  { id: "dispute_resolution", label: "争议解决" },
];

function ImmersiveDocumentStage({
  documents,
  finding,
  findings,
  quoteScope,
  sourceDocumentId,
  versions,
  externalDocumentId,
  externalRange,
  onSelectFinding,
  onQuoteSelectionChange,
}: {
  documents: ReviewDocument[];
  finding: Finding | null;
  findings: Finding[];
  quoteScope: FrozenQuoteScope | null;
  sourceDocumentId: string | null;
  versions: MaterialVersionProjection[];
  externalDocumentId: string | null;
  externalRange: { start: number; end: number } | null;
  onSelectFinding: (id: string) => void;
  onQuoteSelectionChange?: (selection: FrozenQuoteSelection | null) => void;
}) {
  const [topic, setTopic] = useState<ImmersiveTopic>("all");
  const stage = useRef<HTMLElement>(null);
  const [chosen, setChosen] = useState<Record<string, string>>({});
  const [mobileRole, setMobileRole] = useState("procurement");
  const [linked, setLinked] = useState(true);
  const [focuses, setFocuses] = useState<Record<string, { start: number; end: number }>>({});
  const located = useRef<{
    request: string;
    focuses: typeof focuses;
    panes: WeakSet<HTMLElement>;
  } | null>(null);
  const roles = ["procurement", "draft_contract"] as const;
  const displayed = roles.flatMap((role) => {
    const candidates = documents.filter((item) => item.role === role);
    const explicit =
      candidates.find((item) => item.id === externalDocumentId) ??
      candidates.find((item) => item.id === chosen[role]) ??
      candidates.find((item) => item.id === sourceDocumentId);
    const document = explicit ?? (candidates.length === 1 ? candidates[0] : undefined);
    return document ? [document] : [];
  });
  const activeRole = displayed.some((item) => item.role === mobileRole)
    ? mobileRole
    : (displayed[0]?.role ?? mobileRole);
  const externalStart = externalRange?.start;
  const externalEnd = externalRange?.end;
  const requestedRole = documents.find(
    (item) => item.id === (externalDocumentId ?? sourceDocumentId),
  )?.role;
  useEffect(() => {
    if (requestedRole === "procurement" || requestedRole === "draft_contract")
      setMobileRole(requestedRole);
  }, [externalDocumentId, externalStart, externalEnd, sourceDocumentId, requestedRole]);
  const selectedSourceRole = documents.find((item) => item.id === sourceDocumentId)?.role;
  useEffect(() => {
    if (
      sourceDocumentId &&
      (selectedSourceRole === "procurement" || selectedSourceRole === "draft_contract")
    ) {
      setChosen((current) =>
        current[selectedSourceRole] === sourceDocumentId
          ? current
          : { ...current, [selectedSourceRole]: sourceDocumentId },
      );
    }
  }, [sourceDocumentId, selectedSourceRole]);
  const navigation = new Map(
    documents.map((document) => [document.id, keyClauseNavigationFor(quoteScope, document)]),
  );
  function matchingCandidates(document: ReviewDocument, selectedTopic: ImmersiveTopic) {
    const nav = navigation.get(document.id);
    return (nav?.allCandidates ?? nav?.candidates ?? []).filter(
      (candidate) => selectedTopic === "all" || candidate.categories.includes(selectedTopic),
    );
  }
  function count(selectedTopic: ImmersiveTopic): number {
    if (selectedTopic === "all") return findings.length;
    return findings.filter((item) =>
      item.evidence.some((anchor) => {
        const document = documentForAnchor(anchor, documents);
        if (
          !document ||
          !anchor.text_offset ||
          anchor.status !== "located" ||
          document.text === null
        )
          return false;
        const resolved = resolveAnchor(
          document.text,
          anchor,
          document.parsed_document,
          document.parsed_document_id,
        );
        if (resolved.kind !== "highlight") return false;
        return matchingCandidates(document, selectedTopic).some(
          (candidate) =>
            candidate.range.start < resolved.end && resolved.start < candidate.range.end,
        );
      }),
    ).length;
  }
  function selectTopic(next: ImmersiveTopic) {
    setTopic(next);
    const ranges: Record<string, { start: number; end: number }> = {};
    if (next !== "all")
      for (const document of linked
        ? displayed
        : displayed.filter((item) => item.role === activeRole)) {
        const candidate = matchingCandidates(document, next)[0];
        if (candidate && frozenQuoteMatchesDocument(candidate.selection, quoteScope, document))
          ranges[document.id] = candidate.range;
      }
    setFocuses(ranges);
  }
  useEffect(() => {
    setFocuses({});
  }, [finding?.id, quoteScope?.attemptId]);
  const request = JSON.stringify([
    quoteScope?.attemptId,
    finding?.id,
    sourceDocumentId,
    externalDocumentId,
    externalStart,
    externalEnd,
  ]);
  useEffect(() => {
    if (located.current?.request !== request || located.current.focuses !== focuses) {
      located.current = { request, focuses, panes: new WeakSet() };
    }
    const placed = located.current.panes;
    for (const pane of stage.current?.querySelectorAll<HTMLElement>(".immersive-document-column") ??
      []) {
      if (!pane.dataset.sourceId || placed.has(pane)) continue;
      const start =
        pane.dataset.sourceId === externalDocumentId
          ? externalStart
          : focuses[pane.dataset.sourceId]?.start;
      const mark =
        start === undefined
          ? pane.querySelector<HTMLElement>(".hit-evidence")
          : pane.querySelector<HTMLElement>(`[data-offset="${start}"]`);
      const scroller = mark?.closest<HTMLElement>("[data-review-document-scroll]");
      if (mark && scroller && scroller.clientHeight > 0) {
        scroller.scrollTop +=
          mark.getBoundingClientRect().top -
          scroller.getBoundingClientRect().top -
          scroller.clientHeight / 2;
        placed.add(pane);
      }
    }
  }, [activeRole, chosen, externalDocumentId, externalStart, focuses, request]);
  const extras = documents.filter((item) => !roles.includes(item.role as (typeof roles)[number]));
  return (
    <section ref={stage} className="immersive-document-stage" aria-label="原文对照">
      <div className="immersive-topics" role="group" aria-label="条款主题">
        {IMMERSIVE_TOPICS.map((item) => (
          <Button
            key={item.id}
            aria-pressed={topic === item.id}
            onClick={() => selectTopic(item.id)}
          >
            {item.label}
            {item.id !== "all" && count(item.id) > 0 ? ` ${count(item.id)}` : ""}
          </Button>
        ))}
        <label className="immersive-link-switch">
          <input
            type="checkbox"
            checked={linked}
            onChange={(event) => setLinked(event.target.checked)}
          />{" "}
          按条款联动
        </label>
      </div>
      {displayed.length > 1 && (
        <div className="immersive-role-switch" role="group" aria-label="切换对照材料">
          {displayed.map((document) => (
            <Button
              key={document.id}
              aria-pressed={activeRole === document.role}
              onClick={() => setMobileRole(document.role)}
            >
              {roleLabel(document.role)}
            </Button>
          ))}
        </div>
      )}
      {roles.map((role) => {
        const choices = documents.filter((item) => item.role === role);
        if (choices.length < 2) return null;
        return (
          <label className="immersive-source-choice" key={role}>
            {roleLabel(role)}
            <select
              value={
                chosen[role] ??
                (choices.some((item) => item.id === sourceDocumentId)
                  ? (sourceDocumentId ?? "")
                  : "")
              }
              onChange={(event) => setChosen((old) => ({ ...old, [role]: event.target.value }))}
            >
              <option value="">请选择材料</option>
              {choices.map((item) => (
                <option key={documentKey(item)} value={item.id}>
                  {item.filename}
                </option>
              ))}
            </select>
          </label>
        );
      })}
      <div className="immersive-document-columns">
        {displayed.map((document) => {
          const matches = versions.filter((item) => item.document_id === document.id);
          const version = matches.length === 1 ? `第 ${matches[0]!.version} 版` : "版本未记录";
          const range =
            document.id === externalDocumentId ? externalRange : (focuses[document.id] ?? null);
          return (
            <article
              key={documentKey(document)}
              data-role={document.role}
              data-relation={finding?.relation}
              className={`immersive-document-column${displayed.length === 1 || activeRole === document.role ? " is-mobile-active" : ""}`}
              data-source-id={document.id}
              onPointerDown={() => setMobileRole(document.role)}
            >
              <header>
                <strong>{roleLabel(document.role)}</strong>
                <span title={document.filename}>{document.filename}</span>
                <span>{version}</span>
              </header>
              <div
                className="immersive-document-scroll col-doc"
                data-review-document-scroll
                tabIndex={0}
                role="region"
                aria-label={`${roleLabel(document.role)}正文`}
              >
                <FindingMarkerContext.Provider
                  value={{
                    markers: findings.flatMap((item, index) =>
                      item.evidence.flatMap((anchor) => {
                        if (
                          !evidenceSourceMatches(
                            anchor,
                            document.id,
                            document.parsed_document,
                            document.parsed_document_id,
                          ) ||
                          document.text === null
                        )
                          return [];
                        const resolved = resolveAnchor(
                          document.text,
                          anchor,
                          document.parsed_document,
                          document.parsed_document_id,
                        );
                        return resolved.kind === "highlight" &&
                          quoteScope?.sourceProofs?.some(
                            (proof) =>
                              proof.documentId === document.id &&
                              proof.parsedDocumentId === document.parsed_document_id &&
                              proof.originalDigest === document.digest,
                          )
                          ? [
                              {
                                start: resolved.start,
                                end: resolved.end,
                                number: index + 1,
                                findingId: item.id,
                                relation: item.relation,
                              },
                            ]
                          : [];
                      }),
                    ),
                    onSelect: onSelectFinding,
                  }}
                >
                  <DocumentBody
                    document={document}
                    finding={finding}
                    immersive
                    findRange={range}
                    focusRange={range}
                    textIndex={null}
                    activeSource
                    showDiagnostics={false}
                    quoteScope={quoteScope}
                    onQuoteSelectionChange={onQuoteSelectionChange}
                  />
                </FindingMarkerContext.Provider>
              </div>
            </article>
          );
        })}
        {displayed.length === 0 && <p>请选择本次审查的材料。</p>}
      </div>
      {extras.length > 0 && (
        <details
          className="immersive-other-sources"
          open={extras.some((item) => item.id === sourceDocumentId) || undefined}
        >
          <summary>其他材料（{extras.length}）</summary>
          {extras.map((document) => (
            <article key={documentKey(document)}>
              <h3>{document.filename}</h3>
              <DocumentBody
                document={document}
                finding={finding}
                findRange={null}
                textIndex={null}
                activeSource
                showDiagnostics={false}
                quoteScope={quoteScope}
                onQuoteSelectionChange={onQuoteSelectionChange}
              />
            </article>
          ))}
        </details>
      )}
    </section>
  );
}

export function DocumentStage({
  documents,
  finding,
  findings,
  selectedFindingId,
  layout,
  onSelectFinding,
  showDiagnostics = false,
  caseContext = null,
  caseReturnState,
  canEditRelations = false,
  relationPending = false,
  relationError = null,
  onCreateRelation,
  onDeleteRelation,
  quoteScope = null,
  sourceDocumentId = null,
  contractFactsCitation = null,
  coverageEvidence = null,
  contractFactsLocationScope = null,
  onQuoteSelectionChange,
  immersive = false,
  materialVersions = [],
}: {
  documents: ReviewDocument[];
  finding: Finding | null;
  findings: Finding[];
  selectedFindingId: string | null;
  immersive?: boolean;
  materialVersions?: MaterialVersionProjection[];
  layout: ReviewLayout;
  showDiagnostics?: boolean;
  onSelectFinding: (findingId: string) => void;
  caseContext?: ReviewCaseContext | null;
  caseReturnState?: CaseReturnState;
  canEditRelations?: boolean;
  relationPending?: boolean;
  relationError?: string | null;
  onCreateRelation?: (input: CaseRelationInput) => Promise<void>;
  onDeleteRelation?: (relationId: string) => void;
  quoteScope?: FrozenQuoteScope | null;
  sourceDocumentId?: string | null;
  contractFactsCitation?: ContractFactsCitation | null;
  coverageEvidence?: EvidenceAnchor | null;
  contractFactsLocationScope?: ContractFactsLocationScope | null;
  onQuoteSelectionChange?: (selection: FrozenQuoteSelection | null) => void;
}) {
  const initialDocument =
    (sourceDocumentId ? documents.find((document) => document.id === sourceDocumentId) : null) ??
    initialDocumentOf(finding, documents);
  const [activeKey, setActiveKey] = useState(() =>
    initialDocument ? documentKey(initialDocument) : "",
  );
  const [query, setQuery] = useState("");
  const deferredQuery = useDeferredValue(query);
  const [matchIndex, setMatchIndex] = useState(0);
  const [showAllCandidates, setShowAllCandidates] = useState(false);
  const [readingGroup, setReadingGroup] = useState<ReadingGroup | "all">("all");
  const [selectedKeyClauseId, setSelectedKeyClauseId] = useState<string | null>(null);
  const [compareSources, setCompareSources] = useState(false);
  const [leftTab, setLeftTab] = useState<"材料" | "审查成员" | "相关案件">("材料");
  const [relatedCaseId, setRelatedCaseId] = useState("");
  const [relationReason, setRelationReason] = useState("");
  const active = activeDocumentOf(documents, activeKey);
  const requestedSource = sourceDocumentId
    ? documents.find((document) => document.id === sourceDocumentId)
    : null;
  const requestedSourceKey = requestedSource ? documentKey(requestedSource) : "";
  useEffect(() => {
    if (requestedSourceKey) setActiveKey(requestedSourceKey);
  }, [requestedSourceKey]);
  const factSource = useMemo(() => {
    const fromFacts = documentForContractFactsCitation(
      contractFactsCitation,
      documents,
      contractFactsLocationScope,
    );
    if (fromFacts) return fromFacts;
    if (
      !coverageEvidence ||
      coverageEvidence.status !== "located" ||
      !coverageEvidence.text_offset ||
      !coverageEvidence.parsed_document_id ||
      !quoteScope?.isFrozenAttempt ||
      !quoteScope.parsedDocumentIds.includes(coverageEvidence.parsed_document_id)
    )
      return null;
    const matches = documents.filter(
      (document) =>
        document.id === coverageEvidence.document_version_id &&
        document.parsed_document_id === coverageEvidence.parsed_document_id &&
        document.text !== null &&
        quoteScope.sourceProofs?.some(
          (proof) =>
            proof.documentId === document.id &&
            proof.parsedDocumentId === document.parsed_document_id &&
            proof.originalDigest === document.digest,
        ) &&
        (!document.parsed_document ||
          !coverageEvidence.normalized_digest ||
          document.parsed_document.normalized_digest === coverageEvidence.normalized_digest) &&
        sliceByCodePoints(
          document.text,
          coverageEvidence.text_offset!.start,
          coverageEvidence.text_offset!.end,
        ) === coverageEvidence.quote,
    );
    return matches.length === 1 ? matches[0]! : null;
  }, [contractFactsCitation, contractFactsLocationScope, coverageEvidence, documents, quoteScope]);
  const factRange = factSource
    ? contractFactsCitation
      ? { start: contractFactsCitation.start, end: contractFactsCitation.end }
      : (coverageEvidence?.text_offset ?? null)
    : null;
  const quoteParsedIds = quoteScope?.parsedDocumentIds.join("\u0000") ?? "";
  useEffect(() => {
    onQuoteSelectionChange?.(null);
  }, [
    active?.id,
    active?.parsed_document?.id,
    active?.parsed_document_id,
    active?.digest,
    active?.text,
    onQuoteSelectionChange,
    quoteScope?.attemptId,
    quoteScope?.caseId,
    quoteScope?.isFrozenAttempt,
    quoteScope?.runId,
    quoteParsedIds,
    selectedFindingId,
  ]);
  useEffect(() => {
    setSelectedKeyClauseId(null);
  }, [
    active?.id,
    active?.parsed_document?.id,
    active?.parsed_document_id,
    active?.digest,
    quoteScope?.attemptId,
    quoteScope?.caseId,
    quoteScope?.isFrozenAttempt,
    quoteScope?.runId,
    quoteParsedIds,
    selectedFindingId,
  ]);
  const activeTextIndex = useMemo(
    () => (active === null || active.text === null ? null : createTextIndex(active.text)),
    [active, active?.parsed_document, active?.parsed_document_id, active?.text],
  );
  const evidenceDocs = useMemo(() => evidenceDocumentsOf(finding, documents), [documents, finding]);
  useEffect(() => {
    if (!factSource) return;
    setActiveKey(documentKey(factSource));
    setSelectedKeyClauseId(null);
    setCompareSources(false);
  }, [factSource]);
  useEvidenceAutoSwitch(
    finding,
    active,
    documents,
    setActiveKey,
    factSource !== null || Boolean(sourceDocumentId),
  );
  const relatedFindings = useMemo(() => relatedFindingsOf(active, findings), [active, findings]);
  const activeIsEvidence = isActiveEvidence(active, evidenceDocs);
  const split = shouldSplit(compareSources, layout, evidenceDocs.length, activeIsEvidence);
  const matches = useMemo(
    () =>
      active?.text ? findTextMatches(active.text, deferredQuery, activeTextIndex ?? undefined) : [],
    [active, activeTextIndex, deferredQuery],
  );
  const currentMatch =
    factRange &&
    active !== null &&
    factSource !== null &&
    documentKey(active) === documentKey(factSource)
      ? factRange
      : currentMatchOf(matches, matchIndex);
  useScrollToEvidence(finding, active);
  useEffect(() => {
    if (!factRange || !factSource || !active || documentKey(active) !== documentKey(factSource))
      return;
    const target = window.document.querySelector<HTMLElement>(
      `.doc-pane[data-active-source="true"] [data-offset="${factRange.start}"]`,
    );
    const pane = target?.closest<HTMLElement>(".col-doc");
    if (target && pane && pane.scrollHeight > pane.clientHeight) {
      pane.scrollTop +=
        target.getBoundingClientRect().top -
        pane.getBoundingClientRect().top -
        pane.clientHeight / 2;
    }
  }, [active, factRange, factSource]);
  const visibleClauses = useMemo(
    () => visibleClausesOf(active, deferredQuery),
    [active, deferredQuery],
  );
  const allKeyClauseNavigation = useMemo(
    () => keyClauseNavigationFor(quoteScope, active),
    [active, quoteScope],
  );
  const keyClauseNavigation = useMemo(() => {
    const available = showAllCandidates
      ? (allKeyClauseNavigation.allCandidates ?? allKeyClauseNavigation.candidates)
      : allKeyClauseNavigation.candidates;
    const candidates =
      readingGroup === "all"
        ? available
        : available.filter((candidate) => readingGroupFor(candidate) === readingGroup);
    return {
      ...allKeyClauseNavigation,
      candidates,
      message: candidates.length
        ? ""
        : readingGroup === "all"
          ? allKeyClauseNavigation.message
          : "当前分类没有匹配位置。",
    };
  }, [allKeyClauseNavigation, readingGroup, showAllCandidates]);
  const selectedKeyClause =
    keyClauseNavigation.candidates.find(
      (candidate) =>
        candidate.id === selectedKeyClauseId &&
        active !== null &&
        candidate.documentId === active.id &&
        candidate.parsedDocumentId === active.parsed_document_id &&
        frozenQuoteMatchesDocument(candidate.selection, quoteScope, active),
    ) ?? null;

  function submitRelation(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!relationIsSubmittable(onCreateRelation, relatedCaseId, relationReason)) return;
    void onCreateRelation!({
      related_case_id: relatedCaseId,
      reason: relationReason.trim(),
    }).then(
      () => {
        setRelatedCaseId("");
        setRelationReason("");
      },
      () => undefined,
    );
  }

  function moveMatch(direction: -1 | 1) {
    if (matches.length === 0) return;
    const next = (matchIndex + direction + matches.length) % matches.length;
    const range = matches[next];
    setMatchIndex(next);
    if (range) focusOffsetNode(range.start, active);
  }

  function selectKeyClause(candidate: KeyClauseCandidate) {
    if (
      !active ||
      !quoteScope ||
      candidate.documentId !== active.id ||
      candidate.parsedDocumentId !== active.parsed_document_id ||
      !frozenQuoteMatchesDocument(candidate.selection, quoteScope, active)
    ) {
      setSelectedKeyClauseId(null);
      return;
    }
    setSelectedKeyClauseId(candidate.id);
    onQuoteSelectionChange?.(null);
    focusOffsetNode(candidate.range.start, active);
  }

  if (immersive) {
    return (
      <ImmersiveDocumentStage
        documents={documents}
        finding={finding}
        findings={findings}
        quoteScope={quoteScope}
        sourceDocumentId={sourceDocumentId}
        versions={materialVersions}
        externalDocumentId={factSource?.id ?? null}
        externalRange={factRange}
        onSelectFinding={onSelectFinding}
        onQuoteSelectionChange={onQuoteSelectionChange}
      />
    );
  }

  return (
    <>
      <EvidenceAside
        caseContext={caseContext}
        caseReturnState={caseReturnState}
        leftTab={leftTab}
        onTab={setLeftTab}
        collaborators={<CollaboratorsPane context={caseContext!} />}
        relatedCases={
          <RelatedCasesPane
            context={caseContext!}
            canEditRelations={canEditRelations}
            relationPending={relationPending}
            relationError={relationError}
            relatedCaseId={relatedCaseId}
            relationReason={relationReason}
            onCreateRelation={onCreateRelation}
            onDeleteRelation={onDeleteRelation}
            onRelatedCase={setRelatedCaseId}
            onReason={setRelationReason}
            onSubmitRelation={submitRelation}
          />
        }
        materials={
          <MaterialPane
            documents={documents}
            active={active}
            query={query}
            resultQuery={deferredQuery}
            matches={matches}
            currentMatchIndex={matchIndex}
            clauses={visibleClauses}
            keyClauseNavigation={keyClauseNavigation}
            selectedKeyClauseId={selectedKeyClause?.id ?? null}
            relatedFindings={relatedFindings}
            selectedFindingId={selectedFindingId}
            onActiveKey={(key) => {
              setActiveKey(key);
              setMatchIndex(0);
            }}
            onQuery={(value) => {
              setQuery(value);
              setMatchIndex(0);
            }}
            onPreviousMatch={() => moveMatch(-1)}
            onNextMatch={() => moveMatch(1)}
            onFocusOffset={(offset) => focusOffsetNode(offset, active)}
            onSelectKeyClause={selectKeyClause}
            onSelectFinding={onSelectFinding}
          />
        }
      />
      <ClauseContext.Provider
        value={{
          document: active,
          navigation: keyClauseNavigation,
          ranges: [],
          group: readingGroup,
          showAll: showAllCandidates,
          findRange: null,
          selected: selectedKeyClause,
        }}
      >
        <DocumentColumn
          documents={documents}
          finding={finding}
          textIndex={activeTextIndex}
          active={active}
          currentMatch={currentMatch}
          showDiagnostics={showDiagnostics}
          split={split}
          evidenceDocs={evidenceDocs}
          layout={layout}
          activeIsEvidence={activeIsEvidence}
          compareSources={compareSources}
          onCompare={setCompareSources}
          onActiveKey={setActiveKey}
          quoteScope={quoteScope}
          focusRange={
            factRange &&
            active !== null &&
            factSource !== null &&
            documentKey(active) === documentKey(factSource)
              ? factRange
              : null
          }
          onSelectKeyClause={selectKeyClause}
          hasMoreCandidates={
            (allKeyClauseNavigation.allCandidates ?? allKeyClauseNavigation.candidates).filter(
              (candidate) => readingGroup === "all" || readingGroupFor(candidate) === readingGroup,
            ).length >
            allKeyClauseNavigation.candidates.filter(
              (candidate) => readingGroup === "all" || readingGroupFor(candidate) === readingGroup,
            ).length
          }
          onToggleAll={() => {
            setShowAllCandidates((value) => !value);
            setSelectedKeyClauseId(null);
          }}
          onReadingGroup={(group) => {
            setReadingGroup(group);
            setSelectedKeyClauseId(null);
          }}
          onClearKeyClause={() => setSelectedKeyClauseId(null)}
          onQuoteSelectionChange={onQuoteSelectionChange}
        />
      </ClauseContext.Provider>
    </>
  );
}
