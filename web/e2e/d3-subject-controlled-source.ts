import assert from "node:assert/strict";
import { realpath, writeFile } from "node:fs/promises";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium, type Browser, type BrowserContext, type Locator, type Page } from "playwright";
import { chromiumExecutable } from "./chromium";
import { chooseAntOption } from "./c1b-select";

const web = required("E2E_WEB").replace(/\/$/, "");
const caseA = required("E2E_D3_CASE_A");
const caseB = required("E2E_D3_CASE_B");
const fixtureKey = required("E2E_D3_FIXTURE_KEY");
const evidenceRoot = required("E2E_D3_OUT");
const subjectName = "D3示例网络服务有限公司";
const caseATitle = "D3主体核验案件甲";
const caseBTitle = "D3主体核验案件乙";
const trace: Array<{ step: string; detail?: unknown }> = [];

function step(name: string, detail?: unknown): void {
  trace.push({ step: name, detail });
  console.log(`[d3] ${name}${detail === undefined ? "" : ` ${JSON.stringify(detail)}`}`);
}

async function signIn(page: Page, username: string, credential: string): Promise<void> {
  await page.getByLabel("账号", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(credential);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("button", { name: "退出", exact: true }).waitFor({ state: "visible" });
}

async function login(browser: Browser, username: string, credential: string): Promise<{ context: BrowserContext; page: Page }> {
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, timezoneId: "Asia/Shanghai" });
  const page = await context.newPage();
  page.setDefaultTimeout(12_000);
  await page.goto(`${web}/login`);
  await signIn(page, username, credential);
  return { context, page };
}

async function logout(page: Page): Promise<void> {
  await page.getByRole("button", { name: "退出", exact: true }).click();
  await page.getByRole("heading", { name: "登录合同审查", exact: true }).waitFor({ state: "visible" });
}

async function navigateCase(page: Page, caseId: string, title: string): Promise<void> {
  await page.goto(`${web}/cases/${encodeURIComponent(caseId)}`);
  await page.getByRole("heading", { name: title, exact: true }).waitFor({ state: "visible" });
  await page.locator(".case-subject-roles-panel").getByText("案件主体与角色", { exact: true }).waitFor({ state: "visible" });
}

function binding(page: Page): Locator {
  return page.locator(".case-subject-binding-row").filter({ has: page.getByText(subjectName, { exact: true }) });
}

async function assertBinding(page: Page, version: number, state: string): Promise<void> {
  const row = binding(page);
  await row.waitFor({ state: "visible" });
  await row.getByText(new RegExp(`身份 v${version} · 绑定 v\\d+`)).waitFor({ state: "visible" });
  await row.getByText(state, { exact: true }).waitFor({ state: "visible" });
}

async function assertPinnedSnapshot(page: Page, snapshotId: string, currentSourceVersion = 1): Promise<void> {
  const url = new URL(page.url());
  assert.equal(url.searchParams.get("source_case"), caseA);
  assert.equal(url.searchParams.get("source_snapshot"), snapshotId);
  const panel = page.locator(".subject-source-verification-panel");
  await panel.getByText(`D3受控合成主体目录 · 来源版本 v${currentSourceVersion} · 受控来源（合成演示）`, { exact: true }).waitFor({ state: "visible" });
  await panel.getByText("固定记录 1 条", { exact: true }).waitFor({ state: "visible" });
  await panel.getByText(/D3受控合成主体目录 · 来源 v1 · 快照时间/).waitFor({ state: "visible" });
}

async function assertProposerCannotDecide(page: Page): Promise<void> {
  await page.getByText("更正提出人不能批准或拒绝同一更正", { exact: true }).waitFor({ state: "visible" });
  assert.equal(await page.getByRole("button", { name: "批准并生效", exact: true }).count(), 0, "proposer was offered self-approval");
  assert.equal(await page.getByRole("button", { name: "驳回申请", exact: true }).count(), 0, "proposer was offered a decision");
}

