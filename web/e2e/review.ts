import { request as httpRequest } from "node:http";
import { chromiumExecutable } from "./chromium";
import { readCDPRequestPostData } from "./cdp-request-post-data";
import { C1B_BUSINESS_AT_UTC, C1B_BUSINESS_TIME_ZONE, c1bBusinessAtInput } from "./c1b-clock";
// Drives the installed Chromium against the Vite dev server.
// The Go test starts the API and Vite, then sets the environment below.
const web = required("E2E_WEB");
const procurement = required("E2E_PROCUREMENT");
const draft = required("E2E_DRAFT");
const revised = required("E2E_REVISED");
const cdpPort = required("E2E_CDP_PORT");
const profile = required("E2E_CHROME_PROFILE");
const parsingScreenshot = process.env.E2E_PARSING_SCREENSHOT;
const failureScreenshot = process.env.E2E_FAILURE_SCREENSHOT;
const failureDOM = process.env.E2E_FAILURE_DOM;

async function run(): Promise<void> {
const chrome = Bun.spawn(
  [
    chromiumExecutable(),
    "--headless=new",
    "--disable-gpu",
    "--no-sandbox",
    "--disable-dev-shm-usage",
    `--remote-debugging-port=${cdpPort}`,
    `--user-data-dir=${profile}`,
    "--no-first-run",
    "about:blank",
  ],
  { stdout: "ignore", stderr: "inherit" },
);
let chromeStop: Promise<void> | undefined;
const stopChrome = (): Promise<void> => {
  if (chromeStop) return chromeStop;
  chromeStop = (async () => {
    chrome.kill("SIGTERM");
    const code = await Promise.race([chrome.exited, Bun.sleep(2500).then(() => null)]);
    if (code === null) {
      chrome.kill("SIGKILL");
      const finalCode = await Promise.race([chrome.exited, Bun.sleep(1500).then(() => null)]);
      console.error(finalCode === null
        ? "[browser] chromium exitCode unavailable 1500ms after SIGKILL"
        : `[browser] chromium exitCode after SIGKILL=${finalCode}`);
      return;
    }
    console.log(`[browser] chromium exitCode=${code}`);
  })();
  return chromeStop;
};
process.once("SIGTERM", () => {
  void stopChrome().finally(() => process.exit(143));
});

let failed = false;
let page: Page | undefined;
let pageSession: string | undefined;
try {
  const version = await waitJSON(`http://127.0.0.1:${cdpPort}/json/version`);
  const browser = version.webSocketDebuggerUrl as string;
  if (!browser) throw new Error("chromium did not expose a debugger socket");
  page = await new Page(browser);
  step("Chromium debugger websocket connected");
  await page.send("Target.setDiscoverTargets", { discover: true });
  const created = (await page.send("Target.createTarget", { url: web })) as { targetId: string };
  const attached = (await page.send("Target.attachToTarget", { targetId: created.targetId, flatten: true })) as {
    sessionId: string;
  };
  const session = attached.sessionId;
  pageSession = session;
  await page.send("Page.enable", {}, session);
  await page.send("Runtime.enable", {}, session);
  await page.send("DOM.enable", {}, session);
  await page.send("Network.enable", { maxPostDataSize: 1024 * 1024 }, session);
  await page.send("Emulation.setTimezoneOverride", { timezoneId: C1B_BUSINESS_TIME_ZONE }, session);
  if (process.env.E2E_DOCS_SCREENSHOT || process.env.E2E_EXECUTION_SCREENSHOT || parsingScreenshot) {
    await page.send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false }, session);
  }
  await page.send("Page.navigate", { url: `${web}/login` }, session);
  await page.wait(session, "document.querySelector('input[name=username]')");

  await fill(page, session, "input[name=username]", "demo-admin");
  await fill(page, session, "input[name=password]", "admin-demo");
  await clickText(page, session, "button", "登录");
  await page.wait(session, "document.body.innerText.includes('新建案件')");
  step("authenticated demo-admin");

  const seededCase = process.env.E2E_CASE_ID;
  if (seededCase) {
    await page.send("Page.navigate", { url: `${web}/cases/${seededCase}` }, session);
    await page.wait(session, "document.body.innerText.includes('采购质保核对') && document.querySelector('input[type=file]')");
  } else {
    await clickText(page, session, "button", "新建案件");
    await page.wait(session, "document.querySelector('.ant-modal input')");
    await fill(page, session, ".ant-modal input", "采购质保核对");
    await clickText(page, session, "button", "创建");
    await page.wait(session, "document.body.innerText.includes('采购质保核对') && document.querySelector('input[type=file]')");
  }

  await upload(page, session, procurement);
  await clickText(page, session, "button", "新增材料");
  await waitParsedDocument(page, session, "purchase.txt");
  step("procurement source uploaded and parsed");

  await selectAntOption(page, session, "材料角色", "拟签合同");
  await upload(page, session, draft);
  await clickText(page, session, "button", "新增材料");
  await waitParsedDocument(page, session, "draft.txt");
  step("primary contract uploaded and parsed");

  const initialProcurementParse = await readDocumentMetadata(page, session, "purchase.txt");
  const initialDraftParse = await readDocumentMetadata(page, session, "draft.txt");
  assertReadyParseMetadata(initialProcurementParse, "purchase.txt");
  assertReadyParseMetadata(initialDraftParse, "draft.txt");

  await fillReviewBusinessTime(page, session);
  await clickText(page, session, "button", "开始审查");
  await loadDiagnostics(page, session);
  step("initial warranty review submitted");
  await page.wait(session, `[...document.querySelectorAll('button')].some((item) => item.textContent.trim() === '重新执行未完成的审查' && !item.disabled)`);
  await clickText(page, session, "button", "重新执行未完成的审查");
  await page.wait(session, `(() => {
    const row = document.querySelector('.execution-attempt-row');
    return row?.getAttribute('data-attempt-count') === '2' &&
      row.getAttribute('data-attempt-ordinal') === '2' &&
      document.querySelector('.execution-inspector-summary p')?.innerText.includes('第 2 次执行');
  })()`);
  step("synthetic retry completed; attempt 2 selected");
  await selectAttemptNumber(page, session, 1);
  await page.wait(session, "document.querySelector('.execution-inspector-summary p')?.innerText.includes('第 1 次执行')");
  const firstAttemptInputs = await readWorkspaceParseMetadata(page, session);
  const firstAttemptParsedIds = await readSelectedAttemptParseIds(page, session);
  assertAttemptParseRefsMatchDisplayed(firstAttemptParsedIds, await readWorkspaceSourceTabs(page, session), "attempt 1");
  await selectAttemptNumber(page, session, 2);
  await page.wait(session, "document.querySelector('.execution-inspector-summary p')?.innerText.includes('第 2 次执行')");
  await page.wait(
    session,
    "(() => { const text = document.body.innerText.replace(/\\s/g, ''); return text.includes('质保月数不一致') && text.includes('36个月') && text.includes('24个月'); })()",
  );
  const selectedFinding = await page.eval(session, `(() => {
    const button = [...document.querySelectorAll('.finding-button')].find((item) => item.textContent?.includes('质保月数不一致'));
    if (!(button instanceof HTMLButtonElement)) return 'missing warranty finding button';
    button.click();
    return 'selected';
  })()`);
  if (selectedFinding !== "selected") throw new Error(selectedFinding);
  await page.wait(session, "document.querySelector('.finding-detail h3')?.textContent.includes('质保月数不一致')");
  const retryInputs = await readWorkspaceParseMetadata(page, session);
  const retryAttemptParsedIds = await readSelectedAttemptParseIds(page, session);
  assertAttemptParseRefsMatchDisplayed(retryAttemptParsedIds, await readWorkspaceSourceTabs(page, session), "attempt 2");
  if (JSON.stringify(firstAttemptInputs) !== JSON.stringify(retryInputs)) {
    throw new Error(`retry did not retain the first attempt's frozen parse identities and digests: first=${JSON.stringify(firstAttemptInputs)} retry=${JSON.stringify(retryInputs)}`);
  }
  if (JSON.stringify(firstAttemptParsedIds) !== JSON.stringify(retryAttemptParsedIds)) {
    throw new Error("retry changed the parsed document references pinned by the first attempt");
  }
  await clickText(page, session, "button", "双来源对照");
  const evidenceSources = await readEvidenceSources(page, session);
  if (evidenceSources.length === 0 || evidenceSources.some((item) =>
    !item.parsedDocumentId ||
    item.sourceParsedDocumentId !== item.parsedDocumentId
  )) {
    throw new Error(`evidence provenance does not match the displayed parse: ${JSON.stringify(evidenceSources)}`);
  }
  await page.wait(session, `(() => {
    const disclosure = document.querySelector('.execution-inspector .execution-inspector-disclosure');
    return Boolean(disclosure && !disclosure.querySelector('.ant-collapse-item-active') && document.querySelectorAll('.workspace-grid mark.hit-evidence').length >= 2);
  })()`);
  if (process.env.E2E_EXECUTION_SCREENSHOT) {
    await page.eval(session, "document.querySelector('.execution-inspector')?.scrollIntoView({ block: 'start' })");
    const shot = await page.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false }, session) as { data: string };
    await Bun.write(process.env.E2E_EXECUTION_SCREENSHOT, Buffer.from(shot.data, "base64"));
  }
  await page.wait(session, "document.querySelector('input[type=radio][value=confirm_risk]')?.matches(':enabled') && document.querySelector('textarea')?.matches(':enabled')");


  const radio = await page.eval(
    session,
    `(() => {
      const label = [...document.querySelectorAll("label")].find((item) => item.textContent.includes("确认风险"));
      const input = label?.querySelector("input");
      if (!input) return "missing";
      input.click();
      return "ok";
    })()`,
  );
  if (radio !== "ok") throw new Error(`confirm radio ${radio}`);
  await fill(page, session, "textarea", "两份材料写明的质保月数不同");
  await clickText(page, session, "button", "保存决定");
  await page.wait(session, "document.body.innerText.includes('已记录确认风险')");
  step("attempt 2 decision recorded");
  const oldRunURL = await page.eval(session, "location.href");
  await clickText(page, session, "a", "返回案件");
  await page.wait(session, "document.querySelector('input[type=file]')");
  await selectAntOption(page, session, "材料角色", "拟签合同");
  const beforeReplacement = await readCaseDetail(page, session, oldRunURL);
  const primaryMaterial = beforeReplacement.materials?.find((item) => item.purpose === "primary_contract");
  if (!primaryMaterial) throw new Error("current main contract has no material identity");
  if (primaryMaterial.current_document_id !== initialDraftParse.documentVersionId) {
    throw new Error(`current primary document ${primaryMaterial.current_document_id} differs from the frozen initial draft ${initialDraftParse.documentVersionId}`);
  }
  await selectAntOption(page, session, "操作", "替换选中的材料");
  const replacementNotice = `将替换“draft.txt”（主合同）当前第 ${primaryMaterial.current_version} 版；若版本已更新，请刷新重试。`;
  await page.wait(session, `document.body.innerText.includes(${JSON.stringify(replacementNotice)})`);
  const replacementRequestStart = page.documentUploads.length;
  await upload(page, session, revised);
  await clickText(page, session, "button", "替换材料");
  await waitParsedDocument(page, session, "draft-revised.txt");
  await assertReplacementMultipart(page.documentUploads.slice(replacementRequestStart), primaryMaterial.material_id, primaryMaterial.current_document_id);
  const revisedMetadata = await readDocumentMetadata(page, session, "draft-revised.txt");
  const afterReplacement = await readCaseDetail(page, session, oldRunURL);
  const revisedPrimary = afterReplacement.materials?.find((item) => item.material_id === primaryMaterial.material_id);
  if (!revisedPrimary || revisedPrimary.current_document_id !== revisedMetadata.documentVersionId || revisedPrimary.current_version !== primaryMaterial.current_version + 1) {
    throw new Error(`explicit primary replacement did not append a version: before=${JSON.stringify(primaryMaterial)} after=${JSON.stringify(revisedPrimary)} new_document=${revisedMetadata.documentVersionId}`);
  }
  step("primary material explicitly replaced; version increment verified");
  await fillReviewBusinessTime(page, session);
  await page.wait(session, `[...document.querySelectorAll('button')].some(
    (item) => item.textContent.trim() === '开始复审' && !item.disabled,
  )`);
  await clickText(page, session, "button", "开始复审");
  await loadDiagnostics(page, session);
  await page.wait(session, "document.querySelector('.review-provenance')?.innerText.includes('修改稿对照')");
  await openProvenanceSection(page, session, "修改稿对照");
  await page.wait(session, `(() => {
    const text = document.querySelector('.review-provenance')?.innerText.replace(/\\s/g, '') ?? '';
    return text.includes('修改稿对照') && text.includes('查看上一轮审查') &&
      text.includes('消除') && text.includes('先前决定') && text.includes('确认风险');
  })()`);
  const revisionRunURL = await page.eval(session, "location.href");
  const revisionRunParsedIds = await readSelectedAttemptParseIds(page, session);
  assertAttemptParseRefsMatchDisplayed(revisionRunParsedIds, await readWorkspaceSourceTabs(page, session), "revision run");
  if ((await page.eval(session, "location.href")) === oldRunURL) throw new Error("revision overwrote original run");
  await clickText(page, session, "a", "查看上一轮审查");
  await page.wait(session, "(() => { const text = document.body.innerText.replace(/\\s/g, ''); return text.includes('已记录确认风险') && text.includes('24个月'); })()");
  if ((await page.eval(session, "location.href")).split("?")[0] !== oldRunURL.split("?")[0]) {
    throw new Error("previous-run link did not preserve the original run identity");
  }
  await clickText(page, session, "a", "返回案件");
  await page.wait(session, "document.querySelector('input[type=file]')");
  const previousParse = await readDocumentMetadata(page, session, "draft-revised.txt");
  assertReadyParseMetadata(previousParse, "draft-revised.txt before explicit reparse");
  if (!revisionRunParsedIds.includes(previousParse.parsedDocumentId)) {
    throw new Error("revision run does not reference its displayed pre-reparse document parse");
  }
  await clickDocumentRowButton(page, session, primaryMaterial.material_id, "draft-revised.txt", "重新解析");
  await page.wait(session, `(() => {
    const row = [...document.querySelectorAll('.material-current-set .document-list li[data-material-id]')].find((item) => item.innerText.includes('draft-revised.txt'));
    const summary = row?.querySelector('.parsed-document-summary');
    return Boolean(summary && summary.dataset.parsedDocumentId !== ${JSON.stringify(previousParse.parsedDocumentId)} && document.body.innerText.includes('材料已更新'));
  })()`);
  const reparsedDocument = await readDocumentMetadata(page, session, "draft-revised.txt");
  assertReadyParseMetadata(reparsedDocument, "draft-revised.txt after explicit reparse");
  if (reparsedDocument.parsedDocumentId === previousParse.parsedDocumentId) {
    throw new Error("explicit text reparse reused the previous parsed document identity");
  }
  if (reparsedDocument.originalDigest !== previousParse.originalDigest || reparsedDocument.normalizedDigest !== previousParse.normalizedDigest) {
    throw new Error("explicit text reparse changed the original or normalized text digest for identical input");
  }
  if (reparsedDocument.parserId !== previousParse.parserId || reparsedDocument.parserVersion !== previousParse.parserVersion || reparsedDocument.configHash !== previousParse.configHash || reparsedDocument.normalizationVersion !== previousParse.normalizationVersion) {
    throw new Error("explicit text reparse changed parser configuration without an input change");
  }
  if (parsingScreenshot) {
    await page.eval(session, "window.scrollTo(0, 0)");
    const shot = await page.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false }, session) as { data: string };
    await Bun.write(parsingScreenshot, Buffer.from(shot.data, "base64"));
  }
  await fillReviewBusinessTime(page, session);
  await clickText(page, session, "button", "开始复审");
  await loadDiagnostics(page, session);
  await page.wait(session, `document.querySelector('.execution-inspector') && [...document.querySelectorAll('.review-material-context [role=tab] [data-parsed-document-id]')].some((item) => item.dataset.parsedDocumentId === ${JSON.stringify(reparsedDocument.parsedDocumentId)})`);
  step("reparse review submitted and selected parse verified");
  await selectParsedDocumentTab(page, session, reparsedDocument.parsedDocumentId);
  const reparsedRunURL = await page.eval(session, "location.href");
  if (new URL(reparsedRunURL).pathname === new URL(revisionRunURL).pathname) {
    throw new Error("explicit reparse reused the previous review run");
  }
  const reparsedInputs = await readWorkspaceParseMetadata(page, session);
  if (!reparsedInputs.some((item) => item.documentVersionId === reparsedDocument.documentVersionId && item.parsedDocumentId === reparsedDocument.parsedDocumentId)) {
    throw new Error("new review did not display the explicitly selected parsed document");
  }
  const documentTabs = await readWorkspaceSourceTabs(page, session);
  const reparsedAttemptIds = await readSelectedAttemptParseIds(page, session);
  if (!reparsedAttemptIds.includes(reparsedDocument.parsedDocumentId)) {
    throw new Error("post-reparse attempt did not pin the selected parsed document");
  }
  assertAttemptParseRefsMatchDisplayed(reparsedAttemptIds, documentTabs, "post-reparse run");
  const expectedReparsedIds = new Set(revisionRunParsedIds.map((id) =>
    id === previousParse.parsedDocumentId ? reparsedDocument.parsedDocumentId : id,
  ));
  const actualReparsedIds = new Set(reparsedAttemptIds);
  if (expectedReparsedIds.size !== actualReparsedIds.size || [...expectedReparsedIds].some((id) => !actualReparsedIds.has(id))) {
    throw new Error("new attempt parse references do not match the prior frozen inputs with the selected document reparse");
  }
  if (!reparsedAttemptIds.includes(reparsedDocument.parsedDocumentId) || reparsedAttemptIds.includes(previousParse.parsedDocumentId)) {
    throw new Error("new attempt did not pin the selected parse identity or retained its superseded parse");
  }
  const previousParseIds = documentTabs
    .filter((item) => item.documentVersionId === reparsedDocument.documentVersionId && item.parsedDocumentId !== "" && item.parsedDocumentId !== reparsedDocument.parsedDocumentId)
    .map((item) => item.parsedDocumentId);
  await selectParsedDocumentTab(page, session, reparsedDocument.parsedDocumentId);
  const selectedParse = await page.eval(session, "document.querySelector('.doc-pane[data-active-source=\"true\"]')?.dataset.parsedDocumentId ?? ''");
  if (selectedParse !== reparsedDocument.parsedDocumentId) {
    throw new Error(`current parsed document was not the primary selection: ${selectedParse}`);
  }
  for (const previousParseId of previousParseIds) {
    await selectParsedDocumentTab(page, session, previousParseId);
    const previousInputs = await readWorkspaceParseMetadata(page, session);
    if (!previousInputs.some((item) => item.documentVersionId === reparsedDocument.documentVersionId && item.parsedDocumentId === previousParseId)) {
      throw new Error(`historical parse ${previousParseId} did not become the displayed source`);
    }
  }
  if (previousParseIds.length > 0) await selectParsedDocumentTab(page, session, reparsedDocument.parsedDocumentId);
  await page.send("Page.navigate", { url: revisionRunURL }, session);
  await page.wait(session, "document.querySelector('.review-provenance')?.innerText.includes('修改稿对照')");
  await openProvenanceSection(page, session, "修改稿对照");
  await page.wait(session, `(() => {
    const text = document.querySelector('.review-provenance')?.innerText.replace(/\\s/g, '') ?? '';
    return text.includes('修改稿对照') && text.includes('查看上一轮审查') &&
      text.includes('消除') && text.includes('先前决定') && text.includes('确认风险');
  })()`);
  await selectParsedDocumentTab(page, session, previousParse.parsedDocumentId);
  const restoredRevisionInputs = await readWorkspaceParseMetadata(page, session);
  if (!restoredRevisionInputs.some((item) => item.documentVersionId === previousParse.documentVersionId && item.parsedDocumentId === previousParse.parsedDocumentId)) {
    throw new Error("pre-reparse revision run no longer references its original parsed document");
  }
  if ((await page.eval(session, "location.href")).split("?")[0] !== revisionRunURL.split("?")[0]) {
    throw new Error("reparse changed the pre-reparse revision run identity");
  }
  await page.send("Page.navigate", { url: oldRunURL }, session);
  await page.wait(session, "(() => { const text = document.body.innerText.replace(/\\s/g, ''); return text.includes('已记录确认风险') && text.includes('24个月'); })()");
  const restoredOldRunInputs = await readWorkspaceParseMetadata(page, session);
  if (!restoredOldRunInputs.some((item) => item.documentVersionId === initialDraftParse.documentVersionId && item.parsedDocumentId === initialDraftParse.parsedDocumentId)) {
    throw new Error("original run history no longer references its pre-reparse parsed document");
  }
  if ((await page.eval(session, "location.href")).split("?")[0] !== oldRunURL.split("?")[0]) {
    throw new Error("reparse changed the original run identity");
  }
  await page.wait(session, `document.querySelector('a[href="/api-docs"]')`);
  const docsLink = await page.eval(session, `(() => {
    const link = document.querySelector('a[href="/api-docs"]');
    if (!(link instanceof HTMLAnchorElement)) return 'missing API docs link';
    link.click();
    return 'clicked';
  })()`);
  if (docsLink !== "clicked") throw new Error(docsLink);
  await page.wait(session, "location.pathname === '/api-docs'");
  await page.send("Page.reload", {}, session);
  await page.wait(session, "document.querySelector('[aria-label=\"OpenAPI 操作导航\"] input[type=search]')");
  await fill(page, session, '[aria-label="OpenAPI 操作导航"] input[type=search]', "/api/cases");
  const operation = await page.eval(session, `(() => {
    const label = [...document.querySelectorAll('.api-operation-menu-label')].find(
      (item) => item.textContent?.includes('列出可见案件'),
    );
    if (!label) return 'missing';
    const item = label.closest('.ant-menu-item');
    (item ?? label).click();
    return 'ok';
  })()`);
  if (operation !== "ok") throw new Error(`API operation 列出可见案件 ${operation}`);
  await page.wait(session, "document.querySelector('[aria-label=\"接口详细文档\"]')?.innerText.includes('/api/cases') && document.querySelector('[aria-label=\"接口详细文档\"]')?.innerText.includes('200')");
  await clickText(page, session, "button", "发送真实请求");
  await page.wait(session, "document.querySelector('.api-response-panel')?.innerText.includes('200') && document.querySelector('.api-response-body')?.innerText.includes('采购质保核对')");
  const foreign = await page.eval(session, "performance.getEntriesByType('resource').map(item => item.name).filter(url => /^https?:/.test(url) && new URL(url).origin !== location.origin).join(',')");
  if (foreign) throw new Error(`documentation used foreign browser resources: ${foreign}`);
  const localContract = await page.eval(session, "performance.getEntriesByType('resource').map(item => new URL(item.name, location.href).pathname).filter(path => path === '/openapi.json').length");
  if (localContract === "0") throw new Error("documentation did not load same-origin /openapi.json");
  const rawContract = await fetch(`${web}/openapi.json`);
  const contract = await rawContract.json() as { openapi?: string; paths?: Record<string, Record<string, unknown>> };
  if (!rawContract.ok || contract.openapi !== "3.1.1" || !contract.paths?.["/api/cases"]?.get) {
    throw new Error("downloadable OpenAPI contract does not match the real API");
  }
  if (process.env.E2E_DOCS_SCREENSHOT) {
    await page.eval(session, "window.scrollTo(0, 0)");
    const shot = await page.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false }, session) as { data: string };
    await Bun.write(process.env.E2E_DOCS_SCREENSHOT, Buffer.from(shot.data, "base64"));
  }
  if (page.consoleErrors.length > 0) throw new Error(`browser console errors: ${page.consoleErrors.join(" | ")}`);
  console.log("browser console errors: none");
  console.log("browser e2e ok: login/upload/parse-metadata/evidence-provenance/retry-attempt-history/revision/text-reparse/new-run/old-run-preserved/api-search-authenticated-request-local");
} catch (error) {
  failed = true;
  console.error(error);
  if (page && failureDOM) {
    try {
      await Bun.write(failureDOM, await page.eval(pageSession ?? "", "document.body ? document.body.innerText : 'no body'"));
    } catch (snapshotError) {
      console.error(`[diagnostic] DOM snapshot unavailable: ${snapshotError instanceof Error ? snapshotError.message : String(snapshotError)}`);
    }
  }
  if (page && failureScreenshot) {
    try {
      const shot = await page.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false }, pageSession) as { data: string };
      await Bun.write(failureScreenshot, Buffer.from(shot.data, "base64"));
    } catch (snapshotError) {
      console.error(`[diagnostic] screenshot unavailable: ${snapshotError instanceof Error ? snapshotError.message : String(snapshotError)}`);
    }
  }
  process.exitCode = 1;
} finally {
  page?.close();
  await stopChrome();
  if (failed) process.exitCode = 1;
}
}

