import { ApiError, jsonInit, requestJson } from "./http";
import { enumValue, responseReader } from "./respond";

/**
 * Contract approval workbench reads. Responses are projections of frozen
 * rounds and append-only decisions; the UI never derives an approval state
 * from a pending mutation or a retry.
 */
export type ApprovalInboxView = "todo" | "done" | "initiated";
export type ApprovalRoundState =
  | "in_review"
  | "returned"
  | "withdrawn"
  | "approved"
  | "terminated";
export type ApprovalTaskState =
  | "pending"
  | "active"
  | "approved"
  | "returned"
  | "cancelled";
export type ApprovalDecisionAction = "approve" | "return";

export interface ApprovalInboxItem {
  view: ApprovalInboxView;
  contract_id: string;
  contract_name: string;
  contract_number: string | null;
  contract_state: string;
  contract_revision_id: string;
  approval_round_id: string;
  round_number: number;
  round_state: ApprovalRoundState;
  round_started_at: string;
  round_ended_at: string | null;
  task_id: string | null;
  node_order: number | null;
  node_code: string | null;
  role_code: string | null;
  assignee_subject_id: string | null;
  assignee_display: string | null;
  task_state: ApprovalTaskState | null;
  task_row_version: number | null;
  task_activated_at: string | null;
  task_decided_at: string | null;
  decision_id: string | null;
  decision_action: ApprovalDecisionAction | null;
  decision_actor_subject_id: string | null;
  decision_actor_display: string | null;
  decision_opinion: string | null;
  decision_decided_at: string | null;
  allowed_actions: ApprovalDecisionAction[];
}

export interface ApprovalInboxPage {
  items: ApprovalInboxItem[];
  next_cursor: string | null;
  has_more: boolean;
}

export interface ApprovalRoundReadSummary {
  approval_round_id: string;
  round_number: number;
  contract_revision_id: string;
  state: ApprovalRoundState;
  workflow_template_id: string;
  workflow_template_version: string;
  initiated_by: string;
  initiated_by_display: string;
  started_at: string;
  ended_at: string | null;
  previous_round_id: string | null;
  row_version: number;
}

export interface ApprovalReadTask {
  task_id: string;
  node_order: number;
  node_code: string;
  role_code: string;
  assignee_subject_id: string;
  assignee_display: string;
  state: ApprovalTaskState;
  row_version: number;
  activated_at: string | null;
  decided_at: string | null;
}

export interface ApprovalReadDecision {
  decision_id: string;
  task_id: string;
  node_order: number;
  node_code: string;
  action: ApprovalDecisionAction;
  actor_subject_id: string;
  actor_display: string;
  opinion: string;
  decided_at: string;
}

export interface ApprovalFieldChange {
  path: string;
  category: string;
  classification: string;
  change: string;
  before?: unknown;
  after?: unknown;
}

export interface ApprovalRestrictedChange {
  path: string;
  changed: boolean | null;
  reason?: string;
}

export interface ApprovalRevisionDiff {
  from_revision_number: number | null;
  to_revision_number: number;
  changed: boolean;
  fields: ApprovalFieldChange[];
  restricted_fields: ApprovalRestrictedChange[];
  amount_terms: ApprovalFieldChange[];
}

export interface ApprovalRoundReadDetail {
  contract_id: string;
  contract_name: string;
  contract_number: string | null;
  contract_state: string;
  contract_row_version: number;
  current_revision_id: string | null;
  round: ApprovalRoundReadSummary;
  tasks: ApprovalReadTask[];
  decisions: ApprovalReadDecision[];
  active_task_id: string | null;
  risk_disposition_state: string;
  risk_disposition_reason?: string;
  previous_round: ApprovalRoundReadSummary | null;
  revision_changed: boolean;
  revision_diff: ApprovalRevisionDiff | null;
  allowed_actions: ApprovalDecisionAction[];
}

