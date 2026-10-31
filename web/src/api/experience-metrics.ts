import { requestJson } from "./http";
import { enumValue, responseReader } from "./respond";
import type { KnowledgeRef } from "./governed-knowledge";
import type { ExperienceDirection } from "./experience-usage";

export type ExperienceOutcome = "pending" | "adopted" | "overturned" | "other";
export interface ExperienceMetricsSample {
  case_id: string;
  run_id: string;
  attempt_id: string;
  finding_id: string;
  input_sha256: string;
  snapshot_sha256: string;
  usage_sha256: string;
  expected_action: ExperienceDirection;
  decision_id: string;
  decision_action: string;
  decision_disposition: string;
  outcome: ExperienceOutcome;
  used_at: string;
}
export interface ExperienceMetrics {
  ref: KnowledgeRef;
  has_been_published: boolean;
  usage_count: number;
  pending_count: number;
  adopted_count: number;
  overturned_count: number;
  other_count: number;
  samples: ExperienceMetricsSample[];
}
const { invalid, object, text, bool, integer, array } = responseReader((path) => `经验指标响应中的 ${path} 无法识别`);
function digest(value: unknown, path: string): string {
  const result = text(value, path);
  return /^[0-9a-f]{64}$/.test(result) ? result : invalid(path);
}
function sample(value: unknown, path: string): ExperienceMetricsSample {
  const item = object(value, path);
  return {
    case_id: text(item.case_id, `${path}.case_id`), run_id: text(item.run_id, `${path}.run_id`),
    attempt_id: text(item.attempt_id, `${path}.attempt_id`), finding_id: text(item.finding_id, `${path}.finding_id`),
    input_sha256: digest(item.input_sha256, `${path}.input_sha256`), snapshot_sha256: digest(item.snapshot_sha256, `${path}.snapshot_sha256`),
    usage_sha256: digest(item.usage_sha256, `${path}.usage_sha256`),
    expected_action: enumValue<ExperienceDirection>(item.expected_action, ["", "confirm_risk", "false_positive"]) ?? invalid(`${path}.expected_action`),
    decision_id: text(item.decision_id, `${path}.decision_id`), decision_action: text(item.decision_action, `${path}.decision_action`),
    decision_disposition: text(item.decision_disposition, `${path}.decision_disposition`),
    outcome: enumValue<ExperienceOutcome>(item.outcome, ["pending", "adopted", "overturned", "other"]) ?? invalid(`${path}.outcome`),
    used_at: text(item.used_at, `${path}.used_at`),
  };
}
export function getExperienceMetrics(ref: KnowledgeRef, signal?: AbortSignal): Promise<ExperienceMetrics> {
  const path = `/api/admin/governed-knowledge/${encodeURIComponent(ref.knowledge_id)}/versions/${encodeURIComponent(ref.version_id)}/experience-metrics`;
  return requestJson(path, { method: "GET", signal }, (value) => {
    const item = object(value, "metrics");
    const identity = object(item.ref, "metrics.ref");
    const result: ExperienceMetrics = {
      ref: { knowledge_id: text(identity.knowledge_id, "metrics.ref.knowledge_id"), version_id: digest(identity.version_id, "metrics.ref.version_id"),
        content_sha256: digest(identity.content_sha256, "metrics.ref.content_sha256") },
      has_been_published: bool(item.has_been_published, "metrics.has_been_published"),
      usage_count: integer(item.usage_count, "metrics.usage_count", 0), pending_count: integer(item.pending_count, "metrics.pending_count", 0),
      adopted_count: integer(item.adopted_count, "metrics.adopted_count", 0), overturned_count: integer(item.overturned_count, "metrics.overturned_count", 0),
      other_count: integer(item.other_count, "metrics.other_count", 0), samples: array(item.samples, "metrics.samples", sample),
    };
    if (result.ref.knowledge_id !== ref.knowledge_id || result.ref.version_id !== ref.version_id || result.ref.content_sha256 !== ref.content_sha256 ||
        result.usage_count !== result.pending_count + result.adopted_count + result.overturned_count + result.other_count ||
        result.samples.length > Math.min(20, result.usage_count)) return invalid("metrics.identity_or_totals");
    return result;
  });
}
