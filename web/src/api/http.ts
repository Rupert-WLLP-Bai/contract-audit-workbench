export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;
  readonly details: unknown;

  constructor(
    message: string,
    status: number,
    code: string,
    retryable: boolean,
    details: unknown,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.retryable = retryable;
    this.details = details;
  }
}
let sessionIdentity: string | null = null;
let sessionEpoch = 0;

export function setSessionIdentity(
  actor: {
    id: string;
    role: string;
    audience?: string;
    tenant_id?: string;
    org_id?: string;
  } | null,
): boolean {
  const nextIdentity =
    actor === null
      ? null
      : `${actor.audience ?? "client"}\u0000${actor.id}\u0000${actor.role}\u0000${actor.tenant_id ?? ""}\u0000${actor.org_id ?? ""}`;
  if (sessionIdentity === nextIdentity) return false;
  sessionIdentity = nextIdentity;
  sessionEpoch += 1;
  return true;
}

export function isUnauthenticated(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.status === 401 || error.code === "unauthenticated")
  );
}

export function isConflict(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.status === 409 || error.code === "conflict")
  );
}

/** A 403 is an authorisation outcome: retrying the same request cannot succeed. */
export function isForbidden(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.status === 403 || error.code === "forbidden")
  );
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return "请求没有完成";
}

interface ErrorFields {
  message: string | null;
  code: string | null;
  retryable: boolean | null;
  details: unknown;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (typeof value !== "object" || value === null) return null;
  return value as Record<string, unknown>;
}

export function parseErrorPayload(payload: unknown): ErrorFields {
  const record = asRecord(payload);
  const nested = record ? asRecord(record.error) : null;
  const source = nested ?? record;
  if (!source)
    return { message: null, code: null, retryable: null, details: null };
  const directMessage = typeof record?.error === "string" ? record.error : null;
  const message =
    typeof source.message === "string" ? source.message : directMessage;
  const code = typeof source.code === "string" ? source.code : null;
  const retryable =
    typeof source.retryable === "boolean" ? source.retryable : null;
  return {
    message: message === null ? null : message.slice(0, 300),
    code,
    retryable,
    details: source.details ?? null,
  };
}

function defaultCode(status: number): string {
  if (status === 401) return "unauthenticated";
  if (status === 403) return "forbidden";
  if (status === 404) return "not_found";
  if (status === 409) return "conflict";
  if (status === 0) return "network";
  return "request_failed";
}

function defaultMessage(status: number): string {
  if (status === 401) return "登录已失效或账号密码不正确";
  if (status === 403) return "没有访问该内容的权限";
  if (status === 404) return "没有找到对应记录";
  if (status === 409) return "该事项已被他人更新，请按最新内容重新确认";
  if (status === 0) return "无法连接审查服务";
  return "审查服务暂时不能完成这个操作";
}

export async function errorFromResponse(response: Response): Promise<ApiError> {
  let payload: unknown = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  const parsed = parseErrorPayload(payload);
  return new ApiError(
    parsed.message && parsed.message.trim() !== ""
      ? parsed.message
      : defaultMessage(response.status),
    response.status,
    parsed.code ?? defaultCode(response.status),
    parsed.retryable ?? false,
    parsed.details,
  );
}

export async function requestJson<T>(
  path: string,
  init: RequestInit,
  parse: (value: unknown) => T,
): Promise<T> {
  const requestEpoch = sessionEpoch;
  const isSessionRequest =
    path === "/api/session" || path.startsWith("/api/session?");
  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      credentials: "include",
      cache: "no-store",
      headers: {
        Accept: "application/json",
        ...init.headers,
      },
    });
  } catch (error) {
    if (error instanceof Error && error.name === "AbortError") throw error;
    throw new ApiError("无法连接审查服务", 0, "network", true, null);
  }
  if (!isSessionRequest && requestEpoch !== sessionEpoch)
    throw new DOMException("Session changed", "AbortError");
  if (!response.ok) throw await errorFromResponse(response);
  if (response.status === 204) return parse(null);
  const text = await response.text();
  if (text.trim() === "") return parse(null);
  try {
    const value = JSON.parse(text) as unknown;
    if (!isSessionRequest && requestEpoch !== sessionEpoch)
      throw new DOMException("Session changed", "AbortError");
    return parse(value);
  } catch (error) {
    if (
      error instanceof ApiError ||
      (error instanceof Error && error.name === "AbortError")
    )
      throw error;
    throw new ApiError(
      "服务返回的内容无法识别",
      response.status,
      "invalid_response",
      false,
      null,
    );
  }
}

export function jsonInit(
  method: string,
  body?: unknown,
  headers?: Record<string, string>,
): RequestInit {
  return {
    method,
    headers: {
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      ...headers,
    },
    body: body === undefined ? null : JSON.stringify(body),
  };
}
