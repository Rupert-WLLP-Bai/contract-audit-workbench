// File metadata (manifest mirror):
// Purpose: Checks the strict single-contract facts parser, including Unicode codepoint ranges, source binding, and partial-result semantics.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, it } from "vitest";
import { parseContractFacts } from "./contract-facts";

const digest = "a".repeat(64);
const source = {
  document_id: "document-1",
  parsed_document_id: "parsed-1",
  original_digest: digest,
  normalized_digest: "b".repeat(64),
};

function projection(overrides: Record<string, unknown> = {}) {
  return {
    schema_version: "contract-facts/v1",
    attempt_id: "attempt-1",
    status: "partial",
    structured_output_status: "valid",
    source,
    input_codepoints: 8,
    input_truncated: false,
    fields: [
      {
        field: "contract_amount",
        status: "known",
        value: "按原文记录",
        citations: [{ ...source, quote: "金额💰", start: 2, end: 5 }],
      },
      ...[
        "parties",
        "tax_treatment",
        "business_dates",
        "term",
        "payment_schedule",
        "subject_matter",
        "exceptions",
      ].map((field) => ({ field, status: "unknown", value: "", citations: [] })),
    ],
    ...overrides,
  };
}

describe("contract facts parser", () => {
  it("accepts exact Unicode codepoint spans and keeps unknown fields partial", () => {
    const result = parseContractFacts(projection());
    expect(result?.fields[0]?.citations[0]).toMatchObject({ quote: "金额💰", start: 2, end: 5 });
    expect(result?.fields.map((field) => field.field)).toContain("business_dates");
    expect(result?.status).toBe("partial");
  });

  it("rejects incomplete facts mislabeled as complete and complete facts mislabeled partial", () => {
    const raw = projection() as unknown as {
      status: string;
      fields: Array<Record<string, unknown>>;
    };
    expect(() => parseContractFacts({ ...raw, status: "suggested" })).toThrow(/coverage/);

    const completeFields = raw.fields.map((field) => ({
      ...field,
      status: "known",
      value: field.field === "contract_amount" ? "按原文记录" : "有依据的值",
      citations:
        field.field === "contract_amount"
          ? field.citations
          : [{ ...source, quote: "金额💰", start: 2, end: 5 }],
    }));
    expect(() => parseContractFacts({ ...raw, fields: completeFields })).toThrow(/coverage/);
  });

  it("rejects mismatched source identities, out-of-range offsets, and oversized values", () => {
    const raw = projection() as unknown as { fields: Array<Record<string, unknown>> };
    const fields = structuredClone(raw.fields) as Array<Record<string, unknown>>;
    fields[0]!.citations = [
      { ...source, parsed_document_id: "other-parsed", quote: "金额💰", start: 2, end: 5 },
    ];
    expect(() => parseContractFacts(projection({ fields }))).toThrow(/citations.source/);

    fields[0]!.citations = [{ ...source, quote: "金额💰", start: 6, end: 9 }];
    expect(() => parseContractFacts(projection({ fields }))).toThrow(/coverage/);

    fields[0]!.citations = [{ ...source, quote: "金额💰", start: 2, end: 5 }];
    fields[0]!.value = "值".repeat(1201);
    expect(() => parseContractFacts(projection({ fields }))).toThrow(/fields\[0\]\.value/);
  });

  it("requires a partial projection when the supplied body was truncated", () => {
    expect(() =>
      parseContractFacts(projection({ status: "suggested", input_truncated: true })),
    ).toThrow(/input/);
    expect(parseContractFacts(projection({ input_truncated: true }))?.status).toBe("partial");
  });

  it("keeps structured-output validity separate from partial fact coverage", () => {
    expect(
      parseContractFacts(projection({ structured_output_status: "invalid" }))
        ?.structured_output_status,
    ).toBe("invalid");
    expect(() => parseContractFacts(projection({ structured_output_status: "passed" }))).toThrow(
      /structured_output_status/,
    );
  });
});
