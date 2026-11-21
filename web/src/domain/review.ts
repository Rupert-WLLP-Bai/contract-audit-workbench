// File metadata (manifest mirror):
// Purpose: Projects review states, decision and workspace links and case stage actions from available run, material, report and permission facts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r5
// Copyright: See LICENSE
// End file metadata.

import type {
  CaseSummary,
  ConstraintEvaluation,
  DecisionAction,
  DecisionInput,
  Finding,
} from "../api/types";
import { caseSearchText } from "./case-identity";
export { contractTypeLabel } from "./case-identity";
export { formatDateTime } from "./time";

export const DECISION_ACTIONS: readonly {
  action: DecisionAction;
  label: string;
}[] = [
  { action: "confirm_risk", label: "确认风险" },
  { action: "false_positive", label: "误报" },
  { action: "insufficient_evidence", label: "证据不足" },
  { action: "accept_residual", label: "接受残余风险" },
];

export const RUN_FILTERS = [
  "all",
  "needs_human",
  "active",
  "partial",
  "completed",
  "failed",
  "cancelled",
  "interrupted",
] as const;
export type RunFilter = (typeof RUN_FILTERS)[number];

export type CaseSortField = "updated_at" | "title" | "pending_decisions" | "document_count";
export type SortDirection = "asc" | "desc";

export const FINDING_FILTERS = ["all", "conflict", "insufficient", "undecided"] as const;
export type FindingFilter = (typeof FINDING_FILTERS)[number];

export type FindingDecisionInput = DecisionInput & { findingId: string };
export type FindingDecisionOutcome = {
  status: "success" | "conflict" | "failed";
  message: string;
  stop?: boolean;
};

export type ReviewLayout = "split" | "switch";

export interface RunEvent {
  seq: number;
  type: string;
  state: string | null;
}

const RUN_LABELS: Record<string, string> = {
  accepted: "已受理",
  queued: "排队中",
  started: "已启动",
  running: "审查中",
  awaiting_human: "待人工",
  partial: "部分完成",
  completed: "执行完成",
  failed: "失败",
  cancelled: "已取消",
  interrupted: "已中断",
};

const PARSE_LABELS: Record<string, string> = {
  pending: "待解析",
  parsing: "解析中",
  ready: "已解析",
  failed: "解析失败",
};

const ROLE_LABELS: Record<string, string> = {
  procurement: "采购文件",
  award: "中标文件",
  draft_contract: "拟签合同",
  attachment: "技术附件",
};

const ORIGIN_LABELS: Record<string, string> = {
  rule: "规则",
  ai: "模型建议",
  model: "模型建议",
  manual: "人工",
};

const RELATION_LABELS: Record<string, string> = {
  conflict: "冲突",
  insufficient: "信息不足",
  compatible: "一致",
};

const CONFIDENCE_LABELS: Record<string, string> = {
  low: "置信低",
  medium: "置信中",
  high: "置信高",
};

const ROLE_NAME: Record<string, string> = {
  admin: "管理员",
  reviewer: "审查员",
  clerk: "经办",
  viewer: "只读",
};

const MODEL_STATUS: Record<string, string> = {
  connected: "已连通",
  configured: "已配置",
  unconfigured: "未配置",
  disabled: "未启用",
  unreachable: "不可达",
  unknown: "未知",
};

const BUSINESS_STATUS_LABELS: Record<string, string> = {
  active: "进行中",
  draft: "草稿",
  pending: "待处理",
  suspended: "已暂停",
  closed: "已关闭",
  archived: "已归档",
};

const BUSINESS_STATUS_TONES: Record<string, StatusTone> = {
  active: "progress",
  pending: "attention",
  suspended: "attention",
  closed: "good",
};

const PARSE_TONES: Record<string, StatusTone> = {
  ready: "good",
  failed: "bad",
  parsing: "progress",
  pending: "progress",
};

const TASK_KIND_LABELS: Record<string, string> = {
  decision: "审查判断",
  review: "审查复核",
  remediation: "整改",
  report: "报告",
  general: "一般任务",
};

export function labelOf(table: Record<string, string>, value: string, fallback: string): string {
  if (value === "") return fallback;
  const label = Object.hasOwn(table, value) ? table[value] : undefined;
  return label ?? fallback;
}

export function runLabel(state: string): string {
  return labelOf(RUN_LABELS, state, "状态未识别");
}
export function isActiveAttemptState(state: string): boolean {
  return state === "accepted" || state === "queued" || state === "started" || state === "running";
}
export function isTerminalAttemptState(state: string): boolean {
  return (
    state === "awaiting_human" ||
    state === "partial" ||
    state === "completed" ||
    state === "failed" ||
    state === "cancelled" ||
    state === "interrupted"
  );
}

