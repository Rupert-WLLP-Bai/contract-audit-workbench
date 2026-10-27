import { Button, Input, Select } from "antd";
import { Fragment, useEffect, useRef, useState, type FormEvent } from "react";
import { errorMessage } from "../api/http";
import { useCreateGeneration, useTemplates } from "../api/queries";
import type { GenerationRecord, Template } from "../api/types";
import { formatDateTime } from "../domain/review";

function templateControlType(fieldType: string): {
  inputType: string;
  multiline: boolean;
} {
  const normalized = fieldType.toLowerCase();
  if (normalized === "textarea" || normalized === "multiline")
    return { inputType: "text", multiline: true };
  if (
    ["number", "integer", "decimal", "percent", "currency"].includes(normalized)
  )
    return { inputType: "number", multiline: false };
  if (normalized === "date") return { inputType: "date", multiline: false };
  return { inputType: "text", multiline: false };
}

export function TemplateGenerationPanel({
  caseId,
  records,
  canWrite,
  archived,
}: {
  caseId: string;
  records: GenerationRecord[];
  canWrite: boolean;
  archived: boolean;
}) {
  const templates = useTemplates(canWrite && !archived);
  const generate = useCreateGeneration(caseId);
  const [selectedTemplateId, setSelectedTemplateId] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const errorRef = useRef<HTMLParagraphElement>(null);
  const selectedTemplate =
    templates.data?.find((item) => item.id === selectedTemplateId) ??
    templates.data?.[0] ??
    null;

  useEffect(() => {
    if (generate.error) errorRef.current?.focus();
  }, [generate.error]);

  function selectTemplate(templateId: string) {
    setSelectedTemplateId(templateId);
    setValues({});
  }

  function updateField(fieldKey: string, value: string) {
    setValues((current) => ({ ...current, [fieldKey]: value }));
  }

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedTemplate) return;
    const fields = Object.fromEntries(
      selectedTemplate.fields.map((field) => [
        field.key,
        values[field.key] ?? "",
      ]),
    );
    generate.mutate(
      { template_id: selectedTemplate.id, fields },
      {
        onSuccess: () => setValues({}),
      },
    );
  }

  return (
    <section
      className="workflow-panel"
      aria-labelledby="template-generation-title"
    >
      <header className="workflow-panel-head">
        <div>
          <h2 id="template-generation-title">模板生成</h2>
          <p className="meta">
            模板身份、版本和摘要由服务端返回；生成记录会关联真实草稿文档。
          </p>
        </div>
      </header>
      {!canWrite ? (
        <p className="callout">当前角色没有已定义的模板写入权限。</p>
      ) : null}
      {archived ? (
        <p className="callout">
          案件已归档，模板生成写入会被服务端拒绝（HTTP 409）。
        </p>
      ) : null}
      {canWrite && !archived && templates.isLoading ? (
        <p role="status">正在读取可用模板。</p>
      ) : null}
      {canWrite && !archived && templates.isError ? (
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
      {canWrite &&
      !archived &&
      templates.isSuccess &&
      templates.data.length === 0 ? (
        <p>服务没有返回可用模板。</p>
      ) : null}
      {canWrite && !archived && selectedTemplate ? (
        <form className="workflow-form" onSubmit={onSubmit}>
          <label className="field">
            模板
            <Select
              aria-label="模板"
              value={selectedTemplate.id}
              onChange={selectTemplate}
              options={
                templates.data?.map((template: Template) => ({
                  label: `${template.name}，${template.version}`,
                  value: template.id,
                })) ?? []
              }
            />
          </label>
          <p className="meta">
            模板 ID：{selectedTemplate.id}，版本：{selectedTemplate.version}，
            摘要：<code className="digest">{selectedTemplate.digest}</code>
          </p>
          {selectedTemplate.fields.map((field, index) => {
            const control = templateControlType(field.type);
            const fieldId = `template-${caseId}-${field.key}-${index}`;
            return (
              <Fragment key={`${field.key}-${index}`}>
                <label className="field" htmlFor={fieldId}>
                  {field.label}
                  {field.required ? "（必填）" : ""}
                  {control.multiline ? (
                    <Input.TextArea
                      id={fieldId}
                      name={field.key}
                      required={field.required}
                      value={values[field.key] ?? ""}
                      onChange={(event) =>
                        updateField(field.key, event.target.value)
                      }
                    />
                  ) : (
                    <Input
                      id={fieldId}
                      name={field.key}
                      type={control.inputType}
                      step={control.inputType === "number" ? "any" : undefined}
                      required={field.required}
                      value={values[field.key] ?? ""}
                      onChange={(event) =>
                        updateField(field.key, event.target.value)
                      }
                    />
                  )}
                </label>
                <p className="meta">
                  字段：{field.key}，类型：{field.type}
                </p>
              </Fragment>
            );
          })}
          {generate.error ? (
            <p
              ref={errorRef}
              className="workflow-error"
              role="alert"
              tabIndex={-1}
            >
              {errorMessage(generate.error)}
            </p>
          ) : null}
          {generate.data ? (
            <p role="status">
              草稿已生成：文档 {generate.data.document_id}，模板{" "}
              {generate.data.template_id} / {generate.data.template_version}，
              摘要 <code className="digest">{generate.data.digest}</code>
            </p>
          ) : null}
          <Button
            aria-label={String(
              generate.isPending ? "正在生成草稿" : "生成待审草稿",
            )}
            type="primary"
            htmlType="submit"
            disabled={generate.isPending}
          >
            {generate.isPending ? "正在生成草稿" : "生成待审草稿"}
          </Button>
        </form>
      ) : null}
      <section aria-labelledby="generation-history-title">
        <h3 id="generation-history-title">生成历史</h3>
        {records.length === 0 ? (
          <p>没有已返回的模板生成记录。</p>
        ) : (
          <ul className="workflow-record-list">
            {records.map((record) => (
              <li key={record.id}>
                <strong>
                  {record.template_id}，{record.template_version}
                </strong>
                <dl className="execution-dl">
                  <div>
                    <dt>生成记录</dt>
                    <dd>{record.id}</dd>
                  </div>
                  <div>
                    <dt>草稿文档</dt>
                    <dd>{record.document_id}</dd>
                  </div>
                  <div>
                    <dt>摘要</dt>
                    <dd>
                      <code className="digest">{record.digest}</code>
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
    </section>
  );
}
