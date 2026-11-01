import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReviewReadiness, ReviewScope } from "../api/review-readiness";
import { parseReviewWorkspace } from "../api/parse";
import type { ReviewWorkspace } from "../api/types";
import { ReviewReadinessPanel } from "./review-readiness-panel";
import { SingleContractScopeCoverage } from "./single-contract-scope-coverage";

const v1 = {
  id: "single_contract_precheck",
  rule_set_id: "single-contract-precheck",
  rule_set_version: "1",
  rule_set_hash: "51676a95611b6e1bf4c30fc50be82e1d493528f34b89f3ece9dbb5aa6c291596",
};

function workspace(options: {
  state?: string;
  history?: string;
  parseStatus?: string;
  reason?: string | null;
  ruleHash?: string;
  attemptRunId?: string;
  selectedParse?: string;
  full?: boolean;
} = {}): ReviewWorkspace {
  const identity = options.full ? undefined : { ...v1, rule_set_hash: options.ruleHash ?? v1.rule_set_hash };
  const state = options.state ?? "partial";
  const attempt = {
    id: "attempt-1", run_id: options.attemptRunId ?? "run-1", ordinal: 1,
    state, history_kind: options.history ?? "frozen", parsed_document_ids: [options.selectedParse ?? "parse-1"],
    review_scope: identity,
  };
  const reason = options.reason === undefined
    ? "主合同中识别到的付款比例合计 100%；规则只识别部分固定表达，无法确认是否遗漏条款或付款基数一致，请人工核对。"
    : options.reason;
  return parseReviewWorkspace({
    run: { id: "run-1", case_id: "case-1", state },
    review_attempts: { attempt, attempts: [attempt] },
    snapshot: { document_ids: ["contract-1"], review_scope: identity },
    documents: [{ id: "contract-1", role: "draft_contract", filename: "合同.txt", parse_status: options.parseStatus ?? "ready", parsed_document_id: "parse-1" }],
    evaluations: reason === null ? [] : [{
      id: "payment-evaluation", domain: "payment_sum", relation: "insufficient", rule_id: "single-payment-sum-v1",
      rule_version: "single-payment-sum-v1", capability_id: "single_contract.payment_sum", reason,
    }],
    findings: [], execution: null, audit_events: [],
  });
}

function readiness(): ReviewReadiness {
  return {
    case_id: "case-1", revision: "a".repeat(64), business_at: null, can_start: false,
    material_selection_version: 1, materials: [],
    scopes: [
      {
        id: "full_review", status: "blocked", can_start: false, rule_pack: null, rule_set: null, checks: [],
        blockers: [{ code: "required_materials_missing", status: "missing", message: "缺少对照材料", missing_dependencies: [], next_action: "补充材料" }],
      },
      {
        id: "single_contract_precheck", status: "ready", can_start: true, rule_pack: null, rule_set: v1, blockers: [],
        checks: [{ rule_id: "single-payment-sum-v1", status: "ready", missing_dependencies: [], next_action: "start_review" }],
      },
    ],
  };
}

function ReadinessHarness() {
  const [scope, setScope] = useState<ReviewScope>("single_contract_precheck");
  return <ReviewReadinessPanel caseId="case-1" data={readiness()} loading={false} error={null} onRefresh={() => {}}
    selectedScope={scope} onSelectScope={setScope} />;
}

