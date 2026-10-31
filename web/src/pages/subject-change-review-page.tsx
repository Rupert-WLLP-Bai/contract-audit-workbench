import { Alert, Button, Card, Descriptions, Empty, Form, Input, Space } from "antd";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { errorMessage } from "../api/http";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import {
  useCaseSubjectChange,
  useCaseSubjectChangeImpact,
  useDecideSubjectChange,
  useSession,
} from "../api/queries";
import type { Actor, CaseRoleBindingProjection, SubjectChangeBasisRef, SubjectVersionProjection } from "../api/types";
import { formatDateTime } from "../domain/review";
import { subjectBindingStateLabel, subjectRoleLabel, subjectVerificationLabel } from "../domain/subjects";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";

function isSubjectVersion(value: SubjectVersionProjection | CaseRoleBindingProjection): value is SubjectVersionProjection {
  return !Object.hasOwn(value, "binding_id");
}

function IdentityProjection({
  title,
  value,
}: {
  title: string;
  value: SubjectVersionProjection | CaseRoleBindingProjection;
}) {
  if (isSubjectVersion(value)) {
    return (
      <Card title={title} size="small" className="subject-change-projection">
        <Descriptions column={1} size="small">
          <Descriptions.Item label="主体名称">{value.display_name}</Descriptions.Item>
          <Descriptions.Item label="法定名称">{value.legal_name || "—"}</Descriptions.Item>
          <Descriptions.Item label="身份版本">v{value.version}</Descriptions.Item>
          <Descriptions.Item label="核验状态">
            <StatusText tone={value.verification_state === "verified" ? "good" : "attention"}>
              {subjectVerificationLabel(value.verification_state, value.verification_scope)}
            </StatusText>
          </Descriptions.Item>
          <Descriptions.Item label="统一社会信用代码">
            {value.identity_claims.length
              ? value.identity_claims.map((claim) => claim.value ?? claim.masked_value ?? "不可用").join("、")
              : "—"}
          </Descriptions.Item>
        </Descriptions>
      </Card>
    );
  }
  return (
    <Card title={title} size="small" className="subject-change-projection">
      <Descriptions column={1} size="small">
        <Descriptions.Item label="案件角色">{subjectRoleLabel(value.role)}</Descriptions.Item>
        <Descriptions.Item label="绑定状态">{subjectBindingStateLabel(value.binding_state)}</Descriptions.Item>
        <Descriptions.Item label="绑定版本">v{value.binding_version}</Descriptions.Item>
        <Descriptions.Item label="主体名称">{value.subject.display_name}</Descriptions.Item>
        <Descriptions.Item label="法定名称">{value.subject.legal_name || "—"}</Descriptions.Item>
        <Descriptions.Item label="身份版本">v{value.subject.version}</Descriptions.Item>
        <Descriptions.Item label="核验状态">
          <StatusText tone={value.subject.verification_state === "verified" ? "good" : "attention"}>
            {subjectVerificationLabel(value.subject.verification_state, value.subject.verification_scope)}
          </StatusText>
        </Descriptions.Item>
        <Descriptions.Item label="统一社会信用代码">
          {value.subject.identity_claims.length
            ? value.subject.identity_claims.map((claim) => claim.value ?? claim.masked_value ?? "不可用").join("、")
            : "—"}
        </Descriptions.Item>
      </Descriptions>
    </Card>
  );
}

function BasisItem({ basis }: { basis: SubjectChangeBasisRef }) {
  if (basis.kind === "material_anchor") {
    return (
      <div className="subject-change-basis-row">
        <div><strong>{basis.filename}</strong><span>材料版本 v{basis.material_version}</span></div>
        <blockquote>{basis.quote}</blockquote>
      </div>
    );
  }
  return (
    <div className="subject-change-basis-row">
      <strong>受控来源对照（合成演示）</strong>
      <span>来源记录时间 {formatDateTime(basis.source_as_of)}</span>
      <span>适用期间 {formatDateTime(basis.valid_from)} 至 {basis.valid_until ? formatDateTime(basis.valid_until) : "未设截止"}</span>
      <span>快照固定时间 {formatDateTime(basis.captured_at)} · 来源版本 v{basis.source_version}</span>
    </div>
  );
}

export function SubjectChangeReviewPage() {
  const { caseId = "", requestId = "" } = useParams();
  const session = useSession();
  const scope = [caseId, requestId, session.data?.id ?? "", session.data?.tenant_id ?? "", session.data?.org_id ?? "", session.data?.audience ?? ""].join(":");
  return <SubjectChangeReviewPageContents key={scope} caseId={caseId} requestId={requestId} actor={session.data} />;
}

