// File metadata (manifest mirror):
// Purpose: Maps stable business scope and audit action identifiers to Chinese presentation labels without renaming API or stored identifiers.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

/** Business presentation only; stored scope and event identifiers remain stable. */
const LINES: Record<string, string> = { ENT: "政企业务" };
const SCENARIOS: Record<string, string> = {
  telecom_dedicated_line: "政企专线",
  cloud_services: "云服务采购",
};
const ACTIONS: Record<string, string> = {
  "case.created": "新建案件",
  "case.updated": "更新案件信息",
  "case.profile.updated": "更新案件信息",
  "case.relation.created": "关联案件",
  "case.relation.deleted": "取消案件关联",
  "case.task.created": "创建办理任务",
  "case.task.updated": "更新办理任务",
  "task.created": "创建办理任务",
  "task.updated": "更新办理任务",
  "material.uploaded": "上传材料",
  "remediation.created": "创建整改",
  "remediation.submitted": "提交整改材料",
  "remediation.verified": "复核整改",
  "report.created": "生成审查报告",
  "case.archived": "归档案件",
  "rulepack.published": "发布规则包",
  "policy.updated": "更新策略",
  "knowledge.created": "新增知识条目",
  "finding.confirm_risk": "确认风险",
  "finding.false_positive": "标记为误报",
  "finding.insufficient_evidence": "标记证据不足",
  "finding.accept_residual": "接受残余风险",
};
const DOMAINS: Record<string, string> = {
  compliance: "合规与授权", commercial: "商务与财务", technical: "技术与服务",
  fulfillment: "履约与变更", counterparty: "主体与关联",
  warranty: "质保", amount: "金额", schedule: "交付与验收时间",
  payment_sum: "付款比例", payment_condition: "付款条件", liability: "责任",
  "付款": "付款", "质保": "质保",
};
const CAPABILITIES: Record<string, string> = {
  authorization_basis: "授权依据", commercial_baseline: "商务条款", warranty_months: "质保期限",
  acceptance_schedule: "交付与验收时间", counterparty_record: "主体登记信息", amount: "金额",
  party: "合同主体", approval_authority: "审批或授权", approval_validity: "审批有效期",
  effective_scope: "服务范围", scope: "技术与服务范围", milestone: "履约里程碑",
  explicit_start_event: "启动时间", subject_id: "主体标识", signing_party: "签署方",
  payee: "收款方", responsibility: "责任与义务", "dedicated_line.minimum_bandwidth": "最低保障带宽",
  "dedicated_line.monthly_availability": "月可用率", "payment.advance_ratio.ceiling": "预付款比例上限",
  "cloud_services.minimum_bandwidth": "最低保障带宽", "cloud_services.monthly_availability": "月可用率",
  "single_contract.payment_sum": "主合同中可识别的付款比例合计",
};
const COMPARATORS: Record<string, string> = {
  minimum_bandwidth: "最低保障带宽对照", monthly_sla: "自然月服务可用率对照",
  advance_payment_ceiling: "预付款比例上限对照",
};
const REVIEW_SCOPES: Record<string, string> = {
  full_review: "完整审查", single_contract_precheck: "单合同预审",
};
const SOURCE_TYPES: Record<string, string> = {
  reference: "参考资料", synthetic_policy: "合成制度", human_feedback: "人工反馈",
};
const REMEDIATION_STATES: Record<string, string> = {
  open: "待整改", submitted: "待复审", closed: "已关闭", reopened: "重新打开",
};
const POLICY_PREVIEW_REASONS: Record<string, string> = {
  unknown_rule: "绑定的规则未找到", threshold_missing: "未提取到对应阈值", multiple_thresholds: "提取到多个阈值，无法唯一确认",
  source_span_missing_or_ambiguous: "规则原文引用缺失或不唯一", unsupported_semantics_or_measurement_basis: "规则语义或计量口径尚不能确认",
  threshold_or_unit_mismatch: "政策阈值或单位与规则不同", unexpressible_qualifier_or_scope: "限定条件或范围不能由当前规则表达",
  invalid_numeric_value: "数值无法识别", percentage_out_of_range: "百分比超出范围", ratio_unit_missing: "比例单位不明确",
  ratio_out_of_range: "比例超出范围", unexpressible_threshold: "当前规则无法表达该阈值", semantic_check_only: "需要人工核对含义",
  unbound_supported_threshold: "提取到尚未绑定规则的阈值", binding_or_policy_invalid: "规则绑定或政策内容待核对",
};
const CHECK_ACTIONS: Record<string, string> = {
  start_review: "开始审查后执行本项检查，是否可以开始以准备状态为准",
  reparse_material: "请到材料列表重新解析对应材料",
  provide_missing_material_or_legal_basis: "补充下列材料或法律依据后重新核对",
  no_action: "本项不适用，无需补充材料",
  // These two exact strings are emitted by the current readiness service.
  "本范围不执行正式治理规则；补齐材料后发起完整审查": "本范围不执行正式治理规则；补齐材料后发起完整审查",
  "联系治理管理员修复规则包": "联系治理管理员修复规则包",
};

