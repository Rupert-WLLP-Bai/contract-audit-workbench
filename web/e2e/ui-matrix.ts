import { chromiumExecutable } from "./chromium";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium, type Page } from "playwright";

/**
 * Desktop and narrow-screen screenshot matrix for the established UI checks
 * UX-AUDIT "M0 应补" list: login, case list, case detail, workspace, API docs,
 * admin settings, portal reference at 1440x900 / 1280x800 / 1024x768 /
 * 768x1024 / 390x844 / 375x812.
 *
 * Gating: a cell is only `captured` after a page-specific readiness wait AND a
 * structural state probe. A cell that shows the app's own PageStatus
 * (loading/empty/error/denied) or never becomes ready is recorded as `failed`
 * or `NOT_RUN` and gets NO screenshot, so a transient loading frame can never be
 * mistaken for a baseline. HTTP 200, viewport width and console output are
 * recorded but are never sufficient on their own.
 *
 * The harness never starts a stack: point E2E_WEB (and optionally E2E_ADMIN /
 * E2E_PORTAL) at a running environment and read the manifest it writes. Missing
 * identifiers are recorded as NOT_RUN instead of being faked.
 *
 * Browser convention follows web/e2e/c1b-journey.ts: CHROMIUM_BIN or
 * /usr/bin/chromium, headless, no display required.
 *
 * Run:
 *   cd web && E2E_WEB=http://127.0.0.1:18080 E2E_USERNAME=... E2E_PASSWORD=... \
 *     E2E_CASE_ID=... E2E_RUN_ID=... E2E_SOURCE_SHA=<repo source digest> E2E_SERVER_SHA=<Core binary SHA-256> bun e2e/ui-matrix.ts
 */

const web = required("E2E_WEB").replace(/\/$/, "");
const outDir = resolve(process.env.E2E_OUT ?? "../artifacts/ui-matrix");
const sourceSHA = (process.env.E2E_SOURCE_SHA ?? "").trim();
const serverSHA = (process.env.E2E_SERVER_SHA ?? "").trim();
const executionSHA = (process.env.E2E_EXECUTION_SHA ?? "").trim();
const caseID = (process.env.E2E_CASE_ID ?? "").trim();
const runID = (process.env.E2E_RUN_ID ?? "").trim();
const adminSite = (process.env.E2E_ADMIN ?? "").replace(/\/$/, "");
const portalSite = (process.env.E2E_PORTAL ?? "").replace(/\/$/, "");
const username = (process.env.E2E_USERNAME ?? "").trim();
const password = (process.env.E2E_PASSWORD ?? "").trim();
const readyTimeout = Number(process.env.E2E_READY_TIMEOUT_MS ?? 15000);
const settleMs = Number(process.env.E2E_SETTLE_MS ?? 200);

const VIEWPORTS = [
  { id: "1440x900", width: 1440, height: 900 },
  { id: "1280x800", width: 1280, height: 800 },
  { id: "1024x768", width: 1024, height: 768 },
  { id: "768x1024", width: 768, height: 1024 },
  { id: "390x844", width: 390, height: 844 },
  { id: "375x812", width: 375, height: 812 },
] as const;

type Site = "client" | "admin" | "portal";
type PageState = "ready" | "loading" | "empty" | "error" | "denied" | "unknown";

interface Target {
  id: string;
  site: Site;
  path: string | null;
  needs?: string;
  /** Page-specific readiness: the only acceptable reason to take a screenshot. */
  ready: (page: Page) => Promise<void>;
  /** A legitimate but non-baseline state (for example an empty case list). */
  emptyWhen?: PageState;
}

const casePath = caseID === "" ? null : `/cases/${caseID}`;
const reviewPath = caseID === "" || runID === "" ? null : `/cases/${caseID}/reviews/${runID}`;

async function waitForText(page: Page, selector: string): Promise<void> {
  await page.waitForFunction(
    (target) => (document.querySelector(target)?.textContent ?? "").trim().length > 0,
    selector,
    { timeout: readyTimeout },
  );
}

/** The app's own PageStatus is the structural loading/error/empty/denied marker. */
async function pageState(page: Page): Promise<PageState> {
  return await page.evaluate(() => {
    const node = document.querySelector(".page-status");
    if (!node) return "ready";
    const kind = Array.from(node.classList).find((name) => name.startsWith("page-status-"));
    const value = kind ? kind.replace("page-status-", "") : "";
    if (value === "loading" || value === "empty" || value === "error" || value === "denied") return value;
    return "unknown";
  });
}

