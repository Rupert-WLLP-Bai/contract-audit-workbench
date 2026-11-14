// File metadata (manifest mirror):
// Purpose: Projects selected frozen-review findings and case evidence/remediation workflows into readable navigation items, preserving stable source identities, uncertain states and same-context return paths without creating task authority.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-14
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import type { EvidenceRequest } from "../../api/evidence-workflow";
import type { CaseDetail, ReviewWorkspace } from "../../api/types";
import {
  businessCapabilityLabel,
  businessDomainLabel,
  remediationStatusLabel,
} from "../../domain/business-labels";
import { findingTitle } from "../../domain/finding-presentation";
import { latestDecision, originLabel } from "../../domain/review";
import { agentStage } from "./review-outcome";

export type WorkFilter = "judgment" | "evidence" | "remediation";
export type WorkTargetKind =
  "finding" | "gap" | "evaluation" | "request-source" | "request" | "remediation";
export interface WorkTarget {
  kind: WorkTargetKind;
  id: string;
  runId: string;
  attemptId: string;
}
export interface WorkItem {
  key: string;
  category: WorkFilter;
  title: string;
  origin: string;
  status: string;
  verification: string;
  sourceRunId: string | null;
  sourceAttemptId: string | null;
  target: WorkTarget | null;
  sourceTarget?: WorkTarget | null;
  requestSourceTarget?: WorkTarget | null;
  needsCheck: boolean;
}
export interface WorkProjection {
  items: WorkItem[];
  frozenScope: boolean;
  sourcesKnown: boolean;
  agentSkipped: boolean;
  requestsKnown: boolean;
  caseKnown: boolean;
}

const returnParams = [
  "attempt_id",
  "finding",
  "citation",
  "work_filter",
  "work_kind",
  "work_item",
  "review_section",
] as const;
const reviewSections = new Set(["document", "work", "coverage", "trace", "diff", "history"]);

function hasControlCharacter(value: string): boolean {
  for (const character of value) {
    if (character.charCodeAt(0) <= 0x1f) return true;
  }
  return false;
}

/** Keep only local review paths and opaque navigation identities, never form contents. */
export function reviewReturnPath(path: string, caseId: string): string | null {
  if (!path.startsWith("/") || path.startsWith("//") || path.includes("\\")) return null;
  try {
    const url = new URL(path, "https://review.invalid");
    const parts = url.pathname.split("/");
    if (
      url.origin !== "https://review.invalid" ||
      parts.length !== 5 ||
      parts[1] !== "cases" ||
      decodeURIComponent(parts[2] ?? "") !== caseId ||
      parts[3] !== "reviews" ||
      !decodeURIComponent(parts[4] ?? "")
    )
      return null;
    const params = new URLSearchParams();
    for (const key of returnParams) {
      const value = url.searchParams.get(key);
      if (!value || value.length > 512 || hasControlCharacter(value)) continue;
      if (key === "review_section" && !reviewSections.has(value)) continue;
      params.set(key, value);
    }
    const query = params.toString();
    return `${url.pathname}${query ? `?${query}` : ""}`;
  } catch {
    return null;
  }
}

export function workReturnState(
  path: string,
  caseId: string,
  actorScope: string,
): { workReturn: { path: string; caseId: string; actorScope: string } } | null {
  const safePath = reviewReturnPath(path, caseId);
  return safePath && actorScope ? { workReturn: { path: safePath, caseId, actorScope } } : null;
}

export function readWorkReturn(state: unknown, caseId: string, actorScope: string): string | null {
  if (!state || typeof state !== "object" || !("workReturn" in state)) return null;
  const value = state.workReturn;
  if (
    !value ||
    typeof value !== "object" ||
    !("path" in value) ||
    !("caseId" in value) ||
    !("actorScope" in value) ||
    typeof value.path !== "string" ||
    value.caseId !== caseId ||
    !actorScope ||
    value.actorScope !== actorScope
  )
    return null;
  return reviewReturnPath(value.path, caseId);
}

function counts(values: string[]): Map<string, number> {
  const result = new Map<string, number>();
  for (const value of values) result.set(value, (result.get(value) ?? 0) + 1);
  return result;
}

function sourceKey(
  caseId: string,
  runId: string,
  attemptId: string,
  kind: "gap" | "evaluation",
  id: string,
): string {
  return JSON.stringify([caseId, runId, attemptId, kind, id]);
}

function requestSource(item: EvidenceRequest): { kind: "gap" | "evaluation"; id: string } | null {
  if (item.source_gap) return { kind: "gap", id: item.source_gap.gap_id };
  if (item.source_evaluation) return { kind: "evaluation", id: item.source_evaluation.id };
  return null;
}

