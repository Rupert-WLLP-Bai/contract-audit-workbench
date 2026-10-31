import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { errorMessage } from "../../api/http";
import { getReview } from "../../api/endpoints";
import type { CaseDetail } from "../../api/types";
import { formatDateTime } from "../../domain/review";
import { useStableIdempotencyKey } from "./idempotency";
import {
  confirmServiceObjectAssociation,
  createServiceObject,
  decideMappingAssociation,
  getAssociationCandidates,
  getServiceObject,
  getMappingAssociationChange,
  listServiceObjectAssociations,
  listServiceObjectVersions,
  listServiceObjects,
  previewMappingAssociation,
  proposeMappingAssociation,
  type AssociationPreview,
  type AssociationSource,
  type MappingChangeDraft,
  type MappingChangePreview,
  type MappingDecisionInput,
  type ServiceObjectAssociation,
  type ServiceObjectAssociationConfirmation,
  type ServiceObjectCreateInput,
  type ServiceObjectSummary,
} from "./service-objects-api";

interface FrozenContractCandidate extends AssociationSource {
  filename: string;
  label: string;
}

function candidateKey(candidate: AssociationSource): string {
  return JSON.stringify([candidate.material_id, candidate.material_version, candidate.document_id, candidate.parsed_document_id]);
}

function frozenContractCandidates(caseDetail: CaseDetail | undefined, workspace: Awaited<ReturnType<typeof getReview>> | undefined): FrozenContractCandidate[] {
  if (!caseDetail || !workspace) return [];
  const contractMaterials = (caseDetail.materials ?? []).filter((material) =>
    (material.role === "draft_contract" && material.purpose === "primary_contract") ||
    (material.role === "attachment" && material.purpose === "supplement"),
  );
  const versions = caseDetail.material_versions ?? [];
  const result: FrozenContractCandidate[] = [];
  for (const document of workspace.documents) {
    if (document.parsed_document_id === null) continue;
    for (const material of contractMaterials) {
      if (document.role !== material.role) continue;
      const version = versions.find((item) => item.material_id === material.material_id && item.document_id === document.id);
      if (!version) continue;
      result.push({
        run_id: workspace.run.id,
        attempt_id: workspace.review_attempts?.attempt?.id ?? "",
        material_id: version.material_id,
        material_version: version.version,
        document_id: version.document_id,
        parsed_document_id: document.parsed_document_id,
        filename: document.filename,
        label: `${document.filename} · ${material.purpose === "supplement" ? "补充协议" : "主合同"}材料第 ${version.version} 版`,
      });
    }
  }
  return result;
}

function useSelectedObjectId(params: URLSearchParams, setParams: (next: URLSearchParams, options?: { replace?: boolean }) => void) {
  const current = params.get("service_object_id") ?? "";
  return [current, (nextId: string) => {
    const next = new URLSearchParams(params);
    if (nextId) next.set("service_object_id", nextId);
    else next.delete("service_object_id");
    setParams(next);
  }] as const;
}

function summaryLabel(item: ServiceObjectSummary): string {
  const association = item.current_association;
  const status = item.state === "confirmed" ? "已确认" : item.state === "inactive" ? "已停用" : "待确认";
  return `${item.display_name} · ${status} · 第 ${item.current_version} 版${association ? ` · 事实关联第 ${association.association_version} 版` : " · 尚无事实关联"}`;
}

function timeLabel(value: string | null): string {
  return value ? formatDateTime(value) : "时间未记录";
}

function associationBasis(association: ServiceObjectAssociation): MappingChangeDraft["basis_refs"] {
  for (const value of association.basis_refs) {
    if (typeof value !== "object" || value === null || Array.isArray(value)) continue;
    const ref = value as Record<string, unknown>;
    if (ref.document_id !== association.document_id || ref.parsed_document_id !== association.parsed_document_id ||
      typeof ref.start !== "number" || !Number.isInteger(ref.start) || ref.start < 0 ||
      typeof ref.end !== "number" || !Number.isInteger(ref.end) || ref.end <= ref.start ||
      typeof ref.quote_sha256 !== "string" || !/^[a-f0-9]{64}$/.test(ref.quote_sha256)) continue;
    return [{ kind: "material_anchor", material_id: association.material_id, material_version: association.material_version,
      document_id: association.document_id, parsed_document_id: association.parsed_document_id,
      start: ref.start, end: ref.end, quote_digest: ref.quote_sha256 }];
  }
  throw new Error("原合同关联缺少可核对的原文依据，无法提交更正。");
}

