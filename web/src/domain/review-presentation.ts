import type {
  ExecutionEvent,
  Finding,
  ReviewDocument,
} from "../api/types";
import { roleLabel } from "./review";
import {
  evidenceSourceMatches,
  resolveAnchor,
  type MarkedRange,
  type ResolvedAnchor,
} from "./text";

export type ReviewStageTone = "info" | "pending" | "good" | "bad" | "neutral";

export interface ReviewStage {
  label: string;
  detail: string;
  tone: ReviewStageTone;
}

const PHASE_LABELS: { phases: string[]; label: string; detail: string }[] = [
  {
    phases: ["acceptance"],
    label: "正在准备审查",
    detail: "正在确认本次尝试使用的材料与运行配置。",
  },
  {
    phases: ["rulepack"],
    label: "正在加载审查依据",
    detail: "正在读取本次运行冻结的规则包与审查清单。",
  },
  {
    phases: ["rules"],
    label: "正在执行规则核对",
    detail: "规则命中会单独标识，不会自动写成人工结论。",
  },
  {
    phases: ["evidence", "evidence_tool", "verified_finding"],
    label: "正在核验依据",
    detail: "正在核对引用原文、解析身份与定位结果。",
  },
  {
    phases: ["model_request", "model", "agent_review", "agent_review_result"],
    label: "正在识别风险",
    detail: "模型输出只是建议；缺少可核对引用的内容不会成为结论。",
  },
  {
    phases: ["terminal_persistence"],
    label: "正在保存审查结果",
    detail: "正在写入本次尝试的发现、证据与运行记录。",
  },
  {
    phases: ["cancellation"],
    label: "正在取消审查",
    detail: "取消只终止本次尝试，已经持久化的记录仍会保留。",
  },
];

function latestEvent(events: ExecutionEvent[]): ExecutionEvent | null {
  let latest: ExecutionEvent | null = null;
  for (const event of events) {
    if (latest === null || event.seq > latest.seq) latest = event;
  }
  return latest;
}

/**
 * Converts persisted run and execution state into a business-facing stage.
 * It never claims a stage that the server has not reported.
 */
export function reviewStage(
  runState: string,
  events: ExecutionEvent[] = [],
  finality?: {
    partial_reason?: string | null;
    failure_message?: string | null;
  } | null,
): ReviewStage {
  if (runState === "awaiting_human") {
    return {
      label: "待人工复核",
      detail: "审查运行已暂停，等待逐条记录人工决定。",
      tone: "pending",
    };
  }
  if (runState === "completed") {
    return {
      label: "审查完成",
      detail: "运行已完成；完成不等于合同获批，仍需查看发现与人工决定。",
      tone: "good",
    };
  }
  if (runState === "partial") {
    return {
      label: "部分完成，待人工复核",
      detail: finality?.partial_reason ?? "部分结果已保留，未能完成的步骤未报告原因。",
      tone: "pending",
    };
  }
  if (runState === "failed") {
    return {
      label: "审查失败",
      detail: finality?.failure_message ?? "这次运行失败，失败前的记录仍然保留。",
      tone: "bad",
    };
  }
  if (runState === "cancelled") {
    return {
      label: "审查已取消",
      detail: "取消前已经持久化的发现和用量记录仍然保留。",
      tone: "neutral",
    };
  }
  if (runState === "interrupted") {
    return {
      label: "审查已中断",
      detail: "运行被中断，可人工重新尝试；原尝试不会被替换。",
      tone: "bad",
    };
  }
  if (runState === "accepted" || runState === "queued") {
    return {
      label: "等待开始审查",
      detail: "运行已受理，正在等待执行资源。",
      tone: "info",
    };
  }
  if (runState === "started" || runState === "running") {
    const phase = latestEvent(events);
    const matched = phase
      ? PHASE_LABELS.find((item) => item.phases.includes(phase.phase))
      : null;
    if (matched) return { ...matched, tone: "info" };
    return {
      label: "正在进行审查",
      detail: phase
        ? `最近执行阶段：${phase.phase}，服务未提供业务化说明。`
        : "服务尚未返回可读的执行阶段。",
      tone: "info",
    };
  }
  return {
    label: "状态未识别",
    detail: "服务返回了尚未识别的运行状态。",
    tone: "neutral",
  };
}

export interface FindingSummary {
  total: number;
  high: number;
  medium: number;
  low: number;
  info: number;
  unrated: number;
  undecided: number;
  decided: number;
  conflicts: number;
  insufficient: number;
  verified: number;
}

export function summarizeFindings(findings: Finding[]): FindingSummary {
  const summary: FindingSummary = {
    total: findings.length,
    high: 0,
    medium: 0,
    low: 0,
    info: 0,
    unrated: 0,
    undecided: 0,
    decided: 0,
    conflicts: 0,
    insufficient: 0,
    verified: 0,
  };
  for (const finding of findings) {
    switch (finding.severity ?? "unrated") {
      case "high":
        summary.high += 1;
        break;
      case "medium":
        summary.medium += 1;
        break;
      case "low":
        summary.low += 1;
        break;
      case "info":
        summary.info += 1;
        break;
      default:
        summary.unrated += 1;
    }
    if (finding.decisions.length === 0) summary.undecided += 1;
    else summary.decided += 1;
    if (finding.relation === "conflict") summary.conflicts += 1;
    if (finding.relation === "insufficient") summary.insufficient += 1;
    if (finding.verification_status === "verified") summary.verified += 1;
  }
  return summary;
}

