import { Button, Checkbox, Input, Select, Table, type TableProps } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent, type RefObject } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { ApiError, errorMessage, isConflict, isForbidden, statusLabel } from "../api/http";
import {
  commandGovernedKnowledge, createGovernedKnowledge, getGovernedKnowledge, getKnowledgeHistory,
  listGovernedKnowledge, previewGovernedPolicy,
  type KnowledgeAction, type KnowledgeCommand, type KnowledgeContent, type KnowledgeScope, type KnowledgeSummary,
  type KnowledgeVersion, type PolicyPreview,
} from "../api/governed-knowledge";
import { KnowledgeEditor, emptyKnowledgeContent, validateKnowledgeDraft, type KnowledgeFormIssue } from "../components/governed-knowledge-editor";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import { LegalKnowledgeAdminList } from "./legal-knowledge-page";

const root = "/admin/governed-knowledge";
const queryRoot = ["admin", "governed-knowledge"] as const;
const labels: Record<string, string> = { policy: "政策制度", guidance: "操作指引", exception: "例外", experience: "审查经验", legal: "法律规范", draft: "草稿", submitted: "已提交", validated: "已校验", approved: "已批准", published: "已发布", withdrawn: "已撤回", create: "创建", submit: "提交", validate: "校验", approve: "批准", publish: "发布", withdraw: "撤回", rollback: "回滚" };
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

