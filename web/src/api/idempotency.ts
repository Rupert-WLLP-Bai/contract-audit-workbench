import {
  assertCommandContextCurrent,
  bindIdempotencyCommandContext,
  captureCommandContextIfAvailable,
  clearIdempotencyCommandContext,
  sessionIdentityFor,
  type CommandContext,
} from "./http";

export function newIdempotencyKey(): string {
  const key = typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, "0")).join("");
  const context = captureCommandContextIfAvailable();
  if (context) bindIdempotencyCommandContext(key, context);
  return key;
}

export async function pendingMutationKey(
  operation: string,
  actorScope: { actorId: string; tenantId: string; orgId: string; audience: string; contextVersion?: string; sessionIdentity?: string },
  target: string,
  payload: unknown,
  commandSnapshot?: CommandContext,
): Promise<{ storageKey: string; key: string }> {
  const commandContext = commandSnapshot ?? captureCommandContextIfAvailable();
  if (!commandContext && actorScope.sessionIdentity !== undefined)
    throw new DOMException("Session changed", "AbortError");
  if (
    commandContext && (
      (actorScope.contextVersion !== undefined && commandContext.contextVersion !== actorScope.contextVersion) ||
      (actorScope.sessionIdentity !== undefined && commandContext.identity !== actorScope.sessionIdentity)
    )
  ) throw new DOMException("Context changed", "AbortError");
  const fingerprint = await idempotencyFingerprint(
    JSON.stringify({
      operation,
      actorScope,
      sessionIdentity: actorScope.sessionIdentity ?? commandContext?.identity ?? null,
      contextVersion: actorScope.contextVersion ?? commandContext?.contextVersion ?? null,
      target,
      payload,
    }),
  );
  if (commandContext) assertCommandContextCurrent(commandContext);
  const storageKey = `caw:mutation-idempotency:${fingerprint}`;
  const existing = sessionStorage.getItem(storageKey);
  const key = existing ?? newIdempotencyKey();
  if (commandContext) bindIdempotencyCommandContext(key, commandContext);
  if (existing === null) sessionStorage.setItem(storageKey, key);
  return { storageKey, key };
}

export function idempotencyActorScope(actor: {
  id: string;
  role?: string;
  tenant_id: string;
  org_id: string;
  audience: string;
  active_context?: { context_version: string };
  csrf_token?: string;
  principal_id?: string | null;
}) {
  return {
    actorId: actor.id,
    tenantId: actor.tenant_id,
    orgId: actor.org_id,
    audience: actor.audience,
    contextVersion: actor.active_context?.context_version,
    sessionIdentity: sessionIdentityFor({ ...actor, role: actor.role ?? "" }),
  };
}

export function clearPendingMutationKey(storageKey: string): void {
  const key = sessionStorage.getItem(storageKey);
  if (key) clearIdempotencyCommandContext(key);
  sessionStorage.removeItem(storageKey);
}

export async function idempotencyFingerprint(value: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export async function fileSha256(file: File): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
