import type { FullConfig, FullResult, Reporter, Suite, TestCase, TestResult } from "playwright/types/testReporter";
import { readFileSync, mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

function record(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid operation inventory object");
  return value as Record<string, unknown>;
}

export default class FunctionalReporter implements Reporter {
  private suite!: Suite;
  private outcomes = new Map<string, string>();
  onBegin(_config: FullConfig, suite: Suite) { this.suite = suite; }
  onTestEnd(test: TestCase, result: TestResult) {
    this.outcomes.set(test.id, result.status === "passed" ? "PASS" : result.status === "skipped" ? "NOT_RUN" : "FAIL");
  }
  async onEnd(result: FullResult) {
    const root = resolve(import.meta.dirname, "../../..");
    const spec = record(JSON.parse(readFileSync(resolve(root, "internal/apidocs/openapi.json"), "utf8")));
    const tests = this.suite.allTests().map(test => ({
      feature: test.titlePath().slice(1).join(" > "),
      location: `${test.location.file.replace(root+"/", "")}:${test.location.line}`,
      operations: test.annotations.filter(a => a.type === "operation").map(a => a.description!),
      requirements: /pdf/i.test(test.title) ? ["DOC-02", "DOC-03", "NX-PARSE-001", "NX-PARSE-002", "AC-17", "AC-18"] : /docx/i.test(test.title) ? ["DOC-01", "DOC-03", "NX-PARSE-001", "NX-PARSE-002", "AC-17", "AC-18"] : /^Standalone (PNG|JPEG)/.test(test.title) ? ["DOC-03", "CA-002", "CA-004"] : [],
      outcome: this.outcomes.get(test.id) || "NOT_RUN",
    }));
    const operations = Object.entries(record(spec.paths)).flatMap(([path, value]) => Object.entries(record(value))
      .filter(([method]) => ["get", "post", "put", "patch", "delete"].includes(method))
      .map(([method, value]) => {
        const operationID = record(value).operationId;
        if (typeof operationID !== "string") throw new Error(`Missing operationId ${method} ${path}`);
        const mapped = tests.filter(test => test.operations.includes(operationID));
        return { method: method.toUpperCase(), path, operation_id: operationID,
          tests: mapped.map(test => test.feature),
          outcome: mapped.length === 0 || mapped.every(test => test.outcome === "NOT_RUN") ? "NOT_RUN" : mapped.some(test => test.outcome === "FAIL") ? "FAIL" : "PASS" };
      }));
    const target = resolve(root, "artifacts/documents-functional");
    mkdirSync(target, { recursive: true });
    const report = JSON.stringify({
      generated_at: new Date().toISOString(), suite_status: result.status,
      run_id: process.env.E2E_RUN_ID, selected_filter: process.env.E2E_FUNCTIONAL_GREP || null,
      model: process.env.E2E_MODEL, provider: process.env.E2E_PROVIDER,
      data_class: "synthetic", runtime: "real HTTP/PostgreSQL/durable worker/Model Gateway/live model/Chromium",
      features: tests, operations,
      counts: Object.fromEntries(["PASS", "FAIL", "NOT_RUN"].map(status => [status, tests.filter(test => test.outcome === status).length])),
      limitations: ["Positive real OIDC browser results are recorded separately in oidc-browser/results.json", "Business UAT and future planned use cases are outside functional implementation coverage"],
    }, null, 2)+"\n";
    writeFileSync(resolve(target, "feature-outcomes.json"), report);
    const runTarget = resolve(target, process.env.E2E_RUN_ID!);
    mkdirSync(runTarget, { recursive: true });
    writeFileSync(resolve(runTarget, "feature-outcomes.json"), report);
    if (!process.env.E2E_FUNCTIONAL_GREP && operations.some(operation => operation.outcome === "NOT_RUN")) return { status: "failed" as const };
  }
}
