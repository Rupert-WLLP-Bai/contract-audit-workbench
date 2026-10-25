import type { CaseSummary, Finding } from "../api/types";
import {
  filterCases,
  filterFindings,
  parseRunEvent,
  parseRunFilter,
  reviewLayout,
  selectedFindingId,
  validateCaseTitle,
  validateDecisionReason,
} from "./review";

function finding(id: string, relation: string, decided: boolean): Finding {
  return {
    id,
    origin: "rule",
    domain: "质保",
    claim: id,
    relation,
    confidence: null,
    severity: null,
    severity_source: null,
    verification_status: "unknown",
    evidence: [],
    unverified_reason: null,
    version: 1,
    decisions: decided
      ? [
          {
            id: `${id}-d`,
            action: "confirm_risk",
            reason: "已核对",
            actor: "甲",
            decided_at: "2026-10-21T01:00:00Z",
          },
        ]
      : [],
  };
}

function item(
  id: string,
  title: string,
  state: string | null,
  pending: number,
): CaseSummary {
  return {
    id,
    title,
    contract_type: "procurement_ict",
    document_count: 2,
    pending_decisions: pending,
    latest_run: state
      ? { id: `${id}-run`, state, updated_at: "2026-10-21T01:00:00Z" }
      : null,
    updated_at: "2026-10-21T01:00:00Z",
  };
}

describe("case filters", () => {
  const items = [
    item("c1", "基站质保", "awaiting_human", 1),
    item("c2", "传输合同", "running", 0),
    item("c3", "已完成采购", "completed", 0),
    item("c4", "失败样本", "failed", 0),
  ];

  it("matches title text and keeps an unknown filter on all", () => {
    expect(filterCases(items, "质保", "all").map((entry) => entry.id)).toEqual([
      "c1",
    ]);
    expect(parseRunFilter("nope")).toBe("all");
  });

  it("treats pending decisions as needing a person even if the run state lags", () => {
    const lagged = item("c5", "滞后", "running", 2);
    expect(filterCases([lagged], "", "needs_human")).toHaveLength(1);
    expect(filterCases(items, "", "active").map((entry) => entry.id)).toEqual([
      "c2",
    ]);
    expect(filterCases(items, "", "failed").map((entry) => entry.id)).toEqual([
      "c4",
    ]);
  });
});

describe("findings and decisions", () => {
  const findings = [
    finding("f1", "conflict", false),
    finding("f2", "insufficient", true),
  ];

  it("filters undecided items and prefers them when the url has no selection", () => {
    expect(
      filterFindings(findings, "undecided").map((entry) => entry.id),
    ).toEqual(["f1"]);
    expect(selectedFindingId(findings, null)).toBe("f1");
    expect(selectedFindingId(findings, "f2")).toBe("f2");
  });

  it("requires a reason for every decision and a usable title", () => {
    expect(validateDecisionReason("  ")).toBe("请填写理由");
    expect(validateDecisionReason("好")).toBe("理由至少两个字");
    expect(validateDecisionReason("条款不一致")).toBeNull();
    expect(validateCaseTitle("")).toBe("请填写案件标题");
  });
});

describe("layout and events", () => {
  it("splits evidence only from the 1440 breakpoint up", () => {
    expect(reviewLayout(1439)).toBe("switch");
    expect(reviewLayout(1440)).toBe("split");
  });

  it("drops events that have no sequence", () => {
    expect(parseRunEvent("not-json")).toBeNull();
    expect(parseRunEvent(JSON.stringify({ type: "run_state" }))).toBeNull();
    expect(
      parseRunEvent(
        JSON.stringify({ seq: 4, type: "run_state", state: "running" }),
      ),
    ).toEqual({
      seq: 4,
      type: "run_state",
      state: "running",
    });
  });
});
