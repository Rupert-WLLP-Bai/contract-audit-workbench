import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router";
import { AdminApp } from "../admin-app";
import { setSessionIdentity } from "../api/http";
import type {
  RulePackDetail,
  RulePackHistoryEntry,
  RulePackState,
  RulePackSummary,
} from "../api/rulepacks";
import { RulePackDetailPage } from "./rulepack-detail-page";
import { renderWithAntd as render } from "../test/antd";

const clients: QueryClient[] = [];
const contentHash = "a".repeat(64);
const snapshotHash = "b".repeat(64);
const summary: RulePackSummary = {
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
const detail: RulePackDetail = {
  pack: summary,
  manifest: {
    schema_version: "rulepack/v1",
    id: summary.id,
    version: summary.version,
    tenant_id: summary.tenant_id,
    effective_from: summary.effective_from,
    effective_until: summary.effective_until,
    rules: [
      {
        rule_id: "LINE-BW-001",
        version: "1",
        domain: "technical",
        capability_id: "dedicated_line.minimum_bandwidth",
        mandatory: true,
        business_scope: {
          business_line: "ENT",
          scenario: "telecom_dedicated_line",
          contract_type: "service",
        },
        tenant_id: summary.tenant_id,
        comparator: "minimum_bandwidth",
        threshold: { value: "100", unit: "Mbit/s" },
        required_fact: {
          dimension: "bandwidth",
          source_roles: ["procurement"],
          object_required: true,
          unit_family: "bandwidth",
          exclusions: [],
        },
        authority_sources: [
          { id: "internal-policy", version: "3", kind: "policy" },
        ],
      },
    ],
    lifecycle: { state: "published" },
  },
};
const history = {
  items: [
    {
      sequence: 1,
      pack_id: summary.id,
      version: summary.version,
      action: "publish",
      actor_id: "admin-test",
      content_hash: contentHash,
      pointer_revision: 7,
      created_at: "2026-10-20T12:00:00Z",
      previous_version: null,
      previous_active: null,
      resulting_version: "1",
      resulting_active: true,
    },
  ],
};

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
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
    const fetchMock = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        requests.push({ path, init });
        if (path === "/api/session?audience=admin") {
          return json({
            audience: "admin",
            actor: {
              id: "admin-test",
              display_name: "测试管理员",
              role: "admin",
              tenant_id: "tenant-test",
              org_id: "org-test",
            },
          });
        }
        if (path === "/api/admin/rulepacks/telecom-core/versions/1")
          return json(detail);
        if (path === "/api/admin/rulepacks/telecom-core/history")
          return json(history);
        if (
          path === "/api/admin/rulepacks/telecom-core/rollback" &&
          init?.method === "POST"
        ) {
          return json({
            pack: { ...summary, is_active: true, active_pointer_revision: 8 },
          });
        }
        return json(
          {
            error: {
              code: "not_found",
              message: "未找到测试路由",
              retryable: false,
            },
          },
          404,
        );
      },
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: false, gcTime: 0 },
        mutations: { retry: false },
      },
    });
    clients.push(client);
    const user = userEvent.setup();
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter
          initialEntries={["/admin/rulepacks/telecom-core/versions/1"]}
        >
          <AdminApp />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(await screen.findByText(/无前值 → v1 生效/)).toBeInTheDocument();
    await chooseHistoryVersion(user, "telecom-core， v1");
    await user.click(screen.getByRole("button", { name: "回滚到所选版本" }));

    await waitFor(() =>
      expect(
        requests.some(
          ({ path, init }) =>
            path === "/api/admin/rulepacks/telecom-core/rollback" &&
            init?.method === "POST",
        ),
      ).toBe(true),
    );
    const rollbackRequest = requests.find(
      ({ path, init }) =>
        path === "/api/admin/rulepacks/telecom-core/rollback" &&
        init?.method === "POST",
    );
    expect(JSON.parse(String(rollbackRequest?.init?.body))).toEqual({
      version: "1",
      expected_pointer_revision: 7,
    });
    expect(
      (rollbackRequest?.init?.headers as Record<string, string>)?.[
        "Idempotency-Key"
      ],
    ).toBeTruthy();
  });
});

