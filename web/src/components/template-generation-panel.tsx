// File metadata (manifest mirror):
// Purpose: Lets authorized users explicitly choose an exact case template version, generate or replace primary materials through idempotent commands, and inspect immutable generation history.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-14
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import { Button, Input, Select } from "antd";
import { Fragment, useEffect, useRef, useState, type FormEvent, type RefObject } from "react";
import {
  ApiError,
  assertCommandContextCurrent,
  bindIdempotencyCommandContext,
  captureCommandContext,
  errorMessage,
} from "../api/http";
import { idempotencyFingerprint, newIdempotencyKey } from "../api/idempotency";
import { useCreateGeneration, useSession, useTemplates } from "../api/queries";
import type {
  Actor,
  DocumentVersion,
  GenerationRecord,
  MaterialProjection,
  MaterialSelectionProjection,
  Template,
  TemplateField,
} from "../api/types";
import { formatDateTime } from "../domain/review";
import type { UseQueryResult } from "@tanstack/react-query";
import { TechnicalDetail } from "./technical-detail";

function templateControlType(fieldType: string): {
  inputType: string;
  multiline: boolean;
} {
  const normalized = fieldType.toLowerCase();
  if (normalized === "textarea" || normalized === "multiline")
    return { inputType: "text", multiline: true };
  if (["number", "integer", "decimal", "percent", "currency"].includes(normalized))
    return { inputType: "number", multiline: false };
  if (normalized === "date") return { inputType: "date", multiline: false };
  return { inputType: "text", multiline: false };
}

