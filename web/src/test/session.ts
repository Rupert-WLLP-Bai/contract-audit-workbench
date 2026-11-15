// File metadata (manifest mirror):
// Purpose: Provides the shared authenticated test actor and command-context installation helpers so unit tests exercise the same session identity, context version and CSRF fields the web client expects; it does not grant authorization.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import {
  bindIdempotencyCommandContext,
  captureCommandContext,
  clearIdempotencyCommandContext,
  setSessionIdentity,
} from "../api/http";
import type { Actor, SessionAudience } from "../api/types";

export function testActor(audience: SessionAudience = "client"): Actor {
  const admin = audience === "admin";
  return {
    id: admin ? "admin-test" : "actor-test",
    display_name: admin ? "测试管理员" : "测试审查员",
    role: admin ? "admin" : "reviewer",
    tenant_id: "tenant-test",
    org_id: "org-test",
    audience,
    principal_id: admin ? "principal-admin-test" : "principal-actor-test",
    active_context: {
      membership_id: "membership-test",
      tenant_id: "tenant-test",
      tenant_name: "测试租户",
      org_id: "org-test",
      org_name: "测试组织",
      context_version: "1",
    },
    csrf_token: "test-csrf",
    context_switch_enabled: false,
  };
}

export function installTestSession(audience: SessionAudience = "client"): Actor {
  const actor = testActor(audience);
  setSessionIdentity(actor);
  return actor;
}

export function clearTestSession(): void {
  setSessionIdentity(null);
}

/** Binds caller-supplied idempotency keys the same way the UI's newIdempotencyKey does. */
export function bindTestIdempotency(...keys: string[]): void {
  const context = captureCommandContext();
  for (const key of keys) {
    clearIdempotencyCommandContext(key);
    bindIdempotencyCommandContext(key, context);
  }
}
