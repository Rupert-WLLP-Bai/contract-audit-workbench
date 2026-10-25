import { test, expect } from "playwright/test";
import { parseActor } from "../../src/api/parse";

function required(name: string) {
  const value = process.env[name];
  if (!value) throw new Error(`Missing explicitly configured real identity prerequisite ${name}`);
  return value;
}
const app = required("E2E_OIDC_APP");
const issuer = required("E2E_OIDC_ISSUER");

for (const audience of ["client", "admin"]) test(`real Keycloak ${audience} authorization code, trusted identity, audience isolation and logout`, async ({ page }) => {
  await page.goto(`${app}/api/auth/oidc/login?audience=${audience}`);
  await expect(page).toHaveURL(new RegExp(new URL(issuer).pathname.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  await page.locator('input[name="username"]').fill(required("E2E_OIDC_ADMIN_USERNAME"));
  await page.locator('input[name="password"]').fill(required("E2E_OIDC_ADMIN_PASSWORD"));
  await page.locator("#kc-login").click();
  await page.waitForURL(url => url.origin === app && !url.pathname.startsWith("/api/auth/oidc/"));
  const response = await page.request.get(`${app}/api/session?audience=${audience}`);
  expect(response.status()).toBe(200);
  const actor = parseActor(await response.json());
  expect(actor.id).toBe("oidc-admin");
  expect(actor.tenant_id).toBe("tenant-oidc");
  expect(actor.org_id).toBe("org-oidc");
  expect(actor.role).toBe("admin");
  expect(actor.audience).toBe(audience);
  const opposite = audience === "client" ? "/api/admin/settings" : "/api/cases";
  expect((await page.request.get(app+opposite)).status()).toBe(401);
  expect((await page.request.delete(`${app}/api/session?audience=${audience}`)).status()).toBe(204);
  expect((await page.request.get(`${app}/api/session?audience=${audience}`)).status()).toBe(401);
});

test("real Keycloak viewer cannot establish Admin session and forged callback stays rejected", async ({ page }) => {
  await page.goto(`${app}/api/auth/oidc/login?audience=admin`);
  await page.locator('input[name="username"]').fill(required("E2E_OIDC_VIEWER_USERNAME"));
  await page.locator('input[name="password"]').fill(required("E2E_OIDC_VIEWER_PASSWORD"));
  const callback = page.waitForResponse(response => response.url().startsWith(`${app}/api/auth/oidc/callback`));
  await page.locator("#kc-login").click();
  expect((await callback).status()).toBe(401);
  expect((await page.request.get(`${app}/api/session?audience=admin`)).status()).toBe(401);
  expect((await page.request.get(`${app}/api/auth/oidc/callback?code=forged&state=forged`)).status()).toBe(401);
});
