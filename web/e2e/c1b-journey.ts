import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { chromium, type Locator, type Page } from "playwright";

type Material = { role: string; filename: string; text: string };
type RulePackIdentity = { id: string; version: string; content_sha256: string };
type Scenario = {
  id: string;
  title: string;
  expected_relations: Record<string, string>;
  fields: Record<string, string>;
  materials: Material[];
};
type Dataset = {
  [key: string]: unknown;
  dataset_id: string;
  data_class: string;
  template_id: string;
  template_version: string;
  sha256: string;
  rulepack?: RulePackIdentity;
  scenarios: Scenario[];
};
type Workspace = {
  run: { id: string; state: string };
  findings: Array<Record<string, unknown>>;
  evaluations: Array<Record<string, unknown>>;
  documents: Array<Record<string, unknown>>;
  execution?: { attempt?: { id?: string; state?: string }; agent_review?: { rulepack?: RulePackIdentity } };
  previous_run_id?: string | null;
  revisions?: Array<Record<string, unknown>>;
  revision_comparison_reason?: string | null;
};
type CaseDetail = {
  id: string;
  business_line: string;
  scenario: string;
  documents: Array<Record<string, unknown>>;
  generations: Array<Record<string, unknown>>;
  remediations: Array<Record<string, unknown>>;
  reports: Array<Record<string, unknown>>;
  archives: Array<Record<string, unknown>>;
  archived_at: string | null;
};

const web = required("E2E_WEB").replace(/\/$/, "");
const datasetPath = required("E2E_TELECOM_DATASET");
const scenarioID = process.env.E2E_TELECOM_SCENARIO || "conflict";
const sourceSHA = (process.env.E2E_SOURCE_SHA ?? "").trim();
const serverSHA = (process.env.E2E_SERVER_SHA ?? "").trim();
const executionSHA = (process.env.E2E_EXECUTION_SHA ?? "").trim();
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.CHROMIUM_BIN || "/usr/bin/chromium",
  args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const consoleErrors: string[] = [];
const httpErrors: string[] = [];
const consoleErrorURLs: string[] = [];
page.on("console", (message) => {
  if (message.type() === "error") {
    consoleErrors.push(message.text());
    consoleErrorURLs.push(message.location().url);
  }
});
page.on("pageerror", (error) => consoleErrors.push(error.message));
page.on("response", (response) => {
  if (response.status() >= 400) {
    httpErrors.push(`${response.request().method()} ${new URL(response.url()).pathname} ${response.status()}`);
  }
});

