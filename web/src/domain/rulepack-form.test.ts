import { describe, expect, it } from "vitest";
import type { RulePackManifestInput, RulePackRule } from "../api/rulepacks";
import { validateRulePackDraft } from "./rulepack-form";

function validDraft(): RulePackManifestInput {
  const specifications: (Pick<RulePackRule, "capability_id" | "comparator"> & {
    dimension: string;
    unit: string;
  })[] = [
    {
      capability_id: "dedicated_line.minimum_bandwidth",
      comparator: "minimum_bandwidth",
      dimension: "bandwidth",
      unit: "Mbit/s",
    },
    {
      capability_id: "dedicated_line.monthly_availability",
      comparator: "monthly_sla",
      dimension: "availability",
      unit: "percent",
    },
    {
      capability_id: "payment.advance_ratio.ceiling",
      comparator: "advance_payment_ceiling",
      dimension: "advance_ratio",
      unit: "ratio",
    },
  ];
  const rules: RulePackRule[] = specifications.map((spec, index) => ({
    rule_id: `rule-${index + 1}`,
    version: "2",
    domain: "technical",
    capability_id: spec.capability_id,
    mandatory: true,
    business_scope: {
      business_line: "ENT",
      scenario: "dedicated_line",
      contract_type: "service",
    },
    tenant_id: "tenant-test",
    comparator: spec.comparator,
    threshold: { value: "100", unit: spec.unit },
    required_fact: {
      dimension: spec.dimension,
      source_roles: ["procurement"],
      object_required: true,
      unit_family: spec.dimension,
      exclusions: spec.comparator === "monthly_sla" ? [] : null,
    },
    authority_sources: [{ id: "policy", version: "3", kind: "policy" }],
  }));
  return {
    schema_version: "rulepack/v1",
    id: "telecom-core",
    version: "2",
    tenant_id: "tenant-test",
    effective_from: "2026-10-01T00:00:00Z",
    effective_until: "2027-01-01T00:00:00Z",
    rules,
  };
}

function issueFields(draft: RulePackManifestInput): string[] {
  return validateRulePackDraft(draft, "tenant-test").map(
    (issue) => issue.fieldId,
  );
}

describe("validateRulePackDraft", () => {
  it("accepts a tenant-bound draft with all mandatory core capabilities", () => {
    const draft = validDraft();
    const original = structuredClone(draft);
    expect(validateRulePackDraft(draft, "tenant-test")).toEqual([]);
    expect(draft).toEqual(original);
  });

  it.each(["id", "version"] as const)(
    "rejects whitespace-only pack %s",
    (key) => {
      const draft = validDraft();
      draft[key] = " \t ";
      expect(issueFields(draft)).toEqual([`rulepack-${key}`]);
    },
  );

  it("reports pack and individual rule tenant mismatches independently", () => {
    const draft = validDraft();
    draft.tenant_id = "another-tenant";
    draft.rules[1]!.tenant_id = "another-tenant";
    expect(issueFields(draft)).toEqual(["rulepack-tenant", "rule-1-tenant"]);
  });

  it.each([
    ["invalid", "2027-01-01T00:00:00Z", "rulepack-effective-from"],
    ["2026-10-01T00:00:00Z", "", "rulepack-effective-until"],
    [
      "2026-10-01T00:00:00Z",
      "2026-10-01T00:00:00Z",
      "rulepack-effective-until",
    ],
    [
      "2027-01-01T00:00:00Z",
      "2026-10-01T00:00:00Z",
      "rulepack-effective-until",
    ],
  ])(
    "rejects invalid or non-increasing validity bounds %s / %s",
    (start, end, field) => {
      const draft = validDraft();
      draft.effective_from = start;
      draft.effective_until = end;
      expect(issueFields(draft)).toEqual([field]);
    },
  );

  it("reports both invalid dates without treating them as a valid range", () => {
    const draft = validDraft();
    draft.effective_from = "";
    draft.effective_until = "not-a-date";
    expect(issueFields(draft)).toEqual([
      "rulepack-effective-from",
      "rulepack-effective-until",
    ]);
  });

  it("requires rules and identifies every missing core capability", () => {
    const draft = validDraft();
    draft.rules = [];
    const issues = validateRulePackDraft(draft, "tenant-test");
    expect(issues).toHaveLength(4);
    expect(
      issues.every((issue) => issue.fieldId === "rulepack-rules-heading"),
    ).toBe(true);
    for (const capability of [
      "dedicated_line.minimum_bandwidth",
      "dedicated_line.monthly_availability",
      "payment.advance_ratio.ceiling",
    ]) {
      expect(issues.some((issue) => issue.message.includes(capability))).toBe(
        true,
      );
    }
  });

  it.each([0, 1, 2])("keeps core rule %i mandatory", (index) => {
    const draft = validDraft();
    draft.rules[index]!.mandatory = false;
    expect(issueFields(draft)).toEqual([`rule-${index}-mandatory`]);
  });

  it.each(["constructor", "__proto__", "toString"])(
    "does not mistake literal capability %s for a core capability",
    (capability) => {
      const draft = validDraft();
      draft.rules.push({
        ...structuredClone(draft.rules[0]!),
        capability_id: capability,
        mandatory: false,
      });
      expect(validateRulePackDraft(draft, "tenant-test")).toEqual([]);
    },
  );

  it("reports all empty rule fields with links to the affected second row", () => {
    const draft = validDraft();
    const rule = draft.rules[1]!;
    rule.rule_id = " ";
    rule.version = "";
    rule.domain = "\t";
    rule.business_scope = {
      business_line: " ",
      scenario: "",
      contract_type: " ",
    };
    rule.threshold = { value: " ", unit: "" };
    rule.required_fact = {
      dimension: " ",
      source_roles: [],
      object_required: false,
      unit_family: "",
      exclusions: [],
    };
    rule.authority_sources = [];
    expect(issueFields(draft)).toEqual([
      "rule-1-id",
      "rule-1-version",
      "rule-1-domain",
      "rule-1-business-line",
      "rule-1-scenario",
      "rule-1-contract-type",
      "rule-1-threshold",
      "rule-1-unit",
      "rule-1-dimension",
      "rule-1-unit-family",
      "rule-1-roles",
      "rule-1-authority-heading",
    ]);
  });

  it("requires a capability on additional rules without removing the core rules", () => {
    const draft = validDraft();
    draft.rules.push({
      ...structuredClone(draft.rules[0]!),
      capability_id: " ",
    });
    expect(issueFields(draft)).toEqual(["rule-3-capability"]);
  });

  it("links incomplete authority fields to their actual rule and source indexes", () => {
    const draft = validDraft();
    draft.rules[2]!.authority_sources.push({
      id: " ",
      version: "\t",
      kind: "",
    });
    expect(issueFields(draft)).toEqual([
      "rule-2-authority-1-id",
      "rule-2-authority-1-version",
      "rule-2-authority-1-kind",
    ]);
  });

  it("leaves literal thresholds and optional comparator semantics for server validation", () => {
    const draft = validDraft();
    draft.rules[0]!.threshold.value = "0.000000000000000001";
    draft.rules[1]!.threshold.value = "{not an expression}";
    draft.rules[2]!.required_fact.object_required = false;
    expect(validateRulePackDraft(draft, "tenant-test")).toEqual([]);
    expect(draft.rules[0]!.threshold.value).toBe("0.000000000000000001");
    expect(draft.rules[1]!.threshold.value).toBe("{not an expression}");
  });
});
