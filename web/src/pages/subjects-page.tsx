import { Alert, Button, Card, Empty, Form, Input, Space, Table, Typography } from "antd";
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router";
import type { ColumnsType } from "antd/es/table";
import type { SubjectCreateFormValues } from "../components/subject-create-fields";
import { SubjectCreateFields, subjectCreateInput } from "../components/subject-create-fields";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import { errorMessage } from "../api/http";
import { useCreateSubject, useSubject, useSubjectCaseLinks, useSubjectHistory, useSubjects, useSession } from "../api/queries";
import type { SubjectSummary } from "../api/types";
import { formatDateTime } from "../domain/review";
import { subjectVerificationLabel } from "../domain/subjects";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";

function verificationTone(state: SubjectSummary["verification_state"]): "good" | "attention" | "neutral" {
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
      const pending = await pendingMutationKey("subject.create", idempotencyActorScope(actor), "unbound", input);
      const result = await create.mutateAsync({ subject: input, idempotencyKey: pending.key });
      setCreated(result);
      form.resetFields();
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        setFormNote("主体已登记；浏览器未能清理重试标识。请勿重复提交这次内容，如需重试请联系管理员。");
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
      render: (_value, subject) => <Link to={`/subjects/${encodeURIComponent(subject.subject_id)}`}>{subject.display_name}</Link>,
    },
    { title: "法定名称", dataIndex: "legal_name", key: "legal_name", render: (value: string | null) => value || "—" },
    {
      title: "核验状态",
      key: "verification_state",
      render: (_value, subject) => <StatusText tone={verificationTone(subject.verification_state)}>{subjectVerificationLabel(subject.verification_state, subject.verification_scope)}</StatusText>,
    },
    { title: "版本", dataIndex: "current_version", key: "current_version", render: (value: number) => `v${value}` },
    { title: "创建时间", dataIndex: "created_at", key: "created_at", render: (value: string) => formatDateTime(value) },
  ];

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>主体台账</h1>
          <p>主体身份与案件角色分开管理。只有当前账号可见的主体会出现在列表中。</p>
        </div>
        <Button type="primary" onClick={() => { setShowCreate((visible) => !visible); setFormError(null); }}>
          {showCreate ? "收起新建" : "新建待核验主体"}
        </Button>
      </header>

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
                description={<Link to={`/subjects/${encodeURIComponent(created.subject_id)}`}>查看主体详情</Link>}
              />
            ) : null}
            <Form.Item>
              <Button type="primary" htmlType="submit" loading={create.isPending} disabled={!session.data}>
                登记主体
              </Button>
            </Form.Item>
          </Form>
        </Card>
      ) : null}

      <Card className="subject-ledger-card">
        <Form
          layout="inline"
          onFinish={() => {
            setQuery(searchInput.trim());
            setCursor(null);
            setCursorStack([]);
          }}
        >
          <Form.Item label="搜索主体">
            <Input value={searchInput} onChange={(event) => setSearchInput(event.target.value)} allowClear />
          </Form.Item>
          <Form.Item>
            <Button htmlType="submit">查询</Button>
          </Form.Item>
        </Form>
        {subjects.isError ? <Alert type="error" showIcon title="主体列表暂不可用" description={errorMessage(subjects.error)} action={<Button onClick={() => void subjects.refetch()}>重试</Button>} /> : null}
        <Table<SubjectSummary>
          rowKey="subject_id"
          className="subject-ledger-table"
          columns={columns}
          dataSource={subjects.data?.items ?? []}
          loading={subjects.isLoading || subjects.isFetching}
          pagination={false}
          locale={{ emptyText: <Empty description={query ? "没有可见的匹配主体" : "当前没有可见主体"} /> }}
        />
        {subjects.data ? (
          <div className="subject-ledger-pagination">
            <span>本页 {subjects.data.items.length} 项</span>
            <Space>
              <Button disabled={cursorStack.length === 0 || subjects.isFetching} onClick={() => {
                const next = [...cursorStack];
                setCursor(next.pop() ?? null);
                setCursorStack(next);
              }}>上一页</Button>
              <Button disabled={subjects.data.next_cursor === null || subjects.isFetching} onClick={() => {
                if (subjects.data?.next_cursor === null || subjects.data?.next_cursor === undefined) return;
                setCursorStack((stack) => [...stack, cursor]);
                setCursor(subjects.data.next_cursor);
              }}>下一页</Button>
            </Space>
          </div>
        ) : null}
      </Card>
    </section>
  );
}