function versionDetail(state: RulePackState, revision = 4): RulePackDetail {
  const value = structuredClone(detail);
  value.pack.status = state;
  value.pack.revision = revision;
  value.pack.is_active = state === "published";
  value.manifest.lifecycle = {
    state,
    ...(state === "draft"
      ? {}
      : {
          verification: {
            manifest_hash: contentHash,
            verifier_version: "verifier-3",
            passed: true,
          },
        }),
    ...(state === "approved" || state === "published"
      ? {
          approval: {
            manifest_hash: contentHash,
            verification_hash: "e".repeat(64),
            actor_id: "approver-test",
          },
        }
      : {}),
    ...(state === "published"
      ? {
          release: {
            manifest_hash: contentHash,
            approval_hash: "f".repeat(64),
            release_hash: "c".repeat(64),
          },
        }
      : {}),
  };
  const baseRule = value.manifest.rules[0]!;
  value.manifest.rules.push(
    {
      ...structuredClone(baseRule),
      rule_id: "LINE-SLA-001",
      capability_id: "dedicated_line.monthly_availability",
      comparator: "monthly_sla",
      threshold: { value: "99.9", unit: "percent" },
      required_fact: {
        ...baseRule.required_fact,
        dimension: "availability",
        unit_family: "ratio",
        exclusions: [],
      },
    },
    {
      ...structuredClone(baseRule),
      rule_id: "ADVANCE-001",
      domain: "payment",
      capability_id: "payment.advance_ratio.ceiling",
      comparator: "advance_payment_ceiling",
      threshold: { value: "0.3", unit: "ratio" },
      required_fact: {
        ...baseRule.required_fact,
        dimension: "advance_ratio",
        unit_family: "ratio",
        exclusions: null,
      },
    },
  );
  return value;
}

function renderDetail(
  initial: RulePackDetail,
  mutation: (
    path: string,
    init: RequestInit,
    setDetail: (next: RulePackDetail) => void,
  ) => Response | Promise<Response>,
  entries: RulePackHistoryEntry[] = [],
) {
  let current = initial;
  const requests: { path: string; init?: RequestInit }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      requests.push({ path, init });
      if (path === "/api/session?audience=admin")
        return json({
          audience: "admin",
          actor: {
            id: "admin-test",
            display_name: "测试管理员",
            role: "admin",
            tenant_id: "tenant-test",
            org_id: "org-test",
          },
        });
      if (path === "/api/admin/rulepacks/telecom-core/history")
        return json({ items: entries });
      if (init?.method === "POST" || init?.method === "PUT")
        return mutation(path, init, (next) => {
          current = next;
        });
      if (path === "/api/admin/rulepacks/telecom-core/versions/1")
        return json(current);
      return json(
        {
          error: {
            code: "not_found",
            message: "Unexpected route",
            retryable: false,
          },
        },
        404,
      );
    }),
  );
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter
        initialEntries={["/admin/rulepacks/telecom-core/versions/1"]}
      >
        <Routes>
          <Route
            path="/admin/rulepacks/:packId/versions/:version"
            element={<RulePackDetailPage />}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return requests;
}

function mutations(requests: { path: string; init?: RequestInit }[]) {
  return requests.filter(
    ({ init }) => init?.method === "POST" || init?.method === "PUT",
  );
}

function requestBody(request: { init?: RequestInit } | undefined) {
  return JSON.parse(String(request?.init?.body));
}

function idempotencyKey(
  request: { init?: RequestInit } | undefined,
): string | null {
  return new Headers(request?.init?.headers).get("Idempotency-Key");
}

async function clickAction(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
) {
  const button = await screen.findByRole("button", { name });
  await waitFor(() => expect(button).toBeEnabled());
  await user.click(button);
}

function firstThreshold(value: string) {
  fireEvent.change(screen.getAllByLabelText("比较阈值")[0]!, {
    target: { value },
  });
}

async function chooseHistoryVersion(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
) {
  await user.click(screen.getByRole("combobox", { name: "目标历史版本" }));
  const normalized = name.replace(/\s*，\s*/, "\\s*，\\s*");
  const options = await screen.findAllByRole("option", {
    name: new RegExp(normalized),
  });
  await user.click(options.at(-1)!);
}

