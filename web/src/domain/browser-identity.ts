// File metadata (manifest mirror):
// Purpose: Builds browser preference and recent-case storage keys from the stable effective account, tenant, organization, role and membership.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { Actor } from "../api/types";

/** Browser history and display preferences follow the stable effective identity. */
export function browserIdentityKey(actor: Actor | null | undefined): string {
  if (!actor?.active_context || !actor.id || !actor.tenant_id || !actor.org_id || !actor.role) {
    return "";
  }
  const context = actor.active_context;
  if (actor.tenant_id !== context.tenant_id || actor.org_id !== context.org_id) return "";
  return JSON.stringify([
    actor.audience,
    actor.tenant_id,
    actor.org_id,
    actor.id,
    actor.role,
    actor.principal_id,
    context.membership_id,
  ]);
}
