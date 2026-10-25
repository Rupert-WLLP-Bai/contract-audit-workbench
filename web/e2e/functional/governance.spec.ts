import {
  test, expect, operations, apiURL, adminURL, body, login, uiLogin,
  createCase, upload, startReview, waitWorkspace, evaluation, assertAnchors,
  record, rows, type APIRequestContext, type Page,
} from "./helpers";
import {
  parseRulePackDetail, parseRulePackHistory, parseRulePackList,
  parseRulePackManifest, parseRulePackTestResponse,
  type RulePackDetail, type RulePackManifestInput,
} from "../../src/api/rulepacks";
import { parseReportRecord } from "../../src/api/parse";

function manifest(id: string, version: string, bandwidth: string, tenant: string): RulePackManifestInput {
  const scope = { business_line: "ENT", scenario: "telecom_dedicated_line", contract_type: "service" };
  const authority = [{ id: "functional-operator-policy", version: "1", kind: "internal_policy" }];
  return {
    schema_version: "rulepack/v1", id, version, tenant_id: tenant,
    effective_from: "2020-01-01T00:00:00Z", effective_until: "2100-01-01T00:00:00Z",
    rules: [
      {
        rule_id: "ENT-TECH-BW-001", version: "1", domain: "technical",
        capability_id: "dedicated_line.minimum_bandwidth", mandatory: true,
        business_scope: scope, tenant_id: tenant, comparator: "minimum_bandwidth",
        threshold: { value: bandwidth, unit: "Mbit/s" },
        required_fact: { dimension: "minimum_bandwidth", source_roles: ["draft_contract"], object_required: true, unit_family: "bandwidth_decimal", exclusions: null },
        authority_sources: authority,
      },
      {
        rule_id: "ENT-TECH-SLA-001", version: "1", domain: "technical",
        capability_id: "dedicated_line.monthly_availability", mandatory: true,
        business_scope: scope, tenant_id: tenant, comparator: "monthly_sla",
        threshold: { value: "99.9", unit: "percent" },
        required_fact: { dimension: "monthly_availability", source_roles: ["draft_contract"], object_required: true, unit_family: "percentage", measurement_window: "calendar_month", time_zone: "Asia/Shanghai", calendar: "gregorian", outage_definition: "all_service_unavailability", exclusions: [] },
        authority_sources: authority,
      },
      {
        rule_id: "ENT-COM-ADV-001", version: "1", domain: "commercial",
        capability_id: "payment.advance_ratio.ceiling", mandatory: true,
        business_scope: scope, tenant_id: tenant, comparator: "advance_payment_ceiling",
        threshold: { value: "1/3", unit: "ratio", basis: "contract_total" },
        required_fact: { dimension: "advance_payment_ratio", source_roles: ["draft_contract"], object_required: true, unit_family: "ratio", basis: "contract_total", exclusions: null },
        authority_sources: authority,
      },
    ],
  };
}

function versionURL(pack: string, version: string): string {
  return `${apiURL}/api/admin/rulepacks/${encodeURIComponent(pack)}/versions/${version}`;
}

async function getVersion(api: APIRequestContext, pack: string, version: string): Promise<RulePackDetail> {
  return parseRulePackDetail(await body(await api.get(versionURL(pack, version))));
}

