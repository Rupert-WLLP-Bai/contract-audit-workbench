import { Tabs } from "antd";
import { Collapse } from "antd";
import { Button, Input, Select } from "antd";
import {
  memo,
  useDeferredValue,
  useEffect,
  useMemo,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import { Link } from "react-router";
import type {
  CaseRelationInput,
  EvidenceAnchor,
  Finding,
  ReviewDocument,
} from "../api/types";
import type { ReviewLayout } from "../domain/review";
import {
  actorRoleLabel,
  caseHref,
  formatDateTime,
  parseLabel,
  parseTone,
  roleLabel,
} from "../domain/review";
import {
  createTextIndex,
  evidenceSourceMatches,
  findTextMatches,
  resolveAnchor,
  segmentText,
  type CodePointTextIndex,
  type ResolvedAnchor,
  type TextSegment,
} from "../domain/text";
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
  collaborators: {
    id: string;
    name: string;
    role: string;
    responsibilities: string[];
  }[];
  relatedCases: {
    relationId: string;
    id: string;
    title: string;
    reason: string;
    createdAt: string;
    runId: string | null;
  }[];
  availableCases: { id: string; title: string }[];
}

function documentKey(document: ReviewDocument): string {
  return `${document.id}\u0000${document.parsed_document?.id ?? document.parsed_document_id ?? "legacy"}`;
}

function documentLabel(
  document: ReviewDocument,
  documents: ReviewDocument[],
): string {
  const sameVersion = documents.filter((item) => item.id === document.id);
  return sameVersion.length > 1
    ? `${roleLabel(document.role)}，来源 ${sameVersion.findIndex((item) => documentKey(item) === documentKey(document)) + 1}`
    : roleLabel(document.role);
}

function documentForAnchor(
  anchor: EvidenceAnchor,
  documents: ReviewDocument[],
): ReviewDocument | null {
  const candidates = documents.filter(
    (document) => document.id === anchor.document_version_id,
  );
  return (
    candidates.find((document) =>
      evidenceSourceMatches(anchor, document.id, document.parsed_document, document.parsed_document_id),
    ) ??
    candidates[0] ??
    null
  );
}

function anchorOf(
  document: ReviewDocument,
  finding: Finding | null,
): EvidenceAnchor | null {
  const anchors =
    finding?.evidence.filter(
      (item) => item.document_version_id === document.id,
    ) ?? [];
  return (
    anchors.find((item) =>
      evidenceSourceMatches(item, document.id, document.parsed_document, document.parsed_document_id),
    ) ??
    anchors[0] ??
    null
  );
}

function DocPane({
  document,
  activeSource,
  children,
}: {
  document: ReviewDocument;
  activeSource: boolean;
  children: ReactNode;
}) {
  return (
    <div
      className="doc-pane"
      data-document-version-id={document.id}
      data-parsed-document-id={document.parsed_document?.id ?? document.parsed_document_id ?? ""}
      data-active-source={activeSource ? "true" : "false"}
    >
      {children}
    </div>
  );
}

function DiagnosticsCollapse({
  document,
  showDiagnostics,
}: {
  document: ReviewDocument;
  showDiagnostics: boolean;
}) {
  if (!showDiagnostics) return null;
  return (
    <Collapse
      className="doc-source-details"
      items={[
        {
          key: "content",
          label: "解析来源与摘要",
          children: (
            <>
              <ParsedDocumentSummary document={document} />
            </>
          ),
        },
      ]}
    />
  );
}

const DocSegments = memo(function DocSegments({
  segments,
}: {
  segments: TextSegment[];
}) {
  return (
    <>
      {segments.map((segment) =>
        segment.highlighted === "evidence" ? (
          <mark
            key={segment.start}
            data-offset={segment.start}
            className="hit-evidence"
          >
            {segment.text}
          </mark>
        ) : (
          <span
            key={segment.start}
            data-offset={
              segment.anchor || segment.highlighted === "find"
                ? segment.start
                : undefined
            }
            className={
              segment.highlighted === "find" ? "hit-find" : undefined
            }
          >
            {segment.text}
          </span>
        ),
      )}
    </>
  );
});