function step(message: string): void {
  console.log(`[step] ${message}`);
}

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

async function waitJSON(url: string): Promise<Record<string, unknown>> {
  const deadline = Date.now() + 15000;
  let last = "";
  while (Date.now() < deadline) {
    try {
      const value = await localJSON(url);
      if (value) return value;
    } catch (error) {
      last = String(error);
    }
    await Bun.sleep(200);
  }
  throw new Error(`chromium debugger did not answer: ${last}`);
}

async function localJSON(url: string): Promise<Record<string, unknown> | null> {
  return await new Promise((resolve, reject) => {
    const request = httpRequest(url, { timeout: 1500 }, (response) => {
      let body = "";
      response.setEncoding("utf8");
      response.on("data", (chunk: string) => { body += chunk; });
      response.on("end", () => {
        if ((response.statusCode ?? 0) < 200 || (response.statusCode ?? 0) >= 300) {
          resolve(null);
          return;
        }
        try {
          resolve(JSON.parse(body) as Record<string, unknown>);
        } catch (error) {
          reject(error);
        }
      });
    });
    request.on("timeout", () => request.destroy(new Error("loopback debugger request timed out after 1500ms")));
    request.on("error", reject);
    request.end();
  });
}

class Page {
  private socket: WebSocket;
  private next = 0;
  private pending = new Map<number, {
    timer: ReturnType<typeof setTimeout>;
    resolve: (message: { result?: unknown; error?: { message: string } }) => void;
    reject: (error: Error) => void;
  }>();
  private opened: Promise<void>;
  readonly consoleErrors: string[] = [];
  readonly documentUploads: Array<{
    url: string;
    method: string;
    requestId?: string;
    sessionId?: string;
    postData?: string;
    postDataError?: string;
    postDataReady: Promise<void>;
  }> = [];