export function parseLabel(status: string): string {
  return labelOf(PARSE_LABELS, status, "解析状态未识别");
}

export function roleLabel(role: string): string {
  return labelOf(ROLE_LABELS, role, "材料角色待核对");
}

export function originLabel(origin: string): string {
  return labelOf(ORIGIN_LABELS, origin, "来源待核对");
}

export function relationLabel(relation: string): string {
  return labelOf(RELATION_LABELS, relation, "关系未识别");
}

export function confidenceLabel(confidence: string | null): string | null {
  if (confidence === null || confidence === "") return null;
  return labelOf(CONFIDENCE_LABELS, confidence, "置信未识别");
}

export function actorRoleLabel(role: string): string {
  return labelOf(ROLE_NAME, role, "角色未识别");
}

export function modelStatusLabel(status: string): string {
  return labelOf(MODEL_STATUS, status, "连通状态未识别");
}

export function businessStatusLabel(value: string): string {
  if (value === "") return "业务状态未提供";
  return Object.hasOwn(BUSINESS_STATUS_LABELS, value)
    ? BUSINESS_STATUS_LABELS[value]!
    : "业务状态待核对";
}

export function businessStatusTone(value: string): StatusTone {
  return Object.hasOwn(BUSINESS_STATUS_TONES, value)
    ? (BUSINESS_STATUS_TONES[value] ?? "neutral")
    : "neutral";
}

export function taskKindLabel(value: string): string {
  if (value === "") return "任务";
  return Object.hasOwn(TASK_KIND_LABELS, value) ? TASK_KIND_LABELS[value]! : "任务类型待核对";
}

export function decisionLabel(action: string): string {
  const known = DECISION_ACTIONS.find((item) => item.action === action);
  if (known) return known.label;
  return "决定未识别";
}

export type StatusTone = "neutral" | "attention" | "good" | "bad" | "progress";

export function runTone(state: string): StatusTone {
  if (state === "awaiting_human" || state === "partial") return "attention";
  if (state === "completed") return "good";
  if (state === "failed" || state === "cancelled" || state === "interrupted") return "bad";
  if (isActiveAttemptState(state)) return "progress";
  return "neutral";
}

export function parseTone(status: string): StatusTone {
  return Object.hasOwn(PARSE_TONES, status) ? (PARSE_TONES[status] ?? "neutral") : "neutral";
}

export function parseRunFilter(value: string | null): RunFilter {
  if (value !== null && (RUN_FILTERS as readonly string[]).includes(value))
    return value as RunFilter;
  return "all";
}

export function filterCases(
  items: CaseSummary[],
  query: string,
  run: RunFilter,
  businessStatus = "all",
): CaseSummary[] {
  const needle = query.trim().toLowerCase();
  return items.filter((item) => {
    if (needle !== "") {
      const haystack = caseSearchText(item);
      if (!haystack.includes(needle)) return false;
    }
    if (businessStatus !== "all" && item.business_status !== businessStatus) return false;
    const state = item.latest_run?.state ?? null;
    if (run === "all") return true;
    if (run === "needs_human") return state === "awaiting_human" || item.pending_decisions > 0;
    if (run === "active")
      return (
        state === "queued" || state === "running" || state === "accepted" || state === "started"
      );
    if (run === "partial") return state === "partial";
    if (run === "completed") return state === "completed";
    if (run === "cancelled") return state === "cancelled";
    if (run === "interrupted") return state === "interrupted";
    return state === "failed";
  });
}

export function latestDecision(finding: Finding) {
  if (finding.decisions.length === 0) return null;
  return finding.decisions[finding.decisions.length - 1] ?? null;
}

export function filterFindings(findings: Finding[], filter: FindingFilter): Finding[] {
  if (filter === "all") return findings;
  if (filter === "conflict") return findings.filter((finding) => finding.relation === "conflict");
  if (filter === "insufficient")
    return findings.filter((finding) => finding.relation === "insufficient");
  return findings.filter((finding) => latestDecision(finding) === null);
}

const FINDING_SEVERITY_ORDER: Record<string, number> = {
  high: 0,
  medium: 1,
  low: 2,
  info: 3,
};

/** Preserve the source order when state and severity are equal. */
export function sortFindings(findings: readonly Finding[]): Finding[] {
  return findings
    .map((finding, index) => ({ finding, index }))
    .sort((left, right) => {
      const decided =
        Number(latestDecision(left.finding) !== null) -
        Number(latestDecision(right.finding) !== null);
      if (decided !== 0) return decided;
      const severity =
        (FINDING_SEVERITY_ORDER[left.finding.severity ?? ""] ?? 4) -
        (FINDING_SEVERITY_ORDER[right.finding.severity ?? ""] ?? 4);
      return severity || left.index - right.index;
    })
    .map(({ finding }) => finding);
}

