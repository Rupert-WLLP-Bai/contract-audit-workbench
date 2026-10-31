import { chromiumExecutable } from "./chromium";
import { C1B_BUSINESS_AT_UTC, C1B_BUSINESS_TIME_ZONE, enterC1BBusinessAt } from "./c1b-clock";
import { parseReviewWorkspace } from "../src/api/parse";
import { readFrozenGovernance } from "./c1b-frozen-governance";
import type { ConstraintEvaluation, ReviewWorkspace } from "../src/api/types";
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium, type Page } from "playwright";
import { replacePrimaryMaterial, selectMaterialOption, uploadNewMaterial } from "./materials-ui";
import { selectReportOption, selectReviewAttempt } from "./c1b-select";

type Material = { role: string; filename: string; text: string };
type Scenario = {
  id: string;
  title: string;
  expected_relations: Record<string, string>;
  fields: Record<string, string>;
  materials: Material[];
  draft_override?: { filename: string; text: string };
};
type Dataset = {
  dataset_id: string;
  data_class: string;
  template_id: string;
  template_version: string;
  sha256: string;
  scenarios: Scenario[];
};
type CaseDetail = {
  id: string;
  title: string;
  business_line: string;
  scenario: string;
  generations: Array<Record<string, any>>;
  documents: Array<Record<string, any>>;
  materials?: Array<{ material_id: string; purpose: string; current_document_id: string; current_version: number }>;
  reports: Array<{ id: string; run_id: string; attempt_id: string; version: number; created_at: string }>;
  archives: Array<{ id: string; report_id: string }>;
  archived_at: string | null;
};

function required(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}

const web = required("E2E_WEB").replace(/\/$/, "");
const datasetPath = required("E2E_TELECOM_DATASET");
const scenarioIDs = (process.env.E2E_TELECOM_SCENARIOS ?? "compatible,insufficient")
  .split(",").map((value) => value.trim()).filter(Boolean);
const resumeReportID = (process.env.E2E_RESUME_REPORT_ID ?? "").trim();
const resumeInsufficientCaseID = (process.env.E2E_RESUME_INSUFFICIENT_CASE_ID ?? "").trim();
const resumeInsufficientRunID = (process.env.E2E_RESUME_INSUFFICIENT_RUN_ID ?? "").trim();
const sourceSHA = (process.env.E2E_SOURCE_SHA ?? "").trim();
const serverSHA = (process.env.E2E_SERVER_SHA ?? "").trim();
const executionSHA = (process.env.E2E_EXECUTION_SHA ?? "").trim();
const outDir = resolve(process.env.E2E_OUT ?? `../artifacts/d2-8-browser/c1b-scenarios-${Date.now()}`);
const username = process.env.E2E_USERNAME || "demo-admin";
const password = process.env.E2E_PASSWORD || "admin-demo";
const browser = await chromium.launch({
  headless: true,
  executablePath: chromiumExecutable(),
  args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
});
const page = await browser.newPage({
  viewport: { width: 1440, height: 1000 },
  timezoneId: C1B_BUSINESS_TIME_ZONE,
});
const consoleErrors: string[] = [];
const httpErrors: string[] = [];
const expectedHTTPFailures = new Set(["GET /api/session 401"]);
const decisionHTTPTrace: Array<{ method: string; path: string; status?: number }> = [];
const results: Array<Record<string, unknown>> = [];

async function submitFindingDecision(targetPage: Page, findingID: string, claim: string): Promise<void> {
  await targetPage.getByRole("button", { name: `选择线索：${claim}`, exact: true }).click();
  await targetPage.waitForURL((url) => url.searchParams.get("finding") === findingID);
  const detail = targetPage.locator(".finding-detail");
  const action = detail.locator(`input[type="radio"][name="decision-${findingID}"][value="insufficient_evidence"]`);
  await action.waitFor({ state: "attached", timeout: 10_000 });
  assert.equal(await action.count(), 1, `${findingID} decision control is not uniquely rendered`);
  await action.check();
  await detail.getByLabel("理由", { exact: true }).fill("当前材料没有证明同一电路对象达到采购指标，保留为证据不足。");
  const decisionResponse = targetPage.waitForResponse((response) => {
    if (response.request().method() !== "POST") return false;
    const path = new URL(response.url()).pathname;
    return path.includes("/findings/") && path.endsWith("/decisions");
  });
  await detail.getByRole("button", { name: "保存决定", exact: true }).click();
  const response = await decisionResponse;
  const path = new URL(response.url()).pathname;
  assert.ok(path.endsWith(`/findings/${findingID}/decisions`), `decision targeted a different finding: ${path}`);
  assert.equal(response.status(), 204, "insufficient evidence decision was not recorded");
  await targetPage.getByText("已记录证据不足", { exact: false }).waitFor();
}
page.on("console", (message) => {
  if (message.type() === "error") consoleErrors.push(message.text());
});
page.on("pageerror", (error) => consoleErrors.push(error.message));
page.on("request", (request) => {
  const pathname = new URL(request.url()).pathname;
  if (pathname.includes("/findings/") && pathname.endsWith("/decisions")) decisionHTTPTrace.push({ method: request.method(), path: pathname });
});
page.on("response", (response) => {
  const pathname = new URL(response.url()).pathname;
  if (pathname.includes("/findings/") && pathname.endsWith("/decisions")) decisionHTTPTrace.push({ method: response.request().method(), path: pathname, status: response.status() });
  if (response.status() >= 400) httpErrors.push(`${response.request().method()} ${pathname} ${response.status()}`);
});

