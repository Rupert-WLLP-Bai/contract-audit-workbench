import casesJSON from "./business-labels.contract.json?raw";
import { businessActionLabel, businessCapabilityLabel, businessLineLabel, businessSourceTypeLabel, reviewScopeLabel, scenarioLabel, suppliedBusinessName } from "./business-labels";
import { actorRoleLabel, businessStatusLabel, contractTypeLabel, decisionLabel, modelStatusLabel, originLabel, parseLabel, relationLabel, roleLabel, runLabel, taskKindLabel } from "./review";

describe("business and export labels", () => {
  it("uses the same bounded vocabulary as the HTML report", () => {
    const labels: Record<string, (value: string) => string> = {
      business_line: businessLineLabel, scenario: scenarioLabel, review_scope: reviewScopeLabel,
      capability: businessCapabilityLabel, contract_type: contractTypeLabel, relation: relationLabel,
      origin: originLabel, decision: decisionLabel,
    };
    const cases: { field: string; value: string; label: string }[] = JSON.parse(casesJSON);
    for (const item of cases) expect(labels[item.field]?.(item.value), `${item.field}: ${item.value}`).toBe(item.label);
  });

  it("does not trust punctuation or mixed code as a business name", () => {
    expect(businessActionLabel("case.relation.created")).toBe("关联案件");
    expect(businessActionLabel("case.relation.created·")).toBe("已记录一项操作，类型待核对");
    expect(businessActionLabel("constructor")).toBe("已记录一项操作，类型待核对");
    expect(businessLineLabel("")).toBe("业务条线未设置");
    expect(scenarioLabel(null)).toBe("业务场景未设置");
    expect(businessSourceTypeLabel("reference")).toBe("参考资料");
    expect(suppliedBusinessName(" ENT（测试）股份有限公司 ", "名称未提供")).toBe(" ENT（测试）股份有限公司 ");
    expect(suppliedBusinessName("  ", "名称未提供")).toBe("名称未提供");
  });

  it("keeps unknown shared enum values out of the business label", () => {
    for (const label of [runLabel, parseLabel, roleLabel, originLabel, relationLabel, actorRoleLabel, modelStatusLabel, contractTypeLabel, decisionLabel, businessStatusLabel, taskKindLabel]) {
      for (const value of ["constructor", "__proto__", "unknown（旧）"]) {
        expect(typeof label(value)).toBe("string");
        expect(label(value)).not.toContain(value);
      }
    }
  });
});
