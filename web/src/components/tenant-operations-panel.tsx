import { Button, Table, type TableProps } from "antd";
import { errorMessage } from "../api/http";
import { useTenantOperations } from "../api/tenant-operations";
import "./tenant-operations-panel.css";

type BudgetRow = { key: string; label: string; used: number; limit: number | null; globalLimit: number | null };
const columns: TableProps<BudgetRow>["columns"] = [
  { title: "指标", dataIndex: "label", rowScope: "row" },
  { title: "当前租户用量", dataIndex: "used", render: (value: number) => value.toLocaleString() },
  { title: "租户上限", dataIndex: "limit", render: (value: number | null) => value === null ? "未取得" : value.toLocaleString() },
  { title: "租户余量", key: "remaining", render: (_, row) => row.limit === null ? "无法计算" : Math.max(0, row.limit - row.used).toLocaleString() },
  { title: "全局上限", dataIndex: "globalLimit", render: (value: number | null) => value === null ? "未取得" : value.toLocaleString() },
];

export function TenantOperationsPanel() {
  const query = useTenantOperations();
  const data = query.isError ? undefined : query.data;
  return (
    <section className="settings-panel tenant-operations-panel" aria-labelledby="tenant-operations-title">
      <header className="settings-panel-head">
        <h2 id="tenant-operations-title">身份目录与租户模型预算</h2>
        <Button loading={query.isFetching} onClick={() => void query.refetch()}>刷新运营状态</Button>
      </header>
      {query.isError ? <p role="alert">运营状态没有加载：{errorMessage(query.error)}</p> : null}
      {!data && query.isLoading ? <p role="status">正在读取目录状态和当前租户用量</p> : null}
      {data ? <>
        <dl className="settings-facts">
          <div><dt>当前租户</dt><dd>{data.tenant_id}</dd></div>
          <div data-tone={data.directory.state === "ready" ? undefined : "attention"}>
            <dt>身份目录</dt><dd>{data.directory.state === "ready" ? "企业目录已生效" : data.directory.state === "local_demo" ? "本地演示身份" : "目录不可用，当前授权已拒绝"}</dd>
          </div>
          <div><dt>本副本目录版本</dt><dd className="is-code">{data.directory.revision || "没有生效的企业目录版本"}</dd></div>
          {data.directory.checked_at ? <div><dt>最近本地检查</dt><dd>{new Date(data.directory.checked_at).toLocaleString()}</dd></div> : null}
          {data.budget ? <div><dt>网关预算版本</dt><dd className="is-code">{data.budget.revision}</dd></div> : null}
          <div><dt>用量观测时间</dt><dd>{new Date(data.usage.observed_at).toLocaleString()}</dd></div>
        </dl>
        {data.directory.mode === "enterprise" ? <p>本地文件在身份查询时最多每 {data.directory.local_verification_interval_ms / 1000} 秒核验内容；跨副本撤权还取决于各副本挂载更新的到达时间。</p> : null}
        <p>登录失败限流按服务进程生效。多副本共享防护由入口或企业身份服务提供。</p>
        {data.budget_state !== "ready" ? <p role="status">{data.budget_state === "unconfigured" ? "尚未配置模型网关状态读取地址。" : "暂时无法读取模型网关的实际配额。"}本页继续显示账本用量，上限和余量保持未知。</p> : null}
        {data.budget?.mode === "fixture" ? <p>当前网关使用合成模型响应；此处用量用于演示预算链路。</p> : null}
        <Table<BudgetRow>
          rowKey="key" columns={columns} pagination={false} size="small" scroll={{ x: 640 }}
          dataSource={[
            { key: "active", label: "进行中的调用", used: data.usage.active_calls, limit: data.budget?.tenant.max_in_flight ?? null, globalLimit: data.budget?.global.max_in_flight ?? null },
            { key: "calls", label: "最近 60 秒调用", used: data.usage.window_calls, limit: data.budget?.tenant.calls_per_minute ?? null, globalLimit: data.budget?.global.calls_per_minute ?? null },
            { key: "tokens", label: "最近 60 秒预算 token", used: data.usage.charged_tokens + data.usage.reserved_tokens, limit: data.budget?.tenant.tokens_per_minute ?? null, globalLimit: data.budget?.global.tokens_per_minute ?? null },
          ]}
        />
        <p>预算 token 包含已结算计费与在途预留；最近 60 秒有 {data.usage.unknown_usage_calls.toLocaleString()} 次调用未报告实际用量，按保守上限计费。明确未发出的调用已释放预算。</p>
        <p>全局预算同时约束所有租户。本页余量是当前租户的观测快照，受理以服务端账本为准；超配额返回 HTTP 429。</p>
      </> : null}
    </section>
  );
}
