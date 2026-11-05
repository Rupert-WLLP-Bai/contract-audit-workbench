import type { Page, Route } from "playwright/test";
import { createElement } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { CollapsibleSection, SectionAnchorNav } from "../../src/components/collapsible-section";
import { SectionNavigationProvider } from "../../src/components/section-navigation";

const at = "2026-11-05T01:00:00Z";
export const caseId = "case-37";
export const runId = "run-1";
export const attemptId = "attempt-1";
export const evidenceId = "fact_0123456789abcdef01234567";
const text = `😀甲方应支付100万元。\n${"这是一行合成阅读内容。\n".repeat(120)}`;
const document = {
  id: "doc-1", role: "draft_contract", filename: "合成合同.txt", media_type: "text/plain", digest: "a".repeat(64),
  size: text.length, parse_status: "ready", parse_error: null, created_at: at, parsed_document: null,
  parsed_document_id: "parsed-1", text, clauses: [], superseded: false,
};
const run = { id: runId, case_id: caseId, state: "awaiting_human", updated_at: at, started_at: at };
const attempt = {
  id: attemptId, run_id: runId, ordinal: 1, retry_of_attempt_id: null, parsed_document_ids: ["parsed-1"],
  state: "awaiting_human", accepted_at: at, started_at: at, finished_at: at, input_hash: null, output_hash: null,
  error_code: null, history_kind: "frozen",
};
const evidence = {
  evidence_id: evidenceId, semantic_type: "amount", value: "1000000", unit: "元", currency: null,
  verification: "typed_semantic_and_frozen_quote_verified",
  anchor: { id: `anchor-${evidenceId}`, document_id: "doc-1", parsed_document_id: "parsed-1", quote: "100万元",
    status: "located", start: 6, end: 11, reason: null, page: null, element_id: null },
};
const review = {
  run, documents: [document],
  findings: [{ id: "finding-1", origin: "rule", domain: "金额", claim: "核对合成金额", relation: "conflict",
    version: 1, decisions: [], evidence: [{ id: "finding-anchor", document_version_id: "doc-1", parsed_document_id: "parsed-1",
      quote: "100万元", status: "located", text_offset: { start: 6, end: 11 }, page: null, bbox: null }] }],
  review_attempts: { attempt, attempts: [attempt] }, execution: null, snapshot: { document_ids: ["doc-1"] },
  agent_review: {
    attempt_id: attemptId, status: "completed", rulepack: null,
    scope: [{ domain: "commercial", capability_ids: ["amount"], source_ids: ["doc-1"] }],
    domains: [{ domain: "commercial", status: "completed", capabilities: [{ capability_id: "amount", mandatory: true,
      status: "compatible", relation: "compatible", reason_code: "matched_supported_field", evidence_ids: [evidenceId], canonical_evaluation_ids: [] }] }],
    gaps: [], evidence: [evidence], unresolved_gap_ids: [], observations: null,
  },
  evaluations: [], audit_events: [], previous_run_id: null, revisions: [],
};
const detail = {
  id: caseId, title: "导航样本 37", contract_type: "service", business_line: "ENT", scenario: "telecom_dedicated_line",
  business_status: "active", created_at: at, updated_at: at, documents: [document], members: [], latest_run: run,
  finding_counts: { total: 1, conflict: 1, insufficient: 0, undecided: 1 }, activity: [], revision_pending: false,
  configuration_pending: false, subject_context_changed_after_latest_run: false,
  generations: [], remediations: [], reports: [], archives: [], archived_at: null,
};
const dimensions = ["buyer_kind", "seller_kind", "buyer_enterprise_size", "seller_enterprise_size", "funding_kind",
  "procurement_scope", "project_kind", "contract_clause_kind", "transaction_kind"];

export interface NavigationFixture {
  tenant: string;
  identity: string;
  archived: boolean;
  holdLegal: boolean;
  releaseLegal: () => void;
  unmatched: string[];
}

/** Render the actual controlled components with two nested disclosures. */
export function mountNestedNavigation(element: HTMLElement): void {
  const root = createRoot(element);
  const inner = createElement(CollapsibleSection, { id: "nested-inner", title: "嵌套内层", children: createElement("h2", { id: "nested-heading" }, "嵌套目标标题") });
  const outer = createElement(CollapsibleSection, { id: "nested-outer", title: "嵌套外层", children: inner });
  const content = createElement("div", null,
    createElement(SectionAnchorNav, { items: [{ id: "nested-heading", label: "嵌套目标" }, { id: "nested-outer", label: "外层" }] }), outer);
  root.render(createElement(MemoryRouter, { initialEntries: ["/nested"], children:
    createElement(SectionNavigationProvider, { scope: "synthetic-actor:case", ready: true, children: content }) }));
}

