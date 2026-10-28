import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./http";
import {
  getExperienceCandidate,
  getExperienceDistillation,
  requestExperienceDistillation,
  submitExperienceFeedback,
  type ExperienceFeedbackInput,
} from "./experience-feedback";

const input: ExperienceFeedbackInput = {
  run_id: "run-1",
  attempt_id: "attempt-1",
  finding_id: "finding-1",
  decision_id: "decision-1",
  reason: "复核期限",
  evidence_refs: [{ document_id: "doc-1", parsed_document_id: "parse-1", quote: "😀证据", start: 1, end: 4 }],
};
const feedback = {
  feedback_id: "feedback-1", tenant_id: "tenant", org_id: "org", case_id: "case/1",
  ...input, decision_action: "confirm_risk", decision_disposition: "confirmed",
  applicant_id: "reviewer", created_at: "2026-10-27T00:00:00Z",
};
const candidate = {
  case_id: "case/1", feedback_id: "feedback-1", job_id: "job-1", candidate_id: "candidate-1",
  version_id: "version-1", content_sha256: "digest", created_at: "2026-10-27T00:00:00Z",
  content: { schema_version: "experience/v1", applicability: ["专线"], exclusions: null,
    check_action: "核对期限", minimum_evidence: [], counterexamples: null,
    valid_from: "", valid_until: "", policy_refs: null, review_diff: "" },
};

afterEach(() => vi.unstubAllGlobals());

describe("experience feedback API", () => {
  it("sends exact source identities and code-point evidence with an idempotency key", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => Response.json(feedback, { status: 201 }));
    vi.stubGlobal("fetch", fetchMock);
    const result = await submitExperienceFeedback("case/1", input, "feedback-key");
    expect(result.feedback_id).toBe("feedback-1");
    const [path, init] = fetchMock.mock.calls[0]!;
    expect(path).toBe("/api/cases/case%2F1/experience-feedback");
    expect(JSON.parse(String(init?.body))).toEqual(input);
    expect(new Headers(init?.headers).get("Idempotency-Key")).toBe("feedback-key");
    expect(init?.credentials).toBe("include");
  });

  it("does not accept a response for another decision, an invalid span, or different content", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(Response.json({ ...feedback, decision_id: "different" }))
      .mockResolvedValueOnce(Response.json({ ...feedback, evidence_refs: [{ ...input.evidence_refs[0], end: 3 }] }))
      .mockResolvedValueOnce(Response.json({ ...feedback, reason: "不同理由" })));
    await expect(submitExperienceFeedback("case/1", input, "one")).rejects.toMatchObject({ code: "invalid_response" });
    await expect(submitExperienceFeedback("case/1", input, "two")).rejects.toMatchObject({ code: "invalid_response" });
    await expect(submitExperienceFeedback("case/1", input, "three")).rejects.toMatchObject({ code: "invalid_response" });
  });

  it("preserves server errors and rejects mismatched job or candidate identities", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(Response.json({ error: { message: "冻结制度缺失", code: "policy_missing", details: { policy: "required" } } }, { status: 409 }))
      .mockResolvedValueOnce(Response.json({ job_id: "other", feedback_id: "feedback-1", state: "completed", candidate_id: "candidate-1", version_id: "version-1" }))
      .mockResolvedValueOnce(Response.json({ ...candidate, feedback_id: "other" })));
    await expect(requestExperienceDistillation("case/1", "feedback-1", "key")).rejects.toMatchObject({
      status: 409, code: "policy_missing", message: "冻结制度缺失", details: { policy: "required" },
    } satisfies Partial<ApiError>);
    await expect(getExperienceDistillation("case/1", "feedback-1", "job-1")).rejects.toMatchObject({ code: "invalid_response" });
    await expect(getExperienceCandidate("case/1", "feedback-1", "job-1", "candidate-1", "version-1")).rejects.toMatchObject({ code: "invalid_response" });
  });

  it("keeps absent optional job data and nullable candidate sections distinct", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(Response.json({ job_id: "job-1", feedback_id: "feedback-1", state: "queued" }))
      .mockResolvedValueOnce(Response.json(candidate)));
    expect(await getExperienceDistillation("case/1", "feedback-1", "job-1")).toEqual({
      job_id: "job-1", feedback_id: "feedback-1", state: "queued", failure_code: null, candidate_id: null, version_id: null,
    });
    expect((await getExperienceCandidate("case/1", "feedback-1", "job-1", "candidate-1", "version-1")).content.exclusions).toBeNull();
  });
});