const targets: Target[] = [
  {
    id: "login",
    site: "client",
    path: "/login",
    ready: async (page) => {
      await page.locator('input[name="username"]').waitFor({ state: "visible", timeout: readyTimeout });
      await page.locator('input[name="password"]').waitFor({ state: "visible", timeout: readyTimeout });
    },
  },
  {
    id: "cases",
    site: "client",
    path: "/cases",
    ready: async (page) => {
      // Either a populated filter bar with rows, or the deliberate empty state.
      await page.locator(".cases-filters").waitFor({ state: "visible", timeout: readyTimeout });
    },
    emptyWhen: "empty",
  },
  {
    id: "case-detail",
    site: "client",
    path: casePath,
    needs: "E2E_CASE_ID",
    ready: async (page) => {
      await page.locator(".document-list li[data-document-id]").first().waitFor({ state: "visible", timeout: readyTimeout });
      await waitForText(page, ".page-head h1");
    },
  },
  {
    id: "review-workspace",
    site: "client",
    path: reviewPath,
    needs: "E2E_CASE_ID and E2E_RUN_ID",
    ready: async (page) => {
      // Source text and at least one finding must be visibly rendered.
      await page.locator(".workspace-grid").waitFor({ state: "visible", timeout: readyTimeout });
      await page.locator(".finding-button").first().waitFor({ state: "visible", timeout: readyTimeout });
      await waitForText(page, ".col-doc .doc-pane[data-document-version-id]");
      const inspector = page.locator(".execution-inspector-disclosure");
      await inspector.waitFor({ state: "visible", timeout: readyTimeout });
      if (!(await inspector.evaluate((element) => (element as HTMLDetailsElement).open))) {
        await inspector.locator(":scope > summary.execution-inspector-summary").click();
      }
      await page.locator(".execution-inspector-body").waitFor({ state: "visible", timeout: readyTimeout });
      await page.locator('.execution-inspector-body select[aria-label="查看执行记录"]').waitFor({ state: "visible", timeout: readyTimeout });
    },
  },
  {
    id: "api-docs",
    site: "client",
    path: "/api-docs",
    ready: async (page) => {
      await page.locator(".api-docs-browser").waitFor({ state: "visible", timeout: readyTimeout });
      await page.waitForFunction(() => {
        const loading = document.querySelector(".api-docs-loading");
        const browser = document.querySelector(".api-docs-browser");
        const failure = document.querySelector(".api-docs-page .ant-alert-error");
        return !loading && failure === null && (browser?.textContent ?? "").trim().length > 0;
      }, undefined, { timeout: readyTimeout });
    },
  },
  {
    id: "admin-settings",
    site: "admin",
    path: "/admin/settings",
    needs: "E2E_ADMIN",
    ready: async (page) => {
      // Actual settings content, not the loading shell.
      await page.locator("table.data-table").waitFor({ state: "visible", timeout: readyTimeout });
      await page.locator("section dl.execution-dl").first().waitFor({ state: "visible", timeout: readyTimeout });
    },
  },
  {
    id: "portal-reference",
    site: "portal",
    path: "",
    needs: "E2E_PORTAL",
    ready: async (page) => {
      await page.locator(".api-docs-browser").waitFor({ state: "visible", timeout: readyTimeout });
      await page.waitForFunction(() => {
        const loading = document.querySelector(".api-docs-loading");
        const browser = document.querySelector(".api-docs-browser");
        const failure = document.querySelector(".api-docs-page .ant-alert-error");
        return !loading && failure === null && (browser?.textContent ?? "").trim().length > 0;
      }, undefined, { timeout: readyTimeout });
    },
  },
];

