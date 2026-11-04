import type { CaseProfile, CaseSummary, WorkbenchMember } from "../api/types";
import { businessLineLabel, businessScopeLabel, scenarioLabel } from "./business-labels";
import { preciseTime } from "./time";

type CaseIdentity = CaseSummary & { profile?: CaseProfile | null };

const CONTRACT_LABELS: Record<string, string> = {
  procurement_ict: "采购 / ICT", network_engineering: "网络工程",
  framework: "框架协议", service: "服务合同", unspecified: "未分类",
};

export function contractTypeLabel(value: string): string {
  return Object.hasOwn(CONTRACT_LABELS, value) ? CONTRACT_LABELS[value]! : "合同类型待核对";
}

export function caseIdentityDetails(item: CaseIdentity, members: WorkbenchMember[] = []): string {
  const profile = item.profile;
  const number = item.case_number ?? profile?.number;
  const customer = item.customer ?? profile?.customer;
  const supplier = item.supplier ?? profile?.supplier;
  const owner = item.owner_name || members.find((member) => member.id === profile?.owner_id)?.display_name;
  return [
    number?.trim() ? `案件业务编号 ${number}` : "业务编号未登记",
    customer?.trim() && customer !== "unspecified" ? `客户 ${customer}` : "",
    supplier?.trim() && supplier !== "unspecified" ? `供应商 ${supplier}` : "",
    owner ? `负责人 ${owner}` : "",
    businessScopeLabel(item.business_line, item.scenario),
    contractTypeLabel(item.contract_type),
    `更新时间 ${preciseTime(item.updated_at)}`,
  ].filter(Boolean).join(" · ");
}

// Full machine identity is secondary and never used as a business number.
export function caseOptionLabel(item: CaseIdentity, members: WorkbenchMember[] = []): string {
  return `${item.title} · ${caseIdentityDetails(item, members)} · 案件标识 ${item.id}`;
}

export function caseSearchText(item: CaseIdentity): string {
  return [item.title, item.id, item.case_number, item.profile?.number,
    item.customer, item.profile?.customer, item.supplier, item.profile?.supplier,
    businessLineLabel(item.business_line), scenarioLabel(item.scenario),
    item.business_line, item.scenario,
  ].filter(Boolean).join(" ").toLowerCase();
}
