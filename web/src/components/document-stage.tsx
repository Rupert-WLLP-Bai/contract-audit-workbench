import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link } from "react-router";
import type { CaseRelationInput, EvidenceAnchor, Finding, ReviewDocument } from "../api/types";
import type { ReviewLayout } from "../domain/review";
import { actorRoleLabel, formatDateTime, parseLabel, parseTone, roleLabel, shortDigest } from "../domain/review";
import { evidenceSourceMatches, findTextMatches, resolveAnchor, segmentText } from "../domain/text";
import { ParsedDocumentSummary } from "./parsed-document-summary";
import { StatusText } from "./status-text";

export interface ReviewCaseContext {
  caseId: string;
  title: string;
  number: string | null;
  customer: string | null;
  supplier: string | null;
  contractType: string;
  businessLine: string;
  scenario: string;
  status: string;
  ownerId: string | null;
  ownerName: string | null;
  dueAt: string | null;
  collaborators: { id: string; name: string; role: string; responsibilities: string[] }[];
  relatedCases: { relationId: string; id: string; title: string; reason: string; createdAt: string; runId: string | null }[];
  availableCases: { id: string; title: string }[];
}

function documentKey(document: ReviewDocument): string {
  return `${document.id}\u0000${document.parsed_document?.id ?? "legacy"}`;
}


function documentLabel(document: ReviewDocument, documents: ReviewDocument[]): string {
  const sameVersion = documents.filter((item) => item.id === document.id);
  return sameVersion.length > 1 && document.parsed_document
    ? `${roleLabel(document.role)} · 解析 ${shortDigest(document.parsed_document.id)}`
    : roleLabel(document.role);
}

function documentForAnchor(anchor: EvidenceAnchor, documents: ReviewDocument[]): ReviewDocument | null {
  const candidates = documents.filter((document) => document.id === anchor.document_version_id);
  return candidates.find((document) => evidenceSourceMatches(anchor, document.id, document.parsed_document)) ?? candidates[0] ?? null;
}

