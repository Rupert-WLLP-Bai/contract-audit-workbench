import { Card } from "antd";
import { Button, Checkbox, Input, Select } from "antd";
import { useRef, useState } from "react";
import type {
  RulePackAuthoritySource,
  RulePackLegalAuthority,
  RulePackManifestInput,
  RulePackRule,
} from "../api/rulepacks";
import { RulePackLegalAuthorityEditor } from "./rulepack-legal-authority-editor";
import { toLocalInputValue, toUtcInstant } from "./local-datetime";
import { OptionalFactField } from "./optional-fact-field";

const COMPARATORS = [
  ["minimum_bandwidth", "最低带宽"],
  ["monthly_sla", "自然月可用率"],
  ["advance_payment_ceiling", "预付款比例上限"],
] as const;
const REQUIRED_CAPABILITIES: Record<string, true> = {
  "dedicated_line.minimum_bandwidth": true,
  "dedicated_line.monthly_availability": true,
  "payment.advance_ratio.ceiling": true,
};

type RulePatch = (patch: Partial<RulePackRule>) => void;

function isCoreCapability(rule: RulePackRule): boolean {
  return Object.hasOwn(REQUIRED_CAPABILITIES, rule.capability_id);
}

function blankRule(tenantId: string): RulePackRule {
  return {
    rule_id: "",
    version: "1",
    domain: "",
    capability_id: "",
    mandatory: true,
    business_scope: { business_line: "", scenario: "", contract_type: "" },
    tenant_id: tenantId,
    comparator: "minimum_bandwidth",
    threshold: { value: "", unit: "Mbit/s" },
    required_fact: {
      dimension: "",
      source_roles: [],
      object_required: true,
      unit_family: "",
      exclusions: null,
    },
    authority_sources: [{ id: "", version: "", kind: "" }],
  };
}
export function createEmptyRulePackDraft(
  tenantId: string,
): RulePackManifestInput {
  return {
    schema_version: "rulepack/v1",
    id: "",
    version: "",
    tenant_id: tenantId,
    effective_from: "",
    effective_until: "",
    rules: [blankRule(tenantId)],
  };
}

function updateRule(
  value: RulePackManifestInput,
  onChange: (next: RulePackManifestInput) => void,
  index: number,
  patch: Partial<RulePackRule>,
) {
  onChange({
    ...value,
    rules: value.rules.map((rule, itemIndex) =>
      itemIndex === index ? { ...rule, ...patch } : rule,
    ),
  });
}

function updateAuthority(
  value: RulePackManifestInput,
  onChange: (next: RulePackManifestInput) => void,
  ruleIndex: number,
  sourceIndex: number,
  patch: Partial<RulePackAuthoritySource>,
) {
  onChange({
    ...value,
    rules: value.rules.map((rule, index) =>
      index !== ruleIndex
        ? rule
        : {
            ...rule,
            authority_sources: rule.authority_sources.map((source, index) =>
              index === sourceIndex ? { ...source, ...patch } : source,
            ),
          },
    ),
  });
}

function splitLines(value: string): string[] {
  return value
    .split("\n")
    .map((item) => item.trim())
    .filter(Boolean);
}

function comparatorDefaults(
  comparator: RulePackRule["comparator"],
  rule: RulePackRule,
): Partial<RulePackRule> {
  const defaultUnit =
    comparator === "minimum_bandwidth"
      ? "Mbit/s"
      : comparator === "monthly_sla"
        ? "percent"
        : "ratio";
  return {
    comparator,
    threshold: {
      ...rule.threshold,
      unit: rule.threshold.unit || defaultUnit,
    },
    required_fact: {
      ...rule.required_fact,
      exclusions:
        comparator === "monthly_sla"
          ? (rule.required_fact.exclusions ?? [])
          : null,
    },
  };
}

