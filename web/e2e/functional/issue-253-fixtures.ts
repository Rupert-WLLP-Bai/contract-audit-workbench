// File metadata (manifest mirror):
// Purpose: Provides small synthetic session, ledger and draft data for contract layout checks while refusing unintended API mutations and external service access.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-05
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { Page, Route } from "playwright/test";

const contractId = "0f7d5c1e-4a2b-4c3d-8e9f-0123456789ab";
const caseId = "80000000-0000-4000-8000-000000000001";
const at = "2026-11-05T01:00:00Z";
export const longName = `合成合同${"LongContractName".repeat(10)}`;
export const longNumber = `SYNTHETIC-${"1234567890".repeat(8)}`;
export const longBank = `合成银行${"LongBankBranch".repeat(8)}`;
export const longSupplier = `合成供应方${"LongSupplierName".repeat(8)}`;
export const longUrl = `https://example.invalid/${"long-unbroken-path".repeat(14)}`;
export const longSubjectName = "M".repeat(100);
export const longMaterialName = `${"W".repeat(100)}.pdf`;

const summary = {
  contract_id: contractId, authority_mode: "native", name: longName, number: longNumber, amount: "123456.00",
  currency: "CNY", contract_state: "draft", workflow_state: "not_started", row_version: 7, created_at: at,
};

const draft = {
  contract: { name: longName, number: longNumber, remark: longUrl, related_parties: [{
    party_id: "90000000-0000-4000-8000-000000000001", role: "buyer", name_snapshot: longSubjectName,
    binding_id: "a0000000-0000-4000-8000-000000000001",
  }] },
  supplier: { name: longSupplier, bank_name: longBank },
  items: Array.from({ length: 3 }, (_, index) => ({
    item_id: `00000000-0000-4000-8000-00000000000${index + 1}`, name: `合成标的${index + 1}`, quantity: "123.000000", unit: "套",
  })),
  funding: Array.from({ length: 3 }, (_, index) => ({ nature: `资金${index + 1}`, channel: "合成渠道", amount: "12345.00", funding_year: 2026, cost_center: longNumber })),
  payments: Array.from({ length: 3 }, (_, index) => ({ sequence: index + 1, is_advance: false, amount: "12345.00", ratio_base: longUrl })),
  materials: Array.from({ length: 3 }, (_, index) => ({
    material_id: `10000000-0000-4000-8000-00000000000${index + 1}`,
    material_version_id: `20000000-0000-4000-8000-00000000000${index + 1}`,
    sha256: String(index + 1).repeat(64), purpose: "supporting",
  })),
};

export type FixtureMode = "empty" | "editable" | "readonly";

