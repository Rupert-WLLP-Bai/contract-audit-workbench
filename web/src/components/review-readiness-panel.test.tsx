// File metadata (manifest mirror):
// Purpose: Verifies business-scope and published-rule preparation presentation, additive readiness parsing, and repair links while preserving server-derived admission decisions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-15
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import userEvent from "@testing-library/user-event";
import { render, screen, within } from "@testing-library/react";
import { parseReviewReadiness } from "../api/review-readiness";
import { ReviewReadinessPanel } from "./review-readiness-panel";

const cloud = { business_line: "ENT", scenario: "cloud_services", contract_type: "service" };
function response() {
  return {
    case_id: "case-1",
    revision: "a".repeat(64),
    business_at: "2026-10-31T10:00:00Z",
    can_start: false,
    material_selection_version: 1,
    materials: [
      {
        identity_kind: "material_entity",
        material_id: "mat-1",
        version: 1,
        document_id: "doc-1",
        filename: "合成合同.txt",
        role: "draft_contract",
        purpose: "primary_contract",
        selected: true,
        parse: { status: "ready", next_action: "ready_for_review" },
      },
    ],
    scopes: [
      {
        id: "full_review",
        status: "blocked",
        can_start: false,
        rule_pack: {
          pack_id: "opaque-pack",
          version: "7",
          snapshot_digest: "b".repeat(64),
          applicable_scopes: [cloud],
        },
        checks: [
          {
            rule_id: "machine-rule",
            comparator: "minimum_bandwidth",
            source_roles: ["draft_contract", "procurement"],
            status: "missing_basis",
            missing_dependencies: ["legal_authority:law-id@v1", "material_role:procurement"],
            next_action: "provide_missing_material_or_legal_basis",
          },
        ],
        blockers: [
          {
            code: "governed_dependencies_incomplete",
            status: "blocked",
            message: "强制制度待核对",
            missing_dependencies: [],
            next_action: "补齐制度",
          },
        ],
      },
    ],
  };
}

function show(value: unknown = response(), business = cloud) {
  const data = parseReviewReadiness(value, "case-1");
  render(
    <ReviewReadinessPanel
      caseId="case-1"
      data={data}
      loading={false}
      error={null}
      onRefresh={() => {}}
      selectedScope="full_review"
      onSelectScope={() => {}}
      business={business}
    />,
  );
}

describe("review preparation business and rule matching", () => {
  it("puts repairs first and keeps parsed material in the ready disclosure", async () => {
    show();
    const panel = within(screen.getByRole("region", { name: "审查准备清单" }));
    expect(panel.getByText("规则包 v7")).toBeVisible();
    const issues = within(panel.getByRole("list", { name: "待处理事项" }));
    expect(issues.getByText("最低保障带宽对照")).toBeVisible();
    expect(issues.getByRole("link", { name: "补齐或确认材料" })).toHaveAttribute(
      "href",
      "#case-materials",
    );
    expect(issues.getByRole("link", { name: "核对法律依据与制度" })).toHaveAttribute(
      "href",
      "/cases/case-1/legal-knowledge",
    );
    expect(
      panel.queryByText(/规则声明的事实材料角色|案件登记业务|治理名称未登记|machine-rule/),
    ).not.toBeInTheDocument();
    const detail = panel.getByText(/份已解析送审材料/).closest("details");
    expect(detail).not.toHaveAttribute("open");
    await userEvent.setup().click(panel.getByText(/份已解析送审材料/));
    expect(detail).toHaveAttribute("open");
    expect(panel.getByText("合成合同.txt · 已解析")).toBeVisible();
  });

  it("does not claim a match when the declaration differs from the registered business", () => {
    show(response(), { ...cloud, scenario: "telecom_dedicated_line" });
    expect(screen.getByText(/规则范围未确认/)).toBeVisible();
    expect(screen.queryByText("规则包 v7")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "核对规则包" })).toHaveAttribute(
      "href",
      "/admin/rulepacks",
    );
  });

  it("explains absent rules and tolerates older responses without the optional projection", () => {
    const value = response();
    const older = {
      ...value,
      scopes: [
        {
          ...value.scopes[0]!,
          rule_pack: undefined,
          checks: [],
          blockers: [
            {
              code: "business_at_required",
              status: "missing",
              message: "需要业务时间",
              missing_dependencies: [],
              next_action: "填写时间",
            },
          ],
        },
      ],
    };
    show(older);
    expect(screen.getByText("需要业务时间")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "填写业务时间" })).toHaveAttribute(
      "href",
      "#review-business-time",
    );
  });

  it("does not infer a scope or a rule name from identifiers in an older response", () => {
    const value = response();
    const { pack_id, version, snapshot_digest } = value.scopes[0]!.rule_pack;
    const { rule_id, status, missing_dependencies, next_action } = value.scopes[0]!.checks[0]!;
    show({
      ...value,
      scopes: [
        {
          ...value.scopes[0]!,
          rule_pack: { pack_id, version, snapshot_digest },
          checks: [{ rule_id, status, missing_dependencies, next_action }],
        },
      ],
    });
    expect(screen.getByText(/规则范围未确认/)).toBeInTheDocument();
    expect(screen.getByText("检查项目名称待核对")).toBeInTheDocument();
    expect(screen.queryByText(/其中一项与案件登记分类一致/)).not.toBeInTheDocument();
  });

  it("rejects malformed declared scope instead of inventing a match", () => {
    const value = response();
    const invalid = {
      ...value,
      scopes: [
        {
          ...value.scopes[0]!,
          rule_pack: {
            ...value.scopes[0]!.rule_pack,
            applicable_scopes: [{ ...cloud, scenario: 123 }],
          },
        },
      ],
    };
    expect(() => parseReviewReadiness(invalid, "case-1")).toThrow();
  });

  it("keeps unknown codes private and never counts an unknown state as ready", () => {
    const value = response();
    const check = value.scopes[0]!.checks[0]!;
    show(
      {
        ...value,
        scopes: [
          {
            ...value.scopes[0]!,
            checks: [
              {
                ...check,
                rule_id: "unknown-rule",
                comparator: "minimum_bandwidth（旧）",
                status: "ready·",
                next_action: "start_review（测试）",
              },
              {
                ...check,
                rule_id: "rule-2",
                next_action: "本范围不执行正式治理规则；补齐材料后发起完整审查",
              },
            ],
          },
        ],
      },
      { ...cloud, business_line: "ENT（测试）", scenario: "telecom_dedicated_line（旧）" },
    );
    expect(screen.getByText(/规则范围未确认/)).toBeVisible();
    const issues = within(screen.getByRole("list", { name: "待处理事项" }));
    expect(issues.getByText("检查项目名称待核对")).toBeVisible();
    expect(issues.getByText("最低保障带宽对照")).toBeVisible();
    expect(issues.getAllByText(/适用依据待确认/)).toHaveLength(2);
    expect(screen.getByText(/1 项已就绪/)).toBeInTheDocument();
    expect(
      screen.queryByText(/unknown-rule|ENT（测试）|start_review（测试）/),
    ).not.toBeInTheDocument();
  });
});
