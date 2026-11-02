import { Alert, Button, Card, Input, Space, Tag, Typography } from "antd";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  approveSupportGrant,
  getSupportGrantCase,
  listSupportGrants,
  requestSupportGrant,
  revokeSupportGrant,
  type SupportGrant,
} from "../api/support-grants";
import { errorMessage, invalidateSessionIdentity } from "../api/http";
import { clearIdentityQueries, useSession } from "../api/queries";
import { PageStatus } from "../components/page-status";
import "./support-grants.css";

const SUPPORT_GRANTS_KEY = ["support-grants"] as const;

function useSupportGrants() {
  const session = useSession();
  const contextVersion = session.data?.active_context?.context_version ?? "";
  return useQuery({
    queryKey: [...SUPPORT_GRANTS_KEY, session.data?.id ?? "", contextVersion],
    queryFn: ({ signal }) => listSupportGrants(signal),
    enabled: session.isSuccess,
    retry: false,
  });
}

function statusLabel(status: SupportGrant["status"]): string {
  return ({ pending: "待批准", approved: "已批准", revoked: "已撤销", expired: "已到期" })[status];
}

function statusColor(status: SupportGrant["status"]): string {
  return ({ pending: "processing", approved: "success", revoked: "default", expired: "warning" })[status];
}

function formatTimestamp(value: string): string {
  const timestamp = new Date(value);
  return Number.isNaN(timestamp.getTime()) ? value : timestamp.toLocaleString("zh-CN");
}

export function CaseSupportGrantPanel({ caseId }: { caseId: string }) {
  const session = useSession();
  const queryClient = useQueryClient();
  const grants = useSupportGrants();
  const [principalId, setPrincipalId] = useState("");
  const [reason, setReason] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [message, setMessage] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const visible = (grants.data?.grants ?? []).filter((grant) => grant.case_id === caseId);
  const refresh = () => queryClient.invalidateQueries({ queryKey: SUPPORT_GRANTS_KEY });
  const request = async () => {
    if (!session.data || session.data.principal_id === null || !session.data.context_switch_enabled || !principalId.trim() || principalId.trim() === session.data.principal_id || !reason.trim() || !expiresAt) return;
    setProblem(null);
    setMessage(null);
    try {
      const grant = await requestSupportGrant(caseId, {
        principal_id: principalId.trim(),
        actions: ["case.read"],
        reason: reason.trim(),
        expires_at: new Date(expiresAt).toISOString(),
      });
      setPrincipalId("");
      setReason("");
      setExpiresAt("");
      setMessage(`只读支援请求已记录，编号 ${grant.id}`);
      await refresh();
    } catch (error) {
      setProblem(errorMessage(error));
      if (error instanceof Error && "code" in error && error.code === "context_switch_disabled") void session.refetch();
    }
  };
  const command = async (grant: SupportGrant, kind: "approve" | "revoke") => {
    setProblem(null);
    setMessage(null);
    try {
      const updated = kind === "approve"
        ? await approveSupportGrant(grant.id, grant.revision)
        : await revokeSupportGrant(grant.id, grant.revision);
      setMessage(kind === "approve" ? `已批准只读支援 ${updated.id}` : `已撤销只读支援 ${updated.id}`);
      await refresh();
    } catch (error) {
      setProblem(errorMessage(error));
      if (error instanceof Error && "code" in error && error.code === "context_switch_disabled") void session.refetch();
      await refresh();
    }
  };
  const enabled = session.data?.context_switch_enabled === true;

  return (
    <Card title="案件只读支援" className="workflow-panel">
      <p>授权范围固定为本案件只读。支援身份不能审查、审批、决定、签署或导出。</p>
      {grants.isLoading ? <p>正在加载支援授权…</p> : null}
      {grants.isError ? <Alert type="error" showIcon title={errorMessage(grants.error)} /> : null}
      {visible.length === 0 ? <p>本案件暂无可见的支援授权。</p> : null}
      {visible.map((grant) => {
        const principalId = session.data?.principal_id;
        const isDistinctApprover = principalId !== null && principalId !== undefined && principalId !== grant.created_by && principalId !== grant.principal_id;
        return (
          <div className="support-grant-row" key={grant.id}>
            <div>
              <strong>支援对象：{grant.principal_id}</strong>
              <span>范围：单案件只读 · 案件 {grant.case_id}</span>
              <span>原因：{grant.reason}</span>
              <span>到期：{formatTimestamp(grant.expires_at)}</span>
            </div>
            <Space wrap>
              <Tag color={statusColor(grant.status)}>{statusLabel(grant.status)}</Tag>
              {enabled && grant.status === "pending" && isDistinctApprover ? (
                <Button type="primary" onClick={() => void command(grant, "approve")}>独立批准</Button>
              ) : null}
              {enabled && grant.status === "approved" ? (
                <Button danger onClick={() => void command(grant, "revoke")}>撤销授权</Button>
              ) : null}
            </Space>
          </div>
        );
      })}
      {enabled ? (
        <form className="support-grant-form" onSubmit={(event) => { event.preventDefault(); void request(); }}>
          <Typography.Title level={4}>请求单案件只读支援</Typography.Title>
          <label>
            受益成员 ID
            <Input value={principalId} onChange={(event) => setPrincipalId(event.target.value)} maxLength={200} required />
          </label>
          <label>
            支援原因
            <Input.TextArea value={reason} onChange={(event) => setReason(event.target.value)} maxLength={1000} required />
          </label>
          <label>
            到期时间
            <Input type="datetime-local" value={expiresAt} onChange={(event) => setExpiresAt(event.target.value)} required />
          </label>
          {principalId.trim() === session.data?.principal_id ? <p role="alert">受益成员必须与当前请求人不同。</p> : null}
          <Button htmlType="submit" type="primary" disabled={session.data?.principal_id === null || !principalId.trim() || principalId.trim() === session.data?.principal_id || !reason.trim() || !expiresAt}>提交独立批准请求</Button>
        </form>
      ) : <p>支援授权命令当前未启用；已批准的只读访问仍按服务端授权校验。</p>}
      {message ? <Alert type="success" showIcon title={message} /> : null}
      {problem ? <Alert type="error" showIcon title={problem} /> : null}
    </Card>
  );
}

