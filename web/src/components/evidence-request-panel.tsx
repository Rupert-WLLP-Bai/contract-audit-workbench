import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Input, Select } from "antd";
import { useRef, useState, type FormEvent, type SyntheticEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import type { BusinessAgentCapability, BusinessAgentGap, BusinessAgentReview } from "../api/business-agent-review";
import { getCase, getReview, getSession, getWorkbench } from "../api/endpoints";
import {
  createEvidenceReceipt,
  createEvidenceRequest,
  listEvidenceHistory,
  listEvidenceRequests,
  uploadEvidenceDocument,
  verifyEvidenceRequest,
  type EvidenceRequestEvent,
  type CreateEvidenceRequestInput,
  type EvidenceQuote,
  type EvidenceReceipt,
  type EvidenceRequest,
} from "../api/evidence-workflow";
import { ErrorNotice } from "./error-notice";
import { queryKeys, useCreateWorkbenchTask, useStartReview } from "../api/queries";
import type { Actor, CaseDetail, ConstraintEvaluation, ReviewDocument, ReviewWorkspace, WorkbenchTask } from "../api/types";
import { caseHref, formatDateTime, relationLabel, roleLabel } from "../domain/review";
import { StatusText } from "./status-text";

const materialRoles = [
  { value: "procurement", label: "采购文件" },
  { value: "award", label: "中标文件" },
  { value: "draft_contract", label: "拟签合同" },
  { value: "attachment", label: "技术附件" },
];

const domainLabels: Record<string, string> = {
  compliance: "合规与授权", commercial: "商务与财务", technical: "技术与服务",
  fulfillment: "履约与变更", counterparty: "主体与关联",
};
const capabilityLabels: Record<string, string> = {
  authorization_basis: "授权依据", commercial_baseline: "商务条款", warranty_months: "质保期限",
  acceptance_schedule: "交付与验收时间", counterparty_record: "主体登记信息", amount: "金额",
  party: "合同主体", approval_authority: "审批或授权", approval_validity: "审批有效期",
  effective_scope: "服务范围", scope: "技术与服务范围", milestone: "履约里程碑",
  explicit_start_event: "启动时间", subject_id: "主体标识", signing_party: "签署方",
  payee: "收款方", responsibility: "责任与义务", "dedicated_line.minimum_bandwidth": "最低保障带宽",
  "dedicated_line.monthly_availability": "月可用率", "payment.advance_ratio.ceiling": "预付款比例上限",
};
const conditionLabels: Record<string, string> = { compatible: "检查结果一致", conflict: "检查结果存在冲突", resolved: "缺口已解决" };
const statusLabels: Record<string, string> = { open: "待提交", received: "已提交，待复核", verified: "已核验" };
const eventLabels: Record<string, string> = { created: "创建请求", received: "提交回执", verified: "完成核验" };
const requestKey = (caseId: string) => ["evidence-requests", caseId] as const;

function useOperationKeys() {
  const keys = useRef(new Map<string, string>());
  return (operation: string, input: unknown) => {
    const identity = JSON.stringify([operation, input]);
    const existing = keys.current.get(identity);
    if (existing) return existing;
    const key = crypto.randomUUID();
    keys.current.set(identity, key);
    return key;
  };
}

function sameObjects(before: string[], after: string[]): boolean {
  if (before.length === 0) return true;
  if (before.length !== after.length) return false;
  const beforeSet = new Set(before);
  const afterSet = new Set(after);
  return before.every(Boolean) && after.every(Boolean) && beforeSet.size === before.length &&
    afterSet.size === after.length && before.every((value) => afterSet.has(value));
}

function displayGap(domain: string, capability: string): string {
  return `${domainLabels[domain] ?? "其他领域"}：${capabilityLabels[capability] ?? "其他审查项目"}`;
}

function roleHasPermission(role: string | undefined, allowed: readonly string[]): boolean {
  return allowed.includes(role ?? "");
}

function requestPermissions(context: PanelContext) {
  const role = context.actor?.role;
  const available = context.detailReady && context.detail?.archived_at === null;
  return {
    canSubmit: available && roleHasPermission(role, ["admin", "reviewer", "clerk"]),
    canUpload: available && roleHasPermission(role, ["admin", "clerk"]),
    canVerify: available && roleHasPermission(role, ["admin", "reviewer"]),
  };
}

function matchingSourceEvaluations(
  source: EvidenceRequest["source_evaluation"],
  evaluations: ConstraintEvaluation[],
): ConstraintEvaluation[] {
  if (!source) return [];
  return evaluations.filter((entry) =>
    entry.domain === source.domain && entry.rule_version === source.rule_version &&
    entry.rule_id === source.rule_id && entry.capability_id === source.capability_id &&
    sameObjects(source.object_refs, entry.object_refs),
  );
}

function matchingSourceGap(
  source: EvidenceRequest["source_gap"],
  agentReview: BusinessAgentReview | null,
  attemptId: string | null,
): BusinessAgentGap | undefined {
  if (!source || !agentReview || agentReview.attempt_id !== attemptId) return undefined;
  const matches = (agentReview.gaps ?? []).filter((gap) => gap.gap_id === source.gap_id);
  const candidate = matches.length === 1 ? matches[0] : undefined;
  if (!candidate || candidate.domain !== source.domain || candidate.capability_id !== source.capability_id)
    return undefined;
  return candidate;
}

function gapIsResolved(
  gap: BusinessAgentGap | undefined,
  agentReview: BusinessAgentReview | null,
): boolean {
  const capability = matchingGapCapability(gap, agentReview);
  if (!gap || !capability) return false;
  return gap.status === "resolved" &&
    !(agentReview?.unresolved_gap_ids ?? []).includes(gap.gap_id) &&
    capability.status === "completed" &&
    (capability.relation === "compatible" || capability.relation === "conflict");
}

function matchingGapCapability(
  gap: BusinessAgentGap | undefined,
  agentReview: BusinessAgentReview | null,
): BusinessAgentCapability | undefined {
  if (!gap) return undefined;
  const matches = (agentReview?.domains ?? [])
    .filter((domain) => domain.domain === gap.domain)
    .flatMap((domain) => domain.capabilities ?? [])
    .filter((entry) => entry.capability_id === gap.capability_id);
  return matches.length === 1 ? matches[0] : undefined;
}

function hasLaterFrozenAttempt(
  attempt: NonNullable<ReviewWorkspace["review_attempts"]>["attempt"] | undefined,
  attemptId: string | null,
  runId: string,
  sourceAttemptId: string | null,
): boolean {
  return !!attemptId && attempt?.id === attemptId && attempt.run_id === runId &&
    attempt.id !== sourceAttemptId && attempt.history_kind === "frozen" &&
    !!attempt.finished_at && (attempt.state === "completed" || attempt.state === "partial");
}

function receiptFrozenDocuments(
  documents: ReviewDocument[] | undefined,
  item: EvidenceRequest,
  receipt: EvidenceReceipt | undefined,
) {
  return (documents ?? []).filter((document) =>
    referenceableFrozenDocument(document) &&
    matchesRequestMaterialRole(document, item) &&
    matchesReceiptDocument(document, receipt) &&
    matchesReceiptAnswer(document, receipt));
}

function referenceableFrozenDocument(document: ReviewDocument): boolean {
  return !!document.parsed_document_id && !!document.text;
}

function matchesRequestMaterialRole(document: ReviewDocument, item: EvidenceRequest): boolean {
  return !item.material_role || document.role === item.material_role;
}

function matchesReceiptDocument(document: ReviewDocument, receipt: EvidenceReceipt | undefined): boolean {
  return !receipt?.document_id || document.id === receipt.document_id;
}

function matchesReceiptAnswer(document: ReviewDocument, receipt: EvidenceReceipt | undefined): boolean {
  return !receipt?.answer || !!document.text?.includes(receipt.answer);
}

function quoteMatchesFrozenDocument(
  quote: EvidenceQuote | null,
  document: ReviewDocument | undefined,
  receipt: EvidenceReceipt | undefined,
): boolean {
  return !!quote && !!document && quote.document_id === document.id &&
    quote.parsed_document_id === document.parsed_document_id &&
    Array.from(quote.quote).length <= 4096 &&
    (!receipt?.answer || quote.quote.includes(receipt.answer));
}

function requiresBusinessReviewTime(detail: CaseDetail | undefined): boolean {
  return detail?.business_line === "ENT" &&
    (detail.scenario === "telecom_dedicated_line" ||
      (detail.scenario === "cloud_services" && detail.contract_type === "service"));
}

function requestCardReviewFacts(
  item: EvidenceRequest,
  attemptId: string | null,
  runId: string,
  evaluations: ConstraintEvaluation[],
  agentReview: BusinessAgentReview | null,
  context: PanelContext,
) {
  const source = item.source_evaluation;
  const matchingEvaluations = matchingSourceEvaluations(source, evaluations);
  const gap = matchingSourceGap(item.source_gap, agentReview, attemptId);
  const observed = source
    ? matchingEvaluations.length === 1 && matchingEvaluations[0]?.relation === item.verification_condition
    : gapIsResolved(gap, agentReview);
  const attempt = context.workspace?.review_attempts?.attempt;
  return {
    source,
    matchingEvaluations,
    gap,
    observed,
    frozenLater: hasLaterFrozenAttempt(attempt, attemptId, runId, item.source_attempt_id),
    requiresBusinessAt: requiresBusinessReviewTime(context.detail),
  };
}

function requestVerificationReady(
  canVerify: boolean,
  itemStatus: string,
  receipt: EvidenceReceipt | undefined,
  frozenLater: boolean,
  observed: boolean,
  quoteValid: boolean,
  reason: string,
): boolean {
  return canVerify && itemStatus === "received" && !!receipt && frozenLater &&
    observed && quoteValid && !!reason.trim();
}

function requestMatchDescription(
  source: EvidenceRequest["source_evaluation"],
  matchingEvaluations: ConstraintEvaluation[],
  gap: BusinessAgentGap | undefined,
): string {
  if (source) return conditionLabels[matchingEvaluations[0]?.relation ?? ""] ?? "";
  return gap ? `${displayGap(gap.domain, gap.capability_id)}，缺口已解决` : "";
}

function availableRequestDocuments(
  documents: CaseDetail["documents"],
  materialRole: string | null,
): CaseDetail["documents"] {
  return documents.filter((document) =>
    !document.superseded && (!materialRole || document.role === materialRole),
  );
}

function requestDocumentOptions(
  documents: CaseDetail["documents"],
  uploaded: { id: string; filename: string; role: string } | null,
) {
  const options = documents.map((document) => ({
    value: document.id,
    label: `${document.filename}（${roleLabel(document.role)}）`,
  }));
  if (uploaded && !options.some((option) => option.value === uploaded.id))
    options.push({ value: uploaded.id, label: `${uploaded.filename}（${roleLabel(uploaded.role)}）` });
  return options;
}

function quoteFromSelection(
  document: ReviewDocument | undefined,
  selectionStart: number,
  selectionEnd: number,
): EvidenceQuote | null {
  if (!document?.text || !document.parsed_document_id || selectionEnd <= selectionStart)
    return null;
  return {
    document_id: document.id,
    parsed_document_id: document.parsed_document_id,
    quote: document.text.slice(selectionStart, selectionEnd),
    start: Array.from(document.text.slice(0, selectionStart)).length,
    end: Array.from(document.text.slice(0, selectionEnd)).length,
  };
}

function requestReceiptInput(
  mode: "document" | "answer",
  documentId: string,
  answer: string,
  expectedVersion: number,
): { document_id?: string; answer?: string; expected_version: number } | null {
  if (mode === "document" && documentId)
    return { document_id: documentId, expected_version: expectedVersion };
  if (mode === "answer" && answer.trim())
    return { answer: answer.trim(), expected_version: expectedVersion };
  return null;
}

function requestVerificationInput(
  ready: boolean,
  attemptId: string | null,
  runId: string,
  quote: EvidenceQuote | null,
  source: EvidenceRequest["source_evaluation"],
  matchingEvaluations: ConstraintEvaluation[],
  gap: BusinessAgentGap | undefined,
  reason: string,
  expectedVersion: number,
): { run_id: string; attempt_id: string; evaluation_id?: string; gap_id?: string; evidence: EvidenceQuote[]; reason: string; expected_version: number } | null {
  if (!ready || !attemptId || !quote) return null;
  return {
    run_id: runId,
    attempt_id: attemptId,
    ...(source ? { evaluation_id: matchingEvaluations[0]?.id } : { gap_id: gap?.gap_id }),
    evidence: [quote],
    reason: reason.trim(),
    expected_version: expectedVersion,
  };
}

function followupReviewInput(
  requiresBusinessAt: boolean,
  businessAt: string,
): { business_at?: string } {
  return requiresBusinessAt ? { business_at: new Date(businessAt).toISOString() } : {};
}

function validBusinessReviewTime(value: string): boolean {
  return !Number.isNaN(new Date(value).getTime());
}

function submitRequestForm<Input>(
  event: FormEvent<HTMLFormElement>,
  input: Input | null,
  mutate: (value: Input) => void,
): void {
  event.preventDefault();
  if (input !== null) mutate(input);
}

function uploadRequestFile(
  file: File | null,
  role: string,
  upload: (input: { selectedFile: File; selectedRole: string }) => void,
): void {
  if (file && role) upload({ selectedFile: file, selectedRole: role });
}

function requestStatusTone(status: string): "good" | "attention" | "neutral" {
  if (status === "verified") return "good";
  if (status === "received") return "attention";
  return "neutral";
}

function requestSourceDescription(item: EvidenceRequest): string {
  const source = item.source_evaluation;
  if (source)
    return `规则检查，${displayGap(source.domain, source.capability_id)}，${source.reason || "原因未报告"}`;
  return item.source_gap
    ? displayGap(item.source_gap.domain, item.source_gap.capability_id)
    : "未报告";
}

function RequestCardHeading({
  item,
  caseId,
  context,
}: {
  item: EvidenceRequest;
  caseId: string;
  context: PanelContext;
}) {
  const assignee = context.detail?.members.find((member) => member.id === item.assignee_id);
  return (
    <>
      <header className="workflow-panel-head">
        <div>
          <h3>{item.question}</h3>
          <p className="meta">
            责任人：{assignee?.display_name ?? "姓名未报告"}；期限：{formatDateTime(item.due_at)}
          </p>
        </div>
        <StatusText tone={requestStatusTone(item.status)}>
          {statusLabels[item.status] ?? item.status}
        </StatusText>
      </header>
      <p>
        要求材料：{item.material_role ? roleLabel(item.material_role) : "不限材料类型"}；核验条件：
        {conditionLabels[item.verification_condition] ?? item.verification_condition}
      </p>
      <p>
        来源：{requestSourceDescription(item)}。{" "}
        <Link to={caseHref(caseId, item.source_run_id, item.source_attempt_id)}>
          查看来源审查
        </Link>
      </p>
    </>
  );
}

function RequestCardNextStep({ item }: { item: EvidenceRequest }) {
  if (item.status === "open")
    return <p>下一步：责任人或案件管理人提交材料或答复。</p>;
  if (item.status === "received")
    return <p>下一步：提交回执后发起新审查，再根据新审查的检查结果和原文核验。上传或提交材料本身不代表已核验。</p>;
  if (item.status === "verified") return <p>下一步：查看核验审查与处理记录。</p>;
  return null;
}

function RequestCardVerifiedOutcome({
  item,
  caseId,
}: {
  item: EvidenceRequest;
  caseId: string;
}) {
  if (item.status !== "verified" || !item.verification) return null;
  const verification = item.verification;
  const relation = verification.relation ?? verification.gap_status ?? "";
  const result = conditionLabels[relation] ?? verification.relation ?? verification.gap_status ?? "未报告";
  return (
    <div>
      <p>核验结果：{result}；{verification.reason}</p>
      {verification.evidence.map((entry, index) => (
        <blockquote key={`${entry.document_id}-${entry.start}-${index}`}>
          {entry.quote}
        </blockquote>
      ))}
      <p>
        <Link to={caseHref(caseId, verification.run_id, verification.attempt_id)}>
          查看核验审查
        </Link> · {formatDateTime(verification.verified_at)}
      </p>
    </div>
  );
}

type RequestHistoryView = {
  data: EvidenceRequestEvent[] | undefined;
  error: unknown;
  isError: boolean;
  isLoading: boolean;
  isSuccess: boolean;
  refetch: () => unknown;
};

function RequestCardReceiptSummary({
  item,
  receipts,
  history,
  documents,
}: {
  item: EvidenceRequest;
  receipts: EvidenceReceipt[];
  history: RequestHistoryView;
  documents: CaseDetail["documents"];
}) {
  if (receipts.length === 0)
    return item.status !== "open" && history.isSuccess
      ? <p>处理记录中未报告回执。</p>
      : null;
  return (
    <div>
      <p>已提交材料或答复：</p>
      <ul>
        {receipts.map((receipt) => (
          <li key={receipt.id}>
            {receipt.document_id
              ? documents.find((document) => document.id === receipt.document_id)?.filename ?? "材料名称未报告"
              : receipt.answer}
            {` · ${formatDateTime(receipt.created_at)}`}
          </li>
        ))}
      </ul>
    </div>
  );
}

function RequestCardHistoryDetails({
  history,
  context,
  documents,
}: {
  history: RequestHistoryView;
  context: PanelContext;
  documents: CaseDetail["documents"];
}) {
  return (
    <details>
      <summary>处理记录</summary>
      {history.data ? (
        <ol>
          {history.data.map((event) => (
            <RequestCardHistoryEvent key={`${event.request.version}-${event.type}`}
              event={event} context={context} documents={documents} />
          ))}
        </ol>
      ) : null}
    </details>
  );
}

function RequestCardHistoryEvent({
  event,
  context,
  documents,
}: {
  event: EvidenceRequestEvent;
  context: PanelContext;
  documents: CaseDetail["documents"];
}) {
  const filename = historyReceiptFilename(event, documents);
  return (
    <li>
      {eventLabels[event.type] ?? event.type} · {historyActorName(event, context)} · {formatDateTime(event.occurred_at)}
      {event.receipt?.document_id ? <p>提交材料：{filename}</p> : null}
      {event.receipt?.answer ? <p>提交答复：{event.receipt.answer}</p> : null}
    </li>
  );
}

function historyActorName(event: EvidenceRequestEvent, context: PanelContext): string {
  const memberName = context.detail?.members.find((member) => member.id === event.actor_id)?.display_name;
  return memberName ?? (event.actor_id === "system" ? "系统" : "姓名未报告");
}

function historyReceiptFilename(
  event: EvidenceRequestEvent,
  documents: CaseDetail["documents"],
): string | undefined {
  if (!event.receipt?.document_id) return undefined;
  return documents.find((document) => document.id === event.receipt?.document_id)?.filename ?? "材料名称未报告";
}

function RequestCardHistory({
  item,
  documents,
  context,
  history,
  receipts,
}: {
  item: EvidenceRequest;
  documents: CaseDetail["documents"];
  context: PanelContext;
  history: RequestHistoryView;
  receipts: EvidenceReceipt[];
}) {
  return (
    <>
      {history.isLoading ? <p role="status">正在读取处理记录。</p> : null}
      <ErrorNotice error={history.error} />
      {history.isError ? (
        <Button htmlType="button" onClick={() => void history.refetch()}>
          重试读取记录
        </Button>
      ) : null}
      <RequestCardReceiptSummary item={item} receipts={receipts} history={history} documents={documents} />
      <RequestCardHistoryDetails history={history} context={context} documents={documents} />
    </>
  );
}

type RequestReceiptFormProps = {
  item: EvidenceRequest;
  canSubmit: boolean;
  canUpload: boolean;
  documents: CaseDetail["documents"];
  documentOptions: { value: string; label: string }[];
  mode: "document" | "answer";
  setMode: (mode: "document" | "answer") => void;
  documentId: string;
  setDocumentId: (id: string) => void;
  answer: string;
  setAnswer: (answer: string) => void;
  uploadRole: string;
  setUploadRole: (role: string) => void;
  file: File | null;
  setFile: (file: File | null) => void;
  uploadPending: boolean;
  uploadError: unknown;
  uploaded: { id: string; filename: string; role: string } | null;
  onUpload: () => void;
  receiptPending: boolean;
  receiptError: unknown;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
};

function RequestFileUpload({
  item,
  canUpload,
  uploadRole,
  setUploadRole,
  file,
  setFile,
  uploadPending,
  uploadError,
  uploaded,
  onUpload,
}: Pick<RequestReceiptFormProps,
  "item" | "canUpload" | "uploadRole" | "setUploadRole" | "file" | "setFile" |
  "uploadPending" | "uploadError" | "uploaded" | "onUpload">) {
  if (!canUpload) return <p>如需上传新材料，请由有上传权限的案件成员登记后再选择。</p>;
  return (
    <>
      {!item.material_role ? (
        <label className="field">上传材料类型
          <Select<string> aria-label="上传材料类型" placeholder="选择材料类型" value={uploadRole || undefined}
            onChange={setUploadRole} options={materialRoles} />
        </label>
      ) : null}
      <label className="field">上传补充文件
        <input type="file" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
      </label>
      <p className="meta">同类型上传会形成新版本。上传后还需提交回执；单文件不超过 32 MiB。</p>
      <Button htmlType="button" disabled={!file || !uploadRole || uploadPending} loading={uploadPending} onClick={onUpload}>
        上传补充文件
      </Button>
      <ErrorNotice error={uploadError} />
      {uploaded ? <p role="status">已上传 {uploaded.filename}，请提交材料回执。</p> : null}
    </>
  );
}

function RequestDocumentReceiptFields({
  ...props
}: RequestReceiptFormProps) {
  return (
    <>
      <label className="field">选择案件材料
        <Select<string> aria-label="选择案件材料" placeholder="选择材料" value={props.documentId || undefined}
          onChange={props.setDocumentId} options={props.documentOptions} />
      </label>
      {props.documentOptions.length === 0 ? <p>当前没有符合材料类型的案件材料。</p> : null}
      <RequestFileUpload {...props} />
    </>
  );
}

function RequestReceiptFields(props: RequestReceiptFormProps) {
  return (
    <>
      <label className="field">提交方式
        <Select<"document" | "answer"> aria-label="提交方式" value={props.mode} onChange={props.setMode}
          options={[{ value: "document", label: "案件材料" }, { value: "answer", label: "文字答复" }]} />
      </label>
      {props.mode === "document" ? (
        <RequestDocumentReceiptFields {...props} />
      ) : (
        <label className="field">文字答复
          <Input.TextArea maxLength={4000} value={props.answer} onChange={(event) => props.setAnswer(event.target.value)} />
        </label>
      )}
    </>
  );
}

function RequestReceiptForm(props: RequestReceiptFormProps) {
  const { item } = props;
  if (!((item.status === "open" || item.status === "received") && props.canSubmit)) return null;
  const disabled = props.receiptPending ||
    (props.mode === "document" ? !props.documentId : !props.answer.trim());
  return (
    <form className="workflow-form" onSubmit={props.onSubmit}>
      <h4>提交材料或答复</h4>
      <p className="meta">由当前任务责任人或案件管理人提交；权限和请求版本由服务端核对。</p>
      <RequestReceiptFields {...props} />
      <ErrorNotice error={props.receiptError} />
      <Button type="primary" htmlType="submit" loading={props.receiptPending} disabled={disabled}>
        提交回执
      </Button>
    </form>
  );
}

function FollowupReview({
  canSubmit,
  requiresBusinessAt,
  businessAt,
  setBusinessAt,
  startPending,
  startError,
  validBusinessAt,
  onStart,
}: {
  canSubmit: boolean;
  requiresBusinessAt: boolean;
  businessAt: string;
  setBusinessAt: (value: string) => void;
  startPending: boolean;
  startError: unknown;
  validBusinessAt: boolean;
  onStart: () => void;
}) {
  if (!canSubmit) return null;
  return (
    <div className="workflow-form">
      <h4>发起后续审查</h4>
      <p className="meta">审查必须在回执提交之后受理，才能作为该回执的核验依据。</p>
      {requiresBusinessAt ? (
        <label className="field">审查业务时间（本地时间）
          <Input type="datetime-local" value={businessAt} onChange={(event) => setBusinessAt(event.target.value)} />
        </label>
      ) : null}
      <ErrorNotice error={startError} />
      <Button htmlType="button" loading={startPending}
        disabled={startPending || (requiresBusinessAt && !validBusinessAt)} onClick={onStart}>
        发起后续审查
      </Button>
    </div>
  );
}

function VerificationReadiness({
  frozenLater,
  observed,
  matchDescription,
}: {
  frozenLater: boolean;
  observed: boolean;
  matchDescription: string;
}) {
  if (!frozenLater)
    return <p className="callout">当前执行记录不能用于核验。请打开提交回执后已完成或部分完成、且已保存审查材料的审查。</p>;
  if (!observed)
    return <p className="callout">当前审查没有唯一匹配且达到请求条件的实际检查，仍需补充或复审。</p>;
  return <p>当前审查的匹配检查：{matchDescription}。</p>;
}

function VerificationEvidenceFields({
  receiptId,
  onReceiptChange,
  receipts,
  documents,
  receipt,
  quoteDocumentId,
  onQuoteDocumentChange,
  frozenDocuments,
  frozenDocument,
  onSelectQuote,
  quote,
  quoteValid,
}: {
  receiptId: string;
  onReceiptChange: (id: string) => void;
  receipts: EvidenceReceipt[];
  documents: CaseDetail["documents"];
  receipt: EvidenceReceipt | undefined;
  quoteDocumentId: string;
  onQuoteDocumentChange: (id: string) => void;
  frozenDocuments: ReviewDocument[];
  frozenDocument: ReviewDocument | undefined;
  onSelectQuote: (event: SyntheticEvent<HTMLTextAreaElement>) => void;
  quote: EvidenceQuote | null;
  quoteValid: boolean;
}) {
  return (
    <>
      <RequestReceiptSelector receiptId={receiptId} onReceiptChange={onReceiptChange}
        receipts={receipts} documents={documents} />
      <RequestFrozenDocumentChoice receipt={receipt} frozenDocuments={frozenDocuments}
        quoteDocumentId={quoteDocumentId} onQuoteDocumentChange={onQuoteDocumentChange} />
      <RequestQuoteText document={frozenDocument} onSelectQuote={onSelectQuote} />
      <RequestSelectedQuote quote={quote} quoteValid={quoteValid} />
    </>
  );
}

function RequestReceiptSelector({
  receiptId,
  onReceiptChange,
  receipts,
  documents,
}: Pick<Parameters<typeof VerificationEvidenceFields>[0], "receiptId" | "onReceiptChange" | "receipts" | "documents">) {
  const options = receipts.map((entry) => ({
    value: entry.id,
    label: `${entry.document_id
      ? (documents.find((document) => document.id === entry.document_id)?.filename ?? "提交材料")
      : `答复：${entry.answer ?? ""}`} · ${formatDateTime(entry.created_at)}`,
  }));
  return (
    <label className="field">选择提交回执
      <Select<string> aria-label="选择提交回执" placeholder="选择回执" value={receiptId || undefined}
        onChange={onReceiptChange} options={options} />
    </label>
  );
}

function RequestFrozenDocumentChoice({
  receipt,
  frozenDocuments,
  quoteDocumentId,
  onQuoteDocumentChange,
}: Pick<Parameters<typeof VerificationEvidenceFields>[0], "receipt" | "frozenDocuments" | "quoteDocumentId" | "onQuoteDocumentChange">) {
  const options = receipt
    ? frozenDocuments.map((document) => ({ value: document.id, label: `${document.filename}（${roleLabel(document.role)}）` }))
    : [];
  return (
    <>
      {receipt && frozenDocuments.length === 0 ? <p>当前审查未包含可引用的回执材料或答复原文。</p> : null}
      <label className="field">选择本次审查材料
        <Select<string> aria-label="选择本次审查材料" placeholder="选择本次审查材料"
          value={quoteDocumentId || undefined} onChange={onQuoteDocumentChange} options={options} />
      </label>
    </>
  );
}

function RequestQuoteText({
  document,
  onSelectQuote,
}: {
  document: ReviewDocument | undefined;
  onSelectQuote: (event: SyntheticEvent<HTMLTextAreaElement>) => void;
}) {
  if (!document?.text) return null;
  return (
    <label className="field">在本次审查使用的原文中选择引用内容
      <Input.TextArea aria-label="本次审查使用的原文" readOnly rows={8}
        value={document.text} onSelect={onSelectQuote} />
      <span className="meta">用鼠标或键盘选择覆盖该检查引用内容的完整原文，最多 4096 个字符；过短的片段可能无法通过核验。</span>
    </label>
  );
}

function RequestSelectedQuote({
  quote,
  quoteValid,
}: Pick<Parameters<typeof VerificationEvidenceFields>[0], "quote" | "quoteValid">) {
  if (!quote) return null;
  return (
    <div>
      <p>已选引用：</p>
      <blockquote>{quote.quote}</blockquote>
      {!quoteValid ? <p className="callout">引用过长，或未包含所提交的文字答复。</p> : null}
    </div>
  );
}

function RequestVerificationForm({
  frozenLater,
  observed,
  matchDescription,
  historyError,
  evidence,
  canVerify,
  reason,
  setReason,
  verifyError,
  verifyPending,
  verifiedReady,
  onSubmit,
}: {
  frozenLater: boolean;
  observed: boolean;
  matchDescription: string;
  historyError: boolean;
  evidence: Omit<Parameters<typeof VerificationEvidenceFields>[0], "documents"> & {
    documents: CaseDetail["documents"];
  };
  canVerify: boolean;
  reason: string;
  setReason: (value: string) => void;
  verifyError: unknown;
  verifyPending: boolean;
  verifiedReady: boolean;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <form className="workflow-form" onSubmit={onSubmit}>
      <h4>核验补充材料</h4>
      <p className="meta">请在提交回执后的审查工作区选择回执和本次审查使用的原文。系统会核对检查结果、材料关联和审查规则。</p>
      <VerificationReadiness frozenLater={frozenLater} observed={observed} matchDescription={matchDescription} />
      {historyError ? <p>无法读取回执，不能核验。</p> : null}
      <VerificationEvidenceFields {...evidence} />
      <label className="field">核验理由
        <Input.TextArea required maxLength={2000} value={reason} onChange={(event) => setReason(event.target.value)} disabled={!canVerify} />
      </label>
      {!canVerify ? <p>当前身份不能提交核验。</p> : null}
      <ErrorNotice error={verifyError} />
      <Button aria-label="提交核验" type="primary" htmlType="submit" loading={verifyPending}
        disabled={!verifiedReady || verifyPending}>
        提交核验
      </Button>
    </form>
  );
}

type EvidencePanelProps = {
  caseId: string;
  runId: string;
  attemptId: string | null;
  canManage: boolean;
  evaluations: ConstraintEvaluation[];
  agentReview: BusinessAgentReview | null;
  documents: CaseDetail["documents"];
};

type PanelContext = {
  workspace: ReviewWorkspace | undefined;
  detail: CaseDetail | undefined;
  detailError: unknown;
  detailReady: boolean;
  actor: Actor | undefined;
  tasks: WorkbenchTask[] | undefined;
  tasksError: unknown;
  tasksLoading: boolean;
  refreshTasks: () => void;
};

type RequestSourceOption = { value: string; label: string };

function isFrozenRequestSourceAttempt(
  attempt: NonNullable<ReviewWorkspace["review_attempts"]>["attempt"] | undefined,
  attemptId: string | null,
  runId: string,
): boolean {
  return !!attemptId && attempt?.id === attemptId && attempt.run_id === runId &&
    attempt.history_kind === "frozen" && !!attempt.finished_at &&
    (attempt.state === "completed" || attempt.state === "partial");
}

function countRequestSourceIds<T>(items: T[], getId: (item: T) => string): Map<string, number> {
  const counts = new Map<string, number>();
  for (const item of items) {
    const id = getId(item);
    counts.set(id, (counts.get(id) ?? 0) + 1);
  }
  return counts;
}

function isEligibleRequestEvaluation(entry: ConstraintEvaluation, counts: Map<string, number>): boolean {
  return !!entry.id && !!entry.domain && !!entry.rule_version &&
    (entry.relation === "insufficient" || entry.relation === "unknown") && counts.get(entry.id) === 1;
}

function eligibleRequestEvaluations(evaluations: ConstraintEvaluation[]): ConstraintEvaluation[] {
  const counts = countRequestSourceIds(evaluations, (evaluation) => evaluation.id);
  return evaluations.filter((entry) => isEligibleRequestEvaluation(entry, counts));
}

function isEligibleRequestGap(gap: BusinessAgentGap, counts: Map<string, number>): boolean {
  return !!gap.gap_id && !!gap.domain && !!gap.capability_id &&
    ["insufficient", "unknown", "unsupported"].includes(gap.status) && counts.get(gap.gap_id) === 1;
}

function eligibleRequestGaps(
  agentReview: BusinessAgentReview | null,
  attemptId: string | null,
): BusinessAgentGap[] {
  const reported = agentReview && agentReview.attempt_id === attemptId ? agentReview.gaps ?? [] : [];
  const counts = countRequestSourceIds(reported, (gap) => gap.gap_id);
  return reported.filter((gap) => isEligibleRequestGap(gap, counts));
}

function requestSourceOptions(
  evaluations: ConstraintEvaluation[],
  gaps: BusinessAgentGap[],
): RequestSourceOption[] {
  return [
    ...evaluations.map((entry, index) => ({
      value: `evaluation:${entry.id}`,
      label: `规则检查 ${index + 1}：${displayGap(entry.domain, entry.capability_id)}，${entry.reason || "原因未报告"}（${relationLabel(entry.relation)}）`,
    })),
    ...gaps.map((gap, index) => ({
      value: `gap:${gap.gap_id}`,
      label: `审查缺口 ${index + 1}：${displayGap(gap.domain, gap.capability_id)}`,
    })),
  ];
}

function eligibleExistingRequestTasks(
  tasks: WorkbenchTask[] | undefined,
  caseId: string,
  members: CaseDetail["members"] | undefined,
): WorkbenchTask[] {
  return (tasks ?? []).filter((entry) => entry.case_id === caseId && entry.status !== "completed" && !!entry.due_at &&
    members?.some((member) => member.id === entry.assignee_id));
}

function existingTaskControlDisabled(task: WorkbenchTask | null, pending: boolean, tasksLoading: boolean): boolean {
  return !!task || pending || tasksLoading;
}

function resolveRequestTask(
  task: WorkbenchTask | null,
  taskMode: "new" | "existing",
  existingTask: WorkbenchTask | undefined,
): WorkbenchTask | null {
  return task ?? (taskMode === "existing" ? existingTask ?? null : null);
}

function isRequestTaskReady(
  task: WorkbenchTask | null,
  taskMode: "new" | "existing",
  existingTask: WorkbenchTask | undefined,
  title: string,
  assignee: string,
  dueValid: boolean,
): boolean {
  return !!task || (taskMode === "existing" ? !!existingTask : !!title.trim() && !!assignee && dueValid);
}

function canSubmitRequest(
  canManage: boolean,
  frozen: boolean,
  attemptId: string | null,
  validSource: boolean,
  question: string,
  taskReady: boolean,
  pending: boolean,
): attemptId is string {
  return canManage && frozen && !!attemptId && validSource && !!question.trim() && taskReady && !pending;
}

function isCreateRequestButtonDisabled(
  frozen: boolean,
  validSource: boolean,
  question: string,
  taskReady: boolean,
): boolean {
  return !frozen || !validSource || !question.trim() || !taskReady;
}

function createRequestButtonLabel(task: WorkbenchTask | null, taskMode: "new" | "existing"): string {
  return task ? "重试创建补充请求" : taskMode === "existing" ? "发起补充请求" : "创建任务并发起请求";
}

function createEvidenceRequestInput(
  task: WorkbenchTask,
  source: string,
  runId: string,
  attemptId: string,
  question: string,
  role: string,
  condition: "compatible" | "conflict",
): CreateEvidenceRequestInput {
  const isGap = source.startsWith("gap:");
  return {
    task_id: task.id,
    task_version: task.version,
    source_run_id: runId,
    source_attempt_id: attemptId,
    ...(isGap ? { source_gap_id: source.slice(4) } : { source_evaluation_id: source.slice(11) }),
    question: question.trim(),
    ...(role ? { material_role: role } : {}),
    verification_condition: isGap ? "resolved" : condition,
  };
}

function CreateRequestSourceFields({
  frozen,
  options,
  source,
  pending,
  onSourceChange,
}: {
  frozen: boolean;
  options: RequestSourceOption[];
  source: string;
  pending: boolean;
  onSourceChange: (value: string) => void;
}) {
  return (
    <>
      {!frozen ? <p className="callout">只有已完成或部分完成、且已保存审查材料的执行记录可以发起请求。</p> : null}
      {frozen && options.length === 0 ? <p>本次审查没有返回符合条件的待补充检查。</p> : null}
      <label className="field">缺失检查
        <Select<string> aria-label="缺失检查" placeholder="选择缺失检查" value={source || undefined} onChange={onSourceChange}
          options={options} disabled={!frozen || pending || options.length === 0} />
      </label>
    </>
  );
}

function ExistingRequestTaskLoadStatus({ context }: { context: PanelContext }) {
  if (context.tasksLoading) return <p role="status">正在读取案件任务。</p>;
  return !context.tasks && !context.tasksError ? <p>案件任务尚未载入。</p> : null;
}

function ExistingRequestTaskEmptyStatus({ context, eligibleTasks }: {
  context: PanelContext;
  eligibleTasks: WorkbenchTask[];
}) {
  return context.tasks && eligibleTasks.length === 0 ? <p>暂无包含责任人和期限的未完成案件任务。</p> : null;
}

function ExistingRequestTaskFields({ task, pending, existingTaskId, onExistingTaskChange, context, eligibleTasks }: {
  task: WorkbenchTask | null;
  pending: boolean;
  existingTaskId: string;
  onExistingTaskChange: (value: string) => void;
  context: PanelContext;
  eligibleTasks: WorkbenchTask[];
}) {
  return (
    <>
      <label className="field">选择已有任务
        <Select<string> aria-label="选择已有任务" placeholder="选择案件任务" value={existingTaskId || undefined}
          onChange={onExistingTaskChange} disabled={existingTaskControlDisabled(task, pending, context.tasksLoading)} options={eligibleTasks.map((entry) => ({
            value: entry.id, label: `${entry.title} · ${context.detail?.members.find((member) => member.id === entry.assignee_id)?.display_name ?? "责任人姓名未报告"} · ${formatDateTime(entry.due_at ?? "")}`,
          }))} />
      </label>
      <ExistingRequestTaskLoadStatus context={context} />
      <Button htmlType="button" loading={context.tasksLoading} disabled={existingTaskControlDisabled(task, pending, context.tasksLoading)} onClick={context.refreshTasks}>刷新案件任务</Button>
      <ErrorNotice error={context.tasksError} />
      <ExistingRequestTaskEmptyStatus context={context} eligibleTasks={eligibleTasks} />
    </>
  );
}

function NewRequestTaskFields({ context, task, pending, title, onTitleChange, assignee, onAssigneeChange, due, onDueChange }: {
  context: PanelContext;
  task: WorkbenchTask | null;
  pending: boolean;
  title: string;
  onTitleChange: (value: string) => void;
  assignee: string;
  onAssigneeChange: (value: string) => void;
  due: string;
  onDueChange: (value: string) => void;
}) {
  return (
    <>
      <label className="field">任务名称
        <Input required maxLength={200} value={title} onChange={(event) => onTitleChange(event.target.value)} disabled={!!task || pending} />
      </label>
      <label className="field">责任人
        <Select<string> aria-label="责任人" placeholder="选择案件成员" value={assignee || undefined} onChange={onAssigneeChange}
          options={(context.detail?.members ?? []).map((member) => ({ value: member.id, label: member.display_name }))}
          disabled={!!task || pending || !context.detail} />
      </label>
      <label className="field">完成期限（本地时间）
        <Input type="datetime-local" required value={due} onChange={(event) => onDueChange(event.target.value)} disabled={!!task || pending} />
      </label>
    </>
  );
}

function CreateRequestTaskFields({
  taskMode, onTaskModeChange, task, pending, existingTaskId, onExistingTaskChange, context, eligibleTasks,
  title, onTitleChange, assignee, onAssigneeChange, due, onDueChange,
}: {
  taskMode: "new" | "existing";
  onTaskModeChange: (value: "new" | "existing") => void;
  task: WorkbenchTask | null;
  pending: boolean;
  existingTaskId: string;
  onExistingTaskChange: (value: string) => void;
  context: PanelContext;
  eligibleTasks: WorkbenchTask[];
  title: string;
  onTitleChange: (value: string) => void;
  assignee: string;
  onAssigneeChange: (value: string) => void;
  due: string;
  onDueChange: (value: string) => void;
}) {
  return (
    <>
      <label className="field">关联任务
        <Select<"new" | "existing"> aria-label="关联任务" value={taskMode} onChange={onTaskModeChange} disabled={!!task || pending}
          options={[{ value: "new", label: "创建新任务" }, { value: "existing", label: "选择已有任务" }]} />
      </label>
      {taskMode === "existing"
        ? <ExistingRequestTaskFields task={task} pending={pending} existingTaskId={existingTaskId} onExistingTaskChange={onExistingTaskChange} context={context} eligibleTasks={eligibleTasks} />
        : <NewRequestTaskFields context={context} task={task} pending={pending} title={title} onTitleChange={onTitleChange}
          assignee={assignee} onAssigneeChange={onAssigneeChange} due={due} onDueChange={onDueChange} />}
    </>
  );
}

function CreateRequestDetailsFields({
  question,
  onQuestionChange,
  role,
  onRoleChange,
  source,
  condition,
  onConditionChange,
  pending,
}: {
  question: string;
  onQuestionChange: (value: string) => void;
  role: string;
  onRoleChange: (value: string) => void;
  source: string;
  condition: "compatible" | "conflict";
  onConditionChange: (value: "compatible" | "conflict") => void;
  pending: boolean;
}) {
  return (
    <>
      <label className="field">需要补充的问题
        <Input.TextArea required maxLength={2000} value={question} onChange={(event) => onQuestionChange(event.target.value)} disabled={pending} />
      </label>
      <label className="field">要求的材料类型
        <Select<string> aria-label="要求的材料类型" value={role} onChange={onRoleChange} disabled={pending}
          options={[{ value: "", label: "不限材料类型" }, ...materialRoles]} />
      </label>
      {source.startsWith("gap:") ? <p>核验条件：缺口已解决。</p> : (
        <label className="field">核验条件
          <Select<"compatible" | "conflict"> aria-label="核验条件" value={condition} onChange={onConditionChange} disabled={pending}
            options={[{ value: "compatible", label: "检查结果一致" }, { value: "conflict", label: "检查结果存在冲突" }]} />
        </label>
      )}
    </>
  );
}

function CreateRequestLinkedTaskFooter({ task, pending, onReplace }: {
  task: WorkbenchTask | null;
  pending: boolean;
  onReplace: () => void;
}) {
  if (!task) return null;
  return (
    <>
      <p className="callout">已关联任务“{task.title}”。请求未完成时可在此重试，任务不会再次创建。</p>
      <Button htmlType="button" disabled={pending} onClick={onReplace}>更换关联任务</Button>
    </>
  );
}

function CreateRequest({ caseId, runId, attemptId, evaluations, agentReview, canManage, context }: Omit<EvidencePanelProps, "documents"> & { context: PanelContext }) {
  const createTask = useCreateWorkbenchTask(caseId);
  const client = useQueryClient();
  const keyFor = useOperationKeys();
  const createGeneration = useRef(0);
  const [source, setSource] = useState("");
  const [title, setTitle] = useState("");
  const [assignee, setAssignee] = useState("");
  const [due, setDue] = useState("");
  const [question, setQuestion] = useState("");
  const [role, setRole] = useState("");
  const [condition, setCondition] = useState<"compatible" | "conflict">("compatible");
  const [task, setTask] = useState<WorkbenchTask | null>(null);
  const [taskMode, setTaskMode] = useState<"new" | "existing">("new");
  const [existingTaskId, setExistingTaskId] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [pending, setPending] = useState(false);
  const [created, setCreated] = useState(false);
  if (!canManage) return null;

  const frozen = isFrozenRequestSourceAttempt(context.workspace?.review_attempts?.attempt, attemptId, runId);
  const missingEvaluations = eligibleRequestEvaluations(evaluations);
  const gaps = eligibleRequestGaps(agentReview, attemptId);
  const options = requestSourceOptions(missingEvaluations, gaps);
  const validSource = options.some((option) => option.value === source);
  const dueValid = validBusinessReviewTime(due);
  const eligibleTasks = eligibleExistingRequestTasks(context.tasks, caseId, context.detail?.members);
  const existingTask = eligibleTasks.find((entry) => entry.id === existingTaskId);
  const taskReady = isRequestTaskReady(task, taskMode, existingTask, title, assignee, dueValid);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canSubmitRequest(canManage, frozen, attemptId, validSource, question, taskReady, pending)) return;
    setError(null);
    setCreated(false);
    setPending(true);
    try {
      let linkedTask = resolveRequestTask(task, taskMode, existingTask);
      if (!linkedTask) {
        linkedTask = await createTask.mutateAsync({
          title: title.trim(), kind: "review", assignee_id: assignee, due_at: new Date(due).toISOString(),
          status: "open", priority: "high", finding_id: null,
        });
      }
      setTask(linkedTask);
      const input = createEvidenceRequestInput(linkedTask, source, runId, attemptId, question, role, condition);
      await createEvidenceRequest(caseId, input, keyFor(`create:${createGeneration.current}`, input));
      createGeneration.current += 1;
      setTask(null);
      setExistingTaskId("");
      setSource("");
      setTitle("");
      setQuestion("");
      setCreated(true);
      await client.invalidateQueries({ queryKey: requestKey(caseId) });
    } catch (caught) {
      setError(caught);
    } finally {
      setPending(false);
    }
  }

  function replaceLinkedTask() {
    setTask(null);
    setError(null);
  }

  const submitLabel = createRequestButtonLabel(task, taskMode);
  const submitDisabled = isCreateRequestButtonDisabled(frozen, validSource, question, taskReady);

  return (
    <form className="workflow-form" onSubmit={(event) => void submit(event)}>
      <h3>发起补充材料请求</h3>
      <p className="meta">选择本次审查中需要补充材料的检查，再关联一项有责任人和截止时间的案件任务。</p>
      <CreateRequestSourceFields frozen={frozen} options={options} source={source} pending={pending} onSourceChange={setSource} />
      <CreateRequestTaskFields
        taskMode={taskMode} onTaskModeChange={setTaskMode} task={task} pending={pending}
        existingTaskId={existingTaskId} onExistingTaskChange={setExistingTaskId} context={context} eligibleTasks={eligibleTasks}
        title={title} onTitleChange={setTitle} assignee={assignee} onAssigneeChange={setAssignee}
        due={due} onDueChange={setDue}
      />
      <ErrorNotice error={context.detailError} />
      <CreateRequestDetailsFields
        question={question} onQuestionChange={setQuestion} role={role} onRoleChange={setRole}
        source={source} condition={condition} onConditionChange={setCondition} pending={pending}
      />
      <CreateRequestLinkedTaskFooter task={task} pending={pending} onReplace={replaceLinkedTask} />
      <ErrorNotice error={error} />
      {created ? <p role="status">补充材料请求已创建。</p> : null}
      <Button aria-label={submitLabel} type="primary" htmlType="submit" loading={pending} disabled={submitDisabled}>
        {submitLabel}
      </Button>
    </form>
  );
}

