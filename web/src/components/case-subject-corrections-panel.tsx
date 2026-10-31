import { Alert, Button, Card, Checkbox, Empty, Input, Select, Space } from "antd";
import { useMemo, useRef, useState, type MouseEvent } from "react";
import { Link } from "react-router";
import { errorMessage } from "../api/http";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import {
  useCaseDetail,
  useCaseSubjectChanges,
  useCaseSubjects,
  useMaterialParsedDocuments,
  useMaterialParsedTextPage,
  useProposeSubjectChange,
  useSession,
  useSubject,
  useSubjects,
} from "../api/queries";
import type { CaseRoleBindingProjection, MaterialParsedTextPage, SubjectChangeProposalInput, SubjectRole } from "../api/types";
import { materialAnchorVersionOptions, runeSpanFromUTF16 } from "../domain/material-anchor";
import { formatDateTime } from "../domain/review";
import { subjectBindingStateLabel, subjectRoleLabel } from "../domain/subjects";

type ChangeTargetKind = "subject" | "case_subject_role";
type IdentifierAction = "unchanged" | "replace" | "clear";
type SelectedAnchor = MaterialParsedTextPage & { start: number; end: number; quote: string };
const roleOptions: Array<{ value: SubjectRole; label: string }> = [
  { value: "customer", label: "客户" }, { value: "supplier", label: "供应商" },
  { value: "contractor", label: "承包方" }, { value: "service_provider", label: "服务提供方" },
  { value: "other", label: "其他角色" },
];
const bindingStateOptions = [
  { value: "active", label: "在办绑定" },
  { value: "pending_confirmation", label: "待确认绑定" },
  { value: "inactive", label: "停用绑定" },
];
const usccPattern = /^[0-9A-HJ-NP-RTUW-Y]{18}$/;


function sha256(value: string): Promise<string> {
  return crypto.subtle.digest("SHA-256", new TextEncoder().encode(value)).then((digest) =>
    Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join(""),
  );
}

function subjectOptions(bindings: CaseRoleBindingProjection[]) {
  const seen = new Set<string>();
  return bindings.flatMap((binding) => {
    if (binding.binding_state === "inactive" || seen.has(binding.subject.subject_id)) return [];
    seen.add(binding.subject.subject_id);
    return [{
      value: binding.subject.subject_id,
      label: `${binding.subject.display_name} · ${subjectRoleLabel(binding.role)} · 身份 v${binding.subject.version}`,
    }];
  });
}

