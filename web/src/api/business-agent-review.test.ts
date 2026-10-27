import { describe, expect, it } from "vitest";
import { parseBusinessAgentReview } from "./business-agent-review";

const anchor = {
  id: "anchor-1",
  document_id: "doc-1",
  parsed_document_id: "parsed-1",
  quote: "质保36个月",
  status: "located",
  start: 0,
  end: 7,
  reason: null,
  page: null,
  element_id: null,
};
const capability = {
  capability_id: "warranty_months",
  mandatory: true,
  status: "insufficient",
  relation: "insufficient",
  reason_code: "required_source_or_field_missing",
  evidence_ids: ["evidence-1"],
  canonical_evaluation_ids: null,
};
const evidence = {
  evidence_id: "evidence-1",
  semantic_type: "warranty",
  value: "36",
  unit: "month",
  currency: null,
  verification: "typed_semantic_and_frozen_quote_verified",
  anchor,
};
const projection = {
  attempt_id: "attempt-1",
  status: "partial",
  rulepack: { id: "rules", version: "1" },
  scope: [{ domain: "technical", capability_ids: ["warranty_months"], source_ids: null }],
  domains: [{ domain: "technical", status: "partial", capabilities: [capability] }],
  gaps: [{ gap_id: "gap-1", domain: "technical", capability_id: "warranty_months", mandatory: true, status: "open", reason_code: "required_source_or_field_missing", evidence_ids: null }],
  evidence: [evidence],
  unresolved_gap_ids: ["gap-1"],
};

describe("business agent review projection", () => {
  it("preserves the actual public fields and null collections", () => {
    expect(parseBusinessAgentReview(projection)).toEqual(projection);
    expect(parseBusinessAgentReview(null)).toBeNull();
    expect(parseBusinessAgentReview(undefined)).toBeNull();
    expect(parseBusinessAgentReview({ ...projection, scope: null, domains: null, gaps: null, evidence: null, unresolved_gap_ids: null })).toMatchObject({
      scope: null, domains: null, gaps: null, evidence: null, unresolved_gap_ids: null,
    });
  });

  it("retains unfamiliar status values without interpreting them as success", () => {
    const result = parseBusinessAgentReview({ ...projection, status: "new-status", domains: [{ domain: "technical", status: "new-status", capabilities: [{ ...capability, status: "new-status", relation: null }] }] });
    expect(result?.status).toBe("new-status");
    expect(result?.domains?.[0]?.capabilities?.[0]?.status).toBe("new-status");
  });

  it("rejects missing or malformed business fields", () => {
    const missingMandatory: Record<string, unknown> = { ...capability };
    delete missingMandatory.mandatory;
    expect(() => parseBusinessAgentReview({ ...projection, domains: [{ domain: "technical", status: "partial", capabilities: [missingMandatory] }] })).toThrow(/capabilities\[0\]/);
    expect(() => parseBusinessAgentReview({ ...projection, gaps: "none" })).toThrow(/agent_review.gaps/);
    expect(() => parseBusinessAgentReview({ ...projection, evidence: [{ ...evidence, anchor: { ...anchor, start: 1.5 } }] })).toThrow(/anchor.start/);
  });

  it("rejects diagnostic fields rather than carrying them into the business view", () => {
    expect(() => parseBusinessAgentReview({ ...projection, planner: { prompt_sha256: "private" } })).toThrow(/agent_review/);
    expect(() => parseBusinessAgentReview({ ...projection, evidence: [{ ...evidence, anchor: { ...anchor, normalized_digest: "private" } }] })).toThrow(/anchor/);
  });
});
