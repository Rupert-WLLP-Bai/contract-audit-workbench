import { useState } from "react";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createEmptyRulePackDraft, RulePackManifestEditor } from "./rulepack-manifest-editor";
import { RulePackTestPanel } from "./rulepack-test-panel";
import type { RulePackManifestInput, RulePackManifest, RulePackTestResponse } from "../api/rulepacks";

const clients: QueryClient[] = [];

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
});

function ControlledManifestEditor() {
  const [value, setValue] = useState<RulePackManifestInput>(() => createEmptyRulePackDraft("tenant-test"));
  return <RulePackManifestEditor value={value} tenantId="tenant-test" onChange={setValue} />;
}

const manifest: RulePackManifest = {
  schema_version: "rulepack/v1",
  id: "telecom-core",
  version: "1",
  tenant_id: "tenant-test",
  effective_from: "2026-10-01T00:00:00Z",
  effective_until: "2027-01-01T00:00:00Z",
  rules: [{
    rule_id: "LINE-BW-001",
    version: "1",
    domain: "technical",
    capability_id: "dedicated_line.minimum_bandwidth",
    mandatory: true,
    business_scope: { business_line: "ENT", scenario: "telecom_dedicated_line", contract_type: "service" },
    tenant_id: "tenant-test",
    comparator: "minimum_bandwidth",
    threshold: { value: "100", unit: "Mbit/s" },
    required_fact: { dimension: "bandwidth", source_roles: ["procurement"], object_required: true, unit_family: "bandwidth", exclusions: [] },
    authority_sources: [{ id: "internal-policy", version: "1", kind: "policy" }],
  }],
  lifecycle: { state: "draft" },
};

describe("RulePack editable row focus", () => {
  it("uses null outside SLA and an explicit empty array for monthly SLA", async () => {
    const user = userEvent.setup();
    const changes: RulePackManifestInput[] = [];
    function Editor() {
      const [value, setValue] = useState(() => createEmptyRulePackDraft("tenant-test"));
      return <RulePackManifestEditor value={value} tenantId="tenant-test" onChange={(next) => { changes.push(next); setValue(next); }} />;
    }
    render(<Editor />);
    expect(createEmptyRulePackDraft("tenant-test").rules[0]?.required_fact.exclusions).toBeNull();
    expect(screen.getByLabelText("排除项（每行一项）")).toBeDisabled();
    await user.selectOptions(screen.getByLabelText("比较器"), "monthly_sla");
    expect(changes.at(-1)?.rules[0]?.required_fact.exclusions).toEqual([]);
    expect(screen.getByLabelText("排除项（每行一项）")).toBeEnabled();
    await user.selectOptions(screen.getByLabelText("比较器"), "advance_payment_ceiling");
    expect(changes.at(-1)?.rules[0]?.required_fact.exclusions).toBeNull();
  });

  it("keeps rule and authority inputs focused while their values change", async () => {
    const user = userEvent.setup();
    render(<ControlledManifestEditor />);

    await user.type(screen.getByLabelText("规则 ID"), "RULE-A");
    const ruleId = screen.getByLabelText("规则 ID");
    expect(ruleId).toHaveValue("RULE-A");
    expect(document.activeElement).toBe(ruleId);

    await user.type(screen.getByLabelText("来源 ID"), "policy-A");
    const sourceId = screen.getByLabelText("来源 ID");
    expect(sourceId).toHaveValue("policy-A");
    expect(document.activeElement).toBe(sourceId);
  });

  it("keeps sample source and fact inputs focused while their IDs change", async () => {
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
    clients.push(client);
    render(
      <QueryClientProvider client={client}>
        <RulePackTestPanel packId={manifest.id} version={manifest.version} manifest={manifest} revision={1} />
      </QueryClientProvider>,
    );

    await user.click(screen.getByRole("button", { name: "添加样本材料" }));
    await user.clear(screen.getByLabelText("样本材料 ID"));
    await user.type(screen.getByLabelText("样本材料 ID"), "source-alpha");
    const sourceId = screen.getByLabelText("样本材料 ID");
    expect(sourceId).toHaveValue("source-alpha");
    expect(document.activeElement).toBe(sourceId);

    await user.click(screen.getByRole("button", { name: "添加事实" }));
    await user.clear(screen.getByLabelText("事实 ID"));
    await user.type(screen.getByLabelText("事实 ID"), "fact-alpha");
    const factId = screen.getByLabelText("事实 ID");
    expect(factId).toHaveValue("fact-alpha");
    expect(document.activeElement).toBe(factId);
  });

  it("moves focus to a local sample validation error", async () => {
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
    clients.push(client);
    render(
      <QueryClientProvider client={client}>
        <RulePackTestPanel packId={manifest.id} version={manifest.version} manifest={manifest} revision={1} />
      </QueryClientProvider>,
    );

    await user.click(screen.getByRole("button", { name: "添加样本材料" }));
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));

    const error = screen.getByRole("alert");
    expect(error).toHaveTextContent("每份都必须填写 ID、版本、角色和文本");
    expect(document.activeElement).toBe(error);
  });
});


