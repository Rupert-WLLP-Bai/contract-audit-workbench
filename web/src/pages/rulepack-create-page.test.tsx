import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import type { RulePackDetail, RulePackManifestInput } from "../api/rulepacks";
import { setSessionIdentity } from "../api/http";
import { RulePackCreatePage } from "./rulepack-create-page";

const clients: QueryClient[] = [];
const session = { audience: "admin", actor: { id: "admin-test", display_name: "测试管理员", role: "admin", tenant_id: "tenant-test", org_id: "org-test" } };
const manifest: RulePackManifestInput = {
  schema_version: "rulepack/v1", id: "regional / core", version: "release/2", tenant_id: "tenant-test",
  effective_from: "2026-10-01T00:00:00.000Z", effective_until: "2027-01-01T00:00:00.000Z",
  rules: [
    {
      rule_id: "BANDWIDTH", version: "3", domain: "technical", capability_id: "dedicated_line.minimum_bandwidth", mandatory: true,
      business_scope: { business_line: "ENT", scenario: "dedicated_line", contract_type: "service" }, tenant_id: "tenant-test",
      comparator: "minimum_bandwidth", threshold: { value: "000100.000", unit: "Mbit/s" },
      required_fact: { dimension: "bandwidth", source_roles: ["procurement", "contract"], object_required: true, unit_family: "bandwidth", exclusions: null },
      authority_sources: [{ id: "policy-bandwidth", version: "7", kind: "policy" }],
    },
    {
      rule_id: "SLA", version: "3", domain: "technical", capability_id: "dedicated_line.monthly_availability", mandatory: true,
      business_scope: { business_line: "ENT", scenario: "dedicated_line", contract_type: "service" }, tenant_id: "tenant-test",
      comparator: "monthly_sla", threshold: { value: "99.9500", unit: "percent" },
      required_fact: { dimension: "availability", source_roles: ["procurement", "contract"], object_required: true, unit_family: "availability", exclusions: [] },
      authority_sources: [{ id: "policy-sla", version: "7", kind: "policy" }],
    },
    {
      rule_id: "ADVANCE", version: "3", domain: "payment", capability_id: "payment.advance_ratio.ceiling", mandatory: true,
      business_scope: { business_line: "ENT", scenario: "dedicated_line", contract_type: "service" }, tenant_id: "tenant-test",
      comparator: "advance_payment_ceiling", threshold: { value: "0.3000", unit: "ratio" },
      required_fact: { dimension: "advance_ratio", source_roles: ["procurement", "contract"], object_required: true, unit_family: "ratio", exclusions: null },
      authority_sources: [{ id: "policy-advance", version: "7", kind: "policy" }],
    },
  ],
};
const created: RulePackDetail = {
  pack: {
    id: manifest.id, version: manifest.version, tenant_id: "tenant-test", status: "draft", revision: 1,
    content_hash: "a".repeat(64), snapshot_sha256: "b".repeat(64), effective_from: manifest.effective_from, effective_until: manifest.effective_until,
    authority_sources: ["policy-bandwidth", "policy-sla", "policy-advance"], is_active: false, active_pointer_revision: 0,
  },
  manifest: { ...manifest, lifecycle: { state: "draft" } },
};

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

function Destination() {
  return <output aria-label="目标路径">{useLocation().pathname}</output>;
}

