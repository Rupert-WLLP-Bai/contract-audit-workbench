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
export interface CommandContext {
  contextVersion: string;
  csrfToken: string;
  identity: string;
  epoch: number;
}
let activeCommandContext: CommandContext | null = null;
type IdempotencyBinding = { context: CommandContext } | { invalidated: true };
type IdempotencyContextLookup =
  | { status: "bound"; context: CommandContext }
  | { status: "invalidated" }
  | { status: "unknown" };
const MAX_IDEMPOTENCY_BINDINGS = 2048;
const idempotencyContexts = new Map<string, IdempotencyBinding>();

function saveIdempotencyBinding(key: string, binding: IdempotencyBinding): void {
  idempotencyContexts.delete(key);
  idempotencyContexts.set(key, binding);
  while (idempotencyContexts.size > MAX_IDEMPOTENCY_BINDINGS) {
    const oldest = idempotencyContexts.keys().next().value;
    if (oldest === undefined) break;
    idempotencyContexts.delete(oldest);
  }
}

function invalidateIdempotencyBindings(): void {
  for (const key of [...idempotencyContexts.keys()])
    saveIdempotencyBinding(key, { invalidated: true });
}

export function sessionIdentityFor(actor: {
  id: string;
  role: string;
  audience?: string;
  tenant_id?: string;
  org_id?: string;
  principal_id?: string | null;
  active_context?: { context_version: string };
  csrf_token?: string;
}): string {
  return `${actor.audience ?? "client"}\u0000${actor.id}\u0000${actor.role}\u0000${actor.tenant_id ?? ""}\u0000${actor.org_id ?? ""}\u0000${actor.principal_id ?? ""}\u0000${actor.active_context?.context_version ?? ""}\u0000${actor.csrf_token ?? ""}`;
}

export function captureCommandContext(actor?: {
  id: string;
  role: string;
  audience?: string;
  tenant_id?: string;
  org_id?: string;
  principal_id?: string | null;
  active_context?: { context_version: string };
  csrf_token?: string;
}): CommandContext {
  const requestedIdentity = actor ? sessionIdentityFor(actor) : null;
  if (requestedIdentity && requestedIdentity !== sessionIdentity)
    throw new DOMException("Session changed", "AbortError");
  const snapshot = activeCommandContext;
  if (!snapshot) throw new Error("登录上下文不可用，请刷新后重试");
  if (requestedIdentity && requestedIdentity !== snapshot.identity)
    throw new DOMException("Context changed", "AbortError");
  return { ...snapshot };
}

export function captureCommandContextIfAvailable(): CommandContext | null {
  return activeCommandContext ? { ...activeCommandContext } : null;
}

export function isCommandContextVersionCurrent(contextVersion: string): boolean {
  return activeCommandContext?.contextVersion === contextVersion;
}

export function isSessionIdentityCurrent(identity: string): boolean {
  return identity !== "" && identity === sessionIdentity && activeCommandContext?.identity === identity;
}

export function assertCommandContextCurrent(context: CommandContext): void {
  if (
    context.epoch !== sessionEpoch ||
    context.contextVersion !== activeCommandContext?.contextVersion ||
    context.csrfToken !== activeCommandContext?.csrfToken ||
    context.identity !== sessionIdentity ||
    context.identity !== activeCommandContext?.identity
  ) throw new DOMException("Context changed", "AbortError");
}

export function bindIdempotencyCommandContext(key: string, context: CommandContext): void {
  assertCommandContextCurrent(context);
  saveIdempotencyBinding(key, { context: { ...context } });
}

export function idempotencyCommandContext(key: string | null): IdempotencyContextLookup {
  if (!key) return { status: "unknown" };
  const binding = idempotencyContexts.get(key);
  if (!binding) return { status: "unknown" };
  if ("invalidated" in binding) return { status: "invalidated" };
  return { status: "bound", context: { ...binding.context } };
}

export function clearIdempotencyCommandContext(key: string): void {
  idempotencyContexts.delete(key);
}

export function invalidateSessionIdentity(): void {
  sessionIdentity = null;
  sessionEpoch += 1;
  activeCommandContext = null;
  invalidateIdempotencyBindings();
}

export function setSessionIdentity(
  actor: {
    id: string;
    role: string;
    audience?: string;
    tenant_id?: string;
    org_id?: string;
    active_context?: { context_version: string };
    csrf_token?: string;
    principal_id?: string | null;
  } | null,
): boolean {
  const nextIdentity = actor === null ? null : sessionIdentityFor(actor);
  if (sessionIdentity === nextIdentity) {
    if (actor?.active_context && actor.csrf_token) {
      activeCommandContext = {
        contextVersion: actor.active_context.context_version,
        csrfToken: actor.csrf_token,
        identity: nextIdentity ?? "",
        epoch: sessionEpoch,
      };
    }
    return false;
  }
  invalidateIdempotencyBindings();
  sessionIdentity = nextIdentity;
  sessionEpoch += 1;
  activeCommandContext = actor?.active_context && actor.csrf_token
    ? { contextVersion: actor.active_context.context_version, csrfToken: actor.csrf_token, identity: nextIdentity ?? "", epoch: sessionEpoch }
    : null;
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

const MAX_QUERY_RETRIES = 2;

/** Retry only explicitly retryable API reads; auth and conflict outcomes are final. */
export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  return (
    failureCount < MAX_QUERY_RETRIES &&
    error instanceof ApiError &&
    error.retryable &&
    !isUnauthenticated(error) &&
    !isForbidden(error) &&
    !isConflict(error)
  );
}

