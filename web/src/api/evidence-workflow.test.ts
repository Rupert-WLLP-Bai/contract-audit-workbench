import { describe, expect, it } from "vitest";
import { parseEvidenceRequest } from "./evidence-workflow";

const baseRequest = {
  id: "request-1",
  case_id: "case-1",
  task_id: "task-1",
  task_version: 2,
  assignee_id: "user-1",
  due_at: "2026-10-10T00:00:00Z",
  source_run_id: "run-1",
  source_attempt_id: "attempt-1",
  source_evaluation: {
    id: "evaluation-1",
    domain: "technical",
    rule_version: "1",
    rule_id: "RULE-1",
    capability_id: "capability-1",
    object_refs: ["LINE-1"],
    relation: "insufficient",
    reason: "缺少带宽证明",
  },
  source_gap: null,
  question: "请补充带宽证明",
  material_role: "procurement",
  verification_condition: "compatible",
  status: "open",
  version: 3,
  created_by: "user-1",
  created_at: "2026-10-05T00:00:00Z",
  updated_at: "2026-10-06T00:00:00Z",
  verification: null,
};

const verification = {
  run_id: "verification-run",
  attempt_id: "verification-attempt",
  receipt_id: "receipt-1",
  evaluation_id: "evaluation-2",
  relation: "compatible",
  gap_id: null,
  gap_status: null,
  evidence: [
    {
      document_id: "document-1",
      parsed_document_id: "parsed-1",
      quote: "带宽 100 Mbit/s",
      start: 0,
      end: 12,
    },
  ],
  reason: "已补齐",
  verified_by: "reviewer",
  verified_at: "2026-10-07T00:00:00Z",
};

describe("parseEvidenceRequest", () => {
  it("reads a request sourced from an evaluation", () => {
    expect(parseEvidenceRequest(baseRequest)).toMatchObject({
      id: "request-1",
      task_version: 2,
      source_evaluation: {
        id: "evaluation-1",
        object_refs: ["LINE-1"],
        rule_id: "RULE-1",
      },
      source_gap: null,
      material_role: "procurement",
      version: 3,
      verification: null,
    });
  });

  it("reads a request sourced from a gap and absent optional references", () => {
    const parsed = parseEvidenceRequest({
      ...baseRequest,
      source_evaluation: null,
      source_gap: {
        gap_id: "gap-1",
        domain: "technical",
        capability_id: "capability-1",
        status: "open",
        reason_code: "missing_bandwidth",
      },
    });
    expect(parsed.source_evaluation).toBeNull();
    expect(parsed.source_gap).toEqual({
      gap_id: "gap-1",
      domain: "technical",
      capability_id: "capability-1",
      status: "open",
      reason_code: "missing_bandwidth",
    });
  });

  it("defaults optional evaluation fields and empty object references", () => {
    const parsed = parseEvidenceRequest({
      ...baseRequest,
      source_evaluation: {
        id: "evaluation-1",
        domain: "technical",
        rule_version: "1",
        relation: "insufficient",
        reason: "缺少",
        object_refs: null,
      },
    });
    expect(parsed.source_evaluation).toMatchObject({
      rule_id: "",
      capability_id: "",
      object_refs: [],
    });
    expect(
      parseEvidenceRequest({
        ...baseRequest,
        source_evaluation: {
          id: "evaluation-1",
          domain: "technical",
          rule_version: "1",
          relation: "insufficient",
          reason: "缺少",
          object_refs: undefined,
        },
        material_role: null,
      }).source_evaluation?.object_refs,
    ).toEqual([]);
  });

  it("reads a verified request and its quote spans", () => {
    const parsed = parseEvidenceRequest({
      ...baseRequest,
      status: "verified",
      verification,
    });
    expect(parsed.verification).toEqual(verification);
    expect(
      parseEvidenceRequest({
        ...baseRequest,
        status: "verified",
        verification: {
          ...verification,
          evaluation_id: undefined,
          relation: undefined,
          gap_id: undefined,
          gap_status: undefined,
        },
      }).verification,
    ).toMatchObject({
      evaluation_id: null,
      relation: null,
      gap_id: null,
      gap_status: null,
    });
  });

  it("requires exactly one source reference", () => {
    expect(() =>
      parseEvidenceRequest({ ...baseRequest, source_evaluation: null }),
    ).toThrow("补充材料响应中的 来源检查 无法识别");
    expect(() =>
      parseEvidenceRequest({
        ...baseRequest,
        source_gap: {
          gap_id: "gap-1",
          domain: "technical",
          capability_id: "capability-1",
          status: "open",
          reason_code: "missing",
        },
      }),
    ).toThrow("补充材料响应中的 来源检查 无法识别");
  });

  it("requires and forbids verification according to the request status", () => {
    expect(() =>
      parseEvidenceRequest({ ...baseRequest, status: "verified" }),
    ).toThrow("补充材料响应中的 verification 无法识别");
    for (const status of ["open", "received"]) {
      expect(() =>
        parseEvidenceRequest({ ...baseRequest, status, verification }),
      ).toThrow("补充材料响应中的 verification 无法识别");
    }
    expect(
      parseEvidenceRequest({ ...baseRequest, status: "cancelled", verification })
        .status,
    ).toBe("cancelled");
  });

  it.each([
    ["request", null, "request"],
    ["source evaluation", { ...baseRequest, source_evaluation: 7 }, "source_evaluation"],
    ["source gap", { ...baseRequest, source_evaluation: null, source_gap: 7 }, "source_gap"],
    ["id", { ...baseRequest, id: 1 }, "id"],
    ["task version", { ...baseRequest, task_version: 0 }, "task_version"],
    ["task version fraction", { ...baseRequest, task_version: 1.5 }, "task_version"],
    ["version", { ...baseRequest, version: 0 }, "version"],
    ["status", { ...baseRequest, status: null }, "status"],
    ["evaluation id", { ...baseRequest, source_evaluation: { ...baseRequest.source_evaluation, id: 1 } }, "source_evaluation.id"],
    ["evaluation object refs", { ...baseRequest, source_evaluation: { ...baseRequest.source_evaluation, object_refs: [1] } }, "object_ref"],
    ["gap id", { ...baseRequest, source_evaluation: null, source_gap: { gap_id: 1 } }, "source_gap.gap_id"],
  ])("rejects a malformed %s", (_label, value, field) => {
    expect(() => parseEvidenceRequest(value)).toThrow(
      `补充材料响应中的 ${field} 无法识别`,
    );
  });

  it("rejects malformed verification fields and quote spans", () => {
    expect(() =>
      parseEvidenceRequest({
        ...baseRequest,
        status: "verified",
        verification: { ...verification, evidence: [null] },
      }),
    ).toThrow("补充材料响应中的 evidence 无法识别");
    expect(() =>
      parseEvidenceRequest({
        ...baseRequest,
        status: "verified",
        verification: {
          ...verification,
          evidence: [{ ...verification.evidence[0], start: -1 }],
        },
      }),
    ).toThrow("补充材料响应中的 start 无法识别");
    expect(() =>
      parseEvidenceRequest({
        ...baseRequest,
        status: "verified",
        verification: {
          ...verification,
          evidence: [{ ...verification.evidence[0], end: 0 }],
        },
      }),
    ).toThrow("补充材料响应中的 end 无法识别");
    expect(() =>
      parseEvidenceRequest({
        ...baseRequest,
        status: "verified",
        verification: { ...verification, run_id: null },
      }),
    ).toThrow("补充材料响应中的 verification.run_id 无法识别");
  });
});
