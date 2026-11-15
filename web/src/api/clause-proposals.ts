// File metadata (manifest mirror):
// Purpose: Uses generated wire contracts for authorized proposal listing, creation, derived text and explicit decisions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { jsonInit, requestJson } from "./http";
import { paths } from "./paths";
import { responseReader } from "./respond";
import type { WireSchemas } from "./wire.generated";

export type ClauseProposalEvidence = NonNullable<WireSchemas["ClauseProposal"]["evidence"]>;
export type ClauseProposalHistory = WireSchemas["ClauseProposal"]["history"][number];
export type ClauseProposal = WireSchemas["ClauseProposal"];
export type ClauseProposalList = WireSchemas["ClauseProposalList"];
export type CreateClauseProposalInput = WireSchemas["ClauseProposalCreate"];
export type ClauseProposalDerivedTextInput = WireSchemas["ClauseDerivedRequest"];
export type ClauseProposalDerivedText = WireSchemas["ClauseDerivedResponse"] & {
  format: "derived_plain_text";
};
export type ClauseProposalDecisionInput = Omit<
  WireSchemas["ClauseProposalDecision"],
  "action" | "confirmed"
> & {
  action: "accept" | "accept_edited" | "reject";
  confirmed: true;
};

const reader = responseReader((path) => `条款建议响应无法识别：${path}`);

function parseEvidence(value: unknown, path: string): ClauseProposalEvidence {
  const row = reader.object(value, path);
  const start = reader.integer(row.start, `${path}.start`, 0);
  const end = reader.integer(row.end, `${path}.end`, 1);
  const quote = reader.text(row.quote, `${path}.quote`);
  if (end <= start || quote === "") throw new Error(`${path} 的冻结引用区间无效`);
  return {
    document_id: reader.text(row.document_id, `${path}.document_id`),
    parsed_document_id: reader.text(row.parsed_document_id, `${path}.parsed_document_id`),
    normalized_digest: reader.text(row.normalized_digest, `${path}.normalized_digest`),
    start,
    end,
    quote,
  };
}

function parseHistory(value: unknown, path: string): ClauseProposalHistory {
  const row = reader.object(value, path);
  const action = reader.text(row.action, `${path}.action`);
  if (action !== "accept" && action !== "accept_edited" && action !== "reject") {
    throw new Error(`${path}.action 无效`);
  }
  return {
    version: reader.integer(row.version, `${path}.version`, 1),
    action,
    actor_id: reader.text(row.actor_id, `${path}.actor_id`),
    reason: reader.maybeText(row.reason, `${path}.reason`) ?? "",
    accepted_text: reader.maybeText(row.accepted_text, `${path}.accepted_text`) ?? "",
    contract_id: reader.maybeText(row.contract_id, `${path}.contract_id`) ?? "",
    material_id: reader.maybeText(row.material_id, `${path}.material_id`) ?? "",
    document_id: reader.maybeText(row.document_id, `${path}.document_id`) ?? "",
    sha256: reader.maybeText(row.sha256, `${path}.sha256`) ?? "",
    revision_id: reader.maybeText(row.revision_id, `${path}.revision_id`) ?? "",
    created_at: reader.text(row.created_at, `${path}.created_at`),
  };
}

function parseRulePack(value: unknown, path: string): ClauseProposal["rule_pack"] {
  if (value === undefined || value === null) return undefined;
  const row = reader.object(value, path);
  return {
    id: reader.text(row.id, `${path}.id`),
    version: reader.text(row.version, `${path}.version`),
    content_hash: reader.text(row.content_hash, `${path}.content_hash`),
    snapshot_digest: reader.text(row.snapshot_digest, `${path}.snapshot_digest`),
    compiler_ref: reader.text(row.compiler_ref, `${path}.compiler_ref`),
  };
}

function parsePlaybook(value: unknown, path: string): ClauseProposal["playbook"] {
  if (value === undefined || value === null) return undefined;
  const row = reader.object(value, path);
  return {
    knowledge_id: reader.text(row.knowledge_id, `${path}.knowledge_id`),
    version_id: reader.text(row.version_id, `${path}.version_id`),
    content_sha256: reader.text(row.content_sha256, `${path}.content_sha256`),
  };
}

