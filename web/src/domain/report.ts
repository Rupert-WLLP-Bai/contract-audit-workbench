import type { EvidenceAnchor, ParsedDocumentMetadata } from "../api/types";
import type {
  ReportAnchor,
  ReportDecision,
  ReportDetail,
  ReportDocument,
  ReportFinding,
} from "../api/reports";
import { resolveAnchor, type MarkedRange, type ResolvedAnchor } from "./text";
import { domainLabel, severityRank } from "./review-presentation";

export interface ReportSummary {
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
  locatedAnchors: number;
  totalAnchors: number;
}

export function reportSummary(report: ReportDetail): ReportSummary {
  const summary: ReportSummary = {
    total: report.findings.length,
    high: 0,
    medium: 0,
    low: 0,
    info: 0,
    unrated: 0,
    undecided: 0,
    decided: 0,
    conflicts: 0,
    insufficient: 0,
    locatedAnchors: 0,
    totalAnchors: 0,
  };
  const decidedFindings = new Set(report.decisions.map((item) => item.finding_id));
  for (const finding of report.findings) {
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
    if (decidedFindings.has(finding.id)) summary.decided += 1;
    else summary.undecided += 1;
    if (finding.relation === "conflict") summary.conflicts += 1;
    if (finding.relation === "insufficient") summary.insufficient += 1;
    summary.totalAnchors += finding.anchors.length;
    for (const anchor of finding.anchors) {
      const document = reportDocument(report, anchor.document_id);
      if (document && resolveReportAnchor(anchor, document).kind === "highlight")
        summary.locatedAnchors += 1;
    }
  }
  return summary;
}

export function reportDocument(
  report: ReportDetail,
  documentId: string,
): ReportDocument | null {
  return report.documents.find((item) => item.id === documentId) ?? null;
}

export function reportFindings(report: ReportDetail): ReportFinding[] {
  return [...report.findings].sort((left, right) => {
    const severity = severityRank(left.severity) - severityRank(right.severity);
    if (severity !== 0) return severity;
    return left.created_at.localeCompare(right.created_at);
  });
}

export function findingDecisions(
  report: ReportDetail,
  findingId: string,
): ReportDecision[] {
  return report.decisions
    .filter((item) => item.finding_id === findingId)
    .sort((left, right) => left.created_at.localeCompare(right.created_at));
}

export function latestFindingDecision(
  report: ReportDetail,
  findingId: string,
): ReportDecision | null {
  const decisions = findingDecisions(report, findingId);
  return decisions.length === 0 ? null : decisions[decisions.length - 1]!;
}

export function reportAnchorAsEvidence(anchor: ReportAnchor): EvidenceAnchor {
  return {
    id: anchor.id,
    document_version_id: anchor.document_id,
    parsed_document_id: anchor.parsed_document_id,
    normalized_digest: anchor.normalized_digest,
    quote: anchor.quote,
    status: anchor.status,
    page: anchor.page,
    text_offset: anchor.text_offset,
    bbox: null,
    mismatch_reason: anchor.mismatch_reason,
  };
}

export function parsedMetadataFor(
  document: ReportDocument,
): ParsedDocumentMetadata {
  return document.parsed_document;
}

export function resolveReportAnchor(
  anchor: ReportAnchor,
  document: ReportDocument,
): ResolvedAnchor {
  return resolveAnchor(
    document.text,
    reportAnchorAsEvidence(anchor),
    document.parsed_document,
  );
}

export interface ReportAnchorMatch {
  finding: ReportFinding;
  anchor: ReportAnchor;
  document: ReportDocument;
  resolved: ResolvedAnchor;
}

/** All anchors for one frozen document, including quoted snapshots that do not resolve. */
export function reportDocumentAnchors(
  report: ReportDetail,
  document: ReportDocument,
): ReportAnchorMatch[] {
  const matches: ReportAnchorMatch[] = [];
  for (const finding of report.findings) {
    for (const anchor of finding.anchors) {
      if (anchor.document_id !== document.id) continue;
      matches.push({
        finding,
        anchor,
        document,
        resolved: resolveReportAnchor(anchor, document),
      });
    }
  }
  return matches;
}

