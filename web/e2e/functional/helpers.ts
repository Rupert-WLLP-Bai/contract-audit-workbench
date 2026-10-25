import { test, expect, type APIRequestContext, type APIResponse, type Page } from "playwright/test";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { parseAcceptedReview, parseActor, parseCaseDetail, parseId, parseReviewWorkspace } from "../../src/api/parse";
import type { ReviewWorkspace } from "../../src/api/types";
export { test, expect };
export type { APIRequestContext, Page, ReviewWorkspace };
export const apiURL = process.env.E2E_API!;
export const webURL = process.env.E2E_WEB!;
export const adminURL = process.env.E2E_ADMIN!;
export const portalURL = process.env.E2E_PORTAL!;
export const root = resolve(import.meta.dirname, "../../..");
export type Row = Record<string, unknown>;
export function record(value: unknown): Row {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("Expected response object");
  return value as Row;
}
export function rows(value: unknown): Row[] {
  if (!Array.isArray(value)) throw new Error("Expected response array");
  return value.map(record);
}
export function operations(...ids: string[]) { return { annotation: ids.map(description => ({ type: "operation", description })) }; }
export async function body(response: Pick<APIResponse, "text" | "status" | "url">, expected = 200): Promise<Row> {
  const text = await response.text();
  expect(response.status(), `${response.url()}: ${text}`).toBe(expected);
  return text ? record(JSON.parse(text)) : {};
}
export async function login(api: APIRequestContext, username = "demo-admin", password = "admin-demo", audience = "client", base = apiURL) {
  const raw = await body(await api.post(`${base}/api/session`, { data: { username, password, audience } }));
  const actor = parseActor(raw);
  expect(raw.audience).toBe(audience);
  expect(actor.id).toBe(username);
  return { actor, audience: raw.audience };
}
export async function uiLogin(page: Page, audience = "client") {
  await page.goto(audience === "admin" ? adminURL : `${webURL}/login`);
  await page.getByLabel("账号", { exact: true }).fill("demo-admin");
  await page.getByLabel("密码", { exact: true }).fill("admin-demo");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}
export async function createCase(api: APIRequestContext, title = "功能验收", telecom = false) {
  const created = await body(await api.post(`${apiURL}/api/cases`, { data: {
    title: `${title} ${Date.now()}`, contract_type: telecom ? "service" : "procurement_ict",
    ...(telecom ? { business_line: "ENT", scenario: "telecom_dedicated_line" } : {}),
  } }), 201);
  const id = parseId(created);
  expect(id).toMatch(/^case_/);
  return id;
}
export async function upload(api: APIRequestContext, caseID: string, role: string, filename: string, content: string | Buffer, mimeType = "text/plain") {
  const created = await body(await api.post(`${apiURL}/api/cases/${caseID}/documents`, {
    multipart: { role, file: { name: filename, mimeType, buffer: typeof content === "string" ? Buffer.from(content) : content } },
  }), 201);
  const id = parseId(created);
  expect(id).toMatch(/^doc_/);
  return id;
}
export async function fixture(path: string) { return readFile(resolve(root, path)); }
export async function detail(api: APIRequestContext, caseID: string) { return parseCaseDetail(await body(await api.get(`${apiURL}/api/cases/${caseID}`))); }
export async function startReview(api: APIRequestContext, caseID: string) {
  const accepted = parseAcceptedReview(await body(await api.post(`${apiURL}/api/cases/${caseID}/reviews`, { data: {} }), 202));
  expect(accepted.attempt_id).toMatch(/^att_/);
  return accepted;
}
export async function waitWorkspace(api: APIRequestContext, caseID: string, runID: string, attemptID?: string | null): Promise<ReviewWorkspace> {
  const load = async () => parseReviewWorkspace(await body(await api.get(`${apiURL}/api/cases/${caseID}/reviews/${runID}${attemptID ? `?attempt_id=${attemptID}` : ""}`)));
  let ws = await load();
  await expect.poll(async () => { ws = await load(); return ws.run.state; }, { timeout: 150_000, intervals: [250, 500, 1000] })
    .toMatch(/^(awaiting_human|completed|partial|failed|cancelled)$/);
  return ws;
}
export function assertAnchors(ws: ReviewWorkspace) {
  let located = 0;
  for (const finding of ws.findings) for (const anchor of finding.evidence) {
    if (anchor.status !== "located") continue;
    const doc = ws.documents.find(item => item.id === anchor.document_version_id && item.parsed_document?.id === anchor.parsed_document_id);
    if (!doc?.parsed_document || !anchor.text_offset || doc.text === null) throw new Error(`Missing frozen anchor source ${anchor.id}`);
    expect(anchor.normalized_digest).toBe(doc.parsed_document.normalized_digest);
    expect(Array.from(doc.text).slice(anchor.text_offset.start, anchor.text_offset.end).join("")).toBe(anchor.quote);
    expect(anchor.bbox).toBeNull();
    located++;
  }
  expect(located).toBeGreaterThan(0);
}
export function evaluation(ws: ReviewWorkspace, domainOrRule: string) {
  const found = ws.evaluations?.find(item => item.domain === domainOrRule || item.rule_id === domainOrRule);
  if (!found) throw new Error(`Missing evaluation ${domainOrRule}`);
  return found;
}
export async function warrantyCase(api: APIRequestContext) {
  const caseID = await createCase(api, "质保冲突😀");
  const purchase = await upload(api, caseID, "procurement", "采购.txt", "采购文件要求质保36个月。😀合同总价100万元。预付款30%，验收通过后支付70%。");
  const draft = await upload(api, caseID, "draft_contract", "拟签.md", "拟签合同约定质保24个月。合同总价100万元。预付款40%，验收通过后支付60%。", "text/markdown");
  const accepted = await startReview(api, caseID);
  return { caseID, purchase, draft, accepted, ws: await waitWorkspace(api, caseID, accepted.id, accepted.attempt_id) };
}