function FailedParseBody({
  document,
  resolved,
  showDiagnostics,
  activeSource,
}: {
  document: ReviewDocument;
  resolved: ResolvedAnchor | null;
  showDiagnostics: boolean;
  activeSource: boolean;
}) {
  return (
    <DocPane document={document} activeSource={activeSource}>
      <header className="doc-pane-head">
        <strong>{document.filename}</strong>
        <StatusText tone="bad">解析失败</StatusText>
      </header>
      <DiagnosticsCollapse
        document={document}
        showDiagnostics={showDiagnostics}
      />
      <p className="callout">这份材料解析失败，不能据此给出无风险结论。</p>
      {document.parse_error ? <p>{document.parse_error}</p> : null}
      {resolved?.kind === "snapshot" ? (
        <blockquote>{resolved.quote}</blockquote>
      ) : null}
    </DocPane>
  );
}

function HighlightBody({
  document,
  resolved,
  segments,
  showDiagnostics,
  activeSource,
}: {
  document: ReviewDocument;
  resolved: Extract<ResolvedAnchor, { kind: "highlight" }>;
  segments: TextSegment[];
  showDiagnostics: boolean;
  activeSource: boolean;
}) {
  return (
    <DocPane document={document} activeSource={activeSource}>
      <header className="doc-pane-head">
        <strong>{document.filename}</strong>
        <span>{roleLabel(document.role)}</span>
        {resolved.page !== null ? <span>第 {resolved.page} 页</span> : null}
      </header>
      <DiagnosticsCollapse
        document={document}
        showDiagnostics={showDiagnostics}
      />
      <div className="doc-text">
        <DocSegments segments={segments} />
      </div>
    </DocPane>
  );
}

function SnapshotCallout({
  resolved,
}: {
  resolved: Extract<ResolvedAnchor, { kind: "snapshot" }>;
}) {
  return (
    <div className="callout">
      <p>{resolved.reason}</p>
      {resolved.page !== null ? <p>记录中的页码：第 {resolved.page} 页</p> : null}
      <blockquote>{resolved.quote}</blockquote>
    </div>
  );
}

function EmptyOrText({
  resolved,
  document,
  segments,
}: {
  resolved: ResolvedAnchor | null;
  document: ReviewDocument;
  segments: TextSegment[];
}) {
  if (resolved !== null) return null;
  if (document.text === null) return <p>这份材料还没有可阅读的正文。</p>;
  return (
    <div className="doc-text">
      <DocSegments segments={segments} />
    </div>
  );
}

function PlainBody({
  document,
  resolved,
  showDiagnostics,
  segments,
  activeSource,
}: {
  document: ReviewDocument;
  resolved: ResolvedAnchor | null;
  showDiagnostics: boolean;
  segments: TextSegment[];
  activeSource: boolean;
}) {
  return (
    <DocPane document={document} activeSource={activeSource}>
      <header className="doc-pane-head">
        <strong>{document.filename}</strong>
        <span>{roleLabel(document.role)}</span>
        <StatusText tone={parseTone(document.parse_status)}>
          {parseLabel(document.parse_status)}
        </StatusText>
      </header>
      <DiagnosticsCollapse
        document={document}
        showDiagnostics={showDiagnostics}
      />
      {resolved?.kind === "snapshot" ? (
        <SnapshotCallout resolved={resolved} />
      ) : null}
      {resolved?.kind === "unavailable" ? (
        <p className="callout">{resolved.reason}</p>
      ) : null}
      <EmptyOrText
        resolved={resolved}
        document={document}
        segments={segments}
      />
    </DocPane>
  );
}

