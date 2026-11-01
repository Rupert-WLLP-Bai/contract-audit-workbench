import { useQuery } from "@tanstack/react-query";
import { requestJson } from "./http";
import { enumValue, responseReader } from "./respond";

export type BudgetCapacity = { max_in_flight: number; calls_per_minute: number; tokens_per_minute: number };
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
  login_rate_limit_scope: "process";
  budget_state: "ready" | "unavailable" | "unconfigured";
  budget: { revision: string; mode: string; tenant: BudgetCapacity; global: BudgetCapacity } | null;
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

const { object, text, integer, invalid } = responseReader((path) => `租户运营响应中的 ${path} 无法识别`);
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
export function parseTenantOperations(value: unknown): TenantOperations {
  const item = object(value, "operations");
  if (item.schema_version !== "tenant-operations/v1" || item.login_rate_limit_scope !== "process") return invalid("operations.schema");
  const directory = object(item.directory, "directory");
  const usage = object(item.usage, "usage");
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
    login_rate_limit_scope: "process", budget_state: budgetState, budget,
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
  if (!result.tenant_id || result.usage.window_seconds !== 60 || result.usage.unknown_usage_calls > result.usage.window_calls ||
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
