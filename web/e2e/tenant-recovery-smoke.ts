import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright";
import type { ReviewWorkspace } from "../src/api/types";

type SessionContext = {
  version: string;
  csrf: string;
  tenant: string;
};

const web = required("E2E_WEB");
const out = required("CAW_DELIVERY_DIR");
const screenshots = required("E2E_SCREENSHOT_DIR");
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_BIN,
  headless: true,
  args: ["--no-sandbox", "--disable-dev-shm-usage"],
});
const context = await browser.newContext();
await context.tracing.start({ screenshots: true, snapshots: true, sources: true });
const page = await context.newPage();
const evidence: Record<string, unknown> = {
  data_class: "synthetic",
  auth: "loopback OIDC + PostgreSQL session authority",
  model: "browserModels synthetic",
  status: "NOT_RUN",
};

try {
  await page.goto(`${web}/login`);
  const oidcLogin = page.getByRole("link", { name: "通过组织身份提供方登录" });
  try {
    await oidcLogin.waitFor({ timeout: 10_000 });
  } catch {
    // Vite may finish dependency optimization while the first page load is in
    // flight. Retry the page once only if the app shell did not render.
    evidence.startup_reload = true;
    await page.reload({ waitUntil: "domcontentloaded" });
    await oidcLogin.waitFor({ timeout: 10_000 });
  }
  await oidcLogin.click();
  await page.getByRole("heading", { name: "选择组织身份" }).waitFor();
  await page.getByRole("radio", { name: /合成租户 A/ }).check();
  await page.getByRole("button", { name: "进入工作台" }).click();
  await page.getByRole("heading", { name: "登记主合同并建案" }).waitFor();

  // Capture the form's original authority once. The stale command below keeps
  // these exact values; it must not refresh or recapture after switching.
  const originalContext = await readSession();
  if (originalContext.tenant !== "smoke-tenant-a" || !originalContext.version || !originalContext.csrf) {
    throw new Error("OIDC did not establish tenant A with a command context");
  }

  await page.getByLabel("切换工作上下文").click();
  await page.getByText(/合成租户 B/).last().click();
  await pollUntil("tenant B session", async () => page.evaluate(async () => {
    const response = await fetch("/api/session");
    if (!response.ok) return false;
    const session = await response.json();
    return session.active_context.tenant_id === "smoke-tenant-b";
  }));
  const currentContext = await readSession();
  if (currentContext.tenant !== "smoke-tenant-b" || currentContext.version === originalContext.version) {
    throw new Error(`context switch did not advance authority: before=${JSON.stringify(originalContext)} after=${JSON.stringify(currentContext)}`);
  }

  const stale = await page.evaluate(async (captured) => {
    const response = await fetch("/api/cases", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Context-Version": captured.version,
        "X-CSRF-Token": captured.csrf,
      },
      body: JSON.stringify({ title: "陈旧表单不得写入", contract_type: "procurement_ict" }),
    });
    return { status: response.status, body: await response.text() };
  }, originalContext);
  if (stale.status !== 409) {
    throw new Error(`old context command returned ${stale.status}: ${stale.body}; before=${JSON.stringify(originalContext)} after=${JSON.stringify(currentContext)}`);
  }

  // New UI commands capture the revalidated session and its current tokens.
  if (
    !currentContext.csrf
  ) {
    throw new Error("fresh session did not revalidate to tenant B");
  }
  await page.getByLabel("案件标题").fill("合成租户 B 的嵌入式审查");
  await page.locator('input[type="file"]').setInputFiles({
    name: "draft.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("拟签合同约定质保24个月。合同总价100万元。"),
  });
  await page.getByRole("button", { name: "上传主合同并建立案件" }).click();
  await page.waitForURL(/\/cases\/[^/]+(?:\?|$)/, { timeout: 60_000 });
  const homeCaseID = new URL(page.url()).pathname.split("/").at(-1);
  if (!homeCaseID) throw new Error("embedded intake did not navigate to its case");
  await page.locator(".material-current-set").getByText("draft.txt", { exact: true }).waitFor();
  const postIntakeContext = await readSession();
  if (postIntakeContext.tenant !== "smoke-tenant-b" || !postIntakeContext.version || !postIntakeContext.csrf) {
    throw new Error("embedded intake did not retain the validated tenant B session");
  }

  // The embedded intake intentionally creates an ENT governed telecom case;
  // this test only needs to establish that real browser intake completed in B.
  // Exercise legacy unspecified review behavior on a separate case created by
  // the same browser session and current command authority.
  const legacyCase = await page.evaluate(async (captured) => {
    const response = await fetch("/api/cases", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-Context-Version": captured.version,
        "X-CSRF-Token": captured.csrf,
        "Idempotency-Key": `tenant-recovery-${crypto.randomUUID()}`,
      },
      body: JSON.stringify({
        title: "合成租户 B 的 legacy 重试审查",
        contract_type: "procurement_ict",
        business_line: "",
        scenario: "",
      }),
    });
    return { status: response.status, body: await response.text() };
  }, postIntakeContext);
  if (legacyCase.status !== 201) {
    throw new Error(`legacy case creation returned ${legacyCase.status}: ${legacyCase.body}`);
  }
  const legacyCaseID = (JSON.parse(legacyCase.body) as { id?: string }).id;
  if (!legacyCaseID) throw new Error("legacy case creation response has no id");
  await page.goto(`${web}/cases/${legacyCaseID}`);
  await page.getByLabel("材料角色").waitFor();
  await addMaterial("采购文件", "purchase.txt", "采购文件要求质保36个月。合同总价100万元。");
  await addMaterial("拟签合同", "draft.txt", "拟签合同约定质保24个月。合同总价100万元。");
  await page.getByRole("button", { name: "开始审查" }).click();
  await page.getByRole("heading", { name: "审查工作区" }).waitFor();

  const retryButton = page.getByRole("button", { name: "重新执行未完成的审查" });
  await retryButton.waitFor({ timeout: 60_000 });
  const route = new URL(page.url());
  const routeMatch = route.pathname.match(/\/cases\/([^/]+)\/reviews\/([^/]+)/);
  if (!routeMatch) throw new Error(`review route missing: ${route.pathname}`);
  const caseID = routeMatch[1]!;
  const runID = routeMatch[2]!;
  const readWorkspace = (attempt?: string) =>
    page.evaluate(async ({ caseID, runID, attempt }) => {
      const query = attempt ? `?attempt_id=${encodeURIComponent(attempt)}` : "";
      const response = await fetch(`/api/cases/${caseID}/reviews/${runID}${query}`);
      if (!response.ok) throw new Error(`workspace ${response.status}`);
      return (await response.json()) as ReviewWorkspace;
    }, { caseID, runID, attempt });

  await pollUntil("initial attempt terminal and frozen", async () => {
    const workspace = await readWorkspace();
    const attempt = workspace.review_attempts?.attempt;
    return Boolean(
      attempt?.history_kind === "frozen" &&
      attempt.frozen_result_available === true &&
      isTerminal(attempt.state),
    );
  }, 60_000);

  const firstWorkspace = await readWorkspace();
  const firstAttempt = firstWorkspace.review_attempts?.attempt;
  if (
    firstAttempt?.history_kind !== "frozen" ||
    firstAttempt.frozen_result_available !== true ||
    !firstAttempt.parsed_document_ids?.length
  ) {
    throw new Error(`initial attempt did not freeze source identity before terminal state: ${JSON.stringify(firstAttempt)}`);
  }
  if (!(await retryButton.isEnabled())) throw new Error("real retry action is unavailable");
  await retryButton.click();

  await pollUntil("retry attempt terminal and frozen", async () => {
    const workspace = await readWorkspace();
    const attempt = workspace.review_attempts?.attempt;
    return Boolean(
      workspace.review_attempts?.attempts?.length === 2 &&
      attempt?.id !== firstAttempt.id &&
      attempt?.ordinal === 2 &&
      attempt.retry_of_attempt_id === firstAttempt.id &&
      attempt.history_kind === "frozen" &&
      attempt.frozen_result_available === true &&
      isTerminal(attempt.state),
    );
  }, 90_000);

  const latest = await readWorkspace();
  const oldAttempt = await readWorkspace(firstAttempt.id);
  const retryAttempt = latest.review_attempts?.attempt;
  if (
    retryAttempt?.history_kind !== "frozen" ||
    retryAttempt.frozen_result_available !== true ||
    retryAttempt?.parsed_document_ids?.join("|") !== firstAttempt.parsed_document_ids.join("|")
  ) {
    throw new Error(`retry attempt differs from the frozen source: first=${JSON.stringify(firstAttempt)} retry=${JSON.stringify(retryAttempt)} attempts=${JSON.stringify(latest.review_attempts?.attempts)}`);
  }
  if (
    oldAttempt.review_attempts?.attempt?.history_kind !== "frozen" ||
    oldAttempt.review_attempts?.attempt?.frozen_result_available !== true ||
    oldAttempt.review_attempts?.attempt?.parsed_document_ids?.join("|") !== firstAttempt.parsed_document_ids.join("|") ||
    oldAttempt.review_attempts?.attempt?.id !== firstAttempt.id
  ) {
    throw new Error("old frozen attempt identity changed");
  }

  const locatedCitations = latest.findings
    ?.flatMap((finding) => finding.evidence ?? [])
    .filter((citation) => citation.status === "located") ?? [];
  if (!locatedCitations.length) throw new Error("retry produced no located frozen citation");
  if (locatedCitations.some((citation) => !firstAttempt.parsed_document_ids!.includes(String(citation.parsed_document_id)))) {
    throw new Error("citation parse identity drifted after retry");
  }

  evidence.status = "PASS";
  evidence.context = {
    stale_status: stale.status,
    before_tenant: originalContext.tenant,
    after_tenant: postIntakeContext.tenant,
    before_version: originalContext.version,
    after_version: postIntakeContext.version,
  };
  evidence.home_intake_case_id = homeCaseID;
  evidence.legacy_case_id = legacyCaseID;
  evidence.tenant_id = postIntakeContext.tenant;
  evidence.case_id = caseID;
  evidence.run_id = runID;
  evidence.attempts = {
    first: {
      id: firstAttempt.id,
      ordinal: firstAttempt.ordinal,
      state: firstAttempt.state,
      retry_of_attempt_id: firstAttempt.retry_of_attempt_id,
      history_kind: firstAttempt.history_kind,
      frozen_result_available: firstAttempt.frozen_result_available,
      parsed_document_ids: firstAttempt.parsed_document_ids,
    },
    retry: {
      id: retryAttempt?.id,
      ordinal: retryAttempt?.ordinal,
      state: retryAttempt?.state,
      retry_of_attempt_id: retryAttempt?.retry_of_attempt_id,
      history_kind: retryAttempt?.history_kind,
      frozen_result_available: retryAttempt?.frozen_result_available,
      parsed_document_ids: retryAttempt?.parsed_document_ids,
      located_citations: locatedCitations.length,
    },
  };
  await mkdir(screenshots, { recursive: true });
  await page.screenshot({ path: join(screenshots, "tenant-recovery.png"), fullPage: true });
} catch (error) {
  evidence.status = "FAIL";
  evidence.error = String(error);
  evidence.failure_url = page.url();
  evidence.failure_text = await page.locator("body").innerText().catch(() => "");
  await mkdir(screenshots, { recursive: true });
  await page.screenshot({ path: join(screenshots, "tenant-recovery-failure.png"), fullPage: true }).catch(() => undefined);
  throw error;
} finally {
  await writeFile(resolve(out, "tenant-recovery-evidence.json"), `${JSON.stringify(evidence, null, 2)}\n`);
  await context.tracing.stop({ path: resolve(out, "tenant-recovery-trace.zip") });
  await browser.close();
}