try {
  const dataset = await loadDataset(datasetPath);
  assert.equal(dataset.dataset_id, process.env.E2E_TELECOM_DATASET_ID || "telecom-c1b-v1");
  const scenario = dataset.scenarios.find((item) => item.id === scenarioID);
  assert.ok(scenario, `scenario ${scenarioID} missing from the frozen dataset`);
  const compatible = dataset.scenarios.find((item) => item.id === "compatible");
  assert.ok(compatible, "compatible scenario missing from the frozen dataset");
  const baselineBandwidth = `${compatible.fields.bandwidth_mbps} Mbit/s`;
  const originalDraftBandwidth = `${scenario.fields.bandwidth_mbps} Mbit/s`;

  await page.goto(`${web}/login`);
  await page.getByLabel("账号", { exact: true }).fill(process.env.E2E_USERNAME || "demo-admin");
  await page.getByLabel("密码", { exact: true }).fill(process.env.E2E_PASSWORD || "admin-demo");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("heading", { name: "案件", exact: true }).waitFor();

  const title = `${scenario.title} · Chromium ${Date.now()}`;
  await page.getByRole("button", { name: "新建案件", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("标题", { exact: true }).fill(title);
  await dialog.getByLabel("合同类型", { exact: true }).selectOption("service");
  await dialog.getByRole("button", { name: "创建", exact: true }).click();
  await page.waitForURL(/\/cases\/[^/]+$/);
  const caseID = new URL(page.url()).pathname.split("/").at(-1) ?? "";
  assert.ok(caseID, "created case ID was not present in the route");
  const initialDetail = await waitForCase(page, caseID, (detail) => detail.business_line === "ENT" && detail.scenario === "telecom_dedicated_line");
  assert.equal(initialDetail.business_line, "ENT");
  assert.equal(initialDetail.scenario, "telecom_dedicated_line");

  const templateResponse = await page.request.get(`${web}/api/templates`);
  assert.equal(templateResponse.status(), 200, "template listing failed");
  const templateData = await templateResponse.json() as { items: Array<{ id: string; version: string; fields: Array<{ key: string; label: string; required: boolean }> }> };
  const template = templateData.items.find((item) => item.id === dataset.template_id);
  assert.ok(template, `template ${dataset.template_id} not returned by the API`);
  assert.equal(template.version, dataset.template_version, "template version differs from the frozen dataset");
  assert.deepEqual(new Set(template.fields.map((field) => field.key)), new Set(Object.keys(scenario.fields)));

  const templateSelect = page.getByLabel("模板", { exact: true });
  await templateSelect.selectOption(dataset.template_id);
  for (const field of template.fields) {
    const value: string | undefined = scenario.fields[field.key];
    if (value === undefined) throw new Error(`fixture field ${field.key} is missing`);
    const control = page.getByLabel(field.required ? `${field.label}（必填）` : field.label, { exact: true });
    const tag = await control.evaluate((element) => element.tagName);
    if (tag === "SELECT") await control.selectOption(value);
    else await control.fill(value);
  }
  const generationResponse = await postFromUI(page, "/api/cases/", "generations", async () => {
    await page.getByRole("button", { name: "生成待审草稿", exact: true }).click();
  });
  assert.ok(generationResponse.document_id, "generation did not return a draft document ID");
  await waitForCase(page, caseID, (detail) => detail.generations.some((item) => item.id === generationResponse.id));

  const uploaded: Record<string, string> = {};
  for (const material of scenario.materials) {
    uploaded[material.role] = await uploadMaterial(page, material);
  }
  assert.ok(uploaded.procurement, "procurement source was not uploaded");
  assert.ok(uploaded.award, "approval source was not uploaded");
  assert.ok(uploaded.attachment, "SLA annex was not uploaded");

  await page.getByRole("button", { name: "开始审查", exact: true }).click();
  await page.waitForURL(/\/cases\/[^/]+\/reviews\/[^/]+/);
  const firstURL = new URL(page.url());
  const firstRunID = firstURL.pathname.split("/").at(-1) ?? "";
  assert.ok(firstRunID, "initial review run ID was not in the route");
  const firstWorkspace = await waitForWorkspace(page, caseID, firstRunID);
  if (dataset.rulepack) assert.deepEqual(firstWorkspace.execution?.agent_review?.rulepack, dataset.rulepack, "initial attempt froze a different governed policy");
  const firstAttemptID = getAttemptID(firstWorkspace, firstURL.searchParams.get("attempt_id"));
  assertRelations(firstWorkspace, scenario.expected_relations);

  const bandwidthEval = evaluation(firstWorkspace, "ENT-TECH-BW-001");
  const findingID = String(bandwidthEval.finding_id ?? "");
  assert.ok(findingID, "bandwidth evaluation is not linked to a finding");
  const bandwidthFinding = firstWorkspace.findings.find((item) => item.id === findingID);
  assert.ok(bandwidthFinding, "bandwidth finding is absent from workspace");
  const claim = String(bandwidthFinding.claim ?? "");
  assert.ok(claim, "bandwidth finding has no claim");
  const evidence = Array.isArray(bandwidthFinding.evidence) ? bandwidthFinding.evidence as Array<Record<string, unknown>> : [];
  const citedDocumentIDs = new Set(evidence.map((item) => String(item.document_version_id ?? "")));
  assert.ok(citedDocumentIDs.has(uploaded.procurement), "bandwidth evidence does not cite the procurement source");
  assert.ok(citedDocumentIDs.has(generationResponse.document_id), "bandwidth evidence does not cite the generated draft");

  const findingButton = page.getByRole("button", { name: `选择线索：${claim}`, exact: true });
  await findingButton.click();
  await page.getByRole("button", { name: "双来源对照", exact: true }).click();
  await page.locator(".document-split mark.hit-evidence").nth(1).waitFor();
  const highlighted = await page.locator(".document-split mark.hit-evidence").allTextContents();
  assert.ok(highlighted.length >= 2, `bidirectional evidence view showed ${highlighted.length} highlights`);
  const referencesHeading = page.getByRole("heading", { name: "引用该材料的线索", exact: true });
  await referencesHeading.waitFor();
  await page.getByRole("button", { name: `查看线索：${claim}`, exact: true }).click();
  await page.locator(".document-split mark.hit-evidence").nth(1).waitFor();

  await page.locator('input[type="radio"][value="confirm_risk"]').first().check();
  await page.getByLabel("理由", { exact: true }).fill(`采购材料要求 ${baselineBandwidth}，拟签合同为 ${originalDraftBandwidth}。`);
  const decisionResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().includes(`/reviews/${firstRunID}/findings/${findingID}/decisions`),
  );
  await page.getByRole("button", { name: "保存决定", exact: true }).click();
  assert.equal((await decisionResponse).status(), 204, "human decision was not recorded");

  await page.goto(`${web}/cases/${caseID}`);
  await page.getByRole("heading", { name: title, exact: true }).waitFor();
  await selectRunAndAttempt(page.getByLabel("冻结运行和尝试", { exact: true }), firstRunID, firstAttemptID);
  await page.getByRole("button", { name: "生成冻结报告", exact: true }).click();
  const firstReport = await waitForReport(page, caseID, firstRunID, firstAttemptID);
  const firstReportBytes = await readReportLinks(page, firstReport.id as string);

  await selectRunAndAttempt(page.getByLabel("来源运行和尝试", { exact: true }), firstRunID, firstAttemptID);
  const remediationGroup = page.getByRole("group", { name: "绑定发现（至少选择一项）", exact: true });
  await remediationGroup.getByRole("checkbox").first().check();
  await page.getByLabel("负责人 ID", { exact: true }).fill("demo-admin");
  const dueDate = page.getByLabel("截止时间", { exact: true });
  const dueType = await dueDate.getAttribute("type");
  await dueDate.fill(dueType === "date" ? "2027-01-01" : "2027-01-01T00:00");
  await page.getByLabel("整改措施", { exact: true }).fill("将修改稿修订到采购基线并重新审查。");
  await page.getByRole("button", { name: "创建整改", exact: true }).click();
  const createdDetail = await waitForCase(page, caseID, (detail) => detail.remediations.length === 1);
  const remediation = createdDetail.remediations[0];
  assert.ok(remediation, "created remediation was not returned by case detail");
  const remediationID = String(remediation.id ?? "");
  assert.ok(remediationID, "remediation ID missing");

  const revisedText = renderDraft(compatible.fields);
  const revisedID = await uploadMaterial(page, {
    role: "draft_contract",
    filename: "telecom-revised-draft.txt",
    text: revisedText,
  });
  assert.ok(revisedID, "revised draft upload returned no identity");

  await page.getByRole("button", { name: "开始复审", exact: true }).click();
  await page.waitForURL(/\/cases\/[^/]+\/reviews\/[^/]+/);
  const secondURL = new URL(page.url());
  const secondRunID = secondURL.pathname.split("/").at(-1) ?? "";
  assert.ok(secondRunID && secondRunID !== firstRunID, "re-review overwrote the original run");
  const secondWorkspace = await waitForWorkspace(page, caseID, secondRunID);
  if (dataset.rulepack) assert.deepEqual(secondWorkspace.execution?.agent_review?.rulepack, dataset.rulepack, "re-review froze a different governed policy");
  const secondAttemptID = getAttemptID(secondWorkspace, secondURL.searchParams.get("attempt_id"));
  assert.notEqual(secondAttemptID, firstAttemptID, "re-review reused the previous attempt");
  assertRelations(secondWorkspace, compatible.expected_relations);

  await page.goto(`${web}/cases/${caseID}`);
  await page.getByRole("heading", { name: title, exact: true }).waitFor();
  const itemForm = page.getByRole("group", { name: "本案当前的拟签合同修改稿", exact: true });
  await itemForm.getByRole("radio", { name: `telecom-revised-draft.txt · ${revisedID}`, exact: true }).check();
  await page.getByLabel("提交理由", { exact: true }).fill("已按采购材料修订并生成新的审查运行。");
  await page.getByRole("button", { name: "提交修改稿", exact: true }).click();
  await page.getByLabel("复核运行和尝试", { exact: true }).waitFor();
  await selectRunAndAttempt(page.getByLabel("复核运行和尝试", { exact: true }), secondRunID, secondAttemptID);
  const verifyReason = page.getByLabel("复核理由", { exact: true });
  await verifyReason.waitFor({ state: "visible", timeout: 15000 });
  // Readiness requires matching run/attempt metadata and complete same-scope compatible rule evaluations.
  try {
    await page.waitForFunction(() => {
      const label = Array.from(document.querySelectorAll("label")).find((node) => (node.textContent ?? "").trim().startsWith("复核理由"));
      const control = label?.querySelector("textarea");
      return control instanceof HTMLTextAreaElement && !control.disabled;
    }, undefined, { timeout: 30000 });
  } catch (error) {
    const findingIDs = Array.isArray(remediation.finding_ids)
      ? remediation.finding_ids.filter((value): value is string => typeof value === "string")
      : [];
    const uniqueObjectRefs = (value: unknown): value is string[] => Array.isArray(value) && value.length > 0 &&
      value.every((item) => typeof item === "string" && item.trim() !== "") && new Set(value).size === value.length;
    const evaluationChecks = findingIDs.map((findingID) => {
      const sourceRows = firstWorkspace.evaluations.filter((evaluation) => evaluation.finding_id === findingID);
      const source = sourceRows[0];
      const sourceRefs = source && uniqueObjectRefs(source.object_refs) ? source.object_refs : null;
      const matching = source && sourceRefs !== null && typeof source.rule_id === "string" && source.rule_id.trim() !== "" &&
        typeof source.rule_version === "string" && source.rule_version.trim() !== "" &&
        typeof source.capability_id === "string" && source.capability_id.trim() !== ""
        ? secondWorkspace.evaluations.filter((evaluation) => {
          const candidateRefs = evaluation.object_refs;
          return evaluation.rule_id === source.rule_id && evaluation.rule_version === source.rule_version &&
            evaluation.capability_id === source.capability_id && uniqueObjectRefs(candidateRefs) &&
            candidateRefs.length === sourceRefs.length && candidateRefs.every((objectRef) => sourceRefs.includes(objectRef));
        })
        : [];
      return {
        finding_id: findingID,
        source_rows: sourceRows.length,
        source_rule_id: source?.rule_id ?? null,
        source_rule_version: source?.rule_version ?? null,
        source_capability_id: source?.capability_id ?? null,
        source_object_refs: sourceRefs,
        matching_candidates: matching.map((evaluation) => ({
          rule_id: evaluation.rule_id,
          rule_version: evaluation.rule_version,
          capability_id: evaluation.capability_id,
          object_refs: evaluation.object_refs,
          relation: evaluation.relation,
          finding_id: evaluation.finding_id,
        })),
        exactly_one_compatible: matching.length === 1 && matching[0]?.relation === "compatible",
      };
    });
    const revisionMarks = (secondWorkspace.revisions ?? [])
      .filter((revision) => findingIDs.includes(String(revision.previous_finding_id ?? "")))
      .map((revision) => ({
        previous_finding_id: revision.previous_finding_id,
        finding_id: revision.finding_id,
        mark: revision.mark,
        relation: revision.relation,
      }));
    const diagnostics = {
      source_run_id: firstWorkspace.run.id,
      expected_previous_run_id: firstRunID,
      actual_previous_run_id: secondWorkspace.previous_run_id ?? null,
      candidate_run_id: secondWorkspace.run.id,
      expected_candidate_run_id: secondRunID,
      source_attempt_id: firstAttemptID,
      candidate_attempt_id: secondWorkspace.execution?.attempt?.id ?? null,
      expected_candidate_attempt_id: secondAttemptID,
      candidate_attempt_state: secondWorkspace.execution?.attempt?.state ?? null,
      revision_comparison_reason: secondWorkspace.revision_comparison_reason ?? null,
      evaluation_checks: evaluationChecks,
      every_finding_compatible: findingIDs.length > 0 && evaluationChecks.every((check) => check.exactly_one_compatible),
      presentation_revision_marks: revisionMarks,
      visible_callouts: await page.locator(".callout").allTextContents(),
      verification_reason_disabled: await verifyReason.isDisabled(),
    };
    throw new Error(`remediation verification readiness failed: ${JSON.stringify(diagnostics)}; ${error instanceof Error ? error.message : String(error)}`);
  }
  await verifyReason.fill("新尝试在相同能力和电路对象下均为 compatible。");
  const verifyResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().includes(`/remediations/${remediationID}/verify`),
  );
  await page.getByRole("button", { name: "提交复核", exact: true }).click();
  assert.equal((await verifyResponse).status(), 200, "compatible re-review did not close remediation");
  const verifiedDetail = await waitForCase(page, caseID, (detail) => detail.remediations.some((item) => item.id === remediationID && item.status === "closed"));
  assert.equal(verifiedDetail.remediations.find((item) => item.id === remediationID)?.status, "closed");

  await page.goto(`${web}/cases/${caseID}/reviews/${firstRunID}?attempt_id=${encodeURIComponent(firstAttemptID)}`);
  await page.getByRole("heading", { name: "审查工作区", exact: true }).waitFor();
  await page.getByText("已记录确认风险", { exact: false }).waitFor();
  await page.getByRole("button", { name: `选择线索：${claim}`, exact: true }).click();
  await page.getByRole("button", { name: "双来源对照", exact: true }).click();
  await page.locator(".document-split").waitFor({ state: "visible" });
  await page.locator(`.document-split .doc-pane[data-document-version-id="${uploaded.procurement}"]`).waitFor({ state: "visible" });
  const procurementText = await page.locator(`.document-split .doc-pane[data-document-version-id="${uploaded.procurement}"] .doc-text`).innerText();
  assert.ok(procurementText.includes(baselineBandwidth), "old procurement source is not displayed");
  const oldHistory = await page.locator("body").innerText();
  assert.ok(oldHistory.includes(originalDraftBandwidth), "old run no longer displays its frozen draft value");
  assert.ok(oldHistory.includes(baselineBandwidth), "old run no longer displays its procurement evidence");

  await page.goto(`${web}/cases/${caseID}`);
  await page.getByRole("heading", { name: title, exact: true }).waitFor();
  await selectRunAndAttempt(page.getByLabel("冻结运行和尝试", { exact: true }), secondRunID, secondAttemptID);
  await page.getByRole("button", { name: "生成冻结报告", exact: true }).click();
  const secondReport = await waitForReport(page, caseID, secondRunID, secondAttemptID);
  const secondReportBytes = await readReportLinks(page, secondReport.id as string);
  assert.ok(secondReportBytes.json.length > 0 && secondReportBytes.html.length > 0);
  const firstReportBytesAfter = await readReportLinks(page, firstReport.id as string);
  assert.deepEqual(firstReportBytesAfter.json, firstReportBytes.json, "later report creation changed frozen JSON bytes");
  assert.deepEqual(firstReportBytesAfter.html, firstReportBytes.html, "later report creation changed frozen HTML bytes");

  await selectReport(page.getByLabel("归档所依据的报告", { exact: true }), String(secondReport.id));
  await page.getByLabel("归档理由", { exact: true }).fill("已完成复审并归档冻结报告。");
  const archiveResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/archive`),
  );
  await page.getByRole("button", { name: "归档案件", exact: true }).click();
  assert.equal((await archiveResponse).status(), 200, "archive action was not accepted");
  const archived = await waitForCase(page, caseID, (detail) => Boolean(detail.archived_at) && detail.archives.length > 0);
  assert.ok(archived.archived_at, "archive timestamp was not retained");

  const deniedWrite = await page.request.post(`${web}/api/cases/${caseID}/generations`, {
    data: { template_id: dataset.template_id, fields: scenario.fields },
  });
  assert.equal(deniedWrite.status(), 409, "archived case accepted a new generation");
  const archivedReportBytes = await readReportLinks(page, firstReport.id as string);
  assert.deepEqual(archivedReportBytes.json, firstReportBytes.json, "archiving changed frozen JSON bytes");
  assert.deepEqual(archivedReportBytes.html, firstReportBytes.html, "archiving changed frozen HTML bytes");

  const expectedUnauthorizedConsole = "Failed to load resource: the server responded with a status of 401 (Unauthorized)";
  const unexpectedConsoleErrors = consoleErrors.filter((message) => message !== expectedUnauthorizedConsole);
  const unexpectedHTTPFailures = httpErrors.filter((failure) => failure !== "GET /api/session 401");
  assert.deepEqual(unexpectedHTTPFailures, [], `unexpected HTTP failures: ${unexpectedHTTPFailures.join(" | ")}`);
  assert.deepEqual(unexpectedConsoleErrors, [], `unexpected browser console errors: ${unexpectedConsoleErrors.join(" | ")}; URLs: ${consoleErrorURLs.join(" | ")}`);
  console.log(JSON.stringify({
    console_errors: consoleErrors,
    http_errors: httpErrors,
    expected_unauthenticated_session_probe: httpErrors.includes("GET /api/session 401"),
    source_sha: sourceSHA === "" ? "NOT_RECORDED" : sourceSHA,
    server_sha: serverSHA === "" ? "NOT_RECORDED" : serverSHA,
    execution_sha: executionSHA === "" ? "NOT_RECORDED" : executionSHA,
    data_class: dataset.data_class,
    dataset_id: dataset.dataset_id,
    dataset_sha256: dataset.sha256,
    scenario: scenario.id,
    case_id: caseID,
    original_run_id: firstRunID,
    re_review_run_id: secondRunID,
    first_report_id: firstReport.id,
    final_report_id: secondReport.id,
    remediation_status: "closed",
    archive_id: archived.archives.at(-1)?.id,
    result: "real Chromium C1-B journey passed",
  }, null, 2));
} finally {
  await browser.close();
}

async function loadDataset(path: string): Promise<Dataset> {
  const dataset = JSON.parse(await Bun.file(path).text()) as Dataset;
  const digestInput = Object.fromEntries(Object.entries(dataset as Record<string, unknown>).filter(([key]) => key !== "sha256"));
  const actual = createHash("sha256").update(JSON.stringify(sortKeys(digestInput)), "utf8").digest("hex");
  assert.equal(dataset.sha256, actual, "frozen scenario dataset SHA-256 mismatch");
  return dataset;
}

function sortKeys(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortKeys);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0).map(([key, item]) => [key, sortKeys(item)]));
  }
  return value;
}

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

async function postFromUI(page: Page, pathPrefix: string, suffix: string, action: () => Promise<void>): Promise<Record<string, any>> {
  const responsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().includes(pathPrefix) && response.url().endsWith(suffix),
  );
  await action();
  const response = await responsePromise;
  assert.equal(response.status(), 201, `${suffix} response returned HTTP ${response.status()}`);
  return await response.json() as Record<string, any>;
}

async function uploadMaterial(page: Page, material: Material): Promise<string> {
  const roleSelect = page.getByLabel("材料角色", { exact: true });
  const fileInput = page.getByLabel("文件", { exact: true });
  const uploadButton = page.getByRole("button", { name: "上传", exact: true });
  await roleSelect.selectOption(material.role);
  // The upload panel remounts its file input after a completed upload, so the
  // attach/enable cycle is retried until the submit button is actually enabled.
  for (let attempt = 0; attempt < 4; attempt++) {
    await fileInput.setInputFiles({
      name: material.filename,
      mimeType: "text/plain",
      buffer: Buffer.from(material.text, "utf8"),
    });
    await page.waitForTimeout(150);
    if (!(await uploadButton.isDisabled().catch(() => true))) break;
  }
  const responsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith("/documents"),
  );
  await uploadButton.waitFor({ state: "visible", timeout: 15000 });
  await uploadButton.scrollIntoViewIfNeeded();
  await uploadButton.click();
  const response = await responsePromise;
  assert.equal(response.status(), 201, `upload ${material.filename} returned HTTP ${response.status()}`);
  const result = await response.json() as { id?: string };
  assert.ok(result.id, `upload ${material.filename} returned no document ID`);
  await page.getByText(material.filename, { exact: true }).waitFor();
  return result.id;
}

async function waitForCase(page: Page, caseID: string, ready: (detail: CaseDetail) => boolean): Promise<CaseDetail> {
  const deadline = Date.now() + 20000;
  let last: CaseDetail | null = null;
  while (Date.now() < deadline) {
    const response = await page.request.get(`${web}/api/cases/${caseID}`);
    assert.equal(response.status(), 200, `case ${caseID} detail returned HTTP ${response.status()}`);
    last = await response.json() as CaseDetail;
    if (ready(last)) return last;
    await page.waitForTimeout(100);
  }
  throw new Error(`case ${caseID} did not reach expected state; last detail=${JSON.stringify(last)}`);
}

async function waitForWorkspace(page: Page, caseID: string, runID: string): Promise<Workspace> {
  const deadline = Date.now() + 90000;
  let last: Workspace | null = null;
  while (Date.now() < deadline) {
    const response = await page.request.get(`${web}/api/cases/${caseID}/reviews/${runID}`);
    assert.equal(response.status(), 200, `review ${runID} returned HTTP ${response.status()}`);
    last = await response.json() as Workspace;
    if (!["accepted", "queued", "running"].includes(last.run.state)) return last;
    await page.waitForTimeout(200);
  }
  throw new Error(`review ${runID} did not terminate; last workspace=${JSON.stringify(last)}`);
}

function getAttemptID(workspace: Workspace, routeAttempt: string | null): string {
  const attemptID = routeAttempt || workspace.execution?.attempt?.id || "";
  assert.ok(attemptID, `review ${workspace.run.id} has no attempt identity`);
  return attemptID;
}

function assertRelations(workspace: Workspace, expected: Record<string, string>): void {
  for (const [ruleID, relation] of Object.entries(expected)) {
    const observed = evaluation(workspace, ruleID).relation;
    assert.equal(observed, relation, `${ruleID} relation`);
  }
}

function evaluation(workspace: Workspace, ruleID: string): Record<string, any> {
  const result = workspace.evaluations.find((item) => item.rule_id === ruleID);
  assert.ok(result, `evaluation ${ruleID} is missing: ${JSON.stringify(workspace.evaluations)}`);
  return result as Record<string, any>;
}

function renderDraft(fields: Record<string, string>): string {
  const circuit = fields.circuit_id;
  const sla = fields.sla_percent;
  const period = fields.sla_period === "calendar_month"
    ? "统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。"
    : "";
  const exclusions = fields.sla_exclusions === "none" ? "无排除项。" : "排除项：planned_maintenance。";
  const prepayment = fields.prepayment_percent;
  const tax = fields.tax_basis === "inclusive" ? "含税" : "不含税";
  return [
    "拟签合同",
    `电路 ${circuit}：最低保障带宽 ${fields.bandwidth_mbps} Mbit/s。`,
    `电路 ${circuit}：SLA 可用率 ${sla}%；${period}${period ? "" : `时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；${exclusions}`}`,
    `电路 ${circuit}：预付款比例 ${prepayment}%，预付款以合同总额为基础，${tax}。`,
    `电路 ${circuit}：合同总额 ${fields.amount_cny} 元，${tax}。`,
  ].join("\n");
}

async function selectRunAndAttempt(select: Locator, runID: string, attemptID: string): Promise<void> {
  const options = await select.locator("option").evaluateAll((elements) => elements.map((element) => {
    const option = element as HTMLOptionElement;
    return { value: option.value, text: option.textContent ?? "" };
  }));
  const target = options.find((option) =>
    option.value.includes(runID) || option.text.includes(runID) ||
    option.value.includes(attemptID) || option.text.includes(attemptID),
  );
  if (target) {
    await select.selectOption(target.value);
    return;
  }
  assert.equal(options.length, 1, `no selector option matches run ${runID} / attempt ${attemptID}: ${JSON.stringify(options)}`);
  await select.selectOption(options[0]!.value);
}

async function selectReport(select: Locator, reportID: string): Promise<void> {
  const options = await select.locator("option").evaluateAll((elements) => elements.map((element) => {
    const option = element as HTMLOptionElement;
    return { value: option.value, text: option.textContent ?? "" };
  }));
  const target = options.find((option) => option.value.includes(reportID) || option.text.includes(reportID));
  assert.ok(target, `archive report ${reportID} was not available: ${JSON.stringify(options)}`);
  await select.selectOption(target.value);
}

async function waitForReport(page: Page, caseID: string, runID: string, attemptID: string): Promise<Record<string, any>> {
  const detail = await waitForCase(page, caseID, (value) => value.reports.some((item) => item.run_id === runID && item.attempt_id === attemptID));
  const report = detail.reports.find((item) => item.run_id === runID && item.attempt_id === attemptID);
  assert.ok(report, `report for ${runID}/${attemptID} is absent`);
  return report as Record<string, any>;
}

async function readReportLinks(page: Page, reportID: string): Promise<{ json: Buffer; html: Buffer }> {
  const links = page.locator("a");
  const matches = await links.evaluateAll((elements, id) => elements
    .filter((element) => (element as HTMLAnchorElement).href.includes(`/reports/${id}`))
    .map((element) => ({ href: (element as HTMLAnchorElement).href, text: element.textContent ?? "" })), reportID);
  const jsonLink = matches.find((item) => item.text.includes("读取冻结 JSON") || item.href.includes("format=json"));
  const htmlLink = matches.find((item) => item.text.includes("读取冻结 HTML") || item.href.includes("format=html"));
  assert.ok(jsonLink, `UI has no frozen JSON read link for report ${reportID}`);
  assert.ok(htmlLink, `UI has no frozen HTML read link for report ${reportID}`);
  const [jsonResponse, htmlResponse] = await Promise.all([
    page.request.get(jsonLink.href),
    page.request.get(htmlLink.href),
  ]);
  assert.equal(jsonResponse.status(), 200, "frozen JSON link was not readable");
  assert.equal(htmlResponse.status(), 200, "frozen HTML link was not readable");
  assert.match(jsonResponse.headers()["content-type"] ?? "", /application\/json/i);
  assert.match(htmlResponse.headers()["content-type"] ?? "", /text\/html/i);
  assert.match(htmlResponse.headers()["content-security-policy"] ?? "", /default-src 'none'/);
  const json = Buffer.from(await jsonResponse.body());
  const html = Buffer.from(await htmlResponse.body());
  assert.doesNotThrow(() => JSON.parse(json.toString("utf8")), "frozen JSON is invalid");
  assert.ok(html.toString("utf8").toLowerCase().includes("<!doctype html>"), "frozen HTML document is incomplete");
  return { json, html };
}