function renderManifest(initial: RulePackManifestInput | RulePackManifest, options: { lockedIdentity?: boolean; disabled?: boolean } = {}) {
  const changes: RulePackManifestInput[] = [];
  function Editor() {
    const { lifecycle: _lifecycle, ...content } = initial;
    const [value, setValue] = useState<RulePackManifestInput>(() => structuredClone(content));
    return <RulePackManifestEditor value={value} tenantId="tenant-test" {...options} onChange={(next) => { changes.push(next); setValue(next); }} />;
  }
  render(<Editor />);
  return changes;
}

function inputValue(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label, { exact: true }), { target: { value } });
}

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

const report: RulePackTestResponse = {
  revision: 6,
  content_hash: "c".repeat(64),
  report: {
    pack_id: "telecom-core", pack_version: "1", pack_hash: "c".repeat(64),
    relations: ["compatible", "conflict", "insufficient", "not_applicable"],
    results: [
      {
        rule_id: "LINE-BW-001", rule_version: "1", domain: "technical", capability_id: "dedicated_line.minimum_bandwidth", mandatory: true,
        relation: "compatible", reason_code: "verified_bandwidth", fact_ids: ["fact-bandwidth"],
        citations: [{ source_id: "contract-A", source_version: "3", tenant_id: "tenant-test", case_id: "sample-case", source_digest: "d".repeat(64), start: 1, end: 6, quote: "带宽100" }],
      },
      { rule_id: "SLA", rule_version: "1", domain: "technical", capability_id: "availability", mandatory: true, relation: "conflict", reason_code: "below_threshold", fact_ids: ["fact-sla"] },
      { rule_id: "ADVANCE", rule_version: "1", domain: "payment", capability_id: "advance_ratio", mandatory: true, relation: "insufficient", reason_code: "missing_evidence", fact_ids: [] },
      { rule_id: "OPTIONAL", rule_version: "1", domain: "technical", capability_id: "optional_scope", mandatory: false, relation: "not_applicable", reason_code: "scope_excluded" },
    ],
  },
};

function renderPanel(response: () => Response | Promise<Response> = () => json(report), inputManifest = manifest) {
  const requests: { path: string; init?: RequestInit }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    requests.push({ path: String(input), init });
    return response();
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><RulePackTestPanel packId={inputManifest.id} version={inputManifest.version} manifest={inputManifest} revision={6} /></QueryClientProvider>);
  return requests;
}

async function addSource(user: ReturnType<typeof userEvent.setup>, text = "🚀带宽100 Mbit/s") {
  await user.click(screen.getByRole("button", { name: "添加样本材料" }));
  inputValue("样本材料 ID", "contract-A");
  inputValue("材料版本", "3");
  inputValue("材料角色", "procurement");
  inputValue("样本文本", text);
}

