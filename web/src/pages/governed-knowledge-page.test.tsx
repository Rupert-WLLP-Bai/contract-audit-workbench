import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import { AdminApp } from "../admin-app";
import type { KnowledgeVersion, PolicyPreview } from "../api/governed-knowledge";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { validateKnowledgeDraft } from "../components/governed-knowledge-editor";
import { GovernedKnowledgeCreatePage, GovernedKnowledgeDetailPage, GovernedKnowledgeListPage } from "./governed-knowledge-page";

const clients: QueryClient[] = [];
const root = "/api/admin/governed-knowledge";
const id = "operator-policy";
const versionId = "a".repeat(64);
const digest = "b".repeat(64);
const versionPath = `${root}/${id}/versions/${versionId}`;
const initial: KnowledgeVersion = {
  ref: { knowledge_id: id, version_id: versionId, content_sha256: digest },
  content: {
    knowledge_id: id, version: "1", kind: "policy", title: "专线制度", summary: "核对带宽", body: "带宽不低于100 Mbit/s。", source_type: "reference", source_reference: "制度文件",
    scope: { business_line: "ENT", scenario: "line", contract_type: "service" }, valid_from: "2026-01-01T00:00:00Z", valid_until: "2027-01-01T00:00:00Z", applicability: [], exclusions: [],
    binding: { pack_id: "pack", pack_version: "1", pack_content_hash: "c".repeat(64), pack_snapshot_digest: "d".repeat(64), compiler_ref: "governed-rulepack/v1", rule_ids: ["BW"], source_spans: [{ rule_id: "BW", start: 0, end: 15, quote: "带宽不低于100 Mbit/s" }], candidate_diff: "差异已审阅", validation_evidence: "校验记录" },
  }, status: "submitted", revision: 2, cross_case_reviewed: false, active: false, pointer_revision: 7, created_by: "admin", created_at: "2026-01-01T00:00:00Z",
};
const preview: PolicyPreview = {
  ref: initial.ref, extractor_ref: "governed-policy-threshold/v1", approvable: false,
  comparisons: [{ rule_id: "BW", comparator: "minimum_bandwidth", status: "different", reason: "threshold_or_unit_mismatch", candidate: { comparator: "minimum_bandwidth", value: "100", unit: "Mbit/s", start: 0, end: 15, quote: "带宽不低于100 Mbit/s" }, rule_threshold: { value: "200", unit: "Mbit/s" } }],
  issues: [], semantic_only: [{ start: 16, end: 20, quote: "特殊情况下", reason: "semantic_review_required" }],
};
function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}
function Destination() { return <output aria-label="目标路径">{useLocation().pathname}</output>; }
function show(path: string, client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })) {
  clients.push(client);
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}><Routes>
    <Route path="/admin/governed-knowledge" element={<GovernedKnowledgeListPage />} />
    <Route path="/admin/governed-knowledge/new" element={<GovernedKnowledgeCreatePage />} />
    <Route path="/admin/governed-knowledge/:knowledgeId/versions/:versionId" element={<GovernedKnowledgeDetailPage />} />
    <Route path="/destination/:knowledgeId/versions/:versionId" element={<Destination />} />
  </Routes></MemoryRouter></QueryClientProvider>);
}
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); setSessionIdentity(null); vi.unstubAllGlobals(); });

