import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { chromium, type Browser, type Page } from "playwright";
import { chromiumExecutable } from "./chromium";
import { chooseAntOption, clickAntOptionByAttribute, selectedAntLabel } from "./c1b-select";
import { replacePrimaryMaterial, selectMaterialOption, uploadNewMaterial } from "./materials-ui";
import { parseReviewWorkspace } from "../src/api/parse";
import type { Finding, ReviewWorkspace } from "../src/api/types";
import { parseServiceObjectDetail, type ServiceObjectDetail } from "../src/features/operator-audit/service-objects-api";

const web = required("E2E_WEB").replace(/\/$/, "");
const output = required("E2E_OBJECT_HISTORY_OUT");
const updatePath = required("E2E_LEGAL_UPDATE_CONTENT");
const sourceSHA = required("E2E_SOURCE_SHA");
const draftA = "拟签合同\n电路 LINE-001：最低保障带宽 50 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。\n电路 LINE-001：预付款比例 20%，预付款以合同总额为基础，含税。\n电路 LINE-001：合同总额 100000 元，含税。";
const draftB = draftA.replace("带宽 50 Mbit/s", "带宽 80 Mbit/s");
const supplement = "补充协议\n电路 LINE-002：最低保障带宽 200 Mbit/s。\n电路 LINE-002：SLA 可用率 99.9%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。";
const procurement = "政企专线采购依据\n电路 LINE-001：最低保障带宽 100 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。\n电路 LINE-001：合同总额 100000 元，含税。";
const award = "电路 LINE-001：审批依据，预付款上限 30%，预付款以合同总额为基础，含税。";

type Material = { material_id: string; current_document_id: string; current_version: number; purpose: string };
type CaseDetail = { id: string; title: string; materials: Material[]; documents: Array<{ id: string; filename: string }> };
type FactRef = { document_id: string; parsed_document_id: string; fact: { object_ref: string; dimension: string; value: string; citations: Array<{ quote: string }> } };
type ObjectResult = {
  object_ref: string; service_object_id: string | null; object_version: number | null; association_version: number | null;
  association_run_id: string; association_attempt_id: string; mapping_id: string | null; mapping_version: number | null;
  binding_version: string | null; snapshot_set_id: string | null; snapshot_collected_at?: string | null;
  status: string; reason_code: string; fact_refs: FactRef[]; facts_sha256: string;
  checks: Array<{ relation: string; evidence: Array<{ kind: string; document_version_id?: string; parsed_version_id?: string; snapshot_id?: string; observed_at?: string }> }>;
  source: null | { run_id: string; attempt_id: string; material_id: string; material_version: number; document_id: string; parsed_document_id: string; purpose: string };
};
type Evaluation = { evaluation_id: string; source_review_id: string; source_attempt_id: string; business_at: string; evidence_cutoff_at: string; machine_status: string;
  scope: { kind: string; service_object_ids?: string[] }; coverage: { scope_kind: string; candidate_count: number; evaluated_count: number; unresolved_count: number; insufficient_count: number; status: string }; objects: ObjectResult[] };
type SnapshotSet = { snapshot_set_id: string; collected_at: string; business_at: string; observations: Array<{ snapshot_id: string; observed_at: string; external_object_id: string; data_class: string }> };
type KnowledgeVersion = { ref: { knowledge_id: string; version_id: string; content_sha256: string }; revision: number; pointer_revision: number; status: string; active: boolean };

type Step = { name: string; detail?: unknown };
const steps: Step[] = [];
const responseTrace: Array<{ method: string; path: string; status: number }> = [];
const failures: string[] = [];
let page: Page | undefined;
let browser: Browser | undefined;
let caseID = "";
let runA = "";
let attemptA = "";
let evaluationID = "";

