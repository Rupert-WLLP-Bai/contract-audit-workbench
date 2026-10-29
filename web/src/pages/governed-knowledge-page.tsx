import { Button, Checkbox, Input, Select, Table, type TableProps } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { ApiError, errorMessage, isConflict, isForbidden, statusLabel } from "../api/http";
import {
  commandGovernedKnowledge, createGovernedKnowledge, getGovernedKnowledge, getKnowledgeHistory,
  listGovernedKnowledge, previewGovernedPolicy,
  type KnowledgeAction, type KnowledgeCommand, type KnowledgeContent, type KnowledgeSummary,
  type KnowledgeVersion, type PolicyPreview,
} from "../api/governed-knowledge";
import { KnowledgeEditor, emptyKnowledgeContent, validateKnowledgeDraft, type KnowledgeFormIssue } from "../components/governed-knowledge-editor";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";

const root = "/admin/governed-knowledge";
const queryRoot = ["admin", "governed-knowledge"] as const;
const labels: Record<string, string> = { policy: "政策制度", guidance: "操作指引", exception: "例外", experience: "审查经验", draft: "草稿", submitted: "已提交", validated: "已校验", approved: "已批准", published: "已发布", withdrawn: "已撤回", create: "创建", submit: "提交", validate: "校验", approve: "批准", publish: "发布", withdraw: "撤回", rollback: "回滚" };
const comparisonLabels: Record<string, string> = { match: "一致", different: "存在差异", ambiguous: "无法唯一确认", unexpressible: "无法表达" };
const detailPath = (knowledgeId: string, versionId: string) => `${root}/${encodeURIComponent(knowledgeId)}/versions/${encodeURIComponent(versionId)}`;
const display = (value: string) => labels[value] ?? value;

function ServerError({ error }: { error: unknown }) {
  if (!error) return null;
  return <div role="alert" className="workflow-error">
    <p>{error instanceof Error ? error.message : errorMessage(error)}</p>
    {error instanceof ApiError && <p>错误代码：{error.code}；{statusLabel(error.status)}{error.retryable ? "；服务允许重试" : ""}</p>}
    {error instanceof ApiError && error.details != null && <pre>{typeof error.details === "string" ? error.details : JSON.stringify(error.details, null, 2)}</pre>}
  </div>;
}
function ResultStatus({ status, active }: { status: string; active: boolean }) {
  return <div className="rulepack-badges"><StatusText tone={status === "published" ? "good" : status === "draft" ? "neutral" : "attention"}>{display(status)}</StatusText><StatusText tone={active ? "good" : "neutral"}>{active ? "当前生效" : "未生效"}</StatusText></div>;
}

