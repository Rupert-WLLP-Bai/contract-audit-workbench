import { readCDPRequestPostData } from "./cdp-request-post-data";
import { chooseAntOption, clickAntOptionByAttribute } from "./c1b-select";
import assert from "node:assert/strict";
import type { Locator, Page } from "playwright";

export type MaterialUpload = { role: string; purpose?: string; filename: string; text: string };
export type PrimaryMaterial = {
  role?: string;
  purpose?: string;
  material_id: string;
  current_document_id: string;
  current_version: number;
};
type UploadResult = { id?: string; material_id?: string; version?: number; replayed?: boolean };

const roleLabels: Record<string, string> = {
  procurement: "采购文件",
  award: "中标文件",
  draft_contract: "拟签合同",
  attachment: "附件",
};

const purposeLabels: Record<string, string> = {
  primary_contract: "主合同",
  supplement: "补充协议",
  acceptance: "验收材料",
  invoice: "发票材料",
  correspondence: "往来函件",
  other: "其他附件",
};

export async function selectMaterialOption(page: Page, label: string, optionText: string): Promise<void> {
  const select = page.getByLabel(label, { exact: true });
  await chooseAntOption(page, select, (candidate) => candidate === optionText);
}

export async function uploadNewMaterial(page: Page, material: MaterialUpload): Promise<UploadResult & { id: string; material_id: string; version: number; filename: string }> {
  const roleLabel = roleLabels[material.role];
  assert.ok(roleLabel, `unknown material role ${material.role}`);
  await selectMaterialOption(page, "材料角色", roleLabel);
  if (material.role === "attachment") {
    const purpose = material.purpose ?? "supplement";
    assert.ok(purposeLabels[purpose], `unknown attachment purpose ${purpose}`);
    await selectMaterialOption(page, "附件用途", purposeLabels[purpose]!);
  }
  await selectMaterialOption(page, "材料操作", "新增材料");
  await setMaterialFile(page, material);
  const button = page.getByRole("button", { name: "新增材料", exact: true });
  const result = await submitMaterial(page, button, material.filename);
  assert.equal(result.version, 1, `upload ${material.filename} did not create version 1`);
  return { ...result, filename: material.filename } as UploadResult & { id: string; material_id: string; version: number; filename: string };
}

export async function replacePrimaryMaterial(
  page: Page,
  material: MaterialUpload,
  target: PrimaryMaterial,
  targetFilename: string,
): Promise<UploadResult & { id: string; material_id: string; version: number; filename: string }> {
  return await replaceMaterial(page, material, target, targetFilename, "主合同");
}

export async function replaceMaterial(
  page: Page,
  material: MaterialUpload,
  target: PrimaryMaterial,
  targetFilename: string,
  targetUsage: string,
): Promise<UploadResult & { id: string; material_id: string; version: number; filename: string }> {
  const roleLabel = roleLabels[material.role];
  assert.ok(roleLabel, `unknown material role ${material.role}`);
  await selectMaterialOption(page, "材料角色", roleLabel);
  if (material.role === "attachment") {
    const purpose = material.purpose ?? "supplement";
    assert.ok(purposeLabels[purpose], `unknown attachment purpose ${purpose}`);
    await selectMaterialOption(page, "附件用途", purposeLabels[purpose]!);
  }
  await selectMaterialOption(page, "材料操作", "替换选中的材料");
  const targetField = page.locator('.upload-form input[role="combobox"][aria-label="要替换的材料"]');
  await targetField.waitFor({ state: "visible" });
  await page.keyboard.press("Escape").catch(() => {});
  await targetField.click();
  const listboxId = await targetField.getAttribute("aria-controls");
  assert.ok(listboxId, "target material combobox omitted its controlled listbox ID");
  const identityOptions = await page.locator(`#${listboxId} [role="option"]`).evaluateAll((elements) => elements.map((element) => ({
    label: element.getAttribute("aria-label") ?? "",
    materialId: element.textContent?.trim() ?? "",
  })));
  const identityOption = identityOptions.find((item) => item.materialId === target.material_id && item.label.startsWith(targetFilename));
  assert.ok(identityOption, `target material ID ${target.material_id} did not map to ${targetFilename} in its own listbox: ${JSON.stringify(identityOptions)}`);
  await clickAntOptionByAttribute(page, targetField, "title", identityOption.label);
  const filenameLabel = await targetField.evaluate((element) => {
    const wrapper = element.closest(".ant-select");
    return wrapper?.querySelector(".ant-select-content")?.getAttribute("title") ??
      wrapper?.querySelector(".ant-select-content")?.textContent?.trim() ?? "";
  });
  const usageNotice = `将替换“${targetFilename}”（${targetUsage}）当前第 ${target.current_version} 版；若版本已更新，请刷新重试。`;
  assert.ok(filenameLabel.startsWith(targetFilename), `selected target label changed: ${filenameLabel}`);
  await page.getByText(usageNotice, { exact: true }).waitFor();
  await setMaterialFile(page, material);
  const button = page.getByRole("button", { name: "替换材料", exact: true });
  const result = await submitMaterial(page, button, material.filename, {
    materialId: target.material_id,
    replacesDocumentId: target.current_document_id,
  });
  assert.equal(result.material_id, target.material_id, "replacement changed material identity");
  assert.equal(result.version, target.current_version + 1, "replacement did not increment the selected version");
  assert.equal(result.replayed, false, "first explicit replacement unexpectedly replayed");
  return { ...result, filename: material.filename } as UploadResult & { id: string; material_id: string; version: number; filename: string };
}

