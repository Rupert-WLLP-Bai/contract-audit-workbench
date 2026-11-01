import { afterEach, describe, expect, it, vi } from "vitest";
import {
  decideApprovalTask,
  listApprovalInbox,
  parseApprovalInboxPage,
  parseApprovalRoundDetail,
  parseApprovalTaskDetail,
  type ApprovalInboxItem,
  type ApprovalRoundReadDetail,
  type ApprovalTaskReadDetail,
} from "./approvals";

const at = "2026-11-01T02:30:00Z";

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

export function inboxItem(
  overrides: Partial<ApprovalInboxItem> = {},
): ApprovalInboxItem {
  return {
    view: "todo",
    contract_id: "contract-1",
    contract_name: "专线采购合同",
    contract_number: "HT-2026-001",
    contract_state: "in_approval",
    contract_revision_id: "revision-2",
    approval_round_id: "round-2",
    round_number: 2,
    round_state: "in_review",
    round_started_at: at,
    round_ended_at: null,
    task_id: "task-1",
    node_order: 1,
    node_code: "department_head",
    role_code: "department_head",
    assignee_subject_id: "actor-1",
    assignee_display: "审查员甲",
    task_state: "active",
    task_row_version: 3,
    task_activated_at: at,
    task_decided_at: null,
    decision_id: null,
    decision_action: null,
    decision_actor_subject_id: null,
    decision_actor_display: null,
    decision_opinion: null,
    decision_decided_at: null,
    allowed_actions: ["approve", "return"],
    ...overrides,
  };
}

function roundDetail(
  overrides: Partial<ApprovalRoundReadDetail> = {},
): ApprovalRoundReadDetail {
  return {
    contract_id: "contract-1",
    contract_name: "专线采购合同",
    contract_number: "HT-2026-001",
    contract_state: "in_approval",
    contract_row_version: 8,
    current_revision_id: "revision-2",
    round: {
      approval_round_id: "round-2",
      round_number: 2,
      contract_revision_id: "revision-2",
      state: "in_review",
      workflow_template_id: "template-approval",
      workflow_template_version: "1.0.0",
      initiated_by: "actor-1",
      initiated_by_display: "审查员甲",
      started_at: at,
      ended_at: null,
      previous_round_id: "round-1",
      row_version: 4,
    },
    tasks: [
      {
        task_id: "task-1",
        node_order: 1,
        node_code: "department_head",
        role_code: "department_head",
        assignee_subject_id: "actor-1",
        assignee_display: "审查员甲",
        state: "active",
        row_version: 3,
        activated_at: at,
        decided_at: null,
      },
    ],
    decisions: [],
    active_task_id: "task-1",
    risk_disposition_state: "unknown",
    risk_disposition_reason: "risk_disposition_not_configured",
    previous_round: null,
    revision_changed: false,
    revision_diff: null,
    allowed_actions: ["approve", "return"],
    ...overrides,
  };
}

function taskDetail(
  overrides: Partial<ApprovalTaskReadDetail> = {},
): ApprovalTaskReadDetail {
  return {
    selected_task_id: "task-1",
    round_detail: roundDetail(),
    ...overrides,
  };
}

afterEach(() => vi.restoreAllMocks());

describe("approval response parsers", () => {
  it("keeps the server view, decision and allowed actions explicit", () => {
    const page = parseApprovalInboxPage({
      items: [
        inboxItem(),
        inboxItem({
          view: "done",
          task_state: "approved",
          decision_action: "return",
          decision_decided_at: at,
          allowed_actions: [],
        }),
      ],
      next_cursor: "opaque-cursor",
      has_more: true,
    });
    expect(page.items[0]?.view).toBe("todo");
    expect(page.items[0]?.allowed_actions).toEqual(["approve", "return"]);
    expect(page.items[1]?.decision_action).toBe("return");
    expect(page.items[1]?.allowed_actions).toEqual([]);
    expect(page.next_cursor).toBe("opaque-cursor");
  });

  it("rejects an unknown round state instead of guessing one", () => {
    expect(() =>
      parseApprovalInboxPage({
        items: [inboxItem({ round_state: "done" as never })],
        next_cursor: null,
        has_more: false,
      }),
    ).toThrow(/approval_inbox\.items\[0\]\.round_state/);
  });

  it("parses restricted diff entries without inventing a plaintext value", () => {
    const detail = parseApprovalRoundDetail({
      ...roundDetail(),
      revision_changed: true,
      revision_diff: {
        from_revision_number: 1,
        to_revision_number: 2,
        changed: true,
        fields: [
          {
            path: "contract.amount",
            category: "amount",
            classification: "internal",
            change: "changed",
            before: 100,
            after: 120,
          },
        ],
        restricted_fields: [
          { path: "supplier.bank_account", changed: null, reason: "restricted" },
        ],
        amount_terms: [],
      },
    });
    expect(detail.revision_diff?.fields[0]?.before).toBe(100);
    expect(detail.revision_diff?.restricted_fields[0]).toEqual({
      path: "supplier.bank_account",
      changed: null,
      reason: "restricted",
    });
    expect(
      Object.hasOwn(detail.revision_diff?.restricted_fields[0] ?? {}, "value"),
    ).toBe(false);
  });

  it("requires the selected task and active task to come from the response", () => {
    const detail = parseApprovalTaskDetail(taskDetail());
    expect(detail.selected_task_id).toBe("task-1");
    expect(detail.round_detail.active_task_id).toBe("task-1");
    expect(() =>
      parseApprovalTaskDetail({
        selected_task_id: "task-1",
        round_detail: roundDetail({ allowed_actions: ["reject" as never] }),
      }),
    ).toThrow(/round_detail\.allowed_actions\[0\]/);
  });

  it("encodes the view, cursor and limit in the inbox request", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(
        jsonResponse({ items: [], next_cursor: null, has_more: false }),
      );
    await listApprovalInbox("done", "cursor-with/slash", 10);
    const url = String(fetch.mock.calls[0]?.[0]);
    expect(url).toContain("/api/approval-tasks?");
    expect(url).toContain("view=done");
    expect(url).toContain("limit=10");
    expect(url).toContain("cursor=cursor-with%2Fslash");
  });

  it("sends the current row version and reuses the caller's idempotency key", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({
        contract_id: "contract-1",
        contract_state: "in_approval",
        round: {
          approval_round_id: "round-2",
          round_number: 2,
          state: "in_review",
        },
        task: {
          task_id: "task-1",
          node_order: 1,
          state: "approved",
          row_version: 4,
        },
        decision: {
          decision_id: "decision-1",
          task_id: "task-1",
          action: "approve",
          opinion: "同意",
          decided_at: at,
        },
      }),
    );
    const result = await decideApprovalTask(
      {
        task_id: "task-1",
        contract_id: "contract-1",
        approval_round_id: "round-2",
        contract_revision_id: "revision-2",
        expected_row_version: 3,
        action: "approve",
        opinion: "同意",
      },
      "idem-key-1",
    );
    expect(result.task.state).toBe("approved");
    const [url, init] = fetch.mock.calls[0]!;
    expect(String(url)).toBe("/api/approval-tasks/task-1/decisions");
    expect(new Headers(init?.headers).get("Idempotency-Key")).toBe(
      "idem-key-1",
    );
    expect(JSON.parse(String(init?.body))).toEqual({
      contract_id: "contract-1",
      approval_round_id: "round-2",
      contract_revision_id: "revision-2",
      expected_row_version: 3,
      action: "approve",
      opinion: "同意",
    });
  });
});
