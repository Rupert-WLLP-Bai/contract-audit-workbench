import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium, type Page } from "playwright";

/**
 * D2-7b browser evidence: session revocation and identity switch against a real
 * runtime. The subject is the application's own behavior in a real browser:
 *
 * 1. the review workspace subscribes its EventSource stream (observed through
 *    the network layer, not mocked);
 * 2. deleting the server session while that stream is open must end it without
 *    showing cached findings afterwards, and the next read must be refused;
 * 3. signing in as another identity must subscribe a fresh stream and must not
 *    present the former identity's data; a mutation from the read-only identity
 *    must be refused.
 *
 * Setup (case, materials, review) goes through the same HTTP API the UI uses,
 * with the browser context's cookies, reusing the login/multipart shapes from
 * web/e2e/c1b-journey.ts. The vitest suites already cover the component-level
 * contract; this runner adds the browser-level evidence.
 *
 * Run:
 *   cd web && E2E_WEB=http://127.0.0.1:18090 E2E_SOURCE_SHA=<repo source digest> \
 *     E2E_SERVER_SHA=<Core binary SHA-256> bun e2e/identity-sse.ts
 */
const web = required("E2E_WEB").replace(/\/$/, "");
const outDir = resolve(process.env.E2E_OUT ?? "../artifacts/d2-7b");
const sourceSHA = (process.env.E2E_SOURCE_SHA ?? "").trim();
const serverSHA = (process.env.E2E_SERVER_SHA ?? "").trim();
const executionSHA = (process.env.E2E_EXECUTION_SHA ?? "").trim();
const admin = { username: process.env.E2E_USERNAME ?? "demo-admin", password: process.env.E2E_PASSWORD ?? "admin-demo" };
const viewer = { username: process.env.E2E_VIEWER_USERNAME ?? "demo-viewer", password: process.env.E2E_VIEWER_PASSWORD ?? "viewer-demo" };
const readyTimeout = Number(process.env.E2E_READY_TIMEOUT_MS ?? 20000);
const liveLabel = "运行状态已连接";
const closedLabel = "运行状态流已关闭，不会自动重连；请刷新页面或重新登录后再试。";

interface StreamRecord {
  url: string;
  resource_type: string;
  at: string;
  status?: number;
  ended?: string;
  failure?: string;
}

const streams: StreamRecord[] = [];
const observations: Record<string, unknown> = {};
const screenshots: Array<Record<string, unknown>> = [];

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

async function signIn(page: Page, username: string, password: string): Promise<void> {
  await page.goto(`${web}/login`, { waitUntil: "load" });
  await page.getByLabel("账号", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.locator('input[name="password"]').waitFor({ state: "detached", timeout: readyTimeout });
}

/** Collect the first event ids a persisted stream replays after a cursor. */
async function streamIDs(page: Page, url: string, lastEventID: number, limit: number): Promise<number[]> {
  return await page.evaluate(async ({ url, lastEventID, limit }) => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 5000);
    const ids: number[] = [];
    try {
      const response = await fetch(url, {
        headers: { "Last-Event-ID": String(lastEventID) },
        signal: controller.signal,
        credentials: "include",
      });
      const reader = response.body?.getReader();
      if (!reader) return ids;
      const decoder = new TextDecoder();
      let buffer = "";
      while (ids.length < limit) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        for (const match of buffer.match(/id: (\d+)/g) ?? []) {
          const id = Number(match.slice(4));
          if (Number.isFinite(id) && !ids.includes(id)) ids.push(id);
        }
        buffer = buffer.slice(-64);
        if (ids.length >= limit) break;
      }
    } catch {
      /* the read timer aborts the stream after the first events */
    }
    clearTimeout(timer);
    return ids;
  }, { url, lastEventID, limit });
}