export function findingQueue(findings: Finding[], filter: FindingFilter): Finding[] {
  return filterFindings(sortFindings(findings), filter);
}

export function adjacentFindingId(
  queue: Finding[],
  selectedId: string | null,
  direction: -1 | 1,
): string | null {
  if (queue.length === 0) return null;
  const index = queue.findIndex((finding) => finding.id === selectedId);
  if (index === -1) return queue[direction === 1 ? 0 : queue.length - 1]?.id ?? null;
  return queue[index + direction]?.id ?? null;
}

export function nextUndecidedFindingId(queue: Finding[], selectedId: string): string | null {
  const index = queue.findIndex((finding) => finding.id === selectedId);
  const start = index < 0 ? 0 : index + 1;
  const candidates = [...queue.slice(start), ...queue.slice(0, Math.max(index, 0))];
  return (
    candidates.find((finding) => finding.id !== selectedId && latestDecision(finding) === null)
      ?.id ?? null
  );
}

export type FindingBatchGroup = { ruleId: string; ruleVersion: string; relation: string };

/** Only a unique, linked rule evaluation proves that findings share a rule. */
export function findingBatchGroup(
  finding: Finding,
  evaluations: readonly ConstraintEvaluation[],
): FindingBatchGroup | null {
  if (finding.origin !== "rule" || !finding.relation || finding.relation === "unknown") return null;
  const linked = evaluations.filter((evaluation) => evaluation.finding_id === finding.id);
  if (linked.length !== 1) return null;
  const evaluation = linked[0];
  if (
    !evaluation ||
    !evaluation.rule_id.trim() ||
    evaluation.domain !== finding.domain ||
    evaluation.relation !== finding.relation
  )
    return null;
  return {
    ruleId: evaluation.rule_id,
    ruleVersion: evaluation.rule_version,
    relation: finding.relation,
  };
}

export function sameFindingBatchGroup(
  left: FindingBatchGroup | null,
  right: FindingBatchGroup | null,
): boolean {
  return (
    left !== null &&
    right !== null &&
    left.ruleId === right.ruleId &&
    left.ruleVersion === right.ruleVersion &&
    left.relation === right.relation
  );
}

export function reviewLayout(viewportWidth: number): ReviewLayout {
  return viewportWidth >= 1100 ? "split" : "switch";
}

export function validateDecisionReason(reason: string): string | null {
  const trimmed = reason.trim();
  if (trimmed.length === 0) return "请填写理由";
  if (trimmed.length < 2) return "理由至少两个字";
  if (trimmed.length > 1000) return "理由不能超过 1000 字";
  return null;
}

export function validateCaseTitle(title: string): string | null {
  const trimmed = title.trim();
  if (trimmed.length === 0) return "请填写案件标题";
  if (trimmed.length > 120) return "标题不能超过 120 个字";
  return null;
}

export function parseRunEvent(data: string): RunEvent | null {
  let value: unknown;
  try {
    value = JSON.parse(data);
  } catch {
    return null;
  }
  if (typeof value !== "object" || value === null) return null;
  const record = value as Record<string, unknown>;
  if (typeof record.seq !== "number" || !Number.isInteger(record.seq) || record.seq < 0)
    return null;
  const type = typeof record.event_type === "string" ? record.event_type : record.type;
  if (typeof type !== "string" || type === "") return null;
  const state =
    typeof record.status === "string"
      ? record.status
      : typeof record.state === "string"
        ? record.state
        : null;
  return { seq: record.seq, type, state };
}

export function formatBytes(size: number): string {
  if (!Number.isFinite(size) || size < 0) return "大小未知";
  if (size < 1024) return `${Math.round(size)} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

export function shortDigest(digest: string): string {
  if (digest.length < 8) return digest === "" ? "摘要缺失" : digest;
  return digest.slice(0, 8);
}

export function formatWait(ms: number | null): string {
  if (ms === null || !Number.isFinite(ms) || ms < 0) return "未报告";
  if (ms < 1000) return `${Math.round(ms)} 毫秒`;
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds} 秒`;
  return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
}

export function readyDocumentCount(statuses: readonly string[]): number {
  return statuses.filter((status) => status === "ready").length;
}

export function selectedFindingId(findings: Finding[], requested: string | null): string | null {
  if (requested && findings.some((finding) => finding.id === requested)) return requested;
  const ordered = sortFindings(findings);
  return ordered[0]?.id ?? null;
}

/** In-app review route; encoding lives here so every link agrees. */
export function caseHref(
  caseId: string,
  runId: string | null = null,
  attemptId: string | null = null,
): string {
  const base = `/cases/${encodeURIComponent(caseId)}`;
  if (runId === null) return base;
  const review = `${base}/reviews/${encodeURIComponent(runId)}`;
  return attemptId === null ? review : `${review}?attempt_id=${encodeURIComponent(attemptId)}`;
}

