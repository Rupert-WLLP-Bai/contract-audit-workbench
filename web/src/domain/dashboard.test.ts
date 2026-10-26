import { describe, expect, it } from "vitest";
import type {
  WorkbenchCase,
  WorkbenchRun,
  WorkbenchTask,
} from "../api/types";
import {
  buildTrend,
  caseLink,
  caseTab,
  formatDuration,
  highRiskCount,
  isOverdue,
  myOpenTasks,
  parseCaseTab,
  PERIOD_LABELS,
  PERIOD_SCOPE,
  relativeDate,
  tabCounts,
  taskKindLabel,
  taskPriorityLabel,
  taskStatusLabel,
  trendHasData,
} from "./dashboard";

function run(overrides: Partial<WorkbenchRun> = {}): WorkbenchRun {
  return {
    id: "run-1",
    case_id: "case-1",
    case_title: "合同",
    state: "completed",
    created_at: "2026-10-20T08:00:00",
    updated_at: "2026-10-20T09:00:00",
    attempt_id: "attempt-1",
    started_at: "2026-10-20T08:00:00",
    finished_at: "2026-10-20T09:00:00",
    usage: null,
    ...overrides,
  };
}

function task(overrides: Partial<WorkbenchTask> = {}): WorkbenchTask {
  return {
    id: "task-1",
    case_id: "case-1",
    title: "任务",
    kind: "general",
    assignee_id: "me",
    due_at: null,
    status: "open",
    priority: "medium",
    finding_id: null,
    version: 1,
    created_at: "2026-10-20T08:00:00",
    created_by: "me",
    updated_at: "2026-10-20T08:00:00",
    ...overrides,
  };
}

function workbenchCase(
  overrides: Partial<WorkbenchCase> = {},
): WorkbenchCase {
  return {
    id: "case-1",
    title: "合同",
    contract_type: "service",
    document_count: 1,
    pending_decisions: 0,
    latest_run: null,
    updated_at: "2026-10-20T08:00:00",
    profile: null,
    severity: null,
    progress: null,
    ...overrides,
  };
}

describe("dashboard period and case tabs", () => {
  it("labels periods and scopes", () => {
    expect(PERIOD_LABELS).toEqual({ day: "今日", week: "本周", month: "本月" });
    expect(PERIOD_SCOPE.week).toContain("本周");
  });

  it("parses known case tabs and rejects unknown values", () => {
    expect(parseCaseTab("pending")).toBe("pending");
    expect(parseCaseTab("other")).toBe("other");
    expect(parseCaseTab("unknown")).toBe("all");
    expect(parseCaseTab(null)).toBe("all");
  });

  it("classifies every review state without inventing a completion", () => {
    expect(caseTab(workbenchCase())).toBe("pending");
    for (const state of ["accepted", "queued", "started", "running"]) {
      expect(
        caseTab(
          workbenchCase({
            latest_run: { id: "run", state, updated_at: "now" },
          }),
        ),
      ).toBe("reviewing");
    }
    expect(
      caseTab(
        workbenchCase({
          latest_run: { id: "run", state: "completed", updated_at: "now" },
        }),
      ),
    ).toBe("completed");
    for (const state of ["awaiting_human", "partial"]) {
      expect(
        caseTab(
          workbenchCase({
            latest_run: { id: "run", state, updated_at: "now" },
          }),
        ),
      ).toBe("pending");
    }
    for (const state of ["failed", "cancelled", "interrupted", "unexpected"]) {
      expect(
        caseTab(
          workbenchCase({
            latest_run: { id: "run", state, updated_at: "now" },
          }),
        ),
      ).toBe("other");
    }
  });

  it("counts tabs and high risk cases", () => {
    const items = [
      workbenchCase({ id: "1", severity: "high" }),
      workbenchCase({ id: "2", severity: "medium" }),
      workbenchCase({
        id: "3",
        severity: "high",
        latest_run: { id: "run", state: "completed", updated_at: "now" },
      }),
    ];
    expect(tabCounts(items)).toEqual({
      all: 3,
      pending: 2,
      reviewing: 0,
      completed: 1,
      other: 0,
    });
    expect(highRiskCount(items)).toBe(2);
  });
});

