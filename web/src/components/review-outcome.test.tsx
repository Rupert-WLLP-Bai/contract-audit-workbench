// File metadata (manifest mirror):
// Purpose: Verifies review outcome presentation and next-step guidance for limited prechecks, failed stages, missing inputs, budgets, unsupported checks and stale authorization context.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import type { ComponentProps } from "react";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { parseReviewWorkspace } from "../api/parse";
import type { CaseDetail, ReviewWorkspace } from "../api/types";
import type { ReviewReadiness } from "../api/review-readiness";
import { renderWithAntd as render } from "../test/antd";
import { CaseNextStep } from "./case-next-step";
import { ReviewOutcomeSummary } from "./review-outcome-summary";
import { canReadmitCancelledPreparation, canRestartUserCancelled, hasTransientRetryCause } from "../features/review/review-outcome";

const scope = { id: "single_contract_precheck", rule_set_id: "single-contract-precheck", rule_set_version: "1", rule_set_hash: "51676a95611b6e1bf4c30fc50be82e1d493528f34b89f3ece9dbb5aa6c291596" };

function workspace(): ReviewWorkspace {
  const attempt = { id: "attempt-1", run_id: "run-1", ordinal: 1, state: "partial", history_kind: "frozen", parsed_document_ids: ["parse-1"], review_scope: scope,
    outcome: { reason: "scope_limited", agent_stage: "skipped", retry_cause: "none" } };
  return parseReviewWorkspace({
    run: { id: "run-1", case_id: "case-1", state: "partial" },
    review_attempts: { attempt, attempts: [attempt] },
    snapshot: { document_ids: ["doc-1"], review_scope: scope },
    documents: [{ id: "doc-1", role: "draft_contract", filename: "合同.txt", parse_status: "ready", parsed_document_id: "parse-1" }],
    evaluations: [{ id: "eval-1", domain: "payment_sum", relation: "insufficient", rule_id: "single-payment-sum-v1", rule_version: "single-payment-sum-v1", capability_id: "single_contract.payment_sum", reason: "请核对原文" }],
    findings: [{ id: "finding-1", decisions: [], evidence: [] }], execution: null, audit_events: [],
  });
}

function detail(): CaseDetail {
  return {
    id: "case-1", title: "合同", contract_type: "service", created_at: "", updated_at: "",
    documents: [], materials: [], material_versions: [],
    material_selection: { version: 1, status: "not_applicable", document_ids: [], primary_material_id: null },
    members: [], latest_run: { id: "run-1", state: "partial", updated_at: "" }, finding_counts: null,
    activity: [], subject_context_changed_after_latest_run: false, business_line: "ENT", scenario: "telecom", business_status: "active",
    generations: [], remediations: [], reports: [], archives: [], archived_at: null,
  };
}

function readiness(ready = true): ReviewReadiness {
  return { case_id: "case-1", revision: "revision", business_at: null, can_start: ready, material_selection_version: 1, materials: [],
    scopes: [{ id: "full_review", status: ready ? "ready" : "blocked", can_start: ready, rule_pack: null, rule_set: null, checks: [],
      blockers: ready ? [] : [{ code: "required_materials_missing", status: "missing", message: "缺少采购材料", missing_dependencies: [], next_action: "补充采购材料" }] }] };
}

function props(overrides: Partial<ComponentProps<typeof CaseNextStep>> = {}): ComponentProps<typeof CaseNextStep> {
  return { caseDetail: detail(), actorRole: "reviewer", sessionReady: true, sessionLoading: false, sessionError: null,
    readiness: readiness(), readinessLoading: false, readinessError: null, selectedScope: "full_review",
    businessAtInput: "", validBusinessAt: false, historyLoading: false, historyError: null, latestWorkspace: workspace(),
    startPending: false, startError: null, onRetrySession: vi.fn(), onRefreshReadiness: vi.fn(), onRefreshHistory: vi.fn(), onStartReview: vi.fn(), ...overrides };
}