let exitCode = 0;
try {
  const dataset = await loadDataset(datasetPath);
  assert.equal(dataset.dataset_id, "telecom-c1b-v1");
  assert.ok(dataset.sha256, "frozen scenario dataset has no digest");
  assert.ok(scenarioIDs.length > 0, "no browser scenarios were selected");
  await signIn(page);

  for (const scenarioID of scenarioIDs) {
    const scenario = dataset.scenarios.find((item) => item.id === scenarioID);
    assert.ok(scenario, `scenario ${scenarioID} missing from the frozen dataset`);
    assert.ok(scenario.id === "compatible" || scenario.id === "insufficient", `scenario ${scenario.id} is outside this smoke's non-conflict scope`);
    if (scenario.id === "compatible" && resumeReportID !== "") {
      const existingDetail = await findCaseByReport(page, scenario.title, resumeReportID);
      const existingResult: Record<string, unknown> = { scenario: scenario.id, case_id: existingDetail.id, report_id: resumeReportID, stage: "existing_report" };
      results.push(existingResult);
      await finishExistingScenario(page, dataset, scenario, existingDetail, resumeReportID, existingResult, expectedHTTPFailures);
      continue;
    }
    if (scenario.id === "insufficient" && resumeInsufficientCaseID !== "" && resumeInsufficientRunID !== "") {
      const existingDetail = await waitForCase(page, resumeInsufficientCaseID, (detail) => detail.id === resumeInsufficientCaseID);
      const existingResult: Record<string, unknown> = { scenario: scenario.id, case_id: existingDetail.id, run_id: resumeInsufficientRunID, stage: "existing_review" };
      results.push(existingResult);
      await finishExistingInsufficientScenario(page, dataset, scenario, existingDetail, resumeInsufficientRunID, existingResult, expectedHTTPFailures);
      continue;
    }
    await page.goto(`${web}/cases`);
    await page.getByRole("heading", { name: "案件", exact: true }).waitFor();
    const title = `${scenario.title} · Chromium ${Date.now()}`;
    await page.getByRole("button", { name: "新建案件", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("标题", { exact: true }).fill(title);
    await selectMaterialOption(page, "合同类型", "服务合同");
    await dialog.getByRole("button", { name: "创建", exact: true }).click();
    await page.waitForURL(/\/cases\/[^/]+$/);
    const caseID = new URL(page.url()).pathname.split("/").at(-1) ?? "";
    assert.ok(caseID, "created case ID was not present in the route");
    const scenarioResult: Record<string, unknown> = { scenario: scenario.id, title, case_id: caseID, stage: "case_created" };
    results.push(scenarioResult);
    const initialDetail = await waitForCase(page, caseID, (detail) => detail.business_line === "ENT" && detail.scenario === "telecom_dedicated_line");
    assert.equal(initialDetail.title, title);

    const templateResponse = await page.request.get(`${web}/api/templates`);
    assert.equal(templateResponse.status(), 200, "template listing failed");
    const templateData = await templateResponse.json() as { items: Array<{ id: string; name: string; version: string; fields: Array<{ key: string; label: string; required: boolean }> }> };
    const template = templateData.items.find((item) => item.id === dataset.template_id);
    assert.ok(template, `template ${dataset.template_id} not returned by the API`);
    assert.equal(template.version, dataset.template_version, "template version differs from the frozen dataset");
    assert.deepEqual(new Set(template.fields.map((field) => field.key)), new Set(Object.keys(scenario.fields)));
    await selectMaterialOption(page, "模板", `${template.name}，${template.version}`);
    for (const field of template.fields) {
      const value = scenario.fields[field.key];
      assert.notEqual(value, undefined, `fixture field ${field.key} is missing`);
      const control = page.getByLabel(field.required ? `${field.label}（必填）` : field.label, { exact: true });
      if (await control.evaluate((element) => element.tagName) === "SELECT") await control.selectOption(value!);
      else await control.fill(value!);
    }
    const generationResponsePromise = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/generations"));
    await page.getByRole("button", { name: "生成待审草稿", exact: true }).click();
    const generationResponse = await generationResponsePromise;
    assert.equal(generationResponse.status(), 201, "generation did not return HTTP 201");
    const generation = await generationResponse.json() as { id?: string; document_id?: string };
    assert.ok(generation.id && generation.document_id, "generation did not return draft identities");
    Object.assign(scenarioResult, { generation_id: generation.id, generated_document_id: generation.document_id, stage: "generated" });
    await waitForCase(page, caseID, (detail) => detail.generations.some((item) => item.id === generation.id));

    const uploaded: Record<string, string> = {};
    for (const material of scenario.materials) uploaded[material.role] = await uploadMaterial(page, material);
    if (scenario.draft_override) {
      const beforeReplacement = await waitForCase(page, caseID, (detail) =>
        detail.materials?.some((material) => material.purpose === "primary_contract" && material.current_document_id === generation.document_id) === true,
      );
      const primaryMaterial = beforeReplacement.materials?.find((material) => material.purpose === "primary_contract");
      assert.ok(primaryMaterial, `${scenario.id} generated primary contract has no material identity`);
      const currentDocument = beforeReplacement.documents.find((document) => document.id === primaryMaterial.current_document_id);
      assert.ok(currentDocument, `${scenario.id} current primary document is absent from case detail`);
      const result = await replacePrimaryMaterial(page, {
        role: "draft_contract",
        filename: scenario.draft_override.filename,
        text: scenario.draft_override.text,
      }, primaryMaterial, String(currentDocument.filename ?? ""));
      uploaded.draft_contract = result.id;
    }
    assert.ok(uploaded.procurement, `${scenario.id} procurement evidence was not uploaded`);
    const detailWithMaterials = await waitForCase(page, caseID, (detail) =>
      Object.values(uploaded).every((documentID) => detail.documents.some((document) => document.id === documentID)));
    Object.assign(scenarioResult, { uploaded_document_ids: uploaded, case_document_count: detailWithMaterials.documents.length, stage: "materials_ready" });

    await enterC1BBusinessAt(page);
    const startReviewResponsePromise = page.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/reviews`));
    await page.getByRole("button", { name: "开始审查", exact: true }).click();
    const startReviewResponse = await startReviewResponsePromise;
    assert.ok(startReviewResponse.status() === 201 || startReviewResponse.status() === 202, `${scenario.id} review start returned HTTP ${startReviewResponse.status()}`);
    assert.deepEqual(startReviewResponse.request().postDataJSON(), { business_at: C1B_BUSINESS_AT_UTC }, `${scenario.id} did not submit the frozen business time`);
    await page.waitForURL(/\/cases\/[^/]+\/reviews\/[^/]+/);
    const reviewURL = new URL(page.url());
    const runID = reviewURL.pathname.split("/").at(-1) ?? "";
    assert.ok(runID, `${scenario.id} review run ID was not in the route`);
    const workspace = await waitForWorkspace(page, caseID, runID);
    const attemptID = workspace.review_attempts?.attempt?.id ?? "";
    assert.ok(attemptID, `${scenario.id} review has no attempt identity`);
    const frozenGovernance = await readFrozenGovernance(page, web, caseID, runID, attemptID);
    const rulepack = frozenGovernance.rulepack;
    if (workspace.agent_review?.rulepack) {
      assert.equal(workspace.agent_review.rulepack.id, rulepack.id, `${scenario.id} workspace rulepack ID differs from its frozen snapshot`);
      assert.equal(workspace.agent_review.rulepack.version, rulepack.version, `${scenario.id} workspace rulepack version differs from its frozen snapshot`);
    }
    assertRelations(workspace, scenario.expected_relations);
    Object.assign(scenarioResult, {
      run_id: runID,
      attempt_id: attemptID,
      run_state: workspace.run.state,
      finding_count: workspace.findings.length,
      business_at: C1B_BUSINESS_AT_UTC,
      business_time_zone: C1B_BUSINESS_TIME_ZONE,
      rulepack,
      expected_relations: scenario.expected_relations,
      stage: "reviewed",
    });
    const expectedWorkspaceDocumentIDs = scenario.draft_override
      ? Object.values(uploaded)
      : [...Object.values(uploaded), generation.document_id!];
    const missingWorkspaceDocumentIDs = expectedWorkspaceDocumentIDs.filter((id) => !workspace.documents.some((document) => document.id === id));
    assert.deepEqual(missingWorkspaceDocumentIDs, [], `${scenario.id} review omitted expected material identities`);

    let decisionFindingID: string | null = null;
    if (scenario.id === "compatible") {
      assert.equal(workspace.findings.length, 0, "compatible scenario unexpectedly produced findings");
    } else {
      assert.ok(workspace.findings.length > 0, "insufficient scenario produced no findings");
      const bandwidthEvaluation = workspaceEvaluations(workspace).find((item) => item.rule_id === "ENT-TECH-BW-001");
      assert.equal(bandwidthEvaluation?.relation, "insufficient");
      decisionFindingID = String(bandwidthEvaluation?.finding_id ?? "");
      assert.ok(decisionFindingID, "insufficient bandwidth result is not linked to a finding");
      const finding = workspace.findings.find((item) => item.id === decisionFindingID);
      assert.ok(finding, "insufficient bandwidth finding is absent from the workspace");
      assert.ok(typeof finding.claim === "string" && finding.claim.trim() !== "", "insufficient bandwidth finding has no claim");
      const evidence = finding.evidence;
      assert.ok(evidence.length > 0, "insufficient bandwidth finding has no evidence trace");
      assert.ok(evidence.some((item) => item.document_version_id === uploaded.procurement), "insufficient finding does not preserve the procurement source identity");
      try {
        await submitFindingDecision(page, decisionFindingID, finding.claim);
      } catch (cause) {
        const decisionDOM = await page.evaluate(() => ({
          url: location.href,
          radios: Array.from(document.querySelectorAll<HTMLInputElement>('input[type="radio"]')).map((radio) => ({ name: radio.name, value: radio.value, checked: radio.checked, disabled: radio.disabled, group: radio.closest('[role="group"]')?.getAttribute('aria-label') ?? null })),
          buttons: Array.from(document.querySelectorAll<HTMLButtonElement>('button')).filter((button) => (button.innerText ?? "").includes("保存决定") || button.getAttribute("aria-label") === "保存决定").map((button) => ({ text: button.innerText, ariaLabel: button.getAttribute("aria-label"), disabled: button.disabled, outerHTML: button.outerHTML.slice(0, 3000) })),
          reasonFields: Array.from(document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>('input,textarea')).filter((field) => field.value.includes("当前材料没有证明同一电路对象")).map((field) => ({ value: field.value, tag: field.tagName, disabled: field.disabled, ariaLabel: field.getAttribute("aria-label") })),
          alerts: Array.from(document.querySelectorAll<HTMLElement>('[role="alert"],[role="status"]')).map((element) => element.innerText),
          visibleText: document.body.innerText.slice(0, 18000),
        }));
        const evidenceDirectory = resolve(process.env.CAW_DELIVERY_DIR ?? outDir, "browser-evidence");
        await mkdir(evidenceDirectory, { recursive: true });
        await writeFile(resolve(evidenceDirectory, `insufficient-decision-${caseID}-${runID}-${attemptID}.json`), `${JSON.stringify({ case_id: caseID, run_id: runID, attempt_id: attemptID, decision_finding_id: decisionFindingID, request_trace: decisionHTTPTrace, http_errors: httpErrors, dom: decisionDOM, error: cause instanceof Error ? cause.message : String(cause) }, null, 2)}\n`, "utf8");
        throw cause;
      }
    }
    scenarioResult.decision_finding_id = decisionFindingID;

    await page.goto(`${web}/cases/${caseID}`);
    await page.getByRole("heading", { name: title, exact: true }).waitFor();
    await selectReviewAttempt(page, web, caseID, runID, attemptID, page.getByLabel("选择审查和执行记录", { exact: true }));
    const reportResponse = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/reports"));
    await page.getByRole("button", { name: "生成报告", exact: true }).click();
    const createdReport = await reportResponse;
    assert.ok(createdReport.status() === 200 || createdReport.status() === 201, `report creation returned HTTP ${createdReport.status()}`);
    const reportDetail = await waitForCase(page, caseID, (detail) => detail.reports.some((report) => report.run_id === runID && report.attempt_id === attemptID));
    const report = reportDetail.reports.find((item) => item.run_id === runID && item.attempt_id === attemptID);
    assert.ok(report, `${scenario.id} frozen report was not returned`);
    scenarioResult.report_id = report.id;
    const reportBytes = await readReportLinks(page, report.id);
    assert.ok(reportBytes.json.length > 0 && reportBytes.html.length > 0, `${scenario.id} frozen report is empty`);
    const frozenJSON = JSON.parse(reportBytes.json.toString("utf8")) as Record<string, unknown>;
    const reportRelations = collectRelations(frozenJSON);
    for (const relation of Object.values(scenario.expected_relations)) assert.ok(reportRelations.includes(relation), `frozen report omitted ${relation} for ${scenario.id}`);

    await selectReportOption(page, page.getByLabel("归档所依据的报告", { exact: true }), report);
    await page.getByLabel("归档理由", { exact: true }).fill(`已完成 ${scenario.id} 场景审查并归档冻结报告。`);
    const archiveResponse = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/archive`));
    await page.getByRole("button", { name: "归档案件", exact: true }).click();
    assert.equal((await archiveResponse).status(), 200, `${scenario.id} archive action was not accepted`);
    const archived = await waitForCase(page, caseID, (detail) => Boolean(detail.archived_at) && detail.archives.some((item) => item.report_id === report.id));
    const afterArchiveBytes = await readReportLinks(page, report.id);
    assert.deepEqual(afterArchiveBytes.json, reportBytes.json, `${scenario.id} archive changed frozen JSON bytes`);
    assert.deepEqual(afterArchiveBytes.html, reportBytes.html, `${scenario.id} archive changed frozen HTML bytes`);

    const deniedWrite = await page.request.post(`${web}/api/cases/${caseID}/generations`, {
      data: { template_id: dataset.template_id, fields: scenario.fields },
    });
    assert.equal(deniedWrite.status(), 409, `${scenario.id} archived case accepted a new generation`);
    expectedHTTPFailures.add(`POST /api/cases/${caseID}/generations 409`);
    await page.goto(`${web}/cases/${caseID}/reviews/${runID}?attempt_id=${encodeURIComponent(attemptID)}`);
    await page.getByRole("heading", { name: "审查工作区", exact: true }).waitFor();
    const archivedReviewBodyText = await page.locator("body").innerText();
    const archivedReviewWorkspaceResponse = await page.request.get(`${web}/api/cases/${caseID}/reviews/${runID}?attempt_id=${encodeURIComponent(attemptID)}`);
    const archivedReviewWorkspaceRaw = await archivedReviewWorkspaceResponse.text();
    const historyEvidenceDirectory = resolve(process.env.CAW_DELIVERY_DIR ?? outDir, "browser-evidence");
    await mkdir(historyEvidenceDirectory, { recursive: true });
    await writeFile(resolve(historyEvidenceDirectory, `archived-history-${caseID}-${runID}-${attemptID}.json`), `${JSON.stringify({
      page_url: page.url(),
      expected_run_id: runID,
      expected_attempt_id: attemptID,
      page_text: archivedReviewBodyText.slice(0, 24000),
      workspace_http_status: archivedReviewWorkspaceResponse.status(),
      workspace_raw: archivedReviewWorkspaceRaw.slice(0, 60000),
    }, null, 2)}\n`, "utf8");
    assert.equal(archivedReviewWorkspaceResponse.status(), 200, `${scenario.id} archived historical workspace was not readable`);
    const archivedReviewWorkspace = parseReviewWorkspace(JSON.parse(archivedReviewWorkspaceRaw));
    assert.equal(archivedReviewWorkspace.run.id, runID, `${scenario.id} archived workspace returned a different review run`);
    assert.equal(archivedReviewWorkspace.review_attempts?.attempt?.id, attemptID, `${scenario.id} archived workspace returned a different review attempt`);
    assert.equal(archivedReviewWorkspace.run.state, "awaiting_human", `${scenario.id} archived workspace did not retain its awaiting-human run state`);
    assert.equal(archivedReviewWorkspace.review_attempts?.attempt?.state, "completed", `${scenario.id} archived workspace did not retain its completed attempt state`);
    const archivedFrozenDocuments = expectedWorkspaceDocumentIDs.map((documentID) => archivedReviewWorkspace.documents.find((document) => document.id === documentID));
    assert.ok(archivedFrozenDocuments.every((document) => document && document.parsed_document_id && document.parse_status === "ready"), `${scenario.id} archived workspace omitted a frozen parsed material or its ready status`);
    assert.ok(archivedReviewBodyText.includes("合同与证据原文"), `${scenario.id} archived review page omitted the frozen source section`);
    const visiblePrimaryDocument = archivedFrozenDocuments.find((document) => document?.role === "draft_contract");
    assert.ok(visiblePrimaryDocument, `${scenario.id} archived workspace omitted its frozen primary contract`);
    const primaryParseID = visiblePrimaryDocument.parsed_document_id ?? visiblePrimaryDocument.parsed_document?.id ?? "";
    assert.ok(primaryParseID, `${scenario.id} archived primary has no frozen parse identity`);
    const primaryTab = page.locator(`.ant-tabs-tab [data-document-version-id="${visiblePrimaryDocument.id}"][data-parsed-document-id="${primaryParseID}"]`);
    await primaryTab.click();
    const primaryPane = page.locator(`.doc-pane[data-document-version-id="${visiblePrimaryDocument.id}"][data-parsed-document-id="${primaryParseID}"][data-active-source="true"]`);
    await primaryPane.waitFor({ state: "visible" });
    const primaryPaneText = await primaryPane.innerText();
    assert.ok(primaryPaneText.includes(visiblePrimaryDocument.filename), `${scenario.id} archived review page omitted its selected frozen primary filename`);
    assert.ok(primaryPaneText.includes("已解析"), `${scenario.id} archived review page omitted its selected frozen parse status`);
    for (const roleLabel of ["拟签合同", "采购文件", "中标文件", "技术附件"]) assert.ok(archivedReviewBodyText.includes(roleLabel), `${scenario.id} archived review page omitted material role ${roleLabel}`);
    assert.ok(archivedReviewBodyText.includes(title), `${scenario.id} archived review page omitted its visible case title`);
    assert.ok(archivedReviewBodyText.includes("案件已归档，历史审查仅供查看。"), `${scenario.id} archived review page omitted the read-only history notice`);
    await assertArchivedReviewWriteControls(page, scenario.id, scenario.id === "insufficient");
    assert.equal(archived.archives.at(-1)?.report_id, report.id);

    Object.assign(scenarioResult, {
      archive_id: archived.archives.at(-1)?.id,
      archive_read_only_status: deniedWrite.status(),
      frozen_report_unchanged_after_archive: true,
      stage: "archived_and_read_only",
    });
  }

  const expectedUnauthorizedConsole = "Failed to load resource: the server responded with a status of 401 (Unauthorized)";
  assert.deepEqual(consoleErrors.filter((message) => message !== expectedUnauthorizedConsole), [], "unexpected browser console errors");
  assert.deepEqual(httpErrors.filter((failure) => !expectedHTTPFailures.has(failure)), [], "unexpected HTTP failures");
  console.log(JSON.stringify({
    result: "real Chromium compatible and insufficient C1-B scenarios passed",
    source_sha: sourceSHA || "NOT_RECORDED",
    server_sha: serverSHA || "NOT_RECORDED",
    execution_sha: executionSHA || "NOT_RECORDED",
    dataset_id: dataset.dataset_id,
    dataset_sha256: dataset.sha256,
    scenarios: results,
    console_errors: consoleErrors,
    http_errors: httpErrors,
    expected_unauthenticated_session_probe: httpErrors.includes("GET /api/session 401"),
  }, null, 2));
} catch (error) {
  exitCode = 1;
  console.error(`FAIL: ${error instanceof Error ? error.stack ?? error.message : String(error)}`);
} finally {
  await browser.close();
  await mkdir(outDir, { recursive: true });
  await writeFile(resolve(outDir, "c1b-scenarios.json"), `${JSON.stringify({
    captured_at: new Date().toISOString(),
    source_sha: sourceSHA || "NOT_RECORDED",
    server_sha: serverSHA || "NOT_RECORDED",
    execution_sha: executionSHA || "NOT_RECORDED",
    dataset_path: datasetPath,
    scenarios: results,
    console_errors: consoleErrors,
    http_errors: httpErrors,
    result: exitCode === 0 ? "passed" : "failed",
  }, null, 2)}\n`, "utf8");
}
if (exitCode !== 0) process.exit(exitCode);

