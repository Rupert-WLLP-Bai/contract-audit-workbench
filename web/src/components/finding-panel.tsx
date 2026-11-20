// File metadata (manifest mirror):
// Purpose: Renders risk findings, cited passages, clause suggestions and judgment drafts, and enforces the workspace’s exact citation and keyboard behavior before calling the existing decision command.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-19
// Revision: r7
// Copyright: See LICENSE
// End file metadata.

import { Tabs } from "antd";
import { Card } from "antd";
import { Button, Checkbox, Input, Radio } from "antd";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import type {
  ConstraintEvaluation,
  DecisionAction,
  EvidenceAnchor,
  Finding,
  HumanDecision,
  ReviewDocument,
} from "../api/types";
import { useBatchDecisionDraft, useDecisionDraft } from "../hooks/decision-drafts";
import type { FindingDecisionInput, FindingDecisionOutcome, FindingFilter } from "../domain/review";
import {
  DECISION_ACTIONS,
  confidenceLabel,
  decisionLabel,
  adjacentFindingId,
  findingBatchGroup,
  findingQueue,
  formatDateTime,
  latestDecision,
  nextUndecidedFindingId,
  originLabel,
  parseLabel,
  relationLabel,
  roleLabel,
  sameFindingBatchGroup,
  shortDigest,
  validateDecisionReason,
} from "../domain/review";
import { evidenceSourceMatches, resolveAnchor } from "../domain/text";
import { businessDomainLabel } from "../domain/business-labels";
import { findingPresentation, findingTitle } from "../domain/finding-presentation";
import { FindingBatchControls } from "./finding-batch-controls";
import { TechnicalDetail } from "./technical-detail";
import { ClauseProposalPanel, type ClauseProposalPanelContext } from "./clause-proposal-panel";
import "./finding-panel.css";
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

const WORKSPACE_FILTERS = [
  { id: "undecided", label: "待判断" },
  { id: "decided", label: "已判断" },
  { id: "all", label: "全部" },
] as const;
type WorkspaceFilter = (typeof WORKSPACE_FILTERS)[number]["id"];
const WORKSPACE_DECISION_ACTIONS = [
  { action: "confirm_risk", label: "确认是风险" },
  { action: "false_positive", label: "不构成风险" },
  { action: "insufficient_evidence", label: "依据不够" },
] as const;

function isModelFinding(finding: Finding): boolean {
  return finding.origin === "model" || finding.origin === "ai";
}