describe("duration and trend aggregation", () => {
  it("formats durations, preserving unknown and sub-second values", () => {
    expect(formatDuration(null)).toBe("未报告");
    expect(formatDuration(Number.NaN)).toBe("未报告");
    expect(formatDuration(-1)).toBe("未报告");
    expect(formatDuration(500)).toBe("不足 1 秒");
    expect(formatDuration(45_000)).toBe("45 秒");
    expect(formatDuration(125_000)).toBe("2 分钟");
    expect(formatDuration(60 * 60 * 1000)).toBe("1 小时");
    expect(formatDuration(90 * 60 * 1000)).toBe("1 小时 30 分钟");
  });

  it("buckets starts and completions by local day and ignores invalid runs", () => {
    const now = new Date("2026-10-21T12:00:00");
    const points = buildTrend(
      [
        run({
          started_at: "2026-10-21T08:00:00",
          finished_at: "2026-10-21T08:10:00",
        }),
        run({
          started_at: "2026-10-20T23:00:00",
          finished_at: "2026-10-21T01:00:00",
        }),
        run({ started_at: null, finished_at: "2026-10-21T02:00:00" }),
        run({
          started_at: "2026-10-21T03:00:00",
          finished_at: null,
        }),
        run({
          started_at: "2026-10-19T03:00:00",
          finished_at: "2026-10-19T02:00:00",
        }),
        run({
          started_at: "not-a-date",
          finished_at: "still-not-a-date",
        }),
        run({
          started_at: "2025-01-01T00:00:00",
          finished_at: "2025-01-01T01:00:00",
        }),
      ],
      now,
      3,
    );

    expect(points).toHaveLength(3);
    expect(points.map((point) => point.key)).toEqual([
      "2026-10-19",
      "2026-10-20",
      "2026-10-21",
    ]);
    const today = points[points.length - 1]!;
    expect(today.started).toBe(2);
    expect(today.completed).toBe(3);
    expect(today.averageMs).toBe(65 * 60 * 1000);
    expect(points[1]!.started).toBe(1);
    expect(points[1]!.completed).toBe(0);
    expect(points[1]!.averageMs).toBeNull();
    expect(points[0]!.averageMs).toBeNull();
    expect(trendHasData(points)).toBe(true);
    expect(
      trendHasData(
        points.map((point) => ({
          ...point,
          started: 0,
          completed: 0,
          averageMs: null,
        })),
      ),
    ).toBe(false);
  });
});

describe("tasks and links", () => {
  it("filters my open tasks and sorts by due then priority", () => {
    const tasks = [
      task({ id: "no-date-low", priority: "low" }),
      task({ id: "other", assignee_id: "other", due_at: "2026-10-19" }),
      task({ id: "done", status: "completed", due_at: "2026-10-18" }),
      task({ id: "late-high", due_at: "2026-10-19", priority: "high" }),
      task({ id: "late-medium", due_at: "2026-10-19", priority: "medium" }),
      task({ id: "early", due_at: "2026-10-18", priority: "low" }),
    ];
    expect(myOpenTasks(tasks, "me").map((item) => item.id)).toEqual([
      "early",
      "late-high",
      "late-medium",
      "no-date-low",
    ]);
    expect(myOpenTasks(tasks, null)).toEqual([]);
  });

  it("detects overdue tasks only from a valid saved due date", () => {
    const now = new Date("2026-10-20T12:00:00");
    expect(isOverdue(task(), now)).toBe(false);
    expect(isOverdue(task({ due_at: "2026-10-20T11:00:00" }), now)).toBe(true);
    expect(isOverdue(task({ due_at: "2026-10-20T13:00:00" }), now)).toBe(false);
    expect(isOverdue(task({ due_at: "invalid" }), now)).toBe(false);
  });

  it("labels known task fields without leaking raw enums", () => {
    expect(taskKindLabel("review")).toBe("审查任务");
    expect(taskKindLabel("decision")).toBe("人工决定");
    expect(taskKindLabel("remediation")).toBe("整改任务");
    expect(taskKindLabel("general")).toBe("一般任务");
    expect(taskPriorityLabel("high")).toBe("高优先级");
    expect(taskPriorityLabel("medium")).toBe("中优先级");
    expect(taskPriorityLabel("low")).toBe("低优先级");
    expect(taskStatusLabel("open")).toBe("待处理");
    expect(taskStatusLabel("in_progress")).toBe("进行中");
    expect(taskStatusLabel("completed")).toBe("已完成");
  });

  it("links runs and cases with encoded identifiers", () => {
    expect(caseLink({ id: "case/1", latest_run: null })).toBe("/cases/case%2F1");
    expect(
      caseLink({
        id: "case/1",
        latest_run: { id: "run 1", state: "completed", updated_at: "now" },
      }),
    ).toBe("/cases/case%2F1/reviews/run%201");
  });

  it("renders relative dates and falls back to an absolute date", () => {
    const now = new Date("2026-10-21T12:00:00");
    expect(relativeDate("invalid", now)).toBe("未报告");
    expect(relativeDate("2026-10-21T08:00:00", now)).toBe("今天");
    expect(relativeDate("2026-10-20T08:00:00", now)).toBe("昨天");
    expect(relativeDate("2026-10-18T08:00:00", now)).toBe("3 天前");
    expect(relativeDate("2026-10-01T08:00:00", now)).toMatch(/10.*01/);
    expect(relativeDate("2026-10-25T08:00:00", now)).toMatch(/2026/);
  });
});
