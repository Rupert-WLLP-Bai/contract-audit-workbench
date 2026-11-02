import { useQuery } from "@tanstack/react-query";
import { requestJson } from "./http";
import { enumValue, responseReader } from "./respond";

export type ProjectionState = "current" | "stale" | "rebuilding";
export type WatermarkKind = "pg_snapshot" | "unknown";
export interface OperationsCost {
  currency: string;
  minor_unit_scale: number;
  known: boolean;
  settled_minor: string | null;
  unknown_hold_minor: string | null;
}
export interface OperationsQuota {
  period_basis: "selected_utc_date_range";
  calls: string | null;
  reserved_tokens: string | null;
  unknown_usage_calls: string | null;
}
export interface OperationsPool {
  attribution: string;
  capacity: string | null;
  in_use: string | null;
}
export interface OperationsMetricSummary {
  cases_created: string | null;
  runs_completed: string | null;
  attempts_completed: string | null;
  money: OperationsCost | null;
  logical_quota: OperationsQuota | null;
  shared_pool: OperationsPool | null;
}
export interface TenantOperationsSummary {
  tenant_id: string;
  tenant_name: string;
  status: "active" | "suspended" | "closed";
  metrics: OperationsMetricSummary;
}
export interface JurisdictionOperationsSummary {
  jurisdiction_id: string;
  tenants: TenantOperationsSummary[];
  totals: OperationsMetricSummary;
}
export interface JurisdictionOperationsView {
  schema_version: "jurisdiction-operations/v1";
  observed_at: string;
  watermark_kind: WatermarkKind;
  watermark: string | null;
  projection_state: ProjectionState;
  source_snapshot_token: string | null;
  built_at: string | null;
  dirty_since: string | null;
  projection_age_seconds: number | null;
  from_date: string;
  to_date: string;
  jurisdictions: JurisdictionOperationsSummary[];
}