function RuleIdentitySection({
  value,
  tenantId,
  lockedIdentity,
  lockedPackId,
  disabled,
  onChange,
}: {
  value: RulePackManifestInput;
  tenantId: string;
  lockedIdentity: boolean;
  lockedPackId: boolean;
  disabled: boolean;
  onChange: (next: RulePackManifestInput) => void;
}) {
  return (
    <section
      className="rulepack-editor-section"
      aria-labelledby="rulepack-identity-heading"
    >
      <h2 id="rulepack-identity-heading">规则包身份与适用期</h2>
      <div className="rulepack-fields">
        <label className="field" htmlFor="rulepack-id">
          规则包 ID
          <Input
            id="rulepack-id"
            required
            value={value.id}
            readOnly={lockedIdentity || lockedPackId}
            onChange={(event) =>
              onChange({ ...value, id: event.target.value })
            }
          />
        </label>
        <label className="field" htmlFor="rulepack-version">
          版本
          <Input
            id="rulepack-version"
            required
            value={value.version}
            readOnly={lockedIdentity}
            onChange={(event) =>
              onChange({ ...value, version: event.target.value })
            }
          />
        </label>
        <label className="field" htmlFor="rulepack-tenant">
          可信会话租户
          <Input
            id="rulepack-tenant"
            value={tenantId}
            readOnly
            aria-describedby="rulepack-tenant-hint"
          />
        </label>
        <p className="meta" id="rulepack-tenant-hint">
          租户由服务器从当前管理会话确认；这里仅显示身份，不接受跨租户选择。
        </p>
        <label className="field" htmlFor="rulepack-effective-from">
          生效时间（本地时区）
          <Input
            id="rulepack-effective-from"
            type="datetime-local"
            required
            value={toLocalInputValue(value.effective_from)}
            disabled={disabled}
            onChange={(event) =>
              onChange({
                ...value,
                effective_from: toUtcInstant(event.target.value),
              })
            }
          />
        </label>
        <label className="field" htmlFor="rulepack-effective-until">
          失效时间（本地时区，结束时刻不含）
          <Input
            id="rulepack-effective-until"
            type="datetime-local"
            required
            value={toLocalInputValue(value.effective_until)}
            disabled={disabled}
            onChange={(event) =>
              onChange({
                ...value,
                effective_until: toUtcInstant(event.target.value),
              })
            }
          />
        </label>
        <p className="meta">
          提交时按 UTC RFC 3339
          保存。适用时间范围为开始时刻（含）至结束时刻（不含）。
        </p>
      </div>
    </section>
  );
}

