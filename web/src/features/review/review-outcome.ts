// File metadata (manifest mirror):
// Purpose: Derives selected frozen-review coverage, agent-stage, unresolved-gap and transient-retry presentation hints while preserving server authority.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-20
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import type { ReviewWorkspace } from "../../api/types";
import { businessCapabilityLabel } from "../../domain/business-labels";
import { runLabel, runTone } from "../../domain/review";
import { isLegacyPaymentCoverageFinding, singleContractCoverage } from "./single-contract-coverage";

/** The selected attempt and the workspace must describe the same execution. */
export function selectedOutcome(workspace: ReviewWorkspace) {
  const attempt = workspace.review_attempts?.attempt;
  if (!attempt || !attempt.id || attempt.run_id !== workspace.run.id) return null;
  if (
    attempt.history_kind === "preparing" &&
    attempt.state === "cancelled" &&
    workspace.run.state === "cancelled" &&
    ["cancelled_by_user", "cancelled", "authorization_revoked"].includes(
      attempt.outcome?.reason ?? "",
    )
  )
    return attempt.outcome ?? null;
  if (attempt.history_kind !== "frozen") return null;
  return attempt.outcome ?? null;
}

export function limitedPrecheck(workspace: ReviewWorkspace): boolean {
  return (
    workspace.run.state === "partial" &&
    workspace.review_attempts?.attempt.state === "partial" &&
    selectedOutcome(workspace)?.reason === "scope_limited" &&
    singleContractCoverage(workspace).kind === "known"
  );
}

function frozenInputsReady(workspace: ReviewWorkspace): boolean {
  const attempt = workspace.review_attempts?.attempt;
  const documentIds = workspace.snapshot?.document_ids ?? [];
  return Boolean(
    attempt &&
    documentIds.length > 0 &&
    new Set(documentIds).size === documentIds.length &&
    documentIds.every((id) => {
      const matches = workspace.documents.filter((document) => document.id === id);
      const document = matches[0];
      return (
        matches.length === 1 &&
        document?.parse_status === "ready" &&
        Boolean(
          document.parsed_document_id &&
          attempt.parsed_document_ids.includes(document.parsed_document_id),
        )
      );
    }),
  );
}

/** These are narrow navigation hints, not authorization or a retry guarantee. */
export function hasTransientRetryCause(workspace: ReviewWorkspace): boolean {
  const attempt = workspace.review_attempts?.attempt;
  const outcome = selectedOutcome(workspace);
  return Boolean(
    attempt &&
    ["partial", "failed", "interrupted"].includes(attempt.state) &&
    attempt.state === workspace.run.state &&
    frozenInputsReady(workspace) &&
    outcome?.reason === "transient_failure" &&
    outcome.retry_cause === "model_timeout",
  );
}

export function canRestartUserCancelled(workspace: ReviewWorkspace): boolean {
  const attempt = workspace.review_attempts?.attempt;
  return Boolean(
    attempt?.history_kind === "frozen" &&
    attempt.state === "cancelled" &&
    workspace.run.state === "cancelled" &&
    selectedOutcome(workspace)?.reason === "cancelled_by_user" &&
    frozenInputsReady(workspace),
  );
}

/** A preparation has no frozen input; a new admission must name its source. */
export function canReadmitCancelledPreparation(workspace: ReviewWorkspace): boolean {
  const execution = workspace.review_attempts;
  const attempt = execution?.attempt;
  return Boolean(
    attempt &&
    attempt.history_kind === "preparing" &&
    attempt.state === "cancelled" &&
    workspace.run.state === "cancelled" &&
    selectedOutcome(workspace)?.reason === "cancelled_by_user" &&
    execution?.attempts.filter(
      (entry) => entry.id === attempt.id && entry.run_id === workspace.run.id,
    ).length === 1 &&
    !execution?.attempts.some((entry) => entry.ordinal > attempt.ordinal),
  );
}

export function hasFrozenParseFailure(workspace: ReviewWorkspace): boolean {
  if (workspace.review_attempts?.attempt.history_kind !== "frozen" || !selectedOutcome(workspace))
    return false;
  const ids = workspace.snapshot?.document_ids ?? [];
  return workspace.documents.some(
    (document) => ids.includes(document.id) && document.parse_status === "failed",
  );
}