export interface ApprovalTaskReadDetail {
  selected_task_id: string;
  round_detail: ApprovalRoundReadDetail;
}

export interface ApprovalRoundHistoryItem {
  round: ApprovalRoundReadSummary;
  active_task: ApprovalReadTask | null;
  task_count: number;
  decision_count: number;
}

export interface ApprovalRoundHistoryPage {
  items: ApprovalRoundHistoryItem[];
  next_cursor: string | null;
  has_more: boolean;
}

export interface ApprovalDecisionInput {
  task_id: string;
  expected_round_row_version: number;
  decision: ApprovalDecisionAction;
  comment: string;
  credential?: string;
}

export interface ApprovalDecisionResult {
  decision_id: string;
  decision: ApprovalDecisionAction;
  round: {
    approval_round_id: string;
    contract_revision_id: string;
    round_number: number;
    state: ApprovalRoundState;
    row_version: number;
  };
  task: {
    task_id: string;
    node_order: number;
    state: ApprovalTaskState;
  };
  next_task: {
    task_id: string;
    node_order: number;
    state: ApprovalTaskState;
  } | null;
  contract_state: string;
  workflow_state: string;
  row_version: number;
}

const reader = responseReader(
  (path) => `审批响应无法识别：${path}`,
);

const roundStates: readonly ApprovalRoundState[] = [
  "in_review",
  "returned",
  "withdrawn",
  "approved",
  "terminated",
];
const taskStates: readonly ApprovalTaskState[] = [
  "pending",
  "active",
  "approved",
  "returned",
  "cancelled",
];
const actions: readonly ApprovalDecisionAction[] = ["approve", "return"];

function nullableInteger(value: unknown, path: string): number | null {
  return value === null ? null : reader.integer(value, path, 1);
}

function roundState(value: unknown, path: string): ApprovalRoundState {
  const state = enumValue(value, roundStates);
  return state ?? reader.invalid(path);
}

function taskState(value: unknown, path: string): ApprovalTaskState {
  const state = enumValue(value, taskStates);
  return state ?? reader.invalid(path);
}

function decisionAction(value: unknown, path: string): ApprovalDecisionAction {
  const action = enumValue(value, actions);
  return action ?? reader.invalid(path);
}

function optionalAction(
  value: unknown,
  path: string,
): ApprovalDecisionAction | null {
  return value === null ? null : decisionAction(value, path);
}

function optionalTaskState(
  value: unknown,
  path: string,
): ApprovalTaskState | null {
  return value === null ? null : taskState(value, path);
}

