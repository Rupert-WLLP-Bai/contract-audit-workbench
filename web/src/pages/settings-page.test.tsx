import { cleanup, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as api from "../api/endpoints";
import type {
  Actor,
  LLMEndpointView,
  SessionAudience,
  SettingsView,
} from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { SettingsPage } from "./settings-page";

const settings: SettingsView = {
  model: { status: "connected", label: "模型可用" },
  queue: { depth: 0, oldest_wait_ms: null },
  redaction_policy: "仅发送必要片段。",
  users: [],
  execution: null,
  operator_audit: null,
};

const endpoint: LLMEndpointView = {
  configured: true,
  name: "生产网关",
  protocol: "openai-compatible",
  base_url: "https://llm.example.com/v1",
  models: ["review-a", "ocr-b"],
  review_model: "review-a",
  ocr_model: "ocr-b",
  has_api_key: true,
  revision: 2,
  updated_at: "2026-10-30T08:00:00.000Z",
  updated_by: "admin-1",
};

function actor(role: string, audience: SessionAudience): Actor {
  return {
    id: "user-1",
    display_name: "测试用户",
    role,
    tenant_id: "tenant-1",
    org_id: "org-1",
    audience,
    principal_id: "principal-user-1",
    active_context: { membership_id: "membership-1", tenant_id: "tenant-1", tenant_name: "租户一", org_id: "org-1", org_name: "组织一", context_version: "1" },
    csrf_token: "test-csrf",
    context_switch_enabled: false,
  };
}

const clients: QueryClient[] = [];

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.restoreAllMocks();
});

function renderPage(
  audience: SessionAudience,
  role: string,
  view: LLMEndpointView = endpoint,
) {
  vi.spyOn(api, "getSettings").mockResolvedValue(settings);
  vi.spyOn(api, "getSession").mockResolvedValue(actor(role, audience));
  const getEndpoint = vi.spyOn(api, "getLLMEndpoint").mockResolvedValue(view);
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ error: { code: "model_usage_unavailable", message: "用量账本暂不可用", retryable: false } }), { status: 503, headers: { "Content-Type": "application/json" } }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <SettingsPage audience={audience} />
    </QueryClientProvider>,
  );
  return getEndpoint;
}

describe("settings LLM endpoint access", () => {
  it("keeps the endpoint configuration out of the client workbench", async () => {
    const getEndpoint = renderPage("client", "reviewer");
    expect(await screen.findByText("设置")).toBeInTheDocument();
    expect(screen.queryByText("LLM 端点")).not.toBeInTheDocument();
    expect(getEndpoint).not.toHaveBeenCalled();
  });

  it("keeps endpoint configuration available to admins when tenant usage is unavailable", async () => {
    const getEndpoint = renderPage("admin", "admin");
    expect(await screen.findByText("LLM 端点")).toBeInTheDocument();
    expect(getEndpoint).toHaveBeenCalledTimes(1);
    expect(await screen.findByText("https://llm.example.com/v1")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "审查模型" })).toBeInTheDocument();
    expect(
      screen.getByRole("combobox", { name: "图片转写模型" }),
    ).toBeInTheDocument();
  });
});