async function signIn(currentPage: Page): Promise<void> {
  await currentPage.goto(`${web}/login`);
  await currentPage.getByLabel("账号", { exact: true }).fill(username);
  await currentPage.getByLabel("密码", { exact: true }).fill(password);
  await currentPage.getByRole("button", { name: "登录", exact: true }).click();
  await currentPage.waitForURL(/\/cases(?:$|\?)/, { timeout: 15000 });
  await currentPage.getByRole("heading", { name: "案件", exact: true }).waitFor();
}

async function loadDataset(path: string): Promise<Dataset> {
  const dataset = JSON.parse(await Bun.file(path).text()) as Dataset;
  const { sha256, ...payload } = dataset;
  const digest = await Bun.CryptoHasher.hash("sha256", canonical(payload), "hex");
  assert.equal(digest, sha256, "telecom dataset digest differs from its committed bytes");
  return dataset;
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value !== null && typeof value === "object") {
    return `{${Object.entries(value).sort(([left], [right]) => left.localeCompare(right)).map(([key, item]) => `${JSON.stringify(key)}:${canonical(item)}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

async function uploadMaterial(currentPage: Page, material: Material): Promise<string> {
  const result = await uploadNewMaterial(currentPage, material);
  return result.id;
}

async function waitForCase(currentPage: Page, caseID: string, ready: (detail: CaseDetail) => boolean): Promise<CaseDetail> {
  const deadline = Date.now() + 20000;
  let last: CaseDetail | null = null;
  while (Date.now() < deadline) {
    const response = await currentPage.request.get(`${web}/api/cases/${caseID}`);
    assert.equal(response.status(), 200, `case ${caseID} detail returned HTTP ${response.status()}`);
    last = await response.json() as CaseDetail;
    if (ready(last)) return last;
    await currentPage.waitForTimeout(100);
  }
  throw new Error(`case ${caseID} did not reach expected state; last detail=${JSON.stringify(last)}`);
}

async function findCaseByReport(currentPage: Page, scenarioTitle: string, reportID: string): Promise<CaseDetail> {
  const listResponse = await currentPage.request.get(`${web}/api/cases`);
  assert.equal(listResponse.status(), 200, "case listing failed during scenario recovery");
  const list = await listResponse.json() as { items: Array<{ id: string; title: string }> };
  const candidates = list.items.filter((item) => item.title.startsWith(`${scenarioTitle} · Chromium`));
  for (const candidate of candidates) {
    const response = await currentPage.request.get(`${web}/api/cases/${candidate.id}`);
    assert.equal(response.status(), 200, `case ${candidate.id} detail failed during scenario recovery`);
    const detail = await response.json() as CaseDetail;
    if (detail.reports.some((report) => report.id === reportID)) return detail;
  }
  throw new Error(`could not find scenario report ${reportID} under ${candidates.length} compatible case candidates`);
}

async function finishExistingScenario(
  currentPage: Page,
  dataset: Dataset,
  scenario: Scenario,
  detail: CaseDetail,
  reportID: string,
  result: Record<string, unknown>,
  allowedHTTPFailures: Set<string>,
): Promise<void> {
  const report = detail.reports.find((item) => item.id === reportID);
  assert.ok(report, `scenario report ${reportID} is absent from case ${detail.id}`);
  const workspace = await waitForWorkspace(currentPage, detail.id, report.run_id);
  assert.equal(workspace.review_attempts?.attempt?.id, report.attempt_id, "existing report attempt differs from its review workspace");
  const frozenGovernance = await readFrozenGovernance(currentPage, web, detail.id, report.run_id, report.attempt_id);
  Object.assign(result, { rulepack: frozenGovernance.rulepack });
  assertRelations(workspace, scenario.expected_relations);
  assert.equal(workspace.findings.length, 0, "compatible scenario unexpectedly produced findings");
  Object.assign(result, { title: detail.title, run_id: report.run_id, attempt_id: report.attempt_id, finding_count: 0, expected_relations: scenario.expected_relations, stage: "reviewed" });

  await currentPage.goto(`${web}/cases/${detail.id}`);
  await currentPage.getByRole("heading", { name: detail.title, exact: true }).waitFor();
  const reportBytes = await readReportLinks(currentPage, reportID);
  const reportRelations = collectRelations(JSON.parse(reportBytes.json.toString("utf8")) as unknown);
  for (const relation of Object.values(scenario.expected_relations)) assert.ok(reportRelations.includes(relation), `existing frozen report omitted ${relation}`);

  let archived = detail;
  if (!detail.archived_at) {
    await selectReportOption(currentPage, currentPage.getByLabel("归档所依据的报告", { exact: true }), report);
    await currentPage.getByLabel("归档理由", { exact: true }).fill("已完成符合场景复核并归档冻结报告。");
    const archiveResponse = currentPage.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/api/cases/${detail.id}/archive`));
    await currentPage.getByRole("button", { name: "归档案件", exact: true }).click();
    assert.equal((await archiveResponse).status(), 200, "compatible case archive action was not accepted");
    archived = await waitForCase(currentPage, detail.id, (value) => Boolean(value.archived_at) && value.archives.some((item) => item.report_id === reportID));
  }
  assert.ok(archived.archives.some((item) => item.report_id === reportID), "compatible report has no archive record");
  const afterArchiveBytes = await readReportLinks(currentPage, reportID);
  assert.deepEqual(afterArchiveBytes.json, reportBytes.json, "compatible archive changed frozen JSON bytes");
  assert.deepEqual(afterArchiveBytes.html, reportBytes.html, "compatible archive changed frozen HTML bytes");

  const deniedWrite = await currentPage.request.post(`${web}/api/cases/${detail.id}/generations`, {
    data: { template_id: dataset.template_id, fields: scenario.fields },
  });
  assert.equal(deniedWrite.status(), 409, "archived compatible case accepted a new generation");
  allowedHTTPFailures.add(`POST /api/cases/${detail.id}/generations 409`);
  await currentPage.goto(`${web}/cases/${detail.id}/reviews/${report.run_id}?attempt_id=${encodeURIComponent(report.attempt_id)}`);
  await currentPage.getByRole("heading", { name: "审查工作区", exact: true }).waitFor();
  assert.ok((await currentPage.locator("body").innerText()).includes(report.run_id), "compatible archived history is not readable");
  await assertArchivedReviewWriteControls(currentPage, scenario.id, false);
  Object.assign(result, {
    archive_id: archived.archives.at(-1)?.id,
    archive_read_only_status: deniedWrite.status(),
    frozen_report_unchanged_after_archive: true,
    stage: "archived_and_read_only",
  });
}

async function finishExistingInsufficientScenario(
  currentPage: Page,
  dataset: Dataset,
  scenario: Scenario,
  initialDetail: CaseDetail,
  runID: string,
  result: Record<string, unknown>,
  allowedHTTPFailures: Set<string>,
): Promise<void> {
  assert.equal(initialDetail.archived_at, null, "insufficient scenario case was already archived");
  const workspace = await waitForWorkspace(currentPage, initialDetail.id, runID);
  const attemptID = workspace.review_attempts?.attempt?.id ?? "";
  assert.ok(attemptID, "insufficient scenario review has no attempt identity");
  const frozenGovernance = await readFrozenGovernance(currentPage, web, initialDetail.id, runID, attemptID);
  assertRelations(workspace, scenario.expected_relations);
  const procurementID = initialDetail.documents.find((document) => document.role === "procurement")?.id;
  assert.ok(procurementID, "insufficient scenario has no procurement document identity");
  const reviewDraft = initialDetail.documents.filter((document) => document.role === "draft_contract").at(-1)?.id;
  assert.ok(reviewDraft, "insufficient scenario has no current draft document");
  for (const documentID of [procurementID, reviewDraft]) {
    assert.ok(workspace.documents.some((document) => document.id === documentID), `insufficient review omitted document ${documentID}`);
  }
  Object.assign(result, { attempt_id: attemptID, run_state: workspace.run.state, finding_count: workspace.findings.length, rulepack: frozenGovernance.rulepack, expected_relations: scenario.expected_relations, stage: "reviewed" });

  const findingID = await recordInsufficientDecision(currentPage, initialDetail.id, runID, workspace, procurementID);
  result.decision_finding_id = findingID;
  result.stage = "decision_recorded";
  await currentPage.goto(`${web}/cases/${initialDetail.id}`);
  await currentPage.getByRole("heading", { name: initialDetail.title, exact: true }).waitFor();
  let currentDetail = await waitForCase(currentPage, initialDetail.id, (detail) => detail.id === initialDetail.id);
  let report = currentDetail.reports.find((item) => item.run_id === runID && item.attempt_id === attemptID);
  if (!report) {
    await selectReviewAttempt(currentPage, web, initialDetail.id, runID, attemptID, currentPage.getByLabel("选择审查和执行记录", { exact: true }));
    const reportResponse = currentPage.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/reports"));
    await currentPage.getByRole("button", { name: "生成报告", exact: true }).click();
    const createdReport = await reportResponse;
    assert.ok(createdReport.status() === 200 || createdReport.status() === 201, `insufficient report creation returned HTTP ${createdReport.status()}`);
    currentDetail = await waitForCase(currentPage, initialDetail.id, (detail) => detail.reports.some((item) => item.run_id === runID && item.attempt_id === attemptID));
    report = currentDetail.reports.find((item) => item.run_id === runID && item.attempt_id === attemptID);
  }
  assert.ok(report, "insufficient scenario frozen report was not returned");
  result.report_id = report.id;
  const reportBytes = await readReportLinks(currentPage, report.id);
  const reportRelations = collectRelations(JSON.parse(reportBytes.json.toString("utf8")) as unknown);
  for (const relation of Object.values(scenario.expected_relations)) assert.ok(reportRelations.includes(relation), `insufficient frozen report omitted ${relation}`);

  if (!currentDetail.archived_at) {
    await selectReportOption(currentPage, currentPage.getByLabel("归档所依据的报告", { exact: true }), report);
    await currentPage.getByLabel("归档理由", { exact: true }).fill("已记录证据不足决定并归档冻结报告。");
    const archiveResponse = currentPage.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/api/cases/${initialDetail.id}/archive`));
    await currentPage.getByRole("button", { name: "归档案件", exact: true }).click();
    assert.equal((await archiveResponse).status(), 200, "insufficient case archive action was not accepted");
    currentDetail = await waitForCase(currentPage, initialDetail.id, (detail) => Boolean(detail.archived_at) && detail.archives.some((item) => item.report_id === report.id));
  }
  assert.ok(currentDetail.archives.some((item) => item.report_id === report.id), "insufficient report has no archive record");
  const afterArchiveBytes = await readReportLinks(currentPage, report.id);
  assert.deepEqual(afterArchiveBytes.json, reportBytes.json, "insufficient archive changed frozen JSON bytes");
  assert.deepEqual(afterArchiveBytes.html, reportBytes.html, "insufficient archive changed frozen HTML bytes");

  const deniedWrite = await currentPage.request.post(`${web}/api/cases/${initialDetail.id}/generations`, {
    data: { template_id: dataset.template_id, fields: scenario.fields },
  });
  assert.equal(deniedWrite.status(), 409, "archived insufficient case accepted a new generation");
  allowedHTTPFailures.add(`POST /api/cases/${initialDetail.id}/generations 409`);
  await currentPage.goto(`${web}/cases/${initialDetail.id}/reviews/${runID}?attempt_id=${encodeURIComponent(attemptID)}`);
  await currentPage.getByRole("heading", { name: "审查工作区", exact: true }).waitFor();
  const historicalText = await currentPage.locator("body").innerText();
  assert.ok(historicalText.includes(runID), "insufficient archived history is not readable");
  assert.ok(historicalText.includes("已记录证据不足"), "insufficient archived decision is absent from history");
  await assertArchivedReviewWriteControls(currentPage, scenario.id, true);
  Object.assign(result, {
    archive_id: currentDetail.archives.at(-1)?.id,
    archive_read_only_status: deniedWrite.status(),
    frozen_report_unchanged_after_archive: true,
    stage: "archived_and_read_only",
  });
}