describe("RulePackDetailPage draft concurrency", () => {
  it("validates edits locally and saves literal content with the current row CAS", async () => {
    const initial = versionDetail("draft");
    const saved = versionDetail("draft", 5);
    saved.manifest.rules[0]!.threshold.value = "00125.00";
    const requests = renderDetail(initial, (_path, _init, setDetail) => {
      setDetail(saved);
      return json(saved);
    });
    const user = userEvent.setup();
    expect(
      await screen.findByRole("button", { name: "保存草稿" }),
    ).toBeDisabled();
    expect(screen.getByLabelText("规则包 ID")).toHaveAttribute("readonly");
    expect(screen.getByLabelText("版本", { exact: true })).toHaveAttribute(
      "readonly",
    );
    firstThreshold(" ");
    expect(screen.getByRole("button", { name: "验证草稿" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "保存草稿" }));
    expect(screen.getByRole("alert")).toHaveFocus();
    expect(mutations(requests)).toHaveLength(0);
    firstThreshold("00125.00");
    await user.click(screen.getByRole("button", { name: "保存草稿" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "保存草稿" })).toBeDisabled(),
    );
    expect(screen.getByRole("button", { name: "验证草稿" })).toBeEnabled();
    expect(mutations(requests)).toHaveLength(1);
    const request = mutations(requests)[0]!;
    expect(request.path).toBe("/api/admin/rulepacks/telecom-core/versions/1");
    expect(request.init?.method).toBe("PUT");
    const body = requestBody(request);
    expect(body.expected_revision).toBe(4);
    expect(body.manifest).not.toHaveProperty("lifecycle");
    expect(body.manifest.rules[0].threshold).toEqual({
      value: "00125.00",
      unit: "Mbit/s",
    });
    expect(
      body.manifest.rules.map((rule: { tenant_id: string }) => rule.tenant_id),
    ).toEqual(["tenant-test", "tenant-test", "tenant-test"]);
    expect(screen.getAllByLabelText("比较阈值")[0]).toHaveValue("00125.00");
  });

  it("preserves a conflicting local draft until the user explicitly adopts the new CAS revision", async () => {
    const initial = versionDetail("draft");
    const newer = versionDetail("draft", 6);
    newer.manifest.rules[0]!.threshold.value = "200";
    const saved = versionDetail("draft", 7);
    saved.manifest.rules[0]!.threshold.value = "175.00";
    let attempts = 0;
    const requests = renderDetail(initial, (_path, _init, setDetail) => {
      if (++attempts === 1) {
        setDetail(newer);
        return json(
          {
            error: {
              code: "conflict",
              message: "Row revision changed",
              retryable: false,
            },
          },
          409,
        );
      }
      setDetail(saved);
      return json(saved);
    });
    const user = userEvent.setup();
    await screen.findByRole("button", { name: "保存草稿" });
    firstThreshold("175.00");
    await user.click(screen.getByRole("button", { name: "保存草稿" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Row revision changed",
    );
    const adopt = await screen.findByRole("button", {
      name: "下一次保存使用服务器修订 6",
    });
    expect(screen.getAllByLabelText("比较阈值")[0]).toHaveValue("175.00");
    expect(screen.getByRole("button", { name: "保存草稿" })).toBeDisabled();
    await user.click(
      screen.getByRole("button", { name: "重读服务器版本（保留本地修改）" }),
    );
    expect(screen.getAllByLabelText("比较阈值")[0]).toHaveValue("175.00");
    await user.click(adopt);
    expect(screen.getByRole("button", { name: "保存草稿" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "保存草稿" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "保存草稿" })).toBeDisabled(),
    );
    expect(
      mutations(requests)
        .map(requestBody)
        .map((body) => body.expected_revision),
    ).toEqual([4, 6]);
    expect(
      requestBody(mutations(requests)[1]).manifest.rules[0].threshold.value,
    ).toBe("175.00");
    expect(
      screen.queryByRole("button", { name: /下一次保存使用服务器修订/ }),
    ).not.toBeInTheDocument();
  });
});