  constructor(url: string) {
    this.socket = new WebSocket(url);
    this.socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data)) as {
        id?: number;
        sessionId?: string;
        result?: unknown;
        error?: { message: string };
        method?: string;
        params?: {
          type?: string;
          args?: { value?: unknown; description?: string }[];
          exceptionDetails?: { text?: string; exception?: { description?: string } };
          requestId?: string;
          request?: { url?: string; method?: string; postData?: string };
        };
      };
      if (message.id === undefined) {
        const params = message.params;
        if (message.method === "Runtime.exceptionThrown") {
          this.consoleErrors.push(params?.exceptionDetails?.text ?? params?.exceptionDetails?.exception?.description ?? "uncaught browser exception");
        } else if (message.method === "Runtime.consoleAPICalled" && params?.type === "error") {
          this.consoleErrors.push(params.args?.map((argument) => argument.value ?? argument.description ?? "").join(" ") || "console.error");
        } else if (message.method === "Network.requestWillBeSent" && params?.request?.method === "POST" && params.request.url?.endsWith("/documents")) {
          const upload: {
            url: string;
            method: string;
            requestId?: string;
            sessionId?: string;
            postData?: string;
            postDataError?: string;
            postDataReady: Promise<void>;
          } = {
            url: params.request.url,
            method: params.request.method,
            requestId: params.requestId,
            sessionId: message.sessionId,
            postData: params.request.postData,
            postDataReady: Promise.resolve(),
          };
          upload.postDataReady = readCDPRequestPostData({
            requestId: upload.requestId,
            sessionId: upload.sessionId,
            postData: upload.postData,
            getRequestPostData: (requestId, sessionId) => this.send(
              "Network.getRequestPostData",
              { requestId },
              sessionId,
            ),
          }).then((postData) => {
            upload.postData = postData;
          }).catch((error: unknown) => {
            upload.postDataError = error instanceof Error ? error.message : String(error);
          });
          this.documentUploads.push(upload);
        }
        return;
      }
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      clearTimeout(pending.timer);
      pending.resolve(message);
    });
    this.opened = new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("Chromium debugger websocket open timed out after 10000ms")), 10000);
      this.socket.addEventListener("open", () => {
        clearTimeout(timer);
        resolve();
      }, { once: true });
      this.socket.addEventListener("error", () => {
        clearTimeout(timer);
        this.rejectPending(new Error("chromium socket failed"));
        reject(new Error("chromium socket failed"));
      }, { once: true });
      this.socket.addEventListener("close", () => {
        this.rejectPending(new Error("chromium debugger websocket closed"));
      }, { once: true });
    });
  }

  async send(method: string, params: Record<string, unknown> = {}, sessionId?: string): Promise<unknown> {
    await this.opened;
    const id = ++this.next;
    const body: Record<string, unknown> = { id, method, params };
    if (sessionId) body.sessionId = sessionId;
    const result = new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`CDP method timed out after 10000ms: ${method}`));
      }, 10000);
      this.pending.set(id, { timer, resolve: (message) => {
        if (message.error) reject(new Error(`${method}: ${message.error.message}`));
        else resolve(message.result);
      }, reject });
    });
    this.socket.send(JSON.stringify(body));
    return result;
  }

  close(): void {
    this.rejectPending(new Error("Chromium debugger page closed"));
    this.socket.close();
  }

  private rejectPending(error: Error): void {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
  }

  async eval(session: string, expression: string): Promise<string> {
    const result = (await this.send(
      "Runtime.evaluate",
      { expression, returnByValue: true, awaitPromise: true },
      session,
    )) as { result?: { value?: string }; exceptionDetails?: { text?: string; exception?: { description?: string } } };
    if (result.exceptionDetails) throw new Error(`${result.exceptionDetails.exception?.description ?? result.exceptionDetails.text ?? "browser evaluation failed"}\nExpression: ${expression}`);
    return String(result.result?.value ?? "");
  }

  async wait(session: string, expression: string): Promise<void> {
    const deadline = Date.now() + 15000;
    let last = "";
    while (Date.now() < deadline) {
      last = await this.eval(session, `(() => (${expression}) ? "yes" : "no")()`);
      if (last === "yes") return;
      await Bun.sleep(200);
    }
    const text = await this.eval(session, "document.body ? document.body.innerText.slice(0, 1200) : 'no body'");
    throw new Error(`timed out waiting for ${expression}\n${text}`);
  }
}

