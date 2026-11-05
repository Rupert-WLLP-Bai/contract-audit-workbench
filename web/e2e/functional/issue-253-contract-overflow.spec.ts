import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import { test, expect, type Page, type TestInfo } from "playwright/test";
import { installContractFixture, longBank, longName, longNumber, longSupplier, longUrl, type FixtureMode } from "./issue-253-fixtures";

const web = process.env.E2E_WEB ?? "http://127.0.0.1:5173";

async function openEditor(page: Page, mode: FixtureMode) {
  const fixture = await installContractFixture(page, mode);
  await page.goto(`${web}/contracts`);
  await expect(page.locator("fieldset.contracts-page-section")).toHaveCount(4);
  if (mode !== "empty") {
    await page.locator(".contracts-page-list button").click();
    await expect(page.locator(".contracts-page-list-name")).toHaveText(longName);
    await expect(page.getByLabel("合同名称", { exact: true })).toHaveValue(longName);
    await expect(page.getByText("版本 7", { exact: true })).toBeVisible();
    await expect(page.locator(".contracts-page-row")).toHaveCount(6);
    await expect(page.locator(".contracts-page-materials > li")).toHaveCount(3);
  }
  return fixture;
}

async function geometry(page: Page, info: TestInfo, label: string) {
  const result = await page.evaluate(() => {
    const rect = (element: Element) => {
      const box = element.getBoundingClientRect();
      return { left: box.left, right: box.right, width: box.width, top: box.top, bottom: box.bottom };
    };
    const describe = (element: Element) => {
      const html = element as HTMLElement;
      const style = getComputedStyle(element);
      return { tag: element.tagName, className: element.getAttribute("class"), rect: rect(element),
        clientWidth: html.clientWidth, scrollWidth: html.scrollWidth,
        minInlineSize: style.minInlineSize, overflowX: style.overflowX };
    };
    const groups = Object.fromEntries(Object.entries({
      layout: ".contracts-page-layout", editor: ".contracts-page-editor", list: ".contracts-page-list",
      sections: "fieldset.contracts-page-section", grids: ".contracts-page-fields, .contracts-page-row",
      fields: ".contracts-page-fields .field", materials: ".contracts-page-materials > li",
      controls: ".contracts-page-section input, .contracts-page-section textarea, .contracts-page-section select, .contracts-page-section button, .contracts-page-section [role='combobox']",
      tables: ".contracts-page-section .ant-table-content",
      itemInputs: "fieldset.contracts-page-section input[aria-label*='行']",
    }).map(([key, selector]) => [key, Array.from(document.querySelectorAll(selector), describe)]));
    const root = document.documentElement;
    const issues: string[] = [];
    if (root.scrollWidth > root.clientWidth + 1) issues.push(`document ${root.scrollWidth} > ${root.clientWidth}`);
    const editor = document.querySelector<HTMLElement>(".contracts-page-editor")!;
    const layout = document.querySelector<HTMLElement>(".contracts-page-layout")!;
    const list = document.querySelector<HTMLElement>(".contracts-page-list")!;
    const inside = (child: Element, parent: Element) => {
      const a = rect(child); const b = rect(parent);
      return a.left >= b.left - 1 && a.right <= b.right + 1;
    };
    if (!inside(editor, layout)) issues.push("editor outside layout");
    if (!inside(list, layout)) issues.push("list outside layout");
    if (layout.scrollWidth > layout.clientWidth + 1) issues.push(`layout scroll ${layout.scrollWidth} > ${layout.clientWidth}`);
    if (list.scrollWidth > list.clientWidth + 1) issues.push(`list scroll ${list.scrollWidth} > ${list.clientWidth}`);
    document.querySelectorAll<HTMLElement>("fieldset.contracts-page-section").forEach((section, index) => {
      if (!inside(section, editor)) issues.push(`section ${index + 1} outside editor`);
      if (section.scrollWidth > section.clientWidth + 1) issues.push(`section ${index + 1} scroll ${section.scrollWidth} > ${section.clientWidth}`);
      section.querySelectorAll<HTMLElement>(".contracts-page-fields, .contracts-page-row, .contracts-page-materials > li, .ant-table-content").forEach((child) => {
        if (!inside(child, section)) issues.push(`section ${index + 1} child outside: ${child.className}`);
        if (child.scrollWidth > child.clientWidth + 1 && !child.classList.contains("ant-table-content"))
          issues.push(`section ${index + 1} child scroll ${child.className}: ${child.scrollWidth} > ${child.clientWidth}`);
      });
      section.querySelectorAll<HTMLElement>(".contracts-page-fields > *, .contracts-page-row > *").forEach((child) => {
        if (child.parentElement && !inside(child, child.parentElement)) issues.push(`section ${index + 1} grid item outside`);
      });
      section.querySelectorAll<HTMLElement>("input, textarea, select, button, [role='combobox']").forEach((control) => {
        if (!control.closest(".ant-table-content") && !inside(control, section)) issues.push(`section ${index + 1} control outside: ${control.tagName}`);
      });
    });
    document.querySelectorAll<HTMLElement>("fieldset.contracts-page-section input[aria-label*='行']").forEach((input) => {
      if (rect(input).width < 70) issues.push(`item input too narrow: ${Math.round(rect(input).width)}px`);
    });
    return { viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio, scale: visualViewport?.scale },
      document: { clientWidth: root.clientWidth, scrollWidth: root.scrollWidth }, groups, issues };
  });
  writeFileSync(info.outputPath(`${label}.json`), JSON.stringify(result, null, 2));
  console.log(`EVIDENCE ${label} ${JSON.stringify({ viewport: result.viewport, document: result.document, issues: result.issues })}`);
  return result;
}