function TemplateFieldControl({
  field,
  value,
  fieldId,
  onChange,
}: {
  field: TemplateField;
  value: string;
  fieldId: string;
  onChange: (value: string) => void;
}) {
  const control = templateControlType(field.type);
  if (control.multiline) {
    return (
      <Input.TextArea
        id={fieldId}
        name={field.key}
        required={field.required}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    );
  }
  return (
    <Input
      id={fieldId}
      name={field.key}
      type={control.inputType}
      step={control.inputType === "number" ? "any" : undefined}
      required={field.required}
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}

function TemplateFieldRow({
  field,
  index,
  caseId,
  value,
  onChange,
}: {
  field: TemplateField;
  index: number;
  caseId: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const fieldId = `template-${caseId}-${field.key}-${index}`;
  return (
    <Fragment key={`${field.key}-${index}`}>
      <label className="field" htmlFor={fieldId}>
        {field.label}
        {field.required ? "（必填）" : ""}
        <TemplateFieldControl field={field} value={value} fieldId={fieldId} onChange={onChange} />
      </label>
      <p className="meta">
        字段：{field.key}，类型：{field.type}
      </p>
    </Fragment>
  );
}

function templateSelectionKey(template: Template): string {
  return JSON.stringify([template.id, template.version, template.digest]);
}

function selectedTemplateOf(data: Template[] | undefined, selectionKey: string): Template | null {
  return data?.find((item) => templateSelectionKey(item) === selectionKey) ?? null;
}

function templateOptions(data: Template[] | undefined) {
  if (data === undefined) return [];
  return data.map((template: Template) => ({
    label: `${template.name}（${template.source === "tenant" ? "租户" : "平台内置"}）`,
    value: templateSelectionKey(template),
  }));
}

function fieldValue(values: Record<string, string>, key: string): string {
  return Object.hasOwn(values, key) ? values[key]! : "";
}

function replaceablePrimaryMaterials(
  materials: MaterialProjection[],
  selection: MaterialSelectionProjection | undefined,
): MaterialProjection[] {
  if (!selection) return [];
  if (selection.primary_material_id) {
    return materials.filter(
      (material) =>
        material.material_id === selection.primary_material_id &&
        material.purpose === "primary_contract",
    );
  }
  if (selection.status === "pending_confirmation") {
    return materials.filter(
      (material) => material.purpose === "primary_contract" && material.legacy_selected,
    );
  }
  return [];
}

function TemplateReadState({
  templates,
  session,
}: {
  templates: UseQueryResult<Template[]>;
  session: UseQueryResult<Actor>;
}) {
  return (
    <>
      {session.isLoading || session.isFetching ? (
        <p role="status">正在确认当前身份后读取模板。</p>
      ) : null}
      {session.isError ? (
        <p className="workflow-error" role="alert">
          当前身份没有确认，无法读取模板。{errorMessage(session.error)}{" "}
          <Button type="text" htmlType="button" onClick={() => void session.refetch()}>
            重试读取身份
          </Button>
        </p>
      ) : null}
      {templates.isLoading ? <p role="status">正在读取可用模板。</p> : null}
      {templates.isError ? (
        <p className="workflow-error" role="alert">
          {errorMessage(templates.error)}{" "}
          <Button
            aria-label={"重试读取模板"}
            type="text"
            htmlType="button"
            onClick={() => void templates.refetch()}
          >
            重试读取模板
          </Button>
        </p>
      ) : null}
      {templates.isSuccess && templates.data.length === 0 ? <p>服务没有返回可用模板。</p> : null}
    </>
  );
}

function TemplateForm({
  caseId,
  template,
  generate,
  values,
  errorRef,
  onSubmit,
  onUpdateField,
  materials,
  documents,
  materialSelection,
  intent,
  targetMaterialId,
  onIntentChange,
  onTargetChange,
  isPreparing,
  preparationError,
}: {
  caseId: string;
  template: Template;
  generate: ReturnType<typeof useCreateGeneration>;
  values: Record<string, string>;
  errorRef: RefObject<HTMLParagraphElement | null>;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void | Promise<void>;
  onUpdateField: (fieldKey: string, value: string) => void;
  materials: MaterialProjection[];
  documents: DocumentVersion[];
  materialSelection: MaterialSelectionProjection | undefined;
  intent: "add" | "replace";
  targetMaterialId: string;
  onIntentChange: (value: "add" | "replace") => void;
  onTargetChange: (value: string) => void;
  isPreparing: boolean;
  preparationError: string | null;
}) {
  const primaryMaterials = replaceablePrimaryMaterials(materials, materialSelection);
  const hasPrimaryMaterial = materials.some((material) => material.purpose === "primary_contract");
  const target = primaryMaterials.find((material) => material.material_id === targetMaterialId);
  const documentById = new Map(documents.map((document) => [document.id, document]));
  const targetOptions = primaryMaterials.map((material) => ({
    value: material.material_id,
    label: `${documentById.get(material.current_document_id)?.filename ?? "主合同材料"}，v${material.current_version}`,
  }));
  return (
    <form className="workflow-form" onSubmit={onSubmit}>
      <div className="meta">
        {template.source === "tenant" ? "租户模板" : "平台内置模板"}：{template.name} · 精确版本{" "}
        <TechnicalDetail value={template.version} label="模板版本标识" /> ·{" "}
        <TechnicalDetail value={template.id} label="模板标识" /> ·{" "}
        <TechnicalDetail value={template.digest} label="模板摘要" />
      </div>
      <label className="field">
        草稿操作
        <Select
          aria-label="草稿操作"
          value={intent}
          onChange={(value: "add" | "replace") => onIntentChange(value)}
          disabled={generate.isPending}
          options={[
            { value: "add", label: "新增主合同草稿" },
            { value: "replace", label: "明确替换已有草稿材料" },
          ]}
        />
      </label>
      {intent === "replace" ? (
        targetOptions.length === 0 ? (
          <p role="alert">当前案件没有可替换的主合同材料。</p>
        ) : (
          <label className="field">
            要替换的主合同材料
            <Select
              aria-label="要替换的主合同材料"
              value={targetMaterialId || undefined}
              onChange={onTargetChange}
              disabled={generate.isPending}
              options={targetOptions}
            />
            {target ? (
              <span className="meta">
                将替换“{documentById.get(target.current_document_id)?.filename ?? "所选主合同材料"}
                ”（主合同）当前第 {target.current_version} 版；若版本已更新，请刷新重试。
              </span>
            ) : null}
          </label>
        )
      ) : null}
      {hasPrimaryMaterial && intent === "add" ? (
        <p className="callout">
          当前案件已有主合同。请先确认要纳入后续审查的主合同材料，再替换该材料；独立主合同请另建案件。
        </p>
      ) : null}
      {template.fields.map((field, index) => (
        <TemplateFieldRow
          key={`${field.key}-${index}`}
          field={field}
          index={index}
          caseId={caseId}
          value={fieldValue(values, field.key)}
          onChange={(value) => onUpdateField(field.key, value)}
        />
      ))}
      {generate.error ? (
        <p ref={errorRef} className="workflow-error" role="alert" tabIndex={-1}>
          {errorMessage(generate.error)}
          {generate.error instanceof ApiError && generate.error.code === "primary_contract_exists"
            ? " 可明确替换已有主合同；独立主合同请另建案件。"
            : ""}
        </p>
      ) : null}
      {preparationError ? <p role="alert">{preparationError}</p> : null}
      {generate.data ? (
        <div
          className="callout"
          role="status"
          data-document-id={generate.data.document_id}
          data-material-id={generate.data.material_id}
          data-version={generate.data.version}
        >
          草稿已生成：{template.name}（模板版本{" "}
          <TechnicalDetail value={generate.data.template_version} label="查看精确模板版本" />
          ），当前第 {generate.data.version ?? "?"} 版。
          {generate.data.template_digest ? (
            <>
              {" "}
              模板摘要{" "}
              <TechnicalDetail value={generate.data.template_digest} label="查看模板摘要" />
            </>
          ) : null}
          摘要 <code className="digest">{generate.data.digest}</code>
        </div>
      ) : null}
      <Button
        aria-label={String(
          isPreparing ? "正在准备请求" : generate.isPending ? "正在生成草稿" : "生成待审草稿",
        )}
        type="primary"
        htmlType="submit"
        disabled={isPreparing || generate.isPending || (intent === "replace" && !target)}
      >
        {isPreparing ? "正在准备请求" : generate.isPending ? "正在生成草稿" : "生成待审草稿"}
      </Button>
    </form>
  );
}

function TemplateGenerationBody({
  caseId,
  actorId,
  templates,
  session,
  materials,
  documents,
  materialSelection,
}: {
  caseId: string;
  actorId: string;
  templates: UseQueryResult<Template[]>;
  session: UseQueryResult<Actor>;
  materials: MaterialProjection[];
  documents: DocumentVersion[];
  materialSelection: MaterialSelectionProjection | undefined;
}) {
  const generate = useCreateGeneration(caseId);
  const [selectedTemplateId, setSelectedTemplateId] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [intent, setIntent] = useState<"add" | "replace">("add");
  const [targetMaterialId, setTargetMaterialId] = useState("");
  const [isPreparing, setIsPreparing] = useState(false);
  const [preparationError, setPreparationError] = useState<string | null>(null);
  const errorRef = useRef<HTMLParagraphElement>(null);
  const availableTemplates = templates.isError ? undefined : templates.data;
  const selectedTemplate = selectedTemplateOf(availableTemplates, selectedTemplateId);
  const options = templateOptions(availableTemplates);

  useEffect(() => {
    if (generate.error && errorRef.current) errorRef.current.focus();
  }, [generate.error]);

  function selectTemplate(templateId: string) {
    setSelectedTemplateId(templateId);
    setValues({});
  }

  function updateField(fieldKey: string, value: string) {
    setValues((current) => ({ ...current, [fieldKey]: value }));
  }

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedTemplate) return;
    const fields = Object.fromEntries(
      selectedTemplate.fields.map((field) => [field.key, fieldValue(values, field.key)]),
    );
    const target = replaceablePrimaryMaterials(materials, materialSelection).find(
      (material) => material.material_id === targetMaterialId,
    );
    if (intent === "replace" && !target) return;
    const input = {
      template_id: selectedTemplate.id,
      template_version: selectedTemplate.version,
      template_digest: selectedTemplate.digest,
      fields,
      intent,
      ...(intent === "replace" && target
        ? { material_id: target.material_id, replaces_document_id: target.current_document_id }
        : {}),
    } as const;
    const commandContext = captureCommandContext();
    setIsPreparing(true);
    setPreparationError(null);
    try {
      const fingerprint = await idempotencyFingerprint(
        JSON.stringify({ input, sessionIdentity: commandContext.identity }),
      );
      assertCommandContextCurrent(commandContext);
      const storageKey = `caw:generation-idempotency:${caseId}:${actorId}:${commandContext.contextVersion}:${fingerprint}`;
      const idempotencyKey = sessionStorage.getItem(storageKey) ?? newIdempotencyKey();
      sessionStorage.setItem(storageKey, idempotencyKey);
      bindIdempotencyCommandContext(idempotencyKey, commandContext);
      generate.mutate(
        { input, idempotencyKey },
        {
          onSuccess: () => {
            setValues({});
            try {
              sessionStorage.removeItem(storageKey);
            } catch {
              try {
                sessionStorage.setItem(storageKey, newIdempotencyKey());
              } catch {
                setPreparationError(
                  "草稿已生成，但浏览器无法清理安全重试记录。请不要重复提交同一内容；如需继续，请刷新后重试或联系管理员。",
                );
              }
            }
          },
        },
      );
    } catch {
      setPreparationError("暂时无法准备安全重试标识，请重试；如果问题仍在，请联系管理员。");
    } finally {
      setIsPreparing(false);
    }
  }

  return (
    <>
      <TemplateReadState templates={templates} session={session} />
      {templates.isSuccess && !templates.isError ? (
        <label className="field">
          模板来源
          <Select
            aria-label="选择模板"
            value={selectedTemplate ? selectedTemplateId : undefined}
            placeholder="请选择平台内置或当前案件可用的租户模板"
            onChange={selectTemplate}
            options={options}
          />
          <span className="meta">
            租户模板仅在当前案件授权范围内显示。选择后会固定模板版本和摘要。
          </span>
        </label>
      ) : null}
      {selectedTemplateId && !selectedTemplate && templates.isSuccess && !templates.isError ? (
        <p role="status">所选模板版本已不在当前可用列表中，请重新选择一个明确版本。</p>
      ) : null}
      {selectedTemplate ? (
        <TemplateForm
          caseId={caseId}
          template={selectedTemplate}
          generate={generate}
          values={values}
          errorRef={errorRef}
          materials={materials}
          documents={documents}
          materialSelection={materialSelection}
          intent={intent}
          targetMaterialId={targetMaterialId}
          onIntentChange={(value) => {
            setIntent(value);
            const candidates = replaceablePrimaryMaterials(materials, materialSelection);
            setTargetMaterialId(
              value === "replace" && candidates.length === 1 ? candidates[0]!.material_id : "",
            );
          }}
          onTargetChange={setTargetMaterialId}
          isPreparing={isPreparing}
          preparationError={preparationError}
          onSubmit={onSubmit}
          onUpdateField={updateField}
        />
      ) : null}
    </>
  );
}

