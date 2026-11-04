// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “case filters, findings and decisions”; representative assertions cover matches title text and keeps an unknown filter on all, treats pending decisions as needing a person even if the run state lags, filters undecided items and prefers them when the url has no selection. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import type { CaseSummary, Finding } from "../api/types";
import {
  filterCases,
  filterFindings,
  formatDateTime,
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

  it("searches business numbers, Chinese scenarios and the full case identity before pagination", () => {
    const named = { ...items[0]!, case_number: "BUS-2026-01", business_line: "ENT", scenario: "telecom_dedicated_line" };
    const same = { ...items[1]!, title: named.title, id: "case-full-other", scenario: "cloud_services" };
    for (const term of ["BUS-2026-01", "政企专线", named.id, named.title]) {
      expect(filterCases([named, same], term, "all")).toContainEqual(named);
    }
    expect(filterCases([named, same], "云服务采购", "all")).toEqual([same]);
    expect(filterCases([named, same], "case-full-other", "all")).toEqual([same]);
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
  it("splits evidence only from the 1100 breakpoint up", () => {
    expect(reviewLayout(1099)).toBe("switch");
    expect(reviewLayout(1100)).toBe("split");
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

describe("business display time", () => {
  it("formats UTC timestamps in the fixed Asia/Shanghai zone", () => {
    expect(formatDateTime("2026-01-15T01:00:00.000Z")).toContain("2026/01/15 09:00");
    expect(formatDateTime("invalid-date")).toBe("时间格式异常");
    expect(formatDateTime("")).toBe("时间未记录");
  });
});