describe("knowledge governance administration", () => {
  it("validates source offsets as Unicode characters", () => {
    const content = { ...initial.content, body: "\u{20000}带宽不低于100 Mbit/s。", binding: { ...initial.content.binding, source_spans: [{ rule_id: "BW", start: 1, end: 16, quote: "带宽不低于100 Mbit/s" }] } };
    expect(validateKnowledgeDraft(content)).toEqual([]);
    const wrong = { ...content, binding: { ...content.binding, source_spans: [{ ...content.binding.source_spans[0]!, start: 2, end: 17 }] } };
    expect(validateKnowledgeDraft(wrong)).toContainEqual({ id: "span-quote-0", message: "引用文字和起始位置必须与正文一致" });
  });
  it("exposes the governance route through the admin shell", async () => {
    const paths: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      paths.push(path);
      if (path === "/api/session?audience=admin") return json({ audience: "admin", actor: { id: "admin", display_name: "管理员", role: "admin", tenant_id: "tenant", org_id: "org" } });
      if (path === root) return json({ items: [] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
    clients.push(client);
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/admin/governed-knowledge"]}><AdminApp /></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByRole("heading", { name: "知识治理" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "知识治理" })).toHaveAttribute("href", "/admin/governed-knowledge");
    await waitFor(() => expect(paths).toContain(root));
  });
  it("does not open governance for a non-admin session", async () => {
    const paths: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      paths.push(path);
      if (path === "/api/session?audience=admin") return json({ audience: "admin", actor: { id: "reviewer", display_name: "审查员", role: "reviewer", tenant_id: "tenant", org_id: "org" } });
      return json({ error: { code: "forbidden", message: "无权访问", retryable: false } }, 403);
    }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
    clients.push(client);
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/admin/governed-knowledge"]}><AdminApp /></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByText("需要管理权限")).toBeInTheDocument();
    expect(paths).not.toContain(root);
  });
  it("shows actual policy differences and retries an uncertain command with the same revision and key", async () => {
    const requests: RequestInit[] = [];
    let current = initial;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath && init?.method === "GET") return json(current);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/preview`) return json(preview);
      if (path === `${versionPath}/commands` && init?.method === "POST") {
        requests.push(init);
        if (requests.length === 1) return json({ error: { code: "case_busy", message: "关联案件正在修改", retryable: true, details: { reason: "locked" } } }, 503);
        current = { ...initial, status: "validated", revision: 3 };
        return json(current);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("BW · 存在差异")).toBeInTheDocument();
    expect(screen.getByText(/^规则阈值：200 Mbit\/s$/)).toBeInTheDocument();
    expect(screen.getByText(/特殊情况下/)).toBeInTheDocument();
    expect(screen.queryByText("提取或绑定问题")).not.toBeInTheDocument();
    expect(screen.queryByText("正在读取政策规则差异")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("校验依据"), "已核对规则与制度");
    await user.click(screen.getByRole("button", { name: "校验" }));
    expect(await screen.findByText("关联案件正在修改")).toBeInTheDocument();
    expect(screen.getByText(/错误代码：case_busy/)).toBeInTheDocument();
    expect(screen.getByText(/locked/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试校验" }));
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(JSON.parse(String(requests[0]?.body))).toEqual({ action: "validate", expected_revision: 2, expected_content_sha256: digest, expected_pointer_revision: 7, reason: "已核对规则与制度", cross_case_reviewed: false });
    expect(requests[1]?.body).toBe(requests[0]?.body);
    expect(new Headers(requests[1]?.headers).get("Idempotency-Key")).toBe(new Headers(requests[0]?.headers).get("Idempotency-Key"));
    expect(await screen.findByRole("button", { name: "批准" })).toBeInTheDocument();
  });
  it.each([
    { state: "draft", active: false, label: "提交", action: "submit", next: "submitted", nextActive: false },
    { state: "approved", active: false, label: "发布", action: "publish", next: "published", nextActive: true },
    { state: "published", active: true, label: "撤回", action: "withdraw", next: "withdrawn", nextActive: false },
    { state: "published", active: false, label: "回滚", action: "rollback", next: "published", nextActive: true },
    { state: "withdrawn", active: false, label: "回滚", action: "rollback", next: "published", nextActive: true },
  ])("uses the server revision and reason for $action", async ({ state, active, label, action, next, nextActive }) => {
    let current: KnowledgeVersion = { ...initial, status: state, active, revision: 8, pointer_revision: 12 };
    const requests: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath && init?.method === "GET") return json(current);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/preview`) return json(preview);
      if (path === `${versionPath}/commands` && init?.method === "POST") {
        requests.push(init);
        current = { ...current, status: next, active: nextActive, revision: 9, pointer_revision: action === "submit" ? 12 : 13 };
        return json(current);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    const reason = await screen.findByLabelText(`${label}依据`);
    await user.type(reason, "人工审阅依据");
    await user.click(screen.getByRole("button", { name: label }));
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(JSON.parse(String(requests[0]?.body))).toEqual({ action, expected_revision: 8, expected_content_sha256: digest, expected_pointer_revision: 12, reason: "人工审阅依据", cross_case_reviewed: false });
  });
  it("requires a real cross-case review confirmation before experience approval", async () => {
    const experience: KnowledgeVersion = { ...initial, status: "validated", revision: 3, content: { ...initial.content, kind: "experience", source_type: "human_feedback", source_case_id: "case-1", candidate_id: "candidate-1", candidate_version_id: "e".repeat(64), candidate_content_sha256: "f".repeat(64), candidate_review_diff: "已删除敏感信息", reviewed_applicability: ["适用"], reviewed_exclusions: ["排除"], check_action: "核对", minimum_evidence: "合同", counterexamples: ["反例"], policy_refs: [initial.ref] } };
    const requests: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath && init?.method === "GET") return json(experience);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/commands` && init?.method === "POST") { requests.push(init); return json({ ...experience, status: "approved", revision: 4, cross_case_reviewed: true }); }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    await screen.findByRole("heading", { name: "专线制度" });
    await user.type(screen.getByLabelText("批准依据"), "已审阅候选");
    await user.click(screen.getByRole("button", { name: "批准" }));
    expect(await screen.findByText("请先确认跨案审阅及敏感内容处理")).toBeInTheDocument();
    expect(requests).toHaveLength(0);
    const confirmation = screen.getByRole("checkbox", { name: /我已审阅跨案适用性/ });
    expect(confirmation).not.toBeChecked();
    await user.click(confirmation);
    await user.click(screen.getByRole("button", { name: "批准" }));
    await waitFor(() => expect(requests).toHaveLength(1));
    const body = JSON.parse(String(requests[0]?.body)) as Record<string, unknown>;
    expect(body).toMatchObject({ action: "approve", expected_revision: 3, expected_content_sha256: digest, expected_pointer_revision: 7, reason: "已审阅候选", cross_case_reviewed: true });
    expect(body).not.toHaveProperty("approval_actor_id");
  });
  it("prefills an experience candidate link without inventing its review or approval", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === root || path === "/api/admin/rulepacks") return json({ items: [] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/new?source_case=case-1&candidate_id=candidate-1&candidate_version_id=${"e".repeat(64)}&candidate_content_sha256=${"f".repeat(64)}`);
    expect(await screen.findByLabelText("候选 ID")).toHaveValue("candidate-1");
    expect(screen.getByLabelText("来源案件 ID（如有）")).toHaveValue("case-1");
    expect(screen.getByLabelText("候选版本 ID")).toHaveValue("e".repeat(64));
    expect(screen.getByLabelText("候选审阅差异")).toHaveValue("");
    expect(screen.queryByRole("button", { name: "批准" })).not.toBeInTheDocument();
  });
  it("copies a version into an editable new draft instead of mutating it", async () => {
    const requests: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath) return json(initial);
      if (path === "/api/admin/rulepacks" || (path === root && init?.method === "GET")) return json({ items: [] });
      if (path === root && init?.method === "POST") {
        requests.push(init);
        const payload = JSON.parse(String(init.body)) as { content: KnowledgeVersion["content"] };
        return json({ ...initial, ref: { ...initial.ref, version_id: "9".repeat(64) }, content: payload.content, status: "draft", revision: 1 }, 201);
      }
      return json({ error: { code: "not_found", message: "没有找到规则包", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/new?from_knowledge=${id}&from_version=${versionId}`);
    const title = await screen.findByLabelText("标题");
    expect(screen.getByLabelText("知识 ID")).toHaveAttribute("readonly");
    fireEvent.change(title, { target: { value: "更新的专线制度" } });
    await user.type(screen.getByLabelText("版本号"), "2");
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    await waitFor(() => expect(requests).toHaveLength(1));
    const payload = JSON.parse(String(requests[0]?.body)) as { content: KnowledgeVersion["content"] };
    expect(payload.content).toMatchObject({ knowledge_id: id, version: "2", title: "更新的专线制度", body: initial.content.body });
    expect(payload.content).not.toHaveProperty("source_org_id");
    expect(new Headers(requests[0]?.headers).get("Idempotency-Key")).toBeTruthy();
  });
  it("shows a denied or missing source without displaying stale content", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(json({ error: { code: "not_found", message: "没有找到对应记录", retryable: false } }, 404)));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("没有找到对应记录")).toBeInTheDocument();
    expect(screen.getByText(/错误代码：not_found/)).toBeInTheDocument();
    expect(screen.queryByText(initial.content.body)).not.toBeInTheDocument();
  });

  it("hides cached detail content after a failed refresh", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
    const detailKey = ["admin", "governed-knowledge", "detail", id, versionId];
    client.setQueryData(detailKey, initial);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) throw new TypeError("connection reset");
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/preview`) return json(preview);
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`, client);
    expect(await screen.findByText("知识版本没有加载")).toBeInTheDocument();
    expect(screen.queryByText(initial.content.body)).not.toBeInTheDocument();
  });

  const summary = (overrides: Record<string, unknown> = {}) => ({
    ref: { knowledge_id: "kb-1", version_id: digest, content_sha256: "c".repeat(64) },
    version: "1",
    title: "专线制度",
    kind: "policy",
    source_type: "reference",
    scope: { business_line: "ENT", scenario: "line", contract_type: "service" },
    valid_from: "2026-01-01T00:00:00Z",
    valid_until: "2027-01-01T00:00:00Z",
    status: "published",
    revision: 2,
    pointer_revision: 3,
    active: true,
    ...overrides,
  });

  it("lists knowledge versions with their scope, status and revision", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === root) return json({ items: [
        summary(),
        summary({ ref: { knowledge_id: "kb-2", version_id: "e".repeat(64), content_sha256: "f".repeat(64) }, title: "操作手册", kind: "guidance", status: "draft", active: false, revision: 1, pointer_revision: 0, scope: { business_line: "EB", scenario: "cloud", contract_type: "purchase", org_id: "org-1", case_id: "case-1" } }),
        summary({ ref: { knowledge_id: "kb-3", version_id: "a".repeat(64), content_sha256: "b".repeat(64) }, title: "工程制度", status: "approved", active: false, scope: { business_line: "EB", scenario: "build", contract_type: "works", org_id: "org-2" } }),
      ] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show("/admin/governed-knowledge");
    expect(await screen.findByRole("link", { name: "专线制度" })).toHaveAttribute(
      "href",
      `/admin/governed-knowledge/kb-1/versions/${encodeURIComponent(digest)}`,
    );
    expect(screen.getByText("kb-1 · v1")).toBeInTheDocument();
    expect(screen.getByText("已发布")).toBeInTheDocument();
    expect(screen.getByText("当前生效")).toBeInTheDocument();
    expect(screen.getByText("ENT / line / service")).toBeInTheDocument();
    expect(screen.getByText("操作指引")).toBeInTheDocument();
    expect(screen.getByText("草稿")).toBeInTheDocument();
    expect(screen.getByText(/组织 org-1 · 案件 case-1/)).toBeInTheDocument();
    const firstRow = screen.getByRole("link", { name: "专线制度" }).closest("tr")!;
    const draftRow = screen.getByRole("link", { name: "操作手册" }).closest("tr")!;
    expect(within(firstRow).getByText("已发布").closest(".ant-tag")).toHaveClass("ant-tag-success");
    expect(within(draftRow).getByText("草稿").closest(".ant-tag")).toHaveClass("ant-tag-default");
    expect(within(firstRow).queryByText(/组织/)).not.toBeInTheDocument();
    const orgOnlyRow = screen.getByRole("link", { name: "工程制度" }).closest("tr")!;
    expect(within(orgOnlyRow).getByText(/组织 org-2/)).toBeInTheDocument();
    expect(within(orgOnlyRow).queryByText(/案件/)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("查找知识"), "不存在");
    expect(screen.queryByRole("link", { name: "专线制度" })).not.toBeInTheDocument();
    expect(screen.getByText("没有可显示的知识版本")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("查找知识"), { target: { value: "" } });
    await chooseOption(user, screen.getByRole("combobox", { name: /知识类型/ }), "操作指引");
    expect(screen.queryByRole("link", { name: "专线制度" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "操作手册" })).toBeInTheDocument();
  });

  it("shows loading, forbidden and retryable list failures", async () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    show("/admin/governed-knowledge");
    expect(await screen.findByText("正在读取知识版本")).toBeInTheDocument();
    cleanup();

    let attempt = 0;
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      calls.push(path);
      attempt += 1;
      if (attempt === 1) return json({ error: { code: "unavailable", message: "列表不可用", retryable: true, details: { scope: "admin" } } }, 503);
      return json({ items: [] });
    }));
    const user = userEvent.setup();
    show("/admin/governed-knowledge");
    expect(await screen.findByText("知识版本列表没有加载")).toBeInTheDocument();
    expect(screen.getByText("列表不可用")).toBeInTheDocument();
    expect(screen.getByText(/错误代码：unavailable/)).toBeInTheDocument();
    expect(screen.getByText(/"scope": "admin"/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() => expect(calls.filter((path) => path === root)).toHaveLength(2));
    cleanup();

    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(json({ error: { code: "forbidden", message: "无权访问", retryable: false } }, 403)));
    show("/admin/governed-knowledge");
    expect(await screen.findByText("知识版本列表没有加载")).toBeInTheDocument();
    expect(screen.getByText("无权访问")).toBeInTheDocument();
  });

  it("shows a validation summary and replayable create failure", async () => {
    const posts: RequestInit[] = [];
    let postAttempt = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath) return json(initial);
      if (path === "/api/admin/rulepacks" || (path === root && init?.method === "GET")) return json({ items: [] });
      if (path === root && init?.method === "POST") {
        posts.push(init);
        postAttempt += 1;
        if (postAttempt === 1) return json({ error: { code: "case_busy", message: "请稍后重试", retryable: true } }, 503);
        return json({ ...initial, ref: { ...initial.ref, version_id: "9".repeat(64) }, status: "draft", revision: 1 }, 201);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/new?from_knowledge=${id}&from_version=${versionId}`);
    const version = await screen.findByLabelText("版本号");
    expect(screen.queryByText("请检查以下内容")).not.toBeInTheDocument();
    await user.type(version, "2");
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    expect(await screen.findByText("请稍后重试")).toBeInTheDocument();
    expect(screen.queryByRole("list")).not.toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: /创建草稿/ }));
    await waitFor(() => expect(posts).toHaveLength(2));
    expect(new Headers(posts[1]?.headers).get("Idempotency-Key")).toBe(
      new Headers(posts[0]?.headers).get("Idempotency-Key"),
    );
    expect(posts[1]?.body).toBe(posts[0]?.body);
  });

  it("summarises client validation without sending a create request", async () => {
    const posts: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/admin/rulepacks") return json({ items: [] });
      if (path === root && init?.method === "GET") return json({ items: [] });
      if (init?.method === "POST") posts.push(init);
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show("/admin/governed-knowledge/new");
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    expect(await screen.findByText("请检查以下内容")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "请填写知识 ID" })).toHaveAttribute(
      "href",
      "#knowledge-id",
    );
    expect(screen.getByRole("link", { name: "至少添加一条规则及对应正文引用" })).toHaveAttribute(
      "href",
      "#knowledge-spans",
    );
    expect(posts).toHaveLength(0);
  });

  it("rejects an incomplete or failed source version", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/admin/rulepacks") return json({ items: [] });
      if (path === root) return json({ items: [] });
      return json({ error: { code: "not_found", message: "来源不存在", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/new?from_knowledge=${id}`);
    expect(await screen.findByText("来源版本信息不完整")).toBeInTheDocument();
    cleanup();
    show(`/admin/governed-knowledge/new?from_knowledge=${id}&from_version=${versionId}`);
    expect(await screen.findByText("来源知识版本没有加载")).toBeInTheDocument();
    expect(screen.getByText("来源不存在")).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "返回知识列表" }).length).toBeGreaterThan(0);
  });

  it("creates an experience draft with trimmed review lists", async () => {
    const source: KnowledgeVersion = {
      ...initial,
      content: {
        ...initial.content,
        kind: "experience",
        source_type: "human_feedback",
        source_case_id: "case-1",
        candidate_id: "candidate-1",
        candidate_version_id: "e".repeat(64),
        candidate_content_sha256: "f".repeat(64),
        candidate_review_diff: "已审阅",
        reviewed_applicability: [" 适用 A ", "", "适用 B"],
        reviewed_exclusions: [" 排除 A ", ""],
        counterexamples: [" 反例 ", ""],
        check_action: "核对",
        minimum_evidence: "合同",
        policy_refs: [initial.ref],
      },
    };
    const posts: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath) return json(source);
      if (path === "/api/admin/rulepacks" || (path === root && init?.method === "GET")) return json({ items: [] });
      if (path === root && init?.method === "POST") {
        posts.push(init);
        const payload = JSON.parse(String(init.body)) as { content: KnowledgeVersion["content"] };
        return json({ ...source, ref: { ...source.ref, version_id: "9".repeat(64) }, content: payload.content, status: "draft", revision: 1 }, 201);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/new?from_knowledge=${id}&from_version=${versionId}`);
    const version = await screen.findByLabelText("版本号");
    await user.type(version, "2");
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    await waitFor(() => expect(posts).toHaveLength(1));
    const payload = JSON.parse(String(posts[0]?.body)) as { content: KnowledgeVersion["content"] };
    expect(payload.content.reviewed_applicability).toEqual(["适用 A", "适用 B"]);
    expect(payload.content.reviewed_exclusions).toEqual(["排除 A"]);
    expect(payload.content.counterexamples).toEqual(["反例"]);
  });

  it("renders preview issues and missing candidates", async () => {
    const previewVariant: PolicyPreview = {
      ...preview,
      approvable: true,
      comparisons: [{ rule_id: "BW", comparator: "monthly_sla", status: "ambiguous", rule_threshold: { value: "99.9", unit: "%", basis: "月" } }],
      issues: [{ start: 0, end: 4, quote: "带宽", reason: "missing_value" }],
      semantic_only: [],
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json(initial);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/preview`) return json(previewVariant);
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText(/预览：规则关联和可以提取的数值已通过检查。/)).toBeInTheDocument();
    expect(screen.getByText(/规则关联和可以提取的数值已通过检查/)).toBeInTheDocument();
    expect(screen.getByText(/BW · 无法唯一确认/)).toBeInTheDocument();
    expect(screen.getByText(/未提取到候选/)).toBeInTheDocument();
    expect(screen.getByText(/规则阈值：99.9 %，月/)).toBeInTheDocument();
    expect(screen.queryByText(/原因：/)).not.toBeInTheDocument();
    expect(screen.getByText("提取或绑定问题")).toBeInTheDocument();
    expect(screen.getByText(/missing_value：带宽（0–4）/)).toBeInTheDocument();
    expect(screen.queryByText("仍需人工核对含义")).not.toBeInTheDocument();
  });

  it("shows an empty preview comparison list", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json(initial);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/preview`) return json({ ...preview, comparisons: [] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("没有返回规则比较项。")).toBeInTheDocument();
  });

  it("rejects a stale preview digest and retries it", async () => {
    let attempts = 0;
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      calls.push(path);
      if (path === versionPath) return json(initial);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/preview`) {
        attempts += 1;
        if (attempts === 1) return json({ ...preview, ref: { ...preview.ref, content_sha256: "z".repeat(64) } });
        return json(preview);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("政策规则差异没有加载")).toBeInTheDocument();
    expect(screen.getByText("政策预览与当前版本摘要不一致")).toBeInTheDocument();
    expect(screen.queryByText(/服务允许重试/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() =>
      expect(calls.filter((path) => path === `${versionPath}/preview`)).toHaveLength(2),
    );
    expect(await screen.findByText(/BW · 存在差异/)).toBeInTheDocument();
  });

  it("requires an action reason and keeps terminal versions read-only", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json({ ...initial, status: "draft" });
      if (path === `${root}/${id}/history`) return json({ items: [] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    await user.click(await screen.findByRole("button", { name: "提交" }));
    expect(await screen.findByText("请填写操作依据")).toBeInTheDocument();
    cleanup();

    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json({ ...initial, status: "archived" });
      if (path === `${root}/${id}/history`) return json({ items: [] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("当前状态没有可执行的治理操作。")).toBeInTheDocument();
  });

  it("shows a non-retryable command failure and refreshes after a conflict", async () => {
    let gets = 0;
    const posts: RequestInit[] = [];
    let postAttempt = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath && init?.method === "GET") {
        gets += 1;
        return json({ ...initial, status: "submitted" });
      }
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/commands` && init?.method === "POST") {
        posts.push(init);
        postAttempt += 1;
        if (postAttempt === 1) return json({ error: { code: "policy_missing", message: "政策缺失", retryable: false, details: { policy: "required" } } }, 400);
        return json({ error: { code: "conflict", message: "版本冲突", retryable: false } }, 409);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    const reason = await screen.findByLabelText("校验依据");
    await user.type(reason, "核对规则");
    await user.click(screen.getByRole("button", { name: "校验" }));
    expect(await screen.findByText("政策缺失")).toBeInTheDocument();
    expect(screen.getByText(/错误代码：policy_missing/)).toBeInTheDocument();
    expect(screen.getByText("政策缺失").closest("div[tabindex]")).toHaveAttribute(
      "tabindex",
      "-1",
    );
    expect(screen.queryAllByRole("status").some((item) => item.textContent === "")).toBe(false);
    expect(gets).toBe(1);
    expect(screen.queryByRole("button", { name: "重试校验" })).not.toBeInTheDocument();
    const getsBeforeConflict = gets;
    await user.click(await screen.findByRole("button", { name: /校验/ }));
    expect(await screen.findByText("版本冲突")).toBeInTheDocument();
    await waitFor(() => expect(gets).toBeGreaterThan(getsBeforeConflict));
    expect(screen.getByText("服务端记录已变化，请检查重新读取的版本后再操作。")).toBeInTheDocument();
    expect(posts).toHaveLength(2);
  });

  it("reports success and renders approval metadata", async () => {
    let current: KnowledgeVersion = { ...initial, status: "submitted" };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath && init?.method === "GET") return json(current);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/commands` && init?.method === "POST") {
        current = { ...initial, status: "validated", revision: 3, approval_actor_id: "admin", approved_digest: digest, approval_reason: "内容完整" };
        return json(current);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    await user.type(await screen.findByLabelText("校验依据"), "核对规则");
    await user.click(screen.getByRole("button", { name: "校验" }));
    expect(await screen.findByText("校验操作已由服务端记录。")).toBeInTheDocument();
    expect(screen.getByText(/批准人：admin/)).toBeInTheDocument();
    expect(screen.getByText(/内容完整/)).toBeInTheDocument();
  });

  it("renders experience content, policy dependencies and history transitions", async () => {
    const experience: KnowledgeVersion = {
      ...initial,
      status: "approved",
      cross_case_reviewed: true,
      content: {
        ...initial.content,
        kind: "experience",
        source_type: "human_feedback",
        source_case_id: "case-1",
        candidate_id: "candidate-1",
        candidate_version_id: "e".repeat(64),
        candidate_content_sha256: "f".repeat(64),
        candidate_review_diff: "已删除敏感信息",
        reviewed_applicability: ["适用"],
        reviewed_exclusions: ["排除"],
        check_action: "核对",
        minimum_evidence: "合同",
        counterexamples: ["反例"],
        policy_refs: [initial.ref],
        exception_basis: "例外依据",
      },
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json(experience);
      if (path === `${root}/${id}/history`) return json({ items: [{
        sequence: 2,
        ref: initial.ref,
        action: "publish",
        revision: 4,
        pointer_revision: 9,
        actor_id: "admin",
        reason: "人工批准",
        previous_version_id: "v-old",
        previous_active: true,
        resulting_version_id: "v-new",
        resulting_active: false,
        occurred_at: "2026-01-02T00:00:00Z",
      }, {
        sequence: 1,
        ref: initial.ref,
        action: "create",
        revision: 1,
        pointer_revision: 0,
        actor_id: "admin",
        reason: "",
        previous_version_id: "v-only",
        previous_active: false,
        resulting_active: false,
        occurred_at: "2026-01-01T00:00:00Z",
      }] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("经验审阅")).toBeInTheDocument();
    expect(screen.getByText("跨案审阅：已记录")).toBeInTheDocument();
    expect(screen.getByText(/候选：candidate-1/)).toBeInTheDocument();
    expect(screen.getByText(/反例：反例/)).toBeInTheDocument();
    expect(screen.getByText(/例外依据：例外依据/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /operator-policy \/ a{12}/ })).toBeInTheDocument();
    expect(screen.getByText("发布", { selector: "strong" })).toBeInTheDocument();
    expect(screen.getByText(/人工批准/)).toBeInTheDocument();
    expect(screen.getByText(/v-old（生效） → v-new（未生效）/)).toBeInTheDocument();
    const createdEntry = screen.getByText("创建", { selector: "strong" }).closest("li")!;
    expect(within(createdEntry).queryByText(/依据：/)).not.toBeInTheDocument();
    expect(screen.getByText(/v-only（未生效） → 无（未生效）/)).toBeInTheDocument();
    expect(screen.queryByText("正在读取历史")).not.toBeInTheDocument();
  });

  it("reports an empty or failed history", async () => {
    let attempts = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json({ ...initial, status: "archived" });
      if (path === `${root}/${id}/history`) {
        attempts += 1;
        if (attempts === 1) return json({ error: { code: "unavailable", message: "历史不可用", retryable: true } }, 503);
        return json({ items: [] });
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("历史不可用")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试历史" }));
    expect(await screen.findByText("没有返回历史记录。")).toBeInTheDocument();
  });

  it("omits empty optional fields and non-exception bases from a created draft", async () => {
    const source: KnowledgeVersion = {
      ...initial,
      content: {
        ...initial.content,
        source_case_id: "",
        exception_basis: "复制过来的旧例外",
        scope: { ...initial.content.scope, org_id: "", case_id: "" },
      },
    };
    const posts: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath) return json(source);
      if (path === "/api/admin/rulepacks" || (path === root && init?.method === "GET")) return json({ items: [] });
      if (path === root && init?.method === "POST") {
        posts.push(init);
        const payload = JSON.parse(String(init.body)) as { content: KnowledgeVersion["content"] };
        return json({ ...source, ref: { ...source.ref, version_id: "9".repeat(64) }, content: payload.content, status: "draft", revision: 1 }, 201);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/new?from_knowledge=${id}&from_version=${versionId}`);
    await user.type(await screen.findByLabelText("版本号"), "2");
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    await waitFor(() => expect(posts).toHaveLength(1));
    const payload = JSON.parse(String(posts[0]?.body)) as { content: KnowledgeVersion["content"] };
    expect(payload.content).not.toHaveProperty("source_case_id");
    expect(payload.content).not.toHaveProperty("exception_basis");
    expect(payload.content.scope).not.toHaveProperty("org_id");
    expect(payload.content.scope).not.toHaveProperty("case_id");
  });

  it("shows a single validation issue, focuses the summary and sends nothing", async () => {
    const posts: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath) return json(initial);
      if (path === "/api/admin/rulepacks" || (path === root && init?.method === "GET")) return json({ items: [] });
      if (init?.method === "POST") posts.push(init);
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/new?from_knowledge=${id}&from_version=${versionId}`);
    expect(screen.queryByText("请检查以下内容")).not.toBeInTheDocument();
    await user.type(await screen.findByLabelText("版本号"), "2");
    fireEvent.change(screen.getByLabelText("标题"), { target: { value: "   " } });
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    const heading = await screen.findByText("请检查以下内容");
    expect(screen.getByRole("link", { name: "请填写标题" })).toHaveAttribute(
      "href",
      "#knowledge-title",
    );
    expect(heading.closest('[role="alert"]')).toHaveAttribute("tabindex", "-1");
    expect(heading.closest('[role="alert"]')).toHaveFocus();
    expect(posts).toHaveLength(0);
  });

  it("renders null applicability and exclusions as empty", async () => {
    const content = { ...initial.content, applicability: null, exclusions: null };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json({ ...initial, content });
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/preview`) return json(preview);
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("适用条件：无")).toBeInTheDocument();
    expect(screen.getByText("排除条件：无")).toBeInTheDocument();
  });

  it("renders source, scope and binding without optional values", async () => {
    const content: KnowledgeVersion["content"] = {
      ...initial.content,
      kind: "guidance",
      binding: {
        ...initial.content.binding,
        source_spans: null,
        extraction_compiler_ref: undefined,
      },
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json({ ...initial, content });
      if (path === `${root}/${id}/history`) return json({ items: [] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("来源：reference · 制度文件")).toBeInTheDocument();
    expect(screen.getByText("范围：ENT / line / service")).toBeInTheDocument();
    expect(screen.getByText("规则包与正文引用")).toBeInTheDocument();
    expect(screen.queryByText(/政策提取器/)).not.toBeInTheDocument();
    expect(screen.queryByText(/例外依据/)).not.toBeInTheDocument();
  });

  it("renders an experience review without optional lists", async () => {
    const content: KnowledgeVersion["content"] = {
      ...initial.content,
      kind: "experience",
      source_type: "human_feedback",
      source_case_id: "case-1",
      candidate_id: "candidate-1",
      candidate_version_id: "e".repeat(64),
      candidate_content_sha256: "f".repeat(64),
      candidate_review_diff: "已审阅",
      reviewed_applicability: undefined,
      reviewed_exclusions: undefined,
      counterexamples: undefined,
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json({ ...initial, content });
      if (path === `${root}/${id}/history`) return json({ items: [] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByText("经验审阅")).toBeInTheDocument();
    expect(screen.getByText("审阅适用条件：")).toBeInTheDocument();
    expect(screen.getByText("审阅排除条件：")).toBeInTheDocument();
    expect(screen.getByText("反例：")).toBeInTheDocument();
  });

  it("offers replay after retryable and server-side command failures", async () => {
    const failures = [
      { status: 400, code: "busy", retryable: true, message: "服务忙" },
      { status: 500, code: "server_error", retryable: false, message: "服务错误" },
    ];
    for (const failure of failures) {
      const posts: RequestInit[] = [];
      vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path === versionPath && init?.method === "GET") return json({ ...initial, status: "submitted" });
        if (path === `${root}/${id}/history`) return json({ items: [] });
        if (path === `${versionPath}/commands` && init?.method === "POST") {
          posts.push(init);
          return json({ error: { code: failure.code, message: failure.message, retryable: failure.retryable } }, failure.status);
        }
        return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
      }));
      const user = userEvent.setup();
      show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
      await user.type(await screen.findByLabelText("校验依据"), "核对规则");
      await user.click(screen.getByRole("button", { name: "校验" }));
      expect(await screen.findByText(failure.message)).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "重试校验" })).toBeInTheDocument();
      expect(posts).toHaveLength(1);
      cleanup();
    }
  });

  it("replays a network-failed command with the original key and body", async () => {
    let current: KnowledgeVersion = { ...initial, status: "submitted" };
    const requests: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath && init?.method === "GET") return json(current);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/commands` && init?.method === "POST") {
        requests.push(init);
        if (requests.length === 1) throw new TypeError("connection reset");
        current = { ...current, status: "validated", revision: 3 };
        return json(current);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    await user.type(await screen.findByLabelText("校验依据"), "确认策略来源");
    await user.click(screen.getByRole("button", { name: "校验" }));
    expect(await screen.findByText("无法连接审查服务")).toBeInTheDocument();
    const replay = await screen.findByRole("button", { name: "重试校验" });
    await user.click(replay);
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(requests[1]?.body).toBe(requests[0]?.body);
    expect(new Headers(requests[1]?.headers).get("Idempotency-Key")).toBe(
      new Headers(requests[0]?.headers).get("Idempotency-Key"),
    );
  });

  it("asks for cross-case review only for experience approval", async () => {
    const policy: KnowledgeVersion = {
      ...initial,
      status: "validated",
      content: {
        ...initial.content,
        source_case_id: "case-1",
        scope: { ...initial.content.scope, case_id: "case-2" },
      },
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json(policy);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/preview`) return json(preview);
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByLabelText("批准依据")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    cleanup();

    const experience: KnowledgeVersion = {
      ...initial,
      status: "submitted",
      content: {
        ...initial.content,
        kind: "experience",
        source_type: "human_feedback",
        source_case_id: "case-1",
      },
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === versionPath) return json(experience);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    expect(await screen.findByLabelText("校验依据")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("does not carry cross-case confirmation from validation into approval", async () => {
    let current: KnowledgeVersion = {
      ...initial,
      status: "submitted",
      content: {
        ...initial.content,
        kind: "experience",
        source_type: "human_feedback",
        source_case_id: "case-1",
        scope: { ...initial.content.scope, case_id: "case-2" },
      },
    };
    const requests: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath && init?.method === "GET") return json(current);
      if (path === `${root}/${id}/history`) return json({ items: [] });
      if (path === `${versionPath}/commands` && init?.method === "POST") {
        requests.push(init);
        current = { ...current, status: "validated", revision: current.revision + 1 };
        return json(current);
      }
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/${id}/versions/${versionId}`);
    await user.type(await screen.findByLabelText("校验依据"), "先完成规则校验");
    await user.click(screen.getByRole("button", { name: "校验" }));
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(JSON.parse(String(requests[0]?.body))).toMatchObject({
      action: "validate",
      cross_case_reviewed: false,
    });

    await user.type(await screen.findByLabelText("批准依据"), "重新确认跨案适用性");
    const confirmation = screen.getByRole("checkbox", { name: /我已审阅跨案适用性/ });
    expect(confirmation).not.toBeChecked();
    await user.click(screen.getByRole("button", { name: "批准" }));
    expect(await screen.findByText("请先确认跨案审阅及敏感内容处理")).toBeInTheDocument();
    expect(requests).toHaveLength(1);
  });

  it("keeps the source error page stable when a pending create fails after it unmounts the form", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
    let detailReads = 0;
    let failSource: ((reason: unknown) => void) | undefined;
    let failCreate: ((reason: unknown) => void) | undefined;
    const posts: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === versionPath && init?.method === "GET") {
        detailReads += 1;
        if (detailReads === 1) return json(initial);
        return await new Promise<Response>((_resolve, reject) => { failSource = reject; });
      }
      if (path === root && init?.method === "POST") {
        posts.push(init);
        return await new Promise<Response>((_resolve, reject) => { failCreate = reject; });
      }
      if (path === root || path === "/api/admin/rulepacks") return json({ items: [] });
      return json({ error: { code: "not_found", message: "没有找到", retryable: false } }, 404);
    }));
    const user = userEvent.setup();
    show(`/admin/governed-knowledge/new?from_knowledge=${id}&from_version=${versionId}`, client);
    await user.type(await screen.findByLabelText("版本号"), "2");
    await user.click(screen.getByRole("button", { name: "创建草稿" }));
    await waitFor(() => expect(posts).toHaveLength(1));

    let refreshSource: Promise<void> | undefined;
    await act(async () => {
      refreshSource = client.invalidateQueries({
        queryKey: ["admin", "governed-knowledge", "detail", id, versionId],
      });
    });
    await waitFor(() => expect(detailReads).toBe(2));
    expect(screen.getByLabelText("标题")).toBeInTheDocument();
    await act(async () => {
      if (!failSource) throw new Error("source refresh did not start");
      failSource(new TypeError("source refresh failed"));
    });
    expect(await screen.findByText("来源知识版本没有加载")).toBeInTheDocument();
    expect(screen.queryByLabelText("标题")).not.toBeInTheDocument();
    expect(screen.queryByText(initial.content.body)).not.toBeInTheDocument();

    await act(async () => {
      if (!failCreate) throw new Error("create request did not start");
      failCreate(new TypeError("create response was lost"));
      await refreshSource;
    });
    expect(screen.getByText("来源知识版本没有加载")).toBeInTheDocument();
    expect(screen.queryByLabelText("标题")).not.toBeInTheDocument();
    expect(posts).toHaveLength(1);
  });
});
