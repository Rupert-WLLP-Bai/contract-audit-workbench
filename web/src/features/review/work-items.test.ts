// File metadata (manifest mirror):
// Purpose: Verifies work-item source identity, independent facts, incomplete states, explicit skipped review stages and actor-scoped local return-path validation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-05
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { EvidenceRequest } from "../../api/evidence-workflow";
import type { CaseDetail, ConstraintEvaluation, ExecutionAttempt, Finding, Remediation, ReviewWorkspace } from "../../api/types";
import { projectReviewWork, readWorkReturn, reviewReturnPath, workReturnState } from "./work-items";

const time = "2026-11-05T00:00:00Z";
const attempt: ExecutionAttempt = {
  id: "attempt-new", run_id: "run-new", ordinal: 2, retry_of_attempt_id: null, state: "completed",
  accepted_at: time, started_at: time, finished_at: time, parsed_document_ids: [], input_hash: null,
  output_hash: null, error_code: null, history_kind: "frozen",
};
const finding = (id: string, origin: string): Finding => ({
  id, origin, domain: "commercial", claim: "同一条款", relation: "conflict", confidence: null,
  verification_status: "unverified", evidence: [], unverified_reason: null, version: 1,
  severity: null, severity_source: null, decisions: [],
});
const gap = (id: string, reason: string, status = "insufficient") => ({
  gap_id: id, domain: "commercial", capability_id: "payment", mandatory: true, status, reason_code: reason, evidence_ids: [],
});
function workspace(): ReviewWorkspace {
  return {
    run: { id: "run-new", case_id: "case-1", state: "completed", updated_at: time, started_at: time, model_ref: null, partial_reason: null, failure_message: null },
    documents: [], findings: [finding("f-rule", "rule"), finding("f-model", "model")],
    review_attempts: { attempt, attempts: [attempt] }, execution: null, audit_events: [], evaluations: [],
    agent_review: { attempt_id: "attempt-new", status: "partial", rulepack: null, scope: [], domains: [],
      gaps: [gap("g1", "required_source_or_field_missing"), gap("g2", "max_calls_reached", "unknown")],
      unresolved_gap_ids: ["g1", "g2"], evidence: [], observations: [] },
  };
}
function request(id: string, gapId: string, run = "run-new", sourceAttempt = "attempt-new"): EvidenceRequest {
  return {
    id, case_id: "case-1", task_id: `task-${id}`, task_version: 1, assignee_id: "person", due_at: time,
    source_run_id: run, source_attempt_id: sourceAttempt, source_evaluation: null,
    source_gap: { gap_id: gapId, domain: "commercial", capability_id: "payment", status: "insufficient", reason_code: "required_source_or_field_missing" },
    question: "补充来源", material_role: null, verification_condition: "resolved", status: "open", version: 1,
    created_by: "person", created_at: time, updated_at: time, verification: null,
  };
}
function remediation(id: string, status = "open"): Remediation {
  return { id, case_id: "case-1", run_id: "run-old", attempt_id: "attempt-old", finding_ids: ["old-finding"], assignee_id: "person", due_at: time,
    measure: "处理修改稿", status, version: 1, document_ids: [], reason: "", verification_run_id: null, verification_attempt_id: null, created_at: time, updated_at: time };
}
function detail(): CaseDetail {
  return { id: "case-1", title: "案件", contract_type: "service", created_at: time, updated_at: time, documents: [], members: [],
    latest_run: { id: "run-new", state: "completed", updated_at: time }, finding_counts: null, activity: [], business_line: "", scenario: "", business_status: "active",
    generations: [], remediations: [remediation("rem-old")], reports: [], archives: [], archived_at: null };
}

