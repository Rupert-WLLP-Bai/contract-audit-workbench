import { describe, expect, it } from "vitest";
import { caseStagePresentation, type CaseStageFacts } from "./review";
import { compactTime } from "./time";

const base: CaseStageFacts = {
  archived: false,
  documentCount: 1,
  parsing: "ready",
  runKnown: true,
  runState: "partial",
  pendingDecisions: 0,
  pendingRemediation: false,
  reportAvailable: false,
  canUpload: true,
  canStartReview: true,
  startEligible: true,
  preparationAvailable: true,
  retryEligible: false,
  canRetry: false,
  canReview: true,
  canRemediate: true,
  canCreateReport: true,
  reportEligible: true,
  canReadReport: true,
};
describe("case stage presentation", () => {
  it("separates unknown, pending and incomplete work from permission", () => {
    expect(caseStagePresentation({ ...base, pendingDecisions: null })).toMatchObject({
      label: "判断状态待确认",
      action: "view",
      aiIncomplete: true,
    });
    expect(caseStagePresentation({ ...base, pendingDecisions: 2 })).toMatchObject({
      label: "待判断 2 条",
      action: "judge",
      aiIncomplete: true,
    });
    expect(caseStagePresentation({ ...base, pendingDecisions: 2, canReview: false }).action).toBe(
      "view",
    );
    expect(caseStagePresentation(base)).toMatchObject({
      action: "prepare",
      actionLabel: "重新审查",
    });
    expect(
      caseStagePresentation({
        ...base,
        retryEligible: true,
        canRetry: false,
        preparationAvailable: false,
      }),
    ).toMatchObject({ action: "view", actionLabel: "查看原因" });
    expect(caseStagePresentation({ ...base, retryEligible: true, canRetry: true }).action).toBe(
      "retry",
    );
    expect(caseStagePresentation({ ...base, runKnown: false, runState: null }).label).toBe(
      "审查状态待确认",
    );
    expect(
      caseStagePresentation({ ...base, runState: "unrecognized", pendingDecisions: 2 }).action,
    ).toBe("view");
    expect(caseStagePresentation({ ...base, runState: "awaiting_human" }).action).toBe("view");
    expect(
      caseStagePresentation({ ...base, runState: "completed", reportEligible: false }).action,
    ).toBe("view");
    expect(caseStagePresentation({ ...base, archived: null }).action).toBe("view");
  });
  it("does not invent missing material, remediation, or report facts", () => {
    expect(caseStagePresentation({ ...base, documentCount: null }).label).toBe("材料状态待确认");
    expect(
      caseStagePresentation({ ...base, runState: "completed", pendingRemediation: null }).action,
    ).toBe("view");
    expect(
      caseStagePresentation({ ...base, runState: "completed", reportAvailable: null }).action,
    ).toBe("view");
    expect(caseStagePresentation({ ...base, runState: "completed" }).action).toBe("report");
    expect(caseStagePresentation({ ...base, archived: true, reportAvailable: false }).action).toBe(
      "view",
    );
  });
  it("formats only recorded valid instants in Beijing time", () => {
    const now = new Date("2026-11-20T00:00:00Z");
    expect(compactTime("2026-11-19T16:15:00Z", now)).toBe("11-20 00:15");
    expect(compactTime("2025-01-02T00:00:00Z", now)).toBe("2025-01-02");
    expect(compactTime("", now)).toBe("—");
    expect(compactTime("bad", now)).toBe("—");
  });
});
