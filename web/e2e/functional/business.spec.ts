import { test, expect, operations, apiURL, webURL, body, login, uiLogin, createCase, upload, startReview, waitWorkspace, assertAnchors, evaluation, fixture, record, rows, detail } from "./helpers";
import { parseTemplateList, parseGenerationRecord } from "../../src/api/parse";
import { reviewChoiceKey } from "../../src/domain/workflow";

async function scenario(id: string) {
  const dataset = record(JSON.parse((await fixture("testdata/telecom-c1b-v1.json")).toString("utf8")));
  const chosen = rows(dataset.scenarios).find(item => item.id === id);
  if (!chosen || typeof dataset.template_id !== "string") throw new Error(`Missing synthetic scenario ${id}`);
  const fields = Object.fromEntries(Object.entries(record(chosen.fields)).map(([key, value]) => { if (typeof value !== "string") throw new Error(`Invalid fixture field ${key}`); return [key, value]; }));
  return { fields, templateID: dataset.template_id, materials: rows(chosen.materials), expectedRelations: record(chosen.expected_relations) };
}

for (const id of ["compatible", "conflict", "insufficient"]) test(`three mandatory telecom comparisons and five-domain live projection: ${id}`, operations("listTemplates", "generateDraft", "startReview", "getWorkspace"), async ({ request, page }) => {
  await login(request);
  const gold = await scenario(id);
  const caseID = await createCase(request, `专线${id}`, true);
  const templates = parseTemplateList(await body(await request.get(`${apiURL}/api/templates`)));
  expect(templates.find(item => item.id === gold.templateID)?.version).toBe("1.0.0");
  const generation = parseGenerationRecord(await body(await request.post(`${apiURL}/api/cases/${caseID}/generations`, { data: { template_id: gold.templateID, fields: gold.fields } }), 201));
  for (const material of gold.materials) await upload(request, caseID, String(material.role), String(material.filename), String(material.text));
  const started = await startReview(request, caseID);
  const ws = await waitWorkspace(request, caseID, started.id, started.attempt_id);
  for (const [ruleID, expected] of Object.entries(gold.expectedRelations)) expect(evaluation(ws, ruleID).relation).toBe(expected);
  if (id === "conflict") assertAnchors(ws);
  const projection = ws.execution?.agent_review;
  expect(projection?.attempt_id).toBe(started.attempt_id);
  expect(projection?.mode).toBe("model_live");
  expect(projection?.domains.map(item => item.domain).sort()).toEqual(["commercial", "compliance", "counterparty", "fulfillment", "technical"]);
  expect(projection?.source_snapshot?.document_ids).toContain(generation.document_id);
  expect(projection?.unresolved_gap_ids.length).toBeGreaterThan(0);
  expect(projection?.usage.planner_calls).toBeGreaterThan(0);
  expect(ws.execution?.events.some(event => event.node_kind === "model_call" && event.model_ref === "gpt-6-luna")).toBe(true);
  await uiLogin(page);
  await page.goto(`${webURL}/cases/${caseID}/reviews/${started.id}?attempt_id=${started.attempt_id}`);
  await page.getByRole("region", { name: "执行检查器", exact: true }).locator("summary").first().click();
  await expect(page.getByRole("heading", { name: "五域检查与计划", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "范围、支持表达与证据覆盖", exact: true })).toBeVisible();
  await expect(page.getByText("合规与授权", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("主体与关联", { exact: true }).first()).toBeVisible();
  const repeated = await waitWorkspace(request, caseID, started.id, started.attempt_id);
  expect(repeated.execution?.agent_review).toEqual(projection);
});

test("UI generation, remediation revision, frozen reports and read-only archive preserve prior review", operations("listTemplates", "generateDraft", "createRemediation", "submitRemediation", "verifyRemediation", "createReport", "readReport", "archiveCase", "decideFinding", "getCase", "getWorkspace"), async ({ page }) => {
  await uiLogin(page);
  const api = page.request;
  const gold = await scenario("conflict");
  const fixed = await scenario("compatible");
  const caseID = await createCase(api, "整改报告闭环", true);
  await page.goto(`${webURL}/cases/${caseID}`);
  const templates = parseTemplateList(await body(await api.get(`${apiURL}/api/templates`)));
  const template = templates.find(item => item.id === gold.templateID)!;
  await page.getByLabel("模板", { exact: true }).selectOption(gold.templateID);
  for (const field of template.fields) {
    const control = page.getByLabel(field.required ? `${field.label}（必填）` : field.label, { exact: true });
    const value = gold.fields[field.key];
    if (await control.evaluate(element => element.tagName) === "SELECT") await control.selectOption(value); else await control.fill(value);
  }
  const generated = page.waitForResponse(response => response.request().method() === "POST" && response.url().endsWith(`/cases/${caseID}/generations`));
  await page.getByRole("button", { name: "生成待审草稿", exact: true }).click();
  expect((await generated).status()).toBe(201);
  for (const material of gold.materials) await upload(api, caseID, String(material.role), String(material.filename), String(material.text));
  await page.reload();
  await page.getByRole("button", { name: "开始审查", exact: true }).click();
  await expect(page).toHaveURL(/\/reviews\/run_/);
  const runID = new URL(page.url()).pathname.split("/").at(-1)!;
  const first = await waitWorkspace(api, caseID, runID);
  const attemptID = first.execution!.attempt.id;
  const findingID = evaluation(first, "ENT-TECH-BW-001").finding_id!;
  const finding = first.findings.find(item => item.id === findingID)!;
  await page.getByRole("button", { name: `选择线索：${finding.claim}`, exact: true }).click();
  await page.locator('input[type="radio"][value="confirm_risk"]').check();
  await page.getByLabel("理由", { exact: true }).fill("专线带宽低于100 Mbit/s，必须整改。");
  await page.getByRole("button", { name: "保存决定", exact: true }).click();
  await expect(page.getByText("专线带宽低于100 Mbit/s，必须整改。", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "返回案件", exact: true }).click();
  await page.getByLabel("选择审查和执行记录", { exact: true }).selectOption(reviewChoiceKey(runID, attemptID));
  await page.getByRole("button", { name: "生成报告", exact: true }).click();
  let report = (await detail(api, caseID)).reports[0];
  await expect.poll(async () => { report = (await detail(api, caseID)).reports[0]; return report?.id; }).toBeTruthy();
  const reportBase = `${apiURL}/api/cases/${caseID}/reports/${report.id}`;
  const frozenJSON = await (await api.get(reportBase+"?format=json")).body();
  const frozenHTML = await (await api.get(reportBase+"?format=html")).body();
  expect(frozenJSON.toString()).toContain("100 Mbit/s");
  expect(frozenHTML.toString()).toContain("专线带宽低于100 Mbit/s");
  await page.getByLabel("选择原审查和执行记录", { exact: true }).selectOption(reviewChoiceKey(runID, attemptID));
  const group = page.getByRole("group", { name: "绑定发现（至少选择一项）", exact: true });
  await group.getByRole("checkbox", { name: new RegExp(finding.claim.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")) }).check();
  await page.getByLabel("负责人 ID", { exact: true }).fill("demo-admin");
  const due = page.getByLabel("截止时间", { exact: true });
  await due.fill(await due.getAttribute("type") === "date" ? "2027-01-01" : "2027-01-01T00:00");
  await page.getByLabel("整改措施", { exact: true }).fill("带宽修订到100 Mbit/s并重新审查。");
  await page.getByRole("button", { name: "创建整改", exact: true }).click();
  await expect.poll(async () => (await detail(api, caseID)).remediations.length).toBe(1);
  let item = (await detail(api, caseID)).remediations[0];
  const revised = parseGenerationRecord(await body(await api.post(`${apiURL}/api/cases/${caseID}/generations`, { data: { template_id: fixed.templateID, fields: fixed.fields } }), 201));
  const next = await startReview(api, caseID);
  const second = await waitWorkspace(api, caseID, next.id, next.attempt_id);
  expect(next.id).not.toBe(runID);
  expect(second.previous_run_id).toBe(runID);
  expect(evaluation(second, "ENT-TECH-BW-001").relation).toBe("compatible");
  expect(second.revisions?.some(change => change.previous_finding_id === findingID && change.mark === "消除")).toBe(true);
  expect((await api.post(`${apiURL}/api/cases/${caseID}/remediations/${item.id}/submit`, { data: { document_ids: [revised.document_id], reason: "陈旧版本必须拒绝", expected_version: item.version+1 } })).status()).toBe(409);
  await page.goto(`${webURL}/cases/${caseID}`);
  await page.getByRole("group", { name: "本案当前的拟签合同修改稿", exact: true }).getByRole("radio").check();
  await page.getByLabel("提交理由", { exact: true }).fill("已修订带宽并提交真实新版本");
  await page.getByRole("button", { name: "提交修改稿", exact: true }).click();
  await expect.poll(async () => (await detail(api, caseID)).remediations[0].status).toBe("submitted");
  await page.getByLabel("选择复核的审查和执行记录", { exact: true }).selectOption(reviewChoiceKey(next.id, next.attempt_id));
  await expect(page.getByLabel("复核理由", { exact: true })).toBeEnabled();
  await page.getByLabel("复核理由", { exact: true }).fill("同对象新冻结审查带宽符合，关闭整改。");
  await page.getByRole("button", { name: "提交复核", exact: true }).click();
  await expect.poll(async () => (await detail(api, caseID)).remediations[0].status).toBe("closed");
  item = (await detail(api, caseID)).remediations[0];
  expect(item.status).toBe("closed");
  expect(item.verification_run_id).toBe(next.id);
  expect(await (await api.get(reportBase+"?format=json")).body()).toEqual(frozenJSON);
  expect(await (await api.get(reportBase+"?format=html")).body()).toEqual(frozenHTML);
  const old = await waitWorkspace(api, caseID, runID, attemptID);
  expect(evaluation(old, "ENT-TECH-BW-001").relation).toBe("conflict");
  expect(old.findings.find(item => item.id === findingID)?.decisions[0].reason).toBe("专线带宽低于100 Mbit/s，必须整改。");
  await page.getByLabel("选择审查和执行记录", { exact: true }).selectOption(reviewChoiceKey(next.id, next.attempt_id));
  await page.getByRole("button", { name: "生成报告", exact: true }).click();
  await expect.poll(async () => (await detail(api, caseID)).reports.length).toBe(2);
  const finalReport = (await detail(api, caseID)).reports[1];
  await page.getByLabel("归档所依据的报告", { exact: true }).selectOption(finalReport.id);
  await page.getByLabel("归档理由", { exact: true }).fill("合成闭环验收结束，归档只读。");
  await page.getByRole("button", { name: "归档案件", exact: true }).click();
  await expect.poll(async () => (await detail(api, caseID)).archived_at).not.toBeNull();
  expect((await api.post(`${apiURL}/api/cases/${caseID}/reviews`, { data: {} })).status()).toBe(409);
  expect((await api.post(`${apiURL}/api/cases/${caseID}/documents/${revised.document_id}/parse`, { data: {} })).status()).toBe(409);
  await page.goto(`${webURL}/cases/${caseID}`);
  await expect(page.getByText(/案件已归档，只能读取案件/)).toBeVisible();
  await expect(page.getByRole("button", { name: "上传", exact: true })).toBeDisabled();
});

test("maintenance read-only preserves history and rejects business mutations", operations("getSettings", "createCase", "reparseDocument", "startReview"), async ({ request, playwright }) => {
  await login(request);
  const caseID = await createCase(request, "维护只读");
  const docID = await upload(request, caseID, "procurement", "readonly.txt", "采购文件要求质保36个月。");
  const base = process.env.E2E_READONLY_API!;
  const readOnly = await playwright.request.newContext({ extraHTTPHeaders: { Referer: base } });
  try {
    await login(readOnly, "demo-admin", "admin-demo", "client", base);
    const observed = await body(await readOnly.get(`${base}/api/cases/${caseID}`));
    expect(rows(observed.documents).map(doc => doc.id)).toContain(docID);
    for (const path of ["/api/cases", `/api/cases/${caseID}/documents/${docID}/parse`, `/api/cases/${caseID}/reviews`]) {
      const denied = await body(await readOnly.post(base+path, { data: path === "/api/cases" ? { title: "拒绝写入", contract_type: "service" } : {} }), 503);
      expect(record(denied.error).code).toBe("read_only");
    }
    expect(record((await body(await readOnly.get(`${base}/api/settings`))).model).status).toBe("unknown");
  } finally { await readOnly.dispose(); }
});
