// File metadata (manifest mirror):
// Purpose: Distinguishes legacy and current single-contract coverage and verifies recognized terms against the frozen parse identity and source text before display.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-21
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import type { ReviewScopeIdentity } from "../../api/review-readiness";
import type {
  ConstraintEvaluation,
  EvidenceAnchor,
  Finding,
  ReviewDocument,
  ReviewWorkspace,
} from "../../api/types";
import { createTextIndex, sliceByCodePoints } from "../../domain/text";

// The digest identifies the published, immutable v1 mapping. It is not a
// substitute for the scope identity returned with the selected attempt.
const SINGLE_CONTRACT_V1_HASH = "51676a95611b6e1bf4c30fc50be82e1d493528f34b89f3ece9dbb5aa6c291596";
const SINGLE_CONTRACT_V2_HASH = "3a26ce92e0dfd674d444f3ab2ca832d71d161f84d0a8bb94f8eee33c7ff2f8ab";
const paymentIdentity = {
  "1": {
    rule: "single-payment-sum-v1",
    domain: "payment_sum",
    capability: "single_contract.payment_sum",
  },
  "2": {
    rule: "single-payment-terms-v2",
    domain: "payment_terms",
    capability: "single_contract.payment_terms",
  },
};

export function singleContractVersion(
  scope: ReviewScopeIdentity | null | undefined,
): "1" | "2" | null {
  if (scope?.id !== "single_contract_precheck" || scope.rule_set_id !== "single-contract-precheck")
    return null;
  if (scope.rule_set_version === "1" && scope.rule_set_hash === SINGLE_CONTRACT_V1_HASH) return "1";
  if (scope.rule_set_version === "2" && scope.rule_set_hash === SINGLE_CONTRACT_V2_HASH) return "2";
  return null;
}

export type SingleContractCoverage =
  | { kind: "unknown" }
  | {
      kind: "known";
      attemptOrdinal: number;
      version: "1" | "2";
      payment: {
        status:
          | "pending"
          | "failed"
          | "cancelled"
          | "unconfirmed"
          | "not_executed"
          | "insufficient"
          | "observed";
        reason?: string;
        evidence?: EvidenceAnchor[];
      };
    };

function noPaymentResult(
  state: string,
): Extract<SingleContractCoverage, { kind: "known" }>["payment"] {
  if (state === "failed" || state === "interrupted") return { status: "failed" };
  if (state === "cancelled") return { status: "cancelled" };
  if (["accepted", "queued", "started", "running"].includes(state)) return { status: "pending" };
  return { status: "unconfirmed" };
}

function matchesPayment(evaluation: ConstraintEvaluation, version: "1" | "2"): boolean {
  const { rule, domain, capability } = paymentIdentity[version];
  return (
    evaluation.rule_id === rule &&
    evaluation.rule_version === rule &&
    evaluation.domain === domain &&
    evaluation.capability_id === capability
  );
}

/** Only the historical no-result proposal matching this exact evaluation is coverage-only. */
export function isLegacyPaymentCoverageFinding(
  workspace: ReviewWorkspace,
  finding: Finding,
): boolean {
  const coverage = singleContractCoverage(workspace);
  if (
    coverage.kind !== "known" ||
    coverage.version !== "1" ||
    (coverage.payment.status !== "insufficient" && coverage.payment.status !== "not_executed") ||
    finding.origin !== "rule" ||
    finding.domain !== "payment_sum" ||
    finding.relation !== "insufficient" ||
    finding.severity !== null ||
    finding.version !== 1 ||
    finding.decisions.length !== 0 ||
    finding.evidence.some((anchor) => anchor.status === "located")
  )
    return false;
  return (workspace.evaluations ?? []).some(
    (evaluation) =>
      matchesPayment(evaluation, "1") &&
      evaluation.finding_id === finding.id &&
      evaluation.reason === finding.claim &&
      (evaluation.reason.startsWith("未识别到支持的付款比例表达") ||
        evaluation.reason.startsWith("主合同未完成解析，付款比例检查未执行")),
  );
}

