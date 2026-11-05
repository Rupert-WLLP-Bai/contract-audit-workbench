import { execFileSync } from "node:child_process";
import { test, expect, type Page, type TestInfo } from "playwright/test";
import { attemptId, caseId, installNavigationFixture, mountNestedNavigation, runId } from "./issue-233-fixtures";

const web = process.env.E2E_WEB ?? "http://127.0.0.1:4177";
const casePath = `/cases/${caseId}`;

async function evidence(page: Page, info: TestInfo, name: string, id: string) {
  const result = await page.evaluate((targetId) => {
    const target = document.getElementById(targetId)!;
    const header = document.querySelector<HTMLElement>(".topbar");
    const rect = (element: Element | null) => {
      if (!element) return null;
      const value = element.getBoundingClientRect();
      return { top: value.top, bottom: value.bottom, left: value.left, right: value.right };
    };
    const parents: Array<{ id: string; open: boolean }> = [];
    for (let node: Element | null = target; node; node = node.parentElement) {
      if (node instanceof HTMLDetailsElement) parents.push({ id: node.id, open: node.open });
    }
    return { viewport: { width: innerWidth, height: innerHeight, devicePixelRatio, visualScale: visualViewport?.scale },
      activeId: (document.activeElement as HTMLElement | null)?.id, target: rect(target), header: rect(header), parents };
  }, id);
  console.log(`EVIDENCE ${name} ${JSON.stringify({ browser: page.context().browser()?.version(), ...result })}`);
  await info.attach(`${name}.json`, { body: JSON.stringify(result, null, 2), contentType: "application/json" });
  expect(result.parents.every((parent) => parent.open)).toBe(true);
  expect(result.activeId).toBe(id);
  expect(result.target).not.toBeNull();
  if (result.header) expect(result.target!.top).toBeGreaterThanOrEqual(result.header.bottom - 1);
  expect(result.target!.top).toBeLessThan(result.viewport.height);
}

test("case anchors handle initial links, repeat activation, reload and browser history", async ({ page }, info) => {
  await installNavigationFixture(page);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto(`${web}${casePath}#remediation-title`);
  await expect(page.locator("#remediation-title")).toBeFocused();
  await evidence(page, info, "initial-remediation", "remediation-title");
  await page.getByRole("link", { name: "跳转到审查报告与归档" }).click();
  await expect(page.locator("#report-archive-title")).toBeFocused();
  await evidence(page, info, "report-click", "report-archive-title");
  await page.locator("#case-report-archive > summary").click();
  await expect(page.locator("#case-report-archive")).not.toHaveAttribute("open", "");
  await page.getByRole("link", { name: "跳转到审查报告与归档" }).click();
  await expect(page.locator("#report-archive-title")).toBeFocused();
  await page.goBack();
  await expect(page.locator("#remediation-title")).toBeFocused();
  await page.goForward();
  await expect(page.locator("#report-archive-title")).toBeFocused();
  await page.reload();
  await expect(page.locator("#report-archive-title")).toBeFocused();
  await evidence(page, info, "report-reload", "report-archive-title");
  await page.goto(`${web}${casePath}#no-such-section`);
  await expect(page.getByRole("alert").filter({ hasText: "无法找到或读取所选分区" })).toBeVisible();
});

