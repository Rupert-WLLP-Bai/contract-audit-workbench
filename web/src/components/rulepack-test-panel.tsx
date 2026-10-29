import { Card } from "antd";
import { Button, Input, Select } from "antd";
import { useState, useEffect, useRef, type FormEvent } from "react";
import { errorMessage, isConflict } from "../api/http";
import { useTestRulePack } from "../api/queries";
import type {
  RulePackManifest,
  RulePackRelation,
  RulePackRule,
  RulePackTestFact,
  RulePackTestInput,
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
        Unicode 码点半开区间。
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
  const [citationSourceKey, setCitationSourceKey] = useState("");
  const selectedRule = rules.find((rule) => rule.rule_id === fact.rule_id);
  const eligibleSources = sources.filter(
    (source) => source.role === fact.source_role && source.id !== "",
  );
  const selectedSource =
    eligibleSources.find(
      (source) => selectionKey(source) === citationSourceKey,
    ) ?? eligibleSources[0];
  const selection = selectedSource
    ? selections[selectionKey(selectedSource)]
    : undefined;
  return (
    <Card
      role="group"
      aria-label={"类型化事实 " + String(index + 1)}
      className="rulepack-sample-card"
      title={<>类型化事实 {index + 1}</>}
    >
      <div className="rulepack-fields">
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
            onChange={(event) =>
              onChange({ ...fact, value: event.target.value })
            }
          />
        </label>
        <label className="field" htmlFor={`sample-fact-${index}-unit`}>
          单位
          <Input
            disabled={disabled}
            id={`sample-fact-${index}-unit`}
            required
            value={fact.unit}
            onChange={(event) =>
              onChange({ ...fact, unit: event.target.value })
            }
          />
        </label>
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
              ...[
                ...new Set(
                  [
                    ...sources.map((source) => source.role),
                    ...(selectedRule?.required_fact.source_roles ?? []),
                  ].filter(Boolean),
                ),
              ].map((role) => ({ value: role, label: role })),
            ]}
          />
        </label>
        <label className="field" htmlFor={`sample-fact-${index}-window`}>
          测量窗口（可选）
          <Input
            disabled={disabled}
            id={`sample-fact-${index}-window`}
            value={fact.measurement_window ?? ""}
            onChange={(event) =>
              onChange({
                ...fact,
                measurement_window: event.target.value || undefined,
              })
            }
          />
        </label>
        <label className="field" htmlFor={`sample-fact-${index}-timezone`}>
          时区（可选）
          <Input
            disabled={disabled}
            id={`sample-fact-${index}-timezone`}
            value={fact.time_zone ?? ""}
            onChange={(event) =>
              onChange({ ...fact, time_zone: event.target.value || undefined })
            }
          />
        </label>
        <label className="field" htmlFor={`sample-fact-${index}-calendar`}>
          日历（可选）
          <Input
            disabled={disabled}
            id={`sample-fact-${index}-calendar`}
            value={fact.calendar ?? ""}
            onChange={(event) =>
              onChange({ ...fact, calendar: event.target.value || undefined })
            }
          />
        </label>
        <label className="field" htmlFor={`sample-fact-${index}-outage`}>
          故障口径（可选）
          <Input
            disabled={disabled}
            id={`sample-fact-${index}-outage`}
            value={fact.outage_definition ?? ""}
            onChange={(event) =>
              onChange({
                ...fact,
                outage_definition: event.target.value || undefined,
              })
            }
          />
        </label>
        <label className="field" htmlFor={`sample-fact-${index}-basis`}>
          计算基数（可选）
          <Input
            disabled={disabled}
            id={`sample-fact-${index}-basis`}
            value={fact.basis ?? ""}
            onChange={(event) =>
              onChange({ ...fact, basis: event.target.value || undefined })
            }
          />
        </label>
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
        <label className="field" htmlFor={`sample-fact-${index}-applicable`}>
          适用性
          <Select
            disabled={disabled}
            id={`sample-fact-${index}-applicable`}
            aria-label="适用性"
            value={
              fact.applicable === undefined
                ? "unset"
                : fact.applicable
                  ? "yes"
                  : "no"
            }
            onChange={(value) => {
              onChange({
                ...fact,
                applicable: value === "unset" ? undefined : value === "yes",
              });
            }}
            options={[
              { label: "未指定", value: "unset" },
              { label: "适用", value: "yes" },
              { label: "不适用", value: "no" },
            ]}
          />
        </label>
      </div>
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
          <ul className="rulepack-result-list">
            {fact.citations.map((citation, citationIndex) => (
              <li
                key={`${citation.source_id}:${citation.start}:${citationIndex}`}
              >
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
        )}
        {selectedSource ? (
          <p className="meta">
            当前来源 {selectedSource.id} v{selectedSource.version} 的选区：
            {selection
              ? `码点 [${selection.start},${selection.end})`
              : "未选择文本"}
          </p>
        ) : (
          <p className="meta">
            先添加角色为“{fact.source_role || "所选事实角色"}”的样本材料。
          </p>
        )}
        <Button
          aria-label={"添加当前选区引用"}
          htmlType="button"
          disabled={
            disabled ||
            !selectedSource ||
            !selection ||
            fact.citations.length >= 8
          }
          onClick={() => selectedSource && onUseSelection(selectedSource)}
        >
          添加当前选区引用
        </Button>
      </section>
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
  const firstRule = manifest.rules[0];
  const [businessLine, setBusinessLine] = useState(
    firstRule?.business_scope.business_line ?? "",
  );
  const [scenario, setScenario] = useState(
    firstRule?.business_scope.scenario ?? "",
  );
  const [contractType, setContractType] = useState(
    firstRule?.business_scope.contract_type ?? "",
  );
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
  useEffect(() => {
    if (test.error || formMessage) errorRef.current?.focus();
  }, [formMessage, test.error]);

  function addSource() {
    const source = newSampleSource(sources.length + 1);
    const sourceKey = nextRowKey("source");
    setSources((current) => [...current, source]);
    setSourceKeys((current) => [...current, sourceKey]);
  }

  function removeSource(index: number) {
    setSources((current) =>
      current.filter((_, itemIndex) => itemIndex !== index),
    );
    setSourceKeys((current) =>
      current.filter((_, itemIndex) => itemIndex !== index),
    );
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
    setFacts((current) =>
      current.filter((_, itemIndex) => itemIndex !== index),
    );
    setFactKeys((current) =>
      current.filter((_, itemIndex) => itemIndex !== index),
    );
  }

  function updateSource(index: number, next: RulePackTestSource) {
    setSources((current) =>
      current.map((source, itemIndex) => (itemIndex === index ? next : source)),
    );
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
      [selectionKey(source)]: selection ?? undefined,
    }));
  }

  function appendSelectedCitation(
    factIndex: number,
    source: RulePackTestSource,
  ) {
    const selected = selections[selectionKey(source)];
    if (!selected) return;
    setFacts((current) =>
      current.map((fact, index) =>
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
      ),
    );
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    test.reset();
    setFormMessage(null);
    if (
      Date.parse(at) < Date.parse(manifest.effective_from) ||
      Date.parse(at) >= Date.parse(manifest.effective_until)
    ) {
      setFormMessage("测试时间必须位于该版本的有效期内。");
      return;
    }
    const sourceIdentities = sources.map((source) =>
      JSON.stringify([source.id, source.version]),
    );
    const sourceBytes = sources.map(
      (source) => new TextEncoder().encode(source.text).byteLength,
    );
    const totalCitationCount = facts.reduce(
      (total, fact) => total + fact.citations.length,
      0,
    );
    const factIdentities = facts.map((fact) => fact.id);
    if (
      sources.length > 16 ||
      new Set(sourceIdentities).size !== sourceIdentities.length ||
      sources.some(
        (source) =>
          !source.id.trim() ||
          !source.version.trim() ||
          !source.role.trim() ||
          !source.text.trim(),
      )
    ) {
      setFormMessage(
        "样本材料最多 16 份；每份都必须填写 ID、版本、角色和文本，且 ID 与版本组合不能重复。",
      );
      return;
    }
    if (
      facts.length > 128 ||
      new Set(factIdentities).size !== factIdentities.length ||
      totalCitationCount > 256 ||
      facts.some(
        (fact) =>
          !fact.id.trim() ||
          !fact.rule_id ||
          !fact.dimension.trim() ||
          !fact.value.trim() ||
          !fact.unit.trim() ||
          !fact.source_role.trim() ||
          fact.citations.length === 0 ||
          fact.citations.length > 8,
      )
    ) {
      setFormMessage(
        "类型化事实最多 128 条、总引用最多 256 条；每条事实 ID 唯一且需提供 1–8 条来源引用。",
      );
      return;
    }
    if (
      sourceBytes.some((size) => size > 32 * 1024) ||
      sourceBytes.reduce((total, size) => total + size, 0) > 256 * 1024
    ) {
      setFormMessage("样本文本超过测试接口上限：单份 32 KiB，总计 256 KiB。");
      return;
    }
    const selectedQuotesMatch = facts.every((fact) =>
      fact.citations.every((citation) => {
        const source = sources.find(
          (item) =>
            item.id === citation.source_id &&
            item.version === citation.source_version,
        );
        return (
          source !== undefined &&
          citation.start >= 0 &&
          citation.end > citation.start &&
          Array.from(source.text)
            .slice(citation.start, citation.end)
            .join("") === citation.quote
        );
      }),
    );
    if (!selectedQuotesMatch) {
      setFormMessage("引用与样本文本的码点范围不一致；请重新选择原文引用。");
      return;
    }
    if (
      businessLine.trim() === "" ||
      scenario.trim() === "" ||
      contractType.trim() === "" ||
      Number.isNaN(Date.parse(at))
    ) {
      setFormMessage("请填写完整的业务范围和有效测试时间。");
      return;
    }
    const input: RulePackTestInput = {
      expected_revision: revision,
      business_line: businessLine,
      scenario,
      contract_type: contractType,
      at,
      sources,
      facts,
    };
    if (
      new TextEncoder().encode(JSON.stringify(input)).byteLength >
      1024 * 1024
    ) {
      setFormMessage(
        "测试请求超过服务器 1 MiB 请求上限；请缩短文本或减少事实与引用。",
      );
      return;
    }
    test.mutate(input);
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
        <div className="rulepack-fields">
          <label className="field" htmlFor="rulepack-test-business-line">
            业务线
            <Input
              id="rulepack-test-business-line"
              required
              value={businessLine}
              onChange={(event) => setBusinessLine(event.target.value)}
            />
          </label>
          <label className="field" htmlFor="rulepack-test-scenario">
            场景
            <Input
              id="rulepack-test-scenario"
              required
              value={scenario}
              onChange={(event) => setScenario(event.target.value)}
            />
          </label>
          <label className="field" htmlFor="rulepack-test-contract-type">
            合同类型
            <Input
              id="rulepack-test-contract-type"
              required
              value={contractType}
              onChange={(event) => setContractType(event.target.value)}
            />
          </label>
          <label className="field" htmlFor="rulepack-test-at">
            测试时点（本地时区）
            <Input
              id="rulepack-test-at"
              type="datetime-local"
              required
              value={toLocalInputValue(at)}
              onChange={(event) => setAt(toUtcInstant(event.target.value))}
            />
          </label>
        </div>
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
              onClick={addSource}
            >
              添加样本材料
            </Button>
          </header>
          {sources.map((source, index) => (
            <SourceEditor
              key={sourceKeys[index] ?? index}
              source={source}
              index={index}
              onChange={(next) => updateSource(index, next)}
              onSelect={captureSourceSelection}
              onRemove={() => removeSource(index)}
              disabled={test.isPending}
            />
          ))}
          {sources.length === 0 ? (
            <p>添加用于本次测试的样本文本；不要引用真实案件材料。</p>
          ) : null}
        </section>
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
              disabled={manifest.rules.length === 0 || facts.length >= 128}
              onClick={addFact}
            >
              添加事实
            </Button>
          </header>
          {facts.map((fact, index) => (
            <FactEditor
              key={factKeys[index] ?? index}
              fact={fact}
              index={index}
              rules={manifest.rules}
              sources={sources}
              selections={selections}
              onChange={(next) =>
                setFacts((current) =>
                  current.map((item, itemIndex) =>
                    itemIndex === index ? next : item,
                  ),
                )
              }
              onUseSelection={(source) => appendSelectedCitation(index, source)}
              onRemove={() => removeFact(index)}
              disabled={test.isPending}
            />
          ))}
          {facts.length === 0 ? (
            <p>可先不提供事实，运行一次明确的缺证样本测试。</p>
          ) : null}
        </section>
        {formMessage ? (
          <p
            className="workflow-error"
            role="alert"
            tabIndex={-1}
            ref={errorRef}
          >
            {formMessage}
          </p>
        ) : null}
        {test.error ? (
          <p
            className="workflow-error"
            role="alert"
            tabIndex={-1}
            ref={errorRef}
          >
            {errorMessage(test.error)}
            {isConflict(test.error)
              ? " 规则包修订已变化；请刷新详情后重新测试。"
              : ""}
          </p>
        ) : null}
        <Button
          aria-label={String(test.isPending ? "正在测试样本" : "测试当前修订")}
          type="primary"
          htmlType="submit"
          disabled={test.isPending}
        >
          {test.isPending ? "正在测试样本" : "测试当前修订"}
        </Button>
      </form>
      {test.data ? (
        <section
          className="rulepack-test-result"
          aria-labelledby="rulepack-test-result-title"
        >
          <h3 id="rulepack-test-result-title">测试结果</h3>
          <p role="status">
            规则包 {test.data.report.pack_id} v{test.data.report.pack_version}，
            测试修订 {test.data.revision}，内容摘要{" "}
            <code className="digest">{test.data.content_hash}</code>
          </p>
          <p>
            总关系：
            {test.data.report.relations
              .map((relation) => RELATION_LABELS[relation])
              .join("、") || "没有适用规则"}
          </p>
          {test.data.report.results.length === 0 ? (
            <p>当前业务范围没有匹配规则。</p>
          ) : (
            <ul className="rulepack-result-list">
              {test.data.report.results.map((result, index) => (
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
      ) : null}
    </section>
  );
}