const { object, text, integer, bool, array, nullableText, invalid } = responseReader((path) => `辖区运营统计响应中的 ${path} 无法识别`);
const PROJECTION_STATES = ["current", "stale", "rebuilding"] as const;
const WATERMARK_KINDS = ["pg_snapshot", "unknown"] as const;
const TENANT_STATES = ["active", "suspended", "closed"] as const;
function date(value: string, path: string): string {
  return /^\d{4}-\d{2}-\d{2}$/.test(value) && Number.isFinite(Date.parse(`${value}T00:00:00Z`)) ? value : invalid(path);
}
function timestamp(value: string | null, path: string): string | null {
  if (value === null) return null;
  return Number.isFinite(Date.parse(value)) ? value : invalid(path);
}
function count(value: unknown, path: string): string | null {
  const result = nullableText(value, path);
  if (result === null) return null;
  return /^(0|[1-9][0-9]*)$/.test(result) ? result : invalid(path);
}
function parseMoney(value: unknown, path: string): OperationsCost {
  const item = object(value, path);
  const currency = text(item.currency, `${path}.currency`);
  const scale = integer(item.minor_unit_scale, `${path}.minor_unit_scale`, 0);
  if (currency !== "USD" || scale !== 6) return invalid(`${path}.unit`);
  return {
    currency, minor_unit_scale: scale,
    known: bool(item.known, `${path}.known`),
    settled_minor: count(item.settled_minor, `${path}.settled_minor`),
    unknown_hold_minor: count(item.unknown_hold_minor, `${path}.unknown_hold_minor`),
  };
}
function parseQuota(value: unknown, path: string): OperationsQuota {
  const item = object(value, path);
  const basis = text(item.period_basis, `${path}.period_basis`);
  if (basis !== "selected_utc_date_range") return invalid(`${path}.period_basis`);
  return {
    period_basis: basis,
    calls: count(item.calls, `${path}.calls`),
    reserved_tokens: count(item.reserved_tokens, `${path}.reserved_tokens`),
    unknown_usage_calls: count(item.unknown_usage_calls, `${path}.unknown_usage_calls`),
  };
}
function parsePool(value: unknown, path: string): OperationsPool {
  const item = object(value, path);
  return {
    attribution: text(item.attribution, `${path}.attribution`),
    capacity: count(item.capacity, `${path}.capacity`),
    in_use: count(item.in_use, `${path}.in_use`),
  };
}
function parseMetrics(value: unknown, path: string): OperationsMetricSummary {
  const item = object(value, path);
  return {
    cases_created: count(item.cases_created, `${path}.cases_created`),
    runs_completed: count(item.runs_completed, `${path}.runs_completed`),
    attempts_completed: count(item.attempts_completed, `${path}.attempts_completed`),
    money: item.money === null ? null : parseMoney(item.money, `${path}.money`),
    logical_quota: item.logical_quota === null ? null : parseQuota(item.logical_quota, `${path}.logical_quota`),
    shared_pool: item.shared_pool === null ? null : parsePool(item.shared_pool, `${path}.shared_pool`),
  };
}
function parseTenant(value: unknown, path: string): TenantOperationsSummary {
  const item = object(value, path);
  return {
    tenant_id: text(item.tenant_id, `${path}.tenant_id`),
    tenant_name: text(item.tenant_name, `${path}.tenant_name`),
    status: enumValue(item.status, TENANT_STATES) ?? invalid(`${path}.status`),
    metrics: parseMetrics(item.metrics, `${path}.metrics`),
  };
}
function parseJurisdiction(value: unknown, path: string): JurisdictionOperationsSummary {
  const item = object(value, path);
  return {
    jurisdiction_id: text(item.jurisdiction_id, `${path}.jurisdiction_id`),
    tenants: array(item.tenants, `${path}.tenants`, parseTenant),
    totals: parseMetrics(item.totals, `${path}.totals`),
  };
}
function parseJurisdictionOperations(value: unknown): JurisdictionOperationsView {
  const item = object(value, "jurisdiction_operations");
  if (item.schema_version !== "jurisdiction-operations/v1") return invalid("schema_version");
  const observedAt = timestamp(text(item.observed_at, "observed_at"), "observed_at");
  if (observedAt === null) return invalid("observed_at");
  const watermarkKind = enumValue(item.watermark_kind, WATERMARK_KINDS) ?? invalid("watermark_kind");
  const watermark = nullableText(item.watermark, "watermark");
  if (watermarkKind === "pg_snapshot" && watermark !== null) return invalid("watermark.pg_snapshot_has_no_event_sequence");
  const projectionState = enumValue(item.projection_state, PROJECTION_STATES) ?? invalid("projection_state");
  const fromDate = date(text(item.from_date, "from_date"), "from_date");
  const toDate = date(text(item.to_date, "to_date"), "to_date");
  if (fromDate > toDate || Date.parse(`${toDate}T00:00:00Z`) - Date.parse(`${fromDate}T00:00:00Z`) > 30 * 24 * 60 * 60 * 1000) return invalid("date_range");
  const age = item.projection_age_seconds === null ? null : integer(item.projection_age_seconds, "projection_age_seconds", 0);
  return {
    schema_version: "jurisdiction-operations/v1",
    observed_at: observedAt,
    watermark_kind: watermarkKind,
    watermark,
    projection_state: projectionState,
    source_snapshot_token: nullableText(item.source_snapshot_token, "source_snapshot_token"),
    built_at: timestamp(nullableText(item.built_at, "built_at"), "built_at"),
    dirty_since: timestamp(nullableText(item.dirty_since, "dirty_since"), "dirty_since"),
    projection_age_seconds: age,
    from_date: fromDate,
    to_date: toDate,
    jurisdictions: array(item.jurisdictions, "jurisdictions", parseJurisdiction),
  };
}
export function getJurisdictionOperations(fromDate: string, toDate: string, signal?: AbortSignal) {
  const query = new URLSearchParams({ from_date: fromDate, to_date: toDate });
  return requestJson(`/api/admin/jurisdiction/operations?${query.toString()}`, { method: "GET", signal }, parseJurisdictionOperations);
}
export function useJurisdictionOperations(fromDate: string, toDate: string, enabled: boolean) {
  return useQuery({
    queryKey: ["jurisdiction-operations", fromDate, toDate],
    queryFn: ({ signal }) => getJurisdictionOperations(fromDate, toDate, signal),
    enabled,
    retry: false,
  });
}
