// File metadata (manifest mirror):
// Purpose: Validates existing authorized case-document and exact current or historical parse identities into minimal frozen source proofs without copying parsed content or fabricating metadata.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { CaseDetail, MaterialParsedDocumentList, ReviewDocument } from "../api/types";

export interface FrozenSourceProof {
  documentId: string;
  parsedDocumentId: string;
  originalDigest: string;
}

export function frozenSourceProof(
  detail: CaseDetail | undefined,
  document: ReviewDocument,
  attemptParsedIds: readonly string[],
  history: MaterialParsedDocumentList | undefined,
): FrozenSourceProof | null {
  const parsedId = document.parsed_document_id;
  if (
    !detail ||
    !parsedId ||
    !attemptParsedIds.includes(parsedId) ||
    document.parse_status !== "ready"
  )
    return null;
  const matches = detail.documents.filter((item) => item.id === document.id);
  if (matches.length !== 1) return null;
  const original = matches[0]!;
  if (!/^[a-f0-9]{64}$/i.test(original.digest)) return null;
  if (document.digest && document.digest !== original.digest) return null;
  const workspaceMetadata = document.parsed_document;
  if (
    workspaceMetadata &&
    (workspaceMetadata.id !== parsedId ||
      workspaceMetadata.document_id !== document.id ||
      workspaceMetadata.status !== "ready" ||
      workspaceMetadata.original_digest !== original.digest)
  )
    return null;
  const current = original.parsed_document;
  if (
    current?.id === parsedId &&
    current.document_id === document.id &&
    current.status === "ready" &&
    current.original_digest === original.digest
  ) {
    return { documentId: document.id, parsedDocumentId: parsedId, originalDigest: original.digest };
  }
  const versions = (detail.material_versions ?? []).filter(
    (item) => item.document_id === document.id,
  );
  if (versions.length !== 1 || !history) return null;
  const version = versions[0]!;
  if (
    history.document_id !== document.id ||
    history.material_id !== version.material_id ||
    history.material_version !== version.version
  )
    return null;
  const parses = history.items.filter((item) => item.parsed_document_id === parsedId);
  if (
    parses.length !== 1 ||
    parses[0]!.status !== "ready" ||
    parses[0]!.original_digest !== original.digest
  )
    return null;
  return { documentId: document.id, parsedDocumentId: parsedId, originalDigest: original.digest };
}
