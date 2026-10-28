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

export function scenarioLabel(value: string | null): string {
  if (value === null) return "场景未报告";
  if (value === "telecom_dedicated_line") return "政企专线";
  if (value === "cloud_services") return "云服务";
  return value;
}

export function packStatusLabel(value: string | null): string {
  if (value === null) return "状态未报告";
  if (value === "published" || value === "enabled" || value === "active")
    return "已发布";
  if (value === "draft" || value === "pending") return "待发布";
  if (value === "disabled" || value === "retired") return "已停用";
  return value;
}

export function packStatusTone(value: string | null): StatusTone {
  if (value === "published" || value === "enabled" || value === "active")
    return "good";
  if (value === "draft" || value === "pending") return "attention";
  return "neutral";
}

export function relationLabelFor(value: Relation): string {
  switch (value) {
    case "compatible":
      return "一致";
    case "conflict":
      return "冲突";
    case "insufficient":
      return "信息不足";
    case "not_applicable":
      return "不适用";
    default:
      return "关系未报告";
  }
}

export function relationTone(value: Relation): StatusTone {
  switch (value) {
    case "compatible":
      return "good";
    case "conflict":
      return "bad";
    case "insufficient":
      return "attention";
    default:
      return "neutral";
  }
}

export function machineStatusLabel(value: MachineStatus): string {
  switch (value) {
    case "completed":
      return "执行完成";
    case "partial":
      return "部分完成";
    case "failed":
      return "执行失败";
    case "cancelled":
      return "已取消";
    default:
      return "执行状态未报告";
  }
}

export function machineStatusTone(value: MachineStatus): StatusTone {
  switch (value) {
    case "completed":
      return "good";
    case "partial":
      return "attention";
    case "failed":
      return "bad";
    default:
      return "neutral";
  }
}

export function availabilityLabel(value: Availability): string {
  switch (value) {
    case "available":
      return "可用";
    case "unavailable":
      return "不可用";
    case "stale":
      return "已过期";
    case "rejected":
      return "已拒绝";
    default:
      return "可用性未报告";
  }
}

export function availabilityTone(value: Availability): StatusTone {
  switch (value) {
    case "available":
      return "good";
    case "unavailable":
    case "stale":
      return "attention";
    case "rejected":
      return "bad";
    default:
      return "neutral";
  }
}

export function bindingStatusLabel(value: BindingStatus): string {
  switch (value) {
    case "confirmed":
      return "已人工确认";
    case "missing":
      return "缺失";
    case "ambiguous":
      return "多义（无法唯一确定）";
    default:
      return "映射状态未报告";
  }
}

export function bindingStatusTone(value: BindingStatus): StatusTone {
  if (value === "confirmed") return "good";
  if (value === "missing" || value === "ambiguous") return "attention";
  return "neutral";
}

/**
 * A synthetic fixture is always labelled as such. The word used for server
 * "live" data is deliberately separate so a fixture can never read as live.
 */
export function dataClassLabel(value: string | null): string {
  if (value === null) return "数据分类未报告";
  if (value === "synthetic") return "合成数据（fixture）";
  if (value === "production") return "生产数据";
  return value;
}

export function dataClassTone(value: string | null): StatusTone {
  return value === "synthetic" ? "attention" : "neutral";
}

export function timeLabel(value: string | null): string {
  if (value === null) return "时间未报告";
  return formatDateTime(value);
}

export function quantityLabel(value: QuantityValue | null): string {
  if (value === null || value.value === null) return "数值未报告";
  if (value.unit === null) return `${value.value}（单位未报告）`;
  return `${value.value} ${value.unit}`;
}

export function jobStateLabel(value: string | null): string {
  if (value === null) return "状态未报告";
  if (value === "accepted") return "已受理";
  if (value === "queued") return "排队中";
  if (value === "running") return "采集中";
  if (value === "completed") return "已完成";
  if (value === "failed") return "失败";
  if (value === "denied" || value === "rejected") return "已拒绝";
  if (value === "cancelled") return "已取消";
  return value;
}

export function jobStateTone(value: string | null): StatusTone {
  if (value === "completed") return "good";
  if (value === "accepted" || value === "queued" || value === "running")
    return "progress";
  if (value === "failed" || value === "denied" || value === "rejected")
    return "bad";
  return "neutral";
}

export function impactModeLabel(value: ImpactMode): string {
  switch (value) {
    case "explain":
      return "解释模式（explain）";
    case "shadow":
      return "影子对照（shadow）";
    default:
      return "模式未报告";
  }
}

export function impactModeTone(value: ImpactMode): StatusTone {
  if (value === "shadow") return "progress";
  return "neutral";
}

export function impactActionLabel(value: ImpactAction): string {
  switch (value) {
    case "recompute":
      return "重新计算（recompute）";
    case "reuse_candidate":
      return "预计可复用（尚未跳过）";
    case "deny":
      return "不可复用（deny）";
    default:
      return "动作未报告";
  }
}

export function impactActionTone(value: ImpactAction): StatusTone {
  switch (value) {
    case "recompute":
      return "progress";
    case "reuse_candidate":
      return "attention";
    case "deny":
      return "bad";
    default:
      return "neutral";
  }
}

export function impactFallbackLabel(value: ImpactFallback): string {
  switch (value) {
    case "none":
      return "无需回退";
    case "full":
      return "回退全量（full）";
    case "deny":
      return "拒绝（deny）";
    default:
      return "回退策略未报告";
  }
}

export function dependencyCompleteLabel(value: boolean | null): string {
  if (value === true) return "依赖完整";
  if (value === false) return "依赖不完整";
  return "依赖完整性未报告";
}

export function impactReasonLabel(value: string): string {
  switch (value) {
    case "dependent_field_changed":
      return "依赖字段发生变化";
    case "unchanged_complete_dependencies":
      return "依赖未变化且完整";
    case "dependency_incomplete":
      return "依赖不完整";
    case "authorization_revoked":
      return "授权已撤销";
    case "denied":
      return "拒绝复用";
    default:
      return value;
  }
}
