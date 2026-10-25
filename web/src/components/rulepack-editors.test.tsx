import { useState } from "react";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it } from "vitest";
import { createEmptyRulePackDraft, RulePackManifestEditor } from "./rulepack-manifest-editor";
import { RulePackTestPanel } from "./rulepack-test-panel";
import type { RulePackManifestInput, RulePackManifest } from "../api/rulepacks";

const clients: QueryClient[] = [];

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
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