export function CaseSubjectCorrectionsPanel({ caseId }: { caseId: string }) {
  const session = useSession();
  const caseDetail = useCaseDetail(caseId);
  const subjects = useCaseSubjects(caseId);
  const canManage = subjects.data?.can_manage === true;
  const changes = useCaseSubjectChanges(caseId);
  const propose = useProposeSubjectChange(caseId);
  const [candidateSearch, setCandidateSearch] = useState("");
  const candidates = useSubjects(candidateSearch, null, caseId);
  const versions = useMemo(() => materialAnchorVersionOptions(caseDetail.data), [caseDetail.data]);

  const [targetKind, setTargetKind] = useState<ChangeTargetKind>("subject");
  const [subjectId, setSubjectId] = useState<string>();
  const [bindingId, setBindingId] = useState<string>();
  const [rebindSubjectId, setRebindSubjectId] = useState<string>();
  const [targetRole, setTargetRole] = useState<SubjectRole>();
  const [bindingState, setBindingState] = useState<"active" | "inactive" | "pending_confirmation">();
  const [displayName, setDisplayName] = useState("");
  const [legalName, setLegalName] = useState("");
  const [clearLegalName, setClearLegalName] = useState(false);
  const [identifierAction, setIdentifierAction] = useState<IdentifierAction>("unchanged");
  const [identifierValue, setIdentifierValue] = useState("");
  const [basisKey, setBasisKey] = useState<string>();
  const [parsedDocumentId, setParsedDocumentId] = useState<string>();
  const [pageOffset, setPageOffset] = useState(0);
  const [pageStack, setPageStack] = useState<number[]>([]);
  const [selectedAnchor, setSelectedAnchor] = useState<SelectedAnchor | null>(null);
  const [basisNote, setBasisNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const textRef = useRef<HTMLPreElement>(null);

  const selectedVersion = versions.find((item) => item.key === basisKey);
  const parsedDocuments = useMaterialParsedDocuments(caseId, selectedVersion?.material_id, selectedVersion?.version);
  const selectedParse = parsedDocuments.data?.items.find((item) => item.parsed_document_id === parsedDocumentId);
  const parsedPage = useMaterialParsedTextPage(caseId, selectedVersion?.material_id, selectedVersion?.version, selectedParse?.parsed_document_id, pageOffset);
  const selectedSubjectBinding = subjects.data?.items.find((item) => item.subject.subject_id === subjectId && item.binding_state !== "inactive");
  const selectedBinding = subjects.data?.items.find((item) => item.binding_id === bindingId);
  const bindingSubjectCurrent = useSubject(selectedBinding?.subject.subject_id ?? "");
  const rebindTarget = candidates.data?.items.find((item) => item.subject_id === rebindSubjectId);
  const parsedPageText = parsedPage.data?.text ?? "";

  function resetAnchorSelection() {
    setSelectedAnchor(null);
    setError(null);
  }

  function captureSelectedText(event: MouseEvent<HTMLPreElement>) {
    const page = parsedPage.data;
    const node = event.currentTarget.firstChild;
    const selection = window.getSelection();
    if (!page || !node || !selection || selection.rangeCount === 0) return;
    const range = selection.getRangeAt(0);
    if (range.startContainer !== node || range.endContainer !== node) {
      setError("请在同一段材料原文中选择一段连续文本。 ");
      return;
    }
    const span = runeSpanFromUTF16(page.text, range.startOffset, range.endOffset);
    if (!span) {
      setError("请先选中一段完整的材料原文。 ");
      return;
    }
    setError(null);
    setSelectedAnchor({
      ...page,
      start: page.offset + span.start,
      end: page.offset + span.end,
      quote: span.quote,
    });
  }

  async function submit() {
    const actor = session.data;
    const anchor = selectedAnchor;
    if (!actor || !anchor || !basisNote.trim()) return;
    setError(null);
    setNotice(null);
    try {
      const basis = {
        kind: "material_anchor" as const,
        material_id: anchor.material_id,
        material_version: anchor.material_version,
        document_id: anchor.document_id,
        parsed_document_id: anchor.parsed_document_id,
        start: anchor.start,
        end: anchor.end,
        quote_digest: await sha256(anchor.quote),
      };
      let input: SubjectChangeProposalInput;
      if (targetKind === "subject") {
        const target = selectedSubjectBinding?.subject;
        if (!target) throw new Error("请选择本案在办主体。");
        const normalizedIdentifier = identifierValue.trim().toUpperCase();
        if (identifierAction === "replace" && !usccPattern.test(normalizedIdentifier))
          throw new Error("请输入18位统一社会信用代码；格式正确不代表身份已核验。");
        const patch = {
          ...(displayName.trim() ? { display_name: displayName.trim() } : {}),
          ...(clearLegalName ? { legal_name: "" } : legalName.trim() ? { legal_name: legalName.trim() } : {}),
          ...(identifierAction === "clear" ? { identity_claims: [] } : identifierAction === "replace" ? {
            identity_claims: [{ namespace: "cn.gov.samr.uscc" as const, identifier_type: "unified_social_credit_code" as const, value: normalizedIdentifier }],
          } : {}),
        };
        if (Object.keys(patch).length === 0) throw new Error("至少选择一项需要更正的主体资料。");
        input = {
          target: { kind: "subject", subject_id: target.subject_id, binding_id: null },
          expected_subject_version: target.version,
          expected_binding_version: null,
          basis_note: basisNote.trim(),
          basis_refs: [basis],
          patch,
        };
      } else {
        if (!selectedBinding) throw new Error("请选择本案角色绑定。");
        if (!bindingSubjectCurrent.data) throw new Error("主体当前版本暂不可用，请刷新后重试。");
        if (rebindSubjectId && !rebindTarget) throw new Error("请从当前可见候选中重新选择主体。");
        const patch = {
          ...(targetRole ? { role: targetRole } : {}),
          ...(bindingState ? { binding_state: bindingState } : {}),
          ...(rebindTarget && rebindTarget.subject_id !== selectedBinding.subject.subject_id ? {
            subject_id: rebindTarget.subject_id,
            expected_new_subject_version: rebindTarget.current_version,
          } : {}),
        };
        if (Object.keys(patch).length === 0) throw new Error("至少选择一项需要更正的案件绑定信息。");
        input = {
          target: { kind: "case_subject_role", subject_id: selectedBinding.subject.subject_id, binding_id: selectedBinding.binding_id },
          expected_subject_version: bindingSubjectCurrent.data.current_version,
          expected_binding_version: selectedBinding.binding_version,
          basis_note: basisNote.trim(),
          basis_refs: [basis],
          patch,
        };
      }

      const targetKey = targetKind === "subject" ? subjectId! : bindingId!;
      const pending = await pendingMutationKey("subject-change.propose", idempotencyActorScope(actor), `${caseId}:${targetKey}`, input);
      const result = await propose.mutateAsync({ value: input, idempotencyKey: pending.key });
      setNotice("更正申请已提交，等待具备复核权限的其他人员处理。身份不会因提交申请而自动变为已核验。");
      setDisplayName(""); setLegalName(""); setClearLegalName(false); setIdentifierAction("unchanged"); setIdentifierValue("");
      setBasisNote(""); setBasisKey(undefined); setSelectedAnchor(null); setPageOffset(0); setPageStack([]);
      setSubjectId(undefined); setBindingId(undefined); setRebindSubjectId(undefined); setTargetRole(undefined); setBindingState(undefined);
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        setError("申请已提交，但浏览器未能清理安全重试标识。请勿重复提交这次内容。 ");
      }
      void changes.refetch();
      void result;
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  function selectVersion(value: string) {
    setBasisKey(value);
    setParsedDocumentId(undefined);
    setPageOffset(0);
    setPageStack([]);
    resetAnchorSelection();
  }

  return (
    <Card title="主体更正与复核" className="case-subject-corrections-panel">
      {changes.isError ? <Alert type="error" showIcon title="更正申请列表暂不可用" description={errorMessage(changes.error)} action={<Button onClick={() => void changes.refetch()}>重试</Button>} /> : null}
      {changes.data?.items.length ? changes.data.items.map((item) => (
        <div className="subject-history-row" key={item.request_id}>
          <div>
            <strong>{item.state === "pending_decision" ? "待复核" : item.state === "applied" ? "已生效" : item.state === "rejected" ? "已驳回" : "已批准"}</strong>
            <span>{item.target_kind === "subject" ? "主体身份更正" : "案件角色更正"} · 目标身份 v{item.expected_subject_version}</span>
            <Link to={`/cases/${encodeURIComponent(caseId)}/subject-changes/${encodeURIComponent(item.request_id)}`}>查看更正前后与依据</Link>
          </div>
          <span>{formatDateTime(item.created_at)}</span>
        </div>
      )) : changes.data ? <Empty description="本案暂无更正申请" /> : null}

      {canManage ? <div className="case-subject-add">
        <h3>发起主体更正申请</h3>
        <p>申请必须引用本案材料某一固定版本中的可定位原文，并由服务端核验后交由有权人员复核。</p>
        <Space wrap>
          <Select aria-label="更正目标类型" value={targetKind} options={[
            { value: "subject", label: "主体身份资料" }, { value: "case_subject_role", label: "案件角色绑定" },
          ]} onChange={(value: ChangeTargetKind) => {
            setTargetKind(value); setSubjectId(undefined); setBindingId(undefined); setRebindSubjectId(undefined);
            setTargetRole(undefined); setBindingState(undefined); setDisplayName(""); setLegalName("");
            setClearLegalName(false); setIdentifierAction("unchanged"); setIdentifierValue("");
          }} />
          {targetKind === "subject" ? (
            <Select aria-label="选择本案主体" value={subjectId} onChange={setSubjectId} loading={subjects.isLoading}
              placeholder="选择需要更正的主体" options={subjectOptions(subjects.data?.items ?? [])} />
          ) : (
            <Select aria-label="选择本案角色绑定" value={bindingId} onChange={setBindingId} loading={subjects.isLoading}
              placeholder="选择需要更正的绑定" options={(subjects.data?.items ?? []).map((item) => ({
                value: item.binding_id,
                label: `${item.subject.display_name} · ${subjectRoleLabel(item.role)} · ${subjectBindingStateLabel(item.binding_state)} · 绑定 v${item.binding_version}`,
              }))} />
          )}
        </Space>

        {targetKind === "subject" ? (
          <Space orientation="vertical" className="subject-correction-form">
            <Input aria-label="更正后主体名称" value={displayName} onChange={(event) => setDisplayName(event.target.value)} placeholder="更正后的主体名称（留空表示不变）" maxLength={200} />
            <Input aria-label="更正后法定名称" value={legalName} disabled={clearLegalName} onChange={(event) => setLegalName(event.target.value)} placeholder="更正后的法定名称（留空表示不变）" maxLength={200} />
            <Checkbox checked={clearLegalName} onChange={(event) => { setClearLegalName(event.target.checked); if (event.target.checked) setLegalName(""); }}>明确清空法定名称</Checkbox>
            <Select aria-label="统一社会信用代码更正方式" value={identifierAction} options={[
              { value: "unchanged", label: "保留当前标识信息" }, { value: "replace", label: "更正统一社会信用代码" }, { value: "clear", label: "明确清空统一社会信用代码" },
            ]} onChange={(value: IdentifierAction) => { setIdentifierAction(value); if (value !== "replace") setIdentifierValue(""); }} />
            {identifierAction === "replace" ? <Input aria-label="更正后统一社会信用代码" autoComplete="off" maxLength={18} value={identifierValue} onChange={(event) => setIdentifierValue(event.target.value.toUpperCase())} placeholder="18位统一社会信用代码；提交后仍为待核验" /> : null}
            {selectedSubjectBinding?.subject.identity_claims.length ? <span>当前标识：{selectedSubjectBinding.subject.identity_claims.map((item) => item.value ?? item.masked_value ?? "暂不可用").join("、")}</span> : null}
          </Space>
        ) : (
          <Space wrap className="subject-correction-form">
            <Select aria-label="更正后案件角色" value={targetRole} onChange={(value: SubjectRole) => setTargetRole(value)} allowClear placeholder="案件角色保持不变" options={roleOptions} />
            <Select aria-label="更正后绑定状态" value={bindingState} onChange={(value: "active" | "inactive" | "pending_confirmation") => setBindingState(value)} allowClear placeholder="绑定状态保持不变" options={bindingStateOptions} />
            <Input aria-label="搜索重新绑定主体" value={candidateSearch} onChange={(event) => { setCandidateSearch(event.target.value); setRebindSubjectId(undefined); }} placeholder="搜索明确要绑定的现有主体" />
            <Select aria-label="选择重新绑定主体" value={rebindSubjectId} onChange={setRebindSubjectId} loading={candidates.isLoading} placeholder="不重新绑定主体" options={(candidates.data?.items ?? []).map((item) => ({
              value: item.subject_id, label: `${item.display_name} · v${item.current_version}`,
            }))} />
            {candidates.isError ? <Alert type="error" showIcon title="主体候选暂不可用" description={errorMessage(candidates.error)} action={<Button onClick={() => void candidates.refetch()}>重试</Button>} /> : null}
          </Space>
        )}

        <Space orientation="vertical" className="subject-correction-form">
          <Select aria-label="选择固定材料版本" value={basisKey} onChange={selectVersion} loading={caseDetail.isLoading}
            placeholder="选择本案材料及固定版本" options={versions.map((item) => ({ value: item.key, label: `${item.role} · ${item.purpose} · v${item.version}` }))} />
          {caseDetail.isError ? <Alert type="error" showIcon title="案件材料版本暂不可用" description={errorMessage(caseDetail.error)} action={<Button onClick={() => void caseDetail.refetch()}>重试</Button>} /> : null}
          {versions.length === 0 && caseDetail.data ? <Empty description="本案没有可引用的固定材料版本" /> : null}
          {parsedDocuments.isError ? <Alert type="error" showIcon title="固定解析版本列表暂不可用" description={errorMessage(parsedDocuments.error)} action={<Button onClick={() => void parsedDocuments.refetch()}>重试</Button>} /> : null}
          {selectedVersion && parsedDocuments.data ? (
            parsedDocuments.data.items.length ? (
              <Select aria-label="选择材料固定解析版本" value={parsedDocumentId} onChange={(value: string) => {
                setParsedDocumentId(value); setPageOffset(0); setPageStack([]); resetAnchorSelection();
              }} placeholder="选择一份固定解析记录" options={parsedDocuments.data.items.map((item) => ({
                value: item.parsed_document_id,
                disabled: item.status !== "ready",
                label: `解析 ${item.parsed_document_id} · ${item.status === "ready" ? "可引用" : "解析失败"} · ${formatDateTime(item.created_at)} · ${item.parser_id} ${item.parser_version}`,
              }))} />
            ) : <Empty description="该材料版本没有固定解析记录，无法建立原文依据" />
          ) : null}
          {selectedParse?.status === "failed" ? <Alert type="warning" showIcon title="所选固定解析记录失败，请选择其他解析版本。" /> : null}
          {parsedPage.isError ? <Alert type="error" showIcon title="所选材料版本原文暂不可用" description={errorMessage(parsedPage.error)} action={<Button onClick={() => void parsedPage.refetch()}>重试</Button>} /> : null}
          {parsedPage.isLoading && selectedVersion ? <p>正在读取所选材料版本的固定解析文本…</p> : null}
          {parsedPage.data ? (
            <div className="subject-anchor-source">
              <p>{parsedPage.data.filename} · 固定材料版本 v{parsedPage.data.material_version} · 共 {parsedPage.data.total_runes} 字 · 当前显示 {parsedPage.data.offset + 1} 至 {parsedPage.data.offset + [...parsedPageText].length}</p>
              <pre ref={textRef} tabIndex={0} onMouseUp={captureSelectedText} aria-label="固定材料原文，可选择作为更正依据">{parsedPageText}</pre>
              <Space>
                <Button disabled={pageStack.length === 0 || parsedPage.isFetching} onClick={() => {
                  const stack = [...pageStack];
                  setPageOffset(stack.pop() ?? 0); setPageStack(stack); resetAnchorSelection();
                }}>上一段</Button>
                <Button disabled={parsedPage.data.next_offset === null || parsedPage.isFetching} onClick={() => {
                  if (parsedPage.data?.next_offset === null || parsedPage.data?.next_offset === undefined) return;
                  setPageStack((stack) => [...stack, pageOffset]); setPageOffset(parsedPage.data.next_offset); resetAnchorSelection();
                }}>下一段</Button>
              </Space>
            </div>
          ) : null}
          {selectedAnchor ? (
            <div className="subject-anchor-selected" data-material-id={selectedAnchor.material_id} data-document-id={selectedAnchor.document_id} data-parsed-document-id={selectedAnchor.parsed_document_id}>
              <strong>{selectedAnchor.filename} · v{selectedAnchor.material_version} · 原文位置 {selectedAnchor.start}–{selectedAnchor.end}</strong>
              <blockquote>{selectedAnchor.quote}</blockquote>
            </div>
          ) : parsedPage.data ? <span>在上方原文中拖选一段连续文本作为更正依据。</span> : null}
          <Input.TextArea aria-label="申请说明" value={basisNote} onChange={(event) => setBasisNote(event.target.value)} placeholder="说明更正原因；该说明不能替代材料依据" maxLength={2000} rows={3} />
          {targetKind === "case_subject_role" && selectedBinding?.binding_state === "inactive" ? (
            <Alert type="info" showIcon title={`该绑定保留身份 v${selectedBinding.subject.version} 的历史记录；恢复或更改时按当前主体 v${bindingSubjectCurrent.data?.current_version ?? "…"} 版本校验。`} />
          ) : null}
          {bindingSubjectCurrent.isError && targetKind === "case_subject_role" && bindingId ? <Alert type="error" showIcon title="主体当前版本暂不可用" description={errorMessage(bindingSubjectCurrent.error)} action={<Button onClick={() => void bindingSubjectCurrent.refetch()}>重试</Button>} /> : null}
          <Button type="primary" disabled={!session.data || !selectedAnchor || !basisNote.trim() || propose.isPending || (targetKind === "subject" ? !subjectId : !bindingId || !bindingSubjectCurrent.data)} loading={propose.isPending} onClick={() => void submit()}>提交更正申请</Button>
        </Space>
      </div> : subjects.data ? <Alert type="info" showIcon title="当前案件为只读状态，无法发起主体更正。" /> : null}
      {error ? <Alert type="error" showIcon title={error} /> : null}
      {notice ? <Alert type="success" showIcon title={notice} /> : null}
    </Card>
  );
}