async function assertArchivedReviewWriteControls(currentPage: Page, scenarioID: string, hasDecision: boolean): Promise<void> {
  const decisionButton = currentPage.getByRole("button", { name: "保存决定", exact: true });
  if (hasDecision) {
    await decisionButton.waitFor({ state: "visible" });
    assert.equal(await decisionButton.isEnabled(), false, `${scenarioID} archived review still permits a decision`);
  } else {
    assert.equal(await decisionButton.count(), 0, `${scenarioID} compatible review exposed a decision control`);
  }
  for (const name of ["重新执行未完成的审查", "取消本次执行"]) {
    const actionButton = currentPage.getByRole("button", { name, exact: true });
    if (await actionButton.count() > 0) {
      assert.equal(await actionButton.isEnabled(), false, `${scenarioID} archived review still permits ${name}`);
    }
  }
}

async function recordInsufficientDecision(
  currentPage: Page,
  caseID: string,
  runID: string,
  workspace: ReviewWorkspace,
  procurementID: string,
): Promise<string> {
  const evaluation = workspaceEvaluations(workspace).find((item) => item.rule_id === "ENT-TECH-BW-001");
  assert.equal(evaluation?.relation, "insufficient", "bandwidth rule is not insufficient");
  const findingID = String(evaluation?.finding_id ?? "");
  assert.ok(findingID, "insufficient bandwidth evaluation is not linked to a finding");
  const finding = workspace.findings.find((item) => item.id === findingID);
  assert.ok(finding, "insufficient bandwidth finding is missing");
  assert.ok(typeof finding.claim === "string" && finding.claim.trim() !== "", "insufficient bandwidth finding has no claim");
  const evidence = finding.evidence;
  assert.ok(evidence.length > 0, "insufficient bandwidth finding has no evidence trace");
  assert.ok(evidence.some((item) => item.document_version_id === procurementID), "insufficient finding lost its procurement source identity");
  const decisions = finding.decisions;
  if (decisions.length > 0) {
    assert.ok(decisions.some((decision) => decision.action === "insufficient_evidence"), "insufficient finding already has a different decision");
    return findingID;
  }
  await currentPage.goto(`${web}/cases/${caseID}/reviews/${runID}`);
  await currentPage.getByRole("heading", { name: "审查工作区", exact: true }).waitFor();
  await submitFindingDecision(currentPage, findingID, finding.claim);
  return findingID;
}
async function waitForWorkspace(currentPage: Page, caseID: string, runID: string): Promise<ReviewWorkspace> {
  const deadline = Date.now() + 90000;
  let last: ReviewWorkspace | null = null;
  while (Date.now() < deadline) {
    const response = await currentPage.request.get(`${web}/api/cases/${caseID}/reviews/${runID}`);
    assert.equal(response.status(), 200, `review ${runID} returned HTTP ${response.status()}`);
    last = parseReviewWorkspace(await response.json());
    if (!["accepted", "queued", "running"].includes(last.run.state)) return last;
    await currentPage.waitForTimeout(200);
  }
  throw new Error(`review ${runID} did not terminate; last workspace=${JSON.stringify(last)}`);
}

