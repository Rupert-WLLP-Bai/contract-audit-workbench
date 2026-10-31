/**
 * Runs against the fixture stack. The review planner and experience-distillation
 * gateway are deterministic test doubles: these tests verify workflow and
 * governance behavior, not model quality.
 */
import { test, expect, operations, apiURL, webURL, adminURL, body, login, uiLogin, createCase, upload, startReview, waitWorkspace, detail, record, rows, type APIRequestContext } from "./helpers";
import { parseRulePackDetail, type RulePackDetail, type RulePackManifestInput } from "../../src/api/rulepacks";
import { parseAcceptedReview, parseReportRecord, parseReviewWorkspace } from "../../src/api/parse";
import { parseKnowledgeVersion, type KnowledgeContent, type KnowledgeVersion } from "../../src/api/governed-knowledge";
import { paths } from "../../src/api/paths";
import type { Locator, Page } from "playwright/test";

const ENT = { business_line: "ENT", scenario: "telecom_dedicated_line", contract_type: "service" };
const POLICY_STATEMENTS = [
  { ruleID: "ENT-TECH-BW-001", quote: "带宽不低于100 Mbit/s", suffix: "" },
  { ruleID: "ENT-TECH-SLA-001", quote: "月可用率不低于99.9%", suffix: "，统计周期为自然月，时区Asia/Shanghai，公历gregorian，故障口径all_service_unavailability，无排除项" },
  { ruleID: "ENT-COM-ADV-001", quote: "预付款比例不超过1/3", suffix: "，预付款以合同总额为基础" },
] as const;
type Rule = RulePackManifestInput["rules"][number];

function rule(ruleID: string, domain: string, capabilityID: string, comparator: Rule["comparator"], threshold: Rule["threshold"], fact: Rule["required_fact"], tenant: string, authorityID: string, authorityVersion: string): Rule {
  return { rule_id: ruleID, version: "1", domain, capability_id: capabilityID, mandatory: true, business_scope: ENT, tenant_id: tenant, comparator, threshold, required_fact: fact, authority_sources: [{ id: authorityID, version: authorityVersion, kind: "internal_policy" }] };
}

function manifest(id: string, version: string, tenant: string, authorityID: string, authorityVersion: string): RulePackManifestInput {
  return {
    schema_version: "rulepack/v1", id, version, tenant_id: tenant,
    effective_from: "2020-01-01T00:00:00Z", effective_until: "2100-01-01T00:00:00Z",
    rules: [
      rule("ENT-TECH-BW-001", "technical", "dedicated_line.minimum_bandwidth", "minimum_bandwidth", { value: "100", unit: "Mbit/s" }, { dimension: "minimum_bandwidth", source_roles: ["draft_contract"], object_required: true, unit_family: "bandwidth_decimal", exclusions: null }, tenant, authorityID, authorityVersion),
      rule("ENT-TECH-SLA-001", "technical", "dedicated_line.monthly_availability", "monthly_sla", { value: "99.9", unit: "percent" }, { dimension: "monthly_availability", source_roles: ["draft_contract"], object_required: true, unit_family: "percentage", measurement_window: "calendar_month", time_zone: "Asia/Shanghai", calendar: "gregorian", outage_definition: "all_service_unavailability", exclusions: [] }, tenant, authorityID, authorityVersion),
      rule("ENT-COM-ADV-001", "commercial", "payment.advance_ratio.ceiling", "advance_payment_ceiling", { value: "1/3", unit: "ratio", basis: "contract_total" }, { dimension: "advance_payment_ratio", source_roles: ["draft_contract"], object_required: true, unit_family: "ratio", basis: "contract_total", exclusions: null }, tenant, authorityID, authorityVersion),
    ],
  };
}

function policyContent(pack: RulePackDetail, knowledgeID: string, title: string): KnowledgeContent {
  let bodyText = "";
  const spans: { rule_id: string; start: number; end: number; quote: string }[] = [];
  for (const statement of POLICY_STATEMENTS) {
    const start = Array.from(bodyText).length;
    bodyText += statement.quote + statement.suffix + "。";
    spans.push({ rule_id: statement.ruleID, start, end: start + Array.from(statement.quote).length, quote: statement.quote });
  }
  const summary = pack.pack;
  return {
    knowledge_id: knowledgeID, version: "1", kind: "policy", title, body: bodyText,
    summary: "核验带宽、可用率和预付款比例", source_type: "reference", source_reference: "功能验收政策来源",
    scope: ENT, valid_from: "2020-01-01T00:00:00Z", valid_until: "2100-01-01T00:00:00Z",
    applicability: null, exclusions: null,
    binding: {
      pack_id: summary.id, pack_version: summary.version, pack_content_hash: summary.content_hash, pack_snapshot_digest: summary.snapshot_sha256,
      compiler_ref: "governed-rulepack/v1", extraction_compiler_ref: "governed-policy-threshold/v1",
      rule_ids: POLICY_STATEMENTS.map((statement) => statement.ruleID), source_spans: spans,
      candidate_diff: "规则差异已审阅", validation_evidence: "规则包校验通过",
    },
  };
}

