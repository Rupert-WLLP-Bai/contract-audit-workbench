// File metadata (manifest mirror):
// Purpose: Fetches and parses tenant usage, capacity and budget counters from the server; tenant identity is taken from session scope, not a client argument.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { useQuery } from "@tanstack/react-query";
import { requestJson } from "./http";
import { enumValue, responseReader } from "./respond";

export type BudgetCapacity = { max_in_flight: number; calls_per_minute: number; tokens_per_minute: number };
export type TenantCostWindow = {
  period_key: string;
  limit_minor: string | null;
  reserved_minor: string | null;
  settled_minor: string | null;
  unknown_hold_minor: string | null;
  known: boolean;
};
export type TenantCost = {
  currency: "USD";
  minor_unit_scale: 6;
  timezone: "UTC";
  day: TenantCostWindow;
  month: TenantCostWindow;
};
export type TenantOperations = {
  tenant_id: string;
  directory: {
    mode: "local_demo" | "enterprise";
    state: "local_demo" | "ready" | "unavailable";
    revision: string;
    last_good_revision: string;
    checked_at: string;
    loaded_at: string;
    refresh_error_code: string;
    local_verification_interval_ms: number;
  };
  tenant_control: {
    status: "active" | "suspended" | "closed";
    live_epoch: string;
  };
  execution_queue: {
    policy_hash: string;
    policy_version: string;
    tenant_weight: number;
    tenant_in_flight_limit: number;
    tenant_pending_limit: number;
    tenant_live: boolean;
  };
  login_rate_limit_scope: "process";
  budget_state: "ready" | "unavailable" | "unconfigured";
  budget: { revision: string; mode: string; tenant: BudgetCapacity; global: BudgetCapacity } | null;
  cost: TenantCost;
  usage: {
    observed_at: string;
    window_start: string;
    window_seconds: number;
    active_calls: number;
    window_calls: number;
    charged_tokens: number;
    reserved_tokens: number;
    unknown_usage_calls: number;
  };
};