function selectSource(start = 2, end = 7) {
  const text = screen.getByLabelText<HTMLTextAreaElement>("样本文本");
  text.focus();
  text.setSelectionRange(start, end);
  fireEvent.select(text);
}

async function addCitedFact(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "添加事实" }));
  inputValue("事实 ID", "fact-bandwidth");
  inputValue("事实维度", "bandwidth");
  inputValue("业务对象", "LINE-7");
  inputValue("事实值", "00100.00");
  inputValue("单位", "Mbit/s");
  selectSource();
  await user.click(screen.getByRole("button", { name: "添加当前选区引用" }));
}

describe("RulePackManifestEditor structured edits", () => {
  it("keeps literal fields and independent nested updates intact", () => {
    const changes = renderManifest(manifest);
    inputValue("比较阈值", "{not JSON}");
    inputValue("计算基数（可选）", "contract_total");
    inputValue("测量窗口（可选）", "calendar_month");
    inputValue("时区（可选）", "Asia/Shanghai");
    inputValue("日历（可选）", "gregorian");
    inputValue("故障口径（可选）", "customer_visible");
    inputValue("事实计算基数（可选）", "gross_amount");
    inputValue("允许材料角色（每行一项）", " procurement \n\n contract \n");
    fireEvent.click(screen.getByLabelText("要求精确业务对象"));
    const rule = changes.at(-1)!.rules[0]!;
    expect(rule.threshold).toEqual({ value: "{not JSON}", unit: "Mbit/s", basis: "contract_total" });
    expect(rule.required_fact).toEqual({
      dimension: "bandwidth", source_roles: ["procurement", "contract"], object_required: false, unit_family: "bandwidth", exclusions: [],
      measurement_window: "calendar_month", time_zone: "Asia/Shanghai", calendar: "gregorian", outage_definition: "customer_visible", basis: "gross_amount",
    });
    expect(rule.authority_sources).toEqual([{ id: "internal-policy", version: "1", kind: "policy" }]);
    for (const label of ["计算基数（可选）", "测量窗口（可选）", "时区（可选）", "日历（可选）", "故障口径（可选）", "事实计算基数（可选）"]) inputValue(label, "");
    expect(changes.at(-1)!.rules[0]!.threshold.basis).toBeUndefined();
    expect(changes.at(-1)!.rules[0]!.required_fact).toMatchObject({ measurement_window: undefined, time_zone: undefined, calendar: undefined, outage_definition: undefined, basis: undefined });
  });

  it("protects core capabilities while allowing optional literal capability names", async () => {
    const user = userEvent.setup();
    const changes = renderManifest(manifest);
    expect(screen.getByLabelText(/此项为必检规则/)).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "添加规则" }));
    expect(screen.queryByRole("button", { name: "移除规则 1" })).not.toBeInTheDocument();
    const second = within(screen.getByRole("group", { name: "规则 2" }));
    fireEvent.change(second.getByLabelText("能力 ID"), { target: { value: "__proto__" } });
    await user.click(second.getByLabelText(/此项为必检规则/));
    expect(changes.at(-1)!.rules[1]).toMatchObject({ capability_id: "__proto__", mandatory: false });
    await user.click(second.getByRole("button", { name: "移除规则 2" }));
    expect(screen.getAllByLabelText("规则 ID")).toHaveLength(1);
    expect(changes.at(-1)!.rules[0]!.rule_id).toBe("LINE-BW-001");
  });

  it("removes a middle rule and edits the remaining authority without crossing rows", async () => {
    const user = userEvent.setup();
    const initial: RulePackManifestInput = {
      ...manifest, lifecycle: { state: "draft" },
      rules: ["optional-A", "optional-B", "optional-C"].map((id) => ({
        ...structuredClone(manifest.rules[0]!), rule_id: id, capability_id: id,
        authority_sources: [{ id: `${id}-policy`, version: "1", kind: "policy" }],
      })),
    };
    const changes = renderManifest(initial);
    await user.click(screen.getByRole("button", { name: "移除规则 2" }));
    const remaining = within(screen.getByRole("group", { name: "规则 2 · optional-C" }));
    await user.click(remaining.getByRole("button", { name: "添加来源" }));
    fireEvent.change(remaining.getAllByLabelText("来源 ID")[1]!, { target: { value: "replacement-policy" } });
    fireEvent.change(remaining.getAllByLabelText("来源版本")[1]!, { target: { value: "9" } });
    fireEvent.change(remaining.getAllByLabelText("来源类型")[1]!, { target: { value: "regulation" } });
    await user.click(remaining.getByRole("button", { name: "移除规则 2 的来源 1" }));
    expect(changes.at(-1)!.rules.map((rule) => rule.rule_id)).toEqual(["optional-A", "optional-C"]);
    expect(changes.at(-1)!.rules[0]!.authority_sources).toEqual([{ id: "optional-A-policy", version: "1", kind: "policy" }]);
    expect(changes.at(-1)!.rules[1]!.authority_sources).toEqual([{ id: "replacement-policy", version: "9", kind: "regulation" }]);
  });

  it("normalizes local dates and clears optional SLA fields without losing other requirements", () => {
    const changes = renderManifest(manifest, { lockedIdentity: true });
    expect(screen.getByLabelText("规则包 ID")).toHaveAttribute("readonly");
    expect(screen.getByLabelText("版本", { exact: true })).toHaveAttribute("readonly");
    inputValue("生效时间（本地时区）", "2026-11-03T08:45");
    expect(changes.at(-1)!.effective_from).toBe(new Date(2026, 10, 3, 8, 45).toISOString());
    inputValue("失效时间（本地时区，结束时刻不含）", "");
    expect(changes.at(-1)!.effective_until).toBe("");
    inputValue("阈值单位", "");
    inputValue("比较器", "monthly_sla");
    inputValue("排除项（每行一项）", " planned maintenance \n\n force majeure ");
    expect(changes.at(-1)!.rules[0]!.threshold.unit).toBe("percent");
    expect(changes.at(-1)!.rules[0]!.required_fact.exclusions).toEqual(["planned maintenance", "force majeure"]);
    inputValue("阈值单位", "");
    inputValue("比较器", "advance_payment_ceiling");
    expect(changes.at(-1)!.rules[0]!.threshold.unit).toBe("ratio");
    expect(changes.at(-1)!.rules[0]!.required_fact.exclusions).toBeNull();
  });
});

