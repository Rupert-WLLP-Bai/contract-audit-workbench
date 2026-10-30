/**
 * `<input type="datetime-local">` speaks local wall time while the API accepts
 * RFC 3339 UTC instants. Forms convert through these two functions so an
 * invalid or empty draft stays empty instead of "now". `precision` follows the
 * input's step: minute steps need `YYYY-MM-DDTHH:mm`, sub-second steps need
 * milliseconds.
 */
const INPUT_LENGTH = { minute: 16, millisecond: 23 } as const;

export function toLocalInputValue(
  value: string,
  precision: keyof typeof INPUT_LENGTH = "minute",
): string {
  const date = new Date(value);
  if (value === "" || Number.isNaN(date.getTime())) return "";
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
    .toISOString()
    .slice(0, INPUT_LENGTH[precision]);
}

export function toUtcInstant(value: string): string {
  const date = new Date(value);
  return value === "" || Number.isNaN(date.getTime()) ? "" : date.toISOString();
}
