import { FileTextOutlined, LinkOutlined } from "@ant-design/icons";
import { Tag } from "antd";
import { useEffect, useState } from "react";
import type { ReportDetail, ReportDocument } from "../api/reports";
import {
  reportDocumentAnchors,
  reportSourceHighlights,
} from "../domain/report";
import { formatBytes, parseLabel, roleLabel } from "../domain/review";
import { segmentMarkedText } from "../domain/text";
import { EmptyState } from "./ui";

function statusLabel(resolved: ReturnType<typeof reportDocumentAnchors>[number]["resolved"]): string {
  if (resolved.kind === "highlight") return "已在冻结正文定位";
  if (resolved.kind === "snapshot") return "仅引用快照";
  return "未能定位";
}

function AnchorList({
  report,
  document,
  onOpenFinding,
}: {
  report: ReportDetail;
  document: ReportDocument;
  onOpenFinding: (findingId: string) => void;
}) {
  const matches = reportDocumentAnchors(report, document);
  if (matches.length === 0)
    return <p className="report-source-empty">这份材料没有被冻结报告引用。</p>;
  return (
    <section className="report-source-anchors" aria-label="本材料的引用与依据">
      <h4>本材料的引用与依据</h4>
      <ul>
        {matches.map(({ finding, anchor, resolved }) => (
          <li key={`${finding.id}-${anchor.id}`}>
            <button
              type="button"
              className="report-source-anchor"
              onClick={() => onOpenFinding(finding.id)}
            >
              <span className="report-source-anchor__head">
                <Tag
                  color={
                    resolved.kind === "highlight"
                      ? "success"
                      : resolved.kind === "snapshot"
                        ? "warning"
                        : "default"
                  }
                >
                  {statusLabel(resolved)}
                </Tag>
                <strong>{finding.claim || finding.id}</strong>
              </span>
              <span className="report-source-anchor__quote">
                “{anchor.quote}”
              </span>
              {resolved.kind !== "highlight" ? (
                <span className="report-source-anchor__reason">
                  {resolved.reason}
                </span>
              ) : null}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function ReportSourceView({
  report,
  selectedFindingId,
  onOpenFinding,
}: {
  report: ReportDetail;
  selectedFindingId: string | null;
  onOpenFinding: (findingId: string) => void;
}) {
  const [documentId, setDocumentId] = useState(report.documents[0]?.id ?? "");
  const document =
    report.documents.find((item) => item.id === documentId) ??
    report.documents[0] ??
    null;

  useEffect(() => {
    if (report.documents.length === 0) {
      setDocumentId("");
      return;
    }
    if (!report.documents.some((item) => item.id === documentId))
      setDocumentId(report.documents[0]?.id ?? "");
  }, [documentId, report.documents]);

  if (!document)
    return (
      <EmptyState
        title="冻结报告没有返回审查材料"
        detail="报告仍未包含解析正文；不能据此还原原文位置。"
      />
    );

  const highlights = reportSourceHighlights(report, document, selectedFindingId);
  const segments = segmentMarkedText(document.text, highlights, []);
  const anchors = reportDocumentAnchors(report, document);
  const located = anchors.filter((item) => item.resolved.kind === "highlight");
  const pages = document.clauses
    .map((clause) => clause.page)
    .filter((page): page is number => page !== null);

  return (
    <div className="report-source">
      <aside className="report-source-docs" aria-label="审查材料">
        <h3>审查材料</h3>
        <ul>
          {report.documents.map((item) => {
            const count = reportDocumentAnchors(report, item).length;
            return (
              <li key={item.id}>
                <button
                  type="button"
                  className="report-source-doc"
                  aria-pressed={item.id === document.id}
                  onClick={() => setDocumentId(item.id)}
                >
                  <span className="report-source-doc__icon" aria-hidden="true">
                    <FileTextOutlined />
                  </span>
                  <span className="report-source-doc__copy">
                    <strong>{roleLabel(item.role)}</strong>
                    <span>{item.filename}</span>
                    <small>
                      {parseLabel(item.parsed_document.status)} ·{" "}
                      {formatBytes(item.size)} · 引用 {count} 条
                    </small>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      </aside>

      <section className="report-source-main" aria-label="冻结解析正文">
        <header className="report-source-head">
          <div>
            <h3>{document.filename}</h3>
            <p>
              {roleLabel(document.role)} · 解析状态{" "}
              {parseLabel(document.parsed_document.status)} ·{" "}
              {pages.length > 0
                ? `服务报告 ${pages.length} 个页码`
                : "页码未报告"}
            </p>
          </div>
          <Tag icon={<LinkOutlined />} color={located.length > 0 ? "blue" : "default"}>
            {`${located.length} / ${anchors.length} 条引用已定位`}
          </Tag>
        </header>
        {document.text.trim() === "" ? (
          <EmptyState
            title="解析正文为空"
            detail="服务返回了空正文；引用只能保留快照，不能在原文中定位。"
          />
        ) : (
          <div className="report-source-text" aria-label="冻结解析正文内容">
            {segments.map((segment) => {
              if (segment.marked.length === 0)
                return (
                  <span key={`${segment.start}-${segment.text.length}`}>
                    {segment.text}
                  </span>
                );
              const primary =
                segment.marked.find((item) => item.active) ?? segment.marked[0]!;
              const peers = segment.marked
                .filter((item) => item.findingId !== primary.findingId)
                .map((item) => item.findingId);
              return (
                <mark
                  key={`${segment.start}-${segment.text.length}`}
                  className="report-source-mark"
                  data-finding-id={primary.findingId}
                  data-peer-finding-ids={peers.join(" ") || undefined}
                  data-severity={primary.severity ?? "unrated"}
                  data-active={primary.active ? "true" : undefined}
                  role="button"
                  tabIndex={0}
                  aria-label={`查看发现：${primary.claim ?? primary.findingId}`}
                  onClick={() => onOpenFinding(primary.findingId)}
                  onKeyDown={(event) => {
                    if (event.key !== "Enter" && event.key !== " ") return;
                    event.preventDefault();
                    onOpenFinding(primary.findingId);
                  }}
                >
                  {segment.text}
                </mark>
              );
            })}
          </div>
        )}
        <AnchorList
          report={report}
          document={document}
          onOpenFinding={onOpenFinding}
        />
      </section>
    </div>
  );
}
