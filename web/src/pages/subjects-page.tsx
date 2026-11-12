// File metadata (manifest mirror):
// Purpose: Lists subjects visible to the case and opens case-scoped subject details, verification and binding history; case roles and permissions come from server projections.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-08
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import {
  ApartmentOutlined,
  ArrowLeftOutlined,
  ArrowRightOutlined,
  FileSearchOutlined,
  LeftOutlined,
  PlusOutlined,
  RightOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Alert, Button, Card, Empty, Form, Input, Table } from "antd";
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router";
import type { ColumnsType } from "antd/es/table";
import type { SubjectCreateFormValues } from "../components/subject-create-fields";
import { SubjectCreateFields, subjectCreateInput } from "../components/subject-create-fields";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import { errorMessage } from "../api/http";
import {
  useCreateSubject,
  useSubject,
  useSubjectCaseLinks,
  useSubjectHistory,
  useSubjects,
  useSession,
} from "../api/queries";
import type { SubjectSummary } from "../api/types";
import { businessStatusLabel, businessStatusTone, formatDateTime } from "../domain/review";
import {
  subjectBindingStateLabel,
  subjectRoleLabel,
  subjectVerificationLabel,
} from "../domain/subjects";
import publicDirectory from "../domain/subject-public-directory.json";
import {
  clearPendingMutationKey,
  idempotencyActorScope,
  pendingMutationKey,
} from "../api/idempotency";
import "./subjects-page.css";

const detailSections = {
  overview: "主体概览",
  source: "登记来源",
  cases: "关联案件",
  history: "更正历史",
} as const;
type DetailSection = keyof typeof detailSections;

const aliasLabels = {
  legal: "法定名称",
  trade: "简称",
  former: "曾用名",
  other: "其他名称",
} as const;

function subjectSourceLabel(source: string): string {
  return source === "authorized_correction"
    ? "经更正审批"
    : source === "official_identifier"
      ? "受控来源"
      : "经办人录入";
}

function verificationTone(
  state: SubjectSummary["verification_state"],
): "good" | "attention" | "neutral" {
  if (state === "verified") return "good";
  if (state === "pending_verification") return "attention";
  return "neutral";
}