function matchesKnowledge(item: KnowledgeSummary, kind: string, search: string): boolean {
  return (kind === "all" || item.kind === kind) && `${item.title} ${item.ref.knowledge_id} ${item.version}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase());
}
function ScopeCell({ scope }: { scope: KnowledgeScope }) {
  return <>{scope.business_line} / {scope.scenario} / {scope.contract_type}{scope.org_id && <div className="meta">组织 {scope.org_id}{scope.case_id && ` · 案件 ${scope.case_id}`}</div>}</>;
}

export function GovernedKnowledgeListPage() {
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("all");
  const list = useQuery({ queryKey: [...queryRoot, "list"], queryFn: ({ signal }) => listGovernedKnowledge(signal), retry: false, enabled: kind !== "legal" });
  if (kind === "legal") return <LegalKnowledgeAdminList search={search} onSearch={setSearch} onSelectOtherKind={() => setKind("all")} />;
  if (list.isLoading) return <PageStatus kind="loading" title="正在读取知识版本" />;
  if (list.isError) return <PageStatus kind={isForbidden(list.error) ? "denied" : "error"} title="知识版本列表没有加载" action={<><ServerError error={list.error} /><Button htmlType="button" onClick={() => void list.refetch()}>重试</Button></>} />;
  const filtered = (list.data ?? []).filter((item) => matchesKnowledge(item, kind, search));
  const columns: TableProps<KnowledgeSummary>["columns"] = [
    { title: "知识 / 版本", key: "identity", render: (_, item) => <><Link to={detailPath(item.ref.knowledge_id, item.ref.version_id)}>{item.title}</Link><div className="meta">{item.ref.knowledge_id} · v{item.version}</div></> },
    { title: "类型", key: "kind", render: (_, item) => display(item.kind) },
    { title: "状态", key: "status", render: (_, item) => <ResultStatus status={item.status} active={item.active} /> },
    { title: "适用范围", key: "scope", render: (_, item) => <ScopeCell scope={item.scope} /> },
    { title: "有效期（不含结束）", key: "validity", render: (_, item) => <>{item.valid_from}<div className="meta">至 {item.valid_until}</div></> },
    { title: "修订", key: "revision", render: (_, item) => <>{item.revision}<div className="meta">指针 {item.pointer_revision}</div></> },
  ];
  return <section className="page page-rulepacks">
    <header className="page-head"><div><h1>知识治理</h1><p>管理政策、指引、例外和审查经验的精确版本及发布状态。列表只显示当前管理员可访问的版本。</p></div><Button type="primary" href={`${root}/new`}>创建知识草稿</Button></header>
    <div className="governance-filters">
      <label className="field" htmlFor="knowledge-search">查找知识<Input id="knowledge-search" type="search" value={search} placeholder="标题、知识 ID 或版本" onChange={(event) => setSearch(event.target.value)} /></label>
      <label className="field" htmlFor="knowledge-kind-filter">知识类型<Select id="knowledge-kind-filter" style={{ width: "100%" }} value={kind} onChange={setKind} options={[{ value: "all", label: "全部类型" }, { value: "policy", label: "政策制度" }, { value: "guidance", label: "操作指引" }, { value: "exception", label: "例外" }, { value: "experience", label: "审查经验" }, { value: "legal", label: "法律规范" }]} /></label>
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

function initialDraft(params: URLSearchParams): KnowledgeContent {
  const candidateLink = ["source_case", "candidate_id", "candidate_version_id", "candidate_content_sha256"].some((key) => params.has(key));
  if (!candidateLink) return emptyKnowledgeContent();
  return {
    ...emptyKnowledgeContent(),
    kind: "experience",
    source_type: "human_feedback",
    source_case_id: params.get("source_case") ?? "",
    candidate_id: params.get("candidate_id") ?? "",
    candidate_version_id: params.get("candidate_version_id") ?? "",
    candidate_content_sha256: params.get("candidate_content_sha256") ?? "",
  };
}

function sourceFailure(source: { isError: boolean; error: unknown }) {
  if (!source.isError) return null;
  return <PageStatus kind={isForbidden(source.error) ? "denied" : "error"} title="来源知识版本没有加载" action={<><ServerError error={source.error} /><Link to={root}>返回知识列表</Link></>} />;
}

function createPageStatus(
  fromId: string,
  fromVersion: string,
  source: { isLoading: boolean; isError: boolean; error: unknown },
  initialized: boolean,
) {
  if (Boolean(fromId) !== Boolean(fromVersion)) return <PageStatus kind="error" title="来源版本信息不完整" action={<Link to={root}>返回知识列表</Link>} />;
  if (fromId && source.isLoading) return <PageStatus kind="loading" title="正在读取来源版本" />;
  const failure = sourceFailure(source);
  if (fromId && failure) return failure;
  if (fromId && !initialized) return <PageStatus kind="loading" title="正在准备新版本" />;
  return null;
}

function CreateHeader({ fromId }: { fromId: string }) {
  return <header className="page-head"><div><h1>{fromId ? "编辑并创建新版本" : "创建知识草稿"}</h1><p>{fromId ? "原版本会保留。填写新版本号并审阅内容，保存后会创建一份独立草稿。" : "填写内容、适用范围、来源与规则依据。保存只创建草稿。"}</p></div></header>;
}

function ValidationSummary({ issues, error, summaryRef }: { issues: KnowledgeFormIssue[]; error: unknown; summaryRef: RefObject<HTMLDivElement | null> }) {
  return <div tabIndex={-1} ref={summaryRef} role="alert"><h2>请检查以下内容</h2>{issues.length > 0 && <ul>{issues.map((issue, index) => <li key={`${issue.id}-${index}`}><a href={`#${issue.id}`}>{issue.message}</a></li>)}</ul>}<ServerError error={error} /></div>;
}

function CreateActions({ fromId, fromVersion, pending }: { fromId: string; fromVersion: string; pending: boolean }) {
  return <div className="dialog-actions"><Button htmlType="button" href={fromId ? detailPath(fromId, fromVersion) : root}>取消</Button><Button type="primary" htmlType="submit" loading={pending}>创建草稿</Button></div>;
}

interface CreateMutation {
  mutateAsync: (input: { content: KnowledgeContent; key: string }) => Promise<KnowledgeVersion>;
}

