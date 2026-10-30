import { Button, Input, Select } from "antd";
import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { ApiError, errorMessage, statusLabel } from "../api/http";
import { listGovernedKnowledge, type KnowledgeCondition, type KnowledgeContent, type KnowledgeKind, type KnowledgeRef, type KnowledgeSpan, type KnowledgeSummary } from "../api/governed-knowledge";
import { useRulePack, useRulePacks } from "../api/queries";
import type { RulePackSummary } from "../api/rulepacks";
import { toLocalInputValue, toUtcInstant } from "./local-datetime";

export interface KnowledgeFormIssue { id: string; message: string }
const kinds: { value: KnowledgeKind; label: string }[] = [
  { value: "policy", label: "政策制度" }, { value: "guidance", label: "操作指引" },
  { value: "exception", label: "例外" }, { value: "experience", label: "审查经验" },
];
const conditionFields = [
  { value: "org_id", label: "组织 ID" }, { value: "case_id", label: "案件 ID" },
  { value: "business_line", label: "业务线" }, { value: "scenario", label: "业务场景" },
  { value: "contract_type", label: "合同类型" }, { value: "business_status", label: "业务状态" },
];

export function emptyKnowledgeContent(): KnowledgeContent {
  return {
    knowledge_id: "", version: "", kind: "policy", title: "", body: "", summary: "",
    source_type: "reference", source_reference: "", scope: { business_line: "", scenario: "", contract_type: "" },
    valid_from: "", valid_until: "", applicability: [], exclusions: [],
    binding: { pack_id: "", pack_version: "", pack_content_hash: "", pack_snapshot_digest: "", compiler_ref: "governed-rulepack/v1", rule_ids: [], source_spans: [], candidate_diff: "", validation_evidence: "" },
  };
}

function lines(value: string): string[] {
  return value.split("\n");
}
function textOrEmpty(value: string | undefined): string {
  return value ?? "";
}
function linesOrEmpty(value: string[] | undefined): string {
  return (value ?? []).join("\n");
}
function lookupError(error: unknown): string {
  if (!(error instanceof ApiError)) return errorMessage(error);
  const status = statusLabel(error.status);
  const details = error.details == null ? "" : `；${typeof error.details === "string" ? error.details : JSON.stringify(error.details)}`;
  return `${error.message}（${error.code}；${status}）${details}`;
}
function Field({ id, label, issues, children }: { id: string; label: string; issues: KnowledgeFormIssue[]; children: ReactNode }) {
  const issue = issues.find((entry) => entry.id === id);
  return <div className="field"><label htmlFor={id}>{label}</label>{children}{issue && <span id={`${id}-error`} className="workflow-error">{issue.message}</span>}</div>;
}
function errorProps(id: string, issues: KnowledgeFormIssue[]) {
  return issues.some((issue) => issue.id === id) ? { "aria-invalid": true as const, "aria-describedby": `${id}-error` } : {};
}

function requiredIssue(issues: KnowledgeFormIssue[], id: string, label: string, text: string | undefined): void {
  if (!text?.trim()) issues.push({ id, message: `请填写${label}` });
}

function validateRequiredFields(issues: KnowledgeFormIssue[], value: KnowledgeContent): void {
  requiredIssue(issues, "knowledge-id", "知识 ID", value.knowledge_id);
  requiredIssue(issues, "knowledge-version", "新版本号", value.version);
  requiredIssue(issues, "knowledge-title", "标题", value.title);
  requiredIssue(issues, "knowledge-summary", "摘要", value.summary);
  requiredIssue(issues, "knowledge-body", "正文", value.body);
  requiredIssue(issues, "knowledge-source-reference", "来源说明", value.source_reference);
  requiredIssue(issues, "knowledge-line", "业务线", value.scope.business_line);
  requiredIssue(issues, "knowledge-scenario", "业务场景", value.scope.scenario);
  requiredIssue(issues, "knowledge-contract", "合同类型", value.scope.contract_type);
  requiredIssue(issues, "knowledge-from", "生效时间", value.valid_from);
  requiredIssue(issues, "knowledge-until", "失效时间", value.valid_until);
}

function validateWindow(issues: KnowledgeFormIssue[], value: KnowledgeContent): void {
  if (value.valid_from && value.valid_until && Date.parse(value.valid_until) <= Date.parse(value.valid_from))
    issues.push({ id: "knowledge-until", message: "失效时间必须晚于生效时间" });
}

