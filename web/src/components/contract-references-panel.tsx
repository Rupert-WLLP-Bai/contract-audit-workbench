// File metadata (manifest mirror):
// Purpose: Renders ContractReferencesPanel, the contract references panel workbench UI, from the selected case/workspace projection and the module’s query/state inputs; it displays server-derived state and does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-05
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { Alert, Button, Select, Tag } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import {
  checkContractReferences,
  type ContractReferenceCheck,
  type DraftContent,
} from "../api/contracts";
import { errorMessage } from "../api/http";
import { queryKeys, useCases, useSession } from "../api/queries";
import { actorReadContextKey } from "../domain/actor-read-context";
import { caseOptionLabel } from "../domain/case-identity";
import { roleLabel } from "../domain/review";
import { subjectBindingStateLabel, subjectRoleLabel, subjectVerificationLabel } from "../domain/subjects";
import { preciseTime } from "../domain/time";
import { caseSelectPresentation } from "./case-select-presentation";
import { InvalidTimeDetail } from "./invalid-time-detail";
import { TechnicalDetail } from "./technical-detail";

function contractPartyRoleLabel(role: string): string {
  switch (role) {
    case "buyer": return "采购方";
    case "supplier": return "供应商";
    case "guarantor": return "担保方";
    case "consortium_member": return "联合体成员";
    case "other": return "其他角色";
    case "": return "未记录";
    default: return "待核对";
  }
}

function caseRoleLabel(role: string): string {
  switch (role) {
    case "customer": case "supplier": case "contractor": case "service_provider": case "other":
      return subjectRoleLabel(role);
    case "": return "未记录";
    default: return "待核对";
  }
}

function bindingStateLabel(state: string): string {
  switch (state) {
    case "active": case "inactive": case "pending_confirmation": return subjectBindingStateLabel(state);
    case "": return "绑定状态未记录";
    default: return "绑定状态待核对";
  }
}

function verificationStateLabel(state: string): string {
  switch (state) {
    case "pending_verification": case "verified": return subjectVerificationLabel(state, undefined);
    case "": return "未记录";
    default: return "待核对";
  }
}

function contractMaterialPurposeLabel(purpose: string): string {
  switch (purpose) {
    case "draft": return "合同草稿";
    case "signed_contract": return "已签合同";
    case "supporting": return "佐证材料";
    case "acceptance": return "验收材料";
    case "payment": return "付款材料";
    case "other": return "其他用途";
    case "": return "未记录";
    default: return "待核对";
  }
}

function warningLabel(warning: string): string {
  if (warning === "subject_pending_verification") return "存在尚未核验的主体";
  return warning === "" ? "未记录" : "待核对";
}

/**
 * Read-only view of the contract reference chain.
 *
 * The panel never writes. It calls the strict resolver, shows the frozen or
 * current subject/material pins, and links previews to the existing
 * document-service original endpoint. That endpoint re-checks the source case
 * on every request, so the link is not an authorization bypass.
 */