const DocumentBody = memo(function DocumentBody({
  document,
  finding,
  findRange,
  activeSource,
  showDiagnostics,
  textIndex: providedTextIndex,
}: {
  document: ReviewDocument;
  finding: Finding | null;
  findRange: { start: number; end: number } | null;
  activeSource: boolean;
  showDiagnostics: boolean;
  textIndex: CodePointTextIndex | null;
}) {
  const textIndex = useMemo(
    () =>
      providedTextIndex ??
      (document.text === null ? null : createTextIndex(document.text)),
    [
      document,
      document.parsed_document,
      document.parsed_document_id,
      document.text,
      providedTextIndex,
    ],
  );
  const anchor = useMemo(
    () => anchorOf(document, finding),
    [document, finding],
  );
  const resolved = useMemo(
    () =>
      anchor
        ? resolveAnchor(
            document.text,
            anchor,
            document.parsed_document,
            document.parsed_document_id,
            textIndex ?? undefined,
          )
        : null,
    [anchor, document, textIndex],
  );
  const clauseStarts = useMemo(
    () => document.clauses.map((clause) => clause.text_offset.start),
    [document, document.clauses],
  );
  const segments = useMemo(() => {
    if (
      document.text === null ||
      document.parse_status === "failed"
    ) return null;
    const highlight =
      resolved?.kind === "highlight"
        ? { start: resolved.start, end: resolved.end }
        : null;
    if (resolved !== null && highlight === null) return null;
    return segmentText(
      document.text,
      highlight,
      highlight ? clauseStarts : [],
      findRange,
      textIndex ?? undefined,
    );
  }, [clauseStarts, document, findRange, resolved, textIndex]);

  if (document.parse_status === "failed") {
    return (
      <FailedParseBody
        document={document}
        resolved={resolved}
        showDiagnostics={showDiagnostics}
        activeSource={activeSource}
      />
    );
  }

  if (resolved?.kind === "highlight" && document.text !== null) {
    return (
      <HighlightBody
        document={document}
        resolved={resolved}
        segments={segments ?? []}
        showDiagnostics={showDiagnostics}
        activeSource={activeSource}
      />
    );
  }

  return (
    <PlainBody
      document={document}
      resolved={resolved}
      showDiagnostics={showDiagnostics}
      segments={segments ?? []}
      activeSource={activeSource}
    />
  );
});

function ownerLabel(context: ReviewCaseContext): string {
  return context.ownerName || (context.ownerId ? "姓名未返回" : "未分配");
}

function businessScope(context: ReviewCaseContext): string {
  return (
    [context.businessLine, context.scenario]
      .filter((item) => item !== "" && item !== "未报告")
      .join(" / ") || "未设置"
  );
}

function deadlineLabel(context: ReviewCaseContext): string {
  return `${context.dueAt ? formatDateTime(context.dueAt) : "未设期限"} ，${context.status || "未报告"}`;
}

function CaseContextHeader({ context }: { context: ReviewCaseContext }) {
  return (
    <>
      <header className="review-case-context-head">
        <div>
          <p>案件上下文</p>
          <h2>{context.title}</h2>
          {context.number ? <span>{context.number}</span> : null}
        </div>
        <Link
          className="review-case-open"
          to={`/cases/${encodeURIComponent(context.caseId)}`}
        >
          案件详情
        </Link>
      </header>
      <dl className="review-case-context-meta">
        <div>
          <dt>客户</dt>
          <dd>{context.customer || "未设置"}</dd>
        </div>
        <div>
          <dt>供应商</dt>
          <dd>{context.supplier || "未设置"}</dd>
        </div>
        <div>
          <dt>合同类型</dt>
          <dd>{context.contractType || "未报告"}</dd>
        </div>
        <div>
          <dt>业务线 / 场景</dt>
          <dd>{businessScope(context)}</dd>
        </div>
        <div>
          <dt>负责人</dt>
          <dd>{ownerLabel(context)}</dd>
        </div>
        <div>
          <dt>期限 / 状态</dt>
          <dd>{deadlineLabel(context)}</dd>
        </div>
      </dl>
    </>
  );
}

function ContextTabs({
  context,
  leftTab,
  onTab,
}: {
  context: ReviewCaseContext;
  leftTab: string;
  onTab: (tab: "材料" | "审查成员" | "相关案件") => void;
}) {
  return (
    <Tabs
      activeKey={leftTab}
      onChange={(key) => {
        const next = (["材料", "审查成员", "相关案件"] as const).find(
          (label) => label === key,
        );
        if (next) onTab(next);
      }}
      items={[
        { key: "材料", label: "材料" },
        {
          key: "审查成员",
          label: `审查成员 ${context.collaborators.length}`,
        },
        {
          key: "相关案件",
          label: `相关案件 ${context.relatedCases.length}`,
        },
      ]}
    />
  );
}

function MaterialTabs({
  documents,
  activeKey,
  onChange,
}: {
  documents: ReviewDocument[];
  activeKey: string;
  onChange: (key: string) => void;
}) {
  return (
    <Tabs
      activeKey={activeKey}
      onChange={onChange}
      items={documents.map((item) => ({
        key: documentKey(item),
        label: (
          <span
            aria-label={documentLabel(item, documents)}
            title={item.filename}
            data-document-version-id={item.id}
            data-parsed-document-id={item.parsed_document?.id ?? item.parsed_document_id ?? ""}
          >
            {documentLabel(item, documents)}
          </span>
        ),
      }))}
    />
  );
}