async function createViaUI(page: Page, content: RulePackManifestInput) {
  await expect(page.getByRole("heading", { name: "创建规则包草稿", exact: true })).toBeVisible();
  await page.locator("#rulepack-id").fill(content.id);
  await page.locator("#rulepack-version").fill(content.version);
  await page.locator("#rulepack-effective-from").fill("2020-01-01T00:00");
  await page.locator("#rulepack-effective-until").fill("2100-01-01T00:00");
  await expect(page.locator("#rulepack-tenant")).toHaveValue(content.tenant_id);
  for (let index = 0; index < content.rules.length; index++) {
    const rule = content.rules[index]!;
    if (index > 0) await page.getByRole("button", { name: "添加规则", exact: true }).click();
    const values: Record<string, string> = {
      id: rule.rule_id, version: rule.version, domain: rule.domain, capability: rule.capability_id,
      "business-line": rule.business_scope.business_line, scenario: rule.business_scope.scenario,
      "contract-type": rule.business_scope.contract_type, threshold: rule.threshold.value,
      unit: rule.threshold.unit, basis: rule.threshold.basis ?? "", dimension: rule.required_fact.dimension,
      roles: rule.required_fact.source_roles.join("\n"), "unit-family": rule.required_fact.unit_family,
      "measurement-window": rule.required_fact.measurement_window ?? "", "time-zone": rule.required_fact.time_zone ?? "",
      calendar: rule.required_fact.calendar ?? "", outage: rule.required_fact.outage_definition ?? "",
      "fact-basis": rule.required_fact.basis ?? "",
      "authority-0-id": rule.authority_sources[0]!.id,
      "authority-0-version": rule.authority_sources[0]!.version,
      "authority-0-kind": rule.authority_sources[0]!.kind,
    };
    await page.locator(`#rule-${index}-comparator`).selectOption(rule.comparator);
    for (const [field, value] of Object.entries(values)) await page.locator(`#rule-${index}-${field}`).fill(value);
    await expect(page.locator(`#rule-${index}-mandatory`)).toBeChecked();
    await expect(page.locator(`#rule-${index}-mandatory`)).toBeDisabled();
  }
  const response = page.waitForResponse(item => item.request().method() === "POST" && item.url().endsWith("/api/admin/rulepacks"));
  await page.getByRole("button", { name: "创建草稿", exact: true }).click();
  expect((await response).status()).toBe(201);
  await expect(page.getByRole("heading", { name: `${content.id} · v${content.version}`, exact: true })).toBeVisible();
  return getVersion(page.request, content.id, content.version);
}

async function action(page: Page, name: string, endpoint: string) {
  const response = page.waitForResponse(item => item.request().method() === "POST" && item.url().endsWith(endpoint));
  await page.getByRole("button", { name, exact: true }).click();
  const completed = await response;
  expect(completed.status(), await completed.text()).toBe(200);
}

async function publishViaUI(page: Page, pack: string, version: string) {
  const base = `/api/admin/rulepacks/${pack}/versions/${version}`;
  await action(page, "验证草稿", `${base}/validate`);
  await expect(page.getByRole("button", { name: "批准此内容摘要", exact: true })).toBeVisible();
  let current = await getVersion(page.request, pack, version);
  expect(current.pack.status).toBe("validated");
  expect(current.manifest.lifecycle.verification?.passed).toBe(true);
  expect(current.manifest.lifecycle.verification?.manifest_hash).toBe(current.pack.content_hash);
  await action(page, "批准此内容摘要", `${base}/approve`);
  current = await getVersion(page.request, pack, version);
  expect(current.pack.status).toBe("approved");
  expect(current.manifest.lifecycle.approval?.actor_id).toBe("demo-admin");
  expect(current.manifest.lifecycle.approval?.manifest_hash).toBe(current.pack.content_hash);
  await action(page, "发布此版本", `${base}/publish`);
  await expect(page.getByRole("button", { name: "停用活动指针", exact: true })).toBeVisible();
  current = await getVersion(page.request, pack, version);
  expect(current.pack.status).toBe("published");
  expect(current.pack.is_active).toBe(true);
  expect(current.manifest.lifecycle.release?.manifest_hash).toBe(current.pack.content_hash);
  await expect(page.getByRole("heading", { name: "规则内容（只读）", exact: true })).toBeVisible();
  return current;
}