/** Keyboard traversal: focus must move, stay visible and stay inside the viewport. */
async function keyboardAudit(page: Page): Promise<{
  steps: number; distinct: number; targets: string[]; offscreen: string[]; hidden: string[];
}> {
  const identities: number[] = [];
  const targets: string[] = [];
  const offscreen: string[] = [];
  const hidden: string[] = [];
  await page.evaluate(() => {
    document.body.setAttribute("tabindex", "-1");
    document.body.focus({ preventScroll: true });
  });
  for (let step = 0; step < 8; step++) {
    await page.keyboard.press("Tab");
    const info = await page.evaluate(() => {
      let element = document.activeElement as HTMLElement | null;
      while (element?.shadowRoot?.activeElement instanceof HTMLElement) {
        element = element.shadowRoot.activeElement as HTMLElement;
      }
      if (!element || element === document.body || element === document.documentElement || element.tabIndex < 0) {
        return { identity: null, label: element?.tagName ?? "NONE", visible: false, onScreen: false };
      }
      const auditWindow = window as Window & {
        __matrixFocusAudit?: { nextId: number; ids: WeakMap<Element, number> };
      };
      const registry = auditWindow.__matrixFocusAudit ?? { nextId: 1, ids: new WeakMap<Element, number>() };
      auditWindow.__matrixFocusAudit = registry;
      let identity = registry.ids.get(element);
      if (identity === undefined) {
        identity = registry.nextId++;
        registry.ids.set(element, identity);
      }
      const rect = element.getBoundingClientRect();
      const style = window.getComputedStyle(element);
      const visible = style.visibility !== "hidden" && style.display !== "none" && rect.width > 0 && rect.height > 0;
      const onScreen = rect.left < window.innerWidth && rect.right > 0 && rect.top < window.innerHeight && rect.bottom > 0;
      const label = `${element.tagName}:${(element.getAttribute("aria-label") || element.textContent || "").trim().slice(0, 40)}`;
      return { identity, label, visible, onScreen };
    });
    targets.push(`${info.identity ?? "none"}:${info.label}`);
    if (info.identity !== null) {
      identities.push(info.identity);
      if (!info.visible) hidden.push(info.label);
      else if (!info.onScreen) offscreen.push(info.label);
    }
  }
  await page.evaluate(() => document.body.removeAttribute("tabindex"));
  return { steps: 8, distinct: new Set(identities).size, targets, offscreen, hidden };
}

/** Three-column workspace content: real regions, real text, real findings. */
async function workspaceAudit(page: Page): Promise<Record<string, unknown>> {
  return await page.evaluate(() => {
    const grid = document.querySelector(".workspace-grid");
    if (!grid) return { ok: false, reason: "no .workspace-grid" };
    const columns = window.getComputedStyle(grid).gridTemplateColumns.split(" ").filter(Boolean).length;
    const text = (selector: string) => (document.querySelector(selector)?.textContent ?? "").trim().length;
    return {
      ok: true,
      columns,
      layout_class: grid.className,
      nav_text: text(".col-nav"),
      doc_text: text(".col-doc .doc-pane[data-document-version-id]"),
      findings: document.querySelectorAll(".col-findings .finding-button").length,
      inspector_text: text(".execution-inspector"),
      inspector_open: Boolean((document.querySelector(".execution-inspector-disclosure") as HTMLDetailsElement | null)?.open),
      inspector_body_text: text(".execution-inspector-body"),
    };
  });
}

interface Capture {
  validation?: Record<string, unknown>;
  diagnostic?: Record<string, unknown>;
  target: string;
  viewport: string;
  site: Site;
  url: string | null;
  final_url?: string;
  state: PageState | "not_ready";
  status: "captured" | "failed" | "NOT_RUN";
  reason?: string;
  file?: string;
  bytes?: number;
  sha256?: string;
  http_status?: number;
  measured_width?: number;
  split_layout?: boolean;
  horizontal_overflow_px?: number;
  console_errors?: string[];
  network_errors?: string[];
}

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function baseOf(site: Site): string {
  if (site === "admin") return adminSite;
  if (site === "portal") return portalSite;
  return web;
}

async function signIn(page: Page, base: string): Promise<Record<string, unknown>> {
  const audience = base === adminSite ? "admin" : "client";
  let submitted = false;
  try {
    await page.goto(new URL("/login", base).toString(), { waitUntil: "load" });
    await page.waitForFunction(() => window.location.pathname !== "/login" ||
      document.querySelector('input[name="password"]') instanceof HTMLInputElement,
    undefined, { timeout: readyTimeout });
    if (await page.locator('input[name="password"]').count() > 0) {
      await page.getByLabel("账号", { exact: true }).fill(username);
      await page.getByLabel("密码", { exact: true }).fill(password);
      await page.getByRole("button", { name: "登录", exact: true }).click();
      await page.locator('input[name="password"]').waitFor({ state: "detached", timeout: readyTimeout });
      submitted = true;
    }
    const session = await page.request.get(new URL(`/api/session?audience=${audience}`, base).toString());
    const protectedPath = audience === "admin" ? "/api/admin/settings" : "/api/cases";
    const protectedResponse = await page.request.get(new URL(protectedPath, base).toString());
    return {
      audience,
      base,
      submitted,
      final_url: page.url(),
      session_status: session.status(),
      protected_path: protectedPath,
      protected_status: protectedResponse.status(),
    };
  } catch (error) {
    return {
      audience,
      base,
      submitted,
      final_url: page.url(),
      error: error instanceof Error ? error.message : String(error),
    };
  }
}

