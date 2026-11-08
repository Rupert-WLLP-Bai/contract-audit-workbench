// File metadata (manifest mirror):
// Purpose: Lists server-visible cases with URL-backed all, actor-assigned pending-task, and recent-run views; searches case identities and opens the newest persisted run with its exact frozen attempt identity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-08
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import { Button, Input, Table, Tabs, type TableColumnsType } from "antd";
import { useMemo } from "react";
import { useSearchParams } from "react-router";
import { errorMessage } from "../api/http";
import { useSession, useWorkbench } from "../api/queries";
import type { WorkbenchCase, WorkbenchRun, WorkbenchSnapshot } from "../api/types";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import { TechnicalDetail } from "../components/technical-detail";
import { businessLineLabel } from "../domain/business-labels";
import {
  businessStatusLabel,
  businessStatusTone,
  formatDateTime,
  latestRun,
  runLabel,
  runTone,
} from "../domain/review";
import "./review-entry-page.css";

type ReviewEntryView = "all" | "mine" | "recent";

const REVIEW_ENTRY_VIEWS: { key: ReviewEntryView; label: string }[] = [
  { key: "all", label: "全部案件" },
  { key: "mine", label: "待我处理" },
  { key: "recent", label: "最近审查" },
];

function parseReviewEntryView(value: string | null): ReviewEntryView {
  return value === "mine" || value === "recent" ? value : "all";
}

function indexRunsByCase(runs: WorkbenchRun[]): Map<string, WorkbenchRun[]> {
  const indexed = new Map<string, WorkbenchRun[]>();
  for (const run of runs) {
    const caseRuns = indexed.get(run.case_id) ?? [];
    caseRuns.push(run);
    indexed.set(run.case_id, caseRuns);
  }
  return indexed;
}

function latestRunForCase(
  runsByCase: Map<string, WorkbenchRun[]>,
  caseId: string,
): WorkbenchRun | null {
  return latestRun(runsByCase.get(caseId) ?? []);
}

function caseSearchText(item: WorkbenchCase): string {
  return [
    item.title,
    item.case_number,
    item.profile?.customer,
    item.profile?.supplier,
    item.customer,
    item.supplier,
    item.owner_name,
    item.business_line,
    item.scenario,
    item.id,
  ]
    .filter(Boolean)
    .join(" ")
    .toLocaleLowerCase();
}

function selectReviewEntryCases(
  snapshot: WorkbenchSnapshot,
  view: ReviewEntryView,
  query: string,
  actorId: string,
  runsByCase: Map<string, WorkbenchRun[]>,
): WorkbenchCase[] {
  const pendingTaskCaseIds = new Set(
    actorId
      ? snapshot.tasks
          .filter(
            (task) =>
              task.assignee_id === actorId && task.status !== "completed",
          )
          .map((task) => task.case_id)
      : [],
  );
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const visible = snapshot.cases.filter((item) => {
    if (view === "mine" && !pendingTaskCaseIds.has(item.id)) return false;
    if (view === "recent" && !latestRunForCase(runsByCase, item.id)) return false;
    return !normalizedQuery || caseSearchText(item).includes(normalizedQuery);
  });
  if (view !== "recent") return visible;
  return visible.sort((left, right) => {
    const leftTime = latestRunForCase(runsByCase, left.id)?.updated_at ?? "";
    const rightTime = latestRunForCase(runsByCase, right.id)?.updated_at ?? "";
    return rightTime.localeCompare(leftTime);
  });
}

function ReviewEntryCaseCell({ item }: { item: WorkbenchCase }) {
  const context =
    item.profile?.customer ||
    (item.business_line ? businessLineLabel(item.business_line) : "");
  const businessStatus = item.business_status
    ? businessStatusLabel(item.business_status)
    : "";
  return (
    <div className="review-entry-case">
      <strong>{item.title}</strong>
      {item.case_number ? <span>业务编号：{item.case_number}</span> : null}
      {context ? <span>{context}</span> : null}
      {businessStatus ? (
        <StatusText tone={businessStatusTone(item.business_status ?? "")}>
          {businessStatus}
        </StatusText>
      ) : null}
    </div>
  );
}

function ReviewEntryStatusCell({
  item,
  run,
}: {
  item: WorkbenchCase;
  run: WorkbenchRun | null;
}) {
  if (!run) return <span className="review-entry-muted">尚未发起</span>;
  return (
    <div className="review-entry-status">
      <StatusText tone={runTone(run.state)}>{runLabel(run.state)}</StatusText>
      <TechnicalDetail value={item.id} label="案件标识" />
      <TechnicalDetail value={run.id} label="审查标识" />
      <TechnicalDetail value={run.attempt_id} label="执行标识" />
    </div>
  );
}