function verifiedPaymentEvidence(
  evaluation: ConstraintEvaluation,
  document: ReviewDocument,
): EvidenceAnchor[] {
  if (document.text === null || !evaluation.evidence?.length) return [];
  const index = createTextIndex(document.text);
  return evaluation.evidence.filter(
    (item) =>
      item.status === "located" &&
      item.document_version_id === document.id &&
      item.parsed_document_id === document.parsed_document_id &&
      item.text_offset !== null &&
      typeof item.quote === "string" &&
      Boolean(item.quote.trim()) &&
      sliceByCodePoints(index.text, item.text_offset.start, item.text_offset.end, index) ===
        item.quote,
  );
}

/** Derives coverage only from the selected attempt and the matching release. */
export function singleContractCoverage(workspace: ReviewWorkspace): SingleContractCoverage {
  const attempt = workspace.review_attempts?.attempt;
  const snapshot = workspace.snapshot;
  const scope = snapshot?.review_scope;
  const version = singleContractVersion(scope);
  if (
    !attempt ||
    !attempt.id ||
    attempt.run_id !== workspace.run.id ||
    (attempt.history_kind !== "frozen" && attempt.history_kind !== "preparing") ||
    !version ||
    singleContractVersion(attempt.review_scope) !== version ||
    (snapshot?.rule_set_hash && snapshot.rule_set_hash !== scope?.rule_set_hash)
  ) {
    return { kind: "unknown" };
  }

  const base = { kind: "known" as const, attemptOrdinal: attempt.ordinal, version };
  if (attempt.history_kind !== "frozen") {
    return { ...base, payment: noPaymentResult(attempt.state) };
  }

  const identity = paymentIdentity[version];
  const evaluations = (workspace.evaluations ?? []).filter((evaluation) =>
    matchesPayment(evaluation, version),
  );
  if (evaluations.length === 0) {
    const partialIdentity = (workspace.evaluations ?? []).some(
      (evaluation) =>
        evaluation.rule_id === identity.rule ||
        evaluation.rule_version === identity.rule ||
        evaluation.domain === identity.domain ||
        evaluation.capability_id === identity.capability,
    );
    return {
      ...base,
      payment: partialIdentity ? { status: "unconfirmed" } : noPaymentResult(attempt.state),
    };
  }
  const evaluation = evaluations[0];
  if (evaluations.length !== 1 || !evaluation || evaluation.relation !== "insufficient") {
    return { ...base, payment: { status: "unconfirmed" } };
  }

  const primary = workspace.documents.filter(
    (document) =>
      document.role === "draft_contract" && snapshot?.document_ids.includes(document.id),
  );
  if (primary.length !== 1) return { ...base, payment: { status: "unconfirmed" } };
  const document = primary[0]!;
  if (document.parse_status === "failed" || document.parse_status === "pending") {
    return { ...base, payment: { status: "not_executed", reason: evaluation.reason } };
  }
  if (
    document.parse_status !== "ready" ||
    !document.parsed_document_id ||
    !attempt.parsed_document_ids.includes(document.parsed_document_id)
  ) {
    return { ...base, payment: { status: "unconfirmed" } };
  }
  if (version === "2") {
    if (evaluation.finding_id !== null) return { ...base, payment: { status: "unconfirmed" } };
    if (evaluation.reason_code === "payment_terms_parse_unavailable")
      return { ...base, payment: { status: "not_executed", reason: evaluation.reason } };
    if (evaluation.reason_code === "payment_terms_observed") {
      const evidence = verifiedPaymentEvidence(evaluation, document);
      return evidence.length
        ? { ...base, payment: { status: "observed", reason: evaluation.reason, evidence } }
        : { ...base, payment: { status: "unconfirmed" } };
    }
    if (evaluation.reason_code !== "payment_terms_unrecognized")
      return { ...base, payment: { status: "unconfirmed" } };
  }
  return { ...base, payment: { status: "insufficient", reason: evaluation.reason } };
}
