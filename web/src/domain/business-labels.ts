// File metadata (manifest mirror):
// Purpose: Maps stable business scope and audit action identifiers to Chinese presentation labels without renaming API or stored identifiers.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
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

function display(table: Record<string, string>, value: string | null | undefined, missing: string, unknown: string): string {
  if (!value || value === "unspecified") return missing;
  if (Object.hasOwn(table, value)) return table[value]!;
  // Some saved fixtures already contain a human-readable label. Keep it
  // visible instead of replacing real data with a review prompt; only bare
  // machine identifiers are marked as needing verification.
  return /[^\x00-\x7f]/.test(value) ? value : unknown;
}

export const businessLineLabel = (value: string | null | undefined) =>
  display(LINES, value, "业务条线未设置", "业务条线待核对");
export const scenarioLabel = (value: string | null | undefined) =>
  display(SCENARIOS, value, "业务场景未设置", "业务场景待核对");
export const businessActionLabel = (value: string | null | undefined) =>
  display(ACTIONS, value, "业务操作", "业务操作");
export function businessScopeLabel(line: string | null | undefined, scenario: string | null | undefined): string {
  return [line ? businessLineLabel(line) : null, scenario ? scenarioLabel(scenario) : null]
    .filter(Boolean).join(" / ") || "业务范围未设置";
}