async function fill(page: Page, session: string, selector: string, value: string): Promise<void> {
  const status = await page.eval(
    session,
    `(() => {
      const el = document.querySelector(${JSON.stringify(selector)});
      if (!el) return "missing";
      const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement : HTMLInputElement;
      const set = Object.getOwnPropertyDescriptor(proto.prototype, "value")?.set;
      if (!set) return "no setter";
      set.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return "ok";
    })()`,
  );
  if (status !== "ok") throw new Error(`fill ${selector}: ${status}`);
}

async function fillReviewBusinessTime(page: Page, session: string): Promise<void> {
  const selector = 'input[aria-label="审查业务时间（本地时区）"]';
  const present = await page.eval(
    session,
    `Boolean(document.querySelector(${JSON.stringify(selector)}))`,
  );
  if (present !== "true") return;
  const input = c1bBusinessAtInput(C1B_BUSINESS_TIME_ZONE);
  await fill(page, session, selector, input);
  const resolved = new Date(`${input}:00+08:00`).toISOString();
  if (resolved !== C1B_BUSINESS_AT_UTC) throw new Error(`business time ${input} resolved to ${resolved}, expected ${C1B_BUSINESS_AT_UTC}`);
}

async function clickText(page: Page, session: string, tag: string, text: string): Promise<void> {
  await page.wait(session, `[...document.querySelectorAll(${JSON.stringify(tag)})].some((item) => item.textContent.trim() === ${JSON.stringify(text)} && item.matches(':not(:disabled)'))`);
  const status = await page.eval(
    session,
    `(() => {
      const node = [...document.querySelectorAll(${JSON.stringify(tag)})].find((item) => item.textContent.trim() === ${JSON.stringify(text)} && !item.disabled);
      if (!node) return "missing";
      node.click();
      return "ok";
    })()`,
  );
  if (status !== "ok") throw new Error(`click ${text}: ${status}`);
}