function ReviewEntryTodoCell({
  item,
  assignedTasks,
}: {
  item: WorkbenchCase;
  assignedTasks: number;
}) {
  return (
    <div className="review-entry-todo">
      <span>
        {item.pending_decisions > 0
          ? `${item.pending_decisions} 项待判断`
          : "暂无待判断"}
      </span>
      {assignedTasks > 0 ? (
        <span className="review-entry-muted">{assignedTasks} 项分派待办</span>
      ) : null}
    </div>
  );
}

function ReviewEntryActions({
  item,
  run,
}: {
  item: WorkbenchCase;
  run: WorkbenchRun | null;
}) {
  return (
    <div className="review-entry-actions">
      {run ? (
        <Button
          type="primary"
          href={`/cases/${encodeURIComponent(item.id)}/reviews/${encodeURIComponent(run.id)}${run.attempt_id ? `?attempt_id=${encodeURIComponent(run.attempt_id)}` : ""}`}
        >
          打开审查
        </Button>
      ) : null}
      <Button href={`/cases/${encodeURIComponent(item.id)}`}>
        {run ? "案件详情" : "进入案件"}
      </Button>
    </div>
  );
}

function reviewEntryColumns(
  runsByCase: Map<string, WorkbenchRun[]>,
  pendingTaskCounts: Map<string, number>,
): TableColumnsType<WorkbenchCase> {
  return [
    {
      title: "合同 / 案件",
      key: "case",
      render: (_, item) => <ReviewEntryCaseCell item={item} />,
    },
    {
      title: "执行状态",
      key: "status",
      width: 190,
      render: (_, item) => (
        <ReviewEntryStatusCell
          item={item}
          run={latestRunForCase(runsByCase, item.id)}
        />
      ),
    },
    {
      title: "待办",
      key: "todo",
      width: 180,
      render: (_, item) => (
        <ReviewEntryTodoCell
          item={item}
          assignedTasks={pendingTaskCounts.get(item.id) ?? 0}
        />
      ),
    },
    {
      title: "最近执行",
      key: "recent",
      width: 180,
      render: (_, item) => {
        const run = latestRunForCase(runsByCase, item.id);
        return run ? (
          <time dateTime={run.updated_at}>{formatDateTime(run.updated_at)}</time>
        ) : (
          <span className="review-entry-muted">尚无审查记录</span>
        );
      },
    },
    {
      title: "操作",
      key: "actions",
      width: 210,
      align: "right",
      render: (_, item) => (
        <ReviewEntryActions
          item={item}
          run={latestRunForCase(runsByCase, item.id)}
        />
      ),
    },
  ];
}

function ReviewEntryHeader() {
  return (
    <header className="page-head review-entry-header">
      <div>
        <h1>合同审查</h1>
        <p>从当前有权访问的案件进入审查，进入有权访问的案件继续办理。</p>
      </div>
      <div className="review-entry-primary-action">
        <Button type="primary" href="/cases">
          发起审查
        </Button>
        <span>先选择案件；系统会在案件准备流程中核对权限与材料。</span>
      </div>
    </header>
  );
}

function ReviewEntrySummary({ snapshot }: { snapshot: WorkbenchSnapshot }) {
  const items = [
    {
      label: "可见案件",
      value: snapshot.cases.length,
      state: snapshot.cases.length > 0 ? "filled" : "empty",
    },
    {
      label: "本周运行",
      value: snapshot.runs.length,
      state: snapshot.runs.length > 0 ? "filled" : "empty",
    },
    {
      label: "待人工处理",
      value: snapshot.dashboard.pending_review_cases,
      state: snapshot.dashboard.pending_review_cases > 0 ? "open" : "filled",
    },
  ];
  return (
    <ol className="review-entry-summary" aria-label="审查工作区摘要">
      {items.map((item) => (
        <li key={item.label} data-state={item.state}>
          <span className="review-entry-summary-mark" aria-hidden="true" />
          <span>{item.label}</span>
          <strong>{item.value}</strong>
        </li>
      ))}
    </ol>
  );
}