describe("RulePackDetailPage lifecycle transitions", () => {
  it("validates, approves, publishes and disables using each newly returned revision and content identity", async () => {
    const validated = versionDetail("validated", 5);
    const approved = versionDetail("approved", 6);
    const published = versionDetail("published", 7);
    published.pack.active_pointer_revision = 8;
    const disabled = structuredClone(published);
    disabled.pack.revision = 8;
    disabled.pack.active_pointer_revision = 9;
    disabled.pack.is_active = false;
    const requests = renderDetail(
      versionDetail("draft"),
      (path, _init, setDetail) => {
        if (path.endsWith("/validate")) {
          setDetail(validated);
          return json(validated);
        }
        if (path.endsWith("/approve")) {
          setDetail(approved);
          return json(approved);
        }
        if (path.endsWith("/publish")) {
          setDetail(published);
          return json({ pack: published.pack });
        }
        if (path.endsWith("/disable")) {
          setDetail(disabled);
          return json({ pack: disabled.pack });
        }
        return json(
          {
            error: {
              code: "unexpected_action",
              message: "Unexpected action",
              retryable: false,
            },
          },
          400,
        );
      },
    );
    const user = userEvent.setup();
    await clickAction(user, "验证草稿");
    await clickAction(user, "批准此内容摘要");
    await clickAction(user, "发布此版本");
    await clickAction(user, "停用活动指针");
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "停用活动指针" }),
      ).not.toBeInTheDocument(),
    );
    const actions = mutations(requests);
    expect(actions.map(({ path }) => path.split("/").at(-1))).toEqual([
      "validate",
      "approve",
      "publish",
      "disable",
    ]);
    expect(actions.map(requestBody)).toEqual([
      { expected_revision: 4 },
      { expected_revision: 5, expected_content_hash: contentHash },
      { expected_revision: 6, expected_pointer_revision: 7 },
      { expected_revision: 7, expected_pointer_revision: 8 },
    ]);
    expect(idempotencyKey(actions[2])).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "保存草稿" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "规则内容（只读）" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "版本与来源身份" }));
    expect(screen.getByText(/verifier-3/)).toBeInTheDocument();
    expect(screen.getByText(/approver-test/)).toBeInTheDocument();
    expect(screen.getByText(/发布摘要/)).toHaveTextContent("c".repeat(64));
  });

  it("replays a retryable publish with exactly the same CAS body and idempotency key", async () => {
    const approved = versionDetail("approved", 6);
    const published = versionDetail("published", 7);
    published.pack.active_pointer_revision = 8;
    let attempts = 0;
    const requests = renderDetail(approved, (_path, _init, setDetail) => {
      if (++attempts === 1)
        return json(
          {
            error: {
              code: "service_unavailable",
              message: "Publish outcome unknown",
              retryable: true,
            },
          },
          503,
        );
      setDetail(published);
      return json(published);
    });
    const user = userEvent.setup();
    await clickAction(user, "发布此版本");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Publish outcome unknown",
    );
    await user.click(
      screen.getByRole("button", { name: "使用同一幂等键重试原请求" }),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "停用活动指针" }),
      ).toBeEnabled(),
    );
    const actions = mutations(requests);
    expect(actions).toHaveLength(2);
    expect(requestBody(actions[0])).toEqual({
      expected_revision: 6,
      expected_pointer_revision: 7,
    });
    expect(requestBody(actions[1])).toEqual({
      expected_revision: 6,
      expected_pointer_revision: 7,
    });
    expect(idempotencyKey(actions[0])).toBeTruthy();
    expect(idempotencyKey(actions[1])).toBe(idempotencyKey(actions[0]));
    expect(
      screen.queryByRole("button", { name: "使用同一幂等键重试原请求" }),
    ).not.toBeInTheDocument();
  });

  it("refreshes a pointer conflict and uses a new operation identity for the revised publish", async () => {
    const refreshed = versionDetail("approved", 6);
    refreshed.pack.active_pointer_revision = 9;
    const published = versionDetail("published", 7);
    published.pack.active_pointer_revision = 10;
    let attempts = 0;
    const requests = renderDetail(
      versionDetail("approved", 6),
      (_path, _init, setDetail) => {
        if (++attempts === 1) {
          setDetail(refreshed);
          return json(
            {
              error: {
                code: "conflict",
                message: "Pointer revision changed",
                retryable: false,
              },
            },
            409,
          );
        }
        setDetail(published);
        return json(published);
      },
    );
    const user = userEvent.setup();
    await clickAction(user, "发布此版本");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Pointer revision changed",
    );
    await screen.findByText(/指针修订 9/);
    expect(
      screen.queryByRole("button", { name: "使用同一幂等键重试原请求" }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "发布此版本" }));
    await screen.findByRole("button", { name: "停用活动指针" });
    const actions = mutations(requests);
    expect(actions.map(requestBody)).toEqual([
      { expected_revision: 6, expected_pointer_revision: 7 },
      { expected_revision: 6, expected_pointer_revision: 9 },
    ]);
    expect(idempotencyKey(actions[1])).not.toBe(idempotencyKey(actions[0]));
  });

  it("removes disable after a concurrent administrator has already disabled the pointer", async () => {
    const inactive = versionDetail("published", 5);
    inactive.pack.is_active = false;
    inactive.pack.active_pointer_revision = 8;
    const requests = renderDetail(
      versionDetail("published"),
      (_path, _init, setDetail) => {
        setDetail(inactive);
        return json(
          {
            error: {
              code: "conflict",
              message: "Already disabled",
              retryable: false,
            },
          },
          409,
        );
      },
    );
    await userEvent
      .setup()
      .click(await screen.findByRole("button", { name: "停用活动指针" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Already disabled",
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "停用活动指针" }),
      ).not.toBeInTheDocument(),
    );
    expect(requestBody(mutations(requests)[0])).toEqual({
      expected_revision: 4,
      expected_pointer_revision: 7,
    });
    expect(mutations(requests)).toHaveLength(1);
  });

  it("blocks lifecycle operations for a mismatched trusted tenant", async () => {
    const anotherTenant = versionDetail("approved");
    anotherTenant.pack.tenant_id = "tenant-other";
    anotherTenant.manifest.tenant_id = "tenant-other";
    const requests = renderDetail(anotherTenant, () => json({}));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /可信租户与规则包不一致/,
    );
    expect(screen.getByRole("button", { name: "发布此版本" })).toBeDisabled();
    expect(screen.getByLabelText("目标历史版本")).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "回滚到所选版本" }),
    ).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "保存草稿" }),
    ).not.toBeInTheDocument();
    expect(mutations(requests)).toHaveLength(0);
  });
});

