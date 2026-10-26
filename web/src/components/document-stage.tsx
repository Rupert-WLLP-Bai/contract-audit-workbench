import {
  AimOutlined,
  DownOutlined,
  LeftOutlined,
  RightOutlined,
  SearchOutlined,
  UpOutlined,
  ZoomInOutlined,
  ZoomOutOutlined,
} from "@ant-design/icons";
import { Button, Collapse, Drawer, Input, Select, Tabs } from "antd";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
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
  formatDateTime,
  parseLabel,
  parseTone,
  roleLabel,
  shortDigest,
} from "../domain/review";
import {
  findingHighlights,
  firstClauseOffsetForPage,
  reportedPages,
  severityRank,
} from "../domain/review-presentation";
import {
  evidenceSourceMatches,
  findTextMatches,
  resolveAnchor,
  segmentMarkedText,
} from "../domain/text";
import { ParsedDocumentSummary } from "./parsed-document-summary";
import { RiskTag } from "./ui";
import { StatusText } from "./status-text";
import "./review-workspace.css";

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
  return `${document.id}\u0000${document.parsed_document?.id ?? "legacy"}`;
}

function documentLabel(
  document: ReviewDocument,
  documents: ReviewDocument[],
): string {
  const sameVersion = documents.filter((item) => item.id === document.id);
  return sameVersion.length > 1 && document.parsed_document
    ? `${roleLabel(document.role)} · 解析 ${shortDigest(document.parsed_document.id)}`
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
      evidenceSourceMatches(anchor, document.id, document.parsed_document),
    ) ??
    candidates[0] ??
    null
  );
}

function DocumentSourceDetails({ document }: { document: ReviewDocument }) {
  return (
    <Collapse
      className="doc-source-details review-doc-source"
      items={[
        {
          key: "content",
          label: "解析来源与摘要",
          children: <ParsedDocumentSummary document={document} />,
        },
      ]}
    />
  );
}

