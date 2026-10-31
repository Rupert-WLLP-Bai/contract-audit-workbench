import { paths } from "./paths";
import { requestJson } from "./http";
import { responseReader } from "./respond";

/** One verified business-agent citation read at its frozen position. */
export interface FrozenContractCitation {
  case_id: string;
  run_id: string;
  attempt_id: string;
  evidence_id: string;
  document_id: string;
  parsed_document_id: string;
  filename: string;
  original_digest: string;
  parse_digest: string;
  start: number;
  end: number;
  quote: string;
  text: string;
  material_id: string | null;
  material_version: number | null;
}

const { record, text, integer, nullableText } = responseReader(
  (path) => `冻结引用响应中的 ${path} 无法识别`,
);

const FIELDS = [
  "case_id",
  "run_id",
  "attempt_id",
  "evidence_id",
  "document_id",
  "parsed_document_id",
  "filename",
  "original_digest",
  "parse_digest",
  "start",
  "end",
  "quote",
  "text",
  "material_id",
  "material_version",
] as const;

function digest(value: unknown, path: string): string {
  const result = text(value, path);
  if (!/^[0-9a-f]{64}$/.test(result)) {
    throw new Error(`冻结引用响应中的 ${path} 不是 SHA-256`);
  }
  return result;
}

export function parseFrozenContractCitation(
  value: unknown,
  path = "citation",
): FrozenContractCitation {
  const row = record(value, path, FIELDS, FIELDS);
  const start = integer(row.start, `${path}.start`, 0);
  const end = integer(row.end, `${path}.end`, 0);
  const quote = text(row.quote, `${path}.quote`);
  const textValue = text(row.text, `${path}.text`);
  // The highlight is a Unicode code-point slice. Reject a payload whose range
  // does not reproduce its own quote instead of highlighting nearby text.
  const characters = Array.from(textValue);
  if (quote === "" || end <= start || end > characters.length) {
    throw new Error(`冻结引用响应中的 ${path} 区间无效`);
  }
  if (characters.slice(start, end).join("") !== quote) {
    throw new Error(`冻结引用响应中的 ${path} 区间与引用原文不一致`);
  }
  const materialId = nullableText(row.material_id, `${path}.material_id`);
  const materialVersion = row.material_version === null
    ? null
    : integer(row.material_version, `${path}.material_version`, 1);
  if ((materialId === null) !== (materialVersion === null)) {
    throw new Error(`冻结引用响应中的 ${path} 材料身份不完整`);
  }
  return {
    case_id: text(row.case_id, `${path}.case_id`),
    run_id: text(row.run_id, `${path}.run_id`),
    attempt_id: text(row.attempt_id, `${path}.attempt_id`),
    evidence_id: text(row.evidence_id, `${path}.evidence_id`),
    document_id: text(row.document_id, `${path}.document_id`),
    parsed_document_id: text(row.parsed_document_id, `${path}.parsed_document_id`),
    filename: text(row.filename, `${path}.filename`),
    original_digest: digest(row.original_digest, `${path}.original_digest`),
    parse_digest: digest(row.parse_digest, `${path}.parse_digest`),
    start,
    end,
    quote,
    text: textValue,
    material_id: materialId,
    material_version: materialVersion,
  };
}

export function getFrozenContractCitation(
  caseId: string,
  runId: string,
  attemptId: string,
  evidenceId: string,
  signal?: AbortSignal,
) {
  return requestJson(
    paths.frozenContractCitation(caseId, runId, attemptId, evidenceId),
    { method: "GET", signal },
    (value) => parseFrozenContractCitation(value),
  );
}
