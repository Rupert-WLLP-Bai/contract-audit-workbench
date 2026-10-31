import { chromiumExecutable } from "./chromium";
import { parseReviewWorkspace } from "../src/api/parse";
import type { ConstraintEvaluation, ReviewWorkspace } from "../src/api/types";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium, type Locator, type Page } from "playwright";

type Remediation = {
  id: string;
  run_id: string;
  attempt_id: string;
  finding_ids: string[];
  status: string;
};
type Report = { id: string; run_id: string; attempt_id: string };
type CaseDetail = {
  id: string;
  title: string;
  archived_at: string | null;
  remediations: Remediation[];
  reports: Report[];
  archives: Array<{ id: string }>;
};
type Dataset = {
  dataset_id: string;
  sha256: string;
  template_id: string;
  scenarios: Array<{ id: string; fields: Record<string, string> }>;
};

function required(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}

const web = required("E2E_WEB").replace(/\/$/, "");
const caseID = required("E2E_CASE_ID");
const sourceRunID = required("E2E_SOURCE_RUN_ID");
const sourceAttemptID = required("E2E_SOURCE_ATTEMPT_ID");
const candidateRunID = required("E2E_CANDIDATE_RUN_ID");
const candidateAttemptID = required("E2E_CANDIDATE_ATTEMPT_ID");
const datasetPath = required("E2E_TELECOM_DATASET");
const scenarioID = process.env.E2E_TELECOM_SCENARIO || "conflict";
const sourceSHA = (process.env.E2E_SOURCE_SHA ?? "").trim();
const serverSHA = (process.env.E2E_SERVER_SHA ?? "").trim();
const executionSHA = (process.env.E2E_EXECUTION_SHA ?? "").trim();
const outDir = resolve(process.env.E2E_OUT ?? `../artifacts/d2-8-browser/c1b-resume-${Date.now()}`);
const username = process.env.E2E_USERNAME || "demo-admin";
const password = process.env.E2E_PASSWORD || "admin-demo";
const browser = await chromium.launch({
  headless: true,
  executablePath: chromiumExecutable(),
  args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const consoleErrors: string[] = [];
const httpErrors: string[] = [];
page.on("console", (message) => {
  if (message.type() === "error") consoleErrors.push(message.text());
});
page.on("pageerror", (error) => consoleErrors.push(error.message));
page.on("response", (response) => {
  if (response.status() >= 400) {
    httpErrors.push(`${response.request().method()} ${new URL(response.url()).pathname} ${response.status()}`);
  }
});

const observations: Record<string, unknown> = { case_id: caseID };
let exitCode = 0;
try {
  const rawDataset = JSON.parse(await Bun.file(datasetPath).text()) as Dataset & Record<string, unknown>;
  const digestInput = Object.fromEntries(Object.entries(rawDataset).filter(([key]) => key !== "sha256"));
  const actualDatasetSHA = createHash("sha256").update(JSON.stringify(sortKeys(digestInput)), "utf8").digest("hex");
  assert.equal(rawDataset.dataset_id, "telecom-c1b-v1");
  assert.equal(rawDataset.sha256, actualDatasetSHA, "frozen scenario dataset SHA-256 mismatch");
  const scenario = rawDataset.scenarios.find((item) => item.id === scenarioID);
  assert.ok(scenario, `scenario ${scenarioID} is absent from the frozen dataset`);

  await page.goto(`${web}/login`);
  await page.getByLabel("账号", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.waitForURL(/\/cases(?:$|\?)/, { timeout: 15000 });

  let detail = await waitForCase((value) => value.id === caseID && value.remediations.length > 0);
  assert.equal(detail.archived_at, null, "the existing case was already archived");
  const remediation = detail.remediations.find((item) => item.run_id === sourceRunID && item.attempt_id === sourceAttemptID);
  assert.ok(remediation, "the existing source remediation was not found");
  observations.remediation_id = remediation.id;
  observations.remediation_status_before = remediation.status;
  assert.equal(remediation.status, "submitted", "the existing remediation is not awaiting verification");

  const source = await loadWorkspace(sourceRunID, sourceAttemptID);
  const candidate = await loadWorkspace(candidateRunID, candidateAttemptID);
  observations.source_attempt_state = source.review_attempts?.attempt.state ?? null;
  observations.candidate_attempt_state = candidate.review_attempts?.attempt.state ?? null;
  observations.candidate_previous_run_id = candidate.previous_run_id ?? null;
  observations.revision_comparison_reason = candidate.revision_comparison_reason ?? null;
  observations.candidate_revision_marks = (candidate.revisions ?? []).map((revision) => ({
    previous_finding_id: revision.previous_finding_id,
    mark: revision.mark,
    relation: revision.relation,
  }));
  assert.equal(source.review_attempts?.attempt.id, sourceAttemptID);
  assert.equal(candidate.review_attempts?.attempt.id, candidateAttemptID);
  assert.equal(candidate.previous_run_id, sourceRunID);
  assert.equal(candidate.revision_comparison_reason ?? null, null, "candidate workspace still reports an incomparable run");
  assert.ok(isTerminal(source.review_attempts?.attempt.state ?? ""), "source attempt is not terminal");
  assert.ok(isTerminal(candidate.review_attempts?.attempt.state ?? ""), "candidate attempt is not terminal");
  const sourceFindingIDs = source.findings.map((finding) => finding.id);
  assert.equal(new Set(sourceFindingIDs).size, sourceFindingIDs.length, "source finding IDs are not unique");
  const allSourceFindingsCompatible = assertCompatible(sourceFindingIDs, workspaceEvaluations(source), workspaceEvaluations(candidate));
  observations.all_source_findings_compatible = allSourceFindingsCompatible;
  const eligibility = assertCompatible(remediation.finding_ids, workspaceEvaluations(source), workspaceEvaluations(candidate));
  observations.evaluation_eligibility = eligibility;
  await page.goto(`${web}/cases/${caseID}`);
  await page.getByRole("heading", { name: detail.title, exact: true }).waitFor();
  await selectRunAndAttempt(page.getByLabel("选择复核的审查和执行记录", { exact: true }), candidateRunID, candidateAttemptID);
  const verifyReason = page.getByLabel("复核理由", { exact: true });
  await verifyReason.waitFor({ state: "visible", timeout: 15000 });
  try {
    await page.waitForFunction(() => {
      const label = Array.from(document.querySelectorAll("label")).find((node) => (node.textContent ?? "").trim().startsWith("复核理由"));
      const control = label?.querySelector("textarea");
      return control instanceof HTMLTextAreaElement && !control.disabled;
    }, undefined, { timeout: 30000 });
  } catch (error) {
    const diagnostic = {
      source_run_id: sourceRunID,
      candidate_run_id: candidateRunID,
      source_attempt_id: sourceAttemptID,
      candidate_attempt_id: candidateAttemptID,
      candidate_previous_run_id: candidate.previous_run_id ?? null,
      revision_comparison_reason: candidate.revision_comparison_reason ?? null,
      evaluation_eligibility: eligibility,
      visible_callouts: await page.locator(".callout").allTextContents(),
      verification_reason_disabled: await verifyReason.isDisabled(),
    };
    throw new Error(`existing remediation remained disabled: ${JSON.stringify(diagnostic)}; ${error instanceof Error ? error.message : String(error)}`);
  }

  if (remediation.status === "submitted") {
    await verifyReason.fill("已在原案件核实新尝试的规则能力和业务对象均兼容。");
    const verifyResponse = page.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().includes(`/remediations/${remediation.id}/verify`),
    );
    await page.getByRole("button", { name: "提交复核", exact: true }).click();
    const response = await verifyResponse;
    observations.verification_status = response.status();
    assert.equal(response.status(), 200, "existing remediation verification was not accepted");
  }
  detail = await waitForCase((value) => value.remediations.some((item) => item.id === remediation.id && item.status === "closed"));
  observations.remediation_status_after = detail.remediations.find((item) => item.id === remediation.id)?.status;
  assert.equal(observations.remediation_status_after, "closed");

  await page.goto(`${web}/cases/${caseID}/reviews/${sourceRunID}?attempt_id=${encodeURIComponent(sourceAttemptID)}`);
  await page.getByRole("heading", { name: "审查工作区", exact: true }).waitFor();
  await page.getByText("已记录确认风险", { exact: false }).waitFor();
  const oldHistory = await page.locator("body").innerText();
  assert.ok(oldHistory.includes("50 Mbit/s"), "old source run no longer displays its frozen draft value");
  assert.ok(oldHistory.includes("100 Mbit/s"), "old source run no longer displays its procurement evidence");
  observations.old_history_values_preserved = true;

  await page.goto(`${web}/cases/${caseID}`);
  await page.getByRole("heading", { name: detail.title, exact: true }).waitFor();
  const firstReport = await ensureReport(sourceRunID, sourceAttemptID);
  const firstReportBytes = await readReportLinks(page, firstReport.id);
  const secondReport = await ensureReport(candidateRunID, candidateAttemptID);
  const secondReportBytes = await readReportLinks(page, secondReport.id);
  assert.ok(secondReportBytes.json.length > 0 && secondReportBytes.html.length > 0);
  const firstReportBytesAfterCreation = await readReportLinks(page, firstReport.id);
  assert.deepEqual(firstReportBytesAfterCreation.json, firstReportBytes.json, "candidate report creation changed the frozen source JSON bytes");
  assert.deepEqual(firstReportBytesAfterCreation.html, firstReportBytes.html, "candidate report creation changed the frozen source HTML bytes");

  await selectReport(page.getByLabel("归档所依据的报告", { exact: true }), secondReport.id);
  await page.getByLabel("归档理由", { exact: true }).fill("已完成原案件复审并归档冻结报告。");
  const archiveResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/archive`),
  );
  await page.getByRole("button", { name: "归档案件", exact: true }).click();
  const archivedResponse = await archiveResponse;
  observations.archive_status = archivedResponse.status();
  assert.equal(archivedResponse.status(), 200, "archive action was not accepted");
  detail = await waitForCase((value) => Boolean(value.archived_at) && value.archives.length > 0);
  observations.archive_id = detail.archives.at(-1)?.id ?? null;
  assert.ok(detail.archived_at, "archive timestamp was not retained");

  const deniedWrite = await page.request.post(`${web}/api/cases/${caseID}/generations`, {
    data: { template_id: rawDataset.template_id, fields: scenario.fields },
  });
  observations.archived_generation_status = deniedWrite.status();
  assert.equal(deniedWrite.status(), 409, "archived case accepted a new generation");
  await page.goto(`${web}/cases/${caseID}`);
  await page.getByRole("heading", { name: detail.title, exact: true }).waitFor();
  const sourceReportAfterArchive = await readReportLinks(page, firstReport.id);
  const candidateReportAfterArchive = await readReportLinks(page, secondReport.id);
  assert.deepEqual(sourceReportAfterArchive.json, firstReportBytes.json, "archive changed frozen source JSON bytes");
  assert.deepEqual(sourceReportAfterArchive.html, firstReportBytes.html, "archive changed frozen source HTML bytes");
  assert.deepEqual(candidateReportAfterArchive.json, secondReportBytes.json, "archive changed frozen candidate JSON bytes");
  assert.deepEqual(candidateReportAfterArchive.html, secondReportBytes.html, "archive changed frozen candidate HTML bytes");
  observations.frozen_report_bytes_stable = true;

  const expectedUnauthorizedConsole = "Failed to load resource: the server responded with a status of 401 (Unauthorized)";
  const unexpectedConsoleErrors = consoleErrors.filter((message) => message !== expectedUnauthorizedConsole);
  const unexpectedHTTPFailures = httpErrors.filter((failure) => failure !== "GET /api/session 401");
  assert.deepEqual(unexpectedConsoleErrors, [], `unexpected browser console errors: ${unexpectedConsoleErrors.join(" | ")}`);
  assert.deepEqual(unexpectedHTTPFailures, [], `unexpected HTTP failures: ${unexpectedHTTPFailures.join(" | ")}`);
  observations.console_errors = consoleErrors;
  observations.http_errors = httpErrors;
  observations.expected_unauthenticated_session_probe = httpErrors.includes("GET /api/session 401");
  observations.result = "existing C1-B case recovered through verification, frozen reports, archive, and read-only history";
  console.log(JSON.stringify({
    source_sha: sourceSHA === "" ? "NOT_RECORDED" : sourceSHA,
    server_sha: serverSHA === "" ? "NOT_RECORDED" : serverSHA,
    execution_sha: executionSHA === "" ? "NOT_RECORDED" : executionSHA,
    dataset_sha256: rawDataset.sha256,
    case_id: caseID,
    source_run_id: sourceRunID,
    source_attempt_id: sourceAttemptID,
    candidate_run_id: candidateRunID,
    candidate_attempt_id: candidateAttemptID,
    remediation_id: remediation.id,
    first_report_id: firstReport.id,
    final_report_id: secondReport.id,
    archive_id: detail.archives.at(-1)?.id,
    result: observations.result,
  }, null, 2));
} catch (error) {
  exitCode = 1;
  observations.error = error instanceof Error ? error.message : String(error);
  console.error(`FAIL: ${observations.error}`);
} finally {
  await browser.close();
  await mkdir(outDir, { recursive: true });
  await writeFile(resolve(outDir, "resume.json"), `${JSON.stringify({
    captured_at: new Date().toISOString(),
    browser: "Chromium",
    executable: chromiumExecutable(),
    web,
    source_sha: sourceSHA === "" ? "NOT_RECORDED" : sourceSHA,
    server_sha: serverSHA === "" ? "NOT_RECORDED" : serverSHA,
    execution_sha: executionSHA === "" ? "NOT_RECORDED" : executionSHA,
    observations,
    console_errors: consoleErrors,
    http_errors: httpErrors,
    result: exitCode === 0 ? "passed" : "failed",
  }, null, 2)}\n`, "utf8");
}
process.exit(exitCode);

async function loadWorkspace(runID: string, attemptID: string): Promise<ReviewWorkspace> {
  const response = await page.request.get(`${web}/api/cases/${caseID}/reviews/${runID}?attempt_id=${encodeURIComponent(attemptID)}`);
  assert.equal(response.status(), 200, `review ${runID}/${attemptID} returned HTTP ${response.status()}`);
  return parseReviewWorkspace(await response.json());
}

async function waitForCase(ready: (detail: CaseDetail) => boolean): Promise<CaseDetail> {
  const deadline = Date.now() + 20000;
  let last: CaseDetail | null = null;
  while (Date.now() < deadline) {
    const response = await page.request.get(`${web}/api/cases/${caseID}`);
    assert.equal(response.status(), 200, `case ${caseID} returned HTTP ${response.status()}`);
    last = await response.json() as CaseDetail;
    if (ready(last)) return last;
    await page.waitForTimeout(100);
  }
  throw new Error(`case ${caseID} did not reach expected state; last detail=${JSON.stringify(last)}`);
}

async function ensureReport(runID: string, attemptID: string): Promise<Report> {
  let detail = await waitForCase((value) => value.id === caseID);
  let report = detail.reports.find((item) => item.run_id === runID && item.attempt_id === attemptID);
  if (!report) {
    await selectRunAndAttempt(page.getByLabel("选择审查和执行记录", { exact: true }), runID, attemptID);
    const responsePromise = page.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/reports`),
    );
    await page.getByRole("button", { name: "生成报告", exact: true }).click();
    const response = await responsePromise;
    assert.ok([200, 201].includes(response.status()), `report ${runID}/${attemptID} returned HTTP ${response.status()}`);
    detail = await waitForCase((value) => value.reports.some((item) => item.run_id === runID && item.attempt_id === attemptID));
    report = detail.reports.find((item) => item.run_id === runID && item.attempt_id === attemptID);
  }
  assert.ok(report, `report for ${runID}/${attemptID} was absent`);
  return report;
}

