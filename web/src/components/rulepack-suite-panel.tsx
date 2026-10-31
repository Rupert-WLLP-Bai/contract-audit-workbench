import { Button, Select } from "antd";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { errorMessage } from "../api/http";
import { queryKeys } from "../api/queries";
import { listRulePacks, testRulePackFixtureSuite, type RulePackFixtureRun, type RulePackTestResult } from "../api/rulepacks";

function resultText(result?: RulePackTestResult): string {
  if (!result) return "无结果";
  return `${result.relation}（${result.reason_code}，规则 v${result.rule_version}）`;
}

function FixtureRun({ run }: { run: RulePackFixtureRun }) {
  return (
    <div aria-live="polite">
      <p>套件 {run.suite_id}：{run.passed ? "全部样本通过" : "存在未通过样本"}。规则包 {run.pack_id} v{run.pack_version}，内容摘要 <code>{run.pack_hash}</code></p>
      <ul className="rulepack-result-list">
        {run.cases.map((item) => (
          <li key={item.id}>
            <strong>{item.title}（{item.id}，{item.kind}）</strong>：{item.passed ? "通过" : "未通过"}
            {item.failures.length > 0 ? <ul>{item.failures.map((failure, index) => <li key={`${index}:${failure}`}>{failure}</li>)}</ul> : null}
            {item.report ? <details><summary>查看实际结论与引用</summary>
              {item.report.results.map((result) => <div key={result.rule_id}>
                <p>{result.rule_id}：{resultText(result)}</p>
                {result.citations?.map((citation, index) => <blockquote key={`${citation.source_id}:${citation.start}:${index}`}>
                  {citation.source_id} v{citation.source_version} [{citation.start},{citation.end})：{citation.quote}
                </blockquote>)}
              </div>)}
            </details> : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

export function RulePackSuitePanel({ packId, version, revision, contentHash, available }: {
  packId: string;
  version: string;
  revision: number;
  contentHash: string;
  available: boolean;
}) {
  const versions = useQuery({ queryKey: queryKeys.rulePackList, queryFn: ({ signal }) => listRulePacks(signal), enabled: available, retry: false });
  const [compareVersion, setCompareVersion] = useState("");
  const test = useMutation({ mutationFn: () => {
    const baseline = versions.data?.find((item) => item.id === packId && item.version === compareVersion);
    if (compareVersion && !baseline) throw new Error("对比版本已变化，请刷新版本列表后重试。");
    return testRulePackFixtureSuite(packId, version, {
      run_fixture_suite: true,
      expected_revision: revision,
      expected_content_hash: contentHash,
      ...(baseline ? { compare_version: baseline.version, compare_expected_revision: baseline.revision, compare_expected_hash: baseline.content_hash } : {}),
    });
  }, retry: false });
  useEffect(() => { test.reset(); setCompareVersion(""); }, [packId, version, revision, contentHash]);
  const options = (versions.data ?? []).filter((item) => item.id === packId && item.version !== version)
    .map((item) => ({ value: item.version, label: `v${item.version} · 修订 ${item.revision} · ${item.status}` }));
  return (
    <section className="workflow-panel" aria-labelledby="rulepack-suite-test-title">
      <h2 id="rulepack-suite-test-title">随包样本自检</h2>
      <p className="meta">仅运行已保存的合成套件，使用真实规则比较器及引用校验；不读取案件或生成审查结论。结果绑定当前修订与内容摘要。</p>
      {!available ? <p>当前版本没有样本套件。请在草稿中应用并保存套件。</p> : <>
        <label className="field" htmlFor="rulepack-suite-compare">对比版本（可选）
          <Select id="rulepack-suite-compare" value={compareVersion} onChange={setCompareVersion} disabled={test.isPending} options={[{ value: "", label: "不对比" }, ...options]} />
        </label>
        {versions.isError ? <p role="alert">无法读取版本列表：{errorMessage(versions.error)}</p> : null}
        <Button type="primary" htmlType="button" disabled={test.isPending || versions.isLoading} onClick={() => test.mutate()}>{test.isPending ? "正在执行套件" : "执行套件"}</Button>
      </>}
      {test.error ? <p role="alert">执行失败：{errorMessage(test.error)}</p> : null}
      {test.data ? <>
        <FixtureRun run={test.data.suite} />
        {test.data.comparison ? <section aria-label="版本差异">
          <h3>版本差异</h3>
          <p>对比 {test.data.comparison.baseline.pack_id} v{test.data.comparison.baseline.pack_version}，修订 {test.data.compare_revision}，内容摘要 <code>{test.data.comparison.baseline.pack_hash}</code></p>
          {test.data.comparison.changes.length === 0 ? <p>所运行样本的规则结果没有差异。</p> : <ul>
            {test.data.comparison.changes.map((change, index) => <li key={`${change.case_id}:${change.rule_id ?? "execution"}:${index}`}>
              <strong>{change.case_id}{change.rule_id ? ` · ${change.rule_id}` : " · 执行"}</strong>
              <p>对比版本：{change.before_error || resultText(change.before)}</p>
              <p>当前版本：{change.after_error || resultText(change.after)}</p>
              {(change.before?.citations?.length || change.after?.citations?.length) ? <details><summary>查看引用差异</summary>
                <p>对比版本：{change.before?.citations?.map((c) => `${c.source_id} v${c.source_version} [${c.start},${c.end}) ${c.quote}`).join("；") || "无引用"}</p>
                <p>当前版本：{change.after?.citations?.map((c) => `${c.source_id} v${c.source_version} [${c.start},${c.end}) ${c.quote}`).join("；") || "无引用"}</p>
              </details> : null}
            </li>)}
          </ul>}
        </section> : null}
      </> : null}
    </section>
  );
}
