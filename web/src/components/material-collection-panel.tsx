import { Button, Checkbox, Select, Upload } from "antd";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, errorMessage } from "../api/http";
import { fileSha256, idempotencyFingerprint, newIdempotencyKey } from "../api/idempotency";
import {
  useConfirmLegacyMaterialSelection,
  useReparse,
  useUploadDocument,
} from "../api/queries";
import type { CaseDetail, MaterialProjection, MaterialPurpose } from "../api/types";
import { formatDateTime, parseLabel, parseTone, roleLabel } from "../domain/review";
import { ParsedDocumentSummary } from "./parsed-document-summary";
import { StatusText } from "./status-text";

const roles: Array<{ value: MaterialProjection["role"]; label: string }> = [
  { value: "procurement", label: "采购文件" },
  { value: "award", label: "中标文件" },
  { value: "draft_contract", label: "拟签合同" },
  { value: "attachment", label: "附件" },
];

const attachmentPurposes: Array<{ value: MaterialPurpose; label: string }> = [
  { value: "supplement", label: "补充协议" },
  { value: "acceptance", label: "验收材料" },
  { value: "invoice", label: "发票材料" },
  { value: "correspondence", label: "往来函件" },
  { value: "other", label: "其他附件" },
];

const purposeLabels: Record<MaterialPurpose, string> = {
  primary_contract: "主合同",
  supplement: "补充协议",
  procurement: "采购文件",
  award: "中标文件",
  acceptance: "验收材料",
  invoice: "发票材料",
  correspondence: "往来函件",
  other: "其他附件",
};

interface Props {
  caseDetail: CaseDetail;
  canWrite: boolean;
  actorId: string;
}

function fixedPurpose(role: MaterialProjection["role"], attachmentPurpose: MaterialPurpose): MaterialPurpose {
  if (role === "procurement") return "procurement";
  if (role === "award") return "award";
  if (role === "draft_contract") return "primary_contract";
  return attachmentPurpose;
}

function legacyStatusLabel(material: MaterialProjection): string {
  if (material.legacy_status === "native") return "本系统材料";
  if (material.legacy_status === "confirmed") return "已确认旧材料";
  return material.legacy_selected ? "旧系统兼容选择，待确认" : "旧材料待确认";
}

