import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { type Locator, type Page } from "playwright";
import { parseReviewWorkspace } from "../src/api/parse";
import { formatDateTime } from "../src/domain/review";

function antSelectRoot(select: Locator): Locator {
  return select.locator("xpath=ancestor-or-self::*[contains(concat(' ', normalize-space(@class), ' '), ' ant-select ')][1]");
}

export async function selectedAntLabel(select: Locator): Promise<string> {
  const item = antSelectRoot(select).locator(".ant-select-content").first();
  if ((await item.count()) === 0) return "";
  const title = await item.getAttribute("title");
  return (title ?? (await item.textContent()) ?? "").replace(/\s+/g, " ").trim();
}

async function selectionApplied(select: Locator, matches: (label: string) => boolean): Promise<boolean> {
  const deadline = Date.now() + 2000;
  do {
    const label = await selectedAntLabel(select);
    if (label && matches(label)) return true;
    await Bun.sleep(50);
  } while (Date.now() < deadline);
  return false;
}

function cssQuoted(value: string): string {
  return `"${value.replaceAll("\\", "\\\\").replaceAll('"', '\\"').replaceAll("\n", "\\a ")}"`;
}

async function controlledDropdown(page: Page, select: Locator): Promise<Locator> {
  let controls = (await select.getAttribute("aria-controls"))?.trim() ?? "";
  if ((await select.getAttribute("aria-expanded")) !== "true") await select.click();
  controls = (await select.getAttribute("aria-controls"))?.trim() ?? controls;
  assert.ok(controls, "Ant Design combobox omitted its controlled popup ID");
  const ariaListbox = page.locator(`[id=${cssQuoted(controls)}]`);
  const dropdown = page.locator(".ant-select-dropdown:visible").filter({ has: ariaListbox });
  const visibleDropdown = dropdown.first();
  try {
    await visibleDropdown.waitFor({ state: "visible", timeout: 5000 });
  } catch {
    assert.fail(`no visible Ant Design popup is associated with aria-controls=${controls}`);
  }
  return visibleDropdown;
}

async function clickExactOption(listbox: Locator, label: string): Promise<void> {
  const titled = listbox.locator(`.ant-select-item-option[title=${cssQuoted(label)}]`);
  const titledCount = await titled.count();
  if (titledCount === 1) {
    await titled.waitFor({ state: "visible", timeout: 5000 });
    await titled.click();
    return;
  }
  assert.equal(titledCount, 0, `Ant Design listbox contains duplicate exact titles for ${label}`);
  const option = listbox.getByRole("option", { name: label, exact: true });
  assert.equal(await option.count(), 1, `Ant Design listbox does not contain one exact option named ${label}`);
  await option.waitFor({ state: "visible", timeout: 5000 });
  await option.click();
}

export async function clickAntOptionByAttribute(
  page: Page,
  select: Locator,
  attribute: "title" | "aria-label",
  value: string,
): Promise<void> {
  const dropdown = await controlledDropdown(page, select);
  const option = dropdown.locator(`.ant-select-item-option[${attribute}=${cssQuoted(value)}]`);
  assert.equal(await option.count(), 1, `Ant Design popup does not contain one visible option with ${attribute}=${value}`);
  await option.waitFor({ state: "visible", timeout: 5000 });
  await option.click();
}

async function captureAntSelectFailure(page: Page, labels: string[]): Promise<void> {
  const directory = process.env.E2E_SCREENSHOT_DIR?.trim() || fileURLToPath(new URL("../../artifacts/screenshots/", import.meta.url));
  const slug = labels.join("-").replace(/[^\p{L}\p{N}-]+/gu, "_").slice(0, 60) || "unknown";
  try { await mkdir(directory, { recursive: true }); await page.screenshot({ path: join(directory, `ant-select-${slug}-${Date.now()}.png`) }); }
  catch { /* Diagnostics must not mask the original failure. */ }
}

export async function chooseAntOption(page: Page, select: Locator, matches: (label: string) => boolean): Promise<void> {
  let labels: string[] = [];
  for (let attempt = 0; attempt < 5; attempt++) {
    if (matches(await selectedAntLabel(select))) return;
    let dropdown: Locator;
    try { dropdown = await controlledDropdown(page, select); } catch { await Bun.sleep(100); continue; }
    const options = dropdown.locator(".ant-select-item-option");
    try { await options.first().waitFor({ state: "visible", timeout: 5000 }); } catch { await Bun.sleep(100); continue; }
    const measured = await options.evaluateAll((elements) => elements.map((element) => ({
      label: element.getAttribute("title") ?? element.getAttribute("aria-label") ?? element.textContent?.trim() ?? "",
      visible: element.getBoundingClientRect().height > 0,
    })));
    labels = measured.map((option) => option.label);
    const targetLabels = measured.filter((option) => option.visible && matches(option.label)).map((option) => option.label);
    if (targetLabels.length !== 1) break;
    await clickExactOption(dropdown, targetLabels[0]!);
    if (await selectionApplied(select, matches)) return;
    await Bun.sleep(100);
  }
  await captureAntSelectFailure(page, labels);
  assert.fail(`no visible Ant Design option matched: ${JSON.stringify(labels)}`);
}

