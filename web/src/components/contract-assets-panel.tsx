import { Alert, Button, Card, Input, Select, Tag } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  createContractAssetHandover,
  listContractAcceptances,
  listContractAssets,
  listContractDeliveries,
  type ContractAcceptance,
  type ContractAssetHandover,
  type ContractAssetList,
  type ContractDelivery,
  type ContractDetail,
} from "../api/contracts";
import { errorMessage } from "../api/http";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import { useCaseDetail, useCases } from "../api/queries";
import type { Actor } from "../api/types";
import { formatDateTime } from "../domain/review";
import type { WireSchemas } from "../api/wire.generated";

function actorScope(actor: Actor | null): string {
  return actor === null ? "anonymous" :
    [actor.audience, actor.tenant_id, actor.org_id, actor.id, actor.role].join("\u0000");
}

function localDateTimeValue(): string {
  const now = new Date();
  const local = new Date(now.getTime() - now.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

function toRFC3339(value: string): string {
  const parsed = new Date(value);
  return value && Number.isFinite(parsed.getTime()) ? parsed.toISOString() : "";
}

function fixedAssetLabel(status: ContractAssetList["fixed_asset_status"]): { text: string; color?: string } {
  if (status === "true") return { text: "冻结修订标记为固定资产", color: "blue" };
  if (status === "not_applicable") return { text: "冻结修订明确不适用固定资产移交" };
  return { text: "固定资产属性未知" };
}

function verificationLabel(status: "unknown" | "verified"): { text: string; color?: string } {
  return status === "verified" ? { text: "目录身份已核验", color: "green" } : { text: "核验状态未知" };
}

function bookingLabel(status: ContractAssetHandover["booking_status"]): { text: string; color?: string } {
  switch (status) {
    case "pending_booking": return { text: "尚无 ERP 入账回执", color: "orange" };
    case "partial_booking": return { text: "ERP 回执记录部分入账", color: "blue" };
    case "booked": return { text: "ERP 回执记录已入账", color: "green" };
    case "unknown": return { text: "ERP 入账状态未知" };
  }
}

function acceptanceLabel(item: ContractAcceptance): string {
  const result = item.result === "passed" ? "已通过" : item.result === "partial" ? "部分通过" : "未通过";
  return `${item.actual_at} · ${result} · 验收区间 [${item.scope_start}, ${item.scope_end}) ${item.unit} · 已通过数量 ${item.accepted_quantity} ${item.unit}`;
}

function evidenceMaterialRef(
  source: NonNullable<ReturnType<typeof useCaseDetail>["data"]>,
  documentId: string,
): WireSchemas["MaterialRef"] | null {
  const version = (source.material_versions ?? []).find((entry) => entry.document_id === documentId);
  const document = source.documents.find((entry) => entry.id === documentId);
  if (!version || !document) return null;
  return {
    material_id: version.material_id,
    material_version_id: version.document_id,
    sha256: document.digest,
    purpose: "supporting",
  };
}

export function ContractAssetsPanel({ detail, actor, canManage, onRefresh }: {
  detail: ContractDetail;
  actor: Actor | null;
  canManage: boolean;
  onRefresh: () => Promise<void>;
}) {
  const contractId = detail.summary.contract_id;
  const revisionId = detail.current_revision_id;
  const rowVersion = detail.summary.row_version;
  const identity = actorScope(actor);
  const active = detail.summary.contract_state === "registered" || detail.summary.contract_state === "performing";
  const cases = useCases();
  const [caseId, setCaseId] = useState<string>();
  const caseDetail = useCaseDetail(caseId ?? "");
  const [materialId, setMaterialId] = useState<string>();
  const [assets, setAssets] = useState<ContractAssetList | null>(null);
  const [deliveries, setDeliveries] = useState<ContractDelivery[]>([]);
  const [acceptances, setAcceptances] = useState<ContractAcceptance[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [assetPlanId, setAssetPlanId] = useState<string>();
  const [deliveryId, setDeliveryId] = useState<string>();
  const [acceptanceId, setAcceptanceId] = useState<string>();
  const [scopeStart, setScopeStart] = useState("");
  const [scopeEnd, setScopeEnd] = useState("");
  const [handedOverAt, setHandedOverAt] = useState(localDateTimeValue);
  const [location, setLocation] = useState("");
  const [transferNote, setTransferNote] = useState("");
  const requestGeneration = useRef(0);
  const identityRef = useRef({ contractId, revisionId, identity });
  identityRef.current = { contractId, revisionId, identity };

  function isCurrent(generation: number): boolean {
    const current = identityRef.current;
    return generation === requestGeneration.current && current.contractId === contractId &&
      current.revisionId === revisionId && current.identity === identity;
  }

  useEffect(() => {
    const generation = ++requestGeneration.current;
    const controller = new AbortController();
    setAssets(null);
    setDeliveries([]);
    setAcceptances([]);
    setLoading(false);
    setError(null);
    setMessage(null);
    setAssetPlanId(undefined);
    setDeliveryId(undefined);
    setAcceptanceId(undefined);
    setCaseId(undefined);
    setMaterialId(undefined);
    if (!actor || !active || !revisionId) {
      return () => {
        controller.abort();
        requestGeneration.current++;
      };
    }
    setLoading(true);
    void Promise.all([
      listContractAssets(contractId, controller.signal),
      listContractDeliveries(contractId, controller.signal),
      listContractAcceptances(contractId, controller.signal),
    ]).then(([assetResult, deliveryResult, acceptanceResult]) => {
      if (!isCurrent(generation)) return;
      if (assetResult.contract_id !== contractId || assetResult.row_version !== deliveryResult.row_version ||
          assetResult.row_version !== acceptanceResult.row_version) {
        throw new Error("合同资产与履约事实版本不一致，请刷新合同后重试");
      }
      setAssets(assetResult);
      setDeliveries(deliveryResult.items);
      setAcceptances(acceptanceResult.items);
    }).catch((caught: unknown) => {
      if (!isCurrent(generation)) return;
      setAssets(null);
      setDeliveries([]);
      setAcceptances([]);
      setError(errorMessage(caught));
    }).finally(() => {
      if (isCurrent(generation)) setLoading(false);
    });
    return () => {
      controller.abort();
      requestGeneration.current++;
    };
  }, [contractId, revisionId, rowVersion, identity, active]);

  const selectedPlan = (assets?.items ?? []).find((item) => item.contract_revision_id === revisionId && item.asset_plan_id === assetPlanId);
  const planDeliveries = deliveries.filter((item) => item.contract_revision_id === revisionId && item.item_id === selectedPlan?.item_id);
  const planAcceptances = acceptances.filter((item) => item.contract_revision_id === revisionId &&
    item.item_id === selectedPlan?.item_id && item.delivery_id === deliveryId && item.result !== "failed");
  const selectedDelivery = planDeliveries.find((item) => item.delivery_id === deliveryId);
  const selectedMaterial = useMemo(() => caseDetail.data && materialId
    ? evidenceMaterialRef(caseDetail.data, materialId) : null, [caseDetail.data, materialId]);
  const materials = useMemo(() => {
    const source = caseDetail.data;
    if (!source || caseDetail.isError) return [];
    const documents = new Map(source.documents.map((document) => [document.id, document]));
    return (source.material_versions ?? []).flatMap((version) => {
      const document = documents.get(version.document_id);
      return document ? [{ value: version.document_id,
        label: `${document.filename} · 版本 ${version.version} · SHA-256 ${document.digest}` }] : [];
    });
  }, [caseDetail.data, caseDetail.isError]);

  async function createHandover() {
    if (!actor || !revisionId || !assets || !selectedPlan || !selectedDelivery || !acceptanceId ||
        !selectedMaterial || !location.trim() || !transferNote.trim() || !scopeStart.trim() || !scopeEnd.trim() ||
        !toRFC3339(handedOverAt) || !canCreate || busy) return;
    const generation = requestGeneration.current;
    const input = {
      expected_row_version: rowVersion,
      contract_revision_id: revisionId,
      asset_plan_id: selectedPlan.asset_plan_id,
      delivery_id: selectedDelivery.delivery_id,
      acceptance_id: acceptanceId,
      scope_start: scopeStart.trim(),
      scope_end: scopeEnd.trim(),
      handed_over_at: toRFC3339(handedOverAt),
      location: location.trim(),
      transfer_note: transferNote.trim(),
      evidence: [selectedMaterial],
    } satisfies WireSchemas["ContractAssetHandoverRequest"];
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const pending = await pendingMutationKey(
        "clm-asset-handover-create",
        idempotencyActorScope(actor),
        `${contractId}:${revisionId}:${selectedPlan.asset_plan_id}:${selectedDelivery.delivery_id}:${acceptanceId}`,
        input,
      );
      if (!isCurrent(generation)) {
        clearPendingMutationKey(pending.storageKey);
        return;
      }
      const result = await createContractAssetHandover(contractId, input, pending.key);
      if (result.contract_id !== contractId || result.contract_revision_id !== revisionId ||
          result.asset_plan_id !== selectedPlan.asset_plan_id || result.contract_row_version <= rowVersion) {
        throw new Error("资产移交响应与当前合同修订或版本不匹配");
      }
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        setMessage("人工移交已登记；浏览器未能清理安全重试标识，请勿重复提交。");
      }
      if (!isCurrent(generation)) return;
      setMessage((current) => current ?? "人工移交事实已登记；ERP 入账回执由可信来源单独记录。");
      try {
        await onRefresh();
      } catch (caught) {
        if (isCurrent(generation)) {
          setAssets(null);
          setDeliveries([]);
          setAcceptances([]);
          setMessage(`人工移交已登记，但合同版本刷新失败：${errorMessage(caught)}`);
        }
      }
    } catch (caught) {
      if (isCurrent(generation)) {
        setAssets(null);
        setDeliveries([]);
        setAcceptances([]);
        setError(errorMessage(caught));
      }
    } finally {
      if (isCurrent(generation)) setBusy(false);
    }
  }

  const versionAligned = assets !== null && assets.row_version === rowVersion;
  const canCreate = Boolean(actor && canManage && active && revisionId && !loading && !busy && versionAligned &&
    assets?.fixed_asset_status === "true" && selectedPlan?.fixed_asset_status === "true" && selectedPlan.plan_status === "known");
  const currentAssets = (assets?.items ?? []).filter((item) => item.contract_revision_id === revisionId);
  const historicalAssets = (assets?.items ?? []).filter((item) => item.contract_revision_id !== revisionId);

  if (!active) return null;
  const assetStatus = assets ? fixedAssetLabel(assets.fixed_asset_status) : null;
  return <section className="workflow-panel" aria-labelledby="contract-assets-heading">
    <h3 id="contract-assets-heading">固定资产计划、人工移交与 ERP 入账</h3>
    <p className="meta">资产计划来自冻结合同修订。人工移交、组织目录身份、部门归属核验和 ERP 入账回执是不同事实；本页面不会调用或写回 ERP。</p>
    {!revisionId ? <Alert type="warning" showIcon message="当前没有合同冻结修订，资产计划不可用。" /> : null}
    {!actor ? <Alert type="warning" showIcon message="访问身份不可用，资产与履约事实未读取。" /> : null}
    {assetStatus ? <p><Tag color={assetStatus.color}>{assetStatus.text}</Tag></p> : null}
    {assets?.fixed_asset_status === "not_applicable" ? <Alert type="info" showIcon message="该冻结修订明确标记为非固定资产，不适用资产移交。" /> : null}
    {assets?.fixed_asset_status === "unknown" ? <Alert type="warning" showIcon message="固定资产属性未知；不能按非固定资产处理，也不能登记移交。" /> : null}
    {error ? <Alert type="error" showIcon message={error} /> : null}
    {message ? <Alert type="info" showIcon message={message} /> : null}
    {!versionAligned && assets && !loading ? <Alert type="warning" showIcon message="资产或履约事实版本与合同详情不同步，暂不允许提交。请刷新合同。" /> : null}
    {loading ? <p role="status">正在读取冻结资产计划及履约事实。</p> : null}
    {!loading && assets && currentAssets.length === 0 ? <p>当前冻结修订没有可用的资产计划记录。</p> : null}
    {currentAssets.map((item) => <Card size="small" key={`${item.contract_revision_id}:${item.asset_plan_id}`} title={item.name || "未命名资产计划"} style={{ marginBottom: 12 }}>
      <p>计划数量：{item.planned_quantity ?? "未知"} {item.unit ?? ""} · 计划状态：{item.plan_status === "known" ? "已核对" : "未知"}</p>
      <p>部门计划值：{item.department_name || "未提供名称"}（{item.department_code || "未提供代码"}） · 部门主数据核验：{item.department_verification === "verified" ? "已核验" : "未知"}</p>
      <p>计划保管人 ID：{item.custodian_user_id || "未指定"} · 这不证明部门归属或移交授权。</p>
      {item.handovers.map((handover) => <article key={handover.handover_id} style={{ borderTop: "1px solid #f0f0f0", paddingTop: 8, marginTop: 8 }}>
        <p><Tag>人工移交事实</Tag>{handover.quantity} {handover.unit} · 范围 [{handover.scope_start}, {handover.scope_end}) · {formatDateTime(handover.handed_over_at)} · {handover.location}</p>
        <p>{handover.transfer_note}</p>
        <p>部门归属核验：{handover.department_verification === "verified" ? "已核验" : "未知"}；目录用户核验：{verificationLabel(handover.custodian_verification).text}；整体分配核验：{handover.assignment_verification === "verified" ? "已核验" : "未知"}。</p>
        {handover.custodian_verification === "verified" ? <p>目录中的当时用户：{handover.custodian_display_name || handover.custodian_user_id} · 组织 ID {handover.custodian_org_id || "未知"} · 来源 {handover.custodian_directory_source || "未知"} · 核验时间 {handover.custodian_directory_checked_at ? formatDateTime(handover.custodian_directory_checked_at) : "未知"}。目录身份不等于部门主数据或移交权限。</p> : <p>目录用户身份未知；不会据此推断部门归属或移交权限。</p>}
        <p><Tag color={bookingLabel(handover.booking_status).color}>{bookingLabel(handover.booking_status).text}</Tag>入账状态独立于部门和分配核验。</p>
        {handover.evidence_redacted ? <p>部分案件材料摘要因当前授权范围已隐藏。</p> : handover.evidence.length ? <ul>{handover.evidence.map((entry) => <li key={`${entry.material_id}:${entry.material_version_id}`}>{entry.filename} · 版本 {entry.material_version} · SHA-256 {entry.digest}</li>)}</ul> : <p>没有可见的移交证据摘要。</p>}
        {handover.receipts.length ? <div><strong>可信 ERP 回执与冲正</strong>{handover.receipts.map((receipt) => <p key={receipt.receipt_id}><Tag color={receipt.event_type === "receipt" ? "green" : "orange"}>{receipt.event_type === "receipt" ? "ERP 入账回执" : "ERP 冲正回执"}</Tag>{receipt.quantity} {receipt.unit} · 资产代码 {receipt.asset_code || "未知"} · 来源事件 {receipt.source_event_id} · 发生于 {formatDateTime(receipt.occurred_at)} · 记录于 {formatDateTime(receipt.recorded_at)}</p>)}</div> : <p>尚无可信 ERP 入账回执；人工移交记录不表示已经入账。</p>}
      </article>)}
    </Card>)}
    {historicalAssets.length ? <details><summary>历史冻结修订资产计划与记录</summary>{historicalAssets.map((item) => <Card size="small" key={`${item.contract_revision_id}:${item.asset_plan_id}`} title={`${item.name || "未命名资产计划"} · 历史修订`} style={{ marginTop: 8 }}>
      <p>修订 {item.contract_revision_id} · {fixedAssetLabel(item.fixed_asset_status).text} · 计划数量 {item.planned_quantity ?? "未知"} {item.unit ?? ""}</p>
      {item.handovers.map((handover) => <article key={handover.handover_id}>
        <p>人工移交 {handover.quantity} {handover.unit} · {formatDateTime(handover.handed_over_at)} · {handover.location}</p>
        <p>部门核验：{handover.department_verification === "verified" ? "已核验" : "未知"} · 目录用户核验：{handover.custodian_verification === "verified" ? "已核验" : "未知"} · 整体分配核验：{handover.assignment_verification === "verified" ? "已核验" : "未知"}</p>
        <p><Tag color={bookingLabel(handover.booking_status).color}>{bookingLabel(handover.booking_status).text}</Tag>入账独立于部门归属核验。</p>
        {handover.receipts.map((receipt) => <p key={receipt.receipt_id}><Tag>{receipt.event_type === "receipt" ? "ERP入账回执" : "ERP冲正回执"}</Tag>{receipt.quantity} {receipt.unit} · {receipt.asset_code || "代码未知"} · {receipt.source_event_id} · {formatDateTime(receipt.occurred_at)}</p>)}
      </article>)}
    </Card>)}</details> : null}
    {actor && canManage && assets?.fixed_asset_status === "true" && currentAssets.some((item) => item.plan_status === "known") ? <div className="contracts-page-section">
      <h4>追加人工移交事实</h4>
      <p className="meta">请填写实际人工交接区间；所选验收事实会分别显示验收区间和已通过数量，由服务端确认交接区间位于可交接前缀。范围使用原始十进制文本并按半开区间处理，是逻辑分配位置，不是设备序列号。服务端会重新核验合同管理权限；目录用户身份不代表部门归属或移交授权。提交不会创建 ERP 入账回执。</p>
      <div className="contracts-page-fields">
        <Select placeholder="选择当前修订的已核对资产计划" value={assetPlanId} options={currentAssets.filter((item) => item.fixed_asset_status === "true" && item.plan_status === "known").map((item) => ({ value: item.asset_plan_id, label: `${item.name || "未命名计划"} · ${item.planned_quantity ?? "数量未知"} ${item.unit ?? ""}` }))} onChange={(value) => { setAssetPlanId(value); setDeliveryId(undefined); setAcceptanceId(undefined); setScopeStart(""); setScopeEnd(""); }} />
        <Select placeholder="选择同一修订和商品的实际交付事实" value={deliveryId} disabled={!selectedPlan} options={planDeliveries.map((item) => ({ value: item.delivery_id, label: `${item.batch_reference} · ${item.quantity} ${item.unit} · ${item.location}` }))} onChange={(value) => { setDeliveryId(value); setAcceptanceId(undefined); setScopeStart(""); setScopeEnd(""); }} />
        <Select placeholder="选择该交付的验收事实" value={acceptanceId} disabled={!deliveryId} options={planAcceptances.map((item) => ({ value: item.acceptance_id, label: acceptanceLabel(item) }))} onChange={(value) => { setAcceptanceId(value); setScopeStart(""); setScopeEnd(""); }} />
        <Input aria-label="移交范围起点" placeholder="实际交接区间起点" inputMode="decimal" value={scopeStart} onChange={(event) => setScopeStart(event.target.value)} />
        <Input aria-label="移交范围终点" placeholder="实际交接区间终点" inputMode="decimal" value={scopeEnd} onChange={(event) => setScopeEnd(event.target.value)} />
        <Input aria-label="实际移交时间" type="datetime-local" value={handedOverAt} onChange={(event) => setHandedOverAt(event.target.value)} />
        <Input aria-label="资产移交地点" placeholder="实际移交地点" value={location} onChange={(event) => setLocation(event.target.value)} />
        <Input aria-label="资产移交说明" placeholder="移交说明" value={transferNote} onChange={(event) => setTransferNote(event.target.value)} />
      </div>
      <div className="contracts-page-fields">
        <Select placeholder="选择有权读取的案件证据" value={caseId} options={(cases.data ?? []).map((item) => ({ value: item.id, label: item.title }))} onChange={(value) => { setCaseId(value); setMaterialId(undefined); }} />
        <Select placeholder="选择证据材料版本" value={materialId} disabled={!caseId} options={materials} onChange={setMaterialId} />
      </div>
      <Button type="primary" loading={busy} disabled={!canCreate || !assetPlanId || !deliveryId || !acceptanceId || !selectedMaterial || !scopeStart.trim() || !scopeEnd.trim() || !toRFC3339(handedOverAt) || !location.trim() || !transferNote.trim()} onClick={() => void createHandover()}>追加人工移交事实</Button>
    </div> : null}
    {actor && !canManage ? <p className="meta">当前身份没有可用于写入人工移交事实的授权，仍可查看已授权合同范围内的事实。</p> : null}
    {!caseId && actor && canManage && assets?.fixed_asset_status === "true" ? <p className="meta">如需登记移交，请先选择有权读取的案件材料版本作为依据。</p> : null}
  </section>;
}