async function readReportLinks(currentPage: Page, reportID: string): Promise<{ json: Buffer; html: Buffer }> {
  const matches = await currentPage.locator("a").evaluateAll((elements, id) => elements
    .filter((element) => (element as HTMLAnchorElement).href.includes(`/reports/${id}`))
    .map((element) => ({ href: (element as HTMLAnchorElement).href, text: element.textContent ?? "" })), reportID);
  const jsonLink = matches.find((item) => item.text.includes("查看 JSON 报告") || item.href.includes("format=json"));
  const htmlLink = matches.find((item) => item.text.includes("查看 HTML 报告") || item.href.includes("format=html"));
  assert.ok(jsonLink, `UI has no frozen JSON link for report ${reportID}`);
  assert.ok(htmlLink, `UI has no frozen HTML link for report ${reportID}`);
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
  assert.ok(html.toString("utf8").toLowerCase().includes("<!doctype html>"), "frozen HTML is incomplete");
  return { json, html };
}

async function selectRunAndAttempt(select: Locator, runID: string, attemptID: string): Promise<void> {
  const options = await select.locator("option").evaluateAll((elements) => elements.map((element) => {
    const option = element as HTMLOptionElement;
    return { value: option.value, text: option.textContent ?? "" };
  }));
  const target = options.find((option) => option.value.includes(runID) || option.text.includes(runID) ||
    option.value.includes(attemptID) || option.text.includes(attemptID));
  assert.ok(target, `no option matches ${runID}/${attemptID}: ${JSON.stringify(options)}`);
  await select.selectOption(target.value);
}