test("controlled nested sections open and a legal target waits for its data", async ({ page }, info) => {
  const fixture = await installNavigationFixture(page);
  fixture.holdLegal = true;
  try {
    await page.goto(`${web}${casePath}#legal-fact-seller_kind`);
    await expect(page.getByRole("heading", { name: "导航样本 37" })).toBeVisible();
    await expect(page.locator("#legal-fact-seller_kind")).toHaveCount(0);
    await expect(page.getByText("无法找到或读取所选分区，请返回页面目录或重试。")).toHaveCount(0);
    fixture.releaseLegal();
    const seller = page.locator("#legal-fact-seller_kind");
    await expect(seller).toHaveAttribute("open", "");
    await expect(seller.locator(":scope > summary")).toBeFocused();

    await page.evaluate(async () => {
      const host = document.createElement("div");
      host.id = "nested-test-host";
      document.body.appendChild(host);
      const load = new Function("path", "return import(path)") as (path: string) => Promise<{ mountNestedNavigation: typeof mountNestedNavigation }>;
      const fixtureModule = await load("/e2e/functional/issue-233-fixtures.ts");
      fixtureModule.mountNestedNavigation(host);
    });
    const link = page.getByRole("link", { name: "跳转到嵌套目标" });
    await link.focus();
    await page.keyboard.press("Enter");
    await expect(page.locator("#nested-heading")).toBeFocused();
    await expect(page.locator("#nested-outer")).toHaveAttribute("open", "");
    await expect(page.locator("#nested-inner")).toHaveAttribute("open", "");
    await evidence(page, info, "nested-keyboard", "nested-heading");
  } finally {
    fixture.releaseLegal();
  }
});

test("the next step opens its collapsed report target by keyboard", async ({ page }, info) => {
  const fixture = await installNavigationFixture(page);
  fixture.archived = true;
  await page.goto(`${web}${casePath}`);
  const next = page.getByRole("link", { name: "查看已有报告与归档记录" });
  await next.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#report-archive-title")).toBeFocused();
  await evidence(page, info, "next-step-keyboard", "report-archive-title");
});

test("list, review, citation and return retain selection and actual reading positions", async ({ page }, info) => {
  const fixture = await installNavigationFixture(page);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto(`${web}/cases?q=${encodeURIComponent("导航样本")}&sort=title&direction=asc&page=2&page_size=25`);
  const row = page.getByRole("link", { name: "导航样本 37", exact: true });
  await row.scrollIntoViewIfNeeded();
  const listScroll = await page.evaluate(() => scrollY);
  expect(listScroll).toBeGreaterThan(0);
  await row.click();
  const next = page.getByRole("link", { name: "核对原文并处理决定" });
  await next.scrollIntoViewIfNeeded();
  const caseScroll = await page.evaluate(() => scrollY);
  await next.click();
  await expect(page).toHaveURL(new RegExp(`/cases/${caseId}/reviews/${runId}`));
  const queue = page.getByRole("region", { name: "人工办理" });
  await queue.getByRole("button", { name: "查看判断与原文" }).click();
  await expect(page).toHaveURL(/work_kind=finding/);
  const pane = page.locator(".page-review .col-doc");
  const findingsPane = page.locator(".page-review .col-findings");
  await expect(pane).toBeVisible();
  await expect(findingsPane).toBeVisible();
  await pane.evaluate((element) => { element.scrollTop = 600; });
  await findingsPane.evaluate((element) => { element.scrollTop = 150; });
  const documentScroll = await pane.evaluate((element) => element.scrollTop);
  const findingScroll = await findingsPane.evaluate((element) => element.scrollTop);
  expect(documentScroll).toBeGreaterThan(0);
  expect(findingScroll).toBeGreaterThan(0);
  await page.getByRole("button", { name: "读取本次冻结原文" }).first().click();
  await expect(page.locator(".frozen-citation-reader mark.hit-evidence").first()).toHaveText("100万元");
  await expect(page).toHaveURL(new RegExp(`attempt_id=${attemptId}`));
  console.log(`EVIDENCE citation-position ${JSON.stringify({ before: documentScroll, current: await pane.evaluate((element) => element.scrollTop), url: page.url() })}`);
  await page.goBack();
  await expect.poll(() => pane.evaluate((element) => element.scrollTop)).toBe(documentScroll);
  await expect.poll(() => findingsPane.evaluate((element) => element.scrollTop)).toBe(findingScroll);
  await page.waitForTimeout(700);
  await expect(pane).toHaveJSProperty("scrollTop", documentScroll);
  await expect(findingsPane).toHaveJSProperty("scrollTop", findingScroll);
  await expect(page).toHaveURL(/work_item=finding-1/);
  const stored = await page.evaluate(() => JSON.stringify({ history: history.state, local: { ...localStorage }, session: { ...sessionStorage } }));
  expect(stored).not.toContain("甲方应支付100万元");
  console.log(`EVIDENCE after-back ${JSON.stringify({ url: page.url(), scroll: await pane.evaluate((element) => element.scrollTop) })}`);
  await page.getByRole("link", { name: "返回案件", exact: true }).click();
  await expect.poll(() => page.evaluate(() => scrollY)).toBe(caseScroll);
  await page.getByRole("link", { name: "返回案件列表" }).click();
  await expect(page).toHaveURL(/page=2/);
  await expect(page).toHaveURL(new RegExp(`selected=${caseId}`));
  await expect(page.getByRole("searchbox", { name: "搜索案件", exact: true })).toHaveValue("导航样本");
  await expect.poll(() => page.evaluate(() => scrollY)).toBe(listScroll);
  console.log(`EVIDENCE return-positions ${JSON.stringify({ listScroll, caseScroll, documentScroll, findingScroll, unmatched: fixture.unmatched })}`);
  await info.attach("list-return.json", { body: JSON.stringify({ listScroll, caseScroll, documentScroll, findingScroll, unmatched: fixture.unmatched }), contentType: "application/json" });
});