async function loadDiagnostics(page: Page, session: string): Promise<void> {
  await page.wait(session, `document.body.innerText.includes("加载运行轨迹与诊断") || document.querySelector(".execution-inspector")`);
  const hasButton = await page.eval(
    session,
    `[...document.querySelectorAll("button")].some((item) => item.textContent.trim() === "加载运行轨迹与诊断" && !item.disabled)`,
  );
  if (hasButton === "true") await clickText(page, session, "button", "加载运行轨迹与诊断");
  await page.wait(session, "document.querySelector('.execution-inspector')");
}

async function clickExpression(page: Page, session: string, expression: string): Promise<string> {
  const raw = await page.eval(
    session,
    `(() => {
      const element = ${expression};
      if (!(element instanceof HTMLElement)) return "missing";
      element.scrollIntoView({ block: "center", inline: "center" });
      const rect = element.getBoundingClientRect();
      if (rect.width === 0 || rect.height === 0) return "hidden";
      const x = rect.left + rect.width / 2;
      const y = rect.top + rect.height / 2;
      if (x < 0 || y < 0 || x > window.innerWidth || y > window.innerHeight) return "out-of-viewport";
      return JSON.stringify({ x, y });
    })()`,
  );
  if (!raw.startsWith("{")) return raw;
  const point = JSON.parse(raw) as { x: number; y: number };
  await page.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: point.x, y: point.y }, session);
  await page.send("Input.dispatchMouseEvent", {
    type: "mousePressed", x: point.x, y: point.y, button: "left", buttons: 1, clickCount: 1,
  }, session);
  await page.send("Input.dispatchMouseEvent", {
    type: "mouseReleased", x: point.x, y: point.y, button: "left", buttons: 0, clickCount: 1,
  }, session);
  return "ok";
}

