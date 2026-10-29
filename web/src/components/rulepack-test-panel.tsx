import { Card } from "antd";
import { Button, Input, Select } from "antd";
import {
  useState,
  useEffect,
  useRef,
  type FormEvent,
  type RefObject,
} from "react";
import { errorMessage, isConflict } from "../api/http";
import { useTestRulePack } from "../api/queries";
import type {
  RulePackManifest,
  RulePackRelation,
  RulePackRule,
  RulePackTestFact,
  RulePackTestInput,
  RulePackTestResponse,
  RulePackTestSource,
} from "../api/rulepacks";
import { StatusText } from "./status-text";
import { toLocalInputValue, toUtcInstant } from "./local-datetime";
import type { StatusTone } from "../domain/review";

interface Selection {
  start: number;
  end: number;
  quote: string;
}
const RELATION_LABELS: Record<RulePackRelation, string> = {
  compatible: "符合",
  conflict: "冲突",
  insufficient: "缺证 / 不可比较",
  not_applicable: "不适用",
};

const RELATION_TONES: Record<RulePackRelation, StatusTone> = {
  compatible: "good",
  conflict: "bad",
  insufficient: "attention",
  not_applicable: "neutral",
};

function selectionKey(
  source: Pick<RulePackTestSource, "id" | "version">,
): string {
  return JSON.stringify([source.id, source.version]);
}

function toCodePointOffset(text: string, utf16Offset: number): number {
  return Array.from(text.slice(0, utf16Offset)).length;
}

function newSampleSource(index: number): RulePackTestSource {
  return { id: `sample-source-${index}`, version: "1", role: "", text: "" };
}

function newSampleFact(rule: RulePackRule, index: number): RulePackTestFact {
  return {
    id: `sample-fact-${index}`,
    rule_id: rule.rule_id,
    dimension: rule.required_fact.dimension,
    object_ref: "",
    value: "",
    unit: rule.threshold.unit,
    source_role: rule.required_fact.source_roles[0] ?? "",
    citations: [],
  };
}

function readSelection(
  text: string,
  start: number,
  end: number,
): Selection | null {
  if (start >= end) return null;
  return {
    start: toCodePointOffset(text, start),
    end: toCodePointOffset(text, end),
    quote: text.slice(start, end),
  };
}

function SourceEditor({
  source,
  index,
  onChange,
  onSelect,
  onRemove,
  disabled,
}: {
  source: RulePackTestSource;
  index: number;
  onChange: (source: RulePackTestSource) => void;
  onSelect: (source: RulePackTestSource, selection: Selection | null) => void;
  onRemove: () => void;
  disabled: boolean;
}) {
  return (
    <Card
      role="group"
      aria-label={"样本材料 " + String(index + 1)}
      className="rulepack-sample-card"
      title={<>样本材料 {index + 1}</>}
    >
      <div className="rulepack-fields">
        <label className="field" htmlFor={`sample-source-${index}-id`}>
          样本材料 ID
          <Input
            disabled={disabled}
            id={`sample-source-${index}-id`}
            required
            value={source.id}
            onChange={(event) =>
              onChange({ ...source, id: event.target.value })
            }
          />
        </label>
        <label className="field" htmlFor={`sample-source-${index}-version`}>
          材料版本
          <Input
            disabled={disabled}
            id={`sample-source-${index}-version`}
            required
            value={source.version}
            onChange={(event) =>
              onChange({ ...source, version: event.target.value })
            }
          />
        </label>
        <label className="field" htmlFor={`sample-source-${index}-role`}>
          材料角色
          <Input
            disabled={disabled}
            id={`sample-source-${index}-role`}
            required
            value={source.role}
            onChange={(event) =>
              onChange({ ...source, role: event.target.value })
            }
          />
        </label>
        <label
          className="field rulepack-sample-source-text"
          htmlFor={`sample-source-${index}-text`}
        >
          样本文本
          <Input.TextArea
            disabled={disabled}
            id={`sample-source-${index}-text`}
            required
            value={source.text}
            onSelect={(event) =>
              onSelect(
                source,
                readSelection(
                  event.currentTarget.value,
                  event.currentTarget.selectionStart,
                  event.currentTarget.selectionEnd,
                ),
              )
            }
            onChange={(event) =>
              onChange({ ...source, text: event.target.value })
            }
          />
        </label>
      </div>
      <p className="meta">
        选中文字后可作为引用。界面会把浏览器 UTF-16 选区换算为服务端使用的
        Unicode 码点半区间。
      </p>
      <Button
        aria-label={"移除材料 " + String(index + 1)}
        disabled={disabled}
        type="text"
        htmlType="button"
        onClick={onRemove}
      >
        移除材料 {index + 1}
      </Button>
    </Card>
  );
}

