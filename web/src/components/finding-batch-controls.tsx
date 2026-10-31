import { Button, Input, Radio } from "antd";
import { useId, type FormEvent } from "react";
import type { Finding } from "../api/types";
import { DECISION_ACTIONS, relationLabel, type FindingBatchGroup } from "../domain/review";
import type { BatchDecisionDraftHandle } from "../hooks/decision-drafts";

export function FindingBatchControls({
  draft,
  selected,
  selectedId,
  group,
  membersValid,
  retryingSingle,
  needsReconfirmation,
  canSubmit,
  canDecide,
  pending,
  onSelect,
  onSubmit,
}: {
  draft: BatchDecisionDraftHandle;
  selected: Finding[];
  selectedId: string | null;
  group: FindingBatchGroup | null;
  membersValid: boolean;
  retryingSingle: boolean;
  needsReconfirmation: boolean;
  canSubmit: boolean;
  canDecide: boolean;
  pending: boolean;
  onSelect: (findingId: string) => void;
  onSubmit: () => void;
}) {
  const reasonId = useId();
  const hintId = useId();
  const radioName = useId();
  const hasDraft = draft.selectedIds.length > 0 || draft.action !== null || draft.reason !== "";
  if (!canDecide && !hasDraft && draft.results.length === 0) return null;

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!pending) onSubmit();
  }

  return (
    <section aria-label="批量裁决" style={{ marginTop: "var(--space-4)" }}>
      <h3>批量裁决</h3>
      <p className="meta">逐条勾选同一规则、同类结论的线索；每条决定单独保存并保留历史。</p>
      {draft.selectedIds.length > 0 ? (
        <p>
          已选 {draft.selectedIds.length} 条
          {group ? `：规则 ${group.ruleId}${group.ruleVersion ? `（${group.ruleVersion}）` : ""}，${relationLabel(group.relation)}` : "：无法确认规则"}
        </p>
      ) : null}
      {draft.selectedIds.length !== selected.length || (draft.selectedIds.length > 0 && !membersValid) ? (
        <p role="alert">所选线索或规则信息已变化，请检查并调整选择后再提交。</p>
      ) : null}
      {draft.selectedIds.length === 1 && !retryingSingle ? <p className="meta">至少选择两条线索才能批量提交。</p> : null}
      {retryingSingle ? <p className="meta">其余线索已处理，未成功项可单条重试。</p> : null}
      {needsReconfirmation ? <p role="status">请逐条查看未成功的线索，并确认最新版本后重试。</p> : null}
      {hasDraft ? (
        <form onSubmit={submit}>
          <fieldset disabled={!canDecide || pending}>
            <legend>统一决定</legend>
            {DECISION_ACTIONS.map((item) => (
              <div className="choice" key={item.action}>
                <Radio
                  name={radioName}
                  value={item.action}
                  checked={draft.action === item.action}
                  onChange={() => draft.update((current) => ({ ...current, action: item.action }))}
                >
                  {item.label}
                </Radio>
              </div>
            ))}
          </fieldset>
          <label className="field" htmlFor={reasonId}>批量决定理由</label>
          <Input.TextArea
            id={reasonId}
            aria-describedby={hintId}
            rows={3}
            value={draft.reason}
            disabled={!canDecide || pending}
            onChange={(event) => draft.update((current) => ({ ...current, reason: event.target.value }))}
          />
          <p id={hintId} className="meta">同一理由会写入每条已选线索的决定记录，至少两个字。</p>
          <div className="filter-row">
            <Button type="primary" htmlType="submit" loading={pending} disabled={!canSubmit || !canDecide || pending}>
              {pending ? "正在逐条提交" : "逐条提交已选线索"}
            </Button>
            <Button
              htmlType="button"
              disabled={pending}
              onClick={() => draft.update(() => ({ action: null, reason: "", selectedIds: [], results: [] }))}
            >
              放弃批量草稿并清空选择
            </Button>
          </div>
        </form>
      ) : null}
      {draft.results.length > 0 ? (
        <div aria-live="polite">
          <h4>逐条提交结果</h4>
          <ul>
            {draft.results.map((result) => (
              <li key={result.findingId}>
                <Button htmlType="button" type="link" onClick={() => onSelect(result.findingId)}>
                  {result.claim || "未命名线索"}
                </Button>
                {result.status === "success" ? "成功" : result.status === "conflict" ? "冲突" : "失败"}：{result.message}
                {result.status !== "success" && draft.selectedIds.includes(result.findingId) ? (
                  <Button
                    htmlType="button"
                    disabled={pending || !canDecide || selectedId !== result.findingId || selected.find((finding) => finding.id === result.findingId)?.version == null}
                    onClick={() => {
                      const version = selected.find((finding) => finding.id === result.findingId)?.version;
                      if (version == null) return;
                      draft.update((current) => ({
                        ...current,
                        results: current.results.map((item) => item.findingId === result.findingId ? { ...item, confirmedVersion: version } : item),
                      }));
                    }}
                  >
                    {selected.find((finding) => finding.id === result.findingId)?.version === result.confirmedVersion && result.confirmedVersion !== null ? "已确认最新版本" : "确认按最新版本重试"}
                  </Button>
                ) : null}
              </li>
            ))}
          </ul>
          {!hasDraft ? (
            <Button htmlType="button" onClick={() => draft.update((current) => ({ ...current, results: [] }))}>关闭结果</Button>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