export function severityRank(severity: string | null): number {
  switch (severity ?? "unrated") {
    case "high":
      return 0;
    case "medium":
      return 1;
    case "low":
      return 2;
    case "info":
      return 3;
    default:
      return 4;
  }
}

function resolvedFor(
  anchor: Finding["evidence"][number],
  documents: ReviewDocument[],
): { document: ReviewDocument | null; resolved: ResolvedAnchor | null } {
  const document =
    documents.find((item) =>
      evidenceSourceMatches(anchor, item.id, item.parsed_document),
    ) ??
    documents.find((item) => item.id === anchor.document_version_id) ??
    null;
  return {
    document,
    resolved: document
      ? resolveAnchor(document.text, anchor, document.parsed_document)
      : null,
  };
}

/**
 * Only an anchor that resolves against the frozen parsed text is presented as a
 * precise location. A quoted snapshot without a matching span stays unlocated.
 */
export function findingLocation(
  finding: Finding,
  documents: ReviewDocument[],
): string {
  if (finding.evidence.length === 0) return "未附原文位置";
  for (const anchor of finding.evidence) {
    const { document, resolved } = resolvedFor(anchor, documents);
    if (!document || resolved?.kind !== "highlight") continue;
    const parts = [
      resolved.page === null ? null : `第 ${resolved.page} 页`,
      roleLabel(document.role),
    ].filter(Boolean);
    return parts.length > 0 ? parts.join(" · ") : "正文已定位";
  }
  const quotedPage = finding.evidence.find((item) => item.page !== null)?.page;
  if (quotedPage !== null && quotedPage !== undefined) {
    return `记录页码 ${quotedPage} · 未能定位`;
  }
  return "原文位置未报告";
}

export interface DocumentHighlight extends MarkedRange {
  claim: string;
}

/**
 * Returns only spans that still resolve against this frozen document. A quoted
 * snapshot or a mismatched offset never becomes a clickable highlight.
 */
export function findingHighlights(
  findings: Finding[],
  document: ReviewDocument,
  selectedId: string | null,
): DocumentHighlight[] {
  if (document.text === null) return [];
  const highlights: DocumentHighlight[] = [];
  for (const finding of findings) {
    for (const anchor of finding.evidence) {
      if (
        !evidenceSourceMatches(anchor, document.id, document.parsed_document)
      )
        continue;
      const resolved = resolveAnchor(
        document.text,
        anchor,
        document.parsed_document,
      );
      if (resolved.kind !== "highlight") continue;
      highlights.push({
        findingId: finding.id,
        claim: finding.claim || finding.id,
        severity: finding.severity,
        active: finding.id === selectedId,
        start: resolved.start,
        end: resolved.end,
      });
    }
  }
  return highlights;
}

/** Pages are only those reported by saved clauses with a usable offset. */
export function reportedPages(document: ReviewDocument): number[] {
  const pages = new Set<number>();
  for (const clause of document.clauses) {
    if (clause.page !== null) pages.add(clause.page);
  }
  return [...pages].sort((left, right) => left - right);
}

export function firstClauseOffsetForPage(
  document: ReviewDocument,
  page: number,
): number | null {
  const clause = document.clauses.find((item) => item.page === page);
  return clause?.text_offset.start ?? null;
}

const DOMAIN_LABELS: Record<string, string> = {
  warranty: "质量保证",
  quality: "质量",
  payment: "付款",
  price: "价格",
  delivery: "交付",
  liability: "责任",
  confidentiality: "保密",
  "intellectual-property": "知识产权",
  ip: "知识产权",
  termination: "合同终止",
  compliance: "合规",
  technical: "技术",
  commercial: "商务",
  fulfillment: "履约",
  counterparty: "相对方",
  dispute: "争议解决",
  invoice: "发票",
  tax: "税务",
  service: "服务",
  sla: "服务水平",
  security: "安全",
  data: "数据",
  privacy: "隐私",
};

/**
 * Business-facing label for the free-form finding domain. Unknown values from
 * the service stay visible unchanged instead of being guessed into a category.
 */
export function domainLabel(domain: string | null | undefined): string | null {
  if (!domain) return null;
  return DOMAIN_LABELS[domain.toLowerCase()] ?? domain;
}

export function verificationLabel(status: string): string {
  if (status === "verified") return "已核验";
  if (status === "unverified") return "未核验";
  return "核验状态未报告";
}

export interface DecisionSummary {
  label: string;
  pending: boolean;
}

export function decisionSummary(finding: Finding): DecisionSummary {
  const decision = finding.decisions[finding.decisions.length - 1];
  if (!decision) return { label: "待人工处理", pending: true };
  return { label: "已有处理记录", pending: false };
}
