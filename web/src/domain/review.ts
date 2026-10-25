import type { CaseSummary, DecisionAction, Finding } from "../api/types";

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

export type CaseSortField =
  | "updated_at"
  | "title"
  | "pending_decisions"
  | "document_count";
export type SortDirection = "asc" | "desc";

export const FINDING_FILTERS = [
  "all",
  "conflict",
  "insufficient",
  "undecided",
] as const;
export type FindingFilter = (typeof FINDING_FILTERS)[number];

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
  disabled: "未启用",
  unreachable: "不可达",
  unknown: "未知",
};

const CONTRACT_LABELS: Record<string, string> = {
  procurement_ict: "采购 / ICT",
  network_engineering: "网络工程",
  service: "服务合同",
  unspecified: "未分类",
};

export function labelOf(
  table: Record<string, string>,
  value: string,
  fallback: string,
): string {
  if (value === "") return fallback;
  return table[value] ?? `${fallback}（${value}）`;
}

export function runLabel(state: string): string {
  return labelOf(RUN_LABELS, state, "状态未识别");
}
export function isActiveAttemptState(state: string): boolean {
  return (
    state === "accepted" ||
    state === "queued" ||
    state === "started" ||
    state === "running"
  );
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
  return labelOf(ROLE_LABELS, role, "其他材料");
}

export function originLabel(origin: string): string {
  return labelOf(ORIGIN_LABELS, origin, "其他来源");
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

export function contractTypeLabel(value: string): string {
  return labelOf(CONTRACT_LABELS, value, "合同类型");
}

export function decisionLabel(action: string): string {
  const known = DECISION_ACTIONS.find((item) => item.action === action);
  if (known) return known.label;
  if (action === "") return "决定未识别";
  return `决定未识别（${action}）`;
}

export type StatusTone = "neutral" | "attention" | "good" | "bad" | "progress";

export function runTone(state: string): StatusTone {
  if (state === "awaiting_human" || state === "partial") return "attention";
  if (state === "completed") return "good";
  if (state === "failed" || state === "cancelled" || state === "interrupted")
    return "bad";
  if (isActiveAttemptState(state)) return "progress";
  return "neutral";
}

export function parseTone(status: string): StatusTone {
  if (status === "ready") return "good";
  if (status === "failed") return "bad";
  if (status === "parsing" || status === "pending") return "progress";
  return "neutral";
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
      const haystack = `${item.title} ${item.id}`.toLowerCase();
      if (!haystack.includes(needle)) return false;
    }
    if (businessStatus !== "all" && item.business_status !== businessStatus)
      return false;
    const state = item.latest_run?.state ?? null;
    if (run === "all") return true;
    if (run === "needs_human")
      return state === "awaiting_human" || item.pending_decisions > 0;
    if (run === "active")
      return (
        state === "queued" ||
        state === "running" ||
        state === "accepted" ||
        state === "started"
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

export function filterFindings(
  findings: Finding[],
  filter: FindingFilter,
): Finding[] {
  if (filter === "all") return findings;
  if (filter === "conflict")
    return findings.filter((finding) => finding.relation === "conflict");
  if (filter === "insufficient")
    return findings.filter((finding) => finding.relation === "insufficient");
  return findings.filter((finding) => latestDecision(finding) === null);
}

export function reviewLayout(viewportWidth: number): ReviewLayout {
  return viewportWidth >= 1440 ? "split" : "switch";
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
  if (
    typeof record.seq !== "number" ||
    !Number.isInteger(record.seq) ||
    record.seq < 0
  )
    return null;
  const type =
    typeof record.event_type === "string" ? record.event_type : record.type;
  if (typeof type !== "string" || type === "") return null;
  const state =
    typeof record.status === "string"
      ? record.status
      : typeof record.state === "string"
        ? record.state
        : null;
  return { seq: record.seq, type, state };
}

export function formatDateTime(value: string): string {
  if (value === "") return "时间未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(date);
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

export function selectedFindingId(
  findings: Finding[],
  requested: string | null,
): string | null {
  if (requested && findings.some((finding) => finding.id === requested))
    return requested;
  const undecided = findings.find(
    (finding) => latestDecision(finding) === null,
  );
  return undecided?.id ?? findings[0]?.id ?? null;
}
