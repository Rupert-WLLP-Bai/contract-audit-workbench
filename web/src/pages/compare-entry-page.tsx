// File metadata (manifest mirror):
// Purpose: Resolves the selected case and comparison target, then routes into a side-by-side comparison of persisted review runs without creating a new review.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-11-08
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import { Button, Input, Table, type TableColumnsType } from "antd";
import { useState } from "react";
import { Link } from "react-router";
import { errorMessage, isForbidden, isUnauthenticated } from "../api/http";
import { useWorkbench } from "../api/queries";
import type { WorkbenchCase, WorkbenchRun } from "../api/types";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import { TechnicalDetail } from "../components/technical-detail";
import { InvalidTimeDetail } from "../components/invalid-time-detail";
import { caseIdentityDetails, caseSearchText } from "../domain/case-identity";
import {
  businessStatusLabel,
  businessStatusTone,
  formatDateTime,
  latestRun,
  runLabel,
  runTone,
} from "../domain/review";
import "./compare-entry-page.css";

export function CompareEntryPage() {
  const query = useWorkbench("week");
  const [search, setSearch] = useState("");
  if (query.isLoading)
    return <PageStatus kind="loading" title="正在读取案件列表" />;
  if (query.isError || !query.data)
    return (
      <PageStatus
        kind={isForbidden(query.error) || isUnauthenticated(query.error) ? "denied" : "error"}
        title={isForbidden(query.error) || isUnauthenticated(query.error) ? "没有权限读取案件列表" : "版本对照数据没有加载"}
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
  const needle = search.trim().toLowerCase();
  const visibleCases = snapshot.cases.filter((item) => caseSearchText(item).includes(needle));
  const columns: TableColumnsType<WorkbenchCase> = [
    {
      title: "合同",
      key: "case",
      render: (_, item) => (
        <div className="compare-entry-case">
          <strong>{item.title}</strong>
          <span>{caseIdentityDetails(item, snapshot.members)}</span>
          <TechnicalDetail value={item.id} label="案件标识" />
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
          <TechnicalDetail value={item.business_status} label="业务状态编码" />
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
          return <span className="compare-entry-muted">本周未返回运行记录</span>;
        return (
          <div className="compare-entry-run">
            <TechnicalDetail value={latest.id} label="运行标识" />
            <StatusText tone={runTone(latest.state)}>
              {runLabel(latest.state)}
            </StatusText>
            <TechnicalDetail value={latest.state} label="运行状态编码" />
            <span className="compare-entry-muted">
              本周返回 {runCount} 次运行
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
        <div><time dateTime={item.updated_at}>{formatDateTime(item.updated_at)}</time>
          <InvalidTimeDetail value={item.updated_at} label="案件更新时间" />
        </div>
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
              选择审查
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
          <p>先选择同一案件及两次明确的冻结执行，再进入结果对照。</p>
        </div>
        <Button aria-label={"案件列表"} type="text" href="/cases">
          案件列表
        </Button>
      </header>
      <ol className="compare-entry-summary" aria-label="版本对照摘要">
        <li data-state={snapshot.cases.length > 0 ? "filled" : "empty"}>
          <span className="compare-entry-summary-mark" aria-hidden="true" />
          <span>本周返回案件</span>
          <strong>{snapshot.cases.length}</strong>
        </li>
        <li data-state={snapshot.runs.length > 0 ? "filled" : "empty"}>
          <span className="compare-entry-summary-mark" aria-hidden="true" />
          <span>本周返回运行</span>
          <strong>{snapshot.runs.length}</strong>
        </li>
      </ol>
      <p className="compare-entry-note">本周返回的运行数量不能确认冻结执行或可比性；请进入案件选择并核对两次执行。</p>
      {snapshot.cases.length === 0 ? (
        <PageStatus
          kind="empty"
          title="本周未返回案件"
          detail="列表按当前服务端授权和工作台数据范围返回。"
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
                placeholder="案件标题、业务编号、中文场景或完整案件标识"
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