async function submitCreate(
  event: FormEvent<HTMLFormElement>,
  draft: KnowledgeContent,
  keys: Map<string, string>,
  create: CreateMutation,
  client: ReturnType<typeof useQueryClient>,
  navigate: (path: string) => void,
  setIssues: (issues: KnowledgeFormIssue[]) => void,
  setError: (error: unknown) => void,
): Promise<void> {
  event.preventDefault();
  const nextIssues = validateKnowledgeDraft(draft);
  setIssues(nextIssues);
  setError(null);
  if (nextIssues.length) return;
  const content = contentForCreate(draft);
  const identity = JSON.stringify(content);
  let key = keys.get(identity);
  if (!key) { key = crypto.randomUUID(); keys.set(identity, key); }
  try {
    const created = await create.mutateAsync({ content, key });
    keys.delete(identity);
    await client.invalidateQueries({ queryKey: queryRoot });
    void navigate(detailPath(created.ref.knowledge_id, created.ref.version_id));
  } catch (problem) { setError(problem); }
}

function CreateNavigation({ fromId, fromVersion }: { fromId: string; fromVersion: string }) {
  return <nav className="page-back" aria-label="知识治理导航"><Link to={fromId ? detailPath(fromId, fromVersion) : root}>返回</Link></nav>;
}

function hasCreateSummary(issues: KnowledgeFormIssue[], error: unknown): boolean {
  return issues.length > 0 || error != null;
}

function useSourceDraft(
  source: { data: KnowledgeVersion | undefined },
  initialized: boolean,
  setDraft: (draft: KnowledgeContent) => void,
  setInitialized: (initialized: boolean) => void,
): void {
  useEffect(() => {
    if (!source.data || initialized) return;
    setDraft({ ...source.data.content, version: "" });
    setInitialized(true);
  }, [source.data, initialized, setDraft, setInitialized]);
}

function useSummaryFocus(summaryRef: RefObject<HTMLDivElement | null>, issues: KnowledgeFormIssue[], error: unknown): void {
  useEffect(() => { if (issues.length || error) summaryRef.current?.focus(); }, [issues, error, summaryRef]);
}

export function GovernedKnowledgeCreatePage() {
  const [params] = useSearchParams();
  const fromId = params.get("from_knowledge") ?? "";
  const fromVersion = params.get("from_version") ?? "";
  const source = useQuery({ queryKey: [...queryRoot, "detail", fromId, fromVersion], queryFn: ({ signal }) => getGovernedKnowledge(fromId, fromVersion, signal), enabled: Boolean(fromId && fromVersion), retry: false });
  const navigate = useNavigate();
  const client = useQueryClient();
  const [initialized, setInitialized] = useState(false);
  const [draft, setDraft] = useState<KnowledgeContent>(() => initialDraft(params));
  const [issues, setIssues] = useState<KnowledgeFormIssue[]>([]);
  const [error, setError] = useState<unknown>(null);
  const keys = useRef(new Map<string, string>());
  const summaryRef = useRef<HTMLDivElement>(null);
  const create = useMutation({ mutationFn: ({ content, key }: { content: KnowledgeContent; key: string }) => createGovernedKnowledge(content, key), retry: false });
  useSourceDraft(source, initialized, setDraft, setInitialized);
  useSummaryFocus(summaryRef, issues, error);
  const status = createPageStatus(fromId, fromVersion, source, initialized);
  if (status) return status;
  return <section className="page page-rulepack-form">
    <CreateNavigation fromId={fromId} fromVersion={fromVersion} />
    <CreateHeader fromId={fromId} />
    <form className="workflow-form rulepack-form" noValidate onSubmit={(event) => void submitCreate(event, draft, keys.current, create, client, navigate, setIssues, setError)}>
      {hasCreateSummary(issues, error) && <ValidationSummary issues={issues} error={error} summaryRef={summaryRef} />}
      <KnowledgeEditor value={draft} onChange={setDraft} issues={issues} lockedIdentity={Boolean(fromId)} disabled={create.isPending} />
      <CreateActions fromId={fromId} fromVersion={fromVersion} pending={create.isPending} />
    </form>
  </section>;
}