function parseInboxItem(value: unknown, path: string): ApprovalInboxItem {
  const row = reader.object(value, path);
  const view = enumValue(row.view, ["todo", "done", "initiated"] as const);
  if (view === null) return reader.invalid(`${path}.view`);
  return {
    view,
    contract_id: reader.text(row.contract_id, `${path}.contract_id`),
    contract_name: reader.text(row.contract_name, `${path}.contract_name`),
    contract_number: reader.nullableText(
      row.contract_number,
      `${path}.contract_number`,
    ),
    contract_state: reader.text(row.contract_state, `${path}.contract_state`),
    contract_revision_id: reader.text(
      row.contract_revision_id,
      `${path}.contract_revision_id`,
    ),
    approval_round_id: reader.text(
      row.approval_round_id,
      `${path}.approval_round_id`,
    ),
    round_number: reader.integer(row.round_number, `${path}.round_number`, 1),
    round_state: roundState(row.round_state, `${path}.round_state`),
    round_started_at: reader.text(
      row.round_started_at,
      `${path}.round_started_at`,
    ),
    round_ended_at: reader.nullableText(
      row.round_ended_at,
      `${path}.round_ended_at`,
    ),
    task_id: reader.nullableText(row.task_id, `${path}.task_id`),
    node_order: nullableInteger(row.node_order, `${path}.node_order`),
    node_code: reader.nullableText(row.node_code, `${path}.node_code`),
    role_code: reader.nullableText(row.role_code, `${path}.role_code`),
    assignee_subject_id: reader.nullableText(
      row.assignee_subject_id,
      `${path}.assignee_subject_id`,
    ),
    assignee_display: reader.nullableText(
      row.assignee_display,
      `${path}.assignee_display`,
    ),
    task_state: optionalTaskState(row.task_state, `${path}.task_state`),
    task_row_version: nullableInteger(
      row.task_row_version,
      `${path}.task_row_version`,
    ),
    task_activated_at: reader.nullableText(
      row.task_activated_at,
      `${path}.task_activated_at`,
    ),
    task_decided_at: reader.nullableText(
      row.task_decided_at,
      `${path}.task_decided_at`,
    ),
    decision_id: reader.nullableText(row.decision_id, `${path}.decision_id`),
    decision_action: optionalAction(
      row.decision_action,
      `${path}.decision_action`,
    ),
    decision_actor_subject_id: reader.nullableText(
      row.decision_actor_subject_id,
      `${path}.decision_actor_subject_id`,
    ),
    decision_actor_display: reader.nullableText(
      row.decision_actor_display,
      `${path}.decision_actor_display`,
    ),
    decision_opinion: reader.nullableText(
      row.decision_opinion,
      `${path}.decision_opinion`,
    ),
    decision_decided_at: reader.nullableText(
      row.decision_decided_at,
      `${path}.decision_decided_at`,
    ),
    allowed_actions: reader.array(
      row.allowed_actions,
      `${path}.allowed_actions`,
      (entry, itemPath) => decisionAction(entry, itemPath),
    ),
  };
}

export function parseApprovalInboxPage(value: unknown): ApprovalInboxPage {
  const row = reader.object(value, "approval_inbox");
  return {
    items: reader.array(row.items, "approval_inbox.items", parseInboxItem),
    next_cursor: reader.nullableText(
      row.next_cursor,
      "approval_inbox.next_cursor",
    ),
    has_more: reader.bool(row.has_more, "approval_inbox.has_more"),
  };
}

function parseRoundSummary(
  value: unknown,
  path: string,
): ApprovalRoundReadSummary {
  const row = reader.object(value, path);
  return {
    approval_round_id: reader.text(
      row.approval_round_id,
      `${path}.approval_round_id`,
    ),
    round_number: reader.integer(row.round_number, `${path}.round_number`, 1),
    contract_revision_id: reader.text(
      row.contract_revision_id,
      `${path}.contract_revision_id`,
    ),
    state: roundState(row.state, `${path}.state`),
    workflow_template_id: reader.text(
      row.workflow_template_id,
      `${path}.workflow_template_id`,
    ),
    workflow_template_version: reader.text(
      row.workflow_template_version,
      `${path}.workflow_template_version`,
    ),
    initiated_by: reader.text(row.initiated_by, `${path}.initiated_by`),
    initiated_by_display: reader.text(
      row.initiated_by_display,
      `${path}.initiated_by_display`,
    ),
    started_at: reader.text(row.started_at, `${path}.started_at`),
    ended_at: reader.nullableText(row.ended_at, `${path}.ended_at`),
    previous_round_id: reader.nullableText(
      row.previous_round_id,
      `${path}.previous_round_id`,
    ),
    row_version: reader.integer(row.row_version, `${path}.row_version`, 1),
  };
}

function parseReadTask(value: unknown, path: string): ApprovalReadTask {
  const row = reader.object(value, path);
  return {
    task_id: reader.text(row.task_id, `${path}.task_id`),
    node_order: reader.integer(row.node_order, `${path}.node_order`, 1),
    node_code: reader.text(row.node_code, `${path}.node_code`),
    role_code: reader.text(row.role_code, `${path}.role_code`),
    assignee_subject_id: reader.text(
      row.assignee_subject_id,
      `${path}.assignee_subject_id`,
    ),
    assignee_display: reader.text(
      row.assignee_display,
      `${path}.assignee_display`,
    ),
    state: taskState(row.state, `${path}.state`),
    row_version: reader.integer(row.row_version, `${path}.row_version`, 1),
    activated_at: reader.nullableText(
      row.activated_at,
      `${path}.activated_at`,
    ),
    decided_at: reader.nullableText(row.decided_at, `${path}.decided_at`),
  };
}

