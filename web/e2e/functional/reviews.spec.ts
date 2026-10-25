import { test, expect, operations, apiURL, webURL, body, login, uiLogin, createCase, upload, startReview, waitWorkspace, warrantyCase, assertAnchors, evaluation, record, rows } from "./helpers";
import { parseReviewWorkspace } from "../../src/api/parse";

test("real model review retains deterministic relations, exact anchors, manual decision CAS and SSE resume", operations("startReview", "getWorkspace", "decideFinding", "streamReviewEvents"), async ({ request, page }) => {
  await login(request);
  const { caseID, accepted, ws } = await warrantyCase(request);
  expect(evaluation(ws, "warranty").relation).toBe("conflict");
  expect(evaluation(ws, "payment_condition").relation).toBe("conflict");
  expect(ws.run.model_ref).toBe("codex/gpt-6-luna");
  expect(ws.run.partial_reason).toBeNull();
  expect(ws.execution?.usage?.calls).toBeGreaterThan(0);
  expect(ws.execution?.usage?.known_subtotals.total_tokens).toBeGreaterThan(0);
  assertAnchors(ws);
  const finding = ws.findings.find(item => item.domain === "warranty" && item.origin === "rule")!;
  expect(finding.relation).toBe("conflict");
  await uiLogin(page);
  await page.goto(`${webURL}/cases/${caseID}/reviews/${accepted.id}?attempt_id=${accepted.attempt_id}`);
  await expect(page.getByRole("heading", { name: "审查工作区", exact: true })).toBeVisible();
  await page.getByRole("button", { name: `选择线索：${finding.claim}`, exact: true }).click();
  await expect(page.locator("mark.hit-evidence").first()).toContainText(/质保/);
  await page.getByRole("button", { name: "双来源对照", exact: true }).click();
  await expect(page.locator(".document-split mark.hit-evidence")).toHaveCount(2);
  await page.getByRole("button", { name: "单篇阅读", exact: true }).click();
  await expect(page.getByRole("button", { name: "单篇阅读", exact: true })).toHaveAttribute("aria-pressed", "true");
  const streamURL = `${webURL}/api/cases/${caseID}/reviews/${accepted.id}/events`;
  const initial = await page.evaluate(async url => {
    const controller = new AbortController();
    const response = await fetch(url, { signal: controller.signal });
    const reader = response.body!.getReader();
    const chunk = await reader.read(); controller.abort();
    return new TextDecoder().decode(chunk.value);
  }, streamURL);
  const ids = [...initial.matchAll(/^id: (\d+)$/gm)].map(match => Number(match[1]));
  expect(ids.length).toBeGreaterThan(0);
  const cursor = Math.max(...ids);
  await page.locator('input[type="radio"][value="confirm_risk"]').check();
  await page.getByLabel("理由", { exact: true }).fill("合成采购要求36个月，拟签仅24个月，确认风险。");
  const saved = page.waitForResponse(response => response.request().method() === "POST" && response.url().includes(`/findings/${finding.id}/decisions`));
  await page.getByRole("button", { name: "保存决定", exact: true }).click();
  expect((await saved).status()).toBe(204);
  await expect(page.getByText("合成采购要求36个月，拟签仅24个月，确认风险。", { exact: true })).toBeVisible();
  const stale = await request.post(`${apiURL}/api/cases/${caseID}/reviews/${accepted.id}/findings/${finding.id}/decisions`, {
    data: { action: "false_positive", reason: "陈旧版本必须拒绝，不覆盖人工决定。", expected_version: finding.version }, headers: { "Idempotency-Key": "stale-functional-decision" },
  });
  expect(stale.status()).toBe(409);
  const resumed = await page.evaluate(async ({ url, cursor }) => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 15_000);
    try {
      const response = await fetch(url, { headers: { "Last-Event-ID": String(cursor) }, signal: controller.signal });
      const reader = response.body!.getReader(); let text = "";
      while (!text.includes("decision")) { const chunk = await reader.read(); if (chunk.done) break; text += new TextDecoder().decode(chunk.value); }
      return text;
    } finally { clearTimeout(timer); controller.abort(); }
  }, { url: streamURL, cursor });
  expect(resumed).toContain("decision");
  expect([...resumed.matchAll(/^id: (\d+)$/gm)].every(match => Number(match[1]) > cursor)).toBe(true);
  const after = await waitWorkspace(request, caseID, accepted.id, accepted.attempt_id);
  expect(after.findings.find(item => item.id === finding.id)?.decisions).toHaveLength(1);
  expect(after.audit_events.some(event => event.actor === "demo-admin" && event.event_type.includes("decision"))).toBe(true);
});