async function publishPack(api: APIRequestContext, packID: string, authorityID: string, tenant: string, stamp: number): Promise<RulePackDetail> {
  const base = `${apiURL}/api/admin/rulepacks/${encodeURIComponent(packID)}/versions/1`;
  const created = parseRulePackDetail(await body(await api.post(`${apiURL}/api/admin/rulepacks`, { data: { manifest: manifest(packID, "1", tenant, authorityID, "1") } }), 201));
  const validated = parseRulePackDetail(await body(await api.post(`${base}/validate`, { data: { expected_revision: created.pack.revision } }), 200));
  const approved = parseRulePackDetail(await body(await api.post(`${base}/approve`, { data: { expected_revision: validated.pack.revision, expected_content_hash: validated.pack.content_hash } }), 200));
  const published = parseRulePackDetail(await body(await api.post(`${base}/publish`, { data: { expected_revision: approved.pack.revision, expected_pointer_revision: approved.pack.active_pointer_revision }, headers: { "Idempotency-Key": `rulepack-publish-${packID}-${stamp}` } }), 200));
  expect(validated.pack.status).toBe("validated");
  expect(approved.pack.status).toBe("approved");
  expect(published.pack.status).toBe("published");
  expect(published.pack.is_active).toBe(true);
  return published;
}

async function createPolicy(api: APIRequestContext, pack: RulePackDetail, knowledgeID: string, title: string): Promise<KnowledgeVersion> {
  const created = parseKnowledgeVersion(await body(await api.post(`${apiURL}/api/admin/governed-knowledge`, { data: { content: policyContent(pack, knowledgeID, title) }, headers: { "Idempotency-Key": `knowledge-create-${knowledgeID}` } }), 201));
  expect(created.ref.knowledge_id).toBe(knowledgeID);
  expect(created.status).toBe("draft");
  return created;
}

async function advancePolicy(api: APIRequestContext, created: KnowledgeVersion, actions: readonly string[], prefix: string): Promise<KnowledgeVersion> {
  const statuses: Record<string, string> = { submit: "submitted", validate: "validated", approve: "approved", publish: "published" };
  const path = `${apiURL}/api/admin/governed-knowledge/${encodeURIComponent(created.ref.knowledge_id)}/versions/${encodeURIComponent(created.ref.version_id)}/commands`;
  let current = created;
  for (const action of actions) {
    const response = await api.post(path, { data: { action, expected_revision: current.revision, expected_content_sha256: created.ref.content_sha256, expected_pointer_revision: current.pointer_revision, reason: `${prefix} ${action} 依据` }, headers: { "Idempotency-Key": `${prefix}-${action}` } });
    const raw = await response.text();
    expect(response.status(), `${response.url()}: ${raw}`).toBe(200);
    current = parseKnowledgeVersion(JSON.parse(raw));
    expect(current.status).toBe(statuses[action]);
  }
  return current;
}

async function selectAll(locator: Locator): Promise<void> {
  await locator.evaluate((element) => {
    const area = element as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, area.value.length);
    area.dispatchEvent(new Event("select", { bubbles: true }));
    area.dispatchEvent(new KeyboardEvent("keyup", { key: "Shift", bubbles: true }));
    document.dispatchEvent(new Event("selectionchange"));
  });
}

async function chooseVisibleOption(page: Page, text: string) {
  await visibleOption(page, text).evaluate((element) => (element as HTMLElement).click());
}

async function disableActiveGovernance(api: APIRequestContext) {
  const knowledge = await body(await api.get(`${apiURL}/api/admin/governed-knowledge`));
  for (const item of rows(knowledge.items)) {
    if (!item.active) continue;
    const ref = record(item.ref);
    const version = await body(await api.get(`${apiURL}/api/admin/governed-knowledge/${ref.knowledge_id}/versions/${ref.version_id}`));
    if (!version.active) continue;
    await body(await api.post(`${apiURL}/api/admin/governed-knowledge/${ref.knowledge_id}/versions/${ref.version_id}/commands`, {
      data: { action: "withdraw", expected_revision: version.revision, expected_content_sha256: record(version.ref).content_sha256,
        expected_pointer_revision: version.pointer_revision, reason: "隔离下一次工作流验收" },
      headers: { "Idempotency-Key": `withdraw-${ref.version_id}-${Date.now()}` },
    }));
  }
  const packs = await body(await api.get(`${apiURL}/api/admin/rulepacks`));
  for (const item of rows(packs.items)) {
    if (!item.is_active) continue;
    const detail = await body(await api.get(`${apiURL}/api/admin/rulepacks/${item.id}/versions/${item.version}`));
    const pack = record(detail.pack);
    await body(await api.post(`${apiURL}/api/admin/rulepacks/${item.id}/versions/${item.version}/disable`, {
      data: { expected_revision: pack.revision, expected_pointer_revision: pack.active_pointer_revision },
    }));
  }
}

function visibleOption(page: Page, text: string) {
  return page.locator(".ant-select-item-option:visible").filter({ hasText: text }).first();
}