describe("RulePackDetailPage history and rollback", () => {
  it("offers only published versions and preserves the exact failed rollback when selection changes", async () => {
    const base: RulePackHistoryEntry = {
      sequence: 1,
      pack_id: "telecom-core",
      version: "1",
      action: "publish",
      actor_id: "admin-test",
      content_hash: contentHash,
      pointer_revision: 7,
      created_at: "2026-10-20T12:00:00Z",
      previous_version: null,
      previous_active: null,
      resulting_version: "1",
      resulting_active: true,
    };
    const entries: RulePackHistoryEntry[] = [
      base,
      {
        ...base,
        sequence: 2,
        version: "2",
        previous_version: "1",
        previous_active: true,
        resulting_version: "2",
      },
      {
        ...base,
        sequence: 3,
        version: "2",
        action: "disable",
        previous_version: "2",
        previous_active: true,
        resulting_version: "2",
        resulting_active: false,
      },
      {
        ...base,
        sequence: 4,
        version: "9",
        action: "test",
        resulting_version: null,
        resulting_active: null,
      },
      { ...base, sequence: 5, version: "2" },
    ];
    const inactive = versionDetail("published");
    inactive.pack.is_active = false;
    const afterRollback = structuredClone(inactive);
    afterRollback.pack.active_pointer_revision = 8;
    let attempts = 0;
    const requests = renderDetail(
      inactive,
      (_path, _init, setDetail) => {
        if (++attempts === 1)
          return json(
            {
              error: {
                code: "service_unavailable",
                message: "Rollback outcome unknown",
                retryable: true,
              },
            },
            503,
          );
        setDetail(afterRollback);
        return json({ pack: afterRollback.pack });
      },
      entries,
    );
    const user = userEvent.setup();
    const target = await screen.findByLabelText("目标历史版本");
    expect(
      screen.getByRole("button", { name: "回滚到所选版本" }),
    ).toBeDisabled();
    expect(screen.getByText(/v2 生效 → v2 未生效/)).toBeInTheDocument();
    await user.click(target);
    expect(await screen.findAllByRole("option")).toHaveLength(3);
    expect(
      screen.getAllByRole("option", { name: /telecom-core\s*，\s*v2/ }).at(-1),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: "telecom-core， v9" }),
    ).not.toBeInTheDocument();
    await user.click(
      screen.getAllByRole("option", { name: /telecom-core\s*，\s*v2/ }).at(-1)!,
    );
    await user.click(screen.getByRole("button", { name: "回滚到所选版本" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Rollback outcome unknown",
    );
    await chooseHistoryVersion(user, "telecom-core， v1");
    await user.click(
      screen.getByRole("button", { name: "使用同一幂等键重试原请求" }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "使用同一幂等键重试原请求" }),
      ).not.toBeInTheDocument(),
    );
    const actions = mutations(requests);
    expect(actions).toHaveLength(2);
    expect(actions.map(requestBody)).toEqual([
      { version: "2", expected_pointer_revision: 7 },
      { version: "2", expected_pointer_revision: 7 },
    ]);
    expect(idempotencyKey(actions[0])).toBeTruthy();
    expect(idempotencyKey(actions[1])).toBe(idempotencyKey(actions[0]));
    await user.click(screen.getByRole("combobox", { name: "目标历史版本" }));
    expect(
      screen.getAllByRole("option", { name: /telecom-core\s*，\s*v1/ }).at(-1),
    ).toHaveAttribute("aria-selected", "true");
  });
});

