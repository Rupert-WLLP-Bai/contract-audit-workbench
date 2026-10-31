import { Button, Card, Input, Select } from "antd";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type SyntheticEvent,
} from "react";
import {
  getExperienceCandidate,
  getExperienceDistillation,
  getExperienceFeedback,
  requestExperienceDistillation,
  submitExperienceFeedback,
  type ExperienceCandidate,
  type ExperienceDistillationStatus,
  type ExperienceEvidenceRef,
  type ExperienceFeedback,
  type ExperienceFeedbackInput,
} from "../api/experience-feedback";
import { ApiError, errorMessage } from "../api/http";
import type { Finding, HumanDecision, ReviewDocument } from "../api/types";
import { DECISION_ACTIONS, decisionLabel, formatDateTime, roleLabel } from "../domain/review";
import { sliceByCodePoints } from "../domain/text";
import {
  createFrozenQuoteSelection,
  frozenQuoteMatchesDocument,
  type FrozenQuoteScope,
  type FrozenQuoteSelection,
} from "../domain/frozen-quote";

export interface ExperienceFeedbackPanelProps {
  caseId: string;
  runId: string;
  attemptId: string | null;
  finding: Finding | null;
  documents: ReviewDocument[];
  canSubmit: boolean;
  quoteSelection?: FrozenQuoteSelection | null;
  quoteScope?: FrozenQuoteScope | null;
}

type SavedTracking = { feedbackId: string; jobId: string | null; distillKey: string | null };
type Selection = {
  sourceKey: string;
  text: string;
  caseId: string;
  runId: string;
  attemptId: string | null;
  digest: string;
  evidence: ExperienceEvidenceRef;
};

function distillationBlocked(
  canSubmit: boolean,
  requesting: boolean,
): boolean {
  return !canSubmit || requesting;
}

function recoveryBlocked(
  manualFeedbackId: string,
  recovering: boolean,
): boolean {
  return !manualFeedbackId.trim() || recovering;
}

function selectFromSource(
  source: ReviewDocument | undefined,
  event: SyntheticEvent<HTMLTextAreaElement>,
  onSelection: (selection: Selection | null) => void,
  scope: FrozenQuoteScope | null,
) {
  if (!source) return;
  onSelection(selectionFromTextArea(source, event.currentTarget, scope));
}

function savedJobId(saved: SavedTracking | null): string | null {
  return saved?.jobId ?? null;
}

function savedDistillKey(saved: SavedTracking | null): string | null {
  return saved?.distillKey ?? null;
}

function displayTextOf(source: ReviewDocument | undefined): string {
  return source?.text?.replace(/\r\n?/g, "\n") ?? "";
}

function storageKey(props: ExperienceFeedbackPanelProps): string {
  return `experience-feedback:${JSON.stringify([props.caseId, props.runId, props.attemptId, props.finding?.id ?? null])}`;
}

function trackingText(value: unknown): string | null {
  return typeof value === "string" && value.trim() ? value : null;
}

function parseTracking(value: unknown): SavedTracking | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const item = value as Record<string, unknown>;
  const feedbackId = trackingText(item.feedbackId);
  if (!feedbackId) return null;
  return {
    feedbackId,
    jobId: trackingText(item.jobId),
    distillKey: trackingText(item.distillKey),
  };
}

function loadTracking(key: string): SavedTracking | null {
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return null;
    return parseTracking(JSON.parse(raw));
  } catch {
    return null;
  }
}

function saveTracking(key: string, value: SavedTracking | null) {
  try {
    if (value === null) sessionStorage.removeItem(key);
    else sessionStorage.setItem(key, JSON.stringify(value));
  } catch {
    // Storage can be disabled; the visible identities still allow manual recovery.
  }
}

function parseId(document: ReviewDocument): string | null {
  return document.parsed_document_id ?? null;
}

function sourceKey(document: ReviewDocument): string {
  return JSON.stringify([document.id, parseId(document)]);
}

function parsedSourceMatches(document: ReviewDocument, id: string): boolean {
  const parsed = document.parsed_document;
  return (
    parsed === null ||
    (parsed.status === "ready" &&
      parsed.document_id === document.id &&
      parsed.id === id)
  );
}

function sourceBaseUsable(document: ReviewDocument, id: string): boolean {
  return (
    document.parse_status === "ready" &&
    document.text !== null &&
    document.text !== "" &&
    document.id.trim() !== "" &&
    id.trim() !== ""
  );
}

function sourceUsable(document: ReviewDocument): boolean {
  const id = parseId(document);
  if (id === null) return false;
  return sourceBaseUsable(document, id) && parsedSourceMatches(document, id);
}

// Textarea selection positions count UTF-16 units and normalize CRLF to LF.
// Convert to the original frozen string before computing the server's rune offsets.
function originalOffset(text: string, displayedOffset: number): number | null {
  let source = 0;
  let displayed = 0;
  while (displayed < displayedOffset && source < text.length) {
    source += text[source] === "\r" && text[source + 1] === "\n" ? 2 : 1;
    displayed += 1;
  }
  return displayed === displayedOffset ? source : null;
}

function displayedSelectionIsReal(
  document: ReviewDocument,
  element: HTMLTextAreaElement,
): boolean {
  const text = document.text;
  return (
    text !== null &&
    element.selectionStart !== element.selectionEnd &&
    element.value === text.replace(/\r\n?/g, "\n")
  );
}