function step(name: string, detail?: unknown): void {
  steps.push({ name, detail });
  console.log(`[object-history] ${name}${detail === undefined ? "" : ` ${JSON.stringify(detail)}`}`);
}
function required(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}
async function signIn(target: Page, username: string, password: string): Promise<void> {
  await target.goto(`${web}/login`);
  await target.getByLabel("账号", { exact: true }).fill(username);
  await target.getByLabel("密码", { exact: true }).fill(password);
  await target.getByRole("button", { name: "登录", exact: true }).click();
  await target.getByRole("heading", { name: "案件", exact: true }).waitFor();
}
async function caseDetail(target: Page): Promise<CaseDetail> {
  const response = await target.request.get(`${web}/api/cases/${caseID}`);
  assert.equal(response.status(), 200, "case detail failed");
  return await response.json() as CaseDetail;
}
async function waitParsed(target: Page, materialID: string): Promise<void> {
  await target.waitForFunction((id) => {
    const row = document.querySelector(`.material-current-set li[data-material-id="${CSS.escape(id)}"]`);
    return row?.querySelector<HTMLElement>(".parsed-document-summary")?.dataset.status === "ready";
  }, materialID, { timeout: 30000 });
}
async function waitWorkspace(target: Page, runID: string, attemptID?: string): Promise<ReviewWorkspace> {
  const deadline = Date.now() + 90000;
  let last: ReviewWorkspace | undefined;
  while (Date.now() < deadline) {
    const query = attemptID ? `?attempt_id=${encodeURIComponent(attemptID)}` : "";
    const response = await target.request.get(`${web}/api/cases/${caseID}/reviews/${runID}${query}`);
    assert.equal(response.status(), 200, "frozen workspace read failed");
    last = parseReviewWorkspace(await response.json());
    if (!["accepted", "queued", "running"].includes(last.run.state) &&
        last.review_attempts?.attempt && ["completed", "partial"].includes(last.review_attempts.attempt.state)) return last;
    if (last.run.state === "failed") throw new Error(`review failed: ${JSON.stringify(last.run)}`);
    await target.waitForTimeout(200);
  }
  throw new Error(`review did not reach a terminal attempt: ${JSON.stringify(last?.run)}`);
}
async function startReview(target: Page, button: "开始审查" | "开始复审"): Promise<{ run: string; attempt: string; workspace: ReviewWorkspace }> {
  const input = target.getByLabel("审查业务时间（北京时间）", { exact: true });
  const local = shanghaiInput(new Date()).slice(0, 16);
  await input.fill(local);
  const responsePromise = target.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/cases/${caseID}/reviews`);
  await target.getByRole("button", { name: button, exact: true }).click();
  const response = await responsePromise;
  assert.ok([201, 202].includes(response.status()), `review acceptance HTTP ${response.status()}: ${await response.text()}`);
  const posted = response.request().postDataJSON() as { business_at?: string };
  assert.equal(new Date(posted.business_at ?? "").toISOString(), new Date(`${local}:00+08:00`).toISOString(), "review business time changed at submission");
  await target.waitForURL(/\/cases\/[^/]+\/reviews\/[^/?]+/);
  const run = new URL(target.url()).pathname.split("/").at(-1) ?? "";
  assert.ok(run, "review route omitted its run ID");
  const workspace = await waitWorkspace(target, run);
  const attempt = workspace.review_attempts?.attempt?.id ?? "";
  assert.ok(attempt, "review omitted its attempt ID");
  return { run, attempt, workspace };
}
function shanghaiInput(instant: Date): string {
  return new Date(instant.getTime() + 8 * 3600_000).toISOString().slice(0, 19);
}
function microseconds(instant: string): bigint {
  const match = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?Z$/.exec(instant);
  assert.ok(match, `expected UTC instant: ${instant}`);
  const millis = Date.parse(`${match[1]}Z`);
  assert.ok(Number.isFinite(millis), `invalid UTC instant: ${instant}`);
  return BigInt(millis) * 1000n + BigInt((match[2] ?? "").padEnd(6, "0").slice(0, 6));
}
function isoMicros(value: bigint): string {
  const millis = value / 1000n;
  const micros = value % 1000n;
  return `${new Date(Number(millis)).toISOString().slice(0, -1)}${micros.toString().padStart(3, "0")}Z`;
}
async function createObject(target: Page, name: string, code: string): Promise<string> {
  await target.getByLabel("服务对象名称", { exact: true }).fill(name);
  await target.getByLabel("服务对象业务编号", { exact: true }).fill(code);
  const responsePromise = target.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/cases/${caseID}/service-objects`);
  await target.getByRole("button", { name: "登记待确认服务对象", exact: true }).click();
  const response = await responsePromise;
  assert.equal(response.status(), 201, `create ${name} failed: ${await response.text()}`);
  const created = await response.json() as { service_object_id: string; version: number; association_version: number };
  assert.ok(created.service_object_id);
  assert.equal(created.version, 1);
  assert.equal(created.association_version, 0);

  const detailResponse = await target.request.get(`${web}/api/cases/${caseID}/service-objects/${encodeURIComponent(created.service_object_id)}`);
  assert.equal(detailResponse.status(), 200, `read ${name} after create returned HTTP ${detailResponse.status()}`);
  const rawDetail: unknown = await detailResponse.json();
  let detail: ServiceObjectDetail;
  try {
    detail = parseServiceObjectDetail(rawDetail);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`service-object detail parser rejected server response: ${message}; response=${JSON.stringify(rawDetail)}`);
  }
  assert.equal(detail.identity.display_name, name);
  await target.getByRole("heading", { name: `${name} · 身份第 1 版`, exact: true }).waitFor();
  return created.service_object_id;
}
async function chooseFrozenObject(target: Page, filename: string, ref: string, expectedText: string): Promise<void> {
  const materials = target.getByLabel("服务对象冻结合同材料", { exact: true });
  const options = await materials.locator("option").allTextContents();
  const label = options.find((value) => value.startsWith(`${filename} · `));
  assert.ok(label, `frozen material ${filename} missing: ${JSON.stringify(options)}`);
  await materials.selectOption({ label });
  const candidates = target.getByLabel("冻结服务对象候选", { exact: true });
  await target.waitForFunction(({ objectRef }) => Array.from(document.querySelectorAll<HTMLSelectElement>('select[aria-label="冻结服务对象候选"] option')).some((option) => option.value === objectRef), { objectRef: ref });
  await candidates.selectOption(ref);
  const details = target.getByRole("region", { name: "所选服务对象详情" });
  await details.getByText(expectedText, { exact: false }).first().waitFor();
  await details.getByRole("checkbox", { name: "我已核对所选审查、材料版本和对象事实。", exact: true }).check();
}
async function associate(target: Page, objectID: string, filename: string, ref: string, expectedText: string): Promise<void> {
  await chooseFrozenObject(target, filename, ref, expectedText);
  const responsePromise = target.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/cases/${caseID}/service-objects/${objectID}/associations`);
  await target.getByRole("button", { name: "确认首个冻结事实关联", exact: true }).click();
  const response = await responsePromise;
  assert.equal(response.status(), 201, `association ${ref} failed: ${await response.text()}`);
  const data = await response.json() as { service_object_id: string; version: number; association_version: number };
  assert.equal(data.service_object_id, objectID);
  assert.equal(data.version, 2);
  assert.equal(data.association_version, 1);
  await target.getByText("事实关联已确认，第 1 版。", { exact: true }).waitFor();
}
async function bind(target: Page, name: string, externalID: string): Promise<{ version: string; mapping: string }> {
  const section = target.getByRole("region", { name: "对象映射" });
  await chooseAntOption(target, section.getByLabel("合同对象", { exact: true }), (label) => label.startsWith(`${name}（`));
  await chooseAntOption(target, section.getByLabel("外部对象", { exact: true }), (label) => label.includes(externalID));
  await section.getByRole("checkbox", { name: "我已人工核对上述合同对象与外部对象的对应关系", exact: true }).check();
  const responsePromise = target.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/cases/${caseID}/object-bindings`);
  await section.getByRole("button", { name: "确认对象映射", exact: true }).click();
  const response = await responsePromise;
  assert.equal(response.status(), 201, `binding ${name} failed: ${await response.text()}`);
  const binding = await response.json() as { version: string; mapping_id: string; external_object: { external_object_id: string } };
  assert.ok(binding.version && binding.mapping_id, "mapping identities absent");
  assert.equal(binding.external_object.external_object_id, externalID);
  await section.getByText(/映射已确认，版本/).waitFor();
  return { version: binding.version, mapping: binding.mapping_id };
}
async function readEvaluation(target: Page, id: string): Promise<Evaluation> {
  const response = await target.request.get(`${web}/api/cases/${caseID}/fulfillment-evaluations/${id}`);
  assert.equal(response.status(), 200, `evaluation ${id} not readable`);
  return await response.json() as Evaluation;
}
function objectFor(evaluation: Evaluation, id: string): ObjectResult {
  const rows = evaluation.objects.filter((item) => item.service_object_id === id);
  assert.equal(rows.length, 1, `expected exactly one frozen object ${id}: ${JSON.stringify(evaluation.objects)}`);
  return rows[0]!;
}
async function exactBoundary(target: Page, body: Record<string, unknown>, objectID: string, cutoff: string, key: string): Promise<ObjectResult> {
  const response = await target.request.post(`${web}/api/cases/${caseID}/fulfillment-evaluations`, {
    headers: { "Idempotency-Key": key, Origin: web }, data: { ...body, evidence_cutoff_at: cutoff, scope: { kind: "explicit_subset", service_object_ids: [objectID] } },
  });
  assert.equal(response.status(), 201, `boundary ${key} failed: ${await response.text()}`);
  const result = await response.json() as Evaluation;
  assert.equal(microseconds(result.evidence_cutoff_at), microseconds(cutoff));
  assert.equal(result.scope.kind, "explicit_subset");
  return objectFor(result, objectID);
}
async function frozenFindingView(target: Page, finding: Finding, oldDocumentID: string, expectedQuote: string, navigate = true): Promise<void> {
  if (navigate) await target.goto(`${web}/cases/${caseID}/reviews/${runA}?attempt_id=${encodeURIComponent(attemptA)}`);
  const route = new URL(target.url());
  assert.equal(route.pathname, `/cases/${caseID}/reviews/${runA}`, "historical view changed its review");
  assert.equal(route.searchParams.get("attempt_id"), attemptA, "historical view changed its attempt");
  await target.getByRole("heading", { name: "审查工作区", exact: true }).waitFor();
  const button = target.getByRole("button", { name: `选择线索：${finding.claim}`, exact: true });
  await button.click();
  await button.and(target.locator('[aria-current="true"]')).waitFor();
  await target.getByRole("button", { name: "双来源对照", exact: true }).click();
  const pane = target.locator(`.document-split .doc-pane[data-document-version-id="${oldDocumentID}"]`);
  await pane.waitFor();
  const text = await pane.locator(".doc-text").innerText();
  assert.ok(text.includes("50 Mbit/s"), "attempt A did not display its original contract value");
  assert.ok(!text.includes("80 Mbit/s"), "attempt A displayed the replacement contract value");
  const highlights = await pane.locator("mark.hit-evidence").allTextContents();
  assert.ok(highlights.some((value) => value === expectedQuote), `old contract citation was not highlighted at the frozen quote: ${JSON.stringify(highlights)}`);
}
async function frozenLegalView(target: Page, expected: Record<string, unknown>, expectedBody: string): Promise<void> {
  const section = target.getByRole("region", { name: "本次审查法律依据" });
  const responsePromise = target.waitForResponse((response) => response.request().method() === "GET" && new URL(response.url()).pathname === `/api/cases/${caseID}/runs/${runA}/attempts/${attemptA}/legal-citations/${String(expected.citation_id)}`);
  await section.getByRole("button", { name: "读取本次冻结条文", exact: true }).click();
  const response = await responsePromise;
  assert.equal(response.status(), 200, "exact frozen legal citation could not be read");
  assert.deepEqual(await response.json(), expected, "frozen legal citation changed after later material/knowledge versions");
  await section.locator("blockquote").filter({ hasText: expectedBody }).waitFor();
  await section.getByText("法律审核状态为未运行；该条文按本次审查冻结的版本显示。", { exact: true }).waitFor();
}
async function publishLaterLegalVersion(browserInstance: Browser): Promise<KnowledgeVersion> {
  const admin = await browserInstance.newContext();
  try {
    const login = await admin.request.post(`${web}/api/session`, { data: { username: "demo-admin", password: "admin-demo", audience: "admin" } });
    assert.equal(login.status(), 200, "admin session for the explicit knowledge update failed");
    const content: unknown = JSON.parse(await readFile(updatePath, "utf8"));
    const created = await admin.request.post(`${web}/api/admin/governed-knowledge`, { headers: { Origin: web }, data: { content, idempotency_key: "object-history-later-create" } });
    assert.equal(created.status(), 201, `create later legal version: ${await created.text()}`);
    let version = await created.json() as KnowledgeVersion;
    const ref = version.ref;
    assert.ok(ref.knowledge_id && ref.version_id && ref.content_sha256);
    for (const action of ["submit", "validate", "approve", "publish"]) {
      const response = await admin.request.post(`${web}/api/admin/governed-knowledge/${encodeURIComponent(ref.knowledge_id)}/versions/${encodeURIComponent(ref.version_id)}/commands`, {
        headers: { Origin: web },
        data: { action, expected_revision: version.revision, expected_content_sha256: ref.content_sha256, expected_pointer_revision: version.pointer_revision,
          reason: "浏览器旅程中的后续知识版本", cross_case_reviewed: true, idempotency_key: `object-history-later-${action}` },
      });
      assert.equal(response.status(), 200, `later legal ${action}: ${await response.text()}`);
      version = await response.json() as KnowledgeVersion;
    }
    assert.equal(version.status, "published");
    assert.equal(version.active, true);
    return version;
  } finally {
    await admin.close();
  }
}

