import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Input, Select } from "antd";
import { useRef, useState, type FormEvent, type SyntheticEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import type { BusinessAgentReview } from "../api/business-agent-review";
import { getCase, getReview, getSession, getWorkbench } from "../api/endpoints";
import {
  createEvidenceReceipt,
  createEvidenceRequest,
  listEvidenceHistory,
  listEvidenceRequests,
  uploadEvidenceDocument,
  verifyEvidenceRequest,
  type CreateEvidenceRequestInput,
  type EvidenceQuote,
  type EvidenceReceipt,
  type EvidenceRequest,
} from "../api/evidence-workflow";
import { ApiError, errorMessage } from "../api/http";
import { queryKeys, useCreateWorkbenchTask, useStartReview } from "../api/queries";
import type { Actor, CaseDetail, ConstraintEvaluation, ReviewWorkspace, WorkbenchTask } from "../api/types";
import { formatDateTime, relationLabel, roleLabel } from "../domain/review";
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

function ErrorNotice({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <div className="workflow-error" role="alert">
      <p>{errorMessage(error)}</p>
      {error instanceof ApiError ? <p>服务响应：{error.status} · {error.code}{error.retryable ? " · 可重试" : ""}</p> : null}
      {error instanceof ApiError && error.details !== null ? <details><summary>服务错误详情</summary><pre>{JSON.stringify(error.details, null, 2)}</pre></details> : null}
    </div>
  );
}

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

  const selectedAttempt = context.workspace?.review_attempts?.attempt;
  const frozen = !!attemptId && selectedAttempt?.id === attemptId && selectedAttempt.run_id === runId &&
    selectedAttempt.history_kind === "frozen" && !!selectedAttempt.finished_at &&
    (selectedAttempt.state === "completed" || selectedAttempt.state === "partial");
  const evaluationCounts = new Map<string, number>();
  for (const evaluation of evaluations) evaluationCounts.set(evaluation.id, (evaluationCounts.get(evaluation.id) ?? 0) + 1);
  const missingEvaluations = evaluations.filter((entry) => entry.id && entry.domain && entry.rule_version &&
    (entry.relation === "insufficient" || entry.relation === "unknown") && evaluationCounts.get(entry.id) === 1);
  const reportedGaps = agentReview && agentReview.attempt_id === attemptId ? agentReview.gaps ?? [] : [];
  const gapCounts = new Map<string, number>();
  for (const gap of reportedGaps) gapCounts.set(gap.gap_id, (gapCounts.get(gap.gap_id) ?? 0) + 1);
  const gaps = reportedGaps.filter((gap) => gap.gap_id && gap.domain && gap.capability_id &&
    ["insufficient", "unknown", "unsupported"].includes(gap.status) && gapCounts.get(gap.gap_id) === 1);
  const options = [
    ...missingEvaluations.map((entry, index) => ({ value: `evaluation:${entry.id}`, label: `规则检查 ${index + 1}：${displayGap(entry.domain, entry.capability_id)}，${entry.reason || "原因未报告"}（${relationLabel(entry.relation)}）` })),
    ...gaps.map((gap, index) => ({ value: `gap:${gap.gap_id}`, label: `审查缺口 ${index + 1}：${displayGap(gap.domain, gap.capability_id)}` })),
  ];
  const validSource = options.some((option) => option.value === source);
  const dueValid = due !== "" && !Number.isNaN(new Date(due).getTime());
  const eligibleTasks = (context.tasks ?? []).filter((entry) => entry.case_id === caseId && entry.status !== "completed" && !!entry.due_at &&
    context.detail?.members.some((member) => member.id === entry.assignee_id));
  const existingTask = eligibleTasks.find((entry) => entry.id === existingTaskId);
  const taskReady = !!task || (taskMode === "existing" ? !!existingTask : !!title.trim() && !!assignee && dueValid);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canManage || !frozen || !attemptId || !validSource || !question.trim() || !taskReady || pending) return;
    setError(null);
    setCreated(false);
    setPending(true);
    try {
      let linkedTask = task ?? (taskMode === "existing" ? existingTask ?? null : null);
      if (!linkedTask) {
        linkedTask = await createTask.mutateAsync({
          title: title.trim(), kind: "review", assignee_id: assignee, due_at: new Date(due).toISOString(),
          status: "open", priority: "high", finding_id: null,
        });
      }
      setTask(linkedTask);
      const isGap = source.startsWith("gap:");
      const input: CreateEvidenceRequestInput = {
        task_id: linkedTask.id, task_version: linkedTask.version, source_run_id: runId, source_attempt_id: attemptId,
        ...(isGap ? { source_gap_id: source.slice(4) } : { source_evaluation_id: source.slice(11) }),
        question: question.trim(), ...(role ? { material_role: role } : {}),
        verification_condition: isGap ? "resolved" : condition,
      };
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

  return (
    <form className="workflow-form" onSubmit={(event) => void submit(event)}>
      <h3>发起补充材料请求</h3>
      <p className="meta">选择本次冻结审查中实际存在的缺失检查，并关联有责任人和期限的案件任务。</p>
      {!frozen ? <p className="callout">仅已完成或部分完成、且已冻结的审查尝试可以发起请求。</p> : null}
      {frozen && options.length === 0 ? <p>本次审查未报告符合条件的缺失检查。</p> : null}
      <label className="field">缺失检查
        <Select<string> aria-label="缺失检查" placeholder="选择缺失检查" value={source || undefined} onChange={setSource}
          options={options} disabled={!frozen || pending || options.length === 0} />
      </label>
      <label className="field">关联任务
        <Select<"new" | "existing"> aria-label="关联任务" value={taskMode} onChange={setTaskMode} disabled={!!task || pending}
          options={[{ value: "new", label: "创建新任务" }, { value: "existing", label: "选择已有任务" }]} />
      </label>
      {taskMode === "existing" ? <>
        <label className="field">选择已有任务
          <Select<string> aria-label="选择已有任务" placeholder="选择案件任务" value={existingTaskId || undefined}
            onChange={setExistingTaskId} disabled={!!task || pending || context.tasksLoading} options={eligibleTasks.map((entry) => ({
              value: entry.id, label: `${entry.title} · ${context.detail?.members.find((member) => member.id === entry.assignee_id)?.display_name ?? "责任人姓名未报告"} · ${formatDateTime(entry.due_at ?? "")}`,
            }))} />
        </label>
        {context.tasksLoading ? <p role="status">正在读取案件任务。</p> : !context.tasks && !context.tasksError ? <p>案件任务尚未载入。</p> : null}
        <Button htmlType="button" loading={context.tasksLoading} disabled={!!task || pending || context.tasksLoading} onClick={context.refreshTasks}>刷新案件任务</Button>
        <ErrorNotice error={context.tasksError} />
        {context.tasks && eligibleTasks.length === 0 ? <p>暂无包含责任人和期限的未完成案件任务。</p> : null}
      </> : <>
        <label className="field">任务名称
          <Input required maxLength={200} value={title} onChange={(event) => setTitle(event.target.value)} disabled={!!task || pending} />
        </label>
        <label className="field">责任人
          <Select<string> aria-label="责任人" placeholder="选择案件成员" value={assignee || undefined} onChange={setAssignee}
            options={(context.detail?.members ?? []).map((member) => ({ value: member.id, label: member.display_name }))}
            disabled={!!task || pending || !context.detail} />
        </label>
        <label className="field">完成期限（本地时间）
          <Input type="datetime-local" required value={due} onChange={(event) => setDue(event.target.value)} disabled={!!task || pending} />
        </label>
      </>}
      <ErrorNotice error={context.detailError} />
      <label className="field">需要补充的问题
        <Input.TextArea required maxLength={2000} value={question} onChange={(event) => setQuestion(event.target.value)} disabled={pending} />
      </label>
      <label className="field">要求的材料类型
        <Select<string> aria-label="要求的材料类型" value={role} onChange={setRole} disabled={pending}
          options={[{ value: "", label: "不限材料类型" }, ...materialRoles]} />
      </label>
      {source.startsWith("gap:") ? <p>核验条件：缺口已解决。</p> : (
        <label className="field">核验条件
          <Select<"compatible" | "conflict"> aria-label="核验条件" value={condition} onChange={setCondition} disabled={pending}
            options={[{ value: "compatible", label: "检查结果一致" }, { value: "conflict", label: "检查结果存在冲突" }]} />
        </label>
      )}
      {task ? <>
        <p className="callout">已关联任务“{task.title}”。请求未完成时可在此重试，任务不会再次创建。</p>
        <Button htmlType="button" disabled={pending} onClick={() => { setTask(null); setError(null); }}>更换关联任务</Button>
      </> : null}
      <ErrorNotice error={error} />
      {created ? <p role="status">补充材料请求已创建。</p> : null}
      <Button aria-label={task ? "重试创建补充请求" : taskMode === "existing" ? "发起补充请求" : "创建任务并发起请求"} type="primary" htmlType="submit" loading={pending} disabled={!frozen || !validSource || !question.trim() || !taskReady}>
        {task ? "重试创建补充请求" : taskMode === "existing" ? "发起补充请求" : "创建任务并发起请求"}
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
  const role = context.actor?.role;
  const archived = !context.detailReady || !context.detail || context.detail.archived_at !== null;
  const canSubmit = !archived && (role === "admin" || role === "reviewer" || role === "clerk");
  const canUpload = !archived && (role === "admin" || role === "clerk");
  const canVerify = !archived && (role === "admin" || role === "reviewer");
  const receipts = (history.data ?? []).flatMap((event) => event.receipt ? [event.receipt] : []);
  const receipt = receipts.find((entry) => entry.id === receiptId);
  const source = item.source_evaluation;
  const matchingEvaluations = source ? evaluations.filter((entry) => entry.domain === source.domain &&
    entry.rule_version === source.rule_version && entry.rule_id === source.rule_id && entry.capability_id === source.capability_id &&
    sameObjects(source.object_refs, entry.object_refs)) : [];
  const sourceGap = item.source_gap;
  const matchingGap = sourceGap && agentReview && agentReview.attempt_id === attemptId ?
    (agentReview.gaps ?? []).filter((gap) => gap.gap_id === sourceGap.gap_id) : [];
  const candidateGap = matchingGap.length === 1 ? matchingGap[0] : undefined;
  const gap = sourceGap && candidateGap && candidateGap.domain === sourceGap.domain && candidateGap.capability_id === sourceGap.capability_id ? candidateGap : undefined;
  const capabilities = gap ? (agentReview?.domains ?? []).filter((domain) => domain.domain === gap.domain)
    .flatMap((domain) => domain.capabilities ?? []).filter((capability) => capability.capability_id === gap.capability_id) : [];
  const capability = capabilities.length === 1 ? capabilities[0] : undefined;
  const gapResolved = !!gap && gap.status === "resolved" && !(agentReview?.unresolved_gap_ids ?? []).includes(gap.gap_id) &&
    capability?.status === "completed" && (capability.relation === "compatible" || capability.relation === "conflict");
  const observed = source ? matchingEvaluations.length === 1 && matchingEvaluations[0]?.relation === item.verification_condition : gapResolved;
  const attempt = context.workspace?.review_attempts?.attempt;
  const frozenLater = !!attemptId && attempt?.id === attemptId && attempt.run_id === runId && attempt.id !== item.source_attempt_id &&
    attempt.history_kind === "frozen" && !!attempt.finished_at && (attempt.state === "completed" || attempt.state === "partial");
  const frozenDocuments = (context.workspace?.documents ?? []).filter((document) => document.parsed_document_id && document.text &&
    (!item.material_role || document.role === item.material_role) &&
    (!receipt?.document_id || document.id === receipt.document_id) &&
    (!receipt?.answer || document.text.includes(receipt.answer)));
  const frozenDocument = frozenDocuments.find((document) => document.id === quoteDocumentId);
  const quoteValid = !!quote && !!frozenDocument && quote.document_id === frozenDocument.id &&
    quote.parsed_document_id === frozenDocument.parsed_document_id && Array.from(quote.quote).length <= 4096 &&
    (!receipt?.answer || quote.quote.includes(receipt.answer));
  const verifiedReady = canVerify && item.status === "received" && !!receipt && frozenLater && observed && quoteValid && !!reason.trim();
  const requiresBusinessAt = context.detail?.business_line === "ENT" &&
    (context.detail.scenario === "telecom_dedicated_line" || (context.detail.scenario === "cloud_services" && context.detail.contract_type === "service"));
  const validBusinessAt = businessAt !== "" && !Number.isNaN(new Date(businessAt).getTime());
  const availableDocuments = documents.filter((document) => !document.superseded && (!item.material_role || document.role === item.material_role));
  const documentOptions = availableDocuments.map((document) => ({ value: document.id, label: `${document.filename}（${roleLabel(document.role)}）` }));
  if (uploaded && !documentOptions.some((option) => option.value === uploaded.id)) {
    documentOptions.push({ value: uploaded.id, label: `${uploaded.filename}（${roleLabel(uploaded.role)}）` });
  }

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
    if (selectionEnd <= selectionStart) {
      setQuote(null);
      return;
    }
    const text = frozenDocument.text;
    setQuote({
      document_id: frozenDocument.id, parsed_document_id: frozenDocument.parsed_document_id,
      quote: text.slice(selectionStart, selectionEnd),
      start: Array.from(text.slice(0, selectionStart)).length,
      end: Array.from(text.slice(0, selectionEnd)).length,
    });
  }

  function submitVerification(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!verifiedReady || !attemptId || !quote) return;
    verify.mutate({
      run_id: runId, attempt_id: attemptId,
      ...(source ? { evaluation_id: matchingEvaluations[0]?.id } : { gap_id: gap?.gap_id }),
      evidence: [quote], reason: reason.trim(), expected_version: item.version,
    });
  }

  return (
    <li>
      <header className="workflow-panel-head">
        <div><h3>{item.question}</h3><p className="meta">责任人：{context.detail?.members.find((member) => member.id === item.assignee_id)?.display_name ?? "姓名未报告"}；期限：{formatDateTime(item.due_at)}</p></div>
        <StatusText tone={item.status === "verified" ? "good" : item.status === "received" ? "attention" : "neutral"}>{statusLabels[item.status] ?? item.status}</StatusText>
      </header>
      <p>要求材料：{item.material_role ? roleLabel(item.material_role) : "不限材料类型"}；核验条件：{conditionLabels[item.verification_condition] ?? item.verification_condition}</p>
      <p>来源：{source ? `规则检查，${displayGap(source.domain, source.capability_id)}，${source.reason || "原因未报告"}` : item.source_gap ? displayGap(item.source_gap.domain, item.source_gap.capability_id) : "未报告"}。 <Link to={`/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(item.source_run_id)}?attempt_id=${encodeURIComponent(item.source_attempt_id)}`}>查看来源审查</Link></p>
      {item.status === "open" ? <p>下一步：责任人或案件管理人提交材料或答复。</p> : null}
      {item.status === "received" ? <p>下一步：在提交回执后发起新审查，并结合该审查的实际检查和冻结原文进行核验。上传和提交本身不会完成核验。</p> : null}
      {item.status === "verified" ? <p>下一步：查看核验审查与处理记录。</p> : null}
      {item.status === "verified" && item.verification ? <div>
        <p>核验结果：{conditionLabels[item.verification.relation ?? item.verification.gap_status ?? ""] ?? item.verification.relation ?? item.verification.gap_status ?? "未报告"}；{item.verification.reason}</p>
        {item.verification.evidence.map((entry, index) => <blockquote key={`${entry.document_id}-${entry.start}-${index}`}>{entry.quote}</blockquote>)}
        <p><Link to={`/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(item.verification.run_id)}?attempt_id=${encodeURIComponent(item.verification.attempt_id)}`}>查看核验审查</Link> · {formatDateTime(item.verification.verified_at)}</p>
      </div> : null}
      {history.isLoading ? <p role="status">正在读取处理记录。</p> : null}
      <ErrorNotice error={history.error} />
      {history.isError ? <Button htmlType="button" onClick={() => void history.refetch()}>重试读取记录</Button> : null}
      {receipts.length > 0 ? <div>
        <p>已提交材料或答复：</p>
        <ul>{receipts.map((entry) => <li key={entry.id}>
          {entry.document_id ? documents.find((document) => document.id === entry.document_id)?.filename ?? "材料名称未报告" : entry.answer}
          {` · ${formatDateTime(entry.created_at)}`}
        </li>)}</ul>
      </div> : item.status !== "open" && history.isSuccess ? <p>处理记录中未报告回执。</p> : null}
      <details>
        <summary>处理记录</summary>
        {history.data ? <ol>{history.data.map((event) => <li key={`${event.request.version}-${event.type}`}>
          {eventLabels[event.type] ?? event.type} · {context.detail?.members.find((member) => member.id === event.actor_id)?.display_name ?? (event.actor_id === "system" ? "系统" : "姓名未报告")} · {formatDateTime(event.occurred_at)}
          {event.receipt?.document_id ? <p>提交材料：{documents.find((document) => document.id === event.receipt?.document_id)?.filename ?? "材料名称未报告"}</p> : null}
          {event.receipt?.answer ? <p>提交答复：{event.receipt.answer}</p> : null}
        </li>)}</ol> : null}
      </details>
      {(item.status === "open" || item.status === "received") && canSubmit ? <form className="workflow-form" onSubmit={(event) => {
        event.preventDefault();
        if (mode === "document" && documentId) submitReceipt.mutate({ document_id: documentId, expected_version: item.version });
        if (mode === "answer" && answer.trim()) submitReceipt.mutate({ answer: answer.trim(), expected_version: item.version });
      }}>
        <h4>提交材料或答复</h4>
        <p className="meta">由当前任务责任人或案件管理人提交；权限和请求版本由服务端核对。</p>
        <label className="field">提交方式
          <Select<"document" | "answer"> aria-label="提交方式" value={mode} onChange={setMode} options={[{ value: "document", label: "案件材料" }, { value: "answer", label: "文字答复" }]} />
        </label>
        {mode === "document" ? <>
          <label className="field">选择案件材料
            <Select<string> aria-label="选择案件材料" placeholder="选择材料" value={documentId || undefined} onChange={setDocumentId} options={documentOptions} />
          </label>
          {documentOptions.length === 0 ? <p>当前没有符合材料类型的案件材料。</p> : null}
          {canUpload ? <>
            {!item.material_role ? <label className="field">上传材料类型
              <Select<string> aria-label="上传材料类型" placeholder="选择材料类型" value={uploadRole || undefined} onChange={setUploadRole} options={materialRoles} />
            </label> : null}
            <label className="field">上传补充文件
              <input type="file" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
            </label>
            <p className="meta">同类型上传会形成新版本。上传后还需提交回执；单文件不超过 32 MiB。</p>
            <Button htmlType="button" disabled={!file || !uploadRole || upload.isPending} loading={upload.isPending} onClick={() => {
              if (file && uploadRole) upload.mutate({ selectedFile: file, selectedRole: uploadRole });
            }}>上传补充文件</Button>
            <ErrorNotice error={upload.error} />
            {uploaded ? <p role="status">已上传 {uploaded.filename}，请提交材料回执。</p> : null}
          </> : <p>如需上传新材料，请由有上传权限的案件成员登记后再选择。</p>}
        </> : <label className="field">文字答复
          <Input.TextArea maxLength={4000} value={answer} onChange={(event) => setAnswer(event.target.value)} />
        </label>}
        <ErrorNotice error={submitReceipt.error} />
        <Button type="primary" htmlType="submit" loading={submitReceipt.isPending} disabled={submitReceipt.isPending || (mode === "document" ? !documentId : !answer.trim())}>提交回执</Button>
      </form> : null}
      {item.status === "received" ? <>
        {canSubmit ? <div className="workflow-form">
          <h4>发起后续审查</h4>
          <p className="meta">审查必须在回执提交之后受理，才能作为该回执的核验依据。</p>
          {requiresBusinessAt ? <label className="field">审查业务时间（本地时间）
            <Input type="datetime-local" value={businessAt} onChange={(event) => setBusinessAt(event.target.value)} />
          </label> : null}
          <ErrorNotice error={start.error} />
          <Button htmlType="button" loading={start.isPending} disabled={start.isPending || (!!requiresBusinessAt && !validBusinessAt)} onClick={() => {
            start.mutate(requiresBusinessAt ? { business_at: new Date(businessAt).toISOString() } : {}, { onSuccess: (accepted) => {
              void navigate(`/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(accepted.id)}?attempt_id=${encodeURIComponent(accepted.attempt_id)}`);
            } });
          }}>发起后续审查</Button>
        </div> : null}
        <form className="workflow-form" onSubmit={submitVerification}>
          <h4>核验补充材料</h4>
          <p className="meta">在回执之后的审查工作区操作。选择实际回执及冻结原文，服务端会核对检查、材料关联和审查规则。</p>
          {!frozenLater ? <p className="callout">当前尝试不是可用于核验的后续冻结审查；请打开回执提交后的完成或部分完成审查。</p> : null}
          {frozenLater && !observed ? <p className="callout">当前审查没有唯一匹配且达到请求条件的实际检查，仍需补充或复审。</p> : null}
          {frozenLater && observed ? <p>当前审查的匹配检查：{source ? conditionLabels[matchingEvaluations[0]?.relation ?? ""] : gap ? `${displayGap(gap.domain, gap.capability_id)}，缺口已解决` : ""}。</p> : null}
          {history.isError ? <p>无法读取回执，不能核验。</p> : null}
          <label className="field">选择提交回执
            <Select<string> aria-label="选择提交回执" placeholder="选择回执" value={receiptId || undefined} onChange={(value) => { setReceiptId(value); setQuoteDocumentId(""); setQuote(null); }}
              options={receipts.map((entry: EvidenceReceipt) => ({ value: entry.id, label: `${entry.document_id ? (documents.find((document) => document.id === entry.document_id)?.filename ?? "提交材料") : `答复：${entry.answer ?? ""}`} · ${formatDateTime(entry.created_at)}` }))} />
          </label>
          {receipt && frozenDocuments.length === 0 ? <p>当前审查未包含可引用的回执材料或答复原文。</p> : null}
          <label className="field">选择冻结材料
            <Select<string> aria-label="选择冻结材料" placeholder="选择冻结材料" value={quoteDocumentId || undefined} onChange={(value) => { setQuoteDocumentId(value); setQuote(null); }}
              options={receipt ? frozenDocuments.map((document) => ({ value: document.id, label: `${document.filename}（${roleLabel(document.role)}）` })) : []} />
          </label>
          {frozenDocument?.text ? <label className="field">在冻结原文中选中引用内容
            <Input.TextArea aria-label="冻结原文" readOnly rows={8} value={frozenDocument.text} onSelect={selectQuote} />
            <span className="meta">用鼠标或键盘选择覆盖该检查引用内容的完整原文，最多 4096 个字符；过短的片段可能无法通过核验。</span>
          </label> : null}
          {quote ? <div><p>已选引用：</p><blockquote>{quote.quote}</blockquote>{!quoteValid ? <p className="callout">引用过长，或未包含所提交的文字答复。</p> : null}</div> : null}
          <label className="field">核验理由
            <Input.TextArea required maxLength={2000} value={reason} onChange={(event) => setReason(event.target.value)} disabled={!canVerify} />
          </label>
          {!canVerify ? <p>当前身份不能提交核验。</p> : null}
          <ErrorNotice error={verify.error} />
          <Button aria-label="提交核验" type="primary" htmlType="submit" loading={verify.isPending} disabled={!verifiedReady || verify.isPending}>提交核验</Button>
        </form>
      </> : null}
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