async function selectAntOption(
  page: Page,
  session: string,
  labelText: string,
  optionText: string,
): Promise<void> {
  const fieldExpression = `[...document.querySelectorAll("label.field")].find(
    (item) => item.textContent.trim().startsWith(${JSON.stringify(labelText)}),
  )`;
  const visible = JSON.stringify(optionText);
  const optionExpression = `[...document.querySelectorAll(".ant-select-dropdown .ant-select-item-option")].find(
    (item) => item.getAttribute("title") === ${visible} &&
      item.getBoundingClientRect().height > 0,
  )`;
  const selectedExpression = `(() => {
    const content = (${fieldExpression})?.querySelector(".ant-select-content");
    return content?.textContent?.trim() === ${visible} ||
      content?.getAttribute("title") === ${visible};
  })()`;
  let state = "";
  for (let attempt = 0; attempt < 5; attempt++) {
    if ((await page.eval(session, `Boolean(${optionExpression})`)) !== "true") {
      const opened = await clickExpression(
        page,
        session,
        `(${fieldExpression})?.querySelector("input[role=combobox]")`,
      );
      if (opened !== "ok") throw new Error(`select ${labelText}: ${opened}`);
    }
    const deadline = Date.now() + 5000;
    while (Date.now() < deadline) {
      if ((await page.eval(session, `Boolean(${optionExpression})`)) === "true") break;
      await Bun.sleep(100);
    }
    const picked = await clickExpression(page, session, optionExpression);
    if (picked !== "ok") {
      const options = await page.eval(
        session,
        `JSON.stringify([...document.querySelectorAll(".ant-select-dropdown .ant-select-item-option")].map(
          (item) => ({ title: item.getAttribute("title"), height: item.getBoundingClientRect().height }),
        ))`,
      );
      state = `click=${picked} options=${options}`;
      await Bun.sleep(200);
      continue;
    }
    try {
      await page.wait(session, selectedExpression);
      return;
    } catch (error) {
      state = await page.eval(
        session,
        `JSON.stringify({
          content: (${fieldExpression})?.querySelector(".ant-select-content")?.textContent,
          title: (${fieldExpression})?.querySelector(".ant-select-content")?.getAttribute("title"),
          expanded: (${fieldExpression})?.querySelector("input[role=combobox]")?.getAttribute("aria-expanded"),
          dropdowns: [...document.querySelectorAll(".ant-select-dropdown")].map((item) => ({
            visible: item.getBoundingClientRect().height > 0,
            options: [...item.querySelectorAll(".ant-select-item-option")].map((option) => option.getAttribute("title")),
          })),
        })`,
      );
      await Bun.sleep(200);
    }
  }
  throw new Error(`select ${labelText}: option ${optionText} not selected; state=${state}`);
}

async function upload(page: Page, session: string, path: string): Promise<void> {
  const filename = path.split("/").at(-1) ?? path;
  for (let attempt = 0; attempt < 3; attempt++) {
    await page.wait(
      session,
      "document.querySelector('input[type=file]:not([disabled])')?.isConnected",
    );
    await Bun.sleep(150);
    try {
      const document = (await page.send("DOM.getDocument", {}, session)) as { root?: { nodeId: number } };
      const found = (await page.send(
        "DOM.querySelector",
        { nodeId: document.root?.nodeId, selector: "input[type=file]:not([disabled])" },
        session,
      )) as { nodeId?: number };
      if (!found.nodeId) throw new Error("file input missing");
      await page.send("DOM.setFileInputFiles", { nodeId: found.nodeId, files: [path] }, session);
      await page.wait(session, `document.body.innerText.includes(${JSON.stringify(filename)})`);
      return;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (!message.includes("Could not find node with given id") || attempt === 2) {
        throw new Error(`upload ${path}: ${message}`);
      }
      await Bun.sleep(100);
    }
  }
}

async function readCaseDetail(
  page: Page,
  session: string,
  runURL: string,
): Promise<{ materials?: Array<{ material_id: string; purpose: string; current_document_id: string; current_version: number }> }> {
  const caseID = new URL(runURL).pathname.split("/")[2] ?? "";
  if (!caseID) throw new Error(`case ID missing from review URL ${runURL}`);
  const raw = await page.eval(session, `fetch(${JSON.stringify(`/api/cases/${caseID}`)}).then(async (response) => {
    if (!response.ok) throw new Error('case detail HTTP ' + response.status);
    return JSON.stringify(await response.json());
  })`);
  return JSON.parse(raw) as { materials?: Array<{ material_id: string; purpose: string; current_document_id: string; current_version: number }> };
}

interface DocumentMetadataSnapshot {
  documentVersionId: string;
  parsedDocumentId: string;
  originalDigest: string;
  parsedOriginalDigest: string;
  parserId: string;
  parserVersion: string;
  configHash: string;
  normalizedDigest: string;
  normalizationVersion: string;
  irDigest: string;
  artifactDigest: string;
  parsedCreatedAt: string;
  status: string;
  text: string;
}

interface WorkspaceParseSnapshot {
  documentVersionId: string;
  parsedDocumentId: string;
  originalDigest: string;
  normalizedDigest: string;
  artifactDigest: string;
  configHash: string;
}

interface EvidenceSourceSnapshot {
  documentVersionId: string;
  parsedDocumentId: string;
  sourceParsedDocumentId: string;
}
interface DocumentTabSnapshot {
  documentVersionId: string;
  parsedDocumentId: string;
}

async function readWorkspaceSourceTabs(page: Page, session: string): Promise<DocumentTabSnapshot[]> {
  const raw = await page.eval(session, `JSON.stringify([...document.querySelectorAll('.review-material-context [role=tab] [data-document-version-id]')].map((tab) => ({
    documentVersionId: tab.dataset.documentVersionId ?? '',
    parsedDocumentId: tab.dataset.parsedDocumentId ?? '',
  })))`);
  return JSON.parse(raw) as DocumentTabSnapshot[];
}

async function waitParsedDocument(page: Page, session: string, filename: string): Promise<void> {
  await page.wait(session, `(() => {
    const row = [...document.querySelectorAll('.material-current-set .document-list li[data-material-id]')].find(
      (item) => item.innerText.includes(${JSON.stringify(filename)}),
    );
    return row?.querySelector('.parsed-document-summary')?.dataset.status === 'ready';
  })()`);
}