function ReviewEntryCases({
  snapshot,
  view,
  query,
  actorId,
  runsByCase,
  onViewChange,
  onQueryChange,
}: {
  snapshot: WorkbenchSnapshot;
  view: ReviewEntryView;
  query: string;
  actorId: string;
  runsByCase: Map<string, WorkbenchRun[]>;
  onViewChange: (view: ReviewEntryView) => void;
  onQueryChange: (query: string) => void;
}) {
  const pendingTaskCounts = new Map<string, number>();
  for (const task of snapshot.tasks) {
    if (
      actorId &&
      task.assignee_id === actorId &&
      task.status !== "completed"
    ) {
      pendingTaskCounts.set(
        task.case_id,
        (pendingTaskCounts.get(task.case_id) ?? 0) + 1,
      );
    }
  }
  const cases = selectReviewEntryCases(
    snapshot,
    view,
    query,
    actorId,
    runsByCase,
  );
  const tabItems = REVIEW_ENTRY_VIEWS.map(({ key, label }) => ({
    key,
    label,
  }));
  const emptyTitle =
    view === "mine"
      ? "当前没有分派给你的待办"
      : view === "recent"
        ? "当前没有审查记录"
        : query
          ? "没有符合搜索条件的案件"
          : "当前身份没有可访问案件";
  const emptyDetail =
    view === "mine"
      ? "仅显示分派给当前登录成员且尚未完成的任务。"
      : view === "recent"
        ? "案件列表按当前服务端授权返回；有审查记录后会显示在此处。"
        : query
          ? "请检查案件名称、业务编号或客户名称。"
          : "案件列表按当前服务端授权返回。";

  return (
    <section className="review-entry-collection" aria-label="审查案件列表">
      <div className="review-entry-list-toolbar">
        <Tabs
          activeKey={view}
          items={tabItems}
          onChange={(key) => onViewChange(parseReviewEntryView(key))}
        />
        <Input
          aria-label="搜索案件"
          allowClear
          placeholder="名称、业务编号或客户"
          value={query}
          onChange={(event) => onQueryChange(event.target.value)}
        />
      </div>
      <p className="review-entry-scope">
        显示当前已加载的授权案件。运行状态、待办与最近执行分别呈现；执行完成后请继续核对审查结论。小屏幕可横向滚动表格。
      </p>
      {cases.length === 0 ? (
        <PageStatus kind="empty" title={emptyTitle} detail={emptyDetail} />
      ) : (
        <div className="review-entry-table">
          <Table<WorkbenchCase>
            key={`${view}:${query}`}
            rowKey="id"
            columns={reviewEntryColumns(runsByCase, pendingTaskCounts)}
            dataSource={cases}
            size="middle"
            tableLayout="fixed"
            scroll={{ x: 980 }}
            pagination={{
              defaultPageSize: 5,
              pageSizeOptions: [5, 10, 20, 50],
              showSizeChanger: true,
              showTotal: (total) => `共 ${total} 份合同`,
            }}
          />
        </div>
      )}
    </section>
  );
}

function ReviewEntryContent({
  snapshot,
  view,
  query,
  actorId,
  runsByCase,
  onViewChange,
  onQueryChange,
}: {
  snapshot: WorkbenchSnapshot;
  view: ReviewEntryView;
  query: string;
  actorId: string;
  runsByCase: Map<string, WorkbenchRun[]>;
  onViewChange: (view: ReviewEntryView) => void;
  onQueryChange: (query: string) => void;
}) {
  return (
    <section className="page review-entry-page">
      <ReviewEntryHeader />
      <ReviewEntrySummary snapshot={snapshot} />
      <ReviewEntryCases
        snapshot={snapshot}
        view={view}
        query={query}
        actorId={actorId}
        runsByCase={runsByCase}
        onViewChange={onViewChange}
        onQueryChange={onQueryChange}
      />
    </section>
  );
}

export function ReviewEntryPage() {
  const query = useWorkbench("week");
  const session = useSession();
  const [params, setParams] = useSearchParams();
  const view = parseReviewEntryView(params.get("view"));
  const search = params.get("q") ?? "";
  const runsByCase = useMemo(
    () => indexRunsByCase(query.data?.runs ?? []),
    [query.data?.runs],
  );

  function updateParam(key: "view" | "q", value: string) {
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (value) next.set(key, value);
        else next.delete(key);
        return next;
      },
      { replace: key === "q" },
    );
  }

  if (query.isLoading)
    return <PageStatus kind="loading" title="正在读取审查工作区案件" />;
  if (query.isError || !query.data)
    return (
      <PageStatus
        kind="error"
        title="审查工作区没有加载"
        detail={errorMessage(query.error)}
        action={
          <Button
            aria-label="重试"
            type="primary"
            htmlType="button"
            onClick={() => void query.refetch()}
          >
            重试
          </Button>
        }
      />
    );
  return (
    <ReviewEntryContent
      snapshot={query.data}
      view={view}
      query={search}
      actorId={session.data?.id ?? ""}
      runsByCase={runsByCase}
      onViewChange={(nextView) => updateParam("view", nextView === "all" ? "" : nextView)}
      onQueryChange={(nextQuery) => updateParam("q", nextQuery)}
    />
  );
}