async function setMaterialFile(page: Page, material: MaterialUpload): Promise<void> {
  const input = page.getByLabel("文件", { exact: true });
  await input.setInputFiles({ name: material.filename, mimeType: "text/plain", buffer: Buffer.from(material.text, "utf8") });
  await page.waitForFunction(() => {
    const button = Array.from(document.querySelectorAll("button")).find((item) =>
      ["新增材料", "替换材料"].includes(item.getAttribute("aria-label") ?? item.textContent?.trim() ?? ""),
    );
    return button instanceof HTMLButtonElement && !button.disabled;
  });
}

async function submitMaterial(
  page: Page,
  button: Locator,
  filename: string,
  replacement?: { materialId: string; replacesDocumentId: string },
): Promise<UploadResult & { id: string; material_id: string; version: number }> {
  await button.scrollIntoViewIfNeeded();
  const cdp = replacement ? await page.context().newCDPSession(page) : null;
  let cdpRequestId: string | null = null;
  const cdpSessionId = cdp ? "playwright-page-target" : null;
  let cdpPostData = "";
  let cdpPostDataError = "";
  let cdpPostDataReady: Promise<void> | null = null;
  if (cdp) {
    await cdp.send("Network.enable");
    cdp.on("Network.requestWillBeSent", (event: { requestId: string; request: { url: string; method: string; postData?: string } }) => {
      if (event.request.method === "POST" && event.request.url.endsWith("/documents")) {
        cdpRequestId = event.requestId;
        cdpPostDataReady = readCDPRequestPostData({
          requestId: event.requestId,
          sessionId: cdpSessionId ?? undefined,
          postData: event.request.postData,
          getRequestPostData: (requestId, sessionId) => {
            if (!cdp || sessionId !== cdpSessionId) throw new Error(`unexpected CDP target session ${sessionId}`);
            return cdp.send("Network.getRequestPostData", { requestId });
          },
        }).then((body) => {
          cdpPostData = body;
        }).catch((error: unknown) => {
          cdpPostDataError = error instanceof Error ? error.message : String(error);
        });
      }
    });
  }
  const [request, response] = await Promise.all([
    page.waitForRequest((candidate) => candidate.method() === "POST" && candidate.url().endsWith("/documents")),
    page.waitForResponse((candidate) => candidate.request().method() === "POST" && candidate.url().endsWith("/documents")),
    button.click(),
  ]).then(([request, response]) => [request, response] as const);
  assert.equal(response.status(), 201, `upload ${filename} returned HTTP ${response.status()}`);
  const result = await response.json() as UploadResult;
  assert.ok(result.id && result.material_id && result.version, `upload ${filename} omitted material version identity`);
  if (replacement) {
    const contentType = request.headers()["content-type"] ?? "";
    assert.ok(contentType.includes("multipart/form-data"), `replacement request was not multipart: ${contentType}`);
    assert.ok(cdp && cdpRequestId && cdpSessionId, "replacement request did not expose its CDP request and target session IDs");
    assert.ok(cdpPostDataReady, `CDP request-body capture was not started for request ${cdpRequestId} in session ${cdpSessionId}`);
    await Promise.race([
      cdpPostDataReady,
      new Promise<never>((_, reject) => setTimeout(() => reject(new Error(`timed out awaiting request ${cdpRequestId} body in session ${cdpSessionId}`)), 10000)),
    ]);
    assert.ok(!cdpPostDataError, `CDP request-body capture failed for request ${cdpRequestId} in session ${cdpSessionId}: ${cdpPostDataError}`);
    assert.ok(cdpPostData, `CDP request-body capture was empty for request ${cdpRequestId} in session ${cdpSessionId}`);
    const body = cdpPostData;
    assert.ok(body.includes(`name="material_id"\r\n\r\n${replacement.materialId}\r\n`), "replacement request omitted the selected material identity");
    assert.ok(body.includes(`name="replaces_document_id"\r\n\r\n${replacement.replacesDocumentId}\r\n`), "replacement request omitted the expected current document for CAS");
  }
  if (cdp) await cdp.detach();
  const currentMaterial = page.locator(`.material-current-set li[data-material-id="${result.material_id}"]`);
  await currentMaterial.getByText(filename, { exact: true }).waitFor();
  return result as UploadResult & { id: string; material_id: string; version: number };
}