function validateScope(issues: KnowledgeFormIssue[], value: KnowledgeContent): void {
  if (value.scope.case_id && !value.scope.org_id) issues.push({ id: "knowledge-org", message: "限定案件时需要填写所属组织 ID" });
  if (value.source_case_id && value.kind !== "experience" && value.scope.case_id !== value.source_case_id)
    issues.push({ id: "knowledge-source-case", message: "案件来源的非经验内容必须限定同一个案件" });
}

function validateBinding(issues: KnowledgeFormIssue[], value: KnowledgeContent): void {
  if (!value.binding.pack_id) issues.push({ id: "knowledge-pack", message: "请选择已发布的活动规则包" });
  requiredIssue(issues, "knowledge-diff", "候选规则差异", value.binding.candidate_diff);
  requiredIssue(issues, "knowledge-evidence", "验证依据", value.binding.validation_evidence);
  if (!value.binding.source_spans?.length) issues.push({ id: "knowledge-spans", message: "至少添加一条规则及对应正文引用" });
}

function validateSpans(issues: KnowledgeFormIssue[], value: KnowledgeContent): void {
  (value.binding.source_spans ?? []).forEach((span, index) => {
    if (!span.rule_id) issues.push({ id: `span-rule-${index}`, message: "请选择规则" });
    if (!span.quote || !Number.isInteger(span.start) || span.start < 0 || Array.from(value.body).slice(span.start, span.end).join("") !== span.quote)
      issues.push({ id: `span-quote-${index}`, message: "引用文字和起始位置必须与正文一致" });
  });
}

function validateConditions(issues: KnowledgeFormIssue[], value: KnowledgeContent): void {
  for (const group of ["applicability", "exclusions"] as const) {
    (value[group] ?? []).forEach((item, index) => {
      if (!item.equals.trim()) issues.push({ id: `${group}-value-${index}`, message: "请填写条件值" });
    });
  }
}

function validateException(issues: KnowledgeFormIssue[], value: KnowledgeContent): void {
  if (value.kind !== "exception") return;
  requiredIssue(issues, "knowledge-exception", "例外批准依据", value.exception_basis);
  requiredIssue(issues, "knowledge-org", "限定组织 ID", value.scope.org_id);
}

function validateExperience(issues: KnowledgeFormIssue[], value: KnowledgeContent): void {
  if (value.kind !== "experience") return;
  requiredIssue(issues, "knowledge-source-case", "来源案件 ID", value.source_case_id);
  requiredIssue(issues, "knowledge-candidate", "候选 ID", value.candidate_id);
  requiredIssue(issues, "knowledge-candidate-version", "候选版本 ID", value.candidate_version_id);
  requiredIssue(issues, "knowledge-candidate-hash", "候选内容摘要", value.candidate_content_sha256);
  requiredIssue(issues, "knowledge-review-diff", "候选审阅差异", value.candidate_review_diff);
  if (!value.reviewed_applicability?.some((item) => item.trim())) issues.push({ id: "knowledge-reviewed-applicability", message: "请填写审阅后的适用条件" });
  if (!value.reviewed_exclusions?.some((item) => item.trim())) issues.push({ id: "knowledge-reviewed-exclusions", message: "请填写审阅后的排除条件" });
  requiredIssue(issues, "knowledge-check-action", "检查动作", value.check_action);
  requiredIssue(issues, "knowledge-minimum-evidence", "最低证据", value.minimum_evidence);
  if (!value.counterexamples?.some((item) => item.trim())) issues.push({ id: "knowledge-counterexamples", message: "请填写反例" });
}

function validatePolicyRefs(issues: KnowledgeFormIssue[], value: KnowledgeContent): void {
  if ((value.kind === "experience" || value.kind === "exception") && !value.policy_refs?.length)
    issues.push({ id: "knowledge-policy-refs", message: "请添加政策依赖" });
  (value.policy_refs ?? []).forEach((item, index) => {
    if (!item.knowledge_id || !item.version_id || !item.content_sha256)
      issues.push({ id: `policy-ref-${index}`, message: "请完整填写政策 ID、精确版本 ID 和内容摘要" });
  });
}

