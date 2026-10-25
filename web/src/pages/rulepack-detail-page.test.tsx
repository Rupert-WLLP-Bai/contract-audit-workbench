import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router";
import { AdminApp } from "../admin-app";
import { setSessionIdentity } from "../api/http";

const clients: QueryClient[] = [];
const contentHash = "a".repeat(64);
const snapshotHash = "b".repeat(64);
const summary = {
  id: "telecom-core",
  version: "1",
  tenant_id: "tenant-test",
  status: "published",
  revision: 4,
  content_hash: contentHash,
  snapshot_sha256: snapshotHash,
  effective_from: "2026-10-01T00:00:00Z",
  effective_until: "2027-01-01T00:00:00Z",
  authority_sources: ["internal-policy"],
  is_active: false,
  active_pointer_revision: 7,
};
const detail = {
  pack: summary,
  manifest: {
    schema_version: "rulepack/v1",
    id: summary.id,
    version: summary.version,
    tenant_id: summary.tenant_id,
    effective_from: summary.effective_from,
    effective_until: summary.effective_until,
    rules: [{
      rule_id: "LINE-BW-001",
      version: "1",
      domain: "technical",
      capability_id: "dedicated_line.minimum_bandwidth",
      mandatory: true,
      business_scope: { business_line: "ENT", scenario: "telecom_dedicated_line", contract_type: "service" },
      tenant_id: summary.tenant_id,
      comparator: "minimum_bandwidth",
      threshold: { value: "100", unit: "Mbit/s" },
      required_fact: { dimension: "bandwidth", source_roles: ["procurement"], object_required: true, unit_family: "bandwidth", exclusions: [] },
      authority_sources: [{ id: "internal-policy", version: "3", kind: "policy" }],
    }],
    lifecycle: { state: "published" },
  },
};
const history = {
  items: [{
    sequence: 1,
    pack_id: summary.id,
    version: summary.version,
    action: "publish",
    actor_id: "admin-test",
    content_hash: contentHash,
    pointer_revision: 7,
    created_at: "2026-10-20T12:00:00Z",
  }],
};

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("RulePack inactive published version rollback", () => {
  it("offers and posts the routed version when it is the only disabled publication", async () => {
    const requests: { path: string; init?: RequestInit }[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      requests.push({ path, init });
      if (path === "/api/session?audience=admin") {
        return json({ audience: "admin", actor: { id: "admin-test", display_name: "测试管理员", role: "admin", tenant_id: "tenant-test", org_id: "org-test" } });
      }
      if (path === "/api/admin/rulepacks/telecom-core/versions/1") return json(detail);
      if (path === "/api/admin/rulepacks/telecom-core/history") return json(history);
      if (path === "/api/admin/rulepacks/telecom-core/rollback" && init?.method === "POST") {
        return json({ pack: { ...summary, is_active: true, active_pointer_revision: 8 } });
      }
      return json({ error: { code: "not_found", message: "未找到测试路由", retryable: false } }, 404);
    });
    vi.stubGlobal("fetch", fetchMock);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
    clients.push(client);
    const user = userEvent.setup();
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={["/admin/rulepacks/telecom-core/versions/1"]}>
          <AdminApp />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    const target = await screen.findByRole("option", { name: "telecom-core · v1" });
    expect(target).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("目标历史版本"), "1");
    await user.click(screen.getByRole("button", { name: "回滚到所选版本" }));

    await waitFor(() => expect(requests.some(({ path, init }) => path === "/api/admin/rulepacks/telecom-core/rollback" && init?.method === "POST")).toBe(true));
    const rollbackRequest = requests.find(({ path, init }) => path === "/api/admin/rulepacks/telecom-core/rollback" && init?.method === "POST");
    expect(JSON.parse(String(rollbackRequest?.init?.body))).toEqual({ version: "1", expected_pointer_revision: 7 });
    expect((rollbackRequest?.init?.headers as Record<string, string>)?.["Idempotency-Key"]).toBeTruthy();
  });
});
