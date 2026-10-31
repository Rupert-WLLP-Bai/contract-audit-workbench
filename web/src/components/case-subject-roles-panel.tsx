import { Alert, Button, Card, Checkbox, Empty, Form, Input, Select, Space } from "antd";
import { useState } from "react";
import { Link } from "react-router";
import { errorMessage } from "../api/http";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import {
  useBindExistingSubject,
  useCaseSubjects,
  useCreateCaseRole,
  useSession,
  useSubjects,
} from "../api/queries";
import type { CaseRoleCreateInput, SubjectRole, SubjectSummary } from "../api/types";
import { subjectBindingStateLabel, subjectRoleLabel, subjectVerificationLabel } from "../domain/subjects";
import { SubjectCreateFields, subjectCreateInput, type SubjectCreateFormValues } from "./subject-create-fields";
import { SubjectSourceVerificationPanel } from "./subject-source-verification-panel";
import { StatusText } from "./status-text";

const roleOptions: Array<{ value: SubjectRole; label: string }> = [
  { value: "customer", label: "客户" },
  { value: "supplier", label: "供应商" },
  { value: "contractor", label: "承包方" },
  { value: "service_provider", label: "服务提供方" },
  { value: "other", label: "其他角色" },
];

export function CaseSubjectRolesPanel({ caseId }: { caseId: string }) {
  const session = useSession();
  const caseSubjects = useCaseSubjects(caseId);
  const createBinding = useCreateCaseRole(caseId);
  const bindExisting = useBindExistingSubject(caseId);
  const [mode, setMode] = useState<"create" | "bind">("create");
  const [role, setRole] = useState<SubjectRole>("customer");
  const [adoptLegacy, setAdoptLegacy] = useState(false);
  const [candidateQuery, setCandidateQuery] = useState("");
  const [candidateId, setCandidateId] = useState<string | undefined>();
  const [createForm] = Form.useForm<SubjectCreateFormValues>();
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionNote, setActionNote] = useState<string | null>(null);
  const candidates = useSubjects(candidateQuery, null, caseId);

  const legacyField = role === "customer" || role === "supplier" ? role : null;
  const legacyValue = legacyField ? caseSubjects.data?.legacy_profile?.[legacyField] ?? null : null;
  const canAdopt = legacyField !== null && legacyValue !== null && legacyValue.trim() !== "" && caseSubjects.data?.legacy_profile !== null;

  async function submitNewSubject(values: SubjectCreateFormValues) {
    if (!session.data || !caseSubjects.data) return;
    setActionError(null);
    setActionNote(null);
    try {
      const payload: CaseRoleCreateInput = {
        role,
        subject: subjectCreateInput(values),
        ...(adoptLegacy && canAdopt && legacyField && caseSubjects.data.legacy_profile
          ? { legacy_binding: {
              field: legacyField,
              value: legacyValue!,
              expected_profile_version: caseSubjects.data.legacy_profile.version,
            } }
          : {}),
      };
      const pending = await pendingMutationKey("case-subject.create", idempotencyActorScope(session.data), caseId, payload);
      const result = await createBinding.mutateAsync({ value: payload, idempotencyKey: pending.key });
      createForm.resetFields();
      setAdoptLegacy(false);
      setActionNote(result.binding_state === "pending_confirmation"
        ? "主体已登记并加入本案，绑定待确认；旧档案文字仍保留。"
        : "主体已登记并加入本案，身份状态为待核验。旧档案文字仍保留。");
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        setActionError("登记已成功，但浏览器无法清理安全重试标识。请勿重复提交这次内容；如需继续，请刷新后重试。");
      }
    } catch (error) {
      setActionError(errorMessage(error));
    }
  }

  async function submitExisting() {
    if (!session.data || !candidateId) return;
    const candidate = candidates.data?.items.find((item) => item.subject_id === candidateId);
    if (!candidate) {
      setActionError("请从当前可见的主体候选中明确选择一项。");
      return;
    }
    setActionError(null);
    setActionNote(null);
    const payload = { role, subject_id: candidate.subject_id, expected_subject_version: candidate.current_version };
    try {
      const pending = await pendingMutationKey("case-subject.bind-existing", idempotencyActorScope(session.data), caseId, payload);
      await bindExisting.mutateAsync({ value: payload, idempotencyKey: pending.key });
      setCandidateId(undefined);
      setActionNote("已将你选择的主体绑定到本案；同角色的其他绑定保持不变。身份仍按服务端状态显示。");
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        setActionError("绑定已成功，但浏览器无法清理安全重试标识。请勿重复提交这次内容；如需继续，请刷新后重试。");
      }
    } catch (error) {
      setActionError(errorMessage(error));
    }
  }

  return (
    <Card title="案件主体与角色" className="case-subject-roles-panel">
      {caseSubjects.isLoading ? <p>正在读取本案主体…</p> : null}
      {caseSubjects.isError ? <Alert type="error" showIcon title="本案主体信息暂不可用" description={errorMessage(caseSubjects.error)} action={<Button onClick={() => void caseSubjects.refetch()}>重试</Button>} /> : null}
      {caseSubjects.data?.legacy_profile ? (
        <div className="legacy-subject-profile">
          <strong>旧档案文字（原样保留，未自动关联）</strong>
          <div><span>客户：</span><span className="preserved-source-text">{caseSubjects.data.legacy_profile.customer ?? "—"}</span></div>
          <div><span>供应商：</span><span className="preserved-source-text">{caseSubjects.data.legacy_profile.supplier ?? "—"}</span></div>
        </div>
      ) : null}

      {caseSubjects.data?.items.length === 0 ? <Empty description="本案尚无主体角色绑定" /> : null}
      {caseSubjects.data?.items.map((binding) => (
        <div className="case-subject-binding-row" key={binding.binding_id}>
          <div className="case-subject-binding-main">
            <strong>{binding.subject.display_name}</strong>
            {binding.subject.legal_name ? <span>{binding.subject.legal_name}</span> : null}
            <span>{subjectRoleLabel(binding.role)} · 身份 v{binding.subject.version} · 绑定 v{binding.binding_version}</span>
            {binding.legacy_label !== null ? <span>来自旧档案字段：<span className="preserved-source-text">{binding.legacy_label}</span></span> : null}
          </div>
          <div className="case-subject-binding-status">
            <StatusText tone={binding.binding_state === "active" ? "good" : binding.binding_state === "pending_confirmation" ? "attention" : "neutral"}>{subjectBindingStateLabel(binding.binding_state)}</StatusText>
            <StatusText tone={binding.subject.verification_state === "verified" ? "good" : "attention"}>{subjectVerificationLabel(binding.subject.verification_state, binding.subject.verification_scope)}</StatusText>
          </div>
        </div>
      ))}

      {caseSubjects.data?.can_manage ? (
        <div className="case-subject-add">
          <h3>添加案件角色</h3>
          <Space wrap>
            <Select
              aria-label="案件角色"
              value={role}
              options={roleOptions}
              onChange={(value: SubjectRole) => { setRole(value); setAdoptLegacy(false); }}
            />
            <Select
              aria-label="主体来源方式"
              value={mode}
              options={[{ value: "create", label: "新建待核验主体" }, { value: "bind", label: "绑定已存在主体" }]}
              onChange={(value: "create" | "bind") => { setMode(value); setActionError(null); setActionNote(null); }}
            />
          </Space>
          {mode === "create" ? (
            <Form form={createForm} layout="vertical" onFinish={(values) => void submitNewSubject(values)}>
              <SubjectCreateFields />
              {canAdopt ? (
                <Checkbox checked={adoptLegacy} onChange={(event) => setAdoptLegacy(event.target.checked)}>
                  明确采用本案旧档案的{legacyField === "customer" ? "客户" : "供应商"}字段作为本次绑定来源
                </Checkbox>
              ) : null}
              <div className="case-subject-actions"><Button htmlType="submit" type="primary" loading={createBinding.isPending}>登记主体并添加角色</Button></div>
            </Form>
          ) : (
            <div className="case-subject-bind-form">
              <Input
                aria-label="搜索可见主体"
                value={candidateQuery}
                onChange={(event) => { setCandidateQuery(event.target.value); setCandidateId(undefined); }}
                placeholder="输入主体名称搜索当前可见候选"
              />
              <Select
                aria-label="选择已存在主体"
                value={candidateId}
                onChange={setCandidateId}
                loading={candidates.isLoading}
                showSearch={false}
                placeholder="请选择主体；同名主体不会自动合并"
                options={(candidates.data?.items ?? []).map((item: SubjectSummary) => ({
                  value: item.subject_id,
                  label: `${item.display_name} · v${item.current_version} · ${subjectVerificationLabel(item.verification_state, item.verification_scope)}`,
                }))}
              />
              {candidates.isError ? <Alert type="error" showIcon title="主体候选暂不可用" description={errorMessage(candidates.error)} action={<Button onClick={() => void candidates.refetch()}>重试</Button>} /> : null}
              <Button type="primary" disabled={!candidateId} loading={bindExisting.isPending} onClick={() => void submitExisting()}>绑定选中的主体</Button>
            </div>
          )}
          {actionError ? <Alert type="error" showIcon title={actionError} /> : null}
          {actionNote ? <Alert type="success" showIcon title={actionNote} /> : null}
        </div>
      ) : caseSubjects.data ? (
        <Alert type="info" showIcon title="当前案件为只读状态，无法添加或绑定主体角色。" />
      ) : null}

      {caseSubjects.data ? <SubjectSourceVerificationPanel caseId={caseId} bindings={caseSubjects.data.items} canManage={caseSubjects.data.can_manage} /> : null}
      <p className="subject-help-text">角色可有多个绑定。创建、旧档案采纳和绑定已有主体都由服务端按本案权限与版本校验。</p>
      <Link to="/subjects">打开主体台账</Link>
    </Card>
  );
}
