import type {
  WorkbenchCase,
  WorkbenchPeriod,
  WorkbenchRun,
  WorkbenchTask,
} from "../api/types";

export const PERIOD_LABELS: Record<WorkbenchPeriod, string> = {
  day: "今日",
  week: "本周",
  month: "本月",
};

export const PERIOD_SCOPE: Record<WorkbenchPeriod, string> = {
  day: "已完成与平均时长按今日统计",
  week: "已完成与平均时长按本周统计",
  month: "已完成与平均时长按本月统计",
};

export const RISK_ROWS = [
  { key: "high", label: "高风险", color: "#ef4444" },
  { key: "medium", label: "中风险", color: "#f59e0b" },
  { key: "low", label: "低风险", color: "#10b981" },
  { key: "info", label: "提示", color: "#2563eb" },
  { key: "unrated", label: "未评级", color: "#94a3b8" },
] as const;

export type CaseTab = "all" | "pending" | "reviewing" | "completed" | "other";

export const CASE_TABS: { key: CaseTab; label: string }[] = [
  { key: "all", label: "全部" },
  { key: "pending", label: "待审查" },
  { key: "reviewing", label: "审查中" },
  { key: "completed", label: "已完成" },
  { key: "other", label: "异常" },
];

export function parseCaseTab(value: string | null): CaseTab {
  return CASE_TABS.some((tab) => tab.key === value)
    ? (value as CaseTab)
    : "all";
}

const ACTIVE_STATES = ["accepted", "queued", "started", "running"];

export function caseTab(item: Pick<WorkbenchCase, "latest_run">): CaseTab {
  const state = item.latest_run?.state;
  if (state === undefined) return "pending";
  if (ACTIVE_STATES.includes(state)) return "reviewing";
  if (state === "completed") return "completed";
  if (state === "awaiting_human" || state === "partial") return "pending";
  return "other";
}

export function tabCounts(
  items: Pick<WorkbenchCase, "latest_run">[],
): Record<CaseTab, number> {
  const counts: Record<CaseTab, number> = {
    all: items.length,
    pending: 0,
    reviewing: 0,
    completed: 0,
    other: 0,
  };
  for (const item of items) counts[caseTab(item)] += 1;
  return counts;
}

export function highRiskCount(
  items: Pick<WorkbenchCase, "severity">[],
): number {
  return items.filter((item) => item.severity === "high").length;
}

export function formatDuration(milliseconds: number | null): string {
  if (milliseconds === null || !Number.isFinite(milliseconds) || milliseconds < 0)
    return "未报告";
  if (milliseconds < 1000) return "不足 1 秒";
  const seconds = Math.round(milliseconds / 1000);
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} 小时` : `${hours} 小时 ${rest} 分钟`;
}

export type TrendPoint = {
  key: string;
  label: string;
  started: number;
  completed: number;
  averageMs: number | null;
};

function dayKey(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

function parseTime(value: string | null): Date | null {
  if (!value) return null;
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

/**
 * Buckets real review runs into local calendar days. A run counts as started
 * on its start day and completed on its finish day; duration only contributes
 * when both timestamps exist and are ordered.
 */
export function buildTrend(
  runs: WorkbenchRun[],
  now: Date,
  days = 14,
): TrendPoint[] {
  const buckets = new Map<
    string,
    { label: string; started: number; completed: number; totalMs: number; withDuration: number }
  >();
  const points: TrendPoint[] = [];
  for (let index = days - 1; index >= 0; index -= 1) {
    const date = new Date(now);
    date.setHours(0, 0, 0, 0);
    date.setDate(date.getDate() - index);
    const key = dayKey(date);
    buckets.set(key, {
      label: `${String(date.getMonth() + 1).padStart(2, "0")}/${String(date.getDate()).padStart(2, "0")}`,
      started: 0,
      completed: 0,
      totalMs: 0,
      withDuration: 0,
    });
    points.push({ key, label: "", started: 0, completed: 0, averageMs: null });
  }
  const first = points[0]?.key;
  const last = points.at(-1)?.key;

  for (const run of runs) {
    const startedAt = parseTime(run.started_at);
    if (startedAt) {
      const key = dayKey(startedAt);
      if (key >= (first ?? "") && key <= (last ?? "")) {
        const bucket = buckets.get(key);
        if (bucket) bucket.started += 1;
      }
    }
    const finishedAt = parseTime(run.finished_at);
    if (!finishedAt) continue;
    const key = dayKey(finishedAt);
    if (!(key >= (first ?? "") && key <= (last ?? ""))) continue;
    const bucket = buckets.get(key);
    if (!bucket) continue;
    bucket.completed += 1;
    if (startedAt && finishedAt.getTime() >= startedAt.getTime()) {
      bucket.totalMs += finishedAt.getTime() - startedAt.getTime();
      bucket.withDuration += 1;
    }
  }

  return points.map((point) => {
    const bucket = buckets.get(point.key)!;
    return {
      ...point,
      label: bucket.label,
      started: bucket.started,
      completed: bucket.completed,
      averageMs:
        bucket.withDuration === 0
          ? null
          : Math.round(bucket.totalMs / bucket.withDuration),
    };
  });
}

export function trendHasData(points: TrendPoint[]): boolean {
  return points.some(
    (point) => point.started > 0 || point.completed > 0,
  );
}

export function myOpenTasks(
  tasks: WorkbenchTask[],
  actorId: string | null | undefined,
): WorkbenchTask[] {
  if (!actorId) return [];
  const priorityRank = { high: 0, medium: 1, low: 2 } as const;
  return tasks
    .filter(
      (task) => task.assignee_id === actorId && task.status !== "completed",
    )
    .sort((left, right) => {
      const dueLeft = left.due_at ?? "";
      const dueRight = right.due_at ?? "";
      if (dueLeft !== dueRight) {
        if (dueLeft === "") return 1;
        if (dueRight === "") return -1;
        return dueLeft.localeCompare(dueRight);
      }
      return priorityRank[left.priority] - priorityRank[right.priority];
    });
}

export function isOverdue(task: WorkbenchTask, now: Date): boolean {
  if (!task.due_at) return false;
  const due = parseTime(task.due_at);
  return due !== null && due.getTime() < now.getTime();
}

export function taskKindLabel(kind: WorkbenchTask["kind"]): string {
  return (
    {
      review: "审查任务",
      decision: "人工决定",
      remediation: "整改任务",
      general: "一般任务",
    }[kind] ?? "任务"
  );
}

export function taskPriorityLabel(priority: WorkbenchTask["priority"]): string {
  return { high: "高优先级", medium: "中优先级", low: "低优先级" }[priority];
}

export function taskStatusLabel(status: WorkbenchTask["status"]): string {
  return { open: "待处理", in_progress: "进行中", completed: "已完成" }[status];
}

export function caseLink(
  item: Pick<WorkbenchCase, "id" | "latest_run">,
): string {
  const id = encodeURIComponent(item.id);
  if (!item.latest_run) return `/cases/${id}`;
  return `/cases/${id}/reviews/${encodeURIComponent(item.latest_run.id)}`;
}

export function relativeDate(value: string, now: Date): string {
  const date = parseTime(value);
  if (!date) return "未报告";
  const days = Math.floor(
    (now.getTime() - date.getTime()) / (24 * 60 * 60 * 1000),
  );
  if (days < 0) return date.toLocaleDateString("zh-CN");
  if (days === 0) return "今天";
  if (days === 1) return "昨天";
  if (days < 7) return `${days} 天前`;
  return date.toLocaleDateString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
  });
}