function RuleBasicsFields({
  rule,
  index,
  tenantId,
  disabled,
  onPatch,
}: {
  rule: RulePackRule;
  index: number;
  tenantId: string;
  disabled: boolean;
  onPatch: RulePatch;
}) {
  return (
    <>
      <label className="field" htmlFor={`rule-${index}-id`}>
        规则 ID
        <Input
          disabled={disabled}
          id={`rule-${index}-id`}
          required
          value={rule.rule_id}
          onChange={(event) => onPatch({ rule_id: event.target.value })}
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-version`}>
        规则版本
        <Input
          disabled={disabled}
          id={`rule-${index}-version`}
          required
          value={rule.version}
          onChange={(event) => onPatch({ version: event.target.value })}
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-domain`}>
        检查域
        <Input
          disabled={disabled}
          id={`rule-${index}-domain`}
          required
          value={rule.domain}
          onChange={(event) => onPatch({ domain: event.target.value })}
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-capability`}>
        能力 ID
        <Input
          disabled={disabled}
          id={`rule-${index}-capability`}
          required
          value={rule.capability_id}
          onChange={(event) => onPatch({ capability_id: event.target.value })}
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-business-line`}>
        业务线
        <Input
          disabled={disabled}
          id={`rule-${index}-business-line`}
          required
          value={rule.business_scope.business_line}
          onChange={(event) =>
            onPatch({
              business_scope: {
                ...rule.business_scope,
                business_line: event.target.value,
              },
            })
          }
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-scenario`}>
        场景
        <Input
          disabled={disabled}
          id={`rule-${index}-scenario`}
          required
          value={rule.business_scope.scenario}
          onChange={(event) =>
            onPatch({
              business_scope: {
                ...rule.business_scope,
                scenario: event.target.value,
              },
            })
          }
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-contract-type`}>
        合同类型
        <Input
          disabled={disabled}
          id={`rule-${index}-contract-type`}
          required
          value={rule.business_scope.contract_type}
          onChange={(event) =>
            onPatch({
              business_scope: {
                ...rule.business_scope,
                contract_type: event.target.value,
              },
            })
          }
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-tenant`}>
        规则租户
        <Input
          disabled={disabled}
          id={`rule-${index}-tenant`}
          value={tenantId}
          readOnly
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-comparator`}>
        比较器
        <Select
          disabled={disabled}
          id={`rule-${index}-comparator`}
          aria-label="比较器"
          value={rule.comparator}
          onChange={(comparator: RulePackRule["comparator"]) =>
            onPatch(comparatorDefaults(comparator, rule))
          }
          options={COMPARATORS.map(([id, label]) => ({
            value: id,
            label: `${label}，${id}`,
          }))}
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-threshold`}>
        比较阈值
        <Input
          disabled={disabled}
          id={`rule-${index}-threshold`}
          required
          value={rule.threshold.value}
          onChange={(event) =>
            onPatch({
              threshold: { ...rule.threshold, value: event.target.value },
            })
          }
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-unit`}>
        阈值单位
        <Input
          disabled={disabled}
          id={`rule-${index}-unit`}
          required
          value={rule.threshold.unit}
          onChange={(event) =>
            onPatch({
              threshold: { ...rule.threshold, unit: event.target.value },
            })
          }
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-basis`}>
        计算基数（可选）
        <Input
          disabled={disabled}
          id={`rule-${index}-basis`}
          value={rule.threshold.basis ?? ""}
          onChange={(event) =>
            onPatch({
              threshold: {
                ...rule.threshold,
                basis: event.target.value || undefined,
              },
            })
          }
        />
      </label>
    </>
  );
}

function displayOptional(value: string | undefined): string {
  return value ?? "";
}

function optionalValue(value: string): string | undefined {
  return value || undefined;
}

function RuleOptionalFactFields({
  rule,
  index,
  disabled,
  onPatch,
}: {
  rule: RulePackRule;
  index: number;
  disabled: boolean;
  onPatch: RulePatch;
}) {
  const fact = rule.required_fact;
  const optional = (key: keyof typeof fact) => (value: string) =>
    onPatch({ required_fact: { ...fact, [key]: optionalValue(value) } });
  return (
    <>
      <OptionalFactField
        id={`rule-${index}-measurement-window`}
        label="测量窗口（可选）"
        value={displayOptional(fact.measurement_window)}
        disabled={disabled}
        onChange={optional("measurement_window")}
      />
      <OptionalFactField
        id={`rule-${index}-time-zone`}
        label="时区（可选）"
        value={displayOptional(fact.time_zone)}
        disabled={disabled}
        onChange={optional("time_zone")}
      />
      <OptionalFactField
        id={`rule-${index}-calendar`}
        label="日历（可选）"
        value={displayOptional(fact.calendar)}
        disabled={disabled}
        onChange={optional("calendar")}
      />
      <OptionalFactField
        id={`rule-${index}-outage`}
        label="故障口径（可选）"
        value={displayOptional(fact.outage_definition)}
        disabled={disabled}
        onChange={optional("outage_definition")}
      />
      <OptionalFactField
        id={`rule-${index}-fact-basis`}
        label="事实计算基数（可选）"
        value={displayOptional(fact.basis)}
        disabled={disabled}
        onChange={optional("basis")}
      />
    </>
  );
}

function RuleFactFields({
  rule,
  index,
  disabled,
  onPatch,
}: {
  rule: RulePackRule;
  index: number;
  disabled: boolean;
  onPatch: RulePatch;
}) {
  return (
    <>
      <label className="field" htmlFor={`rule-${index}-dimension`}>
        所需事实维度
        <Input
          disabled={disabled}
          id={`rule-${index}-dimension`}
          required
          value={rule.required_fact.dimension}
          onChange={(event) =>
            onPatch({
              required_fact: {
                ...rule.required_fact,
                dimension: event.target.value,
              },
            })
          }
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-roles`}>
        允许材料角色（每行一项）
        <Input.TextArea
          disabled={disabled}
          id={`rule-${index}-roles`}
          required
          value={rule.required_fact.source_roles.join("\n")}
          onChange={(event) =>
            onPatch({
              required_fact: {
                ...rule.required_fact,
                source_roles: splitLines(event.target.value),
              },
            })
          }
        />
      </label>
      <label className="field" htmlFor={`rule-${index}-unit-family`}>
        单位族
        <Input
          disabled={disabled}
          id={`rule-${index}-unit-family`}
          required
          value={rule.required_fact.unit_family}
          onChange={(event) =>
            onPatch({
              required_fact: {
                ...rule.required_fact,
                unit_family: event.target.value,
              },
            })
          }
        />
      </label>
      <RuleOptionalFactFields
        rule={rule}
        index={index}
        disabled={disabled}
        onPatch={onPatch}
      />
      <label className="field" htmlFor={`rule-${index}-exclusions`}>
        排除项（每行一项）
        <Input.TextArea
          id={`rule-${index}-exclusions`}
          disabled={disabled || rule.comparator !== "monthly_sla"}
          value={(rule.required_fact.exclusions ?? []).join("\n")}
          onChange={(event) =>
            onPatch({
              required_fact: {
                ...rule.required_fact,
                exclusions: splitLines(event.target.value),
              },
            })
          }
        />
      </label>
    </>
  );
}