function renderCreate(createResponse: () => Response | Promise<Response> = () => json(created), sessionResponse: () => Response | Promise<Response> = () => json(session)) {
  const requests: { path: string; init?: RequestInit }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    requests.push({ path, init });
    if (path === "/api/session?audience=admin") return sessionResponse();
    if (path === "/api/admin/rulepacks" && init?.method === "POST") return createResponse();
    return json({ error: { code: "not_found", message: "Unexpected route", retryable: false } }, 404);
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/admin/rulepacks/new"]}>
        <Routes>
          <Route path="/admin/rulepacks/new" element={<RulePackCreatePage />} />
          <Route path="/admin/rulepacks/:packId/versions/:version" element={<Destination />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return requests;
}

function change(label: string, value: string, container = screen) {
  fireEvent.change(container.getByLabelText(label, { exact: true }), { target: { value } });
}

async function fillDraft(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByRole("heading", { name: "创建规则包草稿" });
  change("规则包 ID", "regional / core");
  change("版本", "release/2");
  const localStart = new Date(2026, 9, 1);
  const localEnd = new Date(2027, 0, 1);
  const dateInput = (date: Date) => new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
  change("生效时间（本地时区）", dateInput(localStart));
  change("失效时间（本地时区，结束时刻不含）", dateInput(localEnd));
  for (const [index, rule] of manifest.rules.entries()) {
    if (index > 0) await user.click(screen.getByRole("button", { name: "添加规则" }));
    const fields = within(screen.getByRole("group", { name: new RegExp(`^规则 ${index + 1}`) }));
    const values = [
      ["规则 ID", rule.rule_id], ["规则版本", rule.version], ["检查域", rule.domain], ["能力 ID", rule.capability_id],
      ["业务线", rule.business_scope.business_line], ["场景", rule.business_scope.scenario], ["合同类型", rule.business_scope.contract_type],
      ["比较器", rule.comparator], ["比较阈值", rule.threshold.value], ["阈值单位", rule.threshold.unit],
      ["所需事实维度", rule.required_fact.dimension], ["允许材料角色（每行一项）", "procurement\ncontract"], ["单位族", rule.required_fact.unit_family],
      ["来源 ID", rule.authority_sources[0]!.id], ["来源版本", "7"], ["来源类型", "policy"],
    ];
    for (const [label, value] of values) fireEvent.change(fields.getByLabelText(label!, { exact: true }), { target: { value } });
  }
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("RulePackCreatePage", () => {
  it("waits for trusted admin identity before exposing the draft", async () => {
    let resolve!: (response: Response) => void;
    const pending = new Promise<Response>((complete) => { resolve = complete; });
    renderCreate(undefined, () => pending);
    expect(screen.getByRole("heading", { name: "正在确认管理身份" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "创建草稿" })).not.toBeInTheDocument();
    resolve(json(session));
    expect(await screen.findByLabelText("可信会话租户")).toHaveValue("tenant-test");
    expect(screen.getByLabelText("规则租户")).toHaveAttribute("readonly");
  });

  it.each([
    [403, "forbidden", "返回管理设置", "/admin/settings"],
    [401, "unauthenticated", "重新登录", "/admin/login"],
    [503, "service_unavailable", "登录管理工作台", "/admin/login"],
  ] as const)("blocks creation when session lookup returns %i", async (status, code, link, href) => {
    const requests = renderCreate(undefined, () => json({ error: { code, message: "Session unavailable", retryable: false } }, status));
    expect(await screen.findByRole("link", { name: link })).toHaveAttribute("href", href);
    expect(screen.queryByRole("button", { name: "创建草稿" })).not.toBeInTheDocument();
    expect(requests.some(({ init }) => init?.method === "POST")).toBe(false);
  });

  it("stops an invalid draft locally and links the error to its input", async () => {
    const user = userEvent.setup();
    const requests = renderCreate();
    await user.click(await screen.findByRole("button", { name: "创建草稿" }));
    const summary = screen.getByRole("alert");
    expect(summary).toHaveFocus();
    await user.click(within(summary).getByRole("link", { name: /规则包 ID/ }));
    expect(screen.getByLabelText("规则包 ID")).toHaveFocus();
    expect(requests.some(({ init }) => init?.method === "POST")).toBe(false);
  });

  it("submits structured literal values with the trusted tenant and navigates to the encoded identity", async () => {
    let resolve!: (response: Response) => void;
    const pending = new Promise<Response>((complete) => { resolve = complete; });
    const requests = renderCreate(() => pending);
    const user = userEvent.setup();
    await fillDraft(user);
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "正在创建草稿" })).toBeDisabled());
    expect(screen.getByLabelText("生效时间（本地时区）")).toBeDisabled();
    for (const input of screen.getAllByLabelText("规则 ID")) expect(input).toBeDisabled();
    const request = requests.find(({ init }) => init?.method === "POST");
    const body = JSON.parse(String(request?.init?.body));
    expect(body.manifest).toEqual({
      ...manifest,
      effective_from: new Date(2026, 9, 1).toISOString(), effective_until: new Date(2027, 0, 1).toISOString(),
    });
    expect(body.manifest).not.toHaveProperty("lifecycle");
    resolve(json(created));
    expect(await screen.findByLabelText("目标路径")).toHaveTextContent("/admin/rulepacks/regional%20%2F%20core/versions/release%2F2");
  });

  it("retains the draft after an identity conflict and permits a corrected version", async () => {
    let attempts = 0;
    const requests = renderCreate(() => ++attempts === 1
      ? json({ error: { code: "conflict", message: "Version already exists", retryable: false } }, 409)
      : json(created));
    const user = userEvent.setup();
    await fillDraft(user);
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    const error = await screen.findByRole("alert");
    expect(error).toHaveTextContent("Version already exists");
    expect(error).toHaveFocus();
    expect(screen.getByLabelText("规则包 ID")).toHaveValue("regional / core");
    expect(screen.getAllByLabelText("比较阈值")[0]).toHaveValue("000100.000");
    change("版本", "release/3");
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    expect(await screen.findByLabelText("目标路径")).toHaveTextContent("/versions/release%2F3");
    const posts = requests.filter(({ init }) => init?.method === "POST");
    expect(posts).toHaveLength(2);
    expect(JSON.parse(String(posts[1]?.init?.body)).manifest.version).toBe("release/3");
  });

  it.each(["{invalid JSON", JSON.stringify({ pack: { status: "draft" }, manifest: null })])("keeps form values when a successful HTTP response is malformed: %s", async (response) => {
    renderCreate(() => new Response(response, { status: 200, headers: { "Content-Type": "application/json" } }));
    const user = userEvent.setup();
    await fillDraft(user);
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    const error = await screen.findByRole("alert");
    expect(error).toHaveFocus();
    expect(screen.queryByLabelText("目标路径")).not.toBeInTheDocument();
    expect(screen.getByLabelText("版本", { exact: true })).toHaveValue("release/2");
    expect(screen.getByRole("button", { name: "创建草稿" })).toBeEnabled();
  });
});