export function GovernedKnowledgeListPage() {
  const list = useQuery({ queryKey: [...queryRoot, "list"], queryFn: ({ signal }) => listGovernedKnowledge(signal), retry: false });
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("all");
  if (list.isLoading) return <PageStatus kind="loading" title="正在读取知识版本" />;
  if (list.isError) return <PageStatus kind={isForbidden(list.error) ? "denied" : "error"} title="知识版本列表没有加载" action={<><ServerError error={list.error} /><Button htmlType="button" onClick={() => void list.refetch()}>重试</Button></>} />;
  const filtered = (list.data ?? []).filter((item) => (kind === "all" || item.kind === kind) && `${item.title} ${item.ref.knowledge_id} ${item.version}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()));
  const columns: TableProps<KnowledgeSummary>["columns"] = [
    { title: "知识 / 版本", key: "identity", render: (_, item) => <><Link to={detailPath(item.ref.knowledge_id, item.ref.version_id)}>{item.title}</Link><div className="meta">{item.ref.knowledge_id} · v{item.version}</div></> },
    { title: "类型", key: "kind", render: (_, item) => display(item.kind) },
    { title: "状态", key: "status", render: (_, item) => <ResultStatus status={item.status} active={item.active} /> },
    { title: "适用范围", key: "scope", render: (_, item) => <>{item.scope.business_line} / {item.scope.scenario} / {item.scope.contract_type}{item.scope.org_id && <div className="meta">组织 {item.scope.org_id}{item.scope.case_id && ` · 案件 ${item.scope.case_id}`}</div>}</> },
    { title: "有效期（不含结束）", key: "validity", render: (_, item) => <>{item.valid_from}<div className="meta">至 {item.valid_until}</div></> },
    { title: "修订", key: "revision", render: (_, item) => <>{item.revision}<div className="meta">指针 {item.pointer_revision}</div></> },
  ];
  return <section className="page page-rulepacks">
    <header className="page-head"><div><h1>知识治理</h1><p>管理政策、指引、例外和审查经验的精确版本及发布状态。列表只显示当前管理员可访问的版本。</p></div><Button type="primary" href={`${root}/new`}>创建知识草稿</Button></header>
    <div className="governance-filters">
      <label className="field" htmlFor="knowledge-search">查找知识<Input id="knowledge-search" type="search" value={search} placeholder="标题、知识 ID 或版本" onChange={(event) => setSearch(event.target.value)} /></label>
      <label className="field" htmlFor="knowledge-kind-filter">知识类型<Select id="knowledge-kind-filter" style={{ width: "100%" }} value={kind} onChange={setKind} options={[{ value: "all", label: "全部类型" }, { value: "policy", label: "政策制度" }, { value: "guidance", label: "操作指引" }, { value: "exception", label: "例外" }, { value: "experience", label: "审查经验" }]} /></label>
    </div>
    <Table rowKey={(item) => `${item.ref.knowledge_id}:${item.ref.version_id}`} dataSource={filtered} columns={columns} pagination={{ pageSize: 10 }} scroll={{ x: 950 }} locale={{ emptyText: "没有可显示的知识版本" }} />
  </section>;
}

function contentForCreate(value: KnowledgeContent): KnowledgeContent {
  const result: KnowledgeContent = {
    ...value, encoding: undefined, source_org_id: undefined, source_feedback_id: undefined,
    source_case_id: value.source_case_id || undefined,
    scope: { ...value.scope, org_id: value.scope.org_id || undefined, case_id: value.scope.case_id || undefined },
  };
  if (value.kind !== "experience") {
    result.candidate_id = undefined;
    result.candidate_version_id = undefined;
    result.candidate_content_sha256 = undefined;
    result.candidate_review_diff = undefined;
    result.reviewed_applicability = undefined;
    result.reviewed_exclusions = undefined;
    result.check_action = undefined;
    result.minimum_evidence = undefined;
    result.counterexamples = undefined;
  } else {
    result.reviewed_applicability = value.reviewed_applicability?.map((item) => item.trim()).filter(Boolean);
    result.reviewed_exclusions = value.reviewed_exclusions?.map((item) => item.trim()).filter(Boolean);
    result.counterexamples = value.counterexamples?.map((item) => item.trim()).filter(Boolean);
  }
  if (value.kind !== "exception") result.exception_basis = undefined;
  return result;
}

export function GovernedKnowledgeCreatePage() {
  const [params] = useSearchParams();
  const fromId = params.get("from_knowledge") ?? "";
  const fromVersion = params.get("from_version") ?? "";
  const source = useQuery({ queryKey: [...queryRoot, "detail", fromId, fromVersion], queryFn: ({ signal }) => getGovernedKnowledge(fromId, fromVersion, signal), enabled: Boolean(fromId && fromVersion), retry: false });
  const navigate = useNavigate();
  const client = useQueryClient();
  const [initialized, setInitialized] = useState(false);
  const [draft, setDraft] = useState<KnowledgeContent>(() => {
    const candidateLink = ["source_case", "candidate_id", "candidate_version_id", "candidate_content_sha256"].some((key) => params.has(key));
    return { ...emptyKnowledgeContent(), ...(candidateLink ? { kind: "experience" as const, source_type: "human_feedback", source_case_id: params.get("source_case") ?? "", candidate_id: params.get("candidate_id") ?? "", candidate_version_id: params.get("candidate_version_id") ?? "", candidate_content_sha256: params.get("candidate_content_sha256") ?? "" } : {}) };
  });
  const [issues, setIssues] = useState<KnowledgeFormIssue[]>([]);
  const [error, setError] = useState<unknown>(null);
  const keys = useRef(new Map<string, string>());
  const summaryRef = useRef<HTMLDivElement>(null);
  const create = useMutation({ mutationFn: ({ content, key }: { content: KnowledgeContent; key: string }) => createGovernedKnowledge(content, key), retry: false });
  useEffect(() => {
    if (!source.data || initialized) return;
    setDraft({ ...source.data.content, version: "" });
    setInitialized(true);
  }, [source.data, initialized]);
  useEffect(() => { if (issues.length || error) summaryRef.current?.focus(); }, [issues, error]);
  if (Boolean(fromId) !== Boolean(fromVersion)) return <PageStatus kind="error" title="来源版本信息不完整" action={<Link to={root}>返回知识列表</Link>} />;
  if (fromId && source.isLoading) return <PageStatus kind="loading" title="正在读取来源版本" />;
  if (fromId && source.isError) return <PageStatus kind={isForbidden(source.error) ? "denied" : "error"} title="来源知识版本没有加载" action={<><ServerError error={source.error} /><Link to={root}>返回知识列表</Link></>} />;
  if (fromId && !initialized) return <PageStatus kind="loading" title="正在准备新版本" />;
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextIssues = validateKnowledgeDraft(draft);
    setIssues(nextIssues);
    setError(null);
    if (nextIssues.length) return;
    const content = contentForCreate(draft);
    const identity = JSON.stringify(content);
    let key = keys.current.get(identity);
    if (!key) { key = crypto.randomUUID(); keys.current.set(identity, key); }
    try {
      const created = await create.mutateAsync({ content, key });
      keys.current.delete(identity);
      await client.invalidateQueries({ queryKey: queryRoot });
      void navigate(detailPath(created.ref.knowledge_id, created.ref.version_id));
    } catch (problem) { setError(problem); }
  }
  return <section className="page page-rulepack-form">
    <nav className="page-back" aria-label="知识治理导航"><Link to={fromId ? detailPath(fromId, fromVersion) : root}>返回</Link></nav>
    <header className="page-head"><div><h1>{fromId ? "编辑并创建新版本" : "创建知识草稿"}</h1><p>{fromId ? "原版本会保留。填写新版本号并审阅内容，保存后会创建一份独立草稿。" : "填写内容、适用范围、来源与规则依据。保存只创建草稿。"}</p></div></header>
    <form className="workflow-form rulepack-form" noValidate onSubmit={(event) => void submit(event)}>
      {(issues.length > 0 || error != null) && <div tabIndex={-1} ref={summaryRef} role="alert"><h2>请检查以下内容</h2>{issues.length > 0 && <ul>{issues.map((issue, index) => <li key={`${issue.id}-${index}`}><a href={`#${issue.id}`}>{issue.message}</a></li>)}</ul>}<ServerError error={error} /></div>}
      <KnowledgeEditor value={draft} onChange={setDraft} issues={issues} lockedIdentity={Boolean(fromId)} disabled={create.isPending} />
      <div className="dialog-actions"><Button htmlType="button" href={fromId ? detailPath(fromId, fromVersion) : root}>取消</Button><Button type="primary" htmlType="submit" loading={create.isPending}>创建草稿</Button></div>
    </form>
  </section>;
}

function Preview({ preview }: { preview: PolicyPreview }) {
  return <section className="rulepack-editor-section" aria-labelledby="knowledge-preview-heading">
    <h2 id="knowledge-preview-heading">政策与结构化规则差异</h2>
    <p>{preview.approvable ? "预览：规则关联和可以提取的数值已通过检查。" : "预览：规则关联或数值尚未通过检查。"} 这不代表内容已经人工审核或批准。</p>
    <p className="meta">提取器：{preview.extractor_ref}</p>
    {preview.comparisons.length === 0 ? <p>没有返回规则比较项。</p> : <ul>{preview.comparisons.map((item, index) => <li key={`${item.rule_id}-${index}`}><strong>{item.rule_id} · {comparisonLabels[item.status] ?? item.status}</strong><div>比较类型：{item.comparator}</div><div>政策原文：{item.candidate ? `${item.candidate.quote}（${item.candidate.value} ${item.candidate.unit}${item.candidate.basis ? `，${item.candidate.basis}` : ""}；位置 ${item.candidate.start}–${item.candidate.end}）` : "未提取到候选"}</div><div>规则阈值：{item.rule_threshold.value} {item.rule_threshold.unit}{item.rule_threshold.basis ? `，${item.rule_threshold.basis}` : ""}</div>{item.reason && <div>原因：{item.reason}</div>}</li>)}</ul>}
    {preview.issues.length > 0 && <><h3>提取或绑定问题</h3><ul>{preview.issues.map((item, index) => <li key={index}>{item.reason}{item.quote && `：${item.quote}（${item.start}–${item.end}）`}</li>)}</ul></>}
    {preview.semantic_only.length > 0 && <><h3>仍需人工核对含义</h3><ul>{preview.semantic_only.map((item, index) => <li key={index}>{item.quote}（{item.start}–{item.end}）：{item.reason}</li>)}</ul></>}
  </section>;
}
function actionFor(version: KnowledgeVersion): KnowledgeAction | null {
  if (version.status === "draft") return "submit";
  if (version.status === "submitted") return "validate";
  if (version.status === "validated") return "approve";
  if (version.status === "approved") return "publish";
  if (version.status === "published" && version.active) return "withdraw";
  if ((version.status === "published" || version.status === "withdrawn") && !version.active) return "rollback";
  return null;
}
type Replay = { command: KnowledgeCommand; key: string };

export function GovernedKnowledgeDetailPage() {
  const { knowledgeId = "", versionId = "" } = useParams();
  return <GovernedKnowledgeDetail key={JSON.stringify([knowledgeId, versionId])} />;
}
function GovernedKnowledgeDetail() {
  const { knowledgeId = "", versionId = "" } = useParams();
  const client = useQueryClient();
  const detailKey = [...queryRoot, "detail", knowledgeId, versionId] as const;
  const detail = useQuery({ queryKey: detailKey, queryFn: ({ signal }) => getGovernedKnowledge(knowledgeId, versionId, signal), enabled: Boolean(knowledgeId && versionId), retry: false });
  const history = useQuery({ queryKey: [...queryRoot, "history", knowledgeId], queryFn: ({ signal }) => getKnowledgeHistory(knowledgeId, signal), enabled: Boolean(knowledgeId), retry: false });
  const previewDigest = detail.data?.ref.content_sha256 ?? "";
  const preview = useQuery({ queryKey: [...queryRoot, "preview", knowledgeId, versionId, previewDigest], queryFn: async ({ signal }) => {
    const result = await previewGovernedPolicy(knowledgeId, versionId, signal);
    if (result.ref.content_sha256 !== previewDigest) throw new ApiError("政策预览与当前版本摘要不一致", 200, "invalid_response", false, null);
    return result;
  }, enabled: detail.data?.content.kind === "policy", retry: false });
  const [reason, setReason] = useState("");
  const [crossCaseReviewed, setCrossCaseReviewed] = useState(false);
  const [actionError, setActionError] = useState<unknown>(null);
  const [success, setSuccess] = useState("");
  const [replay, setReplay] = useState<Replay | null>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  const command = useMutation({ mutationFn: (request: Replay) => commandGovernedKnowledge(knowledgeId, versionId, request.command, request.key), retry: false });
  useEffect(() => { if (actionError) errorRef.current?.focus(); }, [actionError]);
  if (detail.isLoading) return <PageStatus kind="loading" title="正在读取知识版本" />;
  if (detail.isError || !detail.data) return <PageStatus kind={isForbidden(detail.error) ? "denied" : "error"} title="知识版本没有加载" action={<>{detail.error && <ServerError error={detail.error} />}<Link to={root}>返回知识列表</Link></>} />;
  const version = detail.data;
  const content = version.content;
  const action = actionFor(version);
  const crossCaseApproval = action === "approve" && content.kind === "experience" && content.scope.case_id !== content.source_case_id;
  async function run(request: Replay) {
    setActionError(null);
    setSuccess("");
    try {
      const updated = await command.mutateAsync(request);
      client.setQueryData(detailKey, updated);
      setReplay(null);
      setReason("");
      setCrossCaseReviewed(false);
      setSuccess(`${display(request.command.action)}操作已由服务端记录。`);
      await client.invalidateQueries({ queryKey: queryRoot });
    } catch (problem) {
      setActionError(problem);
      if (problem instanceof ApiError && (problem.retryable || problem.status === 0 || problem.status >= 500 || problem.code === "invalid_response")) setReplay(request);
      else setReplay(null);
      if (isConflict(problem) || (problem instanceof ApiError && [401, 403, 404].includes(problem.status))) void detail.refetch();
    }
  }
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (detail.isFetching || command.isPending) return;
    if (!action || !reason.trim()) { setActionError(new Error("请填写操作依据")); return; }
    if (crossCaseApproval && !crossCaseReviewed) { setActionError(new Error("请先确认跨案审阅及敏感内容处理")); return; }
    void run({ key: crypto.randomUUID(), command: { action, expected_revision: version.revision, expected_content_sha256: version.ref.content_sha256, expected_pointer_revision: version.pointer_revision, reason: reason.trim(), cross_case_reviewed: crossCaseReviewed } });
  }
  return <section className="page page-rulepack-detail">
    <nav className="page-back" aria-label="知识治理导航"><Link to={root}>返回知识列表</Link></nav>
    <header className="page-head"><div><h1>{content.title}</h1><p>{display(content.kind)} · {content.knowledge_id} · v{content.version}</p><ResultStatus status={version.status} active={version.active} /></div><Button href={`${root}/new?from_knowledge=${encodeURIComponent(knowledgeId)}&from_version=${encodeURIComponent(versionId)}`}>编辑并创建新版本</Button></header>
    <section className="rulepack-editor-section" aria-labelledby="knowledge-version-heading">
      <h2 id="knowledge-version-heading">精确版本</h2>
      <p>版本 ID：{version.ref.version_id}</p><p>内容 SHA-256：{version.ref.content_sha256}</p><p>内容修订：{version.revision}；生效指针修订：{version.pointer_revision}</p><p>创建人：{version.created_by}；创建时间：{version.created_at}</p>
      {version.approval_actor_id && <p>批准人：{version.approval_actor_id}；批准摘要：{version.approved_digest}；批准依据：{version.approval_reason}</p>}
      {content.kind === "experience" && <p>跨案审阅：{version.cross_case_reviewed ? "已记录" : "未记录"}</p>}
      <h3>摘要</h3><p style={{ whiteSpace: "pre-wrap" }}>{content.summary}</p><h3>正文</h3><p style={{ whiteSpace: "pre-wrap" }}>{content.body}</p>
      <h3>来源与范围</h3><p>来源：{content.source_type} · {content.source_reference}{content.source_case_id && `；来源案件 ${content.source_case_id}`}{content.source_org_id && `；来源组织 ${content.source_org_id}`}{content.source_feedback_id && `；反馈 ${content.source_feedback_id}`}</p>
      <p>范围：{content.scope.business_line} / {content.scope.scenario} / {content.scope.contract_type}{content.scope.org_id && `；组织 ${content.scope.org_id}`}{content.scope.case_id && `；案件 ${content.scope.case_id}`}</p><p>有效期：{content.valid_from} 至 {content.valid_until}（不含结束）</p>
      <p>适用条件：{content.applicability?.length ? content.applicability.map((item) => `${item.field} = ${item.equals}`).join("；") : "无"}</p><p>排除条件：{content.exclusions?.length ? content.exclusions.map((item) => `${item.field} = ${item.equals}`).join("；") : "无"}</p>
      {content.exception_basis && <p>例外依据：{content.exception_basis}</p>}
      {content.kind === "experience" && <><h3>经验审阅</h3><p>候选：{content.candidate_id} / {content.candidate_version_id}</p><p>候选摘要：{content.candidate_content_sha256}</p><p>审阅差异：{content.candidate_review_diff}</p><p>审阅适用条件：{content.reviewed_applicability?.join("；")}</p><p>审阅排除条件：{content.reviewed_exclusions?.join("；")}</p><p>检查动作：{content.check_action}</p><p>最低证据：{content.minimum_evidence}</p><p>反例：{content.counterexamples?.join("；")}</p></>}
      {Boolean(content.policy_refs?.length) && <><h3>政策依赖</h3><ul>{content.policy_refs?.map((ref) => <li key={`${ref.knowledge_id}:${ref.version_id}`}><Link to={detailPath(ref.knowledge_id, ref.version_id)}>{ref.knowledge_id} / {ref.version_id}</Link>；摘要 {ref.content_sha256}</li>)}</ul></>}
      <h3>规则包与正文引用</h3><p>{content.binding.pack_id} · v{content.binding.pack_version}；内容摘要 {content.binding.pack_content_hash}；快照摘要 {content.binding.pack_snapshot_digest}</p><p>规则编译器：{content.binding.compiler_ref}{content.binding.extraction_compiler_ref && `；政策提取器：${content.binding.extraction_compiler_ref}`}</p><ul>{content.binding.source_spans?.map((span, index) => <li key={index}>{span.rule_id}：{span.quote}（{span.start}–{span.end}）</li>)}</ul><p>候选差异：{content.binding.candidate_diff}</p><p>验证依据：{content.binding.validation_evidence}</p>
    </section>
    {content.kind === "policy" && <>{preview.isLoading && <p role="status">正在读取政策规则差异</p>}{preview.isError && <section className="rulepack-editor-section"><h2>政策规则差异没有加载</h2><ServerError error={preview.error} /><Button htmlType="button" onClick={() => void preview.refetch()}>重试</Button></section>}{preview.data && !preview.isError && <Preview preview={preview.data} />}</>}
    <section className="rulepack-editor-section" aria-labelledby="knowledge-action-heading">
      <h2 id="knowledge-action-heading">治理操作</h2><p>操作基于当前内容摘要、修订和生效指针修订；权限、依赖和最终状态由服务端核验。</p>
      {success && <p role="status">{success}</p>}
      {actionError != null && <div tabIndex={-1} ref={errorRef}><ServerError error={actionError} />{isConflict(actionError) && <p>服务端记录已变化，请检查重新读取的版本后再操作。</p>}</div>}
      {replay ? <><p>上次请求的结果尚未确认。请重试，系统会使用原请求标识查询结果。</p><Button htmlType="button" loading={command.isPending} onClick={() => void run(replay)}>重试{display(replay.command.action)}</Button></> : action ? <form onSubmit={submit}>
        <label className="field" htmlFor="knowledge-action-reason">{display(action)}依据<Input.TextArea id="knowledge-action-reason" value={reason} rows={3} maxLength={2000} onChange={(event) => setReason(event.target.value)} /></label>
        {crossCaseApproval && <Checkbox checked={crossCaseReviewed} onChange={(event) => setCrossCaseReviewed(event.target.checked)}>我已审阅跨案适用性并去除来源案件敏感内容</Checkbox>}
        <div className="dialog-actions"><Button type="primary" htmlType="submit" loading={command.isPending} disabled={detail.isFetching}>{display(action)}</Button></div>
      </form> : <p>当前状态没有可执行的治理操作。</p>}
    </section>
    <section className="rulepack-editor-section" aria-labelledby="knowledge-history-heading"><h2 id="knowledge-history-heading">治理历史</h2>
      {history.isLoading && <p role="status">正在读取历史</p>}{history.isError && <><ServerError error={history.error} /><Button htmlType="button" onClick={() => void history.refetch()}>重试历史</Button></>}
      {history.data && !history.isError && (history.data.length ? <ol>{history.data.map((entry) => <li key={entry.sequence}><strong>{display(entry.action)}</strong> · {entry.occurred_at} · {entry.actor_id}<div>版本 {entry.ref.version_id}；修订 {entry.revision}；指针修订 {entry.pointer_revision}</div>{entry.reason && <div>依据：{entry.reason}</div>}{(entry.previous_version_id || entry.resulting_version_id) && <div>生效指针：{entry.previous_version_id || "无"}（{entry.previous_active ? "生效" : "未生效"}） → {entry.resulting_version_id || "无"}（{entry.resulting_active ? "生效" : "未生效"}）</div>}</li>)}</ol> : <p>没有返回历史记录。</p>)}
    </section>
  </section>;
}