// Determine character positions from the rendered text, then select with real
// pointer events so React handles the same mouseup as it does for a user.
async function selectText(page: Page, pre: Locator, quote: string): Promise<void> {
  await pre.scrollIntoViewIfNeeded();
  const points = await pre.evaluate((element, needle) => {
    const node = element.firstChild;
    if (!node || node.nodeType !== Node.TEXT_NODE) return null;
    const text = node.textContent ?? "";
    const start = text.indexOf(needle);
    if (start < 0 || text.indexOf(needle, start + 1) >= 0) return null;
    const range = document.createRange();
    range.setStart(node, start);
    range.setEnd(node, start + 1);
    const first = range.getBoundingClientRect();
    range.setStart(node, start + needle.length - 1);
    range.setEnd(node, start + needle.length);
    const last = range.getBoundingClientRect();
    if (!first.width || !last.width) return null;
    return {
      from: { x: first.left + first.width * 0.1, y: first.top + first.height / 2 },
      to: { x: last.left + last.width * 0.9, y: last.top + last.height / 2 },
    };
  }, quote);
  assert.ok(points, "fixed material quote was not uniquely rendered");
  await page.mouse.move(points.from.x, points.from.y);
  await page.mouse.down();
  await page.mouse.move(points.to.x, points.to.y, { steps: 10 });
  await page.mouse.up();
  await page.locator(".subject-anchor-selected blockquote").getByText(quote, { exact: true }).waitFor({ state: "visible" });
}

async function assertExternalEvidenceDirectory(): Promise<string> {
  const root = await realpath(fileURLToPath(new URL("../..", import.meta.url)));
  const output = await realpath(evidenceRoot);
  const fromRoot = relative(root, output);
  assert.ok(fromRoot === ".." || fromRoot.startsWith(`..${sep}`), "browser evidence must be outside the source checkout");
  return output;
}

