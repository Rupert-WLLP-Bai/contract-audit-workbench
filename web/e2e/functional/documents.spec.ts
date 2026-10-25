import {
  test, expect, apiURL, webURL, body, login, createCase, upload, fixture,
  detail, waitWorkspace, assertAnchors, evaluation, operations, startReview,
  type APIRequestContext, type Page, type ReviewWorkspace,
} from "./helpers";
import { parseAcceptedReview } from "../../src/api/parse";
import type { AcceptedReview, ReviewDocument } from "../../src/api/types";

const pdfRoot = "internal/documentparse/testdata/pdf/";
const docxRoot = "internal/documentparse/testdata/docx/";
const docxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document";

function source(ws: ReviewWorkspace, documentID: string): ReviewDocument & { text: string } {
  const found = ws.documents.find(item => item.id === documentID);
  if (!found || found.text === null || !found.parsed_document) throw new Error(`Missing complete frozen source ${documentID}`);
  expect(found.parse_status).toBe("ready");
  expect(found.parsed_document.status).toBe("ready");
  expect(found.parsed_document.document_id).toBe(documentID);
  expect(found.parsed_document.original_digest).toBe(found.digest);
  return { ...found, text: found.text };
}

async function uiUpload(page: Page, caseID: string, role: string, filename: string, bytes: Buffer, mimeType: string) {
  await page.goto(`${webURL}/cases/${caseID}`);
  await page.getByLabel("材料角色", { exact: true }).selectOption(role);
  await page.getByLabel("文件", { exact: true }).setInputFiles({ name: filename, mimeType, buffer: bytes });
  const response = page.waitForResponse(item => item.request().method() === "POST" && item.url().endsWith(`/api/cases/${caseID}/documents`));
  await page.getByRole("button", { name: "上传", exact: true }).click();
  const created = await body(await response, 201);
  if (typeof created.id !== "string") throw new Error("Upload did not return document identity");
  await expect(page.locator(`[data-document-id="${created.id}"]`)).toContainText(filename);
  return created.id;
}

async function uiReview(page: Page, api: APIRequestContext, caseID: string, recheck = false) {
  await page.goto(`${webURL}/cases/${caseID}`);
  const response = page.waitForResponse(item => item.request().method() === "POST" && item.url().endsWith(`/api/cases/${caseID}/reviews`));
  await page.getByRole("button", { name: recheck ? "开始复审" : "开始审查", exact: true }).click();
  const accepted = parseAcceptedReview(await body(await response, 202));
  const ws = await waitWorkspace(api, caseID, accepted.id, accepted.attempt_id);
  expect(ws.run.state, JSON.stringify({
    caseID, accepted, run: ws.run, execution: ws.execution,
    documents: ws.documents.map(item => ({
      id: item.id, filename: item.filename, parse_status: item.parse_status,
      parse_error: item.parse_error, parsed_document: item.parsed_document,
    })),
  }, null, 2)).not.toMatch(/^(failed|cancelled)$/);
  await page.goto(`${webURL}/cases/${caseID}/reviews/${accepted.id}?attempt_id=${accepted.attempt_id}`);
  await expect(page.getByRole("heading", { name: "审查工作区", exact: true })).toBeVisible();
  return { accepted, ws };
}

async function original(api: APIRequestContext, caseID: string, documentID: string, expected: Buffer) {
  const response = await api.get(`${apiURL}/api/cases/${caseID}/documents/${documentID}/original`);
  expect(response.status()).toBe(200);
  expect(await response.body()).toEqual(expected);
}

function locatedSource(ws: ReviewWorkspace, documentID: string, page: number | null | "mixed") {
  assertAnchors(ws);
  const anchors = ws.findings.flatMap(item => item.evidence).filter(item => item.document_version_id === documentID && item.status === "located");
  expect(anchors.length, "Review must cite the real complex document, including its parsed identity").toBeGreaterThan(0);
  for (const anchor of anchors) {
    expect(anchor.page).toBe(page === "mixed" ? (anchor.quote.includes("36") ? 2 : 1) : page);
    expect(anchor.bbox).toBeNull();
  }
  if (page === "mixed") expect(anchors.some(item => item.quote.includes("36") && item.page === 2)).toBe(true);
  return anchors;
}