function FactIdentityFields({
  fact,
  index,
  rules,
  disabled,
  onChange,
}: {
  fact: RulePackTestFact;
  index: number;
  rules: RulePackRule[];
  disabled: boolean;
  onChange: (fact: RulePackTestFact) => void;
}) {
  return (
    <>
      <label className="field" htmlFor={`sample-fact-${index}-id`}>
        事实 ID
        <Input
          disabled={disabled}
          id={`sample-fact-${index}-id`}
          required
          value={fact.id}
          onChange={(event) => onChange({ ...fact, id: event.target.value })}
        />
      </label>
      <label className="field" htmlFor={`sample-fact-${index}-rule`}>
        对应规则
        <Select
          disabled={disabled}
          id={`sample-fact-${index}-rule`}
          aria-label="对应规则"
          value={fact.rule_id || undefined}
          onChange={(value) => {
            const rule = rules.find((item) => item.rule_id === value);
            if (rule)
              onChange({
                ...fact,
                rule_id: rule.rule_id,
                dimension: rule.required_fact.dimension,
                unit: rule.threshold.unit,
                source_role: rule.required_fact.source_roles[0] ?? "",
              });
          }}
          options={[
            { label: "选择规则", value: "" },
            ...rules.map((rule, ruleIndex) => ({
              value: rule.rule_id,
              label: `${rule.rule_id || `未命名规则 ${ruleIndex + 1}`}，${rule.capability_id || "未填写能力"}`,
            })),
          ]}
        />
      </label>
      <label className="field" htmlFor={`sample-fact-${index}-dimension`}>
        事实维度
        <Input
          disabled={disabled}
          id={`sample-fact-${index}-dimension`}
          required
          value={fact.dimension}
          onChange={(event) =>
            onChange({ ...fact, dimension: event.target.value })
          }
        />
      </label>
      <label className="field" htmlFor={`sample-fact-${index}-object`}>
        业务对象
        <Input
          disabled={disabled}
          id={`sample-fact-${index}-object`}
          value={fact.object_ref}
          onChange={(event) =>
            onChange({ ...fact, object_ref: event.target.value })
          }
        />
      </label>
      <label className="field" htmlFor={`sample-fact-${index}-value`}>
        事实值
        <Input
          disabled={disabled}
          id={`sample-fact-${index}-value`}
          required
          value={fact.value}
          onChange={(event) => onChange({ ...fact, value: event.target.value })}
        />
      </label>
      <label className="field" htmlFor={`sample-fact-${index}-unit`}>
        单位
        <Input
          disabled={disabled}
          id={`sample-fact-${index}-unit`}
          required
          value={fact.unit}
          onChange={(event) => onChange({ ...fact, unit: event.target.value })}
        />
      </label>
    </>
  );
}

function roleOptions(
  sources: RulePackTestSource[],
  selectedRule: RulePackRule | undefined,
): { value: string; label: string }[] {
  return [
    ...new Set(
      [
        ...sources.map((source) => source.role),
        ...(selectedRule?.required_fact.source_roles ?? []),
      ].filter(Boolean),
    ),
  ].map((role) => ({ value: role, label: role }));
}

function FactRoleField({
  fact,
  index,
  rules,
  sources,
  disabled,
  onChange,
}: {
  fact: RulePackTestFact;
  index: number;
  rules: RulePackRule[];
  sources: RulePackTestSource[];
  disabled: boolean;
  onChange: (fact: RulePackTestFact) => void;
}) {
  const selectedRule = rules.find((rule) => rule.rule_id === fact.rule_id);
  return (
    <label className="field" htmlFor={`sample-fact-${index}-role`}>
      材料角色
      <Select
        disabled={disabled}
        id={`sample-fact-${index}-role`}
        aria-label="材料角色"
        value={fact.source_role || undefined}
        onChange={(value) => onChange({ ...fact, source_role: value })}
        options={[
          { label: "选择样本材料角色", value: "" },
          ...roleOptions(sources, selectedRule),
        ]}
      />
    </label>
  );
}