function GenerationHistory({ records }: { records: GenerationRecord[] }) {
  return (
    <section aria-labelledby="generation-history-title">
      <h3 id="generation-history-title">生成历史</h3>
      {records.length === 0 ? (
        <p>没有已返回的模板生成记录。</p>
      ) : (
        <ul className="workflow-record-list">
          {records.map((record) => (
            <li key={record.id}>
              <strong>模板生成记录</strong>
              <dl className="execution-dl">
                <div>
                  <dt>精确模板版本</dt>
                  <dd>
                    <TechnicalDetail value={record.template_version} label="查看模板版本" />
                  </dd>
                </div>
                <div>
                  <dt>模板标识</dt>
                  <dd>
                    <TechnicalDetail value={record.template_id} label="查看模板标识" />
                  </dd>
                </div>
                <div>
                  <dt>生成记录</dt>
                  <dd>
                    <TechnicalDetail value={record.id} label="查看编号" />
                  </dd>
                </div>
                <div>
                  <dt>草稿文档</dt>
                  <dd>
                    <TechnicalDetail value={record.document_id} label="查看标识" />
                  </dd>
                </div>
                {record.material_id ? (
                  <div>
                    <dt>材料版本</dt>
                    <dd>
                      v{record.version ?? "?"} ·{" "}
                      <TechnicalDetail value={record.material_id} label="材料标识" />
                    </dd>
                  </div>
                ) : null}
                <div>
                  <dt>模板内容摘要</dt>
                  <dd>
                    {record.template_digest ? (
                      <TechnicalDetail value={record.template_digest} label="查看模板摘要" />
                    ) : (
                      "历史记录未返回模板摘要"
                    )}
                  </dd>
                </div>
                <div>
                  <dt>摘要</dt>
                  <dd>
                    <TechnicalDetail value={record.digest} label="查看摘要" />
                  </dd>
                </div>
                <div>
                  <dt>生成时间</dt>
                  <dd>{formatDateTime(record.created_at)}</dd>
                </div>
              </dl>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export function TemplateGenerationPanel({
  caseId,
  records,
  canWrite,
  archived,
  materials,
  documents,
  materialSelection,
  actorId,
}: {
  caseId: string;
  records: GenerationRecord[];
  canWrite: boolean;
  archived: boolean;
  materials?: MaterialProjection[];
  documents?: DocumentVersion[];
  materialSelection?: MaterialSelectionProjection;
  actorId: string;
}) {
  const session = useSession();
  const currentActor =
    session.isSuccess && !session.isFetching && !session.isError ? session.data : null;
  const templates = useTemplates(caseId, currentActor, canWrite && !archived);
  return (
    <section className="workflow-panel" aria-labelledby="template-generation-title">
      <header className="workflow-panel-head">
        <div>
          <h2 id="template-generation-title">模板生成</h2>
          <p className="meta">从已发布模板生成草稿；替换材料时保留原版本。</p>
        </div>
      </header>
      {!canWrite ? <p className="callout">当前角色没有已定义的模板写入权限。</p> : null}
      {archived ? (
        <p className="callout">案件已归档，模板生成写入会被服务端拒绝（HTTP 409）。</p>
      ) : null}
      {canWrite && !archived ? (
        materials && documents ? (
          <TemplateGenerationBody
            caseId={caseId}
            actorId={actorId}
            templates={templates}
            session={session}
            materials={materials}
            documents={documents}
            materialSelection={materialSelection}
          />
        ) : (
          <p role="alert">材料信息暂不可用，请刷新重试或联系管理员。</p>
        )
      ) : null}
      <GenerationHistory records={records} />
    </section>
  );
}