async function saveRawEvidence(filename: string, value: unknown): Promise<void> {
  const directory = process.env.CAW_DELIVERY_DIR;
  if (!directory) return;
  const target = join(directory, "browser-evidence");
  await mkdir(target, { recursive: true });
  await writeFile(join(target, filename), `${JSON.stringify(value, null, 2)}\n`, "utf8");
}

export async function selectReviewAttempt(page: Page, web: string, caseID: string, runID: string, attemptID: string, select: Locator): Promise<void> {
  const caseResponse = await page.request.get(`${web}/api/cases/${caseID}`);
  assert.equal(caseResponse.status(), 200, "case detail for exact review selection did not load");
  const caseDetail = await caseResponse.json();
  let currentRunID = String(caseDetail.latest_run?.id ?? "");
  let runOrder = 0;
  let expectedLabel: string | null = null;
  const seen = new Set<string>();
  while (currentRunID) {
    assert.ok(!seen.has(currentRunID), "review history contains a cycle while selecting an exact attempt");
    seen.add(currentRunID);
    const response = await page.request.get(`${web}/api/cases/${caseID}/reviews/${currentRunID}`);
    assert.equal(response.status(), 200, `review ${currentRunID} for exact selection did not load`);
    const rawWorkspace = await response.json();
    const workspace = parseReviewWorkspace(rawWorkspace);
    if (currentRunID === runID) {
      const attempts = workspace.review_attempts?.attempts ?? [];
      const attempt = attempts.find((item) => item.id === attemptID) ?? (workspace.review_attempts?.attempt?.id === attemptID ? workspace.review_attempts.attempt : null);
      assert.ok(attempt, `review ${runID} does not contain exact attempt ${attemptID}`);
      expectedLabel = `审查记录 ${runOrder + 1}，第 ${attempt.ordinal} 次`;
      break;
    }
    currentRunID = workspace.previous_run_id ?? "";
    runOrder++;
  }
  assert.ok(expectedLabel, `run ${runID} is not in the case review history`);
  await chooseAntOption(page, select, (label) => label.startsWith(expectedLabel!));
  await select.waitFor({ state: "visible" });
  const selectedLabel = await selectedAntLabel(select);
  if (!selectedLabel.startsWith(expectedLabel!)) {
    let selectorDOM: unknown;
    try {
      selectorDOM = await select.evaluate((element) => {
        const root = element.closest(".ant-select");
        const controls = element.getAttribute("aria-controls");
        const popup = controls ? document.getElementById(controls) : null;
        const visiblePopups = Array.from(document.querySelectorAll<HTMLElement>(".ant-select-dropdown")).filter((item) => item.getBoundingClientRect().height > 0).map((item) => item.outerHTML.slice(0, 12000));
        return { element: element.outerHTML.slice(0, 4000), role: element.getAttribute("role"), id: element.id, ariaExpanded: element.getAttribute("aria-expanded"), ariaControls: controls, popup: popup?.outerHTML.slice(0, 12000) ?? null, root: root?.outerHTML.slice(0, 12000) ?? null, activeElement: document.activeElement instanceof HTMLElement ? document.activeElement.outerHTML.slice(0, 4000) : null, visiblePopups };
      });
    } catch (cause) { selectorDOM = { capture_error: String(cause) }; }
    const exactResponse = await page.request.get(`${web}/api/cases/${caseID}/reviews/${runID}?attempt_id=${encodeURIComponent(attemptID)}`);
    await saveRawEvidence(`review-selector-${runID}-${attemptID}.json`, { case_id: caseID, run_id: runID, attempt_id: attemptID, selector_url: page.url(), expected_label: expectedLabel, selected_label: selectedLabel, exact_workspace_http_status: exactResponse.status(), exact_workspace: exactResponse.ok() ? await exactResponse.json() : await exactResponse.text(), selector_dom: selectorDOM });
  }
  assert.ok(selectedLabel.startsWith(expectedLabel!), `review selector did not retain exact run/attempt option; expected ${expectedLabel}, observed ${selectedLabel || "<empty>"}`);
  const selectedResponse = await page.request.get(`${web}/api/cases/${caseID}/reviews/${runID}?attempt_id=${encodeURIComponent(attemptID)}`);
  assert.equal(selectedResponse.status(), 200, `exact review attempt ${runID}/${attemptID} could not be read`);
  const selectedWorkspace = parseReviewWorkspace(await selectedResponse.json());
  assert.equal(selectedWorkspace.run?.id, runID, "selected review run identity differs from requested run");
  assert.equal(selectedWorkspace.review_attempts?.attempt?.id, attemptID, "selected review attempt identity differs from requested attempt");
}

export async function selectReportOption(page: Page, select: Locator, report: { id: string; version: number; created_at: string }): Promise<void> {
  const expectedLabel = `版本 ${report.version}，创建于 ${formatDateTime(report.created_at)}`;
  await chooseAntOption(page, select, (label) => label === expectedLabel);
  const selectedLabel = await selectedAntLabel(select);
  assert.equal(selectedLabel, expectedLabel, `selected report does not match exact report ${report.id}`);
}