export function SupportGrantsPage() {
  const grants = useSupportGrants();
  const session = useSession();
  if (grants.isLoading) return <PageStatus kind="loading" title="正在加载支援访问" />;
  if (grants.isError) return <PageStatus kind="error" title="支援访问没有加载" detail={errorMessage(grants.error)} />;
  const mine = session.data?.principal_id === null || session.data?.principal_id === undefined
    ? []
    : (grants.data?.grants ?? []).filter((grant) => grant.principal_id === session.data?.principal_id);
  return (
    <section className="page">
      <header className="page-head">
        <div><h1>案件支援访问</h1><p>仅显示授予当前成员的案件级只读支援。当前登录身份与租户不会改变。</p></div>
      </header>
      {mine.length === 0 ? <Card>目前没有授予当前成员的支援案件。</Card> : null}
      {mine.map((grant) => (
        <Card key={grant.id} className="support-grant-card">
          <Space orientation="vertical" size={8}>
            <Space wrap><Tag color={statusColor(grant.status)}>{statusLabel(grant.status)}</Tag><span>授权编号 {grant.id}</span></Space>
            <span>目标租户 / 组织：{grant.tenant_id} / {grant.org_id}</span>
            <span>案件：{grant.case_id}</span>
            <span>授权范围：case.read</span>
            <span>到期：{formatTimestamp(grant.expires_at)}</span>
            {grant.status === "approved" ? <Button type="primary" href={`/support-grants/${encodeURIComponent(grant.id)}`}>打开只读案件概览</Button> : null}
          </Space>
        </Card>
      ))}
    </section>
  );
}

export function SupportGrantCasePage() {
  const { grantId = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const session = useSession();
  const query = useQuery({
    queryKey: [...SUPPORT_GRANTS_KEY, "case", grantId, session.data?.active_context?.context_version ?? ""],
    queryFn: ({ signal }) => getSupportGrantCase(grantId, signal),
    enabled: session.isSuccess && grantId !== "",
    refetchInterval: 15_000,
    retry: false,
  });
  useEffect(() => {
    if (!query.isError) return;
    const status = query.error instanceof Error && "status" in query.error ? query.error.status : 0;
    if (status === 401) {
      invalidateSessionIdentity();
      clearIdentityQueries(queryClient);
      void queryClient.resetQueries({ queryKey: ["session"] });
    }
    if ([401, 403, 404].includes(Number(status))) {
      queryClient.removeQueries({ queryKey: [...SUPPORT_GRANTS_KEY, "case", grantId] });
      void navigate("/support", { replace: true });
    }
  }, [query.isError, query.error, queryClient, grantId, navigate]);
  if (query.isLoading) return <PageStatus kind="loading" title="正在确认支援授权" />;
  if (query.isError) return <PageStatus kind="denied" title="支援访问已关闭" detail={errorMessage(query.error)} action={<Button href="/support">返回支援访问</Button>} />;
  if (!query.data) return <PageStatus kind="empty" title="没有可显示的案件" />;
  const view = query.data;
  return (
    <section className="page support-readonly-page">
      <nav className="page-back" aria-label="支援访问导航"><Link to="/support">返回支援访问</Link></nav>
      <Alert type="info" showIcon title="只读支援视图" description="此页面使用当前登录成员身份访问，不切换租户；仅显示案件概览，不提供合同正文、审查、审批、决定、签署或导出功能。授权状态会重新向服务端核验。" />
      <Card title={view.title} className="support-case-card">
        <dl>
          <dt>案件编号</dt><dd>{view.case_id}</dd>
          <dt>合同类型</dt><dd>{view.contract_type}</dd>
          <dt>业务状态</dt><dd>{view.business_status}</dd>
          <dt>目标租户 / 组织</dt><dd>{view.tenant_id} / {view.org_id}</dd>
          <dt>案件建立时间</dt><dd>{formatTimestamp(view.created_at)}</dd>
          <dt>最近更新</dt><dd>{formatTimestamp(view.updated_at)}</dd>
          <dt>支援授权编号</dt><dd>{view.grant_id}</dd>
        </dl>
      </Card>
    </section>
  );
}