describe("single contract coverage", () => {
  it("lets a user move from the planned precheck to the server-blocked full review", async () => {
    render(<ReadinessHarness />);
    const coverage = screen.getByRole("region", { name: "单合同预审范围" });
    expect(within(coverage).getByText("检查已准备，尚未执行")).toBeInTheDocument();
    expect(within(coverage).getByRole("link", { name: "补齐或确认对照材料" })).toHaveAttribute("href", "#case-materials");
    await userEvent.setup().click(within(coverage).getByRole("button", { name: "查看完整审查准备" }));
    expect(screen.getByRole("radio", { name: "完整审查" })).toBeChecked();
    expect(screen.getByText("所选范围需要先补齐以下事项")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "单合同预审范围" })).not.toBeInTheDocument();
  });

  it("does not advertise the v1 expressions when readiness returns an unknown identity", () => {
    const data = readiness();
    data.scopes[1]!.rule_set = { ...v1, rule_set_version: "2" };
    render(<ReviewReadinessPanel caseId="case-1" data={data} loading={false} error={null} onRefresh={() => {}}
      selectedScope="single_contract_precheck" onSelectScope={() => {}} />);
    const coverage = screen.getByRole("region", { name: "单合同预审范围" });
    expect(within(coverage).getByText("范围身份待核对")).toBeInTheDocument();
    expect(within(coverage).queryByText(/预付款20%/)).not.toBeInTheDocument();
    expect(screen.queryByText("主合同中可识别的付款比例合计")).not.toBeInTheDocument();
  });

  it("shows a persisted 100% observation as insufficient, with excluded work and a repair route", () => {
    render(<SingleContractScopeCoverage mode="workspace" workspace={workspace()} />);
    const coverage = screen.getByRole("region", { name: "本次单合同预审范围" });
    expect(within(coverage).getByText("已执行识别，信息不足")).toBeInTheDocument();
    expect(within(coverage).getByText("范围外，未执行")).toBeInTheDocument();
    expect(within(coverage).queryByText("已通过")).not.toBeInTheDocument();
    expect(within(coverage).getByRole("link", { name: "返回案件补齐对照材料" })).toHaveAttribute("href", "/cases/case-1#case-materials");
  });

  it("keeps an unrecognized expression as insufficient evidence after the rule ran", () => {
    const result = workspace({ reason: "未识别到支持的付款比例表达；不能推断合同未约定付款，需人工核对原文。" });
    render(<SingleContractScopeCoverage mode="workspace" workspace={result} />);
    const coverage = screen.getByRole("region", { name: "本次单合同预审范围" });
    expect(within(coverage).getByText("已执行识别，信息不足")).toBeInTheDocument();
    expect(within(coverage).queryByText("未执行付款识别")).not.toBeInTheDocument();
  });

  it("does not report identification as executed when the frozen contract was not parsed", () => {
    const result = workspace({ parseStatus: "failed", reason: "主合同未完成解析，付款比例检查未执行；请重新解析并核对原文。" });
    render(<SingleContractScopeCoverage mode="workspace" workspace={result} />);
    expect(screen.getByText("未执行付款识别")).toBeInTheDocument();
    expect(screen.queryByText("已执行识别，信息不足")).not.toBeInTheDocument();
  });

  it("retains a persisted payment observation if a later stage fails", () => {
    render(<SingleContractScopeCoverage mode="workspace" workspace={workspace({ state: "failed" })} />);
    expect(screen.getByText("已执行识别，信息不足")).toBeInTheDocument();
    expect(screen.queryByText("执行失败或中断")).not.toBeInTheDocument();
  });

  it("does not treat an incomplete evaluation identity as a recorded result", () => {
    const result = workspace();
    result.evaluations![0]!.capability_id = "";
    render(<SingleContractScopeCoverage mode="workspace" workspace={result} />);
    expect(screen.getByText("识别状态待核对")).toBeInTheDocument();
    expect(screen.queryByText("已执行识别，信息不足")).not.toBeInTheDocument();
  });

  it.each([
    ["running", "尚未返回识别结果"],
    ["failed", "执行失败或中断"],
    ["cancelled", "执行已取消"],
  ])("reports %s without fabricating a payment result", (state, label) => {
    render(<SingleContractScopeCoverage mode="workspace" workspace={workspace({ state, history: "preparing", reason: null })} />);
    expect(screen.getByText(label)).toBeInTheDocument();
    expect(screen.queryByText("已执行识别，信息不足")).not.toBeInTheDocument();
    expect(screen.getByText("范围外，未执行")).toBeInTheDocument();
  });

  it.each([
    { history: "legacy_unfrozen" },
    { ruleHash: "unknown-version-hash" },
    { attemptRunId: "different-run" },
  ])("does not infer coverage from an unknown or old identity", (options) => {
    render(<SingleContractScopeCoverage mode="workspace" workspace={workspace(options)} />);
    expect(screen.getByText("范围身份未确认")).toBeInTheDocument();
    expect(screen.queryByText("已执行识别，信息不足")).not.toBeInTheDocument();
    expect(screen.queryByText("范围外，未执行")).not.toBeInTheDocument();
  });

  it("does not infer coverage when a returned snapshot hash conflicts with its scope", () => {
    const result = workspace();
    result.snapshot!.rule_set_hash = "different-hash";
    render(<SingleContractScopeCoverage mode="workspace" workspace={result} />);
    expect(screen.getByText("范围身份未确认")).toBeInTheDocument();
    expect(screen.queryByText("已执行识别，信息不足")).not.toBeInTheDocument();
  });

  it("does not use a parsed document from another attempt to establish execution", () => {
    render(<SingleContractScopeCoverage mode="workspace" workspace={workspace({ selectedParse: "other-parse" })} />);
    expect(screen.getByText("识别状态待核对")).toBeInTheDocument();
    expect(screen.queryByText("已执行识别，信息不足")).not.toBeInTheDocument();
  });

  it("switches between the selected attempts' execution states", () => {
    const first = workspace();
    const second = workspace({ state: "cancelled", reason: null });
    second.review_attempts!.attempt.id = "attempt-2";
    second.review_attempts!.attempt.ordinal = 2;
    const view = render(<SingleContractScopeCoverage mode="workspace" workspace={first} />);
    expect(screen.getByText("已执行识别，信息不足")).toBeInTheDocument();
    view.rerender(<SingleContractScopeCoverage mode="workspace" workspace={second} />);
    expect(screen.getByText("执行已取消")).toBeInTheDocument();
    expect(screen.queryByText("已执行识别，信息不足")).not.toBeInTheDocument();
    view.rerender(<SingleContractScopeCoverage mode="workspace" workspace={first} />);
    expect(screen.getByText("已执行识别，信息不足")).toBeInTheDocument();
  });

  it("keeps the old precheck scope when switching to and back from a later review", () => {
    const prior = workspace();
    const view = render(<SingleContractScopeCoverage mode="workspace" workspace={prior} />);
    expect(screen.getByText("已执行识别，信息不足")).toBeInTheDocument();
    view.rerender(<SingleContractScopeCoverage mode="workspace" workspace={workspace({ full: true })} />);
    expect(screen.queryByRole("region", { name: "本次单合同预审范围" })).not.toBeInTheDocument();
    view.rerender(<SingleContractScopeCoverage mode="workspace" workspace={prior} />);
    const coverage = screen.getByRole("region", { name: "本次单合同预审范围" });
    expect(within(coverage).getByText("已执行识别，信息不足")).toBeInTheDocument();
    expect(within(coverage).getByText("范围外，未执行")).toBeInTheDocument();
  });
});
