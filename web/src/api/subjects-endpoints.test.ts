import {
  bindExistingSubject,
  createSubjectSourceSnapshot,
  createCaseRole,
  createSubject,
  decideSubjectChange,
  getCaseSubjects,
  getCaseSubjectChange,
  getCaseSubjectChangeImpact,
  getSubjectCaseLinks,
  getSubjectSourceSnapshot,
  listSubjectSourceSnapshots,
  getMaterialParsedTextPage,
  listMaterialParsedDocuments,
  listSubjects,
  proposeSubjectChange,
  verifySubjectAgainstSourceSnapshot,
} from "./endpoints";
import type { SubjectChangeProposalInput } from "./types";

const principal = { user_id: "u-1", issuer: "https://id.example", subject: "principal-1" };
const claim = {
  namespace: "cn.gov.samr.uscc",
  identifier_type: "unified_social_credit_code",
  masked_value: "9132••••••••••01X3",
  verification_state: "pending_verification",
  source_kind: "user_asserted",
  source_time: null,
  payload_hash: "a".repeat(64),
};
const version = {
  subject_id: "s-1",
  version: 1,
  display_name: "示例公司",
  legal_name: null,
  verification_state: "pending_verification",
  source_kind: "user_asserted",
  basis_summary: "经办人录入，待核验",
  payload_hash: "b".repeat(64),
  identity_claims: [claim],
  aliases: [],
  created_by: principal,
  created_at: "2026-10-31T01:00:00Z",
};

function subjectSummary() {
  return {
    subject_id: "s-1",
    current_version: 1,
    display_name: "示例公司",
    legal_name: null,
    verification_state: "pending_verification",
    aliases: [],
    created_at: "2026-10-31T01:00:00Z",
    created_by: principal,
    identifiers: [claim],
  };
}