export function validateKnowledgeDraft(value: KnowledgeContent): KnowledgeFormIssue[] {
  const issues: KnowledgeFormIssue[] = [];
  validateRequiredFields(issues, value);
  validateWindow(issues, value);
  validateScope(issues, value);
  validateBinding(issues, value);
  validateSpans(issues, value);
  validateConditions(issues, value);
  validateException(issues, value);
  validateExperience(issues, value);
  validatePolicyRefs(issues, value);
  return issues;
}

function withField<K extends keyof KnowledgeContent>(
  value: KnowledgeContent,
  key: K,
  next: KnowledgeContent[K],
): KnowledgeContent {
  return { ...value, [key]: next };
}

function withSection<K extends "scope" | "binding">(
  value: KnowledgeContent,
  key: K,
  patch: Partial<KnowledgeContent[K]>,
): KnowledgeContent {
  return { ...value, [key]: { ...value[key], ...patch } };
}

function withSpans(value: KnowledgeContent, spans: KnowledgeSpan[]): KnowledgeContent {
  return withSection(value, "binding", {
    source_spans: spans,
    rule_ids: [...new Set(spans.map((span) => span.rule_id).filter(Boolean))],
  });
}

function replaceAt<T>(items: readonly T[], index: number, patch: Partial<T>): T[] {
  return items.map((item, i) => (i === index ? { ...item, ...patch } : item));
}

function removeAt<T>(items: readonly T[], index: number): T[] {
  return items.filter((_, i) => i !== index);
}

function publishedPacks(items: RulePackSummary[]): RulePackSummary[] {
  return items.filter((pack) => pack.status === "published" && pack.is_active);
}

function packOptions(
  activePacks: RulePackSummary[],
  binding: KnowledgeContent["binding"],
): { currentKey: string | undefined; options: { value: string; label: string }[] } {
  const options = activePacks.map((pack) => ({ value: JSON.stringify([pack.id, pack.version]), label: `${pack.id} · ${pack.version}` }));
  const currentKey = binding.pack_id ? JSON.stringify([binding.pack_id, binding.pack_version]) : undefined;
  if (currentKey && !options.some((option) => option.value === currentKey))
    options.push({ value: currentKey, label: `${binding.pack_id} · ${binding.pack_version}（原绑定）` });
  return { currentKey, options };
}

function ruleOptions(
  rules: { rule_id: string; capability_id: string; business_scope: { business_line: string; scenario: string; contract_type: string } }[] | undefined,
  spans: KnowledgeSpan[] | null | undefined,
): { value: string; label: string }[] {
  const options = (rules ?? []).map((rule) => ({ value: rule.rule_id, label: `${rule.rule_id} · ${rule.capability_id} · ${rule.business_scope.business_line} / ${rule.business_scope.scenario} / ${rule.business_scope.contract_type}` }));
  for (const span of spans ?? []) {
    if (span.rule_id && !options.some((rule) => rule.value === span.rule_id)) options.push({ value: span.rule_id, label: span.rule_id });
  }
  return options;
}

function placeQuote(body: string, quote: string, fallbackStart: number): Pick<KnowledgeSpan, "quote" | "start" | "end"> {
  const found = body.indexOf(quote);
  const start = quote && found >= 0 ? Array.from(body.slice(0, found)).length : fallbackStart;
  return { quote, start, end: start + Array.from(quote).length };
}

function QueryFailure({ label, error, onRetry }: { label: string; error: unknown; onRetry: () => void }) {
  return <p role="alert" className="workflow-error">{label}：{lookupError(error)} <Button htmlType="button" onClick={onRetry}>重试</Button></p>;
}

function SectionIssue({ id, issues }: { id: string; issues: KnowledgeFormIssue[] }) {
  const issue = issues.find((entry) => entry.id === id);
  return <div id={id} tabIndex={-1}>{issue && <p role="alert" className="workflow-error">{issue.message}</p>}</div>;
}

interface SectionProps {
  value: KnowledgeContent;
  onChange: (value: KnowledgeContent) => void;
  issues: KnowledgeFormIssue[];
  disabled: boolean;
}

