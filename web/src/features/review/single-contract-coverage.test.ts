import { describe, expect, it } from "vitest";
import { parseReviewWorkspace } from "../../api/parse";
import { isLegacyPaymentCoverageFinding, singleContractCoverage } from "./single-contract-coverage";

const legacyReason = "未识别到支持的付款比例表达；不能推断合同未约定付款，需人工核对原文。";
const identityHash = {
  "1": "51676a95611b6e1bf4c30fc50be82e1d493528f34b89f3ece9dbb5aa6c291596",
  "2": "3a26ce92e0dfd674d444f3ab2ca832d71d161f84d0a8bb94f8eee33c7ff2f8ab",
};
const evidence = {
  id: "quote",
  document_version_id: "doc",
  parsed_document_id: "parse",
  status: "located",
  quote: "两个月",
  text_offset: { start: 1, end: 4 },
  observation_labels: ["付款期限"],
};

function workspace(version: "1" | "2", override: object = {}) {
  const scope = {
    id: "single_contract_precheck",
    rule_set_id: "single-contract-precheck",
    rule_set_version: version,
    rule_set_hash: identityHash[version],
  };
  const attempt = {
    id: "attempt",
    run_id: "run",
    ordinal: 1,
    state: "partial",
    history_kind: "frozen",
    parsed_document_ids: ["parse"],
    review_scope: scope,
  };
  const legacy = version === "1";
  const rule = legacy ? "single-payment-sum-v1" : "single-payment-terms-v2";
  const domain = legacy ? "payment_sum" : "payment_terms";
  return parseReviewWorkspace({
    run: { id: "run", case_id: "case", state: "partial" },
    review_attempts: { attempt, attempts: [attempt] },
    snapshot: { document_ids: ["doc"], review_scope: scope },
    documents: [
      {
        id: "doc",
        role: "draft_contract",
        parse_status: "ready",
        parsed_document_id: "parse",
        text: "😀两个月内付款。",
      },
    ],
    evaluations: [
      {
        id: "eval",
        rule_id: rule,
        rule_version: rule,
        domain,
        capability_id: "single_contract." + domain,
        relation: "insufficient",
        reason: legacy ? legacyReason : "已定位付款期限。",
        reason_code: legacy ? "" : "payment_terms_observed",
        finding_id: legacy ? "finding" : null,
        evidence: legacy ? undefined : [evidence],
        ...override,
      },
    ],
    findings: legacy
      ? [
          {
            id: "finding",
            origin: "rule",
            domain,
            claim: legacyReason,
            relation: "insufficient",
            version: 1,
            evidence: [],
            decisions: [],
          },
        ]
      : [],
    execution: null,
    audit_events: [],
  });
}

describe("frozen single-contract coverage", () => {
  it("identifies only the original linked v1 no-result item", () => {
    const selected = workspace("1");
    const finding = selected.findings[0]!;
    expect(isLegacyPaymentCoverageFinding(selected, finding)).toBe(true);
    expect(isLegacyPaymentCoverageFinding(selected, { ...finding, version: 2 })).toBe(false);
    expect(isLegacyPaymentCoverageFinding(selected, { ...finding, id: "other" })).toBe(false);
    expect(isLegacyPaymentCoverageFinding(selected, { ...finding, origin: "manual" })).toBe(false);
    expect(isLegacyPaymentCoverageFinding(selected, { ...finding, claim: "别的线索" })).toBe(false);
    const withAnchor = {
      ...finding,
      evidence: [
        {
          ...evidence,
          status: "located" as const,
          normalized_digest: null,
          page: null,
          bbox: null,
          mismatch_reason: null,
        },
      ],
    };
    expect(isLegacyPaymentCoverageFinding(selected, withAnchor)).toBe(false);
  });

  it("exposes pinned v2 observations and rejects differing text or parse identity", () => {
    expect(singleContractCoverage(workspace("2"))).toMatchObject({
      kind: "known",
      version: "2",
      payment: {
        status: "observed",
        evidence: [{ quote: "两个月", observation_labels: ["付款期限"] }],
      },
    });
    expect(
      singleContractCoverage(
        workspace("2", { evidence: [{ ...evidence, parsed_document_id: "other" }] }),
      ),
    ).toMatchObject({
      kind: "known",
      payment: { status: "unconfirmed" },
    });
    expect(
      singleContractCoverage(workspace("2", { evidence: [{ ...evidence, quote: "三个月" }] })),
    ).toMatchObject({
      kind: "known",
      payment: { status: "unconfirmed" },
    });
  });
});
