// File metadata (manifest mirror):
// Purpose: 声明 `TenantCostPolicyWindow`, `TenantCostPolicy`, `nullableAmount`, `result` 等 API 类型、解析器或请求函数，校验服务端响应并封装受控调用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useQuery } from "@tanstack/react-query";
import { jsonInit, requestJson, type CommandContext } from "./http";
import { responseReader } from "./respond";

export interface TenantCostPolicyWindow {
  period_key: string;
  limit_minor: string | null;
  reserved_minor: string;
  settled_minor: string;
  unknown_hold_minor: string;
  exposure_minor: string;
}
export interface TenantCostPolicy {
  schema_version: "tenant-cost-policy/v1";
  tenant_id: string;
  revision: string;
  configured: boolean;
  currency: "USD";
  minor_unit_scale: 6;
  timezone: "UTC";
  price_state: "unconfigured" | "known";
  day_limit_minor: string | null;
  month_limit_minor: string | null;
  day: TenantCostPolicyWindow;
  month: TenantCostPolicyWindow;
}

const { object, text, bool, invalid } = responseReader((path) => `租户费用政策响应中的 ${path} 无法识别`);
function nullableAmount(value: unknown, path: string): string | null {
  if (value === null) return null;
  const result = text(value, path);
  return /^(0|[1-9][0-9]*)$/.test(result) ? result : invalid(path);
}
function amount(value: unknown, path: string): string {
  const result = nullableAmount(value, path);
  return result === null ? invalid(path) : result;
}
function windowValue(value: unknown, path: string, pattern: RegExp): TenantCostPolicyWindow {
  const item = object(value, path);
  const period = text(item.period_key, `${path}.period_key`);
  if (!pattern.test(period)) return invalid(`${path}.period_key`);
  return {
    period_key: period,
    limit_minor: nullableAmount(item.limit_minor, `${path}.limit_minor`),
    reserved_minor: amount(item.reserved_minor, `${path}.reserved_minor`),
    settled_minor: amount(item.settled_minor, `${path}.settled_minor`),
    unknown_hold_minor: amount(item.unknown_hold_minor, `${path}.unknown_hold_minor`),
    exposure_minor: amount(item.exposure_minor, `${path}.exposure_minor`),
  };
}
function parseTenantCostPolicy(value: unknown): TenantCostPolicy {
  const item = object(value, "tenant_cost_policy");
  if (item.schema_version !== "tenant-cost-policy/v1" || item.currency !== "USD" || item.minor_unit_scale !== 6 || item.timezone !== "UTC") return invalid("identity_or_unit");
  const priceState = text(item.price_state, "price_state");
  if (priceState !== "known" && priceState !== "unconfigured") return invalid("price_state");
  const result: TenantCostPolicy = {
    schema_version: "tenant-cost-policy/v1",
    tenant_id: text(item.tenant_id, "tenant_id"),
    revision: text(item.revision, "revision"),
    configured: bool(item.configured, "configured"),
    currency: "USD", minor_unit_scale: 6, timezone: "UTC", price_state: priceState,
    day_limit_minor: nullableAmount(item.day_limit_minor, "day_limit_minor"),
    month_limit_minor: nullableAmount(item.month_limit_minor, "month_limit_minor"),
    day: windowValue(item.day, "day", /^\d{4}-\d{2}-\d{2}$/),
    month: windowValue(item.month, "month", /^\d{4}-\d{2}$/),
  };
  if (!result.tenant_id || !/^(0|[1-9][0-9]*)$/.test(result.revision)) return invalid("identity_or_revision");
  return result;
}

const TENANT_COST_POLICY_PATH = "/api/admin/tenant-cost-policy";
export function getTenantCostPolicy(signal?: AbortSignal): Promise<TenantCostPolicy> {
  return requestJson(TENANT_COST_POLICY_PATH, { method: "GET", signal }, parseTenantCostPolicy);
}
export function updateTenantCostCaps(
  expectedRevision: string,
  dayLimitMinor: string | null,
  monthLimitMinor: string | null,
  commandContext: CommandContext,
): Promise<TenantCostPolicy> {
  return requestJson(TENANT_COST_POLICY_PATH, {
    ...jsonInit("PUT", { expected_revision: expectedRevision, day_limit_minor: dayLimitMinor, month_limit_minor: monthLimitMinor }),
    commandContext,
  }, parseTenantCostPolicy);
}
export function useTenantCostPolicy(tenantId: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ["tenant-cost-policy", tenantId ?? ""],
    queryFn: ({ signal }) => getTenantCostPolicy(signal),
    enabled: enabled && tenantId !== undefined,
    refetchInterval: 30_000,
    retry: false,
  });
}