function ComparisonList({ comparisons }: { comparisons: PolicyPreview["comparisons"] }) {
  return <ul>{comparisons.map((item, index) => <li key={`${item.rule_id}-${index}`}><strong>{item.rule_id} · {comparisonLabels[item.status] ?? item.status}</strong><div>比较类型：{item.comparator}</div><div>政策原文：{item.candidate ? `${item.candidate.quote}（${item.candidate.value} ${item.candidate.unit}${item.candidate.basis ? `，${item.candidate.basis}` : ""}；位置 ${item.candidate.start}–${item.candidate.end}）` : "未提取到候选"}</div><div>规则阈值：{item.rule_threshold.value} {item.rule_threshold.unit}{item.rule_threshold.basis ? `，${item.rule_threshold.basis}` : ""}</div>{item.reason && <div>原因：{item.reason}</div>}</li>)}</ul>;
}
function PreviewIssues({ issues }: { issues: PolicyPreview["issues"] }) {
  return <ul>{issues.map((item, index) => <li key={index}>{item.reason}{item.quote && `：${item.quote}（${item.start}–${item.end}）`}</li>)}</ul>;
}
function SemanticNotes({ notes }: { notes: PolicyPreview["semantic_only"] }) {
  return <ul>{notes.map((item, index) => <li key={index}>{item.quote}（{item.start}–{item.end}）：{item.reason}</li>)}</ul>;
}
function Preview({ preview }: { preview: PolicyPreview }) {
  return <section className="rulepack-editor-section" aria-labelledby="knowledge-preview-heading">
    <h2 id="knowledge-preview-heading">政策与结构化规则差异</h2>
    <p>{preview.approvable ? "预览：规则关联和可以提取的数值已通过检查。" : "预览：规则关联或数值尚未通过检查。"} 这不代表内容已经人工审核或批准。</p>
    <p className="meta">提取器：{preview.extractor_ref}</p>
    {preview.comparisons.length === 0 ? <p>没有返回规则比较项。</p> : <ComparisonList comparisons={preview.comparisons} />}
    {preview.issues.length > 0 && <><h3>提取或绑定问题</h3><PreviewIssues issues={preview.issues} /></>}
    {preview.semantic_only.length > 0 && <><h3>仍需人工核对含义</h3><SemanticNotes notes={preview.semantic_only} /></>}
  </section>;
}
function PreviewSection({ kind, preview }: { kind: string; preview: { isLoading: boolean; isError: boolean; error: unknown; data: PolicyPreview | undefined; refetch: () => unknown } }) {
  if (kind !== "policy") return null;
  return <>{preview.isLoading && <p role="status">正在读取政策规则差异</p>}{preview.isError && <section className="rulepack-editor-section"><h2>政策规则差异没有加载</h2><ServerError error={preview.error} /><Button htmlType="button" onClick={() => void preview.refetch()}>重试</Button></section>}{preview.data && !preview.isError && <Preview preview={preview.data} />}</>;
}

const statusActions: Record<string, KnowledgeAction> = { draft: "submit", submitted: "validate", validated: "approve", approved: "publish" };
function actionFor(version: KnowledgeVersion): KnowledgeAction | null {
  const direct = statusActions[version.status];
  if (direct) return direct;
  if (version.status === "published") return version.active ? "withdraw" : "rollback";
  if (version.status === "withdrawn" && !version.active) return "rollback";
  return null;
}
type Replay = { command: KnowledgeCommand; key: string };

function DetailHeader({ knowledgeId, versionId, content, version }: { knowledgeId: string; versionId: string; content: KnowledgeContent; version: KnowledgeVersion }) {
  return <>
    <nav className="page-back" aria-label="知识治理导航"><Link to={root}>返回知识列表</Link></nav>
    <header className="page-head"><div><h1>{content.title}</h1><p>{display(content.kind)} · {content.knowledge_id} · v{content.version}</p><ResultStatus status={version.status} active={version.active} /></div>{content.kind !== "legal" && <Button href={`${root}/new?from_knowledge=${encodeURIComponent(knowledgeId)}&from_version=${encodeURIComponent(versionId)}`}>编辑并创建新版本</Button>}</header>
  </>;
}

