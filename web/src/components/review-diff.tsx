import { useQuery } from "@tanstack/react-query";
import { Button, Collapse, Select } from "antd";
import { useState } from "react";
import { Link } from "react-router";
import { getReview, getReviewHistory } from "../api/endpoints";
import { errorMessage } from "../api/http";
import type { EvidenceAnchor, ReviewWorkspace } from "../api/types";
import { decisionLabel, formatDateTime, originLabel, relationLabel, runLabel } from "../domain/review";
import {
  compareReviewWorkspaces,
  frozenDocument,
  frozenWorkspaceProblem,
  reviewScope,
  reviewScopeDescription,
  ruleDifference,
  scopeDifference,
  sideAnchors,
  type DifferenceState,
  type ReviewDiffKind,
  type ReviewDiffRow,
  type ReviewDiffSide,
} from "../domain/review-diff";
import { resolveAnchor } from "../domain/text";
import { reviewChoiceForKey, reviewChoices, type ReviewChoice } from "../domain/workflow";
import "./review-diff.css";

interface ReviewDiffProps {
  caseId: string;
  latestRunId: string;
  actorScope: string;
}

const labels: Record<ReviewDiffKind, string> = {
  added: "新增线索",
  disappeared: "消失的线索",
  changed: "结论或说明变化",
  unmatched: "无法唯一对应，需人工核对",
  unchanged: "对应结果未变化",
};

function choiceProblem(choice: ReviewChoice): string | null {
  const attempt = choice.attempt;
  if (attempt.run_id !== choice.runId || !attempt.id) return "执行身份无效，不可比较";
  if (attempt.history_kind !== "frozen") return "无冻结记录，不可比较";
  if (!attempt.finished_at || (attempt.state !== "completed" && attempt.state !== "awaiting_human")) return "未完整完成，不可比较";
  return null;
}

function Choice({ label, choices, value, onChange }: {
  label: string;
  choices: ReviewChoice[];
  value: string;
  onChange: (value: string) => void;
}) {
  return <label className="field">
    {label}
    <Select<string>
      aria-label={label}
      value={value || undefined}
      onChange={onChange}
      placeholder="选择审查和具体执行记录"
      options={choices.map((choice) => {
        const problem = choiceProblem(choice);
        return {
          value: choice.key,
          disabled: problem !== null,
          label: `审查 ${choice.runId} · 第 ${choice.attempt.ordinal} 次 (${choice.attempt.id}) · ${runLabel(choice.attempt.state)}${problem ? ` · ${problem}` : ""}`,
        };
      })}
    />
  </label>;
}

function stateText(state: DifferenceState, same: string, different: string, unknown: string): string {
  return state === "same" ? same : state === "different" ? different : unknown;
}

function sameIds(first: string[], second: string[]): boolean {
  if (first.length !== second.length) return false;
  const counts = new Map<string, number>();
  for (const id of first) counts.set(id, (counts.get(id) ?? 0) + 1);
  for (const id of second) {
    const count = counts.get(id) ?? 0;
    if (!count) return false;
    counts.set(id, count - 1);
  }
  return true;
}

function SnapshotSide({ title, workspace }: { title: string; workspace: ReviewWorkspace }) {
  const attempt = workspace.review_attempts?.attempt;
  const snapshot = workspace.snapshot;
  const scope = reviewScope(workspace);
  const agent = workspace.agent_review?.attempt_id === attempt?.id ? workspace.agent_review : null;
  const evaluations = workspace.evaluations ?? [];
  const rules = [...new Set(evaluations.map((item) => `${item.rule_id || "规则 ID 未返回"} / ${item.rule_version || "版本未返回"} / ${item.capability_id || "能力未返回"}`))];
  return <section className="review-diff-side" aria-label={`${title}身份与依据`}>
    <h4>{title}</h4>
    <dl>
      <dt>审查 / 执行</dt><dd>{workspace.run.id} / {attempt?.id ?? "未返回"}，{runLabel(attempt?.state ?? "")}</dd>
      <dt>审查范围</dt><dd>{reviewScopeDescription(workspace)}</dd>
      <dt>规则身份</dt><dd>{scope ? `${scope.rule_set_id} / ${scope.rule_set_version} / ${scope.rule_set_hash}` : agent?.rulepack ? `${agent.rulepack.id} / ${agent.rulepack.version}，规则包摘要未返回` : "规则包身份未返回"}</dd>
      <dt>规则集摘要</dt><dd>{scope?.rule_set_hash || snapshot?.rule_set_hash || "未返回"}</dd>
      <dt>已返回的规则检查</dt><dd>{rules.length ? rules.join("；") : "未返回，不能推断已检查"}</dd>
      <dt>输入摘要</dt><dd>{attempt?.input_hash || "未返回"}</dd>
      <dt>输出摘要</dt><dd>{attempt?.output_hash || "未返回"}</dd>
      <dt>冻结材料与解析版本</dt><dd>
        {(snapshot?.document_ids ?? []).map((id) => {
          const doc = workspace.documents.find((item) => item.id === id && !!item.parsed_document_id && attempt?.parsed_document_ids.includes(item.parsed_document_id));
          return <div key={id}>
            {doc?.filename ?? "材料未返回"} · 材料 {id} · 解析 {doc?.parsed_document_id ?? "未返回"} · 原件摘要 {doc?.digest || "未返回"}
            <div>解析器 {doc?.parsed_document ? `${doc.parsed_document.parser_id} ${doc.parsed_document.parser_version}` : "未返回"} · 配置摘要 {doc?.parsed_document?.config_hash || "未返回"} · 正文摘要 {doc?.parsed_document?.normalized_digest || "未返回"}</div>
          </div>;
        })}
      </dd>
    </dl>
  </section>;
}