function selectionFromTextArea(
  document: ReviewDocument,
  element: HTMLTextAreaElement,
  scope: FrozenQuoteScope | null,
): Selection | null {
  const text = document.text;
  const parsedId = parseId(document);
  if (text === null || parsedId === null || !displayedSelectionIsReal(document, element)) return null;
  const startIndex = originalOffset(text, element.selectionStart);
  const endIndex = originalOffset(text, element.selectionEnd);
  if (startIndex === null || endIndex === null) return null;
  const quote = text.slice(startIndex, endIndex);
  const start = Array.from(text.slice(0, startIndex)).length;
  const end = Array.from(text.slice(0, endIndex)).length;
  const frozen = createFrozenQuoteSelection(scope, document, { start, end });
  if (!frozen || frozen.quote !== quote) return null;
  return selectionFromFrozenQuote(frozen, document);
}

function selectionFromFrozenQuote(
  selection: FrozenQuoteSelection,
  document: ReviewDocument,
): Selection {
  return {
    sourceKey: sourceKey(document),
    text: document.text!,
    caseId: selection.caseId,
    runId: selection.runId,
    attemptId: selection.attemptId,
    digest: selection.digest,
    evidence: {
      document_id: selection.documentId,
      parsed_document_id: selection.parsedDocumentId,
      quote: selection.quote,
      start: selection.start,
      end: selection.end,
    },
  };
}
function selectionFromPanelQuote(
  selection: FrozenQuoteSelection | null,
  scope: FrozenQuoteScope | null,
  sources: ReviewDocument[],
  caseId: string,
  runId: string,
  attemptId: string | null,
): Selection | null {
  if (selection === null || scope === null || !attemptId ||
    scope.caseId !== caseId || scope.runId !== runId ||
    scope.attemptId !== attemptId || !scope.isFrozenAttempt ||
    selection.caseId !== caseId || selection.runId !== runId ||
    selection.attemptId !== attemptId) return null;
  const source = sources.find((document) => document.id === selection.documentId &&
    document.parsed_document_id === selection.parsedDocumentId);
  if (!frozenQuoteMatchesDocument(selection, scope, source)) return null;
  return selectionFromFrozenQuote(selection, source);
}

function feedbackMatchesSource(item: ExperienceFeedback, props: ExperienceFeedbackPanelProps): boolean {
  return item.case_id === props.caseId && item.run_id === props.runId &&
    item.attempt_id === props.attemptId && item.finding_id === props.finding?.id;
}