function parseReadDecision(
  value: unknown,
  path: string,
): ApprovalReadDecision {
  const row = reader.object(value, path);
  return {
    decision_id: reader.text(row.decision_id, `${path}.decision_id`),
    task_id: reader.text(row.task_id, `${path}.task_id`),
    node_order: reader.integer(row.node_order, `${path}.node_order`, 1),
    node_code: reader.text(row.node_code, `${path}.node_code`),
    action: decisionAction(row.action, `${path}.action`),
    actor_subject_id: reader.text(
      row.actor_subject_id,
      `${path}.actor_subject_id`,
    ),
    actor_display: reader.text(row.actor_display, `${path}.actor_display`),
    opinion: reader.text(row.opinion, `${path}.opinion`),
    decided_at: reader.text(row.decided_at, `${path}.decided_at`),
  };
}

function parseFieldChange(value: unknown, path: string): ApprovalFieldChange {
  const row = reader.object(value, path);
  const change: ApprovalFieldChange = {
    path: reader.text(row.path, `${path}.path`),
    category: reader.text(row.category, `${path}.category`),
    classification: reader.text(row.classification, `${path}.classification`),
    change: reader.text(row.change, `${path}.change`),
  };
  if (Object.hasOwn(row, "before")) change.before = row.before;
  if (Object.hasOwn(row, "after")) change.after = row.after;
  return change;
}

function parseRestrictedChange(
  value: unknown,
  path: string,
): ApprovalRestrictedChange {
  const row = reader.object(value, path);
  return {
    path: reader.text(row.path, `${path}.path`),
    changed: reader.nullableBool(row.changed, `${path}.changed`),
    reason: reader.maybeText(row.reason, `${path}.reason`),
  };
}

function parseRevisionDiff(value: unknown, path: string): ApprovalRevisionDiff {
  const row = reader.object(value, path);
  return {
    from_revision_number: nullableInteger(
      row.from_revision_number,
      `${path}.from_revision_number`,
    ),
    to_revision_number: reader.integer(
      row.to_revision_number,
      `${path}.to_revision_number`,
      1,
    ),
    changed: reader.bool(row.changed, `${path}.changed`),
    fields: reader.array(
      row.fields,
      `${path}.fields`,
      parseFieldChange,
    ),
    restricted_fields: reader.array(
      row.restricted_fields,
      `${path}.restricted_fields`,
      parseRestrictedChange,
    ),
    amount_terms: reader.array(
      row.amount_terms,
      `${path}.amount_terms`,
      parseFieldChange,
    ),
  };
}