function RequestCard({ item, caseId, runId, attemptId, documents, evaluations, agentReview, context }: EvidencePanelProps & { item: EvidenceRequest; context: PanelContext }) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const start = useStartReview(caseId);
  const keyFor = useOperationKeys();
  const history = useQuery({
    queryKey: [...requestKey(caseId), item.id, "history"],
    queryFn: ({ signal }) => listEvidenceHistory(caseId, item.id, signal),
    retry: false,
  });
  const [mode, setMode] = useState<"document" | "answer">("document");
  const [documentId, setDocumentId] = useState("");
  const [answer, setAnswer] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [uploadRole, setUploadRole] = useState(item.material_role ?? "");
  const [uploaded, setUploaded] = useState<{ id: string; filename: string; role: string } | null>(null);
  const [receiptId, setReceiptId] = useState("");
  const [quoteDocumentId, setQuoteDocumentId] = useState("");
  const [quote, setQuote] = useState<EvidenceQuote | null>(null);
  const [reason, setReason] = useState("");
  const [businessAt, setBusinessAt] = useState("");
  const { canSubmit, canUpload, canVerify } = requestPermissions(context);
  const receipts = (history.data ?? []).flatMap((event) => event.receipt ? [event.receipt] : []);
  const receipt = receipts.find((entry) => entry.id === receiptId);
  const reviewFacts = requestCardReviewFacts(item, attemptId, runId, evaluations, agentReview, context);
  const { source, matchingEvaluations, gap, observed, frozenLater, requiresBusinessAt } = reviewFacts;
  const frozenDocuments = receiptFrozenDocuments(context.workspace?.documents, item, receipt);
  const frozenDocument = frozenDocuments.find((document) => document.id === quoteDocumentId);
  const quoteValid = quoteMatchesFrozenDocument(quote, frozenDocument, receipt);
  const verifiedReady = requestVerificationReady(canVerify, item.status, receipt, frozenLater, observed, quoteValid, reason);
  const validBusinessAt = validBusinessReviewTime(businessAt);
  const availableDocuments = availableRequestDocuments(documents, item.material_role);
  const documentOptions = requestDocumentOptions(availableDocuments, uploaded);

  const upload = useMutation({
    mutationFn: ({ selectedFile, selectedRole }: { selectedFile: File; selectedRole: string }) => uploadEvidenceDocument(caseId, selectedFile, selectedRole),
    onSuccess: async (result, input) => {
      setUploaded({ id: result.id, filename: input.selectedFile.name, role: input.selectedRole });
      setDocumentId(result.id);
      setFile(null);
      await client.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await client.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
  const submitReceipt = useMutation({
    mutationFn: (input: { document_id?: string; answer?: string; expected_version: number }) =>
      createEvidenceReceipt(caseId, item.id, input, keyFor(`receipt:${item.id}`, input)),
    onSuccess: async () => {
      setAnswer("");
      await client.invalidateQueries({ queryKey: requestKey(caseId) });
    },
  });
  const verify = useMutation({
    mutationFn: (input: { run_id: string; attempt_id: string; evaluation_id?: string; gap_id?: string; evidence: EvidenceQuote[]; reason: string; expected_version: number }) =>
      verifyEvidenceRequest(caseId, item.id, receiptId, input, keyFor(`verify:${item.id}:${receiptId}`, input)),
    onSuccess: async () => {
      setReason("");
      setQuote(null);
      await client.invalidateQueries({ queryKey: requestKey(caseId) });
    },
  });

  function selectQuote(event: SyntheticEvent<HTMLTextAreaElement>) {
    if (!frozenDocument?.text || !frozenDocument.parsed_document_id) return;
    const { selectionStart, selectionEnd } = event.currentTarget;
    setQuote(quoteFromSelection(frozenDocument, selectionStart, selectionEnd));
  }

  function startFollowupReview() {
    start.mutate(followupReviewInput(requiresBusinessAt, businessAt), {
      onSuccess: (accepted) => {
        void navigate(caseHref(caseId, accepted.id, accepted.attempt_id));
      },
    });
  }

  function changeReceipt(receipt: string) {
    setReceiptId(receipt);
    setQuoteDocumentId("");
    setQuote(null);
  }

  function changeQuoteDocument(documentId: string) {
    setQuoteDocumentId(documentId);
    setQuote(null);
  }

  const matchDescription = requestMatchDescription(source, matchingEvaluations, gap);
  const receiptInput = requestReceiptInput(mode, documentId, answer, item.version);
  const verificationInput = requestVerificationInput(
    verifiedReady, attemptId, runId, quote, source, matchingEvaluations, gap, reason, item.version,
  );

  return (
    <li>
      <RequestCardHeading item={item} caseId={caseId} context={context} />
      <RequestCardNextStep item={item} />
      <RequestCardVerifiedOutcome item={item} caseId={caseId} />
      <RequestCardHistory
        item={item}
        documents={documents}
        context={context}
        history={history}
        receipts={receipts}
      />
      <RequestReceiptForm
        item={item} canSubmit={canSubmit} canUpload={canUpload} documents={documents}
        documentOptions={documentOptions} mode={mode} setMode={setMode} documentId={documentId}
        setDocumentId={setDocumentId} answer={answer} setAnswer={setAnswer} uploadRole={uploadRole}
        setUploadRole={setUploadRole} file={file} setFile={setFile} uploadPending={upload.isPending}
        uploadError={upload.error} uploaded={uploaded}
        onUpload={() => uploadRequestFile(file, uploadRole, upload.mutate)}
        receiptPending={submitReceipt.isPending} receiptError={submitReceipt.error}
        onSubmit={(event) => submitRequestForm(event, receiptInput, submitReceipt.mutate)}
      />
      {item.status === "received" ? (
        <>
          <FollowupReview canSubmit={canSubmit} requiresBusinessAt={requiresBusinessAt} businessAt={businessAt}
            setBusinessAt={setBusinessAt} startPending={start.isPending} startError={start.error}
            validBusinessAt={validBusinessAt} onStart={startFollowupReview} />
          <RequestVerificationForm
            frozenLater={frozenLater} observed={observed} matchDescription={matchDescription}
            historyError={history.isError}
            evidence={{ receiptId, onReceiptChange: changeReceipt, receipts, documents, receipt,
              quoteDocumentId, onQuoteDocumentChange: changeQuoteDocument, frozenDocuments,
              frozenDocument, onSelectQuote: selectQuote, quote, quoteValid }}
            canVerify={canVerify} reason={reason} setReason={setReason} verifyError={verify.error}
            verifyPending={verify.isPending} verifiedReady={verifiedReady}
            onSubmit={(event) => submitRequestForm(event, verificationInput, verify.mutate)}
          />
        </>
      ) : null}
    </li>
  );
}

export function EvidenceRequestPanel(props: EvidencePanelProps) {
  const client = useQueryClient();
  const [params] = useSearchParams();
  const selectedAttemptId = params.get("attempt_id");
  // Observe the exact queries already owned by ReviewPage without another fetch or polling loop.
  const workspace = useQuery({
    queryKey: queryKeys.review(props.caseId, props.runId, selectedAttemptId),
    queryFn: ({ signal }) => getReview(props.caseId, props.runId, selectedAttemptId, signal),
    enabled: false,
  });
  const detail = useQuery({
    queryKey: queryKeys.case(props.caseId),
    queryFn: ({ signal }) => getCase(props.caseId, signal),
    enabled: false,
  });
  const session = useQuery({
    queryKey: queryKeys.session,
    queryFn: ({ signal }) => getSession("client", signal),
    enabled: false,
  });
  const workbench = useQuery({
    queryKey: queryKeys.workbench("week"),
    queryFn: ({ signal }) => getWorkbench("week", signal),
    enabled: false,
  });
  const context: PanelContext = {
    workspace: workspace.data, detail: detail.data, detailError: detail.error,
    detailReady: detail.isSuccess && !detail.isFetching, actor: session.isSuccess ? session.data : undefined,
    tasks: workbench.isSuccess && !workbench.isFetching ? workbench.data.tasks : undefined,
    tasksError: workbench.error, tasksLoading: workbench.isFetching,
    refreshTasks: () => { void workbench.refetch(); },
  };
  const requests = useQuery({
    queryKey: requestKey(props.caseId),
    queryFn: ({ signal }) => listEvidenceRequests(props.caseId, signal),
    enabled: !!props.caseId,
    retry: false,
  });
  return (
    <section className="workflow-panel" aria-label="补充材料请求">
      <header className="workflow-panel-head">
        <div><h2>补充材料请求</h2><p className="meta">跟踪缺失检查、材料回执与后续审查核验。</p></div>
        <Button htmlType="button" loading={requests.isFetching} onClick={() => void client.invalidateQueries({ queryKey: requestKey(props.caseId) })}>刷新请求</Button>
      </header>
      <CreateRequest key={`${props.caseId}:${props.runId}:${props.attemptId ?? ""}`} {...props} context={context} />
      {requests.isLoading ? <p role="status">正在读取补充材料请求。</p> : null}
      <ErrorNotice error={requests.error} />
      {requests.isError ? <Button htmlType="button" onClick={() => void requests.refetch()}>重试读取请求</Button> : null}
      {requests.data?.length === 0 ? <p>当前案件没有补充材料请求。</p> : null}
      {requests.data && requests.data.length > 0 ? <ul className="workflow-record-list">{requests.data.map((item) =>
        <RequestCard key={`${props.caseId}:${item.id}`} item={item} {...props} context={context} />
      )}</ul> : null}
    </section>
  );
}
