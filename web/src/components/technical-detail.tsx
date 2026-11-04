// File metadata (manifest mirror):
// Purpose: Renders machine identifiers and digests as human-readable text while keeping the full value available on demand for audit and troubleshooting.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

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
  return Object.hasOwn(labels, prefix) ? labels[prefix]! : "记录";
}

/** Copy a full identifier to the clipboard; failures stay visible, never silent. */
export function useCopyTechnicalValue(value: string | null | undefined) {
  const [result, setResult] = useState<{ value: string; sequence: number; status: "success" | "failure" } | null>(null);
  const current = useRef(value);
  const request = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mounted = useRef(false);
  if (current.current !== value) {
    current.current = value;
    request.current += 1;
  }
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      request.current += 1;
      if (timer.current !== null) clearTimeout(timer.current);
    };
  }, []);
  useEffect(() => {
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = null;
  }, [value]);
  const copy = useCallback(async () => {
    if (!value) return;
    const sequence = ++request.current;
    if (timer.current !== null) clearTimeout(timer.current);
    setResult(null);
    let status: "success" | "failure";
    try {
      if (!navigator.clipboard?.writeText) throw new Error("Clipboard unavailable");
      await navigator.clipboard.writeText(value);
      status = "success";
    } catch {
      status = "failure";
    }
    if (!mounted.current || sequence !== request.current || current.current !== value) return;
    setResult({ value, sequence, status });
    if (status === "success") {
      timer.current = setTimeout(() => {
        if (mounted.current && sequence === request.current && current.current === value) setResult(null);
      }, 2000);
    }
  }, [value]);
  return { status: result && result.value === value && result.sequence === request.current ? result.status : "idle", copy };
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
  const { status, copy } = useCopyTechnicalValue(value);
  if (!value) return null;
  return (
    <details className="caw-technical-detail">
      <summary>{label ?? "技术详情"}</summary>
      <div className="caw-technical-detail-body">
        <code className="caw-wrap" title={value}>
          {value}
        </code>
        <button type="button" className="caw-technical-copy" onClick={() => void copy()}>
          复制完整值
        </button>
        <span role="status" aria-live="polite">
          {status === "success" ? "已复制完整值" : status === "failure" ? "复制失败，请选中上方完整值手动复制。" : ""}
        </span>
        {children}
      </div>
    </details>
  );
}