function DocumentBody({
  document,
  findings,
  selectedFindingId,
  onSelectFinding,
  findRange,
  activeSource,
}: {
  document: ReviewDocument;
  findings: Finding[];
  selectedFindingId: string | null;
  onSelectFinding: (findingId: string) => void;
  findRange: { start: number; end: number } | null;
  activeSource: boolean;
}) {
  const letterAnchor = findings
    .find((item) => item.id === selectedFindingId)
    ?.evidence.find((item) => item.document_version_id === document.id);
  const resolved = letterAnchor
    ? resolveAnchor(document.text, letterAnchor, document.parsed_document)
    : null;
  const highlights = useMemo(
    () => findingHighlights(findings, document, selectedFindingId),
    [document, findings, selectedFindingId],
  );
  const clauseStarts = document.clauses.map(
    (clause) => clause.text_offset.start,
  );
  const segments = useMemo(
    () =>
      document.text === null
        ? []
        : segmentMarkedText(
            document.text,
            highlights,
            clauseStarts,
            findRange,
          ),
    [clauseStarts, document.text, findRange, highlights],
  );

  const paneAttributes = {
    "data-document-version-id": document.id,
    "data-parsed-document-id": document.parsed_document?.id ?? "",
    "data-active-source": activeSource ? "true" : "false",
  };

  if (document.parse_status === "failed") {
    return (
      <div className="doc-pane" {...paneAttributes}>
        <header className="doc-pane-head">
          <div>
            <strong>{document.filename}</strong>
            <span className="meta">
              {roleLabel(document.role)} · 解析失败
            </span>
          </div>
          <StatusText tone="bad">解析失败</StatusText>
        </header>
        <p className="review-doc-callout">
          这份材料解析失败，不能据此给出无风险结论。
        </p>
        {document.parse_error ? (
          <p className="meta">{document.parse_error}</p>
        ) : null}
        <DocumentSourceDetails document={document} />
      </div>
    );
  }

  return (
    <div className="doc-pane" {...paneAttributes}>
      <header className="doc-pane-head">
        <div>
          <strong>{document.filename}</strong>
          <span className="meta">
            {roleLabel(document.role)}
            {resolved?.kind === "highlight" && resolved.page !== null
              ? ` · 第 ${resolved.page} 页`
              : ""}
          </span>
        </div>
        <StatusText tone={parseTone(document.parse_status)}>
          {parseLabel(document.parse_status)}
        </StatusText>
      </header>

      {resolved?.kind === "snapshot" ? (
        <div className="review-doc-callout">
          <p>{resolved.reason}</p>
          {resolved.page !== null ? (
            <p className="meta">记录中的页码：第 {resolved.page} 页</p>
          ) : null}
          <blockquote>{resolved.quote}</blockquote>
        </div>
      ) : null}
      {resolved?.kind === "unavailable" ? (
        <p className="review-doc-callout">{resolved.reason}</p>
      ) : null}

      {document.text === null ? (
        <p className="meta">这份材料还没有可阅读的正文。</p>
      ) : (
        <div className="doc-text">
          {segments.map((segment) => {
            if (segment.marked.length === 0) {
              return (
                <span
                  key={segment.start}
                  data-offset={segment.anchor ? segment.start : undefined}
                  className={segment.find ? "hit-find" : undefined}
                >
                  {segment.text}
                </span>
              );
            }
            const ordered = [...segment.marked].sort(
              (left, right) =>
                Number(right.active) - Number(left.active) ||
                severityRank(left.severity) - severityRank(right.severity) ||
                left.findingId.localeCompare(right.findingId),
            );
            const primary = ordered[0];
            if (!primary) {
              return <span key={segment.start}>{segment.text}</span>;
            }
            const otherIds = ordered
              .slice(1)
              .map((item) => item.findingId)
              .join(",");
            const current = ordered.find((item) => item.active) ?? null;
            const classes = [
              current ? "hit-evidence" : "hit-evidence-peer",
              segment.find ? "hit-find" : "",
            ]
              .filter(Boolean)
              .join(" ");
            const select = () => onSelectFinding(primary.findingId);
            return (
              <mark
                key={segment.start}
                data-offset={segment.anchor ? segment.start : undefined}
                data-finding-id={primary.findingId}
                data-peer-finding-ids={otherIds || undefined}
                data-severity={primary.severity ?? "unrated"}
                className={classes}
                role="button"
                tabIndex={0}
                aria-label={`定位线索：${primary.claim ?? primary.findingId}`}
                aria-current={current ? "true" : undefined}
                title={
                  ordered.length > 1
                    ? `${primary.claim ?? primary.findingId}（此处还有 ${ordered.length - 1} 条线索）`
                    : (primary.claim ?? primary.findingId)
                }
                onClick={select}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    select();
                  }
                }}
              >
                {segment.text}
              </mark>
            );
          })}
        </div>
      )}
      <DocumentSourceDetails document={document} />
    </div>
  );
}

function ContextField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

const SCENARIO_LABELS: Record<string, string> = {
  telecom_dedicated_line: "政企专线",
  cloud_services: "云服务采购",
  network_engineering: "网络工程",
  engineering_procurement: "工程采购",
  network_maintenance: "网络代维",
};

const BUSINESS_STATUS_LABELS: Record<string, string> = {
  active: "进行中",
  archived: "已归档",
};