async function sampleWithUnicodeSelection(page: Page, pack: string) {
  await page.locator("#rulepack-test-at").fill("2026-10-24T12:00");
  await page.getByRole("button", { name: "添加样本材料", exact: true }).click();
  await page.locator("#sample-source-0-id").fill("sample-draft");
  await page.locator("#sample-source-0-role").fill("draft_contract");
  const quote = "电路 LINE-001：最低保障带宽 150 Mbit/s。";
  const text = `😀${quote}`;
  await page.locator("#sample-source-0-text").fill(text);
  const path = `/api/admin/rulepacks/${pack}/versions/1/test`;
  let response = page.waitForResponse(item => item.request().method() === "POST" && item.url().endsWith(path));
  await page.getByRole("button", { name: "测试当前修订", exact: true }).click();
  let completed = await response;
  expect(completed.status(), await completed.text()).toBe(200);
  const missing = parseRulePackTestResponse(await completed.json());
  expect(missing.report.results).toHaveLength(3);
  expect(missing.report.results.every(result => result.relation === "insufficient")).toBe(true);

  await page.getByRole("button", { name: "添加事实", exact: true }).click();
  await page.locator("#sample-fact-0-rule").selectOption("ENT-TECH-BW-001");
  await page.locator("#sample-fact-0-id").fill("fact-bw");
  await page.locator("#sample-fact-0-object").fill("LINE-001");
  await page.locator("#sample-fact-0-value").fill("150");
  await page.getByRole("button", { name: "测试当前修订", exact: true }).click();
  await expect(page.locator("#rulepack-test").getByRole("alert")).toContainText("1–8 条来源引用");
  const source = page.locator("#sample-source-0-text");
  await source.focus();
  await source.press("Control+Home");
  await source.press("ArrowRight");
  await source.press("Shift+End");
  const addCitation = page.getByRole("button", { name: "添加当前选区引用", exact: true });
  await expect(addCitation).toBeEnabled();
  await addCitation.click();
  const end = 1 + Array.from(quote).length;
  await expect(page.locator("#rulepack-test")).toContainText(`码点 [1,${end})`);
  response = page.waitForResponse(item => item.request().method() === "POST" && item.url().endsWith(path));
  await page.getByRole("button", { name: "测试当前修订", exact: true }).click();
  completed = await response;
  expect(completed.status(), await completed.text()).toBe(200);
  const report = parseRulePackTestResponse(await completed.json());
  const result = report.report.results.find(item => item.rule_id === "ENT-TECH-BW-001");
  expect(result?.relation).toBe("compatible");
  expect(result?.fact_ids).toEqual(["fact-bw"]);
  expect(result?.citations).toEqual([expect.objectContaining({ source_id: "sample-draft", source_version: "1", start: 1, end, quote })]);
  const sent = record(completed.request().postDataJSON());
  expect(rows(rows(sent.facts)[0]!.citations)[0]).toEqual(expect.objectContaining({ start: 1, end, quote }));
  await expect(page.locator(".rulepack-test-result")).toContainText("dedicated_line.minimum_bandwidth");
  await expect(page.locator(".rulepack-test-result")).toContainText(quote);
}

