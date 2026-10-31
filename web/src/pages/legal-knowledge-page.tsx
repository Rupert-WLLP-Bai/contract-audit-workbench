import { Alert, Button, Input, Table, type TableProps } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { errorMessage, isForbidden } from "../api/http";
import { getCompanyPolicySourceStatus, getLegalKnowledgeForCase, getLegalSourceCaptureForCase, importLegalSource, searchAdminLegalKnowledge, searchLegalKnowledgeForCase, type LegalHit, type LegalImportInput, type LegalSourceCapture } from "../api/legal-knowledge";
import { useCases, useSession } from "../api/queries";
import type { KnowledgeVersion } from "../api/governed-knowledge";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import { PageStatus } from "../components/page-status";
import { beijingInputToUtc, toBeijingInputValue } from "../components/beijing-datetime";
import { formatDateTime } from "../domain/review";

const detailPath = (knowledgeId: string, versionId: string) => `/admin/governed-knowledge/${encodeURIComponent(knowledgeId)}/versions/${encodeURIComponent(versionId)}`;

export function LegalKnowledgePage() {
  const { caseId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const cases = useCases();
  const committedQuery = params.get("q") ?? "";
  const committedArticle = params.get("article_number") ?? "";
  const committedBusinessAt = params.get("business_at") ?? "";
  const cursor = params.get("cursor") ?? undefined;
  const [query, setQuery] = useState(committedQuery);
  const [article, setArticle] = useState(committedArticle);
  const [businessAt, setBusinessAt] = useState(toBeijingInputValue(committedBusinessAt));
  const [businessTimeError, setBusinessTimeError] = useState("");
  useEffect(() => {
    setQuery(committedQuery);
    setArticle(committedArticle);
    setBusinessAt(toBeijingInputValue(committedBusinessAt));
    setBusinessTimeError("");
  }, [caseId, committedQuery, committedArticle, committedBusinessAt]);
  const search = useQuery({
    queryKey: ["cases", caseId, "legal-knowledge", committedQuery, committedArticle, committedBusinessAt, cursor],
    queryFn: ({ signal }) => searchLegalKnowledgeForCase(caseId, { q: committedQuery, article_number: committedArticle ? Number(committedArticle) : undefined, business_at: committedBusinessAt || undefined, cursor }, signal),
    enabled: Boolean(caseId) && (committedQuery.trim() !== "" || committedArticle !== ""), retry: false,
  });
  const company = useQuery({ queryKey: ["cases", caseId, "company-policy-sources"], queryFn: ({ signal }) => getCompanyPolicySourceStatus(caseId, signal), enabled: Boolean(caseId), retry: false });
  const selected = params.get("knowledge_id") && params.get("version_id") ? { knowledgeId: params.get("knowledge_id")!, versionId: params.get("version_id")! } : null;
  const detail = useQuery({ queryKey: ["cases", caseId, "legal-knowledge-detail", selected?.knowledgeId, selected?.versionId], queryFn: ({ signal }) => getLegalKnowledgeForCase(caseId, selected!.knowledgeId, selected!.versionId, signal), enabled: Boolean(selected), retry: false });
  const captureId = detail.data?.content.legal_metadata?.source_snapshot_id ?? "";
  const capture = useQuery({ queryKey: ["cases", caseId, "legal-source-capture", captureId], queryFn: ({ signal }) => getLegalSourceCaptureForCase(caseId, captureId, signal), enabled: Boolean(caseId && captureId), retry: false });
  const columns: TableProps<LegalHit>["columns"] = useMemo(() => [
    { title: "法规与条号", key: "article", render: (_, item) => <><strong>{item.legal_metadata.norm_title}</strong><div>{item.legal_metadata.article_label}</div><div className="meta">{item.legal_metadata.norm_version} · {item.ref.version_id}</div></> },
    { title: "效力与治理", key: "status", render: (_, item) => <>{item.legal_metadata.official_status} · {item.governance_status}<div className="meta">法律审阅：{item.legal_metadata.legal_review_status}</div></> },
    { title: "生效时间", key: "effective", render: (_, item) => <>{item.legal_metadata.effective_from}{item.legal_metadata.effective_until ? ` 至 ${item.legal_metadata.effective_until}` : "起"}</> },
    { title: "命中内容", key: "snippet", render: (_, item) => <>{item.snippet || item.summary}</> },
    { title: "查看", key: "open", render: (_, item) => <Button htmlType="button" onClick={() => { const next = new URLSearchParams(params); next.set("knowledge_id",item.ref.knowledge_id); next.set("version_id",item.ref.version_id); setParams(next); }}>查看精确版本</Button> },
  ], [params,setParams]);
  if (!caseId) {
    if (cases.isLoading) return <PageStatus kind="loading" title="正在读取可访问案件" />;
    if (cases.isError) return <PageStatus kind={isForbidden(cases.error) ? "denied" : "error"} title="案件列表没有加载" action={<Button onClick={() => void cases.refetch()}>重试</Button>} />;
    return <section className="page"><header className="page-head"><div><h1>法律知识</h1><p>先选择有权访问的案件，再按条文名称、条号或正文检索。</p></div></header><ul className="document-list">{(cases.data ?? []).map((item) => <li key={item.id}><Link to={`/cases/${encodeURIComponent(item.id)}/legal-knowledge`}>{item.title}</Link></li>)}</ul></section>;
  }
  if (cases.isSuccess && !cases.data?.some((item) => item.id === caseId)) return <PageStatus kind="denied" title="当前案件不可用" />;
  if (company.isError && isForbidden(company.error)) return <PageStatus kind="denied" title="没有权限读取本案资料" />;
  return <section className="page">
    <nav className="page-back" aria-label="案件导航"><Link to={`/cases/${encodeURIComponent(caseId)}`}>返回案件</Link></nav>
    <header className="page-head"><div><h1>法律知识</h1><p>检索结果来自本案授权范围；条文是否适用仍须结合业务时间和具体条件判断。</p></div></header>
    <form className="governance-filters" onSubmit={(event) => { event.preventDefault(); const utc = businessAt ? beijingInputToUtc(businessAt) : ""; if (businessAt && !utc) { setBusinessTimeError("请输入有效的北京时间。"); return; } setBusinessTimeError(""); const next = new URLSearchParams(); if (query.trim()) next.set("q",query.trim()); if (article) next.set("article_number",article); if (utc) next.set("business_at",utc); setParams(next); }}>
      <label className="field">名称或正文<Input aria-label="名称或正文" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入法规名称或正文关键词" /></label>
      <label className="field">条号<Input aria-label="条号" inputMode="numeric" value={article} onChange={(event) => setArticle(event.target.value.replace(/[^0-9]/g,""))} placeholder="例如 1260" /></label>
      <label className="field">业务时间（北京时间）<Input aria-label="业务时间（北京时间）" type="datetime-local" value={businessAt} onChange={(event) => setBusinessAt(event.target.value)} /></label>
      <Button type="primary" htmlType="submit" disabled={!query.trim() && !article}>检索</Button>
    </form>
    {businessTimeError ? <Alert type="error" title={businessTimeError} /> : null}
    {search.isError ? <Alert type="error" showIcon title="法律知识检索失败" description={errorMessage(search.error)} action={<Button onClick={() => void search.refetch()}>重试</Button>} /> : null}
    {search.isLoading ? <PageStatus kind="loading" title="正在检索法律知识" /> : null}
    {search.isSuccess ? <><p className="meta">检索到 {search.data.items.length} 条当前授权范围内的记录。</p><Table rowKey={(item) => `${item.ref.knowledge_id}:${item.ref.version_id}`} columns={columns} dataSource={search.data.items} pagination={false} locale={{ emptyText: "没有匹配的法律条文" }} />{search.data.next_cursor ? <Button htmlType="button" onClick={() => { const next = new URLSearchParams(params); next.set("cursor", search.data.next_cursor!); setParams(next); }}>下一页</Button> : null}</> : null}
    {company.isError && !isForbidden(company.error) ? <Alert type="error" title="公司制度来源状态暂不可用" description={errorMessage(company.error)} action={<Button onClick={() => void company.refetch()}>重试</Button>} /> : null}
    {company.isSuccess ? <section className="rulepack-editor-section"><h2>公司制度资料</h2>{company.data.items.map((source) => <Alert key={source.catalog_key} type="info" showIcon title={source.display_label} description={source.can_be_cited ? "存在已发布资料，可按案件授权查看。" : "资料待提供；当前不可引用，也未满足必需依赖。"} />)}</section> : null}
    {selected ? <section className="rulepack-editor-section" aria-label="法律条文精确版本"><h2>法律条文精确版本</h2>{detail.isLoading ? <p>正在读取精确版本…</p> : null}{detail.isError ? <Alert type="error" title="精确版本没有加载" description={errorMessage(detail.error)} action={<Button onClick={() => void detail.refetch()}>重试</Button>} /> : null}{detail.isSuccess ? <LegalVersionDetail value={detail.data} capture={capture.data} captureLoading={capture.isLoading} captureError={capture.isError ? errorMessage(capture.error) : ""} onRetryCapture={() => void capture.refetch()} /> : null}<Button htmlType="button" onClick={() => { const next = new URLSearchParams(params); next.delete("knowledge_id"); next.delete("version_id"); setParams(next); }}>关闭</Button></section> : null}
  </section>;
}

function isImportInput(value: unknown): value is LegalImportInput {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const item = value as Record<string, unknown>;
  const capture = item.capture;
  return typeof capture === "object" && capture !== null && !Array.isArray(capture) && Array.isArray(item.articles);
}

export function LegalKnowledgeAdminList({ search, onSearch, onSelectOtherKind }: { search: string; onSearch: (value: string) => void; onSelectOtherKind: () => void }) {
  const [params, setParams] = useSearchParams();
  const committedSearch = params.get("q") ?? "";
  const cursor = params.get("cursor") ?? undefined;
  const [searchDraft, setSearchDraft] = useState(committedSearch || search);
  useEffect(() => setSearchDraft(committedSearch || search), [committedSearch, search]);
  const query = useQuery({ queryKey: ["admin","governed-knowledge","legal",committedSearch,cursor], queryFn: ({signal}) => searchAdminLegalKnowledge({q:committedSearch || undefined,cursor},signal), retry:false });
  const [raw, setRaw] = useState("");
  const [localError, setLocalError] = useState("");
  const [success, setSuccess] = useState<Awaited<ReturnType<typeof importLegalSource>> | null>(null);
  const actor = useSession("admin");
  const client = useQueryClient();
  const command = useMutation({ mutationFn: ({input,key}:{input:LegalImportInput;key:string}) => importLegalSource(input,key), retry:false });
  const columns: TableProps<LegalHit>["columns"] = [
    { title:"法规条文",key:"title",render:(_,item)=><><Link to={detailPath(item.ref.knowledge_id,item.ref.version_id)}>{item.legal_metadata.norm_title} · {item.legal_metadata.article_label}</Link><div className="meta">{item.legal_metadata.norm_version} · {item.ref.knowledge_id}</div></> },
    { title:"来源与效力",key:"source",render:(_,item)=><>{item.legal_metadata.issuer}<div className="meta">{item.legal_metadata.official_status} · {item.governance_status}</div></> },
    { title:"施行 / 捕获",key:"time",render:(_,item)=><>{item.legal_metadata.effective_from}<div className="meta">捕获于 {item.legal_metadata.source_captured_at}</div></> },
    { title:"法律审阅",key:"review",render:(_,item)=><>{item.legal_metadata.legal_review_status} · {item.legal_metadata.reviewer ?? "审阅人未记录"}</> },
  ];
  async function submit(event: FormEvent) {
    event.preventDefault(); setLocalError(""); setSuccess(null);
    let decoded: unknown;
    try { decoded = JSON.parse(raw); } catch { setLocalError("请输入有效的 JSON 导入对象。"); return; }
    if (!isImportInput(decoded)) { setLocalError("导入对象必须包含 capture 和 articles；捕获字节及状态证据使用原始 base64，不能手工改写哈希。"); return; }
    if (!actor.data) { setLocalError("管理身份暂不可用，请刷新后重试。"); return; }
    try {
      const pending = await pendingMutationKey("legal-source-import", idempotencyActorScope(actor.data), "legal-source-capture", decoded);
      const result = await command.mutateAsync({input:decoded,key:pending.key});
      clearPendingMutationKey(pending.storageKey);
      setSuccess(result); setRaw("");
      await Promise.all([client.invalidateQueries({queryKey:["admin","governed-knowledge","legal"]}),client.invalidateQueries({queryKey:["admin","governed-knowledge","list"]})]);
    } catch (error) { setLocalError(error instanceof Error ? error.message : "导入失败；可使用相同内容重试。"); }
  }
  function submitSearch(event: FormEvent) {
    event.preventDefault();
    const next = new URLSearchParams(params);
    const value = searchDraft.trim();
    if (value) next.set("q", value); else next.delete("q");
    next.delete("cursor");
    setParams(next);
    onSearch(value);
  }
  return <section className="page page-rulepacks">
    <header className="page-head"><div><h1>知识治理 · 法律规范</h1><p>法律条文使用已捕获的官方来源和效力证据，并沿用知识治理的提交、校验、批准与发布流程。法律审阅状态只显示当前登记值。</p></div><Button onClick={onSelectOtherKind}>返回其他知识类型</Button></header>
    <section className="rulepack-editor-section"><h2>导入官方来源捕获</h2><p>粘贴带有 capture 与 articles 的原始 JSON。服务端会核对来源正文、状态证据和各自摘要；法律审阅状态保持 NOT_RUN。</p><form className="workflow-form" onSubmit={(event)=>void submit(event)}><label className="field">官方捕获 JSON<Input.TextArea aria-label="官方捕获 JSON" rows={10} value={raw} onChange={(event)=>setRaw(event.target.value)} disabled={command.isPending} placeholder={`{"capture":{...},"articles":[...]}`} /></label>{localError && <Alert type="error" showIcon title="导入没有完成" description={<><p>{localError}</p>{command.error && <pre>{JSON.stringify((command.error as {details?:unknown}).details ?? command.error,null,2)}</pre>}</>} />}{success && <Alert type="success" showIcon title={`已保存来源快照 ${success.capture_snapshot_id}`} description={<>{success.provenance} · 正文摘要 {success.extracted_full_text_sha256}<div>导入 {success.articles.length} 条精确版本；每条继续使用治理流程。</div></>} />}{command.isPending && <p role="status">正在核验并保存来源捕获…</p>}<Button type="primary" htmlType="submit" loading={command.isPending} disabled={!raw.trim() || command.isPending}>导入并核验</Button></form></section>
    <form className="governance-filters" onSubmit={submitSearch}><label className="field" htmlFor="legal-admin-search">查找法律条文<Input id="legal-admin-search" type="search" value={searchDraft} placeholder="法规名称、条号或正文" onChange={(event)=>setSearchDraft(event.target.value)} /></label><Button type="primary" htmlType="submit">检索</Button></form>
    {query.isLoading && <PageStatus kind="loading" title="正在读取法律条文" />}{query.isError && <Alert type="error" showIcon title="法律条文列表没有加载" description={errorMessage(query.error)} action={<Button onClick={()=>void query.refetch()}>重试</Button>} />}{query.data && <><Table rowKey={(item)=>`${item.ref.knowledge_id}:${item.ref.version_id}`} dataSource={query.data.items} columns={columns} pagination={false} locale={{emptyText:"没有可显示的法律条文"}} />{query.data.next_cursor ? <Button htmlType="button" onClick={()=>{const next=new URLSearchParams(params);next.set("cursor",query.data.next_cursor!);setParams(next);}}>下一页</Button> : null}</>}
  </section>;
}

function LegalVersionDetail({ value, capture, captureLoading, captureError, onRetryCapture }: { value: KnowledgeVersion; capture?: LegalSourceCapture; captureLoading: boolean; captureError: string; onRetryCapture: () => void }) {
  const content = value.content;
  const metadata = content.legal_metadata;
  if (content.kind !== "legal" || !metadata) return <Alert type="error" title="服务返回的法律版本结构无法识别" />;
  const captureMatches = capture?.capture_snapshot_id === metadata.source_snapshot_id &&
    capture.source_id === metadata.source_id && capture.raw_sha256 === metadata.source_sha256;
  return <>
    <h3>{metadata.norm_title} · {metadata.article_label}</h3>
    <pre className="legal-article-body">{content.body}</pre>
    <dl>
      <dt>规范版本</dt><dd>{metadata.norm_version} · 精确版本 {value.ref.version_id}</dd>
      <dt>制定机关</dt><dd>{metadata.issuer}</dd>
      <dt>公布时间</dt><dd>{metadata.promulgated_on}</dd>
      <dt>施行时间</dt><dd>{metadata.effective_from}{metadata.effective_until ? ` 至 ${metadata.effective_until}` : "起"}</dd>
      <dt>来源捕获</dt><dd>{formatDateTime(metadata.source_captured_at)} · SHA-256 {metadata.source_sha256}</dd>
      <dt>效力观察</dt><dd>{metadata.official_status} · {formatDateTime(metadata.status_observed_at)} · SHA-256 {metadata.status_evidence_sha256}</dd>
      <dt>审阅状态</dt><dd>{metadata.legal_review_status === "NOT_RUN" ? "未审核（NOT_RUN）" : metadata.legal_review_status}{metadata.reviewer ? ` · 审阅人 ${metadata.reviewer}` : ""}</dd>
      <dt>官方来源</dt><dd><a href={metadata.official_url} target="_blank" rel="noreferrer">{metadata.official_url}</a></dd>
    </dl>
    <section aria-label="原始来源捕获">
      <h4>原始来源捕获</h4>
      {captureLoading ? <p>正在读取固定来源快照…</p> : null}
      {captureError ? <Alert type="error" showIcon title="来源快照没有加载" description={captureError} action={<Button onClick={onRetryCapture}>重试</Button>} /> : null}
      {capture ? captureMatches ? <>
        <p>{capture.norm_title} · {capture.provenance} · HTTP {capture.response_status} · {capture.media_type}</p>
        <p>原始响应 SHA-256：{capture.raw_sha256} · 状态证据 SHA-256：{capture.status_response_sha256}</p>
        <details><summary>查看来源原始响应（Base64）</summary><pre>{capture.raw_response}</pre></details>
        <details><summary>查看效力状态原始响应（Base64）</summary><pre>{capture.status_response}</pre></details>
        <h5>来源提取正文</h5><pre className="legal-article-body">{capture.extracted_full_text}</pre>
      </> : <Alert type="error" showIcon title="来源快照与法律版本引用不一致" /> : null}
    </section>
  </>;
}
