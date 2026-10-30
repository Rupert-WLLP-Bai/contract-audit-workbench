import { Button, Input, Table, type TableColumnsType } from "antd";
import { useState } from "react";
import { Link } from "react-router";
import { errorMessage } from "../api/http";
import { useWorkbench } from "../api/queries";
import type { WorkbenchCase, WorkbenchRun } from "../api/types";
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
import "./compare-entry-page.css";

function customerLabel(item: WorkbenchCase): string {
  const customer = item.profile?.customer;
  if (customer && customer !== "unspecified") return customer;
  const businessLine = item.business_line;
  if (businessLine && businessLine !== "unspecified") return businessLine;
  return "业务信息未报告";
}

export function CompareEntryPage() {
  const query = useWorkbench("week");
  const [search, setSearch] = useState("");
  if (query.isLoading)
    return <PageStatus kind="loading" title="正在读取可对照的合同案件" />;
  if (query.isError || !query.data)
    return (
      <PageStatus
        kind="error"
        title="版本对照数据没有加载"
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
  const compareReady = snapshot.cases.filter(
    (item) => (runsByCase.get(item.id)?.length ?? 0) >= 2,
  ).length;
  const needle = search.trim().toLowerCase();
  const visibleCases = snapshot.cases.filter((item) =>
    [
      item.title,
      item.id,
      item.profile?.number ?? "",
      item.profile?.customer ?? "",
      item.business_line ?? "",
    ]
      .join(" ")
      .toLowerCase()
      .includes(needle),
  );
  const columns: TableColumnsType<WorkbenchCase> = [
    {
      title: "合同",
      key: "case",
      render: (_, item) => (
        <div className="compare-entry-case">
          <strong>{item.title}</strong>
          <span>{customerLabel(item)}</span>
        </div>
      ),
    },
    {
      title: "状态",
      key: "status",
      width: 220,
      render: (_, item) => (
        <div className="compare-entry-status">
          <StatusText tone={businessStatusTone(item.business_status ?? "")}>
            {businessStatusLabel(item.business_status ?? "")}
          </StatusText>
          <code>{item.profile?.number ?? item.id}</code>
        </div>
      ),
    },
    {
      title: "最近运行",
      key: "run",
      width: 220,
      render: (_, item) => {
        const latest = latestRun(runsByCase.get(item.id) ?? []);
        const runCount = runsByCase.get(item.id)?.length ?? 0;
        if (!latest)
          return <span className="compare-entry-muted">尚无审查运行</span>;
        return (
          <div className="compare-entry-run">
            <code>{latest.id}</code>
            <StatusText tone={runTone(latest.state)}>
              {runLabel(latest.state)}
            </StatusText>
            <span className="compare-entry-muted">
              {runCount >= 2 ? `${runCount} 次运行可对照` : "仅 1 次运行"}
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
      width: 170,
      align: "right",
      render: (_, item) => {
        const latest = latestRun(runsByCase.get(item.id) ?? []);
        return (
          <div className="compare-entry-actions">
            <Link to={`/compare?case_id=${encodeURIComponent(item.id)}`}>
              开始对照
            </Link>
            <Link
              to={
                latest
                  ? `/cases/${encodeURIComponent(item.id)}/reviews/${encodeURIComponent(latest.id)}${latest.attempt_id ? `?attempt_id=${encodeURIComponent(latest.attempt_id)}` : ""}`
                  : `/cases/${encodeURIComponent(item.id)}`
              }
            >
              {latest ? "打开审查" : "案件详情"}
            </Link>
          </div>
        );
      },
    },
  ];

  return (
    <section className="page compare-entry-page">
      <header className="page-head">
        <div>
          <h1>版本对照</h1>
          <p>先选择合同，再对照两次审查使用的材料、修改内容和发现的问题。</p>
        </div>
        <Button aria-label={"案件列表"} type="text" href="/cases">
          案件列表
        </Button>
      </header>
      <ol className="compare-entry-summary" aria-label="版本对照摘要">
        <li data-state={snapshot.cases.length > 0 ? "filled" : "empty"}>
          <span className="compare-entry-summary-mark" aria-hidden="true" />
          <span>可见合同</span>
          <strong>{snapshot.cases.length}</strong>
        </li>
        <li data-state={snapshot.runs.length > 0 ? "filled" : "empty"}>
          <span className="compare-entry-summary-mark" aria-hidden="true" />
          <span>本周运行</span>
          <strong>{snapshot.runs.length}</strong>
        </li>
        <li data-state={compareReady > 0 ? "filled" : "open"}>
          <span className="compare-entry-summary-mark" aria-hidden="true" />
          <span>可对照合同</span>
          <strong>{compareReady}</strong>
        </li>
      </ol>
      {snapshot.cases.length === 0 ? (
        <PageStatus
          kind="empty"
          title="当前身份没有可访问案件"
          detail="案件列表按当前服务端授权返回。"
        />
      ) : (
        <>
          <div className="compare-entry-toolbar">
            <label>
              搜索合同
              <Input
                type="search"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="合同名称、编号、客户"
              />
            </label>
            <span>共 {visibleCases.length} 份合同</span>
          </div>
          <div className="compare-entry-table">
            <Table<WorkbenchCase>
              rowKey="id"
              columns={columns}
              dataSource={visibleCases}
              size="middle"
              tableLayout="fixed"
              scroll={{ x: 960 }}
              locale={{ emptyText: "没有匹配的合同" }}
              pagination={{
                defaultPageSize: 10,
                pageSizeOptions: [10, 20, 50],
                showSizeChanger: true,
                showTotal: (total) => `共 ${total} 份合同`,
              }}
            />
          </div>
        </>
      )}
    </section>
  );
}