test("Admin governs drafts with preserved CAS edits, located sample facts and frozen v1/v2 review reports", operations(
  "listRulePacks", "createRulePack", "getRulePackVersion", "editRulePackVersion", "testRulePackVersion",
  "validateRulePackVersion", "approveRulePackVersion", "publishRulePackVersion", "disableRulePackVersion",
  "rollbackRulePackVersion", "rulePackHistory", "startReview", "getWorkspace", "createReport", "readReport",
), async ({ page, request }, testInfo) => {
  test.setTimeout(360_000);
  page.setDefaultTimeout(30_000);
  const client = await login(request);
  const pack = `functional-governance-${Date.now()}`;
  await uiLogin(page, "admin");
  await page.getByRole("link", { name: "规则包治理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "规则包治理", exact: true })).toBeVisible();
  await page.getByRole("link", { name: "创建草稿", exact: true }).click();
  const initial = manifest(pack, "1", "100", client.actor.tenant_id);
  await createViaUI(page, initial);
  let primaryFailure: unknown;
  try {
    const bandwidthEditor = page.getByRole("group", { name: /^规则 \d+ · ENT-TECH-BW-001$/ });
    await bandwidthEditor.getByLabel("比较阈值", { exact: true }).fill("120");
    await bandwidthEditor.getByLabel("来源版本", { exact: true }).fill("2");
    const concurrent = manifest(pack, "1", "110", client.actor.tenant_id);
    const current = await getVersion(page.request, pack, "1");
    const remote = parseRulePackDetail(await body(await page.request.put(versionURL(pack, "1"), { data: { expected_revision: current.pack.revision, manifest: concurrent } })));
    expect(remote.pack.revision).toBe(current.pack.revision + 1);
    const failed = page.waitForResponse(item => item.request().method() === "PUT" && item.url().endsWith(`/api/admin/rulepacks/${pack}/versions/1`));
    await page.getByRole("button", { name: "保存草稿", exact: true }).click();
    const stale = await failed;
    expect(stale.status(), await stale.text()).toBe(409);
    await expect(page.getByText(/你的本地修改仍保留/)).toBeVisible();
    await expect(bandwidthEditor.getByLabel("比较阈值", { exact: true })).toHaveValue("120");
    await expect(bandwidthEditor.getByLabel("来源版本", { exact: true })).toHaveValue("2");
    await page.getByRole("button", { name: "重读服务器版本（保留本地修改）", exact: true }).click();
    await page.getByRole("button", { name: `下一次保存使用服务器修订 ${remote.pack.revision}`, exact: true }).click();
    const savedResponse = page.waitForResponse(item => item.request().method() === "PUT" && item.url().endsWith(`/api/admin/rulepacks/${pack}/versions/1`));
    await page.getByRole("button", { name: "保存草稿", exact: true }).click();
    expect((await savedResponse).status()).toBe(200);
    await expect(page.getByText("草稿已保存；先前的验证和批准状态已由服务器按内容变化重置。", { exact: true })).toBeVisible();
    const saved = await getVersion(page.request, pack, "1");
    expect(saved.pack.status).toBe("draft");
    expect(saved.manifest.lifecycle.verification).toBeUndefined();
    expect(saved.manifest.lifecycle.approval).toBeUndefined();
    const savedBandwidth = saved.manifest.rules.find(rule => rule.rule_id === "ENT-TECH-BW-001");
    expect(savedBandwidth?.threshold.value).toBe("120");
    expect(savedBandwidth?.authority_sources[0]?.version).toBe("2");
    await sampleWithUnicodeSelection(page, pack);
    let v1 = await publishViaUI(page, pack, "1");
    const v1Manifest = JSON.stringify(v1.manifest);
    expect((await page.request.put(versionURL(pack, "1"), { data: { expected_revision: v1.pack.revision, manifest: initial } })).status()).toBe(409);
    expect((await page.request.post(`${versionURL(pack, "1")}/approve`, { data: { expected_revision: v1.pack.revision - 1, expected_content_hash: v1.pack.content_hash } })).status()).toBe(409);
    await action(page, "停用活动指针", `/api/admin/rulepacks/${pack}/versions/1/disable`);
    await expect(page.getByRole("button", { name: "停用活动指针", exact: true })).toHaveCount(0);
    const disabled = await getVersion(page.request, pack, "1");
    expect(disabled.pack.is_active).toBe(false);
    expect(JSON.stringify(disabled.manifest)).toBe(v1Manifest);
    // The existing rollback control reactivates an exact disabled publication.
    await page.getByRole("combobox", { name: "目标历史版本", exact: true }).selectOption("1");
    await action(page, "回滚到所选版本", `/api/admin/rulepacks/${pack}/rollback`);
    await expect(page.getByRole("button", { name: "停用活动指针", exact: true })).toBeVisible();
    v1 = await getVersion(page.request, pack, "1");
    expect(v1.pack.active_pointer_revision).toBe(disabled.pack.active_pointer_revision + 1);
    expect(JSON.stringify(v1.manifest)).toBe(v1Manifest);

    const caseID = await createCase(request, "规则包冻结", true);
    await upload(request, caseID, "procurement", "governed-procurement.txt", "电路 LINE-001：最低保障带宽 120 Mbit/s。");
    await upload(request, caseID, "draft_contract", "governed-draft.txt", "电路 LINE-001：最低保障带宽 150 Mbit/s。");
    const first = await startReview(request, caseID);
    const firstWorkspace = await waitWorkspace(request, caseID, first.id, first.attempt_id);
    expect(evaluation(firstWorkspace, "ENT-TECH-BW-001").relation).toBe("compatible");
    const report1 = parseReportRecord(await body(await request.post(`${apiURL}/api/cases/${caseID}/reports`, { data: { run_id: first.id, attempt_id: first.attempt_id } }), 201));
    const reportURL = `${apiURL}/api/cases/${caseID}/reports/${report1.id}`;
    const jsonBefore = await request.get(reportURL);
    expect(jsonBefore.status()).toBe(200);
    const jsonBytes = await jsonBefore.body();
    const htmlBefore = await request.get(`${reportURL}?format=html`);
    expect(htmlBefore.status()).toBe(200);
    const htmlBytes = await htmlBefore.body();
    const frozen1 = record(JSON.parse(jsonBytes.toString("utf8")));
    const frozenPack1 = record(frozen1.rulepack);
    expect(frozenPack1.availability).toBe("frozen");
    expect(frozenPack1.manifest_hash).toBe(v1.pack.content_hash);
    expect(parseRulePackManifest(frozenPack1.snapshot)).toEqual(v1.manifest);

    await page.getByRole("link", { name: "创建新草稿", exact: true }).click();
    await createViaUI(page, manifest(pack, "2", "200", client.actor.tenant_id));
    const v2 = await publishViaUI(page, pack, "2");
    expect(v2.pack.content_hash).not.toBe(v1.pack.content_hash);
    const second = await startReview(request, caseID);
    expect(second.id).not.toBe(first.id);
    const secondWorkspace = await waitWorkspace(request, caseID, second.id, second.attempt_id);
    expect(evaluation(secondWorkspace, "ENT-TECH-BW-001").relation).toBe("conflict");
    assertAnchors(secondWorkspace);
    const bandwidth = evaluation(secondWorkspace, "ENT-TECH-BW-001");
    const finding = secondWorkspace.findings.find(item => item.id === bandwidth.finding_id);
    expect(finding?.evidence.some(anchor => anchor.status === "located" && anchor.quote.includes("150 Mbit/s"))).toBe(true);
    const report2 = parseReportRecord(await body(await request.post(`${apiURL}/api/cases/${caseID}/reports`, { data: { run_id: second.id, attempt_id: second.attempt_id } }), 201));
    const frozen2 = await body(await request.get(`${apiURL}/api/cases/${caseID}/reports/${report2.id}`));
    const frozenPack2 = record(frozen2.rulepack);
    expect(frozenPack2.manifest_hash).toBe(v2.pack.content_hash);
    expect(parseRulePackManifest(frozenPack2.snapshot)).toEqual(v2.manifest);
    await page.getByRole("combobox", { name: "目标历史版本", exact: true }).selectOption("1");
    await action(page, "回滚到所选版本", `/api/admin/rulepacks/${pack}/rollback`);
    const rolledBack = await getVersion(page.request, pack, "1");
    expect(rolledBack.pack.is_active).toBe(true);
    expect(JSON.stringify(rolledBack.manifest)).toBe(v1Manifest);
    expect((await getVersion(page.request, pack, "2")).pack.is_active).toBe(false);
    expect(await (await request.get(reportURL)).body()).toEqual(jsonBytes);
    expect(await (await request.get(`${reportURL}?format=html`)).body()).toEqual(htmlBytes);
    const oldWorkspace = await waitWorkspace(request, caseID, first.id, first.attempt_id);
    expect(evaluation(oldWorkspace, "ENT-TECH-BW-001").relation).toBe("compatible");
    expect(oldWorkspace.execution?.attempt?.input_hash).toBe(firstWorkspace.execution?.attempt?.input_hash);
    expect(oldWorkspace.execution?.attempt?.output_hash).toBe(firstWorkspace.execution?.attempt?.output_hash);
    const oldV2 = await waitWorkspace(request, caseID, second.id, second.attempt_id);
    expect(evaluation(oldV2, "ENT-TECH-BW-001").relation).toBe("conflict");
    const history = parseRulePackHistory(await body(await page.request.get(`${apiURL}/api/admin/rulepacks/${pack}/history`)));
    for (const expected of ["create", "edit", "test", "validate", "approve", "publish", "disable", "rollback"]) expect(history.some(entry => entry.action === expected)).toBe(true);
    expect(history.every(entry => entry.actor_id === "demo-admin" && entry.content_hash.length === 64)).toBe(true);
    expect(history.filter(entry => entry.action === "publish").map(entry => entry.version).sort()).toEqual(["1", "2"]);
    await expect(page.locator(".rulepack-history")).toContainText("rollback");
    await page.getByRole("link", { name: "返回规则包", exact: true }).click();
    await page.getByLabel("查找规则包", { exact: true }).fill(pack);
    await expect(page.getByRole("link", { name: `${pack} · v1`, exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: `${pack} · v2`, exact: true })).toBeVisible();
    await page.getByRole("combobox", { name: "版本状态", exact: true }).selectOption("active");
    await expect(page.getByRole("link", { name: `${pack} · v1`, exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: `${pack} · v2`, exact: true })).toHaveCount(0);
    const list = parseRulePackList(await body(await page.request.get(`${apiURL}/api/admin/rulepacks`)));
    expect(list.filter(item => item.id === pack && item.is_active).map(item => item.version)).toEqual(["1"]);
    await testInfo.attach("governance-freeze.json", { contentType: "application/json", body: JSON.stringify({ pack, caseID, first, second, report1, report2, v1: v1.pack, v2: v2.pack, history }, null, 2) });
    await testInfo.attach("governance-active-version.png", { contentType: "image/png", body: await page.screenshot({ fullPage: true }) });
  } catch (error) {
    primaryFailure = error;
    throw error;
  } finally {
    // Deactivate only this test's publication; keep versions/history/reports.
    // A cleanup failure must not replace the action that failed first.
    try {
      const list = parseRulePackList(await body(await page.request.get(`${apiURL}/api/admin/rulepacks`, { timeout: 5_000 })));
      const active = list.find(item => item.id === pack && item.is_active);
      if (active) await body(await page.request.post(`${versionURL(pack, active.version)}/disable`, {
        timeout: 5_000,
        data: { expected_revision: active.revision, expected_pointer_revision: active.active_pointer_revision },
      }));
    } catch (cleanupError) {
      if (primaryFailure === undefined) throw cleanupError;
      testInfo.annotations.push({ type: "cleanup_failure", description: String(cleanupError) });
    }
  }
});

test("governance rejects anonymous/client writes, forged tenant/lifecycle and nonadmin Admin login", operations(
  "createSession", "listRulePacks", "getRulePackVersion", "createRulePack", "editRulePackVersion",
  "validateRulePackVersion", "approveRulePackVersion", "publishRulePackVersion", "disableRulePackVersion",
  "rollbackRulePackVersion", "rulePackHistory", "testRulePackVersion",
), async ({ request, page }) => {
  expect((await request.get(`${apiURL}/api/admin/rulepacks`)).status()).toBe(401);
  const client = await login(request);
  const pack = `functional-denied-${Date.now()}`;
  const content = manifest(pack, "1", "100", client.actor.tenant_id);
  const base = versionURL(pack, "1");
  for (const path of [`${apiURL}/api/admin/rulepacks`, base, `${apiURL}/api/admin/rulepacks/${pack}/history`]) expect((await request.get(path)).status()).toBe(401);
  expect((await request.post(`${apiURL}/api/admin/rulepacks`, { data: { manifest: content } })).status()).toBe(401);
  expect((await request.put(base, { data: { expected_revision: 1, manifest: content } })).status()).toBe(401);
  for (const action of ["validate", "approve", "publish", "disable", "test"]) expect((await request.post(`${base}/${action}`, { data: { expected_revision: 1 } })).status()).toBe(401);
  expect((await request.post(`${apiURL}/api/admin/rulepacks/${pack}/rollback`, { data: { version: "1", expected_pointer_revision: 0 } })).status()).toBe(401);
  expect((await request.post(`${apiURL}/api/session`, { data: { username: "demo-viewer", password: "viewer-demo", audience: "admin" } })).status()).toBe(403);
  await uiLogin(page, "admin");
  const admin = page.request;
  expect((await admin.post(`${apiURL}/api/admin/rulepacks`, { data: { manifest: { ...content, tenant_id: "foreign-tenant" } } })).status()).toBe(422);
  expect((await admin.post(`${apiURL}/api/admin/rulepacks`, { data: { manifest: { ...content, lifecycle: { state: "published" } } } })).status()).toBe(422);
  expect((await admin.post(`${apiURL}/api/admin/rulepacks`, { data: { manifest: content, approved_by: "forged" } })).status()).toBe(422);
  await body(await admin.post(`${apiURL}/api/admin/rulepacks`, { data: { manifest: content } }), 201);
  expect((await admin.post(`${apiURL}/api/admin/rulepacks`, { data: { manifest: content } })).status()).toBe(409);
  const beforeInvalidSample = await getVersion(admin, pack, "1");
  const historyURL = `${apiURL}/api/admin/rulepacks/${pack}/history`;
  const historyBeforeInvalidSample = parseRulePackHistory(await body(await admin.get(historyURL)));
  const invalidSample = await body(await admin.post(`${base}/test`, { data: {
    expected_revision: beforeInvalidSample.pack.revision,
    business_line: "ENT", scenario: "telecom_dedicated_line", contract_type: "service",
    at: "2026-10-24T12:00:00Z", sources: [], facts: [],
  } }), 422);
  expect(record(invalidSample.error).code).toBe("invalid");
  expect(await getVersion(admin, pack, "1")).toEqual(beforeInvalidSample);
  expect(parseRulePackHistory(await body(await admin.get(historyURL)))).toEqual(historyBeforeInvalidSample);
  expect((await admin.post(`${base}/publish`, { data: { expected_revision: 1, expected_pointer_revision: 0 } })).status()).toBe(409);
  expect((await admin.post(`${apiURL}/api/admin/rulepacks/${pack}/rollback`, { data: { version: "1", expected_pointer_revision: 0 } })).status()).toBe(409);
  expect((await admin.get(`${apiURL}/api/cases`)).status()).toBe(401);
  expect(adminURL).toContain("/admin");
});