function OptionalFactField({
  id,
  label,
  value,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <label className="field" htmlFor={id}>
      {label}
      <Input
        disabled={disabled}
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}

function optionalValue(value: string): string | undefined {
  return value || undefined;
}

function displayOptional(value: string | undefined): string {
  return value ?? "";
}

function FactOptionalFields({
  fact,
  index,
  disabled,
  onChange,
}: {
  fact: RulePackTestFact;
  index: number;
  disabled: boolean;
  onChange: (fact: RulePackTestFact) => void;
}) {
  const optional = (key: keyof RulePackTestFact) => (value: string) =>
    onChange({ ...fact, [key]: optionalValue(value) });
  return (
    <>
      <OptionalFactField
        id={`sample-fact-${index}-window`}
        label="测量窗口（可选）"
        value={displayOptional(fact.measurement_window)}
        disabled={disabled}
        onChange={optional("measurement_window")}
      />
      <OptionalFactField
        id={`sample-fact-${index}-timezone`}
        label="时区（可选）"
        value={displayOptional(fact.time_zone)}
        disabled={disabled}
        onChange={optional("time_zone")}
      />
      <OptionalFactField
        id={`sample-fact-${index}-calendar`}
        label="日历（可选）"
        value={displayOptional(fact.calendar)}
        disabled={disabled}
        onChange={optional("calendar")}
      />
      <OptionalFactField
        id={`sample-fact-${index}-outage`}
        label="故障口径（可选）"
        value={displayOptional(fact.outage_definition)}
        disabled={disabled}
        onChange={optional("outage_definition")}
      />
      <OptionalFactField
        id={`sample-fact-${index}-basis`}
        label="计算基数（可选）"
        value={displayOptional(fact.basis)}
        disabled={disabled}
        onChange={optional("basis")}
      />
      <label className="field" htmlFor={`sample-fact-${index}-exclusions`}>
        排除项（每行一项）
        <Input.TextArea
          disabled={disabled}
          id={`sample-fact-${index}-exclusions`}
          value={(fact.exclusions ?? []).join("\n")}
          onChange={(event) =>
            onChange({
              ...fact,
              exclusions: event.target.value
                .split("\n")
                .map((item) => item.trim())
                .filter(Boolean),
            })
          }
        />
      </label>
    </>
  );
}

function applicabilityValue(value: boolean | undefined): string {
  if (value === undefined) return "unset";
  return value ? "yes" : "no";
}

function applicabilityPatch(value: string): boolean | undefined {
  if (value === "unset") return undefined;
  return value === "yes";
}

function FactApplicabilityField({
  fact,
  index,
  disabled,
  onChange,
}: {
  fact: RulePackTestFact;
  index: number;
  disabled: boolean;
  onChange: (fact: RulePackTestFact) => void;
}) {
  return (
    <label className="field" htmlFor={`sample-fact-${index}-applicable`}>
      适用性
      <Select
        disabled={disabled}
        id={`sample-fact-${index}-applicable`}
        aria-label="适用性"
        value={applicabilityValue(fact.applicable)}
        onChange={(value) =>
          onChange({ ...fact, applicable: applicabilityPatch(value) })
        }
        options={[
          { label: "未指定", value: "unset" },
          { label: "适用", value: "yes" },
          { label: "不适用", value: "no" },
        ]}
      />
    </label>
  );
}

function CitationRows({
  fact,
  index,
  disabled,
  onChange,
}: {
  fact: RulePackTestFact;
  index: number;
  disabled: boolean;
  onChange: (fact: RulePackTestFact) => void;
}) {
  return (
    <ul className="rulepack-result-list">
      {fact.citations.map((citation, citationIndex) => (
        <li key={`${citation.source_id}:${citation.start}:${citationIndex}`}>
          <strong>
            {citation.source_id} v{citation.source_version}，码点 [
            {citation.start},{citation.end})
          </strong>
          <blockquote>{citation.quote}</blockquote>
          <Button
            disabled={disabled}
            type="text"
            htmlType="button"
            aria-label={`移除事实 ${index + 1} 的引用 ${citationIndex + 1}`}
            onClick={() =>
              onChange({
                ...fact,
                citations: fact.citations.filter(
                  (_, itemIndex) => itemIndex !== citationIndex,
                ),
              })
            }
          >
            移除引用
          </Button>
        </li>
      ))}
    </ul>
  );
}

function citationSourceOf(
  eligibleSources: RulePackTestSource[],
  citationSourceKey: string,
): RulePackTestSource | undefined {
  return (
    eligibleSources.find(
      (source) => selectionKey(source) === citationSourceKey,
    ) ?? eligibleSources[0]
  );
}

function CitationSourceSummary({
  selectedSource,
  selection,
  sourceRole,
}: {
  selectedSource: RulePackTestSource | undefined;
  selection: Selection | undefined;
  sourceRole: string;
}) {
  if (!selectedSource) {
    return (
      <p className="meta">
        先添加角色为“{sourceRole || "所选事实角色"}”的样本材料。
      </p>
    );
  }
  return (
    <p className="meta">
      当前来源 {selectedSource.id} v{selectedSource.version} 的选区：
      {selection
        ? `码点 [${selection.start},${selection.end})`
        : "未选择文本"}
    </p>
  );
}

function AddCitationButton({
  disabled,
  selectedSource,
  selection,
  citationCount,
  onUse,
}: {
  disabled: boolean;
  selectedSource: RulePackTestSource | undefined;
  selection: Selection | undefined;
  citationCount: number;
  onUse: () => void;
}) {
  return (
    <Button
      aria-label={"添加当前选区引用"}
      htmlType="button"
      disabled={
        disabled || !selectedSource || !selection || citationCount >= 8
      }
      onClick={onUse}
    >
      添加当前选区引用
    </Button>
  );
}

function FactCitationSection({
  fact,
  index,
  sources,
  selections,
  disabled,
  onChange,
  onUseSelection,
}: {
  fact: RulePackTestFact;
  index: number;
  sources: RulePackTestSource[];
  selections: Record<string, Selection | undefined>;
  disabled: boolean;
  onChange: (fact: RulePackTestFact) => void;
  onUseSelection: (source: RulePackTestSource) => void;
}) {
  const [citationSourceKey, setCitationSourceKey] = useState("");
  const eligibleSources = sources.filter(
    (source) => source.role === fact.source_role && source.id !== "",
  );
  const selectedSource = citationSourceOf(eligibleSources, citationSourceKey);
  const selection = selectedSource
    ? selections[selectionKey(selectedSource)]
    : undefined;
  return (
    <section
      className="rulepack-citations"
      aria-labelledby={`sample-fact-${index}-citations-title`}
    >
      <h4 id={`sample-fact-${index}-citations-title`}>来源引用</h4>
      <label
        className="field"
        htmlFor={`sample-fact-${index}-citation-source`}
      >
        引用材料
        <Select
          disabled={disabled}
          id={`sample-fact-${index}-citation-source`}
          aria-label="引用材料"
          value={
            selectedSource
              ? selectionKey(selectedSource) || undefined
              : undefined
          }
          onChange={setCitationSourceKey}
          options={[
            { label: "选择材料角色匹配的样本", value: "" },
            ...eligibleSources.map((source) => ({
              value: selectionKey(source),
              label: `${source.id} v${source.version}`,
            })),
          ]}
        />
      </label>
      {fact.citations.length === 0 ? (
        <p>至少添加一条与样本原文完全相同的引用，事实才会参与比较。</p>
      ) : (
        <CitationRows
          fact={fact}
          index={index}
          disabled={disabled}
          onChange={onChange}
        />
      )}
      <CitationSourceSummary
        selectedSource={selectedSource}
        selection={selection}
        sourceRole={fact.source_role}
      />
      <AddCitationButton
        disabled={disabled}
        selectedSource={selectedSource}
        selection={selection}
        citationCount={fact.citations.length}
        onUse={() => {
          if (selectedSource) onUseSelection(selectedSource);
        }}
      />
    </section>
  );
}

function FactEditor({
  fact,
  index,
  rules,
  sources,
  selections,
  onChange,
  onUseSelection,
  onRemove,
  disabled,
}: {
  fact: RulePackTestFact;
  index: number;
  rules: RulePackRule[];
  sources: RulePackTestSource[];
  selections: Record<string, Selection | undefined>;
  onChange: (fact: RulePackTestFact) => void;
  onUseSelection: (source: RulePackTestSource) => void;
  onRemove: () => void;
  disabled: boolean;
}) {
  return (
    <Card
      role="group"
      aria-label={"类型化事实 " + String(index + 1)}
      className="rulepack-sample-card"
      title={<>类型化事实 {index + 1}</>}
    >
      <div className="rulepack-fields">
        <FactIdentityFields
          fact={fact}
          index={index}
          rules={rules}
          disabled={disabled}
          onChange={onChange}
        />
        <FactRoleField
          fact={fact}
          index={index}
          rules={rules}
          sources={sources}
          disabled={disabled}
          onChange={onChange}
        />
        <FactOptionalFields
          fact={fact}
          index={index}
          disabled={disabled}
          onChange={onChange}
        />
        <FactApplicabilityField
          fact={fact}
          index={index}
          disabled={disabled}
          onChange={onChange}
        />
      </div>
      <FactCitationSection
        fact={fact}
        index={index}
        sources={sources}
        selections={selections}
        disabled={disabled}
        onChange={onChange}
        onUseSelection={onUseSelection}
      />
      <Button
        aria-label={"移除事实 " + String(index + 1)}
        disabled={disabled}
        type="text"
        htmlType="button"
        onClick={onRemove}
      >
        移除事实 {index + 1}
      </Button>
    </Card>
  );
}

function TestScopeFields({
  businessLine,
  scenario,
  contractType,
  at,
  onBusinessLine,
  onScenario,
  onContractType,
  onAt,
}: {
  businessLine: string;
  scenario: string;
  contractType: string;
  at: string;
  onBusinessLine: (value: string) => void;
  onScenario: (value: string) => void;
  onContractType: (value: string) => void;
  onAt: (value: string) => void;
}) {
  return (
    <div className="rulepack-fields">
      <label className="field" htmlFor="rulepack-test-business-line">
        业务线
        <Input
          id="rulepack-test-business-line"
          required
          value={businessLine}
          onChange={(event) => onBusinessLine(event.target.value)}
        />
      </label>
      <label className="field" htmlFor="rulepack-test-scenario">
        场景
        <Input
          id="rulepack-test-scenario"
          required
          value={scenario}
          onChange={(event) => onScenario(event.target.value)}
        />
      </label>
      <label className="field" htmlFor="rulepack-test-contract-type">
        合同类型
        <Input
          id="rulepack-test-contract-type"
          required
          value={contractType}
          onChange={(event) => onContractType(event.target.value)}
        />
      </label>
      <label className="field" htmlFor="rulepack-test-at">
        测试时点（本地时区）
        <Input
          id="rulepack-test-at"
          type="datetime-local"
          required
          value={toLocalInputValue(at)}
          onChange={(event) => onAt(toUtcInstant(event.target.value))}
        />
      </label>
    </div>
  );
}

function SourcesSection({
  sources,
  sourceKeys,
  onAdd,
  onChange,
  onSelect,
  onRemove,
  disabled,
}: {
  sources: RulePackTestSource[];
  sourceKeys: string[];
  onAdd: () => void;
  onChange: (index: number, next: RulePackTestSource) => void;
  onSelect: (source: RulePackTestSource, selection: Selection | null) => void;
  onRemove: (index: number) => void;
  disabled: boolean;
}) {
  return (
    <section aria-labelledby="rulepack-test-sources-title">
      <header className="workflow-panel-head">
        <div>
          <h3 id="rulepack-test-sources-title">样本材料</h3>
          <p className="meta">接口最多 16 份材料，单份文本最多 32 KiB。</p>
        </div>
        <Button
          aria-label={"添加样本材料"}
          htmlType="button"
          disabled={sources.length >= 16}
          onClick={onAdd}
        >
          添加样本材料
        </Button>
      </header>
      {sources.map((source, index) => (
        <SourceEditor
          key={sourceKeys[index] ?? index}
          source={source}
          index={index}
          onChange={(next) => onChange(index, next)}
          onSelect={onSelect}
          onRemove={() => onRemove(index)}
          disabled={disabled}
        />
      ))}
      {sources.length === 0 ? (
        <p>添加用于本次测试的样本文本；不要引用真实案件材料。</p>
      ) : null}
    </section>
  );
}

function FactsSection({
  facts,
  factKeys,
  rules,
  sources,
  selections,
  onAdd,
  onChange,
  onUseSelection,
  onRemove,
  disabled,
}: {
  facts: RulePackTestFact[];
  factKeys: string[];
  rules: RulePackRule[];
  sources: RulePackTestSource[];
  selections: Record<string, Selection | undefined>;
  onAdd: () => void;
  onChange: (index: number, next: RulePackTestFact) => void;
  onUseSelection: (factIndex: number, source: RulePackTestSource) => void;
  onRemove: (index: number) => void;
  disabled: boolean;
}) {
  return (
    <section aria-labelledby="rulepack-test-facts-title">
      <header className="workflow-panel-head">
        <div>
          <h3 id="rulepack-test-facts-title">类型化事实</h3>
          <p className="meta">
            事实只有在来源角色、码点引用和有限比较器同时满足时才可比较；缺证结果会保留为不可比较。
          </p>
        </div>
        <Button
          aria-label={"添加事实"}
          htmlType="button"
          disabled={rules.length === 0 || facts.length >= 128}
          onClick={onAdd}
        >
          添加事实
        </Button>
      </header>
      {facts.map((fact, index) => (
        <FactEditor
          key={factKeys[index] ?? index}
          fact={fact}
          index={index}
          rules={rules}
          sources={sources}
          selections={selections}
          onChange={(next) => onChange(index, next)}
          onUseSelection={(source) => onUseSelection(index, source)}
          onRemove={() => onRemove(index)}
          disabled={disabled}
        />
      ))}
      {facts.length === 0 ? (
        <p>可先不提供事实，运行一次明确的缺证样本测试。</p>
      ) : null}
    </section>
  );
}

function TestResult({ data }: { data: RulePackTestResponse }) {
  return (
    <section
      className="rulepack-test-result"
      aria-labelledby="rulepack-test-result-title"
    >
      <h3 id="rulepack-test-result-title">测试结果</h3>
      <p role="status">
        规则包 {data.report.pack_id} v{data.report.pack_version}，
        测试修订 {data.revision}，内容摘要{" "}
        <code className="digest">{data.content_hash}</code>
      </p>
      <p>
        总关系：
        {data.report.relations
          .map((relation) => RELATION_LABELS[relation])
          .join("、") || "没有适用规则"}
      </p>
      {data.report.results.length === 0 ? (
        <p>当前业务范围没有匹配规则。</p>
      ) : (
        <ul className="rulepack-result-list">
          {data.report.results.map((result, index) => (
            <li key={`${result.rule_id}:${result.rule_version}:${index}`}>
              <div className="workflow-panel-head">
                <strong>
                  {result.domain}，{result.capability_id}
                </strong>
                <StatusText tone={RELATION_TONES[result.relation]}>
                  {RELATION_LABELS[result.relation]}
                </StatusText>
              </div>
              <p>
                规则 {result.rule_id} v{result.rule_version}，
                {result.mandatory ? "必检" : "非必检"}，原因代码{" "}
                <code>{result.reason_code}</code>
              </p>
              {result.fact_ids?.length ? (
                <p>采用事实：{result.fact_ids.join("、")}</p>
              ) : (
                <p>未采用可核验事实。</p>
              )}
              {result.citations?.map((citation, citationIndex) => (
                <figure
                  key={`${citation.source_id}:${citation.source_digest}:${citation.start}:${citation.end}:${citationIndex}`}
                >
                  <blockquote>
                    {citation.source_id} v{citation.source_version}，码点 [
                    {citation.start},{citation.end})，{citation.quote}
                  </blockquote>
                  <figcaption>
                    租户 {citation.tenant_id}，案件 {citation.case_id}，
                    来源 SHA-256 <code>{citation.source_digest}</code>
                  </figcaption>
                </figure>
              ))}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

interface ValidationParams {
  manifest: RulePackManifest;
  businessLine: string;
  scenario: string;
  contractType: string;
  at: string;
  sources: RulePackTestSource[];
  facts: RulePackTestFact[];
  revision: number;
}

interface ValidationOutcome {
  message: string | null;
  input: RulePackTestInput | null;
}

const SOURCE_MESSAGE =
  "样本材料最多 16 份；每份都必须填写 ID、版本、角色和文本，且 ID 与版本组合不能重复。";
const FACT_MESSAGE =
  "类型化事实最多 128 条、总引用最多 256 条；每条事实 ID 唯一且需提供 1–8 条来源引用。";

function scopeTimeMessage(at: string, manifest: RulePackManifest): string | null {
  if (
    Date.parse(at) < Date.parse(manifest.effective_from) ||
    Date.parse(at) >= Date.parse(manifest.effective_until)
  ) {
    return "测试时间必须位于该版本的有效期内。";
  }
  return null;
}

function sourceIncomplete(source: RulePackTestSource): boolean {
  return (
    !source.id.trim() ||
    !source.version.trim() ||
    !source.role.trim() ||
    !source.text.trim()
  );
}

function sourceIdentityMessage(sources: RulePackTestSource[]): string | null {
  const identities = sources.map((source) =>
    JSON.stringify([source.id, source.version]),
  );
  if (sources.length > 16) return SOURCE_MESSAGE;
  if (new Set(identities).size !== identities.length) return SOURCE_MESSAGE;
  if (sources.some(sourceIncomplete)) return SOURCE_MESSAGE;
  return null;
}

function factTextIncomplete(fact: RulePackTestFact): boolean {
  return (
    !fact.id.trim() ||
    !fact.rule_id ||
    !fact.dimension.trim() ||
    !fact.value.trim() ||
    !fact.unit.trim() ||
    !fact.source_role.trim()
  );
}

function factCitationCountInvalid(fact: RulePackTestFact): boolean {
  return fact.citations.length === 0 || fact.citations.length > 8;
}

function factIncomplete(fact: RulePackTestFact): boolean {
  return factTextIncomplete(fact) || factCitationCountInvalid(fact);
}

function factSetMessage(facts: RulePackTestFact[]): string | null {
  const totalCitations = facts.reduce(
    (total, fact) => total + fact.citations.length,
    0,
  );
  if (facts.length > 128) return FACT_MESSAGE;
  if (new Set(facts.map((fact) => fact.id)).size !== facts.length)
    return FACT_MESSAGE;
  if (totalCitations > 256) return FACT_MESSAGE;
  if (facts.some(factIncomplete)) return FACT_MESSAGE;
  return null;
}

function sourceBytesMessage(sources: RulePackTestSource[]): string | null {
  const bytes = sources.map(
    (source) => new TextEncoder().encode(source.text).byteLength,
  );
  if (bytes.some((size) => size > 32 * 1024)) {
    return "样本文本超过测试接口上限：单份 32 KiB，总计 256 KiB。";
  }
  if (bytes.reduce((total, size) => total + size, 0) > 256 * 1024) {
    return "样本文本超过测试接口上限：单份 32 KiB，总计 256 KiB。";
  }
  return null;
}

function citationMatches(
  citation: RulePackTestFact["citations"][number],
  sources: RulePackTestSource[],
): boolean {
  const source = sources.find(
    (item) =>
      item.id === citation.source_id &&
      item.version === citation.source_version,
  );
  return (
    source !== undefined &&
    citation.start >= 0 &&
    citation.end > citation.start &&
    Array.from(source.text).slice(citation.start, citation.end).join("") ===
      citation.quote
  );
}

function citationsMatch(
  facts: RulePackTestFact[],
  sources: RulePackTestSource[],
): boolean {
  return facts.every((fact) =>
    fact.citations.every((citation) => citationMatches(citation, sources)),
  );
}

function scopeFieldsMessage(
  businessLine: string,
  scenario: string,
  contractType: string,
  at: string,
): string | null {
  if (
    businessLine.trim() === "" ||
    scenario.trim() === "" ||
    contractType.trim() === "" ||
    Number.isNaN(Date.parse(at))
  ) {
    return "请填写完整的业务范围和有效测试时间。";
  }
  return null;
}

function requestSizeMessage(input: RulePackTestInput): string | null {
  if (
    new TextEncoder().encode(JSON.stringify(input)).byteLength >
    1024 * 1024
  ) {
    return "测试请求超过服务器 1 MiB 请求上限；请缩短文本或减少事实与引用。";
  }
  return null;
}

function validateSampleTest(params: ValidationParams): ValidationOutcome {
  const timeMessage = scopeTimeMessage(params.at, params.manifest);
  if (timeMessage) return { message: timeMessage, input: null };
  const sourceMessage = sourceIdentityMessage(params.sources);
  if (sourceMessage) return { message: sourceMessage, input: null };
  const factMessage = factSetMessage(params.facts);
  if (factMessage) return { message: factMessage, input: null };
  const bytesMessage = sourceBytesMessage(params.sources);
  if (bytesMessage) return { message: bytesMessage, input: null };
  if (!citationsMatch(params.facts, params.sources)) {
    return {
      message: "引用与样本文本的码点范围不一致；请重新选择原文引用。",
      input: null,
    };
  }
  const scopeMessage = scopeFieldsMessage(
    params.businessLine,
    params.scenario,
    params.contractType,
    params.at,
  );
  if (scopeMessage) return { message: scopeMessage, input: null };
  const input: RulePackTestInput = {
    expected_revision: params.revision,
    business_line: params.businessLine,
    scenario: params.scenario,
    contract_type: params.contractType,
    at: params.at,
    sources: params.sources,
    facts: params.facts,
  };
  const sizeMessage = requestSizeMessage(input);
  if (sizeMessage) return { message: sizeMessage, input: null };
  return { message: null, input };
}

function replaceAt<T>(items: T[], index: number, next: T): T[] {
  return items.map((item, itemIndex) => (itemIndex === index ? next : item));
}

function removeAt<T>(items: T[], index: number): T[] {
  return items.filter((_, itemIndex) => itemIndex !== index);
}

function appendCitation(
  facts: RulePackTestFact[],
  factIndex: number,
  source: RulePackTestSource,
  selected: Selection,
): RulePackTestFact[] {
  return facts.map((fact, index) =>
    index === factIndex
      ? {
          ...fact,
          citations: [
            ...fact.citations,
            {
              source_id: source.id,
              source_version: source.version,
              ...selected,
            },
          ],
        }
      : fact,
  );
}

function useErrorFocus(
  formMessage: string | null,
  error: unknown,
  errorRef: RefObject<HTMLParagraphElement | null>,
) {
  useEffect(() => {
    if (error || formMessage) errorRef.current?.focus();
  }, [formMessage, error, errorRef]);
}

function selectionOrUndefined(
  selection: Selection | null,
): Selection | undefined {
  return selection ?? undefined;
}

function scopeSeed(rule: RulePackRule | undefined) {
  return {
    businessLine: rule?.business_scope.business_line ?? "",
    scenario: rule?.business_scope.scenario ?? "",
    contractType: rule?.business_scope.contract_type ?? "",
  };
}

function FormAlerts({
  formMessage,
  error,
  errorRef,
}: {
  formMessage: string | null;
  error: unknown;
  errorRef: RefObject<HTMLParagraphElement | null>;
}) {
  return (
    <>
      {formMessage ? (
        <p className="workflow-error" role="alert" tabIndex={-1} ref={errorRef}>
          {formMessage}
        </p>
      ) : null}
      {error ? (
        <p className="workflow-error" role="alert" tabIndex={-1} ref={errorRef}>
          {errorMessage(error)}
          {isConflict(error)
            ? " 规则包修订已变化；请刷新详情后重新测试。"
            : ""}
        </p>
      ) : null}
    </>
  );
}

function TestSubmitButton({ pending }: { pending: boolean }) {
  return (
    <Button
      aria-label={String(pending ? "正在测试样本" : "测试当前修订")}
      type="primary"
      htmlType="submit"
      disabled={pending}
    >
      {pending ? "正在测试样本" : "测试当前修订"}
    </Button>
  );
}

export function RulePackTestPanel({
  packId,
  version,
  manifest,
  revision,
}: {
  packId: string;
  version: string;
  manifest: RulePackManifest;
  revision: number;
}) {
  const test = useTestRulePack(packId, version);
  const errorRef = useRef<HTMLParagraphElement>(null);
  const seed = scopeSeed(manifest.rules[0]);
  const [businessLine, setBusinessLine] = useState(seed.businessLine);
  const [scenario, setScenario] = useState(seed.scenario);
  const [contractType, setContractType] = useState(seed.contractType);
  const [at, setAt] = useState(manifest.effective_from);
  const [sources, setSources] = useState<RulePackTestSource[]>([]);
  const [facts, setFacts] = useState<RulePackTestFact[]>([]);
  const [selections, setSelections] = useState<
    Record<string, Selection | undefined>
  >({});
  const rowKeySequence = useRef(0);
  const nextRowKey = (kind: string) => `${kind}-${rowKeySequence.current++}`;
  const [sourceKeys, setSourceKeys] = useState<string[]>([]);
  const [factKeys, setFactKeys] = useState<string[]>([]);
  const [formMessage, setFormMessage] = useState<string | null>(null);
  useErrorFocus(formMessage, test.error, errorRef);

  function addSource() {
    const source = newSampleSource(sources.length + 1);
    const sourceKey = nextRowKey("source");
    setSources((current) => [...current, source]);
    setSourceKeys((current) => [...current, sourceKey]);
  }

  function removeSource(index: number) {
    setSources((current) => removeAt(current, index));
    setSourceKeys((current) => removeAt(current, index));
  }

  function addFact() {
    const rule = manifest.rules[0];
    if (!rule) return;
    const fact = newSampleFact(rule, facts.length + 1);
    const factKey = nextRowKey("fact");
    setFacts((current) => [...current, fact]);
    setFactKeys((current) => [...current, factKey]);
  }

  function removeFact(index: number) {
    setFacts((current) => removeAt(current, index));
    setFactKeys((current) => removeAt(current, index));
  }

  function updateSource(index: number, next: RulePackTestSource) {
    setSources((current) => replaceAt(current, index, next));
    setSelections((current) => ({
      ...current,
      [selectionKey(next)]: undefined,
    }));
  }

  function captureSourceSelection(
    source: RulePackTestSource,
    selection: Selection | null,
  ) {
    setSelections((current) => ({
      ...current,
      [selectionKey(source)]: selectionOrUndefined(selection),
    }));
  }

  function appendSelectedCitation(
    factIndex: number,
    source: RulePackTestSource,
  ) {
    const selected = selections[selectionKey(source)];
    if (!selected) return;
    setFacts((current) =>
      appendCitation(current, factIndex, source, selected),
    );
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    test.reset();
    setFormMessage(null);
    const outcome = validateSampleTest({
      manifest,
      businessLine,
      scenario,
      contractType,
      at,
      sources,
      facts,
      revision,
    });
    if (outcome.message) {
      setFormMessage(outcome.message);
      return;
    }
    if (outcome.input) test.mutate(outcome.input);
  }

  return (
    <section className="workflow-panel" aria-labelledby="rulepack-test-title">
      <header className="workflow-panel-head">
        <div>
          <h2 id="rulepack-test-title">样本规则测试</h2>
          <p className="meta">
            仅在内存中测试当前修订 {revision}；样本文本不会写入案件或持久存储。
          </p>
        </div>
      </header>
      <form
        className="workflow-form rulepack-test-form"
        noValidate
        onSubmit={submit}
      >
        <TestScopeFields
          businessLine={businessLine}
          scenario={scenario}
          contractType={contractType}
          at={at}
          onBusinessLine={setBusinessLine}
          onScenario={setScenario}
          onContractType={setContractType}
          onAt={setAt}
        />
        <SourcesSection
          sources={sources}
          sourceKeys={sourceKeys}
          onAdd={addSource}
          onChange={updateSource}
          onSelect={captureSourceSelection}
          onRemove={removeSource}
          disabled={test.isPending}
        />
        <FactsSection
          facts={facts}
          factKeys={factKeys}
          rules={manifest.rules}
          sources={sources}
          selections={selections}
          onAdd={addFact}
          onChange={(index, next) =>
            setFacts((current) => replaceAt(current, index, next))
          }
          onUseSelection={appendSelectedCitation}
          onRemove={removeFact}
          disabled={test.isPending}
        />
        <FormAlerts
          formMessage={formMessage}
          error={test.error}
          errorRef={errorRef}
        />
        <TestSubmitButton pending={test.isPending} />
      </form>
      {test.data ? <TestResult data={test.data} /> : null}
    </section>
  );
}