const captures: Capture[] = [];
const accessProbes: Array<Record<string, unknown>> = [];
const capturedAt = new Date().toISOString();
async function persistManifest(): Promise<void> {
  const manifest = {
    captured_at: capturedAt,
    source_sha: sourceSHA === "" ? "NOT_RECORDED" : sourceSHA,
    server_sha: serverSHA === "" ? "NOT_RECORDED" : serverSHA,
    execution_sha: executionSHA === "" ? "NOT_RECORDED" : executionSHA,
    browser: "Chromium",
    executable: chromiumExecutable(),
    web,
    admin: adminSite === "" ? "NOT_RUN" : adminSite,
    portal: portalSite === "" ? "NOT_RUN" : portalSite,
    viewports: VIEWPORTS.map((viewport) => viewport.id),
    gating: "page-specific readiness + PageStatus probe; loading/empty/error/denied cells are never captured",
    validations: "a capture also requires no horizontal overflow, real three-column workspace content at >=1280px and at least three distinct visible keyboard focus targets; nonfocusable body sentinels are ignored",
    access_probes: accessProbes,
    captures,
  };
  await writeFile(resolve(outDir, "manifest.json"), JSON.stringify(manifest, null, 2), "utf8");
}
const browser = await chromium.launch({
  headless: true,
  executablePath: chromiumExecutable(),
  args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
});
const page = await browser.newPage({ viewport: { width: VIEWPORTS[0].width, height: VIEWPORTS[0].height } });
const consoleErrors: string[] = [];
page.on("console", (message) => {
  if (message.type() === "error") consoleErrors.push(message.text());
});
page.on("pageerror", (error) => consoleErrors.push(error.message));
const networkErrors: string[] = [];
page.on("response", (response) => {
  if (response.status() >= 400) {
    const pathname = new URL(response.url()).pathname;
    networkErrors.push(`${response.status()} ${response.request().method()} ${pathname}`);
  }
});
page.on("requestfailed", (request) => {
  networkErrors.push(`${request.method()} ${request.url()} ${request.failure()?.errorText ?? "request failed"}`);
});