function VersionIdentity({ version, content }: { version: KnowledgeVersion; content: KnowledgeContent }) {
  return <>
    <p>版本 ID：{version.ref.version_id}</p><p>内容 SHA-256：{version.ref.content_sha256}</p><p>内容修订：{version.revision}；生效指针修订：{version.pointer_revision}</p><p>创建人：{version.created_by}；创建时间：{version.created_at}</p>
    {version.approval_actor_id && <p>批准人：{version.approval_actor_id}；批准摘要：{version.approved_digest}；批准依据：{version.approval_reason}</p>}
    {content.kind === "experience" && <p>跨案审阅：{version.cross_case_reviewed ? "已记录" : "未记录"}</p>}
  </>;
}
function SourceLine({ content }: { content: KnowledgeContent }) {
  return <p>来源：{content.source_type} · {content.source_reference}{content.source_case_id && `；来源案件 ${content.source_case_id}`}{content.source_org_id && `；来源组织 ${content.source_org_id}`}{content.source_feedback_id && `；反馈 ${content.source_feedback_id}`}</p>;
}
function ScopeLine({ scope }: { scope: KnowledgeScope }) {
  return <p>范围：{scope.business_line} / {scope.scenario} / {scope.contract_type}{scope.org_id && `；组织 ${scope.org_id}`}{scope.case_id && `；案件 ${scope.case_id}`}</p>;
}
function ConditionLines({ content }: { content: KnowledgeContent }) {
  return <><p>适用条件：{content.applicability?.length ? content.applicability.map((item) => `${item.field} = ${item.equals}`).join("；") : "无"}</p><p>排除条件：{content.exclusions?.length ? content.exclusions.map((item) => `${item.field} = ${item.equals}`).join("；") : "无"}</p></>;
}
function ContentSummary({ content }: { content: KnowledgeContent }) {
  return <>
    <h3>摘要</h3><p style={{ whiteSpace: "pre-wrap" }}>{content.summary}</p><h3>正文</h3><p style={{ whiteSpace: "pre-wrap" }}>{content.body}</p>
    <h3>来源与范围</h3><SourceLine content={content} />
    <ScopeLine scope={content.scope} /><p>有效期：{content.valid_from} 至 {content.valid_until}（不含结束）</p>
    <ConditionLines content={content} />
    {content.kind === "legal" && content.legal_metadata && <LegalMetadataSection metadata={content.legal_metadata} />}
    {content.exception_basis && <p>例外依据：{content.exception_basis}</p>}
  </>;
}
function LegalMetadataSection({ metadata }: { metadata: NonNullable<KnowledgeContent["legal_metadata"]> }) {
  return <section aria-label="来源和效力证据"><h3>来源与效力证据</h3><dl><dt>法规范版本</dt><dd>{metadata.norm_title} · {metadata.norm_version} · {metadata.article_label}</dd><dt>制定机关</dt><dd>{metadata.issuer}</dd><dt>公布 / 施行</dt><dd>{metadata.promulgated_on} / {metadata.effective_from}{metadata.effective_until ? ` 至 ${metadata.effective_until}` : "起"}</dd><dt>效力观察</dt><dd>{metadata.official_status} · {metadata.status_observed_at}</dd><dt>来源捕获</dt><dd>{metadata.source_snapshot_id} · {metadata.source_captured_at}</dd><dt>来源摘要</dt><dd>{metadata.source_sha256}</dd><dt>条文摘要</dt><dd>{metadata.norm_text_sha256}</dd><dt>法律审阅</dt><dd>{metadata.legal_review_status} · {metadata.reviewer ?? "审阅人未记录"}</dd><dt>官方来源</dt><dd><a href={metadata.official_url} target="_blank" rel="noreferrer">{metadata.official_url}</a></dd></dl></section>;
}
function ExperienceReview({ content }: { content: KnowledgeContent }) {
  if (content.kind !== "experience") return null;
  return <><h3>经验审阅</h3><p>候选：{content.candidate_id} / {content.candidate_version_id}</p><p>候选摘要：{content.candidate_content_sha256}</p><p>审阅差异：{content.candidate_review_diff}</p><p>审阅适用条件：{content.reviewed_applicability?.join("；")}</p><p>审阅排除条件：{content.reviewed_exclusions?.join("；")}</p><p>检查动作：{content.check_action}</p><p>最低证据：{content.minimum_evidence}</p><p>反例：{content.counterexamples?.join("；")}</p></>;
}
function PolicyRefList({ content }: { content: KnowledgeContent }) {
  if (!content.policy_refs?.length) return null;
  return <><h3>政策依赖</h3><ul>{content.policy_refs.map((ref) => <li key={`${ref.knowledge_id}:${ref.version_id}`}><Link to={detailPath(ref.knowledge_id, ref.version_id)}>{ref.knowledge_id} / {ref.version_id}</Link>；摘要 {ref.content_sha256}</li>)}</ul></>;
}
function DetailBinding({ content }: { content: KnowledgeContent }) {
  return <>
    <h3>规则包与正文引用</h3><p>{content.binding.pack_id} · v{content.binding.pack_version}；内容摘要 {content.binding.pack_content_hash}；快照摘要 {content.binding.pack_snapshot_digest}</p><p>规则编译器：{content.binding.compiler_ref}{content.binding.extraction_compiler_ref && `；政策提取器：${content.binding.extraction_compiler_ref}`}</p><ul>{content.binding.source_spans?.map((span, index) => <li key={index}>{span.rule_id}：{span.quote}（{span.start}–{span.end}）</li>)}</ul><p>候选差异：{content.binding.candidate_diff}</p><p>验证依据：{content.binding.validation_evidence}</p>
  </>;
}
function VersionSection({ version, content }: { version: KnowledgeVersion; content: KnowledgeContent }) {
  return <section className="rulepack-editor-section" aria-labelledby="knowledge-version-heading">
    <h2 id="knowledge-version-heading">精确版本</h2>
    <VersionIdentity version={version} content={content} />
    <ContentSummary content={content} />
    <ExperienceReview content={content} />
    <PolicyRefList content={content} />
    {content.kind !== "legal" && <DetailBinding content={content} />}
  </section>;
}

