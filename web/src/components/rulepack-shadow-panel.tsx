import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "antd";
import { useEffect, useState } from "react";
import { errorMessage } from "../api/http";
import { createShadowReport, getShadowReport, listShadowReports, listShadowSamples, type ShadowEnvelope, type ShadowSampleOption, type ShadowSampleRef } from "../api/rulepack-shadow";

function sampleKey(sample: ShadowSampleRef): string {
  return JSON.stringify([sample.case_id, sample.run_id, sample.attempt_id]);
}

const statusText = { completed: "已完成", partial: "部分不可比", failed: "未完成" } as const;
const changeText = { changed: "结论变化", removed: "规则移除", unavailable: "无法比较" } as const;
const relationText: Record<string, string> = { compatible: "符合", conflict: "冲突", insufficient: "证据不足", not_applicable: "不适用" };

function ShadowReportView({ value, currentHash }: { value: ShadowEnvelope; currentHash: string }) {
  const { report } = value;
  return (
    <div aria-live="polite">
      <h3>对比报告 {report.id}</h3>
      <p>完成 {report.completed} 条，部分不可比 {report.partial} 条，未完成 {report.failed} 条。</p>
      {report.candidate.content_sha256 !== currentHash ? <p className="workflow-error" role="alert">此报告针对的是旧内容摘要，请按当前版本重新对比。</p> : null}
      <p className="meta">候选 {report.candidate.id} v{report.candidate.version}，修订 {report.candidate.revision}；报告由 {report.created_by} 于 <time dateTime={report.created_at}>{report.created_at}</time> 创建。</p>
      <p className="meta">候选内容摘要 <code className="digest">{report.candidate.content_sha256}</code></p>
      <p className="meta">候选保存版本摘要 <code className="digest">{report.candidate.snapshot_sha256}</code>；报告摘要 <code className="digest">{value.report_sha256}</code></p>
      <ol className="rulepack-history">
        {report.samples.map((sample) => (
          <li key={sampleKey(sample)}>
            <details>
              <summary>{statusText[sample.status]} · 案件 {sample.case_id} · 审查 {sample.run_id} · 执行 {sample.attempt_id}</summary>
              <p>{sample.message}{sample.error_code ? `（${sample.error_code}）` : ""}</p>
              {sample.base_pack_id ? <p className="meta">原规则包 {sample.base_pack_id} v{sample.base_pack_version}，提取器 {sample.extractor_ref}</p> : null}
              {sample.input_sha256 ? <p className="meta">输入 <code className="digest">{sample.input_sha256}</code>；输出 <code className="digest">{sample.output_sha256}</code></p> : null}
              {sample.knowledge_sha256 ? <p className="meta">冻结知识 <code className="digest">{sample.knowledge_sha256}</code></p> : null}
              {sample.rule_request_sha256 ? <p className="meta">冻结规则输入 <code className="digest">{sample.rule_request_sha256}</code></p> : null}
              {sample.base_pack_sha256 ? <p className="meta">原规则内容 <code className="digest">{sample.base_pack_sha256}</code>；原规则快照 <code className="digest">{sample.base_pack_snapshot_sha256}</code></p> : null}
              {sample.changes.length ? (
                <ul>
                  {sample.changes.map((change) => (
                    <li key={`${change.rule_id}:${change.kind}`}>
                      <strong>{change.rule_id} · {changeText[change.kind]}</strong>{" "}
                      {change.base_relation ? `${relationText[change.base_relation] ?? change.base_relation}（${change.base_reason}）` : "无原规则"}
                      {" → "}
                      {change.candidate_relation ? `${relationText[change.candidate_relation] ?? change.candidate_relation}（${change.candidate_reason}）` : "无候选规则"}
                      {change.detail ? `；${change.detail}` : ""}
                    </li>
                  ))}
                </ul>
              ) : sample.status === "completed" ? <p>可比较的规则没有结论变化。</p> : null}
            </details>
          </li>
        ))}
      </ol>
    </div>
  );
}