async function capture(page: Page, name: string): Promise<void> {
  const buffer = await page.screenshot({ path: resolve(outDir, name), fullPage: true });
  screenshots.push({ file: name, bytes: buffer.byteLength, sha256: createHash("sha256").update(buffer).digest("hex") });
}

const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.CHROMIUM_BIN || "/usr/bin/chromium",
  args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
});
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await context.newPage();

page.on("request", (request) => {
  if (request.url().includes("/events")) {
    streams.push({ url: request.url(), resource_type: request.resourceType(), at: new Date().toISOString() });
  }
});
page.on("response", (response) => {
  if (response.url().includes("/events")) {
    const record = [...streams].reverse().find((item) => item.url === response.url() && item.status === undefined && item.ended === undefined);
    if (record) record.status = response.status();
  }
});
page.on("requestfinished", (request) => {
  if (request.url().includes("/events")) {
    const record = [...streams].reverse().find((item) => item.url === request.url() && item.ended === undefined);
    if (record) record.ended = new Date().toISOString();
  }
});
page.on("requestfailed", (request) => {
  if (request.url().includes("/events")) {
    const record = [...streams].reverse().find((item) => item.url === request.url() && item.ended === undefined);
    if (record) {
      record.failure = request.failure()?.errorText ?? "request failed";
      record.ended = new Date().toISOString();
    }
  }
});