function sameObjectRefs(left: string[], right: string[]): boolean {
  if (
    left.length !== right.length ||
    left.some((id) => !id.trim()) ||
    right.some((id) => !id.trim())
  )
    return false;
  const leftIds = new Set(left);
  const rightIds = new Set(right);
  return (
    leftIds.size === left.length &&
    rightIds.size === right.length &&
    left.every((id) => rightIds.has(id))
  );
}

function citationStatus(verification: string, count: number): string {
  if (!count) return "未提供引用，不能确认";
  const label =
    verification === "verified"
      ? "服务端报告已核验"
      : verification === "unverified"
        ? "尚未核验"
        : "核验状态待核对";
  return `${label}；${count} 处引用，需在原文核对定位`;
}

const gapReasons: Record<string, string> = {
  required_source_or_field_missing: "缺少必要材料或字段",
  required_authority_source_missing: "缺少授权依据来源",
  authorized_counterparty_source_missing: "缺少经授权的相对方来源",
  approval_validity_interval_missing: "缺少审批有效期区间",
  source_expression_not_supported: "当前工具不支持来源中的表达形式",
  multiple_source_values_require_review: "多个来源值需要人工核对",
  max_calls_reached: "达到本轮调用预算",
  capability_not_registered: "当前能力尚不支持此项检查",
  claim_requires_semantic_verification: "来源内容的含义仍需核对",
  approval_period_dates_unverified: "审批有效期所需日期尚未核验",
  stable_party_role_linkage_missing: "主体与角色的关联尚未核验",
  source_fact_only_not_a_decision: "仅提取了来源事实，尚无审查结论",
};