export function RulePackShadowPanel({ packId, version, revision, contentHash, enabled }: { packId: string; version: string; revision: number; contentHash: string; enabled: boolean }) {
  const queryClient = useQueryClient();
  const [samplesOpen, setSamplesOpen] = useState(false);
  const [reportsOpen, setReportsOpen] = useState(false);
  const [selected, setSelected] = useState<ShadowSampleRef[]>([]);
  const [reportID, setReportID] = useState("");
  const reportsKey = ["rule-shadow", packId, version, "reports"] as const;
  const samples = useQuery({ queryKey: ["rule-shadow", packId, version, revision, contentHash, "samples"], queryFn: ({ signal }) => listShadowSamples(packId, version, signal), enabled: samplesOpen && enabled, retry: false });
  const reports = useQuery({ queryKey: reportsKey, queryFn: ({ signal }) => listShadowReports(packId, version, signal), enabled: enabled && reportsOpen, retry: false });
  const report = useQuery({ queryKey: ["rule-shadow", packId, version, "report", reportID], queryFn: ({ signal }) => getShadowReport(packId, version, reportID, signal), enabled: enabled && reportID !== "", retry: false, staleTime: 0 });
  const create = useMutation({ mutationFn: () => createShadowReport(packId, version, revision, contentHash, selected), retry: false,
    onSuccess: (value) => { setReportsOpen(true); setReportID(value.report.id); void queryClient.invalidateQueries({ queryKey: reportsKey }); } });
  const max = samples.data?.selection_limit ?? 20;
  useEffect(() => {
    if (!samples.data || samples.isFetching || samples.isError) return;
    const available = new Set(samples.data.items.map(sampleKey));
    setSelected((previous) => {
      const remaining = previous.filter((item) => available.has(sampleKey(item)));
      return remaining.length === previous.length ? previous : remaining;
    });
  }, [samples.data, samples.isFetching, samples.isError]);
  function toggle(sample: ShadowSampleOption, checked: boolean) {
    const item = { case_id: sample.case_id, run_id: sample.run_id, attempt_id: sample.attempt_id };
    setSelected((previous) => checked ? [...previous, item] : previous.filter((entry) => sampleKey(entry) !== sampleKey(item)));
  }
  return (
    <section className="workflow-panel" id="rulepack-shadow" aria-labelledby="rulepack-shadow-title">
      <header className="workflow-panel-head"><div><h2 id="rulepack-shadow-title">近期审查影子对比</h2><p className="meta">使用已保存的真实规则输入和冻结审查结果；对比只作为人工批准的参考。</p></div></header>
      {!enabled ? <p>请先保存或刷新当前规则包，再选择样本。</p> : null}
      <div className="workflow-links">
        <Button htmlType="button" disabled={!enabled || create.isPending} onClick={() => { setSamplesOpen(true); if (samplesOpen) void samples.refetch(); }}>读取可选审查</Button>
        <Button htmlType="button" type="primary" loading={create.isPending} disabled={!enabled || samples.isFetching || samples.isError || selected.length === 0 || selected.length > max} onClick={() => create.mutate()}>对比所选审查</Button>
      </div>
      <p className="meta">最多选择 {max} 条；从最近至多 {samples.data?.scan_limit ?? 250} 条终态执行中查找当前可审查的案件。</p>
      {samplesOpen && samples.isFetching ? <p>正在读取可选审查。</p> : null}
      {samplesOpen && samples.isError ? <p className="workflow-error" role="alert">可选审查无法读取：{errorMessage(samples.error)}</p> : null}
      {samples.data && !samples.isFetching && !samples.isError && samples.data.items.length === 0 ? <p>近期没有当前身份可审查的匹配记录。</p> : null}
      {samples.data && !samples.isFetching && !samples.isError && samples.data.items.length > 0 ? (
        <fieldset><legend>选择审查样本（已选 {selected.length} 条）</legend>
          <ul className="rulepack-history">
            {samples.data.items.map((item) => {
              const checked = selected.some((entry) => sampleKey(entry) === sampleKey(item));
              return <li key={sampleKey(item)}><label><input type="checkbox" checked={checked} disabled={!enabled || create.isPending || (!checked && selected.length >= max)} onChange={(event) => toggle(item, event.target.checked)} /> 案件 {item.case_id} · 审查 {item.run_id} · 执行 {item.attempt_id} · <time dateTime={item.finished_at}>{item.finished_at}</time></label></li>;
            })}
          </ul>
        </fieldset>
      ) : null}
      {create.isError ? <p className="workflow-error" role="alert">影子对比未完成：{errorMessage(create.error)}</p> : null}
      <h3>已保存的对比</h3>
      <Button htmlType="button" disabled={!enabled} onClick={() => { setReportsOpen(true); if (reportsOpen) void reports.refetch(); }}>读取已保存报告</Button>
      {reports.isFetching ? <p>正在读取报告。</p> : null}
      {reports.isError ? <p className="workflow-error" role="alert">报告列表无法读取：{errorMessage(reports.error)}</p> : null}
      {!reports.isFetching && !reports.isError && reports.data?.length === 0 ? <p>还没有可读取的影子报告。</p> : null}
      {!reports.isFetching && !reports.isError && reports.data?.length ? <ul className="rulepack-history">{reports.data.map((item) => <li key={item.report.id}><Button htmlType="button" disabled={!enabled} onClick={() => { setReportID(item.report.id); if (reportID === item.report.id) void report.refetch(); }}>查看报告 {item.report.id}</Button> <time dateTime={item.report.created_at}>{item.report.created_at}</time></li>)}</ul> : null}
      {reportID && report.isFetching ? <p>正在检查报告及来源授权。</p> : null}
      {reportID && report.isError ? <p className="workflow-error" role="alert">报告无法读取：{errorMessage(report.error)}</p> : null}
      {report.data && !report.isFetching && !report.isError && !reports.isFetching && !reports.isError && reports.data?.some((item) => item.report.id === reportID) ? <ShadowReportView value={report.data} currentHash={contentHash} /> : null}
    </section>
  );
}