function DocumentBody({
  document,
  finding,
  findRange,
  activeSource,
}: {
  document: ReviewDocument;
  finding: Finding | null;
  findRange: { start: number; end: number } | null;
  activeSource: boolean;
}) {
  const anchors = finding?.evidence.filter((item) => item.document_version_id === document.id) ?? [];
  const anchor = anchors.find((item) => evidenceSourceMatches(item, document.id, document.parsed_document)) ?? anchors[0] ?? null;
  const resolved = anchor ? resolveAnchor(document.text, anchor, document.parsed_document) : null;

  if (document.parse_status === "failed") {
    return (
      <div
        className="doc-pane"
        data-document-version-id={document.id}
        data-parsed-document-id={document.parsed_document?.id ?? ""}
        data-active-source={activeSource ? "true" : "false"}
      >
        <header className="doc-pane-head">
          <strong>{document.filename}</strong>
          <StatusText tone="bad">解析失败</StatusText>
        </header>
        <details className="doc-source-details"><summary>解析来源与摘要</summary><ParsedDocumentSummary document={document} /></details>
        <p className="callout">这份材料解析失败，不能据此给出无风险结论。</p>
        {document.parse_error ? <p>{document.parse_error}</p> : null}
        {resolved?.kind === "snapshot" ? <blockquote>{resolved.quote}</blockquote> : null}
      </div>
    );
  }

  if (resolved?.kind === "highlight" && document.text !== null) {
    const segments = segmentText(
      document.text,
      { start: resolved.start, end: resolved.end },
      document.clauses.map((clause) => clause.text_offset.start),
      findRange,
    );
    return (
      <div
        className="doc-pane"
        data-document-version-id={document.id}
        data-parsed-document-id={document.parsed_document?.id ?? ""}
        data-active-source={activeSource ? "true" : "false"}
      >
        <header className="doc-pane-head">
          <strong>{document.filename}</strong>
          <span>{roleLabel(document.role)}</span>
          {resolved.page !== null ? <span>第 {resolved.page} 页</span> : null}
        </header>
        <details className="doc-source-details"><summary>解析来源与摘要</summary><ParsedDocumentSummary document={document} /></details>
        <div className="doc-text">
          {segments.map((segment) =>
            segment.highlighted === "evidence" ? (
              <mark key={segment.start} data-offset={segment.start} className="hit-evidence">
                {segment.text}
              </mark>
            ) : (
              <span
                key={segment.start}
                data-offset={segment.anchor || segment.highlighted === "find" ? segment.start : undefined}
                className={segment.highlighted === "find" ? "hit-find" : undefined}
              >
                {segment.text}
              </span>
            ),
          )}
        </div>
      </div>
    );
  }

  return (
    <div
      className="doc-pane"
      data-document-version-id={document.id}
      data-parsed-document-id={document.parsed_document?.id ?? ""}
      data-active-source={activeSource ? "true" : "false"}
    >
      <header className="doc-pane-head">
        <strong>{document.filename}</strong>
        <span>{roleLabel(document.role)}</span>
        <StatusText tone={parseTone(document.parse_status)}>{parseLabel(document.parse_status)}</StatusText>
      </header>
      <details className="doc-source-details"><summary>解析来源与摘要</summary><ParsedDocumentSummary document={document} /></details>
      {resolved?.kind === "snapshot" ? (
        <div className="callout">
          <p>{resolved.reason}</p>
          {resolved.page !== null ? <p>记录中的页码：第 {resolved.page} 页</p> : null}
          <blockquote>{resolved.quote}</blockquote>
        </div>
      ) : null}
      {resolved?.kind === "unavailable" ? <p className="callout">{resolved.reason}</p> : null}
      {resolved === null && document.text === null ? <p>这份材料还没有可阅读的正文。</p> : null}
      {resolved === null && document.text !== null ? (
        <div className="doc-text">
          {segmentText(document.text, null, [], findRange).map((segment) => (
            <span
              key={segment.start}
              data-offset={segment.anchor || segment.highlighted === "find" ? segment.start : undefined}
              className={segment.highlighted === "find" ? "hit-find" : undefined}
            >
              {segment.text}
            </span>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function DocumentStage({
  documents,
  finding,
  findings,
  selectedFindingId,
  layout,
  onSelectFinding,
  caseContext = null,
  canEditRelations = false,
  relationPending = false,
  relationError = null,
  onCreateRelation,
  onDeleteRelation,
}: {
  documents: ReviewDocument[];
  finding: Finding | null;
  findings: Finding[];
  selectedFindingId: string | null;
  layout: ReviewLayout;
  onSelectFinding: (findingId: string) => void;
  caseContext?: ReviewCaseContext | null;
  canEditRelations?: boolean;
  relationPending?: boolean;
  relationError?: string | null;
  onCreateRelation?: (input: CaseRelationInput) => Promise<void>;
  onDeleteRelation?: (relationId: string) => void;
}) {
  const initialDocument = finding?.evidence.map((anchor) => documentForAnchor(anchor, documents)).find((item) => item !== null) ?? documents[0] ?? null;
  const [activeKey, setActiveKey] = useState(() => initialDocument ? documentKey(initialDocument) : "");
  const [query, setQuery] = useState("");
  const [matchIndex, setMatchIndex] = useState(0);
  const [compareSources, setCompareSources] = useState(false);
  const [leftTab, setLeftTab] = useState<"材料" | "审查成员" | "相关案件">("材料");
  const [relatedCaseId, setRelatedCaseId] = useState("");
  const [relationReason, setRelationReason] = useState("");
  const active = documents.find((document) => documentKey(document) === activeKey) ?? documents[0] ?? null;
  const evidenceDocs = useMemo(() => {
    if (!finding) return [];
    const unique = new Map<string, ReviewDocument>();
    for (const anchor of finding.evidence) {
      const document = documentForAnchor(anchor, documents);
      if (document) unique.set(documentKey(document), document);
    }
    return [...unique.values()].slice(0, 2);
  }, [documents, finding]);
  useEffect(() => {
    if (!finding || !active || finding.evidence.some((anchor) => anchor.document_version_id === active.id)) return;
    const source = finding.evidence.map((anchor) => documentForAnchor(anchor, documents)).find((item) => item !== null);
    if (source) setActiveKey(documentKey(source));
  }, [active, documents, finding]);
  const relatedFindings = active === null
    ? []
    : findings.filter((item) => item.evidence.some((anchor) => anchor.document_version_id === active.id));
  const activeIsEvidence = active !== null && evidenceDocs.some((item) => documentKey(item) === documentKey(active));
  const split = compareSources && layout === "split" && evidenceDocs.length >= 2 && activeIsEvidence;
  const matches = useMemo(() => (active?.text ? findTextMatches(active.text, query) : []), [active, query]);
  const currentMatch = matches[matchIndex] ?? null;
  useEffect(() => {
    if (!finding || !active) return;
    const mark = document.querySelector('.col-doc [data-active-source="true"] .hit-evidence');
    if (mark instanceof HTMLElement) mark.scrollIntoView({ block: "nearest" });
  }, [finding?.id, active?.id, active?.parsed_document?.id]);
  const visibleClauses =
    active?.clauses.filter((clause) => query.trim() === "" || clause.heading.includes(query.trim())) ?? [];

  function focusOffset(offset: number) {
    const node = document.querySelector(`[data-offset="${offset}"]`);
    if (node instanceof HTMLElement) node.scrollIntoView({ block: "center" });
  }

  function submitRelation(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!onCreateRelation || relatedCaseId === "" || relationReason.trim() === "") return;
    void onCreateRelation({ related_case_id: relatedCaseId, reason: relationReason.trim() }).then(() => {
      setRelatedCaseId("");
      setRelationReason("");
    }, () => undefined);
  }

  return (
    <>
      <aside className="col-nav">
        {caseContext ? <>
          <header className="review-case-context-head">
            <div><p>案件上下文</p><h2>{caseContext.title}</h2><span>{caseContext.number ?? caseContext.caseId}</span></div>
            <Link className="review-case-open" to={`/cases/${encodeURIComponent(caseContext.caseId)}`}>案件详情</Link>
          </header>
          <dl className="review-case-context-meta">
            <div><dt>客户</dt><dd>{caseContext.customer || "未设置"}</dd></div>
            <div><dt>供应商</dt><dd>{caseContext.supplier || "未设置"}</dd></div>
            <div><dt>合同类型</dt><dd>{caseContext.contractType || "未报告"}</dd></div>
            <div><dt>业务线 / 场景</dt><dd>{[caseContext.businessLine, caseContext.scenario].filter((item) => item !== "" && item !== "未报告").join(" / ") || "未设置"}</dd></div>
            <div><dt>负责人</dt><dd>{caseContext.ownerName || caseContext.ownerId || "未分配"}</dd></div>
            <div><dt>期限 / 状态</dt><dd>{caseContext.dueAt ? formatDateTime(caseContext.dueAt) : "未设期限"} · {caseContext.status || "未报告"}</dd></div>
          </dl>
          <div className="review-context-tabs" role="tablist" aria-label="案件材料、成员与关联案件">
            {(["材料", "审查成员", "相关案件"] as const).map((label) => <button key={label} type="button" role="tab" aria-selected={leftTab === label} onClick={() => setLeftTab(label)}>{label}{label === "相关案件" ? <span>{caseContext.relatedCases.length}</span> : label === "审查成员" ? <span>{caseContext.collaborators.length}</span> : null}</button>)}
          </div>
        </> : null}
        {leftTab === "材料" ? <section className="review-material-context" aria-label="本次运行材料">
          <h2>材料</h2>
          {documents.length === 0 ? <p className="meta">本次运行没有带上材料。</p> : <div className="tablist" role="tablist" aria-label="案件材料">
            {documents.map((item) => (
              <button
                key={documentKey(item)}
                type="button"
                role="tab"
                aria-label={documentLabel(item, documents)}
                aria-selected={active !== null && documentKey(active) === documentKey(item)}
                data-document-version-id={item.id}
                data-parsed-document-id={item.parsed_document?.id ?? ""}
                onClick={() => {
                  setActiveKey(documentKey(item));
                  setMatchIndex(0);
                }}
              >
                {documentLabel(item, documents)}
                <span className="material-filename" title={item.filename}>{item.filename}</span>
              </button>
            ))}
          </div>}
          {documents.length > 0 ? <>
            <label className="field">
              <span>文内搜索</span>
              <input
                value={query}
                onChange={(event) => {
                  setQuery(event.target.value);
                  setMatchIndex(0);
                }}
                placeholder="条款或正文"
              />
            </label>
            <p className="meta">{query.trim() === "" ? "输入后在当前材料中查找" : `正文匹配 ${matches.length} 处`}</p>
            {matches.length > 0 ? (
              <button
                type="button"
                className="button button-quiet"
                onClick={() => {
                  const next = (matchIndex + 1) % matches.length;
                  const range = matches[next];
                  setMatchIndex(next);
                  if (range) focusOffset(range.start);
                }}
              >
                下一处
              </button>
            ) : null}
            <h3>条款</h3>
            {visibleClauses.length === 0 ? <p className="meta">当前材料没有可定位的已保存条款。</p> : null}
            <ul className="clause-list">
              {visibleClauses.map((clause) => (
                <li key={clause.id}>
                  <button type="button" onClick={() => focusOffset(clause.text_offset.start)}>
                    {clause.page !== null ? `第 ${clause.page} 页 · ` : ""}
                    {clause.heading}
                  </button>
                </li>
              ))}
            </ul>
            <h3>引用该材料的线索</h3>
            {relatedFindings.length === 0 ? <p className="meta">当前材料没有关联到本次运行的线索。</p> : (
              <ul className="clause-list">
                {relatedFindings.map((item) => (
                  <li key={item.id}>
                    <button
                      type="button"
                      aria-pressed={selectedFindingId === item.id}
                      onClick={() => onSelectFinding(item.id)}
                    >
                      查看线索：{item.claim || item.id}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </> : null}
        </section> : null}
        {caseContext && leftTab === "审查成员" ? <section className="review-collaborators" aria-label="案件协作人员">
          <h2>审查成员</h2>
          <p className="meta">当前案件已授权的成员及实际分工。</p>
          {caseContext.collaborators.length === 0 ? <p className="meta">当前没有可显示的案件成员。</p> : <ul>
            {caseContext.collaborators.map((member) => <li key={member.id}>
              <strong>{member.name}</strong><span>{actorRoleLabel(member.role)}</span><small>{member.responsibilities.join(" · ")}</small>
            </li>)}
          </ul>}
        </section> : null}
        {caseContext && leftTab === "相关案件" ? <section className="review-related-cases" aria-label="关联案件">
          <h2>相关案件 <span>{caseContext.relatedCases.length}</span></h2>
          {caseContext.relatedCases.length === 0 ? <p className="meta">当前案件没有已保存的关系。</p> : <ul>
            {caseContext.relatedCases.map((relation) => <li key={relation.relationId}>
              <div><Link to={relation.runId ? `/cases/${encodeURIComponent(relation.id)}/reviews/${encodeURIComponent(relation.runId)}` : `/cases/${encodeURIComponent(relation.id)}`}>{relation.title}</Link><small>{relation.id} · {formatDateTime(relation.createdAt)}</small><p>{relation.reason}</p></div>
              {canEditRelations && onDeleteRelation ? <button type="button" className="button button-quiet" disabled={relationPending} onClick={() => onDeleteRelation(relation.relationId)}>解除关联</button> : null}
            </li>)}
          </ul>}
          {canEditRelations && onCreateRelation ? caseContext.availableCases.length > 0 ? <form className="review-relation-form" onSubmit={submitRelation}>
            <label>关联可见案件<select required value={relatedCaseId} onChange={(event) => setRelatedCaseId(event.target.value)}><option value="">选择案件</option>{caseContext.availableCases.map((item) => <option key={item.id} value={item.id}>{item.title} · {item.id}</option>)}</select></label>
            <label>关联原因<input required maxLength={500} value={relationReason} onChange={(event) => setRelationReason(event.target.value)} placeholder="填写业务关联依据" /></label>
            {relationError ? <p role="alert">{relationError}</p> : null}
            <button className="button button-primary" type="submit" disabled={relationPending || relatedCaseId === "" || relationReason.trim() === ""}>{relationPending ? "正在保存" : "建立关联"}</button>
          </form> : <p className="meta">当前身份可见范围内没有可新增的案件。</p> : <p className="meta">当前案件关系为只读。</p>}
        </section> : null}
      </aside>
      <section className="col-doc" aria-label="原文">
        <header className="document-toolbar">
          <h2>合同与证据原文</h2>
          {layout === "split" && evidenceDocs.length >= 2 && activeIsEvidence ? (
            <div className="filter-row" role="group" aria-label="正文显示模式">
              <button type="button" aria-pressed={!compareSources} onClick={() => setCompareSources(false)}>单篇阅读</button>
              <button type="button" aria-pressed={compareSources} onClick={() => setCompareSources(true)}>双来源对照</button>
            </div>
          ) : null}
        </header>
        {!split && evidenceDocs.length >= 2 ? (
          <div className="source-switch" role="group" aria-label="证据来源">
            {evidenceDocs.map((item) => (
              <button
                key={documentKey(item)}
                type="button"
                aria-pressed={active !== null && documentKey(active) === documentKey(item)}
                data-document-version-id={item.id}
                data-parsed-document-id={item.parsed_document?.id ?? ""}
                onClick={() => setActiveKey(documentKey(item))}
              >
                {documentLabel(item, documents)}
              </button>
            ))}
          </div>
        ) : null}
        {documents.length === 0 ? <p>这次运行没有带上材料。</p> : null}
        {split ? (
          <div className="document-split">
            {evidenceDocs.map((item) => (
              <DocumentBody
                key={documentKey(item)}
                document={item}
                finding={finding}
                findRange={item === active ? currentMatch : null}
                activeSource={item === active}
              />
            ))}
          </div>
        ) : active ? (
          <DocumentBody document={active} finding={finding} findRange={currentMatch} activeSource />
        ) : null}
      </section>
    </>
  );
}
