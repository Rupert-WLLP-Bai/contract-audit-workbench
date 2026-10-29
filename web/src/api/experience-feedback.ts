import { jsonInit, requestJson } from "./http";
import { responseReader } from "./respond";

export interface ExperienceEvidenceRef {
  document_id: string;
  parsed_document_id: string;
  quote: string;
  start: number;
  end: number;
}

export interface ExperienceFeedbackInput {
  run_id: string;
  attempt_id: string;
  finding_id: string;
  decision_id: string;
  reason: string;
  evidence_refs: ExperienceEvidenceRef[];
}

export interface ExperienceFeedback extends ExperienceFeedbackInput {
  feedback_id: string;
  case_id: string;
  decision_action: string;
  decision_disposition: string;
  applicant_id: string;
  created_at: string;
}

export interface ExperienceDistillationStatus {
  job_id: string;
  feedback_id: string;
  state: string;
  failure_code: string | null;
  candidate_id: string | null;
  version_id: string | null;
}

export interface ExperiencePolicyRef {
  knowledge_id: string;
  version_id: string;
  content_sha256: string;
}

export interface ExperienceCandidate {
  case_id: string;
  feedback_id: string;
  candidate_id: string;
  version_id: string;
  content_sha256: string;
  job_id: string;
  created_at: string;
  content: {
    schema_version: string;
    applicability: string[] | null;
    exclusions: string[] | null;
    check_action: string;
    minimum_evidence: string[] | null;
    counterexamples: string[] | null;
    valid_from: string;
    valid_until: string;
    policy_refs: ExperiencePolicyRef[] | null;
    review_diff: string;
  };
}

const { invalid, object, text, array, integer: safeInteger } = responseReader(
  (field) => `经验反馈响应中的 ${field} 无法识别`,
);
const integer = (value: unknown, field: string): number =>
  safeInteger(value, field, 0);

function identity(value: unknown, field: string): string {
  const result = text(value, field);
  return result !== "" && result.trim() === result ? result : invalid(field);
}

function optionalIdentity(value: unknown, field: string): string | null {
  return value === undefined ? null : identity(value, field);
}

function stringArray(value: unknown, field: string): string[] | null {
  return value === null ? null : array(value, field, text);
}

function evidence(value: unknown, field: string): ExperienceEvidenceRef {
  const item = object(value, field);
  const start = integer(item.start, `${field}.start`);
  const end = integer(item.end, `${field}.end`);
  const quote = text(item.quote, `${field}.quote`);
  if (end <= start || quote === "" || Array.from(quote).length !== end - start) return invalid(field);
  return {
    document_id: identity(item.document_id, `${field}.document_id`),
    parsed_document_id: identity(item.parsed_document_id, `${field}.parsed_document_id`),
    quote,
    start,
    end,
  };
}

function parseFeedback(value: unknown): ExperienceFeedback {
  const item = object(value, "feedback");
  if (!Array.isArray(item.evidence_refs) || item.evidence_refs.length === 0) return invalid("evidence_refs");
  return {
    feedback_id: identity(item.feedback_id, "feedback_id"),
    case_id: identity(item.case_id, "case_id"),
    run_id: identity(item.run_id, "run_id"),
    attempt_id: identity(item.attempt_id, "attempt_id"),
    finding_id: identity(item.finding_id, "finding_id"),
    decision_id: identity(item.decision_id, "decision_id"),
    decision_action: text(item.decision_action, "decision_action"),
    decision_disposition: text(item.decision_disposition, "decision_disposition"),
    reason: text(item.reason, "reason"),
    evidence_refs: item.evidence_refs.map((entry, index) => evidence(entry, `evidence_refs[${index}]`)),
    applicant_id: identity(item.applicant_id, "applicant_id"),
    created_at: text(item.created_at, "created_at"),
  };
}

function parseReceipt(value: unknown): { job_id: string } {
  const item = object(value, "receipt");
  return { job_id: identity(item.job_id, "job_id") };
}

function parseStatus(value: unknown): ExperienceDistillationStatus {
  const item = object(value, "status");
  const candidateId = optionalIdentity(item.candidate_id, "candidate_id");
  const versionId = optionalIdentity(item.version_id, "version_id");
  if ((candidateId === null) !== (versionId === null)) return invalid("candidate_id/version_id");
  return {
    job_id: identity(item.job_id, "job_id"),
    feedback_id: identity(item.feedback_id, "feedback_id"),
    state: identity(item.state, "state"),
    failure_code: optionalIdentity(item.failure_code, "failure_code"),
    candidate_id: candidateId,
    version_id: versionId,
  };
}