function MaterialSearch({
  query,
  resultQuery,
  onQuery,
  matches,
  onNext,
}: {
  query: string;
  resultQuery: string;
  onQuery: (value: string) => void;
  matches: { start: number; end: number }[];
  onNext: () => void;
}) {
  return (
    <>
      <label className="field">
        <span>文内搜索</span>
        <Input
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="条款或正文"
        />
      </label>
      <p className="meta">
        {resultQuery.trim() === ""
          ? "输入后在当前材料中查找"
          : `正文匹配 ${matches.length} 处`}
      </p>
      {matches.length > 0 ? (
        <Button aria-label={"下一处"} htmlType="button" type="text" onClick={onNext}>
          下一处
        </Button>
      ) : null}
    </>
  );
}

const ClauseList = memo(function ClauseList({
  clauses,
  onFocus,
}: {
  clauses: ReviewDocument["clauses"];
  onFocus: (offset: number) => void;
}) {
  return (
    <>
      <h3>条款</h3>
      {clauses.length === 0 ? (
        <p className="meta">这份材料没有可在原文中找到的已保存条款。</p>
      ) : null}
      <ul className="clause-list">
        {clauses.map((clause) => (
          <li key={clause.id}>
            <Button
              aria-label={`${clause.page !== null ? `第 ${clause.page} 页，` : ""}${clause.heading}`}
              htmlType="button"
              onClick={() => onFocus(clause.text_offset.start)}
            >
              {clause.page !== null ? `第 ${clause.page} 页，` : ""}
              {clause.heading}
            </Button>
          </li>
        ))}
      </ul>
    </>
  );
});