test("experience feedback, distillation and candidate review", operations("createSession", "createRulePack", "validateRulePackVersion", "approveRulePackVersion", "publishRulePackVersion", "createGovernedKnowledge", "governedKnowledgeCommand", "createCase", "uploadDocument", "startReview", "getWorkspace", "decideFinding", "submitExperienceFeedback", "requestExperienceDistillation", "getExperienceDistillation", "getExperienceCandidate"), async ({ page, request, playwright }, testInfo) => {
  test.setTimeout(360_000);
  page.setDefaultTimeout(30_000);
  testInfo.annotations.push({ type: "fixture-model", description: "fixture model: deterministic test double" });
  await testInfo.attach("fixture-model.txt", { body: "fixture model: deterministic test double\nThis test verifies workflow behavior, not model quality.\n", contentType: "text/plain" });
  const stamp = Date.now();
  const adminApi = await playwright.request.newContext({ extraHTTPHeaders: { Referer: apiURL } });
  try {
    const admin = await login(adminApi, "demo-admin", "admin-demo", "admin");
    expect(admin.audience).toBe("admin");
    await disableActiveGovernance(adminApi);
    const packID = `workflows-exp-pack-${stamp}`;
    const knowledgeID = `workflows-exp-policy-${stamp}`;
    const pack = await publishPack(adminApi, packID, knowledgeID, admin.actor.tenant_id, stamp);
    const policy = await createPolicy(adminApi, pack, knowledgeID, `经验闭环制度 ${stamp}`);
    const publishedPolicy = await advancePolicy(adminApi, policy, ["submit", "validate", "approve", "publish"], `experience-policy-${stamp}`);
    expect(publishedPolicy.active).toBe(true);

    const client = await login(request);
    expect(client.audience).toBe("client");
    const caseID = await createCase(request, `经验反馈 ${stamp}`, true);
    const procurementFile = `experience-procurement-${stamp}.txt`;
    await upload(request, caseID, "procurement", procurementFile, "采购文件要求电路 LINE-001 的最低带宽为100 Mbit/s，合同总价100万元。");
    await upload(request, caseID, "draft_contract", `experience-draft-${stamp}.txt`, "拟签合同约定电路 LINE-001 的最低带宽为80 Mbit/s，合同总价120万元。");
    const startedResponse = await request.post(`${apiURL}/api/cases/${caseID}/reviews`, { data: { business_at: "2026-02-03T01:05:06Z" }, headers: { "Idempotency-Key": `experience-review-${stamp}` } });
    const startedText = await startedResponse.text();
    expect([201, 202], `${startedResponse.url()}: ${startedText}`).toContain(startedResponse.status());
    const accepted = parseAcceptedReview(JSON.parse(startedText));
    const workspace = await waitWorkspace(request, caseID, accepted.id, accepted.attempt_id);
    expect(workspace.findings.length).toBeGreaterThan(0);
    const finding = workspace.findings[0]!;
    if (finding.version === null) throw new Error("Selected finding has no version");
    const decisionReason = `核对冻结原文差异 ${stamp}`;
    const decisionResponse = await request.post(`${apiURL}/api/cases/${caseID}/reviews/${accepted.id}/findings/${finding.id}/decisions`, { data: { action: "confirm_risk", reason: decisionReason, expected_version: finding.version }, headers: { "Idempotency-Key": `experience-decision-${stamp}` } });
    const decisionText = await decisionResponse.text();
    expect(decisionResponse.status(), `${decisionResponse.url()}: ${decisionText}`).toBe(204);
    const updated = parseReviewWorkspace(await body(await request.get(`${apiURL}/api/cases/${caseID}/reviews/${accepted.id}?attempt_id=${encodeURIComponent(accepted.attempt_id)}`)));
    const decidedFinding = updated.findings.find((item) => item.id === finding.id);
    const decision = decidedFinding?.decisions.find((item) => item.action === "confirm_risk" && item.reason === decisionReason);
    expect(decision?.id, "the re-read workspace must expose the appended decision id").toBeTruthy();
    const decisionID = String(decision?.id);

    await uiLogin(page);
    await page.goto(`${webURL}/cases/${encodeURIComponent(caseID)}/reviews/${encodeURIComponent(accepted.id)}?attempt_id=${encodeURIComponent(accepted.attempt_id)}&finding=${encodeURIComponent(finding.id)}`);
    await expect(page.getByRole("heading", { name: "审查工作区", exact: true })).toBeVisible();
    await page.getByRole("button", { name: `选择线索：${finding.claim || "未命名线索"}`, exact: true }).first().click();
    const experience = page.locator(".workflow-panel").filter({ hasText: "复盘经验建议" }).first();
    await expect(experience).toBeVisible();
    const decisionSelect = experience.getByRole("combobox", { name: "审查判断" });
    await expect(decisionSelect).toBeEnabled({ timeout: 30_000 });
    await decisionSelect.click();
    await chooseVisibleOption(page, "确认风险");
    await experience.getByRole("combobox", { name: "本次审查材料" }).click();
    const materialOption = visibleOption(page, procurementFile);
    await expect(materialOption).toBeVisible();
    await materialOption.evaluate((element) => (element as HTMLElement).click());
    await selectAll(experience.getByLabel("本次审查使用的原文", { exact: true }));
    const useSelection = experience.getByRole("button", { name: "选用选中原文", exact: true });
    await expect(useSelection).toBeEnabled({ timeout: 15_000 });
    await useSelection.click();
    await expect(experience.getByText(/已选证据：/)).toBeVisible();
    await experience.getByLabel("经验建议理由", { exact: true }).fill(`复用验收与付款核对经验 ${stamp}`);
    const submitFeedback = experience.getByRole("button", { name: "提交经验建议并请求提炼", exact: true });
    await expect(submitFeedback).toBeEnabled({ timeout: 15_000 });
    const feedbackWait = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/experience-feedback`), { timeout: 90_000 });
    const distillWait = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/distillations"), { timeout: 90_000 });
    await submitFeedback.click();
    const feedbackResponse = await feedbackWait;
    const feedbackText = await feedbackResponse.text();
    expect(feedbackResponse.status(), `${feedbackResponse.url()}: ${feedbackText}`).toBe(201);
    const feedback = record(JSON.parse(feedbackText));
    if (typeof feedback.feedback_id !== "string" || feedback.feedback_id === "") throw new Error("Experience feedback response did not include feedback_id");
    const feedbackID = String(feedback.feedback_id);
    const distillResponse = await distillWait;
    const distillText = await distillResponse.text();
    expect(distillResponse.status(), `${distillResponse.url()}: ${distillText}`).toBe(202);
    const distill = record(JSON.parse(distillText));
    if (typeof distill.job_id !== "string" || distill.job_id === "") throw new Error("Experience distillation response did not include job_id");
    const jobID = String(distill.job_id);
    const candidateSection = page.locator('section[aria-label="经验候选内容"]');
    await expect(candidateSection).toBeVisible({ timeout: 90_000 });
    await expect(candidateSection.getByText("检查动作", { exact: true })).toBeVisible();
    await expect(candidateSection.getByText("核对验收日期与付款条款", { exact: true })).toBeVisible();
    await expect(candidateSection.getByText("候选仅供后续治理审核，不代表批准或发布。", { exact: true })).toBeVisible();
    await expect(candidateSection).not.toContainText("已批准");
    await expect(candidateSection).not.toContainText("已发布");

    const job = await body(await request.get(`${apiURL}/api/cases/${caseID}/experience-feedback/${encodeURIComponent(feedbackID)}/distillations/${encodeURIComponent(jobID)}`));
    expect(job.state).toBe("completed");
    if (typeof job.candidate_id !== "string" || typeof job.version_id !== "string") throw new Error("Completed distillation did not include candidate identities");
    const candidateID = String(job.candidate_id);
    const candidateVersionID = String(job.version_id);
    const candidate = await body(await request.get(`${apiURL}/api/cases/${caseID}/experience-candidates/${encodeURIComponent(candidateID)}/versions/${encodeURIComponent(candidateVersionID)}`));
    expect(record(candidate.content).check_action).toBe("核对验收日期与付款条款");
    expect(candidate.feedback_id).toBe(feedbackID);
    await testInfo.attach("experience-candidate.png", { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
    await testInfo.attach("experience-candidate-summary.json", { body: JSON.stringify({ fixture_model: "deterministic test double", pack_id: pack.pack.id, policy: publishedPolicy.ref, case_id: caseID, run_id: accepted.id, attempt_id: accepted.attempt_id, finding_id: finding.id, decision_id: decisionID, feedback_id: feedbackID, job_id: jobID, job_status: job.state, candidate_id: candidateID, candidate_version_id: candidateVersionID, candidate_check_action: record(candidate.content).check_action }, null, 2), contentType: "application/json" });
  } finally {
    await adminApi.dispose();
  }
});

test("governed knowledge lifecycle through the admin UI", operations("createSession", "createRulePack", "validateRulePackVersion", "approveRulePackVersion", "publishRulePackVersion", "createGovernedKnowledge", "governedKnowledgeCommand", "getGovernedKnowledgeVersion", "listGovernedKnowledgeHistory"), async ({ page, playwright }, testInfo) => {
  test.setTimeout(360_000);
  page.setDefaultTimeout(30_000);
  const stamp = Date.now();
  const adminApi = await playwright.request.newContext({ extraHTTPHeaders: { Referer: apiURL } });
  try {
    const admin = await login(adminApi, "demo-admin", "admin-demo", "admin");
    expect(admin.audience).toBe("admin");
    const packID = `workflows-ui-pack-${stamp}`;
    const knowledgeID = `workflows-ui-policy-${stamp}`;
    const pack = await publishPack(adminApi, packID, knowledgeID, admin.actor.tenant_id, stamp);
    const draft = await createPolicy(adminApi, pack, knowledgeID, `治理界面制度 ${stamp}`);
    const versionID = draft.ref.version_id;
    await uiLogin(page, "admin");
    await page.goto(`${new URL(adminURL).origin}/admin/governed-knowledge/${encodeURIComponent(knowledgeID)}/versions/${encodeURIComponent(versionID)}`);
    await expect(page.getByRole("heading", { name: "治理操作", exact: true })).toBeVisible();
    const steps = [
      { action: "submit", button: "提交", status: "已提交", expected: "submitted" },
      { action: "validate", button: "校验", status: "已校验", expected: "validated" },
      { action: "approve", button: "批准", status: "已批准", expected: "approved" },
      { action: "publish", button: "发布", status: "已发布", expected: "published" },
    ] as const;
    let finalVersion = draft;
    for (const step of steps) {
      await page.locator("#knowledge-action-reason").fill(`界面治理${step.button} ${stamp}`);
      const commandWait = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/api/admin/governed-knowledge/${encodeURIComponent(knowledgeID)}/versions/${encodeURIComponent(versionID)}/commands`), { timeout: 60_000 });
      await page.getByRole("button", { name: step.button }).click();
      const commandResponse = await commandWait;
      const commandText = await commandResponse.text();
      expect(commandResponse.status(), `${commandResponse.url()}: ${commandText}`).toBe(200);
      await expect(page.getByRole("status").filter({ hasText: `${step.button}操作已由服务端记录。` })).toBeVisible();
      await expect(page.locator(".rulepack-badges").first().getByText(step.status, { exact: true })).toBeVisible();
      finalVersion = parseKnowledgeVersion(await body(await adminApi.get(`${apiURL}/api/admin/governed-knowledge/${encodeURIComponent(knowledgeID)}/versions/${encodeURIComponent(versionID)}`)));
      expect(finalVersion.status).toBe(step.expected);
      await testInfo.attach(`knowledge-${step.action}.json`, { body: JSON.stringify(finalVersion, null, 2), contentType: "application/json" });
    }
    await expect(page.getByText("当前生效", { exact: true })).toBeVisible();
    const historySection = page.locator("h2#knowledge-history-heading").locator("..");
    await expect(historySection.getByText("发布", { exact: true })).toBeVisible();
    for (const label of ["创建", "提交", "校验", "批准", "发布"]) expect(await historySection.locator("strong").allTextContents()).toContain(label);
    expect(await historySection.innerText()).toContain(versionID);
    const historyRaw = await body(await adminApi.get(`${apiURL}/api/admin/governed-knowledge/${encodeURIComponent(knowledgeID)}/history`));
    const historyActions = rows(historyRaw.items).map((item) => String(item.action));
    expect(historyActions).toEqual(expect.arrayContaining(["create", "submit", "validate", "approve", "publish"]));
    await testInfo.attach("governed-knowledge-published.png", { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
    await testInfo.attach("governed-knowledge-summary.json", { body: JSON.stringify({ pack_id: pack.pack.id, pack_content_hash: pack.pack.content_hash, knowledge_id: knowledgeID, version_id: versionID, final_status: finalVersion.status, final_active: finalVersion.active, lifecycle_actions: historyActions }, null, 2), contentType: "application/json" });
  } finally {
    await adminApi.dispose();
  }
});

