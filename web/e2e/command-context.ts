// File metadata (manifest mirror):
// Purpose: Reads the current authenticated browser session context and CSRF proof once for positive setup commands while preserving rejected and stale-session failures.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import assert from "node:assert/strict";
import type { APIRequestContext } from "playwright";

type SessionResponse = {
  active_context?: { context_version?: unknown } | null;
  csrf_token?: unknown;
};

// Read the proof from this browser context's authenticated session once. Never
// retry or refresh it after a rejected command; stale-session failures stay visible.
export async function browserSessionCommandHeaders(
  request: APIRequestContext,
  baseURL: string,
): Promise<Record<string, string>> {
  const response = await request.get(new URL("/api/session", baseURL).toString());
  assert.equal(response.status(), 200, `current browser session returned HTTP ${response.status()}`);
  const session = await response.json() as SessionResponse;
  const version = session.active_context?.context_version;
  const csrf = session.csrf_token;
  if (typeof version !== "string" || version.length === 0 || typeof csrf !== "string" || csrf.length === 0) {
    throw new Error("current browser session omitted context version or CSRF proof");
  }
  return { "X-Context-Version": version, "X-CSRF-Token": csrf };
}
