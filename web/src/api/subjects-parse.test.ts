import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  parseCaseRoleBinding,
  parseCaseSubjects,
  parseSubjectChangeDetail,
  parseSubjectChangeImpact,
  parseSubjectCaseLinks,
  parseSubjectList,
  parseSubjectSummary,
  parseSubjectSourceSnapshot,
  parseSubjectSourceSnapshotList,
  parseSubjectSourceSnapshotDetail,
  parseSubjectSourceDirectory,
  parseSubjectSourceVerificationResult,
  parseSubjectVersionProjection,
  parseMaterialParsedTextPage,
  parseMaterialParsedDocumentList,
} from "./subjects-parse";

const principal = { user_id: "u-1", issuer: "https://id.example", subject: "principal-1" };
const alias = { value: "示例贸易名称", kind: "trade" };
const maskedClaim = {
  namespace: "cn.gov.samr.uscc",
  identifier_type: "unified_social_credit_code",
  masked_value: "9132••••••••••01X3",
  verification_state: "pending_verification",
  source_kind: "user_asserted",
  source_time: null,
  payload_hash: "a".repeat(64),
};

function fixture(name: string): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/contracts/subjects", name), "utf8"));
}

function subjectVersion(overrides: Record<string, unknown> = {}) {
  return { ...parseSubjectVersionProjection(fixture("subject-version-no-claims.json")), ...overrides };
}

function binding(overrides: Record<string, unknown> = {}) {
  const value = parseCaseSubjects(fixture("case-subjects-legacy.json")).items[0];
  if (!value) throw new Error("case subject fixture has no bindings");
  return { ...value, ...overrides };
}

const summary = {
  request_id: "change-1",
  target_kind: "subject",
  state: "pending_decision",
  source_case_id: "c-1",
  target_subject_id: "s-1",
  target_binding_id: null,
  expected_subject_version: 1,
  expected_binding_version: null,
  payload_hash: "c".repeat(64),
  proposer: principal,
  proposed_by_display_name: "经办人甲",
  can_decide: true,
  decision_blocked_reason: null,
  created_at: "2026-10-31T01:00:00Z",
};