/** Every API request receives a local synthetic response or an explicit 404. */
export async function installNavigationFixture(page: Page): Promise<NavigationFixture> {
  let release = () => {};
  const legalGate = new Promise<void>((resolve) => { release = resolve; });
  const state: NavigationFixture = { tenant: "tenant-a", identity: "user-a", archived: false, holdLegal: false, releaseLegal: release, unmatched: [] };
  const appOrigin = new URL(process.env.E2E_WEB ?? "http://127.0.0.1:4177").origin;
  const json = (route: Route, value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
  await page.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    if (url.origin !== appOrigin) return route.abort();
    if (!/^\/api(?:\/|$)/.test(url.pathname)) return route.continue();
    const path = url.pathname;
    const method = route.request().method();
    if (method !== "GET") return json(route, { error: "synthetic read-only fixture" }, 404);
    if (path === "/api/session") return json(route, {
      actor: { id: state.identity, display_name: "合成审查员", role: "reviewer", tenant_id: state.tenant, org_id: "org-a" },
      audience: "client", principal_id: null, active_context: { membership_id: null, tenant_id: state.tenant,
        tenant_name: "合成租户", org_id: "org-a", org_name: "合成组织", context_version: "1" },
      csrf_token: "synthetic-token-not-a-real-credential", context_switch_enabled: false,
    });
    if (path === "/api/cases") return json(route, { items: Array.from({ length: 52 }, (_, index) => ({
      id: `case-${index}`, title: `导航样本 ${String(index).padStart(2, "0")}`, contract_type: "service", document_count: 1,
      pending_decisions: 1, latest_run: run, updated_at: at, business_line: "ENT", scenario: "telecom_dedicated_line", business_status: "active",
    })) });
    if (path === `/api/cases/${caseId}`) return json(route, { ...detail, archived_at: state.archived ? at : null });
    if (path === `/api/cases/${caseId}/reviews/${runId}`) return json(route, review);
    if (path === `/api/cases/${caseId}/reviews/${runId}/events`) return json(route, { error: "fixture stream disabled" }, 403);
    if (path === `/api/cases/${caseId}/runs/${runId}/attempts/${attemptId}/contract-citations/${evidenceId}`) return json(route, {
      case_id: caseId, run_id: runId, attempt_id: attemptId, evidence_id: evidenceId, document_id: "doc-1", parsed_document_id: "parsed-1",
      filename: "合成合同.txt", original_digest: "a".repeat(64), parse_digest: "b".repeat(64), start: 6, end: 11,
      quote: "100万元", text, material_id: null, material_version: null,
    });
    if (path === `/api/cases/${caseId}/review-readiness`) return json(route, { case_id: caseId, revision: "a".repeat(64), business_at: null,
      can_start: false, material_selection_version: 0, materials: [], scopes: [{ id: "full_review", status: "blocked", can_start: false,
        checks: [], blockers: [{ code: "required_materials_missing", status: "blocked", message: "补充材料", missing_dependencies: [], next_action: "查看材料" }] }] });
    if (path === `/api/cases/${caseId}/legal-applicability-facts`) {
      if (state.holdLegal) await legalGate;
      return json(route, { business_at: url.searchParams.get("business_at"), evidence_cutoff_at: url.searchParams.get("evidence_cutoff_at"),
        facts: dimensions.map((dimension) => ({ dimension, status: "unknown", value: "", unknown_reason: "missing_fact", fact: null })) });
    }
    const history = path.match(new RegExp(`^/api/cases/${caseId}/legal-applicability-facts/([^/]+)/history$`));
    if (history) return json(route, { case_id: caseId, dimension: history[1], current_version: 0, items: [], next_cursor: null, can_manage: false });
    if (path === `/api/cases/${caseId}/evidence-requests` || path.endsWith("/manual-findings") || path.endsWith("/experience-usage")) return json(route, { items: [] });
    state.unmatched.push(`${method} ${path}`);
    return json(route, { error: "unmatched synthetic endpoint" }, 404);
  });
  return state;
}