test("a tenant switch discards the former citation and list return state", async ({ page }) => {
  const fixture = await installNavigationFixture(page);
  await page.goto(`${web}/cases?q=${encodeURIComponent("导航样本")}&sort=title&direction=asc&page=2&page_size=25`);
  await page.getByRole("link", { name: "导航样本 37", exact: true }).click();
  await page.getByRole("link", { name: "核对原文并处理决定" }).click();
  await page.getByRole("button", { name: "读取本次冻结原文" }).first().click();
  await expect(page.locator(".frozen-citation-reader").first()).toBeVisible();
  fixture.tenant = "tenant-b";
  await page.reload();
  await expect(page.getByText("合成审查员")).toBeVisible();
  await expect.poll(() => new URL(page.url()).searchParams.has("citation")).toBe(false);
  await expect(page.locator(".frozen-citation-reader")).toHaveCount(0);
  await page.getByRole("link", { name: "返回案件", exact: true }).click();
  await page.getByRole("link", { name: "返回案件列表" }).click();
  await expect(page).toHaveURL(`${web}/cases`);
});

test("keyboard navigation works at narrow width and native 200 percent zoom", async ({ page }, info) => {
  await installNavigationFixture(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${web}${casePath}`);
  const target = page.getByRole("link", { name: "跳转到整改与复核" });
  await target.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#remediation-title")).toBeFocused();
  await evidence(page, info, "narrow-keyboard", "remediation-title");

  await page.setViewportSize({ width: 1280, height: 720 });
  const title = `CAW-233-ZOOM-${process.pid}-${Date.now()}`;
  await page.evaluate((value) => { document.title = value; }, title);
  let windows: string[] = [];
  await expect.poll(() => {
    try {
      windows = execFileSync("xdotool", ["search", "--onlyvisible", "--name", title], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim().split(/\s+/);
    } catch { windows = []; }
    return windows.length;
  }).toBe(1);
  execFileSync("xdotool", ["windowactivate", "--sync", windows[0]!]);
  try {
    execFileSync("xdotool", ["key", "--clearmodifiers", "ctrl+0"]);
    const before = await page.evaluate(() => ({ width: innerWidth, dpr: devicePixelRatio }));
    for (let index = 0; index < 5; index += 1) execFileSync("xdotool", ["key", "--clearmodifiers", "ctrl+plus"]);
    await expect.poll(() => page.evaluate(() => devicePixelRatio)).toBe(before.dpr * 2);
    await expect.poll(() => page.evaluate(() => innerWidth)).toBe(before.width / 2);
    const report = page.getByRole("link", { name: "跳转到审查报告与归档" });
    await report.focus();
    await page.keyboard.press("Enter");
    await expect(page.locator("#report-archive-title")).toBeFocused();
    await evidence(page, info, "native-200-percent", "report-archive-title");
    await page.screenshot({ path: info.outputPath("native-200-percent.png") });
  } finally {
    execFileSync("xdotool", ["key", "--clearmodifiers", "ctrl+0"]);
  }
});
