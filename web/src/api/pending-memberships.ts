import type { WireSchemas } from "./wire.generated";
import { jsonInit, requestJson } from "./http";
import { paths } from "./paths";
import { parseEmpty } from "./parse";
import { responseReader } from "./respond";

export type MembershipChoice = WireSchemas["MembershipChoice"];
export type PendingMembershipAudience = "client" | "admin";

const reader = responseReader((path) => `登录身份选项无效：${path}`);

function parseMembershipChoice(value: unknown): MembershipChoice {
  const membership = reader.record(
    value,
    "membership",
    ["id", "jurisdiction_id", "org_id", "org_name", "role", "tenant_id", "tenant_name"],
    ["id", "org_id", "org_name", "role", "tenant_id", "tenant_name"],
  );
  return {
    id: reader.text(membership.id, "membership.id"),
    ...(membership.jurisdiction_id === undefined
      ? {}
      : { jurisdiction_id: reader.text(membership.jurisdiction_id, "membership.jurisdiction_id") }),
    org_id: reader.text(membership.org_id, "membership.org_id"),
    org_name: reader.text(membership.org_name, "membership.org_name"),
    role: reader.text(membership.role, "membership.role"),
    tenant_id: reader.text(membership.tenant_id, "membership.tenant_id"),
    tenant_name: reader.text(membership.tenant_name, "membership.tenant_name"),
  };
}

function parsePendingMembershipList(value: unknown): WireSchemas["PendingMembershipList"] {
  const result = reader.record(value, "pending_memberships", ["memberships"]);
  return {
    memberships: reader.array(result.memberships, "pending_memberships.memberships", parseMembershipChoice),
  };
}

export function listPendingMemberships(
  audience: PendingMembershipAudience,
  signal?: AbortSignal,
): Promise<WireSchemas["PendingMembershipList"]> {
  return requestJson(
    paths.oidcMemberships(audience),
    { method: "GET", signal },
    parsePendingMembershipList,
  );
}

export function selectPendingMembership(
  audience: PendingMembershipAudience,
  membershipId: string,
): Promise<null> {
  const body: WireSchemas["PendingMembershipSelectionRequest"] = {
    audience,
    membership_id: membershipId,
  };
  return requestJson(
    paths.oidcMemberships(audience),
    jsonInit("POST", body),
    parseEmpty,
  );
}
