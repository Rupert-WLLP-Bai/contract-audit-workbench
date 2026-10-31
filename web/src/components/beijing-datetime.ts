const BEIJING_OFFSET_MINUTES = 8 * 60;

export function toBeijingInputValue(value: string): string {
  const instant = new Date(value);
  if (value === "" || Number.isNaN(instant.getTime())) return "";
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(instant);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((item) => item.type === type)?.value ?? "";
  return `${part("year")}-${part("month")}-${part("day")}T${part("hour")}:${part("minute")}`;
}

export function beijingInputToUtc(value: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value);
  if (!match) return "";
  const [, year, month, day, hour, minute] = match;
  const wallTime = [year, month, day, hour, minute].map(Number);
  const [y, m, d, h, min] = wallTime;
  const localAsUtc = Date.UTC(y!, m! - 1, d!, h!, min!);
  const utc = new Date(localAsUtc - BEIJING_OFFSET_MINUTES * 60_000);
  const check = toBeijingInputValue(utc.toISOString());
  return check === value ? utc.toISOString() : "";
}
