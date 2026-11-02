// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “organizationName”; representative assertions cover names the demonstration organisation without hiding unknown scopes. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-10-28
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, it } from "vitest";
import { organizationName } from "./organization";

describe("organizationName", () => {
  it("names the demonstration organisation without hiding unknown scopes", () => {
    expect(organizationName("demo-org")).toBe("两江新区分公司");
    expect(organizationName("branch-sales")).toBe("branch-sales");
    expect(organizationName(null)).toBe("正在确认");
  });
});
