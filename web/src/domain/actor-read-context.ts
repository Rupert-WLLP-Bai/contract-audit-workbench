import type { Actor } from "../api/types";

// Read caches follow the effective authorization context without storing CSRF.
export function actorReadContextKey(actor: Actor | null): string {
  if (actor === null) return "";
  return JSON.stringify([
    actor.audience, actor.tenant_id, actor.org_id, actor.id, actor.role,
    actor.principal_id, actor.active_context?.membership_id,
    actor.active_context?.context_version,
    actor.active_context?.tenant_id, actor.active_context?.org_id,
  ]);
}
