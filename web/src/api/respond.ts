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

type Invalid = (path: string) => never;

function objectReader(
  invalid: Invalid,
  value: unknown,
  path: string,
): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value))
    return invalid(path);
  return value as Record<string, unknown>;
}

function recordReader(
  invalid: Invalid,
  value: unknown,
  path: string,
  keys: readonly string[],
  required: readonly string[],
): Record<string, unknown> {
  const item = objectReader(invalid, value, path);
  if (
    Object.keys(item).some((key) => !keys.includes(key)) ||
    required.some((key) => !Object.hasOwn(item, key))
  )
    return invalid(path);
  return item;
}

function textReader(invalid: Invalid, value: unknown, path: string): string {
  return typeof value === "string" ? value : invalid(path);
}

function optionalTextReader(
  invalid: Invalid,
  value: unknown,
  path: string,
): string | null {
  return value === undefined || value === null
    ? null
    : textReader(invalid, value, path);
}

function sentinelTextReader<S extends null | undefined>(
  invalid: Invalid,
  value: unknown,
  path: string,
  sentinel: S,
): string | S {
  return value === sentinel ? sentinel : textReader(invalid, value, path);
}

function integerReader(
  invalid: Invalid,
  value: unknown,
  path: string,
  minimum?: number,
): number {
  return typeof value === "number" &&
    Number.isSafeInteger(value) &&
    (minimum === undefined || value >= minimum)
    ? value
    : invalid(path);
}

function boolReader(invalid: Invalid, value: unknown, path: string): boolean {
  return typeof value === "boolean" ? value : invalid(path);
}

function nullableBoolReader(
  invalid: Invalid,
  value: unknown,
  path: string,
): boolean | null {
  return value === null ? null : boolReader(invalid, value, path);
}

function arrayReader<T>(
  invalid: Invalid,
  value: unknown,
  path: string,
  parse: (entry: unknown, path: string) => T,
): T[] {
  if (!Array.isArray(value)) return invalid(path);
  return value.map((entry, index) => parse(entry, `${path}[${index}]`));
}

function sentinelArrayReader<T, S extends null | undefined>(
  invalid: Invalid,
  value: unknown,
  path: string,
  sentinel: S,
  parse: (entry: unknown, path: string) => T,
): T[] | S {
  return value === sentinel
    ? sentinel
    : arrayReader(invalid, value, path, parse);
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
  return {
    invalid,
    object: (value, path) => objectReader(invalid, value, path),
    record: (value, path, keys, required = keys) =>
      recordReader(invalid, value, path, keys, required),
    text: (value, path) => textReader(invalid, value, path),
    optionalText: (value, path) => optionalTextReader(invalid, value, path),
    nullableText: (value, path) =>
      sentinelTextReader(invalid, value, path, null),
    maybeText: (value, path) =>
      sentinelTextReader(invalid, value, path, undefined),
    integer: (value, path, minimum) => integerReader(invalid, value, path, minimum),
    bool: (value, path) => boolReader(invalid, value, path),
    nullableBool: (value, path) => nullableBoolReader(invalid, value, path),
    array: (value, path, parse) => arrayReader(invalid, value, path, parse),
    nullableArray: (value, path, parse) =>
      sentinelArrayReader(invalid, value, path, null, parse),
    optionalArray: (value, path, parse) =>
      sentinelArrayReader(invalid, value, path, undefined, parse),
  };
}
