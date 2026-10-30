import { Button, Input, Select } from "antd";
import {
  Fragment,
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type RefObject,
} from "react";
import { errorMessage } from "../api/http";
import { useCreateGeneration, useTemplates } from "../api/queries";
import type {
  GenerationRecord,
  Template,
  TemplateField,
} from "../api/types";
import { formatDateTime } from "../domain/review";
import type { UseQueryResult } from "@tanstack/react-query";

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
        <TemplateFieldControl
          field={field}
          value={value}
          fieldId={fieldId}
          onChange={onChange}
        />
      </label>
      <p className="meta">
        字段：{field.key}，类型：{field.type}
      </p>
    </Fragment>
  );
}

function selectedTemplateOf(
  data: Template[] | undefined,
  selectedTemplateId: string,
): Template | null {
  return (
    data?.find((item) => item.id === selectedTemplateId) ?? data?.[0] ?? null
  );
}

function templateOptions(data: Template[] | undefined) {
  if (data === undefined) return [];
  return data.map((template: Template) => ({
    label: `${template.name}，${template.version}`,
    value: template.id,
  }));
}

function fieldValue(values: Record<string, string>, key: string): string {
  return Object.hasOwn(values, key) ? values[key]! : "";
}

function TemplateReadState({
  templates,
}: {
  templates: UseQueryResult<Template[]>;
}) {
  return (
    <>
      {templates.isLoading ? (
        <p role="status">正在读取可用模板。</p>
      ) : null}
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
      {templates.isSuccess && templates.data.length === 0 ? (
        <p>服务没有返回可用模板。</p>
      ) : null}
    </>
  );
}

function TemplateForm({
  caseId,
  template,
  options,
  generate,
  values,
  errorRef,
  onSubmit,
  onSelectTemplate,
  onUpdateField,
}: {
  caseId: string;
  template: Template;
  options: { label: string; value: string }[];
  generate: ReturnType<typeof useCreateGeneration>;
  values: Record<string, string>;
  errorRef: RefObject<HTMLParagraphElement | null>;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onSelectTemplate: (templateId: string) => void;
  onUpdateField: (fieldKey: string, value: string) => void;
}) {
  return (
    <form className="workflow-form" onSubmit={onSubmit}>
      <label className="field">
        模板
        <Select
          aria-label="模板"
          value={template.id}
          onChange={onSelectTemplate}
          options={options}
        />
      </label>
      <p className="meta">
        模板 ID：{template.id}，版本：{template.version}，
        摘要：<code className="digest">{template.digest}</code>
      </p>
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
  );
}

function TemplateGenerationBody({
  caseId,
  templates,
}: {
  caseId: string;
  templates: UseQueryResult<Template[]>;
}) {
  const generate = useCreateGeneration(caseId);
  const [selectedTemplateId, setSelectedTemplateId] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const errorRef = useRef<HTMLParagraphElement>(null);
  const selectedTemplate = selectedTemplateOf(
    templates.data,
    selectedTemplateId,
  );

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

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedTemplate) return;
    const fields = Object.fromEntries(
      selectedTemplate.fields.map((field) => [
        field.key,
        fieldValue(values, field.key),
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
    <>
      <TemplateReadState templates={templates} />
      {selectedTemplate ? (
        <TemplateForm
          caseId={caseId}
          template={selectedTemplate}
          options={templateOptions(templates.data)}
          generate={generate}
          values={values}
          errorRef={errorRef}
          onSubmit={onSubmit}
          onSelectTemplate={selectTemplate}
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
  );
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
      {canWrite && !archived ? (
        <TemplateGenerationBody caseId={caseId} templates={templates} />
      ) : null}
      <GenerationHistory records={records} />
    </section>
  );
}
