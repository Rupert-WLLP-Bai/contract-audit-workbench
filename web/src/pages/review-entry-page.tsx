import { Button, Table, type TableColumnsType } from "antd";
import { errorMessage } from "../api/http";
import { useWorkbench } from "../api/queries";
import type { WorkbenchCase, WorkbenchRun } from "../api/types";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import {
  businessStatusLabel,
  businessStatusTone,
  formatDateTime,
  runLabel,
  runTone,
} from "../domain/review";
import "./review-entry-page.css";

function latestRun(runs: WorkbenchRun[]): WorkbenchRun | null {
  return (
    [...runs].sort(
      (left, right) =>
        right.updated_at.localeCompare(left.updated_at) ||
        right.id.localeCompare(left.id),
    )[0] ?? null
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

  const snapshot = query.data;
  const runsByCase = new Map<string, WorkbenchRun[]>();
  for (const run of snapshot.runs) {
    const runs = runsByCase.get(run.case_id) ?? [];
    runs.push(run);
    runsByCase.set(run.case_id, runs);
  }
  const columns: TableColumnsType<WorkbenchCase> = [
    {
      title: "合同",
      key: "case",
      render: (_, item) => (
        <div className="review-entry-case">
          <strong>{item.title}</strong>
          <span>
            {item.profile?.customer ??
              item.business_line ??
              "业务信息未报告"}
          </span>
        </div>
      ),
    },
    {
      title: "状态",
      key: "status",
      width: 250,
      render: (_, item) => (
        <div className="review-entry-status">
          <StatusText tone={businessStatusTone(item.business_status ?? "")}>
            {businessStatusLabel(item.business_status ?? "")}
          </StatusText>
          <code>{item.id}</code>
        </div>
      ),
    },
    {
      title: "最近运行",
      key: "run",
      width: 235,
      render: (_, item) => {
        const latest = latestRun(runsByCase.get(item.id) ?? []);
        if (!latest)
          return <span className="review-entry-muted">尚无审查运行</span>;
        return (
          <div className="review-entry-run-cell">
            <code>{latest.id}</code>
            <StatusText tone={runTone(latest.state)}>
              {runLabel(latest.state)}
            </StatusText>
            <span className="review-entry-muted">
              {latest.attempt_id
                ? `尝试 ${latest.attempt_id}`
                : "未提供执行记录"}
            </span>
          </div>
        );
      },
    },
    {
      title: "更新时间",
      key: "updated",
      width: 165,
      render: (_, item) => (
        <time dateTime={item.updated_at}>
          {formatDateTime(item.updated_at)}
        </time>
      ),
    },
    {
      title: "操作",
      key: "actions",
      width: 200,
      align: "right",
      render: (_, item) => {
        const latest = latestRun(runsByCase.get(item.id) ?? []);
        return (
          <div className="review-entry-actions">
            {latest ? (
              <Button
                aria-label={"打开审查"}
                type="primary"
                href={`/cases/${encodeURIComponent(item.id)}/reviews/${encodeURIComponent(latest.id)}${latest.attempt_id ? `?attempt_id=${encodeURIComponent(latest.attempt_id)}` : ""}`}
              >
                打开审查
              </Button>
            ) : null}
            <Button
              aria-label={String(latest ? "案件详情" : "进入案件")}
              href={`/cases/${encodeURIComponent(item.id)}`}
            >
              {latest ? "案件详情" : "进入案件"}
            </Button>
          </div>
        );
      },
    },
  ];

  return (
    <section className="page review-entry-page">
      <header className="page-head">
        <div>
          <h1>合同审查</h1>
          <p>选择有权查看的案件，查看已有审查记录或发起新审查。</p>
        </div>
        <Button aria-label={"案件列表"} type="text" href="/cases">
          案件列表
        </Button>
      </header>
      <ol className="review-entry-summary" aria-label="审查工作区摘要">
        <li data-state={snapshot.cases.length > 0 ? "filled" : "empty"}>
          <span className="review-entry-summary-mark" aria-hidden="true" />
          <span>可见案件</span>
          <strong>{snapshot.cases.length}</strong>
        </li>
        <li data-state={snapshot.runs.length > 0 ? "filled" : "empty"}>
          <span className="review-entry-summary-mark" aria-hidden="true" />
          <span>本周运行</span>
          <strong>{snapshot.runs.length}</strong>
        </li>
        <li
          data-state={
            snapshot.dashboard.pending_review_cases > 0 ? "open" : "filled"
          }
        >
          <span className="review-entry-summary-mark" aria-hidden="true" />
          <span>待人工处理</span>
          <strong>{snapshot.dashboard.pending_review_cases}</strong>
        </li>
      </ol>
      {snapshot.cases.length === 0 ? (
        <PageStatus
          kind="empty"
          title="当前身份没有可访问案件"
          detail="案件列表按当前服务端授权返回。"
        />
      ) : (
        <div className="review-entry-table">
          <Table<WorkbenchCase>
            rowKey="id"
            columns={columns}
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
      )}
    </section>
  );
}
