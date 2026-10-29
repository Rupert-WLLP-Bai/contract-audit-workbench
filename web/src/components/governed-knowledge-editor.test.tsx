import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { KnowledgeContent } from "../api/governed-knowledge";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import {
  KnowledgeEditor,
  emptyKnowledgeContent,
  validateKnowledgeDraft,
  type KnowledgeFormIssue,
} from "./governed-knowledge-editor";

const digest = (letter: string) => letter.repeat(64);
const quote = "带宽不低于100 Mbit/s";

function draft(overrides: Partial<KnowledgeContent> = {}): KnowledgeContent {
  return {
    ...emptyKnowledgeContent(),
    knowledge_id: "kb-1",
    version: "2",
    title: "专线制度",
    summary: "摘要",
    body: `${quote}。特殊情况下另行确认。`,
    source_reference: "制度文件",
    scope: { business_line: "ENT", scenario: "line", contract_type: "service" },
    valid_from: "2026-01-01T00:00:00Z",
    valid_until: "2027-01-01T00:00:00Z",
    binding: {
      pack_id: "pack",
      pack_version: "1",
      pack_content_hash: digest("c"),
      pack_snapshot_digest: digest("d"),
      compiler_ref: "governed-rulepack/v1",
      rule_ids: ["BW"],
      source_spans: [{ rule_id: "BW", start: 0, end: 15, quote }],
      candidate_diff: "差异已审阅",
      validation_evidence: "校验记录",
    },
    ...overrides,
  };
}

const packSummary = {
  id: "pack",
  version: "1",
  tenant_id: "tenant",
  status: "published",
  revision: 1,
  content_hash: digest("c"),
  snapshot_sha256: digest("d"),
  effective_from: "2026-01-01T00:00:00Z",
  effective_until: "2027-01-01T00:00:00Z",
  authority_sources: [],
  is_active: true,
  active_pointer_revision: 1,
};
const secondPack = { ...packSummary, id: "pack2", version: "2", content_hash: digest("e"), snapshot_sha256: digest("f") };
const draftPack = { ...packSummary, id: "draft-pack", status: "draft", is_active: false };
const packDetail = {
  pack: packSummary,
  manifest: {
    schema_version: "rulepack/v1",
    id: "pack",
    version: "1",
    tenant_id: "tenant",
    effective_from: "2026-01-01T00:00:00Z",
    effective_until: "2027-01-01T00:00:00Z",
    rules: [
      {
        rule_id: "BW",
        version: "1",
        domain: "technical",
        capability_id: "dedicated_line.minimum_bandwidth",
        mandatory: true,
        business_scope: { business_line: "ENT", scenario: "line", contract_type: "service" },
        tenant_id: "tenant",
        comparator: "minimum_bandwidth",
        threshold: { value: "100", unit: "Mbit/s" },
        required_fact: {
          dimension: "bandwidth",
          source_roles: ["procurement"],
          object_required: true,
          unit_family: "bandwidth",
          exclusions: [],
        },
        authority_sources: [],
      },
    ],
    lifecycle: { state: "published" },
  },
};
const policySummary = {
  ref: { knowledge_id: "policy", version_id: digest("9"), content_sha256: digest("8") },
  version: "3",
  title: "基础政策",
  kind: "policy",
  source_type: "reference",
  scope: { business_line: "ENT", scenario: "line", contract_type: "service" },
  valid_from: "2026-01-01T00:00:00Z",
  valid_until: "2027-01-01T00:00:00Z",
  status: "published",
  revision: 1,
  pointer_revision: 1,
  active: true,
};

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function installApi(overrides: (path: string) => Response | null = () => null) {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    calls.push(path);
    const overridden = overrides(path);
    if (overridden) return overridden;
    if (path === "/api/admin/rulepacks") {
      return json({ items: [packSummary, secondPack, draftPack] });
    }
    if (path === "/api/admin/rulepacks/pack/versions/1") return json(packDetail);
    if (path === "/api/admin/governed-knowledge") {
      return json({ items: [policySummary] });
    }
    return json(
      { error: { code: "not_found", message: "没有找到", retryable: false } },
      404,
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  return { fetchMock, calls };
}

function Harness({
  initial = draft(),
  issues = [],
  lockedIdentity = false,
  disabled = false,
  onChange,
}: {
  initial?: KnowledgeContent;
  issues?: KnowledgeFormIssue[];
  lockedIdentity?: boolean;
  disabled?: boolean;
  onChange?: (value: KnowledgeContent) => void;
}) {
  const [value, setValue] = useState(initial);
  return (
    <KnowledgeEditor
      value={value}
      onChange={(next) => {
        setValue(next);
        onChange?.(next);
      }}
      issues={issues}
      lockedIdentity={lockedIdentity}
      disabled={disabled}
    />
  );
}

function show(props: Parameters<typeof Harness>[0] = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <Harness {...props} />
    </QueryClientProvider>,
  );
  return client;
}

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
});

