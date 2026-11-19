// File metadata (manifest mirror):
// Purpose: Displays scoped clause suggestions and history, verifies the source for the workspace diff, and uses existing commands to create proposals and accept or reject them with append-only records.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-19
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import {
  Alert,
  Button,
  Card,
  Checkbox,
  Collapse,
  Input,
  Select,
  Space,
  Tag,
  Typography,
} from "antd";
import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  assertCommandContextCurrent,
  captureCommandContext,
  errorMessage,
  isConflict,
  isForbidden,
  isUnauthenticated,
} from "../api/http";
import {
  useCaseClauseProposals,
  useContractLedger,
  useCreateClauseProposal,
  useDecideClauseProposal,
  useDeriveClauseProposalText,
  useSession,
  useUploadDocument,
  queryKeys,
} from "../api/queries";
import {
  type ClauseProposal,
  type ClauseProposalDecisionInput,
  type ClauseProposalEvidence,
  type CreateClauseProposalInput,
} from "../api/clause-proposals";
import type {
  Actor,
  CaseDetail,
  EvidenceAnchor,
  Finding,
  Remediation,
  ReviewDocument,
} from "../api/types";
import { actorReadContextKey } from "../domain/actor-read-context";
import { resolveAnchor, sliceByCodePoints } from "../domain/text";
import {
  clearPendingMutationKey,
  fileSha256,
  idempotencyActorScope,
  pendingMutationKey,
} from "../api/idempotency";
import { TechnicalDetail } from "./technical-detail";

const { TextArea } = Input;

export interface ClauseProposalPanelContext {
  caseId: string;
  runId: string;
  attemptId: string;
  frozenParsedDocumentIds: string[];
  remediations: Remediation[];
  caseDetail?: CaseDetail;
  canWrite: boolean;
}

interface ClauseProposalContext extends ClauseProposalPanelContext {
  finding: Finding;
  documents: ReviewDocument[];
  immersive?: boolean;
}

type DecisionMode = "accept" | "accept_edited" | "reject";

interface UploadedClauseDraft {
  case_id: string;
  proposal_id: string;
  proposal_version: number;
  contract_id: string;
  accepted_text: string;
  action: "accept" | "accept_edited";
  material_id: string;
  document_id: string;
  material_version: number;
  sha256: string;
  upload_key: string;
}

function responseError(error: unknown, operation: string): string {
  if (isUnauthenticated(error)) return "登录状态已失效，请重新登录后继续。";
  if (isForbidden(error)) return "当前账号没有完成此操作的权限；候选和历史仍以服务端授权为准。";
  if (isConflict(error)) return "来源或版本已变化。请刷新候选与合同状态，核对后再重新确认。";
  return `${operation}失败：${errorMessage(error)}`;
}

function uploadStorageKey(actorScope: string, caseId: string, proposalId: string): string {
  return `caw:clause-proposal-upload:${encodeURIComponent(JSON.stringify([actorScope, caseId, proposalId]))}`;
}

function readUploadedDraft(key: string): UploadedClauseDraft | null {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(key) ?? "null");
    if (typeof value !== "object" || value === null) return null;
    const row = value as Record<string, unknown>;
    if (
      typeof row.case_id !== "string" ||
      typeof row.proposal_id !== "string" ||
      typeof row.proposal_version !== "number" ||
      typeof row.contract_id !== "string" ||
      typeof row.accepted_text !== "string" ||
      (row.action !== "accept" && row.action !== "accept_edited") ||
      typeof row.material_id !== "string" ||
      typeof row.document_id !== "string" ||
      typeof row.material_version !== "number" ||
      typeof row.sha256 !== "string" ||
      typeof row.upload_key !== "string"
    )
      return null;
    return row as unknown as UploadedClauseDraft;
  } catch {
    return null;
  }
}

function storeUploadedDraft(key: string, value: UploadedClauseDraft): boolean {
  try {
    sessionStorage.setItem(key, JSON.stringify(value));
    return true;
  } catch {
    return false;
  }
}

function removeUploadedDraft(key: string): void {
  try {
    sessionStorage.removeItem(key);
  } catch {
    // The in-memory decision result remains authoritative for this page.
  }
}

function verifiedEvidence(
  anchor: EvidenceAnchor,
  documents: ReviewDocument[],
  frozenParsedDocumentIds: string[],
): { evidence: ClauseProposalEvidence; filename: string } | null {
  if (
    anchor.status !== "located" ||
    !anchor.parsed_document_id ||
    !anchor.normalized_digest ||
    !anchor.text_offset ||
    !frozenParsedDocumentIds.includes(anchor.parsed_document_id)
  )
    return null;
  const source = documents.find(
    (document) =>
      document.id === anchor.document_version_id &&
      document.parsed_document_id === anchor.parsed_document_id &&
      document.parsed_document?.id === anchor.parsed_document_id &&
      document.parsed_document?.document_id === anchor.document_version_id &&
      document.parsed_document?.status === "ready" &&
      document.parsed_document.normalized_digest === anchor.normalized_digest,
  );
  if (!source) return null;
  const resolved = resolveAnchor(
    source.text,
    anchor,
    source.parsed_document,
    source.parsed_document_id,
  );
  if (
    resolved.kind !== "highlight" ||
    resolved.start !== anchor.text_offset.start ||
    resolved.end !== anchor.text_offset.end
  )
    return null;
  return {
    evidence: {
      document_id: anchor.document_version_id,
      parsed_document_id: anchor.parsed_document_id,
      normalized_digest: anchor.normalized_digest,
      start: resolved.start,
      end: resolved.end,
      quote: anchor.quote,
    },
    filename: source.filename,
  };
}

