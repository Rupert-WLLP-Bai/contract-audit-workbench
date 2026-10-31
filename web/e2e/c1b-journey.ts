import { chromiumExecutable } from "./chromium";
import { C1B_BUSINESS_AT_UTC, C1B_BUSINESS_TIME_ZONE, enterC1BBusinessAt } from "./c1b-clock";
import { parseReviewWorkspace } from "../src/api/parse";
import type { ConstraintEvaluation, ReviewDiagnostics as ReviewDiagnosticsDTO, ReviewWorkspace } from "../src/api/types";
import { readFrozenGovernance, type FrozenRulepack } from "./c1b-frozen-governance";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { chromium, type Locator, type Page } from "playwright";
import { replaceMaterial, replacePrimaryMaterial, uploadNewMaterial } from "./materials-ui";
import { chooseAntOption, selectReportOption, selectReviewAttempt } from "./c1b-select";

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
type CaseDetail = {
  id: string;
  business_line: string;
  scenario: string;
  documents: Array<Record<string, unknown>>;
  materials?: Array<{ material_id: string; role: string; purpose: string; current_document_id: string; current_version: number }>;
  generations: Array<Record<string, unknown>>;
  remediations: Array<Record<string, unknown>>;
  reports: Array<{ id: string; run_id: string; attempt_id: string; version: number; created_at: string }>;
  archives: Array<Record<string, unknown>>;
  archived_at: string | null;
};
type DiagnosticDocument = {
  id: string;
  digest: string;
  parsed_document?: { id: string; original_digest?: string; artifact_digest?: string; normalized_digest?: string | null } | null;
};
type ReviewDiagnostics = Omit<ReviewDiagnosticsDTO, "snapshot" | "execution"> & {
  snapshot: NonNullable<ReviewDiagnosticsDTO["snapshot"]>;
  execution: NonNullable<ReviewDiagnosticsDTO["execution"]>;
  governed_rulepack: FrozenRulepack;
  documents: DiagnosticDocument[];
};

