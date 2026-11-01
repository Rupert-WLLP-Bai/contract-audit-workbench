import { Alert, Button, Input, Select, Tag } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  createContractAcceptance,
  createContractDelivery,
  listContractAcceptances,
  listContractDeliveries,
  recordContractRectification,
  type ContractAcceptance,
  type ContractAcceptanceList,
  type ContractDeliveryList,
  type ContractDetail,
  type MaterialRef,
} from "../api/contracts";
import { errorMessage } from "../api/http";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import { paths } from "../api/paths";
import { useCaseDetail, useCases } from "../api/queries";
import type { Actor } from "../api/types";
import type { WireSchemas } from "../api/wire.generated";

const itemId = (item: { item_id?: string }, index: number) => item.item_id ?? `missing-${index}`;
type DefectDraft = { description: string; responsible: string; remediation_due: string; failed_quantity: string };
function decimalUnits(value: string): bigint | null {
  const match = /^(\d{1,12})(?:\.(\d{1,6}))?$/.exec(value.trim());
  if (!match) return null;
  return BigInt(match[1] ?? "0") * 1_000_000n + BigInt((match[2] ?? "").padEnd(6, "0") || "0");
}
function decimalText(units: bigint): string {
  const whole = units / 1_000_000n;
  const fraction = String(units % 1_000_000n).padStart(6, "0");
  return `${whole}.${fraction}`;
}
function addDecimal(left: string, right: string): string {
  const a = decimalUnits(left); const b = decimalUnits(right);
  return a === null || b === null ? "" : decimalText(a + b);
}
function hasFailedQuantity(start: string, end: string, accepted: string): boolean {
  const a = decimalUnits(start); const b = decimalUnits(end); const c = decimalUnits(accepted);
  return a !== null && b !== null && c !== null && c < b - a;
}
function defectsMatchRange(start: string, end: string, accepted: string, defects: DefectDraft[]): boolean {
  const a = decimalUnits(start); const b = decimalUnits(end); const c = decimalUnits(accepted);
  if (a === null || b === null || c === null || b <= a || c > b - a) return false;
  const failed = b - a - c;
  if (failed === 0n) return defects.every((defect) => !defect.failed_quantity.trim());
  const provided = defects.filter((defect) => defect.failed_quantity.trim());
  if (provided.length === 0 || provided.some((defect) => !defect.description.trim() || !defect.responsible.trim() || !defect.remediation_due)) return false;
  let total = 0n;
  for (const defect of provided) {
    const amount = decimalUnits(defect.failed_quantity);
    if (amount === null || amount <= 0n) return false;
    total += amount;
  }
  return total === failed;
}

