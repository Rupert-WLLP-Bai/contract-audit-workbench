import { jsonInit, requestJson } from "./http";
import { enumValue, responseReader } from "./respond";
import type { KnowledgeRef } from "./governed-knowledge";

export type ExperienceDirection = "" | "confirm_risk" | "false_positive";
export interface ExperienceUsageIdentity {
  caseId: string;
  runId: string;
  attemptId: string;
  findingId: string;
}
export interface ExperienceUsage {
  case_id: string;
  run_id: string;
  attempt_id: string;
  finding_id: string;
  ref: KnowledgeRef;
  snapshot_sha256: string;
  input_sha256: string;
  expected_action: ExperienceDirection;
  recorded_by: string;
  usage_sha256: string;
  recorded_at: string;
}
export interface ExperienceUsageOption {
  ref: KnowledgeRef;
  title: string;
  body: string;
  summary: string;
  check_action: string;
  minimum_evidence: string;
  reviewed_applicability: string[] | null;
  reviewed_exclusions: string[] | null;
  counterexamples: string[] | null;
  usage: ExperienceUsage | null;
}
export interface ExperienceUsageOptions { items: ExperienceUsageOption[]; can_record: boolean }

const { invalid, object, text, bool, array, nullableArray } = responseReader((path) => `经验使用响应中的 ${path} 无法识别`);
function digest(value: unknown, path: string): string {
  const result = text(value, path);
  return /^[0-9a-f]{64}$/.test(result) ? result : invalid(path);
}
function reference(value: unknown, path: string): KnowledgeRef {
  const item = object(value, path);
  const knowledge_id = text(item.knowledge_id, `${path}.knowledge_id`);
  if (!knowledge_id) return invalid(`${path}.knowledge_id`);
  return { knowledge_id, version_id: digest(item.version_id, `${path}.version_id`), content_sha256: digest(item.content_sha256, `${path}.content_sha256`) };
}
function direction(value: unknown, path: string): ExperienceDirection {
  return enumValue<ExperienceDirection>(value, ["", "confirm_risk", "false_positive"]) ?? invalid(path);
}
function usage(value: unknown, path: string): ExperienceUsage {
  const item = object(value, path);
  return {
    case_id: text(item.case_id, `${path}.case_id`), run_id: text(item.run_id, `${path}.run_id`),
    attempt_id: text(item.attempt_id, `${path}.attempt_id`), finding_id: text(item.finding_id, `${path}.finding_id`),
    ref: reference(item.ref, `${path}.ref`), snapshot_sha256: digest(item.snapshot_sha256, `${path}.snapshot_sha256`),
    input_sha256: digest(item.input_sha256, `${path}.input_sha256`), expected_action: direction(item.expected_action, `${path}.expected_action`),
    recorded_by: text(item.recorded_by, `${path}.recorded_by`), usage_sha256: digest(item.usage_sha256, `${path}.usage_sha256`),
    recorded_at: text(item.recorded_at, `${path}.recorded_at`),
  };
}
function sameRef(a: KnowledgeRef, b: KnowledgeRef): boolean {
  return a.knowledge_id === b.knowledge_id && a.version_id === b.version_id && a.content_sha256 === b.content_sha256;
}
function sameIdentity(item: ExperienceUsage, identity: ExperienceUsageIdentity): boolean {
  return item.case_id === identity.caseId && item.run_id === identity.runId && item.attempt_id === identity.attemptId && item.finding_id === identity.findingId;
}
function option(value: unknown, path: string): ExperienceUsageOption {
  const item = object(value, path);
  const ref = reference(item.ref, `${path}.ref`);
  const recorded = item.usage === null ? null : usage(item.usage, `${path}.usage`);
  if (recorded && !sameRef(recorded.ref, ref)) return invalid(`${path}.usage.ref`);
  return {
    ref, title: text(item.title, `${path}.title`), body: text(item.body, `${path}.body`),
    summary: text(item.summary, `${path}.summary`), check_action: text(item.check_action, `${path}.check_action`),
    minimum_evidence: text(item.minimum_evidence, `${path}.minimum_evidence`),
    reviewed_applicability: nullableArray(item.reviewed_applicability, `${path}.reviewed_applicability`, text),
    reviewed_exclusions: nullableArray(item.reviewed_exclusions, `${path}.reviewed_exclusions`, text),
    counterexamples: nullableArray(item.counterexamples, `${path}.counterexamples`, text), usage: recorded,
  };
}
export function listExperienceUsage(identity: ExperienceUsageIdentity, signal?: AbortSignal): Promise<ExperienceUsageOptions> {
  const query = new URLSearchParams({ run_id: identity.runId, attempt_id: identity.attemptId, finding_id: identity.findingId });
  return requestJson(`/api/cases/${encodeURIComponent(identity.caseId)}/experience-usage?${query}`, { method: "GET", signal }, (value) => {
    const response = object(value, "options");
    const items = array(response.items, "options.items", option);
    if (items.some((item) => item.usage && !sameIdentity(item.usage, identity))) return invalid("options.usage.identity");
    return { items, can_record: bool(response.can_record, "options.can_record") };
  });
}
export function recordExperienceUsage(identity: ExperienceUsageIdentity, ref: KnowledgeRef, expectedAction: ExperienceDirection): Promise<ExperienceUsage> {
  return requestJson(`/api/cases/${encodeURIComponent(identity.caseId)}/experience-usage`, jsonInit("POST", {
    run_id: identity.runId, attempt_id: identity.attemptId, finding_id: identity.findingId, ref, expected_action: expectedAction,
  }), (value) => {
    const result = usage(value, "usage");
    if (!sameIdentity(result, identity) || !sameRef(result.ref, ref) || result.expected_action !== expectedAction) return invalid("usage.identity");
    return result;
  });
}
