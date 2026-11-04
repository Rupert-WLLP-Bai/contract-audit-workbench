// File metadata (manifest mirror):
// Purpose: Renders machine identifiers and digests as human-readable text while keeping the full value available on demand for audit and troubleshooting.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useCallback, useState, type ReactNode } from "react";

/** Short, non-technical stand-in for a raw `case_...` / `run_...` style id. */
export function technicalLabel(value: string | null | undefined): string {
  if (!value) return "未提供";
  const [prefix = ""] = value.split("_", 1);
  const labels: Record<string, string> = {
    case: "案件",
    run: "审查",
    att: "尝试",
    doc: "材料",
    mat: "材料版本",
    report: "报告",
    rem: "整改",
    parsed: "解析版本",
    task: "任务",
    subject: "主体",
    knowledge: "知识",
    request: "请求",
    binding: "绑定",
  };
  return labels[prefix] ?? "记录";
}

/** Copy a full identifier to the clipboard; failures stay visible, never silent. */
export function useCopyTechnicalValue() {
  const [copied, setCopied] = useState(false);
  const copy = useCallback((value: string) => {
    void navigator.clipboard?.writeText(value).then(
      () => setCopied(true),
      () => setCopied(false),
    );
    window.setTimeout(() => setCopied(false), 2000);
  }, []);
  return { copied, copy };
}

/**
 * Primary business surfaces show the human label; the raw id/digest stays in
 * `title`, in the URL/API, and behind an explicit "技术详情" disclosure.
 */
export function TechnicalDetail({
  value,
  label,
  children,
}: {
  value: string | null | undefined;
  label?: string;
  children?: ReactNode;
}) {
  const { copied, copy } = useCopyTechnicalValue();
  if (!value) return null;
  return (
    <details className="caw-technical-detail">
      <summary>{label ?? "技术详情"}</summary>
      <div className="caw-technical-detail-body">
        <code className="caw-wrap" title={value}>
          {value}
        </code>
        <button type="button" className="caw-technical-copy" onClick={() => copy(value)}>
          {copied ? "已复制" : "复制完整标识"}
        </button>
        {children}
      </div>
    </details>
  );
}
