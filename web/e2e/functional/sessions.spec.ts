// File metadata (manifest mirror):
// Purpose: Checks client and admin sessions, the contract landing and deep-link return, responsive keyboard navigation, authorized case access and session cleanup.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-19
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import {
  test,
  expect,
  operations,
  apiURL,
  webURL,
  portalURL,
  body,
  login,
  uiLogin,
  createCase,
  upload,
  record,
  rows,
} from "./helpers";
import { parseActor, parseSettings } from "../../src/api/parse";

test(
  "session UI enters the personal workbench by default and restores the authorized case link after login",
  operations("createSession", "getSession", "deleteSession", "listCases"),
  async ({ page }) => {
    await page.goto(`${webURL}/login`);
    await page.getByLabel("账号", { exact: true }).fill("demo-admin");
    await page.getByLabel("密码", { exact: true }).fill("wrong-password");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("账号或密码不正确");
    await page.getByLabel("密码", { exact: true }).fill("admin-demo");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page.getByRole("button", { name: "头像菜单", exact: true })).toBeVisible();
    const session = await body(await page.request.get(`${apiURL}/api/session`));
    expect(parseActor(session).role).toBe("admin");
    await expect(page).toHaveURL(`${webURL}/`);
    await expect(page.getByLabel("全局搜索案件")).toBeVisible();
    await page.goto(`${webURL}/workbench/home?q=ledger#main`);
    await expect(page).toHaveURL(`${webURL}/?q=ledger#main`);
    const nav = page.getByRole("navigation", { name: "主导航", exact: true });
    await nav.getByRole("link", { name: "案件与材料", exact: true }).click();
    await expect(page.getByRole("heading", { name: "案件", exact: true })).toBeVisible();
    await nav.getByRole("link", { name: "我的待办", exact: true }).click();
    await expect(page).toHaveURL(`${webURL}/`);
    await page.getByRole("button", { name: "头像菜单", exact: true }).click();
    await page.getByRole("button", { name: "退出", exact: true }).click();
    await expect(page.getByRole("heading", { name: "登录合同审查", exact: true })).toBeVisible();
    expect((await page.request.get(`${apiURL}/api/session`)).status()).toBe(401);
    const deepLink = `${webURL}/cases?view=mine#main`;
    await page.goto(deepLink);
    await expect(page.getByRole("heading", { name: "登录合同审查", exact: true })).toBeVisible();
    await page.getByLabel("账号", { exact: true }).fill("demo-admin");
    await page.getByLabel("密码", { exact: true }).fill("admin-demo");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page).toHaveURL(deepLink);
  },
);

