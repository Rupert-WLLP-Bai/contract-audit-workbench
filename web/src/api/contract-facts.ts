// File metadata (manifest mirror):
// Purpose: Parses the versioned single-contract facts projection with bounded fields, explicit uncertainty and exact frozen-source citation identities.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { responseReader } from "./respond";

export type ContractFactFieldName =
  | "parties"
  | "contract_amount"
  | "tax_treatment"
  | "business_dates"
  | "term"
  | "payment_schedule"
  | "subject_matter"
  | "exceptions";

export type ContractFactStatus = "known" | "unknown" | "conflicting";
export type ContractFactsStructuredOutputStatus = "valid" | "invalid" | "not_returned";

export interface ContractFactsSource {
  document_id: string;
  parsed_document_id: string;
  original_digest: string;
  normalized_digest: string;
}

export interface ContractFactsCitation extends ContractFactsSource {
  quote: string;
  start: number;
  end: number;
}

export interface ContractFact {
  field: ContractFactFieldName;
  status: ContractFactStatus;
  value: string;
  citations: ContractFactsCitation[];
}

export interface ContractFactsProjection {
  schema_version: "contract-facts/v1";
  attempt_id: string;
  status: "suggested" | "partial";
  structured_output_status: ContractFactsStructuredOutputStatus;
  source: ContractFactsSource;
  input_codepoints: number;
  input_truncated: boolean;
  fields: ContractFact[];
}

const fieldNames: ContractFactFieldName[] = [
  "parties",
  "contract_amount",
  "tax_treatment",
  "business_dates",
  "term",
  "payment_schedule",
  "subject_matter",
  "exceptions",
];

const { invalid, record, text, integer, bool, array } = responseReader(
  (path) => `合同事实建议中的 ${path} 无法识别`,
);

function source(value: unknown, path: string): ContractFactsSource {
  const item = record(value, path, [
    "document_id",
    "parsed_document_id",
    "original_digest",
    "normalized_digest",
  ]);
  const result = {
    document_id: text(item.document_id, `${path}.document_id`),
    parsed_document_id: text(item.parsed_document_id, `${path}.parsed_document_id`),
    original_digest: text(item.original_digest, `${path}.original_digest`),
    normalized_digest: text(item.normalized_digest, `${path}.normalized_digest`),
  };
  if (
    Object.values(result).some((entry) => entry.trim() === "") ||
    !/^[0-9a-f]{64}$/.test(result.original_digest) ||
    !/^[0-9a-f]{64}$/.test(result.normalized_digest)
  ) {
    return invalid(`${path}.identity`);
  }
  return result;
}

function citation(value: unknown, path: string): ContractFactsCitation {
  const item = record(value, path, [
    "document_id",
    "parsed_document_id",
    "original_digest",
    "normalized_digest",
    "quote",
    "start",
    "end",
  ]);
  const quote = text(item.quote, `${path}.quote`);
  const start = integer(item.start, `${path}.start`, 0);
  const end = integer(item.end, `${path}.end`, 1);
  if (end <= start || end - start !== Array.from(quote).length || Array.from(quote).length > 500) {
    return invalid(`${path}.text_offset`);
  }
  return {
    document_id: text(item.document_id, `${path}.document_id`),
    parsed_document_id: text(item.parsed_document_id, `${path}.parsed_document_id`),
    original_digest: text(item.original_digest, `${path}.original_digest`),
    normalized_digest: text(item.normalized_digest, `${path}.normalized_digest`),
    quote,
    start,
    end,
  };
}

function fact(value: unknown, path: string): ContractFact {
  const item = record(value, path, ["field", "status", "value", "citations"]);
  const field = text(item.field, `${path}.field`);
  const status = text(item.status, `${path}.status`);
  if (
    !fieldNames.includes(field as ContractFactFieldName) ||
    (status !== "known" && status !== "unknown" && status !== "conflicting")
  ) {
    return invalid(`${path}.identity`);
  }
  const valueText = text(item.value, `${path}.value`);
  const citations = array(item.citations, `${path}.citations`, citation);
  if (
    Array.from(valueText).length > 1200 ||
    (status === "unknown" && (valueText !== "" || citations.length !== 0)) ||
    (status === "known" && (valueText.trim() === "" || citations.length === 0)) ||
    (status === "conflicting" && (valueText !== "" || citations.length < 2)) ||
    citations.length > 3
  ) {
    return invalid(`${path}.status`);
  }
  return { field: field as ContractFactFieldName, status, value: valueText, citations };
}

export function parseContractFacts(value: unknown): ContractFactsProjection | null {
  if (value === null || value === undefined) return null;
  const item = record(value, "contract_facts", [
    "schema_version",
    "attempt_id",
    "status",
    "structured_output_status",
    "source",
    "input_codepoints",
    "input_truncated",
    "fields",
  ]);
  const schemaVersion = text(item.schema_version, "contract_facts.schema_version");
  const status = text(item.status, "contract_facts.status");
  const structuredOutputStatus = text(
    item.structured_output_status,
    "contract_facts.structured_output_status",
  );
  if (
    schemaVersion !== "contract-facts/v1" ||
    (status !== "suggested" && status !== "partial") ||
    (structuredOutputStatus !== "valid" &&
      structuredOutputStatus !== "invalid" &&
      structuredOutputStatus !== "not_returned")
  ) {
    return invalid("contract_facts.schema_version");
  }
  const sourceValue = source(item.source, "contract_facts.source");
  const fields = array(item.fields, "contract_facts.fields", fact);
  if (
    fields.length !== fieldNames.length ||
    new Set(fields.map((entry) => entry.field)).size !== fieldNames.length ||
    fieldNames.some((name) => !fields.some((entry) => entry.field === name))
  ) {
    return invalid("contract_facts.fields");
  }
  for (const entry of fields) {
    for (const ref of entry.citations) {
      if (
        ref.document_id !== sourceValue.document_id ||
        ref.parsed_document_id !== sourceValue.parsed_document_id ||
        ref.original_digest !== sourceValue.original_digest ||
        ref.normalized_digest !== sourceValue.normalized_digest
      ) {
        return invalid("contract_facts.citations.source");
      }
    }
  }
  const inputCodepoints = integer(item.input_codepoints, "contract_facts.input_codepoints", 1);
  const inputTruncated = bool(item.input_truncated, "contract_facts.input_truncated");
  if (inputCodepoints > 32000 || (inputTruncated && status !== "partial")) {
    return invalid("contract_facts.input");
  }
  const hasPartialField = fields.some((entry) => entry.status !== "known");
  if (
    (status === "suggested" && (hasPartialField || inputTruncated)) ||
    (status === "partial" && !hasPartialField && !inputTruncated) ||
    fields.some((entry) => entry.citations.some((ref) => ref.end > inputCodepoints))
  ) {
    return invalid("contract_facts.coverage");
  }
  for (const entry of fields) {
    if (
      (entry.status === "known" && entry.citations.length > 3) ||
      (entry.status === "conflicting" && entry.citations.length > 3)
    ) {
      return invalid("contract_facts.citations");
    }
  }
  const attemptId = text(item.attempt_id, "contract_facts.attempt_id");
  if (attemptId.trim() === "") return invalid("contract_facts.attempt_id");
  return {
    schema_version: schemaVersion,
    attempt_id: attemptId,
    status,
    structured_output_status: structuredOutputStatus,
    source: sourceValue,
    input_codepoints: inputCodepoints,
    input_truncated: inputTruncated,
    fields,
  };
}