describe("validateKnowledgeDraft", () => {
  it("reports every missing required field in order", () => {
    const issues = validateKnowledgeDraft(emptyKnowledgeContent());
    expect(issues).toEqual([
      { id: "knowledge-id", message: "请填写知识 ID" },
      { id: "knowledge-version", message: "请填写新版本号" },
      { id: "knowledge-title", message: "请填写标题" },
      { id: "knowledge-summary", message: "请填写摘要" },
      { id: "knowledge-body", message: "请填写正文" },
      { id: "knowledge-source-reference", message: "请填写来源说明" },
      { id: "knowledge-line", message: "请填写业务线" },
      { id: "knowledge-scenario", message: "请填写业务场景" },
      { id: "knowledge-contract", message: "请填写合同类型" },
      { id: "knowledge-from", message: "请填写生效时间" },
      { id: "knowledge-until", message: "请填写失效时间" },
      { id: "knowledge-pack", message: "请选择已发布的活动规则包" },
      { id: "knowledge-diff", message: "请填写候选规则差异" },
      { id: "knowledge-evidence", message: "请填写验证依据" },
      { id: "knowledge-spans", message: "至少添加一条规则及对应正文引用" },
    ]);
    expect(validateKnowledgeDraft(draft())).toEqual([]);
  });

  it("treats whitespace as missing", () => {
    const issues = validateKnowledgeDraft(draft({ title: "   ", summary: "\n" }));
    expect(issues).toContainEqual({ id: "knowledge-title", message: "请填写标题" });
    expect(issues).toContainEqual({ id: "knowledge-summary", message: "请填写摘要" });
  });

  it("requires a later expiry and a consistent case scope", () => {
    expect(
      validateKnowledgeDraft(
        draft({ valid_from: "2027-01-01T00:00:00Z", valid_until: "2026-01-01T00:00:00Z" }),
      ),
    ).toContainEqual({ id: "knowledge-until", message: "失效时间必须晚于生效时间" });
    expect(
      validateKnowledgeDraft(
        draft({ scope: { ...draft().scope, case_id: "case-1" } }),
      ),
    ).toContainEqual({ id: "knowledge-org", message: "限定案件时需要填写所属组织 ID" });
    expect(
      validateKnowledgeDraft(
        draft({
          source_case_id: "case-1",
          scope: { ...draft().scope, org_id: "org-1", case_id: "case-2" },
        }),
      ),
    ).toContainEqual({
      id: "knowledge-source-case",
      message: "案件来源的非经验内容必须限定同一个案件",
    });
    expect(
      validateKnowledgeDraft(
        draft({
          source_case_id: "case-1",
          scope: { ...draft().scope, org_id: "org-1", case_id: "case-1" },
        }),
      ),
    ).toEqual([]);
  });

  it("checks each source span against the body by Unicode characters", () => {
    const body = `\u{20000}${quote}。`;
    const valid = draft({
      body,
      binding: { ...draft().binding, source_spans: [{ rule_id: "BW", start: 1, end: 16, quote }] },
    });
    expect(validateKnowledgeDraft(valid)).toEqual([]);
    expect(
      validateKnowledgeDraft({
        ...valid,
        binding: {
          ...valid.binding,
          source_spans: [
            { rule_id: "", start: 1, end: 16, quote },
            { rule_id: "BW", start: 0, end: 15, quote },
          ],
        },
      }),
    ).toEqual([
      { id: "span-rule-0", message: "请选择规则" },
      { id: "span-quote-1", message: "引用文字和起始位置必须与正文一致" },
    ]);
    expect(
      validateKnowledgeDraft({
        ...valid,
        binding: {
          ...valid.binding,
          source_spans: [{ rule_id: "BW", start: 0, end: 0, quote: "" }],
        },
      }),
    ).toContainEqual({ id: "span-quote-0", message: "引用文字和起始位置必须与正文一致" });
  });

  it("requires condition values and reports each empty item", () => {
    expect(
      validateKnowledgeDraft(
        draft({
          applicability: [{ field: "business_status", equals: "" }],
          exclusions: [
            { field: "org_id", equals: " " },
            { field: "case_id", equals: "ok" },
          ],
        }),
      ),
    ).toEqual([
      { id: "applicability-value-0", message: "请填写条件值" },
      { id: "exclusions-value-0", message: "请填写条件值" },
    ]);
  });

  it("requires the exception basis and organisation for exception knowledge", () => {
    const issues = validateKnowledgeDraft(
      draft({
        kind: "exception",
        scope: { ...draft().scope, org_id: "" },
        policy_refs: [policySummary.ref],
      }),
    );
    expect(issues).toContainEqual({ id: "knowledge-exception", message: "请填写例外批准依据" });
    expect(issues).toContainEqual({ id: "knowledge-org", message: "请填写限定组织 ID" });
    expect(issues).not.toContainEqual(
      expect.objectContaining({ id: "knowledge-policy-refs" }),
    );
  });

  it("requires reviewed experience fields, exclusions and policy references", () => {
    const issues = validateKnowledgeDraft(
      draft({
        kind: "experience",
        source_case_id: "case-1",
        scope: { ...draft().scope, case_id: "case-2" },
        policy_refs: [{ knowledge_id: "policy", version_id: "", content_sha256: "" }],
      }),
    );
    const ids = issues.map((issue) => issue.id);
    expect(ids).toEqual(
      expect.arrayContaining([
        "knowledge-candidate",
        "knowledge-candidate-version",
        "knowledge-candidate-hash",
        "knowledge-review-diff",
        "knowledge-reviewed-applicability",
        "knowledge-reviewed-exclusions",
        "knowledge-check-action",
        "knowledge-minimum-evidence",
        "knowledge-counterexamples",
        "policy-ref-0",
      ]),
    );
    expect(issues.find((issue) => issue.id === "policy-ref-0")?.message).toBe(
      "请完整填写政策 ID、精确版本 ID 和内容摘要",
    );
    expect(
      validateKnowledgeDraft(
        draft({
          kind: "experience",
          source_case_id: "case-1",
          candidate_id: "candidate-1",
          candidate_version_id: digest("1"),
          candidate_content_sha256: digest("2"),
          candidate_review_diff: "已审阅",
          reviewed_applicability: ["适用"],
          reviewed_exclusions: ["排除"],
          check_action: "核对",
          minimum_evidence: "合同",
          counterexamples: ["反例"],
          policy_refs: [policySummary.ref],
        }),
      ),
    ).toEqual([]);
  });

  it("requires policy references for experience and exception knowledge", () => {
    expect(validateKnowledgeDraft(draft({ kind: "exception", exception_basis: "批准", scope: { ...draft().scope, org_id: "org-1" } }))).toContainEqual({
      id: "knowledge-policy-refs",
      message: "请添加政策依赖",
    });
    expect(validateKnowledgeDraft(draft({ kind: "experience", policy_refs: [] }))).toContainEqual({
      id: "knowledge-policy-refs",
      message: "请添加政策依赖",
    });
  });
});

