// File metadata (manifest mirror):
// Purpose: Provides shared Beijing-time display formatting and precise readable time labels without changing submitted or persisted timestamps.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Shared Beijing-time formatter for displayed timestamps.
export function formatDateTime(value: string, seconds = false): string {
  if (value === "") return "时间未记录";
  const date = new Date(value);
  if (isInvalidTime(value)) return "时间格式异常";
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit",
    ...(seconds ? { second: "2-digit" as const } : {}),
    hourCycle: "h23",
  }).format(date);
}

export function isInvalidTime(value: string | null | undefined): value is string {
  if (typeof value !== "string" || value === "") return false;
  const calendar = /^(\d{4})-(\d{2})-(\d{2})(?:$|[Tt\s])/.exec(value.trim());
  if (calendar) {
    const year = Number(calendar[1]);
    const month = Number(calendar[2]);
    const day = Number(calendar[3]);
    const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
    const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1];
    if (!days || day < 1 || day > days) return true;
  }
  return Number.isNaN(new Date(value).getTime());
}

export function preciseTime(value: string | null | undefined): string {
  if (!value) return "未记录";
  if (isInvalidTime(value)) return "时间格式异常";
  const offset = new Intl.DateTimeFormat("en", {
    timeZone: "Asia/Shanghai", timeZoneName: "longOffset",
  }).formatToParts(new Date(value)).find((part) => part.type === "timeZoneName")?.value.replace(/^GMT/, "UTC");
  return `${formatDateTime(value, true)}（北京时间${offset ? ` ${offset}` : ""}）`;
}
