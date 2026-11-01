import { Alert, Button, Checkbox, Input, Modal, Select, Tag } from "antd";
import { useEffect, useMemo, useState } from "react";
import {
  checkContractSigning,
  createContractSupplement,
  emptyDraft,
  listContractSupplements,
  listContractSigningRegistrations,
  registerContractSigning,
  type ContractDetail,
  type ContractSigningCheckResult,
  type ContractSigningRegistration,
  type ContractSupplement,
  type DraftContent,
  type MaterialRef,
} from "../api/contracts";
import { errorMessage } from "../api/http";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import { paths } from "../api/paths";
import { useCaseDetail, useCases } from "../api/queries";
import type { Actor } from "../api/types";
import type { WireSchemas } from "../api/wire.generated";

const comparisonScope = "all_structured_business_fields_and_restricted_fields" as const;

export function ContractSigningPanel({
  detail,
  actor,
  onRefresh,
  onOpenDraft,
}: {
  detail: ContractDetail;
  actor: Actor | null;
  onRefresh: () => Promise<void>;
  onOpenDraft: (id: string) => void;
}) {
  const cases = useCases();
  const [caseID, setCaseID] = useState<string>();
  const caseDetail = useCaseDetail(caseID ?? "");
  const [materialVersionID, setMaterialVersionID] = useState<string>();
  const [contentJSON, setContentJSON] = useState(() => JSON.stringify(detail.draft ?? emptyDraft(), null, 2));
  const [signingDate, setSigningDate] = useState("");
  const [legacyBasis, setLegacyBasis] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [check, setCheck] = useState<ContractSigningCheckResult | null>(null);
  const [registration, setRegistration] = useState<ContractSigningRegistration | null>(null);
  const [registrationLoaded, setRegistrationLoaded] = useState(false);
  const [supplements, setSupplements] = useState<ContractSupplement[]>([]);
  const [parentSupplement, setParentSupplement] = useState<ContractSupplement | null>(null);
  const [supplementOpen, setSupplementOpen] = useState(false);
  const [supplementDraft, setSupplementDraft] = useState(() => JSON.stringify(detail.draft ?? emptyDraft(), null, 2));
  const [supplementReason, setSupplementReason] = useState("");
  const [relationType, setRelationType] = useState<"supplementary_agreement" | "change_order">("supplementary_agreement");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setRegistration(null);
    setRegistrationLoaded(false);
    setSupplements([]);
    setParentSupplement(null);
    setError(null);
    if (!detail.summary.contract_id) return;
    void Promise.all([
      listContractSigningRegistrations(detail.summary.contract_id),
      listContractSupplements(detail.summary.contract_id),
    ]).then(([registrations, result]) => {
      if (!active) return;
      setRegistration(registrations[0] ?? null);
      setRegistrationLoaded(true);
      setSupplements(result.items ?? []);
      setParentSupplement(result.parent ?? null);
    }).catch((caught) => {
      if (active) setError(errorMessage(caught));
    });
    return () => { active = false; };
  }, [detail.summary.contract_id, detail.signing_registration]);

  const materials = useMemo(() => {
    const source = caseDetail.data;
    if (!source || caseDetail.isError) return [];
    const documents = new Map(source.documents.map((document) => [document.id, document]));
    return (source.material_versions ?? []).flatMap((version) => {
      const document = documents.get(version.document_id);
      if (!document) return [];
      return [{
        value: version.document_id,
        label: `${document.filename} · 版本 ${version.version} · SHA-256 ${document.digest}`,
        ref: { material_id: version.material_id, material_version_id: version.document_id, sha256: document.digest, purpose: "signed_contract" as const } satisfies MaterialRef,
        filename: document.filename,
        digest: document.digest,
      }];
    });
  }, [caseDetail.data, caseDetail.isError]);
  const selectedMaterial = materials.find((material) => material.value === materialVersionID);
  let signedContent: DraftContent | null = null;
  let jsonError: string | null = null;
  try {
    const parsed: unknown = JSON.parse(contentJSON);
    if (typeof parsed !== "object" || parsed === null || !("contract" in parsed)) throw new Error("需要包含 contract 对象的合同结构化内容");
    signedContent = parsed as DraftContent;
  } catch (caught) {
    jsonError = caught instanceof Error ? caught.message : "JSON 格式无效";
  }

  function clearCheck() {
    setCheck(null);
    setConfirmed(false);
    setMessage(null);
  }

  async function runCheck() {
    if (!detail.current_revision_id || !selectedMaterial || !signedContent || !confirmed) return;
    setBusy(true); setError(null); setMessage(null);
    try {
      const result = await checkContractSigning(detail.summary.contract_id, {
        baseline_revision_id: detail.current_revision_id,
        signed_evidence: selectedMaterial.ref,
        signed_content: signedContent as unknown as WireSchemas["DraftContent"],
        ...(legacyBasis.trim() ? { legacy_approval_basis: legacyBasis.trim() } : {}),
        manual_comparison: { confirmed: true, signed_material_digest: selectedMaterial.digest, contract_revision_id: detail.current_revision_id, scope: comparisonScope } as const,
      });
      setCheck(result);
    } catch (caught) { setCheck(null); setError(errorMessage(caught)); }
    finally { setBusy(false); }
  }

  async function register() {
    if (!actor || !detail.current_revision_id || !selectedMaterial || !signedContent || !check?.can_register || !signingDate || check.verification_status !== "human_transcription_confirmed") return;
    const buyer = signedContent.buyer?.purchasing_unit?.name?.trim();
    const supplier = signedContent.supplier?.name?.trim();
    const parties = [buyer ? { role: "buyer" as const, name: buyer } : null, supplier ? { role: "supplier" as const, name: supplier } : null].filter((party): party is { role: "buyer" | "supplier"; name: string } => party !== null);
    if (parties.length === 0) { setError("签署内容中没有可登记的采购方或供应方主体名称"); return; }
    const input = {
      expected_row_version: detail.summary.row_version,
      baseline_revision_id: detail.current_revision_id,
      signing_date: signingDate,
      signed_evidence: selectedMaterial.ref,
      signed_content: signedContent as unknown as WireSchemas["DraftContent"],
      manual_comparison: { confirmed: true, signed_material_digest: selectedMaterial.digest, contract_revision_id: detail.current_revision_id, scope: comparisonScope } as const,
      parties,
      signature_evidence_status: "document_only" as const,
      ...(legacyBasis.trim() ? { legacy_approval_basis: legacyBasis.trim() } : {}),
    };
    setBusy(true); setError(null); setMessage(null);
    let pending: Awaited<ReturnType<typeof pendingMutationKey>> | null = null;
    try {
      pending = await pendingMutationKey("contract-signing-register", idempotencyActorScope(actor), detail.summary.contract_id, input);
      const fact = await registerContractSigning(detail.summary.contract_id, input, pending.key);
      clearPendingMutationKey(pending.storageKey);
      setRegistration(fact); clearCheck(); setMessage("签署登记已追加；合同原文和既有审批事实未修改。");
      await onRefresh();
    } catch (caught) { setError(errorMessage(caught)); }
    finally { setBusy(false); }
  }

  async function createSupplement() {
    if (!actor || !detail.current_revision_id) return;
    let draft: DraftContent;
    try { draft = JSON.parse(supplementDraft) as DraftContent; }
    catch { setError("补充协议草稿 JSON 格式无效"); return; }
    if (!supplementReason.trim() || !draft || typeof draft !== "object" || !draft.contract) { setError("请填写理由并提供包含 contract 的结构化草稿"); return; }
    const input = { expected_row_version: detail.summary.row_version, parent_revision_id: detail.current_revision_id, relation_type: relationType, reason: supplementReason.trim(), draft };
    setBusy(true); setError(null);
    let pending: Awaited<ReturnType<typeof pendingMutationKey>> | null = null;
    try {
      pending = await pendingMutationKey("contract-supplement-create", idempotencyActorScope(actor), detail.summary.contract_id, input);
      const result = await createContractSupplement(detail.summary.contract_id, { ...input, draft: draft as unknown as WireSchemas["DraftContent"] }, pending.key);
      clearPendingMutationKey(pending.storageKey);
      setSupplementOpen(false); setSupplements((current) => [...current, result.supplement]); setSupplementReason("");
      await onRefresh(); onOpenDraft(result.contract.summary.contract_id);
    } catch (caught) { setError(errorMessage(caught)); }
    finally { setBusy(false); }
  }

  return <section className="contracts-page-section" aria-label="签署登记与补充协议">
    <h3>签署登记与补充协议</h3>
    {registration ? <Alert type="info" showIcon title="已有签署登记事实（只读）" description={<div>
      <p>签署日期：{registration.signing_date}；登记修订：{registration.contract_revision_id}</p>
      <p>签署主体：{registration.parties.map((party) => `${party.role}：${party.name}`).join("；")}</p>
      <p>原件材料：<a href={paths.documentOriginal(registration.signed_material.case_id, registration.signed_material.material_version_id)} target="_blank" rel="noreferrer">{registration.signed_material.filename}</a> · 版本 {registration.signed_material.material_version} · SHA-256 {registration.signed_material.digest}</p>
      <p>人工核对：{registration.comparison_confirmed_by} · {registration.comparison_confirmed_at}；电子签名/印章验真状态：{registration.signature_evidence_status === "document_only" ? "未验证" : registration.signature_evidence_status}</p>
      <p>登记人：{registration.registered_by} · {registration.registered_at}；审批依据：{registration.approval_evidence_status}</p>
      {registration.warnings.map((warning) => <p key={warning}>{warning}</p>)}
    </div>} /> : <>
      <Alert type="warning" showIcon title="此登记由人工转录并核对冻结修订的结构化字段，不会自动读取原件正文，也不验证电子签名或印章真实性。" />
      <div className="contracts-page-fields">
        <Select placeholder="选择有权读取的案件" value={caseID} loading={cases.isLoading} options={(cases.data ?? []).map((item) => ({ value: item.id, label: item.title }))} onChange={(value) => { setCaseID(value); setMaterialVersionID(undefined); clearCheck(); }} />
        <Select placeholder={caseID ? "选择已授权材料版本" : "先选择案件"} value={materialVersionID} disabled={!caseID || caseDetail.isLoading || caseDetail.isError} loading={caseDetail.isLoading} options={materials.map(({ value, label }) => ({ value, label }))} onChange={(value) => { setMaterialVersionID(value); clearCheck(); }} />
      </div>
      {caseDetail.isError ? <Alert type="error" showIcon title="无法读取该案件材料；材料元数据已隐藏。请检查案件读取授权后重试。" /> : null}
      {selectedMaterial && caseID ? <p className="meta"><a href={paths.documentOriginal(caseID, selectedMaterial.value)} target="_blank" rel="noreferrer">在新窗口打开授权原件</a>　{selectedMaterial.filename}　SHA-256 {selectedMaterial.digest}</p> : null}
      <label className="field field-wide">签署原件结构化转录（人工逐项核对）<Input.TextArea rows={10} value={contentJSON} onChange={(event) => { setContentJSON(event.target.value); clearCheck(); }} /></label>
      {jsonError ? <Alert type="error" title={jsonError} /> : null}
      <label className="field">签署日期<Input type="date" value={signingDate} onChange={(event) => { setSigningDate(event.target.value); clearCheck(); }} /></label>
      <label className="field field-wide">存量审批历史未知时的人工依据（仅限真实导入来源）<Input.TextArea rows={2} value={legacyBasis} onChange={(event) => { setLegacyBasis(event.target.value); clearCheck(); }} /></label>
      <Checkbox checked={confirmed} disabled={!selectedMaterial || !detail.current_revision_id || !signedContent || !!jsonError} onChange={(event) => { setConfirmed(event.target.checked); setCheck(null); }}>我已逐项将以上转录与所选原件及冻结修订对照；确认范围包括关键日期、附件、主体、金额、履约、资产与财务字段。此确认绑定所示材料摘要和修订，且不代表自动验真。</Checkbox>
      <div className="contracts-page-fields"><Button loading={busy} disabled={!registrationLoaded || !confirmed || !selectedMaterial || !detail.current_revision_id || !signedContent || !!jsonError} onClick={() => void runCheck()}>核对转录与冻结修订</Button>
        {check?.can_register ? <Button type="primary" loading={busy} disabled={!actor || !signingDate || !confirmed} onClick={() => void register()}>追加签署登记</Button> : null}
      </div>
      {check ? <Alert type={check.can_register ? "success" : "warning"} showIcon title={`审批历史：${check.approval_status}；结构化比较：${check.comparison_result}；登记许可：${check.can_register ? "可登记" : "拒绝"}`} description={<div>
        <p>人工转录确认状态：{check.verification_status}。服务端未自动提取或验证原件字段。</p>
        {check.fields.map((change) => <p key={change.path}>{change.path}：{change.change}</p>)}
        {check.restricted_fields.map((change) => <p key={change.path}>{change.path}：{change.changed === null ? "无法核验" : change.changed ? "有差异" : "无差异"}</p>)}
        {check.warnings.map((warning) => <p key={warning}>{warning}</p>)}
      </div>} /> : null}
    </>}
    {message ? <Alert type="success" showIcon title={message} /> : null}
    {error ? <Alert type="error" showIcon title={error} /> : null}
    <div className="contracts-page-fields"><Button onClick={() => { setSupplementDraft(JSON.stringify(detail.draft ?? emptyDraft(), null, 2)); setSupplementOpen(true); }} disabled={!actor || !detail.current_revision_id}>从当前不可变修订创建补充协议草稿</Button></div>
    {parentSupplement || supplements.length ? <div><h4>补充协议关系</h4>
      {parentSupplement ? <p><Tag>{parentSupplement.relation_type === "change_order" ? "变更单" : "补充协议"}</Tag>父合同 {parentSupplement.parent_contract_id} · 父修订 {parentSupplement.parent_revision_id} · {parentSupplement.reason} <Button type="link" onClick={() => onOpenDraft(parentSupplement.parent_contract_id)}>打开父合同</Button></p> : null}
      {supplements.map((item) => <p key={item.supplement_contract_id}><Tag>{item.relation_type === "change_order" ? "变更单" : "补充协议"}</Tag>子合同 {item.supplement_contract_id} · 父修订 {item.parent_revision_id} · {item.reason} <Button type="link" onClick={() => onOpenDraft(item.supplement_contract_id)}>打开草稿</Button></p>)}</div> : null}
    <Modal title="创建补充协议草稿" open={supplementOpen} onCancel={() => setSupplementOpen(false)} onOk={() => void createSupplement()} confirmLoading={busy} okText="创建草稿">
      <Alert type="info" title={`父合同修订固定为 ${detail.current_revision_id ?? "不可用"}；不会修改父合同、原文或审批事实。`} />
      <Select style={{ width: "100%", marginTop: 12 }} value={relationType} options={[{ value: "supplementary_agreement", label: "补充协议" }, { value: "change_order", label: "变更单" }]} onChange={setRelationType} />
      <Input.TextArea aria-label="创建理由" placeholder="创建理由" rows={2} value={supplementReason} onChange={(event) => setSupplementReason(event.target.value)} />
      <Input.TextArea aria-label="补充协议结构化草稿" rows={12} value={supplementDraft} onChange={(event) => setSupplementDraft(event.target.value)} />
    </Modal>
  </section>;
}
