// File metadata (manifest mirror):
// Purpose: 实现 `PROJECTION_LABEL`, `PROJECTION_TEXT_COLOR`, `TENANT_STATUS_LABEL`, `TENANT_STATUS_COLOR` 等界面组件或状态处理，呈现服务端返回的业务数据并提交受控用户操作。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useState } from "react";
import { Alert, Button, Card, Descriptions, Empty, Input, Space, Statistic, Table, Tag, type TableProps } from "antd";
import { errorMessage, isForbidden, isUnauthenticated } from "../api/http";
import { useSession } from "../api/queries";
import { useJurisdictionOperations, type JurisdictionOperationsSummary, type OperationsMetricSummary, type TenantOperationsSummary } from "../api/jurisdiction-operations";
import { PageStatus } from "../components/page-status";
import "./jurisdiction-operations-page.css";

const PROJECTION_LABEL = { current: "当前快照", stale: "快照待刷新", rebuilding: "重算中" } as const;
const PROJECTION_TEXT_COLOR = { current: "var(--state-compatible)", stale: "var(--state-insufficient)", rebuilding: "var(--state-progress)" } as const;
const TENANT_STATUS_LABEL = { active: "启用", suspended: "已暂停", closed: "已关闭" } as const;
const TENANT_STATUS_COLOR = { active: "success", suspended: "warning", closed: "default" } as const;
function utcToday() { return new Date().toISOString().slice(0, 10); }
function isRealDate(value: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const parsed = new Date(`${value}T00:00:00Z`);
  return Number.isFinite(parsed.getTime()) && parsed.toISOString().slice(0, 10) === value;
}
function formatInteger(value: string | null): string {
  if (value === null) return "—";
  return BigInt(value).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}