function SeverityCounts({ findings }: { findings: Finding[] }) {
  const severities = [
    "high",
    "medium",
    "low",
    "info",
    ...(findings.some((item) => item.severity === null) ? ["unrated"] : []),
  ];
  return (
    <dl className="finding-counts finding-severity-counts" aria-label="本次审查风险分级">
      {severities.map((severity) => (
        <div key={severity} data-severity={severity}>
          <dt>{SEVERITY_LABELS[severity]}</dt>
          <dd>{findings.filter((item) => (item.severity ?? "unrated") === severity).length}</dd>
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
  checked,
  selectionDisabled,
  selectionHint,
  onToggleSelection,
}: {
  finding: Finding;
  selectedId: string | null;
  onSelect: (findingId: string) => void;
  checked: boolean;
  selectionDisabled: boolean;
  selectionHint: string;
  onToggleSelection: (checked: boolean) => void;
}) {
  const decision = latestDecision(finding);
  return (
    <li>
      <div style={{ display: "flex", alignItems: "flex-start", gap: "var(--space-2)" }}>
        <Checkbox
          aria-label={`批量选择线索：${findingTitle(finding)}`}
          title={selectionHint}
          checked={checked}
          disabled={selectionDisabled}
          onChange={(event) => onToggleSelection(event.target.checked)}
        />
        <Button
          htmlType="button"
          className="finding-button"
          data-relation={finding.relation}
          data-severity={finding.severity ?? "unrated"}
          aria-label={`选择线索：${findingTitle(finding)}`}
          aria-current={finding.id === selectedId}
          onClick={() => onSelect(finding.id)}
        >
          <span className="finding-card-title">{findingTitle(finding)}</span>
          <span className="finding-grade" data-severity={finding.severity ?? "unrated"}>
            {SEVERITY_LABELS[finding.severity ?? "unrated"] ?? "未分级"}
          </span>
          <span className="meta">
            {originLabel(finding.origin)}， {relationLabel(finding.relation)}
            {decision ? `，已记录${decisionLabel(decision.action)}` : "，待裁决"}
          </span>
        </Button>
      </div>
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
      <p>下面是各条线索对应的原文片段。</p>
      {findings.map((item) => (
        <section key={item.id}>
          <Button
            aria-label={findingTitle(item)}
            htmlType="button"
            onClick={() => {
              onSelect(item.id);
              onPanel("风险审查");
            }}
          >
            {findingTitle(item)}
          </Button>
          {item.evidence.map((anchor) => (
            <blockquote key={anchor.id}>
              {anchor.quote}
              <small>
                {documents.find((document) => document.id === anchor.document_version_id)
                  ?.filename ?? "来源未对应到材料"}
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
          <TechnicalDetail
            label="材料状态编码"
            value={JSON.stringify({ role: document.role, parse_status: document.parse_status })}
          />
        </section>
      ))}
    </div>
  );
}

function ConclusionTab({ findings }: { findings: Finding[] }) {
  return (
    <div className="finding-tab-content">
      <p>规则结果、模型建议和人工判断分开保存。</p>
      {findings.map((item) => (
        <section key={item.id}>
          <h3>{findingTitle(item)}</h3>
          <p>
            {originLabel(item.origin)}，{relationLabel(item.relation)}
          </p>
          <TechnicalDetail
            label="发现类型编码"
            value={JSON.stringify({
              origin: item.origin,
              relation: item.relation,
              domain: item.domain,
            })}
          />
          {item.decisions.length === 0 ? (
            <p>尚未记录审查判断。</p>
          ) : (
            item.decisions.map((decision) => (
              <article key={decision.id}>
                <strong>{decisionLabel(decision.action)}</strong>
                <p>{decision.reason}</p>
                <TechnicalDetail label="判断类型编码" value={decision.action} />
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
  const located = finding.evidence.filter((anchor) => anchor.status === "located").length;
  const decision = latestDecision(finding);
  const sourceOpen = finding.evidence.length === 0 || located !== finding.evidence.length;
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
  const presentation = findingPresentation(finding);
  return (
    <>
      <h3>{findingTitle(finding)}</h3>
      <p className="meta">
        {originLabel(finding.origin)}，{relationLabel(finding.relation)}
        {confidence ? `，${confidence}` : ""}
        {finding.domain ? `，${businessDomainLabel(finding.domain)}` : ""}
      </p>
      {presentation ? <p className="callout">{presentation.nextStep}</p> : null}
      {presentation ? (
        <TechnicalDetail label="规则原始结果与编号" value={presentation.raw} />
      ) : null}
      <TechnicalDetail
        label="发现类型编码"
        value={JSON.stringify({
          domain: finding.domain,
          origin: finding.origin,
          relation: finding.relation,
          confidence: finding.confidence,
        })}
      />
      <FindingCircuit finding={finding} />
      {finding.severity_source ? (
        <p className="finding-grade-source">
          {SEVERITY_LABELS[finding.severity ?? "unrated"]}，{" "}
          {showDiagnostics ? (
            <>
              {finding.severity_source.policy_id} v{finding.severity_source.policy_version} 第
              {finding.severity_source.clause}条：
            </>
          ) : null}
          {finding.severity_source.reason}
        </p>
      ) : null}
      {finding.unverified_reason &&
      !(presentation && finding.unverified_reason === finding.claim) ? (
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
      {anchor.parsed_document_id === null && anchor.normalized_digest === null ? (
        "历史来源未记录解析身份或正文摘要"
      ) : (
        <>
          本次使用的解析结果{" "}
          {anchor.parsed_document_id === null ? (
            "未记录"
          ) : (
            <span title={anchor.parsed_document_id}>{shortDigest(anchor.parsed_document_id)}</span>
          )}
          {"，"}正文 SHA-256{" "}
          {anchor.normalized_digest === null ? (
            "未记录"
          ) : (
            <span title={anchor.normalized_digest}>{shortDigest(anchor.normalized_digest)}</span>
          )}
        </>
      )}
    </p>
  );
}

function resolveEvidence(anchor: EvidenceAnchor, source: ReviewDocument | null) {
  return resolveAnchor(
    source?.text ?? null,
    anchor,
    source?.parsed_document ?? null,
    source?.parsed_document_id ?? null,
  );
}

function locatedCitationSource(
  anchor: EvidenceAnchor,
  documents: ReviewDocument[],
  parsedDocumentIds: readonly string[],
): ReviewDocument | null {
  if (
    anchor.status !== "located" ||
    !anchor.parsed_document_id ||
    !parsedDocumentIds.includes(anchor.parsed_document_id) ||
    anchor.quote.trim() === ""
  )
    return null;
  const matches = documents.filter(
    (document) =>
      document.id === anchor.document_version_id &&
      document.parsed_document_id === anchor.parsed_document_id &&
      document.parse_status === "ready" &&
      resolveEvidence(anchor, document).kind === "highlight",
  );
  return matches.length === 1 ? matches[0]! : null;
}

function canConfirmRisk(
  finding: Finding,
  documents: ReviewDocument[],
  parsedDocumentIds: readonly string[],
): boolean {
  return (
    !isModelFinding(finding) ||
    (finding.evidence.length > 0 &&
      finding.evidence.every(
        (anchor) => locatedCitationSource(anchor, documents, parsedDocumentIds) !== null,
      ))
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
      {resolved.kind === "unavailable" ? <p className="callout">{resolved.reason}</p> : null}
      {located && anchor.page !== null ? <p className="meta">第 {anchor.page} 页</p> : null}
      {anchor.mismatch_reason ? <p>{anchor.mismatch_reason}</p> : null}
      {anchor.quote ? <blockquote>{anchor.quote}</blockquote> : <p>没有保存引用内容。</p>}
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
      <EvidenceResolution anchor={anchor} resolved={resolved} located={located} />
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
      {finding.evidence.length === 0 ? <p>这条线索没有附带证据，不能视为已验证。</p> : null}
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
      {decisions.length === 0 ? <p>还没有人工判断。</p> : null}
      <ol className="history">
        {decisions.map((decision) => (
          <li key={decision.id}>
            <p>
              <strong>{decisionLabel(decision.action)}</strong>， {decision.actor}，{" "}
              <time dateTime={decision.decided_at}>{formatDateTime(decision.decided_at)}</time>
            </p>
            <p>{decision.reason}</p>
            <TechnicalDetail label="判断类型编码" value={decision.action} />
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
        <p role="status">服务没有返回这条线索的版本号，暂不能保存，以免覆盖其他人的决定。</p>
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
  riskConfirmable,
  immersive,
  onAction,
}: {
  finding: Finding;
  action: DecisionAction | null;
  canDecide: boolean;
  riskConfirmable: boolean;
  immersive: boolean;
  onAction: (action: DecisionAction) => void;
}) {
  const correction = finding.decisions.length > 0;
  const actions = immersive ? WORKSPACE_DECISION_ACTIONS : DECISION_ACTIONS;
  const choices = actions.map((item) => (
    <div key={item.action} className="choice">
      <Radio
        disabled={!canDecide || (item.action === "confirm_risk" && !riskConfirmable)}
        name={`decision-${finding.id}`}
        value={item.action}
        checked={action === item.action}
        onChange={() => onAction(item.action)}
      >
        {item.label}
      </Radio>
    </div>
  ));
  if (immersive)
    return (
      <fieldset className="review-decision-choices">
        <legend>你的判断</legend>
        <div>{choices}</div>
      </fieldset>
    );
  return (
    <Card
      role="group"
      aria-label={String(correction ? "更正判断" : "审查判断")}
      title={<>{correction ? "更正判断" : "审查判断"}</>}
    >
      {correction ? <p className="meta">修改判断时，之前的记录也会保留。</p> : null}
      {choices}
    </Card>
  );
}

function DecisionSubmit({
  pending,
  canSubmit,
  immersive,
}: {
  pending: boolean;
  canSubmit: boolean;
  immersive: boolean;
}) {
  const label = pending ? "正在保存" : immersive ? "保存判断" : "保存决定";
  return (
    <Button aria-label={label} type="primary" htmlType="submit" disabled={!canSubmit}>
      {label}
    </Button>
  );
}

function DecisionForm({
  finding,
  pending,
  canDecide,
  riskConfirmable,
  immersive = false,
  decisionDisabledReason,
  conflictMessage,
  saveError,
  onDecide,
  onSaved,
}: {
  finding: Finding;
  pending: boolean;
  canDecide: boolean;
  riskConfirmable: boolean;
  immersive?: boolean;
  decisionDisabledReason?: string | null;
  conflictMessage: string | null;
  saveError: string | null;
  onDecide: (input: {
    findingId: string;
    action: DecisionAction;
    reason: string;
    expectedVersion: number;
  }) => Promise<boolean>;
  onSaved: (findingId: string, advance: boolean) => void;
}) {
  const reasonId = useId();
  const hintId = useId();
  const draft = useDecisionDraft(finding.id);
  const advanceOnSave = useRef(false);
  const submitting = useRef(false);
  const [attempted, setAttempted] = useState(false);
  const reasonError = attempted ? validateDecisionReason(draft.reason) : null;
  const actionAllowed =
    draft.action === "confirm_risk"
      ? riskConfirmable
      : !(immersive && draft.action === "accept_residual");
  const canSubmit = canSubmitDecision(
    canDecide && actionAllowed,
    draft.action,
    finding.version,
    draft.reason,
    pending,
  );

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setAttempted(true);
    const advance = !immersive || advanceOnSave.current;
    advanceOnSave.current = false;
    if (!canSubmit || submitting.current || draft.action === null || finding.version === null)
      return;
    submitting.current = true;
    try {
      const saved = await onDecide({
        findingId: finding.id,
        action: draft.action,
        reason: draft.reason.trim(),
        expectedVersion: finding.version,
      });
      if (saved) {
        draft.clear();
        setAttempted(false);
        onSaved(finding.id, advance);
      }
    } finally {
      submitting.current = false;
    }
  }

  return (
    <form
      className={immersive ? "review-decision-form" : undefined}
      onSubmit={onSubmit}
      onKeyDown={(event) => {
        if (
          event.key === "Enter" &&
          (event.ctrlKey || event.metaKey) &&
          !event.nativeEvent.isComposing &&
          !event.repeat &&
          !event.altKey &&
          !event.shiftKey
        ) {
          event.preventDefault();
          advanceOnSave.current = true;
          event.currentTarget.requestSubmit();
        }
      }}
    >
      {!canDecide ? <p>{decisionDisabledReason ?? "当前角色不能提交审查判断。"}</p> : null}
      <DecisionCard
        finding={finding}
        action={draft.action}
        canDecide={canDecide && !pending}
        riskConfirmable={riskConfirmable}
        immersive={immersive}
        onAction={(action) => draft.update((current) => ({ ...current, action }))}
      />
      {!riskConfirmable ? (
        <p className="review-citation-unmatched">引用没对上原文，不能确认是风险。</p>
      ) : null}
      <label className="field" htmlFor={reasonId}>
        理由
      </label>
      <Input.TextArea
        id={reasonId}
        value={draft.reason}
        onChange={(event) =>
          draft.update((current) => ({
            ...current,
            reason: event.target.value,
          }))
        }
        aria-describedby={hintId}
        rows={immersive ? 2 : 4}
        disabled={!canDecide || pending}
      />
      <DecisionAlerts
        version={finding.version}
        reasonError={reasonError}
        conflictMessage={conflictMessage}
        saveError={saveError}
      />
      <div className={immersive ? "review-decision-submit" : undefined}>
        <p id={hintId} className="meta">
          {immersive
            ? "Ctrl+Enter 保存并看下一条"
            : "四种决定都要写理由。保存后写入审查记录。按 Ctrl+Enter 可保存。"}
        </p>
        <DecisionSubmit pending={pending} canSubmit={canSubmit} immersive={immersive} />
      </div>
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
  onSaved,
  clauseProposalContext,
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
  onSaved: (findingId: string) => void;
  clauseProposalContext?: ClauseProposalPanelContext;
}) {
  return (
    <article className="finding-detail" aria-live="polite">
      <FindingDetailHeader finding={finding} showDiagnostics={showDiagnostics} />
      <EvidenceList finding={finding} documents={documents} showDiagnostics={showDiagnostics} />
      {clauseProposalContext ? (
        <ClauseProposalPanel {...clauseProposalContext} finding={finding} documents={documents} />
      ) : null}
      <DecisionHistory decisions={finding.decisions} />
      <DecisionForm
        finding={finding}
        riskConfirmable={canConfirmRisk(
          finding,
          documents,
          clauseProposalContext?.frozenParsedDocumentIds ?? [],
        )}
        pending={pending}
        canDecide={canDecide}
        decisionDisabledReason={decisionDisabledReason}
        conflictMessage={conflictMessage}
        saveError={saveError}
        onDecide={onDecide}
        onSaved={onSaved}
      />
    </article>
  );
}

function WorkspaceFindingDetails({
  finding,
  documents,
  onSelectCitation,
  clauseProposalContext,
}: {
  finding: Finding;
  documents: ReviewDocument[];
  onSelectCitation?: (findingId: string, anchor: EvidenceAnchor) => void;
  clauseProposalContext?: ClauseProposalPanelContext;
}) {
  const presentation = findingPresentation(finding);
  const parseIds = clauseProposalContext?.frozenParsedDocumentIds ?? [];
  const title = findingTitle(finding);
  const reasons = new Set(
    [presentation?.nextStep, finding.severity_source?.reason, finding.unverified_reason]
      .map((text) => text?.trim())
      .filter((text) => text && text !== title && text !== finding.claim),
  );
  return (
    <div className="review-finding-detail">
      {finding.claim !== title ? <p>{finding.claim}</p> : null}
      {Array.from(reasons, (reason) => (
        <p key={reason} className="meta">
          {reason}
        </p>
      ))}
      {finding.evidence.length === 0 ? <p className="meta">未附原文引用</p> : null}
      <ul className="review-finding-citations" aria-label="原文引用">
        {finding.evidence.map((anchor) => {
          const source = locatedCitationSource(anchor, documents, parseIds);
          const label =
            source?.filename ?? sourceForAnchor(anchor, documents)?.filename ?? "材料未对应";
          return (
            <li
              key={anchor.id}
              data-evidence-document-version-id={anchor.document_version_id}
              data-evidence-parsed-document-id={anchor.parsed_document_id ?? undefined}
            >
              {source && onSelectCitation ? (
                <button type="button" onClick={() => onSelectCitation(finding.id, anchor)}>
                  {label} · <span className="review-citation-matched">原文已对上</span>
                </button>
              ) : (
                <span>
                  {label} ·{" "}
                  <span
                    className={source ? "review-citation-matched" : "review-citation-unmatched"}
                  >
                    {source ? "原文已对上" : "引用没对上原文"}
                  </span>
                </span>
              )}
              {anchor.quote && <q>{anchor.quote}</q>}
            </li>
          );
        })}
      </ul>
      {clauseProposalContext ? (
        <ClauseProposalPanel
          {...clauseProposalContext}
          finding={finding}
          documents={documents}
          immersive
        />
      ) : null}
    </div>
  );
}

export function FindingPanel({
  immersive = false,
  findings,
  evaluations = [],
  documents,
  selectedId,
  onSelect,
  onSelectCitation,
  showDiagnostics = false,
  pending,
  canDecide = true,
  decisionDisabledReason,
  conflictMessage,
  saveError,
  onDecide,
  onBatchDecide,
  requestedFindingId = null,
  navigationSerial = 0,
  clauseProposalContext,
}: {
  immersive?: boolean;
  findings: Finding[];
  evaluations?: ConstraintEvaluation[];
  documents: ReviewDocument[];
  selectedId: string | null;
  onSelect: (findingId: string) => void;
  onSelectCitation?: (findingId: string, anchor: EvidenceAnchor) => void;
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
  onBatchDecide?: (input: FindingDecisionInput) => Promise<FindingDecisionOutcome>;
  requestedFindingId?: string | null;
  navigationSerial?: number;
  clauseProposalContext?: ClauseProposalPanelContext;
}) {
  const [filter, setFilter] = useState<FindingFilter>("all");
  const [workspaceFilter, setWorkspaceFilter] = useState<WorkspaceFilter>(() =>
    findings.some((finding) => finding.id === selectedId && latestDecision(finding))
      ? "all"
      : "undecided",
  );
  const [panel, setPanel] = useState("风险审查");
  useEffect(() => {
    if (!requestedFindingId) return;
    const match = findings.filter((finding) => finding.id === requestedFindingId);
    if (match.length !== 1) return;
    setPanel("风险审查");
    if (immersive) {
      const targetFilter = latestDecision(match[0]!) ? "decided" : "undecided";
      setWorkspaceFilter((current) => (current === targetFilter ? current : "all"));
    }
  }, [requestedFindingId, navigationSerial, findings, immersive]);
  const allFindings = findingQueue(findings, "all");
  const pendingFindings = allFindings.filter((finding) => latestDecision(finding) === null);
  const decidedFindings = allFindings.filter((finding) => latestDecision(finding) !== null);
  const workspaceQueues = {
    all: allFindings,
    undecided: pendingFindings,
    decided: decidedFindings,
  };
  const visible = immersive ? workspaceQueues[workspaceFilter] : findingQueue(findings, filter);
  const batch = useBatchDecisionDraft();
  const parsedDocumentIds = clauseProposalContext?.frozenParsedDocumentIds ?? [];
  const [busyScope, setBusyScope] = useState<{ key: string | null } | null>(null);
  const batchPending = busyScope !== null && busyScope.key === batch.scopeKey;
  const scopeEpoch = useRef(0);
  const submittingScopes = useRef(new Set<string | null>());
  const selectedRef = useRef(selectedId);
  const scopeRef = useRef(batch.scopeKey);
  useEffect(() => {
    selectedRef.current = selectedId;
    scopeRef.current = batch.scopeKey;
  }, [selectedId, batch.scopeKey]);
  useEffect(
    () => () => {
      scopeEpoch.current += 1;
    },
    [batch.scopeKey],
  );
  const selectedForBatch = batch.selectedIds.flatMap((id) => {
    const finding = findings.find((item) => item.id === id);
    return finding ? [finding] : [];
  });
  const group = selectedForBatch[0] ? findingBatchGroup(selectedForBatch[0], evaluations) : null;
  const membersValid =
    selectedForBatch.length === batch.selectedIds.length &&
    selectedForBatch.length > 0 &&
    group !== null &&
    selectedForBatch.every(
      (finding) =>
        finding.version !== null &&
        sameFindingBatchGroup(group, findingBatchGroup(finding, evaluations)),
    );
  const retryingSingle =
    batch.selectedIds.length === 1 &&
    batch.results.some(
      (result) => result.findingId === batch.selectedIds[0] && result.status !== "success",
    );
  const needsReconfirmation = batch.results.some((result) => {
    if (result.status === "success" || !batch.selectedIds.includes(result.findingId)) return false;
    const current = findings.find((finding) => finding.id === result.findingId);
    return current?.version == null || result.confirmedVersion !== current.version;
  });
  const canBatchSubmit =
    Boolean(onBatchDecide) &&
    canDecide &&
    !pending &&
    !batchPending &&
    (batch.selectedIds.length >= 2 || retryingSingle) &&
    membersValid &&
    !needsReconfirmation &&
    batch.action !== null &&
    (batch.action !== "confirm_risk" ||
      selectedForBatch.every((finding) => canConfirmRisk(finding, documents, parsedDocumentIds))) &&
    validateDecisionReason(batch.reason) === null;
  const previousId = adjacentFindingId(visible, selectedId, -1);
  const nextId = adjacentFindingId(visible, selectedId, 1);
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.isComposing ||
        event.repeat ||
        (immersive ? event.altKey : !event.altKey) ||
        event.ctrlKey ||
        event.metaKey ||
        event.shiftKey ||
        panel !== "风险审查" ||
        pending ||
        batchPending
      )
        return;
      const target = event.target;
      if (
        target instanceof Element &&
        target.closest(
          "input, textarea, select, [contenteditable]:not([contenteditable='false']), [role='dialog']",
        )
      )
        return;
      const key = event.key.toLowerCase();
      const targetId = key === "j" ? nextId : key === "k" ? previousId : null;
      if (targetId === null) return;
      event.preventDefault();
      onSelect(targetId);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [batchPending, immersive, nextId, onSelect, panel, pending, previousId]);

  async function submitBatch() {
    const action = batch.action;
    if (!canBatchSubmit || !onBatchDecide || action === null) return;
    const items: { finding: Finding; input: FindingDecisionInput }[] = [];
    for (const finding of selectedForBatch) {
      if (finding.version === null) return;
      items.push({
        finding,
        input: {
          findingId: finding.id,
          action,
          reason: batch.reason.trim(),
          expectedVersion: finding.version,
        },
      });
    }
    const epoch = scopeEpoch.current;
    if (submittingScopes.current.has(batch.scopeKey)) return;
    submittingScopes.current.add(batch.scopeKey);
    const startingSelection = selectedId;
    const successes = new Set<string>();
    let stopped = false;
    setBusyScope({ key: batch.scopeKey });
    for (const { finding, input } of items) {
      let outcome: FindingDecisionOutcome;
      if (scopeEpoch.current !== epoch || stopped) {
        outcome = { status: "failed", message: "未提交：审查范围或权限已变化。" };
      } else {
        try {
          outcome = await onBatchDecide(input);
        } catch (error) {
          outcome = {
            status: "failed",
            message: error instanceof Error ? error.message : "请求没有完成",
          };
        }
      }
      if (outcome.stop) stopped = true;
      if (outcome.status === "success") successes.add(finding.id);
      batch.update((current) => ({
        ...current,
        selectedIds:
          outcome.status === "success"
            ? current.selectedIds.filter((id) => id !== finding.id)
            : current.selectedIds,
        results: [
          ...current.results.filter((result) => result.findingId !== finding.id),
          {
            findingId: finding.id,
            claim: findingTitle(finding),
            status: outcome.status,
            message: outcome.message,
            confirmedVersion: null,
          },
        ],
      }));
    }
    if (successes.size === items.length) {
      batch.update((current) => ({ ...current, action: null, reason: "" }));
    }
    submittingScopes.current.delete(batch.scopeKey);
    if (scopeEpoch.current !== epoch) return;
    setBusyScope(null);
    if (
      startingSelection &&
      selectedRef.current === startingSelection &&
      successes.has(startingSelection)
    ) {
      const remaining = visible.filter(
        (finding) => finding.id === startingSelection || !successes.has(finding.id),
      );
      const next = nextUndecidedFindingId(remaining, startingSelection);
      if (next !== null) onSelect(next);
    }
  }
  const selected = findings.find((finding) => finding.id === selectedId) ?? null;
  const selectedHidden =
    selected !== null && !visible.some((finding) => finding.id === selected.id);

  if (immersive) {
    return (
      <section className="col-findings review-finding-panel" aria-label="风险线索">
        <header className="review-finding-panel-header">
          <h2>风险线索</h2>
          <span className="meta">J / K 切换</span>
        </header>
        <div className="review-finding-filters" role="group" aria-label="筛选线索">
          {WORKSPACE_FILTERS.map(({ id, label }) => (
            <button
              key={id}
              type="button"
              aria-label={label}
              aria-pressed={workspaceFilter === id}
              onClick={() => {
                setWorkspaceFilter(id);
                const queue = workspaceQueues[id];
                if (!queue.some((finding) => finding.id === selectedId) && queue[0])
                  onSelect(queue[0].id);
              }}
            >
              {label} <span>{workspaceQueues[id].length}</span>
            </button>
          ))}
        </div>
        <div className="review-finding-queue">
          {visible.length === 0 ? (
            <p className="meta">
              {findings.length ? "当前筛选没有线索" : "本次审查还没有风险线索"}
            </p>
          ) : null}
          <ul>
            {visible.map((finding) => {
              const decision = latestDecision(finding);
              const model = isModelFinding(finding);
              const active = finding.id === selectedId;
              const severity = finding.severity ?? "unrated";
              return (
                <li
                  key={finding.id}
                  data-finding-id={finding.id}
                  data-source={model && !decision ? "model" : "record"}
                  data-selected={active}
                >
                  <button
                    className="review-finding-select finding-button"
                    type="button"
                    aria-label={`选择线索：${findingTitle(finding)}`}
                    aria-expanded={active}
                    onClick={() => onSelect(finding.id)}
                  >
                    <span className="finding-grade" data-severity={severity}>
                      {SEVERITY_LABELS[severity] ?? "未分级"}
                    </span>
                    <strong>{findingTitle(finding)}</strong>
                    {model ? <span className="review-finding-ai">AI 建议</span> : null}
                  </button>
                  {decision ? (
                    <p className="review-finding-decision-summary">
                      已判断 · {decision.actor} ·{" "}
                      {WORKSPACE_DECISION_ACTIONS.find(({ action }) => action === decision.action)
                        ?.label ?? decisionLabel(decision.action)}{" "}
                      ·{" "}
                      <time dateTime={decision.decided_at}>
                        {formatDateTime(decision.decided_at)}
                      </time>
                    </p>
                  ) : null}
                  {active ? (
                    <WorkspaceFindingDetails
                      finding={finding}
                      documents={documents}
                      onSelectCitation={onSelectCitation}
                      clauseProposalContext={clauseProposalContext}
                    />
                  ) : null}
                </li>
              );
            })}
          </ul>
        </div>
        {selected && !selectedHidden ? (
          <DecisionForm
            key={`${batch.scopeKey ?? ""}:${selected.id}`}
            immersive
            finding={selected}
            pending={pending}
            canDecide={canDecide}
            riskConfirmable={canConfirmRisk(selected, documents, parsedDocumentIds)}
            decisionDisabledReason={decisionDisabledReason}
            conflictMessage={conflictMessage}
            saveError={saveError}
            onDecide={onDecide}
            onSaved={(findingId, advance) => {
              if (scopeRef.current !== batch.scopeKey || selectedRef.current !== findingId) return;
              const next = advance ? nextUndecidedFindingId(allFindings, findingId) : null;
              if (next !== null) {
                if (workspaceFilter === "decided") setWorkspaceFilter("undecided");
                onSelect(next);
              } else if (workspaceFilter === "undecided") setWorkspaceFilter("all");
            }}
          />
        ) : null}
      </section>
    );
  }

  return (
    <aside className="col-findings">
      <h2 className="sr-only">审查结果</h2>
      <Tabs
        activeKey={panel}
        onChange={setPanel}
        items={["风险审查", "条款比对", "合同信息", "审查结论"].map((label) => ({
          key: label,
          label,
        }))}
      />
      <div hidden={panel !== "风险审查"}>
        {selected ? (
          <div className="review-selected-finding" aria-label="当前选中线索">
            <span className="meta">当前线索</span>
            <strong>{findingTitle(selected)}</strong>
            {findingPresentation(selected) ? (
              <p>{findingPresentation(selected)?.nextStep}</p>
            ) : null}
          </div>
        ) : null}
        <SeverityCounts findings={findings} />
        <FindingFilterButtons filter={filter} onChange={setFilter} />
        <p className="meta" aria-live="polite">
          当前筛选显示 {visible.length} / {findings.length} 条线索。
        </p>
        <div className="filter-row" role="group" aria-label="浏览线索队列">
          <Button
            htmlType="button"
            disabled={pending || batchPending || previousId === null}
            onClick={() => previousId !== null && onSelect(previousId)}
          >
            上一条
          </Button>
          <Button
            htmlType="button"
            disabled={pending || batchPending || nextId === null}
            onClick={() => nextId !== null && onSelect(nextId)}
          >
            下一条
          </Button>
        </div>
        <p className="meta">待裁决优先，再按严重度排序。Alt+J 下一条，Alt+K 上一条。</p>
        {findings.length === 0 ? <p>这次审查还没有生成风险线索。</p> : null}
        {findings.length > 0 && visible.length === 0 ? <p>当前筛选条件下没有线索。</p> : null}
        <ul className="finding-list">
          {visible.map((finding) => {
            const checked = batch.selectedIds.includes(finding.id);
            const candidateGroup = findingBatchGroup(finding, evaluations);
            const compatible =
              candidateGroup !== null &&
              (batch.selectedIds.length === 0 || sameFindingBatchGroup(group, candidateGroup));
            const selectionDisabled =
              pending ||
              batchPending ||
              (!checked &&
                (!canDecide || !onBatchDecide || finding.version === null || !compatible));
            const selectionHint =
              candidateGroup === null
                ? "缺少唯一关联规则，不能批量裁决"
                : finding.version === null
                  ? "缺少版本号，不能批量裁决"
                  : !compatible
                    ? "只能选择同一规则、同类结论的线索"
                    : "勾选参与批量裁决";
            return (
              <FindingListRow
                key={finding.id}
                finding={finding}
                selectedId={selectedId}
                onSelect={onSelect}
                checked={checked}
                selectionDisabled={selectionDisabled}
                selectionHint={selectionHint}
                onToggleSelection={(nextChecked) =>
                  batch.update((current) => ({
                    ...current,
                    selectedIds: nextChecked
                      ? current.selectedIds.includes(finding.id)
                        ? current.selectedIds
                        : [...current.selectedIds, finding.id]
                      : current.selectedIds.filter((id) => id !== finding.id),
                  }))
                }
              />
            );
          })}
        </ul>
        <FindingBatchControls
          draft={batch}
          selected={selectedForBatch}
          selectedId={selectedId}
          group={group}
          membersValid={membersValid}
          retryingSingle={retryingSingle}
          needsReconfirmation={needsReconfirmation}
          canSubmit={canBatchSubmit}
          canDecide={Boolean(onBatchDecide) && canDecide}
          pending={pending || batchPending}
          onSelect={onSelect}
          onSubmit={() => void submitBatch()}
        />
        {selectedHidden ? <p className="meta">当前线索不在这个筛选结果里，右侧仍保留它。</p> : null}
        {selected ? (
          <FindingDetail
            key={selected.id}
            finding={selected}
            documents={documents}
            pending={pending || batchPending}
            showDiagnostics={showDiagnostics}
            canDecide={canDecide}
            decisionDisabledReason={decisionDisabledReason}
            conflictMessage={conflictMessage}
            saveError={saveError}
            onDecide={onDecide}
            onSaved={(findingId) => {
              if (scopeRef.current !== batch.scopeKey || selectedRef.current !== findingId) return;
              const next = nextUndecidedFindingId(visible, findingId);
              if (next !== null) onSelect(next);
            }}
            clauseProposalContext={clauseProposalContext}
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