describe("RulePackTestPanel consumer behavior", () => {
  it("submits code point citations and literal facts, then renders all result relations and provenance", async () => {
    const user = userEvent.setup();
    const requests = renderPanel();
    await addSource(user);
    await addCitedFact(user);
    inputValue("测量窗口（可选）", "calendar_month");
    inputValue("时区（可选）", "Asia/Shanghai");
    inputValue("日历（可选）", "gregorian");
    inputValue("故障口径（可选）", "service_outage");
    inputValue("计算基数（可选）", "contract_total");
    inputValue("排除项（每行一项）", " planned \n\n external ");
    await user.selectOptions(screen.getByLabelText("适用性"), "no");
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(await screen.findByRole("heading", { name: "测试结果" })).toBeInTheDocument();
    expect(requests).toHaveLength(1);
    expect(requests[0]?.path).toBe("/api/admin/rulepacks/telecom-core/versions/1/test");
    expect(requests[0]?.init?.method).toBe("POST");
    expect(JSON.parse(String(requests[0]?.init?.body))).toEqual({
      expected_revision: 6, business_line: "ENT", scenario: "telecom_dedicated_line", contract_type: "service", at: "2026-10-01T00:00:00Z",
      sources: [{ id: "contract-A", version: "3", role: "procurement", text: "🚀带宽100 Mbit/s" }],
      facts: [{
        id: "fact-bandwidth", rule_id: "LINE-BW-001", dimension: "bandwidth", object_ref: "LINE-7", value: "00100.00", unit: "Mbit/s", source_role: "procurement",
        measurement_window: "calendar_month", time_zone: "Asia/Shanghai", calendar: "gregorian", outage_definition: "service_outage", basis: "contract_total", exclusions: ["planned", "external"], applicable: false,
        citations: [{ source_id: "contract-A", source_version: "3", start: 1, end: 6, quote: "带宽100" }],
      }],
    });
    const results = screen.getByRole("region", { name: "测试结果" });
    expect(results).toHaveTextContent("总关系：符合、冲突、缺证 / 不可比较、不适用");
    expect(results).toHaveTextContent("fact-bandwidth");
    expect(results).toHaveTextContent("verified_bandwidth");
    expect(results).toHaveTextContent("码点 [1,6)");
    expect(results).toHaveTextContent("带宽100");
    expect(results).toHaveTextContent("sample-case");
    expect(results).toHaveTextContent("d".repeat(64));
  });

  it.each([
    { label: "测试时点（本地时区）", value: "2026-09-30T00:00", message: /有效期内/ },
    { label: "测试时点（本地时区）", value: "2027-01-02T00:00", message: /有效期内/ },
    { label: "测试时点（本地时区）", value: "", message: /业务范围和有效测试时间/ },
    { label: "业务线", value: " ", message: /业务范围和有效测试时间/ },
    { label: "场景", value: "", message: /业务范围和有效测试时间/ },
    { label: "合同类型", value: "", message: /业务范围和有效测试时间/ },
  ])("blocks malformed sample scope or time: $label / $value", async ({ label, value, message }) => {
    const requests = renderPanel();
    inputValue(label, value);
    await userEvent.setup().click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(screen.getByRole("alert")).toHaveTextContent(message);
    expect(screen.getByRole("alert")).toHaveFocus();
    expect(requests).toHaveLength(0);
  });

  it("rejects a duplicate source identity but permits different versions", async () => {
    const user = userEvent.setup();
    const requests = renderPanel();
    await addSource(user);
    await user.click(screen.getByRole("button", { name: "添加样本材料" }));
    const second = within(screen.getByRole("group", { name: "样本材料 2" }));
    for (const [label, value] of [["样本材料 ID", "contract-A"], ["材料版本", "3"], ["材料角色", "procurement"], ["样本文本", "另一个版本"]]) {
      fireEvent.change(second.getByLabelText(label!), { target: { value } });
    }
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(screen.getByRole("alert")).toHaveTextContent(/ID 与版本组合不能重复/);
    expect(requests).toHaveLength(0);
    fireEvent.change(second.getByLabelText("材料版本"), { target: { value: "4" } });
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(await screen.findByRole("heading", { name: "测试结果" })).toBeInTheDocument();
    expect(JSON.parse(String(requests[0]?.init?.body)).sources.map((source: { version: string }) => source.version)).toEqual(["3", "4"]);
  });

  it("requires cited facts and detects citations invalidated by source edits", async () => {
    const user = userEvent.setup();
    const requests = renderPanel();
    await addSource(user);
    await addCitedFact(user);
    await user.click(screen.getByRole("button", { name: "移除事实 1 的引用 1" }));
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(screen.getByRole("alert")).toHaveTextContent(/1–8 条来源引用/);
    expect(requests).toHaveLength(0);
    selectSource();
    await user.click(screen.getByRole("button", { name: "添加当前选区引用" }));
    inputValue("样本文本", "🚀合同只承诺50 Mbit/s");
    expect(screen.getByRole("button", { name: "添加当前选区引用" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(screen.getByRole("alert")).toHaveTextContent(/引用与样本文本的码点范围不一致/);
    expect(requests).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "移除事实 1" }));
    await user.click(screen.getByRole("button", { name: "移除材料 1" }));
    expect(screen.queryByLabelText("事实 ID")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("样本材料 ID")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(await screen.findByRole("heading", { name: "测试结果" })).toBeInTheDocument();
    expect(JSON.parse(String(requests[0]?.init?.body))).toMatchObject({ sources: [], facts: [] });
  });

  it("measures source limits in UTF-8 bytes before sending sample data", async () => {
    const user = userEvent.setup();
    const requests = renderPanel();
    await addSource(user, "中".repeat(11_000));
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(screen.getByRole("alert")).toHaveTextContent(/单份 32 KiB/);
    expect(requests).toHaveLength(0);
  });

  it("disables sample submission while pending and preserves inputs on a revision conflict", async () => {
    let resolve!: (response: Response) => void;
    const pending = new Promise<Response>((complete) => { resolve = complete; });
    const user = userEvent.setup();
    renderPanel(() => pending);
    await addSource(user);
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "正在测试样本" })).toBeDisabled());
    expect(screen.getByLabelText("样本文本")).toBeDisabled();
    resolve(json({ error: { code: "conflict", message: "Revision changed", retryable: false } }, 409));
    const error = await screen.findByRole("alert");
    expect(error).toHaveTextContent("Revision changed");
    expect(error).toHaveFocus();
    expect(screen.getByLabelText("样本文本")).toHaveValue("🚀带宽100 Mbit/s");
    expect(screen.getByRole("button", { name: "测试当前修订" })).toBeEnabled();
  });

  it("removes an earlier report when a subsequent sample fails local validation", async () => {
    const user = userEvent.setup();
    const requests = renderPanel();
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    await screen.findByRole("heading", { name: "测试结果" });
    await user.click(screen.getByRole("button", { name: "添加事实" }));
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "测试结果" })).not.toBeInTheDocument();
    expect(requests).toHaveLength(1);
  });

  it("shows no applicable rules for an empty report and prevents facts without rules", async () => {
    const user = userEvent.setup();
    renderPanel(() => json({ ...report, report: { ...report.report, relations: [], results: [] } }), { ...manifest, rules: [] });
    expect(screen.getByRole("button", { name: "添加事实" })).toBeDisabled();
    inputValue("业务线", "ENT");
    inputValue("场景", "telecom_dedicated_line");
    inputValue("合同类型", "service");
    inputValue("测试时点（本地时区）", "2026-10-15T12:00");
    await addSource(user);
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    const results = await screen.findByRole("region", { name: "测试结果" });
    expect(results).toHaveTextContent("没有适用规则");
    expect(results).toHaveTextContent("当前业务范围没有匹配规则");
  });

  it("rejects malformed report relations without rendering a misleading result", async () => {
    renderPanel(() => json({ ...report, report: { ...report.report, relations: ["unknown_relation"] } }));
    await userEvent.setup().click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(await screen.findByRole("alert")).toHaveFocus();
    expect(screen.queryByRole("heading", { name: "测试结果" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "测试当前修订" })).toBeEnabled();
  });
});