test("sidebars retain readable labels, keyboard controls and contained narrow layout", async ({
  page,
}) => {
  await uiLogin(page);
  await page.goto(`${webURL}/cases`);
  const tokens = await page.evaluate(() => {
    const probe = document.createElement("span");
    document.body.append(probe);
    const color = (token: string) => {
      probe.style.color = `var(--${token})`;
      return getComputedStyle(probe).color;
    };
    const palette = {
      rail: color("surface-rail"),
      raised: color("surface-rail-raised"),
      primary: color("text-primary"),
      secondary: color("text-secondary"),
      selected: color("nav-item-selected"),
      selectedText: color("nav-item-selected-fg"),
      focus: color("focus-ring"),
    };
    probe.remove();
    return palette;
  });
  const rail = page.locator(".shell-sidenav");
  const nav = rail.getByRole("navigation", { name: "主导航" });
  const contracts = nav.getByRole("link", { name: "合同清单", exact: true });
  const cases = nav.getByRole("link", { name: "案件与材料", exact: true });
  const tasks = nav.getByRole("link", { name: "我的待办", exact: true });
  const review = nav.getByRole("link", { name: "发起审查", exact: true });
  const tasksItem = tasks.locator("xpath=ancestor::*[@role='menuitem']");
  const trigger = rail.getByRole("button", { name: "收合侧栏" });

  await expect(rail).toHaveCSS("background-color", tokens.rail);
  await expect(contracts).toHaveCSS("color", tokens.secondary);
  await expect(tasks).toHaveCSS("color", tokens.secondary);
  await expect(tasksItem.locator("svg")).toHaveCSS("color", tokens.secondary);
  await expect(nav.getByText("合同履约", { exact: true })).toBeVisible();
  await expect(cases).toHaveAttribute("aria-current", "page");
  await expect(cases).toHaveCSS("color", tokens.selectedText);
  await expect(cases.locator("xpath=ancestor::*[@role='menuitem']")).toHaveCSS(
    "background-color",
    tokens.selected,
  );

  await tasks.hover();
  await expect(tasks).toHaveCSS("color", tokens.primary);
  await expect(tasksItem).toHaveCSS("background-color", tokens.raised);
  await tasksItem.press("ArrowDown");
  await expect(review).toBeFocused();
  await expect(review).toHaveCSS("outline-color", tokens.focus);
  await expect(review).toHaveCSS("outline-style", "solid");
  const tools = nav.getByRole("menuitem", { name: "更多工具" });
  await expect(tools).toHaveAttribute("aria-expanded", "false");
  await tools.hover();
  await expect(tools).toHaveAttribute("aria-expanded", "false");
  await tools.press("Enter");
  await expect(nav.getByRole("link", { name: "API 文档" })).toHaveCount(0);
  const account = page.getByRole("button", { name: "头像菜单" });
  await account.press("Enter");
  await page.getByRole("switch", { name: "专业模式" }).press("Space");
  await account.press("Enter");
  await expect(nav.getByRole("link", { name: "API 文档" })).toBeVisible();

  await expect(trigger.locator("svg")).toHaveCSS("color", tokens.secondary);
  await trigger.press("Space");
  await expect(rail.getByRole("button", { name: "展开侧栏" })).toHaveAttribute(
    "aria-expanded",
    "false",
  );
  await rail.getByRole("button", { name: "展开侧栏" }).press("Space");
  await expect(trigger).toHaveAttribute("aria-expanded", "true");

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(rail).toBeHidden();
  const mobileTrigger = page.getByRole("button", { name: "打开主导航" });
  await mobileTrigger.press("Enter");
  const menu = page.getByRole("dialog", { name: "主导航" });
  await expect(menu.getByRole("link", { name: "我的待办" })).toBeVisible();
  await expect(menu.getByRole("link", { name: "合同登记与履约" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();
  await expect(mobileTrigger).toBeFocused();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(390);

  await mobileTrigger.press("Enter");
  await menu.getByRole("link", { name: "合同清单", exact: true }).press("Enter");
  await expect(page).toHaveURL(`${webURL}/contract-desk`);
  await expect(menu).toBeHidden();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(390);
  await page.goBack();
  await expect(page).toHaveURL(`${webURL}/cases`);
  await expect(menu).toBeHidden();

  await expect(page.locator(".cases-page .page-pattern-surface")).toHaveCSS(
    "background-color",
    tokens.rail,
  );

  await page.setViewportSize({ width: 1440, height: 1000 });
  await uiLogin(page, "admin");
  const adminRail = page.locator(".admin-sidenav");
  await expect(adminRail).toHaveCSS("background-color", tokens.rail);
  await expect(adminRail.getByText("规则版本与发布历史", { exact: true })).toHaveCSS(
    "color",
    tokens.secondary,
  );
  await adminRail.getByRole("link", { name: "规则库" }).click();
  await expect(page.locator(".page-rulepacks .page-pattern-surface")).toHaveCSS(
    "background-color",
    tokens.rail,
  );
});

test(
  "client/admin audiences, anonymous reads, malformed requests and Origin guards stay isolated",
  operations("createSession", "getSession", "deleteSession", "adminSettings", "getSettings"),
  async ({ request, playwright }) => {
    expect((await request.get(`${apiURL}/api/cases`)).status()).toBe(401);
    expect((await request.get(`${apiURL}/api/settings`)).status()).toBe(401);
    await login(request);
    expect((await request.get(`${apiURL}/api/session?audience=admin`)).status()).toBe(401);
    expect((await request.get(`${apiURL}/api/admin/settings`)).status()).toBe(401);
    const admin = await playwright.request.newContext({ extraHTTPHeaders: { Referer: apiURL } });
    try {
      await login(admin, "demo-admin", "admin-demo", "admin");
      expect((await admin.get(`${apiURL}/api/cases`)).status()).toBe(401);
      const settings = parseSettings(await body(await admin.get(`${apiURL}/api/admin/settings`)));
      expect(settings.model.status).toBe("connected");
      expect(settings.model.label).toContain("codex/gpt-6-luna");
      expect(settings.queue.depth).toBeNull();
      expect(settings.execution?.model_calls).toBeGreaterThanOrEqual(0);
      expect(JSON.stringify(settings)).not.toMatch(
        /functional-worker-|functional-document-|LLM_API_KEY/,
      );
      await body(await admin.delete(`${apiURL}/api/session?audience=admin`), 204);
      expect((await admin.get(`${apiURL}/api/admin/settings`)).status()).toBe(401);
      expect((await request.get(`${apiURL}/api/session`)).status()).toBe(200);
    } finally {
      await admin.dispose();
    }
    expect(
      (
        await request.post(`${apiURL}/api/cases`, {
          data: { title: "forged", contract_type: "service" },
          headers: { Origin: "https://unauthorized.invalid" },
        })
      ).status(),
    ).toBe(403);
    expect(
      (
        await request.post(`${apiURL}/api/cases`, {
          data: { title: "forged", tenant_id: "other", contract_type: "service" },
        })
      ).status(),
    ).toBe(422);
    expect(
      (
        await request.post(`${apiURL}/api/session`, {
          data: { username: "demo-admin", password: "admin-demo", audience: "unknown" },
        })
      ).status(),
    ).toBe(422);
  },
);

test(
  "case membership grants only the named case and viewer writes remain forbidden",
  operations(
    "createCase",
    "getCase",
    "addCaseMember",
    "getOriginal",
    "uploadDocument",
    "getWorkspace",
  ),
  async ({ request, playwright }) => {
    await login(request);
    const caseID = await createCase(request, "私有成员案件");
    const docID = await upload(
      request,
      caseID,
      "procurement",
      "private.txt",
      "只允许案件成员读取的合成质保36个月。",
    );
    const other = await createCase(request, "其它私有案件");
    const viewer = await playwright.request.newContext({ extraHTTPHeaders: { Referer: apiURL } });
    try {
      await login(viewer, "demo-viewer", "viewer-demo");
      for (const suffix of ["", `/documents/${docID}/original`, "/reviews/run_nonexistent"])
        expect((await viewer.get(`${apiURL}/api/cases/${caseID}${suffix}`)).status()).toBe(404);
      await body(
        await request.post(`${apiURL}/api/cases/${caseID}/members`, {
          data: { actor_id: "demo-viewer" },
        }),
      );
      const visible = await body(await viewer.get(`${apiURL}/api/cases/${caseID}`));
      expect(rows(visible.members).map((actor) => actor.id)).toContain("demo-viewer");
      expect((await viewer.get(`${apiURL}/api/cases/${other}`)).status()).toBe(404);
      expect(
        (await viewer.get(`${apiURL}/api/cases/${caseID}/documents/${docID}/original`)).status(),
      ).toBe(200);
      expect(
        (
          await viewer.post(`${apiURL}/api/cases`, {
            data: { title: "unauthorized", contract_type: "service" },
          })
        ).status(),
      ).toBe(403);
      expect(
        (
          await viewer.post(`${apiURL}/api/cases/${caseID}/documents/${docID}/parse`, { data: {} })
        ).status(),
      ).toBe(403);
      expect(
        (
          await viewer.post(`${apiURL}/api/cases/${caseID}/members`, {
            data: { actor_id: "demo-clerk" },
          })
        ).status(),
      ).toBe(403);
    } finally {
      await viewer.dispose();
    }
  },
);

test(
  "health, actual OpenAPI and absent OIDC configuration have honest responses",
  operations("getHealth", "getOpenAPI", "oidcLogin", "oidcCallback"),
  async ({ request }) => {
    const health = await body(await request.get(`${apiURL}/health`));
    expect(health.status).toBe("ok");
    const spec = await body(await request.get(`${apiURL}/openapi.json`));
    expect(spec.openapi).toMatch(/^3\./);
    expect(
      record(record(record(spec.paths)["/api/admin/rulepacks/{packID}/versions/{version}"]).put)
        .operationId,
    ).toBe("editRulePackVersion");
    const begin = await request.get(`${apiURL}/api/auth/oidc/login?audience=client`);
    expect(begin.status()).toBe(503);
    expect(await begin.text()).toContain("authentication unavailable");
    const callback = await request.get(`${apiURL}/api/auth/oidc/callback?code=forged&state=forged`);
    expect(callback.status()).toBe(503);
    expect(await callback.text()).toContain("authentication failed");
  },
);

test(
  "Admin metadata and local Scalar reference render actual backend data",
  operations("adminSettings", "getSettings", "getOpenAPI"),
  async ({ page, request }) => {
    await uiLogin(page, "admin");
    await expect(page.getByRole("heading", { name: "设置", exact: true })).toBeVisible();
    await expect(page.getByText(/codex\/gpt-6-luna/).first()).toBeVisible();
    await login(request);
    const publicSettings = parseSettings(await body(await request.get(`${apiURL}/api/settings`)));
    expect(publicSettings.users.map((user) => user.id)).toContain("demo-admin");
    await page.goto(portalURL);
    await expect(page.locator(".scalar-api-reference")).toBeVisible();
    await page.getByRole("button", { name: /Open Search/ }).click();
    await page.getByRole("combobox").fill("/api/cases");
    await page.getByRole("option").filter({ hasText: "列出可见案件" }).click();
    await expect(page.locator(".scalar-api-reference")).toContainText("/api/cases");
    await expect(
      page.getByRole("button", { name: /Test Request|Send Request|Try it/ }),
    ).toHaveCount(0);
    expect(await page.locator('script[src^="http"]').count()).toBe(0);
  },
);
