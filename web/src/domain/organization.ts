// File metadata (manifest mirror):
// Purpose: Maps known organization identifiers to display names and preserves a fallback for unknown IDs; this presentation mapping does not define tenant authorization.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-11-12
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import roster from "./demo-directory.json";

export interface DirectoryMember {
  id: string;
  display_name: string;
  role: string;
  tenant_id: string;
  org_id: string;
}

interface DemoDirectoryFile {
  organizations: { id: string; name: string }[];
  users: DirectoryMember[];
}

const directory = roster as DemoDirectoryFile;

const ORGANIZATION_NAMES: Record<string, string> = Object.fromEntries(
  directory.organizations.map((item) => [item.id, item.name]),
);

const LEGACY_LOCAL_DEMO_IDS = [
  "demo-admin",
  "demo-clerk",
  "demo-reviewer",
  "demo-viewer",
  "demo-otherorg-clerk",
] as const;

const LEGACY_PERSON_NAMES = new Set([
  "演示管理员",
  "演示经办",
  "演示审查员",
  "演示只读",
  "演示其他组织经办",
]);

const ROSTER_PERSON_NAMES = new Map(directory.users.map((user) => [user.id, user.display_name]));

export function knownOrganizationName(orgId: string | null | undefined): string | null {
  if (!orgId || !Object.hasOwn(ORGANIZATION_NAMES, orgId)) return null;
  return ORGANIZATION_NAMES[orgId] ?? null;
}

export function organizationName(orgId: string | null | undefined): string {
  if (!orgId) return "正在确认";
  return knownOrganizationName(orgId) ?? orgId;
}

// The running development API was built with the five fixed local-demo logins.
// Until that process is replaced, the organization page presents the same
// district roster the next process loads. A directory that already contains
// the district accounts is shown as returned.
// The current process still returns the previous Chinese labels for the fixed
// demo logins. The shell shows the roster name for those labels only.
export function presentedPersonName(id: string, displayName: string): string {
  if (!LEGACY_PERSON_NAMES.has(displayName)) return displayName;
  return ROSTER_PERSON_NAMES.get(id) ?? displayName;
}

export function presentOrganizationMembers(members: DirectoryMember[]): DirectoryMember[] {
  const legacy = new Set<string>(LEGACY_LOCAL_DEMO_IDS);
  if (members.length !== legacy.size || members.some((member) => !legacy.has(member.id))) return members;
  const tenant = members[0]?.tenant_id;
  const presented = directory.users.filter((user) => user.tenant_id === tenant);
  return presented.length > 0 ? presented : members;
}

export const DISTRICT_ORGANIZATION_NAMES = directory.organizations.map((item) => item.name);