function evidenceLabel(anchor: EvidenceAnchor, filename: string): string {
  const page = anchor.page === null ? "页码未记录" : `第${anchor.page}页`;
  return `${filename} · ${page} · ${anchor.quote.slice(0, 48)}`;
}

function statusLabel(status: string): { label: string; color: string } {
  switch (status) {
    case "pending":
      return { label: "待人工处理", color: "processing" };
    case "accept":
      return { label: "已接受并生成新修订", color: "success" };
    case "accept_edited":
      return { label: "已编辑接受并生成新修订", color: "success" };
    case "reject":
      return { label: "已拒绝", color: "default" };
    default:
      return { label: "未知状态", color: "warning" };
  }
}

function historyLabel(action: string): string {
  if (action === "accept") return "按建议接受";
  if (action === "accept_edited") return "编辑后接受";
  if (action === "reject") return "拒绝建议";
  return "未知操作";
}

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}

function currentDraftMaterials(
  detail: CaseDetail | undefined,
  proposal: ClauseProposal,
  uploaded: UploadedClauseDraft | null,
) {
  if (!detail?.materials || !detail.material_versions) return [];
  const materialVersions = detail.material_versions;
  const sourceDocumentId = proposal.evidence?.document_id;
  if (!sourceDocumentId) return [];
  return detail.materials.flatMap((material) => {
    if (material.role !== "draft_contract") return [];
    const isFrozenSource = material.current_document_id === sourceDocumentId;
    const isSavedUpload =
      uploaded !== null &&
      uploaded.case_id === detail.id &&
      uploaded.proposal_id === proposal.id &&
      uploaded.proposal_version === proposal.version &&
      uploaded.material_id === material.material_id &&
      material.current_document_id === uploaded.document_id;
    if (!isFrozenSource && !isSavedUpload) return [];
    const document = detail.documents.find(
      (item) =>
        item.id === material.current_document_id &&
        !item.superseded &&
        item.role === "draft_contract",
    );
    if (!document) return [];
    const version = materialVersions.find(
      (item) =>
        item.material_id === material.material_id &&
        item.document_id === document.id &&
        item.version === material.current_version,
    );
    if (!version) return [];
    return [{ material, document, version }];
  });
}

function writeUnavailableReason(caseDetail: CaseDetail | undefined, canWrite: boolean): string {
  if (!caseDetail) return "正在读取案件和材料状态，写入操作暂不可用。";
  if (caseDetail.archived_at !== null) return "该案件已归档，不能继续创建或处理条款建议。";
  if (!canWrite) return "当前审查权限不允许写入；最终授权仍由服务端复核。";
  return "";
}

function ProposalHistory({ proposal }: { proposal: ClauseProposal }) {
  if (proposal.history.length === 0) return <p className="meta">尚无人工处理历史。</p>;
  return (
    <section aria-label="条款建议处理历史">
      <h5>处理历史</h5>
      <ol>
        {proposal.history.map((entry) => (
          <li key={`${entry.version}:${entry.created_at}`}>
            <strong>{historyLabel(entry.action)}</strong> ·{" "}
            {new Date(entry.created_at).toLocaleString()}
            <p>操作人：{entry.actor_id}</p>
            {entry.reason ? <p>理由：{entry.reason}</p> : null}
            {entry.accepted_text ? <blockquote>{entry.accepted_text}</blockquote> : null}
            {entry.revision_id ? <p>生成合同修订：{entry.revision_id}</p> : null}
            {entry.contract_id ? <p>合同：{entry.contract_id}</p> : null}
          </li>
        ))}
      </ol>
    </section>
  );
}

