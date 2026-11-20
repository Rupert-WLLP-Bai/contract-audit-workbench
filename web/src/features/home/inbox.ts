// File metadata (manifest mirror):
// Purpose: Projects personal tasks and evidence requests, actionable approvals, visible cases pending judgment and submitted remediation into distinct inbox rows.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import type { ApprovalInboxItem } from "../../api/approvals";
import type { EvidenceRequest } from "../../api/evidence-workflow";
import type { WorkbenchSnapshot } from "../../api/types";
import { caseHref } from "../../domain/review";
import { writeReviewDrawer } from "../review/work-items";

export type InboxKind = "decision" | "evidence" | "approval" | "task" | "team" | "remediation";
export interface InboxRow {
  key: string;
  kind: InboxKind;
  title: string;
  caseTitle?: string;
  due: string | null;
  updatedAt: string | null;
  action: string;
  href: string;
}

export function inboxRows(
  snapshot: WorkbenchSnapshot | undefined,
  actorId: string,
  requests: readonly EvidenceRequest[],
  approvals: readonly ApprovalInboxItem[],
): InboxRow[] {
  const cases = new Map(snapshot?.cases.map((entry) => [entry.id, entry]));
  const evidence = requests.filter((request) => {
    const item = cases.get(request.case_id);
    return (
      item &&
      !item.archived_at &&
      request.status === "open" &&
      request.assignee_id === actorId &&
      request.id &&
      request.task_id &&
      request.source_run_id &&
      request.source_attempt_id
    );
  });
  // Requests stay distinct even when two checks share one assigned task.
  const linkedTasks = new Set<string>();
  const rows = new Map<string, InboxRow>();
  for (const request of evidence) {
    const key = JSON.stringify(["evidence", request.case_id, request.id]);
    linkedTasks.add(JSON.stringify([request.case_id, request.task_id]));
    if (rows.has(key)) continue;
    const params = writeReviewDrawer(
      new URLSearchParams({
        attempt_id: request.source_attempt_id,
        work_kind: "request",
        work_item: request.id,
      }),
      "record",
      "supplement",
    );
    rows.set(key, {
      key,
      kind: "evidence",
      title: request.question,
      caseTitle: cases.get(request.case_id)?.title,
      due: request.due_at,
      updatedAt: request.updated_at,
      action: "查看补件",
      href: `${caseHref(request.case_id, request.source_run_id)}?${params}`,
    });
  }
  for (const task of snapshot?.tasks ?? []) {
    const item = cases.get(task.case_id);
    const key = JSON.stringify([task.case_id, task.id]);
    if (
      !item ||
      item.archived_at ||
      !task.id ||
      task.assignee_id !== actorId ||
      !["open", "in_progress"].includes(task.status) ||
      linkedTasks.has(key) ||
      rows.has(key)
    )
      continue;
    rows.set(key, {
      key,
      kind: task.kind === "decision" ? "decision" : "task",
      title: task.title,
      caseTitle: item.title,
      due: task.due_at,
      updatedAt: task.updated_at,
      action: "查看任务",
      href: `/workbench/tasks?task=${encodeURIComponent(task.id)}`,
    });
  }
  for (const approval of approvals) {
    if (
      approval.view !== "todo" ||
      approval.task_state !== "active" ||
      approval.round_state !== "in_review" ||
      !approval.task_id ||
      !approval.allowed_actions.length
    )
      continue;
    const key = `approval:${approval.task_id}`;
    rows.set(key, {
      key,
      kind: "approval",
      title: approval.contract_name,
      due: null,
      updatedAt: approval.task_activated_at,
      action: "查看会签",
      href: `/workbench/approvals?view=todo&task=${encodeURIComponent(approval.task_id)}`,
    });
  }
  return [...rows.values()].sort((left, right) =>
    (left.due ?? "9999").localeCompare(right.due ?? "9999"),
  );
}

export function visibleCaseRows(snapshot: WorkbenchSnapshot | undefined) {
  const cases = new Map(
    snapshot?.cases.filter((item) => !item.archived_at).map((item) => [item.id, item]),
  );
  const team: InboxRow[] = [];
  const remediation = new Map<string, InboxRow>();
  for (const item of cases.values()) {
    if (!(item.pending_decisions > 0)) continue;
    const run = item.latest_run;
    team.push({
      key: item.id,
      kind: "team",
      title: item.title,
      caseTitle: `${item.pending_decisions} 条线索待判断`,
      due: null,
      updatedAt: run?.updated_at ?? item.updated_at,
      action: run ? "去判断" : "查看案件",
      href: caseHref(item.id, run?.id),
    });
  }
  for (const item of snapshot?.remediations ?? []) {
    const relatedCase = cases.get(item.case_id);
    if (!relatedCase || item.status !== "submitted" || !item.id) continue;
    const key = JSON.stringify([item.case_id, item.id]);
    const query = new URLSearchParams({ work_kind: "remediation", work_item: item.id });
    remediation.set(key, {
      key,
      kind: "remediation",
      title: item.measure || relatedCase.title,
      caseTitle: item.measure ? relatedCase.title : undefined,
      due: item.due_at,
      updatedAt: item.updated_at,
      action: "查看整改",
      href: `${caseHref(item.case_id)}?${query}`,
    });
  }
  const newestFirst = (left: InboxRow, right: InboxRow) =>
    (right.updatedAt ?? "").localeCompare(left.updatedAt ?? "");
  return {
    team: team.sort(newestFirst),
    remediation: [...remediation.values()].sort(newestFirst),
    teamComplete:
      snapshot !== undefined &&
      Array.from(cases.values()).every((item) => typeof item.pending_decisions === "number"),
  };
}