function ContextComparison({ source, target }: { source: ReviewWorkspace; target: ReviewWorkspace }) {
  const sourceAttempt = source.review_attempts?.attempt;
  const targetAttempt = target.review_attempts?.attempt;
  const sourceDocuments = source.snapshot?.document_ids ?? [];
  const targetDocuments = target.snapshot?.document_ids ?? [];
  const sourceParses = sourceAttempt?.parsed_document_ids ?? [];
  const targetParses = targetAttempt?.parsed_document_ids ?? [];
  const inputHash = sourceAttempt?.input_hash && targetAttempt?.input_hash
    ? sourceAttempt.input_hash === targetAttempt.input_hash ? "输入摘要相同" : "输入摘要不同"
    : "输入摘要未完整返回，不能确认所有输入相同";
  const rules = ruleDifference(source, target);
  const scope = scopeDifference(source, target);
  const sourceRules = (source.evaluations ?? []).map((item) => JSON.stringify([item.domain, item.rule_id, item.rule_version, item.capability_id]));
  const targetRules = (target.evaluations ?? []).map((item) => JSON.stringify([item.domain, item.rule_id, item.rule_version, item.capability_id]));
  const completeRuleIdentities = [...(source.evaluations ?? []), ...(target.evaluations ?? [])]
    .every((item) => item.domain && item.rule_id && item.rule_version && item.capability_id);
  return <section aria-label="比较前提">
    <h3>比较前提</h3>
    <ul className="review-diff-summary">
      <li>范围：{stateText(scope, "可见范围身份一致", "可见范围不同，不能自动配对", "范围身份不完整，不能确认一致")}</li>
      <li>规则：{stateText(rules, "冻结规则摘要一致或属于同一审查", "冻结规则摘要不同", "规则摘要未完整返回，不能确认规则集一致")}</li>
      <li>已返回的检查：{!sourceRules.length || !targetRules.length || !completeRuleIdentities ? "规则身份或检查记录不完整" : sameIds(sourceRules, targetRules) ? "检查身份与版本一致（不代表完整规则集一致）" : "检查身份、版本或返回数量不同"}</li>
      <li>输入：{sameIds(sourceDocuments, targetDocuments) ? "材料版本集合一致" : "材料版本集合不同"}；{inputHash}</li>
      <li>解析：{sameIds(sourceParses, targetParses) ? "冻结解析版本集合一致" : "冻结解析版本集合不同"}；缺少的解析器、配置或正文摘要标记为未返回。</li>
    </ul>
    <p className="callout">对照只说明两个记录中返回的内容。范围变化、规则变化、未执行或缺少唯一对应时，不能把线索消失当成风险消除；模型结果仍需结合原文及人工判断。</p>
    <div className="review-diff-columns">
      <SnapshotSide title="来源" workspace={source} />
      <SnapshotSide title="目标" workspace={target} />
    </div>
  </section>;
}

function Citation({ workspace, anchor }: { workspace: ReviewWorkspace; anchor: EvidenceAnchor }) {
  const document = frozenDocument(workspace, anchor);
  const resolved = document
    ? resolveAnchor(document.text, anchor, document.parsed_document, document.parsed_document_id)
    : null;
  if (!document || !resolved || resolved.kind !== "highlight" || document.text === null) {
    const reason = resolved?.kind === "snapshot" || resolved?.kind === "unavailable" ? resolved.reason : "未找到与本次执行一致的冻结材料和解析身份。";
    return <li>
      <p>引用未能定位：{reason}</p>
      {anchor.quote ? <blockquote>保存的引用片段：{anchor.quote}</blockquote> : null}
    </li>;
  }
  const characters = Array.from(document.text);
  const from = Math.max(0, resolved.start - 80);
  const to = Math.min(characters.length, resolved.end + 80);
  return <li>
    <p>{document.filename} · 材料 {document.id} · 解析 {document.parsed_document_id} {resolved.page === null ? "" : `· 第 ${resolved.page} 页`} · 字符 {resolved.start}–{resolved.end}（Unicode 码点）</p>
    <blockquote className="review-diff-quote">{from > 0 ? "…" : ""}{characters.slice(from, resolved.start).join("")}<mark>{characters.slice(resolved.start, resolved.end).join("")}</mark>{characters.slice(resolved.end, to).join("")}{to < characters.length ? "…" : ""}</blockquote>
  </li>;
}