function ClauseProposalCard({
  context,
  proposal,
  actor,
  actorScope,
}: {
  context: ClauseProposalContext;
  proposal: ClauseProposal;
  actor: Actor | null;
  actorScope: string;
}) {
  const queryClient = useQueryClient();
  const derive = useDeriveClauseProposalText(context.caseId, proposal.id);
  const upload = useUploadDocument(context.caseId);
  const decide = useDecideClauseProposal(context.caseId, proposal.id);
  const [mode, setMode] = useState<DecisionMode | null>(null);
  const [acceptedText, setAcceptedText] = useState(proposal.proposed_text);
  const [decisionReason, setDecisionReason] = useState("");
  const [contractId, setContractId] = useState("");
  const [materialId, setMaterialId] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [derivedText, setDerivedText] = useState<string | null>(null);
  const [uploaded, setUploaded] = useState<UploadedClauseDraft | null>(null);
  const [storageWarning, setStorageWarning] = useState(false);
  const [resubmitError, setResubmitError] = useState<string | null>(null);
  const [staleConflict, setStaleConflict] = useState(false);
  const uploadKey = uploadStorageKey(actorScope, context.caseId, proposal.id);
  const savedUpload = uploaded ?? readUploadedDraft(uploadKey);
  const ledger = useContractLedger({}, actorScope, actor !== null);
  const contracts = useMemo(
    () => (ledger.isError ? [] : (ledger.data?.pages.flatMap((page) => page.items) ?? [])),
    [ledger.data, ledger.isError],
  );
  const effectiveContractId = contractId || savedUpload?.contract_id || "";
  const selectedContract =
    contracts.find((item) => item.contract_id === effectiveContractId) ?? null;
  const contractChoices = contracts.map((item) => ({
    value: item.contract_id,
    label: `${item.name}${item.number ? `（${item.number}）` : ""} · ${item.workflow_state}`,
  }));
  const draftMaterials = useMemo(
    () => currentDraftMaterials(context.caseDetail, proposal, savedUpload ?? null),
    [context.caseDetail, proposal, savedUpload],
  );
  const effectiveMaterialId = materialId || savedUpload?.material_id || "";
  const selectedMaterial =
    draftMaterials.find((item) => item.material.material_id === effectiveMaterialId) ?? null;
  const activeMode = mode ?? savedUpload?.action ?? null;
  const currentAction = activeMode === "accept_edited" ? "accept_edited" : "accept";
  const replacementText =
    activeMode === "accept_edited"
      ? mode === null
        ? (savedUpload?.accepted_text ?? proposal.proposed_text)
        : acceptedText
      : proposal.proposed_text;
  const uploadedMatches = Boolean(
    savedUpload &&
    activeMode !== "reject" &&
    savedUpload.case_id === context.caseId &&
    savedUpload.proposal_id === proposal.id &&
    savedUpload.proposal_version === proposal.version &&
    savedUpload.contract_id === effectiveContractId &&
    savedUpload.material_id === selectedMaterial?.material.material_id &&
    savedUpload.action === currentAction &&
    savedUpload.accepted_text === replacementText,
  );
  const uploadedDocument = savedUpload
    ? context.caseDetail?.documents.find((item) => item.id === savedUpload.document_id)
    : undefined;
  const uploadedParseReady =
    uploadedMatches &&
    uploadedDocument?.parse_status === "ready" &&
    uploadedDocument.parsed_document?.status === "ready";
  const currentStatus = statusLabel(proposal.status);
  const pending = proposal.status === "pending";
  const history = proposal.history.at(-1);
  const linkedRemediation = context.remediations.find(
    (item) => item.id === proposal.remediation_id && item.case_id === context.caseId,
  );
  const evidence = proposal.evidence;
  const sourceDocuments = evidence
    ? context.documents.filter(
        (document) =>
          document.id === evidence.document_id &&
          document.parsed_document_id === evidence.parsed_document_id &&
          document.parse_status === "ready" &&
          document.text !== null &&
          (!document.parsed_document ||
            (document.parsed_document.id === evidence.parsed_document_id &&
              document.parsed_document.document_id === evidence.document_id &&
              document.parsed_document.status === "ready" &&
              document.parsed_document.normalized_digest === evidence.normalized_digest)) &&
          sliceByCodePoints(document.text, evidence.start, evidence.end) === evidence.quote,
      )
    : [];
  const sourceDocument = sourceDocuments.length === 1 ? sourceDocuments[0] : undefined;
  const evidenceVerified =
    proposal.evidence_verified &&
    (!context.immersive ||
      Boolean(
        evidence &&
        sourceDocument &&
        context.frozenParsedDocumentIds.includes(evidence.parsed_document_id) &&
        evidence.quote.trim() !== "" &&
        (!proposal.original_text || proposal.original_text === evidence.quote),
      ));
  const sourceTitle = evidenceVerified
    ? proposal.original_text || proposal.evidence?.quote || "审查留档的原文未返回"
    : (proposal.unavailable_reason ?? "原文位置未能核验");
  const decisionReasonValid =
    decisionReason.trim().length > 0 && utf8Length(decisionReason.trim()) <= 4000;
  const acceptedTextValid =
    replacementText.trim().length > 0 && utf8Length(replacementText) <= 16000;

  function startDecision(next: DecisionMode) {
    setMode(next);
    if (next === "accept_edited") {
      setAcceptedText(
        savedUpload?.action === "accept_edited"
          ? savedUpload.accepted_text
          : proposal.proposed_text,
      );
    }
    setDecisionReason("");
    setConfirmed(false);
    setDerivedText(null);
    setResubmitError(null);
    setStaleConflict(false);
  }

  async function deriveText() {
    if (
      !pending ||
      !evidenceVerified ||
      activeMode === null ||
      activeMode === "reject" ||
      !acceptedTextValid ||
      savedUpload
    )
      return;
    setResubmitError(null);
    try {
      const result = await derive.mutateAsync({
        expected_version: proposal.version,
        ...(activeMode === "accept_edited" ? { edited_text: replacementText } : {}),
      });
      setDerivedText(result.text);
    } catch (error) {
      setResubmitError(responseError(error, "生成派生修改稿"));
    }
  }

  async function uploadDerivedText() {
    if (
      !actor ||
      !derivedText ||
      !selectedMaterial ||
      !selectedContract ||
      activeMode === null ||
      activeMode === "reject" ||
      !evidenceVerified ||
      !acceptedTextValid ||
      savedUpload
    )
      return;
    setResubmitError(null);
    try {
      const commandContext = captureCommandContext(actor);
      const file = new File([derivedText], "条款修订稿.txt", { type: "text/plain;charset=utf-8" });
      const sha256 = await fileSha256(file);
      const value = {
        case_id: context.caseId,
        proposal_id: proposal.id,
        proposal_version: proposal.version,
        contract_id: selectedContract.contract_id,
        accepted_text: replacementText,
        action: currentAction,
        material_id: selectedMaterial.material.material_id,
        replaces_document_id: selectedMaterial.document.id,
        sha256,
      };
      const pendingKey = await pendingMutationKey(
        "contract.clause-proposal.material-upload",
        idempotencyActorScope(actor),
        `${context.caseId}:${proposal.id}`,
        value,
        commandContext,
      );
      assertCommandContextCurrent(commandContext);
      const result = await upload.mutateAsync({
        file,
        role: "draft_contract",
        intent: "replace",
        purpose: selectedMaterial.material.purpose,
        material_id: selectedMaterial.material.material_id,
        replaces_document_id: selectedMaterial.document.id,
        idempotency_key: pendingKey.key,
      });
      const uploadedRef: UploadedClauseDraft = {
        case_id: context.caseId,
        proposal_id: proposal.id,
        proposal_version: proposal.version,
        contract_id: selectedContract.contract_id,
        accepted_text: replacementText,
        action: currentAction,
        material_id: result.material_id,
        document_id: result.id,
        material_version: result.version,
        sha256,
        upload_key: pendingKey.key,
      };
      setUploaded(uploadedRef);
      setStorageWarning(!storeUploadedDraft(uploadKey, uploadedRef));
    } catch (error) {
      setResubmitError(responseError(error, "上传派生 TXT 修改稿"));
    }
  }

  async function submitDecision(action: DecisionMode) {
    if (!actor || !pending || !context.canWrite || !confirmed || !decisionReasonValid) return;
    if (
      action !== "reject" &&
      (!evidenceVerified || !selectedContract || !uploadedMatches || !savedUpload)
    )
      return;
    setResubmitError(null);
    const input: ClauseProposalDecisionInput =
      action === "reject"
        ? {
            action,
            confirmed: true,
            expected_version: proposal.version,
            reason: decisionReason.trim(),
          }
        : {
            action,
            confirmed: true,
            expected_version: proposal.version,
            contract_id: selectedContract!.contract_id,
            expected_contract_row_version: selectedContract!.row_version,
            ...(action === "accept_edited" ? { accepted_text: replacementText } : {}),
            reason: decisionReason.trim(),
            material_id: savedUpload!.material_id,
            document_id: savedUpload!.document_id,
            sha256: savedUpload!.sha256,
          };
    try {
      const commandContext = captureCommandContext(actor);
      const pendingKey = await pendingMutationKey(
        "contract.clause-proposal.decision",
        idempotencyActorScope(actor),
        `${context.caseId}:${proposal.id}`,
        input,
        commandContext,
      );
      assertCommandContextCurrent(commandContext);
      const result = await decide.mutateAsync({ value: input, idempotencyKey: pendingKey.key });
      clearPendingMutationKey(pendingKey.storageKey);
      if (result.status === "accept" || result.status === "accept_edited") {
        removeUploadedDraft(uploadKey);
        setUploaded(null);
      }
      setMode(null);
      setConfirmed(false);
    } catch (error) {
      setResubmitError(responseError(error, action === "reject" ? "拒绝条款建议" : "接受条款建议"));
      setStaleConflict(isConflict(error));
    }
  }

  function downloadDerivedText() {
    if (derivedText === null) return;
    const url = URL.createObjectURL(new Blob([derivedText], { type: "text/plain;charset=utf-8" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "条款修订稿.txt";
    link.click();
    URL.revokeObjectURL(url);
  }

  const status = <Tag color={currentStatus.color}>{currentStatus.label}</Tag>;
  return (
    <Card
      size="small"
      title={
        <Space>
          {`条款建议 v${proposal.version}`} {status}
        </Space>
      }
    >
      {context.immersive ? (
        <div className="review-clause-proposal">
          {sourceDocument ? <p className="meta">{sourceDocument.filename}</p> : null}
          {evidenceVerified ? (
            <div className="review-clause-diff">
              <p aria-label="原条款">
                <del>
                  <span aria-hidden="true">− </span>
                  {sourceTitle}
                </del>
              </p>
              <p aria-label="建议修改">
                <ins>
                  <span aria-hidden="true">+ </span>
                  {proposal.proposed_text}
                </ins>
              </p>
            </div>
          ) : (
            <blockquote>{proposal.proposed_text}</blockquote>
          )}
          <p className="meta">{proposal.reason}</p>
        </div>
      ) : (
        <dl>
          <dt>来源审查</dt>
          <dd>
            run {proposal.run_id} · attempt {proposal.attempt_id}
          </dd>
          <dt>原条款</dt>
          <dd>
            {evidenceVerified ? (
              <blockquote>
                {proposal.before_context}
                <mark>{sourceTitle}</mark>
                {proposal.after_context}
              </blockquote>
            ) : (
              <p>{sourceTitle}</p>
            )}
          </dd>
          <dt>建议文本</dt>
          <dd>
            <blockquote>{proposal.proposed_text}</blockquote>
          </dd>
          <dt>建议理由</dt>
          <dd>{proposal.reason}</dd>
          {proposal.remediation_id ? (
            <>
              <dt>关联整改</dt>
              <dd>
                {linkedRemediation ? (
                  <Link
                    to={`/cases/${encodeURIComponent(context.caseId)}?work_kind=remediation&work_item=${encodeURIComponent(linkedRemediation.id)}#case-remediation`}
                  >
                    打开关联整改：{linkedRemediation.measure.slice(0, 80)}
                  </Link>
                ) : (
                  "已记录整改身份；当前授权案件列表未返回此记录。"
                )}
              </dd>
            </>
          ) : null}
        </dl>
      )}
      {!context.immersive ? (
        <TechnicalDetail
          label="条款建议来源技术详情"
          value={JSON.stringify({
            input_hash: proposal.input_hash,
            evidence: proposal.evidence,
            rule_pack: proposal.rule_pack,
            playbook: proposal.playbook,
            remediation_id: proposal.remediation_id,
          })}
        />
      ) : null}
      {!context.immersive || proposal.history.length > 0 ? (
        <ProposalHistory proposal={proposal} />
      ) : null}
      {!evidenceVerified ? (
        <Alert
          type="warning"
          title="引用未核验：不能接受为合同修改。"
          description={
            proposal.unavailable_reason ??
            "服务端无法确认该建议与本次审查留档的原文位置一致；可以查看或拒绝此候选。"
          }
        />
      ) : null}
      {history?.revision_id ? (
        <Alert
          type="success"
          title="已追加合同修订；旧材料、旧审查和本条处理历史均保留。"
          description={
            <>
              <p>要复核修改后的材料，请创建一轮新审查。旧审查的重试继续使用原留档材料。</p>
              <Link to={`/cases/${encodeURIComponent(context.caseId)}`}>
                返回案件页，选择审查范围和业务时点并发起新审查
              </Link>
            </>
          }
        />
      ) : null}
      {proposal.status === "reject" && savedUpload ? (
        <Alert
          type="warning"
          title="该候选已拒绝；此前上传的材料保留在案件中，没有被接受为合同修订。"
          description={`材料 ${savedUpload.material_id} v${savedUpload.material_version} · 文档 ${savedUpload.document_id}`}
        />
      ) : null}
      {pending && context.canWrite ? (
        <section aria-label="条款建议处理">
          {!context.immersive ? <h5>人工处理</h5> : null}
          <Space wrap>
            <Button
              disabled={
                !evidenceVerified || Boolean(savedUpload && savedUpload.action !== "accept")
              }
              onClick={() => startDecision("accept")}
            >
              {context.immersive ? "采纳这条修改" : "按原建议生成修改稿"}
            </Button>
            <Button
              disabled={
                !evidenceVerified || Boolean(savedUpload && savedUpload.action !== "accept_edited")
              }
              onClick={() => startDecision("accept_edited")}
            >
              {context.immersive ? "改一下再采纳" : "编辑后生成修改稿"}
            </Button>
            <Button danger onClick={() => startDecision("reject")}>
              拒绝建议
            </Button>
          </Space>
          {activeMode ? (
            <div className="workflow-form">
              <h6>
                {activeMode === "reject"
                  ? "拒绝候选"
                  : activeMode === "accept"
                    ? "按建议接受"
                    : "编辑后接受"}
              </h6>
              {activeMode !== "reject" ? (
                <>
                  {activeMode === "accept" ? (
                    <blockquote>{proposal.proposed_text}</blockquote>
                  ) : (
                    <TextArea
                      aria-label="确认的替换条款"
                      value={replacementText}
                      onChange={(event) => {
                        setAcceptedText(event.target.value);
                        setDerivedText(null);
                      }}
                      rows={5}
                      disabled={Boolean(savedUpload)}
                    />
                  )}
                  {activeMode === "accept_edited" ? (
                    <p className="meta">UTF-8 {utf8Length(replacementText)}/16000 字节</p>
                  ) : null}
                  <label htmlFor={`clause-contract-${proposal.id}`}>生成修订的合同</label>
                  {ledger.isError ? (
                    <Alert type="error" title={responseError(ledger.error, "读取合同目录")} />
                  ) : (
                    <Select
                      id={`clause-contract-${proposal.id}`}
                      value={effectiveContractId || undefined}
                      options={contractChoices}
                      showSearch={{ optionFilterProp: "label" }}
                      placeholder="选择当前授权合同"
                      onChange={setContractId}
                      style={{ width: "100%" }}
                      loading={ledger.isLoading}
                      disabled={Boolean(savedUpload)}
                    />
                  )}
                  {!ledger.isError && ledger.hasNextPage ? (
                    <Button
                      type="link"
                      loading={ledger.isFetchingNextPage}
                      onClick={() => void ledger.fetchNextPage()}
                    >
                      加载更多合同
                    </Button>
                  ) : null}
                  {draftMaterials.length > 0 ? (
                    <>
                      <label htmlFor={`clause-material-${proposal.id}`}>替换当前草稿材料</label>
                      <p className="meta">
                        这里只能替换与本次留档引用相同的现行材料；若它已变化，需重新核对来源，不能跨材料生成修订。
                      </p>
                      <Select
                        id={`clause-material-${proposal.id}`}
                        value={effectiveMaterialId || undefined}
                        placeholder="选择本案件中的现行合同草稿材料"
                        onChange={setMaterialId}
                        style={{ width: "100%" }}
                        disabled={Boolean(savedUpload)}
                        options={draftMaterials.map(({ material, document, version }) => ({
                          value: material.material_id,
                          label: `${document.filename} · 材料 v${version.version}`,
                        }))}
                      />
                    </>
                  ) : (
                    <Alert
                      type="warning"
                      title="没有与本次留档引用直接对应的现行合同草稿材料。"
                      description="只能替换当前仍以该留档原件为直接前一版本的草稿材料；材料已变化时请重新核对案件来源。"
                    />
                  )}
                  <label htmlFor={`clause-decision-reason-${proposal.id}`}>本次处理理由</label>
                  <TextArea
                    id={`clause-decision-reason-${proposal.id}`}
                    value={decisionReason}
                    onChange={(event) => setDecisionReason(event.target.value)}
                    rows={3}
                    maxLength={4000}
                    placeholder="说明为何接受或编辑这条建议。"
                  />
                  <p className="meta">UTF-8 {utf8Length(decisionReason.trim())}/4000 字节</p>
                  <Space wrap>
                    <Button
                      onClick={() => void deriveText()}
                      loading={derive.isPending}
                      disabled={
                        !evidenceVerified ||
                        !selectedContract ||
                        !selectedMaterial ||
                        !acceptedTextValid
                      }
                    >
                      生成服务端派生 TXT 预览
                    </Button>
                    <Button onClick={downloadDerivedText} disabled={derivedText === null}>
                      下载 TXT 预览
                    </Button>
                  </Space>
                  {derivedText !== null ? (
                    <>
                      <Alert
                        type="info"
                        title="这是从审查留档原文派生的纯文本修改稿，不会保留原 DOCX/PDF 排版、签章或页码。"
                      />
                      <pre style={{ maxHeight: 320, overflow: "auto", whiteSpace: "pre-wrap" }}>
                        {derivedText}
                      </pre>
                      <Button
                        onClick={() => void uploadDerivedText()}
                        loading={upload.isPending}
                        disabled={
                          !selectedMaterial ||
                          !selectedContract ||
                          !context.canWrite ||
                          uploadedMatches
                        }
                      >
                        {uploadedMatches ? "修改稿已上传" : "上传为真实合同材料版本"}
                      </Button>
                    </>
                  ) : null}
                  {savedUpload ? (
                    <Alert
                      type={uploadedMatches && uploadedParseReady ? "success" : "warning"}
                      title={
                        uploadedMatches
                          ? uploadedParseReady
                            ? `已上传且解析就绪：材料 ${savedUpload.material_id} v${savedUpload.material_version}；尚未形成合同修订。`
                            : `已上传材料 ${savedUpload.material_id} v${savedUpload.material_version}；等待服务端返回可验证的解析结果。`
                          : "已有上传材料，但它不匹配当前候选、合同或替换文本；不能改绑或重复上传，请刷新候选和授权合同后核对。"
                      }
                      description={
                        uploadedMatches && uploadedDocument?.parse_status === "failed"
                          ? `解析失败：${uploadedDocument.parse_error ?? "未提供原因"}。请按现有材料流程处理解析失败；不能接受此材料。`
                          : storageWarning
                            ? "浏览器未能保存续办记录；请在本页完成确认。"
                            : "上传与接受分为两步。Core 失败时，保留此引用并可按原请求重试，不要重新生成材料。"
                      }
                    />
                  ) : null}
                  {savedUpload && uploadedMatches && !uploadedParseReady ? (
                    <Button
                      onClick={() =>
                        void queryClient.invalidateQueries({
                          queryKey: queryKeys.case(context.caseId),
                          exact: true,
                        })
                      }
                    >
                      重新读取上传材料状态
                    </Button>
                  ) : null}
                  <Checkbox
                    checked={confirmed}
                    onChange={(event) => setConfirmed(event.target.checked)}
                  >
                    我确认按上述条款生成新合同修订，并保留原材料和历史。
                  </Checkbox>
                  <Button
                    type="primary"
                    onClick={() => void submitDecision(activeMode)}
                    loading={decide.isPending}
                    disabled={
                      !confirmed ||
                      !decisionReasonValid ||
                      !evidenceVerified ||
                      !selectedContract ||
                      !uploadedParseReady
                    }
                  >
                    确认并生成合同新修订
                  </Button>
                </>
              ) : (
                <>
                  <p>拒绝只追加人工决定，不修改合同或材料。</p>
                  <label htmlFor={`clause-reject-reason-${proposal.id}`}>拒绝理由</label>
                  <TextArea
                    id={`clause-reject-reason-${proposal.id}`}
                    value={decisionReason}
                    onChange={(event) => setDecisionReason(event.target.value)}
                    rows={3}
                    maxLength={4000}
                    showCount
                  />
                  <Checkbox
                    checked={confirmed}
                    onChange={(event) => setConfirmed(event.target.checked)}
                  >
                    我确认拒绝这条条款建议。
                  </Checkbox>
                  <Button
                    danger
                    onClick={() => void submitDecision("reject")}
                    loading={decide.isPending}
                    disabled={!confirmed || !decisionReasonValid}
                  >
                    确认拒绝
                  </Button>
                </>
              )}
              {resubmitError ? <Alert type="error" title={resubmitError} /> : null}
              {staleConflict ? (
                <Button
                  onClick={() => {
                    setStaleConflict(false);
                    setResubmitError(null);
                    decide.reset();
                    void queryClient.invalidateQueries({
                      queryKey: queryKeys.clauseProposals(context.caseId, actorScope),
                      exact: true,
                    });
                    void queryClient.invalidateQueries({
                      queryKey: queryKeys.contractLedgerPrefix,
                    });
                    void queryClient.invalidateQueries({
                      queryKey: queryKeys.case(context.caseId),
                      exact: true,
                    });
                  }}
                >
                  重新读取候选和合同状态
                </Button>
              ) : null}
            </div>
          ) : null}
        </section>
      ) : null}
      {!context.canWrite && pending ? (
        <p className="meta">
          {writeUnavailableReason(context.caseDetail, context.canWrite)}可以核对候选和历史。
        </p>
      ) : null}
      {proposal.history.some((entry) => entry.contract_id) ? (
        <TechnicalDetail label="处理历史技术详情" value={JSON.stringify(proposal.history)} />
      ) : null}
      {history?.revision_id ? (
        <Typography.Text type="secondary">
          新审查会另建记录。重试旧审查仍使用原留档材料。
        </Typography.Text>
      ) : null}
    </Card>
  );
}

export function ClauseProposalPanel({
  caseId,
  runId,
  attemptId,
  frozenParsedDocumentIds,
  finding,
  documents,
  remediations,
  caseDetail,
  canWrite,
  immersive = false,
}: ClauseProposalContext) {
  const session = useSession();
  const actor = session.isSuccess && !session.isFetching ? session.data : null;
  const actorScope = actor ? actorReadContextKey(actor) : "";
  const proposalsQuery = useCaseClauseProposals(
    caseId,
    actorScope,
    actor !== null && attemptId !== "",
  );
  const create = useCreateClauseProposal(caseId);
  const [selectedEvidenceId, setSelectedEvidenceId] = useState("");
  const [proposedText, setProposedText] = useState("");
  const [reason, setReason] = useState("");
  const [remediationId, setRemediationId] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [createdId, setCreatedId] = useState<string | null>(null);
  const validAnchors = useMemo(
    () =>
      finding.evidence.flatMap((anchor) => {
        const verified = verifiedEvidence(anchor, documents, frozenParsedDocumentIds);
        return verified ? [{ anchor, ...verified }] : [];
      }),
    [documents, finding.evidence, frozenParsedDocumentIds],
  );
  const selectedEvidence =
    validAnchors.find((item) => item.anchor.id === selectedEvidenceId) ?? validAnchors[0] ?? null;
  const queryItems = proposalsQuery.isError ? [] : (proposalsQuery.data?.items ?? []);
  const proposals = queryItems.filter(
    (item) =>
      item.case_id === caseId &&
      item.run_id === runId &&
      item.attempt_id === attemptId &&
      item.finding_id === finding.id,
  );
  const activeRemediations = remediations.filter((item) => item.case_id === caseId);
  const canCreate =
    canWrite &&
    !!actor &&
    attemptId !== "" &&
    validAnchors.length > 0 &&
    proposedText.trim() !== "" &&
    utf8Length(proposedText) <= 16000 &&
    reason.trim() !== "" &&
    utf8Length(reason.trim()) <= 4000 &&
    proposalsQuery.isSuccess &&
    !proposalsQuery.isFetching &&
    !create.isPending;

  async function createProposal() {
    if (!canCreate || !actor || !selectedEvidence) return;
    setCreateError(null);
    const input: CreateClauseProposalInput = {
      run_id: runId,
      attempt_id: attemptId,
      finding_id: finding.id,
      evidence: selectedEvidence.evidence,
      proposed_text: proposedText.trim(),
      reason: reason.trim(),
      ...(remediationId ? { remediation_id: remediationId } : {}),
    };
    try {
      const commandContext = captureCommandContext(actor);
      const pending = await pendingMutationKey(
        "contract.clause-proposal.create",
        idempotencyActorScope(actor),
        `${caseId}:${finding.id}`,
        input,
        commandContext,
      );
      assertCommandContextCurrent(commandContext);
      const created = await create.mutateAsync({ value: input, idempotencyKey: pending.key });
      clearPendingMutationKey(pending.storageKey);
      setCreatedId(created.id);
      setProposedText("");
      setReason("");
      setRemediationId("");
      setSelectedEvidenceId("");
    } catch (error) {
      setCreateError(responseError(error, "保存条款候选"));
    }
  }

  const proposalCards = proposals.map((proposal) => (
    <ClauseProposalCard
      key={`${actorScope}:${proposal.id}`}
      context={{
        caseId,
        runId,
        attemptId,
        frozenParsedDocumentIds,
        finding,
        documents,
        remediations,
        caseDetail,
        canWrite: canWrite && proposalsQuery.isSuccess && !proposalsQuery.isFetching,
        immersive,
      }}
      proposal={proposal}
      actor={actor}
      actorScope={actorScope}
    />
  ));
  const showProposalTools =
    !immersive || !proposalsQuery.isSuccess || (canWrite && validAnchors.length > 0);
  if (!showProposalTools && proposalCards.length === 0) return null;
  const panelContent = (
    <div className="workflow-form">
      {session.isLoading || session.isFetching ? <p>正在确认当前登录权限…</p> : null}
      {session.isError ? (
        <Alert type="error" title={responseError(session.error, "确认登录状态")} />
      ) : null}
      {proposalsQuery.isLoading ? <p>正在读取条款建议…</p> : null}
      {proposalsQuery.isError ? (
        <Alert type="error" title={responseError(proposalsQuery.error, "读取条款建议")} />
      ) : null}
      {!immersive ? proposalCards : null}
      {proposalsQuery.isSuccess && proposals.length === 0 ? (
        <p className="meta">本条线索尚无条款建议。</p>
      ) : null}
      {attemptId === "" ? (
        <Alert
          type="warning"
          title="当前选中的是历史记录或缺少留档的审查记录，不能从这里创建条款修改候选。"
        />
      ) : validAnchors.length === 0 ? (
        <Alert
          type="warning"
          title="本条线索没有可核验的本次留档引用。"
          description="引用不完整、无法定位或与本次解析摘要不符时，只能查看候选，不能接受为合同修改。"
        />
      ) : null}
      {canWrite && attemptId !== "" && validAnchors.length > 0 ? (
        <section aria-label="创建条款建议">
          <h4>提出条款修改候选</h4>
          <label htmlFor={`clause-evidence-${finding.id}`}>本次审查中可核验的原文位置</label>
          <Select
            id={`clause-evidence-${finding.id}`}
            value={selectedEvidence?.anchor.id}
            onChange={setSelectedEvidenceId}
            options={validAnchors.map(({ anchor, filename }) => ({
              value: anchor.id,
              label: evidenceLabel(anchor, filename),
            }))}
            style={{ width: "100%" }}
          />
          {selectedEvidence ? <blockquote>{selectedEvidence.evidence.quote}</blockquote> : null}
          <label htmlFor={`clause-proposed-text-${finding.id}`}>建议替换条款</label>
          <TextArea
            id={`clause-proposed-text-${finding.id}`}
            value={proposedText}
            onChange={(event) => setProposedText(event.target.value)}
            rows={4}
            maxLength={16000}
          />
          <p className="meta">UTF-8 {utf8Length(proposedText)}/16000 字节</p>
          <label htmlFor={`clause-proposal-reason-${finding.id}`}>建议理由</label>
          <TextArea
            id={`clause-proposal-reason-${finding.id}`}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            rows={3}
            maxLength={4000}
          />
          <p className="meta">UTF-8 {utf8Length(reason.trim())}/4000 字节</p>
          {activeRemediations.length > 0 ? (
            <>
              <label htmlFor={`clause-remediation-${finding.id}`}>关联已有整改（可选）</label>
              <Select
                id={`clause-remediation-${finding.id}`}
                value={remediationId || undefined}
                onChange={setRemediationId}
                allowClear
                placeholder="不关联整改"
                style={{ width: "100%" }}
                options={activeRemediations.map((item) => ({
                  value: item.id,
                  label: `${item.status} · ${item.measure.slice(0, 80)}`,
                }))}
              />
            </>
          ) : (
            <p className="meta">当前案件没有可关联的既有整改。</p>
          )}
          <Button
            type="primary"
            onClick={() => void createProposal()}
            loading={create.isPending}
            disabled={!canCreate}
          >
            保存条款候选
          </Button>
          {createdId ? (
            <Alert
              type="success"
              title="条款候选已追加保存。"
              description={`候选编号：${createdId}`}
            />
          ) : null}
          {createError ? <Alert type="error" title={createError} /> : null}
        </section>
      ) : !canWrite ? (
        <p className="meta">{writeUnavailableReason(caseDetail, canWrite)}可核对本次候选和历史。</p>
      ) : null}
    </div>
  );

  return (
    <section aria-label="条款建议与修订">
      {immersive ? proposalCards : null}
      {showProposalTools ? (
        <Collapse
          items={[
            {
              key: "clause-proposals",
              label: immersive ? "新建或查看条款建议" : "条款建议与修订",
              children: panelContent,
            },
          ]}
        />
      ) : null}
    </section>
  );
}
