import { Card } from "antd";
import { Button, Checkbox, Input, Select } from "antd";
import { useRef, useState } from "react";
import type {
  RulePackAuthoritySource,
  RulePackManifestInput,
  RulePackRule,
} from "../api/rulepacks";
import { toLocalInputValue, toUtcInstant } from "./local-datetime";

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

  return (
    <div className="rulepack-editor">
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
          <Card
            role="group"
            aria-label={
              "规则 " +
              String(ruleIndex + 1) +
              String(rule.rule_id ? `，${rule.rule_id}` : "")
            }
            className="rulepack-rule"
            key={ruleKeys[ruleIndex] ?? ruleIndex}
            title={
              <>
                规则 {ruleIndex + 1}
                {rule.rule_id ? `，${rule.rule_id}` : ""}
              </>
            }
          >
            <div className="rulepack-fields">
              <label className="field" htmlFor={`rule-${ruleIndex}-id`}>
                规则 ID
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-id`}
                  required
                  value={rule.rule_id}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      rule_id: event.target.value,
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-version`}>
                规则版本
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-version`}
                  required
                  value={rule.version}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      version: event.target.value,
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-domain`}>
                检查域
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-domain`}
                  required
                  value={rule.domain}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      domain: event.target.value,
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-capability`}>
                能力 ID
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-capability`}
                  required
                  value={rule.capability_id}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      capability_id: event.target.value,
                    })
                  }
                />
              </label>
              <label
                className="field"
                htmlFor={`rule-${ruleIndex}-business-line`}
              >
                业务线
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-business-line`}
                  required
                  value={rule.business_scope.business_line}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      business_scope: {
                        ...rule.business_scope,
                        business_line: event.target.value,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-scenario`}>
                场景
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-scenario`}
                  required
                  value={rule.business_scope.scenario}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      business_scope: {
                        ...rule.business_scope,
                        scenario: event.target.value,
                      },
                    })
                  }
                />
              </label>
              <label
                className="field"
                htmlFor={`rule-${ruleIndex}-contract-type`}
              >
                合同类型
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-contract-type`}
                  required
                  value={rule.business_scope.contract_type}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      business_scope: {
                        ...rule.business_scope,
                        contract_type: event.target.value,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-tenant`}>
                规则租户
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-tenant`}
                  value={tenantId}
                  readOnly
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-comparator`}>
                比较器
                <Select
                  disabled={disabled}
                  id={`rule-${ruleIndex}-comparator`}
                  aria-label="比较器"
                  value={rule.comparator}
                  onChange={(comparator: RulePackRule["comparator"]) => {
                    const defaultUnit =
                      comparator === "minimum_bandwidth"
                        ? "Mbit/s"
                        : comparator === "monthly_sla"
                          ? "percent"
                          : "ratio";
                    updateRule(value, onChange, ruleIndex, {
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
                    });
                  }}
                  options={COMPARATORS.map(([id, label]) => ({
                    value: id,
                    label: `${label}，${id}`,
                  }))}
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-threshold`}>
                比较阈值
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-threshold`}
                  required
                  value={rule.threshold.value}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      threshold: {
                        ...rule.threshold,
                        value: event.target.value,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-unit`}>
                阈值单位
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-unit`}
                  required
                  value={rule.threshold.unit}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      threshold: {
                        ...rule.threshold,
                        unit: event.target.value,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-basis`}>
                计算基数（可选）
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-basis`}
                  value={rule.threshold.basis ?? ""}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      threshold: {
                        ...rule.threshold,
                        basis: event.target.value || undefined,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-dimension`}>
                所需事实维度
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-dimension`}
                  required
                  value={rule.required_fact.dimension}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      required_fact: {
                        ...rule.required_fact,
                        dimension: event.target.value,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-roles`}>
                允许材料角色（每行一项）
                <Input.TextArea
                  disabled={disabled}
                  id={`rule-${ruleIndex}-roles`}
                  required
                  value={rule.required_fact.source_roles.join("\n")}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      required_fact: {
                        ...rule.required_fact,
                        source_roles: splitLines(event.target.value),
                      },
                    })
                  }
                />
              </label>
              <label
                className="field"
                htmlFor={`rule-${ruleIndex}-unit-family`}
              >
                单位族
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-unit-family`}
                  required
                  value={rule.required_fact.unit_family}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      required_fact: {
                        ...rule.required_fact,
                        unit_family: event.target.value,
                      },
                    })
                  }
                />
              </label>
              <label
                className="field"
                htmlFor={`rule-${ruleIndex}-measurement-window`}
              >
                测量窗口（可选）
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-measurement-window`}
                  value={rule.required_fact.measurement_window ?? ""}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      required_fact: {
                        ...rule.required_fact,
                        measurement_window: event.target.value || undefined,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-time-zone`}>
                时区（可选）
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-time-zone`}
                  value={rule.required_fact.time_zone ?? ""}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      required_fact: {
                        ...rule.required_fact,
                        time_zone: event.target.value || undefined,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-calendar`}>
                日历（可选）
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-calendar`}
                  value={rule.required_fact.calendar ?? ""}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      required_fact: {
                        ...rule.required_fact,
                        calendar: event.target.value || undefined,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-outage`}>
                故障口径（可选）
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-outage`}
                  value={rule.required_fact.outage_definition ?? ""}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      required_fact: {
                        ...rule.required_fact,
                        outage_definition: event.target.value || undefined,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-fact-basis`}>
                事实计算基数（可选）
                <Input
                  disabled={disabled}
                  id={`rule-${ruleIndex}-fact-basis`}
                  value={rule.required_fact.basis ?? ""}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      required_fact: {
                        ...rule.required_fact,
                        basis: event.target.value || undefined,
                      },
                    })
                  }
                />
              </label>
              <label className="field" htmlFor={`rule-${ruleIndex}-exclusions`}>
                排除项（每行一项）
                <Input.TextArea
                  id={`rule-${ruleIndex}-exclusions`}
                  disabled={disabled || rule.comparator !== "monthly_sla"}
                  value={(rule.required_fact.exclusions ?? []).join("\n")}
                  onChange={(event) =>
                    updateRule(value, onChange, ruleIndex, {
                      required_fact: {
                        ...rule.required_fact,
                        exclusions: splitLines(event.target.value),
                      },
                    })
                  }
                />
              </label>
              <Checkbox
                id={`rule-${ruleIndex}-mandatory`}
                checked={rule.mandatory}
                disabled={
                  disabled || (isCoreCapability(rule) && rule.mandatory)
                }
                onChange={(event) =>
                  updateRule(value, onChange, ruleIndex, {
                    mandatory: isCoreCapability(rule)
                      ? true
                      : event.target.checked,
                  })
                }
                className="workflow-check"
              >
                此项为必检规则
                {isCoreCapability(rule) ? "（专线核心能力必须保留必检）" : ""}
              </Checkbox>
              <Checkbox
                disabled={disabled}
                checked={rule.required_fact.object_required}
                onChange={(event) =>
                  updateRule(value, onChange, ruleIndex, {
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
            </div>
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
                  onClick={() => addAuthority(ruleIndex)}
                >
                  添加来源
                </Button>
              </header>
              {rule.authority_sources.map((source, sourceIndex) => (
                <div
                  className="rulepack-source-row"
                  key={authorityKeys[ruleIndex]?.[sourceIndex] ?? sourceIndex}
                >
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
                      onChange={(event) =>
                        updateAuthority(
                          value,
                          onChange,
                          ruleIndex,
                          sourceIndex,
                          { id: event.target.value },
                        )
                      }
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
                      onChange={(event) =>
                        updateAuthority(
                          value,
                          onChange,
                          ruleIndex,
                          sourceIndex,
                          { version: event.target.value },
                        )
                      }
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
                      onChange={(event) =>
                        updateAuthority(
                          value,
                          onChange,
                          ruleIndex,
                          sourceIndex,
                          { kind: event.target.value },
                        )
                      }
                    />
                  </label>
                  <Button
                    disabled={disabled}
                    type="text"
                    htmlType="button"
                    aria-label={`移除规则 ${ruleIndex + 1} 的来源 ${sourceIndex + 1}`}
                    onClick={() => removeAuthority(ruleIndex, sourceIndex)}
                  >
                    移除来源
                  </Button>
                </div>
              ))}
            </section>
            {value.rules.length > 1 && !isCoreCapability(rule) ? (
              <Button
                aria-label={"移除规则 " + String(ruleIndex + 1)}
                disabled={disabled}
                type="text"
                htmlType="button"
                onClick={() => removeRule(ruleIndex)}
              >
                移除规则 {ruleIndex + 1}
              </Button>
            ) : null}
          </Card>
        ))}
      </section>
    </div>
  );
}
