// File metadata (manifest mirror):
// Purpose: Renders machine identifiers and digests as human-readable text while keeping the full value available on demand for audit and troubleshooting.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-05
// Revision: r3
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

function TechnicalValue({ value }: { value: string }) {
  const { status, copy } = useCopyTechnicalValue(value);
  return (
    <span className="caw-technical-value">
      <code className="caw-wrap" title={value}>
        {value}
      </code>
      <button
        type="button"
        className="caw-technical-copy"
        aria-label="复制完整值"
        onClick={() => void copy()}
      >
        复制
      </button>
      {status !== "idle" ? (
        <span role="status" aria-live="polite" className="caw-technical-value-status">
          {status === "success"
            ? "已复制完整值"
            : "复制失败，请选中完整值手动复制。"}
        </span>
      ) : null}
    </span>
  );
}

export interface TechnicalField {
  label: string;
  value: string | null | undefined;
}

/**
 * Keep the first screen about the business object. All stable identifiers,
 * hashes and enum codes stay together in one disclosure so they remain
 * available for audit and support without becoming a second column of prose.
 */
export function TechnicalFieldGroup({
  fields,
  label = "技术详情 · 完整标识与编码",
}: {
  fields: readonly TechnicalField[];
  label?: string;
}) {
  if (fields.length === 0) return null;
  return (
    <details className="caw-technical-detail caw-technical-group">
      <summary>
        {label}（{fields.length} 项）
      </summary>
      <dl className="caw-technical-group-body">
        {fields.map((field, index) => (
          <div className="caw-technical-group-row" key={`${field.label}:${index}`}>
            <dt>{field.label}</dt>
            <dd>
              {field.value ? (
                <TechnicalValue value={field.value} />
              ) : (
                <span className="caw-technical-empty">未报告</span>
              )}
            </dd>
          </div>
        ))}
      </dl>
    </details>
  );
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
        {status !== "idle" ? (
          <span role="status" aria-live="polite">
            {status === "success" ? "已复制完整值" : "复制失败，请选中上方完整值手动复制。"}
          </span>
        ) : null}
        {children}
      </div>
    </details>
  );
}
