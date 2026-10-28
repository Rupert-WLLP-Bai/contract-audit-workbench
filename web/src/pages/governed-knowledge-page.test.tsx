import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import { AdminApp } from "../admin-app";
import type { KnowledgeVersion, PolicyPreview } from "../api/governed-knowledge";
import { renderWithAntd as render } from "../test/antd";
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
function show(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
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
    expect(screen.getByText(/规则阈值：200 Mbit\/s/)).toBeInTheDocument();
    expect(screen.getByText(/特殊情况下/)).toBeInTheDocument();
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
    await user.click(screen.getByRole("checkbox", { name: /我已审阅跨案适用性/ }));
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
});
