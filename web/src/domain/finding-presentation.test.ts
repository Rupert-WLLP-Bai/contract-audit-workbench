import { describe, expect, it } from "vitest";
import type { Finding } from "../api/types";
import { findingPresentation, findingTitle } from "./finding-presentation";

function ruleFinding(rule: string, reason: string): Finding {
  return { id: "f", origin: "rule", relation: "insufficient", claim: `规则 ${rule} 信息不足：${reason}` } as Finding;
}

describe("finding presentation", () => {
  it.each([
    ["ENT-TECH-BW-001", "拟签合同中没有提取到该规则所需的条款。", "带宽要求暂无法核对", "没有提取到"],
    ["ENT-TECH-SLA-001", "合同依据未完整说明可用率的统计口径。", "服务可用率要求暂无法核对", "统计口径"],
    ["ENT-COM-ADV-001", "缺少采购文件或中标文件中的合同依据。", "预付款依据不足，暂无法核对", "补充相关文件"],
    ["ENT-TECH-SLA-001", "规则 ENT-TECH-SLA-001 的审查结果为 insufficient（sla_annex_basis_incomparable）。", "服务可用率要求暂无法核对", "统计口径"],
    ["ENT-COM-ADV-001", "规则 ENT-COM-ADV-001 的审查结果为 insufficient（amount_or_tax_basis_incomparable）。", "预付款比例暂无法核对", "含税口径"],
  ])("explains known rule results while retaining the original", (rule, reason, title, guidance) => {
    const finding = ruleFinding(rule, reason);
    expect(findingTitle(finding)).toBe(title);
    expect(findingPresentation(finding)).toMatchObject({ title, raw: finding.claim });
    expect(findingPresentation(finding)?.nextStep).toContain(guidance);
  });

  it("explains the known SLA annex conflict without changing the recorded result", () => {
    const finding = { ...ruleFinding("ENT-TECH-SLA-001", ""), relation: "conflict", claim: "规则 ENT-TECH-SLA-001 的审查结果为 conflict（contract_sla_annex_conflict）。" };
    expect(findingPresentation(finding)).toMatchObject({ title: "服务可用率与技术附件不一致", raw: finding.claim, nextStep: expect.stringContaining("对照两处原文") });
    expect(finding.relation).toBe("conflict");
  });

  it("leaves unknown reasons and non-rule findings unchanged", () => {
    const unknown = ruleFinding("ENT-TECH-BW-001", "规则 ENT-TECH-BW-001 的审查结果为 insufficient（future_reason）。");
    expect(findingPresentation(unknown)).toBeNull();
    expect(findingTitle(unknown)).toBe(unknown.claim);
    const model = { ...unknown, origin: "model" };
    expect(findingPresentation(model)).toBeNull();
  });
});