function BasicSection({ value, onChange, issues, disabled, lockedIdentity }: SectionProps & { lockedIdentity: boolean }) {
  const set = (patch: Partial<KnowledgeContent>) => onChange({ ...value, ...patch });
  const kindSourceType = (kind: KnowledgeKind) =>
    kind === "experience" ? "human_feedback" : value.source_type === "human_feedback" ? "reference" : value.source_type;
  return <section className="rulepack-editor-section" aria-labelledby="knowledge-basic-heading">
    <h2 id="knowledge-basic-heading">知识内容与来源</h2>
    <div className="rulepack-fields">
      <Field id="knowledge-id" label="知识 ID" issues={issues}><Input id="knowledge-id" {...errorProps("knowledge-id", issues)} value={value.knowledge_id} readOnly={lockedIdentity} disabled={disabled} onChange={(event) => set({ knowledge_id: event.target.value })} /></Field>
      <Field id="knowledge-version" label="版本号" issues={issues}><Input id="knowledge-version" {...errorProps("knowledge-version", issues)} value={value.version} disabled={disabled} onChange={(event) => set({ version: event.target.value })} /></Field>
      <Field id="knowledge-kind" label="知识类型" issues={issues}><Select id="knowledge-kind" style={{ width: "100%" }} value={value.kind} options={kinds} disabled={disabled || lockedIdentity} onChange={(kind: KnowledgeKind) => set({ kind, source_type: kindSourceType(kind) })} /></Field>
      <Field id="knowledge-title" label="标题" issues={issues}><Input id="knowledge-title" {...errorProps("knowledge-title", issues)} value={value.title} disabled={disabled} onChange={(event) => set({ title: event.target.value })} /></Field>
      <Field id="knowledge-source-type" label="来源类型" issues={issues}><Select id="knowledge-source-type" style={{ width: "100%" }} value={value.source_type} disabled={disabled || value.kind === "experience"} options={[{ value: "reference", label: "参考制度" }, { value: "synthetic_policy", label: "合成制度" }, ...(value.kind === "experience" ? [{ value: "human_feedback", label: "人工反馈" }] : [])]} onChange={(source_type: string) => set({ source_type })} /></Field>
      <Field id="knowledge-source-reference" label="来源说明" issues={issues}><Input id="knowledge-source-reference" {...errorProps("knowledge-source-reference", issues)} value={value.source_reference} disabled={disabled} onChange={(event) => set({ source_reference: event.target.value })} /></Field>
      <Field id="knowledge-source-case" label="来源案件 ID（如有）" issues={issues}><Input id="knowledge-source-case" {...errorProps("knowledge-source-case", issues)} value={value.source_case_id ?? ""} disabled={disabled} onChange={(event) => set({ source_case_id: event.target.value })} /></Field>
    </div>
    <Field id="knowledge-summary" label="摘要" issues={issues}><Input.TextArea id="knowledge-summary" {...errorProps("knowledge-summary", issues)} value={value.summary} rows={2} disabled={disabled} onChange={(event) => set({ summary: event.target.value })} /></Field>
    <Field id="knowledge-body" label="正文" issues={issues}><Input.TextArea id="knowledge-body" {...errorProps("knowledge-body", issues)} value={value.body} rows={6} disabled={disabled} onChange={(event) => set({ body: event.target.value })} /></Field>
    <p className="meta">案件来源需由当前管理员具有来源案件访问权限；来源组织及反馈由服务端确认。</p>
  </section>;
}

function ConditionSection({ group, value, onChange, issues, disabled }: SectionProps & { group: "applicability" | "exclusions" }) {
  const items = value[group] ?? [];
  const setItems = (next: KnowledgeCondition[]) => onChange(withField(value, group, next));
  return <div>
    <h3>{group === "applicability" ? "适用条件（全部满足）" : "排除条件（任一满足即排除）"}</h3>
    {items.map((item, index) => <div className="rulepack-fields" key={index}>
      <Field id={`${group}-field-${index}`} label={`条件 ${index + 1} 字段`} issues={issues}><Select id={`${group}-field-${index}`} style={{ width: "100%" }} value={item.field} options={conditionFields} disabled={disabled} onChange={(field: string) => setItems(replaceAt(items, index, { field }))} /></Field>
      <Field id={`${group}-value-${index}`} label="等于" issues={issues}><Input id={`${group}-value-${index}`} {...errorProps(`${group}-value-${index}`, issues)} value={item.equals} disabled={disabled} onChange={(event) => setItems(replaceAt(items, index, { equals: event.target.value }))} /></Field>
      <Button htmlType="button" disabled={disabled} onClick={() => setItems(removeAt(items, index))}>删除条件 {index + 1}</Button>
    </div>)}
    <Button htmlType="button" disabled={disabled} onClick={() => setItems([...items, { field: "business_status", equals: "" }])}>添加{group === "applicability" ? "适用" : "排除"}条件</Button>
  </div>;
}

