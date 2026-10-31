import { Alert, Button, Card, Empty, Input, Select, Space } from "antd";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { errorMessage } from "../api/http";
import { idempotencyActorScope, pendingMutationKey, clearPendingMutationKey } from "../api/idempotency";
import { getLegalApplicabilityFactHistory, getLegalApplicabilityFacts, legalApplicabilityDimensions, recordLegalApplicabilityFact, type LegalApplicabilityDimension, type LegalApplicabilityFactInput, type LegalApplicabilityPartyPin, type LegalApplicabilityStatus, type LegalApplicabilityTemporalBasis, type LegalApplicabilityUnknownReason } from "../api/legal-applicability";
import { useCaseDetail, useCaseSubjects, useMaterialParsedDocuments, useMaterialParsedTextPage, useSession } from "../api/queries";
import type { CaseRoleBindingProjection, MaterialAnchorBasisInput } from "../api/types";
import { materialAnchorVersionOptions, runeSpanFromUTF16 } from "../domain/material-anchor";
import { legalApplicabilityControlledValues } from "../domain/legal-applicability";
import { formatDateTime } from "../domain/review";
import { beijingInputToUtc, toBeijingInputValue } from "./beijing-datetime";
import { StatusText } from "./status-text";

const labels: Record<LegalApplicabilityDimension, string> = {
  buyer_kind: "采购方主体类型", seller_kind: "供应方主体类型",
  buyer_enterprise_size: "采购方企业规模", seller_enterprise_size: "供应方企业规模",
  funding_kind: "资金性质", procurement_scope: "采购范围", project_kind: "项目类型",
  contract_clause_kind: "合同条款类型", transaction_kind: "交易性质",
};
const values: Record<LegalApplicabilityDimension, Array<{ value: string; label: string }>> = legalApplicabilityControlledValues;
const unknownReasons: Array<{ value: LegalApplicabilityUnknownReason; label: string }> = [
  { value: "conflicting_evidence", label: "依据相互冲突" }, { value: "uncertain_evidence", label: "依据不确定" },
  { value: "unsupported_basis", label: "缺少受支持的依据" }, { value: "insufficient_evidence", label: "依据不足" },
  { value: "expired_basis", label: "依据已过期" }, { value: "stale_party_context", label: "主体关联版本已变化" },
  { value: "party_scope_ambiguous", label: "主体范围不明确" }, { value: "historical_transition_basis_required", label: "需要历史衔接依据" },
];
const partyDimension = (dimension: LegalApplicabilityDimension) => dimension.startsWith("buyer_") || dimension.startsWith("seller_");
const partySide = (dimension: LegalApplicabilityDimension): "buyer" | "seller" => dimension.startsWith("buyer_") ? "buyer" : "seller";

interface Draft {
  status: LegalApplicabilityStatus;
  value: string;
  unknown_reason: LegalApplicabilityUnknownReason;
  temporal_basis: LegalApplicabilityTemporalBasis;
  valid_from: string;
  valid_until: string;
  basis_note: string;
  binding_id: string;
  scope_note: string;
}
interface SelectedAnchor extends MaterialAnchorBasisInput { filename: string; quote: string }

function initialDraft(): Draft {
  return { status: "unknown", value: "", unknown_reason: "insufficient_evidence", temporal_basis: "current_state", valid_from: "", valid_until: "", basis_note: "", binding_id: "", scope_note: "" };
}
function eligiblePartyBindings(bindings: CaseRoleBindingProjection[], side: "buyer" | "seller") {
  return bindings.filter((binding) => binding.binding_state === "active" &&
    (side === "buyer" ? binding.role === "customer" : ["supplier", "contractor", "service_provider"].includes(binding.role)));
}
function sha256(value: string): Promise<string> {
  return crypto.subtle.digest("SHA-256", new TextEncoder().encode(value)).then((digest) =>
    Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join(""),
  );
}

