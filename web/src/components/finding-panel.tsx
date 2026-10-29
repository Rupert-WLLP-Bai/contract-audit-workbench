import { Tabs } from "antd";
import { Card } from "antd";
import { Button, Input, Radio } from "antd";
import { useId, useState, type FormEvent } from "react";
import type {
  DecisionAction,
  EvidenceAnchor,
  Finding,
  HumanDecision,
  ReviewDocument,
} from "../api/types";
import type { FindingFilter } from "../domain/review";
import {
  DECISION_ACTIONS,
  confidenceLabel,
  decisionLabel,
  filterFindings,
  formatDateTime,
  latestDecision,
  originLabel,
  parseLabel,
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

const SEVERITY_LABELS: Record<string, string> = {
  high: "高风险",
  medium: "中风险",
  low: "低风险",
  info: "提示",
  unrated: "未分级",
};

function SeverityCounts({ findings }: { findings: Finding[] }) {
  const severities = [
    "high",
    "medium",
    "low",
    "info",
    ...(findings.some((item) => item.severity === null) ? ["unrated"] : []),
  ];
  return (
    <dl
      className="finding-counts finding-severity-counts"
      aria-label="本次审查风险分级"
    >
      {severities.map((severity) => (
        <div key={severity} data-severity={severity}>
          <dt>{SEVERITY_LABELS[severity]}</dt>
          <dd>
            {
              findings.filter(
                (item) => (item.severity ?? "unrated") === severity,
              ).length
            }
          </dd>
        </div>
      ))}
    </dl>
  );
}

function FindingFilterButtons({
  filter,
  onChange,
}: {
  filter: FindingFilter;
  onChange: (filter: FindingFilter) => void;
}) {
  return (
    <div className="filter-row" role="group" aria-label="筛选线索">
      {FILTERS.map((item) => (
        <Button
          aria-label={String(item.label)}
          key={item.id}
          htmlType="button"
          aria-pressed={filter === item.id}
          onClick={() => onChange(item.id)}
        >
          {item.label}
        </Button>
      ))}
    </div>
  );
}

function FindingListRow({
  finding,
  selectedId,
  onSelect,
}: {
  finding: Finding;
  selectedId: string | null;
  onSelect: (findingId: string) => void;
}) {
  const decision = latestDecision(finding);
  return (
    <li>
      <Button
        htmlType="button"
        className="finding-button"
        data-relation={finding.relation}
        data-severity={finding.severity ?? "unrated"}
        aria-label={`选择线索：${finding.claim || "未命名线索"}`}
        aria-current={finding.id === selectedId}
        onClick={() => onSelect(finding.id)}
      >
        <span className="finding-card-title">
          {finding.claim || "未命名线索"}
        </span>
        <span
          className="finding-grade"
          data-severity={finding.severity ?? "unrated"}
        >
          {SEVERITY_LABELS[finding.severity ?? "unrated"] ?? "未分级"}
        </span>
        <span className="meta">
          {originLabel(finding.origin)}， {relationLabel(finding.relation)}
          {decision
            ? `，已记录${decisionLabel(decision.action)}`
            : "，待裁决"}
        </span>
      </Button>
    </li>
  );
}

function ClauseComparisonTab({
  findings,
  documents,
  onSelect,
  onPanel,
}: {
  findings: Finding[];
  documents: ReviewDocument[];
  onSelect: (findingId: string) => void;
  onPanel: (panel: string) => void;
}) {
  return (
    <div className="finding-tab-content">
      <p>各发现引用的实际材料片段；变更本身不代表风险。</p>
      {findings.map((item) => (
        <section key={item.id}>
          <Button
            aria-label={String(item.claim)}
            htmlType="button"
            onClick={() => {
              onSelect(item.id);
              onPanel("风险审查");
            }}
          >
            {item.claim}
          </Button>
          {item.evidence.map((anchor) => (
            <blockquote key={anchor.id}>
              {anchor.quote}
              <small>
                {documents.find(
                  (document) => document.id === anchor.document_version_id,
                )?.filename ?? "来源未对应到材料"}
              </small>
            </blockquote>
          ))}
        </section>
      ))}
    </div>
  );
}

function ContractInfoTab({
  documents,
  showDiagnostics,
}: {
  documents: ReviewDocument[];
  showDiagnostics: boolean;
}) {
  return (
    <div className="finding-tab-content">
      {documents.map((document) => (
        <section key={document.id}>
          <h3>{document.filename}</h3>
          <dl>
            <dt>材料角色</dt>
            <dd>{roleLabel(document.role)}</dd>
            <dt>解析状态</dt>
            <dd>{parseLabel(document.parse_status)}</dd>
            {showDiagnostics ? (
              <>
                <dt>原件身份</dt>
                <dd>{document.id}</dd>
                <dt>本次使用的解析结果</dt>
                <dd>{document.parsed_document?.id ?? "未记录"}</dd>
                <dt>原件 SHA-256</dt>
                <dd>{document.digest}</dd>
              </>
            ) : null}
          </dl>
        </section>
      ))}
    </div>
  );
}

function ConclusionTab({ findings }: { findings: Finding[] }) {
  return (
    <div className="finding-tab-content">
      <p>规则结果、模型建议和审查判断分别保存。审查执行结束不代表合同已获批。</p>
      {findings.map((item) => (
        <section key={item.id}>
          <h3>{item.claim}</h3>
          <p>
            {originLabel(item.origin)}，{relationLabel(item.relation)}
          </p>
          {item.decisions.length === 0 ? (
            <p>尚未记录审查判断。</p>
          ) : (
            item.decisions.map((decision) => (
              <article key={decision.id}>
                <strong>{decisionLabel(decision.action)}</strong>
                <p>{decision.reason}</p>
                <small>
                  {decision.actor}，{formatDateTime(decision.decided_at)}
                </small>
              </article>
            ))
          )}
        </section>
      ))}
    </div>
  );
}

function FindingCircuit({ finding }: { finding: Finding }) {
  const located = finding.evidence.filter(
    (anchor) => anchor.status === "located",
  ).length;
  const decision = latestDecision(finding);
  const sourceOpen =
    finding.evidence.length === 0 || located !== finding.evidence.length;
  const nodes = [
    {
      label: "本次审查原文",
      value: String(finding.evidence.length),
      meta: "处引用",
      state: sourceOpen ? "open" : "filled",
    },
    {
      label: "定位",
      value: `${located}/${finding.evidence.length}`,
      meta: "已对上原文",
      state: sourceOpen ? "open" : "filled",
    },
    {
      label: "建议",
      value: originLabel(finding.origin),
      meta: finding.verification_status === "unverified" ? "尚未核验" : "内容含义待核实",
      state: "open",
    },
    {
      label: "审查判断",
      value: decision ? decisionLabel(decision.action) : "待裁决",
      meta: decision ? formatDateTime(decision.decided_at) : "尚未记录",
      state: decision ? "filled" : "open",
    },
  ];
  return (
    <ol className="finding-circuit" aria-label="线索链路">
      {nodes.map((node) => (
        <li key={node.label} data-state={node.state}>
          <span className="finding-circuit-mark" aria-hidden="true" />
          <span className="finding-circuit-label">{node.label}</span>
          <strong className="finding-circuit-value">{node.value}</strong>
          <span className="finding-circuit-meta">{node.meta}</span>
        </li>
      ))}
    </ol>
  );
}

function FindingDetailHeader({
  finding,
  showDiagnostics,
}: {
  finding: Finding;
  showDiagnostics: boolean;
}) {
  const confidence = confidenceLabel(finding.confidence);
  return (
    <>
      <h3>{finding.claim || "未命名线索"}</h3>
      <p className="meta">
        {originLabel(finding.origin)}，{relationLabel(finding.relation)}
        {confidence ? `，${confidence}` : ""}
        {finding.domain ? `，${finding.domain}` : ""}
      </p>
      <FindingCircuit finding={finding} />
      {finding.severity_source ? (
        <p className="finding-grade-source">
          {SEVERITY_LABELS[finding.severity ?? "unrated"]}，{" "}
          {showDiagnostics ? (
            <>
              {finding.severity_source.policy_id} v
              {finding.severity_source.policy_version} 第
              {finding.severity_source.clause}条：
            </>
          ) : null}
          {finding.severity_source.reason}
        </p>
      ) : null}
      {finding.unverified_reason ? (
        <p className="callout">尚未核验：{finding.unverified_reason}</p>
      ) : null}
    </>
  );
}

function sourceForAnchor(
  anchor: EvidenceAnchor,
  documents: ReviewDocument[],
): ReviewDocument | null {
  return (
    documents.find((document) =>
      evidenceSourceMatches(
        anchor,
        document.id,
        document.parsed_document,
        document.parsed_document_id,
      ),
    ) ??
    documents.find((document) => document.id === anchor.document_version_id) ??
    null
  );
}

function anchorLabel(anchor: EvidenceAnchor, located: boolean): string {
  if (located) return "已定位";
  if (anchor.status === "unlocatable") return "无法定位";
  if (anchor.status === "anchor_mismatch") return "定位失效";
  if (anchor.status === "located") return "未能在正文定位";
  return "来源缺失";
}

function EvidenceDiagnostics({ anchor }: { anchor: EvidenceAnchor }) {
  return (
    <p
      className="meta"
      data-evidence-parsed-document-id={anchor.parsed_document_id ?? ""}
      data-evidence-normalized-digest={anchor.normalized_digest ?? ""}
      data-evidence-document-version-id={anchor.document_version_id}
    >
      {anchor.parsed_document_id === null &&
      anchor.normalized_digest === null ? (
        "历史来源未记录解析身份或正文摘要"
      ) : (
        <>
          本次使用的解析结果{" "}
          {anchor.parsed_document_id === null ? (
            "未记录"
          ) : (
            <span title={anchor.parsed_document_id}>
              {shortDigest(anchor.parsed_document_id)}
            </span>
          )}
          {"，"}正文 SHA-256{" "}
          {anchor.normalized_digest === null ? (
            "未记录"
          ) : (
            <span title={anchor.normalized_digest}>
              {shortDigest(anchor.normalized_digest)}
            </span>
          )}
        </>
      )}
    </p>
  );
}

function resolveEvidence(
  anchor: EvidenceAnchor,
  source: ReviewDocument | null,
) {
  return resolveAnchor(
    source?.text ?? null,
    anchor,
    source?.parsed_document ?? null,
    source?.parsed_document_id ?? null,
  );
}

function EvidenceResolution({
  anchor,
  resolved,
  located,
}: {
  anchor: EvidenceAnchor;
  resolved: ReturnType<typeof resolveEvidence>;
  located: boolean;
}) {
  return (
    <>
      {resolved.kind === "snapshot" && !anchor.mismatch_reason ? (
        <p className="callout">{resolved.reason}</p>
      ) : null}
      {resolved.kind === "unavailable" ? (
        <p className="callout">{resolved.reason}</p>
      ) : null}
      {located && anchor.page !== null ? (
        <p className="meta">第 {anchor.page} 页</p>
      ) : null}
      {anchor.mismatch_reason ? <p>{anchor.mismatch_reason}</p> : null}
      {anchor.quote ? (
        <blockquote>{anchor.quote}</blockquote>
      ) : (
        <p>没有保存引用内容。</p>
      )}
    </>
  );
}

function EvidenceCard({
  anchor,
  source,
  showDiagnostics,
}: {
  anchor: EvidenceAnchor;
  source: ReviewDocument | null;
  showDiagnostics: boolean;
}) {
  const resolved = resolveEvidence(anchor, source);
  const located = anchor.status === "located" && resolved.kind === "highlight";
  return (
    <section className="evidence-card">
      <header>
        <strong>{source ? roleLabel(source.role) : "来源未对应到材料"}</strong>
        <span>{anchorLabel(anchor, located)}</span>
      </header>
      {source ? <p className="meta">{source.filename}</p> : null}
      {showDiagnostics ? <EvidenceDiagnostics anchor={anchor} /> : null}
      <EvidenceResolution
        anchor={anchor}
        resolved={resolved}
        located={located}
      />
    </section>
  );
}

function EvidenceList({
  finding,
  documents,
  showDiagnostics,
}: {
  finding: Finding;
  documents: ReviewDocument[];
  showDiagnostics: boolean;
}) {
  return (
    <>
      <h4>证据</h4>
      {finding.evidence.length === 0 ? (
        <p>这条线索没有附带证据，不能视为已验证。</p>
      ) : null}
      <div className="evidence-stack">
        {finding.evidence.map((anchor) => (
          <EvidenceCard
            key={anchor.id}
            anchor={anchor}
            source={sourceForAnchor(anchor, documents)}
            showDiagnostics={showDiagnostics}
          />
        ))}
      </div>
    </>
  );
}

function DecisionHistory({ decisions }: { decisions: HumanDecision[] }) {
  return (
    <>
      <h4>决定记录</h4>
      {decisions.length === 0 ? (
        <p>还没有审查人员的判断。模型建议不会自动结案。</p>
      ) : null}
      <ol className="history">
        {decisions.map((decision) => (
          <li key={decision.id}>
            <p>
              <strong>{decisionLabel(decision.action)}</strong>，{" "}
              {decision.actor}，{" "}
              <time dateTime={decision.decided_at}>
                {formatDateTime(decision.decided_at)}
              </time>
            </p>
            <p>{decision.reason}</p>
          </li>
        ))}
      </ol>
    </>
  );
}

function canSubmitDecision(
  canDecide: boolean,
  action: DecisionAction | null,
  version: number | null,
  reason: string,
  pending: boolean,
): boolean {
  return (
    canDecide &&
    action !== null &&
    version !== null &&
    validateDecisionReason(reason) === null &&
    !pending
  );
}

function DecisionAlerts({
  version,
  reasonError,
  conflictMessage,
  saveError,
}: {
  version: number | null;
  reasonError: string | null;
  conflictMessage: string | null;
  saveError: string | null;
}) {
  return (
    <>
      {version === null ? (
        <p role="status">
          服务没有返回这条线索的版本号，暂不能保存，以免覆盖其他人的决定。
        </p>
      ) : null}
      {reasonError ? <p role="alert">{reasonError}</p> : null}
      {conflictMessage ? <p role="alert">{conflictMessage}</p> : null}
      {saveError ? <p role="alert">{saveError}</p> : null}
    </>
  );
}

function DecisionCard({
  finding,
  action,
  canDecide,
  onAction,
}: {
  finding: Finding;
  action: DecisionAction | null;
  canDecide: boolean;
  onAction: (action: DecisionAction) => void;
}) {
  const correction = finding.decisions.length > 0;
  return (
    <Card
      role="group"
      aria-label={String(correction ? "更正判断" : "审查判断")}
      title={<>{correction ? "更正判断" : "审查判断"}</>}
    >
      {correction ? (
        <p className="meta">修改判断时，之前的记录也会保留。</p>
      ) : null}
      {DECISION_ACTIONS.map((item) => (
        <div key={item.action} className="choice">
          <Radio
            disabled={!canDecide}
            name={`decision-${finding.id}`}
            value={item.action}
            checked={action === item.action}
            onChange={() => onAction(item.action)}
          >
            {item.label}
          </Radio>
        </div>
      ))}
    </Card>
  );
}

function DecisionSubmit({
  pending,
  canSubmit,
}: {
  pending: boolean;
  canSubmit: boolean;
}) {
  return (
    <Button
      aria-label={String(pending ? "正在保存" : "保存决定")}
      type="primary"
      htmlType="submit"
      disabled={!canSubmit}
    >
      {pending ? "正在保存" : "保存决定"}
    </Button>
  );
}

function DecisionForm({
  finding,
  pending,
  canDecide,
  decisionDisabledReason,
  conflictMessage,
  saveError,
  onDecide,
}: {
  finding: Finding;
  pending: boolean;
  canDecide: boolean;
  decisionDisabledReason?: string | null;
  conflictMessage: string | null;
  saveError: string | null;
  onDecide: (input: {
    findingId: string;
    action: DecisionAction;
    reason: string;
    expectedVersion: number;
  }) => Promise<boolean>;
}) {
  const reasonId = useId();
  const hintId = useId();
  const [action, setAction] = useState<DecisionAction | null>(null);
  const [reason, setReason] = useState("");
  const [attempted, setAttempted] = useState(false);
  const reasonError = attempted ? validateDecisionReason(reason) : null;
  const canSubmit = canSubmitDecision(
    canDecide,
    action,
    finding.version,
    reason,
    pending,
  );

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setAttempted(true);
    if (!canSubmitDecision(canDecide, action, finding.version, reason, pending))
      return;
    if (action === null || finding.version === null) return;
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
    <form onSubmit={onSubmit}>
      {!canDecide ? (
        <p>{decisionDisabledReason ?? "当前角色不能提交审查判断。"}</p>
      ) : null}
      <DecisionCard
        finding={finding}
        action={action}
        canDecide={canDecide}
        onAction={setAction}
      />
      <label className="field" htmlFor={reasonId}>
        理由
      </label>
      <Input.TextArea
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
      <DecisionAlerts
        version={finding.version}
        reasonError={reasonError}
        conflictMessage={conflictMessage}
        saveError={saveError}
      />
      <DecisionSubmit pending={pending} canSubmit={canSubmit} />
    </form>
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
  showDiagnostics,
  onDecide,
}: {
  finding: Finding;
  documents: ReviewDocument[];
  pending: boolean;
  canDecide: boolean;
  showDiagnostics: boolean;
  decisionDisabledReason?: string | null;
  conflictMessage: string | null;
  saveError: string | null;
  onDecide: (input: {
    findingId: string;
    action: DecisionAction;
    reason: string;
    expectedVersion: number;
  }) => Promise<boolean>;
}) {
  return (
    <article className="finding-detail" aria-live="polite">
      <FindingDetailHeader finding={finding} showDiagnostics={showDiagnostics} />
      <EvidenceList
        finding={finding}
        documents={documents}
        showDiagnostics={showDiagnostics}
      />
      <DecisionHistory decisions={finding.decisions} />
      <DecisionForm
        finding={finding}
        pending={pending}
        canDecide={canDecide}
        decisionDisabledReason={decisionDisabledReason}
        conflictMessage={conflictMessage}
        saveError={saveError}
        onDecide={onDecide}
      />
    </article>
  );
}