describe("KnowledgeEditor", () => {
  it("renders the documented sections and locks identity", async () => {
    installApi();
    show({ lockedIdentity: true, disabled: true });
    expect(await screen.findByRole("heading", { name: "知识内容与来源" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "适用范围与有效期" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "规则包绑定与正文依据" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "政策依赖" })).toBeInTheDocument();
    expect(screen.getByLabelText("知识 ID")).toHaveAttribute("readonly");
    expect(screen.getByLabelText("知识 ID")).toBeDisabled();
    expect(screen.getByLabelText("标题")).toBeDisabled();
    expect(screen.queryByRole("heading", { name: "例外批准依据" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "经验候选与人工审阅" })).not.toBeInTheDocument();
  });

  it("switches the source type with the knowledge kind", async () => {
    installApi();
    const user = userEvent.setup();
    show();
    await chooseOption(user, screen.getByLabelText("知识类型"), "审查经验");
    expect(await screen.findByRole("heading", { name: "经验候选与人工审阅" })).toBeInTheDocument();
    expect(screen.getByText("人工反馈")).toBeInTheDocument();
    await chooseOption(user, screen.getByLabelText("知识类型"), "政策制度");
    expect(screen.queryByRole("heading", { name: "经验候选与人工审阅" })).not.toBeInTheDocument();
    expect(screen.getByText("参考制度")).toBeInTheDocument();
  });

  it("edits basic fields and renders field-level issues", async () => {
    installApi();
    const changes: KnowledgeContent[] = [];
    show({
      issues: [
        { id: "knowledge-title", message: "请填写标题" },
        { id: "knowledge-spans", message: "至少添加一条规则及对应正文引用" },
        { id: "knowledge-policy-refs", message: "请添加政策依赖" },
      ],
      onChange: (value) => changes.push(value),
    });
    const title = screen.getByLabelText("标题");
    expect(title).toHaveAttribute("aria-invalid", "true");
    expect(title).toHaveAttribute("aria-describedby", "knowledge-title-error");
    expect(screen.getByText("请填写标题")).toBeInTheDocument();
    expect(screen.getAllByRole("alert").map((item) => item.textContent)).toEqual(
      expect.arrayContaining([
        "至少添加一条规则及对应正文引用",
        "请添加政策依赖",
      ]),
    );
    fireEvent.change(title, { target: { value: "新标题" } });
    expect(changes.at(-1)?.title).toBe("新标题");
    fireEvent.change(screen.getByLabelText("来源案件 ID（如有）"), {
      target: { value: "case-9" },
    });
    expect(changes.at(-1)?.source_case_id).toBe("case-9");
    fireEvent.change(screen.getByLabelText("摘要"), { target: { value: "新摘要" } });
    expect(changes.at(-1)?.summary).toBe("新摘要");
    fireEvent.change(screen.getByLabelText("正文"), { target: { value: "新正文" } });
    expect(changes.at(-1)?.body).toBe("新正文");
    fireEvent.change(screen.getByLabelText("来源说明"), { target: { value: "新来源" } });
    expect(changes.at(-1)?.source_reference).toBe("新来源");
  });

  it("edits scope and date fields through their local conversions", async () => {
    installApi();
    const changes: KnowledgeContent[] = [];
    show({ onChange: (value) => changes.push(value) });
    fireEvent.change(screen.getByLabelText("业务线"), { target: { value: "EB" } });
    expect(changes.at(-1)?.scope.business_line).toBe("EB");
    fireEvent.change(screen.getByLabelText("业务场景"), { target: { value: "cloud" } });
    expect(changes.at(-1)?.scope.scenario).toBe("cloud");
    fireEvent.change(screen.getByLabelText("合同类型"), { target: { value: "purchase" } });
    expect(changes.at(-1)?.scope.contract_type).toBe("purchase");
    fireEvent.change(screen.getByLabelText("限定组织 ID（可选）"), { target: { value: "org-1" } });
    expect(changes.at(-1)?.scope.org_id).toBe("org-1");
    fireEvent.change(screen.getByLabelText("限定案件 ID（可选）"), { target: { value: "case-1" } });
    expect(changes.at(-1)?.scope.case_id).toBe("case-1");
  });

  it("adds, edits and removes applicability and exclusion conditions", async () => {
    installApi();
    const user = userEvent.setup();
    const changes: KnowledgeContent[] = [];
    show({
      initial: draft({
        applicability: [{ field: "business_status", equals: "active" }],
        exclusions: [],
      }),
      onChange: (value) => changes.push(value),
    });
    fireEvent.change(screen.getByLabelText("等于"), { target: { value: "closed" } });
    expect(changes.at(-1)?.applicability?.[0]).toEqual({
      field: "business_status",
      equals: "closed",
    });
    await chooseOption(user, screen.getByLabelText("条件 1 字段"), "组织 ID");
    expect(changes.at(-1)?.applicability?.[0]?.field).toBe("org_id");
    await user.click(screen.getByRole("button", { name: "添加排除条件" }));
    expect(changes.at(-1)?.exclusions).toEqual([
      { field: "business_status", equals: "" },
    ]);
    fireEvent.change(screen.getByLabelText("等于", { selector: "#exclusions-value-0" }), {
      target: { value: "draft" },
    });
    expect(changes.at(-1)?.exclusions?.[0]?.equals).toBe("draft");
    await user.click(screen.getAllByRole("button", { name: "删除条件 1" })[0]!);
    expect(changes.at(-1)?.applicability).toEqual([]);
  });

  it("binds a published active pack and resets rule references", async () => {
    installApi();
    const user = userEvent.setup();
    const changes: KnowledgeContent[] = [];
    show({ onChange: (value) => changes.push(value) });
    await screen.findByText(/规则包有效期/);
    expect(screen.queryByText(/draft-pack/)).not.toBeInTheDocument();
    await chooseOption(
      user,
      screen.getByLabelText("已发布并生效的规则包"),
      "pack2 · 2",
    );
    expect(changes.at(-1)?.binding).toMatchObject({
      pack_id: "pack2",
      pack_version: "2",
      pack_content_hash: digest("e"),
      pack_snapshot_digest: digest("f"),
      compiler_ref: "governed-rulepack/v1",
      rule_ids: [],
      source_spans: [],
    });
  });

  it("keeps an original pack binding visible as an option", async () => {
    installApi();
    show({
      initial: draft({
        binding: { ...draft().binding, pack_id: "legacy", pack_version: "0" },
      }),
    });
    expect(await screen.findByText("legacy · 0（原绑定）")).toBeInTheDocument();
  });

  it("adds spans, locates quotes and updates their offsets", async () => {
    installApi();
    const user = userEvent.setup();
    const changes: KnowledgeContent[] = [];
    show({
      initial: draft({
        binding: { ...draft().binding, source_spans: [], rule_ids: [] },
      }),
      onChange: (value) => changes.push(value),
    });
    await screen.findByText(/规则包有效期/);
    await user.click(screen.getByRole("button", { name: "添加规则引用" }));
    expect(changes.at(-1)?.binding.source_spans).toEqual([
      { rule_id: "", start: 0, end: 0, quote: "" },
    ]);
    await chooseOption(user, screen.getByLabelText("引用 1 的规则"), "BW · dedicated_line.minimum_bandwidth · ENT / line / service");
    expect(changes.at(-1)?.binding.rule_ids).toEqual(["BW"]);
    fireEvent.change(screen.getByLabelText("正文中的原文"), {
      target: { value: quote },
    });
    expect(changes.at(-1)?.binding.source_spans?.[0]).toEqual({
      rule_id: "BW",
      start: 0,
      end: 15,
      quote,
    });
    fireEvent.change(screen.getByLabelText("起始字符位置（从 0 开始）"), {
      target: { value: "16" },
    });
    expect(changes.at(-1)?.binding.source_spans?.[0]).toMatchObject({
      start: 16,
      end: 31,
    });
    fireEvent.change(screen.getByLabelText("正文中的原文"), {
      target: { value: "不存在的原文" },
    });
    expect(changes.at(-1)?.binding.source_spans?.[0]).toMatchObject({
      start: 16,
      end: 22,
    });
    await user.click(screen.getByRole("button", { name: "删除引用 1" }));
    expect(changes.at(-1)?.binding.source_spans).toEqual([]);
    expect(changes.at(-1)?.binding.rule_ids).toEqual([]);
  });

  it("keeps existing rule ids selectable even without a pack detail", async () => {
    installApi();
    show({
      initial: draft({
        binding: {
          ...draft().binding,
          source_spans: [{ rule_id: "CUSTOM", start: 0, end: 15, quote }],
        },
      }),
    });
    expect(await screen.findByText("CUSTOM")).toBeInTheDocument();
  });

  it("edits binding notes and retries failed rulepack reads", async () => {
    let failures = 2;
    const { calls } = installApi((path) => {
      if (path === "/api/admin/rulepacks" && failures > 0) {
        failures -= 1;
        return json(
          { error: { code: "unavailable", message: "规则包不可用", retryable: true, details: "稍后重试" } },
          503,
        );
      }
      return null;
    });
    const user = userEvent.setup();
    const changes: KnowledgeContent[] = [];
    show({ onChange: (value) => changes.push(value) });
    expect(await screen.findByText(/规则包列表：规则包不可用/)).toBeInTheDocument();
    expect(screen.getByText(/unavailable/)).toBeInTheDocument();
    expect(screen.getByText(/稍后重试/)).toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "重试" })[0]!);
    await waitFor(() => expect(calls.filter((path) => path === "/api/admin/rulepacks")).toHaveLength(2));
    fireEvent.change(screen.getByLabelText("候选规则差异说明"), {
      target: { value: "新的差异" },
    });
    expect(changes.at(-1)?.binding.candidate_diff).toBe("新的差异");
    fireEvent.change(screen.getByLabelText("验证依据"), {
      target: { value: "新的依据" },
    });
    expect(changes.at(-1)?.binding.validation_evidence).toBe("新的依据");
  });

  it("formats object error details and retries a failed pack detail", async () => {
    let attempts = 0;
    const { calls } = installApi((path) => {
      if (path === "/api/admin/rulepacks/pack/versions/1") {
        attempts += 1;
        if (attempts === 1) {
          return json(
            { error: { code: "invalid_response", message: "详情损坏", retryable: false, details: { field: "rules" } } },
            500,
          );
        }
      }
      return null;
    });
    const user = userEvent.setup();
    show();
    expect(await screen.findByText(/规则包详情：详情损坏/)).toBeInTheDocument();
    expect(screen.getByText(/"field":"rules"/)).toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "重试" })[0]!);
    await waitFor(() =>
      expect(calls.filter((path) => path === "/api/admin/rulepacks/pack/versions/1")).toHaveLength(2),
    );
  });

  it("edits exception knowledge", async () => {
    installApi();
    const changes: KnowledgeContent[] = [];
    show({
      initial: draft({ kind: "exception", scope: { ...draft().scope, org_id: "org-1" } }),
      onChange: (value) => changes.push(value),
    });
    expect(screen.getByRole("heading", { name: "例外批准依据" })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("批准依据"), { target: { value: "特批" } });
    expect(changes.at(-1)?.exception_basis).toBe("特批");
  });

  it("edits experience candidate review fields line by line", async () => {
    installApi();
    const changes: KnowledgeContent[] = [];
    show({
      initial: draft({
        kind: "experience",
        source_type: "human_feedback",
        reviewed_applicability: ["旧适用"],
        counterexamples: ["旧反例"],
      }),
      onChange: (value) => changes.push(value),
    });
    expect(screen.getByRole("heading", { name: "经验候选与人工审阅" })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("候选 ID"), { target: { value: "candidate-9" } });
    expect(changes.at(-1)?.candidate_id).toBe("candidate-9");
    fireEvent.change(screen.getByLabelText("候选版本 ID"), { target: { value: "v9" } });
    expect(changes.at(-1)?.candidate_version_id).toBe("v9");
    fireEvent.change(screen.getByLabelText("候选内容 SHA-256"), { target: { value: "hash" } });
    expect(changes.at(-1)?.candidate_content_sha256).toBe("hash");
    fireEvent.change(screen.getByLabelText("候选审阅差异"), { target: { value: "审阅" } });
    expect(changes.at(-1)?.candidate_review_diff).toBe("审阅");
    fireEvent.change(screen.getByLabelText("审阅后的适用条件（每行一项）"), {
      target: { value: "a\nb" },
    });
    expect(changes.at(-1)?.reviewed_applicability).toEqual(["a", "b"]);
    fireEvent.change(screen.getByLabelText("审阅后的排除条件（每行一项）"), {
      target: { value: "c\n\nd" },
    });
    expect(changes.at(-1)?.reviewed_exclusions).toEqual(["c", "", "d"]);
    fireEvent.change(screen.getByLabelText("检查动作"), { target: { value: "核对" } });
    expect(changes.at(-1)?.check_action).toBe("核对");
    fireEvent.change(screen.getByLabelText("最低证据"), { target: { value: "合同" } });
    expect(changes.at(-1)?.minimum_evidence).toBe("合同");
    fireEvent.change(screen.getByLabelText("反例（每行一项）"), {
      target: { value: "x\ny" },
    });
    expect(changes.at(-1)?.counterexamples).toEqual(["x", "y"]);
  });

  it("adds, deduplicates, edits and removes policy references", async () => {
    installApi();
    const user = userEvent.setup();
    const changes: KnowledgeContent[] = [];
    show({
      initial: draft({
        kind: "exception",
        scope: { ...draft().scope, org_id: "org-1" },
        policy_refs: [],
      }),
      onChange: (value) => changes.push(value),
    });
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: /从可访问的生效政策添加/ }),
      "基础政策 · policy v3",
    );
    expect(changes.at(-1)?.policy_refs).toEqual([policySummary.ref]);
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: /从可访问的生效政策添加/ }),
      "基础政策 · policy v3",
    );
    expect(changes.at(-1)?.policy_refs).toHaveLength(1);
    fireEvent.change(screen.getByLabelText("政策 1 ID"), {
      target: { value: "policy-2" },
    });
    expect(changes.at(-1)?.policy_refs?.[0]?.knowledge_id).toBe("policy-2");
    fireEvent.change(screen.getByLabelText("精确版本 ID"), {
      target: { value: "v9" },
    });
    expect(changes.at(-1)?.policy_refs?.[0]?.version_id).toBe("v9");
    fireEvent.change(screen.getByLabelText("内容 SHA-256"), {
      target: { value: "hash" },
    });
    expect(changes.at(-1)?.policy_refs?.[0]?.content_sha256).toBe("hash");
    await user.click(screen.getByRole("button", { name: "手动添加政策依赖" }));
    expect(changes.at(-1)?.policy_refs).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "删除政策 1" }));
    expect(changes.at(-1)?.policy_refs).toHaveLength(1);
  });

  it("shows and retries a failed policy list", async () => {
    let failures = 1;
    const { calls } = installApi((path) => {
      if (path === "/api/admin/governed-knowledge" && failures > 0) {
        failures -= 1;
        return json(
          { error: { code: "unavailable", message: "政策不可用", retryable: true } },
          503,
        );
      }
      return null;
    });
    const user = userEvent.setup();
    show();
    expect(await screen.findByText(/政策列表：政策不可用/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() =>
      expect(calls.filter((path) => path === "/api/admin/governed-knowledge")).toHaveLength(2),
    );
  });
});