function RuleCheckboxes({
  rule,
  index,
  disabled,
  onPatch,
}: {
  rule: RulePackRule;
  index: number;
  disabled: boolean;
  onPatch: RulePatch;
}) {
  const core = isCoreCapability(rule);
  return (
    <>
      <Checkbox
        id={`rule-${index}-mandatory`}
        checked={rule.mandatory}
        disabled={disabled || (core && rule.mandatory)}
        onChange={(event) =>
          onPatch({ mandatory: core ? true : event.target.checked })
        }
        className="workflow-check"
      >
        此项为必检规则
        {core ? "（专线核心能力必须保留必检）" : ""}
      </Checkbox>
      <Checkbox
        disabled={disabled}
        checked={rule.required_fact.object_required}
        onChange={(event) =>
          onPatch({
            required_fact: {
              ...rule.required_fact,
              object_required: event.target.checked,
            },
          })
        }
        className="workflow-check"
      >
        要求精确业务对象
      </Checkbox>
    </>
  );
}

function AuthorityRow({
  ruleIndex,
  sourceIndex,
  source,
  disabled,
  onUpdate,
  onRemove,
}: {
  ruleIndex: number;
  sourceIndex: number;
  source: RulePackAuthoritySource;
  disabled: boolean;
  onUpdate: (patch: Partial<RulePackAuthoritySource>) => void;
  onRemove: () => void;
}) {
  return (
    <div className="rulepack-source-row">
      <label
        className="field"
        htmlFor={`rule-${ruleIndex}-authority-${sourceIndex}-id`}
      >
        来源 ID
        <Input
          disabled={disabled}
          id={`rule-${ruleIndex}-authority-${sourceIndex}-id`}
          required
          value={source.id}
          onChange={(event) => onUpdate({ id: event.target.value })}
        />
      </label>
      <label
        className="field"
        htmlFor={`rule-${ruleIndex}-authority-${sourceIndex}-version`}
      >
        来源版本
        <Input
          disabled={disabled}
          id={`rule-${ruleIndex}-authority-${sourceIndex}-version`}
          required
          value={source.version}
          onChange={(event) => onUpdate({ version: event.target.value })}
        />
      </label>
      <label
        className="field"
        htmlFor={`rule-${ruleIndex}-authority-${sourceIndex}-kind`}
      >
        来源类型
        <Input
          disabled={disabled}
          id={`rule-${ruleIndex}-authority-${sourceIndex}-kind`}
          required
          value={source.kind}
          onChange={(event) => onUpdate({ kind: event.target.value })}
        />
      </label>
      <Button
        disabled={disabled}
        type="text"
        htmlType="button"
        aria-label={`移除规则 ${ruleIndex + 1} 的来源 ${sourceIndex + 1}`}
        onClick={onRemove}
      >
        移除来源
      </Button>
    </div>
  );
}

function AuthoritySection({
  ruleIndex,
  sources,
  keys,
  disabled,
  onAdd,
  onUpdate,
  onRemove,
}: {
  ruleIndex: number;
  sources: RulePackAuthoritySource[];
  keys: string[];
  disabled: boolean;
  onAdd: () => void;
  onUpdate: (sourceIndex: number, patch: Partial<RulePackAuthoritySource>) => void;
  onRemove: (sourceIndex: number) => void;
}) {
  return (
    <section
      className="rulepack-authorities"
      aria-labelledby={`rule-${ruleIndex}-authority-heading`}
    >
      <header className="workflow-panel-head">
        <h3 id={`rule-${ruleIndex}-authority-heading`} tabIndex={-1}>
          规则依据来源
        </h3>
        <Button
          aria-label={"添加来源"}
          disabled={disabled}
          htmlType="button"
          onClick={onAdd}
        >
          添加来源
        </Button>
      </header>
      {sources.map((source, sourceIndex) => (
        <AuthorityRow
          key={keys[sourceIndex] ?? sourceIndex}
          ruleIndex={ruleIndex}
          sourceIndex={sourceIndex}
          source={source}
          disabled={disabled}
          onUpdate={(patch) => onUpdate(sourceIndex, patch)}
          onRemove={() => onRemove(sourceIndex)}
        />
      ))}
    </section>
  );
}