function SubjectChangeReviewPageContents({ caseId, requestId, actor }: { caseId: string; requestId: string; actor: Actor | undefined }) {
  const detail = useCaseSubjectChange(caseId, requestId);
  const targetKind = detail.data?.summary.target_kind ?? null;
  const impact = useCaseSubjectChangeImpact(caseId, requestId, targetKind);
  const decide = useDecideSubjectChange(caseId, requestId);
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  if (detail.isLoading) return <PageStatus kind="loading" title="正在读取更正申请" />;
  if (detail.isError) return <PageStatus kind="error" title="更正申请暂不可用" detail={errorMessage(detail.error)} action={<Button onClick={() => void detail.refetch()}>重试</Button>} />;
  if (!detail.data) return <PageStatus kind="empty" title="没有更正申请" />;
  const proposal = detail.data;
  const canDecide = proposal.summary.state === "pending_decision" && proposal.can_decide && impact.data?.can_decide === true;
  const decisionBlockedReason = !proposal.can_decide
    ? proposal.decision_blocked_reason
    : impact.data?.can_decide === false
      ? impact.data.decision_blocked_reason
      : null;
  const decisionIsBlocked = !proposal.can_decide || impact.data?.can_decide === false;

  async function submitDecision(decision: "approve" | "reject") {
    const currentImpact = impact.data;
    if (!actor || !currentImpact || !canDecide || reason.trim() === "") return;
    setError(null);
    setNotice(null);
    const input = {
      decision,
      expected_subject_version: proposal.summary.expected_subject_version,
      expected_binding_version: proposal.summary.expected_binding_version,
      expected_payload_hash: proposal.summary.payload_hash,
      expected_impact_etag: currentImpact.impact_etag,
      reason: reason.trim(),
    } as const;
    try {
      const pending = await pendingMutationKey(
        `subject-change.${decision}`,
        idempotencyActorScope(actor),
        `${caseId}:${requestId}`,
        input,
      );
      const result = await decide.mutateAsync({ value: input, idempotencyKey: pending.key });
      setNotice(result.decision === "approved" ? "更正已批准并由服务端追加新版本。" : "更正申请已驳回；原版本保持不变。");
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        setError("决定已提交，但浏览器无法清理安全重试标识。请勿重复提交；如需继续，请刷新后重试。");
      }
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  return (
    <section className="page subject-change-review-page">
      <nav className="page-back" aria-label="更正导航"><Link to={`/cases/${encodeURIComponent(caseId)}`}>返回案件</Link></nav>
      <header className="page-head">
        <div>
          <h1>主体更正复核</h1>
          <p>请按可见的更正前后内容、材料依据和影响范围作出决定。</p>
        </div>
        <StatusText tone={proposal.summary.state === "pending_decision" ? "attention" : proposal.summary.state === "applied" ? "good" : "neutral"}>
          {proposal.summary.state === "pending_decision" ? "待复核" : proposal.summary.state === "applied" ? "已生效" : proposal.summary.state === "approved" ? "已批准" : "已驳回"}
        </StatusText>
      </header>

      <Card title="更正前后" className="subject-change-diff-card">
        <div className="subject-change-projections">
          <IdentityProjection title="更正前" value={proposal.before} />
          <IdentityProjection title="申请更正为" value={proposal.after} />
        </div>
        <div className="subject-change-proposer">
          <span>申请人：{proposal.proposed_by_display_name}</span>
          <span>提交时间：{formatDateTime(proposal.summary.created_at)}</span>
          <span>目标身份版本：v{proposal.summary.expected_subject_version}{proposal.summary.expected_binding_version === null ? "" : ` · 绑定版本 v${proposal.summary.expected_binding_version}`}</span>
        </div>
      </Card>

      <Card title="更正依据" className="subject-change-basis-card">
        {proposal.basis_refs.length
          ? proposal.basis_refs.map((basis, index) => <BasisItem key={`${basis.kind}:${index}`} basis={basis} />)
          : <Empty description="申请未附可核验依据，不能作出决定" />}
      </Card>

      <Card title="可见影响范围" className="subject-change-impact-card">
        {impact.isLoading ? <p>正在读取当前影响范围…</p> : null}
        {impact.isError ? <Alert type="error" showIcon title="影响范围暂不可用" description={errorMessage(impact.error)} action={<Button onClick={() => void impact.refetch()}>刷新影响范围</Button>} /> : null}
        {impact.data ? (
          <>
            <p>当前账号可见影响案件：{impact.data.visible_affected_count} 项。隐藏案件信息不会显示。</p>
            {impact.data.authorized_impact_cases.length ? (
              <ul>
                {impact.data.authorized_impact_cases.map((item) => (
                  <li key={item.case_id}><Link to={`/cases/${encodeURIComponent(item.case_id)}`}>{item.case_title}</Link> · {item.business_status}</li>
                ))}
              </ul>
            ) : <Empty description="没有当前可见的影响案件" />}
          </>
        ) : null}
      </Card>

      {proposal.summary.state === "pending_decision" && decisionIsBlocked ? (
        <Alert type="info" showIcon title={decisionBlockedReason ?? "当前账号暂不具备复核权限。"} />
      ) : null}
      {canDecide ? (
        <Card title="复核决定" className="subject-change-decision-card">
          <p>审批身份与案件授权由服务端核验。文档依据可以支持名称或角色更正，但不会单独把身份标记为真实核验。</p>
          <Form layout="vertical">
            <Form.Item label="复核理由" required>
              <Input.TextArea aria-label="复核理由" value={reason} onChange={(event) => setReason(event.target.value)} maxLength={2000} rows={4} />
            </Form.Item>
            <Space>
              <Button type="primary" disabled={!canDecide || !impact.data || reason.trim() === ""} loading={decide.isPending} onClick={() => void submitDecision("approve")}>批准并生效</Button>
              <Button danger disabled={!canDecide || !impact.data || reason.trim() === ""} loading={decide.isPending} onClick={() => void submitDecision("reject")}>驳回申请</Button>
              <Button disabled={impact.isFetching} onClick={() => void impact.refetch()}>刷新影响范围</Button>
            </Space>
          </Form>
        </Card>
      ) : null}
      {error ? <Alert type="error" showIcon title={error} /> : null}
      {notice ? <Alert type="success" showIcon title={notice} /> : null}
    </section>
  );
}