function parseRoundDetail(
  value: unknown,
  path: string,
): ApprovalRoundReadDetail {
  const row = reader.object(value, path);
  return {
    contract_id: reader.text(row.contract_id, `${path}.contract_id`),
    contract_name: reader.text(row.contract_name, `${path}.contract_name`),
    contract_number: reader.nullableText(
      row.contract_number,
      `${path}.contract_number`,
    ),
    contract_state: reader.text(row.contract_state, `${path}.contract_state`),
    contract_row_version: reader.integer(
      row.contract_row_version,
      `${path}.contract_row_version`,
      1,
    ),
    current_revision_id: reader.nullableText(
      row.current_revision_id,
      `${path}.current_revision_id`,
    ),
    round: parseRoundSummary(row.round, `${path}.round`),
    tasks: reader.array(row.tasks, `${path}.tasks`, parseReadTask),
    decisions: reader.array(
      row.decisions,
      `${path}.decisions`,
      parseReadDecision,
    ),
    active_task_id: reader.nullableText(
      row.active_task_id,
      `${path}.active_task_id`,
    ),
    risk_disposition_state: reader.text(
      row.risk_disposition_state,
      `${path}.risk_disposition_state`,
    ),
    risk_disposition_reason: reader.maybeText(
      row.risk_disposition_reason,
      `${path}.risk_disposition_reason`,
    ),
    previous_round:
      row.previous_round === null
        ? null
        : parseRoundSummary(row.previous_round, `${path}.previous_round`),
    revision_changed: reader.bool(
      row.revision_changed,
      `${path}.revision_changed`,
    ),
    revision_diff:
      row.revision_diff === null
        ? null
        : parseRevisionDiff(row.revision_diff, `${path}.revision_diff`),
    allowed_actions: reader.array(
      row.allowed_actions,
      `${path}.allowed_actions`,
      (entry, itemPath) => decisionAction(entry, itemPath),
    ),
  };
}

export function parseApprovalTaskDetail(value: unknown): ApprovalTaskReadDetail {
  const row = reader.object(value, "approval_task");
  return {
    selected_task_id: reader.text(
      row.selected_task_id,
      "approval_task.selected_task_id",
    ),
    round_detail: parseRoundDetail(
      row.round_detail,
      "approval_task.round_detail",
    ),
  };
}

export function parseApprovalRoundDetail(
  value: unknown,
): ApprovalRoundReadDetail {
  return parseRoundDetail(value, "approval_round");
}

function parseHistoryItem(
  value: unknown,
  path: string,
): ApprovalRoundHistoryItem {
  const row = reader.object(value, path);
  return {
    round: parseRoundSummary(row.round, `${path}.round`),
    active_task:
      row.active_task === null
        ? null
        : parseReadTask(row.active_task, `${path}.active_task`),
    task_count: reader.integer(row.task_count, `${path}.task_count`, 0),
    decision_count: reader.integer(
      row.decision_count,
      `${path}.decision_count`,
      0,
    ),
  };
}

export function parseApprovalRoundHistoryPage(
  value: unknown,
): ApprovalRoundHistoryPage {
  const row = reader.object(value, "approval_round_history");
  return {
    items: reader.array(
      row.items,
      "approval_round_history.items",
      parseHistoryItem,
    ),
    next_cursor: reader.nullableText(
      row.next_cursor,
      "approval_round_history.next_cursor",
    ),
    has_more: reader.bool(row.has_more, "approval_round_history.has_more"),
  };
}

function decisionTask(value: unknown, path: string) {
  const task = reader.object(value, path);
  return {
    task_id: reader.text(task.approval_task_id, `${path}.approval_task_id`),
    node_order: reader.integer(task.node_order, `${path}.node_order`, 1),
    state: taskState(task.state, `${path}.state`),
  };
}

export function parseApprovalDecisionResult(
  value: unknown,
): ApprovalDecisionResult {
  const row = reader.object(value, "approval_decision");
  const round = reader.object(row.round, "approval_decision.round");
  return {
    decision_id: reader.text(
      row.decision_id,
      "approval_decision.decision_id",
    ),
    decision: decisionAction(
      row.decision,
      "approval_decision.decision",
    ),
    round: {
      approval_round_id: reader.text(
        round.approval_round_id,
        "approval_decision.round.approval_round_id",
      ),
      contract_revision_id: reader.text(
        round.contract_revision_id,
        "approval_decision.round.contract_revision_id",
      ),
      round_number: reader.integer(
        round.round_number,
        "approval_decision.round.round_number",
        1,
      ),
      state: roundState(round.state, "approval_decision.round.state"),
      row_version: reader.integer(
        round.row_version,
        "approval_decision.round.row_version",
        1,
      ),
    },
    task: decisionTask(row.task, "approval_decision.task"),
    next_task:
      row.next_task === null
        ? null
        : decisionTask(row.next_task, "approval_decision.next_task"),
    contract_state: reader.text(
      row.contract_state,
      "approval_decision.contract_state",
    ),
    workflow_state: reader.text(
      row.workflow_state,
      "approval_decision.workflow_state",
    ),
    row_version: reader.integer(
      row.row_version,
      "approval_decision.row_version",
      1,
    ),
  };
}