export function reportSourceHighlights(
  report: ReportDetail,
  document: ReportDocument,
  selectedFindingId: string | null,
): MarkedRange[] {
  return reportDocumentAnchors(report, document)
    .filter((item) => item.resolved.kind === "highlight")
    .map((item) => {
      const resolved = item.resolved;
      if (resolved.kind !== "highlight")
        throw new Error("unreachable highlight filter");
      return {
        start: resolved.start,
        end: resolved.end,
        findingId: item.finding.id,
        severity: item.finding.severity,
        active: item.finding.id === selectedFindingId,
        claim: item.finding.claim || item.finding.id,
      };
    });
}

export function reportFindingLocation(
  report: ReportDetail,
  finding: ReportFinding,
): string {
  if (finding.anchors.length === 0) return "未附原文位置";
  for (const anchor of finding.anchors) {
    const document = reportDocument(report, anchor.document_id);
    if (!document) continue;
    const resolved = resolveReportAnchor(anchor, document);
    if (resolved.kind !== "highlight") continue;
    const page = resolved.page === null ? null : `第 ${resolved.page} 页`;
    return [page, document.role === "" ? null : document.filename]
      .filter(Boolean)
      .join(" · ");
  }
  const quotedPage = finding.anchors.find((item) => item.page !== null)?.page;
  if (quotedPage !== null && quotedPage !== undefined)
    return `记录页码 ${quotedPage} · 未能定位`;
  return "原文位置未报告";
}

export function reportAnchorLocation(
  anchor: ReportAnchor,
  document: ReportDocument,
): string {
  const resolved = resolveReportAnchor(anchor, document);
  if (resolved.kind === "highlight") {
    return resolved.page === null
      ? "已在冻结正文中定位"
      : `已在冻结正文第 ${resolved.page} 页定位`;
  }
  if (resolved.kind === "snapshot")
    return resolved.reason || "只能读取引用快照，未在正文中定位";
  return resolved.reason;
}

export function reportDomainCounts(
  findings: ReportFinding[],
): { domain: string; label: string; count: number }[] {
  const counts = new Map<string, number>();
  for (const finding of findings) {
    const key = finding.domain || "未报告领域";
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([domain, count]) => ({
      domain,
      label: domainLabel(domain) ?? domain,
      count,
    }))
    .sort(
      (left, right) =>
        right.count - left.count || left.label.localeCompare(right.label, "zh"),
    );
}

export interface ReportConclusion {
  tone: "error" | "warning" | "info";
  title: string;
  detail: string;
}

/**
 * Wording is derived only from the frozen finding and decision counts. It never
 * presents a review result as an approval or a legal opinion.
 */
export function reportConclusion(summary: ReportSummary): ReportConclusion {
  if (summary.total === 0)
    return {
      tone: "info",
      title: "报告未记录发现项",
      detail:
        "冻结报告中的发现列表为空；这不代表合同获批，仍需人工确认审查范围与材料完整性。",
    };
  if (summary.high > 0)
    return {
      tone: "error",
      title: `报告包含 ${summary.high} 项高风险发现`,
      detail: `报告中另有 ${summary.conflicts} 项冲突、${summary.undecided} 项待人工处理。请逐条核对冻结证据与人工决定，不能把规则或模型建议直接当成批准结论。`,
    };
  if (summary.conflicts > 0)
    return {
      tone: "warning",
      title: `有 ${summary.conflicts} 项发现需要人工复核`,
      detail: `其中 ${summary.undecided} 项尚未记录决定，${summary.decided} 项已有处理记录。规则命中与模型建议都不构成人工结论。`,
    };
  if (summary.undecided > 0)
    return {
      tone: "warning",
      title: `有 ${summary.undecided} 项发现等待人工处理`,
      detail: `报告共记录 ${summary.total} 项发现，${summary.decided} 项已有处理记录；请核对证据后再记录决定。`,
    };
  return {
    tone: "info",
    title: "报告中的发现均已有处理记录",
    detail:
      "处理记录不等于合同获批；报告仍记录技术审查快照，不构成法律意见或业务审批。",
  };
}

const DISPOSITION_LABELS: Record<string, string> = {
  confirm_risk: "建议确认风险",
  false_positive: "建议标记为误报",
  insufficient_evidence: "建议补充证据",
  accept_residual: "建议接受残余风险",
};

export function dispositionLabel(value: string | null): string {
  if (!value) return "服务未返回处置建议";
  return DISPOSITION_LABELS[value] ?? `处置建议未识别（${value}）`;
}

export function reportPagePath(caseId: string, reportId: string): string {
  return `/cases/${encodeURIComponent(caseId)}/reports/${encodeURIComponent(reportId)}`;
}