async function emptyMessageIssue(page: Page): Promise<string | null> {
  const bounds = await page.getByText("还没有明细行", { exact: true }).evaluate((element) => {
    const scroller = element.closest(".ant-table-content");
    if (!scroller) return null;
    const range = document.createRange();
    range.selectNodeContents(element);
    const message = range.getBoundingClientRect();
    const viewport = scroller.getBoundingClientRect();
    return { messageLeft: message.left, messageRight: message.right, viewportLeft: viewport.left, viewportRight: viewport.right };
  });
  if (!bounds || bounds.messageLeft < bounds.viewportLeft - 1 || bounds.messageRight > bounds.viewportRight + 1)
    return `empty table message outside its visible area: ${JSON.stringify(bounds)}`;
  return null;
}

for (const mode of ["empty", "editable"] as const) {
  test(`${mode} sections stay within the editor across viewport widths`, async ({ page }, info) => {
    const fixture = await openEditor(page, mode);
    const issues: string[] = [];
    for (const width of [1440, 1280, 1024, 390]) {
      await page.setViewportSize({ width, height: 900 });
      const result = await geometry(page, info, `${mode}-${width}`);
      issues.push(...result.issues.map((issue) => `${width}: ${issue}`));
      if (mode === "empty") {
        const issue = await emptyMessageIssue(page);
        if (issue) issues.push(`${width}: ${issue}`);
      }
      if (mode === "editable" && width === 390) {
        const table = page.locator("fieldset.contracts-page-section").nth(1).locator(".ant-table-content");
        const dimensions = await table.evaluate((element) => ({ clientWidth: element.clientWidth, scrollWidth: element.scrollWidth }));
        if (dimensions.scrollWidth <= dimensions.clientWidth) issues.push(`390: item table does not offer local scrolling (${dimensions.scrollWidth}/${dimensions.clientWidth})`);
        await page.getByLabel("第 1 行明细金额", { exact: true }).focus();
        await expect(page.getByLabel("第 1 行明细金额", { exact: true })).toBeFocused();
        if (await table.evaluate((element) => element.scrollLeft) <= 0) issues.push("390: keyboard focus cannot reach the right side of the item table");
        await page.getByLabel("第 1 行明细金额", { exact: true }).fill("12345.67");
      }
      if (width === 1280 || width === 390) await page.screenshot({ path: info.outputPath(`${mode}-${width}.png`), fullPage: true });
    }
    if (mode === "editable") {
      await expect(page.getByLabel("合同编号", { exact: true })).toHaveValue(longNumber);
      await expect(page.getByLabel("供应商全称", { exact: true })).toHaveValue(longSupplier);
      await expect(page.getByLabel("开户行", { exact: true })).toHaveValue(longBank);
      await expect(page.locator("fieldset.contracts-page-section .field-wide textarea").first()).toHaveValue(longUrl);
      await page.getByLabel("合同名称", { exact: true }).fill(`${longName}X`);
      await page.setViewportSize({ width: 1280, height: 900 });
      await expect(page.getByLabel("合同名称", { exact: true })).toHaveValue(`${longName}X`);
      await expect(page.getByLabel("第 1 行明细金额", { exact: true })).toHaveValue("12345.67");
      await expect(page.getByText("有未保存的修改", { exact: true })).toBeVisible();
      await expect(page.getByText("版本 7", { exact: true })).toBeVisible();
    }
    expect(fixture.unmatched).toEqual([]);
    expect(fixture.blocked).toEqual([]);
    expect(issues).toEqual([]);
  });

  test(`${mode} sections fit at native 200 percent browser zoom`, async ({ page }, info) => {
    test.skip(process.env.E2E_NATIVE_ZOOM !== "1", "Native zoom requires a headed browser and xdotool");
    await page.setViewportSize({ width: 1280, height: 900 });
    const fixture = await openEditor(page, mode);
    const title = `CAW-253-ZOOM-${process.pid}-${Date.now()}`;
    await page.evaluate((value) => { document.title = value; }, title);
    await page.bringToFront();
    let windows: string[] = [];
    await expect.poll(() => {
      try { windows = execFileSync("xdotool", ["search", "--onlyvisible", "--name", title], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim().split(/\s+/); }
      catch { windows = []; }
      return windows.length;
    }).toBe(1);
    execFileSync("xdotool", ["windowfocus", "--sync", windows[0]!]);
    try {
      execFileSync("xdotool", ["key", "--clearmodifiers", "ctrl+0"]);
      const before = await page.evaluate(() => ({ width: innerWidth, dpr: devicePixelRatio }));
      for (let index = 0; index < 5; index += 1) execFileSync("xdotool", ["key", "--clearmodifiers", "ctrl+plus"]);
      await expect.poll(() => page.evaluate(() => devicePixelRatio)).toBe(before.dpr * 2);
      await expect.poll(() => page.evaluate(() => innerWidth)).toBe(before.width / 2);
      const result = await geometry(page, info, `${mode}-native-200`);
      const emptyIssue = mode === "empty" ? await emptyMessageIssue(page) : null;
      await page.screenshot({ path: info.outputPath(`${mode}-native-200.png`), fullPage: true });
      expect(fixture.unmatched).toEqual([]);
      expect(fixture.blocked).toEqual([]);
      expect(result.issues).toEqual([]);
      expect(emptyIssue).toBeNull();
    } finally {
      execFileSync("xdotool", ["key", "--clearmodifiers", "ctrl+0"]);
    }
  });
}

test("masked read-only draft stays masked and cannot be edited", async ({ page }, info) => {
  await page.setViewportSize({ width: 390, height: 900 });
  const fixture = await openEditor(page, "readonly");
  await expect(page.getByLabel("开户行", { exact: true })).toHaveValue("••••");
  await expect(page.getByLabel("开户行", { exact: true })).toHaveAttribute("readonly");
  await expect(page.getByLabel("账号", { exact: true })).toHaveValue("••••••••");
  await expect(page.getByLabel("合同名称", { exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "保存草稿" })).toBeDisabled();
  const result = await geometry(page, info, "readonly-390");
  expect(fixture.unmatched).toEqual([]);
  expect(fixture.blocked).toEqual([]);
  expect(result.issues).toEqual([]);
});