function workspaceEvaluations(workspace: ReviewWorkspace): ConstraintEvaluation[] {
  assert.ok(Array.isArray(workspace.evaluations), `review ${workspace.run.id} omitted evaluations`);
  return workspace.evaluations;
}

function assertRelations(workspace: ReviewWorkspace, expected: Record<string, string>): void {
  for (const [ruleID, relation] of Object.entries(expected)) {
    const evaluation = workspaceEvaluations(workspace).find((item) => item.rule_id === ruleID);
    assert.ok(evaluation, `evaluation ${ruleID} is missing`);
    assert.equal(evaluation.relation, relation, `${ruleID} relation`);
  }
}
function collectRelations(value: unknown): string[] {
  if (Array.isArray(value)) return value.flatMap(collectRelations);
  if (value === null || typeof value !== "object") return [];
  const record = value as Record<string, unknown>;
  const current = typeof record.relation === "string" ? [record.relation] : [];
  return [...current, ...Object.values(record).flatMap(collectRelations)];
}

async function readReportLinks(currentPage: Page, reportID: string): Promise<{ json: Buffer; html: Buffer }> {
  await currentPage.waitForFunction((id) => {
    const hrefs = Array.from(document.querySelectorAll("a"), (element) => (element as HTMLAnchorElement).href);
    return hrefs.some((href) => href.includes(`/reports/${id}`) && href.includes("format=json")) &&
      hrefs.some((href) => href.includes(`/reports/${id}`) && href.includes("format=html"));
  }, reportID, { timeout: 15000 });
  const links = await currentPage.locator("a").evaluateAll((elements) => elements.map((element) => ({
    href: (element as HTMLAnchorElement).href,
    text: element.textContent ?? "",
  })));
  const jsonHref = links.find((link) => link.href.includes(`/reports/${reportID}`) && link.href.includes("format=json"))?.href;
  const htmlHref = links.find((link) => link.href.includes(`/reports/${reportID}`) && link.href.includes("format=html"))?.href;
  assert.ok(jsonHref && htmlHref, `report ${reportID} links were absent from the case page`);
  const [jsonResponse, htmlResponse] = await Promise.all([currentPage.request.get(jsonHref), currentPage.request.get(htmlHref)]);
  assert.equal(jsonResponse.status(), 200, "frozen JSON report was unreadable");
  assert.equal(htmlResponse.status(), 200, "frozen HTML report was unreadable");
  assert.match(jsonResponse.headers()["content-type"] ?? "", /application\/json/i);
  assert.match(htmlResponse.headers()["content-type"] ?? "", /text\/html/i);
  assert.match(htmlResponse.headers()["content-security-policy"] ?? "", /default-src 'none'/);
  const json = Buffer.from(await jsonResponse.body());
  const html = Buffer.from(await htmlResponse.body());
  assert.doesNotThrow(() => JSON.parse(json.toString("utf8")), "frozen JSON is invalid");
  assert.ok(html.toString("utf8").toLowerCase().includes("<!doctype html>"), "frozen HTML is incomplete");
  return { json, html };
}