function display(table: Record<string, string>, value: string | null | undefined, missing: string, unknown: string, legacyLabels = false): string {
  if (!value || value === "unspecified") return missing;
  if (Object.hasOwn(table, value)) return table[value]!;
  // Only previously recognised labels are accepted in legacy enum fields.
  return legacyLabels && Object.values(table).includes(value) ? value : unknown;
}

export const businessLineLabel = (value: string | null | undefined) =>
  display(LINES, value, "业务条线未设置", "业务条线待核对", true);
export const scenarioLabel = (value: string | null | undefined) =>
  display(SCENARIOS, value, "业务场景未设置", "业务场景待核对", true);
export const businessActionLabel = (value: string | null | undefined) =>
  display(ACTIONS, value, "业务操作未记录", "已记录一项操作，类型待核对", true);
export const businessDomainLabel = (value: string | null | undefined) =>
  display(DOMAINS, value, "领域未报告", "领域待核对");
export const businessCapabilityLabel = (value: string | null | undefined) =>
  display(CAPABILITIES, value, "审查项目未报告", "审查项目待核对");
export const businessComparatorLabel = (value: string | null | undefined) =>
  display(COMPARATORS, value, "比较类型未报告", "比较类型待核对");
export const knownBusinessComparatorLabel = (value: string | null | undefined) =>
  value && Object.hasOwn(COMPARATORS, value) ? COMPARATORS[value] : undefined;
export const reviewScopeLabel = (value: string | null | undefined) =>
  display(REVIEW_SCOPES, value, "审查范围身份未返回", "审查范围待核对");
export const businessSourceTypeLabel = (value: string | null | undefined) =>
  display(SOURCE_TYPES, value, "来源类型未报告", "来源类型待核对");
export const readinessCheckActionLabel = (value: string | null | undefined) =>
  display(CHECK_ACTIONS, value, "处理方式未报告", "请联系审查人员核对处理方式");
export const remediationStatusLabel = (value: string | null | undefined) =>
  display(REMEDIATION_STATES, value, "整改状态未报告", "整改状态待核对");
export const policyPreviewReasonLabel = (value: string | null | undefined) =>
  display(POLICY_PREVIEW_REASONS, value, "原因未报告", "原因待核对");
/** Only for fields whose source contract explicitly provides a display name. */
export function suppliedBusinessName(value: string | null | undefined, missing: string): string {
  return value?.trim() ? value : missing;
}
export function businessScopeLabel(line: string | null | undefined, scenario: string | null | undefined): string {
  return [line ? businessLineLabel(line) : null, scenario ? scenarioLabel(scenario) : null]
    .filter(Boolean).join(" / ") || "业务范围未设置";
}