/** Answer only explicitly enumerated read endpoints, and never send writes to the server. */
export async function installContractFixture(page: Page, mode: FixtureMode, references = false) {
  const unmatched: string[] = [];
  const blocked: string[] = [];
  const referenceRequests: string[] = [];
  const origin = new URL(process.env.E2E_WEB ?? "http://127.0.0.1:5173").origin;
  const json = (route: Route, value: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const method = request.method();
    if (url.origin !== origin) {
      blocked.push(`${method} external origin`);
      return route.abort();
    }
    if (method !== "GET") {
      blocked.push(`${method} ${url.pathname}`);
      return json(route, { error: "synthetic fixture blocks mutations" }, 405);
    }
    if (!/^\/api(?:\/|$)/.test(url.pathname)) {
      if (url.pathname === "/contracts" || url.pathname === "/favicon.svg" || url.pathname === "/huangjueshu-competition-logo.png" || url.pathname === "/@react-refresh" ||
          /^\/(?:src|node_modules|@vite|@id|@fs)\//.test(url.pathname)) return route.continue();
      blocked.push(`${method} unexpected local path ${url.pathname}`);
      return route.abort();
    }
    if (url.pathname === "/api/session") return json(route, {
      actor: { id: "30000000-0000-4000-8000-000000000001", display_name: "合成审查员", role: "admin",
        tenant_id: "40000000-0000-4000-8000-000000000001", org_id: "50000000-0000-4000-8000-000000000001" },
      audience: "client", principal_id: "60000000-0000-4000-8000-000000000001",
      active_context: { membership_id: "70000000-0000-4000-8000-000000000001",
        tenant_id: "40000000-0000-4000-8000-000000000001", tenant_name: "合成租户",
        org_id: "50000000-0000-4000-8000-000000000001", org_name: "合成组织", context_version: "1" },
      csrf_token: "synthetic-token-not-a-real-credential", context_switch_enabled: false,
    });
    if (url.pathname === "/api/contracts") return json(route, { items: [], next_cursor: null, has_more: false });
    if (url.pathname === "/api/auth/contexts") return json(route, { memberships: [], context_switch_enabled: false });
    if (url.pathname === "/api/contracts/drafts") return json(route, { items: mode === "empty" ? [] : [summary] });
    if (url.pathname === "/api/contracts/ledger/views") return json(route, { items: [] });
    if (url.pathname === "/api/cases") return json(route, { items: references ? [{
      id: caseId, title: "合成来源案件", case_number: "SYNTHETIC-CASE", contract_type: "service", document_count: 3,
      pending_decisions: 0, latest_run: null, updated_at: at,
    }] : [] });
    if (url.pathname === `/api/contracts/${contractId}` && mode !== "empty") return json(route, {
      summary, current_revision_id: null, active_round_id: null,
      allowed_commands: mode === "editable" ? ["replace_draft"] : [], warnings: [],
      draft: mode === "readonly" ? { ...draft, supplier: { ...draft.supplier, bank_name: "••••", bank_account: "••••••••" } } : draft,
    });
    if (url.pathname === `/api/contracts/${contractId}/lifecycle-overview`) return json(route, {
      contract: { contract_id: contractId, current_revision_id: null, row_version: 7, authority_mode: "native" }, observed_at: at,
      completeness: { plans: "unknown", deliveries: "unknown", acceptances: "unknown", payments: "unknown", assets: "unknown",
        disputes: "unknown", warranty: "unknown", other_obligations: "unknown" },
      source_observed_at: { contract_recorded_at: null, finance: null, erp: null }, plans: null,
      deliveries: { items: [], row_version: 7, summary: [] }, acceptances: { items: [], row_version: 7, summary: [] },
      payments: null, assets: { contract_id: contractId, fixed_asset_status: "unknown", items: [], row_version: 7 },
    });
    if (url.pathname === `/api/contracts/${contractId}/registrations`) return json(route, { items: [] });
    if (url.pathname === `/api/contracts/${contractId}/supplements`) return json(route, { items: [], parent: null });
    if (references && url.pathname === `/api/contracts/${contractId}/reference-checks` &&
        url.searchParams.size === 1 && url.searchParams.get("case_id") === caseId) {
      referenceRequests.push(`${method} ${url.pathname}?case_id=${caseId}`);
      return json(route, {
        contract_id: contractId, source: "draft", contract_revision_id: null, source_case_id: caseId, checked_at: at,
        subjects: [{ party_id: draft.contract.related_parties[0]!.party_id, role: "buyer", name_snapshot: longSubjectName,
          binding_id: draft.contract.related_parties[0]!.binding_id, binding_version: 1, case_role: "customer",
          binding_state: "active", subject_version: 1, subject_payload_hash: "a".repeat(64),
          verification_state: "verified", frozen: false }],
        materials: draft.materials.map((material, index) => ({ ...material, material_version: index + 1,
          material_role: "award", filename: index === 0 ? longMaterialName : `合成材料${index + 1}.pdf`,
          media_type: "application/pdf", frozen: false,
          original_path: `/api/cases/${caseId}/documents/${material.material_version_id}/original` })),
        warnings: [],
      });
    }
    unmatched.push(`${method} ${url.pathname}`);
    return json(route, { error: "unmatched synthetic endpoint" }, 404);
  });
  return { unmatched, blocked, referenceRequests };
}
