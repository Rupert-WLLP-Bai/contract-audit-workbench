import { useId, useState, type FormEvent } from "react";
import type { DecisionAction, Finding, ReviewDocument } from "../api/types";
import type { FindingFilter } from "../domain/review";
import {
  DECISION_ACTIONS,
  confidenceLabel,
  decisionLabel,
  filterFindings,
  formatDateTime,
  latestDecision,
  originLabel,
  relationLabel,
  roleLabel,
  shortDigest,
  validateDecisionReason,
} from "../domain/review";
import { evidenceSourceMatches, resolveAnchor } from "../domain/text";
const FILTERS: { id: FindingFilter; label: string }[] = [
  { id: "all", label: "全部" },
  { id: "conflict", label: "冲突" },
  { id: "insufficient", label: "信息不足" },
  { id: "undecided", label: "待裁决" },
];

const SEVERITY_LABELS: Record<string, string> = { high: "高风险", medium: "中风险", low: "低风险", info: "提示", unrated: "未分级" };

export function FindingPanel({
  findings,
  documents,
  selectedId,
  onSelect,
  pending,
  canDecide = true,
  decisionDisabledReason,
  conflictMessage,
  saveError,
  onDecide,
}: {
  findings: Finding[];
  documents: ReviewDocument[];
  selectedId: string | null;
  onSelect: (findingId: string) => void;
  pending: boolean;
  canDecide?: boolean;
  decisionDisabledReason?: string | null;
  conflictMessage: string | null;
  saveError: string | null;
  onDecide: (input: { findingId: string; action: DecisionAction; reason: string; expectedVersion: number }) => Promise<boolean>;
}) {
  const [filter, setFilter] = useState<FindingFilter>("all");
  const [panel, setPanel] = useState("风险审查");
  const visible = filterFindings(findings, filter);
  const selected = findings.find((finding) => finding.id === selectedId) ?? null;
  const selectedHidden = selected !== null && !visible.some((finding) => finding.id === selected.id);

  return (
    <aside className="col-findings">
      <h2 className="sr-only">审查结果</h2>
      <nav className="finding-panel-tabs" aria-label="审查结果分类" role="tablist">{["风险审查", "条款比对", "合同信息", "审查结论"].map((name) => <button type="button" key={name} role="tab" aria-selected={panel === name} onClick={() => setPanel(name)}>{name}</button>)}</nav>
      <div hidden={panel !== "风险审查"}>
      <dl className="finding-counts finding-severity-counts" aria-label="当前运行风险分级">
        {["high", "medium", "low", "info", ...(findings.some((item) => item.severity === null) ? ["unrated"] : [])].map((severity) => <div key={severity} data-severity={severity}><dt>{SEVERITY_LABELS[severity]}</dt><dd>{findings.filter((item) => (item.severity ?? "unrated") === severity).length}</dd></div>)}
      </dl>
      <div className="filter-row" role="group" aria-label="筛选线索">
        {FILTERS.map((item) => (
          <button key={item.id} type="button" aria-pressed={filter === item.id} onClick={() => setFilter(item.id)}>
            {item.label}
          </button>
        ))}
      </div>
      {findings.length === 0 ? <p>这次运行还没有形成风险线索。这不等于材料已经无风险。</p> : null}
      <ul className="finding-list">
        {visible.map((finding) => {
          const decision = latestDecision(finding);
          return (
            <li key={finding.id}>
              <button type="button" className="finding-button" data-relation={finding.relation} data-severity={finding.severity ?? "unrated"} aria-label={`选择线索：${finding.claim || "未命名线索"}`} aria-current={finding.id === selectedId} onClick={() => onSelect(finding.id)}>
                <span className="finding-card-title">{finding.claim || "未命名线索"}</span>
                <span className="finding-grade" data-severity={finding.severity ?? "unrated"}>{SEVERITY_LABELS[finding.severity ?? "unrated"] ?? "未分级"}</span>
                <span className="meta">
                  {originLabel(finding.origin)} · {relationLabel(finding.relation)}
                  {decision ? ` · 已记录${decisionLabel(decision.action)}` : " · 待裁决"}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
      {selectedHidden ? <p className="meta">当前线索不在这个筛选结果里，右侧仍保留它。</p> : null}
      {selected ? (
        <FindingDetail
          key={selected.id}
          finding={selected}
          documents={documents}
          pending={pending}
          canDecide={canDecide}
          decisionDisabledReason={decisionDisabledReason}
          conflictMessage={conflictMessage}
          saveError={saveError}
          onDecide={onDecide}
        />
      ) : null}
      </div>
      {panel === "条款比对" ? <div className="finding-tab-content"><p>各发现引用的实际材料片段；变更本身不代表风险。</p>{findings.map((item) => <section key={item.id}><button type="button" onClick={() => { onSelect(item.id); setPanel("风险审查"); }}>{item.claim}</button>{item.evidence.map((anchor) => <blockquote key={anchor.id}>{anchor.quote}<small>{anchor.document_version_id} · {anchor.status}</small></blockquote>)}</section>)}</div> : null}
      {panel === "合同信息" ? <div className="finding-tab-content">{documents.map((document) => <section key={document.id}><h3>{document.filename}</h3><dl><dt>材料角色</dt><dd>{roleLabel(document.role)}</dd><dt>解析状态</dt><dd>{document.parse_status}</dd><dt>原件身份</dt><dd>{document.id}</dd><dt>冻结解析</dt><dd>{document.parsed_document?.id ?? "未记录"}</dd><dt>原件 SHA-256</dt><dd>{document.digest}</dd></dl></section>)}</div> : null}
      {panel === "审查结论" ? <div className="finding-tab-content"><p>规则、模型建议和人工决定分别保留。运行结束不表示合同获批。</p>{findings.map((item) => <section key={item.id}><h3>{item.claim}</h3><p>{originLabel(item.origin)} · {relationLabel(item.relation)}</p>{item.decisions.length === 0 ? <p>尚未记录人工决定。</p> : item.decisions.map((decision) => <article key={decision.id}><strong>{decisionLabel(decision.action)}</strong><p>{decision.reason}</p><small>{decision.actor} · {formatDateTime(decision.decided_at)}</small></article>)}</section>)}</div> : null}
    </aside>
  );
}

function FindingDetail({
  finding,
  documents,
  pending,
  canDecide,
  decisionDisabledReason,
  conflictMessage,
  saveError,
  onDecide,
}: {
  finding: Finding;
  documents: ReviewDocument[];
  pending: boolean;
  canDecide: boolean;
  decisionDisabledReason?: string | null;
  conflictMessage: string | null;
  saveError: string | null;
  onDecide: (input: { findingId: string; action: DecisionAction; reason: string; expectedVersion: number }) => Promise<boolean>;
}) {
  const reasonId = useId();
  const hintId = useId();
  const [action, setAction] = useState<DecisionAction | null>(null);
  const [reason, setReason] = useState("");
  const [attempted, setAttempted] = useState(false);
  const reasonError = attempted ? validateDecisionReason(reason) : null;
  const canSubmit = canDecide && action !== null && finding.version !== null && validateDecisionReason(reason) === null && !pending;
  const confidence = confidenceLabel(finding.confidence);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setAttempted(true);
    if (!canDecide || action === null || finding.version === null || validateDecisionReason(reason) !== null) return;
    const saved = await onDecide({
      findingId: finding.id,
      action,
      reason: reason.trim(),
      expectedVersion: finding.version,
    });
    if (saved) {
      setReason("");
      setAttempted(false);
    }
  }

  return (
    <article className="finding-detail" aria-live="polite">
      <p className="meta">
        {originLabel(finding.origin)} · {relationLabel(finding.relation)}
        {confidence ? ` · ${confidence}` : ""}
        {finding.domain ? ` · ${finding.domain}` : ""}
      </p>
      <h3>{finding.claim || "未命名线索"}</h3>
      {finding.severity_source ? <p className="finding-grade-source">{SEVERITY_LABELS[finding.severity ?? "unrated"]} · {finding.severity_source.policy_id} v{finding.severity_source.policy_version} 第{finding.severity_source.clause}条：{finding.severity_source.reason}</p> : null}
      {finding.unverified_reason ? <p className="callout">尚未核验：{finding.unverified_reason}</p> : null}
      <h4>证据</h4>
      {finding.evidence.length === 0 ? <p>这条线索没有附带证据，不能视为已验证。</p> : null}
      <div className="evidence-stack">
        {finding.evidence.map((anchor) => {
          const source = documents.find((document) => evidenceSourceMatches(anchor, document.id, document.parsed_document)) ??
            documents.find((document) => document.id === anchor.document_version_id);
          const resolved = resolveAnchor(source?.text ?? null, anchor, source?.parsed_document ?? null);
          const located = anchor.status === "located" && resolved.kind === "highlight";
          const anchorLabel = located
            ? "已定位"
            : anchor.status === "unlocatable"
              ? "无法定位"
              : anchor.status === "anchor_mismatch"
                ? "定位失效"
                : anchor.status === "located"
                  ? "未能在正文定位"
                  : "来源缺失";
          return (
            <section key={anchor.id} className="evidence-card">
              <header>
                <strong>{source ? roleLabel(source.role) : "来源未对应到材料"}</strong>
                <span>{anchorLabel}</span>
              </header>
              {source ? <p className="meta">{source.filename}</p> : null}
              <p
                className="meta"
                data-evidence-parsed-document-id={anchor.parsed_document_id ?? ""}
                data-evidence-normalized-digest={anchor.normalized_digest ?? ""}
                data-evidence-document-version-id={anchor.document_version_id}
              >
                {anchor.parsed_document_id === null && anchor.normalized_digest === null ? (
                  "历史来源未记录解析身份或正文摘要"
                ) : (
                  <>
                    冻结解析 {anchor.parsed_document_id === null ? "未记录" : <span title={anchor.parsed_document_id}>{shortDigest(anchor.parsed_document_id)}</span>}
                    {" · "}正文 SHA-256 {anchor.normalized_digest === null ? "未记录" : <span title={anchor.normalized_digest}>{shortDigest(anchor.normalized_digest)}</span>}
                  </>
                )}
              </p>
              {resolved.kind === "snapshot" && !anchor.mismatch_reason ? <p className="callout">{resolved.reason}</p> : null}
              {resolved.kind === "unavailable" ? <p className="callout">{resolved.reason}</p> : null}
              {located && anchor.page !== null ? <p className="meta">第 {anchor.page} 页</p> : null}
              {anchor.mismatch_reason ? <p>{anchor.mismatch_reason}</p> : null}
              {anchor.quote ? <blockquote>{anchor.quote}</blockquote> : <p>没有引用快照。</p>}
            </section>
          );
        })}
      </div>
      <h4>决定记录</h4>
      {finding.decisions.length === 0 ? <p>还没有人工决定。模型建议不会自动结案。</p> : null}
      <ol className="history">
        {finding.decisions.map((decision) => (
          <li key={decision.id}>
            <p>
              <strong>{decisionLabel(decision.action)}</strong> · {decision.actor} ·{" "}
              <time dateTime={decision.decided_at}>{formatDateTime(decision.decided_at)}</time>
            </p>
            <p>{decision.reason}</p>
          </li>
        ))}
      </ol>
      <form onSubmit={onSubmit}>
        {!canDecide ? <p>{decisionDisabledReason ?? "当前角色不能提交人工决定。"}</p> : null}
        <fieldset disabled={!canDecide}>
          <legend>{finding.decisions.length > 0 ? "更正决定" : "人工决定"}</legend>
          {finding.decisions.length > 0 ? <p className="meta">更正会追加一条新记录，不会抹掉原来的决定。</p> : null}
          {DECISION_ACTIONS.map((item) => (
            <label key={item.action} className="choice">
              <input
                type="radio"
                name={`decision-${finding.id}`}
                value={item.action}
                checked={action === item.action}
                onChange={() => setAction(item.action)}
              />
              {item.label}
            </label>
          ))}
        </fieldset>
        <label className="field" htmlFor={reasonId}>
          理由
        </label>
        <textarea
          id={reasonId}
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          aria-describedby={hintId}
          rows={4}
          disabled={!canDecide}
        />
        <p id={hintId} className="meta">
          四种决定都要写理由。保存后写入审查记录。
        </p>
        {finding.version === null ? (
          <p role="status">服务没有返回这条线索的版本号，暂不能保存，以免覆盖其他人的决定。</p>
        ) : null}
        {reasonError ? <p role="alert">{reasonError}</p> : null}
        {conflictMessage ? <p role="alert">{conflictMessage}</p> : null}
        {saveError ? <p role="alert">{saveError}</p> : null}
        <button className="button button-primary" type="submit" disabled={!canSubmit}>
          {pending ? "正在保存" : "保存决定"}
        </button>
      </form>
    </article>
  );
}