describe("RulePack editor literal values and fact selection", () => {
  it.each(["{malformed JSON", "{\"value\":\"0.3000\"}", "000100.000"])("preserves a threshold as a literal string: %s", (literal) => {
    const changes = renderManifest(manifest);
    inputValue("比较阈值", literal);
    inputValue("检查域", "payment");
    expect(changes.at(-1)!.rules[0]!.threshold).toEqual({ value: literal, unit: "Mbit/s" });
    expect(screen.getByLabelText("比较阈值")).toHaveValue(literal);
  });

  it("retargets a fact to another rule and sends the selected source version", async () => {
    const user = userEvent.setup();
    const inputManifest: RulePackManifest = {
      ...manifest,
      rules: [manifest.rules[0]!, {
        ...structuredClone(manifest.rules[0]!), rule_id: "LINE-SLA-002", capability_id: "dedicated_line.monthly_availability", comparator: "monthly_sla",
        threshold: { value: "99.95", unit: "percent" },
        required_fact: { ...manifest.rules[0]!.required_fact, dimension: "availability", source_roles: ["contract"], unit_family: "ratio", exclusions: [] },
      }],
    };
    const requests = renderPanel(undefined, inputManifest);
    await addSource(user);
    await user.click(screen.getByRole("button", { name: "添加样本材料" }));
    const sourceFields = within(screen.getByRole("group", { name: "样本材料 2" }));
    for (const [label, value] of [["样本材料 ID", "contract-A"], ["材料版本", "4"], ["材料角色", "contract"], ["样本文本", "月可用率99.95%"]]) {
      fireEvent.change(sourceFields.getByLabelText(label!), { target: { value } });
    }
    await user.click(screen.getByRole("button", { name: "添加事实" }));
    await user.selectOptions(screen.getByLabelText("对应规则"), "LINE-SLA-002");
    inputValue("事实 ID", "fact-sla");
    inputValue("事实值", "99.95");
    expect(screen.getByLabelText("事实维度")).toHaveValue("availability");
    expect(screen.getByLabelText("单位")).toHaveValue("percent");
    expect(screen.getByLabelText("材料角色", { selector: "select" })).toHaveValue("contract");
    expect(screen.queryByRole("option", { name: "contract-A v3" })).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("引用材料"), JSON.stringify(["contract-A", "4"]));
    const text = sourceFields.getByLabelText<HTMLTextAreaElement>("样本文本");
    text.focus();
    text.setSelectionRange(0, 10);
    fireEvent.select(text);
    await user.click(screen.getByRole("button", { name: "添加当前选区引用" }));
    for (const label of ["测量窗口（可选）", "时区（可选）", "日历（可选）", "故障口径（可选）", "计算基数（可选）"]) {
      inputValue(label, "temporary");
      inputValue(label, "");
    }
    await user.selectOptions(screen.getByLabelText("适用性"), "yes");
    await user.selectOptions(screen.getByLabelText("适用性"), "unset");
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    await screen.findByRole("heading", { name: "测试结果" });
    const fact = JSON.parse(String(requests[0]?.init?.body)).facts[0];
    expect(fact).toMatchObject({ id: "fact-sla", rule_id: "LINE-SLA-002", dimension: "availability", value: "99.95", unit: "percent", source_role: "contract" });
    expect(fact.citations).toEqual([{ source_id: "contract-A", source_version: "4", start: 0, end: 10, quote: "月可用率99.95%" }]);
    for (const field of ["measurement_window", "time_zone", "calendar", "outage_definition", "basis", "applicable"]) expect(fact).not.toHaveProperty(field);
  });

  it.each(["version", "removed"])("rejects a citation whose source is %s", async (change) => {
    const user = userEvent.setup();
    const requests = renderPanel();
    await addSource(user);
    await addCitedFact(user);
    if (change === "version") inputValue("材料版本", "4");
    else await user.click(screen.getByRole("button", { name: "移除材料 1" }));
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(screen.getByRole("alert")).toHaveTextContent(/引用与样本文本的码点范围不一致/);
    expect(requests).toHaveLength(0);
  });

  it("rejects duplicate fact IDs even when each fact has a valid citation", async () => {
    const user = userEvent.setup();
    const requests = renderPanel();
    await addSource(user);
    await addCitedFact(user);
    await user.click(screen.getByRole("button", { name: "添加事实" }));
    const second = within(screen.getByRole("group", { name: "类型化事实 2" }));
    fireEvent.change(second.getByLabelText("事实 ID"), { target: { value: "fact-bandwidth" } });
    fireEvent.change(second.getByLabelText("事实值"), { target: { value: "100" } });
    selectSource();
    await user.click(second.getByRole("button", { name: "添加当前选区引用" }));
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    expect(screen.getByRole("alert")).toHaveTextContent(/事实 ID 唯一/);
    expect(requests).toHaveLength(0);
    fireEvent.change(second.getByLabelText("事实 ID"), { target: { value: "fact-other" } });
    await user.click(screen.getByRole("button", { name: "测试当前修订" }));
    await screen.findByRole("heading", { name: "测试结果" });
    expect(JSON.parse(String(requests[0]?.init?.body)).facts.map((fact: { id: string }) => fact.id)).toEqual(["fact-bandwidth", "fact-other"]);
  });
});
