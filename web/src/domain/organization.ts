// File metadata (manifest mirror):
// Purpose: Maps known organization identifiers to display names and preserves a fallback for unknown IDs; this presentation mapping does not define tenant authorization.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-10-28
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

const ORGANIZATION_NAMES: Record<string, string> = {
  "demo-org": "两江新区分公司",
};

export function organizationName(orgId: string | null | undefined): string {
  if (!orgId) return "正在确认";
  return ORGANIZATION_NAMES[orgId] ?? orgId;
}
