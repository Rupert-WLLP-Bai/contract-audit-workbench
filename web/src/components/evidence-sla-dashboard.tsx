import { useEffect, useState } from "react";
import { Alert, Button, Empty, Table } from "antd";
import type { TableColumnsType } from "antd";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import {
  listEvidenceHistory,
  listEvidenceRequests,
  type EvidenceReceipt,
  type EvidenceRequest,
} from "../api/evidence-workflow";
import { errorMessage } from "../api/http";
import {
  averageResponseDurationMs,
  evidenceRequestStatusLabel,
  firstRegisteredEvidenceReceipt,
  firstResponseDurationMs,
  formatElapsedDuration,
  hasEvidenceDeadline,
  isEvidenceRequestOverdue,
  isOutstandingEvidenceRequest,
} from "../domain/evidence-sla";
import { caseHref, formatDateTime } from "../domain/review";
import { StatusText } from "./status-text";
import "./evidence-sla-dashboard.css";

export interface EvidenceSLACase {
  id: string;
  title: string;
}

interface EvidenceSLARow {
  key: string;
  caseId: string;
  caseTitle: string;
  request: EvidenceRequest;
  firstReceipt: EvidenceReceipt | null;
  firstResponseMs: number | null;
  historyLoading: boolean;
  historyUnavailable: boolean;
}

type PanelMode = "dashboard" | "reminder";

const maximumConcurrentSLAReads = 8;
let activeSLAReads = 0;
const queuedSLAReads: (() => void)[] = [];

async function withSLASlot<T>(read: () => Promise<T>): Promise<T> {
  if (activeSLAReads >= maximumConcurrentSLAReads) {
    await new Promise<void>((resolve) => queuedSLAReads.push(resolve));
  } else {
    activeSLAReads++;
  }
  try {
    return await read();
  } finally {
    const next = queuedSLAReads.shift();
    if (next) next();
    else activeSLAReads--;
  }
}

const statusTones: Record<string, "attention" | "progress" | "good" | "neutral"> = {
  open: "attention",
  received: "progress",
  verified: "good",
  cancelled: "neutral",
};

function responseCell(row: EvidenceSLARow) {
  if (row.historyLoading) return <span role="status">读取回执历史…</span>;
  if (row.historyUnavailable) return <span>回执历史不可用</span>;
  if (!row.firstReceipt)
    return <span>{row.request.status === "open" ? "尚无回执" : "未找到登记回执"}</span>;
  return (
    <div className="evidence-sla-response">
      <strong>
        {row.firstResponseMs === null
          ? "时长无法计算"
          : formatElapsedDuration(row.firstResponseMs)}
      </strong>
      <time className="meta" dateTime={row.firstReceipt.created_at}>
        首次登记 {formatDateTime(row.firstReceipt.created_at)}
      </time>
    </div>
  );
}