export function MaterialCollectionPanel({ caseDetail, canWrite, actorId }: Props) {
  const upload = useUploadDocument(caseDetail.id);
  const reparse = useReparse(caseDetail.id);
  const confirmSelection = useConfirmLegacyMaterialSelection(caseDetail.id);
  const materials = caseDetail.materials;
  const versions = caseDetail.material_versions;
  const selection = caseDetail.material_selection;
  const [role, setRole] = useState<MaterialProjection["role"]>("procurement");
  const [attachmentPurpose, setAttachmentPurpose] = useState<MaterialPurpose>("supplement");
  const [intent, setIntent] = useState<"add" | "replace">("add");
  const [targetMaterialId, setTargetMaterialId] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [hashingFile, setHashingFile] = useState(false);
  const [uploadPreparationError, setUploadPreparationError] = useState<string | null>(null);
  const [legacyDocumentIds, setLegacyDocumentIds] = useState<string[]>([]);
  const [selectionNote, setSelectionNote] = useState<string | null>(null);
  const lastSelectionVersion = useRef<number | null>(null);
  const uploadRetry = useRef<{ file: File; signature: string; key: string } | null>(null);
  const completedUploadKeys = useRef(new Set<string>());

  useEffect(() => {
    if (!selection || lastSelectionVersion.current === selection.version) return;
    const initial = selection.status === "pending_confirmation"
      ? selection.candidate_document_ids ?? (materials ?? []).filter((material) => material.legacy_selected).map((material) => material.current_document_id)
      : selection.document_ids;
    setLegacyDocumentIds(initial);
    setSelectionNote(null);
    lastSelectionVersion.current = selection.version;
  }, [materials, selection]);

  if (!materials || !versions || !selection) {
    return (
      <section id="case-materials" aria-labelledby="case-materials-title">
        <h2 id="case-materials-title">材料</h2>
        <p role="alert">
          材料信息暂不可用，请刷新重试或联系管理员。
        </p>
        <ul className="document-list">
          {caseDetail.documents.map((document) => (
            <li key={document.id} data-document-id={document.id}>
              <strong>{document.filename}</strong>
              <p className="meta">{roleLabel(document.role)}，等待材料版本信息</p>
            </li>
          ))}
        </ul>
      </section>
    );
  }
  const materialSelection = selection;
  const hasSelectionCandidates = materialSelection.candidate_document_ids !== undefined;

  const documentById = new Map(caseDetail.documents.map((document) => [document.id, document]));
  const pendingLegacyPrimaryIds = new Set(
    materialSelection.status === "pending_confirmation"
      ? materials.filter((material) => material.purpose === "primary_contract" && material.legacy_selected).map((material) => material.material_id)
      : [],
  );
  const targetMaterials = materials.filter(
    (material) => material.role === role && material.purpose === fixedPurpose(role, attachmentPurpose) &&
      (material.purpose !== "primary_contract" || material.material_id === materialSelection.primary_material_id || pendingLegacyPrimaryIds.has(material.material_id)),
  );
  const targetOptions = targetMaterials.map((material) => {
    const document = documentById.get(material.current_document_id);
    return {
      value: material.material_id,
      label: `${document?.filename ?? "所选材料"}，v${material.current_version}，${legacyStatusLabel(material)}`,
    };
  });
  const effectiveTargetMaterialId = targetMaterialId || (
    intent === "replace" && targetMaterials.length === 1
      ? targetMaterials[0]?.material_id ?? ""
      : ""
  );
  const primaryMaterials = materials.filter((material) => material.purpose === "primary_contract");
  const primaryExists = primaryMaterials.length > 0;
  const canReplace = intent === "replace" && targetMaterials.length > 0 && effectiveTargetMaterialId !== "";
  const selectedIds = new Set(
    selection.status === "pending_confirmation" ? [] : selection.document_ids,
  );

  async function onUpload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!file || (intent === "replace" && !canReplace) || hashingFile) return;
    if (file.size > 32 * 1024 * 1024) {
      setUploadPreparationError("单个文件不能超过 32 MiB，请选择较小的文件。");
      return;
    }
    setHashingFile(true);
    setUploadPreparationError(null);
    try {
      const target = targetMaterials.find((material) => material.material_id === effectiveTargetMaterialId);
      const purpose = fixedPurpose(role, attachmentPurpose);
      const contentDigest = await fileSha256(file);
      const signature = JSON.stringify({ actorId, file: { name: file.name, size: file.size, lastModified: file.lastModified, type: file.type, contentDigest }, role, purpose, intent, material_id: target?.material_id, document_id: target?.current_document_id });
      const fingerprint = await idempotencyFingerprint(signature);
      const storageKey = `caw:upload-idempotency:${caseDetail.id}:${actorId}:${fingerprint}`;
      const retry = uploadRetry.current;
      const idempotencyKey = retry?.file === file && retry.signature === signature
        ? retry.key
        : completedUploadKeys.current.has(storageKey)
          ? newIdempotencyKey()
          : sessionStorage.getItem(storageKey) ?? newIdempotencyKey();
      uploadRetry.current = { file, signature, key: idempotencyKey };
      sessionStorage.setItem(storageKey, idempotencyKey);
      upload.mutate(
        {
          file,
          role,
          intent,
          ...(role === "attachment" ? { purpose } : {}),
          ...(intent === "replace" && target
            ? { material_id: target.material_id, replaces_document_id: target.current_document_id }
            : {}),
          idempotency_key: idempotencyKey,
        },
        {
          onSuccess: () => {
            setFile(null);
            uploadRetry.current = null;
            completedUploadKeys.current.add(storageKey);
            try {
              sessionStorage.removeItem(storageKey);
            } catch {
              try {
                sessionStorage.setItem(storageKey, newIdempotencyKey());
              } catch {
                setUploadPreparationError("材料已登记，但浏览器无法清理安全重试记录。请不要重复提交同一文件；如需继续，请刷新后重试或联系管理员。");
              }
            }
          },
        },
      );
    } catch {
      setUploadPreparationError("暂时无法准备安全重试标识，请重试；如果问题仍在，请联系管理员。");
    } finally {
      setHashingFile(false);
    }
  }

  function onConfirmSelection() {
    if (materialSelection.status !== "pending_confirmation") return;
    const documentIds = [...legacyDocumentIds].sort();
    confirmSelection.mutate(
      { version: materialSelection.version, document_ids: documentIds },
      {
        onSuccess: () => {
          setSelectionNote("材料集合已由你确认。新的审查会按此集合冻结输入，历史审查保持不变。");
        },
      },
    );
  }

  return (
    <section id="case-materials" aria-labelledby="case-materials-title">
      <h2 id="case-materials-title">材料</h2>
      {!canWrite ? <p>当前角色只能查看材料，不能上传或重新解析。</p> : null}
      {selection.status === "pending_confirmation" ? (
        <div className="callout" aria-labelledby="legacy-material-selection-title">
          <h3 id="legacy-material-selection-title">确认旧材料集合</h3>
          <p>旧材料不会按角色自动合并或标成同一版本。勾选要用于后续审查的材料，可包含同用途的多份材料；确认只影响后续新审查。</p>
          {!hasSelectionCandidates ? <p role="alert">材料信息暂不可用，请刷新重试或联系管理员。</p> : null}
          <ul className="material-selection-list">
            {materials.map((material) => {
              const document = documentById.get(material.current_document_id);
              return (
                <li key={material.material_id} data-material-id={material.material_id} data-document-id={material.current_document_id}>
                  <Checkbox
                    checked={legacyDocumentIds.includes(material.current_document_id)}
                    disabled={!canWrite || !hasSelectionCandidates || confirmSelection.isPending}
                    onChange={(event) => setLegacyDocumentIds((current) =>
                      event.target.checked
                        ? [...new Set([...current, material.current_document_id])]
                        : current.filter((id) => id !== material.current_document_id),
                    )}
                  >
                    {document?.filename ?? "材料文件名暂不可用"}，{purposeLabels[material.purpose]}，{legacyStatusLabel(material)}
                  </Checkbox>
                </li>
              );
            })}
          </ul>
          {confirmSelection.isError ? <p role="alert">{errorMessage(confirmSelection.error)}</p> : null}
          {selectionNote ? <p role="status">{selectionNote}</p> : null}
          <Button
            type="primary"
            htmlType="button"
            disabled={!canWrite || !hasSelectionCandidates || legacyDocumentIds.length === 0 || confirmSelection.isPending}
            onClick={onConfirmSelection}
          >
            {confirmSelection.isPending ? "正在确认集合" : "确认本次材料集合"}
          </Button>
        </div>
      ) : null}
      <div className="material-current-set" aria-label="本次材料集合">
        <h3>本次材料集合</h3>
        {materials.length === 0 ? <p>当前没有材料。</p> : null}
        <ul className="document-list">
          {materials.map((material) => {
            const document = documentById.get(material.current_document_id);
            return (
              <li key={material.material_id} data-material-id={material.material_id}>
                <div className="document-main">
                  <strong>{document?.filename ?? "材料文件名暂不可用"}</strong>
                  <p className="meta">
                    {roleLabel(material.role)} · {purposeLabels[material.purpose]} · 当前版本 v{material.current_version}
                  </p>
                  <p className="meta">{legacyStatusLabel(material)}；{selectedIds.has(material.current_document_id) ? "已选入本次审查" : selection.status === "pending_confirmation" ? "尚未确认选中集合" : "未选入本次审查"}</p>
                  {document ? <ParsedDocumentSummary document={document} /> : <p className="meta">当前材料元数据尚未返回，请刷新重试或联系管理员。</p>}
                  {document?.parse_status === "failed" && document.parse_error ? <p>{document.parse_error}</p> : null}
                </div>
                {document ? (
                  <div className="document-actions">
                    <StatusText tone={parseTone(document.parse_status)}>{parseLabel(document.parse_status)}</StatusText>
                    <Button
                      type="text"
                      htmlType="button"
                      aria-label={`重新解析 ${document.filename}`}
                      disabled={!canWrite || reparse.isPending}
                      onClick={() => reparse.mutate(document.id)}
                    >
                      {reparse.isPending && reparse.variables === document.id ? "正在重新解析" : "重新解析"}
                    </Button>
                  </div>
                ) : null}
              </li>
            );
          })}
        </ul>
      </div>
      <div className="material-history" aria-label="材料历史版本">
        <h3>材料版本历史</h3>
        {materials.map((material) => {
          const history = versions
            .filter((version) => version.material_id === material.material_id)
            .sort((left, right) => left.version - right.version);
          return (
            <section className="material-history-chain" key={material.material_id} data-material-id={material.material_id} aria-label={`${purposeLabels[material.purpose]}版本历史`}>
              <h4>{documentById.get(material.current_document_id)?.filename ?? "材料文件名暂不可用"} · {purposeLabels[material.purpose]}版本历史</h4>
              {history.length === 0 ? <p>服务尚未返回此材料的版本历史。</p> : null}
              <ol>
                {history.map((version) => {
                  const document = documentById.get(version.document_id);
                  const current = version.document_id === material.current_document_id;
                  return (
                    <li key={`${version.material_id}:${version.version}`} data-document-id={version.document_id}>
                      <span>v{version.version} · {document?.filename ?? version.document_id} · {current ? "当前版本" : "同一材料的历史版本"}</span>
                      {version.previous_document_id ? (() => {
                        const previousVersion = history.find((item) => item.document_id === version.previous_document_id);
                        const previousDocument = documentById.get(version.previous_document_id);
                        return <span className="meta">，替换自“{previousDocument?.filename ?? "上一版材料"}”第 {previousVersion?.version ?? Math.max(1, version.version - 1)} 版</span>;
                      })() : null}
                      <time dateTime={version.created_at}>，{formatDateTime(version.created_at)}</time>
                    </li>
                  );
                })}
              </ol>
            </section>
          );
        })}
      </div>
      <form className="upload-form" onSubmit={onUpload}>
        <h3>添加或替换材料</h3>
        <p className="meta">新增为默认操作。替换仅更新所选材料的当前版本；其他同用途材料和历史记录会保留。单文件不超过 32 MiB。</p>
        <label className="field">
          材料角色
          <Select
            aria-label="材料角色"
            value={role}
            onChange={(value: MaterialProjection["role"]) => {
              setRole(value);
              setTargetMaterialId("");
            }}
            disabled={!canWrite || upload.isPending || hashingFile}
            options={roles}
          />
        </label>
        {role === "attachment" ? (
          <label className="field">
            附件用途
            <Select
              aria-label="附件用途"
              value={attachmentPurpose}
              onChange={(value: MaterialPurpose) => {
                setAttachmentPurpose(value);
                setTargetMaterialId("");
              }}
              disabled={!canWrite || upload.isPending || hashingFile}
              options={attachmentPurposes}
            />
          </label>
        ) : null}
        <label className="field">
          操作
          <Select
            aria-label="材料操作"
            value={intent}
            onChange={(value: "add" | "replace") => {
              setIntent(value);
              setTargetMaterialId(value === "replace" ? targetMaterials[0]?.material_id ?? "" : "");
            }}
            disabled={!canWrite || upload.isPending || hashingFile}
            options={[
              { value: "add", label: "新增材料" },
              { value: "replace", label: "替换选中的材料" },
            ]}
          />
        </label>
        {intent === "replace" ? (
          targetOptions.length === 0 ? <p role="alert">当前角色和用途下没有可替换的材料。</p> : (
            <label className="field">
              要替换的材料
              <Select
                aria-label="要替换的材料"
                value={effectiveTargetMaterialId || undefined}
                onChange={setTargetMaterialId}
                disabled={!canWrite || upload.isPending || hashingFile}
                options={targetOptions}
              />
              {(() => {
                const target = targetMaterials.find((item) => item.material_id === effectiveTargetMaterialId);
                const document = target ? documentById.get(target.current_document_id) : undefined;
                return target ? (
                  <span className="meta">
                    将替换“{document?.filename ?? "所选材料"}”（{purposeLabels[target.purpose]}）当前第 {target.current_version} 版；若版本已更新，请刷新重试。
                  </span>
                ) : null;
              })()}
            </label>
          )
        ) : null}
        {role === "draft_contract" && primaryExists && intent === "add" ? (
          <p className="callout">此案已有主合同。请先确认要纳入后续审查的主合同材料，再替换该材料；独立主合同请另建案件。</p>
        ) : null}
        <div className="field">
          <label htmlFor="case-original-file">文件</label>
          <Upload
            id="case-original-file"
            onChange={({ fileList }) => {
              const chosen = fileList[0]?.originFileObj;
              setFile(chosen ?? null);
            }}
            accept=".txt,.md,.png,.jpg,.jpeg,.pdf,.docx,text/plain,text/markdown,image/png,image/jpeg,application/pdf"
            disabled={!canWrite || upload.isPending || hashingFile}
            maxCount={1}
            fileList={file ? [{ uid: "selected-material", name: file.name }] : []}
            beforeUpload={(original) => {
              setFile(original);
              return false;
            }}
            onRemove={() => {
              setFile(null);
              return true;
            }}
          >
            <Button disabled={!canWrite || upload.isPending}>选择文件</Button>
          </Upload>
        </div>
        {uploadPreparationError ? <p role="alert">{uploadPreparationError}</p> : null}
        {upload.isError ? (
          <div role="alert">
            <p>{errorMessage(upload.error)}</p>
            {upload.error instanceof ApiError && upload.error.code === "primary_contract_exists" ? (
              <p>可以明确替换已有主合同；若要保留为独立主合同，请返回案件列表新建案件。</p>
            ) : null}
          </div>
        ) : null}
        {upload.data ? (
          <p role="status" data-document-id={upload.data.id} data-material-id={upload.data.material_id} data-version={upload.data.version}>
            {upload.data.replayed ? "此前提交已成功" : "材料已登记"}，当前第 {upload.data.version} 版。
          </p>
        ) : null}
        {reparse.isError ? <p role="alert">{errorMessage(reparse.error)}</p> : null}
        <Button
          aria-label={upload.isPending ? "正在提交材料" : intent === "replace" ? "替换材料" : "新增材料"}
          type="primary"
          htmlType="submit"
          disabled={!canWrite || !file || upload.isPending || hashingFile || (intent === "replace" && !canReplace)}
        >
          {hashingFile ? "正在检查文件" : upload.isPending ? "正在提交材料" : intent === "replace" ? "替换材料" : "新增材料"}
        </Button>
      </form>
    </section>
  );
}