function isReplayable(problem: unknown): boolean {
  return problem instanceof ApiError && (problem.retryable || problem.status === 0 || problem.status >= 500 || problem.code === "invalid_response");
}
function shouldRefetch(problem: unknown): boolean {
  return isConflict(problem) || (problem instanceof ApiError && [401, 403, 404].includes(problem.status));
}
function needsCrossCaseReview(action: KnowledgeAction | null, content: KnowledgeContent): boolean {
  return action === "approve" && content.kind === "experience" && content.scope.case_id !== content.source_case_id;
}
function actionSubmitError(action: KnowledgeAction | null, reason: string, crossCaseApproval: boolean, crossCaseReviewed: boolean): Error | null {
  if (!action || !reason.trim()) return new Error("请填写操作依据");
  if (crossCaseApproval && !crossCaseReviewed) return new Error("请先确认跨案审阅及敏感内容处理");
  return null;
}

interface ActionCommand {
  mutateAsync: (request: Replay) => Promise<KnowledgeVersion>;
  isPending: boolean;
}

async function runCommand(
  request: Replay,
  command: ActionCommand,
  client: ReturnType<typeof useQueryClient>,
  detailKey: readonly unknown[],
  detail: { refetch: () => unknown },
  setReplay: (replay: Replay | null) => void,
  setReason: (reason: string) => void,
  setCrossCaseReviewed: (reviewed: boolean) => void,
  setSuccess: (message: string) => void,
  setActionError: (error: unknown) => void,
): Promise<void> {
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
    setReplay(isReplayable(problem) ? request : null);
    if (shouldRefetch(problem)) void detail.refetch();
  }
}

