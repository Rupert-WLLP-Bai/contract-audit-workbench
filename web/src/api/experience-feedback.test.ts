import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./http";
import {
  getExperienceCandidate,
  getExperienceDistillation,
  getExperienceFeedback,
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

  it("accepts a trimmed reason and rejects every source identity drift", async () => {
    const spaced = { ...input, reason: "  复核期限  " };
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(Response.json({ ...feedback, reason: "复核期限" }, { status: 201 }))
      .mockResolvedValueOnce(Response.json({ ...feedback, case_id: "other" }))
      .mockResolvedValueOnce(Response.json({ ...feedback, run_id: "other" }))
      .mockResolvedValueOnce(Response.json({ ...feedback, attempt_id: "other" }))
      .mockResolvedValueOnce(Response.json({ ...feedback, finding_id: "other" }))
      .mockResolvedValueOnce(Response.json({ ...feedback, evidence_refs: [] }))
      .mockResolvedValueOnce(Response.json({ ...feedback, evidence_refs: [{ ...input.evidence_refs[0], document_id: "other" }] }))
      .mockResolvedValueOnce(Response.json({ ...feedback, evidence_refs: [{ ...input.evidence_refs[0], parsed_document_id: "other" }] }))
      .mockResolvedValueOnce(Response.json({ ...feedback, evidence_refs: [{ ...input.evidence_refs[0], quote: "其他" }] })));
    await expect(submitExperienceFeedback("case/1", spaced, "trimmed")).resolves.toMatchObject({ reason: "复核期限" });
    for (const key of ["one", "two", "three", "four", "five", "six", "seven", "eight"]) {
      await expect(submitExperienceFeedback("case/1", input, key)).rejects.toMatchObject({ code: "invalid_response" });
    }
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

  it("accepts zero-based evidence spans", async () => {
    const zeroStart: ExperienceFeedbackInput = {
      ...input,
      evidence_refs: [
        { ...input.evidence_refs[0], start: 0, end: 3, quote: "😀证据" },
      ],
    };
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        Response.json({ ...feedback, evidence_refs: zeroStart.evidence_refs }, { status: 201 }),
      ),
    );
    await expect(
      submitExperienceFeedback("case/1", zeroStart, "zero"),
    ).resolves.toMatchObject({ evidence_refs: zeroStart.evidence_refs });
  });

  it("rejects evidence whose quote length does not match its span", async () => {
    const malformed: ExperienceFeedbackInput = {
      ...input,
      evidence_refs: [
        { ...input.evidence_refs[0], start: 0, end: 3, quote: "ab" },
      ],
    };
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        Response.json({ ...feedback, evidence_refs: malformed.evidence_refs }, { status: 201 }),
      ),
    );
    await expect(
      submitExperienceFeedback("case/1", malformed, "bad"),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });

  it("rejects a response without evidence and padded identities", async () => {
    const withoutEvidence = { ...feedback } as Record<string, unknown>;
    delete withoutEvidence.evidence_refs;
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(Response.json(withoutEvidence, { status: 201 }))
      .mockResolvedValueOnce(Response.json({ ...feedback, feedback_id: "" }, { status: 201 }))
      .mockResolvedValueOnce(Response.json({ ...feedback, feedback_id: " feedback-1" }, { status: 201 })));
    await expect(
      submitExperienceFeedback("case/1", input, "no-evidence"),
    ).rejects.toMatchObject({
      code: "invalid_response",
      message: "经验反馈响应中的 evidence_refs 无法识别",
    });
    for (const key of ["empty-id", "padded-id"]) {
      await expect(
        submitExperienceFeedback("case/1", input, key),
      ).rejects.toMatchObject({
        code: "invalid_response",
        message: "经验反馈响应中的 feedback_id 无法识别",
      });
    }
  });

  it("rejects feedback returned for another case", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(Response.json({ ...feedback, case_id: "case/2" }))
      .mockResolvedValueOnce(Response.json(feedback)));
    await expect(
      getExperienceFeedback("case/1", "feedback-1"),
    ).rejects.toMatchObject({ code: "invalid_response" });
    await expect(
      getExperienceFeedback("case/1", "feedback-1"),
    ).resolves.toMatchObject({ feedback_id: "feedback-1" });
  });

  it("keeps candidate policy references", async () => {
    const policyRefs = [
      {
        knowledge_id: "knowledge-1",
        version_id: "version-1",
        content_sha256: "sha256",
      },
    ];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        Response.json({
          ...candidate,
          content: { ...candidate.content, policy_refs: policyRefs },
        }),
      ),
    );
    const parsed = await getExperienceCandidate(
      "case/1",
      "feedback-1",
      "job-1",
      "candidate-1",
      "version-1",
    );
    expect(parsed.content.policy_refs).toEqual(policyRefs);
  });
});
