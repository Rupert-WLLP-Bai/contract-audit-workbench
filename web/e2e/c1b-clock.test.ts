// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “C1-B frozen business time”; representative assertions cover produces the same Shanghai wall time and UTC instant on every host. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { C1B_BUSINESS_AT_UTC, C1B_BUSINESS_TIME_ZONE, c1bBusinessAtInput } from "./c1b-clock";

describe("C1-B frozen business time", () => {
  it("produces the same Shanghai wall time and UTC instant on every host", () => {
    assert.equal(C1B_BUSINESS_TIME_ZONE, "Asia/Shanghai");
    assert.equal(C1B_BUSINESS_AT_UTC, "2026-01-15T01:00:00.000Z");
    assert.equal(c1bBusinessAtInput(), "2026-01-15T09:00");
    assert.equal(c1bBusinessAtInput("UTC"), "2026-01-15T01:00");
  });
});