/** Exponential backoff with a fixed upper bound for query retries. */
export function queryRetryDelay(failureCount: number): number {
  return Math.min(500 * 2 ** Math.max(0, failureCount), 4_000);
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return "请求没有完成";
}

/** Status 0 is the fetch-failure sentinel; everything else is an HTTP code. */
export function statusLabel(status: number): string {
  return status === 0 ? "无法连接服务" : `HTTP ${status}`;
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

function errorFieldsFrom(record: Record<string, unknown>): ErrorFields {
  const nested = asRecord(record.error);
  const source = nested ?? record;
  const directMessage =
    typeof record.error === "string" ? record.error : null;
  const message =
    (typeof source.message === "string" ? source.message : null) ??
    directMessage;
  return {
    message: message === null ? null : message.slice(0, 300),
    code: typeof source.code === "string" ? source.code : null,
    retryable:
      typeof source.retryable === "boolean" ? source.retryable : null,
    details: source.details ?? null,
  };
}

export function parseErrorPayload(payload: unknown): ErrorFields {
  const record = asRecord(payload);
  if (!record)
    return { message: null, code: null, retryable: null, details: null };
  return errorFieldsFrom(record);
}

const defaultOutcomes: Record<number, { code: string; message: string }> = {
  401: { code: "unauthenticated", message: "登录已失效或账号密码不正确" },
  403: { code: "forbidden", message: "没有访问该内容的权限" },
  404: { code: "not_found", message: "没有找到对应记录" },
  409: { code: "conflict", message: "该事项已被他人更新，请按最新内容重新确认" },
  0: { code: "network", message: "无法连接审查服务" },
};
const requestFailure = {
  code: "request_failed",
  message: "审查服务暂时不能完成这个操作",
};

function defaultOutcome(status: number): { code: string; message: string } {
  return defaultOutcomes[status] ?? requestFailure;
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
      : defaultOutcome(response.status).message,
    response.status,
    parsed.code ?? defaultOutcome(response.status).code,
    parsed.retryable ?? false,
    parsed.details,
  );
}

async function fetchResponse(
  path: string,
  init: RequestInit,
): Promise<Response> {
  try {
    return await fetch(path, {
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
}

function assertSessionEpoch(
  requestEpoch: number,
  isSessionRequest: boolean,
): void {
  if (!isSessionRequest && requestEpoch !== sessionEpoch)
    throw new DOMException("Session changed", "AbortError");
}

async function readJsonResponse<T>(
  response: Response,
  parse: (value: unknown) => T,
  requestEpoch: number,
  isSessionRequest: boolean,
): Promise<T> {
  if (response.status === 204) return parse(null);
  const text = await response.text();
  if (text.trim() === "") return parse(null);
  try {
    const value = JSON.parse(text) as unknown;
    assertSessionEpoch(requestEpoch, isSessionRequest);
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

export async function requestJson<T>(
  path: string,
  init: RequestInit & { commandContext?: CommandContext },
  parse: (value: unknown) => T,
): Promise<T> {
  const requestEpoch = sessionEpoch;
  const isSessionRequest =
    path === "/api/session" || path.startsWith("/api/session?");
  const method = (init.method ?? "GET").toUpperCase();
  const isWrite = !["GET", "HEAD", "OPTIONS"].includes(method);
  const isPreSessionProof =
    (path === "/api/session" && method === "POST") ||
    path === "/api/auth/oidc/memberships" ||
    path.startsWith("/api/auth/oidc/memberships?");
  const headers = new Headers(init.headers);
  const idempotencyKey = headers.get("Idempotency-Key");
  const idempotencyBinding = idempotencyCommandContext(idempotencyKey);
  let commandContext: CommandContext | null;
  if (isWrite && !isPreSessionProof && idempotencyKey !== null) {
    if (idempotencyBinding.status === "invalidated" ||
      (idempotencyBinding.status === "unknown" && !init.commandContext))
      throw new DOMException("Idempotency command context is unavailable", "AbortError");
    if (
      idempotencyBinding.status === "bound" && init.commandContext && (
        init.commandContext.identity !== idempotencyBinding.context.identity ||
        init.commandContext.epoch !== idempotencyBinding.context.epoch ||
        init.commandContext.contextVersion !== idempotencyBinding.context.contextVersion ||
        init.commandContext.csrfToken !== idempotencyBinding.context.csrfToken
      )
    ) throw new DOMException("Idempotency command context changed", "AbortError");
    commandContext = init.commandContext ?? (idempotencyBinding.status === "bound" ? idempotencyBinding.context : null);
  } else {
    commandContext = init.commandContext ?? (isWrite && !isPreSessionProof ? captureCommandContext() : null);
  }
  if (commandContext) assertCommandContextCurrent(commandContext);
  if (commandContext) {
    headers.set("X-Context-Version", commandContext.contextVersion);
    if (!isPreSessionProof) headers.set("X-CSRF-Token", commandContext.csrfToken);
  }
  const { commandContext: _commandContext, ...requestInit } = init;
  const response = await fetchResponse(path, {
    ...requestInit,
    headers: Object.fromEntries(headers.entries()),
  });
  assertSessionEpoch(requestEpoch, isSessionRequest);
  if (!response.ok) throw await errorFromResponse(response);
  return readJsonResponse(response, parse, requestEpoch, isSessionRequest);
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
