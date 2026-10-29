/** Display vocabulary for the operator-audit panels. Unknown stays unknown. */

import { formatDateTime, type StatusTone } from "../../domain/review";
import type {
  Availability,
  BindingStatus,
  ImpactAction,
  ImpactFallback,
  ImpactMode,
  MachineStatus,
  QuantityValue,
  Relation,
} from "./types";

/**
 * Vocabulary is data. One lookup per policy keeps every panel consistent:
 * `keepUnknown` passes an unrecognised server value through as-is, otherwise
 * the concept's fallback is shown. A null value always shows the fallback.
 */
function labelMap<T extends string>(
  vocabulary: Partial<Record<T, string>>,
  fallback: string,
  keepUnknown = false,
) {
  return (value: T | null): string => {
    if (value === null) return fallback;
    return (
      (vocabulary as Record<string, string | undefined>)[value] ??
      (keepUnknown ? value : fallback)
    );
  };
}

function toneMap<T extends string>(vocabulary: Partial<Record<T, StatusTone>>) {
  return (value: T | null): StatusTone => {
    if (value === null) return "neutral";
    return (
      (vocabulary as Record<string, StatusTone | undefined>)[value] ?? "neutral"
    );
  };
}

export const scenarioLabel = labelMap<string>(
  {
    telecom_dedicated_line: "政企专线",
    cloud_services: "云服务",
  },
  "场景未报告",
  true,
);

export const packStatusLabel = labelMap<string>(
  {
    published: "已发布",
    enabled: "已发布",
    active: "已发布",
    draft: "待发布",
    pending: "待发布",
    disabled: "已停用",
    retired: "已停用",
  },
  "状态未报告",
  true,
);

export const packStatusTone = toneMap<string>({
  published: "good",
  enabled: "good",
  active: "good",
  draft: "attention",
  pending: "attention",
});

export const relationLabelFor = labelMap<Relation>(
  {
    compatible: "一致",
    conflict: "冲突",
    insufficient: "信息不足",
    not_applicable: "不适用",
  },
  "关系未报告",
);

export const relationTone = toneMap<Relation>({
  compatible: "good",
  conflict: "bad",
  insufficient: "attention",
});

export const machineStatusLabel = labelMap<MachineStatus>(
  {
    completed: "执行完成",
    partial: "部分完成",
    failed: "执行失败",
    cancelled: "已取消",
  },
  "执行状态未报告",
);

export const machineStatusTone = toneMap<MachineStatus>({
  completed: "good",
  partial: "attention",
  failed: "bad",
});

export const availabilityLabel = labelMap<Availability>(
  {
    available: "可用",
    unavailable: "不可用",
    stale: "已过期",
    rejected: "已拒绝",
  },
  "可用性未报告",
);

export const availabilityTone = toneMap<Availability>({
  available: "good",
  unavailable: "attention",
  stale: "attention",
  rejected: "bad",
});

export const bindingStatusLabel = labelMap<BindingStatus>(
  {
    confirmed: "已人工确认",
    missing: "缺失",
    ambiguous: "多义（无法唯一确定）",
  },
  "映射状态未报告",
);

export const bindingStatusTone = toneMap<BindingStatus>({
  confirmed: "good",
  missing: "attention",
  ambiguous: "attention",
});

/**
 * A synthetic fixture is always labelled as such. The word used for server
 * "live" data is deliberately separate so a fixture can never read as live.
 */
export const dataClassLabel = labelMap<string>(
  {
    synthetic: "合成数据（fixture）",
    production: "生产数据",
  },
  "数据分类未报告",
  true,
);

export const dataClassTone = toneMap<string>({ synthetic: "attention" });

export function timeLabel(value: string | null): string {
  if (value === null) return "时间未报告";
  return formatDateTime(value);
}

export function quantityLabel(value: QuantityValue | null): string {
  if (value === null || value.value === null) return "数值未报告";
  if (value.unit === null) return `${value.value}（单位未报告）`;
  return `${value.value} ${value.unit}`;
}

export const jobStateLabel = labelMap<string>(
  {
    accepted: "已受理",
    queued: "排队中",
    running: "采集中",
    completed: "已完成",
    failed: "失败",
    denied: "已拒绝",
    rejected: "已拒绝",
    cancelled: "已取消",
  },
  "状态未报告",
  true,
);

export const jobStateTone = toneMap<string>({
  completed: "good",
  accepted: "progress",
  queued: "progress",
  running: "progress",
  failed: "bad",
  denied: "bad",
  rejected: "bad",
});

export const impactModeLabel = labelMap<ImpactMode>(
  { explain: "解释模式（explain）", shadow: "影子对照（shadow）" },
  "模式未报告",
);

export const impactModeTone = toneMap<ImpactMode>({ shadow: "progress" });

export const impactActionLabel = labelMap<ImpactAction>(
  {
    recompute: "重新计算（recompute）",
    reuse_candidate: "预计可复用（尚未跳过）",
    deny: "不可复用（deny）",
  },
  "动作未报告",
);

export const impactActionTone = toneMap<ImpactAction>({
  recompute: "progress",
  reuse_candidate: "attention",
  deny: "bad",
});

export const impactFallbackLabel = labelMap<ImpactFallback>(
  { none: "无需回退", full: "回退全量（full）", deny: "拒绝（deny）" },
  "回退策略未报告",
);

export function dependencyCompleteLabel(value: boolean | null): string {
  if (value === true) return "依赖完整";
  if (value === false) return "依赖不完整";
  return "依赖完整性未报告";
}

const impactReasons: Record<string, string> = {
  dependent_field_changed: "依赖字段发生变化",
  unchanged_complete_dependencies: "依赖未变化且完整",
  dependency_incomplete: "依赖不完整",
  authorization_revoked: "授权已撤销",
  denied: "拒绝复用",
};

export function impactReasonLabel(value: string): string {
  return impactReasons[value] ?? value;
}