function ScopeSection({ value, onChange, issues, disabled }: SectionProps) {
  const setScope = (patch: Partial<KnowledgeContent["scope"]>) => onChange(withSection(value, "scope", patch));
  return <section className="rulepack-editor-section" aria-labelledby="knowledge-scope-heading">
    <h2 id="knowledge-scope-heading">适用范围与有效期</h2>
    <div className="rulepack-fields">
      <Field id="knowledge-line" label="业务线" issues={issues}><Input id="knowledge-line" {...errorProps("knowledge-line", issues)} value={value.scope.business_line} disabled={disabled} onChange={(event) => setScope({ business_line: event.target.value })} /></Field>
      <Field id="knowledge-scenario" label="业务场景" issues={issues}><Input id="knowledge-scenario" {...errorProps("knowledge-scenario", issues)} value={value.scope.scenario} disabled={disabled} onChange={(event) => setScope({ scenario: event.target.value })} /></Field>
      <Field id="knowledge-contract" label="合同类型" issues={issues}><Input id="knowledge-contract" {...errorProps("knowledge-contract", issues)} value={value.scope.contract_type} disabled={disabled} onChange={(event) => setScope({ contract_type: event.target.value })} /></Field>
      <Field id="knowledge-org" label="限定组织 ID（可选）" issues={issues}><Input id="knowledge-org" {...errorProps("knowledge-org", issues)} value={value.scope.org_id ?? ""} disabled={disabled} onChange={(event) => setScope({ org_id: event.target.value })} /></Field>
      <Field id="knowledge-case" label="限定案件 ID（可选）" issues={issues}><Input id="knowledge-case" value={value.scope.case_id ?? ""} disabled={disabled} onChange={(event) => setScope({ case_id: event.target.value })} /></Field>
      <Field id="knowledge-from" label="生效时间（本地时间）" issues={issues}><Input id="knowledge-from" {...errorProps("knowledge-from", issues)} type="datetime-local" step="0.001" value={toLocalInputValue(value.valid_from, "millisecond")} disabled={disabled} onChange={(event) => onChange({ ...value, valid_from: toUtcInstant(event.target.value) })} /></Field>
      <Field id="knowledge-until" label="失效时间（不含，本地时间）" issues={issues}><Input id="knowledge-until" {...errorProps("knowledge-until", issues)} type="datetime-local" step="0.001" value={toLocalInputValue(value.valid_until, "millisecond")} disabled={disabled} onChange={(event) => onChange({ ...value, valid_until: toUtcInstant(event.target.value) })} /></Field>
    </div>
    <ConditionSection group="applicability" value={value} onChange={onChange} issues={issues} disabled={disabled} />
    <ConditionSection group="exclusions" value={value} onChange={onChange} issues={issues} disabled={disabled} />
  </section>;
}

function SpanSection({ value, onChange, issues, disabled, ruleOptions: options }: SectionProps & { ruleOptions: { value: string; label: string }[] }) {
  const spans = value.binding.source_spans ?? [];
  const setSpans = (next: KnowledgeSpan[]) => onChange(withSpans(value, next));
  const changeSpan = (index: number, patch: Partial<KnowledgeSpan>) => setSpans(replaceAt(spans, index, patch));
  return <>
    {(spans).map((span, index) => <div className="rulepack-fields" key={index}>
      <Field id={`span-rule-${index}`} label={`引用 ${index + 1} 的规则`} issues={issues}><Select id={`span-rule-${index}`} {...errorProps(`span-rule-${index}`, issues)} style={{ width: "100%" }} value={span.rule_id || undefined} options={options} disabled={disabled} onChange={(rule_id: string) => changeSpan(index, { rule_id })} /></Field>
      <Field id={`span-quote-${index}`} label="正文中的原文" issues={issues}><Input id={`span-quote-${index}`} {...errorProps(`span-quote-${index}`, issues)} value={span.quote} disabled={disabled} onChange={(event) => changeSpan(index, placeQuote(value.body, event.target.value, span.start))} /></Field>
      <Field id={`span-start-${index}`} label="起始字符位置（从 0 开始）" issues={issues}><Input id={`span-start-${index}`} type="number" min={0} value={span.start} disabled={disabled} onChange={(event) => { const start = Number(event.target.value); changeSpan(index, { start, end: start + Array.from(span.quote).length }); }} /></Field>
      <Button htmlType="button" disabled={disabled} onClick={() => setSpans(removeAt(spans, index))}>删除引用 {index + 1}</Button>
    </div>)}
    <Button htmlType="button" disabled={disabled} onClick={() => setSpans([...spans, { rule_id: "", start: 0, end: 0, quote: "" }])}>添加规则引用</Button>
  </>;
}

