import type { ReviewScopeIdentity } from "../../api/review-readiness";
import type { ReviewWorkspace } from "../../api/types";

// The digest identifies the published, immutable v1 mapping. It is not a
// substitute for the scope identity returned with the selected attempt.
const SINGLE_CONTRACT_V1_HASH = "51676a95611b6e1bf4c30fc50be82e1d493528f34b89f3ece9dbb5aa6c291596";
const PAYMENT_RULE = "single-payment-sum-v1";

export function isSingleContractV1(scope: ReviewScopeIdentity | null | undefined): boolean {
  return scope?.id === "single_contract_precheck" &&
    scope.rule_set_id === "single-contract-precheck" &&
    scope.rule_set_version === "1" &&
    scope.rule_set_hash === SINGLE_CONTRACT_V1_HASH;
}

export type SingleContractCoverage =
  | { kind: "unknown" }
  | {
      kind: "known";
      attemptOrdinal: number;
      payment: {
        status: "pending" | "failed" | "cancelled" | "unconfirmed" | "not_executed" | "insufficient";
        reason?: string;
      };
    };

function noPaymentResult(state: string): Extract<SingleContractCoverage, { kind: "known" }>["payment"] {
  if (state === "failed" || state === "interrupted") return { status: "failed" };
  if (state === "cancelled") return { status: "cancelled" };
  if (["accepted", "queued", "started", "running"].includes(state)) return { status: "pending" };
  return { status: "unconfirmed" };
}

/** Derives coverage only from the selected attempt and its persisted v1 result. */
export function singleContractCoverage(workspace: ReviewWorkspace): SingleContractCoverage {
  const attempt = workspace.review_attempts?.attempt;
  const snapshot = workspace.snapshot;
  const scope = snapshot?.review_scope;
  if (!attempt || !attempt.id || attempt.run_id !== workspace.run.id ||
      (attempt.history_kind !== "frozen" && attempt.history_kind !== "preparing") ||
      !isSingleContractV1(scope) || !isSingleContractV1(attempt.review_scope) ||
      (snapshot?.rule_set_hash && snapshot.rule_set_hash !== scope?.rule_set_hash)) {
    return { kind: "unknown" };
  }

  const base = { kind: "known" as const, attemptOrdinal: attempt.ordinal };
  if (attempt.history_kind !== "frozen") {
    return { ...base, payment: noPaymentResult(attempt.state) };
  }

  const evaluations = (workspace.evaluations ?? []).filter((evaluation) =>
    evaluation.rule_id === PAYMENT_RULE && evaluation.rule_version === PAYMENT_RULE &&
    evaluation.domain === "payment_sum" && evaluation.capability_id === "single_contract.payment_sum",
  );
  if (evaluations.length === 0) {
    const partialIdentity = (workspace.evaluations ?? []).some((evaluation) =>
      evaluation.rule_id === PAYMENT_RULE || evaluation.rule_version === PAYMENT_RULE ||
      evaluation.domain === "payment_sum" || evaluation.capability_id === "single_contract.payment_sum",
    );
    return { ...base, payment: partialIdentity ? { status: "unconfirmed" } : noPaymentResult(attempt.state) };
  }
  const evaluation = evaluations[0];
  if (evaluations.length !== 1 || !evaluation || evaluation.relation !== "insufficient") {
    return { ...base, payment: { status: "unconfirmed" } };
  }

  const primary = workspace.documents.filter((document) =>
    document.role === "draft_contract" && snapshot?.document_ids.includes(document.id),
  );
  if (primary.length !== 1) return { ...base, payment: { status: "unconfirmed" } };
  const document = primary[0]!;
  if (document.parse_status === "failed" || document.parse_status === "pending") {
    return { ...base, payment: { status: "not_executed", reason: evaluation.reason } };
  }
  if (document.parse_status !== "ready" || !document.parsed_document_id ||
      !attempt.parsed_document_ids.includes(document.parsed_document_id)) {
    return { ...base, payment: { status: "unconfirmed" } };
  }
  return { ...base, payment: { status: "insufficient", reason: evaluation.reason } };
}