export function CaseLegalApplicabilityPanel({ caseId }: { caseId: string }) {
  const session = useSession();
  const queryClient = useQueryClient();
  const caseDetail = useCaseDetail(caseId);
  const subjects = useCaseSubjects(caseId);
  const canManage = subjects.data?.can_manage === true;
  const [businessAtInput, setBusinessAtInput] = useState(() => toBeijingInputValue(new Date().toISOString()));
  const [evidenceCutoff, setEvidenceCutoff] = useState(() => toBeijingInputValue(new Date().toISOString()));
  const [submittedTimes, setSubmittedTimes] = useState<{ businessAt: string; cutoff: string }>(() => ({ businessAt: beijingInputToUtc(toBeijingInputValue(new Date().toISOString())), cutoff: new Date().toISOString() }));
  const [drafts, setDrafts] = useState<Record<string, Draft>>(() => Object.fromEntries(legalApplicabilityDimensions.map((dimension) => [dimension, initialDraft()])));
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveNote, setSaveNote] = useState<string | null>(null);
  const [savingDimension, setSavingDimension] = useState<string | null>(null);
  const [historyCursors, setHistoryCursors] = useState<Partial<Record<LegalApplicabilityDimension, string | null>>>({});
  const [historyStacks, setHistoryStacks] = useState<Partial<Record<LegalApplicabilityDimension, Array<string | null>>>>({});
  const [materialKey, setMaterialKey] = useState("");
  const [parsedDocumentId, setParsedDocumentId] = useState("");
  const [pageOffset, setPageOffset] = useState(0);
  const [pageStack, setPageStack] = useState<number[]>([]);
  const [anchor, setAnchor] = useState<SelectedAnchor | null>(null);
  const textRef = useRef<HTMLPreElement>(null);
  const options = useMemo(() => materialAnchorVersionOptions(caseDetail.data), [caseDetail.data]);
  const selectedMaterial = options.find((item) => item.key === materialKey);
  const parsedDocuments = useMaterialParsedDocuments(caseId, selectedMaterial?.material_id, selectedMaterial?.version);
  const selectedParse = parsedDocuments.data?.items.find((item) => item.parsed_document_id === parsedDocumentId);
  const parsedPage = useMaterialParsedTextPage(caseId, selectedMaterial?.material_id, selectedMaterial?.version, selectedParse?.parsed_document_id, pageOffset);
  const factQuery = useQuery({
    queryKey: ["cases", caseId, "legal-applicability-facts", submittedTimes.businessAt, submittedTimes.cutoff],
    queryFn: ({ signal }) => getLegalApplicabilityFacts(caseId, submittedTimes.businessAt, submittedTimes.cutoff, signal),
    enabled: Boolean(caseId && submittedTimes.businessAt && submittedTimes.cutoff),
    retry: false,
  });
  const histories = useQueries({ queries: legalApplicabilityDimensions.map((dimension) => ({
    queryKey: ["cases", caseId, session.data?.id, "legal-applicability-history", dimension, historyCursors[dimension] ?? ""],
    queryFn: async ({ signal }) => {
      const history = await getLegalApplicabilityFactHistory(caseId, dimension, historyCursors[dimension] ?? null, signal);
      if (history.case_id !== caseId || history.dimension !== dimension) throw new Error("法律适用事实历史响应范围与请求不匹配");
      return history;
    },
    enabled: Boolean(caseId && session.data), retry: false,
  })) });

  useEffect(() => {
    const now = new Date().toISOString();
    const beijingNow = toBeijingInputValue(now);
    setHistoryCursors({});
    setHistoryStacks({});
    setAnchor(null);
    setMaterialKey("");
    setParsedDocumentId("");
    setDrafts(Object.fromEntries(legalApplicabilityDimensions.map((dimension) => [dimension, initialDraft()])));
    setBusinessAtInput(beijingNow);
    setEvidenceCutoff(beijingNow);
    setSubmittedTimes({ businessAt: now, cutoff: now });
  }, [caseId, session.data?.id]);

  useEffect(() => {
    if (!factQuery.data) return;
    setDrafts((current) => {
      const next = { ...current };
      for (const row of factQuery.data.facts) {
        const fact = row.fact;
        if (!fact) continue;
        next[row.dimension] = {
          status: fact.status, value: fact.value,
          unknown_reason: fact.unknown_reason || "insufficient_evidence",
          temporal_basis: fact.temporal_basis,
          valid_from: fact.valid_until ? toBeijingInputValue(fact.valid_from) : "",
          valid_until: fact.valid_until ? toBeijingInputValue(fact.valid_until) : "",
          basis_note: fact.basis_note,
          binding_id: fact.party?.binding_id ?? "", scope_note: fact.party?.scope_note ?? "",
        };
      }
      return next;
    });
  }, [factQuery.data]);

  function updateDraft(dimension: LegalApplicabilityDimension, patch: Partial<Draft>) {
    setDrafts((current) => ({ ...current, [dimension]: { ...current[dimension]!, ...patch } }));
  }
  function submitBusinessTime() {
    const businessAt = beijingInputToUtc(businessAtInput);
    if (!businessAt) { setSaveError("请输入有效的北京时间（UTC+08:00）。"); return; }
    const cutoffUTC = beijingInputToUtc(evidenceCutoff);
    if (!cutoffUTC) { setSaveError("请输入有效的证据截止时间（北京时间）。"); return; }
    setSaveError(null);
    setSubmittedTimes({ businessAt, cutoff: cutoffUTC });
  }
  function chooseMaterialVersion(key: string) {
    setMaterialKey(key); setParsedDocumentId(""); setPageOffset(0); setPageStack([]); setAnchor(null);
  }
  function captureAnchor(event: MouseEvent<HTMLPreElement>) {
    const page = parsedPage.data;
    const node = textRef.current?.firstChild;
    const selection = window.getSelection();
    if (!page || !node || !selection || selection.rangeCount !== 1 || !textRef.current?.contains(selection.anchorNode) || !textRef.current.contains(selection.focusNode)) {
      setAnchor(null); return;
    }
    const range = selection.getRangeAt(0);
    const startUTF16 = range.startContainer === node ? range.startOffset : event.currentTarget.textContent?.length ?? 0;
    const endUTF16 = range.endContainer === node ? range.endOffset : 0;
    const span = runeSpanFromUTF16(page.text, startUTF16, endUTF16);
    if (!span) { setAnchor(null); return; }
    setAnchor({
      kind: "material_anchor", material_id: page.material_id, material_version: page.material_version,
      document_id: page.document_id, parsed_document_id: page.parsed_document_id,
      start: page.offset + span.start, end: page.offset + span.end, quote: span.quote,
      filename: page.filename, quote_digest: "",
    });
  }

  async function saveFact(dimension: LegalApplicabilityDimension) {
    const current = factQuery.data?.facts.find((item) => item.dimension === dimension);
    const history = histories[legalApplicabilityDimensions.indexOf(dimension)]?.data;
    const draft = drafts[dimension];
    if (!session.data || !current || !history || !history.can_manage || !draft || !canManage) return;
    setSaveError(null); setSaveNote(null); setSavingDimension(dimension);
    try {
      const basisRefs: MaterialAnchorBasisInput[] = [];
      if (anchor) basisRefs.push({
        kind: "material_anchor", material_id: anchor.material_id, material_version: anchor.material_version,
        document_id: anchor.document_id, parsed_document_id: anchor.parsed_document_id,
        start: anchor.start, end: anchor.end, quote_digest: await sha256(anchor.quote),
      });
      const unknownReason = draft.status === "unknown" ? draft.unknown_reason : undefined;
      const requiresAnchor = draft.status === "established" || draft.temporal_basis === "historical_interval" || (draft.status === "unknown" && draft.unknown_reason !== "insufficient_evidence");
      if (requiresAnchor && basisRefs.length === 0) throw new Error("请从本案固定材料原文中选择可核验依据。");
      const side = partySide(dimension);
      const binding = partyDimension(dimension) ? subjects.data?.items.find((item) => item.binding_id === draft.binding_id) : undefined;
      if (partyDimension(dimension) && draft.status === "established" && !binding) throw new Error(`请为${side === "buyer" ? "采购方" : "供应方"}选择本案当前有效主体绑定。`);
      const party: LegalApplicabilityPartyPin | undefined = binding ? {
        side, binding_id: binding.binding_id, binding_version: binding.binding_version,
        subject_id: binding.subject.subject_id, subject_version: binding.subject.version,
        scope_note: draft.scope_note.trim(),
      } : undefined;
      if (binding && !draft.scope_note.trim()) throw new Error("请说明所选主体绑定对应本事实的主体范围。");
      const payload: LegalApplicabilityFactInput = {
        expected_version: history.current_version,
        dimension, status: draft.status,
        ...(draft.status === "established" ? { value: draft.value } : { unknown_reason: unknownReason }),
        temporal_basis: draft.temporal_basis,
        ...(draft.temporal_basis === "historical_interval" ? {
          valid_from: beijingInputToUtc(draft.valid_from), valid_until: beijingInputToUtc(draft.valid_until),
        } : {}),
        basis_note: draft.basis_note.trim(), basis_refs: basisRefs,
        ...(party ? { party } : {}),
      };
      if (!payload.basis_note) throw new Error("请填写事实依据说明。");
      if (draft.status === "established" && !values[dimension].some((option) => option.value === draft.value)) throw new Error("请选择该维度的受控取值。");
      if (draft.temporal_basis === "historical_interval" && (!payload.valid_from || !payload.valid_until || Date.parse(payload.valid_from) >= Date.parse(payload.valid_until))) throw new Error("历史事实需填写北京时间的有效起止时段，结束时间为排他边界。");
      const pending = await pendingMutationKey("legal-applicability.record", idempotencyActorScope(session.data), `${caseId}:${dimension}`, payload);
      const saved = await recordLegalApplicabilityFact(caseId, payload, pending.key);
      setSaveNote(`${labels[dimension]}已追加为第 ${saved.version} 版。`);
      try { clearPendingMutationKey(pending.storageKey); }
      catch { setSaveError("记录已保存，但浏览器无法清理安全重试标识；请勿重复提交本次内容，刷新后查看版本。"); }
      await queryClient.invalidateQueries({ queryKey: ["cases", caseId, "legal-applicability-facts"] });
      await queryClient.invalidateQueries({ queryKey: ["cases", caseId, session.data.id, "legal-applicability-history"] });
      await factQuery.refetch();
    } catch (cause) { setSaveError(errorMessage(cause)); }
    finally { setSavingDimension(null); }
  }

  return (
    <Card title="法律适用事实" className="case-legal-applicability-panel">
      <p>事实用于规则适用判断。已确认事实必须有本案材料依据；资料不足时保留“未知”，不推断法律结论。时间均按北京时间输入，历史区间结束时间为排他边界。</p>
      <Space wrap className="workflow-form">
        <label className="field">判断业务时点（北京时间）<Input aria-label="法律判断业务时点（北京时间）" type="datetime-local" value={businessAtInput} onChange={(event) => setBusinessAtInput(event.target.value)} /></label>
        <label className="field">证据截止时点（北京时间）<Input aria-label="法律事实证据截止时点（北京时间）" type="datetime-local" value={evidenceCutoff} onChange={(event) => setEvidenceCutoff(event.target.value)} /></label>
        <Button onClick={submitBusinessTime}>读取该时点的事实</Button>
      </Space>
      {caseDetail.isError ? <Alert type="error" showIcon title="案件材料暂不可用" description={errorMessage(caseDetail.error)} action={<Button onClick={() => void caseDetail.refetch()}>重试</Button>} /> : null}
      {factQuery.isLoading ? <p role="status">正在读取该时点的事实…</p> : null}
      {factQuery.isError ? <Alert type="error" showIcon title="法律适用事实暂不可用" description={errorMessage(factQuery.error)} action={<Button onClick={() => void factQuery.refetch()}>重试</Button>} /> : null}
      {subjects.data && !canManage ? <Alert type="info" showIcon title="当前案件为只读状态，可以查看事实，不能记录新版本。" /> : null}
      {subjects.isError ? <Alert type="error" showIcon title="本案主体绑定暂不可用" description={errorMessage(subjects.error)} action={<Button onClick={() => void subjects.refetch()}>重试</Button>} /> : null}

      <section className="workflow-form" aria-label="法律适用事实材料依据">
        <h3>选择本案固定材料依据</h3>
        <Select aria-label="选择事实依据的材料版本" value={materialKey || undefined} onChange={chooseMaterialVersion} placeholder="选择材料和固定版本" options={options.map((item) => ({ value: item.key, label: `${item.role} · ${item.purpose} · v${item.version}` }))} />
        {caseDetail.data && !caseDetail.isError && options.length === 0 ? <Empty description="本案暂无可引用的固定材料版本。" /> : null}
        {selectedMaterial && parsedDocuments.data ? <Select aria-label="选择事实依据的固定解析版本" value={parsedDocumentId || undefined} onChange={(value: string) => { setParsedDocumentId(value); setPageOffset(0); setPageStack([]); setAnchor(null); }} placeholder="选择固定解析版本" options={parsedDocuments.data.items.map((item) => ({ value: item.parsed_document_id, disabled: item.status !== "ready", label: `${item.parser_id} ${item.parser_version} · ${item.status} · ${item.created_at}` }))} /> : null}
        {selectedMaterial && parsedDocuments.data?.items.length === 0 ? <Empty description="所选材料版本还没有固定解析记录。" /> : null}
        {parsedDocuments.isError ? <Alert type="error" showIcon title="固定解析记录暂不可用" description={errorMessage(parsedDocuments.error)} action={<Button onClick={() => void parsedDocuments.refetch()}>重试</Button>} /> : null}
        {parsedPage.isError ? <Alert type="error" showIcon title="固定材料原文暂不可用" description={errorMessage(parsedPage.error)} action={<Button onClick={() => void parsedPage.refetch()}>重试</Button>} /> : null}
        {parsedPage.data ? <>
          <p>{parsedPage.data.filename} · 材料 v{parsedPage.data.material_version} · 固定解析 {parsedPage.data.parsed_document_id} · 字符 {parsedPage.data.offset + 1}–{parsedPage.data.offset + [...parsedPage.data.text].length} / {parsedPage.data.total_runes}</p>
          <pre ref={textRef} tabIndex={0} aria-label="法律适用事实依据原文，可选择引用" onMouseUp={captureAnchor}>{parsedPage.data.text}</pre>
          <Space>
            <Button disabled={pageStack.length === 0 || parsedPage.isFetching} onClick={() => { const stack = [...pageStack]; setPageOffset(stack.pop() ?? 0); setPageStack(stack); setAnchor(null); }}>上一段</Button>
            <Button disabled={parsedPage.data.next_offset === null || parsedPage.isFetching} onClick={() => { if (parsedPage.data?.next_offset == null) return; setPageStack((stack) => [...stack, pageOffset]); setPageOffset(parsedPage.data.next_offset); setAnchor(null); }}>下一段</Button>
          </Space>
        </> : null}
        {anchor ? <div className="subject-anchor-selected" data-material-id={anchor.material_id} data-document-id={anchor.document_id} data-parsed-document-id={anchor.parsed_document_id}><strong>{anchor.filename} · v{anchor.material_version} · {anchor.start}–{anchor.end}</strong><blockquote>{anchor.quote}</blockquote><Button onClick={() => setAnchor(null)}>清除所选依据</Button></div> : null}
      </section>

      {factQuery.data ? <div className="legal-applicability-fact-list">
        {legalApplicabilityDimensions.map((dimension, dimensionIndex) => {
          const row = factQuery.data.facts.find((item) => item.dimension === dimension);
          if (!row) return null;
          const historyQuery = histories[dimensionIndex];
          const history = historyQuery?.data;
          const draft = drafts[dimension] ?? initialDraft();
          const side = partySide(dimension);
          const bindings = partyDimension(dimension) ? eligiblePartyBindings(subjects.data?.items ?? [], side) : [];
          return <section className="case-subject-add" key={dimension} aria-label={labels[dimension]}>
            <h3>{labels[dimension]}</h3>
            <p>该时点结果：<StatusText tone={row.status === "established" ? "good" : "attention"}>{row.status === "established" ? values[dimension].find((item) => item.value === row.value)?.label ?? row.value : unknownReasons.find((item) => item.value === row.unknown_reason)?.label ?? "未知"}</StatusText>{row.fact ? ` · 已记录第 ${row.fact.version} 版 · ${row.fact.basis_note}` : " · 尚无已记录事实"}</p>
            {row.fact ? <p>事实时间：{row.fact.temporal_basis === "current_state" ? "当前观察" : `${formatDateTime(row.fact.valid_from)} 至 ${row.fact.valid_until ? formatDateTime(row.fact.valid_until) : "未设截止"}`} · 记录于 {formatDateTime(row.fact.created_at)}</p> : null}
            {row.fact?.party ? <p>主体范围：{row.fact.party.side === "buyer" ? "采购方" : "供应方"} · {row.fact.party.scope_note} · 绑定 v{row.fact.party.binding_version} / 身份 v{row.fact.party.subject_version}</p> : null}
            {row.fact?.basis_refs.map((ref, index) => <div className="subject-anchor-selected" key={`${ref.material_id}:${ref.material_version}:${ref.start}:${index}`}><p>依据：{ref.filename} · 材料 v{ref.material_version} · {ref.start}–{ref.end}</p><blockquote>{ref.quote}</blockquote></div>)}
            <div aria-label={`${labels[dimension]}历史版本`}>
              <h4>版本历史（当前写入头 v{history?.current_version ?? "…"}）</h4>
              {historyQuery?.isError ? <Alert type="error" showIcon title="事实历史暂不可用，不能追加版本" description={errorMessage(historyQuery.error)} action={<Button onClick={() => void historyQuery.refetch()}>重试</Button>} /> : null}
              {historyQuery?.isLoading ? <p role="status">正在读取事实版本历史…</p> : null}
              {history?.items.map((version) => <div className="subject-anchor-selected" key={`${version.fact_id}:${version.version}`}>
                <p>第 {version.version} 版 · {version.status === "established" ? values[dimension].find((option) => option.value === version.value)?.label ?? version.value : unknownReasons.find((option) => option.value === version.unknown_reason)?.label ?? "未知"} · {formatDateTime(version.created_at)}</p>
                <p>{version.basis_note}</p>
                {version.basis_refs.map((ref, index) => <div key={`${ref.parsed_document_id}:${ref.start}:${index}`}><p>{ref.filename} · 材料 v{ref.material_version} · {ref.start}–{ref.end}</p><blockquote>{ref.quote}</blockquote></div>)}
              </div>)}
              {history && history.items.length === 0 ? <Empty description="当前没有已记录的事实版本。" /> : null}
              <Space>
                <Button disabled={!history || !(historyStacks[dimension]?.length)} onClick={() => {
                  const stack = [...(historyStacks[dimension] ?? [])];
                  const previous = stack.pop() ?? null;
                  setHistoryStacks((current) => ({ ...current, [dimension]: stack }));
                  setHistoryCursors((current) => ({ ...current, [dimension]: previous }));
                }}>上一页历史</Button>
                <Button disabled={!history?.next_cursor} onClick={() => {
                  if (!history?.next_cursor) return;
                  setHistoryStacks((current) => ({ ...current, [dimension]: [...(current[dimension] ?? []), historyCursors[dimension] ?? null] }));
                  setHistoryCursors((current) => ({ ...current, [dimension]: history.next_cursor }));
                }}>下一页历史</Button>
              </Space>
            </div>
            {canManage ? <>
              <Space wrap>
                <Select aria-label={`${labels[dimension]}事实状态`} value={draft.status} options={[{ value: "established", label: "已确认" }, { value: "unknown", label: "未知" }]} onChange={(status: LegalApplicabilityStatus) => updateDraft(dimension, { status, value: status === "established" ? draft.value : "", unknown_reason: status === "unknown" ? draft.unknown_reason : "insufficient_evidence" })} />
                {draft.status === "established" ? <Select aria-label={`${labels[dimension]}受控取值`} value={draft.value || undefined} placeholder="选择受控取值" options={values[dimension]} onChange={(value: string) => updateDraft(dimension, { value })} /> : <Select aria-label={`${labels[dimension]}未知原因`} value={draft.unknown_reason} options={unknownReasons} onChange={(unknown_reason: LegalApplicabilityUnknownReason) => updateDraft(dimension, { unknown_reason })} />}
                <Select aria-label={`${labels[dimension]}时间范围`} value={draft.temporal_basis} options={[{ value: "current_state", label: "当前观察" }, { value: "historical_interval", label: "历史有效区间" }]} onChange={(temporal_basis: LegalApplicabilityTemporalBasis) => updateDraft(dimension, { temporal_basis })} />
              </Space>
              {draft.temporal_basis === "historical_interval" ? <Space wrap>
                <label className="field">有效起点（北京时间）<Input aria-label={`${labels[dimension]}有效起点（北京时间）`} type="datetime-local" value={draft.valid_from} onChange={(event) => updateDraft(dimension, { valid_from: event.target.value })} /></label>
                <label className="field">排他有效终点（北京时间）<Input aria-label={`${labels[dimension]}有效终点（北京时间）`} type="datetime-local" value={draft.valid_until} onChange={(event) => updateDraft(dimension, { valid_until: event.target.value })} /></label>
              </Space> : null}
              {partyDimension(dimension) ? <Space wrap>
                <Select aria-label={`${labels[dimension]}本案主体绑定`} value={draft.binding_id || undefined} placeholder={`${side === "buyer" ? "选择采购方" : "选择供应方"}当前有效绑定`} options={bindings.map((binding) => ({ value: binding.binding_id, label: `${binding.subject.display_name} · 绑定 v${binding.binding_version} · 身份 v${binding.subject.version}` }))} onChange={(binding_id: string) => updateDraft(dimension, { binding_id })} />
                <Input aria-label={`${labels[dimension]}主体范围说明`} value={draft.scope_note} onChange={(event) => updateDraft(dimension, { scope_note: event.target.value })} placeholder="说明该绑定在本事实中的主体范围" maxLength={500} />
              </Space> : null}
              <Input.TextArea aria-label={`${labels[dimension]}依据说明`} value={draft.basis_note} onChange={(event) => updateDraft(dimension, { basis_note: event.target.value })} placeholder="说明事实依据；该说明不能替代材料原文" maxLength={2000} rows={2} />
              <Button type="primary" disabled={!history || !history.can_manage || historyQuery?.isError || (!row.fact && row.status === "unknown" && !draft.basis_note.trim() && draft.unknown_reason !== "insufficient_evidence")} loading={savingDimension === dimension} onClick={() => void saveFact(dimension)}>追加事实版本</Button>
            </> : null}
          </section>;
        })}
      </div> : null}
      {saveError ? <Alert type="error" showIcon title={saveError} /> : null}
      {saveNote ? <Alert type="success" showIcon title={saveNote} /> : null}
    </Card>
  );
}