const web = required("E2E_WEB").replace(/\/$/, "");
const datasetPath = required("E2E_TELECOM_DATASET");
const scenarioID = process.env.E2E_TELECOM_SCENARIO || "conflict";
const sourceSHA = (process.env.E2E_SOURCE_SHA ?? "").trim();
const serverSHA = (process.env.E2E_SERVER_SHA ?? "").trim();
const executionSHA = (process.env.E2E_EXECUTION_SHA ?? "").trim();
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
const consoleErrorURLs: string[] = [];
const reviewResponses = new Map<string, Record<string, unknown>>();
page.on("response", async (response) => {
  const url = response.url();
  if (!/\/api\/cases\/[^/]+\/reviews\/[^/]+/.test(url) || response.status() !== 200) return;
  try {
    const data = parseReviewWorkspace(await response.json());
    reviewResponses.set(url, {
      run_id: data.run?.id,
      run_state: data.run?.state,
      previous_run_id: data.previous_run_id,
      revision_comparison_reason: data.revision_comparison_reason,
      attempt_id: data.review_attempts?.attempt?.id,
      attempt_state: data.review_attempts?.attempt?.state,
    });
  } catch {
    // Polling may abort a body mid-read; the next response replaces this entry.
  }
});
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
  await chooseAntSelect(page, dialog.getByLabel("合同类型", { exact: true }), "服务合同");
  await dialog.getByRole("button", { name: "创建", exact: true }).click();
  try {
    await page.waitForURL(/\/cases\/[^/]+$/);
  } catch (error) {
    const body = await page.locator("body").innerText();
    throw new Error(`case creation did not navigate: url=${page.url()} body=${body.slice(0, 1200)}`);
  }
  const caseID = new URL(page.url()).pathname.split("/").at(-1) ?? "";
  assert.ok(caseID, "created case ID was not present in the route");
  const initialDetail = await waitForCase(page, caseID, (detail) => detail.business_line === "ENT" && detail.scenario === "telecom_dedicated_line");
  assert.equal(initialDetail.business_line, "ENT");
  assert.equal(initialDetail.scenario, "telecom_dedicated_line");

  const templateResponse = await page.request.get(`${web}/api/templates`);
  assert.equal(templateResponse.status(), 200, "template listing failed");
  const templateData = await templateResponse.json() as { items: Array<{ id: string; name: string; version: string; fields: Array<{ key: string; label: string; required: boolean }> }> };
  const template = templateData.items.find((item) => item.id === dataset.template_id);
  assert.ok(template, `template ${dataset.template_id} not returned by the API`);
  assert.equal(template.version, dataset.template_version, "template version differs from the frozen dataset");
  assert.deepEqual(new Set(template.fields.map((field) => field.key)), new Set(Object.keys(scenario.fields)));

  const templateSelect = page.getByLabel("模板", { exact: true });
  await chooseAntSelect(page, templateSelect, `${template.name}，${template.version}`);
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
  const samePurposePair = scenario.id === "conflict" ? {
    first: await uploadNewMaterial(page, {
      role: "attachment",
      purpose: "supplement",
      filename: "联系人说明-A1.txt",
      text: "项目联系人说明：甲方联系人为联系人甲，电话 010-10000001。",
    }),
    second: await uploadNewMaterial(page, {
      role: "attachment",
      purpose: "supplement",
      filename: "联系人说明-B.txt",
      text: "项目联系人说明：乙方联系人为联系人乙，电话 010-10000002。",
    }),
  } : null;
  if (samePurposePair) {
    const pairDetail = await waitForCase(page, caseID, (detail) =>
      detail.documents.some((item) => item.id === samePurposePair.first.id) &&
      detail.documents.some((item) => item.id === samePurposePair.second.id),
    );
    for (const upload of [samePurposePair.first, samePurposePair.second]) {
      const material = pairDetail.materials?.find((item) => item.material_id === upload.material_id);
      assert.equal(material?.purpose, "supplement", `${upload.id} did not retain the selected supplement purpose`);
      assert.equal(material?.current_document_id, upload.id, `${upload.id} is not the current document for its own material`);
      assert.equal(material?.current_version, 1, `${upload.id} did not start at material version 1`);
      await waitForCurrentMaterialParsed(page, upload.material_id);
    }
  }

  await enterC1BBusinessAt(page);
  const firstReviewResponsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/reviews`));
  await page.getByRole("button", { name: "开始审查", exact: true }).click();
  const firstReviewResponse = await firstReviewResponsePromise;
  assert.ok(firstReviewResponse.status() === 201 || firstReviewResponse.status() === 202, `review start returned HTTP ${firstReviewResponse.status()}`);
  assert.deepEqual(firstReviewResponse.request().postDataJSON(), { business_at: C1B_BUSINESS_AT_UTC }, "review did not submit the frozen business time");
  try {
    await page.waitForURL(/\/cases\/[^/]+\/reviews\/[^/]+/);
  } catch (error) {
    const body = await page.locator("body").innerText();
    throw new Error(`review acceptance did not navigate: url=${page.url()} http=${httpErrors.join(" | ")} body=${body.slice(0, 1600)}`);
  }
  const firstURL = new URL(page.url());
  const firstRunID = firstURL.pathname.split("/").at(-1) ?? "";
  assert.ok(firstRunID, "initial review run ID was not in the route");
  const firstWorkspace = await waitForWorkspace(page, caseID, firstRunID);
  const firstAttemptID = getAttemptID(firstWorkspace, firstURL.searchParams.get("attempt_id"));
  const firstReviewDiagnostics = await readReviewDiagnostics(page, caseID, firstRunID, firstAttemptID);
  const firstRulepack = firstReviewDiagnostics.governed_rulepack;
  assertFrozenRulepack(firstReviewDiagnostics, firstRulepack, "initial attempt");
  if (firstWorkspace.agent_review?.rulepack)
    assertWorkspaceRulepack(firstWorkspace.agent_review.rulepack, firstRulepack, "initial attempt workspace");
  assertRelations(firstWorkspace, scenario.expected_relations);
  if (dataset.rulepack) assertFrozenRulepack(firstReviewDiagnostics, dataset.rulepack, "initial attempt");
  const firstAttachmentDiagnostics = samePurposePair ? firstReviewDiagnostics : null;
  if (samePurposePair && firstAttachmentDiagnostics) {
    const snapshotIDs = new Set(firstAttachmentDiagnostics.snapshot.document_ids);
    for (const upload of [samePurposePair.first, samePurposePair.second]) {
      assert.ok(snapshotIDs.has(upload.id), `original attempt did not freeze attachment ${upload.filename}`);
      assert.ok(firstWorkspace.documents.some((document) => document.id === upload.id), `original workspace omitted attachment ${upload.filename}`);
      const frozen: ReviewDiagnostics["documents"][number] | undefined = firstAttachmentDiagnostics.documents.find((document) => document.id === upload.id);
      assert.equal(frozen?.digest, createHash("sha256").update(upload === samePurposePair.first
        ? "项目联系人说明：甲方联系人为联系人甲，电话 010-10000001。"
        : "项目联系人说明：乙方联系人为联系人乙，电话 010-10000002。", "utf8").digest("hex"),
      `original attempt changed the uploaded bytes for ${upload.filename}`);
    }
  }

  const bandwidthEval = evaluation(firstWorkspace, "ENT-TECH-BW-001");
  const findingID = String(bandwidthEval.finding_id ?? "");
  assert.ok(findingID, "bandwidth evaluation is not linked to a finding");
  const bandwidthFinding = firstWorkspace.findings.find((item) => item.id === findingID);
  assert.ok(bandwidthFinding, "bandwidth finding is absent from workspace");
  const claim = String(bandwidthFinding.claim ?? "");
  assert.ok(claim, "bandwidth finding has no claim");
  const evidence = bandwidthFinding.evidence;
  const citedDocumentIDs = new Set(evidence.map((item) => String(item.document_version_id ?? "")));
  assert.ok(citedDocumentIDs.has(uploaded.procurement), "bandwidth evidence does not cite the procurement source");
  assert.ok(citedDocumentIDs.has(generationResponse.document_id), "bandwidth evidence does not cite the generated draft");

  const findingButton = page.getByRole("button", { name: `选择线索：${claim}`, exact: true });
  await findingButton.click();
  // The URL changes before React finishes rendering the selected finding.
  // Wait for the business selection before inspecting evidence or deciding.
  await findingButton.and(page.locator('[aria-current="true"]')).waitFor();
  await page.getByRole("button", { name: "双来源对照", exact: true }).click();
  await page.locator(".document-split mark.hit-evidence").nth(1).waitFor();
  const highlighted = await page.locator(".document-split mark.hit-evidence").allTextContents();
  assert.ok(highlighted.length >= 2, `bidirectional evidence view showed ${highlighted.length} highlights`);
  const referencesHeading = page.getByRole("heading", { name: "引用该材料的线索", exact: true });
  await referencesHeading.waitFor();
  await page.getByRole("button", { name: `查看线索：${claim}`, exact: true }).click();
  await findingButton.and(page.locator('[aria-current="true"]')).waitFor();
  await page.locator(".document-split mark.hit-evidence").nth(1).waitFor();

  await page.locator(`input[type="radio"][name="decision-${findingID}"][value="confirm_risk"]`).check();
  await page.getByLabel("理由", { exact: true }).fill(`采购材料要求 ${baselineBandwidth}，拟签合同为 ${originalDraftBandwidth}。`);
  const [decisionResponse] = await Promise.all([
    page.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().includes(`/reviews/${firstRunID}/findings/${findingID}/decisions`),
    ),
    page.getByRole("button", { name: "保存决定", exact: true }).click(),
  ]);
  assert.equal(decisionResponse.status(), 204, "human decision was not recorded");

  await page.goto(`${web}/cases/${caseID}`);
  await page.getByRole("heading", { name: title, exact: true }).waitFor();
  await selectReviewAttempt(page, web, caseID, firstRunID, firstAttemptID, page.getByLabel("选择审查和执行记录", { exact: true }));
  await page.getByRole("button", { name: "生成报告", exact: true }).click();
  const firstReport = await waitForReport(page, caseID, firstRunID, firstAttemptID);
  const firstReportBytes = await readReportLinks(page, firstReport.id as string);

  await selectReviewAttempt(page, web, caseID, firstRunID, firstAttemptID, page.getByLabel("选择原审查和执行记录", { exact: true }));
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
  const beforeReplacement = await waitForCase(page, caseID, (detail) => detail.documents.some((item) => item.id === generationResponse.document_id));
  const primaryMaterial = (beforeReplacement as CaseDetail & { materials?: Array<{ material_id: string; purpose: string; current_document_id: string; current_version: number }> }).materials?.find((item) => item.purpose === "primary_contract");
  assert.ok(primaryMaterial, "generated main contract has no material identity");
  assert.equal(primaryMaterial.current_document_id, generationResponse.document_id, "the intended main contract is not current before replacement");
  const primaryDocument = beforeReplacement.documents.find((document) => document.id === primaryMaterial.current_document_id);
  assert.ok(primaryDocument, "current primary document is absent from case detail");
  const replacement = await replacePrimaryMaterial(page, {
    role: "draft_contract",
    filename: "telecom-revised-draft.txt",
    text: revisedText,
  }, primaryMaterial, String(primaryDocument.filename ?? ""));
  const revisedID = replacement.id;
  assert.ok(revisedID, "revised draft upload returned no identity");
  const afterReplacement = await waitForCase(page, caseID, (detail) => detail.documents.some((item) => item.id === revisedID));
  const updatedPrimary = (afterReplacement as CaseDetail & { materials?: Array<{ material_id: string; purpose: string; current_document_id: string; current_version: number }> }).materials?.find((item) => item.purpose === "primary_contract");
  assert.equal(updatedPrimary?.material_id, primaryMaterial.material_id, "the UI replacement changed the selected material identity");
  assert.equal(updatedPrimary?.current_document_id, revisedID, "the UI replacement did not become the current document");
  assert.equal(updatedPrimary?.current_version, primaryMaterial.current_version + 1, "the UI replacement did not create the next material version");

  let attachmentA2: Awaited<ReturnType<typeof replaceMaterial>> | null = null;
  await enterC1BBusinessAt(page);
  const reReviewResponsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/reviews`));
  await page.getByRole("button", { name: "开始复审", exact: true }).click();
  const reReviewResponse = await reReviewResponsePromise;
  assert.ok(reReviewResponse.status() === 201 || reReviewResponse.status() === 202, `re-review start returned HTTP ${reReviewResponse.status()}`);
  assert.deepEqual(reReviewResponse.request().postDataJSON(), { business_at: C1B_BUSINESS_AT_UTC }, "re-review did not submit the frozen business time");
  try {
    await page.waitForURL(/\/cases\/[^/]+\/reviews\/[^/]+/);
  } catch (error) {
    const body = await page.locator("body").innerText();
    throw new Error(`re-review acceptance did not navigate: url=${page.url()} http=${httpErrors.join(" | ")} body=${body.slice(0, 1600)}`);
  }
  const secondURL = new URL(page.url());
  const secondRunID = secondURL.pathname.split("/").at(-1) ?? "";
  assert.ok(secondRunID && secondRunID !== firstRunID, "re-review overwrote the original run");
  const secondWorkspace = await waitForWorkspace(page, caseID, secondRunID);
  const secondAttemptID = getAttemptID(secondWorkspace, secondURL.searchParams.get("attempt_id"));
  assert.notEqual(secondAttemptID, firstAttemptID, "re-review reused the previous attempt");
  const secondReviewDiagnostics = await readReviewDiagnostics(page, caseID, secondRunID, secondAttemptID);
  const secondRulepack = secondReviewDiagnostics.governed_rulepack;
  assertFrozenRulepack(secondReviewDiagnostics, firstRulepack, "re-review");
  if (secondWorkspace.agent_review?.rulepack)
    assertWorkspaceRulepack(secondWorkspace.agent_review.rulepack, secondRulepack, "re-review workspace");
  if (dataset.rulepack) assertFrozenRulepack(secondReviewDiagnostics, dataset.rulepack, "re-review");
  assertRelations(secondWorkspace, compatible.expected_relations);
  await page.goto(`${web}/cases/${caseID}`);
  await page.getByRole("heading", { name: title, exact: true }).waitFor();
  const itemForm = page.getByRole("group", { name: "本案当前的拟签合同修改稿", exact: true });
  await itemForm.locator(`input[type="radio"][value="${revisedID}"]`).check();
  await page.getByLabel("提交理由", { exact: true }).fill("已按采购材料修订并生成新的审查运行。");
  await page.getByRole("button", { name: "提交修改稿", exact: true }).click();
  await page.getByLabel("选择复核的审查和执行记录", { exact: true }).waitFor();
  // The record list is rendered newest-first, so the latest run is record 1.
  await selectReviewAttempt(page, web, caseID, secondRunID, secondAttemptID, page.getByLabel("选择复核的审查和执行记录", { exact: true }));
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
      const sourceRows = workspaceEvaluations(firstWorkspace).filter((evaluation) => evaluation.finding_id === findingID);
      const source = sourceRows[0];
      const sourceRefs = source && uniqueObjectRefs(source.object_refs) ? source.object_refs : null;
      const matching = source && sourceRefs !== null && typeof source.rule_id === "string" && source.rule_id.trim() !== "" &&
        typeof source.rule_version === "string" && source.rule_version.trim() !== "" &&
        typeof source.capability_id === "string" && source.capability_id.trim() !== ""
        ? workspaceEvaluations(secondWorkspace).filter((evaluation) => {
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
      candidate_attempt_id: secondWorkspace.review_attempts?.attempt?.id ?? null,
      expected_candidate_attempt_id: secondAttemptID,
      candidate_attempt_state: secondWorkspace.review_attempts?.attempt?.state ?? null,
      revision_comparison_reason: secondWorkspace.revision_comparison_reason ?? null,
      evaluation_checks: evaluationChecks,
      every_finding_compatible: findingIDs.length > 0 && evaluationChecks.every((check) => check.exactly_one_compatible),
      presentation_revision_marks: revisionMarks,
      remediations: (await page.request.get(`${web}/api/cases/${caseID}`).then((response) => response.json()).catch(() => null))?.remediations ?? null,
      review_responses: [...reviewResponses.entries()].filter(([url]) => url.includes(firstRunID) || url.includes(secondRunID)),
      visible_callouts: await page.locator(".callout").allTextContents(),
      verification_reason_disabled: await verifyReason.isDisabled(),
    };
    throw new Error(`remediation verification readiness failed: ${JSON.stringify(diagnostics)}; ${error instanceof Error ? error.message : String(error)}`);
  }
  await verifyReason.fill("新尝试在相同能力和电路对象下均为 compatible。");
  const [verifyResponse] = await Promise.all([
    page.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().includes(`/remediations/${remediationID}/verify`),
    ),
    page.getByRole("button", { name: "提交复核", exact: true }).click(),
  ]);
  const verifyResponseBody = await verifyResponse.text();
  const caseDetailResponse = await page.request.get(`${web}/api/cases/${caseID}`);
  const caseDetailRaw = caseDetailResponse.status() === 200 ? await caseDetailResponse.json() : null;
  await saveRawEvidence(`remediation-verification-${remediationID}.json`, {
    response: {
      status: verifyResponse.status(),
      url: verifyResponse.url(),
      body: verifyResponseBody,
    },
    scope: {
      case_id: caseID,
      remediation_id: remediationID,
      source_run_id: firstRunID,
      source_attempt_id: firstAttemptID,
      target_run_id: secondRunID,
      target_attempt_id: secondAttemptID,
      finding_ids: remediation.finding_ids ?? null,
      source_evaluations: firstWorkspace.evaluations,
      target_evaluations: secondWorkspace.evaluations,
      source_report_id: firstReport.id,
      case_detail_http_status: caseDetailResponse.status(),
      case_detail_remediations: caseDetailRaw?.remediations ?? null,
      case_detail_reports: caseDetailRaw?.reports ?? null,
    },
  });
  assert.equal(verifyResponse.status(), 200, "compatible re-review did not close remediation");
  const verifiedDetail = await waitForCase(page, caseID, (detail) => detail.remediations.some((item) => item.id === remediationID && item.status === "closed"));
  assert.equal(verifiedDetail.remediations.find((item) => item.id === remediationID)?.status, "closed");

  await page.goto(`${web}/cases/${caseID}/reviews/${firstRunID}?attempt_id=${encodeURIComponent(firstAttemptID)}`);
  await page.getByRole("heading", { name: "审查工作区", exact: true }).waitFor();
  await page.getByText("已记录确认风险", { exact: false }).waitFor();
  await page.getByRole("button", { name: `选择线索：${claim}`, exact: true }).click();
  await findingButton.and(page.locator('[aria-current="true"]')).waitFor();
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
  // Record 1 is the newest saved run, which is the re-review after run 1.
  await selectReviewAttempt(page, web, caseID, secondRunID, secondAttemptID, page.getByLabel("选择审查和执行记录", { exact: true }));
  await page.getByRole("button", { name: "生成报告", exact: true }).click();
  const secondReport = await waitForReport(page, caseID, secondRunID, secondAttemptID);
  const secondReportBytes = await readReportLinks(page, secondReport.id as string);
  assert.ok(secondReportBytes.json.length > 0 && secondReportBytes.html.length > 0);
  const firstReportBytesAfter = await readReportLinks(page, firstReport.id as string);
  assert.deepEqual(firstReportBytesAfter.json, firstReportBytes.json, "later report creation changed frozen JSON bytes");
  assert.deepEqual(firstReportBytesAfter.html, firstReportBytes.html, "later report creation changed frozen HTML bytes");
  const secondReportBytesBeforeAttachmentUpdate = secondReportBytes;
  const firstReportBytesBeforeAttachmentUpdate = firstReportBytes;
  const secondAttachmentDiagnostics = secondReviewDiagnostics;

  if (samePurposePair) {
    const attachmentTarget = afterReplacement.materials?.find((item) => item.material_id === samePurposePair.first.material_id);
    const attachmentB = afterReplacement.materials?.find((item) => item.material_id === samePurposePair.second.material_id);
    assert.ok(attachmentTarget, "contact note A has no current material projection");
    assert.ok(attachmentB, "contact note B has no current material projection");
    assert.equal(attachmentTarget.current_document_id, samePurposePair.first.id, "contact note A changed before explicit replacement");
    const attachmentA1Doc = afterReplacement.documents.find((item) => item.id === samePurposePair.first.id);
    assert.ok(attachmentA1Doc, "current document for contact note A is absent");
    attachmentA2 = await replaceMaterial(page, {
      role: "attachment",
      purpose: "supplement",
      filename: "联系人说明-A2.txt",
      text: "项目联系人说明：甲方联系人为联系人甲，电话 010-10000001；本次更新了联系人说明文件。",
    }, attachmentTarget, String(attachmentA1Doc.filename ?? ""), "补充协议");
    const afterAttachmentReplacement = await waitForCase(page, caseID, (detail) =>
      detail.documents.some((item) => item.id === attachmentA2?.id),
    );
    const updatedA = afterAttachmentReplacement.materials?.find((item) => item.material_id === samePurposePair.first.material_id);
    const unchangedB = afterAttachmentReplacement.materials?.find((item) => item.material_id === samePurposePair.second.material_id);
    assert.equal(attachmentA2.material_id, attachmentTarget.material_id, "attachment A replacement changed material identity");
    assert.equal(attachmentA2.version, attachmentTarget.current_version + 1, "attachment A replacement did not increment its version");
    assert.equal(updatedA?.current_document_id, attachmentA2.id, "attachment A replacement is not current after refresh");
    assert.equal(updatedA?.current_version, attachmentTarget.current_version + 1, "attachment A current version did not increment after refresh");
    assert.equal(unchangedB?.current_document_id, samePurposePair.second.id, "replacing attachment A changed attachment B's current document");
    assert.equal(unchangedB?.current_version, 1, "replacing attachment A changed attachment B's version");
    await page.reload();
    await page.getByRole("heading", { name: title, exact: true }).waitFor();
    const currentA = page.locator(`.material-current-set li[data-material-id="${samePurposePair.first.material_id}"]`);
    const currentB = page.locator(`.material-current-set li[data-material-id="${samePurposePair.second.material_id}"]`);
    await currentA.getByText("联系人说明-A2.txt", { exact: true }).waitFor();
    await currentB.getByText("联系人说明-B.txt", { exact: true }).waitFor();
    assert.match(await currentA.innerText(), /当前版本 v2/);
    assert.match(await currentB.innerText(), /当前版本 v1/);
    assert.doesNotMatch(await currentA.innerText(), /联系人说明-A1\.txt/);
    await waitForCurrentMaterialParsed(page, samePurposePair.first.material_id);
  }

  let thirdRunID = "";
  let thirdAttemptID = "";
  let thirdWorkspace: ReviewWorkspace | null = null;
  let thirdReviewDiagnostics: ReviewDiagnostics | null = null;
  if (samePurposePair && attachmentA2) {
    await enterC1BBusinessAt(page);
    const thirdReviewResponsePromise = page.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/reviews`));
    await page.getByRole("button", { name: "开始复审", exact: true }).click();
    const thirdReviewResponse = await thirdReviewResponsePromise;
    assert.ok(thirdReviewResponse.status() === 201 || thirdReviewResponse.status() === 202, `attachment re-review start returned HTTP ${thirdReviewResponse.status()}`);
    assert.deepEqual(thirdReviewResponse.request().postDataJSON(), { business_at: C1B_BUSINESS_AT_UTC }, "attachment re-review changed the frozen business time");
    await page.waitForURL(/\/cases\/[^/]+\/reviews\/[^/]+/);
    const thirdURL = new URL(page.url());
    thirdRunID = thirdURL.pathname.split("/").at(-1) ?? "";
    assert.ok(thirdRunID && thirdRunID !== firstRunID && thirdRunID !== secondRunID, "attachment re-review did not create a distinct run");
    thirdWorkspace = await waitForWorkspace(page, caseID, thirdRunID);
    thirdAttemptID = getAttemptID(thirdWorkspace, thirdURL.searchParams.get("attempt_id"));
    assert.notEqual(thirdAttemptID, firstAttemptID, "attachment re-review reused the initial attempt");
    assert.notEqual(thirdAttemptID, secondAttemptID, "attachment re-review reused the draft re-review attempt");
    thirdReviewDiagnostics = await readReviewDiagnostics(page, caseID, thirdRunID, thirdAttemptID);
    assertFrozenRulepack(thirdReviewDiagnostics, firstRulepack, "attachment re-review");
    if (thirdWorkspace.agent_review?.rulepack)
      assertWorkspaceRulepack(thirdWorkspace.agent_review.rulepack, firstRulepack, "attachment re-review workspace");
    if (dataset.rulepack) assertFrozenRulepack(thirdReviewDiagnostics, dataset.rulepack, "attachment re-review");
    assertRelations(thirdWorkspace, compatible.expected_relations);
    const thirdSnapshotIDs = new Set(thirdReviewDiagnostics.snapshot.document_ids);
    for (const id of [attachmentA2.id, samePurposePair.second.id]) {
      assert.ok(thirdSnapshotIDs.has(id), `attachment re-review did not freeze current attachment ${id}`);
      assert.ok(thirdWorkspace.documents.some((document) => document.id === id), `attachment re-review workspace omitted current attachment ${id}`);
    }
    assert.ok(!thirdSnapshotIDs.has(samePurposePair.first.id), "attachment re-review still selected replaced attachment A1");
    const currentA2 = diagnosticDocument(thirdReviewDiagnostics, attachmentA2.id);
    const currentB = diagnosticDocument(thirdReviewDiagnostics, samePurposePair.second.id);
    assert.equal(currentA2?.digest, createHash("sha256").update("项目联系人说明：甲方联系人为联系人甲，电话 010-10000001；本次更新了联系人说明文件。", "utf8").digest("hex"),
      "attachment re-review A2 content hash differs from uploaded bytes");
    assert.equal(currentB?.digest, diagnosticDocument(firstAttachmentDiagnostics!, samePurposePair.second.id)?.digest,
      "unchanged attachment B hash changed between attempts");
    const firstAfterAttachment = await readReviewDiagnostics(page, caseID, firstRunID, firstAttemptID);
    const secondAfterAttachment = await readReviewDiagnostics(page, caseID, secondRunID, secondAttemptID);
    for (const id of [samePurposePair.first.id, samePurposePair.second.id]) {
      assert.deepEqual(diagnosticDocument(firstAfterAttachment, id), diagnosticDocument(firstAttachmentDiagnostics!, id),
        `later attachment replacement changed original attempt document ${id}`);
      assert.deepEqual(diagnosticDocument(secondAfterAttachment, id), diagnosticDocument(secondAttachmentDiagnostics, id),
        `later attachment replacement changed draft re-review document ${id}`);
    }
    assert.equal(firstAfterAttachment.execution?.attempt?.input_hash, firstAttachmentDiagnostics?.execution?.attempt?.input_hash,
      "later attachment replacement changed initial attempt input hash");
    assert.equal(secondAfterAttachment.execution?.attempt?.input_hash, secondReviewDiagnostics.execution?.attempt?.input_hash,
      "later attachment replacement changed draft re-review input hash");
  }

  await page.goto(`${web}/cases/${caseID}`);
  await page.getByRole("heading", { name: title, exact: true }).waitFor();
  const reportRunID = thirdRunID || secondRunID;
  const reportAttemptID = thirdAttemptID || secondAttemptID;
  await selectReviewAttempt(page, web, caseID, reportRunID, reportAttemptID, page.getByLabel("选择审查和执行记录", { exact: true }));
  await page.getByRole("button", { name: "生成报告", exact: true }).click();
  const finalReport = await waitForReport(page, caseID, reportRunID, reportAttemptID);
  const finalReportBytes = await readReportLinks(page, finalReport.id as string);
  assert.ok(finalReportBytes.json.length > 0 && finalReportBytes.html.length > 0);
  const firstReportBytesAfterAttachmentReview = await readReportLinks(page, firstReport.id as string);
  const secondReportBytesAfterAttachmentReview = await readReportLinks(page, secondReport.id as string);
  assert.deepEqual(firstReportBytesAfterAttachmentReview.json, firstReportBytesBeforeAttachmentUpdate.json, "attachment re-review changed original frozen JSON report bytes");
  assert.deepEqual(firstReportBytesAfterAttachmentReview.html, firstReportBytesBeforeAttachmentUpdate.html, "attachment re-review changed original frozen HTML report bytes");
  assert.deepEqual(secondReportBytesAfterAttachmentReview.json, secondReportBytesBeforeAttachmentUpdate.json, "attachment re-review changed draft re-review JSON report bytes");
  assert.deepEqual(secondReportBytesAfterAttachmentReview.html, secondReportBytesBeforeAttachmentUpdate.html, "attachment re-review changed draft re-review HTML report bytes");

  await selectReportOption(page, page.getByLabel("归档所依据的报告", { exact: true }), finalReport);
  await page.getByLabel("归档理由", { exact: true }).fill("已完成复审并归档冻结报告。");
  const [archiveResponse] = await Promise.all([
    page.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().endsWith(`/api/cases/${caseID}/archive`),
    ),
    page.getByRole("button", { name: "归档案件", exact: true }).click(),
  ]);
  assert.equal(archiveResponse.status(), 200, "archive action was not accepted");
  const archived = await waitForCase(page, caseID, (detail) => Boolean(detail.archived_at) && detail.archives.length > 0);
  const finalArchive = archived.archives.find((item) => item.report_id === finalReport.id);
  assert.ok(finalArchive, `final archive did not point to the third report ${String(finalReport.id)}`);
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
    business_at: C1B_BUSINESS_AT_UTC,
    business_time_zone: C1B_BUSINESS_TIME_ZONE,
    rulepack: firstRulepack,
    case_id: caseID,
    original_run_id: firstRunID,
    re_review_run_id: secondRunID,
    first_report_id: firstReport.id,
    final_report_id: finalReport.id,
    attachment_re_review_run_id: thirdRunID || null,
    attachment_re_review_attempt_id: thirdAttemptID || null,
    remediation_status: "closed",
    archive_id: finalArchive.id,
    archived_report_id: finalArchive.report_id,
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
  const [response] = await Promise.all([
    page.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().includes(pathPrefix) && response.url().endsWith(suffix),
    ),
    action(),
  ]);
  assert.equal(response.status(), 201, `${suffix} response returned HTTP ${response.status()}`);
  return await response.json() as Record<string, any>;
}

async function chooseAntSelect(page: Page, select: Locator, optionText: string): Promise<void> {
  await chooseAntOption(page, select, (label) => label === optionText);
}

async function uploadMaterial(page: Page, material: Material): Promise<string> {
  const result = await uploadNewMaterial(page, material);
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

async function waitForCurrentMaterialParsed(page: Page, materialID: string): Promise<void> {
  await page.waitForFunction((id) => {
    const row = document.querySelector(`.material-current-set li[data-material-id="${CSS.escape(id)}"]`);
    return row?.querySelector<HTMLElement>(".parsed-document-summary")?.dataset.status === "ready";
  }, materialID, { timeout: 30000 });
}

async function waitForWorkspace(page: Page, caseID: string, runID: string): Promise<ReviewWorkspace> {
  const deadline = Date.now() + 90000;
  let last: ReviewWorkspace | null = null;
  while (Date.now() < deadline) {
    const response = await page.request.get(`${web}/api/cases/${caseID}/reviews/${runID}`);
    assert.equal(response.status(), 200, `review ${runID} returned HTTP ${response.status()}`);
    const raw = await response.json();
    await saveRawEvidence(`workspace-${runID}.json`, raw);
    last = parseReviewWorkspace(raw);
    if (!["accepted", "queued", "running"].includes(last.run.state)) return last;
    await page.waitForTimeout(200);
  }
  throw new Error(`review ${runID} did not terminate; last workspace=${JSON.stringify(last)}`);
}

async function saveRawEvidence(filename: string, value: unknown): Promise<void> {
  const directory = process.env.CAW_DELIVERY_DIR;
  if (!directory) return;
  const target = join(directory, "browser-evidence");
  await mkdir(target, { recursive: true });
  await writeFile(join(target, filename), `${JSON.stringify(value, null, 2)}\n`, "utf8");
}

async function readReviewDiagnostics(page: Page, caseID: string, runID: string, attemptID: string): Promise<ReviewDiagnostics> {
  const { raw, diagnostics, rulepack } = await readFrozenGovernance(page, web, caseID, runID, attemptID);
  await saveRawEvidence(`diagnostics-${runID}-${attemptID}.json`, raw);
  assert.ok(diagnostics.snapshot, "review diagnostics omitted the snapshot");
  assert.ok(diagnostics.execution, "review diagnostics omitted frozen execution details");
  const documents = parseDiagnosticDocuments(raw);
  assert.equal(diagnostics.snapshot.document_ids instanceof Array, true, "review diagnostics omitted frozen document identities");
  return { ...diagnostics, snapshot: diagnostics.snapshot, execution: diagnostics.execution, governed_rulepack: rulepack, documents };
}

function parseDiagnosticDocuments(value: unknown): DiagnosticDocument[] {
  assert.ok(value !== null && typeof value === "object" && !Array.isArray(value), "review diagnostics must be an object");
  const rawDocuments = (value as Record<string, unknown>).documents;
  assert.ok(Array.isArray(rawDocuments), "review diagnostics omitted document rows");
  return rawDocuments.map((raw, index) => {
    assert.ok(raw !== null && typeof raw === "object" && !Array.isArray(raw), `review diagnostic document ${index} must be an object`);
    const record = raw as Record<string, unknown>;
    assert.equal(typeof record.id, "string", `review diagnostic document ${index} omitted ID`);
    assert.equal(typeof record.digest, "string", `review diagnostic document ${index} omitted original digest`);
    const parsed = record.parsed_document;
    if (parsed === null || parsed === undefined) return { id: record.id as string, digest: record.digest as string, parsed_document: null };
    assert.ok(typeof parsed === "object" && !Array.isArray(parsed), `review diagnostic document ${index} parse must be an object`);
    const parseRecord = parsed as Record<string, unknown>;
    assert.equal(typeof parseRecord.id, "string", `review diagnostic document ${index} omitted parse ID`);
    return {
      id: record.id as string,
      digest: record.digest as string,
      parsed_document: {
        id: parseRecord.id as string,
        original_digest: typeof parseRecord.original_digest === "string" ? parseRecord.original_digest : undefined,
        artifact_digest: typeof parseRecord.artifact_digest === "string" ? parseRecord.artifact_digest : undefined,
        normalized_digest: typeof parseRecord.normalized_digest === "string" ? parseRecord.normalized_digest : null,
      },
    };
  });
}

function diagnosticDocument(diagnostics: ReviewDiagnostics, documentID: string) {
  const document = diagnostics.documents.find((item) => item.id === documentID);
  assert.ok(document, `review diagnostics omitted document ${documentID}`);
  assert.match(document.digest, /^[0-9a-f]{64}$/, `review diagnostics omitted the original byte hash for ${documentID}`);
  const parsed = document.parsed_document;
  assert.ok(parsed?.id, `review diagnostics omitted the frozen parse identity for ${documentID}`);
  assert.match(parsed.artifact_digest ?? "", /^[0-9a-f]{64}$/, `review diagnostics omitted the parse artifact hash for ${documentID}`);
  assert.match(parsed.normalized_digest ?? "", /^[0-9a-f]{64}$/, `review diagnostics omitted the normalized text hash for ${documentID}`);
  return {
    id: document.id,
    digest: document.digest,
    parsed_document_id: parsed.id,
    artifact_digest: parsed.artifact_digest,
    normalized_digest: parsed.normalized_digest,
    parsed_original_digest: parsed.original_digest ?? null,
  };
}

function getAttemptID(workspace: ReviewWorkspace, routeAttempt: string | null): string {
  const attemptID = routeAttempt || workspace.review_attempts?.attempt?.id || "";
  assert.ok(attemptID, `review ${workspace.run.id} has no attempt identity`);
  return attemptID;
}

function assertWorkspaceRulepack(
  actual: { id: string; version: string } | null | undefined,
  expected: RulePackIdentity,
  stage: string,
): void {
  assert.ok(actual, `${stage} did not resolve a governed rule pack`);
  assert.equal(actual.id, expected.id, `${stage} resolved a different governed rule pack ID`);
  assert.equal(actual.version, expected.version, `${stage} resolved a different governed rule pack version`);
}

function assertFrozenRulepack(diagnostics: ReviewDiagnostics, expected: RulePackIdentity | FrozenRulepack, stage: string): void {
  const actual = diagnostics.governed_rulepack;
  assert.ok(actual, `${stage} diagnostics omitted the frozen governed rule pack`);
  assert.equal(actual.id, expected.id, `${stage} froze a different governed rule pack ID`);
  assert.equal(actual.version, expected.version, `${stage} froze a different governed rule pack version`);
  assert.equal(actual.content_sha256, expected.content_sha256, `${stage} froze a different governed rule pack hash`);
}

function workspaceEvaluations(workspace: ReviewWorkspace): ConstraintEvaluation[] {
  assert.ok(Array.isArray(workspace.evaluations), `review ${workspace.run.id} omitted evaluations`);
  return workspace.evaluations;
}

function assertRelations(workspace: ReviewWorkspace, expected: Record<string, string>): void {
  for (const [ruleID, relation] of Object.entries(expected)) {
    const observed = evaluation(workspace, ruleID).relation;
    assert.equal(observed, relation, `${ruleID} relation`);
  }
}

function evaluation(workspace: ReviewWorkspace, ruleID: string): ConstraintEvaluation {
  const result = workspaceEvaluations(workspace).find((item) => item.rule_id === ruleID);
  assert.ok(result, `evaluation ${ruleID} is missing: ${JSON.stringify(workspaceEvaluations(workspace))}`);
  return result;
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

async function waitForReport(page: Page, caseID: string, runID: string, attemptID: string): Promise<{ id: string; version: number; created_at: string }> {
  const detail = await waitForCase(page, caseID, (value) => value.reports.some((item) => item.run_id === runID && item.attempt_id === attemptID));
  const report = detail.reports.find((item) => item.run_id === runID && item.attempt_id === attemptID);
  assert.ok(report, `report for ${runID}/${attemptID} is absent`);
  return report;
}

async function readReportLinks(page: Page, reportID: string): Promise<{ json: Buffer; html: Buffer }> {
  const links = page.locator("a");
  const matches = await links.evaluateAll((elements, id) => elements
    .filter((element) => (element as HTMLAnchorElement).href.includes(`/reports/${id}`))
    .map((element) => ({ href: (element as HTMLAnchorElement).href, text: element.textContent ?? "" })), reportID);
  const jsonLink = matches.find((item) => item.text.includes("查看 JSON 报告") || item.href.includes("format=json"));
  const htmlLink = matches.find((item) => item.text.includes("查看 HTML 报告") || item.href.includes("format=html"));
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
