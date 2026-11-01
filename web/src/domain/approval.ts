import type { StatusTone } from "./review";

export const APPROVAL_NODE_LABELS: Record<string, string> = {
  department_head: "部门负责人",
  business_domain: "业务领域",
  internal_control: "内控",
  legal_counsel: "法务顾问",
  legal_officer: "法务专员",
  business_leader: "业务负责人",
  handler_final_check: "经办人最终核对",
};

export const APPROVAL_ROUND_STATE_LABELS: Record<string, string> = {
  in_review: "会签中",
  returned: "已退回",
  withdrawn: "已撤回",
  approved: "已通过",
  terminated: "已终止",
};

export const APPROVAL_TASK_STATE_LABELS: Record<string, string> = {
  pending: "待激活",
  active: "待处理",
  approved: "已通过",
  returned: "已退回",
  cancelled: "已取消",
};

export const APPROVAL_ACTION_LABELS: Record<string, string> = {
  approve: "通过",
  return: "退回修改",
};

export function approvalNodeLabel(code: string | null | undefined): string {
  if (!code) return "未知节点";
  return APPROVAL_NODE_LABELS[code] ?? code;
}

export function approvalRoundStateLabel(state: string): string {
  return APPROVAL_ROUND_STATE_LABELS[state] ?? state;
}

export function approvalTaskStateLabel(state: string): string {
  return APPROVAL_TASK_STATE_LABELS[state] ?? state;
}

export function approvalActionLabel(action: string): string {
  return APPROVAL_ACTION_LABELS[action] ?? action;
}

export function approvalRoundTone(state: string): StatusTone {
  if (state === "approved") return "good";
  if (state === "in_review") return "progress";
  if (state === "returned") return "attention";
  if (state === "terminated") return "bad";
  return "neutral";
}

export function approvalTaskTone(state: string): StatusTone {
  if (state === "approved") return "good";
  if (state === "active") return "progress";
  if (state === "returned") return "attention";
  return "neutral";
}

export function approvalProgressLabel(states: string[]): string {
  if (states.length === 0) return "未返回任务";
  const approved = states.filter((state) => state === "approved").length;
  return `已通过 ${approved} / ${states.length} 个节点`;
}

export function approvalFieldValue(value: unknown): string {
  if (value === undefined) return "未返回";
  if (value === null) return "空";
  if (typeof value === "boolean") return value ? "是" : "否";
  if (typeof value === "string") return value === "" ? "空" : value;
  if (typeof value === "number") return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return "无法显示";
  }
}
