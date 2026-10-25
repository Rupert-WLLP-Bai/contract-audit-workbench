import { useQuery } from "@tanstack/react-query";
import { ApiError, requestJson } from "./http";
import { isRecord } from "./guards";
import type { SessionAudience } from "./types";

export type HealthStatus =
  | "ready"
  | "unavailable"
  | "unknown"
  | "unconfigured"
  | "stopped"
  | "starting"
  | "completed";
export type HealthService = {
  id: string;
  name: string;
  category: string;
  kind: string;
  status: HealthStatus;
  detail: string;
  target: string;
  depends_on: string[];
  container: {
    id: string;
    name: string;
    image: string;
    state: string;
    health: string;
    started_at: string;
    restarts: number;
  } | null;
  probe: { kind: string; status: string; duration_ms: number } | null;
};
export type ProductionComponent = {
  id: string;
  name: string;
  category: string;
  target: string;
  observed: string;
  state: "observed" | "not_connected";
  verification: "PASS" | "FAIL" | "BLOCKED" | "NOT_RUN" | "NOT_APPLICABLE";
  service_ids: string[];
  reference: string;
};
export type HealthCluster = {
  status: "ready" | "unavailable";
  namespace: string;
  observed_at: string;
  detail: string;
  nodes: {
    name: string;
    status: "ready" | "unavailable" | "unknown";
    version: string;
  }[];
};
export type SystemHealth = {
  schema_version: "system-health/v1";
  observed_at: string;
  mode: string;
  source: string;
  services: HealthService[];
  architecture: ProductionComponent[];
  cluster: HealthCluster | null;
};
function invalid(): never {
  throw new ApiError(
    "系统健康响应格式无效",
    500,
    "invalid_response",
    false,
    null,
  );
}
function text(value: unknown): string {
  if (typeof value !== "string") return invalid();
  return value;
}
function counter(value: unknown): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
    return invalid();
  return value;
}
function parseService(value: unknown): HealthService {
  if (!isRecord(value) || !Array.isArray(value.depends_on)) return invalid();
  const status = text(value.status);
  let container: HealthService["container"] = null;
  if (value.container !== null) {
    if (!isRecord(value.container)) return invalid();
    const c = value.container;
    container = {
      id: text(c.id),
      name: text(c.name),
      image: text(c.image),
      state: text(c.state),
      health: text(c.health),
      started_at: text(c.started_at),
      restarts: counter(c.restarts),
    };
  }
  let probe: HealthService["probe"] = null;
  if (value.probe !== null) {
    if (!isRecord(value.probe)) return invalid();
    probe = {
      kind: text(value.probe.kind),
      status: text(value.probe.status),
      duration_ms: counter(value.probe.duration_ms),
    };
  }
  if (
    status !== "ready" &&
    status !== "unavailable" &&
    status !== "unknown" &&
    status !== "unconfigured" &&
    status !== "stopped" &&
    status !== "starting" &&
    status !== "completed"
  )
    return invalid();
  return {
    id: text(value.id),
    name: text(value.name),
    category: text(value.category),
    kind: text(value.kind),
    status,
    detail: text(value.detail),
    target: text(value.target),
    depends_on: value.depends_on.map(text),
    container,
    probe,
  };
}
export function parseSystemHealth(value: unknown): SystemHealth {
  if (
    !isRecord(value) ||
    value.schema_version !== "system-health/v1" ||
    !Array.isArray(value.services) ||
    !Array.isArray(value.architecture)
  )
    return invalid();
  let cluster: HealthCluster | null = null;
  if (value.cluster !== null) {
    if (!isRecord(value.cluster) || !Array.isArray(value.cluster.nodes))
      return invalid();
    const status = text(value.cluster.status),
      observed_at = text(value.cluster.observed_at);
    if (
      (status !== "ready" && status !== "unavailable") ||
      !Number.isFinite(Date.parse(observed_at))
    )
      return invalid();
    const nodes = value.cluster.nodes.map(
      (node): HealthCluster["nodes"][number] => {
        if (!isRecord(node)) return invalid();
        const status = text(node.status);
        if (
          status !== "ready" &&
          status !== "unavailable" &&
          status !== "unknown"
        )
          return invalid();
        return { name: text(node.name), status, version: text(node.version) };
      },
    );
    if (new Set(nodes.map((node) => node.name)).size !== nodes.length)
      return invalid();
    cluster = {
      status,
      namespace: text(value.cluster.namespace),
      observed_at,
      detail: text(value.cluster.detail),
      nodes,
    };
  }
  const observed = text(value.observed_at);
  if (!Number.isFinite(Date.parse(observed))) return invalid();
  const services = value.services.map(parseService);
  if (new Set(services.map((item) => item.id)).size !== services.length)
    return invalid();
  const architecture = value.architecture.map(
    (component): ProductionComponent => {
      if (!isRecord(component) || !Array.isArray(component.service_ids))
        return invalid();
      const state = text(component.state),
        verification = text(component.verification);
      if (state !== "observed" && state !== "not_connected") return invalid();
      if (
        verification !== "PASS" &&
        verification !== "FAIL" &&
        verification !== "BLOCKED" &&
        verification !== "NOT_RUN" &&
        verification !== "NOT_APPLICABLE"
      )
        return invalid();
      const reference = text(component.reference);
      if (!reference.startsWith("https://")) return invalid();
      return {
        id: text(component.id),
        name: text(component.name),
        category: text(component.category),
        target: text(component.target),
        observed: text(component.observed),
        state,
        verification,
        service_ids: component.service_ids.map(text),
        reference,
      };
    },
  );
  if (new Set(architecture.map((item) => item.id)).size !== architecture.length)
    return invalid();
  return {
    schema_version: value.schema_version,
    observed_at: observed,
    mode: text(value.mode),
    source: text(value.source),
    services,
    architecture,
    cluster,
  };
}
export function useSystemHealth(audience: SessionAudience = "client") {
  return useQuery({
    queryKey: ["system-health", audience],
    queryFn: ({ signal }) =>
      requestJson(
        audience === "admin"
          ? "/api/admin/system-health"
          : "/api/system-health",
        { method: "GET", signal },
        parseSystemHealth,
      ),
    refetchInterval: 30_000,
    retry: false,
  });
}