function BindingSection({ value, onChange, issues, disabled }: SectionProps) {
  const packs = useRulePacks();
  const selectedPack = useRulePack(value.binding.pack_id, value.binding.pack_version);
  const activePacks = publishedPacks(packs.data ?? []);
  const { currentKey, options } = packOptions(activePacks, value.binding);
  const options2 = ruleOptions(selectedPack.data?.manifest.rules, value.binding.source_spans);
  const setBinding = (patch: Partial<KnowledgeContent["binding"]>) => onChange(withSection(value, "binding", patch));
  const selectPack = (key: string) => {
    const pack = activePacks.find((entry) => JSON.stringify([entry.id, entry.version]) === key);
    if (pack) setBinding({ pack_id: pack.id, pack_version: pack.version, pack_content_hash: pack.content_hash, pack_snapshot_digest: pack.snapshot_sha256, compiler_ref: "governed-rulepack/v1", rule_ids: [], source_spans: [] });
  };
  return <section className="rulepack-editor-section" aria-labelledby="knowledge-binding-heading">
    <h2 id="knowledge-binding-heading">规则包绑定与正文依据</h2>
    <p>选择当前生效规则包，为每条引用规则标明正文原文。校验与发布时服务端会重新核验规则包、范围及有效期。</p>
    {packs.isError && <QueryFailure label="规则包列表" error={packs.error} onRetry={() => void packs.refetch()} />}
    {selectedPack.isError && <QueryFailure label="规则包详情" error={selectedPack.error} onRetry={() => void selectedPack.refetch()} />}
    <Field id="knowledge-pack" label="已发布并生效的规则包" issues={issues}><Select id="knowledge-pack" {...errorProps("knowledge-pack", issues)} style={{ width: "100%" }} value={currentKey} options={options} loading={packs.isLoading} disabled={disabled} onChange={selectPack} /></Field>
    {selectedPack.data && <p className="meta">规则包有效期：{selectedPack.data.pack.effective_from} 至 {selectedPack.data.pack.effective_until}；内容摘要 {selectedPack.data.pack.content_hash}</p>}
    <SectionIssue id="knowledge-spans" issues={issues} />
    <SpanSection value={value} onChange={onChange} issues={issues} disabled={disabled} ruleOptions={options2} />
    <p className="meta">输入原文后会定位首次出现的位置；若原文出现多次，可修改起始字符位置。位置按 Unicode 字符计算。</p>
    <Field id="knowledge-diff" label="候选规则差异说明" issues={issues}><Input.TextArea id="knowledge-diff" {...errorProps("knowledge-diff", issues)} value={value.binding.candidate_diff} rows={2} disabled={disabled} onChange={(event) => setBinding({ candidate_diff: event.target.value })} /></Field>
    <Field id="knowledge-evidence" label="验证依据" issues={issues}><Input.TextArea id="knowledge-evidence" {...errorProps("knowledge-evidence", issues)} value={value.binding.validation_evidence} rows={2} disabled={disabled} onChange={(event) => setBinding({ validation_evidence: event.target.value })} /></Field>
  </section>;
}

function ExceptionSection({ value, onChange, issues, disabled }: SectionProps) {
  return <section className="rulepack-editor-section"><h2>例外批准依据</h2><Field id="knowledge-exception" label="批准依据" issues={issues}><Input.TextArea id="knowledge-exception" {...errorProps("knowledge-exception", issues)} value={value.exception_basis ?? ""} rows={3} disabled={disabled} onChange={(event) => onChange({ ...value, exception_basis: event.target.value })} /></Field></section>;
}

