import { Button, Card, Input, Select } from "antd";
import { useEffect, useMemo, useRef, useState, type FormEvent, type SyntheticEvent } from "react";
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
import type { Finding, ReviewDocument } from "../api/types";
import { DECISION_ACTIONS, decisionLabel, formatDateTime, roleLabel } from "../domain/review";
import { sliceByCodePoints } from "../domain/text";

export interface ExperienceFeedbackPanelProps {
  caseId: string;
  runId: string;
  attemptId: string | null;
  finding: Finding | null;
  documents: ReviewDocument[];
  canSubmit: boolean;
}

type SavedTracking = { feedbackId: string; jobId: string | null; distillKey: string | null };
type Selection = { sourceKey: string; text: string; evidence: ExperienceEvidenceRef };

function storageKey(props: ExperienceFeedbackPanelProps): string {
  return `experience-feedback:${JSON.stringify([props.caseId, props.runId, props.attemptId, props.finding?.id ?? null])}`;
}

function loadTracking(key: string): SavedTracking | null {
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
    const item = value as Record<string, unknown>;
    if (typeof item.feedbackId !== "string" || !item.feedbackId.trim()) return null;
    return {
      feedbackId: item.feedbackId,
      jobId: typeof item.jobId === "string" && item.jobId.trim() ? item.jobId : null,
      distillKey: typeof item.distillKey === "string" && item.distillKey.trim() ? item.distillKey : null,
    };
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

function sourceUsable(document: ReviewDocument): boolean {
  const parsed = document.parsed_document;
  const id = parseId(document);
  return document.parse_status === "ready" && document.text !== null && document.text !== "" &&
    document.id.trim() !== "" && id !== null && id.trim() !== "" &&
    (parsed === null || (parsed.status === "ready" && parsed.document_id === document.id && parsed.id === id));
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

function selectionFromTextArea(document: ReviewDocument, element: HTMLTextAreaElement): Selection | null {
  const text = document.text;
  const parsedId = parseId(document);
  if (text === null || parsedId === null || element.selectionStart === element.selectionEnd ||
      element.value !== text.replace(/\r\n?/g, "\n")) return null;
  const startIndex = originalOffset(text, element.selectionStart);
  const endIndex = originalOffset(text, element.selectionEnd);
  if (startIndex === null || endIndex === null) return null;
  const quote = text.slice(startIndex, endIndex);
  const start = Array.from(text.slice(0, startIndex)).length;
  const end = Array.from(text.slice(0, endIndex)).length;
  if (quote === "" || end - start > 4096 || sliceByCodePoints(text, start, end) !== quote) return null;
  return {
    sourceKey: sourceKey(document),
    text,
    evidence: { document_id: document.id, parsed_document_id: parsedId, quote, start, end },
  };
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

function CandidateView({ candidate, completed }: { candidate: ExperienceCandidate; completed: boolean }) {
  const { content } = candidate;
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

export function ExperienceFeedbackPanel(props: ExperienceFeedbackPanelProps) {
  return <FeedbackForSource key={storageKey(props)} {...props} />;
}

function FeedbackForSource(props: ExperienceFeedbackPanelProps) {
  const { caseId, runId, attemptId, finding, documents, canSubmit } = props;
  const key = storageKey(props);
  const [saved] = useState(() => loadTracking(key));
  const [feedback, setFeedback] = useState<ExperienceFeedback | null>(null);
  const [jobId, setJobId] = useState<string | null>(saved?.jobId ?? null);
  const distillKey = useRef<string | null>(saved?.distillKey ?? null);
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

  useEffect(() => {
    if (!saved) return;
    const controller = new AbortController();
    void getExperienceFeedback(caseId, saved.feedbackId, controller.signal).then((item) => {
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
  }, [caseId, saved]);

  useEffect(() => {
    if (!feedback || !jobId) return;
    const controller = new AbortController();
    let timer: number | undefined;
    async function readStatus() {
      try {
        const item = await getExperienceDistillation(caseId, feedback!.feedback_id, jobId!, controller.signal);
        if (controller.signal.aborted) return;
        setStatus(item);
        setStatusError(null);
        if (item.candidate_id && item.version_id) {
          try {
            const result = await getExperienceCandidate(caseId, feedback!.feedback_id, jobId!, item.candidate_id, item.version_id, controller.signal);
            if (!controller.signal.aborted) {
              setCandidate(result);
              setCandidateError(null);
            }
          } catch (error) {
            if (!controller.signal.aborted) {
              setCandidate(null);
              setCandidateError(error);
            }
          }
        } else {
          setCandidate(null);
          setCandidateError(null);
        }
        if (!controller.signal.aborted && (item.state === "queued" || item.state === "running")) {
          timer = window.setTimeout(() => void readStatus(), 4000);
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setStatus(null);
          setCandidate(null);
          setCandidateError(null);
          setStatusError(error);
        }
      }
    }
    void readStatus();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [caseId, feedback, jobId, refresh]);

  const decisions = finding?.decisions.filter((decision) => decision.id.trim() !== "" &&
    DECISION_ACTIONS.some((action) => action.action === decision.action)) ?? [];
  const sources = useMemo(() => documents.filter(sourceUsable), [documents]);
  const source = sources.find((document) => sourceKey(document) === selectedSource);
  const displayText = useMemo(() => source?.text?.replace(/\r\n?/g, "\n") ?? "", [source?.text]);
  const validEvidence = useMemo(() => chosenEvidence !== null && sources.some((document) =>
    sourceKey(document) === chosenEvidence.sourceKey && document.text !== null && document.text === chosenEvidence.text &&
    sliceByCodePoints(document.text, chosenEvidence.evidence.start, chosenEvidence.evidence.end) === chosenEvidence.evidence.quote), [chosenEvidence, sources]);
  const reasonLength = Array.from(reason.trim()).length;
  const ready = canSubmit && Boolean(caseId && runId && attemptId && finding?.id) &&
    decisions.some((decision) => decision.id === decisionId) && validEvidence && reasonLength >= 2 && reasonLength <= 1000 &&
    !pending && !distillPending && !recovering && feedback === null;

  async function startDistillation(item: ExperienceFeedback, newOperation: boolean) {
    if (!canSubmit || requestingDistillation.current) return;
    requestingDistillation.current = true;
    const requestKey = newOperation || !distillKey.current ? crypto.randomUUID() : distillKey.current;
    distillKey.current = requestKey;
    setJobId(null);
    setStatus(null);
    setStatusError(null);
    setCandidate(null);
    setCandidateError(null);
    setDistillError(null);
    setDistillPending(true);
    saveTracking(key, { feedbackId: item.feedback_id, jobId: null, distillKey: requestKey });
    try {
      const receipt = await requestExperienceDistillation(caseId, item.feedback_id, requestKey);
      saveTracking(key, { feedbackId: item.feedback_id, jobId: receipt.job_id, distillKey: requestKey });
      setJobId(receipt.job_id);
    } catch (error) {
      setDistillError(error);
    } finally {
      requestingDistillation.current = false;
      setDistillPending(false);
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!ready || !attemptId || !finding || !chosenEvidence || submitting.current) return;
    submitting.current = true;
    const input: ExperienceFeedbackInput = {
      run_id: runId,
      attempt_id: attemptId,
      finding_id: finding.id,
      decision_id: decisionId,
      reason: reason.trim(),
      evidence_refs: [chosenEvidence.evidence],
    };
    const body = JSON.stringify(input);
    if (feedbackOperation.current?.body !== body) feedbackOperation.current = { body, key: crypto.randomUUID() };
    setPending(true);
    setSubmitError(null);
    setRecoveryError(null);
    try {
      const item = await submitExperienceFeedback(caseId, input, feedbackOperation.current.key);
      setFeedback(item);
      distillKey.current = null;
      saveTracking(key, { feedbackId: item.feedback_id, jobId: null, distillKey: null });
      await startDistillation(item, false);
    } catch (error) {
      setSubmitError(error);
    } finally {
      submitting.current = false;
      setPending(false);
    }
  }

  async function recover() {
    if (!manualFeedbackId.trim() || recovering) return;
    setRecovering(true);
    setRecoveryError(null);
    try {
      const item = await getExperienceFeedback(caseId, manualFeedbackId.trim());
      if (!feedbackMatchesSource(item, props)) {
        throw new ApiError("该反馈不属于当前审查执行记录和线索", 200, "invalid_response", false, null);
      }
      const restoredJobId = manualJobId.trim() || null;
      distillKey.current = null;
      setFeedback(item);
      setJobId(restoredJobId);
      setStatus(null);
      setCandidate(null);
      setStatusError(null);
      setCandidateError(null);
      setDistillError(null);
      saveTracking(key, { feedbackId: item.feedback_id, jobId: restoredJobId, distillKey: null });
    } catch (error) {
      setRecoveryError(error);
    } finally {
      setRecovering(false);
    }
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

  return (
    <Card title="复盘经验建议" className="workflow-panel">
      <p>选择已记录的审查判断和本次审查使用的原文，说明希望今后参考的经验。整理出的经验还需要审核。</p>
      {!finding ? <p>请选择一条审查线索。</p> : null}
      {!attemptId ? <p>当前审查缺少可用的执行记录，暂时不能提交反馈。</p> : null}
      {!canSubmit ? <p>当前角色或审查尝试不能提交经验反馈；权限由服务端核验。</p> : null}
      {finding && decisions.length === 0 ? <p>当前线索没有可用于反馈的审查判断。</p> : null}
      {finding && sources.length === 0 ? <p>本次执行没有可供选择的已解析原文。</p> : null}
      {recovering ? <p role="status">正在读取已保存的反馈…</p> : null}
      <ErrorNotice error={recoveryError} />
      {!feedback && finding && attemptId ? (
        <form onSubmit={(event) => void submit(event)}>
          <label className="field">
            审查判断
            <Select aria-label="审查判断" value={decisionId || undefined} placeholder="选择已记录的决定"
              disabled={!canSubmit || pending || recovering}
              onChange={(value: string) => setDecisionId(value)}
              options={decisions.map((decision, index) => ({ value: decision.id, label: `第 ${index + 1} 条 · ${decisionLabel(decision.action)} · ${decision.actor} · ${formatDateTime(decision.decided_at)}` }))} />
          </label>
          <label className="field">
            本次审查材料
            <Select aria-label="本次审查材料" value={selectedSource || undefined} placeholder="选择材料"
              disabled={!canSubmit || pending || recovering}
              onChange={(value: string) => { setSelectedSource(value); setSelection(null); setChosenEvidence(null); }}
              options={sources.map((document, index) => ({ value: sourceKey(document), label: `第 ${index + 1} 份 · ${document.filename} · ${roleLabel(document.role)}` }))} />
          </label>
          {source && source.text !== null ? (
            <>
              <label className="field">
                本次审查使用的原文（选择一段后点击选用）
                <Input.TextArea aria-label="本次审查使用的原文" readOnly value={displayText} rows={6}
                  onSelect={(event: SyntheticEvent<HTMLTextAreaElement>) => setSelection(selectionFromTextArea(source, event.currentTarget))} />
              </label>
              <p className="meta">可用鼠标或键盘选择原文，最多 4096 个 Unicode 字符。</p>
              <Button htmlType="button" disabled={!canSubmit || pending || !selection || selection.sourceKey !== selectedSource}
                onClick={() => setChosenEvidence(selection)}>选用选中原文</Button>
            </>
          ) : null}
          {validEvidence && chosenEvidence ? <blockquote>已选证据：{chosenEvidence.evidence.quote}</blockquote> : <p>请选择一段与本次审查使用的原文一致的内容。</p>}
          <label className="field">
            经验建议理由
            <Input.TextArea aria-label="经验建议理由" value={reason} rows={3} disabled={!canSubmit || pending || recovering}
              onChange={(event) => setReason(event.target.value)} />
          </label>
          <p className="meta">填写 2 至 1000 个字符。提交会保留原审查判断，不会自动批准候选。</p>
          <ErrorNotice error={submitError} />
          <Button type="primary" htmlType="submit" aria-label="提交经验建议并请求提炼" disabled={!ready} loading={pending}>提交经验建议并请求提炼</Button>
        </form>
      ) : null}
      {feedback ? (
        <section aria-label="经验反馈进度" aria-live="polite">
          <h3>反馈已记录</h3>
          <p>审查判断：{decisionLabel(feedback.decision_action)}。</p>
          <p>建议理由：{feedback.reason}</p>
          {feedback.evidence_refs.map((ref, index) => <blockquote key={`${ref.document_id}:${ref.start}:${index}`}>{ref.quote}</blockquote>)}
          {distillPending ? <p role="status">正在请求经验提炼…</p> : null}
          <ErrorNotice error={distillError} />
          {jobId ? (
            <>
              <p>提炼任务已受理。</p>
              {status ? <p>任务状态：{Object.hasOwn(STATUS_LABELS, status.state) ? STATUS_LABELS[status.state] : `未识别（${status.state}）`}{status.failure_code ? `，失败代码：${status.failure_code}` : ""}</p> : statusError ? null : <p role="status">正在读取任务状态…</p>}
              <ErrorNotice error={statusError} />
              <ErrorNotice error={candidateError} />
              {candidate ? <CandidateView candidate={candidate} completed={status?.state === "completed"} /> : status?.candidate_id && status.version_id ? <p>候选已生成；内容尚未读取。</p> : null}
              <Button htmlType="button" onClick={() => setRefresh((value) => value + 1)}>刷新任务状态</Button>{" "}
              {(status?.state === "failed" || status?.state === "cancelled") && canSubmit ? <Button htmlType="button" loading={distillPending} disabled={distillPending || pending} onClick={() => void startDistillation(feedback, true)}>重新请求提炼</Button> : null}
            </>
          ) : !distillPending && canSubmit ? (
            <Button htmlType="button" disabled={pending} onClick={() => void startDistillation(feedback, false)}>请求提炼</Button>
          ) : null}
          <p><Button htmlType="button" disabled={pending || distillPending || !canSubmit} onClick={startAnother}>提出另一条建议</Button></p>
        </section>
      ) : null}
      {finding && attemptId ? (
        <details>
          <summary>用反馈编号和任务编号恢复状态</summary>
          <p>更换浏览器或清除会话后，可以输入已记录的编号恢复状态。查看时仍需要有该案件的访问权限。</p>
          {feedback ? <p>反馈编号：{feedback.feedback_id}{jobId ? <>；任务编号：{jobId}</> : null}</p> : null}
          <label className="field">反馈编号<Input aria-label="反馈编号" value={manualFeedbackId} onChange={(event) => setManualFeedbackId(event.target.value)} /></label>
          <label className="field">任务编号（可选）<Input aria-label="任务编号" value={manualJobId} onChange={(event) => setManualJobId(event.target.value)} /></label>
          <Button htmlType="button" disabled={!manualFeedbackId.trim() || recovering || pending || distillPending} loading={recovering} onClick={() => void recover()}>读取反馈和任务</Button>
        </details>
      ) : null}
    </Card>
  );
}