async function showEvidence(page: Page, ws: ReviewWorkspace, documentID: string) {
  const finding = ws.findings.find(item => item.evidence.some(anchor => anchor.document_version_id === documentID && anchor.status === "located"));
  if (!finding) throw new Error(`Review has no located finding for ${documentID}`);
  await page.getByRole("button", { name: `选择线索：${finding.claim}`, exact: true }).click();
  await page.locator(`[role="tab"][data-document-version-id="${documentID}"]`).click();
  const pane = page.locator(`.doc-pane[data-document-version-id="${documentID}"][data-active-source="true"]`);
  const highlights = pane.locator("mark.hit-evidence");
  await expect(highlights.first()).toBeVisible();
  const anchor = finding.evidence.find(item => item.document_version_id === documentID && item.status === "located");
  if (!anchor) throw new Error("Selected evidence disappeared");
  await expect.poll(async () => (await highlights.allTextContents()).join("")).toBe(anchor.quote);
  return pane;
}

async function reparseHistory(page: Page, api: APIRequestContext, caseID: string, documentID: string, before: { accepted: AcceptedReview; ws: ReviewWorkspace }, native: boolean) {
  const oldSource = source(before.ws, documentID);
  const oldParsed = oldSource.parsed_document;
  if (!oldParsed) throw new Error("No initial parse to freeze");
  await page.goto(`${webURL}/cases/${caseID}`);
  const response = page.waitForResponse(item => item.request().method() === "POST" && item.url().endsWith(`/documents/${documentID}/parse`), { timeout: 150_000 });
  await page.getByRole("button", { name: `重新解析 ${oldSource.filename}`, exact: true }).click();
  await body(await response);
  const latest = (await detail(api, caseID)).documents.find(item => item.id === documentID)?.parsed_document;
  if (!latest) throw new Error("Reparse did not persist a new immutable artifact");
  expect(latest.status).toBe("ready");
  expect(latest.id).not.toBe(oldParsed.id);
  expect(latest.original_digest).toBe(oldParsed.original_digest);
  expect(latest.parser_id).toBe(oldParsed.parser_id);
  expect(latest.parser_version).toBe(oldParsed.parser_version);
  expect(latest.config_hash).toBe(oldParsed.config_hash);
  if (native) expect(latest.normalized_digest).toBe(oldParsed.normalized_digest);
  const historical = await waitWorkspace(api, caseID, before.accepted.id, before.accepted.attempt_id);
  expect(source(historical, documentID)).toEqual(oldSource);
  expect(historical.findings).toEqual(before.ws.findings);
  expect(historical.execution?.attempt.parsed_document_ids).toContain(oldParsed.id);
  expect(historical.execution?.attempt.parsed_document_ids).not.toContain(latest.id);
  const next = await uiReview(page, api, caseID, true);
  expect(next.accepted.id).not.toBe(before.accepted.id);
  expect(source(next.ws, documentID).parsed_document?.id).toBe(latest.id);
  expect(next.ws.execution?.attempt.parsed_document_ids).toContain(latest.id);
  expect(next.ws.execution?.attempt.parsed_document_ids).not.toContain(oldParsed.id);
  assertAnchors(next.ws);
  const after = await waitWorkspace(api, caseID, before.accepted.id, before.accepted.attempt_id);
  expect(source(after, documentID)).toEqual(oldSource);
  expect(after.findings).toEqual(before.ws.findings);
  await page.goto(`${webURL}/cases/${caseID}/reviews/${before.accepted.id}?attempt_id=${before.accepted.attempt_id}`);
  await expect(page.locator(`[role="tab"][data-document-version-id="${documentID}"][data-parsed-document-id="${oldParsed.id}"]`)).toBeVisible();
  await showEvidence(page, after, documentID);
}