async function run(): Promise<void> {
  const output = await assertExternalEvidenceDirectory();
  const browser = await chromium.launch({ executablePath: chromiumExecutable(), headless: true, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
  const contexts: BrowserContext[] = [];
  const failures: string[] = [];
  try {
    const adminLogin = await login(browser, "demo-admin", "admin-demo");
    contexts.push(adminLogin.context);
    const admin = adminLogin.page;
    await navigateCase(admin, caseA, caseATitle);
    await chooseAntOption(admin, admin.getByLabel("案件角色", { exact: true }), (label) => label === "供应商");
    await admin.getByLabel("主体名称", { exact: true }).fill(subjectName);
    await admin.getByLabel("统一社会信用代码（可选）", { exact: true }).fill("91350100M000100Y43");
    await admin.getByRole("button", { name: "登记主体并添加角色", exact: true }).click();
    await assertBinding(admin, 1, "待核验");
    step("subject registered through the case form and remains pending verification");

    const sourcePanel = admin.locator(".subject-source-verification-panel");
    await sourcePanel.getByText(/不代表真实工商或主体身份核验/).waitFor({ state: "visible" });
    await sourcePanel.getByRole("button", { name: "固定新的合成来源版本", exact: true }).click();
    await admin.waitForURL((url) => url.searchParams.get("source_case") === caseA && !!url.searchParams.get("source_snapshot"));
    const snapshotURL = admin.url();
    const snapshotId = new URL(snapshotURL).searchParams.get("source_snapshot");
    assert.ok(snapshotId, "snapshot was not fixed in the case URL");
    await assertPinnedSnapshot(admin, snapshotId);
    await admin.reload();
    await assertPinnedSnapshot(admin, snapshotId);
    await logout(admin);
    await signIn(admin, "demo-admin", "admin-demo");
    await admin.goto(snapshotURL);
    await assertPinnedSnapshot(admin, snapshotId);
    step("versioned synthetic source snapshot survived refresh and sign-out/sign-in", { snapshot_id: snapshotId });

    // Advance the configured synthetic source to a different identifier. The
    // v1 snapshot must still match the original subject after v2 is available.
    const updateSource = await adminLogin.context.request.post(`${web}/api/__d3_fixture__/subject-source-v2`, {
      headers: { Origin: web, "X-D3-Fixture-Key": fixtureKey },
    });
    assert.equal(updateSource.status(), 204, "could not prepare the next synthetic source version");
    await admin.reload();
    await assertPinnedSnapshot(admin, snapshotId, 2);
    await sourcePanel.getByRole("button", { name: "固定新的合成来源版本", exact: true }).click();
    await admin.waitForURL((url) => !!url.searchParams.get("source_snapshot") && url.searchParams.get("source_snapshot") !== snapshotId);
    const snapshotV2 = new URL(admin.url()).searchParams.get("source_snapshot");
    assert.ok(snapshotV2 && snapshotV2 !== snapshotId, "new source version did not create a distinct snapshot");
    await sourcePanel.getByText(/D3受控合成主体目录 · 来源 v2 · 快照时间/).waitFor({ state: "visible" });
    await chooseAntOption(admin, admin.getByLabel("选择已有合成来源快照", { exact: true }), (label) => label.startsWith("D3受控合成主体目录 · 来源 v1 · 固定于 "));
    await assertPinnedSnapshot(admin, snapshotId, 2);
    await admin.reload();
    await assertPinnedSnapshot(admin, snapshotId, 2);
    step("source v2 and a second snapshot left the selected v1 snapshot frozen", { snapshot_v1: snapshotId, snapshot_v2: snapshotV2 });

    // Bind before proposing: changing affected bindings after proposal makes its
    // captured impact stale and must not be mistaken for an approval scenario.
    await navigateCase(admin, caseB, caseBTitle);
    await chooseAntOption(admin, admin.getByLabel("案件角色", { exact: true }), (label) => label === "供应商");
    await chooseAntOption(admin, admin.getByLabel("主体来源方式", { exact: true }), (label) => label === "绑定已存在主体");
    await admin.getByLabel("搜索可见主体", { exact: true }).fill(subjectName);
    await chooseAntOption(admin, admin.getByLabel("选择已存在主体", { exact: true }), (label) => label === `${subjectName} · v1 · 待核验`);
    await admin.getByRole("button", { name: "绑定选中的主体", exact: true }).click();
    await admin.getByText("已将你选择的主体绑定到本案；同角色的其他绑定保持不变。身份仍按服务端状态显示。", { exact: true }).waitFor({ state: "visible" });
    await assertBinding(admin, 1, "待核验");
    const foreignSnapshotURL = `${web}/cases/${encodeURIComponent(caseB)}?source_case=${encodeURIComponent(caseB)}&source_snapshot=${encodeURIComponent(snapshotId)}`;
    await admin.goto(foreignSnapshotURL);
    await admin.locator(".subject-source-verification-panel").getByText("所选合成来源快照暂不可用", { exact: true }).waitFor({ state: "visible" });
    assert.equal(await admin.getByRole("button", { name: "对照合成来源并发起审批", exact: true }).count(), 0, "a snapshot from another case became selectable");
    step("the second case cannot use the first case's source snapshot");

    await admin.goto(snapshotURL);
    await assertPinnedSnapshot(admin, snapshotId, 2);
    await chooseAntOption(admin, admin.getByLabel("选择待对照的案件主体", { exact: true }), (label) => label === `${subjectName} · 供应商 · 身份 v1`);
    await admin.getByRole("button", { name: "对照合成来源并发起审批", exact: true }).click();
    await admin.getByRole("heading", { name: "主体更正复核", exact: true }).waitFor({ state: "visible" });
    const sourceChangeURL = admin.url();
    const sourceChangeId = new URL(sourceChangeURL).pathname.split("/").pop();
    assert.ok(sourceChangeId, "source matching did not open a change request");
    await admin.getByText("待复核", { exact: true }).waitFor({ state: "visible" });
    const sourceBasis = admin.locator(".subject-change-basis-card");
    await sourceBasis.getByText("受控来源对照（合成演示）", { exact: true }).waitFor({ state: "visible" });
    await sourceBasis.getByText(/快照固定时间 .* · 来源版本 v1/).waitFor({ state: "visible" });
    const before = admin.locator(".subject-change-projection").filter({ hasText: "更正前" });
    const after = admin.locator(".subject-change-projection").filter({ hasText: "申请更正为" });
    await before.getByText("待核验", { exact: true }).waitFor({ state: "visible" });
    await after.getByText("已对照核验（受控来源，合成演示）", { exact: true }).waitFor({ state: "visible" });
    await assertProposerCannotDecide(admin);
    step("controlled-source match produced a pending proposal and blocked its proposer", { request_id: sourceChangeId });

    const reviewerLogin = await login(browser, "demo-reviewer", "reviewer-demo");
    contexts.push(reviewerLogin.context);
    const reviewer = reviewerLogin.page;
    await reviewer.goto(sourceChangeURL);
    await reviewer.getByRole("heading", { name: "主体更正复核", exact: true }).waitFor({ state: "visible" });
    const partialImpact = reviewer.locator(".subject-change-impact-card");
    await partialImpact.getByText("当前账号可见影响案件：1 项。隐藏案件信息不会显示。", { exact: true }).waitFor({ state: "visible" });
    await partialImpact.getByRole("link", { name: caseATitle, exact: true }).waitFor({ state: "visible" });
    assert.equal(await partialImpact.getByText(caseBTitle, { exact: true }).count(), 0, "hidden affected case title leaked");
    await reviewer.getByText("当前授权不足以决定全部受影响案件", { exact: true }).waitFor({ state: "visible" });
    assert.equal(await reviewer.getByRole("button", { name: "批准并生效", exact: true }).count(), 0, "partially authorized reviewer was offered approval");
    await reviewer.goto(`${web}/cases/${encodeURIComponent(caseB)}`);
    await reviewer.getByText("案件没有加载", { exact: true }).waitFor({ state: "visible" });
    assert.equal(await reviewer.getByRole("heading", { name: caseBTitle, exact: true }).count(), 0, "reviewer opened a case without membership");
    step("reviewer with only one affected case cannot see or decide the hidden case");

    // Membership is fixture setup: there is no case-member editor in the UI.
    // Keep Origin explicit for the cookie-authenticated write after #29.
    const grant = await adminLogin.context.request.post(`${web}/api/cases/${encodeURIComponent(caseB)}/members`, {
      headers: { Origin: web }, data: { actor_id: "demo-reviewer" },
    });
    assert.equal(grant.status(), 200, "could not prepare the second synthetic case grant");
    await reviewer.goto(sourceChangeURL);
    const fullImpact = reviewer.locator(".subject-change-impact-card");
    await fullImpact.getByText("当前账号可见影响案件：2 项。隐藏案件信息不会显示。", { exact: true }).waitFor({ state: "visible" });
    await fullImpact.getByRole("link", { name: caseATitle, exact: true }).waitFor({ state: "visible" });
    await fullImpact.getByRole("link", { name: caseBTitle, exact: true }).waitFor({ state: "visible" });
    await reviewer.getByLabel("复核理由", { exact: true }).fill("受控来源快照与本案主体标识一致；按双人复核流程批准。");
    await reviewer.getByRole("button", { name: "批准并生效", exact: true }).click();
    await reviewer.getByText("更正已批准并由服务端追加新版本。", { exact: true }).waitFor({ state: "visible" });
    await navigateCase(admin, caseA, caseATitle);
    await assertBinding(admin, 2, "已对照核验（受控来源，合成演示）");
    step("independent reviewer with both case grants approved the source proposal");

    await chooseAntOption(admin, admin.getByLabel("更正目标类型", { exact: true }), (label) => label === "主体身份资料");
    await chooseAntOption(admin, admin.getByLabel("选择本案主体", { exact: true }), (label) => label === `${subjectName} · 供应商 · 身份 v2`);
    await chooseAntOption(admin, admin.getByLabel("统一社会信用代码更正方式", { exact: true }), (label) => label === "更正统一社会信用代码");
    await admin.getByLabel("更正后统一社会信用代码", { exact: true }).fill("91350100M000100Y44");
    await chooseAntOption(admin, admin.getByLabel("选择固定材料版本", { exact: true }), (label) => label === "附件 · 其他附件 · v1");
    await chooseAntOption(admin, admin.getByLabel("选择材料固定解析版本", { exact: true }), (label) => label.startsWith("解析 ") && label.includes(" · 可引用 · "));
    const quote = "统一社会信用代码更正为91350100M000100Y44";
    await selectText(admin, admin.getByLabel("固定材料原文，可选择作为更正依据", { exact: true }), quote);
    await admin.getByLabel("申请说明", { exact: true }).fill("依据本案固定材料所载标识提出更正，仍需独立复核。");
    await admin.getByRole("button", { name: "提交更正申请", exact: true }).click();
    await admin.getByText("更正申请已提交，等待具备复核权限的其他人员处理。身份不会因提交申请而自动变为已核验。", { exact: true }).waitFor({ state: "visible" });
    const correctionRow = admin.locator(".case-subject-corrections-panel .subject-history-row").filter({ hasText: "主体身份更正 · 目标身份 v2" });
    await correctionRow.getByRole("link", { name: "查看更正前后与依据", exact: true }).click();
    await admin.getByRole("heading", { name: "主体更正复核", exact: true }).waitFor({ state: "visible" });
    const correctionURL = admin.url();
    const correctionId = new URL(correctionURL).pathname.split("/").pop();
    assert.ok(correctionId && correctionId !== sourceChangeId, "identifier correction did not open its own proposal");
    const materialBasis = admin.locator(".subject-change-basis-card");
    await materialBasis.getByText("subject-source-basis.txt", { exact: true }).waitFor({ state: "visible" });
    await materialBasis.getByText("材料版本 v1", { exact: true }).waitFor({ state: "visible" });
    await materialBasis.getByText(quote, { exact: true }).waitFor({ state: "visible" });
    await assertProposerCannotDecide(admin);
    await reviewer.goto(correctionURL);
    await reviewer.locator(".subject-change-basis-card").getByText(quote, { exact: true }).waitFor({ state: "visible" });
    await reviewer.getByLabel("复核理由", { exact: true }).fill("已核对本案冻结原文与更正内容，按双人复核流程批准。");
    await reviewer.getByRole("button", { name: "批准并生效", exact: true }).click();
    await reviewer.getByText("更正已批准并由服务端追加新版本。", { exact: true }).waitFor({ state: "visible" });
    await navigateCase(admin, caseA, caseATitle);
    await assertBinding(admin, 3, "待核验");
    await navigateCase(admin, caseB, caseBTitle);
    await assertBinding(admin, 3, "待核验");
    step("identifier correction used a fixed material quote and appended a pending version in both cases", { request_id: correctionId });

    await admin.getByRole("link", { name: "打开主体台账", exact: true }).click();
    await admin.getByRole("heading", { name: "主体台账", exact: true }).waitFor({ state: "visible" });
    await admin.getByRole("link", { name: subjectName, exact: true }).click();
    await admin.getByRole("heading", { name: subjectName, exact: true }).waitFor({ state: "visible" });
    const subjectURL = admin.url();
    const history = admin.locator(".subject-history-card");
    const v1 = history.locator(".subject-history-row").filter({ has: admin.getByText("身份 v1", { exact: true }) });
    const v2 = history.locator(".subject-history-row").filter({ has: admin.getByText("身份 v2", { exact: true }) });
    const v3 = history.locator(".subject-history-row").filter({ has: admin.getByText("身份 v3", { exact: true }) });
    await v1.getByText(/待核验/).waitFor({ state: "visible" });
    await v2.getByText(/已对照核验（受控来源，合成演示）/).waitFor({ state: "visible" });
    await v3.getByText(/待核验/).waitFor({ state: "visible" });
    for (const requestId of [sourceChangeId, correctionId]) {
      const href = `/cases/${encodeURIComponent(caseA)}/subject-changes/${encodeURIComponent(requestId)}`;
      const event = history.locator(".subject-history-row").filter({ hasText: "更正已批准" }).filter({ has: admin.locator(`a[href="${href}"]`) });
      await event.waitFor({ state: "visible" });
    }
    await admin.reload();
    await v2.getByText(/已对照核验（受控来源，合成演示）/).waitFor({ state: "visible" });
    await v3.getByText(/待核验/).waitFor({ state: "visible" });
    await logout(reviewer);
    await signIn(reviewer, "demo-reviewer", "reviewer-demo");
    await reviewer.goto(subjectURL);
    await reviewer.locator(".subject-history-card").getByText("身份 v3", { exact: true }).waitFor({ state: "visible" });
    step("visible identity history preserves both approvals and versions across refresh and re-login");

    await admin.screenshot({ path: join(output, "d3-subject-history.png"), fullPage: true });
    await writeFile(join(output, "d3-subject-controlled-source.json"), `${JSON.stringify({
      assertions: "COMPLETE", case_ids: [caseA, caseB], source_snapshot_id: snapshotId,
      source_change_request_id: sourceChangeId, identifier_change_request_id: correctionId, trace,
    }, null, 2)}\n`, "utf8");
  } catch (error) {
    failures.push(error instanceof Error ? error.stack ?? error.message : String(error));
    throw error;
  } finally {
    await writeFile(join(output, "d3-subject-trace.json"), `${JSON.stringify({ trace, failures }, null, 2)}\n`, "utf8");
    await Promise.all(contexts.map((context) => context.close()));
    await browser.close();
  }
}

function required(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}

void run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