function ExperienceSection({ value, onChange, issues, disabled }: SectionProps) {
  const set = (patch: Partial<KnowledgeContent>) => onChange({ ...value, ...patch });
  return <section className="rulepack-editor-section">
    <h2>经验候选与人工审阅</h2>
    <p>填写案件中已生成的精确候选身份，并对内容、范围、反例和依赖进行人工审阅。有效期和政策依赖必须与候选约束一致。</p>
    <div className="rulepack-fields">
      <Field id="knowledge-candidate" label="候选 ID" issues={issues}><Input id="knowledge-candidate" {...errorProps("knowledge-candidate", issues)} value={textOrEmpty(value.candidate_id)} disabled={disabled} onChange={(event) => set({ candidate_id: event.target.value })} /></Field>
      <Field id="knowledge-candidate-version" label="候选版本 ID" issues={issues}><Input id="knowledge-candidate-version" {...errorProps("knowledge-candidate-version", issues)} value={textOrEmpty(value.candidate_version_id)} disabled={disabled} onChange={(event) => set({ candidate_version_id: event.target.value })} /></Field>
      <Field id="knowledge-candidate-hash" label="候选内容 SHA-256" issues={issues}><Input id="knowledge-candidate-hash" {...errorProps("knowledge-candidate-hash", issues)} value={textOrEmpty(value.candidate_content_sha256)} disabled={disabled} onChange={(event) => set({ candidate_content_sha256: event.target.value })} /></Field>
    </div>
    <Field id="knowledge-review-diff" label="候选审阅差异" issues={issues}><Input.TextArea id="knowledge-review-diff" {...errorProps("knowledge-review-diff", issues)} rows={3} value={textOrEmpty(value.candidate_review_diff)} disabled={disabled} onChange={(event) => set({ candidate_review_diff: event.target.value })} /></Field>
    <Field id="knowledge-reviewed-applicability" label="审阅后的适用条件（每行一项）" issues={issues}><Input.TextArea id="knowledge-reviewed-applicability" {...errorProps("knowledge-reviewed-applicability", issues)} rows={3} value={linesOrEmpty(value.reviewed_applicability)} disabled={disabled} onChange={(event) => set({ reviewed_applicability: lines(event.target.value) })} /></Field>
    <Field id="knowledge-reviewed-exclusions" label="审阅后的排除条件（每行一项）" issues={issues}><Input.TextArea id="knowledge-reviewed-exclusions" {...errorProps("knowledge-reviewed-exclusions", issues)} rows={3} value={linesOrEmpty(value.reviewed_exclusions)} disabled={disabled} onChange={(event) => set({ reviewed_exclusions: lines(event.target.value) })} /></Field>
    <Field id="knowledge-check-action" label="检查动作" issues={issues}><Input.TextArea id="knowledge-check-action" {...errorProps("knowledge-check-action", issues)} rows={2} value={textOrEmpty(value.check_action)} disabled={disabled} onChange={(event) => set({ check_action: event.target.value })} /></Field>
    <Field id="knowledge-minimum-evidence" label="最低证据" issues={issues}><Input.TextArea id="knowledge-minimum-evidence" {...errorProps("knowledge-minimum-evidence", issues)} rows={2} value={textOrEmpty(value.minimum_evidence)} disabled={disabled} onChange={(event) => set({ minimum_evidence: event.target.value })} /></Field>
    <Field id="knowledge-counterexamples" label="反例（每行一项）" issues={issues}><Input.TextArea id="knowledge-counterexamples" {...errorProps("knowledge-counterexamples", issues)} rows={3} value={linesOrEmpty(value.counterexamples)} disabled={disabled} onChange={(event) => set({ counterexamples: lines(event.target.value) })} /></Field>
  </section>;
}

function policyOptions(items: KnowledgeSummary[]): { value: string; label: string }[] {
  return items.filter((item) => item.kind === "policy" && item.active).map((item) => ({ value: JSON.stringify(item.ref), label: `${item.title} · ${item.ref.knowledge_id} v${item.version}` }));
}

