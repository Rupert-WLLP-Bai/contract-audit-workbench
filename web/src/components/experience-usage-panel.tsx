import { Button, Select } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { listExperienceUsage, recordExperienceUsage, type ExperienceDirection, type ExperienceUsageIdentity, type ExperienceUsageOption } from "../api/experience-usage";
import { errorMessage } from "../api/http";
import { formatDateTime } from "../domain/review";

const directionLabels: Record<ExperienceDirection, string> = {
  "": "仅作检查参考，方向未明确",
  confirm_risk: "参考后预期确认风险",
  false_positive: "参考后预期判为误报",
};

function ExperienceReference({ identity, item, canRecord, queryKey }: {
  identity: ExperienceUsageIdentity;
  item: ExperienceUsageOption;
  canRecord: boolean;
  queryKey: readonly unknown[];
}) {
  const fieldId = useId();
  const [direction, setDirection] = useState<ExperienceDirection | undefined>(undefined);
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (value: ExperienceDirection) => recordExperienceUsage(identity, item.ref, value),
    onSuccess: async () => { await client.invalidateQueries({ queryKey }); },
    onError: () => { void client.invalidateQueries({ queryKey }); },
    retry: false,
  });
  return <li>
    <h4>{item.title}</h4>
    <p className="meta">精确版本 {item.ref.version_id} · 内容摘要 {item.ref.content_sha256}</p>
    <p>{item.summary}</p>
    <details><summary>查看经验正文与适用条件</summary>
      <p style={{ whiteSpace: "pre-wrap" }}>{item.body}</p>
      <p>适用条件：{item.reviewed_applicability?.join("；") || "未记录"}</p>
      <p>排除条件：{item.reviewed_exclusions?.join("；") || "未记录"}</p>
      <p>检查动作：{item.check_action || "未记录"}</p>
      <p>最低证据：{item.minimum_evidence || "未记录"}</p>
      <p>反例：{item.counterexamples?.join("；") || "未记录"}</p>
    </details>
    {item.usage ? <p>已登记实际参考 · {directionLabels[item.usage.expected_action]} · {formatDateTime(item.usage.recorded_at)}</p> : canRecord ? <>
      <label className="field" htmlFor={fieldId}>这条经验对当前发现的预期方向
        <Select id={fieldId} style={{ width: "100%" }} value={direction} placeholder="请选择经验的预期方向"
          options={Object.entries(directionLabels).map(([value, label]) => ({ value, label }))}
          onChange={(value: ExperienceDirection) => setDirection(value)} disabled={mutation.isPending} />
      </label>
      <Button htmlType="button" onClick={() => { if (direction !== undefined) mutation.mutate(direction); }}
        loading={mutation.isPending} disabled={direction === undefined}>确认实际参考并登记</Button>
      {mutation.isError && <p role="alert">经验使用未能登记：{errorMessage(mutation.error)}</p>}
    </> : null}
  </li>;
}

export function ExperienceUsagePanel({ caseId, runId, attemptId, findingId, canDecide }: {
  caseId: string; runId: string; attemptId: string | null; findingId: string | null; canDecide: boolean;
}) {
  const identity = { caseId, runId, attemptId: attemptId ?? "", findingId: findingId ?? "" };
  const queryKey = ["experience-usage", caseId, runId, attemptId, findingId] as const;
  const options = useQuery({ queryKey, queryFn: ({ signal }) => listExperienceUsage(identity, signal),
    enabled: canDecide && Boolean(attemptId && findingId), retry: false });
  if (!canDecide || !attemptId || !findingId) return null;
  return <section className="workflow-panel" aria-labelledby="experience-usage-heading">
    <h2 id="experience-usage-heading">本次发现参考的经验</h2>
    <p className="meta">只登记实际参考过的冻结经验，并在首次人工判断前确认。候选出现或被检索不算使用；预期方向不会替代人工判断。</p>
    {options.isLoading && <p role="status">正在读取本次审查的经验版本</p>}
    {options.isError && <p role="alert">经验版本没有加载：{errorMessage(options.error)} <Button htmlType="button" onClick={() => void options.refetch()}>重试</Button></p>}
    {options.data && !options.isError && (options.data.items.length ? <ul>
      {options.data.items.map((item) => <ExperienceReference key={`${item.ref.knowledge_id}:${item.ref.version_id}`} identity={identity}
        item={item} canRecord={options.data.can_record && !options.isFetching} queryKey={queryKey} />)}
    </ul> : <p>本次审查没有当前可访问的经验版本。</p>)}
    {options.data && !options.data.can_record && <p className="meta">当前发现已经有人工判断，或审查状态不允许新增登记。</p>}
  </section>;
}
