import type { ReviewWorkspace } from "../../api/types";
import { singleContractCoverage } from "./single-contract-coverage";

/** The selected attempt and the workspace must describe the same execution. */
export function selectedOutcome(workspace: ReviewWorkspace) {
  const attempt = workspace.review_attempts?.attempt;
  if (!attempt || !attempt.id || attempt.run_id !== workspace.run.id || attempt.history_kind !== "frozen") return null;
  return attempt.outcome ?? null;
}

export function limitedPrecheck(workspace: ReviewWorkspace): boolean {
  return workspace.run.state === "partial" &&
    workspace.review_attempts?.attempt.state === "partial" &&
    selectedOutcome(workspace)?.reason === "scope_limited" &&
    singleContractCoverage(workspace).kind === "known";
}

function frozenInputsReady(workspace: ReviewWorkspace): boolean {
  const attempt = workspace.review_attempts?.attempt;
  const documentIds = workspace.snapshot?.document_ids ?? [];
  return Boolean(attempt && documentIds.length > 0 && new Set(documentIds).size === documentIds.length &&
    documentIds.every((id) => {
      const matches = workspace.documents.filter((document) => document.id === id);
      const document = matches[0];
      return matches.length === 1 && document?.parse_status === "ready" && Boolean(document.parsed_document_id && attempt.parsed_document_ids.includes(document.parsed_document_id));
    }));
}

/** These are narrow navigation hints, not authorization or a retry guarantee. */
export function hasTransientRetryCause(workspace: ReviewWorkspace): boolean {
  const attempt = workspace.review_attempts?.attempt;
  const outcome = selectedOutcome(workspace);
  return Boolean(attempt && ["partial", "failed", "interrupted"].includes(attempt.state) &&
    attempt.state === workspace.run.state && frozenInputsReady(workspace) && outcome?.reason === "transient_failure" && outcome.retry_cause === "model_timeout");
}

export function canRestartUserCancelled(workspace: ReviewWorkspace): boolean {
  const attempt = workspace.review_attempts?.attempt;
  return Boolean(attempt?.state === "cancelled" && workspace.run.state === "cancelled" &&
    selectedOutcome(workspace)?.reason === "cancelled_by_user" && frozenInputsReady(workspace));
}

export function hasFrozenParseFailure(workspace: ReviewWorkspace): boolean {
  if (!selectedOutcome(workspace)) return false;
  const ids = workspace.snapshot?.document_ids ?? [];
  return workspace.documents.some((document) => ids.includes(document.id) && document.parse_status === "failed");
}

export function agentStage(workspace: ReviewWorkspace): string {
  const outcome = selectedOutcome(workspace);
  const attempt = workspace.review_attempts?.attempt;
  const agent = workspace.agent_review?.attempt_id === attempt?.id ? workspace.agent_review : null;
  // A partial/failed persisted review must never be hidden by a completed stage event.
  if (agent?.status === "failed" || agent?.status === "rejected" || outcome?.agent_stage === "failed") return "failed";
  if (agent?.status === "partial" || agent?.status === "blocked_live" || outcome?.agent_stage === "partial") return "partial";
  if (agent?.status === "completed" && outcome?.agent_stage === "skipped") return "unknown";
  if (agent && agent.status !== "completed") return "unknown";
  return ["completed", "skipped"].includes(outcome?.agent_stage ?? "") ? outcome!.agent_stage : "unknown";
}

export function unresolvedGapKinds(workspace: ReviewWorkspace) {
  const review = workspace.agent_review;
  if (!review || review.attempt_id !== workspace.review_attempts?.attempt.id) return { materials: false, budget: false, unsupported: false };
  const reasons = new Set((review.gaps ?? []).filter((gap) => gap.status === "open").map((gap) => gap.reason_code));
  return {
    materials: ["required_source_or_field_missing", "required_authority_source_missing", "authorized_counterparty_source_missing", "approval_validity_interval_missing"].some((reason) => reasons.has(reason)),
    budget: reasons.has("max_calls_reached"),
    unsupported: reasons.has("source_expression_not_supported") || reasons.has("capability_not_registered"),
  };
}