export function ContractFulfillmentPanel({ detail, actor, onRefresh }: {
  detail: ContractDetail;
  actor: Actor | null;
  onRefresh: () => Promise<void>;
}) {
  const contractId = detail.summary.contract_id;
  const revisionId = detail.current_revision_id;
  const actorKey = actor ? [actor.audience, actor.tenant_id, actor.org_id, actor.id, actor.role].join("\u0000") : "anonymous";
  const canManage = detail.summary.authority_mode === "native" && detail.draft !== undefined;
  const active = detail.summary.contract_state === "registered" || detail.summary.contract_state === "performing";
  const cases = useCases();
  const [caseId, setCaseId] = useState<string>();
  const caseDetail = useCaseDetail(caseId ?? "");
  const [materialId, setMaterialId] = useState<string>();
  const [deliveries, setDeliveries] = useState<ContractDeliveryList | null>(null);
  const [acceptances, setAcceptances] = useState<ContractAcceptanceList | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [deliveryItem, setDeliveryItem] = useState<string>();
  const [deliveryPlan, setDeliveryPlan] = useState<number>();
  const [batch, setBatch] = useState("");
  const [quantity, setQuantity] = useState("");
  const [actualAt, setActualAt] = useState("");
  const [location, setLocation] = useState("");
  const [deliveryNote, setDeliveryNote] = useState("");
  const [selectedDelivery, setSelectedDelivery] = useState<string>();
  const [acceptancePlan, setAcceptancePlan] = useState<number>();
  const [scopeStart, setScopeStart] = useState("0");
  const [scopeEnd, setScopeEnd] = useState("");
  const [acceptedQuantity, setAcceptedQuantity] = useState("");
  const [standard, setStandard] = useState("");
  const [inspectorNote, setInspectorNote] = useState("");
  const [reinspectionOf, setReinspectionOf] = useState<string>();
  const [defects, setDefects] = useState<DefectDraft[]>([{ description: "", responsible: "", remediation_due: "", failed_quantity: "" }]);
  const [rectificationNotes, setRectificationNotes] = useState<Record<string, string>>({});
  const requestGeneration = useRef(0);

  async function reload() {
    const generation = ++requestGeneration.current;
    if (!contractId || !active) {
      setDeliveries(null); setAcceptances(null); setLoading(false);
      return;
    }
    setLoading(true); setError(null);
    setDeliveries(null); setAcceptances(null);
    try {
      const [deliveryResult, acceptanceResult] = await Promise.all([
        listContractDeliveries(contractId), listContractAcceptances(contractId),
      ]);
      if (generation !== requestGeneration.current) return;
      setDeliveries(deliveryResult); setAcceptances(acceptanceResult);
    } catch (caught) {
      if (generation !== requestGeneration.current) return;
      setDeliveries(null); setAcceptances(null); setError(errorMessage(caught));
    } finally {
      if (generation === requestGeneration.current) setLoading(false);
    }
  }
  useEffect(() => {
    void reload();
    return () => { requestGeneration.current++; };
  }, [contractId, revisionId, active, actorKey]);

  const materials = useMemo(() => {
    const source = caseDetail.data;
    if (!source || caseDetail.isError) return [];
    const documents = new Map(source.documents.map((document) => [document.id, document]));
    return (source.material_versions ?? []).flatMap((version) => {
      const document = documents.get(version.document_id);
      if (!document) return [];
      return [{ value: version.document_id, label: `${document.filename} · 版本 ${version.version} · SHA-256 ${document.digest}`,
        ref: { material_id: version.material_id, material_version_id: version.document_id, sha256: document.digest, purpose: "supporting" as const } satisfies MaterialRef,
        caseId: source.id, filename: document.filename, digest: document.digest }];
    });
  }, [caseDetail.data, caseDetail.isError]);
  const selectedMaterial = materials.find((entry) => entry.value === materialId);
  const frozenItems = detail.draft?.items ?? [];
  const selectedDeliveryItem = frozenItems.find((item) => item.item_id === deliveryItem);
  const selectedDeliveryItemVerifiable = Boolean(selectedDeliveryItem?.quantity && selectedDeliveryItem.unit);
  const deliveryPlans = detail.draft?.delivery_plans ?? [];
  const acceptancePlans = detail.draft?.acceptance_plans ?? [];
  const deliveryById = new Map((deliveries?.items ?? []).map((entry) => [entry.delivery_id, entry]));
  const retestParent = (acceptances?.items ?? []).find((entry) => entry.acceptance_id === reinspectionOf);
  const retestStart = retestParent ? addDecimal(retestParent.scope_start, retestParent.accepted_quantity) : scopeStart;
  const retestEnd = retestParent?.scope_end ?? scopeEnd;
  const acceptanceHasFailedQuantity = hasFailedQuantity(retestStart, retestEnd, acceptedQuantity);
  const acceptanceDefectsMatch = defectsMatchRange(retestStart, retestEnd, acceptedQuantity, defects);
  const acceptanceHasChild = new Set((acceptances?.items ?? []).flatMap((entry) => entry.reinspection_of ? [entry.reinspection_of] : []));

  function chooseAcceptancePlan(value: number | undefined) {
    setAcceptancePlan(value);
    const selected = acceptancePlans.find((entry) => entry.sequence === value);
    if (selected?.criteria) setStandard(selected.criteria);
  }

  async function submitWithKey(operation: string, input: object, send: (key: string) => Promise<unknown>) {
    if (!actor) return;
    setBusy(true); setError(null); setMessage(null);
    let pending: Awaited<ReturnType<typeof pendingMutationKey>> | null = null;
    try {
      pending = await pendingMutationKey(operation, idempotencyActorScope(actor), contractId, input);
      await send(pending.key);
      clearPendingMutationKey(pending.storageKey);
      await onRefresh(); await reload();
      setMessage("履约事实已追加。原冻结修订、合同文本和既有事实保持不变。");
    } catch (caught) { setError(errorMessage(caught)); }
    finally { setBusy(false); }
  }

  async function saveDelivery() {
    if (!revisionId || !selectedMaterial || !deliveryItem || !actualAt) return;
    const input = { expected_row_version: detail.summary.row_version, contract_revision_id: revisionId,
      ...(deliveryPlan === undefined ? {} : { delivery_plan_sequence: deliveryPlan }), item_id: deliveryItem,
      batch_reference: batch.trim(), quantity: quantity.trim(), actual_at: new Date(actualAt).toISOString(),
      location: location.trim(), description: deliveryNote.trim(), evidence: [{ ...selectedMaterial.ref, purpose: "supporting" as const }] } satisfies WireSchemas["ContractDeliveryRequest"];
    await submitWithKey("clm-delivery-create", input, (key) => createContractDelivery(contractId, input, key));
  }

  async function saveAcceptance(parent?: ContractAcceptance) {
    if (!revisionId || !selectedMaterial) return;
    const delivery = parent ? deliveryById.get(parent.delivery_id) : deliveryById.get(selectedDelivery ?? "");
    if (!delivery) return;
    const start = parent ? addDecimal(parent.scope_start, parent.accepted_quantity) : scopeStart.trim();
    const end = parent ? parent.scope_end : scopeEnd.trim();
    const oldDefectIds = parent?.defects.map((defect) => parent.rectifications.find((fact) => fact.defect_id === defect.defect_id)?.rectification_id).filter((id): id is string => Boolean(id)) ?? [];
    const input = { expected_row_version: detail.summary.row_version, contract_revision_id: revisionId, delivery_id: delivery.delivery_id,
      ...(acceptancePlan === undefined ? {} : { acceptance_plan_sequence: acceptancePlan }), scope_start: start, scope_end: end,
      accepted_quantity: acceptedQuantity.trim(), ...(parent ? { reinspection_of: parent.acceptance_id, rectification_ids: oldDefectIds } : {}),
      actual_at: new Date(actualAt).toISOString(), standard: standard.trim(), inspector_note: inspectorNote.trim(),
      defects: defects.filter((defect) => defect.failed_quantity.trim()).map((defect) => ({ ...defect, description: defect.description.trim(), responsible: defect.responsible.trim(), failed_quantity: defect.failed_quantity.trim() })), evidence: [{ ...selectedMaterial.ref, purpose: "acceptance" }] } satisfies WireSchemas["ContractAcceptanceRequest"];
    await submitWithKey(parent ? "clm-acceptance-retest" : "clm-acceptance-create", input, (key) => createContractAcceptance(contractId, input, key));
    if (parent) setReinspectionOf(undefined);
  }

  async function saveRectification(parent: ContractAcceptance, defectId: string) {
    if (!revisionId || !selectedMaterial) return;
    const input = { expected_row_version: detail.summary.row_version, contract_revision_id: revisionId,
      defect_id: defectId, description: (rectificationNotes[defectId] ?? "").trim(), evidence: [{ ...selectedMaterial.ref, purpose: "acceptance" }] } satisfies WireSchemas["ContractRectificationRequest"];
    await submitWithKey("clm-rectification-create", input,
      async (key) => {
        const result = await recordContractRectification(contractId, parent.acceptance_id, input, key);
        return result;
      });
  }

  if (!active) return null;
  return <section className="contracts-page-section" aria-label="合同交付、验收与整改事实">
    <h3>交付、验收与整改事实</h3>
    <Alert type="info" showIcon title="只记录当前冻结修订对应的实际事实。范围按同一冻结单位记账，逻辑区间不代表具体设备序列号；未分配数量仍为待验收。案件证据撤权后由服务端隐藏或拒绝。" />
    {loading ? <p role="status">正在读取履约事实…</p> : null}
    {error ? <Alert type="error" showIcon title={error} /> : null}
    {message ? <Alert type="success" showIcon title={message} /> : null}
    {caseDetail.isError ? <Alert type="error" showIcon title="无法读取所选案件材料；材料元数据已隐藏。请检查案件授权。" /> : null}
    {(deliveries?.summary ?? []).length ? <div className="contracts-page-summary">
      {(deliveries?.summary ?? []).map((item) => <p key={`${item.contract_revision_id}-${item.item_id}`}><strong>{item.item_name || item.item_id}</strong>：计划 {item.planned_quantity ?? "未提供"} {item.unit ?? ""}；交付 {item.delivered_quantity}；合格 {item.qualified_quantity}；未分配待验 {item.unallocated_quantity}；待复验 {item.pending_reinspection_quantity}</p>)}
    </div> : null}
    <h4>实际交付</h4>
    {(deliveries?.items ?? []).map((item) => <div key={item.delivery_id}><p><Tag>交付</Tag>{item.item_name} · 批次 {item.batch_reference} · {item.quantity} {item.unit} · {item.actual_at} · {item.location} · 记录人 {item.recorded_by}</p><EvidenceFacts evidence={item.evidence} redacted={item.evidence_redacted} /></div>)}
    {canManage ? <>
      {frozenItems.some((item) => !item.quantity || !item.unit) ? <Alert type="warning" showIcon title="部分冻结商品行缺少可核验数量或单位，服务端不会推断；此类行不能登记交付事实。" /> : null}
      <div className="contracts-page-fields">
        <Select placeholder="选择实际交付的冻结商品行" value={deliveryItem} options={frozenItems.map((item, index) => ({ value: itemId(item, index), label: `${item.name ?? "商品行"} · 冻结计划 ${item.quantity ?? "未知"} ${item.unit ?? ""}` }))} onChange={setDeliveryItem} />
        {deliveryPlans.length ? <Select placeholder="选择冻结交付计划" value={deliveryPlan} options={deliveryPlans.filter((plan) => !plan.item_id || plan.item_id === deliveryItem).map((plan) => ({ value: plan.sequence, label: `期次 ${plan.sequence} · ${plan.planned_date ?? "未定日期"} · ${plan.quantity ?? "计划数量未定"}` }))} onChange={setDeliveryPlan} /> : null}
        <Input aria-label="批次引用" placeholder="批次引用" value={batch} onChange={(event) => setBatch(event.target.value)} />
        <Input aria-label="实际交付数量" placeholder="实际数量（最多 6 位小数）" value={quantity} onChange={(event) => setQuantity(event.target.value)} />
        <Input aria-label="实际交付时间" type="datetime-local" value={actualAt} onChange={(event) => setActualAt(event.target.value)} />
        <Input aria-label="交付地点" placeholder="实际交付地点" value={location} onChange={(event) => setLocation(event.target.value)} />
        <Input aria-label="交付范围说明" placeholder="范围或说明" value={deliveryNote} onChange={(event) => setDeliveryNote(event.target.value)} />
      </div>
      <EvidencePicker cases={cases.data ?? []} caseId={caseId} setCaseId={(value) => { setCaseId(value); setMaterialId(undefined); }} materials={materials} value={materialId} onChange={setMaterialId} />
      <Button type="primary" loading={busy} disabled={!actor || !revisionId || !deliveryItem || !selectedDeliveryItemVerifiable || !selectedMaterial || !batch.trim() || !quantity.trim() || !location.trim() || !actualAt} onClick={() => void saveDelivery()}>追加实际交付事实</Button>
    </> : <p>当前身份可查看已授权履约事实；没有可用于写入的管理授权。</p>}

    <h4>验收、缺陷与复验</h4>
    {(acceptances?.items ?? []).map((item) => <article key={item.acceptance_id}>
      <p><Tag color={item.result === "passed" ? "green" : item.result === "failed" ? "red" : "orange"}>{item.result === "passed" ? "通过" : item.result === "failed" ? "未通过" : "部分通过"}</Tag>范围 [{item.scope_start}, {item.scope_end}) {item.unit} · 合格 {item.accepted_quantity} · 未通过 {item.failed_quantity} · {item.actual_at} · 验收人 {item.recorded_by}{item.reinspection_of ? " · 复验" : ""}</p>
      <p>验收标准：{item.standard}；现场说明：{item.inspector_note || "无"}</p>
      <EvidenceFacts evidence={item.evidence} redacted={item.evidence_redacted} />
      {item.defects.map((defect) => <div key={defect.defect_id}>
        <p>问题：{defect.description}；责任方：{defect.responsible}；整改期限：{defect.remediation_due}；失败数量：{defect.failed_quantity}</p>
        {canManage && !(item.rectifications ?? []).some((fact) => fact.defect_id === defect.defect_id) ? <>
          <Input aria-label="整改说明" placeholder="整改事实说明" value={rectificationNotes[defect.defect_id] ?? ""} onChange={(event) => setRectificationNotes((current) => ({ ...current, [defect.defect_id]: event.target.value }))} />
          <Button loading={busy} disabled={!selectedMaterial || !rectificationNotes[defect.defect_id]?.trim()} onClick={() => void saveRectification(item, defect.defect_id)}>追加该问题的整改事实</Button>
        </> : null}
      </div>)}
      {item.rectifications.map((fact) => <div key={fact.rectification_id}><p>整改：{fact.description} · 记录人 {fact.recorded_by} · {fact.recorded_at}</p><EvidenceFacts evidence={fact.evidence} redacted={fact.evidence_redacted} /></div>)}
      {canManage && item.result !== "passed" && !acceptanceHasChild.has(item.acceptance_id) ? <Button onClick={() => { setReinspectionOf(item.acceptance_id); setSelectedDelivery(item.delivery_id); setScopeStart(addDecimal(item.scope_start, item.accepted_quantity)); setScopeEnd(item.scope_end); setAcceptedQuantity(item.failed_quantity); setDefects([{ description: "", responsible: "", remediation_due: "", failed_quantity: "" }]); }}>按失败后缀准备复验</Button> : null}
    </article>)}
    {canManage ? <>
      <div className="contracts-page-fields">
        {retestParent ? <Alert type="warning" showIcon title={`复验范围由父事实固定为 [${retestStart}, ${retestEnd}) ${retestParent.unit}。本次合格数量是该范围的前缀；若仍有失败后缀，需追加新的问题/整改事实。`} /> : null}
        {!retestParent ? <Select placeholder="选择待验收交付批次" value={selectedDelivery} options={(deliveries?.items ?? []).map((item) => ({ value: item.delivery_id, label: `${item.item_name} · ${item.batch_reference} · ${item.quantity} ${item.unit}` }))} onChange={(value) => { setSelectedDelivery(value); const selected = deliveryById.get(value); if (selected) setScopeEnd(selected.quantity); }} /> : null}
        {!retestParent ? <><Input aria-label="范围起点" placeholder="范围起点" value={scopeStart} onChange={(event) => setScopeStart(event.target.value)} /><Input aria-label="范围终点" placeholder="范围终点" value={scopeEnd} onChange={(event) => setScopeEnd(event.target.value)} /></> : null}
        {acceptancePlans.length ? <Select placeholder="选择冻结验收计划" value={acceptancePlan} options={acceptancePlans.map((plan) => ({ value: plan.sequence, label: `期次 ${plan.sequence} · ${plan.planned_date ?? "未定日期"} · ${plan.criteria ?? "按实际记录标准"}` }))} onChange={chooseAcceptancePlan} /> : null}
        <Input aria-label="本次合格数量" placeholder="本次合格数量" value={acceptedQuantity} onChange={(event) => setAcceptedQuantity(event.target.value)} />
        <Input aria-label="实际验收时间" type="datetime-local" value={actualAt} onChange={(event) => setActualAt(event.target.value)} />
        <Input aria-label="验收标准" placeholder="验收标准" value={standard} onChange={(event) => setStandard(event.target.value)} />
        <Input aria-label="验收现场说明" placeholder="验收人员和现场说明" value={inspectorNote} onChange={(event) => setInspectorNote(event.target.value)} />
        {acceptanceHasFailedQuantity ? defects.map((defect, index) => <div className="contracts-page-fields" key={`defect-${index}`}>
          <Input aria-label={`失败数量 ${index + 1}`} placeholder="本项失败数量" value={defect.failed_quantity} onChange={(event) => setDefects((current) => current.map((entry, i) => i === index ? { ...entry, failed_quantity: event.target.value } : entry))} />
          <Input aria-label={`验收问题 ${index + 1}`} placeholder="问题描述" value={defect.description} onChange={(event) => setDefects((current) => current.map((entry, i) => i === index ? { ...entry, description: event.target.value } : entry))} />
          <Input aria-label={`问题责任方 ${index + 1}`} placeholder="整改责任方" value={defect.responsible} onChange={(event) => setDefects((current) => current.map((entry, i) => i === index ? { ...entry, responsible: event.target.value } : entry))} />
          <Input aria-label={`整改期限 ${index + 1}`} type="date" value={defect.remediation_due} onChange={(event) => setDefects((current) => current.map((entry, i) => i === index ? { ...entry, remediation_due: event.target.value } : entry))} />
          {defects.length > 1 ? <Button onClick={() => setDefects((current) => current.filter((_, i) => i !== index))}>移除问题行</Button> : null}
        </div>) : null}
        {acceptanceHasFailedQuantity ? <Button onClick={() => setDefects((current) => [...current, { description: "", responsible: "", remediation_due: "", failed_quantity: "" }])}>添加另一项验收问题</Button> : null}
      </div>
      <EvidencePicker cases={cases.data ?? []} caseId={caseId} setCaseId={(value) => { setCaseId(value); setMaterialId(undefined); }} materials={materials} value={materialId} onChange={setMaterialId} />
      <Button type="primary" loading={busy} disabled={!actor || !revisionId || !selectedMaterial || !selectedDelivery || !actualAt || !standard.trim() || !retestEnd.trim() || !acceptedQuantity.trim() || !acceptanceDefectsMatch || (!!retestParent && retestParent.defects.some((defect) => !retestParent.rectifications.some((fact) => fact.defect_id === defect.defect_id)))} onClick={() => void saveAcceptance(retestParent)}>追加{retestParent ? "复验" : "验收"}事实</Button>
    </> : null}
  </section>;
}

