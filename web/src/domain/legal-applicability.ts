/** Controlled values used by the fact-entry form and its API boundary parser. */
export const legalApplicabilityControlledValues: Record<
  string,
  Array<{ value: string; label: string }>
> = {
  buyer_kind: [
    { value: "state_organ", label: "国家机关" },
    { value: "institution", label: "事业单位" },
    { value: "social_organization", label: "社会团体" },
    { value: "enterprise", label: "企业" },
    { value: "natural_person", label: "自然人" },
  ],
  seller_kind: [
    { value: "state_organ", label: "国家机关" },
    { value: "institution", label: "事业单位" },
    { value: "social_organization", label: "社会团体" },
    { value: "enterprise", label: "企业" },
    { value: "natural_person", label: "自然人" },
  ],
  buyer_enterprise_size: [
    { value: "large", label: "大型企业" },
    { value: "sme", label: "中小企业" },
    { value: "not_enterprise", label: "非企业主体" },
  ],
  seller_enterprise_size: [
    { value: "large", label: "大型企业" },
    { value: "sme", label: "中小企业" },
    { value: "not_enterprise", label: "非企业主体" },
  ],
  funding_kind: [
    { value: "fiscal", label: "财政性资金" },
    { value: "mixed_with_fiscal", label: "含财政性资金" },
    { value: "state_owned_non_fiscal", label: "非财政性国有资金" },
    { value: "private", label: "非国有及非财政资金" },
  ],
  procurement_scope: [
    { value: "centralized_catalog", label: "集中采购目录内" },
    { value: "above_limit", label: "达到采购限额" },
    { value: "catalog_and_above_limit", label: "目录内且达到限额" },
    { value: "outside_catalog_and_below_limit", label: "目录外且未达限额" },
  ],
  project_kind: [
    { value: "construction", label: "建设工程" },
    { value: "construction_related_goods", label: "工程相关货物" },
    { value: "construction_related_services", label: "工程相关服务" },
    { value: "standalone_goods", label: "独立货物采购" },
    { value: "standalone_services", label: "独立服务采购" },
  ],
  contract_clause_kind: [
    { value: "standard_form", label: "格式条款" },
    { value: "negotiated", label: "协商条款" },
  ],
  transaction_kind: [
    { value: "civil_contract", label: "民事合同交易" },
    { value: "non_civil", label: "非民事交易" },
  ],
};

export function isLegalApplicabilityControlledValue(
  dimension: string,
  value: string,
): boolean {
  return legalApplicabilityControlledValues[dimension]?.some(
    (option) => option.value === value,
  ) === true;
}