/** Only projects server data; it never marks a task complete or grants permission. */
export function projectReviewWork(
  workspace: ReviewWorkspace,
  detail: CaseDetail | undefined,
  requests: EvidenceRequest[] | undefined,
): WorkProjection {
  const caseId = workspace.run.case_id;
  const runId = workspace.run.id;
  const attempt = workspace.review_attempts?.attempt;
  const frozenScope = Boolean(
    caseId && runId && attempt?.id && attempt.run_id === runId && attempt.history_kind === "frozen",
  );
  const attemptId = frozenScope ? attempt!.id : "";
  const agentSkipped = Boolean(
    frozenScope && !workspace.agent_review && agentStage(workspace) === "skipped",
  );
  const sourcesKnown =
    Boolean(
      frozenScope &&
      workspace.evaluations &&
      workspace.agent_review &&
      workspace.agent_review.attempt_id === attemptId &&
      workspace.agent_review.gaps &&
      workspace.agent_review.unresolved_gap_ids,
    ) || Boolean(frozenScope && workspace.evaluations && agentSkipped);
  const validDetail = detail?.id === caseId ? detail : undefined;
  const requestsKnown = requests !== undefined && requests.every((item) => item.case_id === caseId);
  const items: WorkItem[] = [];
  const candidates = new Map<string, WorkItem>();
  const sourceMatches = new Map<string, (request: EvidenceRequest) => boolean>();
  const linkedSources = new Set<string>();
  const conflictingSources = new Set<string>();

  if (frozenScope) {
    const findingCounts = counts(workspace.findings.map((item) => item.id));
    workspace.findings.forEach((finding, index) => {
      if (latestDecision(finding)) return;
      const unique = Boolean(finding.id) && findingCounts.get(finding.id) === 1;
      items.push({
        key: unique
          ? JSON.stringify([caseId, runId, attemptId, "finding", finding.id])
          : `invalid-finding:${index}:${finding.id}`,
        category: "judgment",
        title: findingTitle(finding),
        origin: originLabel(finding.origin),
        status: unique ? "待判断" : "身份待核对",
        verification: citationStatus(finding.verification_status, finding.evidence.length),
        sourceRunId: runId,
        sourceAttemptId: attemptId,
        target: unique ? { kind: "finding", id: finding.id, runId, attemptId } : null,
        needsCheck: !unique,
      });
    });

    const evaluations = workspace.evaluations ?? [];
    const evaluationCounts = counts(evaluations.map((item) => item.id));
    evaluations.forEach((evaluation, index) => {
      if (evaluation.relation !== "insufficient" && evaluation.relation !== "unknown") return;
      const unique =
        Boolean(evaluation.id && evaluation.domain && evaluation.rule_version) &&
        evaluationCounts.get(evaluation.id) === 1;
      const item: WorkItem = {
        key: unique
          ? sourceKey(caseId, runId, attemptId, "evaluation", evaluation.id)
          : `invalid-evaluation:${index}:${evaluation.id}`,
        category: "evidence",
        title: `${businessDomainLabel(evaluation.domain)}：${businessCapabilityLabel(evaluation.capability_id)}`,
        origin: "规则检查",
        status: unique ? "尚无关联请求" : "来源身份待核对",
        verification: `${evaluation.reason || "检查原因待核对"}；检查结果不足或未知`,
        sourceRunId: runId,
        sourceAttemptId: attemptId,
        target: unique ? { kind: "evaluation", id: evaluation.id, runId, attemptId } : null,
        needsCheck: !unique || evaluation.relation === "unknown",
      };
      if (unique) {
        const identity = sourceKey(caseId, runId, attemptId, "evaluation", evaluation.id);
        candidates.set(identity, item);
        sourceMatches.set(identity, (request) =>
          Boolean(
            request.source_evaluation &&
            evaluation.relation &&
            request.source_evaluation.domain === evaluation.domain &&
            request.source_evaluation.rule_version === evaluation.rule_version &&
            request.source_evaluation.rule_id === evaluation.rule_id &&
            request.source_evaluation.capability_id === evaluation.capability_id &&
            request.source_evaluation.relation === evaluation.relation &&
            request.source_evaluation.reason === evaluation.reason &&
            sameObjectRefs(request.source_evaluation.object_refs, evaluation.object_refs),
          ),
        );
      } else items.push(item);
    });

    const agent = workspace.agent_review;
    if (agent?.attempt_id === attemptId) {
      const gaps = agent.gaps ?? [];
      const gapCounts = counts(gaps.map((item) => item.gap_id));
      gaps.forEach((gap, index) => {
        if (
          !["insufficient", "unknown", "unsupported"].includes(gap.status) &&
          !(agent.unresolved_gap_ids ?? []).includes(gap.gap_id)
        )
          return;
        const unique =
          Boolean(gap.gap_id && gap.domain && gap.capability_id) && gapCounts.get(gap.gap_id) === 1;
        const item: WorkItem = {
          key: unique
            ? sourceKey(caseId, runId, attemptId, "gap", gap.gap_id)
            : `invalid-gap:${index}:${gap.gap_id}`,
          category: "evidence",
          title: `${businessDomainLabel(gap.domain)}：${businessCapabilityLabel(gap.capability_id)}`,
          origin: "审查缺口",
          status: unique ? "待核对，尚无关联请求" : "来源身份待核对",
          verification: `冻结缺口状态：${gap.status === "insufficient" ? "证据不足" : gap.status === "unsupported" ? "暂不支持" : "待核对"}；${gapReasons[gap.reason_code] ?? "原因待核对"}；${gap.evidence_ids?.length ? `${gap.evidence_ids.length} 处关联引用` : "未提供引用"}`,
          sourceRunId: runId,
          sourceAttemptId: attemptId,
          target: unique ? { kind: "gap", id: gap.gap_id, runId, attemptId } : null,
          requestSourceTarget:
            unique && ["insufficient", "unknown", "unsupported"].includes(gap.status)
              ? { kind: "request-source", id: `gap:${gap.gap_id}`, runId, attemptId }
              : null,
          needsCheck: !unique || gap.status !== "insufficient",
        };
        if (unique) {
          const identity = sourceKey(caseId, runId, attemptId, "gap", gap.gap_id);
          candidates.set(identity, item);
          sourceMatches.set(identity, (request) =>
            Boolean(
              request.source_gap &&
              gap.status &&
              request.source_gap.domain === gap.domain &&
              request.source_gap.capability_id === gap.capability_id &&
              request.source_gap.status === gap.status &&
              request.source_gap.reason_code === gap.reason_code,
            ),
          );
        } else items.push(item);
      });
      const missing = (agent.unresolved_gap_ids ?? []).filter((id) => !gapCounts.has(id));
      missing.forEach((id, index) =>
        items.push({
          key: `missing-gap:${index}:${id}`,
          category: "evidence",
          title: "未返回详情的审查缺口",
          origin: "审查缺口",
          status: "来源详情待核对",
          verification: "无法确认引用或核验状态",
          sourceRunId: runId,
          sourceAttemptId: attemptId,
          target: null,
          needsCheck: true,
        }),
      );
    }
  }

  const requestCounts = counts((requests ?? []).map((item) => item.id));
  for (const [index, request] of (requests ?? []).entries()) {
    if (request.case_id !== caseId) continue;
    const source = requestSource(request);
    const identity =
      source && request.source_run_id && request.source_attempt_id && source.id
        ? sourceKey(
            caseId,
            request.source_run_id,
            request.source_attempt_id,
            source.kind,
            source.id,
          )
        : null;
    const unique = Boolean(request.id) && requestCounts.get(request.id) === 1;
    const sourceCandidate = identity ? candidates.get(identity) : undefined;
    const sourceMismatch = Boolean(
      sourceCandidate && identity && !sourceMatches.get(identity)?.(request),
    );
    if (sourceMismatch && identity) conflictingSources.add(identity);
    const linked = sourceMismatch ? undefined : sourceCandidate;
    if (unique && linked && identity) linkedSources.add(identity);
    const sourceKnown = Boolean(
      source && request.source_run_id && request.source_attempt_id && source.id,
    );
    const selectedSourceUnmatched = Boolean(
      sourceKnown &&
      request.source_run_id === runId &&
      request.source_attempt_id === attemptId &&
      !linked,
    );
    if (request.status === "verified" && unique && sourceKnown && !selectedSourceUnmatched)
      continue;
    const known = request.status === "open" || request.status === "received";
    items.push({
      key: unique
        ? JSON.stringify([caseId, "request", request.id])
        : `invalid-request:${index}:${request.id}`,
      category: "evidence",
      title: request.question || "补充材料请求",
      origin:
        source?.kind === "gap"
          ? "补件请求 · 审查缺口"
          : source?.kind === "evaluation"
            ? "补件请求 · 规则检查"
            : "补件请求 · 来源待核对",
      status: !unique
        ? "请求身份待核对"
        : request.status === "open"
          ? "待提交"
          : request.status === "received"
            ? "已提交，待核验"
            : "状态待核对",
      verification: request.status === "received" ? "待核验" : "尚未取得核验结果",
      sourceRunId: request.source_run_id || null,
      sourceAttemptId: request.source_attempt_id || null,
      target: unique ? { kind: "request", id: request.id, runId, attemptId } : null,
      sourceTarget:
        linked?.target ??
        (source &&
        source.id &&
        request.source_run_id &&
        request.source_attempt_id &&
        (request.source_run_id !== runId || request.source_attempt_id !== attemptId)
          ? {
              kind: source.kind,
              id: source.id,
              runId: request.source_run_id,
              attemptId: request.source_attempt_id,
            }
          : null),
      needsCheck: !unique || !known || !sourceKnown || sourceMismatch || selectedSourceUnmatched,
    });
  }
  for (const [identity, candidate] of candidates) {
    if (linkedSources.has(identity) && !conflictingSources.has(identity)) continue;
    items.push(
      conflictingSources.has(identity)
        ? { ...candidate, status: "来源与请求快照不一致，待核对", needsCheck: true }
        : !requestsKnown
          ? { ...candidate, status: "请求状态待核对", needsCheck: true }
          : candidate,
    );
  }

  const remediationCounts = counts((validDetail?.remediations ?? []).map((item) => item.id));
  for (const [index, remediation] of (validDetail?.remediations ?? []).entries()) {
    if (remediation.case_id !== caseId) continue;
    const unique = Boolean(remediation.id) && remediationCounts.get(remediation.id) === 1;
    const sourceKnown = Boolean(remediation.run_id && remediation.attempt_id);
    if (remediation.status === "closed" && unique && sourceKnown) continue;
    const known = ["open", "reopened", "submitted"].includes(remediation.status);
    items.push({
      key: unique
        ? JSON.stringify([caseId, "remediation", remediation.id])
        : `invalid-remediation:${index}:${remediation.id}`,
      category: "remediation",
      title: remediation.measure || "整改记录",
      origin: "整改",
      status: unique && known ? remediationStatusLabel(remediation.status) : "状态或身份待核对",
      verification: remediation.status === "submitted" ? "已提交修改稿，等待复核" : "尚未完成复核",
      sourceRunId: remediation.run_id || null,
      sourceAttemptId: remediation.attempt_id || null,
      target: unique
        ? {
            kind: "remediation",
            id: remediation.id,
            runId: remediation.run_id,
            attemptId: remediation.attempt_id,
          }
        : null,
      needsCheck: !unique || !known || !sourceKnown,
    });
  }
  return {
    items,
    frozenScope,
    sourcesKnown,
    agentSkipped,
    requestsKnown,
    caseKnown:
      validDetail !== undefined &&
      validDetail.remediations.every((item) => item.case_id === caseId),
  };
}