async function readDocumentMetadata(page: Page, session: string, filename: string): Promise<DocumentMetadataSnapshot> {
  const raw = await page.eval(session, `(() => {
    const row = [...document.querySelectorAll('.material-current-set .document-list li[data-material-id]')].find((item) => item.innerText.includes(${JSON.stringify(filename)}));
    const summary = row?.querySelector('.parsed-document-summary');
    if (!summary) return '';
    const data = summary.dataset;
    return JSON.stringify({
      documentVersionId: data.documentVersionId ?? '',
      parsedDocumentId: data.parsedDocumentId ?? '',
      originalDigest: data.originalDigest ?? '',
      parsedOriginalDigest: data.parsedOriginalDigest ?? '',
      parserId: data.parserId ?? '',
      parserVersion: data.parserVersion ?? '',
      configHash: data.configHash ?? '',
      normalizedDigest: data.normalizedDigest ?? '',
      normalizationVersion: data.normalizationVersion ?? '',
      irDigest: data.irDigest ?? '',
      artifactDigest: data.artifactDigest ?? '',
      parsedCreatedAt: data.parsedCreatedAt ?? '',
      status: data.status ?? '',
      text: summary.innerText,
    });
  })()`);
  if (!raw) throw new Error(`parse metadata missing from ${filename}`);
  return JSON.parse(raw) as DocumentMetadataSnapshot;
}

function assertReadyParseMetadata(metadata: DocumentMetadataSnapshot, label: string): void {
  const requiredFields = [
    metadata.documentVersionId,
    metadata.parsedDocumentId,
    metadata.originalDigest,
    metadata.parsedOriginalDigest,
    metadata.parserId,
    metadata.parserVersion,
    metadata.configHash,
    metadata.normalizedDigest,
    metadata.normalizationVersion,
    metadata.artifactDigest,
    metadata.parsedCreatedAt,
  ];
  if (requiredFields.some((value) => value === "")) throw new Error(`incomplete parsed metadata for ${label}`);
  if (metadata.parsedOriginalDigest !== metadata.originalDigest || metadata.status !== "ready") {
    throw new Error(`original digest or parse status is inconsistent for ${label}`);
  }
  for (const field of ["原件 SHA-256", "解析器", "配置 SHA-256", "规范化", "正文 SHA-256", "IR SHA-256", "产物 SHA-256"]) {
    if (!metadata.text.includes(field)) throw new Error(`${field} is not displayed for ${label}`);
  }
}

async function readWorkspaceParseMetadata(page: Page, session: string): Promise<WorkspaceParseSnapshot[]> {
  const rawTabs = await page.eval(session, `JSON.stringify([...document.querySelectorAll('.review-material-context [role="tab"] [data-document-version-id]')].map((item, index) => ({
    index,
    documentVersionId: item.dataset.documentVersionId ?? '',
    parsedDocumentId: item.dataset.parsedDocumentId ?? '',
  })))`);
  const tabs = JSON.parse(rawTabs) as Array<{ index: number; documentVersionId: string; parsedDocumentId: string }>;
  if (tabs.length === 0) throw new Error("review workspace has no source tabs");
  const snapshots: WorkspaceParseSnapshot[] = [];
  for (const tab of tabs) {
    const selected = await page.eval(session, `(() => {
      const item = document.querySelectorAll('.review-material-context [role="tab"] [data-document-version-id]')[${tab.index}];
      if (!(item instanceof HTMLElement) || item.dataset.documentVersionId !== ${JSON.stringify(tab.documentVersionId)} || item.dataset.parsedDocumentId !== ${JSON.stringify(tab.parsedDocumentId)}) return 'changed';
      item.click();
      return 'selected';
    })()`);
    if (selected !== "selected") throw new Error(`source tab ${tab.index} changed while reading its parse metadata: ${selected}`);
    try {
      await page.wait(session, `(() => {
        const pane = document.querySelector('.doc-pane[data-active-source="true"]');
        return pane?.dataset.documentVersionId === ${JSON.stringify(tab.documentVersionId)} && pane?.dataset.parsedDocumentId === ${JSON.stringify(tab.parsedDocumentId)};
      })()`);
    } catch (error) {
      const state = await page.eval(session, `JSON.stringify({
        panes: [...document.querySelectorAll('.doc-pane')].map((pane) => ({
          active: pane.dataset.activeSource,
          documentVersionId: pane.dataset.documentVersionId,
          parsedDocumentId: pane.dataset.parsedDocumentId,
        })),
      })`);
      throw new Error(`source tab ${tab.index} did not show its frozen parse identity: ${state}`);
    }
    // The business workspace deliberately withholds parser metadata; the
    // frozen parse identity is exposed on the displayed source pane.
    snapshots.push({
      documentVersionId: tab.documentVersionId,
      parsedDocumentId: tab.parsedDocumentId,
      originalDigest: "",
      normalizedDigest: "",
      artifactDigest: "",
      configHash: "",
    });
  }
  return snapshots.sort((left, right) => (left.documentVersionId + ':' + left.parsedDocumentId).localeCompare(right.documentVersionId + ':' + right.parsedDocumentId));
}

async function readEvidenceSources(page: Page, session: string): Promise<EvidenceSourceSnapshot[]> {
  const raw = await page.eval(session, `(() => JSON.stringify([...document.querySelectorAll('.evidence-card')].map((card) => {
    const evidence = card.querySelector('[data-evidence-parsed-document-id]');
    const documentVersionId = evidence?.getAttribute('data-evidence-document-version-id') ?? '';
    const parsedDocumentId = evidence?.getAttribute('data-evidence-parsed-document-id') ?? '';
    const source = [...document.querySelectorAll('.doc-pane')].find((item) =>
      item.dataset.documentVersionId === documentVersionId &&
      item.dataset.parsedDocumentId === parsedDocumentId
    );
    return {
      documentVersionId,
      parsedDocumentId,
      sourceParsedDocumentId: source?.dataset.parsedDocumentId ?? '',
    };
  })))()`);
  return JSON.parse(raw) as EvidenceSourceSnapshot[];
}

async function openProvenanceSection(page: Page, session: string, label: string): Promise<void> {
  const status = await page.eval(session, `(() => {
    const header = [...document.querySelectorAll('.review-provenance .ant-collapse-header')].find(
      (item) => (item.textContent ?? '').trim().startsWith(${JSON.stringify(label)}),
    );
    if (!header) return 'missing';
    if (header.getAttribute('aria-expanded') !== 'true') header.click();
    return 'ok';
  })()`);
  if (status !== "ok") throw new Error(`provenance section ${label}: ${status}`);
  await Bun.sleep(150);
}

async function setInspectorDisclosure(page: Page, session: string, expanded: boolean): Promise<void> {
  await page.eval(session, `(() => {
    const collapse = document.querySelector('.execution-inspector .execution-inspector-disclosure');
    const header = collapse?.querySelector('.ant-collapse-header');
    const open = header?.getAttribute('aria-expanded') === 'true' ||
      Boolean(collapse?.querySelector('.ant-collapse-item-active'));
    if (header && open !== ${expanded}) header.click();
    return 'ok';
  })()`);
  await Bun.sleep(100);
}