export function agentStage(workspace: ReviewWorkspace): string {
  const outcome = selectedOutcome(workspace);
  const attempt = workspace.review_attempts?.attempt;
  const agent = workspace.agent_review?.attempt_id === attempt?.id ? workspace.agent_review : null;
  // A partial/failed persisted review must never be hidden by a completed stage event.
  if (
    agent?.status === "failed" ||
    agent?.status === "rejected" ||
    outcome?.agent_stage === "failed"
  )
    return "failed";
  if (
    agent?.status === "partial" ||
    agent?.status === "blocked_live" ||
    outcome?.agent_stage === "partial"
  )
    return "partial";
  if (agent?.status === "completed" && outcome?.agent_stage === "skipped") return "unknown";
  if (agent && agent.status !== "completed") return "unknown";
  return ["completed", "skipped"].includes(outcome?.agent_stage ?? "")
    ? outcome!.agent_stage
    : "unknown";
}

export function unresolvedGapKinds(workspace: ReviewWorkspace) {
  const review = workspace.agent_review;
  if (!review || review.attempt_id !== workspace.review_attempts?.attempt.id)
    return { materials: false, budget: false, unsupported: false };
  const reasons = new Set(
    (review.gaps ?? []).filter((gap) => gap.status === "open").map((gap) => gap.reason_code),
  );
  return {
    materials: [
      "required_source_or_field_missing",
      "required_authority_source_missing",
      "authorized_counterparty_source_missing",
      "approval_validity_interval_missing",
    ].some((reason) => reasons.has(reason)),
    budget: reasons.has("max_calls_reached"),
    unsupported:
      reasons.has("source_expression_not_supported") || reasons.has("capability_not_registered"),
  };
}

/** A partial report describes the exact attempt, even after a later attempt completes. */
export function partialReportScope(workspace: ReviewWorkspace) {
  const attempt = workspace.review_attempts?.attempt;
  if (
    !attempt ||
    attempt.run_id !== workspace.run.id ||
    attempt.history_kind !== "frozen" ||
    attempt.state !== "partial"
  )
    return null;
  const outcome = selectedOutcome(workspace);
  const precheck =
    outcome?.reason === "scope_limited" || attempt.review_scope?.id === "single_contract_precheck";
  const stage = agentStage(workspace);
  const findings = workspace.findings.filter(
    (finding) => !isLegacyPaymentCoverageFinding(workspace, finding),
  );
  const ruleFindings = findings.filter((finding) => finding.origin === "rule").length;
  const modelFindings = findings.filter(
    (finding) => finding.origin === "model" || finding.origin === "ai",
  ).length;
  const ruleEvaluationSaved = (workspace.evaluations?.length ?? 0) > 0;
  const executionLabel =
    stage === "completed"
      ? "审查未全部完成"
      : stage === "failed" ||
          stage === "partial" ||
          stage === "skipped" ||
          outcome?.reason === "execution_failed"
        ? "智能审查未完成"
        : "智能审查完成情况待确认";

  // The business workspace projects partial_reason through the public
  // failure mapping for its selected attempt. It carries no diagnostic code.
  const publicReason =
    workspace.run.state === "partial" ? workspace.run.partial_reason?.trim() : "";
  const reason = precheck
    ? "审查范围选择了单合同预审，完整审查需另行发起。"
    : outcome?.retry_cause === "model_timeout"
      ? "模型调用超时。"
      : publicReason ||
        (outcome?.reason === "dependency_unavailable"
          ? "审查依赖暂不可用。"
          : outcome?.reason === "execution_failed"
            ? "本次审查执行失败，具体原因未记录。"
            : "本次未记录可用于确定原因的审查信息。");
  const gaps = unresolvedGapKinds(workspace);
  const openGaps =
    workspace.agent_review?.attempt_id === attempt.id
      ? (workspace.agent_review.gaps ?? []).filter((gap) => gap.status === "open")
      : [];
  const namedGaps = [...new Set(openGaps.map((gap) => businessCapabilityLabel(gap.capability_id)))];
  return {
    precheck,
    ruleFindings,
    modelFindings,
    reason,
    decidedCount: findings.filter((finding) => finding.decisions.length > 0).length,
    findingCount: findings.length,
    executionLabel,
    ruleCoverage: ruleEvaluationSaved ? "已保存规则检查结果" : "规则检查完成情况待确认",
    uncovered: precheck
      ? ["跨材料对照、制度核对及预审范围外的风险项"]
      : [
          ...namedGaps,
          ...(gaps.materials ? ["缺少材料或必要字段的检查"] : []),
          ...(gaps.budget ? ["未完成的智能审查调用"] : []),
          ...(gaps.unsupported ? ["当前审查能力不支持的检查"] : []),
          "本次审查未完成或未能核实的项目",
        ],
    emptyMessage: precheck
      ? "本次单合同预审未产生风险线索。其他检查不在预审范围内。"
      : `${ruleEvaluationSaved ? "规则检查未发现线索" : "当前没有已保存的规则线索"}；${executionLabel}，不能据此认定没有风险。`,
  };
}