function formatUsdMinor(value: string | null, known: boolean): string {
  if (!known) return "未知";
  if (value === null) return "—";
  const amount = BigInt(value);
  const whole = (amount / 1_000_000n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `USD ${whole}.${(amount % 1_000_000n).toString().padStart(6, "0")}`;
}
function settledCost(metrics: OperationsMetricSummary) {
  return metrics.money === null ? "—" : formatUsdMinor(metrics.money.settled_minor, metrics.money.known);
}
function unknownHold(metrics: OperationsMetricSummary) {
  if (metrics.money === null) return "—";
  return metrics.money.unknown_hold_minor === null ? "未知" : formatUsdMinor(metrics.money.unknown_hold_minor, true);
}
function formatInstant(value: string | null): string { return value ? `${new Date(value).toLocaleString("zh-CN", { timeZone: "UTC" })} UTC` : "未提供"; }

type MetricRow = { key: string; name: string; tenantId: string | null; status: TenantOperationsSummary["status"] | null; metrics: OperationsMetricSummary; isTotal: boolean };
function metricRows(jurisdiction: JurisdictionOperationsSummary): MetricRow[] {
  return [
    { key: `totals:${jurisdiction.jurisdiction_id}`, name: "授权范围合计", tenantId: null, status: null, metrics: jurisdiction.totals, isTotal: true },
    ...jurisdiction.tenants.map((tenant) => ({
      key: tenant.tenant_id, name: tenant.tenant_name || tenant.tenant_id, tenantId: tenant.tenant_id,
      status: tenant.status, metrics: tenant.metrics, isTotal: false,
    })),
  ];
}
function JurisdictionCard({ jurisdiction }: { jurisdiction: JurisdictionOperationsSummary }) {
  const columns: TableProps<MetricRow>["columns"] = [
    { title: "范围", dataIndex: "name", fixed: "left", width: 190, render: (value: string, row) => <><strong>{value}</strong>{row.tenantId ? <><br /><small>{row.tenantId}</small></> : null}</> },
    { title: "状态", dataIndex: "status", width: 100, render: (value: MetricRow["status"]) => value ? <Tag color={TENANT_STATUS_COLOR[value]}>{TENANT_STATUS_LABEL[value]}</Tag> : "—" },
    { title: "新建案件", key: "cases", width: 120, render: (_: unknown, row) => formatInteger(row.metrics.cases_created) },
    { title: "完成 run", key: "runs", width: 120, render: (_: unknown, row) => formatInteger(row.metrics.runs_completed) },
    { title: "完成 attempt", key: "attempts", width: 135, render: (_: unknown, row) => formatInteger(row.metrics.attempts_completed) },
    { title: "已结算费用", key: "settled", width: 180, render: (_: unknown, row) => settledCost(row.metrics) },
    { title: "未知费用预留", key: "unknown-hold", width: 170, render: (_: unknown, row) => unknownHold(row.metrics) },
    { title: "逻辑额度调用（所选 UTC 日期范围）", key: "quota-calls", width: 205, render: (_: unknown, row) => formatInteger(row.metrics.logical_quota?.calls ?? null) },
    { title: "预留 token", key: "reserved-tokens", width: 130, render: (_: unknown, row) => formatInteger(row.metrics.logical_quota?.reserved_tokens ?? null) },
    { title: "用量未知调用", key: "unknown-calls", width: 140, render: (_: unknown, row) => formatInteger(row.metrics.logical_quota?.unknown_usage_calls ?? null) },
    { title: "共享池归属", key: "pool-attribution", width: 170, render: (_: unknown, row) => row.metrics.shared_pool === null ? "—" : row.metrics.shared_pool.attribution === "unattributed_shared" ? "共享资源 · 未归属租户" : row.metrics.shared_pool.attribution },
    { title: "共享池容量", key: "pool-capacity", width: 120, render: (_: unknown, row) => formatInteger(row.metrics.shared_pool?.capacity ?? null) },
    { title: "共享池使用中", key: "pool-in-use", width: 130, render: (_: unknown, row) => formatInteger(row.metrics.shared_pool?.in_use ?? null) },
  ];
  return <Card className="jurisdiction-operations-card" title={<span>辖区 <code>{jurisdiction.jurisdiction_id}</code></span>}>
    <Table<MetricRow>
      rowKey="key" size="small" pagination={false} scroll={{ x: 1900 }}
      columns={columns} dataSource={metricRows(jurisdiction)}
      rowClassName={(row) => row.isTotal ? "jurisdiction-total-row" : ""}
    />
  </Card>;
}
export function JurisdictionOperationsPage() {
  const session = useSession("admin");
  const [today] = useState(utcToday);
  const [fromDate, setFromDate] = useState(today);
  const [toDate, setToDate] = useState(today);
  const validRange = isRealDate(fromDate) && isRealDate(toDate) && fromDate <= toDate && Date.parse(`${toDate}T00:00:00Z`) - Date.parse(`${fromDate}T00:00:00Z`) <= 30 * 24 * 60 * 60 * 1000;
  const query = useJurisdictionOperations(fromDate, toDate, session.isSuccess && Boolean(session.data) && validRange);
  if (session.isLoading || session.isPending) return <PageStatus kind="loading" title="正在确认统计访问范围" />;
  if (session.isError || !session.data) return <PageStatus kind={isUnauthenticated(session.error) ? "denied" : "error"} title="无法确认管理身份" detail={errorMessage(session.error)} />;
  if (!validRange) return <PageStatus kind="error" title="统计日期范围无效" detail="请选择真实日期，结束日期不早于开始日期，且最多包含 31 天。" />;
  if (query.isLoading) return <PageStatus kind="loading" title="正在读取已授权辖区汇总" />;
  if (query.isError && isForbidden(query.error)) return <PageStatus kind="denied" title="没有辖区运营统计权限" detail="辖区与指标范围由服务端按当前 membership 授权；页面不接收客户端租户清单。" />;
  if (query.isError || !query.data) return <PageStatus kind="error" title="辖区运营统计不可用" detail={errorMessage(query.error)} action={<Button onClick={() => void query.refetch()}>重试</Button>} />;
  const data = query.data;
  const stale = data.projection_state !== "current" || data.dirty_since !== null;
  return <section className="page jurisdiction-operations-page">
    <header className="page-head"><div><h1>辖区运营统计</h1><p>仅显示服务端授权的 jurisdiction、tenant 与 metric 汇总，不包含合同或案件明细。租户逻辑额度与共享池资源分列。</p></div></header>
    <section className="jurisdiction-summary" aria-label="投影状态概览">
      <Statistic title="投影状态" value={PROJECTION_LABEL[data.projection_state]} valueStyle={{ color: PROJECTION_TEXT_COLOR[data.projection_state] }} />
      <Statistic title="授权辖区" value={data.jurisdictions.length.toLocaleString()} />
      <Statistic title="查询窗口" value={`${data.from_date} 至 ${data.to_date}`} />
    </section>
    {stale ? <Alert className="jurisdiction-alert" type="warning" showIcon message="统计投影不是当前状态" description="页面按 PostgreSQL 持久快照提供汇总；重算期间可能落后，请结合构建时间和 dirty 标记判断。" /> : null}
    <Card className="jurisdiction-projection-card" title="快照与重算信息" extra={<Button onClick={() => void query.refetch()}>刷新</Button>}>
      <Descriptions size="small" column={{ xs: 1, md: 3 }}>
        <Descriptions.Item label="PG 观测时刻">{formatInstant(data.observed_at)}</Descriptions.Item>
        <Descriptions.Item label="watermark 类型">{data.watermark_kind === "pg_snapshot" ? "PG 持久快照" : "未知"}</Descriptions.Item>
        <Descriptions.Item label="事件序号 watermark">{data.watermark ?? "没有可证明的事件序号"}</Descriptions.Item>
        <Descriptions.Item label="来源快照版本"><code>{data.source_snapshot_token ?? "未提供"}</code></Descriptions.Item>
        <Descriptions.Item label="投影构建时间">{formatInstant(data.built_at)}</Descriptions.Item>
        <Descriptions.Item label="开始变脏时间">{formatInstant(data.dirty_since)}</Descriptions.Item>
        <Descriptions.Item label="投影年龄（秒）">{data.projection_age_seconds === null ? "未知" : data.projection_age_seconds.toLocaleString()}</Descriptions.Item>
      </Descriptions>
    </Card>
    <Card className="jurisdiction-filter-card" title="统计窗口">
      <Space wrap>
        <label>开始日期 <Input type="date" value={fromDate} onChange={(event) => setFromDate(event.target.value)} /></label>
        <label>结束日期 <Input type="date" value={toDate} onChange={(event) => setToDate(event.target.value)} /></label>
        <Button onClick={() => void query.refetch()} disabled={!validRange}>查询</Button>
      </Space>
      <p className="meta">日期范围含首尾两日，最多 31 天。服务端按当前授权过滤辖区和指标；客户端不提交 jurisdiction 或 tenant ID 列表。</p>
    </Card>
    {data.jurisdictions.length === 0 ? <Card className="jurisdiction-operations-card"><Empty description="当前授权范围内没有可展示的辖区汇总。" /></Card> : data.jurisdictions.map((jurisdiction) => <JurisdictionCard key={jurisdiction.jurisdiction_id} jurisdiction={jurisdiction} />)}
  </section>;
}
