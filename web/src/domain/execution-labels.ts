const PHASE_LABELS: Record<string, string> = {
  acceptance: "受理",
  audit_review_execution_v1: "审查执行",
  input_preparation: "输入准备",
  deterministic_rules: "确定性规则审查",
  model_review: "模型审查",
  model_request: "模型请求",
  citation_verification: "引用核验",
  verified_finding: "引用核验",
  terminal_persistence: "终态保存",
  cancellation: "取消执行",
  restart_recovery: "重启恢复",
  evidence_tool: "证据工具",
  agent_review_result: "智能体审查结果",
};

const EVENT_TYPE_LABELS: Record<string, string> = {
  attempt_accepted: "执行已受理",
  span_started: "开始",
  span_completed: "完成",
  span_failed: "失败",
  node_started: "节点开始",
  node_completed: "节点完成",
  node_failed: "节点失败",
  model_completed: "模型调用完成",
  model_failed: "模型调用失败",
  finding_verified: "引用核验记录",
  attempt_finished: "执行结束",
  attempt_cancelled: "执行已取消",
  attempt_interrupted: "执行已中断",
  tool_evidence: "证据工具结果",
  agent_review_event: "智能体审查事件",
};

const EVENT_STATUS_LABELS: Record<string, string> = {
  accepted: "已受理",
  queued: "排队中",
  started: "已开始",
  running: "进行中",
  completed: "已完成",
  failed: "失败",
  partial: "部分完成",
  cancelled: "已取消",
  interrupted: "已中断",
};

/** Unknown labels pass through; hasOwn keeps prototype keys from leaking in. */
function labelFrom(labels: Record<string, string>, value: string): string {
  return Object.hasOwn(labels, value) ? (labels[value] ?? value) : value;
}

export const phaseLabel = (phase: string): string =>
  labelFrom(PHASE_LABELS, phase);
export const eventTypeLabel = (type: string): string =>
  labelFrom(EVENT_TYPE_LABELS, type);
export const eventStatusLabel = (status: string): string =>
  labelFrom(EVENT_STATUS_LABELS, status);

export function elapsed(value: number | null): string {
  if (value === null) return "未报告";
  if (value < 1_000) return `${value} 毫秒`;
  return `${(value / 1_000).toFixed(2)} 秒`;
}

export function instant(value: string | null): string {
  if (value === null) return "未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).format(date);
}

export function tokenCount(value: number | null): string {
  return value === null ? "未知" : value.toLocaleString("zh-CN");
}