function withPolicyRef(refs: KnowledgeRef[] | undefined, item: KnowledgeRef): KnowledgeRef[] {
  const current = refs ?? [];
  return current.some((entry) => entry.knowledge_id === item.knowledge_id && entry.version_id === item.version_id) ? current : [...current, item];
}

function PolicyPicker({ value, onChange, issues, disabled }: SectionProps) {
  const policies = useQuery({ queryKey: ["admin", "governed-knowledge", "list"], queryFn: ({ signal }) => listGovernedKnowledge(signal), retry: false });
  const setRefs = (next: KnowledgeRef[]) => onChange(withField(value, "policy_refs", next));
  const addPolicy = (key: string) => {
    const item = (policies.data ?? []).find((entry) => JSON.stringify(entry.ref) === key);
    if (item) setRefs(withPolicyRef(value.policy_refs, item.ref));
  };
  return <>
    {policies.isError && <QueryFailure label="政策列表" error={policies.error} onRetry={() => void policies.refetch()} />}
    <label className="field" htmlFor="knowledge-policy-select">从可访问的生效政策添加
      <Select id="knowledge-policy-select" style={{ width: "100%" }} value={undefined} placeholder="选择政策版本" disabled={disabled} options={policyOptions(policies.data ?? [])} onChange={addPolicy} />
    </label>
    <SectionIssue id="knowledge-policy-refs" issues={issues} />
  </>;
}

function PolicyRefsSection({ value, onChange, issues, disabled }: SectionProps) {
  const refs = value.policy_refs ?? [];
  const setRefs = (next: KnowledgeRef[]) => onChange(withField(value, "policy_refs", next));
  return <section className="rulepack-editor-section" aria-labelledby="knowledge-policy-refs-heading">
    <h2 id="knowledge-policy-refs-heading">政策依赖</h2>
    <p>例外和经验需要精确的政策版本。经验的依赖必须与候选记录一致。</p>
    <PolicyPicker value={value} onChange={onChange} issues={issues} disabled={disabled} />
    {refs.map((item, index) => <div className="rulepack-fields" key={index}>
      <Field id={`policy-ref-${index}`} label={`政策 ${index + 1} ID`} issues={issues}><Input id={`policy-ref-${index}`} {...errorProps(`policy-ref-${index}`, issues)} value={item.knowledge_id} disabled={disabled} onChange={(event) => setRefs(replaceAt(refs, index, { knowledge_id: event.target.value }))} /></Field>
      <Field id={`policy-ref-version-${index}`} label="精确版本 ID" issues={issues}><Input id={`policy-ref-version-${index}`} value={item.version_id} disabled={disabled} onChange={(event) => setRefs(replaceAt(refs, index, { version_id: event.target.value }))} /></Field>
      <Field id={`policy-ref-hash-${index}`} label="内容 SHA-256" issues={issues}><Input id={`policy-ref-hash-${index}`} value={item.content_sha256} disabled={disabled} onChange={(event) => setRefs(replaceAt(refs, index, { content_sha256: event.target.value }))} /></Field>
      <Button htmlType="button" disabled={disabled} onClick={() => setRefs(removeAt(refs, index))}>删除政策 {index + 1}</Button>
    </div>)}
    <Button htmlType="button" disabled={disabled} onClick={() => setRefs([...refs, { knowledge_id: "", version_id: "", content_sha256: "" }])}>手动添加政策依赖</Button>
  </section>;
}

export function KnowledgeEditor({ value, onChange, issues, lockedIdentity, disabled }: {
  value: KnowledgeContent; onChange: (value: KnowledgeContent) => void; issues: KnowledgeFormIssue[]; lockedIdentity: boolean; disabled: boolean;
}) {
  return <div className="rulepack-editor">
    <BasicSection value={value} onChange={onChange} issues={issues} lockedIdentity={lockedIdentity} disabled={disabled} />
    <ScopeSection value={value} onChange={onChange} issues={issues} disabled={disabled} />
    <BindingSection value={value} onChange={onChange} issues={issues} disabled={disabled} />
    {value.kind === "exception" && <ExceptionSection value={value} onChange={onChange} issues={issues} disabled={disabled} />}
    {value.kind === "experience" && <ExperienceSection value={value} onChange={onChange} issues={issues} disabled={disabled} />}
    <PolicyRefsSection value={value} onChange={onChange} issues={issues} disabled={disabled} />
  </div>;
}