test("durable cancellation and retry append attempts while freezing original inputs and errors", operations("cancelReview", "retryReview", "getWorkspace", "startReview"), async ({ request }) => {
  await login(request);
  const caseID = await createCase(request, "取消与重试");
  await upload(request, caseID, "procurement", "purchase.txt", "采购文件要求质保36个月。");
  await upload(request, caseID, "draft_contract", "draft.txt", "拟签合同约定质保24个月。");
  const accepted = await startReview(request, caseID);
  let frozen = await body(await request.get(`${apiURL}/api/cases/${caseID}/reviews/${accepted.id}`));
  await expect.poll(async () => {
    frozen = await body(await request.get(`${apiURL}/api/cases/${caseID}/reviews/${accepted.id}`));
    const execution = record(frozen.execution); const attempt = record(execution.attempt);
    return attempt.history_kind === "frozen" && attempt.state === "running";
  }, { timeout: 30_000, intervals: [50, 100, 200] }).toBe(true);
  const before = parseReviewWorkspace(frozen);
  await body(await request.post(`${apiURL}/api/cases/${caseID}/reviews/${accepted.id}/cancel`, { data: {} }));
  const cancelled = await waitWorkspace(request, caseID, accepted.id, accepted.attempt_id);
  expect(cancelled.run.state).toBe("cancelled");
  const retry = await body(await request.post(`${apiURL}/api/cases/${caseID}/reviews/${accepted.id}/retry`, { data: {} }), 202);
  expect(retry.id).toBe(accepted.id);
  expect(retry.attempt_id).not.toBe(accepted.attempt_id);
  const after = await waitWorkspace(request, caseID, accepted.id, String(retry.attempt_id));
  expect(after.execution?.attempt.input_hash).toBe(before.execution?.attempt.input_hash);
  expect(after.execution?.attempt.parsed_document_ids).toEqual(before.execution?.attempt.parsed_document_ids);
  const old = await waitWorkspace(request, caseID, accepted.id, accepted.attempt_id);
  expect(old.execution?.attempt.state).toBe("cancelled");
  expect(old.execution?.attempt.id).toBe(accepted.attempt_id);
  expect(after.execution?.attempts.map(attempt => attempt.id)).toEqual(expect.arrayContaining([accepted.attempt_id, retry.attempt_id]));
});

test("missing live planner model rejects acceptance while preserving ready materials", operations("startReview", "getCase", "uploadDocument"), async ({ playwright }) => {
  const base = process.env.E2E_MISSING_API!;
  const api = await playwright.request.newContext();
  try {
    await login(api, "demo-admin", "admin-demo", "client", base);
    const created = await body(await api.post(`${base}/api/cases`, { data: { title: "未配置模型保留确定性冲突", contract_type: "procurement_ict" } }), 201);
    const c = String(created.id);
    for (const [role, text] of [["procurement", "采购文件要求质保36个月。"], ["draft_contract", "拟签合同约定质保24个月。"]]) await body(await api.post(`${base}/api/cases/${c}/documents`, { multipart: { role, file: { name: `${role}.txt`, mimeType: "text/plain", buffer: Buffer.from(text) } } }), 201);
    const denied = await body(await api.post(`${base}/api/cases/${c}/reviews`, { data: {} }), 409);
    expect(record(denied.error).code).toBe("conflict");
    expect(record(denied.error).message).toContain("model planner requires the configured model identity");
    const unchanged = await body(await api.get(`${base}/api/cases/${c}`));
    expect(unchanged.latest_run).toBeNull();
    expect(rows(unchanged.documents).map(item => item.parse_status)).toEqual(["ready", "ready"]);
  } finally { await api.dispose(); }
});