describe("subject API response parsing", () => {
  it("consumes backend-marshaled P2 directory, snapshot, verification and pinned-parse fixtures", () => {
    const directory = fixture("subject-sources.json");
    const snapshotList = fixture("subject-source-snapshots.json");
    const snapshotDetail = fixture("subject-source-snapshot-detail.json");
    const verifyMatch = fixture("subject-source-verify-match.json");
    const parsedList = fixture("material-parses.json");
    const parsedChunk = fixture("material-parsed-chunk.json");
    expect(parseSubjectSourceDirectory(directory).items[0]).toMatchObject({
      source_id: "synthetic.uscc.demo", verification_scope: "synthetic_controlled",
    });
    expect(parseSubjectSourceSnapshotList(snapshotList).items[0]?.record_count).toBeGreaterThan(0);
    expect(parseSubjectSourceSnapshotDetail(snapshotDetail)).toMatchObject({ case_id: "case_01" });
    expect(parseSubjectSourceVerificationResult(verifyMatch)).toMatchObject({
      snapshot_id: "ssnap_01", match_outcome: "exact_match", state: "pending_decision",
    });
    expect(parseMaterialParsedDocumentList(parsedList).items[0]?.parsed_document_id).toBe("parse_03");
    expect(parseMaterialParsedTextPage(parsedChunk)).toMatchObject({
      material_id: "mat_01", parsed_document_id: "parse_03", offset: 1, next_offset: 2,
    });
  });

  it("preserves exact legacy profile text and case role versions", () => {
    const value = parseCaseSubjects(fixture("case-subjects-legacy.json"));
    expect(value.legacy_profile).toEqual({ version: 1, customer: "旧档案客户", supplier: null });
    expect(value.can_manage).toBe(true);
    expect(value.items[0]?.legacy_label).toBe("旧档案客户");
    expect(value.items[0]?.binding_version).toBe(1);
    expect(value.items[0]?.subject.identity_claims).toEqual([]);
    const masked = parseCaseSubjects(fixture("case-subjects-masked.json"));
    expect(masked.items[0]?.subject.identity_claims[0]?.masked_value).toBe("9135••••••••••0Y43");
    expect(masked.legacy_profile).toBeNull();
    expect(masked.can_manage).toBe(false);
    const links = parseSubjectCaseLinks(fixture("subject-case-links.json"));
    expect(links).toMatchObject({ visible_count: 1, next_cursor: null });
    expect(links.items[0]).toMatchObject({
      case_title: "有权案件",
      business_status: "active",
    });
    expect(() => parseSubjectCaseLinks({ items: links.items, visible_count: 4, next_cursor: null })).toThrow(/visible_count/);
  });

  it("accepts only the known masked claim and server principal shape", () => {
    const value = parseSubjectSummary({
      subject_id: "s-1",
      current_version: 1,
      display_name: "示例公司",
      legal_name: null,
      verification_state: "pending_verification",
      aliases: [alias],
      created_at: "2026-10-31T01:00:00Z",
      created_by: principal,
      identifiers: [maskedClaim],
    });
    expect(value.identifiers[0]?.masked_value).toBe("9132••••••••••01X3");
    expect(() => parseSubjectSummary({ ...value, created_by: { user_id: "u-1" } })).toThrow(/created_by/);
    const currentBinding = binding();
    expect(() => parseCaseRoleBinding({
      ...currentBinding,
      subject: { ...currentBinding.subject, identity_claims: [{ ...maskedClaim, masked_value: "9132000000000001X3" }] },
    })).toThrow(/masked_value/);
    expect(() => parseSubjectSummary({
      subject_id: "s-1",
      current_version: 1,
      display_name: "示例公司",
      legal_name: null,
      verification_state: "pending_verification",
      aliases: [],
      created_at: "2026-10-31T01:00:00Z",
      created_by: principal,
      identifiers: [{ ...maskedClaim, value: "9132000000000001X3" }],
    })).toThrow(/value/);
  });

  it("parses authorized version projections without persisting identity claims", () => {
    const fullClaim = {
      namespace: "cn.gov.samr.uscc",
      identifier_type: "unified_social_credit_code",
      value: "9132000000000001X3",
      verification_state: "pending_verification",
      source_kind: "user_asserted",
      source_time: null,
      payload_hash: "a".repeat(64),
    };
    expect(() => parseSubjectVersionProjection(subjectVersion({ identity_claims: [fullClaim] }))).toThrow(/value/);
    const parsed = parseSubjectVersionProjection(subjectVersion({ identity_claims: [fullClaim] }), true);
    expect(parsed.identity_claims[0]?.value).toBe("9132000000000001X3");
    expect(() => parseSubjectVersionProjection(subjectVersion({ identity_claims: [{ ...fullClaim, value: "bad" }] }))).toThrow(/value/);
    const synthetic = parseSubjectVersionProjection(subjectVersion({
      source_kind: "authorized_correction",
      verification_scope: "synthetic_controlled",
      identity_claims: [{ ...maskedClaim, source_kind: "synthetic_controlled_snapshot", verification_state: "verified", verification_scope: "synthetic_controlled" }],
    }));
    expect(synthetic.verification_scope).toBe("synthetic_controlled");
    expect(synthetic.identity_claims[0]?.source_kind).toBe("synthetic_controlled_snapshot");
  });

  it("parses authorized subject list pages without requiring hidden totals", () => {
    const item = {
      subject_id: "s-1",
      current_version: 1,
      display_name: "示例公司",
      legal_name: null,
      verification_state: "pending_verification",
      aliases: [alias],
      created_at: "2026-10-31T01:00:00Z",
      created_by: principal,
      identifiers: [maskedClaim],
    };
    expect(parseSubjectList({ items: [item], next_cursor: null })).toEqual({ items: [expect.objectContaining({ subject_id: "s-1" })], next_cursor: null });
  });

  it("parses correction before/after from its target kind and keeps impact data authorized-only", () => {
    const after = subjectVersion({ version: 2, display_name: "示例企业" });
    const detail = parseSubjectChangeDetail({
      detail: {
        summary,
        proposed_by_display_name: "经办人甲",
        can_decide: true,
        decision_blocked_reason: null,
        before: subjectVersion(),
        after,
        basis_refs: [{
          kind: "material_anchor",
          material_id: "m-1",
          material_version: 1,
          document_id: "d-1",
          parsed_document_id: "p-1",
          start: 10,
          end: 14,
          quote_digest: "d".repeat(64),
          filename: "主体名称记载.txt",
          quote: "示例企业",
        }, {
          kind: "controlled_source",
          snapshot_id: "snap-1",
          source_catalog_id: "synthetic.uscc.demo",
          source_version: 1,
          source_digest: "e".repeat(64),
          source_scope: "synthetic_controlled",
          captured_at: "2026-10-31T01:00:00Z",
          source_as_of: "2026-10-30T00:00:00Z",
          valid_from: "2026-01-01T00:00:00Z",
          valid_until: null,
        }],
        impact_etag: "opaque-etag",
        authorized_impact_cases: [{ case_id: "c-1", case_name: "可见案件", status: "active" }],
      },
    });
    expect(detail.before).toMatchObject({ version: 1, display_name: "旧档案客户" });
    expect(detail.after).toMatchObject({ version: 2, display_name: "示例企业" });
    expect(detail.authorized_impact_cases).toHaveLength(1);
    expect(parseSubjectChangeImpact({
      request_id: "change-1",
      proposed_by_display_name: "经办人甲",
      can_decide: true,
      decision_blocked_reason: null,
      target_version: 1,
      payload_hash: "c".repeat(64),
      before: binding(),
      after: binding({ binding_version: 2 }),
      basis_refs: [],
      impact_etag: "etag",
      visible_affected_count: 1,
      affected_cases: [],
    }, "case_subject_role").after).toMatchObject({ binding_version: 2 });
  });

  it("keeps synthetic source evidence explicitly scoped and never treats a match as approval", () => {
    const snapshot = parseSubjectSourceSnapshot({
      snapshot_id: "snap-1",
      source_catalog_id: "synthetic.uscc.demo",
      source_version: 1,
      source_digest: "e".repeat(64),
      captured_at: "2026-10-31T01:00:00Z",
      source_scope: "synthetic_controlled",
      record_count: 3,
      label: "已对照核验（受控来源，合成演示）",
    });
    expect(snapshot.source_scope).toBe("synthetic_controlled");
    const savedSnapshot = {
      snapshot_id: "snap-1", source_catalog_id: "synthetic.uscc.demo", source_version: 1,
      source_digest: "e".repeat(64), source_scope: "synthetic_controlled",
      record_count: 3, label: "已对照核验（受控来源，合成演示）", captured_at: "2026-10-31T01:00:00Z",
    };
    expect(parseSubjectSourceSnapshotDetail({ ...savedSnapshot, case_id: "case-1" })).toEqual({ ...savedSnapshot, case_id: "case-1" });
    expect(parseSubjectSourceSnapshotList({ items: [savedSnapshot], next_cursor: "cursor/2" })).toMatchObject({
      items: [savedSnapshot], next_cursor: "cursor/2",
    });
    const exact = parseSubjectSourceVerificationResult({
      request_id: "change-2",
      snapshot_id: snapshot.snapshot_id,
      source_catalog_id: snapshot.source_catalog_id,
      source_version: snapshot.source_version,
      source_digest: snapshot.source_digest,
      source_scope: snapshot.source_scope,
      source_as_of: "2026-10-30T00:00:00Z",
      valid_from: "2026-01-01T00:00:00Z",
      valid_until: null,
      match_outcome: "exact_match",
      state: "pending_decision",
      matched_record: { display_name: "演示主体", masked_identifier: "9135••••••••••0Y43" },
    });
    expect(exact.state).toBe("pending_decision");
    expect(exact.matched_record?.masked_identifier).toBe("9135••••••••••0Y43");
    expect(parseSubjectSourceVerificationResult({
      request_id: null,
      snapshot_id: snapshot.snapshot_id,
      source_catalog_id: snapshot.source_catalog_id,
      source_version: snapshot.source_version,
      source_digest: snapshot.source_digest,
      source_scope: snapshot.source_scope,
      source_as_of: "2026-10-30T00:00:00Z",
      valid_from: "2026-01-01T00:00:00Z",
      valid_until: null,
      match_outcome: "no_match",
      state: "not_proposed",
      matched_record: null,
    })).toMatchObject({ match_outcome: "no_match", request_id: null, state: "not_proposed", matched_record: null });
    expect(() => parseSubjectSourceVerificationResult({
      request_id: "change-2",
      snapshot_id: snapshot.snapshot_id,
      source_catalog_id: snapshot.source_catalog_id,
      source_version: snapshot.source_version,
      source_digest: snapshot.source_digest,
      source_scope: "official_controlled",
      source_as_of: "2026-10-30T00:00:00Z",
      valid_from: "2026-01-01T00:00:00Z",
      valid_until: null,
      match_outcome: "exact_match",
      state: "pending_decision",
      matched_record: { display_name: "演示主体", masked_identifier: "9135••••••••••0Y43" },
    })).toThrow(/主体数据无效/);
  });

  it("parses rune-addressed pinned material text pages and rejects inconsistent ranges", () => {
    const page = {
      material_id: "mat-1", material_version: 3, document_id: "doc-1", parsed_document_id: "parse-3",
      filename: "历史合同.pdf", original_digest: "f".repeat(64), parse_digest: "a".repeat(64), offset: 12, total_runes: 15,
      text: "甲乙丙", next_offset: null,
    };
    expect(parseMaterialParsedTextPage(page)).toEqual(page);
    expect(() => parseMaterialParsedTextPage({ ...page, next_offset: 14 })).toThrow(/主体数据无效/);
    const parsed = {
      material_id: "mat-1", material_version: 3, document_id: "doc-1",
      items: [{ parsed_document_id: "parse-3", status: "ready", parser_id: "pdf", parser_version: "2", created_at: "2026-10-31T00:00:00Z", original_digest: "f".repeat(64), normalized_digest: "a".repeat(64) }],
    };
    expect(parseMaterialParsedDocumentList(parsed)).toEqual(parsed);
    expect(() => parseMaterialParsedDocumentList({ ...parsed, items: [{ ...parsed.items[0], status: "unknown" }] })).toThrow(/主体数据无效/);
  });
});