export function SubjectsPage() {
  const session = useSession();
  const [searchInput, setSearchInput] = useState("");
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState<string | null>(null);
  const [cursorStack, setCursorStack] = useState<Array<string | null>>([]);
  const [showCreate, setShowCreate] = useState(false);
  const [form] = Form.useForm<SubjectCreateFormValues>();
  const [formError, setFormError] = useState<string | null>(null);
  const [formNote, setFormNote] = useState<string | null>(null);
  const [created, setCreated] = useState<SubjectSummary | null>(null);
  const create = useCreateSubject();
  const subjects = useSubjects(query, cursor);
  const items = subjects.data?.items ?? [];

  function search(value: string) {
    setQuery(value.trim());
    setCursor(null);
    setCursorStack([]);
  }

  async function onCreate(values: SubjectCreateFormValues) {
    const actor = session.data;
    if (!actor) {
      setFormError("登录身份暂不可用，请刷新后重试。");
      return;
    }
    setFormError(null);
    setFormNote(null);
    try {
      const input = subjectCreateInput(values);
      const pending = await pendingMutationKey(
        "subject.create",
        idempotencyActorScope(actor),
        "unbound",
        input,
      );
      const result = await create.mutateAsync({ subject: input, idempotencyKey: pending.key });
      setCreated(result);
      form.resetFields();
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        setFormNote(
          "主体已登记；浏览器未能清理重试标识。请勿重复提交这次内容，如需重试请联系管理员。",
        );
      }
    } catch (error) {
      setFormError(errorMessage(error));
    }
  }

  const columns: ColumnsType<SubjectSummary> = [
    {
      title: "主体名称",
      dataIndex: "display_name",
      key: "display_name",
      render: (_value, subject) => (
        <div className="subject-ledger-name">
          <Link to={`/subjects/${encodeURIComponent(subject.subject_id)}`}>
            {subject.display_name}
          </Link>
          {subject.legal_name && subject.legal_name !== subject.display_name ? (
            <span>{subject.legal_name}</span>
          ) : null}
          {subject.aliases.length ? (
            <span>{subject.aliases.map((alias) => alias.value).join(" · ")}</span>
          ) : null}
        </div>
      ),
    },
    {
      title: "统一社会信用代码",
      key: "identifiers",
      width: 190,
      render: (_value, subject) =>
        subject.identifiers.length ? (
          <span className="subject-identifier">
            {subject.identifiers.map((item) => item.masked_value).join("、")}
          </span>
        ) : (
          <span className="subject-muted">暂未登记</span>
        ),
    },
    {
      title: "核验状态",
      key: "verification_state",
      width: 160,
      render: (_value, subject) => (
        <StatusText tone={verificationTone(subject.verification_state)}>
          {subjectVerificationLabel(subject.verification_state, subject.verification_scope)}
        </StatusText>
      ),
    },
    {
      title: "身份版本",
      dataIndex: "current_version",
      key: "current_version",
      width: 88,
      render: (value: number) => <span className="subject-version">v{value}</span>,
    },
    {
      title: "登记时间",
      dataIndex: "created_at",
      key: "created_at",
      width: 170,
      render: (value: string) => <span className="subject-muted">{formatDateTime(value)}</span>,
    },
    {
      title: "操作",
      key: "action",
      width: 88,
      render: (_value, subject) => (
        <Link
          className="subject-view-link"
          aria-label={`查看${subject.display_name}详情`}
          to={`/subjects/${encodeURIComponent(subject.subject_id)}`}
        >
          详情 <ArrowRightOutlined aria-hidden />
        </Link>
      ),
    },
  ];

  return (
    <section className="page subjects-page">
      <header className="page-head">
        <div>
          <h1>主体台账</h1>
          <p>管理合同往来主体，查看身份信息、核验状态与关联案件。</p>
        </div>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => {
            setShowCreate((visible) => !visible);
            setFormError(null);
          }}
        >
          {showCreate ? "收起新建" : "新建待核验主体"}
        </Button>
      </header>

      <dl className="subject-ledger-summary" aria-label="本页主体概况">
        <div>
          <dt>本页主体</dt>
          <dd>
            {subjects.data ? items.length : "—"}
            <span>家</span>
          </dd>
        </div>
        <div>
          <dt>
            <span className="subject-status-dot subject-status-dot-pending" />
            本页待核验
          </dt>
          <dd>
            {subjects.data
              ? items.filter((item) => item.verification_state === "pending_verification").length
              : "—"}
            <span>家</span>
          </dd>
        </div>
        <div>
          <dt>
            <span className="subject-status-dot subject-status-dot-verified" />
            本页已核验
          </dt>
          <dd>
            {subjects.data
              ? items.filter((item) => item.verification_state === "verified").length
              : "—"}
            <span>家</span>
          </dd>
        </div>
      </dl>

      {showCreate ? (
        <Card title="新建主体" className="subject-create-card">
          <Form form={form} layout="vertical" onFinish={(values) => void onCreate(values)}>
            <SubjectCreateFields />
            {formError ? <Alert type="error" showIcon title={formError} /> : null}
            {formNote ? <Alert type="warning" showIcon title={formNote} /> : null}
            {created ? (
              <Alert
                type="success"
                showIcon
                title="主体已登记，当前状态为待核验。"
                description={
                  <Link to={`/subjects/${encodeURIComponent(created.subject_id)}`}>
                    查看主体详情
                  </Link>
                }
              />
            ) : null}
            <Form.Item>
              <Button
                type="primary"
                htmlType="submit"
                loading={create.isPending}
                disabled={!session.data}
              >
                登记主体
              </Button>
            </Form.Item>
          </Form>
        </Card>
      ) : null}

      <Card className="subject-ledger-card" styles={{ body: { padding: 0 } }}>
        <div className="subject-ledger-toolbar">
          <div>
            <h2>主体目录</h2>
            <p>仅展示当前账号有权查看的主体</p>
          </div>
          <form
            className="subject-ledger-search"
            role="search"
            aria-label="搜索主体"
            onSubmit={(event) => {
              event.preventDefault();
              search(searchInput);
            }}
          >
            <Input
              aria-label="搜索主体"
              placeholder="搜索主体名称或别名"
              prefix={<SearchOutlined aria-hidden />}
              value={searchInput}
              onChange={(event) => setSearchInput(event.target.value)}
              onClear={() => search("")}
              allowClear
            />
            <Button htmlType="submit" type="primary">
              查询
            </Button>
            {query ? (
              <Button
                onClick={() => {
                  setSearchInput("");
                  search("");
                }}
              >
                重置
              </Button>
            ) : null}
          </form>
        </div>
        {query ? (
          <p className="subject-search-result" role="status">
            搜索“{query}”的结果
          </p>
        ) : null}
        {subjects.isError ? (
          <Alert
            type="error"
            showIcon
            title="主体列表暂不可用"
            description={errorMessage(subjects.error)}
            action={<Button onClick={() => void subjects.refetch()}>重试</Button>}
          />
        ) : null}
        <Table<SubjectSummary>
          rowKey="subject_id"
          className="subject-ledger-table"
          columns={columns}
          dataSource={items}
          loading={subjects.isLoading || subjects.isFetching}
          pagination={false}
          scroll={{ x: 1000 }}
          locale={{
            emptyText: <Empty description={query ? "没有可见的匹配主体" : "当前没有可见主体"} />,
          }}
        />
        {subjects.data ? (
          <nav className="subject-ledger-pagination" aria-label="主体列表分页">
            <span className="subject-muted">本页 {items.length} 项</span>
            <div className="subject-pagination-controls">
              <Button
                icon={<LeftOutlined aria-hidden />}
                disabled={cursorStack.length === 0 || subjects.isFetching}
                onClick={() => {
                  const next = [...cursorStack];
                  setCursor(next.pop() ?? null);
                  setCursorStack(next);
                }}
              >
                上一页
              </Button>
              <span className="subject-page-number" aria-current="page">
                第 {cursorStack.length + 1} 页
              </span>
              <Button
                icon={<RightOutlined aria-hidden />}
                iconPlacement="end"
                disabled={subjects.data.next_cursor === null || subjects.isFetching}
                onClick={() => {
                  if (
                    subjects.data?.next_cursor === null ||
                    subjects.data?.next_cursor === undefined
                  )
                    return;
                  setCursorStack((stack) => [...stack, cursor]);
                  setCursor(subjects.data.next_cursor);
                }}
              >
                下一页
              </Button>
            </div>
          </nav>
        ) : null}
      </Card>
    </section>
  );
}

