import type { WireSchemas } from "./wire.generated";
import { jsonInit, requestJson } from "./http";
import { paths } from "./paths";
import { isRecord } from "./guards";
import { invalidResponse } from "./respond";

export type SupportGrant = WireSchemas["SupportGrant"];
export type SupportGrantRequest = WireSchemas["SupportGrantRequest"];
export type SupportCaseView = WireSchemas["SupportCaseView"];

function fail(message: string): never {
  throw invalidResponse(message, 500);
}

function record(value: unknown, name: string): Record<string, unknown> {
  if (!isRecord(value)) fail(`${name}不是对象`);
  return value;
}

function text(row: Record<string, unknown>, key: string): string {
  const value = row[key];
  if (typeof value !== "string" || value.trim() === "") fail(`响应缺少 ${key}`);
  return value;
}

export function parseSupportGrant(value: unknown): SupportGrant {
  const row = record(value, "support_grant");
  const status = row.status;
  if (!["pending", "approved", "revoked", "expired"].includes(String(status))) fail("支援授权状态无效");
  if (!Array.isArray(row.actions) || row.actions.length !== 1 || row.actions[0] !== "case.read") fail("支援授权动作无效");
  if (row.approved_by !== null && typeof row.approved_by !== "string") fail("approved_by 无效");
  return {
    id: text(row, "id"),
    principal_id: text(row, "principal_id"),
    tenant_id: text(row, "tenant_id"),
    org_id: text(row, "org_id"),
    case_id: text(row, "case_id"),
    actions: ["case.read"],
    reason: text(row, "reason"),
    status: status as SupportGrant["status"],
    revision: text(row, "revision"),
    created_by: text(row, "created_by"),
    approved_by: row.approved_by as string | null,
    expires_at: text(row, "expires_at"),
  };
}

export function parseSupportGrantList(value: unknown): WireSchemas["SupportGrantList"] {
  const row = record(value, "support_grants");
  if (!Array.isArray(row.grants)) fail("支援授权列表缺少 grants");
  return { grants: row.grants.map(parseSupportGrant) };
}

export function parseSupportCaseView(value: unknown): SupportCaseView {
  const row = record(value, "support_case");
  if (row.read_only !== true) fail("支援案件视图必须是只读");
  return {
    grant_id: text(row, "grant_id"),
    case_id: text(row, "case_id"),
    tenant_id: text(row, "tenant_id"),
    org_id: text(row, "org_id"),
    title: text(row, "title"),
    contract_type: text(row, "contract_type"),
    business_status: text(row, "business_status"),
    created_at: text(row, "created_at"),
    updated_at: text(row, "updated_at"),
    read_only: true,
  };
}

export function listSupportGrants(signal?: AbortSignal) {
  return requestJson(paths.supportGrants, { method: "GET", signal }, parseSupportGrantList);
}

export function requestSupportGrant(caseId: string, input: SupportGrantRequest) {
  return requestJson(paths.caseSupportGrants(caseId), jsonInit("POST", input), parseSupportGrant);
}

export function approveSupportGrant(grantId: string, expectedRevision: string) {
  return requestJson(paths.supportGrantApprove(grantId), jsonInit("POST", { expected_revision: expectedRevision }), parseSupportGrant);
}

export function revokeSupportGrant(grantId: string, expectedRevision: string) {
  return requestJson(paths.supportGrantRevoke(grantId), jsonInit("POST", { expected_revision: expectedRevision }), parseSupportGrant);
}

export function getSupportGrantCase(grantId: string, signal?: AbortSignal) {
  return requestJson(paths.supportGrantCase(grantId), { method: "GET", signal }, parseSupportCaseView);
}