export function EvidenceSLADashboard({
  cases,
  mode = "dashboard",
}: {
  cases: readonly EvidenceSLACase[];
  mode?: PanelMode;
}) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const interval = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(interval);
  }, []);
  const queryClient = useQueryClient();
  const requestQueries = useQueries({
    queries: cases.map((item) => ({
      queryKey: ["evidence-requests", item.id] as const,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        withSLASlot(() => listEvidenceRequests(item.id, signal)),
      retry: false,
    })),
  });
  const requestItems = cases.flatMap((item, index) =>
    (requestQueries[index]?.data ?? []).map((request) => ({
      key: `${item.id}\u0000${request.id}`,
      caseId: item.id,
      caseTitle: item.title || item.id,
      request,
    })),
  );
  const historyItems = mode === "dashboard"
    ? requestItems.filter((item) => item.request.status !== "open")
    : [];
  const historyQueries = useQueries({
    queries: historyItems.map((item) => ({
      queryKey: ["evidence-requests", item.caseId, item.request.id, "history"] as const,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        withSLASlot(() => listEvidenceHistory(item.caseId, item.request.id, signal)),
      retry: false,
    })),
  });
  const historyIndex = new Map(
    historyItems.map((item, index) => [item.key, index] as const),
  );
  const rows: EvidenceSLARow[] = requestItems.map((item) => {
    const queryIndex = historyIndex.get(item.key);
    const historyQuery = queryIndex === undefined ? undefined : historyQueries[queryIndex];
    const firstReceipt = historyQuery?.data
      ? firstRegisteredEvidenceReceipt(historyQuery.data)
      : null;
    return {
      ...item,
      firstReceipt,
      firstResponseMs: firstResponseDurationMs(item.request, firstReceipt),
      historyLoading: historyQuery?.isLoading ?? false,
      historyUnavailable: historyQuery?.isError ?? false,
    };
  });
  const requestError = requestQueries.find((query) => query.isError);
  const requestLoading = requestQueries.some((query) => query.isLoading);
  const historyLoading = historyQueries.some((query) => query.isLoading);
  const historyUnavailable = historyQueries.some((query) => query.isError);
  const outstanding = rows.filter((row) => isOutstandingEvidenceRequest(row.request.status));
  const overdueCount = outstanding.filter((row) => isEvidenceRequestOverdue(row.request, now)).length;
  const noDeadlineCount = outstanding.filter((row) => !hasEvidenceDeadline(row.request.due_at)).length;
  const averageResponseMs = averageResponseDurationMs(rows.map((row) => row.firstResponseMs));
  const sortedRows = [...rows].sort((left, right) => {
    const overdueOrder =
      Number(isEvidenceRequestOverdue(right.request, now)) -
      Number(isEvidenceRequestOverdue(left.request, now));
    const leftDueAt = left.request.due_at ?? "";
    const rightDueAt = right.request.due_at ?? "";
    const leftDeadline = hasEvidenceDeadline(leftDueAt)
      ? Date.parse(leftDueAt)
      : Number.POSITIVE_INFINITY;
    const rightDeadline = hasEvidenceDeadline(rightDueAt)
      ? Date.parse(rightDueAt)
      : Number.POSITIVE_INFINITY;
    return overdueOrder || leftDeadline - rightDeadline ||
      right.request.created_at.localeCompare(left.request.created_at);
  });

  const retryReads = () => {
    void queryClient.invalidateQueries({ queryKey: ["evidence-requests"] });
  };

  if (requestError) {
    const message = errorMessage(requestError.error);
    return (
      <Alert
        className={mode === "reminder" ? "evidence-sla-pre-review" : undefined}
        type="error"
        showIcon
        title="补充材料请求无法完整读取"
        description={message}
        action={<Button onClick={retryReads}>重试读取</Button>}
      />
    );
  }
  if (requestLoading)
    return mode === "reminder" ? (
      <p className="evidence-sla-pre-review" role="status">正在检查补充材料请求，完成后会显示复审提醒。</p>
    ) : (
      <p role="status">正在读取当前授权案件的补充材料请求…</p>
    );

  if (mode === "reminder") {
    if (outstanding.length === 0) return null;
    return (
      <Alert
        className="evidence-sla-pre-review"
        type="warning"
        showIcon
        title={`复审前请核对 ${outstanding.length} 项未完成补证请求，其中 ${overdueCount} 项逾期`}
        description={
          <ul className="evidence-sla-reminder-list">
            {outstanding.map((row) => {
              const overdue = isEvidenceRequestOverdue(row.request, now);
              const dueAt = row.request.due_at ?? "";
              return (
                <li key={row.key}>
                  <Link
                    to={caseHref(
                      row.caseId,
                      row.request.source_run_id,
                      row.request.source_attempt_id,
                    )}
                  >
                    {row.request.question || `补证请求 ${row.request.id}`} ·{" "}
                    {evidenceRequestStatusLabel(row.request.status)}
                  </Link>
                  {"；"}
                  {hasEvidenceDeadline(dueAt)
                    ? `期限 ${formatDateTime(dueAt)}${overdue ? "（逾期）" : ""}`
                    : "未设置期限"}
                </li>
              );
            })}
          </ul>
        }
      />
    );
  }

  const columns: TableColumnsType<EvidenceSLARow> = [
    {
      title: "案件",
      key: "case",
      width: 180,
      render: (_, row) => (
        <div className="evidence-sla-case">
          <Link
            to={caseHref(
              row.caseId,
              row.request.source_run_id,
              row.request.source_attempt_id,
            )}
          >
            {row.caseTitle}
          </Link>
          <span className="meta">案件 {row.caseId}</span>
        </div>
      ),
    },
    {
      title: "补证请求",
      key: "request",
      width: 280,
      render: (_, row) => (
        <div className="evidence-sla-request">
          <strong>{row.request.question || "未填写请求说明"}</strong>
          <span className="meta">请求 {row.request.id}</span>
        </div>
      ),
    },
    {
      title: "指派人 ID",
      dataIndex: ["request", "assignee_id"],
      key: "assignee",
      width: 150,
      render: (value: string) => <code>{value || "未报告"}</code>,
    },
    {
      title: "创建时间",
      key: "created",
      width: 160,
      render: (_, row) => (
        <time dateTime={row.request.created_at}>
          {formatDateTime(row.request.created_at)}
        </time>
      ),
    },
    {
      title: "截止时间",
      key: "deadline",
      width: 190,
      render: (_, row) => {
        const dueAt = row.request.due_at ?? "";
        if (!hasEvidenceDeadline(dueAt)) return <span>未设置期限</span>;
        const overdue = isEvidenceRequestOverdue(row.request, now);
        return (
          <div className="evidence-sla-deadline">
            <time dateTime={dueAt}>{formatDateTime(dueAt)}</time>
            {overdue ? <StatusText tone="bad">逾期</StatusText> : null}
          </div>
        );
      },
    },
    {
      title: "首次登记回执",
      key: "first-response",
      width: 220,
      render: (_, row) => responseCell(row),
    },
    {
      title: "状态",
      key: "status",
      width: 150,
      render: (_, row) => (
        <StatusText tone={statusTones[row.request.status] ?? "neutral"}>
          {evidenceRequestStatusLabel(row.request.status)}
        </StatusText>
      ),
    },
    {
      title: "跳转",
      key: "open",
      width: 130,
      render: (_, row) => (
        <Link to={caseHref(row.caseId, row.request.source_run_id, row.request.source_attempt_id)}>
          查看原始审查
        </Link>
      ),
    },
  ];

  return (
    <section className="evidence-sla-dashboard" aria-labelledby="evidence-sla-table-title">
      <header className="evidence-sla-dashboard-head">
        <div>
          <h2 id="evidence-sla-table-title">补充材料请求</h2>
          <p>按当前账号可读取的案件逐案读取请求与回执历史。</p>
        </div>
        <Button onClick={retryReads} disabled={requestLoading || historyLoading}>
          {requestLoading || historyLoading ? "正在更新" : "刷新数据"}
        </Button>
      </header>
      <dl className="evidence-sla-summary" aria-label="补证时效摘要">
        <div>
          <dt>未完成请求</dt>
          <dd>{outstanding.length}</dd>
        </div>
        <div>
          <dt>逾期请求</dt>
          <dd>{overdueCount}</dd>
        </div>
        <div>
          <dt>未设置期限</dt>
          <dd>{noDeadlineCount}</dd>
        </div>
        <div>
          <dt>首次登记回执平均时长</dt>
          <dd>
            {historyLoading
              ? "读取回执历史…"
              : historyUnavailable
                ? "回执历史不完整"
                : averageResponseMs === null
                  ? "暂无回执样本"
                  : formatElapsedDuration(averageResponseMs)}
          </dd>
          {!historyLoading && !historyUnavailable && averageResponseMs !== null ? (
            <small>基于 {rows.filter((row) => row.firstResponseMs !== null).length} 项有效时长样本</small>
          ) : null}
        </div>
      </dl>
      <p className="evidence-sla-method">
        时长从请求创建到系统首次登记回执计算，不代表对方实际发送时间；未设置期限的请求不计入逾期，
        无登记回执的请求不计入平均值。已回执仍需人工核验，才会变为已核验。
      </p>
      {rows.length === 0 ? (
        <Empty description="当前授权案件范围内没有补充材料请求。" />
      ) : (
        <Table
          className="evidence-sla-table"
          rowKey="key"
          columns={columns}
          dataSource={sortedRows}
          pagination={{ pageSize: 10, showSizeChanger: true, hideOnSinglePage: true }}
          scroll={{ x: 1_260 }}
          size="small"
        />
      )}
    </section>
  );
}