export function SubjectDetailPage() {
  const { subjectId = "" } = useParams();
  const [activeSection, setActiveSection] = useState<DetailSection>("overview");
  const session = useSession();
  const scope = [
    subjectId,
    session.data?.id ?? "",
    session.data?.tenant_id ?? "",
    session.data?.org_id ?? "",
    session.data?.audience ?? "",
  ].join(":");
  const [linkPage, setLinkPage] = useState<{
    scope: string;
    cursor: string | null;
    stack: Array<string | null>;
  }>({ scope: "", cursor: null, stack: [] });
  const linkCursor = linkPage.scope === scope ? linkPage.cursor : null;
  const subject = useSubject(subjectId);
  const links = useSubjectCaseLinks(subjectId, linkCursor);
  const history = useSubjectHistory(subjectId);
  useEffect(() => {
    if (linkPage.scope !== scope) setLinkPage({ scope, cursor: null, stack: [] });
  }, [linkPage.scope, scope]);
  if (subject.isLoading) return <PageStatus kind="loading" title="正在读取主体" />;
  if (subject.isError)
    return (
      <PageStatus
        kind="error"
        title="主体详情暂不可用"
        detail={errorMessage(subject.error)}
        action={<Button onClick={() => void subject.refetch()}>重试</Button>}
      />
    );
  if (!subject.data) return <PageStatus kind="empty" title="没有主体详情" />;
  const current = subject.data.current;
  const reference = publicDirectory.find(
    (item) => item.name === (current.legal_name || current.display_name),
  );
  const caseColumns: ColumnsType<NonNullable<typeof links.data>["items"][number]> = [
    {
      title: "案件名称",
      key: "case_title",
      render: (_value, item) => (
        <Link to={`/cases/${encodeURIComponent(item.case_id)}`}>{item.case_title}</Link>
      ),
    },
    {
      title: "案件角色",
      key: "role",
      width: 150,
      render: (_value, item) => (
        <div className="subject-ledger-name">
          <span>{subjectRoleLabel(item.role)}</span>
          <span>{subjectBindingStateLabel(item.binding_state)}</span>
        </div>
      ),
    },
    {
      title: "业务状态",
      key: "business_status",
      width: 150,
      render: (_value, item) => (
        <StatusText tone={businessStatusTone(item.business_status)}>
          {businessStatusLabel(item.business_status)}
        </StatusText>
      ),
    },
    {
      title: "引用版本",
      key: "version",
      width: 180,
      render: (_value, item) => (
        <span className="subject-muted">
          绑定 v{item.binding_version} · 身份 v{item.subject.version}
        </span>
      ),
    },
  ];
  return (
    <section className="page subject-detail-page">
      <nav className="page-back subject-back" aria-label="主体导航">
        <Link to="/subjects">
          <ArrowLeftOutlined aria-hidden />
          返回主体台账
        </Link>
      </nav>
      <header className="subject-identity-header">
        <div className="subject-identity-mark" aria-hidden>
          <ApartmentOutlined />
        </div>
        <div className="subject-identity-title">
          <div className="subject-identity-heading">
            <h1>{current.display_name}</h1>
            <StatusText tone={verificationTone(current.verification_state)}>
              {subjectVerificationLabel(current.verification_state, current.verification_scope)}
            </StatusText>
          </div>
          {current.legal_name && current.legal_name !== current.display_name ? (
            <p>{current.legal_name}</p>
          ) : null}
          <div className="subject-identity-meta">
            <span>
              主体编号 <code>{subject.data.subject_id}</code>
            </span>
            <span>
              当前身份版本 <strong>v{subject.data.current_version}</strong>
            </span>
          </div>
        </div>
      </header>
      <nav className="subject-detail-sections" aria-label="主体详情分区">
        {(Object.entries(detailSections) as Array<[DetailSection, string]>).map(([key, label]) => (
          <button
            key={key}
            type="button"
            aria-pressed={activeSection === key}
            aria-controls={`subject-section-${key}`}
            onClick={() => setActiveSection(key)}
          >
            {label}
          </button>
        ))}
      </nav>
      <section
        id="subject-section-overview"
        aria-label="主体概览"
        hidden={activeSection !== "overview"}
      >
        <div className="subject-overview-layout">
          <Card title="主体基本信息" className="subject-detail-card">
            <dl className="subject-detail-list">
              <div className="subject-detail-field-wide">
                <dt>法定名称</dt>
                <dd>{current.legal_name || <span className="subject-muted">暂未登记</span>}</dd>
              </div>
              <div className="subject-detail-field-wide">
                <dt>别名</dt>
                <dd>
                  {current.aliases.length ? (
                    <div className="subject-aliases">
                      {current.aliases.map((item, index) => (
                        <span key={`${item.kind}:${index}`}>
                          {item.value}
                          <small>{aliasLabels[item.kind]}</small>
                        </span>
                      ))}
                    </div>
                  ) : (
                    <span className="subject-muted">暂未登记</span>
                  )}
                </dd>
              </div>
              <div className="subject-detail-field-wide">
                <dt>标识信息</dt>
                <dd>
                  {subject.data.identifiers.length ? (
                    <div className="subject-identifiers">
                      {subject.data.identifiers.map((item, index) => (
                        <span key={index}>
                          <small>统一社会信用代码</small>
                          <code>{item.masked_value}</code>
                        </span>
                      ))}
                    </div>
                  ) : (
                    <span className="subject-muted">暂未登记统一社会信用代码</span>
                  )}
                </dd>
              </div>
              <div>
                <dt>登记方式</dt>
                <dd>{subjectSourceLabel(current.source_kind)}</dd>
              </div>
              <div>
                <dt>当前版本登记时间</dt>
                <dd>{formatDateTime(current.created_at)}</dd>
              </div>
            </dl>
          </Card>
          <aside className="subject-overview-aside" aria-label="主体核验与参考">
            <Card title="核验说明" className="subject-verification-card">
              <div className="subject-aside-icon" aria-hidden>
                <FileSearchOutlined />
              </div>
              <h3>
                {current.verification_state === "verified"
                  ? "查看核验依据与适用范围"
                  : "身份信息尚待核验"}
              </h3>
              <p>
                {current.verification_state === "verified"
                  ? "核验结果以登记依据和核验范围为准，可在登记来源中查看说明。"
                  : "当前信息由经办人提交，名称或代码已登记不代表主体真实性已确认。"}
              </p>
              <Button onClick={() => setActiveSection("source")}>
                查看登记来源 <ArrowRightOutlined aria-hidden />
              </Button>
            </Card>
            {reference ? (
              <Card title="公开参考资料" className="subject-reference-card">
                <span className="subject-reference-category">{reference.industry}</span>
                <p>{reference.summary}</p>
                <a href={reference.source_url} target="_blank" rel="noreferrer">
                  查看公开来源 <ArrowRightOutlined aria-hidden />
                </a>
                <small>资料检索于 {reference.retrieved_at}，供核验参考。</small>
              </Card>
            ) : null}
            <p className="subject-detail-note">
              主体身份与案件角色分别管理；身份更正保留历史版本与审查引用。
            </p>
          </aside>
        </div>
      </section>
      <section
        id="subject-section-source"
        aria-label="登记来源"
        hidden={activeSection !== "source"}
      >
        <Card title="登记来源" className="subject-detail-card">
          <dl className="subject-detail-list">
            <div>
              <dt>身份来源</dt>
              <dd>{subjectSourceLabel(current.source_kind)}</dd>
            </div>
            <div>
              <dt>登记时间</dt>
              <dd>{formatDateTime(current.created_at)}</dd>
            </div>
            <div className="subject-detail-field-wide">
              <dt>依据说明</dt>
              <dd>{current.basis_summary}</dd>
            </div>
          </dl>
        </Card>
        {reference ? (
          <Card title="公开参考资料" className="subject-source-reference-card">
            <p>{reference.summary}</p>
            <dl className="subject-detail-list">
              <div className="subject-detail-field-wide">
                <dt>参考来源</dt>
                <dd>
                  <a href={reference.source_url} target="_blank" rel="noreferrer">
                    {reference.source_title} <ArrowRightOutlined aria-hidden />
                  </a>
                </dd>
              </div>
              <div>
                <dt>检索日期</dt>
                <dd>{reference.retrieved_at}</dd>
              </div>
              <div>
                <dt>匹配方式</dt>
                <dd>按主体名称匹配，尚需核对身份</dd>
              </div>
            </dl>
            <p className="subject-detail-note">
              公开网页仅供核验参考，不替代受控来源核验或案件证据。
            </p>
          </Card>
        ) : null}
      </section>
      <section
        id="subject-section-history"
        aria-label="更正历史"
        hidden={activeSection !== "history"}
      >
        <Card title="身份版本与操作记录" className="subject-history-card">
          {history.isLoading ? <p>正在读取身份历史…</p> : null}
          {history.isError ? (
            <Alert
              type="error"
              showIcon
              title="身份历史暂不可用"
              description={errorMessage(history.error)}
              action={<Button onClick={() => void history.refetch()}>重试</Button>}
            />
          ) : null}
          {history.data?.versions.map((item) => (
            <div className="subject-history-row" key={`version:${item.version}`}>
              <span className="subject-history-version">v{item.version}</span>
              <div className="subject-history-content">
                <div>
                  <strong>身份 v{item.version}</strong>
                  {item.version === subject.data.current_version ? (
                    <span className="subject-version">当前版本</span>
                  ) : null}
                </div>
                <p>{item.display_name}</p>
                <span className="subject-muted">
                  {subjectVerificationLabel(item.verification_state, item.verification_scope)}
                </span>
              </div>
              <time dateTime={item.created_at}>{formatDateTime(item.created_at)}</time>
            </div>
          ))}
          {history.data?.events.map((event) => (
            <div className="subject-history-row" key={event.event_id}>
              <span className="subject-history-event-dot" aria-hidden />
              <div className="subject-history-content">
                <span>
                  {event.kind === "proposal_created"
                    ? "发起更正申请"
                    : event.kind === "decision_approved"
                      ? "更正已批准"
                      : event.kind === "decision_rejected"
                        ? "更正被驳回"
                        : "更正已生效"}
                </span>
                <Link
                  to={`/cases/${encodeURIComponent(event.case_id)}/subject-changes/${encodeURIComponent(event.request_id)}`}
                >
                  查看有权访问的案件记录
                </Link>
              </div>
              <time dateTime={event.created_at}>{formatDateTime(event.created_at)}</time>
            </div>
          ))}
          {history.data &&
          history.data.versions.length === 0 &&
          history.data.events.length === 0 ? (
            <Empty description="暂无版本与操作记录" />
          ) : null}
        </Card>
      </section>
      <section id="subject-section-cases" aria-label="关联案件" hidden={activeSection !== "cases"}>
        <Card
          title="当前可见案件绑定"
          className="subject-links-card"
          styles={{ body: { padding: 0 } }}
        >
          {links.isError ? (
            <Alert
              type="error"
              showIcon
              title="案件绑定暂不可用"
              description={errorMessage(links.error)}
              action={<Button onClick={() => void links.refetch()}>重试</Button>}
            />
          ) : null}
          <Table
            rowKey="binding_id"
            columns={caseColumns}
            dataSource={links.data?.items ?? []}
            loading={links.isLoading || links.isFetching}
            pagination={false}
            scroll={{ x: 780 }}
            locale={{ emptyText: <Empty description="没有当前可见的案件绑定" /> }}
          />
          {links.data ? (
            <nav className="subject-ledger-pagination" aria-label="关联案件分页">
              <span className="subject-muted">本页 {links.data.visible_count} 条当前可见绑定</span>
              <div className="subject-pagination-controls">
                <Button
                  icon={<LeftOutlined aria-hidden />}
                  disabled={
                    linkPage.scope !== scope || linkPage.stack.length === 0 || links.isFetching
                  }
                  onClick={() => {
                    const stack = [...linkPage.stack];
                    const cursor = stack.pop() ?? null;
                    setLinkPage({ scope, cursor, stack });
                  }}
                >
                  上一页
                </Button>
                <span className="subject-page-number" aria-current="page">
                  第 {linkPage.scope === scope ? linkPage.stack.length + 1 : 1} 页
                </span>
                <Button
                  icon={<RightOutlined aria-hidden />}
                  iconPlacement="end"
                  disabled={links.data.next_cursor === null || links.isFetching}
                  onClick={() => {
                    if (links.data?.next_cursor == null) return;
                    setLinkPage((current) => ({
                      scope,
                      cursor: links.data!.next_cursor,
                      stack: [...(current.scope === scope ? current.stack : []), linkCursor],
                    }));
                  }}
                >
                  下一页
                </Button>
              </div>
            </nav>
          ) : null}
        </Card>
      </section>
    </section>
  );
}