/** Explicit facts only: callers retain responsibility for authorization and navigation. */
export interface CaseStageFacts {
  archived: boolean | null;
  documentCount: number | null;
  parsing: "ready" | "pending" | "failed" | "unknown";
  runKnown: boolean;
  runState: string | null;
  pendingDecisions: number | null;
  pendingRemediation: boolean | null;
  reportAvailable: boolean | null;
  canUpload: boolean;
  canStartReview: boolean;
  startEligible: boolean;
  preparationAvailable: boolean;
  retryEligible: boolean;
  canRetry: boolean;
  canReview: boolean;
  canRemediate: boolean;
  canCreateReport: boolean;
  reportEligible: boolean;
  canReadReport: boolean;
}
export type CaseStageAction =
  | "upload"
  | "materials"
  | "start"
  | "progress"
  | "judge"
  | "prepare"
  | "retry"
  | "remediation"
  | "report"
  | "view-report"
  | "view";
export interface CaseStagePresentation {
  label: string;
  action: CaseStageAction;
  actionLabel: string;
  aiIncomplete: boolean;
}

export function caseStagePresentation(facts: CaseStageFacts): CaseStagePresentation {
  const result = (
    label: string,
    action: CaseStageAction,
    actionLabel: string,
    aiIncomplete = false,
  ): CaseStagePresentation => ({ label, action, actionLabel, aiIncomplete });
  if (facts.archived === null) return result("案件状态待确认", "view", "查看");
  if (facts.archived)
    return facts.reportAvailable === true && facts.canReadReport
      ? result("已归档", "view-report", "查看报告")
      : result("已归档", "view", "查看");
  if (
    facts.documentCount === null ||
    !Number.isInteger(facts.documentCount) ||
    facts.documentCount < 0
  )
    return result("材料状态待确认", "view", "查看");
  if (facts.documentCount === 0)
    return facts.canUpload
      ? result("待传材料", "upload", "上传材料")
      : result("待传材料", "materials", "查看材料");
  if (facts.parsing === "failed") return result("材料解析失败", "materials", "查看材料");
  if (facts.parsing === "pending") return result("解析中", "materials", "查看材料");
  if (facts.parsing === "unknown") return result("材料状态待确认", "materials", "查看材料");
  if (!facts.runKnown) return result("审查状态待确认", "view", "查看");
  if (facts.runState === null)
    return facts.canStartReview && facts.preparationAvailable && facts.startEligible
      ? result("待开始审查", "start", "开始审查")
      : result("待开始审查", "view", "查看");
  if (["accepted", "queued", "started", "running"].includes(facts.runState))
    return result("审查中", "progress", "查看进度");
  if (!["partial", "failed", "interrupted", "completed", "awaiting_human"].includes(facts.runState))
    return result("审查状态待确认", "view", "查看");
  const incomplete = ["partial", "failed", "interrupted"].includes(facts.runState);
  if (
    facts.pendingDecisions === null ||
    !Number.isInteger(facts.pendingDecisions) ||
    facts.pendingDecisions < 0
  )
    return result("判断状态待确认", "view", "查看", incomplete);
  if (facts.pendingDecisions > 0)
    return facts.canReview
      ? result(
          `待判断 ${facts.pendingDecisions} 条`,
          "judge",
          `去判断 (${facts.pendingDecisions})`,
          incomplete,
        )
      : result(`待判断 ${facts.pendingDecisions} 条`, "view", "查看", incomplete);
  if (incomplete) {
    if (facts.retryEligible && facts.canRetry)
      return result("智能审查未完成", "retry", "重新审查", true);
    if (facts.preparationAvailable && facts.canStartReview)
      return result("智能审查未完成", "prepare", "重新审查", true);
    return result("智能审查未完成", "view", "查看原因", true);
  }
  if (facts.runState === "awaiting_human") return result("判断状态待确认", "view", "查看");
  if (facts.runState !== "completed") return result("审查状态待确认", "view", "查看");
  if (facts.pendingRemediation === null) return result("整改状态待确认", "view", "查看");
  if (facts.pendingRemediation)
    return facts.canRemediate
      ? result("待整改", "remediation", "去整改")
      : result("待整改", "view", "查看");
  if (facts.reportAvailable === null) return result("报告状态待确认", "view", "查看");
  if (facts.reportAvailable)
    return facts.canReadReport
      ? result("已完成", "view-report", "查看报告")
      : result("已完成", "view", "查看");
  return facts.canCreateReport && facts.reportEligible
    ? result("可出报告", "report", "出报告")
    : result("审查已完成", "view", "查看");
}
