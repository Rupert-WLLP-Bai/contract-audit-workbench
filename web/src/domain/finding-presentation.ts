// File metadata (manifest mirror):
// Purpose: Maps known frozen rule identifiers and reasons to review guidance while retaining unrecognized source wording.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { Finding } from "../api/types";

type Presentation = { title: string; nextStep: string; raw: string };

const subjects: Record<string, string> = {
  "ENT-TECH-BW-001": "带宽要求",
  "ENT-TECH-SLA-001": "服务可用率要求",
  "ENT-COM-ADV-001": "预付款比例",
};

// These reasons are emitted by the frozen telecom rule compiler. Unknown
// reasons deliberately retain their original wording rather than guessing.
const explanations: Record<string, string> = {
  contractual_authority_missing: "缺少采购文件或中标文件中的合同依据。请补充相关文件，再核对约定。",
  contractual_authority_ambiguous:
    "采购文件或中标文件中的合同依据存在歧义。请核对相关条款，明确适用依据。",
  authority_value_out_of_range: "合同依据中的阈值超出当前规则支持范围。请人工核对依据与适用范围。",
  authority_semantics_unsupported:
    "合同依据的表述无法支持当前规则比较。请人工核对相关条款及其含义。",
  authority_measurement_basis_missing:
    "合同依据未完整说明可用率的统计口径。请补充或核对统计周期、故障口径及排除项。",
  authority_amount_basis_missing:
    "合同依据未说明预付款比例的计算基础。请补充或核对比例所依据的金额。",
  required_fact_missing: "拟签合同中没有提取到该检查所需的条款。请核对合同并补充相关约定。",
  required_fact_ambiguous: "拟签合同中存在多个候选条款，无法确定唯一约定。请核对并明确适用条款。",
  contract_scope_unsupported: "当前合同范围不能由该规则处理。请人工核对合同范围及适用依据。",
  sla_annex_missing: "缺少服务可用率附件。请补充附件并核对相关约定。",
  sla_annex_basis_incomparable:
    "服务可用率附件与合同的统计口径不能直接比较。请核对统计口径及相关约定。",
  circuit_object_mismatch: "相关材料中的电路对象未能对应。请核对电路标识及相关材料，再进行审查。",
  service_object_mismatch: "相关材料中的服务对象未能对应。请核对服务对象及相关材料，再进行审查。",
  amount_or_tax_basis_incomparable:
    "相关金额或含税口径不能直接比较。请核对金额计算基础和税务口径。",
  source_role_mismatch: "相关条款的材料角色与检查要求不一致。请核对材料及其角色。",
  evidence_missing: "缺少支持该检查的引用。请补充或核对相关原文。",
  source_role_not_allowed: "引用材料的角色不符合该检查要求。请核对引用来源。",
  applicability_not_proven_for_rule_and_object:
    "尚不能确认该条款适用于当前检查对象。请核对适用范围和对象。",
  required_fact_metadata_missing: "提取条款所需的部分信息缺失。请核对相关原文及适用对象。",
  object_not_supported_by_evidence: "引用尚不能支持所提取的检查对象。请核对引用和对象。",
  fact_metadata_mismatch: "提取的条款信息与检查要求不一致。请核对原文及相关信息。",
  fact_value_invalid: "提取的数值无法用于比较。请核对原文中的数值。",
  evidence_does_not_support_fact: "引用尚不能支持提取的条款内容。请核对引用和原文。",
  fact_unit_or_range_invalid: "数值的单位或范围不能用于当前比较。请核对原文中的数值和单位。",
};

const reasonCodes: Record<string, string> = {
  "缺少采购文件或中标文件中的合同依据。": "contractual_authority_missing",
  "采购文件或中标文件中的合同依据存在歧义。": "contractual_authority_ambiguous",
  "合同依据中的阈值超出当前规则支持范围。": "authority_value_out_of_range",
  "合同依据的表述无法支持当前规则比较。": "authority_semantics_unsupported",
  "合同依据未完整说明可用率的统计口径。": "authority_measurement_basis_missing",
  "合同依据未说明预付款比例的计算基础。": "authority_amount_basis_missing",
  "拟签合同中没有提取到该规则所需的条款。": "required_fact_missing",
  "拟签合同中存在多个候选条款，无法确定唯一约定。": "required_fact_ambiguous",
};

export function findingPresentation(finding: Finding): Presentation | null {
  if (
    finding.origin === "rule" &&
    finding.relation === "conflict" &&
    finding.claim === "规则 ENT-TECH-SLA-001 的审查结果为 conflict（contract_sla_annex_conflict）。"
  ) {
    return {
      title: "服务可用率与技术附件不一致",
      nextStep:
        "拟签合同与技术附件中的服务可用率约定不同。请对照两处原文，核对适用约定并作出审查判断。",
      raw: finding.claim,
    };
  }
  if (finding.origin !== "rule" || finding.relation !== "insufficient") return null;
  const match = /^规则 (ENT-[A-Z0-9-]+) 信息不足：(.+)$/.exec(finding.claim);
  if (!match) return null;
  const rule = match[1] ?? "";
  const reason = match[2] ?? "";
  const subject = subjects[rule];
  if (!subject) return null;
  let nextStep: string | undefined;
  if (/^法律适用所需的来源或事实依据尚未核实（[^）]+）。$/.test(reason)) {
    nextStep = "法律适用所需的来源或事实依据尚未核实。请核对规范来源和适用事实。";
  } else {
    const generic = new RegExp(`^规则 ${rule} 的审查结果为 insufficient（([a-z_]+)）。$`).exec(
      reason,
    );
    const code = reasonCodes[reason] ?? generic?.[1] ?? "";
    nextStep = explanations[code];
  }
  if (!nextStep) return null;
  if (rule === "ENT-COM-ADV-001" && reason === "缺少采购文件或中标文件中的合同依据。") {
    return {
      title: "预付款依据不足，暂无法核对",
      nextStep: "缺少采购文件或中标文件中的合同依据。请补充相关文件，再核对预付款约定。",
      raw: finding.claim,
    };
  }
  return { title: `${subject}暂无法核对`, nextStep, raw: finding.claim };
}

export function findingTitle(finding: Finding): string {
  return findingPresentation(finding)?.title ?? (finding.claim || "未命名线索");
}