function CandidateFacts({ preview, selectedRef, onSelect }: { preview: AssociationPreview; selectedRef: string; onSelect: (value: string) => void }) {
  const selected = preview.options.find((option) => option.object_ref === selectedRef) ?? null;
  return (
    <div>
      <label className="field">
        选择冻结审查中的服务对象
        <select aria-label="冻结服务对象候选" value={selectedRef} onChange={(event) => onSelect(event.target.value)}>
          <option value="">选择候选对象</option>
          {preview.options.map((option) => <option key={option.object_ref} value={option.object_ref}>{option.object_ref} · {option.facts.length} 项事实</option>)}
        </select>
      </label>
      {selected ? (
        <ul className="history">
          {selected.facts.map((item) => (
            <li key={`${item.fact.id}:${item.canonical_fact_sha256}`}>
              <strong>{item.fact.dimension}</strong>
              <p>{item.fact.value} {item.fact.unit}</p>
              {item.fact.basis ? <p>{item.fact.basis}</p> : null}
              <p className="meta">来源 {item.fact.source_role} · 原文引用 {item.fact.citations.length} 处</p>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

export function ServiceObjectPanel({ caseId, initialRunId, initialAttemptId, caseDetail, onObjectsChange }: {
  caseId: string;
  initialRunId: string;
  initialAttemptId: string | null;
  caseDetail: CaseDetail | undefined;
  onObjectsChange?: (objects: ServiceObjectSummary[]) => void;
}) {
  const [params, setParams] = useSearchParams();
  const [objectId, selectObject] = useSelectedObjectId(params, setParams);
  const queryClient = useQueryClient();
  const keyFor = useStableIdempotencyKey();
  const lastCaseId = useRef(caseId);
  const [displayName, setDisplayName] = useState("");
  const [businessCode, setBusinessCode] = useState("");
  const [objectType, setObjectType] = useState<"dedicated_line" | "service">("dedicated_line");
  const [selectedCandidateKey, setSelectedCandidateKey] = useState("");
  const [selectedObjectRef, setSelectedObjectRef] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [mappingDraft, setMappingDraft] = useState<{ draft: MappingChangeDraft; preview: MappingChangePreview; mappingId: string; objectId: string; caseId: string } | null>(null);
  const [decisionReason, setDecisionReason] = useState("");

  useEffect(() => {
    if (lastCaseId.current === caseId) return;
    lastCaseId.current = caseId;
    setDisplayName("");
    setBusinessCode("");
    setSelectedCandidateKey("");
    setSelectedObjectRef("");
    setConfirmed(false);
    setFormError(null);
    setMappingDraft(null);
    setDecisionReason("");
    if (params.has("service_object_id")) {
      const next = new URLSearchParams(params);
      next.delete("service_object_id");
      next.delete("service_document");
      next.delete("mapping_change");
      setParams(next, { replace: true });
    }
  }, [caseId, params, setParams]);

  useEffect(() => {
    setMappingDraft(null);
    setConfirmed(false);
    setSelectedObjectRef("");
  }, [caseId, objectId]);

  const list = useInfiniteQuery({
    queryKey: ["service-objects", caseId],
    queryFn: ({ pageParam, signal }) => listServiceObjects(caseId, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: caseId !== "",
    retry: false,
  });
  const summaries = useMemo(() => list.data?.pages.flatMap((page) => page.items) ?? [], [list.data]);
  const currentCanManage = list.data?.pages[0]?.can_manage === true;
  useEffect(() => {
    if (objectId === "" && summaries[0]) selectObject(summaries[0].service_object_id);
  }, [objectId, selectObject, summaries]);
  useEffect(() => {
    onObjectsChange?.(summaries);
  }, [onObjectsChange, summaries]);
  const detail = useQuery({
    queryKey: ["service-objects", caseId, objectId, "detail"],
    queryFn: ({ signal }) => getServiceObject(caseId, objectId, signal),
    enabled: caseId !== "" && objectId !== "",
    retry: false,
  });
  const objectVersions = useInfiniteQuery({
    queryKey: ["service-objects", caseId, objectId, "versions"],
    queryFn: ({ pageParam, signal }) => listServiceObjectVersions(caseId, objectId, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: objectId !== "",
    retry: false,
  });
  const associationVersions = useInfiniteQuery({
    queryKey: ["service-objects", caseId, objectId, "associations"],
    queryFn: ({ pageParam, signal }) => listServiceObjectAssociations(caseId, objectId, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: objectId !== "",
    retry: false,
  });

  const runId = params.get("run_id") ?? initialRunId;
  const requestedAttemptId = params.get("attempt_id") ?? initialAttemptId;
  const review = useQuery({
    queryKey: ["service-objects", caseId, "frozen-review", runId, requestedAttemptId],
    queryFn: ({ signal }) => getReview(caseId, runId, requestedAttemptId, signal),
    enabled: runId !== "",
    retry: false,
  });
  const candidates = useMemo(() => frozenContractCandidates(caseDetail, review.data), [caseDetail, review.data]);
  const urlCandidateKey = params.get("service_document") ?? "";
  const candidate = candidates.find((item) => candidateKey(item) === (urlCandidateKey || selectedCandidateKey)) ?? candidates[0];
  const candidateReady = candidate !== undefined && candidate.attempt_id !== "";
  const previewSource = candidateReady && candidate ? {
    run_id: candidate.run_id,
    attempt_id: candidate.attempt_id,
    material_id: candidate.material_id,
    material_version: candidate.material_version,
    document_id: candidate.document_id,
    parsed_document_id: candidate.parsed_document_id,
  } : null;
  const preview = useQuery({
    queryKey: ["service-objects", caseId, objectId, "candidates", previewSource],
    queryFn: ({ signal }) => getAssociationCandidates(caseId, objectId, previewSource!, signal),
    enabled: objectId !== "" && detail.data?.can_manage === true && previewSource !== null,
    retry: false,
  });

  const create = useMutation({
    mutationFn: (input: ServiceObjectCreateInput) => createServiceObject(caseId, input, keyFor("service-object-create", [caseId, input])),
    onSuccess: async (result) => {
      selectObject(result.service_object_id);
      setDisplayName("");
      setBusinessCode("");
      await queryClient.invalidateQueries({ queryKey: ["service-objects", caseId] });
    },
  });
  const confirmAssociation = useMutation({
    mutationFn: async (input: ServiceObjectAssociationConfirmation) => {
      return confirmServiceObjectAssociation(caseId, objectId, input, keyFor("service-object-association", [caseId, objectId, input]));
    },
    onSuccess: async () => {
      setConfirmed(false);
      setSelectedObjectRef("");
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["service-objects", caseId] }),
        queryClient.invalidateQueries({ queryKey: ["service-objects", caseId, objectId] }),
      ]);
    },
  });

  const mappingRequestId = params.get("mapping_change") ?? "";
  const mappingChange = useQuery({
    queryKey: ["service-objects", caseId, "mapping-change", mappingRequestId],
    queryFn: ({ signal }) => getMappingAssociationChange(caseId, mappingRequestId, signal),
    enabled: caseId !== "" && mappingRequestId !== "",
    retry: false,
  });
  const mappingPreview = useMutation({
    mutationFn: async ({ mappingId, draft, objectId, caseId: draftCaseId }: { mappingId: string; draft: MappingChangeDraft; objectId: string; caseId: string }) => ({ mappingId, draft, objectId, caseId: draftCaseId, preview: await previewMappingAssociation(draftCaseId, mappingId, draft) }),
    onSuccess: setMappingDraft,
  });
  const mappingPropose = useMutation({
    mutationFn: ({ mappingId, draft, preview, caseId: draftCaseId }: { mappingId: string; draft: MappingChangeDraft; preview: MappingChangePreview; caseId: string }) =>
      proposeMappingAssociation(draftCaseId, mappingId, draft, preview, keyFor("mapping-reassociation-proposal", [draftCaseId, mappingId, draft, preview])),
    onSuccess: (result) => {
      setMappingDraft(null);
      const next = new URLSearchParams(params);
      next.set("mapping_change", result.request_id);
      next.set("service_object_id", result.service_object_id);
      setParams(next);
    },
  });
  const mappingDecide = useMutation({
    mutationFn: (input: MappingDecisionInput) => decideMappingAssociation(caseId, mappingRequestId, input,
      keyFor("mapping-reassociation-decision", [caseId, mappingRequestId, input])),
    onSuccess: async () => {
      setDecisionReason("");
      await Promise.all([queryClient.invalidateQueries({ queryKey: ["service-objects", caseId] }), mappingChange.refetch()]);
    },
  });

  const attempts = review.data?.review_attempts?.attempts ?? [];
  const selectedAttemptId = review.data?.review_attempts?.attempt?.id ?? requestedAttemptId;
  const versions = objectVersions.data?.pages.flatMap((page) => page.items) ?? [];
  const associations = associationVersions.data?.pages.flatMap((page) => page.items) ?? [];

  function changeAttempt(nextAttemptId: string) {
    const next = new URLSearchParams(params);
    if (nextAttemptId) next.set("attempt_id", nextAttemptId);
    else next.delete("attempt_id");
    next.delete("service_document");
    setParams(next);
    setSelectedCandidateKey("");
    setSelectedObjectRef("");
    setConfirmed(false);
    setMappingDraft(null);
  }

  function selectCandidate(candidateValue: FrozenContractCandidate) {
    setSelectedCandidateKey(candidateKey(candidateValue));
    setSelectedObjectRef("");
    setConfirmed(false);
    setMappingDraft(null);
    const next = new URLSearchParams(params);
    next.set("service_document", candidateKey(candidateValue));
    setParams(next);
  }

  async function submitCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!currentCanManage || !displayName.trim()) return;
    setFormError(null);
    const input: ServiceObjectCreateInput = {
      object_type: objectType,
      display_name: displayName.trim(),
      ...(businessCode.trim() ? { business_code: businessCode.trim(), aliases: [{ kind: "circuit", value: businessCode.trim() }] } : { aliases: [] }),
    };
    create.mutate(input, { onError: (error) => setFormError(errorMessage(error)) });
  }

  function submitAssociation() {
    if (!confirmed || !preview.data || !previewSource || !detail.data?.can_manage) return;
    const option = preview.data.options.find((item) => item.object_ref === selectedObjectRef);
    if (!option) return;
    const input: ServiceObjectAssociationConfirmation = {
      confirmed: true,
      expected_object_version: preview.data.expected_object_version,
      expected_association_version: preview.data.expected_association_version,
      source: previewSource,
      object_ref: option.object_ref,
      facts_sha256: option.facts_sha256,
      candidate_sha256: preview.data.candidate_sha256,
    };
    const mapping = detail.data.mapping;
    if (mapping) {
      if (!detail.data.association || mapping.object_version !== input.expected_object_version || mapping.association_version !== input.expected_association_version) {
        setFormError("对象映射与当前关联版本不一致，请刷新后重试。");
        return;
      }
      try {
        const draft: MappingChangeDraft = {
          cas: { expected_mapping_version: mapping.mapping_version, expected_binding_version: mapping.binding_version,
            expected_object_version: mapping.object_version, expected_association_version: mapping.association_version },
          patch: { kind: "replace_mapping", external_object: mapping.external_object, effective_from: mapping.effective_from,
            effective_until: mapping.effective_until, contract_association: input },
          basis_refs: associationBasis(detail.data.association),
        };
        setFormError(null);
        setMappingDraft(null);
        mappingPreview.mutate({ mappingId: mapping.mapping_id, draft, objectId, caseId }, { onError: (error) => setFormError(errorMessage(error)) });
      } catch (error) {
        setFormError(errorMessage(error));
      }
      return;
    }
    confirmAssociation.mutate(input, { onError: (error) => setFormError(errorMessage(error)) });
  }

  function submitDecision(decision: "approve" | "reject") {
    const request = mappingChange.data;
    if (!request || request.service_object_id !== objectId || request.state !== "pending_decision" || !decisionReason.trim()) return;
    mappingDecide.mutate({ decision, reason: decisionReason.trim(), expected_mapping_version: request.expected_mapping_version,
      expected_binding_version: request.expected_binding_version, expected_object_version: request.expected_object_version,
      expected_association_version: request.expected_association_version, expected_after_sha256: request.after_sha256,
      expected_impact_sha256: request.impact_sha256 });
  }

  return (
    <section className="evidence-card" aria-label="案件服务对象">
      <header><h2>服务对象与冻结事实</h2></header>
      <p className="meta">服务对象有独立身份版本；事实关联固定到所选审查尝试、材料版本与解析版本。再次变更映射需经过单独审批。</p>
      {list.isLoading ? <p role="status">正在读取本案服务对象…</p> : null}
      {list.isError ? <div role="alert"><p>服务对象列表读取失败：{errorMessage(list.error)}</p><button type="button" onClick={() => void list.refetch()}>重试</button></div> : null}
      {list.isSuccess && summaries.length === 0 ? <p>本案尚未登记服务对象。</p> : null}
      {summaries.length > 0 ? (
        <ul className="document-list" aria-label="服务对象列表">
          {summaries.map((item) => (
            <li key={item.service_object_id}>
              <div className="document-main">
                <strong>{summaryLabel(item)}</strong>
                <p className="meta">{item.business_code || "业务编号未登记"} · {item.object_type === "dedicated_line" ? "政企专线" : "服务"}</p>
                {item.current_association ? <p className="meta">绑定审查 {item.current_association.source_run_id} / {item.current_association.source_attempt_id} · 材料第 {item.current_association.material_version} 版 · 对象编号 {item.current_association.object_ref}</p> : null}
              </div>
              <button type="button" aria-pressed={objectId === item.service_object_id} onClick={() => selectObject(item.service_object_id)}>查看详情</button>
            </li>
          ))}
        </ul>
      ) : null}
      {list.hasNextPage ? <button type="button" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>{list.isFetchingNextPage ? "正在读取…" : "读取更多服务对象"}</button> : null}

      {currentCanManage ? (
        <form onSubmit={(event) => void submitCreate(event)}>
          <h3>登记服务对象</h3>
          <label className="field">对象类型<select aria-label="服务对象类型" value={objectType} onChange={(event) => setObjectType(event.target.value as "dedicated_line" | "service")}><option value="dedicated_line">政企专线</option><option value="service">其他服务</option></select></label>
          <label className="field">显示名称<input aria-label="服务对象名称" value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={200} /></label>
          <label className="field">业务编号（可选）<input aria-label="服务对象业务编号" value={businessCode} onChange={(event) => setBusinessCode(event.target.value)} maxLength={200} /></label>
          <button type="submit" disabled={create.isPending || !displayName.trim()}>{create.isPending ? "正在登记…" : "登记待确认服务对象"}</button>
        </form>
      ) : list.isSuccess && summaries.length === 0 ? (
        <p className="callout">当前服务端没有返回空列表写入权限，暂不能新增服务对象。</p>
      ) : null}
      {formError ? <p role="alert">操作失败：{formError}</p> : null}
      {create.isSuccess ? <p role="status">服务对象已登记为待确认，第 {create.data.version} 版。</p> : null}

      {objectId && detail.isLoading ? <p role="status">正在读取服务对象版本…</p> : null}
      {objectId && detail.isError ? <div role="alert"><p>服务对象详情读取失败：{errorMessage(detail.error)}</p><button type="button" onClick={() => void detail.refetch()}>重试</button></div> : null}
      {detail.data ? (
        <section aria-label="所选服务对象详情">
          <h3>{detail.data.identity.display_name} · 身份第 {detail.data.current_version} 版</h3>
          <p>{detail.data.identity.business_code || "业务编号未登记"} · {detail.data.identity.state === "confirmed" ? "已确认" : detail.data.identity.state === "inactive" ? "已停用" : "待确认"}</p>
          {detail.data.identity.basis_summary ? <p>{detail.data.identity.basis_summary}</p> : null}
          {detail.data.can_manage ? null : <p className="callout">服务端当前将该服务对象设为只读。</p>}
          {detail.data.needs_reassociation ? <p>当前事实关联需要复核。</p> : null}
          {detail.data.association ? (
            <div>
              <h4>当前冻结事实关联第 {detail.data.association.association_version} 版</h4>
              <p>基于审查 {detail.data.association.source_run_id} / {detail.data.association.source_attempt_id}，材料版本 {detail.data.association.material_version}，对象编号 {detail.data.association.object_ref}。</p>
              <ul className="history">{detail.data.association.facts.map((item) => <li key={`${item.fact.id}:${item.canonical_fact_sha256}`}><strong>{item.fact.dimension}</strong><p>{item.fact.value} {item.fact.unit}</p>{item.fact.basis ? <p>{item.fact.basis}</p> : null}</li>)}</ul>
            </div>
          ) : null}

          {detail.data.can_manage ? (
            <div>
              <h4>{detail.data.association ? "重新关联冻结合同事实" : "确认首个冻结事实关联"}</h4>
              <label className="field">
                审查执行记录
                <select aria-label="服务对象审查执行记录" value={selectedAttemptId ?? ""} onChange={(event) => changeAttempt(event.target.value)} disabled={attempts.length === 0}>
                  {attempts.length === 0 ? <option value="">暂无执行记录</option> : null}
                  {attempts.map((attempt) => <option key={attempt.id} value={attempt.id}>第 {attempt.ordinal} 次 · {attempt.state} · {timeLabel(attempt.accepted_at ?? attempt.started_at)}</option>)}
                </select>
              </label>
              {review.isError ? <div role="alert"><p>冻结审查读取失败：{errorMessage(review.error)}</p><button type="button" onClick={() => void review.refetch()}>重试</button></div> : null}
              {candidates.length === 0 && review.isSuccess ? <p>所选执行记录没有可用的主合同或补充协议解析版本。</p> : null}
              {candidates.length > 0 ? (
                <label className="field">
                  冻结合同材料版本
                  <select aria-label="服务对象冻结合同材料" value={candidate ? candidateKey(candidate) : ""} onChange={(event) => { const next = candidates.find((item) => candidateKey(item) === event.target.value); if (next) selectCandidate(next); }}>
                    {candidates.map((item) => <option key={candidateKey(item)} value={candidateKey(item)}>{item.label}</option>)}
                  </select>
                </label>
              ) : null}
              {preview.isFetching ? <p role="status">正在从该次冻结审查读取候选事实…</p> : null}
              {preview.isError ? <div role="alert"><p>候选读取失败：{errorMessage(preview.error)}</p><button type="button" onClick={() => void preview.refetch()}>重试</button></div> : null}
              {preview.data ? <CandidateFacts preview={preview.data} selectedRef={selectedObjectRef} onSelect={(value) => { setSelectedObjectRef(value); setConfirmed(false); setMappingDraft(null); }} /> : null}
              {preview.data && preview.data.options.length === 0 ? <p>该冻结材料没有可关联的已核对对象事实。</p> : null}
              {preview.data && preview.data.options.some((option) => option.object_ref === selectedObjectRef) ? (
                <label><input type="checkbox" checked={confirmed} onChange={(event) => { setConfirmed(event.target.checked); setMappingDraft(null); }} />我已核对所选审查、材料版本和对象事实。</label>
              ) : null}
              <p className="meta">{detail.data.mapping ? "已映射对象的合同重关联需由另一人审批；批准后关联与映射同时生效。" : "确认会固定此处展示的冻结事实，不自动认定履约结果。"}</p>
              <button type="button" disabled={!confirmed || !selectedObjectRef || confirmAssociation.isPending || mappingPreview.isPending || !preview.data} onClick={submitAssociation}>{confirmAssociation.isPending || mappingPreview.isPending ? "正在处理…" : detail.data.mapping ? "预览关联更正" : detail.data.association ? "确认新的冻结事实关联" : "确认首个冻结事实关联"}</button>
              {confirmAssociation.isError ? <p role="alert">关联确认失败：{errorMessage(confirmAssociation.error)}</p> : null}
              {confirmAssociation.isSuccess ? <p role="status">事实关联已确认，第 {confirmAssociation.data.association_version} 版。</p> : null}
              {mappingDraft && mappingDraft.caseId === caseId && mappingDraft.objectId === objectId && confirmed && mappingDraft.draft.patch.contract_association.object_ref === selectedObjectRef && JSON.stringify(mappingDraft.draft.patch.contract_association.source) === JSON.stringify(previewSource) ? <div className="callout">
                <h4>核对关联更正预览</h4>
                <p>目标对象 {mappingDraft.preview.association_after.object_ref} · 材料第 {mappingDraft.preview.association_after.material_version} 版 · 来源审查 {mappingDraft.preview.association_after.source_run_id} / {mappingDraft.preview.association_after.source_attempt_id}</p>
                <p>将产生关联第 {mappingDraft.preview.association_after.association_version} 版；既有外部对象不变。</p>
                <button type="button" disabled={mappingPropose.isPending} onClick={() => mappingPropose.mutate(mappingDraft)}>{mappingPropose.isPending ? "正在提交…" : "提交异人审批"}</button>
                {mappingPropose.isError ? <p role="alert">提交失败：{errorMessage(mappingPropose.error)}</p> : null}
              </div> : null}
            </div>
          ) : null}

          {mappingRequestId ? <section aria-label="合同关联更正审批">
            <h4>合同关联更正申请</h4>
            {mappingChange.isLoading ? <p role="status">正在读取审批记录…</p> : null}
            {mappingChange.isError ? <div role="alert">审批记录读取失败：{errorMessage(mappingChange.error)} <button type="button" onClick={() => void mappingChange.refetch()}>重试</button></div> : null}
            {mappingChange.data && mappingChange.data.service_object_id !== objectId ? <p role="alert">此审批属于服务对象 {mappingChange.data.service_object_id}，请先选择该对象。</p> : null}
            {mappingChange.data && mappingChange.data.service_object_id === objectId ? <>
              <p>申请 {mappingChange.data.request_id} · 状态 {mappingChange.data.state} · 提交于 {formatDateTime(mappingChange.data.created_at)}</p>
              <p>目标对象 {mappingChange.data.after.object_ref} · 材料 {mappingChange.data.after.material_id} 第 {mappingChange.data.after.material_version} 版 · 文档 {mappingChange.data.after.document_id} / 解析 {mappingChange.data.after.parsed_document_id}</p>
              <p>来源审查 {mappingChange.data.after.source_run_id} / {mappingChange.data.after.source_attempt_id} · 新关联第 {mappingChange.data.after.association_version} 版</p>
              <ul className="history">{mappingChange.data.after.facts.map((fact) => <li key={fact.fact.id}><strong>{fact.fact.dimension}</strong><p>{fact.fact.value} {fact.fact.unit}</p>{fact.fact.basis ? <p>{fact.fact.basis}</p> : null}</li>)}</ul>
              <p className="meta">可复制当前页面地址交由另一位有决定权限的人员核对。审批会重新校验冻结材料、摘要及当前版本。</p>
              {mappingChange.data.state === "pending_decision" ? <>
                <label className="field">决定理由<textarea value={decisionReason} maxLength={2000} onChange={(event) => setDecisionReason(event.target.value)} /></label>
                <button type="button" disabled={!decisionReason.trim() || mappingDecide.isPending} onClick={() => submitDecision("approve")}>批准关联更正</button>{" "}
                <button type="button" disabled={!decisionReason.trim() || mappingDecide.isPending} onClick={() => submitDecision("reject")}>拒绝关联更正</button>
              </> : null}
              {mappingDecide.isError ? <p role="alert">审批失败：{errorMessage(mappingDecide.error)}</p> : null}
              {mappingDecide.isSuccess ? <p role="status">审批决定已记录。</p> : null}
            </> : null}
          </section> : null}

          <details>
            <summary>身份版本历史</summary>
            {objectVersions.isError ? <div role="alert">历史读取失败：{errorMessage(objectVersions.error)} <button type="button" onClick={() => void objectVersions.refetch()}>重试</button></div> : null}
            {versions.length === 0 && objectVersions.isSuccess ? <p>没有身份版本历史。</p> : null}
            <ol>{versions.map((version) => <li key={version.version}>第 {version.version} 版 · {version.display_name} · {version.state} · {formatDateTime(version.created_at)}</li>)}</ol>
            {objectVersions.hasNextPage ? <button type="button" disabled={objectVersions.isFetchingNextPage} onClick={() => void objectVersions.fetchNextPage()}>读取更早身份版本</button> : null}
          </details>
          <details>
            <summary>冻结事实关联历史</summary>
            {associationVersions.isError ? <div role="alert">关联历史读取失败：{errorMessage(associationVersions.error)} <button type="button" onClick={() => void associationVersions.refetch()}>重试</button></div> : null}
            {associations.length === 0 && associationVersions.isSuccess ? <p>没有事实关联历史。</p> : null}
            <ol>{associations.map((association) => <li key={association.association_version}>关联第 {association.association_version} 版 · 对象 {association.object_ref} · 审查 {association.source_run_id}/{association.source_attempt_id} · 材料第 {association.material_version} 版 · {formatDateTime(association.confirmed_at)}</li>)}</ol>
            {associationVersions.hasNextPage ? <button type="button" disabled={associationVersions.isFetchingNextPage} onClick={() => void associationVersions.fetchNextPage()}>读取更早事实关联</button> : null}
          </details>
        </section>
      ) : null}
    </section>
  );
}