function DiffSide({ label, side }: { label: string; side: ReviewDiffSide | null }) {
  if (!side) return <section className="review-diff-side"><h4>{label}</h4><p>本侧没有唯一对应记录。</p></section>;
  const evaluation = side.evaluation;
  const finding = side.finding;
  const anchors = sideAnchors(side);
  const attempt = side.workspace.review_attempts?.attempt;
  const search = new URLSearchParams();
  if (attempt) search.set("attempt_id", attempt.id);
  if (finding) search.set("finding", finding.id);
  const href = `/cases/${encodeURIComponent(side.workspace.run.case_id)}/reviews/${encodeURIComponent(side.workspace.run.id)}?${search.toString()}`;
  return <section className="review-diff-side">
    <h4>{label}</h4>
    {evaluation ? <>
      <p><strong>{relationLabel(evaluation.relation)}</strong> · {evaluation.reason || "检查说明未返回"}</p>
      <p className="meta">规则 {evaluation.rule_id || "未返回"} · 版本 {evaluation.rule_version || "未返回"} · 能力 {evaluation.capability_id || "未返回"} · 来源内对象引用 {evaluation.object_refs.join("、") || "未返回"}</p>
    </> : null}
    {finding ? <p>{originLabel(finding.origin)}：{finding.claim || "线索内容未返回"}（{relationLabel(finding.relation)}）</p> : <p>本侧没有关联的规则发现。</p>}
    {finding?.unverified_reason ? <p className="callout">尚未核验：{finding.unverified_reason}</p> : null}
    <Link to={href}>打开{label}的冻结执行记录</Link>
    {anchors.length ? <ul className="review-diff-citations">{anchors.map((anchor, index) => <Citation key={`${anchor.id}-${index}`} workspace={side.workspace} anchor={anchor} />)}</ul> : <p className="meta">未返回可定位的引用，不能从本侧定位原文。</p>}
    {finding?.decisions.length ? <details>
      <summary>人工决定历史（独立追加）</summary>
      <ul>{finding.decisions.map((decision) => <li key={decision.id}>{decisionLabel(decision.action)} · {decision.actor} · {formatDateTime(decision.decided_at)}：{decision.reason}</li>)}</ul>
    </details> : null}
  </section>;
}

function DiffRows({ rows, kind }: { rows: ReviewDiffRow[]; kind: ReviewDiffKind }) {
  const selected = rows.filter((row) => row.kind === kind);
  if (!selected.length) return null;
  const content = <ul className="review-diff-list">{selected.map((row, index) => <li key={`${row.source?.evaluation?.id ?? row.source?.finding?.id ?? ""}-${row.target?.evaluation?.id ?? row.target?.finding?.id ?? ""}-${index}`}>
    <p className="meta">{row.reason}</p>
    <div className="review-diff-columns"><DiffSide label="来源" side={row.source} /><DiffSide label="目标" side={row.target} /></div>
  </li>)}</ul>;
  return kind === "unchanged" ? <details><summary>{labels[kind]}（{selected.length}）</summary>{content}</details>
    : <section><h3>{labels[kind]}（{selected.length}）</h3>{content}</section>;
}