async function selectAttemptNumber(page: Page, session: string, ordinal: number): Promise<void> {
  const optionPrefix = JSON.stringify(`第 ${ordinal} 次`);
  const target = JSON.stringify(String(ordinal));
  await page.wait(session, `document.querySelector('.execution-attempt-row')?.dataset.attemptOrdinal !== undefined`);
  const current = await page.eval(
    session,
    `document.querySelector('.execution-attempt-row')?.dataset.attemptOrdinal ?? ''`,
  );
  if (current !== String(ordinal)) {
    await setInspectorDisclosure(page, session, true);
    const optionExpression = `[...document.querySelectorAll(".ant-select-dropdown .ant-select-item-option")].find(
      (item) => (item.getAttribute("title") ?? item.textContent?.trim() ?? "").startsWith(${optionPrefix}) &&
        item.getBoundingClientRect().height > 0,
    )`;
    let state = "";
    let selected = false;
    for (let round = 0; round < 5 && !selected; round++) {
      if ((await page.eval(session, `Boolean(${optionExpression})`)) !== "true") {
        const opened = await clickExpression(
          page,
          session,
          `document.querySelector('.execution-attempt-row input[role=combobox]')`,
        );
        if (opened !== "ok") {
          state = `open=${opened} ` + (await page.eval(session, `JSON.stringify({
            rows: [...document.querySelectorAll('.execution-attempt-row')].map((item) => item.outerHTML.slice(0, 500)),
            body: document.body?.innerText.slice(0, 800) ?? '',
          })`));
          await Bun.sleep(200);
          continue;
        }
      }
      const deadline = Date.now() + 5000;
      while (Date.now() < deadline) {
        if ((await page.eval(session, `Boolean(${optionExpression})`)) === "true") break;
        await Bun.sleep(100);
      }
      const picked = await clickExpression(page, session, optionExpression);
      if (picked === "ok") {
        try {
          await page.wait(
            session,
            `document.querySelector('.execution-attempt-row')?.dataset.attemptOrdinal === ${target} &&
              document.querySelector('.execution-inspector-summary p')?.innerText.includes(${JSON.stringify(`第 ${ordinal} 次执行`)})`,
          );
          selected = true;
          continue;
        } catch (error) {
          state = `not reflected: ${error instanceof Error ? error.message.split("\n")[0] : String(error)}`;
        }
      } else {
        state = `click=${picked}`;
      }
      state += " " + (await page.eval(session, `JSON.stringify({
        current: document.querySelector('.execution-attempt-row')?.dataset.attemptOrdinal,
        dropdowns: [...document.querySelectorAll('.ant-select-dropdown')].map((item) => ({
          visible: item.getBoundingClientRect().height > 0,
          options: [...item.querySelectorAll('.ant-select-item-option')].map((option) => ({
            title: option.getAttribute('title'),
            text: option.textContent?.trim(),
            height: option.getBoundingClientRect().height,
          })),
        })),
        body: document.body?.innerText.slice(0, 500) ?? '',
      })`));
      await Bun.sleep(200);
    }
    if (!selected) throw new Error(`attempt ${ordinal} could not be selected: ${state}`);
  }
  await page.wait(
    session,
    `document.querySelector('.execution-attempt-row')?.dataset.attemptOrdinal === ${target} &&
      document.querySelector('.execution-inspector-summary p')?.innerText.includes(${JSON.stringify(`第 ${ordinal} 次执行`)})`,
  );
  await setInspectorDisclosure(page, session, false);
}

async function readSelectedAttemptParseIds(page: Page, session: string): Promise<string[]> {
  await page.wait(session, `(() => {
    const raw = document.querySelector('.execution-attempt-row')?.dataset.parsedDocumentIds;
    if (!raw) return false;
    const ids = JSON.parse(raw);
    return Array.isArray(ids) && ids.length > 0;
  })()`);
  const raw = await page.eval(
    session,
    `document.querySelector('.execution-attempt-row')?.dataset.parsedDocumentIds ?? "[]"`,
  );
  const value = JSON.parse(raw) as unknown;
  if (!Array.isArray(value) || !value.every((item) => typeof item === "string")) {
    throw new Error(`selected attempt has invalid parsed document references: ${raw}`);
  }
  return value as string[];
}

function assertAttemptParseRefsMatchDisplayed(attemptIds: string[], displayed: DocumentTabSnapshot[], label: string): void {
  if (attemptIds.length === 0) throw new Error(`${label} has no frozen parsed document references`);
  const displayedIds = new Set(displayed.map((item) => item.parsedDocumentId));
  const missing = attemptIds.filter((id) => !displayedIds.has(id));
  if (missing.length > 0) throw new Error(`${label} references undisplayed parses: ${missing.join(", ")}`);
}

async function assertReplacementMultipart(
  requests: Array<{ url: string; method: string; postData?: string; postDataError?: string; postDataReady: Promise<void> }>,
  materialId: string,
  replacesDocumentId: string,
): Promise<void> {
  await Promise.all(requests.map((item) => item.postDataReady));
  const request = requests.find((item) => item.method === "POST" && item.url.endsWith("/documents"));
  if (request?.postDataError) throw new Error(`could not inspect replacement upload request body: ${request.postDataError}`);
  if (!request?.postData) throw new Error("replacement upload request did not expose multipart fields");
  if (!request.postData.includes(`name="material_id"\r\n\r\n${materialId}\r\n`)) {
    throw new Error("replacement request did not send the selected material identity");
  }
  if (!request.postData.includes(`name="replaces_document_id"\r\n\r\n${replacesDocumentId}\r\n`)) {
    throw new Error("replacement request did not send the expected current document for CAS");
  }
}

async function clickDocumentRowButton(page: Page, session: string, materialId: string, filename: string, text: string): Promise<void> {
  const status = await page.eval(session, `(() => {
    const row = [...document.querySelectorAll('.material-current-set .document-list li[data-material-id]')].find((item) => item.dataset.materialId === ${JSON.stringify(materialId)});
    if (row && !row.innerText.includes(${JSON.stringify(filename)})) return 'filename-mismatch';
    const button = [...(row?.querySelectorAll('button') ?? [])].find((item) => item.textContent?.trim() === ${JSON.stringify(text)} && !item.disabled);
    if (!button) return 'missing';
    button.click();
    return 'ok';
  })()`);
  if (status !== "ok") throw new Error(`${text} button missing in ${filename}: ${status}`);
}

async function selectParsedDocumentTab(page: Page, session: string, parsedDocumentId: string): Promise<void> {
  const status = await page.eval(session, `(() => {
    const tab = [...document.querySelectorAll('.review-material-context [role=tab] [data-parsed-document-id]')].find((item) => item.dataset.parsedDocumentId === ${JSON.stringify(parsedDocumentId)});
    if (!tab) return 'missing';
    tab.click();
    return 'ok';
  })()`);
  if (status !== "ok") throw new Error(`parsed source ${parsedDocumentId} was not selectable`);
  await page.wait(session, `(() => {
    const label = [...document.querySelectorAll('.review-material-context [role=tab] [data-parsed-document-id]')].find(
      (item) => item.dataset.parsedDocumentId === ${JSON.stringify(parsedDocumentId)},
    );
    return label?.closest('[role=tab]')?.getAttribute('aria-selected') === 'true' ||
      Boolean(label?.closest('.ant-tabs-tab.ant-tabs-tab-active'));
  })()`);
}

await run();