function ActionStatus({ success, error, errorRef }: { success: string; error: unknown; errorRef: RefObject<HTMLDivElement | null> }) {
  return <>
    {success && <p role="status">{success}</p>}
    {error != null && <div tabIndex={-1} ref={errorRef}><ServerError error={error} />{isConflict(error) && <p>服务端记录已变化，请检查重新读取的版本后再操作。</p>}</div>}
  </>;
}
function ReplayNotice({ replay, pending, onRun }: { replay: Replay | null; pending: boolean; onRun: (replay: Replay) => void }) {
  if (!replay) return null;
  return <><p>上次请求的结果尚未确认。请重试，系统会使用原请求标识查询结果。</p><Button htmlType="button" loading={pending} onClick={() => onRun(replay)}>重试{display(replay.command.action)}</Button></>;
}
function ActionForm({ action, reason, setReason, crossCaseReviewed, setCrossCaseReviewed, crossCaseApproval, pending, fetching, onSubmit }: {
  action: KnowledgeAction; reason: string; setReason: (reason: string) => void; crossCaseReviewed: boolean; setCrossCaseReviewed: (reviewed: boolean) => void; crossCaseApproval: boolean; pending: boolean; fetching: boolean; onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  return <form onSubmit={onSubmit}>
    <label className="field" htmlFor="knowledge-action-reason">{display(action)}依据<Input.TextArea id="knowledge-action-reason" value={reason} rows={3} maxLength={2000} onChange={(event) => setReason(event.target.value)} /></label>
    {crossCaseApproval && <Checkbox checked={crossCaseReviewed} onChange={(event) => setCrossCaseReviewed(event.target.checked)}>我已审阅跨案适用性并去除来源案件敏感内容</Checkbox>}
    <div className="dialog-actions"><Button type="primary" htmlType="submit" loading={pending} disabled={fetching}>{display(action)}</Button></div>
  </form>;
}

function ActionSection({ version, action, detailKey, detail, command }: {
  version: KnowledgeVersion;
  action: KnowledgeAction | null;
  detailKey: readonly unknown[];
  detail: { isFetching: boolean; refetch: () => unknown };
  command: ActionCommand;
}) {
  const client = useQueryClient();
  const [reason, setReason] = useState("");
  const [crossCaseReviewed, setCrossCaseReviewed] = useState(false);
  const [actionError, setActionError] = useState<unknown>(null);
  const [success, setSuccess] = useState("");
  const [replay, setReplay] = useState<Replay | null>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => { if (actionError) errorRef.current?.focus(); }, [actionError]);
  const crossCaseApproval = needsCrossCaseReview(action, version.content);
  const run = (request: Replay) => runCommand(request, command, client, detailKey, detail, setReplay, setReason, setCrossCaseReviewed, setSuccess, setActionError);
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (detail.isFetching || command.isPending) return;
    const submitError = actionSubmitError(action, reason, crossCaseApproval, crossCaseReviewed);
    if (submitError) { setActionError(submitError); return; }
    void run({ key: crypto.randomUUID(), command: { action: action as KnowledgeAction, expected_revision: version.revision, expected_content_sha256: version.ref.content_sha256, expected_pointer_revision: version.pointer_revision, reason: reason.trim(), cross_case_reviewed: crossCaseReviewed } });
  }
  return <section className="rulepack-editor-section" aria-labelledby="knowledge-action-heading">
    <h2 id="knowledge-action-heading">治理操作</h2><p>操作基于当前内容摘要、修订和生效指针修订；权限、依赖和最终状态由服务端核验。</p>
    <ActionStatus success={success} error={actionError} errorRef={errorRef} />
    {replay ? <ReplayNotice replay={replay} pending={command.isPending} onRun={run} /> : action ? <ActionForm action={action} reason={reason} setReason={setReason} crossCaseReviewed={crossCaseReviewed} setCrossCaseReviewed={setCrossCaseReviewed} crossCaseApproval={crossCaseApproval} pending={command.isPending} fetching={detail.isFetching} onSubmit={submit} /> : <p>当前状态没有可执行的治理操作。</p>}
  </section>;
}

