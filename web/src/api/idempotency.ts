export function newIdempotencyKey(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export async function pendingMutationKey(
  operation: string,
  actorScope: { actorId: string; tenantId: string; orgId: string; audience: string },
  target: string,
  payload: unknown,
): Promise<{ storageKey: string; key: string }> {
  const fingerprint = await idempotencyFingerprint(
    JSON.stringify({ operation, actorScope, target, payload }),
  );
  const storageKey = `caw:mutation-idempotency:${fingerprint}`;
  const existing = sessionStorage.getItem(storageKey);
  const key = existing ?? newIdempotencyKey();
  if (existing === null) sessionStorage.setItem(storageKey, key);
  return { storageKey, key };
}

export function idempotencyActorScope(actor: {
  id: string;
  tenant_id: string;
  org_id: string;
  audience: string;
}) {
  return {
    actorId: actor.id,
    tenantId: actor.tenant_id,
    orgId: actor.org_id,
    audience: actor.audience,
  };
}

export function clearPendingMutationKey(storageKey: string): void {
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
