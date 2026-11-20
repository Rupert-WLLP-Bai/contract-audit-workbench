// File metadata (manifest mirror):
// Purpose: Projects authorized case business identity and searchable display text from existing profile fields while retaining stable machine identity and explicit missing-number labels.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-15
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import type { CaseProfile, CaseSummary, WorkbenchMember } from "../api/types";
import { businessLineLabel, scenarioLabel } from "./business-labels";

type CaseIdentity = CaseSummary & { profile?: CaseProfile | null };

const CONTRACT_LABELS: Record<string, string> = {
  procurement_ict: "采购 / ICT",
  network_engineering: "网络工程",
  framework: "框架协议",
  service: "服务合同",
  unspecified: "未分类",
};

export function contractTypeLabel(value: string): string {
  return Object.hasOwn(CONTRACT_LABELS, value) ? CONTRACT_LABELS[value]! : "合同类型待核对";
}

export function caseIdentityDetails(item: CaseIdentity, _members: WorkbenchMember[] = []): string {
  const number = (item.case_number || item.profile?.number || "").trim();
  const type =
    item.contract_type && item.contract_type !== "unspecified"
      ? contractTypeLabel(item.contract_type)
      : "";
  const scope = [item.business_line, item.scenario]
    .filter((value) => value && value !== "unspecified")
    .map((value, index) =>
      index === 0 && value === item.business_line ? businessLineLabel(value) : scenarioLabel(value),
    )
    .filter((value) => value !== "—" && !value.endsWith("待核对"))
    .join(" / ");
  return [number, type === "合同类型待核对" ? "" : type, scope].filter(Boolean).join(" · ");
}

// Full machine identity is secondary and never used as a business number.
export function caseOptionLabel(item: CaseIdentity, members: WorkbenchMember[] = []): string {
  return [item.title, caseIdentityDetails(item, members), `案件标识 ${item.id}`]
    .filter(Boolean)
    .join(" · ");
}

export function caseSearchText(item: CaseIdentity): string {
  return [
    item.title,
    item.id,
    item.case_number,
    item.profile?.number,
    item.customer,
    item.profile?.customer,
    item.supplier,
    item.profile?.supplier,
    businessLineLabel(item.business_line),
    scenarioLabel(item.scenario),
    item.business_line,
    item.scenario,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}