export function FindingPanel({
  findings,
  documents,
  selectedId,
  onSelect,
  showDiagnostics = false,
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
  showDiagnostics?: boolean;
  canDecide?: boolean;
  decisionDisabledReason?: string | null;
  conflictMessage: string | null;
  saveError: string | null;
  onDecide: (input: {
    findingId: string;
    action: DecisionAction;
    reason: string;
    expectedVersion: number;
  }) => Promise<boolean>;
}) {
  const [filter, setFilter] = useState<FindingFilter>("all");
  const [panel, setPanel] = useState("风险审查");
  const visible = filterFindings(findings, filter);
  const selected =
    findings.find((finding) => finding.id === selectedId) ?? null;
  const selectedHidden =
    selected !== null && !visible.some((finding) => finding.id === selected.id);

  return (
    <aside className="col-findings">
      <h2 className="sr-only">审查结果</h2>
      <Tabs
        activeKey={panel}
        onChange={setPanel}
        items={["风险审查", "条款比对", "合同信息", "审查结论"].map(
          (label) => ({ key: label, label }),
        )}
      />
      <div hidden={panel !== "风险审查"}>
        <SeverityCounts findings={findings} />
        <FindingFilterButtons filter={filter} onChange={setFilter} />
        {findings.length === 0 ? (
          <p>本次审查还没有生成风险线索。这不代表材料没有风险。</p>
        ) : null}
        <ul className="finding-list">
          {visible.map((finding) => (
            <FindingListRow
              key={finding.id}
              finding={finding}
              selectedId={selectedId}
              onSelect={onSelect}
            />
          ))}
        </ul>
        {selectedHidden ? (
          <p className="meta">当前线索不在这个筛选结果里，右侧仍保留它。</p>
        ) : null}
        {selected ? (
          <FindingDetail
            key={selected.id}
            finding={selected}
            documents={documents}
            pending={pending}
            showDiagnostics={showDiagnostics}
            canDecide={canDecide}
            decisionDisabledReason={decisionDisabledReason}
            conflictMessage={conflictMessage}
            saveError={saveError}
            onDecide={onDecide}
          />
        ) : null}
      </div>
      {panel === "条款比对" ? (
        <ClauseComparisonTab
          findings={findings}
          documents={documents}
          onSelect={onSelect}
          onPanel={setPanel}
        />
      ) : null}
      {panel === "合同信息" ? (
        <ContractInfoTab documents={documents} showDiagnostics={showDiagnostics} />
      ) : null}
      {panel === "审查结论" ? <ConclusionTab findings={findings} /> : null}
    </aside>
  );
}
