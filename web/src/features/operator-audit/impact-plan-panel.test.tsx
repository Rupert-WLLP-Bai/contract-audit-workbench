import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DEMO_OPERATOR_AUDIT_FLAGS } from "./feature-flags";
import { impactPlan } from "./fixture";
import { ImpactPlanPanel } from "./impact-plan-panel";
import { renderOperatorAudit } from "./test-utils";

describe("impact plan panel", () => {
  it("is off by default", () => {
    renderOperatorAudit(<ImpactPlanPanel plan={impactPlan} />);
    expect(
      screen.queryByText("影响解释与影子对照"),
    ).not.toBeInTheDocument();
  });

  it("labels a reuse candidate as expected-only and never as already skipped", () => {
    renderOperatorAudit(
      <ImpactPlanPanel
        plan={impactPlan}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    expect(screen.getByText("影子对照（shadow）")).toBeInTheDocument();
    expect(screen.getByText("重新计算（recompute）")).toBeInTheDocument();
    expect(
      screen.getByText("预计可复用（尚未跳过）"),
    ).toBeInTheDocument();
    expect(screen.getByText("仅候选，未跳过执行")).toBeInTheDocument();
    expect(screen.getByText("不可复用（deny）")).toBeInTheDocument();
    expect(screen.getByText(/依赖字段发生变化/)).toBeInTheDocument();
    expect(screen.getByText(/依赖未变化且完整/)).toBeInTheDocument();
    expect(screen.getByText(/授权已撤销/)).toBeInTheDocument();
    expect(screen.getByText("依赖完整")).toBeInTheDocument();
    expect(screen.getByText("无需回退")).toBeInTheDocument();
    expect(screen.getByText("full（完整执行）")).toBeInTheDocument();
    expect(
      screen.getByText(
        "“预计可复用”只是候选，本次没有跳过任何检查；业务结果仍来自完整执行。",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("增量复用开关未启用，所有检查仍按完整执行计算。"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/已经跳过/)).not.toBeInTheDocument();
    expect(
      screen.getByText(/合成 fixture 计划：仅用于演示与测试/),
    ).toBeInTheDocument();
  });

  it("shows the full fallback when dependencies are incomplete", () => {
    renderOperatorAudit(
      <ImpactPlanPanel
        plan={{
          ...impactPlan,
          mode: "explain",
          dependencyComplete: false,
          fallback: "full",
          fallbackReason: "dependency_incomplete",
        }}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    expect(screen.getByText("依赖不完整")).toBeInTheDocument();
    expect(screen.getByText("回退全量（full）")).toBeInTheDocument();
    expect(
      screen.getByText(/依赖不完整，按完整执行重新计算，不采用复用/),
    ).toBeInTheDocument();
  });

  it("warns when the business result source is not the full execution", () => {
    renderOperatorAudit(
      <ImpactPlanPanel
        plan={{
          ...impactPlan,
          fallback: "deny",
          businessResultSource: "shadow",
        }}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      /业务结果来源异常（shadow）/,
    );
    expect(
      screen.getByText(/依赖或授权无法证明，拒绝复用/),
    ).toBeInTheDocument();
    expect(screen.queryByText("full（完整执行）")).not.toBeInTheDocument();
  });

  it("keeps unknown mode, dependency state and fallback unknown", () => {
    renderOperatorAudit(
      <ImpactPlanPanel
        plan={{
          ...impactPlan,
          mode: "unknown",
          dependencyComplete: null,
          fallback: "unknown",
        }}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    expect(screen.getByText("模式未报告")).toBeInTheDocument();
    expect(screen.getByText("依赖完整性未报告")).toBeInTheDocument();
    expect(screen.getByText("回退策略未报告")).toBeInTheDocument();
    expect(
      screen.getByText("模式未报告；不视为可执行计划。"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("依赖完整性未报告，不能据此复用。"),
    ).toBeInTheDocument();
  });

  it("explains rather than silently accepting a server shadow plan", () => {
    renderOperatorAudit(
      <ImpactPlanPanel plan={impactPlan} flags={DEMO_OPERATOR_AUDIT_FLAGS} />,
    );
    expect(
      screen.getByText(
        "当前开关只启用解释模式，服务端 shadow 结果仅作对照，不改变业务结果。",
      ),
    ).toBeInTheDocument();
  });
});