function EvidencePicker({ cases, caseId, setCaseId, materials, value, onChange }: {
  cases: Array<{ id: string; title: string }>;
  caseId?: string;
  setCaseId: (value: string) => void;
  materials: Array<{ value: string; label: string }>;
  value?: string;
  onChange: (value: string) => void;
}) {
  return <div className="contracts-page-fields">
    <Select placeholder="选择有权读取的案件证据" value={caseId} options={cases.map((item) => ({ value: item.id, label: item.title }))} onChange={setCaseId} />
    <Select placeholder="选择证据材料版本" value={value} disabled={!caseId} options={materials.map(({ value: id, label }) => ({ value: id, label }))} onChange={onChange} />
  </div>;
}

function EvidenceFacts({ evidence, redacted }: { evidence: WireSchemas["ContractVersionMaterial"][]; redacted?: boolean }) {
  if (redacted) return <p>部分案件材料摘要因当前授权范围已隐藏。</p>;
  return evidence.length ? <ul>{evidence.map((material) => <li key={`${material.material_id}-${material.material_version_id}`}>
    <a href={paths.documentOriginal(material.case_id, material.material_version_id)} target="_blank" rel="noreferrer">{material.filename}</a>
    {` · 版本 ${material.material_version} · SHA-256 ${material.digest}`}
  </li>)}</ul> : <p>没有可见的案件材料摘要。</p>;
}