export function ContractReferencesPanel({
  contractID,
  draft,
}: {
  contractID: string | null;
  draft: DraftContent;
}) {
  const cases = useCases();
  const session = useSession();
  const cache = useQueryClient().getQueryCache();
  const listGeneration = useRef(0);
  const subscribeList = useCallback((notify: () => void) => cache.subscribe((event) => {
    if (event.query.queryKey.length !== queryKeys.cases.length || event.query.queryKey[0] !== queryKeys.cases[0]) return;
    if (event.type === "added" || event.type === "removed" ||
        (event.type === "updated" && (event.action.type === "fetch" || event.action.type === "invalidate"))) listGeneration.current += 1;
    notify();
  }), [cache]);
  const listVersion = useCallback(() => {
    const state = cache.find({ queryKey: queryKeys.cases, exact: true })?.state;
    return `${listGeneration.current}:${state?.dataUpdateCount ?? 0}:${state?.errorUpdateCount ?? 0}:${state?.fetchStatus ?? "missing"}:${state?.status ?? "missing"}:${state?.isInvalidated ?? true}`;
  }, [cache]);
  const listRevision = useSyncExternalStore(subscribeList, listVersion, listVersion);
  const [caseID, setCaseID] = useState<string | null>(null);
  const actorScope = actorReadContextKey(session.data ?? null);
  const sessionReady = session.isSuccess && !session.isError && !session.isFetching && actorScope !== "";
  const listState = cache.find({ queryKey: queryKeys.cases, exact: true })?.state;
  const listReady = sessionReady && cases.isSuccess && !cases.isError && !cases.isFetching &&
    !!listState && listState.status === "success" && listState.fetchStatus === "idle" && !listState.isInvalidated;
  const caseVisible = listReady && (caseID === null || cases.data.some((item) => item.id === caseID));
  // A refreshed authorization list also starts a new context, even if it contains the same case IDs.
  const context = useRef({ contractID, caseID, caseVisible, casesData: cases.data, actorScope, listRevision, generation: 0 });
  const request = useRef(0);
  const mounted = useRef(false);
  const [verification, setVerification] = useState<{
    generation: number;
    busy: boolean;
    check: ContractReferenceCheck | null;
    error: string | null;
  } | null>(null);
  const matchesContext = context.current.contractID === contractID && context.current.caseID === caseID &&
    context.current.caseVisible === caseVisible && context.current.casesData === cases.data && context.current.actorScope === actorScope && context.current.listRevision === listRevision;
  const visible = matchesContext && verification?.generation === context.current.generation ? verification : null;
  const check = visible?.check ?? null;
  const error = visible?.error ?? null;
  const busy = visible?.busy ?? false;

  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      request.current += 1;
    };
  }, []);
  // Invalidate before old promises can publish into the newly committed context.
  useLayoutEffect(() => {
    if (context.current.contractID !== contractID || context.current.caseID !== caseID ||
        context.current.caseVisible !== caseVisible || context.current.casesData !== cases.data || context.current.actorScope !== actorScope || context.current.listRevision !== listRevision) {
      context.current = { contractID, caseID, caseVisible, casesData: cases.data, actorScope, listRevision, generation: context.current.generation + 1 };
      request.current += 1;
      setVerification(null);
    }
  }, [contractID, caseID, caseVisible, cases.data, actorScope, listRevision]);
  useEffect(() => {
    if (caseID && (cases.isError || (cases.isSuccess && !cases.data.some((item) => item.id === caseID)))) {
      setCaseID(null);
    }
  }, [caseID, cases.data, cases.isError, cases.isSuccess]);

  const hasReferences =
    (draft.contract.related_parties?.length ?? 0) > 0 ||
    (draft.materials?.length ?? 0) > 0 ||
    draft.contract.procurement_result_package !== undefined;

  const sourceCase = check === null ? undefined : cases.data?.find((item) => item.id === check.source_case_id);
  const procurement = check?.procurement_result;
  const procurementMaterial = procurement === undefined ? undefined : check?.materials.find((material) =>
    material.material_id === procurement.material_id && material.material_version_id === procurement.material_version_id &&
    material.material_version === procurement.material_version && material.sha256 === procurement.sha256 &&
    material.material_role === procurement.material_role,
  );

  async function run() {
    if (contractID === null || !caseVisible || context.current.contractID !== contractID ||
        context.current.caseID !== caseID || context.current.caseVisible !== caseVisible ||
        context.current.casesData !== cases.data || context.current.actorScope !== actorScope ||
        context.current.listRevision !== listVersion()) return;
    const generation = context.current.generation;
    const sequence = ++request.current;
    setVerification({ generation, busy: true, check: null, error: null });
    try {
      const result = await checkContractReferences(contractID, caseID ?? undefined);
      if (mounted.current && request.current === sequence && context.current.generation === generation && context.current.listRevision === listVersion()) {
        setVerification({ generation, busy: false, check: result, error: null });
      }
    } catch (caught) {
      if (mounted.current && request.current === sequence && context.current.generation === generation && context.current.listRevision === listVersion()) {
        setVerification({ generation, busy: false, check: null, error: errorMessage(caught) });
      }
    }
  }

  return (
    <div className="contracts-page-references">
      <p className="meta">
        核对会按来源案件重新解析主体绑定、材料版本和采购结果来源；草稿保存保持宽松，冻结时才固定来源快照。
        预览/下载复用既有原件端点，每次访问都会重新检查案件读权限和原件摘要。
      </p>
      {contractID === null ? (
        <p className="meta">保存草稿后可核对引用。</p>
      ) : (
        <>
          <div className="contracts-page-fields">
            <Select
              {...caseSelectPresentation}
              showSearch={{ optionFilterProp: "label" }}
              allowClear
              value={caseVisible ? caseID ?? undefined : undefined}
              placeholder="来源案件（草稿必选；已导入合同可留空）"
              loading={cases.isLoading}
              disabled={!listReady}
              options={(listReady ? cases.data : []).map((item) => ({ value: item.id, label: caseOptionLabel(item) }))}
              onChange={(value) => setCaseID(value ?? null)}
            />
            <Button loading={busy} disabled={!caseVisible} onClick={() => void run()}>
              核对引用
            </Button>
          </div>
          {!hasReferences ? (
            <p className="meta">当前快照没有主体、材料或采购结果引用；核对仍会验证来源案件读权限。</p>
          ) : null}
          {error !== null ? <Alert type="error" showIcon message={error} /> : null}
          {check !== null && caseVisible ? (
            <div className="contracts-page-reference-result">
              <p className="meta">
                <strong>来源案件：{sourceCase ? (sourceCase.title.trim() ? sourceCase.title : "名称未记录") : "名称未在当前授权列表中"}</strong>
                {" · "}案件业务编号：{sourceCase ? (sourceCase.case_number?.trim() ? sourceCase.case_number : "未登记") : "未在当前授权列表中"}
                {" · "}{check.source === "revision" ? "冻结版本" : "当前草稿"}
                {" · "}核对时间：{preciseTime(check.checked_at)}
              </p>
              <TechnicalDetail label="来源与合同版本技术详情" value={JSON.stringify({
                contract_id: check.contract_id, source_case_id: check.source_case_id, source: check.source,
                contract_revision_id: check.contract_revision_id, checked_at: check.checked_at,
              }, null, 2)} />
              <InvalidTimeDetail label="核对时间" value={check.checked_at} />
              {check.subjects.length > 0 ? (
                <>
                  <strong>主体引用</strong>
                  <ul className="contracts-page-materials">
                    {check.subjects.map((subject) => (
                      <li key={`${subject.binding_id}-${subject.binding_version}`}>
                        <strong>{subject.name_snapshot.trim() ? subject.name_snapshot : "主体名称未记录"}</strong>
                        <Tag>合同角色：{contractPartyRoleLabel(subject.role)}</Tag>
                        <span className="meta">
                          案件角色：{caseRoleLabel(subject.case_role)} · {bindingStateLabel(subject.binding_state)}
                          {" · "}登记核验状态：{verificationStateLabel(subject.verification_state)} · 核验范围未提供
                          {subject.frozen ? " · 冻结引用" : " · 当前引用"}
                        </span>
                        <TechnicalDetail label="主体引用技术详情" value={JSON.stringify(subject, null, 2)} />
                      </li>
                    ))}
                  </ul>
                </>
              ) : null}
              {check.materials.length > 0 ? (
                <>
                  <strong>材料引用</strong>
                  <ul className="contracts-page-materials">
                    {check.materials.map((material) => (
                      <li key={`${material.material_id}-${material.material_version_id}`}>
                        <strong>{material.filename.trim() ? material.filename : "材料文件名未记录"}</strong>
                        <Tag>{material.material_role === "" ? "材料角色未记录" : roleLabel(material.material_role)}</Tag>
                        <span className="meta">用途：{contractMaterialPurposeLabel(material.purpose)} · 材料第 {material.material_version} 版</span>
                        <a href={material.original_path} target="_blank" rel="noreferrer">
                          预览/下载
                        </a>
                        {material.frozen ? <span className="meta">冻结引用</span> : <span className="meta">当前引用</span>}
                        <TechnicalDetail label="材料引用技术详情" value={JSON.stringify(material, null, 2)} />
                      </li>
                    ))}
                  </ul>
                </>
              ) : null}
              {procurement !== undefined ? (
                <>
                  <strong>采购结果来源</strong>
                  <p className="meta">
                    来源材料：{procurementMaterial?.filename.trim() ? procurementMaterial.filename : "文件名暂不可用"}
                    {" · "}{roleLabel(procurement.material_role)} · 材料第 {procurement.material_version} 版
                  </p>
                  <TechnicalDetail label="采购来源技术详情" value={JSON.stringify(procurement, null, 2)} />
                </>
              ) : null}
              {check.warnings.length > 0 ? (
                <ul className="contracts-page-materials">
                  {check.warnings.map((warning, index) => (
                    <li key={`${warning}-${index}`}>
                      <span>提示：{warningLabel(warning)}</span>
                      <TechnicalDetail label="提示代码技术详情" value={warning} />
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          ) : null}
        </>
      )}
    </div>
  );
}
