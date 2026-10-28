import type { RulePackManifestInput } from "../api/rulepacks";

const REQUIRED_RULE_CAPABILITIES: Record<string, true> = {
  "dedicated_line.minimum_bandwidth": true,
  "dedicated_line.monthly_availability": true,
  "payment.advance_ratio.ceiling": true,
};

export interface RulePackFormIssue {
  fieldId: string;
  message: string;
}

function addRequiredIssue(
  issues: RulePackFormIssue[],
  fieldId: string,
  value: string,
  label: string,
) {
  if (value.trim() === "")
    issues.push({ fieldId, message: `${label}不能为空。` });
}

export function validateRulePackDraft(
  manifest: RulePackManifestInput,
  trustedTenantId: string,
): RulePackFormIssue[] {
  const issues: RulePackFormIssue[] = [];
  addRequiredIssue(issues, "rulepack-id", manifest.id, "规则包 ID");
  addRequiredIssue(issues, "rulepack-version", manifest.version, "版本");
  if (manifest.tenant_id !== trustedTenantId)
    issues.push({
      fieldId: "rulepack-tenant",
      message: "规则包租户与当前管理会话不一致。",
    });
  const start = Date.parse(manifest.effective_from);
  const end = Date.parse(manifest.effective_until);
  if (!Number.isFinite(start))
    issues.push({
      fieldId: "rulepack-effective-from",
      message: "请输入有效的开始时间。",
    });
  if (!Number.isFinite(end))
    issues.push({
      fieldId: "rulepack-effective-until",
      message: "请输入有效的结束时间。",
    });
  if (Number.isFinite(start) && Number.isFinite(end) && start >= end)
    issues.push({
      fieldId: "rulepack-effective-until",
      message: "结束时间必须晚于开始时间。",
    });
  if (manifest.rules.length === 0)
    issues.push({
      fieldId: "rulepack-rules-heading",
      message: "至少添加一条规则。",
    });
  Object.keys(REQUIRED_RULE_CAPABILITIES).forEach((capability) => {
    if (!manifest.rules.some((rule) => rule.capability_id === capability)) {
      issues.push({
        fieldId: "rulepack-rules-heading",
        message: `必须保留核心能力：${capability}。`,
      });
    }
  });
  manifest.rules.forEach((rule, ruleIndex) => {
    const prefix = `rule-${ruleIndex}`;
    if (
      Object.hasOwn(REQUIRED_RULE_CAPABILITIES, rule.capability_id) &&
      !rule.mandatory
    ) {
      issues.push({
        fieldId: `${prefix}-mandatory`,
        message: `规则 ${ruleIndex + 1} 是专线核心能力，必须保持必检。`,
      });
    }
    addRequiredIssue(
      issues,
      `${prefix}-id`,
      rule.rule_id,
      `规则 ${ruleIndex + 1} ID`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-version`,
      rule.version,
      `规则 ${ruleIndex + 1} 版本`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-domain`,
      rule.domain,
      `规则 ${ruleIndex + 1} 检查域`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-capability`,
      rule.capability_id,
      `规则 ${ruleIndex + 1} 能力 ID`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-business-line`,
      rule.business_scope.business_line,
      `规则 ${ruleIndex + 1} 业务线`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-scenario`,
      rule.business_scope.scenario,
      `规则 ${ruleIndex + 1} 场景`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-contract-type`,
      rule.business_scope.contract_type,
      `规则 ${ruleIndex + 1} 合同类型`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-threshold`,
      rule.threshold.value,
      `规则 ${ruleIndex + 1} 阈值`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-unit`,
      rule.threshold.unit,
      `规则 ${ruleIndex + 1} 阈值单位`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-dimension`,
      rule.required_fact.dimension,
      `规则 ${ruleIndex + 1} 事实维度`,
    );
    addRequiredIssue(
      issues,
      `${prefix}-unit-family`,
      rule.required_fact.unit_family,
      `规则 ${ruleIndex + 1} 单位族`,
    );
    if (rule.tenant_id !== trustedTenantId)
      issues.push({
        fieldId: `${prefix}-tenant`,
        message: `规则 ${ruleIndex + 1} 的租户与当前管理会话不一致。`,
      });
    if (rule.required_fact.source_roles.length === 0)
      issues.push({
        fieldId: `${prefix}-roles`,
        message: `规则 ${ruleIndex + 1} 至少需要一个允许的材料角色。`,
      });
    if (rule.authority_sources.length === 0)
      issues.push({
        fieldId: `rule-${ruleIndex}-authority-heading`,
        message: `规则 ${ruleIndex + 1} 至少需要一个规则依据来源。`,
      });
    rule.authority_sources.forEach((source, sourceIndex) => {
      addRequiredIssue(
        issues,
        `rule-${ruleIndex}-authority-${sourceIndex}-id`,
        source.id,
        `规则 ${ruleIndex + 1} 来源 ${sourceIndex + 1} ID`,
      );
      addRequiredIssue(
        issues,
        `rule-${ruleIndex}-authority-${sourceIndex}-version`,
        source.version,
        `规则 ${ruleIndex + 1} 来源 ${sourceIndex + 1} 版本`,
      );
      addRequiredIssue(
        issues,
        `rule-${ruleIndex}-authority-${sourceIndex}-kind`,
        source.kind,
        `规则 ${ruleIndex + 1} 来源 ${sourceIndex + 1} 类型`,
      );
    });
  });
  return issues;
}