const pendingActions: {
  state: RulePackState;
  action: string;
  pending: string;
  rollback?: boolean;
}[] = [
  { state: "draft", action: "验证草稿", pending: "正在验证" },
  { state: "validated", action: "批准此内容摘要", pending: "正在批准" },
  { state: "approved", action: "发布此版本", pending: "正在发布" },
  { state: "published", action: "停用活动指针", pending: "正在停用" },
  {
    state: "published",
    action: "回滚到所选版本",
    pending: "正在回滚",
    rollback: true,
  },
];

describe("RulePackDetailPage in-flight actions", () => {
  it.each(pendingActions)(
    "prevents duplicate $action while the operation is pending",
    async ({ state, action, pending, rollback }) => {
      let resolve!: (response: Response) => void;
      const response = new Promise<Response>((complete) => {
        resolve = complete;
      });
      const entries: RulePackHistoryEntry[] = [
        {
          sequence: 1,
          pack_id: "telecom-core",
          version: "1",
          action: "publish",
          actor_id: "admin-test",
          content_hash: contentHash,
          pointer_revision: 7,
          created_at: "2026-10-20T12:00:00Z",
          previous_version: null,
          previous_active: null,
          resulting_version: "1",
          resulting_active: true,
        },
        {
          sequence: 2,
          pack_id: "telecom-core",
          version: "0",
          action: "publish",
          actor_id: "admin-test",
          content_hash: contentHash,
          pointer_revision: 6,
          created_at: "2026-10-19T12:00:00Z",
          previous_version: null,
          previous_active: null,
          resulting_version: "0",
          resulting_active: false,
        },
      ];
      const requests = renderDetail(
        versionDetail(state),
        () => response,
        entries,
      );
      const user = userEvent.setup();
      await screen.findByRole("button", { name: action });
      if (rollback) {
        await chooseHistoryVersion(user, "telecom-core， v1");
      }
      await clickAction(user, action);
      const busy = await screen.findByRole("button", { name: pending });
      expect(busy).toBeDisabled();
      expect(screen.getByLabelText("目标历史版本")).toBeDisabled();
      await user.click(busy);
      expect(mutations(requests)).toHaveLength(1);
      resolve(
        json(
          {
            error: {
              code: "forbidden",
              message: "Action no longer permitted",
              retryable: false,
            },
          },
          403,
        ),
      );
      const error = await screen.findByRole("alert");
      expect(error).toHaveTextContent("Action no longer permitted");
      expect(error).toHaveFocus();
      await waitFor(() =>
        expect(screen.getByRole("button", { name: action })).toBeEnabled(),
      );
      expect(
        screen.queryByRole("button", { name: "使用同一幂等键重试原请求" }),
      ).not.toBeInTheDocument();
    },
  );
});

describe("RulePackDetailPage pending save", () => {
  it("prevents duplicate saves and retains the edited values after a server failure", async () => {
    let resolve!: (response: Response) => void;
    const response = new Promise<Response>((complete) => {
      resolve = complete;
    });
    const requests = renderDetail(versionDetail("draft"), () => response);
    const user = userEvent.setup();
    await screen.findByRole("button", { name: "保存草稿" });
    firstThreshold("150.000");
    await user.click(screen.getByRole("button", { name: "保存草稿" }));
    const pending = await screen.findByRole("button", { name: "正在保存" });
    expect(pending).toBeDisabled();
    expect(screen.getAllByLabelText("比较阈值")[0]).toBeDisabled();
    await user.click(pending);
    expect(mutations(requests)).toHaveLength(1);
    resolve(
      json(
        {
          error: {
            code: "service_unavailable",
            message: "Draft could not be persisted",
            retryable: false,
          },
        },
        503,
      ),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Draft could not be persisted",
    );
    expect(screen.getAllByLabelText("比较阈值")[0]).toHaveValue("150.000");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "保存草稿" })).toBeEnabled(),
    );
    expect(screen.getByRole("button", { name: "验证草稿" })).toBeDisabled();
  });
});
