// Shared Beijing-time formatter for displayed timestamps.
export function formatDateTime(value: string, seconds = false): string {
  if (value === "") return "时间未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "时间未记录";
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit",
    ...(seconds ? { second: "2-digit" as const } : {}),
    hourCycle: "h23",
  }).format(date);
}

export function preciseTime(value: string | null | undefined): string {
  return !value || Number.isNaN(new Date(value).getTime())
    ? "未记录"
    : `${formatDateTime(value, true)}（北京时间）`;
}
