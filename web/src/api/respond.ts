import { ApiError } from "./http";

/**
 * The one invalid-response error. Every API module names the response family
 * and the failing path, so a malformed payload always identifies both.
 */
export function invalidResponse(message: string, status = 200): never {
  throw new ApiError(message, status, "invalid_response", false, null);
}

/**
 * Membership check only. Callers keep their own failure policy: parser
 * modules reject the payload, display modules fall back to "unknown".
 */
export function enumValue<T extends string>(
  value: unknown,
  allowed: readonly T[],
): T | null {
  return typeof value === "string" &&
    (allowed as readonly string[]).includes(value)
    ? (value as T)
    : null;
}

export interface ResponseReader {
  invalid: (path: string) => never;
  object: (value: unknown, path: string) => Record<string, unknown>;
  record: (
    value: unknown,
    path: string,
    keys: readonly string[],
    required?: readonly string[],
  ) => Record<string, unknown>;
  text: (value: unknown, path: string) => string;
  optionalText: (value: unknown, path: string) => string | null;
  nullableText: (value: unknown, path: string) => string | null;
  maybeText: (value: unknown, path: string) => string | undefined;
  integer: (value: unknown, path: string, minimum?: number) => number;
  bool: (value: unknown, path: string) => boolean;
  nullableBool: (value: unknown, path: string) => boolean | null;
  array: <T>(
    value: unknown,
    path: string,
    parse: (entry: unknown, path: string) => T,
  ) => T[];
  nullableArray: <T>(
    value: unknown,
    path: string,
    parse: (entry: unknown, path: string) => T,
  ) => T[] | null;
  optionalArray: <T>(
    value: unknown,
    path: string,
    parse: (entry: unknown, path: string) => T,
  ) => T[] | undefined;
}

/**
 * Binds the shared payload readers to one response family. Modules keep their
 * own message text; the validation decisions live here once.
 */
export function responseReader(
  message: (path: string) => string,
  status = 200,
): ResponseReader {
  const invalid = (path: string): never => invalidResponse(message(path), status);
  const object = (value: unknown, path: string): Record<string, unknown> => {
    if (typeof value !== "object" || value === null || Array.isArray(value))
      return invalid(path);
    return value as Record<string, unknown>;
  };
  const record = (
    value: unknown,
    path: string,
    keys: readonly string[],
    required: readonly string[] = keys,
  ): Record<string, unknown> => {
    const item = object(value, path);
    if (
      Object.keys(item).some((key) => !keys.includes(key)) ||
      required.some((key) => !Object.hasOwn(item, key))
    )
      return invalid(path);
    return item;
  };
  const text = (value: unknown, path: string): string =>
    typeof value === "string" ? value : invalid(path);
  const optionalText = (value: unknown, path: string): string | null =>
    value === undefined || value === null ? null : text(value, path);
  const nullableText = (value: unknown, path: string): string | null =>
    value === null ? null : text(value, path);
  const maybeText = (value: unknown, path: string): string | undefined =>
    value === undefined ? undefined : text(value, path);
  const integer = (
    value: unknown,
    path: string,
    minimum?: number,
  ): number =>
    typeof value === "number" &&
    Number.isSafeInteger(value) &&
    (minimum === undefined || value >= minimum)
      ? value
      : invalid(path);
  const bool = (value: unknown, path: string): boolean =>
    typeof value === "boolean" ? value : invalid(path);
  const nullableBool = (value: unknown, path: string): boolean | null =>
    value === null ? null : bool(value, path);
  const array = <T>(
    value: unknown,
    path: string,
    parse: (entry: unknown, path: string) => T,
  ): T[] => {
    if (!Array.isArray(value)) return invalid(path);
    return value.map((entry, index) => parse(entry, `${path}[${index}]`));
  };
  const nullableArray = <T>(
    value: unknown,
    path: string,
    parse: (entry: unknown, path: string) => T,
  ): T[] | null => (value === null ? null : array(value, path, parse));
  const optionalArray = <T>(
    value: unknown,
    path: string,
    parse: (entry: unknown, path: string) => T,
  ): T[] | undefined =>
    value === undefined ? undefined : array(value, path, parse);
  return {
    invalid,
    object,
    record,
    text,
    optionalText,
    nullableText,
    maybeText,
    integer,
    bool,
    nullableBool,
    array,
    nullableArray,
    optionalArray,
  };
}