test("evidence request, receipt, later review and verification", operations("createSession", "createCase", "uploadDocument", "addCaseMember", "startReview", "getWorkspace", "getCase", "getWorkbench", "createWorkbenchTask", "createEvidenceRequest", "submitEvidenceReceipt", "verifyEvidenceRequest", "listEvidenceRequests", "listEvidenceRequestHistory"), async ({ page, request }, testInfo) => {
  test.setTimeout(360_000);
  page.setDefaultTimeout(30_000);
  const stamp = Date.now();
  const client = await login(request);
  expect(client.audience).toBe("client");
  const caseID = await createCase(request, `质保补充 ${stamp}`);
  await upload(request, caseID, "procurement", `warranty-procurement-${stamp}.txt`, "采购文件要求质保36个月。");
  await upload(request, caseID, "draft_contract", `warranty-draft-${stamp}.txt`, "拟签合同未写明质保期限。");
  for (const actorID of ["demo-reviewer", "demo-clerk"]) {
    const response = await request.post(`${apiURL}/api/cases/${caseID}/members`, { data: { actor_id: actorID } });
    const responseText = await response.text();
    expect(response.status(), `${response.url()}: ${responseText}`).toBe(200);
  }
  const source = await startReview(request, caseID);
  const sourceWorkspace = await waitWorkspace(request, caseID, source.id, source.attempt_id);
  const sourceAttempt = sourceWorkspace.review_attempts?.attempt;
  expect(sourceAttempt?.history_kind).toBe("frozen");
  expect(["completed", "partial"]).toContain(sourceAttempt?.state);
  const counts = new Map<string, number>();
  for (const item of sourceWorkspace.evaluations ?? []) counts.set(item.id, (counts.get(item.id) ?? 0) + 1);
  const warranty = (sourceWorkspace.evaluations ?? []).find((item) => item.domain === "warranty" && item.relation === "insufficient" && counts.get(item.id) === 1);
  if (!warranty) throw new Error("Source review did not expose a unique warranty insufficient evaluation");

  const question = `请补充拟签合同中的质保期限并说明依据 ${stamp}`;
  await uiLogin(page);
  await page.goto(`${webURL}/cases/${encodeURIComponent(caseID)}/reviews/${encodeURIComponent(source.id)}?attempt_id=${encodeURIComponent(source.attempt_id)}`);
  await expect(page.getByRole("heading", { name: "审查工作区", exact: true })).toBeVisible();
  const panel = page.locator('section[aria-label="补充材料请求"]');
  await expect(panel).toBeVisible();
  const missingSelect = panel.getByRole("combobox", { name: "缺失检查" });
  await expect(missingSelect).toBeEnabled({ timeout: 60_000 });
  await missingSelect.click();
  const warrantyOption = visibleOption(page, "信息不足");
  await expect(warrantyOption).toContainText("信息不足");
  await warrantyOption.evaluate((element) => (element as HTMLElement).click());
  await panel.getByLabel("任务名称", { exact: true }).fill(`补充质保材料 ${stamp}`);
  await panel.getByRole("combobox", { name: "责任人" }).click();
  await chooseVisibleOption(page, "演示审查员");
  await panel.getByLabel("完成期限（本地时间）", { exact: true }).fill("2027-12-31T18:00");
  await panel.getByLabel("需要补充的问题", { exact: true }).fill(question);
  await panel.getByRole("combobox", { name: "要求的材料类型" }).click();
  await chooseVisibleOption(page, "拟签合同");
  await panel.getByRole("combobox", { name: "核验条件" }).click();
  await chooseVisibleOption(page, "检查结果存在冲突");
  const createRequestButton = panel.getByRole("button", { name: "创建任务并发起请求", exact: true });
  await expect(createRequestButton).toBeEnabled({ timeout: 30_000 });
  const taskWait = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/tasks`), { timeout: 60_000 });
  const requestWait = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/evidence-requests`), { timeout: 60_000 });
  await createRequestButton.click();
  const taskResponse = await taskWait;
  const taskText = await taskResponse.text();
  expect(taskResponse.status(), `${taskResponse.url()}: ${taskText}`).toBe(201);
  const requestResponse = await requestWait;
  const requestText = await requestResponse.text();
  expect(requestResponse.status(), `${requestResponse.url()}: ${requestText}`).toBe(201);
  await expect(panel.getByText("补充材料请求已创建。", { exact: true })).toBeVisible();
  const card = panel.locator("li").filter({ hasText: question }).first();
  await expect(card.getByText("待提交", { exact: true })).toBeVisible();
  const listRaw = await body(await request.get(`${apiURL}/api/cases/${caseID}/evidence-requests`));
  const requestItem = rows(listRaw.items).find((item) => item.question === question);
  if (!requestItem) throw new Error("The new evidence request was not returned by the API");
  const requestID = String(requestItem.id);
  expect(requestItem.status).toBe("open");
  expect(requestItem.material_role).toBe("draft_contract");
  expect(requestItem.verification_condition).toBe("conflict");
  expect(record(requestItem.source_evaluation).id).toBe(warranty.id);
  const supplementFile = `warranty-supplement-${stamp}.txt`;
  const supplementText = "拟签合同约定质保24个月。";
  await upload(request, caseID, "draft_contract", supplementFile, supplementText);

  await page.reload();
  await expect(page.getByRole("heading", { name: "审查工作区", exact: true })).toBeVisible();
  const panelAfterUpload = page.locator('section[aria-label="补充材料请求"]');
  const cardAfterUpload = panelAfterUpload.locator("li").filter({ hasText: question }).first();
  await expect(cardAfterUpload.getByRole("heading", { name: "提交材料或答复", exact: true })).toBeVisible({ timeout: 60_000 });
  await cardAfterUpload.getByRole("combobox", { name: "提交方式" }).click();
  await chooseVisibleOption(page, "案件材料");
  await cardAfterUpload.getByRole("combobox", { name: "选择案件材料" }).click();
  const documentOption = visibleOption(page, supplementFile);
  await expect(documentOption).toBeVisible({ timeout: 60_000 });
  await documentOption.evaluate((element) => (element as HTMLElement).click());
  const receiptButton = cardAfterUpload.getByRole("button", { name: "提交回执", exact: true });
  await expect(receiptButton).toBeEnabled();
  const receiptWait = page.waitForResponse((response) => response.request().method() === "POST" && response.url().includes(`/api/cases/${caseID}/evidence-requests/${requestID}/receipts`), { timeout: 60_000 });
  await receiptButton.click();
  const receiptResponse = await receiptWait;
  const receiptText = await receiptResponse.text();
  expect(receiptResponse.status(), `${receiptResponse.url()}: ${receiptText}`).toBe(201);
  const receiptID = String(record(record(JSON.parse(receiptText)).receipt).id);
  await expect(cardAfterUpload.getByText("已提交，待复核", { exact: true })).toBeVisible({ timeout: 30_000 });

  await expect(cardAfterUpload.getByRole("heading", { name: "发起后续审查", exact: true })).toBeVisible();
  const startLaterWait = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/reviews`), { timeout: 60_000 });
  await cardAfterUpload.getByRole("button", { name: "发起后续审查", exact: true }).click();
  const startLaterResponse = await startLaterWait;
  const startLaterText = await startLaterResponse.text();
  expect([201, 202], `${startLaterResponse.url()}: ${startLaterText}`).toContain(startLaterResponse.status());
  const later = parseAcceptedReview(JSON.parse(startLaterText));
  await page.waitForURL((url) => url.pathname.includes(`/reviews/${later.id}`), { timeout: 30_000 });
  const laterWorkspace = await waitWorkspace(request, caseID, later.id, later.attempt_id);
  expect(laterWorkspace.review_attempts?.attempt?.history_kind).toBe("frozen");
  expect(["completed", "partial"]).toContain(laterWorkspace.review_attempts?.attempt?.state);
  const conflict = (laterWorkspace.evaluations ?? []).find((item) => item.domain === "warranty" && item.relation === "conflict");
  if (!conflict) throw new Error("Later review did not expose a warranty conflict evaluation");

  await page.reload();
  await expect(page.getByRole("heading", { name: "审查工作区", exact: true })).toBeVisible();
  const laterPanel = page.locator('section[aria-label="补充材料请求"]');
  const laterCard = laterPanel.locator("li").filter({ hasText: question }).first();
  await expect(laterCard.getByText("已提交，待复核", { exact: true })).toBeVisible({ timeout: 60_000 });
  await expect(laterCard.getByText("当前审查的匹配检查：检查结果存在冲突。", { exact: true })).toBeVisible({ timeout: 60_000 });
  await laterCard.getByRole("combobox", { name: "选择提交回执" }).click();
  await expect(visibleOption(page, supplementFile)).toBeVisible({ timeout: 30_000 });
  await page.keyboard.press("Enter");
  await laterCard.getByRole("combobox", { name: "选择本次审查材料" }).click();
  await expect(visibleOption(page, supplementFile)).toBeVisible({ timeout: 30_000 });
  await page.keyboard.press("Enter");
  await selectAll(laterCard.getByLabel("本次审查使用的原文", { exact: true }));
  await expect(laterCard.getByText(/已选引用：/)).toBeVisible();
  await expect(laterCard.locator("blockquote").filter({ hasText: supplementText })).toBeVisible();
  await laterCard.getByLabel("核验理由", { exact: true }).fill(`核对回执材料与冻结原文 ${stamp}`);
  const verifyButton = laterCard.getByRole("button", { name: "提交核验", exact: true });
  await expect(verifyButton).toBeEnabled({ timeout: 30_000 });
  const verifyWait = page.waitForResponse((response) => response.request().method() === "POST" && response.url().includes(`/api/cases/${caseID}/evidence-requests/${requestID}/receipts/${receiptID}/verify`), { timeout: 60_000 });
  await verifyButton.click();
  const verifyResponse = await verifyWait;
  const verifyText = await verifyResponse.text();
  expect(verifyResponse.status(), `${verifyResponse.url()}: ${verifyText}`).toBe(200);
  await expect(laterCard.getByText("已核验", { exact: true })).toBeVisible({ timeout: 30_000 });

  await page.reload();
  await expect(page.getByRole("heading", { name: "审查工作区", exact: true })).toBeVisible();
  const finalPanel = page.locator('section[aria-label="补充材料请求"]');
  const finalCard = finalPanel.locator("li").filter({ hasText: question }).first();
  await expect(finalCard.getByText("已核验", { exact: true })).toBeVisible({ timeout: 60_000 });
  const historyHeader = page.getByText("处理记录", { exact: true }).first();
  if (await historyHeader.count()) await historyHeader.click({ force: true });
  await page.waitForTimeout(500);
  const finalText = await page.locator("body").innerText();
  expect(finalText).toContain("创建请求");
  expect(finalText).toContain("提交回执");
  expect(finalText).toContain("完成核验");
  const historyRaw = await body(await request.get(`${apiURL}/api/cases/${caseID}/evidence-requests/${requestID}/history`));
  const eventTypes = rows(historyRaw.items).map((item) => String(item.type));
  expect(eventTypes).toEqual(expect.arrayContaining(["created", "received", "verified"]));
  const finalListRaw = await body(await request.get(`${apiURL}/api/cases/${caseID}/evidence-requests`));
  const finalItem = rows(finalListRaw.items).find((item) => item.id === requestID);
  expect(finalItem?.status).toBe("verified");
  const verification = record(finalItem?.verification);
  expect(verification.relation).toBe("conflict");
  const panelText = await finalPanel.innerText();
  expect(panelText).not.toMatch(/parsed_/i);
  expect(panelText).not.toMatch(/normalized/i);
  expect(panelText).not.toMatch(/sha-?256/i);
  for (const document of sourceWorkspace.documents) {
    if (document.parsed_document_id) expect(panelText).not.toContain(document.parsed_document_id);
    if (document.parsed_document?.normalized_digest) expect(panelText).not.toContain(document.parsed_document.normalized_digest);
  }
  await testInfo.attach("evidence-request-verified.png", { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
  await testInfo.attach("evidence-request-summary.json", { body: JSON.stringify({ case_id: caseID, source_run_id: source.id, source_attempt_id: source.attempt_id, source_evaluation_id: warranty.id, source_evaluation_relation: warranty.relation, request_id: requestID, request_status: finalItem?.status, question, material_role: "draft_contract", verification_condition: "conflict", receipt_id: receiptID, later_run_id: later.id, later_attempt_id: later.attempt_id, later_evaluation_id: conflict.id, event_types: eventTypes, verification_relation: verification.relation, privacy_checks: ["no parsed_* identifiers", "no normalized digests", "no SHA-256 text"] }, null, 2), contentType: "application/json" });
});

test("report business view hides raw identities", operations("createSession", "createRulePack", "validateRulePackVersion", "approveRulePackVersion", "publishRulePackVersion", "createGovernedKnowledge", "governedKnowledgeCommand", "createCase", "uploadDocument", "startReview", "getWorkspace", "createReport", "readReport", "getWorkbench"), async ({ page, request, playwright }, testInfo) => {
  test.setTimeout(360_000);
  page.setDefaultTimeout(30_000);
  const stamp = Date.now();
  const adminApi = await playwright.request.newContext({ extraHTTPHeaders: { Referer: apiURL } });
  try {
    const admin = await login(adminApi, "demo-admin", "admin-demo", "admin");
    await disableActiveGovernance(adminApi);
    const packID = `workflows-report-pack-${stamp}`;
    const knowledgeID = `workflows-report-policy-${stamp}`;
    const pack = await publishPack(adminApi, packID, knowledgeID, admin.actor.tenant_id, stamp);
    const policy = await createPolicy(adminApi, pack, knowledgeID, `报告隐私制度 ${stamp}`);
    await advancePolicy(adminApi, policy, ["submit", "validate", "approve", "publish"], `report-policy-${stamp}`);
    const client = await login(request);
    expect(client.audience).toBe("client");
    const caseID = await createCase(request, `报告隐私 ${stamp}`, true);
    await upload(request, caseID, "procurement", `report-procurement-${stamp}.txt`, "采购文件要求电路 LINE-001 的最低带宽为100 Mbit/s，SLA 可用率99.9%，预付款上限30%。");
    await upload(request, caseID, "draft_contract", `report-draft-${stamp}.txt`, "拟签合同约定电路 LINE-001 的最低带宽为50 Mbit/s，SLA 可用率99.5%，预付款比例40%。");
    const startedResponse = await request.post(`${apiURL}/api/cases/${caseID}/reviews`, { data: { business_at: "2026-02-03T01:05:06Z" }, headers: { "Idempotency-Key": `report-review-${stamp}` } });
    const startedText = await startedResponse.text();
    expect([201, 202], `${startedResponse.url()}: ${startedText}`).toContain(startedResponse.status());
    const accepted = parseAcceptedReview(JSON.parse(startedText));
    await waitWorkspace(request, caseID, accepted.id, accepted.attempt_id);
  const report = parseReportRecord(await body(await request.post(`${apiURL}/api/cases/${caseID}/reports`, { data: { run_id: accepted.id, attempt_id: accepted.attempt_id }, headers: { "Idempotency-Key": `report-create-${stamp}` } }), 201));
  const caseTitle = (await detail(request, caseID)).title;
  await uiLogin(page);
  await page.goto(`${webURL}/workbench/reports`);
  await expect(page.getByRole("heading", { name: /报告中心/ })).toBeVisible();
  const reportList = page.locator('section[aria-label="报告中心记录"]');
  const reportTitle = `${caseTitle}，审查报告 v${report.version}`;
  await reportList.getByRole("button", { name: reportTitle, exact: true }).click({ timeout: 60_000 });
  const aside = page.locator('aside[aria-label="所选记录详情"]');
  await expect(aside.getByRole("heading", { name: reportTitle, exact: true })).toBeVisible();
  await expect(aside.getByText("报告版本", { exact: true })).toBeVisible();
  await expect(aside).toContainText(caseTitle);
  await expect(aside.getByText("生成时间", { exact: true })).toBeVisible();
  const htmlLink = aside.getByRole("link", { name: "查看 HTML 报告", exact: true });
  const jsonLink = aside.getByRole("link", { name: "查看 JSON 报告", exact: true });
  await expect(htmlLink).toBeVisible();
  await expect(jsonLink).toBeVisible();
  const asideText = await aside.innerText();
  for (const forbidden of ["报告 ID", report.id, report.run_id, report.attempt_id, "输入摘要", "输出摘要", "报告摘要", report.input_hash, report.output_hash, report.decision_snapshot_hash, report.digest]) {
    expect(asideText, `report business view must not expose ${forbidden}`).not.toContain(forbidden);
  }
  expect(asideText).not.toMatch(/\b[0-9a-f]{64}\b/i);
  const htmlHref = await htmlLink.getAttribute("href");
  const jsonHref = await jsonLink.getAttribute("href");
  expect(htmlHref).toBe(paths.report(caseID, report.id, "html"));
  expect(jsonHref).toBe(paths.report(caseID, report.id, "json"));
  if (!htmlHref) throw new Error("Frozen HTML link has no href");
  const firstHTML = await request.get(`${apiURL}${htmlHref}`);
  if (firstHTML.status() !== 200) expect(firstHTML.status(), `${firstHTML.url()}: ${await firstHTML.text()}`).toBe(200);
  const firstBytes = await firstHTML.body();
  const secondHTML = await request.get(`${apiURL}${htmlHref}`);
  if (secondHTML.status() !== 200) expect(secondHTML.status(), `${secondHTML.url()}: ${await secondHTML.text()}`).toBe(200);
  const secondBytes = await secondHTML.body();
  expect(firstBytes.length).toBeGreaterThan(0);
  expect(secondBytes.equals(firstBytes)).toBe(true);
  await testInfo.attach("report-business-view.png", { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
  await testInfo.attach("report-privacy-summary.json", { body: JSON.stringify({ case_id: caseID, report_id: report.id, run_id: report.run_id, attempt_id: report.attempt_id, report_version: report.version, html_href: htmlHref, json_href: jsonHref, html_status: firstHTML.status(), html_bytes: firstBytes.length, html_byte_stable: secondBytes.equals(firstBytes), hidden_from_business_view: ["report id", "run id", "attempt id", "input hash", "output hash", "decision snapshot hash", "report digest", "输入摘要", "输出摘要", "报告摘要"] }, null, 2), contentType: "application/json" });
  } finally {
    await adminApi.dispose();
  }
});