describe("selected review outcome", () => {
  it("separates a finished limited precheck, excluded work, and human progress", () => {
    render(<ReviewOutcomeSummary workspace={workspace()} />);
    const summary = screen.getByRole("region", { name: "审查结果、范围与人工进度" });
    expect(summary).toHaveTextContent("单合同预审已结束，执行记录保留为部分完成");
    expect(summary).toHaveTextContent("已执行主合同付款比例识别，信息仍不足");
    expect(summary).toHaveTextContent("其他风险不在本次单合同预审范围，未执行");
    expect(summary).toHaveTextContent("1 项尚无人工判断");
    expect(summary).toHaveTextContent("智能审查阶段未执行");
    expect(summary).toHaveTextContent("未检查的范围不能据此认定合规");
    expect(within(summary).getByRole("link", { name: "查看完整审查准备" })).toHaveAttribute("href", "/cases/case-1#review-readiness");
  });

  it.each(["failed", "partial"])("keeps an agent %s visible alongside the limited scope", (stage) => {
    const value = workspace();
    value.review_attempts!.attempt.outcome!.agent_stage = stage;
    render(<ReviewOutcomeSummary workspace={value} />);
    expect(screen.getByText(stage === "failed" ? /智能审查执行失败/ : /智能审查存在未完成/)).toBeInTheDocument();
    expect(screen.getByText(/单合同预审已结束/)).toBeInTheDocument();
    expect(hasTransientRetryCause(value)).toBe(false);
  });

  it("opens full review only after the current server readiness allows it", async () => {
    const start = vi.fn();
    const view = render(<CaseNextStep {...props({ readiness: readiness(false), onStartReview: start })} />);
    expect(screen.getByRole("link", { name: "转到材料集合" })).toHaveAttribute("href", "#case-materials");
    expect(screen.queryByRole("button", { name: "开始完整审查" })).not.toBeInTheDocument();
    view.rerender(<CaseNextStep {...props({ onStartReview: start })} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "开始完整审查" }));
    expect(start).toHaveBeenCalledTimes(1);
  });

  it("keeps full review read-only on the home entry and when readiness is stale", () => {
    const view = render(<CaseNextStep {...props({ suppressWriteAction: true })} />);
    expect(screen.getByRole("link", { name: "打开案件继续" })).toHaveAttribute("href", "/cases/case-1#case-next-step-title");
    expect(screen.queryByRole("button", { name: "开始完整审查" })).not.toBeInTheDocument();
    view.rerender(<CaseNextStep {...props({ readinessLoading: true })} />);
    expect(screen.queryByRole("button", { name: "开始完整审查" })).not.toBeInTheDocument();
  });

  it("does not classify old, foreign, or contradictory attempts as a finished precheck", () => {
    const value = workspace();
    value.review_attempts!.attempt.history_kind = "legacy_unfrozen";
    const view = render(<CaseNextStep {...props({ latestWorkspace: value })} />);
    expect(screen.queryByText(/单合同预审已结束/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "开始完整审查" })).not.toBeInTheDocument();
    value.review_attempts!.attempt.history_kind = "frozen";
    value.review_attempts!.attempt.run_id = "other-run";
    view.rerender(<CaseNextStep {...props({ latestWorkspace: value })} />);
    expect(screen.queryByRole("region", { name: "审查结果、范围与人工进度" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "开始完整审查" })).not.toBeInTheDocument();
  });

  it.each(["scope_limited", "review_incomplete"])("distinguishes missing material, budget and unsupported checks for %s", (reason) => {
    const value = workspace();
    value.review_attempts!.attempt.outcome = { reason, agent_stage: "partial", retry_cause: "none" };
    value.agent_review = { attempt_id: "attempt-1", status: "partial", rulepack: null, scope: null, domains: null, evidence: null, observations: null,
      unresolved_gap_ids: ["material", "budget", "unsupported"], gaps: [
        { gap_id: "material", domain: "technical", capability_id: "x", mandatory: true, status: "open", reason_code: "required_source_or_field_missing", evidence_ids: [] },
        { gap_id: "budget", domain: "technical", capability_id: "y", mandatory: true, status: "open", reason_code: "max_calls_reached", evidence_ids: [] },
        { gap_id: "unsupported", domain: "technical", capability_id: "z", mandatory: true, status: "open", reason_code: "capability_not_registered", evidence_ids: [] },
      ] };
    render(<CaseNextStep {...props({ latestWorkspace: value })} />);
    expect(screen.getByRole("heading", { name: "先补充所需材料或字段" })).toBeInTheDocument();
    expect(screen.getByText(/调用预算耗尽/)).toBeInTheDocument();
    expect(screen.getByText(/部分检查能力或来源表达暂不支持/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "查看并处理材料" })).toHaveAttribute("href", "#case-materials");
    expect(hasTransientRetryCause(value)).toBe(false);
  });

  it("does not recommend retrying a timeout when the selected frozen material failed parsing", () => {
    const value = workspace();
    value.review_attempts!.attempt.outcome = { reason: "transient_failure", agent_stage: "unknown", retry_cause: "model_timeout" };
    value.documents[0]!.parse_status = "failed";
    render(<CaseNextStep {...props({ latestWorkspace: value })} />);
    expect(screen.getByRole("heading", { name: "先核对未完成解析的材料" })).toBeInTheDocument();
    expect(screen.getByText(/冻结材料或当前条件尚需核对/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "查看并处理材料" })).toHaveAttribute("href", "#case-materials");
    expect(hasTransientRetryCause(value)).toBe(false);
  });

  it("offers the original workspace after user cancellation without requiring a frozen result", () => {
    const value = workspace();
    value.run.state = "cancelled";
    value.review_attempts!.attempt.state = "cancelled";
    value.review_attempts!.attempt.outcome = { reason: "cancelled_by_user", agent_stage: "unknown", retry_cause: "none" };
    const item = detail();
    item.latest_run!.state = "cancelled";
    expect(value.review_attempts!.attempt.frozen_result_available).toBe(false);
    expect(canRestartUserCancelled(value)).toBe(true);
    render(<CaseNextStep {...props({ caseDetail: item, latestWorkspace: value })} />);
    expect(screen.getByRole("link", { name: "打开已取消审查" })).toHaveAttribute("href", "/cases/case-1/reviews/run-1");
    expect(screen.queryByRole("button", { name: "开始完整审查" })).not.toBeInTheDocument();
    expect(hasTransientRetryCause(value)).toBe(false);
  });

  it("does not offer a user restart for legacy, revoked, or missing frozen inputs", () => {
    const value = workspace();
    value.run.state = "cancelled";
    value.review_attempts!.attempt.state = "cancelled";
    value.review_attempts!.attempt.outcome = { reason: "cancelled_by_user", agent_stage: "unknown", retry_cause: "none" };
    value.review_attempts!.attempt.history_kind = "legacy_unfrozen";
    expect(canRestartUserCancelled(value)).toBe(false);
    value.review_attempts!.attempt.history_kind = "frozen";
    value.review_attempts!.attempt.outcome.reason = "authorization_revoked";
    expect(canRestartUserCancelled(value)).toBe(false);
    value.review_attempts!.attempt.outcome.reason = "cancelled_by_user";
    value.review_attempts!.attempt.parsed_document_ids = [];
    expect(canRestartUserCancelled(value)).toBe(false);
  });

  it("offers a fresh admission only after a user-cancelled preparation passes readiness", async () => {
    const value = workspace();
    value.run.state = "cancelled";
    value.review_attempts!.attempt.state = "cancelled";
    value.review_attempts!.attempt.history_kind = "preparing";
    // Ready parse identities alone do not prove that AttemptInput was frozen.
    value.review_attempts!.attempt.parsed_document_ids = ["parse-1"];
    value.review_attempts!.attempt.outcome = { reason: "cancelled_by_user", agent_stage: "unknown", retry_cause: "none" };
    const item = detail();
    item.latest_run!.state = "cancelled";
    const start = vi.fn();
    expect(canReadmitCancelledPreparation(value)).toBe(true);
    expect(canRestartUserCancelled(value)).toBe(false);
    const view = render(<CaseNextStep {...props({ caseDetail: item, latestWorkspace: value, readiness: readiness(false), onStartReview: start })} />);
    expect(screen.getByText(/尚未冻结审查输入/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "重新发起新审查" })).not.toBeInTheDocument();
    view.rerender(<CaseNextStep {...props({ caseDetail: item, latestWorkspace: value, onStartReview: start })} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "重新发起新审查" }));
    expect(start).toHaveBeenCalledTimes(1);
    view.rerender(<CaseNextStep {...props({ caseDetail: item, latestWorkspace: value, actorRole: "viewer" })} />);
    expect(screen.queryByRole("button", { name: "重新发起新审查" })).not.toBeInTheDocument();
    value.review_attempts!.attempt.outcome!.reason = "authorization_revoked";
    expect(canReadmitCancelledPreparation(value)).toBe(false);
    value.review_attempts!.attempt.outcome!.reason = "cancelled_by_user";
    value.review_attempts!.attempts.push({ ...value.review_attempts!.attempt, id: "newer", ordinal: 2 });
    expect(canReadmitCancelledPreparation(value)).toBe(false);
  });
});
