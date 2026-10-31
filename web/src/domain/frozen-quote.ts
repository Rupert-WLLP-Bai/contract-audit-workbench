import type { ReviewDocument, TextOffset } from "../api/types";
import { sliceByCodePoints, type CodePointTextIndex } from "./text";

export interface FrozenQuoteSelection {
  caseId: string;
  runId: string;
  attemptId: string;
  documentId: string;
  parsedDocumentId: string;
  digest: string;
  quote: string;
  start: number;
  end: number;
}

export interface FrozenQuoteScope {
  caseId: string;
  runId: string;
  attemptId: string | null;
  isFrozenAttempt: boolean;
  parsedDocumentIds: readonly string[];
}

function parsedDocumentIdOf(document: ReviewDocument): string | null {
  const parsedId = document.parsed_document_id;
  if (!parsedId || parsedId.trim() === "") return null;
  if (document.parsed_document && (
    document.parsed_document.id !== parsedId ||
    document.parsed_document.document_id !== document.id ||
    document.parsed_document.status !== "ready"
  )) return null;
  return parsedId;
}

function scopeMatchesDocument(
  scope: FrozenQuoteScope,
  document: ReviewDocument,
): string | null {
  const parsedDocumentId = parsedDocumentIdOf(document);
  if (!scope.isFrozenAttempt || !scope.caseId.trim() || !scope.runId.trim() ||
    !scope.attemptId?.trim() || document.parse_status !== "ready" ||
    document.text === null || document.text === "" ||
    !parsedDocumentId || !scope.parsedDocumentIds.includes(parsedDocumentId)) return null;
  return parsedDocumentId;
}

export function createFrozenQuoteSelection(
  scope: FrozenQuoteScope | null,
  document: ReviewDocument,
  range: TextOffset,
  textIndex?: CodePointTextIndex,
): FrozenQuoteSelection | null {
  if (scope === null) return null;
  const parsedDocumentId = scopeMatchesDocument(scope, document);
  if (!parsedDocumentId || document.text === null) return null;
  const quote = sliceByCodePoints(document.text, range.start, range.end, textIndex);
  if (quote === null || Array.from(quote).length > 4096) return null;
  return {
    caseId: scope.caseId,
    runId: scope.runId,
    attemptId: scope.attemptId!,
    documentId: document.id,
    parsedDocumentId,
    digest: document.digest,
    quote,
    start: range.start,
    end: range.end,
  };
}

export function frozenQuoteMatchesDocument(
  selection: FrozenQuoteSelection | null,
  scope: FrozenQuoteScope | null,
  document: ReviewDocument | undefined,
): document is ReviewDocument {
  if (selection === null || scope === null || document === undefined || document.text === null) return false;
  const parsedDocumentId = scopeMatchesDocument(scope, document);
  return parsedDocumentId !== null &&
    selection.caseId === scope.caseId &&
    selection.runId === scope.runId &&
    selection.attemptId === scope.attemptId &&
    selection.documentId === document.id &&
    selection.parsedDocumentId === parsedDocumentId &&
    selection.digest === document.digest &&
    Array.from(selection.quote).length <= 4096 &&
    sliceByCodePoints(document.text, selection.start, selection.end) === selection.quote;
}
