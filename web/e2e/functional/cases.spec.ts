import { test, expect, operations, apiURL, webURL, body, uiLogin, detail } from "./helpers";
import { parseCaseList } from "../../src/api/parse";

test("case UI creates, searches, filters, sorts and opens a real case", operations("createCase", "listCases", "getCase"), async ({ page }) => {
  await uiLogin(page);
  await page.getByRole("link", { name: "案件", exact: true }).click();
  await page.getByRole("button", { name: "新建案件", exact: true }).click();
  const dialog = page.getByRole("dialog");
  const title = `Playwright 案件 ${Date.now()}`;
  await dialog.getByLabel("标题", { exact: true }).fill(title);
  await dialog.getByLabel("合同类型", { exact: true }).selectOption("service");
  await dialog.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page).toHaveURL(/\/cases\/case_[^/?]+$/);
  await expect(page.getByRole("heading", { name: title, exact: true })).toBeVisible();
  const caseID = new URL(page.url()).pathname.split("/").at(-1)!;
  const created = await detail(page.request, caseID);
  expect(created.title).toBe(title);
  expect(created.business_line).toBe("ENT");
  expect(created.scenario).toBe("telecom_dedicated_line");
  await page.getByRole("link", { name: "返回案件列表", exact: true }).click();
  await page.getByLabel("搜索案件", { exact: true }).fill(title);
  await expect(page.getByRole("link", { name: title, exact: true })).toBeVisible();
  await page.getByLabel("运行状态", { exact: true }).selectOption("failed");
  await expect(page.getByRole("link", { name: title, exact: true })).toHaveCount(0);
  await page.getByLabel("运行状态", { exact: true }).selectOption("all");
  await page.getByRole("button", { name: /按案件排序/ }).click();
  await expect(page.locator('th[aria-sort="ascending"]')).toContainText("案件");
  await page.getByRole("button", { name: /按案件排序/ }).click();
  await expect(page.locator('th[aria-sort="descending"]')).toContainText("案件");
  await page.getByRole("link", { name: title, exact: true }).click();
  await expect(page.getByRole("heading", { name: title, exact: true })).toBeVisible();
  const list = parseCaseList(await body(await page.request.get(`${apiURL}/api/cases`)));
  expect(list.find(item => item.id === caseID)?.title).toBe(title);
});

test("material UI uploads TXT and MD, exact originals, reparse and superseded versions", operations("uploadDocument", "getOriginal", "reparseDocument", "getCase"), async ({ page }) => {
  await uiLogin(page);
  const created = await body(await page.request.post(`${apiURL}/api/cases`, { data: { title: `原件历史 ${Date.now()}`, contract_type: "procurement_ict" } }), 201);
  const caseID = String(created.id);
  await page.goto(`${webURL}/cases/${caseID}`);
  const originalText = "采购文件要求质保36个月。😀\r\n合同总价100万元。";
  async function register(role: string, filename: string, text: string) {
    await page.getByLabel("材料角色", { exact: true }).selectOption(role);
    await page.getByLabel("文件", { exact: true }).setInputFiles({ name: filename, mimeType: "text/plain", buffer: Buffer.from(text) });
    const saved = page.waitForResponse(response => response.request().method() === "POST" && response.url().endsWith(`/cases/${caseID}/documents`));
    await page.getByRole("button", { name: "上传", exact: true }).click();
    expect((await saved).status()).toBe(201);
    await expect(page.getByText(filename, { exact: true })).toBeVisible();
  }
  await register("procurement", "合成采购.txt", originalText);
  await register("draft_contract", "拟签.md", "拟签合同约定质保24个月。");
  const first = await detail(page.request, caseID);
  const doc = first.documents.find(item => item.filename === "合成采购.txt")!;
  expect(doc.parse_status).toBe("ready");
  const bytes = await page.request.get(`${apiURL}/api/cases/${caseID}/documents/${doc.id}/original`);
  expect(await bytes.body()).toEqual(Buffer.from(originalText));
  expect(bytes.headers()["x-content-type-options"]).toBe("nosniff");
  const parsedID = doc.parsed_document?.id;
  await page.getByRole("button", { name: "重新解析 合成采购.txt", exact: true }).click();
  await expect.poll(async () => (await detail(page.request, caseID)).documents.find(item => item.id === doc.id)?.parsed_document?.id).not.toBe(parsedID);
  await register("procurement", "修订采购.txt", "采购文件要求质保48个月。");
  const revised = await detail(page.request, caseID);
  expect(revised.documents.find(item => item.id === doc.id)?.superseded).toBe(true);
  expect(revised.documents.find(item => item.filename === "修订采购.txt")?.superseded).toBe(false);
  const old = await page.request.get(`${apiURL}/api/cases/${caseID}/documents/${doc.id}/original`);
  expect(await old.body()).toEqual(Buffer.from(originalText));
});