function RuleCard({
  rule,
  index,
  tenantId,
  disabled,
  authorityKeys,
  canRemove,
  onPatch,
  onAddAuthority,
  onUpdateAuthority,
  onRemoveAuthority,
  onAddLegalAuthority,
  onRemoveLegalAuthority,
  onRemoveRule,
}: {
  rule: RulePackRule;
  index: number;
  tenantId: string;
  disabled: boolean;
  authorityKeys: string[];
  canRemove: boolean;
  onPatch: RulePatch;
  onAddAuthority: () => void;
  onUpdateAuthority: (
    sourceIndex: number,
    patch: Partial<RulePackAuthoritySource>,
  ) => void;
  onRemoveAuthority: (sourceIndex: number) => void;
  onAddLegalAuthority: (authority: RulePackLegalAuthority) => void;
  onRemoveLegalAuthority: (index: number) => void;
  onRemoveRule: () => void;
}) {
  return (
    <Card
      role="group"
      aria-label={
        "规则 " + String(index + 1) + String(rule.rule_id ? `，${rule.rule_id}` : "")
      }
      className="rulepack-rule"
      title={
        <>
          规则 {index + 1}
          {rule.rule_id ? `，${rule.rule_id}` : ""}
        </>
      }
    >
      <div className="rulepack-fields">
        <RuleBasicsFields
          rule={rule}
          index={index}
          tenantId={tenantId}
          disabled={disabled}
          onPatch={onPatch}
        />
        <RuleFactFields rule={rule} index={index} disabled={disabled} onPatch={onPatch} />
        <RuleCheckboxes rule={rule} index={index} disabled={disabled} onPatch={onPatch} />
      </div>
      <AuthoritySection
        ruleIndex={index}
        sources={rule.authority_sources}
        keys={authorityKeys}
        disabled={disabled}
        onAdd={onAddAuthority}
        onUpdate={onUpdateAuthority}
        onRemove={onRemoveAuthority}
      />
      <RulePackLegalAuthorityEditor
        value={rule.legal_authorities ?? []}
        disabled={disabled}
        onAdd={onAddLegalAuthority}
        onRemove={onRemoveLegalAuthority}
      />
      {canRemove ? (
        <Button
          aria-label={"移除规则 " + String(index + 1)}
          disabled={disabled}
          type="text"
          htmlType="button"
          onClick={onRemoveRule}
        >
          移除规则 {index + 1}
        </Button>
      ) : null}
    </Card>
  );
}

