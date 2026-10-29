import { Button, Table, type TableColumnsType } from "antd";
import { errorMessage } from "../api/http";
import { useWorkbench } from "../api/queries";
import type { WorkbenchCase, WorkbenchRun, WorkbenchSnapshot } from "../api/types";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import {
  businessStatusLabel,
  businessStatusTone,
  formatDateTime,
  latestRun,
  runLabel,
  runTone,
} from "../domain/review";
import "./review-entry-page.css";

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

function ReviewEntryCaseCell({ item }: { item: WorkbenchCase }) {
  return (
    <div className="review-entry-case">
      <strong>{item.title}</strong>
      <span>
        {item.profile?.customer ?? item.business_line ?? "业务信息未报告"}
      </span>
    </div>
  );
}

function ReviewEntryStatusCell({ item }: { item: WorkbenchCase }) {
  return (
    <div className="review-entry-status">
      <StatusText tone={businessStatusTone(item.business_status ?? "")}>
        {businessStatusLabel(item.business_status ?? "")}
      </StatusText>
      <code>{item.id}</code>
    </div>
  );
}

function ReviewEntryRunCell({ run }: { run: WorkbenchRun | null }) {
  if (!run) return <span className="review-entry-muted">尚无审查运行</span>;
  return (
    <div className="review-entry-run-cell">
      <code>{run.id}</code>
      <StatusText tone={runTone(run.state)}>{runLabel(run.state)}</StatusText>
      <span className="review-entry-muted">
        {run.attempt_id ? `尝试 ${run.attempt_id}` : "未提供执行记录"}
      </span>
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
          aria-label={"打开审查"}
          type="primary"
          href={`/cases/${encodeURIComponent(item.id)}/reviews/${encodeURIComponent(run.id)}${run.attempt_id ? `?attempt_id=${encodeURIComponent(run.attempt_id)}` : ""}`}
        >
          打开审查
        </Button>
      ) : null}
      <Button
        aria-label={String(run ? "案件详情" : "进入案件")}
        href={`/cases/${encodeURIComponent(item.id)}`}
      >
        {run ? "案件详情" : "进入案件"}
      </Button>
    </div>
  );
}

function reviewEntryColumns(
  runsByCase: Map<string, WorkbenchRun[]>,
): TableColumnsType<WorkbenchCase> {
  return [
    {
      title: "合同",
      key: "case",
      render: (_, item) => <ReviewEntryCaseCell item={item} />,
    },
    {
      title: "状态",
      key: "status",
      width: 250,
      render: (_, item) => <ReviewEntryStatusCell item={item} />,
    },
    {
      title: "最近运行",
      key: "run",
      width: 235,
      render: (_, item) => (
        <ReviewEntryRunCell
          run={latestRunForCase(runsByCase, item.id)}
        />
      ),
    },
    {
      title: "更新时间",
      key: "updated",
      width: 165,
      render: (_, item) => (
        <time dateTime={item.updated_at}>{formatDateTime(item.updated_at)}</time>
      ),
    },
    {
      title: "操作",
      key: "actions",
      width: 200,
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
    <header className="page-head">
      <div>
        <h1>合同审查</h1>
        <p>选择有权查看的案件，查看已有审查记录或发起新审查。</p>
      </div>
      <Button aria-label={"案件列表"} type="text" href="/cases">
        案件列表
      </Button>
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

function ReviewEntryCases({ snapshot }: { snapshot: WorkbenchSnapshot }) {
  if (snapshot.cases.length === 0)
    return (
      <PageStatus
        kind="empty"
        title="当前身份没有可访问案件"
        detail="案件列表按当前服务端授权返回。"
      />
    );

  const runsByCase = indexRunsByCase(snapshot.runs);
  return (
    <div className="review-entry-table">
      <Table<WorkbenchCase>
        rowKey="id"
        columns={reviewEntryColumns(runsByCase)}
        dataSource={snapshot.cases}
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
  );
}

function ReviewEntryContent({ snapshot }: { snapshot: WorkbenchSnapshot }) {
  return (
    <section className="page review-entry-page">
      <ReviewEntryHeader />
      <ReviewEntrySummary snapshot={snapshot} />
      <ReviewEntryCases snapshot={snapshot} />
    </section>
  );
}

export function ReviewEntryPage() {
  const query = useWorkbench("week");
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
            aria-label={"重试"}
            type="primary"
            htmlType="button"
            onClick={() => void query.refetch()}
          >
            重试
          </Button>
        }
      />
    );
  return <ReviewEntryContent snapshot={query.data} />;
}