function ErrorNotice({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <div role="alert" className="callout">
      <p>{errorMessage(error)}</p>
      {error instanceof ApiError ? (
        <>
          <p>错误代码：{error.code}（HTTP {error.status}）</p>
          {error.details !== null && error.details !== undefined ? (
            <details><summary>错误详情</summary><pre>{JSON.stringify(error.details, null, 2)}</pre></details>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

function CandidateList({ items }: { items: string[] | null }) {
  if (items === null) return <span>未提供</span>;
  if (items.length === 0) return <span>无</span>;
  return <ul>{items.map((item, index) => <li key={`${index}:${item}`}>{item || "未提供"}</li>)}</ul>;
}

function CandidateFields({ candidate }: { candidate: ExperienceCandidate }) {
  const { content } = candidate;
  return (
    <dl className="execution-dl">
      <div><dt>检查动作</dt><dd>{content.check_action || "未提供"}</dd></div>
      <div><dt>适用条件</dt><dd><CandidateList items={content.applicability} /></dd></div>
      <div><dt>排除条件</dt><dd><CandidateList items={content.exclusions} /></dd></div>
      <div><dt>最低证据</dt><dd><CandidateList items={content.minimum_evidence} /></dd></div>
      <div><dt>反例</dt><dd><CandidateList items={content.counterexamples} /></dd></div>
      <div><dt>有效期</dt><dd>{content.valid_from || "未提供"} 至 {content.valid_until || "未提供"}</dd></div>
      <div><dt>审查差异</dt><dd>{content.review_diff || "未提供"}</dd></div>
      <div><dt>引用制度版本</dt><dd>{content.policy_refs === null ? "未提供" : content.policy_refs.length ? `已引用 ${content.policy_refs.length} 项制度版本` : "无"}</dd></div>
    </dl>
  );
}

function CandidateView({ candidate, completed }: { candidate: ExperienceCandidate; completed: boolean }) {
  const query = new URLSearchParams({
    source_case: candidate.case_id,
    candidate_id: candidate.candidate_id,
    candidate_version_id: candidate.version_id,
    candidate_content_sha256: candidate.content_sha256,
  });
  const path = `/admin/governed-knowledge/new?${query.toString()}`;
  const adminBase = import.meta.env.VITE_ADMIN_URL;
  const governanceUrl = adminBase ? new URL(path, adminBase).href : path;
  return (
    <section aria-label="经验候选内容">
      <h4>已生成经验候选</h4>
      <p>候选仅供后续治理审核，不代表批准或发布。</p>
      <CandidateFields candidate={candidate} />
      {completed ? <p><a href={governanceUrl}>在知识治理中审阅此候选</a></p> : null}
    </section>
  );
}

const STATUS_LABELS: Record<string, string> = {
  queued: "排队中",
  running: "提炼中",
  completed: "提炼完成",
  failed: "提炼失败",
  cancelled: "已取消",
};

function FeedbackNotices({
  finding,
  attemptId,
  canSubmit,
  decisions,
  sources,
  recovering,
}: {
  finding: Finding | null;
  attemptId: string | null;
  canSubmit: boolean;
  decisions: HumanDecision[];
  sources: ReviewDocument[];
  recovering: boolean;
}) {
  return (
    <>
      {!finding ? <p>请选择一条审查线索。</p> : null}
      {!attemptId ? <p>当前审查缺少可用的执行记录，暂时不能提交反馈。</p> : null}
      {!canSubmit ? <p>当前角色或审查尝试不能提交经验反馈；权限由服务端核验。</p> : null}
      {finding && decisions.length === 0 ? <p>当前线索没有可用于反馈的审查判断。</p> : null}
      {finding && sources.length === 0 ? <p>本次执行没有可供选择的已解析原文。</p> : null}
      {recovering ? <p role="status">正在读取已保存的反馈…</p> : null}
    </>
  );
}

function DecisionSelect({
  decisions,
  value,
  onChange,
  disabled,
}: {
  decisions: HumanDecision[];
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
}) {
  return (
    <label className="field">
      审查判断
      <Select aria-label="审查判断" value={value || undefined} placeholder="选择已记录的决定"
        disabled={disabled}
        onChange={onChange}
        options={decisions.map((decision, index) => ({ value: decision.id, label: `第 ${index + 1} 条 · ${decisionLabel(decision.action)} · ${decision.actor} · ${formatDateTime(decision.decided_at)}` }))} />
    </label>
  );
}

function SourceSelect({
  sources,
  value,
  onChange,
  disabled,
}: {
  sources: ReviewDocument[];
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
}) {
  return (
    <label className="field">
      本次审查材料
      <Select aria-label="本次审查材料" value={value || undefined} placeholder="选择材料"
        disabled={disabled}
        onChange={onChange}
        options={sources.map((document, index) => ({ value: sourceKey(document), label: `第 ${index + 1} 份 · ${document.filename} · ${roleLabel(document.role)}` }))} />
    </label>
  );
}

function EvidencePicker({
  source,
  displayText,
  selection,
  validEvidence,
  chosenEvidence,
  canSubmit,
  pending,
  selectedSource,
  onSelect,
  onChoose,
  canUseFrozenSelection,
  onUseFrozenSelection,
}: {
  source: ReviewDocument | undefined;
  displayText: string;
  selection: Selection | null;
  validEvidence: boolean;
  chosenEvidence: Selection | null;
  canSubmit: boolean;
  pending: boolean;
  selectedSource: string;
  onSelect: (event: SyntheticEvent<HTMLTextAreaElement>) => void;
  onChoose: () => void;
  canUseFrozenSelection: boolean;
  onUseFrozenSelection: () => void;
}) {
  return (
    <>
      {source && source.text !== null ? (
        <>
          <label className="field">
            本次审查使用的原文（选择一段后点击选用）
            <Input.TextArea aria-label="本次审查使用的原文" readOnly value={displayText} rows={6}
              onSelect={onSelect} />
          </label>
          <p className="meta">可用鼠标或键盘选择原文，最多 4096 个 Unicode 字符。</p>
          <Button htmlType="button" disabled={!canSubmit || pending || !selection || selection.sourceKey !== selectedSource}
            onClick={onChoose}>选用选中原文</Button>
        </>
      ) : null}
      <Button htmlType="button" disabled={!canSubmit || pending || !canUseFrozenSelection}
        onClick={onUseFrozenSelection}>填入工作区正文当前选区</Button>
      {validEvidence && chosenEvidence ? <blockquote>已选证据：{chosenEvidence.evidence.quote}</blockquote> : <p>请选择一段与本次审查使用的原文一致的内容。</p>}
    </>
  );
}

function FeedbackForm({
  decisions,
  sources,
  source,
  displayText,
  decisionId,
  selectedSource,
  selection,
  chosenEvidence,
  validEvidence,
  reason,
  canSubmit,
  ready,
  pending,
  recovering,
  submitError,
  onDecision,
  onSource,
  onSelect,
  onChoose,
  canUseFrozenSelection,
  onUseFrozenSelection,
  onReason,
  onSubmit,
}: {
  decisions: HumanDecision[];
  sources: ReviewDocument[];
  source: ReviewDocument | undefined;
  displayText: string;
  decisionId: string;
  selectedSource: string;
  selection: Selection | null;
  chosenEvidence: Selection | null;
  validEvidence: boolean;
  reason: string;
  canSubmit: boolean;
  ready: boolean;
  pending: boolean;
  recovering: boolean;
  submitError: unknown;
  onDecision: (value: string) => void;
  onSource: (value: string) => void;
  onSelect: (event: SyntheticEvent<HTMLTextAreaElement>) => void;
  onChoose: () => void;
  canUseFrozenSelection: boolean;
  onUseFrozenSelection: () => void;
  onReason: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  const disabled = !canSubmit || pending || recovering;
  return (
    <form onSubmit={onSubmit}>
      <DecisionSelect decisions={decisions} value={decisionId} onChange={onDecision} disabled={disabled} />
      <SourceSelect sources={sources} value={selectedSource} onChange={onSource} disabled={disabled} />
      <EvidencePicker source={source} displayText={displayText} selection={selection}
        validEvidence={validEvidence} chosenEvidence={chosenEvidence} canSubmit={canSubmit}
        pending={pending} selectedSource={selectedSource} onSelect={onSelect} onChoose={onChoose}
        canUseFrozenSelection={canUseFrozenSelection} onUseFrozenSelection={onUseFrozenSelection} />
      <label className="field">
        经验建议理由
        <Input.TextArea aria-label="经验建议理由" value={reason} rows={3} disabled={disabled}
          onChange={(event) => onReason(event.target.value)} />
      </label>
      <p className="meta">填写 2 至 1000 个字符。提交会保留原审查判断，不会自动批准候选。</p>
      <ErrorNotice error={submitError} />
      <Button type="primary" htmlType="submit" aria-label="提交经验建议并请求提炼" disabled={!ready} loading={pending}>提交经验建议并请求提炼</Button>
    </form>
  );
}

function DistillationStatusLine({
  status,
  statusError,
}: {
  status: ExperienceDistillationStatus | null;
  statusError: unknown;
}) {
  if (status) {
    return (
      <p>任务状态：{Object.hasOwn(STATUS_LABELS, status.state) ? STATUS_LABELS[status.state] : `未识别（${status.state}）`}{status.failure_code ? `，失败代码：${status.failure_code}` : ""}</p>
    );
  }
  if (statusError) return null;
  return <p role="status">正在读取任务状态…</p>;
}

function RetryDistillationButton({
  status,
  pending,
  distillPending,
  canSubmit,
  onRetry,
}: {
  status: ExperienceDistillationStatus | null;
  pending: boolean;
  distillPending: boolean;
  canSubmit: boolean;
  onRetry: () => void;
}) {
  const retryable = status?.state === "failed" || status?.state === "cancelled";
  if (!retryable || !canSubmit) return null;
  return (
    <Button htmlType="button" loading={distillPending} disabled={distillPending || pending} onClick={onRetry}>重新请求提炼</Button>
  );
}

function RequestDistillationButton({
  distillPending,
  pending,
  canSubmit,
  onRequest,
}: {
  distillPending: boolean;
  pending: boolean;
  canSubmit: boolean;
  onRequest: () => void;
}) {
  if (distillPending || !canSubmit) return null;
  return (
    <Button htmlType="button" disabled={pending} onClick={onRequest}>请求提炼</Button>
  );
}

function AnotherSuggestionButton({
  pending,
  distillPending,
  canSubmit,
  onAnother,
}: {
  pending: boolean;
  distillPending: boolean;
  canSubmit: boolean;
  onAnother: () => void;
}) {
  return (
    <p><Button htmlType="button" disabled={pending || distillPending || !canSubmit} onClick={onAnother}>提出另一条建议</Button></p>
  );
}

function DistillationActions({
  status,
  jobId,
  distillPending,
  pending,
  canSubmit,
  onRefresh,
  onRetry,
  onRequest,
  onAnother,
}: {
  status: ExperienceDistillationStatus | null;
  jobId: string | null;
  distillPending: boolean;
  pending: boolean;
  canSubmit: boolean;
  onRefresh: () => void;
  onRetry: () => void;
  onRequest: () => void;
  onAnother: () => void;
}) {
  return (
    <>
      {jobId ? (
        <>
          <p>提炼任务已受理。</p>
          <Button htmlType="button" onClick={onRefresh}>刷新任务状态</Button>{" "}
          <RetryDistillationButton status={status} pending={pending}
            distillPending={distillPending} canSubmit={canSubmit} onRetry={onRetry} />
        </>
      ) : (
        <RequestDistillationButton distillPending={distillPending} pending={pending}
          canSubmit={canSubmit} onRequest={onRequest} />
      )}
      <AnotherSuggestionButton pending={pending} distillPending={distillPending}
        canSubmit={canSubmit} onAnother={onAnother} />
    </>
  );
}

function CandidateSection({
  candidate,
  status,
  candidateError,
}: {
  candidate: ExperienceCandidate | null;
  status: ExperienceDistillationStatus | null;
  candidateError: unknown;
}) {
  return (
    <>
      <ErrorNotice error={candidateError} />
      {candidate ? <CandidateView candidate={candidate} completed={status?.state === "completed"} /> : status?.candidate_id && status.version_id ? <p>候选已生成；内容尚未读取。</p> : null}
    </>
  );
}

function FeedbackProgress({
  feedback,
  status,
  statusError,
  candidate,
  candidateError,
  jobId,
  distillPending,
  distillError,
  pending,
  canSubmit,
  onRefresh,
  onRetry,
  onRequest,
  onAnother,
}: {
  feedback: ExperienceFeedback;
  status: ExperienceDistillationStatus | null;
  statusError: unknown;
  candidate: ExperienceCandidate | null;
  candidateError: unknown;
  jobId: string | null;
  distillPending: boolean;
  distillError: unknown;
  pending: boolean;
  canSubmit: boolean;
  onRefresh: () => void;
  onRetry: () => void;
  onRequest: () => void;
  onAnother: () => void;
}) {
  return (
    <section aria-label="经验反馈进度" aria-live="polite">
      <h3>反馈已记录</h3>
      <p>审查判断：{decisionLabel(feedback.decision_action)}。</p>
      <p>建议理由：{feedback.reason}</p>
      {feedback.evidence_refs.map((ref, index) => <blockquote key={`${ref.document_id}:${ref.start}:${index}`}>{ref.quote}</blockquote>)}
      {distillPending ? <p role="status">正在请求经验提炼…</p> : null}
      <ErrorNotice error={distillError} />
      {jobId ? (
        <>
          <DistillationStatusLine status={status} statusError={statusError} />
          <ErrorNotice error={statusError} />
          <CandidateSection candidate={candidate} status={status} candidateError={candidateError} />
        </>
      ) : null}
      <DistillationActions
        status={status} jobId={jobId} distillPending={distillPending} pending={pending}
        canSubmit={canSubmit} onRefresh={onRefresh} onRetry={onRetry}
        onRequest={onRequest} onAnother={onAnother}
      />
    </section>
  );
}

function RecoveryDetails({
  feedback,
  jobId,
  manualFeedbackId,
  manualJobId,
  recovering,
  pending,
  distillPending,
  onFeedbackId,
  onJobId,
  onRecover,
}: {
  feedback: ExperienceFeedback | null;
  jobId: string | null;
  manualFeedbackId: string;
  manualJobId: string;
  recovering: boolean;
  pending: boolean;
  distillPending: boolean;
  onFeedbackId: (value: string) => void;
  onJobId: (value: string) => void;
  onRecover: () => void;
}) {
  return (
    <details>
      <summary>用反馈编号和任务编号恢复状态</summary>
      <p>更换浏览器或清除会话后，可以输入已记录的编号恢复状态。查看时仍需要有该案件的访问权限。</p>
      {feedback ? <p>反馈编号：{feedback.feedback_id}{jobId ? <>；任务编号：{jobId}</> : null}</p> : null}
      <label className="field">反馈编号<Input aria-label="反馈编号" value={manualFeedbackId} onChange={(event) => onFeedbackId(event.target.value)} /></label>
      <label className="field">任务编号（可选）<Input aria-label="任务编号" value={manualJobId} onChange={(event) => onJobId(event.target.value)} /></label>
      <Button htmlType="button" disabled={!manualFeedbackId.trim() || recovering || pending || distillPending} loading={recovering} onClick={onRecover}>读取反馈和任务</Button>
    </details>
  );
}

function decisionsOf(finding: Finding | null): HumanDecision[] {
  return finding?.decisions.filter((decision) => decision.id.trim() !== "" &&
    DECISION_ACTIONS.some((action) => action.action === decision.action)) ?? [];
}

function validEvidenceOf(
  chosenEvidence: Selection | null,
  sources: ReviewDocument[],
  caseId: string,
  runId: string,
  attemptId: string | null,
): boolean {
  return chosenEvidence !== null && chosenEvidence.caseId === caseId &&
    chosenEvidence.runId === runId && chosenEvidence.attemptId === attemptId &&
    sources.some((document) => sourceKey(document) === chosenEvidence.sourceKey &&
      document.digest === chosenEvidence.digest && document.text !== null &&
      document.text === chosenEvidence.text &&
      sliceByCodePoints(document.text, chosenEvidence.evidence.start, chosenEvidence.evidence.end) === chosenEvidence.evidence.quote);
}

function decisionChosen(
  decisions: HumanDecision[],
  decisionId: string,
): boolean {
  return decisions.some((decision) => decision.id === decisionId);
}

function submitSubjectPresent(params: {
  caseId: string;
  runId: string;
  attemptId: string | null;
  finding: Finding | null;
}): boolean {
  return Boolean(
    params.caseId && params.runId && params.attemptId && params.finding?.id,
  );
}

function reasonLengthValid(length: number): boolean {
  return length >= 2 && length <= 1000;
}

function idleState(params: {
  pending: boolean;
  distillPending: boolean;
  recovering: boolean;
  feedback: ExperienceFeedback | null;
}): boolean {
  return (
    !params.pending &&
    !params.distillPending &&
    !params.recovering &&
    params.feedback === null
  );
}

function feedbackReady(params: {
  canSubmit: boolean;
  caseId: string;
  runId: string;
  attemptId: string | null;
  finding: Finding | null;
  decisions: HumanDecision[];
  decisionId: string;
  validEvidence: boolean;
  reasonLength: number;
  pending: boolean;
  distillPending: boolean;
  recovering: boolean;
  feedback: ExperienceFeedback | null;
}): boolean {
  return (
    params.canSubmit &&
    submitSubjectPresent(params) &&
    decisionChosen(params.decisions, params.decisionId) &&
    params.validEvidence &&
    reasonLengthValid(params.reasonLength) &&
    idleState(params)
  );
}

function submissionBlocked(params: {
  ready: boolean;
  attemptId: string | null;
  finding: Finding | null;
  chosenEvidence: Selection | null;
  submitting: boolean;
}): boolean {
  return (
    !params.ready ||
    !params.attemptId ||
    !params.finding ||
    !params.chosenEvidence ||
    params.submitting
  );
}

function distillationKey(
  newOperation: boolean,
  current: string | null,
): string {
  return newOperation || !current ? crypto.randomUUID() : current;
}

function restoredJobId(manualJobId: string): string | null {
  return manualJobId.trim() || null;
}

function useTrackedRecovery(
  props: ExperienceFeedbackPanelProps,
  saved: SavedTracking | null,
  key: string,
  setFeedback: (value: ExperienceFeedback) => void,
  setRecoveryError: (value: unknown) => void,
  setRecovering: (value: boolean) => void,
) {
  useEffect(() => {
    if (!saved) return;
    const controller = new AbortController();
    void getExperienceFeedback(props.caseId, saved.feedbackId, controller.signal).then((item) => {
      if (!feedbackMatchesSource(item, props)) {
        throw new ApiError("该反馈不属于当前审查执行记录和线索", 200, "invalid_response", false, null);
      }
      if (!controller.signal.aborted) setFeedback(item);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setRecoveryError(error);
    }).finally(() => {
      if (!controller.signal.aborted) setRecovering(false);
    });
    return () => controller.abort();
    // The component remounts whenever the source key changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.caseId, saved, key]);
}

async function readCandidateState(
  caseId: string,
  feedbackId: string,
  jobId: string,
  status: ExperienceDistillationStatus,
  signal: AbortSignal,
): Promise<{ candidate: ExperienceCandidate | null; error: unknown }> {
  if (!status.candidate_id || !status.version_id) {
    return { candidate: null, error: null };
  }
  try {
    const candidate = await getExperienceCandidate(caseId, feedbackId, jobId, status.candidate_id, status.version_id, signal);
    return { candidate, error: null };
  } catch (error) {
    return { candidate: null, error };
  }
}

async function pollDistillation(
  caseId: string,
  feedbackId: string,
  jobId: string,
  signal: AbortSignal,
  setters: {
    setStatus: (value: ExperienceDistillationStatus | null) => void;
    setStatusError: (value: unknown) => void;
    setCandidate: (value: ExperienceCandidate | null) => void;
    setCandidateError: (value: unknown) => void;
  },
): Promise<boolean> {
  try {
    const status = await getExperienceDistillation(caseId, feedbackId, jobId, signal);
    if (signal.aborted) return false;
    setters.setStatus(status);
    setters.setStatusError(null);
    const result = await readCandidateState(caseId, feedbackId, jobId, status, signal);
    if (!signal.aborted) {
      setters.setCandidate(result.candidate);
      setters.setCandidateError(result.error);
    }
    return (
      !signal.aborted &&
      (status.state === "queued" || status.state === "running")
    );
  } catch (error) {
    if (!signal.aborted) {
      setters.setStatus(null);
      setters.setCandidate(null);
      setters.setCandidateError(null);
      setters.setStatusError(error);
    }
    return false;
  }
}

function useDistillationPolling(
  caseId: string,
  feedback: ExperienceFeedback | null,
  jobId: string | null,
  refresh: number,
  setters: {
    setStatus: (value: ExperienceDistillationStatus | null) => void;
    setStatusError: (value: unknown) => void;
    setCandidate: (value: ExperienceCandidate | null) => void;
    setCandidateError: (value: unknown) => void;
  },
) {
  useEffect(() => {
    if (!feedback || !jobId) return;
    const controller = new AbortController();
    let timer: number | undefined;
    async function readStatus() {
      const shouldContinue = await pollDistillation(
        caseId,
        feedback!.feedback_id,
        jobId!,
        controller.signal,
        setters,
      );
      if (shouldContinue) {
        timer = window.setTimeout(() => void readStatus(), 4000);
      }
    }
    void readStatus();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [caseId, feedback, jobId, refresh]);
}

interface FeedbackActionContext {
  props: ExperienceFeedbackPanelProps;
  caseId: string;
  runId: string;
  attemptId: string | null;
  finding: Finding | null;
  canSubmit: boolean;
  key: string;
  ready: boolean;
  decisionId: string;
  reason: string;
  chosenEvidence: Selection | null;
  distillKey: { current: string | null };
  feedbackOperation: { current: { body: string; key: string } | null };
  submitting: { current: boolean };
  requestingDistillation: { current: boolean };
  manualFeedbackId: string;
  manualJobId: string;
  recovering: boolean;
  setFeedback: (value: ExperienceFeedback) => void;
  setJobId: (value: string | null) => void;
  setStatus: (value: ExperienceDistillationStatus | null) => void;
  setStatusError: (value: unknown) => void;
  setCandidate: (value: ExperienceCandidate | null) => void;
  setCandidateError: (value: unknown) => void;
  setDistillError: (value: unknown) => void;
  setDistillPending: (value: boolean) => void;
  setPending: (value: boolean) => void;
  setSubmitError: (value: unknown) => void;
  setRecoveryError: (value: unknown) => void;
  setRecovering: (value: boolean) => void;
  setReason: (value: string) => void;
  setDecisionId: (value: string) => void;
  setSelectedSource: (value: string) => void;
  setSelection: (value: Selection | null) => void;
  setChosenEvidence: (value: Selection | null) => void;
}

async function runStartDistillation(
  ctx: FeedbackActionContext,
  item: ExperienceFeedback,
  newOperation: boolean,
) {
  if (distillationBlocked(ctx.canSubmit, ctx.requestingDistillation.current)) return;
  ctx.requestingDistillation.current = true;
  const requestKey = distillationKey(newOperation, ctx.distillKey.current);
  ctx.distillKey.current = requestKey;
  ctx.setJobId(null);
  ctx.setStatus(null);
  ctx.setStatusError(null);
  ctx.setCandidate(null);
  ctx.setCandidateError(null);
  ctx.setDistillError(null);
  ctx.setDistillPending(true);
  saveTracking(ctx.key, { feedbackId: item.feedback_id, jobId: null, distillKey: requestKey });
  try {
    const receipt = await requestExperienceDistillation(ctx.caseId, item.feedback_id, requestKey);
    saveTracking(ctx.key, { feedbackId: item.feedback_id, jobId: receipt.job_id, distillKey: requestKey });
    ctx.setJobId(receipt.job_id);
  } catch (error) {
    ctx.setDistillError(error);
  } finally {
    ctx.requestingDistillation.current = false;
    ctx.setDistillPending(false);
  }
}

async function runSubmit(ctx: FeedbackActionContext, event: FormEvent<HTMLFormElement>) {
  event.preventDefault();
  if (submissionBlocked({
    ready: ctx.ready,
    attemptId: ctx.attemptId,
    finding: ctx.finding,
    chosenEvidence: ctx.chosenEvidence,
    submitting: ctx.submitting.current,
  })) return;
  ctx.submitting.current = true;
  const input: ExperienceFeedbackInput = {
    run_id: ctx.runId,
    attempt_id: ctx.attemptId!,
    finding_id: ctx.finding!.id,
    decision_id: ctx.decisionId,
    reason: ctx.reason.trim(),
    evidence_refs: [ctx.chosenEvidence!.evidence],
  };
  const body = JSON.stringify(input);
  if (ctx.feedbackOperation.current?.body !== body) ctx.feedbackOperation.current = { body, key: crypto.randomUUID() };
  ctx.setPending(true);
  ctx.setSubmitError(null);
  ctx.setRecoveryError(null);
  try {
    const item = await submitExperienceFeedback(ctx.caseId, input, ctx.feedbackOperation.current.key);
    ctx.setFeedback(item);
    ctx.distillKey.current = null;
    saveTracking(ctx.key, { feedbackId: item.feedback_id, jobId: null, distillKey: null });
    await runStartDistillation(ctx, item, false);
  } catch (error) {
    ctx.setSubmitError(error);
  } finally {
    ctx.submitting.current = false;
    ctx.setPending(false);
  }
}

async function runRecover(ctx: FeedbackActionContext) {
  if (recoveryBlocked(ctx.manualFeedbackId, ctx.recovering)) return;
  ctx.setRecovering(true);
  ctx.setRecoveryError(null);
  try {
    const item = await getExperienceFeedback(ctx.caseId, ctx.manualFeedbackId.trim());
    if (!feedbackMatchesSource(item, ctx.props)) {
      throw new ApiError("该反馈不属于当前审查执行记录和线索", 200, "invalid_response", false, null);
    }
    const restoredJob = restoredJobId(ctx.manualJobId);
    ctx.distillKey.current = null;
    ctx.setFeedback(item);
    ctx.setJobId(restoredJob);
    ctx.setStatus(null);
    ctx.setCandidate(null);
    ctx.setStatusError(null);
    ctx.setCandidateError(null);
    ctx.setDistillError(null);
    saveTracking(ctx.key, { feedbackId: item.feedback_id, jobId: restoredJob, distillKey: null });
  } catch (error) {
    ctx.setRecoveryError(error);
  } finally {
    ctx.setRecovering(false);
  }
}

function useFeedbackPanelState(props: ExperienceFeedbackPanelProps) {
  const { caseId, runId, attemptId, finding, documents, canSubmit } = props;
  const key = storageKey(props);
  const [saved] = useState(() => loadTracking(key));
  const [feedback, setFeedback] = useState<ExperienceFeedback | null>(null);
  const [jobId, setJobId] = useState<string | null>(savedJobId(saved));
  const distillKey = useRef<string | null>(savedDistillKey(saved));
  const feedbackOperation = useRef<{ body: string; key: string } | null>(null);
  const submitting = useRef(false);
  const requestingDistillation = useRef(false);
  const [decisionId, setDecisionId] = useState("");
  const [selectedSource, setSelectedSource] = useState("");
  const [selection, setSelection] = useState<Selection | null>(null);
  const [chosenEvidence, setChosenEvidence] = useState<Selection | null>(null);
  const [reason, setReason] = useState("");
  const [pending, setPending] = useState(false);
  const [distillPending, setDistillPending] = useState(false);
  const [submitError, setSubmitError] = useState<unknown>(null);
  const [distillError, setDistillError] = useState<unknown>(null);
  const [recoveryError, setRecoveryError] = useState<unknown>(null);
  const [recovering, setRecovering] = useState(Boolean(saved));
  const [manualFeedbackId, setManualFeedbackId] = useState("");
  const [manualJobId, setManualJobId] = useState("");
  const [status, setStatus] = useState<ExperienceDistillationStatus | null>(null);
  const [statusError, setStatusError] = useState<unknown>(null);
  const [candidate, setCandidate] = useState<ExperienceCandidate | null>(null);
  const [candidateError, setCandidateError] = useState<unknown>(null);
  const [refresh, setRefresh] = useState(0);

  useTrackedRecovery(props, saved, key, setFeedback, setRecoveryError, setRecovering);
  useDistillationPolling(caseId, feedback, jobId, refresh, {
    setStatus,
    setStatusError,
    setCandidate,
    setCandidateError,
  });
  const decisions = decisionsOf(finding);
  const sources = useMemo(() => documents.filter(sourceUsable), [documents]);
  const quoteScope = props.quoteScope ?? null;
  const frozenSelection = useMemo(
    () => selectionFromPanelQuote(
      props.quoteSelection ?? null, quoteScope, sources, caseId, runId, attemptId,
    ),
    [attemptId, caseId, props.quoteSelection, quoteScope, runId, sources],
  );
  const canUseFrozenSelection = frozenSelection !== null;
  const source = sources.find((document) => sourceKey(document) === selectedSource);
  const displayText = useMemo(() => displayTextOf(source), [source?.text]);
  const validEvidence = useMemo(
    () => validEvidenceOf(chosenEvidence, sources, caseId, runId, attemptId),
    [attemptId, caseId, chosenEvidence, runId, sources],
  );
  const reasonLength = Array.from(reason.trim()).length;
  const ready = feedbackReady({
    canSubmit, caseId, runId, attemptId, finding, decisions, decisionId,
    validEvidence, reasonLength, pending, distillPending, recovering, feedback,
  });

  const actionContext: FeedbackActionContext = {
    props,
    caseId,
    runId,
    attemptId,
    finding,
    canSubmit,
    key,
    ready,
    decisionId,
    reason,
    chosenEvidence,
    distillKey,
    feedbackOperation,
    submitting,
    requestingDistillation,
    manualFeedbackId,
    manualJobId,
    recovering,
    setFeedback,
    setJobId,
    setStatus,
    setStatusError,
    setCandidate,
    setCandidateError,
    setDistillError,
    setDistillPending,
    setPending,
    setSubmitError,
    setRecoveryError,
    setRecovering,
    setReason,
    setDecisionId,
    setSelectedSource,
    setSelection,
    setChosenEvidence,
  };
  function useFrozenSelection() {
    if (!canSubmit || !frozenSelection) return;
    setSelectedSource(frozenSelection.sourceKey);
    setSelection(frozenSelection);
    setChosenEvidence(frozenSelection);
  }

  function startDistillation(item: ExperienceFeedback, newOperation: boolean) {
    return runStartDistillation(actionContext, item, newOperation);
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    return runSubmit(actionContext, event);
  }

  function recover() {
    return runRecover(actionContext);
  }

  function startAnother() {
    saveTracking(key, null);
    distillKey.current = null;
    feedbackOperation.current = null;
    setFeedback(null);
    setJobId(null);
    setStatus(null);
    setCandidate(null);
    setStatusError(null);
    setCandidateError(null);
    setDistillError(null);
    setSubmitError(null);
    setRecoveryError(null);
    setReason("");
    setDecisionId("");
    setSelectedSource("");
    setSelection(null);
    setChosenEvidence(null);
  }

  return {
    caseId,
    runId,
    attemptId,
    finding,
    canSubmit,
    quoteScope,
    canUseFrozenSelection,
    useFrozenSelection,
    feedback,
    jobId,
    decisions,
    sources,
    source,
    displayText,
    decisionId,
    selectedSource,
    selection,
    chosenEvidence,
    validEvidence,
    reason,
    ready,
    pending,
    distillPending,
    submitError,
    distillError,
    recoveryError,
    recovering,
    status,
    statusError,
    candidate,
    candidateError,
    manualFeedbackId,
    manualJobId,
    startDistillation,
    submit,
    recover,
    startAnother,
    setDecisionId,
    setReason,
    setSelection,
    setChosenEvidence,
    setManualFeedbackId,
    setManualJobId,
    setRefresh,
    onSourceChange: (value: string) => {
      setSelectedSource(value);
      setSelection(null);
      setChosenEvidence(null);
    },
  };
}

type FeedbackPanelState = ReturnType<typeof useFeedbackPanelState>;

function FeedbackPanelContent({ state }: { state: FeedbackPanelState }) {
  const { feedback, finding, attemptId } = state;
  return (
    <>
      <ErrorNotice error={state.recoveryError} />
      {!feedback && finding && attemptId ? (
        <FeedbackForm
          decisions={state.decisions}
          sources={state.sources}
          source={state.source}
          displayText={state.displayText}
          decisionId={state.decisionId}
          selectedSource={state.selectedSource}
          selection={state.selection}
          chosenEvidence={state.chosenEvidence}
          validEvidence={state.validEvidence}
          reason={state.reason}
          canSubmit={state.canSubmit}
          ready={state.ready}
          pending={state.pending}
          recovering={state.recovering}
          submitError={state.submitError}
          canUseFrozenSelection={state.canUseFrozenSelection}
          onUseFrozenSelection={state.useFrozenSelection}
          onDecision={state.setDecisionId}
          onSource={state.onSourceChange}
          onSelect={(event) => selectFromSource(state.source, event, state.setSelection, state.quoteScope)}
          onChoose={() => state.setChosenEvidence(state.selection)}
          onReason={state.setReason}
          onSubmit={(event) => void state.submit(event)}
        />
      ) : null}
      {feedback ? (
        <FeedbackProgress
          feedback={feedback}
          status={state.status}
          statusError={state.statusError}
          candidate={state.candidate}
          candidateError={state.candidateError}
          jobId={state.jobId}
          distillPending={state.distillPending}
          distillError={state.distillError}
          pending={state.pending}
          canSubmit={state.canSubmit}
          onRefresh={() => state.setRefresh((value) => value + 1)}
          onRetry={() => void state.startDistillation(feedback, true)}
          onRequest={() => void state.startDistillation(feedback, false)}
          onAnother={state.startAnother}
        />
      ) : null}
      {finding && attemptId ? (
        <RecoveryDetails
          feedback={feedback}
          jobId={state.jobId}
          manualFeedbackId={state.manualFeedbackId}
          manualJobId={state.manualJobId}
          recovering={state.recovering}
          pending={state.pending}
          distillPending={state.distillPending}
          onFeedbackId={state.setManualFeedbackId}
          onJobId={state.setManualJobId}
          onRecover={() => void state.recover()}
        />
      ) : null}
    </>
  );
}

function FeedbackForSource(props: ExperienceFeedbackPanelProps) {
  const state = useFeedbackPanelState(props);
  return (
    <Card title="复盘经验建议" className="workflow-panel">
      <p>选择已记录的审查判断和本次审查使用的原文，说明希望今后参考的经验。整理出的经验还需要审核。</p>
      <FeedbackNotices finding={state.finding} attemptId={state.attemptId}
        canSubmit={state.canSubmit} decisions={state.decisions}
        sources={state.sources} recovering={state.recovering} />
      <FeedbackPanelContent state={state} />
    </Card>
  );
}

export function ExperienceFeedbackPanel(props: ExperienceFeedbackPanelProps) {
  return <FeedbackForSource key={storageKey(props)} {...props} />;
}
