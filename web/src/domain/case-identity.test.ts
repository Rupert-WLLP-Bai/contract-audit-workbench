// File metadata (manifest mirror):
// Purpose: Checks readable case identity and search for same-name or same-minute records and editable business numbers without replacing stable case IDs.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, it } from "vitest";
import type { CaseSummary } from "../api/types";
import { caseIdentityDetails, caseOptionLabel, caseSearchText } from "./case-identity";

const first: CaseSummary = {
  id: "case-full-first", title: "同名案件", contract_type: "service", case_number: "BUS-01",
  customer: "同一客户", supplier: "同一供应商", document_count: 0, pending_decisions: 0,
  latest_run: null, updated_at: "2026-11-05T01:02:03Z", scenario: "telecom_dedicated_line",
};

describe("case identity", () => {
  it("keeps same-name and same-minute records distinct without inventing a number", () => {
    const second = { ...first, id: "case-full-second", case_number: "BUS-02", contract_type: "framework", updated_at: "2026-11-05T01:02:04Z" };
    const legacy = { ...first, id: "case-full-legacy", case_number: "", customer: "", supplier: "" };
    expect(caseOptionLabel(first)).toContain("案件业务编号 BUS-01");
    expect(caseOptionLabel(second)).toContain("案件业务编号 BUS-02");
    expect(caseOptionLabel(second)).toContain("09:02:04");
    expect(caseOptionLabel(legacy)).toContain("业务编号未登记");
    expect(caseOptionLabel(legacy)).toContain("案件标识 case-full-legacy");
    expect(caseIdentityDetails({ ...legacy, updated_at: "invalid" })).toContain("更新时间 未记录");
    expect(caseSearchText(first)).toContain("政企专线");
  });

  it("changing the number changes presentation and search, never the selected value", () => {
    const renamed = { ...first, case_number: "BUS-99" };
    expect(caseOptionLabel(renamed)).toContain("BUS-99");
    expect(caseSearchText(renamed)).not.toContain("bus-01");
    expect(renamed.id).toBe(first.id);
  });
});