/** Business status for the same attempt already authorized by the workspace read. */
export function businessReviewProgress(workspace: ReviewWorkspace) {
  const attempt = workspace.review_attempts?.attempt;
  if (!attempt?.id || attempt.run_id !== workspace.run.id) {
    return {
      tone: "attention" as const,
      label: "审查状态待核对",
      detail: "这条记录缺少可核对的审查尝试，无法确认本次进度。",
      attemptId: null,
    };
  }
  if (!["frozen", "preparing"].includes(attempt.history_kind)) {
    return {
      tone: "attention" as const,
      label: "审查状态待核对",
      detail: "这次历史审查缺少可核对的记录，无法确认执行范围。",
      attemptId: attempt.id,
    };
  }
  const outcome = selectedOutcome(workspace);
  const stage = agentStage(workspace);
  const projection =
    workspace.agent_review?.attempt_id === attempt.id ? workspace.agent_review : null;
  const partial = partialReportScope(workspace);
  const modelError = [
    "model_request_failed",
    "model_not_configured",
    "model_evidence_rejected",
    "model_output_unusable",
  ].includes(attempt.error_code ?? "");
  // The public attempt projection derives this message from that attempt's error code.
  const modelFailed =
    modelError ||
    (outcome?.reason === "execution_failed" &&
      [
        "调用审查模型失败，",
        "审查模型未配置，",
        "模型输出无法解析成质保结论，",
        "模型输出不能对应到材料原文，",
      ].some((prefix) => workspace.run.partial_reason?.startsWith(prefix)));
  let label = runLabel(attempt.state);
  let detail = "按所选审查记录显示。";
  if (["accepted", "queued", "started", "running"].includes(attempt.state)) {
    label = attempt.history_kind === "preparing" ? "准备审查中" : "审查中";
    detail =
      attempt.history_kind === "preparing"
        ? "正在准备本次审查所需的材料与依据。"
        : "本次审查仍在进行，已保存的原文和结果可继续查阅。";
  } else if (
    partial?.precheck &&
    outcome?.reason === "scope_limited" &&
    !modelFailed &&
    stage !== "failed"
  ) {
    label = "单合同预审已结束";
    detail = "本次仅执行单合同预审。跨材料对照、制度核对及范围外风险项未执行。";
  } else if ((attempt.state === "partial" || attempt.state === "failed") && modelFailed) {
    label = "模型审查未完成";
    detail =
      (attempt.state === workspace.run.state && workspace.run.partial_reason?.trim()) ||
      "本次未完成模型审查。";
  } else if (
    ["partial", "failed"].includes(attempt.state) &&
    (projection?.plan?.status === "not_planned" || projection?.plan?.status === "guard_rejected")
  ) {
    label = "智能审查计划未就绪";
    detail =
      projection?.plan?.status === "guard_rejected"
        ? "本次审查计划未通过检查。"
        : "本次未生成可执行的审查计划。";
  } else if (attempt.state === "partial") {
    label = partial?.precheck ? "单合同预审未完成" : "智能审查未完";
    detail =
      (partial?.precheck
        ? stage === "failed"
          ? "本次单合同预审的智能审查执行失败。跨材料对照、制度核对及范围外风险项未执行。"
          : "本次单合同预审未完成。跨材料对照、制度核对及范围外风险项未执行。"
        : partial?.reason) ??
      (stage === "failed"
        ? "智能审查未完成，具体失败阶段未记录。"
        : "尚无法确认本次未完成的具体阶段。");
  } else if (attempt.state === "completed") {
    label = "本次审查已完成";
    detail = "本次所选范围已完成。请查看已保存结果及待判断事项。";
  }
  return {
    tone: attempt.state === "partial" ? ("attention" as const) : runTone(attempt.state),
    label,
    detail,
    attemptId: attempt.id,
  };
}