export function SubjectDetailPage() {
  const { subjectId = "" } = useParams();
  const session = useSession();
  const scope = [subjectId, session.data?.id ?? "", session.data?.tenant_id ?? "", session.data?.org_id ?? "", session.data?.audience ?? ""].join(":");
  const [linkPage, setLinkPage] = useState<{ scope: string; cursor: string | null; stack: Array<string | null> }>({ scope: "", cursor: null, stack: [] });
  const linkCursor = linkPage.scope === scope ? linkPage.cursor : null;
  const subject = useSubject(subjectId);
  const links = useSubjectCaseLinks(subjectId, linkCursor);
  const history = useSubjectHistory(subjectId);
  useEffect(() => {
    if (linkPage.scope !== scope) setLinkPage({ scope, cursor: null, stack: [] });
  }, [linkPage.scope, scope]);
  if (subject.isLoading) return <PageStatus kind="loading" title="正在读取主体" />;
  if (subject.isError) return <PageStatus kind="error" title="主体详情暂不可用" detail={errorMessage(subject.error)} action={<Button onClick={() => void subject.refetch()}>重试</Button>} />;
  if (!subject.data) return <PageStatus kind="empty" title="没有主体详情" />;
  const current = subject.data.current;
  return (
    <section className="page">
      <nav className="page-back" aria-label="主体导航"><Link to="/subjects">返回主体台账</Link></nav>
      <header className="page-head">
        <div>
          <h1>{current.display_name}</h1>
          <p>
            <StatusText tone={verificationTone(current.verification_state)}>
              {subjectVerificationLabel(current.verification_state, current.verification_scope)}
            </StatusText>
            <span>当前身份版本 v{subject.data.current_version}</span>
          </p>
        </div>
      </header>
      <Card title="当前身份资料" className="subject-detail-card">
        <dl className="subject-detail-list">
          <dt>法定名称</dt><dd>{current.legal_name || "—"}</dd>
          <dt>身份来源</dt><dd>{current.source_kind === "authorized_correction" ? "经更正审批" : current.source_kind === "official_identifier" ? "受控来源" : "经办人录入"}</dd>
          <dt>依据说明</dt><dd>{current.basis_summary}</dd>
          <dt>别名</dt><dd>{current.aliases.length ? current.aliases.map((item) => `${item.value}（${item.kind}）`).join("、") : "—"}</dd>
          <dt>登记时间</dt><dd>{formatDateTime(current.created_at)}</dd>
          <dt>标识信息</dt><dd>{subject.data.identifiers.length ? subject.data.identifiers.map((item) => item.masked_value).join("、") : "—"}</dd>
        </dl>
      </Card>
      <Card title="身份版本与操作记录" className="subject-history-card">
        {history.isLoading ? <p>正在读取身份历史…</p> : null}
        {history.isError ? <Alert type="error" showIcon title="身份历史暂不可用" description={errorMessage(history.error)} action={<Button onClick={() => void history.refetch()}>重试</Button>} /> : null}
        {history.data?.versions.map((item) => (
          <div className="subject-history-row" key={`version:${item.version}`}>
            <div><strong>身份 v{item.version}</strong><span>{item.display_name}</span></div>
            <span>{subjectVerificationLabel(item.verification_state, item.verification_scope)} · {formatDateTime(item.created_at)}</span>
          </div>
        ))}
        {history.data?.events.map((event) => (
          <div className="subject-history-row" key={event.event_id}>
            <div>
              <span>{event.kind === "proposal_created" ? "发起更正申请" : event.kind === "decision_approved" ? "更正已批准" : event.kind === "decision_rejected" ? "更正被驳回" : "更正已生效"}</span>
              <Link to={`/cases/${encodeURIComponent(event.case_id)}/subject-changes/${encodeURIComponent(event.request_id)}`}>查看有权访问的案件记录</Link>
            </div>
            <span>{formatDateTime(event.created_at)}</span>
          </div>
        ))}
        {history.data && history.data.versions.length === 0 && history.data.events.length === 0 ? <Empty description="暂无版本与操作记录" /> : null}
      </Card>
      <Card title="当前可见案件绑定" className="subject-links-card">
        {links.isLoading ? <p>正在读取案件绑定…</p> : null}
        {links.isError ? <Alert type="error" showIcon title="案件绑定暂不可用" description={errorMessage(links.error)} action={<Button onClick={() => void links.refetch()}>重试</Button>} /> : null}
        {links.data?.items.length === 0 ? <Empty description="没有当前可见的案件绑定" /> : null}
        {links.data?.items.map((item) => (
          <div className="subject-case-link" key={item.binding_id}>
            <div>
              <Link to={`/cases/${encodeURIComponent(item.case_id)}`}>{item.case_title}</Link>
              <span>{item.role === "customer" ? "客户" : item.role === "supplier" ? "供应商" : item.role === "contractor" ? "承包方" : item.role === "service_provider" ? "服务提供方" : "其他角色"}</span>
            </div>
            <span>{item.business_status} · 绑定 v{item.binding_version} · 身份 v{item.subject.version}</span>
          </div>
        ))}
        {links.data ? (
          <div className="subject-ledger-pagination">
            <Typography.Text type="secondary">本页 {links.data.visible_count} 条当前可见绑定</Typography.Text>
            <Space>
              <Button disabled={linkPage.scope !== scope || linkPage.stack.length === 0 || links.isFetching} onClick={() => {
                const stack = [...linkPage.stack];
                const cursor = stack.pop() ?? null;
                setLinkPage({ scope, cursor, stack });
              }}>上一页</Button>
              <Button disabled={links.data.next_cursor === null || links.isFetching} onClick={() => {
                if (links.data?.next_cursor == null) return;
                setLinkPage((current) => ({ scope, cursor: links.data!.next_cursor, stack: [...(current.scope === scope ? current.stack : []), linkCursor] }));
              }}>下一页</Button>
            </Space>
          </div>
        ) : null}
      </Card>
    </section>
  );
}