let exitCode = 0;
try {
  await mkdir(outDir, { recursive: true });

  // 1. Admin signs in through the UI, then sets up a review through the API.
  await signIn(page, admin.username, admin.password);
  observations.admin_login = "ui";
  const session = await page.request.post(`${web}/api/session`, {
    data: { username: admin.username, password: admin.password, audience: "client" },
  });
  assert.equal(session.status(), 200, `admin session returned HTTP ${session.status()}`);
  const created = await page.request.post(`${web}/api/cases`, {
    data: { title: `D2-7b identity/SSE ${Date.now()}`, contract_type: "service",
            business_line: "ENT", scenario: "telecom_dedicated_line" },
  });
  assert.equal(created.status(), 201, `case creation returned HTTP ${created.status()}`);
  const caseID = String((await created.json()).id);
  observations.case_id = caseID;
  for (const [role, filename, text] of [
    ["procurement", "purchase.txt", "采购文件要求质保36个月。"],
    ["draft_contract", "draft.txt", "拟签合同约定质保24个月。"],
  ] as const) {
    const upload = await page.request.post(`${web}/api/cases/${caseID}/documents`, {
      multipart: { role, file: { name: filename, mimeType: "text/plain", buffer: Buffer.from(text, "utf8") } },
    });
    assert.equal(upload.status(), 201, `upload ${role} returned HTTP ${upload.status()}`);
    const documentID = String((await upload.json()).id);
    const parse = await page.request.post(`${web}/api/cases/${caseID}/documents/${documentID}/parse`, { data: {} });
    assert.equal(parse.status(), 200, `parse ${role} returned HTTP ${parse.status()}`);
  }
  const review = await page.request.post(`${web}/api/cases/${caseID}/reviews`, { data: {} });
  assert.ok([200, 201, 202].includes(review.status()), `review acceptance returned HTTP ${review.status()}`);
  const runID = String((await review.json()).id);
  observations.run_id = runID;
  const deadline = Date.now() + readyTimeout;
  let runState = "";
  while (Date.now() < deadline) {
    const workspace = await page.request.get(`${web}/api/cases/${caseID}/reviews/${runID}`);
    runState = String((await workspace.json()).run?.state ?? "");
    if (["completed", "partial", "failed"].includes(runState)) break;
    await page.waitForTimeout(1000);
  }
  assert.ok(["completed", "partial"].includes(runState), `review did not reach a readable terminal state: ${runState}`);
  observations.run_state = runState;
  const ownerWorkspaceResponse = await page.request.get(`${web}/api/cases/${caseID}/reviews/${runID}`);
  assert.equal(ownerWorkspaceResponse.status(), 200, `the owner could not read the review to identify a valid finding (HTTP ${ownerWorkspaceResponse.status()})`);
  const ownerWorkspace = await ownerWorkspaceResponse.json() as { findings?: Array<{ id?: string; version?: number }> };
  const findingID = String(ownerWorkspace.findings?.[0]?.id ?? "");
  const findingVersion = Number(ownerWorkspace.findings?.[0]?.version);
  assert.ok(findingID, "the owner workspace did not return a finding identity");
  assert.ok(Number.isInteger(findingVersion) && findingVersion > 0, "the owner workspace did not return a valid finding version");
  observations.owner_read_finding = { finding_id: findingID, version: findingVersion, status: ownerWorkspaceResponse.status() };


  // 2. The workspace page opens its own stream.
  await page.goto(`${web}/cases/${caseID}/reviews/${runID}`, { waitUntil: "load" });
  await page.locator(".workspace-grid").waitFor({ state: "visible", timeout: readyTimeout });
  await page.getByText(liveLabel, { exact: false }).first().waitFor({ state: "visible", timeout: readyTimeout });
  const activeEventStreamsBeforeRevocation = streams.filter((item) => item.resource_type === "eventsource" && item.ended === undefined);
  assert.ok(activeEventStreamsBeforeRevocation.length >= 1, "the workspace never subscribed to the run event stream");
  observations.streams_before_revocation = [...activeEventStreamsBeforeRevocation];

  // 2b. Persistent cursor: a replayed stream must start strictly after Last-Event-ID.
  const eventsURL = `${web}/api/cases/${caseID}/reviews/${runID}/events`;
  const firstIDs = await streamIDs(page, eventsURL, 0, 1);
  const firstEventID = firstIDs[0];
  const resumedIDs = firstEventID === undefined ? [] : await streamIDs(page, eventsURL, firstEventID, 1);
  const resumedFirstID = resumedIDs[0];
  const cursorResume = {
    first_event_id: firstEventID ?? null,
    resumed_first_id: resumedFirstID ?? null,
    strictly_after: firstEventID !== undefined && resumedFirstID !== undefined && resumedFirstID > firstEventID,
  };
  observations.cursor_resume = cursorResume;
  assert.ok(cursorResume.strictly_after,
            `the persisted stream did not resume strictly after the cursor: ${JSON.stringify(cursorResume)}`);

  // 2c. Per-resource authorization: a case without the viewer grant stays private.
  const privateCase = await page.request.post(`${web}/api/cases`, {
    data: { title: `D2-7b private ${Date.now()}`, contract_type: "service",
            business_line: "ENT", scenario: "telecom_dedicated_line" },
  });
  assert.equal(privateCase.status(), 201, `private case creation returned HTTP ${privateCase.status()}`);
  const privateCaseID = String((await privateCase.json()).id);
  const adminPrivateRead = await page.request.get(`${web}/api/cases/${privateCaseID}`);
  const perResource: Record<string, unknown> = { private_case_id: privateCaseID, admin_read: adminPrivateRead.status() };
  observations.per_resource = perResource;
  assert.equal(adminPrivateRead.status(), 200, `the owner could not read its private case (HTTP ${adminPrivateRead.status()})`);

  // 3. Server-side revocation while the stream is open.
  const logout = await page.request.delete(`${web}/api/session`);
  assert.ok([200, 204].includes(logout.status()), `logout returned HTTP ${logout.status()}`);
  const closeDeadline = Date.now() + 15000;
  let streamEnded = false;
  let observedLabel = "";
  let findingsCleared = false;
  let loginVisible = false;
  while (Date.now() < closeDeadline) {
    streamEnded = activeEventStreamsBeforeRevocation.some((item) => item.ended !== undefined);
    observedLabel = await page.evaluate(() => {
      const node = Array.from(document.querySelectorAll("span")).find((item) =>
        (item.textContent ?? "").includes("运行状态"));
      return node?.textContent ?? "";
    }).catch(() => "");
    findingsCleared = (await page.locator(".finding-button").count()) === 0;
    loginVisible = await page.getByRole("heading", { name: "登录合同审查", exact: true }).isVisible();
    if (streamEnded && findingsCleared && (observedLabel === closedLabel || loginVisible)) break;
    await page.waitForTimeout(500);
  }
  observations.revocation = { stream_ended: streamEnded, observed_stream_label: observedLabel,
                              findings_cleared: findingsCleared, login_visible: loginVisible,
                              streams_after_revocation: [...streams] };
  const revokedRead = await page.request.get(`${web}/api/cases/${caseID}`);
  observations.revoked_read_status = revokedRead.status();
  assert.ok([401, 403].includes(revokedRead.status()), `revoked read returned HTTP ${revokedRead.status()}`);
  const findingsVisible = await page.locator(".finding-button").count();
  observations.findings_visible_after_revocation = findingsVisible;
  await capture(page, "revoked-1440x900.png");
  assert.equal(findingsVisible, 0, "cached findings remained visible after revocation");
  assert.ok(streamEnded, "the open event stream did not end after server-side revocation");
  assert.ok(observedLabel === closedLabel || loginVisible, "revocation neither closed the stream in the workspace nor redirected to login");

  // 4. Identity switch: fresh reads must replace or deny the former identity's data.
  const eventStreamCountBeforeSwitch = streams.filter((item) => item.resource_type === "eventsource").length;
  await signIn(page, viewer.username, viewer.password);
  const viewerSessionResponse = await page.request.get(`${web}/api/session`);
  assert.equal(viewerSessionResponse.status(), 200, `the switched identity session returned HTTP ${viewerSessionResponse.status()}`);
  const viewerSession = await viewerSessionResponse.json() as { actor?: { id?: string; role?: string }; audience?: string };
  const viewerActor = viewerSession.actor ?? {};
  assert.equal(viewerActor.id, viewer.username, "the browser did not switch to the read-only identity");
  assert.equal(viewerSession.audience, "client", "the switched identity retained the wrong session audience");
  const workspaceURL = `${web}/api/cases/${caseID}/reviews/${runID}`;
  const workspaceResponsePromise = page.waitForResponse((response) =>
    response.request().method() === "GET" && response.url().startsWith(workspaceURL), { timeout: readyTimeout },
  );
  await page.goto(`${web}/cases/${caseID}/reviews/${runID}`, { waitUntil: "load" });
  const viewerWorkspaceResponse = await workspaceResponsePromise;
  const workspaceReadStatus = viewerWorkspaceResponse.status();
  assert.ok([200, 403, 404].includes(workspaceReadStatus), `the switched identity workspace read returned HTTP ${workspaceReadStatus}`);
  let viewerClaims: string[] = [];
  if (workspaceReadStatus === 200) {
    await page.locator(".workspace-grid").waitFor({ state: "visible", timeout: readyTimeout });
    await page.getByText(liveLabel, { exact: false }).first().waitFor({ state: "visible", timeout: readyTimeout });
    const viewerWorkspace = await viewerWorkspaceResponse.json() as { findings?: Array<{ claim?: string }> };
    viewerClaims = (await page.locator(".finding-button").allTextContents()).map((claim) => claim.trim());
    const apiClaims = (viewerWorkspace.findings ?? []).map((finding) => finding.claim ?? "");
    assert.deepEqual(viewerClaims, apiClaims, "the switched identity rendered cached workspace content instead of its fresh response");
  } else {
    await page.locator(".page-status").waitFor({ state: "visible", timeout: readyTimeout });
    assert.deepEqual(await page.locator(".finding-button").allTextContents(), [], "a denied identity retained former workspace findings");
  }
  const streamsAfterSwitch = streams.filter((item) => item.resource_type === "eventsource").length;
  const decisionButton = page.getByRole("button", { name: "保存决定", exact: true });
  const viewerCanSubmitDecision = (await decisionButton.count()) > 0 && await decisionButton.isEnabled();
  const viewerState = await page.evaluate(() => ({
    page_status: document.querySelector(".page-status")?.className ?? "",
    signals: Array.from(document.querySelectorAll(".finding-button")).length,
    stream_label: Array.from(document.querySelectorAll("span"))
      .map((item) => item.textContent ?? "").find((text) => text.includes("运行状态")) ?? "",
  }));
  observations.identity_switch = {
    session_actor_id: viewerActor.id, session_role: viewerActor.role ?? null,
    streams_after_switch: streamsAfterSwitch, viewer_dom: viewerState,
    workspace_read_status: workspaceReadStatus,
    cache_matches_fresh_response: workspaceReadStatus === 200,
    stale_content_cleared_on_denial: workspaceReadStatus !== 200 && viewerClaims.length === 0,
    viewer_can_submit_decision: viewerCanSubmitDecision,
  };
  if (workspaceReadStatus === 200) {
    assert.ok(streamsAfterSwitch > eventStreamCountBeforeSwitch, "the new identity did not subscribe a fresh event stream");
  } else {
    assert.equal(streamsAfterSwitch, eventStreamCountBeforeSwitch, "an identity without case access subscribed to its event stream");
  }
  assert.equal(viewerCanSubmitDecision, false, "the read-only identity retained a decision affordance");
  const viewerPrivateRead = await page.request.get(`${web}/api/cases/${privateCaseID}`);
  perResource.viewer_read = viewerPrivateRead.status();
  assert.ok([403, 404].includes(viewerPrivateRead.status()),
            `a non-member identity read a private case (HTTP ${viewerPrivateRead.status()})`);
  const viewerDecision = await page.request.post(
    `${web}/api/cases/${caseID}/reviews/${runID}/findings/${encodeURIComponent(findingID)}/decisions`,
    { data: { action: "confirm_risk", reason: "identity boundary probe", expected_version: findingVersion } });
  observations.viewer_decision_status = viewerDecision.status();
  assert.equal(viewerDecision.status(), 403,
               `an authenticated viewer's write to valid finding ${findingID} returned HTTP ${viewerDecision.status()} instead of 403`);
  await capture(page, "viewer-after-switch-1440x900.png");

  console.log(JSON.stringify({
    result: "browser identity switch, SSE revocation, cursor resume and per-resource permissions captured",
    source_sha: sourceSHA === "" ? "NOT_RECORDED" : sourceSHA,
    server_sha: serverSHA === "" ? "NOT_RECORDED" : serverSHA,
    execution_sha: executionSHA === "" ? "NOT_RECORDED" : executionSHA,
    case_id: caseID, run_id: runID, run_state: runState,
    streams: streams.length, revoked_read_status: revokedRead.status(),
    viewer_decision_status: viewerDecision.status(),
  }, null, 2));
} catch (error) {
  exitCode = 1;
  observations.error = error instanceof Error ? error.message : String(error);
  console.error(`FAIL: ${observations.error}`);
} finally {
  await context.close();
  await browser.close();
  await mkdir(outDir, { recursive: true });
  await writeFile(resolve(outDir, "identity-sse.json"), `${JSON.stringify({
    captured_at: new Date().toISOString(),
    source_sha: sourceSHA === "" ? "NOT_RECORDED" : sourceSHA,
    server_sha: serverSHA === "" ? "NOT_RECORDED" : serverSHA,
    execution_sha: executionSHA === "" ? "NOT_RECORDED" : executionSHA,
    browser: "Chromium",
    executable: process.env.CHROMIUM_BIN || "/usr/bin/chromium",
    web,
    streams,
    observations,
    screenshots,
    result: exitCode === 0 ? "passed" : "failed",
  }, null, 2)}\n`, "utf8");
}
process.exit(exitCode);