function parseCandidate(value: unknown): ExperienceCandidate {
  const item = object(value, "candidate");
  const content = object(item.content, "content");
  const policyRefs = content.policy_refs;
  if (policyRefs !== null && !Array.isArray(policyRefs)) return invalid("content.policy_refs");
  return {
    case_id: identity(item.case_id, "case_id"),
    feedback_id: identity(item.feedback_id, "feedback_id"),
    candidate_id: identity(item.candidate_id, "candidate_id"),
    version_id: identity(item.version_id, "version_id"),
    content_sha256: identity(item.content_sha256, "content_sha256"),
    job_id: identity(item.job_id, "job_id"),
    created_at: text(item.created_at, "created_at"),
    content: {
      schema_version: text(content.schema_version, "content.schema_version"),
      applicability: stringArray(content.applicability, "content.applicability"),
      exclusions: stringArray(content.exclusions, "content.exclusions"),
      check_action: text(content.check_action, "content.check_action"),
      minimum_evidence: stringArray(content.minimum_evidence, "content.minimum_evidence"),
      counterexamples: stringArray(content.counterexamples, "content.counterexamples"),
      valid_from: text(content.valid_from, "content.valid_from"),
      valid_until: text(content.valid_until, "content.valid_until"),
      policy_refs: policyRefs === null ? null : policyRefs.map((value, index) => {
        const ref = object(value, `content.policy_refs[${index}]`);
        return {
          knowledge_id: identity(ref.knowledge_id, `content.policy_refs[${index}].knowledge_id`),
          version_id: identity(ref.version_id, `content.policy_refs[${index}].version_id`),
          content_sha256: text(ref.content_sha256, `content.policy_refs[${index}].content_sha256`),
        };
      }),
      review_diff: text(content.review_diff, "content.review_diff"),
    },
  };
}

function feedbackPath(caseId: string, feedbackId?: string): string {
  const base = `/api/cases/${encodeURIComponent(caseId)}/experience-feedback`;
  return feedbackId === undefined ? base : `${base}/${encodeURIComponent(feedbackId)}`;
}

function checkFeedbackSource(item: ExperienceFeedback, caseId: string, input: ExperienceFeedbackInput): ExperienceFeedback {
  if (item.case_id !== caseId || item.run_id !== input.run_id || item.attempt_id !== input.attempt_id ||
      item.finding_id !== input.finding_id || item.decision_id !== input.decision_id) return invalid("来源身份");
  if (item.reason !== input.reason.trim() || item.evidence_refs.length !== input.evidence_refs.length ||
      item.evidence_refs.some((ref, index) => {
        const expected = input.evidence_refs[index];
        return !expected || ref.document_id !== expected.document_id || ref.parsed_document_id !== expected.parsed_document_id ||
          ref.quote !== expected.quote || ref.start !== expected.start || ref.end !== expected.end;
      })) return invalid("反馈内容");
  return item;
}

export function submitExperienceFeedback(caseId: string, input: ExperienceFeedbackInput, idempotencyKey: string) {
  return requestJson(feedbackPath(caseId), jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    (value) => checkFeedbackSource(parseFeedback(value), caseId, input));
}

export function getExperienceFeedback(caseId: string, feedbackId: string, signal?: AbortSignal) {
  return requestJson(feedbackPath(caseId, feedbackId), { method: "GET", signal }, (value) => {
    const item = parseFeedback(value);
    return item.case_id === caseId && item.feedback_id === feedbackId ? item : invalid("反馈身份");
  });
}

export function requestExperienceDistillation(caseId: string, feedbackId: string, idempotencyKey: string) {
  return requestJson(`${feedbackPath(caseId, feedbackId)}/distillations`,
    jsonInit("POST", {}, { "Idempotency-Key": idempotencyKey }), parseReceipt);
}

export function getExperienceDistillation(caseId: string, feedbackId: string, jobId: string, signal?: AbortSignal) {
  return requestJson(`${feedbackPath(caseId, feedbackId)}/distillations/${encodeURIComponent(jobId)}`,
    { method: "GET", signal }, (value) => {
      const item = parseStatus(value);
      return item.feedback_id === feedbackId && item.job_id === jobId ? item : invalid("任务身份");
    });
}

export function getExperienceCandidate(caseId: string, feedbackId: string, jobId: string, candidateId: string, versionId: string, signal?: AbortSignal) {
  const path = `/api/cases/${encodeURIComponent(caseId)}/experience-candidates/${encodeURIComponent(candidateId)}/versions/${encodeURIComponent(versionId)}`;
  return requestJson(path, { method: "GET", signal }, (value) => {
    const item = parseCandidate(value);
    return item.case_id === caseId && item.feedback_id === feedbackId && item.job_id === jobId &&
      item.candidate_id === candidateId && item.version_id === versionId ? item : invalid("候选身份");
  });
}
