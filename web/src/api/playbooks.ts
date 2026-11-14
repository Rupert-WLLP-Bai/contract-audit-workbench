// File metadata (manifest mirror):
// Purpose: Reads authorized playbook catalogs and parses frozen exact adoption identities without substituting current published versions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { requestJson } from "./http";
import { paths } from "./paths";
import { responseReader } from "./respond";
import type { WireSchemas } from "./wire.generated";

export type PlaybookRef = WireSchemas["GovernedKnowledgeRef"];
export type PlaybookCandidate = WireSchemas["PlaybookOption"];
export type PlaybookCandidateList = WireSchemas["PlaybookOptions"];
export type FrozenPlaybookAdoption = WireSchemas["AdoptedPlaybookView"];

const reader = responseReader((path) => `手册响应无法识别：${path}`);

export function parsePlaybookRef(value: unknown, path = "playbook_ref"): PlaybookRef {
  const row = reader.object(value, path);
  return {
    knowledge_id: reader.text(row.knowledge_id, `${path}.knowledge_id`),
    version_id: reader.text(row.version_id, `${path}.version_id`),
    content_sha256: reader.text(row.content_sha256, `${path}.content_sha256`),
  };
}

export function parseFrozenPlaybookAdoption(
  value: unknown,
  path = "adopted_playbook",
): FrozenPlaybookAdoption {
  const row = reader.object(value, path);
  return {
    ref: parsePlaybookRef(row.ref, `${path}.ref`),
    content_unavailable_reason: reader.text(
      row.content_unavailable_reason,
      `${path}.content_unavailable_reason`,
    ),
  };
}

function candidate(value: unknown, path: string): PlaybookCandidate {
  const row = reader.object(value, path);
  return {
    ref: parsePlaybookRef(row.ref, `${path}.ref`),
    title: reader.text(row.title, `${path}.title`),
  };
}

export function parsePlaybookCandidateList(value: unknown): PlaybookCandidateList {
  const row = reader.object(value, "playbooks");
  const reason = reader.maybeText(row.reason, "playbooks.reason");
  return {
    items: reader.array(row.items, "playbooks.items", candidate),
    ...(reason === undefined ? {} : { reason }),
  };
}

export function listCasePlaybooks(caseId: string, signal?: AbortSignal) {
  return requestJson(
    paths.casePlaybooks(caseId),
    { method: "GET", signal },
    parsePlaybookCandidateList,
  );
}