try {
  await mkdir(outDir, { recursive: true });

  for (const target of targets) {
    const base = baseOf(target.site);
    if (base === "" || target.path === null) {
      const reason = `${target.needs} was not provided`;
      for (const viewport of VIEWPORTS) {
        captures.push({ target: target.id, viewport: viewport.id, site: target.site, url: null, state: "not_ready", status: "NOT_RUN", reason });
      }
      await persistManifest();
      continue;
    }
    if (target.id === "cases" && username !== "" && password !== "") accessProbes.push(await signIn(page, web));
    if (target.id === "admin-settings" && username !== "" && password !== "") accessProbes.push(await signIn(page, adminSite));
    const url = `${base}${target.path}`;
    let navigated = false;
    let responseStatus = 0;
    for (const viewport of VIEWPORTS) {
      const record: Capture = { target: target.id, viewport: viewport.id, site: target.site, url, state: "not_ready", status: "failed" };
      const file = `${target.id}-${viewport.id}.png`;
      try {
        await page.setViewportSize({ width: viewport.width, height: viewport.height });
        if (!navigated) {
          consoleErrors.length = 0;
          networkErrors.length = 0;
          const response = await page.goto(url, { waitUntil: "load" });
          responseStatus = response?.status() ?? 0;
          navigated = true;
        }
        record.http_status = responseStatus;
        await page.evaluate(() => window.scrollTo(0, 0));
        await target.ready(page);
        record.final_url = page.url();
        const state = await pageState(page);
        record.state = state;
        const measured = await page.evaluate(() => ({
          width: window.innerWidth,
          split: window.matchMedia("(min-width: 1280px)").matches,
          overflow: document.documentElement.scrollWidth - window.innerWidth,
        }));
        record.measured_width = measured.width;
        record.split_layout = measured.split;
        record.horizontal_overflow_px = measured.overflow;
        record.console_errors = [...consoleErrors];
        record.network_errors = [...networkErrors];
        const validation: Record<string, unknown> = { overflow_ok: measured.overflow <= 1 };
        if (target.id === "review-workspace") {
          const workspace = await workspaceAudit(page);
          validation.workspace = workspace;
          const wide = viewport.width >= 1280;
          validation.workspace_ok = Boolean(workspace.ok)
            && (wide ? workspace.columns === 3 : true)
            && Number(workspace.doc_text) > 0
            && Number(workspace.findings) >= 1
            && Number(workspace.inspector_text) > 0
            && workspace.inspector_open === true
            && Number(workspace.inspector_body_text) > 0
            && (wide ? Number(workspace.nav_text) > 0 : true);
        }
        const keyboard = await keyboardAudit(page);
        validation.keyboard = keyboard;
        validation.keyboard_ok = keyboard.distinct >= 3 && keyboard.hidden.length === 0 && keyboard.offscreen.length === 0;
        record.validation = validation;
        if (state !== "ready") {
          record.status = state === target.emptyWhen ? "NOT_RUN" : "failed";
          record.reason = `page reported ${state} after readiness; no screenshot taken`;
        } else if (validation.overflow_ok !== true) {
          record.status = "failed";
          record.reason = `horizontal overflow of ${measured.overflow}px`;
        } else if (validation.workspace_ok === false) {
          record.status = "failed";
          record.reason = `workspace content validation failed: ${JSON.stringify(validation.workspace)}`;
        } else if (validation.keyboard_ok === false) {
          record.status = "failed";
          record.reason = `keyboard focus validation failed: ${JSON.stringify(validation.keyboard)}`;
        } else {
          await page.waitForTimeout(settleMs);
          const png = await page.screenshot({ path: resolve(outDir, file), fullPage: true });
          record.status = "captured";
          record.file = file;
          record.bytes = png.byteLength;
          record.sha256 = createHash("sha256").update(png).digest("hex");
        }
      } catch (error) {
        const state = await pageState(page).catch((): PageState => "unknown");
        record.state = state === "ready" ? "not_ready" : state;
        record.final_url = page.url();
        record.console_errors = [...consoleErrors];
        record.network_errors = [...networkErrors];
        record.diagnostic = await page.evaluate(() => {
          const root = document.querySelector("#root")?.firstElementChild;
          return {
            pathname: location.pathname,
            root_tag: root?.tagName ?? null,
            root_class: typeof root?.className === "string" ? root.className : null,
            page_status_class: document.querySelector(".page-status")?.className ?? null,
            headings: document.querySelectorAll("h1").length,
            login_form: document.querySelectorAll('input[name="password"]').length > 0,
            case_filters: document.querySelectorAll(".cases-filters").length,
            document_rows: document.querySelectorAll(".document-list li[data-document-id]").length,
            workspace_grid: document.querySelectorAll(".workspace-grid").length,
            api_docs_browser: document.querySelectorAll(".api-docs-browser").length,
            admin_table: document.querySelectorAll("table.data-table").length,
          };
        });
        record.reason = `readiness failed: ${error instanceof Error ? error.message : String(error)}`;
      }
      captures.push(record);
      await persistManifest();
    }
  }
} finally {
  await browser.close();
}

await persistManifest();

const failed = captures.filter((capture) => capture.status === "failed");
const notRun = captures.filter((capture) => capture.status === "NOT_RUN");
const states = captures.reduce<Record<string, number>>((counts, capture) => {
  counts[capture.state] = (counts[capture.state] ?? 0) + 1;
  return counts;
}, {});
console.log(JSON.stringify({
  out: outDir,
  captured: captures.length - failed.length - notRun.length,
  failed: failed.length,
  not_run: notRun.length,
  states,
  horizontal_overflow: captures
    .filter((capture) => (capture.horizontal_overflow_px ?? 0) > 1)
    .map((capture) => `${capture.target}@${capture.viewport}`),
  validated_captures: captures.filter((capture) => capture.status === "captured" && capture.validation).length,
  result: failed.length === 0 ? "screenshot matrix completed" : "screenshot matrix had failures",
}, null, 2));
assert.equal(failed.length, 0, `screenshot matrix failures: ${failed.map((capture) => `${capture.target}@${capture.viewport}: ${capture.reason}`).join(" | ")}`);
