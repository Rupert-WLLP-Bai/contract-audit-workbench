import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { parseReviewDiagnostics } from "../src/api/parse";
import type { Page } from "playwright";

export type FrozenRulepack = {
  id: string;
  version: string;
  content_sha256: string;
  snapshot_sha256: string;
};

export async function readFrozenGovernance(
  page: Page,
  web: string,
  caseID: string,
  runID: string,
  attemptID: string,
): Promise<{ raw: Record<string, unknown>; diagnostics: ReturnType<typeof parseReviewDiagnostics>; rulepack: FrozenRulepack }> {
  const response = await page.request.get(
    `${web}/api/cases/${caseID}/reviews/${runID}/diagnostics?attempt_id=${encodeURIComponent(attemptID)}`,
  );
  assert.equal(response.status(), 200, `review diagnostics returned HTTP ${response.status()}`);
  const value: unknown = await response.json();
  assert.ok(value !== null && typeof value === "object" && !Array.isArray(value), "review diagnostics must be an object");
  const raw = value as Record<string, unknown>;
  const evidenceDirectory = process.env.CAW_DELIVERY_DIR;
  if (evidenceDirectory) {
    const target = join(evidenceDirectory, "browser-evidence");
    await mkdir(target, { recursive: true });
    await writeFile(join(target, `diagnostics-${runID}-${attemptID}.json`), `${JSON.stringify(raw, null, 2)}\n`, "utf8");
  }
  const diagnostics = parseReviewDiagnostics(raw);
  assert.equal(raw.case_id, caseID, "diagnostics belong to a different case");
  assert.equal(raw.run_id, runID, "diagnostics belong to a different review run");
  assert.ok(diagnostics.execution, "review diagnostics omitted frozen execution details");
  assert.equal(diagnostics.execution.attempt?.id, attemptID, "diagnostics belong to a different frozen attempt");
  assert.match(diagnostics.execution.attempt?.input_hash ?? "", /^[0-9a-f]{64}$/, "review diagnostics omitted a valid frozen attempt input hash");

  const snapshot = raw.snapshot;
  assert.ok(snapshot !== null && typeof snapshot === "object" && !Array.isArray(snapshot), "review diagnostics omitted its frozen snapshot");
  const governed = (snapshot as Record<string, unknown>).governed_knowledge;
  assert.ok(governed !== null && typeof governed === "object" && !Array.isArray(governed), "frozen snapshot omitted governed knowledge");
  const pack = (governed as Record<string, unknown>).rule_pack;
  assert.ok(pack !== null && typeof pack === "object" && !Array.isArray(pack), "frozen snapshot omitted the governed rule pack");
  const identity = pack as Record<string, unknown>;
  assert.equal(identity.pack_id, "telecom-governed", "frozen snapshot selected a different telecom rule pack");
  assert.equal(identity.version, "1", "frozen snapshot selected a different telecom rule pack version");
  assert.match(String(identity.content_sha256 ?? ""), /^[0-9a-f]{64}$/, "frozen rule pack omitted a valid content hash");
  assert.match(String(identity.snapshot_sha256 ?? ""), /^[0-9a-f]{64}$/, "frozen rule pack omitted a valid snapshot hash");

  const requiredPolicies = (governed as Record<string, unknown>).required_policies;
  assert.ok(Array.isArray(requiredPolicies) && requiredPolicies.length > 0, "governed snapshot omitted required policies");
  for (const [index, rawPolicy] of requiredPolicies.entries()) {
    assert.ok(rawPolicy !== null && typeof rawPolicy === "object" && !Array.isArray(rawPolicy), `required policy ${index} is malformed`);
    const binding = (rawPolicy as Record<string, unknown>).binding;
    assert.ok(binding !== null && typeof binding === "object" && !Array.isArray(binding), `required policy ${index} omitted its frozen rule pack binding`);
    const frozenBinding = binding as Record<string, unknown>;
    assert.equal(frozenBinding.pack_id, identity.pack_id, `required policy ${index} refers to a different rule pack`);
    assert.equal(frozenBinding.pack_version, identity.version, `required policy ${index} refers to a different rule pack version`);
    assert.equal(frozenBinding.pack_content_hash, identity.content_sha256, `required policy ${index} refers to a different rule pack content hash`);
    assert.equal(frozenBinding.pack_snapshot_digest, identity.snapshot_sha256, `required policy ${index} refers to a different frozen rule pack snapshot`);
  }

  return { raw, diagnostics, rulepack: { id: String(identity.pack_id), version: String(identity.version), content_sha256: String(identity.content_sha256), snapshot_sha256: String(identity.snapshot_sha256) } };
}

export function assertSameFrozenRulepack(actual: FrozenRulepack, expected: FrozenRulepack, stage: string): void {
  assert.deepEqual(actual, expected, `${stage} did not preserve the exact frozen rule pack and snapshot`);
}