function ReviewDiffBody({ caseId, latestRunId, actorScope }: ReviewDiffProps) {
  const [sourceKey, setSourceKey] = useState("");
  const [targetKey, setTargetKey] = useState("");
  const history = useQuery({
    queryKey: ["cases", caseId, "review-diff-history", latestRunId, actorScope],
    queryFn: async ({ signal }) => {
      const entries = await getReviewHistory(caseId, latestRunId, signal);
      if (entries.some((entry) => entry.workspace.run.case_id !== caseId || entry.workspace.run.id !== entry.runId)) throw new Error("审查历史中的案件或审查身份不一致。");
      return entries;
    },
    retry: false,
  });
  const entries = history.isSuccess && !history.isFetching ? history.data : [];
  const choices = reviewChoices(entries).filter((choice) => choice.attempt.run_id === choice.runId);
  const sourceChoice = reviewChoiceForKey(choices, sourceKey);
  const targetChoice = reviewChoiceForKey(choices, targetKey);
  const source = useQuery({
    queryKey: ["cases", caseId, "review-diff-record", actorScope, sourceChoice?.runId ?? "", sourceChoice?.attempt.id ?? ""],
    queryFn: ({ signal }) => getReview(caseId, sourceChoice!.runId, sourceChoice!.attempt.id, signal),
    enabled: !!sourceChoice && !choiceProblem(sourceChoice),
    retry: false,
  });
  const target = useQuery({
    queryKey: ["cases", caseId, "review-diff-record", actorScope, targetChoice?.runId ?? "", targetChoice?.attempt.id ?? ""],
    queryFn: ({ signal }) => getReview(caseId, targetChoice!.runId, targetChoice!.attempt.id, signal),
    enabled: !!targetChoice && !choiceProblem(targetChoice),
    retry: false,
  });

  if (history.isFetching) return <p role="status">正在读取当前可见的审查历史…</p>;
  if (history.isError) return <div role="alert"><p>审查历史无法读取：{errorMessage(history.error)}</p><Button onClick={() => void history.refetch()}>重新读取历史</Button></div>;
  const unavailableRuns = entries.filter((entry) => !entry.workspace.review_attempts);
  const sameSelection = !!sourceChoice && !!targetChoice && sourceChoice.key === targetChoice.key;
  const sourceProblem = source.isSuccess && sourceChoice && !source.isFetching
    ? frozenWorkspaceProblem(source.data, caseId, sourceChoice.runId, sourceChoice.attempt.id) : null;
  const targetProblem = target.isSuccess && targetChoice && !target.isFetching
    ? frozenWorkspaceProblem(target.data, caseId, targetChoice.runId, targetChoice.attempt.id) : null;
  const ready = source.isSuccess && target.isSuccess && !source.isFetching && !target.isFetching &&
    sourceChoice && targetChoice && !sameSelection && !sourceProblem && !targetProblem;
  const rows = ready && source.data && target.data ? compareReviewWorkspaces(source.data, target.data) : [];
  return <>
    <p>选择同一案件中两条已完成、保存了冻结原文的执行记录。来源和目标均按所选身份重新读取。</p>
    {unavailableRuns.length ? <p className="callout">以下审查未返回执行记录，不能比较：{unavailableRuns.map((entry) => entry.runId).join("、")}。</p> : null}
    {choices.length === 0 ? <p>当前没有可选择的审查执行记录。</p> : <div className="review-diff-columns">
      <Choice label="来源审查与执行" choices={choices} value={sourceKey} onChange={setSourceKey} />
      <Choice label="目标审查与执行" choices={choices} value={targetKey} onChange={setTargetKey} />
    </div>}
    {sameSelection ? <p className="callout">请选择两条不同的执行记录。</p> : null}
    {(source.isFetching || target.isFetching) ? <p role="status">正在读取所选冻结记录…</p> : null}
    {source.isError ? <div role="alert"><p>来源记录不可读取：{errorMessage(source.error)}</p><Button onClick={() => void source.refetch()}>重新读取来源</Button></div> : null}
    {target.isError ? <div role="alert"><p>目标记录不可读取：{errorMessage(target.error)}</p><Button onClick={() => void target.refetch()}>重新读取目标</Button></div> : null}
    {sourceProblem ? <p role="alert">来源：{sourceProblem}</p> : null}
    {targetProblem ? <p role="alert">目标：{targetProblem}</p> : null}
    {ready && source.data && target.data ? <>
      <ContextComparison source={source.data} target={target.data} />
      <section aria-label="复审差异结果">
        <h3>差异结果</h3>
        <p>人工决定按各自发现展示为独立追加历史，不参与规则或模型关系比较。对象仅有来源内引用时，无法确认跨来源身份的条目保留待核对。</p>
        {rows.length === 0 ? <p>两侧没有返回可比较的规则检查或线索；不能推断不存在风险。</p> : null}
        {(["added", "disappeared", "changed", "unmatched", "unchanged"] as const).map((kind) => <DiffRows key={kind} rows={rows} kind={kind} />)}
      </section>
    </> : null}
  </>;
}

export function ReviewDiff(props: ReviewDiffProps) {
  const [open, setOpen] = useState(false);
  return <section id="review-diff" className="review-diff" aria-label="复审差异">
    <Collapse activeKey={open ? ["diff"] : []} onChange={(keys) => setOpen(keys.includes("diff"))} items={[
      { key: "diff", label: "复审差异：选择两个冻结执行记录", children: open ? <ReviewDiffBody key={`${props.caseId}:${props.latestRunId}:${props.actorScope}`} {...props} /> : null },
    ]} />
  </section>;
}