export function RulePackManifestEditor({
  value,
  tenantId,
  lockedIdentity = false,
  lockedPackId = false,
  disabled = false,
  onChange,
}: {
  value: RulePackManifestInput;
  tenantId: string;
  lockedIdentity?: boolean;
  lockedPackId?: boolean;
  disabled?: boolean;
  onChange: (next: RulePackManifestInput) => void;
}) {
  const keySequence = useRef(0);
  const nextLocalKey = (kind: string) => `${kind}-${keySequence.current++}`;
  const [ruleKeys, setRuleKeys] = useState(() =>
    value.rules.map(() => nextLocalKey("rule")),
  );
  const [authorityKeys, setAuthorityKeys] = useState(() =>
    value.rules.map((rule) =>
      rule.authority_sources.map(() => nextLocalKey("source")),
    ),
  );

  function addRule() {
    const ruleKey = nextLocalKey("rule");
    const sourceKey = nextLocalKey("source");
    setRuleKeys((current) => [...current, ruleKey]);
    setAuthorityKeys((current) => [...current, [sourceKey]]);
    onChange({ ...value, rules: [...value.rules, blankRule(tenantId)] });
  }

  function removeRule(ruleIndex: number) {
    setRuleKeys((current) => current.filter((_, index) => index !== ruleIndex));
    setAuthorityKeys((current) =>
      current.filter((_, index) => index !== ruleIndex),
    );
    onChange({
      ...value,
      rules: value.rules.filter((_, index) => index !== ruleIndex),
    });
  }

  function addAuthority(ruleIndex: number) {
    const sourceKey = nextLocalKey("source");
    setAuthorityKeys((current) =>
      current.map((keys, index) =>
        index === ruleIndex ? [...keys, sourceKey] : keys,
      ),
    );
    updateRule(value, onChange, ruleIndex, {
      authority_sources: [
        ...value.rules[ruleIndex]!.authority_sources,
        { id: "", version: "", kind: "" },
      ],
    });
  }

  function removeAuthority(ruleIndex: number, sourceIndex: number) {
    setAuthorityKeys((current) =>
      current.map((keys, index) =>
        index === ruleIndex
          ? keys.filter((_, itemIndex) => itemIndex !== sourceIndex)
          : keys,
      ),
    );
    updateRule(value, onChange, ruleIndex, {
      authority_sources: value.rules[ruleIndex]!.authority_sources.filter(
        (_, index) => index !== sourceIndex,
      ),
    });
  }

  function addLegalAuthority(ruleIndex: number, authority: RulePackLegalAuthority) {
    const rule = value.rules[ruleIndex];
    if (!rule) return;
    const sourceExists = rule.authority_sources.some((source) =>
      source.id === authority.source_id && source.version === authority.norm_version && source.kind === "normative",
    );
    const sources = sourceExists
      ? rule.authority_sources
      : [...rule.authority_sources, { id: authority.source_id, version: authority.norm_version, kind: "normative" }];
    if (!sourceExists) {
      const key = nextLocalKey("source");
      setAuthorityKeys((current) => current.map((keys, index) => index === ruleIndex ? [...keys, key] : keys));
    }
    updateRule(value, onChange, ruleIndex, {
      authority_sources: sources,
      legal_authorities: [...(rule.legal_authorities ?? []), authority],
    });
  }

  function removeLegalAuthority(ruleIndex: number, authorityIndex: number) {
    const rule = value.rules[ruleIndex];
    if (!rule) return;
    updateRule(value, onChange, ruleIndex, {
      legal_authorities: (rule.legal_authorities ?? []).filter((_, index) => index !== authorityIndex),
    });
  }

  return (
    <div className="rulepack-editor">
      <RuleIdentitySection
        value={value}
        tenantId={tenantId}
        lockedIdentity={lockedIdentity}
        lockedPackId={lockedPackId}
        disabled={disabled}
        onChange={onChange}
      />
      <section
        className="rulepack-editor-section"
        aria-labelledby="rulepack-rules-heading"
      >
        <div className="workflow-panel-head">
          <div>
            <h2 id="rulepack-rules-heading" tabIndex={-1}>
              比较规则
            </h2>
            <p className="meta">
              仅支持服务器内置的三个确定性比较器；不能上传代码或表达式。
            </p>
          </div>
          <Button
            aria-label={"添加规则"}
            htmlType="button"
            disabled={disabled}
            onClick={addRule}
          >
            添加规则
          </Button>
        </div>
        {value.rules.map((rule, ruleIndex) => (
          <RuleCard
            key={ruleKeys[ruleIndex] ?? ruleIndex}
            rule={rule}
            index={ruleIndex}
            tenantId={tenantId}
            disabled={disabled}
            authorityKeys={authorityKeys[ruleIndex] ?? []}
            canRemove={value.rules.length > 1 && !isCoreCapability(rule)}
            onPatch={(patch) =>
              updateRule(value, onChange, ruleIndex, patch)
            }
            onAddAuthority={() => addAuthority(ruleIndex)}
            onUpdateAuthority={(sourceIndex, patch) =>
              updateAuthority(value, onChange, ruleIndex, sourceIndex, patch)
            }
            onRemoveAuthority={(sourceIndex) =>
              removeAuthority(ruleIndex, sourceIndex)
            }
            onAddLegalAuthority={(authority) =>
              addLegalAuthority(ruleIndex, authority)
            }
            onRemoveLegalAuthority={(authorityIndex) =>
              removeLegalAuthority(ruleIndex, authorityIndex)
            }
            onRemoveRule={() => removeRule(ruleIndex)}
          />
        ))}
      </section>
    </div>
  );
}
