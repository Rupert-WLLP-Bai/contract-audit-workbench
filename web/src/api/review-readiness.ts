import { requestJson } from "./http";
import { paths } from "./paths";
import { responseReader } from "./respond";

export interface ReviewReadinessMaterial {
  identity_kind: string;
  material_id: string;
  version: number;
  document_id: string;
  filename: string;
  role: string;
  purpose: string;
  selected: boolean;
  parse: { status: string; parsed_document_id: string | null; next_action: string };
}
export interface ReviewReadinessBlocker {
  code: string;
  status: string;
  message: string;
  missing_dependencies: string[];
  next_action: string;
}
export interface ReviewReadinessScope {
  id: string;
  status: string;
  can_start: boolean;
  rule_pack: { pack_id: string; version: string; snapshot_digest: string } | null;
  checks: { rule_id: string; status: string; missing_dependencies: string[]; next_action: string }[];
  blockers: ReviewReadinessBlocker[];
}
export interface ReviewReadiness {
  case_id: string;
  revision: string;
  business_at: string | null;
  can_start: boolean;
  material_selection_version: number;
  materials: ReviewReadinessMaterial[];
  scopes: ReviewReadinessScope[];
}
const reader = responseReader((path) => `审查准备清单中的 ${path} 无效`);

function material(value: unknown, path: string): ReviewReadinessMaterial {
  const row = reader.record(value, path, ["identity_kind", "material_id", "version", "document_id", "filename", "role", "purpose", "selected", "parse"]);
  const parse = reader.record(row.parse, `${path}.parse`, ["status", "parsed_document_id", "parser_id", "parser_version", "next_action"], ["status", "next_action"]);
  return {
    identity_kind: reader.text(row.identity_kind, `${path}.identity_kind`),
    material_id: reader.text(row.material_id, `${path}.material_id`),
    version: reader.integer(row.version, `${path}.version`, 0),
    document_id: reader.text(row.document_id, `${path}.document_id`),
    filename: reader.text(row.filename, `${path}.filename`),
    role: reader.text(row.role, `${path}.role`),
    purpose: reader.text(row.purpose, `${path}.purpose`),
    selected: reader.bool(row.selected, `${path}.selected`),
    parse: {
      status: reader.text(parse.status, `${path}.parse.status`),
      parsed_document_id: reader.optionalText(parse.parsed_document_id, `${path}.parse.parsed_document_id`),
      next_action: reader.text(parse.next_action, `${path}.parse.next_action`),
    },
  };
}
function scope(value: unknown, path: string): ReviewReadinessScope {
  const row = reader.record(value, path, ["id", "status", "can_start", "rule_pack", "checks", "blockers"], ["id", "status", "can_start", "checks", "blockers"]);
  const pack = row.rule_pack == null ? null : reader.record(row.rule_pack, `${path}.rule_pack`, ["pack_id", "version", "snapshot_digest"]);
  return {
    id: reader.text(row.id, `${path}.id`),
    status: reader.text(row.status, `${path}.status`),
    can_start: reader.bool(row.can_start, `${path}.can_start`),
    rule_pack: pack === null ? null : {
      pack_id: reader.text(pack.pack_id, `${path}.rule_pack.pack_id`),
      version: reader.text(pack.version, `${path}.rule_pack.version`),
      snapshot_digest: reader.text(pack.snapshot_digest, `${path}.rule_pack.snapshot_digest`),
    },
    checks: reader.array(row.checks, `${path}.checks`, (value, path) => {
      const check = reader.record(value, path, ["rule_id", "status", "missing_dependencies", "next_action"]);
      return { rule_id: reader.text(check.rule_id, `${path}.rule_id`), status: reader.text(check.status, `${path}.status`),
        missing_dependencies: reader.nullableArray(check.missing_dependencies, `${path}.missing_dependencies`, reader.text) ?? [], next_action: reader.text(check.next_action, `${path}.next_action`) };
    }),
    blockers: reader.array(row.blockers, `${path}.blockers`, (value, path) => {
      const blocker = reader.record(value, path, ["code", "status", "message", "missing_dependencies", "next_action"]);
      return { code: reader.text(blocker.code, `${path}.code`), status: reader.text(blocker.status, `${path}.status`),
        message: reader.text(blocker.message, `${path}.message`), missing_dependencies: reader.nullableArray(blocker.missing_dependencies, `${path}.missing_dependencies`, reader.text) ?? [],
        next_action: reader.text(blocker.next_action, `${path}.next_action`) };
    }),
  };
}
export function parseReviewReadiness(value: unknown, caseId: string): ReviewReadiness {
  const row = reader.record(value, "readiness", ["case_id", "revision", "business_at", "can_start", "material_selection_version", "materials", "scopes"], ["case_id", "revision", "can_start", "material_selection_version", "materials", "scopes"]);
  const result: ReviewReadiness = {
    case_id: reader.text(row.case_id, "case_id"), revision: reader.text(row.revision, "revision"),
    business_at: reader.optionalText(row.business_at, "business_at"), can_start: reader.bool(row.can_start, "can_start"),
    material_selection_version: reader.integer(row.material_selection_version, "material_selection_version", 0),
    materials: reader.array(row.materials, "materials", material), scopes: reader.array(row.scopes, "scopes", scope),
  };
  const full = result.scopes.find((item) => item.id === "full_review");
  if (result.case_id !== caseId || !/^[a-f0-9]{64}$/.test(result.revision) || !full || full.can_start !== result.can_start ||
      (result.business_at !== null && !Number.isFinite(Date.parse(result.business_at)))) reader.invalid("readiness");
  return result;
}
export function getReviewReadiness(caseId: string, businessAt = "", signal?: AbortSignal) {
  return requestJson(paths.caseReviewReadiness(caseId, businessAt), { method: "GET", signal }, (value) => parseReviewReadiness(value, caseId));
}
