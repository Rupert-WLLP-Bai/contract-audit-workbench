import type { CaseDetail } from "../api/types";

export interface RuneSpan {
  start: number;
  end: number;
  quote: string;
}

export interface MaterialAnchorVersionOption {
  key: string;
  material_id: string;
  version: number;
  role: string;
  purpose: string;
}

const purposeLabels: Record<string, string> = {
  primary_contract: "主合同", supplement: "补充协议", procurement: "采购文件", award: "中标文件",
  acceptance: "验收文件", invoice: "发票", correspondence: "往来文件", other: "其他附件",
};

export function materialAnchorVersionOptions(caseDetail: CaseDetail | undefined): MaterialAnchorVersionOption[] {
  if (!caseDetail?.materials || !caseDetail.material_versions) return [];
  return caseDetail.material_versions.flatMap((version) => {
    const material = caseDetail.materials?.find((item) => item.material_id === version.material_id);
    if (!material) return [];
    const role = material.role === "draft_contract" ? "拟签合同" : material.role === "procurement" ? "采购文件" : material.role === "award" ? "中标文件" : "附件";
    return [{
      key: JSON.stringify([version.material_id, version.version]),
      material_id: version.material_id,
      version: version.version,
      role,
      purpose: purposeLabels[material.purpose] ?? "其他附件",
    }];
  });
}

/** Convert browser UTF-16 offsets into offsets used by the frozen parsed-text API. */
export function runeSpanFromUTF16(text: string, startUTF16: number, endUTF16: number): RuneSpan | null {
  if (!Number.isInteger(startUTF16) || !Number.isInteger(endUTF16) || startUTF16 < 0 || endUTF16 <= startUTF16 || endUTF16 > text.length)
    return null;
  const start = Array.from(text.slice(0, startUTF16)).length;
  const end = Array.from(text.slice(0, endUTF16)).length;
  const quote = Array.from(text).slice(start, end).join("");
  if (end <= start || quote === "") return null;
  return { start, end, quote };
}