const RelatedFindings = memo(function RelatedFindings({
  findings,
  selectedFindingId,
  onSelectFinding,
}: {
  findings: Finding[];
  selectedFindingId: string | null;
  onSelectFinding: (findingId: string) => void;
}) {
  return (
    <>
      <h3>引用该材料的线索</h3>
      {findings.length === 0 ? (
        <p className="meta">当前材料没有关联到本次运行的线索。</p>
      ) : (
        <ul className="clause-list">
          {findings.map((item) => (
            <li key={item.id}>
              <Button
                aria-label={"查看线索：" + String(item.claim || "未命名线索")}
                htmlType="button"
                aria-pressed={selectedFindingId === item.id}
                onClick={() => onSelectFinding(item.id)}
              >
                查看线索：{item.claim || "未命名线索"}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </>
  );
});

function MaterialPane({
  documents,
  active,
  query,
  resultQuery,
  matches,
  clauses,
  relatedFindings,
  selectedFindingId,
  onActiveKey,
  onQuery,
  onNextMatch,
  onFocusOffset,
  onSelectFinding,
}: {
  documents: ReviewDocument[];
  active: ReviewDocument | null;
  query: string;
  resultQuery: string;
  matches: { start: number; end: number }[];
  clauses: ReviewDocument["clauses"];
  relatedFindings: Finding[];
  selectedFindingId: string | null;
  onActiveKey: (key: string) => void;
  onQuery: (value: string) => void;
  onNextMatch: () => void;
  onFocusOffset: (offset: number) => void;
  onSelectFinding: (findingId: string) => void;
}) {
  return (
    <section className="review-material-context" aria-label="本次运行材料">
      <h2 className="sr-only">材料</h2>
      {documents.length === 0 ? (
        <p className="meta">本次运行没有带上材料。</p>
      ) : (
        <MaterialTabs
          documents={documents}
          activeKey={active ? documentKey(active) : ""}
          onChange={onActiveKey}
        />
      )}
      {documents.length > 0 ? (
        <>
          <MaterialSearch
            query={query}
            resultQuery={resultQuery}
            onQuery={onQuery}
            matches={matches}
            onNext={onNextMatch}
          />
          <ClauseList clauses={clauses} onFocus={onFocusOffset} />
          <RelatedFindings
            findings={relatedFindings}
            selectedFindingId={selectedFindingId}
            onSelectFinding={onSelectFinding}
          />
        </>
      ) : null}
    </section>
  );
}

function CollaboratorsPane({
  context,
}: {
  context: ReviewCaseContext;
}) {
  return (
    <section className="review-collaborators" aria-label="案件协作人员">
      <h2>审查成员</h2>
      <p className="meta">当前案件已授权的成员及实际分工。</p>
      {context.collaborators.length === 0 ? (
        <p className="meta">当前没有可显示的案件成员。</p>
      ) : (
        <ul>
          {context.collaborators.map((member) => (
            <li key={member.id}>
              <strong>{member.name}</strong>
              <span>{actorRoleLabel(member.role)}</span>
              <small>{member.responsibilities.join("，")}</small>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function RelationForm({
  availableCases,
  relatedCaseId,
  relationReason,
  pending,
  error,
  onRelatedCase,
  onReason,
  onSubmit,
}: {
  availableCases: { id: string; title: string }[];
  relatedCaseId: string;
  relationReason: string;
  pending: boolean;
  error: string | null;
  onRelatedCase: (value: string) => void;
  onReason: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  if (availableCases.length === 0) {
    return <p className="meta">当前身份可见范围内没有可新增的案件。</p>;
  }
  return (
    <form className="review-relation-form" onSubmit={onSubmit}>
      <label>
        关联可见案件
        <Select
          aria-label="关联可见案件"
          value={relatedCaseId || undefined}
          onChange={onRelatedCase}
          options={[
            { label: "选择案件", value: "" },
            ...availableCases.map((item) => ({
              label: item.title,
              value: item.id,
            })),
          ]}
        />
      </label>
      <label>
        关联原因
        <Input
          required
          maxLength={500}
          value={relationReason}
          onChange={(event) => onReason(event.target.value)}
          placeholder="填写业务关联依据"
        />
      </label>
      {error ? <p role="alert">{error}</p> : null}
      <Button
        aria-label={String(pending ? "正在保存" : "建立关联")}
        type="primary"
        htmlType="submit"
        disabled={pending || relatedCaseId === "" || relationReason.trim() === ""}
      >
        {pending ? "正在保存" : "建立关联"}
      </Button>
    </form>
  );
}

function RelatedCasesPane({
  context,
  canEditRelations,
  relationPending,
  relationError,
  relatedCaseId,
  relationReason,
  onCreateRelation,
  onDeleteRelation,
  onRelatedCase,
  onReason,
  onSubmitRelation,
}: {
  context: ReviewCaseContext;
  canEditRelations: boolean;
  relationPending: boolean;
  relationError: string | null;
  relatedCaseId: string;
  relationReason: string;
  onCreateRelation?: (input: CaseRelationInput) => Promise<void>;
  onDeleteRelation?: (relationId: string) => void;
  onRelatedCase: (value: string) => void;
  onReason: (value: string) => void;
  onSubmitRelation: (event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <section className="review-related-cases" aria-label="关联案件">
      <h2>
        相关案件 <span>{context.relatedCases.length}</span>
      </h2>
      {context.relatedCases.length === 0 ? (
        <p className="meta">当前案件没有已保存的关系。</p>
      ) : (
        <ul>
          {context.relatedCases.map((relation) => (
            <li key={relation.relationId}>
              <div>
                <Link to={caseHref(relation.id, relation.runId)}>
                  {relation.title}
                </Link>
                <small>{formatDateTime(relation.createdAt)}</small>
                <p>{relation.reason}</p>
              </div>
              {canEditRelations && onDeleteRelation ? (
                <Button
                  aria-label={"解除关联"}
                  htmlType="button"
                  type="text"
                  disabled={relationPending}
                  onClick={() => onDeleteRelation(relation.relationId)}
                >
                  解除关联
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {canEditRelations && onCreateRelation ? (
        <RelationForm
          availableCases={context.availableCases}
          relatedCaseId={relatedCaseId}
          relationReason={relationReason}
          pending={relationPending}
          error={relationError}
          onRelatedCase={onRelatedCase}
          onReason={onReason}
          onSubmit={onSubmitRelation}
        />
      ) : (
        <p className="meta">当前案件关系为只读。</p>
      )}
    </section>
  );
}

function DocumentToolbar({
  layout,
  evidenceCount,
  activeIsEvidence,
  compareSources,
  onCompare,
}: {
  layout: ReviewLayout;
  evidenceCount: number;
  activeIsEvidence: boolean;
  compareSources: boolean;
  onCompare: (compare: boolean) => void;
}) {
  const offered = layout === "split" && evidenceCount >= 2 && activeIsEvidence;
  return (
    <header className="document-toolbar">
      <h2>合同与证据原文</h2>
      {offered ? (
        <div className="filter-row" role="group" aria-label="正文显示模式">
          <Button
            aria-label={"单篇阅读"}
            htmlType="button"
            aria-pressed={!compareSources}
            onClick={() => onCompare(false)}
          >
            单篇阅读
          </Button>
          <Button
            aria-label={"双来源对照"}
            htmlType="button"
            aria-pressed={compareSources}
            onClick={() => onCompare(true)}
          >
            双来源对照
          </Button>
        </div>
      ) : null}
    </header>
  );
}

function SourceSwitch({
  evidenceDocs,
  documents,
  active,
  onSelect,
}: {
  evidenceDocs: ReviewDocument[];
  documents: ReviewDocument[];
  active: ReviewDocument | null;
  onSelect: (key: string) => void;
}) {
  return (
    <div className="source-switch" role="group" aria-label="证据来源">
      {evidenceDocs.map((item) => (
        <Button
          aria-label={String(documentLabel(item, documents))}
          key={documentKey(item)}
          htmlType="button"
          aria-pressed={active !== null && documentKey(active) === documentKey(item)}
          data-document-version-id={item.id}
          data-parsed-document-id={item.parsed_document?.id ?? item.parsed_document_id ?? ""}
          onClick={() => onSelect(documentKey(item))}
        >
          {documentLabel(item, documents)}
        </Button>
      ))}
    </div>
  );
}

function DocumentView({
  documents,
  finding,
  active,
  textIndex,
  currentMatch,
  showDiagnostics,
  split,
  evidenceDocs,
}: {
  documents: ReviewDocument[];
  finding: Finding | null;
  active: ReviewDocument | null;
  textIndex: CodePointTextIndex | null;
  currentMatch: { start: number; end: number } | null;
  showDiagnostics: boolean;
  split: boolean;
  evidenceDocs: ReviewDocument[];
}) {
  if (documents.length === 0) return <p>这次运行没有带上材料。</p>;
  if (split) {
    return (
      <div className="document-split">
        {evidenceDocs.map((item) => (
          <DocumentBody
            key={documentKey(item)}
            document={item}
            finding={finding}
            findRange={item === active ? currentMatch : null}
            textIndex={item === active ? textIndex : null}
            showDiagnostics={showDiagnostics}
            activeSource={item === active}
          />
        ))}
      </div>
    );
  }
  return active ? (
    <DocumentBody
      document={active}
      finding={finding}
      findRange={currentMatch}
      textIndex={textIndex}
      showDiagnostics={showDiagnostics}
      activeSource
    />
  ) : null;
}

function initialDocumentOf(
  finding: Finding | null,
  documents: ReviewDocument[],
): ReviewDocument | null {
  return (
    finding?.evidence
      .map((anchor) => documentForAnchor(anchor, documents))
      .find((item) => item !== null) ??
    documents[0] ??
    null
  );
}

function activeDocumentOf(
  documents: ReviewDocument[],
  activeKey: string,
): ReviewDocument | null {
  return (
    documents.find((document) => documentKey(document) === activeKey) ??
    documents[0] ??
    null
  );
}

function evidenceDocumentsOf(
  finding: Finding | null,
  documents: ReviewDocument[],
): ReviewDocument[] {
  if (!finding) return [];
  const unique = new Map<string, ReviewDocument>();
  for (const anchor of finding.evidence) {
    const document = documentForAnchor(anchor, documents);
    if (document) unique.set(documentKey(document), document);
  }
  return [...unique.values()].slice(0, 2);
}

function evidenceSourceOf(
  finding: Finding | null,
  documents: ReviewDocument[],
): ReviewDocument | null {
  if (!finding) return null;
  return (
    finding.evidence
      .map((anchor) => documentForAnchor(anchor, documents))
      .find((item) => item !== null) ?? null
  );
}

function relatedFindingsOf(
  active: ReviewDocument | null,
  findings: Finding[],
): Finding[] {
  if (active === null) return [];
  return findings.filter((item) =>
    item.evidence.some(
      (anchor) => anchor.document_version_id === active.id,
    ),
  );
}

function visibleClausesOf(
  active: ReviewDocument | null,
  query: string,
): ReviewDocument["clauses"] {
  return (
    active?.clauses.filter(
      (clause) => query.trim() === "" || clause.heading.includes(query.trim()),
    ) ?? []
  );
}

function useEvidenceAutoSwitch(
  finding: Finding | null,
  active: ReviewDocument | null,
  documents: ReviewDocument[],
  setActiveKey: (key: string) => void,
) {
  useEffect(() => {
    if (
      !finding ||
      !active ||
      finding.evidence.some(
        (anchor) => anchor.document_version_id === active.id,
      )
    )
      return;
    const source = evidenceSourceOf(finding, documents);
    if (source) setActiveKey(documentKey(source));
  }, [active, documents, finding, setActiveKey]);
}

function useScrollToEvidence(
  finding: Finding | null,
  active: ReviewDocument | null,
) {
  useEffect(() => {
    if (!finding || !active) return;
    const mark = document.querySelector(
      '.col-doc [data-active-source="true"] .hit-evidence',
    );
    if (mark instanceof HTMLElement) mark.scrollIntoView({ block: "nearest" });
  }, [finding, active]);
}

function isActiveEvidence(
  active: ReviewDocument | null,
  evidenceDocs: ReviewDocument[],
): boolean {
  return (
    active !== null &&
    evidenceDocs.some((item) => documentKey(item) === documentKey(active))
  );
}

function shouldSplit(
  compareSources: boolean,
  layout: ReviewLayout,
  evidenceCount: number,
  activeIsEvidence: boolean,
): boolean {
  return (
    compareSources &&
    layout === "split" &&
    evidenceCount >= 2 &&
    activeIsEvidence
  );
}


function currentMatchOf(
  matches: { start: number; end: number }[],
  matchIndex: number,
): { start: number; end: number } | null {
  return matches[matchIndex] ?? null;
}

function relationIsSubmittable(
  onCreateRelation: ((input: CaseRelationInput) => Promise<void>) | undefined,
  relatedCaseId: string,
  relationReason: string,
): boolean {
  return (
    onCreateRelation !== undefined &&
    relatedCaseId !== "" &&
    relationReason.trim() !== ""
  );
}

function focusOffsetNode(offset: number) {
  const node = document.querySelector(`[data-offset="${offset}"]`);
  if (node instanceof HTMLElement) node.scrollIntoView({ block: "center" });
}

function EvidenceAside({
  caseContext,
  leftTab,
  onTab,
  materials,
  collaborators,
  relatedCases,
}: {
  caseContext: ReviewCaseContext | null;
  leftTab: "材料" | "审查成员" | "相关案件";
  onTab: (tab: "材料" | "审查成员" | "相关案件") => void;
  materials: React.ReactNode;
  collaborators: React.ReactNode;
  relatedCases: React.ReactNode;
}) {
  return (
    <aside className="col-nav">
      {caseContext ? (
        <>
          <CaseContextHeader context={caseContext} />
          <ContextTabs context={caseContext} leftTab={leftTab} onTab={onTab} />
        </>
      ) : null}
      {leftTab === "材料" ? materials : null}
      {caseContext && leftTab === "审查成员" ? collaborators : null}
      {caseContext && leftTab === "相关案件" ? relatedCases : null}
    </aside>
  );
}

function DocumentColumn({
  documents,
  finding,
  active,
  textIndex,
  currentMatch,
  showDiagnostics,
  split,
  evidenceDocs,
  layout,
  activeIsEvidence,
  compareSources,
  onCompare,
  onActiveKey,
}: {
  documents: ReviewDocument[];
  finding: Finding | null;
  active: ReviewDocument | null;
  textIndex: CodePointTextIndex | null;
  currentMatch: { start: number; end: number } | null;
  showDiagnostics: boolean;
  split: boolean;
  evidenceDocs: ReviewDocument[];
  layout: ReviewLayout;
  activeIsEvidence: boolean;
  compareSources: boolean;
  onCompare: (compare: boolean) => void;
  onActiveKey: (key: string) => void;
}) {
  return (
    <section className="col-doc" aria-label="原文">
      <DocumentToolbar
        layout={layout}
        evidenceCount={evidenceDocs.length}
        activeIsEvidence={activeIsEvidence}
        compareSources={compareSources}
        onCompare={onCompare}
      />
      {!split && evidenceDocs.length >= 2 ? (
        <SourceSwitch
          evidenceDocs={evidenceDocs}
          documents={documents}
          active={active}
          onSelect={onActiveKey}
        />
      ) : null}
      <DocumentView
        documents={documents}
        finding={finding}
        active={active}
        textIndex={textIndex}
        currentMatch={currentMatch}
        showDiagnostics={showDiagnostics}
        split={split}
        evidenceDocs={evidenceDocs}
      />
    </section>
  );
}

export function DocumentStage({
  documents,
  finding,
  findings,
  selectedFindingId,
  layout,
  onSelectFinding,
  showDiagnostics = false,
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
  showDiagnostics?: boolean;
  onSelectFinding: (findingId: string) => void;
  caseContext?: ReviewCaseContext | null;
  canEditRelations?: boolean;
  relationPending?: boolean;
  relationError?: string | null;
  onCreateRelation?: (input: CaseRelationInput) => Promise<void>;
  onDeleteRelation?: (relationId: string) => void;
}) {
  const initialDocument = initialDocumentOf(finding, documents);
  const [activeKey, setActiveKey] = useState(() =>
    initialDocument ? documentKey(initialDocument) : "",
  );
  const [query, setQuery] = useState("");
  const deferredQuery = useDeferredValue(query);
  const [matchIndex, setMatchIndex] = useState(0);
  const [compareSources, setCompareSources] = useState(false);
  const [leftTab, setLeftTab] = useState<"材料" | "审查成员" | "相关案件">(
    "材料",
  );
  const [relatedCaseId, setRelatedCaseId] = useState("");
  const [relationReason, setRelationReason] = useState("");
  const active = activeDocumentOf(documents, activeKey);
  const activeTextIndex = useMemo(
    () =>
      active === null || active.text === null
        ? null
        : createTextIndex(active.text),
    [active, active?.parsed_document, active?.parsed_document_id, active?.text],
  );
  const evidenceDocs = useMemo(
    () => evidenceDocumentsOf(finding, documents),
    [documents, finding],
  );
  useEvidenceAutoSwitch(finding, active, documents, setActiveKey);
  const relatedFindings = useMemo(
    () => relatedFindingsOf(active, findings),
    [active, findings],
  );
  const activeIsEvidence = isActiveEvidence(active, evidenceDocs);
  const split = shouldSplit(
    compareSources,
    layout,
    evidenceDocs.length,
    activeIsEvidence,
  );
  const matches = useMemo(
    () =>
      active?.text
        ? findTextMatches(
            active.text,
            deferredQuery,
            activeTextIndex ?? undefined,
          )
        : [],
    [active, activeTextIndex, deferredQuery],
  );
  const currentMatch = currentMatchOf(matches, matchIndex);
  useScrollToEvidence(finding, active);
  const visibleClauses = useMemo(
    () => visibleClausesOf(active, deferredQuery),
    [active, deferredQuery],
  );

  function submitRelation(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!relationIsSubmittable(onCreateRelation, relatedCaseId, relationReason))
      return;
    void onCreateRelation!({
      related_case_id: relatedCaseId,
      reason: relationReason.trim(),
    }).then(
      () => {
        setRelatedCaseId("");
        setRelationReason("");
      },
      () => undefined,
    );
  }

  function nextMatch() {
    const next = (matchIndex + 1) % matches.length;
    const range = matches[next];
    setMatchIndex(next);
    if (range) focusOffsetNode(range.start);
  }

  return (
    <>
      <EvidenceAside
        caseContext={caseContext}
        leftTab={leftTab}
        onTab={setLeftTab}
        collaborators={<CollaboratorsPane context={caseContext!} />}
        relatedCases={
          <RelatedCasesPane
            context={caseContext!}
            canEditRelations={canEditRelations}
            relationPending={relationPending}
            relationError={relationError}
            relatedCaseId={relatedCaseId}
            relationReason={relationReason}
            onCreateRelation={onCreateRelation}
            onDeleteRelation={onDeleteRelation}
            onRelatedCase={setRelatedCaseId}
            onReason={setRelationReason}
            onSubmitRelation={submitRelation}
          />
        }
        materials={
          <MaterialPane
            documents={documents}
            active={active}
            query={query}
            resultQuery={deferredQuery}
            matches={matches}
            clauses={visibleClauses}
            relatedFindings={relatedFindings}
            selectedFindingId={selectedFindingId}
            onActiveKey={(key) => {
              setActiveKey(key);
              setMatchIndex(0);
            }}
            onQuery={(value) => {
              setQuery(value);
              setMatchIndex(0);
            }}
            onNextMatch={nextMatch}
            onFocusOffset={focusOffsetNode}
            onSelectFinding={onSelectFinding}
          />
        }
      />
      <DocumentColumn
        documents={documents}
        finding={finding}
        textIndex={activeTextIndex}
        active={active}
        currentMatch={currentMatch}
        showDiagnostics={showDiagnostics}
        split={split}
        evidenceDocs={evidenceDocs}
        layout={layout}
        activeIsEvidence={activeIsEvidence}
        compareSources={compareSources}
        onCompare={setCompareSources}
        onActiveKey={setActiveKey}
      />
    </>
  );
}
