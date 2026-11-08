// File metadata (manifest mirror):
// Purpose: Verifies the UI2 contract registration workspace tabs and hidden section panels keep real controls inside the editor at desktop and narrow viewports, with local table scrolling and keyboard focus; complementary to the main-targeted issue-253 spec.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-08
// Updated: 2026-11-08
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { test, expect, type Page, type TestInfo } from "playwright/test";
import { writeFileSync } from "node:fs";
import { installContractFixture, longName, type FixtureMode } from "./issue-253-fixtures";

const web = process.env.E2E_WEB ?? "http://127.0.0.1:5173";
const sections = ["合同基本信息", "相关方与标的", "资金与付款", "材料与核对"] as const;

/**
 * UI2 /contracts uses workspace tabs (合同台账 / 草稿 / 导出记录) and four
 * mounted-but-hidden section fieldsets. The shared #253 spec targets main's
 * older DOM and cannot drive these controls. This spec drives the UI2 controls
 * while reusing the same synthetic authorized fixture and geometry intent.
 */
async function openDraft(page: Page, mode: FixtureMode) {
  const fixture = await installContractFixture(page, mode);
  await page.goto(`${web}/contracts`);
  if (mode !== "empty") {
    await expect(page.locator("fieldset.contracts-page-section")).toHaveCount(4);
    const draftsTab = page.getByRole("tab", { name: /草稿/ });
    if (await draftsTab.count()) await draftsTab.click();
    await page.locator(".contracts-page-list button").first().click();
    await expect(page.getByLabel("合同名称", { exact: true })).toHaveValue(longName);
  }
  return fixture;
}

async function sectionGeometry(page: Page, info: TestInfo, label: string) {
  const result = await page.evaluate(() => {
    const root = document.documentElement;
    const editor = document.querySelector<HTMLElement>(".contracts-page-editor");
    const visible = [...document.querySelectorAll<HTMLElement>("fieldset.contracts-page-section")]
      .find((fieldset) => !fieldset.hasAttribute("hidden"));
    const controls = visible ? [...visible.querySelectorAll<HTMLElement>("input, textarea, select, button, [role=combobox]")] : [];
    // Controls inside a local horizontal scroller are intentionally reachable by
    // scrolling that container; they are not root-level viewport overflow.
    const outside = controls.filter((element) => {
      if (element.closest(".ant-table-content")) return false;
      const box = element.getBoundingClientRect();
      if (box.width === 0 && box.height === 0) return false;
      return box.right > innerWidth + 1 || box.left < -1;
    }).length;
    const tables = visible ? [...visible.querySelectorAll<HTMLElement>(".ant-table-content")] : [];
    return {
      rootScrollWidth: root.scrollWidth, rootClientWidth: root.clientWidth,
      editorScrollWidth: editor?.scrollWidth ?? 0, editorClientWidth: editor?.clientWidth ?? 0,
      sectionScrollWidth: visible?.scrollWidth ?? 0, sectionClientWidth: visible?.clientWidth ?? 0,
      controlCount: controls.length, controlsOutsideViewport: outside,
      tables: tables.map((table) => ({ clientWidth: table.clientWidth, scrollWidth: table.scrollWidth })),
    };
  });
  writeFileSync(info.outputPath(`${label}.json`), JSON.stringify(result, null, 2));
  return result;
}

for (const mode of ["empty", "editable"] as const) {
  test(`UI2 ${mode} sections stay inside the editor across viewport widths`, async ({ page }, info) => {
    const fixture = await openDraft(page, mode);
    const issues: string[] = [];
    const localScrolls: string[] = [];
    if (mode === "empty") {
      for (const width of [1440, 1280, 1024, 390]) {
        await page.setViewportSize({ width, height: 900 });
        const root = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, client: document.documentElement.clientWidth }));
        if (root.scroll > root.client + 1) issues.push(`${width}: empty workspace root overflow ${root.scroll}/${root.client}`);
      }
      expect(fixture.unmatched).toEqual([]);
      expect(fixture.blocked).toEqual([]);
      expect(issues).toEqual([]);
      return;
    }
    for (const width of [1440, 1280, 1024, 390]) {
      await page.setViewportSize({ width, height: 900 });
      const sectionButtons = page.locator(".contracts-form-sections button");
      const availableSections = await sectionButtons.count() ? sections : [sections[0]];
      for (const section of availableSections) {
        if (await page.getByRole("button", { name: section, exact: true }).count())
          await page.getByRole("button", { name: section, exact: true }).click();
        const result = await sectionGeometry(page, info, `ui2-${mode}-${width}-${section}`);
        const prefix = `${width}/${section}`;
        if (result.rootScrollWidth > result.rootClientWidth + 1) issues.push(`${prefix}: root overflow`);
        if (result.editorScrollWidth > result.editorClientWidth + 1) issues.push(`${prefix}: editor overflow`);
        if (result.sectionScrollWidth > result.sectionClientWidth + 1) issues.push(`${prefix}: section overflow`);
        if (result.controlsOutsideViewport > 0) issues.push(`${prefix}: controls outside viewport`);
        for (const table of result.tables) {
          if (table.scrollWidth > table.clientWidth + 1) localScrolls.push(`${prefix}: table ${table.clientWidth}/${table.scrollWidth}`);
        }
        // Keyboard focus must remain on an on-screen control inside the active section.
        const focusTarget = page.locator("fieldset.contracts-page-section:not([hidden]) input:not([disabled]), fieldset.contracts-page-section:not([hidden]) button:not([disabled])").last();
        if (await focusTarget.count()) {
          await focusTarget.focus();
          const focus = await page.evaluate(() => {
            const element = document.activeElement;
            if (!element || element === document.body) return { focused: false, inViewport: true };
            const box = element.getBoundingClientRect();
            return { focused: true, inViewport: box.left >= -1 && box.right <= innerWidth + 1 };
          });
          if (!focus.focused || !focus.inViewport) issues.push(`${prefix}: keyboard focus unreachable or off-screen`);
        }
      }
      if (width === 1440 || width === 390) {
        await page.screenshot({ path: info.outputPath(`ui2-${mode}-${width}.png`), fullPage: false });
      }
    }
    if (mode === "editable") {
      // Local table scrolling is an explicit, allowed layout, not a root-overflow failure.
      expect(localScrolls.length).toBeGreaterThan(0);
      await page.setViewportSize({ width: 390, height: 900 });
      await expect(page.getByLabel("合同名称", { exact: true })).toHaveValue(longName);
    }
    expect(fixture.unmatched).toEqual([]);
    expect(fixture.blocked).toEqual([]);
    writeFileSync(info.outputPath("ui2-section-evidence.json"), JSON.stringify({ mode, localScrolls, issues }, null, 2));
    expect(issues).toEqual([]);
  });
}
