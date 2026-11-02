// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “admin settings keeps the header and side rail pinned while the endpoint modal locks the page”; representative assertions cover admin settings keeps the header and side rail pinned while the endpoint modal locks the page. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { test, expect, adminURL, uiLogin } from "./helpers";

test("admin settings keeps the header and side rail pinned while the endpoint modal locks the page", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await uiLogin(page, "admin");
  await page.goto(`${new URL(adminURL).origin}/admin/settings`);
  const openEndpoint = page.getByRole("button", { name: /^(修改端点|配置端点)$/ });
  await expect(openEndpoint).toBeVisible();

  await page.evaluate(() => window.scrollTo(0, 400));
  await expect.poll(async () => page.evaluate(() => scrollY)).toBeGreaterThan(0);
  await openEndpoint.click();
  const dialog = page.getByRole("dialog", { name: "配置 LLM 端点" });
  await expect(dialog).toBeVisible();

  const layout = await page.evaluate(() => {
    const rail = document.querySelector(".sidenav")!.getBoundingClientRect();
    const header = document.querySelector(".topbar")!.getBoundingClientRect();
    return { railBottom: rail.bottom, headerTop: header.top, viewportHeight: innerHeight, scrollY };
  });
  expect(layout.railBottom).toBeGreaterThanOrEqual(layout.viewportHeight - 1);
  expect(layout.headerTop).toBeGreaterThanOrEqual(-1);

  await page.mouse.move(1200, 300);
  await page.mouse.wheel(0, 300);
  await page.waitForTimeout(250);
  expect(await page.evaluate(() => scrollY)).toBe(layout.scrollY);

  await page.getByRole("button", { name: "取消" }).click();
  await expect(dialog).toBeHidden();
  const beforeCloseWheel = await page.evaluate(() => scrollY);
  const canScrollDown = await page.evaluate(() => scrollY + innerHeight < document.documentElement.scrollHeight);
  await page.mouse.move(1200, 300);
  await page.mouse.wheel(0, canScrollDown ? 200 : -200);
  await expect.poll(async () => page.evaluate(() => scrollY)).not.toBe(beforeCloseWheel);
});