const { object, text, integer, bool, invalid } = responseReader((path) => `租户运营响应中的 ${path} 无法识别`);
function capacity(value: unknown, path: string): BudgetCapacity {
  const item = object(value, path);
  return {
    max_in_flight: integer(item.max_in_flight, `${path}.max_in_flight`, 1),
    calls_per_minute: integer(item.calls_per_minute, `${path}.calls_per_minute`, 1),
    tokens_per_minute: integer(item.tokens_per_minute, `${path}.tokens_per_minute`, 1),
  };
}
function revision(value: unknown, path: string, optional = false): string {
  const result = text(value, path);
  return (optional && result === "") || /^[0-9a-f]{64}$/.test(result) ? result : invalid(path);
}
function timestamp(value: unknown, path: string, optional = false): string {
  const result = text(value, path);
  return (optional && result === "") || (result !== "" && Number.isFinite(Date.parse(result))) ? result : invalid(path);
}
function amount(value: unknown, path: string, nullable = false): string | null {
  if (nullable && value === null) return null;
  const result = text(value, path);
  return /^(0|[1-9][0-9]*)$/.test(result) ? result : invalid(path);
}
function costWindow(value: unknown, path: string, periodPattern: RegExp): TenantCostWindow {
  const item = object(value, path);
  const periodKey = text(item.period_key, `${path}.period_key`);
  if (!periodPattern.test(periodKey)) return invalid(`${path}.period_key`);
  return {
    period_key: periodKey,
    limit_minor: amount(item.limit_minor, `${path}.limit_minor`, true),
    reserved_minor: amount(item.reserved_minor, `${path}.reserved_minor`, true),
    settled_minor: amount(item.settled_minor, `${path}.settled_minor`, true),
    unknown_hold_minor: amount(item.unknown_hold_minor, `${path}.unknown_hold_minor`, true),
    known: bool(item.known, `${path}.known`),
  };
}
export function parseTenantOperations(value: unknown): TenantOperations {
  const item = object(value, "operations");
  if (item.schema_version !== "tenant-operations/v3" || item.login_rate_limit_scope !== "process") return invalid("operations.schema");
  const directory = object(item.directory, "directory");
  const tenantControl = object(item.tenant_control, "tenant_control");
  const executionQueue = object(item.execution_queue, "execution_queue");
  const usage = object(item.usage, "usage");
  const cost = object(item.cost, "cost");
  if (cost.currency !== "USD" || cost.minor_unit_scale !== 6 || cost.timezone !== "UTC") return invalid("cost.unit");
  const budgetState = enumValue(item.budget_state, ["ready", "unavailable", "unconfigured"] as const) ?? invalid("budget_state");
  let budget: TenantOperations["budget"] = null;
  if (budgetState === "ready") {
    const source = object(item.budget, "budget");
    if (source.schema_version !== "model-budget/v1" || source.window_seconds !== 60) return invalid("budget.schema");
    budget = {
      revision: revision(source.revision, "budget.revision"), mode: text(source.mode, "budget.mode"),
      tenant: capacity(source.tenant, "budget.tenant"), global: capacity(source.global, "budget.global"),
    };
  } else if (item.budget !== null) return invalid("budget");
  const result: TenantOperations = {
    tenant_id: text(item.tenant_id, "tenant_id"),
    tenant_control: {
      status: enumValue(tenantControl.status, ["active", "suspended", "closed"] as const) ?? invalid("tenant_control.status"),
      live_epoch: text(tenantControl.live_epoch, "tenant_control.live_epoch"),
    },
    execution_queue: {
      policy_hash: text(executionQueue.policy_hash, "execution_queue.policy_hash"),
      policy_version: text(executionQueue.policy_version, "execution_queue.policy_version"),
      tenant_weight: integer(executionQueue.tenant_weight, "execution_queue.tenant_weight", 1),
      tenant_in_flight_limit: integer(executionQueue.tenant_in_flight_limit, "execution_queue.tenant_in_flight_limit", 1),
      tenant_pending_limit: integer(executionQueue.tenant_pending_limit, "execution_queue.tenant_pending_limit", 1),
      tenant_live: bool(executionQueue.tenant_live, "execution_queue.tenant_live"),
    },
    login_rate_limit_scope: "process", budget_state: budgetState, budget,
    cost: {
      currency: "USD", minor_unit_scale: 6, timezone: "UTC",
      day: costWindow(cost.day, "cost.day", /^\d{4}-\d{2}-\d{2}$/),
      month: costWindow(cost.month, "cost.month", /^\d{4}-\d{2}$/),
    },
    directory: {
      mode: enumValue(directory.mode, ["local_demo", "enterprise"] as const) ?? invalid("directory.mode"),
      state: enumValue(directory.state, ["local_demo", "ready", "unavailable"] as const) ?? invalid("directory.state"),
      revision: revision(directory.revision, "directory.revision", true),
      last_good_revision: revision(directory.last_good_revision, "directory.last_good_revision", true),
      checked_at: timestamp(directory.checked_at, "directory.checked_at", true), loaded_at: timestamp(directory.loaded_at, "directory.loaded_at", true),
      refresh_error_code: text(directory.refresh_error_code, "directory.refresh_error_code"),
      local_verification_interval_ms: integer(directory.local_verification_interval_ms, "directory.local_verification_interval_ms", 1),
    },
    usage: {
      observed_at: timestamp(usage.observed_at, "usage.observed_at"), window_start: timestamp(usage.window_start, "usage.window_start"),
      window_seconds: integer(usage.window_seconds, "usage.window_seconds", 1),
      active_calls: integer(usage.active_calls, "usage.active_calls", 0), window_calls: integer(usage.window_calls, "usage.window_calls", 0),
      charged_tokens: integer(usage.charged_tokens, "usage.charged_tokens", 0), reserved_tokens: integer(usage.reserved_tokens, "usage.reserved_tokens", 0),
      unknown_usage_calls: integer(usage.unknown_usage_calls, "usage.unknown_usage_calls", 0),
    },
  };
  const epochIsCanonical = /^(0|[1-9][0-9]*)$/.test(result.tenant_control.live_epoch);
  const epochZeroAllowed = result.tenant_control.live_epoch !== "0" || result.directory.mode === "local_demo";
  if (!result.tenant_id || !epochIsCanonical || !epochZeroAllowed ||
      !/^[0-9a-f]{64}$/.test(result.execution_queue.policy_hash) ||
      result.execution_queue.policy_version.length > 80 ||
      result.execution_queue.tenant_weight > 100 ||
      result.execution_queue.tenant_in_flight_limit > 1024 ||
      result.execution_queue.tenant_pending_limit < result.execution_queue.tenant_in_flight_limit ||
      result.execution_queue.tenant_pending_limit > 100_000 ||
      result.execution_queue.tenant_live !== (result.tenant_control.status === "active") ||
      result.usage.window_seconds !== 60 || result.usage.unknown_usage_calls > result.usage.window_calls ||
      (result.directory.state === "ready" && (result.directory.mode !== "enterprise" || !result.directory.revision)) ||
      (result.directory.state !== "ready" && result.directory.revision !== "")) return invalid("operations.identity_or_window");
  return result;
}

export function useTenantOperations() {
  return useQuery({
    queryKey: ["tenant-operations", "admin"],
    queryFn: ({ signal }) => requestJson("/api/admin/tenant-operations", { method: "GET", signal }, parseTenantOperations),
    refetchInterval: 30_000,
    retry: false,
  });
}