function reportedContextValue(value: string): string | null {
  if (value === "" || value === "unspecified" || value === "未报告") return null;
  return value;
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
  const initialDocument =
    finding?.evidence
      .map((anchor) => documentForAnchor(anchor, documents))
      .find((item) => item !== null) ??
    documents[0] ??
    null;
  const [activeKey, setActiveKey] = useState(() =>
    initialDocument ? documentKey(initialDocument) : "",
  );
  const [query, setQuery] = useState("");
  const [matchIndex, setMatchIndex] = useState(0);
  const [zoom, setZoom] = useState(100);
  const [pageIndex, setPageIndex] = useState(0);
  const [compareSources, setCompareSources] = useState(false);
  const [contextOpen, setContextOpen] = useState(false);
  const [leftTab, setLeftTab] = useState<"材料" | "审查成员" | "相关案件">(
    "材料",
  );
  const [relatedCaseId, setRelatedCaseId] = useState("");
  const [relationReason, setRelationReason] = useState("");
  const docHostRef = useRef<HTMLElement>(null);
  const active =
    documents.find((document) => documentKey(document) === activeKey) ??
    documents[0] ??
    null;
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
    if (
      !finding ||
      !active ||
      finding.evidence.some(
        (anchor) => anchor.document_version_id === active.id,
      )
    )
      return;
    const source = finding.evidence
      .map((anchor) => documentForAnchor(anchor, documents))
      .find((item) => item !== null);
    if (source) setActiveKey(documentKey(source));
  }, [active, documents, finding]);

  useEffect(() => {
    setMatchIndex(0);
    setPageIndex(0);
  }, [activeKey]);

  const relatedFindings =
    active === null
      ? []
      : findings.filter((item) =>
          item.evidence.some(
            (anchor) => anchor.document_version_id === active.id,
          ),
        );
  const activeIsEvidence =
    active !== null &&
    evidenceDocs.some((item) => documentKey(item) === documentKey(active));
  const split =
    compareSources &&
    layout === "split" &&
    evidenceDocs.length >= 2 &&
    activeIsEvidence;
  const matches = useMemo(
    () => (active?.text ? findTextMatches(active.text, query) : []),
    [active, query],
  );
  const currentMatch = matches[matchIndex] ?? null;
  const pages = active ? reportedPages(active) : [];
  const currentPage =
    pages.length === 0
      ? null
      : (pages[Math.min(pageIndex, pages.length - 1)] ?? null);

  useEffect(() => {
    if (!finding || !active) return;
    const mark = docHostRef.current?.querySelector(
      '[data-active-source="true"] .hit-evidence',
    );
    if (mark instanceof HTMLElement) mark.scrollIntoView({ block: "nearest" });
  }, [finding?.id, active?.id, active?.parsed_document?.id]);

  function focusOffset(offset: number) {
    const node = docHostRef.current?.querySelector(
      `[data-offset="${offset}"]`,
    );
    if (node instanceof HTMLElement) node.scrollIntoView({ block: "center" });
  }

  function moveMatch(delta: number) {
    if (matches.length === 0) return;
    const next = (matchIndex + delta + matches.length) % matches.length;
    const range = matches[next];
    setMatchIndex(next);
    if (range) focusOffset(range.start);
  }

  function movePage(delta: number) {
    if (pages.length === 0 || active === null) return;
    const next = Math.min(
      pages.length - 1,
      Math.max(0, pageIndex + delta),
    );
    setPageIndex(next);
    const page = pages[next];
    if (page !== undefined) {
      const offset = firstClauseOffsetForPage(active, page);
      if (offset !== null) focusOffset(offset);
    }
  }

  function submitRelation(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (
      !onCreateRelation ||
      relatedCaseId === "" ||
      relationReason.trim() === ""
    )
      return;
    void onCreateRelation({
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

  return (
    <>
      <section className="col-doc review-doc" aria-label="原文" ref={docHostRef}>
        <div className="review-doc-card">
          <header className="review-doc-toolbar">
            <div className="review-doc-toolbar__head">
              <div className="review-doc-title">
                <h2>合同原文</h2>
                <span className="meta">
                  {documents.length === 0
                    ? "本次运行没有材料"
                    : `${documents.length} 份材料 · 仅显示已冻结的解析正文`}
                </span>
              </div>
              {caseContext ? (
                <Button
                  icon={<AimOutlined />}
                  htmlType="button"
                  aria-label="案件上下文"
                  onClick={() => setContextOpen(true)}
                >
                  案件上下文
                </Button>
              ) : null}
            </div>
            {documents.length > 0 ? (
              <>
                <div className="review-doc-toolbar__row">
                  <Select
                    className="review-doc-material"
                    aria-label="材料"
                    value={active ? documentKey(active) : undefined}
                    onChange={(value) => setActiveKey(value)}
                    options={documents.map((item) => ({
                      value: documentKey(item),
                      label: `${documentLabel(item, documents)} · ${item.filename}`,
                    }))}
                  />
                  <div
                    className="review-doc-control"
                    role="group"
                    aria-label="页码导航"
                  >
                    <Button
                      icon={<LeftOutlined />}
                      htmlType="button"
                      aria-label="上一页"
                      disabled={currentPage === null || pageIndex <= 0}
                      onClick={() => movePage(-1)}
                    />
                    <span className="review-doc-control__value">
                      {currentPage === null
                        ? "页码未报告"
                        : `第 ${currentPage} 页`}
                    </span>
                    <Button
                      icon={<RightOutlined />}
                      htmlType="button"
                      aria-label="下一页"
                      disabled={
                        currentPage === null || pageIndex >= pages.length - 1
                      }
                      onClick={() => movePage(1)}
                    />
                  </div>
                  <div
                    className="review-doc-control"
                    role="group"
                    aria-label="原文缩放"
                  >
                    <Button
                      icon={<ZoomOutOutlined />}
                      htmlType="button"
                      aria-label="缩小原文"
                      disabled={zoom <= 80}
                      onClick={() => setZoom((value) => Math.max(80, value - 10))}
                    />
                    <span className="review-doc-control__value">{zoom}%</span>
                    <Button
                      icon={<ZoomInOutlined />}
                      htmlType="button"
                      aria-label="放大原文"
                      disabled={zoom >= 150}
                      onClick={() =>
                        setZoom((value) => Math.min(150, value + 10))
                      }
                    />
                  </div>
                  {active && active.clauses.length > 0 ? (
                    <Select
                      className="review-doc-clause"
                      aria-label="定位条款"
                      placeholder="定位条款"
                      value={undefined}
                      showSearch
                      onChange={(value) => {
                        const offset = Number(value);
                        if (Number.isFinite(offset)) focusOffset(offset);
                      }}
                      options={active.clauses.map((clause) => ({
                        value: clause.text_offset.start,
                        label: `${clause.page !== null ? `第 ${clause.page} 页 · ` : ""}${clause.heading}`,
                      }))}
                    />
                  ) : null}
                </div>
                <div className="review-doc-toolbar__row">
                  <Input
                    className="review-doc-search"
                    aria-label="在原文中搜索"
                    placeholder="搜索原文或条款"
                    value={query}
                    allowClear
                    prefix={<SearchOutlined />}
                    onChange={(event) => {
                      setQuery(event.target.value);
                      setMatchIndex(0);
                    }}
                  />
                  {query.trim() === "" ? null : (
                    <span className="review-doc-match meta" aria-live="polite">
                      {matches.length === 0
                        ? "没有匹配"
                        : `${matchIndex + 1} / ${matches.length}`}
                    </span>
                  )}
                  <Button
                    icon={<UpOutlined />}
                    title="上一处匹配"
                    htmlType="button"
                    aria-label="上一处匹配"
                    disabled={matches.length === 0}
                    onClick={() => moveMatch(-1)}
                  />
                  <Button
                    icon={<DownOutlined />}
                    title="下一处匹配"
                    htmlType="button"
                    aria-label="下一处匹配"
                    disabled={matches.length === 0}
                    onClick={() => moveMatch(1)}
                  />
                </div>
              </>
            ) : null}
          </header>

          {documents.length === 0 ? (
            <p className="review-doc-empty">这次运行没有带上材料。</p>
          ) : null}
          {evidenceDocs.length >= 2 ? (
            <div
              className="source-switch review-doc-sources"
              role="group"
              aria-label="证据来源"
            >
              <span className="meta">证据来源</span>
              {evidenceDocs.map((item) => (
                <Button
                  aria-label={String(documentLabel(item, documents))}
                  key={documentKey(item)}
                  htmlType="button"
                  aria-pressed={
                    active !== null && documentKey(active) === documentKey(item)
                  }
                  data-document-version-id={item.id}
                  data-parsed-document-id={item.parsed_document?.id ?? ""}
                  onClick={() => setActiveKey(documentKey(item))}
                >
                  {documentLabel(item, documents)}
                </Button>
              ))}
              {layout === "split" && activeIsEvidence ? (
                <div
                  className="review-doc-compare"
                  role="group"
                  aria-label="正文显示模式"
                >
                  <span className="review-doc-compare__separator" aria-hidden="true" />
                  <Button
                    aria-label="单篇阅读"
                    htmlType="button"
                    aria-pressed={!compareSources}
                    onClick={() => setCompareSources(false)}
                  >
                    单篇阅读
                  </Button>
                  <Button
                    aria-label="双来源对照"
                    htmlType="button"
                    aria-pressed={compareSources}
                    onClick={() => setCompareSources(true)}
                  >
                    双来源对照
                  </Button>
                </div>
              ) : null}
            </div>
          ) : null}

          <div
            className="review-doc-paper"
            style={{ "--doc-zoom": zoom / 100 } as CSSProperties}
          >
            {split ? (
              <div className="document-split">
                {evidenceDocs.map((item) => (
                  <DocumentBody
                    key={documentKey(item)}
                    document={item}
                    findings={findings}
                    selectedFindingId={selectedFindingId}
                    onSelectFinding={onSelectFinding}
                    findRange={item === active ? currentMatch : null}
                    activeSource={item === active}
                  />
                ))}
              </div>
            ) : active ? (
              <DocumentBody
                document={active}
                findings={findings}
                selectedFindingId={selectedFindingId}
                onSelectFinding={onSelectFinding}
                findRange={currentMatch}
                activeSource
              />
            ) : null}
          </div>
        </div>
      </section>

      {caseContext ? (
        <Drawer
          title="案件上下文"
          size={460}
          open={contextOpen}
          onClose={() => setContextOpen(false)}
          className="review-context-drawer"
        >
          <header className="review-case-context-head">
            <div>
              <h2>{caseContext.title}</h2>
              <span>{caseContext.number ?? caseContext.caseId}</span>
            </div>
            <Link
              className="review-case-open"
              to={`/cases/${encodeURIComponent(caseContext.caseId)}`}
            >
              案件详情
            </Link>
          </header>
          <dl className="review-case-context-meta">
            <ContextField label="客户">
              {caseContext.customer || "未设置"}
            </ContextField>
            <ContextField label="供应商">
              {caseContext.supplier || "未设置"}
            </ContextField>
            <ContextField label="合同类型">
              {caseContext.contractType || "未报告"}
            </ContextField>
            <ContextField label="业务线 / 场景">
              {[
                reportedContextValue(caseContext.businessLine),
                SCENARIO_LABELS[caseContext.scenario] ??
                  reportedContextValue(caseContext.scenario),
              ]
                .filter(Boolean)
                .join(" / ") || "未设置"}
            </ContextField>
            <ContextField label="负责人">
              {caseContext.ownerName || caseContext.ownerId || "未分配"}
            </ContextField>
            <ContextField label="期限 / 状态">
              {caseContext.dueAt
                ? formatDateTime(caseContext.dueAt)
                : "未设期限"}{" "}
              ·{" "}
              {BUSINESS_STATUS_LABELS[caseContext.status] ??
                reportedContextValue(caseContext.status) ??
                "未报告"}
            </ContextField>
          </dl>
          <Tabs
            activeKey={leftTab}
            onChange={(key) => {
              const next = (["材料", "审查成员", "相关案件"] as const).find(
                (label) => label === key,
              );
              if (next) setLeftTab(next);
            }}
            items={[
              { key: "材料", label: "材料" },
              {
                key: "审查成员",
                label: `审查成员 ${caseContext.collaborators.length}`,
              },
              {
                key: "相关案件",
                label: `相关案件 ${caseContext.relatedCases.length}`,
              },
            ]}
          />
          {leftTab === "材料" ? (
            <section
              className="review-material-context"
              aria-label="本次运行材料"
            >
              <h3>材料与关联线索</h3>
              {documents.length === 0 ? (
                <p className="meta">本次运行没有带上材料。</p>
              ) : (
                <ul className="review-material-list">
                  {documents.map((item) => (
                    <li key={documentKey(item)}>
                      <button
                        type="button"
                        className="review-material-item"
                        aria-pressed={
                          active !== null &&
                          documentKey(active) === documentKey(item)
                        }
                        onClick={() => setActiveKey(documentKey(item))}
                      >
                        <strong>{item.filename}</strong>
                        <span className="meta">
                          {roleLabel(item.role)} ·{" "}
                          {item.clauses.length > 0
                            ? `${item.clauses.length} 个条款`
                            : "条款未报告"}
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              <h3>引用该材料的线索</h3>
              {relatedFindings.length === 0 ? (
                <p className="meta">当前材料没有关联到本次运行的线索。</p>
              ) : (
                <ul className="review-link-list">
                  {relatedFindings.map((item) => (
                    <li key={item.id}>
                      <Button
                        aria-label={
                          "查看线索：" + String(item.claim || item.id)
                        }
                        htmlType="button"
                        aria-pressed={selectedFindingId === item.id}
                        onClick={() => onSelectFinding(item.id)}
                      >
                        <span>{item.claim || item.id}</span>
                        <RiskTag severity={item.severity} />
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          ) : null}
          {leftTab === "审查成员" ? (
            <section className="review-collaborators" aria-label="案件协作人员">
              <h3>审查成员</h3>
              <p className="meta">当前案件已授权的成员及实际分工。</p>
              {caseContext.collaborators.length === 0 ? (
                <p className="meta">当前没有可显示的案件成员。</p>
              ) : (
                <ul>
                  {caseContext.collaborators.map((member) => (
                    <li key={member.id}>
                      <strong>{member.name}</strong>
                      <span>{actorRoleLabel(member.role)}</span>
                      <small>{member.responsibilities.join(" · ")}</small>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          ) : null}
          {leftTab === "相关案件" ? (
            <section className="review-related-cases" aria-label="关联案件">
              <h3>
                相关案件 <span>{caseContext.relatedCases.length}</span>
              </h3>
              {caseContext.relatedCases.length === 0 ? (
                <p className="meta">当前案件没有已保存的关系。</p>
              ) : (
                <ul>
                  {caseContext.relatedCases.map((relation) => (
                    <li key={relation.relationId}>
                      <div>
                        <Link
                          to={
                            relation.runId
                              ? `/cases/${encodeURIComponent(relation.id)}/reviews/${encodeURIComponent(relation.runId)}`
                              : `/cases/${encodeURIComponent(relation.id)}`
                          }
                        >
                          {relation.title}
                        </Link>
                        <small>
                          {relation.id} · {formatDateTime(relation.createdAt)}
                        </small>
                        <p>{relation.reason}</p>
                      </div>
                      {canEditRelations && onDeleteRelation ? (
                        <Button
                          aria-label="解除关联"
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
                caseContext.availableCases.length > 0 ? (
                  <form
                    className="review-relation-form"
                    onSubmit={submitRelation}
                  >
                    <label>
                      关联可见案件
                      <Select
                        aria-label="关联可见案件"
                        value={relatedCaseId || undefined}
                        onChange={setRelatedCaseId}
                        options={[
                          { label: "选择案件", value: "" },
                          ...caseContext.availableCases.map((item) => ({
                            label: `${item.title} · ${item.id}`,
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
                        onChange={(event) =>
                          setRelationReason(event.target.value)
                        }
                        placeholder="填写业务关联依据"
                      />
                    </label>
                    {relationError ? <p role="alert">{relationError}</p> : null}
                    <Button
                      aria-label={String(
                        relationPending ? "正在保存" : "建立关联",
                      )}
                      type="primary"
                      htmlType="submit"
                      disabled={
                        relationPending ||
                        relatedCaseId === "" ||
                        relationReason.trim() === ""
                      }
                    >
                      {relationPending ? "正在保存" : "建立关联"}
                    </Button>
                  </form>
                ) : (
                  <p className="meta">当前身份可见范围内没有可新增的案件。</p>
                )
              ) : (
                <p className="meta">当前案件关系为只读。</p>
              )}
            </section>
          ) : null}
        </Drawer>
      ) : null}
    </>
  );
}