export function parseClauseProposal(value: unknown, path = "proposal"): ClauseProposal {
  const row = reader.object(value, path);
  const status = reader.text(row.status, `${path}.status`);
  if (
    status !== "pending" &&
    status !== "accept" &&
    status !== "accept_edited" &&
    status !== "reject"
  ) {
    throw new Error(`${path}.status 无效`);
  }
  const history = reader.array(row.history, `${path}.history`, parseHistory);
  const evidence =
    row.evidence === undefined || row.evidence === null
      ? undefined
      : parseEvidence(row.evidence, `${path}.evidence`);
  const findingId = reader.maybeText(row.finding_id, `${path}.finding_id`);
  const originalText = reader.maybeText(row.original_text, `${path}.original_text`);
  const beforeContext = reader.maybeText(row.before_context, `${path}.before_context`);
  const afterContext = reader.maybeText(row.after_context, `${path}.after_context`);
  const remediationId = reader.maybeText(row.remediation_id, `${path}.remediation_id`);
  const unavailableReason = reader.maybeText(row.unavailable_reason, `${path}.unavailable_reason`);
  return {
    id: reader.text(row.id, `${path}.id`),
    case_id: reader.text(row.case_id, `${path}.case_id`),
    created_at: reader.text(row.created_at, `${path}.created_at`),
    created_by: reader.text(row.created_by, `${path}.created_by`),
    run_id: reader.text(row.run_id, `${path}.run_id`),
    attempt_id: reader.text(row.attempt_id, `${path}.attempt_id`),
    input_hash: reader.text(row.input_hash, `${path}.input_hash`),
    ...(findingId === undefined ? {} : { finding_id: findingId }),
    ...(evidence === undefined ? {} : { evidence }),
    ...(originalText === undefined ? {} : { original_text: originalText }),
    ...(beforeContext === undefined ? {} : { before_context: beforeContext }),
    ...(afterContext === undefined ? {} : { after_context: afterContext }),
    proposed_text: reader.text(row.proposed_text, `${path}.proposed_text`),
    reason: reader.text(row.reason, `${path}.reason`),
    ...(remediationId === undefined ? {} : { remediation_id: remediationId }),
    evidence_verified: reader.bool(row.evidence_verified, `${path}.evidence_verified`),
    ...(unavailableReason === undefined ? {} : { unavailable_reason: unavailableReason }),
    ...(row.rule_pack === undefined || row.rule_pack === null
      ? {}
      : { rule_pack: parseRulePack(row.rule_pack, `${path}.rule_pack`) }),
    ...(row.playbook === undefined || row.playbook === null
      ? {}
      : { playbook: parsePlaybook(row.playbook, `${path}.playbook`) }),
    status,
    version: reader.integer(row.version, `${path}.version`, 1),
    history,
  };
}

export function parseClauseProposalList(value: unknown): ClauseProposalList {
  const row = reader.object(value, "clause_proposals");
  return {
    items: reader.array(row.items, "clause_proposals.items", parseClauseProposal),
  };
}

export function parseClauseProposalDerivedText(value: unknown): ClauseProposalDerivedText {
  const row = reader.object(value, "derived_clause_text");
  const format = reader.text(row.format, "derived_clause_text.format");
  if (format !== "derived_plain_text") throw new Error("修改稿格式不是派生纯文本");
  return { text: reader.text(row.text, "derived_clause_text.text"), format };
}

export function listClauseProposals(caseId: string, signal?: AbortSignal) {
  return requestJson(
    paths.clauseProposals(caseId),
    { method: "GET", signal },
    parseClauseProposalList,
  );
}

export function createClauseProposal(
  caseId: string,
  input: CreateClauseProposalInput,
  idempotencyKey: string,
) {
  return requestJson(
    paths.clauseProposals(caseId),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseClauseProposal,
  );
}

export function deriveClauseProposalText(
  caseId: string,
  proposalId: string,
  input: ClauseProposalDerivedTextInput,
) {
  return requestJson(
    paths.clauseProposalDerivedText(caseId, proposalId),
    jsonInit("POST", input),
    parseClauseProposalDerivedText,
  );
}

export function decideClauseProposal(
  caseId: string,
  proposalId: string,
  input: ClauseProposalDecisionInput,
  idempotencyKey: string,
) {
  return requestJson(
    paths.clauseProposalDecision(caseId, proposalId),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseClauseProposal,
  );
}