async function readSession(): Promise<SessionContext> {
  return page.evaluate(async () => {
    const response = await fetch("/api/session");
    if (!response.ok) throw new Error(`session ${response.status}`);
    const session = await response.json();
    return {
      version: session.active_context.context_version as string,
      csrf: session.csrf_token as string,
      tenant: session.active_context.tenant_id as string,
    };
  });
}

async function pollUntil(
  description: string,
  predicate: () => Promise<boolean>,
  timeoutMs = 30_000,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`timed out waiting for ${description}`);
}

function isTerminal(state: string | undefined): boolean {
  return ["awaiting_human", "completed", "partial", "failed", "cancelled"].includes(state ?? "");
}

async function addMaterial(role: string, name: string, text: string): Promise<void> {
  if (role !== "采购文件") {
    await page.getByLabel("材料角色").click();
    await page.getByText(role, { exact: true }).last().click();
  }
  await page.locator('input[type="file"]').setInputFiles({
    name,
    mimeType: "text/plain",
    buffer: Buffer.from(text),
  });
  await page.getByRole("button", { name: "新增材料" }).click();
  await page.locator('p[role="status"][data-document-id]').filter({ hasText: "材料已登记" }).waitFor();
  await page.waitForFunction((filename) => {
    const document = [...window.document.querySelectorAll(".document-list li")].find((item) => item.textContent?.includes(filename));
    return Boolean(document?.querySelector('[data-status="ready"]'));
  }, name);
}

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}
