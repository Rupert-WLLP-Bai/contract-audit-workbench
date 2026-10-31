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
  getAssociationCandidates,
  getServiceObject,
  listServiceObjectAssociations,
  listServiceObjectVersions,
  listServiceObjects,
  type AssociationPreview,
  type AssociationSource,
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
  const primaryMaterials = (caseDetail.materials ?? []).filter((material) =>
    material.role === "draft_contract" && material.purpose === "primary_contract",
  );
  const versions = caseDetail.material_versions ?? [];
  const result: FrozenContractCandidate[] = [];
  for (const document of workspace.documents) {
    if (document.role !== "draft_contract" || document.parsed_document_id === null) continue;
    for (const material of primaryMaterials) {
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
        label: `${document.filename} · 主合同材料第 ${version.version} 版`,
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

  useEffect(() => {
    if (lastCaseId.current === caseId) return;
    lastCaseId.current = caseId;
    setDisplayName("");
    setBusinessCode("");
    setSelectedCandidateKey("");
    setSelectedObjectRef("");
    setConfirmed(false);
    setFormError(null);
    if (params.has("service_object_id")) {
      const next = new URLSearchParams(params);
      next.delete("service_object_id");
      next.delete("service_document");
      setParams(next, { replace: true });
    }
  }, [caseId, params, setParams]);

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
    enabled: objectId !== "" && detail.data?.can_manage === true && detail.data.association === null && previewSource !== null,
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
  }

  function selectCandidate(candidateValue: FrozenContractCandidate) {
    setSelectedCandidateKey(candidateKey(candidateValue));
    setSelectedObjectRef("");
    setConfirmed(false);
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
    confirmAssociation.mutate({
      confirmed: true,
      expected_object_version: preview.data.expected_object_version,
      expected_association_version: preview.data.expected_association_version,
      source: previewSource,
      object_ref: option.object_ref,
      facts_sha256: option.facts_sha256,
      candidate_sha256: preview.data.candidate_sha256,
    }, { onError: (error) => setFormError(errorMessage(error)) });
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

          {detail.data.can_manage && detail.data.association === null ? (
            <div>
              <h4>确认首个冻结事实关联</h4>
              <label className="field">
                审查执行记录
                <select aria-label="服务对象审查执行记录" value={selectedAttemptId ?? ""} onChange={(event) => changeAttempt(event.target.value)} disabled={attempts.length === 0}>
                  {attempts.length === 0 ? <option value="">暂无执行记录</option> : null}
                  {attempts.map((attempt) => <option key={attempt.id} value={attempt.id}>第 {attempt.ordinal} 次 · {attempt.state} · {timeLabel(attempt.accepted_at ?? attempt.started_at)}</option>)}
                </select>
              </label>
              {review.isError ? <div role="alert"><p>冻结审查读取失败：{errorMessage(review.error)}</p><button type="button" onClick={() => void review.refetch()}>重试</button></div> : null}
              {candidates.length === 0 && review.isSuccess ? <p>所选执行记录没有可用的主合同解析版本。</p> : null}
              {candidates.length > 0 ? (
                <label className="field">
                  冻结主合同版本
                  <select aria-label="服务对象冻结主合同" value={candidate ? candidateKey(candidate) : ""} onChange={(event) => { const next = candidates.find((item) => candidateKey(item) === event.target.value); if (next) selectCandidate(next); }}>
                    {candidates.map((item) => <option key={candidateKey(item)} value={candidateKey(item)}>{item.label}</option>)}
                  </select>
                </label>
              ) : null}
              {preview.isFetching ? <p role="status">正在从该次冻结审查读取候选事实…</p> : null}
              {preview.isError ? <div role="alert"><p>候选读取失败：{errorMessage(preview.error)}</p><button type="button" onClick={() => void preview.refetch()}>重试</button></div> : null}
              {preview.data ? <CandidateFacts preview={preview.data} selectedRef={selectedObjectRef} onSelect={setSelectedObjectRef} /> : null}
              {preview.data && preview.data.options.length === 0 ? <p>该冻结材料没有可关联的已核对对象事实。</p> : null}
              {preview.data && preview.data.options.some((option) => option.object_ref === selectedObjectRef) ? (
                <label><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} />我已核对所选审查、材料版本和对象事实，可确认该首个关联。</label>
              ) : null}
              <p className="meta">首次确认会固定此处展示的冻结事实。后续变更需单独审批；本操作不自动认定履约结果。</p>
              <button type="button" disabled={!confirmed || !selectedObjectRef || confirmAssociation.isPending || !preview.data} onClick={submitAssociation}>{confirmAssociation.isPending ? "正在确认…" : "确认首个冻结事实关联"}</button>
              {confirmAssociation.isError ? <p role="alert">关联确认失败：{errorMessage(confirmAssociation.error)}</p> : null}
              {confirmAssociation.isSuccess ? <p role="status">首个事实关联已确认，第 {confirmAssociation.data.association_version} 版。</p> : null}
            </div>
          ) : null}

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
