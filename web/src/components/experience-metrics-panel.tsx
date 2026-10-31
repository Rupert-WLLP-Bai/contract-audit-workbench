import { Button, Table, type TableProps } from "antd";
import { useQuery } from "@tanstack/react-query";
import { getExperienceMetrics, type ExperienceMetricsSample, type ExperienceOutcome } from "../api/experience-metrics";
import type { KnowledgeVersion } from "../api/governed-knowledge";
import { errorMessage } from "../api/http";
import { formatDateTime } from "../domain/review";

const outcomeLabels: Record<ExperienceOutcome, string> = { pending: "待人工决定", adopted: "采纳", overturned: "推翻", other: "其他或方向未明确" };
const actionLabels: Record<string, string> = { "": "未明确", confirm_risk: "确认风险", false_positive: "误报", insufficient_evidence: "证据不足", accept_residual: "接受残余风险" };
function reviewUrl(item: ExperienceMetricsSample): string {
  const query = new URLSearchParams({ attempt_id: item.attempt_id, finding: item.finding_id });
  const path = `/cases/${encodeURIComponent(item.case_id)}/reviews/${encodeURIComponent(item.run_id)}?${query}`;
  const base = import.meta.env.VITE_CLIENT_URL;
  return base ? new URL(path, base).href : path;
}

export function ExperienceMetricsPanel({ version }: { version: KnowledgeVersion }) {
  const ref = version.ref;
  const eligible = version.status === "published" || version.status === "withdrawn";
  const metrics = useQuery({
    queryKey: ["admin", "governed-knowledge", "experience-metrics", ref.knowledge_id, ref.version_id, ref.content_sha256],
    queryFn: ({ signal }) => getExperienceMetrics(ref, signal), enabled: eligible, retry: false,
  });
  const columns: TableProps<ExperienceMetricsSample>["columns"] = [
    { title: "审查样本", key: "review", render: (_, item) => <>
      <a href={reviewUrl(item)}>案件 {item.case_id} · 查看审查</a>
      <div>执行 {item.attempt_id} · 发现 {item.finding_id}</div>
      <div className="meta">登记于 {formatDateTime(item.used_at)}</div>
    </> },
    { title: "人工判断", key: "outcome", render: (_, item) => <>
      <strong>{outcomeLabels[item.outcome]}</strong>
      <div>预期：{actionLabels[item.expected_action]}</div>
      <div>最新决定：{actionLabels[item.decision_action] ?? item.decision_action}{item.decision_id && `（${item.decision_id}）`}</div>
    </> },
    { title: "冻结身份", key: "identity", render: (_, item) => <>
      <div>输入：<code className="digest">{item.input_sha256}</code></div>
      <div>知识快照：<code className="digest">{item.snapshot_sha256}</code></div>
      <div>引用记录：<code className="digest">{item.usage_sha256}</code></div>
    </> },
  ];
  return <section className="rulepack-editor-section" aria-labelledby="experience-metrics-heading">
    <h2 id="experience-metrics-heading">经验使用与人工判断</h2>
    {!eligible ? <p>经验发布后可查看其精确版本的使用记录。</p> : <>
      <p className="meta">只统计审查员在该发现首次判断前明确登记的实际参考，按执行、经验版本和发现去重；只包含当前账号可读取的案件。历史检索不会补记为使用。</p>
      {metrics.isLoading && <p role="status">正在读取经验使用记录</p>}
      {metrics.isError && <p role="alert">经验使用记录没有加载：{errorMessage(metrics.error)} <Button htmlType="button" onClick={() => void metrics.refetch()}>重试</Button></p>}
      {metrics.data && !metrics.isError && <>
        <dl className="rulepack-summary-grid">
          <div><dt>实际参考次数</dt><dd>{metrics.data.usage_count}</dd></div>
          <div><dt>待人工决定</dt><dd>{metrics.data.pending_count}</dd></div>
          <div><dt>采纳</dt><dd>{metrics.data.adopted_count}</dd></div>
          <div><dt>推翻</dt><dd>{metrics.data.overturned_count}</dd></div>
          <div><dt>其他或方向未明确</dt><dd>{metrics.data.other_count}</dd></div>
        </dl>
        <p className="meta">采纳指最新人工决定与登记的明确预期方向一致，推翻指确认风险与误报之间的相反判断。证据不足、接受残余风险及未明确预期方向单独列示；未作决定不计入采纳或推翻。</p>
        {!metrics.data.has_been_published && <p>该版本没有发布记录。</p>}
        <h3>可追溯样本</h3>
        <p className="meta">显示最近 20 条当前可访问记录。根据样本人工审阅后，可考虑调整适用范围或撤回；指标不会自动改变治理状态。</p>
        <Table rowKey={(item) => `${item.attempt_id}:${item.finding_id}`} columns={columns} dataSource={metrics.data.samples}
          pagination={false} scroll={{ x: 900 }} locale={{ emptyText: "没有已登记的经验使用记录" }} />
      </>}
    </>}
  </section>;
}