async function selectReport(select: Locator, reportID: string): Promise<void> {
  const options = await select.locator("option").evaluateAll((elements) => elements.map((element) => {
    const option = element as HTMLOptionElement;
    return { value: option.value, text: option.textContent ?? "" };
  }));
  const target = options.find((option) => option.value.includes(reportID) || option.text.includes(reportID));
  assert.ok(target, `report ${reportID} was unavailable: ${JSON.stringify(options)}`);
  await select.selectOption(target.value);
}

function workspaceEvaluations(workspace: ReviewWorkspace): ConstraintEvaluation[] {
  assert.ok(Array.isArray(workspace.evaluations), `review ${workspace.run.id} omitted evaluations`);
  return workspace.evaluations;
}

function assertCompatible(findingIDs: string[], sourceEvaluations: ConstraintEvaluation[], candidateEvaluations: ConstraintEvaluation[]) {
  return findingIDs.map((findingID) => {
    const sourceRows = sourceEvaluations.filter((evaluation) => evaluation.finding_id === findingID);
    assert.equal(sourceRows.length, 1, `source finding ${findingID} has ${sourceRows.length} rule evaluations`);
    const source = sourceRows[0]!;
    assert.ok(source.rule_id.trim() && source.rule_version.trim() && source.capability_id.trim());
    assert.ok(uniqueRefs(source.object_refs), `source finding ${findingID} has invalid object identity`);
    const matching = candidateEvaluations.filter((evaluation) => evaluation.rule_id === source.rule_id &&
      evaluation.rule_version === source.rule_version && evaluation.capability_id === source.capability_id &&
      uniqueRefs(evaluation.object_refs) && sameRefs(source.object_refs, evaluation.object_refs));
    assert.equal(matching.length, 1, `source finding ${findingID} has ${matching.length} exact candidate evaluations`);
    assert.equal(matching[0]!.relation, "compatible", `candidate evaluation for ${findingID} is ${matching[0]!.relation}`);
    return { finding_id: findingID, rule_id: source.rule_id, rule_version: source.rule_version,
      capability_id: source.capability_id, object_refs: source.object_refs, candidate_relation: matching[0]!.relation };
  });
}

function uniqueRefs(values: string[]): boolean {
  return values.length > 0 && values.every((value) => value.trim() !== "") && new Set(values).size === values.length;
}

function sameRefs(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((value) => right.includes(value));
}

function isTerminal(state: string): boolean {
  return state !== "" && !["accepted", "queued", "running"].includes(state);
}

function sortKeys(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortKeys);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).sort(([left], [right]) => left.localeCompare(right)).map(([key, item]) => [key, sortKeys(item)]));
  }
  return value;
}