describe("人工办理投影", () => {
  it("groups only an identical source projection, while preserving independent requests, findings, and old-scope remediation", () => {
    const result = projectReviewWork(workspace(), detail(), [request("r1", "g1"), request("r2", "g1"), request("r-old", "g1", "run-old", "attempt-old")]);
    expect(result.items.filter((item) => item.category === "judgment").map((item) => item.target?.id)).toEqual(["f-rule", "f-model"]);
    const evidence = result.items.filter((item) => item.category === "evidence");
    expect(evidence.filter((item) => item.target?.kind === "request")).toHaveLength(3);
    expect(evidence.filter((item) => item.target?.kind === "gap" && item.target.id === "g1")).toHaveLength(0);
    expect(evidence.filter((item) => item.sourceTarget?.id === "g1")).toHaveLength(3);
    expect(evidence.find((item) => item.target?.id === "r-old")?.sourceTarget).toMatchObject({ runId: "run-old", attemptId: "attempt-old" });
    expect(evidence.find((item) => item.target?.id === "g2")).toMatchObject({ needsCheck: true, verification: expect.stringContaining("调用预算") });
    expect(result.items.find((item) => item.target?.id === "rem-old")).toMatchObject({ sourceRunId: "run-old", sourceAttemptId: "attempt-old" });
  });

  it("changes pending counts only when a new server projection says the request is verified", () => {
    const open = request("r1", "g1");
    const before = projectReviewWork(workspace(), detail(), [open]);
    expect(before.items.some((item) => item.target?.id === "r1")).toBe(true);
    const verified = { ...open, status: "verified", verification: { run_id: "run-new", attempt_id: "attempt-new", receipt_id: "receipt", evaluation_id: null, relation: null, gap_id: "g1", gap_status: "resolved", evidence: [], reason: "核对", verified_by: "person", verified_at: time } };
    const after = projectReviewWork(workspace(), detail(), [verified]);
    expect(after.items.some((item) => item.target?.id === "r1" || (item.target?.kind === "gap" && item.target.id === "g1"))).toBe(false);
    expect(workspace().agent_review?.gaps?.[0]?.status).toBe("insufficient");
  });

  it("does not merge duplicate source IDs, missing identity, mismatched scope, or unknown statuses", () => {
    const duplicate = workspace();
    duplicate.agent_review!.gaps = [gap("g1", "required_source_or_field_missing"), gap("g1", "required_source_or_field_missing")];
    duplicate.agent_review!.unresolved_gap_ids = ["g1"];
    const uncertain = { ...request("r1", "g1"), status: "future-status" };
    const result = projectReviewWork(duplicate, detail(), [uncertain]);
    expect(result.items.filter((item) => item.origin === "审查缺口")).toHaveLength(2);
    expect(result.items.find((item) => item.target?.id === "r1")).toMatchObject({ needsCheck: true, sourceTarget: null });
    const conflicting = request("conflict", "g1");
    conflicting.source_gap!.domain = "other-domain";
    const mismatch = projectReviewWork(workspace(), detail(), [conflicting]);
    expect(mismatch.items.find((item) => item.target?.id === "conflict")).toMatchObject({ needsCheck: true, sourceTarget: null });
    expect(mismatch.items.some((item) => item.target?.kind === "gap" && item.target.id === "g1")).toBe(true);
    for (const badAttempt of [{ ...attempt, history_kind: "unexpected" }, { ...attempt, id: "" }, { ...attempt, run_id: "other" }]) {
      const input = workspace();
      input.review_attempts = { attempt: badAttempt, attempts: [badAttempt] };
      const projected = projectReviewWork(input, detail(), []);
      expect(projected.frozenScope).toBe(false);
      expect(projected.items.filter((item) => item.category === "judgment")).toHaveLength(0);
    }
    const input = workspace();
    input.agent_review!.attempt_id = "other";
    expect(projectReviewWork(input, detail(), []).sourcesKnown).toBe(false);
    expect(projectReviewWork(workspace(), { ...detail(), id: "other" }, []).caseKnown).toBe(false);
  });

  it.each([
    ["gap status", { status: "unknown" }],
    ["gap reason", { reason_code: "max_calls_reached" }],
  ])("keeps contradictory %s snapshots as independent items requiring review", (_label, change) => {
    const conflicting = request("different", "g1");
    conflicting.source_gap = { ...conflicting.source_gap!, ...change };
    const result = projectReviewWork(workspace(), detail(), [conflicting]);
    expect(result.items.find((item) => item.target?.id === "different")).toMatchObject({ needsCheck: true, sourceTarget: null });
    expect(result.items.find((item) => item.target?.kind === "gap" && item.target.id === "g1")).toMatchObject({ needsCheck: true });
  });

  it.each([
    ["relation", { relation: "unknown" }],
    ["reason", { reason: "另一检查原因" }],
    ["objects", { object_refs: ["object-b"] }],
    ["missing objects", { object_refs: [] }],
    ["ambiguous objects", { object_refs: ["object-a", "object-a"] }],
  ])("keeps contradictory evaluation %s snapshots as independent items requiring review", (_label, change) => {
    const input = workspace();
    const evaluation: ConstraintEvaluation = { id: "eval-1", domain: "commercial", relation: "insufficient", rule_version: "1",
      reason: "需要核对金额", rule_id: "rule-1", capability_id: "payment", object_refs: ["object-a"], finding_id: null, reason_code: "missing", fact_ids: [] };
    input.evaluations = [evaluation];
    const conflicting: EvidenceRequest = { ...request("different", "g1"), source_gap: null, source_evaluation: { ...evaluation, ...change } };
    const result = projectReviewWork(input, detail(), [conflicting]);
    expect(result.items.find((item) => item.target?.id === "different")).toMatchObject({ needsCheck: true, sourceTarget: null });
    expect(result.items.find((item) => item.target?.kind === "evaluation" && item.target.id === "eval-1")).toMatchObject({ needsCheck: true });
  });

  it("retains independent source and conflicting request even when another request matches", () => {
    const conflicting = request("conflict", "g1");
    conflicting.source_gap!.status = "unknown";
    const result = projectReviewWork(workspace(), detail(), [request("matching", "g1"), conflicting]);
    expect(result.items.find((item) => item.target?.id === "matching")?.sourceTarget?.id).toBe("g1");
    expect(result.items.find((item) => item.target?.id === "conflict")).toMatchObject({ needsCheck: true, sourceTarget: null });
    expect(result.items.find((item) => item.target?.kind === "gap" && item.target.id === "g1")).toMatchObject({ needsCheck: true });
  });

  it("still groups matching evaluation snapshots with an empty object set, including a verified request", () => {
    const input = workspace();
    const evaluation: ConstraintEvaluation = { id: "eval-empty", domain: "commercial", relation: "insufficient", rule_version: "1",
      reason: "需要核对", rule_id: "rule-1", capability_id: "payment", object_refs: [], finding_id: null, reason_code: "missing", fact_ids: [] };
    input.evaluations = [evaluation];
    const matching: EvidenceRequest = { ...request("matching", "g1"), source_gap: null, source_evaluation: evaluation };
    const open = projectReviewWork(input, detail(), [matching]);
    expect(open.items.find((item) => item.target?.id === "matching")?.sourceTarget?.id).toBe("eval-empty");
    expect(open.items.some((item) => item.target?.kind === "evaluation" && item.target.id === "eval-empty")).toBe(false);
    const optional = { ...evaluation, id: "eval-optional", rule_id: "", capability_id: "" };
    input.evaluations = [optional];
    const optionalRequest = { ...matching, source_evaluation: optional };
    expect(projectReviewWork(input, detail(), [optionalRequest]).items.find((item) => item.target?.id === "matching")?.sourceTarget?.id).toBe("eval-optional");
    input.evaluations = [evaluation];
    const verified = { ...matching, status: "verified", verification: { run_id: "run-new", attempt_id: "attempt-new", receipt_id: "receipt",
      evaluation_id: "eval-empty", relation: "compatible", gap_id: null, gap_status: null, evidence: [], reason: "核对", verified_by: "person", verified_at: time } };
    expect(projectReviewWork(input, detail(), [verified]).items.some((item) => item.target?.id === "matching" || item.target?.id === "eval-empty")).toBe(false);
  });

  it("preserves the actual reason for budget, unsupported capability, and manual source reconciliation", () => {
    const input = workspace();
    input.agent_review!.gaps = [gap("budget", "max_calls_reached", "unknown"), gap("capability", "capability_not_registered", "unsupported"),
      gap("expression", "source_expression_not_supported", "unsupported"), gap("many", "multiple_source_values_require_review", "unknown")];
    const result = projectReviewWork(input, detail(), []);
    expect(result.items.find((item) => item.target?.id === "budget")?.verification).toContain("调用预算");
    expect(result.items.find((item) => item.target?.id === "capability")?.verification).toContain("能力尚不支持");
    expect(result.items.find((item) => item.target?.id === "expression")?.verification).toContain("不支持来源中的表达形式");
    expect(result.items.find((item) => item.target?.id === "many")?.verification).toContain("人工核对");
    expect(result.items.filter((item) => item.category === "evidence").every((item) => item.needsCheck)).toBe(true);
  });

  it("distinguishes a reported skipped Agent stage from missing or mismatched Agent results", () => {
    const input = workspace();
    input.agent_review = null;
    expect(projectReviewWork(input, detail(), []).sourcesKnown).toBe(false);
    input.review_attempts!.attempt = { ...attempt, outcome: { reason: "scope_limited", agent_stage: "skipped", retry_cause: "" } };
    expect(projectReviewWork(input, detail(), [])).toMatchObject({ sourcesKnown: true, agentSkipped: true });
    input.agent_review = { ...workspace().agent_review!, attempt_id: "other" };
    expect(projectReviewWork(input, detail(), [])).toMatchObject({ sourcesKnown: false, agentSkipped: false });
  });
});

describe("办理返回路径", () => {
  it("retains only local same-case review identities and rechecks the actor context", () => {
    const state = workReturnState("/cases/case-1/reviews/run-1?attempt_id=a1&work_filter=evidence&work_item=r1&secret=example", "case-1", "actor-a");
    expect(readWorkReturn(state, "case-1", "actor-a")).toBe("/cases/case-1/reviews/run-1?attempt_id=a1&work_filter=evidence&work_item=r1");
    expect(readWorkReturn(state, "case-1", "actor-b")).toBeNull();
    for (const path of ["https://evil.test/cases/case-1/reviews/r", "//evil.test/cases/case-1/reviews/r", "/cases/other/reviews/r", "/cases/case-1/reviews/r/extra", "/cases/case-1/../reviews/r"]) {
      expect(reviewReturnPath(path, "case-1")).toBeNull();
    }
  });
});