function setResponse(body: unknown, status = 200) {
  const fetchMock = vi.fn<typeof fetch>(async () => new Response(JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("subject API endpoints", () => {
  it("reads authorized-only subject summaries and case role plus legacy profile", async () => {
    const fetchMock = setResponse({ items: [subjectSummary()], next_cursor: null });
    const page = await listSubjects("case/1", "示例");
    expect(page.items[0]?.subject_id).toBe("s-1");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/subjects?case_id=case%2F1&q=%E7%A4%BA%E4%BE%8B&limit=50",
      expect.objectContaining({ method: "GET", credentials: "include", cache: "no-store" }),
    );

    setResponse({ items: [], legacy_profile: { version: 4, customer: "旧客户", supplier: null } });
    expect(await getCaseSubjects("case/1")).toEqual({
      items: [], legacy_profile: { version: 4, customer: "旧客户", supplier: null }, can_manage: false,
    });
  });

  it("creates and binds subjects with explicit CAS fields and required idempotency keys", async () => {
    const created = {
      subject_id: "s-1",
      current_version: 1,
      subject_version: version,
      created_at: "2026-10-31T01:00:00Z",
    };
    const fetchMock = setResponse(created, 201);
    const result = await createSubject({ display_name: "示例公司" }, "create-key-1");
    expect(result).toMatchObject({ subject_id: "s-1", current_version: 1 });
    expect(fetchMock).toHaveBeenCalledWith("/api/subjects", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({ "Idempotency-Key": "create-key-1", "Content-Type": "application/json" }),
      body: JSON.stringify({ display_name: "示例公司" }),
    }));

    const bindFetch = setResponse({
      binding_id: "b-1", binding_version: 1, subject_id: "s-1", subject_version: 1,
      role: "customer", display_name: "示例公司", verification_state: "pending_verification",
      binding_state: "active", created_at: "2026-10-31T01:00:00Z", source_kind: "user_asserted",
    }, 201);
    await bindExistingSubject("case/1", { role: "customer", subject_id: "s-1", expected_subject_version: 1 }, "bind-key-1");
    expect(bindFetch).toHaveBeenCalledWith("/api/cases/case%2F1/subjects/bind-existing", expect.objectContaining({
      headers: expect.objectContaining({ "Idempotency-Key": "bind-key-1" }),
      body: JSON.stringify({ role: "customer", subject_id: "s-1", expected_subject_version: 1 }),
    }));
  });

  it("adopts legacy labels only with exact profile CAS and sends correction CAS internally", async () => {
    const response = {
      binding_id: "b-1", binding_version: 1, subject_id: "s-1", subject_version: 1,
      role: "customer", display_name: "示例公司", verification_state: "pending_verification",
      source_kind: "legacy_label", legacy_label: "旧客户", binding_state: "pending_confirmation",
    };
    const fetchMock = setResponse(response, 201);
    await createCaseRole("case-1", {
      role: "customer",
      subject: { display_name: "示例公司" },
      legacy_binding: { field: "customer", value: "旧客户", expected_profile_version: 4 },
    }, "role-key-1");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({
      headers: expect.objectContaining({ "Idempotency-Key": "role-key-1" }),
      body: JSON.stringify({
        role: "customer",
        subject: { display_name: "示例公司" },
        legacy_binding: { field: "customer", value: "旧客户", expected_profile_version: 4 },
      }),
    });

    const basis = { kind: "material_anchor" as const, material_id: "m-1", material_version: 1, document_id: "d-1", parsed_document_id: "p-1", start: 0, end: 2, quote_digest: "d".repeat(64) };
    const proposal: SubjectChangeProposalInput = {
      target: { kind: "subject", subject_id: "s-1", binding_id: null },
      expected_subject_version: 1,
      expected_binding_version: null,
      patch: { display_name: "示例企业" },
      basis_note: "材料记载了更新后的名称",
      basis_refs: [basis],
    };
    const proposalFetch = setResponse({ request_id: "change-1", target_kind: "subject", expected_subject_version: 1, expected_binding_version: null, payload_hash: "c".repeat(64), state: "pending_decision", proposed_by_display_name: "经办人甲", can_decide: false, decision_blocked_reason: "本人不能复核" }, 201);
    await proposeSubjectChange("case-1", proposal, "change-key-1");
    expect(proposalFetch).toHaveBeenCalledWith("/api/cases/case-1/subject-changes", expect.objectContaining({
      headers: expect.objectContaining({ "Idempotency-Key": "change-key-1" }),
      body: JSON.stringify(proposal),
    }));

    const changeSummary = {
      request_id: "change-1", target_kind: "subject", state: "pending_decision",
      target_subject_id: "s-1", target_binding_id: null, expected_subject_version: 1,
      expected_binding_version: null, payload_hash: "c".repeat(64), proposed_by: principal,
      proposed_by_display_name: "经办人甲", can_decide: false, decision_blocked_reason: "本人不能复核", created_at: "2026-10-31T01:00:00Z",
    };
    setResponse({
      summary: changeSummary,
      proposed_by_display_name: "经办人甲", can_decide: false, decision_blocked_reason: "本人不能复核",
      before: version,
      after: { ...version, version: 2, display_name: "示例企业" },
      basis_note: "依据材料更正名称",
      basis_refs: [{ ...basis, filename: "主体资料.txt", quote: "示例公司" }],
    });
    expect((await getCaseSubjectChange("case-1", "change/1")).summary.request_id).toBe("change-1");

    setResponse({ request_id: "change-1", proposed_by_display_name: "经办人甲", can_decide: false, decision_blocked_reason: "本人不能复核", target_version: 1, payload_hash: "c".repeat(64), before: version,
      after: { ...version, version: 2, display_name: "示例企业" }, basis_refs: [{ ...basis, filename: "主体资料.txt", quote: "示例公司" }],
      impact_etag: "opaque-etag", visible_affected_count: 0, affected_cases: [] });
    expect((await getCaseSubjectChangeImpact("case-1", "change/1", "subject")).impact_etag).toBe("opaque-etag");

    const decisionFetch = setResponse({ request_id: "change-1", decision: "approved", applied_subject_version: 2, binding_versions_appended: 1 });
    await decideSubjectChange("case-1", "change/1", {
      decision: "approve", expected_subject_version: 1, expected_binding_version: null,
      expected_payload_hash: "c".repeat(64), expected_impact_etag: "opaque-etag", reason: "依据核对无误",
    }, "decision-key-1");
    expect(decisionFetch).toHaveBeenCalledWith("/api/cases/case-1/subject-changes/change%2F1/decisions", expect.objectContaining({
      headers: expect.objectContaining({ "Idempotency-Key": "decision-key-1" }),
    }));
  });

  it("creates a versioned synthetic source snapshot and requests an explicit proposal without direct verification", async () => {
    const sourceFetch = setResponse({
      snapshot_id: "snap-1", source_catalog_id: "synthetic.uscc.demo", source_version: 1,
      source_digest: "e".repeat(64), captured_at: "2026-10-31T01:00:00Z",
      source_scope: "synthetic_controlled", record_count: 3,
      label: "已对照核验（受控来源，合成演示）",
    }, 201);
    const snapshot = await createSubjectSourceSnapshot("case-1", {
      source_catalog_id: "synthetic.uscc.demo", expected_source_version: 1,
    }, "snapshot-key-1");
    expect(sourceFetch).toHaveBeenCalledWith("/api/cases/case-1/subject-source-snapshots", expect.objectContaining({
      headers: expect.objectContaining({ "Idempotency-Key": "snapshot-key-1" }),
      body: JSON.stringify({ source_catalog_id: "synthetic.uscc.demo", expected_source_version: 1 }),
    }));

    const verifyFetch = setResponse({
      request_id: "change-3", snapshot_id: "snap-1", source_catalog_id: "synthetic.uscc.demo",
      source_version: 1, source_digest: "e".repeat(64), source_scope: "synthetic_controlled",
      source_as_of: "2026-10-30T00:00:00Z", valid_from: "2026-01-01T00:00:00Z", valid_until: null,
      match_outcome: "exact_match", state: "pending_decision",
      matched_record: { display_name: "示例公司", masked_identifier: "9135••••••••••0Y43" },
    }, 202);
    const result = await verifySubjectAgainstSourceSnapshot("case-1", snapshot.snapshot_id, {
      subject_id: "s-1", expected_subject_version: 1,
    }, "verify-key-1");
    expect(verifyFetch).toHaveBeenCalledWith("/api/cases/case-1/subject-source-snapshots/snap-1/verify", expect.objectContaining({
      headers: expect.objectContaining({ "Idempotency-Key": "verify-key-1" }),
      body: JSON.stringify({ subject_id: "s-1", expected_subject_version: 1 }),
    }));
    expect(result.state).toBe("pending_decision");
  });

  it("reads paged authorized case links, resumable source snapshots and fixed parsed material versions", async () => {
    const caseLink = {
      binding_id: "b-1", binding_version: 1, case_id: "c-1", case_title: "可见案件", business_status: "active",
      role: "supplier", binding_state: "active", source_kind: "user_asserted", legacy_label: null,
      created_by: principal, subject: version, created_at: "2026-10-31T01:00:00Z",
    };
    let fetchMock = setResponse({ items: [caseLink], visible_count: 1, next_cursor: "cursor/2" });
    const links = await getSubjectCaseLinks("s-1", "cursor/1", 50);
    expect(links.items[0]?.case_title).toBe("可见案件");
    expect(fetchMock).toHaveBeenCalledWith("/api/subjects/s-1/case-links?cursor=cursor%2F1&limit=50", expect.anything());

    const snapshot = {
      snapshot_id: "snap-1", source_catalog_id: "synthetic.uscc.demo", source_version: 1,
      source_digest: "e".repeat(64), source_scope: "synthetic_controlled", record_count: 3,
      label: "已对照核验（受控来源，合成演示）", captured_at: "2026-10-31T01:00:00Z",
    };
    fetchMock = setResponse({ items: [snapshot], next_cursor: null });
    expect((await listSubjectSourceSnapshots("c-1")).items[0]).toMatchObject({ snapshot_id: "snap-1" });
    expect(fetchMock).toHaveBeenCalledWith("/api/cases/c-1/subject-source-snapshots?limit=50", expect.anything());
    fetchMock = setResponse({ ...snapshot, case_id: "c-1" });
    expect((await getSubjectSourceSnapshot("c-1", "snap-1")).source_scope).toBe("synthetic_controlled");
    expect(fetchMock).toHaveBeenCalledWith("/api/cases/c-1/subject-source-snapshots/snap-1", expect.anything());

    fetchMock = setResponse({
      material_id: "mat-1", material_version: 2, document_id: "doc-1",
      items: [{ parsed_document_id: "parse-2", status: "ready", parser_id: "pdf", parser_version: "1", created_at: "2026-10-31T01:00:00Z", original_digest: "f".repeat(64), normalized_digest: "a".repeat(64) }],
    });
    expect((await listMaterialParsedDocuments("c-1", "mat-1", 2)).items[0]?.parsed_document_id).toBe("parse-2");
    expect(fetchMock).toHaveBeenCalledWith("/api/cases/c-1/materials/mat-1/versions/2/parsed-documents", expect.anything());

    fetchMock = setResponse({
      material_id: "mat-1", material_version: 2, document_id: "doc-1", parsed_document_id: "parse-2",
      filename: "历史采购合同.pdf", original_digest: "f".repeat(64), parse_digest: "a".repeat(64), offset: 0, total_runes: 3, text: "历史文", next_offset: null,
    });
    expect((await getMaterialParsedTextPage("c-1", "mat-1", 2, "parse-2")).parsed_document_id).toBe("parse-2");
    expect(fetchMock).toHaveBeenCalledWith("/api/cases/c-1/materials/mat-1/versions/2/parsed-documents/parse-2?offset=0&limit=32768", expect.anything());
  });
});