test.describe("Complex document parsing with real HTTP, model and browser", () => {
  test.setTimeout(420_000);

  for (const scenario of [
    { file: "native-chinese-table.pdf", page: 1, native: true, reparse: true },
    { file: "single-native.pdf", page: 1, native: true, reparse: false },
    { file: "scanned-chinese-table.pdf", page: 1, native: false, reparse: false },
    { file: "mixed-native-scan.pdf", page: "mixed", native: false, reparse: true },
  ] as const) {
    test(`PDF ${scenario.file}: upload, real parse, review and observed anchors`, operations("uploadDocument", "getOriginal", "getCase", "startReview", "getWorkspace", ...(scenario.reparse ? ["reparseDocument"] : [])), async ({ page }, testInfo) => {
      const api = page.context().request;
      await login(api);
      const caseID = await createCase(api, `PDF ${scenario.file}`);
      const bytes = await fixture(pdfRoot + scenario.file);
      const documentID = await uiUpload(page, caseID, "procurement", scenario.file, bytes, "application/pdf");
      await original(api, caseID, documentID, bytes);
      await upload(api, caseID, "draft_contract", "拟签合同.txt", "拟签合同约定质保12个月。😀");
      const run = await uiReview(page, api, caseID);
      const document = source(run.ws, documentID);
      expect(document.parsed_document?.parser_id).toBe("pdf");
      expect(document.parsed_document?.parser_version).toBe("pdf-poppler-v1");
      expect(evaluation(run.ws, "warranty").relation).toBe("conflict");
      locatedSource(run.ws, documentID, scenario.page);
      if (scenario.file === "native-chinese-table.pdf") {
        expect(Array.from(document.text).slice(16, 42).join("")).toBe("第一条 带宽为100 Mbps，服务对象为合成园区。");
        expect(document.text).toContain("| 带宽 | 100 Mbps |");
        expect(document.text).toContain("| 质保 | 36个月 |");
        expect(document.text).toContain("旋转文字：故障响应4小时。");
        expect(document.text.split("重复证据：服务可用率99.95%。")).toHaveLength(3);
        expect(document.clauses.map(item => item.page)).toEqual([1, 2]);
      } else if (scenario.file === "single-native.pdf") {
        expect(document.text).toBe("合成单页合同：质保36个月。");
      } else {
        expect(document.text).toMatch(/扫描条款[：:]?\s*带宽\s*80\s*Mbps/i);
        expect(document.text).toMatch(/Bandwidth\s*80\s*Mbps/i);
        expect(document.text).toMatch(/Warranty\s*24\s*months/i);
        expect(document.text).toMatch(/质保[^\n0-9]{0,20}24\s*个月/);
        if (scenario.file === "mixed-native-scan.pdf") {
          expect(document.text).toContain("合成混合页：原生承诺100 Mbps。");
          expect(document.text).toContain("原生备注：图片附件也属于本页证据。");
          expect(document.text).toContain("第二页原生条款：质保36个月。");
        }
      }
      const pane = await showEvidence(page, run.ws, documentID);
      const highlightedQuote = (await pane.locator("mark.hit-evidence").allTextContents()).join("");
      const observedPage = scenario.page === "mixed" ? (highlightedQuote.includes("36") ? 2 : 1) : scenario.page;
      await expect(pane).toContainText(`第 ${observedPage} 页`);
      if (scenario.reparse) {
        await testInfo.attach("real-pdf-located-evidence", { body: await page.screenshot(), contentType: "image/png" });
        await reparseHistory(page, api, caseID, documentID, run, scenario.native);
      }
      await original(api, caseID, documentID, bytes);
    });
  }

  test("DOCX merged/nested tables preserve exact text, visible revisions, emoji and page-free evidence", operations("uploadDocument", "getOriginal", "startReview", "getWorkspace", "reparseDocument"), async ({ page }, testInfo) => {
    const api = page.context().request;
    await login(api);
    const caseID = await createCase(api, "DOCX复杂表格");
    const bytes = await fixture(docxRoot + "complex-tables.docx");
    const documentID = await uiUpload(page, caseID, "procurement", "complex-tables.docx", bytes, docxMIME);
    await upload(api, caseID, "draft_contract", "拟签合同.txt", "拟签合同约定质保24个月。");
    const run = await uiReview(page, api, caseID);
    const document = source(run.ws, documentID);
    expect(document.parsed_document?.parser_id).toBe("docx");
    expect(document.parsed_document?.parser_version).toBe("ooxml-docx-v1");
    expect(document.text).toBe("第1条 合成专线合同😀\n\n甲方：合成采购方\n\n服务参数\t承诺值\n专线A\t带宽\t100 Mbps\n月度可用率\t99.9%\n嵌套附件\t附件开始\n质保期\t36个月😀\n附件结束\n\n当前：新金额100元；显式可见\n\n公司网站说明\n\n校验日期：2026-10-24\n\n合成页眉：内部演示\n\n合成页脚：保留原件");
    for (const hidden of ["旧金额999元", "隐藏金额888元", "样式隐藏777元", "DATE"]) expect(document.text).not.toContain(hidden);
    expect(document.clauses.every(item => item.page === null)).toBe(true);
    expect(evaluation(run.ws, "warranty").relation).toBe("conflict");
    locatedSource(run.ws, documentID, null);
    const pane = await showEvidence(page, run.ws, documentID);
    await expect(pane).toContainText("36个月😀");
    await expect(pane.locator(".doc-pane-head")).not.toContainText(/第 \d+ 页/);
    await testInfo.attach("real-docx-nested-table-evidence", { body: await page.screenshot(), contentType: "image/png" });
    await reparseHistory(page, api, caseID, documentID, run, true);
    await original(api, caseID, documentID, bytes);
  });

  for (const file of ["embedded-image.docx", "embedded-jpeg.docx"]) {
    test(`DOCX ${file}: actual embedded image OCR stays inside document order and frozen evidence`, operations("uploadDocument", "getOriginal", "startReview", "getWorkspace", ...(file === "embedded-image.docx" ? ["reparseDocument"] : [])), async ({ page }) => {
      const api = page.context().request;
      await login(api);
      const caseID = await createCase(api, `DOCX图片 ${file}`);
      const bytes = await fixture(docxRoot + file);
      const documentID = await uiUpload(page, caseID, "procurement", file, bytes, docxMIME);
      await upload(api, caseID, "draft_contract", "拟签合同.txt", "拟签合同约定质保24个月。");
      const run = await uiReview(page, api, caseID);
      const document = source(run.ws, documentID);
      expect(document.parsed_document?.parser_version).toBe("ooxml-docx-v1");
      expect(document.text).toMatch(/DOCX\s+IMAGE\s+BANDWIDTH\s+100\s*Mbps/i);
      expect(document.text).toMatch(/WARRANTY\s+36\s+MONTHS/i);
      expect(document.text).toMatch(/质保期\s*36\s*个月/);
      expect(document.text.indexOf("图片起始")).toBeLessThan(document.text.search(/DOCX\s+IMAGE/i));
      expect(document.text.indexOf("图片结束")).toBeGreaterThan(document.text.search(/WARRANTY\s+36/i));
      expect(evaluation(run.ws, "warranty").relation).toBe("conflict");
      locatedSource(run.ws, documentID, null);
      await showEvidence(page, run.ws, documentID);
      if (file === "embedded-image.docx") await reparseHistory(page, api, caseID, documentID, run, false);
      await original(api, caseID, documentID, bytes);
    });
  }

  for (const image of [{ file: "standalone-contract.png", kind: "PNG", mime: "image/png" }, { file: "standalone-contract.jpeg", kind: "JPEG", mime: "image/jpeg" }]) {
    test(`Standalone ${image.kind}: UI upload, real OCR, review and page-free located evidence`, operations("uploadDocument", "getOriginal", "startReview", "getWorkspace"), async ({ page }) => {
      const api = page.context().request;
      await login(api);
      const caseID = await createCase(api, `独立${image.kind}合成图片`);
      const bytes = await fixture(`web/e2e/fixtures/${image.file}`);
      const documentID = await uiUpload(page, caseID, "procurement", image.file, bytes, image.mime);
      await original(api, caseID, documentID, bytes);
      await upload(api, caseID, "draft_contract", "拟签合同.txt", "拟签合同约定质保24个月。");
      const run = await uiReview(page, api, caseID);
      const document = source(run.ws, documentID);
      expect(document.media_type).toBe(image.mime);
      expect(document.parsed_document?.parser_id).toBe("ocr");
      expect(document.parsed_document?.parser_version).toBe(process.env.E2E_MODEL);
      expect(document.text).toMatch(/DOCX\s+IMAGE\s+BANDWIDTH\s+100\s*Mbps/i);
      expect(document.text).toMatch(/WARRANTY\s+36\s+MONTHS/i);
      expect(document.text).toMatch(/质保期\s*36\s*个月/);
      expect(evaluation(run.ws, "warranty").relation).toBe("conflict");
      expect(document.clauses.every(item => item.page === null)).toBe(true);
      locatedSource(run.ws, documentID, null);
      const pane = await showEvidence(page, run.ws, documentID);
      await expect(pane.locator(".doc-pane-head")).not.toContainText(/第 \d+ 页/);
      await original(api, caseID, documentID, bytes);
    });
  }

  for (const invalid of [
    { file: "corrupt.pdf", root: pdfRoot, mime: "application/pdf", reason: /pdfinfo/, parser: "pdf" },
    { file: "encrypted.pdf", root: pdfRoot, mime: "application/pdf", reason: /pdfinfo|encrypted/, parser: "pdf" },
    { file: "interactive-form.pdf", root: pdfRoot, mime: "application/pdf", reason: /flattened/, parser: "pdf" },
    { file: "over-page-limit.pdf", root: pdfRoot, mime: "application/pdf", reason: /page count/, parser: "pdf" },
    { file: "over-image-pixels.pdf", root: pdfRoot, mime: "application/pdf", reason: /pixel limit/, parser: "pdf" },
    { file: "corrupt.docx", root: docxRoot, mime: docxMIME, reason: /missing part/, parser: "docx" },
    { file: "external-image.docx", root: docxRoot, mime: docxMIME, reason: /external resources/, parser: "docx" },
    { file: "unsupported-altchunk.docx", root: docxRoot, mime: docxMIME, reason: /opaque relationship/, parser: "docx" },
    { file: "unsupported-footnote.docx", root: docxRoot, mime: docxMIME, reason: /footnote\/endnote/, parser: "docx" },
  ]) {
    test(`Unsupported/corrupt ${invalid.file} retains original and a truthful failed parse`, operations("uploadDocument", "getOriginal", "reparseDocument", "getCase"), async ({ page }) => {
      const api = page.context().request;
      await login(api);
      const caseID = await createCase(api, `显式解析失败 ${invalid.file}`);
      const bytes = await fixture(invalid.root + invalid.file);
      const documentID = await uiUpload(page, caseID, "attachment", invalid.file, bytes, invalid.mime);
      await body(await api.post(`${apiURL}/api/cases/${caseID}/documents/${documentID}/parse`, { data: {} }));
      const document = (await detail(api, caseID)).documents.find(item => item.id === documentID);
      if (!document?.parsed_document) throw new Error("Failure did not retain an immutable failed parse");
      expect(document.parse_status).toBe("failed");
      expect(document.parse_error).toMatch(invalid.reason);
      expect(document.parsed_document.status).toBe("failed");
      expect(document.parsed_document.parser_id).toBe(invalid.parser);
      expect(document.parsed_document.normalized_digest).toBeNull();
      expect(document.parsed_document.ir_digest).toBeNull();
      await page.reload();
      await expect(page.locator(`[data-document-id="${documentID}"]`)).toContainText("解析失败");
      await original(api, caseID, documentID, bytes);
    });
  }

  test("A corrupt PDF is retained as missing evidence and never becomes a compatible review", operations("uploadDocument", "reparseDocument", "startReview", "getWorkspace"), async ({ request }) => {
    await login(request);
    const caseID = await createCase(request, "失败材料不得当作无风险");
    await upload(request, caseID, "procurement", "采购.txt", "采购要求质保36个月。");
    await upload(request, caseID, "draft_contract", "拟签.txt", "拟签合同约定质保36个月。");
    const failedID = await upload(request, caseID, "attachment", "corrupt.pdf", await fixture(pdfRoot + "corrupt.pdf"), "application/pdf");
    await body(await request.post(`${apiURL}/api/cases/${caseID}/documents/${failedID}/parse`, { data: {} }));
    const accepted = await startReview(request, caseID);
    const ws = await waitWorkspace(request, caseID, accepted.id, accepted.attempt_id);
    expect(evaluation(ws, "warranty").relation).toBe("insufficient");
    const failed = ws.documents.find(item => item.id === failedID);
    expect(failed?.parse_status).toBe("failed");
    expect(failed?.text).toBeNull();
    expect(failed?.parsed_document?.status).toBe("failed");
    const missing = ws.findings.flatMap(item => item.evidence).filter(item => item.document_version_id === failedID);
    expect(missing.length).toBeGreaterThan(0);
    expect(missing.every(item => item.status !== "located" && item.page === null && item.text_offset === null && item.bbox === null)).toBe(true);
  });
});