function approvalQueryString(
  view: ApprovalInboxView,
  cursor: string | null,
  limit: number,
): string {
  const params = new URLSearchParams({ view, limit: String(limit) });
  if (cursor !== null && cursor !== "") params.set("cursor", cursor);
  return params.toString();
}

export function listApprovalInbox(
  view: ApprovalInboxView,
  cursor: string | null = null,
  limit = 25,
  signal?: AbortSignal,
): Promise<ApprovalInboxPage> {
  return requestJson(
    `/api/approval-tasks?${approvalQueryString(view, cursor, limit)}`,
    { method: "GET", signal },
    parseApprovalInboxPage,
  );
}

export function getApprovalTask(
  taskId: string,
  signal?: AbortSignal,
): Promise<ApprovalTaskReadDetail> {
  return requestJson(
    `/api/approval-tasks/${encodeURIComponent(taskId)}`,
    { method: "GET", signal },
    parseApprovalTaskDetail,
  );
}

export function listContractApprovalRounds(
  contractId: string,
  cursor: string | null = null,
  limit = 20,
  signal?: AbortSignal,
): Promise<ApprovalRoundHistoryPage> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor !== null && cursor !== "") params.set("cursor", cursor);
  return requestJson(
    `/api/contracts/${encodeURIComponent(contractId)}/approval-rounds?${params.toString()}`,
    { method: "GET", signal },
    parseApprovalRoundHistoryPage,
  );
}

export function getContractApprovalRound(
  contractId: string,
  roundId: string,
  signal?: AbortSignal,
): Promise<ApprovalRoundReadDetail> {
  return requestJson(
    `/api/contracts/${encodeURIComponent(contractId)}/approval-rounds/${encodeURIComponent(roundId)}`,
    { method: "GET", signal },
    parseApprovalRoundDetail,
  );
}

function parseApprovalCredential(value: unknown): string {
  const row = reader.object(value, "approval_credential");
  return reader.text(row.credential, "approval_credential.credential");
}

async function requestApprovalCredential(taskId: string): Promise<string> {
  return requestJson(
    `/api/approval-tasks/${encodeURIComponent(taskId)}/credentials`,
    jsonInit("POST", {}),
    parseApprovalCredential,
  );
}

/**
 * Submits one decision. A deployment with internal approval credentials
 * enabled rejects the first attempt with 403; the client then requests the
 * short-lived credential for this task and retries with the same idempotency
 * key. A genuine authorization refusal is surfaced unchanged because the
 * credential request fails too. When the capability is off the decision
 * succeeds on the first request and no extra call is made.
 */
export async function decideApprovalTask(
  input: ApprovalDecisionInput,
  idempotencyKey: string,
): Promise<ApprovalDecisionResult> {
  const body = {
    decision: input.decision,
    comment: input.comment,
    expected_round_row_version: input.expected_round_row_version,
  };
  const send = (credential?: string) =>
    requestJson(
      `/api/approval-tasks/${encodeURIComponent(input.task_id)}/decisions`,
      jsonInit(
        "POST",
        credential === undefined ? body : { ...body, credential },
        { "Idempotency-Key": idempotencyKey },
      ),
      parseApprovalDecisionResult,
    );
  try {
    return await send(input.credential);
  } catch (error) {
    if (
      input.credential !== undefined ||
      !(error instanceof ApiError) ||
      error.status !== 403
    ) {
      throw error;
    }
    let credential: string;
    try {
      credential = await requestApprovalCredential(input.task_id);
    } catch {
      throw error;
    }
    return send(credential);
  }
}