async function run(): Promise<void> {
  browser = await chromium.launch({ executablePath: chromiumExecutable(), headless: true, args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, timezoneId: "Asia/Shanghai" });
  page = await context.newPage();
  const current = page;
  current.setDefaultTimeout(15000);
  current.on("response", (response) => {
    const pathname = new URL(response.url()).pathname;
    if (pathname.startsWith("/api/")) responseTrace.push({ method: response.request().method(), path: pathname, status: response.status() });
  });
  await signIn(current, "demo-admin", "admin-demo");
  await current.getByRole("button", { name: "新建案件", exact: true }).click();
  const dialog = current.getByRole("dialog");
  const title = `多对象冻结历史 · ${Date.now()}`;
  await dialog.getByLabel("标题", { exact: true }).fill(title);
  await selectMaterialOption(current, "合同类型", "服务合同");
  await dialog.getByRole("button", { name: "创建", exact: true }).click();
  await current.waitForURL(/\/cases\/[^/]+$/);
  caseID = new URL(current.url()).pathname.split("/").at(-1) ?? "";
  assert.ok(caseID);
  const member = await current.request.post(`${web}/api/cases/${caseID}/members`, { headers: { Origin: web }, data: { actor_id: "demo-reviewer" } });
  assert.equal(member.status(), 200, "independent reviewer could not be added to this synthetic case");
  const primary = await uploadNewMaterial(current, { role: "draft_contract", filename: "合同-A.txt", text: draftA });
  const purchase = await uploadNewMaterial(current, { role: "procurement", filename: "采购依据.txt", text: procurement });
  const approval = await uploadNewMaterial(current, { role: "award", filename: "审批依据.txt", text: award });
  const annex = await uploadNewMaterial(current, { role: "attachment", purpose: "supplement", filename: "补充协议-A.txt", text: supplement });
  for (const material of [primary, purchase, approval, annex]) await waitParsed(current, material.material_id);
  const first = await startReview(current, "开始审查");
  runA = first.run;
  attemptA = first.attempt;
  const frozenPrimary = first.workspace.documents.find((document) => document.id === primary.id);
  const frozenAnnex = first.workspace.documents.find((document) => document.id === annex.id);
  assert.ok(frozenPrimary?.parsed_document_id && frozenAnnex?.parsed_document_id, "attempt A did not freeze both exact parses");
  assert.ok(frozenPrimary.text?.includes("50 Mbit/s") && frozenAnnex.text?.includes("200 Mbit/s"));
  const finding = first.workspace.findings.find((item) => item.evidence.some((anchor) => anchor.document_version_id === primary.id && anchor.quote.includes("50 Mbit/s")) && item.evidence.some((anchor) => anchor.document_version_id === purchase.id));
  assert.ok(finding, "attempt A lacks a two-material finding quoting the original contract");
  const anchor = finding.evidence.find((item) => item.document_version_id === primary.id && item.quote.includes("50 Mbit/s"));
  assert.ok(anchor?.parsed_document_id && anchor.status === "located", "original citation is not located in a frozen parse");
  assert.equal(anchor.parsed_document_id, frozenPrimary.parsed_document_id);
  const legalProjection = first.workspace.legal_authorities;
  assert.ok(legalProjection && legalProjection.run_id === runA && legalProjection.attempt_id === attemptA, "attempt A has no exact legal projection");
  const authority = legalProjection.authorities.find((item) => item.article !== undefined);
  assert.ok(authority?.article && authority.legal_review_status === "NOT_RUN", "verified legal fixture did not produce a frozen citation");
  const exactLegalURL = `${web}/api/cases/${caseID}/runs/${runA}/attempts/${attemptA}/legal-citations/${authority.citation_id}`;
  const exactLegalResponse = await current.request.get(exactLegalURL);
  assert.equal(exactLegalResponse.status(), 200);
  const frozenLegal = await exactLegalResponse.json() as Record<string, unknown>;
  const frozenArticle = frozenLegal.article as { canonical_full_article_body: string; ref: { version_id: string } };
  assert.ok(frozenArticle.canonical_full_article_body && frozenArticle.ref.version_id);
  await frozenFindingView(current, finding, primary.id, anchor.quote);
  await frozenLegalView(current, frozenLegal, frozenArticle.canonical_full_article_body);
  step("attempt A freezes two contract materials and exact legal authority", { case_id: caseID, run_id: runA, attempt_id: attemptA });

  await current.goto(`${web}/cases/${caseID}/operator-audit?run_id=${encodeURIComponent(runA)}&attempt_id=${encodeURIComponent(attemptA)}`);
  await current.getByRole("heading", { name: "服务对象与冻结事实", exact: true }).waitFor();
  const objectA = await createObject(current, "合成专线甲", "LINE-001");
  await associate(current, objectA, "合同-A.txt", "LINE-001", "50 Mbit/s");
  const objectB = await createObject(current, "合成专线乙", "LINE-002");
  assert.notEqual(objectA, objectB);
  await associate(current, objectB, "补充协议-A.txt", "LINE-002", "200 Mbit/s");
  const bindingA = await bind(current, "合成专线甲", "fixture-line-001");
  const bindingB = await bind(current, "合成专线乙", "fixture-line-002");
  assert.notEqual(bindingA.mapping, bindingB.mapping);
  await chooseAntOption(current, current.getByLabel("选择对象映射", { exact: true }), (label) => label.includes("fixture-line-001"));
  const businessLocal = shanghaiInput(new Date(Date.now() + 120_000));
  await current.getByLabel("业务时间（北京时间）", { exact: true }).fill(businessLocal);
  const businessAt = new Date(`${businessLocal}+08:00`).toISOString();
  const collectPromise = current.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/cases/${caseID}/external-collection-jobs`);
  await current.getByRole("button", { name: "采集一次外部证据", exact: true }).click();
  const collectedResponse = await collectPromise;
  assert.equal(collectedResponse.status(), 201, `synthetic collection failed: ${await collectedResponse.text()}`);
  const collection = await collectedResponse.json() as { snapshot_set: SnapshotSet };
  const snapshot = collection.snapshot_set;
  assert.equal(new Date(snapshot.business_at).toISOString(), businessAt);
  assert.equal(snapshot.observations.length, 1);
  const observation = snapshot.observations[0]!;
  assert.equal(observation.external_object_id, "fixture-line-001");
  assert.equal(observation.data_class, "synthetic");
  assert.ok(microseconds(observation.observed_at) < microseconds(snapshot.collected_at), "observation and collection times were not recorded separately");
  await current.waitForFunction((collected) => Date.now() > collected, Date.parse(snapshot.collected_at));
  const observedValue = current.getByText("观测时间（observed_at）", { exact: true }).locator("xpath=following-sibling::dd");
  await observedValue.waitFor();
  assert.notEqual((await observedValue.innerText()).trim(), "时间未报告", "the browser hid the evidence observation time");
  await chooseAntOption(current, current.getByLabel("选择能力包", { exact: true }), (label) => label === "专线配置核验（合成测试清单） @ 1.0.0");
  assert.equal(await selectedAntLabel(current.getByLabel("核验范围", { exact: true })), "整案全部对象", "default scope was not whole case");

  const lost = Promise.withResolvers<{ key: string; body: unknown; response: Evaluation }>();
  let intercepted = false;
  const routePattern = `**/api/cases/${caseID}/fulfillment-evaluations`;
  await current.route(routePattern, async (route) => {
    const request = route.request();
    if (request.method() !== "POST" || intercepted) { await route.continue(); return; }
    intercepted = true;
    try {
      const forwarded = await route.fetch();
      assert.equal(forwarded.status(), 201, `server did not commit lost-response request: ${await forwarded.text()}`);
      const committed = { key: request.headers()["idempotency-key"] ?? "", body: request.postDataJSON(), response: await forwarded.json() as Evaluation };
      await route.abort("failed");
      lost.resolve(committed);
    } catch (error) {
      lost.reject(error);
      await route.abort("failed").catch(() => {});
    }
  });
  await current.getByRole("button", { name: "受理确定性核验", exact: true }).click();
  const lostRequest = await lost.promise;
  assert.ok(lostRequest.key && lostRequest.response.evaluation_id, "the intercepted request did not commit an identifiable evaluation");
  await current.unroute(routePattern);
  const replayPromise = current.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/cases/${caseID}/fulfillment-evaluations`);
  await current.getByRole("button", { name: "受理确定性核验", exact: true }).click();
  const replay = await replayPromise;
  assert.equal(replay.status(), 201, `lost-response replay failed: ${await replay.text()}`);
  assert.equal(replay.request().headers()["idempotency-key"], lostRequest.key, "retry changed its idempotency key");
  assert.deepEqual(replay.request().postDataJSON(), lostRequest.body, "retry changed its scope or temporal intent");
  const evaluation = await replay.json() as Evaluation;
  assert.deepEqual(evaluation, lostRequest.response, "replay did not return the committed immutable result");
  evaluationID = evaluation.evaluation_id;
  await current.waitForURL((url) => url.searchParams.get("evaluation_id") === evaluationID);
  assert.deepEqual(evaluation.scope, { kind: "whole_case" });
  assert.equal(evaluation.coverage.status, "partial");
  assert.equal(evaluation.machine_status, "partial");
  assert.ok(evaluation.coverage.candidate_count >= 2 && evaluation.coverage.unresolved_count + evaluation.coverage.insufficient_count >= 1);
  const a = objectFor(evaluation, objectA);
  const b = objectFor(evaluation, objectB);
  assert.equal(a.object_ref, "LINE-001");
  assert.equal(b.object_ref, "LINE-002");
  assert.equal(a.source?.document_id, primary.id);
  assert.equal(a.source?.parsed_document_id, frozenPrimary.parsed_document_id);
  assert.equal(b.source?.document_id, annex.id);
  assert.equal(b.source?.parsed_document_id, frozenAnnex.parsed_document_id);
  for (const row of [a, b]) {
    assert.equal(row.source?.run_id, runA);
    assert.equal(row.source?.attempt_id, attemptA);
    assert.equal(row.association_run_id, runA);
    assert.equal(row.association_attempt_id, attemptA);
    assert.equal(row.association_version, 1);
    assert.ok(row.fact_refs.length > 0 && row.fact_refs.every((ref) => ref.document_id === row.source?.document_id && ref.parsed_document_id === row.source?.parsed_document_id && ref.fact.object_ref === row.object_ref));
  }
  assert.equal(a.mapping_id, bindingA.mapping);
  assert.equal(b.mapping_id, bindingB.mapping);
  assert.equal(a.snapshot_set_id, snapshot.snapshot_set_id);
  assert.equal(b.reason_code, "external_snapshot_missing");
  assert.equal(b.status, "insufficient");
  assert.equal(a.status, "evaluated");
  assert.ok(a.checks.some((check) => check.evidence.some((item) => item.kind === "external_snapshot" && item.snapshot_id === observation.snapshot_id && item.observed_at === observation.observed_at)), "evaluated object omitted its external evidence identity or observation time");
  assert.ok(a.checks.some((check) => check.evidence.some((item) => item.kind === "document" && item.document_version_id === primary.id && item.parsed_version_id === frozenPrimary.parsed_document_id)), "evaluated object omitted its frozen contract evidence");
  const coverage = current.getByRole("region", { name: "逐对象核验结果" });
  await coverage.getByText(/覆盖不完整/).waitFor();
  await coverage.getByText(/证据截止前没有该对象的快照/).waitFor();
  await coverage.getByText(/合成专线甲 · 已核验/).waitFor();
  await coverage.getByText(/合成专线乙 · 证据不足/).waitFor();
  const times = await coverage.getByText(/业务时间 .*证据截止时间/).innerText();
  assert.ok(!times.includes("未报告"), `the browser did not display both temporal inputs: ${times}`);
  const collectedLabels = await coverage.getByText(/证据取得时间/).allTextContents();
  assert.ok(collectedLabels.some((label) => !label.includes("未报告")), "the browser hid the snapshot acquisition time");
  await current.reload();
  await current.getByRole("region", { name: "逐对象核验结果" }).getByText(/覆盖不完整/).waitFor();
  assert.deepEqual(await readEvaluation(current, evaluationID), evaluation, "refresh changed the frozen aggregate");
  step("whole-case result retains both per-object sources, mappings and explicit missing evidence", { evaluation_id: evaluationID });

  await chooseAntOption(current, current.getByLabel("选择能力包", { exact: true }), (label) => label === "专线配置核验（合成测试清单） @ 1.0.0");
  await current.getByLabel("业务时间（北京时间）", { exact: true }).fill(businessLocal);
  await chooseAntOption(current, current.getByLabel("核验范围", { exact: true }), (label) => label === "明确选择对象");
  const subsetSelector = current.getByLabel("选择核验对象", { exact: true });
  await clickAntOptionByAttribute(current, subsetSelector, "title", `合成专线甲 · ${objectA}`);
  const subsetResponse = current.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/cases/${caseID}/fulfillment-evaluations`);
  await current.getByRole("button", { name: "受理确定性核验", exact: true }).click();
  const subset = await subsetResponse;
  assert.equal(subset.status(), 201, `explicit subset failed: ${await subset.text()}`);
  const subsetRequest: unknown = subset.request().postDataJSON();
  assert.ok(subsetRequest && typeof subsetRequest === "object" && "scope" in subsetRequest, "subset request omitted its scope");
  assert.deepEqual(subsetRequest.scope, { kind: "explicit_subset", service_object_ids: [objectA] });
  const subsetResult = await subset.json() as Evaluation;
  assert.deepEqual(subsetResult.scope, { kind: "explicit_subset", service_object_ids: [objectA] });
  assert.equal(subsetResult.coverage.candidate_count, 1);
  assert.equal(objectFor(subsetResult, objectA).snapshot_set_id, snapshot.snapshot_set_id);
  assert.equal(subsetResult.objects.some((row) => row.service_object_id === objectB), false, "explicit subset silently included the other object");
  await current.getByRole("region", { name: "逐对象核验结果" }).getByText(/范围：明确选择的对象/).waitFor();
  step("explicit subset was selected and submitted in the browser", { object_id: objectA, evaluation_id: subsetResult.evaluation_id });

  const originalBody = lostRequest.body as Record<string, unknown>;
  const cutoffBeforeObservation = isoMicros(microseconds(observation.observed_at) - 1n);
  const cutoffBeforeCollection = isoMicros(microseconds(snapshot.collected_at) - 1n);
  const beforeObservation = await exactBoundary(current, originalBody, objectA, cutoffBeforeObservation, "object-history-before-observation");
  const beforeCollection = await exactBoundary(current, originalBody, objectA, cutoffBeforeCollection, "object-history-before-collection");
  const atCollection = await exactBoundary(current, originalBody, objectA, snapshot.collected_at, "object-history-at-collection");
  assert.equal(beforeObservation.reason_code, "external_snapshot_missing", "observation acquired after cutoff was used");
  assert.equal(beforeCollection.reason_code, "external_snapshot_missing", "snapshot acquired after cutoff was used");
  assert.equal(atCollection.snapshot_set_id, snapshot.snapshot_set_id, "inclusive cutoff did not include the exact collected snapshot");
  assert.equal(atCollection.status, "evaluated");
  const changedBusiness = await exactBoundary(current, { ...originalBody, business_at: new Date(Date.parse(businessAt) + 1000).toISOString() }, objectA, evaluation.evidence_cutoff_at, "object-history-other-business-time");
  assert.equal(changedBusiness.reason_code, "external_snapshot_missing", "snapshot from a different business_at was silently reused");
  step("business time, observation time and inclusive acquisition cutoff have separate boundaries", { business_at: businessAt, observed_at: observation.observed_at, collected_at: snapshot.collected_at });

  await current.goto(`${web}/cases/${caseID}`);
  const detail = await caseDetail(current);
  const currentPrimary = detail.materials.find((material) => material.material_id === primary.material_id);
  assert.ok(currentPrimary && currentPrimary.current_document_id === primary.id && currentPrimary.current_version === 1);
  const replaced = await replacePrimaryMaterial(current, { role: "draft_contract", filename: "合同-B.txt", text: draftB }, currentPrimary, "合同-A.txt");
  await waitParsed(current, replaced.material_id);
  const second = await startReview(current, "开始复审");
  assert.notEqual(second.attempt, attemptA);
  assert.notEqual(second.run, runA);
  await current.goto(`${web}/cases/${caseID}/operator-audit?run_id=${encodeURIComponent(second.run)}&attempt_id=${encodeURIComponent(second.attempt)}&service_object_id=${encodeURIComponent(objectA)}`);
  await current.getByRole("heading", { name: "服务对象与冻结事实", exact: true }).waitFor();
  await chooseFrozenObject(current, "合同-B.txt", "LINE-001", "80 Mbit/s");
  await current.getByRole("button", { name: "预览关联更正", exact: true }).click();
  await current.getByRole("heading", { name: "核对关联更正预览", exact: true }).waitFor();
  await current.getByRole("button", { name: "提交异人审批", exact: true }).click();
  await current.waitForURL((url) => Boolean(url.searchParams.get("mapping_change")));
  const approvalURL = current.url();
  const approvalSection = current.getByRole("region", { name: "合同关联更正审批" });
  await approvalSection.getByText(/状态 pending_decision/).waitFor();
  await approvalSection.getByLabel("决定理由", { exact: true }).fill("申请人不得自批，需另一名审查人复核。 ");
  const selfDecision = current.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname.endsWith("/decision"));
  await approvalSection.getByRole("button", { name: "批准关联更正", exact: true }).click();
  assert.equal((await selfDecision).status(), 403, "proposer was able to approve their own mapping reassociation");
  const reviewerContext = await browser.newContext({ viewport: { width: 1440, height: 1000 }, timezoneId: "Asia/Shanghai" });
  try {
    const reviewer = await reviewerContext.newPage();
    await signIn(reviewer, "demo-reviewer", "reviewer-demo");
    await reviewer.goto(approvalURL);
    const section = reviewer.getByRole("region", { name: "合同关联更正审批" });
    await section.getByText(/状态 pending_decision/).waitFor();
    await section.getByLabel("决定理由", { exact: true }).fill("已核对新合同的固定解析、对象事实和映射版本。 ");
    const accepted = reviewer.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname.endsWith("/decision"));
    await section.getByRole("button", { name: "批准关联更正", exact: true }).click();
    const acceptedResponse = await accepted;
    assert.equal(acceptedResponse.status(), 201, `independent reviewer could not approve reassociation: ${await acceptedResponse.text()}`);
    await section.getByText("审批决定已记录。", { exact: true }).waitFor();
  } finally {
    await reviewerContext.close();
  }
  const objectResponse = await current.request.get(`${web}/api/cases/${caseID}/service-objects/${objectA}`);
  assert.equal(objectResponse.status(), 200);
  const changed = await objectResponse.json() as { association: { association_version: number; source_run_id: string; source_attempt_id: string; document_id: string }; mapping: { mapping_id: string; mapping_version: number; association_version: number } };
  assert.equal(changed.association.association_version, 2);
  assert.equal(changed.association.source_run_id, second.run);
  assert.equal(changed.association.source_attempt_id, second.attempt);
  assert.equal(changed.association.document_id, replaced.id);
  assert.equal(changed.mapping.mapping_id, bindingA.mapping);
  assert.equal(changed.mapping.mapping_version, 2);
  assert.equal(changed.mapping.association_version, 2);
  const historyResponse = await current.request.get(`${web}/api/cases/${caseID}/service-objects/${objectA}/associations`);
  assert.equal(historyResponse.status(), 200);
  const history = await historyResponse.json() as { items: Array<{ association_version: number; source_run_id: string; source_attempt_id: string; document_id: string }> };
  assert.ok(history.items.some((item) => item.association_version === 1 && item.source_run_id === runA && item.source_attempt_id === attemptA && item.document_id === primary.id), "original association disappeared from history");
  assert.ok(history.items.some((item) => item.association_version === 2 && item.source_run_id === second.run && item.source_attempt_id === second.attempt && item.document_id === replaced.id), "approved association was not appended");
  await current.goto(approvalURL);
  await current.getByText("冻结事实关联历史", { exact: true }).click();
  await current.getByText(new RegExp(`关联第 1 版.*${runA}/${attemptA}`)).waitFor();
  await current.getByText(new RegExp(`关联第 2 版.*${second.run}/${second.attempt}`)).waitFor();
  assert.deepEqual(await readEvaluation(current, evaluationID), evaluation, "approved reassociation rewrote the earlier aggregate");
  const laterLegal = await publishLaterLegalVersion(browser);
  assert.notEqual(laterLegal.ref.version_id, frozenArticle.ref.version_id, "knowledge update did not create a new immutable version");
  await frozenFindingView(current, finding, primary.id, anchor.quote);
  await frozenLegalView(current, frozenLegal, frozenArticle.canonical_full_article_body);
  await current.reload();
  await frozenFindingView(current, finding, primary.id, anchor.quote, false);
  await frozenLegalView(current, frozenLegal, frozenArticle.canonical_full_article_body);
  const retainedReviewURL = `${web}/cases/${caseID}/reviews/${runA}?attempt_id=${encodeURIComponent(attemptA)}`;
  await current.getByRole("button", { name: "退出", exact: true }).click();
  await current.getByRole("heading", { name: "登录合同审查", exact: true }).waitFor();
  await signIn(current, "demo-admin", "admin-demo");
  await current.goto(retainedReviewURL);
  await frozenFindingView(current, finding, primary.id, anchor.quote, false);
  await frozenLegalView(current, frozenLegal, frozenArticle.canonical_full_article_body);
  await current.goto(`${web}/cases/${caseID}/legal-knowledge`);
  await current.getByRole("heading", { name: "公司制度资料", exact: true }).waitFor();
  await current.getByText("资料待提供；当前不可引用，也未满足必需依赖。", { exact: true }).first().waitFor();
  const sourcesResponse = await current.request.get(`${web}/api/cases/${caseID}/company-policy-sources`);
  assert.equal(sourcesResponse.status(), 200);
  const sources = await sourcesResponse.json() as { items: Array<{ state: string; can_be_cited: boolean; has_body: boolean; has_published_version: boolean; has_content_hash: boolean; knowledge_ref: unknown; satisfies_mandatory_dependencies: boolean }> };
  const pending = sources.items.filter((item) => item.state === "awaiting_source");
  assert.ok(pending.length > 0, "company-policy fixture did not expose any awaiting source");
  for (const item of pending) assert.deepEqual([item.can_be_cited, item.has_body, item.has_published_version, item.has_content_hash, item.knowledge_ref, item.satisfies_mandatory_dependencies], [false, false, false, false, null, false]);
  await current.goto(`${web}/cases/${caseID}/operator-audit?run_id=${encodeURIComponent(runA)}&attempt_id=${encodeURIComponent(attemptA)}&evaluation_id=${encodeURIComponent(evaluationID)}`);
  await current.getByRole("region", { name: "逐对象核验结果" }).getByText(/覆盖不完整/).waitFor();
  assert.deepEqual(await readEvaluation(current, evaluationID), evaluation, "logout/login changed the frozen aggregate");
  step("independent reassociation, legal version change, refresh and re-login preserve attempt A", { run_b: second.run, attempt_b: second.attempt, later_legal_version: laterLegal.ref.version_id });
  await writeFile(join(output, "journey-result.json"), `${JSON.stringify({ status: "PASS", source_sha: sourceSHA, case_id: caseID, run_a: runA, attempt_a: attemptA, evaluation_id: evaluationID, object_ids: [objectA, objectB], later_legal_version: laterLegal.ref.version_id, steps, response_trace: responseTrace }, null, 2)}\n`);
  await context.close();
}

try {
  await run();
} catch (error) {
  failures.push(error instanceof Error ? error.stack ?? error.message : String(error));
  if (page) {
    try {
      await page.screenshot({ path: join(output, "failure.png"), fullPage: true });
      await writeFile(join(output, "failure-dom.txt"), await page.content());
    } catch (captureError) {
      failures.push(`failure capture: ${String(captureError)}`);
    }
  }
  console.error(failures.join("\n"));
  process.exitCode = 1;
} finally {
  await writeFile(join(output, "journey-trace.json"), `${JSON.stringify({ source_sha: sourceSHA, case_id: caseID, run_a: runA, attempt_a: attemptA, evaluation_id: evaluationID, steps, response_trace: responseTrace, failures }, null, 2)}\n`);
  await browser?.close();
}