function pointerTransition(entry: NonNullable<Awaited<ReturnType<typeof getKnowledgeHistory>>>[number]): string {
  return `生效指针：${entry.previous_version_id || "无"}（${entry.previous_active ? "生效" : "未生效"}） → ${entry.resulting_version_id || "无"}（${entry.resulting_active ? "生效" : "未生效"}）`;
}
function HistoryEntryRow({ entry }: { entry: NonNullable<Awaited<ReturnType<typeof getKnowledgeHistory>>>[number] }) {
  return <li><strong>{display(entry.action)}</strong> · {entry.occurred_at} · {entry.actor_id}<div>版本 {entry.ref.version_id}；修订 {entry.revision}；指针修订 {entry.pointer_revision}</div>{entry.reason && <div>依据：{entry.reason}</div>}{(entry.previous_version_id || entry.resulting_version_id) && <div>{pointerTransition(entry)}</div>}</li>;
}
function HistorySection({ history }: { history: { isLoading: boolean; isError: boolean; error: unknown; data: NonNullable<Awaited<ReturnType<typeof getKnowledgeHistory>>> | undefined; refetch: () => unknown } }) {
  return <section className="rulepack-editor-section" aria-labelledby="knowledge-history-heading"><h2 id="knowledge-history-heading">治理历史</h2>
    {history.isLoading && <p role="status">正在读取历史</p>}{history.isError && <><ServerError error={history.error} /><Button htmlType="button" onClick={() => void history.refetch()}>重试历史</Button></>}
    {history.data && !history.isError && (history.data.length ? <ol>{history.data.map((entry) => <HistoryEntryRow key={entry.sequence} entry={entry} />)}</ol> : <p>没有返回历史记录。</p>)}
  </section>;
}

export function GovernedKnowledgeDetailPage() {
  const { knowledgeId = "", versionId = "" } = useParams();
  return <GovernedKnowledgeDetail key={JSON.stringify([knowledgeId, versionId])} />;
}
function detailPageStatus(detail: { isLoading: boolean; isError: boolean; error: unknown; data: KnowledgeVersion | undefined }) {
  if (detail.isLoading) return <PageStatus kind="loading" title="正在读取知识版本" />;
  if (detail.isError || !detail.data) return <PageStatus kind={isForbidden(detail.error) ? "denied" : "error"} title="知识版本没有加载" action={<>{detail.error && <ServerError error={detail.error} />}<Link to={root}>返回知识列表</Link></>} />;
  return null;
}

function usePolicyPreview(
  detail: { data: KnowledgeVersion | undefined },
  knowledgeId: string,
  versionId: string,
) {
  const previewDigest = detail.data?.ref.content_sha256 ?? "";
  return useQuery({ queryKey: [...queryRoot, "preview", knowledgeId, versionId, previewDigest], queryFn: async ({ signal }) => {
    const result = await previewGovernedPolicy(knowledgeId, versionId, signal);
    if (result.ref.content_sha256 !== previewDigest) throw new ApiError("政策预览与当前版本摘要不一致", 200, "invalid_response", false, null);
    return result;
  }, enabled: detail.data?.content.kind === "policy", retry: false });
}

function GovernedKnowledgeDetail() {
  const { knowledgeId = "", versionId = "" } = useParams();
  const detailKey = [...queryRoot, "detail", knowledgeId, versionId] as const;
  const detail = useQuery({ queryKey: detailKey, queryFn: ({ signal }) => getGovernedKnowledge(knowledgeId, versionId, signal), enabled: Boolean(knowledgeId && versionId), retry: false });
  const history = useQuery({ queryKey: [...queryRoot, "history", knowledgeId], queryFn: ({ signal }) => getKnowledgeHistory(knowledgeId, signal), enabled: Boolean(knowledgeId), retry: false });
  const preview = usePolicyPreview(detail, knowledgeId, versionId);
  const command = useMutation({ mutationFn: (request: Replay) => commandGovernedKnowledge(knowledgeId, versionId, request.command, request.key), retry: false });
  const status = detailPageStatus(detail);
  if (status) return status;
  const version = detail.data as KnowledgeVersion;
  const content = version.content;
  const action = actionFor(version);
  return <section className="page page-rulepack-detail">
    <DetailHeader knowledgeId={knowledgeId} versionId={versionId} content={content} version={version} />
    <VersionSection version={version} content={content} />
    <PreviewSection kind={content.kind} preview={preview} />
    <ActionSection version={version} action={action} detailKey={detailKey} detail={detail} command={command} />
    <HistorySection history={history} />
  </section>;
}
